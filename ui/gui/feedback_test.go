package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 可点元素反馈矩阵——交互铁律的验收（AGENTS.md「GUI 交互铁律」）：凡可点击必带 hover +
// 按下反馈 + 手型光标。每个目标渲染三帧（静止 → 悬停 → 按住），量全窗像素差。
// 新增可点元素请登记到 feedbackTargets；真有豁免理由的（如拖拽把手行）标 rowOnly 并写明。
//
// 量法要点（2026-11-23 审计踩过）：
//   - 页面函数单独渲染、不带侧栏——否则 Find("加密") 命中的是侧栏导航项而不是页内按钮；
//   - 窗口拉高到 1100，保证目标都在屏内（Find 返回布局坐标，屏外元素悬停不到）；
//   - 像素差阈值 4：浅色主题悬停底 #E9EDF2 对页面底 #FAFBFC 每通道只差十几级，阈值高了漏判；
//   - Tester.Image 可能复用缓冲，比较前先拷贝。

const (
	FEEDBACK_WINDOW_WIDTH     = 500  // 内容区宽度（不含侧栏）
	FEEDBACK_WINDOW_HEIGHT    = 1100 // 拉高：让每页的全部目标都在屏内
	FEEDBACK_PIXEL_DELTA      = 4    // 单通道差超过它才算「变了」
	FEEDBACK_MIN_PIXELS       = 20   // 反馈至少改动的像素数（低于它视为没有反馈）
	FEEDBACK_ONE_LINE_HEIGHT  = 24   // 单行文字的高度上限（13px 字 ≈ 17–18）——长路径高过它才说明真换行了
	FEEDBACK_PICK_LABEL_WIDTH = 24   // 「选择」两个字的宽度下限（被挤窄即小于它）
	FEEDBACK_PAGE_MARGIN      = 28   // 页面内容列的左右留白
	FEEDBACK_MIN_WINDOW_WIDTH = 500  // 窗口最小宽度（引导行 / 清单行在此宽度下最容易挤坏）
	FEEDBACK_LEAD_MIN_WIDTH   = 100  // 最小窗口下引导句的宽度下限
	FEEDBACK_LEAD_MAX_HEIGHT  = 60   // 引导句高度上限（约三行）
	PICK_MIN_GAP              = 11   // 选择钮文字到字段右缘的最小距离（钮内边距 8 + 内缩 3；写死，不引用实现常量）
)

// feedbackTarget 是一个受检的可点元素。
type feedbackTarget struct {
	page    func(a *app, c *ui.Context)
	label   string       // 定位文字（元素的文本或 Label）
	note    string       // 说明——失败时报出来
	fill    func(a *app) // 把页面填到目标出现的状态
	rowOnly bool         // 豁免按下态与手型：行本身不是点击目标（拖拽把手 / 悬停才浮现操作钮的行）
}

// feedbackSamples 是造好的样例文件（有真实体积，清单行才有东西可显示）。
type feedbackSamples struct {
	dir      string
	report   string // 季度报表.xlsx
	design   string // 设计稿.psd
	manual   string // 手册.pdf
	video    string // video-export.mp4
	song     string // 歌曲.mp3
	photo    string // 照片.jpg
	document string // 文档.pdf
	key      string // k.pem（不存在的路径即可：密钥页只显示文件名）
}

func (sample feedbackSamples) withDevice() func(a *app) {
	return func(a *app) {
		a.burnInputs = []string{sample.manual}
		a.burnQueried = true
		a.burnDevices = []BurnDevice{{Index: 0, Path: "/dev/sr0", Vendor: "HL-DT-ST", Product: "DVDRAM", Status: "blank"}}
		a.burnDevStr = "1 · HL-DT-ST DVDRAM（空白）"
	}
}

func (sample feedbackSamples) keysDone() func(a *app) {
	return func(a *app) { a.keyDone = &ToolResult{Output: sample.key, Extra: sample.key + ".pub"} }
}

