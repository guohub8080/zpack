// 外部 Office 引擎探测与调用层（纯 Go——无 cgo 依赖，双构建皆可用）：
// --try-msoffice（Windows COM / macOS AppleScript，隐藏进程）
// --try-wps（Windows COM KWPS.Application）
// --try-libreoffice（三平台 soffice --headless，隔离 profile 不干扰用户实例）
package office

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ENGINE_MSOFFICE 等引擎名（结果 JSON 的 engine 字段值）
const (
	ENGINE_MSOFFICE    = "msoffice"
	ENGINE_WPS         = "wps"
	ENGINE_LIBREOFFICE = "libreoffice"
)

// ExternalConvertResult 外部引擎转换结果（与内置 ConvertResult 同形 + engine）
type ExternalConvertResult struct {
	Input        string `json:"input"`
	Output       string `json:"output"`
	Engine       string `json:"engine"` // msoffice / wps / libreoffice
	SourceFormat string `json:"source_format"`
	TargetFormat string `json:"target_format"`
	SizeBytes    int64  `json:"size_bytes"`
}

// ConvertWithEngine 外部引擎转换入口（engine ∈ msoffice/wps/libreoffice）
func ConvertWithEngine(input, output, engine string) (*ExternalConvertResult, error) {
	if _, statErr := os.Stat(input); statErr != nil {
		return nil, fmt.Errorf("路径不存在: %s", input)
	}
	inputExt := strings.ToLower(filepath.Ext(input))
	if _, known := LEGACY_OFFICE_INPUTS[inputExt]; !known {
		return nil, fmt.Errorf("输入暂只支持 .doc/.wps/.xls/.et/.ppt/.dps，收到: %s", inputExt)
	}
	outputExt := strings.ToLower(filepath.Ext(output))
	if _, outputOK := map[string]bool{".docx": true, ".xlsx": true, ".pptx": true}[outputExt]; !outputOK {
		return nil, fmt.Errorf("目标暂只支持 .docx/.xlsx/.pptx，收到: %s", outputExt)
	}
	// 族匹配（同内置引擎）
	if familyOutput, hasFamily := OFFICE_FAMILY[inputExt]; hasFamily && familyOutput != outputExt {
		return nil, fmt.Errorf("跨族转换不支持：%s → %s（只出 %s）", inputExt, outputExt, familyOutput)
	}

	var convertErr error
	switch engine {
	case ENGINE_LIBREOFFICE:
		convertErr = convertViaLibreOffice(input, output)
	case ENGINE_MSOFFICE:
		convertErr = convertViaMSOffice(input, output)
	case ENGINE_WPS:
		convertErr = convertViaWPS(input, output)
	default:
		return nil, fmt.Errorf("未知引擎: %s", engine)
	}
	if convertErr != nil {
		return nil, convertErr
	}
	outputInfo, statErr := os.Stat(output)
	if statErr != nil {
		return nil, fmt.Errorf("引擎未产出文件: %w", statErr)
	}
	return &ExternalConvertResult{
		Input:        absPathLocal(input),
		Output:       absPathLocal(output),
		Engine:       engine,
		SourceFormat: formatLabel(inputExt),
		// 按输出扩展名推导（doc→docx/xls→xlsx/ppt→pptx 全族通用）——此前写死 "docx"，
		// 全格式矩阵加入后 xls/xlsx 报错格式名（2026-11-08 Windows 实测修复）
		TargetFormat: strings.TrimPrefix(strings.ToLower(filepath.Ext(output)), "."),
		SizeBytes:    outputInfo.Size(),
	}, nil
}

// 探测层分层策略（用户定案：不写死路径——不同用户安装位不同）：
// ①环境变量显式覆盖（ZPACK_OFFICE_LIBREOFFICE_PATH / _WORD_PATH / _WPS_PATH）
// ②PATH 查找
// ③平台动态解析：macOS=Spotlight 按包 ID（任意安装位）；Windows=COM 注册表自解析
//   （CreateObject 即探测）+ 常见根；Linux=发行版路径 + flatpak/snap 包装器
// ④常见路径兜底
// 返回 argv 形态（flatpak/snap 是包装命令——不是单一路径）。

