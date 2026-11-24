# 压缩包（archive：compress / extract / convert / list / test / info / bench）

> 压缩与解压专项：一条命令读 zip / 7z / rar / tar 系与多种单流格式，写格式由 `--output` 扩展名决定。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

- **做什么**：压缩包是 zpack 的立身功能。一个引擎（顶层 `core` 包）配七个扁平命令——没有「格式子命令」。
  - **写**：`compress` 把目录或文件打成 zip / tar / tar.gz / tar.xz / tar.zst / 7z。zip 可以带密码（WinZip AES-256）；7z 可以带密码（连文件名一起加密，仅 macOS 完整版）。任何格式写完后都能按字节切成 `.001/.002/...` 分卷。写哪种格式**只看 `--output` 的扩展名**。
  - **读**：`extract` 解开 23 种格式标识对应的归档——zip、tar 及其四种压缩壳、7z、rar、wim、cab、ar、asar、iso、八种单流压缩格式与两种文本编码，另外能把 `.001` 顺序分卷拼起来当源。是哪种格式**只看文件内容（魔数），不看扩展名**（core/format.go:54）。
  - **附属**：`convert` 换格式（先解到临时目录，再按输出扩展名重新打包）；`list` / `test` / `info` 不落盘地查看、校验、统计归档；`bench` 跑本机压缩解压基准。
- **不做什么**（明确的边界）：
  - rar 永远不写——创建 rar 为法律所禁止，nwaples/rardecode 也只读（AGENTS.md 2026-11-02 北极星定案）。
  - tar.bz2 不写（纯 Go 没有成熟的 bzip2 编码器）；读取支持（core/tar.go:219）。
  - 不做私有格式——互操作即护城河。
  - 写入不保留空目录：目录条目不写入，只有文件入包（core/common.go:119）。
  - 解压不还原条目的修改时间与属主：`core` 全包没有 `Chtimes` / `Chmod` 调用，解出来的文件时间就是落盘时刻。
  - zip 写入时符号链接按目标内容存（解引用）；只有 tar 保留符号链接。
  - `list` / `test` / `info` 只认 zip、tar 系、7z、rar 四类，且不接受密码（core/list.go:54）。
  - 7z 写入引擎只在 macOS 的 cgo 构建里存在；Windows、Linux 以及纯 Go 构建一律报「需完整版」（internal/sevenzipcli/cli_stub.go:1）。
- **用户会在什么场景下用它**：
  - 日常打包分发与备份（`compress`），需要口令保护或按邮件 / 介质容量切分时加 `--password` / `--volume-size`。
  - 打开别人发来的任意压缩包（`extract`）：rar、7z、分卷、加密包、Windows 工具做出的 GBK 文件名包都要能开，并且不把文件散落到当前目录。
  - 接收方打不开某种格式时换一种（`convert`）。
  - 下载或传输之后校验完整性（`test`）、不落盘查看内容（`list` / `info`）。
  - Agent 与 GUI 经 stdout JSON 契约调用以上全部能力——GUI 不直连引擎，只认 JSON 字段（见第 6 节的耦合点）。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack compress <目录\|文件...> [旗标]` | 打包压缩。写格式由 `--output` 扩展名决定（cli/archive/compress.go:27） |
| `zpack extract <归档> [旗标]` | 解压。读格式靠魔数识别（cli/archive/extract.go:20） |
| `zpack convert <源归档> [输出路径] [旗标]` | 格式转换：解到临时目录，再按输出扩展名重新打包（cli/archive/convert.go:21） |
| `zpack list <归档>` | 列出条目明细，不解压（cli/archive/list.go:18） |
| `zpack test <归档>` | 完整性校验，不写盘（cli/archive/list.go:28） |
| `zpack info <归档>` | 聚合元数据（cli/archive/list.go:38） |
| `zpack bench [旗标]` | 本机压缩 / 解压性能基准（cli/archive/bench.go:25） |

### 2.1 解析规则（七个命令通用）

- 所有命令都设了 `DisableFlagParsing: true`，旗标是手写解析的：**未知旗标静默忽略**，但未知旗标后面跟的那个值不会被跳过，会被当成位置参数——在 `compress` 里成为输入路径，在 `extract` 里成为归档路径（只在还没取到归档时），在 `convert` 里成为输入或输出路径（cli/archive/compress.go:110、extract.go:101、convert.go:59）。
- `--help` / `-h` 由 `cliutil.HelpAwareRun` 统一守卫：打印帮助后 exit 0（internal/cliutil/help.go:15）。
- `list` / `test` / `info` 完全不解析旗标，只取第一个不以 `-` 开头的参数当归档路径（internal/cmdutil/flags.go:46）——旗标的值排在归档前面就会被误当成归档。
- 同一个值旗标给多次时，取值规则**不统一**：由各命令的解析循环处理的旗标（`--output`、`--password`、`--password-table`、`--volume-size`、`--comment`、`--comment-file`）后者覆盖前者；由 `FlagValue` 取值的旗标（`--level`、`--jobs`、`--temp-dir`）取**第一次**出现的值（internal/cmdutil/flags.go:25）。`--exclude` 可重复，逐个收集。

### 2.2 `compress` 的旗标

| 旗标 | 含义 | 取值与校验 | 缺省 | 出处 |
|---|---|---|---|---|
| `<目录\|文件...>` | 打包对象，可多个 | 逐项 `os.Stat`，不存在即报 `路径不存在：<绝对路径>`；每一项都必须存在，报第一个不存在的 | 无（必填） | cli/archive/compress.go:155 |
| `--output <路径>` | 输出路径；**扩展名决定写格式**（判定表见 4.1.1） | 缺值报 `--output 缺少值`。已存在则覆盖 | 当前目录下 `<首个输入的基名>.zip`（注意是**当前目录**，不是输入所在目录） | cli/archive/compress.go:166 |
| `--exclude <模式>` | 追加排除规则，可重复 | glob：`*` 单层、`**` 多层、`?`、`[abc]`；尾部 `/` 只匹配目录；不含 `/` 的模式匹配任意一层的名字。只对**目录输入**的遍历生效，显式给出的文件不受影响。缺值报 `--exclude 缺少值` | 无 | cli/archive/compress.go:61、core/common.go:45、internal/cmdutil/glob.go:17 |
| `--level <0-9>` | 压缩档位 | 严格整数且 0–9，否则报 `--level 需要 0-9 的整数，收到: <原值>`。作用对象：zip 的 deflate、tar.gz 的 gzip、7z 的 `-mx`；对 tar.xz / tar.zst / 无压缩 tar 无效 | 6 | cli/archive/compress.go:120、core/tar.go:204 |
| `--jobs <1-64>` | 并行度 | 严格整数且 1–64，否则报 `--jobs 需要 1-64 的整数，收到: <原值>`。zip：工作协程数 = `min(jobs, 8)`，为 1 时整档走顺序路径；tar.gz：大于 1 时走并行 gzip，协程数就是 `jobs`（**没有 8 的上限**）；tar / tar.xz / tar.zst / 7z 不参与 | `min(CPU 核数, 8)` | cli/archive/compress.go:133、core/zip.go:538、core/tar.go:185 |
| `--volume-size <尺寸\|模板>` | 写完后按字节切分卷 | 数字可带后缀 `B` / `K` / `KB` / `M` / `MB` / `G` / `GB`（大小写不敏感，1024 基）；或模板（见下表，SI 十进制）。必须 > 0，否则报 `--volume-size 需要 >0 的尺寸（如 100MB、1GB、65536）或模板（gmail/cd650/cd/vcd/fat32/dvd/dvd9/bd/bd50），收到: <剩余文本>`。另在引擎侧有下限 65536 字节，低于报 `分卷大小不得小于 65536 字节（64KB），收到: <字节数>`。缺值报 `--volume-size 缺少值（如 100MB、1GB、65536）` | 不分卷 | cli/archive/compress.go:240、core/core.go:157 |
| `--password <值>` | 加密密码 | 缺值报 `--password 缺少值`。只对 zip（WinZip AES-256）和 7z（`-p` + `-mhe=on`）生效；输出为 tar 系时**被静默忽略**。空串等于不加密 | 不加密 | cli/archive/compress.go:81、core/zip.go:725、core/sevenz.go:93 |
| `--password-table <文件>` | 密码映射文件（格式见 2.5） | 缺值报 `--password-table 缺少值（密码映射文件路径）`。写入时它是 `--password` 之上的**逐文件覆盖**：没给 `--password` 时单靠密码表不会加密。对 7z 只有提示「已忽略」 | 无 | cli/archive/compress.go:87、core/sevenz.go:96 |
| `--comment <文本>` | 归档注释 | 缺值报 `--comment 缺少值`。只有 zip 生效（写进 EOCD 注释字段）；tar 系静默忽略，7z 提示「已忽略」。超过 65535 字节时被标准库拒绝，而返回值没有被检查，注释会**静默丢失** | 无 | cli/archive/compress.go:73、core/zip.go:512 |
| `--comment-file <路径>` | 从文件读注释 | 缺值报 `--comment-file 缺少值（注释文件路径）`；读不到报 `无法读取注释文件: <错误>`。内容首尾空白去掉。**优先于 `--comment`** | 无 | cli/archive/compress.go:67、184 |
| `--temp-dir <目录>` | 临时文件落点 | 缺值报 `--temp-dir 缺少值（临时目录路径）`；目录不存在自动创建，创建失败报 `无法创建临时目录: <错误>`；值全是空白视为没给。影响 zip 分块临时文件与 cab 文件夹溢写；**不影响**加密大文件的溢写文件和 7z 的暂存目录（见第 5、9 节） | 系统临时目录 | cli/archive/tempdir.go:16、core/temp.go:18 |
| `--quiet` | 过程日志静默 | 无值。带它时引擎的进度条、提示、跳过明细全部不输出；stdout 不变；「开始执行」一行仍在 | 关 | internal/cliutil/logf.go:21 |

`--volume-size` 模板表（cli/archive/compress.go:226；大小写不敏感，前后空白忽略）：

| 模板 | 别名 | 字节数 | 说明 |
|---|---|---|---|
| `gmail` | — | 25 000 000 | Gmail 附件上限 |
| `cd650` | — | 650 000 000 | CD-R 74 分钟 |
| `cd` | `vcd` | 700 000 000 | CD-R 80 分钟 |
| `fat32` | — | 4 294 967 295 | FAT32 单文件上限 4GiB−1 |
| `dvd` | `dvd5` | 4 700 000 000 | DVD-5 |
| `dvd9` | — | 8 500 000 000 | DVD-9 |
| `bd` | `bd25` | 25 000 000 000 | BD-R 25 |
| `bd50` | — | 50 000 000 000 | BD-R DL 50 |

（`bd25` 在代码里有，但报错文案的模板清单里漏列了它，cli/archive/compress.go:261。）

### 2.3 `extract` 的旗标

| 旗标 | 含义 | 取值与校验 | 缺省 | 出处 |
|---|---|---|---|---|
| `<归档>` | 归档路径，只取第一个 | 不存在报 `路径不存在：<绝对路径>`；没给报 `请指定归档：extract <归档.zip\|.tar\|.tar.gz> [--output 目录] [--jobs N] [--force]`。路径以 `.001` 结尾时自动拼接后续分卷 | 必填 | cli/archive/extract.go:115、125 |
| `--output <目录>` | 目标目录 | 缺值报 `--output 缺少值`。给了就直接解进去，不走智能解压 | 智能解压（见 4.1.10） | cli/archive/extract.go:84 |
| `--force` | 覆盖已存在的文件 | 无值旗标，出现过即生效 | 跳过已存在，计入 `skipped` | cli/archive/extract.go:118 |
| `--jobs <1-64>` | zip / 7z 的解压并行度 | 严格整数且 1–64，否则报 `--jobs 需要 1-64 的整数，收到: <原值>`。tar、rar、wim、cab、单流格式是顺序流，不参与 | `max(1, CPU 核数/2)`；引擎内硬上限 32 且不超过条目数 | cli/archive/extract.go:42、core/zip.go:998 |
| `--password <值>` | 整档密码 | 缺值报 `--password 缺少值`。zip 逐条目生效；7z、rar 整档一个密码 | 无 | cli/archive/extract.go:78 |
| `--password-table <文件>` | 密码映射文件（格式见 2.5） | 缺值报 `--password-table 缺少值（密码映射文件路径）`。**读取时的优先级高于 `--password`**：条目命中密码表就用表里的，没命中才回退 `--password`。7z、rar 只取能命中 `*` 的那一行当整档密码 | 无 | cli/archive/extract.go:58、core/zip.go:882、core/sevenzip.go:21 |
| `--temp-dir <目录>` | 临时文件落点 | 同 `compress`。只影响 cab 文件夹溢写 | 系统临时目录 | cli/archive/tempdir.go:16 |
| `--quiet` | 过程日志静默 | 同 `compress` | 关 | cli/archive/extract.go:143 |
| `--comment` / `--comment-file` | **对 extract 无效** | 被解析并占用值，但不产生任何效果（写进了 compress 用的包级变量） | — | cli/archive/extract.go:64 |

### 2.4 `convert` / `list` / `test` / `info` / `bench` 的旗标

| 命令 | 旗标 | 含义 | 取值与校验 | 缺省 | 出处 |
|---|---|---|---|---|---|
| `convert` | `<源归档>` | 要转换的归档 | 没给报 `用法: convert <源归档> [输出路径] [--level N] [--jobs N] [--password 值]`；不存在报 `路径不存在：<绝对路径>` | 必填 | cli/archive/convert.go:69、75 |
| `convert` | `[输出路径]` | 第二个位置参数 | 扩展名必须在白名单里（大小写不敏感）：`.zip` `.tar` `.tar.gz` `.tgz` `.tar.xz` `.txz` `.tar.zst` `.tzst`，否则报 `输出格式暂不支持（支持 .zip/.tar/.tar.gz/.tar.xz/.tar.zst）`——`.7z` 与 `.tar.bz2` 都会被拒。写成 `--output <路径>` 也能用，但那是因为旗标名被忽略、值落成了位置参数，不是设计出来的形态。已存在则覆盖 | 源文件同目录 `<基名>.zip`；基名 = 先剥复合后缀（`.tar.gz` `.tar.xz` `.tar.bz2` `.tar.zst` `.tgz` `.txz` `.tbz2` `.tzst`），再剥最后一个扩展名 | cli/archive/convert.go:79、95 |
| `convert` | `--password <值>` | **源归档**的密码（只用于解压；输出归档不加密） | 缺值报 `--password 缺少值` | 无 | cli/archive/convert.go:40 |
| `convert` | `--level <0-9>` | 目标归档的压缩档位 | **非法值静默回退 6，不报错**——这是全局「数值严格校验」的例外（cli/archive/convert.go:113） | 6 | cli/archive/convert.go:112 |
| `convert` | `--jobs N` | **实际无效** | 被解析进 `restArgs` 后从未被读取，传给引擎的恒为 0（cli/archive/convert.go:118） | 0 | cli/archive/convert.go:53 |
| `convert` | `--temp-dir <目录>` | 中转目录的落点 | 缺值报 `--temp-dir 缺少值（临时目录路径）` | 系统临时目录 | cli/archive/convert.go:48 |
| `convert` | `--quiet` | 过程日志静默 | 连 `读取 X ...` / `写入 X ...` 两行一起静默 | 关 | cli/archive/convert.go:109 |
| `list` / `test` / `info` | `<归档>` | 归档路径 | 没给报 `请指定归档文件路径`；不存在报 `路径不存在：<绝对路径>`。**没有任何其他旗标** | 必填 | cli/archive/list.go:47 |
| `bench` | `--size <MB>` | 语料大小 | 严格整数且 1–2000，否则报 `--size 需要 1-2000 的整数（MB），收到: <原值>`；作为最后一个参数且没给值时静默取缺省 | 50 | cli/archive/bench.go:51 |
| `bench` | `--temp-dir <目录>` | 语料与产物的落点 | 缺值报 `--temp-dir 缺少值（临时目录路径）` | 系统临时目录 | cli/archive/bench.go:65 |
| `bench` | `--quiet` | **无效** | bench 的过程日志直接写 stderr，不受 `--quiet` 控制 | — | cli/archive/bench.go:78 |

### 2.5 密码映射文件的格式

`--password-table` 指向的文件每行一条 `匹配模式=密码`（cli/archive/password_table.go:14）：

- 以第一个 `=` 分隔，所以密码里可以含 `=`；模式与密码两端空白都去掉。
- 空行与 `#` 开头的行跳过。行首就是 `=`（模式为空）报 `密码表第 <行号> 行格式错误（应为 匹配模式=密码）: <该行>`。
- 一条有效规则都没有报 `密码表为空: <路径>`；文件读不到报 `无法读取密码表: <系统错误>`。
- 密码**可以是空串**（`a.txt=`），后果见第 9 节。
- 匹配对象是条目名的**基名**（`filepath.Base`），不是完整路径。规则按文件行序逐条尝试，先比精确相等，再用 `filepath.Match` 比 glob，**首个命中生效**；`*` 或 `*=密码` 因此是兜底规则（cli/archive/password_table.go:41）。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

## 3. 输出契约

成功时的 stdout JSON（都是信封的 `data` 载荷；字段顺序即声明顺序）：

### 3.1 `compress`

```json
{"is_success":true,"data":{"archive":"/abs/out.zip","file_count":5,"size_bytes":12345}}
```

| 字段 | 类型 | 含义 | 出处 |
|---|---|---|---|
| `archive` | string | 归档**绝对路径**。分卷且真的切开了时是首卷路径 `<输出路径>.001` | core/core.go:144 |
| `file_count` | int | 入包的文件条目数。不含目录；tar 里的符号链接算一条；zip 里的符号链接按目标内容入包，也算一条。多个输入在包里同名时直接报错（见 3.7），不会有「两个都算」的归档 | core/zip.go:807、core/tar.go:261 |
| `size_bytes` | int | 归档字节数。分卷时是**全卷总字节数** | core/core.go:145 |

### 3.2 `extract`

```json
{"is_success":true,"data":{"output":"/abs/dir","file_count":5,"size_bytes":12345,"skipped":0}}
```

