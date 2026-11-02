package media

import (
	"strings"
	"testing"
)

// TestDecide SmartRender 决策矩阵（docs/unicoverter-reverse.md §5 复刻）
func TestDecide(t *testing.T) {
	h264Aac := ProbeResult{
		DurationSeconds: 10, Video: &StreamProbe{Kind: "video", CodecName: "h264", Bitrate: 8_000_000},
		Audio: &StreamProbe{Kind: "audio", CodecName: "aac", Bitrate: 128_000},
	}
	hevcOpus := ProbeResult{
		DurationSeconds: 10, Video: &StreamProbe{Kind: "video", CodecName: "hevc"},
		Audio: &StreamProbe{Kind: "audio", CodecName: "opus"},
	}
	rotated := h264Aac
	rotated.Rotation = 90

	cases := []struct {
		name   string
		probe  ProbeResult
		ext    string
		expect Mode
	}{
		{"mp4→mkv 全兼容直拷", h264Aac, "mkv", ModeRemux},
		{"mp4→mp4 同容器直拷", h264Aac, "mp4", ModeRemux},
		{"mkv(opus)→mp4 音频不兼容", hevcOpus, "mp4", ModeAudioOnly},
		{"mkv(hevc)→webm 视频不兼容", hevcOpus, "webm", ModeFull},
		{"旋转 90° remux 保矩阵零损（v2：烤入仅重编路径）", rotated, "mkv", ModeRemux},
		{"mp4→m4a 音频容器", h264Aac, "m4a", ModeAudioTrack},
		{"mp4→mp3 音频容器", h264Aac, "mp3", ModeAudioTrack},
		{"未知容器安全侧全编", h264Aac, "xyz", ModeFull},
	}
	for _, testCase := range cases {
		if got := Decide(testCase.probe, testCase.ext); got != testCase.expect {
			t.Errorf("%s: 期望 %s 实际 %s", testCase.name, testCase.expect, got)
		}
	}
}

// TestPlanCompression 业界主流压缩公式族（§3 直译）
func TestPlanCompression(t *testing.T) {
	// 10MB / 10s / 音频 128k → 目标 3MB：总≈2.376Mbps − 音频128k → 视频≈2.248Mbps
	plan, err := PlanCompression(10_345_081, 10.0, 128_000, 3_103_524, true)
	if err != nil {
		t.Fatalf("意外失败: %v", err)
	}
	if plan.VideoBitrate < 2_100_000 || plan.VideoBitrate > 2_400_000 {
		t.Errorf("视频码率 %.0f 不在预期区间", float64(plan.VideoBitrate))
	}
	if plan.AudioBitrate != 128_000 {
		t.Errorf("音频 128k 保留：实际 %d", plan.AudioBitrate)
	}
	if plan.MinimumBytes <= 0 {
		t.Errorf("下限应为正")
	}

	// 音频 >128k → 降级 128k（§3.1 阶梯）
	plan, _ = PlanCompression(10_000_000, 10.0, 320_000, 5_000_000, true)
	if plan.AudioBitrate != 128_000 || plan.AudioAction != "down-to-128k" {
		t.Errorf("高码率音频应降 128k：实际 %d %s", plan.AudioBitrate, plan.AudioAction)
	}

	// 下限保护（§3.4：原始×10% + 音频）
	_, err = PlanCompression(10_000_000, 10.0, 128_000, 500_000, true) // 下限≈1MB+160k
	if err == nil || !strings.Contains(err.Error(), "下限") {
		t.Errorf("低于下限应报错，实际 err=%v", err)
	}

	// 纯音频：码率=大小×8/时长 全给音频（§3.2）
	plan, err = PlanCompression(2_000_000, 8.0, 256_000, 640_000, false)
	if err != nil {
		t.Fatalf("纯音频意外失败: %v", err)
	}
	if plan.VideoBitrate != 0 {
		t.Errorf("纯音频不应有视频码率")
	}
	expectAudio := int64(640_000 * 8 / 8.0 * 0.99)
	if plan.AudioBitrate < expectAudio-2000 || plan.AudioBitrate > expectAudio+2000 {
		t.Errorf("纯音频码率 ≈%d 实际 %d", expectAudio, plan.AudioBitrate)
	}
}

