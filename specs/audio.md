# 音频（audio）

> `zpack audio` 是音频的格式转换、压缩与元信息查询工具：和视频共用内置 libav 引擎，但命令面独立。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

**做什么**：对一条音频做换格式或降码率，一共 3 个子命令（分派在 cli/media/audio.go:39-46）：

| 子命令 | 一句话 |
|---|---|
| `convert` | 换格式，可以同时指定码率 / 采样率 / 声道；输入也可以是视频（抽出音轨） |
| `compress` | 压到更低的码率：缺省是原始码率的一半（下限 32k），也可以用 `--bitrate` 精确指定 |
| `info` | 探测事实（容器、时长、体积、音频流）加压缩建议 |

- **输入**：任何 libav 能打开的文件，按内容识别、不看扩展名——纯音频（mp3 / m4a / aac / flac / ogg / opus / wav …）或视频容器（只取音频，视频的去留看输出扩展名）。
- **输出**：音频容器 mp3 / m4a / aac / flac / ogg / opus / wav（media/decide.go:76-79）。输出成视频容器（mp4 / m4v / mov / mkv / webm）也行，见 4.1.1。
- **和视频的关系**：音频与视频共用一个引擎包 `media`、一个命令面包 `cli/media`（共用结果载荷与日志适配），但对用户是两个独立命令（cli/media/doc.go:4-5）。本 spec 只管 `audio`；`media` 的命令见 [media.md](media.md)。

**不做什么**：

- 不做剪辑、变速、淡入淡出、降噪、响度归一化这类内容处理。
- 不按「目标体积」压缩——那是 `media compress --target-size`（纯音频输入也能用）。`audio compress` 只有码率一个旋钮，没有两遍编码，也没有质量（VBR / Q）档位。
- 码率对无损格式（flac、wav）不生效，也不会报错（见 4.1.2）。
- 不携带标签与封面：引擎不拷贝任何元数据（media/engine_cgo.go 里只读了一个 `rotate` 标签，用于视频旋转探测）；输出到音频容器时封面所在的视频流被丢弃。
- 没有批处理；纯音频转码没有进度输出。
- 纯 Go 构建不可用，必须完整版（见第 5 节）。

**使用场景**：把视频里的音轨抽成 mp3；把 wav / flac 转成 m4a / opus 方便手机播放；把高码率 mp3 压到一半好发邮件；录音降采样率、转单声道；先 `audio info` 看清源码率再决定压到多少。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack audio convert <输入> <输出> [--bitrate 128k] [--sample-rate 44100] [--mono] [--quiet]` | 换格式，输出扩展名决定目标格式 |
| `zpack audio compress <输入> [--bitrate 64k] [--sample-rate N] [--mono] [--output 路径] [--quiet]` | 降码率，格式不变 |
| `zpack audio info <输入>` | 元信息与压缩建议 |

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--bitrate <值>`（别名 `-b`） | 目标码率 | 1–3200 的整数，单位是 kbps（×1000 bit/s）；小写 `k` 可省 | `convert`：不指定就跟随源或取 128k（见 4.1.1）；`compress`：原始码率的 50%，下限 32k |
| `--sample-rate <N>` | 目标采样率 | 8000–192000 的十进制整数，严格解析（`cmdutil.ParseIntStrict`） | 跟随源 |
| `--mono` | 单声道 | 出现即生效 | 不指定时的声道见 4.1.3 |
| `--output <路径>`（别名 `-o`） | 输出路径 | 值被当作第 2 个位置参数 | `compress`：输入路径（就地覆盖）；`convert` 上它就是输出路径的另一种写法 |
| `--quiet` | 关掉引擎的过程日志 | 帮助与用法文案里都没提，但能用 | — |

取值细则（cli/media/audio.go:60-110、211-218）：

- **`--bitrate`**：先去首尾空白、去掉一个小写 `k` 后缀，再转小写，然后按整数解析。合法范围 1–3200，结果乘 1000。
  - `64k` → 64000，`128` → 128000（不带 `k` 也按 kbps 算）。
  - 报 `--bitrate 需如 64k / 128k / 320k，收到: X` 的情形：`64000`（超出 3200）、`3201k`、`0`、`-5k`、`0.5k`、`abc`。
  - **大写 `K` 也会报错**（`128K` → `--bitrate 需如 64k / 128k / 320k，收到: 128K`）：去后缀发生在转小写之前。这是疏漏，见 9。
- **`--sample-rate`**：`44100abc`、空串这类不是完整整数的值报 `--sample-rate 需数字 Hz（如 44100），收到: X`；整数但不在 8000–192000 之间报 `--sample-rate 需 8000-192000 Hz，收到: N`。命令面的范围校验只保证是个数，编码器认不认是另一回事（见 4.1.3）。
- **旗标缺值**：`--bitrate`、`--sample-rate`、`--output` 出现在最后、后面没有值时**静默忽略**，不报错（cli/media/audio.go:67、72、83 三个 `if` 都没有 else 分支）。
- **未知旗标静默忽略**；以 `-` 开头的参数一律当旗标。和 image 一样，未知旗标的值如果不以 `-` 开头会落成位置参数。
- **`--help` / `-h` 出现在任意位置**都打印 `audio` 这一级的帮助并 exit 0。没有子命令级帮助。

位置参数：

| 子命令 | 个数 | 说明 |
|---|---|---|
| `convert` | 恰好 2 个：输入、输出 | 多一个就报用法 |
| `compress` | 至少 1 个：输入 | 第 2 个可选，是输出路径；第 3 个及以后被静默忽略 |
| `info` | 恰好 1 个 | |

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

## 3. 输出契约

成功时的 stdout JSON 都是信封 `{"is_success":true,"data":…}`，下面写的是 `data` 里的内容，字段按出现顺序列出。

### 3.1 `audio convert` 与 `audio compress`（同一个形状）

结构体 `audioResultJSON`（cli/media/audio.go:197-209）：

