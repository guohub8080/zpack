package main

import (
	"fmt"
	"strconv"
	"strings"
)

// 前端校验（2026-11-23 全页面审计第三批）：填错就地红字、禁用主按钮——此前要跳到处理台
// 才看到引擎报错。规则照引擎的解析逐条写（页码规格 pdf.ParsePagesSpec、体积
// cli.parseVolumeSize / parseImageByteSize、码率 cli.parseBitrateStr、采样率
// cli.parseIntStrict + 8000–192000、视频目标 media.ResolveTargetBytes、图片尺寸
// image.ParseNewSizeSpec、期望哈希位数 cli hash --check），**只做语法与范围**；页数、
// 文件是否存在这类要看文件的事仍交给引擎。铁律：这里判错的引擎一定也判错——宁可漏判
// 不可误拦。返回空串 = 合法。

const (
	VOLUME_MIN_BYTES   = 65536  // 分卷下限 64KB（引擎同值）
	BITRATE_MAX_KBPS   = 3200   // 音频码率上限（cli parseBitrateStr 同值）
	SAMPLE_RATE_MIN_HZ = 8000   // 采样率下限（cli audioOptions 同值）
	SAMPLE_RATE_MAX_HZ = 192000 // 采样率上限
	NEW_IMAGE_MAX_PPI  = 2400   // 打印密度上限（cli imageNew 同值；越界引擎会静默回 300）
)

// VOLUME_TEMPLATE_NAMES 是分卷 / 目标体积认识的介质模板名（cli.VOLUME_TEMPLATES 同源）。
var VOLUME_TEMPLATE_NAMES = map[string]bool{
	"gmail": true, "cd650": true, "cd": true, "vcd": true, "fat32": true,
	"dvd": true, "dvd5": true, "dvd9": true, "bd": true, "bd25": true, "bd50": true,
}

// PAPER_NAMES 是新建图片认识的纸型（image.NEW_PAPER_SIZES 同源；可带 L/P 尾缀定方向）。
var PAPER_NAMES = map[string]bool{
	"a0": true, "a1": true, "a2": true, "a3": true, "a4": true, "a5": true,
	"letter": true, "legal": true, "ledger": true,
}

// NAMED_COLORS 是新建图片认识的色名（image.NEW_NAMED_COLORS 同源）。
var NAMED_COLORS = map[string]bool{
	"white": true, "black": true, "red": true, "green": true, "blue": true, "yellow": true,
	"cyan": true, "magenta": true, "gray": true, "grey": true, "orange": true, "purple": true,
	"pink": true, "brown": true,
}

// HASH_HEX_LENGTHS 是各算法期望哈希的十六进制位数（hash --check 的自动判定同表）。
var HASH_HEX_LENGTHS = map[string]int{
	"crc32": 8, "md5": 32, "sha1": 40, "sha224": 56, "sha256": 64, "sha384": 96, "sha512": 128,
}

// visibleIssue 必填项空着时只禁用主按钮、不飘红（一进页面就红一片是噪音）；
// 填了才判对错。
func visibleIssue(issue, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return issue
}

// validatePagesSpec 页码规格：`1-3,5,8-`（1 起闭区间；`8-` 到末页；`-5` 首页到 5）。
// required=false 时空串合法。
func validatePagesSpec(spec string, required bool) string {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		if required {
			return "页码区间必填，如 1-3,5,8-"
		}
		return ""
	}
	for _, token := range strings.Split(trimmed, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			return "页码区间里有空段（检查逗号）"
		}
		bounds := strings.SplitN(token, "-", 2)
		fromText, toText := bounds[0], ""
		if len(bounds) == 2 {
			toText = bounds[1]
		}
		switch {
		case fromText != "" && toText != "": // 闭区间
			if !isPositiveInt(fromText) || !isPositiveInt(toText) {
				return fmt.Sprintf("页码「%s」要用正整数", token)
			}
			if atoi(toText) < atoi(fromText) {
				return fmt.Sprintf("页码区间「%s」起点大于终点", token)
			}
		case fromText != "" && len(bounds) == 2: // 「8-」到末页
			if !isPositiveInt(fromText) {
				return fmt.Sprintf("页码「%s」要用正整数", token)
			}
		case fromText != "": // 单页
			if !isPositiveInt(fromText) {
				return fmt.Sprintf("页码「%s」要用正整数", token)
			}
		case fromText == "" && toText != "": // 「-5」首页到 5
			if !isPositiveInt(toText) {
				return fmt.Sprintf("页码「%s」要用正整数", token)
			}
		default:
			return fmt.Sprintf("页码「%s」看不懂，如 1-3,5,8-", token)
		}
	}
	return ""
}

// parseByteSize 人读体积：正整数 + 可选单位（B/KB/K/MB/M/GB/G），不写单位=字节
// （cli.parseVolumeSize 的数字部分）。
func parseByteSize(text string) (int64, bool) {
	text = strings.ToUpper(strings.TrimSpace(text))
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(text, "KB"), strings.HasSuffix(text, "K"):
		multiplier = 1024
		text = strings.TrimSuffix(strings.TrimSuffix(text, "KB"), "K")
	case strings.HasSuffix(text, "MB"), strings.HasSuffix(text, "M"):
		multiplier = 1024 * 1024
		text = strings.TrimSuffix(strings.TrimSuffix(text, "MB"), "M")
	case strings.HasSuffix(text, "GB"), strings.HasSuffix(text, "G"):
		multiplier = 1024 * 1024 * 1024
		text = strings.TrimSuffix(strings.TrimSuffix(text, "GB"), "G")
	case strings.HasSuffix(text, "B"):
		text = strings.TrimSuffix(text, "B")
	}
	value, parseErr := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if parseErr != nil || value <= 0 {
		return 0, false
	}
	return value * multiplier, true
}

