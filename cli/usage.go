// 子命令 help 守卫：DisableFlagParsing 吞掉了 cobra 的 --help 自动机制——
// agent 嗅探 CLI 的第一本能就是 `工具 --help`（根命令级 cobra 自动可用，子命令级必须手动守卫）。
// 各命令 Run 入口统一走 helpAwareRun：命中 --help/-h → 打印 cobra 帮助（Short/Long + Usage）→ exit 0。
package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/cmdutil"
)

// helpAwareRun 子命令 Run 统一入口：先应答 --help/-h，再进命令体
func helpAwareRun(command *cobra.Command, args []string, body func([]string)) {
	if cmdutil.HasFlag(args, "--help") || cmdutil.HasFlag(args, "-h") {
		if helpErr := command.Help(); helpErr != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	body(args)
}
