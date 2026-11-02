// zip 读写管线：写入并行架构（条目级并行 + >16MB 文件 128KB 分块并行 +
// 魔数 Store 直通），读取并行 + GBK 文件名修复 + 智能解压 + zip-slip 防护。
// 全部错误 error 返回、过程日志走 logf（库形态，见 core.go 包注释）。
package core

import (
	"archive/zip"
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	kflate "github.com/klauspost/compress/flate"
)

// ZIP_DEFLATE_CHUNK_BYTES 大单文件分块并行压缩的分块粒度（128KB，缝合点严格等距；
// pigz 同款架构）
const ZIP_DEFLATE_CHUNK_BYTES int64 = 128 << 10

// ZIP_ENTRY_BUFFER_CAP_BYTES 单条目内存缓冲上限 = 分块并行管线触发阈值：
// 超过它的大文件走 128KB 分块并行，以下小文件整条目进缓冲——对齐业界主流
// ZIP_SaveFileDataCompress_deflate 的 0x40001（256KB+1 即切多线程 deflate）
const ZIP_ENTRY_BUFFER_CAP_BYTES int64 = 0x40001

// ZIP_CHUNK_DICT_BYTES 分块字典衔接长度：每块 deflate 预置前块尾部 32KB 字典——
// 业界主流并行压缩器实证（deflateReset + deflateSetDictionary），
// 跨块匹配合法性的依据是连续解码器滑窗内含同源字节
const ZIP_CHUNK_DICT_BYTES int64 = 32 << 10

// ZIP_HSA_MIN_UNCOMP_BYTES High Speed Archiving 运行时降档检查点：累计未压缩满 30MiB
// （0x1e00000，业界主流生产者循环值）后开始检查比值
const ZIP_HSA_MIN_UNCOMP_BYTES int64 = 0x1e00000

// ZIP_HSA_RATIO_PCT 运行时降档阈值：压缩后×100/原始 ≥ 99（不可压实证）→ 剩余块降 level 0
const ZIP_HSA_RATIO_PCT int64 = 99

// ZIP_COMPRESS_MAX_WORKERS 压缩并行度上限：8 个在飞条目 × 16MB = 128MB 内存预算（长跑不涨内存的界）
const ZIP_COMPRESS_MAX_WORKERS = 8

// ZIP_EXTRACT_MAX_WORKERS 解压并行度上限 32——对齐业界主流 CArkParaExtMan 的
// 32 槽位线程上下文数组（二进制逆向证据：SParaExtContext[32]），其公式为
// min(用户设置 || 核数/2, 文件数, 32)
const ZIP_EXTRACT_MAX_WORKERS = 32

// ZIP_ENTRY_HEADER_SNIFF_LEN 魔数佐证需要读到的文件头长度（最长魔数在偏移 8 + 4 字节）
const ZIP_ENTRY_HEADER_SNIFF_LEN = 16

// zipIncompressibleExts 已压缩格式的扩展名（High Speed Archiving）：重压无收益，Store 直通
var zipIncompressibleExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".mp4": true, ".m4v": true, ".mov": true, ".mkv": true, ".avi": true,
	".mp3": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true,
	".zip": true, ".gz": true, ".bz2": true, ".xz": true, ".zst": true, ".7z": true, ".rar": true,
	".pdf": true, ".jar": true, ".dmg": true, ".iso": true, ".woff": true, ".woff2": true,
}

// zipMagicSniff 单条魔数佐证规则：offset 处按掩码比对 prefix（掩码空串表示逐字节精确比对）
type zipMagicSniff struct {
	extensions []string
	offset     int
	prefix     string
	mask       string
}

// zipMagicSniffs 魔数佐证表：扩展名命中不可压清单后再读文件头比对，对不上仍走 Deflate（防改错扩展名被 Store 放大）
var zipMagicSniffs = []zipMagicSniff{
	{extensions: []string{".jpg", ".jpeg"}, prefix: "\xff\xd8\xff"},
	{extensions: []string{".png"}, prefix: "\x89PNG"},
	{extensions: []string{".gif"}, prefix: "GIF8"},
	{extensions: []string{".webp"}, offset: 8, prefix: "WEBP"},
	{extensions: []string{".avi"}, offset: 8, prefix: "AVI "},
	{extensions: []string{".mkv"}, prefix: "\x1aE\xdf\xa3"},
	{extensions: []string{".mp4", ".m4v", ".mov", ".m4a"}, offset: 4, prefix: "ftyp"},
	{extensions: []string{".mp3"}, prefix: "\xff\xe0", mask: "\xff\xe0"},
	{extensions: []string{".mp3", ".aac"}, prefix: "ID3"},
	{extensions: []string{".aac"}, prefix: "\xff\xf0", mask: "\xff\xf0"},
	{extensions: []string{".ogg"}, prefix: "OggS"},
	{extensions: []string{".flac"}, prefix: "fLaC"},
	{extensions: []string{".zip", ".jar"}, prefix: "PK\x03\x04"},
	{extensions: []string{".gz"}, prefix: "\x1f\x8b"},
	{extensions: []string{".bz2"}, prefix: "BZh"},
	{extensions: []string{".xz"}, prefix: "\xfd7zXZ"},
	{extensions: []string{".zst"}, prefix: "\x28\xb5\x2f\xfd"},
	{extensions: []string{".7z"}, prefix: "7z\xbc\xaf"},
	{extensions: []string{".rar"}, prefix: "Rar!"},
	{extensions: []string{".pdf"}, prefix: "%PDF"},
	{extensions: []string{".woff"}, prefix: "wOFF"},
	{extensions: []string{".woff2"}, prefix: "wOF2"},
}

// zipEntryResult 并行压缩产物：预压缩字节流 + CRC/尺寸，供 CreateRaw 直写
type zipEntryResult struct {
	method            uint16
	crc32Value        uint32
	uncompressedBytes int64
	compressedData    []byte
	degraded          bool // 条目实际超出缓冲上限（collect 后文件变大等），主循环降级顺序流式写
}

