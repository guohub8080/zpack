# PDF（pdf）

> 对已有的 PDF 做页面级操作：合并、抽页、统一页面尺寸、旋转、读元信息——纯 Go，不碰 cgo。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

对**已有的 PDF** 做页面级操作：合并、抽页、统一页面尺寸、旋转、读元信息。引擎是 pdfcpu（v0.16.4，Apache-2.0）的一层薄封装，纯 Go、零 cgo（pdf/pdf.go:1）。

| 子命令 | 做什么 | 典型场景 |
|---|---|---|
| `pdf merge` | 把多个 PDF 按给定顺序合成一个 | 多份合同合一、按章节拼装 |
| `pdf extract` | 按页码规格抽出若干页，存成一个新 PDF | 只要第 3–5 页；截掉封面 |
| `pdf resize` | 把（选中的）页面统一到某个纸型或自定义尺寸，页面内容等比缩放、居中 | 扫描件、手机拍照生成的 PDF 各页大小不一，统一成 A4 |
| `pdf rotate-left` / `rotate-right` / `rotate-180` | 改页面的 `/Rotate` 属性，内容字节不动 | 拍照 PDF 方向不对 |
| `pdf info` | 页数、首页尺寸、体积、首页旋转角、是否含文字页；`--all-pages` 给逐页明细 | 动 resize 之前先体检 |

**不做什么：**

- 不改页面内容本身：不编辑文字、不加水印 / 页码 / 表单（pdfcpu 有这些能力，没有接）。
- 不生成 PDF，不与图片 / Office 互转，不 OCR。
- 不加密、不解密：没有密码旗标，加密的 PDF 读不了。
- `extract` 只产出一个文件，不按页拆成多份。
- 不支持镜像（mirror）：PDF 没有对应的页面属性，只能改写内容流，文字会镜像成不可读（pdf/rotate.go:1-3）。
- `resize` 是**几何缩放**（字号跟着等比缩），不是 Word 那种重新排版——所以选中的页里有文字页时默认拒绝，见 4.2 与 6.4。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack pdf merge <1.pdf> <2.pdf> [...] <输出.pdf>` | 合并。参数顺序就是页序，**最后一个位置参数是输出**，其余都是输入，至少 2 个输入（cli/pdf/pdf.go:54） |
| `zpack pdf extract <输入.pdf> --pages <规格> [--output <路径>]` | 抽页（cli/pdf/pdf.go:89） |
| `zpack pdf resize <输入.pdf> --size <尺寸> [--pages <规格>] [--force] [--output <路径>]` | 统一页面尺寸（cli/pdf/pdf.go:139） |
| `zpack pdf rotate-left\|rotate-right\|rotate-180 <输入.pdf> [--pages <规格>] [--output <路径>]` | 旋转（cli/pdf/pdf.go:194） |
| `zpack pdf info <输入.pdf> [--all-pages]` | 元信息（cli/pdf/pdf.go:243） |

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--pages <规格>`（别名 `-p`） | 选哪些页 | 页码规格，语法见下。extract 里必填；resize / rotate 里可选 | extract：必填；resize / rotate：全部页 |
| `--size <尺寸>` | resize 的目标页面尺寸 | 纸型名（可带 `L` / `P` 尾缀）或 `宽x高` 加可选单位，语法见下 | 必填 |
| `--force` | resize：选中的页里有文字页也照样缩放 | 布尔旗标，没有值 | 不给（遇到文字页就拒绝） |
| `--output <路径>`（别名 `-o`） | 输出文件。已存在则覆盖 | 目录必须已存在；**merge 不认这个旗标** | 同目录下的 `<名>-extract.pdf` / `<名>-resized.pdf` / `<名>-rotated.pdf` |
| `--all-pages` | info：在结果里附上逐页明细 | 布尔旗标 | 不给 |
| `--quiet` | 不输出过程日志（宽尾提示、`--force` 警告） | — | 不给 |
| `--help` / `-h` | 打印帮助，exit 0 | — | — |

**页码规格**（`ParsePagesSpec`，pdf/pdf.go:208；三个带 `--pages` 的子命令共用）：

| 写法 | 含义 |
|---|---|
| `5` | 第 5 页 |
| `1-3` | 第 1 到第 3 页（**两端都含**，页码从 1 起） |
| `8-` | 第 8 页到末页 |
| `-5` | 第 1 页到第 5 页 |
| `1-3,5,8-` | 逗号分隔，任意组合 |

规则：各段两端的空白会被去掉，但段**内部**不能有空格（`1 - 3` 非法）；结果**升序、去重**（`5,1,3` 等于 `1,3,5`）；区间末端超过总页数时自动截至末页，并在 stderr 提示一句；起始页超出、或单页不存在则报错。

**尺寸规格**（`ParsePageSizeSpec`，pdf/resize_page.go:36）：

| 写法 | 含义 |
|---|---|
| `a4`、`a4l`、`a4p`、`b5`、`a3`、`ansia` … | 纸型名，取自 pdfcpu 的纸型表（`types.PaperSize`，150 个键）。不分大小写 |
| 尾缀 `L` / `P` | 强制横向 / 纵向；不带尾缀时按「横页保持横、竖页保持竖」处理 |
| `210x297mm`、`21x29.7cm`、`8.5x11in` | 自定义宽高，单位 `mm` / `cm` / `in`（不分大小写） |
| `595x842` | 自定义宽高，不给单位就是 pt（1pt = 1/72 英寸） |

自定义尺寸的宽、高都必须是正数（可以是小数）；分隔符 `x` 不分大小写。

常用纸型的尺寸（pt，取自 pdfcpu 的表；带 `L` / `P` 尾缀时按需对调宽高）：

| 纸型 | 宽 × 高（pt） | 纸型 | 宽 × 高（pt） |
|---|---|---|---|
| `a3` | 842 × 1191 | `b4` | 709 × 1001 |
| `a4` | 595 × 842 | `b5` | 499 × 709 |
| `a5` | 420 × 595 | `c4` | 649 × 918 |
| `a6` | 298 × 420 | `c5` | 459 × 649 |
| `ansia`（= Letter） | 612 × 792 | `ansib`（= Tabloid） | 792 × 1224 |
| `ansic` | 1224 × 1584 | `archa` | 649 × 865 |
| `archb` | 865 × 1296 | `archc` | 1296 × 1729 |

**查不到的纸型现在的绕法**（尺寸相同，用能查到的键或自定义写法代替）：

| 想要的 | 现在能用的写法 |
|---|---|
| Letter | `ansia` 或 `8.5x11in` |
| Legal | `8.5x14in`（或 `612x1008`） |
| Ledger（17 × 11） | `ansibl` 或 `17x11in` |
| Tabloid（11 × 17） | `ansib` 或 `11x17in` |
| Executive | `7.25x10.5in` |
| Half Letter / Statement | `5.5x8.5in` |
| Gov Legal / Folio | `8.5x13in` |
| Gov Letter | `8x10.5in` |
| Junior Legal | `5x8in` |

