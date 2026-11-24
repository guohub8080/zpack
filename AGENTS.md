# AGENTS.md — zpack 项目协作规范

任何 Agent 在本仓库工作前先读完本文。**本仓库从 exkit-cli 剥离**（剥离完成 SOP 已执行：exkit 侧 zip 命令/门禁段/依赖已全部移除，exkit 门禁 171/171 不含 zip 段）；契约与工程规范继承 exkit，算法行为对齐业界主流压缩器。

## 项目作用

- **压缩/解压专项 CLI**：单文件二进制（零运行环境依赖），Mac 上一条命令交叉编译出 Windows exe；stdout 唯一 JSON 结果 + stderr 日志的输出契约天然是 Agent/未来 GUI 的协议。
- 已交付：`compress` / `extract` 扁平命令（2026-11-02 定案）——**zip + tar/tar.gz 双格式**。zip 侧并行架构（条目级并行 + >16MB 文件 128KB 分块并行 + 魔数 Store 直通 + GBK 文件名修复 + 智能解压 + zip-slip 防护 + 有界内存耐久设计）；tar 侧 PAX 确定性写入 + 符号链接保留 + 魔数识别读取 + 穿越/链接逃逸/设备条目防护。7z/rar 读取为下一里程碑。
- **北极星（2026-11-02 用户定案；2026-11-22 修订）**：单引擎双面孔——同一二进制服务 CLI 与未来 GUI（GUI 是经输出契约调用的薄壳，引擎层零感知）。**读取** zip/rar/7z/tar/tar.gz 百分百（全变体：zip64/分卷/加密带密码/SFX/GBK/符号链接）；**写入** zip/7z/tar.gz（7z 于 2026-11-22 解禁——官方 7-Zip C++ 引擎进程内直调，见 specs/build.md；rar 永不写——法律禁止）；**不做私有格式**（互操作即护城河）。
- **已决策：命令扁平化**（2026-11-02 用户拍板）：`zpack compress/extract <任意输入>`——写格式由 `--output` 扩展名定（.zip/.tar/.tar.gz/.tgz），读格式靠魔数识别；旧 `zpack zip …` 形态退役。
- **已决策：格式策略**（2026-11-02 定案，同日修订）：读取格式**尽可能多**，判定标准是**开源实现/公开规格的可得性**而非格式出身——①公开格式（规格公开）尽量做；②私有格式但有开源实现 → 纳入（alz=unalz BSD、pea=公开规格）；③只有 GPL 实现 → 直接用（daa/isz=daa2iso/isz2iso）——2026-11-24 修订：项目自身按 GPL 发布，许可证不是取舍依据（铁律 2），不再要求 clean-room 重写；④无实现无规格（egg/j2j/k2k/bam）或安全污点格式（ace，CVE-2018-20250）→ 出局；rar 为专有出身例外（事实标准 + nwaples/rardecode MIT）。zpaq 实为开放标准（规格公开 + 参考实现公有领域），归公开格式。批次：A=bz2+zstd+单流gz+.001分卷（✅ 已交付）→ B=xz+lz4+brotli → C=7z+rar → D=iso+asar+uue+z+ar → E=cab/wim(LZX)/arj/lzh(+bh 捆绑评估)/lzip/nsis/zpaq → F=alz→pea→daa/isz（clean-room）。

## 铁律（继承 exkit）

