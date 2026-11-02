// 图片转换/压缩/信息功能。
package image

import (
	"fmt"
	"image"
	"image/color"

	xdraw "golang.org/x/image/draw"
	"os"
	"path/filepath"
	"strings"
)

// Convert 格式转换：读源图 → 按输出扩展名编码
func Convert(input, output string, quality int) (*ImageResult, error) {
	if quality <= 0 || quality > 100 {
		quality = IMAGE_DEFAULT_QUALITY
	}
	img, sourceFormat, err := ReadImageOriented(input)
	if err != nil {
		return nil, err
	}
	outputExt := strings.TrimPrefix(filepath.Ext(output), ".")
	if outputExt == "" {
		return nil, fmt.Errorf("输出路径缺扩展名（如 .jpg/.png/.webp）")
	}
	if !isSupportedOutput(outputExt) {
		return nil, fmt.Errorf("不支持的输出格式 .%s（支持 %s）", outputExt, strings.Join(SupportedOutputFormats, "/"))
	}
	if err := WriteImage(output, img, outputExt, quality); err != nil {
		return nil, err
	}
	inputInfo, _ := os.Stat(input)
	outputInfo, _ := os.Stat(output)
	bounds := img.Bounds()
	return &ImageResult{
		Input:        absPath(input),
		Output:       absPath(output),
		SourceFormat: sourceFormat,
		TargetFormat: normalizeFormat(outputExt),
		Width:        bounds.Dx(),
		Height:       bounds.Dy(),
		OriginalSize: inputInfo.Size(),
		OutputSize:   outputInfo.Size(),
		Ratio:        ratio(outputInfo.Size(), inputInfo.Size()),
	}, nil
}

// Compress 图片压缩：同格式重编码（JPEG/WebP 降质量；PNG/GIF/BMP 尽力压缩容器）
// 缺省质量 80；输出缺省 = 同目录同名（覆盖警告见 CLI 层）
func Compress(input, output string, quality int) (*ImageResult, error) {
	if quality <= 0 || quality > 100 {
		quality = IMAGE_COMPRESS_QUALITY
	}
	img, sourceFormat, err := ReadImageOriented(input)
	if err != nil {
		return nil, err
	}
	// 输出格式跟随源（压缩不改格式）
	outputFormat := normalizeFormat(sourceFormat)
	if outputFormat == "" || !isSupportedOutput(outputFormat) {
		return nil, fmt.Errorf("源格式 %s 无法重编码压缩", sourceFormat)
	}
	if err := WriteImage(output, img, outputFormat, quality); err != nil {
		return nil, err
	}
	inputInfo, _ := os.Stat(input)
	outputInfo, _ := os.Stat(output)
	bounds := img.Bounds()
	return &ImageResult{
		Input:          absPath(input),
		Output:         absPath(output),
		SourceFormat:   sourceFormat,
		TargetFormat:   outputFormat,
		Width:          bounds.Dx(),
		Height:         bounds.Dy(),
		OriginalSize:   inputInfo.Size(),
		OutputSize:     outputInfo.Size(),
		Ratio:          ratio(outputInfo.Size(), inputInfo.Size()),
		OriginalFormat: sourceFormat,
	}, nil
}

