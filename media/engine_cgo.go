//go:build cgo

// 引擎层（cgo + go-astiav）：探测 + 统一执行器。
// 一个执行器伺候三种模式：remux=全拷贝、audio-only=拷视频+编音频、full=全编。
// 管线骨架改编自 go-astiav 官方 transcoding 示例（Apache-2.0）与 build_cli/poc 的 POC 验证版。
package media

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/asticode/go-astiav"
	"github.com/asticode/go-astikit"
)

// probeFile 探测输入：流清单/时长/旋转/大小（决策矩阵的输入）
func probeFile(input string) (ProbeResult, error) {
	info, statErr := os.Stat(input)
	if statErr != nil {
		return ProbeResult{}, statErr
	}
	result := ProbeResult{SizeBytes: info.Size()}

	formatContext := astiav.AllocFormatContext()
	if formatContext == nil {
		return ProbeResult{}, errors.New("format context 分配失败")
	}
	defer formatContext.Free()
	if err := formatContext.OpenInput(input, nil, nil); err != nil {
		return ProbeResult{}, fmt.Errorf("打开输入失败: %w", err)
	}
	defer formatContext.CloseInput()
	if err := formatContext.FindStreamInfo(nil); err != nil {
		return ProbeResult{}, fmt.Errorf("读流信息失败: %w", err)
	}

	// Duration() 返回 AV_TIME_BASE 微秒
	result.DurationSeconds = float64(formatContext.Duration()) / 1_000_000
	if inputFormat := formatContext.InputFormat(); inputFormat != nil {
		result.FormatName = inputFormat.Name()
	}

	for _, stream := range formatContext.Streams() {
		parameters := stream.CodecParameters()
		kind := ""
		switch parameters.MediaType() {
		case astiav.MediaTypeVideo:
			kind = "video"
		case astiav.MediaTypeAudio:
			kind = "audio"
		case astiav.MediaTypeSubtitle:
			kind = "subtitle" // 字幕轨保轨决策（subtitleCompat）；丢弃必告警不静默
		default:
			continue
		}
		entry := StreamProbe{
			Index:     stream.Index(),
			Kind:      kind,
			CodecName: parameters.CodecID().Name(),
			Bitrate:   parameters.BitRate(),
			Width:     parameters.Width(),
			Height:    parameters.Height(),
		}
		if kind == "video" {
			if entry.Bitrate <= 0 {
				// 部分容器流级码率缺失 → 用整档位估（时长够长时近似）
				entry.Bitrate = formatContext.BitRate()
			}
			if frameRate := stream.AvgFrameRate().Float64(); frameRate > 0 {
				entry.Framerate = frameRate
			}
			if sar := parameters.SampleAspectRatio(); sar.Num() > 0 && sar.Den() > 0 {
				entry.SarNum, entry.SarDen = sar.Num(), sar.Den()
			}
			// 可烤元数据 #3 色彩范围/空间 + #5 VFR
			switch parameters.ColorRange() {
			case astiav.ColorRangeMpeg:
				entry.ColorRangeName = "limited"
			case astiav.ColorRangeJpeg:
				entry.ColorRangeName = "full"
			}
			entry.ColorSpaceName = parameters.ColorSpace().String()
			entry.TransferName = transferName(parameters.ColorTransferCharacteristic())
			if rRate := stream.RFrameRate().Float64(); rRate > 0 && entry.Framerate > 0 {
				entry.RFrameRate = rRate
				entry.IsVFR = rRate > entry.Framerate*1.01 || rRate < entry.Framerate*0.99
			}
			// 旋转探测：①display-matrix 侧数据（2016+ 手机标准）②旧式 metadata rotate 标签
			if matrix, hasMatrix := parameters.SideData().DisplayMatrix().Get(); hasMatrix {
				degrees := int(math.Round(matrix.Rotation()))
				if degrees != 0 {
					result.Rotation = degrees
				}
			} else if dict := stream.Metadata(); dict != nil {
				if rotateEntry := dict.Get("rotate", nil, astiav.NewDictionaryFlags()); rotateEntry != nil {
					if degrees, parseErr := strconv.Atoi(strings.TrimSpace(rotateEntry.Value())); parseErr == nil {
						result.Rotation = degrees
					}
				}
			}
			if result.Video == nil {
				result.Video = &entry
			}
		} else if result.Audio == nil {
			entry.SampleRate = parameters.SampleRate()
			if layout := parameters.ChannelLayout(); layout.Channels() > 0 {
				entry.Channels = layout.Channels()
			}
			result.Audio = &entry
		}
		result.Streams = append(result.Streams, entry)
	}
	return result, nil
}

