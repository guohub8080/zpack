// burn 命令——光盘刻录（设备枚举 + 镜像/目录刻录）。
//
// 平台矩阵（2026-11-22 调研定案；2026-11-24 用户定案：刻盘三个平台分别实现，各用该平台上最简单可靠的
// 做法，不强求同一个库——旗标与输出契约三平台一致，差异全收在后端里）：
//
//	darwin : hdiutil burn（mastered；默认不封盘=可追加，--close 才 -forceclose）；设备枚举 drutil list/status；
//	         目录先 hdiutil makehybrid -iso -joliet 产临时镜像再烧（长文件名走 Joliet）
//	linux  : xorriso（未安装报安装引导）；-devices 枚举；目录 -map 原生刻录（可追加默认）、
//	         镜像走 -as cdrecord -multi；--close 封盘；--blank blank=as_needed 按介质自动选擦除
//	windows: 系统自带引擎 IMAPI2（imapi2.dll 设备/写入/擦除 + imapi2fs.dll 目录→镜像；见 burn_windows.go 与
//	         internal/imapi2）；目录/散文件由系统镜像库流式生成后直烧、不经临时 ISO；--close 封盘；
//	         --blank 快速擦除可擦写盘；--speed 就近落到介质支持的档位；live（优盘模式）暂未接入
//
// 模式语义（用户痛点：Linux 默认 DAO 封盘连空白盘都只能烧一次）：
//
//	--mode multi（缺省）= 区段不封盘，烧完可继续追加；--close 显式封盘（最大兼容，老设备只认终章）
//	--mode mastered = 单区段定稿（等价 multi + 封盘）
//	--mode live = UDF 包写入「优盘模式」（各平台均暂未接入，如实报不支持）
package burn

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/cliutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// BurnDevice 刻录机条目（burn devices 输出契约）
type BurnDevice struct {
	Index   int    `json:"index"`
	Path    string `json:"path"`
	Vendor  string `json:"vendor,omitempty"`
	Product string `json:"product,omitempty"`
	Media   string `json:"media,omitempty"`  // 介质类型：CD-R / DVD+RW / BD-RE…（无介质空）
	Status  string `json:"status,omitempty"` // blank / appendable / full / no-media
	Note    string `json:"note,omitempty"`
}

// NewBurnCmd burn 命令：zpack burn devices | zpack burn <iso|目录> [旗标]
func NewBurnCmd() *cobra.Command {
	return &cobra.Command{
		Use: "burn devices | burn <镜像.iso|目录> [--device 0] [--speed 8] [--volid 卷标] [--mode multi|mastered|live] [--close] [--blank] [--no-eject]",
		Long: `光盘刻录。

示例:
  zpack burn devices                      # 枚举刻录机与介质状态
  zpack burn 镜像.iso                      # 烧镜像，默认多区段可追加
  zpack burn 目录/ --close                 # 目录直刻并封盘（最大兼容）
  zpack burn 镜像.iso --device 1 --speed 8 # 第二台刻录机，8x
  zpack burn 目录/ --blank                 # 先擦除（RW 介质）再烧
  zpack burn 目录/ --volid 备份            # 给光盘起名（空白盘；缺省用目录名，Windows 支持中文名）

模式: multi（缺省，区段可追加）/ mastered（单区段定稿）/ live（优盘式 UDF 包写入，暂未接入）。
光盘名称: 空白盘随意起；已有内容的盘缺省沿用原名称（显式 --volid 才改）；已封盘的不能改。
stdout 恒为一条 JSON：{"is_success":true,"devices":[...]} 或 {"is_success":true,"image":"...","size_bytes":N,...}。`,
		Short:              "刻录光盘（设备枚举 / 镜像或目录刻录，多区段默认）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { cliutil.HelpAwareRun(command, args, runBurn) },
	}
}

// runBurn 扁平分派：burn devices / burn <路径> [旗标]
func runBurn(args []string) {
	if len(args) == 0 {
		stdout.ErrorPrint("用法: zpack burn devices | zpack burn <镜像.iso|目录> [旗标]")
	}
	if args[0] == "devices" {
		runBurnDevices()
	}
	runBurnWrite(args)
}