```json
{
  "mode": "audio-only-output",
  "input": "/abs/in.m4a",
  "output": "/abs/out.mp3",
  "audio": {"action": "encode", "codec": "mp3", "bitrate": 128000},
  "size_bytes": 803840,
  "original_size_bytes": 1605632,
  "ratio": 0.5,
  "original_bitrate": 256000,
  "bitrate": 128000,
  "sample_rate": 44100,
  "mono": true
}
```

| 字段 | 类型 | 含义 | 何时出现 |
|---|---|---|---|
| `mode` | string | 引擎走的路径，见下表 | 恒有 |
| `input` `output` | string | 输入、输出的绝对路径 | 恒有 |
| `audio` | object | 音频流的处理结果，见下表 | 恒有 |
| `size_bytes` | int64 | 输出文件字节数 | 恒有 |
| `original_size_bytes` | int64 | 输入文件字节数。注意这是**转码结束后**才去量的（cli/media/media.go:877），就地覆盖时量到的已经是新文件 | 恒有 |
| `ratio` | float | 输出 ÷ 输入 | 恒有（输出为 0 字节时为 0） |
| `original_bitrate` | int64 | 源音频码率，bit/s；探测不到时为 0（0 不出现） | 只在 `compress` |
| `bitrate` | int64 | `convert`：用户请求的码率；`compress`：实际采用的目标码率。**不是实测码率** | 值不为 0 时 |
| `sample_rate` | int | 用户请求的采样率 | 给了 `--sample-rate` |
| `mono` | true | 请求了单声道 | 给了 `--mono` |

后四个字段带 `omitzero`：不给对应旗标时键不出现。

`mode` 的取值（media/media.go:93-107、media/decide.go:111-133）：

| `mode` | 出现条件 | 输入里的视频 |
|---|---|---|
| `audio-only-output` | 输出是音频容器（mp3 / m4a / aac / flac / ogg / opus / wav） | 丢弃 |
| `audio-only` | 输出是视频容器、输入有视频、视频与容器兼容，并且（给了质量旗标，或音频与容器不兼容） | 拷贝保留 |
| `remux` | 输出是视频容器，没给质量旗标，音视频都与容器兼容——这时就是原样转封装，音频没动 | 保留 |
| `full` | 输出是视频容器但视频编码不兼容，或输出扩展名不被识别——视频也会被重编 | 重编码 |

纯音频输入 + 视频容器输出 + 质量旗标：音频编码与容器兼容时报 `audio-only-output`，不兼容时报 `audio-only`。

`audio` 对象（`mediaStreamJSON`，cli/media/media.go:854-858）：

| 字段 | 类型 | 含义 | 何时出现 |
|---|---|---|---|
| `action` | string | `copy`（同编码直拷，没有重编码）/ `encode` / `none`（输入没有音频流） | 恒有 |
| `codec` | string | `copy` 时是源编码名；`encode` 时是目标编码名（`mp3` / `aac` / `flac` / `opus` / `pcm_s16le`） | 没有音频流时不出现 |
| `bitrate` | int64 | 实际采用的码率：`copy` = 源音频流的码率（探测不到时为 0）；`encode` = 计划码率 | 恒有 |

要判断「有没有真的重编码」看 `audio.action`；要知道「用了多少码率」看 `audio.bitrate`。顶上的 `bitrate` 只是请求值。

### 3.2 `audio info`

直接复用 `media info` 的载荷 `mediaInfoJSON`（cli/media/media.go:914-926）。纯音频文件的例子：

