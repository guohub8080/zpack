// Package imapi2 是 Windows 自带刻录引擎（IMAPI2）的纯 Go 薄胶水。
//
// 引擎分工（资源管理器「母盘式」刻录用的同一套系统组件）：
//
//	imapi2.dll  ：IDiscMaster2 设备枚举 / IDiscRecorder2 刻录机 / IDiscFormat2Data 数据写入 / IDiscFormat2Erase 擦除
//	imapi2fs.dll：IFileSystemImage 目录 → 光盘镜像（ISO9660 + Joliet + UDF，含多区段导入）
//
// 原则（2026-11-24 用户定案「有现成成熟的库就不要造轮子」）：镜像生成、文件流、写入调度全部用系统现成能力，
// 本包只做调用编排——经 go-ole 按方法名走 IDispatch；不手写虚函数表偏移、不手写 ISO9660/UDF 生成器、
// 不手写 SCSI/MMC 命令。Go 生态无现成的 IMAPI2 调用封装（2026-11-24 检索：仅
// deploymenttheory/go-bindings-win32 含生成的接口定义，2026-07 新建，无调用编排，未采用）。
//
// 接口名与枚举值取自微软 SDK 的 imapi2.idl / imapi2fs.idl / imapi2*error.h
// （microsoft/win32metadata：generation/WinSDK/RecompiledIdlHeaders/um）；CLSID 取自注册表
// HKCR\CLSID 并与 coclass 定义逐个核对。
//
// 环境事实（2026-11-24 Windows 11 26200 实测）：本机无 imapi2ms.dll（脚本封装层），ProgID
// （IMAPI2.MsftDiscMaster2 等）未注册——按 CLSID 创建核心对象即可；核心对象自带类型信息，可按方法名解析。
package imapi2

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 介质物理类型 IMAPI_MEDIA_PHYSICAL_TYPE（SDK imapi2.idl）
const (
	MEDIA_TYPE_UNKNOWN      int32 = 0x00 // 无介质或无法识别
	MEDIA_TYPE_CDROM        int32 = 0x01
	MEDIA_TYPE_CDR          int32 = 0x02
	MEDIA_TYPE_CDRW         int32 = 0x03
	MEDIA_TYPE_DVDROM       int32 = 0x04
	MEDIA_TYPE_DVDRAM       int32 = 0x05
	MEDIA_TYPE_DVDPLUSR     int32 = 0x06
	MEDIA_TYPE_DVDPLUSRW    int32 = 0x07
	MEDIA_TYPE_DVDPLUSR_DL  int32 = 0x08
	MEDIA_TYPE_DVDDASHR     int32 = 0x09
	MEDIA_TYPE_DVDDASHRW    int32 = 0x0A
	MEDIA_TYPE_DVDDASHR_DL  int32 = 0x0B
	MEDIA_TYPE_DISK         int32 = 0x0C
	MEDIA_TYPE_DVDPLUSRW_DL int32 = 0x0D
	MEDIA_TYPE_HDDVDROM     int32 = 0x0E
	MEDIA_TYPE_HDDVDR       int32 = 0x0F
	MEDIA_TYPE_HDDVDRAM     int32 = 0x10
	MEDIA_TYPE_BDROM        int32 = 0x11
	MEDIA_TYPE_BDR          int32 = 0x12
	MEDIA_TYPE_BDRE         int32 = 0x13
)

// 介质状态位 IMAPI_FORMAT2_DATA_MEDIA_STATE（SDK imapi2.idl）
const (
	MEDIA_STATE_OVERWRITE_ONLY    int32 = 0x0001 // 只能覆写
	MEDIA_STATE_BLANK             int32 = 0x0002 // 空白
	MEDIA_STATE_APPENDABLE        int32 = 0x0004 // 可追加
	MEDIA_STATE_FINAL_SESSION     int32 = 0x0008 // 只能再写一个区段
	MEDIA_STATE_DAMAGED           int32 = 0x0400 // 需擦除或其他恢复才能用
	MEDIA_STATE_ERASE_REQUIRED    int32 = 0x0800 // 须先擦除
	MEDIA_STATE_NON_EMPTY_SESSION int32 = 0x1000 // 上一区段写了一半（不支持）
	MEDIA_STATE_WRITE_PROTECTED   int32 = 0x2000 // 介质或刻录机写保护
	MEDIA_STATE_FINALIZED         int32 = 0x4000 // 已封盘
	MEDIA_STATE_UNSUPPORTED       int32 = 0x8000 // 数据写入器不支持该介质
)

