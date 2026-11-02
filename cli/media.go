// media 命令族：转格式（尽量无损）+ 压缩。
//
//	zpack media convert <输入> <输出.mp4|mkv|mov|webm|mp3|m4a|flac|opus|wav>
//	zpack media compress <输入> [--output 路径] [--target-size 50%|100MB|cd|dvd]
//
// 决策层复刻业界主流 SmartRender 矩阵 + 压缩公式（docs/unicoverter-reverse.md）。
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
	"github.com/guohub8080/zpack/media"
)

// NewMediaCmd media 命令：convert / compress 两个子命令
func NewMediaCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "media <convert|compress> [参数]",
		Short:              "媒体转换（尽量无损）与压缩（业界主流压缩决策层 + 进程内 libav 引擎）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			helpAwareRun(command, args, runMedia)
		},
	}
}

func runMedia(args []string) {
	subcommand := cmdutil.FirstPositional(args)
	restArgs := mediaRestArgs(args)
	switch subcommand {
	case "convert":
		mediaConvert(restArgs)
	case "compress":
		mediaCompress(restArgs)
	case "preset":
		mediaPreset(restArgs)
	case "info":
		mediaInfo(restArgs)
	case "rotate-left", "rotate-right", "rotate-180", "mirror", "flip":
		mediaTransform(restArgs, media.TransformKind(subcommand))
	case "bake":
		mediaBake(restArgs)
	case "screenshot":
		mediaScreenshot(restArgs)
	case "cover":
		mediaCover(restArgs)
	case "insert-frame":
		mediaInsertFrame(restArgs)
	case "clip":
		mediaClip(restArgs)
	case "merge":
		mediaMerge(restArgs)
	case "speed":
		mediaSpeed(restArgs)
	default:
		stdout.ErrorPrint("用法: media convert <输入> <输出.mp4|mkv|webm|mp3|m4a|flac> 或 media compress <输入> [--output 路径] [--target-size 50%|100MB|cd]")
	}
}

// mediaConvert 转格式：尽量无损瀑布（remux → 拷视频编音频 → 全编）
func mediaConvert(args []string) {
	presetID := ""
	screenSize := ""
	videoCodecName := ""
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--screen":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--screen 缺少值（2160p/1440p/1080p/720p/480p/360p）")
			}
			screenSize = args[argIndex+1]
			argIndex++
		case arg == "--codec":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--codec 缺少值（h264/hevc/vp9/av1）")
			}
			videoCodecName = args[argIndex+1]
			argIndex++
		case arg == "--preset":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--preset 缺少值（如 youtube；用 media preset 查看全部）")
			}
			presetID = args[argIndex+1]
			argIndex++
		case arg == "--quiet":
			quietMode = true
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: media convert <输入> <输出.mp4|mkv|mov|webm|mp3|m4a|flac|opus|wav>")
	}
	inputPath, outputPath := positional[0], positional[1]
	absoluteInput, _ := filepath.Abs(inputPath)
	absoluteOutput, _ := filepath.Abs(outputPath)
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if quietMode {
		cliLogf = nil
	}

	if presetID != "" && screenSize != "" {
		stdout.ErrorPrint("--screen 与 --preset 互斥（预设自带适配框）")
	}
	var result media.ExecResult
	var convertErr error
	if presetID != "" {
		preset, found := media.LookupPreset(presetID)
		if !found {
			stdout.ErrorPrint("未知预设: " + presetID + "（用 media preset 查看全部）")
		}
		preset.VideoBitrate = 0 // 预设码率档与 --codec 指令并用时以指令为准——码率跟随源/公式
		result, convertErr = media.ConvertPreset(absoluteInput, absoluteOutput, preset, mediaLogf())
	} else {
		result, convertErr = media.ConvertWithOptions(absoluteInput, absoluteOutput, media.Options{Screen: screenSize, Codec: videoCodecName}, mediaLogf())
	}
	if convertErr != nil {
		os.Remove(absoluteOutput) // 清理半成品
		stdout.ErrorPrint(convertErr.Error())
	}
	resultJSON := buildMediaResultJSON(absoluteInput, absoluteOutput, result)
	if presetID != "" {
		resultJSON.Preset = presetID
	}
	stdout.JSONPrint(resultJSON)
}

