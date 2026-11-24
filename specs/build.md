# 构建与验收（build）

> zpack 怎么从源码变成可交付的单文件二进制（三种构建形态、mac 与 Windows 两套完整版产线），以及怎么判断一次改动「做完了」（go test / go vet 三平台、三个验收脚本）。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是**构建产线与验收门禁的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以脚本和代码为准核对过；拿不准的标了「【待确认】」。本次核对在 Windows 机器上做，只读脚本与文档，**没有运行任何产线脚本和验收脚本**——用例数是按脚本静态数出来的，实际跑出来的数字以首次实跑为准。

## 1. 功能定义

**做什么。**

1. **定义三种构建形态**，以及每种形态能做什么、不能做什么（能力矩阵见第 3.2 节）：
   - 纯 Go 开发构建（`CGO_ENABLED=0`）——开发期快速编译循环用的工具；
   - 交叉编译出来的 Windows exe（同样是纯 Go）；
   - 完整版产线（mac 与 Windows 两套）——把 ffmpeg 全家、office_oxide、7z 直调库从源码静态链进**一个**可执行文件，并断言没有非系统出厂的动态依赖。
2. **规定哪种产物可以交付**：`bin/zpack-cli`、`bin/zpack-cli.exe` 与两个 mac 装机位里的引擎恒为完整版；纯 Go 构建只是开发期工具，不得作为交付或装机产物（AGENTS.md:188-190，2026-11-22 用户定案）。
3. **规定一次改动怎样才算做完**：`go test ./...`、三平台 `go vet`、三个验收脚本全绿（铁律 1，AGENTS.md:15）。
4. **生成真实体感语料**（`scripts/genenv`）：门禁测的是确定性小样本，genenv 生成几 GB 的数据，用来看进度条、真实媒体转换、加密分卷。
5. **mac 装机位刷新**（`ui/right-click/macos/refresh-engine.sh`）：动过引擎之后，把新引擎换进两个已签名的 .app 并重签。

**不做什么。**

- 不定义各命令的行为（在各功能的 spec 里），不定义输出契约（见 [conventions.md](conventions.md)）。
- 不做 Linux 完整版产线：`build_cli/linux/` 下只有两个占位脚本（`build_cli/linux/build.sh:6` 只打印「产线待首跑验证」）。
- 不在 Windows 上铸 7z 写入引擎：7z 静态库只有 mac 产线在铸，所以 Windows 完整版里 `compress --output x.7z` 仍然报「需完整版」（见第 3.2 节）。
- 不做发布：agent 只做本地提交，绝对禁止 push（AGENTS.md:18）；云端产线要等用户 push 才会触发。
- GUI 与右键菜单本体的构建（`build-xcode.sh`、`quickaction.sh`、`ui/gui/install.sh`）归 [gui.md](gui.md)；这里只管它们用到的引擎二进制和 `refresh-engine.sh`。

**会在什么场景下用它。**

- 日常迭代纯 Go 功能（pdf、hash、压缩、图片标准格式……）：走纯 Go 循环，秒级反馈。
- 动了 cgo 层或引擎代码之后：重跑产线重铸 `bin/zpack-cli`；在 mac 上再跑 `refresh-engine.sh`。
- 升级配料版本、改编译旗标、改产线补丁：动手前先读第 6 节的陷阱清单。
- 换了机器：从零重建 prefix。
- 拿到一个二进制，想知道它是不是完整版（第 3.3 节）。
- 门禁红了，想知道是产品问题还是环境噪音（第 4.6 节）。

## 2. 构建命令与验收命令

### 2.1 构建命令

命令一律原样给出。mac 产线在 mac 上跑；Windows 产线在 MSYS2 的 bash 里跑（脚本自己把 `/ucrt64/bin` 加进 PATH，`build_cli/windows/build.sh:21`；云端用的是 `shell: msys2 {0}`，`.github/workflows/build-windows.yml:68`）。

| 形态 | 命令 | 产物 | 版本串 | 用途与限制 | 出处 |
|---|---|---|---|---|---|
| 纯 Go 开发构建 | `CGO_ENABLED=0 go build -o bin/zpack-dev ./cmd/zpack` | `bin/zpack-dev` | `dev`（没注入版本号） | **开发期快速编译循环**（热缓存约 1 秒、冷启动约 10 秒）。media / audio / HDR / HEIC / ffmpeg 代理 / office 内置引擎 / 7z 写入都报「需完整版」。**不得交付、不得装机** | AGENTS.md:193-198 |
| 交叉编译 Windows exe | `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o bin/zpack-cli.exe ./cmd/zpack` | `bin/zpack-cli.exe` | `dev` | 纯 Go；AGENTS.md 只标了「交叉编译」，没有说明用途。注意产物与 Windows 完整版**同名同路径**（见第 9 节） | AGENTS.md:175 |
| 带版本号的通用构建 | `go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)" -o bin/zpack-cli ./cmd/zpack` | `bin/zpack-cli` | `<短哈希>` | AGENTS.md 列的第一条。命令本身不指定 CGO 开关，产物形态跟着环境的缺省值走（见第 9 节） | AGENTS.md:174 |
| mac 完整版 | `bash build_cli/mac/build.sh` | `bin/zpack-cli` | `<短哈希>` | **交付与装机产物**。缺产物时会自动触发三个配料脚本 | AGENTS.md:179；`build_cli/mac/build.sh:9-16` |
| Windows 完整版 | `bash build_cli/windows/build.sh` | `bin/zpack-cli.exe` | `<短哈希>-full` | **交付与装机产物**。等价于 `bash build_cli/windows/build.sh all` | `build_cli/windows/build.sh:19,65-66` |
| 完整循环（开发期 cgo 构建） | 先 `export PKG_CONFIG_PATH=build_cli/mac/prefix/lib/pkgconfig`，再 cgo 构建 | — | — | media / audio / HDR / ffmpeg 直调开发时用。AGENTS.md 没给出完整的 go build 命令，可以照 `build_cli/mac/build.sh:14-16` 的三步：export → `go clean -cache` → `CGO_ENABLED=1 go build …` | AGENTS.md:199 |

版本号由 `-X main.version` 注入（`cmd/zpack/main.go:15`，缺省 `dev`）。只有 Windows 完整版给版本串加了 `-full` 后缀（`build_cli/windows/build.sh:65`）。

### 2.2 测试与验收命令

| 命令 | 作用 | 需要什么 | 出处 |
|---|---|---|---|
| `go test ./...` | 单测（表驱动；只测纯逻辑函数，`run*` 函数直接 `os.Exit` 所以不测） | Go 工具链。纯 Go 构建和 cgo 构建都能跑；缺 7z 引擎时相关用例自动跳过 | AGENTS.md:177；`core/sevenz_test.go:15-17` |
| `go vet ./...`，再加 `GOOS=windows go vet ./...`、`GOOS=linux go vet ./...` | 三平台编译验证 | 同上 | AGENTS.md:178 |
| `bash verify/verify.sh` | 行为门禁 | `bin/zpack-cli`（应当是完整版）；外部命令 python3、perl、unzip、shasum、tar、gzip、bzip2，以及 BSD 形态的 `stat -f%z` | AGENTS.md:181；`verify/verify.sh:12` |
| `bash verify/verify-media.sh` | 媒体门禁 | 完整版 `bin/zpack-cli`；系统自带的 `ffmpeg` / `ffprobe`（造夹具、校验产物）；office 段用到 macOS 的 `textutil` | `verify/verify-media.sh:2-3,22-31` |
| `bash verify/verify-image.sh` | 图片门禁 | `bin/zpack-cli`（纯 Go 即可，image 零 cgo）；系统 `ffmpeg` / `ffprobe` 造夹具；BSD 形态的 `stat -f%z` 和 `md5 -q` | `verify/verify-image.sh:2,12-14`、`:106-107` |
| `go run ./scripts/genenv` | 生成 realEnvTest 语料 | 必须在仓库根目录跑（目录名还得是 `zpack`）；媒体族要完整版 `bin/zpack-cli` | `scripts/genenv/main.go:43-47` |
| `ZPACK_IMAPI2_LIVE_TEST=1 go test ./internal/imapi2 -run TestLiveReadOnly` | 刻录真机只读回归（不写盘） | 光驱里有内容的盘 | AGENTS.md:277 |
| `bash ui/right-click/macos/refresh-engine.sh` | 把引擎换进两个 mac 装机位并重签 | 完整版 `bin/zpack-cli`；重签 Finder 宿主要 Apple Development 证书 | `ui/right-click/macos/refresh-engine.sh:11-16,25,35` |

三个验收脚本都写死了 `bin/zpack-cli`（不带 `.exe`）。2026-11-24 在 Windows 10 的 Git Bash 上 `verify/verify.sh` 能跑起来（见第 4.6 节），说明 Git Bash 能把它解析到 `bin/zpack-cli.exe`。

### 2.3 产线脚本清单

| 脚本 | 职责 | 子命令 | 环境变量开关 |
|---|---|---|---|
| `build_cli/mac/build.sh` | mac 总入口：缺产物时依次触发下面三个脚本 → `go clean -cache` → cgo 构建 → `otool -L` 断言 | 无 | 无（`PKG_CONFIG_PATH` 由脚本自己 export，`:14`） |
| `build_cli/mac/ffmpeg_static.sh` | mac：编码器全家 + FFmpeg n9.0 + ffmpeg 直调库 → `build_cli/mac/prefix/` | 无 | `FORCE=1` 全量重建（先删 prefix，`:13`） |
| `build_cli/mac/office_static.sh` | mac：office_oxide v0.1.16 静态库 → `office/libs/`（同时拷一份进 `prefix/lib`） | 无 | 无；已编出的库存在就跳过编译（`:21`） |
| `build_cli/mac/sevenzip_static.sh` | mac：7-Zip 25.01 直调库 → `internal/sevenzipcli/libs/libzpack_7zcli.a` | 无 | `FORCE=1` 重建（`:29`） |
| `build_cli/mac/prepare.sh` | POC 阶段遗留：装 brew 的 pkgconf / ffmpeg **共享库** | 无 | 无 |
| `build_cli/windows/build.sh` | Windows 总入口：工具链环境 → deps → office → cgo 组装 → DLL 白名单校验 | `deps`（只跑依赖产线）/ `build`（只做 cgo 组装）/ `all`（缺省，全流程） | `GOPROXY`（没设就是 `https://goproxy.cn,direct`，`:37`）；`https_proxy`（`:12` 的注释）；`KEEP_WORK`（传给两个子脚本） |
| `build_cli/windows/deps_static.sh` | Windows：编码器全家 8 步 → `build_cli/windows/prefix/`，直调库另拷 `internal/ffmpegcli/libs/` | 无 | `FORCE=1`（清空 prefix 内容，`:19`）；`KEEP_WORK=1`（保留临时目录，`:23`）；`ZPACK_FFMPEG_SEED`（本地种子旁路，`:179`）；`https_proxy`（`:28-30`） |
| `build_cli/windows/office_static.sh` | Windows：office_oxide v0.1.16 → `office/libs/` | 无 | `KEEP_WORK=1`（`:20`）；`https_proxy`（`:6` 的注释） |
| `build_cli/linux/build.sh`、`build_cli/linux/prepare.sh` | Linux 产线占位（还是 musl + zig cc 的旧设想） | — | — |
| `ui/right-click/macos/refresh-engine.sh` | 把 `bin/zpack-cli` 换进 `/Applications/zpack.app` 与 `/Applications/zpack Finder Menu.app` 并重签 | 无 | `IDENTITY`（签名身份；没设就自动找第一个 Apple Development 证书，`:16`） |
| `scripts/genenv/main.go` | 生成 `realEnvTest/` 语料 | `--fast` / `--full` / `--clean`（见第 4.7 节） | — |

（全局规则见 [conventions.md](conventions.md)。构建与验收没有对外命令，所以也没有 CLI 旗标——「开关」就是上表最后一列的环境变量。）

## 3. 产物形态与验收门的判定

### 3.1 产物

| 产物 | 形态 | 体积 | 动态依赖的硬约束 | 能不能交付 |
|---|---|---|---|---|
| `bin/zpack-dev` | 纯 Go | — | — | **不能**。只是开发期工具 |
| `bin/zpack-cli.exe`（交叉编译出来的） | 纯 Go | AGENTS.md:279 记 2026-11-08 的纯 Go `-s -w` 产物是 16.99MB（那之后又加了不少功能，只作量级参考） | AGENTS.md:404 记纯 Go 的 win exe「始终仅 kernel32.dll」 | **不能** |
| `bin/zpack-cli`（mac 完整版） | cgo 静态链 | AGENTS.md:179、:204 记约 48MB。【待确认】这是不是接进 office_oxide（AGENTS.md:233 记增重约 12MB）与 7z 之后的数字 | `otool -L` 里不得出现 `/opt/homebrew`（`build_cli/mac/build.sh:19-23`），动态依赖只能是系统出厂库 | **能** |
| `bin/zpack-cli.exe`（Windows 完整版） | cgo 静态链 | 83MB（AGENTS.md:275、:323；:294 记的是 82MB。产线脚本最后打印的体积取的是 `du -h` 的输出） | import 表里只允许白名单内的系统 DLL（`build_cli/windows/build.sh:69`）；AGENTS.md:403 记 32 个导入 DLL 全是系统出厂 | **能** |
| 两个 mac 装机位里的 `Contents/Resources/zpack` | mac 完整版的拷贝 | 同上 | 同上，而且所在的 .app 必须保持签名有效 | **能**（这就是装机产物） |

依赖准入的总规则（静态优先、体积预算、零新增动态链接、许可证不作硬约束）见第 6.1 节。

### 3.2 能力矩阵：每种构建形态能做什么

| 能力 | 纯 Go（`CGO_ENABLED=0`） | mac 完整版 | Windows 完整版 | 出处 |
|---|---|---|---|---|
| media 全族、audio 全族 | 报「媒体功能需要完整版（build_cli 产线 CGO 构建）；纯 Go 版不含 libav 引擎」 | 可用 | 可用 | `media/stub_nocgo.go:10` |
| 截图、拼接、帧插入、封面、变速 | 各有自己的文案，例如「视频截图需要完整版（build_cli 产线 CGO 构建）；纯 Go 构建不含 astiav/libav 引擎」 | 可用 | 可用 | `media/screenshot_nocgo.go:9`、`merge_nocgo.go:7`、`insertframe_nocgo.go:7`、`cover_nocgo.go:7`、`speed_nocgo.go:7`、`clip_nocgo.go:7` |
| `zpack ffmpeg` 透明代理 | 报「zpack ffmpeg 需完整版（CGO 构建 + 静态产线）」 | 可用 | 可用 | `internal/ffmpegcli/cli_nocgo.go:9` |
| HDR 图片（EXR / Radiance HDR / JXL） | 报「EXR/HDR/JXL 需完整版（CGO 构建）」 | 可用 | 可用 | `image/hdr_nocgo.go:15,23` |
| HEIC / HEIF / AVIF 解码（convert、compress 等处理路径） | 报「HEIC/HEIF/AVIF 解码需要完整版（build_cli 产线 CGO 构建）；纯 Go 构建不含 astiav/libav 引擎」 | 可用 | 可用 | `image/heic_nocgo.go:37` |
| `image info` 遇到 HEIC / HEIF / AVIF | 不解码像素，输出一条 `note`（文案是「像素解码需完整版（CGO 构建）」） | **同样的输出**——这段代码没有按构建形态分支 | 同 mac | `cli/image/image.go:359-374` |
| office 内置引擎（office_oxide） | 报「office 转换需完整版（CGO 构建 + office 产线）」 | 可用 | 可用 | `office/convert_nocgo.go:9` |
| office `--try-msoffice` / `--try-wps` / `--try-libreoffice` | **可用**（外部引擎层是纯 Go） | 可用 | 可用 | `cli/office/office.go:79-90` |
| 7z 写入（`compress --output x.7z`） | 报「zpack 7z 写入需完整版（CGO 构建 + 7z 静态产线）」 | 可用（真实现的构建标签是 `cgo && darwin`） | **同样报「需完整版」**。7z 静态库只有 mac 产线在铸，Windows 走的是桩（构建标签 `!cgo \|\| !darwin`） | `internal/sevenzipcli/cli_cgo.go:1`、`cli_stub.go:1,9`；`core/sevenz.go:28-30` |
| 其余全部：zip / tar 读写，7z / rar / wim / cab / ar / asar / iso 等读取，hash / crypto / base64 / json / skill，pdf，image 标准格式，刻录（Windows 走 IMAPI2） | 可用 | 可用 | 可用 | AGENTS.md:237 |

7z 写入不可用时，`core/sevenz.go:28-30` 在收集输入**之前**就返回「需完整版」，所以连空目录这种本该报「没有可压缩的文件」的场景，也统一报这一句、退出码同样是 1。verify.sh 的 7z 写入段因此有 2 例是碰巧通过的（第 4.6 节）。

