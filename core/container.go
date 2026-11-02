// 随机访问容器（7z/rar）的通用串行解出骨架：各格式完成各自预扫（名字解码/安全全验/
// 条目构造）后交给这里——智能解压定根 → 逐条落地（复用 tar 的 O_EXCL 跳过/force 语义
// 与符号链接落地器）。并行化解出是后续性能项（先正确后快）。
package core

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
)

// serialEntry 随机访问容器的一个条目（open 每次给出新读流；符号链接不设 open）
type serialEntry struct {
	cleanName  string
	isDirEntry bool
	isSymlink  bool
	linkTarget string
	perm       int64
	entrySize  int64
	open       func() (io.ReadCloser, error)
}

// serialContainerExtract 串行逐条解出（进度/跳过/结果统计与 tar 管线同构）
func serialContainerExtract(source archiveSource, options ExtractOptions, entries []serialEntry, logf Logf) (ExtractResult, error) {
	rootEntries := make([]scannedEntry, 0, len(entries))
	var totalFiles int64
	var totalBytes int64
	for _, entry := range entries {
		rootEntries = append(rootEntries, scannedEntry{cleanName: entry.cleanName, isDirEntry: entry.isDirEntry})
		if !entry.isDirEntry {
			totalFiles++
			totalBytes += entry.entrySize
		}
	}

	absoluteTargetRoot, rootErr := prepareExtractRoot(options, source.displayPath, rootEntries)
	if rootErr != nil {
		return ExtractResult{}, rootErr
	}

	progressState := newProgressReporter(totalFiles, totalBytes, logf)
	stopProgress := progressState.start("解压中")
	var skippedFiles atomic.Int64
	for _, entry := range entries {
		targetPath := filepath.Join(absoluteTargetRoot, filepath.FromSlash(entry.cleanName))
		switch {
		case entry.isDirEntry:
			if mkdirErr := os.MkdirAll(targetPath, 0o755); mkdirErr != nil {
				stopProgress()
				return ExtractResult{}, fmt.Errorf("解压失败: %s", mkdirErr.Error())
			}
		case entry.isSymlink:
			skippedLink, linkErr := tarCreateSymlink(targetPath, entry.linkTarget, options.Force, &skippedFiles, logf)
			if linkErr != nil {
				stopProgress()
				return ExtractResult{}, fmt.Errorf("解压失败: 写出符号链接 %s 失败: %s", entry.cleanName, linkErr.Error())
			}
			if !skippedLink {
				progressState.doneFiles.Add(1)
			}
		default:
			entryReader, openErr := entry.open()
			if openErr != nil {
				stopProgress()
				return ExtractResult{}, fmt.Errorf("解压失败: 写出 %s 失败: %s", entry.cleanName, openErr.Error())
			}
			skippedFile, writeErr := tarWriteOutFile(entryReader, targetPath, entry.perm, options.Force, &skippedFiles, logf)
			entryReader.Close()
			if writeErr != nil {
				stopProgress()
				return ExtractResult{}, fmt.Errorf("解压失败: 写出 %s 失败: %s", entry.cleanName, writeErr.Error())
			}
			if !skippedFile {
				progressState.doneFiles.Add(1)
				progressState.doneBytes.Add(entry.entrySize)
			}
		}
	}
	stopProgress()

	return ExtractResult{
		Output:    absoluteTargetRoot,
		FileCount: int(progressState.doneFiles.Load()),
		SizeBytes: progressState.doneBytes.Load(),
		Skipped:   int(skippedFiles.Load()),
	}, nil
}

// entryPermissionsFromMode 从 fs.FileMode 取 perm（0 值归一到 0644，与 zip/tar 条目同语义）
func entryPermissionsFromMode(mode fs.FileMode) int64 {
	perm := int64(mode.Perm())
	if perm == 0 {
		return 0o644
	}
	return perm
}
