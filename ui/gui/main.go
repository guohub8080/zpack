// zpack GUI 主程序（MyGo native 路线，ui/gui/DESIGN.md M1）。
//
// 设计语言 v3（DeepSeek harness 布局 + 无标题带）：窗口没有独立 Header——内容
// 从窗口顶直接流下；侧栏整条稍深色恒不透明（railLight/railDark），栏内无深色块
// ——选中=浅色胶囊 + 品牌蓝图标（自定义 navItem，不用 ui.Sidebar 的灰 pill）。
// 内容区左对齐编辑式排版：contentPadX 呼吸边距，空态页走垂直居中 hero。
// 窗口控制钮位置每帧动态适配：macOS 红绿灯与 Linux 左布局在左（bar.Left>0 →
// 侧栏让位），Windows 与 Linux 右布局在右（bar.Right>0 → 内容区顶部让位），
// 全屏全零两侧都不让——桌面按钮布局怎么换，界面自动跟随。
// 品牌双色 Z 为唯一记忆点：空态右下角水印 + 拖入时全窗品牌蓝 tint。
// 架构铁律（../doc.go）：零引擎代码——一切经 service.go 的 exec zpack 契约。
package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const sidebarWidth = 176

// 品牌双色（assets/zpack-icon.svg 同源）：主题 accent/success 的源头。
const (
	brandBlue  = "#376DB5"
	brandGreen = "#72C3A0"
)

// REPO_URL 开源仓库地址（设置页链接；显示文字去掉协议头）。
const REPO_URL = "https://github.com/guohub8080/zpack"

// 侧栏底色（整条稍深色——DeepSeek harness 布局：栏内只有浅色胶囊，不叠深色块）
const (
	railLight = "#ECEFF3"
	railDark  = "#1B2230"
)

// hexA 解析 #RRGGBB 为带 alpha 的 Color（主题派生色用）。
func hexA(hex string, alpha float32) ui.Color {
	value, _ := strconv.ParseUint(hex[1:], 16, 32)
	return ui.RGBA(uint8(value>>16), uint8(value>>8), uint8(value), alpha)
}

// 文字色 Token（唯一定义点——页面代码一律走 t.Text / t.TextMuted 引用，禁止散写
// 灰阶 ui.Hex）。配色逻辑模仿 macOS 语义标签色：同一基色调 alpha 分层（浅色基
// #3C3C43 暖深灰 / 深色基 #EBEBF5 冷亮灰），60% α ≈ AppKit secondaryLabelColor
// 的视觉层级；正文 primary 用户裁定非纯黑（m08111），浅色取 zinc-700、深色 zinc-200。
var (
	colTextLight      = ui.Hex("#3f3f46")     // 正文·浅色
	colTextDark       = ui.Hex("#e4e4e7")     // 正文·深色
	colTextMutedLight = hexA("#3C3C43", 0.60) // 辅助·浅色（解释行/hint，白底合成 ≈#8A8A8E）
	colTextMutedDark  = hexA("#EBEBF5", 0.60) // 辅助·深色
)

// brandZ 是品牌 Z 标志本体（空态水印/侧栏标识/邀请空态共用）。
var brandZ = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1417.3 1417.3">
<path fill="` + brandBlue + `" d="M1280.2,349.6l-404.1,2.1c-258.2,0-516.6,1-774.7-1.2l-6.2-5.8c-1.2-5.6-.5-11.2.7-16.7,6.9-31.2,37.6-111.4,51.7-140.6,35.6-73.7,85.5-103.7,156.3-108.4l1028.3,1.3c10.1,4.1,16.3,12.3,15,25.6"/>
<path fill="` + brandGreen + `" d="M396.6,1118.4c-13.4-8-17.8-23.2-9.9-38.1,63.9-129,114.1-274.3,180.3-401.2,8.1-15.6,17.6-28.8,27.6-42.8,106.7-94.6,211.6-192.2,318.3-286.8h367.3c-3.3,8.3-4.4,19.6-11.4,27-163.6,164.8-335.1,320.3-502.9,479.9-76.6,72.9-152.4,146.8-228.5,220.2h809.1c.9,0,5.4,5.4,5.1,7.9-23.2,46.5-44.8,122.6-69.7,167.8-30.3,54.9-55.7,78-115.9,80.7-101.9,4.4-206.5-2.9-308.7-.6l-460.7-214Z"/>
<path fill="` + brandBlue + `" d="M857.3,1332.4l-714.8,2.8c-29.9-2.9-46.8-21.9-47.9-55.3,11.2-56.8,19.2-189.3,21-206.1s5.4-16,9.6-23.2c153.5-142.2,313.3-275.9,469.4-414.3-10,14.1-19.4,27.2-27.6,42.8-66.1,126.8-116.3,272.2-180.3,401.2-7.9,14.8-3.5,30.1,9.9,38.1"/>
</svg>`))

// iconPNG 是应用图标本体（resources/icon.png，go:embed 内嵌零漂移）。
// 打包形态（mygo build）Dock 图标来自 AppIcon.icns；裸二进制开发态
// （go run / 直跑 bin）没有 bundle ID，LaunchServices 不给 Dock 图块——
// 须在 WhenReady 运行时经 Dock.SetIcon 补上。
//
//go:embed resources/icon.png
var iconPNG []byte

// iconWindowsPNG 是 Windows 专属全出血图标（resources/icon-windows.png，
// 由 assets/zpack-app-icon-1024.png 裁掉 mac 式透明边距后满幅重排）。
// Windows 任务栏/标题图标惯例撑满画布——mac 的 81% 内容留白版在任务栏
// 明显小一圈（用户 2026-11-22 反馈）；mac/Linux 保持 iconPNG 不变。
//
//go:embed resources/icon-windows.png
var iconWindowsPNG []byte

// fontMiSansW450 是正文默认字体：MiSans VF 实例化钉死 wght=450 的静态单实例
// （resources/fonts/MiSans-W450.ttf.gz，fontTools varLib.instancer 产出，
// OS/2.usWeightClass=450、无 fvar）。字重裁定链（2026-11-23 用户）：VF 裸注册
// 缺省实例 330 偏细（发虚根源之一）→ 500 Medium 太粗 → 定格 450（介于
// Regular 与 Medium 之间）。颜色另一半：正文色 zinc-700 #3f3f46（brandTheme
// 显式落色，非 #000 纯黑——用户裁定纯黑难看；柔和深灰读起来是「设计过的灰」
// 而非洗白的黑）。许可：小米 MiSans 免费商用（resources/fonts/README.md）。
//
//go:embed resources/fonts/MiSans-W450.ttf.gz
var fontMiSansW450 []byte

// fontMiSansVF 是三平台默认字体 MiSans 可变字体（resources/fonts/MiSans-VF.ttf.gz，
// gzip -9 压缩嵌入：20.1MB→14.4MB，启动解压注册）。wght 轴 Thin(100)–Bold(700)——
// mygo 内置控件的 500/600 中间字重（tabs/sidebar/table/toast 等）是真实例而非回退；
// 800/900 取最近实例 Bold（MiSans VF 命名实例上限，实测探针验证 2026-11-22）。
// 经 ui.RegisterFont 注册为 fontMiSansFamily 族，brandTheme 把 ui.Theme.Font 指向它
// ——全局默认字体一处生效；显式 Font 等宽调用点已切 JetBrains Mono（fontMonoFamily，系统 monospace 兜底）。
// 许可：小米 MiSans 免费商用（resources/fonts/README.md）。
//
//go:embed resources/fonts/MiSans-VF.ttf.gz
var fontMiSansVF []byte

// fontJetBrainsMono 是英文/数字展示位（尺寸/哈希/百分比/随机密码等等宽场景）的
// JetBrains Mono 可变字体（resources/fonts/JetBrainsMono-VF.ttf，303KB 裸嵌——
// wght 100–800 标准 CSS 阶梯无需手术）。OFL 1.1 许可随库
// （resources/fonts/JetBrainsMono-OFL.txt），免费商用允许内嵌。
//
//go:embed resources/fonts/JetBrainsMono-VF.ttf
var fontJetBrainsMono []byte

// fontMonoFamily 是 JetBrains Mono 的注册族名（等宽调用点 Font 列表首选，
// 逗号列表次选系统 monospace 兜底）。
const fontMonoFamily = "JetBrains Mono"

// fontMiSansFamily 是正文默认族名=W450 静态实例（brandTheme 的 ui.Theme.Font 指向它）。
const fontMiSansFamily = "MiSans"

// fontMiSansVFFamily 是完整可变字体族名（Bold 标题专用——静态单实例族无字重
// 可插值，显式 Bold/FontWeight 必须落在 VF 族上才能取到 700 实例）。
const fontMiSansVFFamily = "MiSans VF"

// registerMiSans 解压并注册内嵌 MiSans 可变字体（main 启动期一次，先于窗口创建）。
// 失败不阻断启动——回退系统默认字体，仅日志。
func registerMiSans() {
	if err := registerGzFont(fontMiSansW450, fontMiSansFamily); err != nil {
		log.Printf("MiSans W450 字体注册失败（回退系统字体）: %v", err)
	}
	if err := registerGzFont(fontMiSansVF, fontMiSansVFFamily); err != nil {
		log.Printf("MiSans VF 字体注册失败（回退系统字体）: %v", err)
	}
	if err := ui.RegisterFont(fontJetBrainsMono, fontMonoFamily); err != nil {
		log.Printf("JetBrains Mono 字体注册失败（回退系统 monospace）: %v", err)
	}
}

// registerGzFont 解压 gzip 字体并注册为指定族名。
func registerGzFont(gz []byte, family string) error {
	gzr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return err
	}
	ttf, err := io.ReadAll(gzr)
	if err != nil {
		return err
	}
	return ui.RegisterFont(ttf, family)
}

// brandTheme 在系统浅/暗主题上落品牌色：accent=品牌蓝三态、success=品牌绿、
// 表面冷调偏蓝（贴品牌色温，去掉中性灰模板感）。
func brandTheme(c *ui.Context) *ui.Theme {
	base := ui.LightTheme()
	accent, hover, pressed := brandBlue, "#2B568C", "#24486F"
	if c.Theme().Dark {
		base = ui.DarkTheme()
		accent, hover, pressed = "#5B9BD5", "#7FB1E0", "#4A88BD"
	}
	theme := *base
	theme.Accent = ui.Hex(accent)
	theme.AccentHover = ui.Hex(hover)
	theme.AccentPressed = ui.Hex(pressed)
	theme.Selection = hexA(accent, 0.20)
	theme.Focus = hexA(accent, 0.55)
	if theme.Dark {
		theme.Background = ui.Hex("#151A21")
		theme.Surface = ui.Hex("#1D242E")
		theme.SurfaceHover = ui.Hex("#26303C")
		theme.Border = ui.Hex("#2C3644")
		theme.Success = ui.Hex("#6FCBA4")
	} else {
		theme.Background = ui.Hex("#FAFBFC")
		theme.Surface = ui.Hex("#FFFFFF")
		theme.SurfaceHover = ui.Hex("#E9EDF2")
		theme.Border = ui.Hex("#E3E8EE")
		theme.Success = ui.Hex("#4FAF87")
	}
	theme.Radius = 8
	theme.Font = fontMiSansFamily // 全局默认字体：MiSans（三平台统一品牌渲染）
	// 正文/辅助色一律走 Token（定义见 colText*）：m08044 不要纯黑；m08153
	// 解释行更浅——mimic macOS secondaryLabel（基色调 60% α）
	if theme.Dark {
		theme.Text = colTextDark
		theme.TextMuted = colTextMutedDark
	} else {
		theme.Text = colTextLight
		theme.TextMuted = colTextMutedLight
	}
	return &theme
}

// icon 造 16×16 描边 SVG 图标（SidebarItem 同款风格）。
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" stroke-linecap="round">` + shapes + `</svg>`))
}

// iconRaw 全彩多路径图标：自带 viewBox 与固定填充色（用户指定素材原样
// 收编），不吃 currentColor 染色——文件/文件夹这类「类型图钉」用固定色。
func iconRaw(viewBox, inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="` + viewBox + `" fill="none">` + inner + `</svg>`))
}

var (
	iconCompress = icon(`<rect x="2" y="2.5" width="12" height="4.5" rx="1"/><rect x="2" y="9" width="12" height="4.5" rx="1"/><path d="M4.5 4.75h.01M4.5 11.25h.01M6.5 4.75h.01M6.5 11.25h.01"/>`)
	iconArchive  = icon(`<rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M2 6h12M6.5 4.5h.01"/>`)
	iconDisc     = icon(`<circle cx="8" cy="8" r="6"/><circle cx="8" cy="8" r="2.2"/>`)
	iconTasks    = icon(`<path d="M3 8.5l3 3 7-7"/>`)
	iconSettings = icon(`<circle cx="8" cy="8" r="2.2"/><path d="M8 1.8v1.8M8 12.4v1.8M1.8 8h1.8M12.4 8h1.8M3.6 3.6l1.3 1.3M11.1 11.1l1.3 1.3M12.4 3.6l-1.3 1.3M4.9 11.1l-1.3 1.3"/>`)
	iconFile     = iconRaw("0 0 1024 1024",
		`<path d="M862 902c0 16.569-13.431 30-30 30H192c-16.569 0-30-13.431-30-30V122c0-16.569 13.431-30 30-30h476l194 194v616z" fill="#4895FF"/>`+
			`<path d="M862 286H698c-16.569 0-30-13.431-30-30V92" fill="#FFFFFF" fill-opacity=".296"/>`+
			`<path d="M290 349m37 0l224.931 0q37 0 37 37l0 0q0 37-37 37l-224.931 0q-37 0-37-37l0 0q0-37 37-37Z" fill="#FFFFFF"/>`+
			`<path d="M290 511m37 0l370 0q37 0 37 37l0 0q0 37-37 37l-370 0q-37 0-37-37l0 0q0-37 37-37Z" fill="#FFFFFF"/>`+
			`<path d="M290 673m37 0l370 0q37 0 37 37l0 0q0 37-37 37l-370 0q-37 0-37-37l0 0q0-37 37-37Z" fill="#FFFFFF"/>`)
	// 文件夹=琥珀双色（用户提供素材）——彩色类型图钉，与蓝文件一眼可辨
	iconFolder = iconRaw("0 0 1024 1024",
		`<path d="M800 256H430.976L367.68 144.224A32 32 0 0 0 339.84 128H64a32 32 0 0 0-32 32v704a32 32 0 0 0 61.888 11.52L245.984 480H800a32 32 0 0 0 32-32V288a32 32 0 0 0-32-32z" fill="#E8A113"/>`+
			`<path d="M986.368 429.888A32 32 0 0 0 960 416H224a32 32 0 0 0-29.888 20.48l-160 416A32 32 0 0 0 64 896h736a32.032 32.032 0 0 0 29.856-20.48l160-416a32.064 32.064 0 0 0-3.488-29.632z" fill="#FFC431"/>`)
	// 移除钮=红底圆形减号徽章（用户指定素材：横杠为镂空，evenodd 钻出
	// 透明缝）——固定红 #D8262C，走 ui.Image 原色管线
	iconRemoveBadge = iconRaw("0 0 1024 1024",
		`<path fill-rule="evenodd" d="M512 71.68c-243.2 0-440.32 197.12-440.32 440.32s197.12 440.32 440.32 440.32 440.32-197.12 440.32-440.32-197.12-440.32-440.32-440.32z m256 484.864c0 6.656-4.608 11.264-11.264 11.264H266.752c-6.656 0-11.264-4.608-11.264-11.264V467.456c0-6.656 4.608-11.264 11.264-11.264h489.984c6.656 0 11.264 4.608 11.264 11.264v89.088z" fill="#D8262C"/>`)
	iconMinus        = icon(`<path d="M4 8h10"/>`)
	iconCheck        = icon(`<path d="M3.5 8.5l3 3 6-6.5"/>`)
	iconKeyPub       = iconRaw("0 0 1024 1024", `<path d="M908.902 147.098c-24.89-24.89-65.62-24.89-90.51 0L800.5 164.99l-83.127-83.127c-24.89-24.89-65.619-24.89-90.51 0-24.89 24.89-24.89 65.619 0 90.51L709.99 255.5l-51.49 51.49-51.127-51.127c-24.89-24.89-65.619-24.89-90.51 0-24.89 24.89-24.89 65.619 0 90.51L567.99 397.5 454.214 511.276C419.27 491.376 378.84 480 335.75 480 203.34 480 96 587.34 96 719.75S203.34 959.5 335.75 959.5 575.5 852.16 575.5 719.75c0-42.828-11.242-83.025-30.918-117.823l364.319-364.319c24.891-24.89 24.891-65.619 0.001-90.51zM335.5 831C273.92 831 224 781.08 224 719.5S273.92 608 335.5 608 447 657.92 447 719.5 397.08 831 335.5 831z" fill="#1A6FE8"/>`)                                                                                                                                                                                                                                                                                                                                                                                                                       // 用户 m05143：公钥=蓝钥匙
	iconKeyPriv      = iconRaw("0 0 1024 1024", `<path d="M380.719194 507.979978a325.467444 325.467444 0 1 1 87.037213 87.037213l-142.986286 142.986286 68.379539 68.379539-87.037213 87.037213-68.379539-68.379539-43.518607 43.518607 68.379539 68.379539-87.037213 87.037213L20.139875 868.559297l360.60327-360.60327z m267.338849-354.471865a186.504893 186.504893 0 1 0 186.504892 186.504892 187.03181 187.03181 0 0 0-186.528843-186.385138z" fill="#FF7369"/>`)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      // 用户 m05161：私钥=珊瑚红钥匙
	iconError        = iconRaw("52.662277 0 1024 1024", `<path d="M286.412277 238.75h583.75v567.5H286.412277z" fill="#FFFFFF"/><path d="M813.033527 697.03625a44.5475 44.5475 0 0 1 13.15875 31.745 44.545 44.545 0 0 1-13.15875 31.6925 44.55 44.55 0 0 1-31.74375 13.1075 44.54875 44.54875 0 0 1-31.6925-13.1075L567.478527 578.355 385.359777 760.47375a44.55 44.55 0 0 1-31.74375 13.1075 44.54875 44.54875 0 0 1-31.6925-13.1075 44.54125 44.54125 0 0 1-13.15875-31.74375 44.5425 44.5425 0 0 1 13.15875-31.745l182.1175-182.06625L321.923527 332.8a44.53875 44.53875 0 0 1-13.15875-31.74375 44.5375 44.5375 0 0 1 13.15875-31.74375 44.5375 44.5375 0 0 1 31.74375-13.1075 44.53875 44.53875 0 0 1 31.74375 13.1075l182.0675 182.16875L749.597277 269.3125a44.5375 44.5375 0 0 1 31.74375-13.1075 44.53875 44.53875 0 0 1 31.74375 13.1075 44.5475 44.5475 0 0 1 13.1075 31.74375 44.54375 44.54375 0 0 1-13.15875 31.74375L630.914777 514.91875l182.11875 182.1175zM564.662277 0C281.884777 0 52.662277 229.2225 52.662277 512s229.2225 512 512 512 512-229.2225 512-512-229.2225-512-512-512z" fill="#D24646"/>`) // 用户 m05049 原图全量（叉臂+圆盘两 subpath 缺一不可）
	iconWarn         = iconRaw("0 0 1024 1024", `<path d="M572.074667 337.134933a57.275733 57.275733 0 1 0-114.176-6.007466h-0.170667l12.936533 272.896v0.443733a44.544 44.544 0 1 0 89.019734-1.194667l12.424533-266.1376z m-196.949334-191.214933c76.151467-130.048 199.816533-129.774933 275.797334 0l340.3776 581.085867c76.117333 130.048 15.7696 235.4176-135.236267 235.4176H169.984c-150.8352 0-211.217067-105.6768-135.202133-235.4176L375.125333 145.92z m140.049067 687.581867a57.275733 57.275733 0 1 0 0-114.517334 57.275733 57.275733 0 0 0 0 114.517334z" fill="#E6A23C"/>`)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 // 用户 m07961 警告三角原图原样坐标（只换琥珀 fill——缩放 path 会弄坏弧线标志位）
	iconDone         = iconRaw("0 0 1024 1024", `<path d="M512 0C229.003636 0 0 229.003636 0 512s229.003636 512 512 512 512-229.003636 512-512S794.996364 0 512 0z m260.654545 425.425455l-279.272727 279.272727c-5.585455 5.585455-13.032727 9.309091-21.410909 9.309091-10.24 1.861818-20.48-0.930909-27.927273-8.378182l-175.941818-176.872727a30.906182 30.906182 0 0 1 0-43.752728l14.894546-14.894545c12.101818-12.101818 31.650909-12.101818 43.752727 0l141.498182 141.498182 244.829091-244.829091c12.101818-12.101818 31.650909-12.101818 43.752727 0L772.654545 381.672727c12.101818 12.101818 12.101818 31.650909 0 43.752728z" fill="#65B85D"/>`)                                                                                                                                                                                                                                                                                                                                                                                                                                                               // 用户 m05014 原图原样（勿缩放：弧线标志位不是坐标）
	iconCross        = icon(`<path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>`)
	iconChevronDown  = icon(`<path d="M4 6l4 4 4-4"/>`)
	iconChevronRight = icon(`<path d="M6 4l4 4-4 4"/>`)
	iconEye          = icon(`<path d="M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><circle cx="8" cy="8" r="1.6"/>`)
	iconEyeOff       = icon(`<path d="M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z"/><path d="M3 13L13 3"/>`)
	iconExtract      = icon(`<path d="M8 10.5V3.2M5.4 5.6L8 3l2.6 2.6"/><path d="M2.8 9.8v2.3c0 .8.6 1.4 1.4 1.4h7.6c.8 0 1.4-.6 1.4-1.4V9.8"/>`)
	iconLock         = icon(`<rect x="3.5" y="7" width="9" height="6.5" rx="1.5"/><path d="M5.5 7V5a2.5 2.5 0 015 0v2"/>`)
	iconUnlock       = icon(`<rect x="3.5" y="7" width="9" height="6.5" rx="1.5"/><path d="M5.5 7V5a2.5 2.5 0 014.9-.6"/>`)
	iconKey          = icon(`<circle cx="5.5" cy="10.5" r="3"/><path d="M7.8 8.2L14 2M11.6 4.4l1.8 1.8M9.4 6.6l1.8 1.8"/>`)
	iconBase64       = icon(`<path d="M5.5 3.5L2 8l3.5 4.5M10.5 3.5L14 8l-3.5 4.5"/>`)
	iconVideo        = icon(`<rect x="1.5" y="3" width="13" height="10" rx="2"/><path d="M6.7 6.1l3.6 1.9-3.6 1.9z"/>`)
	iconImage        = icon(`<rect x="1.5" y="2.5" width="13" height="11" rx="1.5"/><circle cx="5.2" cy="6" r="1.1"/><path d="M2.5 11.5l3-3L8 11l2.5-2.5 3 3"/>`)
	iconAudio        = icon(`<path d="M6 13.2V3.4l6-1.7v9.6"/><circle cx="3.8" cy="13.4" r="2.2"/><circle cx="9.8" cy="11.7" r="2.2"/>`)
	iconHash         = icon(`<path d="M6.5 2.5L5.5 13.5M10.5 2.5L9.5 13.5M2.8 5.8h11M2.2 10.2h11"/>`)
	iconDice         = icon(`<path d="M2 4.5h2.2c.8 0 1.6.4 2.1 1l5.4 6c.5.6 1.3 1 2.1 1h.2M12.6 10.4 14 11.8l1.4-1.4M2 11.5h2.2c.8 0 1.6-.4 2.1-1l1-1.1M9.4 6.6l1-1.1c.5-.6 1.3-1 2.1-1h1.5M12.6 2.2 14 3.6l-1.4 1.4"/>`)
	iconDoc          = icon(`<path d="M4 1.5h5.5L13 5v9.5H4z"/><path d="M9.5 1.5V5H13"/>`)
)

// 页面枚举（侧栏选中项）
const (
	pageCompress   = "compress"
	pageExtract    = "extract"
	pageEncrypt    = "encrypt"
	pageDecrypt    = "decrypt"
	pageKeys       = "keys"
	pageBase64     = "base64"
	pageMedia      = "media"
	pageImage      = "image"
	pagePdfMerge   = "pdfmerge"
	pagePdfExtract = "pdfextract"
	pageMediaCmp   = "mediacmp"
	pageAudioCvt   = "audiocvt"
	pageAudioCmp   = "audiocmp"
	pageImageCmp   = "imagecmp"
	pageImageNew   = "imagenew"
	pageOffice     = "office"
	pagePdfRotate  = "pdfrotate"
	pagePdfResize  = "pdfresize"
	pageHash       = "hash"
	pageRandom     = "random"
	pageArchive    = "archive"
	pageBurn       = "burn" // 刻录光盘（m06038：替归档浏览入口）
	pageTasks      = "tasks"
	pageSettings   = "settings"
)

// app 是窗口状态（view 每帧从它重建界面）。
type app struct {
	win       *mygo.Window
	page      string
	service   *Service
	themeMode int // 外观态：themeModeSystem/Light/Dark（gui-settings.json 持久化，见 appearance.go）

	// 用户反馈（异步 toast 队列——帧外 goroutine/回调入队，pane 帧首 drain，见 sniff.go）
	pendingToasts []string
	toastMu       sync.Mutex

	// 压缩台状态
	inputs        []string             // 已选输入路径
	rows          ui.ListState         // 清单列表状态（框架级拖拽排序 ListState.Reorder）
	burnRows      ui.ListState         // 刻录清单列表状态（与压缩台同款拖拽排序）
	infoCache     map[string]entryInfo // 路径→尺寸/是否目录（入列时 Stat 一次，逐帧复用）
	sizeMu        sync.Mutex           // 目录异步求和在飞表锁（临界区只有 map 读写）
	sizing        map[string]bool      // 已派发后台 walk 的目录路径（去重防 goroutine 堆积）
	formatStr     string               // 格式即后缀：.zip/.tar.gz/.tar（名字旁下拉直改）
	level         float64              // 0-9
	password      string
	pwModeStr     string // 密码模式（无密码/加密）——加密态才出输入框               // zip 密码（空=不加密；单行输入即开关，用户定案）
	showPassword  bool   // 眼睛钮：明文显示密码
	volume        string // 自定义分卷尺寸（仅「自定义」态可编辑；人读格式）
	volumeModeStr string // 分卷选择：不分卷/预置值/自定义（单一下拉，用户定案 m03133）
	name          string // 自定义包名（空=默认首输入名；后缀由格式决定不进名字）
	nameDirty     bool   // 用户改过名字——清单增删后不再自动跟随默认
	outDir        string // 自定义保存目录（空=跟随默认首输入所在目录；可输入可点选）
	outDirDirty   bool   // 用户改过保存位置——清单增删后不再自动跟随默认
	outDirGhost   string // 空态默认路径假绑定值（ReadOnly 灰字，参与测高）
	outDirFocus   bool   // 空态点击聚焦后翻回真值绑定（编辑态）

	// 解压台状态
	extractPath     string         // 待解压归档（可为 .001 分卷首卷）
	extractInfo     *InspectResult // 归档体检（格式/文件数/加密/分卷数；异步回填）
	extractDir      string         // 自定义解压目录（空=归档所在目录）
	extractDirDirty bool
	extractDirGhost string // 解压位置空态假绑定（同 outDirGhost）
	extractDirFocus bool
	extractPw       string // 解压密码（归档体检为加密时出现）
	extractShowPw   bool

	// 格式转换组（m05445：视频/图片转换——压缩台同款参数区标准）
	mediaPath     string // 待转换视频（空=hero 空态）
	mediaFmt      string // 输出容器扩展名（.mp4/.mkv/.mov/.webm）
	mediaOut      string // 自定义输出目录（空=源文件旁）
	mediaOutDirty bool
	mediaOutGhost string // 输出位置空态假绑定（同 outDirGhost）
	mediaOutFocus bool
	imagePath     string // 待转换图片（空=hero 空态）
	imageFmt      string // 输出格式扩展名（.png/.jpg/.webp/.bmp/.tiff/.gif）
	imageOut      string
	imageOutDirty bool
	imageOutGhost string
	// 文档组（m05530：PDF 合并/提取页——引擎 pdf merge/extract）
	pdfMergeInputs     []string     // 合并清单（顺序即合并顺序）
	pdfMergeRows       ui.ListState // 合并清单列表状态（拖拽排序 ListState.Reorder——顺序即合并顺序）
	pdfMergeName       string       // 自定义输出名（空=「合并文档」）
	pdfMergeOut        string       // 自定义输出目录（空=首文件旁）
	pdfMergeOutDirty   bool
	pdfMergeOutGhost   string
	pdfMergeOutFocus   bool
	pdfExtractPath     string // 待提取 PDF
	pdfExtractPages    string // 页码区间（如 1-3,5,8-；必填——全提取请直接拷贝）
	pdfExtractOut      string // 自定义输出目录（空=源文件旁）
	pdfExtractOutDirty bool
	pdfExtractOutGhost string
	pdfExtractOutFocus bool
	// 媒体压缩/音频（m05602：CLI 全命令铺进 GUI）
	mediaCmpPath     string // 待压缩视频
	mediaCmpMode     string // 目标体积档（50% 缺省/100MB/25MB/自定义）
	mediaCmpSize     string // 自定义目标体积（如 80MB）
	mediaCmpOut      string
	mediaCmpOutDirty bool
	mediaCmpOutGhost string
	mediaCmpOutFocus bool
	audioCvtPath     string // 待转换音频/视频（抽音轨）
	audioCvtFmt      string // 输出扩展名（.mp3/.m4a/.flac/.wav/.ogg/.opus）
	audioCvtBitrate  string // 可选码率（如 128k）
	audioCvtRate     string // 可选采样率（如 44100）
	audioCvtMono     bool
	audioCvtOut      string
	audioCvtOutDirty bool
	audioCvtOutGhost string
	audioCvtOutFocus bool
	audioCmpPath     string // 待压缩音频
	audioCmpBitrate  string // 可选目标码率（如 64k；空=引擎自动 50%）
	audioCmpMono     bool
	audioCmpOut      string
	audioCmpOutDirty bool
	audioCmpOutGhost string
	audioCmpOutFocus bool
	// 图片压缩/新建 + Office 转换（m05602 第二批）
	imageCmpRows     ui.ListState // 图片压缩清单列表（不排序）
	imageCmpInputs   []string     // 待压缩图片清单（m07800 多图批压；单张=N=1 特例；jpg 才吃质量参数）
	imageCmpMode     string       // 目标体积档（100KB/500KB/1MB/5MB/自定义）
	imageCmpSize     string       // 自定义目标体积
	imageCmpPriority string       // 体积优先 / 画质优先
	imageCmpOut      string
	imageCmpOutDirty bool
	imageCmpOutGhost string
	imageCmpOutFocus bool
	imageNewSize     string // 尺寸（800x600 / a4 / 55x90mm）
	imageNewColor    string // 可选颜色（red / #FF0000）
	imageNewPpi      string // 可选打印密度
	imageNewName     string // 输出文件名（空=空白图.png）
	imageNewOut      string
	imageNewOutDirty bool
	imageNewOutGhost string
	imageNewOutFocus bool
	officePath       string // 待转 Office 文档（doc/xls/ppt 及 WPS 家族）
	officeEngine     string // 引擎档（自动/MS Office/WPS/LibreOffice）
	officeOut        string
	officeOutDirty   bool
	officeOutGhost   string
	officeOutFocus   bool
	// PDF 旋转/页面尺寸 + 文件哈希（m05602 第三批）
	pdfRotatePath     string // 待旋转 PDF
	pdfRotateDir      string // 方向档（左转 90°/右转 90°/转 180°）
	pdfRotatePages    string // 可选页码区间（空=全部）
	pdfRotateOut      string
	pdfRotateOutDirty bool
	pdfRotateOutGhost string
	pdfRotateOutFocus bool
	pdfResizePath     string // 待统一页面尺寸的 PDF
	pdfResizeSize     string // 纸型档（a4/a4l/letter/210x297mm）
	pdfResizePages    string // 可选页码区间（空=全部）
	pdfResizeOut      string
	pdfResizeOutDirty bool
	pdfResizeOutGhost string
	pdfResizeOutFocus bool
	pdfResizeForce    bool         // 含文字页仍缩放（引擎 --force；缺省引擎会拒绝并点名页码）
	hashInputs        []string     // 哈希文件清单
	hashRows          ui.ListState // 哈希清单列表（不排序）
	hashAlgo          string       // 算法档（md5/sha1/sha256/sha384/sha512/crc32）
	hashExpect        string       // 可选期望值（清单=1 时显示）
	hashBusy          bool
	hashResults       []hashResult
	hashErr           string
	// 图片预览（2026-11-22：图片转换/压缩页共用）
	imgPrevPath  string         // 预览对应的文件（过期结果丢弃）
	imgPrevSrc   ui.ImageSource // 解码产物（nil=不可预览）
	imgPrevState int            // 0=无 1=读取中 2=就绪 3=不支持
	imgPrevW     float32
	imgPrevH     float32
	// 随机生成（2026-11-22：crypto password/uuid 的 GUI 面；2026-11-23 字符集改原生多选+自定义分组）
	randomModeIdx int             // 0=密码 1=UUID
	pwLenStr      string          // 长度档（8/12/16/24/32/64）
	uuidFmt       string          // UUID 输出形态（标准/大写/无连字符/大写无连字符）
	pwCharsets    map[string]bool // 字符集多选：键=内置字符类 id 或「custom:」+自定义分组 id（缺省四个内置类全开）
	csMenuOpen    bool            // 字符集多选菜单是否展开
	randomBusy    bool
	randomOut     string
	randomErr     string
	// 刻录光盘（m06038：burn devices + 镜像/目录刻录）
	burnInputs     []string // 刻录内容清单（任意数量文件/文件夹，2026-11-22 用户定案）
	burnDevices    []BurnDevice
	burnDevStr     string
	burnModeStr    string  // 可追加（multi 不封盘）/ 一次性烧录（mastered 封盘）
	burnSpeedIdx   float64 // 刻录速度档：0=自动；1-6 → 4/8/16/24/32/48x
	burnBlank      bool
	burnVolumeName string // 光盘名称（仅空白盘可设；留空=引擎缺省：单个目录用目录名）
	burnQueried    bool
	burnDevErr     string

	// 归档浏览（m05602 收官：list 可视化 + test 校验；入口已让位刻录，代码保留）
	archivePath    string
	archiveListing *ListResult
	archiveBusy    bool
	archiveTest    int // 0=未测 1=测试中 2=通过 3=失败
	archiveTestMsg string
	archiveFails   []string
	imageOutFocus  bool

	donePolicy   string // 完成后策略：无动作/打开文件夹（压缩台+解压台共用）
	advancedOpen bool   // 压缩台高级设置展开态（默认折叠——密码/分卷/级别）
	// 工具分组（用户 m04704 定案：加解密单独分组、文本优先、Base64 不强制文件）
	encModeIdx    int    // 加密：0=文本 1=文件
	encText       string // 明文输入
	encTextOut    string // 密文输出（文本态）
	encErr        string // 文本态失败原因
	encFile       string // 输入文件
	encKey        string // 公钥
	encOut        string // 输出文件（空=默认推导）
	encOutDirty   bool
	encRunning    bool
	encDone       *ToolResult // 文件态结果
	decModeIdx    int
	decText       string
	decTextOut    string
	decErr        string
	decFile       string
	decKey        string
	decOut        string
	decOutDirty   bool
	decRunning    bool
	decDone       *ToolResult
	keyBitsIdx    int // 生成密钥位数：0=1024 1=2048 2=4096
	keyName       string
	keyDir        string
	keyRunning    bool
	keyDone       *ToolResult
	keyDoneBits   string // 生成时用的位数——结果卡标题按它显示，不读当前段选
	engineVersion string // 设置页引擎版本（懒取一次）
	engineQueried bool
	b64In         string // Base64 输入（跑完即清空，guohub 范式）
	b64Running    bool
	b64Err        string
	b64Results    []b64Result // 历史结果（新在前）

	// 处理台状态
	tasks []*Task
}