### 3.3 怎么判断手里的二进制是不是完整版

- **Windows 完整版**：版本串带 `-full` 后缀（`build_cli/windows/build.sh:65`）。交叉编译出来的纯 Go exe 是 `dev`。
- **mac 完整版**：版本串没有后缀，和带版本号的通用构建分不出来，只能看行为。
- **行为探针**（验收脚本用的就是它）：`zpack media convert /dev/null /tmp/x.mp4 2>&1` 的输出里含「完整版」，就是纯 Go 构建（`verify/verify-media.sh:23-27`）。`zpack ffmpeg -version` 也行：纯构建输出报错 JSON，完整版打印 ffmpeg 的版本头。
- **不能当探针的**：`image info x.heic`。两种构建都输出「像素解码需完整版」的 note（`cli/image/image.go:359-374`）。

### 3.4 构建脚本的成功与失败

构建脚本没有 JSON 输出，判定只看退出码和最后几行。

| 脚本 | 成功 | 失败 |
|---|---|---|
| `build_cli/mac/build.sh` | 打印 `✓ 零 brew 动态依赖`、产物体积与动态依赖清单、`✓ 完成（单文件自包含：媒体引擎 + ffmpeg CLI 进程内直调，零释放）`（`:24-29`），exit 0 | `otool -L` 里出现 `/opt/homebrew`：打印 `✗ 仍链接 brew 动态库 …`，exit 1（`:19-23`）。其它任何一步命令失败，`set -euo pipefail` 直接中止（`:5`） |
| `build_cli/windows/build.sh` | 打印 `✓ DLL 全部系统出厂` 与 `✓ <仓库>/bin/zpack-cli.exe（完整版 <体积>）`（`:74-75`），exit 0 | 白名单外有 DLL：打印 `✗ 非出厂 DLL 入链：…`，exit 1（`:69-73`）。子命令不认识：打印 `用法: … [deps\|build\|all]`，exit 1（`:78`）。定位不到用户目录：打印 `✗ 无法定位用户目录：…`，exit 1（`:26-31`） |
| `ffmpeg_static.sh` / `deps_static.sh` | 末尾打印 `═══ 产线就绪 ═══`（`build_cli/mac/ffmpeg_static.sh:155`）/ `═══ 编码器产线就绪 ═══`（`build_cli/windows/deps_static.sh:261`）和产物清单 | 同上，`set -euo pipefail` |

### 3.5 验收脚本的判定

- 每个用例计一次数。最后打印 `验收结果：通过 N / 失败 M`（`verify/verify.sh:1178`）、`媒体门禁：通过 N / 失败 M`（`verify/verify-media.sh:494`）、`图片门禁：通过 N / 失败 M`（`verify/verify-image.sh:300`）。
- 失败的用例打印 `✗ <用例名>`，下面跟「期望 / 实际」两行（`verify/verify.sh:18-23`）。
- 退出码：失败数为 0 才是 0，否则是 1（`verify/verify.sh:1179`、`verify/verify-media.sh:495`、`verify/verify-image.sh:301`）。
- `verify/verify-media.sh` 有两个前置检查，不过就直接 exit 1：纯 Go 构建打印 `⛔ bin/zpack-cli 为纯 Go 构建（无媒体引擎）——请用 build_cli/mac/build.sh 产物运行本门禁`；系统里没有 `ffmpeg` 打印 `⛔ 需要 ffmpeg 生成夹具与校验产物`（`:22-31`）。
- `verify/verify.sh` 检出刻录机时打印 `· 跳过 burn 多文件真实写入用例：检测到刻录机（刻录不可逆，门禁不写盘）`（`:1122`）。这是提示，不是失败。

## 4. 流程细则

### 4.1 日常开发：纯 Go 循环

```bash
CGO_ENABLED=0 go build -o bin/zpack-dev ./cmd/zpack   # 热缓存 ~1s / 冷 ~10s
CGO_ENABLED=0 go test ./...                            # ~4s
bin/zpack-dev <命令>                                   # media/HDR/HEIC/ffmpeg 走 stub 报「需完整版」
```

（AGENTS.md:195-197。）

- **适用范围**：pdf、hash、压缩、图片标准格式这类纯功能——除了第 3.2 节列出来的那几行，都在纯 Go 下可用。刻录在 Windows 上也可用（IMAPI2 经 go-ole 调用，纯 Go 构建，AGENTS.md:237）。
- 产物名带 `-dev`，不占 `bin/zpack-cli`。
- `go test ./...` 在纯 Go 构建和缺 7z 引擎的机器上照样全绿：两个 7z 写入单测在引擎不可用时 `t.Skip`（`core/sevenz_test.go:15-17`、`:73-75`），行为断言放在 `verify/verify.sh` 的门禁里。
- **新功能家族的落位惯例**（AGENTS.md:200）：引擎放顶层包（同 `image/`、`pdf/`，纯函数加文件），命令面放 `cli/<功能>/`，门禁进 `verify/verify.sh`——纯构建可跑、反馈秒级。只有真的要碰 ffmpeg / libav 才进 cgo 层。

### 4.2 动了引擎代码之后：重铸与刷新装机位

1. **先在完整循环里验证**（mac）：`export PKG_CONFIG_PATH=build_cli/mac/prefix/lib/pkgconfig`，再 cgo 构建（AGENTS.md:199）。产线脚本每次都会 `go clean -cache`（`build_cli/mac/build.sh:15`），所以跑完产线后 cgo 冷构建要等一会，属预期（AGENTS.md:200）。
2. **重铸交付产物**：mac 跑 `bash build_cli/mac/build.sh`，Windows 跑 `bash build_cli/windows/build.sh`。
3. **刷新两个 mac 装机位**：`bash ui/right-click/macos/refresh-engine.sh`（引擎取 `bin/zpack-cli`，`:8`、`:11`）。**禁止手工 cp**，原因见第 6.9 节。

`refresh-engine.sh` 做的事：

| 步骤 | 做法 | 出处 |
|---|---|---|
| 前置 | `bin/zpack-cli` 必须可执行，否则打印 `✗ 缺引擎 …（先跑产线 build_cli/mac/build.sh）`，exit 1 | `:25` |
| GUI 本体 `/Applications/zpack.app`（存在才做） | 把引擎拷到 `Contents/Resources/zpack`，`xattr -cr` 清掉**拷贝件**的资源叉（引擎自带 Finder 自定义图标，codesign 见到就拒签；`bin/zpack-cli` 原件不动）→ `codesign --force --sign -`（adhoc 签名）→ `codesign --verify --strict` | `:20-23`、`:27-32` |
| Finder 宿主 `/Applications/zpack Finder Menu.app`（存在才做） | 要有 Apple Development 证书（`IDENTITY`；没给就自动找第一个，找不到打印 `✗ 无 Apple Development 证书，无法重签 Finder 宿主`，exit 1）→ 同样换引擎 → `codesign --force --sign "$IDENTITY" --options runtime`（与 `build-xcode.sh` 同款；appex 没动，不用重签）→ `codesign --verify --deep --strict` | `:16`、`:34-40` |
| 核对 | `pluginkit -mAvv -i <扩展 ID>` 看当前生效的扩展是不是真身里的 appex；不是就提示 `⚠ 右键扩展当前生效的是别处的副本：…——跑 build-xcode.sh 重新部署` | `:14-15`、`:41-45` |

Windows 没有装机位刷新脚本：交付产物就是 `bin/zpack-cli.exe`，GUI 的无头测试也直接认仓库里的 `bin/zpack-cli.exe`（AGENTS.md:274）。

### 4.3 mac 完整版产线（`build_cli/mac/`）

**流程**（`bash build_cli/mac/build.sh`）：

1. 进仓库根；`PREFIX=build_cli/mac/prefix`（`build.sh:6-7`）。
2. `prefix/bin/ffmpeg` 不存在 → 跑 `ffmpeg_static.sh`（`:9`）。
3. office 库不存在 → 跑 `office_static.sh`（`:10`。这一行查的路径不对，见第 9 节）。
4. `internal/sevenzipcli/libs/libzpack_7zcli.a` 不存在 → 跑 `sevenzip_static.sh`（`:11`）。
5. `export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"` → `go clean -cache` → `CGO_ENABLED=1 go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)" -o bin/zpack-cli ./cmd/zpack`（`:14-16`）。
6. 断言 `otool -L bin/zpack-cli` 里 `/opt/homebrew` 出现 0 次，否则 exit 1（`:18-24`）。
7. 打印产物体积与动态依赖清单（`:26-28`）。

**前置工具**：Go、Xcode 命令行工具（clang / ar / ld，`sevenzip_static.sh:19`）、cargo（`brew install rust`，`office_static.sh:13`）、cmake、git、curl。产线脚本不替你装这些；会 `brew install` 的只有 POC 遗留的 `prepare.sh`。

**源码树与缓存**：mac 这边是常驻的——`build_cli/mac/src/`（各库的源码和构建目录）与 `build_cli/mac/prefix/`，都不入库（`.gitignore:15-16`），换机重跑产线就能复现（AGENTS.md:204）。首次约 25 分钟（AGENTS.md:204；脚本头注写的是 15-25 分钟，`build.sh:4`）。幂等的办法是每个库看 `prefix/lib/lib<名>.a` 在不在；FFmpeg 除了看 `libavcodec.a`，源码树里的 `ffmpeg` 可执行文件比已装的库新时也会重编（`ffmpeg_static.sh:127`）。

**配料表**（版本钉死，AGENTS.md:206）：

| 库 | 版本 | 来源 | 许可证 | 用途 | 出处（`build_cli/mac/`） |
|---|---|---|---|---|---|
| libjxl | v0.11.1（SKCMS 后端） | `github.com/libjxl/libjxl`（depth 1、`--recursive`） | BSD | JXL 图片编解码 | `ffmpeg_static.sh:15-34` |
| libaom | v3.11.0 | `aomedia.googlesource.com/aom`（depth 1） | BSD | AV1 编解码 | `ffmpeg_static.sh:36-49` |
| x264 | master（depth 1，不钉 tag） | `code.videolan.org/videolan/x264.git` | GPL | h264 软编 | `ffmpeg_static.sh:66-73` |
| x265 | master（depth 1） | `code.videolan.org/videolan/x265.git`。AGENTS.md:206 说的是 Bitbucket，和脚本不一致，见第 9 节 | GPL | hevc 软编 | `ffmpeg_static.sh:75-92` |
| lame | 3.100 | SourceForge 的 tarball | LGPL（见下方说明） | mp3 编码（ffmpeg 没有原生的 mp3 编码器） | `ffmpeg_static.sh:94-101` |
| opus | v1.5.2 | `github.com/xiph/opus` | BSD（见下方说明） | opus 音轨 | `ffmpeg_static.sh:103-110` |
| libvpx | v1.15.0 | `github.com/webmproject/libvpx` | BSD（见下方说明） | vp8 / vp9 | `ffmpeg_static.sh:112-119` |
| FFmpeg | n9.0 | `github.com/FFmpeg/FFmpeg`（depth 1，tag n9.0） | 核心 LGPL；开了 `--enable-gpl` 并链入 x264 / x265 之后，整个产物按 GPL 看 | libav 全家 + fftools 直调库 | `ffmpeg_static.sh:121-154` |
| office_oxide | v0.1.16 | `github.com/yfedoseev/office_oxide` | MIT / Apache-2.0 | 文档转换的内置引擎 | `office_static.sh:18` |
| 7-Zip | 25.01 | `github.com/ip7z/7zip`（depth 1，tag 25.01） | 7-Zip LGPL + unRAR 特许（RAR 对象在归档阶段剥掉） | 7z 写入 | `sevenzip_static.sh:27`、`:37` |

lame、opus、libvpx 三项的许可证，AGENTS.md 和脚本里都没有写；表里是按各项目公开发布的许可证填的，没有用仓库内的资料核对过。

**FFmpeg 的 configure 旗标**（`ffmpeg_static.sh:129-136`）：

```
./configure --prefix="$PREFIX" --disable-shared --enable-static \
  --disable-debug --disable-doc --disable-ffplay \
  --disable-xlib --disable-libxcb \
  --enable-libjxl --enable-libaom \
  --enable-gpl --enable-libx264 --enable-libx265 --enable-libmp3lame --enable-libopus --enable-libvpx \
  --enable-videotoolbox --enable-pthreads \
  --extra-cflags="-I$PREFIX/include -O2" \
  --extra-ldflags="-L$PREFIX/lib"
```

**产线对 `.pc` 文件的修补**（来由见第 6.10 节 mac 陷阱 #2、#4、#5）：

- `libjxl.pc` 的 `Libs:` 行追加 `-lbrotlicommon`（`ffmpeg_static.sh:31`）。
- `libjxl_threads.pc` 的 `Libs:` 行追加 `-ljxl -lhwy -lbrotlienc -lbrotlidec -lbrotlicommon -lm -lc++`（`:32`）。
- `x265.pc` 不存在或是空文件时手写一份：`Libs: -L$PREFIX/lib -lx265 -lpthread -lm -lc++`，Version 4.1（`:83-92`）。这段在构建块外面，所以 `.a` 已存在、跳过编译时也会补。

**ffmpeg 直调库怎么铸**（`ffmpeg_static.sh:141-154`）：

1. 从 `ffbuild/config.mak` 取出 `CPPFLAGS`。
2. `cc $CLIFLAGS -I. -Dmain=zpack_ffmpeg_cli_main -Wno-error=missing-prototypes -c fftools/ffmpeg.c -o fftools/ffmpeg_zpack.o`——用宏把 `main` 改名。
3. `ar rcs libzpack_ffmpeg_cli.a fftools/ffmpeg_zpack.o`，再加上 `fftools/*.o fftools/*/*.o` 里除 `ffmpeg.o`、`ffprobe.o`、`ffplay.o`、`*ffplay_renderer*`、`*ffmpeg_zpack*` 之外的全部对象。
4. 拷进 `prefix/lib/`，再拷向 `internal/ffmpegcli/libs/`（这一拷的目标路径有问题，见第 9 节）。

**cgo 侧的自含方式**：三个自家铸的库都用 `#cgo … -L${SRCDIR}/libs` 从包目录里取，裸 `go test` / `go build` 不依赖产线环境变量（AGENTS.md:214、`:233`）。

| 包 | 库 | 链接旗标 | 出处 |
|---|---|---|---|
| `internal/ffmpegcli` | `libzpack_ffmpeg_cli.a` | mac / linux：`-L${SRCDIR}/libs -lzpack_ffmpeg_cli -lz -lm -lpthread -liconv`；Windows：zlib 与 pthread 用 `-Wl,-Bstatic … -Wl,-Bdynamic` 钉成静态 | `linkflags_other.go:8`；`linkflags_windows.go:8` |
| `office` | `liboffice_oxide.a` + `office_oxide.h` | mac / linux：`-L${SRCDIR}/libs -loffice_oxide -lm -ldl -lpthread` | `office/convert_cgo.go:11`；`office/linkflags_other.go:7` |
| `internal/sevenzipcli` | `libzpack_7zcli.a` | `-L${SRCDIR}/libs -lzpack_7zcli -lc++` | `internal/sevenzipcli/cli_cgo.go:15` |

libav 本体不在这个表里——它靠 pkg-config 找，所以产线要 export `PKG_CONFIG_PATH`（`build.sh:14`），开发期的完整循环也要手动 export（AGENTS.md:199）。

**office_oxide 的接入**（`office_static.sh`；AGENTS.md:233）：

- `git clone --depth 1 --branch v0.1.16`，`cargo build --release --lib`——crate 自带 staticlib，产出 `liboffice_oxide.a`。
- **克隆之后必须删掉 `examples/`、`cmd/` 和游离的 `*.go`**（`:26-27`）：这些 Go 源会污染宿主模块的包图，`go build ./...` 会扫到。踩过。
- 把 `.a`（AGENTS.md:233 记 44MB）和 `office_oxide.h` 拷到 `office/libs/`，`.a` 另拷一份进 `prefix/lib`（`:29-32`）。链接时按需取件，实际增重约 12MB。
- 不用它官方的 Go 包，手搓了一层 cgo 薄壳——官方包靠环境变量给链接旗标，不满足「裸构建自含」。

**7z 的接入**（`sevenzip_static.sh`；AGENTS.md:235）：