1. **验收全绿才算实现完**：每个命令/行为必须在 `verify/verify.sh` 有用例（正常路径 + 退化输入 + 退出码），全绿才算完成。
2. **依赖准入（2026-11-08 用户放宽定案）：静态优先 + 预算 ≤500MB**：产物单文件自包含（裸机可跑）是硬目标——静态链接优先于动态；体积预算 500MB 内随意（当前实测静态全量约 130MB）。零新增**动态**链接仍有效（`otool -L` 不得挂 brew/系统外路径；Windows import 表仍仅出厂 DLL）。纯 Go 依赖照旧自由引入。**许可证不是硬约束**（2026-11-24 用户定案：「我的开源是可以改协议的，不要因为许可证把自己拴死」）：选库、选实现时不因 GPL 排除候选，只需如实说明「用它之后项目要带什么协议」，由用户决定。项目自身按 **GPL-2.0-or-later** 发布（根目录 `LICENSE`；两套产线都链了 x264 / x265 并开了 FFmpeg 的 `--enable-gpl`，整个产物按 GPL 看，义务明细见 specs/build.md 第 6.7 节）。**AGPL 部分**（2026-11-24 用户拍板）：`pdf render` 用 MuPDF（go-fitz 绑定携带 MuPDF 源码，**AGPL**）——MuPDF 涉及的部分按 AGPL 承担义务（发布含 render 的产物需提供对应源码），定案见 specs/pdf.md 6.12。与项目协议无关、仍然有效的只有第三方自己的专有限制——例如 unRAR 不得用于做 RAR 压缩（所以 rar 仍然永不写）。
3. **命名规范**：变量/函数全语义命名，禁单字母（循环下标 `idx` 例外）；常量 SCREAMING_SNAKE_CASE + 行上注释；固定字面量引用 `cmdutil.AppName`。
4. **Git**：主分支 `main`；Conventional Commits；**完成工作单元即主动本地提交**（不必等用户发话）；**绝对禁止 push**，仅本地提交。
5. **刻盘这件事三个平台分别实现**（2026-11-24 用户定案）：Windows = 系统自带 IMAPI2，Linux = xorriso（libburnia），macOS = 系统自带 hdiutil——各用该平台上最简单、最可靠的做法，不强求同一个库，不为统一引擎做跨平台移植；对用户始终是同一个 `zpack burn`（旗标与输出契约一致）。明细、两次绕路的教训与真机验证记录见 specs/burn.md。
6. **spec 是行为的权威说明**（2026-11-24 用户定案：按功能整理项目，文档和代码都按功能归类）：每个功能一份 spec，放在 `specs/`（目录与流程见 [specs/README.md](specs/README.md)）。改行为先改 spec，再让 agent 按小节同步代码、补验收用例、把提交号回填到 spec 的变更记录；新功能先按 `specs/_template.md` 立 spec。本文只留全局规范，功能细节一律在 spec 里、不在这里重复。
7. **失败时不得删用户的文件**（2026-11-24 实测教训）：命令失败后的清理只能删本次新建的输出（`internal/cliutil` 的 `OutputGuard`）；引擎写输出时如果输出就是输入，必须先写旁边的临时文件、成功后再替换（`media/inplace.go`）——`zpack media compress x.mp4` 的缺省输出就是输入自己，曾把 10MB 的源文件毁成 1297 字节还报成功；`audio compress` 与 image / pdf 命令的失败清理同病。
8. **批量重命名之后扫一遍字符串**（2026-11-24 实测教训）：用 sed 批量替换包限定符（如 `core.` → `engine.`）会误伤字符串字面量里的同形文本（用法提示里的 `[3.pdf...]` 变成了 `[3.engine...]`）；改完用 go/ast 把字符串和注释里的新名字扫一遍再提交。

## 输出契约（所有命令无例外）

### 核心原则

- **stdout 有且只有一个最终输出**（`internal/stdout` 输出管线约束层的 print 函数输出后立即 `os.Exit`，编码先进内存、失败路径 stdout 零字节——绝不半截 JSON）：调用方读完 stdout 即得到完整结果，无需解析多次。
- **print 即 exit，外层 defer 不运行**：任何清理（临时目录/半成品）必须在闭包内 defer（闭包 return 时执行）或 print 前显式调用——convert 的临时目录曾在成功路径静默泄漏即此因（2026-11-04 修复，清理行为已入验收）。
- **子命令 `--help`/`-h` 恒可用**：DisableFlagParsing 会吞掉 cobra 的自动 help，全部命令 Run 统一经 internal/cliutil 的 `HelpAwareRun` 守卫（2026-11-04 修复——此前 `zpack compress --help` 报参数错误，agent 嗅探在此断裂）；主力命令带 Long 示例（compress/extract）。
- **stderr 是过程日志**（进度/警告/跳过明细），可被整体忽略（`2>/dev/null`），不影响结果判断。
- **退出码**：成功 `0` / 失败 `1`（无中间状态）。
- **is_success 与退出码的关系恒定**：exit 0 ⇔ `is_success:true`——刻意的双通道冗余（管道会吃掉 `$?`、stdout 是唯一穿越进程边界的信道）。**唯一例外**：判定类命令（test/hash --expect）「操作完成但结论为否」时，JSON 永远带完整判定字段（`is_success:false`/`is_match:false` + 实算值），退出码跟随结论；消费方规则——脚本可用退出码，但**管道/存档/GUI 场景只信 JSON 字段**。
- 数值参数严格校验（非法/越界 → error JSON + exit 1）；未知 flag 静默忽略；旗标一律长形态 `--word`；路径完整支持空格/中文（argv 直传，禁止拼 shell 字符串）。
- 平台不支持的功能分支必须 `ErrorPrint`「暂不支持 <平台>：<原因>」+ exit 1，禁止静默失败。

