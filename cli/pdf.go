// pdf 命令族：合并/提取页/元信息——纯 Go 层（零 cgo）。
//
//	zpack pdf merge <1.pdf 2.pdf [3.pdf...]> <输出.pdf>
//	zpack pdf extract <输入.pdf> [--pages 1-3,5,8-] [--output 输出.pdf]
//	zpack pdf info <输入.pdf>
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/stdout"
	"github.com/guohub8080/zpack/pdf"
)

// NewPdfCmd pdf 命令
func NewPdfCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "pdf <merge|extract|info> [参数]",
		Short:              "PDF 合并/提取页/元信息（纯 Go——pdfcpu 引擎）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			helpAwareRun(command, args, runPdf)
		},
	}
}

func runPdf(args []string) {
	subcommand := firstPositional(args)
	restArgs := restAfterPositional(args, subcommand)
	switch subcommand {
	case "merge":
		pdfMerge(restArgs)
	case "extract":
		pdfExtract(restArgs)
	case "resize":
		pdfResizePages(restArgs)
	case "rotate-left", "rotate-right", "rotate-180":
		pdfRotate(subcommand, restArgs)
	case "info":
		pdfInfo(restArgs)
	default:
		stdout.ErrorPrint("用法: pdf merge <1.pdf 2.pdf [3.pdf...]> <输出.pdf>\n或: pdf extract <输入.pdf> [--pages 1-3,5,8-] [--output 输出.pdf]\n或: pdf rotate-left|rotate-right|rotate-180 <输入.pdf> [--pages 1-3] [--output 输出.pdf]\n或: pdf info <输入.pdf>")
	}
}

// pdfMerge 合并：末位位置参数=输出（同 image merge 惯例），参数序即页序
func pdfMerge(args []string) {
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--quiet":
			quietMode = true
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		default:
		}
	}
	if len(positional) < 3 { // ≥2 输入 + 1 输出
		stdout.ErrorPrint("用法: pdf merge <1.pdf 2.pdf [3.pdf...]> <输出.pdf>\n参数序即页序；至少 2 个输入")
	}
	outputPath := positional[len(positional)-1]
	inputs := positional[:len(positional)-1]
	absInputs := make([]string, len(inputs))
	for idx, inputPath := range inputs {
		absolute, _ := filepath.Abs(inputPath)
		if _, statErr := os.Stat(absolute); statErr != nil {
			stdout.ErrorPrint("PDF 不存在：" + absolute)
		}
		absInputs[idx] = absolute
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, mergeErr := pdf.Merge(absInputs, absoluteOutput)
	if mergeErr != nil {
		os.Remove(absoluteOutput) // 清理半成品
		stdout.ErrorPrint(mergeErr.Error())
	}
	stdout.JSONPrint(result)
}

// pdfExtract 提取页：--pages 必填；--output 缺省=同目录 <名>-extract.pdf
func pdfExtract(args []string) {
	pagesSpec := ""
	outputPath := ""
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--pages", arg == "-p":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--pages 缺少值（如 1-3,5,8-）")
			}
			pagesSpec = args[argIndex+1]
			argIndex++
		case arg == "--output", arg == "-o":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值（输出 PDF 路径）")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case arg == "--quiet":
			quietMode = true
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		default:
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: pdf extract <输入.pdf> [--pages 1-3,5,8-] [--output 输出.pdf]\n页码 1 起、闭区间、`8-`=到末页；缺省输出=同目录 <名>-extract.pdf")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if pagesSpec == "" {
		stdout.ErrorPrint("需要 --pages（如 --pages 1-3,5,8-）——提取全部页请直接拷贝文件")
	}
	if outputPath == "" {
		base := strings.TrimSuffix(absoluteInput, filepath.Ext(absoluteInput))
		outputPath = base + "-extract.pdf"
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, extractErr := pdf.ExtractPages(absoluteInput, pagesSpec, absoluteOutput, cliLogf)
	if extractErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(extractErr.Error())
	}
	stdout.JSONPrint(result)
}