- 库已存在且没给 `FORCE=1`：打印「7z 直调库已就绪」，直接退出（`:29-32`）。
- 源码不在：`git clone --depth 1 --branch 25.01 https://github.com/ip7z/7zip.git` 到 `build_cli/mac/src/7zip`（`:34-38`）。
- main 改名：sed 把 `MainAr.cpp` 里的 `int Z7_CDECL main(…)` 改成 `extern "C" int Z7_CDECL zpack_7z_main(…)`，已改过就跳过，改完再验一次，没改上就 exit 1（`:40-45`）。
- 编译：`make -j<核数> -f ../../cmpl_mac_arm64.mak USE_ASM= CFLAGS_WARN="-Wno-everything"`。可执行文件那一步链接失败是预期的（crt 找不到改名后的 main），要的只是对象；对象数超过 200 个才算成功（`:47-49`）。**只编 arm64**（`:6`、`:24`、`:47`）。
- 剥 RAR + 并成单成员归档：`ld -r` 把所有不以 `Rar` 开头的 `.o` 并成一个 `zpack_7z_combined.o`，再 `ar rcs libzpack_7zcli.a`；`nm` 验 `T _zpack_7z_main` 在，再验归档里没有 Rar 对象（`:51-58`）。AGENTS.md:235 记约 3.7MB、311 个对象。
- 拷到 `internal/sevenzipcli/libs/`（`:60-62`）。

引擎侧的接线在 `core/sevenz.go`（AGENTS.md:235；行号已对照代码）：

- 引擎不可用立即返回「需完整版」（`:28-30`）。
- `chdir` 是进程全局状态，7z 写入全程持 `sevenzProcessMutex` 串行（`:24-25`、`:56-57`）。
- 路径坐标系：7z 存的名字是相对 cwd 的路径。快路径选公共基目录 chdir，用 `@listfile` 加相对名（`-spd` 让清单条目按字面量处理，`-scsUTF-8` 定清单字符集，`:89-90`）；散装多根输入走兜底——临时 staging 目录用硬链接克隆整棵树，跨设备回退为拷贝（`:144-187`）。
- 已存在的归档先删：7z 的 `a` 对已有归档是「更新并入」，和本工具「覆盖」的契约不符（`:43-45`）。
- 退出码：≥2 算错误，删半成品并报「7z 引擎失败（退出码 N）：…」；1 算警告，容忍（`:122-134`）。
- `.7z` 经 `DetectWriteFormat` 进 `compress`；分卷走通用切分器。
- 已知限制：`--jobs`、`--password-table`、注释对 7z 不生效（有 logf 提示）。互操作：homebrew 的 7zz 26.05 能读自家产物；自家 `extract` 连 mhe 加密头也能解。

### 4.4 Windows 完整版产线（`build_cli/windows/`）

**架构定案**：Windows 走**本机原生 MinGW 产线**（MSYS2 / UCRT64 的 gcc），zig cc 交叉编译的路线已经退役（AGENTS.md:294；`build_cli/windows/build.sh:2`）。

**零解压残留**（2026-11-09 用户定案，AGENTS.md:294）：和 mac 的常驻 `src/` 不同，Windows 这边所有 clone、解压、构建树都放在 `mktemp -d` 出来的临时目录里，`trap cleanup_work EXIT` 退出即清（`deps_static.sh:21-25`、`office_static.sh:19-21`）。`KEEP_WORK=1` 可以保留现场用来调试。仓库里常驻的只有三个脚本加 `build_cli/windows/prefix/` 缓存（`.gitignore:16`）。

**prefix 幂等**：产物在就整块跳过，重跑时零下载、零解压（`build.sh:43-47`；`deps_static.sh` 每一步开头）。

**前置（一次性）**（`build.sh:6-9`；AGENTS.md:296）：

```
winget install MSYS2.MSYS2                                          # 装到 C:\msys64
/c/msys64/usr/bin/bash -lc "pacman -Syu"                            # 首次升级会杀掉自己的终端，属预期，再跑下一行
/c/msys64/usr/bin/bash -lc "pacman -S --needed git make perl diffutils mingw-w64-ucrt-x86_64-{toolchain,cmake,nasm,pkgconf,libiconv,rust}"
```

另外要有 Go（脚本把 `/c/Program Files/Go/bin` 放进 PATH，`build.sh:21`）。

**`build.sh` 的流程**：

1. `MODE` 取第一个参数，缺省 `all`（`:19`）；PATH 前置 `/ucrt64/bin` 和 Go（`:21`）。
2. **用户目录推导**：`LOCALAPPDATA`、`USERPROFILE`、`APPDATA` 有缺的，就按 `/c/Users/$(whoami)` 推导；那里没有 `AppData\Local` 就报 `✗ 无法定位用户目录：…`，exit 1（`:26-36`）。
3. `GOPROXY` 没设就是 `https://goproxy.cn,direct`（`:37`）。
4. `deps` 或 `all`：`prefix/lib/libavcodec.a` 和 `prefix/lib/libzpack_ffmpeg_cli.a` 都在，就打印 `✓ deps prefix 已就绪（跳过）`，否则跑 `deps_static.sh`；`office/libs/liboffice_oxide.a` 在就跳过，否则跑 `office_static.sh`（`:42-53`）。
5. `build` 或 `all`：`export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"`、`CGO_ENABLED=1`、`mkdir -p internal/ffmpegcli/libs`、`go clean -cache`，然后：

   ```
   go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)-full -extldflags '-Wl,-Bstatic -lpthread -Wl,-Bdynamic'" -o bin/zpack-cli.exe ./cmd/zpack
   ```

   （`:58-66`。`-extldflags` 追加在链接行末尾，原因见第 6.11 节 Windows 陷阱 #19。）
6. **DLL 白名单校验**（`:69-73`）：`objdump -p bin/zpack-cli.exe | grep "DLL Name"` 的结果里，匹配不上下面这串的算「非出厂 DLL」，打印 `✗ 非出厂 DLL 入链：…` 并 exit 1：

   ```
   kernel32|ntdll|user32|advapi32|ws2_32|secur32|shlwapi|shell32|ole32|oleaut32|uuid|psapi|gdi32|vfw32|avicap32|userenv|bcrypt|crypt32|mfuuid|strmiids|ncrypt|api-ms-win
   ```
7. `deps` 模式到第 4 步为止（`:77`）；其它不认识的子命令打印用法并 exit 1（`:78`）。

**`deps_static.sh` 的 8 步**（每一步先看 `prefix/lib/<库>.a` 在不在，不在才做，在就打印「缓存跳过」）：

| 步 | 库 | 来源 | Windows 专属处理 | 出处 |
|---|---|---|---|---|
| 1/8 | libjxl v0.11.1（SKCMS） | `github.com/libjxl/libjxl`，tag v0.11.1 | 三个 `.pc` 的后处理（见下） | `:41-65` |
| 2/8 | libaom v3.11.0 | `aomedia.googlesource.com/aom`，tag v3.11.0 | nasm 探测补丁：先核对 `aom_optimization.cmake` 第 218 / 221 行的原文，对不上就中止；对得上就把「看帮助文本里有没有 `-Ox`」换成「真的用 `-Ox` 汇编一个最小文件」 | `:67-102` |
| 3/8 | x264（GPL） | `code.videolan.org/videolan/x264.git` | configure 加 `--host=x86_64-w64-mingw32` | `:104-110` |
| 4/8 | x265（GPL） | Bitbucket 的 `multicoreware/x265_git`，master | `git -c http.sslBackend=openssl clone`；clone 完 `rm -rf .git` 并手写 `x265Version.txt`（`releasetag: 4.1`）；`.pc` 的 `Libs:` 行补 `-lstdc++ -lpthread` | `:112-136` |
| 5/8 | lame 3.100 | SourceForge 的 tarball | configure 加 `--disable-frontend --host=x86_64-w64-mingw32` | `:138-146` |
| 6/8 | opus v1.5.2 | `github.com/xiph/opus` | — | `:148-156` |
| 7/8 | libvpx v1.15.0 | `github.com/webmproject/libvpx` | configure 加 `--target=x86_64-win64-gcc` | `:158-165` |
| 8/8 | FFmpeg n9.0 | 多源回退，见下 | 直调补丁、`.pc` 后处理、直调库 | `:167-232` |

第 1 步的 `.pc` 后处理（`:52-63`）：

- `libjxl.pc`：清掉 `Requires:`，`Libs:` 重写成 `-L${libdir} -ljxl -ljxl_cms -lhwy -lbrotlienc -lbrotlidec -lbrotlicommon -lstdc++ -lm`（依赖顺序要能被 GNU ld 严格按序解析）。
- `libjxl_threads.pc`：`Libs:` 行追加 `-ljxl -lhwy -lbrotlienc -lbrotlidec -lbrotlicommon -lm -lstdc++`。
- `libjxl.pc`、`libjxl_cms.pc`、`libjxl_threads.pc` 的 `Cflags:` 都追加 `-DJXL_STATIC_DEFINE`。

第 8 步 FFmpeg（`:167-232`）：

- **取源**：`ZPACK_FFMPEG_SEED` 指向现成的 tarball 时优先用它（`:179-183`）；否则依次试 `https://ffmpeg.org/releases/ffmpeg-9.0.tar.xz` 和 `https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n9.0.tar.gz`（`:171-174`），`curl -sL --retry 2 --connect-timeout 20 --max-time 600`，下载后先 `tar -tf` 校验完整性再解包（`:188-190`）。全部失败就报 `ffmpeg 全源下载失败`，exit 1（`:197`）。
- **直调补丁**：把 `fftools/cmdutils.c` 里的 `prepare_app_arguments(&argc, &argv);` 换成注释 `/* zpack: 直调禁用 Win32 argv 重取 */`（`:200-201`）。
- **configure**（`:204-210`）：

  ```
  ./configure --prefix="$PREFIX" --target-os=mingw32 --arch=x86_64 \
    --disable-shared --enable-static --disable-debug --disable-doc --disable-ffplay \
    --disable-xlib --disable-libxcb \
    --enable-libjxl --enable-libaom \
    --enable-gpl --enable-libx264 --enable-libx265 --enable-libmp3lame --enable-libopus --enable-libvpx \
    --enable-iconv --extra-libs=-liconv \
    --extra-cflags="-I$PREFIX/include -O2" --extra-ldflags="-L$PREFIX/lib"
  ```

  和 mac 相比：多了 `--target-os=mingw32 --arch=x86_64 --enable-iconv --extra-libs=-liconv`；少了 `--enable-videotoolbox --enable-pthreads`。VideoToolbox 是 mac 专属的硬编；Windows 对应的是 d3d11va / dxva2 硬解，默认内置，没有对应旗标（AGENTS.md:298）。
- **`.pc` 后处理**：`libavcodec.pc` 的 `Libs:` 行追加 `-liconv`（`:213`）；`libavcodec.pc`、`libavformat.pc`、`libavfilter.pc` 的 `Libs:` 行里，在 `-lav…` 后面注入 `-L$PREFIX/lib -ljxl_merged`（`:217-221`）。
- **直调库**（`:224-230`）：`gcc -O2 -I. -Ifftools -Wno-error=missing-prototypes -c fftools/ffmpeg.c -o ffmpeg_cli_main.o` → `objcopy --redefine-sym main=zpack_ffmpeg_cli_main ffmpeg_cli_main.o` → `ar rcs libzpack_ffmpeg_cli.a ffmpeg_cli_main.o` 加上 `find fftools -name '*.o'` 里除去 `ffmpeg.o`、`ffprobe.o`、`ffplay.o` 的全部对象 → 拷到 `prefix/lib/` 和 `<仓库>/internal/ffmpegcli/libs/`。
- 这一步的触发条件是 `libavcodec.a` 或 `libzpack_ffmpeg_cli.a` **任一**不在（`:175`）。

8 步之后还有两段后处理，放在构建块**外面**，所以命中缓存时也会跑：

- **jxl 全家物理合并**（`:237-248`）：`ar x` 解开 `libjxl`、`libjxl_cms`、`libhwy`、`libbrotlienc`、`libbrotlidec`、`libbrotlicommon` 六个 `.a`，合成一个 `libjxl_merged.a`（`*.obj *.o` 都收，开了 `nullglob`）；`libjxl.pc` 的 `Libs:` 重写成 `-L${libdir} -ljxl_merged -lstdc++ -lm`，`libjxl_threads.pc` 里的 `-ljxl` 换成 `-ljxl_merged`。合并本身幂等：`libjxl_merged.a` 在就跳过。
- **运行库静态钉正**（`:250-259`）：`z`、`stdc++`、`bz2`、`lzma`、`iconv`、`pthread` 六个库，用 `gcc -print-file-name=lib<名>.a` 取到 `.a` 的绝对路径，把 `prefix/lib/pkgconfig/` 下所有 `.pc` 里的 ` -l<名> ` 和行尾的 ` -l<名>` 都换成它。

**office_oxide**（`office_static.sh`）：`office/libs/liboffice_oxide.a` 在就跳过（`:14-17`）；否则在临时目录里 clone（三次退避重试，失败等 8 秒，`:24-35`），`cargo build --release --lib`（MSYS2 的 rust，target 是 `x86_64-pc-windows-gnu`，和 MinGW 的 gcc 同 ABI），只把 `.a` 和头文件拷到 `office/libs/`（`:36-41`）。

**链接旗标的 Windows 版**：

| 包 | 旗标 | 出处 |
|---|---|---|
| `internal/ffmpegcli` | `-L${SRCDIR}/libs -lzpack_ffmpeg_cli -Wl,-Bstatic -lz -Wl,-Bdynamic -lm -Wl,-Bstatic -lpthread -liconv -Wl,-Bdynamic`（zlib 钉静态：MinGW ld 默认优先 `libz.dll.a`，会把 MSYS2 的 `libz-1.dll` 带进 import 表） | `internal/ffmpegcli/linkflags_windows.go:3-8` |
| `office` | `-L${SRCDIR}/libs -loffice_oxide -lntdll -lsynchronization -luserenv -lws2_32 -lbcrypt -lcrypt32 -lgdi32 -luser32 -lm -Wl,-Bstatic -lpthread -Wl,-Bdynamic` | `office/linkflags_windows.go:3-11` |

**耗时参考**（AGENTS.md:403，2026-11-24 的实测机二）：首跑 09:30–13:25 全线贯通，deps 8 步用了 1 小时 43 分，其中 FFmpeg 57 分钟；产物 83MB，32 个导入 DLL 全是系统出厂。

### 4.5 云端产线（`.github/workflows/build-windows.yml`）

与本机是同一份脚本（「一份脚本两处共用」，`build-windows.yml:2`），只在用户 push 之后生效（AGENTS.md:296；agent 不 push，AGENTS.md:18）。

| 项 | 现状 | 出处 |
|---|---|---|
| 触发 | push 到 `main` 或 `v*` tag，且改动的路径落在 `build_cli/windows/**`、`cmd/**`、`cli/**`、`core/**`、`image/**`、`internal/**`、`media/**`、`office/**`、`pdf/**`、`ui/**`、`go.mod`、`go.sum` 或这个工作流文件里——改文档、改夹具不占配额；另有 `workflow_dispatch` 可以手动触发 | `:8-26` |
| 并发 | 同一组里只留最后一个 run，进行中的旧 run 被取消 | `:29-31` |
| 运行环境 | `windows-latest`；`msys2/setup-msys2@v2`（UCRT64，装的是和本机一样的那套包）；`actions/setup-go@v5`（1.27） | `:35-59` |
| prefix 缓存 | 只缓存 `build_cli/windows/prefix`，key 是 `deps-n9.0-fullcodecs-win-native-v1` | `:61-65` |
| 步骤 | `bash build_cli/windows/build.sh deps` → `bash build_cli/windows/build.sh build` → 冒烟 → 上传 artifact `zpack-windows-x64-full`（`bin/zpack-cli.exe`） | `:67-86` |
| 冒烟 | `./bin/zpack-cli.exe --version`；`./bin/zpack-cli.exe ffmpeg -version 2>/dev/null \| head -1 \|\| true`；数 `-encoders` 里 `libx264\|libx265\|libmp3lame\|libopus\|libvpx\|libjxl\|libaom` 命中几行，结果不校验（`\|\| true`） | `:75-80` |

两点从工作流文件读出来的现状：

- 缓存里没有 `office/libs`，所以每次云端 run 都会重新 clone 并用 cargo 编一遍 office_oxide（`build.sh:48-52` 的判断每次都不成立）。
- `build.sh deps` 曾经恒以 exit 1 结束，云端产线从来没跑到过 cgo 阶段；已经修了（`build.sh:77`，AGENTS.md:322）。修后云端是否跑通过，【待确认】——本次核对没有联网查 CI 记录。

### 4.6 验收流程与 Windows Git Bash 上的已知噪音

**收尾顺序**：`go test ./...` → `go vet` 三平台 → 三个验收脚本。三个脚本对产物的要求不一样：

| 脚本 | 要什么产物 | 备注 |
|---|---|---|
| `verify/verify.sh` | `bin/zpack-cli`，**应当是完整版** | 7z 写入那一段需要 7z 引擎——注释写明「bin/zpack-cli 恒为完整版故不设能力门控」（`verify/verify.sh:1126`）。拿纯 Go 产物或 Windows 完整版去跑，这一段会红（见第 3.2 节）。有刻录机的机器自动少跑 2 例（`:1115-1123`） |
| `verify/verify-media.sh` | **完整版** `bin/zpack-cli` | 开头有纯构建探针，纯 Go 直接 exit 1（`:22-27`） |
| `verify/verify-image.sh` | `bin/zpack-cli`，纯 Go 就行 | image 零 cgo；HDR / HEIC 的用例在 verify-media.sh 里，不在这里 |

