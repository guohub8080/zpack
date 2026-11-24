# 视频（media）

> 视频功能的转码、压缩、体检、几何变换与剪辑命令面。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

- **做什么**：`zpack media` 是视频功能的命令面，底下是进程内的 libav 引擎（顶层 `media/` 包，go-astiav v0.43.0）。`runMedia` 一共分派 16 个子命令（cli/media/media.go:37-68），按用途分四组：
  - 转码与压缩：`convert`（尽量无损的格式转换）、`compress`（压到目标体积）。
  - 体检与档位：`info`（探测事实 + 确定性的压缩建议）、`preset`（平台档位库的列表与详情）。
  - 显示态与几何：`bake`（把旋转 / SAR / HDR / VFR 物理化进像素）、`rotate-left` / `rotate-right` / `rotate-180` / `mirror` / `flip`。
  - 剪辑：`screenshot`（截图）、`cover`（内嵌封面缩略图）、`insert-frame`（可见封面帧）、`clip`（截取片段）、`merge`（拼接）、`speed`（变速）。
  - 同一个包（`cli/media`，命令面）里还注册了 `zpack ffmpeg <原版任意参数...>`——内置 ffmpeg CLI 的透明代理，全项目唯一不走 JSON 信封的命令（cli/media/ffmpeg.go:18-39）。
- **不做什么**：
  - 音频专属的质量旗标（`--bitrate` / `--sample-rate` / `--mono`）属于 `zpack audio`，见 [audio.md](audio.md)。视频转码时音频流照样要处理，但只按容器兼容表「能拷就拷、不能拷就转码」。
  - 不做画面裁切、滤镜、水印、字幕烧录、字幕文本格式互转。字幕轨只有两种结局：容器兼容就原样保轨，不兼容就丢弃并在 stderr 告警。
  - 不提供轨道选择：探测到的每条视频流、音频流、字幕流都会进执行计划；决策矩阵只看首条视频流与首条音频流，结果 JSON 的 `video` / `audio` 报的是计划里最后一条同类流（media/engine_cgo.go:168、356-375）。
  - 不暴露编码器选择与编码参数：编码器由 `avcodec_find_encoder` 按注册顺序取第一个，画质只有「码率」一个旋钮，没有 CRF、preset、GOP、二遍编码 stats 之类的入口；`--screen` 只降不升。
  - 不做硬件编码指定、不做 GIF、不录屏、不采集设备。这些长尾需求走 `zpack ffmpeg`。
  - 不封装 ffprobe：`media info` 的字段是固定的一组，要探测全貌用 `zpack ffmpeg` / ffprobe。
  - 不处理图片文件本身（那是 `image` 功能），但 `screenshot` 借 image 包写图，`cover` / `insert-frame` 借 image 包读图。
  - 不响应取消：第一次 Ctrl+C 不会中断转码，第二次直接 exit 130，输出路径上会留下半成品（见 [conventions.md](conventions.md) §2「取消的范围」）。
- **使用场景**：
  - 手机竖拍视频要发微信 / Gmail / Discord：`media info` 先体检，再 `media compress --preset wechat`；源已经够小就自动直拷贝，不浪费画质。
  - MKV 里的 HEVC 想放进 MP4、或抽出一条音轨：`media convert`，能 remux 就 remux，秒级完成且字节不变。
  - 老视频瘦身：`media info` 读 `suggestions[]`，按 `order` 与 `flag` 拼 `--screen` / `--codec` 再交给 `convert` / `compress`。
  - 竖拍视频在只认像素的软件里躺倒：`media bake` 一次把旋转和像素比烤实。
  - 截一段、拼几段、做封面图、抽几帧截图、做倍速播放版。
  - 以上都不够用时：`zpack ffmpeg` 直接用原版命令面。

## 2. 命令与旗标

### 2.1 子命令一览

| 命令 | 作用 |
|---|---|
| `zpack media convert <输入> <输出>` | 转格式。三级瀑布尽量无损：能 remux 就 remux，其次只重编音频，最后才全编。输出容器由扩展名定 |
| `zpack media compress <输入> [--output 路径] [--target-size 值]` | 压到目标体积（不带预设时恒全编）。缺省目标 = 原始的 50%，缺省输出 = 同目录 `<基名>.mp4` |
| `zpack media info <文件>` | 体检：容器 / 时长 / 旋转 / 显示尺寸 / 音视频流事实 + `suggestions[]` 压缩建议 + `bakeable[]` |
| `zpack media preset [<ID>]` | 预设库：不带 ID 出分组列表，带 ID 出单档详情。纯 Go，纯构建下也能用 |
| `zpack media bake <输入> <输出>` | 显示元数据物理化：旋转、SAR、HDR、VFR 一次烤进像素；无可烤则 remux |
| `zpack media rotate-left\|rotate-right\|rotate-180\|mirror\|flip <输入> <输出> [--bake]` | 几何变换。旋转族缺省零损写 display-matrix；`mirror`（左右翻）/ `flip`（上下翻）恒烤入 |
| `zpack media screenshot <视频> [--time\|--every\|--count\|--times] [--output\|--output-dir] [--format]` | 截图：单帧或多帧，存成图片 |
| `zpack media cover <视频> <图片> [输出]` | 把图片作为 attached_pic 缩略图嵌进容器（remux，视频流零重编） |
| `zpack media insert-frame <视频> <图片> [输出] [--front\|--back]` | 在开头或末尾插入一帧可见的封面画面（重编码，时长 +1 帧） |
| `zpack media clip <视频> [输出] --seconds-from 秒 --seconds-to 秒` 或 `--frame-from N --frame-to M` | 截取片段，恒帧精确（重编码）。区间左闭右开 `[from, to)` |
| `zpack media merge <视频1> <视频2> [视频3...] <输出> [--reference 路径]` | 拼接：以基准视频（缺省第一个）的分辨率 / 编码为准，其余拉伸匹配，全程一个编码会话 |
| `zpack media speed <视频> [输出] --factor 倍数 [--pitch-match]` | 变速：`--factor 1.5` 快放，`0.8` 慢放（重编码） |
| `zpack ffmpeg <原版任意参数...>` | 内置 ffmpeg CLI 透明代理，stdout / stderr / 退出码全是 ffmpeg 原生语义 |

子命令名取「第一个不以 `-` 开头的参数」（cli/media/media.go:38、902-910），所以旗标只能写在子命令名之后。分派以外的任何子命令名（含 `help`）都报用法错误（cli/media/media.go:65-67）：

```
{"is_success":false,"error_info":"用法: media convert <输入> <输出.mp4|mkv|webm|mp3|m4a|flac> 或 media compress <输入> [--output 路径] [--target-size 50%|100MB|cd]"}
```

**帮助文本是过时的**：`Use` 只写了 `<convert|compress>`，Short 只提「转换与压缩」。`zpack media --help` 的实际输出是：

```
媒体转换（尽量无损）与压缩（业界主流压缩决策层 + 进程内 libav 引擎）

Usage:
  zpack media <convert|compress> [参数] [flags]

Flags:
  -h, --help   help for media
```

任何子命令后跟 `--help` / `-h` 打印的都是这份父命令帮助（`helpAwareRun` 守卫，cli/media/media.go:32），没有子命令级的帮助；每个子命令的用法只出现在「参数不对」时的 error JSON 里（见 §3.6）。

### 2.2 旗标

| 旗标 | 适用命令 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|---|
| `--screen <档位>` | convert、compress | 尺寸上限（适配框）：超出才等比缩小，偶数对齐，只降不升 | `2160p` `1440p` `1080p` `720p` `480p` `360p`；其它值报错。与 `--preset` 互斥 | 不缩放 |
| `--codec <名>` | convert、compress | 目标视频编码 | `h264` `hevc` `vp9` `av1`，且须与输出容器兼容；其它值报错。**带 `--preset` 时不生效**（§9.2 第 18 条） | 容器默认：mp4 / m4v / mov → h264，webm → vp9 |
| `--preset <ID>` | convert、compress | 套用预设库的一档 | 预设 ID（`media preset` 可查）；compress 还要求该档定义了 `max_size_bytes` | 不套用 |
| `--target-size <值>` | compress | 目标体积 | 百分比 / 人读尺寸 / 介质模板，见 §4.1.3 | 原始的 50%；带 messaging 类预设时为预设上限 |
| `--output <路径>` | compress | 输出路径 | 任意 | 同目录 `<基名>.mp4` |
| `--quiet` | convert、compress、bake、五个变换命令 | 抑制 stderr 过程日志（统一开始日志不受影响） | 布尔，出现即生效 | 不静默 |
| `--bake` | rotate-left / right / 180、mirror、flip | 强制烤入（重编码） | 布尔；对 mirror / flip 无意义（本来就烤入） | 旋转族在满足条件时走零损矩阵 |
| `--time <秒>` | screenshot | 截单帧的时间点 | 非负数，可带 `s` 后缀；必须小于视频时长 | — |
| `--every <间隔>` | screenshot | 每隔多少秒截一帧（不含 0 秒那帧） | 正数秒 | — |
| `--count <N>` | screenshot | 均匀分布截 N 帧 | 正整数；**解析失败或 ≤0 时静默忽略** | — |
| `--times <秒,秒,...>` | screenshot | 指定几个时间点 | 逗号分隔的非负数，任一非法即报错 | — |
| `--output <路径>` | screenshot | 单帧输出文件（**格式跟输出路径的扩展名走**） | 任意；无扩展名时补 `.jpg` | 同目录 `<基名>_shot.<format>` |
| `--output-dir <目录>` | screenshot | 多帧输出目录 | 不存在会自动创建 | 视频所在目录 |
| `--format <格式>` | screenshot | 图片格式 | 缺省 `jpg`，命令面不校验；引擎按 image 包的写图能力接受 jpg / jpeg / png / gif / bmp / tiff / webp，其它值见 §4.2 | `jpg` |
| `--front` / `--back` | insert-frame | 封面帧放开头还是末尾 | 布尔；两个都给时后出现的为准 | `--front` |
| `--seconds-from` / `--from <秒>` | clip | 起始时间（含） | 非负浮点；**解析失败或为负时静默忽略** | `0` |
| `--seconds-to` / `--to <秒>` | clip | 结束时间（不含） | 正浮点；**解析失败或 ≤0 时静默忽略** | 无，必须给 |
| `--frame-from <N>` | clip | 起始帧号（含） | 非负整数；解析失败时忽略 | `0` |
| `--frame-to <N>` | clip | 结束帧号（不含） | 正整数；解析失败或 ≤0 时忽略 | `frame-from + 1` |
| `--reference` / `--ref <路径>` | merge | 以哪个输入为基准 | 必须与某个输入的路径字符串**完全相同** | 第一个输入 |
| `--factor` / `--speed <倍数>` | speed | 速度倍数，>1 加速、<1 减速 | 大于 0 且不超过 10 的数（报错文案写的 0.1-10，但下限 0.1 没有强制） | 无，必须给 |
| `--pitch-match` | speed | 「音调跟随速度」 | 布尔；**引擎没有使用这个值**（见 §4.1.13） | 文案上称「保音调」 |

每个子命令的位置参数个数与缺省输出名：

| 子命令 | 位置参数 | 缺省输出 |
|---|---|---|
| convert | 恰好 2 个：输入、输出 | 无（必须给） |
| compress | 恰好 1 个 | 同目录 `<基名>.mp4`（cli/media/media.go:215-217） |
| info | 恰好 1 个 | — |
| preset | 0 或 1 个 | — |
| bake、五个变换命令 | 恰好 2 个 | 无（必须给） |
| screenshot | 至少 1 个（多余的忽略） | 单帧：`<基名>_shot.<format>`；多帧：`--output-dir` 下的 `<基名>_<序号>.<format>`，序号从 1 起、位数对齐到总帧数 |
| cover | 至少 2 个：视频、图片；第 3 个是输出 | `<基名>_cover<原扩展名>` |
| insert-frame | 同 cover | `<基名>_framed<原扩展名>` |
| clip | 至少 1 个；第 2 个是输出 | `<基名>_<起>-<止>s<原扩展名>`，两个时间各保留 1 位小数 |
| merge | 至少 3 个：前 N-1 个是输入，最后一个是输出 | 无（必须给） |
| speed | 至少 1 个；第 2 个是输出 | 倍速 >1 时 `<基名>_1.5x<原扩展名>`（1 位小数），否则 `<基名>_0.80x<原扩展名>`（2 位小数） |

### 2.3 解析规则（本功能特有）

1. **带值旗标的值是下一个参数**，不支持 `--flag=value` 写法——`--screen=1080p` 整个被当成未知旗标静默忽略，等于没给（各子命令的解析循环都是 `arg == "--xxx"` 精确匹配，cli/media/media.go:79）。
2. **带值旗标缺值**一律报「`--xxx 缺少值（…）`」（§3.6）。
3. **未知旗标静默忽略**，包括几种容易误以为生效的写法：`convert --bake`（2026-11-07 已退休，现在没有任何效果）；`cover` / `insert-frame` / `clip` / `merge` / `speed` 用法里写的 `--output`（这五个命令的解析循环都不认它，只是因为以 `-` 开头被跳过、它的值落成了位置参数才碰巧能用，**必须紧挨在输出路径本该在的位置上**——放在输入之前就会把输出名当成输入）；`clip --exact`（不存在这个旗标，clip 恒精确）。
4. **互斥**：`--screen` 与 `--preset` 同时出现即报错「`--screen 与 --preset 互斥（预设自带适配框）`」（cli/media/media.go:115-117、224-226）。
5. **数值校验宽严不一**。严格的：`--screen` / `--codec` / `--target-size` / `--factor` / `--time` / `--every` / `--times`，非法值报 error JSON。宽松的：screenshot 的 `--count`，clip 的四个时间 / 帧号旗标，非法值不报错，当作没给（§4.2）。
6. **路径**：输入、输出都先 `filepath.Abs` 再交给引擎，JSON 里回的是绝对路径；以 `-` 开头的路径会被当成旗标吞掉。
7. **`--quiet` 的作用范围**：只有经 `mediaLogf(args)` 接了日志的命令受它影响——convert、compress、bake、五个变换命令（cli/media/media.go:883-885）。info、preset、screenshot、cover、insert-frame、clip、merge、speed 本来就不输出过程日志，`--quiet` 对它们是空操作。
8. **输出已存在直接覆盖**，没有提示也没有 `--force`；**失败时无条件删除输出路径上的文件**，哪怕那是用户原有的、本次并没有写过的文件（§4.3）。
9. 没有 `--jobs`、`--temp-dir`、`--force` 这类旗标；给了也只是被忽略。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

本功能对全局约定唯一的偏离是 `zpack ffmpeg`：它真正跑起来之后不走 JSON 信封，stdout 是 ffmpeg 的原生输出、可能是二进制流，退出码是 ffmpeg 的退出码（§4.1.14）。其余情况——缺参数、纯构建、`--help`——仍然遵守 JSON 信封。

## 3. 输出契约

成功时的 stdout JSON（字段名、类型、含义）：

### 3.1 信封

所有 media 子命令的成功输出都包在 `{"is_success":true,"data":{…}}` 里，退出码 0；失败是 `{"is_success":false,"error_info":"…"}`，退出码 1。下面各表描述的都是 `data` 里的载荷，字段按实际输出顺序列出（载荷一律用 struct 编码，键序 = 声明序，internal/stdout/log.go:40）。

### 3.2 convert / compress / bake / 五个变换命令

载荷类型 `mediaResultJSON`（cli/media/media.go:839-852）：

| 字段 | 类型 | 含义 | 何时出现 |
|---|---|---|---|
| `mode` | string | 走了哪条路：`remux` 全流拷贝 / `audio-only` 拷视频重编音频 / `full` 视频重编 / `audio-only-output` 输出音频容器 / `transform-matrix` 零损写矩阵 / `transform-bake` 烤入 / `bake` | 恒出 |
| `input` | string | 输入的绝对路径 | 恒出 |
| `output` | string | 输出的绝对路径 | 恒出 |
| `video` | object | 视频流结果，见下表 | 恒出 |
| `audio` | object | 音频流结果，结构同 `video` | 恒出 |
| `size_bytes` | int | 输出文件大小 | 恒出 |
| `original_size_bytes` | int | 输入文件大小。**在生成结果时才去 stat**，不是开工前记的 | 恒出 |
| `ratio` | float | `size_bytes / original_size_bytes`；输出为 0 时是 `0`；输入大小取不到时分母按 1 | 恒出 |
| `target_bytes` | int | 目标体积（字节） | 恒出。只有 compress 有意义，其它命令为 `0` |
| `baked` | array\<string\> | bake 实际烤入的种类，如 `["rotation","sar"]`；无可烤时是 `[]` | 仅 `mode` = `bake` |
| `preset` | string | 套用的预设 ID | 仅带了 `--preset` |
| `already_fit` | bool | 预设压缩时源已达标、走了直拷贝 | 恒出。只有该情况为 `true` |

`video` / `audio`（`mediaStreamJSON`，cli/media/media.go:854-858）：

| 字段 | 类型 | 含义 |
|---|---|---|
| `action` | string | `copy` 流拷贝 / `encode` 重编码 / `none` 没有这条流或被丢弃 |
| `codec` | string | `copy` 时是源编码名（`h264` `hevc` `aac` …）；`encode` 时是目标编码名（视频 `h264` `hevc` `vp9` `av1`，音频 `aac` `opus` `mp3` `flac` `pcm_s16le`）。`action` 为 `none` 时省略 |
| `bitrate` | int | 码率，单位 bps。`copy` = 源流码率（可能是整档码率或 0，见 §4.1.1）；`encode` = 生效码率；`none` = `0` |

