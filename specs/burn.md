# 光盘刻录（burn）

> 把 ISO 镜像、一个目录或若干文件/目录刻到光盘上，并能枚举刻录机与盘内介质的状态；Windows 走系统自带的 IMAPI2、macOS 走 hdiutil、Linux 走 xorriso，对外始终是同一个 `zpack burn`。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

- **做什么**
  - 枚举刻录机：`zpack burn devices` 列出每台刻录机的序号、盘符（或设备路径）、厂商与型号，以及盘内介质的类型与状态（空白 / 可追加 / 已封盘 / 无介质…）。
  - 刻录**数据盘**：`zpack burn <输入...>` 接受三类输入——①一个 `.iso` / `.img` / `.bin` 镜像（原样直烧）；②一个目录（目录的**内容**进镜像根，目录名本身不进盘）；③两项以上的文件与目录（每项按唯一名装进镜像根）。缺省多区段、不封盘，烧完可以继续追加；`--close` 或 `--mode mastered` 才封盘。
  - 可选项：擦除可擦写盘后再刻（`--blank`）、选刻录机（`--device`）、定速度（`--speed`）、给光盘起名（`--volid`）、刻完不弹出（`--no-eject`）。
- **不做什么**（明确的边界）
  - 不做 `--mode live`（UDF 包写入的「优盘模式」）：三个平台都明确拒绝（cli/burn/burn.go:253-254、716-718）。
  - 只刻数据盘：没有音乐 CD、视频 DVD、启动盘制作。
  - 不产出可留存的镜像文件：macOS / Linux 生成的临时 ISO 用完即删（cli/burn/burn.go:310），Windows 根本不落镜像文件。
  - 没有独立的「擦除」命令：擦除只是 `--blank` 的前置步骤。
  - 不区分内置光驱与外接光驱（2026-11-24 用户要求「不要区分外接 / 内置」，AGENTS.md 陷阱 #11）：枚举与总线无关，盘不可写时先查盘。
  - 没有刻录百分比进度（只有每 5 秒一条心跳日志）；没有「中途打断后恢复」的逻辑（实测不需要，见 6.9）。
  - 不校验镜像内容：`.iso` / `.img` / `.bin` 只看扩展名就直烧。
- **使用场景**
  - 把一个文件夹备份到 DVD-R，之后往同一张盘追加文件（多区段）；给老式 DVD 机、车机刻盘时用 `--close` 封盘求最大兼容。
  - 把下载到的 ISO 原样烧到盘上。
  - GUI 的「刻录光盘」页就是经 `burn devices` 与 `burn <输入...>` 两条命令驱动的（ui/gui/service.go:987、1019）。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack burn devices` | 枚举刻录机与介质状态。无刻录机 = 成功 + 空数组 `[]` |
| `zpack burn <镜像.iso\|.img\|.bin> [旗标]` | 镜像原样直烧。只认扩展名（不分大小写），不检查文件内容 |
| `zpack burn <目录> [旗标]` | 目录的**内容**刻进镜像根。Windows 由系统镜像库流式生成后直烧、无临时 ISO；macOS / Linux 先生成临时 ISO |
| `zpack burn <文件\|目录 ...> [旗标]`（两项及以上） | 多输入：文件与目录可任意混排，每项按唯一名装进镜像根；目录整树装入同名目录 |
| `zpack burn --help` / `-h` | 打印帮助，exit 0（经 `helpAwareRun`，internal/cliutil/help.go:15-23）。帮助是 Short 加一段 Long：六条示例（枚举、烧镜像、目录直刻并封盘、选设备与速度、先擦再烧、起光盘名）以及模式、光盘名称、输出形态各一句说明 |

单个的普通文件（非镜像）会被拒绝——它既不是镜像，也不构成「多输入」（cli/burn/burn.go:242-246）。

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--device N` | 刻录机序号，即 `burn devices` 输出里的 `index` | 必须是十进制整数且 ≥ 0，否则报 `--device 需要 0 起的设备序号（见 zpack burn devices），收到: <值>`（cli/burn/burn.go:499-506）。超出实际台数由后端报 `--device N 超出范围（共 M 台，见 zpack burn devices）`。macOS 只支持 0 | `0` |
| `--speed N` | 写入倍速 | 必须是十进制整数且 ≥ 1，否则报 `--speed 需要正整数（如 8），收到: <值>`（cli/burn/burn.go:507-514）。Windows 上会落到介质与刻录机实际支持的档位（见 4.1.7） | 不给 = 不设置，用刻录机默认速度 |
| `--volid 名称` | 光盘名称（卷标） | 任意文本，取值原样保存（cli/burn/burn.go:515-519）。Windows 上做温和规范化；Linux 上原样作 `-volid` 交给 xorriso；macOS 上没有传给任何工具（见 4.1.6） | 不给时：单个目录输入 = 目录名；其余形态 = 当前目录名；Windows 上往有内容的盘追加 = 沿用盘上名称（见 4.1.6） |
| `--mode multi\|mastered\|live` | 写入模式 | 只能是这三个值之一，否则报 `--mode 取值 multi / mastered / live，收到: <值>`（cli/burn/burn.go:520-526）。`mastered` 等价于 `multi` + `--close`；`live` 一律拒绝 | `multi` |
| `--close` | 写完封盘，之后不能再追加 | 布尔开关，没有值 | 不封盘 |
| `--blank` | 先擦除（快速擦除）再刻 | 布尔开关，没有值。只对可擦写盘有效 | 不擦 |
| `--no-eject` | 刻完不弹出 | 布尔开关，没有值 | 刻完弹出 |

**解析规则（本功能特有，全部在 `burnParseFlags` 与 `runBurnWrite`，cli/burn/burn.go:206-221、487-539）**

- **位置参数必须在旗标之前**：从第一个以 `-` 开头的参数起，后面的内容全部当作旗标解析。`zpack burn --volid 备份 目录/` 会因为没有输入而报「用法: …」；旗标区里夹着的普通 token 也不会被当成输入。
- **旗标的值**：仅当下一个参数**不以 `-` 开头**时才算它的值；否则这个旗标被当作没给值，静默取缺省。因此 `zpack burn 目录/ --speed` 等于没给 `--speed`；`--speed -1`、`--device -1` 这类负值也被静默忽略而不是报错（负号开头的 token 被当成了另一个旗标）。
- **数值是严格的**：用 `strconv.Atoi`，整个字符串必须是十进制整数（`5abc` 报错，`007` 得 7，`+5` 得 5）。
- **未知旗标静默忽略**；本功能不识别 `--quiet`、`--jobs`。
- **`--mode mastered` 强制 `close = true`**（cli/burn/burn.go:535-537）；`--mode multi --close` 同样封盘。
- **`devices` 是保留的首参数**：首参数恰为 `devices` 就走枚举，后面的参数一概不看（cli/burn/burn.go:79-81）。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

本功能特有的全局性事项：

- **Ctrl+C**：刻录流程不检查取消请求。第一次 Ctrl+C 不会中断刻录，第二次才以 130 强制退出（原因见 conventions.md 第 2 节「取消的范围」）。刻录被强制退出后的盘况见 6.9。
- **刻录不可逆**：验收脚本在检测到刻录机的机器上跳过会写盘的用例（见第 8 节）；开发调试时不要对着有数据的盘跑 `zpack burn`。
- **引擎错误文案即对外文案**：Windows 引擎（`internal/imapi2`）返回的错误被原样放进 `error_info`（cli/burn/burn.go:265-267），改引擎文案时要同步本文件第 3 节的表。

## 3. 输出契约

信封遵循 conventions.md；下面写的都是 `data` 载荷与 `error_info` 的取值。

### 3.1 `zpack burn devices` 成功（exit 0）

```json
{"is_success":true,"data":{"devices":[{"index":0,"path":"E:\\","vendor":"lenovo","product":"UltraslimDVD","media":"DVD-R","status":"blank"}]}}
```

无刻录机时是 `{"is_success":true,"data":{"devices":[]}}`——空数组，不是 `null`，三平台一致（cli/burn/burn.go:141-143、183-185，cli/burn/burn_windows.go:55）。

`devices[]` 的每个元素是一个 struct，键序 = 声明序（cli/burn/burn.go:41-49）：

