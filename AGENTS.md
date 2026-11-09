# AGENTS.md — zpack 项目协作规范

任何 Agent 在本仓库工作前先读完本文。**本仓库从 exkit-cli 剥离**（剥离完成 SOP 已执行：exkit 侧 zip 命令/门禁段/依赖已全部移除，exkit 门禁 171/171 不含 zip 段）；契约与工程规范继承 exkit，算法行为对齐业界主流压缩器。

## 项目作用

- **压缩/解压专项 CLI**：单文件二进制（零运行环境依赖），Mac 上一条命令交叉编译出 Windows exe；stdout 唯一 JSON 结果 + stderr 日志的输出契约天然是 Agent/未来 GUI 的协议。
- 已交付：`compress` / `extract` 扁平命令（2026-11-02 定案）——**zip + tar/tar.gz 双格式**。zip 侧并行架构（条目级并行 + >16MB 文件 128KB 分块并行 + 魔数 Store 直通 + GBK 文件名修复 + 智能解压 + zip-slip 防护 + 有界内存耐久设计）；tar 侧 PAX 确定性写入 + 符号链接保留 + 魔数识别读取 + 穿越/链接逃逸/设备条目防护。7z/rar 读取为下一里程碑。
- **北极星（2026-11-02 用户定案）**：单引擎双面孔——同一二进制服务 CLI 与未来 GUI（GUI 是经输出契约调用的薄壳，引擎层零感知）。**读取** zip/rar/7z/tar/tar.gz 百分百（全变体：zip64/分卷/加密带密码/SFX/GBK/符号链接）；**写入** zip/tar.gz（7z/rar 永不写——rar 法律禁止、7z 用户定案只读）；**不做私有格式**（互操作即护城河）。
- **已决策：命令扁平化**（2026-11-02 用户拍板）：`zpack compress/extract <任意输入>`——写格式由 `--output` 扩展名定（.zip/.tar/.tar.gz/.tgz），读格式靠魔数识别；旧 `zpack zip …` 形态退役。
- **已决策：格式策略**（2026-11-02 定案，同日修订）：读取格式**尽可能多**，判定标准是**开源实现/公开规格的可得性**而非格式出身——①公开格式（规格公开）尽量做；②私有格式但有许可兼容开源实现（MIT/BSD/公有领域）→ 纳入（alz=unalz BSD、pea=公开规格）；③只有 GPL 实现 → clean-room 重写才可（daa/isz=daa2iso/isz2iso），排末位；④无实现无规格（egg/j2j/k2k/bam）或安全污点格式（ace，CVE-2018-20250）→ 出局；rar 为专有出身例外（事实标准 + nwaples/rardecode MIT）。zpaq 实为开放标准（规格公开 + 参考实现公有领域），归公开格式。批次：A=bz2+zstd+单流gz+.001分卷（✅ 已交付）→ B=xz+lz4+brotli → C=7z+rar → D=iso+asar+uue+z+ar → E=cab/wim(LZX)/arj/lzh(+bh 捆绑评估)/lzip/nsis/zpaq → F=alz→pea→daa/isz（clean-room）。

## 铁律（继承 exkit）

1. **验收全绿才算实现完**：每个命令/行为必须在 `verify/verify.sh` 有用例（正常路径 + 退化输入 + 退出码），全绿才算完成。
2. **依赖准入（2026-11-08 用户放宽定案）：静态优先 + 预算 ≤500MB**：产物单文件自包含（裸机可跑）是硬目标——静态链接优先于动态；体积预算 500MB 内随意（当前实测静态全量约 130MB）。零新增**动态**链接仍有效（`otool -L` 不得挂 brew/系统外路径；Windows import 表仍仅出厂 DLL）。纯 Go 依赖照旧自由引入。许可证红线：GPL 库（x264/x265）不捆绑——当前静态组合 libjxl/libaom 均 BSD，ffmpeg 核心按 LGPL（对外分发需合规静态链接义务）。
3. **命名规范**：变量/函数全语义命名，禁单字母（循环下标 `idx` 例外）；常量 SCREAMING_SNAKE_CASE + 行上注释；固定字面量引用 `cmdutil.AppName`。
4. **Git**：主分支 `main`；Conventional Commits；**完成工作单元即主动本地提交**（不必等用户发话）；**绝对禁止 push**，仅本地提交。

## 输出契约（所有命令无例外）

### 核心原则

- **stdout 有且只有一个最终输出**（`internal/stdout` 输出管线约束层的 print 函数输出后立即 `os.Exit`，编码先进内存、失败路径 stdout 零字节——绝不半截 JSON）：调用方读完 stdout 即得到完整结果，无需解析多次。
- **print 即 exit，外层 defer 不运行**：任何清理（临时目录/半成品）必须在闭包内 defer（闭包 return 时执行）或 print 前显式调用——convert 的临时目录曾在成功路径静默泄漏即此因（2026-11-04 修复，清理行为已入验收）。
- **子命令 `--help`/`-h` 恒可用**：DisableFlagParsing 会吞掉 cobra 的自动 help，全部命令 Run 统一经 `helpAwareRun` 守卫（2026-11-04 修复——此前 `zpack compress --help` 报参数错误，agent 嗅探在此断裂）；主力命令带 Long 示例（compress/extract）。
- **stderr 是过程日志**（进度/警告/跳过明细），可被整体忽略（`2>/dev/null`），不影响结果判断。
- **退出码**：成功 `0` / 失败 `1`（无中间状态）。
- **is_success 与退出码的关系恒定**：exit 0 ⇔ `is_success:true`——刻意的双通道冗余（管道会吃掉 `$?`、stdout 是唯一穿越进程边界的信道）。**唯一例外**：判定类命令（test/hash --expect）「操作完成但结论为否」时，JSON 永远带完整判定字段（`is_success:false`/`is_match:false` + 实算值），退出码跟随结论；消费方规则——脚本可用退出码，但**管道/存档/GUI 场景只信 JSON 字段**。
- 数值参数严格校验（非法/越界 → error JSON + exit 1）；未知 flag 静默忽略；旗标一律长形态 `--word`；路径完整支持空格/中文（argv 直传，禁止拼 shell 字符串）。
- 平台不支持的功能分支必须 `ErrorPrint`「暂不支持 <平台>：<原因>」+ exit 1，禁止静默失败。

### stdout 输出格式（逐命令）

> **信封协议（2026-11-05 定案）**：所有命令统一 `{"is_success":true,"data":{...}}` / `{"is_success":false,"error_info":"..."}`；
> 判定类失败（test/hash --expect）= `{"is_success":false,"data":{判定字段}}`。下表「成功输出」列均为 **data 载荷**（读时自动包一层 data）。