func (sample feedbackSamples) hashDone() func(a *app) {
	return func(a *app) {
		a.hashInputs = []string{sample.manual}
		a.hashResults = []hashResult{{Path: sample.manual, Hash: "3b7f", Size: 1, Algo: "SHA256"}}
	}
}

func (sample feedbackSamples) archiveLoaded() func(a *app) {
	return func(a *app) {
		a.archivePath = sample.manual
		a.archiveListing = &ListResult{Format: "zip", Entries: []ArchiveEntry{{Name: "源码/main.go", Size: 1}}}
	}
}

// feedbackTargets 是全部受检目标。顺序无关。
func feedbackTargets(sample feedbackSamples) []feedbackTarget {
	withCompress := func(a *app) {
		a.inputs = []string{sample.report}
		a.syncName()
	}
	withTwoPdfs := func(a *app) { a.pdfMergeInputs = []string{sample.manual, sample.document} }
	withMedia := func(a *app) { a.mediaPath = sample.video }
	withMediaCmp := func(a *app) { a.mediaCmpPath = sample.video }
	withPhoto := func(a *app) { a.imageCmpInputs = []string{sample.photo} }
	return []feedbackTarget{
		{(*app).pageCompress, "浏览选择…", "压缩台·引导行浏览钮", withCompress, false},
		{(*app).pageCompress, "选择", "压缩台·保存位置选择钮", withCompress, false},
		{(*app).pageCompress, ".zip", "压缩台·后缀下拉触发器", withCompress, false},
		{(*app).pageCompress, "无动作", "压缩台·完成后下拉触发器", withCompress, false},
		{(*app).pageCompress, "高级设置", "压缩台·高级设置折叠头", withCompress, false},
		{(*app).pageCompress, sample.report, "压缩台·清单行（拖拽把手）", withCompress, true},
		{(*app).pageCompress, "取消压缩", "压缩台·取消钮", withCompress, false},
		{(*app).pageCompress, "创建压缩包", "压缩台·主按钮", withCompress, false},
		{(*app).pageExtract, "浏览选择…", "解压台·引导行浏览钮", func(a *app) { a.extractPath = sample.manual }, false},
		{(*app).pageExtract, "取消解压", "解压台·取消钮", func(a *app) { a.extractPath = sample.manual }, false},
		{(*app).pageExtract, "解压", "解压台·主按钮", func(a *app) { a.extractPath = sample.manual }, false},
		{(*app).pageEncrypt, "文件", "加密·输入方式分段", func(a *app) {}, false},
		{(*app).pageEncrypt, "选择", "加密·文件字段选择钮", func(a *app) { a.encModeIdx = 1; a.encFile = sample.manual }, false},
		{(*app).pageEncrypt, "加密", "加密·主按钮", func(a *app) { a.encText = "会议纪要"; a.encKey = sample.key }, false},
		{(*app).pageEncrypt, "复制", "加密·结果复制钮", func(a *app) { a.encTextOut = "QUJDREVGRw==" }, false},
		{(*app).pageDecrypt, "解密", "解密·主按钮", func(a *app) { a.decText = "QUJD"; a.decKey = sample.key }, false},
		{(*app).pageKeys, "4096", "密钥·长度分段", func(a *app) {}, false},
		{(*app).pageKeys, "选择", "密钥·保存位置选择钮", func(a *app) {}, false},
		{(*app).pageKeys, "生成密钥", "密钥·主按钮", func(a *app) {}, false},
		{(*app).pageKeys, revealLabel(), "密钥·结果卡定位钮", sample.keysDone(), false},
		{(*app).pageKeys, filepath.Base(sample.key), "密钥·结果卡文件名（点击复制）", sample.keysDone(), false},
		{(*app).pageBase64, "文本 → Base64", "Base64·主按钮", func(a *app) { a.b64In = "zpack" }, false},
		{(*app).pageBase64, "清除", "Base64·输入清除钮", func(a *app) { a.b64In = "zpack" }, false},
		{(*app).pageBase64, "zpack", "Base64·历史卡（点击复制）", func(a *app) {
			a.b64Results = []b64Result{{from: "zpack", to: "enBhY2s=", enc: true}}
		}, false},
		{(*app).pageMedia, "浏览选择…", "视频转换·引导行浏览钮", withMedia, false},
		{(*app).pageMedia, "选择", "视频转换·文件字段选择钮", withMedia, false},
		{(*app).pageMedia, "MP4", "视频转换·格式下拉触发器", withMedia, false},
		{(*app).pageMedia, "转换", "视频转换·主按钮", withMedia, false},
		{(*app).pageMedia, "取消", "视频转换·取消钮（回空态）", withMedia, false},
		{(*app).pageImage, "选择", "图片转换·输出位置选择钮", func(a *app) { a.imagePath = sample.photo }, false},
		{(*app).pagePdfMerge, "浏览选择…", "PDF合并·引导行浏览钮", withTwoPdfs, false},
		{(*app).pagePdfMerge, sample.manual, "PDF合并·清单行（拖拽把手）", withTwoPdfs, true},
		{(*app).pagePdfMerge, "合并 PDF", "PDF合并·主按钮", withTwoPdfs, false},
		{(*app).pagePdfMerge, "取消", "PDF合并·取消钮（回空态）", withTwoPdfs, false},
		{(*app).pagePdfExtract, "提取页面", "PDF提取·主按钮", func(a *app) {
			a.pdfExtractPath = sample.document
			a.pdfExtractPages = "1-3"
		}, false},
		{(*app).pageMediaCmp, "50%", "视频压缩·目标体积下拉", withMediaCmp, false},
		{(*app).pageMediaCmp, "压缩视频", "视频压缩·主按钮", withMediaCmp, false},
		{(*app).pageAudioCvt, "转换", "音频转换·主按钮", func(a *app) { a.audioCvtPath = sample.song }, false},
		{(*app).pageAudioCmp, "压缩音频", "音频压缩·主按钮", func(a *app) { a.audioCmpPath = sample.song }, false},
		{(*app).pageImageCmp, "清空", "图片压缩·引导行清空钮", withPhoto, false},
		{(*app).pageImageCmp, "100KB", "图片压缩·目标体积下拉", withPhoto, false},
		{(*app).pageImageCmp, "只降画质", "图片压缩·压缩策略单选项", withPhoto, false},
		{(*app).pageImageNew, "生成图片", "新建图片·主按钮", func(a *app) { a.imageNewSize = "800x600" }, false},
		{(*app).pageOffice, "自动", "Office·引擎下拉触发器", func(a *app) { a.officePath = sample.report }, false},
		{(*app).pagePdfRotate, "右转 90°", "PDF旋转·方向下拉触发器", func(a *app) { a.pdfRotatePath = sample.document }, false},
		{(*app).pagePdfRotate, "旋转", "PDF旋转·主按钮", func(a *app) { a.pdfRotatePath = sample.document }, false},
		{(*app).pagePdfResize, "统一尺寸", "页面尺寸·主按钮", func(a *app) { a.pdfResizePath = sample.document }, false},
		{(*app).pageHash, "浏览选择…", "哈希·引导行浏览钮", func(a *app) { a.hashInputs = []string{sample.manual} }, false},
		{(*app).pageHash, "清空", "哈希·引导行清空钮", func(a *app) { a.hashInputs = []string{sample.manual} }, false},
		{(*app).pageHash, "SHA256", "哈希·算法下拉触发器", func(a *app) { a.hashInputs = []string{sample.manual} }, false},
		{(*app).pageHash, "计算哈希", "哈希·主按钮", func(a *app) { a.hashInputs = []string{sample.manual} }, false},
		{(*app).pageHash, "复制", "哈希·结果复制钮", sample.hashDone(), false},
		{(*app).pageRandom, "UUID", "随机·模式分段", func(a *app) {}, false},
		{(*app).pageRandom, "16", "随机·长度下拉触发器", func(a *app) {}, false},
		{(*app).pageRandom, newApp().pwCharsetSummary(), "随机·字符集触发器", func(a *app) {}, false},
		{(*app).pageRandom, "生成密码", "随机·主按钮", func(a *app) {}, false},
		{(*app).pageRandom, "复制", "随机·结果复制钮", func(a *app) { a.randomOut = "Kq9#mP2x" }, false},
		{(*app).pageBurn, sample.manual, "刻录·清单行（拖拽把手）", sample.withDevice(), true},
		{(*app).pageBurn, "浏览选择…", "刻录·引导行浏览钮", sample.withDevice(), false},
		{(*app).pageBurn, "选择文件夹…", "刻录·引导行文件夹钮", sample.withDevice(), false},
		{(*app).pageBurn, "清空", "刻录·引导行清空钮", sample.withDevice(), false},
		{(*app).pageBurn, "刷新", "刻录·刷新钮", sample.withDevice(), false},
		{(*app).pageBurn, "可追加", "刻录·写入模式下拉", sample.withDevice(), false},
		{(*app).pageBurn, "清除", "刻录·清除钮", sample.withDevice(), false},
		{(*app).pageBurn, "刻录", "刻录·主按钮", sample.withDevice(), false},
		{(*app).pageArchive, "浏览选择…", "归档浏览·引导行浏览钮", func(a *app) {}, false},
		{(*app).pageArchive, "校验归档", "归档浏览·校验钮", sample.archiveLoaded(), false},
		{(*app).pageTasks, "取消", "处理台·取消钮", func(a *app) {
			a.tasks = []*Task{{Title: "压缩", State: TaskRunning, Percent: 45, Detail: "45%"}}
		}, false},
		{(*app).pageTasks, revealLabel(), "处理台·定位钮", func(a *app) {
			a.tasks = []*Task{{Title: "解压", State: TaskDone, Percent: 100, Result: "完成", Product: sample.manual}}
		}, false},
		{(*app).pageTasks, "清掉已结束的 1 项", "处理台·清掉已结束", func(a *app) {
			a.tasks = []*Task{{Title: "解压", State: TaskDone, Percent: 100, Result: "完成"}}
		}, false},
		{(*app).pageSettings, "深色", "设置·外观分段", func(a *app) {}, false},
		{(*app).pageSettings, "复制", "设置·引擎路径复制钮", func(a *app) {}, false},
		{(*app).pageSettings, "github.com/guohub8080/zpack", "设置·开源仓库链接", func(a *app) {}, false},
	}
}