// zipEntryOutcome 工作协程经每任务独立通道回传的结果或错误
type zipEntryOutcome struct {
	result  zipEntryResult
	err     error
	taskIdx int
}

// reportLogf logf 非 nil 才输出（库形态调用方静默）
func reportLogf(logf Logf, format string, args ...any) {
	if logf != nil {
		logf(format, args...)
	}
}

// zipSniffMatches 单条魔数规则比对：头太短或对不上都算不命中
func zipSniffMatches(sniff zipMagicSniff, headerBytes []byte) bool {
	if sniff.offset+len(sniff.prefix) > len(headerBytes) {
		return false // 头太短读不到魔数：不赌
	}
	for byteIndex := 0; byteIndex < len(sniff.prefix); byteIndex++ {
		headerByte := headerBytes[sniff.offset+byteIndex]
		maskByte := byte(0xff)
		if byteIndex < len(sniff.mask) {
			maskByte = sniff.mask[byteIndex]
		}
		if headerByte&maskByte != sniff.prefix[byteIndex]&maskByte {
			return false
		}
	}
	return true
}

// zipDecideStoreMethod High Speed Archiving 判定：扩展名在不可压清单且魔数佐证通过
// （同扩展名可能有多条规则——如 mp3 的 ID3 头与帧同步头——任一命中即过；无表项则信任扩展名）→ Store 直通
func zipDecideStoreMethod(entryName string, headerBytes []byte) bool {
	extension := strings.ToLower(filepath.Ext(entryName))
	if !zipIncompressibleExts[extension] {
		return false
	}
	hasSniffRule := false
	for _, sniff := range zipMagicSniffs {
		if !zipExtensionListed(extension, sniff.extensions) {
			continue
		}
		hasSniffRule = true
		if zipSniffMatches(sniff, headerBytes) {
			return true
		}
	}
	return !hasSniffRule // 有规则但全不命中：扩展名改错的文件，仍走 Deflate
}

// zipExtensionListed 扩展名是否在魔数规则的扩展名清单里
func zipExtensionListed(extension string, extensions []string) bool {
	for _, listedExtension := range extensions {
		if extension == listedExtension {
			return true
		}
	}
	return false
}

// zipCompressEntry 单条目压缩（工作协程内执行）：读魔数定 Store/Deflate，产出预压缩字节流 + CRC/尺寸。
// 硬界：只读 ZIP_ENTRY_BUFFER_CAP_BYTES+1 字节，超限置 degraded 由主循环降级顺序流式写。
func zipCompressEntry(task compressTask, compressionLevel int) (zipEntryResult, error) {
	if !utf8.ValidString(task.entryName) {
		return zipEntryResult{}, fmt.Errorf("文件名不是有效 UTF-8，无法安全写入 zip: %q", task.entryName)
	}
	entryFile, openErr := os.Open(task.sourcePath)
	if openErr != nil {
		return zipEntryResult{}, openErr
	}
	defer entryFile.Close()

	headerBytes := make([]byte, ZIP_ENTRY_HEADER_SNIFF_LEN)
	headerReadLen, _ := io.ReadFull(entryFile, headerBytes)
	if _, seekErr := entryFile.Seek(0, io.SeekStart); seekErr != nil {
		return zipEntryResult{}, seekErr
	}
	useStore := compressionLevel == 0 || zipDecideStoreMethod(task.entryName, headerBytes[:headerReadLen])

	cappedReader := io.LimitReader(entryFile, ZIP_ENTRY_BUFFER_CAP_BYTES+1)
	var compressedBuffer bytes.Buffer
	crc32Writer := crc32.NewIEEE()
	var result zipEntryResult
	if useStore {
		writtenCount, copyErr := io.Copy(io.MultiWriter(&compressedBuffer, crc32Writer), cappedReader)
		if copyErr != nil {
			return zipEntryResult{}, copyErr
		}
		result = zipEntryResult{method: zip.Store, crc32Value: crc32Writer.Sum32(), uncompressedBytes: writtenCount}
	} else {
		flateWriter, flateErr := kflate.NewWriter(&compressedBuffer, compressionLevel)
		if flateErr != nil {
			return zipEntryResult{}, flateErr
		}
		writtenCount, copyErr := io.Copy(io.MultiWriter(flateWriter, crc32Writer), cappedReader)
		closeErr := flateWriter.Close()
		if copyErr != nil {
			return zipEntryResult{}, copyErr
		}
		if closeErr != nil {
			return zipEntryResult{}, closeErr
		}
		result = zipEntryResult{method: zip.Deflate, crc32Value: crc32Writer.Sum32(), uncompressedBytes: writtenCount}
	}
	if result.uncompressedBytes > ZIP_ENTRY_BUFFER_CAP_BYTES {
		result.degraded = true
		return result, nil
	}
	result.compressedData = compressedBuffer.Bytes()
	return result, nil
}

// zipWriteSequentialEntry 顺序流式写单个条目（大文件降级路径）：不囤缓冲，边读边写
func zipWriteSequentialEntry(zipWriter *zip.Writer, task compressTask, compressionLevel int) error {
	if !utf8.ValidString(task.entryName) {
		return fmt.Errorf("文件名不是有效 UTF-8，无法安全写入 zip: %q", task.entryName)
	}
	entryFile, openErr := os.Open(task.sourcePath)
	if openErr != nil {
		return openErr
	}
	defer entryFile.Close()

	headerBytes := make([]byte, ZIP_ENTRY_HEADER_SNIFF_LEN)
	headerReadLen, _ := io.ReadFull(entryFile, headerBytes)
	if _, seekErr := entryFile.Seek(0, io.SeekStart); seekErr != nil {
		return seekErr
	}

	entryHeader, headerErr := zip.FileInfoHeader(task.fileInfo)
	if headerErr != nil {
		return headerErr
	}
	entryHeader.Name = filepath.ToSlash(task.entryName) // zip 规范恒用正斜杠
	entryHeader.Method = zip.Deflate
	if compressionLevel == 0 || zipDecideStoreMethod(task.entryName, headerBytes[:headerReadLen]) {
		entryHeader.Method = zip.Store
	}
	entryHeader.Flags |= 0x800 // 显式置 EFS 标志位（bit 11）：声明文件名为 UTF-8

	entryWriter, entryErr := zipWriter.CreateHeader(entryHeader)
	if entryErr != nil {
		return entryErr
	}
	_, copyErr := io.Copy(entryWriter, entryFile)
	return copyErr
}

