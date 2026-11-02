---
name: zpack
description: 单二进制文档媒体工具箱（stdout 恒一条 JSON、exit 0/1）。压缩/解压（zip/tar 系写，26 种读格式含 7z/rar/分卷/加密）；格式转换；hash 校验（md5/sha256）；视频 convert/compress/clip/merge/rotate/screenshot/cover；音频互转压缩；图片 convert/resize/旋转/长图拼接/HEIC/LivePhoto；PDF merge/extract/rotate/页面统一；doc/wps/xls/ppt → docx/xlsx/pptx（含本机 Office/WPS/LibreOffice 后台转换）；内置 ffmpeg CLI 全量透传。
---

# zpack 参考手册

## 1. 输出协议

| 项 | 规则 |
|---|---|
| stdout | 恒一条 JSON：成功 `{"is_success":true,"data":{...}}`；失败 `{"is_success":false,"error_info":"人类可读原因"}`。例外（纯文本）：`base64 encode/decode`（无 --output）、`crypto encrypt/decrypt`（无 --output）、`skill`、`ffmpeg`（原版透传） |
| 退出码 | 成功 0 / 失败 1。判定类（`test`、`hash --expect`、`hash --check`）结论为否时 exit 1 但 JSON 仍带完整 data 字段 |
| stderr | 过程日志（进度条/警告/钳制提示），可整体忽略（`2>/dev/null`）不影响结果判断 |
| 确定性 | 同输入产出字节确定；数值旗标非法/越界即报错退出；未知旗标静默忽略 |
| 通用旗标 | `--quiet`（抑制过程日志）；全部命令 `--help`/`-h` 恒可用 |
| Ctrl+C | 一次=清理半成品后退出；两次=立即退出 |

## 2. 通用值格式

- **页码规格**（PDF 族统一）：`1-3,5,8-`——1 起**双端闭区间**（`1-3` = 第 1、2、3 页，含两端）；`8-`=到末页；`-5`=首页起第 5 页；升序去重；末端超出总页数自动截至末页 + stderr 提示；起始页/单页不存在报错
- **时间/帧区间**（media clip 专用）：**左闭右开 `[from, to)`**——`--seconds-from 2.5 --seconds-to 7.8` = 从 2.5s 起（含）到 7.8s 前（不含）的帧，恰在 7.8s 及之后的帧**不取**；帧号形态同语义（`--frame-from 75 --frame-to 234` = 第 75…233 帧）。设计目的：相邻区间可无缝拼接——`[0,5)` + `[5,10)` = 完整覆盖，边界帧零重复零丢失（Python 切片惯例）。**注意与页码规格不同：页码双闭、时间/帧左开右闭不可混用直觉**
- **体积**：`65536`（字节）| `64KB` | `100MB` | `2GB`
- **尺寸**（视频/图片）：`1920x1080`

## 3. 命令

### 3.1 compress——打包压缩

用途：把目录或文件打包成归档用于分发、备份、体积缩减；支持加密与分卷（按邮件/介质容量切分）。

```
zpack compress <目录|文件...> --output <路径> [旗标]
```

写格式由 --output 扩展名决定：`.zip`（缺省）/`.tar`/`.tar.gz`/`.tgz`/`.tar.xz`/`.tar.zst`。

旗标：`--password <值>`（zip AES-256）| `--password-table <文件>`（逐文件密码，行格式 `匹配模式=密码`）| `--volume-size <体积|模板>`（分卷 `.001/.002...`；整档 ≤ 单卷不分卷；模板 `gmail`25MB `cd`=`vcd`700MB `cd650`650MB `fat32` `dvd`=`dvd5`4.7GB `dvd9`8.5GB `bd`=`bd25` `bd50`；下限 64KB）| `--level <0-9>`（缺省 6，0=仅存储）| `--jobs <1-64>`（缺省 min(核数,8)）| `--exclude <glob>`（可多次）| `--comment <文本|@文件>` | `--temp-dir <目录>` | `--quiet`

行为：目录以目录名为包内根；多文件平铺包根；已存在覆盖；空目录不保留；`.zpackignore`（gitignore 语义）自动生效；缺省排除 `.DS_Store`/`__MACOSX`/`Thumbs.db`/`desktop.ini`/`._*`。

输出：`{"archive":"<绝对路径>","file_count":N,"size_bytes":N}`

### 3.2 extract——解压

用途：展开任意来源的压缩包（用户发来的 rar/7z/分卷/加密包都能开）；智能落盘不污染当前目录。

```
zpack extract <归档> [--output <目录>] [--password <值>] [--password-table <文件>] [--force] [--jobs <N>]
```

