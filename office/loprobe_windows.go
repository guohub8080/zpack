//go:build windows

// LibreOffice 注册表探测（Windows）：HKLM/HKCU 的 SOFTWARE\LibreOffice\UNO\InstallPath
// 是安装器写入的权威路径（任意自定义安装位都写；用户级安装在 HKCU、32 位在 WOW6432Node）
// ——比猜 ProgramFiles 默认目录健壮，自定义安装位（如 D:\Apps\LibreOffice）也能找到。
package office

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// libreOfficeInstallDir 注册表查 LibreOffice 安装 program 目录（未找到返回空）
func libreOfficeInstallDir() string {
	for _, keyPath := range []string{
		`SOFTWARE\LibreOffice\UNO\InstallPath`,
		`SOFTWARE\WOW6432Node\LibreOffice\UNO\InstallPath`,
	} {
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			key, openErr := registry.OpenKey(root, keyPath, registry.QUERY_VALUE|registry.WOW64_64KEY)
			if openErr != nil {
				continue
			}
			// 默认值 + 版本子键（如 "LibreOffice 26.8"）任取非空——各版本都指 program 目录
			valueNames, _ := key.ReadValueNames(-1)
			valueNames = append([]string{""}, valueNames...)
			for _, valueName := range valueNames {
				installDir, _, valErr := key.GetStringValue(valueName)
				if valErr == nil && installDir != "" {
					key.Close()
					if _, statErr := os.Stat(filepath.Join(installDir, "soffice.exe")); statErr == nil {
						return installDir
					}
				}
			}
			key.Close()
		}
	}
	return ""
}
