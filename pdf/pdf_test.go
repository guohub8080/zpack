package pdf

import (
	"strings"
	"testing"
)

func TestParsePagesSpecClampNotice(t *testing.T) {
	_, notices, err := ParsePagesSpec("4-10", 5)
	if err != nil {
		t.Fatalf("宽尾不应报错: %v", err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "已截至第 5 页") {
		t.Errorf("钳制提示缺失: %v", notices)
	}
	_, notices, _ = ParsePagesSpec("1-2", 5)
	if len(notices) != 0 {
		t.Errorf("未越界不应有提示: %v", notices)
	}
}

func TestParsePagesSpec(t *testing.T) {
	tests := []struct {
		name      string
		spec      string
		pageCount int
		want      []int
		wantErr   bool
	}{
		{"单页", "3", 5, []int{3}, false},
		{"闭区间", "2-4", 5, []int{2, 3, 4}, false},
		{"开尾到末页", "4-", 5, []int{4, 5}, false},
		{"前开从首页", "-2", 5, []int{1, 2}, false},
		{"散列组合", "1,3,5", 5, []int{1, 3, 5}, false},
		{"去重升序", "3,1,3", 5, []int{1, 3}, false},
		{"区间含散列", "2-3,5", 5, []int{2, 3, 5}, false},
		{"恰好首页", "1", 1, []int{1}, false},
		{"越界单页", "6", 5, nil, true},
		{"区间宽尾钳制", "3-9", 5, []int{3, 4, 5}, false},
		{"前开宽尾钳制", "-9", 3, []int{1, 2, 3}, false},
		{"起始页越界", "6-9", 5, nil, true},
		{"开尾起始越界", "6-", 5, nil, true},
		{"反序区间", "4-2", 5, nil, true},
		{"零页", "0", 5, nil, true},
		{"非数字", "abc", 5, nil, true},
		{"空规格", "", 5, nil, true},
		{"孤连字符", "-", 5, nil, true},
	}
	for _, tt := range tests {
		got, _, err := ParsePagesSpec(tt.spec, tt.pageCount)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%s: 期望报错，得到 %v", tt.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: 意外报错 %v", tt.name, err)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("%s: got %v want %v", tt.name, got, tt.want)
			continue
		}
		for idx := range got {
			if got[idx] != tt.want[idx] {
				t.Errorf("%s: got %v want %v", tt.name, got, tt.want)
				break
			}
		}
	}
}

// TestParsePageSizeSpecPaperNameCase 纸型名不分大小写，且不能把本身就以 L/P 结尾的纸型当成方向尾缀
// （2026-11-24 修复：letter / legal 等 53 个纸型因 pdfcpu 纸型表是混合大小写的键而一律报「需纸型」）
func TestParsePageSizeSpecPaperNameCase(t *testing.T) {
	cases := []struct {
		name              string
		spec              string
		wantWidth         int
		wantHeight        int
		wantEnforceOrient bool
	}{
		{"letter 小写", "letter", 612, 792, false},
		{"LETTER 大写", "LETTER", 612, 792, false},
		{"legal 以 L 结尾，不是横向尾缀", "legal", 612, 1008, false},
		{"a4l 强制横向", "a4l", 842, 595, true},
		{"A4P 强制纵向", "A4P", 595, 842, true},
	}
	for _, testCase := range cases {
		resize, parseErr := ParsePageSizeSpec(testCase.spec)
		if parseErr != nil {
			t.Errorf("%s（%q）：不应报错 %v", testCase.name, testCase.spec, parseErr)
			continue
		}
		if int(resize.PageDim.Width) != testCase.wantWidth || int(resize.PageDim.Height) != testCase.wantHeight || resize.EnforceOrient != testCase.wantEnforceOrient {
			t.Errorf("%s（%q）：期望 %dx%d 强制方向=%v，得到 %dx%d 强制方向=%v", testCase.name, testCase.spec,
				testCase.wantWidth, testCase.wantHeight, testCase.wantEnforceOrient, int(resize.PageDim.Width), int(resize.PageDim.Height), resize.EnforceOrient)
		}
	}
	if _, parseErr := ParsePageSizeSpec("nosuchpaper"); parseErr == nil {
		t.Error("未知纸型应报错")
	}
}