| 命令 | 成功输出（exit 0） | 失败输出（exit 1） |
|---|---|---|
| `compress` | `{"is_success":true,"archive":"<绝对路径>","file_count":N,"size_bytes":N}` | `{"error_info":"人类可读原因"}` |
| `extract` | `{"is_success":true,"output":"<绝对路径>","file_count":N,"size_bytes":N,"skipped":N}` | `{"error_info":"人类可读原因"}` |
| `base64 encode`（无 --output） | 纯文本：Base64 编码字符串 | `{"error_info":"..."}` |
| `base64 encode`（有 --output） | `{"is_success":true,"output":"<路径>","size_bytes":N}` | 同上 |
| `base64 decode`（无 --output） | 原始解码字节（二进制安全，直接写 stdout） | 同上 |
| `base64 decode`（有 --output） | `{"is_success":true,"output":"<路径>","size_bytes":N}` | 同上 |
| `crypto genkey`（无 --output） | `{"is_success":true,"bits":2048,"public_key":"-----BEGIN...","private_key":"-----BEGIN...","public_b64":"...","private_b64":"..."}` | 同上 |
| `crypto genkey --output <前缀>` | `{"is_success":true,"bits":2048,"public_key":"<公钥路径>","private_key":"<私钥路径>"}` | 同上 |
| `crypto encrypt`（无 --output） | 纯文本：Base64 编码密文 | 同上 |
| `crypto encrypt --output <路径>` | `{"is_success":true,"output":"<路径>","size_bytes":N}` | 同上 |
| `crypto decrypt`（无 --output） | 解码后原始内容（二进制安全） | 同上 |
| `crypto decrypt --output <路径>` | `{"is_success":true,"output":"<路径>","size_bytes":N}` | 同上 |
| `list <归档>` | `{"format":"zip","entry_count":N,"file_count":N,...,"entries":[{"name":"...","size":N,...}]}` | `{"error_info":"..."}` |
| `test <归档>` | `{"is_success":true,"format":"zip","tested_entries":N,"all_ok":true}` 或 `{"is_success":false,"failures":["..."]}` | `{"error_info":"..."}` |
| `info <归档>` | `{"format":"zip","entry_count":N,"file_count":N,"dir_count":N,"total_uncompressed":N,"archive_size":N,"compression_ratio":N,"encrypted":bool}` | `{"error_info":"..."}` |
| `hash <文件...> [--algorithm 算法]` | `{"is_success":true,"algorithm":"sha256","files":[{"path":"<绝对路径>","size_bytes":N,"hash":"<hex>"}]}` | `{"error_info":"..."}` |
| `hash <文件> --expect <hex>`（匹配） | `{"is_success":true,"is_match":true,"algorithm":"...","path":"...","size_bytes":N,"hash":"...","expected":"..."}` | 同上 |
| `hash <文件> --expect <hex>`（不匹配） | exit 1：`{"is_success":false,"is_match":false,...,"hash":"<实算>","expected":"<期望>"}` | — |
| `hash <文件...> --output <清单>` | `{"is_success":true,"output":"<绝对路径>","algorithm":"...","file_count":N}`（shasum 文本格式，`shasum -c` 可验） | `{"error_info":"..."}` |
| `hash --check <清单>` | `{"is_success":true,"checked":N,"matched":N,"mismatched":[],"missing":[]}`；任一不匹配/缺失 → exit 1 同构 JSON（`mismatched`/`missing` 非空） | `{"error_info":"..."}` |
| `skill` | 纯文本：SKILL.md 全文（go:embed 内嵌，与二进制版本永不漂移） | `{"error_info":"..."}` |
| `media convert <输入> <输出>` | `{"is_success":true,"mode":"remux\|audio-only\|full\|audio-only-output","input":"...","output":"...","video":{"action":"copy\|encode\|none","codec":"...","bitrate":N},"audio":{...},"size_bytes":N,"original_size_bytes":N,"ratio":N}` | `{"error_info":"..."}` |
| `media compress <输入> [--target-size]` | 同上（mode 恒 full）+ `"target_bytes":N`；缺省目标=原始 50%；低于压缩下限（原始10%+音频）报错 | 同上 |
| `image compress <输入> [--target-size 100KB] [--priority size\|quality] [--output]` | 同 compress + `"strategy":"size-first","quality":90,"scale_percent":68,"target_bytes":N`（**双优先级**（2026-11-08 用户定案）：`size`（缺省）=质量钉 90 视觉无损档、二分缩放到刚好装下（5000×6000 类巨图主因是像素多）；`quality`=保像素优先（全尺寸降质至 q50 下限，不够再缩+回拉）；仅 jpg——webp=纯 Go 无损 VP8L 质量无效，png/bmp/tiff/gif 无损；源已达标 → 直拷贝 + `"already_fit":true`；缩至 5%+q1 仍超 → 报错引导；与 `--quality` 互斥） | `{"error_info":"..."}` |
| `image info <输入>` | `{"format":"jpeg","width":N,"height":N,"size_bytes":N,"color_model":"ycbcr","orientation":6,"display_width":N,"display_height":N}`（orientation/display 仅 EXIF≠1 时出现） | `{"error_info":"..."}` |
| `image rotate-left\|rotate-right\|rotate-180\|mirror\|flip <输入> <输出>` | `{"is_success":true,"input":"...","output":"...","source_format":"png","target_format":"png","width":N,"height":N,"size_bytes":N,...}`（纯像素重排无损，ffmpeg 像素级交叉验证） | `{"error_info":"..."}` |
| `json get <字段路径>` / `json ok` | stdin 读 JSON：get=值裸打印（stdout 纯文本；复合值紧凑 JSON；路径=点分段+数组下标）；ok=is_success → exit 0/1。**零依赖消费层——调用范式全面替代 python3 -c/jq**（2026-11-08 用户定案：单二进制闭环） | 非法 JSON/字段不存在/缺路径 → `{"error_info":"..."}` |
| `office convert <输入> <输出> [--try-msoffice\|--try-wps\|--try-libreoffice]` | `{"is_success":true,"source_format":"xls(et)","target_format":"xlsx","engine":"...","size_bytes":N}`（**全格式矩阵**：doc/wps→docx、xls/et→xlsx、ppt/dps→pptx——WPS 现代文件与 MS 旧格式同构（.wps/.et/.dps 魔数识别后按对应 MS 格式解析，source_format 报 `core(alias)`）；**族匹配强制**（doc 族只出 docx——跨族拒绝，office_oxide 会尽力转出错误产物故前置校验）；内置=office_oxide（Rust C ABI 静态链，MIT/Apache-2.0，cgo 层纯构建报需完整版）；--try-\* 外部引擎层纯 Go：`--try-msoffice`=Win COM 按类型分派（Word.Application/Excel.Application FileFormat=51/PowerPoint.Application FileFormat=24，go-ole 纯 syscall）/mac Word AppleScript 按包 ID 寻址；`--try-wps`=Win COM（KWPS/KET/KWPP）；`--try-libreoffice`=三平台 soffice --headless 隔离 profile + **目标格式动态**（--convert-to 随输出扩展）；探测层四段式（env 覆盖→PATH→平台动态解析→兜底；**任意安装位寻址**（2026-11-09 定案）：Win 的 ms/wps 走 COM ProgID 注册表自解析（`CreateObject` 即寻址——装哪个盘都行）；Win 的 LibreOffice 优先查注册表 `HKLM/HKCU\SOFTWARE\LibreOffice\UNO\InstallPath`（安装器权威写入，自定义安装位如 D:\Apps 也覆盖；32 位在 WOW6432Node）——目录猜默认位仅兜底 + 盘符扫描（绿色解压版无注册表场景）；**显式指路双通道**：CLI 旗标 `--libreoffice-path <根目录|soffice.exe 全路径>`（进程内覆盖同名 env，值容错目录形态）> 环境变量 `ZPACK_OFFICE_LIBREOFFICE_PATH` > 探测链）；验证=soffice 回读交叉（inline string 产物无 sharedStrings——csv/pptx XML 回读才是互操作铁证）） | `{"error_info":"..."}` |
| `pdf merge <1.pdf 2.pdf...> <输出>` | `{"is_success":true,"output":"<绝对>","inputs":[...],"input_count":N,"page_count":N,"size_bytes":N}` | `{"error_info":"..."}` |
| `pdf extract <输入> --pages 1-3,5,8- [--output]` | `{"is_success":true,"input":"...","output":"...","pages_spec":"...","pages":[N...],"source_page_count":N,"size_bytes":N}`（页码 1 起闭区间；`8-`=到末页、`-5`=首页起；升序去重；**区间末端超出自动截至末页+stderr 提示**（宽尾，同视频剪辑语义）——起始页超出/单页不存在仍报错；缺省输出=同目录 `<名>-extract.pdf`） | 同上 |
| `pdf resize <输入> --size a4\|a4l\|letter\|210x297mm\|595x842 [--pages] [--output]` | `{"is_success":true,"size_spec":"a4","width_pt":595,"height_pt":842,"pages":[N...],"page_count":N,"total_pages":N,"size_bytes":N}`（**页面尺寸统一**——图片型 PDF 各页图片大小不一 → 统一到纸型/自定义尺寸，内容按页缩放；横页 best-fit 保横（a4→A4L），尾缀 `L/P` 强制方向；**文档型拦截·选中页域**（2026-11-08 用户定案修订：只拦**选中页**中的文字页——混合型 PDF 可 `--pages` 圈住图片页避开文字页不连坐；报错**点名具体页码**「第 2、5 页含文字」+ 指路 `pdf info --all-pages` 与 `--force`；`--force` 放行+stderr 警告亦点名页码）；缺省全部页；缺省输出 `<名>-resized.pdf`） | `{"error_info":"..."}` |
| `pdf info <输入> --all-pages` | 同 info + `"pages":[{page,width_pt,height_pt,rotation}]`（全页尺寸+角度+**逐页 text 文字标注**；info 另增 `text_based` 文档型判定——resize 前体检闭环） | 同上 |
| `pdf rotate-left\|rotate-right\|rotate-180 <输入> [--pages 1-3] [--output]` | `{"is_success":true,"input":"...","output":"...","direction":"left","pages":[N...],"page_count":N,"total_pages":N,"size_bytes":N}`（**/Rotate 元数据零损**——同视频写矩阵；方向语义同 image/media 家族；**增量叠加**（已转页再转=累加）；`--pages` 缺省全部页；缺省输出=同目录 `<名>-rotated.pdf`；**mirror 不支持**——PDF 无镜像属性且文本会镜像不可读） | `{"error_info":"..."}` |
| `pdf info <输入>` | `{"is_success":true,"page_count":N,"width_pt":N,"height_pt":N,"width_mm":N,"height_mm":N,"size_bytes":N,"rotation":90}`（尺寸已反映 /Rotate 观看态；rotation=首页角度，0 不出现） | 同上 |
| `image merge <图...> [--reference] <输出>` | `{"is_success":true,"reference":"...","inputs":[...],"output":"...","direction":"vertical","width":N,"height":N,"format":"png","image_count":N,"size_bytes":N}` | `{"error_info":"..."}` |
| `ffmpeg <原版任意参数...>` | **契约唯一例外**：透明代理（进程内直调——ffmpeg CLI 的 main 符号改名后静态链入，`zpack ffmpeg` 即普通 C 函数调用，零释放零子进程，原理见「ffmpeg CLI 进程内直调」节）——stdout/stderr/exit code 均为 ffmpeg 原生语义（stdout 可为二进制媒体流，`pipe:1` 场景必需），不走 JSON 信封；纯构建报「需完整版」 | — |
| `audio convert <输入> <输出> [--bitrate] [--sample-rate] [--mono]` | `{"is_success":true,"mode":"audio-only-output","audio":{"action":"encode","codec":"...","bitrate":N},"size_bytes":N,"ratio":N,"bitrate":N,"sample_rate":N,"mono":true}` | `{"error_info":"..."}` |
| `audio compress <输入> [--bitrate] [--mono]` | 同上 + `"original_bitrate":N`；缺省目标=原始码率 50%（下限 32k）；目标≥原始明确拒绝 | 同上 |
| `skill --install [目录]` | `{"is_success":true,"skill":"zpack","installed":["<绝对路径>"],"size_bytes":N}`（无目录参数=探测 `~/.claude/skills`/`~/.agents/skills` 等全部命中处） | 同上 |

