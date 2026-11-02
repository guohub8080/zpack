// image 命令族：图片格式转换、压缩、元信息。
//
//	zpack image convert <输入> <输出.jpg|png|webp|bmp|tiff|gif>
//	zpack image compress <输入> [--quality 80] [--output 路径]
//	zpack image info <输入>
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/image"
	"github.com/guohub8080/zpack/internal/stdout"
)

// NewImageCmd image 命令：convert / compress / info 三个子命令
func NewImageCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "image <convert|compress|info> [参数]",
		Short:              "图片格式转换与压缩（jpg/png/webp/bmp/tiff/gif 互转；纯 Go 零 cgo）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			helpAwareRun(command, args, runImage)
		},
	}
}

func runImage(args []string) {
	subcommand := firstPositional(args)
	restArgs := restAfterPositional(args, subcommand)
	switch subcommand {
	case "convert":
		imageConvert(restArgs)
	case "compress":
		imageCompress(restArgs)
	case "info":
		imageInfo(restArgs)
	case "resize":
		imageResize(restArgs)
	case "rotate-left", "rotate-right", "rotate-180", "mirror", "flip":
		imageTransform(subcommand, restArgs)
	case "merge":
		imageMerge(restArgs)
	case "livephoto":
		imageLivePhoto(restArgs)
	default:
		stdout.ErrorPrint("用法: image convert <输入> <输出.jpg|png|webp> 或 image compress <输入> [--quality N] 或 image info <输入>")
	}
}

func imageConvert(args []string) {
	quality := 0
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--quality":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--quality 缺少值（1-100）")
			}
			if parsed, err := strconv.Atoi(args[argIndex+1]); err == nil && parsed > 0 && parsed <= 100 {
				quality = parsed
			} else {
				stdout.ErrorPrint("--quality 需要 1-100 的整数，收到: " + args[argIndex+1])
			}
			argIndex++
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: image convert <输入> <输出.jpg|png|webp|bmp|tiff|gif> [--quality N]")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteOutput, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	// HEIC/HEIF/AVIF 输入 → astiav 引擎路径（纯构建会报「需完整版」）
	var result *image.ImageResult
	var convertErr error
	if image.IsHEIC(absoluteInput) {
		result, convertErr = image.ConvertHEIC(absoluteInput, absoluteOutput, quality)
	} else {
		result, convertErr = image.Convert(absoluteInput, absoluteOutput, quality)
	}
	if convertErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(convertErr.Error())
	}
	stdout.JSONPrint(result)
}

func imageCompress(args []string) {
	quality := 0
	targetBytes := int64(0)
	priority := "size" // 缺省缩尺寸优先（用户定案：巨图的主因是像素多，质量钉 90 不动）
	outputPath := ""
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--quality":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--quality 缺少值（1-100）")
			}
			if parsed, err := strconv.Atoi(args[argIndex+1]); err == nil && parsed > 0 && parsed <= 100 {
				quality = parsed
			} else {
				stdout.ErrorPrint("--quality 需要 1-100 的整数，收到: " + args[argIndex+1])
			}
			argIndex++
		case arg == "--target-size":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--target-size 缺少值（如 100KB / 2MB / 500000）")
			}
			parsed, sizeErr := parseImageByteSize(args[argIndex+1])
			if sizeErr != nil {
				stdout.ErrorPrint(sizeErr.Error())
			}
			targetBytes = parsed
			argIndex++
		case arg == "--priority":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--priority 缺少值（size=缩尺寸优先质量钉 90 / quality=保像素优先）")
			}
			priority = args[argIndex+1]
			if priority != "size" && priority != "quality" {
				stdout.ErrorPrint("--priority 需 size 或 quality，收到: " + priority)
			}
			argIndex++
		case arg == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: image compress <输入> [--quality N | --target-size 100KB] [--output 路径]\n--quality 手动定质量（缺省 80）；--target-size 自动压到 ≤ 目标（仅 jpg）+ --priority size|quality\n  size（缺省）=缩尺寸优先：质量钉 90 不动，像素换体积（5000×6000 类巨图首选）\n  quality=保像素优先：全尺寸降质到 q50 下限，不够再缩尺寸；与 --quality 互斥")
	}
	if targetBytes > 0 && quality > 0 {
		stdout.ErrorPrint("--target-size 与 --quality 互斥（自动二分 vs 手动定质量）")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if outputPath == "" {
		outputPath = absoluteInput // 就地覆盖
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	var result *image.ImageResult
	var compressErr error
	if targetBytes > 0 {
		result, compressErr = image.CompressTargetSize(absoluteInput, absoluteOutput, targetBytes, priority, cliLogf)
		if compressErr == nil {
			result.TargetBytes = targetBytes // 引擎层不回填目标值（纯函数纪律）
		}
	} else {
		result, compressErr = image.Compress(absoluteInput, absoluteOutput, quality)
	}
	if compressErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(compressErr.Error())
	}
	stdout.JSONPrint(result)
}

