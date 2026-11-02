//go:build darwin

// MS Office（macOS）：Word AppleScript 自动化——launch 不激活（后台转换，
// 不抢用户焦点）；转换前已运行则转换后不退出（不打扰用户正在用的 Word）。
package office

import (
	"fmt"
	"os/exec"
	"strings"
)

// detectMSOfficeDarwin Word 安装探测（Spotlight 包 ID 任意位置 + 环境变量覆盖）
func detectMSOfficeDarwin() string {
	return detectWordApp()
}

// convertViaMSOffice macOS：Word AppleScript（launch 后台 + SaveAs + 恢复原状态）
func convertViaMSOffice(input, output string) error {
	if detectMSOfficeDarwin() == "" {
		return fmt.Errorf("未检测到 Microsoft Word——安装后重试，或设置 ZPACK_OFFICE_WORD_PATH 显式指定")
	}
	// 按包 ID 寻址（tell application id）——与安装位置/本地化名称无关
	script := fmt.Sprintf(`
tell application id "com.microsoft.Word"
	set wasRunning to running
	set inPosix to %q
	set outPosix to %q
	launch
	open inPosix
	set theDoc to active document
	save as theDoc file name outPosix file format format document default
	close theDoc saving no
	if not wasRunning then quit
end tell`, input, output)
	cmd := exec.Command("osascript", "-e", script)
	if outputBytes, runErr := cmd.CombinedOutput(); runErr != nil {
		return fmt.Errorf("Word 自动化失败: %w（%s）——文件可能含不支持的特性", runErr, strings.TrimSpace(string(outputBytes)))
	}
	return nil
}

// convertViaWPS macOS：WPS 无公开自动化接口——探测后明确报错
func convertViaWPS(input, output string) error {
	if detectWpsApp() {
		return fmt.Errorf("检测到 WPS（macOS）但该平台暂无自动化接口——请在 WPS 中另存，或用 --try-msoffice/--try-libreoffice")
	}
	return fmt.Errorf("未检测到 WPS（macOS）")
}
