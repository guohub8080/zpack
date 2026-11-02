// Package core 是 zpack 的核心算法引擎（纯库形态）：
//
//   - 只做算法与管线，不碰 stdout/stderr、不 os.Exit——错误一律 error 返回，
//     过程日志（进度/GBK 转换提示/跳过明细）经 Logf 回调注入，调用方决定怎么呈现；
//   - 两个统一入口 Compress / Extract，内部按格式（zip / tar / tar.gz）分发；
//   - CLI（internal/cli）与未来 GUI 是同一引擎的两张面孔：
//     CLI 直接函数调用，GUI 既可经 stdout JSON 契约驱动 CLI 二进制，也可直连本包。
//
// 已实现格式：zip 读写（并行架构）、tar/tar.gz 读写（PAX 确定性）。
// 已实现格式：zip 读写、tar 系读写（gz/xz/bz2/zst 壳）、单流 gzip/bzip2/zstd/xz/lz4/brotli、
// .001 分卷、7z 读取（bodgit/sevenzip）、rar 读取（nwaples/rardecode，只读——rar 创建法律禁止）。
package core

import (
	"fmt"
	"io"
	"os"
)

// Format 归档格式标识（与 --output 扩展名、魔数识别一一对应）
type Format int

const (
	FormatZip Format = iota
	FormatTar
	FormatTarGz
	FormatGzipSingle // gzip 单流（内层非 tar）：解出单文件
	FormatBzip2      // bzip2 单流：解出单文件
	FormatZstd       // zstd 单流：解出单文件
	FormatXz         // xz 单流：解出单文件
	FormatLz4        // lz4 单流：解出单文件
	FormatBrotli     // brotli 单流（无魔数，扩展名门控）：解出单文件
	FormatTarXz      // tar.xz：tar 容器 + xz 压缩壳
	FormatTarBz2     // tar.bz2：tar 容器 + bzip2 压缩壳
	FormatTarZst     // tar.zst：tar 容器 + zstd 压缩壳
	Format7z         // 7z 读取（bodgit/sevenzip）
	FormatRar        // rar 读取（nwaples/rardecode；创建法律禁止）
	FormatZ          // .Z（UNIX compress LZW）单流：解出单文件
	FormatUUE        // uuencode 单流：解出单文件（内嵌文件名优先）
	FormatXXE        // xxencode 单流：解出单文件（内嵌文件名优先）
	FormatLzip       // lzip 单流：解出单文件
	FormatWim        // wim 读取（go-wim，LZX 按需解码）
	FormatCab        // cab 读取（MSCF + NONE/MSZIP/LZX，Quantum 暂不支持）
	FormatAr         // ar（Unix 归档）读取：GNU/BSD 双命名系
	FormatAsar       // asar（Electron）读取：.asar 扩展名门控
	FormatISO        // iso9660 读取（kdomanski/iso9660，Rock Ridge 生效）
)

// tarWrappedCodec 单流编解码 → 同编解码的 tar 压缩壳格式；tarCompressionCodec 反向。
// tar 管线用这两个映射复用单流解码器，一套代码吃 tar.gz/tar.xz/tar.bz2/tar.zst 全家
var tarWrappedCodec = map[Format]Format{
	FormatGzipSingle: FormatTarGz,
	FormatXz:         FormatTarXz,
	FormatBzip2:      FormatTarBz2,
	FormatZstd:       FormatTarZst,
}

// tarCompressionDecoder tar 压缩壳格式的解压层（FormatTar 本体无壳 → nil）
func tarCompressionDecoder(tarVariant Format) singleStreamDecoder {
	for codec, wrapped := range tarWrappedCodec {
		if wrapped == tarVariant {
			return singleStreamDecoders[codec]
		}
	}
	return nil
}

// DefaultCompressLevel --level 缺省值（与标准库 DefaultCompression 实际级别一致）
const DefaultCompressLevel = 6

// MaxJobs --jobs 上限（CLI 严格校验 1-MaxJobs 用）
const MaxJobs = 64

// Logf 过程日志回调（printf 语义）；nil = 静默。CLI 侧注入 output.LoggerInfo 适配器
type Logf func(format string, args ...any)

// CompressOptions 压缩入参（路径由调用方解析为可直用形式；校验语义见各字段注释）
type CompressOptions struct {
	Inputs     []string // 输入（目录/文件混合，目录以目录名为包内根前缀）
	Output     string   // 归档输出路径（绝对路径；扩展名决定格式——CLI 已保证非空）
	Excludes   []string // 按名精确排除（默认垃圾清单之外追加）
	Level      int      // 0-9：zip deflate 档 / tar.gz 的 gzip 档
	Jobs       int      // 并行度：zip 有效（压缩侧上限 8）；tar 单流忽略
	VolumeSize int64    // 分卷大小（字节）；>0 = 完整归档写完后按此大小字节切分 .001/.002/...（格式无关纯字节切分，最小 64KB=65536）
	Password   string   // 加密 zip 写入密码（WinZip AES-256 AE-2）；空串=不加密
	Comment    string   // zip 归档注释（EOCD comment 字段，最长 65535 字节）
	// PasswordResolver 逐文件密码解析（--password-table 映射文件解析后注入）：优先于 Password
	PasswordResolver func(fileName string) (string, bool)
}