```json
{
  "input": "/abs/in.m4a",
  "format": "mov",
  "duration_seconds": 5.0,
  "size_bytes": 1605632,
  "rotation": 0,
  "display_width": 0,
  "display_height": 0,
  "video": null,
  "audio": {"codec": "aac", "bitrate": 256000, "sample_rate": 44100, "channels": 1},
  "suggestions": [
    {"kind": "audio", "order": 2, "note": "音频 256 kbps 高于 128k 常用档，压缩将降至 128k", "estimated_ratio": 0.5}
  ],
  "bakeable": []
}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `input` | string | 绝对路径 |
| `format` | string | libav 解复用器的名字；名字里逗号串联多个后缀时只取第一个（media/info.go:80-83）。所以 `.m4a` 报的是 `mov`，不是 `m4a` |
| `duration_seconds` | float | 时长，秒 |
| `size_bytes` | int64 | 文件字节数 |
| `rotation` `display_width` `display_height` | int | 视频相关，纯音频恒为 0 |
| `video` | object / null | 纯音频为 `null`；输入是视频时与 `media info` 相同 |
| `audio` | object / null | `codec`、`bitrate`、`sample_rate`、`channels`，没有音频流时为 `null`（media/info.go:41-55） |
| `suggestions` | array / null | 压缩建议（media/info.go:115-188）。纯音频只有一条规则：音频码率高于 128k 时给出一条 `kind:"audio"`、`order:2` 的建议，带 `estimated_ratio`；没有建议时是 `null` |
| `bakeable` | array | 可烤的显示元数据，纯音频恒为 `[]` |

注意 `suggestions` 里那句「压缩将降至 128k」描述的是 `media compress` 的 128k 阶梯，不是 `audio compress` 的缺省（原始的 50%）——只有源码率正好 256k 时两者才一致。

### 3.3 失败时的 `error_info`

一律是 `{"is_success":false,"error_info":"…"}` + exit 1。文案就是引擎 `error.Error()` 的原文。

| `error_info` | 触发条件 |
|---|---|
| `用法: audio convert <输入> <输出.mp3\|m4a\|flac\|wav\|ogg\|opus> [--bitrate 128k] [--sample-rate 44100] [--mono]` | `convert` 的位置参数不是 2 个 |
| `用法: audio compress <输入> [--bitrate 64k] [--mono] [--output 路径]` + 换行 + `缺省压到原始 50% 码率；--mono 体积再减半` | `compress` 没有位置参数 |
| `用法: audio info <音频文件>` | `info` 的位置参数不是 1 个 |
| `用法: audio convert …` + 换行 + `或: audio compress …` + 换行 + `或: audio info <输入>` | 子命令缺失或未识别 |
| `路径不存在：<绝对路径>` | 输入文件不存在 |
| `--bitrate 需如 64k / 128k / 320k，收到: X` | 码率不是 1–3200 的整数（含大写 `K`） |
| `--sample-rate 需数字 Hz（如 44100），收到: X` | 采样率不是完整的十进制整数 |
| `--sample-rate 需 8000-192000 Hz，收到: N` | 采样率越界 |
| `目标码率 N 不低于原始 M——无需压缩（要转格式用 audio convert）` | `compress` 的目标码率 ≥ 源码率（源码率探测得到、大于 0 时才判） |
| `无法读取输入: <原因>` | 探测失败。原因是 `打开输入失败: …` 或 `读流信息失败: …`；纯 Go 构建是 `无法读取输入: 媒体功能需要完整版（build_cli 产线 CGO 构建）；纯 Go 版不含 libav 引擎` |
| `打开输入失败: …`、`读流信息失败: …`、`input context 分配失败` | 执行阶段再次打开输入失败（不带「无法读取输入」前缀） |
| `输出格式创建失败（检查扩展名）` | libav 不认识输出扩展名（media/engine_cgo.go:301-304） |
| `找不到解码器: <名>`、`找不到编码器: <名>` | 完整版里没有对应的编解码器 |
| `解码参数注入失败: …`、`打开解码器失败: …`、`打开编码器失败: …`、`编码参数导出失败: …` | 编解码器初始化失败，例如编码器不认给它的采样率或声道布局 |
| `输出流创建失败`、`拷贝流参数失败: …`、`滤镜不存在` | 极少见的资源类失败 |
| `打开输出失败: …`、`写头失败: …`、`写尾失败: …` | 输出文件打不开，或容器拒绝要写入的流 |
| `无可输出流` | 所有流都被丢弃——例如没有音频轨的视频输出成 mp3 |
| `音频帧缓冲失败: …`、`fifo 写入失败: …`、`fifo 读出失败: …` | 音频缓冲异常 |
| libav 原话（滤镜图解析或配置失败等，未包装） | 其它引擎内部失败 |

### 3.4 stderr

- 统一开始日志 `zpack audio 开始执行`（cli/root.go:52）。
- `compress` 在动手前一行：`音频压缩: 原始 256000 bps → 目标 128000 bps（单声道）`（cli/media/audio.go:176-178）。
- 引擎的过程日志（media/media.go:109、engine_cgo.go:315、413-449 等）：
  - `决策: audio-only-output（音频:aac → mp3）`
  - `跳过字幕流 #N（<编码> 不兼容容器 <扩展名>）`——只有输出成视频容器且带字幕时才会有
  - 中途出错的日志：`读包失败: …`、`写包失败: …`、`送包失败: …`、`处理失败: …`、`滤镜编码失败: …`（见 4.3，这些不等于命令失败）
- **没有进度输出**：转码进度只按视频帧计数（media/engine_cgo.go:810-817），纯音频一行进度都没有。
- `--quiet` 关掉除开始日志外的以上全部。

## 4. 行为细则

### 4.1 正常路径

三个子命令的共同骨架：解析 → 校验 → 确认输入存在 → 调引擎 → 输出 JSON。`convert` 与 `compress` 调的是同一个引擎入口 `ConvertWithOptions`（media/media.go:81-116）：探测 → 按输出扩展名决策 → 叠加质量旗标 → 执行。

#### 4.1.1 `audio convert`

**输出扩展名决定目标格式**（`audioOnlyTargets`，media/decide.go:76-79）：

| 扩展名 | 目标编码 | 有损 | `--bitrate` 是否生效 | 说明 |
|---|---|---|---|---|
| `.mp3` | mp3（libmp3lame） | 有损 | ✓ | |
| `.m4a` / `.aac` | aac | 有损 | ✓ | `.m4a` 是 MP4 容器；`.aac` 是裸 ADTS 流 |
| `.ogg` / `.opus` | opus（libopus） | 有损 | ✓ | 采样率只能是 48k 系，见 4.1.3 |
| `.flac` | flac | 无损 | ✗ | 给了也不报错，字段照样报请求值 |
| `.wav` | pcm_s16le | 无损 | ✗ | 16 位小端 PCM，同上 |
| `.mp4` / `.m4v` / `.mov` / `.mkv` / `.webm` | 见 3.1 的 `mode` 表 | — | — | 这是视频容器：视频的去留看 `mode`，不是音频格式 |

帮助里只列了 mp3 / m4a / flac / wav / ogg / opus，`.aac` 没写但支持。

**输出是音频容器时**（media/engine_cgo.go:187-198）：

- 视频流、字幕流全部丢弃，只留音频。
- 每条音频流分别判断：源编码就是目标编码，**而且**没给任何质量旗标 → 直拷（`copy`，不重编码，秒级、无损）；否则重编码（`encode`）。
- 重编码的码率：给了 `--bitrate` 就用它；没给就是 **128k**（media/engine_cgo.go:245-250）。所以 `audio convert 高码率.mp3 新.mp3 --mono` 会把码率降到 128k，而不只是变单声道。
- 输入有多条音频轨时，引擎是逐流做计划的——每条都会被转出，不是只取第一条；目标容器能不能装下多条流取决于具体封装器【待确认】未实测。

**输出是视频容器时**：

- 没给质量旗标：走通用决策，通常就是原样转封装。
- 给了任一质量旗标：视频拷贝不动，只重编音频（`mode:"audio-only"`）。「质量旗标干预时不丢视频流」是 `forceAudioEncode` 特意做的（media/media.go:62-73）。这时音频码率：给了 `--bitrate` 用它；没给取「源码率与 128k 里较小的那个」（media/engine_cgo.go:205-209）。
- 例外：视频编码与目标容器不兼容时落到 `full`，此时若音频与容器兼容，音频会被**直拷、质量旗标被忽略**（media/engine_cgo.go:233-242）。