### stderr 过程日志（可忽略，不影响结果判断）

所有 stderr 输出格式统一为：`[YYYY-MM-DD HH:MM:SS] 消息内容`（无级别标签即常态——单级类别是零信息装饰；标签位为未来例外级 WARN 预留，出现标签本身就是信号）

**统一开始日志**：每个命令入口 stderr 恒一行 `zpack <命令名> 开始执行`——由 cli 装配层（root 的 PersistentPreRun）一处约束全部命令；只记命令名**绝不记参数**（参数可能含密码/密钥，落终端即泄密）。

| 类型 | 触发场景 | 示例 |
|---|---|---|
| **进度条** | 操作耗时 >5 秒时每 5 秒采样 | `压缩中 ███████░░░░░░░░░░░░░░░░░░░░░░░░ 25.0% \| 500.0MB/2.0GB \| 100.0MB/s \| 1/4 文件 \| 剩余 15秒` |
| **GBK 名修复** | 解压遇非 EFS 且非 UTF-8 文件名 | `条目名按 GBK 解码: "ÖÐÎÄ" -> "中文"` |
| **跳过已存在** | 解压遇已有文件且无 --force | `跳过已存在: /path/to/file.txt` |
| **分卷创建** | --volume-size 切分时每卷一条 | `分卷 /path/to/out.zip.001: 65536 字节` |
| **部分加密降级** | rar 混合密码预扫降级 | `部分加密 rar：预扫降级，根目录按归档基名保守包裹` |
| **密码错误跳过** | rar 逐文件密码不匹配 | `密码错误跳过: b.txt` |
| **HSA 降档** | 压缩遇不可压大文件降 Store | （进度条中的百分比变化体现） |

### 调用方（AI/GUI/脚本）消费指南

```bash
# 只拿最终结果（推荐）——stdout 解析一次 JSON 即可
result=$(zpack compress 目录/ --output out.zip 2>/dev/null)
# result = {"is_success":true,"archive":"/abs/path/out.zip","file_count":5,"size_bytes":12345}

# 判断成功失败
echo "$result" | python3 -c "import json,sys; r=json.load(sys.stdin); exit(0 if r['is_success'] else 1)"

# 同时看进度（实时）——stderr 管道到终端
zpack extract big.zip 2>&1 | tee /dev/stderr | python3 -c "import json,sys; r=json.load(sys.stdin); print(r)"

# 完全静默（脚本/CI）——只看退出码
zpack compress 目录/ --output out.zip > /dev/null 2>&1 && echo "OK" || echo "FAIL"
```

## 命令规格（2026-11-02 扁平化定案：无格式子命令）

**`zpack compress <目录|文件...> [--output out.zip|out.tar|out.tar.gz] [--exclude 名称]... [--level 0-9] [--jobs N]`**
- **写格式由 `--output` 扩展名决定**：`.tar` → tar、`.tar.gz`/`.tgz` → tar.gz、其余（含缺省 `CWD/<首输入名>.zip`）一律 zip；已存在则覆盖；**`--volume-size <尺寸|模板>`** 分卷创建（100MB/1GB/64KB/65536 人读格式；**模板**（2026-11-04，大小写归一）：`gmail`25MB／`cd650`650MB／`cd`=`vcd`700MB／`fat32`4GiB-1／`dvd`=`dvd5`4.7GB／`dvd9`8.5GB／`bd`=`bd25`25GB／`bd50`50GB——介质标称容量 **SI 十进制精确字节**，与数字后缀的二进制 1024 基并存；格式无关字节切分 `.001/.002/...`，cat 即还原；**整档 ≤ 单卷大小时不切分——保持原文件名，不产孤零零的 .001**；下限 64KB=65536；tar/zip/tar.gz 通吃）
- **`zpack compress <目录|文件...> --output out.zip --password <值>`** 加密 zip 写入（WinZip AES-256 AE-2，零第三方依赖——PBKDF2/AES-CTR/HMAC 反向使用读取侧原语；`--password-table` 密码表支持逐文件密码；已知限制：p7zip 17.05 旧版不识别，自产自解+主流新版 7-Zip 待验证）
- **`.zpackignore`**（gitignore 语义）：输入目录根/CWD 的 `.zpackignore` 自动生效——glob 模式、`#` 注释、`!` 反选、`/dir/` 目录限定、`/root` 根限定；`.zpackignore` 自身始终排除
- 通用：默认排除系统垃圾（`.DS_Store`/`__MACOSX`/`Thumbs.db`/`desktop.ini`/`._` 前缀）；`--exclude` 按名精确匹配；目录以目录名为包内根前缀、多文件平铺进包根；中途失败删半成品；空目录不保留；非法 UTF-8 文件名报错；**`--temp-dir <目录>`**（2026-11-04，compress/extract/convert/bench 四命令通用）：临时落点指定（zip 分块溢写/convert 中转/bench 语料/cab 大文件夹溢写），缺省系统临时目录（Windows 上 %TEMP% 几乎总在 C: 盘——大转换指定到目标盘规避系统盘空间紧张）；指定目录不存在自动 MkdirAll；用后即清（只删自己的子目录，不动用户基目录）
- zip 专属：`--level` 默认 6（0=全 Store）；`--jobs` 默认 `min(核数,8)`、压缩侧上限 8，1-64 严格校验；**条目级并行**（klauspost flate 文本 2.1× 标准库，主协程按收集顺序 `CreateRaw` 落盘，同输入产出字节确定）；**大单文件 128KB 分块并行**（**阈值 0x40001=256KB+1（业界主流压缩器 MT 切换点）**；非末块 sync-flush 缝合、末块 final；分块 CRC 用 zlib `crc32_combine` 公开算法拼合）；**分块字典衔接**（每块 `NewWriterDict` 预置前块尾 ≤32KB，跨块回引合法且比值近单流）；**HSA 前置直通**（不可压扩展名 30+ + 魔数佐证 → Store）+ **HSA 运行时降档**（累计满 30MiB(0x1e00000) 且压缩比 ≥99% → 剩余块降 level 0（deflate 流内 stored 块，容器不变）；%PDF 首块豁免；**快照信号设计保确定性**——装配器按序发累计快照，派发器收第 k-W 个快照后决策，决策输入纯函数化）；包内文件名强制 UTF-8（EFS 位 bit 11）
- tar 专属：**tar.xz/tar.zst 写入**（现有依赖 ulikunitz/xz + klauspost/zstd 接入压缩壳——.txz/.tzst 短扩展名也支持）；tar.bz2 写入暂不支持（纯 Go 无成熟 bzip2 编码器——读取已支持）；PAX 格式确定性写入（Uid/Gid/Uname/Gname 归零、ModTime 截秒、权限只取 perm 位——同输入产出字节确定）；符号链接原样保留（tar 惯例；zip 才解引用）；`--level` 作用于 gzip 档；单流顺序写 `--jobs` 不参与，流式拷贝内存有界
- 输出：`{"is_success":true,"archive":<绝对路径>,"file_count":N,"size_bytes":N}`（字段 2026-11-02 由 `zip` 更名 `archive`，多格式语义）