读格式（魔数识别，不看扩展名）：zip（含 zip64/SFX/加密/GBK 名修复）、tar 系（gz/xz/bz2/zst）、7z、rar（含多卷）、wim、cab、iso、ar、asar、gzip/bzip2/zstd/xz/lz4/lzip 单流、brotli（.br 门控）、.Z、uuencode/xxencode、`.001` 分卷（内层任意）。

行为：缺省智能解压（单根直落 CWD、散包包一层以归档基名命名的目录）；缺省**跳过已存在**（保护已改动文件）；路径穿越/符号链接逃逸/重复条目名拒绝；校验自动。

输出：`{"output":"<绝对路径>","file_count":N,"size_bytes":N,"skipped":N}`

### 3.3 convert——归档格式互换

用途：接收方打不开某格式时转换（如 rar→zip、7z→tar.gz）。

```
zpack convert <源归档> [输出路径] [--password <值>] [--temp-dir <目录>] [--level <0-9>] [--quiet]
```

输出缺省 `<源名>.zip`。输出：`{"source":"...","archive":"...","format":"tar.gz","source_files":N,"file_count":N,"size_bytes":N}`

### 3.4 list / test / info——归检修视

用途：不落盘查看归档内容、验证完整性（下载/传输后）、读统计。

```
zpack list  <归档>   → {"format":"zip","entry_count":N,"file_count":N,"dir_count":N,"entries":[{"name":"...","size":N,...}]}
zpack test  <归档>   → {"format":"zip","tested_entries":N,"all_ok":true}；损坏=判定类失败 {"is_success":false,"failures":[...]}
zpack info  <归档>   → {"format":"zip","entry_count":N,"file_count":N,"dir_count":N,"total_uncompressed":N,"archive_size":N,"compression_ratio":N,"encrypted":bool}
```

### 3.5 hash——哈希计算与完整性校验

用途：算文件哈希（**md5** / sha1 / sha224 / sha256 / sha384 / sha512 / crc32，缺省 sha256）；下载后对官方哈希；生成校验清单；批量验清单。

```
zpack hash <文件...> [--algorithm <md5|sha1|sha224|sha256|sha384|sha512|crc32>]      # 缺省 sha256
zpack hash <文件> --expect <hex>          # 校验：{"is_success":bool,"is_match":bool,"hash":"<实算>","expected":"..."}；不匹配 exit 1；大小写归一（官方大写 hex 可匹配）
zpack hash <文件...> --output <清单>      # shasum 文本格式（md5sum -c / shasum -c 互操作）→ {"output":"...","algorithm":"...","file_count":N}
zpack hash --check <清单>                 # {"checked":N,"matched":N,"mismatched":[],"missing":[]}；任一不符 exit 1；# 注释行跳过；算法按哈希长度自动判定（32=md5/40=sha1/56=sha224/64=sha256/96=sha384/128=sha512/8=crc32），一份清单可混多算法
```

### 3.6 media——视频处理

用途：视频格式转换/压缩瘦身/剪辑/拼接/旋转/变速/截图/封面。**处理任何视频任务先跑 `media info`**——返回事实（编码/尺寸/码率/旋转）+ `suggestions[]`（每项 `{kind,reason,flag}`，flag 可直接拼给 convert/compress）+ `bakeable[]`（可烤元数据）。

```
zpack media info <输入>
zpack media convert <输入> <输出.mp4|mkv|mov|webm|mp3|m4a|flac|opus|wav> [--screen <2160p|1440p|1080p|720p|480p|360p>] [--codec <h264|hevc|vp9|av1>] [--preset <ID>] [--bake]
zpack media compress <输入> [--target-size <50%|100MB|cd|dvd|...>] [--output <输出>] [--preset <ID>] [--screen ...] [--codec ...]
    # 缺省输出=同目录 <名>.mp4
zpack media clip <输入> (--seconds-from <秒> --seconds-to <秒> | --frame-from <N> --frame-to <N>) [--output <输出>]
    # 区间左闭右开 [from, to)：to 时刻/帧不含（见 §2——与 PDF 页码的双闭区间不同）
    # 缺省输出=同目录 <名>_<起>-<止>s.<原扩展名>（如 video_2.5-7.8s.mp4）
zpack media merge <视频1> <视频2> [...] --output <输出> [--reference <基准路径>]
zpack media rotate-left | rotate-right | rotate-180 | mirror | flip <输入> <输出>
zpack media bake <输入> <输出>
zpack media screenshot <输入> [--time <秒> | --count <N> | --every <间隔秒> | --times <秒,...>] [--output <路径> | --output-dir <目录>] [--format <png|jpg>]
    # 单帧缺省输出=同目录 <名>_shot.<格式>；多帧（--count/--every/--times）用 --output-dir
zpack media speed <输入> --factor <0.1-10> [--pitch-match] --output <输出>
zpack media cover <输入> <图片> [--output <输出>]              # 缺省输出=同目录 <名>_cover.<原扩展名>
zpack media insert-frame <输入> <图片> [--front|--back] [--output <输出>]   # 缺省 --front；缺省输出=同目录 <名>_framed.<原扩展名>
zpack media preset [<ID>]
```