// runBurnDevices 三平台设备枚举；无刻录机=成功空列表（如实呈现）
func runBurnDevices() {
	devices := burnListDevices()
	stdout.JSONPrint(map[string]any{"devices": devices})
}

// burnListDevices 平台分派的设备枚举实现
func burnListDevices() []BurnDevice {
	switch runtimeOS() {
	case "darwin":
		return burnListDevicesDarwin()
	case "linux":
		return burnListDevicesLinux()
	case "windows":
		return burnListDevicesWindows()
	default:
		stdout.ErrorPrint(burnUnsupportedPlatform().Error())
	}
	return nil
}

// burnListDevicesDarwin drutil list 枚举 + drutil status 取默认驱动器介质
func burnListDevicesDarwin() []BurnDevice {
	out, err := exec.Command("drutil", "list").Output()
	if err != nil {
		return []BurnDevice{} // drutil 不存在（极端裁剪系统）→ 空列表
	}
	var devices []BurnDevice
	for _, line := range strings.Split(string(out), "\n")[1:] { // 首行表头
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		devices = append(devices, BurnDevice{
			Index:   len(devices),
			Vendor:  fields[0],
			Product: strings.Join(fields[1:len(fields)-2], " "),
			Note:    "macOS 经 hdiutil 使用默认刻录设备",
		})
	}
	// drutil status 的介质信息归第一台（典型 mac 单光驱）
	if status, serr := exec.Command("drutil", "status").Output(); serr == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, " Type:") || strings.HasPrefix(line, "Type:") {
				media := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Type:"))
				if len(devices) > 0 {
					if strings.Contains(media, "No Media") {
						devices[0].Status = "no-media"
					} else if media != "" {
						devices[0].Media = media
					}
				}
				break
			}
		}
	}
	if devices == nil {
		devices = []BurnDevice{}
	}
	return devices
}

// BURN_XORRISO_DEVICE_LINE xorriso -devices 行：`0 -dev '/dev/sr0' rwrw-- : 'HL-DT-ST' 'BDDVDRW GGC-H20L'`
var BURN_XORRISO_DEVICE_LINE = regexp.MustCompile(`^(\d+)\s+-dev\s+'([^']+)'\s+\S+\s+:\s+'([^']*)'\s+'([^']*)'`)

// BURN_XORRISO_MEDIA_STATUS xorriso -toc 摘要行：`Media status : is blank` / `is written , is appendable`
var BURN_XORRISO_MEDIA_STATUS = regexp.MustCompile(`Media status\s*:\s*(.+)$`)
var BURN_XORRISO_MEDIA_CURRENT = regexp.MustCompile(`Media current\s*:\s*(.+)$`)

// burnListDevicesLinux xorriso -devices 枚举 + 逐台 -toc 取介质状态
func burnListDevicesLinux() []BurnDevice {
	if _, err := exec.LookPath("xorriso"); err != nil {
		stdout.ErrorPrint("未找到 xorriso（Linux 刻录引擎）——请安装：sudo apt install xorriso / sudo dnf install xorriso / sudo pacman -S xorriso")
	}
	out, err := exec.Command("xorriso", "-devices").CombinedOutput()
	if err != nil {
		stdout.ErrorPrint("xorriso -devices 失败: " + strings.TrimSpace(string(out)))
	}
	var devices []BurnDevice
	for _, line := range strings.Split(string(out), "\n") {
		match := BURN_XORRISO_DEVICE_LINE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		device := BurnDevice{Index: len(devices), Path: match[2], Vendor: match[3], Product: match[4]}
		// 逐台快查介质（无盘/查询失败留空，不阻塞枚举）
		if toc, terr := exec.Command("xorriso", "-dev", match[2], "-toc").CombinedOutput(); terr == nil {
			for _, tocLine := range strings.Split(string(toc), "\n") {
				if current := BURN_XORRISO_MEDIA_CURRENT.FindStringSubmatch(tocLine); current != nil {
					device.Media = strings.TrimSpace(current[1])
				}
				if status := BURN_XORRISO_MEDIA_STATUS.FindStringSubmatch(tocLine); status != nil {
					device.Status = burnNormalizeXorrisoStatus(strings.TrimSpace(status[1]))
				}
			}
		}
		devices = append(devices, device)
	}
	if devices == nil {
		devices = []BurnDevice{}
	}
	return devices
}