**三个脚本对环境的假设**（都是照着 mac 写的）：

- 工作目录写死在 `/tmp/…`（`verify/verify.sh:30-33` 起）。
- 取文件大小用 BSD 形态 `stat -f%z`（`verify/verify.sh` 有 9 处：`:211`、`:300`、`:301`、`:477`、`:483`、`:489`、`:493`、`:497`、`:541`；另两个脚本同样）。
- `verify/verify-media.sh` 拿 `/dev/null` 当输入做探针（`:23`）；用 macOS 自带的 `textutil` 造 `.doc` 夹具并读回校验（`:363`、`:371`、`:377`、`:393`）；找 `/Applications/LibreOffice.app`（`:389`、`:406-408`）。
- `verify/verify.sh` 用到 python3（往 `/tmp/…` 读写 zip 结构）、perl（造大文本，`:128`、`:297`）、unzip（`:51` 起）、shasum（`:794` 起）、`tar -cjf`（`:326`）、gzip（`:405`）、bzip2（`:413`）、`ln -s`（`:248`）。脚本**不调用** `zip` 和 `7z` 命令——用例名里带这两个字，但归档都是 `zpack` 自产，再用 python3 / unzip 校验。

**Windows Git Bash 上的实测记录**：

| 机器 | 结果 | 出处 |
|---|---|---|
| Win11 26200（2026-11-08） | verify.sh 能跑，前提是放一个 `stat` 垫片（把 `stat -f%z` 转成 `stat -c%s`）到 PATH 前面；shasum、unzip 是 Git Bash 自带的。剩下 6 个具名失败全是脚本坐标系噪音，不是产品 bug。verify-image.sh 要系统 ffmpeg 造夹具，那台机器没装，跑不了 | AGENTS.md:288 |
| Win10 22H2（2026-11-24） | 同样要 `stat -f%z` → `stat -c%s` 的转发垫片（GNU stat 不认 BSD 形态）。当天基线 352 例，通过 319，失败 33：7z write 13 例（Windows 完整版缺 7z 引擎）+ 20 例环境相关（python3 / shasum 的 `/tmp` 路径坐标系和 MSYS 给 exe 的参数路径转换对不上、符号链接与硬链接需要特权、长路径等）。verify-image.sh 与 verify-media.sh 需要系统 `ffmpeg` / `ffprobe` 造夹具，那台机器没有，跑不了 | 2026-11-24 实测（协调方提供的记录，本次核对没有重跑） |

对不上路径的原因（AGENTS.md:288）：python3、perl 按 `/tmp` 坐标写文件，而 MSYS 在启动原生 exe 时会把参数里的 `/tmp/…` 转成 `C:/Users/…/Temp/…`，两套坐标系错位。crypto 全链路和 `hash --check` 在 Windows 坐标下手动验证都正常。Git Bash 的这个自动转换可以用 `MSYS_NO_PATHCONV=1` 关掉。另外 `gofmt -l` 在 autocrlf 的 checkout 下会把所有文件误报成 CRLF。

**基线数字怎么对上**（这是推算，不是实测）：按脚本静态数，verify.sh 现在一共 362 个用例；有刻录机的机器少跑 burn 的 2 例，是 360；再减掉 11-24 当天后来才加的 8 例，正好是实测记录里的 352。

**和实测记录对不上的一例**：实测记录说 7z write 段红了 13 例；按现在的代码读，这一段有 14 例（`verify/verify.sh:1134-1161`），其中 2 例期望的退出码本来就是 1（`:1148`、`:1160`），引擎缺失时退出码也是 1，应当碰巧通过，也就是红 12 例。差的那一例要看实跑输出才能确认，【待确认】。

### 4.7 真实体感语料（`scripts/genenv`）

门禁测的是确定性小样本；genenv 生成的是「真实体感」数据——几 GB 的随机数据用来看进度条，真实的视频 / 音频 / 图片走转换压缩，加密和分卷归档走解压台（`scripts/genenv/main.go:3-5`）。产物落在仓库根的 `realEnvTest/`（不入库，`.gitignore:33`），全部由算法生成，固定种子，可复现，零下载（`main.go:5`）。

| 命令 | 做什么 | 出处 |
|---|---|---|
| `go run ./scripts/genenv` | 标准集，约 3.4GB：`corpus/rand-{1,2,3}g.bin`（各 1GB，种子 `0x7A706163`）、`corpus/text-200m.txt`（可压文本）、`corpus/项目资料/`（混合目录树 8 件：中文名、空格名、三层嵌套），再加媒体族、归档族和一个空的 `out/` | `main.go:57-61`、`:71-134` |
| `go run ./scripts/genenv --fast` | 只生成小件，不到 300MB：`rand-256m.bin`、文本、混合目录树。媒体族和归档族跳过 | `main.go:58-61`、`:75-81` |
| `go run ./scripts/genenv --full` | 标准集，再加 `corpus/rand-5g.bin`（5GB，用来看长任务的进度条） | `main.go:62-64` |
| `go run ./scripts/genenv --clean` | 删除 `realEnvTest/` 整棵树 | `main.go:49-53` |

- **幂等**：已经存在的件跳过（`main.go:85`、`:138`、`:202`、`:233`）。
- **必须在仓库根目录跑**，而且目录名得是 `zpack`（`main.go:43-45`）——genenv 直接 import 了本模块的 `core` 包。
- **媒体族怎么来**：调 `bin/zpack-cli ffmpeg …`（进程内直调）用 lavfi 造 5 个夹具——`video-720p-60s.mp4`（testsrc2 + libx264 + aac）、`video-rotated.mp4`、`audio-tone-320k.mp3`（libmp3lame）、`photo-6000x4000.jpg`、`photo-transparent.png`（`main.go:165-221`）。`bin/zpack-cli` 不在就打印「· 跳过媒体族：未找到 …（先跑 build_cli/mac/build.sh 铸完整版）」（`:166-169`）。
- **归档族怎么来**：不经过二进制，genenv 在进程内直接调 `core.Compress`（`main.go:231-259`）——`archives/机密归档.zip`（密码 `test1234`）、`archives/项目备份.zip.001-002`（两份 1G 随机源，按 1GB 切卷）、`archives/文档集.tar.gz`。
- **Windows 上媒体族恒被跳过**：genenv 找的是 `bin/zpack-cli`，不带 `.exe`（`main.go:47`），而 Windows 的完整版叫 `bin/zpack-cli.exe`。这是按代码读出来的，没有实跑。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 完整版产线 | 有：`build_cli/windows/`，原生 MinGW（MSYS2 / UCRT64） | 有：`build_cli/mac/` | 没有，只有两个占位脚本 |
| 7z 写入引擎 | **没有**——完整版里也报「需完整版」 | 有（构建标签 `cgo && darwin`；直调库只编 arm64） | 没有，同 Windows，走桩 |
| 产物名与版本串 | `bin/zpack-cli.exe`，版本串带 `-full` | `bin/zpack-cli`，版本串不带后缀 | — |
| 产物体积 | 83MB | 约 48MB（【待确认】，见第 3.1 节） | — |
| 动态依赖怎么校验 | `objdump -p` 的输出对 DLL 白名单 | `otool -L` 里不得出现 `/opt/homebrew` | — |
| ffmpeg 的 main 怎么改名 | `objcopy --redefine-sym`，在目标文件层改 | `-Dmain=zpack_ffmpeg_cli_main`，编译宏 | — |
| x265 从哪取 | Bitbucket `multicoreware/x265_git` | 脚本里还是 `code.videolan.org`（AGENTS.md 说已经换成 Bitbucket，见第 9 节） | — |
| 源码树放哪 | 临时目录，退出即清；仓库里常驻的只有脚本和 prefix | 常驻的 `build_cli/mac/src/` 和 `prefix/` | — |
| 硬件编解码 | 没有 VideoToolbox；d3d11va / dxva2 硬解默认内置 | `--enable-videotoolbox` | — |
| 链接器 | GNU ld，严格按命令行顺序解析静态库——Windows 陷阱里一大半出自这里 | ld64，两遍扫描 | — |
| office 链接旗标 | `-lntdll -lsynchronization -luserenv -lws2_32 -lbcrypt -lcrypt32 -lgdi32 -luser32 -lm -lpthread`，没有 `-ldl` | `-lm -ldl -lpthread` | 同 mac |
| 产线 shell 的环境变量 | MSYS2 的登录 shell 不继承 Windows 用户环境（`LOCALAPPDATA` 等），脚本自己推导 | 不涉及 | — |
| 首跑耗时 | 实测机二：deps 8 步 1 小时 43 分（AGENTS.md:403） | 约 25 分钟（AGENTS.md:204） | — |
| `verify/verify.sh` | 要 `stat -f%z` → `stat -c%s` 的垫片；有环境噪音（第 4.6 节） | 三个脚本都是照着它写的 | AGENTS.md 里没有实测记录 |
| `verify/verify-media.sh` | 依赖 macOS 的 `textutil`，按现状跑不了 | 要系统 ffmpeg，要完整版产物 | 同左，没有实测记录 |
| `verify/verify-image.sh` | 要系统 ffmpeg / ffprobe 造夹具，还要 `stat` 垫片和 `md5 -q` | 同左 | 没有实测记录 |
| genenv 的媒体族 | 恒被跳过（找的是 `bin/zpack-cli`，不带 `.exe`） | 可用，要完整版 | 要完整版，而 Linux 还没有完整版产线 |
| 刷新装机位 | 没有这一步，产物就是 `bin/zpack-cli.exe` | `refresh-engine.sh` | — |
| 纯 Go 构建下的刻录 | 可用（IMAPI2，经 go-ole） | 可用（hdiutil） | 可用（xorriso；没装会报安装引导） |

纯 Go 构建与完整版的差别不按平台分，见第 3.2 节的能力矩阵。唯一的平台例外是 7z 写入：mac 完整版有，Windows 完整版没有。

Windows 上才暴露、mac 上毫无症状的代码问题，记录在第 6.8 节。

### 5.1 已知机器的环境事实

只收录 AGENTS.md「环境事实」一节（AGENTS.md:399-404）里和构建、验收有关的条目。

**开发机（mac）**（AGENTS.md:401）

- Go 1.27.3（brew，`/opt/homebrew`）。

**Windows 实测机**（2026-11-08 首测；AGENTS.md:402）

- Win11 26200 x64，Git Bash（MSYS 的路径自动转换要留意）。
- Go 1.27.3，装在 `C:\Program Files\Go`，PATH 里没有——先 `export PATH="/c/Program Files/Go/bin:$PATH"`。
- 装齐了 MS Office、WPS、LibreOffice（三引擎实测的条件）。
- MSYS2 / UCRT64 工具链在 `C:\msys64`，产线用（gcc 16.2、rust、make、nasm、pkgconf）。用 `/c/msys64/usr/bin/bash -lc` 进产线 shell 时，要显式 export `LOCALAPPDATA` / `APPDATA` / `GOPROXY`——现在 `build.sh` 会自己推导（Windows 陷阱 #22、#38）。
- 系统代理是 `127.0.0.1:7897`。Git Bash 的 curl 和 git 不自动走代理，外网下载要手动 export。

**Windows 实测机二**（2026-11-24；AGENTS.md:403）

- Win10 22H2 x64，用户目录 `C:\Users\zhc`，仓库在 `E:\code_files\zpack`，Git Bash，Go 1.27.3（在 PATH 里）。
- **内置光驱** HL-DT-ST DVD-RAM GHC0N（盘符 F:）——所以 `verify/verify.sh` 在这台机器上会跳过 burn 的 2 例。
- 网络：`proxy.golang.org` 不可达（命令要带 `GOPROXY=https://goproxy.cn,direct`）、GitHub 直连快、`*.googlesource.com` 不可达、本机没有配代理。
- 完整版产线首跑 09:30–13:25 全线贯通（MSYS2 20260927 经 winget 安装；UCRT64 gcc 16.2.0-7、rust 1.100.0-2、cmake 4.5.1、nasm 3.02；deps 8 步用了 1 小时 43 分，其中 FFmpeg 57 分钟），产物 83MB，32 个导入 DLL 全是系统出厂。
- 本机专属的六个坑：
  1. 裸 `winget` 从 agent shell 起不来（Permission denied）——要用完整别名路径 `C:\Users\zhc\AppData\Local\Microsoft\WindowsApps\winget.exe`，从 PowerShell 调。
  2. `mirror.msys2.org` 返回 503——在 mirrorlist 前面前置国内镜像（原文件备份在 `%TEMP%\zpack-fullbuild\msys2-backup\`）。
  3. Watt Toolkit（Steam++）把 github.com 指到 127.0.0.1，并用自签的根证书重签 TLS。schannel 的客户端信任它，MSYS2 的 git 和 curl 只认 OpenSSL，报 `unable to get local issuer certificate (20)`——要经 `GIT_SSL_CAINFO` / `CURL_CA_BUNDLE` / `SSL_CERT_FILE` 传一个「系统 CA 加该根」的 bundle。
  4. libaom 和 libjxl 的 skcms 子模块在 googlesource 上——用 `GIT_CONFIG_COUNT` 加 `url.insteadOf` 改走 `github.com/m-ab-s/aom`（v3.11.0 = `d6f30ae4…`，和 Homebrew 公式记录的提交一致）以及 `github.com/google/skcms`（gitlink `42030a77…`）。
  5. cargo 忽略 `HOME`，只认 `%USERPROFILE%`——不设 `CARGO_HOME` 的话，会在用户目录里生成 `.cargo`（51MB）。
  6. 进程创建大约要 100ms（装着腾讯电脑管家），构建比云端慢大约 2.5 倍。
- 那台机器的完整环境记在 `%TEMP%\zpack-fullbuild\scripts\job-build.sh`（本地文件，不在仓库里）。

**依赖**（AGENTS.md:404）

- AGENTS.md 记的 go.mod 纯 Go 依赖：cobra（路由）、klauspost/compress（flate / gzip / zstd）、golang.org/x/text（GBK 解码）、ulikunitz/xz + pierrec/lz4/v4 + andybalholm/brotli（批次 B，mac 增重 1.78MB、Windows 增重 2.46MB）、bodgit/sevenzip + nwaples/rardecode（批次 C，+0.80MB / +0.83MB）、kdomanski/iso9660（批次 D）、sorairolake/lzip-go + nradchenko/go-wim（批次 E-1）、kentalee/go-lzx（批次 E-2，cab）。
- 同一行记「win exe 始终仅 kernel32.dll」，按上下文指的是纯 Go 产物。
- 这份清单和 go.mod 对不上（go.mod 里还有 go-astiav、pdfcpu 等），见第 9 节。

## 6. 设计与陷阱

**改产线脚本、调 ffmpeg / 7z / office 的集成、动依赖版本之前必须知道的事。**

### 6.1 依赖准入（铁律 2，AGENTS.md:16）

2026-11-08 用户放宽后的定案：**静态优先 + 预算 ≤500MB**。

| 规则 | 内容 |
|---|---|
| 硬目标 | 产物是单文件、自包含，裸机上可跑。静态链接优先于动态 |
| 体积预算 | 500MB 以内随意（AGENTS.md:16 记「当前实测静态全量约 130MB」；这个数和两套产线记录的体积对不上，见第 9 节） |
| 零新增**动态**链接，这条照旧有效 | mac：`otool -L` 不得挂 brew 或系统外的路径。Windows：import 表仍然只有出厂 DLL |
| 纯 Go 依赖 | 照旧自由引入 |
| 许可证 | 不是硬约束（AGENTS.md 铁律 2，2026-11-24 修订：不因 GPL 排除候选，项目自身的协议可以改）；项目按 GPL-2.0-or-later 发布，义务见第 6.7 节 |

### 6.2 产物定案：为什么恒为完整版

- **定案**（2026-11-22 用户拍板，AGENTS.md:188-190）：「无纯 Go 心结，一切以功能好用为基础」。`bin/zpack-cli` 与装机产物恒为完整版（`build_cli` 产线的 CGO 构建，media / audio / HDR / HEIC / ffmpeg 直调全可用）。
- 纯 Go 构建只是**开发期快速编译循环**的工具，不得作为交付或装机产物。原因很直接：纯 Go 下 media 族报「需完整版」，交到用户手里会当场撞墙。
- cgo 早就按构建标签分了层，所以纯功能开发用纯循环就行，不需要为了编译快去动 cgo 代码。
- **分层怎么落的**：每个引擎包都是成对的文件——`//go:build cgo` 的真实现，加 `//go:build !cgo` 的桩；7z 更进一步，是 `cgo && darwin` 对 `!cgo || !darwin`。
- **桩不许静默失败**：每个桩都返回一句明确的「…需完整版（…）」错误，CLI 原样放进 `error_info`（文案清单见第 3.2 节）。这对应输出契约里那条「平台不支持的功能分支必须明确报错，禁止静默失败」。

