package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 字体渲染探针（开发辅助，默认跳过）：ZPACK_GUI_FONT_PROBE=1 时在无头 Tester 里渲染
// 同一组文字样本，量出墨水覆盖 / 亚像素彩边 / 核心色 / 对比度，并落参考图与数据日志。
// 用途=跨平台对照：macOS 实测数与 Windows 待填表见 FONT-PROBE.md。命令：
//
//	ZPACK_GUI_FONT_PROBE=1 go test -run TestFontProbe -count=1 -v .
//
// 产物（fontprobe-<系统>-<架构>-1x/2x.png 与 fontprobe-<系统>-<架构>.txt）落
// ZPACK_GUI_FONT_PROBE_OUT 指定的目录，缺省 build/fontprobe（仓库已 gitignore）。

const (
	// probeEnabledEnv 置 1 才跑探针。
	probeEnabledEnv = "ZPACK_GUI_FONT_PROBE"
	// probeOutDirEnv 指定产物目录。
	probeOutDirEnv = "ZPACK_GUI_FONT_PROBE_OUT"
	// probeDefaultOutDir 是缺省产物目录（相对本包）。
	probeDefaultOutDir = "build/fontprobe"
	// probeSampleText 是全部样本共用的测试串：拉丁 + 中文 + 数字两类字形。
	probeSampleText = "zpack 压缩解压 归档 Wiwy 0123"
	// probeSampleFontSize 是样本字号（DIP），与界面正文一致。
	probeSampleFontSize = 13
	// probeLabelFontSize 是参考图行说明的字号（DIP）。
	probeLabelFontSize = 11
	// probeInkThreshold 是「有墨」的亮度偏离下限（约 2%）。
	probeInkThreshold = 5
)

// 参考图版式（像素）。
const (
	probeStripEdgePad = 12  // 左右留白
	probeStripGap     = 24  // 各列间距
	probeStripZoomW   = 360 // 放大列宽度
	probeStripRowPad  = 4   // 每行墨迹四周留白
	probeStripRowGap  = 6   // 行间分隔带高度
)

// probeRowSeparator 是参考图的行间分隔带颜色。
var probeRowSeparator = color.RGBA{0x80, 0x80, 0x80, 0xff}

// probeStyle 是一个样本的渲染设定。
type probeStyle struct {
	group   string  // 所属样本组（字重 / 颜色）
	label   string  // 样本名（日志与参考图行说明共用）
	family  string  // 字体族；空=主题默认字体
	weight  int     // 字重；0=不指定
	opacity float32 // 元素不透明度；1=不指定
	bg      ui.Color
	text    ui.Color
}

// probeMetrics 是一个样本的量测结果。
type probeMetrics struct {
	ink        float64         // 墨水覆盖：有墨像素偏离底色的程度之和，已归一到 scale=1
	fringeMax  int             // 亚像素彩边：边缘像素通道差超出文字/底色自身通道差的最大值
	fringeMean float64         // 同上的边缘均值
	extreme    [3]uint8        // 核心色：笔画核心像素的众数（细笔画无满覆盖像素时取最深）
	contrast   float64         // 核心色对底色的 WCAG 对比度
	bbox       image.Rectangle // 墨迹包围盒（设备像素）
}

// probeInk 是一个有墨像素：颜色与其偏离底色的亮度。
type probeInk struct {
	rgb       [3]uint8
	deviation int
}

// probeRow 是参考图的一行：行说明与样本墨迹裁片，连同其底色。
type probeRow struct {
	bg    ui.Color
	label *image.RGBA
	crop  *image.RGBA
}

