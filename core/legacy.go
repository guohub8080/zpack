// 遗留编码格式：.Z（UNIX compress，LZW）与 uuencode/xxencode（邮件文本编码）。
// 都不是容器：解出为单文件。.Z 解码器按 libarchive archive_read_support_filter_compress.c
// 的公开算法忠实移植（标准库 compress/lzw 硬顶 12 位且无块模式/垃圾跳过，不够用）。
package core

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// zCompressReader .Z（compress）流式解码器——LSB 位序、变宽 9..maxBits、
// 块模式清除码（含老 compress 在清除后插垃圾字节的历史兼容跳过）、KwKwK 展开
type zCompressReader struct {
	source         *bufio.Reader
	bitBuffer      uint32
	bitsAvail      uint
	bytesInSection int  // 已消费的原始字节数（清除码后垃圾跳过的计算依据）
	useResetCode   bool // 头 flags bit7：块模式（存在 256 清除码）
	endOfStream    bool
	maxcode        int // 1<<maxcodeBits
	maxcodeBits    int // 9..16
	sectionEndCode int // 达到即加宽一档
	bits           int // 当前码宽
	oldcode        int
	finbyte        byte
	freeEnt        int
	suffix         [65536]byte
	prefix         [65536]uint16
	stack          []byte
}

// newZCompressReader 构造 .Z 解码流（source 已含 3 字节头）
func newZCompressReader(source io.Reader) (*zCompressReader, error) {
	reader := &zCompressReader{source: bufio.NewReader(source), stack: make([]byte, 0, 65300)}
	magicLow, magicErr := reader.source.ReadByte()
	magicHigh, magicErr2 := reader.source.ReadByte()
	if magicErr != nil || magicErr2 != nil || magicLow != 0x1f || magicHigh != 0x9d {
		return nil, errors.New("Z 流损坏：魔数不符")
	}
	paramByte, paramErr := reader.source.ReadByte()
	if paramErr != nil {
		return nil, paramErr
	}
	reader.maxcodeBits = int(paramByte & 0x1f)
	if reader.maxcodeBits > 16 {
		return nil, errors.New("Z 流损坏：maxBits 越界")
	}
	reader.maxcode = 1 << reader.maxcodeBits
	reader.useResetCode = paramByte&0x80 != 0
	reader.freeEnt = 256
	if reader.useResetCode {
		reader.freeEnt++
	}
	reader.bits = 9
	reader.sectionEndCode = (1 << reader.bits) - 1
	reader.oldcode = -1
	for code := 255; code >= 0; code-- {
		reader.prefix[code] = 0
		reader.suffix[code] = byte(code)
	}
	return reader, nil
}

// Read 实现 io.Reader：栈非空先弹栈，栈空取下一码
func (reader *zCompressReader) Read(buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		if len(reader.stack) > 0 {
			buffer[total] = reader.stack[len(reader.stack)-1]
			reader.stack = reader.stack[:len(reader.stack)-1]
			total++
			continue
		}
		if reader.endOfStream {
			if total > 0 {
				return total, nil
			}
			return 0, io.EOF
		}
		if err := reader.nextCode(); err != nil {
			reader.endOfStream = true
			if errors.Is(err, io.EOF) && total > 0 {
				return total, nil
			}
			if errors.Is(err, io.EOF) {
				return 0, io.EOF
			}
			return total, err
		}
	}
	return total, nil
}

// Close .Z 解码流无独立资源
func (reader *zCompressReader) Close() error { return nil }

// getBits LSB 取 n 位；数据尽头返回 io.EOF
func (reader *zCompressReader) getBits(count int) (int, error) {
	for reader.bitsAvail < uint(count) {
		nextByte, byteErr := reader.source.ReadByte()
		if byteErr != nil {
			return 0, io.EOF
		}
		reader.bitBuffer |= uint32(nextByte) << reader.bitsAvail
		reader.bitsAvail += 8
		reader.bytesInSection++
	}
	code := int(reader.bitBuffer & ((1 << uint(count)) - 1))
	reader.bitBuffer >>= uint(count)
	reader.bitsAvail -= uint(count)
	return code, nil
}

