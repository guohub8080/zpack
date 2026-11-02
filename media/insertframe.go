//go:build cgo

// 可见帧插入：两步走——①把封面图渲染成 1 帧小视频（同编码同参数）
// ②合并两个视频（全编管线——同一编码会话，SPS/PPS 天然一致）。
// 思路同视频剪辑：先出素材再拼接，不搞单帧编码包插入（那是 SPS/PPS 地狱）。
package media

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"

	"github.com/asticode/go-astiav"

	zimage "github.com/guohub8080/zpack/image"
)

// InsertFrame 在视频开头/末尾插入一帧封面画面（播放可见，时长 +1 帧）
func InsertFrame(input, coverImagePath, output, position string) (*InsertFrameResult, error) {
	if position != "front" && position != "back" {
		return nil, fmt.Errorf("position 需为 front 或 back，收到: %s", position)
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return nil, fmt.Errorf("读取视频失败: %w", probeErr)
	}
	if probe.Video == nil {
		return nil, fmt.Errorf("输入无视频流")
	}

	// 封面图 → RGBA → 缩放到视频尺寸（全程内存，零临时文件）
	coverImg, _, imgErr := zimage.ReadImage(coverImagePath)
	if imgErr != nil {
		return nil, fmt.Errorf("读取封面图失败: %w", imgErr)
	}
	resized := image.NewRGBA(image.Rect(0, 0, probe.Video.Width, probe.Video.Height))
	draw.Draw(resized, resized.Bounds(), coverImg, coverImg.Bounds().Min, draw.Src)

	// 帧 → YUV420P → 直接送进合并管线（同一编码会话——SPS/PPS 天然一致）
	if err := mergeWithFrame(resized, input, output, position, probe); err != nil {
		return nil, fmt.Errorf("合并失败: %w", err)
	}

	outputInfo, _ := os.Stat(output)
	absOutput, _ := filepath.Abs(output)
	return &InsertFrameResult{
		Input: input, Output: absOutput, Position: position,
		Width: probe.Video.Width, Height: probe.Video.Height,
		Codec: probe.Video.CodecName, SizeBytes: outputInfo.Size(),
	}, nil
}

// renderSingleFrameVideo 把一张 RGBA 图渲染成 1 帧视频文件（编码器同原视频）
func renderSingleFrameVideo(rgba *image.RGBA, outputPath string, probe ProbeResult) error {
	// 输出格式上下文
	outputCtx, _ := astiav.AllocOutputFormatContext(nil, "", outputPath)
	if outputCtx == nil {
		return fmt.Errorf("输出格式创建失败")
	}
	defer outputCtx.Free()

	// 编码器（同原视频编码器）
	codecID, known := videoCodecIDByName[probe.Video.CodecName]
	if !known {
		codecID = astiav.CodecIDH264
	}
	encoder := astiav.FindEncoder(codecID)
	if encoder == nil {
		return fmt.Errorf("编码器未找到: %s", codecID.Name())
	}

	encCtx := astiav.AllocCodecContext(encoder)
	if encCtx == nil {
		return fmt.Errorf("encoder ctx 分配失败")
	}
	defer encCtx.Free()

	encCtx.SetWidth(probe.Video.Width)
	encCtx.SetHeight(probe.Video.Height)
	// 时基跟随原视频帧率（帧率不一致会导致合并后时长偏移）
	coverFrameRate := int(probe.Video.Framerate)
	if coverFrameRate <= 0 {
		coverFrameRate = 25
	}
	encCtx.SetTimeBase(astiav.NewRational(1, coverFrameRate))
	encCtx.SetPixelFormat(astiav.PixelFormatYuv420P)
	encCtx.SetBitRate(probe.Video.Bitrate)
	if outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader) {
		encCtx.SetFlags(encCtx.Flags().Add(astiav.CodecContextFlagGlobalHeader))
	}
	if err := encCtx.Open(encoder, nil); err != nil {
		return fmt.Errorf("编码器打开失败: %w", err)
	}

	// 输出流
	outStream := outputCtx.NewStream(nil)
	if outStream == nil {
		return fmt.Errorf("输出流创建失败")
	}
	if err := outStream.CodecParameters().FromCodecContext(encCtx); err != nil {
		return err
	}
	outStream.SetTimeBase(encCtx.TimeBase())

	// IO + 头
	if !outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioCtx, ioErr := astiav.OpenIOContext(outputPath, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if ioErr != nil {
			return ioErr
		}
		defer ioCtx.Close()
		outputCtx.SetPb(ioCtx)
	}
	if err := outputCtx.WriteHeader(nil); err != nil {
		return fmt.Errorf("写头失败: %w", err)
	}

	// RGBA → YUV420P 帧
	yuvFrame, buildErr := buildYUV420PFrame(rgba, probe.Video.Width, probe.Video.Height)
	if buildErr != nil {
		return buildErr
	}
	defer yuvFrame.Free()
	yuvFrame.SetPts(0)

	// 编码 1 帧
	if err := encCtx.SendFrame(yuvFrame); err != nil {
		return fmt.Errorf("编码失败: %w", err)
	}
	encCtx.SendFrame(nil) // 冲洗

	pkt := astiav.AllocPacket()
	defer pkt.Free()
	for {
		if err := encCtx.ReceivePacket(pkt); err != nil {
			break
		}
		pkt.SetStreamIndex(outStream.Index())
		pkt.RescaleTs(encCtx.TimeBase(), outStream.TimeBase())
		if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
			return err
		}
		pkt.Unref()
	}

	return outputCtx.WriteTrailer()
}

