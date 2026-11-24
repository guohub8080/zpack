// 归档加密层：WinZip AES（AE-1/AE-2，0x9901 extra 规格——完整规格见
// docs/360zip-default-chain.md §四，PBKDF2-HMAC-SHA1×1000/dk=enc+mac+2B 校验/AES-CTR/尾 10B HMAC）
// 与经典 ZipCrypto（PKWARE 规范三密钥流密码）。全标准库实现（crypto/aes+hmac+sha1），零第三方依赖。
// 解码器接管 archive/zip 的方式：预扫识别加密条目后把内存头 method 改写为桥接码并注册对应解码器
// （桥接码只存在于进程内，不落盘）。编码器（encryptZipAES/encryptZipCrypto）供夹具生成与未来写侧加密复用。
package core

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	kflate "github.com/klauspost/compress/flate"
)

// 加密方案标识（识别层对外的 scheme 名）
const (
	CRYPTO_SCHEME_AES_256 = "WinZip AES-256"
	CRYPTO_SCHEME_AES_192 = "WinZip AES-192"
	CRYPTO_SCHEME_AES_128 = "WinZip AES-128"
	CRYPTO_SCHEME_ZIP     = "ZipCrypto"
)

// ZIP_AES_ITERATIONS WinZip AES 密钥派生迭代数（规格恒定 1000）
const ZIP_AES_ITERATIONS = 1000

// ZIP_AES_HMAC_LEN 尾部认证码长度（HMAC-SHA1 截断 10 字节）
const ZIP_AES_HMAC_LEN = 10

// ZIP_CRYPTO_HEADER_LEN ZipCrypto 12 字节加密头
const ZIP_CRYPTO_HEADER_LEN = 12

// aesStrengthParams 强度 1/2/3 → 密钥与盐长度（WinZip AES 规范）
func aesStrengthParams(strength byte) (keyLen int, saltLen int, scheme string, ok bool) {
	switch strength {
	case 1:
		return 16, 8, CRYPTO_SCHEME_AES_128, true
	case 2:
		return 24, 12, CRYPTO_SCHEME_AES_192, true
	case 3:
		return 32, 16, CRYPTO_SCHEME_AES_256, true
	default:
		return 0, 0, "", false
	}
}

// ErrWrongPassword 密码错误（2B 校验值/12B 头校验未过）
var ErrWrongPassword = errors.New("密码错误或归档损坏")

// pbkdf2HMACSHA1 PBKDF2-HMAC-SHA1（RFC 2898 公开算法，标准库直实现零依赖）
func pbkdf2HMACSHA1(password []byte, salt []byte, iterations int, keyLen int) []byte {
	prf := hmac.New(sha1.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	derived := make([]byte, 0, numBlocks*hashLen)
	var blockIndex [4]byte
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(blockIndex[:], uint32(block))
		prf.Write(blockIndex[:])
		blockBytes := prf.Sum(nil)
		work := make([]byte, hashLen)
		copy(work, blockBytes)
		for iter := 1; iter < iterations; iter++ {
			prf.Reset()
			prf.Write(work)
			work = prf.Sum(work[:0])
			for idx := range blockBytes {
				blockBytes[idx] ^= work[idx]
			}
		}
		derived = append(derived, blockBytes...)
	}
	return derived[:keyLen]
}

// zipAESKeyset WinZip AES 密钥组：加密钥 + 认证钥 + 2B 密码校验值
type zipAESKeyset struct {
	encKey  []byte
	macKey  []byte
	verify  []byte // 2 字节
	scheme  string
	saltLen int
}

