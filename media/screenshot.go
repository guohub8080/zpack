//go:build cgo

// 视频截图：从指定时间点提取帧 → 保存为图片（jpg/png/webp）。
// 管线：astiav open → seek → decode 单帧 → FrameData.GuessImageFormat() → image 包编码。
package media

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strings"

	"github.com/asticode/go-astiav"

	zimage "github.com/guohub8080/zpack/image"
)

// Screenshot 单帧截图：seek 到 timestamp → decode → 保存
func Screenshot(input string, timestamp float64, output string) (*ScreenshotResult, error) {
	img, width, height, err := decodeFrameAt(input, timestamp)
	if err != nil {
		return nil, err
	}
	outputExt := strings.TrimPrefix(filepath.Ext(output), ".")
	if outputExt == "" {
		outputExt = "jpg"
		output = output + ".jpg"
	}
	if err := zimage.WriteImage(output, img, outputExt, zimage.IMAGE_DEFAULT_QUALITY); err != nil {
		return nil, fmt.Errorf("写入截图失败: %w", err)
	}
	outputInfo, _ := os.Stat(output)
	absOutput, _ := filepath.Abs(output)
	return &ScreenshotResult{
		Input: input, Output: absOutput, Timestamp: timestamp,
		Width: width, Height: height, SizeBytes: outputInfo.Size(),
	}, nil
}

// ScreenshotBatch 多帧截图：从时间点列表提取
func ScreenshotBatch(input string, timestamps []float64, outputDir string, format string) (*ScreenshotBatchResult, error) {
	if outputDir == "" {
		outputDir = filepath.Dir(input)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}
	if format == "" {
		format = "jpg"
	}
	baseName := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	result := &ScreenshotBatchResult{
		Input: input, OutputDir: outputDir, Frames: make([]ScreenshotResult, 0, len(timestamps)),
	}
	// 序号宽度自适应：个位数 _1、两位 _01、三位 _001（与总数位数对齐——排序天然正确）
	digitWidth := len(fmt.Sprintf("%d", len(timestamps)))
	for frameIndex, timestamp := range timestamps {
		output := filepath.Join(outputDir, fmt.Sprintf("%s_%0*d.%s", baseName, digitWidth, frameIndex+1, format))
		frameResult, err := Screenshot(input, timestamp, output)
		if err != nil {
			continue // 单帧失败跳过（seek 到末尾等场景）
		}
		result.Frames = append(result.Frames, *frameResult)
	}
	result.Count = len(result.Frames)
	return result, nil
}

// decodeFrameAt 打开视频 → seek → 解码一帧 → 转 Go image.Image
func decodeFrameAt(input string, timestamp float64) (image.Image, int, int, error) {
	formatContext := astiav.AllocFormatContext()
	if formatContext == nil {
		return nil, 0, 0, fmt.Errorf("format context 分配失败")
	}
	defer formatContext.Free()

	if err := formatContext.OpenInput(input, nil, nil); err != nil {
		return nil, 0, 0, fmt.Errorf("打开视频失败: %w", err)
	}
	defer formatContext.CloseInput()
	if err := formatContext.FindStreamInfo(nil); err != nil {
		return nil, 0, 0, fmt.Errorf("读流信息失败: %w", err)
	}

	// 找视频流
	var videoStream *astiav.Stream
	for _, stream := range formatContext.Streams() {
		if stream.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = stream
			break
		}
	}
	if videoStream == nil {
		return nil, 0, 0, fmt.Errorf("无视频流")
	}

	// 打开解码器
	decoder := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decoder == nil {
		return nil, 0, 0, fmt.Errorf("找不到解码器")
	}
	decCtx := astiav.AllocCodecContext(decoder)
	if decCtx == nil {
		return nil, 0, 0, fmt.Errorf("decoder context 分配失败")
	}
	defer decCtx.Free()
	if err := videoStream.CodecParameters().ToCodecContext(decCtx); err != nil {
		return nil, 0, 0, err
	}
	if err := decCtx.Open(decoder, nil); err != nil {
		return nil, 0, 0, fmt.Errorf("打开解码器失败: %w", err)
	}

	// Seek 到时间点（流时基换算：秒 → 流时间戳）
	if timestamp > 0 {
		streamTimeBase := videoStream.TimeBase()
		targetTs := int64(timestamp / streamTimeBase.Float64())
		if err := formatContext.SeekFrame(videoStream.Index(), targetTs, astiav.NewSeekFlags(astiav.SeekFlagBackward)); err != nil {
			// seek 失败（短视频/末尾）→ 从头读
			_ = err
		}
		// seek 后不重开解码器——读帧时解码器会自动处理（残留帧在 packets 中自然跳过）
	}

	// 读帧直到解码成功
	pkt := astiav.AllocPacket()
	defer pkt.Free()
	frame := astiav.AllocFrame()
	defer frame.Free()

	decoded := false
	for !decoded {
		if err := formatContext.ReadFrame(pkt); err != nil {
			return nil, 0, 0, fmt.Errorf("读取帧失败（时间 %.2fs 可能超出时长）: %w", timestamp, err)
		}
		if pkt.StreamIndex() != videoStream.Index() {
			pkt.Unref()
			continue
		}
		if err := decCtx.SendPacket(pkt); err != nil {
			pkt.Unref()
			continue
		}
		pkt.Unref()
		if err := decCtx.ReceiveFrame(frame); err == nil {
			decoded = true
		}
	}

	// astiav FrameData → Go image.Image → 强制 RGBA（PNG 等编码器对 bounds 敏感）
	src, err := frame.Data().GuessImageFormat()
	width, height := frame.Width(), frame.Height()
	if err != nil || width <= 0 || height <= 0 {
		return nil, 0, 0, fmt.Errorf("帧解码失败: %w", err)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	if src != nil {
		draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	}
	return rgba, width, height, nil
}
