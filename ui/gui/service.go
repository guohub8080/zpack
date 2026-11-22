// zpack 引擎服务层：GUI 与 bin/zpack 之间的唯一通道（stdout JSON 信封 + stderr 进度行）。
// 零引擎代码——本文件只是 exec + 解析（架构铁律见 ../doc.go）。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Task 是一条任务（一个 zpack 子进程）。
type Task struct {
	Title   string  // 列表标题（如 "项目备份.zip"）
	State   int     // TaskRunning / TaskDone / TaskFailed
	Percent float64 // 0-100
	Detail  string  // 进度明细（速度/文件数/剩余）
	Result  string  // 完成摘要（产物+规模）或失败原因
	Folder  string  // 完成后「打开文件夹」目标目录（GUI 消费）
	Product string  // 主产物（压缩=归档路径 / 解压=输出目录）；任务卡「显示」钮精确选中

	Updates chan struct{} // 引擎事件 → GUI 重绘信号
	cancel  func()
}

const (
	TaskRunning = iota
	TaskDone
	TaskFailed
)

func countRunning(tasks []*Task) int {
	running := 0
	for _, task := range tasks {
		if task.State == TaskRunning {
			running++
		}
	}
	return running
}

// Cancel 终止子进程并删半成品（idempotent）。
func (task *Task) Cancel() {
	if task.cancel != nil {
		task.cancel()
	}
}

// Service 定位 zpack 二进制并派发任务。
type Service struct {
	binary string
	once   sync.Once
}

func NewService() *Service {
	return &Service{}
}

// zpackPath 引擎定位：环境变量 > 自身 bundle Resources（打包态）> 仓库 bin（开发态）> FinderSync 宿主内嵌引擎（本机兜底）。
func (service *Service) zpackPath() string {
	service.once.Do(func() {
		selfPath, _ := os.Executable()
		engineName := "zpack"
		if runtime.GOOS == "windows" {
			engineName = "zpack.exe" // Windows 产物带扩展名——裸名 bin/zpack 不存在
		}
		candidates := []string{
			os.Getenv("ZPACK_BIN"),
			exeSibling(engineName),     // 开发态平铺布局（引擎与 GUI 同目录）
			bundleResource(engineName), // 打包态：自身 .app Contents/Resources 内嵌引擎
			repoBin(engineName),        // 开发态仓库 bin——必须先于 /Applications：装机引擎
			// 可能旧于仓库（m05014 血案：dev GUI 拿旧引擎静默
			// 覆盖桌面密钥，引擎覆盖守卫形同虚设）
			"/Applications/zpack Finder Menu.app/Contents/Resources/zpack", // FinderSync 宿主内嵌引擎（装机兜底）
		}
		for _, candidate := range candidates {
			// 自体防护：打包态 GUI 可执行名恰为 zpack，exeSibling 会返回自己——
			// 不挡住则把自己当引擎 exec，引擎命令变成再开一个 GUI 窗口
			if candidate == "" || candidate == selfPath || !isExecutable(candidate) {
				continue
			}
			service.binary = candidate
			return
		}
	})
	return service.binary
}

// bundleResource 自身 .app 包内 Resources 伙伴文件（非打包形态返回空串语义由 isExecutable 兜住）。
func bundleResource(name string) string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Dir(exePath)), "Resources", name)
}

// exeSibling 可执行文件同目录的伙伴文件。
func exeSibling(name string) string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exePath), name)
}

// repoBin 开发态：本仓库产线产物。
func repoBin(name string) string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exePath)
	for depth := 0; depth < 5; depth++ {
		candidate := filepath.Join(dir, "bin", name)
		if isExecutable(candidate) {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true // Windows 文件无执行位语义——存在即视为可执行（否则全部候选被误拒）
	}
	return info.Mode()&0o111 != 0
}

