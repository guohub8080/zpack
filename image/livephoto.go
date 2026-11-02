// Live Photo 共享逻辑（纯构建也支持——配对文件检测+拷贝不需要 HEIC 解码）
package image

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func detectLivePhotoImpl(heicPath string) *LivePhotoInfo {
	info := &LivePhotoInfo{}
	if abs, err := filepath.Abs(heicPath); err == nil {
		info.HEICPath = abs
	} else {
		info.HEICPath = heicPath
	}
	base := strings.TrimSuffix(heicPath, filepath.Ext(heicPath))
	for _, ext := range []string{".MOV", ".mov", ".Mp4", ".mp4"} {
		candidate := base + ext
		if _, err := os.Stat(candidate); err == nil {
			if abs, err := filepath.Abs(candidate); err == nil {
				info.VideoPath = abs
			} else {
				info.VideoPath = candidate
			}
			info.HasVideo = true
			break
		}
	}
	return info
}

func extractLivePhotoImpl(heicPath, outputDir string) (string, error) {
	info := detectLivePhotoImpl(heicPath)
	if !info.HasVideo {
		return "", fmt.Errorf("未找到配对视频（在 %s 同目录找同名 .MOV/.mov）", filepath.Dir(heicPath))
	}
	if outputDir == "" {
		outputDir = filepath.Dir(heicPath)
	}
	baseName := filepath.Base(info.VideoPath)
	dstPath := filepath.Join(outputDir, "video_"+baseName)
	data, err := os.ReadFile(info.VideoPath)
	if err != nil {
		return "", fmt.Errorf("读取视频失败: %w", err)
	}
	if err := os.WriteFile(dstPath, data, 0644); err != nil {
		return "", fmt.Errorf("写入视频失败: %w", err)
	}
	return dstPath, nil
}
