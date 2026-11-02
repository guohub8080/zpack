// 临时目录基址注入：CLI --temp-dir 的落点（包级配置，与 cancel.go 的取消标记同一模式）。
// 缺省空串 = os.TempDir()（macOS/Linux $TMPDIR→/tmp，Windows %TMP%→%TEMP%→%USERPROFILE%）；
// 指定后 core 内全部临时溢写文件（zip 分块、cab 大文件夹）随之改道。
package core

import (
	"os"
	"sync"
)

// tempDirBase 包级临时目录基址（并发安全读）
var tempDirBase struct {
	mu    sync.RWMutex
	value string
}

// SetTempDir 设置临时目录基址（CLI --temp-dir 注入；须在压缩/解压开始前调用一次）
func SetTempDir(dir string) {
	tempDirBase.mu.Lock()
	defer tempDirBase.mu.Unlock()
	tempDirBase.value = dir
}

// resolveTempBase 返回实际临时目录基址（空串 → 系统默认）
func resolveTempBase() string {
	tempDirBase.mu.RLock()
	defer tempDirBase.mu.RUnlock()
	if tempDirBase.value != "" {
		return tempDirBase.value
	}
	return os.TempDir()
}
