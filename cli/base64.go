// base64 编解码命令（甜点功能）：文本/文件/标准输入的 Base64 互转。
// 用法：
//
//	zpack base64 encode "文本"           → 编码到 stdout
//	zpack base64 encode --file input.bin → 文件编码到 stdout
//	cat data | zpack base64 encode       → stdin 编码到 stdout
//	zpack base64 decode "SGVsbG8="       → 解码到 stdout
//	zpack base64 decode --file b64.txt --output out.bin → 文件解码到文件
package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// NewBase64Cmd base64 命令：encode / decode 两个子命令
func NewBase64Cmd() *cobra.Command {
	base64Command := &cobra.Command{
		Use:                "base64 <encode|decode> [输入]",
		Short:              "Base64 编码/解码（文本、文件或标准输入）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runBase64) },
	}
	return base64Command
}

func runBase64(args []string) {
	subcommand := cmdutil.FirstPositional(args)
	restArgs := base64RestArgs(args)
	switch subcommand {
	case "encode":
		base64Encode(restArgs)
	case "decode":
		base64Decode(restArgs)
	default:
		stdout.ErrorPrint("用法: base64 encode <文本|--file 路径> 或 base64 decode <Base64|--file 路径> [--output 路径]")
	}
}

// base64ResolveInput 解析输入源：位置参数文本 / --file 文件 / 标准输入
func base64ResolveInput(restArgs []string) ([]byte, string) {
	inputPath := ""
	positional := ""
	for argIndex := 0; argIndex < len(restArgs); argIndex++ {
		switch {
		case restArgs[argIndex] == "--file":
			if argIndex+1 >= len(restArgs) {
				stdout.ErrorPrint("--file 缺少值")
			}
			inputPath = restArgs[argIndex+1]
			argIndex++
		case !strings.HasPrefix(restArgs[argIndex], "-"):
			positional = restArgs[argIndex]
		}
	}
	if inputPath != "" {
		data, readErr := os.ReadFile(inputPath)
		if readErr != nil {
			stdout.ErrorPrint("无法读取文件: " + readErr.Error())
		}
		return data, inputPath
	}
	if positional != "" {
		return []byte(positional), ""
	}
	// 标准输入
	data, readErr := io.ReadAll(os.Stdin)
	if readErr != nil {
		stdout.ErrorPrint("无法读取标准输入: " + readErr.Error())
	}
	return data, "stdin"
}

// base64ResolveOutput 解析输出目标：--output 文件 / stdout
func base64ResolveOutput(restArgs []string) string {
	for argIndex := 0; argIndex < len(restArgs); argIndex++ {
		if restArgs[argIndex] == "--output" && argIndex+1 < len(restArgs) {
			return restArgs[argIndex+1]
		}
	}
	return ""
}

func base64Encode(restArgs []string) {
	data, _ := base64ResolveInput(restArgs)
	encoded := base64.StdEncoding.EncodeToString(data)
	outputPath := base64ResolveOutput(restArgs)
	if outputPath != "" {
		if writeErr := os.WriteFile(outputPath, []byte(encoded), 0o644); writeErr != nil {
			stdout.ErrorPrint("无法写入文件: " + writeErr.Error())
		}
		stdout.JSONPrint(fileOutputJSON{Output: outputPath, SizeBytes: len(encoded)})
		return
	}
	stdout.TextPrint(encoded)
}

func base64Decode(restArgs []string) {
	data, _ := base64ResolveInput(restArgs)
	decoded, decodeErr := base64.StdEncoding.DecodeString(string(data))
	if decodeErr != nil {
		stdout.ErrorPrint("无效的 Base64 输入: " + decodeErr.Error())
	}
	outputPath := base64ResolveOutput(restArgs)
	if outputPath != "" {
		if writeErr := os.WriteFile(outputPath, decoded, 0o644); writeErr != nil {
			stdout.ErrorPrint("无法写入文件: " + writeErr.Error())
		}
		stdout.JSONPrint(fileOutputJSON{Output: outputPath, SizeBytes: len(decoded)})
		return
	}
	// stdout：二进制安全输出
	fmt.Fprintf(os.Stdout, "%s", decoded)
	os.Exit(0)
}

// base64RestArgs 去掉首个非旗标参数（子命令名）
func base64RestArgs(args []string) []string {
	for argIndex, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			rest := append([]string{}, args[:argIndex]...)
			return append(rest, args[argIndex+1:]...)
		}
	}
	return args
}
