package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestTaskFinishClosesUpdates 收尾必须关闭 Updates——watchTask 靠关闭退出，
// 不关则失败提示与完成后策略永不执行（2026-11-23 审计）。
func TestTaskFinishClosesUpdates(t *testing.T) {
	task := newTask("t", "压缩", "/tmp", "/tmp/t.zip")
	task.finish(TaskDone, "完成")
	view := task.View()
	if view.State != TaskDone || view.Result != "完成" || view.Percent != 100 {
		t.Fatalf("收尾后状态不对: %+v", view)
	}
	for {
		if _, open := <-task.Updates; !open {
			break // 已关闭：watchTask 的循环到此退出
		}
	}
	task.finish(TaskFailed, "再收一次") // 幂等：不 panic、不改状态
	task.notify()                   // 收尾后发送不得 panic（send on closed channel）
	if state := task.View().State; state != TaskDone {
		t.Fatalf("二次收尾改了状态: %d", state)
	}
}

// TestTaskCancel 取消：落取消态、杀进程、清半成品各恰好一次；之后的收尾保留取消态
func TestTaskCancel(t *testing.T) {
	task := newTask("t", "压缩", "/tmp", "/tmp/t.zip")
	kills, cleanups := 0, 0
	task.setKill(func() { kills++ })
	task.setCleanup(func() { cleanups++ })
	task.Cancel()
	task.Cancel() // 幂等
	if kills != 1 || cleanups != 1 {
		t.Fatalf("取消应各执行一次，得到 kill=%d cleanup=%d", kills, cleanups)
	}
	if view := task.View(); view.State != TaskCancelled || view.Result != "已取消" {
		t.Fatalf("取消后状态不对: %+v", view)
	}
	task.finish(TaskDone, "引擎其实跑完了") // 取消与进程结束竞争：收尾不得覆盖取消态
	if state := task.View().State; state != TaskCancelled {
		t.Fatalf("收尾覆盖了取消态: %d", state)
	}
	for {
		if _, open := <-task.Updates; !open {
			break
		}
	}
}

// TestTaskCancelAfterFinish 任务已结束再点取消：什么都不动
func TestTaskCancelAfterFinish(t *testing.T) {
	task := newTask("t", "解压", "/tmp", "/tmp")
	kills := 0
	task.setKill(func() { kills++ })
	task.finish(TaskDone, "完成")
	task.Cancel()
	if kills != 0 || task.View().State != TaskDone {
		t.Fatalf("已结束的任务被取消动了: kill=%d state=%d", kills, task.View().State)
	}
}

// TestCountRunning 只数运行中的任务
func TestCountRunning(t *testing.T) {
	running := newTask("a", "压缩", "", "")
	done := newTask("b", "压缩", "", "")
	done.finish(TaskDone, "")
	cancelled := newTask("c", "压缩", "", "")
	cancelled.Cancel()
	if got := countRunning([]*Task{running, done, cancelled}); got != 1 {
		t.Fatalf("期望 1 个运行中，得到 %d", got)
	}
}

// TestRemoveArchiveAndVolumes 取消压缩的半成品清理：归档本体与分卷都删，相邻的其它文件不动
func TestRemoveArchiveAndVolumes(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "out.zip")
	for _, name := range []string{"out.zip", "out.zip.001", "out.zip.002", "out.zip.bak", "out 2.zip.001"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeArchiveAndVolumes(archive)
	for _, name := range []string{"out.zip", "out.zip.001", "out.zip.002"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s 应被删除", name)
		}
	}
	for _, name := range []string{"out.zip.bak", "out 2.zip.001"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s 不应被删除", name)
		}
	}
}