**纸型名的现状限制**：代码先把输入转成大写，再去 pdfcpu 的表里做区分大小写的查找（pdf/resize_page.go:42-49）。表里只有 97 个键本身是全大写的（`A0`–`A10`、`B` 系、`C` 系、`RA` / `SRA` 系、`D` / `RD` 系、`ANSIA`–`ANSIF`、`ARCHA`–`ARCHE3`、`JIS-B0`–`JIS-B12`、`F4`、`AB`、`B40`），这些能用；另外 53 个键带小写字母（`Letter`、`Legal`、`Ledger`、`Tabloid`、`Executive`、`Arch1` …），**现在一个都用不了**——`--size letter` 报的是「需纸型」的错误。详见 9。 **2026-11-24 已修复（提交 118dde3）**：纸型名不分大小写，先按整个名字找、找不到再剥尾缀——本段的限制描述仅作历史记录。

**带值的旗标后面跟的是什么就取什么**：`--pages`、`--size`、`--output` 的值是「紧接着的下一个参数」，哪怕它以 `-` 开头（所以 `--pages -5` 可用；`--size --force` 会把 `--force` 当成尺寸）。只有旗标排在最后、后面什么都没有时才报「缺少值」。

**`--quiet` 的覆盖面**：extract、resize、rotate 的过程日志都受它控制；`pdf info` 的那一行 stderr 日志**不受它控制**，照常输出（cli/pdf/pdf.go:265）。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

## 3. 输出契约

五个子命令成功时都是标准信封 `{"is_success":true,"data":{...}}`，exit 0。下面列的是 data 载荷的字段，**顺序就是代码里结构体的声明顺序**（`pdf info --all-pages` 例外，见 3.5）。

### 3.1 merge