逐命令的 stdout 字段、旗标、行为细则与设计取舍都在 `specs/` 对应功能的 spec 里；stderr 日志规则、`--quiet`、调用方消费指南在 [specs/conventions.md](specs/conventions.md)。本文不再重复。

## 命令规格

命令保持扁平（2026-11-02 定案）：`zpack compress/extract <任意输入>`——写格式由 `--output` 扩展名定，读格式靠魔数识别；无格式子命令。各功能的规格：

| 功能 | spec | 功能 | spec |
|---|---|---|---|
| 压缩包（compress / extract / convert / list / test / info / bench） | [specs/archive.md](specs/archive.md) | 刻录（burn） | [specs/burn.md](specs/burn.md) |
| 加密与校验（crypto / hash） | [specs/crypto.md](specs/crypto.md) | 图片（image） | [specs/image.md](specs/image.md) |
| 视频（media / ffmpeg） | [specs/media.md](specs/media.md) | 音频（audio） | [specs/audio.md](specs/audio.md) |
| 文档转换（office） | [specs/office.md](specs/office.md) | PDF（pdf） | [specs/pdf.md](specs/pdf.md) |
| 零依赖消费层（base64 / json / skill） | [specs/tools.md](specs/tools.md) | GUI 与右键菜单 | [specs/gui.md](specs/gui.md) |

## 构建与验收

```bash
CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)-dev" -o bin/zpack-dev ./cmd/zpack   # 开发期纯 Go 构建（不是交付产物，见下方「产物定案」）
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o bin/zpack-dev.exe ./cmd/zpack  # 交叉编译（mac 上验证 Windows 可编译；别用完整版的文件名 bin/zpack-cli.exe）

go test ./...          # 单测（表驱动；纯逻辑函数才测，run* 函数直接 os.Exit 不测）
go vet ./...           # + GOOS=windows / GOOS=linux go vet ./... 三平台编译验证
bash build_cli/windows/build.sh   # Windows 完整版产线：deps（prefix 幂等缓存）→ office → cgo 组装 → DLL 白名单校验；在 MSYS2 bash 里跑
bash build_cli/mac/build.sh   # mac 完整版产线：自动触发 ffmpeg_static.sh（首次源码级全静态 ~25 分钟，prefix 幂等）→ 静态链接 go build（ffmpeg CLI 对象进程内直调，~48MB 单文件）→ 校验零 brew 动态依赖
go run ./scripts/genenv       # realEnvTest 真实体感数据一键生成（≈3.4GB 算法生成可复现；--fast/--full/--clean；见 scripts/genenv/README.md——门禁测确定性小样本，realEnvTest 测进度条/媒体/加密分卷的真实体感；目录 gitignore 不入库）
bash verify/verify.sh        # 行为门禁：只测 bin/zpack-cli（恒为完整版），确定性输出全字节比对（用例数以脚本为准）
bash verify/verify-media.sh  # 媒体门禁：需产线完整版 bin/zpack-cli（128 用例：转码矩阵/压缩公式/几何变换/bake/HDR/EXIF/audio/ffmpeg 直调），夹具用系统 ffmpeg 生成
bash verify/verify-image.sh  # 图片门禁：纯 Go 即可（65 用例：格式互转/物理尺寸/变换 ffmpeg 像素级交叉/merge/EXIF 定向）
```

### 双层开发节奏（cgo 早已按 build tag 分层——纯功能开发用纯循环）

> **产物定案（2026-11-22 用户拍板：无「纯 Go 心结」，一切以功能好用为基础）**：
> `bin/zpack-cli` 与装机产物**恒为完整版**（`build_cli` 产线 CGO 构建——media/audio/HDR/HEIC/ffmpeg 直调全可用）；
>
> **引擎的可执行文件叫 `zpack-cli`**（2026-11-24 用户定案改名：GUI 自己叫 `zpack`，引擎叫 `zpack-cli`，两边不再同名——GUI 的「同目录」候选不再等于自己）。产线出 `bin/zpack-cli[.exe]`，旧名产物各脚本与 GUI 查找都还认；开发期快速构建仍是 `bin/zpack-dev`。文档示例里写作 `zpack …` 的命令指的就是它。给 Windows / Linux 打 GUI 包之前先 `bash ui/gui/inject-engine.sh`，引擎才会随安装包分发（specs/gui.md 7.4）。
> 纯 Go 构建只是**开发期快速编译循环**的工具，不得作为交付/装机产物（纯 Go 下 media 族报「需完整版」——用户会当场撞墙）。
> 每轮改动收尾时若动过引擎代码：跑产线重铸 bin/zpack-cli + 刷新两装机位——**用 `ui/right-click/macos/refresh-engine.sh`**（拷进 /Applications/zpack.app 与 zpack Finder Menu.app 各自 Resources/zpack-cli 并重签）。**禁止手工 cp**：两个包都是已签名的，原地换文件封印即失效；Finder 宿主封印一坏系统就弃用真身、右键退回别处注册的旧副本（2026-11-23「右键图标又没了」即此因）。

