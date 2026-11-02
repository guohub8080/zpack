//go:build cgo

// 视频封面添加：把图片作为 attached_pic 元数据嵌入视频容器（remux + metadata——视频流零重编）。
// 封面图自动缩放到视频尺寸，编码为 MJPEG（最兼容的封面格式），disposition=AttachedPic。
package media

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"

	"github.com/asticode/go-astiav"

	zimage "github.com/guohub8080/zpack/image"
)

// AddCover 添加封面（attached_pic）——视频流零重编（remux 速度）
// 封面图自动缩放到视频尺寸（不保比例——封面必须精确匹配视频尺寸才不变形）
func AddCover(input, coverImagePath, output string) (*CoverResult, error) {
	// 1. 探测视频尺寸
	probe, probeErr := probeFile(input)
	if probeErr != nil {
		return nil, fmt.Errorf("读取失败: %w", probeErr)
	}
	// 视频文件：封面缩放到视频尺寸；音频文件：用封面图原始尺寸（专辑封面没有"视频尺寸"概念）
	videoWidth, videoHeight := 0, 0
	if probe.Video != nil {
		videoWidth = probe.Video.Width
		videoHeight = probe.Video.Height
	} else {
		// 音频文件：读封面图原始尺寸
		coverCfg, _, cfgErr := zimage.ReadImage(coverImagePath)
		if cfgErr == nil {
			bounds := coverCfg.Bounds()
			videoWidth = bounds.Dx()
			videoHeight = bounds.Dy()
		}
		if videoWidth == 0 || videoHeight == 0 {
			videoWidth, videoHeight = 500, 500 // 兜底：标准专辑封面尺寸
		}
	}

	// 2. 读封面图 → 缩放（视频文件缩到视频尺寸；音频文件保持原始尺寸）
	coverImg, _, imgErr := zimage.ReadImage(coverImagePath)
	if imgErr != nil {
		return nil, fmt.Errorf("读取封面图失败: %w", imgErr)
	}
	resized := image.NewRGBA(image.Rect(0, 0, videoWidth, videoHeight))
	draw.Draw(resized, resized.Bounds(), coverImg, coverImg.Bounds().Min, draw.Src)
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, resized, &jpeg.Options{Quality: 92}); err != nil {
		return nil, fmt.Errorf("封面 JPEG 编码失败: %w", err)
	}
	jpegBytes := jpegBuf.Bytes()

	// 3. 打开输入视频
	inputCtx := astiav.AllocFormatContext()
	if inputCtx == nil {
		return nil, fmt.Errorf("input context 分配失败")
	}
	defer inputCtx.Free()
	if err := inputCtx.OpenInput(input, nil, nil); err != nil {
		return nil, fmt.Errorf("打开视频失败: %w", err)
	}
	defer inputCtx.CloseInput()
	if err := inputCtx.FindStreamInfo(nil); err != nil {
		return nil, fmt.Errorf("读流信息失败: %w", err)
	}

	// 4. 创建输出（容器同输入或由扩展名决定）
	outputCtx, outErr := astiav.AllocOutputFormatContext(nil, "", output)
	if outErr != nil || outputCtx == nil {
		return nil, fmt.Errorf("输出格式创建失败")
	}
	defer outputCtx.Free()

	// 5. 拷贝输入流（remux——零重编）
	streamMap := map[int]int{} // 输入流 index → 输出流 index
	for _, inStream := range inputCtx.Streams() {
		outStream := outputCtx.NewStream(nil)
		if outStream == nil {
			return nil, fmt.Errorf("输出流创建失败")
		}
		if err := inStream.CodecParameters().Copy(outStream.CodecParameters()); err != nil {
			return nil, fmt.Errorf("拷贝流参数失败: %w", err)
		}
		outStream.CodecParameters().SetCodecTag(0) // 跨容器 tag 归零
		outStream.SetTimeBase(inStream.TimeBase())
		streamMap[inStream.Index()] = outStream.Index()
	}

	// 6. 添加 MJPEG 封面流（attached_pic——文件管理器/播放器显示的缩略图）
	coverStream := outputCtx.NewStream(nil)
	if coverStream == nil {
		return nil, fmt.Errorf("封面流创建失败")
	}
	coverStream.CodecParameters().SetCodecID(astiav.CodecIDMjpeg)
	coverStream.CodecParameters().SetMediaType(astiav.MediaTypeVideo)
	coverStream.CodecParameters().SetWidth(videoWidth)
	coverStream.CodecParameters().SetHeight(videoHeight)
	coverStream.CodecParameters().SetPixelFormat(astiav.PixelFormatYuvj420P)
	coverStream.SetTimeBase(astiav.NewRational(1, 25))
	coverStream.SetDispositionFlags(
		coverStream.DispositionFlags().Add(astiav.DispositionFlagAttachedPic))

	// 7. 打开输出 IO + 写头
	if !outputCtx.OutputFormat().Flags().Has(astiav.IOFormatFlagNofile) {
		ioCtx, ioErr := astiav.OpenIOContext(output, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
		if ioErr != nil {
			return nil, fmt.Errorf("打开输出失败: %w", ioErr)
		}
		defer ioCtx.Close()
		outputCtx.SetPb(ioCtx)
	}
	if err := outputCtx.WriteHeader(nil); err != nil {
		return nil, fmt.Errorf("写头失败: %w", err)
	}

	// 8. 写封面帧（MJPEG 包 = JPEG 字节——MJPEG 本质就是 JPEG）
	coverPkt := astiav.AllocPacket()
	defer coverPkt.Free()
	if err := coverPkt.FromData(jpegBytes); err != nil {
		return nil, fmt.Errorf("封面包构建失败: %w", err)
	}
	coverPkt.SetStreamIndex(coverStream.Index())
	coverPkt.SetPts(0)
	coverPkt.SetDts(0)
	if err := outputCtx.WriteInterleavedFrame(coverPkt); err != nil {
		return nil, fmt.Errorf("写封面失败: %w", err)
	}

	// 9. 拷贝全部输入包（视频+音频+字幕 remux）
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
		pkt.SetStreamIndex(outIdx)
		if err := outputCtx.WriteInterleavedFrame(pkt); err != nil {
			pkt.Unref()
			return nil, fmt.Errorf("写包失败: %w", err)
		}
		pkt.Unref()
	}

	// 10. 写尾
	if err := outputCtx.WriteTrailer(); err != nil {
		return nil, fmt.Errorf("写尾失败: %w", err)
	}

	outputInfo, _ := os.Stat(output)
	absOutput, _ := filepath.Abs(output)
	return &CoverResult{
		Input: input, Output: absOutput, CoverImg: coverImagePath,
		Width: videoWidth, Height: videoHeight, SizeBytes: outputInfo.Size(),
	}, nil
}