**输出扩展名区分大小写**：决策层用原样的小写匹配，`.M4A`、`.MP3` 不会被认成音频容器，会落到 `full` 那一行——结果不可预期（libav 本身认大写扩展名，会照样建出容器，但流计划是按「未知容器」做的）。输出扩展名请用小写。

失败时命令面会删掉输出路径上的文件，见 4.3。

#### 4.1.2 `audio compress`

工具做的事（cli/media/audio.go:139-194）：

1. 输出路径 = 第 2 个位置参数（`--output` 的值也算）；没给就是输入路径，**就地覆盖**。
2. 先探测一遍输入，取第一条音频流的码率作为「原始码率」。
3. 校验旗标，定目标码率：

| 情况 | 目标码率 |
|---|---|
| 给了 `--bitrate` | 就是它 |
| 没给，原始码率的 50% ≥ 32000 | 原始 ÷ 2 |
| 没给，原始码率的 50% < 32000（即原始低于 64k） | 32000 |
| 原始码率探测不到（0） | 32000 |

4. 原始码率大于 0、且目标码率 ≥ 原始码率 → **拒绝**：`目标码率 N 不低于原始 M——无需压缩（要转格式用 audio convert）`。
5. 打一行日志，调引擎。目标码率恒大于 0，所以同格式也一定走重编码（`copy` 分支被码率旗标否决，media/engine_cgo.go:190-192）。

例：256k → 128k，320k → 160k，64k → 32k，40k → 32k；32k 的源不给 `--bitrate` 会被拒（目标 32000 不低于原始 32000）。

要点：

- **只给 `--mono` 或 `--sample-rate`、不给 `--bitrate` 时，目标码率仍按上面的缺省规则算**。源码率低于 64k 时目标会被抬到 32k，源码率不高于 32k 时会被拒——哪怕只是想转单声道。
- 源码率探测不到、或输入根本没有音频流时不拒绝：照常调引擎，JSON 里 `bitrate` 报 32000。没有音频流的输入实际只是原样转封装（`action:"none"`）。
- 码率对 flac / wav 不生效，输出体积基本不变，但照样算成功。
- 输出扩展名可以和源不同（`--output x.mp3`），这时其实是在换格式——拒绝文案里的「要转格式用 audio convert」只是建议，不是限制。
- 输入是视频、`--output` 也是视频容器：视频拷贝保留，只有音频被重编。

**就地覆盖是缺省路径，但它有两个问题**：

- 报告失真：`original_size_bytes` 和 `ratio` 是转码之后才去量输入文件量的（cli/media/media.go:877），就地时量到的已经是新文件，于是 `ratio` 是 1。
- 正确性【待确认】：引擎先以读方式打开输入，再以写方式打开同一个路径（media/engine_cgo.go:292、383），后者会截断文件。此后输入读到的可能已是残缺或被新数据覆盖的内容，结果是否完整没有实测；验收脚本的每一条 compress 用例都带 `--output`，从不走就地路径。在确认之前请一律带 `--output`。 **2026-11-24 已实测确认并修复（提交 417e3a8）**：实测 1.2MB 的 mp3 就地覆盖后只剩 33KB 且报 success；现在引擎先写旁边的临时文件、成功后原子替换，30 秒完整。

#### 4.1.3 质量旗标在引擎里怎么落地

编码器参数的设置在 media/engine_cgo.go:538-575，滤镜在 :632-641。

- **码率**：`encCtx.SetBitRate(计划码率)`，是平均码率。无损编码器（flac、pcm）会忽略它。
- **采样率**：先取 `--sample-rate` 的值，没给就取源采样率。接着做一步「对齐」：编码器的支持表里没有**源采样率**时，改取支持表的第一项——libopus 只接受 48k / 24k / 16k / 12k / 8k，所以 44.1k 的源转 opus 会被提到 48k。最后由 `aformat` 滤镜自动重采样。
  - 对齐检查看的是源采样率，不是用户指定的那个。于是「44.1k 的源 + `--opus` 输出 + `--sample-rate 24000`」会先设成 24000、再被对齐改回 48000【待确认】未实测。
  - 用户指定了编码器不认的采样率（比如 aac 遇到 44101）会在打开编码器时失败：`打开编码器失败: …`。命令面的 8000–192000 只保证是个数。
- **声道**：`--mono` → 单声道；否则取**编码器支持表的第一项**；编码器没有支持表才沿用源布局。`aformat` 负责混音。
  - 疑似缺陷：libmp3lame 的声道支持表是 `{ mono, stereo }`，第一项是 mono（FFmpeg n9.0 `libavcodec/libmp3lame.c` 的 `CODEC_CH_LAYOUTS(AV_CHANNEL_LAYOUT_MONO, AV_CHANNEL_LAYOUT_STEREO)`）。按代码，不带 `--mono` 把立体声转成 mp3 也会得到**单声道**。aac 是否同样取决于它的支持表顺序，没能核实。两条都是读代码推出来的，未实测，见 9。
  - 按 FFmpeg n7.1 的源码，libopus、flac、pcm 没有声明声道支持表，于是沿用源布局；源是多声道（5.1 之类）而编码器只认单声道 / 立体声时，打开编码器会失败。
- **定长帧**：mp3、aac 的编码器要求每帧采样数固定（1152 / 1024），滤镜图吐出的帧长度不定，所以中间有一层 `AudioFifo` 缓冲、按帧长切片再编码（media/engine_cgo.go:582-589、737-745、759-788）。

#### 4.1.4 `audio info`

直接调 `mediaInfo`（cli/media/audio.go:242-243），探测后输出 3.2 的报告，不解码。输入是视频时输出与 `media info` 完全相同。

### 4.2 边界与退化输入

