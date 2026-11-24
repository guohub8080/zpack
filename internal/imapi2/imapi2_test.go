package imapi2

import (
	"errors"
	"fmt"
	"testing"
)

// TestMediaTypeName 介质类型名（表驱动）
func TestMediaTypeName(t *testing.T) {
	cases := []struct {
		name      string
		mediaType int32
		want      string
	}{
		{"CD-R", MEDIA_TYPE_CDR, "CD-R"},
		{"DVD-R", MEDIA_TYPE_DVDDASHR, "DVD-R"},
		{"DVD+RW", MEDIA_TYPE_DVDPLUSRW, "DVD+RW"},
		{"BD-RE", MEDIA_TYPE_BDRE, "BD-RE"},
		{"未知类型如实报数值", 0x7F, "未知介质(127)"},
	}
	for _, testCase := range cases {
		if got := MediaTypeName(testCase.mediaType); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}

// TestMediaStatusName 介质状态名（表驱动）。空白 DVD-R 的 BLANK|APPENDABLE 与封盘后的 0xA000
// 都是 2026-11-24 真机实测值
func TestMediaStatusName(t *testing.T) {
	cases := []struct {
		name  string
		media Media
		want  string
	}{
		{"无介质", Media{}, STATUS_NO_MEDIA},
		{"空白 DVD-R（实测 BLANK|APPENDABLE）", Media{Type: MEDIA_TYPE_DVDDASHR, State: MEDIA_STATE_BLANK | MEDIA_STATE_APPENDABLE}, STATUS_BLANK},
		{"已有一个区段", Media{Type: MEDIA_TYPE_DVDDASHR, State: MEDIA_STATE_APPENDABLE}, STATUS_APPENDABLE},
		{"只能再写一个区段", Media{Type: MEDIA_TYPE_CDR, State: MEDIA_STATE_APPENDABLE | MEDIA_STATE_FINAL_SESSION}, STATUS_APPENDABLE},
		{"覆写介质非空白态", Media{Type: MEDIA_TYPE_DVDPLUSRW, State: MEDIA_STATE_OVERWRITE_ONLY}, STATUS_APPENDABLE},
		{"封盘优先于其他位", Media{Type: MEDIA_TYPE_DVDDASHR, State: MEDIA_STATE_FINALIZED | MEDIA_STATE_APPENDABLE}, STATUS_FULL},
		{"封盘的 DVD-R（实测：报 DVD-ROM，状态 0xA000=写保护|不支持，不含封盘位）",
			Media{Type: MEDIA_TYPE_DVDROM, State: MEDIA_STATE_WRITE_PROTECTED | MEDIA_STATE_UNSUPPORTED}, STATUS_FULL},
		{"可擦写盘的写保护才是写保护", Media{Type: MEDIA_TYPE_DVDRAM, State: MEDIA_STATE_WRITE_PROTECTED}, STATUS_WRITE_PROTECTED},
		{"须先擦除", Media{Type: MEDIA_TYPE_DVDDASHRW, State: MEDIA_STATE_ERASE_REQUIRED}, STATUS_ERASE_REQUIRED},
		{"损坏", Media{Type: MEDIA_TYPE_CDROM, State: MEDIA_STATE_DAMAGED}, STATUS_DAMAGED},
		{"不支持", Media{Type: MEDIA_TYPE_DVDROM, State: MEDIA_STATE_UNSUPPORTED}, STATUS_UNSUPPORTED},
	}
	for _, testCase := range cases {
		if got := testCase.media.StatusName(); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}

// TestMediaErasable 介质可擦写判定（表驱动）
func TestMediaErasable(t *testing.T) {
	cases := []struct {
		name      string
		mediaType int32
		want      bool
	}{
		{"CD-R 一次写入", MEDIA_TYPE_CDR, false},
		{"DVD-R 一次写入", MEDIA_TYPE_DVDDASHR, false},
		{"DVD+R 一次写入", MEDIA_TYPE_DVDPLUSR, false},
		{"BD-R 一次写入", MEDIA_TYPE_BDR, false},
		{"CD-RW 可擦写", MEDIA_TYPE_CDRW, true},
		{"DVD-RW 可擦写", MEDIA_TYPE_DVDDASHRW, true},
		{"DVD+RW 可擦写", MEDIA_TYPE_DVDPLUSRW, true},
		{"DVD-RAM 可擦写", MEDIA_TYPE_DVDRAM, true},
		{"BD-RE 可擦写", MEDIA_TYPE_BDRE, true},
		{"无介质不可擦写", MEDIA_TYPE_UNKNOWN, false},
	}
	for _, testCase := range cases {
		media := Media{Type: testCase.mediaType}
		if got := media.Erasable(); got != testCase.want {
			t.Errorf("%s: %v ≠ %v", testCase.name, got, testCase.want)
		}
	}
}

// TestIsRomMediaType 出厂只读的介质类型判定（表驱动）
func TestIsRomMediaType(t *testing.T) {
	cases := []struct {
		name      string
		mediaType int32
		want      bool
	}{
		{"CD-ROM 压盘", MEDIA_TYPE_CDROM, true},
		{"DVD-ROM 压盘", MEDIA_TYPE_DVDROM, true},
		{"BD-ROM 压盘", MEDIA_TYPE_BDROM, true},
		{"DVD-R 可写", MEDIA_TYPE_DVDDASHR, false},
		{"DVD-RAM 可擦写", MEDIA_TYPE_DVDRAM, false},
		{"无介质", MEDIA_TYPE_UNKNOWN, false},
	}
	for _, testCase := range cases {
		if got := IsRomMediaType(testCase.mediaType); got != testCase.want {
			t.Errorf("%s: %v ≠ %v", testCase.name, got, testCase.want)
		}
	}
}

// TestPickWriteSpeed 写入速度落档（表驱动）。DVD-R 的 [5410 2705] 是 2026-11-24 真机实测的支持速度表
func TestPickWriteSpeed(t *testing.T) {
	cases := []struct {
		name          string
		mediaType     int32
		requestedRate int
		supported     []int32
		wantKBps      int32
		wantRate      int
		wantAdjusted  bool
	}{
		{"DVD-R 4× 命中最高档", MEDIA_TYPE_DVDDASHR, 4, []int32{5410, 2705}, 5410, 4, false},
		{"DVD-R 2× 命中低档", MEDIA_TYPE_DVDDASHR, 2, []int32{5410, 2705}, 2705, 2, false},
		{"DVD-R 8× 超出最高档→降到 4×", MEDIA_TYPE_DVDDASHR, 8, []int32{5410, 2705}, 5410, 4, true},
		{"DVD-R 1× 低于最低档→升到 2×", MEDIA_TYPE_DVDDASHR, 1, []int32{5410, 2705}, 2705, 2, true},
		{"DVD-R 3× 就近取不超过请求的最高档", MEDIA_TYPE_DVDDASHR, 3, []int32{5410, 2705}, 2705, 2, true},
		{"速度表乱序也能落档", MEDIA_TYPE_DVDDASHR, 4, []int32{2705, 5410}, 5410, 4, false},
		{"CD-R 16×", MEDIA_TYPE_CDR, 16, []int32{7060, 4234, 2822, 1411, 706}, 2822, 16, false},
		{"无速度表→按请求值", MEDIA_TYPE_DVDDASHR, 4, nil, 5540, 4, false},
	}
	for _, testCase := range cases {
		got := PickWriteSpeed(testCase.mediaType, testCase.requestedRate, testCase.supported)
		if got.KBps != testCase.wantKBps || got.Rate != testCase.wantRate || got.Adjusted != testCase.wantAdjusted {
			t.Errorf("%s: %+v ≠ KBps=%d Rate=%d Adjusted=%v", testCase.name, got, testCase.wantKBps, testCase.wantRate, testCase.wantAdjusted)
		}
	}
}

// TestMediaFreeBytes 剩余空间按 2048 字节/扇区换算（真机实测：空白 DVD-R 共 2297888 扇区）
func TestMediaFreeBytes(t *testing.T) {
	media := Media{Type: MEDIA_TYPE_DVDDASHR, FreeSectors: 2297888}
	if got, want := media.FreeBytes(), int64(4706074624); got != want {
		t.Fatalf("剩余空间 %d ≠ %d", got, want)
	}
	if media.Present() != true {
		t.Fatal("有介质应报告 Present")
	}
	if (Media{}).Present() {
		t.Fatal("类型未知应报告无介质")
	}
}

// TestIsSCode 引擎错误码判定（含被包装的错误）
func TestIsSCode(t *testing.T) {
	seekFailure := &CallError{Operation: "设置多区段接口", SCode: SCODE_IMPORT_SEEK_FAILURE, Message: "无法在源光盘上查找块 0。"}
	if got, want := seekFailure.Error(), "设置多区段接口失败: 无法在源光盘上查找块 0。（0xC0AAB156）"; got != want {
		t.Errorf("带说明的文案: %q ≠ %q", got, want)
	}
	bare := &CallError{Operation: "写入光盘", HResult: 0x80020009, SCode: 0xC0AA0003}
	if got, want := bare.Error(), "写入光盘失败（0xC0AA0003）"; got != want {
		t.Errorf("无说明的文案: %q ≠ %q", got, want)
	}
	if !IsSCode(fmt.Errorf("外层: %w", seekFailure), SCODE_IMPORT_SEEK_FAILURE) {
		t.Error("被包装的引擎错误应能按错误码识别")
	}
	if IsSCode(seekFailure, 0xC0AA0003) || IsSCode(errors.New("普通错误"), SCODE_IMPORT_SEEK_FAILURE) || IsSCode(nil, SCODE_IMPORT_SEEK_FAILURE) {
		t.Error("错误码不同或非引擎错误不应匹配")
	}
	if !IsSCode(&CallError{Operation: "读取介质类型", SCode: SCODE_MEDIA_NO_MEDIA, Message: "设备中没有介质。"}, SCODE_MEDIA_NO_MEDIA) {
		t.Error("没放盘的引擎错误码应能识别")
	}
}

// TestRecorderIndexErrorText 设备序号错误的文案（CLI 在其外层拼成 --device 提示）
func TestRecorderIndexErrorText(t *testing.T) {
	failure := &RecorderIndexError{Index: 5, Count: 1}
	if got, want := failure.Error(), "设备序号 5 超出范围（共 1 台）"; got != want {
		t.Fatalf("%q ≠ %q", got, want)
	}
}

// TestSanitizeVolumeName 引擎读回的盘上卷标清理（表驱动）。"\bZPACK" 是 2026-11-24 真机实测值；
// 16 位形态的取值来自同日的内置光驱实测：引擎每个字节给一个字符，「照片备份」里的 0xFD 已被代码页换成 '?'
// ——按旧假设（16 位码元被按小端解读）还原出来的是乱码，并且被写成了盘名
func TestSanitizeVolumeName(t *testing.T) {
	// 照=U+7167 片=U+7247：大端字节 71 67 72 47，全是 ASCII 字节——无损
	photo := string([]rune{0x10, 0x71, 0x67, 0x72, 0x47})
	// 备=U+5907 份=U+4EFD：大端字节 59 07 4E FD，引擎读回时 0xFD 丢了，成了 '?'
	lostTail := string([]rune{0x10, 0x71, 0x67, 0x72, 0x47, 0x59, 0x07, 0x4E, 0x3F})
	accented := string([]rune{0x08, 'R', 0xE9, 's', 'u', 'm', 0xE9}) // 西欧代码页下高位字节逐字节对应
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"UDF 8 位卷标（实测带 0x08 前导）", "\bZPACK", "ZPACK"},
		{"UDF 8 位卷标带高位字节（逐字节对应，无损）", accented, string([]rune{'R', 0xE9, 's', 'u', 'm', 0xE9})},
		{"UDF 8 位卷标带高位字节、又有字节丢成了 ?（实测形态）", string([]rune{0x08, 'R', 0xE9, 's', 'u', 'm', 0x3F}), ""},
		{"UDF 8 位卷标的字节被 GBK 合并成汉字：边界已错位", "\b" + string([]rune{0x4E2D}), ""},
		{"UDF 8 位卷标里本来就有的 ? 没有高位字节相伴：是原名的一部分", "\bR?sum?", "R?sum?"},
		{"UDF 16 位中文卷标：纯 ASCII 字节按大端还原", photo, "照片"},
		{"UDF 16 位中文卷标丢了字节（实测：份 → ?）：放弃沿用", lostTail, ""},
		{"UDF 16 位卷标含 ? 低字节：分不清丢没丢，放弃沿用", string([]rune{0x10, 0x71, 0x67, 0x00, 0x3F}), ""},
		{"UDF 16 位卷标带高位字节：放弃沿用", string([]rune{0x10, 0xD8, 0x00, 0xDC, 0x01}), ""},
		{"UDF 16 位卷标字节数为奇数（被 0x00 截断，实测 \\x10q）", "\x10q", ""},
		{"普通卷标原样", "ZPACK", "ZPACK"},
		{"普通 Unicode 卷标原样", "照片备份", "照片备份"},
		{"首尾控制字符被剥掉", "\x07abc\x00", "abc"},
		{"夹着控制字符视为读不出", "a\x01b", ""},
		{"空串", "", ""},
		{"只有前导字节", "\b", ""},
	}
	for _, testCase := range cases {
		if got := sanitizeVolumeName(testCase.raw); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}

// TestChooseVolumeName 写入时卷标的取舍（表驱动）：显式指定 > 追加时沿用（Windows 卷标 > 引擎读回值）> 空白盘交给引擎
func TestChooseVolumeName(t *testing.T) {
	photo := string([]rune{0x10, 0x71, 0x67, 0x72, 0x47})                      // 引擎读回的「照片」，可无损还原
	lostTail := string([]rune{0x10, 0x71, 0x67, 0x72, 0x47, 0x59, 0x07, 0x4E}) // 末字节丢失的「照片备?」
	cases := []struct {
		name     string
		explicit string
		mounted  string
		imported string
		appended bool
		want     string
	}{
		{"空白盘未指定：交给引擎", "", "", "", false, ""},
		{"空白盘显式指定", "备份", "", "", false, "备份"},
		{"追加未指定、Windows 给出卷标：沿用", "", "照片备份", "", true, "照片备份"},
		{"追加未指定、没有挂载点：还原引擎读回值", "", "", photo, true, "照片"},
		{"追加未指定、引擎读回值不可信：放弃沿用", "", "", lostTail, true, ""},
		{"显式指定压过盘上原名", "新名字", "照片备份", "", true, "新名字"},
	}
	for _, testCase := range cases {
		if got := chooseVolumeName(testCase.explicit, testCase.mounted, testCase.imported, testCase.appended); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}