| 字段 | 类型 | 含义 | 何时缺省（`omitempty`） |
|---|---|---|---|
| `index` | int | 设备序号，`--device` 用它 | 恒有 |
| `path` | string | Windows：盘符（如 `E:\`）；盘没有挂载点（没放盘、封盘后没被系统挂载等）时用引擎的唯一标识。Linux：设备路径（`/dev/sr0`）。macOS：恒为空串 `""` | 恒有（macOS 为空串） |
| `vendor` | string | 厂商 | 引擎给不出时 |
| `product` | string | 型号 | 引擎给不出时 |
| `media` | string | 介质类型名，见下表 | 无介质、或介质查询失败时 |
| `status` | string | 介质状态名，见下表 | macOS 有盘时（macOS 报不出状态） |
| `note` | string | 备注 | macOS 恒有（固定文案）；Windows：介质查询成功时缺省；Linux 恒缺省 |

`media` 的取值：

| 平台 | 取值 |
|---|---|
| Windows | 表内类型名：`CD-ROM` `CD-R` `CD-RW` `DVD-ROM` `DVD-RAM` `DVD+R` `DVD+RW` `DVD+R DL` `DVD-R` `DVD-RW` `DVD-R DL` `DVD+RW DL` `HD DVD-ROM` `HD DVD-R` `HD DVD-RAM` `BD-ROM` `BD-R` `BD-RE`；表外类型报 `未知介质(<数值>)`（internal/imapi2/imapi2.go:100-127） |
| macOS | `drutil status` 里 `Type:` 一行的原文（cli/burn/burn.go:126-140） |
| Linux | `xorriso -toc` 里 `Media current :` 一行的原文（cli/burn/burn.go:150-179） |

`status` 的取值（词表的唯一来源：internal/imapi2/imapi2.go:95-106；GUI 侧照抄在 ui/gui/burnstate.go:3-7）：

| status | 含义 | Windows 怎么得到 | macOS | Linux 怎么得到 |
|---|---|---|---|---|
| `blank` | 空白盘，可以起名 | 状态位含 BLANK（0x2） | 不报 | 状态短语含 `blank` |
| `appendable` | 有数据，可追加区段 | 状态位含 APPENDABLE（0x4）/ FINAL_SESSION（0x8）/ OVERWRITE_ONLY（0x1）之一，且没有更高优先级的位置位 | 不报 | 状态短语含 `appendable` |
| `full` | 已封盘或只读（压盘） | 状态位含 FINALIZED（0x4000）；或「写保护位 + 只读介质类型（CD-ROM / DVD-ROM / HD DVD-ROM / BD-ROM）」 | 不报 | 状态短语含 `is not appendable` |
| `no-media` | 没放盘 | 读介质类型得到 `E_IMAPI_RECORDER_MEDIA_NO_MEDIA`（0xC0AA0202）或类型为「未知」 | `drutil status` 的类型行含 `No Media` | 状态短语含 `not present` |
| `damaged` | 上一区段没正常关闭，需要恢复 | 状态位含 DAMAGED（0x400） | 不报 | 不报 |
| `erase-required` | 须先擦除才能写 | 状态位含 ERASE_REQUIRED（0x800） | 不报 | 不报 |
| `write-protected` | 写保护（仅可擦写类型） | 状态位含 WRITE_PROTECTED（0x2000）且介质类型不是只读类型 | 不报 | 不报 |
| `unsupported` | 刻录机不支持在该介质上刻录 | 状态位含 UNSUPPORTED（0x8000） | 不报 | 不报 |
| `unrecognized` | 光驱认不出这张盘：读不出盘片身份，或读不出盘上的内容 | 读介质类型得到 `E_IMAPI_RECORDER_MEDIA_INCOMPATIBLE`（0xC0AA0203）；或盘被报成「只读类型 + 写保护」、而引擎读不出盘上任何受支持的数据文件系统（见 6.10）。此时 `note` 里是排查提示 | 不报 | 不报 |
| `unknown` | 介质查询以别的原因失败，或状态位不属于以上任何一种 | 介质查询报了别的错（此时 `note` 里是失败原因）；或没有任何已知位置位 | 不报 | 状态短语认不出时**原样透传**，不是 `unknown` |

- Windows 的判定顺序见 `StatusName`（internal/imapi2/imapi2.go）：无介质 → 无法识别 → 已封盘 → 只读类型带写保护 → 写保护 → 不支持 → 损坏 → 须先擦除 → 空白 → 可追加 → 其他。
- Windows 上一台刻录机的介质查询失败不阻塞其余刻录机的枚举（internal/imapi2/engine_windows.go:113-129）；此时该条目是 `status:"unknown"` 加 `note:"<失败原因>"`。打不开这台刻录机时 `path` 是空串；能打开、只是读不了介质时 `path` 是盘符或引擎唯一标识（cli/burn/burn_windows.go:73-90）。
- 状态位 NON_EMPTY_SESSION（0x1000，「上一区段写了一半」）没有独立的 status 取值：`burn devices` 里落进 `unknown`（除非同时带着别的位置位），但写入前会被拒绝（见 4.3）。
- macOS / Linux 上「不报」表示 status 字段根本不出现，GUI 据此只对明确认识的状态下结论（ui/gui/burnstate.go:3-7）。

### 3.2 `zpack burn <输入...>` 成功（exit 0）

```json
{"is_success":true,"data":{"blank":false,"close":false,"image":"E:\\data\\photos","mode":"multi","size_bytes":6396314}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `image` | string | Windows：**第一个输入的绝对路径**——目录输入时就是那个目录的路径，并不是一个镜像文件（Windows 没有临时 ISO）。macOS / Linux：临时镜像的路径（`<系统临时目录>/zpack-burn-<pid>.iso`），返回时已经删除（cli/burn/burn.go:285-312、599） |
| `size_bytes` | int | Windows：本次写入的字节数——镜像输入 = 镜像文件大小；目录 / 多输入 = 生成镜像的 `TotalBlocks × BlockSize`（cli/burn/burn_windows.go:234、278-283）。macOS / Linux：**不是**写入量，而是所有非目录输入的文件大小之和；输入全是目录时等于第一个输入 `os.Stat` 的大小（目录自身的元数据大小，没有参考意义）（cli/burn/burn.go:224-238、269-273） |
| `mode` | string | `multi` 或 `mastered`（`live` 走不到成功） |
| `close` | bool | 是否封盘（`--close` 或 `--mode mastered`） |
| `blank` | bool | 是否请求了擦除（只反映旗标，不反映盘是否真的被擦） |

键序：这个载荷是 map，经 `Deterministic(true)` 按键名字母序输出，实际顺序是 `blank`、`close`、`image`、`mode`、`size_bytes`（cli/burn/burn.go:274-280，internal/stdout/log.go:40）。消费方不应依赖键序；这个顺序与 AGENTS.md 输出表、与 `zpack burn --help` 里印的示例都不同（见第 9 节）。

输出里没有卷标、没有刻进了多少文件、没有用的哪台刻录机——这些只在 stderr 日志里。

### 3.3 失败（exit 1，`{"is_success":false,"error_info":"..."}`）

下面的文案都是逐字取自代码；`<…>` 是运行时填入的部分。检查有先后，先命中的先报。

**参数与输入形态（三平台共通，进入后端之前）**

| `error_info` | 触发条件 | 出处 |
|---|---|---|
| `用法: zpack burn devices \| zpack burn <镜像.iso\|目录> [旗标]` | `zpack burn` 一个参数都没给 | cli/burn/burn.go:76-78 |
| `用法: zpack burn <镜像.iso\|目录\|文件...> [旗标]` | 只给了旗标、没有任何输入 | cli/burn/burn.go:220-222 |
| `输入不存在: <路径>` | 任一输入 `os.Stat` 失败（路径原样，未转绝对） | cli/burn/burn.go:227-230 |
| `镜像文件（.iso/.img/.bin）只能单独刻录，不能与其他输入混排` | 两项以上输入里有镜像 | cli/burn/burn.go:239-241 |
| `不识别的镜像扩展名（支持 .iso/.img/.bin；文件/文件夹请与其他内容一起多输入）: <路径>` | 只有一项输入且它既不是目录也不是镜像（单个散文件） | cli/burn/burn.go:242-246 |
| `暂不支持 <系统名>：刻录引擎仅 darwin=hdiutil / linux=xorriso / windows=IMAPI2` | 既不是 Windows、macOS，也不是 Linux | cli/burn/burn.go:249-251、712 |
| `暂不支持 live：优盘模式（UDF 包写入）尚未接入，请用 multi（多区段追加）或 mastered（封盘）` | `--mode live` | cli/burn/burn.go:253-255、717 |
| `--device 需要 0 起的设备序号（见 zpack burn devices），收到: <值>` | `--device` 的值不是非负整数 | cli/burn/burn.go:503 |
| `--speed 需要正整数（如 8），收到: <值>` | `--speed` 的值不是正整数 | cli/burn/burn.go:511 |
| `--mode 取值 multi / mastered / live，收到: <值>` | `--mode` 的值不在三者之内 | cli/burn/burn.go:523 |

**设备枚举**

| `error_info` | 平台 | 触发条件 | 出处 |
|---|---|---|---|
| `未找到 xorriso（Linux 刻录引擎）——请安装：sudo apt install xorriso / sudo dnf install xorriso / sudo pacman -S xorriso` | Linux | PATH 里没有 `xorriso` | cli/burn/burn.go:156-158 |
| `xorriso -devices 失败: <xorriso 输出>` | Linux | `xorriso -devices` 退出码非 0 | cli/burn/burn.go:159-162 |
| 引擎错误（见下面的格式）或 `初始化 COM 失败: <原因>` | Windows | COM 初始化失败，或创建引擎对象、读取刻录机数量失败 | cli/burn/burn_windows.go:66-68 |

**刻录**

| `error_info` | 平台 | 触发条件 | 出处 |
|---|---|---|---|
| `--device N 超出范围（共 M 台，见 zpack burn devices）` | Windows / Linux | 序号 ≥ 刻录机台数（没有刻录机时 M = 0）。macOS 不走这条，见下面的「macOS 多刻录机寻址」 | cli/burn/burn_windows.go:106-110，cli/burn/burn.go:663-665 |
| `刻录机里没有光盘——请放入光盘后重试` | Windows | 刻录机里没有介质 | cli/burn/burn_windows.go |
| `光驱认不出这张光盘，无法写入；请检查：盘是否放反（标签面朝上）、是否卡牢、盘片类型是否超出刻录机能力（如蓝光盘放入 DVD 光驱）、盘片是否划伤或太脏` | Windows | 介质状态为 `unrecognized`（见 4.3、6.10） | cli/burn/burn_windows.go |
| 见 4.3 的拒绝表 | Windows | 介质不可写 | cli/burn/burn_windows.go:126-128、187-207 |
| `当前介质 <类型> 不可擦除（--blank 仅适用于 CD-RW / DVD±RW / DVD-RAM / BD-RE 等可擦写盘）` | Windows | 对一次性盘给了 `--blank` | cli/burn/burn_windows.go:130-133 |
| `内容大小 <大小> 超过光盘剩余空间 <剩余>` | Windows | 待写镜像大于盘上剩余空间（仅当剩余扇区数 > 0 时才检查） | cli/burn/burn_windows.go:165-167 |
| `打开文件为流失败: <路径>（0x<码>）` | Windows | 镜像文件（或多输入里的文件）打不开 | internal/imapi2/engine_windows.go:518-530 |
| `<操作>失败: <系统给的本地化说明>（0x<引擎错误码>）`，或无说明时 `<操作>失败（0x<引擎错误码>）` | Windows | 任何一次引擎调用失败。`<操作>` 是代码里给每步起的名字：打开刻录机、读取介质类型、读取介质状态、设置写入速度、封盘设置、读取多区段接口、设置多区段接口、导入上一区段、卷标、加入目录、建目录、加入文件、生成镜像、**写入光盘**、**擦除光盘**、弹出光盘 等 | internal/imapi2/imapi2.go:252-264 |
| `macOS 多刻录机寻址（-device OpenFirmware 路径）M2 交付；当前用默认设备（--device 0）` | macOS | `--device` > 0 | cli/burn/burn.go:636-638 |
| `擦除失败（介质可能不可擦）: <drutil 输出>` | macOS | `--blank` 且 `drutil erase quick` 失败 | cli/burn/burn.go:639-643 |
| `未找到 hdiutil（darwin 侧镜像生成引擎）` | macOS | 目录 / 多输入要生成镜像，但找不到 `hdiutil`（正常系统上不会发生） | cli/burn/burn.go:604-615 |
| `未找到 xorriso（linux 侧镜像生成引擎）——请安装：sudo apt install xorriso / sudo dnf install xorriso / sudo pacman -S xorriso` | Linux | 目录 / 多输入要生成镜像，但找不到 `xorriso` | cli/burn/burn.go:604-615 |
| `未找到 xorriso（Linux 刻录引擎）——请安装：sudo apt install xorriso` | Linux | 刻录阶段找不到 `xorriso`（镜像直烧时只会在这一步报） | cli/burn/burn.go:659-661 |
| `生成临时镜像失败: <工具输出>` | macOS / Linux | `hdiutil makehybrid` 或 `xorriso -as mkisofs` 失败 | cli/burn/burn.go:628-630 |
| `创建刻录暂存目录失败: <原因>` / `暂存刻录内容失败: <原因>` | macOS / Linux | 多输入聚台时建不了临时目录，或某项输入挂不进舞台 | cli/burn/burn.go:368-382 |
| `hdiutil 刻录失败: <Go 的 exec 错误>` / `xorriso 刻录失败: <Go 的 exec 错误>` | macOS / Linux | 刻录命令退出码非 0（通常形如 `exit status 1`；工具自己的报错原文在 stderr 里，不进 JSON） | cli/burn/burn.go:687-701 |

路径类失败（`filepath.Abs`、`os.Stat`）在 Windows 后端里返回的是 Go 原始错误文案，没有加工（cli/burn/burn_windows.go:221-228、254-270）。

刻录失败不会改变已经写到盘上的数据，也没有回滚；临时文件（macOS / Linux 的舞台目录与临时 ISO）在失败路径上照样清理（见 4.1.8）。

### 3.4 stderr 过程日志

格式遵循 conventions.md（`[YYYY-MM-DD HH:MM:SS] 消息`；警告带 `[WARN]` 标签）。本功能会输出：

| 日志 | 平台 | 何时 |
|---|---|---|
| `zpack burn 开始执行` | 全部 | 每次入口（装配层统一输出，只记命令名） |
| `刻录机: <厂商> <型号>（<盘符，没有盘符则是「第 N 台」>）` | Windows | 打开刻录机之后 |
| `介质: <类型>（<状态>），剩余 <容量>` | Windows | 读到介质之后 |
| `快速擦除光盘…` | Windows | 带了 `--blank` 且介质可擦写 |
| `擦除中… 已用 N 秒` | Windows | 擦除期间，每 5 秒一条 |
| `[WARN] 这张盘已有内容，而镜像是原样写进新区段的，不会与盘上原有文件合并——…`（全文见 4.1.3） | Windows | **镜像输入，且盘不是空白**（擦除之后重读再判）。只警告、不拦截，照常写入 |
| `写入速度: N×` / `写入速度: 请求 N× 超出当前介质/刻录机支持的范围，按 M× 刻录` / `写入速度设置未生效（<原因>），按刻录机默认速度刻录` | Windows | 给了 `--speed` |
| `镜像文件: <绝对路径>（<大小>）` | Windows | 镜像输入 |
| `光盘名称: <名称>` | Windows | 目录 / 多输入，且最终使用的卷标非空 |
| `镜像: N 个文件，<大小>（目录内容直烧，无临时文件）` | Windows | 目录 / 多输入；N 含从上一区段导入的文件 |
| `刻录中… 已用 N 秒` | Windows | 写入期间，每 5 秒一条 |
| `光盘已刻录完成，但弹出未成功: <原因>` | Windows | 刻录成功但弹出失败（此时仍是 exit 0） |
| `<工具> <参数…>`（即将执行的完整命令行） | macOS / Linux | 执行刻录命令之前。镜像生成与擦除命令不记 |
| `<外部工具 stdout 的每一行>` | macOS / Linux | 刻录命令的 stdout 逐行加时间戳前缀转记 |

容量的人读写法（`burnFormatBytes`，cli/burn/burn.go:334-358）：整数位不带小数（`3GB` / `5MB`），否则一位小数（`2.3MB`），数字与单位之间不留空格，1024 进制。

macOS / Linux 上刻录命令（`hdiutil burn` / `xorriso -as cdrecord`）自己的 stderr 是**原样透传**的，没有时间戳前缀，这是刻录功能对统一日志格式的唯一偏离（cli/burn/burn.go:690）。

## 4. 行为细则

### 4.1 正常路径

#### 4.1.1 设备枚举：`zpack burn devices`

**Windows**（cli/burn/burn_windows.go:54-90，internal/imapi2/engine_windows.go:69-131）

1. 整个枚举包在一次 COM 会话里（`WithSession`：钉住 OS 线程 + `CoInitializeEx(APARTMENTTHREADED)`，internal/imapi2/call_windows.go:21-30）。
2. 用 `IDiscMaster2`（按 CLSID 创建）读刻录机数量，逐台取唯一标识，再创建 `IDiscRecorder2` 并 `InitializeDiscRecorder`，绑定一个 `IDiscFormat2Data` 数据写入器（`ForceOverwrite = true`、`ClientName = "zpack"`）。
3. 对每台读介质：介质类型、状态位、剩余空间等（`Media`，internal/imapi2/engine_windows.go）。读介质类型若得到「没有介质」的错误码，归一成 `no-media`；若得到「介质不兼容或为未知的物理格式」（0xC0AA0203），归一成 `unrecognized`——这是有盘但光驱认不出，不是查询失败。盘被报成「只读类型 + 写保护」时，再让引擎读一次盘上的文件系统：一个都读不出也算 `unrecognized`（见 6.10）。
4. 输出条目：`index` = 枚举顺序，`path` = 盘符（`VolumePathNames[0]`），没有盘符时用引擎唯一标识，`vendor` / `product` 取自刻录机自报。
5. 没有刻录机 = 空数组；某一台的介质查询失败只影响它自己那条（`status:"unknown"` + `note`）。刻录机与总线无关：内置、USB 外接一视同仁。

**macOS**（cli/burn/burn.go:107-145）

1. `drutil list`：首行当表头跳过；每行按空白切分，不足 3 个字段的行跳过；第 1 个字段作 `vendor`，第 2 到倒数第 3 个字段拼成 `product`，**最后两个字段丢弃**；`path` 恒为空；`note` 恒为「macOS 经 hdiutil 使用默认刻录设备」。
2. `drutil status`：找第一行 `Type:`，只归到第一台刻录机（典型 Mac 只有一台光驱）。含 `No Media` → `status = no-media`；否则该值作 `media`，不报 `status`。
3. `drutil list` 执行失败（极端裁剪的系统上没有 drutil）→ 空数组，算成功。

【待确认】`drutil list` 实际的列布局与「丢掉末两列」的解析是否吻合——没有 macOS 机器可验，这条路径也没有真机验证记录。

**Linux**（cli/burn/burn.go:147-203）

1. PATH 里没有 `xorriso` → 报错 exit 1（带安装引导），不是空数组。
2. `xorriso -devices`：每行形如 `0 -dev '/dev/sr0' rwrw-- : '厂商' '型号'`，用正则取设备路径、厂商、型号；`index` 用**出现顺序**，不是 xorriso 自己报的编号。
3. 逐台 `xorriso -dev <路径> -toc`：`Media current :` 一行作 `media`，`Media status :` 一行归一成 status——含 `blank` → `blank`；含 `appendable` → `appendable`；含 `is not appendable` → `full`；含 `not present` → `no-media`；都不含则**原样透传**（例如 `is offline`）。
4. `-toc` 失败的刻录机 `media` / `status` 留空，不阻塞枚举。

#### 4.1.2 输入形态判定（三平台共通，cli/burn/burn.go:206-264）

按下表顺序检查，命中即报错退出：

| 顺序 | 检查 | 结果 |
|---|---|---|
| 1 | 一个输入都没有 | 「用法: zpack burn <镜像.iso\|目录\|文件...> [旗标]」 |
| 2 | 每个输入都要存在（`os.Stat`，跟随符号链接） | 否则「输入不存在: <路径>」 |
| 3 | 是否镜像：非目录且扩展名是 `.iso` / `.img` / `.bin`（不分大小写） | — |
| 4 | 输入 ≥ 2 项且其中有镜像 | 「镜像文件（.iso/.img/.bin）只能单独刻录，不能与其他输入混排」 |
| 5 | 输入恰 1 项、既不是目录也不是镜像 | 「不识别的镜像扩展名（…）: <路径>」——单个散文件被拒 |
| 6 | 平台没有刻录后端 | 「暂不支持 <系统名>：…」 |
| 7 | `--mode live` | 「暂不支持 live：…」 |

- 名为 `xxx.iso` 的**目录**按目录处理（先判是否目录，再看扩展名）。
- 平台检查放在任何暂存动作**之前**：此前没有后端的平台上，目录 / 多输入会先生成镜像，报出冒号后空白的「生成临时镜像失败: 」，还把舞台目录留在用户目录里（2026-11-23 Windows 实测，cli/burn/burn.go:247-251）。
- 判定通过后定下卷标（4.1.6），再分派到平台后端。

#### 4.1.3 Windows 刻录流程

入口 `burnWindows`（cli/burn/burn_windows.go:94-180），全程在一次 COM 会话里：

| 步骤 | 做什么 | 失败时 |
|---|---|---|
| 1 | 按序号打开刻录机与数据写入器 | 序号越界 → 「--device N 超出范围…」；其余 → 引擎错误 |
| 2 | 读介质；没有盘 | 「刻录机里没有光盘——请放入光盘后重试」 |
| 3 | 写入前检查（4.3 的拒绝表） | 拒绝，不碰盘 |
| 4 | 带了 `--blank`：介质不可擦写 → 报错；可擦写 → **快速擦除** → **重读介质** → 再过一遍拒绝检查（这次按「不带 `--blank`」判） | 擦除失败或复查仍异常 → 报错，不写入 |
| 5 | 给了 `--speed`：落到支持的档位并设置（4.1.7） | 设置失败只记日志，按刻录机默认速度继续 |
| 6 | 准备数据流（见下）；在此之前，镜像是输入而盘不是空白时先打一条 `[WARN]`（4.1.3）——只警告，不拦 | 报错 |
| 7 | 容量预检：待写字节数 > 剩余空间 → 「内容大小 … 超过光盘剩余空间 …」 | 报错，不写入 |
| 8 | 设置是否封盘（`ForceMediaToBeClosed`） | 报错 |
| 9 | 写入（阻塞到完成，期间每 5 秒一条心跳） | 「写入光盘失败: …」 |
| 10 | 没带 `--no-eject` 就弹出 | 弹出失败只记日志，仍算成功 |

第 6 步「准备数据流」按输入形态分两条路（`burnWindowsSource`，cli/burn/burn_windows.go:219-284）：

- **镜像输入**：把文件打开成只读的系统文件流，整个文件原样交给写入器。**不走镜像库，不导入上一区段，不使用卷标**——盘上卷标就是镜像自带的。
  - **写进有数据的盘时先警告、再照常写入**（2026-11-24 用户拍板：警告后允许，不拦截——想故意这么刻的人不该被拦）。警告全文（`BURN_IMAGE_ON_USED_DISC_WARNING`，cli/burn/burn_windows.go）：

    > 这张盘已有内容，而镜像是原样写进新区段的，不会与盘上原有文件合并——写入后电脑上通常只看得到镜像里的文件，原有文件虽没被擦掉，但一般工具读不到。要把新内容追加到原有文件之后，请改用目录或文件作为输入；要保留原有文件可见，请换一张空白盘

  - 判定条件：恰一个输入且是镜像，并且介质不是「启发式空白」。带了 `--blank` 时在擦除、重读介质**之后**才判——擦完的盘是空白的，不警告。
  - 为什么原文文件会看不到：镜像自带一套完整的文件系统，原样落在新区段的起点上；操作系统挂载光盘时读的是**最后一个区段**里的文件系统，它并不认识上一个区段里的文件。数据物理上还在，只是一般的工具读不到（IsoBuster 这类按区段浏览的工具可以）。「通常」「一般工具」是有意留的余地——这个行为**没有实盘验证过**（第 9 节【待确认】#4）。
  - macOS / Linux 没有这条警告：这两个平台的 `burn` 不读介质状态，不知道盘是不是空白（第 5 节）。同样的后果在那两个平台上一样存在。
- **目录 / 多输入**：经 `IFileSystemImage` 在内存里建镜像的目录结构，文件内容在写入时才从磁盘按需读取，不落临时文件。
  - 文件系统：ISO9660 + Joliet + UDF 三种全建，限于当前介质与多区段类型支持的范围（internal/imapi2/engine_windows.go:375-379）。
  - 多区段：盘不是「启发式空白」时导入上一区段（`ImportFileSystem`），旧文件在新区段里保持可见（internal/imapi2/engine_windows.go:359-383）。
  - 单个目录 → 目录的**内容**加到镜像根（`AddTree`，引擎自己遍历，含空目录）。
  - 多输入 → 每项按唯一名加到镜像根：文件用 `AddFile`，目录先建同名目录再把整棵树加进去（internal/imapi2/engine_windows.go:409-445）。
  - 最后 `CreateResultImage` 得到结果镜像：`size_bytes` = `TotalBlocks × BlockSize`。

#### 4.1.4 macOS 刻录流程（cli/burn/burn.go:283-312、596-655）

1. 准备镜像文件：
   - 镜像输入 → 直接用。
   - 单个目录 → `hdiutil makehybrid -iso -joliet -ov -o <临时 ISO> <目录>`（长文件名走 Joliet）。
   - 多输入 → 先把各项挂进一个临时「舞台目录」再 makehybrid（4.1.8）。
2. `--device` 只支持 0；大于 0 直接报错（多刻录机寻址未做）。
3. 带了 `--blank` → 先 `drutil erase quick`（`drutil` 用的是默认光驱；此时 `--device` 只可能是 0）。
4. `hdiutil burn <镜像> [-speed N] [-forceclose] [-noeject]`。缺省不封盘，可追加。
5. 刻完删临时镜像与舞台目录。

#### 4.1.5 Linux 刻录流程（cli/burn/burn.go:657-683）

1. 准备镜像文件，同 macOS，只是生成命令换成 `xorriso -as mkisofs -volid <卷标> [-f] -joliet -rational-rock -o <临时 ISO> <目录>`；`-f` 只在多输入的舞台目录上加（跟随符号链接）。
2. 重新枚举设备（`xorriso -devices`），把 `--device` 序号换成设备路径；越界 → 「--device N 超出范围…」。
3. `xorriso -as cdrecord -v dev=<设备路径> [speed=N] [blank=as_needed] [-multi] [-eject] <镜像>`。
   - 不封盘时加 `-multi`（可追加）；`--close` / `mastered` 时不加（封盘）。
   - `--blank` → `blank=as_needed`，由 xorriso 按介质自动选擦除方式。
   - `--no-eject` → 不加 `-eject`。
4. 刻完删临时镜像与舞台目录。

#### 4.1.6 光盘名称（卷标）

**规则（2026-11-24 用户定案，AGENTS.md 陷阱 #9）**：名称属于最后一个区段——空白盘随意起；已有内容的盘缺省沿用、显式 `--volid` 才改；已封盘的不能改（写入前就被拒绝）。

**第一步，命令层定下「本次的卷标」**（`burnResolveVolumeName`，cli/burn/burn.go:544-557）：

| 情形 | 结果 |
|---|---|
| 显式给了 `--volid` | Windows：`burnWindowsVolumeName(值)`；macOS / Linux：原样 |
| 没给，单个目录输入 | 目录名（取绝对路径的基名，带尾部分隔符也一样） |
| 没给，其余形态（镜像 / 多输入 / 散文件） | 当前工作目录的目录名（沿用最初的做法） |

目录名与当前目录名还要过一遍平台规范化（`burnPlatformVolumeName`，cli/burn/burn.go:561-566）：Windows 走 `burnWindowsVolumeName`，macOS / Linux 走 `burnSanitizeVolid`。

**第二步，Windows 后端决定「真的写哪个」**（cli/burn/burn_windows.go:237-247，internal/imapi2/imapi2.go:354-367、engine_windows.go:333-348、380-386）：

- 盘是空白盘（`HeuristicallyBlank`）→ 用第一步的名字。带 `--blank` 的盘擦完重读就是空白盘，同样可以起名。
- 盘上已有内容且**没给** `--volid` → 第一步的名字作废，按下面的优先级沿用盘上原名：
  1. **Windows 显示的卷标**（`GetVolumeInformation`，Unicode 无损）——只要盘有盘符就读得到；
  2. 读不到盘符时，才去还原引擎的 `ImportedVolumeName`，且只在能确定无损时才用（`sanitizeVolumeName`）；
  3. 都不行 → 放弃沿用、不写任何名字，交给引擎自定（宁可不沿用，也不写乱码）。
- 盘上已有内容且**给了** `--volid` → 用第一步的名字，改名生效。
- 「已有内容」的判据是「不是启发式空白」，不是「有没有剩余空间」：DVD+RW / DVD-RAM 这类覆写介质主要看启发式判定（internal/imapi2/imapi2.go:137-138）。

**第三步（只对目录 / 多输入），各平台的落点**：

| 平台 | 卷标怎么到盘上 |
|---|---|
| Windows | 写进镜像库的 `VolumeName`。三种文件系统各自按自身上限截断，中文名在 UDF / Joliet 层原样保留，ISO9660 层变成下划线——Windows 读 UDF，所以显示中文 |
| Linux | 作为 `-volid` 交给 `xorriso -as mkisofs`。**追加区段时不沿用盘上名称**：新卷标恒是第一步算出的名字（目录名 / 当前目录名 / 显式值） |
| macOS | `hdiutil makehybrid` 与 `hdiutil burn` 都没有传卷标的参数，`--volid` 在 macOS 上**没有任何效果**【待确认】是有意保留的旧行为还是遗漏 |

**Windows 的温和规范化**（`burnWindowsVolumeName`，cli/burn/burn_windows.go:21-51；显式与缺省名都过）：

| 处理 | 原因 |
|---|---|
| 控制字符直接丢弃 | 引擎拒收含控制字符的卷标 |
| `\ / : * ? " < > \|` 换成 `_` | 引擎会把它们原样留在 UDF 卷标里（变成 Windows 路径里的非法字符） |
| 去首尾空白 | — |
| 超过 32 个**字符**截到 32 | 引擎不报错，但各文件系统各自截断（Joliet 截 16，ISO9660 截 32） |
| 清理后为空 → `ZPACK` | 同 `burnSanitizeVolid` 的兜底 |

**macOS / Linux 的缺省名规范化**（`burnSanitizeVolid`，cli/burn/burn.go:568-585）：转大写，只保留 `A-Z` `0-9` `_`，去掉首尾下划线，为空则 `ZPACK`，最多 32 个字符。纯中文名会变成 `ZPACK`，`备份2024` 会变成 `2024`。显式给的 `--volid` 不过这一步（Linux 上原样交给 xorriso，由它去处理）。

**镜像输入**：三个平台都不使用 `--volid`，也不走缺省名规则——卷标来自镜像自身。

#### 4.1.7 写入速度（`--speed`）

- Windows（`PickWriteSpeed`，internal/imapi2/imapi2.go:210-238）：把「N 倍速」落到引擎报告的该介质写入速度表上——取**不超过请求值的最高档**；全都高于请求值时取最低档。落档后记一条日志说明实际倍速。
  - 倍速基准（1×）：CD 176.4 kB/s、DVD 1385 kB/s、DVD-RAM 2770 kB/s、BD 4495.6 kB/s。引擎的速度表单位是 kB/s，而 `SetWriteSpeed` 的单位是扇区/秒，中间按 2048 字节/扇区换算（cli/burn/burn_windows.go:147-149）。
  - 速度表为空时按请求值原样设置。
  - 真机例：DVD-R 的速度表是 `[5410 2705]`（约 4× / 2×）——请求 8× 落到 4×，请求 1× 升到 2×，请求 3× 取 2×。
- macOS：`hdiutil burn -speed N`，直接交给工具。
- Linux：`xorriso -as cdrecord speed=N`，直接交给工具。

#### 4.1.8 临时文件与清理

| 平台 | 会产生什么 | 在哪 |
|---|---|---|
| Windows | 无 | — |
| macOS / Linux，目录或多输入 | 临时 ISO | `<系统临时目录>/zpack-burn-<pid>.iso` |
| macOS / Linux，多输入 | 舞台目录（名字以 `.zpack-burn-stage-` 开头） | 输入们的最长公共祖先目录（同卷才能硬链接）；公共祖先不可写时回退系统临时目录 |

多输入的舞台怎么搭（`burnStageInputs` / `burnLinkEntry`，cli/burn/burn.go:366-453）：

- 顶层条目名按输入顺序取基名，重名依次加 `-2`、`-3`（序号加在扩展名之前，每次从原名起算，不会出现 `a-2-3.txt`）。这套命名 mac/Linux 的舞台和 Windows 的镜像库**共用**（`burnUniqueEntryNames`，cli/burn/burn.go:316-331）。
- 散文件：先试硬链接（零拷贝），失败（跨卷、权限等）回退拷贝。
- 目录：macOS 逐文件硬链接克隆整棵树（跨卷文件回退拷贝，符号链接 / 设备等非常规文件不进盘）；Linux 直接建符号链接，靠 `-f` 让 mkisofs 跟随。
- 舞台建在公共祖先目录而不是系统临时目录，是为了让硬链接同卷生效。

清理是**显式调用**的，不靠 `defer`：输出函数一调用进程就退出，外层 `defer` 不会执行（cli/burn/burn.go:310）。任何失败路径都会清：舞台搭到一半失败、镜像生成失败，由 `burnStageInputs` 自己清场并把错误**返回**给调用方（不在里面直接输出——那样调用方就无从清理了，cli/burn/burn.go:364、597、686）。

### 4.2 边界与退化输入

| 情形 | 行为 |
|---|---|
| 单个散文件 | 拒绝：「不识别的镜像扩展名（支持 .iso/.img/.bin；文件/文件夹请与其他内容一起多输入）: <路径>」。想刻一个文件，再拖一个文件或文件夹进来即可 |
| 镜像 + 其他输入 | 拒绝，不写盘 |
| 重名的多输入 | 后来的落为 `名字-2`、`名字-3`（扩展名之前），内容各归各；目录重名同理（`资料-2`） |
| 空目录 | Windows 保留（真机验证过空文件夹进了盘） |
| 超长 / 中文 / 带空格的文件名 | Windows 经 Joliet + UDF 保留：52 字符中文长文件名、60 字符文件名、带空格文件名、三层深路径都完整（真机验证） |
| `--volid ""`（显式给空串） | Windows 规范化后回退 `ZPACK`。Linux 上 `burnStageISO` 见到空卷标会改用目录名的规范化结果（cli/burn/burn.go:600-603）：单个目录时就是目录名；多输入时是临时舞台目录名，卷标会变成形如 `ZPACKBURNSTAGE123456789` 的字符串。macOS 本来就不传卷标 |
| 一次性盘 + `--blank` | Windows 直接报「当前介质 <类型> 不可擦除…」，**即使盘是空白的**也不继续 |
| `--speed -1`、`--device -1` | 负值被当成下一个旗标，静默忽略，取缺省 |
| 旗标后面没跟值 | 该旗标静默忽略，取缺省 |
| 输入路径以 `-` 开头 | 没法表达：没有 `--` 分隔符，会被当成旗标 |
| 没有刻录机 | `burn devices` 成功 + 空数组；`burn <输入>` 失败：「--device 0 超出范围（共 0 台，见 zpack burn devices）」。macOS / Linux 上这个错误发生在临时镜像生成**之后**（镜像照样会清理） |
| 刻录机里没放盘 | Windows：「刻录机里没有光盘——请放入光盘后重试」；macOS / Linux 由外部工具报错 |
| 内容超过盘容量 | Windows 写入前预检并报错；macOS / Linux 不预检，由 hdiutil / xorriso 报错 |
| `.bin` 文件的内容其实不是光盘镜像 | 照样直烧，不校验内容；刻出来的盘能不能读由内容决定 |
| 输入是符号链接 | 按 `os.Stat` 的结果分类（跟随链接）。Windows 上目录内部的符号链接 / 联接点如何进盘由引擎决定，未实测【待确认】 |

### 4.3 错误处理

**写入前的拒绝规则**（Windows；`burnMediaRefusal`，cli/burn/burn_windows.go:187-215）。按顺序判定，命中即返回：

| 顺序 | 条件 | 不带 `--blank` 时的 `error_info` | 带 `--blank` 时 |
|---|---|---|---|
| 0 | 介质状态为「无法识别」（`unrecognized`，见 6.10） | `光驱认不出这张光盘，无法写入；请检查：盘是否放反（标签面朝上）、是否卡牢、盘片类型是否超出刻录机能力（如蓝光盘放入 DVD 光驱）、盘片是否划伤或太脏` | 同左，不放行 |
| 1 | 状态位含「已封盘」（FINALIZED） | `光盘已封盘，无法继续写入` | 同左，不放行 |
| 2 | 含「写保护」且介质类型是只读类型（CD-ROM / DVD-ROM / HD DVD-ROM / BD-ROM） | `光盘是只读盘（<类型>：压盘或已封盘），无法继续写入` | 同左，不放行 |
| 3 | 含「写保护」，其余类型 | `光盘处于写保护状态，无法写入` | 同左，不放行 |
| 4 | 含「不支持」（UNSUPPORTED） | `刻录机不支持在 <类型> 上刻录` | 同左，不放行 |
| 5 | 含「损坏」（DAMAGED） | `光盘状态异常（上一个区段没有正常关闭或需要恢复），无法写入；<提示>` | 不拦，交给擦除流程 |
| 6 | 含「上一区段写了一半」（NON_EMPTY_SESSION） | `光盘上一个区段没有写完，无法继续写入；<提示>` | 不拦，交给擦除流程 |
| 7 | 含「须先擦除」（ERASE_REQUIRED） | `光盘需要先擦除才能写入；<提示>` | 不拦，交给擦除流程 |
| 8 | 数据写入器不支持当前介质 | `当前介质 <类型> 不支持数据刻录` | 同左，不放行 |

- `<提示>`（`burnRecoveryHint`）：介质可擦写 → `加 --blank 先擦除再刻`；否则 → `请换一张空白盘`。**已经擦过一遍**（擦完重读仍异常）时一律是「请换一张空白盘」——不能再叫人加 `--blank`，他已经加过了。
- **原则：任何异常状态都不得走到写入**。带 `--blank` 放行的那三种（5、6、7）不是「放行写入」，而是「放行去擦除」——擦完重读介质，再按不带 `--blank` 的规则判一次（这次 `<提示>` 一律是「请换一张空白盘」）。擦完仍异常就还是拒绝。
- **「不支持」排在「损坏 / 须先擦除 / 半截区段」之前**，与 `burn devices` 的 `StatusName` 同一顺序（2026-11-24 对齐；此前 `DAMAGED | UNSUPPORTED` 的盘一边显示 `unsupported`、一边被拒成「光盘状态异常…请换一张空白盘」）。理由：刻录机根本写不了这种盘，恢复与否无从谈起，「不支持」是更根本的结论。同一张盘，两处给同一个结论。
- 状态位里只有「空白」（BLANK）、「可追加」（APPENDABLE）、「只能再写一个区段」（FINAL_SESSION）、「只能覆写」（OVERWRITE_ONLY）的盘不拦。
- `burn devices` 里显示 `full` 的盘，写入时的拒绝文案取决于命中哪一行：封盘位 → 「光盘已封盘」；只读类型 + 写保护位 → 「光盘是只读盘（…：压盘或已封盘）」。实测封盘后的 DVD-R 是后者（它对外报成 DVD-ROM，见 6.4）。

**引擎调用失败**（Windows）：统一收敛成 `CallError`，带引擎真实错误码（`SCode`）与系统给的本地化说明，文案格式见 3.3。按错误码识别错误用 `IsSCode`，不要比对文案（internal/imapi2/imapi2.go:267-270）。

**失败之后的状态**：

- 写入之前失败：盘没有被动过。
- 写入途中失败或进程被杀：已写部分留在盘上，没有回滚。一次性盘上这种「半截区段」会怎样见 6.9（实测强杀进程后引擎把它当空白盘）。
- macOS / Linux：刻录命令的报错原文在 stderr 里，`error_info` 只有「… 刻录失败: exit status N」。
- macOS / Linux 的舞台目录与临时 ISO：成功、失败都清理。
- 弹出失败不算失败：exit 0，stderr 里有一行「光盘已刻录完成，但弹出未成功: …」。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 刻录后端 | 系统自带引擎 IMAPI2：`imapi2.dll`（枚举 / 刻录机 / 数据写入 / 擦除）+ `imapi2fs.dll`（目录 → 镜像）；文件流用 shlwapi 的 `SHCreateStreamOnFileEx` | 系统自带 `hdiutil`（刻录、生成镜像）+ `drutil`（枚举、擦除） | xorriso（libburnia）；未安装报安装引导 |
| 外部依赖 | 无。代码只依赖 go-ole（纯 syscall），`CGO_ENABLED=0` 即可用 | 无（系统自带） | 需要自行安装 xorriso |
| 设备枚举 | `IDiscMaster2` 取刻录机，数据写入器读介质状态 | `drutil list` + `drutil status` | `xorriso -devices` + 逐台 `-toc` |
| `path` 字段 | 盘符（`E:\`）；没有挂载点时是引擎唯一标识 | 空串 | 设备路径（`/dev/sr0`） |
| 能报出的 status | `blank` `appendable` `full` `no-media` `damaged` `erase-required` `write-protected` `unsupported` `unrecognized` `unknown` | 只有 `no-media`（有盘时不报） | `blank` `appendable` `full` `no-media`；认不出的短语原样透传 |
| 镜像写进有数据的盘时的 `[WARN]` | 有（4.1.3） | 没有：刻录流程不读介质状态，不知道盘是不是空白 | 没有：同左 |
| 目录 / 多输入 | 系统镜像库流式生成后直烧，无临时 ISO | `hdiutil makehybrid` 产临时 ISO；多输入先聚舞台（目录逐文件硬链接克隆） | `xorriso -as mkisofs` 产临时 ISO；多输入先聚舞台（目录挂符号链接 + `-f`） |
| 镜像输入 | 系统文件流直烧 | `hdiutil burn` | `xorriso -as cdrecord` |
| 追加时导入上一区段 | 目录 / 多输入：引擎 `ImportFileSystem`，旧文件在新视图里可见（实测）。镜像输入不导入，写进有数据的盘时先警告（4.1.3） | 不导入。旧文件在新视图里是否可见【待确认】 | 不导入（没有用 mkisofs 的 `-M`）。旧文件在新视图里是否可见【待确认】 |
| 缺省卷标 | 目录名 / 当前目录名，**可以用中文** | 目录名 / 当前目录名，只保留 `A-Z` `0-9` `_` | 同 macOS |
| 显式 `--volid` | 温和规范化后生效 | 没有传给任何工具，没有效果【待确认】 | 原样作 `-volid` 交给 mkisofs |
| 追加区段沿用盘上名称 | 是（以 Windows 显示的卷标为准） | 否 | 否 |
| `--device` | 任意序号 | 只支持 0 | 任意序号 |
| `--speed` | 落到介质与刻录机支持的档位 | 原样 `-speed N` | 原样 `speed=N` |
| `--blank` | 快速擦除（`IDiscFormat2Erase`）；一次性盘报错 | `drutil erase quick` | `blank=as_needed` |
| 封盘 | `ForceMediaToBeClosed` | `-forceclose` | 不加 `-multi` |
| 不弹出 | 不调 `EjectMedia` | `-noeject` | 不加 `-eject` |
| 容量预检 | 有（写入前） | 无 | 无 |
| 进度 | 每 5 秒一条心跳日志 | 工具自己的输出 | 工具自己的输出 |
| 临时文件 | 无 | 临时 ISO（多输入另有舞台目录） | 临时 ISO（多输入另有舞台目录） |
| 成功 JSON 的 `image` | 第一个输入的绝对路径 | 临时 ISO 的路径（已删除） | 同 macOS |
| 成功 JSON 的 `size_bytes` | 本次写入的字节数 | 非目录输入的文件大小之和，不是写入量 | 同 macOS |
| 写入前拒绝 | 有（按介质状态） | 无（交给外部工具） | 无（交给外部工具） |
| 真机验证 | 已实盘验证（见 6.12） | 未实跑，只做过交叉编译与 vet | 未实跑，只做过交叉编译与 vet |

**纯 Go 构建与完整版（cgo 构建）在本功能上没有差别。** Windows 后端只经 go-ole 调系统 DLL，`CGO_ENABLED=0` 的产物就能刻录（AGENTS.md:237）。构建约束只在文件层：

- `internal/imapi2/imapi2.go` 没有构建标签——里面是常量、介质表与纯函数，三个平台都编译，它的单测三个平台都跑。
- `internal/imapi2/engine_windows.go`、`call_windows.go`、`imapi2_live_windows_test.go` 与 `cli/burn/burn_windows.go`、`burn_windows_test.go`、`burn_name_windows_test.go` 仅 Windows 编译。
- `cli/burn/burn_other.go` 是非 Windows 平台上的占位：`runtimeOS()` 在那些平台上不会等于 `windows`，分支不可达，存在只是为了让 `burn.go` 三平台都能编译（cli/burn/burn_other.go:3-4）。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 为什么三个平台各用各的后端

- **定案**（2026-11-24 用户定案，AGENTS.md 铁律 5 与「刻录引擎定案」段）：刻盘这件事三个平台分别实现，各用该平台上**最简单、最可靠**的做法，不强求同一个库，不为统一引擎做跨平台移植，取舍不考虑沉没成本。对用户始终是同一个 `zpack burn` 和同一个 GUI 刻录页：旗标与输出契约一致，平台差异全部收在后端里（cli/burn/doc.go:3-5）。
- **被否掉的方案一：把 libburnia（xorriso 的底层库）移植到原生 Windows**。官方支持的系统只有 GNU/Linux、FreeBSD、Solaris、NetBSD，Windows 仅经 Cygwin；`configure` 的 `mingw*` 分支只是默认启用 libcdio 传输层。实测把它移植到 UCRT64（约 10 个 POSIX 垫片头 + 4 处源码补丁 + 一层盘符路径翻译 + libtre 提供正则）：**读通了**（进程内枚举光驱、读介质 / 区段 / 卷标），**写没通**（生成 ISO 9660 短名失败、引擎报可写块为 0），剩余工作量不确定，用户叫停。
- **被否掉的方案二：UltraISO**。它不是开源库——闭源商业软件（EZB Systems），没有编程接口；静态分析其刻录部分是自有的 SPTI 引擎。同类的 NeroAPI / cdrecord.exe / cdrdao.exe / cdburn.exe / dvdburn.exe / ImgBurn 也都不用 IMAPI2，一并排除。
- **落地的做法**：Windows 用系统自带的 IMAPI2——用户定案「有现成成熟的库就不要造轮子」，镜像生成、文件流、写入调度全部用系统现成能力，本仓库只做调用编排（internal/imapi2/imapi2.go:1-11）。这是资源管理器「母盘式」刻录用的同一套系统组件，所以行为与资源管理器一致（比如首区段会被填充，见 6.7）。
- **为什么是自己写胶水**：Go 生态没有 IMAPI2 的现成封装（2026-11-24 检索，只有 2026-07 新建的 deploymenttheory/go-bindings-win32，里面是生成的接口定义，没有调用编排，未采用）。Windows 上那约 1700 行调系统的胶水无法由第三方库替代，可靠性靠真盘验证。
- **改动含义**：不要为了让 Linux 的代码「复用」而把 xorriso/libburnia 的逻辑搬到 Windows，也不要把 IMAPI2 的语义照搬到 macOS / Linux。三份后端只需要在旗标与输出契约上对齐。

### 6.2 模式语义与多输入（两条用户定案）

- **缺省不封盘**。用户痛点：Linux 上常见的做法默认 DAO 封盘，连空白盘都只能烧一次。所以 `--mode multi` 是缺省——区段不封盘，烧完可继续追加；`--close` 才显式封盘（最大兼容，老设备只认终章）；`--mode mastered` = 单区段定稿，等价于 multi + 封盘（cli/burn/burn.go:14-18）。
- **`live` 没做**：UDF 包写入的「优盘模式」三个平台都没接入，如实报不支持；Windows 上计划走 `IDiscFormat2RandomWrite`。
- **多输入**（2026-11-22 用户定案）：像 compress 一样接受任意数量的文件 / 文件夹——单 `.iso` 镜像直烧；单目录产 ISO；多输入 / 散文件先聚成临时舞台目录再产 ISO（cli/burn/burn.go:207-208）。镜像不能与其他内容混排，Windows 上它甚至不经过镜像库而是整文件直烧（cli/burn/burn_windows.go:220-235），技术上没法再和别的文件装进同一个根。

### 6.3 Windows 引擎的调用方式（陷阱 #1–#3）

引擎对象一律**按 CLSID 创建、按方法名调 IDispatch**。五个对象：

| 用途 | 接口 | CLSID | 所在 DLL |
|---|---|---|---|
| 设备枚举 | `IDiscMaster2` | `{2735412E-7F64-5B0F-8F00-5D77AFBE261E}` | imapi2.dll |
| 刻录机 | `IDiscRecorder2` | `{2735412D-7F64-5B0F-8F00-5D77AFBE261E}` | imapi2.dll |
| 数据写入 | `IDiscFormat2Data` | `{2735412A-7F64-5B0F-8F00-5D77AFBE261E}` | imapi2.dll |
| 擦除 | `IDiscFormat2Erase` | `{2735412B-7F64-5B0F-8F00-5D77AFBE261E}` | imapi2.dll |
| 目录 → 镜像 | `IFileSystemImage` | `{2C941FC5-975B-59BE-A960-9A2A262853A5}` | imapi2fs.dll |

出处：internal/imapi2/engine_windows.go:18-24。

- **陷阱 #1：本机没有 imapi2ms.dll（脚本封装层）**。ProgID（`IMAPI2.MsftDiscMaster2` 等）没注册，PowerShell 的 `New-Object -ComObject` 报「类未注册」。解法就是上表——按 CLSID 创建核心对象（CLSID 取自注册表 `HKCR\CLSID`，与 SDK 的 coclass 定义逐个核对）；核心对象自带类型信息，可以按方法名解析。接口名与枚举值取自 microsoft/win32metadata 里的 SDK IDL（`generation/WinSDK/RecompiledIdlHeaders/um/imapi2*.idl`、`imapi2*error.h`）。**别凭记忆写方法顺序**——实测与印象不同。
- **陷阱 #2：go-ole 的边界**（v1.3.0）：
  - `Item` 是带参属性，必须用 `GetProperty` 取（internal/imapi2/engine_windows.go:89、411）。
  - `VolumeNameISO9660` / `VolumeNameJoliet` / `VolumeNameUDF` 只读，只有 `VolumeName` 可写；各文件系统按自身上限自动截断（AGENTS.md 陷阱 #2 记为 ISO9660 30 字符 / Joliet 16 字符；陷阱 #9 与代码注释则按 ISO9660 32 字符取上限——以哪个为准【待确认】）。
  - go-ole 对 `*VARIANT` 只按引用传、不支持 `VT_UNKNOWN`，而数组参数（`MultisessionInterfaces`）与流参数（`Write` / `AddFile`）要求**按值**。解法是 `invokeRaw`：自带一份 `DISPPARAMS` 镜像结构，按值走同一条 `IDispatch::Invoke` 通道，参数自右向左排列（internal/imapi2/call_windows.go:32-100）。「接口指针按值传入」的编组有真机只读测试守着（internal/imapi2/imapi2_live_windows_test.go:77-87）。
  - **按声明顺序数虚函数表槽位不可靠**——实测偏到了别的方法上、返回 `IMPORT_SEEK_FAILURE`，已弃用。整个包不出现任何虚函数表偏移（internal/imapi2/call_windows.go:4-5）。
- **陷阱 #3：空白的一次写入盘上，设置 `MultisessionInterfaces` 返回 `IMAPI_E_IMPORT_SEEK_FAILURE`（0xC0AAB156）**。官方文档注明的预期行为（没有上一区段可导入）。代码只在「盘是启发式空白 **且** 错误码正是它」时容忍并继续，其余情况照样报错（internal/imapi2/engine_windows.go:365-370，internal/imapi2/imapi2.go:71-73）。
- **COM 是每线程初始化的**：`WithSession` 钉住 OS 线程再 `CoInitializeEx(APARTMENTTHREADED)`。goroutine 被调度器迁到没初始化的线程上会报错——office 引擎 2026-11-08 踩过同一个坑（internal/imapi2/call_windows.go:19-30）。所有引擎调用都要在同一个会话闭包里完成。
- **错误收敛**：go-ole 的调用错误统一收敛成 `CallError{Operation, HResult, SCode, Message}`。外层 `HResult` 多是 `DISP_E_EXCEPTION`，`EXCEPINFO` 里的 `SCode` 才是引擎真实错误码；说明文字取系统给的本地化文案，引擎没给就为空（internal/imapi2/call_windows.go:102-136）。**按错误码识别错误**（`IsSCode`），不要比对文案。
- **`ForceOverwrite = true`**：可覆写介质（DVD±RW / BD-RE）非空白时写入的前提；一次性介质上没有这个语义。代码是无条件设置的，真机刻一次性盘没有受到影响（internal/imapi2/engine_windows.go:159-163）。
- **多区段接口必须先设**：`MultisessionInterfaces` 要来自已绑定刻录机的写入器，并且先于其他配置设置（internal/imapi2/engine_windows.go:358-365）。

### 6.4 介质状态语义（陷阱 #5、#11）

介质状态是一个位标志（`IMAPI_FORMAT2_DATA_MEDIA_STATE`，internal/imapi2/imapi2.go:54-66）。**取值以微软官方文档为准**：

| 位 | 值 | 含义 |
|---|---|---|
| OVERWRITE_ONLY | 0x0001 | 只能覆写 |
| BLANK | 0x0002 | 空白 |
| APPENDABLE | 0x0004 | 可追加 |
| FINAL_SESSION | 0x0008 | 只能再写一个区段 |
| DAMAGED | 0x0400 | 需擦除或其他恢复才能用 |
| ERASE_REQUIRED | 0x0800 | 须先擦除 |
| NON_EMPTY_SESSION | 0x1000 | 上一区段写了一半（不支持） |
| WRITE_PROTECTED | 0x2000 | 介质或刻录机写保护 |
| FINALIZED | 0x4000 | 已封盘 |
| UNSUPPORTED | 0x8000 | 数据写入器不支持该介质 |

- **陷阱 #5：封盘的一次写入盘不报「已封盘」位**。真机实测封盘后的 DVD-R：介质类型报成 `DVD-ROM`，状态是 `0xA000` = 写保护 | 不支持，**不含封盘位**（当时把它误读成含封盘位）。所以判定规则是「只读介质类型 + 写保护位 → 已封盘或只读」，与压盘无法区分；只有可擦写类型上的写保护位才报「写保护」（internal/imapi2/imapi2.go:159-164、186-196）。
- **没放盘不是空值而是错误码**：读介质类型返回 `E_IMAPI_RECORDER_MEDIA_NO_MEDIA`（0xC0AA0202），归一成 `no-media`（internal/imapi2/engine_windows.go:220-223）。
- **空白 DVD-R 的实测状态是 BLANK | APPENDABLE**，所以「空白」的优先级要排在「可追加」之前。
- **覆写介质（DVD±RW 等）带着数据时的常态是 OVERWRITE_ONLY**，视同可追加（随机写多区段）。
- **陷阱 #11：内置光驱与外接光驱没有区别，盘不可写时先查盘**（2026-11-24 用户要求「不要区分外接 / 内置」；Win10 22H2 + 内置 HL-DT-ST DVD-RAM GHC0N）。枚举走 `IDiscMaster2`，与总线无关——厂商、型号、盘符、介质类型、写速表全部正常，空白盘刻录与追加区段均通过。同一台机器上另一张 Windows 刻录过的 DVD-R（UDF、日期卷标、含两个扫描 PDF）被引擎判 `damaged`（0x400，官方注释「not usable by this interface, might require an erase or other recovery」）：上一区段地址 -1、容量 0、多区段导入报 `IMAPI_E_NO_COMPATIBLE_MULTISESSION_TYPE`（0xC0AAB15C），跳过导入强行 `Write` 报 `E_IMAPI_DF2DATA_INVALID_MEDIA_STATE`（0xC0AA0402）。SCSI 直通只读探针看到这张盘 `GET CONFIGURATION` 返回空、`READ DISC INFORMATION` 自相矛盾（可擦除 + 盘状态空 + 末区段未关闭）——**光驱认不出盘的规格，读得到数据区但无法记录，软件层面无解，只能换盘**。
- **新盘光驱也可能认不出**：曾出现两张新放进去的盘被报 `E_IMAPI_RECORDER_MEDIA_INCOMPATIBLE`（0xC0AA0203「介质不兼容或为未知的物理格式」）、刻录机 `CurrentProfiles` 为空、`READ_TOC` 报「设备未就绪」、Windows `MediaLoaded=False`，连续观察半分钟不变；疑为盘型超出光驱能力（那台机器不支持蓝光）、放反或没卡牢。换了盘之后不再出现。遇到类似现象先怀疑盘，不是代码回归。2026-11-24 起这种盘归一成 `unrecognized`，`note` 与写入拒绝文案都带排查提示。
- **「只读类型 + 写保护」要再读一次文件系统才能定性**：读不出内容的盘有两种形态——上面这种直接报「介质不兼容」；另一种是被报成 `DVD-ROM / full`，但从第 0 扇区起就是读错误（6.10）。所以 `Media` 在「只读类型 + 写保护位」时追加一次 `IdentifyFileSystemsOnDisc`：一个受支持的数据文件系统都读不出、且不是因为盘上本来就没有（音频 CD 之类）才算 `unrecognized`；读得出就还是 `full`。**这条追加读取未实盘验证**——写它的时候手边没有这种形态的盘，只有前一种。
- **改动含义**：新增或调整 status 取值时，要同时改三处——`StatusName`（internal/imapi2/imapi2.go:170-197）、`burnMediaRefusal`（cli/burn/burn_windows.go:195-218）、GUI 的 `burnBlockReason` / `burnMediaSuffix` / `planBurnVolumeName`（ui/gui/burnstate.go、burnname.go）。三处各有表驱动单测守着。

### 6.5 写入前的拒绝（不可写介质的统一处理）

- **起因**：2026-11-24 内置光驱实测发现，在引擎判为 `damaged` 的 DVD-R 上，**镜像直烧路径会直接调到 `Write`**（cli/burn/burn_windows.go:184-186）。而强行写入的结局是引擎报 `E_IMAPI_DF2DATA_INVALID_MEDIA_STATE`。
- **做法**：把拒绝规则收敛成一个纯函数 `burnMediaRefusal(media, erasing)`，放在介质读取之后、任何写动作之前，镜像 / 目录 / 多输入三条路径共用（cli/burn/burn_windows.go:126-128、187-207）。规则见 4.3。
- **原则**：任何异常状态都不得走到 `Write`。可擦写盘的异常状态（损坏 / 须先擦除 / 上一区段写了一半）擦除即恢复，所以带 `--blank` 时交给擦除流程、**擦完重读介质再判**；一次性盘没有擦除这条路，当场拒绝并提示换盘。
- **实测**：补拦之后，在那张 `damaged` 的盘上，目录、`--blank`、镜像三条路径都在写入前返回了明确的 `error_info`。
- **GUI 侧的对应规则**（纯函数在 ui/gui/burnstate.go 与 ui/gui/burnname.go，表驱动单测守着；GUI 本身的细节见 gui spec）：
  - 设备下拉按状态加括注（`burnMediaSuffix`：`（空白）` `（可追加）` `（已满）` `（无介质）` `（状态异常）` `（需先擦除）` `（写保护）` `（不支持）` `（认不出）`），认不出的状态不加括注。
  - 光盘名称行按状态给说明（`planBurnVolumeName`，见 6.6）。
  - 主按钮置灰并写明原因（`burnBlockReason`：`光盘已封盘` / `未放入光盘` / `光盘状态异常` / `光盘需先擦除` / `光盘被写保护` / `不支持这张光盘` / `光驱认不出这张光盘`）。**只拦明确认识的状态**：macOS 有盘时 status 为空、Linux 可能是原样短语、Windows 的 `unknown`，都不拦，交给引擎裁决——这样就不会误拦 mac / Linux。可擦写盘勾了「先擦空再烧」时 `damaged` 与 `erase-required` 放行。
- **改动含义**：以后新增任何写路径（比如 live 模式），第一件事是过 `burnMediaRefusal`。

### 6.6 光盘名称（陷阱 #4、#9）

**规则**（2026-11-24 用户定案）：名称属于最后一个区段——空白盘随意起；已有内容的盘缺省沿用、显式 `--volid` 才改；已封盘的不能改。行为见 4.1.6，下面是来龙去脉。

- **缺省名取目录名**：单个目录输入用目录名（目录叫「照片备份」，刻出来盘名就是「照片备份」，真机验证过），其余形态（镜像 / 多输入 / 单文件）沿用最初的做法取当前目录名（`burnResolveVolumeName`，cli/burn/burn.go:541-557）。
- **陷阱 #4（上半）：引擎导入上一区段后不沿用卷标**。导入之后镜像里的卷标是空的，不显式沿用，新区段就变成时间戳卷标——实测追加一次之后盘名由 `ZPACK_TEST_VOLUME` 变成了 `2026.11.24_0118`。解法：`applyVolumeName` 显式把卷标写回去（internal/imapi2/engine_windows.go:330-348）。
- **陷阱 #4（下半）：`ImportedVolumeName` 不能原样写回，对中文名根本不可靠**。这个属性对 UDF 盘带着 dstring 的「压缩标识」前导字节（实测读回 `"\bZPACK"`），原样写回被判「含无效字符」（`IMAPI_E_INVALID_VOLUME_NAME`）。
  - 第一版假设：「16 位码元被按小端解读」，据此做还原（`sanitizeVolumeName`）。
  - **2026-11-24 二轮更正：这个假设是错的。** 实测引擎对 `ImportedVolumeName` 是**一个字节一个字符**地给出的，而且经过了本机 ANSI 代码页转换——「照片备份」里的 0xFD 读回成了 `?`。按旧假设还原得到的是乱码「焀最爀䜀夀一㼀」，**并且被写成了盘名**（还会在 0x00 处截断，实测读回只剩 `\x10q`）。
  - 现在的做法：沿用盘名**以 Windows 显示的卷标为准**——`mountedVolumeLabel` 调 `GetVolumeInformation`，Unicode 无损（internal/imapi2/engine_windows.go:311-328）。读不到盘符时才退回引擎读回值，且只在能确定无损时才还原（`sanitizeVolumeName`），否则放弃沿用、不写乱码（`chooseVolumeName`，internal/imapi2/imapi2.go:354-367）。
  - `sanitizeVolumeName` 的无损判定（internal/imapi2/imapi2.go:297-343）：带压缩标识的字节串——8 位形态不许有被代码页合并出的字符（≥ 0x100，说明字节边界已错位），带高位字节时又不许出现 `?`（丢失的字节正是变成了它）；16 位形态里 `?` 也可能是合法的低字节，分不清丢没丢，所以只认「纯 ASCII 且没有 `?`」的字节串。不带压缩标识的普通字符串只清理控制字符。
  - **实测**：修复后，不给 `--volid` 追加目录 → 中文名保持不变；显式 `--volid` 追加 → 改名生效；`TestLiveReadOnly` 在有内容的盘上断言「沿用名 = Windows 卷标」通过。
- **Windows 上中文名可用**：系统镜像库原生接受 Unicode（探针实测「备份」→ UDF / Joliet 原样、ISO9660 层变成 `__`，Windows 读 UDF 所以显示中文）。所以 Windows 不做字符集过滤，只做温和规范化 `burnWindowsVolumeName`（控制字符丢弃、路径非法字符换下划线、截 32 字符；规则表见 4.1.6）。真机验证：显式 `--volid` 含路径非法字符 → 全部换成下划线（`a_b_c_d_e_f_g_h_i_j`）；40 个汉字的盘名截到 32 个。
- **macOS / Linux 没有跟进这套规则**：Linux 上显式 `--volid` 原样交给 xorriso（旧行为），macOS 上根本没有传给任何工具；缺省名都过 `burnSanitizeVolid`（只留 `A-Z0-9_`）；追加区段也不会沿用盘上名称。这两个平台的刻录路径没有真机跑过（AGENTS.md 未结项 #3）。
- **GUI 刻录页的「光盘名称」行**（`planBurnVolumeName`，ui/gui/burnname.go:30-57）：只有空白盘（或勾了「先擦空再烧」——擦完就是空白盘）才出输入框；其余状态直接一句话说明为什么不能改（「这张盘已有内容，名称保持不变」「这张盘已封盘，不能再写入」…），不摆一个灰掉的输入框。起刻只在可改名且输入非空时才传 `--volid`（`burnArguments`，ui/gui/service.go:1034-1049）。非 Windows 平台在输入框下方提示「macOS/Linux 下只保留字母、数字和下划线」。
- **改动含义**：不要再依赖 `ImportedVolumeName` 取中文名；`mountedVolumeLabel` 只在盘有盘符时可用，没有盘符的盘（比如封盘后没被挂载）只能靠引擎读回值，中文名此时多半放弃沿用。

### 6.7 写入速度与首区段填充（陷阱 #6）

- **首区段会被引擎填充到约 193MB**（DVD-ROM 兼容模式，同资源管理器母盘式）：31MB 的镜像实测刻了约 100 秒，下一可写位置在 93952 扇区。所以小负载的刻录时间与占用空间都明显大于数据本身，这是引擎行为，不是 bug。
- **单位有三套，换算别搞错**：引擎的写速度表单位是 **kB/s**（DVD-R 实测 `[5410 2705]` ≈ 4× / 2×）；`SetWriteSpeed` 的单位是**扇区/秒**（2048 字节一扇区）；用户给的是**倍速**。
- **倍速基准按 0.1 kB/s 记**：CD 的 1× 是 176.4 kB/s，16× 应为 2822 kB/s——若用整数基准（176）算出来是 2816，档位边界就错位了（internal/imapi2/imapi2.go:93-97、212-218）。
- **真机**：`--speed 4` 生效；第三张盘带 `--speed 8` 刻录正常（29 个文件 75 秒封盘弹出）。请求值超出介质档位时由 `PickWriteSpeed` 落档并记日志（见 4.1.7）。

### 6.8 封盘后的视图与「重新进盘复核」（陷阱 #7）

- **封盘后如果不弹出，Windows 仍挂着上一区段的视图**——卷标、文件列表都是旧的；强制独占访问卸载再挂载也不刷新。所以封盘内容必须**重新进盘后校验**。
- 这也是缺省弹出的一个实际好处：弹出再进盘，用户看到的就是最终视图。用了 `--no-eject` 又封盘时，资源管理器里看到的可能不是最终内容——这是系统行为，不要试图用代码去「刷新挂载」。
- 复核结果：第二张盘（2004 文件 / 1.41 GB 封盘）重新进盘后 2011 个文件逐个哈希一致。第一张新盘的最后一轮（两散文件 + 封盘）没单独复核（盘已换下），同一机制已由第二张盘验证。
- **原始扇区读免管理员可行**：Go 的 `os.Open` 能打开 `\\.\E:` 这样的设备路径（.NET 的 FileStream 拒绝设备路径），但也只读得到系统已经识别的区段范围。

### 6.9 刻录中途打断（第四张盘）

2026-11-24 实测（空白 DVD-R，完整版二进制）：在空白盘上刻一个 1.41 GB 的目录，第 80 秒强制结束进程（没走任何清理路径）。之后两分钟内连续查询 6 次，引擎都报 `DVD-R / blank`，盘上没有挂载任何卷。随后**不换盘**直接再刻一个目录（不给 `--volid`）→ 成功，盘名 = 目录名，文件哈希一致，状态 `appendable`。

**结论：中途打断（断电、强杀、拔线一类）不会让一次性盘变废——引擎视其为空白盘，不需要恢复逻辑。** 这张盘没有封盘，仍可追加。

这也是「刻录流程不响应第一次 Ctrl+C」可以接受的原因之一（见第 2 节）。

### 6.10 光驱里放着一张读不出来的盘

2026-11-24 实测：光驱里放了一张读不出来的盘，引擎报 `DVD-ROM / full`，从第 0 扇区起就是 CRC 数据错误，Windows 挂不上。此时：

- `zpack burn <目录>` 在写入前被拒。2026-11-24 之后的 `error_info` 是「光驱认不出这张光盘，无法写入；请检查：…」（这种盘现在归一成 `unrecognized`）；在此之前是「光盘是只读盘（DVD-ROM：压盘或已封盘），无法继续写入」。
- 旧文案对「盘放反 / 没卡牢 / 划伤 / 盘型不支持」这几种情况有误导性——用户看到的不是「盘坏了或放错了」，而是「盘已经封了」。2026-11-24 用户拍板新增 `unrecognized` 状态：`burn devices` 的 `status` 与 `note`、GUI 的括注与拦截原因、写入拒绝文案，四处都按「认不出」处理（见 3.1、4.3、6.4）。
- 辨别靠 `Media` 里的追加读取（6.4）：真的一个受支持的数据文件系统都读不出才报 `unrecognized`；压盘和已封盘的盘仍然报 `full`。软件层面只能分到「读得出 / 读不出」，分不出是放反、划伤还是盘型不支持——排查提示把这几种都列出来。
- 和 6.4 里那张 `damaged` 的盘对照：同样是「写不了」，引擎给出的形态可以完全不同（一张 `damaged`，一张 `DVD-ROM / full`）。所以**盘不可写时先查盘**（陷阱 #11），不要把一条拒绝文案当成对盘况的准确诊断。

### 6.11 临时文件、print 即 exit 与清理

- **print 即 exit，外层 defer 不运行**（conventions.md 第 6 节）。刻录的 macOS / Linux 路径上有两样要清理的东西（舞台目录、临时 ISO），全部靠**显式调用** `cleanup()`（cli/burn/burn.go:310）。
- **踩过的坑一：多输入的舞台目录泄漏**（2026-11-23 Windows 实测）。镜像生成失败时直接 `ErrorPrint`，进程当场退出，调用方没机会清理，舞台目录连同里面的硬链接一起留在用户目录里。修法：`burnStageISO` / `burnStageInputs` 失败时**返回 error**、自己清场，由最外层统一输出（cli/burn/burn.go:360-365、596-597）。回归测试 `TestBurnStageInputsCleansUpOnISOFailure` 三平台同跑（cli/burn/burn_stage_test.go:195-232）。
- **踩过的坑二：没有后端的平台上先暂存再拒绝**（同日）。目录 / 多输入会先生成镜像，报出冒号后面空白的「生成临时镜像失败: 」，还把舞台目录遗留在用户目录。修法：平台检查提到任何暂存动作之前（cli/burn/burn.go:247-251）。
- **踩过的坑三：引擎缺席时错误文案空白**。找不到 hdiutil / xorriso 时，调用方只拿到「生成临时镜像失败: 」。修法：生成镜像前先 `LookPath` 点名缺的是哪个引擎，并附安装引导（cli/burn/burn.go:608-615）。
- **Windows 路径上的清理**：`writer.Close()` 与数据流的 `cleanup()` 用 `defer`，因为它们在 `burnWindowsInner` 这个闭包里——闭包先返回、外层才输出 JSON，defer 来得及执行（cli/burn/burn_windows.go:114、164）。
- **小出入**：`burnRunCommand` 的注释说「不在此 ErrorPrint——会跳过调用方 defer 的临时镜像清理」，而调用方实际上是用显式 `cleanup()` 而不是 `defer`。结论不变（错误必须返回给调用方），只是注释里的「defer」与代码不一致（cli/burn/burn.go:685-686）。

### 6.12 真机验证记录（Windows）

每一步的校验方法：不弹出，直接读盘符逐文件比对 SHA-256；封盘后重新进盘再读。

| 盘 | 机器 | 刻了什么 | 结果 |
|---|---|---|---|
| 旧盘 | Win11 26200 + lenovo UltraslimDVD（USB） | 区段 1：ISO 镜像 31MB；区段 2：追加目录 15MB，导入 4 个旧文件 | 全部一致。最后一轮封盘没有单独复核 |
| 第一张新盘 | 同上 | 区段 A：目录 6.1MB；区段 B：两散文件 + 封盘 + 弹出 | 重新进盘后 5 个文件全部一致，卷标沿用 `ZPACK`，原始扇区可见两个区段各自的描述符（16 / 93968 扇区） |
| 内置光驱的盘 | Win10 22H2 + 内置 HL-DT-ST GHC0N，新盘 DVD-R RITEKF1 | ①空白盘刻中文目录「照片备份」（不给 `--volid`）；②追加两散文件 + `--volid 照片备份`；③不给 `--volid` 追加目录；④再追加两散文件 + `--volid` + `--close` | ①盘名 = 目录名、3 文件一致、64 秒；②改名生效、7 文件一致；③**盘名保持不变**、11 文件一致（含 52 字符中文长文件名、嵌套子目录、此前各区段的旧文件）；④37 秒完成、`close:true`，弹出后 `burn devices` 报 `no-media`、再刻报「刻录机里没有光盘」。同机另一张 `damaged` 的盘上，CLI 三条路径均在写入前拒绝 |
| 第二张盘 | 空白 DVD-R，完整版 `bin/zpack.exe` | ①`zpack burn 镜像.iso`（首区段）；②镜像与普通文件混排；③镜像区段之后追加目录（不给 `--volid`）；④大负载：2004 文件 / 1.41 GB（1.5 GB 随机大文件 + 两层目录下 2000 个小文件 + 3 个 60 字符文件名 + 一个空目录），`--volid` + `--close` | ①64 秒，3 文件哈希一致，卷标取镜像自带的 `ZPACKIMG`，Windows 以 CDFS 挂载；②被拒、不写盘；③镜像里的旧文件仍在、盘名沿用 `ZPACKIMG`、7 文件一致（Windows 改以 UDF 挂载）；④176 秒刻完封盘弹出（约 8 MB/s），重新进盘后状态 `full`（类型报 DVD-ROM）、盘名「大文件」、2011 个文件逐个哈希一致、空目录保留；封盘后再刻（含带 `--blank`）报「光盘是只读盘（DVD-ROM：压盘或已封盘）」 |
| 第三张盘 | 空白 DVD-R，完整版二进制，`--mode mastered --speed 8` | 两个目录 + 两个重名文件 + 一个带空格的文件名 + 一个空文件夹 | 29 文件 75 秒封盘弹出；重新进盘后状态 `full`、盘名取当前目录名 `zpack`、29 文件哈希一致；重名文件第二个落为 `重复-2.txt`（内容各归各）；带空格的文件名、空文件夹、三层深路径里的 45 字符文件名全部完整；封盘后再刻明确拒绝 |
| 第四张盘 | 空白 DVD-R，完整版二进制 | 刻录中途打断（见 6.9） | 打断后引擎报 `DVD-R / blank`；不换盘直接再刻成功 |

盘名边界另用完整版二进制实测过：显式 `--volid` 含路径非法字符 → 全部换成下划线（`a_b_c_d_e_f_g_h_i_j`）；40 个汉字的盘名截到 32 个；`--speed 4` 生效。

测试用的镜像由系统镜像库经 `IMAPI2FS.MsftFileSystemImage` 生成（Windows 自带，没有 mkisofs 时的替代做法）。

### 6.13 与 GUI 的耦合点

GUI 不直连引擎，全部经这两条命令的 stdout JSON 驱动（见 gui spec）。下面是会反过来约束本功能的耦合点：

- **`devices[]` 的字段是 GUI 的硬依赖**。GUI 里有一份镜像结构体 `BurnDevice`（ui/gui/service.go:976-983），只认 `index` / `path` / `vendor` / `product` / `media` / `status`——**没有 `note`**。所以 Windows 上「`unknown` + `note` 里的失败原因」在 GUI 里是看不到的。字段改名或改类型是破坏性变更。
- **status 词表三处同步**：`StatusName`（引擎）、`burnMediaRefusal`（CLI）、`burnBlockReason` / `burnMediaSuffix` / `planBurnVolumeName`（GUI）。GUI 的下拉显示名是「`index+1` · 厂商 型号（状态括注）」，并用显示名对应选中项（ui/gui/main.go:5478-5480、5542-5549）——状态文案一变，选中态也会跟着变。
- **GUI 起刻时拼的参数**（`burnArguments`，ui/gui/service.go:1034-1049）：`burn <输入...> --device N [--speed N] [--volid 名称] --mode multi|mastered [--blank]`。GUI **不传** `--close` 与 `--no-eject`——封盘用 `--mode mastered` 表达。速度档位固定为 自动 / 4 / 8 / 16 / 24 / 32 / 48。
- **GUI 对成功输出的消费很有限**：只看 `is_success`，摘要固定是「刻录完成」（`convertSummary` 读的是 `data.output`，刻录输出里没有这个字段，ui/gui/service.go:709-720）；失败取 `error_info`。刻录任务没有产物文件，任务卡不出「显示」钮（ui/gui/service.go:675-676）。
- **陷阱 #10：Service 层给页面的回调必须在后台协程里给**（2026-11-24）。`Service.BurnDevices` 的「未找到引擎」分支原本是同步回调，刻录页回调里的 `mygo.RunOnMain` 在无头测试环境里永远等不到主循环——`TestLeadSentenceStaysReadableAtMinWidth` 的刻录页目标没预设 `burnQueried`，必挂（10 分钟超时被杀）。`go test` 的程序在临时目录，`repoBin` 向上找不到仓库的 bin，无头测试里引擎恒为「未找到」。修法：回调一律异步（ui/gui/service.go:985-993）。同批修掉 `TestConvertSummary` 在 Windows 上的失败（测试把带反斜杠的路径直接拼进 JSON 串，整段信封解析失败）。**写涉及刻录页的 GUI 测试时，要预设 `burnQueried = true` 来跳过设备枚举**（ui/gui/burnstate_test.go:65、ui/gui/burnname_test.go:78）。
- **容量写法两份各自实现**：`burnFormatBytes`（cli/burn/burn.go:334-358）与 GUI 的 `humanSize` 是同一约定（整数位不带小数、否则一位小数、数字与单位之间不留空格），改一个要改另一个。
- **GUI 页面的几条用户定案**（细节在 gui spec，这里只记与本功能语义相关的）：「擦除」行只对可重写介质出现，DVD+RW 除外（它天生覆盖写免擦）；刻录速度用滑杆而不是自由输入（真实用途是降速换兼容）；写入模式的提示按平台区分；光盘名称行只有空白盘出输入框（见 6.6）。
- **GUI 判「可重写」与引擎的口径不完全一致**：GUI 靠 `media` 字段里是否含 `RW` 或 `RE` 来决定出不出「先擦空再烧」这一行（ui/gui/main.go:5804-5815），而 DVD-RAM 不含这两个子串——引擎那边 DVD-RAM 是可擦写类型（internal/imapi2/imapi2.go:105），CLI 的 `--blank` 也支持它。所以 DVD-RAM 在 GUI 里出不了擦除开关。这属于 GUI 的规则，记在这里是因为它读的是本功能的 `media` 字段。
- **刻录页是「引导行放不下才换行」的先例**（`Row.Wrap()` + 句子 `Basis(下限).Grow(1)`，ui/gui/main.go:5591-5618，AGENTS.md GUI 交互铁律）。新增可点元素必须在 `feedbackTargets` 登记（ui/gui/feedback_test.go:159-166）。

### 6.14 验收脚本如何防止真实刻录（陷阱 #8）

刻录是物理且不可逆的操作，门禁绝不能真的往盘上写。`verify/verify.sh` 的 burn 段（1096-1124 行）分两层：

- **只跑不碰设备的用例**（任何机器上都跑）：`burn devices`、输入不存在、`--mode live` 被拒、`--help`、镜像与普通文件混排被拒。
- **会走到写入的用例只在「没有刻录机」时跑**：脚本先取 `burn devices` 的输出，`data.devices` 等于 `[]` 才执行「两个散文件的多输入刻录」，并断言它 exit 1 且带回 `error_info`；检测到刻录机就打印一行「跳过 burn 多文件真实写入用例：检测到刻录机（刻录不可逆，门禁不写盘）」。没有刻录机的机器上用例数是 11，有刻录机的机器上是 9。

这条保护是 2026-11-24 Windows 接入 IMAPI2 之后补的——在那之前门禁会真的往盘上写。

单元测试同样不碰光驱：唯一会碰的 `TestLiveReadOnly` 要设 `ZPACK_IMAPI2_LIVE_TEST=1` 才跑，且全程不写入（internal/imapi2/imapi2_live_windows_test.go:15-17）。

### 6.15 改动时容易破坏的东西

- **`burnMediaRefusal` 的判定顺序**。认不出 → 封盘 → 只读类型 + 写保护 → 写保护 → 不支持 → 损坏 → 半截区段 → 须先擦除 → 写入器不支持，与 `StatusName` 同一顺序。「认不出」排在一切状态位之前：它的类型与状态位都不可信（2026-11-24 用户拍板）；「不支持」排在异常状态之前（同日对齐，理由见 4.3）。顺序一变，同一个状态位组合会报出不同的文案——`DAMAGED | UNSUPPORTED` 必须两边都是「不支持」（`TestBurnMediaRefusal` 里有这一例）。
- **`StatusName` 的优先级**：`full` 要压过其他位；「只读类型 + 写保护」必须判成 `full` 而不是 `write-protected`，否则封盘的一次写入盘会在 `burn devices` 里显示成写保护、在 GUI 里不被 `burnBlockReason` 以「已封盘」拦截。
- **追加区段不沿用卷标**：`burnWindowsSource` 里「没给 `--volid` 且盘非空白 → 卷标置空」这一句，以及 `applyVolumeName` / `chooseVolumeName`，任何一处改动都可能让盘名变回时间戳或乱码（6.6）。没有 Windows 卷标可读时不要硬写。
- **任何异常状态走到 `Write`**：新增写路径必须先过 `burnMediaRefusal`（6.5）。
- **临时文件清理**：在 macOS / Linux 路径上加任何新的失败出口，都要保证 `cleanup()` 被调用；不要在可能失败的地方直接 `ErrorPrint`（6.11）。
- **`burnUniqueEntryNames` 是三平台共用的**：改重名规则会同时改变 Windows 镜像里的条目名和 macOS / Linux 舞台里的名字。
- **平台检查必须在暂存之前**（cli/burn/burn.go:249）。
- **`invokeRaw` 的参数顺序**是自右向左；新增按值传参的调用要照这个来（internal/imapi2/call_windows.go:68-72）。
- **速度换算的两个单位**（kB/s ↔ 扇区/秒，倍速基准按 0.1 kB/s 记），见 6.7。
- **`devices` 首参数保留字**：新增任何以 `devices` 命名的输入路径都不可能被当路径处理。
- **输出键不要动**：`index` / `path` / `vendor` / `product` / `media` / `status` 是 GUI 的硬依赖；`devices` 必须是数组而不是 `null`。
- **验收脚本的防真实刻录判断依赖 `data.devices` 等于 `[]`**，改 `burn devices` 的输出形状（比如无刻录机时输出 `null`）会让门禁在有刻录机的机器上真的去刻盘。

## 7. 代码位置

命令面在 6d31586、40fb9e7 两次提交里按功能拆成了子包；下表是**拆分后**的路径。

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面（公共 + macOS / Linux） | `cli/burn/burn.go` | 命令定义与帮助、旗标解析、输入形态判定、macOS / Linux 后端、多输入舞台与临时 ISO、缺省卷标规则、平台与 live 拒绝 |
| 命令面（Windows） | `cli/burn/burn_windows.go` | Windows 刻录流程、写入前拒绝 `burnMediaRefusal`、卷标规范化 `burnWindowsVolumeName`、设备条目映射、心跳日志 |
| 命令面（占位） | `cli/burn/burn_other.go` | 非 Windows 平台上 Windows 分支的占位，仅为可编译 |
| 命令面（包文档） | `cli/burn/doc.go` | 包注释：平台矩阵、模式语义 |
| 装配 | `cli/root.go:65` | `burn.NewBurnCmd()` 挂到根命令 |
| 引擎（公共部分，三平台编译） | `internal/imapi2/imapi2.go` | 介质类型表、状态位与状态名、`Media`、速度落档 `PickWriteSpeed`、错误类型 `CallError` / `RecorderIndexError`、引擎读回卷标的还原 `sanitizeVolumeName` |
| 引擎（编排，仅 Windows） | `internal/imapi2/engine_windows.go` | COM 会话内的设备枚举、介质查询、镜像构建 `ImageBuilder`、写入、擦除、弹出、卷标取舍 |
| 引擎（调用小件，仅 Windows） | `internal/imapi2/call_windows.go` | COM 会话、按 CLSID 创建对象、按值的 `IDispatch::Invoke`、类型化属性读写、错误收敛 |
| GUI 规则函数 | `ui/gui/burnstate.go`、`ui/gui/burnname.go` | status 词表在 GUI 侧的消费者：括注、拦截原因、名称行规则 |
| GUI 调用点 | `ui/gui/service.go:973-1048`、`ui/gui/main.go:5475-5856` | `BurnDevices` / `StartBurn` / `burnArguments`；刻录页 `pageBurn` |
| 单测（命令面） | `cli/burn/burn_stage_test.go`、`burn_name_test.go`、`burn_windows_test.go`、`burn_name_windows_test.go` | 舞台生命周期、重名、容量写法、缺省卷标来源、Windows 卷标规范化、设备条目、写入前拒绝 |
| 单测（引擎） | `internal/imapi2/imapi2_test.go`、`imapi2_live_windows_test.go` | 状态名、可擦写判定、速度落档、错误码识别、卷标还原与取舍；后者是需显式开启的真机只读验证 |
| 单测（GUI 规则） | `ui/gui/burnstate_test.go`、`ui/gui/burnname_test.go` | 拦截原因、括注、名称行、参数拼装 |
| 验收脚本 | `verify/verify.sh:1096-1124` | burn 段（两小节，见第 8 节） |
| Agent 消费文档 | `cli/tools/skill.md:245-252` | `zpack skill` 输出里的「刻录光盘」一节 |
| 外部依赖 | `go.mod` | `github.com/go-ole/go-ole v1.3.0`、`golang.org/x/sys`——本功能在 Windows 上用到的全部外部包 |

## 8. 验收用例

### 8.1 `verify/verify.sh`（纯 Go 产物即可跑）

| 分节 | 用例名 | 断言 |
|---|---|---|
| burn 光盘刻录（1096-1104 行） | burn devices exit | exit 0 |
| | burn devices 信封 | `is_success` 为 `true` |
| | burn devices devices 字段 exit | `json get data.devices` exit 0（字段存在，无刻录机时是 `[]`） |
| | burn 输入不存在 exit | `burn /nonexistent-path.iso` exit 1 |
| | burn 输入不存在 error_info | 输出含「输入不存在」 |
| | burn live 未接入拒绝 exit | `burn /tmp --mode live` exit 1 |
| | burn --help exit | exit 0 |
| burn 多输入（1106-1124 行） | burn 镜像混排拒绝 exit | 镜像 + 散文件 exit 1 |
| | burn 镜像混排 error_info | 输出含「只能单独刻录」 |
| | burn 多文件 exit(无光驱=1) | 两个散文件 exit 1——**仅在没有刻录机的机器上跑** |
| | burn 多文件失败也带回 error_info exit | 失败输出里取得到 `error_info`——**仅在没有刻录机的机器上跑** |

有刻录机的机器上后两个用例被跳过并打印一行说明（原因见 6.14）。另外，`burn devices` 本身要真的去枚举刻录机——门禁在有刻录机的机器上会读一次光驱（只读，不写盘）。

**验收脚本没有覆盖的行为**（目前只有单测，或者完全没有自动化）：

- 单个散文件被拒（「不识别的镜像扩展名…」）。
- `--device` / `--speed` / `--mode` 的非法取值报错——`burnParseFlags` **没有任何自动化测试**（单测与验收都没有）。
- `zpack burn` 不带参数的用法提示；未知旗标静默忽略；旗标必须放在输入之后。
- `--blank`、`--close`、`--no-eject`、`--volid` 的端到端行为（都是不可逆或需要真机的）。
- `status` 的各个取值、卷标规则——只有单测。

### 8.2 单元测试

| 测试 | 守的是什么 |
|---|---|
| `TestBurnStageInputsMountsAndCleansUp` | 多输入聚台：重名加序号、目录按目录名为根、清理函数回收镜像与舞台且不伤源文件（镜像生成器注入假产物，三平台都跑） |
| `TestBurnStageInputsCleansUpOnISOFailure` | 镜像生成失败时舞台目录必须清干净（6.11 的回归） |
| `TestBurnStageInputsISO` | 聚台 → 临时 ISO → 用自家 `core.Extract` 解回交叉验证；Windows 上跳过，Linux 上没有 xorriso 也跳过 |
| `TestBurnUnsupportedPlatform` | 无后端平台的拒绝文案点名平台 |
| `TestBurnUniqueEntryNames` | 重名规则（表驱动） |
| `TestBurnFormatBytes` | 容量的人读写法（表驱动） |
| `TestBurnResolveVolumeName` | 缺省卷标的来源规则（表驱动） |
| `TestBurnWindowsVolumeName`（仅 Windows） | Windows 卷标规范化（表驱动） |
| `TestBurnDeviceEntry`（仅 Windows） | 引擎设备条目 → 输出契约条目 |
| `TestBurnMediaRefusal`（仅 Windows） | 写入前拒绝的 20 种组合（表驱动；含「损坏 | 不支持」两边同一结论、擦完仍异常时提示换盘） |
| `TestBurnImageOnUsedDiscWarning`（仅 Windows） | 镜像 + 非空白盘才警告；空白盘、目录、多输入不警告（表驱动） |
| `internal/stdout` 的 `TestWarnLogLine` | 警告行带 `[WARN]` 标签 |
| `internal/imapi2` 的 `TestMediaStatusName` / `TestMediaErasable` / `TestIsRomMediaType` / `TestPickWriteSpeed` / `TestIsSCode` / `TestSanitizeVolumeName` / `TestChooseVolumeName` 等 | 状态名、可擦写、只读类型、速度落档、错误码识别、卷标还原与取舍（三平台都跑） |
| `TestLiveReadOnly`（仅 Windows，需设 `ZPACK_IMAPI2_LIVE_TEST=1`） | 真机只读：枚举、读介质、设速度、从临时目录构建镜像；断言追加时沿用名 = Windows 卷标。全程不写盘 |
| GUI：`TestBurnBlockReason` / `TestBurnMediaSuffix` / `TestBurnBlockedByMedia` / `TestPlanBurnVolumeName` / `TestBurnArguments` / `TestBurnVolumeNameRow` / `TestBurnImageCaution` / `TestBurnImageCautionRow` | status 词表在 GUI 侧的消费规则；「镜像 + 已有内容的盘」的写入提醒行 |

### 8.3 手动验证

```bash
# 帮助可用
zpack burn --help; echo $?            # 0

# 没有刻录机的机器（不会碰任何设备）
zpack burn devices                    # {"is_success":true,"data":{"devices":[]}}
zpack burn 某个文件.txt                # 单个散文件被拒
zpack burn a.txt b.txt                # --device 0 超出范围（共 0 台，见 zpack burn devices）
zpack burn 目录/ --mode live          # 暂不支持 live：…
zpack burn 目录/ --speed 0            # --speed 需要正整数（如 8），收到: 0
zpack burn 目录/ --mode foo           # --mode 取值 multi / mastered / live，收到: foo

# Windows 真机只读回归（不写盘）
ZPACK_IMAPI2_LIVE_TEST=1 go test ./internal/imapi2 -run TestLiveReadOnly
```

真机写入验证**不可逆**，只用空白盘或可擦写盘。方法沿用 6.12：每步不弹出，直接读盘符逐文件比对 SHA-256（可以分别对源目录和盘上文件跑 `zpack hash <文件...>` 再比对哈希列）；封盘后必须重新进盘再读（6.8）。

## 9. 未结项与已知限制

### 没做的

- **`--blank` 擦除路径没有实跑过**（手头没有可擦写盘）。连带风险：封盘后的可擦写盘如果也被报成 ROM 类型，按类型表判的「可擦写」会失效——拿到 RW 盘后应改为问引擎 `IDiscFormat2Erase::IsCurrentMediaSupported`。
- **macOS / Linux 刻录路径没有实跑过**（只做了交叉编译与 vet）。这两个平台上缺省光盘名仍过 `burnSanitizeVolid`（中文名会变成 `ZPACK`），GUI 名称行的字符集提示也只做过交叉编译检查、没在真机上看过。
- **刻录百分比进度没有接入**（需要自建 COM 事件接收器；现在是每 5 秒一条心跳日志）。
- **live 优盘模式（UDF 包写入）没有接入**：`--mode live` 三个平台都明确拒绝；Windows 计划走 `IDiscFormat2RandomWrite`。
- **macOS 多刻录机寻址没有做**：`--device` > 0 直接拒绝（注释里记为「M2 交付」）。
- **Linux 没有容量预检、没有写入前拒绝**：这两样只有 Windows 后端有，macOS / Linux 全靠外部工具报错。
- **刻录流程不响应取消请求**（第一次 Ctrl+C 不中断，见第 2 节）；是否让刻录也响应取消，尚无定案。
- **验收缺口**：见 8.1 末尾的清单，尤其是 `burnParseFlags` 的校验文案完全没有自动化测试。

### 已知不对但暂时没改的

- **macOS 上 `--volid` 没有任何效果**：`hdiutil makehybrid` 与 `hdiutil burn` 都没有传卷标（cli/burn/burn.go:619、644-654）。是有意保留的旧行为还是遗漏【待确认】。
- **macOS / Linux 追加区段时不沿用盘上名称**：目录 / 多输入恒把第一步算出的名字（目录名、当前目录名或显式值）作 `-volid`（Linux），macOS 上干脆不定。与「已有内容的盘缺省沿用」的定案不符——这套规则目前只在 Windows 后端里实现。盘上的实际表现【待确认】（这两个平台没实跑过）。
- **macOS / Linux 的 `size_bytes` 不是写入量**：是非目录输入的文件大小之和；输入全是目录时是第一个输入 `os.Stat` 的大小（没有意义）。`image` 是已经删除的临时 ISO 路径。两个字段在这两个平台上的语义与 Windows 不同，也没有文档说明过是否有意如此。
- ~~**「光盘是只读盘（DVD-ROM：压盘或已封盘），无法继续写入」对读不出来的盘有误导性**~~ **已解决（2026-11-24 用户拍板）**：新增 `unrecognized` 状态，读不出的盘在 `burn devices`、GUI、写入拒绝文案里都按「光驱认不出这张光盘」处理并带排查提示（见 3.1、4.3、6.4、6.10）。遗留一点：软件层面分不出是放反、划伤还是盘型不支持，排查提示只能把这几种都列出来；「只读类型 + 写保护」形态的追加读取还没有实盘验证过。
- ~~**擦除之后仍然异常时的提示绕**~~ **已解决（2026-11-24）**：带 `--blank` 擦完重读仍异常时，拒绝文案里的提示不再叫人「加 --blank」（他已经加过了），一律是「请换一张空白盘」（`burnMediaRefusal` 的第三个参数 `erased`，4.3）。
- **Windows 上往有数据的盘写镜像时不导入上一区段**：镜像输入走的是整文件直烧（`burnWindowsSource`，cli/burn/burn_windows.go），不经过镜像库的 `ImportFileSystem`。后果与对策见 4.1.3：2026-11-24 用户拍板**警告后允许**（stderr 打一条 `[WARN]`，GUI 在主按钮上方出「写入提醒」行）。遗留：【待确认】镜像之后再追加镜像，旧区段的文件在新视图里到底还看不看得见——没有实测过；按「系统挂载最后一个区段的文件系统」推断是看不到，警告文案据此措辞并留了余地。（「镜像区段之后追加目录」是验证过的，旧文件仍在。）
- **macOS / Linux 上外部工具的 stderr 没有时间戳**，是原样透传的（cli/burn/burn.go:690）；`error_info` 里只有「… 刻录失败: exit status N」，看不到工具给的真正原因。
- **`burnLinux` 的参数 `stagedFromDir` 没有被使用**（cli/burn/burn.go:658）——是「目录原生刻录」旧设计的遗留。
- **`burnRunCommand` 的注释与代码不一致**：注释说「会跳过调用方 defer 的临时镜像清理」，而调用方用的是显式 `cleanup()`（cli/burn/burn.go:310、685-686）。
- **`burn devices` 在 macOS 上有盘时给不出任何状态**（`status` 缺省），Linux 认不出的状态原样透传。GUI 因此只对明确认识的状态下结论；CLI 在这两个平台上也没有写入前拒绝。
- **`MEDIA_TYPE_DISK`（0x0C）不在介质类型表里**，遇到时报 `未知介质(12)`（internal/imapi2/imapi2.go:100-119、122-127）。
- **空目录在 macOS / Linux 上是否保留没有验证**（Windows 上验证过保留）。
- **Windows 镜像里是否跳过 `desktop.ini`、`Thumbs.db` 这类系统垃圾没有验证**：compress 默认排除它们，burn 没有对应的逻辑，行为由引擎的 `AddTree` 决定【待确认】。
- **macOS / Linux 追加区段时不导入上一区段**：每次写的都是全新生成的 ISO（Linux 的 mkisofs 调用里没有 `-M`，macOS 没有任何合并选项，cli/burn/burn.go:619-626）。旧区段的文件在新视图里是否还可见【待确认】——没有实测过；按 ISO9660 多区段的一般行为推断是看不到的。这直接关系到「缺省多区段可追加」在这两个平台上的实际意义。

### 代码与文档不一致的地方（以代码为准）

| # | 文档怎么写 | 代码实际 | 出处 |
|---|---|---|---|
| 1 | 成功 JSON 是 `{"is_success":true,"image":"…","size_bytes":N,"mode":"multi","close":false,"blank":false}` | 载荷包在 `data` 里；载荷是 map，键按字母序输出：`blank`、`close`、`image`、`mode`、`size_bytes` | AGENTS.md:83；cli/burn/burn.go:67（帮助文本）；cli/burn/burn.go:274-280；internal/stdout/log.go:40 |
| 2 | `burn devices` 的 Windows 另报状态只列了 `damaged` / `erase-required` / `write-protected` / `unsupported` | 还有 `unknown`（介质查询失败，或状态位认不出）；`note` 字段也没在这一行里出现 | AGENTS.md:82 对比 internal/imapi2/imapi2.go:89、cli/burn/burn_windows.go:79-82 |
| 3 | 「`blank / appendable / full / no-media` 四平台同名」 | 是三个平台；而且 macOS 有盘时根本不报 status | AGENTS.md:247 对比 cli/burn/burn.go:125-140 |
| 4 | 光盘名称规则：「已有内容的盘缺省沿用、显式 `--volid` 才改」；「mac/Linux 显式 `--volid` 仍原样交外部工具」 | 沿用只在 Windows 后端实现；Linux 上目录 / 多输入追加时不沿用；macOS 上 `--volid` 根本没交给任何工具 | AGENTS.md:257、cli/tools/skill.md:250 对比 cli/burn/burn.go:598-632、644-654 |
| 5 | 「mac/Linux 走 hdiutil/xorriso 临时 ISO」，没说这两个平台上 `image` / `size_bytes` 是什么 | `image` = 已删除的临时 ISO 路径；`size_bytes` = 非目录输入的文件大小之和 | AGENTS.md:83 对比 cli/burn/burn.go:224-238、269-280 |
| 6 | 代码在 `cli/burn_windows.go`、`cli/burn.go` | 拆包后在 `cli/burn/burn_windows.go`、`cli/burn/burn.go` | AGENTS.md:237、242-244 |
| 7 | Linux「目录 `-map` 原生刻录」 | Linux 上目录也是先生成临时 ISO（`xorriso -as mkisofs`），再用 `-as cdrecord` 烧；代码里没有 `-map` | cli/burn/burn.go:8 对比 598-632、657-683 |
| 8 | 用法行是 `burn <镜像.iso\|目录>` | 还接受多输入 `<文件\|目录 ...>`；帮助的示例里也没有多输入的例子 | cli/burn/burn.go:54 对比 221、240 |
| 9 | ISO9660 卷标上限：一处记 30 字符，另一处记 32 字符 | 代码按 32 截断 | AGENTS.md:250 对比 AGENTS.md:257、cli/burn/burn_windows.go:21 |
| 10 | `burn devices` 的输出示例里没有说明 macOS 的形态 | macOS 的 `path` 恒为空串、`note` 恒为「macOS 经 hdiutil 使用默认刻录设备」 | AGENTS.md:82 对比 cli/burn/burn.go:118-123 |

另有两条不属于本功能、但找刻录代码时会撞上的：AGENTS.md:340 的目录结构里 `cli/` 仍写着「root.go（装配）+ commands.go」，拆包后已不存在 `commands.go`；AGENTS.md 里刻录一节的未结项清单（265-275 行）仍按「二轮更新」时的状态写，本节是更新的版本。

### 【待确认】汇总

| # | 事项 | 为什么拿不准 |
|---|---|---|
| 1 | macOS `drutil list` 的列布局，与「丢掉最后两列」的解析是否吻合 | 没有 macOS 机器；这条路径没有真机验证记录 |
| 2 | macOS 上 `--volid` 没有效果，是有意还是遗漏 | 代码没有任何注释说明；AGENTS.md 只说「原样交外部工具」 |
| 3 | macOS / Linux 追加区段时不沿用盘上名称，是否符合预期 | 与定案文字不符，但没实跑过、也没有说明 |
| 4 | Windows 上往有数据的盘追加镜像后，旧文件是否仍可见 | 没有实测；代码路径明确不导入上一区段。2026-11-24 起警告后允许（4.1.3），警告文案按「看不到」措辞并留了余地——实测结果若不是这样，要改文案 |
| 5 | Windows 上目录内部的符号链接 / 联接点如何进盘；是否跳过系统垃圾文件 | 由引擎的 `AddTree` 决定，没有验证 |
| 6 | ISO9660 层的卷标上限是 30 还是 32 个字符 | AGENTS.md 两处记录互相矛盾 |
| 7 | 「光驱里放着读不出来的盘」的具体物理原因 | 只有现象记录（`DVD-ROM / full`、从第 0 扇区起 CRC 错误），没法判断是放反、划伤还是盘型不支持 |
| 8 | 空目录在 macOS / Linux 上是否保留 | 只在 Windows 上验证过 |
| 9 | ~~`StatusName` 与 `burnMediaRefusal` 的判定顺序不完全一致（`DAMAGED` 与 `UNSUPPORTED` 同时置位时，一边报 `unsupported`、一边报「状态异常」）是有意还是巧合~~ **已解决（2026-11-24）**：对齐成同一顺序，「不支持」在前，理由见 4.3 | — |
| 10 | macOS / Linux 追加区段后，旧区段的文件在新视图里是否还可见 | 没有实测；代码不导入上一区段，只能按 ISO9660 的一般行为推断 |
| 11 | 光驱里放着读不出来的盘时，`burn devices` 的 `path` 是盘符还是引擎标识 | 直接报「介质不兼容」的形态：2026-11-24 实测是盘符（`E:\`），Windows 当时也没有挂载它。被报成 `DVD-ROM / full` 的那种形态没有留下输出，按代码推断也是盘符（盘符跟刻录机走，不跟盘走） |

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 新增 `unrecognized` 状态：光驱认不出的盘（读不出身份，或被报成只读盘但读不出任何文件系统）在 `burn devices`、GUI、写入拒绝文案里都按「认不出」处理并带排查提示（3.1、3.3、4.1.1、4.3、5、6.4、6.5、6.10、9） | （提交后回填） |
| 2026-11-24 | 往有数据的盘写镜像：从「不声不响地写」改为**警告后允许**——stderr 打一条 `[WARN]`（conventions.md 第 4 节首次启用警告级别），GUI 在主按钮上方出「写入提醒」行；只在读得到介质状态的 Windows 上有（3.4、4.1.2、4.1.3、5、8.2、9） | （提交后回填） |
| 2026-11-24 | 写入前拒绝与 `burn devices` 对齐判定顺序：「不支持」排到「损坏 / 须先擦除 / 半截区段」之前，`DAMAGED \| UNSUPPORTED` 两边都是「不支持」；带 `--blank` 擦完仍异常时提示改为「请换一张空白盘」（4.3、6.15、8.2、9） | （提交后回填） |
