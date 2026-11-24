// Package cliutil 是命令面的公共底座：只被 cli 及其功能子包引用，放各功能都会用到、
// 但不属于任何一个功能的小件——help 守卫、位置参数切片、过程日志去向、通用载荷。
//
// 与 internal/cmdutil 的分工：cmdutil 是纯逻辑的旗标小件（不依赖 cobra 与输出层，引擎也能引用）；
// cliutil 依赖 cobra 与 internal/stdout，只服务于命令面。
package cliutil

// RestAfterPositional 去掉首个非旗标参数（子命令名），返回其余参数
func RestAfterPositional(args []string, sub string) []string {
	for argIndex, arg := range args {
		if arg == sub {
			rest := append([]string{}, args[:argIndex]...)
			return append(rest, args[argIndex+1:]...)
		}
	}
	return args
}

// FileOutputJSON 通用「写文件成功」载荷（base64 / crypto 等带 --output 的命令共用）
type FileOutputJSON struct {
	Output    string `json:"output"`
	SizeBytes int    `json:"size_bytes"`
}