// imageTransform 几何变换：rotate-left/right/180 + mirror（左右）/flip（上下）——与 media 家族同名同义
func imageTransform(kind string, args []string) {
	quality := image.IMAGE_DEFAULT_QUALITY
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--quality":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--quality 缺少值（1-100）")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 && v <= 100 {
				quality = v
			} else {
				stdout.ErrorPrint("--quality 需 1-100 的整数，收到: " + args[argIndex+1])
			}
			argIndex++
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		default:
		}
	}
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: image " + kind + " <输入> <输出> [--quality N]\n纯像素重排（无损）：rotate-left=逆时针90° / rotate-right=顺时针90° / rotate-180 / mirror=左右镜像 / flip=上下镜像")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteOutput, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	result, transformErr := image.Transform(absoluteInput, absoluteOutput, kind, quality)
	if transformErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(transformErr.Error())
	}
	stdout.JSONPrint(result)
}

// imageMerge 图片拼接：长图（纵向=缺省）/横向——基准图对齐轴尺寸为准，其余等比缩放
func imageMerge(args []string) {
	referencePath := ""
	direction := "vertical"
	quality := image.IMAGE_DEFAULT_QUALITY
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--reference", arg == "--ref":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--reference 缺少值（基准图片路径）")
			}
			referencePath = args[argIndex+1]
			argIndex++
		case arg == "--direction":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--direction 缺少值（vertical=上下长图 / horizontal=左右排开）")
			}
			direction = args[argIndex+1]
			if direction != "vertical" && direction != "horizontal" {
				stdout.ErrorPrint("--direction 需 vertical 或 horizontal，收到: " + direction)
			}
			argIndex++
		case arg == "--quality":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--quality 缺少值（1-100）")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 && v <= 100 {
				quality = v
			} else {
				stdout.ErrorPrint("--quality 需 1-100 的整数，收到: " + args[argIndex+1])
			}
			argIndex++
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		default:
		}
	}
	if len(positional) < 3 { // 末位=输出，前面≥2 输入
		stdout.ErrorPrint("用法: image merge <图1> <图2> [图3...] [--reference 基准路径] <输出.png|jpg|webp> [--direction vertical|horizontal] [--quality N]\n缺省纵向长图（上下堆叠，宽=基准宽）；--direction horizontal 左右排开（高=基准高）")
	}
	outputPath := positional[len(positional)-1]
	inputs := positional[:len(positional)-1]
	absInputs := make([]string, len(inputs))
	for i, inputPath := range inputs {
		absPath, _ := filepath.Abs(inputPath)
		if _, statErr := os.Stat(absPath); statErr != nil {
			stdout.ErrorPrint("图片不存在：" + absPath)
		}
		absInputs[i] = absPath
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	absRef := ""
	if referencePath != "" {
		absRef, _ = filepath.Abs(referencePath)
	}
	result, mergeErr := image.Merge(absInputs, absoluteOutput, absRef, direction, quality)
	if mergeErr != nil {
		os.Remove(absoluteOutput) // 清理半成品
		stdout.ErrorPrint(mergeErr.Error())
	}
	stdout.JSONPrint(result)
}