// nextCode 处理下一码（算法同 libarchive next_code：清除码/垃圾跳过/越界校验/KwKwK/登记新词条/阶梯加宽）
func (reader *zCompressReader) nextCode() error {
	for {
		code, codeErr := reader.getBits(reader.bits)
		if codeErr != nil {
			return codeErr
		}
		newcode := code

		// 清除码：老 compress 实现在每次清除后插 (bits - bytesInSection%bits)%bits 字节垃圾，
		// 且要丢弃当前字节残余位——历史兼容，必须照做
		if code == 256 && reader.useResetCode {
			skipBytes := reader.bits - reader.bytesInSection%reader.bits
			skipBytes %= reader.bits
			reader.bitsAvail = 0
			for skipBytes > 0 {
				if _, skipErr := reader.getBits(8); skipErr != nil {
					return skipErr
				}
				skipBytes--
			}
			reader.bytesInSection = 0
			reader.bits = 9
			reader.sectionEndCode = (1 << reader.bits) - 1
			reader.freeEnt = 257
			reader.oldcode = -1
			continue
		}

		if code > reader.freeEnt || (code == reader.freeEnt && reader.oldcode < 0) {
			return errors.New("Z 流损坏：码越界")
		}

		// KwKwK 特例：码恰为下一步将登记的词条
		if code >= reader.freeEnt {
			reader.stack = append(reader.stack, reader.finbyte)
			code = reader.oldcode
		}
		for code >= 256 {
			reader.stack = append(reader.stack, reader.suffix[code])
			code = int(reader.prefix[code])
		}
		reader.finbyte = byte(code)
		reader.stack = append(reader.stack, reader.finbyte)

		// 登记新词条（字典满后停登，等清除码）
		if reader.freeEnt < reader.maxcode && reader.oldcode >= 0 {
			reader.prefix[reader.freeEnt] = uint16(reader.oldcode)
			reader.suffix[reader.freeEnt] = reader.finbyte
			reader.freeEnt++
		}
		if reader.freeEnt > reader.sectionEndCode {
			reader.bits++
			reader.bytesInSection = 0
			if reader.bits == reader.maxcodeBits {
				reader.sectionEndCode = reader.maxcode
			} else {
				reader.sectionEndCode = (1 << reader.bits) - 1
			}
		}

		reader.oldcode = newcode
		return nil
	}
}

// uuAlphabet / xxAlphabet 两族编码字母表；uu 逐字节算术转换（'`' 归一为 0）
const uuAlphabetShift byte = 32

// XX_ALPHABET xxencode 字母表（值 = 下标）
const XX_ALPHABET = "+-0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// uuDecodeReader uuencode/xxencode 文本流解码器：begin 行取内嵌文件名，逐行解码体
type uuDecodeReader struct {
	scanner  *bufio.Scanner
	conv     func(byte) byte
	started  bool
	done     bool
	pending  []byte
	Filename string // begin 行内嵌的目标文件名（解析后可用）
}

// newUuDecodeReader 构造；xxStyle 选择字母表（false=uuencode，true=xxencode）
func newUuDecodeReader(source io.Reader, xxStyle bool) *uuDecodeReader {
	reader := &uuDecodeReader{scanner: bufio.NewScanner(source)}
	if xxStyle {
		reader.conv = func(symbol byte) byte {
			return byte(strings.IndexByte(XX_ALPHABET, symbol))
		}
	} else {
		reader.conv = func(symbol byte) byte {
			return (symbol - uuAlphabetShift) & 0x3f
		}
	}
	return reader
}