| 字段 | 类型 | 含义 | 出处 |
|---|---|---|---|
| `output` | string | 实际解压根目录的绝对路径。给了 `--output` 就是它；智能解压的单根归档是**当前目录本身**；散装归档是 `当前目录/<归档基名>`；单流格式是 `--output` 或当前目录 | core/common.go:211、core/single.go:100 |
| `file_count` | int | **实际解出**的条目数：不含目录条目，不含被跳过的。符号链接、硬链接各算一条 | core/zip.go:1066 |
| `size_bytes` | int | 实际解出的字节数，跳过的不计 | core/zip.go:1067 |
| `skipped` | int | 因目标已存在而跳过的条目数（符号链接被跳过也算） | core/zip.go:1068 |

rar 的混合密码档案会「优雅收尾」：遇到密码错误就停，已解出的算数，**照样返回 `is_success:true`**（core/rar.go:158）。

### 3.3 `convert`

```json
{"is_success":true,"data":{"source":"/abs/in.rar","archive":"/abs/in.zip","format":"zip","source_files":3,"file_count":3,"size_bytes":456}}
```

| 字段 | 类型 | 含义 | 出处 |
|---|---|---|---|
| `source` | string | 源归档绝对路径 | cli/archive/convert.go:207 |
| `archive` | string | 目标归档绝对路径 | |
| `format` | string | 由输出路径推断的显示用标识：`zip` / `tar` / `tar.gz` / `tar.xz` / `tar.zst` | cli/archive/convert.go:188 |
| `source_files` | int | 从源归档解出的条目数（即解压阶段的 `file_count`） | cli/archive/convert.go:183 |
| `file_count` | int | 写进目标归档的条目数。和 `source_files` 可以不相等——空目录不入包、Windows 上符号链接降级成文件等都会让两个数对不上 | |
| `size_bytes` | int | 目标归档字节数 | |

### 3.4 `list` / `info`

```json
{"is_success":true,"data":{"format":"zip","comment":"备注","entry_count":6,"file_count":5,"dir_count":1,"total_uncompressed":100,"total_compressed":40,"archive_size":150,"compression_ratio":1.5,"encrypted":false,"entries":[{"name":"dir/","size":0,"compressed_size":0,"is_dir":true,"encrypted":false}]}}
```

| 字段 | 类型 | 含义 | 出处 |
|---|---|---|---|
| `format` | string | `zip` `tar` `tar.gz` `tar.xz` `tar.bz2` `tar.zst` `7z` `rar` | core/list.go:295 |
| `comment` | string | 归档注释。只有 zip 且注释非空时出现 | core/list.go:28 |
| `entry_count` | int | 条目总数（文件 + 目录） | core/list.go:283 |
| `file_count` / `dir_count` | int | 非目录条目数 / 目录条目数 | core/list.go:284 |
| `total_uncompressed` | int | 各条目未压缩大小之和（含目录条目的 0） | core/list.go:286 |
| `total_compressed` | int | 各条目压缩后大小之和。tar 系与 7z 恒为 0（没有逐条压缩尺寸） | core/list.go:139 |
| `archive_size` | int | 归档文件大小 | core/list.go:267 |
| `compression_ratio` | float | `archive_size / total_uncompressed`；两者任一为 0 时是 0 | core/list.go:288 |
| `encrypted` | bool | 是否有加密条目。**只有 zip 会置真**；7z、rar、tar 系恒为 false | core/list.go:109、159 |
| `entries` | array | 条目明细，**`info` 不带**这个字段 | core/list.go:37、cli/archive/list.go:112 |

`entries[]` 的字段：`name`（条目名，zip 的会做反斜杠归一与 `path.Clean`，**不做 GBK 修复**）、`size`、`compressed_size`、`is_dir`、`encrypted`（仅 zip）。

### 3.5 `test`

通过（exit 0）：

```json
{"is_success":true,"data":{"format":"zip","tested_entries":5,"all_ok":true}}
```

不通过（判定类失败，exit 1，信封里是 `data` 而不是 `error_info`）：

```json
{"is_success":false,"data":{"format":"zip","tested_entries":5,"failed_count":1,"failures":["CRC 校验失败: a.txt: zip: checksum error"]}}
```

`tested_entries` 就是 `info` 口径的 `file_count`（归档里的非目录条目数），**不是实际校验过的条目数**——zip 的加密条目被跳过不校验，但仍计在内（core/list.go:206、cli/archive/list.go:95）。

### 3.6 `bench`

```json
{"is_success":true,"data":{"data_size_mb":50,"cpu_cores":8,"results":[{"format":"zip","level":1,"compress_seconds":0.1,"compress_mbps":500.0,"decompress_seconds":0.02,"decompress_mbps":2500.0,"original_bytes":52428800,"compressed_bytes":26000000,"ratio":0.5}]}}
```

`format` 的取值是 `zip`（level 1 / 6 / 9）、`tar.gz`（level 1 / 6 / 9）、`tar.gz 并行`（level 6）、`tar`（level 0）；`*_mbps` 按 `data_size_mb × 1MiB ÷ 秒` 算，不按实际语料字节数；`ratio` = `compressed_bytes / original_bytes`（cli/archive/bench.go:102、144）。

### 3.7 失败时的 `error_info`

失败一律是 `{"is_success":false,"error_info":"<原因>"}` + exit 1（`test` 不通过除外，见 3.5）。文案**就是引擎或命令面返回的错误文本**，没有再包一层。

| 命令 | `error_info` | 触发条件 | 出处 |
|---|---|---|---|
| compress | `请指定打包对象：compress <目录> 或 <文件1> <文件2>... [--output out.zip\|out.tar\|out.tar.gz] [--exclude 名称]` | 没有输入 | cli/archive/compress.go:149 |
| compress / extract / convert / list / test / info | `路径不存在：<绝对路径>` | 路径 Stat 失败 | cli/archive/compress.go:161 等 |
| compress / extract / convert / bench | 见 2.2–2.4 的各旗标文案 | 旗标缺值、数值越界 | 各命令文件 |
| compress / extract | `无法读取密码表: <系统错误>`、`密码表第 N 行格式错误（应为 匹配模式=密码）: <该行>`、`密码表为空: <路径>` | 密码表有问题 | cli/archive/password_table.go:17、31、39 |
| compress | `无法读取注释文件: <系统错误>` | `--comment-file` 读不到 | cli/archive/compress.go:187 |
| compress / extract / convert / bench | `无法创建临时目录: <系统错误>` | `--temp-dir` 建不出来 | cli/archive/tempdir.go:26 |
| compress | `读取目录失败: <系统错误>` | 遍历输入目录出错 | core/common.go:141 |
| compress | `多个输入在压缩包里会同名：<包内名>（<来源路径> 与 <来源路径>…）`；多处时是 `多个输入在压缩包里会同名（N 处）：<包内名>（…）、<包内名>（…）…`，超过 3 处时末尾带「等」；句尾一律是 `；请把它们分别放进不同的文件夹再压缩，或只压缩它们共同的上层目录` | 多个输入在包里落成同名条目：两个不同目录里的同名文件、两个同名的目录、同一个路径给了多次（此时括号里写 `<路径> 给了 N 次`）。**不自动改名，直接报错**（2026-11-24 用户拍板）。zip / tar / 7z 共用这条检查；此时还没有创建输出文件 | core/duplicates.go |
| compress | `文件名不是有效 UTF-8，无法安全写入 zip: "<名>"`（外包 `写入文件失败: `） | zip：条目名不是合法 UTF-8，整档中止 | core/zip.go:174、225 |
| compress | `文件名不是有效 UTF-8，无法安全写入 tar: "<名>"`（外包 `写入文件失败: `） | tar 同上 | core/tar.go:283、241 |
| compress | `创建 zip 失败: <系统错误>`、`写入 zip 失败: <系统错误>`、`写入文件失败: <系统错误>`、`加密失败: <系统错误>` | zip 写入各阶段失败 | core/zip.go:509、555、550、734 |
| compress | `创建 tar 归档失败: <系统错误>`、`创建并行 gzip 流失败: <系统错误>`、`创建 gzip 流失败: <系统错误>`、`创建 xz 流失败: <系统错误>`、`创建 zst 流失败: <系统错误>`、`写入归档失败: <系统错误>` | tar 系写入各阶段失败 | core/tar.go:179、188、198、207、216、254 |
| compress | `暂不支持 tar.bz2 写入（纯 Go 无成熟 bzip2 编码器）——读取已支持` | `--output` 是 `.tar.bz2` / `.tbz2`。**报错前输出文件已被创建并截成 0 字节，且不清理** | core/tar.go:177、220 |
| compress | `zpack 7z 写入需完整版（CGO 构建 + 7z 静态产线）` | `--output` 是 `.7z`，而当前构建没有 7z 引擎（纯 Go 构建、Windows、Linux） | internal/sevenzipcli/cli_stub.go:9 |
| compress | `没有可压缩的文件（输入为空或全部被排除）` | 7z：收集不到任何条目 | core/sevenz.go:37 |
| compress | `覆盖旧归档失败: <系统错误>`、`读取工作目录失败: <系统错误>`、`切换工作目录失败: <系统错误>`、`创建清单失败: <系统错误>`、`写入清单失败: <系统错误>`、`创建暂存目录失败: <系统错误>` | 7z 写入的准备阶段失败 | core/sevenz.go:46、60、63、69、79、168 |
| compress | `7z 引擎失败（退出码 N）：<7z 输出的末 8 行，用「；」连接>`；引擎没有任何输出时是 `7z 引擎失败（退出码 N）：7z 引擎无输出` | 7z 引擎退出码 ≥ 2；半成品归档已删除 | core/sevenz.go:122 |
| compress | `分卷大小不得小于 65536 字节（64KB），收到: <字节数>` | `--volume-size` 换算后不足 64KB。此时归档已经压完，**被一并删除** | core/core.go:158 |
| compress | `操作已被用户取消（半成品已清理）` | zip 并行路径（`--jobs` ≥ 2）被取消 | cli/archive/compress.go:211 |
| extract | `请指定归档：extract <归档.zip\|.tar\|.tar.gz> [--output 目录] [--jobs N] [--force]` | 没给归档 | cli/archive/extract.go:116 |
| extract | `无法识别的归档格式: <路径>（支持 zip/7z/rar/tar 系/gzip/bzip2/zstd/xz/lz4/.001 分卷）` | 魔数与兜底探测全部不中。把目录、纯文本当归档解也是这条 | core/format.go:134 |
| extract | `无法读取 gzip 流: <错误>`、`无法读取 zstd 流: <错误>`、`无法读取 xz 流: <错误>` | 魔数命中但解压层初始化失败 | core/single.go:29、41、47 |
| extract | `无法读取 zip 归档: <错误>` | zip 中央目录读不出来 | core/zip.go:871 |
| extract | `归档已加密（WinZip AES-256\|WinZip AES-192\|WinZip AES-128\|ZipCrypto）：需要 --password 提供密码` | zip 里有加密条目而没有可用密码（密码表命中的是空串也算没有） | core/zip.go:889、915 |
| extract | `解压失败: 写出 <条目名> 失败: 密码错误或归档损坏` | zip 密码错误（2 字节校验值或 12 字节头校验不过） | core/zip.go:1035、core/crypto.go:60 |
| extract | `归档含不安全路径: <条目名>` | 条目名以 `/` 开头、含盘符段、含 `..` 段（zip、tar、7z、rar、wim、cab、ar、asar、iso、uuencode 内嵌文件名都查） | core/common.go:149 |
| extract | `归档含重复条目名: <条目名>` | 两个非目录条目解码、归一后同名（各容器管线都查） | core/zip.go:962 等 |
| extract | `归档含不安全符号链接: <条目> -> <目标>`、`归档含不安全硬链接: <条目> -> <目标>`、`归档含不安全条目类型（设备/管道/未知类型 "<字符>"）: <条目>` | tar 的链接逃逸与设备 / 管道 / 未知类型条目 | core/tar.go:109、116、121 |
| extract | `解压失败: 写出 <条目> 失败: <系统错误>`、`解压失败: 写出符号链接 <条目> 失败: <系统错误>`、`解压失败: 写出硬链接 <条目> 失败: <系统错误>`、`解压失败: 硬链接目标未解出: <路径>` | 落盘失败 | core/tar.go:354、410、425、436、448、544 |
| extract | `无法读取 7z 归档: <错误>`；错误文本含 `password` 时替换成 `暂不支持加密 7z：需要密码的档案暂未实现` | 7z 打不开或解密失败。后一条文案已过时——加密 7z 其实是支持的 | core/sevenzip.go:33、85 |
| extract | `无法读取 rar 归档: 归档已加密（rar）：需要 --password 提供密码`、`无法读取 rar 归档: 密码错误或归档损坏` | rar 缺密码 / 密码错 | core/rar.go:87、210、212 |
| extract | `无法读取 wim 归档: <错误>`（含 `无镜像`）、`无法读取 cab 归档: <错误>`、`无法读取 ar 归档: <错误>`、`无法读取 iso 归档: <错误>`、`无法读取 asar 归档: <错误>` | 各容器的结构错误，前缀后带具体原因 | core/wim.go:23、core/cab.go:57、core/ar.go:35、core/iso.go:23、core/asar.go:31 |
| extract | `暂不支持 cab Quantum 压缩文件夹（typeCompress=N）`、`暂不支持跨柜多卷 cab（iFolder=N）` | cab 的两种不支持情形 | core/cab.go:237、135 |
| extract | `unpacked 条目缺失: <路径>` | asar 的外置文件找不到 | core/asar.go:84 |
| extract | `分卷段是目录而非文件: <路径>`、`分卷首卷不存在: <路径>`、`无法读取分卷段: <系统错误>` | `.001` 分卷发现阶段失败 | core/split.go:42、47、53 |
| extract | `获取当前目录失败: <系统错误>`、`创建输出目录失败: <系统错误>` | 智能解压定根或建目录失败 | core/common.go:217、236 |
| convert | `读取源归档失败: <extract 的错误>`、`写入目标归档失败: <compress 的错误>`、`源归档为空，无法转换`、`无法读取临时目录: <系统错误>` | 管线各阶段失败；临时目录都会被清掉 | cli/archive/convert.go:148、152、159、172 |
| list / test / info | `无法识别的归档格式: ...`、`无法读取归档: <系统错误>`、`暂不支持列出 <格式名> 格式内容` | 认不出、打不开，或格式不在四类之内（`<格式名>` 如 `gzip`、`wim`；uu / xx 编码没有名字，显示为 `format(15)` / `format(16)`） | core/list.go:64、core/format.go:134 |

**取消时对外的文案并不统一**，按代码逐条管线读出来的结果如下（没有实际按过 Ctrl+C，【待确认】；conventions.md 写的是统一一条「操作已被用户取消（半成品已清理）」，与下表不一致，详见第 9 节）：

| 场景 | 第一次 Ctrl+C 之后 | 出处 |
|---|---|---|
| zip 压缩，`--jobs` ≥ 2 | `操作已被用户取消（半成品已清理）`，exit 1，归档已删 | core/zip.go:644、692、cli/archive/compress.go:211 |
| zip 压缩，`--jobs 1` | **没有检查点**，照常跑完并报成功 | core/zip.go:539 |
| tar 系压缩 | `写入文件失败: 操作已被用户取消`（错误被 `%s` 重新格式化，`errors.Is` 认不出来，专用的取消文案走不到），半成品已删 | core/tar.go:241、267 |
| 7z 写入 | 没有检查点，照常跑完 | core/sevenz.go |
| zip 解压 | **返回成功**：`is_success:true`、exit 0，`file_count` 是取消前已写出的条数 | core/zip.go:1047 |
| tar 系解压 | `解压失败: 操作已被用户取消`，已解出的文件留在盘上 | core/tar.go:354、391 |
| 7z、rar、wim、cab、ar、asar、iso、单流解压 | 没有检查点，照常跑完 | core/container*.go、core/single.go |
| convert | 解压阶段被取消：`读取源归档失败: 解压失败: 操作已被用户取消`；压缩阶段被取消：`写入目标归档失败: 写入文件失败: 操作已被用户取消` | cli/archive/convert.go:148、172 |

命令面为解压准备的 `操作已被用户取消（已解出的文件保留）`（cli/archive/extract.go:146）因此实际走不到。

### 3.8 stderr 过程日志

统一格式与「开始执行」一行见 conventions.md。本功能会输出的：

| 日志 | 触发 | 出处 |
|---|---|---|
| `压缩中 ████░░░░ 25.0% \| 500.0MB/2.0GB \| 100.0MB/s \| 1/4 文件 \| 剩余 15秒` | 压缩耗时超过 5 秒后每 5 秒一条；30 格进度条；速度与剩余时间只在速度 > 0 且未到 100% 时出现，否则省略这两段 | core/common.go:329、336 |
| `解压中 ████░░░░ ...` | 解压同上。各管线都带总量，只有总量为 0 的归档（例如全是 0 字节文件）才退化成计数模式 `解压中: 3/10 个文件，已处理 1.2MB`。两处口径瑕疵：zip 的「y 文件」把目录条目也算进了总数（实际只数写出的文件）；单流格式的总量取的是**归档文件大小**而不是解出大小，进度条会提前冲到 100% 再被截住 | core/common.go:343、core/zip.go:993、core/single.go:121 |
| `压缩中 已写 12.3MB（输入共 45.6MB）` | 7z 写入每 5 秒一条（7z 引擎本身在非终端下零输出） | core/sevenz.go:114 |
| `条目名按 GBK 解码: "ÖÐÎÄ" -> "中文"` | 条目名既没置 UTF-8 标志又不是合法 UTF-8，按 GBK 解码了 | core/common.go:94 等 |
| `跳过已存在: <目标路径>` | 目标已存在且没有 `--force` | core/zip.go:846、core/tar.go:501、526、core/single.go:164 |
| `归档 N 字节未超过单卷大小 M 字节，不分卷` | 给了 `--volume-size` 但整档装得下 | core/core.go:172 |
| `分卷 <路径>.001: 65536 字节` | 切分卷时每卷一条 | core/core.go:219 |
| `7z 为整档单密码，逐文件密码表仅对 zip 生效，已忽略` | 7z 写入同时给了 `--password-table` | core/sevenz.go:97 |
| `7z 不支持归档注释，已忽略` | 7z 写入给了注释 | core/sevenz.go:100 |
| `输入为散装多根，已建暂存目录对齐条目名` | 7z 写入的输入没有公共基目录，走了暂存目录 | core/sevenz.go:165 |
| `7z 完成（带警告，详见上方 stderr 输出）` | 7z 引擎退出码为 1（容忍为警告） | core/sevenz.go:135 |
| `部分加密 rar：预扫降级，根目录按归档基名保守包裹` | rar 预扫遇到密码类错误，按已读到的条目降级处理 | core/rar.go:123 |
| `Windows 无符号链接特权，降级为内容拷贝: <条目> -> <目标>` | Windows 上创建符号链接失败且错误码是 1314 | core/tar.go:430 |
| `读取 <源文件名> ...`、`写入 <目标文件名> ...` | convert 的两个阶段开始时 | cli/archive/convert.go:139、162 |
| `生成 50MB 测试语料（50% 可压 + 50% 随机）...` | bench 开始 | cli/archive/bench.go:78 |
| `<格式> lvl=<档位>: 压缩 120.5MB/s (0.41s) 解压 900.1MB/s (0.06s) 比值 49.12%` | bench 每跑完一行 | cli/archive/bench.go:154 |
| `<格式> lvl=<档位> 压缩失败: <错误>` | bench 某一行压缩失败，该行被跳过 | cli/archive/bench.go:128 |

