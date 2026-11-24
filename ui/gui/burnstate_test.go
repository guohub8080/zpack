package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestBurnBlockReason 盘不可写时主按钮的拦截原因（表驱动）：只有明确认识的状态才拦；
// 可擦写盘的异常状态勾了「先擦空再烧」就放行；macOS / Linux 上认不出的状态不拦
func TestBurnBlockReason(t *testing.T) {
	cases := []struct {
		name   string
		status string
		erase  bool
		want   string // 期望的拦截原因；空串=不拦
	}{
		{"空白盘", "blank", false, ""},
		{"可追加的盘", "appendable", false, ""},
		{"已封盘", "full", false, "光盘已封盘"},
		{"没放盘", "no-media", false, "未放入光盘"},
		{"状态异常", "damaged", false, "光盘状态异常"},
		{"须先擦除", "erase-required", false, "光盘需先擦除"},
		{"写保护", "write-protected", false, "光盘被写保护"},
		{"不支持该介质", "unsupported", false, "不支持这张光盘"},
		{"光驱认不出这张盘", "unrecognized", false, "光驱认不出这张光盘"},
		{"认不出的盘不因擦除放行", "unrecognized", true, "光驱认不出这张光盘"},
		{"状态异常的可擦写盘勾了先擦空：放行", "damaged", true, ""},
		{"须先擦除的盘勾了先擦空：放行", "erase-required", true, ""},
		{"写保护不因擦除放行", "write-protected", true, "光盘被写保护"},
		{"macOS 有盘时状态为空：不拦", "", false, ""},
		{"Linux 认不出的状态短语原样透传：不拦", "is offline", false, ""},
		{"Windows 的 unknown：不拦，交给引擎", "unknown", false, ""},
	}
	for _, testCase := range cases {
		if got := burnBlockReason(testCase.status, testCase.erase); got != testCase.want {
			t.Errorf("%s: 拦截原因 %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}

// TestBurnMediaSuffix 设备下拉的介质状态括注（表驱动）：认不出的状态不加括注
func TestBurnMediaSuffix(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"blank", "（空白）"}, {"appendable", "（可追加）"}, {"full", "（已满）"}, {"no-media", "（无介质）"},
		{"damaged", "（状态异常）"}, {"erase-required", "（需先擦除）"}, {"write-protected", "（写保护）"}, {"unsupported", "（不支持）"},
		{"unrecognized", "（认不出）"},
		{"", ""}, {"unknown", ""}, {"DVD-R", ""},
	}
	for _, testCase := range cases {
		label := burnDeviceLabel(BurnDevice{Index: 0, Vendor: "HL-DT-ST", Product: "DVD-RAM GHC0N", Status: testCase.status})
		if !strings.HasSuffix(label, testCase.want) || !strings.HasPrefix(label, "1 · HL-DT-ST DVD-RAM GHC0N") {
			t.Errorf("状态 %q: 设备名 %q 应以 %q 结尾", testCase.status, label, testCase.want)
		}
	}
}

// TestBurnBlockedByMedia 盘不可写时刻录页主按钮置灰（去手型）并写明原因；可写的盘不拦。
// 主按钮有手型 = 可点，置灰态去掉手型（同 TestInvalidInputDisablesPrimary）
func TestBurnBlockedByMedia(t *testing.T) {
	registerMiSans()
	primaryClickable := func(status string) (bool, string) {
		application := newApp()
		application.burnQueried = true // 不触发设备枚举（无头环境下枚举回调没有主循环可投递）
		application.burnInputs = []string{"/tmp/备份.iso"}
		application.burnDevices = []BurnDevice{{Index: 0, Path: "F:\\", Vendor: "HL-DT-ST", Product: "DVD-RAM GHC0N", Status: status}}
		application.burnDevStr = burnDeviceLabel(application.burnDevices[0])
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(brandTheme(c))
			application.pageBurn(c)
		}, FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		label := "刻录"
		if reason := burnBlockReason(status, false); reason != "" {
			label = reason
		}
		rect, found := tester.Find(label)
		if !found {
			t.Fatalf("状态 %q：找不到主按钮 %q", status, label)
		}
		tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
		tester.Frame()
		return tester.Cursor() == ui.CursorPointer, label
	}
	for _, testCase := range []struct {
		status  string
		blocked bool
	}{
		{"blank", false}, {"appendable", false},
		{"full", true}, {"no-media", true}, {"damaged", true}, {"erase-required", true}, {"write-protected", true}, {"unsupported", true},
		{"unrecognized", true},
		{"", false}, {"unknown", false},
	} {
		clickable, label := primaryClickable(testCase.status)
		if clickable == testCase.blocked {
			t.Errorf("状态 %q：主按钮 %q 置灰与否不符（有手型=%v，应被拦=%v）", testCase.status, label, clickable, testCase.blocked)
		}
	}
}

// TestBurnImageCaution 镜像 + 已有内容的盘才提醒（表驱动）：空白盘、文件夹、勾了「先擦空再烧」、
// 判不出状态的盘都不提醒
func TestBurnImageCaution(t *testing.T) {
	cases := []struct {
		name   string
		status string
		inputs []string
		erase  bool
		want   string // 期望的提醒；空串=不提醒
	}{
		{"镜像 + 可追加的盘：提醒", "appendable", []string{"a.ISO"}, false, BURN_IMAGE_ON_USED_DISC_CAUTION},
		{"镜像 + 空白盘", "blank", []string{"a.iso"}, false, ""},
		{"镜像 + 勾了先擦空再烧：擦完是空白盘", "appendable", []string{"a.iso"}, true, ""},
		{"文件夹 + 可追加的盘（会导入上一区段）", "appendable", []string{"备份"}, false, ""},
		{"macOS 有盘时状态为空：判不出有没有数据，不提醒", "", []string{"a.iso"}, false, ""},
		{"已封盘的盘：反正会被拦", "full", []string{"a.iso"}, false, ""},
	}
	for _, testCase := range cases {
		if got := burnImageCaution(testCase.status, testCase.inputs, testCase.erase); got != testCase.want {
			t.Errorf("%s: 提醒 %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}

// TestBurnImageCautionRow 刻录页的「写入提醒」行：镜像 + 已有内容的盘时出现，换成空白盘就消失；
// 副标在标签列里恒一行。不拦截——主按钮照常可点
func TestBurnImageCautionRow(t *testing.T) {
	registerMiSans()
	application := newApp()
	application.burnQueried = true // 不触发设备枚举（无头环境下枚举回调没有主循环可投递）
	application.burnInputs = []string{"/tmp/备份.iso"}
	application.burnDevices = []BurnDevice{{Index: 0, Path: "E:\\", Vendor: "lenovo", Product: "UltraslimDVD", Status: "appendable"}}
	application.burnDevStr = burnDeviceLabel(application.burnDevices[0])
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(brandTheme(c))
		application.pageBurn(c)
	}, FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
	tester.Frame()
	if !tester.HasText("这张盘已有内容。镜像是原样写进新区段的") {
		t.Fatal("镜像 + 已有内容的盘应有「写入提醒」")
	}
	if hint, found := tester.Find("镜像不合并"); !found || hint.H >= HINT_ONE_LINE_TOP {
		t.Errorf("副标应存在且恒一行: found=%v 高=%.0f", found, hint.H)
	}
	if start, found := tester.Find("刻录"); !found {
		t.Fatal("提醒不拦截：主按钮应仍是「刻录」")
	} else {
		tester.Move(start.X+start.W/2, start.Y+start.H/2)
		tester.Frame()
		if tester.Cursor() != ui.CursorPointer {
			t.Error("提醒不拦截：主按钮应可点")
		}
	}
	application.burnDevices[0].Status = "blank"
	application.burnDevStr = burnDeviceLabel(application.burnDevices[0])
	tester.Frame()
	if tester.HasText("这张盘已有内容。镜像是原样写进新区段的") {
		t.Error("空白盘不应有「写入提醒」")
	}
}

// TestBurnModeHint 写入模式下方的提示（表驱动）：一次性烧录三平台同文；可追加按平台如实区分——
// macOS 的 hdiutil 不并入原有文件，不能跟着说「还能继续追加」
func TestBurnModeHint(t *testing.T) {
	mastered := "封盘定稿，这张盘之后不能再写；老 DVD 机/车机兼容最好"
	cases := []struct {
		platform string
		mode     string
		want     string
	}{
		{"windows", "可追加", "不封盘，之后还能继续追加；Windows 后续将支持真 UDF 优盘式（可删改）"},
		{"linux", "可追加", "不封盘，之后还能继续追加；已写入的内容不能删"},
		{"darwin", "可追加", "不封盘，但 macOS 不会把新内容并入这张盘原有的文件：已有内容的盘，写入后通常只看得到这次刻入的内容"},
		{"windows", "一次性烧录", mastered},
		{"linux", "一次性烧录", mastered},
		{"darwin", "一次性烧录", mastered},
	}
	for _, testCase := range cases {
		if got := burnModeHint(testCase.mode, testCase.platform); got != testCase.want {
			t.Errorf("%s / %s: %q ≠ %q", testCase.platform, testCase.mode, got, testCase.want)
		}
	}
}