// burnNormalizeXorrisoStatus xorriso 状态短语 → blank/appendable/full/no-media
func burnNormalizeXorrisoStatus(raw string) string {
	switch {
	case strings.Contains(raw, "blank"):
		return "blank"
	case strings.Contains(raw, "appendable"):
		return "appendable"
	case strings.Contains(raw, "is not appendable"):
		return "full"
	case strings.Contains(raw, "not present"):
		return "no-media"
	default:
		return raw
	}
}

// runBurnWrite 刻录主流程：旗标解析 → 输入形态判定 → 平台分派
func runBurnWrite(args []string) {
	// 多输入定案（2026-11-22 用户）：像 compress 一样任意数量文件/文件夹——
	// 单 .iso 镜像直烧；单目录产 ISO；多输入/散文件先聚成临时舞台目录再产 ISO
	inputs := []string{}
	rest := args
	for idx, token := range args {
		if strings.HasPrefix(token, "-") {
			rest = args[idx:]
			break
		}
		inputs = append(inputs, token)
		rest = args[idx+1:]
	}
	flags := burnParseFlags(rest)
	if len(inputs) == 0 {
		stdout.ErrorPrint("用法: zpack burn <镜像.iso|目录|文件...> [旗标]")
	}

	var totalBytes int64
	imageInput := false
	for _, inputPath := range inputs {
		info, statErr := os.Stat(inputPath)
		if statErr != nil {
			stdout.ErrorPrint("输入不存在: " + inputPath)
		}
		if info.IsDir() {
			continue
		}
		totalBytes += info.Size()
		if burnLooksLikeImage(inputPath) {
			imageInput = true
		}
	}
	if len(inputs) > 1 && imageInput {
		stdout.ErrorPrint("镜像文件（.iso/.img/.bin）只能单独刻录，不能与其他输入混排")
	}
	if len(inputs) == 1 && !imageInput {
		if info, statErr := os.Stat(inputs[0]); statErr == nil && !info.IsDir() {
			stdout.ErrorPrint("不识别的镜像扩展名（支持 .iso/.img/.bin；文件/文件夹请与其他内容一起多输入）: " + inputs[0])
		}
	}
	// 无刻录后端的平台在任何暂存动作之前拒绝——此前目录/多输入会先走镜像生成，
	// 报出空原因的「生成临时镜像失败」且把舞台目录遗留在用户目录（2026-11-23 Windows 实测）
	if unsupported := burnUnsupportedPlatform(); unsupported != nil {
		stdout.ErrorPrint(unsupported.Error())
	}
	flags.volid = burnResolveVolumeName(inputs, flags)
	if flags.mode == "live" {
		stdout.ErrorPrint(burnLiveUnsupported())
	}

	imagePath := inputs[0]
	var burnErr error
	if runtimeOS() == "windows" {
		// Windows：系统镜像库流式生成、直烧，不经临时 ISO（见 burn_windows.go）
		totalBytes, burnErr = burnWindows(inputs, flags)
	} else {
		imagePath, burnErr = burnViaTempISO(inputs, flags)
	}
	if burnErr != nil {
		stdout.ErrorPrint(burnErr.Error())
	}

	if totalBytes == 0 {
		if info, statErr := os.Stat(inputs[0]); statErr == nil {
			totalBytes = info.Size()
		}
	}
	stdout.JSONPrint(map[string]any{
		"image":      mustAbs(imagePath),
		"size_bytes": totalBytes,
		"mode":       flags.mode,
		"close":      flags.close,
		"blank":      flags.blank,
	})
}

