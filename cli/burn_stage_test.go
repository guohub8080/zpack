package cli

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/guohub8080/zpack/core"
)

// TestBurnStageInputsISO 多输入聚台 → 临时 ISO → 引擎解回交叉验证（无刻录机也能
// 验证多输入语义的正确性；darwin 走 makehybrid，linux 需 xorriso 才跑）。
//
// 注：自家 ISO 读取层暂按 ISO9660 基础层读名（大写、无 CJK——Joliet 层在盘上且
// Windows 可见，但 core 的 Joliet 支持未做，AGENTS 已录局限）。故交叉验证用
// ASCII 文件名 + 大写期望。
func TestBurnStageInputsISO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows M2")
	}
	if runtime.GOOS == "linux" {
		if _, err := osexec.LookPath("xorriso"); err != nil {
			t.Skip("无 xorriso")
		}
	}

	source := t.TempDir()
	nested := filepath.Join(source, "subdir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		filepath.Join(source, "alpha.txt"): "甲文件内容",
		filepath.Join(source, "beta.log"):  "BBBB-乙",
		filepath.Join(nested, "gamma.md"):  "嵌套丙",
		filepath.Join(source, "bare"):      "bare",
	}
	for path, content := range fixtures {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	inputs := []string{
		filepath.Join(source, "alpha.txt"),
		filepath.Join(source, "beta.log"),
		nested,
		filepath.Join(source, "bare"),
	}
	imagePath, cleanup := burnStageInputs(inputs, burnFlags{volid: "MULTITEST"})
	defer cleanup()
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("临时镜像未生成: %v", err)
	}

	unpackDir := t.TempDir()
	result, err := core.Extract(core.ExtractOptions{Archive: imagePath, OutputDir: unpackDir}, nil)
	if err != nil {
		t.Fatalf("解回失败: %v", err)
	}
	if result.FileCount != 4 {
		t.Fatalf("文件数不符: %d（%+v）", result.FileCount, result)
	}
	expect := map[string]string{
		"ALPHA.TXT": "甲文件内容",
		"BETA.LOG":  "BBBB-乙",
		"BARE":      "bare",
	}
	for name, want := range expect {
		got, err := os.ReadFile(filepath.Join(unpackDir, name))
		if err != nil {
			t.Fatalf("缺少文件 %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("内容不符 %s: %q ≠ %q", name, got, want)
		}
	}
	got, err := os.ReadFile(filepath.Join(unpackDir, "SUBDIR", "GAMMA.MD"))
	if err != nil {
		t.Fatalf("缺少嵌套文件 GAMMA.MD: %v", err)
	}
	if string(got) != "嵌套丙" {
		t.Fatalf("嵌套文件内容不符: %q", got)
	}
}
