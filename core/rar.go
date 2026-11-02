// rar 读取管线（nwaples/rardecode v1.1.3，MIT 纯 Go；rar 创建法律禁止——本仓只读）。
// 串行解出：rar 为顺序流格式，业界惯例无并行机制。
// 文件源走 OpenReader：RAR 自家多卷命名（.part1.rar→.part2.rar、.rar→.r00）由库自动续卷；
// .001 拼接源退化为单卷流式。两遍走读（预扫全验 → 逐条落地）与 tar 管线同构：
// 路径安全/重名/GBK 名尽力修复/符号链接逃逸先全验后动工。加密档案明确报暂不支持。
package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/nwaples/rardecode"
)

// rarScannedEntry 预扫后的 rar 条目（第二遍按头重建同样字段）
type rarScannedEntry struct {
	cleanName  string
	isDirEntry bool
	perm       int64
	entrySize  int64
}

// openRarReader 开 rar 读流：文件源用 OpenReader（多卷续卷）；拼接源用 NewReader（单卷）
func openRarReader(source archiveSource, password string) (*rardecode.Reader, func(), error) {
	if source.joined == nil {
		readCloser, openErr := rardecode.OpenReader(source.displayPath, password)
		if openErr != nil {
			return nil, nil, openErr
		}
		return &readCloser.Reader, func() { readCloser.Close() }, nil
	}
	stream, streamErr := source.openStream()
	if streamErr != nil {
		return nil, nil, streamErr
	}
	reader, newErr := rardecode.NewReader(stream, password)
	if newErr != nil {
		stream.Close()
		return nil, nil, newErr
	}
	return reader, func() { stream.Close() }, nil
}

// rarEntryFromHeader 头 → 预扫条目：名字解码 + 路径安全（v1.1.3 头无加密/链接标记：
// 加密档案在读取时以错误浮出由 rarFriendlyError 映射；rar 符号链接按普通文件落地）
func rarEntryFromHeader(header *rardecode.FileHeader, logf Logf) (rarScannedEntry, error) {
	decodedName, converted := decodeEntryName(header.Name, false)
	if isUnsafeEntryPath(header.Name) || isUnsafeEntryPath(decodedName) {
		return rarScannedEntry{}, fmt.Errorf("归档含不安全路径: %s", header.Name)
	}
	if converted {
		reportLogf(logf, "条目名按 GBK 解码: %q -> %q", header.Name, decodedName)
	}
	cleanName := path.Clean(strings.ReplaceAll(decodedName, "\\", "/"))
	if cleanName == "." || cleanName == "" {
		return rarScannedEntry{cleanName: ""}, nil // 根占位条目：无实体
	}
	entry := rarScannedEntry{
		cleanName:  cleanName,
		isDirEntry: header.IsDir,
		perm:       int64(header.Mode().Perm()),
		entrySize:  header.UnPackedSize,
	}
	if entry.perm == 0 {
		entry.perm = 0o644
	}
	return entry, nil
}

