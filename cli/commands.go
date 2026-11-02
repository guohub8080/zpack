// CLI 命令面：扁平命令（compress/extract，无格式子命令）装配、旗标解析校验、
// 调用 core 引擎并经输出契约呈现结果。写格式由 --output 扩展名决定，读格式靠魔数识别。
// 手写旗标解析的混合语义（未知旗标静默忽略 + 数值严格校验）见 cmdutil 包注释。
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// DEFAULT_JOBS_CAP --jobs 缺省值上限：min(核数, 8)——8 与 core 压缩侧内存预算上限对齐
const DEFAULT_JOBS_CAP = 8

// cliLogf 把 core 的过程日志接到 stderr 契约通道（stdout 只留给最终结果）。
// --quiet 时置 nil → core 侧所有进度/GBK 提示/跳过明细静默
var cliLogf core.Logf = func(format string, args ...any) {
	stdout.LoggerInfo(fmt.Sprintf(format, args...))
}

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
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runCompress) },
	}
}

// NewExtractCmd extract 命令：多格式解压入口，格式靠魔数识别不靠扩展名
func NewExtractCmd() *cobra.Command {
	return &cobra.Command{
		Use: "extract <归档 .zip|.7z|.rar|.tar*|.gz 等> [--output 目录] [--jobs N] [--force] [--password 值] [--temp-dir 目录]",
		Long: `解压任意可读归档（26 种：zip/7z/rar/wim/cab/iso/tar 系/单流压缩/.001 分卷…），格式靠魔数识别不靠扩展名。

示例:
  zpack extract 归档.zip                    # 智能解压：单根直落 CWD，散包自动包一层目录
  zpack extract 归档.7z --output 目录/       # 指定目标目录
  zpack extract 加密.zip --password 值       # WinZip AES / ZipCrypto
  zpack extract 包.zip.001                  # 分卷首卷自动拼接

缺省跳过已存在（skipped 计数保护已改动文件），--force 覆盖；GBK 文件名自动修复；路径穿越/符号链接逃逸防护内置。
stdout 恒为一条 JSON：{"is_success":true,"output":"...","file_count":N,"size_bytes":N,"skipped":N}。`,
		Short:              "解压 zip/7z/rar/tar 系与单流压缩（智能解压、GBK 文件名修复、路径穿越防护、.001 分卷）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runExtract) },
	}
}

// parseCompressArgs 解析 compress 的位置参数与旗标：输入路径列表 / --output / --exclude（可重复）
var quietMode bool         // --quiet：stderr 完全静默
var compressComment string // --comment：归档注释
var commentFilePath string // --comment-file：注释文件路径（优先于 --comment）

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
			quietMode = true
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
		return core.DefaultCompressLevel
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
	if !jobsOk || jobs < 1 || jobs > core.MaxJobs {
		stdout.ErrorPrint("--jobs 需要 1-64 的整数，收到: " + jobsText)
	}
	return jobs
}

// resolveExtractJobsFlag 解压侧 --jobs：缺省 max(1, 核数/2)——业界主流惯例
// CArkParaExtMan::CalcThreadCount 逆向公式 min(设置 || 核数/2, 文件数, 32)
// （文件数封顶与 32 硬顶在 core 各管线内执行）
func resolveExtractJobsFlag(args []string) int {
	jobsText, hasJobs := cmdutil.FlagValue(args, "--jobs")
	if !hasJobs {
		return max(1, runtime.NumCPU()/2)
	}
	jobs, jobsOk := cmdutil.ParseIntStrict(jobsText)
	if !jobsOk || jobs < 1 || jobs > core.MaxJobs {
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
	if quietMode {
		cliLogf = nil
	}
	// --comment-file 优先于 --comment：读文件内容作为归档注释
	if commentFilePath != "" {
		commentData, commentErr := os.ReadFile(commentFilePath)
		if commentErr != nil {
			stdout.ErrorPrint("无法读取注释文件: " + commentErr.Error())
		}
		compressComment = strings.TrimSpace(string(commentData))
	}
	compressOptions := core.CompressOptions{
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
		resolver, tableErr := parsePasswordTable(compressPasswordTable)
		if tableErr != nil {
			stdout.ErrorPrint(tableErr.Error())
		}
		compressOptions.PasswordResolver = resolver
	}
	result, compressErr := core.Compress(compressOptions, cliLogf)
	if compressErr != nil {
		if errors.Is(compressErr, core.ErrCancelled) {
			stdout.ErrorPrint("操作已被用户取消（半成品已清理）")
		}
		stdout.ErrorPrint(compressErr.Error())
	}
	stdout.JSONPrint(compressResultJSON{
		Archive: result.Archive, FileCount: result.FileCount, SizeBytes: result.SizeBytes,
	})
}

// parseExtractArgs 解析 extract 的位置参数与旗标：归档路径 / --output 目录 / --password 值
func parseExtractArgs(args []string) (archivePath string, outputDirPath string, password string, passwordTablePath string) {
	for argIndex := 0; argIndex < len(args); argIndex++ {
		switch {
		case args[argIndex] == "--password-table":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--password-table 缺少值（密码映射文件路径）")
			}
			passwordTablePath = args[argIndex+1]
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
			quietMode = true
		case args[argIndex] == "--password":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--password 缺少值")
			}
			password = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值")
			}
			outputDirPath = args[argIndex+1]
			argIndex++
		case args[argIndex] == "--jobs":
			// 数值旗标：占位跳过其值，值合法性由 resolveJobsFlag 严格校验
			if argIndex+1 < len(args) {
				argIndex++
			}
		case args[argIndex] == "--temp-dir":
			// 临时目录旗标：值由 applyTempDirFlag 统一注入，此处占位跳过防被当归档路径
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--temp-dir 缺少值（临时目录路径）")
			}
			argIndex++
		case strings.HasPrefix(args[argIndex], "-"):
			// 未知旗标静默忽略（宽容语义；--force 无值旗标在此吞掉，语义由 forceOverwrite 读取）
		default:
			if archivePath == "" {
				archivePath = args[argIndex]
			}
		}
	}
	return archivePath, outputDirPath, password, passwordTablePath
}

