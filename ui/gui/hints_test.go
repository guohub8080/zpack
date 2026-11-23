package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 标签列（84px）里的副标 12px 字一行最多七个汉字——超了就折行，八字文案更会掉一个孤字。
// 2026-11-23 全页面审计后定：副标一律改短文案、不动布局；本用例守住「恒一行」。

const (
	HINT_COLUMN_LEFT  = 40 // 标签列文字的左缘上限（内容留白 28）
	HINT_COLUMN_WIDTH = 90 // 标签列宽度上限（列宽 84）
	HINT_ONE_LINE_TOP = 28 // 两行文字的高度下限（一行约 16）
)

// hintStates 是受检的页面状态（都填成非空态——页面函数假定清单非空）。
func hintStates(sample feedbackSamples) []struct {
	note string
	page func(a *app, c *ui.Context)
	fill func(a *app)
} {
	return []struct {
		note string
		page func(a *app, c *ui.Context)
		fill func(a *app)
	}{
		{"压缩台", (*app).pageCompress, func(a *app) {
			a.inputs = []string{sample.report}
			a.syncName()
			a.advancedOpen = true
		}},
		{"解压台", (*app).pageExtract, func(a *app) { a.extractPath = sample.manual }},
		{"加密·文本", (*app).pageEncrypt, func(a *app) {}},
		{"加密·文件", (*app).pageEncrypt, func(a *app) { a.encModeIdx = 1; a.encFile = sample.manual }},
		{"解密·文本", (*app).pageDecrypt, func(a *app) {}},
		{"解密·文件", (*app).pageDecrypt, func(a *app) { a.decModeIdx = 1; a.decFile = sample.manual }},
		{"密钥", (*app).pageKeys, func(a *app) {}},
		{"Base64", (*app).pageBase64, func(a *app) {}},
		{"视频转换", (*app).pageMedia, func(a *app) { a.mediaPath = sample.video }},
		{"图片转换", (*app).pageImage, func(a *app) { a.imagePath = sample.photo }},
		{"PDF 合并", (*app).pagePdfMerge, func(a *app) { a.pdfMergeInputs = []string{sample.manual, sample.document} }},
		{"PDF 提取", (*app).pagePdfExtract, func(a *app) { a.pdfExtractPath = sample.document; a.pdfExtractPages = "1-3" }},
		{"视频压缩", (*app).pageMediaCmp, func(a *app) { a.mediaCmpPath = sample.video }},
		{"音频转换", (*app).pageAudioCvt, func(a *app) { a.audioCvtPath = sample.song }},
		{"音频压缩", (*app).pageAudioCmp, func(a *app) { a.audioCmpPath = sample.song }},
		{"图片压缩", (*app).pageImageCmp, func(a *app) { a.imageCmpInputs = []string{sample.photo} }},
		{"新建图片", (*app).pageImageNew, func(a *app) {}},
		{"Office", (*app).pageOffice, func(a *app) { a.officePath = sample.report }},
		{"PDF 旋转", (*app).pagePdfRotate, func(a *app) { a.pdfRotatePath = sample.document }},
		{"页面尺寸", (*app).pagePdfResize, func(a *app) { a.pdfResizePath = sample.document }},
		{"哈希", (*app).pageHash, func(a *app) { a.hashInputs = []string{sample.manual} }},
		{"随机·密码", (*app).pageRandom, func(a *app) {}},
		{"随机·UUID", (*app).pageRandom, func(a *app) { a.randomModeIdx = 1 }},
		{"刻录", (*app).pageBurn, sample.withDevice()},
		{"归档浏览", (*app).pageArchive, sample.archiveLoaded()},
		{"设置", (*app).pageSettings, func(a *app) {}},
	}
}

// TestLabelHintsStayOnOneLine 标签列里的副标不折行（每页的常见状态逐一量）。
func TestLabelHintsStayOnOneLine(t *testing.T) {
	registerMiSans()
	sample := newFeedbackSamples(t)
	t.Setenv("ZPACK_BIN", sample.report)
	for _, state := range hintStates(sample) {
		application := newApp()
		state.fill(application)
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(brandTheme(c))
			state.page(application, c)
		}, FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		seen := map[string]bool{}
		for _, text := range tester.Texts() {
			rect, found := tester.Find(text)
			if !found || seen[text] || rect.X > HINT_COLUMN_LEFT || rect.W > HINT_COLUMN_WIDTH ||
				rect.H < HINT_ONE_LINE_TOP {
				continue
			}
			seen[text] = true
			t.Errorf("%s：标签列副标折行了（高 %.0f）%q——改成一行放得下的短文案", state.note, rect.H, text)
		}
	}
}