// zipEntryPlan 每条目的压缩路径规划
type zipEntryPlan int

const (
	zipPlanEntryStream zipEntryPlan = iota // ≤缓冲上限：整条目进缓冲（现有并行路径）
	zipPlanChunked                         // >缓冲上限且非 Store：128KB 分块并行
	zipPlanSequential                      // >缓冲上限的 Store 候选 / 非法 UTF-8 名：顺序流式
)

// zipEntryIsStoreByMagic 读文件头做 High Speed Archiving 判定（大文件规划用）
func zipEntryIsStoreByMagic(task compressTask) bool {
	entryFile, openErr := os.Open(task.sourcePath)
	if openErr != nil {
		return false
	}
	defer entryFile.Close()
	headerBytes := make([]byte, ZIP_ENTRY_HEADER_SNIFF_LEN)
	headerReadLen, _ := io.ReadFull(entryFile, headerBytes)
	return zipDecideStoreMethod(task.entryName, headerBytes[:headerReadLen])
}

// zipFirstBytesArePDF 大文件分块前的 %PDF 豁免判定（HSA 运行时降档不适用于 PDF——
// 业界主流压缩器 生产者循环逆向：首块魔数 0x46445025 → param5(HSA)=0）
func zipFirstBytesArePDF(sourcePath string) bool {
	entryFile, openErr := os.Open(sourcePath)
	if openErr != nil {
		return false
	}
	defer entryFile.Close()
	headerBytes := make([]byte, 4)
	readLen, _ := io.ReadFull(entryFile, headerBytes)
	return readLen >= 4 && string(headerBytes) == "%PDF"
}

// zipPlanForTask 规划条目路径：小文件整条目缓冲；大文件按魔数分 Store 顺序 / 分块并行。
// 加密模式下全部走缓冲（加密需要完整 deflate 输出才能包 AES 层）
func zipPlanForTask(task compressTask, compressionLevel int, hasPassword bool) zipEntryPlan {
	if hasPassword {
		return zipPlanEntryStream // 加密条目：统一缓冲路径（deflate→加密→CreateRaw）
	}
	if !utf8.ValidString(task.entryName) {
		return zipPlanSequential // 由顺序路径给出与旧版一致的错误信息
	}
	if task.fileInfo.Size() <= ZIP_ENTRY_BUFFER_CAP_BYTES {
		return zipPlanEntryStream
	}
	if compressionLevel == 0 || zipEntryIsStoreByMagic(task) {
		return zipPlanSequential // Store 零 CPU，顺序拷贝即 I/O 界，无需分块
	}
	return zipPlanChunked
}

// zipPoolTask 工作池任务：小文件整条目 或 大文件 128KB 分片
type zipPoolTask struct {
	entryTask *compressTask
	chunkTask *zipChunkTask
}

// zipChunkTask 大文件的一个分片：按 offset/length 从源文件读出独立压缩；
// level 为本片生效档位（HSA 运行时降档会把剩余片降 0）
type zipChunkTask struct {
	group    *zipChunkGroup
	chunkIdx int64
	offset   int64
	length   int64
	isLast   bool
	level    int
}

// zipChunkOutcome 分片压缩产物
type zipChunkOutcome struct {
	compressedData []byte
	crc32Value     uint32
	readBytes      int64
	err            error
}

// zipChunkGroup 一个大文件的全部分片上下文（主协程消费，按序拼装）
type zipChunkGroup struct {
	sourcePath string
	entryName  string
	fileInfo   fs.FileInfo
	chunkCount int64
	outcomes   []chan zipChunkOutcome
	// HSA 运行时降档（业界主流压缩器 生产者循环同构；%PDF 首块豁免）
	hsaEligible bool
	// 装配快照信号（确定性降档的核心）：装配器每消费一片，把该时点的累计
	// 未压缩/已压缩字节作为快照发进通道；派发器派第 k 片前先收第 k-W+1 个
	// 快照——决策输入被钉死为"前 k-W 片的累计比值"（W=workerCount，固定窗口），
	// 与异步时序解耦，同输入必然同决策。容量 W 保证装配器永不阻塞。
	progressSignals chan zipChunkProgress
}

// zipChunkProgress 装配进度快照（累计值，装配器按序计算后发送）
type zipChunkProgress struct {
	uncompressedTotal int64
	compressedTotal   int64
	consumedCount     int64
}

// zipDeflateChunkBytes 压缩单个分片：dictionary 非空时预置（跨块衔接——业界主流压缩器 WriteDone
// 同构：deflateReset + deflateSetDictionary(前块尾 ≤32KB)，raw deflate 模式字典不入流，
// 连续解码器滑窗含同源字节故距离引用合法）；非末块以 Flush() 收尾（sync-flush 空块缝合点，
// 字节指纹 00 00 00 FF FF）；末块以 Close() 收尾（final 块）
func zipDeflateChunkBytes(chunkData []byte, compressionLevel int, isLastChunk bool, dictionary []byte) ([]byte, error) {
	var chunkBuffer bytes.Buffer
	var flateWriter *kflate.Writer
	var flateErr error
	if len(dictionary) > 0 {
		flateWriter, flateErr = kflate.NewWriterDict(&chunkBuffer, compressionLevel, dictionary)
	} else {
		flateWriter, flateErr = kflate.NewWriter(&chunkBuffer, compressionLevel)
	}
	if flateErr != nil {
		return nil, flateErr
	}
	if _, writeErr := flateWriter.Write(chunkData); writeErr != nil {
		return nil, writeErr
	}
	var finishErr error
	if isLastChunk {
		finishErr = flateWriter.Close()
	} else {
		finishErr = flateWriter.Flush()
	}
	if finishErr != nil {
		return nil, finishErr
	}
	return chunkBuffer.Bytes(), nil
}

