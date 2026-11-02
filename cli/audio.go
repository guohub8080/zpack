// audio 命令族：音频格式转换、压缩、元信息——与视频（media）明确分离。
//
//	zpack audio convert <输入> <输出.mp3|m4a|flac|wav|ogg|opus> [--bitrate 128k] [--sample-rate 44100] [--mono]
//	zpack audio compress <输入> [--bitrate 64k] [--mono] [--output 路径]
//	zpack audio info <输入>
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/stdout"

	"github.com/guohub8080/zpack/media"
)

// NewAudioCmd audio 命令：convert / compress / info 三个子命令
func NewAudioCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "audio <convert|compress|info> [参数]",
		Short:              "音频格式转换与压缩（mp3/m4a/flac/wav/ogg/opus 互转；码率/采样率/声道控制）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			helpAwareRun(command, args, runAudio)
		},
	}
}

func runAudio(args []string) {
	subcommand := firstPositional(args)
	restArgs := restAfterPositional(args, subcommand)
	switch subcommand {
	case "convert":
		audioConvert(restArgs)
	case "compress":
		audioCompress(restArgs)
	case "info":
		audioInfo(restArgs)
	default:
		stdout.ErrorPrint("用法: audio convert <输入> <输出.mp3|m4a|flac|wav|ogg|opus> [--bitrate 128k] [--sample-rate 44100] [--mono]\n或: audio compress <输入> [--bitrate 64k] [--mono] [--output 路径]\n或: audio info <输入>")
	}
}

// audioQualityFlags 音频质量旗标解析（convert/compress 共用）
type audioQualityFlags struct {
	bitrate    int64  // 目标码率 bps（0 = 未指定）
	sampleRate int    // 采样率 Hz（0 = 未指定）
	mono       bool   // 单声道
	rawBitrate string // 原始 --bitrate 文本（报错用）
}

// parseAudioQualityFlags 收集质量旗标与位置参数（--output 显式支持；未知旗标静默忽略）
func parseAudioQualityFlags(args []string) (audioQualityFlags, []string) {
	flags := audioQualityFlags{}
	var positional []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--bitrate", arg == "-b":
			if argIndex+1 < len(args) {
				flags.rawBitrate = args[argIndex+1]
				argIndex++
			}
		case arg == "--sample-rate":
			if argIndex+1 < len(args) {
				if parsed, ok := parseIntStrict(args[argIndex+1]); ok {
					flags.sampleRate = parsed
				} else {
					stdout.ErrorPrint("--sample-rate 需数字 Hz（如 44100），收到: " + args[argIndex+1])
				}
				argIndex++
			}
		case arg == "--mono":
			flags.mono = true
		case arg == "--output", arg == "-o":
			if argIndex+1 < len(args) {
				positional = append(positional, args[argIndex+1])
				argIndex++
			}
		case arg == "--quiet":
			quietMode = true
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		default:
		}
	}
	return flags, positional
}

// apply 校验并落位（非法值直接 ErrorPrint 退出）
func (flags audioQualityFlags) apply() media.Options {
	if flags.rawBitrate != "" {
		if parsed := parseBitrateStr(flags.rawBitrate); parsed > 0 {
			flags.bitrate = parsed
		} else {
			stdout.ErrorPrint("--bitrate 需如 64k / 128k / 320k，收到: " + flags.rawBitrate)
		}
	}
	if flags.sampleRate != 0 && (flags.sampleRate < 8000 || flags.sampleRate > 192000) {
		stdout.ErrorPrint("--sample-rate 需 8000-192000 Hz，收到: " + strconv.Itoa(flags.sampleRate))
	}
	return media.Options{AudioBitrate: flags.bitrate, Mono: flags.mono, SampleRate: flags.sampleRate}
}

// audioConvert 音频格式转换：输入任意可读格式 → 输出由扩展名决定；质量旗标生效
func audioConvert(args []string) {
	flags, positional := parseAudioQualityFlags(args)
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: audio convert <输入> <输出.mp3|m4a|flac|wav|ogg|opus> [--bitrate 128k] [--sample-rate 44100] [--mono]")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteOutput, _ := filepath.Abs(positional[1])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	options := flags.apply()
	result, convertErr := media.ConvertWithOptions(absoluteInput, absoluteOutput, options, mediaLogf())
	if convertErr != nil {
		os.Remove(absoluteOutput) // 清理半成品
		stdout.ErrorPrint(convertErr.Error())
	}
	base := buildMediaResultJSON(absoluteInput, absoluteOutput, result)
	stdout.JSONPrint(audioResultJSON{
		Mode: base.Mode, Input: base.Input, Output: base.Output,
		Audio: base.Audio, SizeBytes: base.SizeBytes,
		OriginalSizeBytes: base.OriginalSizeBytes, Ratio: base.Ratio,
		Bitrate: options.AudioBitrate, SampleRate: options.SampleRate, Mono: options.Mono,
	})
}

