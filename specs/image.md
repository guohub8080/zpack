# 图片（image）

> `zpack image` 是图片的格式转换、压缩、缩放、几何变换、拼接、新建占位图与元信息查询工具：标准格式走纯 Go 编解码，HDR / HEIC 系走内置 libav。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

**做什么**：对一张图片做不涉及内容编辑的常规处理（`merge` 是多张进、一张出），共 12 个子命令，分派在 cli/image/image.go:38-54：

| 类别 | 子命令 | 一句话 |
|---|---|---|
| 读 | `info` | 读头部元信息（不解码像素）：格式、尺寸、色彩模型、EXIF 方向 |
| 转码 | `convert` | 按输出扩展名换格式 |
| 瘦身 | `compress` | 同格式重编码降质量，或压到指定体积 |
| 缩放 | `resize` | 像素 / 百分比 / 适配框 / 物理尺寸（mm·cm·inch + PPI）四种定尺寸方式 |
| 几何 | `rotate-left` `rotate-right` `rotate-180` `mirror` `flip` | 纯像素重排 |
| 拼接 | `merge` | 多图纵向长图 / 横向排开 |
| 新建 | `new` | 纯色或透明的空白占位图 |
| Live Photo | `livephoto` | 找出 HEIC 的配对视频并拷出来（不解码像素） |

读写范围（完整矩阵见 4.1.8）：

- **按内容识别的输入**（不看扩展名）：jpg / png / gif / bmp / tiff / webp（image/image.go:58-73）。
- **按扩展名分流到内置 libav 的输入**（仅完整版）：`.exr`、`.hdr` / `.rgbe`、`.jxl`（image/hdr_cgo.go:303-314）；`.heic` / `.heif` / `.avif`（image/heic.go:21-32）。
- **输出**：jpg / jpeg / png / gif / bmp / tiff / tif / webp（image/image.go:55、88-112）。

**贯穿全部处理路径的一条规则**：convert / compress / resize / 五个几何变换 / merge 读取时一律先把 EXIF Orientation 物化成「观看态」像素（`ReadImageOriented`，image/image.go:78-85），输出不再携带 EXIF 标签。理由见 6.2。

**不做什么**：

- 不写 HEIC / AVIF / EXR / HDR / JXL——这些只能当输入。
- WebP 输出只有无损 VP8L，`--quality` 对它无效（image/image.go:115-120）；TIFF 输出不压缩（image/image.go:106 传 `nil` 选项）。
- 不做色彩管理（ICC → sRGB 缓期）、不做裁剪 / 水印 / 滤镜 / 锐化 / 降噪。
- 没有批处理：一条命令一张图。GUI 的「压缩图片」页是自己循环、每张图起一个任务（ui/gui/main.go:3385-3396）。
- 不保留也不写出任何元数据：除 Orientation（读出来只为物化）以外的 EXIF、ICC、JFIF / pHYs 密度一概不带到输出里（见 9 的文档不一致条目）。
- 不处理动画与多页：GIF、多页 TIFF 只取第一帧。动图 WebP【待确认】——引擎用的 `golang.org/x/image/webp` 不支持动画，预计解码报错，未实测。

**使用场景**：证件照改尺寸（`resize --mm 35x45 --ppi 300`）、把照片压到报名系统的大小上限（`compress --target-size 100KB`）、截图转格式、多张截图拼长图、纠正横倒的照片、生成排版用的占位底图、从 iPhone 的 Live Photo 里取出视频。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack image new <输出> <尺寸> [--color 规格] [--ppi N] [--quality N]` | 新建空白占位图 |
| `zpack image convert <输入> <输出> [--quality N]` | 按输出扩展名换格式 |
| `zpack image compress <输入> [--quality N \| --target-size 体积 [--priority 值]] [--output 路径]` | 同格式重编码瘦身，或压到指定体积 |
| `zpack image info <输入>` | 读元信息 |
| `zpack image resize <输入> <输出> <缩放方式…> [--ppi N] [--quality N]` | 缩放 |
| `zpack image rotate-left\|rotate-right\|rotate-180\|mirror\|flip <输入> <输出> [--quality N]` | 几何变换（与 `media` 家族同名同义） |
| `zpack image merge <图1> <图2> [图3…] <输出> [--reference 路径] [--direction vertical\|horizontal] [--quality N]` | 拼接 |
| `zpack image livephoto <HEIC> [输出目录]` | 提取 Live Photo 的配对视频 |

### 2.1 解析规则（image 全族通用）

- **手写解析**：命令关闭了 cobra 的旗标解析，各子命令自己扫参数（cli/image/image.go:28）。未知旗标静默忽略。
- **`--help` / `-h` 出现在任意位置**都打印 image 这一级的帮助并 exit 0（cli/image/image.go:29-31；`internal/cliutil/help.go`）。没有子命令级的帮助——`zpack image resize --help` 打印的也是同一份 `image` 帮助。
- **以 `-` 开头的参数一律当旗标**，所以以 `-` 开头的文件名无法作为位置参数传入。
- **只有被识别的旗标才吞掉下一个参数当值**。未知旗标的值如果不以 `-` 开头，会落成位置参数：`image convert a.png b.png --jobs 4` 里 `--jobs` 被忽略、`4` 成了第三个位置参数，结果报用法错误。
- **旗标缺值**统一报「`<旗标> 缺少值（…）`」；数值越界统一报「`<旗标> 需 … 的整数，收到: <原值>`」。
- **`--quiet`**（2026-11-24 用户定案补齐；此前只是被当作未知旗标忽略）：过程日志静默，stderr 只剩「开始执行」那一行，stdout 不变。image 全族都认它；有过程日志可静默的是 `compress --target-size`（策略说明）和 `resize`（物理尺寸换算说明）。也没有进度条。
- **输出文件已存在时直接覆盖**，没有跳过 / `--force` 语义。

### 2.2 `image new`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `<输出>`（位置参数 1） | 输出路径；扩展名决定格式 | jpg / jpeg / png / webp / bmp / tiff / tif / gif，大小写不敏感；其它报 `输出格式不支持: xxx（支持 jpg/png/webp/bmp/tiff/gif）` | 无扩展名 = png |
| `<尺寸>`（位置参数 2） | 尺寸规格，三形态见 4.1.1 | 见 4.1.1 | 必填 |
| `--color <规格>` | 底色 | `transparent` / 色名 / `#RRGGBB` / `#RRGGBBAA`（`#` 可省），见 4.1.1 | `transparent`（全透明） |
| `--ppi <N>`（别名 `--dpi`） | 物理尺寸换算成像素用的密度 | 1–2400 的整数 | 300 |
| `--quality <N>` | JPEG 质量，只对 .jpg 生效 | 1–100 的整数 | 92 |

位置参数必须恰好 2 个。全部错误文案见 3.5。

### 2.3 `image convert`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `<输入>` `<输出>` | 恰好 2 个位置参数 | 输入必须存在；输出必须带受支持的扩展名 | — |
| `--quality <N>` | JPEG 质量（对 png / webp / gif / bmp / tiff 无效） | 1–100 的整数 | 92 |

### 2.4 `image compress`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `<输入>` | 恰好 1 个位置参数 | 必须存在 | — |
| `--quality <N>` | 手动定质量，同格式重编码一次 | 1–100 的整数 | 80 |
| `--target-size <体积>` | 体积上界；给了就走自动压缩（见 4.1.3） | **整数** > 0；后缀 `KB` / `K`、`MB` / `M`、`GB` / `G`（1024 基）、`B` 或无后缀（= 字节）；大小写不敏感；不接受小数 | — |
| `--priority <值>` | 自动压缩的策略 | `size` / `size-first` / `size-only` / `quality` / `quality-first` / `quality-only`（前两对是同一档的旧名与新名） | `size` |
| `--output <路径>` | 输出路径 | — | 输入路径（就地覆盖） |

- `--target-size` 与 `--quality` 互斥（cli/image/image.go:214-216）。
- `--priority` 只在给了 `--target-size` 时才被读取；单独给会被静默忽略。
- **输出格式跟随源格式，`--output` 的扩展名不参与判断**（image/convert.go:61-65）。

### 2.5 `image info`

只有 1 个位置参数 `<输入>`，没有旗标。

### 2.6 `image resize`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `<输入>` `<输出>` | 恰好 2 个位置参数 | 输入必须存在 | — |
| `--mm <WxH>`、`--cm <WxH>` | 物理宽高都定——**不保比例**，直接拉伸到换算结果 | `WxH` 两个正数（可小数），分隔符是字母 `x`；只给一个数报错并引导 `-w` / `-h` | — |
| `--mm-w` `--mm-h` `--cm-w` `--cm-h` `<正数>` | 只定一侧，另一侧按比例 | 正数（可小数） | — |
| `--inch <WxH>`、`--inch-w <正数>`、`--inch-h <正数>` | 同上，单位英寸 | 同上 | — |
| `--ppi <N>`（别名 `--dpi`） | 物理尺寸换算成像素用的密度 | 1–2400 的整数 | 300 |
| `--width <N>` | 目标宽（像素），高按比例 | 正整数；可以大于源宽（= 放大） | — |
| `--height <N>` | 目标高（像素），宽按比例 | 正整数；可以放大 | — |
| `--percent <N>` | 等比缩小 | 1–99 的整数 | — |
| `--max <WxH>` | 适配框：只降不升、保比例 | 两个正整数 | — |
| `--quality <N>` | 输出编码质量（JPEG 生效） | 合法值 1–100；**非法值静默忽略**（见 9） | 92 |

缩放方式的优先级（cli/image/image.go:539-574、image/convert.go:221-243）：

1. 一个缩放方式都不给 → `需要指定缩放方式：--cm / --inch / --width / --height / --percent / --max`。
2. cm / mm 族优先于 inch 族：两族都给时 inch 被整体忽略（代码是 if / else-if）。
3. 物理族的结果写进「目标宽 / 目标高」后与像素旗标一起进引擎，引擎按「宽高都给 → 只给宽 → 只给高 → 百分比 → 适配框」的顺序取第一个命中的。所以物理单侧压过 `--percent` 和 `--max`。
4. 结论：**一次只用一族缩放方式**，混用的结果没有定义，也不应当依赖。

### 2.7 几何变换

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `<输入>` `<输出>` | 恰好 2 个位置参数 | 输入必须存在；输出无扩展名时用源格式 | — |
| `--quality <N>` | JPEG 质量 | 1–100 的整数 | 92 |

方向约定（image/transform.go:140-173）：`rotate-left` = 逆时针 90°，`rotate-right` = 顺时针 90°，`mirror` = 左右翻，`flip` = 上下翻。

### 2.8 `image merge`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `<图1> <图2> [图3…] <输出>` | 至少 3 个位置参数，末位是输出，其余是输入 | 任一输入不存在即报错 | 输出无扩展名时用 png |
| `--reference <路径>`（别名 `--ref`） | 基准图，对齐轴的尺寸以它为准 | 必须是输入之一（按绝对路径字符串完全相等比较） | 第一张 |
| `--direction <值>` | `vertical`（上下堆叠）/ `horizontal`（左右排开） | 其它值报错 | `vertical` |
| `--quality <N>` | JPEG 质量 | 1–100 的整数 | 92 |