7z 引擎自己写到 stderr 的内容会被原样转发（internal/sevenzipcli/cli_cgo.go:77）。

`--quiet` 只压制「经引擎 `Logf` 回调发出」的日志：上表中 7z 引擎的输出、bench 的全部日志，以及「开始执行」一行不受它影响。

## 4. 行为细则

### 4.1 正常路径

#### 4.1.1 compress：写格式由 `--output` 扩展名决定

判定按扩展名后缀做，大小写不敏感，自上而下首个命中生效（core/format.go:19）：

| `--output` 的扩展名 | 写格式 | 行为 |
|---|---|---|
| `.7z` | 7z | 交给进程内的官方 7-Zip C++ 引擎（4.1.6）。只在 macOS 完整版存在，其余构建报 `zpack 7z 写入需完整版（CGO 构建 + 7z 静态产线）` |
| `.tar` | tar | 无压缩的 PAX tar |
| `.tar.gz`、`.tgz` | tar.gz | tar + gzip |
| `.tar.xz`、`.txz` | tar.xz | tar + xz |
| `.tar.bz2`、`.tbz2` | tar.bz2 | **能识别但不支持**：先创建输出文件，再报 `暂不支持 tar.bz2 写入…`，留下一个 0 字节文件（core/tar.go:177、219） |
| `.tar.zst`、`.tzst` | tar.zst | tar + zstd |
| **其余全部**——`.zip`、没有扩展名、任何别的扩展名 | zip | 没识别出来的扩展名一律当 zip，产出的文件名保持用户给的原样 |

已存在的输出文件会被覆盖：zip、tar 系直接 `os.Create` 截断重写；7z 是先删再写（7z 的 `a` 命令对已存在的归档是「更新并入」语义，与本工具的「覆盖」契约不符，core/sevenz.go:44）。

#### 4.1.2 compress：输入收集与排除

对每个输入（core/common.go:64）：

- **文件**：成为一个条目，条目名是它的基名。显式给出的文件**不做任何排除**——`--exclude`、默认垃圾清单、`.zpackignore` 都管不到它。
- **目录**：`filepath.WalkDir` 按字典序遍历，所以同输入的条目顺序固定。条目名是相对「输入目录的父目录」的路径，这就是「目录以目录名为包内根前缀」的来由；多个输入时条目平铺在包根，前缀各自是自己的目录名。条目名恒为 `/` 风格。
- **多个输入在包里同名 → 报错**（`reportDuplicateEntries`，core/duplicates.go；2026-11-24 用户拍板：不自动改名，有问题就报错）。文件输入按文件名平铺进包根、目录输入以目录名为根前缀，所以两个不同目录里的 `a.txt`、两个同名的目录都会撞成同一个包内名。这样的归档解压时要么互相覆盖（多数工具）、要么被拒绝（zpack 自己的解压就拒绝，见 extract 的「归档含重复条目名」），所以不让它生成出来。只比较包内名的**完全相同**：大小写不同的两个名字（`A.txt` 与 `a.txt`）算不同条目，不拦——它们在 Linux 上是两个合法的文件，拦了会误伤 Linux 上的目录树（见 9.2）。
- **目录条目不入包**：只收集文件，空目录因此不会保留。
- 遍历中每个条目按下面的次序判定（命中最前面的一条即定）：
  1. 名字是 `.zpackignore` 的文件本身，永远跳过。
  2. `.zpackignore` 的规则（路径相对输入目录的根）。
  3. 默认垃圾清单或 `--exclude`（路径同样相对输入目录的根；输入目录自己不参与判定）。
  4. 是目录就不收集，继续往下走。
  被排除的目录整棵剪枝，不再进入。
- **默认垃圾清单**按基名比对：`.DS_Store`、`__MACOSX`、`Thumbs.db`、`desktop.ini`，以及 `._` 前缀的任何名字（core/common.go:24、37）。

`.zpackignore` 的语义（core/zpackignore.go）：

- **从哪里加载**：每个输入——目录输入取该目录，文件输入取它所在的目录——的 `.zpackignore`，再加当前目录的一份（已经加载过的目录不重复）。加载不到或内容为空就当没有，不报错。
- **怎么生效**：所有加载到的规则文件合成一个清单，对**每个**输入目录生效，匹配路径一律相对「正在遍历的那个输入目录的根」。也就是说 B 目录的 `.zpackignore` 也会套到 A 目录的树上，当前目录的那份也一样（core/common.go:97）。
- **语法**：`#` 开头是注释，空行跳过；`!` 前缀是反选；尾部 `/` 只匹配目录；首部 `/` 只匹配根级。不带首部 `/` 的模式匹配「路径本身，或从任意一层开始的尾部」，所以 `*.log` 能匹配任意深度的日志文件。同一个文件里**最后一条命中的规则说了算**。不支持 `**`。
- **两个边界**：不同 `.zpackignore` 文件之间不能互相反选——每个文件独立判定，任何一个判了排除就是排除；目录一旦被排除，里面的文件无法用 `!` 救回（与 gitignore 相同）。

收集完一个条目都没有时：zip 与 tar 系照常成功，产出一个空归档，`file_count` 为 0；7z 报 `没有可压缩的文件（输入为空或全部被排除）`。

#### 4.1.3 compress：zip 写入管线

每个条目先被规划成三条路径之一（core/zip.go:294），主协程只按规划派发、不做 I/O：

| 规划 | 条件 | 做法 |
|---|---|---|
| 整条目缓冲 | 文件 ≤ 0x40001 字节（256KB + 1）；**或**任何加密条目 | 工作协程把整个文件读完：先读开头 16 字节做 HSA 判定，Store 或 klauspost flate 压进内存；主协程按收集顺序 `CreateRaw` 落盘 |
| 分块并行 | 文件 > 0x40001 字节，未加密，`--level` > 0，且 HSA 魔数判定不是 Store | 128KB 一片，各片独立 deflate；每片预置前一片结尾 ≤ 32KB 作字典；非末片以 `Flush()` 收尾（留下 `00 00 FF FF` 的缝合点），末片以 `Close()` 收尾；各片 CRC 用 zlib `crc32_combine` 算法拼合。压缩后的片先落到临时文件，再 `CreateRaw` 一次写进归档 |
| 顺序流式 | 文件 > 0x40001 字节且（`--level` 为 0 或 HSA 判定 Store）；或条目名不是合法 UTF-8 | 边读边写（`CreateHeader`），不占内存。名字不合法的条目在这里报错，整档中止 |

**`--jobs` 为 1 时整档都走顺序流式**，连并行结构都不建（core/zip.go:539）；文件读了一半发现超过缓冲上限（收集之后文件变大了）也降级到顺序流式。

并行结构（core/zip.go:506）：

- 工作协程数 = `min(--jobs, 8)`。不按条目数封顶——单文件归档也有分片任务要喂满线程池。
- 窗口信号量容量是「工作协程数 + 2」，限制「已派出、未写出」的任务数，所以常驻内存有上界（约 8 × 256KB 的条目缓冲，加上在飞分片）。
- **确定性**：工作协程只负责算，主协程严格按收集顺序消费结果并落盘。同输入、同机器、同参数，产出字节一致。
- 分块压缩时每个分片打开一次源文件按偏移读，分片读不满或读不到就报错，不硬拼——源文件在压缩途中被改动时 CRC 与尺寸才不会错。

HSA（High Speed Archiving，已压缩格式直通存储）：

- **前置直通**：扩展名在不可压清单里（28 个：`.jpg` `.jpeg` `.png` `.gif` `.webp` `.mp4` `.m4v` `.mov` `.mkv` `.avi` `.mp3` `.m4a` `.aac` `.flac` `.ogg` `.zip` `.gz` `.bz2` `.xz` `.zst` `.7z` `.rar` `.pdf` `.jar` `.dmg` `.iso` `.woff` `.woff2`）**并且**文件头魔数佐证通过，才走 Store（core/zip.go:57、74）。有魔数规则却对不上，说明扩展名被改过，仍然 Deflate；清单里有扩展名但没有魔数规则的只有 `.dmg` 与 `.iso`，它们直接信任扩展名。
- **运行时降档**：只作用于分块并行的文件。设 W 为工作协程数，从第 W + 1 片起，每派一片之前先取一个装配器发来的累计快照；累计未压缩 ≥ 30MiB 且「压缩后 × 100 ≥ 未压缩 × 99」，就把**本文件剩余的分片全部降成 level 0**（deflate 流里的 stored 块，容器不变），且不可逆。文件开头是 `%PDF` 的永不降档。
- **降档为什么不破坏确定性**：决策的输入是「前 k − W 片的累计比值」这个纯函数。装配器每消费一片就发一个带累计值的快照；派发器派第 k 片（k > W）之前必须先收到覆盖前 k − W 片的那个快照，与线程调度的先后无关。

`--level 0` 时全部条目 Store，仍然可被标准工具读取。

条目元数据：条目名 `/` 风格并强制置 UTF-8 标志位（bit 11）；权限取源文件的权限位；修改时间取源文件的修改时间——但并行路径用的 `CreateRaw` 并不把这个字段写进头部，实际时间戳可能是 0【待确认：读了标准库实现，未产出归档用第三方工具查看】（见第 9 节）。归档注释写进 EOCD 的注释字段。

任何一步失败都走 `abortArchive`：关闭并**删除输出文件**，不留半成品（core/zip.go:529）。

#### 4.1.4 compress：zip 加密

只在 `--password` 非空时发生（core/zip.go:725）。

- **格式**：WinZip AES，AE-2 版，固定 AES-256。密钥派生 PBKDF2-HMAC-SHA1 迭代 1000 次；AES-CTR 的计数器是**小端**递增（与 NIST 的 CTR 相反，Go 标准库只支持大端，所以自己实现）；尾部 10 字节 HMAC-SHA1 认证码。条目 method 为 99，extra 字段 0x9901 声明强度与内层方法，CRC32 置 0（由 HMAC 取代），`ReaderVersion` 为 51（7-Zip 严格校验），置加密位。全部用标准库实现，没有第三方依赖。
- **小条目**（≤ 256KB + 1）：先 deflate 到内存，整体加密后 `CreateRaw`。
- **大条目**：先把 deflate 输出落到系统临时目录的溢写文件，再按 128KB 一段流式加密写进归档，内存有界（core/crypto.go:495）。
- **`--jobs` 为 1 时**，无论大小都走大条目那条流式路径（core/zip.go:543）。
- **加密条目不走分块并行**：加密要求一条完整的 deflate 流。
- **逐文件密码**：`--password-table` 命中的条目用表里的密码（2.5）。
- 分卷只是字节切分，加密在切分之前已经完成。

#### 4.1.5 compress：tar 系写入

tar 条目由单个协程顺序写入，内存有界（并行只发生在 gzip 压缩层，core/tar.go:170）。

- **PAX 格式，确定性优先**：头部是手工构造的，不用 `archive/tar` 的 `FileInfoHeader`——后者会把 uid、gid、访问时间这些跟机器相关的字段带进头。手工头的字段：条目名（`/` 风格）、权限位、修改时间**截到秒**（亚秒值会被 PAX 写成扩展记录，不同文件系统的 stat 精度不同就会让同输入产出不同字节）、格式 PAX、普通文件、大小；uid、gid、用户名、组名都是零。超过 100 字节的名字走 PAX 长名记录。
- **符号链接原样保留**：类型为符号链接，目标名放进 `Linkname`，没有数据体。
- **压缩壳**：

| `--output` | 壳 | `--jobs` 的作用 | `--level` 的作用 |
|---|---|---|---|
| `.tar` | 无 | 无 | 无 |
| `.tar.gz` / `.tgz` | `--jobs` > 1 时并行 gzip；否则 klauspost 单流 gzip | 决定走哪条路径，以及并行协程数 | gzip 档位 |
| `.tar.xz` / `.txz` | ulikunitz/xz，默认参数 | 无 | **无效** |
| `.tar.zst` / `.tzst` | klauspost/zstd，默认参数 | 无 | **无效** |
| `.tar.bz2` / `.tbz2` | 不支持 | — | — |

- **并行 gzip**（core/gzip_parallel.go）：tar 字节流按 128KB 切块，各块独立 deflate，**不做字典衔接**（gzip 是一条连续流，跨块字典引用在拼接流里可能越界；与 pigz 的 `-i` 独立模式同方案，比值损失不到 0.1%）；非末块 sync-flush，末块 final；外面是标准的 10 字节 gzip 头（mtime 恒 0，OS 字节 255）加 8 字节尾（CRC32 + 原长）。
- **对 tar 无效的旗标**：`--password`、`--password-table`、`--comment` 都不产生任何效果，**也不提示**。
- 条目写入中途失败：删掉输出文件，报 `写入文件失败: <原因>`。

#### 4.1.6 compress：7z 写入

压缩与打包策略全部交给官方 7-Zip（LZMA/LZMA2、多线程），Go 这一层只做接线（core/sevenz.go:27）：

1. 检查引擎可用；不可用直接报「需完整版」。
2. 用与 zip / tar 同一套收集逻辑拿到条目（排除规则、默认垃圾同样生效）。零条目报错。
3. 输出文件已存在就先删。
4. **选工作目录**——7z 存进归档的名字是相对当前目录的路径，绝对路径会被它截成只剩文件名、丢掉目录树，所以：
   - 快路径：如果存在一个公共基目录，使每个条目「相对它的路径」恰好等于条目名（单个目录输入、同一目录下的多个散文件都是这种形态），就直接切到那个目录，零拷贝。
   - 兜底：在系统临时目录建暂存目录，把整棵目标树用**硬链接**克隆一份（跨设备时退化为拷贝），再切进去。
5. 把条目名写进工作目录下的一个清单文件，然后调用引擎：`a -bd -bso0 -y -spd -scsUTF-8 -mx=<--level> [-p<密码> -mhe=on] <绝对输出路径> @<清单>`。`-spd` 让清单里的名字按字面量处理（文件名里的 `*` `?` 不被当通配符），`-scsUTF-8` 指定清单字符集；`-mhe=on` 是头部加密，连文件名也加密。
6. **退出码**：0 成功；1 是警告，容忍并打一条日志；≥ 2 是失败，带上引擎输出的末 8 行报错，并删除半成品归档。
7. 结果：`archive` 是绝对输出路径，`file_count` 是条目数，`size_bytes` 是归档大小。

要点：

- **切换工作目录是进程级状态**，7z 写入全程持一把包级互斥锁串行化。
- 被 7z 忽略的旗标：`--jobs`（静默）、`--password-table`（打日志提示）、`--comment`（打日志提示）。
- 空目录不保留（收集阶段就没有目录条目）。
- `--volume-size` 由通用切分器在 7z 写完之后处理。
- 引擎写到 stderr 的内容被实时转发到真正的 stderr，终端与 GUI 都看得到。
- 7z 引擎正常返回退出码，不会像 ffmpeg 那样在 `main` 里 `exit()`。

#### 4.1.7 compress：分卷

归档**完整写好之后**才切（core/core.go:138、156），与格式无关——zip、tar、tar.gz、tar.xz、tar.zst、7z 都一样。

- **下限 64KB**（65536 字节），低于就报错；此时刚压完的归档会被一并删除。
- **整档不超过单卷大小时不切分**：保持原文件名，不产一个孤零零的 `.001`，只打一条日志。
- 真切时：输出 `<路径>.001`、`.002`……三位序号，每卷正好 `--volume-size` 字节，末卷是余量；`cat` 拼起来就是原归档。原归档在全部卷写好、句柄关闭之后删除。
- 结果里 `archive` 是首卷路径，`size_bytes` 是全卷总字节数。
- 中途失败时已写出的卷段全部清理。已存在的同名卷文件会被直接覆盖。
- `--volume-size` 的解析在引擎之外：模板按 SI 十进制换算，数字后缀按 1024 基换算，两套并存。

#### 4.1.8 extract：读格式靠魔数识别

归档路径以 `.001` 结尾（大小写不敏感）时，先把 `base.001`、`base.002`、`base.003`……逐段找出来，遇到缺号为止，拼成一个逻辑上的单文件再做下面的识别；只有 `.001` 一段也成立（core/split.go:32）。

读文件开头 512 字节，按下面的次序判定，首个命中生效（core/format.go:54）：

