// Package media 是视频与音频功能的命令面：media（convert / compress / info / preset / bake / 几何变换 / 剪辑类）、
// audio（convert / compress / info）与 ffmpeg 透明代理（ffmpeg.go，全项目唯一不走 JSON 信封的命令）。
//
// 音频与视频共用顶层 media 引擎（本包一律以别名 engine 引入），命令面也共用结果载荷与日志适配，所以同在一个包。
// 行为与设计的权威说明见 specs/media.md（视频）与 specs/audio.md（音频）。
package media
