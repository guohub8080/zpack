// Package media —— 媒体转换/压缩决策层（纯 Go，可单测）+ cgo 执行层（probe/remux/transcode）。
//
// 设计来源：docs/unicoverter-reverse.md——业界主流优转 SmartRender 判定矩阵（§5）
// 与压缩公式族（§3）的直译；编解码引擎为进程内 libav（go-astiav）。
package media

// Mode 转换路径（决策瀑布的三级，源自业界主流 SmartRender 矩阵）
type Mode string

const (
	ModeRemux           Mode = "remux"             // 全流拷贝——秒级，bit-exact 零损失
	ModeAudioOnly       Mode = "audio-only"        // 拷视频 + 只重编音频——听感无损
	ModeFull            Mode = "full"              // 视频重编（码率跟随源——autoMask bit3 语义）
	ModeAudioTrack      Mode = "audio-only-output" // 音频容器输出（抽音轨）
	ModeTransformMatrix Mode = "transform-matrix"  // 几何变换·零损写矩阵（编码不变）
	ModeTransformBake   Mode = "transform-bake"    // 几何变换·烤入（重编码）
	ModeBake            Mode = "bake"              // 显示元数据物理化（一级命令）
)

// StreamProbe 单条流探测结果（probe.go 填充）
type StreamProbe struct {
	Index          int    // 输入流索引
	Kind           string // "video" | "audio"
	CodecName      string // libav 编解码器名（h264/hevc/aac/opus/...）
	Bitrate        int64  // 源码率 bps（0=未知）
	Width          int
	Height         int
	Framerate      float64 // 视频帧率 fps（0=未知）
	SampleRate     int     // 音频采样率（0=未知）
	Channels       int     // 音频声道数（0=未知）
	SarNum         int     // 像素宽高比分子（0=未知；1:1=方像素）——可烤元数据 #2
	SarDen         int     // 像素宽高比分母
	ColorRangeName string  // 色彩范围（"limited"/"full"/""未知）——可烤元数据 #3
	ColorSpaceName string  // 色彩空间（"bt709"/"bt2020nc"/""未知）
	TransferName   string  // 传输特性（"smpte2084"=PQ HDR/"arib-std-b67"=HLG HDR/""SDR）
	RFrameRate     float64 // 实际帧率（r_frame_rate；与 AvgFrameRate 差>1% = VFR）
	IsVFR          bool    // 变帧率（可烤元数据 #5——fps 滤镜转 CFR）
}

// ProbeResult 输入探测结果
type ProbeResult struct {
	FormatName      string // 容器名（mov/mkv/webm/...）
	DurationSeconds float64
	Rotation        int // 视频旋转角度（≠0 → 一票否决直拷，RE 矩阵第 2 条）
	Streams         []StreamProbe
	Video           *StreamProbe // 首视频流（无则 nil）
	Audio           *StreamProbe // 首音频流（无则 nil）
	SizeBytes       int64
}

// containerCompat 容器兼容白名单（决策矩阵的判定表）
var containerCompat = map[string]struct {
	video map[string]bool
	audio map[string]bool
}{
	"mp4": {
		video: setOf("h264", "hevc", "av1"),
		audio: setOf("aac", "mp3", "ac3", "eac3", "alac"),
	},
	"mkv": {video: nil, audio: nil}, // mkv 全兼容（nil = 通配）
	"webm": {
		video: setOf("vp8", "vp9", "av1"),
		audio: setOf("opus", "vorbis"),
	},
	"wav": {video: setOf(), audio: setOf("pcm_s16le", "pcm_s24le", "pcm_f32le")},
}

// subtitleCompat 字幕轨容器兼容表（nil = 全兼容）——remux/copy 路径字幕保轨依据
var subtitleCompat = map[string]map[string]bool{
	"mp4":  setOf("mov_text"),
	"mkv":  nil,
	"webm": setOf("webvtt"),
}

// audioOnlyTargets 纯音频输出容器 → 目标编码器名
var audioOnlyTargets = map[string]string{
	"mp3": "mp3", "m4a": "aac", "aac": "aac", "flac": "flac",
	"opus": "opus", "ogg": "opus", "wav": "pcm_s16le",
}

func setOf(names ...string) map[string]bool {
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result
}

