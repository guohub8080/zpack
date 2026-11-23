package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCryptoCharsetPool 候选池：只收启用的字符类，字符去重，按首次出现的顺序
func TestCryptoCharsetPool(t *testing.T) {
	cases := []struct {
		name     string
		charsets []cryptoCharset
		expected string
	}{
		{"全部启用保持字符类顺序", []cryptoCharset{
			{"digit", "012", true}, {"lower", "ab", true}}, "012ab"},
		{"关闭的字符类不进池", []cryptoCharset{
			{"digit", "012", false}, {"lower", "ab", true}}, "ab"},
		{"自定义集与内置类重叠的字符只留一份", []cryptoCharset{
			{"symbol", "()!", true}, {"custom", "(!+", true}}, "()!+"},
		{"非 ASCII 字符按字符去重", []cryptoCharset{
			{"custom", "中文中", true}, {"custom2", "文密", true}}, "中文密"},
		{"全关得到空池", []cryptoCharset{
			{"digit", "012", false}}, ""},
	}
	for _, testCase := range cases {
		if got := cryptoCharsetPool(testCase.charsets); got != testCase.expected {
			t.Errorf("%s: 期望 %q，得到 %q", testCase.name, testCase.expected, got)
		}
	}
}

// TestCryptoGeneratePasswordCharset 自定义字符集：长度按字符数算、只含池内字符、
// 启用的字符类各保底 1 个
func TestCryptoGeneratePasswordCharset(t *testing.T) {
	const (
		LENGTH      = 64
		SAMPLES     = 32
		CJK_CHARSET = "中文密码"
	)
	// 仅自定义集（非 ASCII）：每个字符都是完整的 rune
	onlyCustom := []cryptoCharset{{"custom", CJK_CHARSET, true}}
	for sampleIdx := 0; sampleIdx < SAMPLES; sampleIdx++ {
		password, genErr := cryptoGeneratePassword(LENGTH, onlyCustom, cryptoCharsetPool(onlyCustom))
		if genErr != nil {
			t.Fatalf("随机源失败: %v", genErr)
		}
		runes := []rune(password)
		if len(runes) != LENGTH {
			t.Fatalf("长度按字符数应为 %d，得到 %d（%q）", LENGTH, len(runes), password)
		}
		if !utf8.ValidString(password) {
			t.Fatalf("输出不是合法 UTF-8: %q", password)
		}
		for _, char := range runes {
			if !strings.ContainsRune(CJK_CHARSET, char) {
				t.Fatalf("出现了字符集之外的字符 %q: %q", char, password)
			}
		}
	}
	// 自定义集与数字类同开：两者各保底 1 个
	mixed := []cryptoCharset{{"digit", "0123456789", true}, {"custom", "!#", true}}
	for sampleIdx := 0; sampleIdx < SAMPLES; sampleIdx++ {
		password, genErr := cryptoGeneratePassword(8, mixed, cryptoCharsetPool(mixed))
		if genErr != nil {
			t.Fatalf("随机源失败: %v", genErr)
		}
		if strings.ContainsAny(password, "!#") && strings.ContainsAny(password, "0123456789") {
			continue
		}
		t.Fatalf("数字类与自定义集应各保底 1 个，得到 %q", password)
	}
}

// TestCryptoCharsetFlagValue --charset 两式解析：--charset 值 / --charset=值；没给返回 false
func TestCryptoCharsetFlagValue(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected string
		found    bool
	}{
		{"空格式", []string{"--length", "16", "--charset", "abc"}, "abc", true},
		{"等号式", []string{"--charset=abc"}, "abc", true},
		{"值以连字符开头照收", []string{"--charset", "-_+"}, "-_+", true},
		{"空串也算给了", []string{"--charset", ""}, "", true},
		{"没给", []string{"--length", "16"}, "", false},
		{"只认完整旗标名", []string{"--charsets", "abc"}, "", false},
	}
	for _, testCase := range cases {
		gotValue, gotFound := cryptoCharsetFlagValue(testCase.args)
		if gotValue != testCase.expected || gotFound != testCase.found {
			t.Errorf("%s: 期望 (%q, %v)，得到 (%q, %v)", testCase.name, testCase.expected, testCase.found, gotValue, gotFound)
		}
	}
}
