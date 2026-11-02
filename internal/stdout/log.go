// Package stdout 是全 CLI 的输出管线约束层：
//
// 约束一（唯一通道）：最终输出只允许经本包 print 函数——输出即 os.Exit，
// 一条执行路径在结构上不可能打出两次最终输出；禁止任何包裸写 stdout。
// 约束二（流分工）：stdout 只留最终结果（Agent/脚本消费），过程日志走 stderr 可整体忽略。
// 约束三（信封协议）：成功/判定类 = {"is_success":bool,"data":...}；失败 = {"is_success":false,"error_info":"..."}——
// 信封层永远只有这三个键；消费方一行心智：data if is_success else error_info。
package stdout

import (
	"bytes"
	"encoding/json/v2" // 输出层 v2：载荷全面 struct 化（字段序=声明序，确定性）；生产路径禁传 map（键序随进程随机）
	"fmt"
	"os"
	"time"
)

// formatLogTime 时间格式：YYYY-MM-DD HH:MM:SS（本地时间）
func formatLogTime() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// LoggerInfo 输出过程日志到 stderr，格式：[YYYY-MM-DD HH:MM:SS] 消息。
// 无级别标签即常态；标签位为未来的例外级（如 WARN）预留——出现标签本身就是信号
func LoggerInfo(message string) {
	fmt.Fprintf(os.Stderr, "[%s] %s\n", formatLogTime(), message)
}

// envelope 信封结构：三键协议（is_success / error_info? / data?），键序=声明序（确定性）
type envelope struct {
	IsSuccess bool   `json:"is_success"`
	ErrorInfo string `json:"error_info,omitempty"`
	Data      any    `json:"data,omitempty"`
}

// encodeJSONToBuffer 编码进内存缓冲：不转义 HTML、非 ASCII 原样 UTF-8、尾部换行。
// v2 适配：先编码成字节（失败不碰缓冲）——原子性前提（失败零字节）结构性成立。纯函数可单测
func encodeJSONToBuffer(value any) (*bytes.Buffer, error) {
	// Deterministic(true)：即使载荷意外混入 map 也强制键排序——struct 是主保险，这是兜底
	encoded, encodeErr := json.Marshal(value, json.Deterministic(true))
	if encodeErr != nil {
		return &bytes.Buffer{}, encodeErr
	}
	var buffer bytes.Buffer
	buffer.Write(encoded)
	buffer.WriteByte('\n')
	return &buffer, nil
}

// jsonEncode 原子输出：先完整编码进内存，成功后一次性写 stdout——
// 失败路径 stdout 零字节（绝不出现半截 JSON），「stdout 要么完整要么空」结构性成立
func jsonEncode(value any) {
	buffer, encodeErr := encodeJSONToBuffer(value)
	if encodeErr != nil {
		// 编码失败只可能来自不可序列化类型，这里传的都是简单结构，理论不可达
		fmt.Fprintln(os.Stderr, "["+formatLogTime()+"] JSON 编码失败:", encodeErr)
		os.Exit(1)
	}
	fmt.Print(buffer.String())
}

// JSONPrint 成功输出信封 {"is_success":true,"data":...}，exit 0
func JSONPrint(data any) {
	jsonEncode(envelope{IsSuccess: true, Data: data})
	os.Exit(0)
}

// TextPrint 成功输出纯文本到 stdout，exit 0
func TextPrint(text string) {
	fmt.Println(text)
	os.Exit(0)
}

// SuccessPrint 成功输出无载荷信封 {"is_success":true}，exit 0
func SuccessPrint() {
	jsonEncode(envelope{IsSuccess: true})
	os.Exit(0)
}

// ErrorPrint 失败输出 {"is_success":false,"error_info":"..."}，exit 1（与成功侧对称）
func ErrorPrint(errorInfo string) {
	jsonEncode(envelope{IsSuccess: false, ErrorInfo: errorInfo})
	os.Exit(1)
}

// JSONFailPrint 判定类失败：操作完成、结论为否——{"is_success":false,"data":判定字段}，exit 1
// （如 hash --expect 不匹配：退出码给脚本判断，data 里的 is_match/hash/expected 给 GUI 展示）
func JSONFailPrint(data any) {
	jsonEncode(envelope{IsSuccess: false, Data: data})
	os.Exit(1)
}