// ContainerForExt 扩展名 → 容器键（mp4 家族归并；音频容器单列）
func ContainerForExt(ext string) string {
	switch ext {
	case "mp4", "m4v", "mov":
		return "mp4"
	case "mkv":
		return "mkv"
	case "webm":
		return "webm"
	case "wav":
		return "wav"
	default:
		if _, isAudio := audioOnlyTargets[ext]; isAudio {
			return ext
		}
		return ""
	}
}

// Decide 转换路径决策（业界主流 SmartRender 矩阵复刻）：
// ①全兼容且无旋转→remux ②视频兼容音频不兼容→audio-only ③否则 full；
// 音频容器输出→audio-only-output
func Decide(probe ProbeResult, targetExt string) Mode {
	container := ContainerForExt(targetExt)
	if container == "" {
		return ModeFull // 未知容器按全重编处理（安全侧）
	}
	if _, isAudioOut := audioOnlyTargets[targetExt]; isAudioOut {
		// 音频容器一律走 audio-track 路径：视频必丢（决策在 planStreams——同编码拷贝/异编码转）
		return ModeAudioTrack
	}
	compat := containerCompat[container]
	// 旋转 ≠ 0 不再否决直拷（v2 修正）：remux 时 display-matrix 随流参数拷贝零损保留；
	// 只有重编路径才需要 transpose 烤入（引擎侧处理）
	videoOK := probe.Video == nil || compat.video == nil || compat.video[probe.Video.CodecName]
	audioOK := probe.Audio == nil || compat.audio == nil || compat.audio[probe.Audio.CodecName]
	switch {
	case videoOK && audioOK:
		return ModeRemux
	case videoOK && !audioOK:
		return ModeAudioOnly // 拷视频，音频转 AAC
	default:
		return ModeFull
	}
}

// ScreenshotResult 单帧截图结果（纯层类型——cgo/nocgo 桩共用）
type ScreenshotResult struct {
	Input     string  `json:"input"`
	Output    string  `json:"output"`
	Timestamp float64 `json:"timestamp_seconds"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	SizeBytes int64   `json:"size_bytes"`
}

// ScreenshotBatchResult 多帧截图结果
type ScreenshotBatchResult struct {
	Input     string             `json:"input"`
	Frames    []ScreenshotResult `json:"frames"`
	Count     int                `json:"count"`
	OutputDir string             `json:"output_dir"`
}

// CoverResult 封面添加结果（纯层类型——cgo/nocgo 桩共用）
type CoverResult struct {
	Input     string `json:"input"`
	Output    string `json:"output"`
	CoverImg  string `json:"cover_image"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int64  `json:"size_bytes"`
}

// InsertFrameResult 帧插入结果（纯层——cgo/nocgo 共用）
type InsertFrameResult struct {
	Input     string `json:"input"`
	Output    string `json:"output"`
	Position  string `json:"position"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Codec     string `json:"codec"`
	SizeBytes int64  `json:"size_bytes"`
}

// ClipResult 截取结果（纯层——cgo/nocgo 共用）
type ClipResult struct {
	Input     string  `json:"input"`
	Output    string  `json:"output"`
	StartTime float64 `json:"start_time_seconds"`
	EndTime   float64 `json:"end_time_seconds"`
	Duration  float64 `json:"duration_seconds"`
	Mode      string  `json:"mode"`
	SizeBytes int64   `json:"size_bytes"`
}

// MergeResult 拼接结果（纯层——cgo/nocgo 共用）
type MergeResult struct {
	Reference string   `json:"reference"`
	Inputs    []string `json:"inputs"`
	Output    string   `json:"output"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Codec     string   `json:"codec"`
	Count     int      `json:"video_count"`
	SizeBytes int64    `json:"size_bytes"`
}

// SpeedResult 速度渲染结果（纯层——cgo/nocgo 共用）
type SpeedResult struct {
	Input          string  `json:"input"`
	Output         string  `json:"output"`
	Factor         float64 `json:"factor"`
	NormalDuration float64 `json:"original_duration_seconds"`
	FastDuration   float64 `json:"output_duration_seconds"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	Codec          string  `json:"codec"`
	SizeBytes      int64   `json:"size_bytes"`
}