// burnViaTempISO mac / Linux 的刻录路径：目录 / 散文件先经外部工具产临时 ISO 再烧（用完即删）。
// 返回临时镜像路径（返回时已清理）与刻录错误
func burnViaTempISO(inputs []string, flags burnFlags) (string, error) {
	imagePath := inputs[0]
	cleanup := func() {}
	isStaged := false
	var stageErr error
	if len(inputs) == 1 {
		if info, _ := os.Stat(inputs[0]); info != nil && info.IsDir() {
			imagePath, cleanup, stageErr = burnStageISO(inputs[0], flags, false)
			isStaged = true
		}
	} else {
		imagePath, cleanup, stageErr = burnStageInputs(inputs, flags, burnStageISO)
		isStaged = true
	}
	if stageErr != nil {
		cleanup()
		return imagePath, stageErr
	}
	var burnErr error
	switch runtimeOS() {
	case "darwin":
		burnErr = burnDarwin(imagePath, flags)
	case "linux":
		burnErr = burnLinux(imagePath, isStaged, flags)
	}
	cleanup() // 显式清理临时镜像（print 即 exit，defer 不执行）
	return imagePath, burnErr
}

// burnUniqueEntryNames 多输入的顶层条目名（按输入顺序；重名加 -2 / -3 序号）——
// mac/Linux 的舞台挂载与 Windows 的镜像构建共用
func burnUniqueEntryNames(inputs []string) []string {
	used := map[string]bool{}
	entryNames := make([]string, len(inputs))
	for index, input := range inputs {
		baseName := filepath.Base(input)
		entryName := baseName
		for suffix := 2; used[entryName]; suffix++ {
			// 每次从原名起算——否则第三个重名会叠成 a-2-3.txt
			ext := filepath.Ext(baseName)
			entryName = strings.TrimSuffix(baseName, ext) + "-" + strconv.Itoa(suffix) + ext
		}
		used[entryName] = true
		entryNames[index] = entryName
	}
	return entryNames
}

// BURN_SIZE_UNITS 容量换算单位（1024 进制）
var BURN_SIZE_UNITS = []string{"B", "KB", "MB", "GB", "TB"}

// burnFormatBytes 人读容量：整数位不带小数（3GB / 5MB），否则一位小数（2.3MB），数字与单位之间不留空格
// （与 GUI 的 humanSize 同约定——两个模块各自一份，逻辑一致）
func burnFormatBytes(bytes int64) string {
	value := float64(bytes)
	unitIdx := 0
	for value >= 1024 && unitIdx < len(BURN_SIZE_UNITS)-1 {
		value /= 1024
		unitIdx++
	}
	if unitIdx == 0 {
		return strconv.FormatInt(bytes, 10) + BURN_SIZE_UNITS[0]
	}
	// 先按一位小数取整；取整后进到下一档（1023.96KB → 1024.0KB → 1MB）
	rounded := math.Round(value*10) / 10
	for rounded >= 1024 && unitIdx < len(BURN_SIZE_UNITS)-1 {
		rounded /= 1024
		unitIdx++
	}
	if rounded == math.Trunc(rounded) {
		return strconv.FormatInt(int64(rounded), 10) + BURN_SIZE_UNITS[unitIdx]
	}
	return strconv.FormatFloat(rounded, 'f', 1, 64) + BURN_SIZE_UNITS[unitIdx]
}

// burnStageInputs 多输入聚台（2026-11-22 用户定案：像压缩包一样任意数量）：
// 在输入们的公共祖先目录里建临时舞台（同卷——硬链接零拷贝），逐项挂进舞台：
// darwin=硬链接（跨卷回退拷贝）；linux=符号链接（mkisofs -f 跟随）。
// 目录输入按「目录名为根」语义整树挂入（同 compress）。返回镜像路径与清理函数；
// 任一环节失败自行清场并返回 error（不在此 ErrorPrint——print 即 exit 会让调用方无从清理）。
// buildISO 为镜像生成器（测试注入——无 hdiutil/xorriso 的平台也能验证舞台生命周期）。
func burnStageInputs(inputs []string, flags burnFlags, buildISO func(string, burnFlags, bool) (string, func(), error)) (string, func(), error) {
	ancestor := burnCommonAncestor(inputs)
	stageDir, err := os.MkdirTemp(ancestor, ".zpack-burn-stage-")
	if err != nil {
		// 公共祖先不可写（只读卷等）→ 回退系统临时目录（此时硬链接多会跨卷，逐文件回退拷贝）
		stageDir, err = os.MkdirTemp("", "zpack-burn-stage-")
		if err != nil {
			return "", func() {}, fmt.Errorf("创建刻录暂存目录失败: %w", err)
		}
	}
	entryNames := burnUniqueEntryNames(inputs)
	for index, input := range inputs {
		target := filepath.Join(stageDir, entryNames[index])
		if mountErr := burnLinkEntry(input, target); mountErr != nil {
			_ = os.RemoveAll(stageDir)
			return "", func() {}, fmt.Errorf("暂存刻录内容失败: %w", mountErr)
		}
	}
	imagePath, isoCleanup, isoErr := buildISO(stageDir, flags, runtimeOS() == "linux")
	if isoErr != nil {
		// 舞台目录只有本函数知道——镜像生成失败时归这里清，否则连硬链接一起遗留在用户目录
		_ = os.RemoveAll(stageDir)
		return "", func() {}, isoErr
	}
	return imagePath, func() {
		isoCleanup()
		_ = os.RemoveAll(stageDir)
	}, nil
}