// SECTOR_BYTES 光盘数据扇区字节数（IMAPI_SECTOR_SIZE）
const SECTOR_BYTES int64 = 2048

// SCODE_IMPORT_SEEK_FAILURE 引擎错误码 IMAPI_E_IMPORT_SEEK_FAILURE（SDK imapi2fserror.h）——
// 官方文档注明：介质为空白时 IFileSystemImage::put_MultisessionInterfaces 也返回它
const SCODE_IMPORT_SEEK_FAILURE uint32 = 0xC0AAB156

// SCODE_MEDIA_NO_MEDIA 引擎错误码 E_IMAPI_RECORDER_MEDIA_NO_MEDIA（SDK imapi2error.h）——没放盘时
// 读介质类型等属性返回它而非空值（2026-11-24 真机实测）
const SCODE_MEDIA_NO_MEDIA uint32 = 0xC0AA0202

// SCODE_MEDIA_INCOMPATIBLE 引擎错误码 E_IMAPI_RECORDER_MEDIA_INCOMPATIBLE（SDK imapi2error.h）——
// 盘在光驱里、光驱却认不出它的身份时读介质类型返回它（盘型超出光驱能力 / 放反 / 没卡牢 / 划伤都会这样；
// 2026-11-24 真机实测）。这是「有盘但认不出」，不是查询失败
const SCODE_MEDIA_INCOMPATIBLE uint32 = 0xC0AA0203

// SCODE_EMPTY_DISC 引擎错误码 IMAPI_E_EMPTY_DISC（SDK imapi2fserror.h）——盘读得到、上面没有东西
const SCODE_EMPTY_DISC uint32 = 0xC0AAB150

// SCODE_NO_SUPPORTED_FILE_SYSTEM 引擎错误码 IMAPI_E_NO_SUPPORTED_FILE_SYSTEM（SDK imapi2fserror.h）——
// 盘读得到、但没有本引擎认识的数据文件系统（音频 CD 之类）
const SCODE_NO_SUPPORTED_FILE_SYSTEM uint32 = 0xC0AAB151

// UNRECOGNIZED_MEDIA_HINT 光驱认不出盘时的排查提示。软件层面分不出是哪一种，只能把常见的都列出来
const UNRECOGNIZED_MEDIA_HINT = "请检查：盘是否放反（标签面应朝上）、是否卡牢、盘片类型是否超出刻录机能力（如蓝光盘放入 DVD 光驱）、盘片是否划伤或太脏"

// 介质状态对外名称（burn devices 输出契约的 status 字段）
const (
	STATUS_NO_MEDIA        = "no-media"        // 无介质
	STATUS_BLANK           = "blank"           // 空白
	STATUS_APPENDABLE      = "appendable"      // 有数据，可追加区段
	STATUS_FULL            = "full"            // 已封盘
	STATUS_WRITE_PROTECTED = "write-protected" // 写保护
	STATUS_ERASE_REQUIRED  = "erase-required"  // 须先擦除
	STATUS_DAMAGED         = "damaged"         // 损坏
	STATUS_UNSUPPORTED     = "unsupported"     // 不支持该介质
	STATUS_UNRECOGNIZED    = "unrecognized"    // 光驱认不出这张盘（读不出身份，或读不出内容）
	STATUS_UNKNOWN         = "unknown"         // 其他
)

// mediaTypeInfo 介质类型描述
type mediaTypeInfo struct {
	name          string // 对外名称（输出契约的 media 字段）
	baseTenthKBps int32  // 1× 写入速度，单位 0.1 kB/s：CD 1764 / DVD 13850 / DVD-RAM 27700 / BD 44956
	erasable      bool   // 可擦写
}

