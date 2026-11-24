//go:build cgo

// render 的 MuPDF 引擎（go-fitz 绑定；MuPDF 源码随绑定仓库携带，cgo 构建时一并编译）。
// AGPL 义务见 specs/pdf.md 6.12。绑定 API：ImagePNG/ImageDPI(page, dpi) 直出整页（page 0 基）。
package pdf

import (
	"fmt"
	"image/jpeg"
	"os"

	fitz "github.com/gen2brain/go-fitz"
)

// fitzRenderPage 渲染单页（pageIndex 0 基）到 outPath；返回像素宽高。
// pixel = pt × dpi / 72（绑定内部按 dpi 换算）；横页保持横（按页面自身宽高关系）。
func fitzRenderPage(input string, pageIndex int, dpi int, format string, outPath string) (int, int, error) {
	document, openErr := fitz.New(input)
	if openErr != nil {
		return 0, 0, fmt.Errorf("无法读取 PDF: %s", openErr.Error())
	}
	defer document.Close()

	if pageIndex < 0 || pageIndex >= document.NumPage() {
		return 0, 0, fmt.Errorf("页码 %d 超出范围（共 %d 页）", pageIndex+1, document.NumPage())
	}

	var widthPx, heightPx int
	if format == "jpg" {
		rendered, renderErr := document.ImageDPI(pageIndex, float64(dpi))
		if renderErr != nil {
			return 0, 0, fmt.Errorf("渲染失败: %s", renderErr.Error())
		}
		bounds := rendered.Bounds()
		widthPx, heightPx = bounds.Dx(), bounds.Dy()
		jpegFile, createErr := os.Create(outPath)
		if createErr != nil {
			return 0, 0, createErr
		}
		encodeErr := jpeg.Encode(jpegFile, rendered, &jpeg.Options{Quality: RENDER_JPEG_QUALITY})
		closeErr := jpegFile.Close()
		if encodeErr != nil {
			return 0, 0, encodeErr
		}
		if closeErr != nil {
			return 0, 0, closeErr
		}
	} else {
		pngBytes, pngErr := document.ImagePNG(pageIndex, float64(dpi))
		if pngErr != nil {
			return 0, 0, fmt.Errorf("渲染失败: %s", pngErr.Error())
		}
		if writeErr := os.WriteFile(outPath, pngBytes, 0o644); writeErr != nil {
			return 0, 0, writeErr
		}
		widthPx, heightPx = pngDimensions(pngBytes)
	}
	return widthPx, heightPx, nil
}