// detectLibreOfficeCmd 探测 LibreOffice 启动命令（argv）
func detectLibreOfficeCmd() []string {
	// ① 环境变量显式覆盖（终极健壮——用户自指；容错：带引号剥掉、给目录自动拼 program\soffice.exe）
	if override := strings.Trim(os.Getenv("ZPACK_OFFICE_LIBREOFFICE_PATH"), ` "`); override != "" {
		if info, statErr := os.Stat(override); statErr == nil {
			if info.IsDir() {
				if _, binErr := os.Stat(filepath.Join(override, "program", "soffice.exe")); binErr == nil {
					return []string{filepath.Join(override, "program", "soffice.exe")}
				}
				if _, binErr := os.Stat(filepath.Join(override, "soffice.exe")); binErr == nil {
					return []string{filepath.Join(override, "soffice.exe")}
				}
			} else {
				return []string{override}
			}
		}
	}
	// ② PATH
	if path, onPath := exec.LookPath("soffice"); onPath == nil {
		return []string{path}
	}
	// ③ 平台动态解析
	switch runtime.GOOS {
	case "darwin":
		// Spotlight 按包 ID——任意安装位置（含非 /Applications 卷）
		if appPath := mdfindAppBundle("org.libreoffice.script"); appPath != "" {
			binary := filepath.Join(appPath, "Contents", "MacOS", "soffice")
			if _, statErr := os.Stat(binary); statErr == nil {
				return []string{binary}
			}
		}
	case "windows":
		// 注册表优先（InstallPath 为安装器权威写入——任意自定义安装位都写；用户级在 HKCU、
		// 32 位在 WOW6432Node），目录猜默认位仅作兜底
		if installDir := libreOfficeInstallDir(); installDir != "" {
			return []string{filepath.Join(installDir, "soffice.exe")}
		}
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			binary := filepath.Join(root, "LibreOffice", "program", "soffice.exe")
			if _, statErr := os.Stat(binary); statErr == nil {
				return []string{binary}
			}
		}
		// 盘符扫描（绿色解压版无注册表——覆盖「解压到 D:\LibreOffice 不设 env」场景；26 个 Stat 开销微秒级）
		for driveLetter := 'C'; driveLetter <= 'Z'; driveLetter++ {
			binary := string(driveLetter) + `:\LibreOffice\program\soffice.exe`
			if _, statErr := os.Stat(binary); statErr == nil {
				return []string{binary}
			}
		}
	default:
		// flatpak / snap 包装器（发行版包在 ④ 兜底覆盖）
		if path, onPath := exec.LookPath("flatpak"); onPath == nil {
			return []string{path, "run", "org.libreoffice.LibreOffice"}
		}
		if path, onPath := exec.LookPath("snap"); onPath == nil {
			return []string{path, "run", "libreoffice"}
		}
	}
	// ④ 常见路径兜底
	fallbacks := []string{"/usr/bin/soffice", "/usr/lib/libreoffice/program/soffice",
		"/opt/libreoffice/program/soffice", "/Applications/LibreOffice.app/Contents/MacOS/soffice"}
	for _, candidate := range fallbacks {
		if _, statErr := os.Stat(candidate); statErr == nil {
			return []string{candidate}
		}
	}
	return nil
}

// mdfindAppBundle Spotlight 按包 ID 找 .app（macOS 任意安装位；无结果返回空）
func mdfindAppBundle(bundleID string) string {
	cmd := exec.Command("mdfind", "kMDItemCFBundleIdentifier == '"+bundleID+"'")
	outputBytes, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(outputBytes), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".app") {
			return line
		}
	}
	return ""
}

// detectWordApp macOS：Spotlight 找 Word（任意位置）→ AppleScript 按包 ID 寻址
func detectWordApp() string {
	if override := os.Getenv("ZPACK_OFFICE_WORD_PATH"); override != "" {
		return override
	}
	if mdfindAppBundle("com.microsoft.Word") != "" {
		return "com.microsoft.Word" // 存在即可——AppleScript 按包 ID 寻址，与路径无关
	}
	if _, statErr := os.Stat("/Applications/Microsoft Word.app"); statErr == nil {
		return "com.microsoft.Word"
	}
	return ""
}

