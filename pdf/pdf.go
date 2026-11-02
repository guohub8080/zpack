// PDF 引擎：合并/提取页/元信息——纯 Go（pdfcpu Apache-2.0 封装，零 cgo）。
package pdf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// PDF_PAGE_PT_TO_MM pt → mm 换算（1pt = 1/72 inch）
const PDF_PAGE_PT_TO_MM = 25.4 / 72

// MergeResult 合并结果（信封 data 载荷）
type MergeResult struct {
	Output     string   `json:"output"`
	Inputs     []string `json:"inputs"`
	InputCount int      `json:"input_count"`
	PageCount  int      `json:"page_count"`
	SizeBytes  int64    `json:"size_bytes"`
}

// ExtractResult 提取结果
type ExtractResult struct {
	Input           string `json:"input"`
	Output          string `json:"output"`
	PagesSpec       string `json:"pages_spec"`
	Pages           []int  `json:"pages"` // 实际提取的页码（升序去重）
	SourcePageCount int    `json:"source_page_count"`
	SizeBytes       int64  `json:"size_bytes"`
}

// InfoResult 元信息
type InfoResult struct {
	Input     string `json:"input"`
	PageCount int    `json:"page_count"`
	// 首页尺寸（pt = PDF 点 1/72"；mm = 换算；已反映 /Rotate 观看态）
	WidthPt   float64 `json:"width_pt"`
	HeightPt  float64 `json:"height_pt"`
	WidthMm   float64 `json:"width_mm"`
	HeightMm  float64 `json:"height_mm"`
	SizeBytes int64   `json:"size_bytes"`
	// 首页 /Rotate（度，顺时针；0=未转，omitzero 不出现）——旋转后 info 可验证方向
	Rotation int `json:"rotation,omitzero"`
	// 文档型判定（true=有页声明字体——resize 前体检用；图片型/矢量型不出现）
	TextBased bool `json:"text_based,omitzero"`
}

// Merge 合并多个 PDF（参数序即页序）
func Merge(inputs []string, output string) (*MergeResult, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("至少需要 2 个 PDF")
	}
	for inputIdx, inputPath := range inputs {
		if _, statErr := os.Stat(inputPath); statErr != nil {
			return nil, fmt.Errorf("输入 %d 不存在: %s", inputIdx+1, inputPath)
		}
	}
	if mergeErr := api.MergeCreateFile(context.Background(), inputs, output, false, nil); mergeErr != nil {
		return nil, fmt.Errorf("合并失败: %w", mergeErr)
	}
	pageCount, countErr := api.PageCountFile(context.Background(), output)
	if countErr != nil {
		return nil, fmt.Errorf("合并产物校验失败: %w", countErr)
	}
	outputInfo, _ := os.Stat(output)
	return &MergeResult{
		Output:     absPath(output),
		Inputs:     absPaths(inputs),
		InputCount: len(inputs),
		PageCount:  pageCount,
		SizeBytes:  outputInfo.Size(),
	}, nil
}

// ExtractPages 提取指定页到新 PDF（页码 1 起、区间闭区间、开尾「8-」=到末页；升序去重）
func ExtractPages(input, pagesSpec, output string, logf func(format string, args ...any)) (*ExtractResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if _, statErr := os.Stat(input); statErr != nil {
		return nil, fmt.Errorf("路径不存在: %s", input)
	}
	pageCount, countErr := api.PageCountFile(context.Background(), input)
	if countErr != nil {
		return nil, fmt.Errorf("无法读取 PDF: %w", countErr)
	}
	pages, notices, parseErr := ParsePagesSpec(pagesSpec, pageCount)
	if parseErr != nil {
		return nil, parseErr
	}
	for _, notice := range notices {
		logf("%s", notice)
	}
	// 钳制后的显式页码喂 pdfcpu（原始 token 可能越界）
	tokens := make([]string, len(pages))
	for idx, page := range pages {
		tokens[idx] = strconv.Itoa(page)
	}
	if extractErr := api.TrimFile(context.Background(), input, output, tokens, nil); extractErr != nil {
		return nil, fmt.Errorf("提取失败: %w", extractErr)
	}
	outputInfo, _ := os.Stat(output)
	return &ExtractResult{
		Input:           absPath(input),
		Output:          absPath(output),
		PagesSpec:       pagesSpec,
		Pages:           pages,
		SourcePageCount: pageCount,
		SizeBytes:       outputInfo.Size(),
	}, nil
}