// burnCommonAncestor 取输入们的最长公共目录（同卷硬链接的前提；单输入退化为父目录）
func burnCommonAncestor(inputs []string) string {
	if len(inputs) == 0 {
		return mustAbs(".")
	}
	common := filepath.Dir(mustAbs(inputs[0]))
	for _, input := range inputs[1:] {
		dir := filepath.Dir(mustAbs(input))
		for dir != common && common != "/" && dir != "/" {
			dir = filepath.Dir(dir)
			if len(dir) < len(common) {
				common = filepath.Dir(common)
			}
		}
	}
	if common == "" || common == "." {
		common = mustAbs(".")
	}
	return common
}

// burnLinkEntry 把一个输入挂进舞台：文件=硬链接（darwin/linux）优先、失败回退拷贝；
// 目录=darwin 逐文件硬链接克隆树（跨卷文件回退拷贝）/ linux 符号链接（mkisofs -f 跟随）
func burnLinkEntry(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		if linkErr := os.Link(source, target); linkErr == nil {
			return nil
		}
		return burnCopyFile(source, target)
	}
	if runtimeOS() == "linux" {
		return os.Symlink(source, target)
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil // 符号链接/设备等不进盘（同 compress 收集语义）
		}
		if linkErr := os.Link(path, destination); linkErr == nil {
			return nil
		}
		return burnCopyFile(path, destination)
	})
}

// burnCopyFile 硬链接失败（跨卷/权限）时的拷贝兜底
func burnCopyFile(source, target string) error {
	if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o755); mkdirErr != nil {
		return mkdirErr
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	targetFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer targetFile.Close()
	_, err = io.Copy(targetFile, sourceFile)
	return err
}

// burnFlags 刻录旗标集
type burnFlags struct {
	device     int
	speed      int
	volid      string
	volidGiven bool // 是否显式给了 --volid（Windows 追加区段时未给则沿用盘上原有卷标）
	mode       string
	close      bool
	blank      bool
	noEject    bool
}

// burnParseFlags 手写旗标解析（同仓惯例：未知静默忽略、数值严格校验）
func burnParseFlags(rest []string) burnFlags {
	flags := burnFlags{device: 0, speed: 0, mode: "multi"}
	for idx := 0; idx < len(rest); idx++ {
		token := rest[idx]
		value := func() (string, bool) {
			if idx+1 < len(rest) && !strings.HasPrefix(rest[idx+1], "-") {
				idx++
				return rest[idx], true
			}
			return "", false
		}
		switch token {
		case "--device":
			if v, ok := value(); ok {
				num, err := strconv.Atoi(v)
				if err != nil || num < 0 {
					stdout.ErrorPrint("--device 需要 0 起的设备序号（见 zpack burn devices），收到: " + v)
				}
				flags.device = num
			}
		case "--speed":
			if v, ok := value(); ok {
				num, err := strconv.Atoi(v)
				if err != nil || num < 1 {
					stdout.ErrorPrint("--speed 需要正整数（如 8），收到: " + v)
				}
				flags.speed = num
			}
		case "--volid":
			if v, ok := value(); ok {
				flags.volid = v
				flags.volidGiven = true
			}
		case "--mode":
			if v, ok := value(); ok {
				if v != "multi" && v != "mastered" && v != "live" {
					stdout.ErrorPrint("--mode 取值 multi / mastered / live，收到: " + v)
				}
				flags.mode = v
			}
		case "--close":
			flags.close = true
		case "--blank":
			flags.blank = true
		case "--no-eject":
			flags.noEject = true
		}
	}
	if flags.mode == "mastered" {
		flags.close = true // mastered=单区段定稿
	}
	return flags // 未给 --volid 时留空：缺省卷标取决于输入形态，由 runBurnWrite 经 burnResolveVolumeName 定
}