// pdfResizePages 页面尺寸统一：图片型 PDF 各页大小不一 → 统一到纸型/自定义尺寸
func pdfResizePages(args []string) {
	sizeSpec := ""
	pagesSpec := ""
	outputPath := ""
	forceResize := false
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--force":
			forceResize = true // 文档型 PDF 放行几何缩放（文字等比缩放不重排）
		case arg == "--size":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--size 缺少值（纸型 a4/letter 或 210x297mm / 595x842）")
			}
			sizeSpec = args[argIndex+1]
			argIndex++
		case arg == "--pages", arg == "-p":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--pages 缺少值（如 1-3——缺省全部页）")
			}
			pagesSpec = args[argIndex+1]
			argIndex++
		case arg == "--output", arg == "-o":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值（输出 PDF 路径）")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		default:
		}
	}
	if len(positional) != 1 || sizeSpec == "" {
		stdout.ErrorPrint("用法: pdf resize <输入.pdf> --size a4|a4l|letter|210x297mm|595x842 [--pages 1-3] [--force] [--output 输出.pdf]\n图片型 PDF 各页大小不一时统一到目标尺寸（内容按页缩放；横页最佳适配，纸型尾缀 L/P 强制方向）；\n文档型（含文字页）默认拒绝——几何缩放文字非 Word 式重排，确要缩放加 --force；缺省全部页；缺省输出=同目录 <名>-resized.pdf")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if outputPath == "" {
		base := strings.TrimSuffix(absoluteInput, filepath.Ext(absoluteInput))
		outputPath = base + "-resized.pdf"
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, resizeErr := pdf.ResizePages(absoluteInput, sizeSpec, pagesSpec, absoluteOutput, forceResize, cliLogf)
	if resizeErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(resizeErr.Error())
	}
	stdout.JSONPrint(result)
}

// pdfRotate 旋转：方向=子命令名（left=逆时针90°/right=顺时针90°/180）；--pages 选页缺省全部
func pdfRotate(direction string, args []string) {
	pagesSpec := ""
	outputPath := ""
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--pages", arg == "-p":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--pages 缺少值（如 1-3,5——缺省全部页）")
			}
			pagesSpec = args[argIndex+1]
			argIndex++
		case arg == "--output", arg == "-o":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值（输出 PDF 路径）")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case arg == "--quiet":
			quietMode = true
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		default:
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: pdf " + direction + " <输入.pdf> [--pages 1-3] [--output 输出.pdf]\n方向语义同 image/media 家族：left=逆时针90° right=顺时针90°；--pages 缺省全部页；缺省输出=同目录 <名>-rotated.pdf\n（PDF 旋转是 /Rotate 元数据——零损，同视频写矩阵；mirror 不支持：无对应属性且文本会镜像不可读）")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if outputPath == "" {
		base := strings.TrimSuffix(absoluteInput, filepath.Ext(absoluteInput))
		outputPath = base + "-rotated.pdf"
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	// 子命令名 → 引擎语义名（rotate-180 → "180"）
	engineDirection := strings.TrimPrefix(direction, "rotate-")
	result, rotateErr := pdf.Rotate(absoluteInput, engineDirection, pagesSpec, absoluteOutput, cliLogf)
	if rotateErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(rotateErr.Error())
	}
	stdout.JSONPrint(result)
}

// pdfInfo 元信息（--all-pages 附全页尺寸+角度明细——混合尺寸体检）
func pdfInfo(args []string) {
	allPages := false
	var positional []string
	for _, arg := range args {
		switch {
		case arg == "--all-pages":
			allPages = true
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: pdf info <输入.pdf>")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	result, infoErr := pdf.Info(absoluteInput)
	if infoErr != nil {
		stdout.ErrorPrint(infoErr.Error())
	}
	stdout.LoggerInfo(fmt.Sprintf("PDF 元信息: %d 页, 首页 %.1f×%.1fmm", result.PageCount, result.WidthMm, result.HeightMm))
	if allPages {
		pages, pagesErr := pdf.InfoAllPages(absoluteInput)
		if pagesErr != nil {
			stdout.ErrorPrint(pagesErr.Error())
		}
		// 明细并入 data（信封唯一 stdout 纪律）
		resultJSON := map[string]any{
			"input": result.Input, "page_count": result.PageCount,
			"width_pt": result.WidthPt, "height_pt": result.HeightPt,
			"width_mm": result.WidthMm, "height_mm": result.HeightMm,
			"size_bytes": result.SizeBytes,
		}
		if result.Rotation != 0 {
			resultJSON["rotation"] = result.Rotation
		}
		resultJSON["pages"] = pages
		stdout.JSONPrint(resultJSON)
		return
	}
	stdout.JSONPrint(result)
}
