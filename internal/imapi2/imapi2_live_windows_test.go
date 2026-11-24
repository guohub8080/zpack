//go:build windows

package imapi2

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// TestLiveReadOnly 真机只读验证（可选）：有刻录机的 Windows 机器上设 ZPACK_IMAPI2_LIVE_TEST=1 才跑——
// 枚举设备、读介质、设置写入速度、从临时目录构建镜像。全程不写入光盘；默认跳过，go test ./... 不碰光驱
func TestLiveReadOnly(t *testing.T) {
	if os.Getenv("ZPACK_IMAPI2_LIVE_TEST") == "" {
		t.Skip("设 ZPACK_IMAPI2_LIVE_TEST=1 才做真机只读验证")
	}
	source := t.TempDir()
	nested := filepath.Join(source, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		filepath.Join(source, "alpha.txt"):        "甲文件内容",
		filepath.Join(source, "长文件名 with 空格.bin"): "乙",
		filepath.Join(nested, "gamma.md"):         "嵌套丙",
	}
	for path, content := range fixtures {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loose := filepath.Join(t.TempDir(), "loose.txt")
	if err := os.WriteFile(loose, []byte("散文件"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionErr := WithSession(func() error {
		devices, err := ListDevices()
		if err != nil {
			return err
		}
		if len(devices) == 0 {
			t.Log("无刻录机，真机验证到此为止")
			return nil
		}
		for _, device := range devices {
			t.Logf("设备 %d: %s %s %s 介质=%s 状态=%s 查询错误=%q",
				device.Index, device.Vendor, device.Product, device.VolumePath,
				device.Media.TypeName(), device.Media.StatusName(), device.MediaError)
		}
		writer, err := OpenDataWriter(0)
		if err != nil {
			return err
		}
		defer writer.Close()
		media, err := writer.Media()
		if err != nil {
			return err
		}
		if !media.Present() {
			t.Log("刻录机里没有光盘，介质相关验证跳过")
			return nil
		}
		t.Logf("介质: %+v", media)
		// 不可追加的介质都跳过镜像构建：除了封盘 / 写保护，还有引擎判为不可用的几类（SDK 注释：损坏 = 上一个
		// 区段没正常关闭、须先擦除、上一区段写了一半、不支持）——这类盘上多区段接口设置必然失败，不是回归
		// （2026-11-24 内置光驱实测：Windows 刻录过的 DVD-R 报 damaged，此前这里判 FAIL）
		if media.State&(MEDIA_STATE_FINALIZED|MEDIA_STATE_WRITE_PROTECTED|MEDIA_STATE_DAMAGED|MEDIA_STATE_ERASE_REQUIRED|
			MEDIA_STATE_NON_EMPTY_SESSION|MEDIA_STATE_UNSUPPORTED) != 0 || !media.SupportedByWriter {
			t.Logf("介质不可追加（%s），介质相关验证跳过", media.StatusName())
			return nil
		}
		// 流参数按值传入的编组核对（Write 的 IStream 参数走同一通道）：借一个接收接口指针、无副作用的方法验证
		supportedValue, supportedErr := invokeRaw(writer.writer, "IsCurrentMediaSupported", ole.DISPATCH_METHOD,
			"介质支持判定", unknownArg((*ole.IUnknown)(unsafe.Pointer(writer.recorder))))
		if supportedErr != nil {
			return supportedErr
		}
		byValueSupported := supportedValue.Value()
		supportedValue.Clear()
		if byValueSupported != true {
			return fmt.Errorf("接口指针按值传入的编组核对未通过: %v", byValueSupported)
		}
		choice := PickWriteSpeed(media.Type, 1, media.WriteSpeeds)
		if err := writer.SetSpeed(choice.KBps * 1000 / int32(SECTOR_BYTES)); err != nil {
			return fmt.Errorf("设置写入速度 %d kB/s 失败: %w", choice.KBps, err)
		}
		if err := writer.SetCloseMedia(false); err != nil {
			return err
		}
		if !media.HeuristicallyBlank {
			if err := checkKeptVolumeName(writer, media); err != nil {
				return err
			}
		}
		builder, err := writer.NewImage("ZPACKTEST", media)
		if err != nil {
			return err
		}
		defer builder.Close()
		if name := builder.VolumeName(); name != "ZPACKTEST" {
			return fmt.Errorf("显式指定的卷标应原样生效: %q", name)
		}
		if err := builder.AddTreeContents(source); err != nil {
			return err
		}
		if err := builder.AddEntry("loose.txt", loose, false); err != nil {
			return err
		}
		if err := builder.AddEntry("sub", nested, true); err != nil {
			return err
		}
		fileCount, err := builder.FileCount()
		if err != nil {
			return err
		}
		stream, sizeBytes, err := builder.Build()
		if err != nil {
			return err
		}
		defer stream.Release()
		t.Logf("镜像: %d 个文件, %d 字节, 写入速度 %d×", fileCount, sizeBytes, choice.Rate)
		if sizeBytes <= 0 || sizeBytes%SECTOR_BYTES != 0 {
			return fmt.Errorf("镜像大小 %d 不是正的扇区整数倍", sizeBytes)
		}
		if fileCount < 5 { // 3 个目录内文件 + 1 个散文件 + 1 个嵌套目录装入的文件
			return fmt.Errorf("镜像文件数 %d 少于预期的 5", fileCount)
		}
		return nil
	})
	if sessionErr != nil {
		t.Fatal(sessionErr)
	}
}

// checkKeptVolumeName 追加区段且未指定卷标时，镜像应沿用盘上原有卷标
// （2026-11-24 实测：引擎导入上一区段后缺省会换成时间戳卷标）。盘有挂载点时以 Windows 显示的卷标为准
// ——引擎读回值要过 ANSI 代码页，中文名不可靠（同日内置光驱实测）
func checkKeptVolumeName(writer *DataWriter, media Media) error {
	kept, err := writer.NewImage("", media)
	if err != nil {
		return err
	}
	defer kept.Close()
	expected := writer.mountedVolumeLabel()
	if expected == "" {
		return nil // 读不到 Windows 卷标：只剩引擎读回值这条不可靠的路，不做断言
	}
	if kept.volumeName != expected {
		return fmt.Errorf("追加区段未沿用盘上卷标: Windows 卷标=%q 镜像=%q", expected, kept.volumeName)
	}
	return nil
}