// burnResolveVolumeName 定下本次使用的卷标。未给 --volid：单个目录输入用目录名（2026-11-24 用户定案），
// 其余形态沿用旧规则取当前目录名。显式给了：Windows 做温和规范化（见 burn_windows.go），
// mac/Linux 原样交给外部工具（旧行为）。追加区段未给 --volid 时沿用盘上名称由 Windows 后端按 volidGiven 处理
func burnResolveVolumeName(inputs []string, flags burnFlags) string {
	if flags.volidGiven {
		if runtimeOS() == "windows" {
			return burnWindowsVolumeName(flags.volid)
		}
		return flags.volid
	}
	if len(inputs) == 1 {
		if info, statErr := os.Stat(inputs[0]); statErr == nil && info.IsDir() {
			return burnPlatformVolumeName(filepath.Base(mustAbs(inputs[0])))
		}
	}
	return burnPlatformVolumeName(filepath.Base(mustAbs(".")))
}

// burnPlatformVolumeName 缺省卷标的平台规范化：Windows 允许中文名（系统镜像库原生支持）；
// mac/Linux 只保留 A-Z 0-9 下划线（外部工具的字符集限制）
func burnPlatformVolumeName(raw string) string {
	if runtimeOS() == "windows" {
		return burnWindowsVolumeName(raw)
	}
	return burnSanitizeVolid(raw)
}

// burnSanitizeVolid ISO9660 卷标约束：A-Z0-9_、≤32 字符
func burnSanitizeVolid(raw string) string {
	upper := strings.ToUpper(raw)
	var builder strings.Builder
	for _, char := range upper {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			builder.WriteRune(char)
		}
	}
	label := strings.Trim(builder.String(), "_")
	if label == "" {
		label = "ZPACK"
	}
	if len(label) > 32 {
		label = label[:32]
	}
	return label
}

// burnLooksLikeImage 镜像扩展名白名单（目录另走 staging）
func burnLooksLikeImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".iso", ".img", ".bin":
		return true
	}
	return false
}

// burnStageISO 目录 → 临时 ISO 镜像（返回清理函数）。失败返回 error 而非 ErrorPrint：
// print 即 exit 会跳过调用方的善后（多输入舞台目录曾因此泄漏，2026-11-23 Windows 实测）
func burnStageISO(dir string, flags burnFlags, followSymlinks bool) (string, func(), error) {
	staged := filepath.Join(os.TempDir(), "zpack-burn-"+strconv.Itoa(os.Getpid())+".iso")
	volid := flags.volid
	if volid == "" {
		volid = burnSanitizeVolid(filepath.Base(mustAbs(dir)))
	}
	imageTool := "xorriso"
	if runtimeOS() == "darwin" {
		imageTool = "hdiutil"
	}
	// 引擎缺席先点名——否则调用方只拿到冒号后空白的「生成临时镜像失败: 」
	if _, lookErr := exec.LookPath(imageTool); lookErr != nil {
		missing := "未找到 " + imageTool + "（" + runtimeOS() + " 侧镜像生成引擎）"
		if imageTool == "xorriso" {
			missing += "——请安装：sudo apt install xorriso / sudo dnf install xorriso / sudo pacman -S xorriso"
		}
		return "", func() {}, errors.New(missing)
	}
	var out []byte
	var err error
	if runtimeOS() == "darwin" {
		out, err = exec.Command("hdiutil", "makehybrid", "-iso", "-joliet", "-ov", "-o", staged, dir).CombinedOutput()
	} else {
		mkisofsArgs := []string{"-as", "mkisofs", "-volid", volid}
		if followSymlinks {
			mkisofsArgs = append(mkisofsArgs, "-f")
		}
		mkisofsArgs = append(mkisofsArgs, "-joliet", "-rational-rock", "-o", staged, dir)
		out, err = exec.Command("xorriso", mkisofsArgs...).CombinedOutput()
	}
	if err != nil {
		return "", func() {}, fmt.Errorf("生成临时镜像失败: %s", strings.TrimSpace(string(out)))
	}
	return staged, func() { _ = os.Remove(staged) }, nil
}