// per-stream 执行动作
const (
	actionCopy   = "copy"
	actionEncode = "encode"
	actionDrop   = "drop"
)

// streamPlan 单流计划
type streamPlan struct {
	probe       StreamProbe
	action      string
	targetKind  astiav.CodecID // encode 时的目标编码
	bitrate     int64          // encode 码率
	scaleWidth  int            // >0 = 缩放目标（预设/指令，显示坐标系）
	scaleHeight int
	rotation    int    // ≠0 = 重编时烤入旋转（transpose 无损像素重排）
	videoFilter string // 显式前置滤镜（变换命令；优先于 rotation 的自动 transpose）
	swapDims    bool   // 显式滤镜交换宽高
	mono        bool   // 单声道目标
	sampleRate  int    // 采样率目标（0 = 跟随源）
}

// planStreams 按模式生成分流计划
func planStreams(mode Mode, probe ProbeResult, params execParams, targetExt string) []streamPlan {
	plans := make([]streamPlan, 0, 2)
	targetContainer := ContainerForExt(targetExt)

	videoTargetCodec := astiav.CodecIDH264
	if targetContainer == "webm" {
		videoTargetCodec = astiav.CodecIDVp9
	}
	// --codec 指令覆盖容器默认（ValidateCodec 已保证容器兼容与拼写）
	if params.videoCodec != "" {
		if codecID, known := videoCodecIDByName[params.videoCodec]; known {
			videoTargetCodec = codecID
		}
	}
	audioTargetCodec := astiav.CodecIDAac
	if targetContainer == "webm" {
		audioTargetCodec = astiav.CodecIDOpus
	}
	// 音频容器输出：目标编码由容器定
	if targetName, isAudioOut := audioOnlyTargets[targetExt]; isAudioOut {
		if id, known := audioCodecIDByName[targetName]; known {
			audioTargetCodec = id
		}
	}

	for _, stream := range probe.Streams {
		plan := streamPlan{probe: stream, targetKind: astiav.CodecIDNone, bitrate: 0}

		// 字幕轨：容器兼容则保轨拷贝，否则丢弃（executeFile 侧告警）；音频容器输出必丢
		if stream.Kind == "subtitle" {
			if _, isAudioOut := audioOnlyTargets[targetExt]; !isAudioOut {
				if compat := subtitleCompat[targetContainer]; compat == nil || compat[stream.CodecName] {
					plan.action = actionCopy
					plans = append(plans, plan)
					continue
				}
			}
			plan.action = actionDrop
			plans = append(plans, plan)
			continue
		}
		switch mode {
		case ModeRemux:
			plan.action = actionCopy
		case ModeAudioTrack:
			if stream.Kind == "video" {
				plan.action = actionDrop
			} else if stream.CodecName == audioOnlyTargets[targetExt] && params.audioBitrate <= 0 &&
				!params.audioMono && params.audioSampleRate <= 0 {
				plan.action = actionCopy // 同编码直拷；指定质量旗标 → 强制重编码（压缩/调声道语义）
			} else {
				plan.action = actionEncode
				plan.targetKind = audioTargetCodec
				plan.bitrate = params.audioBitrate
				plan.mono, plan.sampleRate = params.audioMono, params.audioSampleRate
			}
		case ModeAudioOnly:
			if stream.Kind == "video" {
				plan.action = actionCopy
			} else {
				plan.action = actionEncode
				plan.targetKind = audioTargetCodec
				if params.audioBitrate > 0 {
					plan.bitrate = params.audioBitrate
				} else if stream.Bitrate > 0 {
					plan.bitrate = min(stream.Bitrate, COMPRESS_AUDIO_CEILING)
				}
				plan.mono, plan.sampleRate = params.audioMono, params.audioSampleRate
			}
		case ModeFull:
			if stream.Kind == "video" {
				plan.action = actionEncode
				plan.targetKind = videoTargetCodec
				plan.bitrate = params.videoBitrate
				if plan.bitrate <= 0 && stream.Bitrate > 0 {
					plan.bitrate = stream.Bitrate // 跟随源（autoMask bit3 语义）
				}
			} else {
				// full 模式音频：音频容器目标 → 转到容器 codec（同 codec 才拷贝）；
				// 普通容器 → 兼容表允许则拷贝，否则转 AAC/Opus
				if audioTarget, isAudioOut := audioOnlyTargets[targetExt]; isAudioOut {
					if stream.CodecName == audioTarget && params.audioBitrate <= 0 &&
						!params.audioMono && params.audioSampleRate <= 0 {
						plan.action = actionCopy
					} else {
						plan.action = actionEncode
						plan.targetKind = audioTargetCodec
						plan.bitrate = params.audioBitrate
						plan.mono, plan.sampleRate = params.audioMono, params.audioSampleRate
					}
				} else {
					compat := containerCompat[targetContainer]
					if compat.audio == nil || compat.audio[stream.CodecName] {
						plan.action = actionCopy
					} else {
						plan.action = actionEncode
						plan.targetKind = audioTargetCodec
						plan.bitrate = params.audioBitrate
					}
				}
			}
		}
		if plan.action == actionEncode && plan.bitrate <= 0 {
			if stream.Kind == "video" {
				plan.bitrate = 2_000_000 // 源码率未知时的保底
			} else {
				plan.bitrate = COMPRESS_AUDIO_CEILING
			}
		}
		plans = append(plans, plan)
	}
	return plans
}

