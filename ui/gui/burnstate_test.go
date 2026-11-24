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
		{"", false}, {"unknown", false},
	} {
		clickable, label := primaryClickable(testCase.status)
		if clickable == testCase.blocked {
			t.Errorf("状态 %q：主按钮 %q 置灰与否不符（有手型=%v，应被拦=%v）", testCase.status, label, clickable, testCase.blocked)
		}
	}
}