**`zpack extract <归档> [--output 目录] [--jobs N] [--force]`**
- **读格式靠魔数识别不靠扩展名**：zip（PK 前缀 + SFX 兜底——EOCD 尾部定位）/ tar 四压缩壳 tar.gz/tar.xz/tar.bz2/tar.zst（压缩流魔数 + 内层 ustar 佐证）/ tar（ustar@257，覆盖 ustar/PAX/GNU 三系）/ **gzip·bzip2·zstd·xz·lz4·lzip 单流 + brotli（.br 扩展名门控）+ .Z（compress LZW，libarchive 公开算法移植）+ uuencode/xxencode（begin 头行嗅探，.xxe 扩展名区分；内嵌文件名优先，.uue/.xxe 裸体流不支持）**（非容器，解出为单文件，名=基名去压缩扩展名或内嵌名；跳过/覆盖语义同容器）/ **`.001` 顺序分卷**（首卷自动拼段 `.002/.003…` 为逻辑单文件后按内层魔数分发——zip 分卷即通；内层非归档则拼接还原单文件）；**7z 读取**（bodgit/sevenzip，随机访问，加密档案明确报暂不支持）；**rar 读取**（nwaples/rardecode v1.1.3，自家多卷命名自动续卷，创建法律禁止，v1.1.3 头无链接标记→rar 符号链接按普通文件落地）；**wim 读取**（nradchenko/go-wim，LZX 按需解码，多镜像按 N-镜像名/ 平铺；局限：LZMS/ESD、XPRESS、重解析点不支持）；**cab 读取**（MSCF + 存储柜直拷 + MSZIP 逐块独立 deflate 配 32K 跨块预置字典 + LZX ChunkDecoder 含 Intel E8 翻译——LZX+E8 已 7z 字节级交叉验证；Quantum 明确报暂不支持、跨柜多卷暂不支持）；**ar 读取**（GNU name///长名表//与 BSD #1/len 双命名系，纯 ReadAt 定位）；**asar 读取**（Electron 双 pickle 头 + JSON 目录，.asar 扩展名门控，unpacked 条目读兄弟目录）；**iso9660 读取**（kdomanski/iso9660，Rock Ridge 名生效，Joliet 名暂按基础层读——局限）；伪头/损坏明确报「无法读取 <格式> 归档」；无法识别报错（分卷源则回退还原）
- **`--password-table <文件>`**（边角功能，2026-11-03）：密码映射文件，每行 `匹配模式=密码`（精确文件名或 glob，`*=default` 兜底，`#` 注释）；**zip 逐文件独立生效**（随机访问+桥接解码器天然支持），rar/7z 取 `*` 通配命中作整档单密码（顺序流架构限制——逐文件多密码需多遍扫描，暂不做）；优先于 `--password`
- 通用：`--output` 显式指定直接解进去；缺省智能解压（unar 语义）：单根直落当前目录、散装包一层以归档基名（`.tar.gz`/`.tgz` 剥复合后缀）命名的根目录（**当前目录最多出现一个新目录**）；`--force` 覆盖、**缺省跳过**已存在（O_EXCL 原子探测 + `skipped` 计数，保护已改动文件）；GBK 文件名修复（非 EFS 且非 UTF-8 → GBK 尽力解码，逐条 stderr 日志）；路径穿越防护（反斜杠归一后绝对路径/盘符/`..` 段拒绝）；重复条目名拒绝；校验自动
- zip 专属：**加密读取**（WinZip AES + ZipCrypto，`--password` 值——零第三方依赖自实现 PBKDF2/AES-CTR/HMAC）；zip64 自动；空目录条目照建；`--jobs` 并行解压（缺省 `max(1, 核数/2)`、硬上限 32、≤文件数）
- tar 专属：符号链接保留（相对链接放行；绝对/盘符/以条目目录锚点解析后逃出归档根拒绝）；硬链接按归档内成员复制落地（Windows 无特权可用）；设备/管道条目拒绝；GNU sparse 透明读取；**预扫全验后动工**（tar 顺序流无随机访问：预扫+解出两遍读，`--jobs` 不参与）
- 输出：`{"is_success":true,"output":...,"file_count":N,"size_bytes":N,"skipped":N}`

双向进度每 5 秒 stderr，操作耗时 >5 秒时输出进度条（`压缩中 ███████░░░░ 25.0% | 500.0MB/2.0GB | 100.0MB/s | 1/4 文件 | 剩余 15秒`）；<5 秒不输出（合理——秒级操作无需进度）。格式详见输出契约节。

**`zpack hash <文件...> [--algorithm 算法] [--expect 值] [--output 清单] [--check 清单]`**（2026-11-04）
- 算法：`md5/sha1/sha224/sha256/sha384/sha512/crc32`（纯标准库零新依赖；crc32=IEEE 多项式，zlib/zip 同款）；缺省 sha256；算法名大小写/连字符归一（`SHA-256`=`sha256`）；1MB 流式读取内存有界
- 四形态：缺省=计算（多文件逐条）；`--expect <hex>`=单文件校验（恰好一个文件；匹配 exit 0 / 不匹配 exit 1 结构化 JSON 含实算值——脚本用退出码、GUI 用字段）；`--output <清单>`=生成校验清单（shasum 文本格式 `<hex><两空格><原路径>`，`shasum -a N -c` 直接可验——双向互操作已入验收）；`--check <清单>`=批量校验（算法按 hex 长度自动判定：8=crc32/32=md5/40=sha1/56=sha224/64=sha256/96=sha384/128=sha512，混合清单可；相对路径以清单所在目录为基准；`#` 注释行跳过；任一不匹配/缺失 exit 1）
- 退化：未知算法/路径不存在/目录/expect 长度不符或非十六进制 → error JSON + exit 1

**`zpack skill [--install [目录]]`**（2026-11-04，agent 消费端自描述）
- SKILL.md 经 `go:embed` 内嵌于二进制——单文件分发且文档与版本永不漂移；`zpack skill` 输出全文，`--install` 探测 `~/.claude/skills`/`~/.agents/skills`（家目录级）与 `./.claude/skills`/`./.agents/skills`（项目级，仅当已存在）全部安装，或显式指定目录；安装文件与 `zpack skill` stdout 逐字节一致
- 内容=输出契约+命令速查+配方（从 AGENTS.md 提炼，给消费端 agent 渐进披露：frontmatter description 常驻、正文按需加载）

**`zpack media convert|compress ...`**（2026-11-05，go-astiav 进程内引擎 + 决策层）
- `media convert <输入> <输出.mp4|mkv|mov|webm|mp3|m4a|flac|opus|wav>`：**尽量无损三级瀑布**（SmartRender 矩阵复刻）——①目标容器全兼容且无旋转→remux 全流拷贝（bit-exact）②视频兼容音频不兼容→拷视频+转音频 ③视频不兼容→全编（码率跟随源=autoMask bit3 语义）；音频容器输出→丢视频抽音轨（同编码拷贝/异编码转）；跨容器 remux 清 codec_tag 由目标 muxer 重分配
- `media compress <输入> [--output 路径] [--target-size 50%|100MB|cd|dvd]`：**业界主流公式族**——总码率=目标×8÷时长×0.99；音频>128k 降 128k、≤128k 保留；视频=总−音频（下限 32k）；下限保护=原始×10%+音频大小（低于报错）；纯音频=码率全给音频；缺省 --target-size 50%、输出缺省同名 .mp4
- 纯 Go 构建（CGO_ENABLED=0）media 命令报「需完整版」不崩；完整版走 build_cli/ 产线（mac 已验证：brew libav 动态链接）
- **预设库**（2026-11-05）：`media/presets.json`（官方公开规格整理，社区共建=直接改 JSON 提 PR）——`media preset` 列表/详情；`--preset <ID>` 施加约束（convert：容器+缩放上限（只降不升保比例 FitWithinBox 偶数对齐）+码率上限（源超 1.1× 强制重编）；compress：max_size_bytes 为目标，**源已达标 → 直拷贝 remux + already_fit:true 不浪费画质**）；起步集 8 档：youtube/bilibili/douyin（social）+ gmail/discord/wechat 25MB（messaging）+ iphone/appletv（apple）
- **media info 体检**（2026-11-05）：探测事实（容器/时长/尺寸/帧率/双流码率/旋转）+ **确定性压缩建议**（`suggestions[]`：resolution→`--screen 1080p`（像素密度不变）、codec-generation→`--codec hevc`（代际系数 0.55）、bitrate-inflated（bpp>0.25 健康度）、audio 降 128k）——AI/用户先读报告再挑旗标，建议规则全可单测；**skill.md 已写入 Agent 工作流：先 info 体检 → suggestions 按 order 排序（1=bake 先行 2=几何/码率 3=换码）→ flag 拼旗标 → 查 mode/ratio 报告损益**
- **几何变换命令**（2026-11-05）：`media rotate-left|rotate-right|rotate-180|mirror <输入> <输出>`——旋转族**默认零损写 display-matrix**（源无矩阵时秒级、编码字节不变；PSNR 对照验证与烤入路径同向）+ `--bake` 强制烤入（transpose 无损像素重排+重编码）；源已有矩阵自动降级烤入（SideData Add 不可替换）；mirror 恒烤入（矩阵镜像播放器支持不可靠）；±90° 编码目标宽高自动对调；`--bake` 烤入**净角度**（源旋转+命令增量——源矩阵随重编丢失必须一并烤实）；**convert --bake 已退休**（2026-11-07）——物理化统一走 `media bake` 一级命令；media info 的 rotation 恒显示（0=无）+ `display_width/display_height` 显示尺寸（±90° 与存储对调）
- **正交指令**（2026-11-05）：`--screen 2160p|1440p|1080p|720p|480p|360p`（尺寸上限，只降不升保比例）与 `--codec h264|hevc|vp9|av1`（换码，容器白名单校验）——convert/compress 通用，可组合（如 4K→`--screen 1080p --codec hevc`）；指令与决策矩阵协作：需缩放或换码≠源 → 强制 full；`--screen` 与 `--preset` 互斥（预设自带适配框）；`media info` 的 `suggestions[].flag` 直接对应此二旗标（skill 闭环）
- **正交指令**（2026-11-05）：`--screen 2160p|1440p|1080p|720p|480p|360p`（尺寸上限，只降不升保比例）与 `--codec h264|hevc|vp9|av1`（换码，容器白名单校验）——convert/compress 通用可组合（4K→`--screen 1080p --codec hevc`）；与决策矩阵协作：需缩放或换码≠源 → 强制 full；`--screen` 与 `--preset` 互斥；`media info` 的 `suggestions[].flag` 直接对应此二旗标（skill 闭环）
- **旋转与字幕（2026-11-05 修复，原 v1 局限①②）**：旋转读 display-matrix 侧数据（2016+ 手机标准）+ 旧式 rotate 标签兜底——remux 时矩阵随流参数零损保留（旋转不再否决直拷），重编时 transpose 烤入（无损像素重排，方向经 ffmpeg autorotate 参考帧 PSNR 对照验证）；字幕轨按容器兼容表保轨（mkv 全兼容/mp4 mov_text/webm webvtt），不兼容丢弃**必告警**（stderr `跳过字幕流 #N`）
- **可烤元数据 #2 SAR**（2026-11-06）：像素宽高比探测（StreamProbe sar_num/sar_den）→ info 显示尺寸 SAR 感知（720x576 SAR16:15 显示 768x576）+ aspect-ratio 建议；缩放路径归一方像素（scale 后 setsar=1 滤镜 + encCtx SAR 同步 1:1——只做滤镜会被流级 SAR 盖掉双重校正）；适配（--screen/--preset）全部在显示坐标系（DisplayDims = 存储×SAR±90°对调）
- **bake 一级命令**（2026-11-07）：`media bake <输入> <输出>`——显示元数据物理化的统一入口（把散在 convert --bake/rotate --bake 的能力收拢）；info 的 `bakeable` 字段直接回答「本文件可烤什么」（当前：rotation+sar；判定就绪待接：interlace/color-range）；无可烤 → remux 幂等（`baked:[]` 恒出而非缺省——v2 omitzero 保指针空数组可见）；`suggestions` 有 `bakeable` kind 指路
- **`zpack image ...`（2026-11-08 HDR 解码 + EXIF Orientation 定向链）**：HDR 图片走 astiav 通道（零新增依赖）——EXR（OpenEXR gbrpf32）与 Radiance HDR（RGBE→float）读取：SDR 值域（max≤1.05）直通保逐位一致，真 HDR 扩展 Reinhard 滚降（白点→1.0，纯函数表驱动单测+python 直造 EXR 手算值端到端验证）；AVIF 归 HEIC 家族通道——**修复潜伏 bug：ConvertHEIC 的 execFFmpeg 是空壳（HEIC/AVIF convert 从未真工作）**，现经 astiav 首帧解码 + sws（Bicubic 对齐 ffmpeg CLI）——与 ffmpeg rgba64le 逐位等价；纯构建侧 stub 报「需完整版」；JXL 待 ffmpeg 构建带 libjxl 再收编。ICC→sRGB 仍缓期。：图片界的"可烤旋转"=EXIF Orientation（JPEG APP1/TIFF IFD0 零依赖解析）；**处理路径读取即定向**（convert/compress/resize/merge/几何变换一律先物化为观看态像素——zpack 输出不携带 EXIF，不定向则 orientation≠1 照片产出横倒）；`image info` 如实报告存储尺寸+orientation+display 尺寸（5-8 对调）；方向经 ffmpeg autorotate 四象限交叉验证。PPI 密度元数据（JFIF/pHyS）=打印尺寸契约，烤入路径=`resize --ppi`；ICC→sRGB 缓期（纯 Go 无零依赖 ICC 解析）。