| 次序 | 判定依据 | 结果 | 备注 |
|---|---|---|---|
| 1 | `PK\x03\x04`、`PK\x05\x06` 或 `PK\x07\x08` 开头 | zip | |
| 2 | `1F 8B` | tar.gz 或 gzip 单流 | 解开头一段，偏移 257 处是 `ustar` 就是 tar.gz，否则是单流 |
| 3 | `BZh` | tar.bz2 或 bzip2 单流 | 同上 |
| 4 | `28 B5 2F FD` | tar.zst 或 zstd 单流 | 同上 |
| 5 | `FD 37 7A 58 5A 00` | tar.xz 或 xz 单流 | 同上 |
| 6 | `04 22 4D 18` | lz4 单流 | 内层即使是 tar 也没有对应的 tar 壳格式，会被误当成 zip（见第 9 节） |
| 7 | `37 7A BC AF 27 1C` | 7z | |
| 8 | `Rar!\x1a\x07` | rar | |
| 9 | `1F 9D`，且第 3 字节的 `0x60` 位全为 0 | Z（UNIX compress） | |
| 10 | `LZIP` | lzip | |
| 11 | `MSWIM\0\0\0` | wim | |
| 12 | `MSCF` | cab | |
| 13 | `!<arch>\n` | ar | |
| 14 | 偏移 257 处是 `ustar` | tar | 覆盖 ustar / PAX / GNU 三系 |
| 15 | 偏移 32769（0x8001）处是 `CD001` | iso | |
| 16 | 文件第一行以 `begin ` 开头 | uuencode；扩展名是 `.xxe` 则 xxencode | 两种编码只差字母表，靠扩展名区分。`begin` 必须在文件最开头，前面有邮件正文的不认 |
| 17 | 扩展名 `.br`，且试解开头不报错 | brotli | 无魔数，扩展名门控 + 试解 |
| 18 | 扩展名 `.asar`，且头部结构合法 | asar | 无魔数，扩展名门控 + 结构校验 |
| 19 | 兜底：标准 zip 读取器能打开 | zip | 自解压 exe 这类「前面有壳」的 zip 从尾部 EOCD 定位 |

全都不中就报 `无法识别的归档格式`。扩展名只在第 16–18 步起作用，其余情况改名照样按内容识别。

#### 4.1.9 extract：各格式的落地管线

| 格式 | 访问方式 | 条目何时校验 | 并行 | 密码 | 专属行为与已知限制 |
|---|---|---|---|---|---|
| zip | 随机访问（普通文件或分卷拼接源） | 先扫完中央目录，再动工 | `min(max(--jobs,1), 32)`，且不超过条目数 | 逐条目：WinZip AES-128/192/256 与 ZipCrypto | zip64 自动；空目录条目照建；GBK 文件名修复；自解压包可读 |
| tar、tar.gz、tar.xz、tar.bz2、tar.zst | 顺序流，**读两遍**（预扫、解出） | 第一遍全验，第二遍才落盘 | 不并行 | 不支持 | 符号链接保留；硬链接按归档内成员拷贝落地；设备、管道、未知类型条目拒绝；GNU sparse 透明读取；PAX 全局头跳过 |
| 7z | 随机访问 | 先扫完目录，再动工 | 文件级并行，同 zip 的公式 | 整档一个，含头部加密 | 符号链接按普通文件落地【待确认】 |
| rar | 顺序流，读两遍 | 同 tar | 不并行 | 整档一个 | 自家多卷命名自动续卷；符号链接按普通文件落地；混合密码档案预扫降级（见下） |
| wim | 随机访问 | 遍历时校验 | 不并行 | 不支持 | 多镜像归档按 `N-镜像名/` 前缀平铺；LZX 按需解码。LZMS/ESD、XPRESS、重解析点、硬链接、ADS 库不支持，错误原样透传 |
| cab | 随机访问 | 解析时校验 | 不并行 | 不支持 | 存储柜直拷、MSZIP、LZX（含 Intel E8 翻译）；Quantum 明确报不支持；跨柜多卷不支持。每个文件夹整个解码到临时文件，条目从中直读 |
| ar | 随机访问（纯偏移计算） | 解析时校验 | 不并行 | 不支持 | GNU（`name/`、`//` 长名表、`/N` 索引）与 BSD（`#1/len`）两套命名；符号表条目跳过；没有目录条目 |
| asar | 随机访问 | 解析时校验 | 不并行 | 不支持 | `.asar` 扩展名门控；`unpacked` 条目从兄弟目录 `<归档>.unpacked/` 读，缺了报错；只有文件条目，**空目录不会重建**；权限固定 0644 |
| iso | 随机访问 | 遍历时校验 | 不并行 | 不支持 | Rock Ridge 名生效；Joliet 名不读，按基础层读（局限） |
| gzip、bzip2、zstd、xz、lz4、brotli、Z、lzip | 顺序流 | — | — | — | 不是容器，解出**一个文件**：文件名 = 归档基名去掉 `.001` 后再剥一个已知扩展名（复合后缀优先，列表见 core/format.go:185）；直接落 `--output` 或当前目录，不走智能解压 |
| uuencode、xxencode | 顺序流 | — | — | — | 同上，但文件名优先取 `begin` 行里的内嵌名（只窥探前 32 行）；内嵌名先过安全检查，只取文件名部分、不建子目录；取不到再按基名推导 |
| `.001` 分卷 | 拼接源 | — | 随内层 | 随内层 | 内层认不出时按原始字节拼接，还原成单个文件（名 = 去掉 `.001`） |

共通的落地规则：

- **先全验后动工**：路径穿越、符号链接或硬链接逃逸、设备条目、重复条目名，都在写第一个文件之前检查完。tar 和 rar 顺序流没有随机访问，所以宁可多读一遍。
- **权限**：取条目的权限位；为 0 时用 0644。
- **rar 的混合密码档案**：预扫时遇到密码类错误就停止扫描，带着已经读到的条目继续（打 `部分加密 rar` 日志）；第二遍解出时再遇到密码类错误就**正常收尾**，后面的条目不再解，返回成功。
- **rar 的多卷**：普通文件源走库的 `OpenReader`，`.part1.rar → .part2.rar`、`.rar → .r00` 这些命名自动续卷；`.001` 拼接源则当成单卷流。
- **7z、rar 的密码表**：只有能命中 `*` 的规则才会被取用，作为整档密码（core/sevenzip.go:21、core/rar.go:81）。

#### 4.1.10 extract：智能解压与覆盖

- **给了 `--output`**：直接解到那个目录，不存在就创建。
- **没给 `--output`**（unar 语义，core/common.go:185、211）：
  - 归档里所有条目共享同一个顶层名字，并且顶层没有散装文件 → 落到**当前目录**，条目自带的顶层目录就是唯一的「新目录」。
  - 否则（顶层有散装文件，或有多个顶层）→ 落到 `当前目录/<归档基名>/`，避免解开后散落一地。归档基名 = 先去掉 `.001`，再剥一个已知的归档或压缩扩展名（`.tar.gz` 这类复合后缀优先剥）。
- 单流格式与 uu / xx 编码不走智能解压。
- **覆盖语义**：缺省**跳过已存在**的条目，每个记一次 `skipped`，打一条日志，已改动过的文件不会被覆盖。跳过是原子的——用 `O_EXCL` 打开，已存在就失败，没有「先查再写」的时间窗。`--force` 改为截断重写。符号链接没有 `O_EXCL` 可用，靠「已存在」错误码判定；`--force` 时删旧重建。目录条目总是创建，不参与跳过或覆盖。

#### 4.1.11 convert

管线是「源归档 → 临时目录解压 → 按输出扩展名重新压缩 → 清理临时目录」（cli/archive/convert.go:120）：

1. 校验源归档存在，确定输出路径与格式（2.4）。
2. 在临时目录（`--temp-dir` 指定的基目录，或系统临时目录）下建 `zpack-convert-*` 中转目录。
3. 用 `extract` 的引擎把源归档解到中转目录：强制覆盖模式、带 `--password`、不带密码表、不指定并行度。
4. 把中转目录的**第一层每一项**（文件和目录都算）作为输入交给 `compress` 的引擎，`--level` 取命令行的值，并行度为 0（即 zip 走顺序路径、tar.gz 走单流 gzip）。
5. 输出结果；中转目录在管线闭包返回时删除。

注意：

- 输出归档的根布局取决于源归档：源是单根归档，输出就以那个根目录名为前缀；源是散装归档，输出的条目平铺在包根。
- 中转时文件刚落盘，它们的修改时间就是**转换发生的时刻**，会被写进 tar 的头部（见第 6、9 节）。
- 源归档里的符号链接：在能建符号链接的平台上中转后仍是符号链接，tar 输出会保留它，zip 输出解引用成文件。

#### 4.1.12 list / test / info

三个命令共用一个读取基座 `ListArchive`（core/list.go:41）：打开源（`.001` 分卷同样支持）、魔数识别、按格式取条目清单、聚合统计。

- **`list`** 输出聚合统计加 `entries` 明细。
- **`info`** 输出同样的聚合统计，不带 `entries`。
- **`test`** 先调 `ListArchive`，再按格式校验：
  - zip：逐条解压读到底，靠 `archive/zip` 自动验 CRC；**加密条目跳过不校验**。
  - tar 系：完整走读到结尾，解压层的 CRC 在读到尾时被触发。
  - 7z、rar：只要求结构解析成功，**不校验数据**。
- 只支持 zip、tar 系、7z、rar；其余格式报 `暂不支持列出 <格式名> 格式内容`。

#### 4.1.13 bench

- 在临时目录生成语料：一半可压文本（`text.bin`，反复写同一行），一半密码学安全的随机数（`random.bin`）。
- 依次跑 8 行：zip 档位 1 / 6 / 9，tar.gz 档位 1 / 6 / 9，`tar.gz 并行` 档位 6，tar 档位 0。每行压缩一次、解压一次并计时；压缩引擎的并行度是 1（标着「并行」的那行传的是 0）。
- 某一行压缩失败就跳过这一行；解压失败不影响结果。
- 跑完删除整个临时目录，输出结果。

### 4.2 边界与退化输入

| 情形 | 行为 | 出处 |
|---|---|---|
| 输入目录是空的，或里面的东西全被排除 | zip、tar 系：成功，产出空归档，`file_count` 为 0。7z：报 `没有可压缩的文件（输入为空或全部被排除）` | core/zip.go:807、core/sevenz.go:37 |
| 归档里只有目录条目（或是空归档） | `extract` 成功：目录条目照建，`file_count` 为 0 | core/zip.go:827 |
| 0 字节文件 | 正常入包、正常解出 | — |
| tar 条目名超过 100 字节 | 走 PAX 长名记录，往返无损 | core/tar.go:289 |
| 文件名含空格、中文 | 路径经 argv 直传；条目名恒 `/` 风格；zip 强制 UTF-8 标志位 | core/common.go:135 |
| 归档扩展名被改掉 | 照样按内容识别（只有 brotli、asar、xxencode 三种靠扩展名门控） | core/format.go:110、118、124 |
| 整档不超过单卷大小 | 不切分，保持原文件名 | core/core.go:171 |
| `--volume-size` 换算后低于 64KB | 报错，刚压完的归档被删除 | core/core.go:157 |
| `--level 0` | zip 全部 Store（仍可被标准工具读取）；tar.gz 是仅存储的 gzip | core/zip.go:187 |
| `--jobs 1` | zip 走顺序流式；tar.gz 走单流 gzip | core/zip.go:539、core/tar.go:185 |
| 超过 256KB + 1 字节的可压大文件 | zip 走 128KB 分块并行；缝合点不少于「大小 ÷ 128KB」量级；两次产出字节一致 | verify/verify.sh:122 |
| 32MB 不可压大文件 | 走到 HSA 运行时降档的路径：两次产出字节一致，可解回（门禁不断言降档确实发生） | verify/verify.sh:216 |
| 加密时条目超过 256KB | 走流式加密，产物真加密：无密码解压被拒，有密码内容一致 | core/crypto.go:495 |
| 分卷 + 加密 | 加密在切分前完成，从 `.001` 解压同样要密码 | core/core.go:138 |
| 伪造的 7z / rar 魔数 | exit 1，`无法读取 7z 归档: ...` / `无法读取 rar 归档: ...` | core/sevenzip.go:33 |
| 内容认不出（含把目录、纯文本当归档） | exit 1，`无法识别的归档格式: ...` | core/format.go:134 |
| 条目名含 `../`、绝对路径、盘符 | 拒绝解压，目标目录之外不产生任何文件 | core/common.go:149 |
| tar 的符号链接目标逃出归档根、设备 / 管道条目 | 拒绝解压 | core/tar.go:108、120 |
| tar 的硬链接 | 把归档内的目标成员复制一份落地（不用 `os.Link`） | core/tar.go:444 |
| 两个非目录条目同名 | 拒绝解压 | core/zip.go:959 |
| 加密 zip 不给密码 | exit 1，`归档已加密（…）：需要 --password 提供密码` | core/zip.go:887 |
| 加密 zip 密码错误 | exit 1，`解压失败: 写出 <条目> 失败: 密码错误或归档损坏` | core/zip.go:1035 |
| 多条目加密包 + 错密码 + 高并行 | 一条 error JSON、exit 1；不会 panic（2026-11-24 修复） | core/zip.go:1017 |
| `--output` 是 `.tar.bz2` / `.tbz2` | 报 `暂不支持 tar.bz2 写入`，**残留一个 0 字节的输出文件** | core/tar.go:177、219 |
| `--output` 是 `.7z`，而构建里没有 7z 引擎 | 报 `zpack 7z 写入需完整版（CGO 构建 + 7z 静态产线）` | internal/sevenzipcli/cli_stub.go:9 |
| 要入包的文件名不是合法 UTF-8 | 报错并中止，半成品删除 | core/zip.go:173、core/tar.go:282 |
| 归档里的文件名既无 UTF-8 标志又不是合法 UTF-8 | 按 GBK 尽力解码并逐条打日志；非法字节替换成 U+FFFD，不报错（非 GBK 的名字也走这条启发式，是已知局限） | core/common.go:169 |
| 密码表读不到 / 有格式错误的行 / 没有有效行 | 三条各自的 error_info（3.7） | cli/archive/password_table.go:17 |
| convert 的输出扩展名是 `.7z`、`.tar.bz2` 或别的 | 报 `输出格式暂不支持（支持 .zip/.tar/.tar.gz/.tar.xz/.tar.zst）` | cli/archive/convert.go:106 |
| convert 的源归档解出来什么都没有 | 报 `源归档为空，无法转换` | cli/archive/convert.go:159 |
| `list` / `test` / `info` 遇到四类之外的格式 | 报 `暂不支持列出 <格式名> 格式内容` | core/list.go:64 |
| `list` / `info` 遇到加密了头部的 7z 或 rar | **成功**，但条目数是 0、`encrypted` 是 false（读取基座不带密码，失败被吞掉了） | core/list.go:149、167 |
| `test` 遇到同样的归档 | 判定类失败：`归档为空或无法解析` | core/list.go:85 |
| `test` 遇到 `.001` 分卷归档 | 校验阶段**绕过了分卷拼接源**，只打开首卷那一个文件：多卷的 zip 找不到中央目录，报 `无法打开归档: ...`；多卷的 tar 读到首卷末尾就出错。`list` 不受影响 | core/list.go:198、225 |
| bench 的 `--size` 不是整数或不在 1–2000 | 报 `--size 需要 1-2000 的整数（MB），收到: <原值>` | cli/archive/bench.go:61 |

### 4.3 错误处理

- **压缩：中途失败删半成品**。zip 走 `abortArchive`，关闭并删除输出文件；tar 系在条目写入失败或收尾失败时都删除输出文件；7z 在引擎退出码 ≥ 2 时删除输出文件；分卷失败时删除归档和已写出的卷段。唯一的例外是 tar.bz2（见上表）。
- **解压：已写出的文件不回滚**。落盘失败时，之前解出的文件留在盘上，返回 `解压失败: ...`。所以 `file_count` 只在成功时才有意义。
- **并行解出：首错即停**。zip 与 7z 的并行管线里，任何一个工作协程失败就关闭取消通道：其余协程在下一个检查点退出，主循环停止投喂，已经在写的条目自然收尾。对外只报第一个错误。
- **取消**：各条管线的实际表现见 3.7 的表——并不一致，这是已知问题（第 9 节）。
- **convert 失败**：中转目录在闭包返回时删除；目标归档写入失败时额外删除目标文件。
- **bench**：某一行压缩失败就跳过这一行；解压失败不影响该行的比值数据；这两种失败都不影响最终 JSON 的成功输出。
- **`list` 不报读损坏**：zip、7z、rar 的清单读不出来时返回空清单；tar 走读到一半出错时**静默返回已经读到的条目**（core/list.go:133）。所以截断的 tar 用 `list` 看是「成功但条目偏少」，要用 `test` 才能发现。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 7z 写入（`--output *.7z`） | **不可用**：报 `zpack 7z 写入需完整版（CGO 构建 + 7z 静态产线）`。完整版 `bin/zpack-cli.exe` 也一样——引擎文件的构建约束是 `cgo && darwin`，其余平台一律编译桩（internal/sevenzipcli/cli_cgo.go:1、cli_stub.go:1） | 完整版（cgo 构建）可用；纯 Go 开发构建报同样的话 | 不可用，同 Windows（产线未铺） |
| tar 解压遇到符号链接 | 创建符号链接需要特权。没开开发者模式、也不是管理员时失败（错误码 1314），**降级为拷贝归档内的目标文件**并打日志；有特权时与 mac / Linux 相同 | 创建符号链接，无需特权 | 同 macOS |
| 硬链接（tar 解压） | 复制目标成员的内容（不用 `os.Link`，所以无特权也能用） | 同左 | 同左 |
| tar 压缩遇到符号链接 | 三平台代码相同：读出链接目标写进头，保留为符号链接【待确认：Windows 上未实测】 | 保留 | 保留 |
| zip 压缩遇到符号链接 | 三平台相同：按目标文件的内容存（解引用） | 同左 | 同左 |
| 条目权限位 | 只有只读位有意义（条目带只读位时解出的文件是只读的） | 完整的权限位 | 完整的权限位 |
| 文件名不是合法 UTF-8（压缩输入） | 罕见（NTFS 名字是 UTF-16，基本都能转成合法 UTF-8）；出现即报错 | 罕见；出现即报错 | 常见（名字是任意字节）；出现即报错，整档中止 |
| 解出的文件名含本平台非法字符 | **没有做文件名清洗**：含 `:` `*` `?` 之类字符的条目在 Windows 上应当落盘失败，报 `解压失败: 写出 <条目> 失败: ...`【待确认：按代码推断，未实测】 | 基本都能落盘 | 基本都能落盘 |
| 临时目录缺省位置 | `%TMP%` → `%TEMP%` → 用户目录，几乎总在 C 盘；大转换建议 `--temp-dir` 指到目标盘 | `$TMPDIR`，否则 `/tmp` | `$TMPDIR`，否则 `/tmp` |
| 删除被打开的文件 | **不允许**。「关闭必须先于删除」是硬约束，`defer` 的顺序不算数 | 允许 | 允许 |
| 路径分隔符 | 条目名、glob 匹配恒在 `/` 坐标系里做；只有碰文件系统时才经 `filepath.FromSlash` 转换 | 天然一致 | 天然一致 |
| 验收脚本 | 可以跑，但需要 `stat` 垫片，且有一部分用例因脚本坐标系、符号链接特权、长路径等环境原因失败（见第 8 节） | 全绿基线 | 未实测 |