func imageInfo(args []string) {
	var positional []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: image info <图片文件>")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if image.IsHEIC(absoluteInput) {
		// HEIC info：先报告 Live Photo 状态（配对检测不需要解码），然后提示解码需完整版
		liveInfo := image.DetectLivePhoto(absoluteInput)
		if liveInfo.HasVideo {
			stdout.JSONPrint(map[string]any{
				"input": absoluteInput, "format": "heic", "is_livephoto": true,
				"video_path": liveInfo.VideoPath,
				"note":       "配对视频已找到（同目录 .MOV）；像素解码需完整版（CGO 构建）",
			})
		} else {
			stdout.JSONPrint(map[string]any{
				"input": absoluteInput, "format": "heic", "is_livephoto": false,
				"note": "未找到配对视频；像素解码需完整版（CGO 构建）",
			})
		}
	}
	result, infoErr := image.Info(absoluteInput)
	if infoErr != nil {
		stdout.ErrorPrint(infoErr.Error())
	}
	stdout.JSONPrint(result)
}

// imageResize 图片缩放：降像素保观感（--width/--height/--percent/--max 四选一）
// parseDualSpec 物理尺寸双数值规格（WxH，两侧都必须给——如 35x45）。
// 只定一侧是显式旗标的事（--mm-w/--mm-h）——不做单值猜宽高的隐式语义。
func parseDualSpec(spec, flagName string) (widthCm, heightCm float64) {
	parts := strings.SplitN(spec, "x", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		stdout.ErrorPrint(flagName + " 需 WxH 双数值（如 35x45）；只定一侧用 " + flagName + "-w / " + flagName + "-h，收到: " + spec)
	}
	widthCm = parseSingleSpec(parts[0], flagName+" 宽度")
	heightCm = parseSingleSpec(parts[1], flagName+" 高度")
	return widthCm, heightCm
}

// parseSingleSpec 单个正数（物理尺寸一侧的值）
func parseSingleSpec(text, label string) float64 {
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || v <= 0 {
		stdout.ErrorPrint(label + " 需正数，收到: " + text)
	}
	return v
}

