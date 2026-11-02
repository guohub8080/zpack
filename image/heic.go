//go:build cgo

// HEIC 解码与 Live Photo 提取（走 astiav 引擎——FFmpeg 的 ISOBMFF/HEVC 解码器覆盖 HEIC）。
// HEIC 本质：ISOBMFF 容器 + 单帧 HEVC 编码静止图像——FFmpeg 的 mov demuxer + hevc decoder 天然覆盖。
package image

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/asticode/go-astiav"
)

// HEICSupported 当前构建支持 HEIC（astiav 可用）
const HEICSupported = true

// HEICExtensions HEIC 系列扩展名
var HEICExtensions = []string{".heic", ".heif", ".avif"}

// IsHEIC 判断路径是否 HEIC/HEIF/AVIF
func IsHEIC(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, heicExt := range HEICExtensions {
		if ext == heicExt {
			return true
		}
	}
	return false
}

// DecodeHEIC 通过 astiav 解码 HEIC/HEIF/AVIF → Go image.Image（取首帧）
func DecodeHEIC(path string) (interface{}, int, int, error) {
	formatContext := astiav.AllocFormatContext()
	if formatContext == nil {
		return nil, 0, 0, fmt.Errorf("format context 分配失败")
	}
	defer formatContext.Free()

	if err := formatContext.OpenInput(path, nil, nil); err != nil {
		return nil, 0, 0, fmt.Errorf("打开 HEIC 失败: %w", err)
	}
	defer formatContext.CloseInput()
	if err := formatContext.FindStreamInfo(nil); err != nil {
		return nil, 0, 0, fmt.Errorf("读流信息失败: %w", err)
	}

	// 找视频流（HEIC 的图像就是视频流）
	var videoStream *astiav.Stream
	for _, stream := range formatContext.Streams() {
		if stream.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = stream
			break
		}
	}
	if videoStream == nil {
		return nil, 0, 0, fmt.Errorf("HEIC 中无图像流")
	}

	// 打开解码器
	decoder := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decoder == nil {
		return nil, 0, 0, fmt.Errorf("找不到解码器 %s", videoStream.CodecParameters().CodecID().Name())
	}
	codecContext := astiav.AllocCodecContext(decoder)
	if codecContext == nil {
		return nil, 0, 0, fmt.Errorf("decoder context 分配失败")
	}
	defer codecContext.Free()
	if err := videoStream.CodecParameters().ToCodecContext(codecContext); err != nil {
		return nil, 0, 0, err
	}
	if err := codecContext.Open(decoder, nil); err != nil {
		return nil, 0, 0, fmt.Errorf("打开解码器失败: %w", err)
	}

	// 读一帧
	packet := astiav.AllocPacket()
	defer packet.Free()
	frame := astiav.AllocFrame()
	defer frame.Free()

	for {
		if err := formatContext.ReadFrame(packet); err != nil {
			return nil, 0, 0, fmt.Errorf("读取帧失败: %w", err)
		}
		if packet.StreamIndex() != videoStream.Index() {
			packet.Unref()
			continue
		}
		if err := codecContext.SendPacket(packet); err != nil {
			packet.Unref()
			return nil, 0, 0, fmt.Errorf("解码失败: %w", err)
		}
		packet.Unref()
		if err := codecContext.ReceiveFrame(frame); err != nil {
			continue // 试试下一包
		}
		// 成功——返回帧信息（像素数据由调用方通过 astiav 处理）
		return frame, frame.Width(), frame.Height(), nil
	}
}

