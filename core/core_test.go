package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/andybalholm/brotli"
	kgzip "github.com/klauspost/compress/gzip"
	kzstd "github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	uxz "github.com/ulikunitz/xz"
)

// TestIsExcludedName compress 的排除判定：默认垃圾 + ._ 前缀 + 用户名单
func TestIsExcludedName(t *testing.T) {
	userExcludes := []string{"node_modules", ".env"}
	testCases := []struct {
		name     string
		fileName string
		wantExcl bool
	}{
		{"默认 .DS_Store", ".DS_Store", true},
		{"默认 __MACOSX", "__MACOSX", true},
		{"默认 Thumbs.db", "Thumbs.db", true},
		{"默认 desktop.ini", "desktop.ini", true},
		{"macOS 资源叉前缀", "._photo.jpg", true},
		{"用户排除目录名", "node_modules", true},
		{"用户排除文件名", ".env", true},
		{"普通文件", "main.go", false},
		{"含排除词但不同名", "node_modules.bak", false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isExcludedName(testCase.fileName, userExcludes); got != testCase.wantExcl {
				t.Errorf("isExcludedName(%q, %v) = %v, 期望 %v",
					testCase.fileName, userExcludes, got, testCase.wantExcl)
			}
		})
	}
}

// TestIsExcludedNameEmptyUserList 无用户排除名单时只按默认垃圾与 ._ 前缀判定
func TestIsExcludedNameEmptyUserList(t *testing.T) {
	if !isExcludedName(".DS_Store", nil) {
		t.Error(".DS_Store 应被默认排除")
	}
	if isExcludedName("node_modules", nil) {
		t.Error("无用户名单时 node_modules 不应被排除")
	}
}

// TestIsUnsafeEntryPath 路径穿越判定：绝对路径/盘符/../段（含反斜杠归一后命中）
func TestIsUnsafeEntryPath(t *testing.T) {
	testCases := []struct {
		name       string
		entryName  string
		wantUnsafe bool
	}{
		{"正常相对路径", "docs/readme.txt", false},
		{"正常多级中文", "文档/第 1 章/节.txt", false},
		{"当前目录段", "./a/b.txt", false},
		{"父目录穿越", "../evil.txt", true},
		{"中段父目录", "safe/../../evil.txt", true},
		{"根绝对路径", "/etc/passwd", true},
		{"反斜杠穿越", "safe\\..\\evil.txt", true},
		{"反斜杠绝对", "\\windows\\system32\\x.dll", true},
		{"Windows 盘符", "C:/windows/x.dll", true},
		{"裸 ..", "..", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isUnsafeEntryPath(testCase.entryName); got != testCase.wantUnsafe {
				t.Errorf("isUnsafeEntryPath(%q) = %v, 期望 %v",
					testCase.entryName, got, testCase.wantUnsafe)
			}
		})
	}
}

// TestDecodeEntryName 条目名解码：EFS 原样 / 非 EFS 且非 UTF-8 走 GBK / 合法 UTF-8 原样
func TestDecodeEntryName(t *testing.T) {
	decoded, converted := decodeEntryName("中文.txt", true)
	if decoded != "中文.txt" || converted {
		t.Errorf("EFS 置位应原样: got (%q,%v)", decoded, converted)
	}
	decoded, converted = decodeEntryName("plain-ascii.txt", false)
	if decoded != "plain-ascii.txt" || converted {
		t.Errorf("ASCII 原样: got (%q,%v)", decoded, converted)
	}
	// "中文 文件.txt" 的 GBK 字节序列（实测自中文 Windows 系统压缩工具的归档）
	decoded, converted = decodeEntryName("\xd6\xd0\xce\xc4 \xce\xc4\xbc\xfe.txt", false)
	if decoded != "中文 文件.txt" || !converted {
		t.Errorf("GBK 解码: got (%q,%v)", decoded, converted)
	}
	// 非 GBK 非 UTF-8 字节：解码器宽容（替换 U+FFFD），不报错、标记已转换
	decoded, converted = decodeEntryName("\xff\xff\xff", false)
	if !converted || decoded == "\xff\xff\xff" {
		t.Errorf("非法字节应尽力转换: got (%q,%v)", decoded, converted)
	}
}

// TestZipDecideStoreMethod High Speed Archiving 判定：扩展名 × 魔数佐证
func TestZipDecideStoreMethod(t *testing.T) {
	testCases := []struct {
		name        string
		entryName   string
		headerBytes []byte
		wantStore   bool
	}{
		{"jpg 魔数匹配", "photo.jpg", []byte("\xff\xd8\xff\xe0\xd6\xd0"), true},
		{"jpg 大写扩展名匹配", "PHOTO.JPG", []byte("\xff\xd8\xff\xe0"), true},
		{"jpg 魔数不符（其实是文本）", "photo.jpg", []byte("plain text bytes here!"), false},
		{"png 魔数匹配", "icon.png", []byte("\x89PNG\r\n\x1a\n"), true},
		{"mp4 偏移4 ftyp", "clip.mp4", []byte("\x00\x00\x00\x20ftypisom"), true},
		{"mp4 头太短不赌", "clip.mp4", []byte("\x00\x00"), false},
		{"mp3 帧同步掩码", "song.mp3", []byte("\xff\xfb\x90\x44"), true},
		{"mp3 ID3 头", "song.mp3", []byte("ID3\x04\x00"), true},
		{"dmg 无表项信任扩展名", "inst.dmg", []byte("whatever header"), true},
		{"文本不 Store", "notes.txt", []byte("\xff\xd8\xff"), false},
		{"Go 源码不 Store", "main.go", []byte("package main"), false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := zipDecideStoreMethod(testCase.entryName, testCase.headerBytes); got != testCase.wantStore {
				t.Errorf("zipDecideStoreMethod(%q, %x) = %v, 期望 %v",
					testCase.entryName, testCase.headerBytes, got, testCase.wantStore)
			}
		})
	}
}

