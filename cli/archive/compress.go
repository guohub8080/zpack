// compress 命令：多格式打包入口——旗标解析校验 → 调 core 引擎（本包别名 engine）→ 经输出契约呈现结果。
// 写格式由 --output 扩展名决定（.zip 缺省 / .tar / .tar.gz / .tar.xz / .tar.zst）；读格式见 extract.go。
// 手写旗标解析的混合语义（未知旗标静默忽略 + 数值严格校验）见 cmdutil 包注释。
package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	engine "github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/cliutil"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// DEFAULT_JOBS_CAP --jobs 缺省值上限：min(核数, 8)——8 与 core 压缩侧内存预算上限对齐
const DEFAULT_JOBS_CAP = 8

// NewCompressCmd compress 命令：多格式打包入口，格式由 --output 扩展名决定
func NewCompressCmd() *cobra.Command {
	return &cobra.Command{
		Use: "compress <目录|文件...> [--output out.zip|out.tar|out.tar.gz] [--exclude 名称]... [--level 0-9] [--jobs N] [--volume-size 100MB|cd|dvd|bd50] [--password 值] [--password-table 文件]",
		Long: `压缩目录或文件。写格式由 --output 扩展名决定：.zip（缺省）/ .tar / .tar.gz(.tgz) / .tar.xz / .tar.zst。

示例:
  zpack compress 目录/ --output out.zip           # 缺省 zip，level 6，条目级并行
  zpack compress 目录/ --output out.tar.gz        # tar.gz（>256KB 文件 128KB 分块并行）
  zpack compress a.txt b.txt --output 包.zip       # 多文件平铺进包根
  zpack compress 目录/ --output out.zip --password 秘密   # WinZip AES-256 加密
  zpack compress 大目录/ --output out.zip --volume-size dvd  # 4.7GB/卷 → out.zip.001/.002/...

stdout 恒为一条 JSON：{"is_success":true,"archive":"...","file_count":N,"size_bytes":N}；失败 {"error_info":"..."} + exit 1。`,
		Short:              "压缩为 zip / tar / tar.gz（zip 并行架构；tar 单流顺序写）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { cliutil.HelpAwareRun(command, args, runCompress) },
	}
}

// --comment / --comment-file：归档注释（parseCompressArgs 解析后由 runCompress 读取；--comment-file 优先）
var compressComment string // --comment：归档注释
var commentFilePath string // --comment-file：注释文件路径（优先于 --comment）

// parseCompressArgs 解析 compress 的位置参数与旗标：输入路径列表 / --output / --exclude（可重复）
func parseCompressArgs(args []string) (inputPaths []string, outputPath string, excludeItems []string, volumeSizeText string, compressPassword string, compressPasswordTable string, commentFilePath string) {
	excludeItems = []string{}
	for argIndex := 0; argIndex < len(args); argIndex++ {
		switch {
		case args[argIndex] == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--exclude":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--exclude 缺少值")
			}
			excludeItems = append(excludeItems, args[argIndex+1])
			argIndex++
		case args[argIndex] == "--comment-file":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--comment-file 缺少值（注释文件路径）")
			}
			commentFilePath = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--comment":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--comment 缺少值")
			}
			compressComment = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--quiet":
			// --quiet：静默由 cliutil.EngineLogf(args) 统一判定，这里只占位（不被当作输入路径）
		case args[argIndex] == "--password":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--password 缺少值")
			}
			compressPassword = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--password-table":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--password-table 缺少值（密码映射文件路径）")
			}
			compressPasswordTable = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--volume-size":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--volume-size 缺少值（如 100MB、1GB、65536）")
			}
			volumeSizeText = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--jobs", args[argIndex] == "--level":
			// 数值旗标：占位跳过其值，值合法性由 resolve*Flag 严格校验
			if argIndex+1 < len(args) {
				argIndex++
			}
		case args[argIndex] == "--temp-dir":
			// 临时目录旗标：值由 applyTempDirFlag 统一注入，此处占位跳过防被当输入路径
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--temp-dir 缺少值（临时目录路径）")
			}
			argIndex++
		case strings.HasPrefix(args[argIndex], "-"):
			// 未知旗标静默忽略（宽容语义）
		default:
			inputPaths = append(inputPaths, args[argIndex])
		}
	}
	return inputPaths, outputPath, excludeItems, volumeSizeText, compressPassword, compressPasswordTable, commentFilePath
}

