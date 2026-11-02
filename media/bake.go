// bake 一级命令：把显示元数据物理化（像素级固化）——「先烤再做别的」的统一入口。
// 可烤清单（BakeableKinds，与 info 的 bakeable 字段同源）：
//
//	rotation：display-matrix/rotate 标签 → transpose 像素重排（无损）
//	sar：非方像素 → 缩放至显示尺寸 + setsar=1（重采样）
//
// 未来候选（判定就绪、执行未接）：interlace（field_order）、color-range——暂不进默认清单。
package media

import "fmt"

// BAKE_KIND_ROTATION/SAR 可烤种类（结果 JSON 与 info.bakeable 同词表）
const (
	BakeKindRotation = "rotation"
	BakeKindSar      = "sar"
)

// BakeableKinds 探测事实 → 本文件可烤清单（纯函数，info 与 bake 命令共用）
func BakeableKinds(probe ProbeResult) []string {
	kinds := []string{}
	if probe.Rotation != 0 {
		kinds = append(kinds, BakeKindRotation)
	}
	if probe.Video != nil && probe.Video.SarNum > 0 && probe.Video.SarDen > 0 &&
		probe.Video.SarNum != probe.Video.SarDen {
		kinds = append(kinds, BakeKindSar)
	}
	if probe.Video != nil && probe.Video.IsVFR {
		kinds = append(kinds, BakeKindVfr)
	}
	if isHDR(probe) {
		kinds = append(kinds, BakeKindHdr)
	}
	return kinds
}

// isHDR 探测 HDR（PQ/HLG 传输特性或 bt2020 色彩空间）
func isHDR(probe ProbeResult) bool {
	if probe.Video == nil {
		return false
	}
	t := probe.Video.TransferName
	return t == "smpte2084" || t == "arib-std-b67" ||
		probe.Video.ColorSpaceName == "bt2020nc" || probe.Video.ColorSpaceName == "bt2020c"
}

const (
	BakeKindVfr = "vfr" // 变帧率→CFR（fps 滤镜，有损——丢/造帧）
	BakeKindHdr = "hdr" // HDR→SDR（tonemap，有损——色域压缩）
)

// BakePlan 烤入计划
type BakePlan struct {
	Kinds       []string // 将烤入的种类
	ScaleWidth  int      // sar 烤入时的目标尺寸（显示坐标系）
	ScaleHeight int
	TargetFps   float64 // VFR→CFR 目标帧率
	LossyFilter string  // 有损类滤镜段（fps/tonemap）
}

// PlanBake 计划：有可烤 → 全编（rotation 烤入 + sar 缩放归一）；无可烤 → remux 直拷
func PlanBake(probe ProbeResult) BakePlan {
	kinds := BakeableKinds(probe)
	plan := BakePlan{Kinds: kinds}
	hasSar := false
	for _, kind := range kinds {
		if kind == BakeKindSar {
			hasSar = true
		}
	}
	if hasSar {
		// 显示尺寸 = 存储×SAR±90°对调（transpose 先烤旋转，scale 落在显示坐标系）
		plan.ScaleWidth, plan.ScaleHeight = DisplayDims(probe)
	}
	// 有损类滤镜（vfr/hdr）追加在无损几何之后
	hasLossy := false
	for _, kind := range kinds {
		if kind == BakeKindVfr || kind == BakeKindHdr {
			hasLossy = true
			break
		}
	}
	if hasLossy {
		plan.TargetFps = probe.Video.Framerate // VFR→CFR 目标帧率取 avg
		plan.LossyFilter = BakeFilterChain(kinds, plan.TargetFps)
	}
	return plan
}

// Bake 执行：可烤元数据全部物理化；无可烤则 remux（幂等——输出行为正确且不再带显示元数据）
func Bake(input, output string, logf Logf) (ExecResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return ExecResult{}, fmt.Errorf("无法读取输入: %w", probeErr)
	}
	if probe.Video == nil {
		return ExecResult{}, fmt.Errorf("输入无视频流，无可烤元数据")
	}
	targetExt := containerExtOf(output)
	if audioOnlyTargets[targetExt] != "" {
		return ExecResult{}, fmt.Errorf("bake 需要视频容器输出（mp4/mkv/webm）")
	}
	plan := PlanBake(probe)
	if len(plan.Kinds) == 0 {
		logf("无可烤显示元数据（无旋转、方像素）——remux 直拷")
		result, execErr := executeFile(input, output, ModeRemux, probe, execParams{}, logf)
		if execErr != nil {
			return result, execErr
		}
		result.Mode = ModeBake
		result.Baked = []string{}
		return result, nil
	}
	logf("烤入: %v（rotation=transpose 无损重排；sar=缩放至显示尺寸+方像素）", plan.Kinds)
	params := execParams{rotation: probe.Rotation}
	if plan.ScaleWidth > 0 {
		params.scaleWidth = plan.ScaleWidth
		params.scaleHeight = plan.ScaleHeight
	}
	if plan.LossyFilter != "" {
		params.videoFilter = plan.LossyFilter // 有损类（fps/tonemap）走显式滤镜前缀
	}
	result, execErr := executeFile(input, output, ModeFull, probe, params, logf)
	if execErr != nil {
		return result, execErr
	}
	result.Mode = ModeBake
	result.Baked = plan.Kinds
	return result, nil
}
