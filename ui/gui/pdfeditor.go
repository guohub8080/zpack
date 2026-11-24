// PDF 页面编辑器（specs/gui.md 2.12）——画布页范式：缩略图网格 + 多选 + 拖拽重排，
// 功能面对齐 PDF Arranger（GPL-3.0 参考实现，换成 zpack 的 Golang/cgo 与契约驱动）。
//
// 架构纪律（薄壳铁律）：编辑态是纯前端内存模型；所有落盘都编译成 CLI 调用
// （collect 保序收集 + rotate 分组叠加）；缩略图渲染是**展示辅助**。
//
// 渲染服务（2026-11-24 二版重写，用户定案两轮反馈）：
// ① **真渲染旋转页**——旋转不是显示层假转，编辑旋转角应用进位图（rotateRGBA）后重新
//
//	渲染贴图，所见即导出所得；
//
// ② **导入即进度条**——ui.Progress 显示首渲进度（done/total），不卡住；
// ③ **非阻塞**——双通道请求队列（urgent=旋转重渲插队 / normal=首渲按序），单渲染协程
//
//	串行处理（MuPDF 文档对象非并发安全），网格立即可浏览（未渲页占位）；
//
// ④ 首屏优先（前 12 页先入 normal 队）。
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	fitz "github.com/gen2brain/go-fitz"
)

// EDITOR_THUMB_DPI 缩略图渲染分辨率（屏幕预览档；与 CLI render 的交付 DPI 无关）
const EDITOR_THUMB_DPI = 72

// EDITOR_FIRST_BATCH 打开文档时优先渲染的头批页数（定案「跳页不等按序渲染」的第一块）
const EDITOR_FIRST_BATCH = 12

// editorZoomTier 缩放档（页面对齐 pdfarranger 的放大/缩小）
type editorZoomTier struct {
	label     string  // 工具栏显示（百分比）
	gridMinW  float32 // GridView 单元最小宽
	gridCellH float32 // 单元高
	thumbW    float32 // 缩略图显示宽
}

// EDITOR_ZOOM_TIERS 缩放档表（缺省档 1 = 100%）
var EDITOR_ZOOM_TIERS = []editorZoomTier{
	{"50%", 72, 100, 60},
	{"100%", 140, 190, 120},
	{"150%", 210, 280, 186},
	{"200%", 280, 370, 252},
}

// editorPage 编辑态页：源页码（1 基）+ 旋转角（顺时针 0/90/180/270——导出叠加到产物）
type editorPage struct {
	srcPage  int
	rotation int
}

// thumbKey 缩略图缓存键：页 × 旋转角（旋转变了就是新图）
type thumbKey struct {
	srcPage  int
	rotation int
}

// thumbRequest 渲染请求
type thumbRequest struct {
	srcPage  int
	rotation int
}

// pdfEditorState 编辑器全部状态
type pdfEditorState struct {
	docPath   string                      // 打开的 PDF（第一版：单文档）
	pageCount int                         // 源文档总页数
	pages     []editorPage                // 编辑态页序
	rotation  map[int]int                 // 源页码 → 旋转角
	thumbs    map[thumbKey]ui.ImageSource // 缩略图缓存（键=页×角）
	rendering map[thumbKey]bool
	grid      ui.GridState      // 画布网格（Selected/Selection/Reorder 框架开箱）
	selection ui.Selection[int] // 多选（键=编辑序下标）
	selected  int               // 最后一次点击（-1 无）
	zoomIdx   int               // 缩放档索引（缺省 1=100%）
	doneCount int               // 首渲已完成页数（进度条）
	total     int               // 首渲总页数
	quit      chan struct{}     // 关闭渲染服务
	urgent    chan thumbRequest // 旋转重渲（插队）
	normal    chan thumbRequest // 首渲（按序）
}

// ensureEditorState 懒初始化（含 GridState 指针接线——Selected/Selection 不接指针
// 点击恒不生效，第一版漏接被用户指出）
func (a *app) ensureEditorState() *pdfEditorState {
	if a.pdfEd == nil {
		a.pdfEd = &pdfEditorState{
			thumbs:    map[thumbKey]ui.ImageSource{},
			rendering: map[thumbKey]bool{},
			selected:  -1,
			zoomIdx:   1,
		}
		a.pdfEd.grid.Selected = &a.pdfEd.selected
		a.pdfEd.grid.Selection = &a.pdfEd.selection
	}
	return a.pdfEd
}

