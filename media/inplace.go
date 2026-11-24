// 就地改写的安全出口（纯 Go，cgo 与非 cgo 构建共用）。
//
// 引擎是边读输入边写输出的：输出与输入是同一个文件时，以写模式打开输出会先把源文件截断——
// 源文件当场损毁，命令却照样报成功。2026-11-24 实测：`media compress x.mp4` 的缺省输出就是输入自己，
// 10MB 的源文件只剩 1297 字节且报 success；`audio compress` 的「就地覆盖」同理；失败路径上命令面的
// 「清理半成品」还会把源文件直接删掉。
//
// 对策：主执行器（convert / compress / preset / bake / 几何变换）遇到同文件时改写旁边的临时文件，
// 成功后原子替换；剪辑类命令（各自直接打开输出）遇到同文件时明确拒绝。
package media

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// INPLACE_MARK 就地改写时临时文件名的标记段（<名>.zpack-inplace<原扩展名>——必须保留原扩展名，
// 引擎按输出扩展名决定容器格式）
const INPLACE_MARK = ".zpack-inplace"

// SameFilePath 两个路径是否指向同一个文件。
// 两个都已存在时按文件身份比（同一文件的符号链接 / 硬链接也算）；输出还不存在时按绝对路径比
// （截断源文件的风险发生在「以写模式打开输出」那一刻，那时输出就是输入）。
func SameFilePath(input, output string) bool {
	inputInfo, inputErr := os.Stat(input)
	if inputErr != nil {
		return false
	}
	if outputInfo, outputErr := os.Stat(output); outputErr == nil && os.SameFile(inputInfo, outputInfo) {
		return true
	}
	inputAbs, inputAbsErr := filepath.Abs(input)
	outputAbs, outputAbsErr := filepath.Abs(output)
	if inputAbsErr != nil || outputAbsErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(inputAbs, outputAbs)
	}
	return inputAbs == outputAbs
}

// stageInPlaceOutput 输出与输入是同一个文件时，把实际输出改到同目录的临时文件。
// 返回实际要写的路径和收尾函数：收尾(执行错误) → 失败删临时文件、原文件不动；成功则临时文件原子替换原文件。
// 不是同一个文件时原样返回，收尾函数只透传执行错误。
func stageInPlaceOutput(input, output string) (string, func(error) error) {
	if !SameFilePath(input, output) {
		return output, func(execErr error) error { return execErr }
	}
	tempOutput := strings.TrimSuffix(output, filepath.Ext(output)) + INPLACE_MARK + filepath.Ext(output)
	return tempOutput, func(execErr error) error {
		if execErr != nil {
			os.Remove(tempOutput)
			return execErr
		}
		if renameErr := os.Rename(tempOutput, output); renameErr != nil {
			os.Remove(tempOutput)
			return fmt.Errorf("就地替换 %s 失败（原文件未改动）: %w", output, renameErr)
		}
		return nil
	}
}

// rejectInPlace 输出与任一输入是同一个文件时拒绝——用于各自直接打开输出的剪辑类命令
// （就地改写只有走 executeFile 的命令支持）。输出还不能存在时也按路径比：用户还没来得及毁，先拦住。
func rejectInPlace(inputs []string, output string) error {
	for _, input := range inputs {
		if SameFilePath(input, output) {
			return fmt.Errorf("输出不能与输入是同一个文件（%s）：%s", output, inPlaceHint)
		}
	}
	return nil
}

// inPlaceHint 同文件被拒时给出的做法
const inPlaceHint = "请先输出到另一个文件，成功后再替换原文件（convert / compress / bake / rotate 系命令支持就地改写）"

// executeFile 主执行器入口：输出与输入是同一个文件时先写临时文件、成功后原子替换
func executeFile(input, output string, mode Mode, probe ProbeResult, params execParams, logf Logf) (ExecResult, error) {
	stagedOutput, finishInPlace := stageInPlaceOutput(input, output)
	result, execErr := executeFileTo(input, stagedOutput, mode, probe, params, logf)
	if execErr = finishInPlace(execErr); execErr != nil {
		return result, execErr
	}
	return result, nil
}
