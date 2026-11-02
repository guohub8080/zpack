//go:build !cgo

// 非 CGO 构建桩：HEIC/Live Photo 需要 astiav 引擎——纯构建返回明确指引。
package image

import "fmt"

// HEICSupported 当前构建不支持 HEIC（需 astiav/CGO）
const HEICSupported = false

// IsHEIC 判断路径是否 HEIC/HEIF/AVIF（纯构建也能判断——只看扩展名）
func IsHEIC(path string) bool {
	ext := lowerExt(path)
	return ext == ".heic" || ext == ".heif" || ext == ".avif"
}

func lowerExt(path string) string {
	for idx := len(path) - 1; idx >= 0; idx-- {
		if path[idx] == '.' {
			return toLower(path[idx:])
		}
	}
	return ""
}

func toLower(s string) string {
	result := []byte(s)
	for idx, ch := range result {
		if ch >= 'A' && ch <= 'Z' {
			result[idx] = ch + 32
		}
	}
	return string(result)
}

// ErrHEICNeedsFullBuild 纯构建不支持 HEIC
var ErrHEICNeedsFullBuild = fmt.Errorf("HEIC/HEIF/AVIF 解码需要完整版（build_cli 产线 CGO 构建）；纯 Go 构建不含 astiav/libav 引擎")

// ConvertHEIC 纯构建桩
func ConvertHEIC(input, output string, quality int) (*ImageResult, error) {
	return nil, ErrHEICNeedsFullBuild
}

// DetectLivePhoto 配对文件检测（纯构建也支持——只是文件查找）
type LivePhotoInfo struct {
	HEICPath  string `json:"heic_path"`
	VideoPath string `json:"video_path,omitempty"`
	HasVideo  bool   `json:"has_video"`
}

func DetectLivePhoto(heicPath string) *LivePhotoInfo {
	return detectLivePhotoImpl(heicPath)
}

// ExtractLivePhotoVideo 配对文件提取（纯构建也支持——只是文件拷贝）
func ExtractLivePhotoVideo(heicPath, outputDir string) (string, error) {
	return extractLivePhotoImpl(heicPath, outputDir)
}
