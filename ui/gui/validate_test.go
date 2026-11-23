package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestValidatePagesSpec 页码规格的语法与范围（引擎 pdf.ParsePagesSpec 同规则，不看页数）。
func TestValidatePagesSpec(t *testing.T) {
	cases := []struct {
		text     string
		required bool
		wantBad  bool // 期望「有错」
	}{
		{"1-3,5,8-", true, false},
		{" 1-3 , 5 ", true, false},
		{"1 - 3", true, true}, // 区间两侧不能夹空格（引擎 Atoi("1 ") 同样失败）
		{"-5", true, false},
		{"8-", true, false},
		{"12", true, false},
		{"1-3,,5", true, true},
		{"5-3", true, true},
		{"0", true, true},
		{"a4", true, true},
		{"1--3", true, true},
		{"-", true, true},
		{"-0", true, true},
		{"", true, true},
		{"", false, false}, // 可选页码：留空合法
	}
	for _, testCase := range cases {
		if got := validatePagesSpec(testCase.text, testCase.required); (got != "") != testCase.wantBad {
			t.Errorf("页码 %q（必填=%v）期望有错=%v，得到 %q", testCase.text, testCase.required, testCase.wantBad, got)
		}
	}
}

// TestValidateByteSize 体积：正整数 + 可选单位、模板名、下限。
func TestValidateByteSize(t *testing.T) {
	cases := []struct {
		text    string
		minimum int64
		wantBad bool
	}{
		{"100MB", 0, false},
		{"2GB", 0, false},
		{"65536", 0, false},
		{"64KB", VOLUME_MIN_BYTES, false},
		{"63KB", VOLUME_MIN_BYTES, true}, // 低于分卷下限
		{"65535", VOLUME_MIN_BYTES, true},
		{"dvd", VOLUME_MIN_BYTES, false}, // 模板名
		{"1.5MB", 0, true},               // 引擎只认整数
		{"0", 0, true},
		{"-5MB", 0, true},
		{"", 0, true},
		{"abc", 0, true},
		{"1TB", 0, true}, // 引擎不认 TB
	}
	for _, testCase := range cases {
		if got := validateByteSize(testCase.text, testCase.minimum); (got != "") != testCase.wantBad {
			t.Errorf("体积 %q（下限 %d）期望有错=%v，得到 %q", testCase.text, testCase.minimum, testCase.wantBad, got)
		}
	}
}

// TestValidateAudio 码率与采样率（引擎 parseBitrateStr / parseIntStrict 同规则）。
func TestValidateAudio(t *testing.T) {
	bad := []string{"128kbps", "0k", "3300k", "abc", "-5k", "8k8"}
	for _, text := range bad {
		if got := validateBitrate(text); got == "" {
			t.Errorf("码率 %q 应判错", text)
		}
	}
	// 引擎「128」去掉 k 后仍是合法数字——这里同款放行
	for _, text := range []string{"", "128k", "128", "3200K", "1k"} {
		if got := validateBitrate(text); got != "" {
			t.Errorf("码率 %q 应合法，得到 %q", text, got)
		}
	}
	for _, text := range []string{"", "44100", "8000", "192000"} {
		if got := validateSampleRate(text); got != "" {
			t.Errorf("采样率 %q 应合法，得到 %q", text, got)
		}
	}
	for _, text := range []string{"7999", "192001", "abc", "44.1k"} {
		if got := validateSampleRate(text); got == "" {
			t.Errorf("采样率 %q 应判错", text)
		}
	}
}

// TestValidateMediaTarget 视频压缩目标：百分比 / 体积 / 模板名。
func TestValidateMediaTarget(t *testing.T) {
	for _, text := range []string{"50%", "80MB", "500KB", "cd", "100%", "1%"} {
		if got := validateMediaTarget(text); got != "" {
			t.Errorf("目标 %q 应合法，得到 %q", text, got)
		}
	}
	for _, text := range []string{"", "0%", "101%", "abc%", "1.5MB", "0"} {
		if got := validateMediaTarget(text); got == "" {
			t.Errorf("目标 %q 应判错", text)
		}
	}
}

// TestValidateImageNew 新建图片：尺寸、颜色、打印密度。
func TestValidateImageNew(t *testing.T) {
	for _, text := range []string{"800x600", "55x90mm", "35x45CM", "8.5x11in", "a4", "a4l", "letter", "ledger"} {
		if got := validateImageSize(text); got != "" {
			t.Errorf("尺寸 %q 应合法，得到 %q", text, got)
		}
	}
	// 「legal」被引擎的「尾缀 L=横向」规则误伤（core 变 lega）——GUI 同款判错，不误放行
	if got := validateImageSize("legal"); got == "" {
		t.Errorf("尺寸 legal 应判错（引擎同款）")
	}
	for _, text := range []string{"", "800", "0x600", "axb", "800x600px"} {
		if got := validateImageSize(text); got == "" {
			t.Errorf("尺寸 %q 应判错", text)
		}
	}
	for _, text := range []string{"", "transparent", "red", "#FF0000", "#3344FF88", "Grey"} {
		if got := validateImageColor(text); got != "" {
			t.Errorf("颜色 %q 应合法，得到 %q", text, got)
		}
	}
	for _, text := range []string{"#FFF", "#GGHHII", "notacolor", "#FF0000AA1"} {
		if got := validateImageColor(text); got == "" {
			t.Errorf("颜色 %q 应判错", text)
		}
	}
	for _, text := range []string{"", "300", "2400", "1"} {
		if got := validatePPI(text); got != "" {
			t.Errorf("打印密度 %q 应合法，得到 %q", text, got)
		}
	}
	for _, text := range []string{"0", "2401", "300dpi", "-1"} {
		if got := validatePPI(text); got == "" {
			t.Errorf("打印密度 %q 应判错", text)
		}
	}
}

