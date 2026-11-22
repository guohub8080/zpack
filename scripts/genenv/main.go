// genenv —— realEnvTest 真实环境测试数据一键生成器（2026-11-22 用户定案 m06340）。
//
// 定位：verify 门禁测的是确定性小样本；本目录测的是「真实体感」——几 GB 随机数据
// 观测进度条、真实视频/音频/图片走转换压缩、加密/分卷归档走解压台归档浏览。
// 产物全部算法生成（可复现、零下载），落 realEnvTest/（gitignore，不入库）。
//
// 用法：
//
//	go run ./scripts/genenv            # 标准集（≈3.4GB：3×1G 随机 + 语料 + 媒体 + 归档）
//	go run ./scripts/genenv --full     # 追加 rand-5g.bin（观测长进度条）
//	go run ./scripts/genenv --fast     # 只生成小件（<300MB，秒级冒烟）
//	go run ./scripts/genenv --clean    # 删除 realEnvTest/ 整树
//
// 媒体族依赖 bin/zpack 完整版（进程内 ffmpeg 直调）；纯 Go 件（语料/PDF/归档）无依赖。
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/guohub8080/zpack/core"
)

var (
	root  string // realEnvTest/
	zpack string // bin/zpack 完整版路径
)

func main() {
	full := flag.Bool("full", false, "追加 5GB 单文件随机数据")
	fast := flag.Bool("fast", false, "只生成小件（<300MB）")
	clean := flag.Bool("clean", false, "删除 realEnvTest/ 整树")
	flag.Parse()

	repoRoot, err := os.Getwd()
	must(err)
	if filepath.Base(repoRoot) != "zpack" || !dirExists(filepath.Join(repoRoot, "core")) {
		fatal("请在仓库根目录运行：go run ./scripts/genenv")
	}
	root = filepath.Join(repoRoot, "realEnvTest")
	zpack = filepath.Join(repoRoot, "bin", "zpack")

	if *clean {
		_ = os.RemoveAll(root)
		fmt.Println("已清理 " + root)
		return
	}

	started := time.Now()
	must(os.MkdirAll(root, 0o755))
	genCorpus(*fast)
	if !*fast {
		genMedia()
		genArchives()
	}
	if *full {
		genRandFile(filepath.Join(root, "corpus", "rand-5g.bin"), 5<<30)
	}
	fmt.Printf("\n完成（%s）——目录结构：\n", time.Since(started).Round(time.Millisecond))
	printTree(root, "", 2)
}

// ── 语料 ─────────────────────────────────────────────────────────────────────

func genCorpus(fast bool) {
	corpusDir := filepath.Join(root, "corpus")
	must(os.MkdirAll(corpusDir, 0o755))

	if fast {
		genRandFile(filepath.Join(corpusDir, "rand-256m.bin"), 256<<20)
	} else {
		for idx := 1; idx <= 3; idx++ {
			genRandFile(filepath.Join(corpusDir, fmt.Sprintf("rand-%dg.bin", idx)), 1<<30)
		}
	}

	// 可压文本（重复语料——压缩比值好看，文本行带序号保证非完全重复）
	textPath := filepath.Join(corpusDir, "text-200m.txt")
	if !fileExists(textPath) {
		textFile, err := os.Create(textPath)
		must(err)
		source := rand.New(rand.NewSource(42))
		phrases := []string{
			"压缩引擎行为对齐业界主流：条目级并行 + 分块字典衔接。",
			"The quick brown fox jumps over the lazy dog, again and again.",
			"分卷创建走格式无关字节切分，cat 即还原；整档不超单卷保持原名。",
			"WinZip AES-256 AE-2: salt16 + verify2 + CTR stream + 10B HMAC tail.",
		}
		written := int64(0)
		var chunk strings.Builder
		for written < 200<<20 {
			chunk.Reset()
			for line := 0; line < 4096; line++ {
				fmt.Fprintf(&chunk, "%08d %s\n", source.Int63(), phrases[source.Intn(len(phrases))])
			}
			block := []byte(chunk.String())
			_, err = textFile.Write(block)
			must(err)
			written += int64(len(block))
		}
		must(textFile.Close())
		fmt.Printf("✓ text-200m.txt（可压文本）\n")
	}

	// 混合目录树：中文名/空格名/子目录/大小文件混排——压缩台拖目录主场景
	mixedDir := filepath.Join(corpusDir, "项目资料")
	samples := map[string]string{
		filepath.Join(mixedDir, "季度报表.xlsx"):               strings.Repeat("X", 300<<10),
		filepath.Join(mixedDir, "设计稿 v2.psd"):              strings.Repeat("P", 640<<10),
		filepath.Join(mixedDir, "video-export.mp4"):        strings.Repeat("V", 12<<20),
		filepath.Join(mixedDir, "子目录", "说明.txt"):           "zpack realEnvTest 混合夹具\n",
		filepath.Join(mixedDir, "子目录", "data.bin"):         randomString(2 << 20),
		filepath.Join(mixedDir, "空格 文件 名.md"):              "# 标题\n\n文件名带空格的路径支持。\n",
		filepath.Join(mixedDir, "英文-note.md"):              "mixed corpus note\n",
		filepath.Join(mixedDir, "nested", "deep", "深.txt"): "嵌套三层\n",
	}
	for path, content := range samples {
		if fileExists(path) {
			continue
		}
		must(os.MkdirAll(filepath.Dir(path), 0o755))
		must(os.WriteFile(path, []byte(content), 0o644))
	}
	fmt.Printf("✓ 项目资料/（混合目录树 8 件）\n")

	// 空输出落点：GUI 观察产物专用，不污染别处
	must(os.MkdirAll(filepath.Join(root, "out"), 0o755))
}