// ConvertHEIC HEIC → 标准格式（走 astiav 解码管线 → 帧转 image.Image → 标准编码）
func ConvertHEIC(input, output string, quality int) (*ImageResult, error) {
	// 用 astiav 直接输出视频帧到临时文件（PNG），再用纯 Go 转
	// 更简单的方案：直接用 astiav 的 format context 输出到目标格式
	// 最简：借 media 引擎的转码管线（HEIC 作为视频输入，输出单帧图片）

	// 打开输入
	formatContext := astiav.AllocFormatContext()
	if formatContext == nil {
		return nil, fmt.Errorf("format context 分配失败")
	}
	defer formatContext.Free()
	if err := formatContext.OpenInput(input, nil, nil); err != nil {
		return nil, fmt.Errorf("打开 HEIC 失败: %w", err)
	}
	defer formatContext.CloseInput()
	if err := formatContext.FindStreamInfo(nil); err != nil {
		return nil, fmt.Errorf("读流信息失败: %w", err)
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
		return nil, fmt.Errorf("HEIC 中无图像流")
	}
	width := videoStream.CodecParameters().Width()
	height := videoStream.CodecParameters().Height()
	sourceFormat := strings.TrimPrefix(strings.ToLower(filepath.Ext(input)), ".")

	// astiav 解码首帧 → Go RGBA（浮点族 tonemap/整数族 sws；同 EXR 通道）
	frame, frameErr := decodeFirstFrame(input)
	if frameErr != nil {
		return nil, fmt.Errorf("HEIC 解码失败: %w", frameErr)
	}
	defer frame.Unref()
	rgba, convErr := frameToRGBA(frame)
	if convErr != nil {
		return nil, fmt.Errorf("HEIC 像素转换失败: %w", convErr)
	}
	width, height = rgba.Bounds().Dx(), rgba.Bounds().Dy()
	var img image.Image = rgba
	outputExt := strings.TrimPrefix(filepath.Ext(output), ".")
	if outputExt == "" {
		outputExt = "jpg"
	}
	if quality <= 0 {
		quality = IMAGE_DEFAULT_QUALITY
	}
	if err := WriteImage(output, img, outputExt, quality); err != nil {
		return nil, err
	}
	inputInfo, _ := os.Stat(input)
	outputInfo, _ := os.Stat(output)
	return &ImageResult{
		Input: absPath(input), Output: absPath(output),
		SourceFormat: sourceFormat, TargetFormat: normalizeFormat(outputExt),
		Width: width, Height: height,
		OriginalSize: inputInfo.Size(), OutputSize: outputInfo.Size(),
		Ratio: ratio(outputInfo.Size(), inputInfo.Size()),
	}, nil
}

// LivePhotoInfo Live Photo 探测结果
type LivePhotoInfo struct {
	HEICPath  string `json:"heic_path"`
	VideoPath string `json:"video_path,omitempty"` // 配对 .MOV 路径
	HasVideo  bool   `json:"has_video"`
}

// DetectLivePhoto 探测 Live Photo（配对文件检测：IMG_1234.HEIC ↔ IMG_1234.MOV/.mov）
func DetectLivePhoto(heicPath string) *LivePhotoInfo {
	info := &LivePhotoInfo{
		HEICPath: absPath(heicPath),
	}
	base := strings.TrimSuffix(heicPath, filepath.Ext(heicPath))
	for _, ext := range []string{".MOV", ".mov", ".Mp4", ".mp4"} {
		candidate := base + ext
		if _, err := os.Stat(candidate); err == nil {
			info.VideoPath = absPath(candidate)
			info.HasVideo = true
			break
		}
	}
	return info
}

// ExtractLivePhotoVideo 提取 Live Photo 的视频（配对文件模式：拷贝 .MOV）
func ExtractLivePhotoVideo(heicPath, outputDir string) (string, error) {
	info := DetectLivePhoto(heicPath)
	if !info.HasVideo {
		return "", fmt.Errorf("未找到配对视频（在 %s 同目录找同名 .MOV/.mov）", filepath.Dir(heicPath))
	}
	if outputDir == "" {
		outputDir = filepath.Dir(heicPath)
	}
	baseName := filepath.Base(info.VideoPath)
	dstPath := filepath.Join(outputDir, "video_"+baseName)
	if err := copyFile(info.VideoPath, dstPath); err != nil {
		return "", fmt.Errorf("拷贝视频失败: %w", err)
	}
	return dstPath, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