// ImageInfo 图片元数据
type ImageInfo struct {
	Input      string `json:"input"`
	Format     string `json:"format"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SizeBytes  int64  `json:"size_bytes"`
	ColorModel string `json:"color_model"`
	// EXIF Orientation（0=无标签）；display_* = 观看态尺寸（orientation 5-8 与存储对调）
	Orientation   int `json:"orientation,omitzero"`
	DisplayWidth  int `json:"display_width,omitzero"`
	DisplayHeight int `json:"display_height,omitzero"`
}

// Info 读取图片信息（不解码像素——仅头部）
func Info(input string) (*ImageInfo, error) {
	file, err := os.Open(input)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// HDR 格式（EXR/Radiance/AVIF）走 astiav 探测（纯构建=需完整版桩）
	if hdrInfo, handled, hdrErr := probeHDRInfo(input); handled {
		if hdrErr != nil {
			return nil, hdrErr
		}
		return hdrInfo, nil
	}
	cfg, format, err := image.DecodeConfig(file)
	if err != nil {
		return nil, fmt.Errorf("无法读取图片信息 %s: %w", filepath.Base(input), err)
	}
	info, _ := os.Stat(input)
	colorModel := colorModelName(cfg.ColorModel)
	orientation := ReadOrientation(input)
	result := &ImageInfo{
		Input:      absPath(input),
		Format:     format,
		Width:      cfg.Width,
		Height:     cfg.Height,
		SizeBytes:  info.Size(),
		ColorModel: colorModel,
	}
	if orientation > 1 {
		result.Orientation = orientation
		result.DisplayWidth, result.DisplayHeight = cfg.Width, cfg.Height
		if OrientationSwapsDims(orientation) {
			result.DisplayWidth, result.DisplayHeight = cfg.Height, cfg.Width
		}
	}
	return result, nil
}

func isSupportedOutput(ext string) bool {
	ext = strings.ToLower(ext)
	for _, f := range SupportedOutputFormats {
		if f == ext {
			return true
		}
	}
	return false
}

func normalizeFormat(f string) string {
	switch strings.ToLower(f) {
	case "jpeg":
		return "jpg"
	case "tif":
		return "tiff"
	default:
		return strings.ToLower(f)
	}
}

func colorModelName(model color.Model) string {
	if model == nil {
		return "unknown"
	}
	// 类型断言检测常见色彩模型
	switch model {
	case color.RGBAModel:
		return "rgba"
	case color.NRGBAModel:
		return "nrgba"
	case color.GrayModel:
		return "gray"
	case color.Gray16Model:
		return "gray16"
	case color.CMYKModel:
		return "cmyk"
	case color.YCbCrModel:
		return "ycbcr"
	case color.NYCbCrAModel:
		return "ycbcra"
	default:
		return "other"
	}
}

func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func ratio(a, b int64) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// ── 缩放（降像素保观感——同视频 --screen 语义）──

// ResizeOptions 缩放参数（四选一：Width/Height/Percent/MaxBox）
type ResizeOptions struct {
	Width     int // 目标宽（高按比例）
	Height    int // 目标高（宽按比例）
	Percent   int // 缩放百分比（50 = 一半）
	MaxWidth  int // 适配框宽（只降不升）
	MaxHeight int // 适配框高
	Quality   int // 输出编码质量
}

// Resize 图片缩放：降像素保观感（CatmullRom 插值——x/image/draw 官方推荐的高质量缩放）
func Resize(input, output string, opts ResizeOptions) (*ImageResult, error) {
	img, sourceFormat, err := ReadImageOriented(input)
	if err != nil {
		return nil, err
	}
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()
	dstW, dstH := srcW, srcH

	switch {
	case opts.Width > 0 && opts.Height > 0:
		dstW, dstH = opts.Width, opts.Height
	case opts.Width > 0:
		dstW = opts.Width
		dstH = srcH * dstW / srcW
	case opts.Height > 0:
		dstH = opts.Height
		dstW = srcW * dstH / srcH
	case opts.Percent > 0 && opts.Percent < 100:
		dstW = srcW * opts.Percent / 100
		dstH = srcH * opts.Percent / 100
	case opts.MaxWidth > 0 && opts.MaxHeight > 0:
		// 适配框（只降不升保比例——同 FitWithinBox 语义）
		scale := float64(opts.MaxWidth) / float64(srcW)
		if hScale := float64(opts.MaxHeight) / float64(srcH); hScale < scale {
			scale = hScale
		}
		if scale < 1.0 {
			dstW = int(float64(srcW) * scale)
			dstH = int(float64(srcH) * scale)
		}
	}

	if dstW == srcW && dstH == srcH {
		// 无缩放 → 纯格式转换（或同格式直拷语义）
		return Convert(input, output, opts.Quality)
	}

	// CatmullRom 缩放（高质量插值——x/image/draw 官方推荐）
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Over, nil)

	outputExt := strings.TrimPrefix(filepath.Ext(output), ".")
	if outputExt == "" {
		outputExt = sourceFormat
	}
	if !isSupportedOutput(outputExt) {
		return nil, fmt.Errorf("不支持的输出格式 .%s", outputExt)
	}
	quality := opts.Quality
	if quality <= 0 {
		quality = IMAGE_DEFAULT_QUALITY
	}
	if err := WriteImage(output, dst, outputExt, quality); err != nil {
		return nil, err
	}
	inputInfo, _ := os.Stat(input)
	outputInfo, _ := os.Stat(output)
	return &ImageResult{
		Input: absPath(input), Output: absPath(output),
		SourceFormat: sourceFormat, TargetFormat: normalizeFormat(outputExt),
		Width: dstW, Height: dstH,
		OriginalSize: inputInfo.Size(), OutputSize: outputInfo.Size(),
		Ratio: ratio(outputInfo.Size(), inputInfo.Size()),
	}, nil
}

// ── 物理尺寸缩放（cm/inch + DPI → 像素）──

// CM_PER_INCH 1 英寸 = 2.54 厘米（国际标准）
const CM_PER_INCH = 2.54

// PHYSICAL_DEFAULT_PPI 物理尺寸模式缺省像素密度（300 = 印刷质量标准——人眼阅读距离分辨极限）
const PHYSICAL_DEFAULT_PPI = 300

// CmToPixels 厘米 → 像素（cm ÷ 2.54 × PPI——像素密度换算率）
func CmToPixels(cm float64, ppi int) int {
	if ppi <= 0 {
		ppi = PHYSICAL_DEFAULT_PPI
	}
	return int(cm/CM_PER_INCH*float64(ppi) + 0.5)
}

// InchToPixels 英寸 → 像素
func InchToPixels(inch float64, ppi int) int {
	if ppi <= 0 {
		ppi = PHYSICAL_DEFAULT_PPI
	}
	return int(inch*float64(ppi) + 0.5)
}

// PixelsToCm 像素 → 厘米（PPI 已知时）
func PixelsToCm(pixels int, ppi int) float64 {
	if ppi <= 0 {
		return 0
	}
	return float64(pixels) / float64(ppi) * CM_PER_INCH
}

// PhysicalSize 物理尺寸（cm；PPI 未知时为 0）
type PhysicalSize struct {
	WidthCm  float64 `json:"width_cm,omitempty"`
	HeightCm float64 `json:"height_cm,omitempty"`
	PPI      int     `json:"ppi,omitempty"`
}

// PhysicalPPI 取有效像素密度（0 → 缺省 300）
func PhysicalPPI(ppi int) int {
	if ppi <= 0 {
		return PHYSICAL_DEFAULT_PPI
	}
	return ppi
}

// MmToPixels 毫米 → 像素（mm → cm ÷ 10，再 cm ÷ 2.54 × PPI）
func MmToPixels(mm float64, ppi int) int {
	return CmToPixels(mm/10.0, ppi)
}
