// 单流压缩格式（gzip/bzip2/zstd/xz/lz4/brotli）：不是容器，解出为单个文件（名 = 归档基名
// 去扩展名）。同一批解码器也充当 tar 压缩壳（tar.gz/tar.xz/tar.bz2/tar.zst）的解压层。
// 输出语义：--output 显式目录或当前目录；跳过/覆盖语义与容器归档一致。
package core

import (
	"compress/bzip2"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/andybalholm/brotli"
	kgzip "github.com/klauspost/compress/gzip"
	kzstd "github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	lzip "github.com/sorairolake/lzip-go"
	"github.com/ulikunitz/xz"
)

// singleStreamDecoder 单流格式的解码器构造（读流包装）
type singleStreamDecoder func(stream io.Reader) (io.ReadCloser, error)

// singleStreamDecoders 各单流格式的解码器表（tar 压缩壳复用同一张表）
var singleStreamDecoders = map[Format]singleStreamDecoder{
	FormatGzipSingle: func(stream io.Reader) (io.ReadCloser, error) {
		gzipReader, gzipErr := kgzip.NewReader(stream)
		if gzipErr != nil {
			return nil, fmt.Errorf("无法读取 gzip 流: %s", gzipErr.Error())
		}
		return gzipReader, nil
	},
	FormatBzip2: func(stream io.Reader) (io.ReadCloser, error) {
		// compress/bzip2 只读且无 Close 语义，包一层空 Close
		return io.NopCloser(bzip2.NewReader(stream)), nil
	},
	FormatZstd: func(stream io.Reader) (io.ReadCloser, error) {
		zstdReader, zstdErr := kzstd.NewReader(stream)
		if zstdErr != nil {
			return nil, fmt.Errorf("无法读取 zstd 流: %s", zstdErr.Error())
		}
		return io.NopCloser(zstdReader), nil
	},
	FormatXz: func(stream io.Reader) (io.ReadCloser, error) {
		xzReader, xzErr := xz.NewReader(stream)
		if xzErr != nil {
			return nil, fmt.Errorf("无法读取 xz 流: %s", xzErr.Error())
		}
		return io.NopCloser(xzReader), nil
	},
	FormatLz4: func(stream io.Reader) (io.ReadCloser, error) {
		// LZ4 Frame 格式（.lz4 文件标准容器）；非法帧在首次读时暴露
		return io.NopCloser(lz4.NewReader(stream)), nil
	},
	FormatBrotli: func(stream io.Reader) (io.ReadCloser, error) {
		// brotli 流无魔数：入口已按扩展名门控，此处直接解
		return io.NopCloser(brotli.NewReader(stream)), nil
	},
	FormatZ: func(stream io.Reader) (io.ReadCloser, error) {
		zReader, zErr := newZCompressReader(stream)
		if zErr != nil {
			return nil, fmt.Errorf("无法读取 Z 流: %s", zErr.Error())
		}
		return zReader, nil
	},
	FormatLzip: func(stream io.Reader) (io.ReadCloser, error) {
		lzReader, lzErr := lzip.NewReader(stream)
		if lzErr != nil {
			return nil, fmt.Errorf("无法读取 lzip 流: %s", lzErr.Error())
		}
		return io.NopCloser(lzReader), nil // lzip.Reader 无 Close 语义
	},
}

// singleStreamExtract 单流格式解压：解出为单文件（基名去压缩扩展名），流式拷贝有界内存
func singleStreamExtract(source archiveSource, format Format, options ExtractOptions, logf Logf) (ExtractResult, error) {
	decoder, hasDecoder := singleStreamDecoders[format]
	if !hasDecoder {
		return ExtractResult{}, fmt.Errorf("暂不支持的单流格式: %s", source.displayPath)
	}
	return extractSingleFile(source, decoder, options, logf)
}

