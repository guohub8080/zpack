# 全局约定（所有命令无例外）

> stdout 只有一条 JSON 结果、stderr 是可忽略的过程日志、退出码只有 0 和 1——这是 zpack 每个命令都遵守的输出契约，也是 GUI 与 Agent 调用 zpack 的全部依据。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 管的是**跨功能的全局规则**。各功能自己的旗标、字段和行为细则在各自的 spec 里（见 [README.md](README.md)），那些 spec 只写本功能特有的部分，全局规则一律以本文为准。

## 1. 这份文档管什么

- 输出契约：stdout / stderr / 退出码三者的分工与格式。
- 命令行约定：旗标形态、数值校验、路径、`--help`。
- 代码层面的硬约束：谁能写 stdout、为什么「print 即 exit」、信封怎么编码。
- 调用方（脚本 / Agent / GUI）该怎么消费结果。

不管：某个命令的具体旗标与字段（在功能 spec 里）、构建方式（[build.md](build.md)）。

## 2. 命令行约定

| 约定 | 规则 | 出处 |
|---|---|---|
| 命令形态 | 扁平命令：`zpack <命令> [子命令] <位置参数...> [旗标]`。没有「格式子命令」——写格式由 `--output` 扩展名决定，读格式靠魔数识别 | AGENTS.md 2026-11-02 定案 |
| 旗标形态 | 一律长形态 `--word`；个别历史短别名保留（如 audio 的 `-b` / `-o`）。新增旗标不得再引入短形态 | cli 各文件 |
| 旗标解析 | 各命令关闭 cobra 的旗标解析（`DisableFlagParsing: true`），手写解析：**未知旗标静默忽略**，不报错 | cli 各命令的构造函数 |
| 数值参数 | 严格校验：非法或越界 → error JSON + exit 1。整数必须整个字符串是合法十进制整数（`"5abc"`、`""` 不算） | `internal/cmdutil` 的 `ParseIntStrict` |
| 路径 | 完整支持空格与中文：路径经 argv 直传，禁止拼 shell 字符串 | — |
| `--help` / `-h` | 任何子命令都可用，打印帮助后 exit 0 | `internal/cliutil` 的 `HelpAwareRun` |
| `--quiet` | 过程日志全部静默：stderr 只剩「开始执行」那一行，stdout 不变。compress / extract / convert / media 全族 / audio 全族 / pdf 全族支持（2026-11-24 起 pdf 与 audio 才真正生效——此前只解析不生效）。日志去向按**本次调用的参数**判定，不靠包级变量 | `internal/cliutil` 的 `EngineLogf` |
| 平台不支持 | 必须输出 error JSON「暂不支持 <平台>：<原因>」+ exit 1，禁止静默失败 | — |
| 中断 | 第一次 Ctrl+C 或 SIGTERM：发出取消请求——**只有压缩包引擎**（compress / extract / convert）的循环会检查它，清理半成品后输出「操作已被用户取消（半成品已清理）」+ exit 1。第二次 Ctrl+C：立即退出，exit 130（不等清理） | `cli/root.go` 的信号处理、`core/cancel.go` |

关于「未知旗标静默忽略」与「数值严格校验」并存：这是与旧版 exkit 的故意分歧——旧版对数值也是静默回退或钳位，现在数值必须报错，其余宽容行为保留（`internal/cmdutil/flags.go` 包注释）。

**取消的范围**：信号一旦被接管（`signal.Notify`），进程就不再有「收到 Ctrl+C 即退出」的默认行为。所以对压缩包以外的命令，第一次 Ctrl+C 不会中断它们，要按第二次才会以 130 退出。

## 3. 输出契约

### 3.1 信封

stdout 上**有且只有一个最终输出**，三种信封（`internal/stdout/log.go`）：

| 场合 | 信封 | 退出码 | 输出函数 |
|---|---|---|---|
| 成功，有载荷 | `{"is_success":true,"data":{...}}` | 0 | `JSONPrint` |
| 成功，无载荷 | `{"is_success":true}` | 0 | `SuccessPrint`（当前没有命令使用，预留） |
| 失败 | `{"is_success":false,"error_info":"人类可读原因"}` | 1 | `ErrorPrint` |
| 判定类：操作完成但结论为否 | `{"is_success":false,"data":{判定字段}}` | 1 | `JSONFailPrint` |
| 纯文本输出 | 文本本身（无信封） | 0 | `TextPrint` |

- 信封层永远只有 `is_success` / `error_info` / `data` 三个键，键序固定。消费方一行心智：**有 `is_success` 就看 `data`，否则看 `error_info`**。
- `data` 载荷一律用 struct 编码（字段序 = 声明序，输出确定），不传 map（键序会随进程随机）。
- 不转义 HTML，非 ASCII 原样 UTF-8，尾部一个换行。