// StartCompress 派发压缩任务（立即返回；进度经 Updates 通知）。
// volume 非空时加 --volume-size（人读尺寸或模板，如 100MB/1GB/cd/dvd）；
// nameOverride 非空时用它作包名（GUI 可编辑名字——用户定案），否则取首输入名；
// outDirOverride 非空时用它作输出目录（GUI 可编辑/点选保存位置——用户定案），
// 否则输出到首输入所在目录。
func (service *Service) StartCompress(inputs []string, format string, level int, password, volume, nameOverride, outDirOverride string) *Task {
	if len(inputs) == 0 || service.zpackPath() == "" {
		return nil
	}
	first := inputs[0]
	parent := filepath.Dir(first)
	base := strings.TrimSuffix(filepath.Base(first), filepath.Ext(first))
	if nameOverride = strings.ReplaceAll(nameOverride, "\n", ""); strings.TrimSpace(nameOverride) != "" {
		base = filepath.Base(nameOverride)
		// 用户已把后缀敲进名字里（如「报告.zip」）——去掉，扩展名由格式决定
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if outDirOverride = strings.ReplaceAll(outDirOverride, "\n", ""); strings.TrimSpace(outDirOverride) != "" {
		parent = strings.TrimSpace(outDirOverride)
	}
	output := filepath.Join(parent, base+"."+format)
	output = uniquePath(output)

	task := &Task{
		Title:   filepath.Base(output),
		State:   TaskRunning,
		Detail:  "启动中…",
		Folder:  filepath.Dir(output),
		Product: output,
		Updates: make(chan struct{}, 8),
	}
	var once sync.Once
	var cmd *exec.Cmd
	task.cancel = func() {
		once.Do(func() {
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = os.Remove(output) // 删半成品
			task.State = TaskFailed
			task.Result = "已取消"
			task.notify()
		})
	}
	go service.runCompress(task, &cmd, inputs, output, level, password, volume)
	return task
}

func (service *Service) runCompress(task *Task, cmdPtr **exec.Cmd, inputs []string, output string, level int, password, volume string) {
	arguments := []string{"compress"}
	arguments = append(arguments, inputs...)
	arguments = append(arguments, "--output", output, "--level", strconv.Itoa(level))
	if volume = strings.TrimSpace(volume); volume != "" {
		arguments = append(arguments, "--volume-size", volume)
	}
	if password != "" {
		arguments = append(arguments, "--password", password)
	}
	cmd := exec.Command(service.zpackPath(), arguments...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	*cmdPtr = cmd
	if err := cmd.Start(); err != nil {
		task.State = TaskFailed
		task.Result = "启动引擎失败：" + err.Error()
		task.notify()
		return
	}

	// stderr 进度行流（另一 goroutine 增量解析）
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		var pending []byte
		for {
			read, err := stderr.Read(buffer)
			if read > 0 {
				pending = append(pending, buffer[:read]...)
				for {
					lineBreak := bytesIndex(pending, '\n')
					if lineBreak < 0 {
						break
					}
					line := string(pending[:lineBreak])
					pending = pending[lineBreak+1:]
					if percent, detail, ok := parseProgressLine(line); ok {
						task.Percent = percent
						task.Detail = detail
						task.notify()
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// stdout：信封 JSON 整段回读
	raw := make([]byte, 0, 4096)
	readBuffer := make([]byte, 8192)
	for {
		read, err := stdout.Read(readBuffer)
		raw = append(raw, readBuffer[:read]...)
		if err != nil {
			break
		}
	}
	_ = cmd.Wait()
	<-done

	if task.State != TaskRunning {
		return // 已取消
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		task.State = TaskDone
		task.Percent = 100
		task.Result = compressSummary(raw, output)
	} else {
		task.State = TaskFailed
		task.Result = envelopeError(raw, "压缩失败（exit "+strconv.Itoa(code)+"）")
	}
	task.notify()
}

// compressSummary 从信封 JSON 提产物摘要（文件数/大小）。
func compressSummary(raw []byte, output string) string {
	var envelope struct {
		Data struct {
			FileCount int   `json:"file_count"`
			SizeBytes int64 `json:"size_bytes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Data.FileCount > 0 {
		return itoa(envelope.Data.FileCount) + " 个文件 · " + humanSize(envelope.Data.SizeBytes)
	}
	return filepath.Base(output)
}

// envelopeError 从信封 JSON 提 error_info（无则回退）。
func envelopeError(raw []byte, fallback string) string {
	var envelope struct {
		ErrorInfo string `json:"error_info"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.ErrorInfo != "" {
		return envelope.ErrorInfo
	}
	return fallback
}

// 进度行样例：`[2026-11-21 12:00:00] 压缩中 █████░░ 68.4% | 1.2GB/1.8GB | 210MB/s | 3/48 文件 | 剩余 12秒`
var percentPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%`)

func parseProgressLine(line string) (float64, string, bool) {
	body := line
	if closeIdx := strings.Index(line, "]"); closeIdx >= 0 {
		body = line[closeIdx+1:]
	}
	if !strings.Contains(body, "中") {
		return 0, "", false
	}
	match := percentPattern.FindStringSubmatch(body)
	if match == nil {
		return 0, "", false
	}
	percent, _ := strconv.ParseFloat(match[1], 64)
	detail := strings.TrimSpace(body[percentPattern.FindStringIndex(body)[1]:])
	detail = strings.ReplaceAll(detail, "|", "·")
	detail = strings.Join(strings.Fields(detail), " ")
	return percent, strings.TrimPrefix(detail, "· "), true
}

// uniquePath 同名不覆盖：out.zip 已在 → out 2.zip → out 3.zip…
func uniquePath(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for index := 2; index < 9999; index++ {
		candidate := base + " " + strconv.Itoa(index) + ext
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
	return path
}

func bytesIndex(data []byte, target byte) int {
	for idx, current := range data {
		if current == target {
			return idx
		}
	}
	return -1
}

func (task *Task) notify() {
	select {
	case task.Updates <- struct{}{}:
	default:
	}
}

func sizeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if info.IsDir() {
		var total int64
		_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
		return total
	}
	return info.Size()
}

func humanSize(bytes int64) string {
	value := float64(bytes)
	units := []string{"B", "KB", "MB", "GB", "TB"}
	unitIdx := 0
	for value >= 1024 && unitIdx < len(units)-1 {
		value /= 1024
		unitIdx++
	}
	if unitIdx == 0 {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + units[unitIdx]
}

// InspectResult 归档体检结果（zpack info 信封回读；分卷计数为 GUI 侧配套探测）。
type InspectResult struct {
	Format       string // zip / tar.gz / 7z / rar …（魔数识别）
	FileCount    int
	TotalBytes   int64 // 解压后总大小
	ArchiveBytes int64 // 归档本体大小
	Encrypted    bool
	Volumes      int // 分卷数（>1 即分卷集；GUI 侧数 .001/.002/…）
	Err          string
}

// Inspect 异步体检归档（zpack info <归档>）：格式/文件数/是否加密。
// 分卷计数不依赖引擎——GUI 直接数同基名 .001/.002/… 兄弟文件。
func (service *Service) Inspect(path string, callback func(InspectResult)) {
	if service.zpackPath() == "" {
		callback(InspectResult{Err: "未找到 zpack 引擎"})
		return
	}
	go func() {
		result := InspectResult{Volumes: countVolumes(path)}
		cmd := exec.Command(service.zpackPath(), "info", path)
		raw, err := cmd.Output()
		if err != nil {
			// 退出非零先别弃——引擎 error 信封可能已在 stdout（is_success:false），
			// 引擎判语（无法识别/损坏）优先于笼统「预览不可用」（m00961）
			var failureEnvelope struct {
				IsSuccess bool   `json:"is_success"`
				ErrorInfo string `json:"error_info"`
			}
			if json.Unmarshal(raw, &failureEnvelope) == nil && !failureEnvelope.IsSuccess {
				result.Err = failureEnvelope.ErrorInfo
				callback(result)
				return
			}
			// info 失败不阻断解压（如分卷集 info 未覆盖）——留空壳结果
			result.Err = "预览不可用"
			callback(result)
			return
		}
		var envelope struct {
			IsSuccess bool `json:"is_success"`
			Data      struct {
				Format            string `json:"format"`
				FileCount         int    `json:"file_count"`
				TotalUncompressed int64  `json:"total_uncompressed"`
				ArchiveSize       int64  `json:"archive_size"`
				Encrypted         bool   `json:"encrypted"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || !envelope.IsSuccess {
			result.Err = "无法识别的归档"
			callback(result)
			return
		}
		result.Format = envelope.Data.Format
		result.FileCount = envelope.Data.FileCount
		result.TotalBytes = envelope.Data.TotalUncompressed
		result.ArchiveBytes = envelope.Data.ArchiveSize
		result.Encrypted = envelope.Data.Encrypted
		callback(result)
	}()
}

// countVolumes 分卷集探测：path 以 .001 结尾时数同基名连续序号卷并算总大小；
// 非分卷返回 1（单卷即一个文件）。
func countVolumes(path string) int {
	if !strings.HasSuffix(path, ".001") {
		return 1
	}
	base := strings.TrimSuffix(path, ".001")
	dir := filepath.Dir(base)
	stem := filepath.Base(base)
	volumes := 0
	for index := 1; ; index++ {
		member := filepath.Join(dir, stem+"."+fmt.Sprintf("%03d", index))
		if _, err := os.Stat(member); err != nil {
			break
		}
		volumes++
	}
	if volumes == 0 {
		return 1
	}
	return volumes
}

// StartExtract 派发解压任务（zpack extract；格式靠魔数识别，含分卷/加密）。
// password 非空才传 --password；outDir 非空传 --output（缺省=引擎智能解压）。
func (service *Service) StartExtract(archive, password, outDir string) *Task {
	if archive == "" || service.zpackPath() == "" {
		return nil
	}
	task := &Task{
		Title:   filepath.Base(archive),
		State:   TaskRunning,
		Detail:  "启动中…",
		Folder:  outDir,
		Product: outDir,
		Updates: make(chan struct{}, 8),
	}
	var once sync.Once
	var cmd *exec.Cmd
	task.cancel = func() {
		once.Do(func() {
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			task.State = TaskFailed
			task.Result = "已取消"
			task.notify()
		})
	}
	go service.runExtract(task, &cmd, archive, password, outDir)
	return task
}

func (service *Service) runExtract(task *Task, cmdPtr **exec.Cmd, archive, password, outDir string) {
	arguments := []string{"extract", archive}
	if strings.TrimSpace(outDir) != "" {
		arguments = append(arguments, "--output", strings.TrimSpace(outDir))
	}
	if password != "" {
		arguments = append(arguments, "--password", password)
	}
	cmd := exec.Command(service.zpackPath(), arguments...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	*cmdPtr = cmd
	if err := cmd.Start(); err != nil {
		task.State = TaskFailed
		task.Result = "启动引擎失败：" + err.Error()
		task.notify()
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		var pending []byte
		for {
			read, err := stderr.Read(buffer)
			if read > 0 {
				pending = append(pending, buffer[:read]...)
				for {
					lineBreak := bytesIndex(pending, '\n')
					if lineBreak < 0 {
						break
					}
					line := string(pending[:lineBreak])
					pending = pending[lineBreak+1:]
					if percent, detail, ok := parseProgressLine(line); ok {
						task.Percent = percent
						task.Detail = detail
						task.notify()
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	raw := make([]byte, 0, 4096)
	readBuffer := make([]byte, 8192)
	for {
		read, err := stdout.Read(readBuffer)
		raw = append(raw, readBuffer[:read]...)
		if err != nil {
			break
		}
	}
	_ = cmd.Wait()
	<-done

	if task.State != TaskRunning {
		return // 已取消
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		task.State = TaskDone
		task.Percent = 100
		task.Result = extractSummary(raw)
	} else {
		task.State = TaskFailed
		task.Result = envelopeError(raw, "解压失败（exit "+strconv.Itoa(code)+"）")
	}
	task.notify()
}

// extractSummary 从信封 JSON 提解压摘要（文件数/大小/跳过数）。
func extractSummary(raw []byte) string {
	var envelope struct {
		Data struct {
			FileCount int   `json:"file_count"`
			SizeBytes int64 `json:"size_bytes"`
			Skipped   int   `json:"skipped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Data.FileCount > 0 {
		summary := itoa(envelope.Data.FileCount) + " 个文件 · " + humanSize(envelope.Data.SizeBytes)
		if envelope.Data.Skipped > 0 {
			summary += " · 跳过 " + itoa(envelope.Data.Skipped)
		}
		return summary
	}
	return "解压完成"
}

// ─── 工具箱（base64 / crypto 家族：无进度短命令，stdout 信封回读）───

// ToolResult 工具箱一次执行的产物（成功恒走 --output 文件形态）。
type ToolResult struct {
	Output string // 主产物路径（genkey=私钥）
	Extra  string // 次产物/摘要（genkey=公钥；其余=产物规模）
	Err    string
}

// RunTool 执行 base64/crypto 短命令。失败信封（is_success:false）也在
// stdout（ErrorPrint 契约），exit≠0 时 raw 里仍是完整 JSON。
// StartConvert 起一个通用转换任务（media convert / image convert 等两参
// 命令）：arguments 为完整 argv（不含引擎路径）。stderr 进度行与压缩/解压
// 同一管线解析；stdout 信封回读出摘要（m05445：视频/图片转换页）。
func (service *Service) StartConvert(title string, arguments []string, folder, product string) *Task {
	if service.zpackPath() == "" {
		return nil
	}
	task := &Task{
		Title:   title,
		State:   TaskRunning,
		Detail:  "启动中…",
		Folder:  folder,
		Product: product,
		Updates: make(chan struct{}, 8),
	}
	var once sync.Once
	var cmd *exec.Cmd
	task.cancel = func() {
		once.Do(func() {
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			task.State = TaskFailed
			task.Result = "已取消"
			task.notify()
		})
	}
	go service.runConvert(task, &cmd, arguments)
	return task
}

func (service *Service) runConvert(task *Task, cmdPtr **exec.Cmd, arguments []string) {
	cmd := exec.Command(service.zpackPath(), arguments...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	*cmdPtr = cmd
	if err := cmd.Start(); err != nil {
		task.State = TaskFailed
		task.Result = "启动引擎失败：" + err.Error()
		task.notify()
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		var pending []byte
		for {
			read, err := stderr.Read(buffer)
			if read > 0 {
				pending = append(pending, buffer[:read]...)
				for {
					lineBreak := bytesIndex(pending, '\n')
					if lineBreak < 0 {
						break
					}
					line := string(pending[:lineBreak])
					pending = pending[lineBreak+1:]
					if percent, detail, ok := parseProgressLine(line); ok {
						task.Percent = percent
						task.Detail = detail
						task.notify()
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	raw := make([]byte, 0, 4096)
	readBuffer := make([]byte, 8192)
	for {
		read, err := stdout.Read(readBuffer)
		raw = append(raw, readBuffer[:read]...)
		if err != nil {
			break
		}
	}
	_ = cmd.Wait()
	<-done

	if task.State != TaskRunning {
		return // 已取消
	}
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		task.State = TaskDone
		task.Percent = 100
		task.Result = convertSummary(raw)
	} else {
		task.State = TaskFailed
		task.Result = envelopeError(raw, "转换失败（exit "+strconv.Itoa(code)+"）")
	}
	task.notify()
}

// convertSummary 从信封 JSON 提转换摘要（产物名 + 大小）。
func convertSummary(raw []byte) string {
	var envelope struct {
		IsSuccess bool `json:"is_success"`
		Data      struct {
			Output    string `json:"output"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.IsSuccess && envelope.Data.Output != "" {
		return "已输出 " + filepath.Base(envelope.Data.Output) + " · " + humanSize(envelope.Data.SizeBytes)
	}
	return "转换完成"
}

func (service *Service) RunTool(args []string, callback func(ToolResult)) {
	if service.zpackPath() == "" {
		callback(ToolResult{Err: "未找到 zpack 引擎"})
		return
	}
	go func() {
		cmd := exec.Command(service.zpackPath(), args...)
		raw, err := cmd.Output()
		var envelope struct {
			IsSuccess bool   `json:"is_success"`
			ErrorInfo string `json:"error_info"`
			Data      struct {
				Output     string `json:"output"`
				SizeBytes  int    `json:"size_bytes"`
				PublicKey  string `json:"public_key"`
				PrivateKey string `json:"private_key"`
			} `json:"data"`
		}
		parseErr := json.Unmarshal(raw, &envelope)
		if err != nil || parseErr != nil || !envelope.IsSuccess {
			reason := envelope.ErrorInfo
			if reason == "" {
				reason = "命令执行失败"
			}
			callback(ToolResult{Err: reason})
			return
		}
		result := ToolResult{}
		if envelope.Data.PrivateKey != "" { // genkey：两把钥匙都报给用户
			result.Output = envelope.Data.PrivateKey
			result.Extra = envelope.Data.PublicKey
		} else {
			result.Output = envelope.Data.Output
			if envelope.Data.SizeBytes > 0 {
				result.Extra = humanSize(int64(envelope.Data.SizeBytes))
			}
		}
		callback(result)
	}()
}

// HashEntry 单文件哈希结果（hash 页消费）。
type HashEntry struct {
	Path string
	Size int64
	Hash string
}

// HashExec 执行 hash 命令并解析 files 数组；--expect 不匹配时引擎出
// is_success:false + data{is_match:false,...}——判定类失败仍带回完整结果。
func (service *Service) HashExec(args []string, callback func(entries []HashEntry, isMatch *bool, errMsg string)) {
	if service.zpackPath() == "" {
		callback(nil, nil, "未找到 zpack 引擎")
		return
	}
	go func() {
		cmd := exec.Command(service.zpackPath(), args...)
		raw, err := cmd.Output()
		var envelope struct {
			IsSuccess bool   `json:"is_success"`
			ErrorInfo string `json:"error_info"`
			Data      struct {
				Files []struct {
					Path      string `json:"path"`
					SizeBytes int64  `json:"size_bytes"`
					Hash      string `json:"hash"`
				} `json:"files"`
				IsMatch *bool `json:"is_match"`
			} `json:"data"`
		}
		parseErr := json.Unmarshal(raw, &envelope)
		if parseErr != nil {
			reason := "命令执行失败"
			if err != nil && parseErr != nil {
				reason = "哈希结果解析失败"
			}
			callback(nil, nil, reason)
			return
		}
		if len(envelope.Data.Files) == 0 {
			reason := envelope.ErrorInfo
			if reason == "" {
				reason = "命令执行失败"
			}
			callback(nil, nil, reason)
			return
		}
		entries := make([]HashEntry, 0, len(envelope.Data.Files))
		for _, file := range envelope.Data.Files {
			entries = append(entries, HashEntry{Path: file.Path, Size: file.SizeBytes, Hash: file.Hash})
		}
		callback(entries, envelope.Data.IsMatch, "")
	}()
}

// ArchiveEntry 归档内条目（浏览页消费）。
type ArchiveEntry struct {
	Name string
	Size int64
}

// ListResult 归档内容清单。
type ListResult struct {
	Format      string
	EntryCount  int
	TotalBytes  int64
	ArchiveSize int64
	Encrypted   bool
	Entries     []ArchiveEntry
	Err         string
}

// ListArchive 可视化 zpack list——不解压看归档内容清单。
func (service *Service) ListArchive(path string, callback func(ListResult)) {
	if service.zpackPath() == "" {
		callback(ListResult{Err: "未找到 zpack 引擎"})
		return
	}
	go func() {
		cmd := exec.Command(service.zpackPath(), "list", path)
		raw, err := cmd.Output()
		var envelope struct {
			IsSuccess bool   `json:"is_success"`
			ErrorInfo string `json:"error_info"`
			Data      struct {
				Format            string `json:"format"`
				EntryCount        int    `json:"entry_count"`
				TotalUncompressed int64  `json:"total_uncompressed"`
				ArchiveSize       int64  `json:"archive_size"`
				Encrypted         bool   `json:"encrypted"`
				Entries           []struct {
					Name string `json:"name"`
					Size int64  `json:"size"`
				} `json:"entries"`
			} `json:"data"`
		}
		parseErr := json.Unmarshal(raw, &envelope)
		if err != nil || parseErr != nil || !envelope.IsSuccess {
			reason := envelope.ErrorInfo
			if reason == "" {
				reason = "无法读取归档"
			}
			callback(ListResult{Err: reason})
			return
		}
		result := ListResult{
			Format:      envelope.Data.Format,
			EntryCount:  envelope.Data.EntryCount,
			TotalBytes:  envelope.Data.TotalUncompressed,
			ArchiveSize: envelope.Data.ArchiveSize,
			Encrypted:   envelope.Data.Encrypted,
		}
		for _, entry := range envelope.Data.Entries {
			result.Entries = append(result.Entries, ArchiveEntry{Name: entry.Name, Size: entry.Size})
		}
		callback(result)
	}()
}

// TestArchive 归档校验：ok=全部通过；failures=损坏明细（判定类失败也带回数据）。
func (service *Service) TestArchive(path string, callback func(ok bool, tested int, failures []string, errMsg string)) {
	if service.zpackPath() == "" {
		callback(false, 0, nil, "未找到 zpack 引擎")
		return
	}
	go func() {
		cmd := exec.Command(service.zpackPath(), "test", path)
		raw, err := cmd.Output()
		var envelope struct {
			IsSuccess bool   `json:"is_success"`
			ErrorInfo string `json:"error_info"`
			Data      struct {
				TestedEntries int      `json:"tested_entries"`
				AllOk         bool     `json:"all_ok"`
				Failures      []string `json:"failures"`
			} `json:"data"`
		}
		parseErr := json.Unmarshal(raw, &envelope)
		if parseErr != nil {
			reason := "命令执行失败"
			if err == nil && parseErr != nil {
				reason = "校验结果解析失败"
			}
			callback(false, 0, nil, reason)
			return
		}
		if len(envelope.Data.Failures) > 0 || !envelope.IsSuccess {
			callback(false, envelope.Data.TestedEntries, envelope.Data.Failures, envelope.ErrorInfo)
			return
		}
		callback(true, envelope.Data.TestedEntries, nil, "")
	}()
}

// ZpackPath 暴露引擎路径（设置页展示）。
func (service *Service) ZpackPath() string {
	return service.zpackPath()
}

// RunToolText 执行文本态短命令（stdout 非信封：TextPrint 或裸字节），
// 回调给 stdout 全文（去尾换行）；失败时 stdout 里是 error 信封（ErrorPrint 契约）。
func (service *Service) RunToolText(args []string, callback func(text, errMsg string)) {
	if service.zpackPath() == "" {
		callback("", "未找到 zpack 引擎")
		return
	}
	go func() {
		cmd := exec.Command(service.zpackPath(), args...)
		raw, err := cmd.Output()
		if err != nil {
			var envelope struct {
				ErrorInfo string `json:"error_info"`
			}
			if json.Unmarshal(raw, &envelope) == nil && envelope.ErrorInfo != "" {
				callback("", envelope.ErrorInfo)
				return
			}
			callback("", "命令执行失败")
			return
		}
		callback(strings.TrimRight(string(raw), "\n"), "")
	}()
}

// ───────────────────────── 刻录（m06038）─────────────────────────

// BurnDevice 刻录机条目（burn devices 信封镜像）
type BurnDevice struct {
	Index   int    `json:"index"`
	Path    string `json:"path"`
	Vendor  string `json:"vendor,omitempty"`
	Product string `json:"product,omitempty"`
	Media   string `json:"media,omitempty"`
	Status  string `json:"status,omitempty"`
}

// BurnDevices 异步枚举刻录机（zpack burn devices；无光驱=空列表也算成功）
func (service *Service) BurnDevices(callback func(devices []BurnDevice, errMsg string)) {
	if service.zpackPath() == "" {
		callback(nil, "未找到引擎")
		return
	}
	go func() {
		output, err := exec.Command(service.zpackPath(), "burn", "devices").Output()
		if err == nil {
			var envelope struct {
				IsSuccess bool `json:"is_success"`
				Data      struct {
					Devices []BurnDevice `json:"devices"`
				} `json:"data"`
			}
			if json.Unmarshal(output, &envelope) == nil && envelope.IsSuccess {
				devices := envelope.Data.Devices
				if devices == nil {
					devices = []BurnDevice{}
				}
				callback(devices, "")
				return
			}
		}
		callback(nil, "设备枚举失败")
	}()
}

// StartBurn 起刻录任务（inputs=任意数量文件/文件夹——镜像单文件直烧；
// multi=多区段可追加 / mastered=单区段定稿封盘）
func (service *Service) StartBurn(inputs []string, deviceIndex int, speed, mode string, blank bool) *Task {
	if len(inputs) == 0 {
		return nil
	}
	arguments := []string{"burn"}
	arguments = append(arguments, inputs...)
	arguments = append(arguments, "--device", strconv.Itoa(deviceIndex))
	if strings.TrimSpace(speed) != "" {
		arguments = append(arguments, "--speed", strings.TrimSpace(speed))
	}
	arguments = append(arguments, "--mode", mode)
	if blank {
		arguments = append(arguments, "--blank")
	}
	title := "刻录 " + filepath.Base(inputs[0])
	if len(inputs) > 1 {
		title = "刻录 " + strconv.Itoa(len(inputs)) + " 项"
	}
	folder := filepath.Dir(inputs[0])
	product := inputs[0]
	return service.StartConvert(title, arguments, folder, product)
}