var audioCodecIDByName = map[string]astiav.CodecID{
	"aac": astiav.CodecIDAac, "mp3": astiav.CodecIDMp3, "flac": astiav.CodecIDFlac,
	"opus": astiav.CodecIDOpus, "pcm_s16le": astiav.CodecIDPcmS16Le,
}

// streamWorker 单流执行器（copy 或 decode→filter→encode）
type streamWorker struct {
	plan         streamPlan
	input        *astiav.Stream
	output       *astiav.Stream
	decCtx       *astiav.CodecContext
	encCtx       *astiav.CodecContext
	graph        *astiav.FilterGraph
	srcCtx       *astiav.BuffersrcFilterContext
	sinkCtx      *astiav.BuffersinkFilterContext
	decFrame     *astiav.Frame
	filtFrame    *astiav.Frame
	encPkt       *astiav.Packet
	lastPts      *int64
	framesDone   int64             // 进度计数（视频帧）
	audioFifo    *astiav.AudioFifo // 定长帧编码器（mp3=1152/aac=1024）的采样缓冲
	audioSamples int64             // 已编码采样数（fifo 切片后补 PTS 用，时基 1/采样率）
}

// executeFile 统一执行器
func executeFile(input, output string, mode Mode, probe ProbeResult, params execParams, logf Logf) (ExecResult, error) {
	closer := astikit.NewCloser()
	defer closer.Close()

	// 输入
	inputCtx := astiav.AllocFormatContext()
	if inputCtx == nil {
		return ExecResult{}, errors.New("input context 分配失败")
	}
	closer.Add(inputCtx.Free)
	if err := inputCtx.OpenInput(input, nil, nil); err != nil {
		return ExecResult{}, fmt.Errorf("打开输入失败: %w", err)
	}
	closer.Add(inputCtx.CloseInput)
	if err := inputCtx.FindStreamInfo(nil); err != nil {
		return ExecResult{}, fmt.Errorf("读流信息失败: %w", err)
	}

	// 输出
	outputCtx, outputErr := astiav.AllocOutputFormatContext(nil, "", output)
	if outputErr != nil || outputCtx == nil {
		return ExecResult{}, errors.New("输出格式创建失败（检查扩展名）")
	}
	closer.Add(outputCtx.Free)

	targetExt := strings.TrimPrefix(extOf(output), ".")
	plans := planStreams(mode, probe, params, targetExt)
	workers := map[int]*streamWorker{}
	result := ExecResult{}

	for _, plan := range plans {
		if plan.action == actionDrop {
			if plan.probe.Kind == "subtitle" {
				logf("跳过字幕流 #%d（%s 不兼容容器 %s）", plan.probe.Index, plan.probe.CodecName, targetExt)
			}
			continue
		}
		if plan.probe.Kind == "video" {
			plan.scaleWidth = params.scaleWidth
			plan.scaleHeight = params.scaleHeight
			plan.rotation = params.rotation
			plan.videoFilter = params.videoFilter
			plan.swapDims = params.swapEncodeDims
		}
		inputStream := inputCtx.Streams()[plan.probe.Index]
		outputStream := outputCtx.NewStream(nil)
		if outputStream == nil {
			return ExecResult{}, errors.New("输出流创建失败")
		}
		worker := &streamWorker{plan: plan, input: inputStream, output: outputStream}

		if plan.action == actionCopy {
			if err := inputStream.CodecParameters().Copy(outputStream.CodecParameters()); err != nil {
				return ExecResult{}, fmt.Errorf("拷贝流参数失败: %w", err)
			}
			// 跨容器 remux：源 codec_tag（如 mp4 的 mp4a/avc1）目标容器可能不认——
			// 清零让目标 muxer 自行分配合法 tag
			outputStream.CodecParameters().SetCodecTag(0)
			outputStream.SetTimeBase(inputStream.TimeBase())
			// transform-matrix 路径：写 display-matrix（源无矩阵前提由 PlanTransform 保证）
			if params.hasMatrixWrite && plan.probe.Kind == "video" {
				if matrixErr := outputStream.CodecParameters().SideData().DisplayMatrix().Add(
					astiav.NewDisplayMatrixFromRotation(float64(params.writeMatrixAngle))); matrixErr != nil {
					return ExecResult{}, fmt.Errorf("写旋转矩阵失败: %w", matrixErr)
				}
			}
		} else {
			if err := setupEncoder(closer, worker, outputCtx, inputCtx, inputStream, logf); err != nil {
				return ExecResult{}, err
			}
			outputStream.SetTimeBase(worker.encCtx.TimeBase())
		}
		workers[plan.probe.Index] = worker

		// 结果记录
		if plan.probe.Kind == "video" {
			result.VideoAction = plan.action
			result.VideoCodec = plan.targetKind.Name()
			if plan.action == actionCopy {
				result.VideoCodec = plan.probe.CodecName
				result.VideoBitrate = plan.probe.Bitrate
			} else {
				result.VideoBitrate = plan.bitrate
			}
		} else {
			result.AudioAction = plan.action
			result.AudioCodec = plan.targetKind.Name()
			if plan.action == actionCopy {
				result.AudioCodec = plan.probe.CodecName
				result.AudioBitrate = plan.probe.Bitrate
			} else {
				result.AudioBitrate = plan.bitrate
			}
		}
	}
	if len(workers) == 0 {
		return ExecResult{}, errors.New("无可输出流")
	}

	// 输出 IO + 头
	if !outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioContext, ioErr := astiav.OpenIOContext(output, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if ioErr != nil {
			return ExecResult{}, fmt.Errorf("打开输出失败: %w", ioErr)
		}
		closer.AddWithError(ioContext.Close)
		outputCtx.SetPb(ioContext)
	}
	if err := outputCtx.WriteHeader(nil); err != nil {
		return ExecResult{}, fmt.Errorf("写头失败: %w", err)
	}

	// 进度基准：总视频帧数
	totalFrames := int64(0)
	if probe.Video != nil && probe.DurationSeconds > 0 {
		frameRate := 30.0
		for _, stream := range inputCtx.Streams() {
			if stream.CodecParameters().MediaType() == astiav.MediaTypeVideo {
				if rate := stream.AvgFrameRate().Float64(); rate > 0 {
					frameRate = rate
					break
				}
			}
		}
		totalFrames = int64(probe.DurationSeconds * frameRate)
	}
	nextProgressStep := int64(0)

	// 主循环
	pkt := astiav.AllocPacket()
	closer.Add(pkt.Free)
	for {
		if stop := func() bool {
			if err := inputCtx.ReadFrame(pkt); err != nil {
				if !errors.Is(err, astiav.ErrEof) {
					logf("读包失败: %v", err)
				}
				return true
			}
			defer pkt.Unref()
			worker, ok := workers[pkt.StreamIndex()]
			if !ok {
				return false
			}
			switch worker.plan.action {
			case actionCopy:
				pkt.RescaleTs(worker.input.TimeBase(), worker.output.TimeBase())
				pkt.SetStreamIndex(worker.output.Index())
				if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
					logf("写包失败: %v", err)
					return true
				}
			case actionEncode:
				pkt.RescaleTs(worker.input.TimeBase(), worker.decCtx.TimeBase())
				if err := worker.decCtx.SendPacket(pkt); err != nil {
					logf("送包失败: %v", err)
					return true
				}
				if err := drainDecodeFilterEncode(closer, worker, outputCtx, totalFrames, &nextProgressStep, logf); err != nil {
					logf("处理失败: %v", err)
					return true
				}
			}
			return false
		}(); stop {
			break
		}
	}

	// 冲洗
	for _, worker := range workers {
		if worker.plan.action != actionEncode {
			continue
		}
		if err := flushWorker(closer, worker, outputCtx, totalFrames, &nextProgressStep, logf); err != nil {
			return ExecResult{}, err
		}
	}
	if err := outputCtx.WriteTrailer(); err != nil {
		return ExecResult{}, fmt.Errorf("写尾失败: %w", err)
	}

	if info, statErr := os.Stat(output); statErr == nil {
		result.OutputBytes = info.Size()
	}
	return result, nil
}

