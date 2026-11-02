// 归档内容列表 / 完整性验证 / 元数据信息——读侧解析器的轻量封装（不解压不写盘）。
// list：逐条列出归档内容（名/尺寸/加密/目录标记）
// test：逐条解压到内存验 CRC（不写盘），报告损坏条目
// info：聚合元数据（格式/条目数/总大小/压缩比/加密状态）
package core

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// ArchiveEntry 归档条目（列表输出用）
type ArchiveEntry struct {
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	CompressedSize int64  `json:"compressed_size"`
	IsDir          bool   `json:"is_dir"`
	Encrypted      bool   `json:"encrypted"`
}

// ArchiveInfo 归档元数据（info 命令输出）
type ArchiveInfo struct {
	Format            string         `json:"format"`
	Comment           string         `json:"comment,omitempty"`
	EntryCount        int            `json:"entry_count"`
	FileCount         int            `json:"file_count"`
	DirCount          int            `json:"dir_count"`
	TotalUncompressed int64          `json:"total_uncompressed"`
	TotalCompressed   int64          `json:"total_compressed"`
	ArchiveSize       int64          `json:"archive_size"`
	CompressionRatio  float64        `json:"compression_ratio"`
	Encrypted         bool           `json:"encrypted"`
	EntryList         []ArchiveEntry `json:"entries,omitempty"`
}

// ListArchive 列出归档内容（支持 zip/tar系/7z/rar）
func ListArchive(archivePath string) (*ArchiveInfo, error) {
	source, sourceErr := openExtractSource(archivePath)
	if sourceErr != nil {
		return nil, sourceErr
	}
	defer source.close()

	format, sniffErr := sniffSource(source)
	if sniffErr != nil {
		return nil, sniffErr
	}

	var entries []ArchiveEntry
	switch format {
	case FormatZip:
		entries = listZip(source)
	case FormatTar, FormatTarGz, FormatTarXz, FormatTarBz2, FormatTarZst:
		entries = listTar(source, format)
	case Format7z:
		entries = list7z(source)
	case FormatRar:
		entries = listRar(source)
	default:
		return nil, fmt.Errorf("暂不支持列出 %s 格式内容", formatName(format))
	}
	return buildArchiveInfo(format, entries, archivePath), nil
}

// TestArchive 验证归档完整性：逐条解压到内存验 CRC（不写盘）
func TestArchive(archivePath string) (*ArchiveInfo, []string, error) {
	info, infoErr := ListArchive(archivePath)
	if infoErr != nil {
		return nil, nil, infoErr
	}

	var failures []string
	switch {
	case info.Format == "zip":
		failures = testZip(archivePath)
	case strings.HasPrefix(info.Format, "tar"):
		failures = testTar(archivePath)
	default:
		// 其他格式：结构解析成功即通过（CRC 逐条验证后续按格式补充）
		if len(info.EntryList) == 0 && info.FileCount == 0 {
			failures = append(failures, "归档为空或无法解析")
		}
	}
	return info, failures, nil
}

// ─── 内部实现 ───

func listZip(source archiveSource) []ArchiveEntry {
	reader, err := zip.NewReader(source.readerAt(), source.size())
	if err != nil {
		return nil
	}
	zipComment = reader.Comment // 存到包级变量供 buildArchiveInfo 读取
	var entries []ArchiveEntry
	for _, f := range reader.File {
		name := f.Name
		cleanName := path.Clean(strings.ReplaceAll(name, "\\", "/"))
		isDir := f.FileInfo().IsDir() || strings.HasSuffix(name, "/")
		entries = append(entries, ArchiveEntry{
			Name:           cleanName,
			Size:           int64(f.UncompressedSize64),
			CompressedSize: int64(f.CompressedSize64),
			IsDir:          isDir,
			Encrypted:      f.Flags&0x1 != 0,
		})
	}
	return entries
}

func listTar(source archiveSource, format Format) []ArchiveEntry {
	compression := tarCompressionDecoder(format)
	if compression == nil && format != FormatTar {
		return nil
	}
	tarReader, closer, err := openTarStream(source, compression)
	if err != nil {
		return nil
	}
	if closer != nil {
		defer closer.Close()
	}
	var entries []ArchiveEntry
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			break
		}
		entries = append(entries, ArchiveEntry{
			Name:           header.Name,
			Size:           header.Size,
			CompressedSize: 0, // tar 无逐条压缩尺寸
			IsDir:          header.Typeflag == '5',
		})
	}
	return entries
}

func list7z(source archiveSource) []ArchiveEntry {
	// bodgit 不暴露未解压的 Reader 直接列表——用 sevenzip.NewReader
	reader, err := sevenzipNewReader(source)
	if err != nil {
		return nil
	}
	var entries []ArchiveEntry
	for _, f := range reader.File {
		entries = append(entries, ArchiveEntry{
			Name:           f.Name,
			Size:           int64(f.UncompressedSize),
			CompressedSize: 0,
			IsDir:          f.FileInfo().IsDir(),
			Encrypted:      false, // bodgit 不直接暴露加密标记
		})
	}
	return entries
}

