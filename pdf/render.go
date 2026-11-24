// render：PDF 页面渲染为图片（PNG/JPG）——「保存图片」交付能力 + GUI 页面编辑器的预览图来源。
// 引擎是 MuPDF（github.com/gen2brain/go-fitz 绑定，自带 MuPDF 源码随 cgo 编译）——AGPL 义务
// 见 specs/pdf.md 6.12（2026-11-24 用户拍板）。GUI 侧的缩略图预览走各平台系统渲染器（快），
// 不走本命令——展示辅助归 GUI 层，本命令只管「保存图片」交付。
// cgo 分层：render_cgo.go（真引擎）/ render_nocgo.go（stub 报「需完整版」），与 media 惯例同构。
package pdf

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// RENDER_DPI_DEFAULT / RENDER_DPI_MIN / RENDER_DPI_MAX 渲染分辨率缺省与合法区间
const (
	RENDER_DPI_DEFAULT = 96
	RENDER_DPI_MIN     = 36
	RENDER_DPI_MAX     = 600
)

// RENDER_FORMATS 支持的输出格式
var RENDER_FORMATS = []string{"png", "jpg"}

// RENDER_JPEG_QUALITY jpg 输出的固定质量（视觉无损档，与 image 家族惯例同源）
const RENDER_JPEG_QUALITY = 90

// RenderOptions render 子命令的参数（cli 层解析后传入）
type RenderOptions struct {
	PagesSpec string // 页码规格；空 = 全部页
	DPI       int    // 渲染分辨率（36–600）
	Format    string // "png" / "jpg"
	OutputDir string // 输出目录（必须已存在；空 = 输入同目录）
}

// RenderedFile 单页渲染产物
type RenderedFile struct {
	Page     int    `json:"page"`     // 1 起的原文档页码
	Path     string `json:"path"`     // 产出文件绝对路径
	WidthPx  int    `json:"width_px"` // 像素宽（页面 pt × dpi / 72，四舍五入）
	HeightPx int    `json:"height_px"`
}

// RenderResult render 子命令的输出载荷（specs/pdf.md 3.7）
type RenderResult struct {
	Input     string         `json:"input"`
	OutputDir string         `json:"output_dir"`
	Format    string         `json:"format"`
	DPI       int            `json:"dpi"`
	PageCount int            `json:"page_count"`
	Files     []RenderedFile `json:"files"`
}

// NormalizeRenderFormat 格式名归一与校验（返回小写形式；非法返回空）
func NormalizeRenderFormat(format string) string {
	lower := strings.ToLower(strings.TrimSpace(format))
	if lower == "" {
		lower = "png"
	}
	for _, supported := range RENDER_FORMATS {
		if lower == supported {
			return lower
		}
	}
	return ""
}

// renderOutputPath 单页产物路径：<输入基名>-p<页码>.<ext>（specs/pdf.md 3.7）
func renderOutputPath(input string, outputDir string, pageNumber int, format string) string {
	base := filepath.Base(input)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return filepath.Join(outputDir, fmt.Sprintf("%s-p%d.%s", base, pageNumber, format))
}

// ResolveRenderPages 页码定案：spec 空 = 全部页（1..total）；非空走 ParsePagesSpec（与
// extract/resize/rotate 同一套语法与宽容规则）。总页数由 pdfcpu 读（操作面同源）。
func ResolveRenderPages(input string, spec string) ([]int, error) {
	pageCount, countErr := api.PageCountFile(context.Background(), input)
	if countErr != nil {
		return nil, fmt.Errorf("无法读取 PDF: %s", countErr.Error())
	}
	if pageCount <= 0 {
		return nil, fmt.Errorf("PDF 无页面可渲染")
	}
	if strings.TrimSpace(spec) == "" {
		pageNumbers := make([]int, pageCount)
		for idx := range pageNumbers {
			pageNumbers[idx] = idx + 1
		}
		return pageNumbers, nil
	}
	pageNumbers, _, parseErr := ParsePagesSpec(spec, pageCount)
	if parseErr != nil {
		return nil, parseErr
	}
	if len(pageNumbers) == 0 {
		return nil, fmt.Errorf("PDF 无页面可渲染")
	}
	return pageNumbers, nil
}

// pngDimensions 从 PNG 字节读 IHDR 宽高（结构固定：8 签名 + 4 长度 + 4 "IHDR" + 4 宽 + 4 高，大端）
func pngDimensions(pngBytes []byte) (int, int) {
	if len(pngBytes) < 24 {
		return 0, 0
	}
	width := int(pngBytes[16])<<24 | int(pngBytes[17])<<16 | int(pngBytes[18])<<8 | int(pngBytes[19])
	height := int(pngBytes[20])<<24 | int(pngBytes[21])<<16 | int(pngBytes[22])<<8 | int(pngBytes[23])
	return width, height
}

// RenderDocument 渲染编排：校验已由 cli 层完成，这里定页序、逐页渲染、汇总。
// 逐页独立：第 k 页失败即整体失败，已写出的前 k-1 个文件不回滚（stderr 报失败页码）。
func RenderDocument(input string, options RenderOptions, pageNumbers []int) (RenderResult, error) {
	outputDir := options.OutputDir
	if outputDir == "" {
		outputDir = filepath.Dir(input)
	}
	absoluteOutputDir, absErr := filepath.Abs(outputDir)
	if absErr != nil {
		absoluteOutputDir = outputDir
	}
	absoluteInput, absErr := filepath.Abs(input)
	if absErr != nil {
		absoluteInput = input
	}

	result := RenderResult{
		Input:     absoluteInput,
		OutputDir: absoluteOutputDir,
		Format:    options.Format,
		DPI:       options.DPI,
		PageCount: len(pageNumbers),
		Files:     []RenderedFile{},
	}
	for _, pageNumber := range pageNumbers {
		outPath := renderOutputPath(absoluteInput, absoluteOutputDir, pageNumber, options.Format)
		widthPx, heightPx, renderErr := fitzRenderPage(absoluteInput, pageNumber-1, options.DPI, options.Format, outPath)
		if renderErr != nil {
			return result, fmt.Errorf("第 %d 页渲染失败: %s", pageNumber, renderErr.Error())
		}
		result.Files = append(result.Files, RenderedFile{
			Page:     pageNumber,
			Path:     outPath,
			WidthPx:  widthPx,
			HeightPx: heightPx,
		})
	}
	return result, nil
}
