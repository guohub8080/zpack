// --temp-dir 旗标的统一处理：四个耗临时空间的命令（convert/bench/compress/extract）共用。
// 语义：缺省 = 系统临时目录（os.TempDir）；指定 = MkdirAll 保证存在后注入 core
// （zip 分块/cab 大文件夹溢写随之改道），返回基目录供 convert/bench 的 MkdirTemp 落位。
package cli

import (
	"os"
	"strings"

	"github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// applyTempDirFlag 解析 --temp-dir 并注入；返回基目录（空串 = 系统默认）
func applyTempDirFlag(args []string) string {
	base, has := cmdutil.FlagValue(args, "--temp-dir")
	if !has {
		return ""
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	if mkdirErr := os.MkdirAll(base, 0o755); mkdirErr != nil {
		stdout.ErrorPrint("无法创建临时目录: " + mkdirErr.Error())
	}
	core.SetTempDir(base)
	return base
}