**`zpack audio convert|compress|info ...`**（2026-11-08，音频与视频分家——同一 astiav 引擎、独立命令面）
- `audio convert <输入> <输出.mp3|m4a|flac|wav|ogg|opus>`：跨格式互转 + 质量旗标 `--bitrate 128k`/`--sample-rate 44100`/`--mono`（aformat 滤镜自动重采样/混音下变换）
- `audio compress <输入> [--bitrate 64k] [--mono]`：**缺省压到原始码率 50%**（下限 32k），`--output` 缺省就地覆盖；目标≥原始明确拒绝（要转格式用 convert）；同格式+指定码率=强制重编码（决策矩阵直拷被质量旗标否决——质量旗标干预时视频容器走 audio-only 模式拷视频+重编音频，不丢视频流）
- `audio info <输入>`：复用 media info（音频流探测）
- 实测：320k 402KB → 160k 201KB（50.0%）→ 64k 80KB（20%）；--mono/--sample-rate 经 ffprobe 交叉验证落地

已知局限 v2：文本字幕互转（srt↔mov_text）未做（不兼容即丢+告警）；两遍编码阻塞于 astiav v0.43 未暴露 stats_file/stats_in（上游暴露后半天接上，或改采样逼近法）；单遍编码目标偏差实测 ±5%（易压内容欠靶更大）

## 构建与验收

```bash
go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)" -o bin/zpack ./cmd/zpack
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o bin/zpack.exe ./cmd/zpack   # 交叉编译

go test ./...          # 单测（表驱动；纯逻辑函数才测，run* 函数直接 os.Exit 不测）
go vet ./...           # + GOOS=windows / GOOS=linux go vet ./... 三平台编译验证
bash build_cli/mac/build.sh   # mac 完整版产线：自动触发 ffmpeg_static.sh（首次源码级全静态 ~25 分钟，prefix 幂等）→ 静态链接 go build（ffmpeg CLI 对象进程内直调，~48MB 单文件）→ 校验零 brew 动态依赖
bash verify/verify.sh        # 行为门禁：只测 bin/zpack（纯 Go 产物），确定性输出全字节比对（预期 226/226）
bash verify/verify-media.sh  # 媒体门禁：需产线完整版 bin/zpack（128 用例：转码矩阵/压缩公式/几何变换/bake/HDR/EXIF/audio/ffmpeg 直调），夹具用系统 ffmpeg 生成
bash verify/verify-image.sh  # 图片门禁：纯 Go 即可（65 用例：格式互转/物理尺寸/变换 ffmpeg 像素级交叉/merge/EXIF 定向）
```

### 双层开发节奏（cgo 早已按 build tag 分层——纯功能开发用纯循环）

**纯 Go 循环（默认——pdf/hash/压缩/图片标准格式等纯功能的日常开发）**：
```bash
CGO_ENABLED=0 go build -o bin/zpack-dev ./cmd/zpack   # 热缓存 ~1s / 冷 ~10s
CGO_ENABLED=0 go test ./...                            # ~4s
bin/zpack-dev <命令>                                   # media/HDR/HEIC/ffmpeg 走 stub 报「需完整版」
```
**完整循环（media/audio/HDR/ffmpeg 直调开发时）**：`export PKG_CONFIG_PATH=build_cli/mac/prefix/lib/pkgconfig` 后 cgo 构建即可（`internal/ffmpegcli/libs/` 已包内自含，无需产线环境变量）；发布产物走 `build_cli/mac/build.sh`。
**新功能家族的落位惯例**（如 pdf）：顶层包 `pdf/`（纯 Go，同 `image/` 惯例：引擎纯函数+文件，CLI 面 `cli/pdf.go`，**门禁进 `verify/verify.sh`**——纯构建可跑，反馈秒级）；仅当真的要碰 ffmpeg/libav 才进 cgo 层。**注意**：`build_cli/mac/build.sh` 会 `go clean -cache`——跑完产线后 cgo 冷构建要等一会，属预期。

### mac 静态产线全记录（build_cli/mac，2026-11-08）

**流程**：`bash build_cli/mac/build.sh` → 缺产物自动触发 `ffmpeg_static.sh`（源码级全静态，首次 ~25 分钟，`prefix/` 幂等复用跳过）→ `go clean -cache` 后静态链接构建 → 断言 `otool -L` 零 brew 动态依赖。产物 `bin/zpack` ~48MB，动态依赖仅系统出厂库——**单文件裸机可跑**。`src/` 与 `prefix/` 不入 git（.gitignore），换机重跑产线即复现。

**配料表**（版本钉死）：libjxl v0.11.1（**SKCMS 后端**）/ libaom v3.11 / x264（videolan git）/ x265（**Bitbucket `multicoreware/x265_git`**——code.videolan.org 镜像要认证）/ lame 3.100（sourceforge tarball）/ opus v1.5.2 / libvpx v1.15 / FFmpeg **n9.0**（2026-11-09 用户定案 8.1.2→9.0 与 Windows 产线对齐；mac 侧脚本已改、**产线待复跑重验**）。ffmpeg configure 关键旗标：`--enable-gpl --enable-libx264 --enable-libx265 --enable-libmp3lame --enable-libopus --enable-libvpx --enable-libjxl --enable-libaom --enable-videotoolbox --disable-ffplay --disable-xlib --disable-libxcb`。GPL 库（x264/x265）入列=产物承担 GPL 义务；LGPL 退路=去掉二者（VideoToolbox 仍可 h264/hevc 硬编）。

**ffmpeg CLI 进程内直调（零释放定案原理，2026-11-09 用户定案记录）**：