// MEDIA_TYPE_TABLE 介质类型描述表（未知类型不在表内）
var MEDIA_TYPE_TABLE = map[int32]mediaTypeInfo{
	MEDIA_TYPE_CDROM:        {name: "CD-ROM", baseTenthKBps: 1764},
	MEDIA_TYPE_CDR:          {name: "CD-R", baseTenthKBps: 1764},
	MEDIA_TYPE_CDRW:         {name: "CD-RW", baseTenthKBps: 1764, erasable: true},
	MEDIA_TYPE_DVDROM:       {name: "DVD-ROM", baseTenthKBps: 13850},
	MEDIA_TYPE_DVDRAM:       {name: "DVD-RAM", baseTenthKBps: 27700, erasable: true},
	MEDIA_TYPE_DVDPLUSR:     {name: "DVD+R", baseTenthKBps: 13850},
	MEDIA_TYPE_DVDPLUSRW:    {name: "DVD+RW", baseTenthKBps: 13850, erasable: true},
	MEDIA_TYPE_DVDPLUSR_DL:  {name: "DVD+R DL", baseTenthKBps: 13850},
	MEDIA_TYPE_DVDDASHR:     {name: "DVD-R", baseTenthKBps: 13850},
	MEDIA_TYPE_DVDDASHRW:    {name: "DVD-RW", baseTenthKBps: 13850, erasable: true},
	MEDIA_TYPE_DVDDASHR_DL:  {name: "DVD-R DL", baseTenthKBps: 13850},
	MEDIA_TYPE_DVDPLUSRW_DL: {name: "DVD+RW DL", baseTenthKBps: 13850, erasable: true},
	MEDIA_TYPE_HDDVDROM:     {name: "HD DVD-ROM"},
	MEDIA_TYPE_HDDVDR:       {name: "HD DVD-R"},
	MEDIA_TYPE_HDDVDRAM:     {name: "HD DVD-RAM", erasable: true},
	MEDIA_TYPE_BDROM:        {name: "BD-ROM", baseTenthKBps: 44956},
	MEDIA_TYPE_BDR:          {name: "BD-R", baseTenthKBps: 44956},
	MEDIA_TYPE_BDRE:         {name: "BD-RE", baseTenthKBps: 44956, erasable: true},
}

// MediaTypeName 介质类型名（未知类型如实报数值）
func MediaTypeName(mediaType int32) string {
	if info, ok := MEDIA_TYPE_TABLE[mediaType]; ok {
		return info.name
	}
	return fmt.Sprintf("未知介质(%d)", mediaType)
}

// Media 介质信息（数据写入器视角）
type Media struct {
	Type               int32   // IMAPI_MEDIA_PHYSICAL_TYPE；MEDIA_TYPE_UNKNOWN=无介质
	State              int32   // IMAPI_FORMAT2_DATA_MEDIA_STATE 位标志
	TotalSectors       int32   // 介质总扇区数
	FreeSectors        int32   // 剩余可写扇区数
	PreviousSession    int32   // 上一区段起始扇区；-1=无
	WriteSpeeds        []int32 // 该介质在当前刻录机上支持的写入速度（kB/s，引擎给的顺序）
	PhysicallyBlank    bool    // 刻录机报告为物理空白
	HeuristicallyBlank bool    // 启发式判定为空白（DVD+RW/DVD-RAM 等覆写介质主要看它）
	SupportedByWriter  bool    // 数据写入器支持当前介质
	Unrecognized       bool    // 光驱认不出这张盘：读不出身份，或读不出内容（有盘，但无从得知类型与状态）
}

// Present 是否有介质（认不出的盘也算有盘）
func (media Media) Present() bool {
	return media.Type != MEDIA_TYPE_UNKNOWN || media.Unrecognized
}

// TypeName 介质类型名
func (media Media) TypeName() string {
	return MediaTypeName(media.Type)
}