### 2.9 `image livephoto`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `<HEIC>` | 第 1 个位置参数 | 必须存在；不检查是不是真的 HEIC | — |
| `[输出目录]` | 第 2 个位置参数 | 目录必须已存在 | HEIC 所在目录 |

用法文案里写的是 `[--output-dir 目录]`，但代码**没有解析这个旗标**：`--output-dir` 被当未知旗标忽略，它的值落到第 2 个位置参数上，所以碰巧能用；目录路径以 `-` 开头时失效（cli/image/image.go:589-606）。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

## 3. 输出契约

成功时的 stdout JSON 都是信封 `{"is_success":true,"data":…}`，下面写的是 `data` 里的内容，字段按出现顺序列出。

### 3.1 通用结果（new / convert / compress / resize / 五个几何变换共用）

结构体 `ImageResult`（image/image.go:31-49）：

```json
{
  "input": "/abs/in.jpg",
  "output": "/abs/out.jpg",
  "source_format": "jpeg",
  "target_format": "jpg",
  "width": 800,
  "height": 600,
  "original_size_bytes": 123456,
  "output_size_bytes": 45678,
  "ratio": 0.37,
  "original_format": "jpeg",
  "quality": 90,
  "target_bytes": 102400,
  "already_fit": true,
  "strategy": "size-first",
  "scale_percent": 68,
  "quality_degraded": false
}
```

| 字段 | 类型 | 含义 | 何时出现 |
|---|---|---|---|
| `input` | string | 输入的绝对路径。**`new` 例外**：填的是输出路径 | 恒有 |
| `output` | string | 输出的绝对路径 | 恒有 |
| `source_format` | string | 源格式：标准格式 = Go 解码器名（`jpeg` / `png` / `gif` / `bmp` / `tiff` / `webp`）；HDR 族 = `exr` / `hdr` / `jxl`；HEIC 族 = 小写扩展名（`heic` / `heif` / `avif`）；`new` 恒为 `blank` | 恒有 |
| `target_format` | string | 输出格式，各命令的归一程度不同，见下表 | 恒有 |
| `width` `height` | int | 输出的像素尺寸，已是观看态 | 恒有 |
| `original_size_bytes` | int64 | 输入文件字节数；`new` 为 0 | 恒有 |
| `output_size_bytes` | int64 | 输出文件字节数 | 恒有 |
| `ratio` | float | 输出 ÷ 输入；输入为 0 时为 0 | 恒有 |
| `original_format` | string | 与 `source_format` 相同 | 只有不带 `--target-size` 的 compress |
| `quality` | int | 最终命中的 JPEG 质量 | 只在 `--target-size` 且真的编码过时（≥ 1） |
| `target_bytes` | int64 | 目标体积，命令面回填（cli/image/image.go:229-231） | 只在 `--target-size` 成功时 |
| `already_fit` | true | 源文件本来就不超目标，原样拷贝 | 只在 `--target-size` 且已达标时 |
| `strategy` | string | `size-first` / `quality-only` / `scale-then-quality` / `last-resort` | 只在 `--target-size` 且真的编码过时 |
| `scale_percent` | int | 最终缩放百分比，100 = 没缩 | 同 `strategy` |
| `quality_degraded` | true | 走了最小缩放下的兜底链，或 quality-only 的命中质量低于 50（见 4.1.3） | 只在 `--target-size` 且确实降级时 |

后 7 个字段带 `omitzero`：零值不出现在 JSON 里。所以 `already_fit` 为 true 时没有 `quality` 键（它是 0），不要指望读到 `"quality":0`。

`target_format` 的取值：

| 命令 | 取值规则 |
|---|---|
| `new` | 小写扩展名，`jpeg` → `jpg`，无扩展名 = `png`，`tif` 保持 `tif`（image/new.go:45-51） |
| `convert` | `normalizeFormat(扩展名)`：`jpeg` → `jpg`、`tif` → `tiff`（image/convert.go:41） |
| `compress` | 等于归一后的源格式（image/convert.go:61） |
| `resize` | 发生了缩放：`normalizeFormat(扩展名)`，无扩展名时 = 源格式名；没发生缩放：同 `convert`（image/convert.go:245-260） |
| 几何变换 | **小写扩展名原样，不归一**（`.jpeg` 输出报 `jpeg`），无扩展名时 = 源格式名（image/transform.go:116-119） |

### 3.2 `image info`

结构体 `ImageInfo`（image/convert.go:86-97）：