func listRar(source archiveSource) []ArchiveEntry {
	password := ""
	reader, closer, err := openRarReader(source, password)
	if err != nil {
		return nil
	}
	defer closer()
	var entries []ArchiveEntry
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			break
		}
		entries = append(entries, ArchiveEntry{
			Name:           header.Name,
			Size:           header.UnPackedSize,
			CompressedSize: header.PackedSize,
			IsDir:          header.IsDir,
		})
	}
	return entries
}

// sevenzipNewReader 小件（避免直接 import bodgit 到 list.go）
func sevenzipNewReader(source archiveSource) (*sevenzipReader, error) {
	return sevenzipOpen(source)
}

// testZip 逐条解压验 CRC（内存中，不写盘）
func testZip(archivePath string) []string {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return []string{"无法打开归档: " + err.Error()}
	}
	defer reader.Close()

	var failures []string
	for _, f := range reader.File {
		if f.Flags&0x1 != 0 {
			continue // 加密条目跳过（需要密码）
		}
		rc, openErr := f.Open()
		if openErr != nil {
			failures = append(failures, fmt.Sprintf("打开失败: %s: %v", f.Name, openErr))
			continue
		}
		_, copyErr := io.Copy(io.Discard, rc)
		rc.Close()
		if copyErr != nil {
			failures = append(failures, fmt.Sprintf("CRC 校验失败: %s: %v", f.Name, copyErr))
		}
	}
	return failures
}

func testTar(archivePath string) []string {
	// tar 的完整性 = 能完整走读到 EOF；gzip 层有 CRC
	source, err := openFileSource(archivePath)
	if err != nil {
		return []string{err.Error()}
	}
	defer source.close()
	format, _ := sniffSource(source)
	compression := tarCompressionDecoder(format)
	tarReader, closer, err := openTarStream(source, compression)
	if err != nil {
		return []string{err.Error()}
	}
	if closer != nil {
		defer closer.Close()
	}
	var failures []string
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			return failures // 完整走读成功
		}
		if nextErr != nil {
			failures = append(failures, fmt.Sprintf("条目读取失败: %v", nextErr))
			return failures
		}
		// 读取条目数据到 discard（触发 CRC 验证）
		if _, copyErr := io.Copy(io.Discard, tarReader); copyErr != nil {
			failures = append(failures, fmt.Sprintf("数据校验失败: %s: %v", header.Name, copyErr))
		}
	}
}

// buildArchiveInfo 聚合归档信息
var zipComment string // zip 归档注释（listZip 时读取）

func buildArchiveInfo(format Format, entries []ArchiveEntry, archivePath string) *ArchiveInfo {
	info := &ArchiveInfo{
		Format:    formatName(format),
		EntryList: entries,
		Comment:   zipComment,
	}
	fileInfo, statErr := os.Stat(archivePath)
	if statErr == nil {
		info.ArchiveSize = fileInfo.Size()
	}
	var totalUncomp, totalComp int64
	fileCount, dirCount := 0, 0
	for _, e := range entries {
		if e.IsDir {
			dirCount++
		} else {
			fileCount++
		}
		totalUncomp += e.Size
		totalComp += e.CompressedSize
		if e.Encrypted {
			info.Encrypted = true
		}
	}
	info.EntryCount = len(entries)
	info.FileCount = fileCount
	info.DirCount = dirCount
	info.TotalUncompressed = totalUncomp
	info.TotalCompressed = totalComp
	if totalUncomp > 0 && info.ArchiveSize > 0 {
		info.CompressionRatio = float64(info.ArchiveSize) / float64(totalUncomp)
	}
	return info
}

// formatName 格式标识 → 人类可读名
func formatName(format Format) string {
	switch format {
	case FormatZip:
		return "zip"
	case FormatTar:
		return "tar"
	case FormatTarGz:
		return "tar.gz"
	case FormatTarXz:
		return "tar.xz"
	case FormatTarBz2:
		return "tar.bz2"
	case FormatTarZst:
		return "tar.zst"
	case Format7z:
		return "7z"
	case FormatRar:
		return "rar"
	case FormatWim:
		return "wim"
	case FormatCab:
		return "cab"
	case FormatGzipSingle:
		return "gzip"
	case FormatBzip2:
		return "bzip2"
	case FormatZstd:
		return "zstd"
	case FormatXz:
		return "xz"
	case FormatLz4:
		return "lz4"
	case FormatBrotli:
		return "brotli"
	case FormatLzip:
		return "lzip"
	case FormatZ:
		return "Z"
	case FormatAr:
		return "ar"
	case FormatAsar:
		return "asar"
	case FormatISO:
		return "iso"
	default:
		return fmt.Sprintf("format(%d)", int(format))
	}
}