// ── 整窗视图：侧栏 + 内容区（无标题带——内容从窗口顶直接流下）──
func (a *app) view(c *ui.Context) {
	c.SetTheme(brandTheme(c)) // 品牌主题（每帧按系统深浅派生）
	t := c.Theme()
	vibrant := c.Vibrancy()
	if vibrant {
		c.Root().Background(ui.Transparent)
	}
	bar := c.TitleBar()
	controlsLeft := bar.Left > 0.5   // macOS 红绿灯 / Linux 左布局：控制钮悬于侧栏左上
	controlsRight := bar.Right > 0.5 // Windows / Linux 右布局：控制钮悬于内容区右上

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// 左：侧栏（整条稍深色底，恒不透明——栏内只有浅色胶囊与内容区形成明度差）
		side := ui.Column(c).Width(sidebarWidth).Shrink(0)
		if t.Dark {
			side.Background(ui.Hex(railDark))
		} else {
			side.Background(ui.Hex(railLight))
		}
		side.Children(func() { a.sidebar(c, bar, controlsLeft) })

		// 右：内容区——整面即拖放区（压缩工具的第一动作就是产品本身）
		a.pane(c, bar, controlsRight)
	})
}

// ── 侧栏：让位区（动态）+ 品牌行 + 导航（末项设置）+ 底部处理台 ──
func (a *app) sidebar(c *ui.Context, bar ui.TitleBar, controlsLeft bool) {
	// 顶部让位：控制钮在左（红绿灯/左布局）→ 让到其下缘；否则只留呼吸
	clear := float32(10)
	if controlsLeft {
		clear = max(bar.Bottom, 40) + 2
	}
	ui.Box(c).Height(clear).Shrink(0).DragWindow()

	// 品牌行：双色 Z + 字标（可拖拽；左缩进对齐导航图标列）
	ui.Row(c).Height(28).Shrink(0).AlignItems(ui.Center).Padding(0, 0, 0, 20).DragWindow().Children(func() {
		ui.Image(c, brandZ).Size(19, 19)
		ui.Box(c).Width(8).Shrink(0)
		ui.Text(c, "zpack").FontSize(14).Font(fontMiSansVFFamily).Bold()
	})
	ui.Box(c).Height(4).Shrink(0)

	// 导航（DeepSeek harness 布局：分区小标 + 浅色胶囊选中态，栏内无深色块）
	// 页多了会超高：导航列套 Scroll——长则滚、短则自然（m05740）
	page := a.page
	navWrap := ui.Scroll(c).Grow(1).MinHeight(0)
	navWrap.Children(func() {
		nav := ui.Column(c).Gap(2).Shrink(0).Margin(0, 10, 0, 10)
		nav.Children(func() {
			a.navSectionHeader(c, "归档")
			a.navItem(c, pageCompress, iconCompress, "压缩台", &page, 0)
			a.navItem(c, pageExtract, iconExtract, "解压台", &page, 0)
			a.navItem(c, pageBurn, iconDisc, "刻录光盘", &page, 0)
			a.navItem(c, pageArchive, iconArchive, "归档浏览", &page, 0) // m06330 用户定案：CLI 真实能力不轻撤
			a.navSectionHeader(c, "视频")
			a.navItem(c, pageMedia, iconVideo, "视频转换", &page, 0)
			a.navItem(c, pageMediaCmp, iconVideo, "视频压缩", &page, 0)
			a.navSectionHeader(c, "音频")
			a.navItem(c, pageAudioCvt, iconAudio, "音频转换", &page, 0)
			a.navItem(c, pageAudioCmp, iconAudio, "音频压缩", &page, 0)
			a.navSectionHeader(c, "图片")
			a.navItem(c, pageImage, iconImage, "图片转换", &page, 0)
			a.navItem(c, pageImageCmp, iconImage, "图片压缩", &page, 0)
			a.navItem(c, pageImageNew, iconImage, "新建图片", &page, 0)
			a.navSectionHeader(c, "文档")
			a.navItem(c, pagePdfMerge, iconDoc, "PDF 合并", &page, 0)
			a.navItem(c, pagePdfExtract, iconExtract, "PDF 提取页", &page, 0)
			a.navItem(c, pageOffice, iconDoc, "Office 转换", &page, 0)
			a.navItem(c, pagePdfRotate, iconDoc, "PDF 旋转", &page, 0)
			a.navItem(c, pagePdfResize, iconDoc, "页面尺寸", &page, 0)
			a.navSectionHeader(c, "加密与编码")
			a.navItem(c, pageKeys, iconKey, "生成密钥", &page, 0)
			a.navItem(c, pageEncrypt, iconLock, "加密", &page, 0)
			a.navItem(c, pageDecrypt, iconUnlock, "解密", &page, 0)
			a.navItem(c, pageBase64, iconBase64, "Base64", &page, 0)
			a.navItem(c, pageHash, iconHash, "文件哈希", &page, 0)
			a.navItem(c, pageRandom, iconDice, "随机生成", &page, 0)
			// 设置随列表滚动（2026-11-23 用户定案：只有处理台固定在底角）——不归任何分组，
			// 上方留出分组级的间距，不让它看着像「加密与编码」的最后一项
			ui.Box(c).Height(6).Shrink(0)
			a.navItem(c, pageSettings, iconSettings, "设置", &page, 0)
		})
	})
	// 底部：处理台钉在侧栏底角（独立于分组，运行中角标）。上方必须留间隙：滚动区下沿会把
	// 一行切成半截，不留间隙时这半截行（悬停/选中高亮时尤其明显）会紧贴到固定按钮的顶上
	ui.Column(c).Shrink(0).Margin(6, 10, 10, 10).Children(func() {
		a.navItem(c, pageTasks, iconTasks, "处理台", &page, countRunning(a.tasks))
	})
	if page != a.page {
		a.page = page
	}
}

// navSectionHeader 侧栏分区标题（小号弱化文字，与导航项左对齐；顶部 8——品牌行下收紧）
func (a *app) navSectionHeader(c *ui.Context, title string) {
	t := c.Theme()
	ui.Text(c, title).FontSize(12).TextColor(t.TextMuted).Margin(8, 0, 2, 10)
}

// navItem 侧栏导航项（选中=深蓝胶囊+白字白图标——用户定案 v3 调优，覆盖先前浅色胶囊）。
// 悬停与按下各有反馈（交互铁律）：未选中按下比悬停再深一档，选中按下用 AccentPressed。
func (a *app) navItem(c *ui.Context, id string, svg *ui.SVG, label string, page *string, badge int) {
	t := c.Theme()
	chosen := *page == id
	row := ui.ButtonBase(c).AlignItems(ui.Center).Gap(10).
		Padding(7, 10).Radius(8).AlignSelf(ui.Stretch).Cursor(ui.CursorPointer)
	pillBg := t.Text.Alpha(0.10) // 未选中角标底
	pillFg := t.Text
	pill := ui.Hex(brandBlue) // 选中胶囊底色（深色主题用更沉的同族蓝）
	if t.Dark {
		pill = ui.Hex("#2B568C")
	}
	switch {
	case !chosen && row.Pressed():
		row.Background(t.Text.Alpha(0.12))
	case !chosen && row.Hovered():
		row.Background(t.Text.Alpha(0.06))
	case chosen && row.Pressed():
		row.Background(t.AccentPressed)
	case chosen && row.Hovered():
		row.Background(pill.Mix(ui.Hex("#FFFFFF"), 0.14)) // 亮一档：选中态悬停也要有反馈
	case chosen:
		row.Background(pill)
	}
	row.Children(func() {
		iconColor, labelColor := t.TextMuted, t.Text
		if chosen {
			// 深蓝底 → 白字白图标，角标反白
			pillBg, pillFg = hexA("#FFFFFF", 0.25), ui.Hex("#FFFFFF")
			iconColor, labelColor = ui.Hex("#FFFFFF"), ui.Hex("#FFFFFF")
		}
		ui.Icon(c, svg).Size(16, 16).TextColor(iconColor)
		ui.Text(c, label).FontSize(13).Grow(1).SingleLine().TextColor(labelColor)
		if badge > 0 {
			ui.Box(c).Padding(0, 6).Radius(999).Background(pillBg).Shrink(0).Children(func() {
				ui.Text(c, itoa(badge)).FontSize(11).TextColor(pillFg).SingleLine()
			})
		}
	})
	if row.Clicked() {
		*page = id
	}
}

// ── 内容区：顶部让位拖拽条（动态）+ 页面 + 拖放反馈 ──
func (a *app) pane(c *ui.Context, bar ui.TitleBar, controlsRight bool) {
	t := c.Theme()
	a.drainToasts(c) // 帧首先倒异步 toast（帧外只能入队，见 sniff.go）

	pane := ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Clip()

	// 拖放状态先求值（上一帧事件），再一次建子树
	// 加密/解密页不设整页拖放：拖放分区到字段（输入文件/钥匙各自成区，m05234）
	dropZone := a.page == pageCompress || a.page == pageArchive || a.page == pageExtract ||
		a.page == pageMedia || a.page == pageImage || a.page == pagePdfMerge ||
		a.page == pagePdfExtract || a.page == pageMediaCmp ||
		a.page == pageAudioCvt || a.page == pageAudioCmp || a.page == pageImageCmp ||
		a.page == pageOffice || a.page == pagePdfRotate || a.page == pagePdfResize ||
		a.page == pageHash || a.page == pageBurn
	dragOver := dropZone && pane.FileDragOver()
	if dropZone {
		if files := pane.DroppedFiles(); len(files) > 0 {
			switch a.page {
			case pageArchive:
				a.setArchivePath(files[0]) // 归档浏览单归档：取首件
			case pageBurn:
				a.addBurnInputs(files) // 刻录内容：任意数量文件/文件夹
			case pageExtract:
				a.setExtractArchive(files[0]) // 解压台单归档：取首件
			case pageMedia:
				a.setMediaPath(files[0])
			case pagePdfMerge:
				a.addPdfMerges(files)
			case pagePdfExtract:
				a.setPdfExtractPath(files[0])
			case pageMediaCmp:
				a.setMediaCmpPath(files[0])
			case pageAudioCvt:
				a.setAudioCvtPath(files[0])
			case pageAudioCmp:
				a.setAudioCmpPath(files[0])
			case pageImageCmp:
				a.addImageCmpInputs(files)
			case pageOffice:
				a.setOfficePath(files[0])
			case pagePdfRotate:
				a.setPdfRotatePath(files[0])
			case pagePdfResize:
				a.setPdfResizePath(files[0])
			case pageHash:
				a.addHashFiles(files)
			case pageImage:
				a.setImagePath(files[0])
			default:
				a.addInputs(files)
			}
		}
	}

	pane.Children(func() {
		// 品牌时刻：右下角 Z 水印（默认常驻，垫在内容层下——低透明度不扰阅读）
		// IgnoreInput：水印参与指针命中链会遮挡重叠处的可点控件（血案：Z 挪到
		// .zip 格式下拉上时点击被吞）——装饰层必须退出命中测试
		ui.Box(c).Absolute().Right(-36).Bottom(-36).IgnoreInput(true).Children(func() {
			ui.Image(c, brandZ).Size(230, 230).Opacity(0.06)
		})

		// 右半边 head（用户定案）：页面标题条——Win/Linux 关闭钮落在内容区右缘，
		// 标题条高度盖住按钮带且右缘让位（bar.Right）；macOS 侧无右钮，
		// 标题条做视觉锚点 + 可拖拽窗口
		headH := float32(52)
		if controlsRight {
			// 用户定案（m00961）：head 与标题按钮带严格同高（bar.Height=36）——
			// 按钮多高 head 就多高，齐平不加深；+8/+16 呼吸高度都是丑源
			headH = bar.Height
		}
		ui.Row(c).Height(headH).Shrink(0).AlignItems(ui.Center).
			Padding(0, bar.Right, 0, contentPadX).DragWindow().Children(func() {
			ui.Text(c, pageTitle(a.page)).FontSize(15).Font(fontMiSansVFFamily).Bold()
		})
		// head 分隔线（用户定案）：标题条与内容区的 1px 界线
		ui.Box(c).Height(1).Background(t.Border).Shrink(0)

		// 空态页走垂直居中 hero（Scroll 撑不满高度，居中须无滚动容器承载）
		if a.heroPage() {
			a.heroPane(c)
		} else {
			// 页面（左对齐编辑式排版——内容从窗口顶流下，页面首元素承担呼吸）
			ui.Scroll(c).Grow(1).Children(func() {
				switch a.page {
				case pageEncrypt:
					a.pageEncrypt(c)
				case pageDecrypt:
					a.pageDecrypt(c)
				case pageKeys:
					a.pageKeys(c)
				case pageBase64:
					a.pageBase64(c)
				case pageExtract:
					a.pageExtract(c)
				case pageMedia:
					a.pageMedia(c)
				case pageImage:
					a.pageImage(c)
				case pagePdfMerge:
					a.pagePdfMerge(c)
				case pagePdfExtract:
					a.pagePdfExtract(c)
				case pageMediaCmp:
					a.pageMediaCmp(c)
				case pageAudioCvt:
					a.pageAudioCvt(c)
				case pageAudioCmp:
					a.pageAudioCmp(c)
				case pageImageCmp:
					a.pageImageCmp(c)
				case pageImageNew:
					a.pageImageNew(c)
				case pageOffice:
					a.pageOffice(c)
				case pagePdfRotate:
					a.pagePdfRotate(c)
				case pagePdfResize:
					a.pagePdfResize(c)
				case pageHash:
					a.pageHash(c)
				case pageRandom:
					a.pageRandom(c)
				case pageArchive:
					a.pageArchive(c)
				case pageBurn:
					a.pageBurn(c)
				case pageTasks:
					a.pageTasks(c)
				case pageSettings:
					a.pageSettings(c)
				default:
					a.pageCompress(c)
				}
			})
		}

		// （旧空态水印已并入 pane 首个常驻子元素——见上方「品牌时刻」）

		// 拖入反馈：内嵌圆角环 + 品牌蓝薄雾（整面即拖放区的应答）
		if dragOver {
			ui.Box(c).Absolute().Left(8).Top(8).Right(8).Bottom(8).
				Radius(14).Border(1.5, t.Accent).Background(hexA(brandBlue, 0.05))
		}
	})
}

// 排版节奏 token（唯一来源）：内容左右留白 / 顶部让位带下的第一口气。
// 用户定案（v4）：左右 56→28（56 太空，减半）；底部收紧 28→16。
const (
	contentPadX   = 28
	contentPadTop = 10
	contentPadBtm = 16
)

// LEAD_SENTENCE_MIN_WIDTH 是引导句在换行引导行（Wrap）里参与「放不放得下」计算时假设的
// 宽度：窄于此才认为放不下、把按钮换到下一行；放得下时句子再伸展吃掉余量。
const LEAD_SENTENCE_MIN_WIDTH = 120

// CTA_HEIGHT / CTA_ROW_MARGIN 是主行动行的节奏：钮高 44、上距 24（压缩台定案，全页面统一）。
const (
	CTA_HEIGHT     = 44
	CTA_ROW_MARGIN = 24
)

// pageColumn 页面统一容器：左对齐、统一水平呼吸、最大行宽。
// Padding 依 CSS 序（上,右,下,左）——v2 曾写 Padding(0,40,0,0) 语义错位成
// 「右 40 左 0」，整个内容区贴死侧栏，即是本轮重排的导火索。
func pageColumn(c *ui.Context) ui.Element {
	return ui.Column(c).MaxWidth(640).Padding(contentPadTop, contentPadX, contentPadBtm, contentPadX).Gap(0)
}

// pageTitle 右半边 head 的页面标题（与侧栏导航同名）。
func pageTitle(p string) string {
	switch p {
	case pageExtract:
		return "解压台"
	case pageArchive:
		return "归档浏览"
	case pageBurn:
		return "刻录光盘"
	case pageEncrypt:
		return "加密"
	case pageDecrypt:
		return "解密"
	case pageKeys:
		return "生成密钥"
	case pageBase64:
		return "Base64"
	case pageMedia:
		return "视频转换"
	case pageImage:
		return "图片转换"
	case pagePdfMerge:
		return "PDF 合并"
	case pagePdfExtract:
		return "PDF 提取页"
	case pageMediaCmp:
		return "视频压缩"
	case pageAudioCvt:
		return "音频转换"
	case pageAudioCmp:
		return "音频压缩"
	case pageImageCmp:
		return "图片压缩"
	case pageImageNew:
		return "新建图片"
	case pageOffice:
		return "Office 转换"
	case pagePdfRotate:
		return "PDF 旋转"
	case pagePdfResize:
		return "页面尺寸"
	case pageHash:
		return "文件哈希"
	case pageRandom:
		return "随机生成"
	case pageTasks:
		return "处理台"
	case pageSettings:
		return "设置"
	}
	return "压缩台"
}

// browseFiles 弹出 Finder 文件选择框（多选），结果并入压缩清单。
// 对话框必须从 goroutine 发起（官方示例范式）——在帧构建闭包里同步调用
// 会把主 goroutine 卡进 await 的嵌套事件泵，帧构建重入后选择面板失灵。
func (a *app) browseFiles() {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: window, Title: "选择要压缩的文件", Multiple: true,
		})
		if err != nil {
			log.Println("browse:", err)
			return
		}
		if len(paths) == 0 {
			return
		}
		mygo.RunOnMain(func() {
			a.addInputs(paths)
			window.Invalidate()
		})
	}()
}

// browseOutDir 选择保存目录（对话框 Directory 模式）——必须在 goroutine
// 里发起：Dialog.Open 阻塞等待选窗，帧构建闭包里同步调会把主 goroutine
// 卡进嵌套事件泵、面板失灵（browseFiles 同款教训）。
func (a *app) browseOutDir() {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: window, Title: "选择保存位置", Directory: true,
		})
		if err != nil {
			log.Println("browse dir:", err)
			return
		}
		if len(paths) == 0 || paths[0] == "" {
			return
		}
		mygo.RunOnMain(func() {
			a.outDir = paths[0]
			a.outDirDirty = true
			window.Invalidate()
		})
	}()
}

// browseArchive 弹出 Finder 选择框（单选归档）——同样必须 goroutine 发起
// （Dialog.Open 同步调=嵌套事件泵，browseFiles 同款教训）。
func (a *app) browseArchive() {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: window, Title: "选择压缩包",
		})
		if err != nil {
			log.Println("browse archive:", err)
			return
		}
		if len(paths) == 0 || paths[0] == "" {
			return
		}
		mygo.RunOnMain(func() {
			a.setExtractArchive(paths[0])
			window.Invalidate()
		})
	}()
}

// setExtractArchive 选定归档并触发异步体检：格式/文件数/是否加密（zpack info
// 信封回读）+ 分卷集探测（GUI 侧数 .001/.002/… 兄弟文件）。换归档即重置
// 密码/解压位置（上一档的密码不留给下一档）。
func (a *app) setExtractArchive(path string) {
	path = strings.TrimSuffix(path, "/")
	if path == "" || path == "/" {
		return
	}
	a.extractPath = path
	a.extractInfo = nil
	a.extractDir = ""
	a.extractDirDirty = false
	a.extractPw = ""
	a.win.Invalidate()
	a.service.Inspect(path, func(result InspectResult) {
		mygo.RunOnMain(func() {
			if a.extractPath == path { // 体检回来前换了归档则丢弃过期结果
				a.extractInfo = &result
				a.toastEngineVerdict(filepath.Base(path), result.Err) // 引擎判格浮出（m00961）
				a.win.Invalidate()
			}
		})
	})
}

// browseExtractDir 选择解压目录（Directory 模式）——goroutine 发起
// （Dialog.Open 同步调=嵌套事件泵，同族教训）。
func (a *app) browseExtractDir() {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: window, Title: "选择解压位置", Directory: true,
		})
		if err != nil {
			log.Println("browse extract dir:", err)
			return
		}
		if len(paths) == 0 || paths[0] == "" {
			return
		}
		mygo.RunOnMain(func() {
			a.extractDir = paths[0]
			a.extractDirDirty = true
			window.Invalidate()
		})
	}()
}

// clearExtract 清空解压台选档（尾槽移除钮/取消解压共用）。
// redraw 请求重绘；没有窗口的建帧冒烟（无头测试）里静默跳过。
func (a *app) redraw() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *app) clearExtract() {
	a.extractPath = ""
	a.extractInfo = nil
	a.extractDir = ""
	a.extractDirDirty = false
	a.extractPw = ""
	a.extractShowPw = false
}

// heroPage 空态页判定：这些页面无内容时以垂直居中 hero 呈现。
func (a *app) heroPage() bool {
	switch a.page {
	case pageTasks:
		return len(a.tasks) == 0
	case pageExtract:
		return a.extractPath == ""
	case pageMedia:
		return a.mediaPath == ""
	case pageImage:
		return a.imagePath == ""
	case pagePdfMerge:
		return len(a.pdfMergeInputs) == 0
	case pagePdfExtract:
		return a.pdfExtractPath == ""
	case pageMediaCmp:
		return a.mediaCmpPath == ""
	case pageAudioCvt:
		return a.audioCvtPath == ""
	case pageAudioCmp:
		return a.audioCmpPath == ""
	case pageImageCmp:
		return len(a.imageCmpInputs) == 0
	case pageOffice:
		return a.officePath == ""
	case pagePdfRotate:
		return a.pdfRotatePath == ""
	case pagePdfResize:
		return a.pdfResizePath == ""
	case pageHash:
		return len(a.hashInputs) == 0
	case pageArchive:
		return a.archivePath == ""
	case pageBurn:
		return len(a.burnInputs) == 0
	}
	return a.page == pageCompress && len(a.inputs) == 0
}

// heroPane 空态 hero 分发（Scroll 撑不满高度，居中须无滚动容器承载）。
func (a *app) heroPane(c *ui.Context) {
	t := c.Theme()
	switch a.page {
	case pageMedia:
		hero(c, "拖入视频开始转换", "MP4 · MKV · MOV · WebM；能直接封装就不重编码", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseMedia()
			}
		}, t)
	case pageImage:
		hero(c, "拖入图片开始转换", "PNG · JPG · WebP · BMP · TIFF · GIF 互转", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseImage()
			}
		}, t)
	case pagePdfMerge:
		hero(c, "拖入 PDF 开始合并", "多个 PDF 按清单顺序合成一个文件", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browsePdfMerge()
			}
		}, t)
	case pagePdfExtract:
		hero(c, "拖入 PDF 提取页面", "页码区间如 1-3,5,8-，提取成新 PDF", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browsePdfExtract()
			}
		}, t)
	case pageMediaCmp:
		hero(c, "拖入视频开始压缩", "压到目标体积：50% · 100MB · 25MB（Gmail）或自定义", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseMediaCmp()
			}
		}, t)
	case pageAudioCvt:
		hero(c, "拖入音频开始转换", "MP3 · M4A · FLAC · WAV · OGG · OPUS；视频可抽音轨", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseAudioCvt()
			}
		}, t)
	case pageAudioCmp:
		hero(c, "拖入音频开始压缩", "缺省压到原码率一半；可指定码率与单声道", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseAudioCmp()
			}
		}, t)
	case pageImageCmp:
		hero(c, "拖入图片开始压缩", "压到目标体积；仅 JPG 吃质量参数，其余无损格式按需转换", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseImageCmp()
			}
		}, t)
	case pageOffice:
		hero(c, "拖入文档开始转换", "DOC/XLS/PPT（含 WPS 家族）→ DOCX/XLSX/PPTX", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseOffice()
			}
		}, t)
	case pagePdfRotate:
		hero(c, "拖入 PDF 开始旋转", "零损写方向元数据；可选页码区间", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browsePdfRotate()
			}
		}, t)
	case pagePdfResize:
		hero(c, "拖入 PDF 统一页面", "按纸型或自定义尺寸统一；文字页会体检拦截", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browsePdfResize()
			}
		}, t)
	case pageHash:
		hero(c, "拖入文件计算哈希", "MD5 · SHA1 · SHA256 · SHA384 · SHA512 · CRC32；支持校验", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseHash()
			}
		}, t)
	case pageArchive:
		hero(c, "拖入归档查看内容", "不解压浏览归档清单；可校验完整性", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseArchive2()
			}
		}, t)
	case pageBurn:
		hero(c, "拖入镜像或文件夹刻录", "CD · DVD · 蓝光；默认可追加", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseBurnFiles()
			}
		}, t)
	case pageTasks:
		hero(c, "还没有任务", "从压缩台开始，或直接把文件拖进窗口", nil, t)
	case pageExtract:
		hero(c, "拖入压缩包开始解压", "自动识别格式与分卷：ZIP · 7Z · RAR · TAR 系 · ISO · CAB · WIM", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseArchive()
			}
		}, t)
	default:
		hero(c, "拖入文件或文件夹", "支持多选，默认保存在文件旁，同名自动加序号", func() {
			if ui.Button(c, "浏览选择…").Cursor(ui.CursorPointer).Clicked() {
				a.browseFiles()
			}
		}, t)
	}
}

// hero 单个空态邀请块：标题 + 副文案 +（可选动作按钮），垂直居中且
// 底部多留一口气（光学重心略高于几何中心是大标题版式的惯例）。
func hero(c *ui.Context, title, subtitle string, action func(), t *ui.Theme) {
	column := ui.Column(c).Grow(1).Justify(ui.Center).AlignItems(ui.Start).
		Padding(0, contentPadX, 56, contentPadX)
	column.Children(func() {
		ui.Text(c, title).FontSize(26).Font(fontMiSansVFFamily).Bold()
		ui.Text(c, subtitle).FontSize(13).TextColor(t.TextMuted).Margin(10, 0, 0, 0)
		if action != nil {
			ui.Row(c).Margin(28, 0, 0, 0).Children(action)
		}
	})
}