**纯 Go 构建与完整版的差别**：本功能里**只有 7z 写入**依赖 cgo 构建。zip、tar 系的全部读写、加密、分卷、其他所有格式的读取、convert、list / test / info、bench 都是纯 Go，两种构建下行为相同。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 总体决策与它们的来由

- **单引擎双面孔**（2026-11-02 用户定案）：CLI 与未来的 GUI 是同一个 `core` 引擎的两张面孔。分层铁律——`core` 不 import cobra、不碰 stdout、不 `os.Exit`，错误一律用 error 返回，过程日志经 `Logf` 回调注入（传 nil 即静默）。所以 GUI 任何时候都可以改成进程内直连引擎，而不用重写算法。
- **扁平命令**（2026-11-02 用户拍板）：没有「格式子命令」，`zpack compress/extract <任意输入>`。写格式由 `--output` 扩展名决定，读格式靠魔数识别；旧的 `zpack zip …` 形态已退役。好处是调用方不需要先知道格式，Agent 少一层分支。
- **格式策略**（2026-11-02 定案）：读格式尽可能多，判断标准是**开源实现或公开规格的可得性**，而不是格式出身。rar 是「专有出身的例外」（事实标准 + nwaples/rardecode 是 MIT）；**rar 永远不写**是法律约束，不是技术取舍。
- **不做私有格式**：互操作就是护城河。产出的 zip / tar / 7z 必须能被系统工具和主流压缩软件打开——验收里大量使用系统 `unzip`、`tar` 做交叉验证就是这条的落地。
- **7z 全量接入官方 C++**（2026-11-22 用户拍板）：7z 的压缩与打包策略不去自己实现，直接把官方 7-Zip（ip7z 25.01 的 Alone2 bundle）的 `main` 改名后链进进程里当普通 C 函数调用，与 ffmpeg 直调是同一个模式。Go 这层只管接线。许可上，unRAR 的限制代码不允许进入产物——`Rar*.o` 在归档阶段被剥除。

### 6.2 输出契约层面的坑

- **print 即 exit，外层 defer 不运行**。`stdout.JSONPrint` 输出后立即 `os.Exit`。convert 的中转目录曾经在成功路径上静默泄漏，就是把 `defer os.RemoveAll` 写在闭包外面导致的（2026-11-04 修复，清理行为已入验收 verify/verify.sh:829）。现在的写法是把整条管线放进闭包、`defer` 写在闭包里（cli/archive/convert.go:136）。bench 则是在 `JSONPrint` 之前显式 `RemoveAll`（cli/archive/bench.go:159）。**新加任何清理逻辑都要想着这一点。**
- **引擎的错误文案就是对外的 `error_info`**。改 `core` 里任何一条文案都要同步改 `verify/verify.sh` 的预期值，否则门禁直接红。
- **`--help` 必须手动守卫**。`DisableFlagParsing` 吞掉了 cobra 的自动 help；2026-11-04 之前 `zpack compress --help` 报的是参数错误。Agent 嗅探一个 CLI 的第一步就是 `--help`，这里断了后面全断。
- **数值严格校验是一点点补齐的**。`bench --size` 此前用 `Sscanf` 只看前缀，`5abc` 被当成 5；2026-11-24 改用 `ParseIntStrict`（提交 6016f9b，门禁 verify/verify.sh:1173）。**`convert --level` 是漏网之鱼**，非法值至今静默回退 6（第 9 节）。
- **过程日志的去向按「本次调用的参数」判定**。`cliutil.EngineLogf(args)` 看到 `--quiet` 就返回 nil，否则接 stderr（internal/cliutil/logf.go:21）。不再用包级变量：命令面按功能拆成子包之后各命令各管各的，而且一个进程只执行一条命令，全局状态本来就是多余的耦合（提交 6d31586、40fb9e7）。同一次拆包里 pdf 与 audio 的 `--quiet` 才真正开始生效。
- **命令面里还残留的包级变量**：`compressComment` 与 `commentFilePath`（cli/archive/compress.go:47）。它们是同一个味道的遗留——`extract` 的解析器也在往里写。进程只跑一条命令时无害，做进程内复用时要注意。

### 6.3 zip 并行架构：为什么这样写

- **「工作协程只算、主协程按序落盘」是确定性的来源**。工作协程把条目或分片压成字节放在通道里，主协程严格按收集顺序取出来 `CreateRaw` 写进归档。谁先算完不影响归档里的先后。门禁用「同输入两次产出 `cmp` 一致」守着（verify/verify.sh:146、222）。
- **分块并行的阈值是 0x40001（256KB + 1）**，对齐业界主流压缩器的多线程 deflate 切换点。`core/zip.go` 文件头注释和 AGENTS.md 里写的「>16MB」是旧值，以常量为准（core/zip.go:31）。verify.sh 里那条用例名也还叫「>16MB 触发」。
- **分块之间用字典衔接**：每片 deflate 之前预置前一片结尾最多 32KB。连续解码器的滑窗里本来就含这些同源字节，所以跨片回引是合法的。单测 `TestZipChunkDictionaryLinkage` 用「随机底座 + 重复尾」证明衔接确实生效——失效的话归档会是 1.6MB 量级而不是 400KB 量级。
- **分片的 CRC 用 zlib 的 `crc32_combine` 算法拼合**，不用为了算 CRC 再读一遍盘。
- **并行度不按条目数封顶**：单文件归档也有几百个分片任务要喂满线程池，空闲协程在通道关闭后自己退出，没有代价。
- **窗口信号量**（容量 = 工作协程数 + 2）把「已派出未写出」的任务数钉死，这是「长跑不涨内存」的界。没有预填充：主循环要弹令牌时必定有令牌可弹，产线占满就等，不会死锁。
- **klauspost 的 flate 是逐个 `zip.Writer` 注册的**，不动包级注册，免得影响进程里其他用 `archive/zip` 的调用方（core/zip.go:516）。
- **HSA 的两条线**：前置直通靠「扩展名 + 魔数佐证」双重确认，防止把改错了扩展名的可压文件当 Store 存进去反而变大；运行时降档对付的是「扩展名看不出、压到 30MiB 才发现不可压」的大文件。
- **降档的确定性靠快照信号**。最自然的写法是派发器随时去读装配器的累计值，但那样决策取决于线程调度，同输入可能产出不同字节。现在的做法是把决策输入钉成一个纯函数：装配器每消费一片就发一个带累计值的快照，派发器派第 k 片之前必须先收到对应的那个快照。通道容量等于工作协程数，装配器发快照永不阻塞（core/zip.go:343）。
- **tar.gz 的并行 gzip 故意不做字典衔接**（core/gzip_parallel.go:152）。zip 的分块是拼进同一个条目的 deflate 流，解码器滑窗连续；gzip 分块并行后拼成一条流，跨块字典引用的合法性没有保证，所以选择独立块（pigz 的 `-i` 模式同方案），用不到 0.1% 的比值损失换正确性。
- **AGENTS.md 说「tar 单流顺序写、`--jobs` 不参与」，这已经过时**。`--jobs` > 1 时 tar.gz 走并行 gzip（core/tar.go:185）。`core/tar.go` 的 `tarCompress` 注释里也还是旧说法。
- **并行 gzip 写入器里有一处数据竞争**【待确认：代码审阅结论，未用 `-race` 实测】：工作协程读 `outcomeChans` 这个 map 时没有加锁（core/gzip_parallel.go:182），而主协程在持锁修改它（core/gzip_parallel.go:161、214、233）。Go 的 map 并发读写是未定义行为，运行时可能直接 `fatal error: concurrent map read and map write`——不可恢复、exit 2、stdout 为空。见第 9 节。
- **两条写入路径的条目时间戳不一致**【待确认：代码审阅结论，未实测】。并行路径的条目头只设了 `Modified`，而标准库的 `CreateRaw` 写的是 `ModifiedDate` / `ModifiedTime` 两个字段，不读 `Modified`——所以小条目和分块条目的时间戳字段应是 0。顺序路径用 `FileInfoHeader` 构造头，两个字段都被填上，时间是真实的。另外 DOS 时间是**本地时区**，所以「产出字节确定」只在同一台机器上成立，换时区的机器会产出不同的字节。tar 没有这两个问题（头部存的是 UTC 纪元秒）。见第 9 节。
- **进度条在 zip 分块路径上口径不一**：小条目按输入字节累计（core/zip.go:765），分块条目按压缩后字节累计（core/zip.go:773），而总量是输入字节。大文件的进度会显得偏慢。

### 6.4 zip 加密：血案与设计

- **血案 m03442**：带密码压缩一个超过 256KB 缓冲上限的文件，条目被降级到顺序路径，而那条路径当时不包 AES——产出的是**明文裸奔的归档，且零警告**。修复后：加密条目无论多大都走流式加密路径，绝不回退明文（core/crypto.go:490）；顺序路径（`--jobs 1`）也接上了加密。回归有两层：单测 `TestZipPasswordLargeEntryEncrypted`（并行、顺序、分卷 + 密码三种组合，断言「无密码解压必须失败」）和门禁（verify/verify.sh:708）。**任何改动「降级」「顺序路径」的代码都要把这条再想一遍。**
- **读取侧的桥接码**：`archive/zip` 的解码器按 method 号注册，没有「带密码打开某个条目」的入口。做法是在预扫阶段识别出加密条目，把内存里的 method 改写成一个不落盘的桥接码（`0x7000 | 强度 << 8 | 内层方法`，或 `0x7100 | 内层方法`），再给这个桥接码注册「解密 + 内层解压」的解码器。磁盘上的文件不动。
- **两处容易写错的细节**：WinZip AES 的 CTR 计数器是**小端**递增，Go 标准库只支持大端，所以自己实现了一个；ZipCrypto 的密钥更新用的是**不带取反**的原始 CRC32 值，不能直接用 `crc32.Update`（当初靠 Info-ZIP 交叉验证才定位到）。
- **密码正确性怎么判**：WinZip AES 先比 2 字节密码校验值，错就立即报 `密码错误或归档损坏`；尾部的 10 字节 HMAC 是尽力校验（内层流在最后一个块之后不再拉取数据时不触发）。ZipCrypto 比 12 字节加密头的最后一字节。
- **密码表在 zip 读取上其实不成立**【待确认：代码审阅结论，未实测】。桥接解码器按桥接码注册，**只在第一次遇到某个桥接码时注册**，闭包里捕获的是那第一个条目的密码（core/zip.go:896）。同一归档里两个条目只要「方案、强度、内层方法」相同，就共用第一个条目的密码。AGENTS.md 说「zip 逐文件独立生效」，单测 `TestZipPasswordTable` 验证的也只是「两个归档各有各的密码」。见第 9 节。
- **压缩侧的三个边角**：单给 `--password-table` 不给 `--password` 时根本不加密（按代码确定）；被 Store 的条目（`--level 0` 或 HSA 直通）加密后，extra 里声明的内层方法却写死为 deflate【待确认：按代码推断，未实测这些条目是否真的解不开】；密码表给出空密码时小文件按空密码加密、大文件写明文（按代码确定）。见第 9 节。

### 6.5 tar 系：确定性与两遍读

- **确定性纪律**：PAX 格式、uid / gid / 用户名 / 组名归零、修改时间截秒、权限只取权限位。头部手工构造，因为 `FileInfoHeader` 会把本机相关的字段带进来。门禁「同输入两次 tar.gz 产出字节一致」守着这条（verify/verify.sh:304）。
- **为什么要读两遍**：tar 是顺序流，gzip 层没有随机访问——想跳过一个条目的数据，也得把它解压一遍。可是安全纪律要求「先全验后动工」——不可能写了一半才发现里面有恶意条目。于是预扫一遍把头都读出来、再重开一遍落盘。代价是解压时间翻倍，这是有意的取舍。
- **tar.bz2 的「不支持」留了个尾巴**：`os.Create` 写在格式分派之前，所以报错时输出文件已经被创建（已存在的旧归档也被截空），而且句柄没关、文件没删（core/tar.go:177、219）。见第 9 节。
- **tar.xz / tar.zst 的产出没有确定性验收**，`--level` 对它们也不生效。

### 6.6 7z 写入：接线里的每一处都有原因

- **路径坐标系是最大的坑**。7z 存进归档的名字是相对当前目录的路径；直接喂绝对路径，它会把路径截成只剩文件名，整棵目录树就丢了。所以要么切到公共基目录（快路径，零拷贝），要么建暂存目录把树克隆成「条目名即相对路径」的形状。
- **切换工作目录是进程级状态**，必须用一把包级互斥锁把 7z 写入串行化；切回去的 `defer` 注册在清理暂存目录之前，所以清理时当前目录已经不在暂存目录里——在 Windows 上当前目录在里面的目录是删不掉的。
- **`a` 命令对已存在的归档是「更新」**，会把新文件并进旧归档。本工具的契约是覆盖，所以先删。
- **`-spd`**：清单里的名字按字面量处理，否则文件名里的 `*` `?` 会被 7z 当通配符展开。
- **退出码**：7z 用 1 表示警告（例如某些文件被跳过），不是失败；≥ 2 才是失败。
- **进度**：7z 在非终端下零输出，所以 Go 侧每 5 秒轮询归档文件大小来报进度。
- **stderr 接管**：引擎直接写 fd 2，Go 侧临时把 fd 2 换成管道，逐行捕获后再写到真正的 fd 2——终端和 GUI 子进程都不会失去 7z 的输出。恢复顺序是先恢复 fd 2 再关写端，读协程才会收到 EOF。
- **产线四个坑**（AGENTS.md「7z 写入直调引擎」节）：① 7z 的格式与编码器注册靠静态构造函数，普通的 `.a` 按需拉取拉不到这些对象，命令全部 `E_NOTIMPL`——要把全部对象用 `ld -r` 合并成单一成员归档，等价于 whole-archive；② `DISABLE_RAR` 只被 `Format7zF` 的 makefile 认，Alone2 的 makefile 无视它——所以只能在归档阶段剥除 `Rar*.o`；③ `USE_ASM=1` 与 clang 的 `-Weverything` 各有冲突，要双覆盖；④ `main` 改名后可执行链接必败，这是预期，只要 `.o`。
- **「需完整版」这句报错在 Windows 上是误导**：Windows 的完整版产物里也没有这个引擎，因为引擎文件只对 `cgo && darwin` 编译。Windows 产线还没铺（AGENTS.md 记为 M2）。

### 6.7 分卷：为什么不做成 zip 的「分卷格式」

- 做成了**格式无关的字节切分**：归档先完整写好，再按大小切成 `.001/.002/...`，`cat` 就能还原。业界主流压缩器的 `.001` 分卷也是这个形态，任何格式都能用，读取侧只要会把段拼起来。
- **整档装得下就不切**：避免产出一个孤零零的 `.001` 让人误以为缺了后续分卷。
- **下限 64KB** 与提示文案都对齐业界主流压缩器的同一条报错。
- **先关句柄再删原归档**（见 6.12 的第 2 条）。
- 加密与分卷是正交的：加密在切分之前完成，切分只是字节复制。

### 6.8 解压：安全纪律与智能解压

- **先全验后动工**。路径穿越（反斜杠归一后以 `/` 开头、含盘符段、含 `..` 段）、符号链接逃逸（目标是绝对路径或盘符；相对目标以条目所在目录为锚点解析后逃出归档根）、硬链接指向归档外、设备 / 管道 / 未知类型条目、重复条目名、uuencode 的内嵌文件名——全部在写第一个文件之前检查。重复条目名要拒绝，是因为并行写出同名目标会互相践踏。
- **放行的符号链接是「静态」判定的**：只看链接目标本身以条目所在目录为锚点解析后是否在归档根内。归档里的每一个链接目标都这样约束过。这是静态判定，不覆盖输出目录里原本就有的符号链接——归档条目的路径若经过这样一个旧链接，落点就不受本工具控制。
- **硬链接不建硬链接而是复制内容**：Windows 无特权时 `os.Link` 不可用，复制在三平台都成立。目标可能是前向引用，流读完后统一补。
- **GBK 文件名修复是启发式**：条目名没有 UTF-8 标志位又不是合法 UTF-8，就按 GBK 解码。中文 Windows 的系统压缩工具产出的归档正是这种。GBK 解码器对非法字节一律替换成 U+FFFD、不报错，所以既不是 UTF-8 也不是 GBK 的名字也会被「修复」一遍——已知局限。`list` 不做这个修复，输出的是原始名字。
- **智能解压的判定只看顶层**：所有条目共享一个顶层名且顶层没有散装文件才直落。这样当前目录最多出现一个新目录，与 unar 的行为一致。
- **跳过用 `O_EXCL` 做**，是原子操作，没有「先检查存在再写」的时间窗。

### 6.9 并行解出与取消