// CompressResult 压缩产物统计（CLI 组装 {"is_success":true,"archive":...,"file_count":...,"size_bytes":...}）
type CompressResult struct {
	Archive   string // 归档绝对路径
	FileCount int    // 入包条目数
	SizeBytes int64  // 归档字节数
}

// ExtractOptions 解压入参
type ExtractOptions struct {
	Archive   string // 归档路径（绝对路径；格式由魔数识别，不靠扩展名）
	OutputDir string // 显式输出目录；空串 = 智能解压（unar 语义）
	Force     bool   // true 覆盖已存在；false 原子跳过（结果 Skipped 计数）
	Jobs      int    // 并行度：zip 解压有效（上限 64）；tar 单流忽略
	Password  string // 加密归档密码（zip WinZip AES/ZipCrypto、7z、rar）；空串遇加密报「需要 --password」
	// PasswordResolver 逐文件密码解析回调（--password-table 映射文件解析后注入）：
	// 返回 (密码, 是否命中)；优先于 Password 字段，未命中回退 Password
	PasswordResolver func(fileName string) (string, bool)
}

// ExtractResult 解压统计（CLI 组装 {"is_success":true,"output":...,"file_count":...,"size_bytes":...,"skipped":...}）
type ExtractResult struct {
	Output    string // 实际解压根目录（绝对路径）
	FileCount int    // 解出条目数（不含目录条目）
	SizeBytes int64  // 解出总字节
	Skipped   int    // 因已存在而跳过的条目数
}

// Compress 压缩统一入口：按 Output 扩展名分发格式（.tar / .tar.gz|.tgz / 其余 zip）。
// VolumeSize > 0 时：完整归档写完后按此大小字节切分 .001/.002/...（格式无关——业界主流压缩器
// 同款纯字节切分，cat 即还原），原归档删除，返回首卷路径与全卷总字节。
// 失败时半成品归档/卷已被删除（中途失败删半成品纪律），错误信息人类可读。
func Compress(options CompressOptions, logf Logf) (CompressResult, error) {
	var result CompressResult
	var compressErr error
	switch DetectWriteFormat(options.Output) {
	case FormatTar:
		result, compressErr = tarCompress(options, FormatTar, logf)
	case FormatTarGz, FormatTarXz, FormatTarBz2, FormatTarZst:
		result, compressErr = tarCompress(options, DetectWriteFormat(options.Output), logf)
	default:
		result, compressErr = zipCompress(options, logf)
	}
	if compressErr != nil {
		return CompressResult{}, compressErr
	}
	if options.VolumeSize > 0 {
		firstVolume, totalBytes, splitErr := splitArchiveIntoVolumes(result.Archive, options.VolumeSize, logf)
		if splitErr != nil {
			os.Remove(result.Archive) // 切分失败删归档
			return CompressResult{}, splitErr
		}
		result.Archive = firstVolume
		result.SizeBytes = totalBytes
	}
	return result, nil
}

// VOLUME_SIZE_MINIMUM 分卷大小下限：64KB=65536 字节（业界主流压缩器同值：
// "目前所选的分卷大小为%d字节。请输入不小于65536的数值"）
const VOLUME_SIZE_MINIMUM int64 = 65536