// TestClickableFeedback 全部受检元素：悬停有变化、按下有变化、手型光标。
func TestClickableFeedback(t *testing.T) {
	registerMiSans()
	sample := newFeedbackSamples(t)
	// 引擎路径行只在找到引擎时出复制钮：拿样例里的一个可执行文件顶替引擎路径
	t.Setenv("ZPACK_BIN", sample.report)
	for _, target := range feedbackTargets(sample) {
		target := target
		t.Run(target.note, func(t *testing.T) {
			application := newApp()
			target.fill(application)
			tester := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(brandTheme(c))
				target.page(application, c)
			}, FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
			feedbackSettle(tester)
			rect, found := tester.Find(target.label)
			if !found {
				t.Fatalf("找不到元素 %q", target.label)
			}
			centerX, centerY := rect.X+rect.W/2, rect.Y+rect.H/2
			if centerY > FEEDBACK_WINDOW_HEIGHT {
				t.Fatalf("目标在屏外（y=%.0f）——把窗口拉高或给它更少的内容", centerY)
			}
			rest := feedbackFrame(tester)
			tester.Move(centerX, centerY)
			feedbackSettle(tester)
			hovered := feedbackFrame(tester)
			pointer := tester.Cursor() == ui.CursorPointer
			tester.Press(centerX, centerY)
			feedbackSettle(tester)
			pressed := feedbackFrame(tester)

			hoverPixels := feedbackDiff(rest, hovered)
			if hoverPixels < FEEDBACK_MIN_PIXELS {
				t.Errorf("悬停无反馈（改动 %d px）——凡可点击必带 hover", hoverPixels)
			}
			if target.rowOnly {
				return
			}
			if pressedPixels := feedbackDiff(hovered, pressed); pressedPixels < FEEDBACK_MIN_PIXELS {
				t.Errorf("按下无反馈（改动 %d px）——悬停与按下给同一底色等于没有按下态", pressedPixels)
			}
			if !pointer {
				t.Errorf("悬停时光标不是手型——可点元素须 Cursor(ui.CursorPointer)")
			}
		})
	}
}

