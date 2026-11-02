// 预设库：平台/设备档位（官方公开规格整理，见 presets.json 的 source_note）。
// 纯 Go（无 cgo）——纯构建下 `media preset` 列表/详情同样可用；社区共建 = 直接改 JSON 提 PR。
package media

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"sort"
)

//go:embed presets.json
var presetsDocument []byte

// Preset 单个设备/平台预设
type Preset struct {
	Name         string `json:"name"`
	Group        string `json:"group"`
	Container    string `json:"container"` // 目标扩展名（mp4/mkv/...）
	Width        int    `json:"width"`     // 0=跟随源；>0=适配上限（只降不升、保比例）
	Height       int    `json:"height"`
	VideoBitrate int64  `json:"video_bitrate"`  // 0=跟随源；>0=上限（源超 1.1× 强制重编）
	AudioBitrate int64  `json:"audio_bitrate"`  // 0=默认 128k
	MaxSizeBytes int64  `json:"max_size_bytes"` // >0=messaging 类压缩目标上限
	Note         string `json:"note"`
}

type presetFile struct {
	Version int               `json:"version"`
	Presets map[string]Preset `json:"presets"`
}

// presetRegistry 解析后的注册表（包级一次）
var presetRegistry = func() map[string]Preset {
	var file presetFile
	if err := json.Unmarshal(presetsDocument, &file); err != nil {
		panic("预设文件损坏: " + err.Error())
	}
	return file.Presets
}()

// LookupPreset 按 ID 查预设
func LookupPreset(id string) (Preset, bool) {
	preset, exists := presetRegistry[id]
	return preset, exists
}

// ListPresets 全部预设（按组分组、组内按 ID 排序——稳定输出）
func ListPresets() map[string][]string {
	grouped := map[string][]string{}
	for id, preset := range presetRegistry {
		grouped[preset.Group] = append(grouped[preset.Group], id)
	}
	for group := range grouped {
		sort.Strings(grouped[group])
	}
	return grouped
}

// PresetCount 预设总数
func PresetCount() int {
	return len(presetRegistry)
}

// FitWithinBox 计算适配目标尺寸：源不超框 → (0,0) 不缩放；超框 → 等比缩入（偶数对齐——
// 编码器要求），纯 Go 便于单测
func FitWithinBox(sourceWidth, sourceHeight, boxWidth, boxHeight int) (int, int) {
	if sourceWidth <= 0 || sourceHeight <= 0 || boxWidth <= 0 || boxHeight <= 0 {
		return 0, 0
	}
	if sourceWidth <= boxWidth && sourceHeight <= boxHeight {
		return 0, 0
	}
	scaleRatio := min(float64(boxWidth)/float64(sourceWidth), float64(boxHeight)/float64(sourceHeight))
	targetWidth := evenAlign(int(float64(sourceWidth)*scaleRatio + 0.5))
	targetHeight := evenAlign(int(float64(sourceHeight)*scaleRatio + 0.5))
	if targetWidth < 2 || targetHeight < 2 {
		return 0, 0
	}
	return targetWidth, targetHeight
}

func evenAlign(value int) int {
	if value%2 == 1 {
		return value - 1
	}
	return value
}

// applyPresetToParams 预设 → 执行参数：缩放目标 + 码率上限
func applyPresetToParams(preset Preset, probe ProbeResult, params execParams) (execParams, bool) {
	needsFull := false
	// 码率超限 1.1× → 强制重编（源画质超预算）
	if preset.VideoBitrate > 0 && probe.Video != nil && probe.Video.Bitrate > preset.VideoBitrate*110/100 {
		needsFull = true
	}
	// 尺寸超框 → 需要缩放 → 必须重编（旋转感知：±90° 按显示尺寸适配）
	if probe.Video != nil && preset.Width > 0 && preset.Height > 0 {
		sourceWidth, sourceHeight := DisplayDims(probe)
		targetWidth, targetHeight := FitWithinBox(sourceWidth, sourceHeight, preset.Width, preset.Height)
		if targetWidth > 0 {
			params.scaleWidth = targetWidth
			params.scaleHeight = targetHeight
			needsFull = true
		}
	}
	if preset.VideoBitrate > 0 && needsFull {
		params.videoBitrate = preset.VideoBitrate
	}
	if preset.AudioBitrate > 0 {
		params.audioBitrate = preset.AudioBitrate
	}
	// 重编时烤入源旋转（与 ApplyDirectives 同纪律——漏了则 preset 降档重编丢矩阵，竖拍输出躺倒）
	params.rotation = probe.Rotation
	return params, needsFull
}

// ConvertPreset 预设转换：预设约束叠加决策矩阵（超码率/需缩放 → 强制 full）
func ConvertPreset(input, output string, preset Preset, logf Logf) (ExecResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return ExecResult{}, fmt.Errorf("无法读取输入: %w", probeErr)
	}
	targetExt := containerExtOf(output)
	if targetExt != "" && targetExt != preset.Container {
		return ExecResult{}, fmt.Errorf("输出扩展名 .%s 与预设 %s 的容器 %s 不符", targetExt, preset.Container, preset.Container)
	}
	mode := Decide(probe, preset.Container)
	params := execParams{}
	params, needsFull := applyPresetToParams(preset, probe, params)
	if needsFull && probe.Video != nil {
		mode = ModeFull
	}
	logf("预设 %s（%s）决策: %s", preset.Name, preset.Note, mode)
	result, execErr := executeFile(input, output, mode, probe, params, logf)
	if execErr != nil {
		return result, execErr
	}
	result.Mode = mode
	return result, nil
}

// CompressPreset 预设压缩：目标 = max_size_bytes；源已达标 → 直拷贝（remux）不重编
func CompressPreset(input, output string, preset Preset, logf Logf) (ExecResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if preset.MaxSizeBytes <= 0 {
		return ExecResult{}, fmt.Errorf("预设 %s 未定义大小上限（max_size_bytes），不能用于 compress", preset.Name)
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return ExecResult{}, fmt.Errorf("无法读取输入: %w", probeErr)
	}
	// 已达标：直拷贝（业界主流「无损」语义——不浪费画质）
	if probe.SizeBytes <= preset.MaxSizeBytes {
		logf("源 %d 字节已符合 %s 上限 %d——直拷贝不重编", probe.SizeBytes, preset.Name, preset.MaxSizeBytes)
		result, execErr := executeFile(input, output, ModeRemux, probe, execParams{}, logf)
		if execErr != nil {
			return result, execErr
		}
		result.Mode = ModeRemux
		result.AlreadyFit = true
		return result, nil
	}
	return Compress(input, output, preset.MaxSizeBytes, logf)
}

func containerExtOf(path string) string {
	ext := extOf(path)
	if len(ext) > 1 {
		return ext[1:]
	}
	return ""
}

// extOf 路径扩展名（含点；无则空串）
func extOf(path string) string {
	for idx := len(path) - 1; idx >= 0; idx-- {
		if path[idx] == '.' {
			return path[idx:]
		}
	}
	return ""
}