| 输入 | 结果 |
|---|---|
| `zpack audio`（没有子命令）或子命令拼错 | exit 1，三行用法 |
| `convert` 位置参数不是 2 个 | exit 1，用法 |
| `compress` 没有位置参数 | exit 1，用法（两行） |
| `compress` 给了 3 个以上位置参数 | 第 3 个起被忽略 |
| 输入不存在 | exit 1，`路径不存在：…` |
| 未知旗标 | 静默忽略；它的值可能变成位置参数 |
| `--bitrate` / `--sample-rate` / `--output` 在末尾、后面没有值 | 静默忽略，按没给处理 |
| `--bitrate 128K`（大写 K） | exit 1，`--bitrate 需如 64k / 128k / 320k，收到: 128K` |
| `--bitrate 64000`、`3201k`、`0`、`-5k`、`0.5k`、`abc` | exit 1，同上文案 |
| `--sample-rate 44100abc` | exit 1，`--sample-rate 需数字 Hz（如 44100），收到: 44100abc` |
| `--sample-rate 7999` / `192001` | exit 1，`--sample-rate 需 8000-192000 Hz，收到: N` |
| 输入不是媒体文件、或已损坏到打不开 | exit 1，`无法读取输入: 打开输入失败: …` |
| 输入没有音频轨，输出成音频容器 | exit 1，`无可输出流` |
| 输入没有音频轨，走 `compress` | 不报错，原样转封装，`action:"none"`、`bitrate:32000` |
| 输出扩展名 libav 不认识 | exit 1，`输出格式创建失败（检查扩展名）` |
| 输出扩展名是大写（`.MP3`） | 不会被当成音频容器，结果不可预期 |
| 输出文件已存在 | 直接覆盖 |
| 纯音频源码率探测不到 | `compress` 不拒绝，目标 32k |
| 纯 Go 构建 | 三个子命令都 exit 1，`无法读取输入: 媒体功能需要完整版（…）` |

### 4.3 错误处理

- 所有失败都是 `ErrorPrint`：exit 1 + `error_info`。
- **失败时会删输出路径上的文件**。`convert` 与 `compress` 在引擎返回错误后都调 `os.Remove(输出路径)`（cli/media/audio.go:125-128、182-185）。和 image 一样，这会把输出路径上原有的文件一起删掉：
  - 输出路径上本来有文件、而错误发生在写盘之前（输入打不开、输出扩展名不认识、编解码器初始化失败）→ **原有文件被删**。例：`audio convert 坏文件.mp3 重要.wav`。
  - 纯 Go 构建下 `convert` 必然失败，同样会删掉输出路径上的文件。
  - `compress` 就地覆盖时输出路径就是输入路径：探测之后的任何失败都等于**删源文件**。例：`audio compress 录音.mp3` 遇到编码器打不开。（2026-11-24 已修复：清理半成品不再删与输入相同的输出，见 media.md 6.3「就地改写毁源文件」）
  
  这几条是读代码得出的，受约束没有实跑。
- **引擎主循环里的错误不会让命令失败**（media/engine_cgo.go:413-449、683-706、722-757）。读包、送包、收帧、写包、滤镜编码出错时只是打一行日志（`读包失败: …` 等）就结束循环，随后照常冲洗、写尾，`executeFile` 按成功返回。所以输入中途损坏或磁盘写满时，可能得到一个截断的产物，而 JSON 是 `is_success:true`、退出码 0。带了 `--quiet` 连那行日志都看不到。
- 只有准备阶段的错误（打开输入、建输出、初始化编解码器、写头）和冲洗阶段的错误会作为失败返回。
- Ctrl+C：音频引擎不检查取消请求，第一次 Ctrl+C 不会中断转码，第二次直接 exit 130，可能留下写到一半的输出文件。
- 输出目录不存在、无权限、文件被占用时，`error_info` 是 `打开输出失败: <系统原话>`。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| `audio` 三个子命令 | 要完整版；完整版产线已贯通（`build_cli/windows`） | 要完整版；完整版产线已贯通（`build_cli/mac`） | 要完整版；**完整版产线还没铺**（AGENTS.md:339），目前只能得到纯 Go 构建 |
| 编码器 | mp3 = libmp3lame、aac = 内置、opus = libopus、flac / wav = 内置。产线配料两平台对齐（lame 3.100、opus v1.5.2，AGENTS.md 配料表） | 同左 | — |
| 输出文件被别的程序占用 | 打不开，`打开输出失败: <系统原话>` | 一般可覆盖 | 一般可覆盖 |

行为本身（旗标、JSON、决策）三平台一致，没有平台分支。

### 纯 Go 构建与完整版（cgo 构建）的差别

| 能力 | 纯 Go（`CGO_ENABLED=0`，开发期的 `bin/zpack-dev`） | 完整版（`build_cli` 产线，交付的 `bin/zpack-cli`） |
|---|---|---|
| `convert` / `compress` / `info` | 全部 exit 1：`无法读取输入: 媒体功能需要完整版（build_cli 产线 CGO 构建）；纯 Go 版不含 libav 引擎`（media/stub_nocgo.go:10-18） | 可用 |
| 旗标与位置参数的校验 | 照常生效。顺序不同：`convert` 先校验旗标再探测；`compress` 先探测再校验旗标，所以纯构建下永远先报「需要完整版」，`--bitrate abc` 的报错被盖住 | 同左 |
| 失败后删输出路径上的文件 | `convert` 必然失败，所以必然删 | 只在真失败时删 |

引擎里靠 `//go:build cgo` 分层：决策、计划、报告是纯 Go（可单测），只有 `probeFile` / `executeFile` 等引擎调用点在非 cgo 构建下换成桩（media/stub_nocgo.go）。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 音频与视频分家，但包没分（2026-11-08）