// mediaCompress 压缩：业界主流公式（缺省目标 = 原始 50%）
func mediaCompress(args []string) {
	targetSizeText := ""
	outputPath := ""
	presetID := ""
	screenSize := ""
	videoCodecName := ""
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--screen":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--screen 缺少值（2160p/1440p/1080p/720p/480p/360p）")
			}
			screenSize = args[argIndex+1]
			argIndex++
		case arg == "--codec":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--codec 缺少值（h264/hevc/vp9/av1）")
			}
			videoCodecName = args[argIndex+1]
			argIndex++
		case arg == "--preset":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--preset 缺少值（如 discord；用 media preset 查看全部）")
			}
			presetID = args[argIndex+1]
			argIndex++
		case arg == "--target-size":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--target-size 缺少值（如 50%、100MB、cd）")
			}
			targetSizeText = args[argIndex+1]
			argIndex++
		case arg == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case arg == "--quiet":
			quietMode = true
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: media compress <输入> [--output 路径] [--target-size 50%|100MB|cd|dvd]")
	}
	inputPath := positional[0]
	absoluteInput, _ := filepath.Abs(inputPath)
	inputInfo, statErr := os.Stat(absoluteInput)
	if statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}

	var preset media.Preset
	if presetID != "" {
		var found bool
		preset, found = media.LookupPreset(presetID)
		if !found {
			stdout.ErrorPrint("未知预设: " + presetID + "（用 media preset 查看全部）")
		}
		if targetSizeText == "" && preset.MaxSizeBytes > 0 {
			targetSizeText = strconv.FormatInt(preset.MaxSizeBytes, 10) // 预设上限即目标
		}
	}
	// 缺省目标 = 50%；输出缺省 = 同目录同名 + .mp4
	if targetSizeText == "" {
		targetSizeText = "50%"
	}
	if outputPath == "" {
		outputPath = filepath.Join(filepath.Dir(absoluteInput), strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))+".mp4")
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	if quietMode {
		cliLogf = nil
	}

	targetBytes, resolveErr := media.ResolveTargetBytes(targetSizeText, inputInfo.Size())
	if resolveErr != nil {
		stdout.ErrorPrint(resolveErr.Error())
	}
	if presetID != "" && screenSize != "" {
		stdout.ErrorPrint("--screen 与 --preset 互斥（预设自带适配框）")
	}
	var result media.ExecResult
	var compressErr error
	directives := media.Options{Screen: screenSize, Codec: videoCodecName}
	if presetID != "" {
		result, compressErr = media.CompressPreset(absoluteInput, absoluteOutput, preset, mediaLogf())
	} else {
		result, compressErr = media.CompressWithOptions(absoluteInput, absoluteOutput, targetBytes, directives, mediaLogf())
	}
	if compressErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(compressErr.Error())
	}
	resultJSON := buildMediaResultJSON(absoluteInput, absoluteOutput, result)
	resultJSON.TargetBytes = targetBytes
	if presetID != "" {
		resultJSON.Preset = presetID
	}
	if result.AlreadyFit {
		resultJSON.AlreadyFit = true
	}
	stdout.JSONPrint(resultJSON)
}