### 6.3 ffmpeg CLI 进程内直调（零释放定案，AGENTS.md:208-214）

**旧方案为什么退役**（用户明确厌恶）：把完整的 ffmpeg.exe（35MB）嵌进 zpack，运行时先释放成临时文件，再起子进程执行。四宗罪：

1. 污染磁盘——临时目录留垃圾，清理失败就残留。
2. **杀毒软件高概率误报**——运行时释放可执行文件是病毒的典型行为。
3. 并发冲突——多个实例抢同一个释放路径，得加锁。
4. 慢——每次都要写 35MB，还要起进程。

go:embed 嵌入、尾注附二进制这两版都已经退役。`build_cli/mac/build.sh:2` 的头注里还写着「ffmpeg CLI 尾注嵌入」，是没改干净的旧话。

**现方案的原理**：编译期把 ffmpeg CLI 当成普通库链进来。

- `fftools/ffmpeg.c` 重编译后给 `main` 改名：mac 用 `-Dmain=zpack_ffmpeg_cli_main` 编译宏；Windows 用 `objcopy --redefine-sym` 在目标文件层改——因为 `-Dmain` 会被 ffmpeg 的 `_WIN32` 分支 `#undef main` 取消（Windows 陷阱 #15、#16）。
- 连同 fftools 的全部对象打成 `libzpack_ffmpeg_cli.a`（约 0.5MB），和 libav 全家一起静态链进 zpack 本体。**ffmpeg 的 main 就是进程里的一个普通 C 函数。**

**运行时形态**：`zpack ffmpeg <参数>` 就是 Go 侧拼好 argc / argv，再调 `C.zpack_ffmpeg_cli_main(argc, argv)`（`internal/ffmpegcli/cli_cgo.go:28-40`）。零文件释放、零子进程、零缓存目录。ffmpeg 的 main 结尾走 `exit()`，进程就此终结，退出码和 stdio 全是 ffmpeg 的原生语义——这是输出契约的唯一例外。

**Windows 要多堵一个洞**：ffmpeg 的 `prepare_app_arguments` 在 `_WIN32` 下用 `GetCommandLineW()` 重新取命令行，直调时拿到的是宿主 zpack.exe 的命令行——`zpack ffmpeg` 里的 `ffmpeg` 子命令名被当成了位置参数，输出名变成 `ffmpeg`。产线用 sed 删掉这两处调用（`build_cli/windows/deps_static.sh:198-201`；Windows 陷阱 #17）。

**怎么验证零释放**（2026-11-09 Windows 实测，AGENTS.md:213）：x264 / x265 转码 + media info + hash 全轮跑完，临时目录和 CWD 新增的文件恰好等于产出物本身；转码进行中系统里没有 ffmpeg 子进程。门禁里有对应的用例（`verify/verify-media.sh:344-348`）。

### 6.4 7z 写入直调引擎（AGENTS.md:235；`build_cli/mac/sevenzip_static.sh:5-17`）

- **定案**（2026-11-22 用户拍板）：「全量接入官方 C」。7-Zip 25.01 的 Alone2 bundle（全格式读 + 写引擎），main 改名成 `extern "C" zpack_7z_main` 后在进程内调用。
- 和 ffmpeg 直调的差别：**7z 不 exit，正常返回退出码**。stdio 由 Go 层接管——`RunCaptured` 临时接管 fd 2，把 7z 的输出逐行记下来，同时实时转发到真正的 stderr（`internal/sevenzipcli/cli_cgo.go:54-92`）。

**产线四坑**（`sevenzip_static.sh:5-17` 头注，全部踩过）：

1. **注册靠静态 ctor**。普通的 `.a` 是按需拉取对象的，永远拉不到那些注册对象——引擎的注册表是空的，所有命令都返回 E_NOTIMPL。修法：先 `ld -r` 把全部对象并成一个可重定位对象，再 `ar` 成**只有一个成员的归档**。这样拉 `zpack_7z_main` 就连带全部对象，等价于 whole-archive。之所以绕这条路，是因为 cgo 的 LDFLAGS 不接受带路径的 `-Wl,-force_load`。
2. **`DISABLE_RAR` 不起作用**。它只被 Format7zF bundle 的 `Arc_gcc.mak` 认，Alone2 配 `cmpl_*.mak` 时无视它，`Rar*.o` 照样链进去。修法：归档阶段手动排除 `Rar*.o`——这是许可证上的要求，unRAR 的限制代码不能进产物。
3. **`USE_ASM=1` 两头挨打**。`Asm/arm64/7zAsm.S` 里的 `and` 宏撞上 `-Wc++-keyword`（clang 把 `.S` 当 C++ 编）；`var_mac_arm64.mak` 又设了 `CFLAGS_WARN=-Weverything`，`C/LzmaEnc.c` 的 implicit void 指针转换报错。修法：`USE_ASM= CFLAGS_WARN="-Wno-everything"` 两个都覆盖。关掉 ASM 没有损失——arm64 上的 CRC / SHA / AES 本来就是 C 的优化路径。
4. **main 改名后可执行文件必然链接失败**（crt 找不到 main）。这是预期现象，要的只是对象。

另外（头注第 2 条）：裸改名得到的是 C++ 修饰名，必须写成 `extern "C"`。

- **已知限制**：`--jobs`、`--password-table`、注释对 7z 不生效；Windows 产线待铺。
- **互操作**：homebrew 的 7zz 26.05 能读自家产物；自家的 `extract` 连 mhe 加密头也能解。

### 6.5 Windows 的架构定案与零解压残留（AGENTS.md:294）

- **架构**：走本机原生 MinGW 产线（MSYS2 / UCRT64 的 gcc），zig cc 交叉编译的路线退役。`build_cli/README.md` 里关于 zig 的说法已经过时（见第 9 节）。
- **零解压残留**（2026-11-09 用户定案）：所有 clone、解压、构建树都在 `mktemp` 的临时目录里，`trap` 退出即清；`KEEP_WORK=1` 保留调试现场。仓库里常驻的只有三个脚本加 prefix 缓存。
- **prefix 幂等**：产物在就整块跳过，重跑时零下载、零解压。
- **一份脚本两处共用**：本机和云端 CI 调的是同一个 `build_cli/windows/build.sh`。

### 6.6 缓存、幂等与网络

三层缓存，各有各的坑。

**1. Go 构建缓存。** cgo 的 pkg-config 结果会进构建缓存，而且不随环境变量失效——切到静态前缀之后必须 `go clean -cache`，否则链的还是 brew 的 dylib（mac 陷阱 #7）。`-L` 目录里库文件的内容变了，也不会触发重新链接（Windows 陷阱 #26）。所以两个 `build.sh` 每次都无条件 `go clean -cache`（mac `build.sh:15`、Windows `build.sh:62`）——**产线慢是故意的**。

**2. prefix 缓存。** 靠产物文件在不在来判断。三种坑：

- **后处理会不会重放**。Windows 这边放在构建块**里面**的后处理，命中缓存时不会重跑：libjxl 三个 `.pc` 的改写（`deps_static.sh:52-63`）、libaom 的 nasm 补丁（针对源码树，`:74-95`）、FFmpeg 的直调补丁和 `.pc` 改写（`:198-221`）。放在构建块**外面**的每次都跑：x265 的 `.pc`（`:131-136`）、jxl 合并（合并本身幂等，`:237-248`）、运行库钉正（`:250-259`）。所以改了「里面」那几段，要么 `FORCE=1`，要么手动重放，要么清掉 prefix。mac 这边 libjxl 的两行 sed 在构建块里（`ffmpeg_static.sh:30-32`），x265 的手写 `.pc` 在外面（`:83-92`）。
- **幂等守卫要用 `-s`，不能用 `-f`。** 被 `set -u` 中断的 heredoc 会留下 0 字节的 `.pc` 文件；用 `-f`（存在）判断的话，它会永久跳过重写。要用 `-s`（非空）（mac 陷阱 #5）。
- **配料或补丁变了**：`FORCE=1`（mac `ffmpeg_static.sh:13`、Windows `deps_static.sh:19`）。

**3. CI 的 actions/cache。** key 是 `deps-n9.0-fullcodecs-win-native-v1`。配料或补丁变了要换 key——工作流的注释写明只在「首跑、变版本、缓存 7 天没被用而逐出」时才编译（`build-windows.yml:3`）。

**网络。**

- **所有 git 网络操作一律假设可能中断**，脚本里的 clone 和下载必须带重试（Windows 陷阱 #36；office_oxide 的 clone 是三次退避重试，`build_cli/windows/office_static.sh:24-35`）。
- 下载要多源回退，再留一条本地种子的旁路（Windows 陷阱 #35）。
- 代理按机器区分，不是所有机器都能用同一个端口（Windows 陷阱 #23）。Windows 的产线脚本从 `https_proxy` 环境变量取代理，没设会打印一句提示（`deps_static.sh:28-30`）。

### 6.7 许可证义务

| 组件 | 许可证 | 对产物的含义 | 出处 |
|---|---|---|---|
| zpack 自身 | GPL-2.0-or-later | 根目录 `LICENSE`。整个产物按 GPL 发布：对外分发要提供对应的完整对应源码（含所链的 x264 / x265 / FFmpeg 的源码与构建脚本） | `LICENSE`；AGENTS.md:16 |
| FFmpeg 核心 | LGPL；开 `--enable-gpl` 并链入 x264 / x265 之后，整个产物按 GPL-2.0-or-later 看 | 同上一行 | 第 4.3 节；`build_cli/mac/ffmpeg_static.sh:4-5` |
| x264、x265 | GPL | 入列之后**产物承担 GPL 义务**。对外分发要提供对应的源码和可重链接的对象 | `build_cli/mac/ffmpeg_static.sh:4-5`；AGENTS.md:206、:298 |
| libjxl、libaom | BSD | 无附加义务 | AGENTS.md:16 |
| lame、opus、libvpx | AGENTS.md 和脚本里都没写（第 4.3 节的配料表里按公开许可证填了，未核对） | — | — |
| office_oxide | MIT / Apache-2.0 | 脚本头注：「无 GPL 污染」 | `build_cli/mac/office_static.sh:3` |
| 7-Zip（Alone2） | 7-Zip LGPL + unRAR 特许 | `Rar*.o` 在归档阶段剥掉，unRAR 的限制代码不进产物 | AGENTS.md:235 |

- **决策史**：2026-11-08 用户定案「功能全优先」——含 GPL 库（x264 / x265），产物承担 GPL 义务；对外分发需要提供对应源码或重链对象；内部使用无碍；需要 LGPL 版的话可以去掉 x264 / x265（`build_cli/mac/ffmpeg_static.sh:4-5`）。Windows 产线「GPL 义务同 mac」（AGENTS.md:298）。2026-11-24 用户定案：许可证不是硬约束，项目自身的协议可以改（「不要因为许可证把自己拴死」）——项目随之按 GPL-2.0-or-later 发布（`LICENSE`）；选库时不因 GPL 排除候选，只如实说明它要求项目带什么协议。
- **LGPL 退路**：去掉 x264 和 x265 两味配料。mac 上 VideoToolbox 仍然能做 h264 / hevc 的硬编（AGENTS.md:206）。Windows 产线没有 VideoToolbox 这样的硬编旗标（AGENTS.md:298），这条退路在 Windows 上就是单纯去掉 x264 / x265。
- 产线脚本里没有「生成许可证清单或源码包」这一步——对外分发时的合规材料得另外准备。
- AGENTS.md 铁律 2 曾写着「GPL 库（x264/x265）不捆绑」，和产线脚本矛盾；2026-11-24 已改为「许可证不是硬约束」，不再矛盾。仍然有效的只有第三方自己的专有限制（unRAR 不得用于做 RAR 压缩），它和项目协议无关。

### 6.8 三平台验证的教训：Windows 上才暴露的问题

2026-11-08 的 Windows 首实测（Win11 26200 + Git Bash + Go 1.27.3）：纯 Go 构建通过、三平台 `go vet` 全过、`go test ./...` 全绿——然后实测还是修掉了 **7 个 Windows 特有、mac 上毫无症状的潜伏 bug**（AGENTS.md:279-286）。和工程约定直接相关的几条：

| 教训 | 出自 |
|---|---|
| **Windows 上「关闭先于删除」是硬约束，defer 的顺序不算数。** 句柄泄漏在 mac 上无声无息；在 Windows 上，删一个还开着的文件会失败，临时目录清理就报「being used by another process」 | bug 1、2（AGENTS.md:280-281） |
| **手拼路径是平台 bug 的温床。** 文件系统场景用 `path/filepath`（比如 `filepath.Abs`）；条目名、glob 匹配这种「逻辑路径」场景用 `path` | bug 3（AGENTS.md:282） |
| glob 匹配统一用 `path.Match` 加入参 `ToSlash`。`filepath.Match` 的 `*` 会按平台分隔符避让，Windows 上语义会漂移 | bug 6（AGENTS.md:285） |
| COM 要钉线程（`LockOSThread` + `CoInitializeEx`，配对 `CoUninitialize`）；file URI 要经 `url.URL` 构造，手拼的反斜杠会让 LibreOffice 的隔离 profile 初始化失败 | bug 4、5（AGENTS.md:283-284） |
| 没开开发者模式时 `os.Symlink` 会因为特权不足失败——要降级，不能让整包解压跟着失败 | bug 7（AGENTS.md:286） |

另外两条同属「在 Windows 上跑一遍才知道」，细则归 [gui.md](gui.md)：

- Service 层给页面的回调必须在后台协程里给——同步回调里的 `mygo.RunOnMain` 在无头测试里永远等不到主循环，整套测试挂死（AGENTS.md:258）。测试里别把带反斜杠的路径直接拼进 JSON 串（同处）。
- `gofmt -l` 在 autocrlf 的 checkout 下会把所有文件误报成 CRLF（AGENTS.md:288）。

### 6.9 刷新装机位：为什么禁止手工 cp

（AGENTS.md:191、`:390`；`ui/right-click/macos/refresh-engine.sh:4-6`。）

- 两个装机位——`/Applications/zpack.app` 和 `/Applications/zpack Finder Menu.app`——都是**已签名**的 .app。包里的文件事后被改，哪怕只是换了一个 `Resources/zpack`，签名就失效了。
- Finder 宿主带 Hardened Runtime。签名一坏，系统就弃用真身，退回别处注册的旧副本。2026-11-23 的「右键图标又没了」就是这么来的：被选中的旧副本里没有 `zpack-menu.icns`（AGENTS.md:191）。
- 所以换引擎必须走 `refresh-engine.sh`：拷进去 → 清资源叉 → 重签 → `codesign --verify` 校验 → 核对 pluginkit 当前生效的扩展是不是真身（步骤见第 4.2 节）。
- 引擎自带 Finder 的自定义图标（在资源叉里），codesign 见到就拒签。脚本只对**拷贝件**做 `xattr -cr`，`bin/zpack-cli` 原件不动（`refresh-engine.sh:18-22`；AGENTS.md:395）。
- `ui/gui/install.sh` 同理：引擎是在 mygo 签名**之后**才注入的，注入完要 adhoc 重签并校验（AGENTS.md:390）。
- 右键出问题时的诊断三连（AGENTS.md:390）：`pluginkit -mAvv -i <appex-id>` 看 Path 是不是 `/Applications` 下的真身 → `codesign --verify --deep --strict` 看签名 → `lsregister -dump | grep` 看有没有旧宿主。同 ID 多副本注册、appex 手动签名必带 `--options runtime` 这两条，细则在 [gui.md](gui.md)（AGENTS.md:389、:391）。

### 6.10 mac 陷阱清单（#1–#14，AGENTS.md:216-231）

全部实际踩过。改产线、调 ffmpeg 集成之前先读。#12–#14 是引擎（cgo 层）代码的坑，一并列在这里。