// setupEncoder 建 decode→filter→encode 链（POC 管线移植）
func setupEncoder(closer *astikit.Closer, worker *streamWorker, outputCtx *astiav.FormatContext, inputCtx *astiav.FormatContext, inputStream *astiav.Stream, logf Logf) error {
	plan := worker.plan
	isVideo := plan.probe.Kind == "video"

	// 解码器
	decoder := astiav.FindDecoder(codecIDByName(plan.probe.CodecName))
	if decoder == nil {
		decoder = astiav.FindDecoder(inputStream.CodecParameters().CodecID())
	}
	if decoder == nil {
		return fmt.Errorf("找不到解码器: %s", plan.probe.CodecName)
	}
	worker.decCtx = astiav.AllocCodecContext(decoder)
	if worker.decCtx == nil {
		return errors.New("decoder context 分配失败")
	}
	closer.Add(worker.decCtx.Free)
	if err := inputStream.CodecParameters().ToCodecContext(worker.decCtx); err != nil {
		return fmt.Errorf("解码参数注入失败: %w", err)
	}
	if isVideo {
		// 关键：视频编码器的帧率感知来自这里——缺了 x264 会把 timebase(1/15360) 当 fps
		worker.decCtx.SetFramerate(inputCtx.GuessFrameRate(inputStream, nil))
		worker.decCtx.SetTimeBase(inputStream.TimeBase())
	} else {
		worker.decCtx.SetTimeBase(inputStream.TimeBase())
	}
	if err := worker.decCtx.Open(decoder, nil); err != nil {
		return fmt.Errorf("打开解码器失败: %w", err)
	}

	// 编码器
	encoder := astiav.FindEncoder(plan.targetKind)
	if encoder == nil {
		return fmt.Errorf("找不到编码器: %s", plan.targetKind.Name())
	}
	worker.encCtx = astiav.AllocCodecContext(encoder)
	if worker.encCtx == nil {
		return errors.New("encoder context 分配失败")
	}
	closer.Add(worker.encCtx.Free)

	if isVideo {
		// 显示坐标系：±90° 旋转或显式交换滤镜时宽高对调（transpose 后的真实帧尺寸）
		targetWidth, targetHeight := worker.decCtx.Width(), worker.decCtx.Height()
		if worker.plan.rotation%180 != 0 || worker.plan.swapDims {
			targetWidth, targetHeight = targetHeight, targetWidth
		}
		if worker.plan.scaleWidth > 0 && worker.plan.scaleHeight > 0 {
			targetWidth, targetHeight = worker.plan.scaleWidth, worker.plan.scaleHeight
			worker.encCtx.SetSampleAspectRatio(astiav.NewRational(1, 1)) // 缩放=进方像素世界（与 setsar=1 滤镜配套，否则流级 SAR 盖滤镜导致双重校正）
		}
		worker.encCtx.SetHeight(targetHeight)
		worker.encCtx.SetWidth(targetWidth)
		if formats := encoder.SupportedPixelFormats(); len(formats) > 0 {
			worker.encCtx.SetPixelFormat(formats[0])
		} else {
			worker.encCtx.SetPixelFormat(worker.decCtx.PixelFormat())
		}
		if worker.plan.scaleWidth == 0 { // 缩放路径已在尺寸块归一 1:1——此处再抄源值会盖掉
			worker.encCtx.SetSampleAspectRatio(worker.decCtx.SampleAspectRatio())
		}
		worker.encCtx.SetTimeBase(worker.decCtx.TimeBase())
		worker.encCtx.SetBitRate(plan.bitrate)
	} else if plan.probe.Kind == "subtitle" {
		// 字幕编码器：毫秒时基（mov_text/webvtt 标准）——不设会报 timebase not set
		worker.encCtx.SetTimeBase(astiav.NewRational(1, 1000))
	} else {
		// 声道目标：mono 旗标 > 编码器支持表首项 > 源布局
		if plan.mono {
			worker.encCtx.SetChannelLayout(astiav.ChannelLayoutMono)
		} else if layouts := encoder.SupportedChannelLayouts(); len(layouts) > 0 {
			worker.encCtx.SetChannelLayout(layouts[0])
		} else {
			worker.encCtx.SetChannelLayout(worker.decCtx.ChannelLayout())
		}
		if plan.sampleRate > 0 {
			worker.encCtx.SetSampleRate(plan.sampleRate)
		} else {
			worker.encCtx.SetSampleRate(worker.decCtx.SampleRate())
		}
		// 采样率对齐：编码器支持表不含源采样率时取首个支持值（libopus 只吃 48k 系）
		if rates := encoder.SupportedSampleRates(); len(rates) > 0 {
			sourceRate := worker.decCtx.SampleRate()
			rateOK := false
			for _, rate := range rates {
				if rate == sourceRate {
					rateOK = true
					break
				}
			}
			if !rateOK {
				worker.encCtx.SetSampleRate(rates[0])
			}
		}
		if formats := encoder.SupportedSampleFormats(); len(formats) > 0 {
			worker.encCtx.SetSampleFormat(formats[0])
		} else {
			worker.encCtx.SetSampleFormat(worker.decCtx.SampleFormat())
		}
		worker.encCtx.SetTimeBase(astiav.NewRational(1, worker.encCtx.SampleRate()))
		if plan.bitrate > 0 {
			worker.encCtx.SetBitRate(plan.bitrate)
		}
	}
	if outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader) {
		worker.encCtx.SetFlags(worker.encCtx.Flags().Add(astiav.CodecContextFlagGlobalHeader))
	}
	if err := worker.encCtx.Open(encoder, nil); err != nil {
		return fmt.Errorf("打开编码器失败: %w", err)
	}
	// 定长帧音频编码器：滤镜图吐任意长度帧会被拒（frame_size not respected）——
	// AudioFifo 缓冲后按编码器帧长切片
	if !isVideo && worker.encCtx.FrameSize() > 0 {
		worker.audioFifo = astiav.AllocAudioFifo(
			worker.encCtx.SampleFormat(), worker.encCtx.ChannelLayout().Channels(),
			worker.encCtx.FrameSize()*4)
		closer.Add(worker.audioFifo.Free)
	}
	if err := worker.output.CodecParameters().FromCodecContext(worker.encCtx); err != nil {
		return fmt.Errorf("编码参数导出失败: %w", err)
	}

	// 滤镜（像素/采样格式归一——POC 同构）
	worker.graph = astiav.AllocFilterGraph()
	if worker.graph == nil {
		return errors.New("filter graph 分配失败")
	}
	closer.Add(worker.graph.Free)
	inputs := astiav.AllocFilterInOut()
	closer.Add(inputs.Free)
	outputs := astiav.AllocFilterInOut()
	closer.Add(outputs.Free)
	srcParams := astiav.AllocBuffersrcFilterContextParameters()
	defer srcParams.Free()

	var srcFilter, sinkFilter *astiav.Filter
	var content string
	if isVideo {
		srcFilter = astiav.FindFilterByName("buffer")
		srcParams.SetHeight(worker.decCtx.Height())
		srcParams.SetPixelFormat(worker.decCtx.PixelFormat())
		srcParams.SetSampleAspectRatio(worker.decCtx.SampleAspectRatio())
		srcParams.SetTimeBase(worker.decCtx.TimeBase())
		srcParams.SetWidth(worker.decCtx.Width())
		sinkFilter = astiav.FindFilterByName("buffersink")
		content = fmt.Sprintf("format=pix_fmts=%s", worker.encCtx.PixelFormat().Name())
		// 链序：源旋转烤入（transpose）→ 显式变换滤镜（hflip 等）→ scale → format——
		// 串联而非二选一（mirror 需要先烤源旋转再镜像）
		if transpose := TransposeFilter(worker.plan.rotation); transpose != "" {
			content = transpose + "," + content
			logf("烤入旋转 %d°（transpose 无损像素重排）", worker.plan.rotation)
		}
		if prefix := worker.plan.videoFilter; prefix != "" {
			content = prefix + "," + content
			logf("应用滤镜: %s", prefix)
		}
		if worker.plan.scaleWidth > 0 && worker.plan.scaleHeight > 0 {
			// 缩放即坐标系切换：归一方像素（可烤元数据 #2——SAR 随缩放烤入显示尺寸）
			content = fmt.Sprintf("scale=%d:%d,setsar=1,", worker.plan.scaleWidth, worker.plan.scaleHeight) + content
		}
	} else {
		srcFilter = astiav.FindFilterByName("abuffer")
		srcParams.SetChannelLayout(worker.decCtx.ChannelLayout())
		srcParams.SetSampleFormat(worker.decCtx.SampleFormat())
		srcParams.SetSampleRate(worker.decCtx.SampleRate())
		srcParams.SetTimeBase(worker.decCtx.TimeBase())
		sinkFilter = astiav.FindFilterByName("abuffersink")
		content = fmt.Sprintf("aformat=sample_fmts=%s:channel_layouts=%s:sample_rates=%d",
			worker.encCtx.SampleFormat().Name(), worker.encCtx.ChannelLayout().String(), worker.encCtx.SampleRate())
	}
	if srcFilter == nil || sinkFilter == nil {
		return errors.New("滤镜不存在")
	}
	var err error
	if worker.srcCtx, err = worker.graph.NewBuffersrcFilterContext(srcFilter, "in"); err != nil {
		return err
	}
	if worker.sinkCtx, err = worker.graph.NewBuffersinkFilterContext(sinkFilter, "out"); err != nil {
		return err
	}
	if err = worker.srcCtx.SetParameters(srcParams); err != nil {
		return err
	}
	if err = worker.srcCtx.Initialize(nil); err != nil {
		return err
	}
	outputs.SetName("in")
	outputs.SetFilterContext(worker.srcCtx.FilterContext())
	outputs.SetPadIdx(0)
	outputs.SetNext(nil)
	inputs.SetName("out")
	inputs.SetFilterContext(worker.sinkCtx.FilterContext())
	inputs.SetPadIdx(0)
	inputs.SetNext(nil)
	if err = worker.graph.Parse(content, inputs, outputs); err != nil {
		return err
	}
	if err = worker.graph.Configure(); err != nil {
		return err
	}

	worker.decFrame = astiav.AllocFrame()
	closer.Add(worker.decFrame.Free)
	worker.filtFrame = astiav.AllocFrame()
	closer.Add(worker.filtFrame.Free)
	worker.encPkt = astiav.AllocPacket()
	closer.Add(worker.encPkt.Free)
	return nil
}