语义：convert 尽量无损三级瀑布（`mode` 明示 `remux`=零损/`audio-only`/`full`）；compress 缺省压 50%、目标体积公式分配码率、--preset 平台档位（youtube/bilibili/discord/gmail…，达标自动直拷贝）；clip 帧精确左闭右开 `[from,to)`；merge 以基准视频参数为纲其余拉伸匹配；rotate 缺省零损写矩阵（mirror/flip 恒烤入）；bake 把显示元数据（旋转/SAR/HDR/VFR）物理化进像素；speed 缺省保音调；cover 写元数据缩略图；insert-frame 在开头插入可见封面帧。

统一结果：`{"mode":...,"video":{"action":"copy|encode|none","codec":"...","bitrate":N},"audio":{...},"size_bytes":N,"original_size_bytes":N,"ratio":N,"target_bytes":N,"preset":"...","already_fit":bool,"baked":[...]}`

### 3.7 audio——音频处理

用途：音频格式互转（flac→mp3 等）、大文件压码率瘦身、读元信息。

```
zpack audio convert <输入> <输出.mp3|m4a|flac|wav|ogg|opus> [--bitrate <Nk>] [--sample-rate <Hz>] [--mono]
zpack audio compress <输入> [--bitrate <Nk>] [--mono] [--output <输出>]
zpack audio info <输入>
```

compress 缺省压到原始码率 50%（下限 32k）；目标 ≥ 原始报错（转格式用 convert）。输出：`{"mode":"audio-only-output","audio":{"action":"encode","codec":"...","bitrate":N},"size_bytes":N,"original_size_bytes":N,"ratio":N,"original_bitrate":N,"bitrate":N,"sample_rate":N,"mono":bool}`

### 3.8 image——图片处理

用途：格式互转（含 HEIC 手机照片、HDR 场景图）、压到目标体积、精确物理尺寸（打印毫米级）、旋转镜像、多图拼长图、提取 iPhone Live Photo 视频。

```
zpack image convert <输入> <输出.jpg|png|webp|bmp|tiff|gif> [--quality <1-100>]
zpack image compress <输入> ([--target-size <体积>] [--priority <size|quality>] | [--quality <1-100>]) [--output <输出>]
zpack image resize <输入> <输出> <缩放方式> [--ppi <N>] [--quality <1-100>]
zpack image rotate-left | rotate-right | rotate-180 | mirror | flip <输入> <输出> [--quality <N>]
zpack image merge <图1> <图2> [...] [输出] [--reference <基准图>] [--direction <vertical|horizontal>] [--quality <N>]
zpack image livephoto <IMG_XXXX.HEIC>
zpack image info <输入>
```

resize 缩放方式（互斥选一）：`--mm-w/--mm-h/--cm-w/--cm-h/--inch-w/--inch-h <N>`（物理尺寸，可组合 `<W>x<H>`=双尺寸强制拉伸）| `--width/--height <px>` | `--percent <N>` | `--max <WxH>`（适配框只降不升）。

compress --target-size（仅 jpg）：`size`（缺省）= 质量钉 90 缩尺寸到刚好装下；`quality` = 保像素优先（降质至 q≥50，不够再缩）；兜底缩至 5%+全量程二分（`quality_degraded:true`）；已达标直拷贝（`already_fit:true`）。

info：`{"format":"jpeg","width":N,"height":N,"size_bytes":N,"color_model":"...","orientation":N,"display_width":N,"display_height":N}`——EXIF orientation≠1 时出现 orientation/display_*；一切处理命令产出恒为观看态（EXIF 自动定向）；HEIC/HDR(EXR)/JXL 自动读取，真 HDR 自动 Reinhard 滚降转 SDR。

### 3.9 pdf——PDF 页面操作

用途：多份 PDF 合一、抽页（按页码区间）、旋转、扫描件页面尺寸统一（A4 化）；动 resize 前先 `info --all-pages` 体检（逐页尺寸 + `text` 文字页标注）。

```
zpack pdf merge <1.pdf> <2.pdf> [...] <输出.pdf>
zpack pdf extract <输入> --pages <页码规格> [--output <输出>]      # 缺省 <名>-extract.pdf
zpack pdf rotate-left | rotate-right | rotate-180 <输入> [--pages <页码规格>] [--output <输出>]
zpack pdf resize <输入> --size <纸型|自定义> [--pages <页码规格>] [--force] [--output <输出>]
zpack pdf info <输入> [--all-pages]
```

