package core

// 加密夹具生成测试：手工构造 WinZip AES zip（method 99 + extra 0x9901，AE-2 版本2/CRC=0）
// 用内部 pbkdf2HMACSHA1/AES-CTR 原语生成，与解密路径自洽；7z CLI 交叉验证见门禁。

import (
	"archive/zip"

	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	kflate "github.com/klauspost/compress/flate"
	"os"
	"path/filepath"
	"testing"
)

// makeAESZipEntry 手工构造单条目 WinZip AES 加密 zip
func makeAESZipEntry(t *testing.T, path, password, filename string, plain []byte, strength byte) {
	t.Helper()
	keyLen := map[byte]int{1: 16, 2: 24, 3: 32}[strength]
	saltLen := map[byte]int{1: 8, 2: 12, 3: 16}[strength]
	salt := make([]byte, saltLen)
	for idx := range salt {
		salt[idx] = byte(idx*37 + 11)
	}
	dk := pbkdf2HMACSHA1([]byte(password), salt, ZIP_AES_ITERATIONS, 2*keyLen+2)
	encKey := dk[:keyLen]
	macKey := dk[keyLen : 2*keyLen]
	verify := dk[2*keyLen:]

	var inner bytes.Buffer
	fw, _ := zipDeflateWriter(&inner, 6)
	fw.Write(plain)
	fw.Close()

	stream, _ := newAESCTR(encKey) // 小端 CTR（WinZip AES 规范）
	ct := make([]byte, inner.Len())
	stream.XORKeyStream(ct, inner.Bytes())

	mac := hmac.New(sha1.New, macKey)
	mac.Write(ct)
	authCode := mac.Sum(nil)[:ZIP_AES_HMAC_LEN]

	data := append(salt, verify...)
	data = append(data, ct...)
	data = append(data, authCode...)

	extra := make([]byte, 11)
	binary.LittleEndian.PutUint16(extra[0:2], 0x9901)
	binary.LittleEndian.PutUint16(extra[2:4], 7)
	binary.LittleEndian.PutUint16(extra[4:6], 2) // AE-2
	extra[6] = 'A'
	extra[7] = 'E'
	extra[8] = strength
	binary.LittleEndian.PutUint16(extra[9:11], 8) // 内层 deflate

	lfh := make([]byte, 30)
	binary.LittleEndian.PutUint32(lfh[0:4], 0x04034b50)
	binary.LittleEndian.PutUint16(lfh[4:6], 45)
	binary.LittleEndian.PutUint16(lfh[6:8], 0x0801) // bit0 加密 + bit11 EFS
	binary.LittleEndian.PutUint16(lfh[8:10], 99)
	binary.LittleEndian.PutUint16(lfh[26:28], uint16(len(filename)))
	binary.LittleEndian.PutUint16(lfh[28:30], uint16(len(extra)))

	cdh := make([]byte, 46)
	binary.LittleEndian.PutUint32(cdh[0:4], 0x02014b50)
	binary.LittleEndian.PutUint16(cdh[4:6], 45)
	binary.LittleEndian.PutUint16(cdh[6:8], 45)
	binary.LittleEndian.PutUint16(cdh[8:10], 0x0801)
	binary.LittleEndian.PutUint16(cdh[10:12], 99)
	binary.LittleEndian.PutUint32(cdh[16:20], 0) // AE-2 CRC=0
	binary.LittleEndian.PutUint32(cdh[20:24], uint32(len(data)))
	binary.LittleEndian.PutUint32(cdh[24:28], uint32(len(plain)))
	binary.LittleEndian.PutUint16(cdh[28:30], uint16(len(filename)))
	binary.LittleEndian.PutUint16(cdh[30:32], uint16(len(extra)))

	eocd := make([]byte, 22)
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50)
	binary.LittleEndian.PutUint16(eocd[8:10], 1)
	binary.LittleEndian.PutUint16(eocd[10:12], 1)
	binary.LittleEndian.PutUint32(eocd[12:16], uint32(46+len(filename)+len(extra)))
	binary.LittleEndian.PutUint32(eocd[16:20], uint32(30+len(filename)+len(extra)+len(data)))

	var out bytes.Buffer
	out.Write(lfh)
	out.WriteString(filename)
	out.Write(extra)
	out.Write(data)
	out.Write(cdh)
	out.WriteString(filename)
	out.Write(extra)
	out.Write(eocd)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func zipDeflateWriter(buf *bytes.Buffer, level int) (*kflate.Writer, error) {
	return kflate.NewWriter(buf, level)
}

// TestGenerateEncryptedZipFixtures 生成加密夹具（供门禁与 7z 交叉验证）
func TestGenerateEncryptedZipFixtures(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过夹具生成")
	}
	makeAESZipEntry(t, "testdata/zip-enc/aes256.zip", "SECRET", "秘密 文件.txt",
		[]byte("AES256 加密夹具：中文内容 password=SECRET\n"), 3)
	makeAESZipEntry(t, "testdata/zip-enc/aes128.zip", "SECRET", "aes128.txt",
		[]byte("AES128 加密夹具\n"), 1)
	_ = zip.Store // 引用 archive/zip 避免未用导入
}

// TestEncryptedZipExtract 加密 zip 解压（自洽往返 + 错密码 + 缺密码）
func TestEncryptedZipExtract(t *testing.T) {
	dir := t.TempDir()
	aesPath := filepath.Join(dir, "aes.zip")
	makeAESZipEntry(t, aesPath, "SECRET", "秘密.txt", []byte("AES 加密载荷"), 3)

	// 缺密码 → 明确报需要密码
	_, noPwErr := Extract(ExtractOptions{Archive: aesPath, OutputDir: filepath.Join(dir, "a")}, nil)
	if noPwErr == nil || !bytes.Contains([]byte(noPwErr.Error()), []byte("--password")) {
		t.Errorf("缺密码应报需要 --password, got: %v", noPwErr)
	}
	// 错密码 → 密码错误
	_, wrongPwErr := Extract(ExtractOptions{Archive: aesPath, OutputDir: filepath.Join(dir, "b"), Password: "WRONG"}, nil)
	if wrongPwErr == nil || !bytes.Contains([]byte(wrongPwErr.Error()), []byte("密码错误")) {
		t.Errorf("错密码应报密码错误, got: %v", wrongPwErr)
	}
	// 正密码 → 往返一致
	outDir := filepath.Join(dir, "c")
	result, extractErr := Extract(ExtractOptions{Archive: aesPath, OutputDir: outDir, Password: "SECRET"}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 {
		t.Fatalf("应解出 1 文件, got %+v", result)
	}
	content, readErr := os.ReadFile(filepath.Join(outDir, "秘密.txt"))
	if readErr != nil || string(content) != "AES 加密载荷" {
		t.Errorf("AES 解密内容不符: %q err=%v", content, readErr)
	}
}