// TestSidebarItemFeedback 侧栏导航项：未选中项与选中项都要有悬停与按下反馈。
func TestSidebarItemFeedback(t *testing.T) {
	registerMiSans()
	for _, item := range []struct {
		page, label string
		chosen      bool
	}{{pageExtract, "压缩台", false}, {pageExtract, "解压台", true}} {
		application := newApp()
		application.page = item.page
		tester := ui.NewTester(application.view, 675, 1100)
		feedbackSettle(tester)
		rect, found := tester.Find(item.label)
		if !found {
			t.Fatalf("侧栏找不到 %q", item.label)
		}
		centerX, centerY := rect.X+rect.W/2, rect.Y+rect.H/2
		rest := feedbackFrame(tester)
		tester.Move(centerX, centerY)
		feedbackSettle(tester)
		hovered := feedbackFrame(tester)
		pointer := tester.Cursor() == ui.CursorPointer
		tester.Press(centerX, centerY)
		feedbackSettle(tester)
		pressed := feedbackFrame(tester)
		if diff := feedbackDiff(rest, hovered); diff < FEEDBACK_MIN_PIXELS {
			t.Errorf("%s（选中=%v）悬停无反馈（%d px）", item.label, item.chosen, diff)
		}
		if diff := feedbackDiff(hovered, pressed); diff < FEEDBACK_MIN_PIXELS {
			t.Errorf("%s（选中=%v）按下无反馈（%d px）", item.label, item.chosen, diff)
		}
		if !pointer {
			t.Errorf("%s 光标不是手型", item.label)
		}
	}
}