// TestConvertSummary 转换摘要的体积：size_bytes / output_size_bytes（图片族）/ 都没有时 Stat 产物
func TestConvertSummary(t *testing.T) {
	dir := t.TempDir()
	product := filepath.Join(dir, "产物.webp")
	if err := os.WriteFile(product, make([]byte, 20642), 0o644); err != nil {
		t.Fatal(err)
	}
	// Windows 路径的反斜杠在 JSON 串里须转义——直接拼进去是非法转义，整段信封解析失败，
	// 两例都会回落成动作名（2026-11-24 Windows 实测机首跑暴露；mac 路径无反斜杠故无症状）
	productJSON := strings.ReplaceAll(product, `\`, `\\`)
	cases := []struct {
		name     string
		raw      string
		expected string
	}{
		{"size_bytes", `{"is_success":true,"data":{"output":"/x/a.mkv","size_bytes":1024}}`, "已输出 a.mkv · 1KB"},
		{"图片族 output_size_bytes", `{"is_success":true,"data":{"output":"` + productJSON + `","output_size_bytes":20642}}`, "已输出 产物.webp · 20.2KB"},
		{"都没有则 Stat 产物", `{"is_success":true,"data":{"output":"` + productJSON + `"}}`, "已输出 产物.webp · 20.2KB"},
		{"失败信封回落动作名", `{"is_success":false,"error_info":"x"}`, "刻录完成"},
		{"非 JSON 回落动作名", `plain`, "刻录完成"},
	}
	for _, testCase := range cases {
		if got := convertSummary([]byte(testCase.raw), "刻录"); got != testCase.expected {
			t.Errorf("%s: 期望 %q，得到 %q", testCase.name, testCase.expected, got)
		}
	}
}

// TestExtractSummary 解压摘要：全部已存在被跳过时如实说明，不报「解压完成」
func TestExtractSummary(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		expected string
	}{
		{"正常", `{"data":{"file_count":9,"size_bytes":260000,"skipped":1}}`, "9 个文件 · 253.9KB · 跳过 1"},
		{"全部跳过", `{"data":{"file_count":0,"size_bytes":0,"skipped":4}}`, "没有新文件（已存在的 4 项已跳过）"},
		{"空信封", `{"data":{}}`, "解压完成"},
		{"非 JSON", `x`, "解压完成"},
	}
	for _, testCase := range cases {
		if got := extractSummary([]byte(testCase.raw)); got != testCase.expected {
			t.Errorf("%s: 期望 %q，得到 %q", testCase.name, testCase.expected, got)
		}
	}
}

// TestHumanSize 体积写法：整数位不带小数、数字与单位之间不留空格（2026-11-23 用户定案）。
func TestHumanSize(t *testing.T) {
	const KB = 1024
	cases := []struct {
		bytes    int64
		expected string
	}{
		{0, "0B"},
		{500, "500B"},
		{1023, "1023B"},
		{KB, "1KB"},
		{1536, "1.5KB"},
		{5 * KB * KB, "5MB"},
		{2411725, "2.3MB"}, // ≈2.3MB
		{3 * KB * KB * KB, "3GB"},
		// 取整后进位：1023.96KB → 1024.0KB → 1MB
		{1048535, "1MB"}, // ≈1023.96KB
		{1024 * KB * KB * KB, "1TB"},
	}
	for _, testCase := range cases {
		if got := humanSize(testCase.bytes); got != testCase.expected {
			t.Errorf("humanSize(%d)：期望 %s，得到 %s", testCase.bytes, testCase.expected, got)
		}
	}
}

// waitTask 等任务收尾（Updates 关闭），超时则失败。
func waitTask(t *testing.T, task *Task) TaskView {
	t.Helper()
	deadline := time.After(60 * time.Second)
	for {
		select {
		case _, open := <-task.Updates:
			if !open {
				return task.View()
			}
		case <-deadline:
			t.Fatalf("任务 %q 60 秒未收尾", task.Title)
		}
	}
}

// repoEngine 是仓库产线的完整版引擎；没有就跳过依赖它的用例。
// Windows 产物名带 .exe——此前只认裸名 bin/zpack，Windows 上三项引擎用例恒跳过（2026-11-24 补）
func repoEngine() string {
	candidates := []string{filepath.Join("..", "..", "bin", "zpack")}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, filepath.Join("..", "..", "bin", "zpack.exe"))
	}
	for _, candidate := range candidates {
		if isExecutable(candidate) {
			return candidate
		}
	}
	return ""
}

// TestTaskLifecycleWithEngine 用真实引擎走一遍：成功任务收尾后通道关闭、产物路径以信封为准。
func TestTaskLifecycleWithEngine(t *testing.T) {
	engine := repoEngine()
	if engine == "" {
		t.Skip("仓库 bin/zpack 不存在，跳过")
	}
	service := &Service{binary: engine}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello zpack"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := service.StartCompress([]string{src}, "zip", 1, "", "", "", t.TempDir())
	if task == nil {
		t.Fatal("引擎在位却返回 nil")
	}
	view := waitTask(t, task)
	if view.State != TaskDone {
		t.Fatalf("期望成功，得到 %+v", view)
	}
	if _, err := os.Stat(view.Product); err != nil {
		t.Fatalf("产物 %q 不存在: %v", view.Product, err)
	}
	if !strings.Contains(view.Result, "1 个文件") {
		t.Fatalf("摘要不对: %q", view.Result)
	}
}

// TestTaskCancelWithEngine 用真实引擎验证取消：进程被杀、半成品清掉、状态是取消而不是失败。
func TestTaskCancelWithEngine(t *testing.T) {
	engine := repoEngine()
	if engine == "" {
		t.Skip("仓库 bin/zpack 不存在，跳过")
	}
	service := &Service{binary: engine}
	src := t.TempDir()
	// 不可压的随机内容：给取消留出时间窗
	big := make([]byte, 96<<20)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	task := service.StartCompress([]string{src}, "zip", 9, "", "", "cancel-me", outDir)
	if task == nil {
		t.Fatal("引擎在位却返回 nil")
	}
	time.Sleep(150 * time.Millisecond)
	task.Cancel()
	view := waitTask(t, task)
	if view.State != TaskCancelled {
		t.Fatalf("期望取消态，得到 %+v", view)
	}
	leftovers, _ := filepath.Glob(filepath.Join(outDir, "*"))
	if len(leftovers) != 0 {
		t.Fatalf("取消后应清掉半成品，残留: %v", leftovers)
	}
}

// TestSamePaths 清单快照比对：顺序或内容不同即视为已变
func TestSamePaths(t *testing.T) {
	cases := []struct {
		name     string
		first    []string
		second   []string
		expected bool
	}{
		{"相同", []string{"a", "b"}, []string{"a", "b"}, true},
		{"nil 对空", nil, []string{}, true},
		{"长度不同", []string{"a"}, []string{"a", "b"}, false},
		{"顺序不同", []string{"a", "b"}, []string{"b", "a"}, false},
	}
	for _, testCase := range cases {
		if got := samePaths(testCase.first, testCase.second); got != testCase.expected {
			t.Errorf("%s: 期望 %v，得到 %v", testCase.name, testCase.expected, got)
		}
	}
}

// TestRunToolStdinHyphen 以「-」开头的文本走 stdin 原样送达引擎（此前经 argv 会被当成旗标跳过）
func TestRunToolStdinHyphen(t *testing.T) {
	engine := repoEngine()
	if engine == "" {
		t.Skip("仓库 bin/zpack 不存在，跳过")
	}
	service := &Service{binary: engine}
	done := make(chan string, 1)
	failure := make(chan string, 1)
	service.RunToolStdin([]string{"base64", "encode"}, "- 事项", func(text, errMsg string) {
		if errMsg != "" {
			failure <- errMsg
			return
		}
		done <- text
	})
	select {
	case text := <-done:
		if text != "LSDkuovpobk=" { // printf -- '- 事项' | base64
			t.Fatalf("期望 LSDkuovpobk=，得到 %q", text)
		}
	case errMsg := <-failure:
		t.Fatalf("引擎报错: %s", errMsg)
	case <-time.After(30 * time.Second):
		t.Fatal("30 秒未返回")
	}
}