resize --size 取值：纸型 `a0`-`a10`/B 系/`letter`/`legal`/`ledger`（尾缀 `L`/`P` 强制横/竖）| `210x297mm` | `8.5x11in` | `595x842`（pt）。语义：内容按页缩放（横页 best-fit 保横）；**文字页默认拒绝并点名页码**——`--pages` 圈住图片页避开或 `--force` 放行。rotate 为 /Rotate 元数据零损、增量叠加。

info：`{"page_count":N,"width_pt":N,"height_pt":N,"width_mm":N,"height_mm":N,"size_bytes":N,"rotation":N,"text_based":bool}`；--all-pages 另加 `"pages":[{"page":N,"width_pt":N,"height_pt":N,"rotation":N,"text":bool}]`。

### 3.10 office——旧格式 Office 文档转现代格式

用途：把别人发来的老文件（.doc/.xls/.ppt）或 WPS 文件（.wps/.et/.dps）转成现代格式（.docx/.xlsx/.pptx）；可选借本机安装的 Office/WPS/LibreOffice 后台转换（保真度更高）。

```
zpack office convert <输入> <输出> [--try-msoffice | --try-wps | --try-libreoffice]
  输入：.doc .wps .xls .et .ppt .dps（WPS 扩展名=MS 格式同构，魔数识别）
  输出：.docx .xlsx .pptx（族匹配强制：doc 族只出 docx，跨族报错）
```

`--try-*`（互斥）：探测本机套件、隐藏后台进程转换、不干扰用户正在使用的程序；探测序=环境变量 `ZPACK_OFFICE_LIBREOFFICE_PATH`/`_WORD_PATH`/`_WPS_PATH` → PATH → 平台动态解析 → 常见路径；未装套件=确定性报错+安装提示。缺省走内置转换引擎。

输出：`{"source_format":"xls(et)","target_format":"xlsx","size_bytes":N}`（--try-* 另有 `"engine":"libreoffice"`）。

### 3.11 ffmpeg——原版 ffmpeg 全量透传

用途：zpack 未封装的长尾需求（特殊滤镜、滤镜链、协议源、设备采集、调试）——透传即得 ffmpeg 全部能力，无需等封装。

```
zpack ffmpeg <原版任意参数...>
```

stdout/stderr/退出码=ffmpeg 原生语义（stdout 可为二进制 `pipe:` 流）；stdin 直通。

### 3.12 编码——base64 与加密

用途：Base64 编解码（内嵌数据、传输安全编码）；RSA 密钥对生成与公钥加解密（小文件/密钥交换）。

```
zpack base64 encode|decode <输入> [--output <路径>]   # 无 --output=stdout 纯文本/原始字节（二进制安全）
zpack crypto genkey [--bits 2048] [--output <前缀>]   # RSA 密钥对（输出 .pub/.pem + b64 形态）
zpack crypto encrypt|decrypt <输入> [--output <路径>] # 公钥加密/私钥解密；无 --output=stdout 纯文本
```

### 3.13 json——消费 JSON 结果（零依赖）

用途：提取 zpack 结果字段、判定成败——替代 python3 -c / jq，单二进制闭环。

```
zpack json get <字段路径>    # stdin 读 JSON，值裸打印（stdout 纯文本）；复合值=紧凑 JSON
zpack json ok                # stdin 读信封 JSON，is_success → exit 0/1
```

字段路径：点分段 + 数字段=数组下标（`data.entries.0.name`）。

### 3.14 杂项

```
zpack bench [目录]                    # 本机压缩性能基准
zpack skill [--install [目录]]        # 本手册输出/安装为技能
```

## 4. 调用范式（零依赖——json 子命令消费，不用 python3/jq）

```bash
# 捕获结果（stderr 进度丢弃）
result=$(zpack compress 目录/ --output out.zip 2>/dev/null)

# 成败判定（is_success → 退出码）
echo "$result" | zpack json ok && echo 成功 || echo 失败

# 字段提取
archive=$(echo "$result" | zpack json get data.archive)
count=$(echo "$result" | zpack json get data.file_count)

# 管道直取（边看进度边拿字段）
zpack extract big.zip 2>&1 | tee /dev/stderr | zpack json get data.output

# CI 只看退出码
zpack compress 目录/ --output out.zip > /dev/null 2>&1 && echo OK || echo FAIL

# 判定类
zpack hash 文件.iso --expect <官方哈希> && echo 完整 || echo 损坏
```
