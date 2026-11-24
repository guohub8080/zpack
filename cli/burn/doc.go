// Package burn 是光盘刻录功能的命令面：zpack burn（设备枚举 / 镜像或目录刻录）。
//
// 三个平台各用各的后端（2026-11-24 用户定案：各用该平台上最简单、最可靠的做法，不为统一引擎做跨平台移植）：
// Windows = 系统自带 IMAPI2（burn_windows.go + internal/imapi2），macOS = hdiutil、Linux = xorriso（burn.go）；
// 对外始终是同一套旗标与输出契约。行为与设计的权威说明见 specs/burn.md。
package burn
