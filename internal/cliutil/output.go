package cliutil

import "os"

// OutputGuard 失败后清理半成品的守卫：只删本次命令新建的输出文件。
//
// 动工前输出路径上就有的文件不动（用户是要覆盖它，不是要它在失败时消失——
// `image new 已有的图.png abc` 曾因尺寸非法把原有的图删掉）；输出与输入是同一个文件时
// 更不能动（就地压缩失败等于删源文件——`image compress 照片.png --target-size 100KB` 曾报错后删掉照片）。
// 2026-11-24 修复：此前各命令失败后无条件 os.Remove(输出路径)。
type OutputGuard struct {
	OutputPath    string
	existedBefore bool
}

// NewOutputGuard 在调用引擎之前创建：记下输出路径当时是否已存在
func NewOutputGuard(outputPath string) OutputGuard {
	_, statErr := os.Stat(outputPath)
	return OutputGuard{OutputPath: outputPath, existedBefore: statErr == nil}
}

// RemoveIfCreated 命令失败后调用：输出是本次新建的才删
func (guard OutputGuard) RemoveIfCreated() {
	if guard.existedBefore {
		return
	}
	os.Remove(guard.OutputPath)
}