```json
{"input":"/abs/photo.jpg","format":"jpeg","width":400,"height":300,"size_bytes":12345,"color_model":"ycbcr","orientation":6,"display_width":300,"display_height":400}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `input` | string | 绝对路径 |
| `format` | string | 解码器名（注意 JPEG 报的是 `jpeg` 不是 `jpg`）；HDR 族报 `exr` / `hdr` / `jxl` |
| `width` `height` | int | **存储尺寸**——没有按 Orientation 转 |
| `size_bytes` | int64 | 文件字节数 |
| `color_model` | string | `rgba` / `nrgba` / `gray` / `gray16` / `cmyk` / `ycbcr` / `ycbcra` / `other` / `unknown`（image/convert.go:160-183）。HDR 族例外：填的是 libav 解码器的名字（image/hdr_cgo.go:291） |
| `orientation` | int | EXIF 方向，2–8；没有标签或等于 1 时**不出现** |
| `display_width` `display_height` | int | 观看态尺寸，方向 5–8 时与存储尺寸对调；只在 `orientation` 出现时出现 |

**HEIC / HEIF / AVIF 输入是另一套载荷**，而且两种构建都一样——不解码像素，只报告 Live Photo 配对状态（cli/image/image.go:359-374）：

```json
{"format":"heic","input":"/abs/IMG_0001.HEIC","is_livephoto":true,"note":"配对视频已找到（同目录 .MOV）；像素解码需完整版（CGO 构建）","video_path":"/abs/IMG_0001.MOV"}
```

```json
{"format":"heic","input":"/abs/IMG_0002.HEIC","is_livephoto":false,"note":"未找到配对视频；像素解码需完整版（CGO 构建）"}
```

- 这是手搭的 map 而不是结构体，键按名字排序、不是书写序（输出层对 map 强制排序，internal/stdout/log.go:39-40）；`format` 对三种扩展名都写死为 `heic`。
- 完整版上也照样打印「像素解码需完整版」这句话，并且不会给出宽高（见 9）。

### 3.3 `image merge`

结构体 `MergeResult`（image/merge.go:22-32）：

```json
{"reference":"/abs/a.png","inputs":["/abs/a.png","/abs/b.png"],"output":"/abs/long.png","direction":"vertical","width":640,"height":840,"format":"png","image_count":2,"size_bytes":12345}
```

`format` 是小写扩展名原样（不归一，无扩展名时为 `png`）；`size_bytes` 是输出文件字节数。这是唯一用 `size_bytes` 而不是 `output_size_bytes` 的图片命令。

### 3.4 `image livephoto`

**现状是双重信封**（cli/image/image.go:610-634 手搭了一层 `is_success` / `data`，输出层又包了一层，见 9）： **2026-11-24 已修复（提交 118dde3）**：现在只有一层信封，`data` 里直接是各字段——下面描述的旧形态仅作历史记录。

```json
{"is_success":true,"data":{"data":{"extracted":"/abs/video_IMG_0001.MOV","has_video":true,"heic_path":"/abs/IMG_0001.HEIC","size_bytes":1234567,"video_path":"/abs/IMG_0001.MOV"},"is_success":true}}
```

没找到配对视频时同样是成功（exit 0）：

```json
{"is_success":true,"data":{"data":{"has_video":false,"heic_path":"/abs/IMG_0002.HEIC","video_path":null},"is_success":true}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `heic_path` | string | 输入的绝对路径 |
| `has_video` | bool | 是否找到配对视频 |
| `video_path` | string / null | 配对视频的绝对路径；没有时为 `null` |
| `extracted` | string | 拷贝出来的文件路径 `<输出目录>/video_<视频文件名>`；只在找到时出现 |
| `size_bytes` | int64 | 拷出来的文件字节数；只在找到时出现 |

### 3.5 失败时的 `error_info`

一律是 `{"is_success":false,"error_info":"…"}` + exit 1。文案就是引擎 `error.Error()` 的原文（conventions.md 的「引擎错误文案即对外文案」），所以改文案要同步 `verify/verify-image.sh`。

| 命令 | `error_info` | 触发条件 |
|---|---|---|
| `image` | `用法: image convert <输入> <输出.jpg\|png\|webp> 或 image compress <输入> [--quality N] 或 image info <输入>` | 子命令缺失或未识别（文案只列了三个子命令，已过时） |
| 所有子命令 | `路径不存在：<绝对路径>`（merge 是 `图片不存在：<绝对路径>`） | 输入文件不存在 |
| 所有子命令 | `用法: image <子命令> …` | 位置参数个数不对 |
| `new` | `--color 缺少值（transparent/色名/#RRGGBB）` | 旗标缺值 |
| `new` / `resize` | `--ppi 缺少值（如 300）`、`--ppi 需 1-2400 的整数，收到: X` | `--ppi` / `--dpi` 缺值或越界 |
| 全族 | `--quality 缺少值（1-100）`、`--quality 需 1-100 的整数，收到: X` | `--quality` 缺值或越界（convert 的第二句写作「需要」；resize 的第二句不存在，见 2.6） |
| `new` | `缺少尺寸（如 800x600 / 55x90mm --ppi 300 / a4）` | 尺寸是空串 |
| `new` | `尺寸规格非法: X（像素 800x600 / 物理 55x90mm + --ppi / 纸型 a4）` | 既不是纸型，也不是 `WxH` |
| `new` | `尺寸需正数: X` | 宽或高不是正数 |
| `new` | `颜色需 transparent/色名/#RRGGBB，收到: X` | 6 个字符但不是十六进制 |
| `new` | `颜色需 transparent/色名/#RRGGBB[AA]，收到: X` | 8 个字符但不是十六进制 |
| `new` | `颜色需 transparent/色名/#RRGGBB（6 位）或 #RRGGBBAA（8 位），收到: X` | 其它长度且不是色名 |
| `new` | `输出格式不支持: X（支持 jpg/png/webp/bmp/tiff/gif）` | 扩展名不在支持列表 |
| `new` | `.jpg 不保真透明——用 .png/.webp/.tiff 或 --color 指定底色` | 透明底 + jpg / bmp / gif |
| `convert` | `输出路径缺扩展名（如 .jpg/.png/.webp）` | 输出没有扩展名 |
| `convert`、没发生缩放的 `resize` | `不支持的输出格式 .X（支持 jpg/jpeg/png/gif/bmp/tiff/tif/webp）` | 输出扩展名不在支持列表 |
| `resize`（发生了缩放） | `不支持的输出格式 .X` | 同上（这条路径不带支持列表） |
| `convert`（HEIC 输入）/ 几何变换 | `不支持的输出格式: X` | 输出扩展名不在支持列表（走的是 `WriteImage` 的文案，没有点号） |
| `convert` / `compress` / `resize` / 几何变换（merge 包在 `读取图片 N 失败:` 里） | `无法解码图片 <文件名>: <解码器原话>` | 内容认不出或文件损坏 |
| `compress`（不带 `--target-size`） | `源格式 X 无法重编码压缩` | 源是 HDR 族（完整版能读、不能写） |
| `compress` | `--target-size 缺少值（如 100KB / 2MB / 500000）`、`--target-size 需要 >0 的体积（如 100KB、2MB、500000），收到: X` | 体积缺值、不是整数或 ≤ 0 |
| `compress` | `--priority 缺少值（size-first=先缩尺寸 / size-only=只缩尺寸 / quality-first=先降画质 / quality-only=只降画质）`、`--priority 需 size\|size-first\|size-only\|quality\|quality-first\|quality-only，收到: X` | 策略缺值或未识别 |
| `compress` | `--output 缺少值` | 缺值 |
| `compress` | `--target-size 与 --quality 互斥（自动二分 vs 手动定质量）` | 两个旗标同时出现且 `--quality` 合法 |
| `compress` | `X 无质量维度（webp=纯 Go 无损 VP8L；PNG/GIF/BMP/TIFF 天生无损）——目标体积请转 jpg（image convert）或缩尺寸（image resize）` | `--target-size` 用在非 JPEG 上 |
| `compress` | `缩至 5% 且 q=1 仍超出目标 N 字节——物理不可达：裁剪内容或接受更大体积` | size-first / quality-first 的兜底链走到头 |
| `compress` | `缩至 5% @q90 仍超出目标 N 字节——「只缩尺寸」不降画质：放宽目标体积或改用「先缩尺寸」` | size-only 装不下 |
| `compress` | `全尺寸 q=1 仍超出目标 N 字节——「只降画质」不缩尺寸：放宽目标体积或改用「先降画质」` | quality-only 装不下 |
| `info` | `无法读取图片信息 <文件名>: <解码器原话>` | 头部认不出或文件损坏 |
| `info`（完整版，HDR） | `无法读取 exr 图片: …`、`无视频流` | libav 打不开文件 |
| `resize` | `<--mm\|--cm> 缺少值（WxH 双数值，如 3.5x4.5 / 35x45；只定一侧用 <-w> / <-h>）` | 缺值 |
| `resize` | `<旗标> 需 WxH 双数值（如 35x45）；只定一侧用 <旗标>-w / <旗标>-h，收到: X` | `--mm` / `--cm` / `--inch` 只给了一个数 |
| `resize` | `<旗标> 宽度 需正数，收到: X`、`<旗标> 高度 需正数，收到: X` | `WxH` 的一侧不是正数 |
| `resize` | `--mm-w` 等单侧旗标 `缺少值（正数）`、`需正数，收到: X` | 缺值或不是正数 |
| `resize` | `--inch 缺少值（WxH 双数值，如 3.5x5；只定一侧用 --inch-w / --inch-h）`、`--inch-w 缺少值（正数）`、`--inch-h 缺少值（正数）` | 缺值 |
| `resize` | `--width 缺少值`、`--width 需正整数，收到: X`（`--height` 同） | 缺值或不是正整数 |
| `resize` | `--percent 缺少值（1-99）`、`--percent 需要 1-99 的整数（缩小），收到: X` | 缺值或不在 1–99 |
| `resize` | `--max 缺少值（如 1920x1080）`、`--max 宽度需正整数，收到: X`、`--max 高度需正整数，收到: X`、`--max 需 WxH 格式（如 1920x1080），收到: X` | 缺值或格式不对 |
| `resize` | `需要指定缩放方式：--cm / --inch / --width / --height / --percent / --max` | 一个缩放方式都没给（文案里漏了 `--mm`） |
| `merge` | `--reference 缺少值（基准图片路径）`、`--direction 缺少值（vertical=上下长图 / horizontal=左右排开）` | 缺值 |
| `merge` | `--direction 需 vertical 或 horizontal，收到: X` | 方向未识别 |
| `merge` | `--reference X 不在输入列表中` | 基准图不是输入之一（含大小写不同） |
| `merge` | `读取图片 N 失败: <原因>` | 第 N 张输入解不开 |
| `merge` | `写入拼接图失败: <原因>` | 输出格式不支持或写盘失败 |
| `livephoto` | `未找到配对视频（在 <目录> 同目录找同名 .MOV/.mov）`、`读取视频失败: …`、`写入视频失败: …`（纯 Go 构建的文案；完整版的后两句是 `拷贝视频失败: …`） | 探测与拷贝之间文件消失、读失败、输出目录不可写 |
| 纯 Go 构建 | `EXR/HDR/JXL 需完整版（CGO 构建）` | 对 exr / hdr / rgbe / jxl 做任何读取（convert / compress / resize / 变换 / merge / info） |
| 纯 Go 构建 | `HEIC/HEIF/AVIF 解码需要完整版（build_cli 产线 CGO 构建）；纯 Go 构建不含 astiav/libav 引擎` | `convert` 一个 HEIC 族文件 |
| 完整版（HEIC） | `打开 HEIC 失败: …`、`读流信息失败: …`、`HEIC 中无图像流`、`HEIC 解码失败: …`、`HEIC 像素转换失败: …` | libav 打不开或解不出 |
| 全族 | 操作系统原话（如 `open C:\x.png: The system cannot find the path specified.`） | 输出目录不存在、无权限、文件被占用 |

### 3.6 stderr

- 统一开始日志 `zpack image 开始执行`（只记到 `image` 一级，不含子命令名，cli/root.go:52）。
- 带 `--quiet` 时，下面这些过程日志全部静默，stderr 只剩「开始执行」那一行（去向由 `cliutil.EngineLogf(args)` 按本次调用的参数判定，与其它命令同一套）。
- `resize` 用了物理尺寸时，一行换算说明：`物理尺寸 3.50cm × 按比例 @ 300 PPI → 413 × 0 像素`。只给一侧时另一侧显示「按比例」，但后面那个像素数是 0（换算在引擎里才完成，日志打在那之前）。
- `compress --target-size` 的策略说明，每条任务 1–2 行（image/compress_target.go:83、103、121、140、155、183）：
  - `size 优先：质量钉 q=90，缩尺寸换体积`
  - `size-only：质量钉 q=90 只缩尺寸，绝不降画质`
  - `quality-only：分辨率保持原样，画质一路二分到 q1`
  - `全尺寸最高只到 q=N（<50 画质下限）——缩尺寸优先保画质`
  - `缩至 5% @q90 仍超目标——画质降级全量程二分`（quality-first 那条是 `@q50`）
- 没有进度条：image 没有接进度采样。巨图的目标体积压缩可能跑分钟级，期间 stderr 也只有上面那几行策略说明。

## 4. 行为细则

### 4.1 正常路径

#### 4.1.1 `image new`：尺寸三形态与颜色写法

工具做的事：解析尺寸（image/new.go:93-146）→ 解析颜色（image/new.go:149-174）→ 由输出扩展名定格式 → 透明能力检查（image/new.go:55-58）→ 生成一张 NRGBA 图并逐像素填色 → `WriteImage`。

**尺寸规格**（先转小写、去首尾空白；`--ppi` 未给时按 300）：

| 形态 | 写法 | 换算 | 例 |
|---|---|---|---|
| 像素 | `WxH`，无单位 | 直接取整（允许小数，四舍五入，下限 1） | `800x600` |
| 物理尺寸 | `WxHmm` / `WxHcm` / `WxHin` | mm：值 ÷ 25.4 × ppi；cm：值 ÷ 2.54 × ppi；in：值 × ppi；四舍五入、下限 1 | `55x90mm --ppi 300` → 650×1063 |
| 纸型 | 纸型名，可带尾缀 `l`（横）/ `p`（竖） | pt ÷ 72 × ppi | `a4 --ppi 72` → 595×842；`a4l --ppi 72` → 842×595 |

- 分隔符是 ASCII 字母 `x`（已转小写，`X` 也行）；全角的 `×` 和 `*` 都不行。
- 纸型表（image/new.go:18-22）是**竖版基准的整数 pt 值**。尾缀 `l` 且宽 < 高时对调，`p` 且宽 > 高时对调；`ledger` 本身就是横的。
- 尾缀识别在数值解析**之前**，只对「去掉尾缀后正好是纸型名」的输入生效，数值规格不受影响。

| 纸型 | pt（竖版基准） | @72 ppi | @300 ppi（按公式推算） |
|---|---|---|---|
| `a0` | 2384×3370 | 2384×3370 | 9933×14042 |
| `a1` | 1684×2384 | 1684×2384 | 7017×9933 |
| `a2` | 1191×1684 | 1191×1684 | 4963×7017 |
| `a3` | 842×1191 | 842×1191 | 3508×4963 |
| `a4` | 595×842 | 595×842 | 2479×3508 |
| `a5` | 420×595 | 420×595 | 1750×2479 |
| `letter` | 612×792 | 612×792 | 2550×3300 |
| `legal` | 612×1008 | 612×1008 | 2550×4200 |
| `ledger` | 1224×792 | 1224×792 | 5100×3300 |

**颜色规格**：

| 写法 | 例 | 结果 |
|---|---|---|
| 不给，或 `transparent`（不分大小写） | | 全透明 |
| 色名（不分大小写） | `red`、`white` | `white` `black` `red` `green` `blue` `yellow` `cyan` `magenta` `gray` `grey` `orange` `purple` `pink` `brown`（image/new.go:25-32）。注意 `green` 是 CSS 的 (0,128,0)，不是纯绿 |
| `#RRGGBB`，`#` 可省 | `#1a2b3c` | 不透明 |
| `#RRGGBBAA`，`#` 可省 | `#ff000080` | 带透明度 |

**透明与编码器的 alpha 能力**（这是「按编码器真实能力划线，不静默丢」的定案，image/new.go:55-58、84-90）：

| 输出格式 | 不给 `--color`（全透明） | `--color '#RRGGBBAA'` 且 AA < FF |
|---|---|---|
| png / webp / tiff | 允许，alpha 原样保留 | 原样写入 |
| jpg | **报错** `.jpg 不保真透明——用 .png/.webp/.tiff 或 --color 指定底色` | **不报错**。Go 的 JPEG 编码器取的是预乘后的颜色（GOROOT/src/image/jpeg/writer.go:396 调 `.RGBA()`）：AA = 00 得到**全黑**图，其它半透明值得到变暗的纯色 |
| bmp | 报错 | 同 jpg（x/image/bmp 的 24 位路径同样取预乘值，bmp/writer.go:148） |
| gif | 报错 | 量化时同样取预乘值，AA = 00 得到黑色 |

也就是说「透明检查」只拦 `transparent` 这一种情况，显式给了带 alpha 的颜色就绕过去了。上表右列是沿代码路径读出来的（`NewBlank` 里 `fill != nil` 即放行 → `WriteImage` → 各编码器），并对照了三个编码器的源码；受「不做图片处理」的约束没有实际运行验证，见 9。

`--quality` 只对 .jpg 有效。输出：`ImageResult`，其中 `input` 与 `output` 都是输出路径、`source_format` 为 `blank`、`original_size_bytes` 和 `ratio` 为 0。

#### 4.1.2 `image convert`

工具做的事：确认输入存在 → 看输入扩展名是不是 HEIC 族 → 不是就走 `Convert`（image/convert.go:16-48）：读取并定向 → 校验输出扩展名 → `WriteImage`。

- 输入格式靠内容识别，不看扩展名（HDR 族例外，按扩展名分流，见 4.1.9）。
- 输出格式 = 输出扩展名，**必须给**。
- `--quality` 只影响 JPEG 输出。

各格式的编码细节（`WriteImage`，image/image.go:88-112）：

| 输出 | 编码器 | 说明 |
|---|---|---|
| jpg / jpeg | 标准库 `image/jpeg` | 4:2:0 baseline（灰度源是单分量）；**不写 JFIF、EXIF、ICC 段** |
| png | 标准库 `image/png` | zlib 最高压缩档（`BestCompression`）；全不透明的图用 24 位色型，否则带 alpha |
| webp | `nativewebp` | 无损 VP8L，压缩档 `DefaultCompression`（4），不包 VP8X；`--quality` 被丢弃（image/image.go:115-120） |
| bmp | `x/image/bmp` | NRGBA / RGBA 源写 24 位；灰度源写 8 位灰；调色板源写 8 位调色板 |
| tiff / tif | `x/image/tiff` | **不压缩**（选项传 `nil`）。NRGBA 源写 4 通道 + 非预乘 alpha 标记（ExtraSamples = 2）；RGBA 源写 4 通道 + 预乘标记（ExtraSamples = 1）；其它类型（如 JPEG 解出的 YCbCr）逐像素取预乘值写 4 通道 |
| gif | 标准库 `image/gif` | 非调色板源量化到 Plan9 固定调色板的前 256 色，并做 Floyd–Steinberg 抖动（GOROOT/src/image/gif/writer.go:421-445）——颜色会偏离、画面会有抖动颗粒；alpha 丢弃 |

输出：`ImageResult`，尺寸是观看态尺寸（已按 Orientation 转过）。

#### 4.1.3 `image compress`

##### 不带 `--target-size`：手动质量，同格式重编码

工具做的事（image/convert.go:52-83）：读取并定向 → 输出格式取源格式（**不看 `--output` 的扩展名**）→ 重新编码。

| 源格式 | 实际效果 |
|---|---|
| jpg | 按 `--quality`（缺省 80）重编码，有损，方向标签不再保留 |
| png | zlib 最高档重编码，像素不变 |
| webp | VP8L 无损重编码，`--quality` 无效 |
| gif / bmp | 原样重编码 |
| tiff | 重编码为**不压缩**的 TIFF |

- 没有「压不动就保留原文件」的保护——重编码结果比源更大也照样落盘（例如源本来是压缩过的 TIFF）。只有 `--target-size` 路径有已达标直拷贝。
- 输出比 `convert` 多一个 `original_format`。
- 缺省就地覆盖。此时解码失败等于删源文件，见 4.3。

##### 带 `--target-size`：四档策略

工具做的事（image/compress_target.go:46-117）：

1. 读取并定向。源必须是 JPEG（解码器报 `jpeg`），否则报「无质量维度」。
2. **源文件字节数 ≤ 目标 → 字节级拷贝到输出**，`already_fit:true`，不碰像素（image/compress_target.go:60-66）。就地时等于把文件原样重写一遍（内容不变，修改时间更新）。
3. 否则按 `--priority` 选策略：

| `--priority` | 做法 | 成功时的 `strategy` / `quality` / `scale_percent` | 装不下时 |
|---|---|---|---|
| `size` / `size-first`（缺省） | 质量钉在 q90，二分缩放 | `size-first` / 90 / 命中的百分比（100 = 没缩） | 缩到 5% 仍超 → 在 5% 缩放下全量程二分质量：命中 → `last-resort` + `quality_degraded:true`；q1 仍超 → 报错 |
| `size-only` | 同上，但**绝不降质** | `size-first`（与上一档同名）/ 90 / 命中的百分比 | 直接报错，不兜底 |
| `quality` / `quality-first` | ① 全尺寸二分质量，最高质量 ≥ q50 就用；② 不够 → 在 q50 下二分缩放，命中后在该尺寸下回拉到最高质量；③ 还不够 → 5% 缩放下全量程二分 | ① `quality-only` / 命中值 / 100；② `scale-then-quality` / ≥ 50 / 命中的百分比；③ `last-resort` + `quality_degraded:true` | ③ 里 q1 仍超 → 报错 |
| `quality-only` | 全尺寸二分质量，分辨率绝不动 | `quality-only` / 命中值 / 100；命中质量 < 50 时加 `quality_degraded:true` | 直接报错，不兜底 |

两个二分的含义（image/compress_target.go:192-235）：

- **二分缩放**：在整数百分比 5–100 上找「按固定质量编码后 ≤ 目标」的最大值。每次尝试都从原图重新缩放（CatmullRom 插值），不累积重采样。粒度是 1%，所以结果可能明显小于目标。
- **二分质量**：在 1–100 上找「编码后 ≤ 目标」的最高质量；全部装不下返回 -1。
- 每一轮尝试都是「缩放 + 内存编码 + 量长度」，不落盘；只有最终结果写文件。

`quality_degraded:true` 有两个来源：走了 `last-resort` 兜底链（恒为 true，表示在最小缩放下动了质量），以及 `quality-only` 的命中质量低于 50。

输出：`ImageResult` + `target_bytes`（命令面回填）。stderr 会先打一行策略说明，见 3.6。

#### 4.1.4 `image info`

工具做的事（image/convert.go:100-137）：`image.DecodeConfig` 只读文件头，**不解码像素**；再单独读一次 EXIF 方向。

- `width` / `height` 是存储尺寸；`display_width` / `display_height` 是观看态尺寸，只在方向为 2–8 时出现（5–8 对调）。
- HDR 族走 libav 探测（完整版），`color_model` 里放的是解码器名。
- HEIC 族不解码，走 3.2 的专用载荷。

#### 4.1.5 `image resize`

工具做的事：命令面把物理尺寸换算成像素并打一行日志（cli/image/image.go:538-571）→ 引擎 `Resize`（image/convert.go:212-277）。

| 方式 | 目标尺寸 |
|---|---|
| `--width N` | 宽 = N，高 = 源高 × N ÷ 源宽（整除截断） |
| `--height N` | 高 = N，宽 = 源宽 × N ÷ 源高 |
| 宽高都给（`--width` + `--height`，或 `--mm WxH` / `--cm WxH` / `--inch WxH`） | 就是给的值，**不保比例** |
| `--percent N` | 源尺寸 × N ÷ 100，整除截断 |
| `--max WxH` | 取「框宽 ÷ 源宽」与「框高 ÷ 源高」里较小的作比例；比例 ≥ 1 时不动（只降不升） |
| 物理单侧（`--mm-w` 等） | 该侧 = round(值 × 换算率)，另一侧按比例 |

- 换算率（image/convert.go:288-301）：cm → px 是 `cm ÷ 2.54 × ppi`，inch → px 是 `inch × ppi`，mm 先除以 10 当 cm。四舍五入。
- 尺寸没变化时（`--max` 没触发缩小、或换算结果恰好等于源尺寸）退化为纯格式转换，走 `Convert`。
- 发生缩放时用 CatmullRom 插值缩到 RGBA 画布（`xdraw.Over` 合成），再按输出扩展名编码；输出无扩展名时用源格式。
- `--quality` 只影响 JPEG 输出。
- **PPI 只用来算像素数，不会写进输出文件**：没有 JFIF 密度段，也没有 pHYs 块（见 9）。

验收脚本里的换算例子（verify/verify-image.sh:66-100）：`--cm-w 5.5 --ppi 300` → 650 px；`--cm 3.5x4.5 --ppi 300` → 413×531；`--cm-w 3.5 --ppi 600` → 827 px；`--inch-w 2 --dpi 72` → 144 px；`--mm-h 45 --dpi 100` → 高 177 px；`--mm 35x45 --dpi 100` → 138×177；`--mm-w 55 --mm-h 45 --dpi 100` → 217×177。

#### 4.1.6 几何变换

工具做的事（image/transform.go:100-124）：读取并定向 → 统一转成 RGBA（调色板 / 灰度 / YCbCr 先转）→ 按像素 4 字节原位搬移（无插值、无重采样，image/transform.go:136-186）→ 按输出扩展名编码（无扩展名用源格式）。

- 「无损」指相对解码出来的像素。输出成 JPEG 仍然会按质量（缺省 92）重编码一次；要像素级一致请输出 PNG。
- ±90° 的两个变换宽高对调。
- 变换作用在观看态像素上，输出不带方向标签。

#### 4.1.7 `image merge`

工具做的事（image/merge.go:36-129）：全部解码并定向 → 定基准图 → 算画布 → 逐张落位 → 编码。

| 方向 | 画布 | 每张图 |
|---|---|---|
| `vertical`（缺省） | 宽 = 基准图宽，高 = 各图缩放后高度之和 | 等比缩放到基准宽，自上而下按参数顺序堆叠 |
| `horizontal` | 高 = 基准图高，宽 = 各图缩放后宽度之和 | 等比缩放到基准高，自左而右排开 |

- 基准图缺省是第一张；`--reference` 指定的必须是输入之一。
- 尺寸正好一致的图直接拷贝（逐位一致）；其余用 CatmullRom 缩放。**会放大**小图（没有「只降不升」的规则），基准图比别的图大很多时，小图会被拉得很糊。
- 画布是 RGBA，透明区域保持透明。输出成 JPEG 时透明区域会变黑（预乘）。
- 输出无扩展名时用 PNG。

验收脚本里的例子（verify/verify-image.sh:125-146）：1920×1080 与 640×480 纵向拼 → 宽 1920、总高 1080 + 1440 = 2520；以 640 宽那张为基准 → 总高 360 + 480 = 840；横向 → 总宽 1920 + 1440 = 3360。

#### 4.1.8 格式支持矩阵

| 格式 | 读 | 写 | 有损 | 透明 | 需要的构建 |
|---|---|---|---|---|---|
| jpg / jpeg | ✓ 按内容 | ✓ | 写有损（质量参数） | ✗ | 纯 Go |
| png | ✓ 按内容 | ✓ | 无损 | ✓ | 纯 Go |
| gif | ✓ 按内容 | ✓ | 无损，但写时量化到 256 色 | ✗ | 纯 Go |
| bmp | ✓ 按内容 | ✓ | 无损 | ✗ | 纯 Go |
| tiff / tif | ✓ 按内容 | ✓（不压缩） | 无损 | ✓（非预乘与预乘两种 alpha 标记都会正确写出） | 纯 Go |
| webp | ✓ 按内容 | ✓（无损 VP8L） | 无损，`--quality` 无效 | ✓ | 纯 Go |
| exr | ✓ 按扩展名 | ✗ | — | 解码后压成 8 位 RGBA | 完整版 |
| hdr / rgbe | ✓ 按扩展名 | ✗ | — | 同上 | 完整版 |
| jxl | ✓ 按扩展名 | ✗ | — | 同上 | 完整版 |
| heic / heif / avif | ✓ 按扩展名，**只有 `convert` 能用** | ✗ | — | — | 完整版 |

HEIC 族只有 `convert` 这一条路会调解码器（cli/image/image.go:145-149）。其余命令对 HEIC 走的是通用的 `ReadImage`，那里没有注册 HEIC 解码器，所以**完整版上 `image resize x.heic …`、`image compress x.heic`、`image info x.heic`（见 3.2）同样给不出像素**。要缩放或压缩 HEIC 得先 `convert` 成 PNG / JPEG。

#### 4.1.9 HDR 与 HEIC 的解码通道（完整版）

两类都借内置 libav（go-astiav）解码，按**扩展名**分流，不看内容：

| 扩展名 | 入口 | 像素路径 |
|---|---|---|
| `.exr` / `.hdr` / `.rgbe` / `.jxl` | `decodeHDRImage`（image/hdr_cgo.go:21-39） | 浮点像素格式走色调映射，整数格式走 sws |
| `.heic` / `.heif` / `.avif` | `ConvertHEIC`（image/heic.go:107-172） | 同上（共用 `decodeFirstFrame` + `frameToRGBA`） |

解码流程（`decodeFirstFrame`，image/hdr_cgo.go:54-130）：打开文件 → 取第一个视频流 → 找解码器 → 「收帧–读包」循环；读到 EOF 仍没有帧就送一个空包冲洗；拿到首帧后 `Ref()` 克隆一份返回（内部那份随后释放）。图片在 libav 眼里就是单帧视频流。

**浮点路径**（EXR、Radiance HDR；image/hdr_cgo.go:152-215）：

1. 按平面读出 32 位浮点（平面顺序 G、B、R，可带 A）。
2. 第一遍扫全图，取 R / G / B 三路的最大值作为场景白点。
3. 白点 ≤ 1.05（`HDR_TONEMAP_THRESHOLD`，image/tonemap.go:6）→ 判为 SDR 值域，**原值直通**，保证普通图的往返逐位一致。
4. 白点 > 1.05 → 真 HDR，每个通道走扩展 Reinhard 曲线：`v' = v × (1 + v ÷ W²) ÷ (1 + v)`，白点映射到 1.0，高亮平滑滚降而不是硬裁（image/tonemap.go:10-15）。
5. `v × 255 + 0.5` 取整并钳到 0–255（`FloatToByte`）。alpha 不参与色调映射。

曲线是**逐通道**作用的，不是在亮度域上（v1；亮度域要先换色彩空间，缓期）。tonemap 的数学是纯函数，表驱动单测守着（image/tonemap_test.go）：白点 4 时 4→1.0、2→0.75、1→0.53125。

**整数路径**（AVIF 等；image/hdr_cgo.go:218-260）：sws 用 Bicubic 缩放器转成 rgba64le（与 ffmpeg CLI 缺省的插值一致），再取每个通道的低字节当 8 位值。

AVIF 归 HEIC 通道是有来历的：`ConvertHEIC` 早先调用的 `execFFmpeg` 是空壳，HEIC / AVIF 转换从没真正工作过，2026-11-08 才改成现在这条 astiav 首帧解码 + sws 的路（AGENTS.md:161）。JXL 也走同一通道，验收脚本用内置 ffmpeg 现编一个 JXL 再解回来对尺寸（verify/verify-media.sh:349-357）。

#### 4.1.10 EXIF Orientation 定向链

手机拍的 JPEG / TIFF，像素是按传感器方向存的，观看器靠 EXIF 方向标签（0x0112）把它转正——和视频的 display matrix 是一回事。

- **读标签** `ReadOrientation`（image/exif.go:18-33）：零依赖的最小解析，只看扩展名 `.jpg` / `.jpeg` / `.jfif` / `.tif` / `.tiff`。JPEG 从 SOI 开始逐段找 APP1 的 `Exif\0\0`；TIFF 直接读文件本体。在 IFD0 里找 0x0112，类型必须是 SHORT、值 1–8，否则一律当 0（无标签）。
- **物化** `ApplyOrientation`（image/transform.go:32-92）：值 ≤ 1 原样返回；2 左右镜像、3 旋转 180°、4 上下镜像、5 主对角线转置、6 顺时针 90°、7 副对角线转置、8 逆时针 90°。
- **读取即定向** `ReadImageOriented`（image/image.go:78-85）= `ReadImage` + `ApplyOrientation`。convert、compress（两条路径）、resize、五个几何变换、merge 的每一张都走它。
- **不定向的地方**：`info` 只报告不处理（存储尺寸 + `orientation` + 显示尺寸）；`--target-size` 的已达标直拷贝是字节级拷贝，原文件里的标签原样留着。
- 因为输出不带 EXIF，定向是一次性的：转出来的图再处理一次结果不变。

#### 4.1.11 `image livephoto`

工具做的事（cli/image/image.go:589-635；image/heic.go:182-213）：

1. 取输入去掉扩展名的部分，在同目录依次试 `.MOV`、`.mov`、`.Mp4`、`.mp4`，第一个存在的就是配对视频。
2. 没找到 → 成功返回 `has_video:false`。
3. 找到 → 整个文件读进内存，写到 `<输出目录>/video_<视频文件名>`，返回路径和字节数。目标已存在时直接覆盖。

不需要解码，纯 Go 构建也能用。配对判定纯靠文件名，不看 HEIC 内部的元数据。

### 4.2 边界与退化输入

| 输入 | 结果 |
|---|---|
| `zpack image`（没有子命令）或子命令拼错 | exit 1，`用法: image convert …`（文案只列了三个子命令） |
| 位置参数多一个或少一个 | exit 1，该子命令的用法文案 |
| 输入不存在 | exit 1，`路径不存在：…` |
| 未知旗标 | 静默忽略；它的值可能变成位置参数（见 2.1） |
| `--quality 0` / `101` / `abc` | exit 1（`resize` 例外：静默忽略，按缺省 92 处理） |
| `--ppi 0` / `2401` / `3.5` | exit 1，`--ppi 需 1-2400 的整数` |
| `--percent 0` / `100` | exit 1，只接受 1–99 |
| `--width 0`、`--height -5`、`--max 0x100` | exit 1，需正整数 |
| `--mm 55`（只给一个数） | exit 1，报错并指向 `--mm-w` / `--mm-h` |
| `resize` 一个缩放方式都不给 | exit 1，`需要指定缩放方式：…` |
| `resize --max` 框比图大 | 不放大，退化为纯格式转换 |
| `resize --width` / 物理尺寸大于源 | 照样放大 |
| 文件内容认不出或已损坏 | exit 1，`无法解码图片 …: <解码器原话>` |
| 输出扩展名不支持 | exit 1，`不支持的输出格式…`（各路径的文案略有不同，见 3.5） |
| 输出没有扩展名 | `new` → png；几何变换 → 源格式；`merge` → png；`resize` 发生缩放时 → 源格式；`convert` 与没缩放的 `resize` → 报错；HEIC 输入的 `convert` → jpg |
| `compress` 的源不是 JPEG 却给了 `--target-size` | exit 1，`… 无质量维度 …` |
| `compress --target-size` 源已不超目标 | 成功，直拷贝，`already_fit:true` |
| `compress --target-size` 目标极小 | 按策略兜底或报错，见 4.1.3 |
| `compress --target-size` 与 `--quality` 同时给 | exit 1，互斥报错 |
| `--priority` 单独给（没有 `--target-size`） | 静默忽略 |
| `compress` 后文件反而变大 | 照样成功并覆盖，没有保护 |
| `new` 用透明底 + jpg / bmp / gif | exit 1，引导换格式或给底色 |
| `new --color '#RRGGBB00'` + jpg | 成功，但得到全黑图（见 4.1.1） |
| `new` 的尺寸极大 | 没有上限校验，会试着分配内存（见 9） |
| `merge` 只给两张图（一进一出） | exit 1，用法文案 |
| `merge --reference` 不在输入里 | exit 1，`--reference … 不在输入列表中` |
| 输出文件已存在 | 直接覆盖 |

### 4.3 错误处理

- 所有失败都是 `ErrorPrint`：exit 1 + `error_info`，没有部分成功的状态。`merge` 里有一张读不出来，整单失败，不产出文件。
- **失败时会删输出路径上的文件**。各命令在引擎返回错误后都调一次 `os.Remove(输出路径)`（`new` image.go:105-108、`convert` :150-153、`compress` :235-238、几何变换 :273-276、`resize` :581-584、`merge` :338-341，均指 cli/image/image.go）。本意是清半成品，但 `os.Remove` 不区分「刚写的半成品」和「用户原有的文件」，于是有三种情况会误删： **2026-11-24 已修复（提交 118dde3）**：改用 `internal/cliutil` 的 `OutputGuard`，只删本次新建的输出。
  - 输出路径上本来有文件，而错误发生在写盘之前（尺寸写错、颜色写错、源图解不开）→ **原有文件被删**。例：`image new 已有.png abc`、`image convert 损坏.jpg 重要.png`。
  - `compress` 不带 `--output` 时输出路径就是输入路径，解码失败 → **源文件被删**。
  - `compress` 不带 `--output` 且源不是 JPEG，却给了 `--target-size` → 报「无质量维度」，然后**源文件被删**。例：`image compress photo.png --target-size 100KB`。
  
  这三条是读代码得出的，受约束没有实跑；在修掉之前（见 9），就地压缩务必先备份或改用 `--output`。
- 操作系统层面的失败（输出目录不存在、无权限、文件被占用）把系统原话放进 `error_info`，没有二次包装。
- Ctrl+C：图片引擎不检查取消请求，第一次 Ctrl+C 不会中断正在进行的编码，第二次直接 exit 130，可能留下写到一半的输出文件（conventions.md「取消的范围」）。
- 内存不够时（超大尺寸的 `new`、很多张大图的 `merge`）是 Go 运行时的分配失败，不是 JSON 错误——输出不是契约格式、退出码也不是 1【待确认】未实测。
- 成功路径不删除任何东西。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 12 个子命令的标准格式路径（纯 Go 编解码） | ✓ | ✓ | ✓ |
| EXR / HDR / JXL / HEIC / AVIF 解码 | 要完整版；完整版产线已贯通（`build_cli/windows`） | 要完整版；完整版产线已贯通（`build_cli/mac`） | 要完整版；**完整版产线还没铺**（AGENTS.md:339「linux 待铺」），目前只能得到纯 Go 构建 |
| Live Photo 配对检测 | 文件系统不分大小写：`.MOV` 与 `.mov` 两个候选会命中同一个文件 | 同左 | 文件系统分大小写：只认后缀大小写完全一致的候选 |
| 输出文件被别的程序占用 | 删除、覆盖都会失败，`error_info` 是系统原话 | 可删可覆盖 | 可删可覆盖 |
| `verify/verify-image.sh` | 要 `stat -f%z` 转发垫片和系统 ffmpeg；`md5 -q` 在 Git Bash 里不存在（见 8） | 开箱可跑 | 有 ffmpeg 时基本可跑，`md5 -q` 要换成 `md5sum` |

### 纯 Go 构建与完整版（cgo 构建）的差别

| 能力 | 纯 Go（`CGO_ENABLED=0`，开发期的 `bin/zpack-dev`） | 完整版（`build_cli` 产线，交付的 `bin/zpack`） |
|---|---|---|
| 12 个子命令的标准格式路径 | 全部可用 | 全部可用 |
| 输入是 exr / hdr / rgbe / jxl | exit 1，`EXR/HDR/JXL 需完整版（CGO 构建）` | 可读 |
| `convert` 的输入是 heic / heif / avif | exit 1，`HEIC/HEIF/AVIF 解码需要完整版（build_cli 产线 CGO 构建）；纯 Go 构建不含 astiav/libav 引擎` | 可转出 |
| `image info` 遇到 HEIC | Live Photo 载荷 | **相同**——完整版也不解码 |
| `image livephoto` | 可用（纯文件操作） | 可用；错误文案略有不同（`拷贝视频失败: …`，纯 Go 是 `读取视频失败` / `写入视频失败`） |

纯 Go 构建只是开发期快速编译循环的工具，不是交付物；交付的 `bin/zpack` 恒为完整版（AGENTS.md:188-190）。

代码上靠 build tag 分层：`//go:build cgo` 对应 `image/hdr_cgo.go`、`image/heic.go`，`//go:build !cgo` 对应 `image/hdr_nocgo.go`、`image/heic_nocgo.go`。下面这几样在两侧**各有一份**，改名必须两边一起改：

| 同名的东西 | cgo 侧 | 非 cgo 侧 |
|---|---|---|
| `hdrFormatByExt`（HDR 族扩展名表） | image/hdr_cgo.go:303-314 | image/hdr_nocgo.go:29-40 |
| `IsHEIC`（HEIC 族扩展名） | image/heic.go:24-32 | image/heic_nocgo.go:12-15 |
| `LivePhotoInfo` 类型 | image/heic.go:175-179 | image/heic_nocgo.go:45-49 |
| `DetectLivePhoto` / `ExtractLivePhotoVideo` | image/heic.go:182-213（自己实现了一份） | image/heic_nocgo.go:51-58 → image/livephoto.go:11-52 |

`image/livephoto.go` 没有 build tag，两种构建都会编译它；但 cgo 构建用的是 `heic.go` 里的那份，`livephoto.go` 里的实现只被非 cgo 构建调用。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 落位与分层

- 顶层包 `image/` 是引擎，`cli/image/` 是命令面。命令面只做「解析校验 → 调引擎 → 呈现结果」，引擎一律以别名 `engine` 引入（cli/image/doc.go:3）。这沿用的是「新功能家族的落位惯例」：引擎纯函数 + 文件、命令面独立、纯构建能跑门禁（AGENTS.md:200）。
- 引擎不 import cobra、不碰 stdout、不 `os.Exit`，错误用 error 返回，过程日志经回调注入。目前只有 `CompressTargetSize` 带 `logf` 参数（nil 即静默，image/compress_target.go:47-49）。
- 引擎的错误文案就是对外的 `error_info`，改文案要同步 `verify/verify-image.sh` 里 grep 的关键字（如「只缩尺寸」、`-mm-w`）。
- 结果载荷都是 struct，字段声明序就是 JSON 键序。GUI 是经 JSON 契约驱动 CLI 的薄壳，字段改名或增删都是对 GUI 的破坏性变更。
- **引擎被 media 复用**：`media/cover.go:36,48` 和 `media/insertframe.go:34` 调 `zimage.ReadImage`，`media/screenshot.go:31` 调 `zimage.WriteImage` 并用 `IMAGE_DEFAULT_QUALITY`。改这两个函数的签名或行为会波及视频封面、插帧、截图三个功能。注意它们用的是**不定向**的 `ReadImage`，不是 `ReadImageOriented`。

### 6.2 「读取即定向」为什么是铁律

zpack 的输出不携带 EXIF（Go 的 JPEG 编码器只写 SOI / DQT / SOF0 / DHT / SOS / EOI，没有任何应用段）。如果读取时不把方向物化成像素，一张 orientation = 6 的照片 convert 之后标签没了、像素却还横着，产出就是横倒的，而且再也转不回来。所以规则是：**处理路径一律先把像素物化成观看态**（AGENTS.md:161，image/image.go:75-85）。

- `image info` 是唯一的例外，它只报告、不处理。
- 方向是否正确，验收里用 ffmpeg autorotate 的四象限逐通道比对守着（verify/verify-image.sh:148-194）。
- 新增任何「读图再写出」的路径，必须用 `ReadImageOriented`；直接用 `ReadImage` 就会重现上面那个 bug。
- `ReadOrientation` 只认扩展名（image/exif.go:25-31）。内容是 JPEG 但扩展名不是 `.jpg` / `.jpeg` / `.jfif` / `.tif` / `.tiff` 的文件，方向不会被读（当作 0）。
- HEIC 路径没有处理方向：`ConvertHEIC` 直接解码首帧，不解析 HEIC 的旋转元数据（`irot`）【待确认】——没有带旋转信息的 HEIC 夹具，未实测竖拍 HEIC 转出来是否横倒。

### 6.3 透明：按编码器的真实 alpha 能力划线

定案（AGENTS.md:66；image/new.go:55 的注释）是「不静默丢」：目标格式的编码路径会丢 alpha 时明确报错并引导——代码注释给的理由是「jpg 无通道、bmp 32bit 的 alpha 字节查看器普遍忽略、gif 调色板量化丢弃」（实际 `x/image/bmp` 对 NRGBA 源写的是 24 位，根本没有 alpha 通道）；png / webp / tiff 保真（webp 的透明做过像素级验证）。

这条线只画在「透明底 + 无 alpha 格式」上，显式给带 alpha 的 `#RRGGBBAA` 就绕过去了（见 4.1.1）。换编码器（比如 WebP 改用有损 VP8、JPEG 换实现）时，这张能力表要重新验证，`formatLosesAlpha`（image/new.go:84-90）要同步。

### 6.4 `compress --target-size` 的策略是怎么定下来的

- **2026-11-08 用户定案「双优先级」**（AGENTS.md:67；cli/image/image.go:160 的注释）：
  - `size`（缺省）：质量钉在 90 的视觉无损档，靠减少像素数换体积。理由是 5000×6000 这类巨图体积大的主因是像素多。
  - `quality`：保像素优先，全尺寸降质到 q50 下限，不够再缩尺寸、回拉质量。
- **2026-11-22 用户扩档为四档**（image/compress_target.go:38-45）：加了 `size-only` 和 `quality-only` 两个「绝不越界」的档——只缩尺寸的绝不降画质，只降画质的绝不缩尺寸，装不下就报错，**不偷偷替用户做另一种牺牲**。旧值 `size` / `quality` 保留为别名。AGENTS.md:67 的输出表还停在双优先级时代（见 9）。
- **q50 是画质下限**：JPEG 质量低于 50 视觉上明显劣化——5000×6000 压到 1MB 时纯降质会变马赛克，宁可缩尺寸也保画质（image/compress_target.go:18-20）。
- **5% 是最小缩放**：再小连缩放都救不了（image/compress_target.go:22-23）。
- **只支持 JPEG**：要做二分，前提是质量与体积单调相关；webp 是纯 Go 无损 VP8L，质量参数无效；PNG / GIF / BMP / TIFF 天生无损（image/compress_target.go:1-2、32-36）。
- **源已达标就直拷贝**：不为了「压缩过了」而白损一轮画质（image/compress_target.go:29-30、60-66）。
- **缩放每次都从原图起**：`scaleImagePercent(img, mid)` 的源永远是原图，避免重复采样累积（image/compress_target.go:82 的注释）。
- **引擎层不回填 `target_bytes`**（纯函数纪律），由命令面在成功后补（cli/image/image.go:229-231）。
- `size-only` 成功时 `strategy` 也报 `size-first`，两者在 JSON 里只能靠「有没有 `last-resort`」区分。消费方不要指望从 `strategy` 看出用户选的是哪一档。
- 耗时：缩放二分最多 7 轮（96 个百分点），兜底链再加 7 轮质量二分，每轮都是全图缩放 + 内存编码。巨图要等十几秒到分钟级，而 image 没有进度输出。
- GUI 的四个单选项与 CLI 的四个新名一一对应（ui/gui/main.go:3301-3306），缺省 `size-first`（ui/gui/main.go:5081）。改取值名要同步 GUI。

### 6.5 缩放：插值与「只降不升」

- 插值统一用 CatmullRom——`x/image/draw` 官方推荐的高质量缩放（image/convert.go:211）。resize、目标体积压缩、merge 共用同一条，换插值三处一起变，验收里的像素比对也要跟着改。
- `--max` 是「只降不升、保比例」，同 media 家族的 FitWithinBox 语义（image/convert.go:234）；`--percent` 只收 1–99，也是缩小语义。整条 resize 的定位是「降像素保观感——同视频 `--screen` 语义」。`--width` / `--height` / 物理尺寸则会放大。
- 物理尺寸族的取舍（cli/image/image.go:382-384 的注释）：`WxH` 双数值必须两侧都给（结果强制拉伸）；只定一侧是显式旗标 `--mm-w` / `--mm-h` 的事——**不做「给一个数、猜是宽还是高」的隐式语义**。所以 `--mm 55` 报错并把人引向 `-w` / `-h`。
- `--ppi` 是像素密度的正名，`--dpi` 是惯称别名，两个都收，语义是同一个换算率（cli/image/image.go:409）。
- 缺省 300 的来历：印刷质量的像素密度标准（image/convert.go:284）。
- PPI 只是换算率，不写进文件（见 9）。

### 6.6 HDR：为什么要分「直通」和「滚降」

- 浮点图（EXR、Radiance HDR）的值可以大于 1.0。如果一律压曲线，普通 SDR 图转一圈就回不到原样。所以先扫全图取最大值：不超过 1.05 判为 SDR 值域原值直通，保往返逐位一致；超过了才走 Reinhard（image/tonemap.go:4-6）。
- 选扩展 Reinhard 曲线：白点映射到 1.0，高亮平滑滚降，不硬裁。
- 两遍处理（先扫后转）是为了确定性：场景白点取全图最大值，不依赖像素处理顺序（image/hdr_cgo.go:178）。
- 数学是纯函数、表驱动单测守着（image/tonemap_test.go），端到端由 python 手造的 EXR 配手算值验证（verify/verify-media.sh:288-318）。改曲线必须同时改这两处。
- 色调映射逐通道做，不在亮度域（v1；亮度域要先换色彩空间，缓期）。ICC → sRGB 也缓期——纯 Go 没有零依赖的 ICC 解析（AGENTS.md:161）。
- 走 astiav 的理由：零新增依赖，完整版自带的解码能力（image/hdr_cgo.go:3-6）。纯构建侧是明确报「需完整版」的桩，不静默失败。

### 6.7 HEIC / AVIF：一个潜伏 bug 的教训

HEIC 本质是 ISOBMFF 容器 + 单帧 HEVC，FFmpeg 的 mov 解复用器加 hevc 解码器天然覆盖（image/heic.go:3-4）。

`ConvertHEIC` 早先调用的 `execFFmpeg` 是个空壳：代码在、调用链在，功能却从没工作过，HEIC / AVIF 转换一直是坏的，直到 2026-11-08 才被发现（AGENTS.md:161）。修法是改走 astiav 首帧解码 + sws，并补上逐位比对的验收（verify/verify-media.sh:319-331）。

**启示**：接入新格式必须有「造夹具 → 跑命令 → 比对像素」的闭环用例，只看 exit 0 或「文件非空」发现不了这类问题。image/heic.go:108-110 还留着三行当时的备选方案注释，不是现行实现，读代码时别被带偏。

### 6.8 astiav 的三个坑（AGENTS.md:229-231）

- **`ImageBufferSize` / `ImageCopyToBuffer` 的 align 必须 ≥ 1**。传 0 会得到 EINVAL → size 0 → 空切片越界 panic。代码里四处都传 1（image/hdr_cgo.go:158、163、239、244）。
- **sws 比对的参照要选对**。sws 缺省 FAST_BILINEAR，ffmpeg CLI 缺省 Bicubic，色度边缘会分歧；rgb24 的 CLI 路径自带抖动。逐位等价的正确参照是 rgba64le。所以代码里显式指定 Bicubic（image/hdr_cgo.go:230），验收拿 rgba64le 比。
- **解码循环不能「单包直收」**。libjxl 首包不吐帧（EAGAIN），要「收帧–读包」循环，EOF 时送空包冲洗；帧要跨函数传递时用 `Ref()` 克隆，否则内部帧被释放后调用方手里是悬空的（image/hdr_cgo.go:97-139）。

### 6.9 手写解析留下的坑

- 各子命令的旗标解析是复制粘贴展开的，同一种校验写了很多遍，措辞已经漂移：convert 的 `--quality` 越界文案是「需要 1-100」，别处是「需 1-100」；resize 的 `--quality` 漏了 else 分支，非法值静默吞掉（cli/image/image.go:521-528）。再加旗标时不要复制新的一份，但抽公共函数时每条文案都得原样保留——验收在比。
- `--priority` 分支里 `ErrorPrint` 之后还跟着 `return`（cli/image/image.go:189、196），是不可达代码，因为 `ErrorPrint` 即 exit。`--max` 分支里的 `break` 只跳出 switch、不跳出 for 循环（cli/image/image.go:509、515），同样是因为 `ErrorPrint` 会 exit 才没出事。别把这两处改成「先收集错误、最后统一报」的写法。
- `imageInfo` 对 HEIC 的分支和 `imageLivePhoto` 的「没找到配对视频」分支，都是靠 `JSONPrint` 的「打印即 exit」提前退出的（cli/image/image.go:359-374、609-618）。`JSONPrint` 之后还有代码是有意为之。输出层这个性质一旦改变，这里会打出两条最终输出。
- 用法文案和实际分派表不同步：`Use` 与 default 分支的文案停在「convert / compress / info」三个子命令的时代（cli/image/image.go:26、56），根帮助里的 Short 写着「纯 Go 零 cgo」（见 9）。

### 6.10 与 GUI 的耦合

- GUI 调的是 CLI 二进制：`image convert`（ui/gui/main.go:2799）、`image compress … --target-size … --priority … --output …`（ui/gui/main.go:3392-3394）、`image new … --color … --ppi …`（ui/gui/main.go:3465-3472）。
- GUI 的前端校验是照引擎的解析规则重写的：`validateImageSize` / `validateImageColor` / `validatePPI`（ui/gui/validate.go:195-249），原则是「GUI 判错的引擎一定也判错，宁可漏判不可误拦」。改尺寸、颜色、PPI 的解析规则必须同步这三个函数。目前已有一处差异：引擎接受不带 `#` 的十六进制颜色，GUI 要求必须带 `#`（漏判方向，安全）。
- GUI 压缩页会把非 JPG 也发给 CLI，靠每个任务各自报错来提醒用户（ui/gui/main.go:3351-3361）。

### 6.11 改动时容易破坏的东西

- `ReadImageOriented` 的使用面——新路径误用 `ReadImage` 就是方向 bug。
- `ImageResult` / `MergeResult` / `ImageInfo` 的字段名与声明序；带 `omitzero` 的字段赋了 0 / false 就等于从 JSON 里删掉。
- 引擎错误文案（验收脚本在 grep）。
- build tag 两侧的同名函数与扩展名表（见第 5 节的表）。
- `--max` 的只降不升、`--percent` 的 1–99 上限。
- 各编码器的 alpha 行为依赖标准库与 `x/image` 的具体版本。
- 验收脚本的 `md5 -q` 与 `stat -f%z` 是 mac 专属写法（见 8）。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面 | `cli/image/image.go` | 12 个子命令的解析、校验、调用与输出（659 行） |
| 命令面包说明 | `cli/image/doc.go` | 包注释；引擎别名约定 |
| 装配 | `cli/root.go:70` | `image.NewImageCmd()` 挂到根命令 |
| 引擎总入口 | `image/image.go` | `ReadImage` / `ReadImageOriented` / `WriteImage`、`ImageResult`、两个缺省质量常量、支持格式表 |
| 引擎 · 新建 | `image/new.go` | `NewBlank`、尺寸三形态与颜色解析、纸型表、色名表、`formatLosesAlpha` |
| 引擎 · 转码 / 信息 / 缩放 | `image/convert.go` | `Convert` / `Compress` / `Info` / `Resize`、`ImageInfo`、cm / inch ↔ 像素换算 |
| 引擎 · 目标体积压缩 | `image/compress_target.go` | `CompressTargetSize` 与四档策略、两个二分、缩放 |
| 引擎 · 几何变换 | `image/transform.go` | `Transform`、像素重排、`ApplyOrientation` |
| 引擎 · 拼接 | `image/merge.go` | `Merge`、`MergeResult` |
| 引擎 · EXIF 方向 | `image/exif.go` | `ReadOrientation`（JPEG APP1 / TIFF IFD0 最小解析） |
| 引擎 · 色调映射 | `image/tonemap.go` | 纯函数：`TonemapReinhard` / `NeedsTonemap` / `FloatToByte` |
| 引擎 · HDR（cgo） | `image/hdr_cgo.go` | astiav 首帧解码、浮点与整数两条像素路径、HDR 探测 |
| 引擎 · HDR 桩（非 cgo） | `image/hdr_nocgo.go` | 报「需完整版」；`hdrFormatByExt` 的另一份 |
| 引擎 · HEIC（cgo） | `image/heic.go` | `ConvertHEIC`、Live Photo 探测与提取 |
| 引擎 · HEIC 桩（非 cgo） | `image/heic_nocgo.go` | `ErrHEICNeedsFullBuild`；`IsHEIC` 的另一份 |
| 引擎 · Live Photo 共享 | `image/livephoto.go` | 配对检测与拷贝（无 build tag，只有非 cgo 构建调用） |
| 测试 | `image/tonemap_test.go` | 引擎包里唯一的测试文件，只测 tonemap 数学 |
| 前端校验（GUI） | `ui/gui/validate.go:195-249` | 尺寸 / 颜色 / PPI 三条校验，照引擎规则重写 |
| 验收脚本 | `verify/verify-image.sh` | 标准格式路径，纯 Go 即可 |
| 验收脚本 | `verify/verify-media.sh:271-357` | HDR / AVIF / JXL 解码，需完整版 |
| 公共底座 | `internal/cliutil`、`internal/stdout`、`internal/cmdutil` | help 守卫、输出信封、旗标小件（见 conventions.md） |

## 8. 验收用例

### `verify/verify-image.sh`（标准格式路径，纯 Go 构建即可）

夹具由系统 ffmpeg 的 `testsrc2` / `mandelbrot` 生成，开头三个是 640×480 的 png / bmp / tiff，后面另造一张 1920×1080 的 png。按脚本的分节：

| 分节 | 覆盖的内容 |
|---|---|
| convert（16–29 行） | png → jpg / png / webp / bmp / tiff 各验 exit 0 与产出非空；bmp → png 体积 < 100KB；webp 输出比 png 小 |
| compress（31–36 行） | jpg `--quality 50` exit 0 且体积小于原 |
| info（38–43 行） | exit 0；`format` = png、`width` = 640、`height` = 480 |
| resize（45–64 行） | `--width 640` → 640；`--percent 50` → 960；`--max 800x600` → 800；缩放同时转 webp；框比图大时不放大；不给缩放方式 exit 1 |
| 物理尺寸缩放（66–100 行） | `--cm-w 5.5 --ppi 300` → 650；`--cm 3.5x4.5` → 413×531；`--cm-w 3.5 --ppi 600` → 827；`--inch-w 2 --dpi 72` → 144；`--mm-h 45 --dpi 100` 高 177；`--mm 35x45 --dpi 100` → 138×177；`--mm-w 55 --mm-h 45` → 217×177；`--ppi` 与 `--dpi` 等价；`--mm 55` 单值 exit 1 且文案指向 `-mm-w` |
| 几何变换（102–123 行） | 五个变换各与 ffmpeg 等效滤镜（`transpose=2` / `transpose=1` / `transpose=1,transpose=1` / `hflip` / `vflip`）做像素 md5 相等；rotate-left 宽高对调为 480×640；mirror 输出 jpg 非空；输入不存在 exit 1；缺参数 exit 1 |
| merge（125–146 行） | 纵向 exit 0 与 `direction` 字段；总高 2520；宽 = 基准宽 1920；`--reference` 换基准后总高 840；横向总宽 3360；jpg 输出非空；退化三项（位置参数不足、基准不在列表、非法方向） |
| EXIF Orientation（148–194 行） | python 手造一张带 APP1（orientation = 6）的四象限 JPEG：`info` 报 `orientation:6` 与 300×400 的显示尺寸；无标签的图不出现 `orientation`；`convert` 产出 300×400；四象限取色与 ffmpeg autorotate 逐通道差 ≤ 4 |
| compress --target-size（196–248 行） | 源图 30% 的目标：exit 0、体积达标、有 `quality`；缺省策略是 `size-first` 且质量 90、尺寸已缩；`--priority quality` 的策略是 `quality-only` 或 `scale-then-quality`，且输出宽度 ≥ size 优先的；已达标直拷（`already_fit:true`，`cmp` 字节一致）；1KB 目标走 `last-resort` 且 `quality_degraded:true`；100B 不可达 exit 1；png 源 exit 1；与 `--quality` 互斥 exit 1；size-only 质量钉 90、1KB 目标 exit 1 且文案含「只缩尺寸」；quality-only 分辨率不变、100B exit 1；旧别名 `size` / `quality` 仍可用；非法体积与非法优先级 exit 1 |
| image new（250–290 行） | 像素 800×600；png 透明像素是 `0 0 0 0`；纯色 red；hex 色；`55x90mm --ppi 300` → 650×1063；`a4 --ppi 72` → 595×842；`a4l` → 842×595；jpg 白底可行；jpg + 透明 exit 1；jpg / webp / bmp / tiff / gif 纯色都产出非空；webp 透明像素级验证；tiff 透明 **只验 exit 0**；jpg / bmp / gif 透明拒绝 exit 1；非法颜色 exit 1；非法尺寸 exit 1 |
| 退化（292–297 行） | 不支持的输出格式、输入不存在、`info` 输入不存在、`--quality 0` 都 exit 1；`--help` exit 0 |

按脚本静态清点约 110 项断言（AGENTS.md:183 记的「65 用例」是旧数字；以脚本最后打印的「图片门禁：通过 N」为准）。

这个脚本的三个已知问题：

- **是按 mac 写的**：用了 `stat -f%z` 和 `md5 -q`。Windows 的 Git Bash 里 `stat` 要转发垫片，而 `md5` 根本不存在（只有 `md5sum`）。`img_xval` 函数（103–109 行）对两个命令的 stderr 做了丢弃，`md5` 找不到时两边都得到空串，**五个像素级比对会空过**。在这台 Windows 机器上已确认 `md5` 不存在。
- **需要系统 ffmpeg / ffprobe**，没装就跑不了（AGENTS.md:288 记录过实测机没装）。
- `verify/verify.sh` 里只有两组 image 用例，其余图片行为全在 `verify-image.sh`：「失败时不得删文件」一节的 3 条，和「image --quiet 生效」一节的 7 条（2026-11-24 新增：`resize` 不带 `--quiet` 有换算说明、带 `--quiet` 时 exit 0 / stderr 只剩开始执行一行 / stdout 仍是成功 JSON；`compress --target-size --quiet` 的 exit 与 stderr 行数）。夹具用 `image new` 造，纯 Go 构建即可跑。

### `verify/verify-media.sh`（需完整版 `bin/zpack`）

| 分节 | 覆盖的内容 |
|---|---|
| HDR 图片解码（271–331 行） | EXR：`image info` exit 0、`format` = exr、宽 160；SDR 值域的 EXR 解出来与 ffmpeg rgb24 逐像素差 ≤ 2；python 手造的真 HDR EXR（R=4.0 G=2.0 B=1.0）经色调映射得到 255 / 191 / 135（±1）；AVIF 经 HEIC 通道解码，与 ffmpeg rgba64le 逐位相等 |
| JXL 解码链（349–357 行） | 用内置 ffmpeg 编一个 JXL 夹具，再 `image convert` 解回来，尺寸等于源 |

### 两套门禁都没覆盖的

- `image livephoto`（一个用例都没有）；HEIC 文件的 `image info` 载荷。
- Radiance HDR（`.hdr` / `.rgbe`）——没有夹具，只有 EXR。
- EXIF 方向只测了 6；TIFF 的方向、方向 2 / 3 / 4 / 5 / 7 / 8 都没测。
- resize 的 `--height`、`--inch WxH`、cm 与 inch 混用、`--quality` 非法值。
- `image new` 的 tiff 透明只看了 exit 0，没看像素；带 alpha 的 `#RRGGBBAA` 完全没测。
- 纯 Go 构建下两条「需完整版」的桩文案（`verify-media.sh` 发现是纯构建会整体拒绝运行）。
- 大端浮点分支、10 位整数分支。

### 手动验证

```bash
zpack image --help; echo $?                      # 0
zpack image; echo $?                             # 1，用法文案
bin/zpack-dev image convert a.exr out.png; echo $?   # 纯构建：需完整版，exit 1
bin/zpack image convert a.exr out.png; echo $?       # 完整版：exit 0
```

带方向的手机照片手动验证：`zpack image info 照片.jpg` 看 `orientation`，再 `zpack image convert 照片.jpg 转出.png`，用任意看图软件确认方向是正的。

## 9. 未结项与已知限制

### 没做的

- **`image info` 不解码 HEIC**，完整版上也只报 Live Photo 状态，并且照样打印「像素解码需完整版」——这句话在完整版上是错的（cli/image/image.go:359-374）。载荷是手搭的 map，`format` 对 `.heif` / `.avif` 也写死成 `heic`。
- **HEIC 族只能 `convert`**：resize、compress、几何变换、merge 遇到 HEIC 都报 `无法解码图片 …: image: unknown format`（完整版也一样）。
- **Radiance HDR 没有验收夹具**。
- **没有 PPI 密度输出**：`--ppi` 只是换算率。
- **ICC → sRGB 色彩管理缓期**；色调映射只做了逐通道的 v1。
- **没有进度输出**：`--target-size` 对巨图可能要跑分钟级，stderr 只有一两行策略说明。

### 已知不对但暂时没改的

- **`image livephoto` 的输出是双重信封**（cli/image/image.go:610-634）：命令面手搭了一层 `{"is_success":true,"data":{…}}` 传给 `JSONPrint`，输出层又包一层，实际输出是 `{"is_success":true,"data":{"data":{…},"is_success":true}}`。按契约读 `data.video_path` 什么都读不到，真载荷在 `data.data` 里。修法是只把内层字段交给 `JSONPrint`，并改成 struct。 **已修复（118dde3）**
- **失败时会误删文件**（见 4.3）：输出路径上原有的文件、以及就地压缩时的源文件。 **已修复（118dde3）**

- **`resize --quality` 的非法值被静默吞掉**（cli/image/image.go:521-528 没有 else 分支），违反「数值非法 → 报错」的约定。其它子命令都报。
- **`resize` 的物理尺寸日志里，按比例的那一侧显示成 0**（cli/image/image.go:553、569）：日志打在引擎算出另一侧之前。纯显示问题。
- **`--output-dir` 没有真正实现**（`livephoto`），靠位置参数碰巧成立。
- **`image new` 不校验尺寸上限**：`image new x.png 100000x100000` 会直接去分配几十 GB 内存，结果不是契约格式的错误【待确认】未实测。
- **`size-only` 成功时 `strategy` 报 `size-first`**，与 `size-first` 无法区分。
- **疑似缺陷，10 位整数像素路径**（image/hdr_cgo.go:248-257）：取 rgba64le 每通道的**低字节**当 8 位值。8 位源转成 16 位后高低两个字节相同，所以现在没问题；10 位源（10bit 的 AVIF 或 HEVC HEIC）的低字节不含最高 2 位，颜色应该会错【待确认】——没有 10 位夹具，未实测。
- **疑似缺陷，大端浮点分支**（image/hdr_cgo.go:166-176）：先按大端读出 32 位值再做字节反转，结果等于按小端去读大端数据，位序不对。OpenEXR 规格只定义小端，Radiance 与 libjxl 解码器也只输出小端，这个分支实际走不到【待确认】。
- **`image new` 的 `#RRGGBBAA` 绕过透明检查**，jpg / bmp / gif 得到黑图或变暗的纯色而不报错（见 4.1.1）。这是读代码得出的，建议补一条验收用例实锤。
- **`--target-size` 的体积解析不防溢出**：`值 × 1024³` 超出 int64 时会回绕成负数或错值，没有检查（cli/image/image.go:638-659）。
- **验收脚本 `verify-image.sh` 在 Git Bash 上像素级比对空过**（见 8）。
- `verify-image.sh` 里有几处注释与实际夹具不符（如 129 行写「src 800x600」，实际是 640×480；144 行注释写「单图」，实际验的是位置参数不足）。

### 代码与文档不一致的地方

| 文档说法 | 代码现状 | 出处 |
|---|---|---|
| 根帮助 Short：「图片格式转换与压缩（jpg/png/webp/bmp/tiff/gif 互转；纯 Go 零 cgo）」 | HDR 族与 HEIC 族的解码走 cgo（`hdr_cgo.go`、`heic.go`），不是「零 cgo」 | cli/image/image.go:27 |
| `Use: "image <convert\|compress\|info> [参数]"`，default 分支的用法文案也只列三个子命令 | 实际有 12 个子命令 | cli/image/image.go:26、56 |
| `image new` 的输出示例 `{"width":N,"height":N,"target_format":"png","size_bytes":N,…}` | 字段名是 `output_size_bytes`，没有 `size_bytes` | AGENTS.md:66 对 image/image.go:37-39 |
| `image compress --priority size\|quality`，只讲双优先级 | 接受 6 个值、四档策略（2026-11-22 扩档） | AGENTS.md:67 对 cli/image/image.go:193 |
| `rotate-left …` 的输出示例里有 `size_bytes` | 字段名是 `output_size_bytes` | AGENTS.md:69 对 image/image.go:39 |
| 「PPI 密度元数据（JFIF/pHyS）=打印尺寸契约，烤入路径=`resize --ppi`」 | 代码里没有任何写 JFIF 密度或 pHYs 的地方；`--ppi` 只参与像素换算。Go 的 JPEG 编码器根本不写 JFIF 段 | AGENTS.md:161 对 image/image.go:88-112 |
| 「JXL 待 ffmpeg 构建带 libjxl 再收编」 | `.jxl` 已经按扩展名分流到 astiav 通道，并有验收用例 | AGENTS.md:161 对 image/hdr_cgo.go:310、verify/verify-media.sh:349-357 |
| 输出表只列了 `new` / `compress` / `info` / 几何变换 / `merge`，没有 `resize` 与 `livephoto` | 两个子命令都在 | AGENTS.md:66-78 |
| 「`verify/verify-image.sh`：65 用例」 | 现在约 110 项断言 | AGENTS.md:183 |
| 「`verify-image.sh` 纯 Go 即可」、脚本头注释「image 零 cgo」 | 标准格式路径确实纯 Go 可跑；但 HDR / HEIC 族的解码走 cgo，那部分只有 `verify-media.sh` 守着 | AGENTS.md:183、verify/verify-image.sh:2 |
| `image/image.go` 包注释「输入: jpg/jpeg/png/gif/bmp/tiff/tif/webp；输出: …」，「CGO_ENABLED=0 可用」 | 输入还包括按扩展名分流的 HDR 族与 HEIC 族；输出表里也接受 `.tif` | image/image.go:1-3、52-55 |
| `cli/tools/skill.md` 写 `--priority <size\|quality>` | 四档取值 | cli/tools/skill.md:135 |
| GUI 常量注释「打印密度上限 2400（…越界引擎会静默回 300）」 | 命令面对越界的 `--ppi` 是报错退出，不是静默回 300 | ui/gui/validate.go:22 对 cli/image/image.go:79-83 |
| `ImageResult.Quality` 的注释「命中质量（0=直拷贝）」、`Strategy` 的注释只列了 `quality-only / scale-then-quality / last-resort` | 直拷贝时 `quality` 是 0 且被 `omitzero` 省略，不会输出 0；`strategy` 还有 `size-first` | image/image.go:43、46 |

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 修复：命令失败后不再删除输出路径上原有的文件（含就地压缩失败删源文件）；`livephoto` 不再输出双重信封 | 118dde3 |
| 2026-11-24 | image 全族认 `--quiet`（2026-11-24 用户定案补齐）：`compress --target-size` 的策略说明与 `resize` 的物理尺寸换算说明静默，去向由 `cliutil.EngineLogf(args)` 判定（2.1、3.6、8） | （提交后回填） |