// TestResolveTargetBytes 目标大小解析（百分比/尺寸/模板）
func TestResolveTargetBytes(t *testing.T) {
	cases := []struct {
		text    string
		orig    int64
		expect  int64
		wantErr bool
	}{
		{"50%", 10_000_000, 5_000_000, false},
		{"100MB", 0, 100 << 20, false},
		{"cd", 0, 700_000_000, false}, // SI 介质标称
		{"dvd", 0, 4_700_000_000, false},
		{"65536", 0, 65536, false},
		{"64KB", 0, 65536, false},
		{"150%", 0, 0, true},
		{"abc", 0, 0, true},
		{"", 0, 0, true},
	}
	for _, testCase := range cases {
		got, err := ResolveTargetBytes(testCase.text, testCase.orig)
		if testCase.wantErr {
			if err == nil {
				t.Errorf("%q: 应报错", testCase.text)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: 意外失败 %v", testCase.text, err)
			continue
		}
		if got != testCase.expect {
			t.Errorf("%q: 期望 %d 实际 %d", testCase.text, testCase.expect, got)
		}
	}
}

// TestPresets 预设注册表与查档
func TestPresets(t *testing.T) {
	if PresetCount() < 8 {
		t.Errorf("起步预设应 ≥8，实际 %d", PresetCount())
	}
	for _, id := range []string{"youtube", "bilibili", "douyin", "gmail", "discord", "wechat", "iphone", "appletv"} {
		preset, found := LookupPreset(id)
		if !found {
			t.Errorf("预设 %s 缺失", id)
			continue
		}
		if preset.Container == "" || preset.Name == "" || preset.Group == "" {
			t.Errorf("预设 %s 字段不全", id)
		}
	}
	if _, found := LookupPreset("no-such"); found {
		t.Error("未知预设不应命中")
	}
	// messaging 类必须有大小上限
	for _, id := range []string{"gmail", "discord", "wechat"} {
		preset, _ := LookupPreset(id)
		if preset.MaxSizeBytes <= 0 {
			t.Errorf("messaging 预设 %s 缺 max_size_bytes", id)
		}
	}
}

// TestFitWithinBox 缩放适配（只降不升、保比例、偶数对齐）
func TestFitWithinBox(t *testing.T) {
	cases := []struct {
		name             string
		srcW, srcH       int
		boxW, boxH       int
		expectW, expectH int
	}{
		{"源小于框不缩放", 1280, 720, 1920, 1080, 0, 0},
		{"源等于框不缩放", 1920, 1080, 1920, 1080, 0, 0},
		{"4K→1080p", 3840, 2160, 1920, 1080, 1920, 1080},
		{"超宽保比例", 2560, 800, 1920, 1080, 1920, 600},
		{"竖屏源入横框", 1080, 2400, 1920, 1080, 486, 1080},
	}
	for _, testCase := range cases {
		gotW, gotH := FitWithinBox(testCase.srcW, testCase.srcH, testCase.boxW, testCase.boxH)
		if gotW != testCase.expectW || gotH != testCase.expectH {
			t.Errorf("%s: 期望 %dx%d 实际 %dx%d", testCase.name, testCase.expectW, testCase.expectH, gotW, gotH)
		}
	}
}

// TestAnalyzeCompression 体检建议规则（确定性）
func TestAnalyzeCompression(t *testing.T) {
	// 4K h264：分辨率 + 代际两条
	probe := ProbeResult{
		Video: &StreamProbe{CodecName: "h264", Width: 3840, Height: 2160, Framerate: 30, Bitrate: 24_000_000},
		Audio: &StreamProbe{CodecName: "aac", Bitrate: 128_000},
	}
	advice := AnalyzeCompression(probe)
	kinds := map[string]bool{}
	for _, item := range advice {
		kinds[item.Kind] = true
	}
	if !kinds["resolution"] || !kinds["codec-generation"] {
		t.Errorf("4K h264 应有分辨率+代际建议，实际 %v", kinds)
	}

	// 1080p hevc 健康码率：无建议
	probe = ProbeResult{
		Video: &StreamProbe{CodecName: "hevc", Width: 1920, Height: 1080, Framerate: 30, Bitrate: 6_000_000},
	}
	if advice = AnalyzeCompression(probe); len(advice) != 0 {
		t.Errorf("健康 1080p hevc 不应建议，实际 %d 条", len(advice))
	}

	// 码率虚高：1080p30 40Mbps → bpp=0.64
	probe = ProbeResult{
		Video: &StreamProbe{CodecName: "hevc", Width: 1920, Height: 1080, Framerate: 30, Bitrate: 40_000_000},
	}
	advice = AnalyzeCompression(probe)
	found := false
	for _, item := range advice {
		if item.Kind == "bitrate-inflated" {
			found = true
		}
	}
	if !found {
		t.Error("40Mbps@1080p30 应触发码率虚高")
	}

	// 纯音频高码率：音频建议
	probe = ProbeResult{Audio: &StreamProbe{CodecName: "aac", Bitrate: 320_000}}
	advice = AnalyzeCompression(probe)
	if len(advice) != 1 || advice[0].Kind != "audio" {
		t.Errorf("纯音频 320k 应只给音频建议，实际 %v", advice)
	}
}

// TestApplyDirectives 正交指令（--screen/--codec）规则
func TestApplyDirectives(t *testing.T) {
	probe4K := ProbeResult{Video: &StreamProbe{CodecName: "h264", Width: 3840, Height: 2160, Bitrate: 20_000_000}}

	// --screen 1080p：4K 缩放 → 强制 full
	params, needsFull, err := ApplyDirectives(probe4K, "1080p", "", execParams{})
	if err != nil || !needsFull || params.scaleWidth != 1920 || params.scaleHeight != 1080 {
		t.Errorf("4K→1080p: needsFull=%v scale=%dx%d err=%v", needsFull, params.scaleWidth, params.scaleHeight, err)
	}
	// 小源：不升档 → 不强制
	probe720 := ProbeResult{Video: &StreamProbe{CodecName: "h264", Width: 1280, Height: 720}}
	_, needsFull, err = ApplyDirectives(probe720, "1080p", "", execParams{})
	if err != nil || needsFull {
		t.Errorf("720p 入 1080p 框不应缩放/强制：needsFull=%v err=%v", needsFull, err)
	}
	// --codec hevc：换码 ≠ 源 → 强制；同码 → 不强制
	_, needsFull, _ = ApplyDirectives(probe4K, "", "hevc", execParams{})
	if !needsFull {
		t.Error("h264→hevc 应强制重编")
	}
	_, needsFull, _ = ApplyDirectives(probe4K, "", "h264", execParams{})
	if needsFull {
		t.Error("同码不应强制")
	}
	// 非法值
	if _, _, err = ApplyDirectives(probe4K, "1080i", "", execParams{}); err == nil {
		t.Error("非法屏幕档应报错")
	}
	if err = ValidateCodec("mpeg2", "mp4"); err == nil {
		t.Error("非法编码应报错")
	}
	if err = ValidateCodec("h264", "webm"); err == nil {
		t.Error("h264 不兼容 webm 应报错")
	}
	if err = ValidateCodec("", "mp4"); err != nil {
		t.Error("空 codec（未启用）应放行")
	}
}

// TestRotationHelpers 旋转纯函数（DisplayDims 对调 / TransposeFilter 映射）
func TestRotationHelpers(t *testing.T) {
	landscape := ProbeResult{Rotation: 90, Video: &StreamProbe{Width: 3840, Height: 2160}}
	w, h := DisplayDims(landscape)
	if w != 2160 || h != 3840 {
		t.Errorf("±90° 应对调显示尺寸：实际 %dx%d", w, h)
	}
	landscape.Rotation = 180
	if w, h = DisplayDims(landscape); w != 3840 || h != 2160 {
		t.Errorf("180° 不对调：实际 %dx%d", w, h)
	}
	cases := map[int]string{0: "", 90: "transpose=1", 180: "transpose=1,transpose=1", 270: "transpose=2", -90: "transpose=2", 450: "transpose=1"}
	for angle, expect := range cases {
		if got := TransposeFilter(angle); got != expect {
			t.Errorf("TransposeFilter(%d)=%q 期望 %q", angle, got, expect)
		}
	}
}

// TestSubtitleCompat 字幕容器兼容表
func TestSubtitleCompat(t *testing.T) {
	if !subtitleCompat["mp4"]["mov_text"] {
		t.Error("mp4 应接受 mov_text")
	}
	if subtitleCompat["mp4"]["srt"] {
		t.Error("mp4 不应接受 srt（需文本字幕转换）")
	}
	if subtitleCompat["mkv"] != nil {
		t.Error("mkv 应全兼容（nil）")
	}
}

// TestTransform 变换命令纯层
func TestTransform(t *testing.T) {
	if TransformFilter(TransformRotateLeft) != "transpose=2" || TransformFilter(TransformRotateRight) != "transpose=1" {
		t.Error("±90° 滤镜映射错误")
	}
	if TransformFilter(TransformMirror) != "hflip" || TransformFilter(TransformRotate180) != "transpose=1,transpose=1" {
		t.Error("180/镜像滤镜映射错误")
	}
	if TransformFilter(TransformFlip) != "vflip" {
		t.Error("上下镜像应为 vflip")
	}
	if !TransformSwapsDims(TransformRotateRight) || TransformSwapsDims(TransformMirror) || TransformSwapsDims(TransformRotate180) {
		t.Error("宽高交换判定错误：仅 ±90° 交换")
	}
	// 净角度合成：源 90 + 右旋 90 = 180；源 -90 + 左旋(-90) = -180→180
	if NetMatrixAngle(90, TransformRotateRight) != 180 {
		t.Errorf("90+90 应为 180，实际 %d", NetMatrixAngle(90, TransformRotateRight))
	}
	if NetMatrixAngle(-90, TransformRotateLeft) != 180 {
		t.Errorf("-90-90 应归一 180，实际 %d", NetMatrixAngle(-90, TransformRotateLeft))
	}
	// 计划：无矩阵源 + 兼容容器 → 矩阵模式；有矩阵源 → 烤入；mirror 恒烤入；--bake 强制烤入
	clean := ProbeResult{Video: &StreamProbe{CodecName: "h264", Width: 1920, Height: 1080}}
	if plan := PlanTransform(clean, TransformRotateRight, "mp4", false); !plan.MatrixMode {
		t.Error("无矩阵源应走零损矩阵")
	}
	rotated := clean
	rotated.Rotation = 90
	if plan := PlanTransform(rotated, TransformRotateLeft, "mp4", false); plan.MatrixMode {
		t.Error("已有矩阵源应烤入（Add 不可替换）")
	}
	if plan := PlanTransform(clean, TransformRotateRight, "mp4", true); plan.MatrixMode {
		t.Error("--bake 应强制烤入")
	}
	if plan := PlanTransform(clean, TransformMirror, "mp4", false); plan.MatrixMode {
		t.Error("mirror 恒烤入")
	}
}

// TestBake 一级 bake 命令纯层
func TestBake(t *testing.T) {
	clean := ProbeResult{Video: &StreamProbe{Width: 1920, Height: 1080}}
	if kinds := BakeableKinds(clean); len(kinds) != 0 {
		t.Errorf("干净源应无可烤，实际 %v", kinds)
	}
	rotSar := ProbeResult{Rotation: -90, Video: &StreamProbe{Width: 720, Height: 576, SarNum: 16, SarDen: 15}}
	kinds := BakeableKinds(rotSar)
	if len(kinds) != 2 || kinds[0] != "rotation" || kinds[1] != "sar" {
		t.Errorf("旋转+SAR 双烤清单错误: %v", kinds)
	}
	plan := PlanBake(rotSar)
	// 显示尺寸：720×16/15=768 → 旋转对调 → 576x768
	if plan.ScaleWidth != 576 || plan.ScaleHeight != 768 {
		t.Errorf("SAR 烤入目标应为显示坐标系 576x768，实际 %dx%d", plan.ScaleWidth, plan.ScaleHeight)
	}
	rotOnly := ProbeResult{Rotation: 90, Video: &StreamProbe{Width: 1920, Height: 1080}}
	plan = PlanBake(rotOnly)
	if plan.ScaleWidth != 0 {
		t.Error("纯旋转不应触发缩放（transpose 已完成尺寸对调）")
	}
}
