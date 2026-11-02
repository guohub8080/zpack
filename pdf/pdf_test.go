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