// ── 页面：压缩台（空态走 heroPane；有选=清单即主角）──
func (a *app) pageCompress(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	removal := -1 // 本帧点了的移除徽章；建帧结束后才落地（见函数尾）
	// 分卷选了「自定义」就填合法尺寸才放行：空着的话引擎当作不分卷，界面却说自定义
	volumeIssue := ""
	if a.volumeModeStr == "自定义" {
		volumeIssue = validateByteSize(a.volume, VOLUME_MIN_BYTES)
	}
	column.Children(func() {
		// 引导行（用户定案文案）：清单头行=状态句 + 整句按钮「点击此处浏览」
		//（弹 Finder 选择框 browseFiles()）——首行间距全 app 统一（m05764）
		ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已准备的压缩归档 "+itoa(len(a.inputs))+" 项共 "+humanSize(a.totalSize())+"，可拖拽加入或").
				FontSize(13).TextColor(t.TextMuted)
			leadBrowseButton(c, t, "浏览选择…", func() { a.browseFiles() })
		})

		// 已选清单：单卡片列表（用户定案）——拖拽排序换框架组件 ui.List
		//（ListState.Reorder）：变高行/落点预示/边缘滚动/Esc 取消全内建，
		// 手搓几何（让位/落位/趋势阈值）整体退役——变高行视觉错位的根治
		card := ui.Column(c).Margin(12, 0, 0, 0).Radius(10).Background(t.Surface).Border(1, t.Border)
		card.Children(func() {
			a.rows.Key = func(i int) any { return a.inputs[i] }
			a.rows.Reorder = func(rows []int, to int) {
				a.inputs = reorderInputs(a.inputs, rows, to)
				a.syncName()
			}
			a.rows.MoveAnimation = &ui.Smooth
			ui.List(c, &a.rows, len(a.inputs), func(idx int) {
				a.inputListRow(c, t, a.inputs[idx], idx, idx == len(a.inputs)-1, true, &removal)
			}).Grow(1).Shrink(0)
		})
		// ── 参数区：开放式两列行（用户定案：弃卡片+行间分隔线——直接
		// 铺在内容底上，纯留白分节奏；左列=主标+弱化副标，右列=控件）──
		paramCard := ui.Column(c).Margin(20, 0, 0, 0).Gap(4)
		paramCard.Children(func() {
			settingRow := func(first bool, label, hint string, control func()) {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(8, 0).Children(func() {
					lab := ui.Column(c).Width(84).Shrink(0).Gap(2)
					lab.Children(func() {
						ui.Text(c, label).FontSize(13)
						ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
					})
					control()
				})
			}
			// 顶端对齐变体（用户 m05428）：控件折行变高时标签贴顶，不跟着居中漂移
			settingRowTop := func(label, hint string, control func()) {
				ui.Row(c).Gap(12).AlignItems(ui.Start).Padding(8, 0).Children(func() {
					lab := ui.Column(c).Width(84).Shrink(0).Gap(2)
					lab.Children(func() {
						ui.Text(c, label).FontSize(13)
						ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
					})
					control()
				})
			}
			settingRow(true, "保存位置", "默认为文件旁", func() {
				planDir, _ := outputPlan(a.inputs, a.formatSpec())
				// 保存位置可编辑+可点选（用户定案）：与名字同款连体字段——
				// 左侧裸输入（空=默认）、右侧文件夹选择钮。超长路径换行：
				// 输入区用多行 TextAreaBase（容器高度自适应，3 行封顶内部
				// 滚动）；换行符全程剥除——路径里进换行就是坏路径
				ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
					MaxHeight(76).
					Background(t.Surface).Border(1, t.Border).Radius(8).
					Children(func() {
						// 空态默认路径（用户 m05307/m05331）：框架 placeholder 单行
						// 绘制不撑高。假绑定方案：绑定 ghost 变量=默认路径，真内容
						// 参与测高→字段随换行自然长高；ReadOnly+灰字=占位符观感
						// （可选中复制）；点击聚焦翻回真值绑定（同槽位焦点不丢），
						// 失焦且空再回 ghost
						bound := &a.outDir
						if a.outDir == "" && !a.outDirFocus {
							a.outDirGhost = planDir + "（默认）"
							bound = &a.outDirGhost
						}
						outTa := ui.TextAreaBase(c, bound).
							Grow(1).FontSize(13).Lines(1, 3).
							Padding(5, 10)
						if bound != &a.outDir {
							outTa.ReadOnly(true).TextColor(t.TextMuted)
						}
						if outTa.Focused() {
							a.outDirFocus = true
						} else if a.outDir == "" {
							a.outDirFocus = false
						}
						if bound == &a.outDir && outTa.Changed() {
							a.outDir = strings.ReplaceAll(a.outDir, "\n", "")
							a.outDirDirty = a.outDir != ""
						}
						browse := pickButton(c, t)
						if browse.Clicked() {
							a.browseOutDir()
						}
					})
			})
			settingRow(false, "新压缩包名字", "后缀随格式", func() {
				// 名字+后缀连体字段（用户定案）：一个边框容器，左侧裸输入、
				// 右侧窄后缀下拉——像保存对话框的「文件名+扩展名」一体控件。
				// 超长名字换行（用户定案）：多行 TextAreaBase，缺省一行高
				//（32px），最多 3 行封顶内部滚动；换行符全程剥除
				ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
					MaxHeight(76).
					Background(t.Surface).Border(1, t.Border).Radius(8).
					Children(func() {
						if ui.TextAreaBase(c, &a.name).
							Placeholder(defaultName(a.inputs)+"（默认）").
							Grow(1).FontSize(13).Lines(1, 3).
							Padding(5, 10).Changed() {
							a.name = strings.ReplaceAll(a.name, "\n", "")
							a.nameDirty = a.name != "" && a.name != defaultName(a.inputs)
						}
						// 后缀下拉=SelectBase 自定义观感（用户定案：右侧 3px 呼吸位
						// +选项 hover 手型光标）——弹层机件与 ui.Select 同源，触发器
						// 与弹层全可控；声明须在输入框之后（踩过）；铁律：cursor+
						// background 缺一不可
						sel := ui.SelectBase(c, &a.formatStr)
						trig := sel.Trigger.
							Height(24).Padding(0, 6).Radius(6).MinWidth(72).Gap(4).
							AlignItems(ui.Center).Margin(0, 3, 0, 0).
							Background(ui.RGBA(0, 0, 0, 0)).Border(0, t.Border).
							Cursor(ui.CursorPointer)
						pressFeedback(trig, t)
						trig.Children(func() {
							ui.Text(c, a.formatStr).Font(fontMonoFamily + ", monospace").FontSize(13).
								TextColor(t.TextMuted).Grow(1)
							ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
						})
						sel.Popup(func(panel ui.Element) {
							panel.MinWidth(trig.Bounds().W).Padding(4).Radius(10).
								Background(t.Surface).Border(1, t.Border)
							for _, suffix := range suffixOptions {
								item := sel.Item(suffix).Height(30).Padding(0, 10).Radius(6).
									Cursor(ui.CursorPointer) // 选项 hover 手型光标（用户定案）
								switch {
								case item.Highlighted():
									item.Background(t.Accent).TextColor(t.AccentText)
								case suffix == a.formatStr:
									item.Background(t.SurfaceHover)
								}
								item.Children(func() {
									ui.Text(c, suffix).Font(fontMonoFamily + ", monospace").FontSize(13)
								})
							}
						})
					})
			})
			// 完成后策略（用户定案 m03442）：无动作/打开文件夹
			settingRow(false, "完成后", "任务成功的动作", func() {
				a.renderDonePolicy(c, t)
			})

			// ── 高级设置（用户定案 m03583）：默认折叠——密码/分卷/级别收拢于此 ──
			advancedActive := 0
			if a.pwModeStr == "加密" {
				advancedActive++
			}
			if a.volumeModeStr != "不分卷" {
				advancedActive++
			}
			if a.level != 6 {
				advancedActive++
			}
			// 头行不解释内容（用户定案 m04422）：仅折叠且有生效项时给计数
			advHint := ""
			if !a.advancedOpen && advancedActive > 0 {
				advHint = "已启用 " + itoa(advancedActive) + " 项"
			}
			advancedRows := func() {
				// 添加密码（用户定案 v2）：与分卷压缩同款「右选项」范式——
				// 模式下拉（无密码/加密）+ 加密态出输入框与眼睛钮；
				// ZIP 与 7Z 支持加密（7z 连文件名一起加密），选其他格式时整行自然消失
				if a.formatStr == ".zip" || a.formatStr == ".7z" {
					settingRow(false, "添加密码", "保护压缩包内容", func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Grow(1).Children(func() {
							pwSel := ui.SelectBase(c, &a.pwModeStr)
							pwTrig := pwSel.Trigger.
								Height(32).MinWidth(96).Radius(8).Padding(0, 10).Gap(6).
								AlignItems(ui.Center).Shrink(0).
								Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
							pressFeedback(pwTrig, t)
							pwTrig.Children(func() {
								ui.Text(c, a.pwModeStr).FontSize(13).Grow(1)
								ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
							})
							pwSel.Popup(func(panel ui.Element) {
								panel.MinWidth(96).Padding(4).Radius(10).
									Background(t.Surface).Border(1, t.Border)
								for _, mode := range []string{"无密码", "加密"} {
									item := pwSel.Item(mode).Height(30).Padding(0, 10).Radius(6).
										Cursor(ui.CursorPointer)
									switch {
									case item.Highlighted():
										item.Background(t.Accent).TextColor(t.AccentText)
									case a.pwModeStr == mode:
										item.Background(t.SurfaceHover)
									}
									finalMode := mode
									item.Children(func() {
										ui.Text(c, finalMode).FontSize(13)
									})
								}
							})
							if a.pwModeStr == "加密" {
								ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
									Background(t.Surface).Border(1, t.Border).Radius(8).Children(func() {
									pwInput := ui.TextInputBase(c, &a.password).
										Placeholder("输入密码").Grow(1).FontSize(13).
										Padding(5, 10)
									if !a.showPassword {
										pwInput.Password() // 逐帧条件调用：停止调用即明文（eye 范式）
									}
									eye := ui.ButtonBase(c).Height(24).Width(24).Radius(6).
										Margin(0, 3, 0, 0).
										Background(ui.RGBA(0, 0, 0, 0)).Cursor(ui.CursorPointer)
									if eye.Pressed() {
										eye.Background(t.TextMuted.Alpha(0.15))
									} else if eye.Hovered() {
										eye.Background(t.SurfaceHover)
									}
									eyeIcon := iconEye
									if a.showPassword {
										eyeIcon = iconEyeOff
									}
									eye.Children(func() {
										ui.Icon(c, eyeIcon).Size(13, 13).TextColor(t.TextMuted)
									})
									if eye.Clicked() {
										a.showPassword = !a.showPassword
									}
								})
							}
						})
					})
				}

				settingRowTop("分卷压缩", "切分成多个小包", func() {
					// 单一下拉（用户定案）：不分卷(顶) → 各预置 → 自定义(底)；
					// 仅「自定义」态出可编辑输入。容器行 Grow(1) 吃满右列：
					// 下拉恒定内容宽，自定义输入随窗口伸缩（用户定案 m03218）。
					// 后果提示独立成行铺在下方（用户 m05378）：整个文案左对齐
					// 自由折行，不再挤在行内右侧半宽折断
					ui.Column(c).Grow(1).Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Grow(1).Wrap().Children(func() {
							volSel := ui.SelectBase(c, &a.volumeModeStr)
							trig := volSel.Trigger.
								Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
								AlignItems(ui.Center).Shrink(0).
								Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
							pressFeedback(trig, t)
							trig.Children(func() {
								trigText := a.volumeModeStr
								if label, isPreset := volumePresetLabel(a.volumeModeStr); isPreset {
									trigText = label
								}
								ui.Text(c, trigText).FontSize(13).Grow(1)
								ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
							})
							volSel.Popup(func(panel ui.Element) {
								panel.MinWidth(150).Padding(4).Radius(10).
									Background(t.Surface).Border(1, t.Border)
								// 顶部：不分卷；中部：各预置；底部：自定义（用户定案顺序）
								options := append([]string{"不分卷"}, presetValues()...)
								options = append(options, "自定义")
								for _, option := range options {
									itemText := option
									if label, isPreset := volumePresetLabel(option); isPreset {
										itemText = label
									}
									item := volSel.Item(option).Height(30).Padding(0, 10).Radius(6).
										Cursor(ui.CursorPointer)
									switch {
									case item.Highlighted():
										item.Background(t.Accent).TextColor(t.AccentText)
									case a.volumeModeStr == option:
										item.Background(t.SurfaceHover)
									}
									finalText := itemText
									item.Children(func() {
										ui.Text(c, finalText).FontSize(13)
									})
								}
							})
							if a.volumeModeStr == "自定义" {
								ui.TextInputBase(c, &a.volume).
									Placeholder("如 300MB / 2GB").Grow(1).MinWidth(120).FontSize(13).
									Padding(5, 10).
									Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
							}
							// 分卷后果提示（用户定案 m03224）：说明当前选择对产物的影响
							volumeHint := ""
							switch {
							case a.volumeModeStr == "不分卷":
								volumeHint = "仅产生 1 个压缩包"
							default:
								if label, isPreset := volumePresetLabel(a.volumeModeStr); isPreset {
									if _, sizePart, found := strings.Cut(label, "· "); found {
										volumeHint = "超过 " + sizePart + " 自动切为多卷（.001、.002…）"
									}
								}
							}
							switch {
							case volumeIssue != "":
								ui.Text(c, volumeIssue).FontSize(12).TextColor(t.Danger).
									Wrap().Margin(4, 0, 0, 0)
							case volumeHint != "":
								ui.Text(c, volumeHint).FontSize(12).TextColor(t.TextMuted).
									Wrap().Margin(4, 0, 0, 0)
							}
						})
					})
				})
			}
			// 高级设置卡片（用户定案 m04388）：可折叠的卡片而非插在中间的行——
			// 白底描边圆角卡（与文件清单卡同族），头行整行可点开合，
			// 展开后收 压缩级别/添加密码/分卷压缩；折叠时提示「已启用 N 项」
			// 折叠态低调（用户定案 m04422/m04430）：灰描边勾勒轮廓、无白底；
			// 展开才现白卡承载内容。描边恒在，白底仅展开态
			advCard := ui.Column(c).Margin(10, 0, 0, 0).Radius(10).Border(1, t.Border)
			if a.advancedOpen {
				advCard.Background(t.Surface)
			}
			advCard.Children(func() {
				head := ui.ButtonBase(c).Shrink(0).Padding(10, 14).Cursor(ui.CursorPointer)
				// hover 底色圆角随形态：折叠=四角全圆（独占整卡），
				// 展开=仅上圆角（下缘与分隔线齐平，用户定案 m04441）
				if a.advancedOpen {
					head.Radius(10, 10, 0, 0)
				} else {
					head.Radius(10)
				}
				pressFeedback(head, t)
				head.Children(func() {
					ui.Row(c).Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, "高级设置").FontSize(13)
						if advHint != "" {
							ui.Text(c, advHint).FontSize(12).TextColor(t.TextMuted)
						}
					})
					openTarget := float32(0)
					if a.advancedOpen {
						openTarget = 180
					}
					ui.Icon(c, iconChevronDown).Size(10, 10).TextColor(t.TextMuted).
						Rotate(head.Animate("advchev", openTarget, 140*time.Millisecond))
				})
				if head.Clicked() {
					a.advancedOpen = !a.advancedOpen
				}
				if a.advancedOpen {
					// 头行与内容间的分隔线（撑满卡片宽）
					ui.Box(c).Height(1).Grow(1).Background(t.Border)
					body := ui.Column(c).Padding(6, 14, 10, 14)
					body.Children(func() {
						// 压缩级别（回归：6d716b8 撤出主区后按用户 m03583 收进高级区）
						settingRow(false, "压缩级别", "0 最快 · 9 最小", func() {
							ui.Row(c).Gap(10).AlignItems(ui.Center).Grow(1).Children(func() {
								// 自绘滑杆（用户定案 m04505）：默认白滑块头在浅色卡上
								// 不显眼——白头加描边+投影+按住放大 15%；品牌蓝
								// 已行进度；Step(1) 整数吸附且无刻度线
								lvl := ui.SliderBase(c, &a.level, 0, 9).Step(1).
									Grow(1).Height(20).FocusRing(false)
								held := lvl.Animate("lvlheld",
									map[bool]float32{true: 1, false: 0}[lvl.Pressed()],
									140*time.Millisecond)
								// 观感对齐 macOS 26 原生 NSSlider（System Settings
								// Sound 实测，物理像素/2）：轨道 6pt 胶囊；滑块头
								// 20×16pt 圆角矩形 r=7（宽>高的「药丸」非正圆，
								// 用户 m04532 指正）+柔和弥散投影；自绘=三平台像素级一致
								lvl.Draw(func(p *ui.Painter, r ui.Rect) {
									const knobW = float32(20)
									const knobH = float32(16)
									const knobR = float32(7)
									const trH = float32(6)
									track := ui.Rect{X: r.X + knobW/2, Y: r.Y + r.H/2 - trH/2, W: r.W - knobW, H: trH}
									frac := float32(a.level) / 9
									p.Fill(track, t.Border.Mix(t.Text, 0.12), trH/2)
									if frac > 0 {
										p.Fill(ui.Rect{X: track.X, Y: track.Y,
											W: max(trH, frac*track.W), H: trH}, t.Accent, trH/2)
									}
									kx := track.X + frac*track.W
									g := 1 + 0.12*held
									kw, kh := knobW*g, knobH*g
									k := ui.Rect{X: kx - kw/2, Y: r.Y + r.H/2 - kh/2, W: kw, H: kh}
									// 投影只在按住/拖动时出现（用户 m04620：静止无影，激活才有）
									p.Shadow(k, knobR, 0, 2, 5, 0, ui.RGBA(15, 23, 42, 0.24*held))
									p.Fill(k, ui.RGB(255, 255, 255), knobR)
									p.Stroke(k, t.Border.Mix(t.Text, 0.25), knobR, 1)
								})
								ui.Text(c, itoa(int(a.level))).Font(fontMonoFamily + ", monospace").FontSize(13).
									TextColor(t.TextMuted).Width(14)
							})
						})
						advancedRows()
					})
				}
			})
		})

		// CTA：创建压缩包（用户定案：按钮说动作不说数量）
		// 底部动作行：取消（清空清单——用户定案）+ 主 CTA
		ctaRow(c).Children(func() {
			cancelButton(c, t, "取消压缩", func() {
				a.inputs = nil
				a.nameDirty = false
				a.name = ""
				a.outDirDirty = false
				a.outDir = ""
				a.password = ""
				a.pwModeStr = "无密码"
				a.volumeModeStr = "不分卷"
				a.volume = ""
			})
			if primaryCTA(c, t, "创建压缩包", volumeIssue == "").Clicked() {
				// 分卷实参：预置=下拉值直传；自定义=手输；不分卷=空
				volArg := ""
				if a.volumeModeStr == "自定义" {
					volArg = strings.TrimSpace(a.volume)
				} else if _, isPreset := volumePresetLabel(a.volumeModeStr); isPreset {
					volArg = a.volumeModeStr
				}
				passArg := ""
				if (a.formatStr == ".zip" || a.formatStr == ".7z") && a.pwModeStr == "加密" {
					passArg = a.password
				}
				a.launchTask(a.service.StartCompress(a.inputs, a.formatSpec(), int(a.level), passArg, volArg, a.name, a.outDir))
			}
		})
	})
	// 移除在建帧之后落地：同一帧里参数区还要读首输入（outputPlan），先删后读会在
	// 删掉最后一项时越界崩溃（2026-11-23 无头实测复现）
	if removal >= 0 {
		a.inputs = append(a.inputs[:removal], a.inputs[removal+1:]...)
		a.syncName()
		a.redraw()
	}
}

// ── 页面：解压台（空态走 heroPane；拖入/选定归档→体检→解压）──
func (a *app) pageExtract(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		// 引导行（与压缩台同款句式，首行间距全 app 统一）
		ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已选定归档 "+filepath.Base(a.extractPath)+"，可拖入替换，或").
				FontSize(13).TextColor(t.TextMuted)
			leadBrowseButton(c, t, "浏览选择…", func() { a.browseArchive() })
		})

		// 归档卡（复制压缩清单卡观感）：图标+完整路径+尾槽（尺寸/分卷数，
		// hover 时移除徽章浮现——零占用尾槽范式）
		removeArchive := false
		card := ui.Column(c).Margin(12, 0, 0, 0).Radius(10).
			Background(t.Surface).Border(1, t.Border)
		card.Children(func() {
			row := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(9, 12)
			if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			row.Children(func() {
				entry := a.entryInfoOf(a.extractPath)
				archiveIcon := iconFile
				if entry.dir {
					archiveIcon = iconFolder
				}
				ui.Image(c, archiveIcon).Size(14, 14)
				pathCell(c, a.extractPath)
				shown := row.Hovered()
				slot := ui.Row(c).Shrink(0).AlignItems(ui.Center).Height(24)
				slot.Children(func() {
					sizeLabel := humanSize(entry.size)
					if a.extractInfo != nil && a.extractInfo.Volumes > 1 {
						sizeLabel = "共 " + itoa(a.extractInfo.Volumes) + " 卷"
					}
					numTarget := float32(1)
					if shown {
						numTarget = 0
					}
					sizeText := ui.Text(c, sizeLabel).Font(fontMonoFamily + ", monospace").FontSize(SIZE_LABEL_FONT_SIZE).
						TextColor(t.TextMuted)
					sizeText.Opacity(slot.Animate("num", numTarget, 110*time.Millisecond))

					del := ui.ButtonBase(c).Absolute().Right(0).Size(24, 24).Radius(6).Center().
						Cursor(ui.CursorPointer).
						Background(ui.RGBA(0, 0, 0, 0))
					hover := del.Hovered()
					down := del.Pressed()
					del.Opacity(del.Animate("vis", revealTarget(shown), 110*time.Millisecond))
					slideTarget := float32(5)
					if shown {
						slideTarget = 0
					}
					sinkTarget := float32(0)
					sinkDur := 120 * time.Millisecond
					if down {
						sinkTarget = 1
						sinkDur = 90 * time.Millisecond
					}
					del.Translate(del.Animate("slide", slideTarget, 110*time.Millisecond),
						del.Animate("sink", sinkTarget, sinkDur))
					del.Children(func() {
						ix := ui.Image(c, iconRemoveBadge).Size(14, 14)
						scaleTarget := float32(1)
						if hover {
							scaleTarget = 1.3
						}
						scale := del.Animate("scale", scaleTarget, 140*time.Millisecond)
						ix.Scale(scale, scale)
					})
					if del.Clicked() {
						removeArchive = true
					}
				})
			})
		})
		if removeArchive {
			a.clearExtract()
		}

		// 体检行：引擎 info 信封（格式/文件数/解压后体积）+ 分卷集 + 加密探测。
		// info 失败不阻断解压（分卷集等场景引擎 extract 仍可能吃进）
		probeText := "识别中…"
		if a.extractInfo != nil {
			if a.extractInfo.Err != "" {
				probeText = a.extractInfo.Err + "，仍可尝试解压"
			} else {
				probeText = strings.ToUpper(a.extractInfo.Format) + " · " +
					itoa(a.extractInfo.FileCount) + " 个文件 · 解压后 " +
					humanSize(a.extractInfo.TotalBytes)
				if a.extractInfo.Volumes > 1 {
					probeText += " · 共 " + itoa(a.extractInfo.Volumes) + " 卷"
				}
				if a.extractInfo.Encrypted {
					probeText += " · 已加密"
				}
			}
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Margin(10, 0, 0, 0).Children(func() {
			ui.Text(c, probeText).FontSize(13).TextColor(t.TextMuted)
		})

		// ── 参数区：与压缩台同款开放式两列行 ──
		paramCard := ui.Column(c).Margin(20, 0, 0, 0).Gap(4)
		paramCard.Children(func() {
			settingRow := func(first bool, label, hint string, control func()) {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(8, 0).Children(func() {
					lab := ui.Column(c).Width(84).Shrink(0).Gap(2)
					lab.Children(func() {
						ui.Text(c, label).FontSize(13)
						ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
					})
					control()
				})
			}
			// 解压位置：默认归档旁（引擎智能解压语义：单根直落、散装包一层）
			settingRow(true, "解压位置", "默认为归档旁", func() {
				planDir := filepath.Dir(a.extractPath)
				ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
					MaxHeight(76).
					Background(t.Surface).Border(1, t.Border).Radius(8).
					Children(func() {
						// 空态默认路径假绑定（同保存位置，用户 m05307/m05331）
						bound := &a.extractDir
						if a.extractDir == "" && !a.extractDirFocus {
							a.extractDirGhost = planDir + "（默认）"
							bound = &a.extractDirGhost
						}
						dirTa := ui.TextAreaBase(c, bound).
							Grow(1).FontSize(13).Lines(1, 3).
							Padding(5, 10)
						if bound != &a.extractDir {
							dirTa.ReadOnly(true).TextColor(t.TextMuted)
						}
						if dirTa.Focused() {
							a.extractDirFocus = true
						} else if a.extractDir == "" {
							a.extractDirFocus = false
						}
						if bound == &a.extractDir && dirTa.Changed() {
							a.extractDir = strings.ReplaceAll(a.extractDir, "\n", "")
							a.extractDirDirty = a.extractDir != ""
						}
						browse := pickButton(c, t)
						if browse.Clicked() {
							a.browseExtractDir()
						}
					})
			})
			// 解压密码常驻：此前仅体检判定「已加密」才出，而引擎对 7z/RAR 不报加密
			// （体检失败的归档更是整行不出）——加密的 7z/RAR 在这里无路可走（2026-11-23 审计）。
			// 未加密时留空即可，提示语按体检结果区分。
			pwHint := "仅加密归档需要"
			if a.extractInfo != nil && a.extractInfo.Err == "" && a.extractInfo.Encrypted {
				pwHint = "归档已加密"
			}
			settingRow(false, "解压密码", pwHint, func() {
				ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
					Background(t.Surface).Border(1, t.Border).Radius(8).Children(func() {
					pwInput := ui.TextInputBase(c, &a.extractPw).
						Placeholder("输入密码").Grow(1).FontSize(13).
						Padding(5, 10)
					if !a.extractShowPw {
						pwInput.Password() // 逐帧条件调用：停止调用即明文（eye 范式）
					}
					eye := ui.ButtonBase(c).Height(24).Width(24).Radius(6).
						Margin(0, 3, 0, 0).
						Background(ui.RGBA(0, 0, 0, 0)).Cursor(ui.CursorPointer)
					if eye.Pressed() {
						eye.Background(t.TextMuted.Alpha(0.15))
					} else if eye.Hovered() {
						eye.Background(t.SurfaceHover)
					}
					eyeIcon := iconEye
					if a.extractShowPw {
						eyeIcon = iconEyeOff
					}
					eye.Children(func() {
						ui.Icon(c, eyeIcon).Size(13, 13).TextColor(t.TextMuted)
					})
					if eye.Clicked() {
						a.extractShowPw = !a.extractShowPw
					}
				})
			})

			// 完成后策略（用户定案 m03442）：无动作/打开文件夹
			settingRow(false, "完成后", "任务成功的动作", func() {
				a.renderDonePolicy(c, t)
			})
		})

		// 底部动作行：取消（清空选档）+ 主 CTA 解压
		ctaRow(c).Children(func() {
			cancelButton(c, t, "取消解压", a.clearExtract)
			if primaryCTA(c, t, "解压", true).Clicked() {
				outDir := strings.TrimSpace(strings.ReplaceAll(a.extractDir, "\n", ""))
				if outDir == "" {
					outDir = filepath.Dir(a.extractPath) // 不依赖进程 CWD
				}
				a.launchTask(a.service.StartExtract(a.extractPath, a.extractPw, outDir))
			}
		})
	})
}

// ── 工具分组：加密 / 解密 / 密钥对 / Base64（用户 m04704 定案：加解密
// 单独一个侧栏分组；文本优先——Base64 不强制文件）──

// toolSegmented 自绘段选（TabsBase 范式，gallery「Tabs and panes」同款）：
// 浅灰轨道 + 选中白胶囊浮起（高亮），未选浅灰字（用户 m04750 定案）。
func toolSegmented(c *ui.Context, t *ui.Theme, selected *int, labels ...string) {
	tabs := ui.TabsBase(c, selected, len(labels))
	tabs.List.Padding(2).Radius(8).Background(t.SurfaceHover).Shrink(0).Children(func() {
		for idx, label := range labels {
			index, name := idx, label
			seg := tabs.Tab(index).Height(24).Padding(0, 12).Radius(6).
				Cursor(ui.CursorPointer)
			if index == *selected {
				seg.Background(t.Accent) // 选中=主按钮同色高亮（用户 m04864 定案）
			} else {
				if seg.Pressed() {
					seg.Background(t.Text.Alpha(0.10))
				} else if seg.Hovered() {
					seg.Background(t.Text.Alpha(0.06))
				}
			}
			seg.Children(func() {
				textColor := map[bool]ui.Color{true: t.AccentText, false: t.TextMuted}[index == *selected]
				ui.Text(c, name).FontSize(13).TextColor(textColor)
			})
		}
	})
}

// openSettingRow 开放式两列行（与压缩台/解压台同款 token：Padding(8,0)、左列 92）。
func openSettingRow(c *ui.Context, t *ui.Theme, label, hint string, control func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(8, 0).Children(func() {
		lab := ui.Column(c).Width(84).Shrink(0).Gap(2)
		lab.Children(func() {
			ui.Text(c, label).FontSize(13)
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
		})
		control()
	})
}

// openSettingRowTop 同 openSettingRow 但 label 与控件顶部对齐——多行控件
// （如压缩策略两行 radio）居中会把 label 吊在半空，顶对齐才是正位。
func openSettingRowTop(c *ui.Context, t *ui.Theme, label, hint string, control func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Start).Padding(8, 0).Children(func() {
		lab := ui.Column(c).Width(84).Shrink(0).Gap(2).Padding(5, 0, 0, 0)
		lab.Children(func() {
			ui.Text(c, label).FontSize(13)
			ui.Text(c, hint).FontSize(12).TextColor(t.TextMuted)
		})
		control()
	})
}

// toolFileField 白底描边圆角文件字段：文件名 + 尾部选择钮（两台同族观感）。
// 字段本身即拖放区（m05234 定案）：加密/解密页输入文件与钥匙各自成分区
// 拖放目标——拖到哪个字段就填哪个，拖悬高亮品牌蓝描边。
func (a *app) toolFileField(c *ui.Context, t *ui.Theme, path, placeholder string, onPick func(), onDrop func(string)) {
	// 字段行是设置行里的伸缩项：下限取 FIELD_MIN_WIDTH 而不是内容的「最小宽度」——路径里一段
	// 没有断点的长英文目录名会按最小宽度把字段行顶出控件列、把尾部选择钮挤到框外；
	// 列宽够时字段行钉在列宽内，路径文字在自己的盒子里换行；列比下限还窄（窗口很小）时
	// 才让字段行溢出，免得路径被挤成每行一个字
	field := ui.Row(c).Grow(1).MinWidth(FIELD_MIN_WIDTH).AlignItems(ui.Center).MinHeight(32).Gap(8).
		Background(t.Surface).Border(1, t.Border).Radius(8)
	over := field.FileDragOver()
	if over {
		field.Border(1, t.Accent).Background(t.Accent.Alpha(0.06))
	}
	field.Children(func() {
		shown := path // m06569 用户定案：显绝对路径（Grow 截断，占位符态仍用 placeholder）
		shownColor := t.Text
		if path == "" {
			shown = placeholder
			shownColor = t.TextMuted
		}
		// m07672 细节补齐（压缩台清单行同款）：文件/文件夹图标+尾部体积 mono
		isDir := false
		if path != "" {
			if st, statErr := os.Stat(path); statErr == nil && st.IsDir() {
				isDir = true
			}
		}
		// 左内边距 10px（对齐压缩台 TextArea Padding(5,10) 基准；图标态由图标扛，
		// 文字到图标的间距交给容器 Gap(8)，不再用 Padding 偏方补）
		if path != "" {
			rowIcon := iconFile
			if isDir {
				rowIcon = iconFolder
			}
			ui.Image(c, rowIcon).Size(14, 14).Margin(0, 0, 0, 10)
		}
		// 路径完整显示、放不下就换行（2026-11-23 用户定案）；尾部选择钮 Shrink(0) 钉宽，
		// 常规窗口宽度下路径再长也不会把它挤窄
		shownText := ui.Text(c, shown).FontSize(13).Grow(1).MinWidth(0).TextColor(shownColor)
		if path == "" {
			shownText.Padding(0, 0, 0, 10)
		}
		if path != "" {
			sizeLabel := humanSize(a.entryInfoOf(path).size)
			if isDir {
				sizeLabel = "文件夹"
				a.ensureDirSize(path)
			}
			ui.Text(c, sizeLabel).Font(fontMonoFamily + ", monospace").FontSize(SIZE_LABEL_FONT_SIZE).
				TextColor(t.TextMuted).Shrink(0)
		}
		pick := pickButton(c, t)
		if pick.Clicked() {
			onPick()
		}
	})
	if files := field.DroppedFiles(); len(files) > 0 {
		onDrop(files[0]) // 拖到哪个字段就填哪个（m05234 分区拖放）
	}
}

