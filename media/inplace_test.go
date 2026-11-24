package media

// 就地改写安全出口的单测（纯 Go，cgo 与非 cgo 构建都跑）。
// 背景：输出与输入是同一个文件时，引擎边读边写会先把源文件截断——2026-11-24 实测 10MB 的源只剩 1297 字节且报成功。

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFileForTest(t *testing.T, path, content string) {
	t.Helper()
	if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(content)
}

// TestSameFilePath 同文件判定：路径比、文件身份、Windows 不分大小写、输出不存在
func TestSameFilePath(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "clip.mp4")
	other := filepath.Join(dir, "other.mp4")
	writeFileForTest(t, source, "source")
	writeFileForTest(t, other, "other")

	cases := []struct {
		name     string
		input    string
		output   string
		expected bool
	}{
		{"同一个路径", source, source, true},
		{"不同文件", source, other, false},
		{"输出还不存在：按路径比", source, filepath.Join(dir, "new.mp4"), false},
		{"输入不存在不算同文件", filepath.Join(dir, "missing.mp4"), source, false},
	}
	if runtime.GOOS == "windows" {
		// Windows 文件名不分大小写：CLIP.MP4 与 clip.mp4 是同一个文件
		upperCased := strings.ToUpper(filepath.Base(source))
		cases = append(cases, struct {
			name     string
			input    string
			output   string
			expected bool
		}{"Windows 不分大小写", source, filepath.Join(dir, upperCased), true})
	}
	for _, testCase := range cases {
		if got := SameFilePath(testCase.input, testCase.output); got != testCase.expected {
			t.Errorf("%s：期望 %v，得到 %v", testCase.name, testCase.expected, got)
		}
	}
}

// TestStageInPlaceOutputDifferentFile 不是同一个文件：原样返回，收尾只透传错误、不碰任何文件
func TestStageInPlaceOutputDifferentFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "clip.mp4")
	target := filepath.Join(dir, "out.mp4")
	writeFileForTest(t, source, "source")

	stagedOutput, finishInPlace := stageInPlaceOutput(source, target)
	if stagedOutput != target {
		t.Fatalf("不同文件不应改写输出路径，得到 %q", stagedOutput)
	}
	sampleErr := errors.New("执行失败")
	if got := finishInPlace(sampleErr); !errors.Is(got, sampleErr) {
		t.Fatalf("不同文件应收尾应透传执行错误，得到 %v", got)
	}
	if got := finishInPlace(nil); got != nil {
		t.Fatalf("不同文件成功收尾应返回 nil，得到 %v", got)
	}
	if readFileForTest(t, source) != "source" {
		t.Fatal("不同文件时源文件不该被动过")
	}
}

// TestStageInPlaceOutputSameFile 同一个文件：改写临时文件（保留扩展名）；失败时原文件不动；成功时原子替换
func TestStageInPlaceOutputSameFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "clip.mp4")
	writeFileForTest(t, source, "original")

	// 失败路径：临时文件写了也要删，原文件不动
	stagedOutput, finishInPlace := stageInPlaceOutput(source, source)
	expectedTemp := filepath.Join(dir, "clip"+INPLACE_MARK+".mp4")
	if stagedOutput != expectedTemp {
		t.Fatalf("就地改写应输出到 %q（保留原扩展名），得到 %q", expectedTemp, stagedOutput)
	}
	writeFileForTest(t, stagedOutput, "half-written")
	sampleErr := errors.New("执行失败")
	if got := finishInPlace(sampleErr); !errors.Is(got, sampleErr) {
		t.Fatalf("失败收尾应透传执行错误，得到 %v", got)
	}
	if readFileForTest(t, source) != "original" {
		t.Fatal("失败后原文件必须原封不动")
	}
	if _, statErr := os.Stat(stagedOutput); !os.IsNotExist(statErr) {
		t.Fatal("失败后临时文件必须被清理")
	}

	// 成功路径：临时文件替换原文件
	stagedOutput, finishInPlace = stageInPlaceOutput(source, source)
	writeFileForTest(t, stagedOutput, "compressed")
	if got := finishInPlace(nil); got != nil {
		t.Fatalf("成功收尾应返回 nil，得到 %v", got)
	}
	if readFileForTest(t, source) != "compressed" {
		t.Fatal("成功后临时文件应替换原文件")
	}
	if _, statErr := os.Stat(stagedOutput); !os.IsNotExist(statErr) {
		t.Fatal("替换后临时文件不应残留")
	}
}

// TestRejectInPlace 剪辑类命令的同文件拒绝：命中任一输入即拒绝，文案带做法
func TestRejectInPlace(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.mp4")
	second := filepath.Join(dir, "b.mp4")
	writeFileForTest(t, first, "a")
	writeFileForTest(t, second, "b")

	if got := rejectInPlace([]string{first, second}, filepath.Join(dir, "out.mp4")); got != nil {
		t.Fatalf("输出与输入不同不应拒绝，得到 %v", got)
	}
	rejectErr := rejectInPlace([]string{first, second}, second)
	if rejectErr == nil {
		t.Fatal("输出与其中一个输入相同应拒绝")
	}
	if !strings.Contains(rejectErr.Error(), "输出不能与输入是同一个文件") || !strings.Contains(rejectErr.Error(), inPlaceHint) {
		t.Fatalf("拒绝文案应点名原因与做法，得到 %v", rejectErr)
	}
}
