// 几何变换命令层（rotate-left/rotate-right/rotate-180/mirror）。
// 旋转族双路径：零损写 display-matrix（源无矩阵时，秒级）或 transpose 烤入（重编）；
// mirror 恒烤入（矩阵镜像播放器支持不可靠）。
package media

// TransformKind 变换种类
type TransformKind string

const (
	TransformRotateLeft  TransformKind = "rotate-left"  // 逆时针 90°
	TransformRotateRight TransformKind = "rotate-right" // 顺时针 90°
	TransformRotate180   TransformKind = "rotate-180"   // 180°
	TransformMirror      TransformKind = "mirror"       // 水平镜像（左右翻，hflip）
	TransformFlip        TransformKind = "flip"         // 垂直镜像（上下翻，vflip）
)

// transformDelta 各变换的顺时针角度增量（mirror 无角度语义）
var transformDelta = map[TransformKind]int{
	TransformRotateLeft:  -90,
	TransformRotateRight: 90,
	TransformRotate180:   180,
}

// TransformFilter 变换 → 烤入滤镜段（与方向经 ffmpeg autorotate 参考帧校准的同一套约定）
func TransformFilter(kind TransformKind) string {
	switch kind {
	case TransformRotateLeft:
		return "transpose=2"
	case TransformRotateRight:
		return "transpose=1"
	case TransformRotate180:
		return "transpose=1,transpose=1"
	case TransformMirror:
		return "hflip"
	case TransformFlip:
		return "vflip"
	default:
		return ""
	}
}

// TransformSwapsDims 变换是否交换宽高（±90° 交换；180/镜像不变）
func TransformSwapsDims(kind TransformKind) bool {
	return kind == TransformRotateLeft || kind == TransformRotateRight
}

// NetMatrixAngle 矩阵模式的角度合成：源旋转 + 变换增量，归一到 (-180, 180]
func NetMatrixAngle(sourceRotation int, kind TransformKind) int {
	delta, hasDelta := transformDelta[kind]
	if !hasDelta {
		return sourceRotation
	}
	net := (sourceRotation + delta) % 360
	if net > 180 {
		net -= 360
	}
	if net <= -180 {
		net += 360
	}
	return net
}

// TransformPlan 变换执行计划
type TransformPlan struct {
	Kind        TransformKind
	MatrixMode  bool   // 零损写矩阵路径（不重编）
	MatrixAngle int    // MatrixMode 时的目标角度（顺时针）
	Filter      string // 烤入滤镜段
}

// PlanTransform 变换计划：旋转族在源无矩阵、容器兼容、未强制 --bake 时走零损矩阵；
// 否则烤入（重编）。mirror 恒烤入。
func PlanTransform(probe ProbeResult, kind TransformKind, targetExt string, forceBake bool) TransformPlan {
	if kind == TransformMirror || kind == TransformFlip {
		return TransformPlan{Kind: kind, Filter: TransformFilter(kind)}
	}
	container := ContainerForExt(targetExt)
	compat := containerCompat[container]
	videoCompatible := probe.Video != nil && (compat.video == nil || compat.video[probe.Video.CodecName])
	canMatrix := !forceBake && probe.Rotation == 0 && videoCompatible && container != ""
	if canMatrix {
		return TransformPlan{
			Kind:        kind,
			MatrixMode:  true,
			MatrixAngle: NetMatrixAngle(probe.Rotation, kind),
			Filter:      "",
		}
	}
	// 烤入路径：重编（源矩阵无法替换时也是这里——Add 只追加不覆盖）
	return TransformPlan{Kind: kind, Filter: TransformFilter(kind)}
}

// Transform 执行几何变换
func Transform(input, output string, kind TransformKind, forceBake bool, logf Logf) (ExecResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return ExecResult{}, probeErr
	}
	if probe.Video == nil {
		return ExecResult{}, errNoVideoStream
	}
	targetExt := containerExtOf(output)
	if targetExt == "" || audioOnlyTargets[targetExt] != "" {
		return ExecResult{}, errTransformNeedsVideoContainer
	}
	plan := PlanTransform(probe, kind, targetExt, forceBake)
	if plan.MatrixMode {
		logf("零损变换：写 display-matrix %d°（编码不变，秒级完成）", plan.MatrixAngle)
		result, execErr := executeFile(input, output, ModeRemux, probe, execParams{
			writeMatrixAngle: plan.MatrixAngle,
			hasMatrixWrite:   true,
		}, logf)
		if execErr != nil {
			return result, execErr
		}
		result.Mode = ModeTransformMatrix
		return result, nil
	}
	// 烤入路径：旋转族烤「净角度」（源旋转 + 命令增量——源矩阵随重编丢失，必须一并烤实）；
	// mirror = 源旋转烤实 + hflip（引擎链序：transpose → hflip → scale → format）
	params := execParams{}
	if kind == TransformMirror || kind == TransformFlip {
		params.rotation = probe.Rotation // 源旋转先烤实（0 则无段）
		params.videoFilter = plan.Filter
		logf("烤入变换：源旋转 %d° + %s（重编码）", probe.Rotation, plan.Filter)
	} else {
		params.rotation = NetMatrixAngle(probe.Rotation, kind)
		logf("烤入变换：净角度 %d°（源 %d° + 命令增量，重编码）", params.rotation, probe.Rotation)
	}
	result, execErr := executeFile(input, output, ModeFull, probe, params, logf)
	if execErr != nil {
		return result, execErr
	}
	result.Mode = ModeTransformBake
	return result, nil
}
