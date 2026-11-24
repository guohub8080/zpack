//go:build windows

// Windows 刻录后端：系统自带引擎 IMAPI2（互操作见 internal/imapi2）。
// 与 mac/Linux 的差异：目录 / 散文件由系统镜像库流式生成后直烧，不经临时 ISO 文件。
// 区段语义一致——--mode multi（缺省）不封盘可追加；mastered / --close 封盘；live（UDF 包写入）暂未接入。
package burn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/guohub8080/zpack/internal/imapi2"
	"github.com/guohub8080/zpack/internal/stdout"
)

// BURN_VOLUME_NAME_MAX 光盘名称上限（字符数）——取三种文件系统里最宽的 ISO9660 卷标识符 32 字符
const BURN_VOLUME_NAME_MAX = 32

// BURN_VOLUME_NAME_INVALID 光盘名称里换成下划线的字符：Windows 路径非法字符
const BURN_VOLUME_NAME_INVALID = `\/:*?"<>|`

// burnWindowsVolumeName Windows 卷标的温和规范化。系统镜像库原生接受 Unicode（中文名可用），
// 只清理它确实处理不好的部分（2026-11-24 探针实测）：控制字符引擎直接拒收 → 丢弃；
// 路径非法字符引擎会原样留在 UDF 卷标里 → 换成下划线；超长引擎不报错但各文件系统各自截断 → 先截到上限。
// 清理后为空的名字回退 ZPACK（同 burnSanitizeVolid）
func burnWindowsVolumeName(raw string) string {
	var builder strings.Builder
	for _, char := range raw {
		switch {
		case unicode.IsControl(char):
			// 丢弃
		case strings.ContainsRune(BURN_VOLUME_NAME_INVALID, char):
			builder.WriteRune('_')
		default:
			builder.WriteRune(char)
		}
	}
	name := strings.TrimSpace(builder.String())
	if trimmed := []rune(name); len(trimmed) > BURN_VOLUME_NAME_MAX {
		name = string(trimmed[:BURN_VOLUME_NAME_MAX])
	}
	if name == "" {
		return burnSanitizeVolid(name)
	}
	return name
}

// burnListDevicesWindows 枚举刻录机并读取各自介质（无刻录机=空列表）
func burnListDevicesWindows() []BurnDevice {
	devices := []BurnDevice{}
	listErr := imapi2.WithSession(func() error {
		entries, err := imapi2.ListDevices()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			devices = append(devices, burnDeviceEntry(entry))
		}
		return nil
	})
	if listErr != nil {
		stdout.ErrorPrint(listErr.Error())
	}
	return devices
}

// burnDeviceEntry 引擎设备条目 → 输出契约条目
func burnDeviceEntry(entry imapi2.Device) BurnDevice {
	device := BurnDevice{Index: entry.Index, Vendor: entry.Vendor, Product: entry.Product}
	device.Path = entry.UniqueID
	if entry.VolumePath != "" {
		device.Path = entry.VolumePath
	}
	switch {
	case entry.MediaError != "":
		device.Status = imapi2.STATUS_UNKNOWN
		device.Note = entry.MediaError
	case entry.Media.Unrecognized:
		// 光驱认不出这张盘——note 直接给排查提示，不让用户去猜「介质不兼容」是什么意思
		device.Status = imapi2.STATUS_UNRECOGNIZED
		device.Note = imapi2.UNRECOGNIZED_MEDIA_HINT
	case !entry.Media.Present():
		device.Status = imapi2.STATUS_NO_MEDIA
	default:
		device.Media = entry.Media.TypeName()
		device.Status = entry.Media.StatusName()
	}
	return device
}

// burnWindows 刻录主流程（Windows）：介质检查 → （--blank 擦除）→ 速度 → 内容 → 写入 → （弹出）。
// 返回写入的镜像字节数
func burnWindows(inputs []string, flags burnFlags) (int64, error) {
	var written int64
	burnErr := imapi2.WithSession(func() error {
		var innerErr error
		written, innerErr = burnWindowsInner(inputs, flags)
		return innerErr
	})
	return written, burnErr
}