// TestValidateHexHash 期望哈希：按算法位数 + 十六进制。
func TestValidateHexHash(t *testing.T) {
	if got := validateHexHash("3b7f", "sha256"); got == "" {
		t.Errorf("短哈希应判错")
	}
	if got := validateHexHash("abc", "md5"); got == "" {
		t.Errorf("非十六进制应判错")
	}
	if got := validateHexHash("d41d8cd98f00b204e9800998ecf8427e", "md5"); got != "" {
		t.Errorf("md5 应合法，得到 %q", got)
	}
	if got := validateHexHash("A1B2C3D4", "crc32"); got != "" {
		t.Errorf("crc32 应合法，得到 %q", got)
	}
	// 不认识的算法不拦（交给引擎）
	if got := validateHexHash("zzz", "unknown"); got != "" {
		t.Errorf("未知算法应放行，得到 %q", got)
	}
}

// TestVisibleIssue 必填项空着时只禁用主按钮、不飘红。
func TestVisibleIssue(t *testing.T) {
	if got := visibleIssue("页码区间必填", ""); got != "" {
		t.Errorf("空输入不应飘红，得到 %q", got)
	}
	if got := visibleIssue("页码区间必填", "  "); got != "" {
		t.Errorf("纯空白不应飘红，得到 %q", got)
	}
	if got := visibleIssue("页码区间必填", "5-3"); got == "" {
		t.Errorf("填了且错了应飘红")
	}
}

// TestInvalidInputDisablesPrimary 填错时主按钮置灰（无手型）、改对后恢复（有手型）——
// 就地红字之外的第二道闸：不让错误参数跑到处理台才报错。
func TestInvalidInputDisablesPrimary(t *testing.T) {
	registerMiSans()
	sample := newFeedbackSamples(t)
	type ctaCase struct {
		note   string
		page   func(a *app, c *ui.Context)
		label  string
		broken func(a *app) // 填错的状态（含输入文件）
		repair func(a *app) // 在 broken 之后改对那一项
	}
	cases := []ctaCase{
		{"PDF 提取页码", (*app).pagePdfExtract, "提取页面",
			func(a *app) { a.pdfExtractPath = sample.document; a.pdfExtractPages = "5-3" },
			func(a *app) { a.pdfExtractPages = "1-3" }},
		{"PDF 旋转页码", (*app).pagePdfRotate, "旋转",
			func(a *app) { a.pdfRotatePath = sample.document; a.pdfRotatePages = "a" },
			func(a *app) { a.pdfRotatePages = "1-3" }},
		{"音频码率", (*app).pageAudioCvt, "转换",
			func(a *app) { a.audioCvtPath = sample.song; a.audioCvtBitrate = "128kbps" },
			func(a *app) { a.audioCvtBitrate = "128k" }},
		{"新建图片尺寸", (*app).pageImageNew, "生成图片",
			func(a *app) { a.imageNewSize = "800" },
			func(a *app) { a.imageNewSize = "800x600" }},
		{"视频压缩目标", (*app).pageMediaCmp, "压缩视频",
			func(a *app) { a.mediaCmpPath = sample.video; a.mediaCmpMode = "自定义"; a.mediaCmpSize = "120%" },
			func(a *app) { a.mediaCmpSize = "80MB" }},
	}
	// 主按钮有手型 = 可点；置灰态去掉手型
	primaryClickable := func(testCase ctaCase, fill func(a *app)) bool {
		application := newApp()
		fill(application)
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(brandTheme(c))
			testCase.page(application, c)
		}, FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		rect, found := tester.Find(testCase.label)
		if !found {
			t.Fatalf("%s：找不到主按钮 %q", testCase.note, testCase.label)
		}
		tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
		tester.Frame()
		return tester.Cursor() == ui.CursorPointer
	}
	for _, testCase := range cases {
		if primaryClickable(testCase, testCase.broken) {
			t.Errorf("%s：填错时主按钮仍有手型（没置灰）", testCase.note)
		}
		if !primaryClickable(testCase, func(a *app) { testCase.broken(a); testCase.repair(a) }) {
			t.Errorf("%s：改对后主按钮没有手型", testCase.note)
		}
	}
}