// TestPathsShowInFull 文件名 / 路径一律完整显示（2026-11-23 用户定案「所有文件名的显示都是
// 完整的，不需要截断或者中间变色」）：放不下就换行，不省略。量法=拿一条远宽于行的路径：
// 清单行 / 卡片 / 字段里的路径文字必须折成多行（若还是一行高，就是被省略了）；
// 字段里另查尾部的「选择」钮没被挤出框（Shrink(0)）——字段量在缺省窗口宽度：最小窗口下
// 两列布局的控件列只剩 172px，一段没有断点的长英文目录名会把它撑破（布局原有的边界，不在此断言）。
func TestPathsShowInFull(t *testing.T) {
	registerMiSans()
	sample := newFeedbackSamples(t)
	t.Setenv("ZPACK_BIN", sample.report)
	long := filepath.Join(sample.dir, "名字特别长的一级目录", "还有一级名字更长的子目录", "最终文件名也很长很长很长很长很长.xlsx")
	type target struct {
		note string
		page func(a *app, c *ui.Context)
		fill func(a *app)
		text string
	}
	narrow := FEEDBACK_MIN_WINDOW_WIDTH - sidebarWidth // 最小窗口的内容区宽
	targets := []struct {
		target
		width    int  // 渲染宽度
		hasField bool // 字段：另查「选择」钮在框内
	}{
		{target{"压缩台清单行", (*app).pageCompress, func(a *app) { a.inputs = []string{long}; a.syncName() }, long}, narrow, false},
		{target{"PDF 合并清单行", (*app).pagePdfMerge, func(a *app) { a.pdfMergeInputs = []string{long} }, long}, narrow, false},
		{target{"解压台归档卡", (*app).pageExtract, func(a *app) { a.extractPath = long }, long}, narrow, false},
		{target{"归档浏览卡", (*app).pageArchive, func(a *app) { a.archivePath = long }, long}, narrow, false},
		{target{"视频转换·文件字段", (*app).pageMedia, func(a *app) { a.mediaPath = long }, long}, FEEDBACK_WINDOW_WIDTH, true},
		{target{"密钥·保存位置", (*app).pageKeys, func(a *app) { a.keyDir = filepath.Dir(long) }, filepath.Dir(long)}, FEEDBACK_WINDOW_WIDTH, true},
	}
	for _, item := range targets {
		application := newApp()
		item.fill(application)
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(brandTheme(c))
			item.page(application, c)
		}, item.width, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		rect, found := tester.Find(item.text)
		if !found {
			t.Errorf("%s：找不到路径文字", item.note)
			continue
		}
		if rect.H <= FEEDBACK_ONE_LINE_HEIGHT {
			t.Errorf("%s：长路径只占 %.0f px 高——被省略成一行了，应完整显示、放不下就换行", item.note, rect.H)
		}
		if !item.hasField {
			continue
		}
		pickRect, found := tester.Find("选择")
		if !found {
			t.Errorf("%s：找不到选择钮", item.note)
			continue
		}
		if pickRect.W < FEEDBACK_PICK_LABEL_WIDTH {
			t.Errorf("%s：选择钮被挤窄（文字宽 %.0f）——Shrink(0) 没生效", item.note, pickRect.W)
		}
		if pickRect.X+pickRect.W > float32(item.width-FEEDBACK_PAGE_MARGIN) {
			t.Errorf("%s：选择钮伸出页面右缘（右边界 %.0f）", item.note, pickRect.X+pickRect.W)
		}
	}
}

