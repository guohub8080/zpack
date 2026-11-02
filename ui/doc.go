// Package ui 是 zpack 的 UI 产品面（北极星：单引擎双面孔，见 AGENTS.md）。
// 本目录聚合所有"构建用户界面"的产物——GUI 壳与系统集成入口，规划三种面（路线图 #6）：
//
//  1. GUI 壳（gui/）：图形压缩/解压界面。壳技术选型另议（曾参考过的
//     mygo 纯 Go UI 方案可参考；壳与引擎解耦可随时换）。
//  2. 右键菜单（right-click/）：文件管理器集成——
//     macOS Finder（Services/.workflow 或 FinderSync 扩展）、
//     Windows Explorer（注册表菜单项指向 exe）、Linux（.desktop / nautilus 脚本）。
//  3. 菜单托盘等其余入口按需追加。
//
// 架构铁律：**所有 UI 入口都是"启动器"，统一经 CLI 二进制干活**——菜单/按钮 → 调
// zpack compress/extract → stdout 唯一 JSON 回读结果、stderr 进度可选透出。UI 零引擎
// 代码、进程级隔离、壳技术任意换；引擎已为此净化（core 错误一律 error 返回、日志经
// Logf 回调，不碰 stdout/os.Exit），将来 GUI 需要进程内嵌亦可直连 core 包。
//
// 在各面动工前本包保持规划占位；每开一个面建自己的子目录（gui/、right-click/）。
package ui
