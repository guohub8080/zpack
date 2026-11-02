// 7z 读取管线（bodgit/sevenzip，MIT 纯 Go）：随机访问（ReaderAt）——普通文件与 .001 拼接源
// 皆可；预扫全验（路径安全/重名/GBK 名尽力修复）→ **并行解出**（// 文件级并行对齐；rar 是顺序流无并行机制——串行即对齐）。加密档案（头加密/流加密缺密码）
// 明确报「暂不支持加密 7z」。
package core

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/bodgit/sevenzip"
)

// sevenZipExtract 7z 解压入口（core.Extract 的 7z 分支）
func sevenZipExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	entryPassword := options.Password
	if options.PasswordResolver != nil {
		// 7z 头加密场景整档一个密码：用 * 通配命中（或任意文件名命中即取第一个）
		if resolved, hit := options.PasswordResolver("*"); hit {
			entryPassword = resolved
		}
	}
	var reader *sevenzip.Reader
	var openErr error
	if entryPassword != "" {
		reader, openErr = sevenzip.NewReaderWithPassword(source.readerAt(), source.size(), entryPassword)
	} else {
		reader, openErr = sevenzip.NewReader(source.readerAt(), source.size())
	}
	if openErr != nil {
		return ExtractResult{}, fmt.Errorf("无法读取 7z 归档: %s", sevenZipFriendlyError(openErr).Error())
	}

	var entries []serialEntry
	seenNames := map[string]bool{}
	for _, archiveFile := range reader.File {
		header := &archiveFile.FileHeader
		decodedName, converted := decodeEntryName(header.Name, false)
		if isUnsafeEntryPath(header.Name) || isUnsafeEntryPath(decodedName) {
			return ExtractResult{}, fmt.Errorf("归档含不安全路径: %s", header.Name)
		}
		if converted {
			reportLogf(logf, "条目名按 GBK 解码: %q -> %q", header.Name, decodedName)
		}
		cleanName := path.Clean(strings.ReplaceAll(decodedName, "\\", "/"))
		if cleanName == "." || cleanName == "" {
			continue // 根占位条目：无实体
		}
		isDirEntry := header.FileInfo().IsDir()
		if !isDirEntry && seenNames[cleanName] {
			return ExtractResult{}, fmt.Errorf("归档含重复条目名: %s", cleanName)
		}
		seenNames[cleanName] = true

		entry := serialEntry{
			cleanName:  cleanName,
			isDirEntry: isDirEntry,
			perm:       entryPermissionsFromMode(header.FileInfo().Mode()),
			entrySize:  int64(header.UncompressedSize),
		}
		if !isDirEntry {
			sevenFile := archiveFile
			password := options.Password
			entry.open = func() (io.ReadCloser, error) {
				opened, openErr := sevenFile.Open()
				if openErr != nil {
					return nil, sevenZipFriendlyError(openErr)
				}
				_ = password
				return opened, nil
			}
		}
		entries = append(entries, entry)
	}
	// 并行解出（zip/7z 文件级并行；bodgit 契约
	// "Multiple files may be read concurrently"——pool 池化 solid 文件夹解码器）
	return parallelContainerExtract(source, options, entries, options.Jobs, logf)
}

// sevenZipFriendlyError 加密档案（缺密码）给出明确文案；其余原样透传
func sevenZipFriendlyError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "password") {
		return errors.New("暂不支持加密 7z：需要密码的档案暂未实现")
	}
	return err
}

// sevenzipReader 类型别名（list.go 用）
type sevenzipReader = sevenzip.Reader

// sevenzipOpen 桥接（list.go 用）
func sevenzipOpen(source archiveSource) (*sevenzip.Reader, error) {
	return sevenzip.NewReader(source.readerAt(), source.size())
}