// PageItem 全页明细（--all-pages：发现混合尺寸的核心用例）
type PageItem struct {
	Page     int     `json:"page"`
	WidthPt  float64 `json:"width_pt"`
	HeightPt float64 `json:"height_pt"`
	Rotation int     `json:"rotation,omitzero"`
	Text     bool    `json:"text,omitzero"` // 该页声明字体（文档页）
}

// InfoAllPages 全页明细（尺寸+角度；混合尺寸文档体检）
func InfoAllPages(input string) ([]PageItem, error) {
	if _, statErr := os.Stat(input); statErr != nil {
		return nil, fmt.Errorf("路径不存在: %s", input)
	}
	ctx, err := api.ReadContextFile(context.Background(), input)
	if err != nil {
		return nil, fmt.Errorf("无法读取 PDF: %w", err)
	}
	dims, dimsErr := api.PageDimsFile(context.Background(), input)
	if dimsErr != nil {
		return nil, fmt.Errorf("读取页面尺寸失败: %w", dimsErr)
	}
	pages := make([]PageItem, 0, len(dims))
	for idx, dim := range dims {
		page := idx + 1
		rotation := 0
		if pageDict, _, _, dictErr := ctx.XRefTable.PageDict(context.Background(), page, false); dictErr == nil && pageDict != nil {
			if degrees := pageDict.IntEntry("Rotate"); degrees != nil {
				rotation = *degrees
			}
		}
		textPage := false
		if pageDict, _, attrs, dictErr := ctx.XRefTable.PageDict(context.Background(), page, false); dictErr == nil {
			textPage = pageHasFont(pageDict, attrs)
		}
		pages = append(pages, PageItem{Page: page, WidthPt: dim.Width, HeightPt: dim.Height, Rotation: rotation, Text: textPage})
	}
	return pages, nil
}

// Info PDF 元信息（页数/首页尺寸/体积）
func Info(input string) (*InfoResult, error) {
	if _, statErr := os.Stat(input); statErr != nil {
		return nil, fmt.Errorf("路径不存在: %s", input)
	}
	pageCount, countErr := api.PageCountFile(context.Background(), input)
	if countErr != nil {
		return nil, fmt.Errorf("无法读取 PDF: %w", countErr)
	}
	dims, dimsErr := api.PageDimsFile(context.Background(), input)
	if dimsErr != nil || len(dims) == 0 {
		return nil, fmt.Errorf("读取页面尺寸失败: %w", dimsErr)
	}
	info, _ := os.Stat(input)
	result := &InfoResult{
		Input:     absPath(input),
		PageCount: pageCount,
		WidthPt:   dims[0].Width,
		HeightPt:  dims[0].Height,
		WidthMm:   dims[0].Width * PDF_PAGE_PT_TO_MM,
		HeightMm:  dims[0].Height * PDF_PAGE_PT_TO_MM,
		SizeBytes: info.Size(),
		Rotation:  firstPageRotation(input),
	}
	if textPages, _, detectErr := DetectTextPages(input); detectErr == nil && len(textPages) > 0 {
		result.TextBased = true
	}
	return result, nil
}

// firstPageRotation 首页 /Rotate（0=未转；读取失败按 0）
func firstPageRotation(input string) int {
	ctx, err := api.ReadContextFile(context.Background(), input)
	if err != nil {
		return 0
	}
	pageDict, _, _, dictErr := ctx.XRefTable.PageDict(context.Background(), 1, false)
	if dictErr != nil || pageDict == nil {
		return 0
	}
	if degrees := pageDict.IntEntry("Rotate"); degrees != nil {
		return *degrees
	}
	return 0
}

