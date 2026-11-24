//go:build windows

package cli

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
// 封盘后介质类型报 DVD-ROM，状态位 0xA000 = 写保护 | 不支持（不含封盘位）
func TestBurnMediaRefusal(t *testing.T) {
	cases := []struct {
		name  string
		media imapi2.Media
		want  string // 期望的 error_info；空串=可写
	}{
		{"空白 DVD-R 可写", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHR, State: imapi2.MEDIA_STATE_BLANK | imapi2.MEDIA_STATE_APPENDABLE, SupportedByWriter: true,
		}, ""},
		{"带封盘位的介质", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDPLUSR, State: imapi2.MEDIA_STATE_FINALIZED | imapi2.MEDIA_STATE_APPENDABLE,
		}, "光盘已封盘，无法继续写入"},
		{"封盘的 DVD-R（实测 DVD-ROM + 写保护位）与压盘同样报只读", imapi2.Media{
			Type:  imapi2.MEDIA_TYPE_DVDROM,
			State: imapi2.MEDIA_STATE_WRITE_PROTECTED | imapi2.MEDIA_STATE_UNSUPPORTED,
		}, "光盘是只读盘（DVD-ROM：压盘或已封盘），无法继续写入"},
		{"可擦写盘的写保护", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDRAM, State: imapi2.MEDIA_STATE_WRITE_PROTECTED,
		}, "光盘处于写保护状态，无法写入"},
		{"写入器不支持的介质", imapi2.Media{
			Type: imapi2.MEDIA_TYPE_DVDDASHRW, State: imapi2.MEDIA_STATE_APPENDABLE,
		}, "当前介质 DVD-RW 不支持数据刻录"},
	}
	for _, testCase := range cases {
		refusal := burnMediaRefusal(testCase.media)
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
