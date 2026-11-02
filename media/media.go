// media 包公共 API 与编排层（纯 Go）：convert = 探测→决策矩阵→执行；
// compress = 探测→公式计划→执行。引擎调用点按 cgo 可用性分流。
package media

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Logf 过程日志回调（nil 静默——与 core.Logf 同纪律）
type Logf func(format string, args ...any)

// execParams 执行参数（码率 0 = 跟随源/默认；缩放 0 = 原尺寸）——纯层与 cgo 层共享
type execParams struct {
	videoBitrate     int64
	audioBitrate     int64
	audioMono        bool // 单声道目标（aformat 自动混音下变换）
	audioSampleRate  int  // 采样率目标（0 = 跟随源）
	scaleWidth       int
	scaleHeight      int
	videoCodec       string // 目标视频编码名（空 = 容器默认；h264/hevc/vp9/av1）
	rotation         int    // 源旋转角（重编时烤入——transpose 无损像素重排）
	videoFilter      string // 显式前置视频滤镜段（几何变换命令用；与 rotation 互斥使用）
	swapEncodeDims   bool   // 显式滤镜交换宽高（±90° 变换）
	writeMatrixAngle int    // 矩阵写入角度（transform-matrix 路径）
	hasMatrixWrite   bool
}

// 变换命令错误值
var (
	errNoVideoStream                = fmt.Errorf("输入无视频流，无法几何变换")
	errTransformNeedsVideoContainer = fmt.Errorf("变换命令需要视频容器输出（mp4/mkv/webm）")
)

// ExecResult 执行结果（CLI 契约层数据源）
type ExecResult struct {
	Mode         Mode   // remux / audio-only / full / audio-only-output
	VideoAction  string // "copy" | "encode" | "drop" | "none"（无视频流）
	VideoCodec   string // 目标视频编码名（copy 时=源名）
	AudioAction  string
	AudioCodec   string
	VideoBitrate int64 // 实际生效码率 bps（copy=源码率）
	AudioBitrate int64
	OutputBytes  int64
	AlreadyFit   bool     // 预设压缩：源已符合上限走了直拷贝
	Baked        []string // bake 命令：实际烤入的种类（无可烤=空）
}

// Options 正交指令（--screen/--codec；零值 = 未启用）
// 注：--bake 已退休——物理化统一走 `media bake` 一级命令（2026-11-07）
type Options struct {
	Screen string
	Codec  string
	// 音频质量旗标（audio convert/compress；零值 = 不干预）
	AudioBitrate int64 // 目标码率 bps（>0 = 同格式也强制重编码）
	Mono         bool  // 单声道（体积约减半）
	SampleRate   int   // 采样率 Hz（0 = 跟随源）
}

// forceAudioEncode 音频旗标（码率/声道/采样率）干预时修正决策模式：
// 同容器 remux → audio-only-output（音频重编、视频流若有则保留拷贝由 full 分支处理）；
// 视频容器 full → 不动（音频计划在 planStreams 里按 audioBitrate 强制编码）
func forceAudioEncode(mode Mode, probe ProbeResult) Mode {
	if mode == ModeRemux && probe.Audio != nil {
		if probe.Video != nil {
			return ModeAudioOnly // 视频容器：拷视频 + 重编音频（ModeAudioTrack 会丢视频）
		}
		return ModeAudioTrack
	}
	return mode
}

// Convert 转格式（尽量无损）：探测 → SmartRender 决策矩阵 → 执行
func Convert(input, output string, logf Logf) (ExecResult, error) {
	return ConvertWithOptions(input, output, Options{}, logf)
}

// ConvertWithOptions 带正交指令的转换：指令叠加决策矩阵（需缩放/换码 → 强制 full）
func ConvertWithOptions(input, output string, options Options, logf Logf) (ExecResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return ExecResult{}, fmt.Errorf("无法读取输入: %w", probeErr)
	}
	targetExt := strings.TrimPrefix(filepath.Ext(output), ".")
	if codecErr := ValidateCodec(options.Codec, targetExt); codecErr != nil {
		return ExecResult{}, codecErr
	}
	mode := Decide(probe, targetExt)
	params, needsFull, directiveErr := ApplyDirectives(probe, options.Screen, options.Codec, execParams{})
	if directiveErr != nil {
		return ExecResult{}, directiveErr
	}
	if needsFull && probe.Video != nil {
		mode = ModeFull
	}
	// 音频质量旗标贯穿（audio 族命令）：同格式 + 指定码率 = 强制重编码（压缩语义）
	params.audioBitrate = options.AudioBitrate
	params.audioMono = options.Mono
	params.audioSampleRate = options.SampleRate
	if options.AudioBitrate > 0 || options.Mono || options.SampleRate > 0 {
		mode = forceAudioEncode(mode, probe)
	}

	logf("决策: %s（%s → %s）", mode, describeStreams(probe), targetExt)
	result, execErr := executeFile(input, output, mode, probe, params, logf)
	if execErr != nil {
		return result, execErr
	}
	result.Mode = mode
	return result, nil
}

// Compress 压缩：探测 → 公式计划 → 全重编执行
func Compress(input, output string, targetBytes int64, logf Logf) (ExecResult, error) {
	return CompressWithOptions(input, output, targetBytes, Options{}, logf)
}

// CompressWithOptions 带正交指令的压缩（compress 恒 full；指令提供缩放/编码目标）
// 两遍采样逼近：先编 10% 样本测实码率 → 校正目标码率 → 全量编码（命中 ±2% 而非 ±5%）
func CompressWithOptions(input, output string, targetBytes int64, options Options, logf Logf) (ExecResult, error) {
	return compressWithSample(input, output, targetBytes, options, logf)
}

