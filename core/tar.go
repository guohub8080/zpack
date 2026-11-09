// tar/tar.gz 读写管线：写入 PAX 格式（长名/UTF-8 安全、确定性纪律见 tarCompress 注释），
// 读取覆盖 ustar/PAX/GNU 三系（长名、GNU sparse 由标准库折叠成普通头），符号链接保留、
// 硬链接按归档内成员复制落地、设备/管道条目拒绝——安全纪律与 zip 解压同源。
// 全部错误 error 返回、过程日志走 logf（库形态，见 core.go 包注释）。
package core

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	kgzip "github.com/klauspost/compress/gzip"
	kzstd "github.com/klauspost/compress/zstd"
	uxz "github.com/ulikunitz/xz"
)

// TAR_MODTIME_PRECISION tar 头时间戳精度：截到秒（传统 tar 精度）。PAX 会把亚秒值写成
// 扩展记录——不同文件系统 stat 精度不同会让同输入产出不同字节，确定性优先故截断。
const TAR_MODTIME_PRECISION = time.Second

// tarEntryKind 预扫条目的落地类型
type tarEntryKind int

const (
	tarKindFile tarEntryKind = iota
	tarKindDir
	tarKindSymlink
	tarKindHardlink
)

// tarScannedEntry 预扫后的 tar 条目：解码归一化名 + 类型/链接目标/尺寸/权限（写出参数）
type tarScannedEntry struct {
	cleanName  string
	isDirEntry bool
	entryKind  tarEntryKind
	linkName   string // 符号链接：目标原样保留（相对链接语义）；硬链接：归档内成员归一化路径
	entrySize  int64
	entryMode  int64
}

// openTarStream 从归档源开一条 tar 读流（compression 非 nil 时先包对应解压层——
// gzip/xz/bzip2/zstd 壳同一入口）；返回的 Closer 关闭解压层与底层流两者（此前只关解压层，
// 底层 *os.File 泄漏——Windows 上文件被占导致删除失败，2026-11-08 实测修复）
func openTarStream(source archiveSource, compression singleStreamDecoder) (*tar.Reader, io.Closer, error) {
	stream, streamErr := source.openStream()
	if streamErr != nil {
		return nil, nil, streamErr
	}
	var closers closeChain // 关闭顺序：解压层 → 底层流
	var reader io.Reader = stream
	if compression != nil {
		decoded, decodeErr := compression(stream)
		if decodeErr != nil {
			stream.Close()
			return nil, nil, decodeErr
		}
		closers = append(closers, decoded)
		reader = decoded
	}
	closers = append(closers, stream)
	return tar.NewReader(reader), closers, nil
}

// closeChain 依序关闭多个 Closer（全量尝试，首个错误返回——读侧关闭失败不掩盖其余清理）
type closeChain []io.Closer

func (chain closeChain) Close() error {
	var firstErr error
	for _, closer := range chain {
		if closeErr := closer.Close(); closeErr != nil && firstErr == nil {
			firstErr = closeErr
		}
	}
	return firstErr
}