### 3.2 退出码

- 成功 `0`，失败 `1`，没有中间状态。
- **exit 0 ⇔ `is_success:true`**。这是刻意的双通道冗余：管道会吃掉 `$?`，stdout 是唯一一定能穿越进程边界的信道。
- **唯一例外是判定类命令**（`test` 校验失败、`hash --expect` 不匹配、`hash --check` 有不匹配或缺失）：操作本身完成了，但结论为否，所以 JSON 是 `is_success:false` + 完整的判定字段（`is_match` / `hash` / `expected` / `mismatched` / `missing` …），退出码跟随结论。消费方规则——脚本可以用退出码，**管道 / 存档 / GUI 场景只信 JSON 字段**。
- 第二次 Ctrl+C 强制退出时是 130。

### 3.3 纯文本输出的命令

这些命令的最终结果本身就是文本，不走信封：

| 命令 | stdout |
|---|---|
| `base64 encode`（无 `--output`） | Base64 字符串 |
| `base64 decode`（无 `--output`） | 原始解码字节（二进制安全） |
| `crypto encrypt` / `crypto decrypt`（无 `--output`） | Base64 密文 / 解码后原始内容 |
| `crypto password` | 每行一个密码 |
| `crypto uuid` | 每行一个 UUID |
| `json get <字段路径>` | 取到的值（复合值打印成紧凑 JSON；目前直接写 stdout，没经输出层——见 tools.md 未结项） |
| `skill` | SKILL.md 全文 |

它们带 `--output` 时改为写文件并输出信封 JSON（见各自的 spec）。

### 3.4 契约的唯一例外：`zpack ffmpeg`

`zpack ffmpeg <原版任意参数...>` 是内置 ffmpeg CLI 的透明代理，stdout / stderr / 退出码全部是 ffmpeg 的原生语义（stdout 可能是二进制媒体流，`pipe:1` 场景必需），**不走 JSON 信封**。详见 [media.md](media.md)。

## 4. stderr 过程日志

stderr 是过程日志，调用方可以整体忽略（`2>/dev/null`），不影响对结果的判断。

- 格式统一：`[YYYY-MM-DD HH:MM:SS] 消息内容`（本地时间）。**没有级别标签是常态**——单级类别是零信息装饰。唯一的例外是**警告**：`[YYYY-MM-DD HH:MM:SS] [WARN] 消息内容`（`LoggerWarn`），只用于「操作会照常继续，但后果不可逆、又不直观」的提醒。出现标签本身就是信号：调用方可以只盯着 `[WARN]` 看，人也能一眼从滚动的日志里把它挑出来；所以不要拿它记普通提示。首例是往有数据的盘写镜像（burn.md 3.4）。
- **统一开始日志**：每个命令入口 stderr 恒有一行 `zpack <命令名> 开始执行`，由装配层（`cli/root.go` 的 `PersistentPreRun`）一处约束全部命令，命令代码不各自输出。**只记命令名、绝不记参数**——参数可能含密码或密钥，落终端或日志即泄密。
- 进度条：操作耗时超过 5 秒时每 5 秒采样一次，格式如 `压缩中 ███████░░░░ 25.0% | 500.0MB/2.0GB | 100.0MB/s | 1/4 文件 | 剩余 15秒`；5 秒内完成的操作不输出进度。
- 各功能特有的过程日志（GBK 名修复、跳过已存在、分卷创建、刻录心跳等）见各自的 spec。

## 5. 调用方（脚本 / Agent / GUI）消费指南

```bash
# 只拿最终结果（推荐）：stdout 解析一次 JSON
result=$(zpack compress 目录/ --output out.zip 2>/dev/null)

# 判断成败：zpack 自带的零依赖消费层（不需要 python3 / jq）
zpack compress 目录/ --output out.zip 2>/dev/null | zpack json ok && echo OK

# 取字段：data 载荷里的路径用点分段，数组用下标
zpack compress 目录/ --output out.zip 2>/dev/null | zpack json get data.archive

# 同时看进度：stderr 留给终端，stdout 给消费方
zpack extract big.zip 2>&1 1>/tmp/result.json | cat; zpack json get data.file_count < /tmp/result.json

# 完全静默（脚本 / CI）：只看退出码
zpack compress 目录/ --output out.zip > /dev/null 2>&1 && echo "OK" || echo "FAIL"
```

`zpack json` 的细则见 [tools.md](tools.md)。

## 6. 设计与陷阱

**改任何命令的输出之前必须知道的事。**

