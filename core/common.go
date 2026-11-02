// 格式间共享的管线逻辑：条目收集/排除、安全判定（路径穿越/GBK 修复/智能解压）、进度报告。
package core

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/guohub8080/zpack/internal/cmdutil"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// PROGRESS_INTERVAL 进度日志最小间隔（过程信息，可被调用方整体忽略）
const PROGRESS_INTERVAL = 5 * time.Second

// defaultExcludedNames 系统垃圾文件：默认排除，不入包
var defaultExcludedNames = []string{".DS_Store", "__MACOSX", "Thumbs.db", "desktop.ini"}

// isExcludedName 是否命中默认垃圾清单、`._` 前缀（macOS 资源叉）或用户排除名单
func isExcludedName(name string, excludeItems []string) bool {
	return isExcludedPath(name, false, excludeItems)
}

// isExcludedPath 排除判定（支持 glob）：精确名匹配 + glob 模式（*.bak / dir/ / **/test/**）
func isExcludedPath(relPath string, isDir bool, excludeItems []string) bool {
	name := relPath
	if idx := strings.LastIndexByte(relPath, '/'); idx >= 0 {
		name = relPath[idx+1:]
	}
	if strings.HasPrefix(name, "._") {
		return true
	}
	for _, defaultItem := range defaultExcludedNames {
		if name == defaultItem {
			return true
		}
	}
	for _, excludeItem := range excludeItems {
		// 统一走 glob 匹配（内部处理精确名、尾部/目录限定、*?[ glob——语义一致零歧义）
		if cmdutil.MatchGlob(relPath, isDir, excludeItem) {
			return true
		}
	}
	return false
}

// compressTask 待压缩条目：源路径 + 包内名（collect 阶段确定，顺序即包内顺序）
type compressTask struct {
	taskIndex  int
	sourcePath string
	entryName  string
	fileInfo   fs.FileInfo
}