func imageResize(args []string) {
	opts := image.ResizeOptions{}
	cmWidth, cmHeight := 0.0, 0.0
	inchHeight := 0.0
	inchWidth := 0.0
	ppi := 0 // 像素密度（PPI 正名；--dpi 为惯称别名——语义同一个换算率）
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--cm", arg == "--mm":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint(arg + " 缺少值（WxH 双数值，如 3.5x4.5 / 35x45；只定一侧用 " + arg + "-w / " + arg + "-h）")
			}
			specWidth, specHeight := parseDualSpec(args[argIndex+1], arg)
			if arg == "--mm" { // mm → cm（÷10）
				specWidth, specHeight = specWidth/10.0, specHeight/10.0
			}
			cmWidth, cmHeight = specWidth, specHeight
			argIndex++
		case arg == "--cm-w", arg == "--cm-h", arg == "--mm-w", arg == "--mm-h":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint(arg + " 缺少值（正数）")
			}
			sideValue := parseSingleSpec(args[argIndex+1], arg)
			if strings.HasSuffix(arg, "-w") {
				cmWidth = sideValue
			} else {
				cmHeight = sideValue
			}
			if arg == "--mm-w" || arg == "--mm-h" { // mm → cm（÷10）
				if strings.HasSuffix(arg, "-w") {
					cmWidth /= 10.0
				} else {
					cmHeight /= 10.0
				}
			}
			argIndex++
		case arg == "--inch":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--inch 缺少值（WxH 双数值，如 3.5x5；只定一侧用 --inch-w / --inch-h）")
			}
			inchWidth, inchHeight = parseDualSpec(args[argIndex+1], "--inch")
			argIndex++
		case arg == "--inch-w", arg == "--inch-h":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint(arg + " 缺少值（正数）")
			}
			if strings.HasSuffix(arg, "-w") {
				inchWidth = parseSingleSpec(args[argIndex+1], arg)
			} else {
				inchHeight = parseSingleSpec(args[argIndex+1], arg)
			}
			argIndex++
		case arg == "--ppi", arg == "--dpi":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint(arg + " 缺少值（如 300）")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 && v <= 2400 {
				ppi = v
			} else {
				stdout.ErrorPrint(arg + " 需 1-2400 的整数，收到: " + args[argIndex+1])
			}
			argIndex++
		case arg == "--width":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--width 缺少值")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 {
				opts.Width = v
			} else {
				stdout.ErrorPrint("--width 需正整数，收到: " + args[argIndex+1])
			}
			argIndex++
		case arg == "--height":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--height 缺少值")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 {
				opts.Height = v
			} else {
				stdout.ErrorPrint("--height 需正整数，收到: " + args[argIndex+1])
			}
			argIndex++
		case arg == "--percent":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--percent 缺少值（1-99）")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 && v < 100 {
				opts.Percent = v
			} else {
				stdout.ErrorPrint("--percent 需要 1-99 的整数（缩小），收到: " + args[argIndex+1])
			}
			argIndex++
		case arg == "--max":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--max 缺少值（如 1920x1080）")
			}
			maxSpec := args[argIndex+1]
			parts := strings.SplitN(maxSpec, "x", 2)
			if len(parts) == 2 {
				if w, err1 := strconv.Atoi(parts[0]); err1 == nil && w > 0 {
					opts.MaxWidth = w
				} else {
					stdout.ErrorPrint("--max 宽度需正整数，收到: " + parts[0])
					break
				}
				if h, err2 := strconv.Atoi(parts[1]); err2 == nil && h > 0 {
					opts.MaxHeight = h
				} else {
					stdout.ErrorPrint("--max 高度需正整数，收到: " + parts[1])
					break
				}
			} else {
				stdout.ErrorPrint("--max 需 WxH 格式（如 1920x1080），收到: " + maxSpec)
			}
			argIndex++
		case arg == "--quality":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--quality 缺少值（1-100）")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 && v <= 100 {
				opts.Quality = v
			}
			argIndex++
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: image resize <输入> <输出> [--mm 35x45 | --mm-w 55 | --mm-h 45 | --cm 3.5x4.5 | --cm-w 5.5 | --inch-w 3.5 | --width 1920 | --height 1080 | --percent 50 | --max 1920x1080] [--ppi 300] [--quality N]\n物理尺寸族（mm/cm/inch 同构）：WxH 双数值强制拉伸 / -w 只定宽 / -h 只定高（另一侧按比例）；--ppi=像素密度正名（--dpi 惯称别名，语义相同）")
	}
	// 物理尺寸 → 像素换算（cm/inch 优先于像素参数；宽高独立——只给一侧另一侧按比例）
	if cmWidth > 0 || cmHeight > 0 {
		if cmWidth > 0 {
			opts.Width = image.CmToPixels(cmWidth, ppi)
		}
		if cmHeight > 0 {
			opts.Height = image.CmToPixels(cmHeight, ppi)
		}
		widthLabel, heightLabel := "按比例", "按比例"
		if cmWidth > 0 {
			widthLabel = fmt.Sprintf("%.2fcm", cmWidth)
		}
		if cmHeight > 0 {
			heightLabel = fmt.Sprintf("%.2fcm", cmHeight)
		}
		stdout.LoggerInfo(fmt.Sprintf("物理尺寸 %s × %s @ %d PPI → %d × %d 像素",
			widthLabel, heightLabel, image.PhysicalPPI(ppi), opts.Width, opts.Height))
	} else if inchWidth > 0 || inchHeight > 0 {
		if inchWidth > 0 {
			opts.Width = image.InchToPixels(inchWidth, ppi)
		}
		if inchHeight > 0 {
			opts.Height = image.InchToPixels(inchHeight, ppi)
		}
		widthLabel, heightLabel := "按比例", "按比例"
		if inchWidth > 0 {
			widthLabel = fmt.Sprintf("%.2finch", inchWidth)
		}
		if inchHeight > 0 {
			heightLabel = fmt.Sprintf("%.2finch", inchHeight)
		}
		stdout.LoggerInfo(fmt.Sprintf("物理尺寸 %s × %s @ %d PPI → %d × %d 像素",
			widthLabel, heightLabel, image.PhysicalPPI(ppi), opts.Width, opts.Height))
	}
	if opts.Width == 0 && opts.Height == 0 && opts.Percent == 0 && opts.MaxWidth == 0 {
		stdout.ErrorPrint("需要指定缩放方式：--cm / --inch / --width / --height / --percent / --max")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteOutput, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	result, resizeErr := image.Resize(absoluteInput, absoluteOutput, opts)
	if resizeErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(resizeErr.Error())
	}
	stdout.JSONPrint(result)
}