- **唯一通道**：最终输出只允许经 `internal/stdout` 的 print 函数，禁止任何包裸写 stdout。原因不只是整洁——见下一条。
- **print 即 exit，外层 defer 不运行**：print 函数输出后立即 `os.Exit`，调用栈上所有 defer 都不会执行。任何清理（临时目录、半成品文件）必须放在闭包内的 defer（闭包 return 时执行）或 print 之前显式调用。convert 的临时目录曾在成功路径静默泄漏，就是这个原因（2026-11-04 修复，清理行为已入验收）。
- **原子输出**：JSON 先完整编码进内存，成功后一次性写 stdout；编码失败时 stdout 零字节。所以「stdout 要么完整要么空」是结构上成立的，不会出现半截 JSON。
- **`--help` 必须手动守卫**：`DisableFlagParsing` 会吞掉 cobra 的自动 help。全部命令的 Run 都经 `HelpAwareRun`：命中 `--help` / `-h` 就打印帮助并 exit 0。2026-11-04 之前 `zpack compress --help` 报的是参数错误——Agent 嗅探一个 CLI 的第一本能就是 `--help`，在这里断了后面的全断。
- **引擎错误文案就是对外文案**：引擎（core / image / pdf 等）返回的 `error.Error()` 被 cli 原样放进 `error_info`。改引擎里的文案必须同步改 `verify/verify.sh` 的预期值。
- **分层**：引擎不 import cli / ui / cobra，不碰 stdout、不 `os.Exit`，错误一律用 error 返回，过程日志经 `Logf` 回调注入（nil 即静默）。cli 是引擎的命令面，只做「解析校验 → 调引擎 → 呈现结果」。
- **GUI 是薄壳**：GUI 不直连引擎，经 stdout JSON 契约驱动 CLI 二进制（见 [gui.md](gui.md)）。所以任何字段改名、增删，都是对 GUI 的破坏性变更。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 输出契约层 | `internal/stdout/log.go` | 信封编码、五个 print 函数、过程日志（`LoggerInfo` 无标签；`LoggerWarn` 带 `[WARN]`，行由纯函数 `warnLogLine` 拼） |
| 旗标小件 | `internal/cmdutil/flags.go` | `HasFlag` / `FlagValue` / `ParseIntStrict` / `FirstPositional`（纯逻辑，引擎也能引用） |
| 命令面公共底座 | `internal/cliutil/` | `help.go`（HelpAwareRun）、`cliutil.go`（RestAfterPositional / FileOutputJSON）、`logf.go`（StderrLogf / EngineLogf）；依赖 cobra 与输出层，只给命令面用 |
| 装配与信号 | `cli/root.go` | `Execute`、统一开始日志、Ctrl+C 处理；命令实现都在 `cli/` 的功能子包里 |
| 单测 | `internal/stdout/log_test.go`、`internal/cmdutil/flags_test.go` | 原子输出、严格整数解析、警告行的格式 |

命令面按功能分子包：`cli/archive`、`cli/burn`、`cli/crypto`、`cli/image`、`cli/media`（视频 + 音频 + ffmpeg 代理）、`cli/office`、`cli/pdf`、`cli/tools`。子包里引入引擎一律用别名 `engine`（如 `engine "github.com/guohub8080/zpack/core"`），所以命令面代码里统一写 `engine.X`。

## 8. 验收用例

- `verify/verify.sh` 的每个用例都同时比对输出与退出码（`assert_equal`），这是本契约最直接的验收。
- Ctrl+C 行为、`--help` 可用性没有自动化用例，靠手动验证：`zpack <任意命令> --help; echo $?` 应为 0。

## 9. 未结项与已知限制

- **压缩包以外的命令按一次 Ctrl+C 不会中断**（原因见第 2 节「取消的范围」）：刻录、视频转码这类长操作想中途停下，得按两次。是否让这些引擎也响应取消，尚无定案。
- 根命令的 Short 描述「zip 并行架构 + tar/tar.gz；7z/rar 解压规划中」已过时（7z / rar 读取早已交付）；`media` 的 Use 只写了 `<convert|compress>`，实际子命令远不止这两个。帮助文本的修订待办。
- `image` 不解析 `--quiet`（它的过程日志始终输出）；是否补上尚无定案。

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | fb313db |
| 2026-11-24 | 数值严格校验补漏：crypto / bench 改用 `ParseIntStrict`（此前 `--length 5abc` 被当成 5） | 6016f9b |
| 2026-11-24 | 命令面按功能拆子包；过程日志去向改为按调用参数判定，pdf / audio 的 `--quiet` 生效 | 6d31586、40fb9e7 |
| 2026-11-24 | 启用一直预留着的警告级别：`[WARN]` 标签行（`LoggerWarn`），只给「照常继续、后果不可逆且不直观」的提醒用；首例是往有数据的盘写镜像（burn.md 3.4） | 510fe45 |
