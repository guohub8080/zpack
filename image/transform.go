// 图片几何变换：rotate-left/rotate-right/rotate-180/mirror/flip——与 media 家族同名同义。
// 纯像素重排（无插值、无重采样）——解码→重排→编码，像素级无损。
package image

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// TRANSFORM_ROTATE_LEFT 逆时针 90°（同 media transpose=2）
const TRANSFORM_ROTATE_LEFT = "rotate-left"

// TRANSFORM_ROTATE_RIGHT 顺时针 90°（同 media transpose=1）
const TRANSFORM_ROTATE_RIGHT = "rotate-right"

// TRANSFORM_ROTATE_180 180°
const TRANSFORM_ROTATE_180 = "rotate-180"

// TRANSFORM_MIRROR 水平镜像（左右翻，hflip）
const TRANSFORM_MIRROR = "mirror"

// TRANSFORM_FLIP 垂直镜像（上下翻，vflip）
const TRANSFORM_FLIP = "flip"

// ApplyOrientation 按 EXIF Orientation（1-8）物理重排像素——"观看态"像素。
// 1=原样 2=左右镜像 3=180° 4=上下镜像 5=主对角线转置 6=顺时针90° 7=副对角线转置 8=逆时针90°
func ApplyOrientation(src image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	rgba := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	xdraw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, xdraw.Src)
	width, height := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	var dst *image.RGBA
	switch orientation {
	case 2: // 左右镜像
		dst = image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, width-1-x, y, rgba, x, y)
			}
		}
	case 3: // 180°
		dst = image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, width-1-x, height-1-y, rgba, x, y)
			}
		}
	case 4: // 上下镜像
		dst = image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, x, height-1-y, rgba, x, y)
			}
		}
	case 5: // 主对角线转置：src(x,y) → dst(y,x)
		dst = image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, y, x, rgba, x, y)
			}
		}
	case 6: // 顺时针 90°
		dst = image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, height-1-y, x, rgba, x, y)
			}
		}
	case 7: // 副对角线转置：src(x,y) → dst(h-1-y, w-1-x)
		dst = image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, height-1-y, width-1-x, rgba, x, y)
			}
		}
	case 8: // 逆时针 90°
		dst = image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, y, width-1-x, rgba, x, y)
			}
		}
	}
	return dst
}

// OrientationSwapsDims orientation 是否交换宽高（5-8 交换）
func OrientationSwapsDims(orientation int) bool {
	return orientation >= 5 && orientation <= 8
}

// Transform 几何变换（kind 见常量；输出格式随扩展名，缺省=源格式）
func Transform(input, output, kind string, quality int) (*ImageResult, error) {
	if !isValidTransform(kind) {
		return nil, fmt.Errorf("未知变换: %s（支持 rotate-left/rotate-right/rotate-180/mirror/flip）", kind)
	}
	src, sourceFormat, readErr := ReadImageOriented(input)
	if readErr != nil {
		return nil, readErr
	}
	// 统一 RGBA（重排按 4 字节像素搬移；palette/gray/YCbCr 先转）
	srcBounds := src.Bounds()
	srcWidth, srcHeight := srcBounds.Dx(), srcBounds.Dy()
	rgba := image.NewRGBA(image.Rect(0, 0, srcWidth, srcHeight))
	xdraw.Draw(rgba, rgba.Bounds(), src, srcBounds.Min, xdraw.Src)

	dst := transformRGBA(rgba, kind)

	outputFormat := strings.ToLower(strings.TrimPrefix(filepath.Ext(output), "."))
	if outputFormat == "" {
		outputFormat = sourceFormat
	}
	if writeErr := WriteImage(output, dst, outputFormat, quality); writeErr != nil {
		return nil, writeErr
	}
	return buildTransformResult(input, output, sourceFormat, outputFormat, dst), nil
}

// isValidTransform kind 是否为已知变换
func isValidTransform(kind string) bool {
	switch kind {
	case TRANSFORM_ROTATE_LEFT, TRANSFORM_ROTATE_RIGHT, TRANSFORM_ROTATE_180, TRANSFORM_MIRROR, TRANSFORM_FLIP:
		return true
	}
	return false
}

// transformRGBA 像素重排（bit-exact：源像素值原样搬移，仅位置变）
func transformRGBA(src *image.RGBA, kind string) *image.RGBA {
	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	var dst *image.RGBA
	switch kind {
	case TRANSFORM_ROTATE_RIGHT: // 顺时针：dst(h×w)，src(x,y) → dst(h-1-y, x)
		dst = image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, height-1-y, x, src, x, y)
			}
		}
	case TRANSFORM_ROTATE_LEFT: // 逆时针：dst(h×w)，src(x,y) → dst(y, w-1-x)
		dst = image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, y, width-1-x, src, x, y)
			}
		}
	case TRANSFORM_ROTATE_180: // dst(w×h)，src(x,y) → dst(w-1-x, h-1-y)
		dst = image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, width-1-x, height-1-y, src, x, y)
			}
		}
	case TRANSFORM_MIRROR: // 左右翻：src(x,y) → dst(w-1-x, y)
		dst = image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, width-1-x, y, src, x, y)
			}
		}
	case TRANSFORM_FLIP: // 上下翻：src(x,y) → dst(x, h-1-y)
		dst = image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				copyPixel(dst, x, height-1-y, src, x, y)
			}
		}
	default:
		return nil
	}
	return dst
}

// copyPixel 4 字节像素搬移（RGBA 切片直寻址）
func copyPixel(dst *image.RGBA, dstX, dstY int, src *image.RGBA, srcX, srcY int) {
	srcOffset := (srcY-src.Rect.Min.Y)*src.Stride + (srcX-src.Rect.Min.X)*4
	dstOffset := (dstY-dst.Rect.Min.Y)*dst.Stride + (dstX-dst.Rect.Min.X)*4
	copy(dst.Pix[dstOffset:dstOffset+4], src.Pix[srcOffset:srcOffset+4])
}

// buildTransformResult 结果载荷（同 convert 的 ImageResult 形状）
func buildTransformResult(input, output, sourceFormat, outputFormat string, img *image.RGBA) *ImageResult {
	outputInfo, _ := os.Stat(output)
	inputInfo, _ := os.Stat(input)
	ratio := 0.0
	if inputInfo.Size() > 0 {
		ratio = float64(outputInfo.Size()) / float64(inputInfo.Size())
	}
	return &ImageResult{
		Input: input, Output: output,
		SourceFormat: sourceFormat, TargetFormat: outputFormat,
		Width:        img.Bounds().Dx(),
		Height:       img.Bounds().Dy(),
		OriginalSize: inputInfo.Size(), OutputSize: outputInfo.Size(),
		Ratio: ratio,
	}
}
