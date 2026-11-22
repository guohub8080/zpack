package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/guohub8080/zpack/internal/sevenzipcli"
)

// TestSevenZWriteArchive 7z 写入引擎（官方 C++ 进程内直调）：
// 目录树保名 + 散装多根 staging + 密码 header 加密 + 本引擎自读回环。
// 纯构建（无 CGO/无产线库）整体跳过——行为断言在 verify.sh 全量门禁覆盖。
func TestSevenZWriteArchive(t *testing.T) {
	if !sevenzipcli.Available() {
		t.Skip("7z 写入需完整版（CGO 构建 + 7z 静态产线）")
	}
	sourceDir := t.TempDir()
	if writeErr := os.MkdirAll(filepath.Join(sourceDir, "资料", "子目录"), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "a.txt"), []byte("hello 7z"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if writeErr := os.WriteFile(filepath.Join(sourceDir, "资料", "报 告.md"), []byte("中文内容"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	scatteredPath := filepath.Join(t.TempDir(), "one.txt")
	if writeErr := os.WriteFile(scatteredPath, []byte("scattered"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	testCases := []struct {
		name     string
		inputs   []string
		password string
	}{
		{"目录输入保树", []string{sourceDir}, ""},
		{"散装多根 staging", []string{filepath.Join(sourceDir, "a.txt"), scatteredPath}, ""},
		{"密码 header 加密", []string{sourceDir}, "test1234"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "out.7z")
			result, compressErr := Compress(CompressOptions{
				Inputs:   testCase.inputs,
				Output:   archivePath,
				Password: testCase.password,
			}, nil)
			if compressErr != nil {
				t.Fatalf("压缩失败: %s", compressErr)
			}
			if result.FileCount != 2 {
				t.Fatalf("file_count 期望 2 实得 %d", result.FileCount)
			}
			if result.SizeBytes <= 0 {
				t.Fatalf("size_bytes 异常: %d", result.SizeBytes)
			}
			extractDir := t.TempDir()
			if _, extractErr := Extract(ExtractOptions{
				Archive:   archivePath,
				OutputDir: extractDir,
				Password:  testCase.password,
			}, nil); extractErr != nil {
				t.Fatalf("自读回环失败: %s", extractErr)
			}
		})
	}
}

// TestSevenZWriteEmptyInput 空输入明确报错（zip 家族外的差异化边界）
func TestSevenZWriteEmptyInput(t *testing.T) {
	if !sevenzipcli.Available() {
		t.Skip("7z 写入需完整版（CGO 构建 + 7z 静态产线）")
	}
	emptyDir := t.TempDir()
	_, compressErr := Compress(CompressOptions{
		Inputs: []string{emptyDir},
		Output: filepath.Join(t.TempDir(), "empty.7z"),
	}, nil)
	if compressErr == nil {
		t.Fatal("空输入应报错")
	}
}