// zipGf2MatrixTimes GF(2) 矩阵×向量（zlib crc32_combine 核心算法，公开移植）
func zipGf2MatrixTimes(matrix []uint32, vector uint32) uint32 {
	var sum uint32
	for matrixIdx := 0; vector != 0; matrixIdx++ {
		if vector&1 == 1 {
			sum ^= matrix[matrixIdx]
		}
		vector >>= 1
	}
	return sum
}

// zipGf2MatrixSquare GF(2) 矩阵平方
func zipGf2MatrixSquare(square []uint32, matrix []uint32) {
	for rowIdx := 0; rowIdx < 32; rowIdx++ {
		square[rowIdx] = zipGf2MatrixTimes(matrix, matrix[rowIdx])
	}
}

// zipCrc32Combine 拼接两段数据的 CRC32（IEEE 多项式）：
// combine(crc(a), crc(b), len(b)) == crc32(a||b)——zlib crc32_combine 算法，免二次读盘
func zipCrc32Combine(crcFirst uint32, crcSecond uint32, secondLength int64) uint32 {
	if secondLength <= 0 {
		return crcFirst
	}
	var evenMatrix [32]uint32
	var oddMatrix [32]uint32
	oddMatrix[0] = 0xedb88320 // CRC-32 反射多项式
	var rowValue uint32 = 1
	for matrixIdx := 1; matrixIdx < 32; matrixIdx++ {
		oddMatrix[matrixIdx] = rowValue
		rowValue <<= 1
	}
	zipGf2MatrixSquare(evenMatrix[:], oddMatrix[:])
	zipGf2MatrixSquare(oddMatrix[:], evenMatrix[:])
	for {
		zipGf2MatrixSquare(evenMatrix[:], oddMatrix[:])
		if secondLength&1 == 1 {
			crcFirst = zipGf2MatrixTimes(evenMatrix[:], crcFirst)
		}
		secondLength >>= 1
		if secondLength == 0 {
			break
		}
		zipGf2MatrixSquare(oddMatrix[:], evenMatrix[:])
		if secondLength&1 == 1 {
			crcFirst = zipGf2MatrixTimes(oddMatrix[:], crcFirst)
		}
		secondLength >>= 1
		if secondLength == 0 {
			break
		}
	}
	return crcFirst ^ crcSecond
}

// zipAssembleChunkedEntry 主协程按序消费分片产物：拼入临时文件（有界内存），
// CRC 逐块拼合，最后 CreateRaw 一次性入包。releaseWindow 在每消费一片后弹出一个窗口令牌
// （逐块释放，产线被窗口背压卡住时主循环的消费即解锁它）
func zipAssembleChunkedEntry(zipWriter *zip.Writer, group *zipChunkGroup, releaseWindow func()) (writtenBytes int64, assembleErr error) {
	tempFile, tempErr := os.CreateTemp(resolveTempBase(), "zpack-zip-chunk-*.part")
	if tempErr != nil {
		return 0, tempErr
	}
	tempPath := tempFile.Name()
	defer func() {
		tempFile.Close()
		os.Remove(tempPath) // 正常/失败路径统一清理半成品
	}()

	var combinedCRC uint32
	var uncompressedTotal int64
	var compressedTotal int64
	for chunkIdx := int64(0); chunkIdx < group.chunkCount; chunkIdx++ {
		outcome := <-group.outcomes[chunkIdx]
		if outcome.err != nil {
			return 0, outcome.err
		}
		if _, writeErr := tempFile.Write(outcome.compressedData); writeErr != nil {
			return 0, writeErr
		}
		combinedCRC = zipCrc32Combine(combinedCRC, outcome.crc32Value, outcome.readBytes)
		uncompressedTotal += outcome.readBytes
		compressedTotal += int64(len(outcome.compressedData))
		// 快照信号：累计值先行、发送在后（happens-before），派发器收到即读到一致快照
		group.progressSignals <- zipChunkProgress{
			uncompressedTotal: uncompressedTotal,
			compressedTotal:   compressedTotal,
			consumedCount:     chunkIdx + 1,
		}
		releaseWindow() // 消费一片还一片的窗口令牌
	}
	entryHeader := &zip.FileHeader{
		Name:               filepath.ToSlash(group.entryName),
		Method:             zip.Deflate,
		CRC32:              combinedCRC,
		CompressedSize64:   uint64(compressedTotal),
		UncompressedSize64: uint64(uncompressedTotal),
		Modified:           group.fileInfo.ModTime(),
	}
	entryHeader.SetMode(group.fileInfo.Mode().Perm())
	entryHeader.Flags |= 0x800 // 显式置 EFS 标志位（bit 11）：声明文件名为 UTF-8
	entryWriter, entryErr := zipWriter.CreateRaw(entryHeader)
	if entryErr != nil {
		return 0, entryErr
	}
	if _, seekErr := tempFile.Seek(0, io.SeekStart); seekErr != nil {
		return 0, seekErr
	}
	if _, copyErr := io.Copy(entryWriter, tempFile); copyErr != nil {
		return 0, copyErr
	}
	return compressedTotal, nil
}