// audioCompress 音频压缩：缺省压到原始码率 50%（下限 32k）；--bitrate 精确指定
func audioCompress(args []string) {
	flags, positional := parseAudioQualityFlags(args)
	if len(positional) < 1 {
		stdout.ErrorPrint("用法: audio compress <输入> [--bitrate 64k] [--mono] [--output 路径]\n缺省压到原始 50% 码率；--mono 体积再减半")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	outputPath := absoluteInput // 缺省就地覆盖
	if len(positional) > 1 {
		if absoluteExtra, absErr := filepath.Abs(positional[1]); absErr == nil {
			outputPath = absoluteExtra
		}
	}

	// 探测原始码率 → 缺省目标 = 50%（下限 32k）
	probeInfo, probeErr := media.Info(absoluteInput)
	if probeErr != nil {
		stdout.ErrorPrint(probeErr.Error())
	}
	srcBitrate := int64(0)
	if probeInfo.Audio != nil {
		srcBitrate = probeInfo.Audio.Bitrate
	}
	options := flags.apply()
	targetBitrate := options.AudioBitrate
	if targetBitrate == 0 {
		targetBitrate = srcBitrate / 2
		if targetBitrate < 32000 {
			targetBitrate = 32000 // 下限 32k——再低语音都糊
		}
	}
	if srcBitrate > 0 && targetBitrate >= srcBitrate {
		stdout.ErrorPrint(fmt.Sprintf("目标码率 %d 不低于原始 %d——无需压缩（要转格式用 audio convert）", targetBitrate, srcBitrate))
	}
	options.AudioBitrate = targetBitrate
	stdout.LoggerInfo(fmt.Sprintf("音频压缩: 原始 %d bps → 目标 %d bps%s", srcBitrate, targetBitrate, monoLabel(options.Mono)))

	// 同格式 + 目标码率 → 强制重编码（管线内已保证不走直拷）
	result, convertErr := media.ConvertWithOptions(absoluteInput, outputPath, options, mediaLogf())
	if convertErr != nil {
		os.Remove(outputPath)
		stdout.ErrorPrint(convertErr.Error())
	}
	base := buildMediaResultJSON(absoluteInput, outputPath, result)
	stdout.JSONPrint(audioResultJSON{
		Mode: base.Mode, Input: base.Input, Output: base.Output,
		Audio: base.Audio, SizeBytes: base.SizeBytes,
		OriginalSizeBytes: base.OriginalSizeBytes, Ratio: base.Ratio,
		OriginalBitrate: srcBitrate, Bitrate: targetBitrate,
		SampleRate: options.SampleRate, Mono: options.Mono,
	})
}

// audioResultJSON audio 族结果形状（输出契约；键序=声明序）
type audioResultJSON struct {
	Mode              string          `json:"mode"`
	Input             string          `json:"input"`
	Output            string          `json:"output"`
	Audio             mediaStreamJSON `json:"audio"`
	SizeBytes         int64           `json:"size_bytes"`
	OriginalSizeBytes int64           `json:"original_size_bytes"`
	Ratio             float64         `json:"ratio"`
	OriginalBitrate   int64           `json:"original_bitrate,omitzero"` // compress：源码率
	Bitrate           int64           `json:"bitrate,omitzero"`          // 目标码率
	SampleRate        int             `json:"sample_rate,omitzero"`      // 目标采样率
	Mono              bool            `json:"mono,omitzero"`
}

func parseBitrateStr(text string) int64 {
	text = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(text), "k"))
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value <= 0 || value > 3200 {
		return 0
	}
	return value * 1000
}

func monoLabel(mono bool) string {
	if mono {
		return "（单声道）"
	}
	return ""
}

// audioInfo 音频元信息
func audioInfo(args []string) {
	var positional []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		stdout.ErrorPrint("用法: audio info <音频文件>")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	if _, statErr := os.Stat(absoluteInput); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absoluteInput)
	}
	// 复用 media info（已支持音频流探测）
	mediaInfo([]string{absoluteInput})
}