// mediaScreenshot 视频截图：单帧（--time）或多帧（--every / --count / --times）
func mediaScreenshot(args []string) {
	timeSpec := ""
	everySpec := ""
	count := 0
	timesList := ""
	outputPath := ""
	outputDir := ""
	format := "jpg"
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--time":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--time 缺少值（如 5.5 或 10s）")
			}
			timeSpec = args[argIndex+1]
			argIndex++
		case arg == "--every":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--every 缺少值（如 10s 或 30）")
			}
			everySpec = args[argIndex+1]
			argIndex++
		case arg == "--count":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--count 缺少值")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 {
				count = v
			}
			argIndex++
		case arg == "--times":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--times 缺少值（如 5,10,15）")
			}
			timesList = args[argIndex+1]
			argIndex++
		case arg == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值")
			}
			outputPath = args[argIndex+1]
			argIndex++
		case arg == "--output-dir":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output-dir 缺少值")
			}
			outputDir = args[argIndex+1]
			argIndex++
		case arg == "--format":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--format 缺少值（jpg/png/webp）")
			}
			format = args[argIndex+1]
			argIndex++
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) < 1 {
		stdout.ErrorPrint("用法: media screenshot <视频> [--time 5.5 | --every 10s | --count 10 | --times 5,10,15] [--output 路径 | --output-dir 目录] [--format jpg|png|webp]")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}

	// 解析时间规格
	parseSeconds := func(spec string) float64 {
		spec = strings.TrimSuffix(strings.TrimSpace(spec), "s")
		if v, err := strconv.ParseFloat(spec, 64); err == nil && v >= 0 {
			return v
		}
		stdout.ErrorPrint("无效时间格式: " + spec)
		return -1
	}

	// 探测视频时长
	probeInfo, probeErr := media.Info(absoluteInput)
	if probeErr != nil {
		stdout.ErrorPrint(probeErr.Error())
	}
	duration := probeInfo.DurationSeconds

	// 模式判定
	switch {
	case timeSpec != "":
		// 单帧
		timestamp := parseSeconds(timeSpec)
		if timestamp < 0 {
			return
		}
		if timestamp >= duration {
			stdout.ErrorPrint(fmt.Sprintf("时间 %.1fs 超出视频时长 %.1fs", timestamp, duration))
		}
		if outputPath == "" {
			base := strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))
			outputPath = filepath.Join(filepath.Dir(absoluteInput), base+"_shot."+format)
		}
		result, err := media.Screenshot(absoluteInput, timestamp, outputPath)
		if err != nil {
			os.Remove(outputPath)
			stdout.ErrorPrint(err.Error())
		}
		stdout.JSONPrint(result)

	case everySpec != "":
		// 等间隔
		interval := parseSeconds(everySpec)
		if interval <= 0 {
			return
		}
		timestamps := media.GenerateTimestamps(duration, 0, interval, "interval")
		if len(timestamps) == 0 {
			stdout.ErrorPrint(fmt.Sprintf("间隔 %.1fs 超出视频时长 %.1fs", interval, duration))
		}
		result, err := media.ScreenshotBatch(absoluteInput, timestamps, outputDir, format)
		if err != nil {
			stdout.ErrorPrint(err.Error())
		}
		stdout.JSONPrint(result)

	case count > 0:
		// 均匀分布
		timestamps := media.GenerateTimestamps(duration, count, 0, "evenly")
		result, err := media.ScreenshotBatch(absoluteInput, timestamps, outputDir, format)
		if err != nil {
			stdout.ErrorPrint(err.Error())
		}
		stdout.JSONPrint(result)

	case timesList != "":
		// 指定时间列表
		var timestamps []float64
		for _, spec := range strings.Split(timesList, ",") {
			if t := parseSeconds(spec); t >= 0 {
				timestamps = append(timestamps, t)
			}
		}
		if len(timestamps) == 0 {
			stdout.ErrorPrint("--times 至少需要一个有效时间点")
		}
		result, err := media.ScreenshotBatch(absoluteInput, timestamps, outputDir, format)
		if err != nil {
			stdout.ErrorPrint(err.Error())
		}
		stdout.JSONPrint(result)

	default:
		// 缺省：中间帧
		midTime := duration / 2
		if outputPath == "" {
			base := strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))
			outputPath = filepath.Join(filepath.Dir(absoluteInput), base+"_shot."+format)
		}
		result, err := media.Screenshot(absoluteInput, midTime, outputPath)
		if err != nil {
			os.Remove(outputPath)
			stdout.ErrorPrint(err.Error())
		}
		stdout.JSONPrint(result)
	}
}

// mediaCover 添加封面（attached_pic——视频流零重编 remux + 封面元数据）
func mediaCover(args []string) {
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		if strings.HasPrefix(arg, "-") {
			continue // 未知旗标静默忽略
		}
		positional = append(positional, arg)
	}
	if len(positional) < 2 {
		stdout.ErrorPrint("用法: media cover <输入视频> <封面图> [--output 路径]")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteCover, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("视频不存在：" + absoluteInput)
	}
	if _, statErr := os.Stat(absoluteCover); statErr != nil {
		stdout.ErrorPrint("封面图不存在：" + absoluteCover)
	}
	outputPath := ""
	if len(positional) > 2 {
		outputPath = positional[2]
	}
	if outputPath == "" {
		base := strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))
		outputPath = filepath.Join(filepath.Dir(absoluteInput), base+"_cover"+filepath.Ext(absoluteInput))
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, coverErr := media.AddCover(absoluteInput, absoluteCover, absoluteOutput)
	if coverErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(coverErr.Error())
	}
	stdout.JSONPrint(result)
}

