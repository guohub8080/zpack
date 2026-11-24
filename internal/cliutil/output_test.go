package cliutil

// OutputGuard 的单测：失败后只删本次新建的输出，动工前就有的文件不动。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutputGuardRemoveIfCreated(t *testing.T) {
	dir := t.TempDir()

	// 本次新建的半成品：调用引擎之前输出还不存在 → 失败后删
	freshOutput := filepath.Join(dir, "fresh.png")
	freshGuard := NewOutputGuard(freshOutput)
	if writeErr := os.WriteFile(freshOutput, []byte("half-written"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	freshGuard.RemoveIfCreated()
	if _, statErr := os.Stat(freshOutput); !os.IsNotExist(statErr) {
		t.Fatal("本次新建的半成品应被清理")
	}

	// 动工前已存在（用户想覆盖的旧文件，或就是输入自己）→ 不动
	existingOutput := filepath.Join(dir, "existing.png")
	if writeErr := os.WriteFile(existingOutput, []byte("original"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	existingGuard := NewOutputGuard(existingOutput)
	existingGuard.RemoveIfCreated()
	content, readErr := os.ReadFile(existingOutput)
	if readErr != nil || string(content) != "original" {
		t.Fatalf("动工前就有的文件不得被删，读到 %q err=%v", content, readErr)
	}
}