// burnDarwin hdiutil 刻录（默认不封盘=可追加；--close 才 -forceclose）
func burnDarwin(imagePath string, flags burnFlags) error {
	if flags.device > 0 {
		stdout.ErrorPrint("macOS 多刻录机寻址（-device OpenFirmware 路径）M2 交付；当前用默认设备（--device 0）")
	}
	if flags.blank {
		if out, err := exec.Command("drutil", "erase", "quick").CombinedOutput(); err != nil {
			stdout.ErrorPrint("擦除失败（介质可能不可擦）: " + strings.TrimSpace(string(out)))
		}
	}
	arguments := []string{"burn", imagePath}
	if flags.speed > 0 {
		arguments = append(arguments, "-speed", strconv.Itoa(flags.speed))
	}
	if flags.close {
		arguments = append(arguments, "-forceclose")
	}
	if flags.noEject {
		arguments = append(arguments, "-noeject")
	}
	return burnRunCommand("hdiutil", arguments)
}

// burnLinux xorriso 刻录（镜像 -as cdrecord / 目录已 staged 成镜像）
func burnLinux(imagePath string, stagedFromDir bool, flags burnFlags) error {
	if _, err := exec.LookPath("xorriso"); err != nil {
		stdout.ErrorPrint("未找到 xorriso（Linux 刻录引擎）——请安装：sudo apt install xorriso")
	}
	devices := burnListDevicesLinux()
	if flags.device >= len(devices) {
		stdout.ErrorPrint(fmt.Sprintf("--device %d 超出范围（共 %d 台，见 zpack burn devices）", flags.device, len(devices)))
	}
	devicePath := devices[flags.device].Path

	arguments := []string{"-as", "cdrecord", "-v", "dev=" + devicePath}
	if flags.speed > 0 {
		arguments = append(arguments, fmt.Sprintf("speed=%d", flags.speed))
	}
	if flags.blank {
		arguments = append(arguments, "blank=as_needed")
	}
	if !flags.close {
		arguments = append(arguments, "-multi") // 可追加（用户痛点：默认不封盘）
	}
	if !flags.noEject {
		arguments = append(arguments, "-eject")
	}
	arguments = append(arguments, imagePath)
	return burnRunCommand("xorriso", arguments)
}

// burnRunCommand 子进程执行：stdout 进程日志走 stderr 契约通道、stderr 原样透传；error 由调用方收敛
// （不在此处 ErrorPrint——print 即 exit 会跳过调用方 defer 的临时镜像清理）
func burnRunCommand(name string, arguments []string) error {
	stdout.LoggerInfo(strings.Join(append([]string{name}, arguments...), " "))
	command := exec.Command(name, arguments...)
	command.Stderr = os.Stderr
	output, err := command.Output()
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			stdout.LoggerInfo(line)
		}
	}
	if err != nil {
		return fmt.Errorf("%s 刻录失败: %s", name, err.Error())
	}
	return nil
}

// runtimeOS 目标平台名（burn 内部用）
func runtimeOS() string { return runtime.GOOS }

// burnUnsupportedPlatform 无刻录后端平台的拒绝原因（nil=平台受支持）
func burnUnsupportedPlatform() error {
	switch runtimeOS() {
	case "darwin", "linux", "windows":
		return nil
	}
	return fmt.Errorf("暂不支持 %s：刻录引擎仅 darwin=hdiutil / linux=xorriso / windows=IMAPI2", runtimeOS())
}

// burnLiveUnsupported 优盘模式的拒绝原因（各平台均未接入）
func burnLiveUnsupported() string {
	return "暂不支持 live：优盘模式（UDF 包写入）尚未接入，请用 multi（多区段追加）或 mastered（封盘）"
}

// mustAbs 绝对路径（失败即原样返回）
func mustAbs(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
