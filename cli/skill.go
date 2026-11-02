// skill 命令：输出/安装 zpack 的 Agent 技能文档（SKILL.md 经 go:embed 内嵌于二进制——
// 单文件分发且文档与二进制版本永不漂移）。
//
//	zpack skill                  → 文档打到 stdout（人看 / 管道）
//	zpack skill --install        → 探测本机 agent 技能根目录，全部安装
//	zpack skill --install <路径>  → 安装到指定技能根目录（<路径>/zpack/SKILL.md）
package cli

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/stdout"
)

//go:embed skill.md
var skillDocument string

// SKILL_DIR_NAME 技能文件夹名（技能根目录下以此名建文件夹，SKILL.md 放里面）
const SKILL_DIR_NAME = "zpack"

// NewSkillCmd skill 命令
func NewSkillCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "skill [--install [目录]]",
		Short:              "输出/安装 Agent 技能文档（SKILL.md 内嵌于二进制，供消费端 agent 安装即学会）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runSkill) },
	}
}

func runSkill(args []string) {
	installMode := false
	installPath := ""
	for argIndex := 0; argIndex < len(args); argIndex++ {
		if args[argIndex] == "--install" {
			installMode = true
			// 后随非旗标参数 = 显式技能根目录；无后随值 = 探测模式
			if argIndex+1 < len(args) && !strings.HasPrefix(args[argIndex+1], "-") {
				installPath = args[argIndex+1]
				argIndex++
			}
		}
	}
	if !installMode {
		stdout.TextPrint(skillDocument)
	}

	var skillRoots []string
	if installPath != "" {
		skillRoots = []string{installPath}
	} else {
		skillRoots = probeSkillRoots()
	}
	if len(skillRoots) == 0 {
		stdout.ErrorPrint("未发现 agent 技能目录；用 zpack skill --install <路径> 指定（如 ~/.claude/skills 或 ~/.agents/skills）")
	}

	installed := []string{}
	for _, root := range skillRoots {
		targetDir := filepath.Join(root, SKILL_DIR_NAME)
		if mkdirErr := os.MkdirAll(targetDir, 0o755); mkdirErr != nil {
			stdout.ErrorPrint("无法创建技能目录: " + mkdirErr.Error())
		}
		targetFile := filepath.Join(targetDir, "SKILL.md")
		// 与 `zpack skill` 的 stdout 逐字节一致（TextPrint 尾部补换行——POSIX 文本文件标准结尾）
		if writeErr := os.WriteFile(targetFile, []byte(skillDocument+"\n"), 0o644); writeErr != nil {
			stdout.ErrorPrint("无法写入技能文件: " + writeErr.Error())
		}
		absoluteFile, _ := filepath.Abs(targetFile)
		installed = append(installed, absoluteFile)
	}
	stdout.JSONPrint(skillInstallJSON{
		Skill: SKILL_DIR_NAME, Installed: installed, SizeBytes: len(skillDocument),
	})
}

// probeSkillRoots 探测本机 agent 技能根目录：家目录级（不存在则跳过——不代用户决定装哪家）
// + 当前项目级（仅当目录已存在）
func probeSkillRoots() []string {
	var roots []string
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		roots = append(roots,
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".agents", "skills"),
		)
	}
	roots = append(roots,
		filepath.Join(".claude", "skills"),
		filepath.Join(".agents", "skills"),
	)
	var found []string
	for _, root := range roots {
		if info, statErr := os.Stat(root); statErr == nil && info.IsDir() {
			found = append(found, root)
		}
	}
	return found
}

// skillInstallJSON skill --install 成功载荷
type skillInstallJSON struct {
	Skill     string   `json:"skill"`
	Installed []string `json:"installed"`
	SizeBytes int      `json:"size_bytes"`
}