// TestFontProbe 渲染全部样本并输出量测日志与参考图。
func TestFontProbe(t *testing.T) {
	if os.Getenv(probeEnabledEnv) != "1" {
		t.Skip("字体探针默认跳过：置 " + probeEnabledEnv + "=1 启用（对照表见 FONT-PROBE.md）")
	}
	registerMiSans()
	outDir := os.Getenv(probeOutDirEnv)
	if outDir == "" {
		outDir = probeDefaultOutDir
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("探针产物目录创建失败: %v", err)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	var logLines []string
	// scale=2 模拟 200% DPI（Windows 实机条件）；1x 多放大一倍，让两张图里每个 DIP 放得一样大。
	for _, setup := range []struct {
		scale float32
		zoom  int
	}{{1, 4}, {2, 2}} {
		var rows []probeRow
		for _, style := range append(probeWeightStyles(), probeColorStyles()...) {
			metrics, frame := probeRender(t, setup.scale, style)
			line := fmt.Sprintf("FONT-PROBE|scale=%d|%s|%s|bg=%s|text=%s|ink=%.2f|fringeMax=%d|fringeMean=%.1f|extreme=#%02x%02x%02x|contrast=%.2f|bbox=%dx%d",
				int(setup.scale), style.group, style.label, probeHex(style.bg), probeHex(style.text),
				metrics.ink, metrics.fringeMax, metrics.fringeMean,
				metrics.extreme[0], metrics.extreme[1], metrics.extreme[2], metrics.contrast,
				metrics.bbox.Dx(), metrics.bbox.Dy())
			logLines = append(logLines, line)
			t.Log(line)
			rows = append(rows, probeRow{bg: style.bg,
				label: probeLabel(t, setup.scale, style.label, style.bg),
				crop:  probeCrop(frame, metrics.bbox.Inset(-probeStripRowPad).Intersect(frame.Bounds()))})
		}
		probeWritePNG(t, filepath.Join(outDir, fmt.Sprintf("fontprobe-%s-%dx.png", platform, int(setup.scale))), probeCompose(rows, setup.zoom))
	}
	logPath := filepath.Join(outDir, "fontprobe-"+platform+".txt")
	if err := os.WriteFile(logPath, []byte(strings.Join(logLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("探针数据日志写入失败: %v", err)
	}
	if absDir, err := filepath.Abs(outDir); err == nil {
		outDir = absDir
	}
	t.Logf("探针产物目录: %s", outDir)
}

// probeWeightStyles 是字重阶梯样本：黑字白底，逐族逐字重请求。
func probeWeightStyles() []probeStyle {
	black, white := ui.Hex("#000000"), ui.Hex("#FFFFFF")
	const group = "字重"
	styles := []probeStyle{
		{group: group, label: "MiSans 400", family: fontMiSansFamily, weight: 400, opacity: 1, bg: white, text: black},
		{group: group, label: "MiSans 500", family: fontMiSansFamily, weight: 500, opacity: 1, bg: white, text: black},
		{group: group, label: "MiSans 600", family: fontMiSansFamily, weight: 600, opacity: 1, bg: white, text: black},
		{group: group, label: "MiSans 700", family: fontMiSansFamily, weight: 700, opacity: 1, bg: white, text: black},
	}
	for _, weight := range []int{150, 200, 300, 330, 400, 450, 500, 520, 600, 630, 700} {
		styles = append(styles, probeStyle{group: group, label: fmt.Sprintf("MiSans VF %d", weight),
			family: fontMiSansVFFamily, weight: weight, opacity: 1, bg: white, text: black})
	}
	// 不指定族、只提字重：mygo 内置控件（如 Toast 标题的 600）对主题字体的处境。
	styles = append(styles, probeStyle{group: group, label: "主题字体 600", weight: 600, opacity: 1, bg: white, text: black})
	return styles
}

// probeColorStyles 是文字色 Token 样本：正文 / 辅助（60% alpha）× 浅深底，外加元素级
// Opacity(0.6) 对照行——与辅助行像素相同即说明「色级 alpha ≡ 元素不透明度」。
func probeColorStyles() []probeStyle {
	const group = "颜色"
	lightBg, darkBg := ui.Hex("#FAFBFC"), ui.Hex("#151A21") // 同 brandTheme 的 Background
	return []probeStyle{
		{group: group, label: "浅底 正文", opacity: 1, bg: lightBg, text: colTextLight},
		{group: group, label: "浅底 辅助 60%", opacity: 1, bg: lightBg, text: colTextMutedLight},
		{group: group, label: "深底 正文", opacity: 1, bg: darkBg, text: colTextDark},
		{group: group, label: "深底 辅助 60%", opacity: 1, bg: darkBg, text: colTextMutedDark},
		{group: group, label: "浅底 对照 Opacity(0.6)", opacity: 0.6, bg: lightBg, text: ui.Hex("#3C3C43")},
		{group: group, label: "深底 对照 Opacity(0.6)", opacity: 0.6, bg: darkBg, text: ui.Hex("#EBEBF5")},
	}
}

// probeRender 按 scale（每 DIP 的设备像素数）渲染一个样本并量测。
func probeRender(t *testing.T, scale float32, style probeStyle) (probeMetrics, *image.RGBA) {
	t.Helper()
	bg, textColor := style.bg, style.text
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(brandTheme(c))
		c.Root().Background(bg)
		element := ui.Text(c, probeSampleText).FontSize(probeSampleFontSize).Margin(20, 0, 0, 20).TextColor(textColor)
		if style.family != "" {
			element = element.Font(style.family)
		}
		if style.weight != 0 {
			element = element.FontWeight(style.weight)
		}
		if style.opacity < 1 {
			element = element.Opacity(style.opacity)
		}
	}, 600, 120)
	tester.SetScale(scale)
	tester.Frame()
	frame := tester.Image()
	return probeMeasure(frame, scale, bg, textColor), frame
}

// probeLabel 渲染参考图的行说明（与样本同缩放、同底色），裁到墨迹包围盒。
func probeLabel(t *testing.T, scale float32, text string, bg ui.Color) *image.RGBA {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(brandTheme(c))
		c.Root().Background(bg)
		shade := colTextLight
		if probeLuminance(bg.R, bg.G, bg.B) < 128 {
			shade = colTextDark
		}
		ui.Text(c, text).FontSize(probeLabelFontSize).Margin(probeStripRowPad, 0, 0, 0).TextColor(shade)
	}, 260, 40)
	tester.SetScale(scale)
	tester.Frame()
	labelFrame := tester.Image()
	// 裁片限定在帧内：越界处取到的是透明像素而不是底色。
	return probeCrop(labelFrame, probeInkBounds(labelFrame, bg).Inset(-probeStripRowPad).Intersect(labelFrame.Bounds()))
}

