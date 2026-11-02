// 目标体积压缩：质量参数二分搜索（JPEG/WebP 有损格式专属——
// 质量与体积单调相关，7 次编码内收敛到 ≤ 目标的最高质量）。
package image

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"

	xdraw "golang.org/x/image/draw"
)

// TARGET_QUALITY_FLOOR 二分下限（q=1 仍超目标=物理不可达——报错引导 resize/换格式）
const TARGET_QUALITY_FLOOR = 1

// SMART_QUALITY_FLOOR 智能策略的画质下限（JPEG q<50 视觉明显劣化——
// 5000×6000 压 1MB 时纯降质会变马赛克，宁可缩尺寸也保画质）
const SMART_QUALITY_FLOOR = 50

// SMART_MIN_SCALE_PERCENT 智能策略最小缩放（再小连缩放都救不了）
const SMART_MIN_SCALE_PERCENT = 5

// SIZE_PRIORITY_QUALITY size 优先模式的持衡质量（用户定案：优先缩尺寸、质量不动——
// 钉在视觉无损档，靠像素数换取体积；5000×6000 这类巨型图正是像素过多的场景）
const SIZE_PRIORITY_QUALITY = 90

// TARGET_QUALITY_CEILING 二分上限（源已达标走直拷贝，不浪费画质）
const TARGET_QUALITY_CEILING = 100

// isLossyFormat 质量参数生效的格式——当前仅 JPEG（webp 走 nativewebp 纯 Go
// 无损 VP8L，质量无效；PNG/GIF/BMP/TIFF 天生无损）
func isLossyFormat(format string) bool {
	return normalizeFormat(format) == "jpg"
}