- 设计是「同一 astiav 引擎、独立命令面」（AGENTS.md:163）：音频的旋钮（码率 / 采样率 / 声道）和视频的（尺寸 / 编码 / 目标体积）不是一回事，分开后帮助、校验、JSON 各自干净。
- 包却没分：引擎都是顶层 `media`，命令面都是 `cli/media`，理由写在 cli/media/doc.go:4——共用结果载荷与日志适配。由此带来四个耦合点：
  - **音频没有自己的引擎函数**。`convert` 和 `compress` 调的都是 `ConvertWithOptions`，音频旗标经 `Options.AudioBitrate` / `Mono` / `SampleRate` 传进去（media/media.go:53-60、101-107）。
  - **结果载荷是从视频的结果里挑出来的**。`buildMediaResultJSON` 先算出通用结果（cli/media/media.go:860-880），audio 只取 `Mode` / `Input` / `Output` / `Audio` / `SizeBytes` / `OriginalSizeBytes` / `Ratio`，再拼上自己的四个字段（cli/media/audio.go:129-135、186-193）。改 `buildMediaResultJSON` 的字段或 `ratio` 算法，media 与 audio 一起变。
  - **`audio info` 直接复用 `media info` 的实现**（cli/media/audio.go:242-243 调 `mediaInfo`）——media info 本来就支持音频流探测，没有另写。代价是 audio info 的载荷是 `mediaInfoJSON`，带着 `rotation` / `display_width` / `video` / `bakeable` 这些视频字段，建议文案也是按 `media compress` 的 128k 阶梯写的。改 media info 就是改 audio info。
  - **日志适配共用** `mediaLogf`（cli/media/media.go:883-885）。

### 6.2 质量旗标如何否决「直拷」

决策矩阵的原则是尽量无损：同编码就直拷。但 `compress` 的语义是必须重编码，`--mono` / `--sample-rate` 也必须落到音频上。做法是在决策之后加一道修正：

- `forceAudioEncode`（media/media.go:62-73）：有质量旗标且原决策是 `remux` 时，有视频就改成 `audio-only`（拷视频 + 重编音频），没有视频改成 `audio-only-output`。注释点明了为什么不直接用 `audio-only-output`——那条路径会丢视频。
- `planStreams` 里音频容器输出的直拷条件要求「源编码 = 目标编码 **且** 码率、声道、采样率三个旗标都没给」（media/engine_cgo.go:190-192）。任一旗标出现就走重编码。
- `compress` 的目标码率恒大于 0，所以同格式也一定重编码（cli/media/audio.go:180 的注释）。
- **有个缝**：原决策是 `full`（视频编码与目标容器不兼容）时不经过 `forceAudioEncode`，而 `planStreams` 的 `full` 分支对「与容器兼容的音频」是直拷的，没看旗标（media/engine_cgo.go:233-242）。`forceAudioEncode` 的注释说「视频容器 full → 不动（音频计划在 planStreams 里按 audioBitrate 强制编码）」，与代码不符。

### 6.3 `compress` 的缺省与下限

- 缺省压到原始码率的 50%、下限 32k、目标不低于原始就拒绝，是 2026-11-08 音频与视频分家时定下的（AGENTS.md:163-165）。实测记录：320k 402KB → 160k 201KB（50.0%）→ 64k 80KB（20%）（AGENTS.md:167）。
- 下限 32k 的理由：再低语音都糊（cli/media/audio.go:169）。
- 拒绝的理由：目标不低于原始时重编码只会白白损失音质，所以明确拒绝并在文案里指路 `audio convert`。
- 原始码率探测不到时不拒绝——没有依据判断「用不用压」，交给用户。

### 6.4 数值解析与静默：两次修正和一处漏网

- **`--sample-rate` 改成严格整数解析**（`cmdutil.ParseIntStrict`，提交 6016f9b；cli/media/audio.go:73-76）。此前是按前缀取数，`44100abc` 会被当成 44100。全项目的数值旗标都在往严格解析收敛（internal/cmdutil/flags.go 的包注释写明这是与旧版的故意分歧）。
- **`--quiet` 真正生效**（提交 6d31586）。此前 audio 的 `--quiet` 只在解析的 switch 里占了个位，没有任何地方读取——放了等于没放。现在日志去向由 `cliutil.EngineLogf(args)` 按本次调用的参数决定（internal/cliutil/logf.go:18-26；conventions.md 第 2 节 `--quiet` 一行记的也是这条）：带 `--quiet` 返回 nil，引擎侧全部静默。命令面有两个使用点：`mediaLogf(args)`（转给引擎的 `Logf`，cli/media/audio.go:124、181）和 `compress` 开工前的那行说明（cli/media/audio.go:176-178）。
  - 设计纪律（logf.go 的注释）：日志去向必须按「本次调用的参数」决定，不能放包级变量——命令面按功能拆包后各命令各管各的，而且一个进程只执行一条命令，全局状态是多余的耦合。
- **漏网的一处**：`parseBitrateStr` 是先去掉 `k` 后缀、再转小写（cli/media/audio.go:212），所以大写 `K` 报错。本意显然是两种大小写都收。

### 6.5 管线的来历与 go-astiav 的几个点

- 管线骨架改编自 go-astiav 官方的 transcoding 示例（Apache-2.0）和 `build_cli/poc` 的验证版（media/engine_cgo.go:3-5）。
- **定长帧编码器要缓冲**：mp3（1152 采样）和 aac（1024 采样）要求每帧采样数固定，而滤镜图吐出的帧长度不定，直接送会被拒（frame_size not respected）。所以中间有一层 `AudioFifo`，凑满一帧再切一片编码，收尾时把不足一帧的尾巴也编掉（media/engine_cgo.go:582-589、737-745、759-788）。
- **声道取「编码器支持表首项」也是示例的写法**（go-astiav 的 examples/transcoding/main.go:302-306）。示例是示意代码，没考虑保立体声——见 9 的疑似缺陷。
- **采样率对齐**：libopus 只接受 48k 系，所以编码器支持表里没有源采样率时取表里第一项（media/engine_cgo.go:552-565）。对齐看的是源采样率而不是用户指定的，见 9。
- **主循环的错误只记日志**（media/engine_cgo.go:413-449），同样是示例的写法：读包、写包、送包失败都是 `logf` 之后结束循环，不作为错误返回。后果见 4.3。
- 引擎不拷贝任何元数据；跨容器直拷时会清掉流的 `codec_tag` 让目标封装器自己分配（media/engine_cgo.go:337-339）。

### 6.6 与 GUI 的耦合