`target_bytes` 与 `already_fit` 在代码里标的是 `omitempty`，意图应是「零值省略」，但输出层用的是 `encoding/json/v2`，它的 `omitempty` 只省空字符串、空切片和 nil 指针，**不省 `0` 和 `false`**（§6）。所以 convert 的结果里也会带 `"target_bytes":0,"already_fit":false`。这一点是从 Go 源码（`encoding/json/v2/fields.go` 的 `isEmpty` 只覆盖 String / Map / Array / Slice / Pointer / Interface）和 `media info` 的实测输出推出来的（info 的 `width` `height` `is_vfr` 同样标了 `omitempty` 却以 `0` / `false` 出现）；convert 本身没有实跑核对。【待确认：见 §9.3 第 46 条】

各命令的 `mode` 取值：

| 命令 | 可能的 `mode` |
|---|---|
| convert | `remux` / `audio-only` / `full` / `audio-only-output` |
| compress | `full`；带 `--preset` 且源已达标时是 `remux` 并带 `already_fit:true` |
| bake | `bake` |
| rotate-left / right / 180 | `transform-matrix`（零损）或 `transform-bake`（烤入） |
| mirror / flip | 恒 `transform-bake` |

示例（remux 的 convert；路径与数值是示意）：

```json
{"is_success":true,"data":{"mode":"remux","input":"/in/a.mp4","output":"/out/a.mkv","video":{"action":"copy","codec":"h264","bitrate":4000000},"audio":{"action":"copy","codec":"aac","bitrate":128000},"size_bytes":2987123,"original_size_bytes":3012345,"ratio":0.9916263,"target_bytes":0,"already_fit":false}}
```

示例（bake 双烤；数值是示意）：

```json
{"is_success":true,"data":{"mode":"bake","input":"/in/dual.mp4","output":"/out/baked.mp4","video":{"action":"encode","codec":"h264","bitrate":4000000},"audio":{"action":"copy","codec":"aac","bitrate":128000},"size_bytes":1234567,"original_size_bytes":2345678,"ratio":0.5263041,"target_bytes":0,"baked":["rotation","sar"],"already_fit":false}}
```

### 3.3 info

载荷类型 `mediaInfoJSON`（cli/media/media.go:914-926）：

| 字段 | 类型 | 含义 |
|---|---|---|
| `input` | string | 绝对路径 |
| `format` | string | 容器名，取 libav 输入格式名的第一段（`mov` `mkv` `webm` `wav` …） |
| `duration_seconds` | float | 时长（秒） |
| `size_bytes` | int | 文件大小 |
| `rotation` | int | 顺时针旋转角度，恒出，无旋转为 `0` |
| `display_width` / `display_height` | int | 显示尺寸 = 存储尺寸 × SAR，±90° 时对调；没有视频流时为 `0` / `0` |
| `video` | object 或 `null` | 首条视频流的事实，结构见下表；没有视频流为 `null` |
| `audio` | object 或 `null` | 首条音频流的事实；没有音频流为 `null` |
| `suggestions` | array 或 `null` | 压缩建议，规则见 §4.1.4；一条都没有时是 `null`（按代码推断：此时切片是 nil；没有拿到无建议的样本实测，【待确认】） |
| `bakeable` | array\<string\> | 可烤种类，固定顺序 `rotation` `sar` `vfr` `hdr`；没有时是 `[]` |

`video` / `audio`（`engine.InfoStream`，media/info.go:41-55）：

| 字段 | 类型 | 含义 | 何时省略 |
|---|---|---|---|
| `codec` | string | 编码名 | 不省 |
| `bitrate` | int | 码率 bps；流级缺失时视频取整档码率，音频为 `0` | 不省 |
| `width` / `height` | int | **存储**尺寸（不随旋转对调） | 不省（音频流里是 `0`） |
| `fps` | float | 平均帧率 | 不省（取不到是 `0`） |
| `sar_num` / `sar_den` | int | 像素宽高比分子分母；容器未标时是 `0` / `0`，方像素且容器有标时是 `1` / `1` | 不省 |
| `color_range` | string | `limited` / `full` | 空串时 |
| `color_space` | string | `bt709` `bt2020nc` `unspecified` … | 空串时 |
| `transfer` | string | `smpte2084`（PQ HDR）/ `arib-std-b67`（HLG HDR）/ `bt709`；其它取值归空串 | 空串时 |
| `is_vfr` | bool | 变帧率判定 | 不省 |
| `sample_rate` / `channels` | int | 音频流才有意义 | 不省 |

与 §3.2 同样的原因，表里写「不省」的字段虽然带 `omitempty`，零值仍然会出现。实测一个系统 WAV：

```json
{"is_success":true,"data":{"input":"C:\\Windows\\Media\\Alarm01.wav","format":"wav","duration_seconds":5.572245,"size_bytes":491516,"rotation":0,"display_width":0,"display_height":0,"video":null,"audio":{"codec":"pcm_s16le","bitrate":705600,"width":0,"height":0,"fps":0,"sar_num":0,"sar_den":0,"is_vfr":false,"sample_rate":22050,"channels":2},"suggestions":[{"kind":"audio","order":2,"note":"音频 705 kbps 高于 128k 常用档，压缩将降至 128k","estimated_ratio":0.18140589569160998}],"bakeable":[]}}
```

`suggestions[]` 的每一项（`engine.InfoAdvice`，media/info.go:58-64）：

| 字段 | 类型 | 含义 |
|---|---|---|
| `kind` | string | `bakeable` / `aspect-ratio` / `resolution` / `bitrate-inflated` / `audio` / `codec-generation` |
| `order` | int | 执行序：1 = 先烤，2 = 几何与码率，3 = 换编码 |
| `flag` | string | 可直接拼给 convert / compress 的旗标，如 `--screen 1080p`、`--codec hevc`；没有对应旗标的建议省略此字段 |
| `note` | string | 给人读的说明（中文） |
| `estimated_ratio` | float | 预计输出 / 原始；估不了是 `0` |

### 3.4 preset

列表（不带 ID，`presetListJSON`，cli/media/media.go:941-944）：

```json
{"is_success":true,"data":{"count":8,"groups":[{"group":"apple","presets":[{"id":"appletv","name":"Apple TV 高清","container":"mp4","max_size_bytes":0,"note":"…"},{"id":"iphone","name":"iPhone 相册优化","container":"mp4","max_size_bytes":0,"note":"…"}]},{"group":"messaging","presets":[…]},{"group":"social","presets":[…]}]}}
```

组名按字典序、组内预设按 ID 字典序（media/preset.go:49-58）。每档的 `max_size_bytes` 是 `0` 表示没有体积上限（那几档不能用于 compress）。

详情（带 ID，`presetDetailJSON`，cli/media/media.go:946-957）：

```json
{"is_success":true,"data":{"id":"youtube","name":"YouTube 高清上传","group":"social","container":"mp4","width":1920,"height":1080,"video_bitrate":8000000,"audio_bitrate":128000,"max_size_bytes":0,"note":"YouTube 官方推荐 1080p H.264 8Mbps（SDR 30fps 档）"}}
```

### 3.5 剪辑类命令

这些载荷类型定义在纯 Go 层（media/decide.go:136-208），路径都是命令面传进去的绝对路径。

| 命令 | 字段（按输出顺序） |
|---|---|
| screenshot 单帧 | `input`、`output`、`timestamp_seconds`（请求的时间点）、`width`、`height`（存储尺寸，不随旋转对调）、`size_bytes` |
| screenshot 多帧 | `input`、`frames`（每项同单帧结构，只含成功的帧）、`count`（成功帧数）、`output_dir` |
| cover | `input`、`output`、`cover_image`、`width`、`height`（封面流的尺寸：视频尺寸，或纯音频输入时的封面图尺寸）、`size_bytes` |
| insert-frame | `input`、`output`、`position`（`front` / `back`）、`width`、`height`、`codec`（源视频编码名）、`size_bytes` |
| clip | `input`、`output`、`start_time_seconds`、`end_time_seconds`、`duration_seconds`（两者之差）、`mode`（恒 `exact`）、`size_bytes` |
| merge | `reference`（基准视频绝对路径）、`inputs`（全部输入）、`output`、`width`、`height`（基准的存储尺寸）、`codec`（基准的编码名）、`video_count`（输入个数）、`size_bytes` |
| speed | `input`、`output`、`factor`、`original_duration_seconds`（探测到的时长）、`output_duration_seconds`（= 原时长 ÷ factor 的算出来的值，不是量出来的）、`width`、`height`、`codec`、`size_bytes` |

### 3.6 失败时的 `error_info`

**用法错误**（参数个数不对；命令面在读输入文件之前就报）：

| 触发 | `error_info` |
|---|---|
| `media` 没有子命令或子命令名不认识 | 见 §2.1 |
| convert 位置参数不是 2 个 | `用法: media convert <输入> <输出.mp4\|mkv\|mov\|webm\|mp3\|m4a\|flac\|opus\|wav>` |
| compress 位置参数不是 1 个 | `用法: media compress <输入> [--output 路径] [--target-size 50%\|100MB\|cd\|dvd]` |
| info 位置参数不是 1 个 | `用法: media info <媒体文件>` |
| bake 位置参数不是 2 个 | `用法: media bake <输入> <输出.mp4\|mkv\|webm>（把旋转/像素比等显示元数据物理化）` |
| 变换命令位置参数不是 2 个 | `用法: media <变换名> <输入> <输出.mp4\|mkv\|webm> [--bake]` |
| screenshot 没有位置参数 | `用法: media screenshot <视频> [--time 5.5 \| --every 10s \| --count 10 \| --times 5,10,15] [--output 路径 \| --output-dir 目录] [--format jpg\|png\|webp]` |
| cover 位置参数少于 2 个 | `用法: media cover <输入视频> <封面图> [--output 路径]` |
| insert-frame 位置参数少于 2 个 | `用法: media insert-frame <视频> <封面图> [--front\|--back] [--output 路径]（缺省 --front）` |
| clip 没给输入，或时间区间不满足 `0 ≤ from < to` | `用法: media clip <视频> --seconds-from 起始秒 --seconds-to 结束秒 [--frame-from N --frame-to M] [--output 路径]`，换行后接 `区间语义：左闭右开 [from, to)——from 帧含、to 帧不含（同 Python 切片）` |
| merge 位置参数少于 3 个 | `用法: media merge <视频1> <视频2> [视频3...] [--reference 基准路径] --output 路径`，换行后接 `缺省以第一个视频为基准；--reference 可指定任意一个作为基准（分辨率/编码参数）` |
| speed 没给 `--factor` 或没有位置参数 | `用法: media speed <视频> --factor 1.5 [--pitch-match] [--output 路径]`，换行后接 `factor > 1 加速，< 1 减速；缺省保音调（快慢不变调）；--pitch-match 音调跟随速度（快放变尖/慢放变低沉）` |

**带值旗标缺值**：`--xxx 缺少值（提示）`。提示分别是：`--screen`「2160p/1440p/1080p/720p/480p/360p」；`--codec`「h264/hevc/vp9/av1」；`--preset`「如 youtube；用 media preset 查看全部」（compress 里写的是「如 discord」）；`--target-size`「如 50%、100MB、cd」；`--output` / `--output-dir` / `--count` 没有提示；`--time`「如 5.5 或 10s」；`--every`「如 10s 或 30」；`--times`「如 5,10,15」；`--format`「jpg/png/webp」；`--seconds-from` / `--seconds-to`「秒」；`--frame-from`「帧号」；`--frame-to`「帧号，不含」；`--reference`「基准视频路径」；`--factor`「如 1.5 / 0.8」。

**取值校验**：

| 触发 | `error_info` | 出处 |
|---|---|---|
| 输入文件不存在 | `路径不存在：<绝对路径>` | cli/media/media.go:112、197、318、571、667、711、745、768 |
| cover / insert-frame / merge 的视频不存在 | `视频不存在：<绝对路径>` | cli/media/media.go:434、479、618 |
| cover / insert-frame 的图片不存在 | `封面图不存在：<绝对路径>` | cli/media/media.go:437、482 |
| `--screen` 与 `--preset` 并用 | `--screen 与 --preset 互斥（预设自带适配框）` | cli/media/media.go:116 |
| 预设 ID 不认识（convert / compress / preset） | `未知预设: <ID>（用 media preset 查看全部）` | cli/media/media.go:123 |
| `--screen` 档位非法 | `--screen 支持 2160p/1440p/1080p/720p/480p/360p，收到: <值>` | media/directives.go:30 |
| `--codec` 名字非法 | `--codec 支持 h264/hevc/vp9/av1，收到: <值>` | media/directives.go:41 |
| `--codec` 与输出容器不兼容 | `--codec <名> 与容器 <mp4\|webm> 不兼容（该容器接受: <按字典序以 / 连接的编码名>）` | media/directives.go:57 |
| `--target-size` 百分比越界 | `--target-size 百分比需 0-100，收到: <小写原文>` | media/plan.go:98 |
| `--target-size` 认不出 | `--target-size 需百分比（50%）、尺寸（100MB）或模板（cd/dvd/bd50），收到: <去掉单位后的文本>` | media/plan.go:123 |
| 目标体积低于压缩下限 | `目标大小低于下限 <字节> 字节（原始×10% + 音频 <字节> 字节）——音频不可压没，请提高目标` | media/plan.go:69 |
| 时长读不出来 | `时长无效: <浮点>` | media/plan.go:36 |
| compress 用了没有上限的预设 | `预设 <预设名> 未定义大小上限（max_size_bytes），不能用于 compress` | media/preset.go:152 |
| convert `--preset` 的容器与输出扩展名不符 | `输出扩展名 .<扩展名> 与预设 <容器> 的容器 <容器> 不符`（预设名那一段代码填错了，填成了容器名，见 §9.2 第 21 条） | media/preset.go:129 |
| 输入不是媒体文件 | `无法读取输入: 打开输入失败: <libav 原文>`（实测 `Invalid data found when processing input`） | media/media.go:87 |
| 变换命令遇到没有视频流的输入 | `输入无视频流，无法几何变换` | media/media.go:33 |
| 变换命令的输出不是视频容器 | `变换命令需要视频容器输出（mp4/mkv/webm）` | media/media.go:34 |
| bake 遇到没有视频流的输入 | `输入无视频流，无可烤元数据` | media/bake.go:100 |
| bake 的输出是音频容器 | `bake 需要视频容器输出（mp4/mkv/webm）` | media/bake.go:104 |
| screenshot 时间格式不对 | `无效时间格式: <去掉 s 后的文本>` | cli/media/media.go:327 |
| screenshot `--time` 超出时长 | `时间 <秒>s 超出视频时长 <秒>s`（各保留 1 位小数） | cli/media/media.go:347 |
| screenshot `--every` 超出时长 | `间隔 <秒>s 超出视频时长 <秒>s` | cli/media/media.go:368 |
| speed 的 `--factor` 非法 | `--factor 需 0.1-10 的正数，收到: <值>` | cli/media/media.go:655 |
| merge 的 `--reference` 不在输入里 | `--reference <路径> 不在输入列表中` | media/merge.go:32 |
| merge 输入里有没视频流的 | `视频 <序号> 无视频流`（序号从 1 起） | media/merge.go:208 |
| clip 时间区间非法 | `时间需 0 ≤ start < end，收到 start=<秒> end=<秒>` | media/clip.go:19 |

**引擎内部的失败**（`找不到解码器: <编码>`、`找不到编码器: <编码>`、`输出格式创建失败（检查扩展名）`、`打开输出失败: …`、`写头失败: …`、`写尾失败: …`、merge 的 `打开视频 <序号> 失败: …`、insert-frame 的 `合并失败: …` 等）原样进 `error_info`。但**主循环中途**的读写错误不会让命令失败，只记 stderr 日志，见 §4.3。

**纯构建**：所有需要引擎的子命令报「…需要完整版…」，见 §5。

### 3.7 stderr

每个命令入口恒有一行统一开始日志 `zpack <命令名> 开始执行`（`media` 或 `ffmpeg`，由装配层输出，cli/root.go:52）。本功能自己的过程日志全部经引擎的 `Logf` 回调发出，格式 `[YYYY-MM-DD HH:MM:SS] 消息`，`--quiet` 时整体静默：