func runExtract(args []string) {
	applyTempDirFlag(args) // --temp-dir：cab 大文件夹溢写落点（缺省系统临时目录）
	archivePath, outputDirPath, password, passwordTablePath := parseExtractArgs(args)
	if cmdutil.HasFlag(args, "--quiet") {
		cliLogf = nil
	}
	if archivePath == "" {
		stdout.ErrorPrint("请指定归档：extract <归档.zip|.tar|.tar.gz> [--output 目录] [--jobs N] [--force]")
	}
	forceOverwrite := cmdutil.HasFlag(args, "--force")
	jobs := resolveExtractJobsFlag(args)

	absoluteArchive, absErr := filepath.Abs(archivePath)
	if absErr != nil {
		absoluteArchive = archivePath
	}
	if _, statErr := os.Stat(absoluteArchive); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteArchive)
	}

	extractOptions := core.ExtractOptions{
		Archive:   absoluteArchive,
		OutputDir: outputDirPath,
		Force:     forceOverwrite,
		Jobs:      jobs,
		Password:  password,
	}
	if passwordTablePath != "" {
		resolver, tableErr := parsePasswordTable(passwordTablePath)
		if tableErr != nil {
			stdout.ErrorPrint(tableErr.Error())
		}
		extractOptions.PasswordResolver = resolver
	}
	result, extractErr := core.Extract(extractOptions, cliLogf)
	if extractErr != nil {
		if errors.Is(extractErr, core.ErrCancelled) {
			stdout.ErrorPrint("操作已被用户取消（已解出的文件保留）")
		}
		stdout.ErrorPrint(extractErr.Error())
	}
	stdout.JSONPrint(extractResultJSON{
		Output: result.Output, FileCount: result.FileCount, SizeBytes: result.SizeBytes, Skipped: result.Skipped,
	})
}

// parsePasswordTable 解析密码映射文件：每行一条"匹配模式=密码"；
// 支持 # 注释、空行忽略；匹配模式为精确文件名或 glob（* 通配）；*= 兜底
func parsePasswordTable(tablePath string) (func(string) (string, bool), error) {
	tableBytes, readErr := os.ReadFile(tablePath)
	if readErr != nil {
		return nil, fmt.Errorf("无法读取密码表: %s", readErr.Error())
	}
	type passwordRule struct {
		pattern  string
		password string
	}
	var rules []passwordRule
	for lineIdx, line := range strings.Split(string(tableBytes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		equalsIdx := strings.Index(line, "=")
		if equalsIdx < 1 {
			return nil, fmt.Errorf("密码表第 %d 行格式错误（应为 匹配模式=密码）: %s", lineIdx+1, line)
		}
		rules = append(rules, passwordRule{
			pattern:  strings.TrimSpace(line[:equalsIdx]),
			password: strings.TrimSpace(line[equalsIdx+1:]),
		})
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("密码表为空: %s", tablePath)
	}
	return func(fileName string) (string, bool) {
		baseName := filepath.Base(fileName)
		for _, rule := range rules {
			if rule.pattern == baseName {
				return rule.password, true
			}
			if matched, matchErr := filepath.Match(rule.pattern, baseName); matchErr == nil && matched {
				return rule.password, true
			}
		}
		return "", false
	}, nil
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

// compressResultJSON compress 成功载荷
type compressResultJSON struct {
	Archive   string `json:"archive"`
	FileCount int    `json:"file_count"`
	SizeBytes int64  `json:"size_bytes"`
}

// extractResultJSON extract 成功载荷
type extractResultJSON struct {
	Output    string `json:"output"`
	FileCount int    `json:"file_count"`
	SizeBytes int64  `json:"size_bytes"`
	Skipped   int    `json:"skipped"`
}

// convertResultJSON convert 成功载荷
type convertResultJSON struct {
	Source      string `json:"source"`
	Archive     string `json:"archive"`
	Format      string `json:"format"`
	SourceFiles int    `json:"source_files"`
	FileCount   int    `json:"file_count"`
	SizeBytes   int64  `json:"size_bytes"`
}

// benchSummaryJSON bench 成功载荷
type benchSummaryJSON struct {
	DataSizeMB int           `json:"data_size_mb"`
	CpuCores   int           `json:"cpu_cores"`
	Results    []benchResult `json:"results"`
}

// fileOutputJSON 通用「写文件成功」载荷（base64/crypto 共用）
type fileOutputJSON struct {
	Output    string `json:"output"`
	SizeBytes int    `json:"size_bytes"`
}