// CompressTargetSize 智能目标体积压缩，两种优先级（用户可调）：
//
//	size（缺省）——质量钉 q=90 不动，二分缩放到刚好装下（"巨大原因是 5000×6000 像素"场景）
//	quality——保像素优先：全尺寸质量二分到 q≥50 直接用；不够再 q=50 缩放、回拉质量
//
// 共同兜底：最小缩放（5%）仍装不下 → 最低缩放下全量程二分（画质降级告知）；仍不行报错
func CompressTargetSize(input, output string, targetBytes int64, priority string, logf func(format string, args ...any)) (*ImageResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	img, sourceFormat, err := ReadImageOriented(input)
	if err != nil {
		return nil, err
	}
	outputFormat := normalizeFormat(sourceFormat)
	if !isLossyFormat(outputFormat) {
		return nil, fmt.Errorf("%s 无质量维度（webp=纯 Go 无损 VP8L；PNG/GIF/BMP/TIFF 天生无损）——目标体积请转 jpg（image convert）或缩尺寸（image resize）", sourceFormat)
	}
	inputInfo, _ := os.Stat(input)

	// 源已达标：直拷贝（零画质损失）
	if inputInfo.Size() <= targetBytes {
		if copyErr := copyFileForTarget(input, output); copyErr != nil {
			return nil, copyErr
		}
		return buildTargetResult(input, output, sourceFormat, outputFormat, img, 0, inputInfo.Size(), true), nil
	}

	if priority == "size" {
		return compressTargetSizeFirst(input, output, sourceFormat, outputFormat, img, targetBytes, logf)
	}
	// ── quality 优先 ──
	// ① 全尺寸质量二分
	fullResBestQ, fullResBytes := binarySearchQuality(img, outputFormat, targetBytes)
	if fullResBestQ >= SMART_QUALITY_FLOOR {
		return finishTarget(input, output, sourceFormat, outputFormat, img, fullResBestQ, fullResBytes, "quality-only", 100, false)
	}
	// ② 像素过多是主因：q=50 下二分缩放（从原图缩放——避免重复采样累积）
	logf("全尺寸最高只到 q=%d（<%d 画质下限）——缩尺寸优先保画质", fullResBestQ, SMART_QUALITY_FLOOR)
	fitScale := -1
	var fitImg *image.RGBA
	lo, hi := SMART_MIN_SCALE_PERCENT, 100
	for lo <= hi {
		mid := lo + (hi-lo)/2
		scaled := scaleImagePercent(img, mid)
		encoded, encodeErr := encodeImageBytes(scaled, outputFormat, SMART_QUALITY_FLOOR)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if int64(len(encoded)) <= targetBytes {
			fitScale, fitImg = mid, scaled
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	// ④ 最小缩放 @q50 仍不行 → 最小缩放下全量程二分（画质降级告知）
	if fitScale < 0 {
		logf("缩至 %d%% @q%d 仍超目标——画质降级全量程二分", SMART_MIN_SCALE_PERCENT, SMART_QUALITY_FLOOR)
		minImg := scaleImagePercent(img, SMART_MIN_SCALE_PERCENT)
		minQ, minBytes := binarySearchQuality(minImg, outputFormat, targetBytes)
		if minQ < 0 {
			return nil, fmt.Errorf("缩至 %d%% 且 q=1 仍超出目标 %d 字节——物理不可达：裁剪内容或接受更大体积", SMART_MIN_SCALE_PERCENT, targetBytes)
		}
		return finishTarget(input, output, sourceFormat, outputFormat, minImg, minQ, minBytes, "last-resort", SMART_MIN_SCALE_PERCENT, true)
	}
	// ③ 缩放定位后回拉质量（同尺寸下最高 q）
	bestQ, bestBytes := binarySearchQuality(fitImg, outputFormat, targetBytes)
	if bestQ < SMART_QUALITY_FLOOR {
		bestQ, bestBytes = SMART_QUALITY_FLOOR, mustEncode(fitImg, outputFormat, SMART_QUALITY_FLOOR)
	}
	return finishTarget(input, output, sourceFormat, outputFormat, fitImg, bestQ, bestBytes, "scale-then-quality", fitScale, false)
}

// compressTargetSizeFirst size 优先：质量钉 SIZE_PRIORITY_QUALITY，二分缩放
func compressTargetSizeFirst(input, output, sourceFormat, outputFormat string, img image.Image, targetBytes int64, logf func(format string, args ...any)) (*ImageResult, error) {
	logf("size 优先：质量钉 q=%d，缩尺寸换体积", SIZE_PRIORITY_QUALITY)
	fitScale := -1
	var fitImg *image.RGBA
	lo, hi := SMART_MIN_SCALE_PERCENT, 100
	for lo <= hi {
		mid := lo + (hi-lo)/2
		scaled := scaleImagePercent(img, mid)
		encoded, encodeErr := encodeImageBytes(scaled, outputFormat, SIZE_PRIORITY_QUALITY)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if int64(len(encoded)) <= targetBytes {
			fitScale, fitImg = mid, scaled
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if fitScale < 0 { // 最小缩放仍不行 → 画质降级兜底
		logf("缩至 %d%% @q%d 仍超目标——画质降级全量程二分", SMART_MIN_SCALE_PERCENT, SIZE_PRIORITY_QUALITY)
		minImg := scaleImagePercent(img, SMART_MIN_SCALE_PERCENT)
		minQ, minBytes := binarySearchQuality(minImg, outputFormat, targetBytes)
		if minQ < 0 {
			return nil, fmt.Errorf("缩至 %d%% 且 q=1 仍超出目标 %d 字节——物理不可达：裁剪内容或接受更大体积", SMART_MIN_SCALE_PERCENT, targetBytes)
		}
		return finishTarget(input, output, sourceFormat, outputFormat, minImg, minQ, minBytes, "last-resort", SMART_MIN_SCALE_PERCENT, true)
	}
	encoded := mustEncode(fitImg, outputFormat, SIZE_PRIORITY_QUALITY)
	return finishTarget(input, output, sourceFormat, outputFormat, fitImg, SIZE_PRIORITY_QUALITY, encoded, "size-first", fitScale, false)
}

// binarySearchQuality ≤ 目标的最高质量（-1=全量程不可达）
func binarySearchQuality(img image.Image, format string, targetBytes int64) (int, []byte) {
	bestQuality := -1
	var bestBytes []byte
	lo, hi := TARGET_QUALITY_FLOOR, TARGET_QUALITY_CEILING
	for lo <= hi {
		mid := lo + (hi-lo)/2
		encoded, encodeErr := encodeImageBytes(img, format, mid)
		if encodeErr != nil {
			return -1, nil
		}
		if int64(len(encoded)) <= targetBytes {
			bestQuality, bestBytes = mid, encoded
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return bestQuality, bestBytes
}

// mustEncode 指定质量编码（回拉兜底路径——二分必有此解）
func mustEncode(img image.Image, format string, quality int) []byte {
	encoded, err := encodeImageBytes(img, format, quality)
	if err != nil {
		return nil
	}
	return encoded
}

// scaleImagePercent 等比缩放（CatmullRom——同 resize 管线插值）
func scaleImagePercent(src image.Image, percent int) *image.RGBA {
	srcW, srcH := src.Bounds().Dx(), src.Bounds().Dy()
	dstW := srcW * percent / 100
	dstH := srcH * percent / 100
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// finishTarget 落盘 + 结果组装
func finishTarget(input, output, sourceFormat, outputFormat string, img image.Image, quality int, encoded []byte, strategy string, scalePercent int, degraded bool) (*ImageResult, error) {
	if writeErr := os.WriteFile(output, encoded, 0o644); writeErr != nil {
		return nil, writeErr
	}
	outputInfo, _ := os.Stat(output)
	result := buildTargetResult(input, output, sourceFormat, outputFormat, img, quality, outputInfo.Size(), false)
	result.Strategy = strategy
	result.ScalePercent = scalePercent
	result.QualityDegraded = degraded
	return result, nil
}

// encodeImageBytes 内存编码（二分每轮不落盘——JPEG 质量可调）
func encodeImageBytes(img image.Image, format string, quality int) ([]byte, error) {
	buffer := &bytes.Buffer{}
	if encodeErr := jpeg.Encode(buffer, img, &jpeg.Options{Quality: quality}); encodeErr != nil {
		return nil, encodeErr
	}
	return buffer.Bytes(), nil
}

// buildTargetResult 结果载荷（target 三字段：目标字节/命中质量/是否直拷）
func buildTargetResult(input, output, sourceFormat, outputFormat string, img image.Image, quality int, outputSize int64, alreadyFit bool) *ImageResult {
	inputInfo, _ := os.Stat(input)
	bounds := img.Bounds()
	result := &ImageResult{
		Input:        absPath(input),
		Output:       absPath(output),
		SourceFormat: sourceFormat,
		TargetFormat: outputFormat,
		Width:        bounds.Dx(),
		Height:       bounds.Dy(),
		OriginalSize: inputInfo.Size(),
		OutputSize:   outputSize,
	}
	if inputInfo.Size() > 0 {
		result.Ratio = float64(outputSize) / float64(inputInfo.Size())
	}
	result.Quality = quality
	result.AlreadyFit = alreadyFit
	return result
}

// copyFileForTarget 拷贝（already-fit 路径；heic.go 另有 copyFile——cgo 层同名不同用）
func copyFileForTarget(src, dst string) error {
	data, readErr := os.ReadFile(src)
	if readErr != nil {
		return readErr
	}
	return os.WriteFile(dst, data, 0o644)
}