| 日志 | 出处 | 触发 |
|---|---|---|
| `决策: <mode>（视频:<编码> 音频:<编码> → <扩展名>）` | media/media.go:109 | 不带 `--preset` 的 convert 每次都发；没有对应流的那一段省略，都没有时写 `无流` |
| `预设 <名>（<note>）决策: <mode>` | media/preset.go:137 | convert `--preset` |
| `烤入旋转 <角度>°（transpose 无损像素重排）` | media/engine_cgo.go:622 | 重编路径且源带旋转 |
| `应用滤镜: <滤镜段>` | media/engine_cgo.go:626 | 重编路径且有显式滤镜（mirror / flip / 有损类烤入） |
| `跳过字幕流 #<序号>（<编码> 不兼容容器 <扩展名>）` | media/engine_cgo.go:315 | 字幕轨被丢弃时，每条一行 |
| `转码进度: <N>%` | media/engine_cgo.go:815 | 有视频流且时长已知时，按已编码视频帧数每约 10% 一条（从 `0%` 起） |
| `读包失败` / `写包失败` / `送包失败` / `处理失败` / `收帧失败` / `滤镜编码失败`（后接 `: <错误>`） | media/engine_cgo.go:417-698 | 主循环中途出错；**只记日志，不影响结果** |
| `第一遍编码：全片 <秒>s 产出 <字节> 字节 → 实测视频码率 <bps> bps（请求 <bps>）` | media/media.go:227 | compress 第一遍做完 |
| `采样校正：请求 <bps> bps 实测 <bps> bps → 校正 <bps> bps` | media/media.go:167 | 第一遍实测码率与请求不同 |
| `采样逼近失败（<原因>）——退回单遍直接编码` | media/media.go:161 | 纯音频、时长不足 2 秒或第一遍编码失败 |
| `压缩计划: 目标 <字节> 字节 → 视频 <bps> bps + 音频 <bps> bps（<音频动作>）` | media/media.go:172 | compress 第二遍开工前。音频动作是 `silent` / `copy-or-keep` / `down-to-128k` / `audio-only-allocate` |
| `源 <字节> 字节已符合 <预设名> 上限 <字节>——直拷贝不重编` | media/preset.go:160 | compress `--preset` 且源已达标 |
| `零损变换：写 display-matrix <角度>°（编码不变，秒级完成）` | media/transform.go:111 | 旋转族走矩阵路径 |
| `烤入变换：净角度 <角度>°（源 <角度>° + 命令增量，重编码）` | media/transform.go:131 | 旋转族烤入 |
| `烤入变换：源旋转 <角度>° + <滤镜>（重编码）` | media/transform.go:128 | mirror / flip |
| `无可烤显示元数据（无旋转、方像素）——remux 直拷` | media/bake.go:108 | bake 无可烤 |
| `烤入: <种类列表>（rotation=transpose 无损重排；sar=缩放至显示尺寸+方像素）` | media/bake.go:117 | bake 有可烤 |

info、preset、screenshot、cover、insert-frame、clip、merge、speed 的引擎函数不接收 `Logf`，除开始日志外没有任何过程输出，也没有进度。

`zpack ffmpeg` 真正跑起来后 stderr 是 ffmpeg 自己的日志，但**第一行仍是 zpack 的开始日志**（实测完整版：stderr 只有 `[时间] zpack ffmpeg 开始执行` 一行是 zpack 的）。

## 4. 行为细则

按场景分小节。每个场景写清：输入是什么、工具做什么、输出什么、出错时怎样。先讲所有子命令共用的探测与执行（§4.1.1），再按子命令逐个讲。

### 4.1 正常路径

#### 4.1.1 公共流程：探测与流计划

转码类命令（convert / compress / bake / 五个变换命令）共用同一套东西：探测 → 决策或计划 → 统一执行器。

**探测**（`probeFile`，media/engine_cgo.go:21-118）。每次命令开工时做一遍，读的是容器头，不解码：

- 文件大小、时长（容器时长，微秒 ÷ 1,000,000）、容器名。
- 每条流：序号、种类（`video` / `audio` / `subtitle`，其它种类的流直接跳过）、编码名、码率。
- 视频流另有：平均帧率、SAR、色彩范围（`limited` / `full`）、色彩空间、传输特性、r_frame_rate 与 VFR 判定（r_frame_rate 与平均帧率相差超过 1% 记为 VFR，media/engine_cgo.go:88-91）、旋转。
- 音频流另有：采样率、声道数。
- `Video` / `Audio` 指向首条视频流 / 首条音频流，决策矩阵只看这两个。

两个取值细节：

- **视频码率的取值顺序**：流级码率 ≤ 0 时用整档码率顶替（media/engine_cgo.go:69-72）；执行层发现码率仍是 0 时视频保底 2,000,000 bps、音频保底 128,000 bps（media/engine_cgo.go:245-251）。
- **旋转角度是顺时针度数**。先读 display-matrix 侧数据（2016 年以后的手机标准），没有再读旧式 `rotate` 元数据标签（media/engine_cgo.go:92-104）。astiav 的 `DisplayMatrix.Rotation()` 返回的是 `-av_display_rotation_get`，与 ffprobe 打印的 `rotation=` 同号；取值四舍五入到整数。

**容器与编码的兼容表**（media/decide.go:52-79）是决策矩阵唯一的判定依据：

| 容器键 | 对应的输出扩展名 | 兼容的视频编码（「全部」= 不限） | 兼容的音频编码 | 兼容的字幕编码 |
|---|---|---|---|---|
| `mp4` | mp4 / m4v / mov | h264、hevc、av1 | aac、mp3、ac3、eac3、alac | mov_text |
| `mkv` | mkv | 全部 | 全部 | 全部 |
| `webm` | webm | vp8、vp9、av1 | opus、vorbis | webvtt |
| `wav` | wav | （空集） | pcm_s16le、pcm_s24le、pcm_f32le | 没有表项 |
| 音频容器 | mp3、m4a、aac、flac、opus、ogg | — | — | — |

扩展名到容器键的换算（`ContainerForExt`，media/decide.go:90-106）**区分大小写**：`.MP4` 落不进任何一个表，按「未知容器」处理。音频容器的目标编码是固定的：mp3 → mp3，m4a / aac → aac，flac → flac，opus / ogg → opus，wav → pcm_s16le（media/decide.go:76-79）。`wav` 同时出现在音频容器表里，所以以它为输出的 convert 恒走抽音轨路径。

**目标编码**（media/engine_cgo.go:147-166）：视频默认 h264，webm 默认 vp9，`--codec` 覆盖；音频默认 aac，webm 默认 opus，音频容器按上表。具体落到哪个编码器实现，取决于 `avcodec_find_encoder` 按注册顺序返回的第一个，结果 JSON 不暴露这一点（§9.3 第 45 条）。

**流计划**（`planStreams`，media/engine_cgo.go:143-255）按模式给每条流定动作：

| 模式 | 视频流 | 音频流 | 字幕流 |
|---|---|---|---|
| `remux` | 拷贝 | 拷贝 | 容器兼容则拷贝，否则丢弃并告警 |
| `audio-only` | 拷贝 | 重编码；码率取 `min(源码率, 128k)`（指定了码率时用指定的） | 同上 |
| `full` | 重编码；码率取计划值，计划为 0 时跟随源 | 视频容器：兼容则拷贝，不兼容才转 aac / opus。音频容器：与容器目标编码相同则拷贝，否则重编码 | 同上 |
| `audio-only-output` | 丢弃（不告警） | 与容器目标编码相同则拷贝，否则重编码（码率 128k，落到保底值） | 丢弃并告警 |

字幕的「兼容」只认上表三行；目标容器没有表项（未知容器、wav）时 nil 被当成「全部兼容」，字幕轨会被原样拷过去（media/engine_cgo.go:172-183）。

**统一执行器**（`executeFile`，media/engine_cgo.go:282-468）：

- 拷贝路径：整份拷流参数（display-matrix 等侧数据随之带过去，所以 remux 保旋转）、清掉 `codec_tag` 让目标容器自己分配（跨容器 remux 必须）、沿用源时基（media/engine_cgo.go:333-340）。
- 重编码路径：解码 → 滤镜图 → 编码。视频滤镜链是一个字符串，分段往前拼出来的，**最终顺序**是：`scale=W:H,setsar=1`（有缩放时）→ 显式滤镜（hflip / vflip / fps / colorspace）→ 源旋转的 `transpose` → `format=pix_fmts=<编码器支持的第一个像素格式>`（media/engine_cgo.go:617-631）。这个顺序与同处的注释、media/transform.go:123、media/bake_exec.go:10 三处注释写的顺序相反，后果见 §9.3 第 39 条。
- 编码器尺寸：源带 ±90° 旋转时宽高对调（旋转烤入后帧的真实尺寸）；有缩放时取缩放目标，并把编码器 SAR 设成 1:1。
- 视频解码器必须先 `SetFramerate(GuessFrameRate)`，否则 x264 会把时基（如 1/15360）当成帧率（media/engine_cgo.go:492）。
- 音频滤镜链 `aformat=sample_fmts=…:channel_layouts=…:sample_rates=…` 把采样格式归一到编码器支持的第一个；定长帧编码器（mp3 1152、aac 1024）经 AudioFifo 攒够一帧再切出来编码（media/engine_cgo.go:582-589、760-788）；编码器不支持源采样率时取它支持的第一个（libopus 只吃 48k 系，media/engine_cgo.go:552-565）。
- PTS 不递增的帧直接丢弃（media/engine_cgo.go:693-695）。
- 输出容器由 libavformat 按输出路径的扩展名猜；猜不出报「输出格式创建失败（检查扩展名）」。

#### 4.1.2 convert

流程（cli/media/media.go:71-139）：

1. 解析旗标与两个位置参数，转绝对路径，检查输入存在。
2. `--screen` 与 `--preset` 互斥检查。
3. 带了 `--preset`：查预设 → 清掉预设的 `video_bitrate` → `ConvertPreset`（规则见 §4.1.5）。
4. 没带预设：`ConvertWithOptions`（media/media.go:81-116）——探测 → `--codec` 与输出容器的兼容校验 → 决策矩阵 → 叠加 `--screen` / `--codec` → 执行。
5. 引擎报错：删输出路径、输出 error JSON。成功：输出结果 JSON。

**三级瀑布决策矩阵**（`Decide`，media/decide.go:111-133）。判定用的是首条视频流与首条音频流：

```
扩展名不认识（不在任何表里）          → full        （未知容器按全编处理，安全侧）
输出是音频容器（mp3/m4a/…/wav）        → audio-only-output
视频兼容 且 音频兼容                  → remux       （全流拷贝，秒级，字节不变）
视频兼容 但 音频不兼容                → audio-only  （拷视频，音频转 aac / opus）
视频不兼容                            → full        （视频重编，码率跟随源）
```

「兼容」指编码名出现在 §4.1.1 的表里；没有视频流（或没有音频流）算兼容；mkv 两列都是「全部」。**旋转不再否决直拷**（2026-11-05 修正，media/decide.go:121-122）：remux 时旋转矩阵随流参数原样保留。

**正交指令叠加在矩阵之上**（`ApplyDirectives`，media/directives.go:64-89）：

- `--screen <档位>`：先在显示坐标系（存储尺寸 × SAR，±90° 对调）里看源是否超出档位的适配框；超出才等比缩入并强制 `full`，不超出不缩放也不强制。适配框：2160p = 3840×2160、1440p = 2560×1440、1080p = 1920×1080、720p = 1280×720、480p = 854×480、360p = 640×360（media/directives.go:13-20）。
- `--codec <名>`：目标编码与源相同 → 不强制重编（矩阵说 remux 就还是 remux）；不同 → 强制 `full`。
- 强制 `full` 只对有视频流的输入生效（media/media.go:98-100）。
- 重编时源旋转会被 transpose 烤入（media/directives.go:81）。

几种典型输入的结果：

| 输入 | 输出 | 旗标 | `mode` |
|---|---|---|---|
| h264 + aac | .mkv / .mp4 / .mov | — | `remux` |
| hevc + opus | .mp4 | — | `audio-only`（hevc 拷贝，opus → aac） |
| hevc | .webm | — | `full`（hevc → vp9） |
| 带 90° 旋转的 h264 + aac | .mkv | — | `remux`（矩阵保留） |
| 任意 | .mp3 / .m4a / .flac / .opus / .wav | — | `audio-only-output`（视频丢弃） |
| 任意 | .avi / .ts / 大写 `.MP4` 等 | — | `full`（h264 + aac 重编进该容器） |
| h264 | .mp4 | `--codec hevc` | `full` |
| h264 | .mp4 | `--codec h264` | 不强制；矩阵怎么判就怎么走 |
| 3840×2160 h264 | .mp4 | `--screen 1080p` | `full`，缩到 1920×1080 |
| 1280×720 h264 | .mp4 | `--screen 1080p` | 源在框内：不缩放、不强制 |
| 内嵌 srt 字幕的 mkv | .mkv | — | 字幕保轨 |
| 内嵌 srt 字幕的 mkv | .mp4 | — | 字幕丢弃 + stderr 告警 |

**字幕轨**：容器兼容表只有三行——mkv 全兼容、mp4 只认 mov_text、webm 只认 webvtt。不兼容的字幕轨丢弃，**必告警**（stderr `跳过字幕流 #N（…）`）。文本字幕互转（srt → mov_text）没有做：引擎里留了 `isTextSubtitle` / `subtitleTargetFor` / `subtitleCodecIDByName` 三个表，但计划层从不产生「字幕重编码」这个动作（media/engine_cgo.go:858-881）。

#### 4.1.3 compress

流程（cli/media/media.go:142-248）：

1. 解析旗标与唯一的位置参数，转绝对路径，取输入文件大小。
2. 带了 `--preset`：查预设；没给 `--target-size` 时目标文本取预设的 `max_size_bytes`。
3. 目标文本仍为空 → `50%`。输出为空 → 同目录 `<基名>.mp4`。
4. `ResolveTargetBytes` 把目标文本换算成字节数。
5. `--screen` 与 `--preset` 互斥检查。
6. 带预设 → `CompressPreset`（§4.1.5）；否则 `CompressWithOptions`：公式计划 → 两遍编码。
7. 引擎报错：删输出路径、输出 error JSON。成功：输出结果 JSON，带 `target_bytes`。

**`--target-size` 的写法**（`ResolveTargetBytes`，media/plan.go:89-126）。大小写不敏感，首尾空白忽略：

| 写法 | 含义 | 例 |
|---|---|---|
| `<N>%` | 原始大小的百分比，必须大于 0 且不超过 100 | `50%` |
| 模板名 | 介质标称容量，十进制精确字节 | `cd` = 700,000,000 |
| `<N>` 或 `<N>b` | 字节 | `65536` |
| `<N>kb` / `<N>k` | × 1024 | `64KB` = 65,536 |
| `<N>mb` / `<N>m` | × 1024² | `100MB` = 104,857,600 |
| `<N>gb` / `<N>g` | × 1024³ | |

`N` 必须是正整数，`1.5GB` 这种小数写法报错。介质模板（media/plan.go:129-138）只有 8 个：

| 模板 | 字节 | 模板 | 字节 |
|---|---|---|---|
| `gmail` | 25,000,000 | `dvd` | 4,700,000,000 |
| `cd650` | 650,000,000 | `dvd9` | 8,500,000,000 |
| `cd` / `vcd` | 700,000,000 | `bd` | 25,000,000,000 |
| | | `bd50` | 50,000,000,000 |

分卷功能认的 `fat32` / `dvd5` / `bd25` 这里**不认**（两边的模板表是各写各的，§9.2 第 22 条）。

**码率公式**（`PlanCompression`，media/plan.go:34-85）。三个常数：容器开销余量 1%（即乘 0.99）、音频上限 128,000 bps、下限系数 10%。

```
音频动作
  源音频码率 > 128k        → 降到 128k           （down-to-128k）
  源音频码率 ≤ 128k 且 > 0 → 保留原码率          （copy-or-keep）
  没有音频流或码率未知     → 音频码率记 0         （silent）

纯音频输入
  音频码率 = 目标 × 8 ÷ 时长 × 0.99     视频码率 0，不检查下限   （audio-only-allocate）

视频输入
  音频字节 = 音频码率 × 时长 ÷ 8
  下限     = 原始 × 10% + 音频字节           目标 < 下限 → 报错
  总码率   = 目标 × 8 ÷ 时长 × 0.99
  视频码率 = 总码率 − 音频码率                低于 32,000 时取 32,000
```

例：10,345,081 字节、10 秒、音频 128k，目标 3,103,524 字节 → 总码率 2,457,991，视频 2,329,991 bps。

**两遍编码**（`compressWithSample`，media/media.go:130-180）：

1. **第一遍**：用同样的参数把**整个输入**编码一遍，输出到系统临时目录的 `zpack-sample-*.mp4`，这一遍完全静默，做完即删（media/media.go:183-230）。
2. **实测码率** = （第一遍产出字节 − min(源音频码率, 128k) × 时长（截去小数）÷ 8）× 8 ÷ 时长；没有音频流时音频那项按 0 算。
3. **校正**：校正码率 = 请求码率 × （请求码率 ÷ 实测码率）。实测等于请求时不校正。
4. **第二遍**：用校正后的码率再完整编码一遍，写出正式输出。

跳过第一遍的情形：没有视频流、时长不足 2 秒、第一遍编码失败——这时记一条 `采样逼近失败（…）——退回单遍直接编码`，直接按公式码率编码。

代码注释写的是「编 10% 样本」，但引擎不支持 seek，实际是**整片编码**（media/media.go:202-204 的注释自己也承认了，改用「全片产出 ÷ 全片时长」反推码率）。所以压缩一个视频 = 两次完整编码，耗时约为单遍的两倍；第一遍的临时文件落在系统临时目录，Windows 上通常是 C 盘，大小约等于压缩结果，没有 `--temp-dir` 可改。

结果 JSON 里 `video.bitrate` 是校正后的码率，`target_bytes` 是换算出的目标字节数。

关于命中精度：文档记录的是单遍编码目标偏差实测 ±5%，容易压的内容欠靶更大（AGENTS.md:169）；代码注释声称采样逼近能命中 ±2%（media/media.go:124）。±2% 这个数字没有找到对应的验证记录，门禁对压缩结果的断言区间也很宽（目标 40%，落在 20% 到 60% 之间就算过，verify/verify-media.sh:65）。

#### 4.1.4 info

