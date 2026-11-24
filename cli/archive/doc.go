// Package archive 是压缩包功能的命令面：compress / extract / convert / list / test / info / bench。
//
// 引擎在顶层 core 包（本包内一律以别名 engine 引入）。行为与设计的权威说明见 specs/archive.md。
// 一个命令一个文件：compress.go / extract.go / convert.go / list.go（list + test + info）/ bench.go；
// tempdir.go 是 --temp-dir 旗标的统一处理，password_table.go 是 --password-table 的解析。
package archive