- **旧方案为何退役（用户明确厌恶）**：把完整 ffmpeg.exe 二进制（35MB）嵌入 zpack，`zpack ffmpeg` 运行时先**释放成临时文件**再起子进程执行——四宗罪：污染磁盘（临时目录留垃圾、清理失败残留）、**杀毒软件高概率误报**（运行时释放可执行文件是病毒典型行为）、并发冲突（多实例抢同一释放路径要加锁）、慢（每写 35MB + 起进程开销）。go:embed 嵌入/尾注附二进制两版均已退役。
- **现方案原理**：编译期把 ffmpeg CLI 当普通库链入——`fftools/ffmpeg.c` 重编译后**符号改名**（mac：`-Dmain=zpack_ffmpeg_cli_main` 编译宏；Windows：`objcopy --redefine-sym` 目标文件层改名——`-Dmain` 被 ffmpeg 的 `_WIN32` 分支 `#undef main` 取消，见 Windows 陷阱 #15/#16），连同 fftools 全对象打包 `libzpack_ffmpeg_cli.a`（~0.5MB），与 libav 全家静态链进 zpack.exe 本体。**ffmpeg 的 main 就是我们进程里的一个普通 C 函数**。
- **运行时形态**：`zpack ffmpeg <参数>` = Go 侧构造 argc/argv → 调用 `C.zpack_ffmpeg_cli_main(argc, argv)`（`internal/ffmpegcli/cli_cgo.go`）——**零文件释放、零子进程、零缓存目录**；main 结尾 `exit()` 终结进程，退出码/stdio 全 ffmpeg 原生语义（契约唯一例外）。Windows 侧 argv 须堵 `prepare_app_arguments` 的 `GetCommandLineW()` 重取（陷阱 #17），否则 ffmpeg 拿到宿主命令行。
- **运行时零释放已实测验证**（2026-11-09 Windows）：x264/x265 转码 + media info + hash 全轮跑完，临时目录与 CWD 新增文件恰等于产出物本身；转码进行中系统无 ffmpeg 子进程。
- 产线拷入 `internal/ffmpegcli/libs/`（`#cgo LDFLAGS -L${SRCDIR}/libs`——**裸 `go test`/`go build` 不依赖产线环境变量**）。

**陷阱清单（全部实际踩过；改产线/调 ffmpeg 集成前先读）**：

1. **configure 找不到自建库**：ffmpeg 探测第三方全靠 pkg-config——`export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"` 必须在 `./configure` 前设置
2. **libjxl 0.11.1 .pc 静态闭包缺陷**：`libjxl.pc` 缺 `-lbrotlicommon`（brotlidec 依赖它）、`libjxl_threads.pc` 闭包全缺——产线 sed 补齐，否则 configure 探测必挂
3. **libjxl 默认 lcms 后端是坑**：lcms submodule 编了不装静态库 → `libjxl_cms.a` 悬 lcms 符号链接失败；`-DJPEGXL_ENABLE_SKCMS=ON` 换自含后端
4. **x265 cmake 不装 pc 文件**（模板 `x265.pc.in` 未处理）：手写一份，依赖闭包（`-lc++ -lpthread -lm`）必须写进 `Libs:` 行——configure 探测期**不读** `Libs.private`
5. **空文件陷阱**：`set -u` 中断的 heredoc 会留下 0 字节 pc 文件，幂等守卫用 `-s`（非空）而非 `-f`（存在），否则永久跳过重写
6. **X11/xcb 自动探测**：ffmpeg configure 在 mac 上会探测到 brew 的 X11 把动态依赖带进静态链接——`--disable-xlib --disable-libxcb`；ffplay 依赖 SDL2（静态不可得）`--disable-ffplay`
7. **cgo 构建缓存吃掉环境切换**：pkg-config 结果进 Go 构建缓存且不随环境变量失效——切换静态前缀后必须 `go clean -cache`，否则仍链 brew dylib
8. **fftools 8.x 对象散布子目录**（`graph/`、`textformat/`）：ar 清单用 `fftools/*.o fftools/*/*.o` glob + 排除法；排除模式必须锚定 `/(ffmpeg|ffprobe|ffplay)\.o$`——`grep -v "ffmpeg.o"` 的正则点号会误吞 `ffmpeg_opt.o`
9. **C argv 惯例**：argv[0]=程序名占位（ffmpeg 的 usage/报错引用它）、argc 含程序名、末位 NULL——传错 ffmpeg 视为零参数只打 usage
10. **cgo 下 C `exit()` 不 flush stdio**：终端正常、管道/重定向丢输出——调用 main 前 `setvbuf(stdout, NULL, _IOLBF, 0)` 钉行缓冲
11. **ffmpeg main 失败路径会返回负值**（非 exit）：与 stub 语义冲突过——`ffmpegcli.Available()` 探针分流；负值=真实失败码照传 `os.Exit`
12. **astiav `ImageBufferSize/ImageCopyToBuffer` align 必须 ≥1**：传 0 → EINVAL → size 0 → 空切片 panic（`b[0]` 越界）
13. **sws 像素比对的参照选择**：sws 缺省 FAST_BILINEAR、ffmpeg CLI 缺省 Bicubic（色度边缘分歧）；rgb24 CLI 路径自带 dithering——逐位等价的正确参照是 `rgba64le` 低字节
14. **解码循环**：单包直收对 libjxl 不够（首包不吐帧 EAGAIN）——收帧-读包循环 + `SendPacket(nil)` EOF 冲洗；帧跨函数传递用 `Ref()` 克隆（内部帧 free 后调用方仍有效）

**office_oxide 静态产线（2026-11-08）**：`build_cli/mac/office_static.sh`——v0.1.16 @ cargo（需 brew rust），crate-type 自带 staticlib → `office/libs/liboffice_oxide.a`（44MB，链接按需取件实际增重 ~12MB）+ 头文件；**克隆后必须清除 examples/cmd 与游离 *.go**（污染宿主模块 `go build ./...` 包图——踩过）。作者 Yury F.（pdf_oxide 1179⭐ 同作者）；许可 MIT/Apache-2.0。手搓 cgo 薄壳（SRCDIR libs 模式，同 ffmpegcli），不用其官方 Go 包（env-var 链接不满足裸构建自含）。构建产线入口 `build.sh` 已串接（office/libs 缺失自动触发）。