流程（cli/media/media.go:756-794）：探测 → `BuildInfoReport`（media/info.go:78-108）→ 命令面按「存储尺寸 × SAR，±90° 对调」算出显示尺寸 → 输出。显示尺寸的算式在命令面和引擎里各写了一份（cli/media/media.go:774-784 与 media/directives.go:93-105），改一处要同时改另一处。`rotation` 恒出，无旋转为 `0`——缺失字段的歧义比冗余糟。

**压缩建议的规则**（`AnalyzeCompression`，media/info.go:115-188）。全部是确定性的纯函数，可单测：

| `kind` | `order` | `flag` | 触发条件 | `estimated_ratio` |
|---|---|---|---|---|
| `bakeable` | 1 | — | `bakeable` 非空 | 0 |
| `aspect-ratio` | 1 | — | SAR 不是方像素 | 0 |
| `resolution` | 2 | `--screen 1080p` | 视频流的**存储高度** > 1080，且塞进 1920×1080 的框算得出缩放目标 | 缩放后像素数 ÷ 原像素数 |
| `bitrate-inflated` | 2 | — | 码率密度 > 0.25 bpp（码率 ÷ 像素数 ÷ 帧率） | 0 |
| `audio` | 2 | — | 音频码率 > 128k | 128k ÷ 音频码率（只在纯音频输入时填，否则 0） |
| `codec-generation` | 3 | `--codec hevc` | 源编码的代际 ≤ 1：mpeg2video、mpeg4、vp8、h264，以及**代际表里没有的名字**（按 0 算） | 0.55 |

同 `order` 的几条保持插入顺序，整体按 `order` 稳定排序：先烤（bakeable、aspect-ratio）→ 几何与码率（resolution、bitrate-inflated、audio）→ 换编码（codec-generation）。纯音频输入最多出一条 `audio`：没有视频流就不会有 `bakeable`，出完 `audio` 就返回。

`resolution` 这条按**存储方向**判断：竖拍 1080×1920 的视频存储高度是 1920，也会触发，建议的落点是 606×1080（把竖屏塞进了横框）。`--screen 1080p` 的实际行为与这条建议一致（§4.2）。

**`bakeable` 的规则**（`BakeableKinds`，media/bake.go:19-35），与 `media bake` 共用同一份判定：

| 种类 | 判定 | 烤入手段 | 性质 |
|---|---|---|---|
| `rotation` | 旋转角度 ≠ 0 | transpose 像素重排 | 无损 |
| `sar` | SAR 不是方像素 | 缩放到显示尺寸 + `setsar=1` | 重采样 |
| `vfr` | r_frame_rate 与平均帧率相差超过 1% | `fps=<平均帧率>` | 有损（丢帧或补帧） |
| `hdr` | 传输特性是 PQ（smpte2084）或 HLG（arib-std-b67），或色彩空间是 bt2020nc / bt2020c | `colorspace=bt709:iall=bt2020:fast=1,format=yuv420p` | 有损（色域压缩） |

`hdr` 的滤镜段里**没有 tonemap**：注释写的是「zscale 归一 bt709 + tonemap（hable）」，实际只有 colorspace 一个滤镜（media/bake_exec.go:22-29；zscale 滤镜在产线里也没有，AGENTS.md:332）。

#### 4.1.5 preset

预设库是内嵌的 `media/presets.json`（`go:embed`），纯 Go，纯构建下照样可用。改 JSON 就是加档位，社区共建走 PR。文件解析失败会 panic（media/preset.go:34-40）。

起步 8 档（media/presets.json）：

| ID | 名称 | 分组 | 容器 | 适配框 | video_bitrate | audio_bitrate | max_size_bytes | 说明 |
|---|---|---|---|---|---|---|---|---|
| `youtube` | YouTube 高清上传 | social | mp4 | 1920×1080 | 8,000,000 | 128,000 | 0 | YouTube 官方推荐 1080p H.264 8Mbps（SDR 30fps 档） |
| `bilibili` | B站 1080P 投稿 | social | mp4 | 1920×1080 | 8,000,000 | 128,000 | 0 | B站投稿工具推荐 1080P H.264 ≤8Mbps AAC |
| `douyin` | 抖音竖屏上传 | social | mp4 | 1080×1920 | 8,000,000 | 128,000 | 0 | 抖音推荐 1080x1920 竖屏 ≤8Mbps；横屏源按比例适配入竖框（v1 不裁切） |
| `gmail` | Gmail 附件可发 | messaging | mp4 | 1920×1080 | 0 | 128,000 | 25,000,000 | Gmail 附件上限 25MB——源已达标则直拷贝 |
| `discord` | Discord 可直接发送 | messaging | mp4 | 1920×1080 | 0 | 128,000 | 25,000,000 | Discord 免费档上传 25MB（Nitro 为更大） |
| `wechat` | 微信可发送 | messaging | mp4 | 1920×1080 | 0 | 128,000 | 25,000,000 | 微信视频消息保守 25MB 档（文件通道上限更高） |
| `iphone` | iPhone 相册优化 | apple | mp4 | 1920×1080 | 6,000,000 | 128,000 | 0 | HEVC/H.264 1080p 档（iPhone 拍摄常见规格） |
| `appletv` | Apple TV 高清 | apple | mp4 | 1920×1080 | 8,000,000 | 128,000 | 0 | Apple TV 官方支持 H.264/HEVC 1080p |

`media preset` 不带 ID 出按组分类的列表（组名、ID 各自按字典序），带 ID 出详情（§3.4）。

**convert `--preset <ID>` 的施加规则**（`ConvertPreset`，media/preset.go:119-144）：

1. 输出扩展名必须等于预设的容器（8 档全是 mp4），否则报错。扩展名取路径里最后一个 `.` 之后的部分，不转小写，也不管那个点是不是落在目录名里（media/preset.go:172-188）。
2. 决策矩阵照常跑，按预设的容器算。
3. 适配框：显示尺寸超出预设的 width × height → 等比缩入 → 强制全编。不超出不缩放。
4. 码率上限「源码率超过上限的 1.1 倍就强制重编」这条规则引擎里有（media/preset.go:94-96），但**命令面在调用前把预设的 `video_bitrate` 清零了**（cli/media/media.go:125），从命令行走不到。被缩放的源，视频码率跟随源。
5. 预设的 `audio_bitrate`（128k）只在音频本来就要重编时生效。
6. `--screen` 互斥；`--codec` 被静默丢弃（命令面没有把它传给 `ConvertPreset`）。
7. 结果 JSON 带 `"preset":"<ID>"`。

**compress `--preset <ID>` 的施加规则**（`CompressPreset`，media/preset.go:147-170）：

1. 预设必须有 `max_size_bytes`，只有 messaging 三档有，其它档报「未定义大小上限」。
2. 目标体积 = 预设的 `max_size_bytes`。同时给了 `--target-size` 时，引擎仍然按预设上限压，但 JSON 的 `target_bytes` 报的是 `--target-size` 换算出来的值，两者不一致。
3. **源文件大小 ≤ 上限 → 直拷贝**（remux），`mode:"remux"`、`already_fit:true`，不重编——业界主流的「无损」语义，不浪费画质。只看体积，不看源是否满足预设的适配框和码率。
4. 否则压到上限，走的是**不带任何指令的标准压缩公式**（media/preset.go:169）：预设的适配框、`video_bitrate`、`audio_bitrate` 都不参与。
5. 缺省输出名与预设无关。
6. `--screen` 互斥；`--codec` 被静默丢弃。

#### 4.1.6 几何变换：rotate-left / rotate-right / rotate-180 / mirror / flip

五种变换与它们的烤入滤镜（media/transform.go:7-40）：

| 子命令 | 含义 | 烤入滤镜 | 交换宽高 | 顺时针角度增量 |
|---|---|---|---|---|
| `rotate-left` | 逆时针 90° | `transpose=2` | 是 | −90 |
| `rotate-right` | 顺时针 90° | `transpose=1` | 是 | +90 |
| `rotate-180` | 180° | `transpose=1,transpose=1` | 否 | +180 |
| `mirror` | 左右翻（水平镜像） | `hflip` | 否 | — |
| `flip` | 上下翻（垂直镜像） | `vflip` | 否 | — |

流程（`Transform`，media/transform.go:94-139）：

1. 探测。没有视频流 → 报错。输出扩展名不认识、或是音频容器 → 报「变换命令需要视频容器输出（mp4/mkv/webm）」。
2. 定计划（`PlanTransform`，media/transform.go:73-91）。`mirror` / `flip` 恒烤入。旋转族要**四个条件同时成立**才走零损矩阵：没带 `--bake`、源没有旋转、视频编码与输出容器兼容、输出容器认识。缺一条就烤入。
3. **零损矩阵路径**：remux，并给输出的视频流追加一个 display-matrix，角度 = 源旋转 + 命令增量（走到这条路的源旋转必为 0，所以就是增量）。编码字节不变，秒级完成。`mode` = `transform-matrix`。
4. **烤入路径**：全编。旋转族烤的是**净角度** = 源旋转 + 命令增量，归一到 (−180, 180]（`NetMatrixAngle`，media/transform.go:48-61）——源的旋转矩阵在重编时会丢，必须和命令增量一起烤实，否则增量方向就错了。`mirror` / `flip` 是「源旋转烤实 + hflip / vflip」两段滤镜（设定的先后与实际链序的差异见 §4.1.1 和 §9.3 第 39 条）。`mode` = `transform-bake`。

源已有矩阵时只能烤入：矩阵路径用的是 `DisplayMatrix().Add(...)`，只追加不替换（media/transform.go:89）。`mirror` / `flip` 不走矩阵，是因为矩阵镜像在各播放器里的支持不可靠。

烤入路径的视频**目标编码是容器默认**（mp4 / mkv → h264，webm → vp9），不是源编码；码率跟随源。所以 hevc 的源做一次 `mirror`，出来是 h264。

#### 4.1.7 bake

`media bake <输入> <输出>` 是「把显示元数据物理化」的统一入口（2026-11-07）。原来的 `convert --bake` 已退休，旗标现在被静默忽略。

流程（`Bake`，media/bake.go:91-133）：

1. 探测。没有视频流 → 报错。输出是音频容器 → 报错。不检查输出扩展名是否认识。
2. 定计划（`PlanBake`，media/bake.go:62-88）：可烤种类 = §4.1.4 的 `bakeable` 四种。含 `sar` 时缩放目标取显示尺寸（存储 × SAR，±90° 对调）。含 `vfr` / `hdr` 时目标帧率取平均帧率，并生成有损滤镜段（`fps=…` 在前、`colorspace=…` 在后，media/bake_exec.go:11-32）。
3. **无可烤** → remux 直拷，`mode:"bake"`、`baked:[]`。幂等：再烤一次也是这样。
4. **有可烤** → 全编：源旋转烤入（transpose），有 `sar` 再缩放并归一方像素，有 `vfr` / `hdr` 再接有损滤镜。视频码率跟随源，音频按 `full` 模式的规则处理。`baked` 是实际烤入的种类列表。

bake 与 `rotate --bake` 的分工：bake 是把**源已有**的显示元数据烤实，rotate 是**施加新的**几何变换。

#### 4.1.8 screenshot

流程（cli/media/media.go:251-416）：解析 → 输入存在检查 → 探测时长 → 按优先级定模式。五种模式，先命中的生效：`--time` > `--every` > `--count` > `--times` > 缺省。

| 模式 | 截哪些时间点 | 输出 |
|---|---|---|
| `--time 秒` | 这一个点。必须小于视频时长 | `--output`；缺省 `<基名>_shot.<format>` |
| `--every 间隔` | 间隔、2 × 间隔、3 × 间隔……不含 0 秒，小于时长。一个点都算不出就报错 | `--output-dir` 下 `<基名>_<序号>.<format>` |
| `--count N` | 0、时长 × 1/N、……、时长 × (N−1)/N（含 0 秒那帧，不含末尾）；N = 1 时取中间帧 | 同上 |
| `--times a,b,c` | 给定的点 | 同上 |
| 缺省 | 时长 ÷ 2 | 同 `--time` |

- 时间写法是非负数，可以带 `s` 后缀。
- 引擎（`decodeFrameAt`，media/screenshot.go:72-163）：打开 → 向前 seek 到目标时间之前最近的关键帧 → 解码 → 把**收到的第一帧**转成 RGBA → 交给 image 包写图。它不比对帧的时间戳，所以按代码看，截到的画面是目标时间之前最近的一个关键帧，而不是目标时刻那一帧（未逐帧实测）。
- 单帧的图片格式跟**输出路径的扩展名**走，`--format` 只参与缺省文件名；输出路径没有扩展名时补 `.jpg`。多帧才按 `--format` 写。jpg 质量 92，png 最佳压缩，webp 是无损 VP8L（image/image.go:88-120）。
- 多帧时某一帧失败就跳过它，`count` 只数成功的；文件名序号从 1 起，位数按请求的总帧数对齐（个位 `_1`、十位 `_01`、百位 `_001`），这样按文件名排序天然正确（media/screenshot.go:43-69）。
- 不处理旋转：竖拍视频截出来的图是存储方向（躺倒的），`width` / `height` 也是存储尺寸。

#### 4.1.9 cover

`media cover <视频> <图片> [输出]`（`AddCover`，media/cover.go:23-167）：

1. 探测，定封面流的尺寸：视频文件取视频的**存储**尺寸；纯音频文件取封面图的原始尺寸，读不出就兜底 500×500（专辑封面没有「视频尺寸」这个概念）。
2. 读封面图（`image.ReadImage`，不做 EXIF 定向），画到一张目标尺寸的 RGBA 画布上，编码成 JPEG（质量 92）。**这一步没有缩放**：`draw.Draw` 是把原图按原尺寸拷到画布左上角，小图右侧和下方留黑，大图被裁（media/cover.go:52-53）。
3. remux：拷贝全部输入流，再追加一条 MJPEG 视频流（yuvj420p，时基 1/25），标上 attached_pic。
4. 写头 → 先写封面包 → 拷贝全部输入包 → 写尾。视频流零重编，remux 速度。

#### 4.1.10 insert-frame

`media insert-frame <视频> <图片> [输出] [--front|--back]`（`InsertFrame` + `mergeWithFrame`，media/insertframe.go:21-53、474-750）。在开头或末尾加一帧**看得见**的封面画面，播放时长多一帧。全程重编码，零临时文件。

1. 探测，没有视频流报错。读封面图（不做 EXIF 定向），画到视频存储尺寸的画布上——同样没有缩放。
2. 封面帧 RGBA → YUV420P（BT.601 手算，media/insertframe.go:416-471）。
3. 编码器与源视频同一种编码、存储尺寸、源流时基、源流码率。`--front` 时封面帧先送进编码器，`--back` 时等原视频帧都送完再送。封面帧和正片走**同一个编码会话**，SPS/PPS 天然一致——这是不搞「单帧编码包插入」的原因（media/insertframe.go:3-5）。
4. 原视频：解码 → 像素格式归一 → 编码，PTS 按「帧序号 ×（时基分母 ÷ 平均帧率）」重排（帧率取不到按 30）。音频流原样拷贝。字幕轨不映射，丢弃。
5. 冲洗编码器，写尾。

文件里还留着一套两步走的旧实现（先把封面渲染成单帧视频文件、再合并，`renderSingleFrameVideo` / `mergeVideos`），已经没有调用方。

#### 4.1.11 clip

`media clip <视频> [输出] --seconds-from 起 --seconds-to 止`，或 `--frame-from N --frame-to M`（cli/media/media.go:502-588、media/clip.go:17-42）。

1. **秒模式**：直接用两个秒数。**帧号模式**：任一帧号旗标给了有效值就进入。`frame-from` 缺省 0，`frame-to` 缺省 `from + 1`；先探测出平均帧率（取不到按 30），秒数 = 帧号 ÷ 帧率。所以帧号模式会多探测一次。
2. 区间必须满足 `0 ≤ from < to`，否则报用法。
3. 输出缺省 `<基名>_<起>-<止>s<原扩展名>`。
4. 引擎（`clipExact`，media/clip.go:149-342），恒帧精确、全编：
   - seek 到 from 之前最近的关键帧，丢掉时间早于 from 的包；包时间 ≥ to 就停。**区间左闭右开 `[from, to)`**——from 那帧含，to 那帧不含，和 Python 切片同一个意思，为的是相邻两段能无缝拼回去。
   - 结束时间超过视频时长时自动截至末尾，JSON 里报的是截断后的 `end_time_seconds`。
   - 视频：取最后一条视频流（循环没有 break，media/clip.go:163-171），重编码，编码与源相同、存储尺寸、源流码率。PTS 按「帧序号 × 帧时长」重排，而**帧时长是按 30fps 假设的**（media/clip.go:273），与源的真实帧率无关。
   - 音频：取最后一条音频流，落在 `[from, to]` 内的包原样拷贝（闭区间，与视频的左闭右开差一个端点），时戳不动。
   - 字幕轨丢弃。

「恒精确」是有意的定案：用户指明了时间或帧号，就是要精确；流拷贝只是内部优化，不是给用户的选项（media/clip.go:28）。文件头注释里写的「缺省流拷贝、`--exact` 全编」已过时，`clipStreamCopy`（media/clip.go:45-146）是没有调用方的遗留代码。

#### 4.1.12 merge

`media merge <视频…> <输出> [--reference 路径]`（`Merge`，media/merge.go:16-412）：

