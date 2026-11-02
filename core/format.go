// 格式识别与归档源抽象：写入格式由输出路径扩展名决定，读取格式靠魔数嗅探（不信任扩展名）。
// 归档源（archiveSource）统一普通文件与 .001 分卷拼接流——提取器只见源，不见文件布局。
package core

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SNIFF_HEADER_LEN 魔数嗅探读取的头部长度：覆盖 zip/gzip/bz2/zstd/7z/rar 前缀魔数与
// tar 的 ustar 魔数（偏移 257 起），512 字节恰为一个 tar 块
const SNIFF_HEADER_LEN = 512

// DetectWriteFormat 由输出路径扩展名决定写入格式：.tar / .tar.gz|.tgz / 其余一律 zip
func DetectWriteFormat(outputPath string) Format {
	lowerOutput := strings.ToLower(outputPath)
	switch {
	case strings.HasSuffix(lowerOutput, ".tar"):
		return FormatTar
	case strings.HasSuffix(lowerOutput, ".tar.gz"), strings.HasSuffix(lowerOutput, ".tgz"):
		return FormatTarGz
	case strings.HasSuffix(lowerOutput, ".tar.xz"), strings.HasSuffix(lowerOutput, ".txz"):
		return FormatTarXz
	case strings.HasSuffix(lowerOutput, ".tar.bz2"), strings.HasSuffix(lowerOutput, ".tbz2"):
		return FormatTarBz2
	case strings.HasSuffix(lowerOutput, ".tar.zst"), strings.HasSuffix(lowerOutput, ".tzst"):
		return FormatTarZst
	default:
		return FormatZip // .zip 或未识别扩展名：默认 zip
	}
}

// SniffFormat 读文件头按魔数识别格式：
// zip（PK 前缀；SFX 兜底靠 EOCD 尾部定位）/ tar.gz（gzip 魔数 + 内层 ustar 佐证）/
// gzip 单流（gzip 魔数、内层非 tar——解出单文件）/ bzip2（BZh）/ zstd（28 B5 2F FD）/
// tar（ustar@257，覆盖 ustar/PAX/GNU 三系）/ 7z / rar（自家多卷命名由 rardecode 续卷）。
// 其余返回无法识别错误——错误文案即 CLI 对外 error_info。
func SniffFormat(archivePath string) (Format, error) {
	source, openErr := openFileSource(archivePath)
	if openErr != nil {
		return FormatZip, openErr
	}
	defer source.close()
	return sniffSource(source)
}

// sniffSource 从归档源读头部识别格式（源可以是普通文件或 .001 拼接流）
func sniffSource(source archiveSource) (Format, error) {
	headerBytes := make([]byte, SNIFF_HEADER_LEN)
	readLen, _ := source.readAt(headerBytes, 0)
	header := headerBytes[:readLen]

	switch {
	case hasMagicPrefix(header, "PK\x03\x04"), hasMagicPrefix(header, "PK\x05\x06"), hasMagicPrefix(header, "PK\x07\x08"):
		return FormatZip, nil
	case hasMagicPrefix(header, "\x1f\x8b"):
		return sniffCompressedInnerTar(source, FormatGzipSingle)
	case hasMagicPrefix(header, "BZh"):
		return sniffCompressedInnerTar(source, FormatBzip2)
	case hasMagicPrefix(header, "\x28\xb5\x2f\xfd"):
		return sniffCompressedInnerTar(source, FormatZstd)
	case hasMagicPrefix(header, "\xfd7zXZ\x00"):
		return sniffCompressedInnerTar(source, FormatXz)
	case hasMagicPrefix(header, "\x04\x22\x4d\x18"):
		return sniffCompressedInnerTar(source, FormatLz4)
	case hasMagicPrefix(header, "7z\xbc\xaf\x27\x1c"):
		return Format7z, nil
	case hasMagicPrefix(header, "Rar!\x1a\x07"):
		return FormatRar, nil
	}
	// .Z（compress）：1F 9D + flags 保留位（0x60）必须为零
	if len(header) >= 3 && header[0] == 0x1f && header[1] == 0x9d && header[2]&0x60 == 0 {
		return FormatZ, nil
	}
	// lzip：LZIP 魔数
	if hasMagicPrefix(header, "LZIP") {
		return FormatLzip, nil
	}
	// wim：MSWIM\0\0\0
	if hasMagicPrefix(header, WIM_IDENT) {
		return FormatWim, nil
	}
	// cab：MSCF
	if hasMagicPrefix(header, CAB_IDENT) {
		return FormatCab, nil
	}
	// ar（Unix 归档）：!<arch>\n
	if hasMagicPrefix(header, AR_MAGIC) {
		return FormatAr, nil
	}
	if len(header) >= 262 && string(header[257:262]) == "ustar" {
		return FormatTar, nil
	}
	// iso9660：CD001 @ 0x8001（卷描述符区）
	identBytes := make([]byte, len(ISO_IDENT))
	if identRead, _ := source.readAt(identBytes, ISO_IDENT_OFFSET); identRead == len(ISO_IDENT) && string(identBytes) == ISO_IDENT {
		return FormatISO, nil
	}
	// uuencode/xxencode：begin 头行（.xxe 扩展名门控区分字母表，缺省按更常见的 uu）
	if headerLen := len(header); headerLen >= 6 {
		headText := string(header[:min(headerLen, 256)])
		firstLine, _, hasNewline := strings.Cut(headText, "\n")
		if strings.HasPrefix(firstLine, "begin ") {
			if strings.HasSuffix(strings.ToLower(filepath.Base(source.displayPath)), ".xxe") {
				return FormatXXE, nil
			}
			return FormatUUE, nil
		}
		_ = hasNewline
	}
	// brotli 流无魔数：仅当扩展名是 .br 时试解开头验证（防任意数据误判）
	if strings.HasSuffix(strings.ToLower(filepath.Base(source.displayPath)), ".br") {
		if format, brotliOk := sniffBrotliByTrial(source); brotliOk {
			return format, nil
		}
	}
	// asar：无魔数，扩展名门控 + 头结构试解
	if strings.HasSuffix(strings.ToLower(filepath.Base(source.displayPath)), ".asar") {
		if sniffAsarByHeader(source) {
			return FormatAsar, nil
		}
	}
	// SFX zip 兜底：zip 目录结构从尾部 EOCD 定位，exe 壳包着的 zip 也能被标准读取器打开
	if probeReader, probeErr := zip.NewReader(source.readerAt(), source.size()); probeErr == nil {
		_ = probeReader
		return FormatZip, nil
	}
	return FormatZip, fmt.Errorf("无法识别的归档格式: %s（支持 zip/7z/rar/tar 系/gzip/bzip2/zstd/xz/lz4/.001 分卷）", source.displayPath)
}