// pressedShade 是描边类控件「按下」时的底色：比悬停底色再深一档
// （SurfaceHover 向文字色调 8%，深浅色主题都成立）。
func pressedShade(t *ui.Theme) ui.Color {
	return t.SurfaceHover.Mix(t.Text, 0.08)
}

// pressFeedback 描边类控件（下拉触发器 / 行内小钮）的悬停与按下反馈：悬停浅底、按下加深。
// 交互铁律：凡可点击必带 hover + 按下反馈——「Pressed() || Hovered() 给同一底色」
// 等于没有按下态（2026-11-23 审计：全文件 13 处同写法）。
func pressFeedback(element ui.Element, t *ui.Theme) {
	if element.Pressed() {
		element.Background(pressedShade(t))
	} else if element.Hovered() {
		element.Background(t.SurfaceHover)
	}
}

// leadBrowseButton 引导行的描边小钮（「浏览选择…」）：悬停强调色描边 + 浅底，按下底色加深。
// 压缩台 / 解压台 / PDF 合并 / 哈希 / 刻录 / 归档浏览 / 转换族引导行共用。
func leadBrowseButton(c *ui.Context, t *ui.Theme, label string, onClick func()) {
	button := ui.ButtonBase(c).Height(24).Padding(0, 10).Radius(7).Border(1, t.Border).
		Cursor(ui.CursorPointer).Label(label)
	if button.Pressed() {
		button.Background(pressedShade(t)).Border(1, t.Accent)
	} else if button.Hovered() {
		button.Background(t.SurfaceHover).Border(1, t.Accent)
	}
	button.Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(t.Accent)
	})
	if button.Clicked() {
		onClick()
	}
}

// FIELD_MIN_WIDTH 是带尾部按钮的只读字段行的最小宽度：图标 + 一段可读的路径 + 体积 + 选择钮
// 放得下的下限；控件列比它还窄时字段行宁可溢出，也不把路径挤成每行一个字。
const FIELD_MIN_WIDTH = 240

// SIZE_LABEL_FONT_SIZE 是清单行 / 文件字段里体积标签的字号：次要信息，比正文小一号
// （2026-11-23 用户定案「文件尺寸字号要小一些」）。
const SIZE_LABEL_FONT_SIZE = 12

// PICK_BUTTON_INSET 是选择钮距字段右缘的内缩：与钮在字段里的上下留白（32−24）÷2 同值——
// 悬停色块四边离字段描边一样远。压缩台保存位置此前没有这 3–4px，色块贴着右边框
// （2026-11-23 用户反馈）；内缩收进小件，调用处不再各写各的。
const (
	PICK_BUTTON_INSET = 4 // 钮距字段右缘的内缩
	PICK_PADDING_X    = 8 // 钮自身的左右内边距
)

// pickButton 字段尾部的「选择」小钮（文件夹图标+「选择」，灰字透明底）：
// 悬停浅底、按下再深一档（pressFeedback 同一套反馈语汇）；Shrink(0) 钉死宽度——
// 路径再长也挤不出字段框（2026-11-23 反馈矩阵测出的长路径布局破损）。
func pickButton(c *ui.Context, t *ui.Theme) ui.Element {
	pick := ui.ButtonBase(c).Height(24).Shrink(0).Padding(0, PICK_PADDING_X).Margin(0, PICK_BUTTON_INSET, 0, 0).
		Radius(6).Gap(4).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).
		Background(ui.RGBA(0, 0, 0, 0)).Border(0, t.Border)
	pressFeedback(pick, t)
	pick.Children(func() {
		ui.Image(c, iconFolder).Size(13, 13)
		ui.Text(c, "选择").FontSize(13).TextColor(t.TextMuted)
	})
	return pick
}

// copyButton 小复制钮（点后写系统剪贴板并 toast）。悬停与按下各有反馈。
func copyButton(c *ui.Context, t *ui.Theme, text string) {
	btn := ui.ButtonBase(c).Height(26).Padding(0, 10).Radius(6).
		Border(1, t.Border).Cursor(ui.CursorPointer).Label("复制")
	pressFeedback(btn, t)
	if btn.Hovered() || btn.Pressed() {
		btn.Border(1, t.Accent)
	}
	btn.Children(func() {
		ui.Text(c, "复制").FontSize(13).TextColor(t.Accent)
	})
	if btn.Clicked() {
		mygo.Clipboard.WriteText(text)
		c.Toast("已复制")
	}
}

// toolResultRow 文件态结果行（成功=产物+Finder；失败=红字原因）。
func (a *app) toolResultRow(c *ui.Context, t *ui.Theme, done *ToolResult) {
	if done == nil {
		return
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(12, 0, 0, 0).Children(func() {
		if done.Err != "" {
			ui.Image(c, iconError).Size(14, 14)
			ui.Text(c, done.Err).FontSize(13).TextColor(t.Danger).Grow(1)
		} else {
			ui.Image(c, iconDone).Size(14, 14)
			summary := done.Output
			if done.Extra != "" {
				summary += "  ·  " + done.Extra
			}
			ui.Text(c, summary).FontSize(13).TextColor(t.TextMuted).Grow(1)
			show := ui.ButtonBase(c).Height(24).Padding(0, 8).
				Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Background(ui.RGBA(0, 0, 0, 0)).Border(1, t.Border)
			pressFeedback(show, t)
			show.Children(func() {
				ui.Text(c, revealLabel()).FontSize(13).TextColor(t.TextMuted)
			})
			if show.Clicked() {
				revealPath(done.Output) // 精确选中产物文件（非仅开目录）
			}
		}
	})
}

// ctaRow 主行动行：钮高 44、上距 24、钮间距 10（压缩台定案的节奏，全页面主按钮统一于此）。
func ctaRow(c *ui.Context) ui.Element {
	return ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(CTA_ROW_MARGIN, 0, 0, 0)
}

// primaryCTA 主按钮：框架 ui.PrimaryButton 自带 Accent / AccentHover / AccentPressed 换脸；
// enabled=false 时置灰并去掉手型（覆盖 Background 会关掉框架的换脸，所以只在置灰时覆盖）。
func primaryCTA(c *ui.Context, t *ui.Theme, label string, enabled bool) ui.Element {
	cta := ui.PrimaryButton(c, label).Height(CTA_HEIGHT).Grow(1).FontSize(13).Cursor(ui.CursorPointer)
	if !enabled {
		cta = cta.Background(t.SurfaceHover).TextColor(t.TextMuted).Cursor(0)
	}
	return cta
}

// settingControl 设置行的控件列：控件 + 其下的行内校验红字（issue 为空不占位）。
// 填错就地说明并禁用主按钮（2026-11-23 审计第三批；此前要跳到处理台才看到引擎报错）。
func settingControl(c *ui.Context, t *ui.Theme, issue string, control func()) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		control()
		if issue != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(4, 2, 0, 2).Children(func() {
				ui.Image(c, iconError).Size(13, 13)
				ui.Text(c, issue).FontSize(12).TextColor(t.Danger).Grow(1).SingleLine().Ellipsis("…").
					Tooltip(issue)
			})
		}
	})
}

// cancelButton 回退类动作钮（取消 / 清除）：描边灰字，悬停泛红、按下更红（压缩台同款）。
func cancelButton(c *ui.Context, t *ui.Theme, label string, onClick func()) ui.Element {
	cancel := ui.ButtonBase(c).Height(CTA_HEIGHT).Padding(0, 16).Radius(8).Border(1, t.Border).
		Cursor(ui.CursorPointer)
	switch {
	case cancel.Pressed():
		cancel.Background(t.Danger.Alpha(0.16)).Border(1, t.Danger)
	case cancel.Hovered():
		cancel.Background(t.Danger.Alpha(0.08)).Border(1, t.Danger)
	}
	cancel.Children(func() {
		text := ui.Text(c, label).FontSize(13).TextColor(t.TextMuted)
		if cancel.Hovered() || cancel.Pressed() {
			text.TextColor(t.Danger)
		}
	})
	if cancel.Clicked() {
		onClick()
	}
	return cancel
}

// toolCTA 工具页主 CTA（缺必填灰置；running 防重入时按钮旁显示「处理中…」）。
func toolCTA(c *ui.Context, t *ui.Theme, label string, ready, running bool, onRun func()) {
	ctaRow(c).Children(func() {
		if running {
			ui.Text(c, "处理中…").FontSize(13).TextColor(t.TextMuted).Grow(1)
		}
		cta := primaryCTA(c, t, label, ready && !running)
		if cta.Clicked() && ready && !running {
			onRun()
		}
	})
}

// toolBrowse 通用的文件/目录选择（goroutine 弹框，官方范式）：单选，回调拿到一个路径。
func (a *app) toolBrowse(title string, directory bool, onPick func(string)) {
	a.toolBrowseMany(title, directory, false, func(paths []string) { onPick(paths[0]) })
}

// toolBrowseMany 多选版：回调拿到全部选中路径。单文件字段与目录选择保持单选；
// 可累积清单的入口（文件哈希）用 multiple=true。
func (a *app) toolBrowseMany(title string, directory, multiple bool, onPick func(paths []string)) {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: window, Title: title, Directory: directory, Multiple: multiple,
		})
		if err != nil {
			log.Println("tool browse:", err)
			return
		}
		picked := paths[:0]
		for _, path := range paths {
			if path != "" {
				picked = append(picked, path)
			}
		}
		if len(picked) == 0 {
			return
		}
		mygo.RunOnMain(func() {
			onPick(picked)
			window.Invalidate()
		})
	}()
}

// encDefaultOut 加密文件态默认输出（输入旁 .enc）。
func encDefaultOut(input string) string {
	if input == "" {
		return ""
	}
	return input + ".enc"
}

// decDefaultOut 解密文件态默认输出（剥 .enc 后 .dec，否则直接 .dec）。
func decDefaultOut(input string) string {
	if input == "" {
		return ""
	}
	return strings.TrimSuffix(input, ".enc") + ".dec"
}

// pageEncrypt 加密页：公钥加密（文本 ⇄ 文件双态）。
func (a *app) pageEncrypt(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		ui.Row(c).Padding(14, 0, 4, 0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "公钥加密任意文件").FontSize(13).TextColor(t.TextMuted)
		})

		paramCard := ui.Column(c).Margin(12, 0, 0, 0).Gap(4)
		paramCard.Children(func() {
			openSettingRow(c, t, "输入方式", "文本或文件", func() {
				toolSegmented(c, t, &a.encModeIdx, "文本", "文件")
			})
			if a.encModeIdx == 0 {
				openSettingRow(c, t, "明文", "要加密的内容", func() {
					ui.TextAreaBase(c, &a.encText).
						Placeholder("输入要加密的内容").Lines(2, 6).Grow(1).FontSize(13).
						Padding(6, 10).Background(t.Surface).Border(1, t.Border).Radius(8)
				})
			} else {
				setEncFile := func(pick string) {
					a.encFile = pick
					a.encDone = nil
					if !a.encOutDirty {
						a.encOut = ""
					}
				}
				openSettingRow(c, t, "输入文件", "拖入或选择", func() {
					a.toolFileField(c, t, a.encFile, "未选择",
						func() { a.toolBrowse("选择输入文件", false, setEncFile) },
						setEncFile)
				})
				openSettingRow(c, t, "输出文件", "默认在输入旁", func() {
					if ui.TextInputBase(c, &a.encOut).
						Placeholder(encDefaultOut(a.encFile)+"（默认）").
						Grow(1).FontSize(13).Padding(5, 10).
						Background(t.Surface).Border(1, t.Border).Radius(8).
						MinHeight(32).Changed() {
						a.encOutDirty = strings.TrimSpace(a.encOut) != ""
					}
				})
			}
			setEncKey := func(pick string) {
				a.encKey = pick
				a.encDone = nil
			}
			openSettingRow(c, t, "公钥", "接收方的公钥", func() {
				a.toolFileField(c, t, a.encKey, "未选择",
					func() { a.toolBrowse("选择公钥文件", false, setEncKey) },
					setEncKey)
			})
		})

		// 文本态结果：密文（Base64）+ 复制
		if a.encModeIdx == 0 && (a.encTextOut != "" || a.encErr != "") {
			ui.Column(c).Margin(12, 0, 0, 0).Gap(6).Children(func() {
				if a.encErr != "" {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Image(c, iconError).Size(14, 14)
						ui.Text(c, a.encErr).FontSize(13).TextColor(t.Danger).Grow(1)
					})
				} else {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "密文（Base64）").FontSize(13)
						copyButton(c, t, a.encTextOut)
					})
					ui.TextAreaBase(c, &a.encTextOut).
						ReadOnly(true).Lines(2, 6).Grow(1).FontSize(13).
						Padding(6, 10).Background(t.Surface).Border(1, t.Border).Radius(8)
				}
			})
		}

		// 文件态结果行
		if a.encModeIdx == 1 {
			a.toolResultRow(c, t, a.encDone)
		}

		ready := a.encKey != "" && (a.encModeIdx == 0 && strings.TrimSpace(a.encText) != "" ||
			a.encModeIdx == 1 && a.encFile != "")
		toolCTA(c, t, "加密", ready, a.encRunning, func() {
			a.encRunning = true
			a.encErr = ""
			a.encTextOut = ""
			a.encDone = nil
			if a.encModeIdx == 0 {
				a.service.RunToolStdin([]string{"crypto", "encrypt", "--key-file", a.encKey}, a.encText, func(text, errMsg string) {
					mygo.RunOnMain(func() {
						a.encRunning = false
						a.encErr = errMsg
						a.encTextOut = text
						a.win.Invalidate()
					})
				})
			} else {
				out := strings.TrimSpace(a.encOut)
				if out == "" {
					out = encDefaultOut(a.encFile)
				}
				a.service.RunTool([]string{"crypto", "encrypt", "--key-file", a.encKey, "--file", a.encFile, "--output", out}, func(result ToolResult) {
					mygo.RunOnMain(func() {
						a.encRunning = false
						a.encDone = &result
						a.win.Invalidate()
					})
				})
			}
		})
	})
}

// pageDecrypt 解密页：私钥解密（文本 ⇄ 文件双态）。
func (a *app) pageDecrypt(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		ui.Row(c).Padding(14, 0, 4, 0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "私钥解密还原文件").FontSize(13).TextColor(t.TextMuted)
		})

		paramCard := ui.Column(c).Margin(12, 0, 0, 0).Gap(4)
		paramCard.Children(func() {
			openSettingRow(c, t, "输入方式", "文本或文件", func() {
				toolSegmented(c, t, &a.decModeIdx, "文本", "文件")
			})
			if a.decModeIdx == 0 {
				openSettingRow(c, t, "密文", "Base64 文本", func() {
					ui.TextAreaBase(c, &a.decText).
						Placeholder("粘贴 Base64 密文").Lines(2, 6).Grow(1).FontSize(13).
						Padding(6, 10).Background(t.Surface).Border(1, t.Border).Radius(8)
				})
			} else {
				setDecFile := func(pick string) {
					a.decFile = pick
					a.decDone = nil
					if !a.decOutDirty {
						a.decOut = ""
					}
				}
				openSettingRow(c, t, "密文文件", "拖入或选择", func() {
					a.toolFileField(c, t, a.decFile, "未选择",
						func() { a.toolBrowse("选择密文文件", false, setDecFile) },
						setDecFile)
				})
				openSettingRow(c, t, "输出文件", "默认在输入旁", func() {
					if ui.TextInputBase(c, &a.decOut).
						Placeholder(decDefaultOut(a.decFile)+"（默认）").
						Grow(1).FontSize(13).Padding(5, 10).
						Background(t.Surface).Border(1, t.Border).Radius(8).
						MinHeight(32).Changed() {
						a.decOutDirty = strings.TrimSpace(a.decOut) != ""
					}
				})
			}
			setDecKey := func(pick string) {
				a.decKey = pick
				a.decDone = nil
			}
			openSettingRow(c, t, "私钥", "你自己的私钥", func() {
				a.toolFileField(c, t, a.decKey, "未选择",
					func() { a.toolBrowse("选择私钥文件", false, setDecKey) },
					setDecKey)
			})
		})

		// 文本态结果：明文 + 复制
		if a.decModeIdx == 0 && (a.decTextOut != "" || a.decErr != "") {
			ui.Column(c).Margin(12, 0, 0, 0).Gap(6).Children(func() {
				if a.decErr != "" {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Image(c, iconError).Size(14, 14)
						ui.Text(c, a.decErr).FontSize(13).TextColor(t.Danger).Grow(1)
					})
				} else {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "明文").FontSize(13)
						copyButton(c, t, a.decTextOut)
					})
					ui.TextAreaBase(c, &a.decTextOut).
						ReadOnly(true).Lines(2, 6).Grow(1).FontSize(13).
						Padding(6, 10).Background(t.Surface).Border(1, t.Border).Radius(8)
				}
			})
		}

		// 文件态结果行
		if a.decModeIdx == 1 {
			a.toolResultRow(c, t, a.decDone)
		}

		ready := a.decKey != "" && (a.decModeIdx == 0 && strings.TrimSpace(a.decText) != "" ||
			a.decModeIdx == 1 && a.decFile != "")
		toolCTA(c, t, "解密", ready, a.decRunning, func() {
			a.decRunning = true
			a.decErr = ""
			a.decTextOut = ""
			a.decDone = nil
			if a.decModeIdx == 0 {
				a.service.RunToolStdin([]string{"crypto", "decrypt", "--key-file", a.decKey}, a.decText, func(text, errMsg string) {
					mygo.RunOnMain(func() {
						a.decRunning = false
						a.decErr = errMsg
						a.decTextOut = text
						a.win.Invalidate()
					})
				})
			} else {
				out := strings.TrimSpace(a.decOut)
				if out == "" {
					out = decDefaultOut(a.decFile)
				}
				a.service.RunTool([]string{"crypto", "decrypt", "--key-file", a.decKey, "--file", a.decFile, "--output", out}, func(result ToolResult) {
					mygo.RunOnMain(func() {
						a.decRunning = false
						a.decDone = &result
						a.win.Invalidate()
					})
				})
			}
		})
	})
}

// pageKeys 密钥对页：生成 RSA 密钥对。
// keyBitsOptions 是生成密钥的位数档（段选顺序即此顺序）。
var keyBitsOptions = []string{"1024", "2048", "4096"}

func (a *app) pageKeys(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		ui.Row(c).Padding(14, 0, 4, 0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "生成 RSA 公钥私钥对").FontSize(13).TextColor(t.TextMuted)
		})

		paramCard := ui.Column(c).Margin(12, 0, 0, 0).Gap(4)
		paramCard.Children(func() {
			openSettingRow(c, t, "密钥长度", "更安全也更慢", func() {
				toolSegmented(c, t, &a.keyBitsIdx, keyBitsOptions...)
			})
			openSettingRow(c, t, "名字", "文件名前缀", func() {
				ui.TextInputBase(c, &a.keyName).
					Placeholder("keypair（默认）").
					Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
			openSettingRow(c, t, "保存位置", "默认桌面", func() {
				ui.Row(c).Grow(1).MinWidth(FIELD_MIN_WIDTH).AlignItems(ui.Center).MinHeight(32).
					Background(t.Surface).Border(1, t.Border).Radius(8).Children(func() {
					dirShown := a.keyDir
					dirColor := t.Text
					if dirShown == "" {
						dirShown = "桌面"
						dirColor = t.TextMuted
					}
					ui.Text(c, dirShown).FontSize(13).Grow(1).MinWidth(0).
						TextColor(dirColor).Padding(0, 0, 0, 10)
					pick := pickButton(c, t)
					if pick.Clicked() {
						a.toolBrowse("选择保存位置", true, func(pick string) {
							a.keyDir = pick
						})
					}
				})
			})
		})

		// 专属结果卡：两把钥匙分行+各自显示钮（用户 m04889 议题③定案：
		// 只标私钥/公钥，不加分发提示）
		if a.keyDone != nil {
			ui.Column(c).Margin(12, 0, 0, 0).Background(t.Surface).
				Border(1, t.Border).Radius(10).Clip().Children(func() {
				if a.keyDone.Err != "" {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(10, 12).Children(func() {
						ui.Image(c, iconError).Size(14, 14)
						ui.Text(c, a.keyDone.Err).FontSize(13).TextColor(t.Danger).Grow(1)
					})
					return
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 12).
					BorderWidth(0, 0, 1, 0).Children(func() {
					ui.Image(c, iconDone).Size(14, 14)
					ui.Text(c, "已生成 "+a.keyDoneBits+" 位密钥对").
						FontSize(13)
				})
				keyRow := func(lock bool, label, fullPath string) {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 12).Children(func() {
						if lock {
							ui.Image(c, iconKeyPriv).Size(13, 13) // m05143：私钥=深红盾牌钥匙孔
						} else {
							ui.Image(c, iconKeyPub).Size(13, 13) // m05143：公钥=蓝钥匙
						}
						ui.Text(c, label).FontSize(13).Shrink(0)
						// 点文件名=复制全路径：按钮外壳给悬停/按下反馈（此前是裸 Row，无反馈不可聚焦）
						nameRow := ui.ButtonBase(c).Height(24).Padding(0, 6).Margin(0, -6).Radius(6).
							Cursor(ui.CursorPointer).Background(ui.RGBA(0, 0, 0, 0)).Grow(1)
						pressFeedback(nameRow, t)
						nameRow.Children(func() {
							ui.Text(c, filepath.Base(fullPath)).FontSize(13).
								TextColor(t.Accent).Grow(1)
						})
						if nameRow.Clicked() { // 点文件名=复制全路径
							mygo.Clipboard.WriteText(fullPath)
							c.Toast("已复制路径")
						}
						show := ui.ButtonBase(c).Height(24).Padding(0, 8).
							Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer).
							Background(ui.RGBA(0, 0, 0, 0)).Border(1, t.Border).Shrink(0)
						pressFeedback(show, t)
						show.Children(func() {
							ui.Text(c, revealLabel()).FontSize(13).TextColor(t.TextMuted)
						})
						if show.Clicked() {
							revealPath(fullPath)
						}
					})
				}
				keyRow(true, "私钥", a.keyDone.Output)
				ui.Box(c).Height(1).Grow(1).Background(t.Border)
				keyRow(false, "公钥", a.keyDone.Extra)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 12).
					Background(t.SurfaceHover.Alpha(0.35)).Children(func() {
					ui.Text(c, "保存位置").FontSize(12).TextColor(t.TextMuted).Shrink(0)
					dirRow := ui.ButtonBase(c).Height(20).Padding(0, 6).Margin(0, -6).Radius(6).
						Cursor(ui.CursorPointer).Background(ui.RGBA(0, 0, 0, 0)).Grow(1)
					pressFeedback(dirRow, t)
					dirRow.Children(func() {
						ui.Text(c, filepath.Dir(a.keyDone.Output)).FontSize(12).
							TextColor(t.TextMuted).Grow(1)
					})
					if dirRow.Clicked() {
						mygo.Clipboard.WriteText(filepath.Dir(a.keyDone.Output))
						c.Toast("已复制路径")
					}
				})
			})
		}

		toolCTA(c, t, "生成密钥", true, a.keyRunning, func() {
			a.keyRunning = true
			a.keyDone = nil
			a.keyDoneBits = keyBitsOptions[a.keyBitsIdx]
			name := strings.TrimSpace(a.keyName)
			if name == "" {
				name = "keypair"
			}
			dir := strings.TrimSpace(a.keyDir)
			if dir == "" {
				home, homeErr := os.UserHomeDir()
				if homeErr != nil {
					home = "."
				}
				dir = filepath.Join(home, "Desktop")
			}
			prefix := filepath.Join(dir, name)
			// GUI 侧前置守卫（m05014 血案补丁）：引擎旧版没有覆盖守卫时
			// 也不能静默覆盖——stat 双目标，任一存在直接红行拒绝
			for _, existing := range []string{prefix + "_private.pem", prefix + "_public.pem"} {
				if _, statErr := os.Stat(existing); statErr == nil {
					a.keyRunning = false
					a.keyDone = &ToolResult{Err: "密钥文件已存在: " + existing}
					return
				}
			}
			a.service.RunTool([]string{"crypto", "genkey", "--bits", a.keyDoneBits, "--output", prefix}, func(result ToolResult) {
				mygo.RunOnMain(func() {
					a.keyRunning = false
					a.keyDone = &result
					a.win.Invalidate()
				})
			})
		})
	})
}

// b64Result 一条 Base64 转换记录（guohub Base64Page 范式：原文+结果+方向）。
type b64Result struct {
	from string
	to   string
	enc  bool
}

// pageBase64 Base64 页（参照用户 guohub Base64Page：单输入+双动作钮+
// 结果历史卡片，点击原文/结果即复制；新结果排最前）。
func (a *app) pageBase64(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		ui.Row(c).Padding(14, 0, 4, 0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "文本 ⇄ Base64，点击历史卡片里的内容即可复制").FontSize(13).TextColor(t.TextMuted)
		})

		paramCard := ui.Column(c).Margin(12, 0, 0, 0).Gap(6)
		paramCard.Children(func() {
			// 状态行：已输入计数 + 清除
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "已输入 "+fmt.Sprint(len([]rune(a.b64In)))).
					FontSize(12).TextColor(t.TextMuted)
				ui.Spacer(c)
				if a.b64In != "" {
					clear := ui.ButtonBase(c).Height(22).Padding(0, 8).Radius(6).
						Border(1, t.Border).Cursor(ui.CursorPointer).Shrink(0)
					pressFeedback(clear, t)
					clear.Children(func() {
						ui.Text(c, "清除").FontSize(12).TextColor(t.TextMuted)
					})
					if clear.Clicked() {
						a.b64In = ""
					}
				}
			})
			ui.TextAreaBase(c, &a.b64In).
				Placeholder("请输入要编码或解码的内容").Lines(3, 8).Grow(1).FontSize(13).
				Padding(8, 12).Background(t.Surface).Border(1, t.Border).Radius(8)
		})

		// 双动作钮（guohub 同款：一个输入、两个方向按钮）
		ready := strings.TrimSpace(a.b64In) != "" && !a.b64Running
		ctaRow(c).Children(func() {
			for _, dir := range []struct {
				label string
				enc   bool
			}{
				{"文本 → Base64", true},
				{"Base64 → 文本", false},
			} {
				act := dir
				if primaryCTA(c, t, act.label, ready).Clicked() && ready {
					act := act
					verb := map[bool]string{true: "encode", false: "decode"}[act.enc]
					input := strings.TrimSpace(a.b64In)
					a.b64Running = true
					a.b64Err = ""
					a.service.RunToolStdin([]string{"base64", verb}, input, func(text, errMsg string) {
						mygo.RunOnMain(func() {
							a.b64Running = false
							a.b64Err = errMsg
							if errMsg == "" && text != "" {
								record := b64Result{from: input, to: text, enc: act.enc}
								a.b64Results = append([]b64Result{record}, a.b64Results...)
								a.b64In = "" // 跑完即清空（guohub 范式）
							}
							a.win.Invalidate()
						})
					})
				}
			}
		})

		// 失败行
		if a.b64Err != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(12, 0, 0, 0).Children(func() {
				ui.Image(c, iconError).Size(14, 14)
				ui.Text(c, a.b64Err).FontSize(13).TextColor(t.Danger).Grow(1)
			})
		}

		// 结果历史（新在前；点原文/结果即复制）
		for idx := range a.b64Results {
			recordIdx := idx
			record := a.b64Results[recordIdx]
			ui.Column(c).Margin(12, 0, 0, 0).Background(t.Surface).
				Border(1, t.Border).Radius(10).Clip().Children(func() {
				// 头行：序号 + 方向徽章 + 计数 + 删除
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 10).
					BorderWidth(0, 0, 1, 0).Children(func() {
					ui.Text(c, fmt.Sprint(recordIdx+1)).FontSize(12).TextColor(t.TextMuted)
					pillBg := map[bool]ui.Color{true: t.Accent.Alpha(0.12), false: t.TextMuted.Alpha(0.10)}[record.enc]
					pillFg := map[bool]ui.Color{true: t.Accent, false: t.TextMuted}[record.enc]
					pillLabel := map[bool]string{true: "Encode", false: "Decode"}[record.enc]
					ui.Text(c, pillLabel).FontSize(11).TextColor(pillFg).
						Background(pillBg).Radius(9).Padding(3, 8).Shrink(0)
					ui.Text(c, "原文 "+fmt.Sprint(len([]rune(record.from)))+" / 结果 "+
						fmt.Sprint(len([]rune(record.to)))).FontSize(12).TextColor(t.TextMuted)
					ui.Spacer(c)
					del := ui.ButtonBase(c).Width(24).Height(24).Radius(6).
						AlignItems(ui.Center).Cursor(ui.CursorPointer).
						Background(ui.RGBA(0, 0, 0, 0))
					if del.Pressed() {
						del.Background(t.Danger.Alpha(0.16))
					} else if del.Hovered() {
						del.Background(t.Danger.Alpha(0.08))
					}
					del.Children(func() {
						ui.Icon(c, iconCross).Size(8, 8).TextColor(t.Danger)
					})
					if del.Clicked() {
						a.b64Results = append(a.b64Results[:recordIdx], a.b64Results[recordIdx+1:]...)
					}
				})
				// 原文（点=复制）
				fromRow := ui.Row(c).Padding(8, 10).Cursor(ui.CursorPointer).
					Background(ui.RGBA(0, 0, 0, 0))
				pressFeedback(fromRow, t)
				fromRow.Children(func() {
					ui.Text(c, record.from).FontSize(13).Grow(1)
				})
				if fromRow.Clicked() {
					mygo.Clipboard.WriteText(record.from)
					c.Toast("已复制原文")
				}
				// 结果（灰底，点=复制）
				toRow := ui.Row(c).Padding(8, 10).Cursor(ui.CursorPointer).
					Background(t.SurfaceHover.Alpha(0.45))
				pressFeedback(toRow, t)
				toRow.Children(func() {
					ui.Text(c, record.to).FontSize(13).Grow(1)
				})
				if toRow.Clicked() {
					mygo.Clipboard.WriteText(record.to)
					c.Toast("已复制结果")
				}
			})
		}
	})
}

// ── 页面：格式转换组（m05445：视频/图片——压缩台同款参数区标准，
// openSettingRow 92 标签列 + 32 高字段族 + ghost 输出位置 + 任务台联动）──
func (a *app) pageMedia(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		a.convertLeadRow(c, "视频文件", a.mediaPath, a.browseMedia)
		openSettingRow(c, t, "输入文件", "拖入或选择", func() {
			a.toolFileField(c, t, a.mediaPath, "未选择", a.browseMedia, a.setMediaPath)
		})
		openSettingRow(c, t, "输出格式", "目标容器", func() {
			a.convertFormatRow(c, t, &a.mediaFmt, ".mp4", ".mkv", ".mov", ".webm")
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.mediaPath), &a.mediaOut, &a.mediaOutDirty, &a.mediaOutGhost, &a.mediaOutFocus)
		a.convertCTARow(c, t, "转换", a.mediaPath != "", func() {
			a.mediaPath, a.mediaOut, a.mediaOutDirty = "", "", false
		}, func() {
			out := a.convertOutput(a.mediaPath, a.mediaFmt, a.mediaOut)
			a.launchTask(a.service.StartConvert(filepath.Base(a.mediaPath),
				[]string{"media", "convert", a.mediaPath, out}, filepath.Dir(out), out))
		})
	})
}

