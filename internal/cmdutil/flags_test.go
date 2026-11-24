package cmdutil

import "testing"

// TestParseIntStrict 严格整数解析：整个字符串必须合法，前缀匹配（"5abc"）不算
func TestParseIntStrict(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		expected int
		wantOk   bool
	}{
		{"纯数字", "42", 42, true},
		{"零", "0", 0, true},
		{"带正号", "+7", 7, true},
		{"带负号", "-3", -3, true},
		{"数字后带字母不算", "5abc", 0, false},
		{"数字后带 x 不算", "3x", 0, false},
		{"小数不算", "1.5", 0, false},
		{"空串不算", "", 0, false},
		{"纯字母不算", "abc", 0, false},
	}
	for _, testCase := range cases {
		gotValue, gotOk := ParseIntStrict(testCase.text)
		if gotOk != testCase.wantOk || (testCase.wantOk && gotValue != testCase.expected) {
			t.Errorf("%s（%q）：期望 (%d, %v)，得到 (%d, %v)",
				testCase.name, testCase.text, testCase.expected, testCase.wantOk, gotValue, gotOk)
		}
	}
}