// drainDecodeFilterEncode 收帧→滤镜→编码→写包（含单调 PTS 过滤——POC 同构）
func drainDecodeFilterEncode(closer *astikit.Closer, worker *streamWorker, outputCtx *astiav.FormatContext, totalFrames int64, nextStep *int64, logf Logf) error {
	for {
		if stop := func() bool {
			if err := worker.decCtx.ReceiveFrame(worker.decFrame); err != nil {
				if !errors.Is(err, astiav.ErrEof) && !errors.Is(err, astiav.ErrEagain) {
					logf("收帧失败: %v", err)
				}
				return true
			}
			defer worker.decFrame.Unref()
			if worker.lastPts != nil && *worker.lastPts >= worker.decFrame.Pts() {
				return false
			}
			worker.lastPts = astikit.Int64Ptr(worker.decFrame.Pts())
			if err := filterEncodeWrite(closer, worker, worker.decFrame, outputCtx, totalFrames, nextStep, logf); err != nil {
				logf("滤镜编码失败: %v", err)
				return true
			}
			return false
		}(); stop {
			return nil
		}
	}
}

// flushWorker 冲洗滤镜与编码器
func flushWorker(closer *astikit.Closer, worker *streamWorker, outputCtx *astiav.FormatContext, totalFrames int64, nextStep *int64, logf Logf) error {
	if err := filterEncodeWrite(closer, worker, nil, outputCtx, totalFrames, nextStep, logf); err != nil {
		return err
	}
	if worker.audioFifo != nil && worker.audioFifo.Size() > 0 {
		if err := drainAudioFifo(worker, outputCtx, totalFrames, nextStep, logf, true); err != nil {
			return err
		}
	}
	return encodeWrite(closer, worker, outputCtx, totalFrames, nextStep, logf, nil)
}

