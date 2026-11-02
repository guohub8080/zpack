//go:build !cgo

// HDR 图片解码——纯构建桩：EXR/HDR/JXL 需完整版（CGO 构建）。
package image

import (
	"fmt"
	"image"
	"strings"
)

// decodeHDRImage 纯构建桩：HDR 格式明确报需完整版（不静默失败）
func decodeHDRImage(path string) (image.Image, string, bool, error) {
	if _, isHDR := hdrFormatByExt(path); isHDR {
		return nil, "", true, fmt.Errorf("EXR/HDR/JXL 需完整版（CGO 构建）")
	}
	return nil, "", false, nil
}

// probeHDRInfo 纯构建桩
func probeHDRInfo(path string) (*ImageInfo, bool, error) {
	if _, isHDR := hdrFormatByExt(path); isHDR {
		return nil, true, fmt.Errorf("EXR/HDR/JXL 需完整版（CGO 构建）")
	}
	return nil, false, nil
}

// hdrFormatByExt 扩展名判定（与 cgo 侧同名同义）
func hdrFormatByExt(path string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(extOf(path), "."))
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