// probeMeasure 量一帧：以底色亮度为基准，统计偏离底色的像素（有墨像素）。
func probeMeasure(frame *image.RGBA, scale float32, bg, textColor ui.Color) probeMetrics {
	bgLum := probeLuminance(bg.R, bg.G, bg.B)
	// 彩边的基线：文字与底色自身的通道差（灰字偏色不算彩边）。
	baseSpread := max(probeSpread(bg.R, bg.G, bg.B), probeSpread(textColor.R, textColor.G, textColor.B))
	metrics := probeMetrics{bbox: probeInkBounds(frame, bg)}
	var inks []probeInk
	for posY := frame.Bounds().Min.Y; posY < frame.Bounds().Max.Y; posY++ {
		for posX := frame.Bounds().Min.X; posX < frame.Bounds().Max.X; posX++ {
			pixel := frame.RGBAAt(posX, posY)
			deviation := probeDeviation(pixel, bgLum)
			if deviation <= probeInkThreshold {
				continue
			}
			metrics.ink += float64(deviation) / 255
			inks = append(inks, probeInk{rgb: [3]uint8{pixel.R, pixel.G, pixel.B}, deviation: deviation})
		}
	}
	metrics.ink /= float64(scale) * float64(scale) // 归一到 scale=1 的 DIP 像素
	if len(inks) == 0 {
		return metrics
	}
	// 满覆盖的参照取偏离度的第 99 百分位：个别离群像素不致把参照顶高
	// （mac 上半透明浅字的笔画交汇处会比合成色更亮）。
	slices.SortFunc(inks, func(first, second probeInk) int { return first.deviation - second.deviation })
	full := inks[(len(inks)-1)*99/100].deviation
	coreCounts := map[[3]uint8]int{} // 笔画核心像素各颜色出现的次数
	fringeSum, fringeCount := 0.0, 0
	for _, inkPixel := range inks {
		if inkPixel.deviation >= full*9/10 {
			coreCounts[inkPixel.rgb]++
			continue
		}
		if inkPixel.deviation > full*5/100 { // 其余是边缘像素：量亚像素彩边
			excess := max(probeSpread(inkPixel.rgb[0], inkPixel.rgb[1], inkPixel.rgb[2])-baseSpread, 0)
			metrics.fringeMax = max(metrics.fringeMax, excess)
			fringeSum += float64(excess)
			fringeCount++
		}
	}
	if fringeCount > 0 {
		metrics.fringeMean = fringeSum / float64(fringeCount)
	}
	// 核心色取众数。
	bestCount := 0
	for shade, count := range coreCounts {
		if count > bestCount {
			metrics.extreme, bestCount = shade, count
		}
	}
	metrics.contrast = probeContrast(metrics.extreme, [3]uint8{bg.R, bg.G, bg.B})
	return metrics
}

