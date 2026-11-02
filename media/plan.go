// compress 计划层：业界主流压缩公式族（总码率=目标×8÷时长；音频阶梯 128k；下限=原始×10%+音频）。
package media

import (
	"fmt"
	"strconv"
	"strings"
)

// COMPRESS_CONTAINER_OVERHEAD 容器开销余量（占目标大小的比例——moov/索引等结构开销）
const COMPRESS_CONTAINER_OVERHEAD = 0.01

// COMPRESS_AUDIO_CEILING 压缩时音频码率上限（阶梯顶 128k：源高于此则降，低于则保留）
const COMPRESS_AUDIO_CEILING = 128_000

// COMPRESS_MINIMUM_RATIO minimumCompressionSize 惯例的 10% 系数（原始大小下限保护）
const COMPRESS_MINIMUM_RATIO = 0.1

// CompressPlan 压缩执行计划
type CompressPlan struct {
	TargetBytes  int64 // 目标大小（字节）
	VideoBitrate int64 // 计划视频码率 bps
	AudioBitrate int64 // 计划音频码率 bps（0=保留原值或无声）
	AudioAction  string
	MinimumBytes int64 // 下限：原始×10% + 音频大小
}

// PlanCompression 业界主流公式直译：
//
//	总码率  = 目标×8 ÷ 时长 × (1−容器开销)      [§3.1]
//	音频    = min(源码率, 128k)；视频 = 总 − 音频  [§3.1 音频降级阶梯]
//	下限    = 原始×10% + 音频目标大小             [§3.4 minimumCompressionSize @0x1001efd43]
//	纯音频输入：码率 = 大小×8÷时长 全给音频       [§3.2 estimateAudioBitrateWithFileSize]
func PlanCompression(originalBytes int64, durationSeconds float64, sourceAudioBitrate int64, targetBytes int64, hasVideo bool) (CompressPlan, error) {
	if durationSeconds <= 0 {
		return CompressPlan{}, fmt.Errorf("时长无效: %f", durationSeconds)
	}
	if targetBytes <= 0 {
		return CompressPlan{}, fmt.Errorf("目标大小必须 > 0，收到: %d", targetBytes)
	}

	// 音频动作：源音频 > 128k → 降到 128k；≤128k → 保留原码率（重编或拷贝由执行层定）
	audioBitrate := sourceAudioBitrate
	audioAction := "copy-or-keep"
	if audioBitrate <= 0 {
		audioAction = "silent"
		audioBitrate = 0
	} else if audioBitrate > COMPRESS_AUDIO_CEILING {
		audioBitrate = COMPRESS_AUDIO_CEILING
		audioAction = "down-to-128k"
	}

	// 纯音频压缩：总码率全给音频（视频 0）
	if !hasVideo {
		audioBitrate = int64(float64(targetBytes) * 8 / durationSeconds * (1 - COMPRESS_CONTAINER_OVERHEAD))
		audioAction = "audio-only-allocate"
		return CompressPlan{
			TargetBytes: targetBytes, VideoBitrate: 0, AudioBitrate: audioBitrate,
			AudioAction:  audioAction,
			MinimumBytes: int64(float64(originalBytes) * COMPRESS_MINIMUM_RATIO),
		}, nil
	}

	// 音频目标大小 = 音频码率×时长/8（业界主流 getDestAudioFileSize 同构）
	audioBytes := int64(float64(audioBitrate) * durationSeconds / 8)
	minimumBytes := int64(float64(originalBytes)*COMPRESS_MINIMUM_RATIO) + audioBytes
	if targetBytes < minimumBytes {
		return CompressPlan{MinimumBytes: minimumBytes}, fmt.Errorf(
			"目标大小低于下限 %d 字节（原始×10%% + 音频 %d 字节）——音频不可压没，请提高目标", minimumBytes, audioBytes)
	}

	// 总码率 → 视频码率
	totalBitrate := int64(float64(targetBytes) * 8 / durationSeconds * (1 - COMPRESS_CONTAINER_OVERHEAD))
	videoBitrate := totalBitrate - audioBitrate
	if videoBitrate < 32_000 { // 编码器有效下限
		videoBitrate = 32_000
	}
	return CompressPlan{
		TargetBytes:  targetBytes,
		VideoBitrate: videoBitrate,
		AudioBitrate: audioBitrate,
		AudioAction:  audioAction,
		MinimumBytes: minimumBytes,
	}, nil
}

// ResolveTargetBytes 解析 --target-size：百分比（50%）、人读尺寸（100MB/1GB/65536）、
// 介质模板（cd/vcd/dvd…，复用 zpack 分卷模板语义——cd=700MB、dvd=4.7GB、bd50=50GB）
func ResolveTargetBytes(text string, originalBytes int64) (int64, error) {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return 0, fmt.Errorf("--target-size 为空")
	}
	// 百分比
	if strings.HasSuffix(text, "%") {
		percent, parseErr := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64)
		if parseErr != nil || percent <= 0 || percent > 100 {
			return 0, fmt.Errorf("--target-size 百分比需 0-100，收到: %s", text)
		}
		return int64(float64(originalBytes) * percent / 100), nil
	}
	// 介质模板（与 cli 分卷模板同值）
	if bytes, isTemplate := compressSizeTemplates[text]; isTemplate {
		return bytes, nil
	}
	// 人读尺寸（1024 基，与 --volume-size 数字语义一致）
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(text, "gb"), strings.HasSuffix(text, "g"):
		multiplier = 1 << 30
		text = strings.TrimSuffix(strings.TrimSuffix(text, "gb"), "g")
	case strings.HasSuffix(text, "mb"), strings.HasSuffix(text, "m"):
		multiplier = 1 << 20
		text = strings.TrimSuffix(strings.TrimSuffix(text, "mb"), "m")
	case strings.HasSuffix(text, "kb"), strings.HasSuffix(text, "k"):
		multiplier = 1 << 10
		text = strings.TrimSuffix(strings.TrimSuffix(text, "kb"), "k")
	case strings.HasSuffix(text, "b"):
		text = strings.TrimSuffix(text, "b")
	}
	value, parseErr := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if parseErr != nil || value <= 0 {
		return 0, fmt.Errorf("--target-size 需百分比（50%%）、尺寸（100MB）或模板（cd/dvd/bd50），收到: %s", text)
	}
	return value * multiplier, nil
}

// compressSizeTemplates 介质模板（cd/dvd 等标称容量，SI 精确字节——与 cli.VOLUME_TEMPLATES 同源）
var compressSizeTemplates = map[string]int64{
	"gmail": 25_000_000,
	"cd650": 650_000_000,
	"cd":    700_000_000,
	"vcd":   700_000_000,
	"dvd":   4_700_000_000,
	"dvd9":  8_500_000_000,
	"bd":    25_000_000_000,
	"bd50":  50_000_000_000,
}