**纯 Go 循环（开发期快速编译——pdf/hash/压缩/图片标准格式等纯功能的日常迭代）**：
```bash
CGO_ENABLED=0 go build -o bin/zpack-dev ./cmd/zpack   # 热缓存 ~1s / 冷 ~10s
CGO_ENABLED=0 go test ./...                            # ~4s
bin/zpack-dev <命令>                                   # media/HDR/HEIC/ffmpeg 走 stub 报「需完整版」
```
**完整循环（media/audio/HDR/ffmpeg 直调开发时）**：`export PKG_CONFIG_PATH=build_cli/mac/prefix/lib/pkgconfig` 后 cgo 构建即可（`internal/ffmpegcli/libs/` 已包内自含，无需产线环境变量）；发布产物走 `build_cli/mac/build.sh`。
**新功能家族的落位惯例**（如 pdf）：顶层包 `pdf/`（纯 Go，同 `image/` 惯例：引擎纯函数+文件，CLI 面 `cli/<功能>/` 子包（引擎以别名 engine 引入；跨功能小件放 `internal/cliutil`；子包一份 doc.go 指向对应 spec），**门禁进 `verify/verify.sh`**——纯构建可跑，反馈秒级）；仅当真的要碰 ffmpeg/libav 才进 cgo 层。**注意**：`build_cli/mac/build.sh` 会 `go clean -cache`——跑完产线后 cgo 冷构建要等一会，属预期。

产线、许可证、验收在各平台的细节（两套完整版产线的流程、配料表、configure 旗标、陷阱清单 #1–#38，依赖与许可证义务，三个验收脚本的环境要求与已知噪音）全部在 [specs/build.md](specs/build.md)——**改产线、调 ffmpeg / office / 7z 集成、跑 Windows 完整版之前先读它的陷阱清单**。这里只留两条最容易被绕过的定案：

- **ffmpeg CLI 进程内直调、零释放**：`zpack ffmpeg` 是把 ffmpeg 的 main 改名后静态链进本体的普通 C 函数调用——运行时不往磁盘释放任何可执行文件、不起子进程（运行时释放可执行文件 = 污染磁盘 + 杀毒误报 + 并发冲突 + 慢，已被用户否决）。
- **刻录三个平台各用各的后端**（铁律 5）：介质状态词表、写入前的拒绝规则、光盘名称规则、真机验证记录与未结项见 [specs/burn.md](specs/burn.md)；GUI 刻录页的对应规则见 [specs/gui.md](specs/gui.md)。**任何异常介质状态都不得走到写入。**

## 目录结构

```
core/                 ← 压缩包引擎（纯库，顶层产品面；名字是历史遗留，实际只管压缩/解压）：core.go（Compress/Extract API+Logf）+ format.go（魔数嗅探/扩展名判定）+ common.go（收集/排除/安全判定/智能解压/进度）+ zip.go / tar.go / 单流格式 / 分卷 / 7z / rar 读取 …
image/ media/ office/ pdf/   ← 各功能的引擎（纯库）：图片 / 视频+音频 / 文档转换 / PDF
cli/                  ← CLI 命令面（顶层产品面），按功能分子包：root.go（装配）+ archive/ burn/ crypto/ image/ media/ office/ pdf/ tools/（每个子包一份 doc.go）
ui/                   ← UI 产品面（顶层）：聚合一切构建 UI 的产物——GUI 壳（gui/）+ 右键菜单（right-click/：macOS Finder/Windows Explorer/Linux 文件管理器）；全部是"启动器"，经 CLI 二进制契约干活（见 ui/doc.go 架构铁律）
cmd/zpack/            ← CLI 二进制入口（薄 main；未来并排 cmd/zpack-ui）
internal/             ← 纯管道：cmdutil（AppName + 旗标小件）+ stdout（输出契约层；cli 专用，core 禁用）+ cliutil（命令面公共底座：help 守卫 / 位置参数 / 日志去向）+ 引擎进程内直调层（ffmpegcli / sevenzipcli）+ imapi2（Windows 刻录引擎胶水——按方法名调系统自带 IMAPI2）
specs/                ← 各功能的规格（行为与设计的权威说明；README.md 是入口与「改 spec → 同步代码」流程）
verify/verify.sh      ← 行为门禁；verify-media.sh ← 媒体门禁（需完整版）；verify-image.sh ← 图片门禁
build_cli/            ← 平台产线：mac/ windows/（全记录与陷阱清单见 specs/build.md）；linux 待铺
docs/                 ← 研究资料目录（本地私藏——逆向研究文档不入库）
```

