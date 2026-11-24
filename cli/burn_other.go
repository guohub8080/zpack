//go:build !windows

// Windows 刻录后端的占位：非 Windows 平台上 runtimeOS() 恒不为 "windows"，这两个分支不可达——
// 存在只为让 burn.go 的平台分派在三平台都可编译（真身在 burn_windows.go）。
package cli

// burnListDevicesWindows 非 Windows 平台的占位（不可达）
func burnListDevicesWindows() []BurnDevice {
	return nil
}

// burnWindows 非 Windows 平台的占位（不可达）
func burnWindows(inputs []string, flags burnFlags) (int64, error) {
	return 0, burnUnsupportedPlatform()
}

// burnWindowsVolumeName 非 Windows 平台的占位（不可达——burnPlatformVolumeName 只在 Windows 上调它）
func burnWindowsVolumeName(raw string) string {
	return burnSanitizeVolid(raw)
}