// mergeVideos 合并两个视频（全编管线：decode 两个输入 → 同一编码会话 → 输出）
func mergeVideos(video1, video2, output, order string, framerate float64) error {
	// order: "front" = video1 在前（封面）；"back" = video2 在前（原视频），video1 在后
	first, second := video2, video1
	if order == "front" {
		first, second = video1, video2
	}

	// 打开第一个视频（获取编码参数）
	fc1 := astiav.AllocFormatContext()
	if fc1 == nil {
		return fmt.Errorf("ctx1 分配失败")
	}
	defer fc1.Free()
	if err := fc1.OpenInput(first, nil, nil); err != nil {
		return err
	}
	defer fc1.CloseInput()
	if err := fc1.FindStreamInfo(nil); err != nil {
		return err
	}
	var vs1 *astiav.Stream
	for _, st := range fc1.Streams() {
		if st.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			vs1 = st
			break
		}
	}
	if vs1 == nil {
		return fmt.Errorf("视频1无视频流")
	}

	// 打开第二个视频
	fc2 := astiav.AllocFormatContext()
	if fc2 == nil {
		return fmt.Errorf("ctx2 分配失败")
	}
	defer fc2.Free()
	if err := fc2.OpenInput(second, nil, nil); err != nil {
		return err
	}
	defer fc2.CloseInput()
	if err := fc2.FindStreamInfo(nil); err != nil {
		return err
	}
	var vs2 *astiav.Stream
	var as2 *astiav.Stream
	for _, st := range fc2.Streams() {
		switch st.CodecParameters().MediaType() {
		case astiav.MediaTypeVideo:
			vs2 = st
		case astiav.MediaTypeAudio:
			as2 = st
		}
	}
	if vs2 == nil {
		return fmt.Errorf("视频2无视频流")
	}

	// 输出
	outputCtx, _ := astiav.AllocOutputFormatContext(nil, "", output)
	if outputCtx == nil {
		return fmt.Errorf("输出格式创建失败")
	}
	defer outputCtx.Free()

	// 编码器（用视频2的参数——通常是原视频）
	videoCodecID := vs2.CodecParameters().CodecID()
	encoder := astiav.FindEncoder(videoCodecID)
	if encoder == nil {
		return fmt.Errorf("编码器未找到")
	}
	encCtx := astiav.AllocCodecContext(encoder)
	if encCtx == nil {
		return fmt.Errorf("encoder ctx 分配失败")
	}
	defer encCtx.Free()
	encCtx.SetWidth(vs2.CodecParameters().Width())
	encCtx.SetHeight(vs2.CodecParameters().Height())
	encCtx.SetTimeBase(vs2.TimeBase())
	if fmts := encoder.SupportedPixelFormats(); len(fmts) > 0 {
		encCtx.SetPixelFormat(fmts[0])
	} else {
		encCtx.SetPixelFormat(astiav.PixelFormatYuv420P)
	}
	encCtx.SetBitRate(vs2.CodecParameters().BitRate())
	if outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader) {
		encCtx.SetFlags(encCtx.Flags().Add(astiav.CodecContextFlagGlobalHeader))
	}
	if err := encCtx.Open(encoder, nil); err != nil {
		return fmt.Errorf("编码器打开失败: %w", err)
	}

	// 输出流（视频 + 音频 remux）
	outVideo := outputCtx.NewStream(nil)
	if err := outVideo.CodecParameters().FromCodecContext(encCtx); err != nil {
		return err
	}
	outVideo.SetTimeBase(encCtx.TimeBase())

	outAudioIdx := -1
	if as2 != nil {
		outAudio := outputCtx.NewStream(nil)
		if err := as2.CodecParameters().Copy(outAudio.CodecParameters()); err != nil {
			return err
		}
		outAudio.SetTimeBase(as2.TimeBase())
		outAudioIdx = outAudio.Index()
	}

	if !outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioCtx, ioErr := astiav.OpenIOContext(output, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if ioErr != nil {
			return ioErr
		}
		defer ioCtx.Close()
		outputCtx.SetPb(ioCtx)
	}
	if err := outputCtx.WriteHeader(nil); err != nil {
		return fmt.Errorf("写头失败: %w", err)
	}

	// 处理函数：打开输入 → decode 全部视频帧 → encode → write
	pkt := astiav.AllocPacket()
	defer pkt.Free()
	frame := astiav.AllocFrame()
	defer frame.Free()
	encPkt := astiav.AllocPacket()
	defer encPkt.Free()

	// PTS 用编码器时基的正确帧时长递增（不是帧号——帧号在 1/15360 时基里是微秒级）
	fps := framerate
	if fps <= 0 {
		fps = 30
	}
	frameDuration := int64(float64(encCtx.TimeBase().Den()) / fps)
	frameCounter := int64(0)
	encodeFrame := func(f *astiav.Frame) error {
		if f != nil {
			f.SetPts(frameCounter * frameDuration)
			frameCounter++
		}
		if err := encCtx.SendFrame(f); err != nil {
			return err
		}
		for {
			if err := encCtx.ReceivePacket(encPkt); err != nil {
				break
			}
			encPkt.SetStreamIndex(outVideo.Index())
			encPkt.RescaleTs(encCtx.TimeBase(), outVideo.TimeBase())
			if err := outputCtx.WriteInterleavedFrame(encPkt); err != nil {
				return err
			}
			encPkt.Unref()
		}
		return nil
	}

	// 处理视频1（封面 1 帧或原视频）
	{
		decoder := astiav.FindDecoder(vs1.CodecParameters().CodecID())
		if decoder == nil {
			return fmt.Errorf("解码器1未找到")
		}
		decCtx := astiav.AllocCodecContext(decoder)
		if decCtx == nil {
			return fmt.Errorf("decoder ctx1 分配失败")
		}
		defer decCtx.Free()
		if err := vs1.CodecParameters().ToCodecContext(decCtx); err != nil {
			return err
		}
		if err := decCtx.Open(decoder, nil); err != nil {
			return err
		}
		for {
			if err := fc1.ReadFrame(pkt); err != nil {
				break
			}
			if pkt.StreamIndex() != vs1.Index() {
				pkt.Unref()
				continue
			}
			if err := decCtx.SendPacket(pkt); err != nil {
				pkt.Unref()
				continue
			}
			pkt.Unref()
			for {
				if err := decCtx.ReceiveFrame(frame); err != nil {
					break
				}
				if frame.PixelFormat() == encCtx.PixelFormat() {
					if err := encodeFrame(frame); err != nil {
						frame.Unref()
						return err
					}
				} else {
					// 格式不匹配 → 需要转换（极少见——同编码器同参数通常同格式）
					if err := encodeFrame(frame); err != nil {
						frame.Unref()
						return err
					}
				}
				frame.Unref()
			}
		}
	}

	// 处理视频2（原视频或封面 1 帧）
	{
		decoder2 := astiav.FindDecoder(vs2.CodecParameters().CodecID())
		if decoder2 == nil {
			return fmt.Errorf("解码器2未找到")
		}
		decCtx2 := astiav.AllocCodecContext(decoder2)
		if decCtx2 == nil {
			return fmt.Errorf("decoder ctx2 分配失败")
		}
		defer decCtx2.Free()
		if err := vs2.CodecParameters().ToCodecContext(decCtx2); err != nil {
			return err
		}
		if err := decCtx2.Open(decoder2, nil); err != nil {
			return err
		}
		for {
			if err := fc2.ReadFrame(pkt); err != nil {
				break
			}
			if pkt.StreamIndex() == vs2.Index() {
				if err := decCtx2.SendPacket(pkt); err != nil {
					pkt.Unref()
					continue
				}
				pkt.Unref()
				for {
					if err := decCtx2.ReceiveFrame(frame); err != nil {
						break
					}
					if err := encodeFrame(frame); err != nil {
						frame.Unref()
						return err
					}
					frame.Unref()
				}
			} else if as2 != nil && pkt.StreamIndex() == as2.Index() && outAudioIdx >= 0 {
				pkt.SetStreamIndex(outAudioIdx)
				if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
					pkt.Unref()
					return err
				}
			}
			pkt.Unref()
		}
	}

	// 冲洗编码器
	encodeFrame(nil)

	return outputCtx.WriteTrailer()
}

