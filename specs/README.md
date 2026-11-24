# specs/ — zpack 各功能的规格

每个功能一份 spec，是这个功能**行为与设计的权威说明**。看 spec 就能知道一个功能现在到底怎么表现、为什么这样设计；改 spec 就能让 agent 把代码同步过来。

分工：[AGENTS.md](../AGENTS.md) 管全局铁律与协作规范（验收、依赖准入、命名、Git）；`specs/` 管各功能的行为、契约与设计。[conventions.md](conventions.md) 管跨功能的输出契约与命令行约定。

## 目录

| spec | 功能 | 覆盖的命令 | 命令面 | 引擎 | 验收 |
|---|---|---|---|---|---|
| [conventions.md](conventions.md) | 全局约定（所有命令） | 全部 | `internal/stdout`、`internal/cmdutil`、`internal/cliutil`、`cli/root.go` | — | `verify/verify.sh` |
| [archive.md](archive.md) | 压缩包 | `compress` `extract` `convert` `list` `test` `info` `bench` | `cli/archive` | `core`（+ `internal/sevenzipcli`） | `verify/verify.sh` |
| [burn.md](burn.md) | 光盘刻录 | `burn` | `cli/burn` | Windows `internal/imapi2`；macOS hdiutil；Linux xorriso | `verify/verify.sh`（有刻录机时跳过真实写入） |
| [crypto.md](crypto.md) | 加密与校验 | `crypto` `hash` | `cli/crypto` | 标准库 | `verify/verify.sh` |
| [image.md](image.md) | 图片 | `image` | `cli/image` | `image` | `verify/verify-image.sh` |
| [audio.md](audio.md) | 音频 | `audio` | `cli/media` | `media` | `verify/verify-media.sh` |
| [media.md](media.md) | 视频、ffmpeg 透明代理 | `media` `ffmpeg` | `cli/media` | `media`（+ `internal/ffmpegcli`） | `verify/verify-media.sh` |
| [office.md](office.md) | 文档格式转换 | `office` | `cli/office` | `office` | 暂无自动化验收（见 office.md 未结项） |
| [pdf.md](pdf.md) | PDF | `pdf` | `cli/pdf` | `pdf` | `verify/verify.sh` |
| [tools.md](tools.md) | 零依赖消费层 | `base64` `json` `skill` | `cli/tools` | 标准库 | `verify/verify.sh` |
| [gui.md](gui.md) | GUI 与右键菜单 | — | `ui/` | 经 CLI 二进制契约 | `ui/gui` 无头测试 |
| [build.md](build.md) | 构建与验收 | — | `build_cli/`、`verify/`、`cmd/zpack` | — | — |

音频与视频共用一个引擎包和一个命令面包（`cli/media`），但各有一份 spec。

## 改 spec → 同步代码

1. **改 spec**：只动相关小节，并在文末「变更记录」加一行（日期、改了什么；「同步的代码」一栏先留空）。
2. **让 agent 同步**：说清「按 `specs/<功能>.md` 的第几节同步代码」。agent 的工作顺序是——读 spec 与对应代码 → 改代码 → 补验收用例（验收全绿才算实现完）→ 跑测试 → 本地提交 → 把提交号回填到「变更记录」。
3. **两边不一致时**：spec 是有意改的，就改代码；spec 过时了，就先修 spec 再动代码。不允许代码和 spec 各说各话。

新增功能：复制 [_template.md](_template.md) 起一份，在 AGENTS.md 里补一行入口，在上面的目录表里登记。

## 每份 spec 怎么读

所有功能 spec 都按 [_template.md](_template.md) 的结构写：

| 小节 | 回答的问题 |
|---|---|
| 1 功能定义 | 做什么、不做什么 |
| 2 命令与旗标 | 怎么调用，每个旗标的取值、校验、缺省 |
| 3 输出契约 | stdout 的 JSON 字段、error_info 的取值、stderr 日志 |
| 4 行为细则 | 每个场景下输入什么、做什么、输出什么、出错时怎样 |
| 5 平台差异 | Windows / macOS / Linux 的不同，纯 Go 构建与完整版的差别 |
| **6 设计与陷阱** | **为什么这样设计、踩过什么坑、改之前必须知道什么** |
| 7 代码位置 | 代码在哪个文件 |
| 8 验收用例 | 哪些用例守着这个功能，怎么手动验证 |
| 9 未结项与已知限制 | 没做的、已知不对的、代码与文档不一致的 |

- 每份 spec 开头有「最后核对日期」和「对应提交」：代码改到那个提交之后，spec 里的行号可能漂移。
- 标了「【待确认】」的条目是写 spec 时没能核实的地方，不是定案。
- spec 描述的是**现状**。想改行为，直接改 spec 的对应小节，再让 agent 同步代码。
