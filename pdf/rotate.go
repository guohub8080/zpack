// PDF 旋转：/Rotate 页面字典属性（元数据级零损——渲染器按属性转，内容字节不动，
// 与视频写 display matrix 同构）。90° 倍数是 PDF 的原生能力；mirror 无对应属性
// （需改写内容矩阵，文本会镜像不可读）——不支持，语义如实。
package pdf

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// ROTATE_CLOCKWISE pdfcpu 顺时针角度映射（rotate-right=90 / rotate-180=180 / rotate-left=270）
const (
	ROTATE_RIGHT_DEGREES = 90
	ROTATE_LEFT_DEGREES  = 270
	ROTATE_180_DEGREES   = 180
)

// RotateResult 旋转结果
type RotateResult struct {
	Input      string `json:"input"`
	Output     string `json:"output"`
	Direction  string `json:"direction"` // left / right / 180（语义名，同 image/media 家族）
	PagesSpec  string `json:"pages_spec,omitempty"`
	Pages      []int  `json:"pages"`       // 实际旋转的页码（升序；空规格=全部页）
	PageCount  int    `json:"page_count"`  // 旋转页数
	TotalPages int    `json:"total_pages"` // 文档总页数
	SizeBytes  int64  `json:"size_bytes"`
}

// RotateDirectionToDegrees 语义方向 → pdfcpu 顺时针角度
func RotateDirectionToDegrees(direction string) (int, error) {
	switch direction {
	case "right":
		return ROTATE_RIGHT_DEGREES, nil
	case "left":
		return ROTATE_LEFT_DEGREES, nil
	case "180":
		return ROTATE_180_DEGREES, nil
	}
	return 0, fmt.Errorf("方向需 left/right/180，收到: %s", direction)
}

// Rotate 旋转指定页（pagesSpec 空=全部页；复用 extract 的页码规格——宽尾钳制同语义）
func Rotate(input, direction, pagesSpec, output string, logf func(format string, args ...any)) (*RotateResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	degrees, dirErr := RotateDirectionToDegrees(direction)
	if dirErr != nil {
		return nil, dirErr
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
	if pagesSpec == "" { // 空=全部页
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
	if rotateErr := api.RotateFile(context.Background(), input, output, degrees, tokens, nil); rotateErr != nil {
		return nil, fmt.Errorf("旋转失败: %w", rotateErr)
	}
	outputInfo, _ := os.Stat(output)
	return &RotateResult{
		Input: absPath(input), Output: absPath(output),
		Direction: direction, PagesSpec: pagesSpec, Pages: pages,
		PageCount: len(pages), TotalPages: totalPages,
		SizeBytes: outputInfo.Size(),
	}, nil
}
