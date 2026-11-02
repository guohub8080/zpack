// ar（Unix 归档，.a/.ar 的内层）读取：GNU 与 BSD 两系命名——GNU "name/" 尾斜杠、
// "//" 长名表、"/N" 索引；BSD "#1/len" 数据前缀名；"/" 符号表条目跳过。
// 条目全部由绝对偏移定位：预扫一遍头（纯 ReadAt），落地用 SectionReader 直读
// （随机访问，普通文件与分卷拼接源皆可），复用 serialContainerExtract 骨架。
package core

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// AR_MAGIC / AR_HEADER_LEN ar 格式常量
const (
	AR_MAGIC      = "!<arch>\n"
	AR_HEADER_LEN = 60
)

// arScannedEntry 预扫后的 ar 条目：数据绝对偏移 + 尺寸/权限
type arScannedEntry struct {
	cleanName  string
	perm       int64
	entrySize  int64
	dataOffset int64
}

// arScanEntries 纯 ReadAt 走读全部条目头（数据不读只算偏移）
func arScanEntries(source archiveSource) ([]arScannedEntry, error) {
	readerAt := source.readerAt()
	totalSize := source.size()
	magicBytes := make([]byte, len(AR_MAGIC))
	if _, magicErr := readerAt.ReadAt(magicBytes, 0); magicErr != nil || string(magicBytes) != AR_MAGIC {
		return nil, fmt.Errorf("无法读取 ar 归档: 魔数不符")
	}
	longNames := ""
	var entries []arScannedEntry
	headerBytes := make([]byte, AR_HEADER_LEN)
	offset := int64(len(AR_MAGIC))
	for offset+AR_HEADER_LEN <= totalSize {
		if _, headerErr := readerAt.ReadAt(headerBytes, offset); headerErr != nil {
			return nil, fmt.Errorf("无法读取 ar 归档: %s", headerErr.Error())
		}
		if string(headerBytes[58:60]) != "`\n" {
			return nil, fmt.Errorf("无法读取 ar 归档: 条目头边界符不符")
		}
		rawName := strings.TrimRight(string(headerBytes[:16]), " ")
		sizeValue, sizeErr := strconv.ParseInt(strings.TrimSpace(string(headerBytes[48:58])), 10, 64)
		if sizeErr != nil || sizeValue < 0 {
			return nil, fmt.Errorf("无法读取 ar 归档: 条目尺寸非法")
		}
		permValue, permErr := strconv.ParseInt(strings.TrimSpace(string(headerBytes[40:48])), 8, 64)
		if permErr != nil {
			permValue = 0o644
		}
		dataStart := offset + AR_HEADER_LEN
		var nameLen int64
		cleanName := ""
		switch {
		case rawName == "/":
			// 符号表：非文件内容，不产出条目
		case rawName == "//" || rawName == "/ARFILENAMES/":
			tableBytes := make([]byte, sizeValue)
			if _, tableErr := readerAt.ReadAt(tableBytes, dataStart); tableErr != nil {
				return nil, fmt.Errorf("无法读取 ar 归档: %s", tableErr.Error())
			}
			longNames = string(tableBytes)
		case strings.HasPrefix(rawName, "#1/"):
			parsedLen, lenErr := strconv.ParseInt(rawName[3:], 10, 64)
			if lenErr != nil || parsedLen > sizeValue {
				return nil, fmt.Errorf("无法读取 ar 归档: BSD 名前缀非法")
			}
			nameLen = parsedLen
			nameBytes := make([]byte, nameLen)
			if _, nameErr := readerAt.ReadAt(nameBytes, dataStart); nameErr != nil {
				return nil, fmt.Errorf("无法读取 ar 归档: %s", nameErr.Error())
			}
			cleanName = strings.TrimRight(strings.TrimRight(string(bytes.TrimRight(nameBytes, "\x00")), " "), "/")
		case strings.HasPrefix(rawName, "/"):
			nameIdx, idxErr := strconv.ParseInt(rawName[1:], 10, 64)
			if idxErr != nil || nameIdx >= int64(len(longNames)) {
				return nil, fmt.Errorf("无法读取 ar 归档: 长名索引越界")
			}
			nameEnd := strings.IndexByte(longNames[nameIdx:], '\n')
			if nameEnd < 0 {
				nameEnd = len(longNames) - int(nameIdx)
			}
			cleanName = strings.TrimSuffix(longNames[nameIdx:int(nameIdx)+nameEnd], "/")
		default:
			cleanName = strings.TrimSuffix(rawName, "/")
		}
		// 数据区起点：BSD 的名字前缀属数据区，条目实际内容在其后
		entryDataOffset := dataStart
		entrySize := sizeValue
		if nameLen > 0 {
			entryDataOffset += nameLen
			entrySize -= nameLen
		}
		if cleanName != "" && cleanName != "." && entrySize >= 0 {
			entries = append(entries, arScannedEntry{
				cleanName:  cleanName,
				perm:       permValue,
				entrySize:  entrySize,
				dataOffset: entryDataOffset,
			})
		}
		// 推进到下一头：数据区 + 偶数对齐
		skipTotal := sizeValue
		if skipTotal%2 == 1 {
			skipTotal++
		}
		offset = dataStart + skipTotal
	}
	return entries, nil
}

// arExtract ar 解压入口（core.Extract 的 ar 分支）：预扫全验 → 串行落地（SectionReader 直读）
func arExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	scanned, scanErr := arScanEntries(source)
	if scanErr != nil {
		return ExtractResult{}, scanErr
	}
	seenNames := map[string]bool{}
	var entries []serialEntry
	for _, arEntry := range scanned {
		decodedName, converted := decodeEntryName(arEntry.cleanName, false)
		if isUnsafeEntryPath(arEntry.cleanName) || isUnsafeEntryPath(decodedName) {
			return ExtractResult{}, fmt.Errorf("归档含不安全路径: %s", arEntry.cleanName)
		}
		if converted {
			reportLogf(logf, "条目名按 GBK 解码: %q -> %q", arEntry.cleanName, decodedName)
		}
		if seenNames[decodedName] {
			return ExtractResult{}, fmt.Errorf("归档含重复条目名: %s", decodedName)
		}
		seenNames[decodedName] = true
		permValue := arEntry.perm
		if permValue == 0 {
			permValue = 0o644
		}
		entryOffset, entryLength := arEntry.dataOffset, arEntry.entrySize
		entries = append(entries, serialEntry{
			cleanName: decodedName,
			perm:      permValue,
			entrySize: entryLength,
			open: func() (io.ReadCloser, error) {
				return io.NopCloser(io.NewSectionReader(source.readerAt(), entryOffset, entryLength)), nil
			},
		})
	}
	return serialContainerExtract(source, options, entries, logf)
}
