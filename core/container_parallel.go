// 并行容器解出骨架（7z 这类随机访问容器）：文件级并行——主流压缩器同构的
// CArkParaExtMan 模型（二进制逆向证据：SParaExtContext[32] 线程上下文、每线程独立解
// 一组文件、事件回报进度/错误、失败即停投喂）。zip 管线（zipExtract）是其同构实现。
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// parallelContainerExtract 并行逐条解出：worker 池消费 serialEntry 列表；
// 首错即取消投喂（已开工条目自然收尾），跳过计数原子，进度与串行骨架同构
func parallelContainerExtract(source archiveSource, options ExtractOptions, entries []serialEntry, jobs int, logf Logf) (ExtractResult, error) {
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

	// 与 zip 管线同参数：min(max(jobs,1), 32)，且不超过条目数
	workerCount := min(max(jobs, 1), ZIP_EXTRACT_MAX_WORKERS)
	if workerCount > len(entries) {
		workerCount = len(entries)
	}
	taskChannel := make(chan serialEntry, workerCount)
	var workerGroup sync.WaitGroup
	var firstErrorMutex sync.Mutex
	var firstError error
	setFirstError := func(extractErr error) {
		firstErrorMutex.Lock()
		defer firstErrorMutex.Unlock()
		if firstError == nil {
			firstError = extractErr
		}
	}
	cancelChannel := make(chan struct{})
	worker := func() {
		defer workerGroup.Done()
		for {
			select {
			case <-cancelChannel:
				return
			default:
			}
			entry, channelOk := <-taskChannel
			if !channelOk {
				return
			}
			targetPath := filepath.Join(absoluteTargetRoot, filepath.FromSlash(entry.cleanName))
			switch {
			case entry.isDirEntry:
				if mkdirErr := os.MkdirAll(targetPath, 0o755); mkdirErr != nil {
					setFirstError(fmt.Errorf("写出 %s 失败: %s", entry.cleanName, mkdirErr.Error()))
					close(cancelChannel)
					return
				}
			case entry.isSymlink:
				skippedLink, linkErr := tarCreateSymlink(targetPath, entry.linkTarget, options.Force, &skippedFiles, logf)
				if linkErr != nil {
					setFirstError(fmt.Errorf("写出符号链接 %s 失败: %s", entry.cleanName, linkErr.Error()))
					close(cancelChannel)
					return
				}
				if !skippedLink {
					progressState.doneFiles.Add(1)
				}
			default:
				entryReader, openErr := entry.open()
				if openErr != nil {
					setFirstError(fmt.Errorf("写出 %s 失败: %s", entry.cleanName, openErr.Error()))
					close(cancelChannel)
					return
				}
				skippedFile, writeErr := tarWriteOutFile(entryReader, targetPath, entry.perm, options.Force, &skippedFiles, logf)
				entryReader.Close()
				if writeErr != nil {
					setFirstError(fmt.Errorf("写出 %s 失败: %s", entry.cleanName, writeErr.Error()))
					close(cancelChannel)
					return
				}
				if !skippedFile {
					progressState.doneFiles.Add(1)
					progressState.doneBytes.Add(entry.entrySize)
				}
			}
		}
	}
	for workerIdx := 0; workerIdx < workerCount; workerIdx++ {
		workerGroup.Add(1)
		go worker()
	}
	for _, entry := range entries {
		select {
		case taskChannel <- entry:
		case <-cancelChannel:
			// 已有 worker 失败：停止投喂（剩余条目不再写）
		}
	}
	close(taskChannel)
	workerGroup.Wait()
	stopProgress()
	if firstError != nil {
		return ExtractResult{}, fmt.Errorf("解压失败: %s", firstError.Error())
	}

	return ExtractResult{
		Output:    absoluteTargetRoot,
		FileCount: int(progressState.doneFiles.Load()),
		SizeBytes: progressState.doneBytes.Load(),
		Skipped:   int(skippedFiles.Load()),
	}, nil
}