// probeInkBounds 是有墨像素的包围盒。
func probeInkBounds(frame *image.RGBA, bg ui.Color) image.Rectangle {
	bgLum := probeLuminance(bg.R, bg.G, bg.B)
	var bbox image.Rectangle
	for posY := frame.Bounds().Min.Y; posY < frame.Bounds().Max.Y; posY++ {
		for posX := frame.Bounds().Min.X; posX < frame.Bounds().Max.X; posX++ {
			if probeDeviation(frame.RGBAAt(posX, posY), bgLum) <= probeInkThreshold {
				continue
			}
			spot := image.Rect(posX, posY, posX+1, posY+1)
			if bbox.Empty() {
				bbox = spot
			} else {
				bbox = bbox.Union(spot)
			}
		}
	}
	return bbox
}

// probeDeviation 是像素亮度偏离底色亮度的绝对值。
func probeDeviation(pixel color.RGBA, bgLum int) int {
	deviation := probeLuminance(pixel.R, pixel.G, pixel.B) - bgLum
	if deviation < 0 {
		return -deviation
	}
	return deviation
}

// probeLuminance 是亮度的整数近似（通道均值）。
func probeLuminance(red, green, blue uint8) int {
	return (int(red) + int(green) + int(blue)) / 3
}

// probeSpread 是三通道极差（最大减最小）。
func probeSpread(red, green, blue uint8) int {
	return int(max(red, green, blue)) - int(min(red, green, blue))
}

// probeHex 把颜色写成 #rrggbb，带 alpha 时追加 @百分比。
func probeHex(shade ui.Color) string {
	if shade.A != 255 {
		return fmt.Sprintf("#%02x%02x%02x@%d%%", shade.R, shade.G, shade.B, (int(shade.A)*100+127)/255)
	}
	return fmt.Sprintf("#%02x%02x%02x", shade.R, shade.G, shade.B)
}

// probeContrast 是两色的 WCAG 对比度。
func probeContrast(first, second [3]uint8) float64 {
	lit, dim := probeRelativeLuminance(first), probeRelativeLuminance(second)
	if lit < dim {
		lit, dim = dim, lit
	}
	return (lit + 0.05) / (dim + 0.05)
}

