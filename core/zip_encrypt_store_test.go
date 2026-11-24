package core

// 加密 + Store 条目的回归（2026-11-24）。
//
// AES 扩展字段（0x9901）里的「内层压缩方式」曾写死为 deflate，而 --level 0 时数据是原样 Store 的——
// 产出的归档连自家 extract 都解不开（flate: corrupt input before offset 5），解出的文件是空的，第三方工具同样解不开。

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEncryptedEntriesRecordRealInnerMethod 并行路径（Jobs>1；Jobs≤1 走顺序加密路径，恒 deflate）下加密条目的内层压缩方式必须与数据一致：level 0 → Store(0)，level 6 → Deflate(8)；且都能原样解回
func TestEncryptedEntriesRecordRealInnerMethod(t *testing.T) {
	cases := []struct {
		name           string
		level          int
		expectedMethod uint16
	}{
		{"level 0 加密：内层是 Store", 0, 0},
		{"level 6 加密：可压文本内层是 Deflate", 6, 8},
	}
	for _, testCase := range cases {
		dir := t.TempDir()
		srcDir := filepath.Join(dir, "src")
		if mkdirErr := os.MkdirAll(srcDir, 0o755); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
		payload := bytes.Repeat([]byte("zpack 加密回归载荷 0123456789\n"), 400)
		if writeErr := os.WriteFile(filepath.Join(srcDir, "data.txt"), payload, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
		archivePath := filepath.Join(dir, "enc.zip")
		if _, compressErr := Compress(CompressOptions{Inputs: []string{srcDir}, Output: archivePath, Level: testCase.level, Password: "pw", Jobs: 4}, nil); compressErr != nil {
			t.Fatalf("%s：压缩失败 %v", testCase.name, compressErr)
		}

		reader, openErr := zip.OpenReader(archivePath)
		if openErr != nil {
			t.Fatalf("%s：打开归档失败 %v", testCase.name, openErr)
		}
		for _, zipEntry := range reader.File {
			if zipEntry.FileInfo().IsDir() {
				continue
			}
			_, innerMethod, isAES := zipDetectWinZipAES(zipEntry)
			if !isAES {
				t.Errorf("%s：%s 应为 AES 加密条目", testCase.name, zipEntry.Name)
			}
			if innerMethod != testCase.expectedMethod {
				t.Errorf("%s：%s 的内层压缩方式应为 %d，实际 %d", testCase.name, zipEntry.Name, testCase.expectedMethod, innerMethod)
			}
		}
		reader.Close()

		outDir := filepath.Join(dir, "out")
		if _, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir, Password: "pw"}, nil); extractErr != nil {
			t.Fatalf("%s：自家 extract 解不开自己加密的归档 %v", testCase.name, extractErr)
		}
		extracted, readErr := os.ReadFile(filepath.Join(outDir, "src", "data.txt"))
		if readErr != nil || !bytes.Equal(extracted, payload) {
			t.Errorf("%s：解回的内容与原文不一致（长度 %d，应为 %d）err=%v", testCase.name, len(extracted), len(payload), readErr)
		}
	}
}

// TestEncryptedPerFilePasswordTable 逐文件密码表：不同条目配不同密码，解压时各自用自己的
// （2026-11-24 修复：桥接解码器曾只按「强度 + 内层方式」注册，同码条目共用首个条目的密码）
func TestEncryptedPerFilePasswordTable(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if mkdirErr := os.MkdirAll(srcDir, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	payloads := map[string][]byte{
		"alpha.txt": []byte("第一个文件的内容，密码是 alpha\n"),
		"beta.txt":  []byte("第二个文件的内容，密码是 beta\n"),
	}
	for name, payload := range payloads {
		if writeErr := os.WriteFile(filepath.Join(srcDir, name), payload, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	passwordFor := func(entryName string) (string, bool) {
		switch {
		case strings.HasSuffix(entryName, "alpha.txt"):
			return "alpha", true
		case strings.HasSuffix(entryName, "beta.txt"):
			return "beta", true
		}
		return "", false
	}
	archivePath := filepath.Join(dir, "table.zip")
	if _, compressErr := Compress(CompressOptions{Inputs: []string{srcDir}, Output: archivePath, Level: 6, Password: "fallback", PasswordResolver: passwordFor}, nil); compressErr != nil {
		t.Fatal(compressErr)
	}
	outDir := filepath.Join(dir, "out")
	if _, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir, Password: "fallback", PasswordResolver: passwordFor}, nil); extractErr != nil {
		t.Fatalf("逐文件密码解压失败: %v", extractErr)
	}
	for name, payload := range payloads {
		extracted, readErr := os.ReadFile(filepath.Join(outDir, "src", name))
		if readErr != nil || !bytes.Equal(extracted, payload) {
			t.Errorf("%s：内容与原文不一致 err=%v", name, readErr)
		}
	}
}

// TestEncryptedTamperIsDetected 密文被改动一个字节必须被发现——AE-2 没有 CRC，完整性全靠尾部 HMAC
// （2026-11-24 修复：内层 deflate 时解压器读到流末就停，HMAC 校验此前从不触发）
func TestEncryptedTamperIsDetected(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if mkdirErr := os.MkdirAll(srcDir, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if writeErr := os.WriteFile(filepath.Join(srcDir, "data.txt"), bytes.Repeat([]byte("完整性校验回归载荷\n"), 200), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	archivePath := filepath.Join(dir, "enc.zip")
	if _, compressErr := Compress(CompressOptions{Inputs: []string{srcDir}, Output: archivePath, Level: 6, Password: "pw", Jobs: 4}, nil); compressErr != nil {
		t.Fatal(compressErr)
	}
	untouched, readErr := os.ReadFile(archivePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	reader, openErr := zip.OpenReader(archivePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	var dataOffset int64
	var compressedSize uint64
	for _, zipEntry := range reader.File {
		if zipEntry.FileInfo().IsDir() {
			continue
		}
		offset, offsetErr := zipEntry.DataOffset()
		if offsetErr != nil {
			t.Fatal(offsetErr)
		}
		dataOffset, compressedSize = offset, zipEntry.CompressedSize64
		break
	}
	reader.Close()
	if compressedSize == 0 {
		t.Fatal("归档里没有文件条目")
	}
	// 密文段布局：salt 16B + 校验值 2B + 密文 + 认证码 10B。改动认证码的最后一个字节——
	// 密文与明文都没变，解压器照常解出完整内容，只有校验认证码才能发现
	authTailOffset := dataOffset + int64(compressedSize) - 1
	tampered := append([]byte{}, untouched...)
	tampered[authTailOffset] ^= 0x5A
	tamperedPath := filepath.Join(dir, "tampered.zip")
	if writeErr := os.WriteFile(tamperedPath, tampered, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, extractErr := Extract(ExtractOptions{Archive: tamperedPath, OutputDir: filepath.Join(dir, "out"), Password: "pw"}, nil); extractErr == nil {
		t.Fatal("密文被篡改的归档不应解压成功")
	}
}