1. 至少 2 个输入。基准缺省是第一个输入；`--reference` 可以指定任意一个，但路径字符串必须与某个输入**完全相同**。
2. 探测基准。输出编码器与基准同一种编码、基准的存储尺寸、基准流时基、基准流码率。
3. **音频只从基准取**，原样拷贝；其它输入的音频直接忽略。
4. 逐个输入处理，全程同一个编码会话，零临时文件：基准自己只做像素格式归一；其它输入先 `scale=基准宽:基准高`——**拉伸，不保比例**——再做格式归一。PTS 按「帧序号 ×（时基分母 ÷ 基准平均帧率）」重排，帧率取不到按 30。所以帧率与基准不同的输入不会被重采样，只是被按基准的节奏重新排了时戳。
5. 字幕轨丢弃。

#### 4.1.13 speed

`media speed <视频> [输出] --factor 倍数`（`Speed`，media/speed.go:16-279）：

1. 校验倍数，探测，没有视频流报错。
2. 视频重编码：编码与源相同、存储尺寸、源流码率。
3. 变速的原理只有一处：帧间距 =（时基分母 ÷ 平均帧率，帧率取不到按 30）÷ 倍数，PTS = 帧序号 × 帧间距（media/speed.go:116-123、208）。快放时帧更密，慢放时更疏。
4. **音频被丢弃**。输出里声明了一条音频流（拷了源流的参数），但主循环里音频包直接扔掉，一个都不写（media/speed.go:257-260，注释写明是 MVP：atempo / asetrate 管线后续接入，旧的「时戳缩放」方案因为音画脱同步已移除）。
5. `--pitch-match` 对应的 `pitchMode` 参数，在函数体里没有任何引用。

所以现状是：`media speed` 的产物**没有声音**，两种音调模式都没有区别；用法文案里的「缺省保音调」不成立。JSON 的 `output_duration_seconds` 是「原时长 ÷ 倍数」算出来的，不是从产物量出来的。

#### 4.1.14 ffmpeg 透明代理

`zpack ffmpeg <原版任意参数...>`（cli/media/ffmpeg.go:18-39）。命令面关掉了旗标解析，原样把参数交给内置的 ffmpeg CLI。

1. `--help` / `-h` 被 zpack 的 help 守卫截获，打印的是 zpack 这一侧的帮助（Short + Usage），exit 0。要看 ffmpeg 自己的帮助用 `-help`（实测可透传）。
2. 一个参数都不给 → JSON 用法错误，exit 1。
3. 纯构建 → JSON 错误「zpack ffmpeg 需完整版（CGO 构建 + 静态产线）」，exit 1。
4. 完整版 → `ffmpegcli.Run(args)`（internal/ffmpegcli/cli_cgo.go:28-40）：
   - 先把 stdout 钉成行缓冲。cgo 环境下 C 的 `exit()` 不会 flush stdio，终端里看着正常，接了管道或重定向就丢输出。
   - 构造 C 风格的 argv：`argv[0]` = `"ffmpeg"` 占位（ffmpeg 的 usage 和报错会引用它），实参从 `argv[1]` 起，argc 含程序名，末位 NULL。
   - 调 `C.zpack_ffmpeg_cli_main(argc, argv)`。ffmpeg 的 main 在这里就是进程内的一个普通 C 函数，正常情况下它在结尾 `exit()`，直接终结进程，不会返回。
   - 万一返回了，`os.Exit(返回值)`——ffmpeg 在失败路径上会返回负值，照样当退出码传出去。
5. stderr 的第一行仍是 zpack 的统一开始日志。

**契约例外**只在这一步成立：stdout 是 ffmpeg 的原生输出（可以是二进制媒体流，`-f mp4 pipe:1` 这类用法必需），stderr 是 ffmpeg 的日志，退出码是 ffmpeg 的退出码。前三步仍然是 JSON 信封。

实测（完整版 `40fb9e7-full`，Windows）：`zpack ffmpeg -version` 的 stdout 以 `ffmpeg version 9.0 Copyright (c) 2000-2026 the FFmpeg developers` 开头、以 `Exiting with exit code 0` 结尾，exit 0；stderr 只有开始日志一行。没有 `zpack ffprobe`，探测用 `media info` 或 `zpack ffmpeg -i <文件>`。

### 4.2 边界与退化输入

标「实测」的都在 `bin/zpack-dev.exe`（纯构建）或 `bin/zpack-cli.exe`（完整版）上跑过；标「按代码」的是读代码得出的，没有实跑。

| 情形 | 行为 |
|---|---|
| 任何子命令参数个数不对 | error JSON + exit 1，文案见 §3.6（实测） |
| 子命令后跟 `--help` / `-h` | 打印 media 父命令的帮助，exit 0（实测） |
| 输入文件不存在 | `路径不存在：…`，exit 1（实测） |
| 输入不是媒体文件 | `无法读取输入: 打开输入失败: Invalid data found when processing input`，exit 1（实测完整版） |
| `media clip --frame-from 0 --frame-to 10`（给了帧号但没给视频路径） | **panic**：`index out of range [0] with length 0`，stderr 是 Go 堆栈，stdout 为空，exit 2（实测）。帧号分支在读输入路径时还没检查有没有位置参数（cli/media/media.go:554 先于 566） |
| `media screenshot <文件> --every 0` | **exit 0 且 stdout 为空**（实测完整版）。间隔为 0 时直接 `return`，既没出错也没有最终输出（cli/media/media.go:362-365） |
| `media screenshot` 的 `--count` 不是正整数（`abc`、`0`、`-3`） | 当作没给，落到后面的模式或缺省的中间帧（按代码） |
| `media clip` 的 `--seconds-from abc` | 当作没给，起点回落到 0，照常截取（实测：CLI 放行，直到引擎才因别的原因报错） |
| `--screen=1080p` 这类 `=` 写法 | 整个被当成未知旗标忽略，等于没给（按代码） |
| `convert --bake`、`clip --exact` | 不存在或已退休，被静默忽略（按代码） |
| 输出文件已存在 | 直接覆盖，没有提示（按代码） |
| `media compress x.mp4`（.mp4 输入、不带 `--output`） | 缺省输出名与输入**是同一个文件**。就地改写：引擎先写旁边的临时文件（<名>.zpack-inplace<扩展名>），成功后原子替换、失败则原文件不动（2026-11-24 修复，见 6.3「就地改写毁源文件」） |
| `convert` 的输出扩展名不认识（`.avi`、`.ts`、大写 `.MP4`） | 按未知容器走全编，编码 h264 + aac，容器由 libavformat 按扩展名猜（按代码） |
| `convert` 的输出是 `.wav` | 恒走抽音轨，出 pcm_s16le（按代码） |
| 竖拍视频（存储 1080×1920）配 `--screen 1080p` | 适配框是横的 1920×1080，会被缩到 606×1080（按代码） |
| `convert --preset` 的输出扩展名不是预设容器、或大小写不同（`.MP4`） | 报「输出扩展名 … 不符」（按代码） |
| `compress --preset youtube`（没有上限的档） | 报「未定义大小上限」（实测） |
| `--target-size dvd5` / `fat32` / `bd25` | 不认，报「需百分比（50%）、尺寸（100MB）或模板（cd/dvd/bd50）」（实测） |
| `--target-size 150%`、`1.5GB`、`999zz` | 报错（实测） |
| 目标体积低于下限（原始 × 10% + 音频） | 报错，exit 1（门禁覆盖） |
| 纯音频输入做 `compress` | 可用，码率全给音频，不检查下限（门禁覆盖） |
| 纯音频输入做 `bake` / 变换命令 | 报错，exit 1（门禁覆盖） |
| 视频不足 2 秒做 `compress` | 跳过第一遍，直接按公式码率编码，stderr 有一条「采样逼近失败」（按代码） |
| `--screen` 与源尺寸相同或更小、`--codec` 与源编码相同 | 不缩放、不强制重编，矩阵怎么判就怎么走（单测覆盖） |
| `--time` 超出时长、`--times` 里有非法值 | 报错，exit 1（实测） |
| `screenshot --format` 给了 image 包不认的值 | 单帧：`写入截图失败: 不支持的输出格式: …`，exit 1。多帧：每帧都失败、被跳过，**exit 0 且 `count:0`**，输出目录里留下一批 0 字节文件（按代码） |
| `media info` 多给了位置参数 | 报用法，exit 1（实测） |
| `media preset <不存在的 ID>` | `未知预设: …`，exit 1（实测） |
| `zpack ffmpeg`（无参数） | JSON 用法错误，exit 1（实测） |
| `zpack ffmpeg --help` / `-h` | zpack 侧帮助，exit 0（实测） |
| 未知顶层命令（如 `zpack nosuchsub`） | cobra 的原文 `Error: unknown command …`，**不是 JSON**，exit 1（实测） |

### 4.3 错误处理

- **半成品的清理靠命令面**。引擎返回 error 时，命令面先 `os.Remove(输出路径)` 再输出 error JSON（cli/media/media.go:130-133 等每处引擎调用的后面）。引擎自己只负责删 compress 第一遍的临时文件（`defer os.Remove`，media/media.go:200）。
- **这个删除是无条件的**：只要引擎报错就删，不问输出路径上原来是什么。校验类的错误（`--screen` 非法、目标低于下限）发生在任何写入之前，也会把输出路径上用户原有的文件删掉。换句话说，输出路径一旦写进命令，就归这条命令处置了。
- **主循环中途的读写错误不会让命令失败**。统一执行器的主循环里，读包、写包、送包、滤镜编码出错都只是记一条 stderr 日志然后跳出循环，接着照常冲洗、写尾、stat 输出大小，返回成功（media/engine_cgo.go:413-459、683-706）。按代码看，中途坏掉的源文件会得到一个截断的产物和一个 `is_success:true`。只有冲洗阶段（`flushWorker`）的错误才会作为 error 返回。**未实测**。
- **compress 第一遍失败不算失败**：记一条「采样逼近失败…退回单遍直接编码」，按公式码率继续。
- **多帧截图是逐帧容错的**：一帧失败就跳过，结果里没有它；全部失败也是 exit 0。
- **没有取消**：第一次 Ctrl+C 不中断转码；第二次 exit 130，不跑任何清理，输出路径上留下半成品。
- **panic 不被兜底**：§4.2 里 clip 的那个越界就是，退出码 2、stdout 为空，直接破坏输出契约。

## 5. 平台差异

命令面、决策层、JSON 契约在三个平台上完全一样；差别全在完整版怎么构建出来。

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 完整版产线 | `build_cli/windows`：本机原生 MinGW（MSYS2 / UCRT64 gcc），`build.sh` 总入口 + `deps_static.sh` + `office_static.sh`。产物 `bin/zpack-cli.exe`，版本串带 `-full` 后缀（build_cli/windows/build.sh:65） | `build_cli/mac`：`build.sh`，缺配料时自动触发 `ffmpeg_static.sh` 源码级全静态。产物 `bin/zpack-cli` | **没有产线**。`build_cli/linux` 里只有两个占位脚本，正文是一句「⏳ 产线待首跑验证」。Linux 上只有纯 Go 可用，media 族全部报「需完整版」 |
| ffmpeg CLI 的 main 改名 | `objcopy --redefine-sym main=zpack_ffmpeg_cli_main`，在目标文件层改。`-Dmain=` 在这里不管用：fftools/cmdutils.h 的 `_WIN32` 分支把它 `#undef` 掉了（AGENTS.md:301-302） | 编译宏 `-Dmain=zpack_ffmpeg_cli_main`（build_cli/mac/ffmpeg_static.sh:145） | — |
| Windows 独有的两处处理 | ① 产线 sed 删掉 cmdutils.c 里 `prepare_app_arguments` 的两处调用，否则 ffmpeg 会用 `GetCommandLineW()` 重取宿主 zpack.exe 的命令行（AGENTS.md:303）。② 链接时 zlib 钉静态：MinGW ld 默认优先 `libz.dll.a`，会把 MSYS2 的 `libz-1.dll` 带进 import 表，违反「仅出厂 DLL」铁律（internal/ffmpegcli/linkflags_windows.go:3-9） | 无 | — |
| pthread | `-extldflags '-Wl,-Bstatic -lpthread -Wl,-Bdynamic'` 钉在链接行末尾：静态 libstdc++ 自己引用 pthread_*，解铃的库必须排在它后面（build_cli/windows/build.sh:63-65） | 直接 `-lpthread`（linkflags_other.go） | — |
| 硬件编码 | 没有 VideoToolbox；d3d11va / dxva2 硬解默认内置。media 引擎不使用硬件编码器 | 产线开了 `--enable-videotoolbox`，但 media 引擎取的是 `avcodec_find_encoder` 的默认结果，不会选它；只有 `zpack ffmpeg -c:v h264_videotoolbox` 这类写法用得上 | — |
| compress 第一遍的临时文件 | `%TEMP%`，通常在 C 盘 | `$TMPDIR` | — |
| JSON 里的路径 | 反斜杠的绝对路径（JSON 里转义成 `\\`） | 正斜杠 | 正斜杠 |
| 媒体门禁 `verify/verify-media.sh` | 脚本是按 mac 写的：`stat -f%z`、`/tmp`、office 分节依赖 `textutil`。AGENTS.md 里没有它在 Windows 或 Linux 上跑通的记录（Windows 的记录只到 `verify.sh`） | 原生可跑，另需系统 `ffmpeg` 造夹具 | 同左 |

**产物定案**（2026-11-22 用户拍板，AGENTS.md:188-191）：`bin/zpack-cli` 与装机产物**恒为完整版**；纯 Go 构建只是开发期快速编译循环的工具，不得作为交付或装机产物。

**纯 Go 构建与完整版在本功能上的差别**。引擎按 build tag 分层：纯 Go 部分是决策、计划、建议、预设（`go test ./...` 在纯构建下照常测得到），cgo 部分是探测与执行。纯构建下引擎调用全部换成返回明确指引的桩（media/stub_nocgo.go 与各 `*_nocgo.go`）。命令面自己的校验（输入存在、预设 ID、`--target-size` 解析、互斥）照常先跑，跑过了才撞到桩：

| 子命令 | 纯构建下的 `error_info`（都是 JSON 信封 + exit 1） |
|---|---|
| convert、compress、info、bake | `无法读取输入: 媒体功能需要完整版（build_cli 产线 CGO 构建）；纯 Go 版不含 libav 引擎` |
| rotate-left / right / 180、mirror、flip | `媒体功能需要完整版（build_cli 产线 CGO 构建）；纯 Go 版不含 libav 引擎`（不包「无法读取输入」） |
| screenshot | 同 convert（它先探测时长） |
| clip（秒模式） | `视频截取需要完整版（CGO 构建）` |
| clip（帧号模式） | 同 convert（它先探测帧率） |
| cover | `封面添加需要完整版（CGO 构建——remux 引擎）` |
| insert-frame | `帧插入需要完整版（CGO 构建——remux + 编码引擎）` |
| merge | `视频拼接需要完整版（CGO 构建）` |
| speed | `速度渲染需要完整版（CGO 构建）` |
| preset | **正常工作** |
| ffmpeg | `zpack ffmpeg 需完整版（CGO 构建 + 静态产线）` |

上表每一条都在 `bin/zpack-dev.exe` 上实测过。注意纯构建下 `zpack ffmpeg` 是走 JSON 信封的——契约例外只在 ffmpeg 真正跑起来之后才成立。

本机现有两个二进制：`bin/zpack-cli.exe` 是完整版（`zpack version 40fb9e7-full`，即本 spec 对应的提交），`bin/zpack-dev.exe` 是纯构建（`zpack version dev`，拆包之前编的，代码与现在一致）。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 为什么这样设计