// filterEncodeWrite 送帧入滤镜图并取帧编码（frame=nil 冲洗图）
func filterEncodeWrite(closer *astikit.Closer, worker *streamWorker, frame *astiav.Frame, outputCtx *astiav.FormatContext, totalFrames int64, nextStep *int64, logf Logf) error {
	if err := worker.srcCtx.AddFrame(frame, astiav.NewBuffersrcFlags(astiav.BuffersrcFlagKeepRef)); err != nil {
		return err
	}
	_ = nextStep
	for {
		if stop, err := func() (bool, error) {
			if err := worker.sinkCtx.GetFrame(worker.filtFrame, astiav.NewBuffersinkFlags()); err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					return true, nil
				}
				return false, err
			}
			defer worker.filtFrame.Unref()
			worker.filtFrame.SetPictureType(astiav.PictureTypeNone)
			if worker.audioFifo != nil {
				// 定长帧路径：先进 fifo，凑满一编码器帧就切一片编码
				if _, err := worker.audioFifo.Write(worker.filtFrame); err != nil {
					return false, fmt.Errorf("fifo 写入失败: %w", err)
				}
				if err := drainAudioFifo(worker, outputCtx, totalFrames, nextStep, logf, false); err != nil {
					return false, err
				}
				return false, nil
			}
			if err := encodeWrite(closer, worker, outputCtx, totalFrames, nextStep, logf, worker.filtFrame); err != nil {
				return false, err
			}
			return false, nil
		}(); err != nil {
			return err
		} else if stop {
			return nil
		}
	}
}