- **取消通道只能关一次**（提交 fd857e7，2026-11-24）。并行解出里，工作协程失败时会关闭取消通道让其他协程停下。修复前是每个失败的协程各自 `close`——多个条目同时失败时（典型：加密包密码错误，每个条目都解不开）就 `panic: close of closed channel`，进程以 exit 2 结束，**stdout 为空**，违反输出契约（应当是 error JSON + exit 1）。当时实测：zip 侧 16 条目加密包 + 错密码 + 16 个工作协程必崩；容器侧（7z）11 个条目的落盘位置被同名目录占住时，6 次里崩 4 次。修法是用 `sync.Once` 守卫关闭（core/zip.go:1017、core/container_parallel.go:53）。回归单测 `core/parallel_failure_test.go` 的两个用例各跑 5 轮——竞态类缺陷一轮撞不上不代表没问题；门禁「多条目加密包错密码」两例（verify/verify.sh:733）。zip 与 7z 的并行骨架是同构的，**改其中一个必须同步改另一个**。
- **取消的检查点分布不均**【待确认：按代码读出来的现状，没有实际按过 Ctrl+C】。`checkCancelled` 只出现在 zip 的压缩派发与主循环、zip 的解压投喂循环、tar 的压缩与解出循环里（core/zip.go:644、692、1047，core/tar.go:267、391）。其余管线没有检查点，第一次 Ctrl+C 对它们无效。更要紧的是两处与契约不符：zip 解压被取消后 `firstError` 仍是 nil，**返回成功**；tar 的两条管线用 `fmt.Errorf("...: %s")` 重新格式化了 `ErrCancelled`，`errors.Is` 认不出来，命令面里专门的取消文案走不到。全局约定见 conventions.md 第 2 节。
- **第二次 Ctrl+C 立即以 130 退出**，不等清理（cli/root.go:37）。

### 6.10 convert

- **管线放在闭包里**是为了让清理真正执行（6.2）。
- **输出格式是白名单**而不是沿用 `compress` 的「其余一律 zip」：防止用户写 `--output x.7z` 时被静默转成 zip。
- **`--jobs` 被解析但从未使用**，传给引擎的并行度恒为 0——zip 因此走顺序路径、tar.gz 走单流 gzip。命令的 Use 行里却还列着 `[--jobs N]`。
- **convert 产出的 tar.gz 字节数不稳定**（2026-11-24 实测：同一输入三次得到 242809 / 242811 / 242817），而 `compress` 的 tar.gz 是稳定的。**原因未查明**【待确认：写 spec 时不能做压缩解压实验，下面只是代码线索】，线索如下：
  - gzip 头两条路径都是确定的：并行写入器的头是写死的 10 字节、mtime 为 0（core/gzip_parallel.go:69）；klauspost 的 `NewWriterLevel` 不设置 `ModTime`，头里 mtime 也是 0。
  - 所以差异只能来自 tar 字节流本身。tar 头里唯一随时间变化的字段是条目的修改时间，取自源文件的 mtime 并截到秒（core/tar.go:288）。`extract` 不还原条目的修改时间（`core` 全包没有 `Chtimes`），中转目录里文件的 mtime 就是**解压落盘的时刻**。解压跨过秒边界时，哪些文件落在哪一秒每次不一样，tar 头的字节就不同，deflate 之后大小差几个字节。`compress` 的输入是 mtime 固定的原文件，所以稳定。
  - 验证办法：对同一个源归档转换两次，用 `tar -tvf` 比较两个产物的修改时间；或者让解压还原 mtime 后再比字节数。
  - 这同时说明 convert（以及 extract）**不保留源归档条目的修改时间**。

### 6.11 list / test / info

- **`zipComment` 是包级变量**（core/list.go:257）。`listZip` 把注释存进去，`buildArchiveInfo` 再读。进程只跑一条命令时无害；在同一进程里先列一个 zip 再列别的格式，会把上一个 zip 的注释带出来。
- **读取基座不带密码**：`list` / `test` / `info` 都不解析 `--password`。对加密了头部的 7z、rar，清单读取失败被吞掉，结果是「成功 + 0 条目」。
- **`test` 的校验阶段没有复用分卷拼接源**：zip 用 `zip.OpenReader(路径)`、tar 用 `openFileSource(路径)`，都只打开首卷那一个文件。
- **`test` 跳过 zip 的加密条目**，但 `tested_entries` 仍然把它们算在内。
- **`list` 对读损坏是静默的**（4.3）。

### 6.12 Windows 实测修掉的五个 bug（2026-11-08，mac 上全部无症状）

