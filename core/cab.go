// cab（Microsoft Cabinet）读取：MSCF 容器 + 三种文件夹压缩——NONE 直拷 / MSZIP（raw
// deflate，同 folder 窗口跨 CFDATA 延续、每块前 2 字节 CK 签名）/ LZX（kentalee/go-lzx
// ChunkDecoder：块间保状态，Intel E8 翻译由 LZX 流头自声明）。Quantum 明确报暂不支持；
// 跨柜多卷（iFolder 0xFFFD-0xFFFF）暂不支持。文件夹按需解码到临时文件（有界内存），
// 条目经 SectionReader 直读；串行落地（solid 家族惯例）。
package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	lzx "github.com/kentalee/go-lzx"
	kflate "github.com/klauspost/compress/flate"
)

// CAB_IDENT cab 魔数
const CAB_IDENT = "MSCF"

// cab 头/保留区旗标
const (
	CAB_FLAG_PREV    = 0x0001
	CAB_FLAG_NEXT    = 0x0002
	CAB_FLAG_RESERVE = 0x0004
)

// cab 文件夹压缩类型（typeCompress & 0x0F）
const (
	CAB_COMPTYPE_NONE    = 0
	CAB_COMPTYPE_MSZIP   = 1
	CAB_COMPTYPE_QUANTUM = 2
	CAB_COMPTYPE_LZX     = 3
)

// cabFolder 解析后的文件夹
type cabFolder struct {
	dataOffset  int64
	dataCount   int
	comptype    int
	windowBits  int
	dataReserve int // 每个 CFDATA 8 字节头后的保留字节数（RESERVE_PRESENT 时>0）
	spillPath   string
	spillSize   int64
	spillLoaded bool
}

// cabSkipString 读跳一个 \0 结尾串（ReadAt 定位推进由调用方 offset 累计）
func cabReadCString(source archiveSource, offset int64) (string, int64, error) {
	chunk := make([]byte, 256)
	for totalRead := 0; totalRead < 4096; {
		readLen, readErr := source.readAt(chunk, offset+int64(totalRead))
		if readErr != nil && readLen == 0 {
			return "", 0, fmt.Errorf("无法读取 cab 归档: %s", readErr.Error())
		}
		if endIdx := bytes.IndexByte(chunk[:readLen], 0); endIdx >= 0 {
			return string(chunk[:endIdx]), int64(totalRead + endIdx + 1), nil
		}
		totalRead += readLen
	}
	return "", 0, fmt.Errorf("无法读取 cab 归档: 名字串超长")
}