// resolveLevelFlag 解析 --level：0-9 严格校验（zip deflate 档 / tar.gz gzip 档共用）
func resolveLevelFlag(args []string) int {
	levelText, hasLevel := cmdutil.FlagValue(args, "--level")
	if !hasLevel {
		return engine.DefaultCompressLevel
	}
	level, levelOk := cmdutil.ParseIntStrict(levelText)
	if !levelOk || level < 0 || level > 9 {
		stdout.ErrorPrint("--level 需要 0-9 的整数，收到: " + levelText)
	}
	return level
}

// resolveJobsFlag 解析 --jobs：1-64 严格校验；缺省 min(CPU 核数, 8)——压缩侧缺省
func resolveJobsFlag(args []string) int {
	jobsText, hasJobs := cmdutil.FlagValue(args, "--jobs")
	if !hasJobs {
		return min(runtime.NumCPU(), DEFAULT_JOBS_CAP)
	}
	jobs, jobsOk := cmdutil.ParseIntStrict(jobsText)
	if !jobsOk || jobs < 1 || jobs > engine.MaxJobs {
		stdout.ErrorPrint("--jobs 需要 1-64 的整数，收到: " + jobsText)
	}
	return jobs
}

func runCompress(args []string) {
	applyTempDirFlag(args) // --temp-dir：zip 分块溢写文件落点（缺省系统临时目录）
	inputPaths, outputPath, excludeItems, volumeSizeText, compressPassword, compressPasswordTable, commentFilePath := parseCompressArgs(args)
	if len(inputPaths) == 0 {
		stdout.ErrorPrint("请指定打包对象：compress <目录> 或 <文件1> <文件2>... [--output out.zip|out.tar|out.tar.gz] [--exclude 名称]")
	}
	compressionLevel := resolveLevelFlag(args)
	jobs := resolveJobsFlag(args)

	// 输入校验：逐项确认存在，缺谁报谁
	for _, inputPath := range inputPaths {
		absoluteInput, absErr := filepath.Abs(inputPath)
		if absErr != nil {
			absoluteInput = inputPath
		}
		if _, statErr := os.Stat(absoluteInput); statErr != nil {
			stdout.ErrorPrint("路径不存在：" + absoluteInput)
		}
	}

	// 未指定 --output 时默认：当前目录下 <首个输入名>.zip（缺省格式即 zip）
	if outputPath == "" {
		firstAbs, _ := filepath.Abs(inputPaths[0])
		outputPath = filepath.Base(firstAbs) + ".zip"
	}
	absoluteOutput, absErr := filepath.Abs(outputPath)
	if absErr != nil {
		absoluteOutput = outputPath
	}

	var volumeSize int64
	if volumeSizeText != "" {
		var volumeErr error
		volumeSize, volumeErr = parseVolumeSize(volumeSizeText)
		if volumeErr != nil {
			stdout.ErrorPrint(volumeErr.Error())
		}
	}
	// --comment-file 优先于 --comment：读文件内容作为归档注释
	if commentFilePath != "" {
		commentData, commentErr := os.ReadFile(commentFilePath)
		if commentErr != nil {
			stdout.ErrorPrint("无法读取注释文件: " + commentErr.Error())
		}
		compressComment = strings.TrimSpace(string(commentData))
	}
	compressOptions := engine.CompressOptions{
		Inputs:     inputPaths,
		Output:     absoluteOutput,
		Comment:    compressComment,
		Excludes:   excludeItems,
		Level:      compressionLevel,
		Jobs:       jobs,
		VolumeSize: volumeSize,
		Password:   compressPassword,
	}
	if compressPasswordTable != "" {
		resolver, fallbackPassword, tableErr := parsePasswordTable(compressPasswordTable)
		if tableErr != nil {
			stdout.ErrorPrint(tableErr.Error())
		}
		compressOptions.PasswordResolver = resolver
		// 没给 --password 时用密码表的 *= 兜底项当基准密码。引擎以「基准密码非空」决定加不加密——
		// 此前只给 --password-table 会让整包保持明文而命令照样报成功（2026-11-24 修复）
		if compressPassword == "" {
			if fallbackPassword == "" {
				stdout.ErrorPrint("--password-table 里没有 *= 兜底项，且未给 --password：没被密码表匹配到的文件将保持明文。请在密码表里补一行 *=密码，或同时给 --password")
			}
			compressOptions.Password = fallbackPassword
		}
	}
	result, compressErr := engine.Compress(compressOptions, cliutil.EngineLogf(args))
	if compressErr != nil {
		if errors.Is(compressErr, engine.ErrCancelled) {
			stdout.ErrorPrint("操作已被用户取消（半成品已清理）")
		}
		stdout.ErrorPrint(compressErr.Error())
	}
	stdout.JSONPrint(compressResultJSON{
		Archive: result.Archive, FileCount: result.FileCount, SizeBytes: result.SizeBytes,
	})
}