GUI 经 CLI 二进制契约驱动：

- 音频转换页：`audio convert <输入> <输出> [--bitrate v] [--sample-rate v] [--mono]`（ui/gui/main.go:3090-3099）。输出格式行给的是 .mp3 / .m4a / .flac / .wav / .ogg / .opus（ui/gui/main.go:3063），缺省 .mp3（ui/gui/main.go:5081）。
- 音频压缩页：`audio compress <输入> --output <输出> [--bitrate v] [--mono]`，输出名是 `<基名>-compressed<扩展名>`（ui/gui/main.go:3138-3146）。**GUI 永远带 `--output`**，不走就地路径。
- 前端校验 `validateBitrate` / `validateSampleRate`（ui/gui/validate.go:150-173）照引擎的解析规则重写，三个常量 `BITRATE_MAX_KBPS = 3200`、`SAMPLE_RATE_MIN_HZ = 8000`、`SAMPLE_RATE_MAX_HZ = 192000`（ui/gui/validate.go:19-21）的注释标着「cli … 同值」。改 CLI 的上下限必须同步这三个常量。

### 6.7 改动时容易破坏的东西

- `audioResultJSON` 的字段名与声明序；`omitzero` 字段赋了 0 / false 就等于从 JSON 里删掉。
- 引擎错误文案与 JSON 里的取值——验收脚本在 grep `"bitrate":64000`、`"bitrate":128000`、`"action":"encode"`、`"is_success":false`。
- `buildMediaResultJSON`、`mediaInfo`、`ConvertWithOptions`、`Options`：都和 media 共用。
- `forceAudioEncode` 的分支与 `planStreams` 里直拷的条件（两者要配套改）。
- `audioOnlyTargets`：它同时决定「哪些扩展名算音频容器」和「目标编码」。加一种格式要同步 `audioCodecIDByName`、帮助文案、GUI 的输出格式行。
- 完整版产线的编码器配料：换掉 libmp3lame / libopus，声道支持表和采样率支持表都会变。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面 | `cli/media/audio.go` | 三个子命令、质量旗标解析（`parseAudioQualityFlags` / `apply`）、`audioResultJSON`、`parseBitrateStr` |
| 命令面（与视频共用） | `cli/media/media.go` | `buildMediaResultJSON`（:860-880）、`mediaStreamJSON`（:854-858）、`mediaLogf`（:883-885）、`mediaInfo`（:756-794）、`mediaInfoJSON`（:914-926） |
| 命令面包说明 | `cli/media/doc.go` | 为什么音频与视频同包 |
| 装配 | `cli/root.go:75` | `media.NewAudioCmd()` 挂到根命令 |
| 引擎入口 | `media/media.go` | `ConvertWithOptions`（:81-116）、`Options`（:53-60）、`forceAudioEncode`（:65-73）、`Info`（:233-243） |
| 决策 | `media/decide.go` | `Decide`、`audioOnlyTargets`（:76-79）、`ContainerForExt`、容器兼容表 |
| 报告 | `media/info.go` | `InfoReport`、`InfoStream`、`BuildInfoReport`、`AnalyzeCompression` |
| 执行（cgo） | `media/engine_cgo.go` | `probeFile`、`planStreams`、`executeFile`、`setupEncoder`、AudioFifo 切片 |
| 纯构建桩 | `media/stub_nocgo.go` | `ErrRequiresFullBuild` |
| 日志去向 | `internal/cliutil/logf.go` | `EngineLogf`（`--quiet` 的判定） |
| 严格整数解析 | `internal/cmdutil/flags.go` | `ParseIntStrict` |
| 测试 | `media/media_test.go` | `TestDecide` 里有两条音频容器用例（mp4 → m4a / mp3 都是 `audio-only-output`）。没有音频旗标、声道、采样率的测试 |
| GUI | `ui/gui/main.go:3051-3146`、`ui/gui/validate.go:150-173` | 两个页面与两条前端校验 |
| 验收脚本 | `verify/verify-media.sh:445-480` | 「audio 命令族」一节，需完整版 |

## 8. 验收用例

### `verify/verify-media.sh`（需完整版 `bin/zpack-cli`）

「audio 命令族」一节（445–480 行），夹具 `audio-src.m4a` 由第 36 行生成：5 秒 440 Hz 正弦、aac 256k。注意 `sine` 源缺省是**单声道** 44.1 kHz，所以整个音频门禁里没有一段立体声。

| 用例 | 断言 |
|---|---|
| `compress` 缺省（带 `--output`） | exit 0；`"action":"encode"`；体积小于源的 70%；ffprobe 码率 < 200000 |
| `compress --bitrate 64k` | JSON 含 `"bitrate":64000` |
| `compress --bitrate 64k --mono` | ffprobe 声道数 = 1 |
| `compress --bitrate 320k`（不低于源的 256k） | exit 1，`"is_success":false` |
| `convert` m4a → mp3，`--bitrate 128k --sample-rate 22050 --mono` | exit 0；`"bitrate":128000`；ffprobe 采样率 = 22050；声道数 = 1 |
| `info` | exit 0；含 `"audio"` |
| 退化 | `compress` 输入不存在 exit 1；`--bitrate abc` exit 1；`audio --help` exit 0 |

共 17 项断言。`media` 一节里还有几条用到纯音频夹具的用例（49–54 行「音频容器抽取」、73–77 行「纯音频压缩」、109–110 行「纯音频 info」），归 [media.md](media.md)。

### 没覆盖的

- **就地覆盖**：每一条 `compress` 用例都带 `--output`，缺省路径从未被验收走过。
- **立体声保真**：夹具是单声道，`--mono` 的断言在「一律单声道」的情况下也会通过。
- `--sample-rate` 的两条报错、`--quiet`、compress 的 32k 下限、码率探测不到的源、无损格式输出。
- `convert` 不带旗标的直拷路径（`action:"copy"`）；视频容器输出 + 旗标（`audio-only`）；`.aac`、`.flac`、`.ogg`、`.wav` 四种输出。
- 纯 Go 构建下的「需完整版」文案——这个脚本发现 `bin/zpack-cli` 是纯构建会直接拒绝运行（22–27 行）。

