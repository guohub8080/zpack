// bench 命令：本机压缩/解压性能基准——测各格式速度 + 各级别比值。
// 生成标准语料（可压文本 + 不可压随机）→ 各格式压缩计时 → 解压计时 → 结果 JSON。
package cli

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/core"
	"github.com/guohub8080/zpack/internal/stdout"
)

// BENCH_DATA_SIZE 基准测试数据量（50MB——够显著又不慢）
const BENCH_DATA_SIZE = 50 << 20

// NewBenchCmd bench 命令
func NewBenchCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "bench [--size MB] [--temp-dir 目录]",
		Short:              "本机压缩/解压性能基准（各格式速度 + 各级别比值）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runBench) },
	}
}

type benchResult struct {
	Format      string  `json:"format"`
	Level       int     `json:"level"`
	CompTime    float64 `json:"compress_seconds"`
	CompSpeed   float64 `json:"compress_mbps"`
	DecompTime  float64 `json:"decompress_seconds"`
	DecompSpeed float64 `json:"decompress_mbps"`
	OrigSize    int64   `json:"original_bytes"`
	CompSize    int64   `json:"compressed_bytes"`
	Ratio       float64 `json:"ratio"`
}

func runBench(args []string) {
	sizeMB := 50
	for argIndex := 0; argIndex < len(args); argIndex++ {
		if args[argIndex] == "--size" && argIndex+1 < len(args) {
			fmt.Sscanf(args[argIndex+1], "%d", &sizeMB)
			if sizeMB < 1 || sizeMB > 2000 {
				stdout.ErrorPrint("--size 需要 1-2000 的整数（MB），收到: " + args[argIndex+1])
			}
			argIndex++
		}
		if args[argIndex] == "--temp-dir" {
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--temp-dir 缺少值（临时目录路径）")
			}
			argIndex++ // 值由 applyTempDirFlag 统一注入
		}
	}

	dir, _ := os.MkdirTemp(applyTempDirFlag(args), "zpack-bench-*")
	srcDir := filepath.Join(dir, "src")
	os.MkdirAll(srcDir, 0o755)

	// 生成语料：50% 可压文本 + 50% 随机数据
	stdout.LoggerInfo(fmt.Sprintf("生成 %dMB 测试语料（50%% 可压 + 50%% 随机）...", sizeMB))
	halfSize := sizeMB << 20 / 2
	textFile := filepath.Join(srcDir, "text.bin")
	f, _ := os.Create(textFile)
	for written := 0; written < halfSize; {
		line := "zpack bench 可压缩文本行 0123456789 abcdefghijklmnopqrstuvwxyz\n"
		f.WriteString(line)
		written += len(line)
	}
	f.Close()
	randomFile := filepath.Join(srcDir, "random.bin")
	f2, _ := os.Create(randomFile)
	buf := make([]byte, 1<<20)
	for written := 0; written < halfSize; {
		if n, _ := cryptoRand.Read(buf); n > 0 {
			f2.Write(buf[:n])
			written += n
		}
	}
	f2.Close()

	var results []benchResult

	// 各格式 × 各级别
	benchFormats := []struct {
		name  string
		ext   string
		level int
	}{
		{"zip", ".zip", 1}, {"zip", ".zip", 6}, {"zip", ".zip", 9},
		{"tar.gz", ".tar.gz", 1}, {"tar.gz", ".tar.gz", 6}, {"tar.gz", ".tar.gz", 9},
		{"tar.gz 并行", ".tar.gz", 6},
		{"tar", ".tar", 0},
	}

	for _, bf := range benchFormats {
		archivePath := filepath.Join(dir, "test"+bf.ext)
		isParallel := bf.name == "tar.gz 并行"
		jobs := 1
		if isParallel {
			jobs = 0 // 默认（触发并行）
		}

		// 压缩
		start := time.Now()
		compResult, compErr := core.Compress(core.CompressOptions{
			Inputs: []string{srcDir}, Output: archivePath, Level: bf.level, Jobs: jobs,
		}, nil)
		compDur := time.Since(start).Seconds()
		if compErr != nil {
			stdout.LoggerInfo(fmt.Sprintf("%s lvl=%d 压缩失败: %v", bf.name, bf.level, compErr))
			continue
		}

		// 解压
		destDir := filepath.Join(dir, "dest")
		os.MkdirAll(destDir, 0o755)
		start = time.Now()
		_, decErr := core.Extract(core.ExtractOptions{Archive: archivePath, OutputDir: destDir, Force: true}, nil)
		decDur := time.Since(start).Seconds()
		if decErr != nil {
			_ = decErr // 解压失败不影响比值数据
		}
		os.RemoveAll(destDir)
		os.Remove(archivePath)

		origSize := int64(sizeMB) << 20
		speed := float64(origSize) / (1 << 20) / compDur
		decSpeed := float64(origSize) / (1 << 20) / decDur
		results = append(results, benchResult{
			Format: bf.name, Level: bf.level,
			CompTime: compDur, CompSpeed: speed,
			DecompTime: decDur, DecompSpeed: decSpeed,
			OrigSize: origSize, CompSize: compResult.SizeBytes,
			Ratio: float64(compResult.SizeBytes) / float64(origSize),
		})
		stdout.LoggerInfo(fmt.Sprintf("%s lvl=%d: 压缩 %.1fMB/s (%.2fs) 解压 %.1fMB/s (%.2fs) 比值 %.2f%%",
			bf.name, bf.level, speed, compDur, decSpeed, decDur,
			float64(compResult.SizeBytes)/float64(origSize)*100))
	}

	os.RemoveAll(dir) // 显式清理：stdout.JSONPrint 内部 os.Exit，defer 不会运行
	stdout.JSONPrint(benchSummaryJSON{
		DataSizeMB: sizeMB, CpuCores: runtimeNumCPU(), Results: results,
	})
}

func runtimeNumCPU() int { return runtime.NumCPU() }
func init()              { cryptoRand = rand.Reader }

var cryptoRand interface{ Read([]byte) (int, error) }