// StatusName 介质状态名（blank / appendable / full / no-media …）
func (media Media) StatusName() string {
	switch {
	case media.Unrecognized:
		return STATUS_UNRECOGNIZED
	case !media.Present():
		return STATUS_NO_MEDIA
	case media.State&MEDIA_STATE_FINALIZED != 0:
		return STATUS_FULL
	// 封盘的一次写入盘对外报告为只读介质类型并带写保护位（2026-11-24 真机实测封盘的 DVD-R：类型 DVD-ROM、
	// 状态 0xA000 = 写保护 | 不支持，并不含封盘位）——与压盘无法区分，一律按「已封盘 / 只读」呈现
	case media.State&MEDIA_STATE_WRITE_PROTECTED != 0 && IsRomMediaType(media.Type):
		return STATUS_FULL
	case media.State&MEDIA_STATE_WRITE_PROTECTED != 0:
		return STATUS_WRITE_PROTECTED
	case media.State&MEDIA_STATE_UNSUPPORTED != 0:
		return STATUS_UNSUPPORTED
	case media.State&MEDIA_STATE_DAMAGED != 0:
		return STATUS_DAMAGED
	case media.State&MEDIA_STATE_ERASE_REQUIRED != 0:
		return STATUS_ERASE_REQUIRED
	case media.State&MEDIA_STATE_BLANK != 0:
		return STATUS_BLANK
	// OVERWRITE_ONLY：DVD±RW 等覆写介质带着数据时的常态——可追加（随机写多区段），视同可追加
	case media.State&(MEDIA_STATE_APPENDABLE|MEDIA_STATE_FINAL_SESSION|MEDIA_STATE_OVERWRITE_ONLY) != 0:
		return STATUS_APPENDABLE
	}
	return STATUS_UNKNOWN
}

// Erasable 介质是否可擦写（CD-RW / DVD±RW / DVD-RAM / BD-RE 等）
func (media Media) Erasable() bool {
	return MEDIA_TYPE_TABLE[media.Type].erasable
}

// ROM_MEDIA_TYPES 出厂即只读的介质类型（压盘）
var ROM_MEDIA_TYPES = map[int32]bool{
	MEDIA_TYPE_CDROM:    true,
	MEDIA_TYPE_DVDROM:   true,
	MEDIA_TYPE_HDDVDROM: true,
	MEDIA_TYPE_BDROM:    true,
}

// IsRomMediaType 介质类型本身是否只读（压盘）
func IsRomMediaType(mediaType int32) bool {
	return ROM_MEDIA_TYPES[mediaType]
}

// FreeBytes 剩余可写字节数
func (media Media) FreeBytes() int64 {
	return int64(media.FreeSectors) * SECTOR_BYTES
}

// SpeedChoice 写入速度选择结果
type SpeedChoice struct {
	KBps     int32 // 交给引擎的写入速度（kB/s）
	Rate     int   // 折合倍速
	Adjusted bool  // 与请求倍速不同（介质/刻录机不支持该档）
}

// PickWriteSpeed 把「N 倍速」落到介质实际支持的写入速度档位上：取不超过请求值的最高档，
// 全都高于请求值时取最低档。supported 为引擎报告的写入速度表（kB/s，任意顺序）
func PickWriteSpeed(mediaType int32, requestedRate int, supported []int32) SpeedChoice {
	baseTenth := MEDIA_TYPE_TABLE[mediaType].baseTenthKBps
	// 基准按 0.1 kB/s 记并四舍五入到 kB/s——CD 1×=176.4 kB/s，16× 应为 2822 而非整数基准算出的 2816
	requestedKBps := int32(0)
	if requestedRate > 0 {
		requestedKBps = (baseTenth*int32(requestedRate) + 5) / 10
	}
	choice := SpeedChoice{KBps: requestedKBps, Rate: requestedRate}
	if len(supported) == 0 || baseTenth == 0 || requestedRate <= 0 {
		return choice
	}
	lowest, bestFit := supported[0], int32(0)
	for _, kbps := range supported {
		lowest = min(lowest, kbps)
		if kbps <= requestedKBps {
			bestFit = max(bestFit, kbps)
		}
	}
	if bestFit > 0 {
		choice.KBps = bestFit
	} else {
		choice.KBps = lowest
	}
	choice.Rate = int(math.Round(float64(choice.KBps) * 10 / float64(baseTenth)))
	choice.Adjusted = choice.Rate != requestedRate
	return choice
}

// RecorderIndexError 设备序号超出刻录机数量
type RecorderIndexError struct {
	Index int // 请求的设备序号
	Count int // 实际刻录机数量
}

func (failure *RecorderIndexError) Error() string {
	return fmt.Sprintf("设备序号 %d 超出范围（共 %d 台）", failure.Index, failure.Count)
}

// CallError 引擎调用失败：SCode 是引擎的真实错误码（IMAPI 错误码见 SDK imapi2error.h / imapi2fserror.h），
// Message 是系统给出的本地化说明
type CallError struct {
	Operation string // 出错的操作
	HResult   uint32 // IDispatch::Invoke 的外层码（引擎报错时多为 DISP_E_EXCEPTION）
	SCode     uint32 // 引擎真实错误码
	Message   string // 系统给的本地化说明；无则为空
}

