# build_cli/ —— 分平台构建产线

zpack 媒体功能（media convert/compress）走 **cgo 静态链接 libav** 路线：真单文件、
首运行零动作、无运行时解压、无杀软误报面。本目录承载三平台产线。

## 布局

```
build_cli/
├── mac/                ← mac 产线（✅ 已验证）
│   ├── prepare.sh          libav 依赖（POC 阶段用 brew 共享库；静态化命令在脚本内 TODO）
│   └── build.sh            CGO 构建（当前产出 poc/transcode）
├── windows/            ← Windows x86_64 产线（⏳ zig cc 全静态，待首跑验证）
│   ├── prepare.sh          zig cc 交叉编译 libav-win64
│   └── build.sh            CGO 交叉构建 + 铁律 2 的 objdump DLL 验收
├── linux/              ← Linux x86_64 产线（⏳ musl 静态，同 windows 模式）
│   ├── prepare.sh
│   └── build.sh
└── poc/                ← 概念验证（Proof of Concept）——证明 cgo+libav 路线成立的
    │                      一次性脚手架，独立 module 不污染主 go.mod，media 正式
    │                      合入后退役
    └── main.go             go-astiav 进程内转码管线（demux→decode→filter→encode→mux）
```

## POC 验证结论（2026-11-04）

| 项 | 结果 |
|---|---|
| 管线 | demux → decode → filtergraph → encode(H264/AAC) → mux，进程内零子进程 ✅ |
| 码率旋钮 | 请求 1.0Mbps → 实际 1.037Mbps（偏差 3.7%）✅ |
| 压缩效果 | 10.3MB(8.1Mbps) → 1.45MB(1.0Mbps)，7.1× ✅ |
| 音频 | AAC 128k 请求 → 126k 实际 ✅ |
| 产物校验 | ffmpeg 全解码无损 ✅ |
| 环境 | mac arm64 + brew ffmpeg 8.1.2（libavcodec 62）+ go-astiav v0.43.0 |

## 与主构建的关系（铁律 2 修订预案）

- 主 go.mod **不引入** go-astiav——media 代码将放独立 module（本目录模式推广）或
  `//go:build cgo` 标签隔离，保证 `CGO_ENABLED=0` 的一键交叉编译产物不受影响
- 产线产物 = 含 media 的完整版；普通构建 = 纯 Go 版（媒体命令报「需完整版」）
- Windows 产物仍需过 `objdump -p | grep "DLL Name"` 验收（zig cc 全静态预期仅 kernel32 系）