// tarEntryFromHeader 头 → 预扫条目：名字解码（非 UTF-8 按 GBK 尽力修复，与 zip 同源启发式）、
// 路径穿越/符号链接逃逸/设备类型检查。logDecode 控制是否打 GBK 转换日志（预扫描打、解出遍不打）
func tarEntryFromHeader(header *tar.Header, logDecode bool, logf Logf) (tarScannedEntry, error) {
	decodedName, converted := decodeEntryName(header.Name, false)
	if isUnsafeEntryPath(header.Name) || isUnsafeEntryPath(decodedName) {
		return tarScannedEntry{}, fmt.Errorf("归档含不安全路径: %s", header.Name)
	}
	if converted && logDecode {
		reportLogf(logf, "条目名按 GBK 解码: %q -> %q", header.Name, decodedName)
	}
	cleanName := path.Clean(strings.ReplaceAll(decodedName, "\\", "/"))
	if cleanName == "." || cleanName == "" {
		return tarScannedEntry{cleanName: ""}, nil // 根占位条目：无实体，跳过
	}
	entry := tarScannedEntry{cleanName: cleanName, entrySize: header.Size, entryMode: header.Mode}
	switch header.Typeflag {
	case tar.TypeReg, tar.TypeRegA, tar.TypeGNUSparse:
		entry.entryKind = tarKindFile // GNU sparse 由标准库折叠：读出即逻辑流
	case tar.TypeDir:
		entry.entryKind = tarKindDir
		entry.isDirEntry = true
	case tar.TypeSymlink:
		if isUnsafeSymlinkTarget(cleanName, header.Linkname) {
			return tarScannedEntry{}, fmt.Errorf("归档含不安全符号链接: %s -> %s", cleanName, header.Linkname)
		}
		entry.entryKind = tarKindSymlink
		entry.linkName = header.Linkname
	case tar.TypeLink:
		cleanLink := path.Clean(strings.ReplaceAll(header.Linkname, "\\", "/"))
		if isUnsafeEntryPath(header.Linkname) || cleanLink == "." || cleanLink == "" {
			return tarScannedEntry{}, fmt.Errorf("归档含不安全硬链接: %s -> %s", cleanName, header.Linkname)
		}
		entry.entryKind = tarKindHardlink
		entry.linkName = cleanLink
	default:
		return tarScannedEntry{}, fmt.Errorf("归档含不安全条目类型（设备/管道/未知类型 %q）: %s", string(rune(header.Typeflag)), cleanName)
	}
	return entry, nil
}

// tarScanArchive 预扫一遍全部头（内容由 Next 自动跳过）：安全检查先全验后动工 +
// 智能解压判定数据 + 进度总量。tar 是顺序流，这一遍需要完整解压走读（gzip 层无随机访问）
func tarScanArchive(source archiveSource, compression singleStreamDecoder, logf Logf) ([]tarScannedEntry, error) {
	tarReader, compressionCloser, openErr := openTarStream(source, compression)
	if openErr != nil {
		return nil, fmt.Errorf("无法读取归档: %s", openErr.Error())
	}
	if compressionCloser != nil {
		defer compressionCloser.Close()
	}

	var entries []tarScannedEntry
	seenNames := map[string]bool{}
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("无法读取 tar 归档: %s", nextErr.Error())
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue // PAX 全局元数据：无实体
		}
		entry, entryErr := tarEntryFromHeader(header, true, logf)
		if entryErr != nil {
			return nil, entryErr
		}
		if entry.cleanName == "" {
			continue
		}
		if !entry.isDirEntry && seenNames[entry.cleanName] {
			return nil, fmt.Errorf("归档含重复条目名: %s", entry.cleanName)
		}
		seenNames[entry.cleanName] = true
		entries = append(entries, entry)
	}
	return entries, nil
}