```json
{"is_success":true,"data":{"output":"/abs/merged.pdf","inputs":["/abs/a.pdf","/abs/b.pdf"],"input_count":2,"page_count":5,"size_bytes":1536}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `output` | string | 输出文件的绝对路径 |
| `inputs` | string[] | 输入文件的绝对路径，顺序同命令行 |
| `input_count` | int | 输入文件个数 |
| `page_count` | int | 合并后的总页数（合并完重新数出来的，pdf/pdf.go:67） |
| `size_bytes` | int | 输出文件的大小 |

### 3.2 extract

```json
{"is_success":true,"data":{"input":"/abs/a.pdf","output":"/abs/a-extract.pdf","pages_spec":"1-3,5","pages":[1,2,3,5],"source_page_count":8,"size_bytes":2048}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `input` | string | 输入的绝对路径 |
| `output` | string | 输出的绝对路径 |
| `pages_spec` | string | 命令行里给的页码规格，原样回显（没有做任何规范化） |
| `pages` | int[] | **实际抽出的页码**，升序去重；宽尾被截至末页后，这里已是截至后的结果 |
| `source_page_count` | int | 源文件的总页数 |
| `size_bytes` | int | 输出文件的大小 |

### 3.3 resize

```json
{"is_success":true,"data":{"input":"/abs/scan.pdf","output":"/abs/scan-resized.pdf","size_spec":"a4","width_pt":595,"height_pt":842,"pages_spec":"2-4","pages":[2,3,4],"page_count":3,"total_pages":6,"size_bytes":20480}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `input` / `output` | string | 绝对路径 |
| `size_spec` | string | 命令行里给的尺寸规格，原样回显 |
| `width_pt` / `height_pt` | number | **目标**页面尺寸（pt）。纸型带 `L` 尾缀时是对调后的宽高；自定义尺寸是换算成 pt 的值（`210x297mm` → 宽约 595.28） |
| `pages_spec` | string | 原样回显。没给 `--pages` 时**不出现** |
| `pages` | int[] | 实际处理的页码。没给 `--pages` 时是全部页 |
| `page_count` | int | 实际处理的页数 |
| `total_pages` | int | 文档总页数 |
| `size_bytes` | int | 输出文件的大小 |

### 3.4 rotate

```json
{"is_success":true,"data":{"input":"/abs/a.pdf","output":"/abs/a-rotated.pdf","direction":"right","pages_spec":"2","pages":[2],"page_count":1,"total_pages":5,"size_bytes":1024}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `input` / `output` | string | 绝对路径 |
| `direction` | string | `left` / `right` / `180`（子命令名去掉 `rotate-`） |
| `pages_spec` | string | 原样回显；没给 `--pages` 时**不出现** |
| `pages` | int[] | 实际旋转的页码 |
| `page_count` | int | 实际旋转的页数 |
| `total_pages` | int | 文档总页数 |
| `size_bytes` | int | 输出文件的大小 |

### 3.5 info

```json
{"is_success":true,"data":{"input":"/abs/a.pdf","page_count":5,"width_pt":400,"height_pt":300,"width_mm":141.11111111111111,"height_mm":105.83333333333333,"size_bytes":1234,"rotation":90,"text_based":true}}
```

（例子里首页是 300×400 pt 的竖页、`/Rotate 90`，所以报出来的观看态是 400×300。）

| 字段 | 类型 | 含义 | 出现条件 |
|---|---|---|---|
| `input` | string | 绝对路径 | 总是 |
| `page_count` | int | 总页数 | 总是 |
| `width_pt` / `height_pt` | number | **首页**尺寸，单位 pt，已按 `/Rotate` 换算成观看时的宽高 | 总是 |
| `width_mm` / `height_mm` | number | 首页尺寸换算成毫米（pt × 25.4 ÷ 72） | 总是 |
| `size_bytes` | int | 文件大小 | 总是 |
| `rotation` | int | 首页的 `/Rotate` 角度（顺时针度数） | 不为 0 时 |
| `text_based` | bool | 文档里**至少有一页**声明了字体 | 为 true 时 |

带 `--all-pages` 时多一个 `pages` 数组，每项：

```json
{"page":2,"width_pt":300,"height_pt":400,"text":true}
```

| 字段 | 类型 | 含义 | 出现条件 |
|---|---|---|---|
| `page` | int | 页码，从 1 起，升序 | 总是 |
| `width_pt` / `height_pt` | number | 该页尺寸（已按 `/Rotate` 换算） | 总是 |
| `rotation` | int | 该页的 `/Rotate` 角度 | 不为 0 时 |
| `text` | bool | 该页声明了字体 | 为 true 时 |

**`--all-pages` 形态与普通 info 的两处不同**（cli/pdf/pdf.go:272-282）：

1. 载荷是现场拼的 map 而不是结构体，所以键**按字典序**排：`height_mm`、`height_pt`、`input`、`page_count`、`pages`、`rotation`、`size_bytes`、`width_mm`、`width_pt`——和上面不带 `--all-pages` 时的顺序不一样。
2. `text_based` 字段在这个形态里**没有**（逐页的 `text` 仍有）。

其余字段的含义和取值与不带 `--all-pages` 时相同。

### 3.6 stderr

- 统一开始日志：`[YYYY-MM-DD HH:MM:SS] zpack pdf 开始执行`（命令名恒为 `pdf`）。
- 宽尾提示（extract / resize / rotate）：`页码区间 <段> 超出末页，已截至第 <末页> 页（末页）`，每个越界的段一条。
- `--force` 警告（resize）：`--force：第 <页码列表> 页为文字页，按几何缩放放行——文字将等比缩放不重排`。
- info 固定一行：`PDF 元信息: <页数> 页, 首页 <宽>×<高>mm`（保留一位小数）。

宽尾提示和 `--force` 警告受 `--quiet` 控制；开始日志和 info 的那一行不受。merge 自己没有过程日志。

## 4. 行为细则

### 4.1 正常路径

#### merge

1. 收集所有不以 `-` 开头的参数。不足 3 个（至少 2 个输入 + 1 个输出）→ 用法错误。
2. 最后一个参数是输出，其余是输入。逐个检查输入文件存在，不存在就报 `PDF 不存在：<绝对路径>`（cli/pdf/pdf.go:75）。
3. 交给 pdfcpu 按顺序合并，中间不加分隔页（pdf/pdf.go:64）。失败 → 报错。
4. 合并完重新读一遍输出文件的页数，当作对产物的校验；读不了 → `合并产物校验失败`（pdf/pdf.go:67）。
5. 输出 JSON。

```bash
zpack pdf merge 封面.pdf 正文.pdf 附录.pdf 合订本.pdf    # 页序 = 参数序
```

#### extract

1. 必须恰好 1 个位置参数；文件要存在；`--pages` 必填。
2. 引擎先读源文件总页数，再按总页数解析页码规格（越界的末端在这里截至末页并发出提示）。
3. 把**截至之后的显式页码**交给 pdfcpu 抽页——原始写法里可能有越界的数字，不能原样传给 pdfcpu（pdf/pdf.go:100）。
4. 写出 `--output` 指定的文件，或同目录的 `<名>-extract.pdf`。
5. 输出 JSON。

```bash
zpack pdf extract 报告.pdf --pages 3-5,8           # → pages [3,4,5,8]，产出 报告-extract.pdf
zpack pdf extract 报告.pdf --pages 12- -o 尾章.pdf   # 第 12 页到末页
```

#### resize

1. 必须恰好 1 个位置参数，并且给了 `--size`。
2. 引擎里的检查顺序（pdf/resize_page.go:99-158）：解析尺寸 → 文件存在 → 读总页数 → 解析页码规格（发宽尾提示）→ 检测文字页 → 拦截判断 → 调 pdfcpu 缩放。
3. 每个选中的页（pdfcpu 的行为）：
   - 取该页的 CropBox（没有就取 MediaBox），连同 `/Rotate` 得到**观看态**的页面区域；
   - 定目标矩形：不带 `L` / `P` 尾缀时，如果源页面和纸型的横竖不一致，就把纸型的宽高对调（横页保持横、竖页保持竖）；带尾缀则强制用纸型的方向；
   - 把页面内容**等比缩放**到刚好装进目标矩形（放大或缩小都会做），并**居中**；带尾缀、方向又不一致时，内容还会被旋转 90° 以便塞进目标方向；
   - 写回新的 MediaBox，页面的 `/Rotate` 归零（原有的旋转被「烤」进内容的变换矩阵里）。
4. 写出 `--output` 指定的文件，或同目录的 `<名>-resized.pdf`。
5. 输出 JSON。

```bash
zpack pdf info 扫描件.pdf --all-pages                  # 先体检：哪些页尺寸不一、哪些页有文字
zpack pdf resize 扫描件.pdf --size a4                  # 各页统一到 A4，横页仍是横的
zpack pdf resize 扫描件.pdf --size a4p                 # 全部强制竖向
zpack pdf resize 扫描件.pdf --size 215x305mm           # 自定义尺寸（mm）
```

#### rotate

1. 必须恰好 1 个位置参数；`--pages` 可选，不给就是全部页。
2. 方向换算成 pdfcpu 的顺时针角度：`rotate-right` = 90，`rotate-left` = 270，`rotate-180` = 180（pdf/rotate.go:16-20）。
3. 每个选中的页：新的 `/Rotate` = （原来的角度 + 这次的增量）对 360 取模，结果落在 0–359。
4. 写出 `--output` 指定的文件，或同目录的 `<名>-rotated.pdf`。
5. 输出 JSON。

```bash
zpack pdf rotate-right 拍照.pdf                       # 全部页顺时针转 90°，产出 拍照-rotated.pdf
zpack pdf rotate-left 拍照.pdf --pages 3              # 只转第 3 页
```

#### info

1. 必须恰好 1 个位置参数；文件要存在。
2. 依次读：总页数 → 首页尺寸 → 首页的 `/Rotate` → 是否有文字页（pdf/pdf.go:160）。
3. 往 stderr 输出一行摘要。
4. 输出 JSON；带了 `--all-pages` 就再读一遍全页明细，并入同一个 JSON。

```bash
zpack pdf info 合同.pdf                                # 页数、首页尺寸、体积
zpack pdf info 合同.pdf --all-pages | zpack json get data.pages.2.width_pt
```

### 4.2 边界与退化输入

#### 页码规格

以一份 5 页的 PDF 为例（pdf/pdf_test.go 里有同样的表）：

| 写法 | 结果 |
|---|---|
| `3` | `[3]` |
| `2-4` | `[2,3,4]` |
| `4-` | `[4,5]` |
| `-2` | `[1,2]` |
| `1,3,5`、`2-3,5` | `[1,3,5]`、`[2,3,5]` |
| `3,1,3` | `[1,3]`（去重、升序） |
| `3-9` | `[3,4,5]` + 提示「页码区间 3-9 超出末页，已截至第 5 页（末页）」 |
| `-9` | `[1,2,3,4,5]` + 同样的提示 |
| `1-3, 5` | 段两端的空白被去掉，`[1,2,3,5]` |
| `6`、`6-9`、`6-` | 报错（单页不存在 / 起始页超出） |
| `4-2`、`1-0`、`-0`、`abc`、`0`、`-`、`1,,3`、`1,3,`、`1 - 3` | 报错 |

- 「末端超出」和「起始页超出」是两种待遇：末端超出是笔误的常见形态，截至末页并提示；起始页超出说明要的页根本不存在，没有可提取的页，直接报错（pdf/pdf.go:205-207）。
- 同一个 `--pages` 的写法用在 extract、resize、rotate 上，解析结果完全一样。

#### 尺寸规格

| 写法 | 结果 |
|---|---|
| `a4`、`A4` | A4 竖（595×842 pt），方向按页自适应 |
| `a4l` | 强制 A4 横（842×595 pt） |
| `a4p` | 强制 A4 竖 |
| `letter`、`legal`、`ledger`、`tabloid`、`executive` | **报错**（纸型表里的键带小写字母，查不到——已知缺陷，见 9） |
| `a99` | 报错：未知纸型 |
| `210x297mm`、`210X297MM` | 约 595.28 × 841.89 pt |
| `21x29.7cm` | 同上 |
| `8.5x11in` | 612 × 792 pt |
| `595x842` | 不带单位 = pt |
| `0x0`、`0x297mm`、`-10x100` | 报错：宽高必须是正数 |
| `210x297m`、`210x297ft` | 报错（不认识的单位不会被剥离，整个宽高解析失败） |
| 只给空白 | 报错：`--size 缺少值` |

- 自定义尺寸也有方向自适应：竖的 `210x297mm` 遇到横的源页，目标会被对调成横的。
- 检查顺序：用法 → 输入文件存在（`路径不存在`）→ 尺寸写法 → 读总页数 → 页码规格 → 文字页拦截 → 交给 pdfcpu。所以输入文件不存在时，尺寸写错了也先报「路径不存在」。

#### resize 的方向与文字页

三页混合尺寸的例子（验收用的夹具）：第 1 页 300×400（竖）、第 2 页 500×300（横）、第 3 页 200×900（竖）。

| 命令 | 结果 |
|---|---|
| `--size a4` | 第 1、3 页 → 595×842；第 2 页 → 842×595（保持横向） |
| `--size a4p` | 三页都是 595 宽；第 2 页的内容被转了 90° |
| `--size 210x297mm` | 首页 → 595.28 宽 |

| 选中页里的文字页 | 不带 `--force` | 带 `--force` |
|---|---|---|
| 没有 | 正常缩放 | 正常缩放，没有警告 |
| 有 | 拒绝，报错里点名是哪几页 | 照常缩放，stderr 警告点名是哪几页 |

「文字页」的判定是：这一页的资源里声明了字体（含从上级页面树继承来的资源），pdf/textdetect.go:36。这是启发式的——声明了字体就算，不管页面上有没有真的画字。

#### rotate 的叠加

| 页面原有角度 | `rotate-right` 后 | `rotate-left` 后 | `rotate-180` 后 |
|---|---|---|---|
| 0 | 90 | 270 | 180 |
| 90 | 180 | 0 | 270 |
| 180 | 270 | 90 | 0 |
| 270 | 0 | 180 | 90 |

角度回到 0 时，pdfcpu 仍会在页面字典里写一个显式的 `/Rotate 0`；`pdf info` 对 0 不输出 `rotation` 字段。

#### 其它

| 情况 | 行为 |
|---|---|
| merge 的输出路径与某个输入相同 | 成功时覆盖那个文件（pdfcpu 先写临时文件再替换） |
| merge 写了 `--output` | merge 不认这个旗标，但它的值是个不以 `-` 开头的参数，会被当成位置参数、恰好落在末位——所以恰好能当输出用。不要依赖这一点 |
| 同一个旗标给两次 | 后一次的值生效 |
| 不认识的旗标 | 静默忽略 |
| `--output` 就是输入文件自己（原地处理） | 成功时：pdfcpu 在同目录写临时文件再替换，结果正确。**失败时：输入文件会被删掉**，见 4.3 |
| 输出路径上已有文件 | 成功时覆盖 |
| 加密的 PDF | 没有密码入口，读取阶段就报错（`无法读取 PDF` 开头） |
| `pdf info` 遇到各页角度不同的文档 | `rotation` 只反映**首页**；逐页角度要看 `--all-pages` |
| 输入是目录、或不是 PDF | 读取阶段报 `无法读取 PDF: …` |

### 4.3 错误处理

所有失败都是 `{"is_success":false,"error_info":"..."}` + exit 1。「pdfcpu 错误原文」是 pdfcpu 的英文错误链，没有本地化。旗标的「缺少值」是在解析参数时报的，早于下面表里的其它检查。

**失败时的清理会删文件**：四个会产出文件的子命令，在引擎返回错误时都会执行 `os.Remove(<输出路径>)`（cli/pdf/pdf.go:82、:132、:187、:236），本意是清掉半成品。但 pdfcpu 自己从不在失败时碰目标文件（新文件直写、失败即删；目标已存在时写 `.<名字>.tmp-*` 临时文件、成功才替换），所以这一句 `Remove` 真正起作用的场合是：**引擎在调用 pdfcpu 之前就拒绝了（尺寸不认识、页码非法、文字页拦截…），而输出路径上恰好已经有一个文件**——那个文件会被删。输出路径就是输入文件时（原地处理），被删的就是输入。这一条是读代码得出的，没有实跑（实跑要牺牲一个文件）。

#### merge

| 场景 | `error_info` | 出处 |
|---|---|---|
| 位置参数不足 3 个 | `用法: pdf merge <1.pdf 2.pdf [3.pdf...]> <输出.pdf>\n参数序即页序；至少 2 个输入` | cli/pdf/pdf.go:67 |
| 某个输入文件不存在 | `PDF 不存在：<绝对路径>` | :75 |
| pdfcpu 合并失败（某个输入不是合法 PDF、损坏、加密、输出目录不存在…） | `合并失败: <pdfcpu 错误原文>` | pdf/pdf.go:65 |
| 合并后读不出页数 | `合并产物校验失败: <pdfcpu 错误原文>` | pdf/pdf.go:69 |

#### extract

| 场景 | `error_info` | 出处 |
|---|---|---|
| 位置参数不是恰好 1 个 | `` 用法: pdf extract <输入.pdf> [--pages 1-3,5,8-] [--output 输出.pdf]\n页码 1 起、闭区间、`8-`=到末页；缺省输出=同目录 <名>-extract.pdf `` | cli/pdf/pdf.go:116 |
| 输入不存在 | `路径不存在：<绝对路径>` | :120 |
| 没给 `--pages` | `需要 --pages（如 --pages 1-3,5,8-）——提取全部页请直接拷贝文件` | :123 |
| `--pages` 后面没有值 | `--pages 缺少值（如 1-3,5,8-）` | :98 |
| `--output` 后面没有值 | `--output 缺少值（输出 PDF 路径）` | :104 |
| 输入读不了（不是 PDF、损坏、加密） | `无法读取 PDF: <pdfcpu 错误原文>` | pdf/pdf.go:91 |
| 页码规格非法 | 见下表 | pdf/pdf.go:208 |
| pdfcpu 抽页失败 | `提取失败: <pdfcpu 错误原文>` | pdf/pdf.go:106 |

**页码规格的报错**（extract / resize / rotate 共用）：

| 情况 | `error_info` |
|---|---|
| `--pages` 只给了空白 | `--pages 缺少值（如 1-3,5,8-）` |
| 区间的起始页非法（不是整数、小于 1、超出总页数） | `起始页非法: <段>（共 <总页数> 页）` |
| 区间的末端不是整数、或比起始页小 | `页码区间非法: <段>` |
| 开尾写法（`8-`）的起始页不存在 | `页码非法: <段>（共 <总页数> 页）——起始页不存在` |
| 单页不存在 | `页码非法: <段>（共 <总页数> 页）` |
| 前开写法（`-5`）的末端不是整数或小于 1 | `页码区间非法: <段>` |
| 其它（孤立的 `-`、空段） | `页码规格非法: <段>` |

#### resize

| 场景 | `error_info` | 出处 |
|---|---|---|
| 位置参数不是恰好 1 个，或没给 `--size` | 四行用法（JSON 里是转义成 `\n` 的一行）：`用法: pdf resize <输入.pdf> --size a4\|a4l\|letter\|210x297mm\|595x842 [--pages 1-3] [--force] [--output 输出.pdf]` + 三行说明 | cli/pdf/pdf.go:174 |
| 输入不存在 | `路径不存在：<绝对路径>` | :178 |
| `--size` 后面没有值 | `--size 缺少值（纸型 a4/letter 或 210x297mm / 595x842）` | :152 |
| `--pages` / `--output` 后面没有值 | `--pages 缺少值（如 1-3——缺省全部页）` / `--output 缺少值（输出 PDF 路径）` | :158 / :164 |
| `--size` 只给了空白 | `--size 缺少值（纸型如 a4/letter，或 210x297mm / 595x842）` | pdf/resize_page.go:39 |
| 纸型查不到，或不是 `宽x高` 的形式 | `--size 需纸型（a4/letter）或 WxH 格式（210x297mm / 595x842），收到: <原样>` | :71 |
| 宽高不是正数 | `--size 宽高需正数（210x297mm / 595x842），收到: <原样>` | :76 |
| 输入读不了 | `无法读取 PDF: <pdfcpu 错误原文>` | :112 |
| 页码规格非法 | 同 extract | :124 |
| 选中页里有文字页、且没带 `--force` | `选中页中第 <页码列表> 页含文字（共 <选中页里的文字页数> 页文档页）——resize 会几何缩放文字（字号缩放非 Word 式重排）；缩小 --pages 范围避开文字页（pdf info --all-pages 查逐页标注）或 --force 明示放行` | :151 |
| pdfcpu 缩放失败 | `尺寸统一失败: <pdfcpu 错误原文>` | :157 |

拦截文案里的「共 N 页文档页」，N 是**选中页里**的文字页数，不是整份文档的页数。页码列表用「、」连接；超过 10 个时只列前 10 个，后面接「…等 N 页」（`FormatPageList`，pdf/resize_page.go:171）。

#### rotate

| 场景 | `error_info` | 出处 |
|---|---|---|
| 位置参数不是恰好 1 个 | 三行用法：`用法: pdf <方向> <输入.pdf> [--pages 1-3] [--output 输出.pdf]` + 两行说明 | cli/pdf/pdf.go:221 |
| 输入不存在 | `路径不存在：<绝对路径>` | :225 |
| `--pages` / `--output` 后面没有值 | `--pages 缺少值（如 1-3,5——缺省全部页）` / `--output 缺少值（输出 PDF 路径）` | :203 / :209 |
| 输入读不了 | `无法读取 PDF: <pdfcpu 错误原文>` | pdf/rotate.go:61 |
| 页码规格非法 | 同 extract | :72 |
| pdfcpu 旋转失败 | `旋转失败: <pdfcpu 错误原文>` | :85 |

方向不合法（`rotate-sideways` 之类）走不到引擎：子命令分派那一步就按「未知子命令」报顶层用法（cli/pdf/pdf.go:49）。

#### info

| 场景 | `error_info` | 出处 |
|---|---|---|
| 位置参数不是恰好 1 个 | `用法: pdf info <输入.pdf>` | cli/pdf/pdf.go:255 |
| 输入不存在 | `路径不存在：<绝对路径>` | :259 |
| 读不了（不是 PDF、损坏、加密、是目录） | `无法读取 PDF: <pdfcpu 错误原文>` | pdf/pdf.go:166 |
| 读不出页面尺寸 | `读取页面尺寸失败: <pdfcpu 错误原文>` | :170 |
| `--all-pages` 读全页明细失败 | `无法读取 PDF` / `读取页面尺寸失败` + pdfcpu 错误原文 | pdf/pdf.go:135 / :139 |

#### 子命令本身不对

没给子命令、或不是这七个之一：四行顶层用法，exit 1（cli/pdf/pdf.go:49）。这四行里**没有列 `resize`**（文案漏了，实际支持）。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 功能与输出字段 | 相同 | 相同 | 相同 |
| JSON 里的路径 | 反斜杠绝对路径（JSON 里转义成 `\\`） | 正斜杠 | 正斜杠 |
| 输出文件正被别的程序打开 | pdfcpu 替换文件会失败，报「…失败」+ 系统错误 | 一般能替换 | 一般能替换 |
| 临时文件 | 输出路径已有文件时，pdfcpu 在同目录建 `.<名字>.tmp-*`，需要该目录的写权限 | 同 | 同 |

- 代码里没有构建标签，三个平台走同一条路径。
- **纯 Go 构建与完整版完全相同**：PDF 是纯 Go 功能，没有「需完整版」的分流。开发时用纯 Go 循环迭代即可（AGENTS.md:193）。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 引擎与落位

- 引擎是 pdfcpu v0.16.4（go.mod:17，Apache-2.0）的薄封装，纯 Go、零 cgo（pdf/pdf.go:1）。zpack 自己的代码只有四件事：参数解析与校验、页码规格、尺寸规格、文字页检测；真正的读写都交给 pdfcpu 的几个文件级 API（`MergeCreateFile` / `TrimFile` / `ResizeFile` / `RotateFile` / `PageCountFile` / `PageDimsFile` / `ReadContextFile`）。
- 抽页用的是 pdfcpu 的 **Trim**（保留选中页）。pdfcpu 自己另有一个叫 extract 的命令，那是导出图片、内容流之类用的，不是这里的功能。
- 落位沿用新功能家族的惯例（AGENTS.md:200）：顶层包 `pdf/` 放引擎，只返回 `error`、不碰 stdout、不 `os.Exit`，过程日志经 `logf` 回调注入；命令面在 `cli/pdf/`；门禁进 `verify/verify.sh`，纯构建就能跑。AGENTS.md:200 里写的命令面路径还是拆包前的 `cli/pdf.go`，现在是 `cli/pdf/pdf.go`（提交 6d31586 拆的）。

### 6.2 页码规格：宽尾宽容、起始严格

- 设计（pdf/pdf.go:205-207）：区间**末端**超出总页数是常见的笔误形态（记得「十几页」、写成 `4-99`），所以截至末页并提示；**起始页**超出或单页不存在，说明要的页根本没有，直接报错。注释里写明这是「同视频剪辑宽尾语义」。
- 页码区间是**两端都闭**的（`1-3` 是三页），和 `media clip` 的时间 / 帧区间（左闭右开 `[from, to)`）不一样，两边直觉不能混用。
- pdfcpu 自己有一套页选择语法（`even`、`odd`、`l`、`1-3` 等）。zpack 不把它暴露给用户：引擎先自己解析、钳制，再把**一个个显式页码**交给 pdfcpu（pdf/pdf.go:100 的注释：「原始 token 可能越界」）。要加 pdfcpu 的其它选择器，得改 `ParsePagesSpec`，而不是把原始写法透传。
- 结果恒为升序：`--pages 5,1` 抽出来也是第 1 页在前。
- 这段解析有表驱动单测守着（pdf/pdf_test.go：18 个用例 + 宽尾提示），改规则先改那里。

### 6.3 `--quiet`：2026-11-24 才真正生效

- 此前 pdf 的 `--quiet` **只被解析、没有任何地方读取**：解析循环里有一个 `case arg == "--quiet"` 分支，但里面什么也没做，日志去向也没有参考它。结果是 `zpack pdf extract a.pdf --pages 4-99 --quiet` 的「已截至末页」提示照样打到 stderr。
- 提交 6d31586 起修好：引擎的日志回调用 `cliutil.EngineLogf(args)` 构造——本次调用的参数里带 `--quiet` 就返回 `nil`（引擎侧所有提示静默），否则接到 stderr（internal/cliutil/logf.go:21-26）。门禁新增「pdf --quiet 静默过程日志」一例（verify/verify.sh:945-947），断言 stderr 里除了「开始执行」没有别的行。
- 为什么不做成包级变量（internal/cliutil/logf.go:19-20 的注释）：日志去向必须按「本次调用的参数」决定。命令面按功能拆包以后各命令各管各的，而且一个进程只执行一条命令，全局状态本来就是多余的耦合。
- **新增过程日志时必须经 `logf` 回调发出**，不能在命令面直接调 `stdout.LoggerInfo`——那样就绕过了 `--quiet`。引擎函数的 `logf` 参数都要容忍 `nil`（每个函数开头都有 `if logf == nil { logf = 空函数 }`）。
- 残留：`pdf info` 的那一行摘要就是直接调 `stdout.LoggerInfo` 的（cli/pdf/pdf.go:265），所以 `pdf info --quiet` 照样输出它。
- resize 的解析循环里没有 `--quiet` 分支（cli/pdf/pdf.go:145-172），它落到 `default` 被忽略。效果一样——静默判定不在循环里。merge / extract / rotate 的循环里显式写了占位分支，那两行只是给读代码的人看的，静默效果同样不靠它们。

### 6.4 文档型拦截：只看选中页（2026-11-08 用户定案修订）

- 为什么要拦：resize 对文字页做的是**几何缩放**——字号等比缩小，不会像 Word 那样重新排版。用户要的是「统一页面尺寸」，对一份文字型 PDF 得到的却是一份字变小的文档，通常不是想要的结果，所以默认拒绝（pdf/textdetect.go:1-2、pdf/resize_page.go:97-98）。
- AGENTS.md 把这次改动记为「修订」，并强调「不连坐」：现在的判定**只看选中的页**——用 `--pages` 圈住图片页就能避开，不会因为没选中的页里有文字而整份拒绝（pdf/resize_page.go:135）。修订前的具体行为代码里已经看不到了。
- 报错要做三件事：点名是哪几页、指路 `pdf info --all-pages` 看逐页标注、指路 `--force`（pdf/resize_page.go:151）。带 `--force` 时放行，并在 stderr 发一条同样点名页码的警告（:154）。
- 判定依据是「这一页的资源里声明了字体」，含从页面树继承来的资源（pdf/textdetect.go:36）。这是启发式的，两个后果：声明了字体但没画字的页也会被拦（可以用 `--force`）；不声明字体的页（纯图片、用路径画出来的字）不会被拦。
- 体检闭环：`pdf info` 的 `text_based` 和 `--all-pages` 的逐页 `text` 用的是同一个判定，所以「先 info、再 resize」不会出现 info 说不含文字、resize 却拒绝的情况。
- GUI 的统一尺寸页有一个「文字页：含文字页仍缩放」开关，就是给 `--force` 的出口——2026-11-23 审计时发现此前 GUI 没有这个出口，文字型 PDF 在 GUI 里走不通（ui/gui/main.go:3773-3778 的注释）。

### 6.5 横页保持横（best-fit）

- pdfcpu 的 resize 有一个「强制方向」的开关：不设它，源页面和目标纸型横竖不一致时，pdfcpu 会把目标的宽高对调；设了它，就按目标方向来。zpack 把这个开关映射成纸型名的 `L` / `P` 尾缀（pdf/resize_page.go:50-56 的注释：「pdfcpu 无此位则做 best-fit——横页保横」）。
- 带尾缀时如果纸型本身的方向和尾缀相反，zpack 先把纸型的宽高对调好再交给 pdfcpu，所以结果 JSON 里的 `width_pt` / `height_pt` 是对调之后的。
- 尾缀还有一个 pdfcpu 层面的副作用：强制方向、而源页方向相反时，**内容会被旋转 90°** 塞进目标方向（所以 `a4p` 处理横页，出来是竖页、内容躺着变成了立着）。

### 6.6 纸型名查表的两个坑

- **坑一（现网缺陷）**：代码先 `strings.ToUpper`，再拿结果去 pdfcpu 的 `types.PaperSize` 里查，而这个表的键是区分大小写的（pdf/resize_page.go:42-49）。pdfcpu 自己的 CLI 是让用户原样输入 `Letter`、`A4`。于是只有键本身全大写的纸型能命中；`Letter`、`Legal`、`Ledger`、`Tabloid`、`Executive`、`Arch1`–`Arch6`、各英制开本这 53 个全部查不到，落进自定义尺寸的分支报「需纸型」。实测 `--size letter`、`legal`、`ledger`、`tabloid` 都是这个结果，纯 Go 开发构建和完整版 `bin/zpack.exe` 上都复现了。帮助文案、AGENTS.md、skill.md、GUI 下拉里都还写着 `letter`。
- **坑二（修坑一时要一起考虑）**：尾缀是「剥掉最后一个字符再查表」。表里有 7 个键以 `l` 结尾（`Legal`、`GovLegal`、`JuniorLegal`、`Royal`、`SuperRoyal`、`Foolscap`、`DoubleFoolscap`），如果只把查表改成不分大小写，`legal` 又会被剥成 `lega` 加「强制横向」而查不到。能用的 97 个键里没有以 `L` / `P` 结尾的，所以这个歧义现在没有表现出来。

### 6.7 旋转：只动元数据

- 旋转改的是页面的 `/Rotate` 属性，内容字节一个都不动，渲染器按属性转——零损，和视频那边写 display matrix 是同一种思路（pdf/rotate.go:1-3）。90° 的倍数是 PDF 的原生能力。
- **mirror 不支持**，而且是有意不支持：PDF 没有镜像属性，要做就得改写内容流，文字会镜像成不可读。用法文案里把这句话写给了用户（cli/pdf/pdf.go:221）。
- **增量叠加**：pdfcpu 是「当前角度 + 本次增量，对 360 取模」。所以对同一页转四次 `rotate-right` 会回到 0。
- `pdf info` 里 `rotation` 和尺寸的来源不同：`rotation` 读的是页面字典**自己**的 `/Rotate`（pdf/pdf.go:189-203，不沿页面树继承）；尺寸用的是 pdfcpu 的 `PageDims`，按**有效**旋转（含继承）换算。如果一份文档把 `/Rotate` 写在页面树的上级节点上，尺寸会对调而 `rotation` 不出现【待确认：没有这样的夹具，未实测】。

### 6.8 失败时删文件：一句多余且危险的 `Remove`

- 四个子命令在引擎返回错误时都会 `os.Remove(<输出路径>)`（cli/pdf/pdf.go:82、:132、:187、:236），本意是项目惯例「中途失败删半成品」。
- 但 pdfcpu v0.16.4 自己就是分阶段写文件的：输出不存在就直接写、失败即删；输出已存在就在同目录写临时文件、成功才替换。失败时它从不碰目标文件。所以 zpack 这句 `Remove` 对 pdfcpu 的失败是多余的，而在「引擎还没调用 pdfcpu 就拒绝了、输出路径上已经有文件」时会删掉那个文件。最坏的情况是 `--output` 指向输入自己（原地处理）时遇到「纸型不认识 / 页码非法 / 文字页拦截」——输入被删。
- 这一条是读代码得出的，没有实跑。改的方向（未做）：引擎报错时不要删，或者至少输出路径与输入相同时不删。

### 6.9 `pdf info --all-pages` 的载荷是现场拼的 map

- 这个形态的 JSON 不是结构体，是 `map[string]any`（cli/pdf/pdf.go:272），违反了「data 载荷一律用结构体」的约定（conventions.md 3.1）。现在没有键序随机，靠的是输出层 `Deterministic(true)` 的兜底（internal/stdout/log.go:39-40）。
- 副作用有两个：键序变成了字典序，和不带 `--all-pages` 时不一样；`text_based` 字段漏了。
- 动机是注释里那句「明细并入 data（信封唯一 stdout 纪律）」——stdout 只能有一条 JSON，所以把 `pages` 并进同一个载荷。要修的话，给 `InfoResult` 加一个 `Pages []PageItem` 字段就行。

### 6.10 GUI 耦合

- GUI 的四个页面——合并（ui/gui/main.go:2860）、提取页（:2901）、旋转（:3703）、统一尺寸（:3792）——都经 `Service.StartConvert(标题, 参数, 目录, 产物)` 起子进程驱动。GUI 读的是 `data.output`、`data.size_bytes`，失败时读 `error_info`（ui/gui/service.go:677-716）。改这些字段名是 GUI 的破坏性变更。
- GUI **总是**传 `--output`，值是 `uniquePath(目录/<名>-xxx.pdf)` 保证不存在的路径，从不原地处理，所以 6.8 的删文件问题 GUI 碰不到。
- GUI 纸型下拉固定四项：`a4`、`a4l`、`letter`、`210x297mm`（ui/gui/main.go:3748）。`letter` 那一项会撞上 6.6 的缺陷。
- GUI 的前端校验 `validatePagesSpec`（ui/gui/validate.go:60）照着引擎的页码语法写，但它不知道总页数，越界只能交给引擎报。改 `ParsePagesSpec` 的规则时要同步改它。

### 6.11 产物不是逐字节确定的

同一组输入，`pdf merge` 产物的字节数会在 1535–1538 之间浮动（实测），原因没有查明。所以验收只比对页数和输入个数，不比对 `size_bytes`。

### 6.12 验收夹具靠 python3 现造

pdf 段的夹具是三个 python 脚本手写的最小合法 PDF（`make_pdf` / `make_pdf_mixed` / `make_pdf_textdoc`，verify/verify.sh:882-906、:949-970、:998-1019）。没有 python3 的机器上，pdf 段整个跑不起来。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面 | `cli/pdf/pdf.go` | 子命令分派；merge / extract / resize / rotate / info 的参数解析、缺省输出名、结果输出 |
| | `cli/pdf/doc.go` | 包文档 |
| 引擎 | `pdf/pdf.go` | `Merge` / `ExtractPages` / `Info` / `InfoAllPages`、`ParsePagesSpec`、各结果结构体 |
| | `pdf/resize_page.go` | `ParsePageSizeSpec` / `ResizePages`、文字页拦截、`FormatPageList` |
| | `pdf/rotate.go` | `Rotate`、方向到角度的换算 |
| | `pdf/textdetect.go` | `DetectTextPages` / `pageHasFont`（文字页判定） |
| 测试 | `pdf/pdf_test.go` | 页码规格的表驱动单测（18 例）+ 宽尾提示；`cli/pdf` 下没有测试 |
| 第三方 | `github.com/pdfcpu/pdfcpu` v0.16.4（go.mod:17） | PDF 读写引擎；纸型表在 `pkg/pdfcpu/types/paperSize.go` |
| 共用小件 | `internal/cliutil/logf.go` | `EngineLogf`（`--quiet` → `nil`）、`StderrLogf` |
| | `internal/cliutil/cliutil.go`、`internal/cliutil/help.go` | `RestAfterPositional`、`HelpAwareRun` |
| 装配 | `cli/root.go` | 挂到根命令（:72）；统一开始日志 |
| GUI 耦合 | `ui/gui/main.go`（:2860、:2901、:3703、:3792）、`ui/gui/service.go`（:677）、`ui/gui/validate.go`（:60） | 四个 PDF 页面经 CLI 驱动 |
| 验收脚本 | `verify/verify.sh` | :878-1078（四个小节，见 8） |

## 8. 验收用例

都在 `verify/verify.sh` 里，纯 Go 构建即可跑。夹具由 python3 现造：`pdf-a.pdf`（2 页）、`pdf-b.pdf`（3 页）、`pdf-c.pdf`（5 页）都是 300×400 pt 的竖页；`pdf-mixed.pdf` 三页尺寸各不相同；`pdf-text.pdf` 两页，第 2 页声明了 Helvetica 字体。

### pdf 命令族（:878-947）

| 用例名 | 断言 |
|---|---|
| `pdf info exit` / `pdf info 页数` / `pdf info mm 换算` | 退出码 0；`"page_count":5`；`"width_mm":105.83` |
| `pdf merge exit` / `pdf merge 页数（2+3）` / `pdf merge 输入计数` | 2 页 + 3 页 → `"page_count":5`、`"input_count":2` |
| `pdf merge 产物可读` | 对合并产物再跑 `pdf info`，退出码 0 |
| `pdf extract 区间 [2,3]` / `pdf extract 产物实际页数（区间2页）` | `--pages 2-3` → `"pages":[2,3]`；产物再 info 页数为 2 |
| `pdf extract 开尾 [4,5]` | `--pages 4-` → `"pages":[4,5]` |
| `pdf extract 前开 [1,2]` | `--pages -2` → `"pages":[1,2]` |
| `pdf extract 散列 [1,3,5]` / `pdf extract 产物实际页数（散列3页）` | `--pages 1,3,5` → `"pages":[1,3,5]`；产物页数 3 |
| `pdf extract 缺省输出名` | 不给 `--output` 时产出 `pdf-c-extract.pdf` |
| `pdf 宽尾提取 exit` / `pdf 宽尾钳制 [4,5]` / `pdf 宽尾提示` | `--pages 4-10` → 退出码 0、`"pages":[4,5]`、stderr 含「已截至第 5 页」 |
| `pdf --quiet 静默过程日志` | 同上再加 `--quiet`：stderr 里除「开始执行」外没有别的行 |

### pdf resize 统一页面尺寸（:948-996）

| 用例名 | 断言 |
|---|---|
| `混合尺寸体检` / `--all-pages pages 数组` | `pdf info pdf-mixed.pdf --all-pages` 能看到 `"width_pt":500` 的页和 `"pages":[` |
| `a4 统一竖页数` / `a4 统一横页数（best-fit）` | `--size a4` 后：宽 595 的页 2 个、宽 842 的页 1 个 |
| `a4p 强制全竖` | `--size a4p` 后：宽 595 的页 3 个 |
| `自定义 mm 尺寸` | `--size 210x297mm` 后首页 `"width_pt":595.27` |
| `resize 未知纸型 exit` / `resize 缺 --size exit` / `resize 零尺寸 exit` | `a99`、不给 `--size`、`0x0` → 退出码 1 |

### pdf resize 文档型拦截（:997-1041）

| 用例名 | 断言 |
|---|---|
| `文档型体检字段` | `pdf info pdf-text.pdf` 含 `"text_based":true` |
| `逐页 text 标注` | `--all-pages` 含 `"page":2,"width_pt":300,"height_pt":400,"text":true`（逐字节） |
| `文档型 resize 拦截 exit` / `拦截文案点名页码` | 不带 `--force` → 退出码 1，`error_info` 含「第 2 页含文字」 |
| `混合型圈图片页放行 exit` / `圈图片页产物` | `--pages 1`（只选没有文字的第 1 页）→ 退出码 0，产物首页宽 595 |
| `圈中文字页拦截 exit` | `--pages 2` → 退出码 1 |
| `--force 放行 exit` / `force 产物尺寸` | 带 `--force` → 退出码 0，产物首页宽 595 |
| `图片型不受拦截 exit` | `pdf-mixed.pdf`（没有字体）→ 退出码 0 |

### pdf 旋转（:1042-1078）

| 用例名 | 断言 |
|---|---|
| `pdf rotate-right =90°(顺时针)` | 产物 `pdf info` 的 `"rotation":90` |
| `pdf rotate-left =270°(逆时针)` | `"rotation":270` |
| `pdf rotate-180 =180°` | `"rotation":180` |
| `pdf 旋转后观看态宽` | 转后的 `"width_pt":400`（原 300×400） |
| `pdf 旋转叠加（right×2=180）` | 对已转 90° 的产物再转一次 → `"rotation":180` |
| `pdf 选页旋转 [2]` | `--pages 2` → `"pages":[2]` |
| `pdf 选页不染首页` | 产物的 `pdf info` 里没有 `rotation` 字段 |
| `pdf rotate 缺省输出名` | 产出 `pdf-c-rotated.pdf` |
| `pdf rotate 页码越界 exit` | `--pages 9` → 1 |
| `pdf 非法方向子命令 exit` | `pdf rotate-sideways` → 1 |

同一段里的退化用例：

| 用例名 | 断言 |
|---|---|
| `pdf extract 越界 exit` / `pdf extract 起始越界 exit` / `pdf extract 非法页码 exit` | `--pages 9`、`--pages 6-9`、`--pages abc` → 1 |
| `pdf extract 缺 --pages exit` | 1 |
| `pdf merge 输入缺失 exit` / `pdf merge 单输入 exit` | 1 |
| `pdf info 不存在 exit` / `pdf --help exit` | 1 / 0 |

### 没有被验收覆盖的

- 纸型 `letter` / `legal` / `ledger`（现在都报错，正是因为没测才没发现）、`L` 尾缀（只测了 `a4p`）、`cm` / `in` / pt 三种自定义单位。
- `resize` 带多页的 `--pages`、`--force` 的 stderr 警告文案。
- `--all-pages` 形态下 JSON 的键序与字段完整性；首页以外各页的 `rotation`。
- 所有 `error_info` 的文案——退化用例只比对退出码。
- 加密 PDF、`--output` 指向输入自己、失败时输出路径上的文件被删。

### 手动验证

```bash
zpack pdf info 样张.pdf --all-pages 2>/dev/null | zpack json get data.pages        # 逐页尺寸与文字页标注
zpack pdf resize 样张.pdf --size a4 --pages 2-4 --output 统一.pdf                  # 只统一第 2–4 页
zpack pdf extract 样张.pdf --pages 4-99 --output 尾页.pdf                         # 看 stderr 的「已截至末页」
zpack pdf extract 样张.pdf --pages 4-99 --output 尾页.pdf --quiet 2>&1 | grep -v 开始执行   # 应无输出
zpack pdf resize 样张.pdf --size letter --output 新建目录/x.pdf; echo $?     # 现状：报「需纸型」。输出路径要给一个不存在的文件
# 不要拿唯一的原件试 --output 指向自己的写法：引擎拒绝时输出路径上的文件会被删（见 4.3）
```

## 9. 未结项与已知限制

**已知不对、暂时没改的：**

- **`--size letter`（以及 `legal` / `ledger` / `tabloid` 等 53 个纸型）用不了**（pdf/resize_page.go:42-49，见 6.6）。实测报 `--size 需纸型（a4/letter）或 WxH 格式（210x297mm / 595x842），收到: letter`。帮助文案（cli/pdf/pdf.go:152、:174）、AGENTS.md:74、cli/tools/skill.md:161、GUI 的下拉（ui/gui/main.go:3748）都还把它当可用的值。GUI 里选 `letter` 的结果就是任务失败。 **2026-11-24 已修复（提交 118dde3）**
- **引擎拒绝时会删掉输出路径上已有的文件**，输出路径就是输入时删的是输入（cli/pdf/pdf.go:82、:132、:187、:236，见 6.8）。读代码得出，未实跑。
- **`pdf info --quiet` 不静默**：那一行摘要绕过了 `--quiet`（cli/pdf/pdf.go:265）。
- **`pdf info --all-pages` 的 JSON 是 map**：键序与不带该旗标时不同，且缺 `text_based`（cli/pdf/pdf.go:272，见 6.9）。
- **顶层用法文案漏了 `resize`**：`zpack pdf` 不带参数时报的四行用法只列了 merge / extract / rotate / info（cli/pdf/pdf.go:49）。
- **`merge` 没有过程日志**，也没有接 `logf`（pdf/pdf.go:55 的签名里没有日志参数）。
- **`merge` 的产物字节数不固定**：同一组输入在 1535–1538 之间浮动（实测），原因未查明。
- **加密 PDF 完全不支持**：没有密码旗标，读取阶段就报错。
- **拦截文案里的「共 N 页文档页」** 容易被读成整份文档的页数，实际是选中页里的文字页数（pdf/resize_page.go:151）。
- **`rotation` 不沿页面树继承**：`/Rotate` 写在上级节点时报不出来（pdf/pdf.go:189-203，见 6.7）。

**代码与文档不一致的地方：**

| 文档 | 文档怎么说 | 代码实际 |
|---|---|---|
| AGENTS.md:74、cli/pdf/pdf.go:152 / :174、cli/tools/skill.md:161、ui/gui/main.go:411 | `--size` 可用 `letter`（skill.md 还列了 `legal`、`ledger`） | 全部报「需纸型」 |
| AGENTS.md:75 | `pdf info --all-pages` = 「同 info + `pages`」 | 这个形态没有 `text_based`，键序也不同 |
| AGENTS.md:74 | resize 的输出字段没列 `input`、`output`、`pages_spec` | 都有（`pages_spec` 仅在给了 `--pages` 时出现） |
| AGENTS.md:76 | rotate 的输出字段没列 `pages_spec` | 给了 `--pages` 时出现 |
| AGENTS.md:200 | 命令面在 `cli/pdf.go` | 拆包后是 `cli/pdf/pdf.go` |
| AGENTS.md:74 命令列 | `pdf resize <输入> --size … [--pages] [--output]` | 还有 `--force`（同行的说明里提到了，命令列漏了）；`--pages` / `--output` 另有短别名 `-p` / `-o` |
| cli/pdf/pdf.go:49 | 顶层用法四行 | 实际有七个子命令，漏了 `resize` |
| conventions.md 第 9 节 | 「`pdf` 的 `--quiet` 可能不生效」 | 提交 6d31586 起 extract / resize / rotate 已生效，只有 `pdf info` 的摘要行仍不受控 |

**没做的：**

- 加密 PDF 的读取（`--password`）。
- 按页拆分成多个文件、页面重排 / 删除页（extract 只能保留所选页、产出一个文件）。
- 水印、页码、表单、书签等 pdfcpu 已有但未接的能力。
- PDF 与图片 / Office 的互转、OCR。

**【待确认】：**

- 页面树上级带 `/Rotate` 的文档，`pdf info` 是否如代码所示「尺寸对调、`rotation` 不出现」——没有这种夹具，未实测。
- 「引擎拒绝时删除输出路径上已有的文件」是读代码得出的（6.8），没有实跑——实跑要牺牲一个文件。逻辑上没有疑问，但建议用一份废弃的 PDF 复现一次再动手修。
- `merge` 产物字节数浮动（1535–1538）的原因没有查明。

**修 `--size letter` 时要一起改的地方**：pdf/resize_page.go:42-49 的查表（区分大小写 → 归一化），以及 `Legal` 等以 `L` 结尾的纸型名与尾缀 `L` 的歧义（6.6）；再给 verify.sh 的 resize 段补 `letter` / `legal` / `ledger` / `a4l` 四个用例。修好之前，第 2 节末尾的对照表是现成的绕法（`ansia`、`8.5x14in` 等），GUI 的下拉把 `letter` 换成 `ansia` 或 `8.5x11in` 就能立刻恢复可用。

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 修复：`resize --size letter` / `legal` 等 53 个纸型报「需纸型」（纸型表是混合大小写的键；尾缀 L/P 规则误伤 legal）；命令失败后不再删除输出路径上原有的文件 | 118dde3 |
| 2026-11-24 | 修复：`--quiet` 此前只被解析、没有地方读取，现在静默引擎过程日志 | 6d31586 |
