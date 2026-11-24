//go:build windows

package burn

import (
	"testing"

	"github.com/guohub8080/zpack/internal/imapi2"
)

// TestBurnDeviceEntry 引擎设备条目 → 输出契约条目（表驱动）。空白 DVD-R 的取值为 2026-11-24 真机实测
func TestBurnDeviceEntry(t *testing.T) {
	cases := []struct {
		name  string
		entry imapi2.Device
		want  BurnDevice
	}{
		{"空白 DVD-R（实测）", imapi2.Device{
			Recorder: imapi2.Recorder{Index: 0, UniqueID: "usb#1", Vendor: "lenovo", Product: "UltraslimDVD", VolumePath: `E:\`},
			Media:    imapi2.Media{Type: imapi2.MEDIA_TYPE_DVDDASHR, State: imapi2.MEDIA_STATE_BLANK | imapi2.MEDIA_STATE_APPENDABLE},
		}, BurnDevice{Index: 0, Path: `E:\`, Vendor: "lenovo", Product: "UltraslimDVD", Media: "DVD-R", Status: "blank"}},
		{"无介质", imapi2.Device{
			Recorder: imapi2.Recorder{Index: 1, UniqueID: "usb#2", VolumePath: `F:\`},
			Media:    imapi2.Media{Type: imapi2.MEDIA_TYPE_UNKNOWN},
		}, BurnDevice{Index: 1, Path: `F:\`, Status: "no-media"}},
		{"介质查询失败如实带原因", imapi2.Device{
			Recorder:   imapi2.Recorder{Index: 0, UniqueID: "usb#1"},
			MediaError: "读取介质类型失败: x",
		}, BurnDevice{Index: 0, Path: "usb#1", Status: "unknown", Note: "读取介质类型失败: x"}},
		{"已封盘且无盘符时路径用引擎标识", imapi2.Device{
			Recorder: imapi2.Recorder{Index: 2, UniqueID: "usb#3"},
			Media:    imapi2.Media{Type: imapi2.MEDIA_TYPE_CDR, State: imapi2.MEDIA_STATE_FINALIZED},
		}, BurnDevice{Index: 2, Path: "usb#3", Media: "CD-R", Status: "full"}},
	}
	for _, testCase := range cases {
		if got := burnDeviceEntry(testCase.entry); got != testCase.want {
			t.Errorf("%s: %+v ≠ %+v", testCase.name, got, testCase.want)
		}
	}
}

// TestBurnMediaRefusal 不可写介质的拒绝原因（表驱动）。封盘 DVD-R 的取值为 2026-11-24 真机实测：
// 封盘后介质类型报 DVD-ROM，状态位 0xA000 = 写保护 | 不支持（不含封盘位）；damaged 的 DVD-R 为同日
// 内置光驱实测（Windows 刻录过的盘，引擎只报 0x400）
func TestBurnMediaRefusal(t *testing.T) {
	cases := []struct {
		name    string
		media   imapi2.Media
		erasing bool   // 是否请求了 --blank
		want    string // 期望的 error_info；空串=可写
	}{
		{"空白 DVD-R 可写", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHR, State: imapi2.MEDIA_STATE_BLANK | imapi2.MEDIA_STATE_APPENDABLE, SupportedByWriter: true,
		}, false, ""},
		{"带封盘位的介质", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDPLUSR, State: imapi2.MEDIA_STATE_FINALIZED | imapi2.MEDIA_STATE_APPENDABLE,
		}, false, "光盘已封盘，无法继续写入"},
		{"封盘的 DVD-R（实测 DVD-ROM + 写保护位）与压盘同样报只读", imapi2.Media{
			Type:  imapi2.MEDIA_TYPE_DVDROM,
			State: imapi2.MEDIA_STATE_WRITE_PROTECTED | imapi2.MEDIA_STATE_UNSUPPORTED,
		}, false, "光盘是只读盘（DVD-ROM：压盘或已封盘），无法继续写入"},
		{"可擦写盘的写保护", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDRAM, State: imapi2.MEDIA_STATE_WRITE_PROTECTED,
		}, false, "光盘处于写保护状态，无法写入"},
		{"写入器不支持的介质", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHRW, State: imapi2.MEDIA_STATE_APPENDABLE,
		}, false, "当前介质 DVD-RW 不支持数据刻录"},
		{"损坏的一次写入盘（实测）：只能换盘", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHR, State: imapi2.MEDIA_STATE_DAMAGED, SupportedByWriter: true,
		}, false, "光盘状态异常（上一个区段没有正常关闭或需要恢复），无法写入；请换一张空白盘"},
		{"损坏的一次写入盘带 --blank：由后面的不可擦除分支报错", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHR, State: imapi2.MEDIA_STATE_DAMAGED, SupportedByWriter: true,
		}, true, ""},
		{"损坏的可擦写盘：未要求擦除时提示先擦除", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHRW, State: imapi2.MEDIA_STATE_DAMAGED | imapi2.MEDIA_STATE_ERASE_REQUIRED, SupportedByWriter: true,
		}, false, "光盘状态异常（上一个区段没有正常关闭或需要恢复），无法写入；加 --blank 先擦除再刻"},
		{"损坏的可擦写盘：请求擦除后放行，交给擦除流程", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHRW, State: imapi2.MEDIA_STATE_DAMAGED | imapi2.MEDIA_STATE_ERASE_REQUIRED, SupportedByWriter: true,
		}, true, ""},
		{"须先擦除的可擦写盘", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_CDRW, State: imapi2.MEDIA_STATE_ERASE_REQUIRED, SupportedByWriter: true,
		}, false, "光盘需要先擦除才能写入；加 --blank 先擦除再刻"},
		{"上一区段没写完的一次写入盘", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHR, State: imapi2.MEDIA_STATE_NON_EMPTY_SESSION, SupportedByWriter: true,
		}, false, "光盘上一个区段没有写完，无法继续写入；请换一张空白盘"},
		{"状态位指明不支持该介质", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_BDR, State: imapi2.MEDIA_STATE_UNSUPPORTED, SupportedByWriter: true,
		}, false, "刻录机不支持在 BD-R 上刻录"},
		{"已封盘的盘不因请求擦除而放行", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDPLUSRW, State: imapi2.MEDIA_STATE_FINALIZED,
		}, true, "光盘已封盘，无法继续写入"},
	}
	for _, testCase := range cases {
		refusal := burnMediaRefusal(testCase.media, testCase.erasing)
		switch {
		case testCase.want == "" && refusal != nil:
			t.Errorf("%s: 不应拒绝，得到: %v", testCase.name, refusal)
		case testCase.want != "" && refusal == nil:
			t.Errorf("%s: 应拒绝（%s）", testCase.name, testCase.want)
		case refusal != nil && refusal.Error() != testCase.want:
			t.Errorf("%s: %q ≠ %q", testCase.name, refusal.Error(), testCase.want)
		}
	}
}