**分层铁律（2026-11-02 重组确立）**：`core` 不 import cli/ui/output/cobra、不碰 stdout、不 os.Exit——错误一律 error 返回、过程日志经 `Logf` 回调注入（nil 静默）。`cli` 与 `ui` 是同一引擎的两张面孔：cli 函数直调 core；ui 一律经 CLI 二进制契约驱动（进程隔离、壳技术可换），需要进程内嵌时才直连 core。引擎内错误文案即 CLI 对外 error_info（改文案必须同步 verify.sh）。

## 路线图（优先级序，2026-11-02 修订）

1. **tar/tar.gz 全链**（进行中）：标准库 `archive/tar`+`compress/gzip` 零新依赖；写（compress 按扩展名）+ 读（extract 魔数：gzip `1F 8B` / ustar@257）+ CLI 扁平化改造一次做
2. ~~`extract` 通吃 7z/rar~~ **✅ 已交付（2026-11-02 批次 C）**：bodgit/sevenzip v1.6.5（7z 随机访问读取，**文件级并行解出**；**加密读取**（`NewReaderWithPassword`，`--password`））+ nwaples/rardecode v1.1.3（RAR 1.0-5.0 读取，自家多卷命名自动续卷；加密在读取时浮出并明示；v1.1.3 头无链接标记→符号链接按普通文件落地；**串行解出**——rar 引擎无并行机制惯例；**加密读取**（密码传入 `OpenReader`；部分加密档案预扫降级+逐文件密码错误跳过——混合密码已知限制））。夹具入库 `core/testdata/`（7z 中文路径/RAR4/RAR5/多卷）。遗留：无。~~7z 只读不写（2026-11-03 旧定案）~~ **已解禁（2026-11-22 用户拍板）：7z 全量接入官方 C++**（7-Zip 25.01 Alone2 进程内直调，见 specs/build.md）；rar 仍永不写（法律）。~~解出并行化~~ ✅ 已交付（2026-11-03：7z 文件级并行 + 全路径 file_count 语义统一为排除 skipped）
3. ~~读取硬化~~ **①加密读取已交付（2026-11-03）**：zip 加密读取全通（WinZip AES-128/192/256 AE-1/AE-2 + ZipCrypto——零第三方依赖，PBKDF2-HMAC-SHA1×1000/AES-CTR/10B HMAC 全自实现，`--password` 旗标）；7z 加密读取（`NewReaderWithPassword`）；rar 加密读取（密码传入 `OpenReader`）；部分加密降级（混合密码场景预扫降级+逐文件密码错误跳过）。遗留：`.z01` 分卷/v7 tar/sparse/fuzz 语料
4. `--exclude` 支持 glob（如 `*.bak` 模式）
5. `--codec zlib` 档（病态超重复语料比值敏感场景，zlib 逐块比 klauspost 省 ~23%）
6. **UI 面**（引擎稳定后，见 ui/doc.go）：GUI 壳（`ui/gui/`，壳技术另议——asuka/mygo/nsgo 调研完毕待原型验证）+ **右键菜单 mac 侧已交付**（2026-11-19，见下）+ Windows Explorer/Linux 待铺——全部是"启动器"，经 stdout JSON 契约驱动 CLI 二进制；GUI 二进制届时新增 `cmd/zpack-ui`
   GUI 交互铁律（所有可点元素的反馈规则、字体与主题定案）、FinderSync 通信架构、右键菜单的签名与安装陷阱、组件先查再造——全部在 [specs/gui.md](specs/gui.md)，**动 `ui/` 之前必读**。
7. ~~**剥离完成 SOP**~~ ✅ **已完成**：exkit 侧已删除 `zip` 命令与 verify 对应段、klauspost+x/text 依赖已退场、AGENTS.md/README 已指向 zpack（exkit 提交 04b8464，门禁 170→171）