// tarCompress tar/tar.gz 写入管线入口（core.Compress 的 tar 分支；参数已由 CLI 层解析校验）。
// 确定性纪律：PAX 格式（长名/大尺寸走扩展记录）、Uid/Gid/Uname/Gname 归零、ModTime 截秒、
// 权限只取 perm 位——同输入产出字节确定。tar/gzip 单流顺序写（jobs 不参与），流式拷贝内存有界。
// 空目录不保留、目录条目不写入（与 zip 语义一致）；符号链接原样保留（tar 惯例，zip 才解引用）。
func tarCompress(options CompressOptions, tarVariant Format, logf Logf) (CompressResult, error) {
	ignores := LoadEffectiveZpackignores(options.Inputs)
	tasks, collectErr := collectCompressTasks(options.Inputs, options.Excludes, ignores)
	if collectErr != nil {
		return CompressResult{}, collectErr
	}

	outputFile, createErr := os.Create(options.Output)
	if createErr != nil {
		return CompressResult{}, fmt.Errorf("创建 tar 归档失败: %s", createErr.Error())
	}
	chainWriter := io.WriteCloser(outputFile)
	var compressionCloser io.Closer
	switch tarVariant {
	case FormatTarGz:
		if options.Jobs > 1 {
			// 并行 gzip：128KB 分块并行 deflate（与 zip 同架构）——多核加速
			parallelWriter, parallelErr := NewParallelGzipWriter(outputFile, options.Jobs, options.Level)
			if parallelErr != nil {
				outputFile.Close()
				return CompressResult{}, fmt.Errorf("创建并行 gzip 流失败: %s", parallelErr.Error())
			}
			compressionCloser = parallelWriter
			chainWriter = parallelWriter
		} else {
			levelWriter, gzipErr := kgzip.NewWriterLevel(outputFile, options.Level)
			if gzipErr != nil {
				outputFile.Close()
				return CompressResult{}, fmt.Errorf("创建 gzip 流失败: %s", gzipErr.Error())
			}
			compressionCloser = levelWriter
			chainWriter = levelWriter
		}
	case FormatTarXz:
		xzWriter, xzErr := uxz.NewWriter(outputFile)
		if xzErr != nil {
			outputFile.Close()
			return CompressResult{}, fmt.Errorf("创建 xz 流失败: %s", xzErr.Error())
		}
		compressionCloser = xzWriter
		chainWriter = xzWriter
	case FormatTarZst:
		zstdWriter, zstdErr := kzstd.NewWriter(outputFile)
		if zstdErr != nil {
			outputFile.Close()
			return CompressResult{}, fmt.Errorf("创建 zstd 流失败: %s", zstdErr.Error())
		}
		compressionCloser = zstdWriter
		chainWriter = zstdWriter
	case FormatTarBz2:
		return CompressResult{}, fmt.Errorf("暂不支持 tar.bz2 写入（纯 Go 无成熟 bzip2 编码器）——读取已支持")
	}
	tarWriter := tar.NewWriter(chainWriter)

	progressState := newProgressReporter(int64(len(tasks)), 0, logf)
	var totalInputBytes int64
	for _, task := range tasks {
		totalInputBytes += task.fileInfo.Size()
	}
	progressState.SetInputBytes(totalInputBytes)
	stopProgress := progressState.start("压缩中")
	writeErr := tarWriteEntries(tarWriter, tasks, progressState)
	stopProgress()

	if writeErr != nil {
		tarWriter.Close()
		if compressionCloser != nil {
			compressionCloser.Close()
		}
		outputFile.Close()
		os.Remove(options.Output) // 中途失败删半成品（与 zip 同纪律）
		return CompressResult{}, fmt.Errorf("写入文件失败: %s", writeErr.Error())
	}
	commitErr := tarWriter.Close()
	if commitErr == nil && compressionCloser != nil {
		commitErr = compressionCloser.Close()
	}
	if commitErr == nil {
		commitErr = outputFile.Close()
	} else {
		outputFile.Close()
	}
	if commitErr != nil {
		os.Remove(options.Output)
		return CompressResult{}, fmt.Errorf("写入归档失败: %s", commitErr.Error())
	}

	outputInfo, statErr := os.Stat(options.Output)
	if statErr != nil {
		return CompressResult{}, fmt.Errorf("读取归档大小失败: %s", statErr.Error())
	}
	return CompressResult{Archive: options.Output, FileCount: len(tasks), SizeBytes: outputInfo.Size()}, nil
}

// tarWriteEntries 逐条写入并计数进度
func tarWriteEntries(tarWriter *tar.Writer, tasks []compressTask, progressState *progressReporter) error {
	for _, task := range tasks {
		if cancelErr := checkCancelled(); cancelErr != nil {
			return cancelErr
		}
		if writeErr := tarWriteEntry(tarWriter, task); writeErr != nil {
			return writeErr
		}
		progressState.doneFiles.Add(1)
		progressState.doneBytes.Add(task.fileInfo.Size())
	}
	return nil
}

// tarWriteEntry 单条目写头+数据：手工构造 Header（不用 FileInfoHeader——它会把 uid/gid/
// 访问时间等本机相关字段带进头，破坏跨机确定性）
func tarWriteEntry(tarWriter *tar.Writer, task compressTask) error {
	if !utf8.ValidString(task.entryName) {
		return fmt.Errorf("文件名不是有效 UTF-8，无法安全写入 tar: %q", task.entryName)
	}
	entryHeader := &tar.Header{
		Name:     filepath.ToSlash(task.entryName),
		Mode:     int64(task.fileInfo.Mode().Perm()),
		ModTime:  task.fileInfo.ModTime().Truncate(TAR_MODTIME_PRECISION),
		Format:   tar.FormatPAX,
		Typeflag: tar.TypeReg,
		Size:     task.fileInfo.Size(),
	}
	if task.fileInfo.Mode()&os.ModeSymlink != 0 {
		linkTarget, linkErr := os.Readlink(task.sourcePath)
		if linkErr != nil {
			return linkErr
		}
		entryHeader.Typeflag = tar.TypeSymlink
		entryHeader.Linkname = filepath.ToSlash(linkTarget)
		entryHeader.Size = 0 // 符号链接无数据体，目标名在 Linkname
		return tarWriter.WriteHeader(entryHeader)
	}
	if writeErr := tarWriter.WriteHeader(entryHeader); writeErr != nil {
		return writeErr
	}
	sourceFile, openErr := os.Open(task.sourcePath)
	if openErr != nil {
		return openErr
	}
	defer sourceFile.Close()
	_, copyErr := io.Copy(tarWriter, sourceFile)
	return copyErr
}

