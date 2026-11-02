//go:build !windows && !darwin

// Linux 引擎层：MS Office 不存在；WPS Linux 无公开自动化接口——探测后明确报错。
package office

import (
	"fmt"
	"os"
)

// convertViaMSOffice Linux：无 Microsoft Office
func convertViaMSOffice(input, output string) error {
	return fmt.Errorf("Linux 无 Microsoft Office——用 --try-libreoffice")
}

// convertViaWPS Linux：探测安装后明确报无接口
func convertViaWPS(input, output string) error {
	for _, candidate := range []string{"/usr/bin/wps", "/opt/kingsoft/wps-office/office6/wps"} {
		if _, statErr := os.Stat(candidate); statErr == nil {
			return fmt.Errorf("检测到 WPS（Linux）但该平台暂无自动化接口——请在 WPS 中另存，或用 --try-libreoffice")
		}
	}
	return fmt.Errorf("未检测到 WPS（Linux）")
}