// mediaInsertFrame 可见帧插入：在视频开头/末尾插入一帧封面画面（播放可见，时长+1帧）
func mediaInsertFrame(args []string) {
	position := "front"
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--front":
			position = "front"
		case arg == "--back":
			position = "back"
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) < 2 {
		stdout.ErrorPrint("用法: media insert-frame <视频> <封面图> [--front|--back] [--output 路径]（缺省 --front）")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteCover, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("视频不存在：" + absoluteInput)
	}
	if _, statErr := os.Stat(absoluteCover); statErr != nil {
		stdout.ErrorPrint("封面图不存在：" + absoluteCover)
	}
	outputPath := ""
	if len(positional) > 2 {
		outputPath = positional[2]
	}
	if outputPath == "" {
		base := strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))
		outputPath = filepath.Join(filepath.Dir(absoluteInput), base+"_framed"+filepath.Ext(absoluteInput))
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, insertErr := media.InsertFrame(absoluteInput, absoluteCover, absoluteOutput, position)
	if insertErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(insertErr.Error())
	}
	stdout.JSONPrint(result)
}

// mediaClip 视频截取：恒帧精确（用户指明时间/帧号就是要精确的）
func mediaClip(args []string) {
	startTime, endTime := 0.0, 0.0
	frameFrom, frameTo := -1, -1
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--seconds-from", arg == "--from":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--seconds-from 缺少值（秒）")
			}
			if v, err := strconv.ParseFloat(args[argIndex+1], 64); err == nil && v >= 0 {
				startTime = v
			}
			argIndex++
		case arg == "--seconds-to", arg == "--to":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--seconds-to 缺少值（秒）")
			}
			if v, err := strconv.ParseFloat(args[argIndex+1], 64); err == nil && v > 0 {
				endTime = v
			}
			argIndex++
		case arg == "--frame-from":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--frame-from 缺少值（帧号）")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v >= 0 {
				frameFrom = v
			}
			argIndex++
		case arg == "--frame-to":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--frame-to 缺少值（帧号，不含）")
			}
			if v, err := strconv.Atoi(args[argIndex+1]); err == nil && v > 0 {
				frameTo = v
			}
			argIndex++
		case strings.HasPrefix(arg, "-"):
		default:
			positional = append(positional, arg)
		}
	}
	// 帧号模式：换算秒
	if frameFrom >= 0 || frameTo >= 0 {
		if frameFrom < 0 {
			frameFrom = 0
		}
		if frameTo < 0 {
			frameTo = frameFrom + 1
		}
		absoluteInput, _ := filepath.Abs(positional[0])
		probeInfo, probeErr := media.Info(absoluteInput)
		if probeErr != nil {
			stdout.ErrorPrint(probeErr.Error())
		}
		fps := probeInfo.Video.Fps
		if fps <= 0 {
			fps = 30
		}
		startTime = float64(frameFrom) / fps
		endTime = float64(frameTo) / fps
	}
	if len(positional) < 1 || startTime < 0 || endTime <= startTime {
		stdout.ErrorPrint("用法: media clip <视频> --seconds-from 起始秒 --seconds-to 结束秒 [--frame-from N --frame-to M] [--output 路径]\n区间语义：左闭右开 [from, to)——from 帧含、to 帧不含（同 Python 切片）")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	outputPath := ""
	if len(positional) > 1 {
		outputPath = positional[1]
	}
	if outputPath == "" {
		base := strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))
		outputPath = filepath.Join(filepath.Dir(absoluteInput), fmt.Sprintf("%s_%.1f-%.1fs%s", base, startTime, endTime, filepath.Ext(absoluteInput)))
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, clipErr := media.Clip(absoluteInput, absoluteOutput, startTime, endTime)
	if clipErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(clipErr.Error())
	}
	stdout.JSONPrint(result)
}