// TestLeadSentenceStaysReadableAtMinWidth 最小窗口（内容宽 268）下各页引导句不被挤成竖条：
// 宽度不低于可读下限、不超过三行。刻录页引导行有三个按钮，曾是 26px 宽 9 行。
func TestLeadSentenceStaysReadableAtMinWidth(t *testing.T) {
	registerMiSans()
	sample := newFeedbackSamples(t)
	targets := []struct {
		note string
		page func(a *app, c *ui.Context)
		fill func(a *app)
	}{
		{"压缩台", (*app).pageCompress, func(a *app) { a.inputs = []string{sample.report}; a.syncName() }},
		{"解压台", (*app).pageExtract, func(a *app) { a.extractPath = sample.manual }},
		{"PDF 合并", (*app).pagePdfMerge, func(a *app) { a.pdfMergeInputs = []string{sample.manual, sample.document} }},
		{"哈希", (*app).pageHash, func(a *app) { a.hashInputs = []string{sample.manual} }},
		{"图片压缩", (*app).pageImageCmp, func(a *app) { a.imageCmpInputs = []string{sample.photo} }},
		{"刻录", (*app).pageBurn, func(a *app) { a.burnInputs = []string{sample.manual} }},
		{"归档浏览", (*app).pageArchive, func(a *app) { a.archivePath = sample.manual }},
		{"视频转换", (*app).pageMedia, func(a *app) { a.mediaPath = sample.video }},
	}
	for _, target := range targets {
		application := newApp()
		target.fill(application)
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(brandTheme(c))
			target.page(application, c)
		}, FEEDBACK_MIN_WINDOW_WIDTH-sidebarWidth, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		texts := tester.Texts()
		if len(texts) == 0 {
			t.Fatalf("%s：页面没有文字", target.note)
		}
		rect, found := tester.Find(texts[0]) // 首个文字即引导句
		if !found {
			t.Fatalf("%s：找不到引导句 %q", target.note, texts[0])
		}
		if rect.W < FEEDBACK_LEAD_MIN_WIDTH {
			t.Errorf("%s：引导句只有 %.0f px 宽——被按钮挤成竖条（应放不下才换行）", target.note, rect.W)
		}
		if rect.H > FEEDBACK_LEAD_MAX_HEIGHT {
			t.Errorf("%s：引导句高 %.0f px——超过三行", target.note, rect.H)
		}
	}
}