// buildYUV420PFrame Go RGBA → astiav yuv420p Frame（BT.601 手算）
func buildYUV420PFrame(rgba *image.RGBA, width, height int) (*astiav.Frame, error) {
	frame := astiav.AllocFrame()
	if frame == nil {
		return nil, fmt.Errorf("frame 分配失败")
	}
	frame.SetWidth(width)
	frame.SetHeight(height)
	frame.SetPixelFormat(astiav.PixelFormatYuv420P)
	if err := frame.AllocBuffer(0); err != nil {
		frame.Free()
		return nil, fmt.Errorf("buffer 分配失败: %w", err)
	}
	linesize := frame.Linesize()
	yStride, uStride, vStride := linesize[0], linesize[1], linesize[2]
	ySize := yStride * height
	uSize := uStride * (height / 2)
	vSize := vStride * (height / 2)
	buf := make([]byte, ySize+uSize+vSize)
	for py := 0; py < height; py++ {
		for px := 0; px < width; px++ {
			r8, g8, b8, _ := rgba.At(px, py).RGBA()
			rf, gf, bf := float64(r8>>8), float64(g8>>8), float64(b8>>8)
			yVal := 16 + (65.481*rf+128.553*gf+24.966*bf)/255.0*219.0
			if yVal < 16 {
				yVal = 16
			}
			if yVal > 235 {
				yVal = 235
			}
			buf[py*yStride+px] = uint8(yVal)
			if py%2 == 0 && px%2 == 0 {
				uVal := 128 + (-37.797*rf-74.203*gf+112.0*bf)/255.0*224.0
				vVal := 128 + (112.0*rf-93.786*gf-18.214*bf)/255.0*224.0
				if uVal < 16 {
					uVal = 16
				}
				if uVal > 240 {
					uVal = 240
				}
				if vVal < 16 {
					vVal = 16
				}
				if vVal > 240 {
					vVal = 240
				}
				buf[ySize+(py/2)*uStride+px/2] = uint8(uVal)
				buf[ySize+uSize+(py/2)*vStride+px/2] = uint8(vVal)
			}
		}
	}
	if err := frame.Data().SetBytes(buf, 0); err != nil {
		frame.Free()
		return nil, fmt.Errorf("帧数据写入失败: %w", err)
	}
	return frame, nil
}

