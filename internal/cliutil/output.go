package cliutil

import (
	"os"
	"sync"
)

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

// registeredGuards 本次运行里建过的全部守卫。强制取消的时候（RemoveCreatedOutputs）据此清掉本次新建的
// 输出——那一刻命令还没走到自己的失败分支，没人替它清
var (
	registeredGuards   []OutputGuard
	registeredGuardsMu sync.Mutex
)

// NewOutputGuard 在调用引擎之前创建：记下输出路径当时是否已存在，并登记到本次运行的名单里
func NewOutputGuard(outputPath string) OutputGuard {
	_, statErr := os.Stat(outputPath)
	guard := OutputGuard{OutputPath: outputPath, existedBefore: statErr == nil}
	registeredGuardsMu.Lock()
	registeredGuards = append(registeredGuards, guard)
	registeredGuardsMu.Unlock()
	return guard
}

// RemoveIfCreated 命令失败后调用：输出是本次新建的才删
func (guard OutputGuard) RemoveIfCreated() {
	if guard.existedBefore {
		return
	}
	os.Remove(guard.OutputPath)
}

// RemoveCreatedOutputs 强制取消时调用：本次运行新建的输出全部删掉，动工前就有的不动。
// 尽力而为——引擎可能还握着文件（Windows 上删不掉打开着的文件），删不掉的留在原地。
// 返回没删掉的路径（空=都清干净了，或本次运行没有新建过输出）
func RemoveCreatedOutputs() []string {
	registeredGuardsMu.Lock()
	guards := registeredGuards
	registeredGuards = nil
	registeredGuardsMu.Unlock()
	var leftover []string
	removed := map[string]bool{}
	for _, guard := range guards {
		if guard.existedBefore || removed[guard.OutputPath] {
			continue
		}
		removed[guard.OutputPath] = true
		if _, statErr := os.Stat(guard.OutputPath); statErr == nil {
			if removeErr := os.Remove(guard.OutputPath); removeErr != nil {
				leftover = append(leftover, guard.OutputPath)
			}
		}
	}
	return leftover
}
