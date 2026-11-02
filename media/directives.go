// 正交指令层（纯 Go）：--screen（尺寸上限）与 --codec（编码代际）——
// 任何模式可叠加；与决策矩阵的协作规则：需缩放或换码 ≠ 源 → 强制 full。
// media info 的 suggestions[].flag 直接对应这里的旗标（skill 教 agent 拼命令的闭环）。
package media

import (
	"fmt"
	"sort"
	"strings"
)

// SCREEN_BOXES 屏幕档位 → 适配框（只降不升保比例——FitWithinBox 语义）
var SCREEN_BOXES = map[string]struct{ Width, Height int }{
	"2160p": {3840, 2160},
	"1440p": {2560, 1440},
	"1080p": {1920, 1080},
	"720p":  {1280, 720},
	"480p":  {854, 480},
	"360p":  {640, 360},
}

// SCREEN_CODEC_NAMES 支持的 --codec 值（容器兼容性由决策矩阵白名单校验）
var SCREEN_CODEC_NAMES = map[string]bool{
	"h264": true, "hevc": true, "vp9": true, "av1": true,
}

// ValidateScreen 校验 --screen 档位
func ValidateScreen(name string) error {
	if _, ok := SCREEN_BOXES[name]; !ok {
		return fmt.Errorf("--screen 支持 2160p/1440p/1080p/720p/480p/360p，收到: %s", name)
	}
	return nil
}

// ValidateCodec 校验 --codec 编码名（且须与输出容器兼容）；空 = 未启用直接放行
func ValidateCodec(name, targetExt string) error {
	if name == "" {
		return nil
	}
	if !SCREEN_CODEC_NAMES[name] {
		return fmt.Errorf("--codec 支持 h264/hevc/vp9/av1，收到: %s", name)
	}
	container := ContainerForExt(targetExt)
	if container == "" {
		return nil // 未知容器按决策矩阵安全侧处理
	}
	compat := containerCompat[container]
	if compat.video == nil {
		return nil // mkv 全兼容
	}
	if !compat.video[name] {
		accepted := make([]string, 0, len(compat.video))
		for codecName := range compat.video {
			accepted = append(accepted, codecName)
		}
		sort.Strings(accepted)
		return fmt.Errorf("--codec %s 与容器 %s 不兼容（该容器接受: %s）", name, container, strings.Join(accepted, "/"))
	}
	return nil
}

// ApplyDirectives 指令叠加：--screen 缩放目标 + --codec 覆盖 + 强制 full 判定。
// 返回更新后的参数与是否需要全编（需缩放 或 换码≠源码）。
func ApplyDirectives(probe ProbeResult, screen, codec string, params execParams) (execParams, bool, error) {
	needsFull := false
	if screen != "" {
		if screenErr := ValidateScreen(screen); screenErr != nil {
			return params, false, screenErr
		}
		if probe.Video != nil {
			box := SCREEN_BOXES[screen]
			sourceWidth, sourceHeight := DisplayDims(probe) // 旋转 90/270 时按显示尺寸适配
			targetWidth, targetHeight := FitWithinBox(sourceWidth, sourceHeight, box.Width, box.Height)
			if targetWidth > 0 {
				params.scaleWidth = targetWidth
				params.scaleHeight = targetHeight
				needsFull = true // 缩放必须重编
			}
		}
	}
	params.rotation = probe.Rotation
	if codec != "" {
		params.videoCodec = codec
		if probe.Video != nil && probe.Video.CodecName != codec {
			needsFull = true // 换码 ≠ 源码 → 重编；同码可维持 remux
		}
	}
	return params, needsFull, nil
}

// DisplayDims 显示尺寸（可烤元数据 #1#2 全感知）：存储 → ×SAR（像素宽高比）→ ±90° 对调。
// --screen/--preset 适配与 info 显示尺寸统一走它——坐标系错误的 fit 就是压扁/裁错的根源
func DisplayDims(probe ProbeResult) (int, int) {
	if probe.Video == nil {
		return 0, 0
	}
	width, height := probe.Video.Width, probe.Video.Height
	if probe.Video.SarNum > 0 && probe.Video.SarDen > 0 {
		width = width * probe.Video.SarNum / probe.Video.SarDen
	}
	if probe.Rotation%180 != 0 {
		width, height = height, width
	}
	return width, height
}

// TransposeFilter 旋转烤入的滤镜段（顺时针角度；0 = 无需）；旋转是像素重排——无损
func TransposeFilter(rotation int) string {
	normalized := ((rotation % 360) + 360) % 360 // → 0/90/180/270
	switch normalized {
	case 90:
		return "transpose=1"
	case 180:
		return "transpose=1,transpose=1"
	case 270:
		return "transpose=2"
	default:
		return ""
	}
}