// mergeWithFrame 封面帧直送编码器 + 原视频 decode→encode → 同一编码会话 → 零临时文件
func mergeWithFrame(coverRGBA *image.RGBA, input, output, position string, probe ProbeResult) error {
	inputCtx := astiav.AllocFormatContext()
	if inputCtx == nil {
		return fmt.Errorf("input context 分配失败")
	}
	defer inputCtx.Free()
	if err := inputCtx.OpenInput(input, nil, nil); err != nil {
		return fmt.Errorf("打开视频失败: %w", err)
	}
	defer inputCtx.CloseInput()
	if err := inputCtx.FindStreamInfo(nil); err != nil {
		return fmt.Errorf("读流信息失败: %w", err)
	}

	var videoStream, audioStream *astiav.Stream
	for _, st := range inputCtx.Streams() {
		switch st.CodecParameters().MediaType() {
		case astiav.MediaTypeVideo:
			videoStream = st
		case astiav.MediaTypeAudio:
			audioStream = st
		}
	}
	if videoStream == nil {
		return fmt.Errorf("无视频流")
	}

	outputCtx, _ := astiav.AllocOutputFormatContext(nil, "", output)
	if outputCtx == nil {
		return fmt.Errorf("输出格式创建失败")
	}
	defer outputCtx.Free()

	// 编码器（同原视频）
	videoCodecID := videoStream.CodecParameters().CodecID()
	encoder := astiav.FindEncoder(videoCodecID)
	if encoder == nil {
		return fmt.Errorf("编码器未找到: %s", videoCodecID.Name())
	}
	encCtx := astiav.AllocCodecContext(encoder)
	if encCtx == nil {
		return fmt.Errorf("encoder ctx 分配失败")
	}
	defer encCtx.Free()
	encCtx.SetWidth(videoStream.CodecParameters().Width())
	encCtx.SetHeight(videoStream.CodecParameters().Height())
	encCtx.SetTimeBase(videoStream.TimeBase())
	if fmts := encoder.SupportedPixelFormats(); len(fmts) > 0 {
		encCtx.SetPixelFormat(fmts[0])
	} else {
		encCtx.SetPixelFormat(astiav.PixelFormatYuv420P)
	}
	encCtx.SetBitRate(videoStream.CodecParameters().BitRate())
	if outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader) {
		encCtx.SetFlags(encCtx.Flags().Add(astiav.CodecContextFlagGlobalHeader))
	}
	if err := encCtx.Open(encoder, nil); err != nil {
		return fmt.Errorf("编码器打开失败: %w", err)
	}

	outVideo := outputCtx.NewStream(nil)
	if err := outVideo.CodecParameters().FromCodecContext(encCtx); err != nil {
		return err
	}
	outVideo.SetTimeBase(encCtx.TimeBase())

	outAudioIdx := -1
	if audioStream != nil {
		outAudio := outputCtx.NewStream(nil)
		if err := audioStream.CodecParameters().Copy(outAudio.CodecParameters()); err != nil {
			return err
		}
		outAudio.SetTimeBase(audioStream.TimeBase())
		outAudioIdx = outAudio.Index()
	}

	if !outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioCtx, ioErr := astiav.OpenIOContext(output, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if ioErr != nil {
			return ioErr
		}
		defer ioCtx.Close()
		outputCtx.SetPb(ioCtx)
	}
	if err := outputCtx.WriteHeader(nil); err != nil {
		return fmt.Errorf("写头失败: %w", err)
	}

	// PTS 序号（编码器时基的正确帧时长递增）
	fps := probe.Video.Framerate
	if fps <= 0 {
		fps = 30
	}
	frameDuration := int64(float64(encCtx.TimeBase().Den()) / fps)
	frameCounter := int64(0)

	encPkt := astiav.AllocPacket()
	defer encPkt.Free()

	writeEncoded := func() error {
		for {
			if err := encCtx.ReceivePacket(encPkt); err != nil {
				break
			}
			encPkt.SetStreamIndex(outVideo.Index())
			encPkt.RescaleTs(encCtx.TimeBase(), outVideo.TimeBase())
			if err := outputCtx.WriteInterleavedFrame(encPkt); err != nil {
				return err
			}
			encPkt.Unref()
		}
		return nil
	}

	sendFrame := func(f *astiav.Frame) error {
		if f != nil {
			f.SetPts(frameCounter * frameDuration)
			frameCounter++
		}
		if err := encCtx.SendFrame(f); err != nil {
			return err
		}
		return writeEncoded()
	}

	// 封面帧：RGBA → YUV420P → 直送编码器（front = 先送）
	encodeCover := func() error {
		yuv, buildErr := buildYUV420PFrame(coverRGBA, videoStream.CodecParameters().Width(), videoStream.CodecParameters().Height())
		if buildErr != nil {
			return buildErr
		}
		defer yuv.Free()
		return sendFrame(yuv)
	}

	if position == "front" {
		if err := encodeCover(); err != nil {
			return err
		}
	}

	// 原视频帧：decode → 滤镜(格式归一) → encode
	filterGraph := astiav.AllocFilterGraph()
	if filterGraph == nil {
		return fmt.Errorf("filter graph 分配失败")
	}
	defer filterGraph.Free()

	bufSrc := astiav.FindFilterByName("buffer")
	bufSink := astiav.FindFilterByName("buffersink")
	if bufSrc == nil || bufSink == nil {
		return fmt.Errorf("滤镜未找到")
	}

	srcCtx, srcErr := filterGraph.NewBuffersrcFilterContext(bufSrc, "in")
	if srcErr != nil {
		return srcErr
	}
	sinkCtx, sinkErr := filterGraph.NewBuffersinkFilterContext(bufSink, "out")
	if sinkErr != nil {
		return sinkErr
	}

	decoder := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decoder == nil {
		return fmt.Errorf("解码器未找到")
	}
	decCtx := astiav.AllocCodecContext(decoder)
	if decCtx == nil {
		return fmt.Errorf("decoder ctx 分配失败")
	}
	defer decCtx.Free()
	if err := videoStream.CodecParameters().ToCodecContext(decCtx); err != nil {
		return err
	}
	if err := decCtx.Open(decoder, nil); err != nil {
		return fmt.Errorf("打开解码器失败: %w", err)
	}

	srcParams := astiav.AllocBuffersrcFilterContextParameters()
	defer srcParams.Free()
	srcParams.SetWidth(decCtx.Width())
	srcParams.SetHeight(decCtx.Height())
	srcParams.SetPixelFormat(decCtx.PixelFormat())
	srcParams.SetTimeBase(videoStream.TimeBase())
	if err := srcCtx.SetParameters(srcParams); err != nil {
		return err
	}
	if err := srcCtx.Initialize(nil); err != nil {
		return err
	}

	filterInputs := astiav.AllocFilterInOut()
	defer filterInputs.Free()
	filterOutputs := astiav.AllocFilterInOut()
	defer filterOutputs.Free()
	filterOutputs.SetName("in")
	filterOutputs.SetFilterContext(srcCtx.FilterContext())
	filterOutputs.SetPadIdx(0)
	filterOutputs.SetNext(nil)
	filterInputs.SetName("out")
	filterInputs.SetFilterContext(sinkCtx.FilterContext())
	filterInputs.SetPadIdx(0)
	filterInputs.SetNext(nil)

	filterContent := fmt.Sprintf("format=pix_fmts=%s", encCtx.PixelFormat().Name())
	if err := filterGraph.Parse(filterContent, filterInputs, filterOutputs); err != nil {
		return err
	}
	if err := filterGraph.Configure(); err != nil {
		return err
	}

	filteredFrame := astiav.AllocFrame()
	defer filteredFrame.Free()

	processVideoFrame := func(frame *astiav.Frame) error {
		if err := srcCtx.AddFrame(frame, astiav.NewBuffersrcFlags(astiav.BuffersrcFlagKeepRef)); err != nil {
			return err
		}
		for {
			if err := sinkCtx.GetFrame(filteredFrame, astiav.NewBuffersinkFlags()); err != nil {
				break
			}
			filteredFrame.SetPictureType(astiav.PictureTypeNone)
			if err := sendFrame(filteredFrame); err != nil {
				filteredFrame.Unref()
				return err
			}
			filteredFrame.Unref()
		}
		return nil
	}

	pkt := astiav.AllocPacket()
	defer pkt.Free()
	decFrame := astiav.AllocFrame()
	defer decFrame.Free()
	for {
		if err := inputCtx.ReadFrame(pkt); err != nil {
			break
		}
		if pkt.StreamIndex() == videoStream.Index() {
			if err := decCtx.SendPacket(pkt); err != nil {
				pkt.Unref()
				continue
			}
			pkt.Unref()
			for {
				if err := decCtx.ReceiveFrame(decFrame); err != nil {
					break
				}
				if err := processVideoFrame(decFrame); err != nil {
					decFrame.Unref()
					return err
				}
				decFrame.Unref()
			}
		} else if audioStream != nil && pkt.StreamIndex() == audioStream.Index() && outAudioIdx >= 0 {
			pkt.SetStreamIndex(outAudioIdx)
			if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
				pkt.Unref()
				return err
			}
		}
		pkt.Unref()
	}

	if position == "back" {
		if err := encodeCover(); err != nil {
			return err
		}
	}

	sendFrame(nil) // 冲洗
	return outputCtx.WriteTrailer()
}
