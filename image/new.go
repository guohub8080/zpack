// 新建空白图片（占位图）：纯色或透明；像素/物理尺寸+分辨率/纸型三种定尺寸形态。
package image

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NEW_DEFAULT_PPI 物理尺寸缺省分辨率（300 = 印刷标准，同 resize --ppi）
const NEW_DEFAULT_PPI = 300

// NEW_PAPER_SIZES 常用纸型（pt，竖版基准；同 pdf resize 家族——尾缀 L/P 定方向）
var NEW_PAPER_SIZES = map[string][2]float64{
	"a0": {2384, 3370}, "a1": {1684, 2384}, "a2": {1191, 1684},
	"a3": {842, 1191}, "a4": {595, 842}, "a5": {420, 595},
	"letter": {612, 792}, "legal": {612, 1008}, "ledger": {1224, 792},
}

// NEW_NAMED_COLORS 常用色名（CSS 基础集子集）
var NEW_NAMED_COLORS = map[string]color.NRGBA{
	"white": {255, 255, 255, 255}, "black": {0, 0, 0, 255},
	"red": {255, 0, 0, 255}, "green": {0, 128, 0, 255}, "blue": {0, 0, 255, 255},
	"yellow": {255, 255, 0, 255}, "cyan": {0, 255, 255, 255}, "magenta": {255, 0, 255, 255},
	"gray": {128, 128, 128, 255}, "grey": {128, 128, 128, 255},
	"orange": {255, 165, 0, 255}, "purple": {128, 0, 128, 255},
	"pink": {255, 192, 203, 255}, "brown": {165, 42, 42, 255},
}

// NewBlank 新建空白图。sizeSpec：<WxH> 像素 | <WxH><mm|cm|in>（--ppi 换算）| 纸型（a4/a4l/letter…）。
// colorSpec：transparent（缺省）| 色名 | #RRGGBB[AA]。
func NewBlank(output, sizeSpec, colorSpec string, ppi, quality int) (*ImageResult, error) {
	width, height, sizeErr := ParseNewSizeSpec(sizeSpec, ppi)
	if sizeErr != nil {
		return nil, sizeErr
	}
	fill, colorErr := ParseNewColorSpec(colorSpec)
	if colorErr != nil {
		return nil, colorErr
	}
	outputFormat := strings.ToLower(strings.TrimPrefix(filepath.Ext(output), "."))
	if outputFormat == "jpeg" {
		outputFormat = "jpg"
	}
	if outputFormat == "" {
		outputFormat = "png" // 占位缺省 PNG
	}
	if !isSupportedOutput(outputFormat) {
		return nil, fmt.Errorf("输出格式不支持: %s（支持 jpg/png/webp/bmp/tiff/gif）", outputFormat)
	}
	// 透明按编码器真实 alpha 能力划线：png/webp/tiff 保真；jpg/bmp/gif 会丢（明确报错不静默）
	if fill == nil && formatLosesAlpha(outputFormat) {
		return nil, fmt.Errorf(".%s 不保真透明——用 .png/.webp/.tiff 或 --color 指定底色", outputFormat)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	if fill != nil {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				img.SetNRGBA(x, y, *fill)
			}
		}
	}
	if writeErr := WriteImage(output, img, outputFormat, quality); writeErr != nil {
		return nil, writeErr
	}
	outputInfo, _ := os.Stat(output)
	return &ImageResult{
		Input:        absPath(output),
		Output:       absPath(output),
		SourceFormat: "blank",
		TargetFormat: outputFormat,
		Width:        width,
		Height:       height,
		OutputSize:   outputInfo.Size(),
	}, nil
}

// formatLosesAlpha 该格式的编码路径会丢 alpha（jpg 无通道；bmp 32bit 的
// alpha 字节查看器普遍忽略；gif 调色板量化丢弃）
func formatLosesAlpha(format string) bool {
	switch format {
	case "jpg", "bmp", "gif":
		return true
	}
	return false
}

