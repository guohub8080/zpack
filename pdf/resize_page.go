// PDF 页面尺寸统一：图片型 PDF 各页图片大小不一 → 统一到目标纸型/自定义尺寸
// （内容按页缩放适配——横页最佳适配旋转，可强制方向）。
package pdf

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// MM_TO_PT 毫米 → PDF 点（1pt = 1/72"，1" = 25.4mm）
const MM_TO_PT = 72.0 / 25.4

// PageResizeResult 统一尺寸结果
type PageResizeResult struct {
	Input      string  `json:"input"`
	Output     string  `json:"output"`
	SizeSpec   string  `json:"size_spec"`
	WidthPt    float64 `json:"width_pt"` // 目标尺寸（pt）
	HeightPt   float64 `json:"height_pt"`
	PagesSpec  string  `json:"pages_spec,omitempty"`
	Pages      []int   `json:"pages"` // 实际处理的页码（空规格=全部）
	PageCount  int     `json:"page_count"`
	TotalPages int     `json:"total_pages"`
	SizeBytes  int64   `json:"size_bytes"`
}

// ParsePageSizeSpec 尺寸规格：纸型（a4/a4l/a4p/letter/legal…——尾缀 L/P=强制横/竖）
// 或自定义 `210x297mm` / `8.5x11in` / `595x842`（缺省 pt）。
func ParsePageSizeSpec(spec string) (*model.Resize, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return nil, fmt.Errorf("--size 缺少值（纸型如 a4/letter，或 210x297mm / 595x842）")
	}
	// 纸型（可带 L/P 尾缀）
	core := strings.ToUpper(trimmed)
	enforceLandscape, enforcePortrait := false, false
	if strings.HasSuffix(core, "L") {
		core, enforceLandscape = core[:len(core)-1], true
	} else if strings.HasSuffix(core, "P") {
		core, enforcePortrait = core[:len(core)-1], true
	}
	if dim, isPaper := types.PaperSize[core]; isPaper {
		resize := &model.Resize{PageSize: core, PageDim: dim}
		if enforceLandscape || enforcePortrait {
			resize.EnforceOrient = true // 尾缀出现即强制（pdfcpu 无此位则做 best-fit——横页保横）
			if (enforceLandscape && dim.Portrait()) || (enforcePortrait && dim.Landscape()) {
				resize.PageDim = &types.Dim{Width: dim.Height, Height: dim.Width}
			}
		}
		return resize, nil
	}
	// 自定义 WxH[mm|cm|in]（缺省 pt）——单位后缀显式剥离
	unitText := ""
	lowerSpec := strings.ToLower(trimmed)
	for _, unit := range []string{"mm", "cm", "in"} {
		if strings.HasSuffix(lowerSpec, unit) {
			unitText = unit
			lowerSpec = strings.TrimSuffix(lowerSpec, unit)
			break
		}
	}
	parts := strings.SplitN(lowerSpec, "x", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("--size 需纸型（a4/letter）或 WxH 格式（210x297mm / 595x842），收到: %s", spec)
	}
	width, widthErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	height, heightErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return nil, fmt.Errorf("--size 宽高需正数（210x297mm / 595x842），收到: %s", spec)
	}
	multiplier := 1.0
	switch unitText {
	case "":
		multiplier = 1 // pt
	case "mm":
		multiplier = MM_TO_PT
	case "cm":
		multiplier = MM_TO_PT * 10
	case "in":
		multiplier = 72
	default:
		return nil, fmt.Errorf("--size 单位需 mm/cm/in（缺省 pt），收到: %s", unitText)
	}
	return &model.Resize{
		PageDim: &types.Dim{Width: width * multiplier, Height: height * multiplier},
		UserDim: true,
	}, nil
}

// ResizePages 统一页面尺寸（pagesSpec 空=全部页；内容按页缩放——横页最佳适配）。
// 文档型（任一选中页含字体）默认拦截——几何缩放文字非重排；--force 放行（用户明示）。
func ResizePages(input, sizeSpec, pagesSpec, output string, force bool, logf func(format string, args ...any)) (*PageResizeResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	resize, specErr := ParsePageSizeSpec(sizeSpec)
	if specErr != nil {
		return nil, specErr
	}
	if _, statErr := os.Stat(input); statErr != nil {
		return nil, fmt.Errorf("路径不存在: %s", input)
	}
	totalPages, countErr := api.PageCountFile(context.Background(), input)
	if countErr != nil {
		return nil, fmt.Errorf("无法读取 PDF: %w", countErr)
	}
	var pages []int
	var tokens []string
	if pagesSpec == "" {
		for page := 1; page <= totalPages; page++ {
			pages = append(pages, page)
		}
	} else {
		var notices []string
		var parseErr error
		pages, notices, parseErr = ParsePagesSpec(pagesSpec, totalPages)
		if parseErr != nil {
			return nil, parseErr
		}
		for _, notice := range notices {
			logf("%s", notice)
		}
		tokens = make([]string, len(pages))
		for idx, page := range pages {
			tokens[idx] = strconv.Itoa(page)
		}
	}
	// 文档型拦截——只看选中页（混合型 PDF 可 --pages 圈住图片页避开文字页）
	textPages, _, detectErr := DetectTextPages(input)
	if detectErr != nil {
		return nil, detectErr
	}
	textSet := map[int]bool{}
	for _, textPage := range textPages {
		textSet[textPage] = true
	}
	var selectedTextPages []int
	for _, page := range pages {
		if textSet[page] {
			selectedTextPages = append(selectedTextPages, page)
		}
	}
	if len(selectedTextPages) > 0 && !force {
		return nil, fmt.Errorf("选中页中第 %s 页含文字（共 %d 页文档页）——resize 会几何缩放文字（字号缩放非 Word 式重排）；缩小 --pages 范围避开文字页（pdf info --all-pages 查逐页标注）或 --force 明示放行", FormatPageList(selectedTextPages), len(selectedTextPages))
	}
	if len(selectedTextPages) > 0 && force {
		logf("--force：第 %s 页为文字页，按几何缩放放行——文字将等比缩放不重排", FormatPageList(selectedTextPages))
	}
	if resizeErr := api.ResizeFile(context.Background(), input, output, tokens, resize, nil); resizeErr != nil {
		return nil, fmt.Errorf("尺寸统一失败: %w", resizeErr)
	}
	outputInfo, _ := os.Stat(output)
	return &PageResizeResult{
		Input: absPath(input), Output: absPath(output),
		SizeSpec: sizeSpec,
		WidthPt:  resize.PageDim.Width, HeightPt: resize.PageDim.Height,
		PagesSpec: pagesSpec, Pages: pages,
		PageCount: len(pages), TotalPages: totalPages,
		SizeBytes: outputInfo.Size(),
	}, nil
}

// FormatPageList 页码列表人读形态（「2、5、7」；超 10 个截断为「2、5、…等 N 页」）
func FormatPageList(pages []int) string {
	const LIST_CAP = 10
	parts := make([]string, 0, len(pages))
	for idx, page := range pages {
		if idx >= LIST_CAP {
			parts = append(parts, fmt.Sprintf("…等 %d 页", len(pages)))
			break
		}
		parts = append(parts, strconv.Itoa(page))
	}
	return strings.Join(parts, "、")
}
