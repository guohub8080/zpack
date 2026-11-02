// convert 命令：格式转换——读任意可读格式 → 写 zip/tar/tar.gz/tar.xz/tar.zst。
// 管线：源归档 → 临时目录解压 → 目标格式压缩 → 清理临时目录。
// 缺省输出 = 源文件基名 + .zip；输出格式由 --output 扩展名决定（与 compress 一致）。
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// NewConvertCmd convert 命令
func NewConvertCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "convert <源归档> [输出路径] [--level N] [--jobs N] [--password 值] [--temp-dir 目录] [--quiet]",
		Short:              "格式转换——读任意格式，写 zip/tar/tar.gz（缺省输出 .zip）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runConvert) },
	}
}

func runConvert(args []string) {
	// 解析参数
	inputPath := ""
	outputPath := ""
	password := ""
	var restArgs []string

	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--password":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--password 缺少值")
			}
			password = args[argIndex+1]
			argIndex++
		case arg == "--quiet":
			quietMode = true
		case arg == "--temp-dir":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--temp-dir 缺少值（临时目录路径）")
			}
			argIndex++ // 值由 applyTempDirFlag 统一注入
		case arg == "--level", arg == "--jobs":
			restArgs = append(restArgs, arg)
			if argIndex+1 < len(args) {
				restArgs = append(restArgs, args[argIndex+1])
				argIndex++
			}
		case !strings.HasPrefix(arg, "-"):
			if inputPath == "" {
				inputPath = arg
			} else if outputPath == "" {
				outputPath = arg
			}
		}
	}

	if inputPath == "" {
		stdout.ErrorPrint("用法: convert <源归档> [输出路径] [--level N] [--jobs N] [--password 值]")
	}

	// 输入校验
	absoluteInput, _ := filepath.Abs(inputPath)
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}

	// 缺省输出：源基名 + .zip
	if outputPath == "" {
		base := filepath.Base(absoluteInput)
		// 剥归档扩展名（含复合 .tar.gz 等）
		for _, ext := range []string{".tar.gz", ".tar.xz", ".tar.bz2", ".tar.zst", ".tgz", ".txz", ".tbz2", ".tzst"} {
			if strings.HasSuffix(base, ext) {
				base = strings.TrimSuffix(base, ext)
				break
			}
		}
		if idx := strings.LastIndex(base, "."); idx > 0 {
			base = base[:idx]
		}
		outputPath = filepath.Join(filepath.Dir(absoluteInput), base+".zip")
	}
	absoluteOutput, _ := filepath.Abs(outputPath)

	// 输出格式校验：白名单扩展名（防止 .7z/.rar 等写入格式被静默转为 zip）
	lowerOut := strings.ToLower(absoluteOutput)
	validExts := []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.zst", ".tzst"}
	valid := false
	for _, ext := range validExts {
		if strings.HasSuffix(lowerOut, ext) {
			valid = true
			break
		}
	}
	if !valid {
		stdout.ErrorPrint("输出格式暂不支持（支持 .zip/.tar/.tar.gz/.tar.xz/.tar.zst）")
	}

	if quietMode {
		cliLogf = nil
	}

	// 解析 level/jobs
	level := 6
	if levelText, has := cmdutil.FlagValue(restArgs, "--level"); has {
		if parsed, ok := cmdutil.ParseIntStrict(levelText); ok && parsed >= 0 && parsed <= 9 {
			level = parsed
		}
	}
	jobs := 0 // 缺省（自动）

	// ─── 管线：解压到临时 → 压缩到目标 ───

	// 1. 临时目录（--temp-dir 指定基址：Windows 系统盘小的场景把中转空间放到目标盘）
	tempBase := applyTempDirFlag(args)
	tempDir, tempErr := os.MkdirTemp(tempBase, "zpack-convert-*")
	if tempErr != nil {
		stdout.ErrorPrint("无法创建临时目录: " + tempErr.Error())
	}

	// 2-4 管线放闭包：defer 清理在闭包 return 时真实执行——stdout.*Print 内部 os.Exit，
	// 外层 defer 永不运行（历史上临时目录因此在成功路径泄漏）
	type pipelineOutcome struct {
		compressed  core.CompressResult
		extracted   core.ExtractResult
		failureText string
	}
	outcome := func() pipelineOutcome {
		defer os.RemoveAll(tempDir)
		if cliLogf != nil {
			stdout.LoggerInfo(fmt.Sprintf("读取 %s ...", filepath.Base(absoluteInput)))
		}
		extractInfo, extractErr := core.Extract(core.ExtractOptions{
			Archive:   absoluteInput,
			OutputDir: tempDir,
			Force:     true,
			Password:  password,
		}, cliLogf)
		if extractErr != nil {
			return pipelineOutcome{failureText: "读取源归档失败: " + extractErr.Error()}
		}
		entries, readErr := os.ReadDir(tempDir)
		if readErr != nil {
			return pipelineOutcome{failureText: "无法读取临时目录: " + readErr.Error()}
		}
		var inputs []string
		for _, entry := range entries {
			inputs = append(inputs, filepath.Join(tempDir, entry.Name()))
		}
		if len(inputs) == 0 {
			return pipelineOutcome{failureText: "源归档为空，无法转换"}
		}
		if cliLogf != nil {
			stdout.LoggerInfo(fmt.Sprintf("写入 %s ...", filepath.Base(absoluteOutput)))
		}
		compResult, compErr := core.Compress(core.CompressOptions{
			Inputs: inputs,
			Output: absoluteOutput,
			Level:  level,
			Jobs:   jobs,
		}, cliLogf)
		if compErr != nil {
			os.Remove(absoluteOutput) // 清理半成品
			return pipelineOutcome{failureText: "写入目标归档失败: " + compErr.Error()}
		}
		return pipelineOutcome{compressed: compResult, extracted: extractInfo}
	}()
	if outcome.failureText != "" {
		stdout.ErrorPrint(outcome.failureText)
	}

	// 5. 输出结果
	stdout.JSONPrint(convertResultJSON{
		Source: absoluteInput, Archive: absoluteOutput, Format: outputExt(absoluteOutput),
		SourceFiles: outcome.extracted.FileCount, FileCount: outcome.compressed.FileCount, SizeBytes: outcome.compressed.SizeBytes,
	})
}

// outputExt 从路径推断格式标识（显示用）
func outputExt(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		return "tar.xz"
	case strings.HasSuffix(lower, ".tar.zst"), strings.HasSuffix(lower, ".tzst"):
		return "tar.zst"
	case strings.HasSuffix(lower, ".tar"):
		return "tar"
	default:
		return "zip"
	}
}