// tarExtract tar/tar.gz 解压管线入口（core.Extract 的 tar 分支；源可为普通文件或分卷拼接流）：
// 预扫全验（路径穿越/链接逃逸/设备类型/重名）→ 智能解压定根 → 顺序流式写出（有界内存）。
// tar 单流顺序格式无随机访问：jobs 不参与（gzip 单流天然串行），预扫 + 解出共两遍读。
func tarExtract(source archiveSource, tarVariant Format, options ExtractOptions, logf Logf) (ExtractResult, error) {
	compression := tarCompressionDecoder(tarVariant)
	scannedTarEntries, scanErr := tarScanArchive(source, compression, logf)
	if scanErr != nil {
		return ExtractResult{}, scanErr
	}

	rootEntries := make([]scannedEntry, 0, len(scannedTarEntries))
	var totalFiles int64
	var totalBytes int64
	for _, tarEntry := range scannedTarEntries {
		rootEntries = append(rootEntries, scannedEntry{cleanName: tarEntry.cleanName, isDirEntry: tarEntry.isDirEntry})
		if tarEntry.entryKind != tarKindDir {
			totalFiles++
			totalBytes += tarEntry.entrySize
		}
	}

	targetRoot, rootErr := resolveTargetRoot(options.OutputDir, source.displayPath, rootEntries)
	if rootErr != nil {
		return ExtractResult{}, rootErr
	}
	absoluteTargetRoot, absErr := filepath.Abs(targetRoot)
	if absErr != nil {
		absoluteTargetRoot = targetRoot
	}
	if mkdirErr := os.MkdirAll(absoluteTargetRoot, 0o755); mkdirErr != nil {
		return ExtractResult{}, fmt.Errorf("创建输出目录失败: %s", mkdirErr.Error())
	}

	progressState := newProgressReporter(totalFiles, totalBytes, logf)
	stopProgress := progressState.start("解压中")
	var skippedFiles atomic.Int64
	extractErr := tarExtractStream(source, compression, absoluteTargetRoot, options.Force, progressState, &skippedFiles, logf)
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

// tarExtractStream 第二遍：重新开流逐条落地。串行流式（预扫已全验，此处校验理论上不再触发——
// 触发即归档在两遍之间被改动，如实报错）
func tarExtractStream(source archiveSource, compression singleStreamDecoder, absoluteRoot string, forceOverwrite bool, progressState *progressReporter, skippedFiles *atomic.Int64, logf Logf) error {
	tarReader, compressionCloser, openErr := openTarStream(source, compression)
	if openErr != nil {
		return openErr
	}
	if compressionCloser != nil {
		defer compressionCloser.Close()
	}
	var pendingSymlinks []tarScannedEntry // Windows 无特权降级拷贝的前向引用待办

	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			if flushErr := flushPendingSymlinkCopies(pendingSymlinks, absoluteRoot, forceOverwrite, progressState, skippedFiles, logf); flushErr != nil {
				return flushErr
			}
			return nil
		}
		if nextErr != nil {
			return nextErr
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if cancelErr := checkCancelled(); cancelErr != nil {
			return cancelErr
		}
		entry, entryErr := tarEntryFromHeader(header, false, logf)
		if entryErr != nil {
			return entryErr
		}
		if entry.cleanName == "" {
			continue
		}
		targetPath := filepath.Join(absoluteRoot, filepath.FromSlash(entry.cleanName))
		switch entry.entryKind {
		case tarKindDir:
			if mkdirErr := os.MkdirAll(targetPath, 0o755); mkdirErr != nil {
				return mkdirErr
			}
		case tarKindFile:
			skippedFile, writeErr := tarWriteOutFile(tarReader, targetPath, entry.entryMode, forceOverwrite, skippedFiles, logf)
			if writeErr != nil {
				return fmt.Errorf("写出 %s 失败: %s", entry.cleanName, writeErr.Error())
			}
			if !skippedFile {
				progressState.doneFiles.Add(1)
				progressState.doneBytes.Add(entry.entrySize)
			}
		case tarKindSymlink:
			skippedLink, linkErr := tarCreateSymlink(targetPath, entry.linkName, forceOverwrite, skippedFiles, logf)
			if linkErr == nil {
				if !skippedLink {
					progressState.doneFiles.Add(1)
				}
				continue
			}
			if !isSymlinkPrivilegeError(linkErr) {
				return fmt.Errorf("写出符号链接 %s 失败: %s", entry.cleanName, linkErr.Error())
			}
			// Windows 无符号链接特权（未开开发者模式）：降级为拷贝归档内目标成员——业界
			// bsdtar 同款降级；绝对/逃逸目标预扫已拒，目标必在归档根内。目标可能前向
			// 引用（尚未解出）——先试即拷，不成立入待办，流结束后统一补拷（2026-11-08 修复）
			reportLogf(logf, "Windows 无符号链接特权，降级为内容拷贝: %s -> %s", entry.cleanName, entry.linkName)
			resolvedName := path.Join(path.Dir(entry.cleanName), entry.linkName)
			sourcePath := filepath.Join(absoluteRoot, filepath.FromSlash(resolvedName))
			if _, statErr := os.Lstat(sourcePath); statErr == nil {
				skippedCopy, copyErr := tarCopyExtractedFile(sourcePath, targetPath, forceOverwrite, skippedFiles, logf)
				if copyErr != nil {
					return fmt.Errorf("写出符号链接 %s 失败: %s", entry.cleanName, copyErr.Error())
				}
				if !skippedCopy {
					progressState.doneFiles.Add(1)
				}
			} else {
				pendingSymlinks = append(pendingSymlinks, entry)
			}
		case tarKindHardlink:
			sourcePath := filepath.Join(absoluteRoot, filepath.FromSlash(entry.linkName))
			skippedHard, copyErr := tarCopyExtractedFile(sourcePath, targetPath, forceOverwrite, skippedFiles, logf)
			if copyErr != nil {
				return fmt.Errorf("写出硬链接 %s 失败: %s", entry.cleanName, copyErr.Error())
			}
			if !skippedHard {
				progressState.doneFiles.Add(1)
			}
		}
	}
}

