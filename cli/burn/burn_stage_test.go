package burn

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/guohub8080/zpack/core"
)

// TestBurnStageInputsISO 多输入聚台 → 临时 ISO → 引擎解回交叉验证（无刻录机也能
// 验证多输入语义的正确性；只有 darwin 走这条路）。
//
// 注：自家 ISO 读取层暂按 ISO9660 基础层读名（大写、无 CJK——Joliet 层在盘上且
// Windows 可见，但 core 的 Joliet 支持未做，AGENTS 已录局限）。故交叉验证用
// ASCII 文件名 + 大写期望。
func TestBurnStageInputsISO(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("只有 macOS 还走临时 ISO；Linux 走 xorriso 原生增量（命令行拼装见 TestBurnLinuxTreeArguments），Windows 走系统镜像库")
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
	imagePath, cleanup, err := burnStageInputs(inputs, burnFlags{volid: "MULTITEST"}, burnStageISO)
	if err != nil {
		t.Fatalf("聚台失败: %v", err)
	}
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

// TestBurnStageInputsMountsAndCleansUp 多输入聚台成功路径：重名输入加序号、目录按目录名为根
// 整树挂入、清理函数同时回收镜像与舞台且不伤源文件。镜像生成器注入假产物——无 hdiutil/xorriso
// 的平台（windows）也跑。
func TestBurnStageInputsMountsAndCleansUp(t *testing.T) {
	source := t.TempDir()
	nested := filepath.Join(source, "nested")
	fixtures := []string{
		filepath.Join(source, "same.txt"),
		filepath.Join(nested, "same.txt"),
		filepath.Join(nested, "gamma.txt"),
	}
	for _, path := range fixtures {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inputs := []string{fixtures[0], fixtures[1], nested}

	var stageDir string
	staged := map[string]string{}
	buildISO := func(mountedDir string, _ burnFlags) (string, func(), error) {
		stageDir = mountedDir
		collectErr := filepath.WalkDir(mountedDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relPath, relErr := filepath.Rel(mountedDir, path)
			if relErr != nil {
				return relErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				// linux 目录输入=符号链接（mkisofs -f 跟随）——记链接目标
				linkTarget, _ := os.Readlink(path)
				staged[filepath.ToSlash(relPath)] = "->" + linkTarget
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			staged[filepath.ToSlash(relPath)] = string(content)
			return nil
		})
		if collectErr != nil {
			t.Fatalf("遍历舞台失败: %v", collectErr)
		}
		fakeISO := filepath.Join(t.TempDir(), "staged.iso")
		if err := os.WriteFile(fakeISO, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
		return fakeISO, func() { _ = os.Remove(fakeISO) }, nil
	}

	imagePath, cleanup, err := burnStageInputs(inputs, burnFlags{volid: "STAGE"}, buildISO)
	if err != nil {
		t.Fatalf("聚台失败: %v", err)
	}
	want := map[string]string{
		"same.txt":   fixtures[0],
		"same-2.txt": fixtures[1],
	}
	if runtime.GOOS == "linux" {
		want["nested"] = "->" + nested
	} else {
		want["nested/same.txt"] = fixtures[1]
		want["nested/gamma.txt"] = fixtures[2]
	}
	for name, content := range want {
		if staged[name] != content {
			t.Errorf("舞台条目 %s 不符: %q ≠ %q（实得 %v）", name, staged[name], content, staged)
		}
	}
	if len(staged) != len(want) {
		t.Errorf("舞台条目数不符: %v（期望 %v）", staged, want)
	}
	if filepath.Dir(stageDir) != filepath.Clean(source) {
		t.Errorf("舞台应建在输入们的公共祖先目录: %s ≠ %s", stageDir, source)
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("临时镜像未生成: %v", err)
	}
	cleanup()
	for _, gone := range []string{imagePath, stageDir} {
		if _, statErr := os.Stat(gone); !os.IsNotExist(statErr) {
			t.Errorf("清理后仍存在: %s（%v）", gone, statErr)
		}
	}
	// 硬链接只增引用计数——删舞台不得伤及源文件
	for _, path := range fixtures {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("清理误伤源文件: %s（%v）", path, statErr)
		}
	}
}

// TestBurnStageInputsCleansUpOnISOFailure 镜像生成失败时舞台目录必须清干净——回归：失败分支曾
// print 即 exit，舞台目录连同硬链接一起遗留在用户目录（2026-11-23 Windows 实测）。
// 镜像生成器注入强制失败，三平台同跑。
func TestBurnStageInputsCleansUpOnISOFailure(t *testing.T) {
	source := t.TempDir()
	inputs := []string{filepath.Join(source, "alpha.txt"), filepath.Join(source, "beta.txt")}
	for _, path := range inputs {
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantErr := errors.New("强制失败")
	buildISO := func(mountedDir string, _ burnFlags) (string, func(), error) {
		// 失败发生在舞台挂满之后——确认本测试没被「根本没暂存」架空
		mounted, readErr := os.ReadDir(mountedDir)
		if readErr != nil || len(mounted) != len(inputs) {
			t.Errorf("镜像生成时舞台应已挂入 %d 项: %v（%v）", len(inputs), readErr, mounted)
		}
		return "", func() {}, wantErr
	}
	imagePath, cleanup, err := burnStageInputs(inputs, burnFlags{volid: "CLEANUP"}, buildISO)
	if !errors.Is(err, wantErr) {
		t.Fatalf("应原样上抛镜像生成错误，得到: %v", err)
	}
	if imagePath != "" {
		t.Fatalf("失败时不应返回镜像路径: %q", imagePath)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".zpack-burn-stage-") {
			t.Fatalf("舞台目录泄漏: %s", entry.Name())
		}
	}
	cleanup() // 失败分支返回的清理函数应为空操作
}

// TestBurnUnsupportedPlatform 无刻录后端平台的拒绝原因：文案点名平台（输出契约铁律），三平台放行
func TestBurnUnsupportedPlatform(t *testing.T) {
	unsupported := burnUnsupportedPlatform()
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		if unsupported != nil {
			t.Fatalf("受支持平台不应拒绝: %v", unsupported)
		}
	default:
		if unsupported == nil || !strings.HasPrefix(unsupported.Error(), "暂不支持 "+runtime.GOOS+"：") {
			t.Fatalf("应点名平台拒绝，得到: %v", unsupported)
		}
	}
}

// TestBurnUniqueEntryNames 多输入的顶层条目名：重名加序号、与输入顺序一一对应（表驱动）
func TestBurnUniqueEntryNames(t *testing.T) {
	join := func(elements ...string) string { return filepath.Join(elements...) }
	cases := []struct {
		name   string
		inputs []string
		want   []string
	}{
		{"无重名原样", []string{join("x", "a.txt"), join("y", "b.txt")}, []string{"a.txt", "b.txt"}},
		{"重名文件依次加序号", []string{join("x", "a.txt"), join("y", "a.txt"), join("z", "a.txt")}, []string{"a.txt", "a-2.txt", "a-3.txt"}},
		{"目录按目录名", []string{join("x", "资料"), join("x", "b.txt")}, []string{"资料", "b.txt"}},
		{"带尾分隔符的目录", []string{join("x", "资料") + string(filepath.Separator), join("y", "资料")}, []string{"资料", "资料-2"}},
		{"无扩展名的重名文件", []string{join("x", "bare"), join("y", "bare")}, []string{"bare", "bare-2"}},
	}
	for _, testCase := range cases {
		got := burnUniqueEntryNames(testCase.inputs)
		if strings.Join(got, "|") != strings.Join(testCase.want, "|") {
			t.Errorf("%s: %v ≠ %v", testCase.name, got, testCase.want)
		}
	}
}

// TestBurnFormatBytes 人读容量：整数位不带小数、否则一位小数、数字与单位之间不留空格（表驱动）
func TestBurnFormatBytes(t *testing.T) {
	const kilo, mega, giga = int64(1024), int64(1024 * 1024), int64(1024 * 1024 * 1024)
	cases := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"零", 0, "0B"},
		{"不足 1KB", 512, "512B"},
		{"整 KB", kilo, "1KB"},
		{"一位小数", kilo + kilo/2, "1.5KB"},
		{"整数位不带小数", 5 * mega, "5MB"},
		{"一位小数（MB）", 2*mega + 3*mega/10, "2.3MB"},
		{"整数 GB", 3 * giga, "3GB"},
		{"取整后进位到下一档", 1023*kilo + 980, "1MB"},
		{"空白 DVD-R 剩余空间（真机 2297888 扇区）", 2297888 * 2048, "4.4GB"},
	}
	for _, testCase := range cases {
		if got := burnFormatBytes(testCase.bytes); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}