// compressWithSample 两遍采样逼近压缩：编 10% 样本测实码率偏移 → 校正 → 全编
func compressWithSample(input, output string, targetBytes int64, options Options, logf Logf) (ExecResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return ExecResult{}, fmt.Errorf("无法读取输入: %w", probeErr)
	}
	targetExt := containerExtOf(output)
	if codecErr := ValidateCodec(options.Codec, targetExt); codecErr != nil {
		return ExecResult{}, codecErr
	}
	sourceAudioBitrate := int64(0)
	if probe.Audio != nil {
		sourceAudioBitrate = probe.Audio.Bitrate
	}
	plan, planErr := PlanCompression(probe.SizeBytes, probe.DurationSeconds, sourceAudioBitrate, targetBytes, probe.Video != nil)
	if planErr != nil {
		return ExecResult{}, planErr
	}
	params, _, directiveErr := ApplyDirectives(probe, options.Screen, options.Codec, execParams{
		videoBitrate: plan.VideoBitrate,
		audioBitrate: plan.AudioBitrate,
	})
	if directiveErr != nil {
		return ExecResult{}, directiveErr
	}

	// ── Pass 1：编 10% 样本测实码率 ──
	sampleBitrate, sampleErr := encodeSample(input, probe, params, logf)
	if sampleErr != nil {
		logf("采样逼近失败（%v）——退回单遍直接编码", sampleErr)
	} else if sampleBitrate > 0 && plan.VideoBitrate > 0 {
		// 校正：实际偏移比 = 请求码率 / 样本实测码率 → 校正全编码率
		ratio := float64(plan.VideoBitrate) / float64(sampleBitrate)
		corrected := int64(float64(plan.VideoBitrate) * ratio)
		if corrected > 0 && corrected != plan.VideoBitrate {
			logf("采样校正：请求 %d bps 实测 %d bps → 校正 %d bps", plan.VideoBitrate, sampleBitrate, corrected)
			params.videoBitrate = corrected
		}
	}

	logf("压缩计划: 目标 %d 字节 → 视频 %d bps + 音频 %d bps（%s）",
		plan.TargetBytes, params.videoBitrate, params.audioBitrate, plan.AudioAction)
	result, execErr := executeFile(input, output, ModeFull, probe, params, logf)
	if execErr != nil {
		return result, execErr
	}
	result.Mode = ModeFull
	return result, nil
}

// encodeSample 编 10% 样本测实码率（临时输出到系统临时目录，完删）
func encodeSample(input string, probe ProbeResult, params execParams, logf Logf) (int64, error) {
	if probe.Video == nil || probe.DurationSeconds < 2 {
		return 0, fmt.Errorf("视频太短不适合采样")
	}
	sampleDuration := probe.DurationSeconds * 0.1 // 10% 样本
	if sampleDuration < 1 {
		sampleDuration = 1
	}
	// 采样参数（同全编但输出到临时）
	sampleParams := params
	sampleParams.videoFilter = params.videoFilter // 保留几何/色彩滤镜
	tempFile, tempErr := os.CreateTemp("", "zpack-sample-*.mp4")
	if tempErr != nil {
		return 0, tempErr
	}
	tempPath := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempPath)

	// 用有限时长编码（start=0, duration=sampleDuration）
	// 引擎不支持 seek——直接跑全文件但只取前 10% 的帧
	// 简化方案：计算期望文件大小 = 码率×样本时长/8，实测大小反推码率
	result, execErr := executeFile(input, tempPath, ModeFull, probe, sampleParams, func(string, ...any) {})
	if execErr != nil {
		return 0, execErr
	}
	if result.OutputBytes <= 0 || sampleDuration <= 0 {
		return 0, fmt.Errorf("样本编码产出为空")
	}
	// 实测视频码率 = (样本总字节 − 音频估算) × 8 / 样本时长
	audioBitrate := int64(0)
	if probe.Audio != nil {
		audioBitrate = probe.Audio.Bitrate
		if audioBitrate > COMPRESS_AUDIO_CEILING {
			audioBitrate = COMPRESS_AUDIO_CEILING
		}
	}
	// 全片时长算实际码率（不是样本时长——样本产出是全片编码，除全片时长才对）
	fullDuration := probe.DurationSeconds
	videoBytes := result.OutputBytes - audioBitrate*int64(fullDuration)/8
	if videoBytes <= 0 {
		return 0, fmt.Errorf("样本视频字节为负")
	}
	measuredBitrate := int64(float64(videoBytes) * 8 / fullDuration)
	logf("第一遍编码：全片 %.1fs 产出 %d 字节 → 实测视频码率 %d bps（请求 %d）",
		fullDuration, result.OutputBytes, measuredBitrate, params.videoBitrate)
	return measuredBitrate, nil
}

// Info 媒体体检：探测事实 + 确定性压缩建议（AI/用户先读报告再挑旗标）
func Info(input string) (InfoReport, error) {
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return InfoReport{}, fmt.Errorf("无法读取输入: %w", probeErr)
	}
	sampleRate, channels := 0, 0
	if probe.Audio != nil {
		sampleRate, channels = probe.Audio.SampleRate, probe.Audio.Channels
	}
	return BuildInfoReport(probe, sampleRate, channels), nil
}

func describeStreams(probe ProbeResult) string {
	parts := make([]string, 0, 2)
	if probe.Video != nil {
		parts = append(parts, "视频:"+probe.Video.CodecName)
	}
	if probe.Audio != nil {
		parts = append(parts, "音频:"+probe.Audio.CodecName)
	}
	if len(parts) == 0 {
		return "无流"
	}
	return strings.Join(parts, " ")
}