// flushPendingSymlinkCopies 符号链接特权降级的前向引用收尾：流读完后目标必然全部解出（若
// 目标条目本身缺失，tarCopyExtractedFile 如实报「目标未解出」——与硬链接同语义）
func flushPendingSymlinkCopies(entries []tarScannedEntry, absoluteRoot string, forceOverwrite bool, progressState *progressReporter, skippedFiles *atomic.Int64, logf Logf) error {
	for _, entry := range entries {
		resolvedName := path.Join(path.Dir(entry.cleanName), entry.linkName)
		sourcePath := filepath.Join(absoluteRoot, filepath.FromSlash(resolvedName))
		targetPath := filepath.Join(absoluteRoot, filepath.FromSlash(entry.cleanName))
		skippedCopy, copyErr := tarCopyExtractedFile(sourcePath, targetPath, forceOverwrite, skippedFiles, logf)
		if copyErr != nil {
			return fmt.Errorf("写出符号链接 %s 失败: %s", entry.cleanName, copyErr.Error())
		}
		if !skippedCopy {
			progressState.doneFiles.Add(1)
		}
	}
	return nil
}

// isSymlinkPrivilegeError 判定符号链接创建失败是否因 Windows 无特权（ERROR_PRIVILEGE_NOT_
// HELD=1314：未开开发者模式/非管理员）。常量字面量跨平台不可引用（syscall 仅 Windows 导出），
// 故按数值比较——unix 上 os.Symlink 不返回此码，永不误判
func isSymlinkPrivilegeError(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == 1314
}