// zipWriteArchive 并行压缩主管线：小文件整条目进缓冲（CreateRaw），大文件 128KB 分块并行
// （拼临时文件后 CreateRaw），Store 候选顺序流式。窗口信号量保证在飞缓冲有界，同输入产出确定。
func zipWriteArchive(absoluteOutput string, tasks []compressTask, compressionLevel int, jobs int, progressState *progressReporter, password string, passwordResolver func(string) (string, bool), comment string) error {
	zipFile, createErr := os.Create(absoluteOutput)
	if createErr != nil {
		return fmt.Errorf("创建 zip 失败: %s", createErr.Error())
	}
	zipWriter := zip.NewWriter(zipFile)
	if comment != "" {
		zipWriter.SetComment(comment)
	}
	// 每写者注册 klauspost flate（压缩 ~2× 标准库；不动包级注册，免影响其他调用方）
	zipWriter.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return kflate.NewWriter(out, compressionLevel)
	})

	commitArchive := func() error {
		if comment != "" {
			zipWriter.SetComment(comment)
		}
		if flushErr := zipWriter.Close(); flushErr != nil {
			return flushErr
		}
		return zipFile.Close()
	}
	abortArchive := func(reason error) error {
		zipWriter.Close()
		zipFile.Close()
		os.Remove(absoluteOutput)
		return reason
	}

	// 并行度只看 jobs：不按条目数封顶——单文件归档也有 128KB 分片子任务要喂满 worker 池；
	// 空闲 worker 在通道关闭后自行退出，无代价。jobs=1 时整体走顺序路径。
	workerCount := min(jobs, ZIP_COMPRESS_MAX_WORKERS)
	if workerCount <= 1 {
		// 单工作协程等价于顺序路径：直接流式写，省一层缓冲
		for _, task := range tasks {
			if writeErr := zipWriteSequentialEntry(zipWriter, task, compressionLevel); writeErr != nil {
				return abortArchive(fmt.Errorf("写入文件失败: %s", writeErr.Error()))
			}
			progressState.doneFiles.Add(1)
		}
		if commitErr := commitArchive(); commitErr != nil {
			return abortArchive(fmt.Errorf("写入 zip 失败: %s", commitErr.Error()))
		}
		return nil
	}

	// 主协程先做全量规划（含大文件魔数预读），产线只按规划派发、不做 I/O
	entryPlans := make([]zipEntryPlan, len(tasks))
	chunkGroups := make([]*zipChunkGroup, len(tasks))
	for taskIdx := range tasks {
		entryPlans[taskIdx] = zipPlanForTask(tasks[taskIdx], compressionLevel, password != "")
		if entryPlans[taskIdx] == zipPlanChunked {
			sourceSize := tasks[taskIdx].fileInfo.Size()
			chunkCount := (sourceSize + ZIP_DEFLATE_CHUNK_BYTES - 1) / ZIP_DEFLATE_CHUNK_BYTES
			group := &zipChunkGroup{
				sourcePath: tasks[taskIdx].sourcePath,
				entryName:  tasks[taskIdx].entryName,
				fileInfo:   tasks[taskIdx].fileInfo,
				chunkCount: chunkCount,
				outcomes:   make([]chan zipChunkOutcome, chunkCount),
				// HSA 运行时降档资格：首块非 %PDF（业界主流压缩器 生产者循环豁免规则）
				hsaEligible:     !zipFirstBytesArePDF(tasks[taskIdx].sourcePath),
				progressSignals: make(chan zipChunkProgress, workerCount),
			}
			for chunkIdx := int64(0); chunkIdx < chunkCount; chunkIdx++ {
				group.outcomes[chunkIdx] = make(chan zipChunkOutcome, 1)
			}
			chunkGroups[taskIdx] = group
		}
	}

	taskChannel := make(chan zipPoolTask, workerCount)
	outcomeChannels := make([]chan zipEntryOutcome, len(tasks))
	for taskIdx := range tasks {
		outcomeChannels[taskIdx] = make(chan zipEntryOutcome, 1)
	}
	// 窗口信号量：容量 workerCount+2 即「在飞（已派出未写出）任务数」上界；
	// 产线每派一个占一个令牌，主循环每写完一个弹出一个归还。无预填充——
	// 主循环弹出时必有令牌（派出数 ≥ 已写数+1），产线占满即背压等待，无死锁。
	windowSlots := make(chan struct{}, workerCount+2)

	var workerGroup sync.WaitGroup
	for workerIdx := 0; workerIdx < workerCount; workerIdx++ {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for poolTask := range taskChannel {
				if poolTask.entryTask != nil {
					result, compressErr := zipCompressEntry(*poolTask.entryTask, compressionLevel)
					outcomeChannels[poolTask.entryTask.taskIndex] <- zipEntryOutcome{result: result, err: compressErr, taskIdx: poolTask.entryTask.taskIndex}
					continue
				}
				chunk := poolTask.chunkTask
				chunkData := make([]byte, chunk.length)
				sourceFile, openErr := os.Open(chunk.group.sourcePath)
				if openErr != nil {
					chunk.group.outcomes[chunk.chunkIdx] <- zipChunkOutcome{err: openErr}
					continue
				}
				_, readErr := sourceFile.ReadAt(chunkData, chunk.offset)
				var dictionary []byte
				if readErr == nil && chunk.offset > 0 {
					// 跨块字典衔接：读本块之前 ≤32KB 作预置字典（业界主流压缩器 WriteDone 同构）
					dictLen := min(ZIP_CHUNK_DICT_BYTES, chunk.offset)
					dictionary = make([]byte, dictLen)
					if _, dictErr := sourceFile.ReadAt(dictionary, chunk.offset-dictLen); dictErr != nil {
						readErr = dictErr
					}
				}
				sourceFile.Close()
				if readErr != nil {
					// 分片读不满/读不到（源文件在压缩期间被改动等）：报错不硬拼，CRC/尺寸才不会错
					chunk.group.outcomes[chunk.chunkIdx] <- zipChunkOutcome{err: readErr}
					continue
				}
				compressedChunk, deflateErr := zipDeflateChunkBytes(chunkData, chunk.level, chunk.isLast, dictionary)
				if deflateErr != nil {
					chunk.group.outcomes[chunk.chunkIdx] <- zipChunkOutcome{err: deflateErr}
					continue
				}
				chunk.group.outcomes[chunk.chunkIdx] <- zipChunkOutcome{
					compressedData: compressedChunk,
					crc32Value:     crc32.ChecksumIEEE(chunkData),
					readBytes:      int64(len(chunkData)),
				}
			}
		}()
	}
	go func() {
		for taskIdx := range tasks {
			if checkCancelled() != nil {
				close(taskChannel)
				return
			}
			switch entryPlans[taskIdx] {
			case zipPlanEntryStream:
				windowSlots <- struct{}{}
				taskChannel <- zipPoolTask{entryTask: &tasks[taskIdx]}
			case zipPlanChunked:
				group := chunkGroups[taskIdx]
				// HSA 运行时降档（业界主流压缩器 生产者循环同构，参数级对齐）：
				// 派第 k 片前收第 (k-W+1) 个装配快照——决策输入被钉死为
				// 前 k-W 片累计比值（纯函数），同输入同决策；PDF 已豁免则永不降档
				levelDowngraded := false
				for chunkIdx := int64(0); chunkIdx < group.chunkCount; chunkIdx++ {
					chunkLevel := compressionLevel
					if group.hsaEligible && chunkIdx >= int64(workerCount) {
						snapshot := <-group.progressSignals
						if snapshot.uncompressedTotal >= ZIP_HSA_MIN_UNCOMP_BYTES &&
							snapshot.compressedTotal*100 >= snapshot.uncompressedTotal*ZIP_HSA_RATIO_PCT {
							levelDowngraded = true
						}
					}
					if levelDowngraded {
						chunkLevel = 0
					}
					windowSlots <- struct{}{}
					chunkOffset := chunkIdx * ZIP_DEFLATE_CHUNK_BYTES
					chunkLength := ZIP_DEFLATE_CHUNK_BYTES
					if chunkOffset+chunkLength > group.fileInfo.Size() {
						chunkLength = group.fileInfo.Size() - chunkOffset
					}
					taskChannel <- zipPoolTask{chunkTask: &zipChunkTask{
						group:    group,
						chunkIdx: chunkIdx,
						offset:   chunkOffset,
						length:   chunkLength,
						isLast:   chunkIdx == group.chunkCount-1,
						level:    chunkLevel,
					}}
				}
			}
			// zipPlanSequential：不派发，主循环内联处理
		}
		close(taskChannel)
	}()

	for taskIdx := range tasks {
		if cancelErr := checkCancelled(); cancelErr != nil {
			workerGroup.Wait()
			return abortArchive(cancelErr)
		}
		task := tasks[taskIdx]
		switch entryPlans[taskIdx] {
		case zipPlanSequential:
			if writeErr := zipWriteSequentialEntry(zipWriter, task, compressionLevel); writeErr != nil {
				return abortArchive(fmt.Errorf("写入文件失败: %s", writeErr.Error()))
			}
			progressState.doneFiles.Add(1)
		case zipPlanEntryStream:
			outcome := <-outcomeChannels[taskIdx]
			if outcome.err != nil {
				return abortArchive(fmt.Errorf("写入文件失败: %s", outcome.err.Error()))
			}
			if outcome.result.degraded {
				// 实际超出缓冲上限（collect 后文件变大等）：降级顺序流式写
				if writeErr := zipWriteSequentialEntry(zipWriter, task, compressionLevel); writeErr != nil {
					return abortArchive(fmt.Errorf("写入文件失败: %s", writeErr.Error()))
				}
			} else {
				result := outcome.result
				entryData := result.compressedData
				entryMethod := result.method
				entryCRC := result.crc32Value
				var entryExtra []byte
				if password != "" {
					entryPassword := password
					if passwordResolver != nil {
						if resolved, hit := passwordResolver(task.entryName); hit {
							entryPassword = resolved
						}
					}
					encrypted, extra, _, encryptErr := zipEncryptEntryForWrite(entryData, entryPassword, result.uncompressedBytes)
					if encryptErr != nil {
						return abortArchive(fmt.Errorf("加密失败: %s", encryptErr.Error()))
					}
					entryData = encrypted
					entryMethod = 99
					entryCRC = 0 // AE-2 用 HMAC 替代 CRC
					entryExtra = extra
				}
				entryHeader := &zip.FileHeader{
					Name:               filepath.ToSlash(task.entryName),
					Method:             entryMethod,
					CRC32:              entryCRC,
					CompressedSize64:   uint64(len(entryData)),
					UncompressedSize64: uint64(result.uncompressedBytes),
					Modified:           task.fileInfo.ModTime(),
					Extra:              entryExtra,
				}
				entryHeader.SetMode(task.fileInfo.Mode().Perm())
				entryHeader.Flags |= 0x800 // 显式置 EFS 标志位（bit 11）：声明文件名为 UTF-8
				if password != "" {
					entryHeader.Flags |= 0x1       // 加密位
					entryHeader.ReaderVersion = 51 // WinZip AES 要求 5.1（7-Zip 严格校验）
				}
				entryWriter, entryErr := zipWriter.CreateRaw(entryHeader)
				if entryErr != nil {
					return abortArchive(fmt.Errorf("写入文件失败: %s", entryErr.Error()))
				}
				if _, copyErr := entryWriter.Write(entryData); copyErr != nil {
					return abortArchive(fmt.Errorf("写入文件失败: %s", copyErr.Error()))
				}
			}
			progressState.doneFiles.Add(1)
			progressState.doneBytes.Add(outcome.result.uncompressedBytes) // 跟踪输入字节（进度条用）
			<-windowSlots                                                 // 写完弹出令牌，腾出一个窗口位（还产线）
		case zipPlanChunked:
			writtenBytes, assembleErr := zipAssembleChunkedEntry(zipWriter, chunkGroups[taskIdx], func() { <-windowSlots })
			if assembleErr != nil {
				return abortArchive(fmt.Errorf("写入文件失败: %s", assembleErr.Error()))
			}
			progressState.doneFiles.Add(1)
			progressState.doneBytes.Add(writtenBytes)
		}
	}
	workerGroup.Wait()

	if commitErr := commitArchive(); commitErr != nil {
		return abortArchive(fmt.Errorf("写入 zip 失败: %s", commitErr.Error()))
	}
	return nil
}

