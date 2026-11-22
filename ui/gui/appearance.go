package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// 外观来源三态：gui-settings.json 的 theme_mode 值，同时是设置页
// ui.Segmented 的段序（跟随系统/浅色/深色）。MyGo 的 Theme.SetSource
// 运行时可切（Windows 走 uxtheme preferred app mode），GUI 据此提供
// 深浅色覆盖开关（2026-11-22 用户定案：外观必须可调，不止跟随系统）。
const (
	themeModeSystem = 0 // 跟随系统深浅色（缺省）
	themeModeLight  = 1 // 强制浅色
	themeModeDark   = 2 // 强制深色
)

// themeModeNames 设置页分段控件的段标签（与三态常量下标一一对应）。
var themeModeNames = []string{"跟随系统", "浅色", "深色"}

// themeSource 把外观态映射到 MyGo 主题源。
func themeSource(mode int) mygo.ThemeSource {
	switch mode {
	case themeModeLight:
		return mygo.ThemeLight
	case themeModeDark:
		return mygo.ThemeDark
	default:
		return mygo.ThemeSystem
	}
}

// guiSettingsPath GUI 偏好文件：%AppData%/zpack/gui-settings.json。
// 与引擎（CLI 契约）无关，纯 GUI 自身偏好。
func guiSettingsPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "zpack", "gui-settings.json")
}

// loadThemeMode 启动时读回持久化外观态；文件缺失/损坏/越界一律回退跟随系统。
func loadThemeMode() int {
	path := guiSettingsPath()
	if path == "" {
		return themeModeSystem
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return themeModeSystem
	}
	var saved struct {
		ThemeMode int `json:"theme_mode"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.ThemeMode < themeModeSystem || saved.ThemeMode > themeModeDark {
		return themeModeSystem
	}
	return saved.ThemeMode
}

// saveThemeMode 外观切换即落盘；失败只记日志不打扰界面。
func saveThemeMode(mode int) {
	path := guiSettingsPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("外观偏好目录创建失败: %v", err)
		return
	}
	raw, err := json.MarshalIndent(map[string]int{"theme_mode": mode}, "", "  ")
	if err != nil {
		log.Printf("外观偏好序列化失败: %v", err)
		return
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		log.Printf("外观偏好写入失败: %v", err)
	}
}
