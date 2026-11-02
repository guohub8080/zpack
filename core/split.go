// .001 顺序分卷：按 .001/.002/.003… 逐段发现并拼接成逻辑单文件（joinedSegments），
// 内层格式经魔数自动识别（zip/tar/…），非归档内层则还原为单文件。这同时是 zip 分卷读取
// （路线图「读取硬化」的 .zip.001 串联项）的实现底座。
package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SPLIT_FIRST_SUFFIX 分卷首卷后缀（顺序切分约定：base.001 起，逐段递增到缺号为止）
const SPLIT_FIRST_SUFFIX = ".001"

// isSplitFirstPath 路径是否像顺序分卷首卷（base.001，大小写不敏感）
func isSplitFirstPath(archivePath string) bool {
	return strings.HasSuffix(strings.ToLower(filepath.Base(archivePath)), SPLIT_FIRST_SUFFIX)
}

// joinedSegments 顺序分卷拼接视图：多段文件的逻辑单文件（ReaderAt + 顺序流 + 总大小）
type joinedSegments struct {
	segmentPaths []string
	segmentSizes []int64
	offsets      []int64 // 每段在逻辑流中的起始偏移（0 起）
	totalSize    int64
}

// discoverSplitSegments 从首卷路径逐段探测 .001/.002/.003… 直到缺号；只含首卷本身也成立
// （单段的 .001 可能是独立切分产物，内层识别交给后续魔数嗅探）
func discoverSplitSegments(firstVolumePath string) (*joinedSegments, error) {
	base := archivePathTrimSplitSuffix(firstVolumePath)
	var segmentPaths []string
	for volumeIdx := 1; ; volumeIdx++ {
		volumeName := fmt.Sprintf("%s.%03d", base, volumeIdx)
		fileInfo, statErr := os.Stat(volumeName)
		if statErr != nil {
			break // 缺号即终止（允许只有 .001 一段）
		}
		if fileInfo.IsDir() {
			return nil, fmt.Errorf("分卷段是目录而非文件: %s", volumeName)
		}
		segmentPaths = append(segmentPaths, volumeName)
	}
	if len(segmentPaths) == 0 {
		return nil, fmt.Errorf("分卷首卷不存在: %s", firstVolumePath)
	}
	segments := &joinedSegments{segmentPaths: segmentPaths}
	for _, segmentPath := range segmentPaths {
		fileInfo, statErr := os.Stat(segmentPath)
		if statErr != nil {
			return nil, fmt.Errorf("无法读取分卷段: %s", statErr.Error())
		}
		segments.offsets = append(segments.offsets, segments.totalSize)
		segments.segmentSizes = append(segments.segmentSizes, fileInfo.Size())
		segments.totalSize += fileInfo.Size()
	}
	return segments, nil
}

// archivePathTrimSplitSuffix 去掉 .001 后缀得到分卷基名
func archivePathTrimSplitSuffix(archivePath string) string {
	return archivePath[:len(archivePath)-len(SPLIT_FIRST_SUFFIX)]
}

// stripSplitSuffix 名字若以 .001 结尾则剥掉（分卷命名归一到内层名，供输出文件/包层根目录命名）
func stripSplitSuffix(name string) string {
	if isSplitFirstPath(name) {
		return archivePathTrimSplitSuffix(name)
	}
	return name
}

// ReadAt 逻辑流定点读：定位偏移所在段后转发（可能跨段拼接）
func (segments *joinedSegments) ReadAt(buffer []byte, offset int64) (int, error) {
	if offset >= segments.totalSize {
		return 0, io.EOF
	}
	totalRead := 0
	for totalRead < len(buffer) {
		segmentIdx := segments.locateSegment(offset)
		if segmentIdx < 0 {
			return totalRead, io.EOF
		}
		segmentFile, openErr := os.Open(segments.segmentPaths[segmentIdx])
		if openErr != nil {
			return totalRead, openErr
		}
		inSegmentOffset := offset - segments.offsets[segmentIdx]
		chunkLen := int64(len(buffer) - totalRead)
		if remaining := segments.segmentSizes[segmentIdx] - inSegmentOffset; remaining < chunkLen {
			chunkLen = remaining
		}
		readLen, readErr := segmentFile.ReadAt(buffer[totalRead:totalRead+int(chunkLen)], inSegmentOffset)
		segmentFile.Close()
		totalRead += readLen
		offset += int64(readLen)
		if readErr != nil || readLen == 0 {
			return totalRead, readErr
		}
	}
	return totalRead, nil
}

// locateSegment 偏移落在哪一段（段数小，线性查找够用；-1 = 越界）
func (segments *joinedSegments) locateSegment(offset int64) int {
	for segmentIdx := len(segments.segmentPaths) - 1; segmentIdx >= 0; segmentIdx-- {
		if offset >= segments.offsets[segmentIdx] {
			return segmentIdx
		}
	}
	return -1
}

// segmentReader 单段顺序读流
type segmentReader struct {
	segments *joinedSegments
	file     *os.File
	nextIdx  int
}

// Read 顺序读：当前段读尽自动切下一段
func (reader *segmentReader) Read(buffer []byte) (int, error) {
	for {
		if reader.file == nil {
			if reader.nextIdx >= len(reader.segments.segmentPaths) {
				return 0, io.EOF
			}
			opened, openErr := os.Open(reader.segments.segmentPaths[reader.nextIdx])
			if openErr != nil {
				return 0, openErr
			}
			reader.file = opened
			reader.nextIdx++
		}
		readLen, readErr := reader.file.Read(buffer)
		if readLen > 0 {
			return readLen, nil
		}
		if readErr == io.EOF {
			reader.file.Close()
			reader.file = nil
			continue // 切下一段
		}
		if readErr != nil {
			return readLen, readErr
		}
		// readLen == 0 且无错误：按 POSIX 语义重试同一段
	}
}

// Close 关闭当前段
func (reader *segmentReader) Close() error {
	if reader.file != nil {
		return reader.file.Close()
	}
	return nil
}

// openStream 打开拼接顺序流
func (segments *joinedSegments) openStream() (io.ReadCloser, error) {
	return &segmentReader{segments: segments}, nil
}

// close 预留：段文件按需开关，无常驻句柄
func (segments *joinedSegments) close() {}
