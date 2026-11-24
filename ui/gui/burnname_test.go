package main

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestPlanBurnVolumeName 光盘名称行规则（表驱动）：只有空白盘（或计划先擦空）能起名，
// 已有内容的盘沿用盘上名称，留空不传参
func TestPlanBurnVolumeName(t *testing.T) {
	cases := []struct {
		name          string
		status        string
		typed         string
		singleDir     bool
		erase         bool
		wantEditable  bool
		wantHint      string
		wantPlacehold string
		wantArg       string
	}{
		{"空白盘可命名（首尾空白去掉）", "blank", " 备份 ", true, false, true, "仅空白盘可设", "留空用文件夹名", "备份"},
		{"空白盘留空=不传参", "blank", "   ", false, false, true, "仅空白盘可设", "留空用默认名", ""},
		{"已有内容的盘沿用原名称", "appendable", "备份", true, false, false, "沿用盘上名称", "", ""},
		{"已封盘", "full", "备份", true, false, false, "已封盘", "", ""},
		{"没放盘", "no-media", "备份", true, false, false, "需空白光盘", "", ""},
		{"没检测到刻录机（状态为空）", "", "备份", true, false, false, "需空白光盘", "", ""},
		{"状态异常的盘（Windows 引擎报 damaged）", "damaged", "备份", true, false, false, "此盘无法写入", "", ""},
		{"须先擦除的盘", "erase-required", "备份", true, false, false, "需先擦除", "", ""},
		{"写保护的盘", "write-protected", "备份", true, false, false, "写保护", "", ""},
		{"不支持的盘", "unsupported", "备份", true, false, false, "不支持该盘", "", ""},
		{"macOS 有盘时状态原样透传：同样不可命名", "DVD-R", "备份", true, false, false, "需空白光盘", "", ""},
		{"计划先擦空的可擦写盘可命名", "appendable", "备份", false, true, true, "仅空白盘可设", "留空用默认名", "备份"},
	}
	for _, testCase := range cases {
		plan := planBurnVolumeName(testCase.status, testCase.typed, testCase.singleDir, testCase.erase)
		if plan.editable != testCase.wantEditable || plan.hint != testCase.wantHint ||
			plan.placeholder != testCase.wantPlacehold || plan.volumeArg != testCase.wantArg {
			t.Errorf("%s: %+v ≠ 可改名=%v 副标=%q 占位=%q 传参=%q", testCase.name, plan,
				testCase.wantEditable, testCase.wantHint, testCase.wantPlacehold, testCase.wantArg)
		}
		if !plan.editable && plan.lockedText == "" {
			t.Errorf("%s: 不可改名时应有一句说明", testCase.name)
		}
		// 字符集提示只在可改名且非 Windows 平台出现
		if gotNote := plan.note != ""; gotNote != (plan.editable && runtime.GOOS != "windows") {
			t.Errorf("%s: 字符集提示出现与否不符: %q", testCase.name, plan.note)
		}
	}
}

// TestBurnArguments 刻录命令参数：光盘名称去首尾空白后非空才传 --volid，空的不传
func TestBurnArguments(t *testing.T) {
	got := burnArguments([]string{"a.iso", "b 目录"}, 1, "8", "multi", true, " 备份 ")
	want := []string{"burn", "a.iso", "b 目录", "--device", "1", "--speed", "8", "--volid", "备份", "--mode", "multi", "--blank"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("有名称: %v ≠ %v", got, want)
	}
	got = burnArguments([]string{"a.iso"}, 0, "", "mastered", false, "  ")
	want = []string{"burn", "a.iso", "--device", "0", "--mode", "mastered"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("无名称: %v ≠ %v", got, want)
	}
}

// BURN_NAME_FIELD_OFFSET_X 光盘名称输入框在标签列右侧的横向偏移（标签右缘 + 间距 12 + 内边距 10 起即是输入区）
const BURN_NAME_FIELD_OFFSET_X = 120

