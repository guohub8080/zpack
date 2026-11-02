// PDF 文字型检测：任一页 Resources 声明字体 → 文档型。
// resize 统一尺寸对文档型是几何缩放文字（字号缩、非 Word 式重排）——默认拦截。
package pdf

import (
	"context"
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// DetectTextPages 检测含字体的页（Resources/Font 存在——含继承资源）。
// 返回文字页页码列表与总页数。
func DetectTextPages(input string) ([]int, int, error) {
	ctx, err := api.ReadContextFile(context.Background(), input)
	if err != nil {
		return nil, 0, fmt.Errorf("无法读取 PDF: %w", err)
	}
	totalPages := ctx.PageCount
	var textPages []int
	for page := 1; page <= totalPages; page++ {
		pageDict, _, attrs, dictErr := ctx.XRefTable.PageDict(context.Background(), page, false)
		if dictErr != nil || pageDict == nil {
			continue
		}
		if pageHasFont(pageDict, attrs) {
			textPages = append(textPages, page)
		}
	}
	return textPages, totalPages, nil
}

// pageHasFont 页级 Resources/Font 判定（含继承属性）
func pageHasFont(pageDict types.Dict, attrs *model.InheritedPageAttrs) bool {
	if attrs != nil && len(attrs.Resources) > 0 {
		if fontDict := attrs.Resources.DictEntry("Font"); len(fontDict) > 0 {
			return true
		}
	}
	if pageDict != nil {
		if fontDict := pageDict.DictEntry("Font"); len(fontDict) > 0 {
			return true
		}
		if resources := pageDict.DictEntry("Resources"); len(resources) > 0 {
			if fontDict := resources.DictEntry("Font"); len(fontDict) > 0 {
				return true
			}
		}
	}
	return false
}
