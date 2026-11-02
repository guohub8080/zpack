// POC：go-astiav 静态链接 libav 的进程内转码管线（zpack media 功能的技术验证）。
// 改编自 go-astiav 官方 examples/transcoding（Apache-2.0，Copyright 2019-2024 asticode）。
// 管线：demux → decode → filtergraph(像素格式归一) → encode(H264/AAC) → mux
// 用法：transcode -i in.mp4 -o out.mp4 -b 2000000
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/asticode/go-astiav"
	"github.com/asticode/go-astikit"
)

var (
	inputPath     = flag.String("i", "", "输入文件")
	outputPath    = flag.String("o", "", "输出文件")
	videoBitrate  = flag.Int("b", 2_000_000, "视频目标码率 bps")
)

var (
	c                   = astikit.NewCloser()
	inputFormatContext  *astiav.FormatContext
	outputFormatContext *astiav.FormatContext
	streams             = make(map[int]*transcodeStream) // 按输入流索引
)

type transcodeStream struct {
	buffersinkContext *astiav.BuffersinkFilterContext
	buffersrcContext  *astiav.BuffersrcFilterContext
	decCodec          *astiav.Codec
	decCodecContext   *astiav.CodecContext
	decFrame          *astiav.Frame
	decLastPTS        *int64
	encCodec          *astiav.Codec
	encCodecContext   *astiav.CodecContext
	encPkt            *astiav.Packet
	filterFrame       *astiav.Frame
	filterGraph       *astiav.FilterGraph
	inputStream       *astiav.Stream
	outputStream      *astiav.Stream
}

func main() {
	flag.Parse()
	if *inputPath == "" || *outputPath == "" {
		log.Fatal("用法: transcode -i <输入> -o <输出> [-b 视频码率]")
	}
	defer c.Close()

	if err := openInputFile(); err != nil {
		log.Fatal(fmt.Errorf("打开输入失败: %w", err))
	}
	if err := openOutputFile(); err != nil {
		log.Fatal(fmt.Errorf("打开输出失败: %w", err))
	}
	if err := initFilters(); err != nil {
		log.Fatal(fmt.Errorf("初始化滤镜失败: %w", err))
	}

	pkt := astiav.AllocPacket()
	c.Add(pkt.Free)

	// 主循环：读包 → 解码 → 滤镜 → 编码 → 写包
	for {
		if stop := func() bool {
			if err := inputFormatContext.ReadFrame(pkt); err != nil {
				if !errors.Is(err, astiav.ErrEof) {
					log.Println("读包失败:", err)
				}
				return true
			}
			defer pkt.Unref()

			s, ok := streams[pkt.StreamIndex()]
			if !ok {
				return false
			}
			pkt.RescaleTs(s.inputStream.TimeBase(), s.decCodecContext.TimeBase())
			if err := s.decCodecContext.SendPacket(pkt); err != nil {
				log.Println("送包失败:", err)
				return true
			}
			for {
				if stop := func() bool {
					if err := s.decCodecContext.ReceiveFrame(s.decFrame); err != nil {
						if !errors.Is(err, astiav.ErrEof) && !errors.Is(err, astiav.ErrEagain) {
							log.Println("收帧失败:", err)
						}
						return true
					}
					defer s.decFrame.Unref()
					if s.decLastPTS != nil && *s.decLastPTS >= s.decFrame.Pts() {
						return false
					}
					s.decLastPTS = astikit.Int64Ptr(s.decFrame.Pts())
					if err := filterEncodeWriteFrame(s.decFrame, s); err != nil {
						log.Println("滤镜编码写帧失败:", err)
						return true
					}
					return false
				}(); stop {
					break
				}
			}
			return false
		}(); stop {
			break
		}
	}

	// 冲洗各流
	for _, s := range streams {
		if err := filterEncodeWriteFrame(nil, s); err != nil {
			log.Fatal("冲洗滤镜失败:", err)
		}
		if err := encodeWriteFrame(nil, s); err != nil {
			log.Fatal("冲洗编码器失败:", err)
		}
	}

	if err := outputFormatContext.WriteTrailer(); err != nil {
		log.Fatal("写尾部失败:", err)
	}
	log.Println("完成")
}