// genRandFile 确定性伪随机大文件（math/rand PRNG——比 /dev/urandom 快数倍且可复现）
func genRandFile(path string, size int64) {
	if fileExists(path) {
		fmt.Printf("· %s 已存在，跳过\n", filepath.Base(path))
		return
	}
	started := time.Now()
	source := rand.New(rand.NewSource(0x7A706163)) // "zpac"——固定种子，产物可复现
	buffer := make([]byte, 1<<20)
	out, err := os.Create(path)
	must(err)
	var written int64
	for written < size {
		source.Read(buffer)
		writeLen := int64(len(buffer))
		if remaining := size - written; remaining < writeLen {
			writeLen = remaining
		}
		_, err = out.Write(buffer[:writeLen])
		must(err)
		written += writeLen
	}
	must(out.Close())
	fmt.Printf("✓ %s（%s，%.0f MB/s）\n", filepath.Base(path), humanSize(size),
		float64(size)/(1<<20)/time.Since(started).Seconds())
}

// ── 媒体（依赖 bin/zpack 完整版进程内 ffmpeg）────────────────────────────────

func genMedia() {
	if !fileExists(zpack) {
		fmt.Printf("· 跳过媒体族：未找到 %s（先跑 build_cli/mac/build.sh 铸完整版）\n", zpack)
		return
	}
	mediaDir := filepath.Join(root, "media")
	must(os.MkdirAll(mediaDir, 0o755))

	type mediaJob struct {
		path string
		args []string
		note string
	}
	jobs := []mediaJob{
		{filepath.Join(mediaDir, "video-720p-60s.mp4"), []string{
			"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30:duration=60",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=60",
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-c:a", "aac", "-shortest",
		}, "视频转换/压缩/信息"},
		{filepath.Join(mediaDir, "video-rotated.mp4"), []string{
			"-i", filepath.Join(mediaDir, "video-720p-60s.mp4"),
			"-c", "copy", "-metadata:s:v:0", "rotate=90",
		}, "旋转/bake（display-matrix）"},
		{filepath.Join(mediaDir, "audio-tone-320k.mp3"), []string{
			"-f", "lavfi", "-i", "sine=frequency=440:duration=30",
			"-c:a", "libmp3lame", "-b:a", "320k",
		}, "音频转换/压缩"},
		{filepath.Join(mediaDir, "photo-6000x4000.jpg"), []string{
			"-f", "lavfi", "-i", "testsrc2=size=6000x4000:rate=1:duration=1",
			"-frames:v", "1", "-q:v", "2",
		}, "图片压缩（大像素）"},
		{filepath.Join(mediaDir, "photo-transparent.png"), []string{
			"-f", "lavfi", "-i", "color=c=black@0.0:s=800x600,format=rgba",
			"-frames:v", "1",
		}, "透明 alpha 转换"},
	}
	for _, job := range jobs {
		if fileExists(job.path) {
			fmt.Printf("· %s 已存在，跳过\n", filepath.Base(job.path))
			continue
		}
		// 先查视频源在不在（rotated 依赖第一个产物）
		if strings.Contains(strings.Join(job.args, " "), "video-720p-60s.mp4") && !fileExists(filepath.Join(mediaDir, "video-720p-60s.mp4")) {
			continue
		}
		started := time.Now()
		fullArgs := append([]string{"ffmpeg"}, job.args...)
		fullArgs = append(fullArgs, job.path)
		out, err := exec.Command(zpack, fullArgs...).CombinedOutput()
		if err != nil {
			fmt.Printf("· %s 生成失败（%v）：%s\n", filepath.Base(job.path), err, lastLine(out))
			continue
		}
		fmt.Printf("✓ %s（%s，%s）\n", filepath.Base(job.path), humanSize(fileSize(job.path)), time.Since(started).Round(time.Millisecond))
	}
	_ = jobs[len(jobs)-1].note
}