// loadImagePreview 异步解码图片做预览（过期结果丢弃）；不支持的光栅格式
// （heic 等）置「不支持」态。渲染期每帧调用，路径未变即空转。
func (a *app) loadImagePreview(path string) {
	switch {
	case path == "":
		if a.imgPrevPath != "" {
			a.imgPrevPath = ""
			a.imgPrevSrc = nil
			a.imgPrevState = 0
		}
		return
	case a.imgPrevPath == path:
		return // 同文件已在途/已就绪
	}
	a.imgPrevPath = path
	a.imgPrevSrc = nil
	a.imgPrevState = 1
	go func() {
		data, readErr := os.ReadFile(path)
		var src ui.ImageSource
		var state int = 3
		if readErr == nil {
			if decoded, decodeErr := ui.DecodeImage(data); decodeErr == nil {
				src = decoded
				state = 2
			}
		}
		mygo.RunOnMain(func() {
			if a.imgPrevPath != path {
				return // 用户已换文件，丢弃过期结果
			}
			a.imgPrevSrc = src
			a.imgPrevState = state
			if state == 2 {
				a.imgPrevW, a.imgPrevH = ui.ImageSize(src)
			}
			a.win.Invalidate()
		})
	}()
}

// imagePreviewBox 预览画框（图片转换/压缩页共用）：贴在输入文件字段上方
// （m06548 用户定案：不要独立「预览」行，缩略图就在输入字段顶上）；Contain
// 保横纵比，Height 定死上界——图再大也不出框；尺寸标 mono 小胶囊贴右下角。
func (a *app) imagePreviewBox(c *ui.Context, t *ui.Theme, path string) {
	a.loadImagePreview(path)
	frame := ui.ButtonBase(c).Grow(1).AspectRatio(4.0/3.0).Margin(8, 0, 8, 0).Radius(8).
		Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
	if frame.Pressed() {
		frame.Border(1, t.Accent).Background(t.SurfaceHover)
	} else if frame.Hovered() {
		frame.Border(1, t.Accent)
	}
	frame.Children(func() {
		// Absolute 铺满层：不参与父尺寸测量——ButtonBase 高度由 AspectRatio(4/3) 定死；
		// 子项直接占位会把画框压塌或撑爆（m07096 出框根因：无高度约束的 Image 按内容自报尺寸）
		if a.imgPrevState == 2 {
			ui.Image(c, a.imgPrevSrc).Fit(ui.Contain).Absolute().Left(3).Right(3).Top(3).Bottom(3)
			return
		}
		ui.Column(c).Absolute().Left(3).Right(3).Top(3).Bottom(3).
			AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if a.imgPrevState == 3 {
				ui.Image(c, iconFile).Size(28, 28).Opacity(0.5)
			} else {
				ui.Text(c, "读取中…").FontSize(13).TextColor(t.TextMuted)
			}
		})
	})
	if frame.Clicked() {
		revealPath(path)
	}
}

func (a *app) pageImage(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		a.convertLeadRow(c, "图片文件", a.imagePath, a.browseImage)
		if a.imagePath != "" {
			a.imagePreviewBox(c, t, a.imagePath)
		}
		fileHint := "拖入或选择"
		if a.imagePath != "" && a.imgPrevState == 2 {
			fileHint = fmt.Sprintf("%.0f × %.0f px", a.imgPrevW, a.imgPrevH)
		}
		openSettingRow(c, t, "输入文件", fileHint, func() {
			a.toolFileField(c, t, a.imagePath, "未选择", a.browseImage, a.setImagePath)
		})
		openSettingRow(c, t, "输出格式", "目标图片格式", func() {
			a.convertFormatRow(c, t, &a.imageFmt, ".png", ".jpg", ".webp", ".bmp", ".tiff", ".gif")
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.imagePath), &a.imageOut, &a.imageOutDirty, &a.imageOutGhost, &a.imageOutFocus)
		a.convertCTARow(c, t, "转换", a.imagePath != "", func() {
			a.imagePath, a.imageOut, a.imageOutDirty = "", "", false
		}, func() {
			out := a.convertOutput(a.imagePath, a.imageFmt, a.imageOut)
			a.launchTask(a.service.StartConvert(filepath.Base(a.imagePath),
				[]string{"image", "convert", a.imagePath, out}, filepath.Dir(out), out))
		})
	})
}

// ── 页面：PDF 合并（多文件清单，顺序即合并顺序）──
func (a *app) pagePdfMerge(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	removeIdx := -1 // 本帧点了的移除徽章；建帧结束后才落地（见函数尾，压缩台同款）
	column.Children(func() {
		// 引导行（压缩台同款句式抄齐 m07672）：状态句带总体积+描边浏览钮
		pdfTotal := int64(0)
		for _, pdfPath := range a.pdfMergeInputs {
			info := a.entryInfoOf(pdfPath)
			if !info.dir {
				pdfTotal += info.size
			}
		}
		ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已准备的 PDF "+itoa(len(a.pdfMergeInputs))+" 项共 "+humanSize(pdfTotal)+
				"，可拖拽加入，按清单顺序合并").FontSize(13).TextColor(t.TextMuted)
			leadBrowseButton(c, t, "浏览选择…", func() { a.browsePdfMerge() })
		})
		// 清单卡：ui.List + ListState.Reorder（同压缩台）。清单顺序即合并顺序——
		// 此前是手写行，不能调顺序，分隔线也因只设宽度没设颜色而不可见（2026-11-23 审计）
		listCard := ui.Column(c).Margin(12, 0, 0, 0).Background(t.Surface).
			Border(1, t.Border).Radius(10).Clip()
		listCard.Children(func() {
			a.pdfMergeRows.Key = func(idx int) any { return a.pdfMergeInputs[idx] }
			a.pdfMergeRows.Reorder = func(rows []int, to int) {
				a.pdfMergeInputs = reorderInputs(a.pdfMergeInputs, rows, to)
			}
			a.pdfMergeRows.MoveAnimation = &ui.Smooth
			ui.List(c, &a.pdfMergeRows, len(a.pdfMergeInputs), func(idx int) {
				a.inputListRow(c, t, a.pdfMergeInputs[idx], idx, idx == len(a.pdfMergeInputs)-1, true, &removeIdx)
			}).Grow(1).Shrink(0)
		})
		// 输出位置：默认首文件旁
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.pdfMergeInputs[0]),
			&a.pdfMergeOut, &a.pdfMergeOutDirty, &a.pdfMergeOutGhost, &a.pdfMergeOutFocus)
		// 名字：空=合并文档（默认）
		openSettingRow(c, t, "名字", "合并产物文件名", func() {
			ui.TextInputBase(c, &a.pdfMergeName).
				Placeholder("合并文档（默认）").
				Grow(1).FontSize(13).Padding(5, 10).
				Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
		})
		a.convertCTARow(c, t, "合并 PDF", len(a.pdfMergeInputs) >= 2, func() {
			a.pdfMergeInputs = nil
			a.pdfMergeName, a.pdfMergeOut, a.pdfMergeOutDirty = "", "", false
		}, func() { // 按钮说动作不说数量（压缩台定案）：数量在引导句里
			name := strings.TrimSpace(a.pdfMergeName)
			if name == "" {
				name = "合并文档"
			}
			dir := strings.TrimSpace(a.pdfMergeOut)
			if dir == "" {
				dir = filepath.Dir(a.pdfMergeInputs[0])
			}
			out := uniquePath(filepath.Join(dir, name+".pdf"))
			args := append([]string{"pdf", "merge"}, a.pdfMergeInputs...)
			args = append(args, out)
			a.launchTask(a.service.StartConvert("PDF 合并", args, filepath.Dir(out), out))
		})
	})
	if removeIdx >= 0 {
		a.removePdfMerge(removeIdx) // 输出行要读首文件：删最后一项必须等建帧结束
	}
}

// ── 页面：PDF 提取页（单文件 + 页码区间 → 新 PDF）──
func (a *app) pagePdfExtract(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	pagesIssue := validatePagesSpec(a.pdfExtractPages, true) // 前端校验：填错就地红字、禁用主按钮
	column.Children(func() {
		a.convertLeadRow(c, "PDF", a.pdfExtractPath, a.browsePdfExtract)
		openSettingRow(c, t, "输入文件", "拖入或选择", func() {
			a.toolFileField(c, t, a.pdfExtractPath, "未选择", a.browsePdfExtract, a.setPdfExtractPath)
		})
		openSettingRow(c, t, "页码区间", "如 1-3,5 或 8-", func() {
			settingControl(c, t, visibleIssue(pagesIssue, a.pdfExtractPages), func() {
				ui.TextInputBase(c, &a.pdfExtractPages).
					Placeholder("1-3,5,8-").
					Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.pdfExtractPath),
			&a.pdfExtractOut, &a.pdfExtractOutDirty, &a.pdfExtractOutGhost, &a.pdfExtractOutFocus)
		a.convertCTARow(c, t, "提取页面", a.pdfExtractPath != "" && pagesIssue == "", func() {
			a.pdfExtractPath, a.pdfExtractPages = "", ""
			a.pdfExtractOut, a.pdfExtractOutDirty = "", false
		}, func() {
			dir := strings.TrimSpace(a.pdfExtractOut)
			if dir == "" {
				dir = filepath.Dir(a.pdfExtractPath)
			}
			base := strings.TrimSuffix(filepath.Base(a.pdfExtractPath), filepath.Ext(a.pdfExtractPath))
			out := uniquePath(filepath.Join(dir, base+"-extract.pdf"))
			a.launchTask(a.service.StartConvert("PDF 提取页",
				[]string{"pdf", "extract", a.pdfExtractPath, "--pages", strings.TrimSpace(a.pdfExtractPages), "--output", out},
				filepath.Dir(out), out))
		})
	})
}

// addPdfMerges 并入合并清单：内容判格（m00961 用户定案——不看扩展名），
// 分类器认出非 PDF 的明确拒收并计数 toast，认不出的放行引擎裁决；去重保序。
func (a *app) addPdfMerges(files []string) {
	added, rejected := 0, 0
	for _, candidate := range files {
		if got := classifyMagic(magicSniff(candidate)); got != "" && got != "pdf" {
			rejected++
			continue
		}
		if slices.Contains(a.pdfMergeInputs, candidate) {
			continue
		}
		a.pdfMergeInputs = append(a.pdfMergeInputs, candidate)
		added++
	}
	if rejected > 0 {
		a.queueToast(fmt.Sprintf("已跳过 %d 个非 PDF 文件", rejected))
	}
	if added > 0 {
		a.win.Update(func() {})
	}
}

// removePdfMerge 移出合并清单。
func (a *app) removePdfMerge(idx int) {
	if idx < 0 || idx >= len(a.pdfMergeInputs) {
		return
	}
	a.pdfMergeInputs = append(a.pdfMergeInputs[:idx], a.pdfMergeInputs[idx+1:]...)
	a.win.Update(func() {})
}

// browsePdfMerge 多选对话框并入合并清单（goroutine 范式同 browseFiles）。
func (a *app) browsePdfMerge() {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: window, Title: "选择要合并的 PDF", Multiple: true,
		})
		if err != nil {
			log.Println("browse pdf merge:", err)
			return
		}
		if len(paths) == 0 {
			return
		}
		mygo.RunOnMain(func() {
			a.addPdfMerges(paths)
			window.Invalidate()
		})
	}()
}

// setPdfExtractPath 选定/拖入待提取 PDF：换源重置输出脏状态。
func (a *app) setPdfExtractPath(path string) {
	if a.rejectIfForeign(path, "pdf") { // 内容判格守卫（m00961）：明确非PDF → toast 拒收
		return
	}
	a.pdfExtractPath = path
	if !a.pdfExtractOutDirty {
		a.pdfExtractOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browsePdfExtract() {
	a.toolBrowse("选择 PDF", false, a.setPdfExtractPath)
}

// ── 页面：视频压缩（media compress——目标体积）──
func (a *app) pageMediaCmp(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		a.convertLeadRow(c, "视频", a.mediaCmpPath, a.browseMediaCmp)
		openSettingRow(c, t, "输入文件", "拖入或选择", func() {
			a.toolFileField(c, t, a.mediaCmpPath, "未选择", a.browseMediaCmp, a.setMediaCmpPath)
		})
		// 目标体积：档位下拉 + 自定义输入（分卷压缩同款范式）
		targetIssue := "" // 前端校验：自定义目标填错就地红字、禁用主按钮
		if a.mediaCmpMode == "自定义" {
			targetIssue = validateMediaTarget(strings.TrimSpace(a.mediaCmpSize))
		}
		openSettingRow(c, t, "目标体积", "压到多大", func() {
			settingControl(c, t, targetIssue, func() {
				ui.Row(c).Grow(1).Gap(10).AlignItems(ui.Center).Children(func() {
					targetSel := ui.SelectBase(c, &a.mediaCmpMode)
					trig := targetSel.Trigger.
						Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
						AlignItems(ui.Center).Shrink(0).
						Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
					pressFeedback(trig, t)
					trig.Children(func() {
						ui.Text(c, a.mediaCmpMode).FontSize(13).Grow(1)
						ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
					})
					targetSel.Popup(func(panel ui.Element) {
						panel.MinWidth(150).Padding(4).Radius(10).
							Background(t.Surface).Border(1, t.Border)
						for _, option := range []string{"50%", "100MB", "25MB", "自定义"} {
							opt := option
							item := targetSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
								Cursor(ui.CursorPointer)
							switch {
							case item.Highlighted():
								item.Background(t.Accent).TextColor(t.AccentText)
							case a.mediaCmpMode == opt:
								item.Background(t.SurfaceHover)
							}
							item.Children(func() {
								ui.Text(c, opt).FontSize(13)
							})
						}
					})
					if a.mediaCmpMode == "自定义" {
						ui.TextInputBase(c, &a.mediaCmpSize).
							Placeholder("如 80MB / 500KB").Grow(1).FontSize(13).Padding(5, 10).
							Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
					}
				})
			})
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.mediaCmpPath),
			&a.mediaCmpOut, &a.mediaCmpOutDirty, &a.mediaCmpOutGhost, &a.mediaCmpOutFocus)
		target := a.mediaCmpMode
		if target == "自定义" {
			target = strings.TrimSpace(a.mediaCmpSize)
		}
		a.convertCTARow(c, t, "压缩视频", a.mediaCmpPath != "" && targetIssue == "", func() {
			a.mediaCmpPath, a.mediaCmpOut, a.mediaCmpOutDirty = "", "", false
		}, func() {
			dir := strings.TrimSpace(a.mediaCmpOut)
			if dir == "" {
				dir = filepath.Dir(a.mediaCmpPath)
			}
			base := strings.TrimSuffix(filepath.Base(a.mediaCmpPath), filepath.Ext(a.mediaCmpPath))
			out := uniquePath(filepath.Join(dir, base+"-compressed.mp4"))
			a.launchTask(a.service.StartConvert(filepath.Base(a.mediaCmpPath),
				[]string{"media", "compress", a.mediaCmpPath, "--target-size", target, "--output", out},
				filepath.Dir(out), out))
		})
	})
}

