// iso（ISO9660）读取：kdomanski/iso9660（MIT 纯 Go）——随机访问（ReaderAt），
// Rock Ridge 名自动生效（Name() 内建 SUSP 优先）；Joliet 卷暂按 ISO9660 层读名
// （GBK 尽力解码兜底，局限记录）。复用 serialContainerExtract 骨架。
package core

import (
	"fmt"
	"io"

	"github.com/kdomanski/iso9660"
)

// ISO_IDENT_OFFSET / ISO_IDENT ISO9660 卷描述符标识位置："CD001" @ 32769（0x8001）
const (
	ISO_IDENT_OFFSET int64  = 32769
	ISO_IDENT        string = "CD001"
)

// isoExtract iso 解压入口（core.Extract 的 iso 分支）
func isoExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	image, imageErr := iso9660.OpenImage(source.readerAt())
	if imageErr != nil {
		return ExtractResult{}, fmt.Errorf("无法读取 iso 归档: %s", imageErr.Error())
	}
	rootDir, rootErr := image.RootDir()
	if rootErr != nil {
		return ExtractResult{}, fmt.Errorf("无法读取 iso 归档: %s", rootErr.Error())
	}

	var entries []serialEntry
	seenNames := map[string]bool{}
	var walkErr error
	var walk func(prefix string, dir *iso9660.File)
	walk = func(prefix string, dir *iso9660.File) {
		if walkErr != nil {
			return
		}
		children, childrenErr := dir.GetChildren()
		if childrenErr != nil {
			walkErr = fmt.Errorf("无法读取 iso 归档: %s", childrenErr.Error())
			return
		}
		for _, child := range children {
			entryName := child.Name()
			if entryName == "" || entryName == "." || entryName == ".." || entryName == "\x00" {
				continue // 目录占位项
			}
			cleanName := prefix + entryName
			decodedName, converted := decodeEntryName(cleanName, false)
			if isUnsafeEntryPath(cleanName) || isUnsafeEntryPath(decodedName) {
				walkErr = fmt.Errorf("归档含不安全路径: %s", cleanName)
				return
			}
			if converted {
				reportLogf(logf, "条目名按 GBK 解码: %q -> %q", cleanName, decodedName)
			}
			if seenNames[decodedName] {
				walkErr = fmt.Errorf("归档含重复条目名: %s", decodedName)
				return
			}
			seenNames[decodedName] = true
			if child.IsDir() {
				entries = append(entries, serialEntry{cleanName: decodedName, isDirEntry: true})
				walk(decodedName+"/", child)
				continue
			}
			isoFile := child
			entries = append(entries, serialEntry{
				cleanName: decodedName,
				perm:      entryPermissionsFromMode(child.Mode()),
				entrySize: child.Size(),
				open: func() (io.ReadCloser, error) {
					// Reader() 每次调用新建 SectionReader，open 幂等
					return io.NopCloser(isoFile.Reader()), nil
				},
			})
		}
	}
	walk("", rootDir)
	if walkErr != nil {
		return ExtractResult{}, walkErr
	}
	return serialContainerExtract(source, options, entries, logf)
}