// TestBurnVolumeNameRow 刻录页的光盘名称行：空白盘出输入框且能输入；已有内容的盘不出输入框、
// 一句说明在场；副标在标签列里恒一行。输入框的占位文字不参与文字查找，故按标签位置定位
func TestBurnVolumeNameRow(t *testing.T) {
	registerMiSans()
	newBurnApp := func(status string) *app {
		application := newApp()
		application.burnQueried = true // 不触发设备枚举（无头环境下枚举回调没有主循环可投递）
		application.burnInputs = []string{"/tmp/备份.iso"}
		application.burnDevices = []BurnDevice{{Index: 0, Path: "E:\\", Vendor: "lenovo", Product: "UltraslimDVD", Status: status}}
		application.burnDevStr = burnDeviceLabel(application.burnDevices[0])
		return application
	}
	render := func(application *app) *ui.Tester {
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(brandTheme(c))
			application.pageBurn(c)
		}, FEEDBACK_WINDOW_WIDTH, FEEDBACK_WINDOW_HEIGHT)
		tester.Frame()
		return tester
	}
	// 点到名称行的控件位置上；返回这一点是否落进了文本输入框
	focusField := func(tester *ui.Tester) bool {
		label, found := tester.Find("光盘名称")
		if !found {
			t.Fatal("刻录页应有「光盘名称」行")
		}
		tester.ClickAtWith(0, label.X+label.W+BURN_NAME_FIELD_OFFSET_X, label.Y+label.H/2)
		active, _ := tester.TextInputActive()
		return active
	}

	blankApp := newBurnApp("blank")
	blank := render(blankApp)
	if hint, found := blank.Find("仅空白盘可设"); !found || hint.H >= HINT_ONE_LINE_TOP {
		t.Errorf("副标应存在且恒一行: found=%v 高=%.0f", found, hint.H)
	}
	if !focusField(blank) {
		t.Fatal("空白盘应有光盘名称输入框")
	}
	// 输入框应撑满控件列：点到页面右缘附近仍落在输入框里
	if label, found := blank.Find("光盘名称"); found {
		blank.ClickAtWith(0, FEEDBACK_WINDOW_WIDTH-FEEDBACK_PAGE_MARGIN-16, label.Y+label.H/2)
		if active, _ := blank.TextInputActive(); !active {
			t.Error("输入框应一直延伸到页面右缘")
		}
	}
	if !focusField(blank) {
		t.Fatal("重新点回输入框左侧应仍能聚焦")
	}
	blank.Type("备份")
	blank.Frame()
	if blankApp.burnVolumeName != "备份" {
		t.Fatalf("输入后光盘名称应为「备份」，实际 %q", blankApp.burnVolumeName)
	}
	if got := blankApp.burnVolumeNamePlan().volumeArg; got != "备份" {
		t.Fatalf("空白盘起刻应传 --volid，实际 %q", got)
	}

	appendable := newBurnApp("appendable")
	appendable.burnVolumeName = "备份" // 之前为别的盘起过名——已有内容的盘不采纳
	if got := appendable.burnVolumeNamePlan().volumeArg; got != "" {
		t.Fatalf("已有内容的盘不应传 --volid，实际 %q", got)
	}
	tester := render(appendable)
	if focusField(tester) {
		t.Error("已有内容的盘不应出输入框")
	}
	if !tester.HasText("这张盘已有内容，名称保持不变") {
		t.Error("应有「名称保持不变」的说明")
	}

	empty := render(newBurnApp("no-media"))
	if focusField(empty) {
		t.Error("没放盘时不应出输入框")
	}
	if !empty.HasText("放入空白光盘后可以设置名称") {
		t.Error("没放盘时应有提示")
	}

	// 盘在光驱里但状态异常：不再说「放入空白光盘」，而是说明这张盘写不了
	damaged := render(newBurnApp("damaged"))
	if focusField(damaged) {
		t.Error("状态异常的盘不应出输入框")
	}
	if !damaged.HasText("这张盘上一个区段没有正常关闭，无法写入") {
		t.Error("状态异常的盘应有说明")
	}
	if damaged.HasText("放入空白光盘后可以设置名称") {
		t.Error("盘已在光驱里时不应再提示放入空白光盘")
	}
}