// deriveZipAESKeys 派生密钥组：PBKDF2-HMAC-SHA1(password, salt, 1000, 2*keyLen+2)
func deriveZipAESKeys(password string, salt []byte, strength byte) (zipAESKeyset, error) {
	keyLen, saltLen, scheme, ok := aesStrengthParams(strength)
	if !ok {
		return zipAESKeyset{}, fmt.Errorf("未知 WinZip AES 强度: %d", strength)
	}
	if len(salt) != saltLen {
		return zipAESKeyset{}, errors.New("盐长度与强度不符")
	}
	derived := pbkdf2HMACSHA1([]byte(password), salt, ZIP_AES_ITERATIONS, 2*keyLen+2)
	return zipAESKeyset{
		encKey:  derived[:keyLen],
		macKey:  derived[keyLen : 2*keyLen],
		verify:  derived[2*keyLen : 2*keyLen+2],
		scheme:  scheme,
		saltLen: saltLen,
	}, nil
}

// newAESCTR WinZip AES 的 CTR 流：**小端计数器**（逆向实证：计数器块首字节=1，
// 后续块从首字节递增——与 NIST 大端 CTR 相反；Go cipher.NewCTR 只支持大端，故自实现）
func newAESCTR(key []byte) (cipher.Stream, error) {
	block, blockErr := aes.NewCipher(key)
	if blockErr != nil {
		return nil, blockErr
	}
	counter := make([]byte, aes.BlockSize)
	counter[0] = 1 // 小端：首字节=1
	return &littleEndianCTR{block: block, counter: counter}, nil
}

// littleEndianCTR 小端递增计数器 CTR 模式（WinZip AES 专用——Go 标准 CTR 是大端）
type littleEndianCTR struct {
	block     cipher.Block
	counter   []byte
	keystream []byte
	ksPos     int
}

func (ctr *littleEndianCTR) refill() {
	ctr.keystream = make([]byte, aes.BlockSize)
	ctr.block.Encrypt(ctr.keystream, ctr.counter)
	ctr.ksPos = 0
	// 小端递增：从首字节开始进位
	for idx := 0; idx < len(ctr.counter); idx++ {
		ctr.counter[idx]++
		if ctr.counter[idx] != 0 {
			break
		}
	}
}

func (ctr *littleEndianCTR) XORKeyStream(dst, src []byte) {
	for len(src) > 0 {
		if ctr.ksPos >= len(ctr.keystream) {
			ctr.refill()
		}
		n := min(len(src), len(ctr.keystream)-ctr.ksPos)
		for i := 0; i < n; i++ {
			dst[i] = src[i] ^ ctr.keystream[ctr.ksPos+i]
		}
		dst = dst[n:]
		src = src[n:]
		ctr.ksPos += n
	}
}

// zipAESEncryptData 编码侧：salt(明文) + 2B 校验值 + AES-CTR(plaintext) + 10B HMAC
// （夹具生成与未来写侧加密复用；auth code 按规范对密文计算）
func zipAESEncryptData(plaintext []byte, password string, strength byte, rngSource io.Reader) (ciphertext []byte, salt []byte, verify []byte, err error) {
	keyLen, saltLen, _, ok := aesStrengthParams(strength)
	if !ok {
		return nil, nil, nil, fmt.Errorf("未知强度: %d", strength)
	}
	salt = make([]byte, saltLen)
	if _, readErr := io.ReadFull(rngSource, salt); readErr != nil {
		return nil, nil, nil, readErr
	}
	keys, keyErr := deriveZipAESKeys(password, salt, strength)
	if keyErr != nil {
		return nil, nil, nil, keyErr
	}
	stream, streamErr := newAESCTR(keys.encKey[:keyLen])
	if streamErr != nil {
		return nil, nil, nil, streamErr
	}
	ciphertext = make([]byte, len(plaintext))
	stream.XORKeyStream(ciphertext, plaintext)
	mac := hmac.New(sha1.New, keys.macKey)
	mac.Write(ciphertext)
	authCode := mac.Sum(nil)[:ZIP_AES_HMAC_LEN]
	return append(ciphertext, authCode...), salt, keys.verify, nil
}

