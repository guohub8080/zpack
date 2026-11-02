//go:build cgo

// HDR 图片解码（astiav 通道——零新增依赖，完整版白送的解码能力；AVIF 走 heic.go 既有通道）：
// EXR（OpenEXR float）/ HDR（Radiance RGBE→float）——AVIF 归 HEIC 家族通道（image/heic.go）。
// 浮点格式走 Reinhard 滚降（真 HDR 才压，SDR 范围直通保 roundtrip 一致）；
// 整数格式 sws → RGBA64 → 8bit。JXL 待 ffmpeg 构建带 libjxl 再收编。
package image

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"os"
	"strings"

	"github.com/asticode/go-astiav"
)

// decodeHDRImage HDR 格式解码入口。handled=false = 非 HDR 格式（走标准库路径）。
func decodeHDRImage(path string) (image.Image, string, bool, error) {
	formatName, isHDR := hdrFormatByExt(path)
	if !isHDR {
		return nil, "", false, nil
	}
	frame, frameErr := decodeFirstFrame(path)
	if frameErr != nil {
		return nil, formatName, true, fmt.Errorf("无法读取 %s 图片: %w", formatName, frameErr)
	}
	defer frame.Unref()

	width, height := frame.Width(), frame.Height()
	if width <= 0 || height <= 0 {
		return nil, formatName, true, fmt.Errorf("帧尺寸异常 %dx%d", width, height)
	}

	rgba, convErr := frameToRGBA(frame)
	return rgba, formatName, true, convErr
}

// frameToRGBA astiav 帧 → Go RGBA（浮点族走 tonemap；整数族走 sws RGBA64）
func frameToRGBA(frame *astiav.Frame) (*image.RGBA, error) {
	width, height := frame.Width(), frame.Height()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("帧尺寸异常 %dx%d", width, height)
	}
	if isFloatPixelFormat(frame.PixelFormat()) {
		return floatFrameToRGBA(frame, width, height)
	}
	return intFrameToRGBA(frame, width, height)
}

// decodeFirstFrame astiav 解码首帧（图片=单帧视频流）
func decodeFirstFrame(path string) (*astiav.Frame, error) {
	inputCtx := astiav.AllocFormatContext()
	if inputCtx == nil {
		return nil, fmt.Errorf("input context 分配失败")
	}
	defer inputCtx.Free()
	if err := inputCtx.OpenInput(path, nil, nil); err != nil {
		return nil, err
	}
	defer inputCtx.CloseInput()
	if err := inputCtx.FindStreamInfo(nil); err != nil {
		return nil, err
	}
	var videoStream *astiav.Stream
	for _, st := range inputCtx.Streams() {
		if st.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = st
			break
		}
	}
	if videoStream == nil {
		return nil, fmt.Errorf("无视频流")
	}
	decoder := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decoder == nil {
		return nil, fmt.Errorf("解码器未找到: %s", videoStream.CodecParameters().CodecID().Name())
	}
	decCtx := astiav.AllocCodecContext(decoder)
	if decCtx == nil {
		return nil, fmt.Errorf("decoder ctx 分配失败")
	}
	defer decCtx.Free()
	if err := videoStream.CodecParameters().ToCodecContext(decCtx); err != nil {
		return nil, err
	}
	if err := decCtx.Open(decoder, nil); err != nil {
		return nil, fmt.Errorf("解码器打开失败: %w", err)
	}

	pkt := astiav.AllocPacket()
	defer pkt.Free()
	frame := astiav.AllocFrame()
	defer func() { frame.Free() }()
	drain := func() bool {
		for {
			if err := decCtx.ReceiveFrame(frame); err != nil {
				return false // EAGAIN（还要更多输入）或 EOF
			}
			return true
		}
	}
	// 收帧-读包循环：有的解码器（libjxl）首包不吐帧，需继续喂或 EOF 冲洗
	for {
		if drain() {
			return cloneFrameRef(frame), nil
		}
		if err := inputCtx.ReadFrame(pkt); err != nil {
			break // EOF → 冲洗
		}
		if pkt.StreamIndex() != videoStream.Index() {
			pkt.Unref()
			continue
		}
		pkt.RescaleTs(videoStream.TimeBase(), decCtx.TimeBase())
		if err := decCtx.SendPacket(pkt); err != nil {
			pkt.Unref()
			return nil, fmt.Errorf("送包失败: %w", err)
		}
		pkt.Unref()
	}
	if err := decCtx.SendPacket(nil); err == nil {
		if drain() {
			return cloneFrameRef(frame), nil
		}
	}
	return nil, fmt.Errorf("解码失败: 无帧产出")
}

