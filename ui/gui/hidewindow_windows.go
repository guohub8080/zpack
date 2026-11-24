//go:build windows

// 根治「闪黑窗」（2026-11-24 用户定案：不要隐藏，要根治）：GUI 是 windowsgui 子系统，
// 它起控制台子进程（zpack 引擎）时 Windows 默认给子进程分配新控制台——黑窗一闪。
// HideWindow=true 是「先创建再隐藏」（仍有一瞬闪现）；根治 = CREATE_NO_WINDOW 创建
// 标志——子进程从创建起就**没有**控制台，窗口从未存在。stdout 管道（JSON 契约）不受影响。
package main

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW Windows 进程创建标志：不为子进程分配控制台
const CREATE_NO_WINDOW = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: CREATE_NO_WINDOW}
}