// ── 归档（zpack 自产：加密/分卷/多格式——解压台+归档浏览场景）────────────────

func genArchives() {
	archiveDir := filepath.Join(root, "archives")
	must(os.MkdirAll(archiveDir, 0o755))
	corpusDir := filepath.Join(root, "corpus")
	mixedDir := filepath.Join(corpusDir, "项目资料")

	encrypt := func(name string, opts core.CompressOptions) {
		target := filepath.Join(archiveDir, name)
		if fileExists(target) || fileExists(target+".001") {
			fmt.Printf("· %s 已存在，跳过\n", name)
			return
		}
		result, err := core.Compress(opts, nil)
		must(err)
		note := humanSize(result.SizeBytes)
		if glob, globErr := filepath.Glob(target + ".*"); globErr == nil && len(glob) > 0 {
			note += fmt.Sprintf("（%d 卷）", len(glob))
		}
		fmt.Printf("✓ %s（%s）\n", name, note)
	}

	encrypt("机密归档.zip", core.CompressOptions{
		Inputs:   []string{mixedDir},
		Output:   filepath.Join(archiveDir, "机密归档.zip"),
		Password: "test1234",
	})
	encrypt("项目备份.zip", core.CompressOptions{ // 分卷：两份 1G 随机源 → 1GB 卷切 .001/.002
		Inputs:     []string{filepath.Join(corpusDir, "rand-1g.bin"), filepath.Join(corpusDir, "rand-2g.bin")},
		Output:     filepath.Join(archiveDir, "项目备份.zip"),
		VolumeSize: 1 << 30,
	})
	encrypt("文档集.tar.gz", core.CompressOptions{
		Inputs: []string{mixedDir},
		Output: filepath.Join(archiveDir, "文档集.tar.gz"),
	})
}

// ── 小件 ─────────────────────────────────────────────────────────────────────

func randomString(size int) string {
	source := rand.New(rand.NewSource(7))
	buffer := make([]byte, size)
	source.Read(buffer)
	return string(buffer)
}

func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }
func dirExists(path string) bool  { info, err := os.Stat(path); return err == nil && info.IsDir() }
func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.0fMB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}
func printTree(dir, prefix string, maxDepth int) {
	if maxDepth < 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for idx, entry := range entries {
		branch := "├── "
		if idx == len(entries)-1 {
			branch = "└── "
		}
		sizeNote := ""
		if !entry.IsDir() {
			sizeNote = "  " + humanSize(fileSize(filepath.Join(dir, entry.Name())))
		}
		fmt.Printf("%s%s%s%s\n", prefix, branch, entry.Name(), sizeNote)
		if entry.IsDir() {
			next := prefix + "│   "
			if idx == len(entries)-1 {
				next = prefix + "    "
			}
			printTree(filepath.Join(dir, entry.Name()), next, maxDepth-1)
		}
	}
}

func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}
func fatal(message string) {
	fmt.Fprintln(os.Stderr, "genenv: "+message)
	os.Exit(1)
}