// splitArchiveIntoVolumes 字节切分：完整归档 → .001/.002/...（cat 即还原）；
// 返回首卷路径与全卷总字节。失败时已写卷段全部清理。
func splitArchiveIntoVolumes(archivePath string, volumeSize int64, logf Logf) (string, int64, error) {
	if volumeSize < VOLUME_SIZE_MINIMUM {
		return "", 0, fmt.Errorf("分卷大小不得小于 %d 字节（64KB），收到: %d", VOLUME_SIZE_MINIMUM, volumeSize)
	}
	archiveFile, openErr := os.Open(archivePath)
	if openErr != nil {
		return "", 0, openErr
	}
	defer archiveFile.Close()
	archiveInfo, statErr := archiveFile.Stat()
	if statErr != nil {
		return "", 0, statErr
	}

	// 整档不超过单卷大小：不切分，保持原文件名（不产孤零零的 .001）
	if archiveInfo.Size() <= volumeSize {
		reportLogf(logf, "归档 %d 字节未超过单卷大小 %d 字节，不分卷", archiveInfo.Size(), volumeSize)
		return archivePath, archiveInfo.Size(), nil
	}

	var writtenVolumes []string
	var totalBytes int64
	volumeIdx := 1
	buffer := make([]byte, min(volumeSize, 1<<20)) // 拷贝缓冲 ≤1MB

	for remaining := archiveInfo.Size(); remaining > 0; {
		volumeName := fmt.Sprintf("%s.%03d", archivePath, volumeIdx)
		volumeFile, createErr := os.Create(volumeName)
		if createErr != nil {
			cleanupSplitVolumes(writtenVolumes)
			return "", 0, createErr
		}
		var volumeBytes int64
		for volumeBytes < volumeSize && remaining > 0 {
			chunkLen := int64(len(buffer))
			if remaining < chunkLen {
				chunkLen = remaining
			}
			if volumeSize-volumeBytes < chunkLen {
				chunkLen = volumeSize - volumeBytes
			}
			readLen, readErr := archiveFile.Read(buffer[:chunkLen])
			if readLen > 0 {
				if _, writeErr := volumeFile.Write(buffer[:readLen]); writeErr != nil {
					volumeFile.Close()
					cleanupSplitVolumes(writtenVolumes)
					return "", 0, writeErr
				}
				volumeBytes += int64(readLen)
				remaining -= int64(readLen)
				totalBytes += int64(readLen)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				volumeFile.Close()
				cleanupSplitVolumes(writtenVolumes)
				return "", 0, readErr
			}
		}
		volumeFile.Close()
		writtenVolumes = append(writtenVolumes, volumeName)
		reportLogf(logf, "分卷 %s: %d 字节", volumeName, volumeBytes)
		volumeIdx++
	}

	// 全卷写好 → 删原归档
	if removeErr := os.Remove(archivePath); removeErr != nil {
		cleanupSplitVolumes(writtenVolumes)
		return "", 0, removeErr
	}
	if len(writtenVolumes) == 0 {
		return "", 0, fmt.Errorf("归档为空，无法分卷")
	}
	return writtenVolumes[0], totalBytes, nil
}

// cleanupSplitVolumes 失败路径清理已写卷段
func cleanupSplitVolumes(volumePaths []string) {
	for _, volumePath := range volumePaths {
		os.Remove(volumePath)
	}
}

// Extract 解压统一入口：开归档源（.001 分卷首卷自动拼接）→ 魔数识别 → 分发。
// 安全纪律（路径穿越/链接逃逸/设备条目/重复条目名/加密明示）先全验后动工；
// 失败即返回错误（已写出部分不回滚，调用方决策）。
func Extract(options ExtractOptions, logf Logf) (ExtractResult, error) {
	source, sourceErr := openExtractSource(options.Archive)
	if sourceErr != nil {
		return ExtractResult{}, sourceErr
	}
	defer source.close()

	format, sniffErr := sniffSource(source)
	if sniffErr != nil {
		// 分卷内层不是任何已知归档：按原始文件拼接还原
		if source.joined != nil {
			return restoreSplitRawFile(source, options, logf)
		}
		return ExtractResult{}, sniffErr
	}
	switch format {
	case FormatTar, FormatTarGz, FormatTarXz, FormatTarBz2, FormatTarZst:
		return tarExtract(source, format, options, logf)
	case FormatGzipSingle, FormatBzip2, FormatZstd, FormatXz, FormatLz4, FormatBrotli:
		return singleStreamExtract(source, format, options, logf)
	case Format7z:
		return sevenZipExtract(source, options, logf)
	case FormatRar:
		return rarExtract(source, options, logf)
	case FormatZ:
		return singleStreamExtract(source, format, options, logf)
	case FormatLzip:
		return singleStreamExtract(source, format, options, logf)
	case FormatWim:
		return wimExtract(source, options, logf)
	case FormatCab:
		return cabExtract(source, options, logf)
	case FormatUUE:
		return uueExtract(source, options, false, logf)
	case FormatXXE:
		return uueExtract(source, options, true, logf)
	case FormatAr:
		return arExtract(source, options, logf)
	case FormatAsar:
		return asarExtract(source, options, logf)
	case FormatISO:
		return isoExtract(source, options, logf)
	default:
		return zipExtract(source, options, logf)
	}
}

// openExtractSource 打开解压源：路径是顺序分卷首卷（base.001）→ 各段拼接为逻辑单文件；
// 否则按普通文件打开。内层格式交给后续魔数嗅探（zip/tar/单流皆可）
func openExtractSource(archivePath string) (archiveSource, error) {
	if isSplitFirstPath(archivePath) {
		segments, joinErr := discoverSplitSegments(archivePath)
		if joinErr != nil {
			return archiveSource{}, joinErr
		}
		return archiveSource{displayPath: archivePath, joined: segments}, nil
	}
	return openFileSource(archivePath)
}
