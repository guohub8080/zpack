//go:build cgo

// 速度渲染：调整视频播放速度（1.5 = 快放 1.5 倍，0.8 = 慢放 0.8 倍）。
// 原理：帧间距 × (1/factor)——快放时帧更密，慢放时帧更疏。全编管线保证均匀 SPS/PPS。
package media

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/asticode/go-astiav"
)

// Speed 调整播放速度（factor > 1 加速，< 1 减速）
func Speed(input, output string, factor float64, pitchMode string) (*SpeedResult, error) {
	if factor <= 0 || factor > 10 {
		return nil, fmt.Errorf("--factor 需 0.1-10，收到 %f", factor)
	}
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return nil, fmt.Errorf("读取视频失败: %w", probeErr)
	}
	if probe.Video == nil {
		return nil, fmt.Errorf("无视频流")
	}

	inputCtx := astiav.AllocFormatContext()
	if inputCtx == nil {
		return nil, fmt.Errorf("input context 分配失败")
	}
	defer inputCtx.Free()
	if err := inputCtx.OpenInput(input, nil, nil); err != nil {
		return nil, err
	}
	defer inputCtx.CloseInput()
	if err := inputCtx.FindStreamInfo(nil); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("无视频流")
	}

	outputCtx, _ := astiav.AllocOutputFormatContext(nil, "", output)
	if outputCtx == nil {
		return nil, fmt.Errorf("输出格式创建失败")
	}
	defer outputCtx.Free()

	// 视频编码器（同原视频）
	videoCodecID := videoStream.CodecParameters().CodecID()
	encoder := astiav.FindEncoder(videoCodecID)
	if encoder == nil {
		return nil, fmt.Errorf("编码器未找到")
	}
	encCtx := astiav.AllocCodecContext(encoder)
	if encCtx == nil {
		return nil, fmt.Errorf("encoder ctx 分配失败")
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
		return nil, fmt.Errorf("编码器打开失败: %w", err)
	}

	outVideo := outputCtx.NewStream(nil)
	if err := outVideo.CodecParameters().FromCodecContext(encCtx); err != nil {
		return nil, err
	}
	outVideo.SetTimeBase(encCtx.TimeBase())

	// 音频流 remux（时戳同步缩放——不改变采样数据，速度通过时戳压缩实现）
	outAudioIdx := -1
	if audioStream != nil {
		outAudio := outputCtx.NewStream(nil)
		if err := audioStream.CodecParameters().Copy(outAudio.CodecParameters()); err != nil {
			return nil, err
		}
		outAudio.SetTimeBase(audioStream.TimeBase())
		outAudioIdx = outAudio.Index()
	}

	if !outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioCtx, ioErr := astiav.OpenIOContext(output, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if ioErr != nil {
			return nil, ioErr
		}
		defer ioCtx.Close()
		outputCtx.SetPb(ioCtx)
	}
	if err := outputCtx.WriteHeader(nil); err != nil {
		return nil, fmt.Errorf("写头失败: %w", err)
	}

	// PTS 步长 × (1/factor)——快放更密，慢放更疏
	fps := probe.Video.Framerate
	if fps <= 0 {
		fps = 30
	}
	normalDuration := int64(float64(encCtx.TimeBase().Den()) / fps)
	speedDuration := int64(float64(normalDuration) / factor) // 帧间距缩放
	frameCounter := int64(0)

	// 视频解码器 + 滤镜
	decoder := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decoder == nil {
		return nil, fmt.Errorf("解码器未找到")
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
		return nil, err
	}

	filterGraph := astiav.AllocFilterGraph()
	if filterGraph == nil {
		return nil, fmt.Errorf("filter graph 分配失败")
	}
	defer filterGraph.Free()

	bufSrc := astiav.FindFilterByName("buffer")
	bufSink := astiav.FindFilterByName("buffersink")
	srcCtx, _ := filterGraph.NewBuffersrcFilterContext(bufSrc, "in")
	sinkCtx, _ := filterGraph.NewBuffersinkFilterContext(bufSink, "out")

	srcParams := astiav.AllocBuffersrcFilterContextParameters()
	defer srcParams.Free()
	srcParams.SetWidth(decCtx.Width())
	srcParams.SetHeight(decCtx.Height())
	srcParams.SetPixelFormat(decCtx.PixelFormat())
	srcParams.SetTimeBase(videoStream.TimeBase())
	if err := srcCtx.SetParameters(srcParams); err != nil {
		return nil, err
	}
	if err := srcCtx.Initialize(nil); err != nil {
		return nil, err
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
		return nil, err
	}
	if err := filterGraph.Configure(); err != nil {
		return nil, err
	}

	// 编码写入
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
			f.SetPts(frameCounter * speedDuration) // ← 速度的魔法就在这里
			frameCounter++
		}
		if err := encCtx.SendFrame(f); err != nil {
			return err
		}
		return writeEncoded()
	}

	// 主循环
	filteredFrame := astiav.AllocFrame()
	defer filteredFrame.Free()
	pkt := astiav.AllocPacket()
	defer pkt.Free()
	decFrame := astiav.AllocFrame()
	defer decFrame.Free()

	for {
		if err := inputCtx.ReadFrame(pkt); err != nil {
			break
		}
		if pkt.StreamIndex() == videoStream.Index() {
			pkt.RescaleTs(videoStream.TimeBase(), decCtx.TimeBase())
			if err := decCtx.SendPacket(pkt); err != nil {
				pkt.Unref()
				continue
			}
			pkt.Unref()
			for {
				if err := decCtx.ReceiveFrame(decFrame); err != nil {
					break
				}
				if err := srcCtx.AddFrame(decFrame, astiav.NewBuffersrcFlags(astiav.BuffersrcFlagKeepRef)); err != nil {
					decFrame.Unref()
					return nil, err
				}
				for {
					if err := sinkCtx.GetFrame(filteredFrame, astiav.NewBuffersinkFlags()); err != nil {
						break
					}
					filteredFrame.SetPictureType(astiav.PictureTypeNone)
					if err := sendFrame(filteredFrame); err != nil {
						filteredFrame.Unref()
						return nil, err
					}
					filteredFrame.Unref()
				}
				decFrame.Unref()
			}
		} else if audioStream != nil && pkt.StreamIndex() == audioStream.Index() && outAudioIdx >= 0 {
			// 音频丢弃（MVP——atempo/asetrate 管线后续接入；旧时戳缩放方案音画脱同步已移除）
			_ = outAudioIdx
		}
		pkt.Unref()
	}

	sendFrame(nil) // 冲洗
	if err := outputCtx.WriteTrailer(); err != nil {
		return nil, err
	}

	outputInfo, _ := os.Stat(output)
	absOutput, _ := filepath.Abs(output)
	normalDur := probe.DurationSeconds
	return &SpeedResult{
		Input: input, Output: absOutput, Factor: factor,
		NormalDuration: normalDur,
		FastDuration:   normalDur / factor,
		Width:          probe.Video.Width, Height: probe.Video.Height,
		Codec: probe.Video.CodecName, SizeBytes: outputInfo.Size(),
	}, nil
}
