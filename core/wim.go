// wim（Windows Imaging Format）读取：nradchenko/go-wim v0.2.0（MIT 纯 Go，含 LZX 流
// 按需解码，读取经 wimlib 在真实 WinPE 镜像逐字节验证）。多镜像归档按 "N-镜像名/"
// 前缀平铺；局限（库不支持，如实透传错误）：LZMS/ESD、XPRESS、重解析点/硬链接/ADS。
// fs.FS 走读 → serialEntry（open 走 fs.File，串行骨架——顺序流格式无并行惯例）。
package core

import (
	"fmt"
	"io"
	"io/fs"
	"path"

	wimlib "github.com/nradchenko/go-wim"
)

// WIM_IDENT wim 魔数：8 字节 "MSWIM\0\0\0"
const WIM_IDENT = "MSWIM\x00\x00\x00"

// wimExtract wim 解压入口（core.Extract 的 wim 分支）
func wimExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	reader, openErr := wimlib.Open(source.readerAt(), source.size())
	if openErr != nil {
		return ExtractResult{}, fmt.Errorf("无法读取 wim 归档: %s", openErr.Error())
	}
	images := reader.Images()
	if len(images) == 0 {
		return ExtractResult{}, fmt.Errorf("无法读取 wim 归档: 无镜像")
	}

	var entries []serialEntry
	seenNames := map[string]bool{}
	for imageIdx, imageInfo := range images {
		image, imageErr := reader.Image(imageIdx + 1)
		if imageErr != nil {
			return ExtractResult{}, fmt.Errorf("无法读取 wim 归档: %s", imageErr.Error())
		}
		imageFS := image.FS()
		// 多镜像时按 "N-镜像名/" 前缀平铺（镜像名不安全字符就地清洗为下划线）
		prefix := ""
		if len(images) > 1 {
			safeName := sanitizeEntryNameComponent(imageInfo.Name)
			if safeName == "" {
				safeName = fmt.Sprintf("image-%d", imageIdx+1)
			}
			prefix = fmt.Sprintf("%d-%s/", imageIdx+1, safeName)
		}
		var walkErr error
		walkErr = fs.WalkDir(imageFS, ".", func(entryPath string, dirEntry fs.DirEntry, walkErrInner error) error {
			if walkErrInner != nil {
				return walkErrInner
			}
			if entryPath == "." {
				return nil
			}
			entryInfo, infoErr := dirEntry.Info()
			if infoErr != nil {
				return infoErr
			}
			cleanName := prefix + entryPath
			decodedName, converted := decodeEntryName(cleanName, false)
			if isUnsafeEntryPath(cleanName) || isUnsafeEntryPath(decodedName) {
				return fmt.Errorf("归档含不安全路径: %s", cleanName)
			}
			if converted {
				reportLogf(logf, "条目名按 GBK 解码: %q -> %q", cleanName, decodedName)
			}
			if seenNames[decodedName] {
				return fmt.Errorf("归档含重复条目名: %s", decodedName)
			}
			seenNames[decodedName] = true
			if entryInfo.IsDir() {
				entries = append(entries, serialEntry{cleanName: decodedName, isDirEntry: true})
				return nil
			}
			wimPath := entryPath
			imageFilesystem := imageFS
			entries = append(entries, serialEntry{
				cleanName: decodedName,
				perm:      entryPermissionsFromMode(entryInfo.Mode()),
				entrySize: entryInfo.Size(),
				open: func() (io.ReadCloser, error) {
					opened, openErr := imageFilesystem.Open(wimPath)
					if openErr != nil {
						return nil, fmt.Errorf("无法读取 wim 条目: %s", openErr.Error())
					}
					return opened, nil
				},
			})
			return nil
		})
		if walkErr != nil {
			return ExtractResult{}, fmt.Errorf("无法读取 wim 归档: %s", walkErr.Error())
		}
	}
	return serialContainerExtract(source, options, entries, logf)
}

// sanitizeEntryNameComponent 镜像名清洗：只留安全字符做目录名（业界多镜像命名惯例：
// 其 WIM 浏览按镜像列出；我们平铺进一棵树，命名不安全字符清洗）
func sanitizeEntryNameComponent(name string) string {
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || cleaned == "/" {
		return ""
	}
	return cleaned
}