1. **configure 找不到自建库**（AGENTS.md:218）。ffmpeg 探测第三方库全靠 pkg-config。**修法**：`export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"` 必须在 `./configure` 之前设置。落点：`build_cli/mac/ffmpeg_static.sh:128`（Windows 侧 `deps_static.sh:203` 也标了同一条）。
2. **libjxl 0.11.1 的 `.pc` 静态闭包有缺陷**（AGENTS.md:219）。`libjxl.pc` 缺 `-lbrotlicommon`（brotlidec 依赖它），`libjxl_threads.pc` 的闭包全缺。**修法**：产线用 sed 补齐，否则 configure 探测必挂。落点：`ffmpeg_static.sh:30-32`。
3. **libjxl 默认的 lcms 后端是坑**（AGENTS.md:220）。lcms 子模块编了却不装静态库，`libjxl_cms.a` 悬着 lcms 的符号，链接失败。**修法**：`-DJPEGXL_ENABLE_SKCMS=ON`，换成自含的 SKCMS 后端。落点：`ffmpeg_static.sh:25`。
4. **x265 的 cmake 不装 pc 文件**（AGENTS.md:221；模板 `x265.pc.in` 没被处理）。**修法**：手写一份，依赖闭包（`-lc++ -lpthread -lm`）必须写进 `Libs:` 行——configure 探测期**不读** `Libs.private`。落点：`ffmpeg_static.sh:83-92`。
5. **空文件陷阱**（AGENTS.md:222）。被 `set -u` 中断的 heredoc 会留下 0 字节的 pc 文件。**修法**：幂等守卫用 `-s`（非空），不能用 `-f`（存在），否则永久跳过重写。落点：`ffmpeg_static.sh:84`。
6. **X11 / xcb 被自动探测到**（AGENTS.md:223）。ffmpeg 的 configure 在 mac 上会探测到 brew 的 X11，把动态依赖带进静态链接。**修法**：`--disable-xlib --disable-libxcb`；ffplay 依赖 SDL2（静态拿不到），所以 `--disable-ffplay`。落点：`ffmpeg_static.sh:130-131`。
7. **cgo 的构建缓存吃掉了环境切换**（AGENTS.md:224）。pkg-config 的结果进了 Go 构建缓存，而且不随环境变量失效。**修法**：切到静态前缀之后必须 `go clean -cache`，否则链的还是 brew 的 dylib。落点：`build.sh:15`。
8. **fftools 8.x 的对象散布在子目录里**（`graph/`、`textformat/`；AGENTS.md:225）。**修法**：ar 的清单用 `fftools/*.o fftools/*/*.o` 的 glob 加排除法；排除模式必须锚定成 `/(ffmpeg|ffprobe|ffplay)\.o$`——`grep -v "ffmpeg.o"` 里那个点号是正则通配，会误吞 `ffmpeg_opt.o`。落点：`ffmpeg_static.sh:147-149`。
9. **C argv 的惯例**（AGENTS.md:226）。argv[0] 是程序名占位（ffmpeg 的 usage 和报错会引用它），argc 含程序名，末位是 NULL。传错的话 ffmpeg 当成零参数，只打 usage。落点：`internal/ffmpegcli/cli_cgo.go:31-39`。
10. **cgo 下 C 的 `exit()` 不 flush stdio**（AGENTS.md:227）。终端里正常，管道或重定向时丢输出。**修法**：调 main 之前 `setvbuf(stdout, NULL, _IOLBF, 0)`，钉成行缓冲。落点：`internal/ffmpegcli/cli_cgo.go:29-30`；7z 侧同款，`internal/sevenzipcli/cli_cgo.go:38-39`。
11. **ffmpeg 的 main 在失败路径上会返回负值**（不是 exit；AGENTS.md:228）。曾经和桩的语义冲突过。**修法**：用 `ffmpegcli.Available()` 探针分流；负值就是真实的失败码，照传给 `os.Exit`。落点：`cli/media/ffmpeg.go:34-38`。
12. **astiav 的 `ImageBufferSize` / `ImageCopyToBuffer` 的 align 必须 ≥1**（AGENTS.md:229）。传 0 → EINVAL → size 0 → 空切片 panic（`b[0]` 越界）。属引擎代码，细则在 [media.md](media.md)、[image.md](image.md)。
13. **sws 像素比对的参照要选对**（AGENTS.md:230）。sws 缺省 FAST_BILINEAR，ffmpeg CLI 缺省 Bicubic，色度边缘有分歧；rgb24 的 CLI 路径自带 dithering。逐位等价的正确参照是 `rgba64le` 的低字节。门禁里的 AVIF 用例用的就是这个参照（`verify/verify-media.sh:322-331`）。
14. **解码循环**（AGENTS.md:231）。单个包直收对 libjxl 不够——首包不吐帧，返回 EAGAIN。**修法**：「收帧—读包」循环，收尾用 `SendPacket(nil)` 冲洗到 EOF；帧跨函数传递要用 `Ref()` 克隆（内部帧释放之后，调用方手里的那份仍然有效）。

### 6.11 Windows 陷阱清单（#15–#38，AGENTS.md:300-330）

全部实际踩过。**编号说明**：AGENTS.md 的编号到 #34 之后出现了两个 #35、两个 #36（AGENTS.md:322-323 与 :328-330 各一组）。本表按脚本注释里的引用排成连续的 #15–#38——脚本引用的 #35（`deps_static.sh:168`）和 #36（`office_static.sh:24`）保持原号，2026-11-24 新增的两条顺延成 #37、#38。

15. **`-Dmain=` 改名在 Windows 上失效**（AGENTS.md:301）。fftools/cmdutils.h 在 `_WIN32` 分支里 `#undef main`（防 SDL 覆盖的历史代码），把宏取消了，mac 的手法失效。**修法**：改走 #16 的 objcopy。
16. **C 的 main 符号进了链接 = `fatal error: randinit twice` + 段错误**（AGENTS.md:302）。曾试过「原名 main 编入 + 转发包装」：MinGW 的 CRT（crt2.o）捕获到 main 后在初始化路径里调用它，和 Go runtime 再入冲突。**治本**：`objcopy --redefine-sym main=zpack_ffmpeg_cli_main`，在目标文件层改名，既绕开了 #undef，又消掉了 C 的 main。落点：`deps_static.sh:222-225`。
17. **ffmpeg 在 `_WIN32` 下无视传进来的 argv**（AGENTS.md:303）。cmdutils.c 的 `prepare_app_arguments` 用 `GetCommandLineW()` 重取命令行，直调时拿到的是宿主 zpack.exe 的命令行——`zpack ffmpeg` 里的 `ffmpeg` 子命令名被当成位置参数，输出名变成 `ffmpeg`。**修法**：产线用 sed 删掉这两处调用。补丁要幂等，放在 prepare 的主体里，不要放进解压分支（嵌套事故时会被跳过）。落点：`deps_static.sh:198-201`。
18. **GNU ld 对静态库严格按序**（AGENTS.md:304）。libavcodec 引用 libiconv，libiconv 必须排在其后（mac 的 ld64 两遍扫描，没有这个问题）。**修法**：把 `-liconv` 注入 libavcodec.pc 的 `Libs:` 行——configure 的 `--extra-libs` 只进 `Libs.private`，不生效。落点：`deps_static.sh:213`。
19. **pkg-config 会把 `-Wl,*` 重排到行尾**（AGENTS.md:305）。用 `-Bstatic` 包裹的办法对 `.pc` 无效。**修法**：MSYS2 的运行库（z / bz2 / lzma / iconv / stdc++ / pthread）一律换成 `.a` 的绝对路径——路径用 `gcc -print-file-name=libXXX.a` 取；别用 `-print-libgcc-file-name`，那是 gcc 的私有目录。winpthread 特殊：静态的 libstdc++.a 自己引用 pthread_*，`-lpthread` 必须排在它之后，而各包 `#cgo` 段的先后没法保证——所以用 `go build -ldflags '-extldflags "-Wl,-Bstatic -lpthread -Wl,-Bdynamic"'` 钉到链接行的末尾。落点：`deps_static.sh:250-259`、`build.sh:63-66`。
20. **Rust 的 staticlib 不自带链接指令**（AGENTS.md:306）。liboffice_oxide.a 的 Rust std 要消费者补 `-lntdll -luserenv -lws2_32 -lbcrypt -lcrypt32`（Nt*、GetUserProfileDirectoryW 等）；`-ldl` 是 unix 概念，MinGW 没有这个库。**修法**：office 的链接旗标拆成 `linkflags_windows.go` / `linkflags_other.go`。落点：`office/linkflags_windows.go:11`、`office/linkflags_other.go:7`。
21. **GNU sed 的 BRE 里，`\(..\|..\)` 这种组合在 MSYS2 上失配**（AGENTS.md:307）。`\([ "]\|$\)` 的写法静默不替换。**修法**：拆成朴素的两条——`s| -lz | path |g` 加 `s| -lz$| path|`。落点：`deps_static.sh:257`。
22. **MSYS2 产线的环境变量全靠手工**（AGENTS.md:308）。`bash -lc` 不继承 LocalAppData / APPDATA，go 找不到 GOCACHE / GOENV；GOPROXY 同理（proxy.golang.org 不可达）。**修法**：显式 export。现在 `build.sh` 会按 `/c/Users/$(whoami)` 推导（见 #38），并给 GOPROXY 一个缺省值。落点：`build.sh:23-37`。
23. **网络**（AGENTS.md:309）。ffmpeg.org 和 GitHub 直连很慢（约 60KB/s），走本机代理 16 秒下完 12MB 的 tarball；tuna、ustc 都没有 ffmpeg releases 的镜像。**代理按机器区分**（2026-11-22 实测）：mac 开发机是 `http://127.0.0.1:7890`（7897 不通），Windows 实测机是 `http://127.0.0.1:7897`——git 网络操作失败重试时，先挂对端口，再裸连。落点：两个产线脚本头注里的 `https_proxy` 提示。
24. **目录被残留进程的 CWD 占住，删不掉**（Windows 特有，`Device or resource busy`；AGENTS.md:310）。**修法**：rm 之前确认没有 shell 驻留在里面；脚本兜底做法是「内容并入 + dotfiles 单独 mv」——glob 的 `*` 不含 dotfiles，rmdir 会因为残留物失败。
25. **`pacman -Syu` 会自杀**（AGENTS.md:311）。升级 msys2-runtime / bash 的时候会杀掉自己的终端，后面的安装静默丢失。**修法**：先单独跑 `-Syu`（等它自杀），再单独跑 `-S` 的包列表。落点：`build.sh:8-9` 的头注。
26. **prefix 缓存和补丁是耦合的**（AGENTS.md:312）。pc 后处理如果只在 make install 的分支里跑，补丁或后处理更新时 prefix 命中就会跳过——要手动重放，或者清掉 prefix。cgo 侧 `-L` 目录里库的内容变了，也不会触发 go 重链（同 mac #7）——所以 build 脚本恒 `go clean -cache`。哪些后处理在构建块里面、哪些在外面，见第 6.6 节。
27. **x265 的三连坑**（Windows 专属；AGENTS.md:313）。
    - a) Git Bash 的 git 走 schannel，对 Bitbucket 握手失败——改用 `git -c http.sslBackend=openssl`。
    - b) Version.cmake 用 `git describe` 推版本，MinGW 有 windres 才走 RC 版本块——depth-1 没有 tag，推出空串，cmake 崩（用 `-D` 传的值会被 git 推导覆盖）。**治本**：clone 之后 `rm -rf .git`，再手写 `x265Version.txt`（Version.cmake 走 tarball 路径，读显式的版本）。
    - c) 稳定 tag 3.6 里的 `cmake_policy(SET CMP0054 OLD)` 被 MSYS2 的 cmake 4.x 拒绝（这个 policy 已经删除，`CMAKE_POLICY_VERSION_MINIMUM` 这个逃生阀管不到 SET）——所以用 master（已经适配）。
    - 另外：GitHub 上的 videolan/x265 镜像 2020 年就停更了，不能用。

    落点：`deps_static.sh:112-128`。
28. **lame 3.100 的 frontend 撞上 gcc 14+**（AGENTS.md:314）。隐式函数声明默认当成错误。**修法**：`--disable-frontend`——只要 libmp3lame，和 x264 的 `--disable-cli` 是同一个思路。落点：`deps_static.sh:141-144`。
29. **libjxl 的静态闭包**（mac #2 在 Windows 上的加重版；AGENTS.md:315）。brotli 和 hwy 互相引用，顺序在多个 pc 展开之后失控。**修法**：libjxl.pc 去掉 Requires，Libs 写成按依赖顺序排好的全闭包。另外 cmake 系的对象后缀是 `.obj`，不是 `.o`。落点：`deps_static.sh:52-57`、`:242`。
30. **libjxl 的 dllimport**（Windows 专属；AGENTS.md:316）。头文件默认按 dllimport 声明，链接器去找 `__imp_` 符号，必然挂。**修法**：静态链要 `-DJXL_STATIC_DEFINE`，注入全部 jxl pc 的 Cflags。mac 没有 dllimport 机制，所以没踩。落点：`deps_static.sh:59-63`。
31. **x265 新版的 pc 闭包**（AGENTS.md:317）。新版 cmake 会自己装 x265.pc，但 C++ 运行时写在 `Libs.private` 里，`pkg-config --libs` 不带出来。**修法**：无条件用 sed 确保 `Libs:` 行含 `-lstdc++ -lpthread`。mac 那边手写的 pc 块已经过时；mac 的 clang 驱动会自动链 C++，所以没踩。落点：`deps_static.sh:131-136`。
32. **jxl 全家物理合并**（AGENTS.md:318）。GNU ld 按序，加上 pkg-config 展开顺序不可控，jxl 和 jxl_cms 跨库互相引用，在 cgo 的最终链接里必然挂。**修法**：`ar x` 解开六个 `.a`，合并成单一的 `libjxl_merged.a`。注意 nullglob——空的 glob 会把字面的 `*.o` 传给 ar。落点：`deps_static.sh:234-248`。
33. **ffmpeg 9 的 pc 生成器变了**（AGENTS.md:319）。依赖闭包直接散装写进 Libs（8.x 走的是 Requires.private），再加上 cgo 展开时的重排。**修法**：jxl 的符号统一由 merged 兜底库紧跟在 `-lav*` 后面注入三个 av 的 pc——散装的残留无害。落点：`deps_static.sh:214-221`。
34. **MSYS2 的 nasm 3.02 没有 `-Ox` 旗标，libaom 的 configure 中止**（2026-11-22 实测机首跑踩到；AGENTS.md:320-321）。`mingw-w64-ucrt-x86_64-nasm 3.02-1` 的帮助文本里不再出现 `-Ox`（优化改成了 `-Oflags...` 的形式），而 libaom 的 `build/cmake/aom_optimization.cmake` 里 `test_nasm()` 是拿帮助文本做字符串匹配的，必然失败，cmake 在 libaom（deps 的第 2/8 步）直接中止。当时列的三种修法：装旧版 nasm 2.16.x 放到 PATH 前面；给 cmake 打补丁放宽检查；`-DAOM_TARGET_CPU=generic` 关掉汇编（有性能损失，不推荐）。**产线复跑之前必须先解决这一坑，否则 prefix 里永远没有 libaom。** 已修（AGENTS.md 记提交 a1c91e1）：取到 aom 之后打补丁——先核对第 218 / 221 行的原文（`tr -d` 容忍 CRLF），对不上就中止；对得上就用 heredoc 写 11 行 cmake，把「看帮助文本」换成「真的用 `-Ox -f win64` 汇编一个最小文件」，探不过仍然 FATAL_ERROR 报原文案；再用 sed 把前后段拼接回写。LF 和 CRLF 两种形态都模拟验证过；libaom 实测编译安装成功。落点：`deps_static.sh:70-95`。
35. **ffmpeg tarball 的下载通道堵死**（代理环境；2026-11-22 实测机二跑踩到；AGENTS.md:328）。ffmpeg.org 经本机代理 TLS 中断（curl exit 35）；GitHub codeload 的归档通道 body 传输停滞（HEAD 通，下载 0B/s）——但**git 协议通道全程畅通**（同一次 run 里 jxl / aom / x264 / x265 / vpx 都是 git clone 成功的）。三层对策：
    - ① 多源回退：`FFMPEG_TARBALLS` 数组逐源尝试，加完整性校验，`--max-time 600` 有界。
    - ② `ZPACK_FFMPEG_SEED` 本地种子旁路：`git clone --depth 1 --branch n9.0` 之后打成 tar（排除 .git；RELEASE 文件在，版本检测就没问题），经 cygpath 转成 `/c/` 的形态再喂给 tar——**MSYS 的 tar 把 `C:/` 当成远程主机名**，报 `tar: Cannot connect to C: resolve failed`。
    - ③ 都拿不到就明确报错退出。

    另：DSH 后台包装器的 fd 跨 runtime 交接会坏——niubash 包装到 MSYS2 bash 时 stdio 重定向报 EBADF，`echo: write error: Bad file descriptor` 秒退，内外层重定向都救不了。绕法是用 Node 的 `child_process.spawn(detached)` 直接拉 MSYS2 bash，stdio 挂 Node 的原生 fd，环境变量（PATH / HOME / 代理）显式传。落点：`deps_static.sh:167-197`。