func (failure *CallError) Error() string {
	if failure.Message != "" {
		return fmt.Sprintf("%s失败: %s（0x%08X）", failure.Operation, failure.Message, failure.SCode)
	}
	return fmt.Sprintf("%s失败（0x%08X）", failure.Operation, failure.SCode)
}

// IsSCode err 是否为指定的引擎错误码
func IsSCode(err error, code uint32) bool {
	var callErr *CallError
	return errors.As(err, &callErr) && callErr.SCode == code
}

// UDF dstring 的首字符「压缩标识」——引擎的 ImportedVolumeName 原样带着它，其后每个字符对应一个原始字节
const (
	UDF_DSTRING_COMPRESSION_8BIT  rune = 0x08 // 其后每字节一个 8 位字符
	UDF_DSTRING_COMPRESSION_16BIT rune = 0x10 // 其后每两个大端字节一个 16 位码元
)

// sanitizeVolumeName 把引擎读回的盘上卷标变成可写回的卷标；还原不出可信的原名就返回空串（调用方放弃沿用）。
// ImportedVolumeName 给的是 UDF dstring 的原始字节序列，引擎把它变成字符串时走的是本机 ANSI 代码页，
// 还会在 0x00 处截断（2026-11-24 内置光驱实测：「照片备份」的 0xFD 读回成了 '?'——按当时「16 位码元被按
// 小端解读」的假设去还原，得到的是乱码「焀最爀䜀夀一㼀」，并且被写成了盘名）。
// 所以带压缩标识的字节串只在确定无损时才还原：
//   - 8 位形态：不许有被代码页合并出的字符（≥ 0x100，说明字节边界已错位）；带高位字节时又不许出现 '?'
//     （丢失的字节正是变成它）
//   - 16 位形态：'?' 也可能是合法的低字节，分不清丢没丢，所以只认「纯 ASCII 且没有 ?」的字节串
//
// 不带压缩标识的普通字符串只清理控制字符。
func sanitizeVolumeName(raw string) string {
	units := []rune(raw)
	if len(units) == 0 {
		return ""
	}
	compression := units[0]
	if compression != UDF_DSTRING_COMPRESSION_8BIT && compression != UDF_DSTRING_COMPRESSION_16BIT {
		return cleanVolumeName(units)
	}
	payload := units[1:]
	hasHigh, hasQuestion := false, false
	for _, unit := range payload {
		hasHigh = hasHigh || unit >= utf8.RuneSelf
		hasQuestion = hasQuestion || unit == '?'
	}
	switch compression {
	case UDF_DSTRING_COMPRESSION_16BIT:
		if len(payload)%2 != 0 || hasHigh || hasQuestion {
			return ""
		}
		decoded := make([]rune, 0, len(payload)/2)
		for index := 0; index+1 < len(payload); index += 2 {
			decoded = append(decoded, payload[index]<<8|payload[index+1])
		}
		return cleanVolumeName(decoded)
	default:
		for _, unit := range payload {
			if unit > 0xFF {
				return ""
			}
		}
		if hasHigh && hasQuestion {
			return ""
		}
		return cleanVolumeName(payload)
	}
}

// cleanVolumeName 去掉首尾控制字符；仍夹着控制字符则视为读不出，返回空串
func cleanVolumeName(units []rune) string {
	trimmed := strings.TrimFunc(string(units), unicode.IsControl)
	if strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return ""
	}
	return trimmed
}

// chooseVolumeName 这次写入用哪个卷标（空串=交给引擎自定）。显式指定的名字优先；追加区段未指定时沿用盘上原名——
// 先取 Windows 显示的卷标，取不到再还原引擎读回的字节串（见 sanitizeVolumeName，还原不出就放弃沿用，不写乱码）；
// 空白盘未指定则交给引擎自定。纯函数，与平台无关，所以放在这里而不是 engine_windows.go（单测在各平台都要能编译）
func chooseVolumeName(explicit, mounted, imported string, appended bool) string {
	switch {
	case explicit != "":
		return explicit
	case !appended:
		return ""
	case mounted != "":
		return mounted
	}
	return sanitizeVolumeName(imported)
}