// probeRelativeLuminance 是 WCAG 相对亮度（sRGB 线性化后加权）。
func probeRelativeLuminance(rgb [3]uint8) float64 {
	channel := func(value uint8) float64 {
		ratio := float64(value) / 255
		if ratio <= 0.04045 {
			return ratio / 12.92
		}
		return math.Pow((ratio+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(rgb[0]) + 0.7152*channel(rgb[1]) + 0.0722*channel(rgb[2])
}

// probeCrop 从帧里拷出 rect 区域。
func probeCrop(frame *image.RGBA, rect image.Rectangle) *image.RGBA {
	crop := image.NewRGBA(image.Rect(0, 0, max(rect.Dx(), 0), max(rect.Dy(), 0)))
	for posY := 0; posY < crop.Bounds().Dy(); posY++ {
		for posX := 0; posX < crop.Bounds().Dx(); posX++ {
			crop.SetRGBA(posX, posY, frame.RGBAAt(rect.Min.X+posX, rect.Min.Y+posY))
		}
	}
	return crop
}

// probeCompose 把各行拼成参考图：三列=行说明 / 原生像素 / 最近邻放大，行序=样本序。
func probeCompose(rows []probeRow, zoom int) *image.RGBA {
	labelWidth, nativeWidth, height := 0, 0, 0
	for _, row := range rows {
		labelWidth = max(labelWidth, row.label.Bounds().Dx())
		nativeWidth = max(nativeWidth, row.crop.Bounds().Dx())
		height += row.crop.Bounds().Dy()*zoom + probeStripRowGap
	}
	labelLeft := probeStripEdgePad
	nativeLeft := labelLeft + labelWidth + probeStripGap
	zoomLeft := nativeLeft + nativeWidth + probeStripGap
	canvas := image.NewRGBA(image.Rect(0, 0, zoomLeft+probeStripZoomW+probeStripEdgePad, height))
	top := 0
	for _, row := range rows {
		rowHeight := row.crop.Bounds().Dy() * zoom
		probeFillRows(canvas, top, rowHeight, color.RGBA{row.bg.R, row.bg.G, row.bg.B, 0xff})
		probeFillRows(canvas, top+rowHeight, probeStripRowGap, probeRowSeparator)
		probeBlit(canvas, row.label, labelLeft, top+(rowHeight-row.label.Bounds().Dy())/2)
		probeBlit(canvas, row.crop, nativeLeft, top+(rowHeight-row.crop.Bounds().Dy())/2)
		probeBlitZoomed(canvas, row.crop, zoomLeft, top, zoom, min(row.crop.Bounds().Dx(), probeStripZoomW/zoom))
		top += rowHeight + probeStripRowGap
	}
	return canvas
}

// probeFillRows 把画布的 [top, top+height) 行填成纯色。
func probeFillRows(canvas *image.RGBA, top, height int, shade color.RGBA) {
	for posY := top; posY < top+height; posY++ {
		for posX := canvas.Bounds().Min.X; posX < canvas.Bounds().Max.X; posX++ {
			canvas.SetRGBA(posX, posY, shade)
		}
	}
}

// probeBlit 把 src 原样画到 dst 的 (left, top)。
func probeBlit(dst, src *image.RGBA, left, top int) {
	for posY := 0; posY < src.Bounds().Dy(); posY++ {
		for posX := 0; posX < src.Bounds().Dx(); posX++ {
			dst.SetRGBA(left+posX, top+posY, src.RGBAAt(posX, posY))
		}
	}
}

// probeBlitZoomed 把 src 的前 sourceWidth 列以 zoom 倍最近邻放大画到 dst 的 (left, top)。
func probeBlitZoomed(dst, src *image.RGBA, left, top, zoom, sourceWidth int) {
	for srcY := 0; srcY < src.Bounds().Dy(); srcY++ {
		for srcX := 0; srcX < sourceWidth; srcX++ {
			pixel := src.RGBAAt(srcX, srcY)
			for stepY := 0; stepY < zoom; stepY++ {
				for stepX := 0; stepX < zoom; stepX++ {
					dst.SetRGBA(left+srcX*zoom+stepX, top+srcY*zoom+stepY, pixel)
				}
			}
		}
	}
}

// probeWritePNG 把参考图落盘。
func probeWritePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("参考图创建失败 %s: %v", path, err)
	}
	encoder := png.Encoder{CompressionLevel: png.BestCompression} // 参考图要入库，压到最小
	if err := encoder.Encode(file, img); err != nil {
		file.Close()
		t.Fatalf("参考图编码失败 %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("参考图写入失败 %s: %v", path, err)
	}
}