- **决策层与执行层分开**（media/decide.go:1-5）。决策矩阵、压缩公式、建议规则、预设施加、几何变换的计划全是纯 Go 的纯函数，表驱动可单测（media/media_test.go）；cgo 只负责探测和执行。执行器只有一个，三种模式共用（remux = 全拷贝、audio-only = 拷视频编音频、full = 全编，media/engine_cgo.go:3-4）。
- **三级瀑布是业界主流优转的 SmartRender 判定矩阵的直译，压缩公式是它的公式族的直译**（2026-11-05，AGENTS.md:150-151）。设计来源是 `docs/unicoverter-reverse.md` 的 §5 与 §3——那是本地私藏的逆向研究文档，不入库（AGENTS.md:345）。公式里几个看似古怪的数（× 0.99、128k 阶梯、原始 × 10% 的下限）都是照原实现搬的，不是拍脑袋定的。
- **引擎是进程内的 libav（go-astiav v0.43.0），不是起 ffmpeg 子进程**。管线骨架改编自 go-astiav 官方的 transcoding 示例（Apache-2.0）和 `build_cli/poc` 里的概念验证（media/engine_cgo.go:5）。POC 在 2026-11-04 验证过这条路成立：请求 1.0Mbps 实得 1.037Mbps，偏差 3.7%；10.3MB 压到 1.45MB（build_cli/README.md:27-34）。
- **为什么还要一个 `zpack ffmpeg`**：zpack 没封装的长尾需求（特殊滤镜、滤镜链、协议源、设备采集、调试）透传即得 ffmpeg 的全部能力，不用等封装（cli/tools/skill.md §3.11）。它也是自举验证的工具——媒体门禁用捆绑的 ffmpeg 造 JXL 夹具（verify/verify-media.sh:333-357）。
- **ffmpeg CLI 为什么是进程内直调**（2026-11-09 用户定案，AGENTS.md:208-214）。旧方案是把整个 ffmpeg.exe（35MB）嵌进 zpack，运行时先释放成临时文件再起子进程，四宗罪：污染磁盘、**杀毒软件高概率误报**（运行时释放可执行文件是病毒的典型行为）、并发冲突（多实例抢同一个释放路径要加锁）、慢（每次写 35MB 外加起进程）。用户明确厌恶这个方案。现方案是编译期把 ffmpeg CLI 当普通库链进来，ffmpeg 的 main 就是进程里的一个普通 C 函数，运行时零释放、零子进程、零缓存目录。2026-11-09 在 Windows 上实测验证过零落盘（AGENTS.md:213）。
- **旋转不再否决直拷**（2026-11-05 的 v2 修正）。v1 的规则是「带旋转就不许直拷」（media/decide.go:44 的字段注释里还留着「一票否决直拷」的旧话），照搬的是逆向矩阵的原文。修正后 remux 时旋转矩阵随流参数原样保留，零损；只有重编路径才需要 transpose 烤入（media/decide.go:121-122、AGENTS.md:158）。
- **旋转族默认写 display-matrix**（2026-11-05）。源没有矩阵时旋转只是加一个侧数据，秒级完成、编码字节不变。两条路径的一致性用 PSNR 验证过（verify/verify-media.sh:180-183）。
- **bake 是一级命令**（2026-11-07）。「先烤再做别的」需要一个统一入口，把散在 `convert --bake` 和 `rotate --bake` 两处的物理化能力收拢起来，于是 `convert --bake` 退休（AGENTS.md:155、160）。clip / 缩放 / 换码都在存储坐标系上算，不先把旋转和像素比烤实，后面的几何就会错。
- **净角度**：烤入时必须烤「源旋转 + 命令增量」，因为源矩阵会随重编一起丢（media/transform.go:122-131）。
- **clip 恒帧精确**：用户指明了时间或帧号就是要精确的，流拷贝只是内部优化，不做成选项（media/clip.go:28）。区间取左闭右开，为的是相邻两段 `[0,5)` + `[5,10)` 能无缝拼回完整视频（cli/tools/skill.md §2）。
- **insert-frame 走同一个编码会话**：封面帧和正片一起过同一个编码器，SPS/PPS 天然一致；不搞「单帧编码包插入」，那样要手工对齐 SPS/PPS（media/insertframe.go:3-5）。
- **预设压缩源已达标就直拷贝**：业界主流的「无损」语义，不为了用预设而浪费画质（media/preset.go:158-168）。
- **info 的字段宁可冗余**：`rotation` 恒出（0 = 无旋转），`baked` 无可烤时是 `[]` 而不是省略——「字段缺失的歧义比冗余糟」。`baked` 为此用了指针加 `omitzero`：nil 指针省略、指向空切片的指针输出 `[]`（cli/media/media.go:788、849、959-969）。
- **建议是机器可执行的**：`suggestions[]` 每条带 `kind` / `order` / `flag`，flag 可以直接拼给 convert / compress。skill.md 里给 Agent 写的工作流就是「先 `media info` → 按 `order` 排序 → 拼旗标 → 回头看 `mode` 和 `ratio`」（AGENTS.md:154、cli/tools/skill.md §3.6）。
- **预设库是 JSON**：官方公开规格整理成一张表，社区共建就是直接改 JSON 提 PR；纯 Go，纯构建下也可用（media/preset.go:1-2）。
- **过程日志的去向按「本次调用的参数」定**，不放包级变量。命令面按功能拆包后各命令各管各的，而且一个进程只执行一条命令，全局状态本来就是多余的耦合（internal/cliutil/logf.go:16-26，提交 6d31586）。`mediaLogf(args)` 把 `cliutil.EngineLogf(args)` 的返回值适配成引擎的 `Logf` 类型：带 `--quiet` 得到 nil，引擎侧所有进度、提示、跳过明细静默（cli/media/media.go:883-885）。

### 6.2 退役和被否掉的方案

| 方案 | 结局 | 原因 |
|---|---|---|
| 嵌入整个 ffmpeg.exe，运行时释放成临时文件再起子进程（go:embed 嵌入版、尾注附二进制版都做过） | 2026-11-09 退役 | 污染磁盘、杀软误报、并发冲突、慢（AGENTS.md:210） |
| Windows 上原名 `main` 编进来，外面套一层转发包装 | 弃用 | MinGW 的 CRT 会捕获 main 并在初始化路径里调用，与 Go runtime 再入冲突：`fatal error: randinit twice` 加段错误。治本是 `objcopy` 在目标文件层改名（AGENTS.md:302） |
| Windows 产线走 zig cc 交叉编译 | 退役 | 改为本机原生 MinGW（AGENTS.md:294）。`build_cli/README.md:13` 还写着「⏳ zig cc」，已过时 |
| 完整版动态链接 brew 的 libav | 退役 | 改为源码级全静态，产物单文件可跑（AGENTS.md:152、204） |
| 主 go.mod 不引入 go-astiav、把 media 放独立 module 的预案 | 没有采用 | 实际做法是 build tag 分层，go.mod 里直接 require 了 go-astiav。`build_cli/README.md:38` 的说法已过时 |
| `speed` 的「音频时戳缩放」方案 | 已移除 | 音画脱同步。现状是音频直接丢弃，atempo / asetrate 管线还没接（media/speed.go:258） |
| clip 缺省流拷贝、`--exact` 才全编 | 改为恒精确 | 见 §6.1。`clipStreamCopy` 成了没有调用方的遗留代码（media/clip.go:44-146） |
| insert-frame 两步走：先把封面渲染成单帧视频文件，再与原视频合并 | 改为封面帧直送编码器 | 省掉临时文件。旧实现 `renderSingleFrameVideo` / `mergeVideos` 还留在文件里，没有调用方（media/insertframe.go:55-413） |
| `convert --bake` | 2026-11-07 退休 | 物理化统一走 `media bake`（media/media.go:52） |
| 真正的两遍编码（ffmpeg 的 stats_file / stats_in） | 没有做 | astiav v0.43 没有暴露这两个选项（AGENTS.md:169）。代码后来走了「两遍采样逼近」，但第一遍其实是整片编码，见 §9.1 第 3 条 |

### 6.3 踩过的坑，以及当初怎么定位、怎么修的

**ffmpeg 直调与产线**（编号接 AGENTS.md 的陷阱清单）：

- **#1 configure 找不到自建库**：ffmpeg 探测第三方库全靠 pkg-config，`PKG_CONFIG_PATH` 必须在 `./configure` 之前设好。
- **#7 cgo 构建缓存吃掉环境切换**：pkg-config 的结果进了 Go 构建缓存，却不随环境变量失效。从 brew 动态库切到静态前缀之后必须 `go clean -cache`，否则链的还是 dylib。两条产线的构建脚本都写死了这一步。
- **#8 fftools 的目标文件散在子目录里**（`graph/`、`textformat/`）：打包要用 `fftools/*.o fftools/*/*.o` 加排除法；排除模式必须锚定 `/(ffmpeg|ffprobe|ffplay)\.o$`，因为 `grep -v "ffmpeg.o"` 里的点号会连 `ffmpeg_opt.o` 一起误吞。
- **#9 C argv 惯例**：`argv[0]` 是程序名占位（ffmpeg 的 usage 和报错引用它），argc 含程序名，末位 NULL。传错了 ffmpeg 会当成零参数，只打 usage。
- **#10 cgo 下 C 的 `exit()` 不 flush stdio**：终端里看着正常，接了管道或重定向输出就没了。修法是调用 main 之前 `setvbuf(stdout, NULL, _IOLBF, 0)` 把 stdout 钉成行缓冲（internal/ffmpegcli/cli_cgo.go:29-30）。门禁里有专门的管道用例（verify/verify-media.sh:347-348）。
- **#11 ffmpeg 的 main 在失败路径上会返回负值**，而不是 exit。这和纯构建桩的语义冲突过：现在先过 `ffmpegcli.Available()` 探针分流，负值当成真实的失败码照传给 `os.Exit`（cli/media/ffmpeg.go:34-38）。
- **#15 / #16 / #17**：Windows 上 `-Dmain=` 失效、C main 入链撞 randinit、ffmpeg 无视传入的 argv——见 §5。#17 的补丁要写成幂等的，并且放在 prepare 主体里，不能放在解压分支里，否则嵌套事故时会被跳过（AGENTS.md:303）。
- **#18 GNU ld 对静态库严格按序**：libavcodec 引用 libiconv，后者必须排在它后面（mac 的 ld64 会扫两遍，没有这个问题）。要把 `-liconv` 注入 libavcodec.pc 的 Libs 行，configure 的 `--extra-libs` 只进 `Libs.private`，不生效。
- **#19 pkg-config 会把 `-Wl,*` 重排到行尾**：用 `Bstatic` 包裹的手法对 .pc 文件无效。MSYS2 的运行时库一律换成 `.a` 的绝对路径；winpthread 特殊，得用 `-extldflags` 钉到链接行末尾。
- **#33 ffmpeg 9 的 pc 生成器变了**：依赖闭包直接散装写进 Libs（8.x 走 `Requires.private`），加上 cgo 展开时的重排，jxl 的符号靠一个合并后的兜底库紧跟在 `-lav*` 后面注入。
- **#34 MSYS2 的 nasm 3.02 没有 `-Ox` 旗标**（2026-11-22 首次在实测机上跑时踩到）：libaom 的 cmake 脚本用字符串匹配探测 `-Ox`，必失败，configure 直接中止。修法是给 aom 的 cmake 脚本打补丁，改成真实汇编探测（提交 a1c91e1，AGENTS.md:320-321）。
- **#35 `build.sh deps` 恒以 exit 1 结束**（2026-11-24 查明）：`case` 里漏了 `deps` 分支，云端产线从来没有跑到过 cgo 阶段。已修（AGENTS.md:322）。
- **ffmpeg 的 tarball 下载通道在代理环境下堵死**：ffmpeg.org TLS 中断，GitHub codeload 传输停滞，但 git 协议通。对策是多源回退加 `ZPACK_FFMPEG_SEED` 本地种子旁路（AGENTS.md:328）。相关的 #37：用 `tar -tzf` 校验 xz 包必失败，已改为 `tar -tf`（AGENTS.md:324）。
- **zlib 的动态库被带进 import 表**：Windows 产线首跑时发现（2026-11-09），修法见 §5。

**astiav 引擎**：

- **#12 `ImageBufferSize` / `ImageCopyToBuffer` 的 align 必须 ≥ 1**：传 0 会得到 EINVAL，size 为 0，空切片上取 `b[0]` 就越界 panic（AGENTS.md:229）。这是 image 包在同一个引擎上踩的。
- **#13 sws 像素比对的参照要选对**：sws 默认 FAST_BILINEAR，ffmpeg CLI 默认 Bicubic，色度边缘会分歧；CLI 的 rgb24 路径自带 dithering。逐位等价的正确参照是 `rgba64le` 的低字节（AGENTS.md:230）。同样是 image 包踩的。
- **#14 解码循环**：单包直收对 libjxl 不够，首个包不吐帧、只得到 EAGAIN。要写成「收帧—读包」循环，结束时 `SendPacket(nil)` 冲洗。帧跨函数传递要用 `Ref()` 克隆，否则内部帧释放之后调用方手里的引用就悬空了（AGENTS.md:231）。
- **x264 把时基当帧率**：视频解码器上下文必须先 `SetFramerate(GuessFrameRate)`，否则编码器会把 1/15360 这样的时基当成 fps（media/engine_cgo.go:492）。
- **定长帧的音频编码器**：滤镜图吐出来的帧长度是任意的，mp3（1152）和 aac（1024）会拒收，报 frame_size not respected。修法是 AudioFifo 缓冲，攒够一帧的长度再切出来编（media/engine_cgo.go:582-589）。
- **libopus 只吃 48k 系的采样率**：编码器不支持源采样率时取它支持的第一个（media/engine_cgo.go:552-565）。
- **流级 SAR 会盖掉滤镜的校正**：缩放之后只加 `setsar=1` 滤镜不够，编码器上下文的 SAR 不同步成 1:1 的话，流级 SAR 会把滤镜的结果盖回去，形成双重校正（2026-11-06，AGENTS.md:159；media/engine_cgo.go:519-532）。
- **`DisplayMatrix().Add` 只追加不替换**：源已有矩阵时写不进新的角度，只能烤入（media/transform.go:89）。
- **`applyPresetToParams` 曾漏设源旋转**：预设降档触发重编时，源旋转没有被烤入，竖拍视频出来是躺倒的。补上之后加了回归用例（verify/verify-media.sh:206-212，注释写明「旋转审计补丁」）。同类的事在 `ApplyDirectives`（media/directives.go:81）和 `Bake`（media/bake.go:118）里各有一份，改一处时记得三处。
- **跨容器 remux 的 `codec_tag`**：源容器的 tag（比如 mp4 的 `mp4a` / `avc1`）目标容器可能不认，拷流参数之后要清零，让目标 muxer 自己分配（media/engine_cgo.go:337-339）。
- **资源释放靠 `astikit.Closer`，它按加入的顺序执行，不是后进先出**（go-astikit v0.42.0 的 defer.go:31-45）。统一执行器里 `inputCtx.Free` 排在 `inputCtx.CloseInput` 前面，之所以没出事，是因为 astiav 的每个方法都先判空指针：`Free` 把内部指针置成 nil 之后，`CloseInput` 就成了空操作。这是照 go-astiav 官方示例的写法来的；给 `Closer` 加新条目时别假设它是栈序。

**就地改写毁源文件（2026-11-24 实测并修复，提交 417e3a8）**：

- 现象：`zpack media compress x.mp4` 不带 `--output`，缺省输出名就是输入自己。10.3MB 的源文件执行后只剩 1297 字节，命令报 `is_success:true`；源文件太小时压缩计划先报「目标大小低于下限」，随后命令面的「清理半成品」把源文件直接删掉。`audio compress` 的就地覆盖同理（见 [audio.md](audio.md)）。
- 根因：引擎边读输入边写输出，以写模式打开输出时先把源文件截断；命令面失败后无条件删输出路径。
- 修法：引擎新增 `media/inplace.go`——输出与输入是同一个文件时改写旁边的临时文件（保留原扩展名，引擎按扩展名定容器格式），成功后原子替换，失败删临时文件、原文件不动；主执行器 `executeFile` 统一走这一步（convert / compress / preset / bake / 几何变换、audio 全族）。剪辑类命令（clip / cover / insert-frame / merge / speed）各自直接打开输出，遇到同文件明确拒绝（与 ffmpeg 一致）。命令面的清理改走 `removeFailedOutput`（`cli/media/cleanup.go`）：输出与任一输入是同一个文件时不动。
- 回归：单测 `media/inplace_test.go`、`cli/media/cleanup_test.go`；门禁「就地改写安全」10 例。

### 6.4 与其它功能、构建产线、GUI 的耦合点

- **音频**（[audio.md](audio.md)）：`zpack audio` 与 `zpack media` 在同一个包（`cli/media`，命令面）里，共用结果载荷的构造（`buildMediaResultJSON`、`mediaStreamJSON`）、日志适配（`mediaLogf`）和 info 的实现（`audio info` 直接调 `mediaInfo`，cli/media/audio.go:243）。引擎层共用更多：`engine.Options` 里的三个音频质量字段、`forceAudioEncode`、`audioOnlyTargets`，以及同一个 `executeFile`。改视频转码的执行器，音频一定跟着变。
- **图片**：`screenshot` 用 image 包写图，`cover` / `insert-frame` 用 image 包读图。HDR / HEIC / AVIF 的解码在 image 包里，走的是同一个 astiav 引擎，所以 #12 到 #14 那几条坑是两家共用的。
- **GUI**：视频转换页调 `media convert <输入> <输出>`（ui/gui/main.go:2696），视频压缩页调 `media compress <输入> --target-size <值> --output <输出>`（ui/gui/main.go:3045），输出名是 `<基名>-compressed.mp4`（重名时另起）。GUI 是薄壳，只认 stdout 的 JSON，所以 §3 里任何字段改名、增删、语义变化都是对 GUI 的破坏性变更。GUI 的前端校验表 `VOLUME_TEMPLATE_NAMES` 与 media 的模板表不同源（§9.2 第 22 条）。
- **skill.md**（`cli/tools/skill.md`，`go:embed` 进二进制）：§3.6 教 Agent 怎么用 media，§3.11 教 `zpack ffmpeg`。里面有几处与代码不符（§9.4），改命令行为时要一起改它。
- **构建产线**：`internal/ffmpegcli/libs/libzpack_ffmpeg_cli.a` 由产线拷进来，链接时靠 `#cgo LDFLAGS: -L${SRCDIR}/libs` 自包含。astiav 那一侧则需要 `PKG_CONFIG_PATH` 指向产线的 prefix 才能做 cgo 构建（AGENTS.md:199）。
- **输出层**：所有最终输出都经 `internal/stdout` 的 print 函数，print 即 exit。引擎返回 error 之后命令面先 `os.Remove` 再 `ErrorPrint`，顺序不能反（§4.3）。
- **装配**：三个命令在 cli/root.go:73-76 注册；统一开始日志由 root 的 `PersistentPreRun` 输出，命令代码不自己打。
- **取消机制不覆盖这里**：只有压缩包引擎的循环会检查取消请求（conventions.md §2）。

### 6.5 改动时容易破坏的东西