// cabExtract cab 解压入口（core.Extract 的 cab 分支）
func cabExtract(source archiveSource, options ExtractOptions, logf Logf) (ExtractResult, error) {
	headerBytes := make([]byte, 36)
	if _, headerErr := source.readAt(headerBytes, 0); headerErr != nil || string(headerBytes[0:4]) != CAB_IDENT {
		return ExtractResult{}, fmt.Errorf("无法读取 cab 归档: 魔数不符")
	}
	flags := binary.LittleEndian.Uint16(headerBytes[30:32])
	folderCount := int(binary.LittleEndian.Uint16(headerBytes[26:28]))
	fileCount := int(binary.LittleEndian.Uint16(headerBytes[28:30]))
	pos := int64(36)
	folderReserve, dataReserve := 0, 0
	if flags&CAB_FLAG_RESERVE != 0 {
		reserveBytes := make([]byte, 4)
		if _, reserveErr := source.readAt(reserveBytes, pos); reserveErr != nil {
			return ExtractResult{}, fmt.Errorf("无法读取 cab 归档: %s", reserveErr.Error())
		}
		folderReserve = int(reserveBytes[2])
		dataReserve = int(reserveBytes[3])
		pos += 4 + int64(binary.LittleEndian.Uint16(reserveBytes[0:2]))
	}
	if flags&CAB_FLAG_PREV != 0 {
		if _, adv, err := cabReadCString(source, pos); err == nil {
			pos += adv
			if _, adv2, err2 := cabReadCString(source, pos); err2 == nil {
				pos += adv2
			}
		}
	}
	if flags&CAB_FLAG_NEXT != 0 {
		if _, adv, err := cabReadCString(source, pos); err == nil {
			pos += adv
			if _, adv2, err2 := cabReadCString(source, pos); err2 == nil {
				pos += adv2
			}
		}
	}

	folders := make([]*cabFolder, 0, folderCount)
	for folderIdx := 0; folderIdx < folderCount; folderIdx++ {
		folderBytes := make([]byte, 8)
		if _, folderErr := source.readAt(folderBytes, pos); folderErr != nil {
			return ExtractResult{}, fmt.Errorf("无法读取 cab 归档: %s", folderErr.Error())
		}
		typeCompress := binary.LittleEndian.Uint16(folderBytes[6:8])
		folders = append(folders, &cabFolder{
			dataOffset:  int64(binary.LittleEndian.Uint32(folderBytes[0:4])),
			dataCount:   int(binary.LittleEndian.Uint16(folderBytes[4:6])),
			comptype:    int(typeCompress & 0x0F),
			windowBits:  int(typeCompress >> 8),
			dataReserve: dataReserve,
		})
		pos += 8 + int64(folderReserve)
	}

	var entries []serialEntry
	seenNames := map[string]bool{}
	filePos := int64(binary.LittleEndian.Uint32(headerBytes[16:20]))
	for fileIdx := 0; fileIdx < fileCount; fileIdx++ {
		metaBytes := make([]byte, 16)
		if _, metaErr := source.readAt(metaBytes, filePos); metaErr != nil {
			return ExtractResult{}, fmt.Errorf("无法读取 cab 归档: %s", metaErr.Error())
		}
		fileSize := int64(binary.LittleEndian.Uint32(metaBytes[0:4]))
		folderOffset := int64(binary.LittleEndian.Uint32(metaBytes[4:8]))
		folderIdxValue := int(binary.LittleEndian.Uint16(metaBytes[8:10]))
		attribs := binary.LittleEndian.Uint16(metaBytes[14:16])
		filePos += 16
		if folderIdxValue >= 0xFFFD {
			return ExtractResult{}, fmt.Errorf("暂不支持跨柜多卷 cab（iFolder=%d）", folderIdxValue)
		}
		if folderIdxValue >= folderCount {
			return ExtractResult{}, fmt.Errorf("无法读取 cab 归档: 文件夹索引越界")
		}
		var name string
		if attribs&0x80 != 0 {
			dirName, adv1, err1 := cabReadCString(source, filePos)
			if err1 != nil {
				return ExtractResult{}, err1
			}
			baseName, adv2, err2 := cabReadCString(source, filePos+adv1)
			if err2 != nil {
				return ExtractResult{}, err2
			}
			name = baseName
			if dirName != "" {
				name = dirName + "/" + baseName
			}
			filePos += adv1 + adv2
		} else {
			var adv int64
			var err error
			if name, adv, err = cabReadCString(source, filePos); err != nil {
				return ExtractResult{}, err
			}
			filePos += adv
		}
		cleanName := path.Clean(strings.ReplaceAll(name, "\\", "/"))
		decodedName, converted := decodeEntryName(cleanName, false)
		if isUnsafeEntryPath(cleanName) || isUnsafeEntryPath(decodedName) {
			return ExtractResult{}, fmt.Errorf("归档含不安全路径: %s", cleanName)
		}
		if converted {
			reportLogf(logf, "条目名按 GBK 解码: %q -> %q", cleanName, decodedName)
		}
		if seenNames[decodedName] {
			return ExtractResult{}, fmt.Errorf("归档含重复条目名: %s", decodedName)
		}
		seenNames[decodedName] = true
		folder := folders[folderIdxValue]
		entries = append(entries, serialEntry{
			cleanName: decodedName,
			perm:      cabPermissions(attribs),
			entrySize: fileSize,
			open: func() (io.ReadCloser, error) {
				return cabOpenEntry(source, folder, folderOffset, fileSize)
			},
		})
	}
	defer func() {
		for _, folder := range folders {
			if folder.spillPath != "" {
				os.Remove(folder.spillPath)
			}
		}
	}()
	return serialContainerExtract(source, options, entries, logf)
}

// cabPermissions attribs → 权限位（只读位映射，其余默认 0644）
func cabPermissions(attribs uint16) int64 {
	if attribs&0x01 != 0 {
		return 0o444
	}
	return 0o644
}

// cabOpenEntry 按需解码所属文件夹到临时文件后 SectionReader 直读
func cabOpenEntry(source archiveSource, folder *cabFolder, offset int64, size int64) (io.ReadCloser, error) {
	if !folder.spillLoaded {
		if err := cabDecodeFolder(source, folder); err != nil {
			return nil, err
		}
	}
	spillFile, openErr := os.Open(folder.spillPath)
	if openErr != nil {
		return nil, openErr
	}
	if offset+size > folder.spillSize {
		spillFile.Close()
		return nil, fmt.Errorf("无法读取 cab 归档: 条目越界")
	}
	return &cabSectionReadCloser{section: io.NewSectionReader(spillFile, offset, size), file: spillFile}, nil
}

// cabSectionReadCloser SectionReader + 底层句柄一起关
type cabSectionReadCloser struct {
	section *io.SectionReader
	file    *os.File
}