// ── 页面：音频转换（audio convert——格式互转/视频抽音轨）──
func (a *app) pageAudioCvt(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	bitrateIssue := validateBitrate(a.audioCvtBitrate) // 前端校验：填错就地红字、禁用主按钮
	rateIssue := validateSampleRate(a.audioCvtRate)
	column.Children(func() {
		a.convertLeadRow(c, "音频", a.audioCvtPath, a.browseAudioCvt)
		openSettingRow(c, t, "输入文件", "视频会抽出音轨", func() {
			a.toolFileField(c, t, a.audioCvtPath, "未选择", a.browseAudioCvt, a.setAudioCvtPath)
		})
		openSettingRow(c, t, "输出格式", "目标音频格式", func() {
			a.convertFormatRow(c, t, &a.audioCvtFmt, ".mp3", ".m4a", ".flac", ".wav", ".ogg", ".opus")
		})
		openSettingRow(c, t, "码率", "留空跟随源", func() {
			settingControl(c, t, bitrateIssue, func() {
				ui.TextInputBase(c, &a.audioCvtBitrate).
					Placeholder("如 128k").Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		openSettingRow(c, t, "采样率", "留空跟随源", func() {
			settingControl(c, t, rateIssue, func() {
				ui.TextInputBase(c, &a.audioCvtRate).
					Placeholder("如 44100").Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		openSettingRow(c, t, "单声道", "合并双声道", func() {
			ui.Row(c).Grow(1).Children(func() {
				ui.Switch(c, &a.audioCvtMono).Cursor(ui.CursorPointer)
			})
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.audioCvtPath),
			&a.audioCvtOut, &a.audioCvtOutDirty, &a.audioCvtOutGhost, &a.audioCvtOutFocus)
		a.convertCTARow(c, t, "转换", a.audioCvtPath != "" && bitrateIssue == "" && rateIssue == "", func() {
			a.audioCvtPath, a.audioCvtOut, a.audioCvtOutDirty = "", "", false
		}, func() {
			out := a.convertOutput(a.audioCvtPath, a.audioCvtFmt, a.audioCvtOut)
			args := []string{"audio", "convert", a.audioCvtPath, out}
			if v := strings.TrimSpace(a.audioCvtBitrate); v != "" {
				args = append(args, "--bitrate", v)
			}
			if v := strings.TrimSpace(a.audioCvtRate); v != "" {
				args = append(args, "--sample-rate", v)
			}
			if a.audioCvtMono {
				args = append(args, "--mono")
			}
			a.launchTask(a.service.StartConvert(filepath.Base(a.audioCvtPath), args, filepath.Dir(out), out))
		})
	})
}

// ── 页面：音频压缩（audio compress——缺省压到原码率一半）──
func (a *app) pageAudioCmp(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	bitrateIssue := validateBitrate(a.audioCmpBitrate) // 前端校验：填错就地红字、禁用主按钮
	column.Children(func() {
		a.convertLeadRow(c, "音频", a.audioCmpPath, a.browseAudioCmp)
		openSettingRow(c, t, "输入文件", "拖入或选择", func() {
			a.toolFileField(c, t, a.audioCmpPath, "未选择", a.browseAudioCmp, a.setAudioCmpPath)
		})
		openSettingRow(c, t, "目标码率", "留空=压一半", func() {
			settingControl(c, t, bitrateIssue, func() {
				ui.TextInputBase(c, &a.audioCmpBitrate).
					Placeholder("如 64k").Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		openSettingRow(c, t, "单声道", "合并双声道", func() {
			ui.Row(c).Grow(1).Children(func() {
				ui.Switch(c, &a.audioCmpMono).Cursor(ui.CursorPointer)
			})
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.audioCmpPath),
			&a.audioCmpOut, &a.audioCmpOutDirty, &a.audioCmpOutGhost, &a.audioCmpOutFocus)
		a.convertCTARow(c, t, "压缩音频", a.audioCmpPath != "" && bitrateIssue == "", func() {
			a.audioCmpPath, a.audioCmpOut, a.audioCmpOutDirty = "", "", false
		}, func() {
			dir := strings.TrimSpace(a.audioCmpOut)
			if dir == "" {
				dir = filepath.Dir(a.audioCmpPath)
			}
			base := strings.TrimSuffix(filepath.Base(a.audioCmpPath), filepath.Ext(a.audioCmpPath))
			ext := filepath.Ext(a.audioCmpPath)
			out := uniquePath(filepath.Join(dir, base+"-compressed"+ext))
			args := []string{"audio", "compress", a.audioCmpPath, "--output", out}
			if v := strings.TrimSpace(a.audioCmpBitrate); v != "" {
				args = append(args, "--bitrate", v)
			}
			if a.audioCmpMono {
				args = append(args, "--mono")
			}
			a.launchTask(a.service.StartConvert(filepath.Base(a.audioCmpPath), args, filepath.Dir(out), out))
		})
	})
}

func (a *app) setMediaCmpPath(path string) {
	if a.rejectIfForeign(path, "av") { // 内容判格守卫（m00961）：明确非音视频 → toast 拒收
		return
	}
	a.mediaCmpPath = path
	if !a.mediaCmpOutDirty {
		a.mediaCmpOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browseMediaCmp() {
	a.toolBrowse("选择视频文件", false, a.setMediaCmpPath)
}

func (a *app) setAudioCvtPath(path string) {
	if a.rejectIfForeign(path, "av") { // 内容判格守卫（m00961）：明确非音视频 → toast 拒收
		return
	}
	a.audioCvtPath = path
	if !a.audioCvtOutDirty {
		a.audioCvtOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browseAudioCvt() {
	a.toolBrowse("选择音频或视频文件", false, a.setAudioCvtPath)
}

func (a *app) setAudioCmpPath(path string) {
	if a.rejectIfForeign(path, "av") { // 内容判格守卫（m00961）：明确非音视频 → toast 拒收
		return
	}
	a.audioCmpPath = path
	if !a.audioCmpOutDirty {
		a.audioCmpOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browseAudioCmp() {
	a.toolBrowse("选择音频文件", false, a.setAudioCmpPath)
}

// ── 页面：图片压缩（image compress——目标体积/画质优先）──
func (a *app) pageImageCmp(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		// 引导行（压缩台句式；m07800 多图批压定案：清单化，单张=N=1 特例）
		cmpTotal := int64(0)
		for _, cmpPath := range a.imageCmpInputs {
			info := a.entryInfoOf(cmpPath)
			if !info.dir {
				cmpTotal += info.size
			}
		}
		ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已准备的图片 "+itoa(len(a.imageCmpInputs))+" 项共 "+humanSize(cmpTotal)+"，可拖拽加入或").
				FontSize(13).TextColor(t.TextMuted)
			leadBrowseButton(c, t, "浏览选择…", func() { a.browseImageCmp() })
			ui.Box(c).Grow(1)
			if len(a.imageCmpInputs) > 0 {
				if ui.Button(c, "清空").FontSize(13).Height(22).Padding(0, 8).
					TextColor(t.TextMuted).Cursor(ui.CursorPointer).Clicked() {
					a.clearImageCmp()
				}
			}
		})
		// 单张：预览画框+输入文件字段行（与图片转换页同构，m07839 间距定案）；
		// 多张：清单卡即信息，预览与字段行都不上（避免与卡重复挤占参数区）
		if len(a.imageCmpInputs) == 1 {
			a.imagePreviewBox(c, t, a.imageCmpInputs[0])
			fileHint := "拖入或选择"
			if a.imgPrevState == 2 {
				fileHint = fmt.Sprintf("%.0f × %.0f px", a.imgPrevW, a.imgPrevH)
			}
			openSettingRow(c, t, "输入文件", fileHint, func() {
				a.toolFileField(c, t, a.imageCmpInputs[0], "未选择", a.browseImageCmp,
					func(dropped string) { a.addImageCmpInputs([]string{dropped}) })
			})
		} else if len(a.imageCmpInputs) > 0 {
			// 图片清单（哈希页清单卡范式：icon+路径+体积+hover 移除徽章）
			removal := -1
			card := ui.Column(c).Margin(12, 0, 0, 0).
				Background(t.Surface).Border(1, t.Border).Radius(10).Clip()
			card.Children(func() {
				ui.List(c, &a.imageCmpRows, len(a.imageCmpInputs), func(idx int) {
					a.inputListRow(c, t, a.imageCmpInputs[idx], idx, idx == len(a.imageCmpInputs)-1, false, &removal)
				}).Grow(1).Shrink(0)
			})
			if removal >= 0 {
				a.removeImageCmpInput(removal)
			}
		}

		targetIssue := "" // 前端校验：自定义目标填错就地红字、禁用主按钮
		if a.imageCmpMode == "自定义" {
			targetIssue = validateByteSize(strings.TrimSpace(a.imageCmpSize), 0)
		}
		openSettingRow(c, t, "目标体积", "压到多大", func() {
			settingControl(c, t, targetIssue, func() {
				ui.Row(c).Grow(1).Gap(10).AlignItems(ui.Center).Children(func() {
					sizeSel := ui.SelectBase(c, &a.imageCmpMode)
					trig := sizeSel.Trigger.
						Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
						AlignItems(ui.Center).Shrink(0).
						Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
					pressFeedback(trig, t)
					trig.Children(func() {
						ui.Text(c, a.imageCmpMode).FontSize(13).Grow(1)
						ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
					})
					sizeSel.Popup(func(panel ui.Element) {
						panel.MinWidth(150).Padding(4).Radius(10).
							Background(t.Surface).Border(1, t.Border)
						for _, option := range []string{"100KB", "500KB", "1MB", "5MB", "自定义"} {
							opt := option
							item := sizeSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
								Cursor(ui.CursorPointer)
							switch {
							case item.Highlighted():
								item.Background(t.Accent).TextColor(t.AccentText)
							case a.imageCmpMode == opt:
								item.Background(t.SurfaceHover)
							}
							item.Children(func() {
								ui.Text(c, opt).FontSize(13)
							})
						}
					})
					if a.imageCmpMode == "自定义" {
						ui.TextInputBase(c, &a.imageCmpSize).
							Placeholder("如 300KB / 2MB").Grow(1).FontSize(13).Padding(5, 10).
							Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
					}
				})
			})
		})
		// m07888：质量维度边界——引擎 qualityCapable=jpg，目标体积只对 JPG 生效
		jpgCount := 0
		for _, cmpPath := range a.imageCmpInputs {
			switch strings.ToLower(filepath.Ext(cmpPath)) {
			case ".jpg", ".jpeg":
				jpgCount++
			}
		}
		openSettingRowTop(c, t, "压缩策略", "先牺牲哪个", func() {
			ui.RadioGroup(c, func() {
				for _, opt := range []struct{ value, title, hint string }{
					{"size-first", "先缩尺寸", "清晰度保持视觉无损档（q90）逐档缩小；到极限再降画质兜底"},
					{"size-only", "只缩尺寸", "画质钉 q90 绝不动，只缩小分辨率；缩到 5% 仍装不下则失败"},
					{"quality-first", "先降画质", "分辨率保持原大小，清晰度逐档下调（下限 q50）；还不够再缩尺寸"},
					{"quality-only", "只降画质", "分辨率绝不缩小，画质一路降到底（q1）；仍装不下则失败"},
				} {
					opt := opt
					row := ui.ButtonBase(c).Grow(1).Cursor(ui.CursorPointer).Radius(8).
						Padding(5, 5).Background(ui.RGBA(0, 0, 0, 0))
					if row.Pressed() {
						row.Background(t.Text.Alpha(0.08))
					} else if row.Hovered() {
						row.Background(t.Text.Alpha(0.05))
					}
					row.Children(func() {
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							// 自拼 radio（RadioBase 交互行+自绘圈）：圈单独下移 4px，标题/说明原位
							//（用户 m07544/m07566：只 margin-top 圈本身，不整行挪）
							rb := ui.RadioBase(c, &a.imageCmpPriority, opt.value).Cursor(ui.CursorPointer).Gap(8).Radius(6)
							if rb.Pressed() {
								rb.Background(pressedShade(t))
							} else if rb.Hovered() {
								rb.Background(t.SurfaceHover)
							}
							rb.Children(func() {
								dot := ui.Box(c).Size(16, 16).Radius(8).Shrink(0).Margin(4, 0, 0, 0)
								if a.imageCmpPriority == opt.value {
									dot.Background(t.Accent)
								} else {
									dot.Background(t.Background).Border(1, t.Border.Mix(t.Text, 0.25))
								}
								dot.DrawOver(func(p *ui.Painter, r ui.Rect) {
									if a.imageCmpPriority == opt.value {
										d := float32(4.5)
										p.Fill(ui.Rect{X: r.X + (r.W-d)/2, Y: r.Y + (r.W-d)/2, W: d, H: d}, t.AccentText, d/2)
									}
								})
								ui.Text(c, opt.title).FontSize(13)
							})
							ui.Text(c, opt.hint).FontSize(12).TextColor(t.TextMuted).Margin(0, 0, 0, 24).Wrap()
						})
					})
					if row.Clicked() {
						a.imageCmpPriority = opt.value
					}
				}
			}).Gap(0).Grow(1)
		})
		// m07888 用户定案：策略区明示质量维度边界——目标体积只对 JPG 生效
		//（引擎 qualityCapable=jpg；PNG/WebP/GIF/BMP/TIFF 天生无损被明确拒绝）
		if len(a.imageCmpInputs) > 0 && jpgCount < len(a.imageCmpInputs) {
			// m07943 用户定案：整行浅黄底圆角横幅（警告条），非裸文字行
			warn := ui.Row(c).Margin(8, 0, 0, 0).Gap(8).AlignItems(ui.Center).
				Background(hexA("#F6C243", 0.16)).Radius(8).Padding(8, 10)
			warn.Children(func() {
				ui.Image(c, iconWarn).Size(14, 14)
				if jpgCount == 0 {
					ui.Text(c, "所选全是无损格式（PNG/WebP/GIF/BMP/TIFF），没有质量维度——目标体积压缩只支持 JPG，可先在「图片转换」转成 .jpg").
						FontSize(12).TextColor(t.Text).Grow(1).Wrap()
				} else {
					ui.Text(c, itoa(len(a.imageCmpInputs)-jpgCount)+" 张无损格式（PNG/WebP 等）没有质量维度，目标体积对它们不生效——逐张任务会报错，其余照常压缩").
						FontSize(12).TextColor(t.Text).Grow(1).Wrap()
				}
			})
		}
		planDir := ""
		if len(a.imageCmpInputs) > 0 {
			planDir = filepath.Dir(a.imageCmpInputs[0])
		}
		a.convertOutRow(c, t, "输出位置", planDir,
			&a.imageCmpOut, &a.imageCmpOutDirty, &a.imageCmpOutGhost, &a.imageCmpOutFocus)
		target := a.imageCmpMode
		if target == "自定义" {
			target = strings.TrimSpace(a.imageCmpSize)
		}
		// 逐张起任务（引擎按单文件出产物；批=多任务进处理台，单张进度各自可见）。
		// 按钮说动作不说数量（压缩台定案）：张数在引导句里
		a.convertCTARow(c, t, "压缩图片", jpgCount > 0 && targetIssue == "", func() {
			a.imageCmpInputs = nil
			a.imageCmpOut, a.imageCmpOutDirty = "", false
		}, func() {
			outDir := strings.TrimSpace(a.imageCmpOut)
			for _, cmpPath := range a.imageCmpInputs {
				dir := outDir
				if dir == "" {
					dir = filepath.Dir(cmpPath)
				}
				base := strings.TrimSuffix(filepath.Base(cmpPath), filepath.Ext(cmpPath))
				ext := filepath.Ext(cmpPath)
				out := uniquePath(filepath.Join(dir, base+"-compressed"+ext))
				a.launchTask(a.service.StartConvert(filepath.Base(cmpPath),
					[]string{"image", "compress", cmpPath, "--target-size", target,
						"--priority", a.imageCmpPriority, "--output", out},
					filepath.Dir(out), out))
			}
			a.page = pageTasks
			a.win.Update(func() {})
		})
	})
}

// ── 页面：新建图片（image new——尺寸/纸型 + 颜色 + PPI）──
func (a *app) pageImageNew(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	sizeIssue := validateImageSize(a.imageNewSize) // 前端校验：填错就地红字、禁用主按钮
	colorIssue := validateImageColor(a.imageNewColor)
	ppiIssue := validatePPI(a.imageNewPpi)
	column.Children(func() {
		ui.Row(c).Padding(14, 0, 4, 0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "生成空白占位图（可指定颜色与打印密度）").FontSize(13).TextColor(t.TextMuted)
		})
		openSettingRow(c, t, "尺寸", "800x600 / a4", func() {
			settingControl(c, t, visibleIssue(sizeIssue, a.imageNewSize), func() {
				ui.TextInputBase(c, &a.imageNewSize).
					Placeholder("800x600（或 a4 / 55x90mm）").
					Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		openSettingRow(c, t, "颜色", "留空=全透明", func() {
			settingControl(c, t, colorIssue, func() {
				ui.TextInputBase(c, &a.imageNewColor).
					Placeholder("如 red / #FF0000 / #3344FF88").
					Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		openSettingRow(c, t, "打印密度", "配 mm/cm/in", func() {
			settingControl(c, t, ppiIssue, func() {
				ui.TextInputBase(c, &a.imageNewPpi).
					Placeholder("缺省 300").
					Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		openSettingRow(c, t, "名字", "扩展名定格式", func() {
			ui.TextInputBase(c, &a.imageNewName).
				Placeholder("空白图.png（默认）").
				Grow(1).FontSize(13).Padding(5, 10).
				Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
		})
		a.convertOutRow(c, t, "输出位置", filepath.Join(func() string {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				return "."
			}
			return home
		}(), "Desktop"),
			&a.imageNewOut, &a.imageNewOutDirty, &a.imageNewOutGhost, &a.imageNewOutFocus)
		a.convertCTARow(c, t, "生成图片", strings.TrimSpace(a.imageNewSize) != "" && sizeIssue == "" && colorIssue == "" && ppiIssue == "", nil, func() {
			dir := strings.TrimSpace(a.imageNewOut)
			if dir == "" {
				home, homeErr := os.UserHomeDir()
				if homeErr != nil {
					home = "."
				}
				dir = filepath.Join(home, "Desktop")
			}
			name := strings.TrimSpace(a.imageNewName)
			if name == "" {
				name = "空白图.png"
			}
			out := uniquePath(filepath.Join(dir, name))
			args := []string{"image", "new", out, strings.TrimSpace(a.imageNewSize)}
			if v := strings.TrimSpace(a.imageNewColor); v != "" {
				args = append(args, "--color", v)
			}
			if v := strings.TrimSpace(a.imageNewPpi); v != "" {
				args = append(args, "--ppi", v)
			}
			a.launchTask(a.service.StartConvert("新建图片", args, filepath.Dir(out), out))
		})
	})
}

// ── 页面：Office 转换（doc/wps→docx、xls/et→xlsx、ppt/dps→pptx）──
func (a *app) pageOffice(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		a.convertLeadRow(c, "文档", a.officePath, a.browseOffice)
		openSettingRow(c, t, "输入文件", "拖入或选择", func() {
			a.toolFileField(c, t, a.officePath, "未选择", a.browseOffice, a.setOfficePath)
		})
		openSettingRow(c, t, "目标格式", "随文档族自动选", func() {
			target := officeTargetOf(a.officePath)
			if target == "" {
				target = "待选择 DOC/XLS/PPT（或 WPS 家族）"
			}
			ui.Text(c, target).FontSize(13).TextColor(t.Text)
		})
		openSettingRow(c, t, "转换引擎", "自动=内置引擎", func() {
			ui.Row(c).Grow(1).Children(func() {
				engineSel := ui.SelectBase(c, &a.officeEngine)
				trig := engineSel.Trigger.
					Height(32).MinWidth(150).Radius(8).Padding(0, 10).Gap(6).
					AlignItems(ui.Center).Shrink(0).
					Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
				pressFeedback(trig, t)
				trig.Children(func() {
					ui.Text(c, a.officeEngine).FontSize(13).Grow(1)
					ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
				})
				engineSel.Popup(func(panel ui.Element) {
					panel.MinWidth(170).Padding(4).Radius(10).
						Background(t.Surface).Border(1, t.Border)
					for _, option := range []string{"自动", "MS Office", "WPS", "LibreOffice"} {
						opt := option
						item := engineSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
							Cursor(ui.CursorPointer)
						switch {
						case item.Highlighted():
							item.Background(t.Accent).TextColor(t.AccentText)
						case a.officeEngine == opt:
							item.Background(t.SurfaceHover)
						}
						item.Children(func() {
							ui.Text(c, opt).FontSize(13)
						})
					}
				})
			})
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.officePath),
			&a.officeOut, &a.officeOutDirty, &a.officeOutGhost, &a.officeOutFocus)
		targetExt := officeTargetOf(a.officePath)
		a.convertCTARow(c, t, "转换文档", a.officePath != "" && targetExt != "", func() {
			a.officePath, a.officeOut, a.officeOutDirty = "", "", false
		}, func() {
			dir := strings.TrimSpace(a.officeOut)
			if dir == "" {
				dir = filepath.Dir(a.officePath)
			}
			base := strings.TrimSuffix(filepath.Base(a.officePath), filepath.Ext(a.officePath))
			out := uniquePath(filepath.Join(dir, base+targetExt))
			args := []string{"office", "convert", a.officePath, out}
			switch a.officeEngine {
			case "MS Office":
				args = append(args, "--try-msoffice")
			case "WPS":
				args = append(args, "--try-wps")
			case "LibreOffice":
				args = append(args, "--try-libreoffice")
			}
			a.launchTask(a.service.StartConvert(filepath.Base(a.officePath), args, filepath.Dir(out), out))
		})
	})
}

// officeTargetOf 源扩展名 → 目标扩展名（引擎族匹配强制的 GUI 侧预判）。
func officeTargetOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".doc", ".wps":
		return ".docx"
	case ".xls", ".et":
		return ".xlsx"
	case ".ppt", ".dps":
		return ".pptx"
	}
	return ""
}

// addImageCmpInputs 收敛拖拽/多选（m07800 多图批压）：内容判格（m00961
// 用户定案——不看扩展名；分类器认出非图片才拒并计数 toast，认不出的放行
// 引擎裁决）+去重保序
func (a *app) addImageCmpInputs(paths []string) {
	rejected := 0
	for _, path := range paths {
		if got := classifyMagic(magicSniff(path)); got != "" && got != "image" {
			rejected++
			continue
		}
		dup := false
		for _, have := range a.imageCmpInputs {
			if have == path {
				dup = true
				break
			}
		}
		if !dup {
			a.imageCmpInputs = append(a.imageCmpInputs, path)
		}
	}
	if rejected > 0 {
		a.queueToast(fmt.Sprintf("已跳过 %d 个非图片文件", rejected))
	}
	if !a.imageCmpOutDirty {
		a.imageCmpOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) removeImageCmpInput(idx int) {
	a.imageCmpInputs = append(a.imageCmpInputs[:idx], a.imageCmpInputs[idx+1:]...)
	if len(a.imageCmpInputs) <= 1 && !a.imageCmpOutDirty {
		a.imageCmpOut = ""
	}
}

func (a *app) clearImageCmp() {
	a.imageCmpInputs = nil
	if !a.imageCmpOutDirty {
		a.imageCmpOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browseImageCmp() {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   window,
			Title:    "选择要压缩的图片",
			Multiple: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.win.Update(func() {
			a.addImageCmpInputs(paths)
		})
	}()
}

func (a *app) setOfficePath(path string) {
	if a.rejectIfForeign(path, "office") { // 内容判格守卫（m00961）：明确非Office 文档 → toast 拒收
		return
	}
	a.officePath = path
	if !a.officeOutDirty {
		a.officeOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browseOffice() {
	a.toolBrowse("选择 Office 文档", false, a.setOfficePath)
}

// ── 页面：PDF 旋转（零损元数据；可选页码区间）──
func (a *app) pagePdfRotate(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	pagesIssue := validatePagesSpec(a.pdfRotatePages, false) // 前端校验：填错就地红字、禁用主按钮
	column.Children(func() {
		a.convertLeadRow(c, "PDF", a.pdfRotatePath, a.browsePdfRotate)
		openSettingRow(c, t, "输入文件", "拖入或选择", func() {
			a.toolFileField(c, t, a.pdfRotatePath, "未选择", a.browsePdfRotate, a.setPdfRotatePath)
		})
		openSettingRow(c, t, "方向", "仅改元数据", func() {
			ui.Row(c).Grow(1).Children(func() {
				dirSel := ui.SelectBase(c, &a.pdfRotateDir)
				trig := dirSel.Trigger.
					Height(32).MinWidth(150).Radius(8).Padding(0, 10).Gap(6).
					AlignItems(ui.Center).Shrink(0).
					Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
				pressFeedback(trig, t)
				trig.Children(func() {
					ui.Text(c, a.pdfRotateDir).FontSize(13).Grow(1)
					ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
				})
				dirSel.Popup(func(panel ui.Element) {
					panel.MinWidth(170).Padding(4).Radius(10).
						Background(t.Surface).Border(1, t.Border)
					for _, option := range []string{"左转 90°", "右转 90°", "转 180°"} {
						opt := option
						item := dirSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
							Cursor(ui.CursorPointer)
						switch {
						case item.Highlighted():
							item.Background(t.Accent).TextColor(t.AccentText)
						case a.pdfRotateDir == opt:
							item.Background(t.SurfaceHover)
						}
						item.Children(func() {
							ui.Text(c, opt).FontSize(13)
						})
					}
				})
			})
		})
		openSettingRow(c, t, "页码", "留空=全部页", func() {
			settingControl(c, t, pagesIssue, func() {
				ui.TextInputBase(c, &a.pdfRotatePages).
					Placeholder("1-3,5,8-（可选）").
					Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.pdfRotatePath),
			&a.pdfRotateOut, &a.pdfRotateOutDirty, &a.pdfRotateOutGhost, &a.pdfRotateOutFocus)
		a.convertCTARow(c, t, "旋转", a.pdfRotatePath != "" && pagesIssue == "", func() {
			a.pdfRotatePath, a.pdfRotatePages = "", ""
			a.pdfRotateOut, a.pdfRotateOutDirty = "", false
		}, func() {
			dir := strings.TrimSpace(a.pdfRotateOut)
			if dir == "" {
				dir = filepath.Dir(a.pdfRotatePath)
			}
			base := strings.TrimSuffix(filepath.Base(a.pdfRotatePath), filepath.Ext(a.pdfRotatePath))
			out := uniquePath(filepath.Join(dir, base+"-rotated.pdf"))
			args := []string{"pdf", pdfRotateCmd(a.pdfRotateDir), a.pdfRotatePath, "--output", out}
			if v := strings.TrimSpace(a.pdfRotatePages); v != "" {
				args = append(args, "--pages", v)
			}
			a.launchTask(a.service.StartConvert(filepath.Base(a.pdfRotatePath), args, filepath.Dir(out), out))
		})
	})
}

func pdfRotateCmd(dir string) string {
	switch dir {
	case "右转 90°":
		return "rotate-right"
	case "转 180°":
		return "rotate-180"
	default:
		return "rotate-left"
	}
}

// ── 页面：PDF 页面尺寸（纸型/自定义统一；文字页体检拦截）──
func (a *app) pagePdfResize(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	pagesIssue := validatePagesSpec(a.pdfResizePages, false) // 前端校验：填错就地红字、禁用主按钮
	column.Children(func() {
		a.convertLeadRow(c, "PDF", a.pdfResizePath, a.browsePdfResize)
		openSettingRow(c, t, "输入文件", "拖入或选择", func() {
			a.toolFileField(c, t, a.pdfResizePath, "未选择", a.browsePdfResize, a.setPdfResizePath)
		})
		openSettingRow(c, t, "纸型", "统一页面尺寸", func() {
			ui.Row(c).Grow(1).Children(func() {
				sizeSel := ui.SelectBase(c, &a.pdfResizeSize)
				trig := sizeSel.Trigger.
					Height(32).MinWidth(150).Radius(8).Padding(0, 10).Gap(6).
					AlignItems(ui.Center).Shrink(0).
					Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
				pressFeedback(trig, t)
				trig.Children(func() {
					ui.Text(c, a.pdfResizeSize).FontSize(13).Grow(1)
					ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
				})
				sizeSel.Popup(func(panel ui.Element) {
					panel.MinWidth(170).Padding(4).Radius(10).
						Background(t.Surface).Border(1, t.Border)
					for _, option := range []string{"a4", "a4l", "letter", "210x297mm"} {
						opt := option
						item := sizeSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
							Cursor(ui.CursorPointer)
						switch {
						case item.Highlighted():
							item.Background(t.Accent).TextColor(t.AccentText)
						case a.pdfResizeSize == opt:
							item.Background(t.SurfaceHover)
						}
						item.Children(func() {
							ui.Text(c, opt).FontSize(13)
						})
					}
				})
			})
		})
		openSettingRow(c, t, "页码", "留空=全部页", func() {
			settingControl(c, t, pagesIssue, func() {
				ui.TextInputBase(c, &a.pdfResizePages).
					Placeholder("1-3,5,8-（可选）").
					Grow(1).FontSize(13).Padding(5, 10).
					Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
			})
		})
		// 含文字页仍缩放：引擎缺省拒绝含文字页的 PDF（缩放会让文字重排）并指路 --force——
		// 此前 GUI 没有这个出口，文字型 PDF 在这里走不通（2026-11-23 审计）
		openSettingRow(c, t, "文字页", "含文字页仍缩放", func() {
			ui.Row(c).Grow(1).AlignItems(ui.Center).Children(func() {
				ui.Switch(c, &a.pdfResizeForce).Cursor(ui.CursorPointer)
			})
		})
		a.convertOutRow(c, t, "输出位置", filepath.Dir(a.pdfResizePath),
			&a.pdfResizeOut, &a.pdfResizeOutDirty, &a.pdfResizeOutGhost, &a.pdfResizeOutFocus)
		a.convertCTARow(c, t, "统一尺寸", a.pdfResizePath != "" && pagesIssue == "", func() {
			a.pdfResizePath, a.pdfResizePages = "", ""
			a.pdfResizeOut, a.pdfResizeOutDirty, a.pdfResizeForce = "", false, false
		}, func() {
			dir := strings.TrimSpace(a.pdfResizeOut)
			if dir == "" {
				dir = filepath.Dir(a.pdfResizePath)
			}
			base := strings.TrimSuffix(filepath.Base(a.pdfResizePath), filepath.Ext(a.pdfResizePath))
			out := uniquePath(filepath.Join(dir, base+"-resized.pdf"))
			args := []string{"pdf", "resize", a.pdfResizePath, "--size", a.pdfResizeSize, "--output", out}
			if v := strings.TrimSpace(a.pdfResizePages); v != "" {
				args = append(args, "--pages", v)
			}
			if a.pdfResizeForce {
				args = append(args, "--force")
			}
			a.launchTask(a.service.StartConvert(filepath.Base(a.pdfResizePath), args, filepath.Dir(out), out))
		})
	})
}

// ── 页面：文件哈希（多文件清单 + 算法 + 期望值校验）──
type hashResult struct {
	Path    string
	Hash    string
	Size    int64
	Algo    string // 计算时用的算法（大写）——徽标按它显示，不读当前下拉值
	Matched *bool  // --expect 校验时的判定（nil=未校验）
}

func (a *app) pageHash(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	expectIssue := validateHexHash(a.hashExpect, a.hashAlgo) // 前端校验：位数/字符填错就地红字
	column.Children(func() {
		// 引导行（压缩台同款句式抄齐 m07672）：状态句带总体积+描边浏览钮
		hashTotal := int64(0)
		for _, hashPath := range a.hashInputs {
			info := a.entryInfoOf(hashPath)
			if !info.dir {
				hashTotal += info.size
			}
		}
		ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已准备的待校验文件 "+itoa(len(a.hashInputs))+" 项共 "+humanSize(hashTotal)+"，可拖拽加入或").
				FontSize(13).TextColor(t.TextMuted)
			leadBrowseButton(c, t, "浏览选择…", func() { a.browseHash() })
			ui.Box(c).Grow(1)
			if len(a.hashInputs) > 0 {
				if ui.Button(c, "清空").FontSize(13).Height(22).Padding(0, 8).
					TextColor(t.TextMuted).Cursor(ui.CursorPointer).Clicked() {
					a.hashInputs = nil
					a.hashResults = nil
					a.hashErr = ""
					a.hashExpect = ""
					a.win.Update(func() {})
				}
			}
		})
		// 文件清单（同压缩台清单卡范式）
		if len(a.hashInputs) > 0 {
			removal := -1
			card := ui.Column(c).Margin(12, 0, 0, 0).
				Background(t.Surface).Border(1, t.Border).Radius(10).Clip()
			card.Children(func() {
				ui.List(c, &a.hashRows, len(a.hashInputs), func(idx int) {
					a.inputListRow(c, t, a.hashInputs[idx], idx, idx == len(a.hashInputs)-1, false, &removal)
				}).Grow(1).Shrink(0)
			})
			if removal >= 0 {
				a.hashInputs = append(a.hashInputs[:removal], a.hashInputs[removal+1:]...)
				a.hashResults = nil
				a.hashErr = ""
			}
		}
		openSettingRow(c, t, "算法", "哈希族", func() {
			ui.Row(c).Grow(1).Children(func() {
				algoSel := ui.SelectBase(c, &a.hashAlgo)
				trig := algoSel.Trigger.
					Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
					AlignItems(ui.Center).Shrink(0).
					Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
				pressFeedback(trig, t)
				trig.Children(func() {
					ui.Text(c, strings.ToUpper(a.hashAlgo)).FontSize(13).Grow(1)
					ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
				})
				algoSel.Popup(func(panel ui.Element) {
					panel.MinWidth(150).Padding(4).Radius(10).
						Background(t.Surface).Border(1, t.Border)
					for _, option := range []string{"md5", "sha1", "sha256", "sha384", "sha512", "crc32"} {
						opt := option
						item := algoSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
							Cursor(ui.CursorPointer)
						switch {
						case item.Highlighted():
							item.Background(t.Accent).TextColor(t.AccentText)
						case a.hashAlgo == opt:
							item.Background(t.SurfaceHover)
						}
						item.Children(func() {
							ui.Text(c, strings.ToUpper(opt)).FontSize(13)
						})
					}
				})
			})
		})
		// 单文件时可选期望值校验
		if len(a.hashInputs) == 1 {
			openSettingRow(c, t, "期望值", "留空=只计算", func() {
				settingControl(c, t, expectIssue, func() {
					ui.TextInputBase(c, &a.hashExpect).
						Placeholder(strings.Repeat("0", 16)+"…（可选）").
						Grow(1).FontSize(13).Padding(5, 10).
						Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
				})
			})
		}
		// 结果 / 错误
		if a.hashErr != "" {
			ui.Row(c).Margin(10, 0, 0, 0).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Image(c, iconError).Size(14, 14)
				ui.Text(c, a.hashErr).FontSize(13).TextColor(t.Danger).Grow(1).Wrap()
			})
		}
		for _, res := range a.hashResults {
			result := res
			ui.Column(c).Margin(10, 0, 0, 0).Gap(4).
				Background(t.Surface).Border(1, t.Border).Radius(10).Padding(10, 12).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Image(c, iconDone).Size(14, 14)
					// m07774 用户定案：结果卡注明完整路径+算法（原先只显基名）
					pathCell(c, result.Path)
					ui.Text(c, result.Algo).FontSize(11).Font(fontMonoFamily+", monospace").
						TextColor(t.TextMuted).
						Background(t.SurfaceHover).Radius(4).Padding(2, 5).Shrink(0)
					if result.Matched != nil {
						if *result.Matched {
							ui.Text(c, "匹配").FontSize(12).TextColor(hexA("#65B85D", 1))
						} else {
							ui.Text(c, "不匹配").FontSize(12).TextColor(t.Danger)
						}
					}
					if ui.Button(c, "复制").FontSize(12).Height(22).Padding(0, 8).
						TextColor(t.TextMuted).Border(1, t.Border).Radius(6).
						Cursor(ui.CursorPointer).Clicked() {
						mygo.Clipboard.WriteText(result.Hash)
						c.Toast("已复制哈希值")
					}
				})
				ui.Text(c, result.Hash).FontSize(12).Font(fontMonoFamily + ", monospace").TextColor(t.TextMuted).Wrap()
			})
		}
		// CTA
		ctaRow(c).Children(func() {
			ready := len(a.hashInputs) > 0 && !a.hashBusy && expectIssue == ""
			if primaryCTA(c, t, "计算哈希", ready).Clicked() && ready {
				a.runHash()
			}
		})
	})
}

func (a *app) runHash() {
	inputs := append([]string(nil), a.hashInputs...)
	algo := a.hashAlgo
	expect := strings.TrimSpace(a.hashExpect)
	a.hashBusy = true
	a.hashResults = nil
	a.hashErr = ""
	a.win.Update(func() {})
	args := []string{"hash", "--algorithm", algo}
	args = append(args, inputs...)
	if len(inputs) == 1 && expect != "" {
		args = append(args, "--expect", strings.ToLower(expect))
	}
	a.service.HashExec(args, func(entries []HashEntry, isMatch *bool, errMsg string) {
		a.win.Update(func() {
			a.hashBusy = false
			if !samePaths(inputs, a.hashInputs) {
				return // 计算期间清单变了：这批结果对应的是旧清单，不挂到新清单下
			}
			if errMsg != "" {
				a.hashErr = errMsg
				return
			}
			for _, entry := range entries {
				a.hashResults = append(a.hashResults, hashResult{
					Path: entry.Path, Hash: entry.Hash, Size: entry.Size,
					Algo: strings.ToUpper(algo), Matched: isMatch,
				})
			}
		})
	})
}

// samePaths 报告两组路径是否逐项相同（顺序也须一致）。
func samePaths(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for idx, path := range first {
		if path != second[idx] {
			return false
		}
	}
	return true
}

// addHashFiles 把文件加入哈希清单（去重）。引擎只对文件算哈希——文件夹不入列，
// 当场 toast 说明，而不是等计算后才报错。
func (a *app) addHashFiles(files []string) {
	added, skippedDirs := 0, 0
	for _, path := range files {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			skippedDirs++
			continue
		}
		dup := false
		for _, existing := range a.hashInputs {
			if existing == path {
				dup = true
				break
			}
		}
		if !dup {
			a.hashInputs = append(a.hashInputs, path)
			added++
		}
	}
	if skippedDirs > 0 {
		a.queueToast("已忽略 " + itoa(skippedDirs) + " 个文件夹——哈希只对文件计算")
	}
	if added == 0 && skippedDirs == 0 {
		return
	}
	a.hashResults = nil
	a.hashErr = ""
	a.win.Update(func() {})
}

// browseHash 哈希页的「浏览选择…」：文件多选。此前误传 directory=true 弹的是
// 文件夹框，而引擎拒绝对目录算哈希——这个按钮永远选不到能算的东西（2026-11-23 审计）。
func (a *app) browseHash() {
	a.toolBrowseMany("选择要计算哈希的文件", false, true, a.addHashFiles)
}

func (a *app) setPdfRotatePath(path string) {
	if a.rejectIfForeign(path, "pdf") { // 内容判格守卫（m00961）：明确非PDF → toast 拒收
		return
	}
	a.pdfRotatePath = path
	if !a.pdfRotateOutDirty {
		a.pdfRotateOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browsePdfRotate() {
	a.toolBrowse("选择 PDF 文件", false, a.setPdfRotatePath)
}

func (a *app) setPdfResizePath(path string) {
	if a.rejectIfForeign(path, "pdf") { // 内容判格守卫（m00961）：明确非PDF → toast 拒收
		return
	}
	a.pdfResizePath = path
	if !a.pdfResizeOutDirty {
		a.pdfResizeOut = ""
	}
	a.win.Update(func() {})
}

func (a *app) browsePdfResize() {
	a.toolBrowse("选择 PDF 文件", false, a.setPdfResizePath)
}

// ── 页面：随机生成（crypto password/uuid 的 GUI 面）──

// 随机密码字符集：原生多选下拉——四个内置字符类 + 「自定义」子菜单（按符号分组挑选）。
const (
	PW_CHARSET_LOWER  = "lower"   // 小写字母
	PW_CHARSET_UPPER  = "upper"   // 大写字母
	PW_CHARSET_DIGIT  = "digit"   // 数字
	PW_CHARSET_SYMBOL = "symbol"  // 符号（引擎内置符号集）
	PW_CHARSET_CUSTOM = "custom:" // 自定义分组的状态键前缀，后接分组 id
	PW_CHARSET_NONE   = "未选择"     // 一项都没勾时触发器上的占位
)

// pwCharset 是多选下拉里的一个内置字符类。
type pwCharset struct {
	id         string // 状态键
	label      string // 触发器摘要里的短名
	title      string // 菜单项全名
	hint       string // 菜单项右侧的字符示例
	engineFlag string // 引擎 crypto password 的对应旗标
}

// pwCharsets 是内置字符类，顺序即菜单顺序。
var pwCharsets = []pwCharset{
	{id: PW_CHARSET_LOWER, label: "小写", title: "小写字母", hint: "a–z", engineFlag: "--lower"},
	{id: PW_CHARSET_UPPER, label: "大写", title: "大写字母", hint: "A–Z", engineFlag: "--upper"},
	{id: PW_CHARSET_DIGIT, label: "数字", title: "数字", hint: "0–9", engineFlag: "--digit"},
	{id: PW_CHARSET_SYMBOL, label: "符号", title: "符号", hint: "!@#$%^&*…", engineFlag: "--symbol"},
}

// pwCustomGroup 是「自定义」子菜单里的一个符号分组；五个分组合起来正好是全部 32 个 ASCII 符号。
type pwCustomGroup struct {
	id    string // 状态键后缀
	title string // 菜单项全名
	chars string // 该组的字符（也是菜单项右侧的示例）
}

// pwCustomGroups 是「自定义」子菜单的符号分组，顺序即菜单顺序。
var pwCustomGroups = []pwCustomGroup{
	{id: "punct", title: "标点", chars: ".,:;!?"},
	{id: "bracket", title: "括号", chars: "()[]{}<>"},
	{id: "math", title: "运算", chars: "+-=*/\\^_|~"},
	{id: "quote", title: "引号", chars: "'\"`"},
	{id: "money", title: "货币等", chars: "$#@%&"},
}

// defaultPwCharsets 是字符集多选的缺省值：四个内置类全开。
func defaultPwCharsets() map[string]bool {
	charsets := map[string]bool{}
	for _, charset := range pwCharsets {
		charsets[charset.id] = true
	}
	return charsets
}

// pwCharsetAny 报告是否至少勾了一项（内置类或自定义分组）。
func (a *app) pwCharsetAny() bool {
	for _, charset := range pwCharsets {
		if a.pwCharsets[charset.id] {
			return true
		}
	}
	return a.pwCustomCount() > 0
}

// pwCustomCount 是勾选的自定义分组数。
func (a *app) pwCustomCount() int {
	count := 0
	for _, group := range pwCustomGroups {
		if a.pwCharsets[PW_CHARSET_CUSTOM+group.id] {
			count++
		}
	}
	return count
}

// pwCustomCharset 是勾选的自定义分组合成的字符集（分组顺序，字符去重）；没勾返回空串。
func (a *app) pwCustomCharset() string {
	seen := map[rune]bool{}
	var charset strings.Builder
	for _, group := range pwCustomGroups {
		if !a.pwCharsets[PW_CHARSET_CUSTOM+group.id] {
			continue
		}
		for _, char := range group.chars {
			if !seen[char] {
				seen[char] = true
				charset.WriteRune(char)
			}
		}
	}
	return charset.String()
}

// pwCharsetSummary 是触发器上显示的已选摘要；一项都没勾显示占位。
func (a *app) pwCharsetSummary() string {
	var labels []string
	for _, charset := range pwCharsets {
		if a.pwCharsets[charset.id] {
			labels = append(labels, charset.label)
		}
	}
	if a.pwCustomCount() > 0 {
		labels = append(labels, "自定义")
	}
	if len(labels) == 0 {
		return PW_CHARSET_NONE
	}
	// 间隔点不带空格：最小窗口下控位只有约 140 宽，带空格的缺省摘要会被省略号截掉末项
	return strings.Join(labels, "·")
}

// pwPasswordArgs 是「生成密码」给引擎的参数：长度 + 没勾的内置类显式关掉（引擎缺省全开）
// + 自定义分组经 --charset 追加。
func (a *app) pwPasswordArgs() []string {
	args := []string{"crypto", "password", "--length", a.pwLenStr}
	for _, charset := range pwCharsets {
		if !a.pwCharsets[charset.id] {
			args = append(args, charset.engineFlag, "false")
		}
	}
	if custom := a.pwCustomCharset(); custom != "" {
		args = append(args, "--charset", custom)
	}
	return args
}

// pwUUIDArgs 是「生成 UUID」给引擎的参数：按输出形态追加旗标。
func (a *app) pwUUIDArgs() []string {
	args := []string{"crypto", "uuid"}
	switch a.uuidFmt {
	case "大写":
		args = append(args, "--upper")
	case "无连字符":
		args = append(args, "--no-dash")
	case "大写无连字符":
		args = append(args, "--no-dash", "--upper")
	}
	return args
}

// pwMenuPanel 给菜单面板套上主题菜单的观感（同 mygo PopoverMenuButton 的面板配方）。
// 宽度取紧：默认窗口下主菜单落在内容区右半，面板窄到 180 左右子菜单才开得在右侧而不翻到侧栏上。
func pwMenuPanel(c *ui.Context, t *ui.Theme, panel ui.Element) {
	panel.MinWidth(180).Padding(t.Space(1)).Radius(t.Radius+2).
		Background(t.Background).Border(1, t.Border).
		Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
}

// pwMenuLook 给菜单项套上主题菜单的行观感：高亮=强调色底反白字。
// 返回该项对勾 / 主文字与次要文字该用的颜色。
func pwMenuLook(c *ui.Context, t *ui.Theme, item ui.Element, label string) (ui.Color, ui.Color) {
	item.AlignItems(ui.Center).Gap(t.Space(2)).Padding(t.Space(1.5), t.Space(2)).
		Radius(t.Radius).Shrink(0).Label(label)
	if item.Highlighted() {
		item.Background(t.Accent).TextColor(t.AccentText)
		return t.AccentText, t.AccentText
	}
	return t.Text, t.TextMuted
}

// pwMenuMark 是菜单项行首的对勾位：勾上画对勾，没勾留空——各行文字因此对齐。
func pwMenuMark(c *ui.Context, on bool, color ui.Color) {
	ui.Row(c).Width(16).Height(16).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		if on {
			ui.Icon(c, iconCheck).Size(12, 12).TextColor(color)
		}
	})
}

// pwMenuHint 是菜单项右侧的次要文字（等宽字体：字符示例 / 分组数）。
func pwMenuHint(c *ui.Context, text string, color ui.Color) {
	ui.Text(c, text).Font(fontMonoFamily + ", monospace").FontSize(12).TextColor(color).Shrink(0)
}

// pwCharsetSelect 字符集多选下拉。机件合规：mygo MenuBase + CheckItem + KeepOpen（勾选不关
// 菜单）+ SubmenuItem/Submenu（「自定义」子菜单）；观感照 PopoverMenuButton 的主题配方。
func (a *app) pwCharsetSelect(c *ui.Context) {
	t := c.Theme()
	// 触发器占满控位但封顶：摘要过长时省略号截断，窄窗口下不撑破行
	ui.Row(c).Grow(1).MaxWidth(232).MinWidth(0).Children(func() {
		menu := ui.MenuBase(c, &a.csMenuOpen)
		trigger := menu.Trigger.
			Grow(1).Height(32).MinWidth(0).Radius(8).Padding(0, 10).Gap(6).
			AlignItems(ui.Center).Label("字符集").
			Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
		pressFeedback(trigger, t)
		trigger.Children(func() {
			summary := a.pwCharsetSummary()
			ui.Text(c, summary).FontSize(13).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").
				TextColor(map[bool]ui.Color{true: t.TextMuted, false: t.Text}[summary == PW_CHARSET_NONE])
			ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
		})
		menu.Popup(func(panel ui.Element) {
			pwMenuPanel(c, t, panel)
			for _, charset := range pwCharsets {
				on := a.pwCharsets[charset.id]
				item := menu.CheckItem(on).KeepOpen()
				fore, muted := pwMenuLook(c, t, item, charset.title)
				item.Children(func() {
					pwMenuMark(c, on, fore)
					ui.Text(c, charset.title).FontSize(13).Grow(1).SingleLine()
					pwMenuHint(c, charset.hint, muted)
				})
				if item.Clicked() {
					a.pwCharsets[charset.id] = !on
				}
			}
			ui.Box(c).Height(1).Background(t.Border).Margin(t.Space(1), t.Space(2))
			// 「自定义」：子菜单里按符号分组挑选
			customItem := menu.SubmenuItem()
			_, muted := pwMenuLook(c, t, customItem, "自定义")
			customItem.Children(func() {
				ui.Row(c).Width(16).Shrink(0)
				ui.Text(c, "自定义").FontSize(13).Grow(1).SingleLine()
				if count := a.pwCustomCount(); count > 0 {
					pwMenuHint(c, fmt.Sprintf("%d 组", count), muted)
				}
				ui.Icon(c, iconChevronRight).Size(10, 10).TextColor(muted)
			})
			menu.Submenu(customItem, func(sub ui.Element) {
				pwMenuPanel(c, t, sub)
				for _, group := range pwCustomGroups {
					key := PW_CHARSET_CUSTOM + group.id
					on := a.pwCharsets[key]
					item := menu.CheckItem(on).KeepOpen()
					fore, muted := pwMenuLook(c, t, item, group.title)
					item.Children(func() {
						pwMenuMark(c, on, fore)
						ui.Text(c, group.title).FontSize(13)
						ui.Box(c).Grow(1)
						pwMenuHint(c, group.chars, muted)
					})
					if item.Clicked() {
						a.pwCharsets[key] = !on
					}
				}
			})
		})
	})
}

// pageRandom 随机生成页：段选 密码/UUID；密码=长度档+字符集多选（内置四类 + 自定义符号分组），
// UUID=v4 一键出。
func (a *app) pageRandom(c *ui.Context) {
	t := c.Theme()
	if a.pwCharsets == nil {
		a.pwCharsets = defaultPwCharsets()
	}
	column := pageColumn(c)
	column.Children(func() {
		ui.Row(c).Padding(14, 0, 4, 0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "随机密码与 UUID，生成后点击复制").FontSize(13).TextColor(t.TextMuted)
		})

		toolSegmented(c, t, &a.randomModeIdx, "密码", "UUID")

		paramCard := ui.Column(c).Margin(12, 0, 0, 0).Gap(6)
		paramCard.Children(func() {
			if a.randomModeIdx == 0 {
				openSettingRow(c, t, "长度", "密码字符数", func() {
					ui.Row(c).Grow(1).Children(func() {
						lenSel := ui.SelectBase(c, &a.pwLenStr)
						trig := lenSel.Trigger.
							Height(32).MinWidth(100).Radius(8).Padding(0, 10).Gap(6).
							AlignItems(ui.Center).Shrink(0).
							Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
						pressFeedback(trig, t)
						trig.Children(func() {
							ui.Text(c, a.pwLenStr).FontSize(13).Grow(1)
							ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
						})
						lenSel.Popup(func(panel ui.Element) {
							panel.MinWidth(110).Padding(4).Radius(10).
								Background(t.Surface).Border(1, t.Border)
							for _, option := range []string{"8", "12", "16", "24", "32", "64"} {
								opt := option
								item := lenSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
									Cursor(ui.CursorPointer)
								switch {
								case item.Highlighted():
									item.Background(t.Accent).TextColor(t.AccentText)
								case a.pwLenStr == opt:
									item.Background(t.SurfaceHover)
								}
								item.Children(func() {
									ui.Text(c, opt).FontSize(13)
								})
							}
						})
					})
				})
				openSettingRow(c, t, "字符集", "至少勾选一类", func() {
					a.pwCharsetSelect(c)
				})
			} else {
				openSettingRow(c, t, "格式", "大小写与连字符", func() {
					ui.Row(c).Grow(1).Children(func() {
						fmtSel := ui.SelectBase(c, &a.uuidFmt)
						trig := fmtSel.Trigger.
							Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
							AlignItems(ui.Center).Shrink(0).
							Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
						pressFeedback(trig, t)
						trig.Children(func() {
							ui.Text(c, a.uuidFmt).FontSize(13).Grow(1)
							ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
						})
						fmtSel.Popup(func(panel ui.Element) {
							panel.MinWidth(150).Padding(4).Radius(10).
								Background(t.Surface).Border(1, t.Border)
							for _, option := range []string{"标准", "大写", "无连字符", "大写无连字符"} {
								opt := option
								item := fmtSel.Item(opt).Height(30).Padding(0, 10).Radius(6).
									Cursor(ui.CursorPointer)
								switch {
								case item.Highlighted():
									item.Background(t.Accent).TextColor(t.AccentText)
								case a.uuidFmt == opt:
									item.Background(t.SurfaceHover)
								}
								item.Children(func() {
									ui.Text(c, opt).FontSize(13)
								})
							}
						})
					})
				})
			}
		})

		// CTA
		ready := !a.randomBusy && (a.randomModeIdx != 0 || a.pwCharsetAny())
		ctaRow(c).Children(func() {
			label := map[bool]string{true: "生成密码", false: "生成 UUID"}[a.randomModeIdx == 0]
			if primaryCTA(c, t, label, ready).Clicked() && ready {
				a.randomBusy = true
				a.randomErr = ""
				a.randomOut = ""
				args := a.pwUUIDArgs()
				if a.randomModeIdx == 0 {
					args = a.pwPasswordArgs()
				}
				a.service.RunToolText(args, func(text, errMsg string) {
					mygo.RunOnMain(func() {
						a.randomBusy = false
						a.randomErr = errMsg
						a.randomOut = text
						a.win.Invalidate()
					})
				})
			}
		})

		// 结果 / 错误
		if a.randomErr != "" {
			ui.Row(c).Margin(10, 0, 0, 0).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Image(c, iconError).Size(14, 14)
				ui.Text(c, a.randomErr).FontSize(13).TextColor(t.Danger).Grow(1).Wrap()
			})
		}
		if a.randomOut != "" {
			ui.Column(c).Margin(10, 0, 0, 0).Gap(4).
				Background(t.Surface).Border(1, t.Border).Radius(10).Padding(10, 12).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.randomOut).FontSize(13).Font(fontMonoFamily + ", monospace").Grow(1).Wrap()
					copyButton(c, t, a.randomOut)
				})
			})
		}
	})
}

func (a *app) pageArchive(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		// 引导行（压缩台同款描边钮抄齐 m07672）
		ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "已浏览 1 个归档，可继续拖拽或").FontSize(13).TextColor(t.TextMuted)
			leadBrowseButton(c, t, "浏览选择…", func() { a.browseArchive2() })
			ui.Box(c).Grow(1)
		})
		// 归档卡（尾槽移除徽章，同解压台范式）
		removeArchive := false
		card := ui.Column(c).Margin(12, 0, 0, 0).Radius(10).
			Background(t.Surface).Border(1, t.Border)
		card.Children(func() {
			row := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(9, 12)
			if row.Hovered() {
				row.Background(t.SurfaceHover)
			}
			row.Children(func() {
				ui.Image(c, iconFile).Size(14, 14)
				pathCell(c, a.archivePath)
				del := ui.ButtonBase(c).Absolute().Right(0).Size(24, 24).Radius(6).
					Cursor(ui.CursorPointer).Background(ui.RGBA(0, 0, 0, 0))
				del.Opacity(del.Animate("vis", revealTarget(row.Hovered()), 110*time.Millisecond))
				del.Children(func() {
					ix := ui.Image(c, iconRemoveBadge).Size(14, 14)
					scaleTarget := float32(1)
					if del.Hovered() {
						scaleTarget = 1.3
					}
					scale := del.Animate("scale", scaleTarget, 140*time.Millisecond)
					ix.Scale(scale, scale)
				})
				if del.Clicked() {
					removeArchive = true
				}
			})
		})
		if removeArchive {
			a.clearArchiveBrowse()
		}
		// 体检行
		probeText := ""
		if a.archiveBusy {
			probeText = "读取中…"
		} else if a.archiveListing != nil {
			listing := a.archiveListing
			if listing.Err != "" {
				probeText = listing.Err + " · 仍可尝试校验"
			} else {
				probeText = strings.ToUpper(listing.Format) + " · " + itoa(listing.EntryCount) + " 个文件 · 解压后 " +
					humanSize(listing.TotalBytes) + " · 归档 " + humanSize(listing.ArchiveSize)
				if listing.Encrypted {
					probeText += " · 已加密"
				}
			}
		}
		if probeText != "" {
			ui.Row(c).Margin(10, 0, 0, 0).Gap(6).AlignItems(ui.Center).Children(func() {
				probeColor := t.TextMuted
				if a.archiveListing != nil && a.archiveListing.Err != "" {
					probeColor = t.Danger
				}
				ui.Text(c, probeText).FontSize(13).TextColor(probeColor).Grow(1).Wrap()
			})
		}
		// 校验行
		ui.Row(c).Margin(10, 0, 0, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			btn := ui.Button(c, "校验归档").FontSize(13).Height(26).Padding(0, 10).
				Background(t.Surface).Border(1, t.Border).Radius(8).
				Cursor(ui.CursorPointer)
			if a.archiveTest == 1 {
				btn.TextColor(t.TextMuted).Cursor(0)
			}
			if btn.Clicked() && a.archiveTest != 1 {
				a.runArchiveTest()
			}
			switch a.archiveTest {
			case 1:
				ui.Text(c, "校验中…").FontSize(13).TextColor(t.TextMuted)
			case 2:
				ui.Image(c, iconDone).Size(14, 14)
				ui.Text(c, a.archiveTestMsg).FontSize(13).TextColor(hexA("#65B85D", 1))
			case 3:
				ui.Image(c, iconError).Size(14, 14)
				ui.Text(c, a.archiveTestMsg).FontSize(13).TextColor(t.Danger).Grow(1).Wrap()
			}
		})
		if len(a.archiveFails) > 0 {
			ui.Column(c).Margin(8, 0, 0, 0).Gap(4).Children(func() {
				for idx, failure := range a.archiveFails {
					if idx >= 3 {
						ui.Text(c, "…另有 "+itoa(len(a.archiveFails)-3)+" 条损坏明细").FontSize(12).TextColor(t.TextMuted)
						break
					}
					ui.Text(c, failure).FontSize(12).TextColor(t.Danger).Wrap()
				}
			})
		}
		// 内容清单
		if a.archiveListing != nil && a.archiveListing.Err == "" && len(a.archiveListing.Entries) > 0 {
			listing := a.archiveListing
			listCard := ui.Column(c).Margin(14, 0, 0, 0).
				Background(t.Surface).Border(1, t.Border).Radius(10).Clip()
			listCard.Children(func() {
				header := ui.Row(c).Padding(9, 12).AlignItems(ui.Center).
					Border(1, t.Border).BorderWidth(0, 0, 1, 0)
				header.Children(func() {
					ui.Text(c, "内容清单 · "+itoa(listing.EntryCount)+" 条").FontSize(13).Grow(1)
				})
				limit := len(listing.Entries)
				if limit > 500 {
					limit = 500
				}
				for idx := 0; idx < limit; idx++ {
					entry := listing.Entries[idx]
					last := idx == limit-1 && len(listing.Entries) <= 500
					row := ui.Row(c).Padding(6, 12).Gap(8).AlignItems(ui.Center)
					if !last {
						row.Border(1, t.Border).BorderWidth(0, 0, 1, 0)
					}
					if row.Hovered() {
						row.Background(t.SurfaceHover)
					}
					row.Children(func() {
						ui.Image(c, iconFile).Size(12, 12)
						pathCell(c, entry.Name)
						ui.Text(c, humanSize(entry.Size)).FontSize(12).Font(fontMonoFamily + ", monospace").TextColor(t.TextMuted)
					})
				}
				if len(listing.Entries) > 500 {
					footer := ui.Row(c).Padding(6, 12)
					footer.Children(func() {
						ui.Text(c, "…仅显示前 500 条，共 "+itoa(listing.EntryCount)+" 条").
							FontSize(12).TextColor(t.TextMuted)
					})
				}
			})
		}
	})
}