### 手动验证

```bash
zpack audio --help; echo $?                   # 0
bin/zpack-cli audio info 歌.mp3                    # 看 bitrate / sample_rate / channels
bin/zpack-dev audio info 歌.mp3; echo $?       # 纯构建：需完整版，exit 1
```

验证 9 里的声道疑似缺陷：拿一段**立体声** wav，`bin/zpack-cli audio convert 立体声.wav 测试.mp3`，再用 ffprobe 看声道数——是 2 就说明没问题，是 1 就坐实了。

## 9. 未结项与已知限制

### 没做的

- 纯 Go 构建不可用（要完整版）；Linux 的完整版产线没铺。
- 码率对无损格式（flac / wav）不生效，也不提示。
- 只有平均码率一个旋钮：没有质量（VBR / Q）档位，没有两遍编码。
- 不保留标签与封面。
- 没有进度输出。
- 输出扩展名区分大小写，大写扩展名的行为没有定义。
- 多音轨输入的行为没有定义【待确认】。

### 已知不对但暂时没改的

- **疑似缺陷：mp3 输出不带 `--mono` 也是单声道**。声道布局的取法是「`--mono` > 编码器支持表首项 > 源布局」（media/engine_cgo.go:539-546），而 libmp3lame 的支持表第一项是 mono（FFmpeg n9.0 `libavcodec/libmp3lame.c`：`CODEC_CH_LAYOUTS(AV_CHANNEL_LAYOUT_MONO, AV_CHANNEL_LAYOUT_STEREO)`）。按代码，立体声转 mp3 会被 `aformat` 混成单声道。aac 是否同样取决于它支持表的顺序，没能核实。两条都只是读代码，未实测；验收夹具是单声道，测不出来。
- **疑似缺陷：就地覆盖**（`compress` 的缺省路径）。输入先以读方式打开、输出再以截断方式打开同一路径（media/engine_cgo.go:292、383），之后的读取可能读到残缺或被覆盖的内容。未实测；验收从不走这条路。在此之前请带 `--output`。
- **疑似缺陷：采样率对齐看的是源采样率**（media/engine_cgo.go:552-565），用户指定的 `--sample-rate` 可能被支持表第一项覆盖（opus 输出 + 44.1k 源）【待确认】未实测。
- **`--bitrate 128K`（大写 K）报错**（cli/media/audio.go:212 先去后缀后转小写），而 GUI 的 `validateBitrate` 接受它（ui/gui/validate.go:151-155 先转小写）——GUI 放行、引擎拒绝。
- **GUI 的采样率校验还是前缀语义**：`validateSampleRate` 用 `leadingInt`（ui/gui/validate.go:163-173），`44100abc` 在 GUI 里能过，到引擎才报错。
- **就地覆盖时 `original_size_bytes` 与 `ratio` 失真**：转码之后才量输入文件（cli/media/media.go:877），`ratio` 会是 1。
- **失败时删输出路径上的文件**，会误删原有文件；就地 `compress` 失败等于删源文件（见 4.3）。
- **引擎主循环里的错误不作为失败返回**，截断的产物会以 `is_success:true` 报出（见 4.3）。
- **`full` 模式下质量旗标被忽略**（音频与目标容器兼容时直拷），且 `forceAudioEncode` 的注释与代码不符（见 6.2）。
- **只给 `--mono` / `--sample-rate` 时的码率不是「跟随源」**：输出到音频容器是定值 128k；`compress` 则仍按 50% 规则算，源码率不高于 32k 时会被拒。
- `compress` 遇到没有音频流或码率探测不到的输入时，JSON 照样报 `bitrate:32000`，容易让人以为压过了。
- `audio info` 的建议文案按 `media compress` 的 128k 阶梯写，与 `audio compress` 的 50% 缺省不是一回事。
- `--bitrate` / `--sample-rate` / `--output` 缺值时静默忽略，与「缺值要报错」的惯例不一致。
- `--quiet`、`--output`（convert 上）、`-b` / `-o` 两个短别名都没写进帮助或用法文案。

### 代码与文档不一致的地方

| 文档说法 | 代码现状 | 出处 |
|---|---|---|
| 输出示例 `{"is_success":true,"mode":…,"audio":{…},"size_bytes":N,"ratio":N,"bitrate":N,"sample_rate":N,"mono":true}` | 还有 `input`、`output`、`original_size_bytes` 三个字段 | AGENTS.md:80 对 cli/media/audio.go:197-209 |
| 命令形态 `audio compress <输入> [--bitrate 64k] [--mono]` | 还接受 `--sample-rate`、`--output` / `-o`、`--quiet`、`-b` | AGENTS.md:81、165 对 cli/media/audio.go:66-88 |
| 「实测：320k 402KB → 160k 201KB（50.0%）→ 64k 80KB（20%）」，且「`--output` 缺省就地覆盖」 | 就地覆盖时命令自己报的 `ratio` 是 1、`original_size_bytes` 是新文件的大小；这组数字应是不走就地覆盖（或在外部量的）时得到的 | AGENTS.md:165、167 对 cli/media/media.go:877 |
| `forceAudioEncode` 的注释：「视频容器 full → 不动（音频计划在 planStreams 里按 audioBitrate 强制编码）」 | `full` 分支对与容器兼容的音频是直拷，没看 `audioBitrate` | media/media.go:64 对 media/engine_cgo.go:233-242 |
| GUI 注释：「采样率：…引擎按前缀数字取，这里同款」 | 引擎已改成严格整数解析 | ui/gui/validate.go:162 对 cli/media/audio.go:73 |
| 「已知局限 v2」只列了字幕互转与两遍编码 | 没有提到声道、采样率对齐、就地覆盖这些问题 | AGENTS.md:169 |

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 修复：`compress` 就地覆盖不再毁掉源文件（引擎先写临时文件、成功后替换；失败时原文件不动） | 417e3a8 |