func (reader *cabSectionReadCloser) Read(buffer []byte) (int, error) {
	return reader.section.Read(buffer)
}
func (reader *cabSectionReadCloser) Close() error { return reader.file.Close() }

// cabDecodeFolder 解码整个文件夹数据流到临时文件（有界内存；三压缩类型分发）
func cabDecodeFolder(source archiveSource, folder *cabFolder) error {
	switch folder.comptype {
	case CAB_COMPTYPE_NONE, CAB_COMPTYPE_MSZIP, CAB_COMPTYPE_LZX:
	default:
		return fmt.Errorf("暂不支持 cab Quantum 压缩文件夹（typeCompress=%d）", folder.comptype)
	}
	spillFile, spillErr := os.CreateTemp(resolveTempBase(), "zpack-cab-*.folder")
	if spillErr != nil {
		return spillErr
	}
	folder.spillPath = spillFile.Name()
	defer spillFile.Close()

	var decoder *lzx.ChunkDecoder
	if folder.comptype == CAB_COMPTYPE_LZX {
		config := lzx.Config{WindowBits: folder.windowBits, IntelE8: lzx.IntelE8Enabled}
		var decodeErr error
		if decoder, decodeErr = lzx.NewChunkDecoder(config); decodeErr != nil {
			return fmt.Errorf("无法读取 cab 归档: LZX 初始化失败: %s", decodeErr.Error())
		}
	}
	if folder.comptype == CAB_COMPTYPE_MSZIP {
		// MSZIP 语义：每个 CFDATA 是独立 deflate 流（CK 签名 + raw deflate），
		// 但 32K 窗口跨块共享——逐块独立解压，前块输出尾部 32K 作预置字典
		dataPos := folder.dataOffset
		var dictionary []byte
		for blockIdx := 0; blockIdx < folder.dataCount; blockIdx++ {
			blockBytes := make([]byte, 8)
			if _, blockErr := source.readAt(blockBytes, dataPos); blockErr != nil {
				return fmt.Errorf("无法读取 cab 归档: %s", blockErr.Error())
			}
			cbData := int64(binary.LittleEndian.Uint16(blockBytes[4:6]))
			payload := make([]byte, cbData)
			if _, payloadErr := source.readAt(payload, dataPos+8+int64(folder.dataReserve)); payloadErr != nil {
				return fmt.Errorf("无法读取 cab 归档: %s", payloadErr.Error())
			}
			if len(payload) < 2 || payload[0] != 'C' || payload[1] != 'K' {
				return fmt.Errorf("无法读取 cab 归档: MSZIP 块签名不符")
			}
			mszipReader := kflate.NewReaderDict(bytes.NewReader(payload[2:]), dictionary)
			out, readErr := io.ReadAll(mszipReader)
			mszipReader.Close()
			if readErr != nil {
				return fmt.Errorf("无法读取 cab 归档: MSZIP 解压失败: %s", readErr.Error())
			}
			if _, writeErr := spillFile.Write(out); writeErr != nil {
				return writeErr
			}
			dictionary = append(dictionary, out...)
			if len(dictionary) > 32768 {
				dictionary = dictionary[len(dictionary)-32768:]
			}
			dataPos += 8 + int64(folder.dataReserve) + cbData
		}
	} else {
		dataPos := folder.dataOffset
		for blockIdx := 0; blockIdx < folder.dataCount; blockIdx++ {
			blockBytes := make([]byte, 8)
			if _, blockErr := source.readAt(blockBytes, dataPos); blockErr != nil {
				return fmt.Errorf("无法读取 cab 归档: %s", blockErr.Error())
			}
			cbData := int64(binary.LittleEndian.Uint16(blockBytes[4:6]))
			cbUncomp := int64(binary.LittleEndian.Uint16(blockBytes[6:8]))
			payload := make([]byte, cbData)
			if _, payloadErr := source.readAt(payload, dataPos+8+int64(folder.dataReserve)); payloadErr != nil {
				return fmt.Errorf("无法读取 cab 归档: %s", payloadErr.Error())
			}
			if folder.comptype == CAB_COMPTYPE_NONE {
				if _, writeErr := spillFile.Write(payload); writeErr != nil {
					return writeErr
				}
			} else {
				out, decodeErr := decoder.Decode(payload, int(cbUncomp))
				if decodeErr != nil {
					return fmt.Errorf("无法读取 cab 归档: LZX 解压失败: %s", decodeErr.Error())
				}
				if _, writeErr := spillFile.Write(out); writeErr != nil {
					return writeErr
				}
			}
			dataPos += 8 + int64(folder.dataReserve) + cbData
		}
	}

	fileInfo, statErr := spillFile.Stat()
	if statErr != nil {
		return statErr
	}
	folder.spillSize = fileInfo.Size()
	folder.spillLoaded = true
	return nil
}