36. **office_oxide 的 git clone 被代理 SSL 中断**（2026-11-22 实测机三跑踩到；AGENTS.md:330）。deps 8/8 全过之后的最后一关：clone 到一半 `SSL_read: unexpected eof`（代理链路抖动，和 #35 同族）。**修法**（AGENTS.md 记提交 8aa0c61）：克隆三次退避重试——失败就 rm -rf 残树，sleep 8 再试；复跑即过。**git 网络操作一律假设可能中断，脚本内的 clone 和下载必须带重试。** 落点：`build_cli/windows/office_static.sh:24-35`。
37. **`build.sh deps` 恒以 exit 1 结束**（2026-11-24 实测机二首跑查明；AGENTS.md:322，那里编成了「35」）。第二个 `case "$MODE"` 没有 `deps)` 分支，落进了 `*)` 的用法分支——云端产线每次都在「✓ office_oxide 静态库就绪」之后退出，**cgo 阶段在 CI 上从来没有执行过**。**修法**：加上 `deps) ;;`。落点：`build.sh:77`。
38. **rustc 1.100 的 std 引用了 `WaitOnAddress` / `WakeByAddressSingle` / `WakeByAddressAll`**（AGENTS.md:323，那里编成了「36」）。MinGW 运行库里只有 `libsynchronization.a` 提供它们（导入自系统出厂的 `api-ms-win-core-synch-l1-2-0.dll`）；rustc 自己链可执行文件时会自动加 `-lsynchronization`，但 staticlib 不带链接指令，最终链接报 50 行 undefined reference。**修法**：`office/linkflags_windows.go` 加 `-lsynchronization`。修后不带任何环境绕过，`build.sh build` 实测通过，产物 83MB。落点：`office/linkflags_windows.go:5-11`。

Windows 完整版对比 brew 版 ffmpeg 还有的缺口（配料待补，AGENTS.md:332）：`drawtext` 滤镜（要 freetype + fribidi + harfbuzz）、`subtitles` 滤镜（要 libass；mac 走 CoreText，免 fontconfig）、`zscale` 滤镜（要 zimg）、`libsvtav1`（AV1 快编）。编码器已经有 202 个，反超 brew 版的 195 个。

### 6.12 验收脚本的平台假设与门禁保护

- **三个脚本都是照着 mac 写的**：`/tmp` 工作目录、`stat -f%z`、`textutil`、`/dev/null`、`/Applications/LibreOffice.app`。在 Windows 上跑要垫片，还会有环境噪音（第 4.6 节）。给脚本加新用例时，先想想这条用例在三个平台上各需要什么外部工具。
- **门禁不许写盘**。刻录不可逆：`verify/verify.sh` 检出刻录机时跳过「真实写入」用例，只跑不碰设备的参数校验用例（`verify/verify.sh:1107-1123`；AGENTS.md:256）。这是 2026-11-24 Windows 接入 IMAPI2 之后补的保护——不补的话，门禁会真的往盘上写。**新增会碰到物理设备的用例时必须沿用这个保护。**
- **门禁默认 `bin/zpack-cli` 是完整版**。verify.sh 的 7z 写入段注释写明「bin/zpack-cli 恒为完整版故不设能力门控」（`:1126`）——产物定案的另一面。
- **「全绿」看的是失败数为 0，不是总数等于某个数**。用例总数会随环境变：verify.sh 有刻录机时少 2 例；verify-media.sh 看有没有 LibreOffice、JXL 夹具有没有生成（第 8 节）。
- **门禁拿 zpack 自己做交叉验证**：JXL 解码链用 `zpack ffmpeg` 自己产夹具来验证（自举，`verify/verify-media.sh:349-357`）；hash 用 shasum 双向互操作；取字段尽量用 `zpack json get`，不靠 python3 或 jq。
- **期望值和引擎文案绑死**。门禁大量用 `grep -q` 匹配 `error_info` 的文案（「输入不存在」「只能单独刻录」「没有可压缩的文件」……）。改引擎里的文案，必须同步改 `verify/verify.sh`（AGENTS.md:348）。

### 6.13 改动时容易破坏的东西

- **动了 `internal/ffmpegcli/libs/`、`office/libs/`、`internal/sevenzipcli/libs/` 里的 `.a`**——哪怕内容没变、只是重新拷了一遍——go 不会重链。要 `go clean -cache`。
- **升配料版本**：mac 改 `ffmpeg_static.sh` 里的 tag / 分支，Windows 改 `deps_static.sh` 的 fetch 参数，**两边要对齐**，而且要换 CI 的缓存 key。两平台对齐是被当作定案的——FFmpeg 从 8.1.2 换成 n9.0 就是为了和 Windows 产线对齐（AGENTS.md:206）。注意 mac 换成 n9.0 之后记着「产线待复跑重验」。
- **改 ffmpeg 的 configure 旗标**：mac 和 Windows 两处都要改。Windows 多出来的旗标各有各的坑（iconv，陷阱 #18；没有 videotoolbox）。
- **升 ffmpeg 大版本**：fftools 的目录结构可能再变，对象清单的规则要跟着看（陷阱 #8）。
- **改 cgo 链接旗标**：Windows 和其它平台是两套文件（`linkflags_windows.go` / `linkflags_other.go`），两边都要顾到。Windows 侧少一个系统库，就是链接期几十行 undefined reference（陷阱 #20、#38）。
- **改直调入口的符号名**（`zpack_ffmpeg_cli_main`、`zpack_7z_main`）：产线的改名步骤、cgo 头文件里的声明、产线的 `nm` 校验，三处要同步。
- **新增引擎包**：照成对文件的分层落位；纯 Go 桩要给出明确的「需完整版」文案；单测在引擎不可用时 `t.Skip`。
- **新增验收用例**：用例数被 AGENTS.md 当作基线记录，加完要同步 AGENTS.md 和本 spec 的第 8 节。会碰物理设备的用例必须有第 6.12 节说的保护。
- **新增依赖外部工具的用例**：三个平台上外部工具的可用性不一样。取字段用 `zpack json get`，少用 python3。
- **改 `bin/zpack-cli` 的路径或名字**：三个验收脚本、genenv、`refresh-engine.sh`、GUI 测试的 `repoEngine` 都写死了路径。
- **在放着完整版的仓库里跑交叉编译命令**（AGENTS.md:175）或通用构建命令（AGENTS.md:174）：产物同名，会把完整版覆盖掉。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 二进制入口 | `cmd/zpack/main.go` | 薄 main，只调 `cli.Execute(version)`；`version` 由 `-X main.version` 注入，缺省 `dev`（`:15`） |
| 命令装配 | `cli/root.go` | 只做装配，不含命令实现。命令面按功能拆在 `cli/` 的子包里：`archive`、`burn`、`crypto`、`image`、`media`、`office`、`pdf`、`tools`；公共底座是 `internal/cliutil/` |
| 模块定义 | `go.mod`、`go.sum` | module `github.com/guohub8080/zpack`，`go 1.27.3`；`go-astiav` 是直接依赖 |
| mac 产线 | `build_cli/mac/build.sh`、`ffmpeg_static.sh`、`office_static.sh`、`sevenzip_static.sh`；`prepare.sh` 是 POC 遗留 | 职责与开关见第 2.3 节 |
| Windows 产线 | `build_cli/windows/build.sh`、`deps_static.sh`、`office_static.sh` | 同上 |
| Linux 产线占位 | `build_cli/linux/build.sh`、`prepare.sh` | 只打印「待首跑验证」 |
| 产线说明 | `build_cli/README.md` | 已经过时，见第 9 节 |
| POC 遗留 | `build_cli/poc/`（自带 `go.mod`，是独立 module） | 2026-11-04 用来证明 cgo + libav 路线成立的一次性脚手架 |
| 云端产线 | `.github/workflows/build-windows.yml` | 见第 4.5 节 |
| 入库忽略 | `.gitignore` | `bin/`、`build_cli/*/src/`、`build_cli/*/prefix/`、`internal/ffmpegcli/libs/`、`internal/sevenzipcli/libs/`、`office/libs/`、`realEnvTest/` 等 |
| ffmpeg 直调层 | `internal/ffmpegcli/`：`cli_cgo.go`、`cli_nocgo.go`、`linkflags_other.go`、`linkflags_windows.go` | `libs/` 不入库，由产线拷进来 |
| 7z 直调层 | `internal/sevenzipcli/`：`cli_cgo.go`（`cgo && darwin`）、`cli_stub.go`（其余情况） | 没有 Windows 版的真实现 |
| office 的 cgo 薄壳 | `office/convert_cgo.go`、`convert_nocgo.go`、`linkflags_other.go`、`linkflags_windows.go` | `libs/` 不入库 |
| 媒体引擎的 cgo 分层 | `media/engine_cgo.go`、`media/stub_nocgo.go`、各功能的 `*_nocgo.go` 桩 | `clip`、`cover`、`insertframe`、`merge`、`screenshot`、`speed` 各有 `cgo` / `!cgo` 两个文件 |
| 图片引擎的 cgo 分层 | `image/hdr_cgo.go`、`hdr_nocgo.go`、`heic.go`、`heic_nocgo.go` | HDR 与 HEIC 两条通道 |
| 7z 写入的引擎接线 | `core/sevenz.go`、`core/sevenz_test.go` | 单测在引擎不可用时跳过 |
| 行为门禁 | `verify/verify.sh` | 见第 8.1 节 |
| 媒体门禁 | `verify/verify-media.sh` | 见第 8.2 节 |
| 图片门禁 | `verify/verify-image.sh` | 见第 8.3 节 |
| 真实体感语料 | `scripts/genenv/main.go`、`scripts/genenv/README.md` | 见第 4.7 节 |
| 装机位刷新 | `ui/right-click/macos/refresh-engine.sh` | 见第 4.2 节、第 6.9 节 |
| GUI 与右键菜单的构建脚本 | `ui/right-click/macos/build-xcode.sh`、`quickaction.sh`、`ui/gui/install.sh`（第一代 Objective-C 版的 `build.sh` 已于 2026-11-24 删除） | 归 [gui.md](gui.md) |

## 8. 验收用例

**用例数怎么数的**：数脚本里 `assert_equal` 和 `report_pass` 的调用点，每个调用点计一次；`verify/verify-image.sh` 里的循环体和被调用多次的函数按运行时展开。**这些数是静态数出来的，本次核对没有实跑**，脚本最后打印的总数以第一次实跑为准。

### 8.1 `verify/verify.sh`——362 例（检出刻录机的机器 360 例）

| 分节 | 行号 | 用例数 | 内容 |
|---|---|---|---|
| zip：行为用例 | 29-237 | 56 | compress 并行打包、extract 并行解压、GBK 修复、zip-slip 防护、128KB 分块并行、HSA |
| tar 及其后的各格式 | 238-742 | 165 | 标题只写了 tar / tar.gz，实际还包含下面那张表里的 15 个分块 |
| hash | 743-816 | 23 | 标准测试向量、`--expect`、`--output` / `--check` 与 shasum 的双向互操作 |
| `--temp-dir` | 817-854 | 10 | convert、compress、extract、bench 的临时目录落点 |
| agent 自描述 | 855-877 | 10 | 子命令 `--help` 守卫、skill 文档的输出与安装 |
| pdf 命令族 | 878-947 | 18 | info / merge / extract；含 11-24 新增的 `--quiet` 1 例（`:945` 起） |
| pdf resize 统一页面尺寸 | 948-996 | 9 | |
| pdf resize 文档型拦截 | 997-1041 | 10 | |
| pdf 旋转 | 1042-1078 | 18 | |
| json 命令族 | 1079-1095 | 13 | |
| burn 光盘刻录 | 1096-1105 | 7 | 都是不碰设备的用例 |
| burn 多输入 | 1106-1125 | 4 | 其中 2 例在检出刻录机时跳过（`:1115-1123`） |
| 7z 写入 | 1126-1163 | 14 | **需要 7z 引擎**。纯 Go 产物和 Windows 完整版在这一段会红 |
| 数值参数严格校验 | 1164-1175 | 5 | 11-24 新增 |

238-742 段里的 15 个分块：

| 分块 | 行号 | 用例数 |
|---|---|---|
| tar 写入与往返（PAX 长名、`--level`、确定性） | 238-310 | 22 |
| tar 智能解压、tar.bz2、跳过 / 覆盖语义 | 311-338 | 9 |
| tar 安全防护（tar-slip、符号链接逃逸、设备 / 管道条目、硬链接落地） | 339-376 | 6 |
| 7z / rar 真读取（夹具入库 `core/testdata`） | 377-391 | 7 |
| 伪头明确报错 | 392-402 | 4 |
| 单流格式与 `.001` 分卷 | 403-442 | 9 |
| 遗留编码格式（.Z / uuencode / xxencode） | 443-456 | 7 |
| 容器格式（ar / asar / iso） | 457-471 | 8 |
| lzip + wim | 472-483 | 7 |
| cab（存储 / MSZIP / LZX+E8） | 484-497 | 7 |
| 分卷创建（`--volume-size` 与模板） | 498-562 | 18 |
| `.zpackignore` 排除规则 | 563-584 | 5 |
| base64 | 585-595 | 4 |
| crypto（RSA、genkey 覆盖守卫、password / uuid、`--charset`） | 596-675 | 35 |
| list / test / info、加密回归（含 11-24 新增的「多条目加密包 + 错密码」2 例，`:727` 起） | 676-742 | 17 |

- 2026-11-24 新增的 8 例：多条目加密包错密码 2 例（`:727` 起）、数值严格校验 5 例（`:1164` 起）、pdf `--quiet` 1 例（`:945` 起）。AGENTS.md:181 写的「预期 354/354」是加这 8 例之前的数（354 + 8 = 362）。
- 对环境和产物的硬依赖：python3、perl、unzip、shasum、`tar -cjf`、gzip、bzip2、`ln -s`、`readlink`、`od`、BSD 形态的 `stat -f%z`，以及 7z 引擎。
- 有刻录机的机器会打印一行跳过提示，并少跑 2 例（`:1122`）。

### 8.2 `verify/verify-media.sh`——最多 158 例

| 分节 | 行号 | 用例数 | 备注 |
|---|---|---|---|
| convert：remux | 39-48 | 4 | |
| convert：音频容器抽取 | 49-55 | 3 | |
| convert：webm 全编 | 56-60 | 2 | |
| compress：目标 40% | 61-67 | 3 | |
| compress：低于下限被拒 | 68-72 | 2 | |
| compress：纯音频 | 73-78 | 2 | |
| 预设 | 79-103 | 9 | |
| media info：体检 + 建议 | 104-113 | 6 | |
| 正交指令 `--screen` / `--codec` | 114-134 | 10 | |
| 旋转烤入 + 字幕保轨 | 135-162 | 9 | |
| 几何变换命令 | 163-188 | 10 | |
| 烤入语义补全 | 189-205 | 5 | |
| 竖拍 × preset 降档回归 | 206-213 | 2 | |
| SAR | 214-222 | 3 | |
| bake 一级命令 | 223-244 | 10 | |
| 截图 | 245-270 | 10 | |
| HDR 图片解码（EXR / Radiance / AVIF） | 271-332 | 6 | 夹具和像素校验用 python3 |
| `zpack ffmpeg` 透传 + JXL 解码链 | 333-358 | 10 | 其中 1 例（`:352-357`，JXL 解码尺寸）只在 JXL 夹具生成成功时跑 |
| office 转换 | 359-387 | 14 | 夹具靠 macOS 的 `textutil` |
| office `--try-*` 外部引擎 | 388-403 | 6 个调用点 | 装了 LibreOffice 跑 4 例（`:391-393` 加 `:401`），没装跑 3 例（`:397-398` 加 `:401`） |
| office 全格式矩阵 | 404-444 | 10 | 只在装了 LibreOffice 时跑 |
| audio 命令族 | 445-481 | 17 | |
| 契约与退化 | 482-491 | 5 | |

- **运行时的总数**：无条件执行的是 144 例。装了 LibreOffice、JXL 夹具也生成了，是 158 例；没装 LibreOffice 是 147 例（JXL 夹具生成成功）或 146 例。
- AGENTS.md:182 写的「128 用例」已经对不上现在的脚本。
- **前置**：完整版 `bin/zpack-cli`；系统的 `ffmpeg` / `ffprobe`；python3；macOS 的 `textutil`（office 段）；可选的 LibreOffice。AVIF 夹具还用到了系统 ffmpeg 的 `libsvtav1`（`:320`）。

### 8.3 `verify/verify-image.sh`——113 例

| 分节 | 行号 | 用例数 | 备注 |
|---|---|---|---|
| convert | 16-30 | 12 | 5 种输出格式各 2 例（循环，`:17-21`），再加 2 例 |
| compress | 31-37 | 2 | |
| info | 38-44 | 4 | 含 3 个字段的循环（`:41-43`） |
| resize | 45-65 | 7 | |
| 物理尺寸缩放（cm / inch + DPI） | 66-101 | 11 | |
| 几何变换 | 102-124 | 9 | `img_xval` 被调 5 次，每次 1 例（`:103-114`）；像素比对用 `md5 -q` |
| merge 拼接 | 125-147 | 10 | |
| EXIF Orientation | 148-195 | 5 | 夹具用 python3 手写 APP1 |
| compress `--target-size` | 196-249 | 25 | |
| image new | 250-291 | 23 | 两个循环：5 种格式的纯色（`:276-280`）、3 种格式拒绝透明（`:285-288`） |
| 退化 | 292-300 | 5 | |

93 个调用点展开成 113 例。AGENTS.md:183 写的「65 用例」已经对不上现在的脚本。「纯 Go 即可」说的是产物（image 零 cgo）；脚本本身要系统 `ffmpeg` / `ffprobe` 造夹具和校验，还要 python3、BSD 形态的 `stat -f%z`、`md5 -q`、`od`。