// drainAudioFifo 从 fifo 切编码器帧长的块编码（final=true 时把剩余不足一帧的尾巴也编掉）
func drainAudioFifo(worker *streamWorker, outputCtx *astiav.FormatContext, totalFrames int64, nextStep *int64, logf Logf, final bool) error {
	frameSize := worker.encCtx.FrameSize()
	for worker.audioFifo.Size() >= frameSize || (final && worker.audioFifo.Size() > 0) {
		chunk := astiav.AllocFrame()
		chunk.SetNbSamples(frameSize)
		if remaining := worker.audioFifo.Size(); remaining > 0 && remaining < frameSize {
			chunk.SetNbSamples(remaining)
		}
		chunk.SetChannelLayout(worker.encCtx.ChannelLayout())
		chunk.SetSampleFormat(worker.encCtx.SampleFormat())
		chunk.SetSampleRate(worker.encCtx.SampleRate())
		chunk.SetPts(worker.audioSamples) // 时基 1/采样率 → PTS 即采样数
		if err := chunk.AllocBuffer(0); err != nil {
			chunk.Free()
			return fmt.Errorf("音频帧缓冲失败: %w", err)
		}
		if _, err := worker.audioFifo.Read(chunk); err != nil {
			chunk.Free()
			return fmt.Errorf("fifo 读出失败: %w", err)
		}
		worker.audioSamples += int64(chunk.NbSamples())
		err := encodeWrite(nil, worker, outputCtx, totalFrames, nextStep, logf, chunk)
		chunk.Free()
		if err != nil {
			return err
		}
	}
	return nil
}

