// asar（Electron 应用归档）读取：8 字节长度头 + JSON 目录 + 数据区连续排布。
// 布局：[u32 头块大小 H][u32 JSON 长 J][JSON J 字节][补齐 (H-4-J)][数据区 @ 8+H]。
// unpacked 条目（无 offset，外置同目录 .asar.unpacked/ 兄弟目录）从兄弟目录读。
package core

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// ASAR_HEADER_PROBE_LEN 头部探测长度（两 u32 + JSON 起始字节）
const ASAR_HEADER_PROBE_LEN = 16

// asarFileHeader asar 目录树的节点（文件带 offset/size；目录带 files）
type asarFileHeader struct {
	Size     int64                      `json:"size"`
	Offset   string                     `json:"offset"`
	Unpacked bool                       `json:"unpacked"`
	Files    map[string]*asarFileHeader `json:"files"`
}

// asarScanEntries 解析目录 JSON → 扁平条目清单（offset 相对数据区起点）
func asarScanEntries(source archiveSource) (dataStart int64, entries []serialEntry, scanErr error) {
	// 真实布局（Electron 双 pickle）：[u32=4][u32 H][u32 J][JSON J][pad][数据 @ 8+H]
	probeBytes := make([]byte, ASAR_HEADER_PROBE_LEN)
	if _, probeErr := source.readAt(probeBytes, 0); probeErr != nil {
		return 0, nil, fmt.Errorf("无法读取 asar 归档: %s", probeErr.Error())
	}
	if leUint32(probeBytes[0:4]) != 4 {
		return 0, nil, fmt.Errorf("无法读取 asar 归档: 外层 pickle 长度不符")
	}
	headerSize := int64(leUint32(probeBytes[4:8]))
	jsonSize := int64(leUint32(probeBytes[8:12]))
	if headerSize < 4 || jsonSize <= 0 || jsonSize > headerSize || 8+headerSize > source.size() {
		return 0, nil, fmt.Errorf("无法读取 asar 归档: 头部长度非法")
	}
	jsonBytes := make([]byte, jsonSize)
	if _, jsonErr := source.readAt(jsonBytes, 12); jsonErr != nil {
		return 0, nil, fmt.Errorf("无法读取 asar 归档: %s", jsonErr.Error())
	}
	var root asarFileHeader
	if err := json.Unmarshal(jsonBytes, &root); err != nil {
		return 0, nil, fmt.Errorf("无法读取 asar 归档: 目录 JSON 非法: %s", err.Error())
	}
	dataStart = 8 + headerSize
	unpackedBase := source.displayPath + ".unpacked"
	var walkErr error
	var walk func(prefix string, files map[string]*asarFileHeader)
	seen := map[string]bool{}
	walk = func(prefix string, files map[string]*asarFileHeader) {
		if walkErr != nil || files == nil {
			return
		}
		for entryName, node := range files {
			cleanName := prefix + entryName
			if isUnsafeEntryPath(cleanName) {
				walkErr = fmt.Errorf("归档含不安全路径: %s", cleanName)
				return
			}
			if node.Files != nil { // 目录：递归
				walk(cleanName+"/", node.Files)
				continue
			}
			if seen[cleanName] {
				walkErr = fmt.Errorf("归档含重复条目名: %s", cleanName)
				return
			}
			seen[cleanName] = true
			nodeSize, nodeOffset := node.Size, node.Offset
			nodeUnpacked := node.Unpacked || node.Offset == ""
			entries = append(entries, serialEntry{
				cleanName: cleanName,
				perm:      0o644,
				entrySize: nodeSize,
				open: func() (io.ReadCloser, error) {
					if nodeUnpacked {
						unpackedPath := filepath.Join(unpackedBase, filepath.FromSlash(cleanName))
						opened, openErr := os.Open(unpackedPath)
						if openErr != nil {
							return nil, fmt.Errorf("unpacked 条目缺失: %s", unpackedPath)
						}
						return opened, nil
					}
					offsetValue, parseErr := strconv.ParseInt(nodeOffset, 10, 64)
					if parseErr != nil {
						return nil, fmt.Errorf("无法读取 asar 归档: 条目偏移非法: %s", cleanName)
					}
					return io.NopCloser(io.NewSectionReader(source.readerAt(), dataStart+offsetValue, nodeSize)), nil
				},
			})
		}
	}
	walk("", root.Files)
	if walkErr != nil {
		return 0, nil, walkErr
	}
	return dataStart, entries, nil
}

// asarExtract asar 解压入口（core.Extract 的 asar 分支，.asar 扩展名门控后进入）
func asarExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	_, entries, scanErr := asarScanEntries(source)
	if scanErr != nil {
		return ExtractResult{}, scanErr
	}
	return serialContainerExtract(source, options, entries, logf)
}

// leUint32 小端 u32
func leUint32(bytesValue []byte) uint32 {
	return uint32(bytesValue[0]) | uint32(bytesValue[1])<<8 | uint32(bytesValue[2])<<16 | uint32(bytesValue[3])<<24
}

// sniffAsarByHeader .asar 扩展名门控后的头结构试解：u32/u32 长度合法且 JSON 以 { 起
func sniffAsarByHeader(source archiveSource) bool {
	_, _, scanErr := asarScanEntries(source)
	return scanErr == nil
}
