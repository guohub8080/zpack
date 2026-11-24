//go:build !windows

// 非 Windows：子进程无控制台窗口概念，空实现。
package main

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