func (a *app) setArchivePath(path string) {
	a.archivePath = strings.TrimSuffix(path, "/")
	a.archiveListing = nil
	a.archiveTest = 0
	a.archiveTestMsg = ""
	a.archiveFails = nil
	a.win.Update(func() {})
	if a.archivePath == "" {
		return
	}
	a.archiveBusy = true
	pathCopy := a.archivePath
	a.service.ListArchive(pathCopy, func(result ListResult) {
		a.win.Update(func() {
			a.archiveBusy = false
			if a.archivePath != pathCopy {
				return // 过期结果丢弃
			}
			a.archiveListing = &result
			a.toastEngineVerdict(filepath.Base(pathCopy), result.Err) // 引擎判格浮出（m00961）
		})
	})
}

func (a *app) clearArchiveBrowse() {
	a.archivePath = ""
	a.archiveListing = nil
	a.archiveTest = 0
	a.archiveTestMsg = ""
	a.archiveFails = nil
	a.win.Update(func() {})
}

func (a *app) browseArchive2() {
	a.toolBrowse("选择归档文件", false, a.setArchivePath)
}

func (a *app) runArchiveTest() {
	path := a.archivePath
	a.archiveTest = 1
	a.archiveTestMsg = ""
	a.archiveFails = nil
	a.win.Update(func() {})
	a.service.TestArchive(path, func(ok bool, tested int, failures []string, errMsg string) {
		a.win.Update(func() {
			if a.archivePath != path {
				return
			}
			if errMsg != "" && len(failures) == 0 {
				a.archiveTest = 3
				a.archiveTestMsg = errMsg
				return
			}
			if ok {
				a.archiveTest = 2
				a.archiveTestMsg = "全部通过 · " + itoa(tested) + " 条目"
				return
			}
			a.archiveTest = 3
			a.archiveTestMsg = itoa(len(failures)) + " 处损坏"
			a.archiveFails = failures
		})
	})
}

// convertLeadRow 引导行：已选定 <kind> <路径> · 可继续拖拽或[浏览选择…]
func (a *app) convertLeadRow(c *ui.Context, kind, path string, browse func()) {
	// 压缩台同款句式抄齐（m07672「没细节」）：状态句带体积+描边浏览钮
	//（hover Accent 描边+Accent 字，与压缩台/刻录逐字节同款）
	t := c.Theme()
	info := a.entryInfoOf(path)
	sizeLabel := humanSize(info.size)
	if info.dir {
		sizeLabel = "文件夹"
		a.ensureDirSize(path)
	}
	ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "已准备的"+kind+" 1 项共 "+sizeLabel+"，可拖拽加入或").
			FontSize(13).TextColor(t.TextMuted)
		leadBrowseButton(c, t, "浏览选择…", func() { browse() })
	})
}

// convertFormatRow 输出格式下拉（SelectBase 触发器与分卷/格式族同观感）
func (a *app) convertFormatRow(c *ui.Context, t *ui.Theme, dst *string, formats ...string) {
	sel := ui.SelectBase(c, dst)
	trig := sel.Trigger.
		Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
		AlignItems(ui.Center).Shrink(0).
		Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
	pressFeedback(trig, t)
	trig.Children(func() {
		ui.Text(c, strings.ToUpper(strings.TrimPrefix(*dst, "."))).FontSize(13).Grow(1)
		ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.MinWidth(150).Padding(4).Radius(10).
			Background(t.Surface).Border(1, t.Border)
		for _, format := range formats {
			itemFormat := format
			item := sel.Item(itemFormat).Height(30).Padding(0, 10).Radius(6).
				Cursor(ui.CursorPointer)
			switch {
			case item.Highlighted():
				item.Background(t.Accent).TextColor(t.AccentText)
			case *dst == itemFormat:
				item.Background(t.SurfaceHover)
			}
			item.Children(func() {
				ui.Text(c, strings.ToUpper(strings.TrimPrefix(itemFormat, "."))).FontSize(13)
			})
		}
	})
}

// convertOutRow 输出位置：ghost 假绑定（同解压位置）——空态显示默认「源文件旁」
func (a *app) convertOutRow(c *ui.Context, t *ui.Theme, label, planDir string, out *string, dirty *bool, ghost *string, focus *bool) {
	openSettingRow(c, t, label, "默认为源文件旁", func() {
		ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
			MaxHeight(76).
			Background(t.Surface).Border(1, t.Border).Radius(8).
			Children(func() {
				bound := out
				if *out == "" && !*focus {
					*ghost = planDir + "（默认）"
					bound = ghost
				}
				outTa := ui.TextAreaBase(c, bound).
					Grow(1).FontSize(13).Lines(1, 3).Padding(5, 10)
				if bound != out {
					outTa.ReadOnly(true).TextColor(t.TextMuted)
				} else if outTa.Changed() {
					*out = strings.ReplaceAll(*out, "\n", "")
					*dirty = *out != ""
				}
				if outTa.Focused() {
					*focus = true
				} else if *out == "" {
					*focus = false
				}
				browse := pickButton(c, t)
				if browse.Clicked() {
					a.toolBrowse("选择输出位置", true, func(pick string) {
						*out = pick
						*dirty = true
					})
				}
			})
	})
}

// convertCTARow 主行动钮（压缩台 CTA 同族）：框架 ui.PrimaryButton 自带悬停/按下换脸。
// 此前每帧无条件写死 Background，把换脸关掉了——12 个转换页的主按钮既无悬停也无按下
// （2026-11-23 审计实测）。置灰态才覆盖配色，并去掉手型光标。
func (a *app) convertCTARow(c *ui.Context, t *ui.Theme, label string, enabled bool, reset func(), run func()) {
	ctaRow(c).Children(func() {
		if reset != nil {
			cancelButton(c, t, "取消", reset) // 回到空态（同压缩台「取消压缩」）
		}
		cta := primaryCTA(c, t, label, enabled)
		if enabled && cta.Clicked() {
			run()
		}
	})
}

// convertOutput 目标路径：目录（自定义或源旁）+ 源基名（去扩展）+ 新扩展；
// 已存在自动加序号（同压缩台 uniquePath 语义）
func (a *app) convertOutput(src, format, outDir string) string {
	dir := strings.TrimSpace(outDir)
	if dir == "" {
		dir = filepath.Dir(src)
	}
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	target := filepath.Join(dir, base+format)
	return uniquePath(target)
}

// setMediaPath/setImagePath 选定/拖入：换源重置输出位置脏状态
func (a *app) setMediaPath(path string) {
	if a.rejectIfForeign(path, "av") { // 内容判格守卫（m00961）：明确非音视频 → toast 拒收
		return
	}
	a.mediaPath = path
	if !a.mediaOutDirty {
		a.mediaOut = ""
	}
	a.mediaOutFocus = false
}

func (a *app) setImagePath(path string) {
	if a.rejectIfForeign(path, "image") { // 内容判格守卫（m00961）：明确非图片 → toast 拒收
		return
	}
	a.imagePath = path
	if !a.imageOutDirty {
		a.imageOut = ""
	}
	a.imageOutFocus = false
}

func (a *app) browseMedia() {
	a.toolBrowse("选择视频文件", false, a.setMediaPath)
}

func (a *app) browseImage() {
	a.toolBrowse("选择图片文件", false, a.setImagePath)
}

// ── 页面：归档浏览（M2 占位；空态走 heroPane）──

// ── 页面：处理台（空态走 heroPane；卡片间 10px 呼吸）──
func (a *app) pageTasks(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {
		cards := ui.Column(c).Gap(10).Padding(14, 0, 0, 0)
		cards.Children(func() {
			// 清理已结束任务（G 项修复：卡片不再永远堆着）
			finished := 0
			for _, task := range a.tasks {
				if task.View().State != TaskRunning {
					finished++
				}
			}
			if finished > 0 {
				ui.Row(c).Margin(0, 0, 2, 0).Children(func() {
					clearLink := ui.ButtonBase(c).Height(22).Padding(0, 8).Radius(6).
						Cursor(ui.CursorPointer).Background(ui.RGBA(0, 0, 0, 0))
					pressFeedback(clearLink, t)
					clearLink.Children(func() {
						ui.Text(c, "清掉已结束的 "+itoa(finished)+" 项").FontSize(12).
							TextColor(t.TextMuted)
					})
					if clearLink.Clicked() {
						kept := a.tasks[:0]
						for _, task := range a.tasks {
							if task.View().State == TaskRunning {
								kept = append(kept, task)
							}
						}
						a.tasks = kept
					}
				})
			}
			for _, task := range a.tasks {
				view := task.View() // 只读快照：任务字段由引擎 goroutine 在写
				ui.Column(c).Gap(10).Padding(14, 16).Radius(10).Background(t.Surface).Children(func() {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, view.Title).FontSize(13).Font(fontMiSansVFFamily).Bold().SingleLine().
							Ellipsis("…").Grow(1).MinWidth(0)
						switch view.State {
						case TaskRunning:
							ui.Text(c, view.Detail).FontSize(13).TextColor(t.TextMuted)
						case TaskDone, TaskFailed, TaskCancelled:
							// 状态符号一律 SVG（交互铁律：禁止 Unicode 字形当图标）
							tone := hexA("#65B85D", 1)
							if view.State == TaskFailed {
								ui.Image(c, iconError).Size(11, 11) // m05049 定案：红圆白叉
								tone = t.Danger
							} else if view.State == TaskCancelled {
								ui.Image(c, iconCross).Size(11, 11).TextColor(t.TextMuted) // 用户主动取消：中性灰，不与失败混同
								tone = t.TextMuted
							} else {
								ui.Image(c, iconDone).Size(11, 11) // m05014 原图
							}
							ui.Text(c, view.Result).FontSize(13).TextColor(tone)
						}
						if view.State == TaskRunning {
							cancelTask := ui.ButtonBase(c).Height(22).Padding(0, 8).Radius(6).Shrink(0).
								Cursor(ui.CursorPointer).Background(ui.RGBA(0, 0, 0, 0))
							pressFeedback(cancelTask, t)
							cancelTask.Children(func() {
								ui.Text(c, "取消").FontSize(12).TextColor(t.TextMuted)
							})
							if cancelTask.Clicked() {
								task.Cancel()
							}
						} else if view.State == TaskDone && view.Product != "" {
							showTask := ui.ButtonBase(c).Height(22).Padding(0, 8).Radius(6).Shrink(0).
								Cursor(ui.CursorPointer).Background(ui.RGBA(0, 0, 0, 0))
							pressFeedback(showTask, t)
							showTask.Children(func() {
								ui.Text(c, revealLabel()).FontSize(12).TextColor(t.TextMuted)
							})
							if showTask.Clicked() {
								revealPath(view.Product)
							}
						}
					})
					if view.State == TaskRunning {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Progress(c, view.Percent/100).Grow(1)
							ui.Text(c, percentText(view.Percent)).Font(fontMonoFamily + ", monospace").FontSize(13).TextColor(t.TextMuted)
						}).AlignSelf(ui.Stretch)
					}
				}).AlignSelf(ui.Stretch)
			}
		})
	})
}

// ── 页面：设置（实底信息；参数记忆后续版本交付）──
func (a *app) pageSettings(c *ui.Context) {
	t := c.Theme()
	column := pageColumn(c)
	column.Children(func() {

		// 引擎版本懒取：首次进设置页查一次
		if a.engineVersion == "" && !a.engineQueried {
			a.engineQueried = true
			a.service.RunToolText([]string{"--version"}, func(text, errMsg string) {
				mygo.RunOnMain(func() {
					if errMsg == "" {
						a.engineVersion = strings.TrimSpace(text)
					} else {
						a.engineVersion = "不可用"
					}
					a.win.Invalidate()
				})
			})
		}
		versionShown := a.engineVersion
		if versionShown == "" {
			versionShown = "查询中…"
		}

		paramCard := ui.Column(c).Margin(12, 0, 0, 0).Gap(4)
		paramCard.Children(func() {
			// 外观可调（2026-11-22 用户定案）：三态切换，切换即施
			// 加 Theme.SetSource 并落盘 gui-settings.json（appearance.go）
			// 控件同解密/加密「输入方式」toolSegmented（m00376）。
			openSettingRow(c, t, "外观", "深浅色来源", func() {
				themeBefore := a.themeMode
				toolSegmented(c, t, &a.themeMode, themeModeNames...)
				if a.themeMode != themeBefore { // 即时模式无 Changed 句柄，前后值对比检变化
					mygo.Theme.SetSource(themeSource(a.themeMode))
					saveThemeMode(a.themeMode)
				}
			})
			openSettingRow(c, t, "引擎版本", "应用内嵌引擎", func() {
				ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
					Background(t.Surface).Border(1, t.Border).Radius(8).Children(func() {
					ui.Text(c, versionShown).FontSize(13).Grow(1).Padding(0, 0, 0, 10)
				})
			})
			openSettingRow(c, t, "引擎路径", "点击复制按钮", func() {
				// 只读展示 + 显式复制钮：此前是整行可点的裸 Row（无悬停/按下、不可键盘
				// 聚焦），看上去却和普通输入框一样（2026-11-23 审计）
				enginePath := a.service.ZpackPath()
				ui.Row(c).Grow(1).MinWidth(FIELD_MIN_WIDTH).AlignItems(ui.Center).MinHeight(32).Gap(6).
					Background(t.Surface).Border(1, t.Border).Radius(8).Padding(0, 10).
					Children(func() {
						if enginePath == "" {
							ui.Text(c, "未找到——将引擎放入应用同目录后重启（或重装）").FontSize(13).
								TextColor(t.Danger).Grow(1).MinWidth(0)
							return
						}
						ui.Text(c, enginePath).Font(fontMonoFamily + ", monospace").FontSize(13).
							TextColor(t.Text).Grow(1).MinWidth(0)
						copyButton(c, t, enginePath)
					})
			})
			openSettingRow(c, t, "开源仓库", "问题反馈与源码", func() {
				// ui.Link 跨平台打开浏览器（此前是 macOS 专属的 open 命令，Windows/Linux 点了没反应）
				ui.Row(c).Grow(1).AlignItems(ui.Center).MinHeight(32).
					Background(t.Surface).Border(1, t.Border).Radius(8).Padding(0, 10).
					Children(func() {
						// 框架 Link 只自带悬停下划线——按下态自己补（字色压深，交互铁律）
						repoLink := ui.Link(c, strings.TrimPrefix(REPO_URL, "https://"), REPO_URL).
							FontSize(13).Cursor(ui.CursorPointer)
						if repoLink.Pressed() {
							repoLink.TextColor(t.AccentPressed)
						}
					})
			})
		})
		ui.Text(c, "任务参数记忆（记住上次的格式/级别/分卷）在后续版本交付").
			FontSize(12).TextColor(t.TextMuted).Margin(10, 0, 0, 0)
	})
}

// launchTask 把任务交给处理台：入列、切页、起监听。引擎缺失时 Start* 返回 nil——
// 此前 15 处调用点对 nil 一律静默，点主按钮毫无反应（2026-11-23 审计）；现在 toast 点名。
// 必须带 go 起监听：watchTask 阻塞到任务收尾，同步调用会冻住建帧（刻录页曾如此）。
func (a *app) launchTask(task *Task) {
	if task == nil {
		a.queueToast("未找到 zpack 引擎——请到「设置」查看引擎位置")
		return
	}
	a.tasks = append(a.tasks, task)
	a.page = pageTasks
	if a.win != nil {
		a.win.Update(func() {})
	}
	go a.watchTask(task, a.donePolicy == "打开文件夹")
}

// watchTask 任务刷新：引擎回调 → 窗口重绘。任务收尾时 Updates 关闭、循环退出，之后做
// 最后一跳：失败 toast 兜底、完成后策略。openFolderAfter 是派发时快照的完成后策略——
// 中途改设置不影响在飞任务。
func (a *app) watchTask(task *Task, openFolderAfter bool) {
	for range task.Updates {
		a.win.Update(func() {})
	}
	a.win.Update(func() {})
	view := task.View()
	switch view.State {
	case TaskFailed:
		// 任务失败 toast 兜底网（用户定案 m00961）：失败绝不可「无事发生过」——
		// 用户在别的页时处理台里的失败不可见，toast 补最后一跳
		reason := view.Result
		if runes := []rune(reason); len(runes) > 120 {
			reason = string(runes[:120]) + "…"
		}
		a.queueToast(view.Title + " 失败：" + reason)
	case TaskDone:
		// 完成后策略（用户定案 m03442）：成功才打开，失败/取消不打扰
		if openFolderAfter && view.Folder != "" {
			openFolder(view.Folder)
		}
	}
}

// revealLabel 按平台给出文件管理器动作文案（用户 m04889 议题①）。
func revealLabel() string {
	switch runtime.GOOS {
	case "windows":
		return "在资源管理器中显示"
	case "linux":
		return "在文件管理器中显示"
	default:
		return "在 Finder 中显示"
	}
}

// revealPath 在系统文件管理器中展示并尽量选中该文件（macOS open -R
// 高亮选中 / Windows explorer /select / Linux 无标准选中退化为开父目录）。
func revealPath(path string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer", "/select,"+path)
	case "linux":
		command = exec.Command("xdg-open", filepath.Dir(path))
	default:
		command = exec.Command("open", "-R", path)
	}
	if err := command.Start(); err != nil {
		log.Println("reveal path:", err)
	}
}

// openFolder 在系统文件管理器中展示目录（完成后策略「打开文件夹」）。
func openFolder(path string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer", path)
	case "linux":
		command = exec.Command("xdg-open", path)
	default:
		command = exec.Command("open", path)
	}
	if err := command.Start(); err != nil {
		log.Println("open folder:", err)
	}
}

// renderDonePolicy 完成后策略选择器（无动作/打开文件夹）——压缩台与
// 解压台共用同一 app 字段：一处改，两处同步。
func (a *app) renderDonePolicy(c *ui.Context, t *ui.Theme) {
	sel := ui.SelectBase(c, &a.donePolicy)
	trig := sel.Trigger.
		Height(28).MinWidth(120).Radius(6).Padding(0, 10).Gap(6).
		AlignItems(ui.Center).Shrink(0).
		Background(t.Surface).Border(1, t.Border).Cursor(ui.CursorPointer)
	pressFeedback(trig, t)
	trig.Children(func() {
		ui.Text(c, a.donePolicy).FontSize(13).Grow(1)
		ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.MinWidth(150).Padding(4).Radius(10).
			Background(t.Surface).Border(1, t.Border)
		for _, policy := range []string{"无动作", "打开文件夹"} {
			item := sel.Item(policy).Height(28).Padding(0, 10).Radius(6).
				Cursor(ui.CursorPointer)
			switch {
			case item.Highlighted():
				item.Background(t.Accent).TextColor(t.AccentText)
			case a.donePolicy == policy:
				item.Background(t.SurfaceHover)
			}
			item.Children(func() {
				ui.Text(c, policy).FontSize(13)
			})
		}
	})
}

// newApp 造应用状态：各页默认值的唯一定义点（main 与测试共用——测试拿到的缺省态
// 与真实启动逐项一致，不在测试里另抄一份）。
func newApp() *app {
	return &app{page: pageCompress, service: NewService(), level: 6, formatStr: ".zip", volumeModeStr: "不分卷",
		pwModeStr: "无密码", donePolicy: "无动作", keyBitsIdx: 1, mediaFmt: ".mp4", imageFmt: ".png",
		mediaCmpMode: "50%", audioCvtFmt: ".mp3", imageCmpMode: "100KB", imageCmpPriority: "size-first",
		officeEngine: "自动", pdfRotateDir: "右转 90°", pdfResizeSize: "a4", hashAlgo: "sha256",
		pwLenStr: "16", uuidFmt: "标准", pwCharsets: defaultPwCharsets(), burnModeStr: "可追加"}
}

