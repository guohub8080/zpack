# zpack

压缩/解压专项 CLI（Go 单二进制，2026-11-02 从 [exkit-cli](../exkit-cli) 剥离孵化）。
单引擎双面孔：stdout 唯一 JSON 契约同时服务 CLI 与未来 GUI 薄壳。zip 并行架构对齐业界主流压缩器，
三方对照：zlib 系 / 7-Zip deflate 系 / zpack=klauspost。

## 命令状态（2026-11-02 扁平化：无格式子命令）

| 命令 | 能力 | 状态 |
|---|---|---|
| `zpack compress` | 多格式打包：写格式由 `--output` 扩展名定（.zip 默认 / .tar / .tar.gz / .tgz）；zip 并行（条目级并行 + >16MB 128KB 分块并行 + 魔数 Store 直通），tar 系 PAX 确定性写入 + 符号链接保留 | ✅ 已验收（91/91） |
| `zpack extract` | 多格式解压：格式靠魔数识别（zip 含 SFX / tar / tar.gz）；智能解压防散落 + GBK 文件名修复 + 路径穿越/符号链接逃逸/设备条目防护 + 硬链接落地 + `--force` | ✅ 已验收 |
| `zpack extract`（7z/rar） | 魔数识别后明确报「暂不支持」（路线图 #2：bodgit/sevenzip + nwaples/rardecode） | ⬜ 路线图 |

## 快速上手

```bash
go build -ldflags "-s -w" -o bin/zpack ./cmd/zpack
./bin/zpack compress ~/项目 --output /tmp/项目.zip      # 或 .tar / .tar.gz / .tgz
./bin/zpack extract /tmp/项目.zip                        # 扩展名无关，魔数识别
bash verify/verify.sh   # 行为门禁 91/91
```

目录分层（产品三面顶层平铺）：`core/` 核心算法引擎（纯库）· `cli/` 命令面 · `ui/` UI 面（GUI 壳 + 右键菜单，路线图 #6；统一经 CLI 契约干活）；`cmd/zpack/` 二进制入口。

规范与路线图见 [AGENTS.md](AGENTS.md)。