// mediaMerge 视频拼接：多个视频合并，首个为基准（分辨率/编码），其余缩放+重编码匹配
func mediaMerge(args []string) {
	referencePath := ""
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--reference", arg == "--ref":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--reference 缺少值（基准视频路径）")
			}
			referencePath = args[argIndex+1]
			argIndex++
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		}
	}
	if len(positional) < 3 { // 至少 2 个输入 + 1 个输出
		stdout.ErrorPrint("用法: media merge <视频1> <视频2> [视频3...] [--reference 基准路径] --output 路径\n缺省以第一个视频为基准；--reference 可指定任意一个作为基准（分辨率/编码参数）")
	}
	outputPath := positional[len(positional)-1]
	inputs := positional[:len(positional)-1]
	if len(inputs) < 2 {
		stdout.ErrorPrint("至少需要 2 个输入视频（第一个为基准）")
	}
	for _, inputPath := range inputs {
		absPath, _ := filepath.Abs(inputPath)
		if _, statErr := os.Stat(absPath); statErr != nil {
			stdout.ErrorPrint("视频不存在：" + absPath)
		}
	}
	absInputs := make([]string, len(inputs))
	for i, p := range inputs {
		absInputs[i], _ = filepath.Abs(p)
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	absRef := ""
	if referencePath != "" {
		absRef, _ = filepath.Abs(referencePath)
	}
	result, mergeErr := media.Merge(absInputs, absoluteOutput, absRef)
	if mergeErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(mergeErr.Error())
	}
	stdout.JSONPrint(result)
}

// mediaSpeed 速度渲染：调整播放速度（--factor 1.5 = 快放 1.5 倍，0.8 = 慢放）
func mediaSpeed(args []string) {
	factor := 0.0
	pitchMode := "preserve" // 缺省保音调（专业——快慢不变调）
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--pitch-match":
			pitchMode = "shift" // 布尔旗标：出现=音调跟随速度（快放变尖/慢放变低沉）
		case arg == "--factor", arg == "--speed":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--factor 缺少值（如 1.5 / 0.8）")
			}
			if v, err := strconv.ParseFloat(args[argIndex+1], 64); err == nil && v > 0 && v <= 10 {
				factor = v
			} else {
				stdout.ErrorPrint("--factor 需 0.1-10 的正数，收到: " + args[argIndex+1])
			}
			argIndex++
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		}
	}
	if factor == 0 || len(positional) < 1 {
		stdout.ErrorPrint("用法: media speed <视频> --factor 1.5 [--pitch-match] [--output 路径]\nfactor > 1 加速，< 1 减速；缺省保音调（快慢不变调）；--pitch-match 音调跟随速度（快放变尖/慢放变低沉）")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	outputPath := ""
	if len(positional) > 1 {
		outputPath = positional[1]
	}
	if outputPath == "" {
		base := strings.TrimSuffix(filepath.Base(absoluteInput), filepath.Ext(absoluteInput))
		ext := filepath.Ext(absoluteInput)
		if factor > 1 {
			outputPath = filepath.Join(filepath.Dir(absoluteInput), fmt.Sprintf("%s_%.1fx%s", base, factor, ext))
		} else {
			outputPath = filepath.Join(filepath.Dir(absoluteInput), fmt.Sprintf("%s_%.2fx%s", base, factor, ext))
		}
	}
	absoluteOutput, _ := filepath.Abs(outputPath)
	result, speedErr := media.Speed(absoluteInput, absoluteOutput, factor, pitchMode)
	if speedErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(speedErr.Error())
	}
	stdout.JSONPrint(result)
}

// mediaBake 一级命令：显示元数据物理化（rotation+sar 全烤；无可烤则 remux 幂等）
func mediaBake(args []string) {
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--quiet":
			quietMode = true
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: media bake <输入> <输出.mp4|mkv|webm>（把旋转/像素比等显示元数据物理化）")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteOutput, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if quietMode {
		cliLogf = nil
	}
	result, bakeErr := media.Bake(absoluteInput, absoluteOutput, mediaLogf())
	if bakeErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(bakeErr.Error())
	}
	stdout.JSONPrint(buildMediaResultJSON(absoluteInput, absoluteOutput, result))
}