func main() {
	application := newApp()
	// ZPACK_GUI_SEED 开发期视觉验证：预填样例清单（sparse 文件即可，不读内容）
	if os.Getenv("ZPACK_GUI_SEED") != "" {
		application.inputs = []string{
			"/tmp/zpack-sample/季度报表.xlsx",
			"/tmp/zpack-sample/设计稿.psd",
			"/tmp/zpack-sample/video-export.mp4",
			"/tmp/zpack-sample/远期交通基础设施建设项目可行性研究汇报材料-最终版-带完整附录与评审意见-请勿外传-内部资料副本20241121重编版.xlsx",
			"/tmp", // 目录样例：验证文件夹图标与「文件夹」标注（开发期常驻，无害）
		}
		application.syncName()
	}
	// 三平台默认字体先于窗口创建注册（内嵌 MiSans VF，失败回退系统字体）
	registerMiSans()
	// 外观偏好先于窗口创建读回（缺文件/损坏回退跟随系统），窗口起来即生效
	application.themeMode = loadThemeMode()
	mygo.App.WhenReady(func() {
		// 裸二进制开发态补 Dock 图标（.app 形态由 icns 提供，此处幂等无害）。
		// Windows 用全出血变体：Dock.SetIcon 在 Windows 后端即任务栏/标题图标
		//（internal/windows/app.go SetDockIcon → w.setIcon），mac 留白版会显小。
		iconApp := iconPNG
		if runtime.GOOS == "windows" {
			iconApp = iconWindowsPNG
		}
		if err := mygo.App.Dock.SetIcon(iconApp); err != nil {
			log.Printf("Dock 图标设置失败: %v", err)
		}
		// 外观来源在窗口创建前施加（Theme.SetSource 启动期一次生效；运行期切换见设置页）
		mygo.Theme.SetSource(themeSource(application.themeMode))
		// 顶部按钮带高度按平台：52 是 mac 红绿灯锚定高度；Windows 原生标题栏
		// 32pt（captionHeight），52 会把最小化/最大化/关闭拉成巨钮——36 给悬停
		// 热区一点余量又贴原生比例（用户 2026-11-22 反馈按钮夸张）。
		titleBarHeight := 52
		if runtime.GOOS == "windows" {
			titleBarHeight = 36
		}
		application.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "zpack",
			Width: 675, Height: 580,
			MinWidth: 500, MinHeight: 480,
			StateKey: "main",
			// macOS 沉浸式：红绿灯内嵌悬于侧栏上；Windows/Linux 控制钮在
			// 顶部按钮带（TitleBarHeight）——两侧位置每帧由 TitleBar() 动态适配
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: titleBarHeight,
			Vibrancy:       mygo.VibrancySidebar,
			Content:        ui.View(application.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// ── 小件 ──

func itoa(n int) string {
	return strconv.Itoa(n)
}

func percentText(percent float64) string {
	return strconv.FormatFloat(percent, 'f', 1, 64) + "%"
}

// revealTarget 是移除钮浮现动画的目标值：行 hover 时显现（1），隐藏时退场（0）。
func revealTarget(shown bool) float32 {
	if shown {
		return 1
	}
	return 0
}

// entryInfo 待压条目的轻量档案：尺寸与是否目录。目录尺寸后台异步求和
// （用户定案：锦上添花——不卡 UI 不卡进度，算完刷新；此前的「不递归求和」
// 由本机制取代）。
type entryInfo struct {
	size   int64
	dir    bool
	sizing bool // 目录体积后台计算中（尾槽显示「计算中」）
	sized  bool // 目录体积已算完（防重复派发的完成标记）
}

// entryInfoOf 每个路径只 Stat 一次并缓存——行构建每帧都跑，逐帧 Stat 会
// 放大成 syscall 风暴（435 项 × 60fps 即每秒 2.6 万次）。
// 缓存与后台回填（walkDirSize）共用 sizeMu：此前这里裸读、回填在锁内写，
// 构成数据竞争（go test -race 实锤，2026-11-23 审计）。
func (a *app) entryInfoOf(path string) entryInfo {
	a.sizeMu.Lock()
	defer a.sizeMu.Unlock()
	return a.entryInfoOfLocked(path)
}

// ensureDirSize 目录体积异步求和的调度入口（行构建逐帧调，只做廉价的
// 内存判断）：未在飞才派发一个后台 goroutine。UI 帧零阻塞、压缩进度
// 零感知——独立 goroutine，与帧内状态只在两处微锁相遇。
func (a *app) ensureDirSize(path string) {
	a.sizeMu.Lock()
	info := a.entryInfoOfLocked(path)
	if a.sizing[path] || info.sized {
		// 在飞中 / 已算完（完成标记是防闪的关键：算完删在飞表后若不标
		// done，下一帧会再派发→「计算中」→算完→再派发，无限闪烁+空转）
		a.sizeMu.Unlock()
		return
	}
	if a.sizing == nil {
		a.sizing = map[string]bool{}
	}
	a.sizing[path] = true
	info.sizing = true
	a.infoCache[path] = info
	a.sizeMu.Unlock()
	if a.win != nil {
		a.win.Invalidate() // 下一帧尾槽从「文件夹」换「计算中」
	}
	go a.walkDirSize(path)
}

// entryInfoOfLocked 锁内版缓存读+补位（entryInfoOf 的锁内孪生，避免
// ensureDirSize 双重加锁）。调用方须已持 sizeMu。
func (a *app) entryInfoOfLocked(path string) entryInfo {
	if info, ok := a.infoCache[path]; ok {
		return info
	}
	info := entryInfo{}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		info.dir = true
	} else if err == nil {
		info.size = st.Size()
	}
	if a.infoCache == nil {
		a.infoCache = map[string]entryInfo{}
	}
	a.infoCache[path] = info
	return info
}

// walkDirSize 后台遍历目录树累加常规文件字节数：符号链接不跟随
// （WalkDir 语义），单点错误静默跳过。完成回填缓存并主线程刷新。
func (a *app) walkDirSize(path string) {
	var total int64
	filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if st, statErr := entry.Info(); statErr == nil {
				total += st.Size()
			}
		}
		return nil
	})
	a.sizeMu.Lock()
	if a.infoCache == nil {
		a.infoCache = map[string]entryInfo{}
	}
	info := a.infoCache[path]
	info.sizing = false
	info.sized = true // 完成标记——断了「删在飞表→下帧再派发」的闪烁环
	info.size = total
	a.infoCache[path] = info
	delete(a.sizing, path)
	a.sizeMu.Unlock()
	if a.win != nil {
		win := a.win
		mygo.RunOnMain(func() { win.Invalidate() })
	}
}

// totalSize 汇总清单体积（目录不计入——见 entryInfo）。
func (a *app) totalSize() int64 {
	var total int64
	for _, path := range a.inputs {
		total += a.entryInfoOf(path).size
	}
	return total
}

// defaultName 默认包名=首输入去扩展名（目录即目录名）。
func defaultName(inputs []string) string {
	if len(inputs) == 0 {
		return ""
	}
	first := inputs[0]
	return strings.TrimSuffix(filepath.Base(first), filepath.Ext(first))
}

// syncName 清单变化后让包名/保存位置跟随默认——直到用户亲手改过
// （nameDirty/outDirDirty）。
func (a *app) syncName() {
	if !a.nameDirty {
		a.name = defaultName(a.inputs)
	}
	if !a.outDirDirty {
		a.outDir = ""
	}
}

// pathCell 清单行 / 卡片里的路径文字：完整显示，放不下就换行——不截断、不分色
// （2026-11-23 用户定案「所有文件名的显示都是完整的」；「目录段省略 + 文件名另色」是过度设计，已撤）。
func pathCell(c *ui.Context, path string) {
	ui.Text(c, path).FontSize(13).Grow(1).MinWidth(0)
}

// inputListRow 清单卡的一行（压缩台 / PDF 合并共用）：文件或文件夹图标、路径、等宽体积
// （目录先「文件夹」，异步算完前显「计算中」）、行 hover 才浮现的移除徽章（体积淡出 +
// 徽章滑入 + 按下下沉 + 悬停放大）。拖拽排序由外层 ui.List 提供；draggable 的清单行是
// 拖拽把手（抓手光标），不排序的清单行本身不可点（不给手型——指针形状不兑现即误导）。
// removal 由调用方在建帧后处理（建帧期间不改清单——顺序即下标）。
func (a *app) inputListRow(c *ui.Context, t *ui.Theme, path string, idx int, last bool, draggable bool, removal *int) {
	row := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(9, 12)
	if draggable {
		row.Cursor(ui.CursorGrab) // 外层 ui.List 配了 Reorder：行是拖拽把手
	}
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if !last { // 行间 1px 分隔线（末行不加）
		row.Border(1, t.Border).BorderWidth(0, 0, 1, 0)
	}
	row.Children(func() {
		info := a.entryInfoOf(path)
		rowIcon := iconFile
		if info.dir {
			rowIcon = iconFolder
			a.ensureDirSize(path)
		}
		ui.Image(c, rowIcon).Size(14, 14)
		pathCell(c, path)
		shown := row.Hovered()
		slot := ui.Row(c).Shrink(0).AlignItems(ui.Center).Height(24)
		slot.Children(func() {
			sizeLabel := humanSize(info.size)
			if info.dir {
				switch {
				case info.sizing:
					sizeLabel = "计算中"
				case info.size > 0:
					sizeLabel = humanSize(info.size)
				default:
					sizeLabel = "文件夹"
				}
			}
			numTarget := float32(1)
			if shown {
				numTarget = 0
			}
			sizeText := ui.Text(c, sizeLabel).Font(fontMonoFamily + ", monospace").FontSize(SIZE_LABEL_FONT_SIZE).
				TextColor(t.TextMuted)
			sizeText.Opacity(slot.Animate("num", numTarget, 110*time.Millisecond))

			del := ui.ButtonBase(c).Absolute().Right(0).Size(24, 24).Radius(6).Center().
				Cursor(ui.CursorPointer).
				Background(ui.RGBA(0, 0, 0, 0))
			hover := del.Hovered()
			down := del.Pressed()
			del.Opacity(del.Animate("vis", revealTarget(shown), 110*time.Millisecond))
			slideTarget := float32(5)
			if shown {
				slideTarget = 0
			}
			sinkTarget := float32(0)
			sinkDur := 120 * time.Millisecond
			if down {
				sinkTarget = 1
				sinkDur = 90 * time.Millisecond
			}
			del.Translate(del.Animate("slide", slideTarget, 110*time.Millisecond),
				del.Animate("sink", sinkTarget, sinkDur))
			del.Children(func() {
				ix := ui.Image(c, iconRemoveBadge).Size(14, 14)
				scaleTarget := float32(1)
				if hover {
					scaleTarget = 1.3
				}
				scale := del.Animate("scale", scaleTarget, 140*time.Millisecond)
				ix.Scale(scale, scale)
			})
			if del.Clicked() {
				*removal = idx
			}
		})
	})
}

// reorderInputs 把行 rows 移到 to 前（ListState.Reorder 契约；to=len 收尾）。
func reorderInputs(paths []string, rows []int, to int) []string {
	moved := make([]string, 0, len(rows))
	rest := make([]string, 0, len(paths))
	at := -1
	for i, v := range paths {
		if i == to {
			at = len(rest)
		}
		if slices.Contains(rows, i) {
			moved = append(moved, v)
		} else {
			rest = append(rest, v)
		}
	}
	if at < 0 {
		at = len(rest)
	}
	out := make([]string, 0, len(paths))
	out = append(out, rest[:at]...)
	out = append(out, moved...)
	out = append(out, rest[at:]...)
	return out
}

// suffixOptions 后缀下拉的可选格式（=压缩格式，后缀即格式——用户定案）。
// .7z（2026-11-22 接入官方 C++ 直调引擎）：加密连文件名一起加密。
var suffixOptions = []string{".zip", ".7z", ".tar.gz", ".tar"}

// volumePreset 分卷预设：value 即 CLI --volume-size 直传串，label 为展示文案。
type volumePreset struct {
	value string
	label string
}

// volumePresets 分卷预设=真实场景档（用户定案：按各平台实测上限取整，
// 光盘档以「刻录」点名用途）。value 即 CLI --volume-size 直传串：
// Gmail 个人账号附件 25MB（Google 官方）；QQ/163 邮箱普通附件 50MB；
// 微信聊天文件 1GB（官方 newsroom：电脑端最大 1GB）；VCD 700MB / DVD
// 4.7GB 为介质标称容量（CLI 模板 SI 十进制精确字节）。
var volumePresets = []volumePreset{
	{"gmail", "Gmail · 25MB"},
	{"50MB", "邮箱 · 50MB"},
	{"1GB", "微信 · 1GB"},
	{"cd", "VCD 刻录 · 700MB"},
	{"dvd", "DVD 刻录 · 4.7GB"},
}

// presetValues 预置值序列（下拉中段）。
func presetValues() []string {
	values := make([]string, 0, len(volumePresets))
	for _, preset := range volumePresets {
		values = append(values, preset.value)
	}
	return values
}

// volumePresetLabel 命中预设返回友好文案；未命中=自定义。
func volumePresetLabel(value string) (string, bool) {
	for _, preset := range volumePresets {
		if preset.value == value {
			return preset.label, true
		}
	}
	return "", false
}

func (a *app) addInputs(paths []string) {
	for _, path := range paths {
		// Finder 拖目录的 file-url 带尾斜杠（file:///tmp/x/）——不去掉则 baseName 切出空名
		path = strings.TrimSuffix(path, "/")
		if path == "" || path == "/" {
			continue
		}
		duplicate := false
		for _, existing := range a.inputs {
			if existing == path {
				duplicate = true
				break
			}
		}
		if !duplicate {
			a.inputs = append(a.inputs, path)
		}
	}
	a.syncName()
}

// outputPlan 展示用输出计划：目录=首输入所在目录（多个文件以第一个为准——
// 引擎 StartCompress 既有行为）；名字=首输入去扩展名+格式扩展名（同名自动加序号）。
func outputPlan(inputs []string, format string) (dir, name string) {
	first := inputs[0]
	dir = filepath.Dir(first)
	base := strings.TrimSuffix(filepath.Base(first), filepath.Ext(first))
	return dir, base + "." + format
}

func (a *app) formatSpec() string {
	if a.formatStr == "" {
		return "zip" // 兜底（字段零值期）
	}
	return strings.TrimPrefix(a.formatStr, ".")
}

// ───────────────────────── 刻录光盘（m06038）─────────────────────────

// burnDeviceLabel 设备下拉显示名：1 · HL-DT-ST DVDRAM…（+介质态短语）
func burnDeviceLabel(device BurnDevice) string {
	label := fmt.Sprintf("%d · %s %s", device.Index+1, device.Vendor, device.Product)
	switch device.Status {
	case "blank":
		label += "（空白）"
	case "appendable":
		label += "（可追加）"
	case "full":
		label += "（已满）"
	case "no-media":
		label += "（无介质）"
	}
	return label
}

// addBurnInputs 追加刻录内容（去重保序；镜像文件禁止与其他内容混排——引擎侧还会拦）
func (a *app) addBurnInputs(paths []string) {
	known := map[string]bool{}
	for _, existing := range a.burnInputs {
		known[existing] = true
	}
	added := false
	for _, raw := range paths {
		path := strings.TrimSuffix(raw, "/")
		if path == "" || known[path] {
			continue
		}
		known[path] = true
		a.burnInputs = append(a.burnInputs, path)
		added = true
	}
	if added {
		a.win.Invalidate()
	}
}

// removeBurnInput 移除第 idx 项
func (a *app) removeBurnInput(idx int) {
	a.burnInputs = append(a.burnInputs[:idx], a.burnInputs[idx+1:]...)
	a.win.Invalidate()
}

// browseBurnFiles 多选文件（含 .iso——单独一个 .iso 时引擎直烧镜像）
func (a *app) browseBurnFiles() {
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   window,
			Title:    "选择要刻录的文件",
			Multiple: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		mygo.RunOnMain(func() {
			a.addBurnInputs(paths)
			window.Invalidate()
		})
	}()
}

// browseBurnDir 浏览选择文件夹（目录直刻——引擎自动聚台产 ISO）
func (a *app) browseBurnDir() {
	a.toolBrowse("选择要刻录的文件夹", true, func(path string) {
		a.addBurnInputs([]string{path})
	})
}

// burnRefreshDevices 重新枚举刻录机（换盘/插外置光驱后手动刷）
func (a *app) burnRefreshDevices() {
	a.burnQueried = false
	a.win.Invalidate()
}

// burnSelectedDevice 当前选中的刻录机（下拉选中项按显示名对应；没选中返回零值与 false）
func (a *app) burnSelectedDevice() (BurnDevice, bool) {
	for _, device := range a.burnDevices {
		if burnDeviceLabel(device) == a.burnDevStr {
			return device, true
		}
	}
	return BurnDevice{}, false
}

// burnVolumeNamePlan 光盘名称行的显示与传参规则——按选中刻录机的介质状态与刻录内容形态定
func (a *app) burnVolumeNamePlan() burnVolumeNamePlan {
	selected, _ := a.burnSelectedDevice()
	singleDir := len(a.burnInputs) == 1 && burnInputIsDir(a.burnInputs[0])
	return planBurnVolumeName(selected.Status, a.burnVolumeName, singleDir, a.burnBlank)
}

// clearBurn 清空刻录内容
func (a *app) clearBurn() {
	a.burnInputs = nil
	a.win.Invalidate()
}

// burnSpeedLabel 刻录速度档文案（0=自动；1-6=倍速）
func burnSpeedIdxLabel() []string { return []string{"自动", "4x", "8x", "16x", "24x", "32x", "48x"} }

func burnSpeedLabel(idx int) string { return burnSpeedIdxLabel()[idx] }

// pageBurn 刻录光盘页：内容（镜像/文件夹）→ 设备 → 模式 → 速度 → 擦除 → 刻录
func (a *app) pageBurn(c *ui.Context) {
	t := c.Theme()

	// 设备懒加载：进入页面一次性 + 刷新钮重置（service 为 nil 的建帧冒烟直接跳过）
	if a.service != nil && !a.burnQueried {
		a.burnQueried = true
		a.service.BurnDevices(func(devices []BurnDevice, errMsg string) {
			mygo.RunOnMain(func() {
				a.burnDevices = devices
				a.burnDevErr = errMsg
				a.burnDevStr = ""
				if len(devices) > 0 {
					a.burnDevStr = burnDeviceLabel(devices[0])
				}
				a.win.Invalidate()
			})
		})
	}

	column := pageColumn(c)
	column.Children(func() {
		// 引导行（压缩台同款抄齐 m07591）：状态句+句尾「浏览选择…」描边钮
		//（hover Accent 描边+Accent 字）；刻录特有=追加「选择文件夹…」幽灵钮
		//（Finder 多选不含文件夹）与右侧「清空」；卡前不再垫 Height(8)
		// 引导行放不下才换行（2026-11-23 用户定案）：Wrap 行里按 Basis 假定句子宽度，于是句子
		// 先收缩折行，窄到连下限都放不下时按钮才换到下一行——不换行的话最小窗口里这句被挤成
		// 26px 宽 9 行的竖条。句子吃掉按钮之外的余量，尾部的「清空」自然靠右（不再垫占位）。
		ui.Row(c).Padding(14, 0, 4, 0).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, "已准备的刻录内容 "+itoa(len(a.burnInputs))+" 项，可拖拽加入或").
				FontSize(13).TextColor(t.TextMuted).Basis(LEAD_SENTENCE_MIN_WIDTH).Grow(1)
			leadBrowseButton(c, t, "浏览选择…", func() { a.browseBurnFiles() })
			dirPick := ui.ButtonBase(c).Height(24).Padding(0, 10).Radius(7).Gap(4).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Background(ui.RGBA(0, 0, 0, 0)).Border(0, t.Border)
			pressFeedback(dirPick, t)
			dirPick.Children(func() {
				ui.Image(c, iconFolder).Size(13, 13)
				ui.Text(c, "选择文件夹…").FontSize(13).TextColor(t.TextMuted)
			})
			if dirPick.Clicked() {
				a.browseBurnDir()
			}
			if len(a.burnInputs) > 0 {
				if ui.Button(c, "清空").FontSize(13).Height(22).Padding(0, 8).
					TextColor(t.TextMuted).Cursor(ui.CursorPointer).Clicked() {
					a.clearBurn()
				}
			}
		})

		// 清单卡（同压缩台清单抄齐 m07637）：ui.List 拖拽+尺寸槽+删除徽章全动画链
		if len(a.burnInputs) > 0 {
			removal := -1
			card := ui.Column(c).Margin(12, 0, 0, 0).
				Background(t.Surface).Border(1, t.Border).Radius(10).Clip()
			card.Children(func() {
				a.burnRows.Key = func(i int) any { return a.burnInputs[i] }
				a.burnRows.Reorder = func(rows []int, to int) {
					a.burnInputs = reorderInputs(a.burnInputs, rows, to)
				}
				a.burnRows.MoveAnimation = &ui.Smooth
				ui.List(c, &a.burnRows, len(a.burnInputs), func(idx int) {
					a.inputListRow(c, t, a.burnInputs[idx], idx, idx == len(a.burnInputs)-1, true, &removal)
				}).Grow(1).Shrink(0)
			})
			if removal >= 0 {
				a.burnInputs = append(a.burnInputs[:removal], a.burnInputs[removal+1:]...)
			}
		}

		// 刻录机（枚举 + 刷新）
		deviceHint := "点击刷新重新枚举"
		if a.burnDevErr != "" {
			deviceHint = a.burnDevErr
		} else if len(a.burnDevices) == 0 {
			deviceHint = "未检测到刻录机"
		} else {
			deviceHint = fmt.Sprintf("共 %d 台", len(a.burnDevices))
		}
		openSettingRow(c, t, "刻录机", deviceHint, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).Children(func() {
				if len(a.burnDevices) > 0 {
					deviceSel := ui.SelectBase(c, &a.burnDevStr)
					deviceTrig := deviceSel.Trigger.
						Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
						AlignItems(ui.Center).Shrink(0).
						Background(t.Surface).Border(1, t.Border).
						Cursor(ui.CursorPointer)
					pressFeedback(deviceTrig, t)
					deviceTrig.Children(func() {
						ui.Text(c, a.burnDevStr).FontSize(13).Grow(1)
						ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
					})
					deviceSel.Popup(func(panel ui.Element) {
						panel.MinWidth(240).Padding(4).Radius(10).
							Background(t.Surface).Border(1, t.Border)
						for _, device := range a.burnDevices {
							label := burnDeviceLabel(device)
							item := deviceSel.Item(label).Height(30).Padding(0, 10).Radius(6).
								Cursor(ui.CursorPointer)
							switch {
							case item.Highlighted():
								item.Background(t.Accent).TextColor(t.AccentText)
							case label == a.burnDevStr:
								item.Background(t.SurfaceHover)
							}
							item.Children(func() {
								ui.Text(c, label).FontSize(13)
							})
						}
					})
				}
				refresh := ui.ButtonBase(c).Height(32).Padding(0, 10).
					Radius(8).Cursor(ui.CursorPointer).
					Background(t.Surface).Border(1, t.Border)
				pressFeedback(refresh, t)
				refresh.Children(func() {
					ui.Text(c, "刷新").FontSize(13).TextColor(t.TextMuted)
				})
				if refresh.Clicked() {
					a.burnRefreshDevices()
				}
				ui.Box(c).Grow(1)
			})
		})

		// 光盘名称（2026-11-24 用户定案）：只有空白盘能起名，已有内容的盘沿用盘上名称。
		// 非空白态不摆灰掉的输入框，直接一句话说清为什么不能改。输入框与分卷大小、码率等
		// 既有文本框同类，不进反馈矩阵（矩阵只收悬停 / 按下态的按钮族）
		namePlan := a.burnVolumeNamePlan()
		openSettingRow(c, t, "光盘名称", namePlan.hint, func() {
			ui.Column(c).Grow(1).Children(func() {
				if namePlan.editable {
					ui.TextInputBase(c, &a.burnVolumeName).
						Placeholder(namePlan.placeholder).Grow(1).MinWidth(120).FontSize(13).
						Padding(5, 10).
						Background(t.Surface).Border(1, t.Border).Radius(8).MinHeight(32)
				} else {
					ui.Text(c, namePlan.lockedText).FontSize(13).TextColor(t.TextMuted).Wrap()
				}
				if namePlan.note != "" {
					ui.Text(c, namePlan.note).FontSize(12).TextColor(t.TextMuted).
						Wrap().Margin(4, 0, 0, 0)
				}
			})
		})

		// 写入模式（可追加=引擎 multi 不封盘——Linux DAO 封死空盘的痛点；一次性=mastered 封盘）
		// 平台差异化提示（用户定案 m06432/m06508）：可追加各平台能做的事不一样，写明白
		modeHint := "封盘定稿，这张盘之后不能再写；老 DVD 机/车机兼容最好"
		if a.burnModeStr == "可追加" {
			switch runtime.GOOS {
			case "windows":
				modeHint = "不封盘，之后还能继续追加；Windows 后续将支持真 UDF 优盘式（可删改）"
			case "darwin":
				modeHint = "不封盘，之后还能继续追加；已写入的内容不能删"
			default:
				modeHint = "不封盘，之后还能继续追加；已写入的内容不能删"
			}
		}
		openSettingRow(c, t, "写入模式", "封盘=不能再写", func() {
			// 下拉+平台提示行（分卷提示同款范式：提示独立成行，随选中与平台变）
			ui.Column(c).Grow(1).Children(func() {
				ui.Row(c).Grow(1).Children(func() {
					modeSel := ui.SelectBase(c, &a.burnModeStr)
					modeTrig := modeSel.Trigger.
						Height(32).MinWidth(130).Radius(8).Padding(0, 10).Gap(6).
						AlignItems(ui.Center).Shrink(0).
						Background(t.Surface).Border(1, t.Border).
						Cursor(ui.CursorPointer)
					pressFeedback(modeTrig, t)
					modeTrig.Children(func() {
						ui.Text(c, a.burnModeStr).FontSize(13).Grow(1)
						ui.Icon(c, iconChevronDown).Size(9, 9).TextColor(t.TextMuted)
					})
					modeSel.Popup(func(panel ui.Element) {
						panel.MinWidth(160).Padding(4).Radius(10).
							Background(t.Surface).Border(1, t.Border)
						for _, label := range []string{"可追加", "一次性烧录"} {
							item := modeSel.Item(label).Height(30).Padding(0, 10).Radius(6).
								Cursor(ui.CursorPointer)
							switch {
							case item.Highlighted():
								item.Background(t.Accent).TextColor(t.AccentText)
							case label == a.burnModeStr:
								item.Background(t.SurfaceHover)
							}
							item.Children(func() {
								ui.Text(c, label).FontSize(13)
							})
						}
					})
				})
				ui.Text(c, modeHint).FontSize(12).TextColor(t.TextMuted).Margin(4, 0, 0, 0).Wrap()
			})
		})

		// 刻录速度（用户定案 m06477：滑杆替代自由输入——真实用途是降速换兼容，
		// 档位 自动/4/8/16/24/32/48；0=自动=光驱与碟片协商最优）
		openSettingRow(c, t, "刻录速度", "自动=光驱协商", func() {
			ui.Row(c).Grow(1).AlignItems(ui.Center).Gap(10).Children(func() {
				sp := ui.SliderBase(c, &a.burnSpeedIdx, 0, 6).Step(1).
					Grow(1).Height(20).FocusRing(false)
				held := sp.Animate("spdheld",
					map[bool]float32{true: 1, false: 0}[sp.Pressed()],
					140*time.Millisecond)
				// 观感同压缩级别滑杆（macOS 26 原生 NSSlider 实测比例）
				sp.Draw(func(p *ui.Painter, r ui.Rect) {
					const knobW = float32(20)
					const knobH = float32(16)
					const knobR = float32(7)
					const trH = float32(6)
					track := ui.Rect{X: r.X + knobW/2, Y: r.Y + r.H/2 - trH/2, W: r.W - knobW, H: trH}
					frac := float32(a.burnSpeedIdx) / 6
					p.Fill(track, t.Border.Mix(t.Text, 0.12), trH/2)
					if frac > 0 {
						p.Fill(ui.Rect{X: track.X, Y: track.Y,
							W: max(trH, frac*track.W), H: trH}, t.Accent, trH/2)
					}
					kx := track.X + frac*track.W
					g := 1 + 0.12*held
					kw, kh := knobW*g, knobH*g
					k := ui.Rect{X: kx - kw/2, Y: r.Y + r.H/2 - kh/2, W: kw, H: kh}
					p.Shadow(k, knobR, 0, 2, 5, 0, ui.RGBA(15, 23, 42, 0.24*held))
					p.Fill(k, ui.RGB(255, 255, 255), knobR)
					p.Stroke(k, t.Border.Mix(t.Text, 0.25), knobR, 1)
				})
				ui.Text(c, burnSpeedLabel(int(a.burnSpeedIdx+0.5))).Font(fontMonoFamily + ", monospace").FontSize(13).
					TextColor(t.TextMuted).Width(36)
			})
		})

		// 擦除（仅可重写介质出现此行，用户定案 m06524：一次性碟物理不可擦，
		// 摆出来只会让人困惑「有的光盘不是删不掉吗」；DVD+RW 天生覆盖写免擦）
		selectedMedia := ""
		if selected, chosen := a.burnSelectedDevice(); chosen {
			selectedMedia = selected.Media
		}
		rewritable := strings.Contains(selectedMedia, "RW") || strings.Contains(selectedMedia, "RE")
		if rewritable && selectedMedia != "DVD+RW" {
			openSettingRow(c, t, "先擦空再烧", "清空这张"+selectedMedia+"后写入", func() {
				ui.Row(c).Grow(1).AlignItems(ui.Center).Children(func() {
					ui.Switch(c, &a.burnBlank).Cursor(ui.CursorPointer)
				})
			})
		}

		// CTA：清除 + 刻录（进处理台看进度）
		burnReady := len(a.burnInputs) > 0 && len(a.burnDevices) > 0
		burnBlockHint := "未检测到刻录机"
		if len(a.burnInputs) == 0 {
			burnBlockHint = "先放入刻录内容"
		}
		ctaRow(c).Children(func() {
			cancelButton(c, t, "清除", a.clearBurn)
			// 没就绪时主按钮置灰，并直接写明被什么挡住（刻录页特有：原因随设备 / 内容变化）
			start := primaryCTA(c, t, map[bool]string{true: "刻录", false: burnBlockHint}[burnReady], burnReady)
			if burnReady && start.Clicked() {
				deviceIndex := 0
				if selected, chosen := a.burnSelectedDevice(); chosen {
					deviceIndex = selected.Index
				}
				// 光盘名称只在空白盘（或计划先擦空）时传——规则见 planBurnVolumeName
				nameArg := a.burnVolumeNamePlan().volumeArg
				engineMode := "multi"
				if a.burnModeStr == "一次性烧录" {
					engineMode = "mastered"
				}
				speedArg := ""
				if spdIdx := int(a.burnSpeedIdx + 0.5); spdIdx > 0 {
					speedArg = strconv.Itoa([]int{0, 4, 8, 16, 24, 32, 48}[spdIdx])
				}
				a.launchTask(a.service.StartBurn(a.burnInputs, deviceIndex, speedArg, engineMode, a.burnBlank, nameArg))
			}
		})
	})
}
