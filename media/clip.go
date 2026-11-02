//go:build cgo

// 视频截取：从时间段 A→B 提取为新 clip。
// 缺省流拷贝（stream copy——零重编、近即时、I/O 级速度）；--exact 全编帧精确。
package media

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/asticode/go-astiav"
)

// Clip 截取视频段（ startTime → endTime ）
// exact=false → 流拷贝（关键帧对齐、近即时）；exact=true → 全编（帧精确、较慢）
func Clip(input, output string, startTime, endTime float64) (*ClipResult, error) {
	if startTime < 0 || endTime <= startTime {
		return nil, fmt.Errorf("时间需 0 ≤ start < end，收到 start=%.2f end=%.2f", startTime, endTime)
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return nil, fmt.Errorf("读取视频失败: %w", probeErr)
	}
	if endTime > probe.DurationSeconds {
		endTime = probe.DurationSeconds // 截到末尾
	}
	mode := "exact" // 恒精确——用户指明时间/帧号就是要精确的（流拷贝是内部优化，不是用户选项）

	execErr := clipExact(input, output, startTime, endTime)
	if execErr != nil {
		return nil, execErr
	}
	outputInfo, _ := os.Stat(output)
	absOutput, _ := filepath.Abs(output)
	return &ClipResult{
		Input: input, Output: absOutput,
		StartTime: startTime, EndTime: endTime,
		Duration: endTime - startTime,
		Mode:     mode, SizeBytes: outputInfo.Size(),
	}, nil
}

// clipStreamCopy 流拷贝截取：seek 到关键帧 → 拷贝包到 endTime（零重编、近即时）
func clipStreamCopy(input, output string, startTime, endTime float64) error {
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

	outputCtx, _ := astiav.AllocOutputFormatContext(nil, "", output)
	if outputCtx == nil {
		return fmt.Errorf("输出格式创建失败")
	}
	defer outputCtx.Free()

	// 拷贝全部流（视频+音频+字幕）
	streamMap := map[int]int{}
	for _, inStream := range inputCtx.Streams() {
		outStream := outputCtx.NewStream(nil)
		if outStream == nil {
			return fmt.Errorf("输出流创建失败")
		}
		if err := inStream.CodecParameters().Copy(outStream.CodecParameters()); err != nil {
			return err
		}
		outStream.CodecParameters().SetCodecTag(0)
		outStream.SetTimeBase(inStream.TimeBase())
		streamMap[inStream.Index()] = outStream.Index()
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

	// Seek 到 startTime 之前最近的关键帧
	// 用视频流的时间基换算秒 → 流时间戳
	var videoStream *astiav.Stream
	for _, st := range inputCtx.Streams() {
		if st.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = st
			break
		}
	}
	seekTarget := int64(0)
	if videoStream != nil && startTime > 0 {
		tb := videoStream.TimeBase()
		seekTarget = int64(startTime / tb.Float64())
		if err := inputCtx.SeekFrame(videoStream.Index(), seekTarget, astiav.NewSeekFlags(astiav.SeekFlagBackward)); err != nil {
			// seek 失败 → 从头读（短视频 seek 到 0 可能失败）
			_ = err
		}
	}

	// 计算时间戳偏移（让 clip 从 PTS≈0 开始）
	// 用 AV_TIME_BASE（微秒）做跨流比较
	endTimeUs := int64(endTime * 1_000_000)

	pkt := astiav.AllocPacket()
	defer pkt.Free()
	for {
		if err := inputCtx.ReadFrame(pkt); err != nil {
			break // EOF
		}
		outIdx, mapped := streamMap[pkt.StreamIndex()]
		if !mapped {
			pkt.Unref()
			continue
		}
		// 包时间 → 微秒（跨流统一比较）
		inStream := inputCtx.Streams()[pkt.StreamIndex()]
		pktUs := int64(float64(pkt.Pts()) * inStream.TimeBase().Float64() * 1_000_000)

		// 超过 endTime → 停
		if pktUs > endTimeUs {
			pkt.Unref()
			break
		}
		// 原样拷贝（不改时戳——MP4 muxer 通过 edit list 处理初始偏移）
		// 手动偏移 PTS/DTS 会破坏 B 帧重排的 DTS 单调性和跨流交织
		pkt.SetStreamIndex(outIdx)
		if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
			pkt.Unref()
			return fmt.Errorf("写包失败: %w", err)
		}
		pkt.Unref()
	}

	return outputCtx.WriteTrailer()
}

// clipExact 全编截取：decode 从精确帧 → encode → 帧精确
func clipExact(input, output string, startTime, endTime float64) error {
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

	// 视频解码器+编码器
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

	videoCodecID := videoStream.CodecParameters().CodecID()
	encoder := astiav.FindEncoder(videoCodecID)
	if encoder == nil {
		return fmt.Errorf("编码器未找到")
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
		return fmt.Errorf("打开编码器失败: %w", err)
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

	// Seek
	if startTime > 0 && videoStream != nil {
		seekTarget := int64(startTime / videoStream.TimeBase().Float64())
		if err := inputCtx.SeekFrame(videoStream.Index(), seekTarget, astiav.NewSeekFlags(astiav.SeekFlagBackward)); err != nil {
			_ = err
		}
	}

	// 时间过滤（微秒）
	startUs := int64(startTime * 1_000_000)
	endUs := int64(endTime * 1_000_000)

	pkt := astiav.AllocPacket()
	defer pkt.Free()
	decFrame := astiav.AllocFrame()
	defer decFrame.Free()
	encPkt := astiav.AllocPacket()
	defer encPkt.Free()

	frameCounter := int64(0)
	frameDuration := int64(float64(encCtx.TimeBase().Den()) / 30.0) // 假设 30fps

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

	for {
		if err := inputCtx.ReadFrame(pkt); err != nil {
			break
		}
		if pkt.StreamIndex() == videoStream.Index() {
			// 帧时间检查（微秒）
			pktUs := int64(float64(pkt.Pts()) * videoStream.TimeBase().Float64() * 1_000_000)
			if pktUs < startUs {
				pkt.Unref()
				continue // 在 start 之前 → 跳过（seek 可能到更早的关键帧）
			}
			if pktUs >= endUs {
				pkt.Unref()
				break // 超过 end → 停
			}
			if err := decCtx.SendPacket(pkt); err != nil {
				pkt.Unref()
				continue
			}
			pkt.Unref()
			for {
				if err := decCtx.ReceiveFrame(decFrame); err != nil {
					break
				}
				if err := encodeFrame(decFrame); err != nil {
					decFrame.Unref()
					return err
				}
				decFrame.Unref()
			}
		} else if audioStream != nil && pkt.StreamIndex() == audioStream.Index() && outAudioIdx >= 0 {
			pktUs := int64(float64(pkt.Pts()) * audioStream.TimeBase().Float64() * 1_000_000)
			if pktUs >= startUs && pktUs <= endUs {
				pkt.SetStreamIndex(outAudioIdx)
				if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
					pkt.Unref()
					return err
				}
			}
		}
		pkt.Unref()
	}

	encodeFrame(nil) // 冲洗
	return outputCtx.WriteTrailer()
}