// mediaTransform 几何变换：rotate-left/right/180 + mirror
// 旋转族默认零损写矩阵（秒级，编码不变）；--bake 强制烤入；mirror 恒烤入
func mediaTransform(args []string, kind media.TransformKind) {
	forceBake := false
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--bake":
			forceBake = true
		case arg == "--quiet":
			quietMode = true
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: media " + string(kind) + " <输入> <输出.mp4|mkv|webm> [--bake]")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteOutput, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	if quietMode {
		cliLogf = nil
	}
	result, transformErr := media.Transform(absoluteInput, absoluteOutput, kind, forceBake, mediaLogf())
	if transformErr != nil {
		os.Remove(absoluteOutput)
		stdout.ErrorPrint(transformErr.Error())
	}
	stdout.JSONPrint(buildMediaResultJSON(absoluteInput, absoluteOutput, result))
}

// mediaInfo 媒体体检：事实 + 压缩建议（stdout 单 JSON）
func mediaInfo(args []string) {
	var positional []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: media info <媒体文件>")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	report, infoErr := media.Info(absoluteInput)
	if infoErr != nil {
		stdout.ErrorPrint(infoErr.Error())
	}
	// 显示尺寸 = 存储 ×SAR ±90°对调（与 media.DisplayDims 同式——SAR 是可烤元数据 #2）
	displayWidth, displayHeight := 0, 0
	if report.Video != nil {
		displayWidth, displayHeight = report.Video.Width, report.Video.Height
		if report.Video.SarNum > 0 && report.Video.SarDen > 0 {
			displayWidth = displayWidth * report.Video.SarNum / report.Video.SarDen
		}
		if report.Rotation%180 != 0 {
			displayWidth, displayHeight = displayHeight, displayWidth
		}
	}
	stdout.JSONPrint(mediaInfoJSON{
		Input: absoluteInput, Format: report.Format,
		DurationSeconds: report.DurationSeconds, SizeBytes: report.SizeBytes,
		Rotation:     report.Rotation, // 恒显示（0=无旋转）——缺失字段的歧义比冗余糟
		DisplayWidth: displayWidth, DisplayHeight: displayHeight,
		Video: report.Video, Audio: report.Audio,
		Suggestions: report.Suggestions,
		Bakeable:    report.Bakeable,
	})
}

// mediaPreset 预设列表/详情
func mediaPreset(args []string) {
	// runMedia 已剥过子命令名，此处直接消费（避免二次剥离吃掉预设 ID）
	presetID := ""
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") && presetID == "" {
			presetID = arg
		}
	}
	if presetID == "" {
		grouped := media.ListPresets()
		groups := make([]string, 0, len(grouped))
		for group := range grouped {
			groups = append(groups, group)
		}
		sort.Strings(groups)
		groupedOutput := []presetGroupJSON{}
		for _, group := range groups {
			entries := []presetEntryJSON{}
			for _, id := range grouped[group] {
				preset, _ := media.LookupPreset(id)
				entries = append(entries, presetEntryJSON{
					ID: id, Name: preset.Name, Container: preset.Container,
					MaxSizeBytes: preset.MaxSizeBytes, Note: preset.Note,
				})
			}
			groupedOutput = append(groupedOutput, presetGroupJSON{Group: group, Presets: entries})
		}
		stdout.JSONPrint(presetListJSON{Count: media.PresetCount(), Groups: groupedOutput})
	}
	preset, found := media.LookupPreset(presetID)
	if !found {
		stdout.ErrorPrint("未知预设: " + presetID + "（用 media preset 查看全部）")
	}
	stdout.JSONPrint(presetDetailJSON{
		ID: presetID, Name: preset.Name, Group: preset.Group, Container: preset.Container,
		Width: preset.Width, Height: preset.Height,
		VideoBitrate: preset.VideoBitrate, AudioBitrate: preset.AudioBitrate,
		MaxSizeBytes: preset.MaxSizeBytes, Note: preset.Note,
	})
}

