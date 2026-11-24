// collect：按给定页序收集页面出新 PDF——「任意页码、任意顺序、任意次数」（pdfcpu
// PagesForPageCollection 的保序语义，区别于 extract 的升序去重）。PDF 页面编辑器的
// 导出原语：编辑态页序（重排/删除/复制后的序列）编译成本命令的页表。
package pdf

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// CollectResult collect 子命令的输出载荷
type CollectResult struct {
	Input     string `json:"input"`
	Output    string `json:"output"`
	PageCount int    `json:"page_count"` // 收集的页数（可含重复）
}

// ParsePageSequence 页序列解析（保序、可重复）：逗号分段，段=单页 `5` 或区间 `1-3` /
// `8-` / `-5`（区间升序展开）；**整体保序**（`3,1,2` → [3,1,2]）、**重复允许**（`2,2` →
// [2,2]）——与 ParsePagesSpec（升序去重）的语义差异是本函数存在的理由（编辑器重排导出）。
func ParsePageSequence(spec string, pageCount int) ([]int, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("--pages 缺少值（如 3,1,2 或 1-3,5）")
	}
	sequence := []int{}
	for _, token := range strings.Split(spec, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			return nil, fmt.Errorf("页序列含空段: %q", spec)
		}
		bounds := strings.SplitN(token, "-", 2)
		fromText, toText := strings.TrimSpace(bounds[0]), ""
		if len(bounds) == 2 {
			toText = strings.TrimSpace(bounds[1])
		}
		from, fromErr := strconv.Atoi(fromText)
		if fromErr != nil || from < 1 || from > pageCount {
			return nil, fmt.Errorf("页码超出范围: %s（共 %d 页）", token, pageCount)
		}
		if len(bounds) == 1 {
			sequence = append(sequence, from)
			continue
		}
		if toText == "" { // `3-` = 到末页
			for page := from; page <= pageCount; page++ {
				sequence = append(sequence, page)
			}
			continue
		}
		to, toErr := strconv.Atoi(toText)
		if toErr != nil || to < from || to > pageCount {
			return nil, fmt.Errorf("页码区间非法: %s（共 %d 页）", token, pageCount)
		}
		for page := from; page <= to; page++ {
			sequence = append(sequence, page)
		}
	}
	return sequence, nil
}

// CountPages 读 PDF 总页数（pdfcpu；collect / render 共用）
func CountPages(input string) (int, error) {
	pageCount, err := api.PageCountFile(context.Background(), input)
	if err != nil {
		return 0, fmt.Errorf("无法读取 PDF: %s", err.Error())
	}
	return pageCount, nil
}

// CollectFile 按给定页序收集出新 PDF（sequence 元素 1 基、可重复、可乱序）。
// 产物不携带旋转增量——编辑态的旋转由导出方按组调 rotate 叠加（/Rotate 可增量）。
func CollectFile(input string, output string, sequence []int) (*CollectResult, error) {
	if _, statErr := os.Stat(input); statErr != nil {
		return nil, fmt.Errorf("路径不存在: %s", input)
	}
	tokens := make([]string, len(sequence))
	for idx, page := range sequence {
		tokens[idx] = strconv.Itoa(page)
	}
	if collectErr := api.CollectFile(context.Background(), input, output, tokens, nil); collectErr != nil {
		return nil, fmt.Errorf("收集失败: %w", collectErr)
	}
	return &CollectResult{
		Input:     input,
		Output:    output,
		PageCount: len(sequence),
	}, nil
}