// restoreSplitRawFile 分卷内层不是归档：按原始字节拼接还原为单文件（透传解码还原）
func restoreSplitRawFile(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	return extractSingleFile(source, func(stream io.Reader) (io.ReadCloser, error) {
		return io.NopCloser(stream), nil
	}, options, logf)
}

// extractSingleFile 单文件落地骨架：定名（去 .001 与压缩扩展名）→ 定目录 → 流式解出；
// 跳过/覆盖语义与容器条目一致
func extractSingleFile(source archiveSource, decoder singleStreamDecoder, options ExtractOptions, logf Logf) (ExtractResult, error) {
	return extractSingleFileNamed(source, decoder, options, "", logf)
}

// extractSingleFileNamed 可指定输出文件名（空 = 基名去扩展名推导；uuencode 内嵌名用）
func extractSingleFileNamed(source archiveSource, decoder singleStreamDecoder, options ExtractOptions, forcedName string, logf Logf) (ExtractResult, error) {
	// 输出目录：显式指定直接用；缺省落当前目录（单文件无"散落"问题，不走智能解压）
	outputDir := options.OutputDir
	if outputDir == "" {
		currentDir, cwdErr := os.Getwd()
		if cwdErr != nil {
			return ExtractResult{}, fmt.Errorf("获取当前目录失败: %s", cwdErr.Error())
		}
		outputDir = currentDir
	}
	absoluteOutputDir, absErr := filepath.Abs(outputDir)
	if absErr != nil {
		absoluteOutputDir = outputDir
	}
	if mkdirErr := os.MkdirAll(absoluteOutputDir, 0o755); mkdirErr != nil {
		return ExtractResult{}, fmt.Errorf("创建输出目录失败: %s", mkdirErr.Error())
	}
	targetName := forcedName
	if targetName == "" {
		targetName = ArchiveBaseName(filepath.Base(stripSplitSuffix(source.displayPath)))
	}
	targetPath := filepath.Join(absoluteOutputDir, targetName)

	progressState := newProgressReporter(1, source.size(), logf)
	stopProgress := progressState.start("解压中")
	defer stopProgress()

	stream, streamErr := source.openStream()
	if streamErr != nil {
		return ExtractResult{}, streamErr
	}
	defer stream.Close()
	decoded, decodeErr := decoder(stream)
	if decodeErr != nil {
		return ExtractResult{}, decodeErr
	}
	defer decoded.Close()

	writtenBytes, skippedFiles, writeErr := writeSingleExtractedFile(decoded, targetPath, options.Force, logf)
	if writeErr != nil {
		return ExtractResult{}, fmt.Errorf("解压失败: %s", writeErr.Error())
	}
	if skippedFiles == 0 {
		progressState.doneFiles.Add(1)
		progressState.doneBytes.Add(writtenBytes)
	}
	return ExtractResult{
		Output:    absoluteOutputDir,
		FileCount: 1 - skippedFiles,
		SizeBytes: writtenBytes,
		Skipped:   skippedFiles,
	}, nil
}

// writeSingleExtractedFile 落地单文件：O_EXCL 原子跳过 / force 截断重写（与容器条目同语义）；
// 返回写出字节数与跳过标记
func writeSingleExtractedFile(decoded io.Reader, targetPath string, forceOverwrite bool, logf Logf) (int64, int, error) {
	openFlags := os.O_WRONLY | os.O_CREATE
	if forceOverwrite {
		openFlags |= os.O_TRUNC
	} else {
		openFlags |= os.O_EXCL // 原子探测：已存在即跳过，无 TOCTOU
	}
	targetFile, createErr := os.OpenFile(targetPath, openFlags, 0o644)
	if createErr != nil {
		if !forceOverwrite && os.IsExist(createErr) {
			reportLogf(logf, "跳过已存在: %s", targetPath)
			return 0, 1, nil
		}
		return 0, 0, createErr
	}
	defer targetFile.Close()
	writtenCount, copyErr := io.Copy(targetFile, decoded)
	if copyErr != nil {
		return writtenCount, 0, copyErr
	}
	return writtenCount, 0, nil
}