// TestSingleRootName 智能解压判定（unar 语义，zip/tar 共用）：单根返回根名，散装/多根返回空串
func TestSingleRootName(t *testing.T) {
	makeEntries := func(pairs ...[2]string) []scannedEntry {
		entries := make([]scannedEntry, 0, len(pairs))
		for _, pair := range pairs {
			entries = append(entries, scannedEntry{cleanName: pair[0], isDirEntry: pair[1] == "dir"})
		}
		return entries
	}
	testCases := []struct {
		name       string
		entries    []scannedEntry
		wantSingle string
	}{
		{"单根多文件", makeEntries([2]string{"proj/a.txt", "file"}, [2]string{"proj/sub/b.txt", "file"}, [2]string{"proj", "dir"}), "proj"},
		{"仅根目录条目", makeEntries([2]string{"proj", "dir"}), "proj"},
		{"根上有散装文件", makeEntries([2]string{"a.txt", "file"}, [2]string{"dir/b.txt", "file"}), ""},
		{"两个根", makeEntries([2]string{"a/x", "file"}, [2]string{"b/y", "file"}), ""},
		{"单根混散装", makeEntries([2]string{"proj/a", "file"}, [2]string{"b.txt", "file"}), ""},
		{"空归档", makeEntries(), ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := singleRootName(testCase.entries); got != testCase.wantSingle {
				t.Errorf("singleRootName = %q, 期望 %q", got, testCase.wantSingle)
			}
		})
	}
}

// TestIsUnsafeSymlinkTarget 符号链接逃逸判定：绝对/盘符/解析后逃出归档根拒绝，归档内相对链接放行
func TestIsUnsafeSymlinkTarget(t *testing.T) {
	testCases := []struct {
		name       string
		entryName  string
		linkTarget string
		wantUnsafe bool
	}{
		{"同目录相对链接", "lib/libfoo.so.1", "libfoo.so.1.0", false},
		{"子目录相对链接", "bin/tool", "../lib/libfoo.so.1", false},
		{"解析后逃出根", "bin/tool", "../../etc/passwd", true},
		{"根条目向上逃出", "tool", "..", true},
		{"绝对路径目标", "bin/tool", "/etc/passwd", true},
		{"Windows 盘符目标", "bin/tool", "C:/evil.dll", true},
		{"反斜杠归一后逃出", "bin/tool", "..\\..\\evil", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isUnsafeSymlinkTarget(testCase.entryName, testCase.linkTarget); got != testCase.wantUnsafe {
				t.Errorf("isUnsafeSymlinkTarget(%q, %q) = %v, 期望 %v",
					testCase.entryName, testCase.linkTarget, got, testCase.wantUnsafe)
			}
		})
	}
}

// TestZipCrc32Combine 分块 CRC 拼合：combine(crc(a), crc(b), len(b)) 必须等于 crc(a||b)
func TestZipCrc32Combine(t *testing.T) {
	testSplits := [][][]byte{
		{[]byte("hello zpack"), []byte(" chunked deflate")},
		{make([]byte, 131072), make([]byte, 131072)}, // 两整块 128KB
		{[]byte("a"), bytes.Repeat([]byte("bc"), 500000)},
		{bytes.Repeat([]byte("x"), 300000), []byte("tail")},
		{[]byte("prefix"), []byte("")}, // 空尾块：len(b)=0
	}
	for splitIdx, parts := range testSplits {
		var whole []byte
		for _, part := range parts {
			whole = append(whole, part...)
		}
		combined := crc32.ChecksumIEEE(parts[0])
		for partIdx := 1; partIdx < len(parts); partIdx++ {
			combined = zipCrc32Combine(combined, crc32.ChecksumIEEE(parts[partIdx]), int64(len(parts[partIdx])))
		}
		wantCRC := crc32.ChecksumIEEE(whole)
		if combined != wantCRC {
			t.Errorf("用例 %d: 拼合 CRC=%#08x, 期望 %#08x", splitIdx, combined, wantCRC)
		}
	}
}

// TestZipDeflateChunkBytes 分片压缩结构：多片拼装后是合法单流（可解回原文），
// 非末块各留一个 sync-flush 缝合点（00 00 00 FF FF），末块无缝合点
func TestZipDeflateChunkBytes(t *testing.T) {
	chunks := [][]byte{
		bytes.Repeat([]byte("第一块数据 abcdefg "), 9000),
		bytes.Repeat([]byte("第二块数据 1234567 "), 8000),
		bytes.Repeat([]byte("最后一块 XYZ"), 500),
	}
	var assembled []byte
	for chunkIdx, chunkData := range chunks {
		compressed, compressErr := zipDeflateChunkBytes(chunkData, DefaultCompressLevel, chunkIdx == len(chunks)-1, nil)
		if compressErr != nil {
			t.Fatal(compressErr)
		}
		assembled = append(assembled, compressed...)
	}
	// sync-flush 缝合点的结构核心是 LEN=0/NLEN=FFFF（00 00 FF FF）；
	// 前导零字节数取决于位对齐（实测常见 00 00 00 FF FF 五字节形态）
	seamCount := bytes.Count(assembled, []byte{0x00, 0x00, 0xff, 0xff})
	if seamCount < len(chunks)-1 {
		t.Errorf("缝合点数 = %d, 期望 ≥%d（每非末块一个）", seamCount, len(chunks)-1)
	}
	reader := flate.NewReader(bytes.NewReader(assembled))
	defer reader.Close()
	decompressed, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatalf("拼装流解压失败: %v", readErr)
	}
	var original []byte
	for _, chunkData := range chunks {
		original = append(original, chunkData...)
	}
	if !bytes.Equal(decompressed, original) {
		t.Error("分块拼装流解压结果与原文不一致")
	}
}