// sniffCompressedInnerTar 压缩流魔数命中后解开头一段验证内层是否 tar：
// 是 → 对应的 tar 压缩壳格式（tar.gz/tar.xz/tar.bz2/tar.zst）；否 → 单流格式（解出单文件）
func sniffCompressedInnerTar(source archiveSource, codec Format) (Format, error) {
	stream, streamErr := source.openStream()
	if streamErr != nil {
		return codec, streamErr
	}
	defer stream.Close()
	decoded, decodeErr := singleStreamDecoders[codec](stream)
	if decodeErr != nil {
		return codec, decodeErr
	}
	defer decoded.Close()
	innerBytes := make([]byte, SNIFF_HEADER_LEN)
	innerLen, _ := io.ReadFull(decoded, innerBytes)
	if innerLen >= 262 && string(innerBytes[257:262]) == "ustar" {
		return tarWrappedCodec[codec], nil
	}
	return codec, nil
}

// sniffBrotliByTrial .br 扩展名门控后的试解验证：解出头几个字节无错即认 brotli
// （空流也是合法 brotli，可接受）
func sniffBrotliByTrial(source archiveSource) (Format, bool) {
	stream, streamErr := source.openStream()
	if streamErr != nil {
		return FormatZip, false
	}
	defer stream.Close()
	decoded, decodeErr := singleStreamDecoders[FormatBrotli](stream)
	if decodeErr != nil {
		return FormatZip, false
	}
	defer decoded.Close()
	probeBytes := make([]byte, 64)
	if _, readErr := decoded.Read(probeBytes); readErr != nil && readErr != io.EOF {
		return FormatZip, false
	}
	return FormatBrotli, true
}

// hasMagicPrefix header 是否以 magic 开头（头太短不命中）
func hasMagicPrefix(header []byte, magic string) bool {
	return len(header) >= len(magic) && string(header[:len(magic)]) == magic
}

// ArchiveBaseName 剥离已知归档扩展名（含复合 .tar.gz/.tgz）得到基名，
// 供智能解压散装归档的包层根目录命名与单流格式输出文件命名
func ArchiveBaseName(archiveFileName string) string {
	lowerName := strings.ToLower(archiveFileName)
	for _, suffix := range []string{
		".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.bz2", ".tbz2", ".tar.zst", ".tzst",
		".tar", ".zip", ".gz", ".bz2", ".zst", ".xz", ".lz4", ".lzma", ".br", ".z", ".uue", ".xxe", ".lz", ".wim",
	} {
		if strings.HasSuffix(lowerName, suffix) {
			trimmed := archiveFileName[:len(archiveFileName)-len(suffix)]
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return archiveFileName
}

// archiveSource 归档源：普通文件或 .001 分卷拼接流的统一视图。
// readerAt 供 zip 这类随机访问读取器；openStream 每次给出新的顺序读流（tar/gzip 等）。
type archiveSource struct {
	displayPath string // 报错与命名用（分卷时为首卷路径）
	backingFile *os.File
	joined      *joinedSegments // 非 nil 时为分卷拼接源
}

// openFileSource 打开普通文件为归档源
func openFileSource(archivePath string) (archiveSource, error) {
	archiveFile, openErr := os.Open(archivePath)
	if openErr != nil {
		return archiveSource{}, fmt.Errorf("无法读取归档: %s", openErr.Error())
	}
	return archiveSource{displayPath: archivePath, backingFile: archiveFile}, nil
}

// size 源总大小（分卷 = 各段之和）
func (source archiveSource) size() int64 {
	if source.joined != nil {
		return source.joined.totalSize
	}
	fileInfo, statErr := source.backingFile.Stat()
	if statErr != nil {
		return 0
	}
	return fileInfo.Size()
}

// readerAt 随机访问视图（zip.NewReader 用；普通文件与分卷拼接都支持）
func (source archiveSource) readerAt() io.ReaderAt {
	if source.joined != nil {
		return source.joined
	}
	return source.backingFile
}

// readAt 定点读（嗅探用）
func (source archiveSource) readAt(buffer []byte, offset int64) (int, error) {
	return source.readerAt().ReadAt(buffer, offset)
}

// openStream 打开一条新的顺序读流（覆盖源全量字节）
func (source archiveSource) openStream() (io.ReadCloser, error) {
	if source.joined != nil {
		return source.joined.openStream()
	}
	return os.Open(source.displayPath)
}

// close 释放源持有的资源
func (source archiveSource) close() {
	if source.joined != nil {
		source.joined.close()
		return
	}
	source.backingFile.Close()
}