// mediaResultJSON 统一结果形状（输出契约；键序=声明序）
type mediaResultJSON struct {
	Mode              string          `json:"mode"`
	Input             string          `json:"input"`
	Output            string          `json:"output"`
	Video             mediaStreamJSON `json:"video"`
	Audio             mediaStreamJSON `json:"audio"`
	SizeBytes         int64           `json:"size_bytes"`
	OriginalSizeBytes int64           `json:"original_size_bytes"`
	Ratio             float64         `json:"ratio"`
	TargetBytes       int64           `json:"target_bytes,omitempty"`
	Baked             *[]string       `json:"baked,omitzero"` // bake：烤入种类（空数组=无可烤幂等；指针保可见）
	Preset            string          `json:"preset,omitempty"`
	AlreadyFit        bool            `json:"already_fit,omitempty"`
}

type mediaStreamJSON struct {
	Action  string `json:"action"`
	Codec   string `json:"codec,omitempty"`
	Bitrate int64  `json:"bitrate"`
}

func buildMediaResultJSON(input, output1 string, result media.ExecResult) mediaResultJSON {
	videoAction := result.VideoAction
	if videoAction == "" {
		videoAction = "none"
	}
	audioAction := result.AudioAction
	if audioAction == "" {
		audioAction = "none"
	}
	ratio := 0.0
	if result.OutputBytes > 0 {
		ratio = float64(result.OutputBytes) / float64(max64(int64(fileSizeOf(input)), 1))
	}
	return mediaResultJSON{
		Mode: string(result.Mode), Input: input, Output: output1,
		Video:     mediaStreamJSON{Action: videoAction, Codec: result.VideoCodec, Bitrate: result.VideoBitrate},
		Audio:     mediaStreamJSON{Action: audioAction, Codec: result.AudioCodec, Bitrate: result.AudioBitrate},
		SizeBytes: result.OutputBytes, OriginalSizeBytes: fileSizeOf(input), Ratio: ratio,
		Baked: bakedPointer(result),
	}
}

// mediaLogf cliLogf（core.Logf）→ media.Logf 适配
func mediaLogf() media.Logf {
	if cliLogf == nil {
		return nil
	}
	return media.Logf(cliLogf)
}

func fileSizeOf(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// mediaRestArgs 去掉首个非旗标参数（子命令名）
func mediaRestArgs(args []string) []string {
	for argIndex, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			rest := append([]string{}, args[:argIndex]...)
			return append(rest, args[argIndex+1:]...)
		}
	}
	return args
}

// ── media 输出契约 struct ──

type mediaInfoJSON struct {
	Input           string             `json:"input"`
	Format          string             `json:"format"`
	DurationSeconds float64            `json:"duration_seconds"`
	SizeBytes       int64              `json:"size_bytes"`
	Rotation        int                `json:"rotation"`
	DisplayWidth    int                `json:"display_width"`
	DisplayHeight   int                `json:"display_height"`
	Video           *media.InfoStream  `json:"video"`
	Audio           *media.InfoStream  `json:"audio"`
	Suggestions     []media.InfoAdvice `json:"suggestions"`
	Bakeable        []string           `json:"bakeable"` // 可烤显示元数据（media bake 作用面）
}

type presetEntryJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Container    string `json:"container"`
	MaxSizeBytes int64  `json:"max_size_bytes"`
	Note         string `json:"note"`
}

type presetGroupJSON struct {
	Group   string            `json:"group"`
	Presets []presetEntryJSON `json:"presets"`
}

type presetListJSON struct {
	Count  int               `json:"count"`
	Groups []presetGroupJSON `json:"groups"`
}

type presetDetailJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Group        string `json:"group"`
	Container    string `json:"container"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	VideoBitrate int64  `json:"video_bitrate"`
	AudioBitrate int64  `json:"audio_bitrate"`
	MaxSizeBytes int64  `json:"max_size_bytes"`
	Note         string `json:"note"`
}

// bakedPointer bake 模式恒出字段（无可烤=[]而非缺省——字段缺失的歧义比冗余糟）
func bakedPointer(result media.ExecResult) *[]string {
	if result.Mode != media.ModeBake {
		return nil
	}
	if result.Baked == nil {
		empty := []string{}
		return &empty
	}
	return &result.Baked
}