// TestPickButtonInset 字段尾部的「选择」钮离字段右缘有内缩：钮的悬停色块四边离字段描边
// 一样远，不贴着右边框（压缩台保存位置此前没有内缩，2026-11-23 用户反馈）。
// 量法：按钮文字右缘到字段右缘（= 页面内容列右缘 − 描边）的距离 ≥ 钮内边距 + 内缩。
func TestPickButtonInset(t *testing.T) {
	registerMiSans()
	sample := newFeedbackSamples(t)
	targets := []struct {
		note string
		page func(a *app, c *ui.Context)
		fill func(a *app)
	}{
		{"压缩台·保存位置", (*app).pageCompress, func(a *app) { a.inputs = []string{sample.report}; a.syncName() }},
		{"解压台·解压位置", (*app).pageExtract, func(a *app) { a.extractPath = sample.manual }},
		{"密钥·保存位置", (*app).pageKeys, func(a *app) {}},
		{"视频转换·输入文件", (*app).pageMedia, func(a *app) { a.mediaPath = sample.video }},
		{"PDF 合并·输出位置", (*app).pagePdfMerge, func(a *app) { a.pdfMergeInputs = []string{sample.manual, sample.document} }},
	}
	for _, target := range targets {
		application := newApp()
		target.fill(application)
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(brandTheme(c))
			target.page(application, c)
		}, FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		rect, found := tester.Find("选择") // 每页第一个「选择」即字段尾部小钮
		if !found {
			t.Errorf("%s：找不到选择钮", target.note)
			continue
		}
		fieldRightEdge := float32(FEEDBACK_WINDOW_WIDTH - contentPadX - 1)
		gap := fieldRightEdge - (rect.X + rect.W)
		// 门槛写死在这里、不引用实现常量：内缩被改小时要红（钮内边距 8 + 至少 3px 内缩）
		if gap < PICK_MIN_GAP {
			t.Errorf("%s：选择钮离字段右缘只有 %.1f px（应 ≥ %d = 钮内边距 + 内缩）——悬停色块会贴边", target.note, gap, PICK_MIN_GAP)
		}
	}
}

// newFeedbackSamples 在临时目录造样例文件：稀疏文件给出真实体积；照片与 PDF 造真能解码的。
func newFeedbackSamples(t *testing.T) feedbackSamples {
	t.Helper()
	dir := t.TempDir()
	sparsify := func(name string, size int64) string {
		path := filepath.Join(dir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(size); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	sample := feedbackSamples{
		dir:    dir,
		report: sparsify("季度报表.xlsx", 2_400_000),
		design: sparsify("设计稿.psd", 38_000_000),
		manual: sparsify("手册.pdf", 4_800_000),
		video:  sparsify("video-export.mp4", 620_000_000),
		song:   sparsify("歌曲.mp3", 8_100_000),
		key:    filepath.Join(dir, "k.pem"),
	}
	sample.photo = sparsify("照片.jpg", 3_200_000)
	sample.document = sparsify("文档.pdf", 1_100_000)
	// 引擎替身：只需存在且可执行（测试里不真正运行它）
	if err := os.Chmod(sample.report, 0o755); err != nil {
		t.Fatal(err)
	}
	return sample
}

func feedbackSettle(tester *ui.Tester) {
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
}

// feedbackFrame 拷下当前帧（Tester.Image 可能复用缓冲，比较前先拷贝）。
func feedbackFrame(tester *ui.Tester) *image.RGBA {
	frame := tester.Image()
	clone := image.NewRGBA(frame.Bounds())
	copy(clone.Pix, frame.Pix)
	return clone
}

// feedbackDiff 数两帧之间「明显不同」的像素。
func feedbackDiff(before, after *image.RGBA) int {
	changed := 0
	bounds := before.Bounds()
	for posY := bounds.Min.Y; posY < bounds.Max.Y; posY++ {
		for posX := bounds.Min.X; posX < bounds.Max.X; posX++ {
			first, second := before.RGBAAt(posX, posY), after.RGBAAt(posX, posY)
			if channelDiff(first.R, second.R) > FEEDBACK_PIXEL_DELTA ||
				channelDiff(first.G, second.G) > FEEDBACK_PIXEL_DELTA ||
				channelDiff(first.B, second.B) > FEEDBACK_PIXEL_DELTA {
				changed++
			}
		}
	}
	return changed
}

func channelDiff(first, second uint8) int {
	if first > second {
		return int(first - second)
	}
	return int(second - first)
}