// encodeWrite 编码并写包（frame=nil 为冲洗）
func encodeWrite(closer *astikit.Closer, worker *streamWorker, outputCtx *astiav.FormatContext, totalFrames int64, nextStep *int64, logf Logf, frame *astiav.Frame) error {
	if err := worker.encCtx.SendFrame(frame); err != nil {
		return err
	}
	for {
		if stop, err := func() (bool, error) {
			if err := worker.encCtx.ReceivePacket(worker.encPkt); err != nil {
				if errors.Is(err, astiav.ErrEof) || errors.Is(err, astiav.ErrEagain) {
					return true, nil
				}
				return false, err
			}
			defer worker.encPkt.Unref()
			worker.encPkt.SetStreamIndex(worker.output.Index())
			worker.encPkt.RescaleTs(worker.encCtx.TimeBase(), worker.output.TimeBase())
			if err := outputCtx.WriteInterleavedFrame(worker.encPkt); err != nil {
				return false, err
			}
			// 进度（每 10% 一档，视频帧计）
			if totalFrames > 0 && worker.plan.probe.Kind == "video" {
				worker.framesDone++
				pct := worker.framesDone * 100 / totalFrames
				if pct >= *nextStep {
					*nextStep = pct + 10
					logf("转码进度: %d%%", pct)
				}
			}
			return false, nil
		}(); err != nil {
			return err
		} else if stop {
			return nil
		}
	}
}

// codecIDByName 常用编解码名 → CodecID（探测记录的是名字；解码按名回查）
func codecIDByName(name string) astiav.CodecID {
	if id, known := videoCodecIDByName[name]; known {
		return id
	}
	if id, known := audioCodecIDByName[name]; known {
		return id
	}
	return astiav.CodecIDNone
}

var videoCodecIDByName = map[string]astiav.CodecID{
	"h264": astiav.CodecIDH264, "hevc": astiav.CodecIDHevc, "vp8": astiav.CodecIDVp8,
	"vp9": astiav.CodecIDVp9, "av1": astiav.CodecIDAv1, "mpeg4": astiav.CodecIDMpeg4,
	"prores": astiav.CodecIDProres,
}

// transferName 传输特性 → 可读名（PQ/HLG = HDR 标记）
func transferName(t astiav.ColorTransferCharacteristic) string {
	switch t {
	case astiav.ColorTransferCharacteristicSmpte2084:
		return "smpte2084"
	case astiav.ColorTransferCharacteristicAribStdB67:
		return "arib-std-b67"
	case astiav.ColorTransferCharacteristicBt709:
		return "bt709"
	default:
		return ""
	}
}

// isTextSubtitle 文本类字幕（可互转——区别于位图字幕 dvdsub/pgssub）
func isTextSubtitle(codecName string) bool {
	switch codecName {
	case "subrip", "srt", "ssa", "ass", "mov_text", "webvtt":
		return true
	}
	return false
}

// subtitleTargetFor 容器 → 目标字幕编码名（不兼容时文本字幕转到此编码）
var subtitleTargetFor = map[string]string{
	"mp4":  "mov_text",
	"mkv":  "subrip",
	"webm": "webvtt",
}

// subtitleCodecIDByName 字幕编码名 → CodecID
var subtitleCodecIDByName = map[string]astiav.CodecID{
	"mov_text": astiav.CodecIDMovText,
	"subrip":   astiav.CodecIDSubrip,
	"webvtt":   astiav.CodecIDWebvtt,
	"ssa":      astiav.CodecIDSsa,
	"ass":      astiav.CodecIDAss,
}