### 8.4 `go test` 与 `go vet`

- `go test ./...`：表驱动单测，只测纯逻辑函数；`run*` 函数直接 `os.Exit`，不测（AGENTS.md:177）。7z 写入的两个单测在引擎不可用时跳过（`core/sevenz_test.go:15-17`、`:73-75`）。
- `go vet ./...` 加 `GOOS=windows`、`GOOS=linux` 各一遍：三平台编译验证（AGENTS.md:178）。为什么 Windows 上还得真跑一遍，见第 6.8 节。
- GUI 的无头测试（`ui/gui`）归 [gui.md](gui.md)；它们认的是仓库里的 `bin/zpack-cli(.exe)`（AGENTS.md:258、:274）。

### 8.5 手动验证的方法

| 要验证什么 | 怎么做 |
|---|---|
| 是不是完整版 | 见第 3.3 节 |
| mac 产物没有挂 brew 的动态库 | `otool -L bin/zpack-cli \| grep -c /opt/homebrew`，应该是 0（`build_cli/mac/build.sh:19`） |
| Windows 产物的 import 表 | `objdump -p bin/zpack-cli.exe \| grep "DLL Name"`，逐个对照 `build_cli/windows/build.sh:69` 的白名单 |
| 编码器全家都链进去了 | `zpack ffmpeg -hide_banner -encoders \| grep -cE "libx264\|libx265\|libmp3lame\|libopus\|libvpx\|libjxl\|libaom"`（云端冒烟的同一条命令，`.github/workflows/build-windows.yml:80`）；`zpack ffmpeg -version` 的输出里要有 `libjxl`（`verify/verify-media.sh:337`） |
| 零释放 | 跑一轮 `zpack ffmpeg …` 转码，前后对比临时目录和 CWD 的文件清单，只应该多出产出物；转码进行中系统里不该有 ffmpeg 子进程（AGENTS.md:213；门禁用例在 `verify/verify-media.sh:344-348`） |
| 7z 写入的互操作 | `zpack compress <目录> --output x.7z`，再用 homebrew 的 7zz 测一遍（AGENTS.md:235 记 7zz 26.05 交叉读通过） |
| 刻录真机（只读） | `ZPACK_IMAPI2_LIVE_TEST=1 go test ./internal/imapi2 -run TestLiveReadOnly`（AGENTS.md:277） |
| 装机位刷新是否生效 | `pluginkit -mAvv -i <appex-id>` 看 Path 是不是 `/Applications` 下的真身；`codesign --verify --deep --strict` 看签名（AGENTS.md:390） |

## 9. 未结项与已知限制

### 9.1 没做的

- **Linux 完整版产线**：只有两个占位脚本。AGENTS.md 里关于 Linux 只记了 `GOOS=linux go vet` 这一条编译验证；7z 写入、media、office 内置引擎在 Linux 上都没有产线，不可用。
- **Windows 的 7z 写入引擎**。`internal/sevenzipcli` 只有 darwin 的 cgo 实现；Windows 完整版里 `compress --output x.7z` 报「需完整版」，verify.sh 的 7z 写入段在 Windows 上必然是红的（AGENTS.md:235 记「Windows 产线待铺（M2）」）。要做的不只是铸库：`RunCaptured` 用 `golang.org/x/sys/unix` 的 `Dup` / `Dup2` 接管 fd 2（`internal/sevenzipcli/cli_cgo.go:24`、`:59`、`:81-85`），Windows 上得另写一套。
- **这带来的后果**：铁律 1 说「验收全绿才算实现完」，可 verify.sh 在 Windows 完整版上现在不可能全绿——7z 写入段必红，另有 Git Bash 的环境噪音。门禁的全绿基线只能在 mac 完整版上取得。
- **mac 的 7z 直调库只编 arm64**（`build_cli/mac/sevenzip_static.sh:6`、`:24`、`:47`）：没有 x86_64 mac 的产物，也没有通用二进制。
- **mac 产线换成 FFmpeg n9.0 之后没有复跑重验**（AGENTS.md:206）。
- **云端产线的 cgo 阶段没有跑通的记录**：`build.sh deps` 的退出码问题修了之后（`build_cli/windows/build.sh:77`），CI 是否完整跑通过没有记载。
- **Windows 完整版对比 brew 版的缺口**：`drawtext`、`subtitles`、`zscale` 三个滤镜和 `libsvtav1` 没有配料（AGENTS.md:332）。
- **两个门禁在 Windows 上跑不完**：verify-media.sh 依赖 macOS 的 `textutil`，不改脚本就不可能在 Windows 上全绿；verify-image.sh 要系统 ffmpeg。两台 Windows 实测机的记录里都没有它们跑通的记载。
- **verify.sh 在 Windows 上的 20 例环境噪音没有逐条消解**：实测记录里没有留下是哪 20 例。
- **CI 每次都重编 office_oxide**：prefix 缓存里没有 `office/libs`（`.github/workflows/build-windows.yml:61-65`）。
- **云端冒烟没有断言**：三条冒烟命令都带 `|| true`（`.github/workflows/build-windows.yml:78-80`）。
- **产线不产出许可证材料**（第 6.7 节）。
- **两条不带形态说明的构建命令**：AGENTS.md:174 的通用构建和 AGENTS.md:175 的交叉编译，产物分别和 mac、Windows 的完整版同名。跑过之后要把完整版重铸回来，AGENTS.md 里没有提这件事。

### 9.2 已知不对、暂时没改的（读脚本和代码发现的，都没有实跑验证）

**mac 产线脚本的三处路径 / 判断问题**（互相叠加；2026-11-24 读脚本发现，提交 3a3cb8d 已修——只做了语法检查和路径逻辑模拟，**没有在 mac 上实跑**，下次在 mac 上跑产线时请复核）：

1. `build_cli/mac/build.sh` 查 office 静态库时少写 `../../`：查的是 `build_cli/mac/office/libs/liboffice_oxide.a`，而 `office_static.sh` 把库写到仓库根的 `office/libs/`（`office_static.sh:7`、`:29-31`）。守卫永远不成立，每次都会去调 `office_static.sh`。同一文件里 7z 的守卫写的是 `$ROOT/../../internal/…`，是对的。**已改为 `$ROOT/../../office/libs/…`。**
2. `build_cli/mac/ffmpeg_static.sh` 把直调库拷到 `$ROOT/internal/ffmpegcli/libs/`，而 `$ROOT` 定义的是 `build_cli/mac`——实际落点是 `build_cli/mac/internal/ffmpegcli/libs/`。cgo 包取的是 `${SRCDIR}/libs`，也就是仓库根下的 `internal/ffmpegcli/libs/`（`internal/ffmpegcli/linkflags_other.go:8`）。Windows 的脚本用的是 `ZPACK_ROOT`，是对的（`build_cli/windows/deps_static.sh:16`、`:229-230`）。**已改为拷到仓库根。**
3. `build_cli/mac/office_static.sh:15` 的判断恒为真：`-f` 对目录恒假，取反之后恒真，`||` 就短路了。结果是每次运行都 `rm -rf` 源码树、重新 clone、重新 cargo build。**已改为 `-d`。**

修复前三条合起来的后果：在 mac 上跑一遍 `build.sh`，office_oxide 每次都要联网重新 clone、重新编译，机器上必须装着 cargo；直调库则拷错了地方——仓库根的 `internal/ffmpegcli/libs/` 里要是没有早先放进去的旧库，链接会失败（找不到 `zpack_ffmpeg_cli` 这个库）；要是有，产线也不会去更新它。所以 AGENTS.md 记的「FFmpeg 升到 n9.0 后产线待复跑重验」在修复前的脚本上复跑也验不到新库。修完第一次在 mac 上跑产线时，建议 `rm -rf build_cli/mac/internal`（旧脚本拷错位置留下的目录）。

**注释和遗留文件里的旧话**：

- `build_cli/mac/build.sh:2`「ffmpeg CLI 尾注嵌入」——尾注嵌入方案已退役；`:4`「约 15-25 分钟」，AGENTS.md:204 记的是约 25 分钟。
- `build_cli/mac/ffmpeg_static.sh:2-3`「FFmpeg 8.1.2」「ffmpeg CLI 二进制（供嵌入）」——实际是 n9.0（`:121-124`），CLI 是进程内直调（`:141`）。
- `build_cli/mac/prepare.sh`：POC 阶段的遗留脚本，装的是 brew 的 ffmpeg **共享库**，和完整版产线无关，但 README 还把它列在 mac 布局里。
- `build_cli/windows/office_static.sh:38` 的注释说「清除会污染宿主模块包图的 Go 源后拷产物」，脚本里其实没有清除动作（也不需要——源码树在临时目录里，退出就清）。
- `core/sevenz.go:1` 和 `internal/sevenzipcli/cli_cgo.go:3` 的注释写「DISABLE_RAR=1 剥 RAR」，实际是归档阶段手动剥（`build_cli/mac/sevenzip_static.sh:6-7`、`:51-58`）——DISABLE_RAR 在 Alone2 下根本不起作用。
- `cmd/zpack/main.go:3-5` 的包注释还是旧布局（`internal/core`、`internal/cli`）。
- `.gitignore:23` 的 `build_cli/windows/fftools-cli/` 已经没有脚本引用：直调库现在落在 `internal/ffmpegcli/libs/`，由 `.gitignore:17` 覆盖。

**其它**：

- `scripts/genenv/main.go:47` 找的是 `bin/zpack-cli`，不带 `.exe`，提示语也只提 mac 产线（`:167`）——Windows 上媒体族恒被跳过。
- `cli/image/image.go:359-374`：`image info` 遇到 HEIC 时输出的 note 写着「像素解码需完整版（CGO 构建）」，而这段代码不区分构建形态，完整版下也是这一句。它归 [image.md](image.md) 管；在这里登记是因为它会影响「是不是完整版」的判断（第 3.3 节）。

### 9.3 代码与文档不一致的地方

以脚本和代码为准。

**AGENTS.md**

| 位置 | 文档说的 | 实际 |
|---|---|---|
| AGENTS.md:16 | 「当前实测静态全量约 130MB」 | 两套产线记录的是 mac 约 48MB（AGENTS.md:204）、Windows 83MB（AGENTS.md:275）。130MB 指什么不明 |
| AGENTS.md:16 | ~~「许可证红线：GPL 库（x264/x265）不捆绑」~~ | **已解决（2026-11-24）**：铁律 2 改为「许可证不是硬约束」，项目按 GPL-2.0-or-later 发布（`LICENSE`），与两套产线都链 x264 / x265 的事实一致 |
| AGENTS.md:181 | verify.sh「预期 354/354」 | 现在 362 例（有刻录机 360） |
| AGENTS.md:181 | verify.sh「只测 bin/zpack-cli（纯 Go 产物）」 | verify.sh 含 7z 写入 14 例，要完整版才绿（`verify/verify.sh:1126`） |
| AGENTS.md:182 | verify-media.sh「128 用例」 | 最多 158 例，随 LibreOffice 和 JXL 夹具浮动 |
| AGENTS.md:183 | verify-image.sh「65 用例」 | 113 例 |
| AGENTS.md:206 | x265 取自 Bitbucket `multicoreware/x265_git`，「code.videolan.org 镜像要认证」 | mac 脚本仍从 `code.videolan.org` clone（`build_cli/mac/ffmpeg_static.sh:78`）；只有 Windows 脚本用的是 Bitbucket |
| AGENTS.md:200 | CLI 面「`cli/pdf.go`」 | 现在是 `cli/pdf/pdf.go` |
| AGENTS.md:237、:242-244 | `cli/burn_windows.go`、`cli/burn.go` | 现在是 `cli/burn/burn_windows.go`、`cli/burn/burn.go`、`cli/burn/burn_other.go` |
| AGENTS.md:282 | `cli/list.go` | 现在是 `cli/archive/list.go` |
| AGENTS.md:339-344（目录结构） | `cli/` 是「root.go（装配）+ commands.go」；`internal/` 里有「output」；build_cli 的 mac 只列了 `ffmpeg_static.sh` 和 `build.sh`；verify 一行没有 verify-image.sh | `cli/` 是 `root.go` 加八个功能子包（`cli/root.go:1-3`）；输出契约层在 `internal/stdout/`，另有 `internal/cliutil/`；mac 产线有四个正式脚本加一个遗留的 `prepare.sh`；verify 有三个脚本 |
| AGENTS.md:305（陷阱 #20） | office 的 Windows 链接库是 `-lntdll -luserenv -lws2_32 -lbcrypt -lcrypt32` | 实际旗标还有 `-lsynchronization`（另一条陷阱记的）、`-lgdi32 -luser32 -lm -lpthread`（`office/linkflags_windows.go:11`） |
| AGENTS.md:322-323 与 :328-330 | 陷阱编号出现了两个 #35、两个 #36 | 见第 6.11 节开头的编号说明 |
| AGENTS.md:404 | 「依赖（go.mod，全纯 Go）」列了一份清单 | go.mod 里还有 go-astiav（cgo）、pdfcpu、nativewebp、go-ole、x/image 等（`go.mod:6-24`） |

**`build_cli/README.md`**（整份都停在 POC 阶段）

| 位置 | 文档说的 | 实际 |
|---|---|---|
| `build_cli/README.md:10-12` | mac 只有 `prepare.sh` 和 `build.sh`，`build.sh`「当前产出 poc/transcode」 | mac 有 `build.sh`、`ffmpeg_static.sh`、`office_static.sh`、`sevenzip_static.sh`，另有遗留的 `prepare.sh`；产物是 `bin/zpack-cli` 完整版 |
| `build_cli/README.md:13-15` | Windows 是「zig cc 全静态，待首跑验证」，有 `prepare.sh` 做「zig cc 交叉编译 libav-win64」 | 没有 `windows/prepare.sh`；三个脚本，走原生 MinGW，2026-11-22 已全线跑通（AGENTS.md:294） |
| `build_cli/README.md:16-18` | Linux 有 `prepare.sh` 和 `build.sh`，「同 windows 模式」 | 是两个只打印提示的占位脚本 |
| `build_cli/README.md:38-39` | 「主 go.mod 不引入 go-astiav」 | go.mod:8，go-astiav 是直接依赖 |
| `build_cli/README.md:41` | 「zig cc 全静态预期仅 kernel32 系」 | 完整版的 import 表按 22 个名称模式的白名单校验（`build_cli/windows/build.sh:69`），实测 32 个系统 DLL |
| `build_cli/README.md:19-23` | poc 是一次性脚手架，「media 正式合入后退役」 | `build_cli/poc/` 还在仓库里 |
| `build_cli/README.md:4` | 「本目录承载三平台产线」 | 实际只有 mac 和 Windows 两套 |

### 9.4 【待确认】清单

1. **三个验收脚本运行时的用例总数**：362（有刻录机 360）、158 / 147 / 146、113，都是静态数出来的，没有实跑。
2. **mac 完整版现在的体积**：AGENTS.md:179、:204 的约 48MB，是不是已经算上了 office_oxide（增重约 12MB）和 7z。
3. **mac 产线三处路径 / 判断问题的实际表现**（第 9.2 节）：直调库拷错了地方、office 的守卫恒不成立、clone 的判断恒为真。要在 mac 上实跑才能确认。
4. **云端产线修后是否完整跑通过**（cgo 阶段）。
5. **「7z write 13 例」差的那一例**：实测记录说红 13 例；按现在的代码读，这一段 14 例里应该红 12 例、2 例碰巧通过（第 4.6 节）。要看实跑输出。
6. **lame、opus、libvpx 的许可证**：AGENTS.md 和脚本里都没写，配料表是按各项目公开发布的许可证填的。
7. **Linux 上三个验收脚本能不能跑**：没有实测记录。
8. **AGENTS.md:174 那条通用构建命令的产物形态**：命令不指定 CGO 开关，跟着环境的缺省值走，AGENTS.md 没说明它算哪种形态。

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从脚本与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 修复：mac 产线三处路径 / 判断错误（office 库守卫少写 ../../、直调库拷错目录、office_oxide 目录判断用了 -f；见 9.2，未在 mac 上实跑） | 3a3cb8d |
| 2026-11-24 | 许可证：项目按 GPL-2.0-or-later 发布（新增 `LICENSE`）；AGENTS.md 铁律 2 与格式策略③不再把许可证当硬约束；本 spec 的 6.1 / 6.7 / 9.3 同步 | daa5d90 |
| 2026-11-24 | 引擎产物改名 zpack-cli（用户定案）：两条产线出 `bin/zpack-cli[.exe]`，三个门禁脚本与 genenv 改测它，全文 `bin/zpack` 指产物处同步（旧名产物各脚本仍认）。产线脚本本身没有重跑——本机是 Windows，完整版产线要数小时 | 机械改名随并行会话的提交 21d1e69 进了库；说明性改动提交后回填 |
