package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// LIST_BADGE_CENTER_X 是清单卡里移除徽章中心的横坐标（无头窗口宽 500）：卡右缘
// 500-28，卡内边距 12，徽章 24 宽贴尾槽右缘。
const LIST_BADGE_CENTER_X = 500 - 28 - 12 - 12

// heroAwarePage 照 pane 的规则建页面：空态（heroPage）时不建页面函数——页面函数本身
// 假定清单非空（直接渲染空清单会读 inputs[0] 越界），真实应用里这一支走 heroPane。
func heroAwarePage(application *app, page func(a *app, c *ui.Context)) func(c *ui.Context) {
	return func(c *ui.Context) {
		c.SetTheme(brandTheme(c))
		if !application.heroPage() {
			page(application, c)
		}
	}
}

// removeBadgeClick 悬停清单行让移除徽章浮现，再点徽章。
func removeBadgeClick(t *testing.T, note, rowText string, tester *ui.Tester) {
	t.Helper()
	rect, found := tester.Find(rowText)
	if !found {
		for idx, text := range tester.Texts() {
			t.Logf("%s 文字[%d] %q", note, idx, text)
		}
		t.Fatalf("%s：找不到清单行文字 %q", note, rowText)
	}
	centerY := rect.Y + rect.H/2
	tester.Move(rect.X+rect.W/2, centerY) // 行悬停：徽章滑入
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	tester.Move(LIST_BADGE_CENTER_X, centerY)
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	tester.Press(LIST_BADGE_CENTER_X, centerY)
	tester.Frame()
	tester.Release(LIST_BADGE_CENTER_X, centerY)
	tester.Frame()
}

// TestRemoveLastListItem 删掉清单里的最后一项不能崩：移除此前在建帧中途落地，而同一帧
// 后面的参数区还要读首输入（outputPlan / 输出位置），压缩台与 PDF 合并删最后一项即越界
// （2026-11-23 无头实测复现）。现在移除统一在建帧结束后落地。
func TestRemoveLastListItem(t *testing.T) {
	registerMiSans()
	sample := newFeedbackSamples(t)
	type listCase struct {
		note      string
		id        string // 侧栏页标识（heroPage 按它判定空态）
		page      func(a *app, c *ui.Context)
		fill      func(a *app)
		text      string
		remaining int // 点完移除徽章后应剩的项数
		count     func(a *app) int
	}
	cases := []listCase{
		{"压缩台", pageCompress, (*app).pageCompress, func(a *app) {
			a.inputs = []string{sample.report}
			a.syncName()
		}, sample.report, 0, func(a *app) int { return len(a.inputs) }},
		{"PDF 合并", pagePdfMerge, (*app).pagePdfMerge, func(a *app) { a.pdfMergeInputs = []string{sample.manual} },
			sample.manual, 0, func(a *app) int { return len(a.pdfMergeInputs) }},
		{"哈希", pageHash, (*app).pageHash, func(a *app) { a.hashInputs = []string{sample.manual} },
			sample.manual, 0, func(a *app) int { return len(a.hashInputs) }},
		// 图片压缩单张走文件字段不出清单（N=1 特例）：两张起步，删一张后落到单张字段
		{"图片压缩", pageImageCmp, (*app).pageImageCmp, func(a *app) {
			a.imageCmpInputs = []string{sample.photo, sample.manual}
		}, sample.photo, 1, func(a *app) int { return len(a.imageCmpInputs) }},
		{"刻录", pageBurn, (*app).pageBurn, sample.withDevice(), sample.manual,
			0, func(a *app) int { return len(a.burnInputs) }},
	}
	for _, testCase := range cases {
		application := newApp()
		application.page = testCase.id
		testCase.fill(application)
		tester := ui.NewTester(heroAwarePage(application, testCase.page), FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		removeBadgeClick(t, testCase.note, testCase.text, tester)
		if remaining := testCase.count(application); remaining != testCase.remaining {
			t.Errorf("%s：点移除徽章后清单剩 %d 项，期望 %d", testCase.note, remaining, testCase.remaining)
		}
		// 再建一帧：删完后的状态必须建得出来（空清单走 hero，单张走字段）
		tester.Frame()
	}
}
