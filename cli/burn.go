// Package cli: burn 命令——光盘刻录（设备枚举 + 镜像/目录刻录）。
//
// 平台矩阵（2026-11-22 调研定案）：
//
//	darwin : hdiutil burn（mastered；默认不封盘=可追加，--close 才 -forceclose）；设备枚举 drutil list/status；
//	         目录先 hdiutil makehybrid -iso -joliet 产临时镜像再烧（长文件名走 Joliet）
//	linux  : xorriso（未安装报安装引导）；-devices 枚举；目录 -map 原生刻录（可追加默认）、
//	         镜像走 -as cdrecord -multi；--close 封盘；--blank blank=as_needed 按介质自动选擦除
//	windows: M1 暂不支持（M2 交 IMAPI2：mastered=IDiscFormat2Data / 优盘模式=IDiscFormat2RandomWrite）
//
// 模式语义（用户痛点：Linux 默认 DAO 封盘连空白盘都只能烧一次）：
//
//	--mode multi（缺省）= 区段不封盘，烧完可继续追加；--close 显式封盘（最大兼容，老设备只认终章）
//	--mode mastered = 单区段定稿（等价 multi + 封盘）
//	--mode live = UDF 包写入「优盘模式」（Windows M2；其余平台如实报不支持）
package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

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

模式: multi（缺省，区段可追加）/ mastered（单区段定稿）/ live（优盘式 UDF 包写入，Windows M2）。
stdout 恒为一条 JSON：{"is_success":true,"devices":[...]} 或 {"is_success":true,"image":"...","size_bytes":N,...}。`,
		Short:              "刻录光盘（设备枚举 / 镜像或目录刻录，多区段默认）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runBurn) },
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
	default:
		// windows: M2 交 IMAPI2（IDiscMaster2 枚举）——如实报暂不支持
		stdout.ErrorPrint("暂不支持 windows：刻录设备枚举走 IMAPI2，M2 交付（mastered=IDiscFormat2Data；优盘模式=IDiscFormat2RandomWrite）")
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
	if flags.mode == "live" {
		stdout.ErrorPrint("优盘模式（UDF 包写入）仅 Windows 计划支持（M2 交付）；当前平台请用 multi（多区段追加）")
	}

	imagePath := inputs[0]
	cleanup := func() {}
	isStaged := false
	if len(inputs) == 1 {
		if info, _ := os.Stat(inputs[0]); info != nil && info.IsDir() {
			imagePath, cleanup = burnStageISO(inputs[0], flags, false)
			isStaged = true
		}
	} else {
		imagePath, cleanup = burnStageInputs(inputs, flags)
		isStaged = true
	}
	var burnErr error
	switch runtimeOS() {
	case "darwin":
		burnErr = burnDarwin(imagePath, flags)
	case "linux":
		burnErr = burnLinux(imagePath, isStaged, flags)
	default:
		stdout.ErrorPrint("暂不支持 windows：刻录走 IMAPI2（mastered=IDiscFormat2Data 流式写；优盘模式=IDiscFormat2RandomWrite 包写入），M2 交付")
	}
	cleanup() // 显式清理临时镜像（ErrorPrint 即 exit，defer 不执行）
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

// burnStageInputs 多输入聚台（2026-11-22 用户定案：像压缩包一样任意数量）：
// 在输入们的公共祖先目录里建临时舞台（同卷——硬链接零拷贝），逐项挂进舞台：
// darwin=硬链接（跨卷回退拷贝）；linux=符号链接（mkisofs -f 跟随）。
// 返回舞台目录与清理函数；目录输入按「目录名为根」语义整树挂入（同 compress）。
func burnStageInputs(inputs []string, flags burnFlags) (string, func()) {
	ancestor := burnCommonAncestor(inputs)
	stageDir, err := os.MkdirTemp(ancestor, ".zpack-burn-stage-")
	if err != nil {
		// 公共祖先不可写（只读卷等）→ 回退系统临时目录（此时硬链接多会跨卷，逐文件回退拷贝）
		stageDir, err = os.MkdirTemp("", "zpack-burn-stage-")
		if err != nil {
			stdout.ErrorPrint("创建刻录暂存目录失败: " + err.Error())
		}
	}
	used := map[string]bool{}
	for _, input := range inputs {
		entryName := filepath.Base(input)
		for suffix := 2; used[entryName]; suffix++ {
			ext := filepath.Ext(entryName)
			entryName = strings.TrimSuffix(entryName, ext) + "-" + strconv.Itoa(suffix) + ext
		}
		used[entryName] = true
		target := filepath.Join(stageDir, entryName)
		if mountErr := burnLinkEntry(input, target); mountErr != nil {
			_ = os.RemoveAll(stageDir)
			stdout.ErrorPrint("暂存刻录内容失败: " + mountErr.Error())
		}
	}
	imagePath, isoCleanup := burnStageISO(stageDir, flags, runtimeOS() == "linux")
	return imagePath, func() {
		isoCleanup()
		_ = os.RemoveAll(stageDir)
	}
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
	device  int
	speed   int
	volid   string
	mode    string
	close   bool
	blank   bool
	noEject bool
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
	if flags.volid == "" {
		base := filepath.Base(mustAbs("."))
		flags.volid = burnSanitizeVolid(base)
	}
	return flags
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

// burnStageISO 目录 → 临时 ISO 镜像（返回清理函数）
func burnStageISO(dir string, flags burnFlags, followSymlinks bool) (string, func()) {
	staged := filepath.Join(os.TempDir(), "zpack-burn-"+strconv.Itoa(os.Getpid())+".iso")
	volid := flags.volid
	if volid == "" {
		volid = burnSanitizeVolid(filepath.Base(mustAbs(dir)))
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
		stdout.ErrorPrint("生成临时镜像失败: " + strings.TrimSpace(string(out)))
	}
	return staged, func() { _ = os.Remove(staged) }
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

// mustAbs 绝对路径（失败即原样返回）
func mustAbs(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