// zipAESDecodingReader 解码管线：读 salt+2B 校验值 → 验密码 → AES-CTR 解密。
//
// 尾 10B 是认证码（HMAC-SHA1，只覆盖认证码之前的密文）。读尽时校验；内层是 deflate 时解压器读到流末就停，
// 由 zipWrapInnerDecompress 把解密流读尽来触发校验——AE-2 没有 CRC（标准库见 CRC=0 直接跳过），
// 完整性全靠这把 HMAC。
// 2026-11-24 修复：此前的尾校验拿「解密后的末尾 10B」去比「全部密文的 HMAC」，永远不等——内层是 Store
// （--level 0 加密）的条目一读到底就报「密码错误」。
type zipAESDecodingReader struct {
	source  io.Reader
	stream  cipher.Stream
	mac     hash.Hash
	pending []byte // 已读入、尚未确认的密文（末尾始终保持 10B 前瞻，防止把认证码当密文解出去）
	plain   []byte // 已解出、尚未吐出的明文
	done    bool
}

// newZipAESDecodingReader 构造：先验 2B 密码校验值，错则立即 ErrWrongPassword
func newZipAESDecodingReader(source io.Reader, password string, strength byte) (io.ReadCloser, error) {
	keyLen, saltLen, _, ok := aesStrengthParams(strength)
	if !ok {
		return nil, fmt.Errorf("未知 WinZip AES 强度: %d", strength)
	}
	prefix := make([]byte, saltLen+2)
	if _, readErr := io.ReadFull(source, prefix); readErr != nil {
		return nil, fmt.Errorf("无法读取 AES 前缀: %s", readErr.Error())
	}
	keys, keyErr := deriveZipAESKeys(password, prefix[:saltLen], strength)
	if keyErr != nil {
		return nil, keyErr
	}
	if !bytes.Equal(keys.verify, prefix[saltLen:]) {
		return nil, ErrWrongPassword
	}
	stream, streamErr := newAESCTR(keys.encKey[:keyLen])
	if streamErr != nil {
		return nil, streamErr
	}
	return io.NopCloser(&zipAESDecodingReader{
		source: source,
		stream: stream,
		mac:    hmac.New(sha1.New, keys.macKey),
	}), nil
}

// Read 分块解密：密文里除了末尾 10B 前瞻，其余都是确认的密文——计入 HMAC 并解出；
// 密文读尽时剩下的 10B 就是认证码，与 HMAC 比对后才给 EOF
func (reader *zipAESDecodingReader) Read(buffer []byte) (int, error) {
	for {
		if len(reader.plain) > 0 {
			plainCount := copy(buffer, reader.plain)
			reader.plain = reader.plain[plainCount:]
			return plainCount, nil
		}
		if reader.done {
			return 0, io.EOF
		}
		chunk := make([]byte, 4096)
		readCount, readErr := reader.source.Read(chunk)
		if readCount > 0 {
			reader.pending = append(reader.pending, chunk[:readCount]...)
			confirmedCount := len(reader.pending) - ZIP_AES_HMAC_LEN
			if confirmedCount > 0 {
				reader.mac.Write(reader.pending[:confirmedCount])
				decrypted := make([]byte, confirmedCount)
				reader.stream.XORKeyStream(decrypted, reader.pending[:confirmedCount])
				reader.plain = append(reader.plain, decrypted...)
				reader.pending = append([]byte{}, reader.pending[confirmedCount:]...)
			}
		}
		if readErr == io.EOF {
			reader.done = true
			if len(reader.pending) != ZIP_AES_HMAC_LEN {
				return 0, fmt.Errorf("加密数据不完整（认证码长度 %d，应为 %d）", len(reader.pending), ZIP_AES_HMAC_LEN)
			}
			if !hmac.Equal(reader.pending, reader.mac.Sum(nil)[:ZIP_AES_HMAC_LEN]) {
				return 0, ErrWrongPassword
			}
			reader.pending = nil
		} else if readErr != nil {
			return 0, readErr
		}
	}
}

// ─── 经典 ZipCrypto（PKWARE APPNOTE 规范三密钥流密码）───

// zipCryptoState ZipCrypto 三密钥状态机
type zipCryptoState struct {
	key0 uint32
	key1 uint32
	key2 uint32
}