// toast 入 toast 队列（sniff.go 的 pendingToasts 通道——帧外 goroutine/回调入队，帧首 drain）
func (a *app) toast(message string) {
	a.toastMu.Lock()
	a.pendingToasts = append(a.pendingToasts, message)
	a.toastMu.Unlock()
}

// editorBrowse 打开文件对话框（goroutine 发起——嵌套事件泵教训见 main.go browseFiles）
func (a *app) editorBrowse() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "选择要编辑的 PDF",
			Filters: []mygo.FileFilter{{Name: "PDF 文档", Extensions: []string{"pdf"}}},
		})
		mygo.RunOnMain(func() {
			if err != nil || len(paths) == 0 {
				return
			}
			a.editorOpen(paths[0])
		})
	}()
}

// editorOpen 打开文档：CLI 读页数（pdf info）→ 建编辑态 → 启动渲染服务（首屏优先队列）
func (a *app) editorOpen(docPath string) {
	absolute, absErr := filepath.Abs(docPath)
	if absErr != nil {
		absolute = docPath
	}
	a.service.RunToolEnvelope([]string{"pdf", "info", absolute}, func(raw []byte, err string) {
		if err != "" {
			a.toast("读 PDF 失败：" + err)
			return
		}
		var envelope struct {
			Data struct {
				PageCount int `json:"page_count"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Data.PageCount <= 0 {
			a.toast("读 PDF 页数失败")
			return
		}
		ed := a.ensureEditorState()
		if ed.quit != nil {
			close(ed.quit) // 换文档：停旧渲染服务
		}
		ed.docPath = absolute
		ed.pageCount = envelope.Data.PageCount
		ed.pages = make([]editorPage, envelope.Data.PageCount)
		for idx := range ed.pages {
			ed.pages[idx] = editorPage{srcPage: idx + 1}
		}
		ed.rotation = map[int]int{}
		ed.thumbs = map[thumbKey]ui.ImageSource{}
		ed.rendering = map[thumbKey]bool{}
		ed.selected = -1
		ed.selection = ui.Selection[int]{}
		ed.doneCount = 0
		ed.total = envelope.Data.PageCount
		ed.quit = make(chan struct{})
		ed.urgent = make(chan thumbRequest, envelope.Data.PageCount)
		ed.normal = make(chan thumbRequest, envelope.Data.PageCount)
		// 首屏优先：前 EDITOR_FIRST_BATCH 页先入队，其余按序（渐进出现）
		for page := 1; page <= envelope.Data.PageCount; page++ {
			request := thumbRequest{srcPage: page}
			if page <= EDITOR_FIRST_BATCH {
				ed.normal <- request
			} else {
				ed.normal <- request // 全部走 normal（urgent 留给旋转重渲插队）
			}
		}
		go ed.renderService(a)
	})
}

// renderService 常驻渲染协程：urgent（旋转重渲）优先于 normal（首渲）——select 双层实现
// 插队语义；单协程串行处理（MuPDF 文档对象非并发安全）。
func (ed *pdfEditorState) renderService(a *app) {
	document, openErr := fitz.New(ed.docPath)
	if openErr != nil {
		a.toast("PDF 打开失败：" + openErr.Error())
		return
	}
	defer document.Close()

	for {
		var request thumbRequest
		select {
		case <-ed.quit:
			return
		default:
		}
		select {
		case <-ed.quit:
			return
		case request = <-ed.urgent: // 旋转重渲插队
		default:
			select {
			case request = <-ed.urgent:
			case request = <-ed.normal:
			case <-ed.quit:
				return
			}
		}
		ed.renderOne(a, document, request)
	}
}

// renderOne 单页渲染：缓存查 → ImageDPI → 旋转位图（编辑角）→ 贴缓存 + 进度 + 触发一帧
func (ed *pdfEditorState) renderOne(a *app, document *fitz.Document, request thumbRequest) {
	key := thumbKey{srcPage: request.srcPage, rotation: request.rotation}
	if _, done := ed.thumbs[key]; done {
		return
	}
	ed.rendering[key] = true
	defer delete(ed.rendering, key)

	rgba, renderErr := document.ImageDPI(request.srcPage-1, EDITOR_THUMB_DPI)
	if renderErr != nil {
		return // 单页失败不阻塞后续（失败页保持占位）
	}
	rotated := rotateRGBA(rgba, request.rotation)
	bitmap := ui.NewBitmap(rotated)
	mygo.RunOnMain(func() {
		ed.thumbs[key] = bitmap
		// 首渲请求（rotation 与打开态一致）计入进度
		if request.rotation == 0 && ed.doneCount < ed.total {
			ed.doneCount++
		}
		a.win.Update(func() {}) // 缩略图到达即贴 + 进度条推进
	})
}

// rotateRGBA 位图顺时针旋转（degrees ∈ {0,90,180,270}）——Pix 直排（非 At/Set 逐像素）。
// 编辑旋转角应用进位图：所见即导出所得（2026-11-24 用户定案：要渲染旋转完的页面）。
func rotateRGBA(src *image.RGBA, degrees int) *image.RGBA {
	width, height := src.Rect.Dx(), src.Rect.Dy()
	switch ((degrees % 360) + 360) % 360 {
	case 90: // 顺时针 90：源 (x,y) → 目标 (h-1-y, x)
		dst := image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				sourceIndex := src.PixOffset(x, y)
				destIndex := dst.PixOffset(height-1-y, x)
				copy(dst.Pix[destIndex:destIndex+4], src.Pix[sourceIndex:sourceIndex+4])
			}
		}
		return dst
	case 180:
		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				sourceIndex := src.PixOffset(x, y)
				destIndex := dst.PixOffset(width-1-x, height-1-y)
				copy(dst.Pix[destIndex:destIndex+4], src.Pix[sourceIndex:sourceIndex+4])
			}
		}
		return dst
	case 270: // 顺时针 270（=逆时针 90）：源 (x,y) → 目标 (y, w-1-x)
		dst := image.NewRGBA(image.Rect(0, 0, height, width))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				sourceIndex := src.PixOffset(x, y)
				destIndex := dst.PixOffset(y, width-1-x)
				copy(dst.Pix[destIndex:destIndex+4], src.Pix[sourceIndex:sourceIndex+4])
			}
		}
		return dst
	}
	return src
}

// editorSelectedIndices 当前选中的编辑序下标（Selection 优先，落回单选 Selected）
func (ed *pdfEditorState) editorSelectedIndices() []int {
	indices := []int{}
	for idx := range ed.pages {
		if ed.selection.Has(idx) {
			indices = append(indices, idx)
		}
	}
	if len(indices) == 0 && ed.selected >= 0 && ed.selected < len(ed.pages) {
		indices = append(indices, ed.selected)
	}
	return indices
}

// editorRotate 选中页旋转：编辑态改角 + 投递**真渲染重渲请求**（urgent 插队——
// 用户点了旋转就等它，渲染完贴真旋转图；编辑期间显示「渲染中」占位）+ 显式触发一帧
func (a *app) editorRotate(indices []int, clockwise bool) {
	ed := a.ensureEditorState()
	for _, index := range indices {
		if index < 0 || index >= len(ed.pages) {
			continue
		}
		if clockwise {
			ed.pages[index].rotation = (ed.pages[index].rotation + 90) % 360
		} else {
			ed.pages[index].rotation = (ed.pages[index].rotation + 270) % 360
		}
		ed.urgent <- thumbRequest{srcPage: ed.pages[index].srcPage, rotation: ed.pages[index].rotation}
	}
	a.win.Update(func() {}) // 立即反馈：格子进「渲染中」态
}

// editorDelete 删除选中页
func (a *app) editorDelete(indices []int) {
	ed := a.ensureEditorState()
	remove := map[int]bool{}
	for _, index := range indices {
		remove[index] = true
	}
	kept := make([]editorPage, 0, len(ed.pages))
	for index, page := range ed.pages {
		if !remove[index] {
			kept = append(kept, page)
		}
	}
	ed.pages = kept
	ed.selection = ui.Selection[int]{}
	ed.selected = -1
	a.win.Update(func() {})
}

// editorExport 编译导出（薄壳铁律：落盘必经契约）：
// ① `pdf collect --pages <编辑序>` 保序收集（重排/删除/复制的页序语义）
// ② 非零旋转按角分组 → `pdf rotate-right/left/180 --pages <产物位置集>` 叠加 /Rotate
func (a *app) editorExport() {
	ed := a.ensureEditorState()
	if ed.docPath == "" || len(ed.pages) == 0 {
		a.toast("先打开一个 PDF")
		return
	}
	sequence := make([]int, len(ed.pages))
	rotationAt := map[int]int{} // 产物位置（1 基）→ 旋转角
	for idx, page := range ed.pages {
		sequence[idx] = page.srcPage
		if page.rotation != 0 {
			rotationAt[idx+1] = page.rotation
		}
	}
	outputPath := strings.TrimSuffix(ed.docPath, filepath.Ext(ed.docPath)) + "-edited.pdf"
	absoluteOutput, _ := filepath.Abs(outputPath)

	args := []string{"pdf", "collect", ed.docPath, "--pages", joinInts(sequence), "--output", absoluteOutput}
	a.service.RunTool(args, func(result ToolResult) {
		if result.Err != "" {
			a.toast("导出失败：" + result.Err)
			return
		}
		a.runExportRotations(absoluteOutput, rotationAt, []int{90, 180, 270}, func(finalErr string) {
			mygo.RunOnMain(func() {
				if finalErr == "" {
					for idx := range ed.pages { // 产物已含旋转——编辑态变化标记清零（提醒色归位）
						ed.pages[idx].rotation = 0
					}
					a.win.Update(func() {})
					a.toast("已导出 " + filepath.Base(absoluteOutput))
				} else {
					a.toast("导出旋转失败：" + finalErr)
				}
			})
		})
	})
}

// runExportRotations 逐组叠加旋转（递归串行；组内页码 = 产物上的 1 基位置）
func (a *app) runExportRotations(output string, rotationAt map[int]int, order []int, done func(string)) {
	if len(order) == 0 {
		done("")
		return
	}
	angle, rest := order[0], order[1:]
	positions := []string{}
	for position, rotation := range rotationAt {
		if rotation == angle {
			positions = append(positions, fmt.Sprint(position))
		}
	}
	if len(positions) == 0 {
		a.runExportRotations(output, rotationAt, rest, done)
		return
	}
	verb := map[int]string{90: "rotate-right", 180: "rotate-180", 270: "rotate-left"}[angle]
	args := []string{"pdf", verb, output, "--pages", strings.Join(positions, ","), "--output", output}
	a.service.RunTool(args, func(result ToolResult) {
		if result.Err != "" {
			done(result.Err)
			return
		}
		a.runExportRotations(output, rotationAt, rest, done)
	})
}

// joinInts []int → "3,1,2"
func joinInts(values []int) string {
	parts := make([]string, len(values))
	for idx, value := range values {
		parts[idx] = fmt.Sprint(value)
	}
	return strings.Join(parts, ",")
}

// editorToolButton 画布工具行的统一小按钮
func editorToolButton(c *ui.Context, t *ui.Theme, label string, onClick func()) {
	button := ui.ButtonBase(c).Height(30).MinWidth(30).Padding(0, 10).Radius(8).
		Border(1, t.Border).Cursor(ui.CursorPointer).Shrink(0)
	pressFeedback(button, t)
	button.Children(func() {
		ui.Text(c, label).FontSize(13)
	})
	if button.Clicked() {
		onClick()
	}
}

// RunToolEnvelope 执行短命令并回传完整信封 raw（页数等 data 字段的消费通道——
// RunTool 只出 Output/Extra/Err，不带 data 其他字段）。失败信封也在 stdout（契约）。
func (service *Service) RunToolEnvelope(args []string, callback func(raw []byte, err string)) {
	if service.zpackPath() == "" {
		callback(nil, "未找到 zpack 引擎")
		return
	}
	go func() {
		cmd := exec.Command(service.zpackPath(), args...)
		hideConsole(cmd)
		raw, runErr := cmd.Output()
		if runErr != nil {
			if exitErr, isExit := runErr.(*exec.ExitError); isExit {
				callback(raw, fmt.Sprintf("exit %d", exitErr.ExitCode()))
				return
			}
			callback(nil, runErr.Error())
			return
		}
		callback(raw, "")
	}()
}

// pagePdfEditor PDF 页面编辑器页（画布页不用 pageColumn 的 640px 定宽——铺满窗口；
// 其余排版骨架与转换族同构：lead 行 + 工具行 + 卡片容器 + 渲染进度条）
func (a *app) pagePdfEditor(c *ui.Context) {
	ed := a.ensureEditorState()
	t := c.Theme()
	tier := EDITOR_ZOOM_TIERS[ed.zoomIdx%len(EDITOR_ZOOM_TIERS)]
	column := ui.Column(c).Padding(contentPadTop, contentPadX, contentPadBtm, contentPadX).Grow(1)
	column.Children(func() {
		a.convertLeadRow(c, "PDF", ed.docPath, a.editorBrowse)

		// 工具行：旋转/删除/缩放/导出（经典排版：与转换族同一行高与间距；空态由 heroPane 接管）
		toolRow := ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(0, 0, 8, 0)
		toolRow.Children(func() {
			editorToolButton(c, t, "旋转左 ⟲", func() { a.editorRotate(a.pdfEd.editorSelectedIndices(), false) })
			editorToolButton(c, t, "旋转右 ⟳", func() { a.editorRotate(a.pdfEd.editorSelectedIndices(), true) })
			editorToolButton(c, t, "删除所选", func() { a.editorDelete(a.pdfEd.editorSelectedIndices()) })
			// 缩放档：− / 百分比 / +（pdfarranger 同款 zoom）
			editorToolButton(c, t, "−", func() {
				if ed.zoomIdx > 0 {
					ed.zoomIdx--
				}
			})
			ui.Text(c, tier.label).FontSize(12).TextColor(t.TextMuted).Shrink(0)
			editorToolButton(c, t, "+", func() {
				if ed.zoomIdx < len(EDITOR_ZOOM_TIERS)-1 {
					ed.zoomIdx++
				}
			})
			ui.Spacer(c)
			exportButton := primaryCTA(c, t, "导出 PDF", len(ed.pages) > 0)
			if exportButton.Clicked() && len(ed.pages) > 0 {
				a.editorExport()
			}
		})

		// 状态行：文档名 + 页数 + 选择数
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, filepath.Base(ed.docPath)).FontSize(13)
			ui.Text(c, fmt.Sprintf("%d 页 · 选中 %d · 缩放 %s", len(ed.pages), len(ed.editorSelectedIndices()), tier.label)).
				FontSize(12).TextColor(t.TextMuted)
		})

		// 渲染进度条（用户定案「导入 PDF 就有反馈一个进度条而不是卡住」）：
		// 首渲未完成时常驻（done/total 渐进），完成即隐藏
		if ed.doneCount < ed.total {
			progressRow := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 4, 0)
			progressRow.Children(func() {
				ui.Progress(c, float64(ed.doneCount)/float64(ed.total)).Grow(1).Height(6)
				ui.Text(c, fmt.Sprintf("渲染缩略图 %d/%d", ed.doneCount, ed.total)).
					FontSize(12).TextColor(t.TextMuted).Shrink(0)
			})
		}

		// 画布卡片：GridView（尺寸随缩放档；Reorder/Selection/Selected 框架开箱——
		// Selected/Selection 必须接指针，否则点击不生效）
		canvasCard := ui.Column(c).Grow(1).Margin(8, 0, 0, 0).Padding(8).Radius(10).
			Background(t.Surface).Border(1, t.Border)
		canvasCard.Children(func() {
			ui.GridView(c, &ed.grid, len(ed.pages), tier.gridMinW, tier.gridCellH, func(i int) {
				page := ed.pages[i]
				key := thumbKey{srcPage: page.srcPage, rotation: page.rotation}
				thumb, hasThumb := ed.thumbs[key]
				_, isRendering := ed.rendering[key]
				edited := page.rotation != 0 // 有编辑变化：Accent 底色提醒（用户定案）
				// 固定布局（勿用 Grow——溢出即重叠，第一版教训）
				cell := ui.Column(c).Gap(4).Padding(6).AlignItems(ui.Center).
					Radius(8).Background(t.Surface).Border(1, t.Border)
				if edited {
					cell.Background(t.Accent).Border(1, t.Accent)
				}
				cell.Children(func() {
					switch {
					case hasThumb:
						ui.Image(c, thumb).Width(tier.thumbW).Height(tier.gridCellH - 34)
						if page.rotation != 0 {
							ui.Text(c, fmt.Sprintf("⟳ %d°", page.rotation)).FontSize(11).TextColor(t.AccentText)
						}
					case isRendering:
						ui.Text(c, "渲染中…").FontSize(12).TextColor(t.TextMuted)
					default:
						ui.Text(c, "排队中…").FontSize(12).TextColor(t.TextMuted)
					}
					ui.Text(c, fmt.Sprintf("第 %d 页", page.srcPage)).FontSize(12)
				})
			}).Grow(1)
		})
	})
}
