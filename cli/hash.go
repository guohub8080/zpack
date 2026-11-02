// hash 命令：文件哈希计算与校验（md5/sha1/sha224/sha256/sha384/sha512/crc32，纯标准库）。
// 四种形态：
//
//	zpack hash <文件...> [--algorithm 算法]               → 计算哈希（多文件逐条）
//	zpack hash <文件...> --algorithm 算法 --output 清单    → 生成 shasum 兼容校验清单
//	zpack hash <文件> --algorithm 算法 --expect <十六进制> → 单文件校验（匹配 exit 0 / 不匹配 exit 1）
//	zpack hash --check <清单>                             → 批量校验（算法按哈希长度自动判定）
package cli

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/guohub8080/zpack/internal/stdout"
)

// HASH_IO_BLOCK 哈希流式读取块大小（1MB——内存有界，任意大文件）
const HASH_IO_BLOCK = 1 << 20

// hashAlgorithm 哈希算法规格（newHash 建新摘要器；crc32 为 IEEE 多项式——zlib/zip 同款）
type hashAlgorithm struct {
	name      string
	hexLength int
	newHash   func() hash.Hash
}

// HASH_ALGORITHMS 支持的算法表（键为 --algorithm 取值）
var HASH_ALGORITHMS = map[string]hashAlgorithm{
	"md5":    {"md5", 32, func() hash.Hash { return md5.New() }},
	"sha1":   {"sha1", 40, func() hash.Hash { return sha1.New() }},
	"sha224": {"sha224", 56, func() hash.Hash { return sha256.New224() }},
	"sha256": {"sha256", 64, func() hash.Hash { return sha256.New() }},
	"sha384": {"sha384", 96, func() hash.Hash { return sha512.New384() }},
	"sha512": {"sha512", 128, func() hash.Hash { return sha512.New() }},
	"crc32":  {"crc32", 8, func() hash.Hash { return crc32.NewIEEE() }},
}

// hashAlgorithmByHexLength 按十六进制长度反推算法（清单批量校验用；nil = 无法判定）
func hashAlgorithmByHexLength(hexLength int) *hashAlgorithm {
	for key, algo := range HASH_ALGORITHMS {
		if algo.hexLength == hexLength {
			found := HASH_ALGORITHMS[key]
			return &found
		}
	}
	return nil
}

// NewHashCmd hash 命令
func NewHashCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "hash <文件...> [--algorithm 算法] [--expect 值] [--output 清单] [--check 清单]",
		Short:              "文件哈希计算与校验（md5/sha1/sha224/sha256/sha384/sha512/crc32）",
		DisableFlagParsing: true,
		Run:                func(command *cobra.Command, args []string) { helpAwareRun(command, args, runHash) },
	}
}

func runHash(args []string) {
	// 解析参数（值旗标跳过后继值，未知旗标静默忽略）
	algorithmName := "sha256"
	expectValue := ""
	outputManifest := ""
	checkManifest := ""
	var inputPaths []string
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--algorithm":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--algorithm 缺少值")
			}
			// 大小写与连字符归一（SHA-256 → sha256）
			algorithmName = strings.ToLower(strings.ReplaceAll(args[argIndex+1], "-", ""))
			argIndex++
		case arg == "--expect":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--expect 缺少值")
			}
			expectValue = args[argIndex+1]
			argIndex++
		case arg == "--output":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--output 缺少值")
			}
			outputManifest = args[argIndex+1]
			argIndex++
		case arg == "--check":
			if argIndex+1 >= len(args) {
				stdout.ErrorPrint("--check 缺少值")
			}
			checkManifest = args[argIndex+1]
			argIndex++
		case strings.HasPrefix(arg, "-"):
			// 未知旗标静默忽略
		default:
			inputPaths = append(inputPaths, arg)
		}
	}

	algo, known := HASH_ALGORITHMS[algorithmName]
	if !known {
		stdout.ErrorPrint("未知算法: " + algorithmName + "（支持 md5/sha1/sha224/sha256/sha384/sha512/crc32）")
	}

	switch {
	case checkManifest != "":
		if expectValue != "" || outputManifest != "" {
			stdout.ErrorPrint("--check 不能与 --expect/--output 同时使用")
		}
		hashCheckManifest(checkManifest)
	case expectValue != "":
		if outputManifest != "" {
			stdout.ErrorPrint("--expect 不能与 --output 同时使用")
		}
		if len(inputPaths) != 1 {
			stdout.ErrorPrint("--expect 校验需要恰好一个文件")
		}
		hashVerifySingle(inputPaths[0], algo, expectValue)
	case outputManifest != "":
		if len(inputPaths) == 0 {
			stdout.ErrorPrint("--output 生成清单需要至少一个文件")
		}
		hashWriteManifest(inputPaths, algo, outputManifest)
	default:
		if len(inputPaths) == 0 {
			stdout.ErrorPrint("用法: hash <文件...> [--algorithm 算法] [--expect 值] [--output 清单] [--check 清单]")
		}
		hashCompute(inputPaths, algo)
	}
}