// ZIP_CRYPTO_INIT_KEYS 规范初始密钥（公开常数）
const (
	ZIP_CRYPTO_KEY0_INIT uint32 = 305419896
	ZIP_CRYPTO_KEY1_INIT uint32 = 591751049
	ZIP_CRYPTO_KEY2_INIT uint32 = 878082192
)

// newZipCryptoState 用密码初始化（逐字节更新密钥）
func newZipCryptoState(password string) *zipCryptoState {
	state := &zipCryptoState{ZIP_CRYPTO_KEY0_INIT, ZIP_CRYPTO_KEY1_INIT, ZIP_CRYPTO_KEY2_INIT}
	for idx := 0; idx < len(password); idx++ {
		state.updateKeys(password[idx])
	}
	return state
}

// zipCryptoCrcTable ZipCrypto 专用 RAW CRC32 表——Go crc32.Update 前后取反（~crc），
// PKWARE APPNOTE 定义的 key 更新用无取反 RAW 值（逆向实证：Info-ZIP 交叉验证定位）
var zipCryptoCrcTable = func() [256]uint32 {
	var t [256]uint32
	for idx := uint32(0); idx < 256; idx++ {
		c := idx
		for bit := 0; bit < 8; bit++ {
			if c&1 == 1 {
				c = 0xEDB88320 ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		t[idx] = c
	}
	return t
}()

// rawCrc32Step RAW CRC32 单步（无取反）：(crc >> 8) ^ table[(crc ^ b) & 0xFF]
func rawCrc32Step(crc uint32, dataByte byte) uint32 {
	return (crc >> 8) ^ zipCryptoCrcTable[(crc^uint32(dataByte))&0xFF]
}

// updateKeys 明文字节更新三密钥（RAW CRC 链 + 乘法混合——不用 Go crc32.Update！）
func (state *zipCryptoState) updateKeys(plainByte byte) {
	state.key0 = rawCrc32Step(state.key0, plainByte)
	state.key1 = state.key1 + state.key0&0xff
	state.key1 = state.key1*134775813 + 1
	state.key2 = rawCrc32Step(state.key2, byte(state.key1>>24))
}

// decryptByte 流密码输出字节：temp = key2|2; (temp*(temp^1))>>8 的低 8 位
func (state *zipCryptoState) decryptByte() byte {
	temp := state.key2 | 2
	return byte((temp * (temp ^ 1)) >> 8)
}

// decrypt 解密一串密文（逐字节：先取流字节再按明文更新密钥）
func (state *zipCryptoState) decrypt(ciphertext []byte) []byte {
	plaintext := make([]byte, len(ciphertext))
	for idx, cipherByte := range ciphertext {
		plainByte := cipherByte ^ state.decryptByte()
		state.updateKeys(plainByte)
		plaintext[idx] = plainByte
	}
	return plaintext
}

// encrypt 加密一串明文（对称结构：流字节相同，但按明文更新密钥）
func (state *zipCryptoState) encrypt(plaintext []byte) []byte {
	ciphertext := make([]byte, len(plaintext))
	for idx, plainByte := range plaintext {
		ciphertext[idx] = plainByte ^ state.decryptByte()
		state.updateKeys(plainByte)
	}
	return ciphertext
}

// newZipCryptoDecodingReader 解码管线：解 12 字节加密头并校验第 12 字节（CRC 高位——
// 错密码即时暴露），随后接内层解压流
func newZipCryptoDecodingReader(source io.Reader, password string, expectCheckByte byte) (io.Reader, error) {
	state := newZipCryptoState(password)
	header := make([]byte, ZIP_CRYPTO_HEADER_LEN)
	if _, readErr := io.ReadFull(source, header); readErr != nil {
		return nil, fmt.Errorf("无法读取 ZipCrypto 头: %s", readErr.Error())
	}
	decryptedHeader := state.decrypt(header)
	if decryptedHeader[ZIP_CRYPTO_HEADER_LEN-1] != expectCheckByte {
		return nil, ErrWrongPassword
	}
	return &zipCryptoStream{source: source, state: state}, nil
}

// zipCryptoStream 密文→明文的流包装
type zipCryptoStream struct {
	source io.Reader
	state  *zipCryptoState
}

func (stream *zipCryptoStream) Read(buffer []byte) (int, error) {
	chunk := make([]byte, len(buffer))
	readLen, readErr := stream.source.Read(chunk)
	if readLen > 0 {
		copy(buffer, stream.state.decrypt(chunk[:readLen]))
	}
	if readErr == io.EOF && readLen == 0 {
		return 0, io.EOF
	}
	return readLen, readErr
}

// 加密条目的桥接码值域（进程内改写 method 用，不落盘；避开真实方法号）。
// archive/zip 的解码器只拿到字节流、拿不到条目，所以每个「方案 + 强度 + 内层方式 + 校验字节 + 密码」
// 互不相同的条目组各占一个桥接码——zipExtract 的预扫按序分配（2026-11-24 修复：此前只按强度 + 内层方式
// 分配，同码的解码器闭包捕获的是首个条目的密码，--password-table 给不同文件配不同密码时后面的全在用错的密码）
const (
	ZIP_BRIDGE_FIRST uint16 = 0x7000
	ZIP_BRIDGE_LAST  uint16 = 0x7FFF
)

// zipDetectWinZipAES 识别 WinZip AES 条目：method 99 → 解析 extra 0x9901 拿强度与内层方法
func zipDetectWinZipAES(zipEntry *zip.File) (strength byte, innerMethod uint16, isAES bool) {
	if zipEntry.Method != 99 {
		return 0, 0, false
	}
	extra := zipEntry.Extra
	for offset := 0; offset+4 <= len(extra); {
		fieldID := binary.LittleEndian.Uint16(extra[offset : offset+2])
		fieldSize := int(binary.LittleEndian.Uint16(extra[offset+2 : offset+4]))
		if offset+4+fieldSize > len(extra) {
			break
		}
		if fieldID == 0x9901 && fieldSize >= 7 {
			payload := extra[offset+4 : offset+4+fieldSize]
			// 布局：版本 u16 | 厂商 'AE' 2B | 强度 u8 | 真实压缩方法 u16
			return payload[4], binary.LittleEndian.Uint16(payload[5:7]), true
		}
		offset += 4 + fieldSize
	}
	return 0, 0, false
}

// zipWrapInnerDecompress 外层解密 + 内层解压（Store 直通 / Deflate 走 klauspost flate）
// 的组合包装：archive/zip 桥接解码器的最终形态
func zipWrapInnerDecompress(source io.Reader, decryptFactory func(io.Reader) (io.Reader, error), innerMethod uint16) (io.ReadCloser, error) {
	decrypted, decryptErr := decryptFactory(source)
	if decryptErr != nil {
		return nil, decryptErr
	}
	if innerMethod == 0 { // Store：解密即明文
		if closer, ok := decrypted.(io.ReadCloser); ok {
			return closer, nil
		}
		return io.NopCloser(decrypted), nil
	}
	// Deflate：解压器读到流末就停，外层解密流的尾校验（HMAC）要靠读尽来触发
	return &drainVerifiedReader{decrypted: decrypted, flate: kflate.NewReader(decrypted)}, nil
}

// drainVerifiedReader 内层 deflate 流结束后把外层解密流读尽，触发 WinZip AES 的尾 10B HMAC 校验
// （不读尽就等于没校验：AE-2 没有 CRC，完整性全靠这把 HMAC）
type drainVerifiedReader struct {
	decrypted io.Reader
	flate     io.Reader
	drained   bool
}

func (reader *drainVerifiedReader) Read(buffer []byte) (int, error) {
	readCount, readErr := reader.flate.Read(buffer)
	if readErr != io.EOF || reader.drained {
		return readCount, readErr
	}
	reader.drained = true
	if _, drainErr := io.Copy(io.Discard, reader.decrypted); drainErr != nil {
		return readCount, drainErr
	}
	return readCount, readErr
}

func (reader *drainVerifiedReader) Close() error { return nil }

// errorReadCloser 构造期错误的懒暴露包装（zip.Decompressor 签名无 error，
// 错误在首次 Read 浮出——标准库惯例模式）
type errorReadCloser struct{ err error }

func (reader errorReadCloser) Read([]byte) (int, error) { return 0, reader.err }
func (reader errorReadCloser) Close() error             { return nil }

// zipEncryptEntryForWrite 写入侧：已压缩数据（Store 或 deflate）→ WinZip AES 加密包（salt+verify+CTR+HMAC）
// 返回加密数据、0x9901 extra 字段、新 method(99)、原始未压尺寸
func zipEncryptEntryForWrite(compressedData []byte, password string, originalSize int64, innerMethod uint16) (
	encryptedData []byte, extraField []byte, encryptedSize int64, err error) {
	// AES-256：salt 16B
	salt := make([]byte, 16)
	if _, saltErr := io.ReadFull(cryptoRand, salt); saltErr != nil {
		return nil, nil, 0, saltErr
	}
	keys, keyErr := deriveZipAESKeys(password, salt, 3)
	if keyErr != nil {
		return nil, nil, 0, keyErr
	}
	stream, streamErr := newAESCTR(keys.encKey)
	if streamErr != nil {
		return nil, nil, 0, streamErr
	}
	ciphertext := make([]byte, len(compressedData))
	stream.XORKeyStream(ciphertext, compressedData)

	mac := hmac.New(sha1.New, keys.macKey)
	mac.Write(ciphertext)
	authCode := mac.Sum(nil)[:ZIP_AES_HMAC_LEN]

	encryptedData = append(salt, keys.verify...)
	encryptedData = append(encryptedData, ciphertext...)
	encryptedData = append(encryptedData, authCode...)

	// 0x9901 extra：version=2(AE-2) + 'AE' + strength=3(AES-256) + 内层真实 method（0=Store / 8=Deflate）——
	// 必须与数据的实际压缩方式一致：写死 deflate 时 --level 0 加密产出的归档连自家 extract 都解不开（2026-11-24）
	extraField = make([]byte, 11)
	binary.LittleEndian.PutUint16(extraField[0:2], 0x9901)
	binary.LittleEndian.PutUint16(extraField[2:4], 7)
	binary.LittleEndian.PutUint16(extraField[4:6], 2)
	extraField[6] = 'A'
	extraField[7] = 'E'
	extraField[8] = 3
	binary.LittleEndian.PutUint16(extraField[9:11], innerMethod)

	return encryptedData, extraField, int64(len(encryptedData)), nil
}

// zipWriteEncryptedSequentialEntry 加密大条目流式路径：deflate 先落临时文件
// （内存有界，128KB 块循环），再流式包 WinZip AES-2（salt+verify → CTR 密文 →
// 10B HMAC 尾）。修复血案 m03442：加密条目超出 256KB 缓冲上限后曾降级明文顺序
// 写——用户带密码压 2.6MB 文件产出裸奔归档且零警告。本路径后加密条目任何尺寸
// 都不再回退明文。
func zipWriteEncryptedSequentialEntry(zipWriter *zip.Writer, task compressTask, compressionLevel int, password string, passwordResolver func(string) (string, bool)) error {
	if !utf8.ValidString(task.entryName) {
		return fmt.Errorf("文件名不是有效 UTF-8，无法安全写入 zip: %q", task.entryName)
	}
	entryPassword := password
	if passwordResolver != nil {
		if resolved, hit := passwordResolver(task.entryName); hit {
			entryPassword = resolved
		}
	}
	if entryPassword == "" { // 密码表可对个别文件给出空：按明文条目写
		return zipWriteSequentialEntry(zipWriter, task, compressionLevel)
	}

	sourceFile, openErr := os.Open(task.sourcePath)
	if openErr != nil {
		return openErr
	}
	defer sourceFile.Close()

	spillFile, spillErr := os.CreateTemp("", "zpack-enc-spill-*")
	if spillErr != nil {
		return spillErr
	}
	defer os.Remove(spillFile.Name())
	defer spillFile.Close()

	flateWriter, flateErr := kflate.NewWriter(spillFile, compressionLevel)
	if flateErr != nil {
		return flateErr
	}
	if _, copyErr := io.Copy(flateWriter, sourceFile); copyErr != nil {
		flateWriter.Close()
		return copyErr
	}
	if closeErr := flateWriter.Close(); closeErr != nil {
		return closeErr
	}
	compressedSize, seekErr := spillFile.Seek(0, io.SeekEnd)
	if seekErr != nil {
		return seekErr
	}
	if _, seekErr := spillFile.Seek(0, io.SeekStart); seekErr != nil {
		return seekErr
	}

	salt := make([]byte, 16) // AES-256：salt 16B
	if _, saltErr := io.ReadFull(cryptoRand, salt); saltErr != nil {
		return saltErr
	}
	keys, keyErr := deriveZipAESKeys(entryPassword, salt, 3)
	if keyErr != nil {
		return keyErr
	}
	stream, streamErr := newAESCTR(keys.encKey)
	if streamErr != nil {
		return streamErr
	}
	mac := hmac.New(sha1.New, keys.macKey)

	entryHeader, headerErr := zip.FileInfoHeader(task.fileInfo)
	if headerErr != nil {
		return headerErr
	}
	entryHeader.Name = filepath.ToSlash(task.entryName)
	entryHeader.Method = 99
	entryHeader.CRC32 = 0 // AE-2 用 HMAC 替代 CRC
	entryHeader.CompressedSize64 = uint64(compressedSize + int64(len(salt)) + int64(len(keys.verify)) + ZIP_AES_HMAC_LEN)
	entryHeader.UncompressedSize64 = uint64(task.fileInfo.Size())
	extraField := make([]byte, 11) // 0x9901：version=2(AE-2)+'AE'+strength=3+真实 method=8(deflate)
	binary.LittleEndian.PutUint16(extraField[0:2], 0x9901)
	binary.LittleEndian.PutUint16(extraField[2:4], 7)
	binary.LittleEndian.PutUint16(extraField[4:6], 2)
	extraField[6] = 'A'
	extraField[7] = 'E'
	extraField[8] = 3
	binary.LittleEndian.PutUint16(extraField[9:11], 8)
	entryHeader.Extra = extraField
	entryHeader.SetMode(task.fileInfo.Mode().Perm())
	entryHeader.Flags |= 0x800     // 显式 EFS（bit 11）
	entryHeader.Flags |= 0x1       // 加密位
	entryHeader.ReaderVersion = 51 // WinZip AES 要求 5.1（7-Zip 严格校验）

	entryWriter, entryErr := zipWriter.CreateRaw(entryHeader)
	if entryErr != nil {
		return entryErr
	}
	if _, writeErr := entryWriter.Write(salt); writeErr != nil {
		return writeErr
	}
	if _, writeErr := entryWriter.Write(keys.verify); writeErr != nil {
		return writeErr
	}
	plainChunk := make([]byte, 128<<10)
	cipherChunk := make([]byte, 128<<10)
	for {
		readLen, readErr := io.ReadFull(spillFile, plainChunk)
		if readLen > 0 {
			stream.XORKeyStream(cipherChunk[:readLen], plainChunk[:readLen])
			if _, writeErr := entryWriter.Write(cipherChunk[:readLen]); writeErr != nil {
				return writeErr
			}
			mac.Write(cipherChunk[:readLen])
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if _, writeErr := entryWriter.Write(mac.Sum(nil)[:ZIP_AES_HMAC_LEN]); writeErr != nil {
		return writeErr
	}
	return nil
}

// cryptoRand 加密随机源（标准库 crypto/rand）
var cryptoRand = rand.Reader