func openInputFile() (err error) {
	if inputFormatContext = astiav.AllocFormatContext(); inputFormatContext == nil {
		return errors.New("input format context is nil")
	}
	c.Add(inputFormatContext.Free)
	if err = inputFormatContext.OpenInput(*inputPath, nil, nil); err != nil {
		return err
	}
	c.Add(inputFormatContext.CloseInput)
	if err = inputFormatContext.FindStreamInfo(nil); err != nil {
		return err
	}

	for _, is := range inputFormatContext.Streams() {
		if is.CodecParameters().MediaType() != astiav.MediaTypeAudio &&
			is.CodecParameters().MediaType() != astiav.MediaTypeVideo {
			continue
		}
		s := &transcodeStream{inputStream: is}
		if s.decCodec = astiav.FindDecoder(is.CodecParameters().CodecID()); s.decCodec == nil {
			return errors.New("找不到解码器: " + is.CodecParameters().CodecID().Name())
		}
		if s.decCodecContext = astiav.AllocCodecContext(s.decCodec); s.decCodecContext == nil {
			return errors.New("decoder context is nil")
		}
		c.Add(s.decCodecContext.Free)
		if err = is.CodecParameters().ToCodecContext(s.decCodecContext); err != nil {
			return err
		}
		if is.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			s.decCodecContext.SetFramerate(inputFormatContext.GuessFrameRate(is, nil))
		}
		if err = s.decCodecContext.Open(s.decCodec, nil); err != nil {
			return err
		}
		s.decCodecContext.SetTimeBase(is.TimeBase())
		s.decFrame = astiav.AllocFrame()
		c.Add(s.decFrame.Free)
		streams[is.Index()] = s
	}
	return nil
}

func openOutputFile() (err error) {
	if outputFormatContext, err = astiav.AllocOutputFormatContext(nil, "", *outputPath); err != nil || outputFormatContext == nil {
		return errors.New("output format context 创建失败")
	}
	c.Add(outputFormatContext.Free)

	for _, is := range inputFormatContext.Streams() {
		s, ok := streams[is.Index()]
		if !ok {
			continue
		}
		if s.outputStream = outputFormatContext.NewStream(nil); s.outputStream == nil {
			return errors.New("output stream is nil")
		}

		// 目标编码：视频 H264（POC 用 libx264），音频 AAC
		var codecID astiav.CodecID
		if s.decCodecContext.MediaType() == astiav.MediaTypeAudio {
			codecID = astiav.CodecIDAac
		} else {
			codecID = astiav.CodecIDH264
		}
		if s.encCodec = astiav.FindEncoder(codecID); s.encCodec == nil {
			return errors.New("找不到编码器: " + codecID.Name())
		}
		if s.encCodecContext = astiav.AllocCodecContext(s.encCodec); s.encCodecContext == nil {
			return errors.New("encoder context is nil")
		}
		c.Add(s.encCodecContext.Free)

		if s.decCodecContext.MediaType() == astiav.MediaTypeAudio {
			if v := s.encCodec.SupportedChannelLayouts(); len(v) > 0 {
				s.encCodecContext.SetChannelLayout(v[0])
			} else {
				s.encCodecContext.SetChannelLayout(s.decCodecContext.ChannelLayout())
			}
			s.encCodecContext.SetSampleRate(s.decCodecContext.SampleRate())
			if v := s.encCodec.SupportedSampleFormats(); len(v) > 0 {
				s.encCodecContext.SetSampleFormat(v[0])
			} else {
				s.encCodecContext.SetSampleFormat(s.decCodecContext.SampleFormat())
			}
			s.encCodecContext.SetTimeBase(astiav.NewRational(1, s.encCodecContext.SampleRate()))
			s.encCodecContext.SetBitRate(128_000) // 音频 128kbps（压缩旋钮示例）
		} else {
			s.encCodecContext.SetHeight(s.decCodecContext.Height())
			if v := s.encCodec.SupportedPixelFormats(); len(v) > 0 {
				s.encCodecContext.SetPixelFormat(v[0])
			} else {
				s.encCodecContext.SetPixelFormat(s.decCodecContext.PixelFormat())
			}
			s.encCodecContext.SetSampleAspectRatio(s.decCodecContext.SampleAspectRatio())
			s.encCodecContext.SetTimeBase(s.decCodecContext.TimeBase())
			s.encCodecContext.SetWidth(s.decCodecContext.Width())
			s.encCodecContext.SetBitRate(int64(*videoBitrate)) // 视频目标码率（压缩功能的核心旋钮）
		}

		if outputFormatContext.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader) {
			s.encCodecContext.SetFlags(s.encCodecContext.Flags().Add(astiav.CodecContextFlagGlobalHeader))
		}
		if err = s.encCodecContext.Open(s.encCodec, nil); err != nil {
			return err
		}
		if err = s.outputStream.CodecParameters().FromCodecContext(s.encCodecContext); err != nil {
			return err
		}
		s.outputStream.SetTimeBase(s.encCodecContext.TimeBase())
	}

	if !outputFormatContext.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		var ioContext *astiav.IOContext
		if ioContext, err = astiav.OpenIOContext(*outputPath, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil); err != nil {
			return err
		}
		c.AddWithError(ioContext.Close)
		outputFormatContext.SetPb(ioContext)
	}
	if err = outputFormatContext.WriteHeader(nil); err != nil {
		return err
	}
	return nil
}