// parseVolumeSize 解析人读卷大小：支持 100MB/1GB/64KB/65536 等格式
// （后缀大小写不敏感：B/K/KB/M/MB/G/GB；纯数字=字节）
// VOLUME_TEMPLATES 分卷尺寸模板：介质/场景标称容量（SI 十进制精确字节——介质容量
// 惯例如此；数字后缀仍按二进制 1024 基，两者并存）。对齐业界主流分卷预设
// （gmail/cd650/cd/fat32/dvd；其 Naver 1.95GB/Daum 4GB 韩国邮件预设无通用价值未纳入），
// bd/bd50 蓝光档，dvd9 为常用扩展
var VOLUME_TEMPLATES = map[string]int64{
	"GMAIL": 25_000_000,    // Gmail 附件上限 25MB
	"CD650": 650_000_000,   // CD-R 74min
	"CD":    700_000_000,   // CD-R 80min
	"VCD":   700_000_000,   // VCD 同 CD-R 80min 规格
	"FAT32": 4_294_967_295, // FAT32 单文件上限 4GiB-1
	"DVD":   4_700_000_000, // DVD-5 单面单层
	"DVD5":  4_700_000_000,
	"DVD9":  8_500_000_000,  // DVD-9 单面双层
	"BD":    25_000_000_000, // Blu-ray BD-R 25
	"BD25":  25_000_000_000,
	"BD50":  50_000_000_000, // Blu-ray BD-R DL 50
}

func parseVolumeSize(text string) (int64, error) {
	text = strings.TrimSpace(strings.ToUpper(text))
	if templateBytes, isTemplate := VOLUME_TEMPLATES[text]; isTemplate {
		return templateBytes, nil
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(text, "KB"), strings.HasSuffix(text, "K"):
		multiplier = 1024
		text = strings.TrimSuffix(strings.TrimSuffix(text, "KB"), "K")
	case strings.HasSuffix(text, "MB"), strings.HasSuffix(text, "M"):
		multiplier = 1024 * 1024
		text = strings.TrimSuffix(strings.TrimSuffix(text, "MB"), "M")
	case strings.HasSuffix(text, "GB"), strings.HasSuffix(text, "G"):
		multiplier = 1024 * 1024 * 1024
		text = strings.TrimSuffix(strings.TrimSuffix(text, "GB"), "G")
	case strings.HasSuffix(text, "B"):
		text = strings.TrimSuffix(text, "B")
	}
	value, parseErr := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if parseErr != nil || value <= 0 {
		return 0, fmt.Errorf("--volume-size 需要 >0 的尺寸（如 100MB、1GB、65536）或模板（gmail/cd650/cd/vcd/fat32/dvd/dvd9/bd/bd50），收到: %s", text)
	}
	return value * multiplier, nil
}

// ── 输出契约 struct（信封 data 载荷；键名/键序=声明序，与契约文档同步）──

// ── 输出契约 struct（信封 data 载荷；键名/键序=声明序，与契约文档同步）──

// compressResultJSON compress 成功载荷
type compressResultJSON struct {
	Archive   string `json:"archive"`
	FileCount int    `json:"file_count"`
	SizeBytes int64  `json:"size_bytes"`
}