// burnWindowsInner burnWindows 的会话内主体（COM 已初始化）
func burnWindowsInner(inputs []string, flags burnFlags) (int64, error) {
	writer, err := imapi2.OpenDataWriter(flags.device)
	var indexErr *imapi2.RecorderIndexError
	if errors.As(err, &indexErr) {
		return 0, fmt.Errorf("--device %d 超出范围（共 %d 台，见 zpack burn devices）", indexErr.Index, indexErr.Count)
	}
	if err != nil {
		return 0, err
	}
	defer writer.Close()
	recorder := writer.Recorder()
	stdout.LoggerInfo(fmt.Sprintf("刻录机: %s %s（%s）", recorder.Vendor, recorder.Product, burnRecorderLabel(recorder)))

	media, err := writer.Media()
	if err != nil {
		return 0, err
	}
	if !media.Present() {
		return 0, errors.New("刻录机里没有光盘——请放入光盘后重试")
	}
	if media.Unrecognized {
		stdout.LoggerInfo("介质: 光驱认不出这张光盘")
	} else {
		stdout.LoggerInfo(fmt.Sprintf("介质: %s（%s），剩余 %s", media.TypeName(), media.StatusName(), burnFormatBytes(media.FreeBytes())))
	}
	if refusal := burnMediaRefusal(media, flags.blank, false); refusal != nil {
		return 0, refusal
	}

	if flags.blank {
		if !media.Erasable() {
			return 0, fmt.Errorf("当前介质 %s 不可擦除（--blank 仅适用于 CD-RW / DVD±RW / DVD-RAM / BD-RE 等可擦写盘）", media.TypeName())
		}
		stdout.LoggerInfo("快速擦除光盘…")
		if err := writer.Erase(false, burnHeartbeat("擦除中")); err != nil {
			return 0, err
		}
		if media, err = writer.Media(); err != nil {
			return 0, err
		}
		// 擦完重读：擦除没能恢复的异常状态同样当场拒绝
		if refusal := burnMediaRefusal(media, false, true); refusal != nil {
			return 0, refusal
		}
	}

	// 擦除之后才判：擦完的盘是空白的，不用警告
	if warning := burnImageOnUsedDiscWarning(inputs, !media.HeuristicallyBlank); warning != "" {
		stdout.LoggerWarn(warning)
	}

	if flags.speed > 0 {
		choice := imapi2.PickWriteSpeed(media.Type, flags.speed, media.WriteSpeeds)
		speedErr := writer.SetSpeed(choice.KBps * 1000 / int32(imapi2.SECTOR_BYTES))
		switch {
		case speedErr != nil:
			stdout.LoggerInfo("写入速度设置未生效（" + speedErr.Error() + "），按刻录机默认速度刻录")
		case choice.Adjusted:
			stdout.LoggerInfo(fmt.Sprintf("写入速度: 请求 %d× 超出当前介质/刻录机支持的范围，按 %d× 刻录", flags.speed, choice.Rate))
		default:
			stdout.LoggerInfo(fmt.Sprintf("写入速度: %d×", choice.Rate))
		}
	}

	stream, sizeBytes, cleanup, err := burnWindowsSource(inputs, writer, media, flags)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if media.FreeSectors > 0 && sizeBytes > media.FreeBytes() {
		return 0, fmt.Errorf("内容大小 %s 超过光盘剩余空间 %s", burnFormatBytes(sizeBytes), burnFormatBytes(media.FreeBytes()))
	}
	if err := writer.SetCloseMedia(flags.close); err != nil {
		return 0, err
	}
	if err := writer.Write(stream, burnHeartbeat("刻录中")); err != nil {
		return 0, err
	}
	if !flags.noEject {
		if ejectErr := writer.Eject(); ejectErr != nil {
			stdout.LoggerInfo("光盘已刻录完成，但弹出未成功: " + ejectErr.Error())
		}
	}
	return sizeBytes, nil
}

// burnMediaRefusal 介质不可写时的拒绝原因（可写返回 nil）。封盘的一次写入盘对外报告为只读介质类型
// 并带写保护位（2026-11-24 真机实测封盘的 DVD-R：DVD-ROM + 0xA000，并不含封盘位），与压盘无法区分。
// erasing=请求了 --blank：可擦写盘的异常状态（损坏 / 须先擦除 / 上一区段写了一半）擦除即恢复，
// 交给擦除流程、擦完重读介质再判；一次性盘没有擦除这条路，当场拒绝——不能带着异常状态走到写入
// （2026-11-24 内置光驱实测：damaged 的 DVD-R 上，镜像直烧路径此前会直接调到 Write）。
// erased=这已经是擦除之后重读的结果：提示里不能再叫人「加 --blank」（他已经加过了），只能换盘
func burnMediaRefusal(media imapi2.Media, erasing, erased bool) error {
	switch {
	case media.Unrecognized:
		// 认不出的盘先于一切状态位：它的类型与状态位都不可信（可能只是被报成了只读盘）
		return errors.New("光驱认不出这张光盘，无法写入；" + imapi2.UNRECOGNIZED_MEDIA_HINT)
	case media.State&imapi2.MEDIA_STATE_FINALIZED != 0:
		return errors.New("光盘已封盘，无法继续写入")
	case media.State&imapi2.MEDIA_STATE_WRITE_PROTECTED != 0 && imapi2.IsRomMediaType(media.Type):
		return fmt.Errorf("光盘是只读盘（%s：压盘或已封盘），无法继续写入", media.TypeName())
	case media.State&imapi2.MEDIA_STATE_WRITE_PROTECTED != 0:
		return errors.New("光盘处于写保护状态，无法写入")
	// 「不支持」先于「损坏 / 须先擦除 / 半截区段」：刻录机根本写不了这种盘，恢复与否无从谈起。
	// 与 burn devices 的 StatusName 同一顺序——同一张盘两处给同一个结论
	case media.State&imapi2.MEDIA_STATE_UNSUPPORTED != 0:
		return fmt.Errorf("刻录机不支持在 %s 上刻录", media.TypeName())
	case !erasing && media.State&imapi2.MEDIA_STATE_DAMAGED != 0:
		return fmt.Errorf("光盘状态异常（上一个区段没有正常关闭或需要恢复），无法写入；%s", burnRecoveryHint(media, erased))
	case !erasing && media.State&imapi2.MEDIA_STATE_NON_EMPTY_SESSION != 0:
		return fmt.Errorf("光盘上一个区段没有写完，无法继续写入；%s", burnRecoveryHint(media, erased))
	case !erasing && media.State&imapi2.MEDIA_STATE_ERASE_REQUIRED != 0:
		return fmt.Errorf("光盘需要先擦除才能写入；%s", burnRecoveryHint(media, erased))
	case !media.SupportedByWriter:
		return fmt.Errorf("当前介质 %s 不支持数据刻录", media.TypeName())
	}
	return nil
}

