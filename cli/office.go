// office 命令族：旧格式 Office 文档转换——doc/wps → docx（cgo 层，office_oxide 引擎）。
package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/stdout"
	"github.com/guohub8080/zpack/office"
)

// NewOfficeCmd office 命令
func NewOfficeCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "office <convert> [参数]",
		Short:              "旧格式 Office 文档转换（doc/wps → docx）",
		DisableFlagParsing: true,
		Run: func(command *cobra.Command, args []string) {
			helpAwareRun(command, args, runOffice)
		},
	}
}

func runOffice(args []string) {
	subcommand := firstPositional(args)
	restArgs := restAfterPositional(args, subcommand)
	switch subcommand {
	case "convert":
		officeConvert(restArgs)
	default:
		stdout.ErrorPrint("用法: office convert <输入> <输出> [--try-msoffice|--try-wps|--try-libreoffice]\n全格式矩阵：doc/wps→docx、xls/et→xlsx、ppt/dps→pptx（族匹配强制，跨族拒绝）\n.wps/.et/.dps = 现代 WPS 文件（MS 格式同构，魔数识别）；--try-* = 本机套件后台转换")
	}
}

// officeConvert doc/wps → docx（缺省内置 office_oxide；--try-* 探测本机套件后台转换）
func officeConvert(args []string) {
	tryEngine := ""
	tryFlags := 0
	var positional []string
	skipNext := false // --libreoffice-path 的值不参与后续解析
	for argIdx, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch {
		case arg == "--try-msoffice", arg == "--try-wps", arg == "--try-libreoffice":
			tryFlags++
			tryEngine = map[string]string{
				"--try-msoffice":    office.ENGINE_MSOFFICE,
				"--try-wps":         office.ENGINE_WPS,
				"--try-libreoffice": office.ENGINE_LIBREOFFICE,
			}[arg]
		case arg == "--libreoffice-path":
			// 显式指路 LibreOffice（便携版/绿色版/非默认安装位）；进程内覆盖同名环境变量——
			// 优先级：CLI 旗标 > ZPACK_OFFICE_LIBREOFFICE_PATH > 探测链（注册表/PATH/目录猜）
			if argIdx+1 >= len(args) || strings.HasPrefix(args[argIdx+1], "-") {
				stdout.ErrorPrint("--libreoffice-path 需要一个路径参数（LibreOffice 根目录或 soffice.exe 全路径均可）")
			}
			os.Setenv("ZPACK_OFFICE_LIBREOFFICE_PATH", args[argIdx+1])
			skipNext = true
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		}
	}
	if tryFlags > 1 {
		stdout.ErrorPrint("--try-msoffice / --try-wps / --try-libreoffice 三旗标互斥（一次选一个引擎）")
	}
	if len(positional) != 2 {
		stdout.ErrorPrint("用法: office convert <输入> <输出> [--try-msoffice|--try-wps|--try-libreoffice] [--libreoffice-path <路径>]\n--try-* = 探测本机已装套件，隐藏后台进程转换（三旗标互斥）\n--libreoffice-path = 显式指路 LibreOffice（根目录或 soffice.exe 均可；便携版/自定义安装位场景）")
	}
	absoluteInput, _ := filepath.Abs(positional[0])
	absoluteOutput, _ := filepath.Abs(positional[1])
	if tryEngine != "" {
		// 外部引擎层纯 Go——双构建皆可用
		result, convertErr := office.ConvertWithEngine(absoluteInput, absoluteOutput, tryEngine)
		if convertErr != nil {
			stdout.ErrorPrint(convertErr.Error())
		}
		stdout.JSONPrint(result)
		return
	}
	if !office.Available() {
		stdout.ErrorPrint(office.ErrNeedsFullBuild.Error())
	}
	result, convertErr := office.Convert(absoluteInput, absoluteOutput)
	if convertErr != nil {
		stdout.ErrorPrint(convertErr.Error())
	}
	stdout.JSONPrint(result)
}
