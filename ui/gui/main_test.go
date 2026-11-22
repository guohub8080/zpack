package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 视图冒烟：无窗口渲染一帧（模板同款 Tester 机制）。
func TestViewRenders(t *testing.T) {
	application := &app{page: pageCompress, service: NewService()}
	tester := ui.NewTester(application.view, 960, 600)
	tester.Frame()
}