// ParseNewSizeSpec 尺寸规格：<800x600> 像素 | <55x90mm|cm|in>（ppi 换算）| <a4|a4l|letter…>（72dpi pt → ppi 换算）
func ParseNewSizeSpec(spec string, ppi int) (int, int, error) {
	trimmed := strings.ToLower(strings.TrimSpace(spec))
	if trimmed == "" {
		return 0, 0, fmt.Errorf("缺少尺寸（如 800x600 / 55x90mm --ppi 300 / a4）")
	}
	if ppi <= 0 {
		ppi = NEW_DEFAULT_PPI
	}
	// 纸型（可带 L/P 尾缀）
	core := trimmed
	landscape := false
	portrait := false
	if strings.HasSuffix(core, "l") {
		core, landscape = core[:len(core)-1], true
	} else if strings.HasSuffix(core, "p") {
		core, portrait = core[:len(core)-1], true
	}
	if paper, isPaper := NEW_PAPER_SIZES[core]; isPaper {
		wPt, hPt := paper[0], paper[1]
		if (landscape && wPt < hPt) || (portrait && wPt > hPt) {
			wPt, hPt = hPt, wPt
		}
		// pt → px：pt/72*ppi
		return pixelsRound(wPt * float64(ppi) / 72), pixelsRound(hPt * float64(ppi) / 72), nil
	}
	// 数字 WxH[单位]
	unit := ""
	for _, suffix := range []string{"mm", "cm", "in"} {
		if strings.HasSuffix(trimmed, suffix) {
			unit = suffix
			trimmed = strings.TrimSuffix(trimmed, suffix)
			break
		}
	}
	parts := strings.SplitN(trimmed, "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("尺寸规格非法: %s（像素 800x600 / 物理 55x90mm + --ppi / 纸型 a4）", spec)
	}
	wValue, wErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	hValue, hErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if wErr != nil || hErr != nil || wValue <= 0 || hValue <= 0 {
		return 0, 0, fmt.Errorf("尺寸需正数: %s", spec)
	}
	multiplier := 1.0 // 缺省像素
	switch unit {
	case "mm":
		multiplier = float64(ppi) / 25.4
	case "cm":
		multiplier = float64(ppi) / 2.54
	case "in":
		multiplier = float64(ppi)
	}
	return pixelsRound(wValue * multiplier), pixelsRound(hValue * multiplier), nil
}

// ParseNewColorSpec 颜色规格：transparent（缺省，nil）/ 色名 / #RRGGBB[AA] / RRGGBB
func ParseNewColorSpec(spec string) (*color.NRGBA, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" || strings.EqualFold(trimmed, "transparent") {
		return nil, nil // nil = 全透明
	}
	if named, isNamed := NEW_NAMED_COLORS[strings.ToLower(trimmed)]; isNamed {
		fill := named
		return &fill, nil
	}
	hex := strings.TrimPrefix(trimmed, "#")
	var r, g, b, a uint8
	a = 255
	switch len(hex) {
	case 6:
		if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err != nil {
			return nil, fmt.Errorf("颜色需 transparent/色名/#RRGGBB，收到: %s", spec)
		}
	case 8:
		if _, err := fmt.Sscanf(hex, "%02x%02x%02x%02x", &r, &g, &b, &a); err != nil {
			return nil, fmt.Errorf("颜色需 transparent/色名/#RRGGBB[AA]，收到: %s", spec)
		}
	default:
		return nil, fmt.Errorf("颜色需 transparent/色名/#RRGGBB（6 位）或 #RRGGBBAA（8 位），收到: %s", spec)
	}
	return &color.NRGBA{r, g, b, a}, nil
}

// pixelsRound 半值进位取整（≥1 下限）
func pixelsRound(v float64) int {
	pixels := int(v + 0.5)
	if pixels < 1 {
		pixels = 1
	}
	return pixels
}