// burnRecoveryHint 异常状态介质恢复到可写的办法：可擦写盘先擦除（--blank），一次性盘只能换盘；
// erased=已经擦过一遍仍异常——可擦写盘这时也只能换盘
func burnRecoveryHint(media imapi2.Media, erased bool) string {
	if media.Erasable() && !erased {
		return "加 --blank 先擦除再刻"
	}
	return "请换一张空白盘"
}

// burnWindowsSource 准备交给写入器的数据流：单个 .iso/.img/.bin 文件直接作为文件流；
// 单个目录 → 镜像根装其内容；多输入 → 每项按唯一名装入镜像根。返回的 cleanup 须在写完后调用
func burnWindowsSource(inputs []string, writer *imapi2.DataWriter, media imapi2.Media, flags burnFlags) (*imapi2.Stream, int64, func(), error) {
	if len(inputs) == 1 && burnLooksLikeImage(inputs[0]) {
		imagePath, err := filepath.Abs(inputs[0])
		if err != nil {
			return nil, 0, nil, err
		}
		info, err := os.Stat(imagePath)
		if err != nil {
			return nil, 0, nil, err
		}
		stream, err := imapi2.OpenFileStream(imagePath)
		if err != nil {
			return nil, 0, nil, err
		}
		stdout.LoggerInfo(fmt.Sprintf("镜像文件: %s（%s）", imagePath, burnFormatBytes(info.Size())))
		return stream, info.Size(), func() { stream.Release() }, nil
	}

	volumeName := flags.volid
	if !flags.volidGiven && !media.HeuristicallyBlank {
		volumeName = "" // 追加区段且未指定卷标 → 沿用盘上原有卷标
	}
	builder, err := writer.NewImage(volumeName, media)
	if err != nil {
		return nil, 0, nil, err
	}
	if name := builder.VolumeName(); name != "" {
		stdout.LoggerInfo("光盘名称: " + name)
	}
	// 构建失败即清场；成功后由返回的 cleanup 在写完后清场
	fail := func(buildErr error) (*imapi2.Stream, int64, func(), error) {
		builder.Close()
		return nil, 0, nil, buildErr
	}
	if len(inputs) == 1 {
		directory, absErr := filepath.Abs(inputs[0])
		if absErr != nil {
			return fail(absErr)
		}
		if addErr := builder.AddTreeContents(directory); addErr != nil {
			return fail(addErr)
		}
	} else {
		entryNames := burnUniqueEntryNames(inputs)
		for index, input := range inputs {
			absolute, absErr := filepath.Abs(input)
			if absErr != nil {
				return fail(absErr)
			}
			info, statErr := os.Stat(absolute)
			if statErr != nil {
				return fail(statErr)
			}
			if addErr := builder.AddEntry(entryNames[index], absolute, info.IsDir()); addErr != nil {
				return fail(addErr)
			}
		}
	}
	fileCount, _ := builder.FileCount()
	stream, sizeBytes, buildErr := builder.Build()
	if buildErr != nil {
		return fail(buildErr)
	}
	stdout.LoggerInfo(fmt.Sprintf("镜像: %d 个文件，%s（目录内容直烧，无临时文件）", fileCount, burnFormatBytes(sizeBytes)))
	return stream, sizeBytes, builder.Close, nil
}

// burnRecorderLabel 刻录机的可读标识：有盘符用盘符，否则用序号
func burnRecorderLabel(recorder imapi2.Recorder) string {
	if recorder.VolumePath != "" {
		return recorder.VolumePath
	}
	return fmt.Sprintf("第 %d 台", recorder.Index)
}

// burnHeartbeat 长操作期间每 5 秒一条过程日志（引擎的百分比进度需事件接收器，暂未接入）
func burnHeartbeat(stage string) func(elapsed time.Duration) {
	return func(elapsed time.Duration) {
		stdout.LoggerInfo(fmt.Sprintf("%s… 已用 %d 秒", stage, int(elapsed.Seconds())))
	}
}