1. **JSON 的字段名和键序**。载荷全是 struct，键序 = 声明序。GUI、skill.md、媒体门禁（用 `grep` 找 `"mode":"remux"` 这样的子串）都依赖它。给 `mediaResultJSON` 中间插一个字段，门禁不一定会红，但确定性比对会变。
2. **`encoding/json/v2` 的 `omitempty` 语义**。它只省空字符串、空切片、nil 指针，不省 `0` 和 `false`；要省零值得用 `omitzero`。现在 `mediaResultJSON` 的 `target_bytes` / `already_fit` 和 `InfoStream` 的一串数值字段都带着 `omitempty` 却照样输出零值。把输出层换回 v1，或者有人「顺手」把这些改成 `omitzero`，输出形状都会变。
3. **视频滤镜链是往前拼出来的**。三处注释声明的顺序与代码实际拼出的顺序相反（§4.1.1、§9.3 第 39 条）。动这个字符串之前先把 §9.3 第 39 条核实掉，否则修的和坏的会搅在一起。
4. **显示坐标系的算式有三份**：引擎的 `DisplayDims`（media/directives.go:93-105）、命令面 info 里手写的一份（cli/media/media.go:774-784）、建议规则里按存储方向算的一份（media/info.go:135-146）。三份现在就不完全一致。
5. **旋转的符号约定**。探测值是顺时针度数，来自 astiav `Rotation()` 的那次取负；`TransposeFilter` 按顺时针角度选 `transpose=1` / `=2`；矩阵路径写回时用的 `NewDisplayMatrixFromRotation` 也是顺时针。任何一处改成另一种约定，烤入的方向就会和矩阵差 180°，而门禁只比容器头里的宽高，发现不了。
6. **引擎的错误文案就是对外的 `error_info`**。媒体门禁直接 grep 文案里的关键词（`下限`、`不兼容`、`跳过字幕流`、`完整版`），改文案必须同步改门禁。
7. **主循环吞错误**（§4.3）。在这个循环里加任何「失败即返回 error」的逻辑都会改变现有的可观察行为：原本报成功的坏源会开始报错。这大概率是好事，但要知道自己在改什么。
8. **`--quiet` 的判定用的是剥掉子命令名之后的那份参数**（`mediaLogf(args)` 的 `args` 是 `mediaRestArgs` 的结果）。`mediaRestArgs` 只剥第一个非旗标参数；audio 那边用的是 `cliutil.RestAfterPositional`，语义略有不同，别互相替换。
9. **纯构建必须编得过**。给引擎加一个新的对外函数，就得同时加 `*_nocgo.go` 桩，否则 `CGO_ENABLED=0` 直接编译失败，连带 Windows 的一键交叉编译。
10. **死代码别顺手接上**。`clipStreamCopy`、`renderSingleFrameVideo`、`mergeVideos`、`isTextSubtitle` / `subtitleTargetFor` / `subtitleCodecIDByName`、`BakeSupportedKinds` / `BakeNeedsReencode`、`swapEncodeDims` 现在都没有调用方。其中几个看起来像现成的功能，其实没有验证过。
11. **产物定案**。动了引擎代码，收尾时要跑产线重铸 `bin/zpack-cli` 并刷新两个装机位，用 `ui/right-click/macos/refresh-engine.sh`，不要手工 cp（AGENTS.md:191）。产线的 `build.sh` 会 `go clean -cache`，之后 cgo 冷构建要等一会儿，这是预期。
12. **命令面里引擎包的别名固定是 `engine`**（`engine "github.com/guohub8080/zpack/media"`），与 audio 共用同一个别名。
13. **ffmpeg 直调不要包一层 JSON**。它是契约里唯一的例外，任何「统一输出」的重构都必须把它绕开，否则 `pipe:1` 输出二进制流的用法会被 JSON 弄坏。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面 | `cli/media/media.go` | `NewMediaCmd`、`runMedia` 分派、16 个子命令的实现（五个几何变换共用一个处理函数）、结果 JSON 的全部 struct |
| 命令面 | `cli/media/ffmpeg.go` | `NewFFmpegCmd`、进程内直调的入口 |
| 命令面 | `cli/media/audio.go` | audio 命令族，见 [audio.md](audio.md)。与本 spec 共用 `buildMediaResultJSON`、`mediaLogf`、`mediaInfo` |
| 命令面 | `cli/media/doc.go` | 包注释 |
| 装配 | `cli/root.go` | 注册三个命令、统一开始日志、Ctrl+C 处理 |
| 命令面公共底座 | `internal/cliutil/` | `HelpAwareRun`（help 守卫）、`EngineLogf`（`--quiet`）、`RestAfterPositional` |
| 旗标小件 | `internal/cmdutil/flags.go` | `HasFlag`、`FirstPositional` |
| 输出契约层 | `internal/stdout/log.go` | 信封、五个 print 函数、过程日志；用的是 `encoding/json/v2` |
| 引擎·决策 | `media/decide.go` | `Mode`、探测结构、容器 / 字幕 / 音频容器兼容表、`Decide`、剪辑类命令的结果类型 |
| 引擎·编排 | `media/media.go` | `Logf`、`execParams`、`ExecResult`、`Options`、`ConvertWithOptions`、`compressWithSample`、`encodeSample`、`Info` |
| 引擎·压缩公式 | `media/plan.go` | 三个常数、`PlanCompression`、`ResolveTargetBytes`、介质模板 |
| 引擎·正交指令 | `media/directives.go` | `SCREEN_BOXES`、`ValidateScreen`、`ValidateCodec`、`ApplyDirectives`、`DisplayDims`、`TransposeFilter` |
| 引擎·体检 | `media/info.go` | `InfoReport` / `InfoStream` / `InfoAdvice`、`AnalyzeCompression`、`BuildInfoReport` |
| 引擎·预设 | `media/preset.go`、`media/presets.json` | 预设注册表、`FitWithinBox`、`ConvertPreset`、`CompressPreset` |
| 引擎·几何变换 | `media/transform.go` | `TransformKind`、`PlanTransform`、`Transform`、`NetMatrixAngle` |
| 引擎·bake | `media/bake.go`、`media/bake_exec.go` | `BakeableKinds`、`PlanBake`、`Bake`、有损类的滤镜链 |
| 引擎·执行 | `media/engine_cgo.go` | `probeFile`、`planStreams`、`executeFile`、`setupEncoder`，以及收帧 / 编码 / 写包各环节 |
| 引擎·剪辑 | `media/clip.go`、`screenshot.go`、`cover.go`、`insertframe.go`、`merge.go`、`speed.go`、`timestamps.go` | 六个剪辑命令的 cgo 实现 |
| 引擎·纯构建桩 | `media/stub_nocgo.go`，以及 clip / cover / insertframe / merge / screenshot / speed 各自的 `*_nocgo.go` | 探测、执行与各剪辑函数的桩 |
| ffmpeg 直调 | `internal/ffmpegcli/` | `cli_cgo.go`（cgo 实现）、`cli_nocgo.go`（桩）、`linkflags_other.go` / `linkflags_windows.go`、`libs/libzpack_ffmpeg_cli.a` |
| 单测 | `media/media_test.go` | 决策矩阵、压缩公式、目标体积解析、预设、适配框、建议规则、正交指令、旋转纯函数、字幕兼容表、变换计划、bake 计划 |
| 验收脚本 | `verify/verify-media.sh` | 媒体门禁，需完整版 |
| 产线 | `build_cli/mac/`、`build_cli/windows/` | 完整版构建；`build_cli/linux/` 只有占位脚本 |
| GUI 耦合 | `ui/gui/main.go`、`ui/gui/service.go`、`ui/gui/validate.go` | 视频转换页、视频压缩页、前端校验表 |
| Agent 文档 | `cli/tools/skill.md` | `go:embed` 进二进制的 SKILL.md |
| 体感数据 | `scripts/genenv/` | realEnvTest 的媒体夹具生成（`media/video-720p-60s.mp4`、`media/video-rotated.mp4`，依赖完整版 `bin/zpack-cli`，缺失时如实跳过） |

## 8. 验收用例

### 8.1 媒体门禁 `verify/verify-media.sh`

只测 `bin/zpack-cli`（完整版），开头先探一次：产物是纯构建就报「⛔」退出，没有系统 `ffmpeg` 也退出（verify/verify-media.sh:22-31）。夹具用系统 ffmpeg 从 `testsrc2` / `sine` 合成。与本 spec 相关的分节（断言点数是按脚本静态数的，没有实跑）：

| 分节（行号） | 覆盖了什么 | 断言点 |
|---|---|---|
| convert：remux（39-48） | mp4 → mkv：exit 0、`mode:remux` + `action:copy`、视频流仍是 h264、体积不小于源的 95% | 4 |
| convert：音频容器抽取（49-55） | mp4 → mp3：exit 0、编码是 mp3、全解码 | 3 |
| convert：webm 全编（56-60） | `mode:full` 且输出里有 vp9 | 2 |
| compress：目标 40%（61-67） | exit 0、产物落在源的 20% 到 60% 之间、全解码 | 3 |
| compress：低于下限被拒（68-72） | exit 1、文案含「下限」 | 2 |
| compress：纯音频（73-78） | 256k 的 m4a → 32KB 的 mp3，落在 16 到 48KB | 2 |
| 预设（79-103） | 列表含 youtube / discord、详情 `width:1920`、未知预设 exit 1、4K 源套 youtube 降档到 1920×1080、discord 压到 25MB 以内、已达标直拷贝（`already_fit:true` + `mode:remux`） | 9 |
| media info（104-113） | 事实字段 codec / fps、4K 的两条建议、纯音频、不存在 exit 1、缺参数 exit 1 | 6 |
| 正交指令（114-134） | `--screen 1080p` 降档、`--codec hevc` 换码、两者组合、非法档位、非法编码、容器不兼容的文案、`--screen` + `--preset` 互斥 | 10 |
| 旋转烤入 + 字幕保轨（135-162） | info 读到侧数据旋转、旋转源 remux 仍是 remux 且保矩阵、重编时烤入（宽高对调、侧数据消失、全解码）、mkv remux 保字幕、转 mp4 时丢弃并告警且无字幕流 | 9 |
| 几何变换命令（163-188） | rotate-right 零损（`mode` 与矩阵已写入）、`--bake` 宽高交换、rotate-180 尺寸不变、mirror 恒烤入、矩阵路径与烤入路径 PSNR ≥ 20、三种退化 | 10 |
| 烤入语义补全（189-205） | 净角度、`media bake` 烤实、info 恒显 `rotation:0` 与显示尺寸对调 | 5 |
| 竖拍 × preset（206-213） | 竖拍 4K 套 youtube 输出 608×1080、矩阵已烤实 | 2 |
| SAR（214-222） | info 的 `sar_num:16` 与 `display_width:768`、`aspect-ratio` 建议、`--screen 360p` 归一到 480×360 1:1 | 3 |
| bake 一级命令（223-244） | info 的 bakeable 双项与 `bakeable` 建议、`baked` 字段、双烤输出 576×768 1:1、无矩阵、全解码、无可烤时 `baked:[]`、两种退化 | 10 |
| 截图（245-270） | 缺省中间帧、`--time`、`--count 4` 出 4 帧、`--every 1.5s`、PNG、输入不存在、`--time` 超时长 | 10 |
| ffmpeg 透传（334-348） | `-version` 头、配置含 libjxl、真实转码有产出、退出码传播、零缓存目录、管道里输出不丢 | 8 |
| 契约与退化（482-489） | 无子命令、输入不存在、convert 缺参数、非法 `--target-size`、`--help` exit 0 | 5 |

合计约 103 个断言点。脚本里另外三节不属本 spec：HDR 图片解码（271-332）归 image，office（359-444）归 office，audio 命令族（445-481）归 audio。整个脚本静态数下来是 158 个断言点，其中一部分在条件分支里（装没装 LibreOffice 结果不同）；AGENTS.md:182 写的是「128 用例」，对不上，见 §9.3 第 48 条。

门禁**没有**覆盖到的：

- `clip`、`cover`、`insert-frame`、`merge`、`speed`、`flip` 六个子命令，脚本里一次都没有出现。纯 Go 主门禁 `verify/verify.sh` 里也没有任何 media 的用例。
- `--quiet`；纯构建下各命令的报错；`screenshot` 的 `--times` 模式。
- compress 不带 `--output` 的缺省行为（每个用例都显式给了 `--output`）。
- 画面是否正确。带旋转的用例只断言容器头里的宽高和侧数据，见 §9.3 第 39 条。
- 一条小注：verify/verify-media.sh:190 的注释说「rotate-right --bake 于 -90° 源 = 净 0°」，按 §4.1.1 的顺时针约定，`-display_rotation 90` 的源探测值是 +90，净角度应是 180°。两种情况宽高都不变，断言区分不了。【待确认：符号约定是从 astiav 源码推的，没有实跑看日志】

### 8.2 单元测试 `media/media_test.go`

纯构建下 `go test ./...` 就能跑，只测纯 Go 的决策层：

| 测试 | 覆盖 |
|---|---|
| `TestDecide` | 决策矩阵 8 种情形，含「旋转 90° 仍 remux」「未知容器全编」 |
| `TestPlanCompression` | 公式数值区间、音频降到 128k、下限保护、纯音频分配 |
| `TestResolveTargetBytes` | 百分比、尺寸、模板、三种非法输入 |
| `TestPresets` | 起步预设不少于 8 档、8 个 ID 都在、messaging 三档有上限 |
| `TestFitWithinBox` | 不升档、等于框不缩放、4K → 1080p、超宽保比例、竖屏入横框 |
| `TestAnalyzeCompression` | 4K h264 出两条建议、健康的 1080p hevc 零建议、码率虚高、纯音频 |
| `TestApplyDirectives` | 缩放强制 full、小源不强制、换码强制、同码不强制、三种非法输入 |
| `TestRotationHelpers` | 显示尺寸对调、`TransposeFilter` 的角度映射 |
| `TestSubtitleCompat` | 字幕兼容表 |
| `TestTransform` | 滤镜映射、宽高交换判定、净角度、四种计划 |
| `TestBake` | 无可烤、旋转 + SAR 双烤、SAR 目标取显示坐标系、纯旋转不缩放 |

`planStreams`、`executeFile`、`setupEncoder` 在 cgo 文件里，没有单测，只靠门禁。

### 8.3 手动验证的方法

```bash
# 帮助守卫与纯构建的报错（纯构建 zpack-dev 即可）
zpack media --help; echo $?                      # 0
zpack media                                      # 用法 JSON，exit 1
zpack media preset youtube                       # 纯构建也可用
zpack ffmpeg -version                            # 纯构建：需完整版；完整版：ffmpeg 原生输出

# 契约形状
zpack media info <视频> 2>/dev/null | zpack json get data.suggestions.0.flag
zpack media convert <视频> <输出.mkv> 2>/dev/null | zpack json get data.mode

# 过程日志（去掉 2>/dev/null；加 --quiet 应只剩开始日志那一行）
zpack media convert <视频> <输出.webm>

# ffmpeg 透传的契约例外
zpack ffmpeg -version | head -1                 # ffmpeg version …
zpack ffmpeg -i <视频> -f null - ; echo $?      # 退出码是 ffmpeg 的

# 媒体门禁（mac，需完整版 bin/zpack-cli 与系统 ffmpeg）
bash verify/verify-media.sh

# 以后给 §9.3 的疑点补证据时，每个都用一份拷贝来做，不要拿唯一的原件
cp <样本.mp4> /tmp/t.mp4 && zpack media compress /tmp/t.mp4 --output /tmp/out.mp4
ffprobe -v error -count_frames -select_streams v:0 -show_entries stream=nb_read_frames /tmp/out.mp4
```

## 9. 未结项与已知限制

编号全文通用，正文里说的「见 §9」都指这里。标「按代码」的条目是读代码就能确定的；标【待确认】的放在 §9.3，需要实跑才能定性。

### 9.1 没做的

1. **文本字幕互转**（srt ↔ mov_text ↔ webvtt）。不兼容的字幕轨只有丢弃一条路（AGENTS.md:169）。引擎里 `isTextSubtitle` / `subtitleTargetFor` / `subtitleCodecIDByName` 三张表留好了，计划层从不产生「字幕重编码」的动作（media/engine_cgo.go:858-881）。
2. **`media speed` 的音频处理**。atempo / asetrate 管线没有接，现状见 §9.2 第 14 条。
3. **真正的两遍编码**。ffmpeg 的 stats_file / stats_in 没有接，理由是 astiav v0.43 没有暴露（AGENTS.md:169）。现有的「两遍采样逼近」第一遍其实是整片编码（§4.1.3），代价是双倍时间和一个系统临时目录里的临时文件。
4. **编码器与编码参数没有入口**。没有 CRF、preset、GOP、编码器选择；画质只有码率一个旋钮。
5. **HDR 烤入没有 tonemap**。只有 colorspace 滤镜，高光细节没有做映射；zscale 滤镜产线里也没有（media/bake_exec.go:22-29、AGENTS.md:332）。
6. **可烤种类里的 interlace 与 color-range**。探测里已经读了 `ColorRangeName`（media/decide.go:33），bake 不处理它们。
7. **Linux 的完整版产线**。`build_cli/linux/` 只有占位脚本。
8. **捆绑 ffmpeg 的缺口**：`drawtext`、`subtitles`、`zscale` 滤镜和 `libsvtav1` 都没有编进去（AGENTS.md:332）。没有 `zpack ffprobe`。
9. **转码不响应取消**。第一次 Ctrl+C 不中断；第二次 exit 130 且不清理（conventions.md §2）。
10. **进度**。转码类命令只有约每 10% 一条的 `转码进度: N%` 日志，没有统一格式的进度条；六个剪辑命令没有任何进度。
11. **compress 第一遍的临时文件位置不可改**。落在系统临时目录，没有 `--temp-dir`。
12. **merge 的三处简化**：非基准输入拉伸不保比例；帧率不同的输入不重采样，只重排时戳；音频只取基准的（media/merge.go:115、237、300-305）。
13. **门禁缺口**（§8.1）：六个剪辑命令没有任何自动化用例；`--quiet`、纯构建报错、compress 的缺省输出、画面正确性都没有用例。