// hashFile 流式计算单个文件哈希（1MB 块，内存有界）
func hashFile(path string, algo hashAlgorithm) (string, int64, error) {
	file, openErr := os.Open(path)
	if openErr != nil {
		return "", 0, openErr
	}
	defer file.Close()
	digest := algo.newHash()
	buffer := make([]byte, HASH_IO_BLOCK)
	sizeBytes, copyErr := io.CopyBuffer(digest, file, buffer)
	if copyErr != nil {
		return "", 0, copyErr
	}
	return hex.EncodeToString(digest.Sum(nil)), sizeBytes, nil
}

// hashCompute 计算模式：多文件逐条输出
func hashCompute(inputPaths []string, algo hashAlgorithm) {
	results := make([]hashFileJSON, 0, len(inputPaths))
	for _, inputPath := range inputPaths {
		absolutePath, _ := filepath.Abs(inputPath)
		digestValue, sizeBytes, hashErr := hashStatAndHash(absolutePath, algo)
		if hashErr != "" {
			stdout.ErrorPrint(hashErr)
		}
		results = append(results, hashFileJSON{
			Path: absolutePath, SizeBytes: sizeBytes, Hash: digestValue,
		})
	}
	stdout.JSONPrint(hashComputeJSON{Algorithm: algo.name, Files: results})
}

// hashStatAndHash 路径校验（存在 + 非目录）后计算哈希；返回错误文案（空 = 成功）
func hashStatAndHash(absolutePath string, algo hashAlgorithm) (string, int64, string) {
	info, statErr := os.Stat(absolutePath)
	if statErr != nil {
		return "", 0, "路径不存在：" + absolutePath
	}
	if info.IsDir() {
		return "", 0, "不支持对目录计算哈希：" + absolutePath
	}
	digestValue, sizeBytes, hashErr := hashFile(absolutePath, algo)
	if hashErr != nil {
		return "", 0, "读取文件失败: " + hashErr.Error()
	}
	return digestValue, sizeBytes, ""
}

// hashVerifySingle 单文件校验模式：--expect 与实算比对（匹配 exit 0 / 不匹配 exit 1）
func hashVerifySingle(inputPath string, algo hashAlgorithm, expectValue string) {
	normalized := strings.ToLower(strings.TrimSpace(expectValue))
	if len(normalized) != algo.hexLength {
		stdout.ErrorPrint(fmt.Sprintf("--expect 期望 %d 位十六进制（%s），收到 %d 位", algo.hexLength, algo.name, len(normalized)))
	}
	if _, decodeErr := hex.DecodeString(normalized); decodeErr != nil {
		stdout.ErrorPrint("--expect 不是合法十六进制: " + expectValue)
	}
	absolutePath, _ := filepath.Abs(inputPath)
	digestValue, sizeBytes, hashErr := hashStatAndHash(absolutePath, algo)
	if hashErr != "" {
		stdout.ErrorPrint(hashErr)
	}
	match := digestValue == normalized
	result := hashExpectJSON{
		IsMatch: match, Algorithm: algo.name, Path: absolutePath,
		SizeBytes: sizeBytes, Hash: digestValue, Expected: normalized,
	}
	if match {
		stdout.JSONPrint(result)
	}
	stdout.JSONFailPrint(result)
}

