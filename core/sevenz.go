// 7z 写入引擎：官方 7-Zip C++（Alone2 bundle，DISABLE_RAR=1 剥 RAR）main 改名后进程内
// 直调（ffmpeg 同款模式，internal/sevenzipcli）。压缩与打包策略全交官方实现
// （LZMA/LZMA2 全家 + 多线程），本层只负责四件事：
//  1. 路径坐标系——7z 存名 = 相对 cwd 的路径（绝对路径一律截成 basename 丢目录树），
//     快路径选公共基目录 chdir + @listfile 相对名；散装多根输入兜底 staging 硬链接克隆树
//  2. 密码——-p + -mhe=on（header 加密 = 文件名也加密，7z 相对 zip 的核心优势）
//  3. 进度——7z 非 tty 零输出，Go 侧轮询归档体积 5 秒一报
//  4. stderr——fd2 临时接管（捕获 + 实时转发），退出码 ≥2 时带尾部行报错
package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guohub8080/zpack/internal/sevenzipcli"
)

// sevenzProcessMutex chdir 是进程全局状态——7z 写入全程串行（并发 Compress 共享此锁）
var sevenzProcessMutex sync.Mutex

func sevenzCompress(options CompressOptions, logf Logf) (CompressResult, error) {
	if !sevenzipcli.Available() {
		return CompressResult{}, sevenzipcli.ErrNeedsFullBuild
	}
	ignores := LoadEffectiveZpackignores(options.Inputs)
	tasks, collectErr := collectCompressTasks(options.Inputs, options.Excludes, ignores)
	if collectErr != nil {
		return CompressResult{}, collectErr
	}
	if len(tasks) == 0 {
		return CompressResult{}, fmt.Errorf("没有可压缩的文件（输入为空或全部被排除）")
	}
	var totalInputBytes int64
	for _, task := range tasks {
		totalInputBytes += task.fileInfo.Size()
	}
	// 已存在先删：7z 的 a 对已存在归档是「更新」（增量并入）语义，与本工具「覆盖」契约不符
	if _, statErr := os.Stat(options.Output); statErr == nil {
		if removeErr := os.Remove(options.Output); removeErr != nil {
			return CompressResult{}, fmt.Errorf("覆盖旧归档失败: %s", removeErr.Error())
		}
	}

	workDir, cleanupStage, stageErr := sevenzPrepareWorkDir(tasks, logf)
	if stageErr != nil {
		return CompressResult{}, stageErr
	}
	defer cleanupStage()

	sevenzProcessMutex.Lock()
	defer sevenzProcessMutex.Unlock()
	previousDir, dirErr := os.Getwd()
	if dirErr != nil {
		return CompressResult{}, fmt.Errorf("读取工作目录失败: %s", dirErr.Error())
	}
	if chdirErr := os.Chdir(workDir); chdirErr != nil {
		return CompressResult{}, fmt.Errorf("切换工作目录失败: %s", chdirErr.Error())
	}
	defer func() { _ = os.Chdir(previousDir) }()

	listFile, listErr := os.CreateTemp(workDir, ".zpack-7z-list-*.txt")
	if listErr != nil {
		return CompressResult{}, fmt.Errorf("创建清单失败: %s", listErr.Error())
	}
	listPath := listFile.Name()
	defer os.Remove(listPath)
	entryNames := make([]string, len(tasks))
	for idx, task := range tasks {
		entryNames[idx] = filepath.FromSlash(task.entryName)
	}
	if _, writeErr := listFile.WriteString(strings.Join(entryNames, "\n") + "\n"); writeErr != nil {
		listFile.Close()
		return CompressResult{}, fmt.Errorf("写入清单失败: %s", writeErr.Error())
	}
	listFile.Close()

	absoluteOutput, absErr := filepath.Abs(options.Output)
	if absErr != nil {
		absoluteOutput = options.Output
	}
	args := []string{
		"a", "-bd", "-bso0", "-y",
		"-spd",      // 清单条目按字面量处理（文件名里的 * ? 不当通配符展开）
		"-scsUTF-8", // 清单文件字符集
		"-mx=" + strconv.Itoa(options.Level),
	}
	if options.Password != "" {
		args = append(args, "-p"+options.Password, "-mhe=on")
	}
	if options.PasswordResolver != nil {
		reportLogf(logf, "7z 为整档单密码，逐文件密码表仅对 zip 生效，已忽略")
	}
	if options.Comment != "" {
		reportLogf(logf, "7z 不支持归档注释，已忽略")
	}
	args = append(args, absoluteOutput, "@"+filepath.Base(listPath))

	progressDone := make(chan struct{})
	go func() { // 7z 非 tty 零进度输出——轮询归档体积补进度
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-progressDone:
				return
			case <-ticker.C:
				if info, statErr := os.Stat(absoluteOutput); statErr == nil {
					reportLogf(logf, "压缩中 已写 %s（输入共 %s）", formatBytes(info.Size()), formatBytes(totalInputBytes))
				}
			}
		}
	}()
	exitCode, capturedLines := sevenzipcli.RunCaptured(args)
	close(progressDone)

	if exitCode >= 2 {
		tailLines := capturedLines
		if len(tailLines) > 8 {
			tailLines = tailLines[len(tailLines)-8:]
		}
		detail := strings.Join(tailLines, "；")
		if detail == "" {
			detail = "7z 引擎无输出"
		}
		os.Remove(absoluteOutput) // 中途失败删半成品（zip 同款契约）
		return CompressResult{}, fmt.Errorf("7z 引擎失败（退出码 %d）：%s", exitCode, detail)
	}
	if exitCode == 1 {
		reportLogf(logf, "7z 完成（带警告，详见上方 stderr 输出）")
	}
	outputInfo, statErr := os.Stat(absoluteOutput)
	if statErr != nil {
		return CompressResult{}, fmt.Errorf("读取归档大小失败: %s", statErr.Error())
	}
	return CompressResult{Archive: absoluteOutput, FileCount: len(tasks), SizeBytes: outputInfo.Size()}, nil
}