// imageLivePhoto Live Photo 提取：检测配对视频并拷贝
func imageLivePhoto(args []string) {
	var positional []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}
	if len(positional) < 1 {
		stdout.ErrorPrint("用法: image livephoto <HEIC 文件> [--output-dir 目录]")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	outputDir := ""
	if len(positional) > 1 {
		outputDir = positional[1]
	}
	// 探测
	liveInfo := image.DetectLivePhoto(absoluteInput)
	if !liveInfo.HasVideo {
		stdout.JSONPrint(map[string]any{
			"is_success": true,
			"data": map[string]any{
				"heic_path":  liveInfo.HEICPath,
				"has_video":  false,
				"video_path": nil,
			},
		})
	}
	// 提取（拷贝）
	videoPath, extractErr := image.ExtractLivePhotoVideo(absoluteInput, outputDir)
	if extractErr != nil {
		stdout.ErrorPrint(extractErr.Error())
	}
	videoInfo, _ := os.Stat(videoPath)
	stdout.JSONPrint(map[string]any{
		"is_success": true,
		"data": map[string]any{
			"heic_path":  liveInfo.HEICPath,
			"has_video":  true,
			"video_path": liveInfo.VideoPath,
			"extracted":  videoPath,
			"size_bytes": videoInfo.Size(),
		},
	})
}

// firstPositional 取首个非旗标位置参数
func firstPositional(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// restAfterPositional 去掉首个非旗标参数（子命令名）
func restAfterPositional(args []string, sub string) []string {
	for argIndex, arg := range args {
		if arg == sub {
			rest := append([]string{}, args[:argIndex]...)
			return append(rest, args[argIndex+1:]...)
		}
	}
	return args
}

// parseImageByteSize 人读体积（100KB/2MB/1GB/500000 字节）
func parseImageByteSize(text string) (int64, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(text))
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(trimmed, "KB"), strings.HasSuffix(trimmed, "K"):
		multiplier = 1024
		trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "KB"), "K")
	case strings.HasSuffix(trimmed, "MB"), strings.HasSuffix(trimmed, "M"):
		multiplier = 1024 * 1024
		trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "MB"), "M")
	case strings.HasSuffix(trimmed, "GB"), strings.HasSuffix(trimmed, "G"):
		multiplier = 1024 * 1024 * 1024
		trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "GB"), "G")
	case strings.HasSuffix(trimmed, "B"):
		trimmed = strings.TrimSuffix(trimmed, "B")
	}
	value, parseErr := strconv.ParseInt(strings.TrimSpace(trimmed), 10, 64)
	if parseErr != nil || value <= 0 {
		return 0, fmt.Errorf("--target-size 需要 >0 的体积（如 100KB、2MB、500000），收到: %s", text)
	}
	return value * multiplier, nil
}
