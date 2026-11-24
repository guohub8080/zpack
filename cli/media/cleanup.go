// 命令失败后的半成品清理（media / audio 命令面共用）。
package media

import (
	"os"

	engine "github.com/guohub8080/zpack/media"
)

// removeFailedOutput 命令失败后清理半成品。输出与任一输入是同一个文件时不动——
// 就地改写（media compress x.mp4 的缺省输出、audio compress）失败时源文件是完好的，
// 照常「清理」删掉的就是用户的源文件（2026-11-24 实测：media compress 遇到太小的源文件报「目标大小低于下限」，
// 源文件随后消失）。就地改写本身的安全出口在引擎侧（media/inplace.go）。
func removeFailedOutput(inputs []string, output string) {
	for _, inputPath := range inputs {
		if engine.SameFilePath(inputPath, output) {
			return
		}
	}
	os.Remove(output)
}