**✅ Windows 首实测（2026-11-08，Win11 26200 x64 + Git Bash + Go 1.27.3 windows/amd64）**：纯 Go 构建通过（`CGO_ENABLED=0 go build` → 16.99MB/-s -w）；`go vet` 三平台（windows/linux/darwin）全过；`go test ./...` 全绿。**修复 7 个 Windows 特有潜伏 bug（mac 上全部无症状）**：
1. **tar 读侧句柄泄漏**（`core/tar.go` openTarStream）：底层流开了只关解压层不关本体——Windows 删被占文件失败暴露（TempDir 清理报「being used by another process」）；closeChain 链式关闭（解压层→底层流）。
2. **分卷创建「先删后关」**（`core/core.go` splitArchiveIntoVolumes）：`defer Close` 要到 return 后才执行，删原档时句柄还开着 → Windows 拒删；显式先 Close 再 Remove。教训同上——**Windows 上「关闭先于删除」是硬约束，defer 顺序不算数**。
3. **cli 手拼绝对路径**（`cli/list.go` absPath）：手写 `cwd+"/"+p` 且以「`/` 开头」判绝对——Windows 上 `C:/...` 盘符路径被当相对拼到 CWD 后；改用 `filepath.Abs`。教训：**Go 的 `path/filepath` 就是 os.path 等价物，手拼路径 = 平台 bug 温床**；条目名/glob 匹配这类「逻辑路径」场景用 `path`（恒 `/`），文件系统场景用 `filepath`。
4. **office COM 未钉线程**（`office/engine_windows.go`）：COM apartment 初始化是每 OS 线程的，goroutine 被调度器迁线程后报「尚未调用 CoInitialize」；withCOMSession：LockOSThread + CoInitializeEx + 配对 CoUninitialize（三路 COM 全包）。
5. **LibreOffice 隔离 profile 畸形 URI**（`office/engines.go`）：手拼 `file://C:\...`（反斜杠）→ profile 初始化失败（libpng Write Error / 弹「bootstrap.ini 已损坏」窗）；经 `url.URL{Scheme:"file", Path:...}` 构造（卷符路径补前导 `/` 成 `file:///C:/...`，自动转义）。
6. **glob 语义跨平台漂移**（`internal/cmdutil/glob.go` + `core/zpackignore.go`）：`filepath.Match` 的 `*` 按平台分隔符避让——Windows 上避开 `\` 不避开 `/`，`dist/*` 误匹配 `dist/sub/output.js`（.zpackignore/--exclude 语义漂移）；统一 `path.Match` + 入参 ToSlash。
7. **tar 符号链接无特权整体失败**：Windows 未开开发者模式时 `os.Symlink` 返回 ERROR_PRIVILEGE_NOT_HELD(1314) → 整包解压失败；降级为拷贝归档内目标成员（bsdtar 同款）+ 前向引用待办补拷 + stderr 日志；附带 `entryName` 收集时 ToSlash 归一（条目名恒 `/` 风格不变量，`core/common.go`）。

**office 三引擎 Windows 全通**（此前②全部盲写）：`--try-msoffice`（Word.Application COM）✓ / `--try-wps`（KWPS.Application，**首启冷启动 >180s 属预期，二次秒级**）✓ / `--try-libreoffice`（隔离 profile 修复后）✓——.doc→.docx 三引擎产物均正常；**Excel 族也已实测**（Excel.Application + KET.Application，.xls→.xlsx ✓，产物经 soffice 回读 csv 数据完整；顺手修复第 8 个 bug：外部引擎层 `target_format` 写死 "docx"——改按输出扩展名推导）。**门禁**：verify.sh 在 Git Bash 可跑（需 stat shim：`stat -f%z`→`stat -c%s` 转发脚本置 PATH 前；shasum/unzip Git Bash 自带）；修复后剩余 6 具名失败均为**脚本坐标系噪音**（python3/perl 以 `/tmp` 坐标写文件 vs MSYS 给 exe 的参数转成 `C:/Users/.../Temp`——两套坐标系错位；crypto 全链路与 hash --check 在 Windows 坐标下手动验证全正常），非产品 bug。verify-image.sh 需系统 ffmpeg 造夹具（实测机未装 → 不可跑；image 家族 convert/rotate/compress/merge/info 用 Go 标准库造夹具手动冒烟全过）。**平台差异备忘**：Git Bash 的 MSYS 参数路径自动转换（`/tmp/x`→`C:/Users/.../Temp/x`，`MSYS_NO_PATHCONV=1` 关闭）；`gofmt -l` 在 autocrlf checkout 下全量误报 CRLF；win exe 依赖仍仅系统出厂 DLL。

**仍未实测**：①office COM 的 PowerPoint.Application/KWPP.Application 路径（与 Word/Excel 同构、共用钉线程修复，逻辑未跑——实测机无 .ppt 夹具来源）；②macOS --try-msoffice AppleScript 路径（开发机无 Word）。

### Windows 完整版产线全记录（build_cli/windows，2026-11-09 首跑贯通；同日配料对齐 mac 定案）

**✅ 已全线跑通且与 mac 配料对齐**（编码器全家完整版 76MB：x264/x265/vpx8/9/aom/jxl/mp3lame/opus + ffmpeg 直调/media/audio/HDR/office 内置全开；import 表仅系统出厂 DLL；x264 与 x265 实转码、media info 解码全验证）。**架构定案：Windows 走本机原生 MinGW 产线（MSYS2/UCRT64 gcc），zig cc 交叉路线退役**。**零解压残留（2026-11-09 用户定案）**：与 mac 的常驻 `src/` 源码树不同——所有 clone/解压/构建树在 `mktemp` 临时目录、`trap` 退出即清（`KEEP_WORK=1` 保留调试现场）；仓库常驻仅三脚本 + prefix 缓存（.gitignore）；prefix 幂等（产物在则整块跳过，重跑零下载零解压）。

**脚本三件**（`bash build_cli/windows/build.sh` 一条命令全流程，对齐 mac/build.sh 形态；子命令 `deps`/`build` 可单跑）：`build.sh`（总入口：工具链环境→deps→office→cgo 组装→DLL 白名单校验）/ `deps_static.sh`（编码器全家 + FFmpeg n9.0 + fftools 直调库 → prefix，`FORCE=1` 全量重建）/ `office_static.sh`（office_oxide v0.1.16，MSYS2 rust windows-gnu staticlib）。云端同构产线 `.github/workflows/build-windows.yml`（windows runner + msys2 action + prefix 缓存 + artifact——**用户 push 后生效**）。前置一次性：`pacman -S --needed git make perl diffutils mingw-w64-ucrt-x86_64-{toolchain,cmake,nasm,pkgconf,libiconv,rust}`。

**配料表（对齐 mac，版本钉死）**：libjxl v0.11.1（SKCMS）/ libaom v3.11.0 / x264（videolan git master）/ x265（**Bitbucket master + x265Version.txt 版本文件法**，4.1 线）/ lame 3.100（--disable-frontend）/ opus v1.5.2 / libvpx v1.15.0（x86_64-win64-gcc target）/ FFmpeg **n9.0** 官方 tarball + office_oxide v0.1.16。ffmpeg configure 关键旗标：`--target-os=mingw32 --disable-shared --enable-static --disable-ffplay --disable-xlib --disable-libxcb --enable-libjxl --enable-libaom --enable-gpl --enable-libx264 --enable-libx265 --enable-libmp3lame --enable-libopus --enable-libvpx --enable-iconv --extra-libs=-liconv`。VideoToolbox 是 mac 专属硬编（Windows 对应 d3d11va/dxva2 硬解默认内置，无对应旗标）。GPL 义务同 mac。

**陷阱清单（全部实际踩过；改产线/调 Windows 集成前先读，编号接 mac 清单）**：
15. **`-Dmain=` 改名在 Windows 失效**：fftools/cmdutils.h 在 `_WIN32` 分支 `#undef main`（防 SDL 覆盖的历史代码）把宏取消——mac 产线手法失效。
16. **C main 符号入链 = `fatal error: randinit twice` + 段错误**：曾试「原名 main 编入 + 转发包装」——MinGW CRT（crt2.o）捕获 main 在初始化路径调用，与 Go runtime 再入冲突。**治本：`objcopy --redefine-sym main=zpack_ffmpeg_cli_main` 目标文件层改名**（既绕 #undef 又消除 C main）。
17. **ffmpeg 在 _WIN32 无视传入 argv**：cmdutils.c 的 `prepare_app_arguments` 用 `GetCommandLineW()` 重取——直调拿到的是**宿主 zpack.exe 的命令行**，`zpack ffmpeg` 的 "ffmpeg" 子命令名被当位置参数（输出名变 "ffmpeg"）。产线 sed 删除两处调用（补丁幂等放 prepare 主体，勿放解压分支——嵌套事故时会被跳过）。
18. **GNU ld 静态库严格按序**：libavcodec 引用 libiconv 须在其后（mac ld64 两遍扫描无此问题）——`-liconv` 注入 libavcodec.pc 的 Libs 行；configure `--extra-libs` 只进 Libs.private 不生效。
19. **pkg-config 把 `-Wl,*` 重排到行尾**：Bstatic 包裹手法对 .pc 无效——MSYS2 运行库（z/bz2/lzma/iconv/stdc++/pthread）一律替换为 **.a 绝对路径**（`gcc -print-file-name=libXXX.a` 给路径；勿用 `-print-libgcc-file-name`——那是 gcc 私有目录）。winpthread 特殊：静态 libstdc++.a 自身引用 pthread_*，解铃须在其后——各包 #cgo 段顺序无法保证，用 `go build -ldflags '-extldflags "-Wl,-Bstatic -lpthread -Wl,-Bdynamic"'` 钉到链接行末尾。
20. **Rust staticlib 不自带链接指令**：liboffice_oxide.a 的 Rust std 要消费者补 `-lntdll -luserenv -lws2_32 -lbcrypt -lcrypt32`（Nt*/GetUserProfileDirectoryW 等）；`-ldl` 是 unix 概念，MinGW 无此库——office 链接旗标拆 `linkflags_windows.go`/`linkflags_other.go`。
21. **GNU sed BRE 的 `\(..\|..\)` 组合在 MSYS2 失配**：`\([ "]\|$\)` 写法静默不替换——拆成朴素两条（`s| -lz | path |g` + `s| -lz$| path|`）。
22. **MSYS2 产线环境变量全靠手工**：`bash -lc` 不继承 LocalAppData/APPDATA（go 找不到 GOCACHE/GOENV）——显式 export；GOPROXY 同理（proxy.golang.org 不可达）。
23. **网络**：ffmpeg.org/GitHub 直连龟速（~60KB/s），走本机代理（`export https_proxy=http://127.0.0.1:7897`）16s 下完 12MB tarball；tuna/ustc 无 ffmpeg releases 镜像。
24. **目录被残留进程 CWD 占住删不掉**（Windows 特有，`Device or resource busy`）：rm 前确认无 shell 驻留；脚本兜底「内容并入 + dotfiles 单独 mv」（glob `*` 不含 dotfiles，rmdir 会因残留物失败）。
25. **pacman -Syu 会自杀**：升级 msys2-runtime/bash 时杀掉自身终端，后续安装静默丢失——先跑 -Syu（等它自杀）再单独跑 -S 包列表。
26. **prefix 缓存与补丁的耦合**：pc 后处理只在 make install 分支跑——补丁/后处理更新时 prefix 命中会跳过，须手动重放或清 prefix；cgo 侧 `-L` 目录里库文件内容变化不触发 go 重链（mac #7 同款），build 脚本恒 `go clean -cache`。
27. **x265 三连坑**（Windows 专属）：a) Git Bash 的 git 走 schannel 对 Bitbucket 握手失败——`git -c http.sslBackend=openssl`；b) Version.cmake 用 `git describe` 推版本 + MinGW 有 windres 才走 RC 版本块——depth-1 无 tag 推出空串 cmake 崩（`-D` 传值会被 git 推导覆盖）——**治本：clone 后 `rm -rf .git` + 手写 `x265Version.txt`**（Version.cmake 的 tarball 路径读显式版本）；c) 稳定 tag 3.6 的 `cmake_policy(SET CMP0054 OLD)` 被 MSYS2 cmake 4.x 拒（policy 已删除，`CMAKE_POLICY_VERSION_MINIMUM` 逃生阀管不到 SET）——用 master（已适配）。另：GitHub 的 videolan/x265 镜像停更 2020 不可用。
28. **lame 3.100 frontend 撞 gcc 14+**：隐式函数声明默认错误化——`--disable-frontend`（只要 libmp3lame，同 x264 --disable-cli 哲学）。
29. **libjxl 静态闭包**（mac #2 的 Windows 加重版）：brotli/hwy 互引顺序在多 pc 展开下失控——libjxl.pc 去 Requires、Libs 全闭包按依赖序重写；cmake 系对象后缀是 `.obj`（非 .o）。
30. **libjxl dllimport**（Windows 专属）：头文件默认 dllimport 声明 → 链接器找 `__imp_` 符号必挂——静态链须 `-DJXL_STATIC_DEFINE`，注入全部 jxl pc 的 Cflags（mac 无 dllimport 机制故未踩）。
31. **x265 新版 pc 闭包**：新版 cmake 已自装 x265.pc，但 C++ 运行时在 Libs.private 不被 `pkg-config --libs` 带出——无条件 sed 确保 Libs 行含 `-lstdc++ -lpthread`（mac 手写 pc 块已过时；mac clang 驱动自动链 C++ 故未踩）。
32. **jxl 全家物理合并**：GNU ld 按序 + pkg-config 展开顺序不可控 → jxl↔jxl_cms 跨库互引在 cgo 终链必挂——`ar x` 解包六个 .a（jxl/jxl_cms/hwy/brotli×3）合并为单一 `libjxl_merged.a`（nullglob：空 glob 会把字面 `*.o` 传给 ar）。
33. **ffmpeg 9 的 pc 生成器变化**：依赖闭包散装直写 Libs（8.x 走 Requires.private）+ cgo 展开重排——jxl 符号由 merged 兜底库紧跟 `-lav*` 注入三个 av pc（散装残留无害）。