// cloneFrameRef 帧引用克隆——内部复用帧 freed 后调用方仍持有效引用
func cloneFrameRef(src *astiav.Frame) *astiav.Frame {
	dst := astiav.AllocFrame()
	if err := dst.Ref(src); err != nil {
		return nil
	}
	return dst
}

// isFloatPixelFormat 是否浮点像素族（GBR(P)F32——EXR/Radiance 解码输出）
func isFloatPixelFormat(pf astiav.PixelFormat) bool {
	switch pf {
	case astiav.PixelFormatGbrpf32Le, astiav.PixelFormatGbrpf32Be,
		astiav.PixelFormatGbrapf32Le, astiav.PixelFormatGbrapf32Be:
		return true
	}
	return false
}

// floatFrameToRGBA 平面浮点（G/B/R[/A]）→ RGBA：先扫场景白点，真 HDR 走 Reinhard
func floatFrameToRGBA(frame *astiav.Frame, width, height int) (*image.RGBA, error) {
	planeBytes := width * height * 4
	planeCount := 4
	if frame.PixelFormat() == astiav.PixelFormatGbrpf32Le || frame.PixelFormat() == astiav.PixelFormatGbrpf32Be {
		planeCount = 3
	}
	bufferSize, sizeErr := frame.ImageBufferSize(1)
	if sizeErr != nil || bufferSize < planeBytes*planeCount {
		return nil, fmt.Errorf("帧缓冲异常: size=%d 需要=%d err=%w", bufferSize, planeBytes*planeCount, sizeErr)
	}
	buffer := make([]byte, bufferSize)
	if _, err := frame.ImageCopyToBuffer(buffer, 1); err != nil {
		return nil, err
	}
	bigEndian := frame.PixelFormat() == astiav.PixelFormatGbrpf32Be || frame.PixelFormat() == astiav.PixelFormatGbrapf32Be

	// 平面浮点值（G=0 B=1 R=2 A=3）
	readFloat := func(plane, offset int) float32 {
		raw := buffer[plane*planeBytes+offset : plane*planeBytes+offset+4]
		bits := binary.LittleEndian.Uint32(raw)
		if bigEndian {
			bits = bitswap32(binary.BigEndian.Uint32(raw))
		}
		return math.Float32frombits(bits)
	}

	// 场景白点（首遍扫——确定性：max 全图）
	sceneMax := float32(0)
	for pixelIdx := 0; pixelIdx < width*height; pixelIdx++ {
		for planeIdx := 0; planeIdx < planeCount; planeIdx++ {
			if v := readFloat(planeIdx, pixelIdx*4); planeIdx < 3 && v > sceneMax {
				sceneMax = v
			}
		}
	}
	tonemapped := NeedsTonemap(sceneMax)

	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixelIdx := y*width + x
			channels := [4]float32{
				readFloat(2, pixelIdx*4), // R
				readFloat(0, pixelIdx*4), // G
				readFloat(1, pixelIdx*4), // B
			}
			for chanIdx := 0; chanIdx < 3; chanIdx++ {
				if tonemapped {
					channels[chanIdx] = TonemapReinhard(channels[chanIdx], sceneMax)
				}
			}
			var alpha uint8 = 255
			if planeCount == 4 {
				alpha = FloatToByte(readFloat(3, pixelIdx*4)) // alpha 不参与 tonemap
			}
			offset := rgba.PixOffset(x, y)
			rgba.Pix[offset] = FloatToByte(channels[0])
			rgba.Pix[offset+1] = FloatToByte(channels[1])
			rgba.Pix[offset+2] = FloatToByte(channels[2])
			rgba.Pix[offset+3] = alpha
		}
	}
	return rgba, nil
}