// tarWriteOutFile 顺序写出普通文件：O_EXCL 原子探测跳过已存在 / force 截断重写（与 zip 同语义）
func tarWriteOutFile(entryReader io.Reader, targetPath string, entryMode int64, forceOverwrite bool, skippedFiles *atomic.Int64, logf Logf) (bool, error) {
	if mkdirErr := os.MkdirAll(filepath.Dir(targetPath), 0o755); mkdirErr != nil {
		return false, mkdirErr
	}
	entryPerm := os.FileMode(entryMode).Perm()
	if entryPerm == 0 {
		entryPerm = 0o644
	}
	openFlags := os.O_WRONLY | os.O_CREATE
	if forceOverwrite {
		openFlags |= os.O_TRUNC
	} else {
		openFlags |= os.O_EXCL // 原子探测：已存在即跳过，无 TOCTOU
	}
	targetFile, createErr := os.OpenFile(targetPath, openFlags, entryPerm)
	if createErr != nil {
		if !forceOverwrite && os.IsExist(createErr) {
			reportLogf(logf, "跳过已存在: %s", targetPath)
			skippedFiles.Add(1)
			return true, nil
		}
		return false, createErr
	}
	_, copyErr := io.Copy(targetFile, entryReader)
	closeErr := targetFile.Close()
	if copyErr != nil {
		return false, copyErr
	}
	return false, closeErr
}

// tarCreateSymlink 落地符号链接：EEXIST 即跳过（非 force）/删旧重建（force）——os.Symlink 无
// O_EXCL 等价物，用错误码原子判定；链接目标原样保留（相对链接语义才正确）
func tarCreateSymlink(targetPath string, linkTarget string, forceOverwrite bool, skippedFiles *atomic.Int64, logf Logf) (bool, error) {
	if mkdirErr := os.MkdirAll(filepath.Dir(targetPath), 0o755); mkdirErr != nil {
		return false, mkdirErr
	}
	symlinkErr := os.Symlink(linkTarget, targetPath)
	if symlinkErr == nil {
		return false, nil
	}
	if os.IsExist(symlinkErr) && !forceOverwrite {
		reportLogf(logf, "跳过已存在: %s", targetPath)
		skippedFiles.Add(1)
		return true, nil
	}
	if os.IsExist(symlinkErr) {
		if removeErr := os.Remove(targetPath); removeErr != nil {
			return false, removeErr
		}
		return false, os.Symlink(linkTarget, targetPath)
	}
	return false, symlinkErr
}

// tarCopyExtractedFile 硬链接落地：复制归档内已解出的目标成员（内容拷贝，跳过/覆盖语义与
// 普通文件一致——不依赖 os.Link 是为了 Windows 无特权场景同样可用）
func tarCopyExtractedFile(sourcePath string, targetPath string, forceOverwrite bool, skippedFiles *atomic.Int64, logf Logf) (bool, error) {
	sourceFile, openErr := os.Open(sourcePath)
	if openErr != nil {
		return false, fmt.Errorf("硬链接目标未解出: %s", sourcePath)
	}
	sourceInfo, statErr := sourceFile.Stat()
	if statErr != nil {
		sourceFile.Close()
		return false, statErr
	}
	skipped, copyErr := tarWriteOutFile(sourceFile, targetPath, int64(sourceInfo.Mode().Perm()), forceOverwrite, skippedFiles, logf)
	sourceFile.Close()
	return skipped, copyErr
}

// isUnsafeSymlinkTarget 符号链接逃逸判定：目标为绝对路径/盘符直接拒；相对目标以条目所在
// 目录为锚点解析后逃出归档根（.. 开头）也拒——放行归档内部的相对链接（如 libfoo -> libfoo.1）
func isUnsafeSymlinkTarget(entryName string, linkTarget string) bool {
	slashTarget := strings.ReplaceAll(linkTarget, "\\", "/")
	if strings.HasPrefix(slashTarget, "/") {
		return true
	}
	for _, segment := range strings.Split(slashTarget, "/") {
		if len(segment) == 2 && segment[1] == ':' { // Windows 盘符（C:）
			return true
		}
	}
	resolved := path.Clean(path.Join(path.Dir(entryName), slashTarget))
	return strings.HasPrefix(resolved, "../") || resolved == ".."
}