// hashWriteManifest 清单生成模式：shasum 文本格式（<hex><两空格><原路径>），
// 产出可直接被 `shasum -a N -c` / zpack hash --check 消费
func hashWriteManifest(inputPaths []string, algo hashAlgorithm, manifestPath string) {
	var builder strings.Builder
	for _, inputPath := range inputPaths {
		absolutePath, _ := filepath.Abs(inputPath)
		digestValue, _, hashErr := hashStatAndHash(absolutePath, algo)
		if hashErr != "" {
			stdout.ErrorPrint(hashErr)
		}
		builder.WriteString(digestValue + "  " + inputPath + "\n")
	}
	if writeErr := os.WriteFile(manifestPath, []byte(builder.String()), 0o644); writeErr != nil {
		stdout.ErrorPrint("无法写入清单: " + writeErr.Error())
	}
	absoluteManifest, _ := filepath.Abs(manifestPath)
	stdout.JSONPrint(hashManifestJSON{Output: absoluteManifest, Algorithm: algo.name, FileCount: len(inputPaths)})
}

// hashCheckManifest 清单批量校验模式：逐行解析（算法按 hex 长度自动判定），
// 相对路径以清单所在目录为基准；任一不匹配/缺失 → exit 1
func hashCheckManifest(manifestPath string) {
	content, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		stdout.ErrorPrint("无法读取清单: " + readErr.Error())
	}
	absoluteManifest, _ := filepath.Abs(manifestPath)
	baseDir := filepath.Dir(absoluteManifest)

	var mismatched []mismatchRecord
	var missing []string
	checkedCount := 0
	matchedCount := 0

	for lineIndex, rawLine := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		spaceIndex := strings.IndexByte(line, ' ')
		if spaceIndex < 0 {
			stdout.ErrorPrint(fmt.Sprintf("清单第 %d 行无法解析: %s", lineIndex+1, rawLine))
		}
		expectedHex := strings.ToLower(line[:spaceIndex])
		remainder := line[spaceIndex+1:]
		// shasum 分隔形态："  "（文本）或 " *"（二进制）
		if strings.HasPrefix(remainder, "*") || strings.HasPrefix(remainder, " ") {
			remainder = remainder[1:]
		}
		entryPath := remainder
		algo := hashAlgorithmByHexLength(len(expectedHex))
		if algo == nil {
			stdout.ErrorPrint(fmt.Sprintf("清单第 %d 行哈希长度 %d 无法判定算法", lineIndex+1, len(expectedHex)))
		}
		resolvedPath := entryPath
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(baseDir, entryPath)
		}
		digestValue, _, hashErr := hashStatAndHash(resolvedPath, *algo)
		checkedCount++
		if hashErr != "" {
			missing = append(missing, resolvedPath)
			continue
		}
		if digestValue == expectedHex {
			matchedCount++
			continue
		}
		mismatched = append(mismatched, mismatchRecord{
			Path: resolvedPath, Algorithm: algo.name, Expected: expectedHex, Actual: digestValue,
		})
	}

	if mismatched == nil {
		mismatched = []mismatchRecord{}
	}
	if missing == nil {
		missing = []string{}
	}
	allMatch := len(mismatched) == 0 && len(missing) == 0
	result := hashCheckJSON{
		Checked: checkedCount, Matched: matchedCount,
		Mismatched: mismatched, Missing: missing,
	}
	if allMatch {
		stdout.JSONPrint(result)
	}
	stdout.JSONFailPrint(result)
}

// ── hash 输出契约 struct ──

type hashFileJSON struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Hash      string `json:"hash"`
}

type hashComputeJSON struct {
	Algorithm string         `json:"algorithm"`
	Files     []hashFileJSON `json:"files"`
}

type hashExpectJSON struct {
	IsMatch   bool   `json:"is_match"`
	Algorithm string `json:"algorithm"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Hash      string `json:"hash"`
	Expected  string `json:"expected"`
}

type hashManifestJSON struct {
	Output    string `json:"output"`
	Algorithm string `json:"algorithm"`
	FileCount int    `json:"file_count"`
}

type mismatchRecord struct {
	Path      string `json:"path"`
	Algorithm string `json:"algorithm"`
	Expected  string `json:"expected"`
	Actual    string `json:"actual"`
}

type hashCheckJSON struct {
	Checked    int              `json:"checked"`
	Matched    int              `json:"matched"`
	Mismatched []mismatchRecord `json:"mismatched"`
	Missing    []string         `json:"missing"`
}
