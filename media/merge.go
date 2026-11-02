//go:build cgo

// 视频拼接：多个视频合并为一个，以首个视频为基准（分辨率/编码/帧率）。
// 其他视频自动缩放+重编码匹配基准。全程流式（逐个输入处理→同一编码会话→零临时文件）。
package media

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/asticode/go-astiav"
)

// Merge 拼接多个视频：以第一个为基准（分辨率/编码参数），其余缩放+重编码匹配
func Merge(inputs []string, output, referencePath string) (*MergeResult, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("至少需要 2 个视频")
	}
	// 基准视频：缺省 = 第一个；--reference 指定 = 按路径匹配
	reference := inputs[0]
	if referencePath != "" {
		found := false
		for _, inputPath := range inputs {
			if inputPath == referencePath {
				reference = referencePath
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("--reference %s 不在输入列表中", referencePath)
		}
	}

	// 探测基准
	refProbe, probeErr := probeFile(reference)
	if probeErr != nil {
		return nil, fmt.Errorf("读取基准视频失败: %w", probeErr)
	}
	if refProbe.Video == nil {
		return nil, fmt.Errorf("基准视频无视频流")
	}
	refWidth := refProbe.Video.Width
	refHeight := refProbe.Video.Height
	refCodec := refProbe.Video.CodecName

	// 打开基准获取编码器参数
	refCtx := astiav.AllocFormatContext()
	if refCtx == nil {
		return nil, fmt.Errorf("reference context 分配失败")
	}
	defer refCtx.Free()
	if err := refCtx.OpenInput(reference, nil, nil); err != nil {
		return nil, fmt.Errorf("打开基准视频失败: %w", err)
	}
	defer refCtx.CloseInput()
	if err := refCtx.FindStreamInfo(nil); err != nil {
		return nil, err
	}

	var refVideo, refAudio *astiav.Stream
	for _, st := range refCtx.Streams() {
		switch st.CodecParameters().MediaType() {
		case astiav.MediaTypeVideo:
			refVideo = st
		case astiav.MediaTypeAudio:
			refAudio = st
		}
	}
	if refVideo == nil {
		return nil, fmt.Errorf("基准无视频流")
	}

	// 输出
	outputCtx, _ := astiav.AllocOutputFormatContext(nil, "", output)
	if outputCtx == nil {
		return nil, fmt.Errorf("输出格式创建失败")
	}
	defer outputCtx.Free()

	// 编码器（基准参数）
	videoCodecID := refVideo.CodecParameters().CodecID()
	encoder := astiav.FindEncoder(videoCodecID)
	if encoder == nil {
		return nil, fmt.Errorf("编码器未找到: %s", videoCodecID.Name())
	}
	encCtx := astiav.AllocCodecContext(encoder)
	if encCtx == nil {
		return nil, fmt.Errorf("encoder ctx 分配失败")
	}
	defer encCtx.Free()
	encCtx.SetWidth(refWidth)
	encCtx.SetHeight(refHeight)
	encCtx.SetTimeBase(refVideo.TimeBase())
	if fmts := encoder.SupportedPixelFormats(); len(fmts) > 0 {
		encCtx.SetPixelFormat(fmts[0])
	} else {
		encCtx.SetPixelFormat(astiav.PixelFormatYuv420P)
	}
	encCtx.SetBitRate(refVideo.CodecParameters().BitRate())
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

	// 音频只从基准取（其他视频的音频忽略——简化）
	outAudioIdx := -1
	if refAudio != nil {
		outAudio := outputCtx.NewStream(nil)
		if err := refAudio.CodecParameters().Copy(outAudio.CodecParameters()); err != nil {
			return nil, err
		}
		outAudio.SetTimeBase(refAudio.TimeBase())
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

	// PTS 序号
	fps := refProbe.Video.Framerate
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

	// 逐个视频处理：open → decode → filter(scale+format) → encode → close
	for inputIdx, inputPath := range inputs {
		isReference := inputPath == reference

		inCtx := astiav.AllocFormatContext()
		if inCtx == nil {
			return nil, fmt.Errorf("input %d context 分配失败", inputIdx+1)
		}

		if err := inCtx.OpenInput(inputPath, nil, nil); err != nil {
			inCtx.Free()
			return nil, fmt.Errorf("打开视频 %d 失败: %w", inputIdx+1, err)
		}

		if err := inCtx.FindStreamInfo(nil); err != nil {
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("读流信息 %d 失败: %w", inputIdx+1, err)
		}

		var inVideo *astiav.Stream
		var inAudio *astiav.Stream
		for _, st := range inCtx.Streams() {
			switch st.CodecParameters().MediaType() {
			case astiav.MediaTypeVideo:
				inVideo = st
			case astiav.MediaTypeAudio:
				inAudio = st
			}
		}
		if inVideo == nil {
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("视频 %d 无视频流", inputIdx+1)
		}

		// 解码器
		decoder := astiav.FindDecoder(inVideo.CodecParameters().CodecID())
		if decoder == nil {
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("视频 %d 解码器未找到", inputIdx+1)
		}
		decCtx := astiav.AllocCodecContext(decoder)
		if decCtx == nil {
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("decoder ctx %d 分配失败", inputIdx+1)
		}
		if err := inVideo.CodecParameters().ToCodecContext(decCtx); err != nil {
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, err
		}
		if err := decCtx.Open(decoder, nil); err != nil {
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("打开解码器 %d 失败: %w", inputIdx+1, err)
		}

		// 滤镜：基准 = 格式归一；其他 = 缩放到基准尺寸 + 格式归一（拉伸不保比例）
		filterGraph := astiav.AllocFilterGraph()
		if filterGraph == nil {
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("filter graph %d 分配失败", inputIdx+1)
		}

		bufSrc := astiav.FindFilterByName("buffer")
		bufSink := astiav.FindFilterByName("buffersink")

		srcCtx, srcErr := filterGraph.NewBuffersrcFilterContext(bufSrc, "in")
		if srcErr != nil {
			filterGraph.Free()
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, srcErr
		}
		sinkCtx, sinkErr := filterGraph.NewBuffersinkFilterContext(bufSink, "out")
		if sinkErr != nil {
			filterGraph.Free()
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, sinkErr
		}

		srcParams := astiav.AllocBuffersrcFilterContextParameters()
		srcParams.SetWidth(decCtx.Width())
		srcParams.SetHeight(decCtx.Height())
		srcParams.SetPixelFormat(decCtx.PixelFormat())
		srcParams.SetTimeBase(inVideo.TimeBase())
		if err := srcCtx.SetParameters(srcParams); err != nil {
			srcParams.Free()
			filterGraph.Free()
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, err
		}
		if err := srcCtx.Initialize(nil); err != nil {
			srcParams.Free()
			filterGraph.Free()
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, err
		}
		srcParams.Free()

		filterInputs := astiav.AllocFilterInOut()
		filterOutputs := astiav.AllocFilterInOut()
		filterOutputs.SetName("in")
		filterOutputs.SetFilterContext(srcCtx.FilterContext())
		filterOutputs.SetPadIdx(0)
		filterOutputs.SetNext(nil)
		filterInputs.SetName("out")
		filterInputs.SetFilterContext(sinkCtx.FilterContext())
		filterInputs.SetPadIdx(0)
		filterInputs.SetNext(nil)

		// 基准视频：只做格式归一；其他视频：缩放到基准尺寸（拉伸）+ 格式归一
		filterContent := fmt.Sprintf("format=pix_fmts=%s", encCtx.PixelFormat().Name())
		if !isReference {
			filterContent = fmt.Sprintf("scale=%d:%d,format=pix_fmts=%s",
				refWidth, refHeight, encCtx.PixelFormat().Name())
		}
		if err := filterGraph.Parse(filterContent, filterInputs, filterOutputs); err != nil {
			filterInputs.Free()
			filterOutputs.Free()
			filterGraph.Free()
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("滤镜 %d 解析失败: %w", inputIdx+1, err)
		}
		if err := filterGraph.Configure(); err != nil {
			filterInputs.Free()
			filterOutputs.Free()
			filterGraph.Free()
			decCtx.Free()
			inCtx.CloseInput()
			inCtx.Free()
			return nil, fmt.Errorf("滤镜 %d 配置失败: %w", inputIdx+1, err)
		}
		filterInputs.Free()
		filterOutputs.Free()

		// 处理帧
		filteredFrame := astiav.AllocFrame()
		pkt := astiav.AllocPacket()
		decFrame := astiav.AllocFrame()

		processErr := func() error {
			for {
				if err := inCtx.ReadFrame(pkt); err != nil {
					break
				}
				if pkt.StreamIndex() == inVideo.Index() {
					pkt.RescaleTs(inVideo.TimeBase(), decCtx.TimeBase())
					if err := decCtx.SendPacket(pkt); err != nil {
						pkt.Unref()
						continue
					}
					pkt.Unref()
					for {
						if err := decCtx.ReceiveFrame(decFrame); err != nil {
							break
						}
						// 推入滤镜
						if err := srcCtx.AddFrame(decFrame, astiav.NewBuffersrcFlags(astiav.BuffersrcFlagKeepRef)); err != nil {
							decFrame.Unref()
							return fmt.Errorf("滤镜推送失败: %w", err)
						}
						// 从滤镜取出 → 编码
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
						decFrame.Unref()
					}
				} else if isReference && inAudio != nil && inAudio.Index() == pkt.StreamIndex() && outAudioIdx >= 0 {
					// 音频只从基准 remux
					pkt.SetStreamIndex(outAudioIdx)
					if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
						pkt.Unref()
						return err
					}
				}
				pkt.Unref()
			}
			return nil
		}()

		// 清理本输入
		decFrame.Free()
		pkt.Free()
		filteredFrame.Free()
		filterGraph.Free()
		decCtx.Free()
		inCtx.CloseInput()
		inCtx.Free()

		if processErr != nil {
			return nil, fmt.Errorf("视频 %d 处理失败: %w", inputIdx+1, processErr)
		}
	}

	// 冲洗 + 收尾
	sendFrame(nil)
	if err := outputCtx.WriteTrailer(); err != nil {
		return nil, fmt.Errorf("写尾失败: %w", err)
	}

	outputInfo, _ := os.Stat(output)
	absOutput, _ := filepath.Abs(output)
	absInputs := make([]string, len(inputs))
	for i, p := range inputs {
		absInputs[i], _ = filepath.Abs(p)
	}
	absRef, _ := filepath.Abs(reference)
	return &MergeResult{
		Reference: absRef, Inputs: absInputs, Output: absOutput,
		Width: refWidth, Height: refHeight, Codec: refCodec,
		Count: len(inputs), SizeBytes: outputInfo.Size(),
	}, nil
}