1. **tar 读侧句柄泄漏**（`core/tar.go` 的 `openTarStream`）。底层流开了却只关解压层、不关本体。mac 上文件描述符最终随进程释放，毫无症状；Windows 上文件被占用，`TempDir` 清理报「being used by another process」才暴露。修法是用 `closeChain` 链式关闭：解压层 → 底层流，全部尝试，返回第一个错误。
2. **分卷创建「先删后关」**（`core/core.go` 的 `splitArchiveIntoVolumes`）。`defer Close` 要到 return 之后才执行，删原归档时句柄还开着，Windows 拒绝删除。修法是显式先 `Close` 再 `Remove`。**教训：Windows 上「关闭先于删除」是硬约束，`defer` 的顺序不算数。**
3. **命令面手拼绝对路径**（现为 `cli/archive/list.go` 的 `absPath`，当时在 `cli/list.go`）。手写 `cwd + "/" + p` 并以「`/` 开头」判断绝对路径——Windows 上 `C:/...` 被当成相对路径拼到当前目录后面。修法是改用 `filepath.Abs`。**教训：`path/filepath` 就是 Go 的 os.path，手拼路径是平台 bug 的温床；文件系统场景用 `filepath`，条目名、glob 匹配这类「逻辑路径」场景用 `path`（恒 `/`）。**
4. **glob 语义跨平台漂移**（`internal/cmdutil/glob.go`、`core/zpackignore.go`）。`filepath.Match` 的 `*` 按平台分隔符避让——Windows 上避开 `\` 却不避开 `/`，`dist/*` 会误匹配 `dist/sub/output.js`。修法是统一用 `path.Match`，入参先 `ToSlash` 归一。同一个修复还顺手把条目名的收集归一成恒 `/` 风格（`core/common.go`），写入端再 `ToSlash` 一次作双保险。
5. **tar 符号链接无特权时整包失败**。Windows 没开开发者模式时 `os.Symlink` 返回 `ERROR_PRIVILEGE_NOT_HELD`（1314），整个归档解压失败。修法是降级为拷贝归档内的目标成员（bsdtar 同款降级），前向引用先记下来、流读完后补拷，并打 stderr 日志。错误码按数值比较，因为 `syscall` 只在 Windows 导出这个常量；unix 上 `os.Symlink` 不会返回这个码，不会误判。

### 6.13 `--temp-dir` 的边界

- 它的实现是往 `core` 里注入一个包级配置（`SetTempDir`，core/temp.go），引擎里用 `resolveTempBase()` 取。
- 跟随它的有三处：zip 分块压缩的临时文件（core/zip.go:449）、cab 文件夹的溢写文件（core/cab.go:239），以及 convert 和 bench 在命令面创建的中转 / 语料目录。
- **不跟随它的有两处**：加密大文件的溢写文件写死用空目录参数（core/crypto.go:515，即系统临时目录）；7z 写入的暂存目录同样写死（core/sevenz.go:166）。另外 7z 的清单文件是建在**工作目录**里的——快路径下工作目录就是输入目录的父目录，也就是在用户的目录里落一个临时文件，用完即删（core/sevenz.go:67）。
- Windows 上系统临时目录几乎总在 C 盘，这是这个旗标存在的原因：大转换时把中转空间指到目标盘。

### 6.14 bench

- 语料一半可压、一半随机，是为了同时量出压缩速度与 Store 类文件的处理速度。
- **标着「`tar.gz 并行`」的那一行实际并没有并行**（代码审阅结论）：它给引擎的并行度是 0（cli/archive/bench.go:118），而 tar.gz 走并行 gzip 的条件是并行度 > 1（core/tar.go:185）。它测的其实是与「`tar.gz` 档位 6」完全相同的配置。见第 9 节。
- bench 的过程日志直接调 `stdout.LoggerInfo`，不受 `--quiet` 控制。

### 6.15 与其它部分的耦合点

- **GUI**（`ui/gui/service.go`）经 CLI 二进制契约驱动本功能，**任何字段改名或增删都是对 GUI 的破坏性变更**：
  - 压缩：传 `compress <输入...> --output <路径> --level N [--volume-size <值>] [--password <值>]`，读 `data.file_count` 与 `data.size_bytes`（ui/gui/service.go:298、393）。取消时 GUI 自己删除归档与已切出的分卷。
  - 解压：传 `extract <归档> [--output <目录>] [--password <值>]`，读 `data.file_count`、`data.size_bytes`、`data.skipped`（ui/gui/service.go:601、633）。GUI 不传 `--force`；解压任务没有登记取消清理（ui/gui/service.go:102 注明只有压缩任务有）。
  - `info`：读 `format`、`file_count`、`total_uncompressed`、`archive_size`、`encrypted`（ui/gui/service.go:547）。
  - `list`：读 `format`、`entry_count`、`total_uncompressed`、`archive_size`、`encrypted`、`entries[].name` 与 `entries[].size`（ui/gui/service.go:857）。
  - `test`：读 `is_success`、`error_info`、`data.tested_entries`、`data.all_ok`、`data.failures`（ui/gui/service.go:900）。
- **内嵌的 SKILL.md**（`cli/tools/skill.md`，经 `go:embed` 进二进制，`zpack skill` 输出它）向 Agent 描述了 compress / extract / convert / list / test / info 的用法与输出。旗标或字段变更时必须同步它，否则 Agent 按过时的文档调用。
- **`verify/verify.sh`** 里约 209 条断言守着本功能（第 8 节），其中大量是对 `error_info` 文案的全字节比对。
- **`scripts/genenv`**（realEnvTest 语料）用来人工体感进度条、媒体、加密分卷的真实表现——门禁只测确定性小样本。

### 6.16 改动时容易破坏的东西（自查清单）

- 改了 `core` 里任何错误文案 → 同步 `verify/verify.sh`。
- 改了输出 JSON 的任何字段名 → 同步 GUI 的结构体、SKILL.md、AGENTS.md 的输出表、本 spec 第 3 节。
- 改了并行管线的结构 → 确定性（两次产出字节一致）、取消通道只关一次、窗口信号量不死锁，zip 与 7z 两套骨架要同步。
- 动了「降级到顺序路径」的分支 → 加密条目绝不能回退明文。
- 新加了临时文件或目录 → 清理要放在 print 之前或闭包的 `defer` 里；在 Windows 上先关句柄再删；想想是否应该跟随 `--temp-dir`。
- 新加了路径拼接 → 用 `filepath.Join` + `FromSlash`，不要手拼；解压的目标路径出来后要确认仍在目标根之内。
- 新加了旗标 → 解析循环里要占位，否则它的值会变成位置参数；决定它受不受 `--quiet` 影响；同步 SKILL.md。
- 新加了一种可写格式 → 想清楚 `convert` 的白名单要不要跟上、`DetectWriteFormat` 的次序、帮助文本。
- 新加了一种可读格式 → `sniffSource` 的次序、`Extract` 的分派、`formatName`、`ArchiveBaseName` 的扩展名表、`list` 的支持范围。
- 新增了包级变量 → 想想能不能改成按调用传参（`EngineLogf` 的先例）。

### 6.17 加密条目的四处修复（2026-11-24 实测发现）

都是整理 spec 时读代码发现、再用二进制实测确认的，已全部修复（提交见变更记录）：

1. **Store 条目加密后自家解不开。** AES 扩展字段（0x9901）里的「内层压缩方式」写死成 deflate，而 `--level 0`（以及魔数直通的 jpg / png / pdf）的数据是原样 Store 的。解压按 deflate 去解，报 `flate: corrupt input before offset …`，解出的文件是空的；第三方工具同样解不开。**修法**：把条目真实的压缩方式写进去（`core/crypto.go` 的 `zipEncryptEntryForWrite`）。
2. **尾部 HMAC 校验从没真正生效，一触发就误报。** 流式解密器读尽时拿「解密后的末尾 10 字节」去比「全部密文的 HMAC」，永远不相等；内层是 deflate 时解压器读到流末就停、走不到这一步，所以既没有保护也没有暴露。而 AE-2 没有 CRC（标准库见 CRC 为 0 直接跳过），完整性全靠这把 HMAC。**修法**：按规范重写——末尾 10 字节密文是认证码，HMAC 只覆盖它之前的密文；内层 deflate 时在流末把解密流读尽以触发校验。回归测试：改动认证码一个字节，解压必须失败。
3. **逐文件密码表不成立。** 桥接解码器只按「强度 + 内层方式」注册一次，闭包里锁的是该码首个条目的密码。**修法**：按「方案 + 强度 + 内层方式 + 校验字节 + 密码」分组分配桥接码（`zipExtract` 的预扫）。
4. **只给 `--password-table` 不给 `--password`，整包是明文而命令报成功。** 引擎以「基准密码非空」决定加不加密。**修法**：没给 `--password` 时用密码表的 `*=兜底项` 当基准密码；既没有 `*` 又没给 `--password` 时明确报错（`cli/archive/compress.go`）。

**改动加密路径时最容易破坏的东西**：扩展字段的内层方式必须与数据的实际压缩方式一致；桥接码必须一个密码组一个；任何路径都不得让加密条目回退成明文。

回归测试：`core/zip_encrypt_store_test.go`（内层方式与往返、逐文件密码、篡改可发现）；门禁「level 0 加密」「密码表」共 7 例。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面·包说明 | `cli/archive/doc.go` | 一个命令一个文件的约定 |
| 命令面·compress | `cli/archive/compress.go` | 旗标解析与校验、缺省输出路径、卷尺寸模板表与解析、结果结构体 |
| 命令面·extract | `cli/archive/extract.go` | 旗标解析、解压侧 `--jobs` 缺省、取消文案 |
| 命令面·convert | `cli/archive/convert.go` | 位置参数解析、缺省输出与格式白名单、闭包管线 |
| 命令面·list / test / info | `cli/archive/list.go` | 三个命令共用 `engine.ListArchive` / `engine.TestArchive`；test 的两种载荷结构体 |
| 命令面·bench | `cli/archive/bench.go` | 语料生成、8 行基准、结果结构体 |
| 命令面·密码表 | `cli/archive/password_table.go` | `匹配模式=密码` 的解析与匹配闭包（compress 与 extract 共用） |
| 命令面·临时目录 | `cli/archive/tempdir.go` | `--temp-dir` 的统一处理与注入 |
| 命令面·装配与信号 | `cli/root.go` | 注册七个命令、统一开始日志、Ctrl+C 处理 |
| 命令面公共底座 | `internal/cliutil/` | `HelpAwareRun`（help 守卫）、`EngineLogf`（按参数决定日志去向） |
| 旗标与 glob 小件 | `internal/cmdutil/flags.go`、`internal/cmdutil/glob.go` | `HasFlag` / `FlagValue` / `ParseIntStrict` / `FirstPositional`；`MatchGlob` |
| 输出契约层 | `internal/stdout/log.go` | 信封与 print 函数、`LoggerInfo` |
| 引擎·入口 | `core/core.go` | `Compress` / `Extract` 两个入口与分派、`Format` 常量、选项与结果结构体、分卷切分 `splitArchiveIntoVolumes` |
| 引擎·格式识别 | `core/format.go` | `DetectWriteFormat`（写格式）、`sniffSource`（魔数嗅探）、归档源抽象 `archiveSource`、`ArchiveBaseName` |
| 引擎·共享逻辑 | `core/common.go` | 条目收集与排除、`isUnsafeEntryPath`、GBK 名字解码、智能解压判定、进度报告器 |
| 引擎·zip | `core/zip.go` | 写入三条路径与并行结构、HSA、解压预扫与并行解出 |
| 引擎·归档加密 | `core/crypto.go` | WinZip AES 与 ZipCrypto 的读写原语、桥接码、加密写入的两条路径 |
| 引擎·tar | `core/tar.go` | 预扫、PAX 写入、两遍读的解压管线、符号链接与硬链接落地 |
| 引擎·并行 gzip | `core/gzip_parallel.go` | `ParallelGzipWriter`（`io.WriteCloser`） |
| 引擎·7z 写入 | `core/sevenz.go` | 官方引擎的接线：工作目录、清单、退出码、进度 |
| 7z 引擎直调层 | `internal/sevenzipcli/cli_cgo.go`（`cgo && darwin`）、`internal/sevenzipcli/cli_stub.go`（其余构建） | 进程内调用改名的 `zpack_7z_main`；stderr 接管；桩返回「需完整版」 |
| 引擎·7z 读取 | `core/sevenzip.go` | bodgit/sevenzip；并行解出 |
| 引擎·rar 读取 | `core/rar.go` | nwaples/rardecode；两遍读、部分加密降级 |
| 引擎·wim / cab / ar / asar / iso | `core/wim.go`、`core/cab.go`、`core/ar.go`、`core/asar.go`、`core/iso.go` | 五种只读容器，全部走串行容器骨架 |
| 引擎·容器骨架 | `core/container.go`（串行）、`core/container_parallel.go`（并行） | 智能定根、逐条落地、跳过 / 覆盖语义、首错即停 |
| 引擎·单流格式 | `core/single.go` | 八种单流格式的解码器表与单文件落地；分卷内层非归档时的原样还原 |
| 引擎·遗留编码 | `core/legacy.go` | `.Z`（LZW）解码器、uuencode / xxencode 解码器 |
| 引擎·`.001` 分卷 | `core/split.go` | 分卷发现与拼接视图（`ReaderAt` + 顺序流） |
| 引擎·list / test / info | `core/list.go` | `ListArchive` / `TestArchive`、`ArchiveInfo` / `ArchiveEntry`、格式名表 |
| 引擎·重名条目检查 | `core/duplicates.go` | 多个输入在包里同名时报错（`reportDuplicateEntries`），zip / tar / 7z 写入线共用 |
| 引擎·.zpackignore | `core/zpackignore.go` | 规则解析、加载、匹配 |
| 引擎·临时目录与取消 | `core/temp.go`、`core/cancel.go` | 两个包级配置：临时目录基址、取消标记 |
| 测试·引擎 | `core/core_test.go`（1352 行）、`core/crypto_fixture_test.go`、`core/parallel_failure_test.go`、`core/sevenz_test.go` | 纯逻辑与端到端往返；7z 写入的两个用例在没有引擎的构建里跳过 |
| 测试·夹具 | `core/testdata/` | 7z、rar（含多卷与加密）、wim、cab、ar、asar、iso、lzip、.Z、uu / xx、加密 zip |
| 测试·命令面与小件 | `cli/archive/compress_test.go`、`internal/cmdutil/flags_test.go`、`internal/cmdutil/glob_test.go`、`internal/stdout/log_test.go` | 卷尺寸解析、严格整数、glob、信封编码 |
| 验收脚本 | `verify/verify.sh` | 行为门禁，只测 `bin/zpack-cli` |
| 真实体感语料 | `scripts/genenv` | realEnvTest：进度条、媒体、加密分卷（目录不入库） |

依赖（全部纯 Go）：klauspost/compress（flate / gzip / zstd）、golang.org/x/text（GBK 解码）、ulikunitz/xz、pierrec/lz4/v4、andybalholm/brotli、bodgit/sevenzip v1.6.5、nwaples/rardecode v1.1.3、nradchenko/go-wim、kdomanski/iso9660、sorairolake/lzip-go、kentalee/go-lzx（go.mod:7–24）。

## 8. 验收用例

### 8.1 `verify/verify.sh` 里覆盖本功能的用例

脚本只测仓库里的 `bin/zpack-cli`，每条用例同时比对输出与退出码（verify/verify.sh:12、24）。下表的行号是脚本里的行号，用例数是按断言调用静态数的近似值（整份脚本静态数约 368，实际运行会因环境少几条）。

| 分节（行号） | 覆盖内容 | 用例数 |
|---|---|---|
| zip 行为（29–251） | 目录与多文件打包、多输入在包里同名拒绝（zip 与 tar 各一条；不产出归档；不重名照常）、未知扩展名默认 zip、`--jobs 2`、`--level 0` 全 Store；默认排垃圾；全部条目置 UTF-8 标志位；jpg 魔数 Store 直通；解压往返（系统 `unzip` 交叉）；智能解压（散包包一层、单根直落）；默认跳过与 `--force`；17MB 大文件分块（缝合点数量、往返、两次产出一致）；GBK 夹具解出；zip-slip 拒绝；错误分支（`--output` 缺值、`--level 11`、`--jobs 0`、无法识别、路径不存在）；字典衔接比值；32MB 不可压文件的降档确定性 | 61 |
| tar / tar.gz（253–346） | tar 与 `.tgz` 写入（gzip 魔数）；系统 `tar` 交叉；解压往返含符号链接；`--jobs` 被接受；改名 `.bin` 仍按魔数识别；PAX 长名往返；`--level 0` 比 9 大；同输入两次产出一致；智能解压；tar.bz2 解压 | 30 |
| tar 安全与硬链接（354–391） | tar-slip 拒绝；符号链接逃逸拒绝；设备 / 管道条目拒绝；硬链接落地 | 6 |
| 7z / rar 读取（392–417） | 夹具解压（含中文路径）；rar4 单卷；rar 多卷续卷；伪造 7z / rar 头报错 | 11 |
| 单流与 `.001`（418–457） | gzip、bzip2 单流解出与重复解压跳过；`.001` 拼 zip；非归档分卷原样还原 | 9 |
| 遗留编码（458–471） | `.Z`；uuencode 与 xxencode（内嵌文件名） | 7 |
| 容器 ar / asar / iso（472–486） | 三个夹具解出 | 8 |
| lzip 与 wim（487–498） | lzip；wim（含 LZX 大文件） | 7 |
| cab（499–512） | 存储柜、MSZIP、LZX + E8 三个夹具 | 7 |
| 分卷创建（513–577） | tar 分卷与 `cat` 合并；从 `.001` 解压往返；zip 分卷往返；卷太小报错；模板 `vcd`（装得下不切分）、`DVD` 大小写归一、未知模板报错 | 18 |
| `.zpackignore`（578–597） | 排除目录、`*.log`、`!` 反选、自身排除 | 5 |
| list / test / info 与加密（691–756） | `info` / `list` / `test` 的 zip 与 tar.gz；加密 zip 的 `encrypted` 标记；加密大条目（无密码被拒）；分卷 + 密码；多条目加密包错密码不 panic；`info` 的 rar 格式名 | 17 |
| `--temp-dir`（832–868） | convert 的中转目录落点、清理与产物可解；compress / extract / bench 接受该旗标；bench 语料清理；缺值报错 | 10 |
| `--help` 守卫（872–877） | `compress --help`、`extract --help` 的退出码与示例 | 3 |
| 7z 写入（1141–1177） | 目录写入、格式自识别、回环内容；密码与头部加密；散装多根；分卷；空输入报错 | 14 |
| bench 数值校验（1188–1189） | `bench --size 5abc` exit 1 | 1 |

合计约 214 条。

### 8.2 单元测试

`core` 包里与本功能相关的（`go test ./core`）：

| 类别 | 用例 |
|---|---|
| 纯逻辑 | `TestIsExcludedName`、`TestIsExcludedNameEmptyUserList`、`TestIsUnsafeEntryPath`、`TestDecodeEntryName`、`TestZipDecideStoreMethod`、`TestSingleRootName`、`TestIsUnsafeSymlinkTarget`、`TestZipCrc32Combine`、`TestZipDeflateChunkBytes`、`TestZipPlanForTask`、`TestZpackignore` |
| 端到端往返 | `TestZipWriteArchiveDeterministicAndRoundtrip`、`TestTarWriteEntry`、`TestSingleStreamExtractGzip` / `Zstd` / `XzLz4Brotli`、`TestSplitExtractZip`、`TestSplitExtractRawFile`、`TestTarCompressedVariantsExtract`、`TestTarSymlinkExtractForwardRef`、`TestSevenZipExtract`、`TestSevenZipExtractParallel`、`TestRarExtract`、`TestZCompressExtract`、`TestUUEXXEExtract`、`TestArExtract`、`TestAsarExtract`、`TestISOExtract`、`TestLzipExtract`、`TestWimExtract`、`TestCabExtract`、`TestCabContentRoundtrip` |
| 字典衔接与降档 | `TestZipChunkDictionaryLinkage`、`TestZipHsaRuntimeDowngradeDeterministic` |
| 加密 | `TestGenerateEncryptedZipFixtures`（生成夹具，短模式跳过）、`TestEncryptedZipExtract`、`TestEncrypted7zExtract`、`TestEncryptedRarExtract`、`TestZipPasswordTable`、`TestZipPasswordLargeEntryEncrypted` |
| 并发失败回归 | `TestZipExtractConcurrentWrongPassword`、`TestParallelContainerExtractConcurrentFailures`（core/parallel_failure_test.go，各跑 5 轮） |
| 重名条目检查 | `TestCollectCompressTasksRejectsDuplicates`、`TestReportDuplicateEntriesReportsUpToLimit`（core/duplicates_test.go：不重名照常收集；同名文件 / 同名目录 / 同一路径给两次都报错；文案整句比对；点到上限为止） |
| 7z 写入 | `TestSevenZWriteArchive`、`TestSevenZWriteEmptyInput`（没有 7z 引擎的构建里跳过） |

其它：`cli/archive/compress_test.go` 的 `TestParseVolumeSize`；`internal/cmdutil` 的 `TestParseIntStrict`、`TestMatchGlob`、`TestMatchGlobDirPattern`；`internal/stdout` 的信封编码与键序测试。

### 8.3 没有自动化覆盖的地方

改到下面这些时，门禁不会告诉你做错了：

- **tar.xz / tar.zst 的写入**。`TestTarCompressedVariantsExtract` 是手工把 tar 流包上 xz / zstd 再解，测的是读取；`compress --output x.tar.xz` 的写入路径没有任何用例。
- **`convert`**：只有 `--temp-dir` 分节里的 4 条。缺省输出路径、格式白名单、`--level`、`--jobs` 都没有用例。
- **`--comment` / `--comment-file`**：门禁里一条都没有。
- **`--password-table`**：门禁里一条都没有；单测只验证了「一个归档一个密码」。
- **compress / extract / convert 的 `--quiet`**：没有用例（门禁里唯一的 `--quiet` 用例是 pdf 的）。
- **取消（Ctrl+C）**：完全没有自动化用例。
- **zip 条目的时间戳**、**tar 系的 `--password` 被忽略**、**list / info 对加密 7z / rar 的表现**、**`test` 对 `.001` 分卷**、**bench 的结果内容**（只测退出码与清理）。
- **7z 写入在 Windows / Linux 上的报错路径**（那一分节的 14 条断言里绝大部分在 Windows 上会失败，见 8.5）。
- **并行 gzip 的数据竞争**：单测里没有直接使用 `ParallelGzipWriter` 的用例，是否开着 `-race` 跑也没有约定。

### 8.4 手动验证的方法

```bash
# 帮助可用（应为 0）
zpack compress --help; echo $?
zpack extract --help; echo $?

# 基本往返，并用系统工具交叉验证 zpack 的产出
zpack compress 某目录 --output /tmp/a.zip && unzip -l /tmp/a.zip
zpack compress 某目录 --output /tmp/a.tar.gz && tar -tvf /tmp/a.tar.gz
zpack extract /tmp/a.zip --output /tmp/out && diff -r 某目录 /tmp/out/某目录

# 智能解压：在空目录里分别解单根包和散包，看当前目录只多出一个目录
# 覆盖语义：改一个已解出的文件再解一次，应被跳过（skipped 计数）；加 --force 应恢复
# 分卷：cat 包.zip.001 包.zip.002 > 合并.zip，与不分卷的产出比对

# 加密：用 7-Zip / WinZip 打开 zpack 加密的 zip（互操作）；
#       反过来用系统工具加密，再用 zpack extract --password 解开

# 进度条：用 scripts/genenv 生成的语料压一个耗时超过 5 秒的包，观察 stderr
# --quiet：zpack compress 大目录 --output x.zip --quiet 2>&1 | wc -l   （应只剩「开始执行」一行）
# Ctrl+C：按 3.7 的表逐条管线试，特别注意 zip 解压与 --jobs 1 的 zip 压缩

# 7z 写入：只在 macOS 完整版可用；在 Windows 上应报「需完整版」
# tar.xz / tar.zst：写出后用系统 tar -Jxf / zstd -d 验证（没有自动化）
```

在 Windows 的 Git Bash 上跑门禁需要一个 `stat` 垫片（把 `stat -f%z` 转发成 `stat -c%s`）放到 PATH 前面；`shasum` 与 `unzip` 用 Git Bash 自带的（AGENTS.md:288）。

### 8.5 2026-11-24 Windows 10 实跑记录

在一台 Windows 10 机器（Git Bash）上跑整份门禁：**352 条里通过 319、失败 33**，不是产品回归：

- **7z 写入分节因 Windows 缺引擎整段失败**，当日运行记为 13 条。按脚本静态数，这一分节共 14 条断言，其中「7z write 错密码 exit」「7z write 空输入 exit」两条在 Windows 上会因为别的原因碰巧得到 exit 1（归档文件不存在、桩直接报错），照此推算应失败 12 条——与运行记录的 13 条差 1，逐例归属以当次运行日志为准【待确认】。
- 其余 20 条与环境有关：脚本的路径坐标系（python3 / perl 以 `/tmp` 坐标写文件，MSYS 给 exe 的参数却转成 `C:/Users/...`）、符号链接与硬链接权限、长路径等。

### 8.6 关闭【待确认】条目的验证步骤

9.4 清单里的条目都是写 spec 时没有条件实测的。下面是可以直接照做的验证步骤；每一条做完，把结论回填到对应位置并从清单里划掉。

```bash
# ① 加密 + Store 条目解不解得开（9.2 第 8 条）
mkdir /tmp/zenc && printf 'x' > /tmp/zenc/a.txt && head -c 200000 /dev/urandom > /tmp/zenc/p.jpg
zpack compress /tmp/zenc/a.txt --output /tmp/l0.zip --password pw --level 0
zpack extract /tmp/l0.zip --output /tmp/l0-out --password pw        # 按代码推断：这里会报错
zpack compress /tmp/zenc/p.jpg --output /tmp/jpg.zip --password pw
zpack extract /tmp/jpg.zip --output /tmp/jpg-out --password pw      # 同上
7z t -ppw /tmp/l0.zip                                               # 用 7-Zip 交叉确认（Info-ZIP 的 unzip 不认 WinZip AES，不能用）

# ② 同一归档里两个条目、两个密码（9.2 第 9 条；需要 Info-ZIP 的 zip，Windows 的 Git Bash 没有）
mkdir /tmp/zpt && cd /tmp/zpt && printf 'A' > a.txt && printf 'B' > b.txt
zip -P pwA t.zip a.txt && zip -P pwB t.zip b.txt                    # 两次调用，各用各的密码
printf 'a.txt=pwA\nb.txt=pwB\n' > tbl.txt
zpack extract t.zip --output out --password-table tbl.txt           # 按代码推断：b.txt 报密码错误

# ③ zip 并行路径的时间戳（9.2 第 12 条）
zpack compress 某目录 --output /tmp/j8.zip                           # 默认并行
zpack compress 某目录 --output /tmp/j1.zip --jobs 1
unzip -l /tmp/j8.zip ; unzip -l /tmp/j1.zip                          # 比较小条目的日期：1980 还是真实日期

# ④ 多输入同名文件（9.2 第 13 条，2026-11-24 起在压缩这一侧就报错；门禁里有 5 条自动用例，这里只是手看一眼）
zpack compress 甲/x.txt 乙/x.txt --output /tmp/dup.zip                   # 报「多个输入在压缩包里会同名：x.txt（… 与 …）；请…」，exit 1，没有产出 dup.zip
zpack compress 丙/资料 丁/资料 --output /tmp/dup.tar                     # 两个同名的目录同理（zip / tar / 7z 同一条规则）

# ⑤ convert 的 tar.gz 为什么不稳定（9.2 第 7 条）
zpack convert 同一个源归档 /tmp/c1.tar ; zpack convert 同一个源归档 /tmp/c2.tar   # 不压缩的 tar，差异好定位
cmp -l /tmp/c1.tar /tmp/c2.tar | head                                # 差异字节序号减 1 再模 512，应都落在 136–147（ustar 头的修改时间字段）
# 再压 tar.gz 三次比大小；改让解压还原 mtime 后重跑，大小应稳定

# ⑥ 并行 gzip 的数据竞争（9.2 第 11 条；在 macOS 上做）
go build -race -o bin/zpack-race ./cmd/zpack
bin/zpack-race compress 一个几百 MB 的目录 --output /tmp/big.tar.gz   # 默认走并行 gzip；有数据竞争会打出 WARNING: DATA RACE

# ⑦ 取消（3.7 的表）：对一个大目录逐条管线按 Ctrl+C，对照表里的结果
# ⑧ 7z 的加密文案（9.2 第 21 条）：对加密了头部的 7z 分别用「不给密码」「错密码」解，记下 error_info
```

## 9. 未结项与已知限制

### 9.1 没做的

- **7z 写入的 Windows、Linux 产线**。引擎文件只对 `cgo && darwin` 编译（internal/sevenzipcli/cli_cgo.go:1），AGENTS.md 记为「Windows 产线待铺（M2）」。
- **路线图的遗留项**（AGENTS.md:354）：`.z01` 分卷（zip 的原生分卷格式）、v7 tar、sparse、fuzz 语料。
- **`--codec zlib` 档**（AGENTS.md:356）：病态超重复语料下 zlib 逐块比 klauspost 省约 23%，尚未做。
- **`list` / `test` / `info` 的范围**：只支持 zip、tar 系、7z、rar；不接受密码；`test` 对 7z、rar 只验结构不验数据。
- **解压不还原修改时间与属主**（`core` 全包没有 `Chtimes`）。convert 因此也不保留源条目的时间。
- **tar.bz2 写入**：纯 Go 没有成熟的 bzip2 编码器。
- **rar 的两处**：rardecode v1.1.3 的头里没有链接标记，rar 符号链接按普通文件落地；混合密码档案只能整档一个密码，逐文件多密码需要多遍扫描，暂不做。
- **wim、cab、iso 的库级限制**：wim 不支持 LZMS / ESD、XPRESS、重解析点、硬链接、ADS；cab 不支持 Quantum 与跨柜多卷；iso 不读 Joliet 名。
- **convert 的能力缺口**：不支持 `--exclude`、密码表、输出加密，目标不能是 `.7z`；`--jobs` 是摆设。
- **tar.xz / tar.zst 不吃 `--level`**，编码器参数全是默认值。
- **7z 写入不吃 `--jobs`、`--password-table`、注释**。
- **解压侧没有文件名清洗**：条目名含目标平台的非法字符时直接落盘失败。
- **设计上的取舍**（不是缺陷，但调用方要知道）：写入不保留空目录；zip 写入把符号链接解引用成文件。
- **加密 zip 的第三方兼容性**：AGENTS.md:123 记录「p7zip 17.05 旧版不识别；主流新版 7-Zip 待验证」——仍然待验证【待确认】。

### 9.2 已知不对但暂时没改的

「核实程度」一栏：「按代码确定」表示逻辑从源码直接读得出、只是没有跑过；「【待确认】」表示依赖标准库或第三方库的行为、或者依赖时序，写 spec 时没有条件实测。

| # | 现象 | 出处 | 核实程度 |
|---|---|---|---|
| 1 | `--output` 是 `.tar.bz2` / `.tbz2` 时报「暂不支持」，但输出文件已经在格式分派**之前**被创建——留下 0 字节文件，同名的旧归档也被截空；句柄没关、文件没删 | core/tar.go:177、219 | 按代码确定 |
| 2 | 输出为 tar 系时 `--password`、`--password-table`、`--comment` 被静默忽略，没有任何提示——用户以为加了密码，产物是明文 | core/tar.go:170、cli/archive/compress.go:191 | 按代码确定 |
| 3 | zip 解压被 Ctrl+C 取消后**返回成功**（`is_success:true`、exit 0） | core/zip.go:1046 | 按代码确定 |
| 4 | tar 系压缩 / 解压被取消时，错误被 `fmt.Errorf("…: %s")` 重新格式化，`errors.Is(err, ErrCancelled)` 认不出来，命令面里专门的取消文案走不到 | core/tar.go:241、354；cli/archive/compress.go:210、cli/archive/extract.go:145 | 按代码确定 |
| 5 | 只有 zip（并行压缩、解压投喂）和 tar 的循环里有取消检查点；7z、rar、wim、cab、ar、asar、iso、单流的解压，7z 写入，以及 `--jobs 1` 的 zip 压缩，第一次 Ctrl+C 都无效 | core/zip.go:539、core/sevenz.go、core/container*.go、core/single.go | 按代码确定 |
| 6 | convert 的 `--level` 非法值静默回退 6（全局数值严格校验的例外）；`--jobs` 被解析但从不使用 | cli/archive/convert.go:113、118 | 按代码确定 |
| 7 | convert 产出的 tar.gz 字节数不稳定（实测 242809 / 242811 / 242817），`compress` 的 tar.gz 稳定。线索指向「中转文件的 mtime 是解压时刻，被写进 tar 头」（6.10） | core/tar.go:288；`core` 全包无 `Chtimes` | 【待确认】原因未验证 |
| 8 | **加密写入时被 Store 的条目**（`--level 0`，或 jpg / png / mp4 / zip / pdf 这类 HSA 直通文件）数据是原始字节，extra 里声明的内层方法却写死为 deflate（8）——这样的条目读回来需要先 inflate 原始数据，应当是解不开的。门禁恰好造过一个这样的归档（含 `pic.jpg` + 密码），但只检查了 `info` 的加密标记，没有解回来 | core/crypto.go:485、571；core/zip.go:732；verify/verify.sh:704 | 【待确认】 |
| 9 | **zip 读取的逐文件密码不成立**：桥接解码器只在第一次遇到某个桥接码时注册，捕获的是第一个条目的密码；同一归档里「方案 + 强度 + 内层方法」相同的条目共用它。AGENTS.md 说「zip 逐文件独立生效」 | core/zip.go:896、927 | 【待确认】 |
| 10 | 压缩侧单给 `--password-table`、不给 `--password` 时**根本不加密**（是否加密只看 `--password` 是否非空）；密码表给出空密码时，小条目按空密码加密、大条目写成明文 | core/zip.go:544、712、725、728；core/crypto.go:505 | 按代码确定 |
| 11 | 并行 gzip 写入器里工作协程**无锁读 map**，与主协程持锁的写并发——数据竞争，运行时可能 `fatal error: concurrent map read and map write`（exit 2、stdout 为空） | core/gzip_parallel.go:182 对 161、214、233 | 【待确认】未用 `-race` 实测 |
| 12 | zip 并行路径的条目头只设 `Modified`，而 `CreateRaw` 只写 `ModifiedDate` / `ModifiedTime`——小条目与分块条目的时间戳字段是 0；顺序路径的时间戳是真实的。同一归档里两种条目元数据不一致 | core/zip.go:487、747；标准库 archive/zip/writer.go 的 `CreateRaw` | 【待确认】 |
| 13 | ~~多个输入含同名文件时不去重，归档里出现重复条目名——而自家 `extract` 拒绝含重复条目名的归档~~ **已解决（2026-11-24 用户拍板：不自动改名，报错）**：`reportDuplicateEntries` 在收集完条目后检查，zip / tar / 7z 写入线共用（core/duplicates.go）。只比较完全相同的包内名；大小写不同的两个名字不拦，但它们在 Windows / macOS 上解压时会撞成同一个文件——这是有意留着的不拦（见 4.1 的说明） | core/duplicates.go |
| 14 | `list` / `info` 遇到加密了头部的 7z 或 rar 时返回**成功**，条目数 0、`encrypted` 为 false（清单读取失败被吞掉） | core/list.go:149、167 | 按代码确定 |
| 15 | `list` 遇到读损坏的 tar 时静默返回已读到的条目 | core/list.go:133 | 按代码确定 |
| 16 | `zipComment` 是包级变量，同进程里先列 zip 再列别的格式会带上一个 zip 的注释 | core/list.go:257 | 按代码确定 |
| 17 | `test` 的校验阶段绕过了分卷拼接源，多卷的 `.001` 归档会失败（只打开首卷那一个文件）；`test` 跳过 zip 的加密条目，却把它们算进 `tested_entries` | core/list.go:198、206、225 | 按代码确定 |
| 18 | `--temp-dir` 管不到两处溢写：加密大文件的溢写文件、7z 写入的暂存目录；7z 的清单文件建在工作目录里——快路径下就是输入目录的父目录，也就是用户的目录里 | core/crypto.go:515、core/sevenz.go:67、166 | 按代码确定 |
| 19 | bench 标着「`tar.gz 并行`」的那一行给引擎的并行度是 0，实际走的是单流 gzip | cli/archive/bench.go:118；core/tar.go:185 | 按代码确定 |
| 20 | 被 lz4 压缩的 tar：内层 `ustar` 佐证命中后查表得到的是零值 `FormatZip`，被当成 zip 去解，报 `无法读取 zip 归档` | core/format.go:70、152；core/core.go:51 | 按代码确定 |
| 21 | 7z 解密失败时，只要库的错误文本含 `password`，对外就显示 `暂不支持加密 7z：需要密码的档案暂未实现`——这条文案已过时（加密 7z 是支持的），对「密码错误」也是误导。具体在缺密码、错密码两种情况下对外是哪一句话【待确认】（没有查 bodgit 库的错误文本，也没有实测） | core/sevenzip.go:83 | 【待确认】 |
| 22 | Windows 与 Linux 上 7z 写入的报错是「需完整版」，但 Windows 的**完整版**产物里同样没有这个引擎 | internal/sevenzipcli/cli_stub.go:9 | 按代码确定 |
| 23 | rar 混合密码档案解到密码错误处**静默收尾并返回成功**，没有逐文件的跳过日志 | core/rar.go:158 | 按代码确定 |
| 24 | bench 的过程日志不受 `--quiet` 控制；`--size` 作为最后一个参数且没给值时静默取缺省 | cli/archive/bench.go:51、78 | 按代码确定 |
| 25 | `extract` 接受 `--comment` / `--comment-file`，但不产生任何效果 | cli/archive/extract.go:64 | 按代码确定 |
| 26 | zip 注释超过 65535 字节时被标准库拒绝，返回值没有被检查，注释静默丢失 | core/zip.go:513、522 | 按代码确定 |
| 27 | `--volume-size` 报错文案的模板清单漏了 `bd25` | cli/archive/compress.go:261 | 按代码确定 |
| 28 | zip 分块路径的进度按压缩后字节累计，小条目按输入字节累计，总量却是输入字节；zip 解压的「y 文件」把目录条目算进总数；单流格式的进度总量是归档大小 | core/zip.go:765、773、993；core/single.go:121 | 按代码确定 |
| 29 | 7z、rar、asar 里的符号链接都按普通文件落地。7z 这一条是按代码推断（管线没有设置符号链接标记） | core/sevenzip.go:57 | 7z【待确认】；rar、asar 按代码确定 |
| 30 | asar 解出不重建空目录；条目遍历走 map，落地顺序每次不同 | core/asar.go:58 | 按代码确定 |
| 31 | tar.gz 的并行 gzip 协程数就是 `--jobs`，没有 zip 那样的 8 上限（`--jobs 64` 会起 64 个压缩协程） | core/tar.go:187；core/gzip_parallel.go:74 | 按代码确定 |
| 32 | 压缩侧用 `--password-table` 时 7z 只打提示、tar 系连提示都没有；密码表在三种输出格式下的语义各不相同 | core/sevenz.go:96；core/tar.go | 按代码确定 |

### 9.3 代码与文档不一致的地方

以代码为准。第 1–8 条是行为层面的不一致（AGENTS.md 与 conventions.md），第 9–14 条是帮助文本与 SKILL.md，第 15–27 条是 AGENTS.md 的目录说明与数字、以及代码注释。

| # | 文档说 | 代码实际 | 文档位置 | 代码位置 |
|---|---|---|---|---|
| 1 | zip 的大文件分块并行阈值是「>16MB」 | 阈值是 0x40001（256KB + 1）。AGENTS.md 的命令规格一节写对了，这里是旧值 | AGENTS.md:8 | core/zip.go:31 |
| 2 | tar「单流顺序写 `--jobs` 不参与」 | `--jobs` > 1 时 tar.gz 走并行 gzip | AGENTS.md:127；AGENTS.md:126「压缩侧上限 8」对 tar.gz 也不成立 | core/tar.go:185 |
| 3 | 写格式只列了 `.tar`、`.tar.gz` / `.tgz`、`.7z`，「其余一律 zip」；分卷「tar/zip/tar.gz 通吃」 | 还认 `.tar.xz` / `.txz`、`.tar.zst` / `.tzst`、`.tar.bz2` / `.tbz2`（最后一种识别后报不支持）；分卷与格式无关，含 7z | AGENTS.md:122 | core/format.go:19；core/core.go:138 |
| 4 | `--exclude`「按名精确匹配」；路线图第 4 条「`--exclude` 支持 glob」仍未划掉 | glob 已经实现（`*`、`**`、`?`、`[…]`、尾部 `/`） | AGENTS.md:125、355 | core/common.go:45；internal/cmdutil/glob.go:17 |
| 5 | stderr 日志表里有「密码错误跳过: b.txt」 | 代码里没有这条日志；rar 遇到密码错误是静默收尾 | AGENTS.md:99 | core/rar.go:158 |
| 6 | 密码表「zip 逐文件独立生效（随机访问 + 桥接解码器天然支持）」 | 桥接解码器按桥接码注册，同类条目共用首个条目的密码（9.2 的第 9 条） | AGENTS.md:132 | core/zip.go:896 |
| 7 | stdout 输出格式表没有 `convert` 与 `bench` 两行 | 两个命令都有稳定的 JSON 载荷（3.3、3.6） | AGENTS.md:34–83 | cli/archive/convert.go:207；cli/archive/bench.go:173 |
| 8 | 统一取消文案「操作已被用户取消（半成品已清理）」+ exit 1 | 各管线表现不一，zip 解压甚至返回成功（3.7） | AGENTS.md 未写取消细节；specs/conventions.md:28 | core/zip.go:1046；core/tar.go:241 |
| 9 | `compress --help` 里的示例 JSON 没有信封：`{"is_success":true,"archive":"...","file_count":N,"size_bytes":N}` | 实际输出带 `data` 包装 | cli/archive/compress.go:39；extract 同（cli/archive/extract.go:32） | internal/stdout/log.go:63 |
| 10 | `compress --help` 的说明里没提 `.7z`，Use 行也没列 `--comment`、`--comment-file`、`--temp-dir`、`--quiet` | 这些格式与旗标都已实现 | cli/archive/compress.go:29 | core/format.go:22；cli/archive/compress.go:67、104 |
| 11 | `compress` 的 Short 描述「压缩为 zip / tar / tar.gz（zip 并行架构；tar 单流顺序写）」 | 过时 | cli/archive/compress.go:40 | core/tar.go:185 |
| 12 | `extract --help` 说「26 种」格式 | `Format` 常量共 23 个；「26」是按什么数出来的没有找到依据 | cli/archive/extract.go:23 | core/core.go:23 |
| 13 | SKILL.md 写 `--comment <文本\|@文件>` | 没有 `@文件` 这种形态：`--comment` 的值原样当注释文本，从文件读要用 `--comment-file <路径>`——照 SKILL.md 写 `--comment @a.txt` 会把 `@a.txt` 四个字符存成注释 | cli/tools/skill.md:38 | cli/archive/compress.go:67 |
| 14 | SKILL.md 的 convert 用法不列 `--jobs`；命令的 Use 行却列了 `[--jobs N]` | 代码里 `--jobs` 确实无效——SKILL.md 反而是对的 | cli/archive/convert.go:23；cli/tools/skill.md:63 | cli/archive/convert.go:118 |
| 15 | 目录结构：`core/` 只列了 5 个文件；`cli/` 写的是「root.go + commands.go」；`internal/` 里的输出层叫 `output` | `core` 有 24 个非测试源文件；命令面已拆成 `cli/archive` 等子包；输出层是 `internal/stdout`，另有 `internal/cliutil` | AGENTS.md:337、340、343 | cli/archive/doc.go；internal/cliutil/cliutil.go |
| 16 | Windows 实测 bug 第 3 条的文件位置是 `cli/list.go` | 现为 `cli/archive/list.go` | AGENTS.md:282 | cli/archive/list.go:65 |
| 17 | 「7z/rar 读取为下一里程碑」；根命令的 Short「7z/rar 解压规划中」 | 早已交付 | AGENTS.md:8；cli/root.go:47；cmd/zpack/main.go:5 | core/core.go:246 |
| 18 | 门禁「预期 354/354」 | 2026-11-24 在 Windows 10 上实跑总数是 352 | AGENTS.md:181 | verify/verify.sh |
| 19 | 7z 读取管线头注释：「加密档案明确报『暂不支持加密 7z』」 | 加密 7z 是支持的（带密码打开）；只有错误文案映射还留着这句话 | core/sevenzip.go:3 | core/sevenzip.go:28、85 |
| 20 | 7z 写入头注释：「Alone2 bundle，DISABLE_RAR=1 剥 RAR」 | `DISABLE_RAR` 对 Alone2 的 makefile 无效，剥除是在归档阶段做的 | core/sevenz.go:1；internal/sevenzipcli/cli_cgo.go:3 | AGENTS.md:235 |
| 21 | `core/zip.go` 文件头注释：「>16MB 文件 128KB 分块并行」；`core/tar.go` 的 `tarCompress` 注释：「单流顺序写（jobs 不参与）」；`core/core.go` 的 `Jobs` 字段注释：「tar 单流忽略」；`core/core.go` 包注释里「已实现格式」写了两遍、第一遍过时 | 同第 1、2 条 | core/zip.go:1；core/tar.go:168；core/core.go:83、9 | core/zip.go:31；core/tar.go:185 |
| 22 | `core/asar.go` 文件头注释的布局是「8 字节长度头 + JSON 目录」 | 函数注释写的是真实布局：双 pickle `[u32=4][u32 H][u32 J][JSON]…`，代码也按此解析 | core/asar.go:1 | core/asar.go:28 |
| 23 | `core/wim.go` 注释：「镜像名不安全字符就地清洗为下划线」 | 清洗函数只做 `path.Clean`，对 `.`、`..`、`/` 返回空串，**没有替换非法字符** | core/wim.go:38 | core/wim.go:100 |
| 24 | 门禁 7z 写入分节的注释：「bin/zpack-cli 恒为完整版故不设能力门控」 | Windows 的完整版里也没有 7z 引擎，这一分节在 Windows 上整段失败 | verify/verify.sh:1126 | internal/sevenzipcli/cli_cgo.go:1 |
| 25 | 验收用例注释「大单文件 128KB 分块并行（>16MB 触发）」 | 触发阈值是 256KB + 1，用例只是用了 17MB 的样本 | verify/verify.sh:122 | core/zip.go:31 |
| 26 | zip 的 HSA 前置直通「不可压扩展名 30+」 | 清单里是 28 个扩展名 | AGENTS.md:126 | core/zip.go:57 |
| 27 | 7z 写入的已知限制：「`--jobs` / `--password-table` / 注释对 7z 忽略（logf 提示）」 | 只有后两者有提示；`--jobs` 是静默忽略 | AGENTS.md:235 | core/sevenz.go:96 |

### 9.4 【待确认】清单

写这份 spec 时没能核实的条目，集中列在这里。它们不是定案。

1. **convert 产出的 tar.gz 字节数为什么不稳定**（9.2 第 7 条、6.10）——没有做压缩解压实验，只有代码线索。
2. ~~加密写入 + Store 条目是否真的解不开~~ **已实测确认并修复**（见 6.17）：`--level 0 --password` 产出的归档自家解不开，解出的文件是空的。
3. ~~zip 密码表的逐文件密码是否真的不成立~~ **已实测确认并修复**（见 6.17）：同码条目共用首个条目的密码，第二个文件起报「密码错误或归档损坏」。
4. **并行 gzip 的数据竞争会不会在实际负载上触发**（9.2 第 11 条）——没有用 `-race` 或大语料实测。
5. **zip 并行路径的条目时间戳是否确为 0**（9.2 第 12 条）——读了标准库 `CreateRaw` 的实现，未产出归档用第三方工具查看。
6. ~~多输入同名文件产出的归档是否被自家 extract 拒绝~~ **已实测并解决（2026-11-24）**：压缩这一侧现在直接报错，走不到解压；门禁 5 条 + 单测。
7. **取消行为**（3.7 的表）——全部按代码读出，没有实际按过 Ctrl+C。
8. **7z 解密失败时对外的具体文案**（9.2 第 21 条）——没有查 bodgit 库的错误文本。
9. **7z 归档里的符号链接按普通文件落地**（4.1.9）——按管线代码推断。
10. **Windows 上解出含平台非法字符的文件名**（第 5 节）——按代码推断，没有清洗逻辑。
11. **Windows 上 tar 压缩遇到符号链接**（第 5 节）——代码路径三平台相同，Windows 上未实测。
12. **加密 zip 与第三方工具的兼容性**（9.1）——AGENTS.md 自述「待验证」。
13. **2026-11-24 门禁失败数的逐例归属**（8.5）——7z 写入分节静态数推算 12 条失败，运行记录是 13 条。
14. **macOS 与 Linux 上的实际表现**——spec 的代码核对是在 Windows 机器上做的，另外两个平台的行为都只有代码依据。

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 修复：并行解出的取消通道只关一次（多条目加密包配错密码曾 panic、exit 2、stdout 为空） | fd857e7 |
| 2026-11-24 | 修复：Store 条目加密后解不开、尾部 HMAC 校验失效、逐文件密码表不成立、只给密码表时整包明文（见 6.17） | 45402e1 |
| 2026-11-24 | 多个输入在包里同名时压缩直接报错，不自动改名（2026-11-24 用户拍板）：zip / tar / 7z 写入线共用一条检查，报错里点名包内名与来源并给出两条出路（3.1、3.7、4.1、7、8.1、8.2、9.2 #13） | d8c9d2c |