// collectCompressTasks 收集待压缩条目：目录以目录名为根前缀递归、多文件按文件名平铺进
// 包根、排除规则同源、目录项不写入（空目录不保留）。顺序即包内顺序（确定性）。
func collectCompressTasks(inputPaths []string, excludeItems []string, ignores []*ZpackIgnore) ([]compressTask, error) {
	var tasks []compressTask
	for _, inputPath := range inputPaths {
		absoluteInput, absErr := filepath.Abs(inputPath)
		if absErr != nil {
			absoluteInput = inputPath
		}
		inputInfo, statErr := os.Stat(absoluteInput)
		if statErr != nil {
			return nil, fmt.Errorf("路径不存在：%s", absoluteInput)
		}
		if !inputInfo.IsDir() {
			tasks = append(tasks, compressTask{
				taskIndex:  len(tasks),
				sourcePath: absoluteInput,
				entryName:  filepath.Base(absoluteInput),
				fileInfo:   inputInfo,
			})
			continue
		}
		parentDir := filepath.Dir(absoluteInput)
		walkErr := filepath.WalkDir(absoluteInput, func(currentPath string, dirEntry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			// .zpackignore 文件本身始终排除
			if dirEntry.Name() == ZPACKIGNORE_FILENAME {
				if dirEntry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			// .zpackignore 规则匹配（相对输入目录根的路径）
			if currentPath != absoluteInput && len(ignores) > 0 {
				relPath, relErr := filepath.Rel(absoluteInput, currentPath)
				if relErr == nil {
					if ZpackIgnoreExcludes(ignores, relPath, dirEntry.IsDir()) {
						if dirEntry.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
				}
			}
			// 根目录本身不参与排除判断（glob 匹配用相对路径）
			relInRoot, relErr := filepath.Rel(absoluteInput, currentPath)
			if relErr != nil {
				relInRoot = dirEntry.Name()
			}
			if currentPath != absoluteInput && isExcludedPath(relInRoot, dirEntry.IsDir(), excludeItems) {
				if dirEntry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if dirEntry.IsDir() {
				return nil // 目录项不写入，只写文件；空目录不保留
			}
			entryInfo, infoErr := dirEntry.Info()
			if infoErr != nil {
				return infoErr
			}
			relativePath, relErr := filepath.Rel(parentDir, currentPath)
			if relErr != nil {
				return relErr
			}
			tasks = append(tasks, compressTask{
				taskIndex:  len(tasks),
				sourcePath: currentPath,
				entryName:  relativePath,
				fileInfo:   entryInfo,
			})
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("读取目录失败: %s", walkErr.Error())
		}
	}
	return tasks, nil
}

// isUnsafeEntryPath 防路径穿越（zip-slip/tar-slip 同源）：反斜杠归一后含绝对路径、
// 盘符或 .. 段一律拒绝
func isUnsafeEntryPath(entryName string) bool {
	slashName := strings.ReplaceAll(entryName, "\\", "/")
	if strings.HasPrefix(slashName, "/") || path.IsAbs(slashName) {
		return true
	}
	for _, segment := range strings.Split(slashName, "/") {
		if segment == ".." {
			return true
		}
		if len(segment) == 2 && segment[1] == ':' { // Windows 盘符（C:）
			return true
		}
	}
	return false
}

// decodeEntryName 条目名解码：EFS 置位按 UTF-8 原样；未置位且非有效 UTF-8 按 GBK 解码
// （中文 Windows 系统压缩工具产出的归档不带 EFS、名字是 GBK 字节——tar 无 EFS 位，
// 传 efsSet=false 走同一启发式。GBK 解码器对非法字节一律替换 U+FFFD 不报错——
// 非 GBK 也非 UTF-8 的名字尽力解码，属启发式的已知局限）
func decodeEntryName(entryName string, efsSet bool) (decodedName string, converted bool) {
	if efsSet || utf8.ValidString(entryName) {
		return entryName, false
	}
	decodedName, _ = simplifiedchinese.GBK.NewDecoder().String(entryName)
	return decodedName, true
}

// scannedEntry 预扫后的归档条目（格式中立：zip/tar 解压共用智能解压判定）
type scannedEntry struct {
	cleanName  string
	isDirEntry bool
}

// singleRootName 智能解压判定（unar 语义）：全部条目共享同一顶层目录名 → 返回该名字（单根，
// 解到当前目录恰好只出现一个新目录）；根上有散装文件或多个根 → 返回空串（调用方应包一层根目录防散落）
func singleRootName(entries []scannedEntry) string {
	singleRoot := ""
	hasEntry := false
	for _, entry := range entries {
		if entry.cleanName == "" {
			continue
		}
		firstSegment, _, hasSlash := strings.Cut(entry.cleanName, "/")
		if !entry.isDirEntry && !hasSlash {
			return "" // 根上有散装文件：解开会散落一地
		}
		if singleRoot == "" {
			singleRoot = firstSegment
		} else if singleRoot != firstSegment {
			return ""
		}
		hasEntry = true
	}
	if !hasEntry {
		return ""
	}
	return singleRoot
}

// resolveTargetRoot 智能解压定根（zip/tar 共用）：显式目录直接用；单根归档落当前目录
// （条目自带根目录）；散装归档包一层以归档基名命名的根目录（防解压散落一地）
func resolveTargetRoot(outputDirPath string, archivePath string, entries []scannedEntry) (string, error) {
	if outputDirPath != "" {
		return outputDirPath, nil
	}
	currentDir, cwdErr := os.Getwd()
	if cwdErr != nil {
		return "", fmt.Errorf("获取当前目录失败: %s", cwdErr.Error())
	}
	if singleRootName(entries) != "" {
		return currentDir, nil
	}
	return filepath.Join(currentDir, ArchiveBaseName(filepath.Base(stripSplitSuffix(archivePath)))), nil
}

// prepareExtractRoot 智能解压定根 + 建目录（zip/tar/7z/rar 管线共用小件）
func prepareExtractRoot(options ExtractOptions, displayPath string, rootEntries []scannedEntry) (string, error) {
	targetRoot, rootErr := resolveTargetRoot(options.OutputDir, displayPath, rootEntries)
	if rootErr != nil {
		return "", rootErr
	}
	absoluteTargetRoot, absErr := filepath.Abs(targetRoot)
	if absErr != nil {
		absoluteTargetRoot = targetRoot
	}
	if mkdirErr := os.MkdirAll(absoluteTargetRoot, 0o755); mkdirErr != nil {
		return "", fmt.Errorf("创建输出目录失败: %s", mkdirErr.Error())
	}
	return absoluteTargetRoot, nil
}

// progressReporter 并行管线进度计数（工作协程原子更新，报告协程周期经 logf 输出）。
// 压缩侧跟踪输入字节（读盘量）、解压侧跟踪输出字节（写盘量）——速度与 ETA 据此计算。
// 进度条格式：████████░░░░ 45.2% | 1.2GB/2.7GB | 89MB/s | 剩余 1分23秒
type progressReporter struct {
	doneFiles  atomic.Int64
	doneBytes  atomic.Int64
	totalFiles int64
	totalBytes int64 // 总字节（压缩=输入总量；解压=输出总量；0=不展示百分比）
	inputBytes int64 // 压缩侧：总输入字节（源文件大小之和，用于输入进度）
	logf       Logf  // nil = 静默（库形态调用方不需要日志）
	startTime  time.Time
}

// newProgressReporter 构造进度计数器
func newProgressReporter(totalFiles int64, totalBytes int64, logf Logf) *progressReporter {
	return &progressReporter{totalFiles: totalFiles, totalBytes: totalBytes, logf: logf, startTime: time.Now()}
}

// SetInputBytes 压缩侧设定总输入字节（收集任务后调用）
func (state *progressReporter) SetInputBytes(total int64) {
	state.inputBytes = total
}

// renderProgressBar 生成 Unicode 进度条
func renderProgressBar(percent float64, width int) string {
	filled := int(percent * float64(width))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return bar
}

// formatBytes 人类可读字节
func formatBytes(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// formatDuration 人类可读时长
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d分%d秒", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d小时%d分", int(d.Hours()), int(d.Minutes())%60)
}

// start 起一个周期进度报告协程，返回停止函数（defer 调用）。
// 带总量时输出进度条+速度+ETA；无总量时退化为计数模式
func (state *progressReporter) start(label string) func() {
	stopChannel := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(PROGRESS_INTERVAL)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if state.logf == nil {
					continue
				}
				doneFiles := state.doneFiles.Load()
				doneBytes := state.doneBytes.Load()
				elapsed := time.Since(state.startTime)
				speed := float64(doneBytes) / elapsed.Seconds()

				// 有总量 → 进度条模式
				totalForPercent := state.totalBytes
				if totalForPercent == 0 && state.inputBytes > 0 {
					totalForPercent = state.inputBytes // 压缩侧用输入字节算百分比
				}
				if totalForPercent > 0 {
					percent := float64(doneBytes) / float64(totalForPercent) * 100
					if percent > 100 {
						percent = 100
					}
					bar := renderProgressBar(percent/100, 30)
					if speed > 0 && percent < 100 {
						remaining := time.Duration(float64(totalForPercent-doneBytes) / speed * float64(time.Second))
						state.logf("%s %s %.1f%% | %s/%s | %s/s | %d/%d 文件 | 剩余 %s",
							label, bar, percent,
							formatBytes(doneBytes), formatBytes(totalForPercent),
							formatBytes(int64(speed)),
							doneFiles, state.totalFiles,
							formatDuration(remaining))
					} else {
						state.logf("%s %s %.1f%% | %s/%s | %d/%d 文件",
							label, bar, percent,
							formatBytes(doneBytes), formatBytes(totalForPercent),
							doneFiles, state.totalFiles)
					}
				} else {
					// 无总量 → 简单计数
					state.logf("%s: %d/%d 个文件，已处理 %s",
						label, doneFiles, state.totalFiles, formatBytes(doneBytes))
				}
			case <-stopChannel:
				return
			}
		}
	}()
	return func() {
		close(stopChannel)
		<-stopped
	}
}