func initFilters() (err error) {
	for _, s := range streams {
		if s.filterGraph = astiav.AllocFilterGraph(); s.filterGraph == nil {
			return errors.New("graph is nil")
		}
		c.Add(s.filterGraph.Free)

		outputs := astiav.AllocFilterInOut()
		c.Add(outputs.Free)
		inputs := astiav.AllocFilterInOut()
		c.Add(inputs.Free)

		buffersrcContextParameters := astiav.AllocBuffersrcFilterContextParameters()
		defer buffersrcContextParameters.Free()

		var buffersrc, buffersink *astiav.Filter
		var content string
		switch s.decCodecContext.MediaType() {
		case astiav.MediaTypeAudio:
			buffersrc = astiav.FindFilterByName("abuffer")
			buffersrcContextParameters.SetChannelLayout(s.decCodecContext.ChannelLayout())
			buffersrcContextParameters.SetSampleFormat(s.decCodecContext.SampleFormat())
			buffersrcContextParameters.SetSampleRate(s.decCodecContext.SampleRate())
			buffersrcContextParameters.SetTimeBase(s.decCodecContext.TimeBase())
			buffersink = astiav.FindFilterByName("abuffersink")
			content = fmt.Sprintf("aformat=sample_fmts=%s:channel_layouts=%s",
				s.encCodecContext.SampleFormat().Name(), s.encCodecContext.ChannelLayout().String())
		default:
			buffersrc = astiav.FindFilterByName("buffer")
			buffersrcContextParameters.SetHeight(s.decCodecContext.Height())
			buffersrcContextParameters.SetPixelFormat(s.decCodecContext.PixelFormat())
			buffersrcContextParameters.SetSampleAspectRatio(s.decCodecContext.SampleAspectRatio())
			buffersrcContextParameters.SetTimeBase(s.inputStream.TimeBase())
			buffersrcContextParameters.SetWidth(s.decCodecContext.Width())
			buffersink = astiav.FindFilterByName("buffersink")
			content = fmt.Sprintf("format=pix_fmts=%s", s.encCodecContext.PixelFormat().Name())
		}
		if buffersrc == nil || buffersink == nil {
			return errors.New("滤镜找不到")
		}

		if s.buffersrcContext, err = s.filterGraph.NewBuffersrcFilterContext(buffersrc, "in"); err != nil {
			return err
		}
		if s.buffersinkContext, err = s.filterGraph.NewBuffersinkFilterContext(buffersink, "out"); err != nil {
			return err
		}
		if err = s.buffersrcContext.SetParameters(buffersrcContextParameters); err != nil {
			return err
		}
		if err = s.buffersrcContext.Initialize(nil); err != nil {
			return err
		}
		outputs.SetName("in")
		outputs.SetFilterContext(s.buffersrcContext.FilterContext())
		outputs.SetPadIdx(0)
		outputs.SetNext(nil)
		inputs.SetName("out")
		inputs.SetFilterContext(s.buffersinkContext.FilterContext())
		inputs.SetPadIdx(0)
		inputs.SetNext(nil)
		if err = s.filterGraph.Parse(content, inputs, outputs); err != nil {
			return err
		}
		if err = s.filterGraph.Configure(); err != nil {
			return err
		}

		s.filterFrame = astiav.AllocFrame()
		c.Add(s.filterFrame.Free)
		s.encPkt = astiav.AllocPacket()
		c.Add(s.encPkt.Free)
	}
	return nil
}

func filterEncodeWriteFrame(f *astiav.Frame, s *transcodeStream) (err error) {
	if err = s.buffersrcContext.AddFrame(f, astiav.NewBuffersrcFlags(astiav.BuffersrcFlagKeepRef)); err != nil {
		return err
	}
	for {
		if stop, err := func() (bool, error) {
			if err := s.buffersinkContext.GetFrame(s.filterFrame, astiav.NewBuffersinkFlags()); err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					return true, nil
				}
				return false, err
			}
			defer s.filterFrame.Unref()
			s.filterFrame.SetPictureType(astiav.PictureTypeNone)
			if err := encodeWriteFrame(s.filterFrame, s); err != nil {
				return false, err
			}
			return false, nil
		}(); err != nil {
			return err
		} else if stop {
			break
		}
	}
	return nil
}

func encodeWriteFrame(f *astiav.Frame, s *transcodeStream) (err error) {
	if err = s.encCodecContext.SendFrame(f); err != nil {
		return err
	}
	for {
		if stop, err := func() (bool, error) {
			if err := s.encCodecContext.ReceivePacket(s.encPkt); err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					return true, nil
				}
				return false, err
			}
			defer s.encPkt.Unref()
			s.encPkt.SetStreamIndex(s.outputStream.Index())
			s.encPkt.RescaleTs(s.encCodecContext.TimeBase(), s.outputStream.TimeBase())
			if err := outputFormatContext.WriteInterleavedFrame(s.encPkt); err != nil {
				return false, err
			}
			return false, nil
		}(); err != nil {
			return err
		} else if stop {
			break
		}
	}
	return nil
}