// zipCompress zip 写入管线入口（core.Compress 的 zip 分支；参数已由 CLI 层解析校验）
func zipCompress(options CompressOptions, logf Logf) (CompressResult, error) {
	ignores := LoadEffectiveZpackignores(options.Inputs)
	tasks, collectErr := collectCompressTasks(options.Inputs, options.Excludes, ignores)
	if collectErr != nil {
		return CompressResult{}, collectErr
	}
	progressState := newProgressReporter(int64(len(tasks)), 0, logf)
	var totalInputBytes int64
	for _, task := range tasks {
		totalInputBytes += task.fileInfo.Size()
	}
	progressState.SetInputBytes(totalInputBytes)
	stopProgress := progressState.start("压缩中")
	writeErr := zipWriteArchive(options.Output, tasks, options.Level, options.Jobs, progressState, options.Password, options.PasswordResolver, options.Comment)
	stopProgress()
	if writeErr != nil {
		return CompressResult{}, writeErr
	}
	outputInfo, statErr := os.Stat(options.Output)
	if statErr != nil {
		return CompressResult{}, fmt.Errorf("读取归档大小失败: %s", statErr.Error())
	}
	return CompressResult{Archive: options.Output, FileCount: len(tasks), SizeBytes: outputInfo.Size()}, nil
}