## 环境事实

- 开发机（mac）：Go 1.27.3（brew，/opt/homebrew）
- Windows 实测机（2026-11-08 首测）：Win11 26200 x64，Git Bash（MSYS 路径自动转换需留意），Go 1.27.3（C:\Program Files\Go，PATH 不含——先 `export PATH="/c/Program Files/Go/bin:$PATH"`）；本机装齐 MS Office + WPS + LibreOffice（三引擎实测条件）；MSYS2/UCRT64 工具链（C:\msys64，产线用——gcc 16.2/rust/make/nasm/pkgconf，`/c/msys64/usr/bin/bash -lc` 进产线 shell 时需显式 export LOCALAPPDATA/APPDATA/GOPROXY）；系统代理 127.0.0.1:7897（Git Bash 的 curl/git 不自动走——外网下载手动 export）
- Windows 实测机二（2026-11-24）：Win10 22H2 x64，用户目录 `C:\Users\zhc`，仓库 `E:\code_files\zpack`，Git Bash，Go 1.27.3（PATH 含）；**内置**光驱 HL-DT-ST DVD-RAM GHC0N（MA02，SATA，盘符 F:）；`proxy.golang.org` 不可达（命令带 `GOPROXY=https://goproxy.cn,direct`）、GitHub 直连快、`*.googlesource.com` 不可达、本机未配代理。完整版产线首跑 09:30–13:25 全线贯通（MSYS2 20260927 经 winget；UCRT64 gcc 16.2.0-7 / rust 1.100.0-2 / cmake 4.5.1 / nasm 3.02；deps 8 步 1h43m，其中 FFmpeg 57min），产物 83MB、32 个导入 DLL 全为系统出厂。本机专属坑：①裸 `winget` 从 agent shell 起不来（Permission denied）——用完整别名路径 `C:\Users\zhc\AppData\Local\Microsoft\WindowsApps\winget.exe` 从 PowerShell 调；②`mirror.msys2.org` 返回 503——mirrorlist 前置国内镜像（原文件备份在 `%TEMP%\zpack-fullbuild\msys2-backup\`）；③**Watt Toolkit（Steam++）把 github.com 指到 127.0.0.1 并用自签根重签 TLS**——schannel 客户端信任，MSYS2 的 git/curl（仅 OpenSSL）报 `unable to get local issuer certificate (20)`，须经 `GIT_SSL_CAINFO` / `CURL_CA_BUNDLE` / `SSL_CERT_FILE` 传「系统 CA + 该根」的 bundle；④libaom 与 libjxl 的 skcms 子模块在 googlesource——用 `GIT_CONFIG_COUNT` + `url.insteadOf` 改走 `github.com/m-ab-s/aom`（v3.11.0 = d6f30ae4…，与 Homebrew 公式记录的提交一致）和 `github.com/google/skcms`（gitlink 42030a77…）；⑤cargo 忽略 `HOME`、只认 `%USERPROFILE%`——不设 `CARGO_HOME` 会在用户目录生成 `.cargo`（51MB）；⑥进程创建约 100ms（装着腾讯电脑管家），构建比云端慢约 2.5 倍。完整环境见 `%TEMP%\zpack-fullbuild\scripts\job-build.sh`。
- 依赖（go.mod，纯 Go 为主；`pdf render` 是唯一 cgo 深度依赖——MuPDF AGPL 见铁律 2）：cobra（路由）、klauspost/compress（flate/gzip/zstd）、golang.org/x/text（GBK 解码）、ulikunitz/xz + pierrec/lz4/v4 + andybalholm/brotli（批次 B，+1.78MB mac/+2.46MB win）、bodgit/sevenzip + nwaples/rardecode（批次 C，+0.80MB mac/+0.83MB win）、kdomanski/iso9660（批次 D）、sorairolake/lzip-go + nradchenko/go-wim（批次 E-1）、kentalee/go-lzx（批次 E-2 cab）、**gen2brain/go-fitz（PDF 渲染为图片，MuPDF AGPL——pdf render；分层 stub 纯构建不编）**

## 文档沿革

2026-11-24 之前本文约 115 KB，逐命令的输出表、命令规格、两套产线全记录、刻录后端全记录、GUI 交互铁律都写在里面。按功能整理后这些内容归位到 `specs/`；要看整理前的原文，见提交 50604b7 的 `AGENTS.md`（`git show 50604b7:AGENTS.md`）。
