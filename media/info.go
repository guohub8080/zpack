// media info 分析层（纯 Go）：探测事实 + 压缩建议——AI/用户先读此报告再挑旗标。
// 建议规则全部确定性（可单测）：代际收益系数、像素密度保持、码率健康度。
package media

import (
	"fmt"
	"sort"
)

// indexOfByte 字节查找（免 strings 依赖的小件）
func indexOfByte(s string, b byte) int {
	for idx := 0; idx < len(s); idx++ {
		if s[idx] == b {
			return idx
		}
	}
	return -1
}

// maxI64 两值取大（info 层自用小件）
func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// InfoReport 媒体体检报告（stdout JSON 数据源）
type InfoReport struct {
	Format          string       `json:"format"`
	DurationSeconds float64      `json:"duration_seconds"`
	SizeBytes       int64        `json:"size_bytes"`
	Rotation        int          `json:"rotation,omitempty"`
	Video           *InfoStream  `json:"video"`
	Audio           *InfoStream  `json:"audio"`
	Suggestions     []InfoAdvice `json:"suggestions"`
	Bakeable        []string     `json:"bakeable"` // 可烤显示元数据清单（media bake 作用面）
}

// InfoStream 单流事实
type InfoStream struct {
	Codec      string  `json:"codec"`
	Bitrate    int64   `json:"bitrate"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	Fps        float64 `json:"fps,omitempty"`
	SarNum     int     `json:"sar_num,omitempty"`
	SarDen     int     `json:"sar_den,omitempty"`
	ColorRange string  `json:"color_range,omitempty"` // limited/full
	ColorSpace string  `json:"color_space,omitempty"` // bt709/bt2020nc
	Transfer   string  `json:"transfer,omitempty"`    // smpte2084(PQ)/arib-std-b67(HLG)/bt709(SDR)
	IsVFR      bool    `json:"is_vfr,omitempty"`      // 变帧率
	SampleRate int     `json:"sample_rate,omitempty"`
	Channels   int     `json:"channels,omitempty"`
}

// InfoAdvice 单条压缩建议（机器可执行：kind 对应旗标策略）
type InfoAdvice struct {
	Kind           string  `json:"kind"`           // resolution | codec-generation | bitrate-inflated | audio
	Order          int     `json:"order"`          // 执行序（1=bake 先行 2=中间 3=最后）
	Flag           string  `json:"flag,omitempty"` // 对应 zpack 旗标
	Note           string  `json:"note"`
	EstimatedRatio float64 `json:"estimated_ratio"` // 预计输出/原始（0=不可估）
}

// codecGeneration 编码代际（越大越新；代际差=可收割的效率）
var codecGeneration = map[string]int{
	"mpeg2video": 0, "mpeg4": 0, "vp8": 1,
	"h264": 1, "vp9": 2, "hevc": 2, "av1": 3,
}

// generationGain 代际收割系数（同画质输出/原始）
var generationGain = map[int]float64{0: 0.6, 1: 0.55, 2: 0.75, 3: 0.7}

// h264→hevc ≈ 0.55（45% 省）；hevc→av1 ≈ 0.7

// BuildInfoReport 探测事实 → 体检报告 + 建议
func BuildInfoReport(probe ProbeResult, audioSampleRate, audioChannels int) InfoReport {
	// ffmpeg 容器名可能逗号串联多后缀（"mov,mp4,m4a,..."）——取首个
	formatName := probe.FormatName
	if commaIdx := indexOfByte(formatName, ','); commaIdx > 0 {
		formatName = formatName[:commaIdx]
	}
	report := InfoReport{
		Format:          formatName,
		DurationSeconds: probe.DurationSeconds,
		SizeBytes:       probe.SizeBytes,
		Rotation:        probe.Rotation,
	}
	if probe.Video != nil {
		report.Video = &InfoStream{
			Codec: probe.Video.CodecName, Bitrate: probe.Video.Bitrate,
			Width: probe.Video.Width, Height: probe.Video.Height, Fps: probe.Video.Framerate,
			SarNum: probe.Video.SarNum, SarDen: probe.Video.SarDen,
			ColorRange: probe.Video.ColorRangeName, ColorSpace: probe.Video.ColorSpaceName,
			Transfer: probe.Video.TransferName, IsVFR: probe.Video.IsVFR,
		}
	}
	if probe.Audio != nil {
		report.Audio = &InfoStream{
			Codec: probe.Audio.CodecName, Bitrate: probe.Audio.Bitrate,
			SampleRate: audioSampleRate, Channels: audioChannels,
		}
	}
	report.Suggestions = AnalyzeCompression(probe)
	report.Bakeable = BakeableKinds(probe)
	return report
}

// AnalyzeCompression 压缩建议规则（确定性，逐条可测）：
// ①分辨率：高度超 1080p 且建议 --screen 1080p（像素密度不变，预计比例=像素比）
// ②代际：h264 源建议 --codec hevc（预计 0.55）
// ③码率虚高：bpp > 0.25（同代编码器下明显超量）→ 质量压缩即省
// ④音频：>128k 建议降 128k（compress 默认已做，提示即可）
func AnalyzeCompression(probe ProbeResult) []InfoAdvice {
	var advice []InfoAdvice
	if kinds := BakeableKinds(probe); len(kinds) > 0 {
		advice = append(advice, InfoAdvice{
			Kind: "bakeable", Order: 1,
			Note: fmt.Sprintf("含可烤显示元数据 %v——`zpack media bake` 一键物理化（先烤再裁剪/缩放，坐标系才正确）", kinds),
		})
	}
	if probe.Video == nil {
		if probe.Audio != nil && probe.Audio.Bitrate > COMPRESS_AUDIO_CEILING {
			advice = append(advice, InfoAdvice{
				Kind: "audio", Order: 2, Note: fmt.Sprintf("音频 %d kbps 高于 128k 常用档，压缩将降至 128k", probe.Audio.Bitrate/1000),
				EstimatedRatio: float64(COMPRESS_AUDIO_CEILING) / float64(maxI64(probe.Audio.Bitrate, 1)),
			})
		}
		return advice
	}
	video := probe.Video

	// ① 分辨率收割：超 1080p → --screen 1080p（像素密度保持，观感不变）
	if video.Height > 1080 {
		targetWidth, targetHeight := FitWithinBox(video.Width, video.Height, 1920, 1080)
		if targetWidth > 0 {
			pixelRatio := float64(targetWidth*targetHeight) / float64(maxI64(int64(video.Width*video.Height), 1))
			advice = append(advice, InfoAdvice{
				Kind: "resolution", Order: 2, Flag: "--screen 1080p",
				Note: fmt.Sprintf("%dx%d 超 1080p 常见屏幕；降至 %dx%d 像素密度不变（观看于 1080p 屏观感无损）",
					video.Width, video.Height, targetWidth, targetHeight),
				EstimatedRatio: pixelRatio,
			})
		}
	}

	// ②b 像素宽高比（可烤元数据 #2）：非方像素——缩放路径自动归一（setsar=1）
	if video.SarNum > 0 && video.SarDen > 0 && video.SarNum != video.SarDen {
		displayWidth := video.Width * video.SarNum / video.SarDen
		advice = append(advice, InfoAdvice{
			Kind: "aspect-ratio", Order: 1,
			Note: fmt.Sprintf("非方像素 SAR %d:%d（存储 %dx%d 显示 %dx%d）；缩放路径自动归一方像素",
				video.SarNum, video.SarDen, video.Width, video.Height, displayWidth, video.Height),
		})
	}

	// ② 代际收割：h264/vp8 → hevc
	if codecGeneration[video.CodecName] <= 1 {
		advice = append(advice, InfoAdvice{
			Kind: "codec-generation", Order: 3, Flag: "--codec hevc",
			Note:           fmt.Sprintf("源为 %s；转 hevc 同画质约省 45%%（播放环境支持 HEVC 时建议）", video.CodecName),
			EstimatedRatio: generationGain[1],
		})
	}

	// ③ 码率健康度：bpp（每像素每帧比特）> 0.25 → 明显虚高
	if video.Framerate > 0 && video.Width > 0 && video.Height > 0 && video.Bitrate > 0 {
		bpp := float64(video.Bitrate) / float64(video.Width*video.Height) / video.Framerate
		if bpp > 0.25 {
			advice = append(advice, InfoAdvice{
				Kind: "bitrate-inflated", Order: 2,
				Note: fmt.Sprintf("码率密度 %.2f bpp 明显偏高（1080p30 健康区间约 0.08-0.15）；质量压缩即可显著瘦身", bpp),
			})
		}
	}

	// ④ 音频
	if probe.Audio != nil && probe.Audio.Bitrate > COMPRESS_AUDIO_CEILING {
		advice = append(advice, InfoAdvice{
			Kind: "audio", Order: 2,
			Note: fmt.Sprintf("音频 %d kbps 高于 128k 常用档，压缩将降至 128k", probe.Audio.Bitrate/1000),
		})
	}
	// 按 Order 排序（bake 先行 → 几何/码率 → 换码最后）
	sort.SliceStable(advice, func(i, j int) bool { return advice[i].Order < advice[j].Order })
	return advice
}
