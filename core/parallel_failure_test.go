package core

// 并行解出「多个条目同时失败」回归：取消通道必须只关闭一次。
//
// 修复前（2026-11-24 实测）：worker 失败时各自 close(cancelChannel)，多个 worker 同时失败就
// panic: close of closed channel——进程以 exit 2 结束、stdout 为空，违反输出契约（应为 error JSON + exit 1）。
// zip 侧 16 条目加密包 + 错密码 + 16 个 worker 必崩；容器侧（7z 等）11 个条目落盘位置被占时 6 次崩 4 次。

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PARALLEL_FAILURE_ENTRY_COUNT 并发失败回归用的条目数（要明显多于默认 worker 数才能保证同时失败）
const PARALLEL_FAILURE_ENTRY_COUNT = 16

// TestZipExtractConcurrentWrongPassword 加密 zip 密码错误：每个条目都解不开，全部 worker 同时失败
func TestZipExtractConcurrentWrongPassword(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if mkdirErr := os.MkdirAll(srcDir, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	for entryIdx := 0; entryIdx < PARALLEL_FAILURE_ENTRY_COUNT; entryIdx++ {
		payload := []byte(fmt.Sprintf("并发失败回归载荷 %d\n", entryIdx))
		if writeErr := os.WriteFile(filepath.Join(srcDir, fmt.Sprintf("f%02d.txt", entryIdx)), payload, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	archivePath := filepath.Join(dir, "enc.zip")
	if _, compressErr := Compress(CompressOptions{Inputs: []string{srcDir}, Output: archivePath, Password: "right"}, nil); compressErr != nil {
		t.Fatal(compressErr)
	}
	// 多跑几轮：竞态类缺陷一轮撞不上不代表没问题
	for roundIdx := 0; roundIdx < 5; roundIdx++ {
		outputDir := filepath.Join(dir, fmt.Sprintf("out-%d", roundIdx))
		_, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: outputDir, Password: "wrong", Jobs: PARALLEL_FAILURE_ENTRY_COUNT}, nil)
		if extractErr == nil || !strings.Contains(extractErr.Error(), "密码错误") {
			t.Fatalf("第 %d 轮：错密码应返回「密码错误」，got: %v", roundIdx, extractErr)
		}
	}
}

// TestParallelContainerExtractConcurrentFailures 容器并行骨架（7z）：多个条目的落盘位置被同名目录占住，同时写入失败
func TestParallelContainerExtractConcurrentFailures(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join("testdata", "7z", "many.7z")
	if _, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: dir, Jobs: 8}, nil); extractErr != nil {
		t.Fatal(extractErr)
	}
	// 把前 PARALLEL_FAILURE_ENTRY_COUNT - 5 个文件换成同名目录：写入必然失败
	blockedCount := 0
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || blockedCount >= PARALLEL_FAILURE_ENTRY_COUNT-5 {
			return nil
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return removeErr
		}
		if mkdirErr := os.Mkdir(path, 0o755); mkdirErr != nil {
			return mkdirErr
		}
		blockedCount++
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if blockedCount == 0 {
		t.Fatal("没有可占位的文件，夹具条目数不足")
	}
	for roundIdx := 0; roundIdx < 5; roundIdx++ {
		_, extractErr := Extract(ExtractOptions{Archive: archivePath, OutputDir: dir, Force: true, Jobs: 8}, nil)
		if extractErr == nil || !strings.Contains(extractErr.Error(), "解压失败") {
			t.Fatalf("第 %d 轮：落盘位置被占应返回「解压失败」，got: %v", roundIdx, extractErr)
		}
	}
}
