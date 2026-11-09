//go:build !windows

// LibreOffice 平台探测占位（非 Windows：注册表机制不存在，走 Spotlight/PATH 等既有路径）。
package office

func libreOfficeInstallDir() string { return "" }