**已知缺口**（vs brew 版，配料待补）：`drawtext` 滤镜（freetype+fribidi+harfbuzz）、`subtitles` 滤镜（libass，mac 走 CoreText 免 fontconfig）、`zscale` 滤镜（zimg）、`libsvtav1`（AV1 快编）。encoders 202 个已反超 brew（195）。

## 目录结构

```
core/                 ← 核心算法引擎（纯库，顶层产品面）：core.go（Compress/Extract API+Format+Logf）+ format.go（魔数嗅探/扩展名判定）+ common.go（收集/排除/安全判定/智能解压/进度）+ zip.go + tar.go
media/                ← 媒体功能包
build_cli/            ← 平台产线：mac/（ffmpeg_static.sh 全静态配料 + build.sh 组装——见「静态产线全记录」节）；windows/（build.sh 总入口 + deps_static.sh 编码器全家 + office_static.sh——见「Windows 完整版产线全记录」节，2026-11-09 贯通）；linux 待铺
cli/                  ← CLI 命令面（顶层产品面）：root.go（装配）+ commands.go（旗标解析校验 → 调 core → 输出契约呈现）
ui/                   ← UI 产品面（顶层）：聚合一切构建 UI 的产物——GUI 壳（gui/）+ 右键菜单（right-click/：macOS Finder/Windows Explorer/Linux 文件管理器）；全部是"启动器"，经 CLI 二进制契约干活（见 ui/doc.go 架构铁律）
cmd/zpack/            ← CLI 二进制入口（薄 main；未来并排 cmd/zpack-ui）
internal/             ← 纯管道：cmdutil（AppName + 旗标小件）+ output（输出契约层；cli 专用，core 禁用）
verify/verify.sh      ← 行为门禁（纯 Go）；verify-media.sh ← 媒体门禁（需完整版）
docs/                 ← 研究资料目录（本地私藏——逆向研究文档不入库）
```

**分层铁律（2026-11-02 重组确立）**：`core` 不 import cli/ui/output/cobra、不碰 stdout、不 os.Exit——错误一律 error 返回、过程日志经 `Logf` 回调注入（nil 静默）。`cli` 与 `ui` 是同一引擎的两张面孔：cli 函数直调 core；ui 一律经 CLI 二进制契约驱动（进程隔离、壳技术可换），需要进程内嵌时才直连 core。引擎内错误文案即 CLI 对外 error_info（改文案必须同步 verify.sh）。

## 路线图（优先级序，2026-11-02 修订）

1. **tar/tar.gz 全链**（进行中）：标准库 `archive/tar`+`compress/gzip` 零新依赖；写（compress 按扩展名）+ 读（extract 魔数：gzip `1F 8B` / ustar@257）+ CLI 扁平化改造一次做
2. ~~`extract` 通吃 7z/rar~~ **✅ 已交付（2026-11-02 批次 C）**：bodgit/sevenzip v1.6.5（7z 随机访问读取，**文件级并行解出**；**加密读取**（`NewReaderWithPassword`，`--password`））+ nwaples/rardecode v1.1.3（RAR 1.0-5.0 读取，自家多卷命名自动续卷；加密在读取时浮出并明示；v1.1.3 头无链接标记→符号链接按普通文件落地；**串行解出**——rar 引擎无并行机制惯例；**加密读取**（密码传入 `OpenReader`；部分加密档案预扫降级+逐文件密码错误跳过——混合密码已知限制））。夹具入库 `core/testdata/`（7z 中文路径/RAR4/RAR5/多卷）。遗留：无。~~7z 创建后置评估~~ **已定案（2026-11-03 用户拍板）：7z 只读不写**——写面恒为 zip + tar 系，与 rar 同为读侧格式。~~解出并行化~~ ✅ 已交付（2026-11-03：7z 文件级并行 + 全路径 file_count 语义统一为排除 skipped）
3. ~~读取硬化~~ **①加密读取已交付（2026-11-03）**：zip 加密读取全通（WinZip AES-128/192/256 AE-1/AE-2 + ZipCrypto——零第三方依赖，PBKDF2-HMAC-SHA1×1000/AES-CTR/10B HMAC 全自实现，`--password` 旗标）；7z 加密读取（`NewReaderWithPassword`）；rar 加密读取（密码传入 `OpenReader`）；部分加密降级（混合密码场景预扫降级+逐文件密码错误跳过）。遗留：`.z01` 分卷/v7 tar/sparse/fuzz 语料
4. `--exclude` 支持 glob（如 `*.bak` 模式）
5. `--codec zlib` 档（病态超重复语料比值敏感场景，zlib 逐块比 klauspost 省 ~23%）
6. **UI 面**（引擎稳定后，见 ui/doc.go）：GUI 壳（`ui/gui/`，壳技术另议）+ **右键菜单**（`ui/right-click/`：macOS Finder Services/FinderSync、Windows Explorer 注册表菜单、Linux .desktop/nautilus 脚本）——全部是"启动器"，经 stdout JSON 契约驱动 CLI 二进制；GUI 二进制届时新增 `cmd/zpack-ui`
7. ~~**剥离完成 SOP**~~ ✅ **已完成**：exkit 侧已删除 `zip` 命令与 verify 对应段、klauspost+x/text 依赖已退场、AGENTS.md/README 已指向 zpack（exkit 提交 04b8464，门禁 170→171）

## 环境事实

- 开发机（mac）：Go 1.27.3（brew，/opt/homebrew）
- Windows 实测机（2026-11-08 首测）：Win11 26200 x64，Git Bash（MSYS 路径自动转换需留意），Go 1.27.3（C:\Program Files\Go，PATH 不含——先 `export PATH="/c/Program Files/Go/bin:$PATH"`）；本机装齐 MS Office + WPS + LibreOffice（三引擎实测条件）；MSYS2/UCRT64 工具链（C:\msys64，产线用——gcc 16.2/rust/make/nasm/pkgconf，`/c/msys64/usr/bin/bash -lc` 进产线 shell 时需显式 export LOCALAPPDATA/APPDATA/GOPROXY）；系统代理 127.0.0.1:7897（Git Bash 的 curl/git 不自动走——外网下载手动 export）
- 依赖（go.mod，全纯 Go）：cobra（路由）、klauspost/compress（flate/gzip/zstd）、golang.org/x/text（GBK 解码）、ulikunitz/xz + pierrec/lz4/v4 + andybalholm/brotli（批次 B，+1.78MB mac/+2.46MB win）、bodgit/sevenzip + nwaples/rardecode（批次 C，+0.80MB mac/+0.83MB win）、kdomanski/iso9660（批次 D）、sorairolake/lzip-go + nradchenko/go-wim（批次 E-1）、kentalee/go-lzx（批次 E-2 cab）；win exe 始终仅 kernel32.dll
