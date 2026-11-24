package cliutil

// CancelSequence 的单测：信号源与动作都是注入的假件，不碰真信号、不退出进程。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cancelProbe 记录三个动作被调用的先后
type cancelProbe struct {
	events  []string
	signals chan os.Signal
}

func (probe *cancelProbe) record(name string) func() {
	return func() { probe.events = append(probe.events, name) }
}

// waitEvents 等序列走到指定个数的事件（或超时）
func (probe *cancelProbe) waitEvents(t *testing.T, count int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if len(probe.events) >= count {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("等不到第 %d 个事件，已有 %v", count, probe.events)
		case <-time.After(time.Millisecond):
		}
	}
}

func newCancelProbe() *cancelProbe {
	return &cancelProbe{signals: make(chan os.Signal, 4)}
}

// TestCancelSequenceFirstSignalRequestsCancel 第一次信号立刻发出取消请求；引擎在这期间自己收尾退出，
// 序列不该再做任何事（宽限期被「进程已退出」自然打断——这里用不注入第二次信号来模拟）
func TestCancelSequenceFirstSignalRequestsCancel(t *testing.T) {
	probe := newCancelProbe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		CancelSequence(time.Hour, probe.signals, probe.record("request"), probe.record("force"), probe.record("immediate"))
	}()
	probe.signals <- os.Interrupt
	probe.waitEvents(t, 1)
	if len(probe.events) != 1 || probe.events[0] != "request" {
		t.Fatalf("第一次信号后应只发出取消请求，得到 %v", probe.events)
	}
	select {
	case <-done:
		t.Fatal("宽限期没到、也没有第二次信号，序列不应结束")
	default:
	}
}

// TestCancelSequenceSecondSignalExitsImmediately 宽限期内第二次信号：立即退出，不强制收尾
func TestCancelSequenceSecondSignalExitsImmediately(t *testing.T) {
	probe := newCancelProbe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		CancelSequence(time.Hour, probe.signals, probe.record("request"), probe.record("force"), probe.record("immediate"))
	}()
	probe.signals <- os.Interrupt
	probe.waitEvents(t, 1)
	probe.signals <- os.Interrupt
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("第二次信号后序列应立即结束")
	}
	if got := probe.events; len(got) != 2 || got[0] != "request" || got[1] != "immediate" {
		t.Fatalf("应为 [request immediate]，得到 %v", got)
	}
}

// TestCancelSequenceForceAfterGrace 宽限期到了还没退出：强制收尾；之后再来信号仍然立即退出
func TestCancelSequenceForceAfterGrace(t *testing.T) {
	probe := newCancelProbe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		CancelSequence(20*time.Millisecond, probe.signals, probe.record("request"), probe.record("force"), probe.record("immediate"))
	}()
	probe.signals <- os.Interrupt
	probe.waitEvents(t, 2)
	if probe.events[0] != "request" || probe.events[1] != "force" {
		t.Fatalf("应为 [request force]，得到 %v", probe.events)
	}
	select {
	case <-done:
		t.Fatal("强制收尾之后没有第二次信号，序列应继续等")
	default:
	}
	probe.signals <- os.Interrupt
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("强制收尾后的第二次信号应立即结束序列")
	}
	if got := probe.events; len(got) != 3 || got[2] != "immediate" {
		t.Fatalf("应为 [request force immediate]，得到 %v", got)
	}
}

// TestRemoveCreatedOutputs 强制取消时的清理：本次新建的输出删掉，动工前就有的不动；
// 同一路径登记多次只处理一次；清过一次之后名单清空（再调用不再删任何东西）
func TestRemoveCreatedOutputs(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.png")
	existing := filepath.Join(dir, "existing.png")
	if writeErr := os.WriteFile(existing, []byte("old"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	NewOutputGuard(fresh)
	NewOutputGuard(fresh) // 同一输出两处登记
	NewOutputGuard(existing)
	for _, path := range []string{fresh, existing} {
		if writeErr := os.WriteFile(path, []byte("data"), 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if leftover := RemoveCreatedOutputs(); len(leftover) != 0 {
		t.Fatalf("没有文件被占用，不应有删不掉的: %v", leftover)
	}
	if _, statErr := os.Stat(fresh); !os.IsNotExist(statErr) {
		t.Fatal("本次新建的输出应被清掉")
	}
	if content, readErr := os.ReadFile(existing); readErr != nil || string(content) != "data" {
		t.Fatalf("动工前就有的输出不应被动: %q %v", content, readErr)
	}
	// 名单已清空：再调一次不会去碰任何文件
	if writeErr := os.WriteFile(fresh, []byte("again"), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	if leftover := RemoveCreatedOutputs(); len(leftover) != 0 {
		t.Fatalf("名单清空后不应再有动作: %v", leftover)
	}
	if _, statErr := os.Stat(fresh); statErr != nil {
		t.Fatal("名单清空后再调用不应删除新写的文件")
	}
}
