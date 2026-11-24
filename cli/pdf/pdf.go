// pdf 命令族：合并/提取页/元信息——纯 Go 层（零 cgo）。
//
//	zpack pdf merge <1.pdf 2.pdf [3.pdf...]> <输出.pdf>
//	zpack pdf extract <输入.pdf> [--pages 1-3,5,8-] [--output 输出.pdf]
//	zpack pdf info <输入.pdf>
package pdf

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/cliutil"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
	engine "github.com/guohub8080/zpack/pdf"
)

// NewPdfCmd pdf 命令
func NewPdfCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "pdf <merge|extract|resize|rotate-left|rotate-right|rotate-180|info> [参数]",
		Short:              "PDF 合并/提取页/页面尺寸/旋转/元信息（纯 Go——pdfcpu 引擎）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			cliutil.HelpAwareRun(command, args, runPdf)
		},
	}
}

func runPdf(args []string) {
	subcommand := cmdutil.FirstPositional(args)
	restArgs := cliutil.RestAfterPositional(args, subcommand)
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
	case "render":
		pdfRender(restArgs)
	case "collect":
		pdfCollect(restArgs)
	default:
		stdout.ErrorPrint("用法: pdf merge <1.pdf 2.pdf [3.pdf...]> <输出.pdf>\n或: pdf extract <输入.pdf> [--pages 1-3,5,8-] [--output 输出.pdf]\n或: pdf resize <输入.pdf> --size a4|letter|210x297mm [--pages 1-3] [--force] [--output 输出.pdf]\n或: pdf rotate-left|rotate-right|rotate-180 <输入.pdf> [--pages 1-3] [--output 输出.pdf]\n或: pdf info <输入.pdf>\n或: pdf render <输入.pdf> [--pages 1-3,5] [--dpi 96] [--format png|jpg] [--output 目录]\n或: pdf collect <输入.pdf> --pages <保序页表,可重复> [--output 输出.pdf]")
	}
}

// pdfCollect 按给定页序收集出新 PDF（保序、可重复——编辑器重排导出的原语；
// 与 extract 的升序去重语义刻意不同，见 specs/pdf.md）
func pdfCollect(args []string) {
	var positional []string
	pagesSpec := ""
	var outputPath string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--pages":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--pages 需要一个页序列参数（保序、可重复，如 3,1,2 或 1-3,1）")
			}
			pagesSpec = args[argIndex+1]
			argIndex++
		case arg == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 需要一个输出路径参数")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 || pagesSpec == "" {
		stdout.ErrorPrint("用法: pdf collect <输入.pdf> --pages <保序页表> [--output 输出.pdf]\n页表保序、可重复（3,1,2 按此序收集；2,2 收集两份）——与 extract 的升序去重刻意不同")
	}
	inputPath, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(inputPath); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + inputPath)
	}
	pageCount, countErr := engine.CountPages(inputPath)
	if countErr != nil {
		stdout.ErrorPrint(countErr.Error())
	}
	sequence, seqErr := engine.ParsePageSequence(pagesSpec, pageCount)
	if seqErr != nil {
		stdout.ErrorPrint(seqErr.Error())
	}
	if outputPath == "" {
		outputPath = strings.TrimSuffix(positional[0], filepath.Ext(positional[0])) + "-collect.pdf"
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, collectErr := engine.CollectFile(inputPath, absoluteOutput, sequence)
	if collectErr != nil {
		stdout.ErrorPrint(collectErr.Error())
	}
	stdout.JSONPrint(result)
}