// validateByteSize 体积输入：正整数 + 可选单位，或介质模板名；minimum>0 时不低于它。
func validateByteSize(text string, minimum int64) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "体积必填，如 100MB、2GB、65536"
	}
	if VOLUME_TEMPLATE_NAMES[strings.ToLower(trimmed)] {
		return ""
	}
	size, ok := parseByteSize(trimmed)
	if !ok {
		return "体积要大于 0，如 100MB、2GB、65536"
	}
	if minimum > 0 && size < minimum {
		return fmt.Sprintf("不能小于 %s（分卷下限）", humanSize(minimum))
	}
	return ""
}

// validateBitrate 音频码率：数字 + 可选 k，1–3200 kbps。
func validateBitrate(text string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return ""
	}
	value, parseErr := strconv.ParseInt(strings.TrimSuffix(trimmed, "k"), 10, 64)
	if parseErr != nil || value <= 0 || value > BITRATE_MAX_KBPS {
		return fmt.Sprintf("码率要 1–%d 的数字加 k，如 128k", BITRATE_MAX_KBPS)
	}
	return ""
}

// validateSampleRate 采样率：8000–192000 Hz 的整数（引擎按前缀数字取，这里同款）。
func validateSampleRate(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	value, ok := leadingInt(trimmed)
	if !ok || value < SAMPLE_RATE_MIN_HZ || value > SAMPLE_RATE_MAX_HZ {
		return fmt.Sprintf("采样率要 %d–%d，如 44100", SAMPLE_RATE_MIN_HZ, SAMPLE_RATE_MAX_HZ)
	}
	return ""
}

// validateMediaTarget 视频压缩目标体积：百分比（1–100%）、体积，或介质模板名。
func validateMediaTarget(text string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return "目标体积必填，如 80MB 或 50%"
	}
	if strings.HasSuffix(trimmed, "%") {
		percent, parseErr := strconv.ParseFloat(strings.TrimSuffix(trimmed, "%"), 64)
		if parseErr != nil || percent <= 0 || percent > 100 {
			return "百分比要 1–100，如 50%"
		}
		return ""
	}
	if VOLUME_TEMPLATE_NAMES[trimmed] {
		return ""
	}
	return validateByteSize(trimmed, 0)
}

// validateImageSize 新建图片尺寸：WxH 像素 / WxH + mm|cm|in / 纸型（可带 L/P 尾缀）。
func validateImageSize(text string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return "尺寸必填，如 800x600 / 55x90mm / a4"
	}
	core := trimmed
	if strings.HasSuffix(core, "l") || strings.HasSuffix(core, "p") {
		core = core[:len(core)-1]
	}
	if PAPER_NAMES[core] {
		return ""
	}
	for _, suffix := range []string{"mm", "cm", "in"} {
		if strings.HasSuffix(trimmed, suffix) {
			trimmed = strings.TrimSuffix(trimmed, suffix)
			break
		}
	}
	parts := strings.SplitN(trimmed, "x", 2)
	if len(parts) != 2 {
		return "尺寸格式不对：800x600 / 55x90mm / a4"
	}
	width, widthErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	height, heightErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return "宽高要是正数，如 800x600"
	}
	return ""
}

// validateImageColor 新建图片底色：留空透明 / 色名 / #RRGGBB[AA]。
func validateImageColor(text string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" || trimmed == "transparent" || NAMED_COLORS[trimmed] {
		return ""
	}
	digits := strings.TrimPrefix(trimmed, "#")
	if strings.HasPrefix(trimmed, "#") && (len(digits) == 6 || len(digits) == 8) && isHexDigits(digits) {
		return ""
	}
	return "颜色写色名（red）或 #RRGGBB / #RRGGBBAA"
}

// validatePPI 打印密度：1–2400 的整数。
func validatePPI(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	value, parseErr := strconv.Atoi(trimmed)
	if parseErr != nil || value <= 0 || value > NEW_IMAGE_MAX_PPI {
		return fmt.Sprintf("打印密度要 1–%d 的整数，如 300", NEW_IMAGE_MAX_PPI)
	}
	return ""
}

// validateHexHash 期望哈希：按算法应有的位数，且只能是十六进制。
func validateHexHash(text, algorithm string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return ""
	}
	want, known := HASH_HEX_LENGTHS[strings.ToLower(algorithm)]
	if !known {
		return ""
	}
	if !isHexDigits(trimmed) {
		return "期望值只能是十六进制字符（0-9、a-f）"
	}
	if len(trimmed) != want {
		return fmt.Sprintf("%s 的哈希是 %d 位，现在是 %d 位", strings.ToUpper(algorithm), want, len(trimmed))
	}
	return ""
}

func isPositiveInt(text string) bool {
	value, parseErr := strconv.Atoi(text)
	return parseErr == nil && value >= 1
}

func atoi(text string) int {
	value, _ := strconv.Atoi(text)
	return value
}

// leadingInt 取开头的整数（引擎 parseIntStrict 的 Sscanf 语义）。
func leadingInt(text string) (int, bool) {
	end := 0
	if end < len(text) && (text[end] == '+' || text[end] == '-') {
		end++
	}
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 || (end == 1 && (text[0] == '+' || text[0] == '-')) {
		return 0, false
	}
	value, parseErr := strconv.Atoi(text[:end])
	return value, parseErr == nil
}

func isHexDigits(text string) bool {
	for _, symbol := range text {
		if !strings.ContainsRune("0123456789abcdef", symbol) {
			return false
		}
	}
	return text != ""
}