// ParsePagesSpec 页码规格解析：`1-3,5,8-`（1 起、闭区间、开尾到末页、`-5`=首页到 5）。
// 返回升序去重页码 + 钳制提示（区间**末端**超出自动截至末页——同视频剪辑宽尾语义；
// 起始页超出/单页不存在仍报错：没有可提取的页）。
func ParsePagesSpec(spec string, pageCount int) ([]int, []string, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil, fmt.Errorf("--pages 缺少值（如 1-3,5,8-）")
	}
	pageSet := map[int]bool{}
	var notices []string
	for _, token := range strings.Split(spec, ",") {
		token = strings.TrimSpace(token)
		bounds := strings.SplitN(token, "-", 2)
		fromText, toText := bounds[0], ""
		if len(bounds) == 2 {
			toText = bounds[1]
		}
		// 闭区间 / 单页（无连字符）/ 开尾「8-」/ 前开「-5」——单页与开尾必须区分
		switch {
		case fromText != "" && toText != "":
			from, fromErr := parsePageNumber(fromText, pageCount)
			if fromErr != nil {
				return nil, nil, fmt.Errorf("起始页非法: %s（共 %d 页）", token, pageCount)
			}
			to, toErr := strconv.Atoi(toText)
			if toErr != nil || to < from {
				return nil, nil, fmt.Errorf("页码区间非法: %s", token)
			}
			if to > pageCount { // 宽尾：截至末页 + 提示（同视频剪辑语义）
				notices = append(notices, fmt.Sprintf("页码区间 %s 超出末页，已截至第 %d 页（末页）", token, pageCount))
				to = pageCount
			}
			for page := from; page <= to; page++ {
				pageSet[page] = true
			}
		case fromText != "" && toText == "" && strings.Contains(token, "-"): // 「8-」= 8 到末页
			from, fromErr := parsePageNumber(fromText, pageCount)
			if fromErr != nil {
				return nil, nil, fmt.Errorf("页码非法: %s（共 %d 页）——起始页不存在", token, pageCount)
			}
			for page := from; page <= pageCount; page++ {
				pageSet[page] = true
			}
		case fromText != "" && toText == "": // 单页「5」
			page, pageErr := parsePageNumber(fromText, pageCount)
			if pageErr != nil {
				return nil, nil, fmt.Errorf("页码非法: %s（共 %d 页）", token, pageCount)
			}
			pageSet[page] = true
		case fromText == "" && toText != "": // 「-5」= 首页到 5
			to, toErr := strconv.Atoi(toText)
			if toErr != nil || to < 1 {
				return nil, nil, fmt.Errorf("页码区间非法: %s", token)
			}
			if to > pageCount { // 宽尾
				notices = append(notices, fmt.Sprintf("页码区间 %s 超出末页，已截至第 %d 页（末页）", token, pageCount))
				to = pageCount
			}
			for page := 1; page <= to; page++ {
				pageSet[page] = true
			}
		default:
			return nil, nil, fmt.Errorf("页码规格非法: %s", token)
		}
	}
	pages := make([]int, 0, len(pageSet))
	for page := range pageSet {
		pages = append(pages, page)
	}
	sort.Ints(pages)
	return pages, notices, nil
}

// parsePageNumber 单个页码（1..pageCount）
func parsePageNumber(text string, pageCount int) (int, error) {
	number, err := strconv.Atoi(text)
	if err != nil || number < 1 || number > pageCount {
		return 0, fmt.Errorf("越界")
	}
	return number, nil
}

func absPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

func absPaths(paths []string) []string {
	absolute := make([]string, len(paths))
	for idx, path := range paths {
		absolute[idx] = absPath(path)
	}
	return absolute
}
