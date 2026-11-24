package media

// 失败后清理半成品的守卫单测：输出与输入是同一个文件时不得删除
// （2026-11-24 实测：media compress 遇到太小的源文件报错后，清理半成品把源文件删掉了）。

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveFailedOutputSkipsInput 输出即输入：不删；输出是另一个文件：照常清理
func TestRemoveFailedOutputSkipsInput(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "clip.mp4")
	if writeErr := os.WriteFile(source, []byte("source"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	removeFailedOutput([]string{source}, source)
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatalf("输出与输入是同一个文件时不得删除，源文件已消失: %v", statErr)
	}

	halfWritten := filepath.Join(dir, "out.mp4")
	if writeErr := os.WriteFile(halfWritten, []byte("half"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	removeFailedOutput([]string{source}, halfWritten)
	if _, statErr := os.Stat(halfWritten); !os.IsNotExist(statErr) {
		t.Fatal("另一个文件的半成品应被清理")
	}

	// 多输入（merge）：输出与其中任一个输入相同都不删
	removeFailedOutput([]string{filepath.Join(dir, "other.mp4"), source}, source)
	if _, statErr := os.Stat(source); statErr != nil {
		t.Fatalf("多输入时任一输入与输出相同都不得删除，源文件已消失: %v", statErr)
	}
}