// rarExtract rar 解压入口（core.Extract 的 rar 分支）：
// 预扫全验（Next 自动跳过数据体）→ 智能解压定根 → 第二遍逐条落地（流式，有界内存）
func rarExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	entryPassword := options.Password
	if options.PasswordResolver != nil {
		if resolved, hit := options.PasswordResolver("*"); hit {
			entryPassword = resolved
		}
	}
	reader, closeReader, openErr := openRarReader(source, entryPassword)
	if openErr != nil {
		return ExtractResult{}, fmt.Errorf("无法读取 rar 归档: %s", rarFriendlyError(openErr, entryPassword).Error())
	}
	var scanned []rarScannedEntry
	seenNames := map[string]bool{}
	prescanDegraded := false // 部分加密（混合密码）档案的预扫降级：流位置被错误密码文件污染
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			lowerMsg := strings.ToLower(nextErr.Error())
			if strings.Contains(lowerMsg, "password") || strings.Contains(lowerMsg, "decrypt") || strings.Contains(lowerMsg, "huffman") {
				prescanDegraded = true
				break // 已收条目仍可用；根目录退保守，逐条解出时各自的密码错误自然暴露
			}
			closeReader()
			return ExtractResult{}, fmt.Errorf("无法读取 rar 归档: %s", rarFriendlyError(nextErr, entryPassword).Error())
		}
		entry, entryErr := rarEntryFromHeader(header, logf)
		if entryErr != nil {
			closeReader()
			return ExtractResult{}, entryErr
		}
		if entry.cleanName == "" {
			continue
		}
		if !entry.isDirEntry && seenNames[entry.cleanName] {
			closeReader()
			return ExtractResult{}, fmt.Errorf("归档含重复条目名: %s", entry.cleanName)
		}
		seenNames[entry.cleanName] = true
		scanned = append(scanned, entry)
	}
	closeReader()
	if prescanDegraded {
		reportLogf(logf, "部分加密 rar：预扫降级，根目录按归档基名保守包裹")
	}

	rootEntries := make([]scannedEntry, 0, len(scanned))
	var totalFiles int64
	var totalBytes int64
	for _, entry := range scanned {
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
	reader2, closeReader2, reopenErr := openRarReader(source, entryPassword)
	if reopenErr != nil {
		stopProgress()
		return ExtractResult{}, fmt.Errorf("无法读取 rar 归档: %s", rarFriendlyError(reopenErr, entryPassword).Error())
	}
	extractErr := func() error {
		defer closeReader2()
		for {
			header, nextErr := reader2.Next()
			if nextErr == io.EOF {
				return nil
			}
			if nextErr != nil {
				// 降级档案的密码类错误：已尽所能，优雅收尾（混合密码已知限制）
				if prescanDegraded {
					lowerMsg := strings.ToLower(nextErr.Error())
					if strings.Contains(lowerMsg, "password") || strings.Contains(lowerMsg, "decrypt") {
						return nil
					}
				}
				return fmt.Errorf("无法读取 rar 归档: %s", rarFriendlyError(nextErr, entryPassword).Error())
			}
			entry, entryErr := rarEntryFromHeader(header, nil)
			if entryErr != nil {
				return entryErr // 预扫已全验，此处触发即两遍之间档案被改动
			}
			if entry.cleanName == "" {
				continue
			}
			targetPath := filepath.Join(absoluteTargetRoot, filepath.FromSlash(entry.cleanName))
			switch {
			case entry.isDirEntry:
				if mkdirErr := os.MkdirAll(targetPath, 0o755); mkdirErr != nil {
					return mkdirErr
				}
			default:
				skippedFile, writeErr := tarWriteOutFile(reader2, targetPath, entry.perm, options.Force, &skippedFiles, logf)
				if writeErr != nil {
					return fmt.Errorf("写出 %s 失败: %s", entry.cleanName, writeErr.Error())
				}
				if !skippedFile {
					progressState.doneFiles.Add(1)
					progressState.doneBytes.Add(entry.entrySize)
				}
			}
		}
	}()
	stopProgress()
	if extractErr != nil {
		return ExtractResult{}, fmt.Errorf("解压失败: %s", extractErr.Error())
	}

	return ExtractResult{
		Output:    absoluteTargetRoot,
		FileCount: int(progressState.doneFiles.Load()),
		SizeBytes: progressState.doneBytes.Load(),
		Skipped:   int(skippedFiles.Load()),
	}, nil
}

// rarFriendlyError 加密档案（缺密码）给出明确文案；其余原样透传。
// options.Password 为空 → 需要密码；非空 → 密码错误
func rarFriendlyError(err error, password string) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "password") || strings.Contains(message, "decrypt") {
		if password == "" {
			return errors.New("归档已加密（rar）：需要 --password 提供密码")
		}
		return ErrWrongPassword
	}
	return err
}