### 9.2 已知不对但暂时没改

14. **`media speed` 没有声音**。音频包在主循环里被直接丢弃，输出里只有一条空壳音频流；`--pitch-match` 对应的参数在函数体里没有任何引用；用法文案里的「缺省保音调」不成立（media/speed.go:94-102、257-260；cli/media/media.go:641、646-647、663）。
15. **`media clip` 给了帧号却没给视频路径会 panic**。`index out of range [0] with length 0`，exit 2，stdout 为空（实测）。帧号分支在读 `positional[0]` 时还没检查有没有位置参数（cli/media/media.go:554 先于 566）。
16. **`media screenshot … --every 0` 退出码 0 且 stdout 为空**（实测）。间隔为 0 时直接 `return`（cli/media/media.go:362-365）。
17. **封面图不缩放、不定向**。`cover` 与 `insert-frame` 的函数注释都说「自动缩放到视频尺寸」，实际是 `draw.Draw` 按原尺寸拷到画布左上角：小图留黑边，大图被裁（media/cover.go:22、52-53；media/insertframe.go:38-39）。读图用的是 `image.ReadImage` 而不是带 EXIF 定向的 `ReadImageOriented`（media/cover.go:36、48；media/insertframe.go:34），竖拍的照片做封面会是躺倒的。
18. **带 `--preset` 时 `--codec` 被静默丢弃**，convert 与 compress 都是（cli/media/media.go:120-129、229-234）。
19. **compress 同时带 `--preset` 与 `--target-size` 时两处目标不一致**。引擎按预设的 `max_size_bytes` 压，JSON 的 `target_bytes` 报的却是 `--target-size` 的值（cli/media/media.go:207-209、220、239-240）。
20. **convert 的预设码率上限从命令行走不到**。命令面在调用前把预设的 `video_bitrate` 清零了，引擎里「源码率超上限 1.1 倍就强制重编」那条规则成了死路（cli/media/media.go:125；media/preset.go:94-109）。
21. **预设容器不符的报错，第二个占位符填成了容器名**。实际输出是「输出扩展名 .mkv 与预设 mp4 的容器 mp4 不符」，本该是预设名（media/preset.go:129）。
22. **介质模板三处各写各的**。media 认 8 个（media/plan.go:129-138），分卷认 11 个（cli/archive/compress.go:226-238），GUI 的前端校验表照着分卷的 11 个写（ui/gui/validate.go:26-29）。后果：视频压缩页填 `dvd5` / `fat32` / `bd25` 时前端放行，点了按钮才看到引擎报错。GUI 的铁律是「GUI 判错的引擎一定也判错」、宁可漏判不可误拦，这里是反过来的漏判。media 那边注释里写的「与 cli.VOLUME_TEMPLATES 同源」不成立（media/plan.go:128）。
23. **`--factor` 的下限没有强制**。文案写 0.1-10，实际只要求大于 0，`--factor 0.01` 被接受（cli/media/media.go:652-655；media/speed.go:17-19；实测）。
24. **数值校验有漏网点**。screenshot 的 `--count`、clip 的四个时间与帧号旗标，非法值不报错，当作没给（cli/media/media.go:279-281、513-539）。这与「数值参数严格校验」的全局约定相悖（conventions.md §2）。
25. **`cover` / `insert-frame` / `clip` / `merge` / `speed` 用法里的 `--output` 不是旗标**。五个命令的解析循环都不认它，只是碰巧在它紧挨着输出位置时能工作（cli/media/media.go:421-427、461-472、506-545、594-605、643-660）。
26. **失败时无条件删除输出路径上的文件**，包括用户原有的、本次没有写过的（§4.3）。
27. **多帧截图逐帧容错过了头**。图片格式不被 image 包接受时，每一帧都在创建空文件之后才失败，结果是 exit 0、`count:0`、输出目录里一批 0 字节文件（media/screenshot.go:59-66；image/image.go:89-110，按代码）。
28. **`screenshot --format` 对带 `--output` 的单帧不生效**，格式跟输出路径的扩展名走（media/screenshot.go:26-30）。
29. **竖屏视频按横框处理**。`resolution` 建议按存储高度判断，`--screen` 与预设的适配框也是横的框，1080×1920 的竖拍视频会被建议并实际缩成 606×1080（media/info.go:135-146；media/directives.go:71-78；按代码）。
30. **主循环中途的错误被吞掉**。坏掉的源会得到截断的产物和 `is_success:true`（§4.3）。
31. **clip、merge、speed、insert-frame、screenshot 五个剪辑命令都不保留旋转**。它们新建的输出流不拷 display-matrix，滤镜链里也没有 transpose，截图不转正（media/clip.go:199-229、media/merge.go:93-113、media/speed.go:71-91、media/insertframe.go:518-538、media/screenshot.go:152-162；按代码）。竖拍视频经这些命令处理之后画面是躺倒的。`cover` 是 remux，不受影响。
32. **结果 JSON 的 `video` / `audio` 报的是最后一条同类流**，多轨文件里不是首条（media/engine_cgo.go:356-375）。
33. **扩展名区分大小写**。`.MP4` 被当成未知容器走全编（media/decide.go:90-106）；预设路径下则是报错。
34. **未知容器下字幕被当成全兼容**，会原样拷过去，目标容器收不收要看 muxer（media/engine_cgo.go:172-183）。
35. **帮助文本过时**。`Use` 只写了 `<convert|compress>`；无子命令时的用法只列 convert 与 compress，容器清单还漏了 `mov`；子命令级没有帮助（cli/media/media.go:28-29、66）。
36. **纯构建的「需完整版」文案有 8 种措辞**，各 `*_nocgo.go` 各写各的（§5）。其中 screenshot 桩里的那一种从命令行走不到，因为命令面先调了 `info`。
37. **死代码**：`clipStreamCopy`（media/clip.go:45-146）、`renderSingleFrameVideo` 与 `mergeVideos`（media/insertframe.go:55-413）、字幕互转的三张表（media/engine_cgo.go:858-881）、`BakeSupportedKinds` 与 `BakeNeedsReencode`（media/bake_exec.go:34-48）、`swapEncodeDims`（media/media.go:26，只有赋值没有读取）、`TransformSwapsDims`（media/transform.go:43，只有单测在用）。`transform` 用法里 merge 分支之后的「至少需要 2 个输入视频」不可达（cli/media/media.go:612-614）。

### 9.3 【待确认】

读代码时发现的疑点。本次任务禁止转码，以下都没有实跑，需要用样本的**拷贝**验证。

38. **输入与输出是同一个文件时没有任何防护，而 `media compress x.mp4` 的缺省输出恰好就是输入本身**（cli/media/media.go:211-217）。引擎先打开输入读，再以写模式打开输出（media/engine_cgo.go:292、382-389）。按代码推演：输出一打开，源文件就被截断成 0 字节，之后读到的只剩探测缓冲里那点数据，命令照样报成功，`original_size_bytes` 还是事后 stat 出来的截断后大小。`audio compress` 的缺省「就地覆盖」走的是同一个执行器（cli/media/audio.go:148）。小样本可能碰巧整个落在探测缓冲里而显得正常。【待确认：会毁文件，不能用唯一的原件试】 **【已实测确认并修复，提交 417e3a8】**实测 10.3MB 的源文件执行后只剩 1297 字节且报 success；太小的源文件在报错后被「清理半成品」删掉。修法见 6.3。
39. **滤镜链的实际顺序与三处注释相反**。代码往前拼出来的是 `scale,setsar=1` → 显式滤镜 → `transpose` → `format`（media/engine_cgo.go:617-631）；注释写的是 transpose → hflip → scale（同处 618-619、media/transform.go:123、media/bake_exec.go:10）。按代码推演有两个后果：
    - 带旋转的源一旦需要缩放（`--screen`、预设降档、SAR 烤入），`scale` 作用在存储方向的帧上、用的却是显示方向的尺寸，随后的 `transpose` 又把尺寸换回去，帧尺寸与编码器尺寸对不上（media/engine_cgo.go:514-524）。产物要么是个没有帧的空壳，要么画面错乱。
    - 带旋转的源做 `mirror` / `flip`，实际得到的是另一个轴上的翻转，两者互换。

    AGENTS.md:159 说适配「全部在显示坐标系」，按代码看只有算目标尺寸那一步在显示坐标系，缩放本身作用在存储方向的帧上。
    
    门禁里两个相关用例（verify/verify-media.sh:206-212、223-237）只断言容器头里的宽高、侧数据和可解码性，分不出画面对不对。【待确认：要看画面和实际帧数】
40. **解码器没有冲洗**。`flushWorker` 只冲洗滤镜图和编码器，没有 `SendPacket(nil)`（media/engine_cgo.go:709-719）；clip、merge、speed、insert-frame 各自的循环同样没有。带 B 帧延迟的源，末尾若干帧可能丢失。AGENTS.md 的陷阱 #14 讲的正是这件事，只不过是在 image 包里踩的。【待确认】
41. **clip 的帧时长按 30fps 假设**（media/clip.go:273）。非 30fps 的源，画面被按 30fps 的节奏重排，而音频原样拷贝，可能音画不同步。merge 与 insert-frame 按基准或源的平均帧率重排，帧率不同的输入不会被重采样（media/merge.go:139-144；media/insertframe.go:563-568）。【待确认】
42. **截图落在前一个关键帧**。引擎 seek 之后收到的第一帧就当作结果，不比对时间戳（media/screenshot.go:116-150）。【待确认：需逐帧核对】
43. **内嵌封面流被当成视频流**。探测不区分 attached_pic（media/engine_cgo.go:51-52、105-107）。带内嵌封面的音频文件，`media info` 的 `video` 会是那条 mjpeg 封面流，并据此给出「转 hevc」之类的建议；`media cover` 自己的产物就是这样。【待确认】
44. **VFR 判定可能误报**。判据是 r_frame_rate 与平均帧率相差超过 1%（media/engine_cgo.go:88-91）。有些容器的 r_frame_rate 是个标称值，恒定帧率的视频也会被记成 VFR，`bakeable` 里多出 `vfr`，`media bake` 随之做一次有损的 fps 滤镜。【待确认】
45. **具体用的是哪一个编码器实现没有确认**。目标编码只有四选一（h264 / hevc / vp9 / av1），实现取决于 `avcodec_find_encoder` 按注册顺序返回的第一个，JSON 里看不出来（media/engine_cgo.go:503）。Windows 静态构建里是否可能有 Media Foundation 之类的编码器排在 libx264 前面，没有查证。【待确认：要查产线编进去的编码器注册顺序，或在完整版上实测】
46. **convert 结果里的 `target_bytes:0` 与 `already_fit:false`**，以及 info 在没有任何建议时 `suggestions` 为 `null`，都是从 json/v2 的语义和 info 的实测推出来的，没有拿到对应的样本实跑（§3.2、§3.3）。【待确认：需要样本实跑】
47. **`--codec` 配音频容器输出**。校验层对音频容器直接放行，随后模式被改成 `full`，要往 mp3 里写视频流（media/directives.go:43-46；media/media.go:94-100）。应该在写头时失败，具体文案未实测。【待确认】
48. **媒体门禁的用例数**。AGENTS.md:182 写 128，脚本静态数出来是 158 个断言点（含条件分节）。【待确认：需要实跑看汇总行】

### 9.4 代码与文档不一致

**AGENTS.md**（以代码为准）：

| 位置 | 文档的说法 | 代码的现状 |
|---|---|---|
| AGENTS.md:64 | convert 的成功输出里没有 `target_bytes` / `already_fit`；`mode` 只有四个值 | 两个字段恒出（§3.2）；`mode` 还有 `transform-matrix` / `transform-bake` / `bake`（media/decide.go:10-18） |
| AGENTS.md:65 | compress「mode 恒 full」 | 带预设且源已达标时是 `remux`（media/preset.go:161-167） |
| AGENTS.md:150 | 「①目标容器全兼容**且无旋转** → remux」 | 旋转不再否决直拷（media/decide.go:121-127）。同一份文档的 AGENTS.md:158 已经写对了，150 行是旧话 |
| AGENTS.md:152 | 完整版「mac 已验证：brew libav 动态链接」 | 现在是源码级全静态（AGENTS.md:204 自己就是这么写的） |
| AGENTS.md:153 | 预设用于 convert 时有「码率上限（源超 1.1× 强制重编）」 | 命令面把预设码率清零了，走不到（§9.2 第 20 条） |
| AGENTS.md:154 | suggestions 的种类是 resolution、codec-generation、bitrate-inflated、audio 四种 | 还有 `aspect-ratio` 与 `bakeable`（media/info.go:117-156） |
| AGENTS.md:155 | 几何变换命令是 `rotate-left\|rotate-right\|rotate-180\|mirror` | 还有 `flip`（cli/media/media.go:49-50；media/transform.go:14） |
| AGENTS.md:156 与 AGENTS.md:157 | 「正交指令」这一段写了两遍 | — |
| AGENTS.md:160 | bakeable「当前：rotation+sar；判定就绪待接：interlace/color-range」 | bakeable 含 `vfr` 与 `hdr`，并且会执行（media/bake.go:19-35、77-86） |
| AGENTS.md:169 | 「两遍编码阻塞……单遍编码目标偏差实测 ±5%」 | 已经是两遍采样逼近（media/media.go:124-230），只是第一遍是整片编码 |
| AGENTS.md:182 | 媒体门禁「128 用例」 | 脚本静态数是 158 个断言点（§9.3 第 48 条） |
| AGENTS.md:340 | cli 目录是「root.go + commands.go」 | 已按功能拆成子包，`cli/commands.go` 不存在（提交 6d31586、40fb9e7 之后文档没跟） |

**内嵌的 skill.md**（`cli/tools/skill.md`）：

| 位置 | 文档的说法 | 代码的现状 |
|---|---|---|
| cli/tools/skill.md:91 | `suggestions[]` 每项是 `{kind,reason,flag}` | 字段是 `kind` / `order` / `flag` / `note` / `estimated_ratio`，没有 `reason`（media/info.go:58-64） |
| cli/tools/skill.md:95 | `media convert … [--bake]` | 已退休，被静默忽略 |
| cli/tools/skill.md:98、101、106-108 | clip / merge / speed / cover / insert-frame 的用法里写着 `--output <输出>` | 这五个命令都不解析 `--output`，输出靠位置参数（§9.2 第 25 条） |
| cli/tools/skill.md:106、112 | speed 的 `--pitch-match`、「缺省保音调」 | 参数未被引擎使用；音频整个被丢弃（§9.2 第 14 条） |
| cli/tools/skill.md:104 | `--format <png\|jpg>` | 缺省还能是 webp，引擎认的更多（§2.2） |
| cli/tools/skill.md:17 | Ctrl+C「一次 = 清理半成品后退出」 | media 不响应第一次（§9.1 第 9 条） |

**代码里的注释与帮助文本**：

| 位置 | 注释的说法 | 代码的现状 |
|---|---|---|
| media/engine_cgo.go:618-619、media/transform.go:123、media/bake_exec.go:10 | 滤镜链顺序 transpose → hflip → scale | 实际反过来（§9.3 第 39 条） |
| media/clip.go:3-4 | 「缺省流拷贝；`--exact` 全编帧精确」 | 恒全编，没有 `--exact` |
| media/decide.go:44 | 旋转「≠0 → 一票否决直拷」 | 不否决 |
| media/bake.go:1-7 | 可烤清单只有 rotation 与 sar；vfr / hdr / interlace / color-range 是「未来候选」 | vfr 与 hdr 已进清单并会执行 |
| media/bake_exec.go:23、27 | 「tonemap（hable 算法）」 | 只有 colorspace |
| media/preset.go:128 | 「与 cli.VOLUME_TEMPLATES 同源」 | 8 个对 11 个（§9.2 第 22 条） |
| media/media.go:124、162-165 | 「编 10% 样本」「采样校正」 | 第一遍是整片编码 |
| cli/media/media.go:28-29、66 | Use / Short / 用法只列 convert 与 compress | 16 个子命令（§2.1） |
| cli/media/media.go:663 | speed「缺省保音调（快慢不变调）」 | 没有声音 |
| cli/media/media.go:429、474、567、608、663 | cover / insert-frame / clip / merge / speed 的用法里有 `--output` | 这五个命令都不解析它 |
| verify/verify-media.sh:190 | 「-90° 源 = 净 0°」 | 应是净 180°（§8.1） |

**其它文档**：`build_cli/README.md:13`（Windows 产线「⏳ zig cc」，已改本机 MinGW）、`build_cli/README.md:38`（「主 go.mod 不引入 go-astiav」，go.mod 里已经 require 了）、`build_cli/README.md:11-12`（mac 的 prepare.sh / build.sh 说明还是 POC 阶段的）。

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 修复：`clip --frame-from/--frame-to` 不带视频路径时不再 panic，改为用法报错 | 118dde3 |
| 2026-11-24 | 修复：输出与输入是同一个文件时不再毁掉源文件——引擎先写临时文件、成功后原子替换；剪辑类命令同路径明确拒绝；命令面清理半成品不再删与输入相同的输出（见 6.3） | 417e3a8 |