// zipExtractTask 待解压条目：归档条目 + 解码安全的目标路径
type zipExtractTask struct {
	zipEntry   *zip.File
	targetPath string
	isDirEntry bool
}

// zipDecodedEntry 预扫阶段的解码结果：归档条目 + 归一化名（解码/反斜杠归一/Clean 后）
type zipDecodedEntry struct {
	zipEntry   *zip.File
	cleanName  string
	isDirEntry bool
}

// zipExtractEntry 单条目解压（工作协程内执行）：流式拷出，读尽时 archive/zip 自动验 CRC。
// 覆盖语义：forceOverwrite=true 截断重写；否则 O_EXCL 创建，目标已存在即跳过（计数由调用方汇总）
func zipExtractEntry(task zipExtractTask, forceOverwrite bool, skippedFiles *atomic.Int64, logf Logf) (bool, error) {
	if task.isDirEntry {
		return false, os.MkdirAll(task.targetPath, 0o755)
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(task.targetPath), 0o755); mkdirErr != nil {
		return false, mkdirErr
	}
	entryMode := task.zipEntry.Mode().Perm()
	if entryMode == 0 {
		entryMode = 0o644
	}
	openFlags := os.O_WRONLY | os.O_CREATE
	if forceOverwrite {
		openFlags |= os.O_TRUNC
	} else {
		openFlags |= os.O_EXCL // 原子探测：已存在即跳过，无 TOCTOU
	}
	targetFile, createErr := os.OpenFile(task.targetPath, openFlags, entryMode)
	if createErr != nil {
		if !forceOverwrite && os.IsExist(createErr) {
			reportLogf(logf, "跳过已存在: %s", task.targetPath)
			skippedFiles.Add(1)
			return true, nil
		}
		return false, createErr
	}
	entryReader, openErr := task.zipEntry.Open()
	if openErr != nil {
		targetFile.Close()
		return false, openErr
	}
	_, copyErr := io.Copy(targetFile, entryReader)
	entryReader.Close()
	closeErr := targetFile.Close()
	if copyErr != nil {
		return false, copyErr
	}
	return false, closeErr
}