// pdfRender 页面渲染为图片（MuPDF 引擎，cgo 分层 stub——纯构建报「需完整版」）
func pdfRender(args []string) {
	options := engine.RenderOptions{DPI: engine.RENDER_DPI_DEFAULT, Format: "png"}
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--pages":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--pages 需要一个页码规格参数")
			}
			options.PagesSpec = args[argIndex+1]
			argIndex++
		case arg == "--dpi":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--dpi 需要一个数值参数")
			}
			dpiValue, parseErr := strconv.Atoi(args[argIndex+1])
			if parseErr != nil || dpiValue < engine.RENDER_DPI_MIN || dpiValue > engine.RENDER_DPI_MAX {
				stdout.ErrorPrint(fmt.Sprintf("--dpi 需 36–600 的正整数，收到: %s", args[argIndex+1]))
			}
			options.DPI = dpiValue
			argIndex++
		case arg == "--format":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--format 需要一个格式参数")
			}
			options.Format = engine.NormalizeRenderFormat(args[argIndex+1])
			if options.Format == "" {
				stdout.ErrorPrint(fmt.Sprintf("--format 支持 png/jpg，收到: %s", args[argIndex+1]))
			}
			argIndex++
		case arg == "--output", arg == "--output-dir":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 需要一个目录参数")
			}
			options.OutputDir = args[argIndex+1]
			argIndex++
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: pdf render <输入.pdf> [--pages 1-3,5] [--dpi 96] [--format png|jpg] [--output 目录]")
	}
	inputPath, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(inputPath); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + inputPath)
	}
	if options.OutputDir == "" {
		options.OutputDir = filepath.Dir(inputPath)
	}
	absoluteOutputDir, _ := filepath.Abs(options.OutputDir)
	if dirStat, statErr := os.Stat(absoluteOutputDir); statErr != nil || !dirStat.IsDir() {
		stdout.ErrorPrint("目录不存在：" + absoluteOutputDir)
	}

	// 页码规格：缺省全部页——总页数由 pdfcpu 读（操作面同源）
	pageNumbers, specErr := engine.ResolveRenderPages(inputPath, options.PagesSpec)
	if specErr != nil {
		stdout.ErrorPrint(specErr.Error())
	}

	result, renderErr := engine.RenderDocument(inputPath, options, pageNumbers)
	if renderErr != nil {
		// render 的失败带页码上下文；整包失败不回滚已写出文件（specs/pdf.md 4.4）
		stdout.ErrorPrint(renderErr.Error())
	}
	stdout.JSONPrint(result)
}

// pdfMerge 合并：末位位置参数=输出（同 image merge 惯例），参数序即页序
func pdfMerge(args []string) {
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--quiet":
			// --quiet：静默由 cliutil.EngineLogf(args) 统一判定，这里只占位
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
	outputGuard := cliutil.NewOutputGuard(absoluteOutput)
	result, mergeErr := engine.Merge(absInputs, absoluteOutput)
	if mergeErr != nil {
		outputGuard.RemoveIfCreated() // 清理半成品（只删本次新建的）
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
			// --quiet：静默由 cliutil.EngineLogf(args) 统一判定，这里只占位
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
	outputGuard := cliutil.NewOutputGuard(absoluteOutput)
	result, extractErr := engine.ExtractPages(absoluteInput, pagesSpec, absoluteOutput, cliutil.EngineLogf(args))
	if extractErr != nil {
		outputGuard.RemoveIfCreated()
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
	outputGuard := cliutil.NewOutputGuard(absoluteOutput)
	result, resizeErr := engine.ResizePages(absoluteInput, sizeSpec, pagesSpec, absoluteOutput, forceResize, cliutil.EngineLogf(args))
	if resizeErr != nil {
		outputGuard.RemoveIfCreated()
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
			// --quiet：静默由 cliutil.EngineLogf(args) 统一判定，这里只占位
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
	outputGuard := cliutil.NewOutputGuard(absoluteOutput)
	result, rotateErr := engine.Rotate(absoluteInput, engineDirection, pagesSpec, absoluteOutput, cliutil.EngineLogf(args))
	if rotateErr != nil {
		outputGuard.RemoveIfCreated()
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
	result, infoErr := engine.Info(absoluteInput)
	if infoErr != nil {
		stdout.ErrorPrint(infoErr.Error())
	}
	stdout.LoggerInfo(fmt.Sprintf("PDF 元信息: %d 页, 首页 %.1f×%.1fmm", result.PageCount, result.WidthMm, result.HeightMm))
	if allPages {
		pages, pagesErr := engine.InfoAllPages(absoluteInput)
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
