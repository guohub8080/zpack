// Package image —— 图片格式转换与压缩（纯 Go：标准库 + x/image + nativewebp）。
// 输入: jpg/jpeg/png/gif/bmp/tiff/tif/webp；输出: jpg/jpeg/png/gif/bmp/tiff/webp。
// CGO_ENABLED=0 可用（铁律 2 合规——零动态链接、体积增幅小）。
package image

import (
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	// x/image 格式（decode 注册 + encode 调用）
	bmp "golang.org/x/image/bmp"
	tiff "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp" // decode only

	"github.com/HugoSmits86/nativewebp"
)

// IMAGE_DEFAULT_QUALITY convert 缺省 JPEG/WebP 质量（保真优先——压缩另有缺省）
const IMAGE_DEFAULT_QUALITY = 92

// IMAGE_COMPRESS_QUALITY compress 缺省质量（瘦身优先）
const IMAGE_COMPRESS_QUALITY = 80

// ImageResult 图片操作结果（信封 data 载荷）
type ImageResult struct {
	Input          string  `json:"input"`
	Output         string  `json:"output"`
	SourceFormat   string  `json:"source_format"`
	TargetFormat   string  `json:"target_format"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	OriginalSize   int64   `json:"original_size_bytes"`
	OutputSize     int64   `json:"output_size_bytes"`
	Ratio          float64 `json:"ratio"`
	OriginalFormat string  `json:"original_format,omitempty"` // compress 时源格式=目标格式
	// 目标体积压缩（--target-size）
	Quality         int    `json:"quality,omitzero"`          // 命中质量（0=直拷贝）
	TargetBytes     int64  `json:"target_bytes,omitzero"`     // 目标体积（cli 层回填）
	AlreadyFit      bool   `json:"already_fit,omitzero"`      // 源已达标直拷贝
	Strategy        string `json:"strategy,omitzero"`         // quality-only / scale-then-quality / last-resort
	ScalePercent    int    `json:"scale_percent,omitzero"`    // 最终缩放（100=未缩；quality-only 不出现）
	QualityDegraded bool   `json:"quality_degraded,omitzero"` // 画质破 50 下限（last-resort）
}

// SupportedInputFormats 可解码格式
var SupportedInputFormats = []string{"jpg", "jpeg", "png", "gif", "bmp", "tiff", "tif", "webp"}

// SupportedOutputFormats 可编码格式
var SupportedOutputFormats = []string{"jpg", "jpeg", "png", "gif", "bmp", "tiff", "webp"}

// ReadImage 读取图片（自动格式检测）
func ReadImage(path string) (image.Image, string, error) {
	// HDR 格式（EXR/Radiance/AVIF）走 astiav 通道（纯构建=需完整版桩）
	if img, format, handled, hdrErr := decodeHDRImage(path); handled {
		return img, format, hdrErr
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	img, format, err := image.Decode(file)
	if err != nil {
		return nil, "", fmt.Errorf("无法解码图片 %s: %w", filepath.Base(path), err)
	}
	return img, format, nil
}

// ReadImageOriented 处理路径读取：ReadImage + EXIF Orientation 物理化——
// 返回"观看态"像素（观看器按标签转过的样子）。zpack 输出不携带 EXIF，
// 若不在此处定向，orientation≠1 的照片产出会横倒。
func ReadImageOriented(path string) (image.Image, string, error) {
	img, format, err := ReadImage(path)
	if err != nil {
		return nil, format, err
	}
	oriented := ApplyOrientation(img, ReadOrientation(path))
	return oriented, format, nil
}

// WriteImage 按格式编码写入
func WriteImage(path string, img image.Image, format string, quality int) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	switch strings.ToLower(format) {
	case "jpg", "jpeg":
		return jpeg.Encode(file, img, &jpeg.Options{Quality: quality})
	case "png":
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		return encoder.Encode(file, img)
	case "gif":
		return gif.Encode(file, img, &gif.Options{NumColors: 256})
	case "bmp":
		return bmp.Encode(file, img)
	case "tiff", "tif":
		return tiff.Encode(file, img, nil)
	case "webp":
		return encodeWebP(file, img, quality)
	default:
		return fmt.Errorf("不支持的输出格式: %s", format)
	}
}

// encodeWebP 用 nativewebp 编码（纯 Go——lossless VP8L）
func encodeWebP(file *os.File, img image.Image, _ int) error {
	return nativewebp.Encode(file, img, &nativewebp.Options{
		UseExtendedFormat: false,
		CompressionLevel:  nativewebp.DefaultCompression,
	})
}
