package main

import (
	"sort"
	"strings"
	"testing"
)

// pwCharsetsOf 由「开着的键」构造一份字符集状态：列出的键为开，其余为关。
func pwCharsetsOf(onKeys ...string) map[string]bool {
	charsets := map[string]bool{}
	for _, key := range onKeys {
		charsets[key] = true
	}
	return charsets
}

// TestPwCustomGroupsCoverPunctuation 自定义符号分组必须恰好铺满全部 32 个 ASCII 符号：
// 不重不漏——漏了用户就选不到那个符号，重了会在候选池里重复计权。
func TestPwCustomGroupsCoverPunctuation(t *testing.T) {
	var want []string
	for code := 33; code < 127; code++ {
		isDigit := code > 47 && code < 58
		isUpper := code > 64 && code < 91
		isLower := code > 96 && code < 123
		if !isDigit && !isUpper && !isLower {
			want = append(want, string(rune(code)))
		}
	}
	var got []string
	for _, group := range pwCustomGroups {
		for _, char := range group.chars {
			got = append(got, string(char))
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "") != strings.Join(got, "") {
		t.Errorf("符号分组铺不满 ASCII 符号：\n期望 %q\n得到 %q", strings.Join(want, ""), strings.Join(got, ""))
	}
}

// TestPwPasswordArgs 引擎参数拼装：缺省不带字符类旗标；没勾的内置类显式关掉；
// 自定义分组按分组顺序合成一个 --charset
func TestPwPasswordArgs(t *testing.T) {
	allBuiltIn := []string{PW_CHARSET_LOWER, PW_CHARSET_UPPER, PW_CHARSET_DIGIT, PW_CHARSET_SYMBOL}
	cases := []struct {
		name     string
		charsets map[string]bool
		expected string
	}{
		{"缺省全开不带旗标", pwCharsetsOf(allBuiltIn...),
			"crypto password --length 16"},
		{"关掉符号", pwCharsetsOf(PW_CHARSET_LOWER, PW_CHARSET_UPPER, PW_CHARSET_DIGIT),
			"crypto password --length 16 --symbol false"},
		{"只留数字", pwCharsetsOf(PW_CHARSET_DIGIT),
			"crypto password --length 16 --lower false --upper false --symbol false"},
		{"自定义两组按分组顺序合成", pwCharsetsOf(append(allBuiltIn,
			PW_CHARSET_CUSTOM+"bracket", PW_CHARSET_CUSTOM+"quote")...),
			"crypto password --length 16 --charset ()[]{}<>'\"`"},
		{"仅自定义分组", pwCharsetsOf(PW_CHARSET_CUSTOM + "punct"),
			"crypto password --length 16 --lower false --upper false --digit false --symbol false --charset .,:;!?"},
	}
	for _, testCase := range cases {
		application := &app{pwLenStr: "16", pwCharsets: testCase.charsets}
		if got := strings.Join(application.pwPasswordArgs(), " "); got != testCase.expected {
			t.Errorf("%s:\n期望 %s\n得到 %s", testCase.name, testCase.expected, got)
		}
	}
}

// TestPwCharsetSummary 触发器摘要：内置类短名按菜单顺序，自定义只出现一次；全不选是占位
func TestPwCharsetSummary(t *testing.T) {
	cases := []struct {
		name     string
		charsets map[string]bool
		expected string
	}{
		{"缺省", defaultPwCharsets(), "小写·大写·数字·符号"},
		{"全不选", map[string]bool{}, PW_CHARSET_NONE},
		{"自定义多组只出现一次", pwCharsetsOf(PW_CHARSET_CUSTOM+"punct", PW_CHARSET_CUSTOM+"math"), "自定义"},
		{"顺序随菜单", map[string]bool{PW_CHARSET_DIGIT: true, PW_CHARSET_LOWER: true}, "小写·数字"},
	}
	for _, testCase := range cases {
		application := &app{pwCharsets: testCase.charsets}
		if got := application.pwCharsetSummary(); got != testCase.expected {
			t.Errorf("%s: 期望 %q，得到 %q", testCase.name, testCase.expected, got)
		}
	}
}