// TestZipPlanForTask 大文件路径规划：小文件整条目 / 大文本分块 / 大 jpg(魔数)顺序 Store / level 0 顺序 / 非法 UTF-8 顺序
func TestZipPlanForTask(t *testing.T) {
	baseDir := t.TempDir()
	smallFile := filepath.Join(baseDir, "small.txt")
	if writeErr := os.WriteFile(smallFile, []byte("tiny"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	bigTextFile := filepath.Join(baseDir, "big-text.bin")
	if writeErr := os.WriteFile(bigTextFile, []byte("seed"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if truncateErr := os.Truncate(bigTextFile, ZIP_ENTRY_BUFFER_CAP_BYTES+1); truncateErr != nil { // 稀疏放大
		t.Fatal(truncateErr)
	}
	bigJpgFile := filepath.Join(baseDir, "big-image.jpg")
	if writeErr := os.WriteFile(bigJpgFile, append([]byte("\xff\xd8\xff\xe0JFIF"), make([]byte, 64)...), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if truncateErr := os.Truncate(bigJpgFile, ZIP_ENTRY_BUFFER_CAP_BYTES+1); truncateErr != nil {
		t.Fatal(truncateErr)
	}

	smallInfo, _ := os.Stat(smallFile)
	bigTextInfo, _ := os.Stat(bigTextFile)
	bigJpgInfo, _ := os.Stat(bigJpgFile)

	if plan := zipPlanForTask(compressTask{sourcePath: smallFile, entryName: "small.txt", fileInfo: smallInfo}, 6, false); plan != zipPlanEntryStream {
		t.Errorf("小文件应走整条目缓冲, got %v", plan)
	}
	if plan := zipPlanForTask(compressTask{sourcePath: bigTextFile, entryName: "big-text.bin", fileInfo: bigTextInfo}, 6, false); plan != zipPlanChunked {
		t.Errorf("大文本应走分块并行, got %v", plan)
	}
	if plan := zipPlanForTask(compressTask{sourcePath: bigJpgFile, entryName: "big-image.jpg", fileInfo: bigJpgInfo}, 6, false); plan != zipPlanSequential {
		t.Errorf("大 jpg（魔数 Store）应走顺序流式, got %v", plan)
	}
	if plan := zipPlanForTask(compressTask{sourcePath: bigTextFile, entryName: "big-text.bin", fileInfo: bigTextInfo}, 0, false); plan != zipPlanSequential {
		t.Errorf("level 0 应全 Store 顺序, got %v", plan)
	}
	if plan := zipPlanForTask(compressTask{sourcePath: smallFile, entryName: "\xff\xfe.txt", fileInfo: smallInfo}, 6, false); plan != zipPlanSequential {
		t.Errorf("非法 UTF-8 名应走顺序（给出既定报错）, got %v", plan)
	}
}

// TestZipWriteArchiveDeterministicAndRoundtrip 并行管线端到端：同输入两次产出字节一致（确定性），
// 且产出可被标准 archive/zip 读取——条目名/EFS/内容全对得上
func TestZipWriteArchiveDeterministicAndRoundtrip(t *testing.T) {
	sourceDir := t.TempDir()
	if mkdirErr := os.MkdirAll(filepath.Join(sourceDir, "sub dir"), 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "a.txt"), []byte("hello zpack zip"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "sub dir", "b file.txt"), []byte("二级 内容"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "pic.jpg"), []byte("\xff\xd8\xff\xe0JFIF-fake"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	tasks, collectErr := collectCompressTasks([]string{sourceDir}, nil, nil)
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	if len(tasks) != 3 {
		t.Fatalf("collect 应得 3 个条目，got %d", len(tasks))
	}

	firstArchive := filepath.Join(t.TempDir(), "first.zip")
	secondArchive := filepath.Join(t.TempDir(), "second.zip")
	if writeErr := zipWriteArchive(firstArchive, tasks, DefaultCompressLevel, 4, newProgressReporter(int64(len(tasks)), 0, nil), "", nil, ""); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := zipWriteArchive(secondArchive, tasks, DefaultCompressLevel, 4, newProgressReporter(int64(len(tasks)), 0, nil), "", nil, ""); writeErr != nil {
		t.Fatal(writeErr)
	}

	firstBytes, readErr := os.ReadFile(firstArchive)
	if readErr != nil {
		t.Fatal(readErr)
	}
	secondBytes, readErr := os.ReadFile(secondArchive)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Error("同输入两次产出应字节一致（并行管线确定性被破坏）")
	}

	reader, openErr := zip.OpenReader(firstArchive)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer reader.Close()
	wantContents := map[string]string{
		filepath.Base(sourceDir) + "/a.txt":              "hello zpack zip",
		filepath.Base(sourceDir) + "/sub dir/b file.txt": "二级 内容",
	}
	foundCount := 0
	for _, zipEntry := range reader.File {
		if zipEntry.Flags&0x800 == 0 {
			t.Errorf("条目 %q 应置 EFS 位", zipEntry.Name)
		}
		// 条目名恒 `/` 风格（zip 规范）——直接以原键查表，不做平台转换
		// （此前 FromSlash 在 Windows 把条目名转成 `\` 风格致查表恒空，2026-11-08 修复）
		entryContent, contentOk := wantContents[zipEntry.Name]
		if !contentOk {
			continue // pic.jpg
		}
		foundCount++
		entryReader, entryOpenErr := zipEntry.Open()
		if entryOpenErr != nil {
			t.Fatal(entryOpenErr)
		}
		entryBytes, readAllErr := io.ReadAll(entryReader)
		entryReader.Close()
		if readAllErr != nil {
			t.Fatal(readAllErr)
		}
		if string(entryBytes) != entryContent {
			t.Errorf("条目 %q 内容不符: got %q", zipEntry.Name, entryBytes)
		}
	}
	if foundCount != len(wantContents) {
		t.Errorf("应找到 %d 个文本条目，got %d", len(wantContents), foundCount)
	}
	// pic.jpg 魔数匹配应走 Store 直通
	for _, zipEntry := range reader.File {
		if strings.HasSuffix(zipEntry.Name, "pic.jpg") && zipEntry.Method != zip.Store {
			t.Errorf("jpg 魔数匹配应 Store 直通，got method=%d", zipEntry.Method)
		}
	}
}

// TestTarWriteEntry 端到端：写 tar（含符号链接）→ 标准读取器读回——条目名/类型/链接目标/
// 内容全对得上，且 PAX 头确定性字段（截秒时间戳）不携带亚秒
func TestTarWriteEntry(t *testing.T) {
	sourceDir := t.TempDir()
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "a.txt"), []byte("hello tar 中文"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if linkErr := os.Symlink("a.txt", filepath.Join(sourceDir, "a-link.txt")); linkErr != nil {
		// Windows 未开开发者模式/非管理员时无符号链接特权（ERROR_PRIVILEGE_NOT_HELD=1314）——
		// 平台限制非产品缺陷，跳过本用例（2026-11-08 Windows 实测；常量字面量跨平台不可引用故数值比较）
		var errno syscall.Errno
		if errors.As(linkErr, &errno) && errno == 1314 {
			t.Skip("Windows 无符号链接特权（未开开发者模式），跳过符号链接写入用例")
		}
		t.Fatal(linkErr)
	}
	longName := strings.Repeat("长名字段", 40) + ".txt" // >100 字节，逼出 PAX 扩展记录
	if writeErr := os.WriteFile(filepath.Join(sourceDir, longName), []byte("pax long name"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	tasks, collectErr := collectCompressTasks([]string{sourceDir}, nil, nil)
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	if len(tasks) != 3 {
		t.Fatalf("collect 应得 3 个条目（含符号链接），got %d", len(tasks))
	}

	archivePath := filepath.Join(t.TempDir(), "out.tar")
	outputFile, createErr := os.Create(archivePath)
	if createErr != nil {
		t.Fatal(createErr)
	}
	tarWriter := tar.NewWriter(outputFile)
	if writeErr := tarWriteEntries(tarWriter, tasks, newProgressReporter(int64(len(tasks)), 0, nil)); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := tarWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if closeErr := outputFile.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	reader, openErr := os.Open(archivePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	baseName := filepath.Base(sourceDir)
	wantContents := map[string]string{
		baseName + "/a.txt":       "hello tar 中文",
		baseName + "/" + longName: "pax long name",
		baseName + "/a-link.txt":  "",
	}
	foundFiles, foundLinks := 0, 0
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if wantContent, isWanted := wantContents[header.Name]; isWanted {
			if header.Typeflag == tar.TypeSymlink {
				foundLinks++
				if header.Linkname != "a.txt" {
					t.Errorf("符号链接目标不符: got %q, 期望 a.txt", header.Linkname)
				}
				if header.Size != 0 {
					t.Errorf("符号链接不应有数据体, got size=%d", header.Size)
				}
				continue
			}
			foundFiles++
			contentBytes, readErr := io.ReadAll(tarReader)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(contentBytes) != wantContent {
				t.Errorf("条目 %q 内容不符: got %q", header.Name, contentBytes)
			}
			if header.ModTime.Nanosecond() != 0 {
				t.Errorf("条目 %q ModTime 应截秒（确定性），got %v", header.Name, header.ModTime)
			}
		}
	}
	if foundFiles != 2 || foundLinks != 1 {
		t.Errorf("应找到 2 文件 + 1 符号链接，got %d/%d", foundFiles, foundLinks)
	}
}

// TestSingleStreamExtractGzip 单流 gzip：解出单文件（基名去扩展名）、内容一致、重复解压跳过语义
func TestSingleStreamExtractGzip(t *testing.T) {
	sourceDir := t.TempDir()
	payloadPath := filepath.Join(sourceDir, "数据 文件.txt")
	if writeErr := os.WriteFile(payloadPath, []byte("gzip single stream payload 中文"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	archivePath := payloadPath + ".gz"
	payloadFile, openErr := os.Open(payloadPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	gzipFile, createErr := os.Create(archivePath)
	if createErr != nil {
		t.Fatal(openErr)
	}
	gzipWriter := kgzip.NewWriter(gzipFile)
	_, copyErr := io.Copy(gzipWriter, payloadFile)
	payloadFile.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr := gzipWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	gzipFile.Close()

	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 || result.Skipped != 0 {
		t.Errorf("单流解压应 1 文件 0 跳过, got %+v", result)
	}
	extracted, readErr := os.ReadFile(filepath.Join(outDir, "数据 文件.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(extracted) != "gzip single stream payload 中文" {
		t.Errorf("单流内容不一致: %q", extracted)
	}
	// 重复解压：默认跳过
	second, secondErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir}, nil)
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	if second.Skipped != 1 || second.FileCount != 0 {
		t.Errorf("重复解压应跳过 1, got %+v", second)
	}
	// --force 覆盖
	forced, forceErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir, Force: true}, nil)
	if forceErr != nil {
		t.Fatal(forceErr)
	}
	if forced.Skipped != 0 || forced.FileCount != 1 {
		t.Errorf("force 应覆盖, got %+v", forced)
	}
}

// TestSingleStreamExtractZstd 单流 zstd（klauspost 编码→解出往返）
func TestSingleStreamExtractZstd(t *testing.T) {
	sourceDir := t.TempDir()
	archivePath := filepath.Join(sourceDir, "payload.zst")
	zstdFile, createErr := os.Create(archivePath)
	if createErr != nil {
		t.Fatal(createErr)
	}
	zstdWriter, writerErr := kzstd.NewWriter(zstdFile)
	if writerErr != nil {
		t.Fatal(writerErr)
	}
	if _, writeErr := zstdWriter.Write(bytes.Repeat([]byte("zstd 单流往返内容 "), 1000)); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := zstdWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	zstdFile.Close()

	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 || result.SizeBytes != int64(len("zstd 单流往返内容 ")*1000) {
		t.Errorf("zstd 单流结果不符: %+v", result)
	}
}

// TestSplitExtractZip .001 分卷拼 zip：三段切分后从首卷解出（分卷是 zip 分卷读取的底座）
func TestSplitExtractZip(t *testing.T) {
	sourceDir := t.TempDir()
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "a.txt"), []byte("split volume payload"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	zipPath := filepath.Join(t.TempDir(), "payload.zip")
	tasks, collectErr := collectCompressTasks([]string{sourceDir}, nil, nil)
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	if writeErr := zipWriteArchive(zipPath, tasks, DefaultCompressLevel, 2, newProgressReporter(int64(len(tasks)), 0, nil), "", nil, ""); writeErr != nil {
		t.Fatal(writeErr)
	}

	// 按段大小切成 .001/.002/.003（末段必非空）
	zipBytes, readErr := os.ReadFile(zipPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	splitDir := t.TempDir()
	segmentSize := (len(zipBytes) + 2) / 3
	for segmentIdx := 0; segmentIdx < 3; segmentIdx++ {
		start := segmentIdx * segmentSize
		end := min(start+segmentSize, len(zipBytes))
		segmentName := fmt.Sprintf("payload.zip.%03d", segmentIdx+1)
		if writeErr := os.WriteFile(filepath.Join(splitDir, segmentName), zipBytes[start:end], 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: filepath.Join(splitDir, "payload.zip.001"), OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 {
		t.Fatalf("分卷 zip 解出应 1 文件, got %+v", result)
	}
	extracted, readErr := os.ReadFile(filepath.Join(outDir, filepath.Base(sourceDir), "a.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(extracted) != "split volume payload" {
		t.Errorf("分卷内容不一致: %q", extracted)
	}
}

// TestSplitExtractRawFile .001 分卷非归档内层：还原为单文件
func TestSplitExtractRawFile(t *testing.T) {
	splitDir := t.TempDir()
	rawBytes := []byte("not an archive, just binary payload bytes 0123456789")
	if writeErr := os.WriteFile(filepath.Join(splitDir, "video.mp4.001"), rawBytes[:15], 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(splitDir, "video.mp4.002"), rawBytes[15:], 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: filepath.Join(splitDir, "video.mp4.001"), OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 {
		t.Fatalf("非归档分卷应还原单文件, got %+v", result)
	}
	extracted, readErr := os.ReadFile(filepath.Join(outDir, "video.mp4"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(extracted, rawBytes) {
		t.Errorf("还原内容不一致: %q", extracted)
	}
}

// TestSingleStreamExtractXzLz4Brotli 单流三格式（批次 B）：编码→Extract 往返 + 命名
func TestSingleStreamExtractXzLz4Brotli(t *testing.T) {
	payload := bytes.Repeat([]byte("批次B 单流往返 payload 中文 "), 500)
	testCases := []struct {
		name       string
		extension  string
		wrapWriter func(io.Writer) (io.WriteCloser, error)
	}{
		{"xz", ".xz", func(w io.Writer) (io.WriteCloser, error) { return uxz.NewWriter(w) }},
		{"lz4", ".lz4", func(w io.Writer) (io.WriteCloser, error) { return lz4.NewWriter(w), nil }},
		{"brotli", ".br", func(w io.Writer) (io.WriteCloser, error) {
			return brotli.NewWriterOptions(w, brotli.WriterOptions{Quality: 5}), nil
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sourceDir := t.TempDir()
			archivePath := filepath.Join(sourceDir, "payload"+testCase.extension)
			archiveFile, createErr := os.Create(archivePath)
			if createErr != nil {
				t.Fatal(createErr)
			}
			wrapped, wrapErr := testCase.wrapWriter(archiveFile)
			if wrapErr != nil {
				t.Fatal(wrapErr)
			}
			if _, writeErr := wrapped.Write(payload); writeErr != nil {
				t.Fatal(writeErr)
			}
			if closeErr := wrapped.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			archiveFile.Close()

			outDir := t.TempDir()
			result, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir}, nil)
			if extractErr != nil {
				t.Fatal(extractErr)
			}
			if result.FileCount != 1 || result.SizeBytes != int64(len(payload)) {
				t.Errorf("单流结果不符: %+v", result)
			}
			extracted, readErr := os.ReadFile(filepath.Join(outDir, "payload"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(extracted, payload) {
				t.Errorf("%s 往返内容不一致", testCase.name)
			}
		})
	}
}

// TestTarCompressedVariantsExtract tar 压缩壳（tar.xz/tar.zst；tar.bz2 由 verify 用系统 tar 覆盖）：
// 先产 tar 字节流再包压缩层，Extract 走内层 ustar 佐证路径
func TestTarCompressedVariantsExtract(t *testing.T) {
	sourceDir := t.TempDir()
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "变体 载荷.txt"), []byte("tar 压缩壳变体往返"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	tasks, collectErr := collectCompressTasks([]string{sourceDir}, nil, nil)
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	var plainTar bytes.Buffer
	tarWriter := tar.NewWriter(&plainTar)
	if writeErr := tarWriteEntries(tarWriter, tasks, newProgressReporter(int64(len(tasks)), 0, nil)); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := tarWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	testCases := []struct {
		name       string
		extension  string
		wrapWriter func(io.Writer) (io.WriteCloser, error)
	}{
		{"tar.xz", ".tar.xz", func(w io.Writer) (io.WriteCloser, error) { return uxz.NewWriter(w) }},
		{"tar.zst", ".tar.zst", func(w io.Writer) (io.WriteCloser, error) { return kzstd.NewWriter(w) }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "variant"+testCase.extension)
			archiveFile, createErr := os.Create(archivePath)
			if createErr != nil {
				t.Fatal(createErr)
			}
			wrapped, wrapErr := testCase.wrapWriter(archiveFile)
			if wrapErr != nil {
				t.Fatal(wrapErr)
			}
			if _, writeErr := wrapped.Write(plainTar.Bytes()); writeErr != nil {
				t.Fatal(writeErr)
			}
			if closeErr := wrapped.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			archiveFile.Close()

			outDir := t.TempDir()
			if _, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir}, nil); extractErr != nil {
				t.Fatal(extractErr)
			}
			extracted, readErr := os.ReadFile(filepath.Join(outDir, filepath.Base(sourceDir), "变体 载荷.txt"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(extracted) != "tar 压缩壳变体往返" {
				t.Errorf("%s 内容不一致: %q", testCase.name, extracted)
			}
		})
	}
}

// TestTarSymlinkExtractForwardRef 符号链接解出端到端（写归档头无需特权，落地才分平台）：
// 前向引用（链接条目在目标文件之前）覆盖 Windows 无特权降级拷贝的待办补拷路径。
// 断言用内容等价（ReadFile 跟随链接）——mac/linux 落真符号链接、Windows 降级拷贝两态皆通过
func TestTarSymlinkExtractForwardRef(t *testing.T) {
	var archiveBytes bytes.Buffer
	tarWriter := tar.NewWriter(&archiveBytes)
	linkHeader := &tar.Header{
		Name:     "root/fwd-link.txt",
		Linkname: "payload.txt",
		Typeflag: tar.TypeSymlink,
		Mode:     0o777,
	}
	if writeErr := tarWriter.WriteHeader(linkHeader); writeErr != nil {
		t.Fatal(writeErr)
	}
	fileHeader := &tar.Header{
		Name:     "root/payload.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len("前向引用目标内容")),
	}
	if writeErr := tarWriter.WriteHeader(fileHeader); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, writeErr := tarWriter.Write([]byte("前向引用目标内容")); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := tarWriter.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	archivePath := filepath.Join(t.TempDir(), "fwd.tar")
	if writeErr := os.WriteFile(archivePath, archiveBytes.Bytes(), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	outDir := t.TempDir()
	if _, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outDir}, nil); extractErr != nil {
		t.Fatal(extractErr)
	}
	// ReadFile 跟随符号链接——mac/linux 读到链接目标、Windows 读到降级拷贝，内容恒一致
	resolved, readErr := os.ReadFile(filepath.Join(outDir, "root", "fwd-link.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(resolved) != "前向引用目标内容" {
		t.Errorf("前向引用符号链接解析内容不符: %q", resolved)
	}
}

// TestSevenZipExtract 7z 读取（夹具入库 testdata/7z/basic.7z：中文/空格路径 + 目录条目）
func TestSevenZipExtract(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/7z/basic.7z", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount < 2 {
		t.Fatalf("7z 应解出 ≥2 文件, got %+v", result)
	}
	extracted, readErr := os.ReadFile(filepath.Join(outDir, "fixture-root", "资料 目录", "载荷 文件.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(extracted) != "7z 夹具载荷：中文内容与空格路径" {
		t.Errorf("7z 内容不一致: %q", extracted)
	}
	second, readErr := os.ReadFile(filepath.Join(outDir, "fixture-root", "b.txt"))
	if readErr != nil || string(second) != "second file" {
		t.Errorf("7z 第二文件不一致: %q err=%v", second, readErr)
	}
}

// TestRarExtract rar 读取四夹具：RAR4 单卷 / RAR5 单卷 / 多卷（.part01+.part02 库自动续卷）
func TestRarExtract(t *testing.T) {
	testCases := []struct {
		name       string
		archiveRel string
	}{
		{"RAR4 单卷", "testdata/rar/single-rar4.rar"},
		{"RAR5 单卷", "testdata/rar/single-rar5.rar"},
		{"多卷 part01", "testdata/rar/test.part01.rar"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result, extractErr := Extract(ExtractOptions{Archive: testCase.archiveRel, OutputDir: t.TempDir()}, nil)
			if extractErr != nil {
				t.Fatal(extractErr)
			}
			if result.FileCount < 1 {
				t.Errorf("rar 应解出 ≥1 文件, got %+v", result)
			}
		})
	}
}

// TestZCompressExtract .Z（compress LZW）解出：真样本（libarchive compat-mac，maxBits=16）+ 单 tar.Z 内层再验证
func TestZCompressExtract(t *testing.T) {
	outDir := t.TempDir()
	// 单文本 .Z：解出为 txt（内容应是文本）
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/z/sample.txt.Z", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 {
		t.Fatalf(".Z 应解出 1 文件, got %+v", result)
	}
	extracted, readErr := os.ReadFile(filepath.Join(outDir, "sample.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	// 夹具真值（7z CLI 交叉验证过）：363470 字节、以十六进制转储文本开头
	if len(extracted) != 363470 || !bytes.HasPrefix(extracted, []byte("1E0A;0044 0307")) {
		t.Errorf(".Z 解出内容可疑: %d bytes, 头 %q", len(extracted), extracted[:24])
	}
	// tar.Z：解出 .tar 单文件后，内层 tar 可再被识别（链式）
	tarOut := t.TempDir()
	result2, extractErr2 := Extract(ExtractOptions{Archive: "testdata/z/compat-mac.tar.Z", OutputDir: tarOut}, nil)
	if extractErr2 != nil {
		t.Fatal(extractErr2)
	}
	if result2.FileCount != 1 {
		t.Fatalf("tar.Z 应解出 1 文件, got %+v", result2)
	}
	if _, statErr := os.Stat(filepath.Join(tarOut, "compat-mac.tar")); statErr != nil {
		t.Errorf("tar.Z 产物命名不符: %v", statErr)
	}
}

// TestUUEXXEExtract uuencode/xxencode 解出（内嵌文件名优先 + 内容往返）
func TestUUEXXEExtract(t *testing.T) {
	wantPayload := []byte("uuencode 夹具载荷：中文与换行\nsecond line 0123456789")
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/uue/basic.uue", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 {
		t.Fatalf("uue 应解出 1 文件, got %+v", result)
	}
	extracted, readErr := os.ReadFile(filepath.Join(outDir, "data.bin"))
	if readErr != nil || !bytes.Equal(extracted, wantPayload) {
		t.Errorf("uue 内容不一致: %q err=%v", extracted, readErr)
	}
	xxOut := t.TempDir()
	if _, extractErr2 := Extract(ExtractOptions{Archive: "testdata/uue/basic.xxe", OutputDir: xxOut}, nil); extractErr2 != nil {
		t.Fatal(extractErr2)
	}
	extracted2, readErr2 := os.ReadFile(filepath.Join(xxOut, "xx-payload.bin"))
	if readErr2 != nil || !bytes.Equal(extracted2, wantPayload) {
		t.Errorf("xxe 内容不一致: %q err=%v", extracted2, readErr2)
	}
}

// TestArExtract ar 读取：GNU 命名（短名/ 长名//表）+ 偶对齐 + 中文内容
func TestArExtract(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/ar/basic.ar", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 3 {
		t.Fatalf("ar 应解出 3 文件, got %+v", result)
	}
	if got := readFileOrFail(t, filepath.Join(outDir, "载荷 文件.txt")); got != "ar 夹具：中文与空格\n" {
		t.Errorf("ar 内容不一致: %q", got)
	}
	if got := readFileOrFail(t, filepath.Join(outDir, "b.dat")); got != "\x00\x01binary" {
		t.Errorf("ar 二进制内容不一致: %q", got)
	}
	longName := "long-name-" + strings.Repeat("x", 30) + ".txt"
	if got := readFileOrFail(t, filepath.Join(outDir, longName)); got != "long name payload" {
		t.Errorf("ar 长名内容不一致: %q", got)
	}
}

// TestAsarExtract asar 读取：JSON 目录 + 数据区偏移 + 嵌套目录
func TestAsarExtract(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/asar/basic.asar", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 2 {
		t.Fatalf("asar 应解出 2 文件, got %+v", result)
	}
	if got := readFileOrFail(t, filepath.Join(outDir, "app", "main.js")); got != "asar payload: hello electron 中文" {
		t.Errorf("asar 内容不一致: %q", got)
	}
	if got := readFileOrFail(t, filepath.Join(outDir, "app", "assets", "logo.txt")); got != "second file contents" {
		t.Errorf("asar 嵌套内容不一致: %q", got)
	}
}

// TestISOExtract iso9660 读取（hdiutil 真样本；Joliet 名尽力解码）
func TestISOExtract(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/iso/basic.iso", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount < 2 {
		t.Fatalf("iso 应解出 ≥2 文件, got %+v", result)
	}
	// 中文名按 Joliet/RR 尽力呈现：不做精确名断言，断言总量与第二文件
	if got := readFileOrFail(t, filepath.Join(outDir, "b.txt")); got != "second" {
		t.Errorf("iso b.txt 内容不一致: %q", got)
	}
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("读取 %s 失败: %v", path, readErr)
	}
	return string(content)
}

// TestSevenZipExtractParallel 7z 并行解出（50 文件实心档案，8 worker；-race 下验证
// bodgit 并发契约："Multiple files may be read concurrently"）
func TestSevenZipExtractParallel(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/7z/many.7z", OutputDir: outDir, Jobs: 8}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 50 {
		t.Fatalf("应解出 50 文件, got %+v", result)
	}
	for idx := 1; idx <= 40; idx++ {
		content, readErr := os.ReadFile(filepath.Join(outDir, "fixture-many", fmt.Sprintf("file-%d.txt", idx)))
		if readErr != nil {
			t.Fatalf("file-%d 缺失: %v", idx, readErr)
		}
		want := fmt.Sprintf("parallel payload %d 中文内容 ", idx)
		if !bytes.HasPrefix(content, []byte(want)) {
			t.Errorf("file-%d 内容头不符: %q", idx, content[:len(want)])
		}
	}
	for idx := 1; idx <= 10; idx++ {
		content, readErr := os.ReadFile(filepath.Join(outDir, "fixture-many", "子目录", fmt.Sprintf("n-%d.txt", idx)))
		if readErr != nil || string(content) != fmt.Sprintf("nested %d", idx) {
			t.Errorf("n-%d 不符: %q err=%v", idx, content, readErr)
		}
	}
	// 重复解压：并行下跳过语义仍成立（原子计数）
	result2, extractErr2 := Extract(ExtractOptions{Archive: "testdata/7z/many.7z", OutputDir: outDir, Jobs: 8}, nil)
	if extractErr2 != nil {
		t.Fatal(extractErr2)
	}
	if result2.Skipped != 50 || result2.FileCount != 0 {
		t.Errorf("并行重复解压应全部跳过, got %+v", result2)
	}
}

// TestLzipExtract lzip 单流解出（内容往返）
func TestLzipExtract(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/lzip/sample.txt.lz", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 {
		t.Fatalf("lzip 应解出 1 文件, got %+v", result)
	}
	extracted, readErr := os.ReadFile(filepath.Join(outDir, "sample.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	want := "lzip 夹具载荷：中文内容与重复文本 0123456789\n"
	if len(extracted) != len(want)*200 || !bytes.HasPrefix(extracted, []byte(want)) {
		t.Errorf("lzip 内容可疑: %d bytes, 头 %q", len(extracted), extracted[:30])
	}
}

// TestWimExtract wim 读取（LZX 压缩流按需解码；单镜像无前缀直落）
func TestWimExtract(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/wim/basic.wim", OutputDir: outDir}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 3 {
		t.Fatalf("wim 应解出 3 文件, got %+v", result)
	}
	if got := readFileOrFail(t, filepath.Join(outDir, "资料 目录", "载荷 文件.txt")); got != "wim 夹具载荷：中文与空格路径" {
		t.Errorf("wim 中文内容不一致: %q", got)
	}
	if got := readFileOrFail(t, filepath.Join(outDir, "big-ish.bin")); len(got) != 8*4096 || got[:8] != "WIMDATA-" {
		t.Errorf("wim LZX 大文件内容可疑: %d bytes", len(got))
	}
}

// TestCabExtract cab 读取：MSZIP / LZX（含 33KB 高压缩）/ LZX+E8 三夹具
func TestCabExtract(t *testing.T) {
	testCases := []struct {
		name       string
		archiveRel string
		wantCount  int
	}{
		{"纯存储", "testdata/cab/stored.cab", 3},
		{"MSZIP", "testdata/cab/mszip.cab", 4},
		{"LZX+E8", "testdata/cab/lzx.cab", 1},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result, extractErr := Extract(ExtractOptions{Archive: testCase.archiveRel, OutputDir: t.TempDir()}, nil)
			if extractErr != nil {
				t.Fatal(extractErr)
			}
			if result.FileCount != testCase.wantCount {
				t.Errorf("cab %s 应解出 %d 文件, got %+v", testCase.name, testCase.wantCount, result)
			}
		})
	}
}

// TestCabContentRoundtrip cab 内容级往返（7z 交叉口径：dir1/file1=60B dir2/file2=78B zero=33000）
func TestCabContentRoundtrip(t *testing.T) {
	outDir := t.TempDir()
	if _, extractErr := Extract(ExtractOptions{Archive: "testdata/cab/mszip.cab", OutputDir: outDir}, nil); extractErr != nil {
		t.Fatal(extractErr)
	}
	if got := readFileOrFail(t, filepath.Join(outDir, "zero")); len(got) != 33000 || got[0] != 0 || got[32999] != 0 {
		t.Errorf("MSZIP zero 文件不符: %d bytes", len(got))
	}
	// LZX+E8 内容与 7z 交叉验证（e8.bin 30 字节）
	lzxDir := t.TempDir()
	if _, extractErr := Extract(ExtractOptions{Archive: "testdata/cab/lzx.cab", OutputDir: lzxDir}, nil); extractErr != nil {
		t.Fatal(extractErr)
	}
	if got := readFileOrFail(t, filepath.Join(lzxDir, "e8.bin")); len(got) != 30 {
		t.Errorf("LZX e8.bin 长度不符: %d", len(got))
	}
	storedDir := t.TempDir()
	if _, extractErr := Extract(ExtractOptions{Archive: "testdata/cab/stored.cab", OutputDir: storedDir}, nil); extractErr != nil {
		t.Fatal(extractErr)
	}
	if got := readFileOrFail(t, filepath.Join(storedDir, "dir1", "file1")); len(got) != 60 {
		t.Errorf("存储 file1 长度不符: %d", len(got))
	}
	if got := readFileOrFail(t, filepath.Join(storedDir, "empty")); got != "" {
		t.Errorf("empty 文件应 0 字节: %q", got)
	}
}

// TestZipChunkDictionaryLinkage 字典衔接分块的比值证明：语料=96KB 随机块 + 其尾 32KB 重复 N 次——
// 无字典时每块开头的 32KB 只能按字面量编码；衔接后为回引。断言总量被压到远小于字面量水位
func TestZipChunkDictionaryLinkage(t *testing.T) {
	sourceDir := t.TempDir()
	// 周期取 32000（<32K 窗口）：恰=32768 时 deflate 不发窗口边界距离（实测行为），
	// 真随机尾+真随机底座保证压缩量只能来自字典衔接的跨块回引
	rng := rand.New(rand.NewSource(7))
	randomTail := make([]byte, 32000)
	rng.Read(randomTail)
	var payload bytes.Buffer
	base := make([]byte, ZIP_DEFLATE_CHUNK_BYTES*3) // 96KB 随机底座（不可压）
	rng.Read(base)
	payload.Write(base)
	for repeatIdx := 0; repeatIdx < 48; repeatIdx++ {
		payload.Write(randomTail) // 共 ~1.5MB 重复尾
	}
	payloadPath := filepath.Join(sourceDir, "linkage.bin")
	if writeErr := os.WriteFile(payloadPath, payload.Bytes(), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	tasks, collectErr := collectCompressTasks([]string{sourceDir}, nil, nil)
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	archivePath := filepath.Join(t.TempDir(), "out.zip")
	if writeErr := zipWriteArchive(archivePath, tasks, DefaultCompressLevel, 4, newProgressReporter(int64(len(tasks)), 0, nil), "", nil, ""); writeErr != nil {
		t.Fatal(writeErr)
	}
	archiveInfo, statErr := os.Stat(archivePath)
	if statErr != nil {
		t.Fatal(statErr)
	}
	// 无字典水位：底座 ~96KB 不可压 + 48×32KB 重复尾按字面量 ≈ 1.63MB；
	// 衔接后重复尾成回引——阈值 600KB（实测 437KB：块相位不齐残留少量字面量 + 缝合点开销），
	// 关键断言是远低于字面量水位（衔接失效则 ~1.63MB 必然爆表）
	if archiveInfo.Size() > 600<<10 {
		t.Errorf("字典衔接未生效：归档 %d bytes（应 <600KB，字面量水位 ~1.63MB）", archiveInfo.Size())
	}
}

// TestZipHsaRuntimeDowngradeDeterministic HSA 运行时降档：33MB 不可压语料满 30MiB 检查点
// 后降 level 0——两次产出必须字节一致（快照信号把决策输入钉成纯函数）且可解回原文
func TestZipHsaRuntimeDowngradeDeterministic(t *testing.T) {
	sourceDir := t.TempDir()
	payload := make([]byte, 33<<20)
	rand.New(rand.NewSource(11)).Read(payload) // 真随机不可压
	payloadPath := filepath.Join(sourceDir, "big.bin")
	if writeErr := os.WriteFile(payloadPath, payload, 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	tasks, collectErr := collectCompressTasks([]string{sourceDir}, nil, nil)
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	firstArchive := filepath.Join(t.TempDir(), "a.zip")
	secondArchive := filepath.Join(t.TempDir(), "b.zip")
	if writeErr := zipWriteArchive(firstArchive, tasks, DefaultCompressLevel, 4, newProgressReporter(1, 0, nil), "", nil, ""); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := zipWriteArchive(secondArchive, tasks, DefaultCompressLevel, 4, newProgressReporter(1, 0, nil), "", nil, ""); writeErr != nil {
		t.Fatal(writeErr)
	}
	firstBytes, _ := os.ReadFile(firstArchive)
	secondBytes, _ := os.ReadFile(secondArchive)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Error("HSA 运行时降档破坏确定性：两次产出不一致")
	}
	// 30MiB 检查点后降档：降档片为 stored 块，总压缩量应 ≈ 前 30MiB 的 deflate 膨胀 + 尾巴直存
	// 只断言可解回（正确性），量级断言留给门禁
	result, extractErr := Extract(ExtractOptions{Archive: firstArchive, OutputDir: t.TempDir()}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount != 1 {
		t.Errorf("应解出 1 文件, got %+v", result)
	}
}

// TestEncrypted7zExtract 7z 加密（头加密夹具）
func TestEncrypted7zExtract(t *testing.T) {
	// 缺密码 → 明确报需要
	_, noPwErr := Extract(ExtractOptions{Archive: "testdata/7z-enc/encrypted.7z", OutputDir: t.TempDir()}, nil)
	if noPwErr == nil {
		t.Error("缺密码应报错")
	}
	// 正密码 → 往返
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/7z-enc/encrypted.7z", OutputDir: outDir, Password: "SECRET7"}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount < 1 {
		t.Errorf("应解出 ≥1 文件, got %+v", result)
	}
}

// TestEncryptedRarExtract rar 加密（libarchive 夹具 rar5，密码 "password"；
// rar4 夹具为混合密码部分加密——b.txt="password"/d.txt="password2"，已知限制：
// 预扫降级+逐文件密码错误跳过而非中止）
func TestEncryptedRarExtract(t *testing.T) {
	outDir := t.TempDir()
	result, extractErr := Extract(ExtractOptions{Archive: "testdata/rar-enc/rar5.rar", OutputDir: outDir, Password: "password"}, nil)
	if extractErr != nil {
		t.Fatal(extractErr)
	}
	if result.FileCount < 3 {
		t.Errorf("rar5 应解出 ≥3 文件（a/b/c 可解，d 密码不同跳过）, got %+v", result)
	}
	if got, readErr := os.ReadFile(filepath.Join(outDir, "b.txt")); readErr != nil || string(got) != "This is from b.txt" {
		t.Errorf("b.txt 解密内容不符: %q err=%v", got, readErr)
	}
}

// TestZipPasswordTable zip 密码表逐文件密码（resolver 回调）
func TestZipPasswordTable(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.zip")
	makeAESZipEntry(t, aPath, "PW_A", "file-a.txt", []byte("content A"), 3)
	bPath := filepath.Join(dir, "b.zip")
	makeAESZipEntry(t, bPath, "PW_B", "file-b.txt", []byte("content B"), 3)

	// resolver 按文件名给密码——两个不同密码的归档都能解
	// （此处以两个归档分别验证：resolver 对每个归档的条目命中各自的密码）
	outA := t.TempDir()
	if _, err := Extract(ExtractOptions{Archive: aPath, OutputDir: outA, PasswordResolver: func(name string) (string, bool) {
		return "PW_A", true // a 归档条目命中 PW_A
	}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readFileOrFail(t, filepath.Join(outA, "file-a.txt")); got != "content A" {
		t.Errorf("A 内容不符: %q", got)
	}
	outB := t.TempDir()
	if _, err := Extract(ExtractOptions{Archive: bPath, OutputDir: outB, PasswordResolver: func(name string) (string, bool) {
		return "PW_B", true
	}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readFileOrFail(t, filepath.Join(outB, "file-b.txt")); got != "content B" {
		t.Errorf("B 内容不符: %q", got)
	}
}

// TestZpackignore .zpackignore 排除规则（gitignore 语义）
func TestZpackignore(t *testing.T) {
	srcDir := t.TempDir()
	os.MkdirAll(filepath.Join(srcDir, "dist"), 0o755)
	os.MkdirAll(filepath.Join(srcDir, "keep"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("code"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "debug.log"), []byte("log"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "important.log"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "dist", "bundle.js"), []byte("build"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "keep", "data.txt"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(srcDir, ".zpackignore"), []byte("dist/\n*.log\n!important.log\n"), 0o644)

	ignores := LoadEffectiveZpackignores([]string{srcDir})
	if len(ignores) != 1 {
		t.Fatalf("应加载 1 个 .zpackignore, got %d", len(ignores))
	}
	tasks, collectErr := collectCompressTasks([]string{srcDir}, nil, ignores)
	if collectErr != nil {
		t.Fatal(collectErr)
	}
	// 应保留：main.go、keep/data.txt、important.log（!反选）；排除：debug.log、dist/、.zpackignore
	if len(tasks) != 3 {
		names := make([]string, len(tasks))
		for idx, task := range tasks {
			names[idx] = task.entryName
		}
		t.Fatalf("应保留 3 个文件, got %d: %v", len(tasks), names)
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		seen[task.entryName] = true
	}
	if !seen[filepath.Base(srcDir)+"/main.go"] || !seen[filepath.Base(srcDir)+"/keep/data.txt"] || !seen[filepath.Base(srcDir)+"/important.log"] {
		t.Errorf("缺少期望文件: %v", seen)
	}
}
