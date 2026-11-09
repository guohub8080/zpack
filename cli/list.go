// list / test / info 命令：归档内容列表、完整性验证、元数据信息（读侧轻量封装）。
// 三个命令共享 core.ListArchive 基座——list 输出条目明细 / info 输出聚合 / test 逐条验 CRC。
package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/cmdutil"
	"github.com/guohub8080/zpack/internal/stdout"
)

// NewListCmd list 命令：列出归档内容
func NewListCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "list <归档>",
		Short:              "列出归档内容（名/尺寸/加密标记，不解压）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runList) },
	}
}

// NewTestCmd test 命令：验证归档完整性
func NewTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "test <归档>",
		Short:              "验证归档完整性（逐条 CRC 校验，不写盘）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runTest) },
	}
}

// NewInfoCmd info 命令：归档元数据
func NewInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "info <归档>",
		Short:              "归档元数据（格式/条目数/总大小/压缩比/加密状态）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runInfo) },
	}
}

func resolveArchivePath(args []string) string {
	archivePath := cmdutil.FirstPositional(args)
	if archivePath == "" {
		stdout.ErrorPrint("请指定归档文件路径")
	}
	absolutePath, absErr := absPath(archivePath)
	if absErr != nil {
		absolutePath = archivePath
	}
	if _, statErr := os.Stat(absolutePath); statErr != nil {
		stdout.ErrorPrint("路径不存在：" + absolutePath)
	}
	return absolutePath
}

// absPath 标准库绝对化（2026-11-08 Windows 实测修复：此前手拼 cwd+"/"+p 且以「/ 开头」判
// 绝对——mac 恰好等价，Windows 上把 C:/ 盘符路径当相对路径拼到 CWD 后。filepath.Abs 跨平台
// 正确处理卷标/正反斜杠/.. 归一，不再手写）
func absPath(p string) (string, error) {
	if p == "" {
		return os.Getwd()
	}
	return filepathAbs(p)
}

// filepathAbs 标准库别名（保持既有调用面）
func filepathAbs(p string) (string, error) {
	return filepath.Abs(p)
}

func runList(args []string) {
	archivePath := resolveArchivePath(args)
	info, err := core.ListArchive(archivePath)
	if err != nil {
		stdout.ErrorPrint(err.Error())
	}
	stdout.JSONPrint(info)
}

func runTest(args []string) {
	archivePath := resolveArchivePath(args)
	info, failures, err := core.TestArchive(archivePath)
	if err != nil {
		stdout.ErrorPrint(err.Error())
	}
	if len(failures) > 0 {
		// 判定类失败：测试完成、结论为否——JSONFailPrint（信封 is_success:false + data 明细）
		stdout.JSONFailPrint(testFailedJSON{
			Format: info.Format, TestedEntries: info.FileCount,
			FailedCount: len(failures), Failures: failures,
		})
		return
	}
	stdout.JSONPrint(testOKJSON{
		Format: info.Format, TestedEntries: info.FileCount, AllOK: true,
	})
}

func runInfo(args []string) {
	archivePath := resolveArchivePath(args)
	info, err := core.ListArchive(archivePath)
	if err != nil {
		stdout.ErrorPrint(err.Error())
	}
	// info 模式不带条目明细
	info.EntryList = nil
	stdout.JSONPrint(info)
}

// ── test 输出契约 struct ──

type testOKJSON struct {
	Format        string `json:"format"`
	TestedEntries int    `json:"tested_entries"`
	AllOK         bool   `json:"all_ok"`
}

type testFailedJSON struct {
	Format        string   `json:"format"`
	TestedEntries int      `json:"tested_entries"`
	FailedCount   int      `json:"failed_count"`
	Failures      []string `json:"failures"`
}