// Read 实现 io.Reader：消费行→解码组→吐字节
func (reader *uuDecodeReader) Read(buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		for len(reader.pending) > 0 && total < len(buffer) {
			buffer[total] = reader.pending[0]
			reader.pending = reader.pending[1:]
			total++
		}
		if total == len(buffer) {
			return total, nil
		}
		if reader.done {
			if total > 0 {
				return total, nil
			}
			return 0, io.EOF
		}
		if !reader.scanner.Scan() {
			reader.done = true
			if total > 0 {
				return total, nil
			}
			if err := reader.scanner.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		line := strings.TrimRight(reader.scanner.Text(), "\r")
		if err := reader.consumeLine(line); err != nil {
			return total, err
		}
	}
	return total, nil
}

// consumeLine 状态机：begin 头 → 数据行（首字符=本行字节数）→ end 终止
func (reader *uuDecodeReader) consumeLine(line string) error {
	if !reader.started {
		if !strings.HasPrefix(line, "begin ") {
			return nil // 头前空行/邮件前文：跳过
		}
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			reader.Filename = strings.Join(parts[2:], " ")
		}
		reader.started = true
		return nil
	}
	if line == "end" || strings.HasPrefix(line, "end ") {
		reader.done = true
		return nil
	}
	if len(line) == 0 {
		return nil
	}
	count := int(reader.conv(line[0]))
	if count > 0 && count <= 63 && len(line) >= 1+((count+2)/3)*4 {
		reader.pending = append(reader.pending, reader.decodeGroups(line[1:], count)...)
	} else if count == 0 {
		reader.done = true // '`'/空长度行：体结束（end 行可能缺省）
	}
	return nil
}

// decodeGroups 解码 4 字符组（LSB 6bit×4 → 3 字节），按 count 截断
func (reader *uuDecodeReader) decodeGroups(body string, count int) []byte {
	decoded := make([]byte, 0, count)
	for groupIdx := 0; groupIdx+4 <= len(body) && len(decoded) < count; groupIdx += 4 {
		// uu/xx 组打包为 MSB-first：4×6bit → 3 字节（c0<<2|c1>>4 / ...）
		var packed uint32
		for symbolIdx := 0; symbolIdx < 4; symbolIdx++ {
			packed |= uint32(reader.conv(body[groupIdx+symbolIdx])) << (18 - 6*uint(symbolIdx))
		}
		chunk := []byte{byte(packed >> 16), byte((packed >> 8) & 0xff), byte(packed & 0xff)}
		take := count - len(decoded)
		if take > 3 {
			take = 3
		}
		decoded = append(decoded, chunk[:take]...)
	}
	return decoded
}

// Close 无独立资源
func (reader *uuDecodeReader) Close() error { return nil }

// uueExtract uu/xxencode 解出：内嵌文件名优先（begin 行），缺省用基名去扩展名
func uueExtract(source archiveSource, options ExtractOptions, xxStyle bool, logf Logf) (ExtractResult, error) {
	// 先窥 begin 行拿内嵌文件名（只读头部几行，代价极小）
	peekStream, peekErr := source.openStream()
	if peekErr != nil {
		return ExtractResult{}, peekErr
	}
	embeddedName := ""
	peekReader := bufio.NewReader(peekStream)
	for probeIdx := 0; probeIdx < 32; probeIdx++ {
		line, lineErr := peekReader.ReadString('\n')
		if lineErr != nil {
			break
		}
		if strings.HasPrefix(line, "begin ") {
			if parts := strings.Fields(strings.TrimRight(line, "\r\n")); len(parts) >= 3 {
				embeddedName = strings.Join(parts[2:], " ")
			}
			break
		}
	}
	peekStream.Close()

	if embeddedName != "" && isUnsafeEntryPath(embeddedName) {
		return ExtractResult{}, fmt.Errorf("归档含不安全路径: %s", embeddedName)
	}
	decoderName := embeddedName
	if slashPos := strings.LastIndexAny(decoderName, "/\\"); slashPos >= 0 {
		decoderName = decoderName[slashPos+1:] // 内嵌名只取文件名部分，不建子目录
	}
	return extractSingleFileNamed(source, func(stream io.Reader) (io.ReadCloser, error) {
		return newUuDecodeReader(stream, xxStyle), nil
	}, options, decoderName, logf)
}