// intFrameToRGBA 整数像素族 → sws RGBA64LE → 8bit（AVIF 10bit 等）
func intFrameToRGBA(frame *astiav.Frame, width, height int) (*image.RGBA, error) {
	dstFrame := astiav.AllocFrame()
	defer dstFrame.Free()
	dstFrame.SetWidth(width)
	dstFrame.SetHeight(height)
	dstFrame.SetPixelFormat(astiav.PixelFormatRgba64Le)
	if err := dstFrame.AllocBuffer(0); err != nil {
		return nil, err
	}
	ssc, err := astiav.CreateSoftwareScaleContext(
		width, height, frame.PixelFormat(),
		width, height, astiav.PixelFormatRgba64Le,
		astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBicubic), // 对齐 ffmpeg CLI 缺省——色度边缘插值一致
	)
	if err != nil {
		return nil, err
	}
	defer ssc.Free()
	if err := ssc.ScaleFrame(frame, dstFrame); err != nil {
		return nil, err
	}
	bufferSize, sizeErr := dstFrame.ImageBufferSize(1)
	if sizeErr != nil || bufferSize <= 0 {
		return nil, fmt.Errorf("sws 输出缓冲异常: size=%d err=%w", bufferSize, sizeErr)
	}
	buffer := make([]byte, bufferSize)
	if _, err := dstFrame.ImageCopyToBuffer(buffer, 1); err != nil {
		return nil, err
	}

	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcOffset := (y*width + x) * 8 // RGBA64 = 8 字节/像素
			offset := rgba.PixOffset(x, y)
			rgba.Pix[offset] = buffer[srcOffset+1] // 16LE 低字节≈高 8 位（v>>8）
			rgba.Pix[offset+1] = buffer[srcOffset+3]
			rgba.Pix[offset+2] = buffer[srcOffset+5]
			rgba.Pix[offset+3] = buffer[srcOffset+7]
		}
	}
	return rgba, nil
}

// probeHDRInfo HDR 格式的元信息（宽高/像素格式名）
func probeHDRInfo(path string) (*ImageInfo, bool, error) {
	formatName, isHDR := hdrFormatByExt(path)
	if !isHDR {
		return nil, false, nil
	}
	inputCtx := astiav.AllocFormatContext()
	if inputCtx == nil {
		return nil, true, fmt.Errorf("input context 分配失败")
	}
	defer inputCtx.Free()
	if err := inputCtx.OpenInput(path, nil, nil); err != nil {
		return nil, true, fmt.Errorf("无法读取 %s 图片: %w", formatName, err)
	}
	defer inputCtx.CloseInput()
	if err := inputCtx.FindStreamInfo(nil); err != nil {
		return nil, true, err
	}
	for _, st := range inputCtx.Streams() {
		if st.CodecParameters().MediaType() != astiav.MediaTypeVideo {
			continue
		}
		info, _ := os.Stat(path)
		return &ImageInfo{
			Input:      absPath(path),
			Format:     formatName,
			Width:      st.CodecParameters().Width(),
			Height:     st.CodecParameters().Height(),
			SizeBytes:  info.Size(),
			ColorModel: st.CodecParameters().CodecID().Name(),
		}, true, nil
	}
	return nil, true, fmt.Errorf("无视频流")
}

// bitswap32 字节序反转（BE float → LE bits）
func bitswap32(v uint32) uint32 {
	return v<<24 | (v&0xFF00)<<8 | (v>>8)&0xFF00 | v>>24
}

// hdrFormatByExt 扩展名 → HDR 格式名（false = 非 HDR 路径）
func hdrFormatByExt(path string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extOf(path)), "."))
	switch ext {
	case "exr":
		return "exr", true
	case "hdr", "rgbe":
		return "hdr", true
	case "jxl":
		return "jxl", true
	}
	return "", false
}

// extOf path 扩展名（含点）
func extOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' || path[i] == '\\' {
			break
		}
	}
	return ""
}