// sevenzPrepareWorkDir 为 @listfile 选定工作目录：
//   - 快路径：存在公共基目录 B 使每个条目的 rel(B, 源) == 条目名（单目录输入、
//     同目录散文件都是此形态）——直接 chdir(B)，零拷贝
//   - 兜底：临时 staging 目录硬链接克隆整棵目标树（跨设备回退拷贝）——条目名即相对路径
func sevenzPrepareWorkDir(tasks []compressTask, logf Logf) (string, func(), error) {
	// 快路径基目录 = 从任一条目反推：源路径去掉「/条目名」尾巴（目录输入即此形态：
	// src/a.txt 与条目名 src/a.txt → 基目录 = src 的父目录）
	candidateBase := strings.TrimSuffix(tasks[0].sourcePath, string(filepath.Separator)+filepath.FromSlash(tasks[0].entryName))
	fastPathOK := candidateBase != tasks[0].sourcePath
	if fastPathOK {
		for _, task := range tasks {
			relPath, relErr := filepath.Rel(candidateBase, task.sourcePath)
			if relErr != nil || filepath.ToSlash(relPath) != task.entryName {
				fastPathOK = false
				break
			}
		}
	}
	if fastPathOK {
		return candidateBase, func() {}, nil
	}
	reportLogf(logf, "输入为散装多根，已建暂存目录对齐条目名")
	stageRoot, mkErr := os.MkdirTemp("", "zpack-7z-stage-")
	if mkErr != nil {
		return "", nil, fmt.Errorf("创建暂存目录失败: %s", mkErr.Error())
	}
	for _, task := range tasks {
		targetPath := filepath.Join(stageRoot, filepath.FromSlash(task.entryName))
		if mkdirErr := os.MkdirAll(filepath.Dir(targetPath), 0o755); mkdirErr != nil {
			os.RemoveAll(stageRoot)
			return "", nil, mkdirErr
		}
		if linkErr := os.Link(task.sourcePath, targetPath); linkErr == nil {
			continue // 硬链接零拷贝（同卷常态）
		}
		if copyErr := sevenzCopyToStage(task.sourcePath, targetPath); copyErr != nil { // 跨设备回退
			os.RemoveAll(stageRoot)
			return "", nil, copyErr
		}
	}
	return stageRoot, func() { os.RemoveAll(stageRoot) }, nil
}

// sevenzCopyToStage staging 兜底拷贝（硬链接不可用时的跨设备路径）
func sevenzCopyToStage(sourcePath, targetPath string) error {
	sourceFile, openErr := os.Open(sourcePath)
	if openErr != nil {
		return openErr
	}
	defer sourceFile.Close()
	targetFile, createErr := os.Create(targetPath)
	if createErr != nil {
		return createErr
	}
	_, copyErr := io.Copy(targetFile, sourceFile)
	closeErr := targetFile.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
