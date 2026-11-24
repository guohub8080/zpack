// burn 命令——光盘刻录（设备枚举 + 镜像/目录刻录）。
//
// 平台矩阵（2026-11-22 调研定案；2026-11-24 用户定案：刻盘三个平台分别实现，各用该平台上最简单可靠的
// 做法，不强求同一个库——旗标与输出契约三平台一致，差异全收在后端里）：
//
//	darwin : hdiutil burn（默认不封盘，--close 才 -forceclose）；设备枚举 drutil list/status；
//	         目录先 hdiutil makehybrid -iso -joliet 产临时镜像再烧（长文件名走 Joliet）。
//	         hdiutil 没有并入上一区段的能力：每次刻录是独立的区段，新视图里通常只有这次刻入的内容
//	linux  : xorriso（未安装报安装引导）；-devices 枚举；目录 / 多输入走原生增量（-dev 读入上一区段、
//	         -map 加内容、-commit 写新区段——旧文件在新视图里保持可见，不落临时 ISO）；
//	         镜像走 -as cdrecord -multi；--close 封盘；--blank blank=as_needed / -blank as_needed 按介质自动选擦除
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
	"bytes"
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
		Use: "burn devices | burn <镜像.iso|目录|文件...> [--device 0] [--speed 8] [--volid 卷标] [--mode multi|mastered|live] [--close] [--blank] [--no-eject]",
		Long: `光盘刻录。

示例:
  zpack burn devices                      # 枚举刻录机与介质状态
  zpack burn 镜像.iso                      # 烧镜像，默认多区段可追加
  zpack burn 目录/ --close                 # 目录直刻并封盘（最大兼容）
  zpack burn 文件a.txt 文件b.txt 资料夹/    # 多输入：每项按名字装进光盘根目录
  zpack burn 镜像.iso --device 1 --speed 8 # 第二台刻录机，8x
  zpack burn 目录/ --blank                 # 先擦除（RW 介质）再烧
  zpack burn 目录/ --volid 备份            # 给光盘起名（空白盘；缺省用目录名，Windows 支持中文名）

模式: multi（缺省，区段可追加）/ mastered（单区段定稿）/ live（优盘式 UDF 包写入，暂未接入）。
可追加: Windows / Linux 上往有数据的盘再刻目录或文件，新内容并入原有文件；镜像不并入（会先警告）。
        macOS 的两次刻录互不相干，已有内容的盘通常只看得到最后刻入的内容。
光盘名称: 空白盘随意起；已有内容的盘缺省沿用原名称（显式 --volid 才改）；已封盘的不能改。
stdout 恒为一条 JSON：成功 {"is_success":true,"data":{"devices":[...]}} 或
{"is_success":true,"data":{"image":"<第一个输入>","size_bytes":N,"mode":"multi","close":false,"blank":false}}；失败 {"is_success":false,"error_info":"..."}。`,
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

	imageInput := false
	for _, inputPath := range inputs {
		info, statErr := os.Stat(inputPath)
		if statErr != nil {
			stdout.ErrorPrint("输入不存在: " + inputPath)
		}
		if !info.IsDir() && burnLooksLikeImage(inputPath) {
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

	// size_bytes 三个平台同义（2026-11-24 用户拍板：统一）：本次写入光盘的字节数
	var writtenBytes int64
	var burnErr error
	if runtimeOS() == "windows" {
		// Windows：系统镜像库流式生成、直烧，不经临时 ISO（见 burn_windows.go）
		writtenBytes, burnErr = burnWindows(inputs, flags)
	} else {
		writtenBytes, burnErr = burnUnix(inputs, flags)
	}
	if burnErr != nil {
		stdout.ErrorPrint(burnErr.Error())
	}
	stdout.JSONPrint(burnWriteResult{
		Image:     mustAbs(inputs[0]), // image 三个平台同义：第一个输入的绝对路径（目录输入就是那个目录，不是镜像文件）
		SizeBytes: writtenBytes,
		Mode:      flags.mode,
		Close:     flags.close,
		Blank:     flags.blank,
	})
}

// burnWriteResult 刻录成功信封的 data（字段序 = 声明序 = 输出序）
type burnWriteResult struct {
	Image     string `json:"image"`
	SizeBytes int64  `json:"size_bytes"`
	Mode      string `json:"mode"`
	Close     bool   `json:"close"`
	Blank     bool   `json:"blank"`
}

// BURN_SECTOR_BYTES 光盘数据扇区字节数（xorriso 的会话日志按它计）
const BURN_SECTOR_BYTES int64 = 2048

// burnUnix macOS / Linux 的刻录路径，返回本次写入的字节数：
//   - 镜像输入：原样直烧，写入量 = 镜像文件大小。
//   - 目录 / 多输入：Linux 走 xorriso 原生增量（2026-11-24 用户拍板「原生能做到是什么就是什么」）——
//     -dev 读得到上一区段就并入，旧文件在新视图里保持可见，也不落临时 ISO；写入量取 xorriso 的会话日志。
//     macOS 的 hdiutil 没有并入上一区段的能力，只能先生成临时 ISO 再烧；写入量 = 临时 ISO 的大小。
//
// 这一层一律返回 error、不 ErrorPrint——print 即 exit 会跳过调用方的清理（临时 ISO、舞台目录、会话日志）
func burnUnix(inputs []string, flags burnFlags) (int64, error) {
	if len(inputs) == 1 {
		if info, statErr := os.Stat(inputs[0]); statErr == nil && !info.IsDir() {
			return burnUnixImage(inputs, flags) // 入口已保证：单个的非目录输入只能是镜像
		}
	}
	if runtimeOS() == "linux" {
		return burnLinuxTree(inputs, flags)
	}
	return burnDarwinTree(inputs, flags)
}

// burnUnixImage 镜像输入的原样直烧（macOS / Linux）
func burnUnixImage(inputs []string, flags burnFlags) (int64, error) {
	imagePath := inputs[0]
	if runtimeOS() == "darwin" {
		sizeBytes, sizeErr := burnImageSize(imagePath)
		if sizeErr != nil {
			return 0, sizeErr
		}
		return sizeBytes, burnDarwinImage(imagePath, flags)
	}
	device, deviceErr := burnLinuxDevice(flags.device)
	if deviceErr != nil {
		return 0, deviceErr
	}
	if refusal := burnStatusRefusal(device.Status); refusal != nil {
		return 0, refusal
	}
	// Linux 读得到介质状态：与 Windows 一样，往有数据的盘写镜像先警告再允许（见 4.1.3）
	if warning := burnImageOnUsedDiscWarning(inputs, device.Status == "appendable" && !flags.blank); warning != "" {
		stdout.LoggerWarn(warning)
	}
	sizeBytes, sizeErr := burnImageSize(imagePath)
	if sizeErr != nil {
		return 0, sizeErr
	}
	return sizeBytes, burnLinuxImage(imagePath, device.Path, flags)
}

// burnImageSize 镜像文件的大小——原样直烧时它就是写入的字节数
func burnImageSize(imagePath string) (int64, error) {
	info, err := os.Stat(imagePath)
	if err != nil {
		return 0, fmt.Errorf("读取镜像大小失败: %w", err)
	}
	return info.Size(), nil
}

// BURN_IMAGE_ON_USED_DISC_WARNING 往有数据的盘写镜像时的警告文案。镜像是原样写进新区段的，不与盘上原有文件
// 合并——写入后新视图里通常只剩镜像自己的文件，原有文件没被擦掉，但一般的工具读不到。旧文件到底还看不看得见
// 没有实盘验证过，所以措辞留了余地（specs/burn.md 4.1.3、9）
const BURN_IMAGE_ON_USED_DISC_WARNING = "这张盘已有内容，而镜像是原样写进新区段的，不会与盘上原有文件合并——写入后电脑上通常只看得到镜像里的文件，原有文件虽没被擦掉，但一般工具读不到。要把新内容追加到原有文件之后，请改用目录或文件作为输入；要保留原有文件可见，请换一张空白盘"

// burnImageOnUsedDiscWarning 镜像输入遇到有数据的盘时的警告（空串=不用警告）。mediaUsed 由各平台按自己读得到
// 的介质状态给出；读不到就不警告（宁可不说，也不说一句可能是错的话）。
// 2026-11-24 用户拍板：警告后照常允许，不拦截——要拦的话，想故意这么刻的人就没法刻了
func burnImageOnUsedDiscWarning(inputs []string, mediaUsed bool) string {
	if len(inputs) != 1 || !burnLooksLikeImage(inputs[0]) || !mediaUsed {
		return ""
	}
	return BURN_IMAGE_ON_USED_DISC_WARNING
}

// burnStatusRefusal 按 burn devices 的 status 做写入前拒绝（读得到状态的平台用；可写返回 nil）。
// 文案与 Windows 同名状态的拒绝一致（specs/burn.md 4.3）。--blank 不改变结论：没有盘、已封盘都无从擦起
func burnStatusRefusal(status string) error {
	switch status {
	case "no-media":
		return errors.New("刻录机里没有光盘——请放入光盘后重试")
	case "full":
		return errors.New("光盘已封盘，无法继续写入")
	}
	return nil
}

// burnDarwinTree macOS 目录 / 多输入：先生成临时 ISO，再烧。hdiutil 没有并入上一区段的能力——
// 每次刻录都是一个独立的区段（只是不封盘），新视图里通常只有这次刻入的内容
func burnDarwinTree(inputs []string, flags burnFlags) (int64, error) {
	imagePath, cleanup, stageErr := burnDarwinStage(inputs, flags)
	if stageErr != nil {
		return 0, stageErr
	}
	sizeBytes, sizeErr := burnImageSize(imagePath)
	if sizeErr != nil {
		cleanup()
		return 0, sizeErr
	}
	stdout.LoggerInfo(BURN_DARWIN_NO_MERGE_NOTE)
	burnErr := burnDarwinImage(imagePath, flags)
	cleanup() // 显式清理临时镜像与舞台目录（print 即 exit，defer 不执行）
	return sizeBytes, burnErr
}

// BURN_DARWIN_NO_MERGE_NOTE macOS 刻录目录 / 多输入时的一句说明。macOS 读不到盘是不是空白，没法只在
// 「有数据的盘」时才警告，所以每次都提示、但不带 [WARN] 标签（conventions.md 第 4 节）
const BURN_DARWIN_NO_MERGE_NOTE = "提示：macOS 的 hdiutil 不会把新内容并入盘上原有的文件。这张盘若已有内容，写入后电脑上通常只看得到这次刻入的文件"

// burnDarwinStage macOS 的目录 / 多输入 → 临时 ISO：单个目录直接 makehybrid；多输入先聚舞台目录
func burnDarwinStage(inputs []string, flags burnFlags) (string, func(), error) {
	if len(inputs) == 1 {
		return burnStageISO(inputs[0], flags)
	}
	return burnStageInputs(inputs, flags, burnStageISO)
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
// darwin=硬链接（跨卷回退拷贝）；linux=符号链接（留给单测与将来可能的复用——Linux 的多输入
// 现在走 xorriso 原生 -map，不再聚台）。
// 目录输入按「目录名为根」语义整树挂入（同 compress）。返回镜像路径与清理函数；
// 任一环节失败自行清场并返回 error（不在此 ErrorPrint——print 即 exit 会让调用方无从清理）。
// buildISO 为镜像生成器（测试注入——无 hdiutil 的平台也能验证舞台生命周期）。
func burnStageInputs(inputs []string, flags burnFlags, buildISO func(string, burnFlags) (string, func(), error)) (string, func(), error) {
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
	imagePath, isoCleanup, isoErr := buildISO(stageDir, flags)
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
// mac/Linux 原样交给外部工具（旧行为）；显式给了空串时三个平台一致，回退 ZPACK（否则 Linux 上空白盘
// 会得到 xorriso 自己的缺省名 ISOIMAGE）。追加区段未给 --volid 时沿用盘上名称：Windows 后端按 volidGiven
// 处理，Linux 见 burnLinuxVolumeID
func burnResolveVolumeName(inputs []string, flags burnFlags) string {
	if flags.volidGiven {
		if runtimeOS() == "windows" {
			return burnWindowsVolumeName(flags.volid)
		}
		if strings.TrimSpace(flags.volid) == "" {
			return burnSanitizeVolid(flags.volid)
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

// burnStageISO macOS：目录 → 临时 ISO 镜像（hdiutil makehybrid），返回清理函数。失败返回 error 而非
// ErrorPrint：print 即 exit 会跳过调用方的善后（多输入舞台目录曾因此泄漏，2026-11-23 Windows 实测）。
// Linux 不再经过这里——目录 / 多输入走 xorriso 原生增量，不落临时 ISO（见 burnLinuxTree）
func burnStageISO(dir string, _ burnFlags) (string, func(), error) {
	staged := filepath.Join(os.TempDir(), "zpack-burn-"+strconv.Itoa(os.Getpid())+".iso")
	// 引擎缺席先点名——否则调用方只拿到冒号后空白的「生成临时镜像失败: 」
	if _, lookErr := exec.LookPath("hdiutil"); lookErr != nil {
		return "", func() {}, errors.New("未找到 hdiutil（darwin 侧镜像生成引擎）")
	}
	out, err := exec.Command("hdiutil", "makehybrid", "-iso", "-joliet", "-ov", "-o", staged, dir).CombinedOutput()
	if err != nil {
		return "", func() {}, fmt.Errorf("生成临时镜像失败: %s", strings.TrimSpace(string(out)))
	}
	return staged, func() { _ = os.Remove(staged) }, nil
}

// burnDarwinImage macOS：hdiutil 刻录一个镜像文件（缺省不封盘；--close 才 -forceclose）。
// 内部只返回 error——调用方还有临时镜像要清
func burnDarwinImage(imagePath string, flags burnFlags) error {
	if flags.device > 0 {
		// 不悄悄换到默认刻录机：刻录不可逆，烧错设备比报错糟得多
		return errors.New("暂不支持 macOS：多刻录机寻址（--device 大于 0）尚未接入，目前只能用默认刻录机（--device 0）")
	}
	if flags.blank {
		if out, err := exec.Command("drutil", "erase", "quick").CombinedOutput(); err != nil {
			return errors.New("擦除失败（介质可能不可擦）: " + strings.TrimSpace(string(out)))
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

// burnLinuxDevice 按序号取 Linux 刻录机的设备路径与介质状态（status 取值同 burn devices）
func burnLinuxDevice(index int) (BurnDevice, error) {
	if _, lookErr := exec.LookPath("xorriso"); lookErr != nil {
		return BurnDevice{}, errors.New("未找到 xorriso（Linux 刻录引擎）——请安装：sudo apt install xorriso")
	}
	devices := burnListDevicesLinux()
	if index >= len(devices) {
		return BurnDevice{}, fmt.Errorf("--device %d 超出范围（共 %d 台，见 zpack burn devices）", index, len(devices))
	}
	return devices[index], nil
}

// burnLinuxImage Linux：镜像原样直烧，走 xorriso 的 cdrecord 兼容模式（缺省 -multi 不封盘）。
// 镜像不经过 xorriso 的 ISO 库，所以不会并入上一区段（往有数据的盘写之前已由调用方警告）
func burnLinuxImage(imagePath, devicePath string, flags burnFlags) error {
	arguments := []string{"-no_rc", "-as", "cdrecord", "-v", "dev=" + devicePath}
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

// burnLinuxTree Linux 目录 / 多输入：xorriso 原生增量（growing）。
//   - -dev 同时做输入与输出：盘上已有 xorriso 认得的 ISO 就先读进来，新内容并进去再写一个区段——
//     新区段带着「旧文件 + 新文件」的完整目录树，系统挂载最后一个区段，旧文件照常可见。空白盘则从零建。
//   - 不落临时 ISO：文件内容在写入时直接从磁盘读。
//   - 写入量取 xorriso 的会话日志（-session_log：每个区段追加一行 `timestamp start_lba size volume-id`）。
//
// 本机没有 Linux，这条路径只做了交叉编译、命令行拼装的单测与按 xorriso 手册的逐项核对——没有实跑过
// （specs/burn.md 4.1.5、9）。内部只返回 error：会话日志是临时文件，得由这里清
func burnLinuxTree(inputs []string, flags burnFlags) (int64, error) {
	device, deviceErr := burnLinuxDevice(flags.device)
	if deviceErr != nil {
		return 0, deviceErr
	}
	if refusal := burnStatusRefusal(device.Status); refusal != nil {
		return 0, refusal
	}
	logFile, logErr := os.CreateTemp("", "zpack-burn-session-*.log")
	if logErr != nil {
		return 0, fmt.Errorf("创建会话日志失败: %w", logErr)
	}
	logPath := logFile.Name()
	_ = logFile.Close()
	arguments := burnLinuxTreeArguments(device.Path, device.Status, inputs, flags, logPath)
	runErr := burnRunCommand("xorriso", arguments)
	logText, _ := os.ReadFile(logPath)
	_ = os.Remove(logPath)
	if runErr != nil {
		return 0, runErr
	}
	return burnSessionLogSize(string(logText)), nil
}

// burnLinuxTreeArguments 拼出 xorriso 原生增量的命令行（纯函数，三平台可单测）。
// 顺序即 xorriso 的执行顺序：先接管驱动器，再（擦除）、定卷标、加内容，最后提交
func burnLinuxTreeArguments(devicePath, mediaStatus string, inputs []string, flags burnFlags, sessionLogPath string) []string {
	// -no_rc 必须是第一个参数才生效：不读用户的 xorriso 配置，行为只由这里给的参数决定
	arguments := []string{"-no_rc", "-session_log", sessionLogPath, "-dev", devicePath}
	if flags.blank {
		// 只动输出驱动器；此刻镜像还没改动，不会触发「同一驱动器上既改又擦」的 FAILURE
		arguments = append(arguments, "-blank", "as_needed")
	}
	if volumeID := burnLinuxVolumeID(inputs, flags, mediaStatus); volumeID != "" {
		// 放在 -dev 之后：镜像加载不会盖掉它（xorriso 手册 -volid_for）
		arguments = append(arguments, "-volid", volumeID)
	}
	// 原生模式缺省只出 Rock Ridge；加 Joliet 给不认 Rock Ridge 的读者（与原先 mkisofs -J 一致）
	arguments = append(arguments, "-joliet", "on")
	if len(inputs) == 1 {
		// 单个目录：目录的内容进镜像根（目录名本身不进盘）
		arguments = append(arguments, "-map", mustAbs(burnRealPath(inputs[0])), "/")
	} else {
		// 多输入：每项按唯一名装进镜像根（与 Windows 的多输入同规则）
		entryNames := burnUniqueEntryNames(inputs)
		for index, input := range inputs {
			arguments = append(arguments, "-map", mustAbs(burnRealPath(input)), "/"+entryNames[index])
		}
	}
	// 与原先 mkisofs -r 同义：属主归零、人人可读、不可写——盘拿到别的机器上也读得了
	arguments = append(arguments, "-find", "/", "-exec", "mkisofs_r", "--")
	if flags.speed > 0 {
		arguments = append(arguments, "-speed", strconv.Itoa(flags.speed))
	}
	if flags.close {
		arguments = append(arguments, "-close", "on")
	} else {
		// 可追加。as_needed：个别盘型（DVD-R DL、快速除格式的 DVD-RW）只能封盘写，交给 xorriso 按介质取舍
		arguments = append(arguments, "-close", "as_needed")
	}
	if flags.noEject {
		arguments = append(arguments, "-commit")
	} else {
		// 写完不重新加载镜像、直接弹出（同 -commit 之后 -eject all）
		arguments = append(arguments, "-commit_eject", "all")
	}
	return arguments
}

// burnLinuxVolumeID 这次写入给 xorriso 的 -volid（空串 = 不给，由 xorriso 沿用盘上已有的卷标）。
// 光盘名称规则（2026-11-24 用户定案，与 Windows 同）：名称属于最后一个区段——显式给了 --volid 就用；
// 没给时只有「这次会从空白盘开始」（介质空白，或先擦除）才给缺省名，可追加的盘沿用盘上名称。
// 判不出介质状态时按旧行为给缺省名
func burnLinuxVolumeID(inputs []string, flags burnFlags, mediaStatus string) string {
	switch {
	case flags.volidGiven, flags.blank, mediaStatus == "blank":
		return flags.volid
	case mediaStatus == "appendable":
		return ""
	}
	return flags.volid
}

// burnRealPath 顶层的符号链接按它指向的真实路径交给 xorriso（与入口处 os.Stat 跟随链接一致）；
// 目录内部的链接原样保留为链接
func burnRealPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// burnSessionLogSize 从 xorriso 的会话日志取这次写入的字节数（读不出返回 0）。
// 日志每个区段追加一行：`timestamp start_lba size volume-id`——前三个各是一个词，size 按 2048 字节扇区计
// （specs/burn.md 4.1.5：单位取自 xorriso 一贯的按 2 KiB 扇区计，没有实跑验证过）。取最后一行
func burnSessionLogSize(logText string) int64 {
	lines := strings.Split(logText, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		fields := strings.Fields(lines[index])
		if len(fields) < 3 {
			continue
		}
		blocks, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || blocks < 0 {
			return 0
		}
		return blocks * BURN_SECTOR_BYTES
	}
	return 0
}

// BURN_FAILURE_DETAIL_MAX 失败详情里最多留的字符数（error_info 要能在任务卡 / 终端一行里读完）
const BURN_FAILURE_DETAIL_MAX = 240

// burnFailureDetail 从外部工具的 stderr 里挑一句放进 error_info（空串 = 没挑到）。
// xorriso 的消息带严重级别：取最后一条 FAILURE / FATAL / MISHAP / SORRY；一条都没有就取最后一个非空行
func burnFailureDetail(stderrText string) string {
	lastLine := ""
	lastFailure := ""
	for _, line := range strings.Split(stderrText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lastLine = line
		for _, severity := range []string{"FAILURE", "FATAL", "MISHAP", "SORRY"} {
			if strings.Contains(line, " : "+severity+" : ") {
				lastFailure = line
				break
			}
		}
	}
	detail := lastFailure
	if detail == "" {
		detail = lastLine
	}
	if trimmed := []rune(detail); len(trimmed) > BURN_FAILURE_DETAIL_MAX {
		return string(trimmed[:BURN_FAILURE_DETAIL_MAX]) + "…"
	}
	return detail
}

// burnRunCommand 子进程执行：stdout 进程日志走 stderr 契约通道；stderr 原样透传，同时留一份用来在失败时
// 挑出真正的原因（只报「exit status N」的话，调用方从 error_info 里看不出该改什么）。
// error 由调用方收敛（不在此 ErrorPrint——print 即 exit 会跳过调用方的清理）
func burnRunCommand(name string, arguments []string) error {
	stdout.LoggerInfo(strings.Join(append([]string{name}, arguments...), " "))
	command := exec.Command(name, arguments...)
	var stderrCopy bytes.Buffer
	command.Stderr = io.MultiWriter(os.Stderr, &stderrCopy)
	output, err := command.Output()
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			stdout.LoggerInfo(line)
		}
	}
	if err != nil {
		message := fmt.Sprintf("%s 刻录失败: %s", name, err.Error())
		if detail := burnFailureDetail(stderrCopy.String()); detail != "" {
			message += "：" + detail
		}
		return errors.New(message)
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