// zipExtract zip 解压管线入口（core.Extract 的 zip 分支；源可为普通文件或 .001 拼接流）：
// 预扫全验（加密明示/路径安全/重名）→ 智能解压定根 → 并行解出
func zipExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	archiveReader, openErr := zip.NewReader(source.readerAt(), source.size())
	if openErr != nil {
		return ExtractResult{}, fmt.Errorf("无法读取 zip 归档: %s", openErr.Error())
	}

	// 预扫：加密识别（元数据层无密码可见：method 99/extra 0x9901 → WinZip AES；
	// 仅 bit0 → 经典 ZipCrypto）+ 名字解码 + zip-slip 检查（先全验后动工）
	bridgeRegistrations := map[uint16]func(io.Reader) io.ReadCloser{}
	var decodedEntries []zipDecodedEntry
	for entryIdx := range archiveReader.File {
		zipEntry := archiveReader.File[entryIdx]
		if strength, innerMethod, isAES := zipDetectWinZipAES(zipEntry); isAES {
			entryPassword := options.Password
			if options.PasswordResolver != nil {
				if resolved, hit := options.PasswordResolver(zipEntry.Name); hit {
					entryPassword = resolved
				}
			}
			if entryPassword == "" {
				_, _, schemeName, _ := aesStrengthParams(strength)
				return ExtractResult{}, fmt.Errorf("归档已加密（%s）：需要 --password 提供密码", schemeName)
			}
			bridge := zipBridgeAES(strength, innerMethod)
			zipEntry.Method = bridge
			passwordCopy := entryPassword
			innerCopy := innerMethod
			strengthCopy := strength
			if _, exists := bridgeRegistrations[bridge]; !exists {
				bridgeRegistrations[bridge] = func(r io.Reader) io.ReadCloser {
					wrapped, wrapErr := zipWrapInnerDecompress(r, func(src io.Reader) (io.Reader, error) {
						return newZipAESDecodingReader(src, passwordCopy, strengthCopy)
					}, innerCopy)
					if wrapErr != nil {
						return errorReadCloser{wrapErr}
					}
					return wrapped
				}
			}
		} else if zipEntry.Flags&0x1 != 0 {
			entryPassword := options.Password
			if options.PasswordResolver != nil {
				if resolved, hit := options.PasswordResolver(zipEntry.Name); hit {
					entryPassword = resolved
				}
			}
			if entryPassword == "" {
				return ExtractResult{}, fmt.Errorf("归档已加密（%s）：需要 --password 提供密码", CRYPTO_SCHEME_ZIP)
			}
			innerMethod := zipEntry.Method
			expectCheck := byte(zipEntry.CRC32 >> 24)
			if zipEntry.Flags&0x8 != 0 { // 数据描述符模式：校验字节取时间高位（规范）
				expectCheck = byte(zipEntry.Modified.Hour()<<3 | zipEntry.Modified.Minute()>>3)
			}
			bridge := zipBridgeZipCrypto(innerMethod)
			zipEntry.Method = bridge
			passwordCopy := entryPassword
			expectCopy := expectCheck
			innerCopy := innerMethod
			if _, exists := bridgeRegistrations[bridge]; !exists {
				bridgeRegistrations[bridge] = func(r io.Reader) io.ReadCloser {
					wrapped, wrapErr := zipWrapInnerDecompress(r, func(src io.Reader) (io.Reader, error) {
						return newZipCryptoDecodingReader(src, passwordCopy, expectCopy)
					}, innerCopy)
					if wrapErr != nil {
						return errorReadCloser{wrapErr}
					}
					return wrapped
				}
			}
		}
		decodedName, converted := decodeEntryName(zipEntry.Name, zipEntry.Flags&0x800 != 0)
		if isUnsafeEntryPath(zipEntry.Name) || isUnsafeEntryPath(decodedName) {
			return ExtractResult{}, fmt.Errorf("归档含不安全路径: %s", zipEntry.Name)
		}
		if converted {
			reportLogf(logf, "条目名按 GBK 解码: %q -> %q", zipEntry.Name, decodedName)
		}
		isDirEntry := zipEntry.FileInfo().IsDir() || strings.HasSuffix(decodedName, "/")
		cleanName := path.Clean(strings.ReplaceAll(decodedName, "\\", "/"))
		if cleanName == "." || cleanName == "" {
			continue // 空名/根目录占位条目：无实体，跳过
		}
		decodedEntries = append(decodedEntries, zipDecodedEntry{zipEntry: zipEntry, cleanName: cleanName, isDirEntry: isDirEntry})
	}

	for bridgeMethod, decompressor := range bridgeRegistrations {
		archiveReader.RegisterDecompressor(bridgeMethod, decompressor)
	}

	// 重复条目名拒绝：并行写出同名目标会互相践踏，无法安全解压
	seenNames := map[string]bool{}
	for _, entry := range decodedEntries {
		if !entry.isDirEntry && seenNames[entry.cleanName] {
			return ExtractResult{}, fmt.Errorf("归档含重复条目名: %s", entry.cleanName)
		}
		seenNames[entry.cleanName] = true
	}

	// 智能解压（unar 语义，见 resolveTargetRoot）
	rootEntries := make([]scannedEntry, 0, len(decodedEntries))
	var tasks []zipExtractTask
	var totalBytes int64
	for _, entry := range decodedEntries {
		rootEntries = append(rootEntries, scannedEntry{cleanName: entry.cleanName, isDirEntry: entry.isDirEntry})
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
	for _, entry := range decodedEntries {
		targetPath := filepath.Join(absoluteTargetRoot, filepath.FromSlash(entry.cleanName))
		if !entry.isDirEntry {
			totalBytes += int64(entry.zipEntry.UncompressedSize64)
		}
		tasks = append(tasks, zipExtractTask{zipEntry: entry.zipEntry, targetPath: targetPath, isDirEntry: entry.isDirEntry})
	}

	progressState := newProgressReporter(int64(len(tasks)), totalBytes, logf)
	stopProgress := progressState.start("解压中")
	var skippedFiles atomic.Int64

	// 库形态健壮性：调用方未设 Jobs（0/负值）时钳到 1，绝不让 worker 池为空（无消费者即死锁）
	workerCount := min(max(options.Jobs, 1), ZIP_EXTRACT_MAX_WORKERS)
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}
	taskChannel := make(chan zipExtractTask, workerCount)
	var workerGroup sync.WaitGroup
	var firstErrorMutex sync.Mutex
	var firstError error
	setFirstError := func(extractErr error) {
		firstErrorMutex.Lock()
		defer firstErrorMutex.Unlock()
		if firstError == nil {
			firstError = extractErr
		}
	}
	cancelChannel := make(chan struct{})
	for workerIdx := 0; workerIdx < workerCount; workerIdx++ {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for {
				select {
				case <-cancelChannel:
					return
				default:
				}
				task, channelOk := <-taskChannel
				if !channelOk {
					return
				}
				skippedEntry, extractErr := zipExtractEntry(task, options.Force, &skippedFiles, logf)
				if extractErr != nil {
					setFirstError(fmt.Errorf("写出 %s 失败: %s", task.zipEntry.Name, extractErr.Error()))
					close(cancelChannel)
					return
				}
				if !task.isDirEntry && !skippedEntry {
					progressState.doneFiles.Add(1)
					progressState.doneBytes.Add(int64(task.zipEntry.UncompressedSize64))
				}
			}
		}()
	}
	for _, task := range tasks {
		if checkCancelled() != nil {
			close(cancelChannel)
			break
		}
		select {
		case taskChannel <- task:
		case <-cancelChannel:
			// 已有协程失败：停止投喂（剩余条目不再写）
		}
	}
	close(taskChannel)
	workerGroup.Wait()
	stopProgress()
	if firstError != nil {
		return ExtractResult{}, fmt.Errorf("解压失败: %s", firstError.Error())
	}

	return ExtractResult{
		Output:    absoluteTargetRoot,
		FileCount: int(progressState.doneFiles.Load()),
		SizeBytes: progressState.doneBytes.Load(),
		Skipped:   int(skippedFiles.Load()),
	}, nil
}