// detectWpsApp 跨平台 WPS 探测（返回存在性描述；Windows 走 COM 自解析不需路径）
func detectWpsApp() bool {
	if override := os.Getenv("ZPACK_OFFICE_WPS_PATH"); override != "" {
		if _, statErr := os.Stat(override); statErr == nil {
			return true
		}
	}
	if _, onPath := exec.LookPath("wps"); onPath == nil {
		return true
	}
	if runtime.GOOS == "darwin" {
		if mdfindAppBundle("com.kingsoft.wpsoffice.mac") != "" {
			return true
		}
		for _, candidate := range []string{"/Applications/wpsoffice.app", "/Applications/WPS Office.app"} {
			if _, statErr := os.Stat(candidate); statErr == nil {
				return true
			}
		}
		return false
	}
	for _, candidate := range []string{"/usr/bin/wps", "/opt/kingsoft/wps-office/office6/wps"} {
		if _, statErr := os.Stat(candidate); statErr == nil {
			return true
		}
	}
	return false
}

// convertViaLibreOffice soffice --headless（隔离 profile——不与用户正开着的实例冲突）
func convertViaLibreOffice(input, output string) error {
	sofficeArgv := detectLibreOfficeCmd()
	if sofficeArgv == nil {
		return fmt.Errorf("未检测到 LibreOffice（%s）——也可设置 ZPACK_OFFICE_LIBREOFFICE_PATH 显式指定", installHint("libreoffice"))
	}
	outputDir := filepath.Dir(output)
	profileDir, mkErr := os.MkdirTemp("", "zpack-lo-profile-*")
	if mkErr == nil {
		defer os.RemoveAll(profileDir)
	}
	args := []string{"--headless", "--norestore"}
	if mkErr == nil {
		// file URL 必须合法（Windows 上 profileDir 是 C:\... 反斜杠形态，手拼 file:// 会产
		// 畸形 URI → LibreOffice profile 初始化失败、甚至弹「bootstrap.ini 已损坏」对话框
		// ——经 url.URL 构造：卷符路径补前导 / 成 file:///C:/... 形态，空格等自动转义
		// （2026-11-08 Windows 实测修复）
		slashProfile := filepath.ToSlash(profileDir)
		if !strings.HasPrefix(slashProfile, "/") {
			slashProfile = "/" + slashProfile // Windows 卷符：file:///C:/...
		}
		profileURI := (&url.URL{Scheme: "file", Path: slashProfile}).String()
		args = append(args, "-env:UserInstallation="+profileURI)
	}
	args = append(args, "--convert-to", strings.TrimPrefix(strings.ToLower(filepath.Ext(output)), "."), "--outdir", outputDir, input)
	argv := append(sofficeArgv, args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	// soffice 无 stderr 静默模式——捕获输出供错误诊断
	outputBytes, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("LibreOffice 转换失败: %w（%s）", runErr, strings.TrimSpace(string(outputBytes)))
	}
	// soffice 产出 = <outdir>/<输入基名>.<目标扩展> → 归位到目标路径
	produced := filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))+strings.ToLower(filepath.Ext(output)))
	if renameErr := os.Rename(produced, output); renameErr != nil {
		return fmt.Errorf("LibreOffice 产物归位失败: %w", renameErr)
	}
	return nil
}

// installHint 各引擎的安装提示（按平台）
func installHint(engine string) string {
	switch runtime.GOOS {
	case "darwin":
		switch engine {
		case "libreoffice":
			return "brew install --cask libreoffice"
		case "wps":
			return "https://www.wps.com/product/wps-mac"
		default:
			return "Microsoft Office for Mac"
		}
	case "windows":
		return "Microsoft Office / WPS Office / LibreOffice 官网"
	default:
		return "apt install libreoffice 或包管理器等价命令"
	}
}

// formatLabel 源格式标注：MS 扩展名=core 名；WPS 别名=core(alias) 形态
func formatLabel(inputExt string) string {
	core := LEGACY_OFFICE_INPUTS[inputExt]
	if inputExt == ".wps" || inputExt == ".et" || inputExt == ".dps" {
		return core + "(" + strings.TrimPrefix(inputExt, ".") + ")"
	}
	return core
}

func absPathLocal(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}
