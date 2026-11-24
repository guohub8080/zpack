# 文档格式转换（office）

> 把旧格式 Office 文档（.doc / .xls / .ppt）和现代 WPS 文件（.wps / .et / .dps）转成 OOXML（.docx / .xlsx / .pptx）：缺省用编进二进制的 office_oxide 引擎，也可以借本机安装的 MS Office / WPS / LibreOffice 在后台转换。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

把**一份**旧格式文档转成对应的现代格式（OOXML）。只有一个子命令 `office convert`，输入一个文件、输出一个文件，方向固定是「旧 → 新」。

| 输入扩展名 | 所属的族 | 唯一合法的输出 |
|---|---|---|
| `.doc`、`.wps` | doc | `.docx` |
| `.xls`、`.et` | xls | `.xlsx` |
| `.ppt`、`.dps` | ppt | `.pptx` |

扩展名不分大小写。`.wps` / `.et` / `.dps` 指的是**现代 WPS Office 存出来的文件**——它们和 MS 的旧格式是同一种二进制结构（office/formats.go:4-10）。WPS 早年那种私有二进制格式不在支持范围内。

有四个引擎，用户选一个，**互斥、也不会自动回退**：

| 选择方式 | 引擎 | 怎么实现 |
|---|---|---|
| 不带 `--try-*`（缺省） | 内置 office_oxide | Rust 核心，以 C ABI 静态链进 zpack 二进制，不依赖本机装了什么；**只有完整版才有** |
| `--try-msoffice` | 本机的 MS Office | Windows 走 COM 自动化；macOS 走 AppleScript；Linux 没有 |
| `--try-wps` | 本机的 WPS Office | Windows 走 COM 自动化；macOS / Linux 没有可用的自动化接口 |
| `--try-libreoffice` | 本机的 LibreOffice | 三个平台都是起一个 `soffice --headless` 子进程 |

**不做什么：**

- 不做「新 → 旧」：目标只认 `.docx` / `.xlsx` / `.pptx`。
- 不跨族：`.doc` 只能出 `.docx`，不能出 `.xlsx`。
- 不转 PDF，不和图片互转。
- 一次只处理一个文件：不批量、不合并、不拆分。
- 不支持 WPS 私有二进制格式（老版 WPS、Microsoft Works 的 `.wps`）。
- 不编辑内容。宏、嵌入对象、修订记录这些转换后是否保留，取决于所用引擎，zpack 不做保证【待确认：没有核对过】。
- 不随包安装任何套件。三个外部引擎都是「借」本机已装的软件，没装就报错。

**使用场景：** 别人发来一份 `.doc` / `.xls`，本机软件打不开或显示不对；手里的 WPS 文件要交给只用 Office 的人；内置引擎转出来的排版不满意，换本机的 Office 再转一次。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack office convert <输入> <输出> [--try-msoffice\|--try-wps\|--try-libreoffice] [--libreoffice-path <路径>]` | 转换一份文档。两个位置参数：先是输入，再是输出（cli/office/office.go:41） |

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--try-msoffice` | 用本机的 MS Office 转换 | 布尔旗标，没有值。和另外两个 `--try-*` 互斥：给了两个及以上（同一个给两次也算）→ 报错 | 不给：用内置引擎 |
| `--try-wps` | 用本机的 WPS Office 转换 | 同上 | 同上 |
| `--try-libreoffice` | 用本机的 LibreOffice 转换 | 同上 | 同上 |
| `--libreoffice-path <路径>` | 显式告诉 zpack LibreOffice 装在哪 | `soffice` 可执行文件的完整路径；在 Windows 上还可以给 LibreOffice 根目录或 `program` 目录（**目录形态只在 Windows 上有效**，见 4.1）。值两头的空格和双引号会被去掉。后面没有值、或下一个参数以 `-` 开头 → 报错。给了两次，后一次生效 | 不给：走探测链（见 4.1） |
| `--help` / `-h` | 打印帮助，exit 0 | — | — |

没有数值参数，没有 `--quiet`（这个命令没有过程日志）。

**环境变量**（探测链的第一段，见 4.1）：`ZPACK_OFFICE_LIBREOFFICE_PATH`、`ZPACK_OFFICE_WORD_PATH`、`ZPACK_OFFICE_WPS_PATH`。真正改变探测结果的只有第一个（另两个的作用见 6.4）；`--libreoffice-path` 的值会在进程内写进它，所以旗标优先于环境变量。

**几条规则：**

- **引擎选择是互斥的，也没有任何自动回退。** 不带 `--try-*` 就只用内置引擎，内置引擎失败不会去借本机软件；带了 `--try-libreoffice` 就只用 LibreOffice，没装也不会退回内置引擎。
- **子命令 `convert` 必须是第一个不以 `-` 开头的参数。** `--libreoffice-path` 这类带值的旗标不能写在 `convert` 前面——它的值会被当成子命令名，报用法错误。
- **内置引擎与外部引擎的检查顺序不同**（所以同一组错误输入，报出来的错可能不一样）：

  | 顺序 | 内置引擎（office/convert_cgo.go:43） | 外部引擎（office/engines.go:35） |
  |---|---|---|
  | 1 | 目标扩展名 | 输入文件存在 |
  | 2 | 族匹配 | 输入扩展名 |
  | 3 | 输入文件存在 | 目标扩展名 |
  | 4 | 输入扩展名、读文件、识别 | 族匹配 |
  | 5 | 转换 | 调用套件 |

- **内置引擎认文件内容，外部引擎只认扩展名。** 内置引擎对 `.wps` / `.et` / `.dps` 会校验文件头的魔数；外部引擎把路径原样交给套件，由套件自己认。
- **不认识的旗标被忽略**，但它的值如果不以 `-` 开头，会被当成位置参数（于是「位置参数不是两个」而报用法错误）。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

## 3. 输出契约

成功时是标准信封，exit 0。内置引擎和外部引擎的载荷只差一个 `engine` 字段。下面列的是 data 载荷，顺序就是结构体的声明顺序。

内置引擎（`ConvertResult`，office/convert_cgo.go:28）：

```json
{"is_success":true,"data":{"input":"E:/docs/合同.doc","output":"E:/docs/合同.docx","source_format":"doc","target_format":"docx","size_bytes":20480}}
```

外部引擎（`ExternalConvertResult`，office/engines.go:25——多了 `engine`）：

```json
{"is_success":true,"data":{"input":"E:/docs/合同.doc","output":"E:/docs/合同.docx","engine":"libreoffice","source_format":"doc","target_format":"docx","size_bytes":20480}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `input` / `output` | string | 输入、输出文件的绝对路径 |
| `engine` | string | 用了哪个外部引擎：`msoffice` / `wps` / `libreoffice`。**内置引擎的输出里没有这个字段** |
| `source_format` | string | 源格式，见下表 |
| `target_format` | string | 目标格式，就是输出扩展名去掉点：`docx` / `xlsx` / `pptx` |
| `size_bytes` | int | 输出文件的大小 |

`source_format` 的取值（`formatLabel`，office/engines.go:282；内置引擎在 office/convert_cgo.go:99、:107 拼出同样的结果）。MS 扩展名报族名；WPS 扩展名报「族名(别名)」，告诉调用方它是按哪个 MS 格式解析的、原本叫什么：

| 输入扩展名 | `source_format` |
|---|---|
| `.doc` | `doc` |
| `.wps` | `doc(wps)` |
| `.xls` | `xls` |
| `.et` | `xls(et)` |
| `.ppt` | `ppt` |
| `.dps` | `ppt(dps)` |

**stderr**：这个命令自己不输出任何过程日志，只有装配层的开始日志 `[YYYY-MM-DD HH:MM:SS] zpack office 开始执行`。外部套件（soffice、osascript）自己的输出被捕获，不会透传到终端——只有失败时才拼进 `error_info`。

**失败**：`{"is_success":false,"error_info":"..."}` + exit 1，见 4.3。

## 4. 行为细则

### 4.1 正常路径

#### 命令面（所有引擎共用）

1. 子命令分派到 `convert`，解析参数：三个 `--try-*` 记数并记下引擎名，`--libreoffice-path` 的值写进环境变量 `ZPACK_OFFICE_LIBREOFFICE_PATH`，其余不以 `-` 开头的参数是位置参数。
2. 校验：`--try-*` 是否互斥 → 位置参数是否恰好两个。
3. 输入、输出路径转成绝对路径。
4. 带了 `--try-*` → 走外部引擎层。没带 → 纯 Go 构建报「需完整版」，完整版走内置引擎。
5. 输出 JSON。

#### 内置引擎（office_oxide）

1. 按第 2 节的顺序检查目标扩展名、族匹配、输入文件存在。
2. 打开文档（`openDocument`，office/convert_cgo.go:81）：
   - `.doc` / `.xls` / `.ppt`：按**路径**打开，office_oxide 根据扩展名决定按什么格式解析。
   - `.wps` / `.et` / `.dps`：把整个文件读进内存，核对前 8 个字节是不是 OLE2 复合文档的魔数（`D0 CF 11 E0 A1 B1 1A E1`），是的话按对应的 MS 格式（doc / xls / ppt）从**字节**打开。
3. 另存到输出路径。
4. 输出 JSON（`source_format` 按上面的表）。

```bash
zpack office convert 合同.doc 合同.docx                 # 内置引擎
zpack office convert 报表.et 报表.xlsx                  # source_format: xls(et)
```

#### `--try-libreoffice`（三个平台同一条路）

1. 探测 LibreOffice 的启动命令，四段依次试，取第一个命中的（`detectLibreOfficeCmd`，office/engines.go:91）：

   | 段 | Windows | macOS | Linux |
   |---|---|---|---|
   | ① 环境变量（含 `--libreoffice-path`） | 值是目录：先找 `<目录>\program\soffice.exe`，再找 `<目录>\soffice.exe`；值是文件：直接用 | 规则相同，但目录形态只会去找 `soffice.exe`——所以在这两个平台上**目录形态等于无效**，只有文件形态有用 | 同 macOS |
   | ② PATH | `soffice` | `soffice` | `soffice` |
   | ③ 平台动态解析 | 注册表 `HKLM` / `HKCU` 下的 `SOFTWARE\LibreOffice\UNO\InstallPath`（含 `WOW6432Node`）→ `ProgramFiles` / `ProgramFiles(x86)` / `LOCALAPPDATA` 下的 `LibreOffice\program\soffice.exe` → 从 `C:` 到 `Z:` 逐个盘符找 `X:\LibreOffice\program\soffice.exe` | 用 Spotlight（`mdfind`）按包 ID `org.libreoffice.script` 找 `.app`，取其中的 `Contents/MacOS/soffice` | 有 `flatpak` 命令 → `flatpak run org.libreoffice.LibreOffice`；否则有 `snap` 命令 → `snap run libreoffice` |
   | ④ 常见路径 | （列的都是 Unix 路径，Windows 上不命中） | `/Applications/LibreOffice.app/Contents/MacOS/soffice` | `/usr/bin/soffice`、`/usr/lib/libreoffice/program/soffice`、`/opt/libreoffice/program/soffice` |

2. 在系统临时目录建一个空目录，当作这次运行的独立配置目录（profile）。
3. 运行（office/engines.go:233-246）：

   ```
   <soffice> --headless --norestore -env:UserInstallation=file:///…/zpack-lo-profile-xxxx --convert-to <目标扩展名> --outdir <输出目录> <输入>
   ```

4. soffice 的产出固定叫 `<输出目录>/<输入基名>.<目标扩展名>`。zpack 把它**改名**成用户给的输出路径（office/engines.go:255）。
5. 删掉临时配置目录。

```bash
zpack office convert 合同.doc 合同.docx --try-libreoffice
zpack office convert 合同.doc 合同.docx --try-libreoffice --libreoffice-path "D:\Apps\LibreOffice"
```

#### `--try-msoffice`

**Windows**（office/engine_windows.go:89）：按输入扩展名选应用。

| 输入扩展名 | COM 对象 | 另存用的 FileFormat |
|---|---|---|
| `.xls`、`.et` | `Excel.Application` | 51 |
| `.ppt`、`.dps` | `PowerPoint.Application` | 24 |
| 其它（`.doc`、`.wps`） | `Word.Application` | 16（`SaveAs2`） |

每一族的流程相同：创建应用对象（经注册表找到安装位，装在哪个盘都行；创建失败就是没装）→ 把窗口设为隐藏、关掉警告弹窗 → 以**只读**方式打开输入（不动源文件）→ 另存到输出路径 → 关闭文档 → 退出应用。

**macOS**（office/engine_msoffice_darwin.go:19）：**只有 Word 一条路**，用 AppleScript 按包 ID `com.microsoft.Word` 寻址（和安装位置、本地化名称无关）：

1. 记下 Word 当前是否在运行。
2. `launch`（启动但不激活，不抢焦点）→ 打开输入 → 把当前文档按默认格式（`.docx`）另存到输出路径 → 关闭不保存。
3. 转换前 Word 没在运行就退出它；本来就在运行就留着，不打扰用户。

**Linux**：没有 MS Office，直接报错。

#### `--try-wps`

**Windows**（office/engine_windows.go:101）：和 MS Office 同构的 COM 接口。

| 输入扩展名 | COM 对象 |
|---|---|
| `.xls`、`.et` | `KET.Application` |
| `.ppt`、`.dps` | `KWPP.Application` |
| 其它（`.doc`、`.wps`） | `KWPS.Application` |

流程、FileFormat 与 MS Office 相同。

**macOS / Linux**：WPS 在这两个平台上没有公开的自动化接口。zpack 会探测是否装了 WPS，然后**明确报错**并建议在 WPS 里另存，或改用 `--try-msoffice` / `--try-libreoffice`。

### 4.2 边界与退化输入

| 情况 | 行为 |
|---|---|
| 扩展名大小写（`.DOC`、`.Xlsx`） | 不区分 |
| `.doc` → `.xlsx`、`.xls` → `.pptx` 等跨族 | 拒绝：族不匹配 |
| 输入没有扩展名，或是别的扩展名（`.pdf`、`.txt`…） | 拒绝：输入暂只支持那六种 |
| 输出不是 `.docx` / `.xlsx` / `.pptx` | 拒绝：目标暂只支持那三种 |
| 输出文件已存在 | 覆盖 |
| 输出路径就是输入路径 | 不可能出现：同一族的输入输出扩展名不同 |
| `.wps` 不是 OLE2 文件（老版 WPS、Microsoft Works） | 内置引擎：拒绝，并提示「请用 WPS Office 另存为对应 MS 格式」。外部引擎：不做这项检查，直接交给套件 |
| `.wps` 是 OLE2 文件，但按 doc 解析不了 | 内置引擎：拒绝，提示「可能是 WPS 私有格式（用 WPS Office 另存）」 |
| `--libreoffice-path` 指向不存在的路径 | **静默忽略**，当作没给，继续走探测链 |
| `--libreoffice-path` 给了，但引擎不是 `--try-libreoffice` | 没有影响 |
| 用户正开着 LibreOffice | 互不影响：zpack 用的是独立的临时配置目录 |
| 用户正开着 Word（macOS） | 互不影响：转换后不退出本来就在运行的 Word |
| 用户正开着 WPS / PowerPoint（Windows） | 【待确认】这两个是单实例应用，创建对象时可能挂到用户已开的实例上，之后的「隐藏窗口」「退出」会不会影响用户正在用的窗口，没有验证过 |
| soffice 起不来或卡住 | 没有超时，zpack 会一直等 |
| WPS 第一次被自动化启动 | 冷启动可能要 180 秒以上，属正常；第二次起是秒级（AGENTS.md:288） |
| 带密码的旧格式文档 | 【待确认】没有核对过各引擎的表现 |
| 输出目录不存在 | 转换失败，报引擎的错误【待确认：没有实跑】 |
| 纯 Go 构建、不带 `--try-*` | 报「需完整版」（见 5） |

### 4.3 错误处理

所有失败都是 `{"is_success":false,"error_info":"..."}` + exit 1。

#### 命令面（两条路共用，cli/office/office.go）

| 场景 | `error_info` | 出处 |
|---|---|---|
| 没给子命令，或不是 `convert` | 三行用法：`用法: office convert <输入> <输出> [--try-msoffice\|--try-wps\|--try-libreoffice]` + 两行说明 | :36 |
| `--libreoffice-path` 后面没有值，或下一个参数以 `-` 开头 | `--libreoffice-path 需要一个路径参数（LibreOffice 根目录或 soffice.exe 全路径均可）` | :63 |
| `--try-*` 给了两个及以上 | `--try-msoffice / --try-wps / --try-libreoffice 三旗标互斥（一次选一个引擎）` | :72 |
| 位置参数不是恰好两个 | 三行用法：`用法: office convert <输入> <输出> [--try-msoffice\|--try-wps\|--try-libreoffice] [--libreoffice-path <路径>]` + 两行说明 | :75 |

前两条在解析参数时报出，早于后两条。

#### 纯 Go 构建用内置引擎

| 场景 | `error_info` |
|---|---|
| 不带 `--try-*`，且二进制是纯 Go 构建 | `office 转换需完整版（CGO 构建 + office 产线）`（office/convert_nocgo.go:9） |

这个判断排在所有文件检查之前：纯 Go 构建下，输入不存在、扩展名不对，报的都是这一句。

#### 内置引擎

| 场景 | `error_info` | 出处 |
|---|---|---|
| 目标扩展名不对 | `目标暂只支持 .docx/.xlsx/.pptx，收到: <扩展名>` | office/convert_cgo.go:48 |
| 跨族 | `跨族转换不支持：<输入扩展名> → <输出扩展名>（<族名> 族只出 <合法输出扩展名>）` | :52 |
| 输入不存在 | `路径不存在: <绝对路径>` | :55 |
| 输入扩展名不支持 | `输入暂只支持 .doc/.wps/.xls/.et/.ppt/.dps，收到: <扩展名>` | :84 |
| WPS 扩展名的文件读不了 | `读取失败: <系统错误原文>` | :90 |
| WPS 扩展名，但文件头不是 OLE2（或不足 8 字节） | `非现代 WPS 文件（WPS 私有二进制不支持——请用 WPS Office 另存为对应 MS 格式）` | :93 |
| WPS 扩展名、文件头对，但按 MS 格式解析失败 | `WPS 文件按 <族名> 解析失败——可能是 WPS 私有格式（用 WPS Office 另存）` | :97 |
| MS 扩展名的文件打不开（损坏、不是那种二进制格式） | `无法读取 <扩展名>——文件损坏或非 <族名> 二进制格式` | :105 |
| 另存失败 | `转换失败（office_oxide 错误码 N）——文件可能损坏或含不支持的特性` | :67 |

另存失败时 zpack 会删掉输出路径上的文件（office/convert_cgo.go:66，本意是清半成品）——如果那里原本有一个旧的结果文件，它也会被删。

#### 外部引擎：共用的前置检查（office/engines.go）

| 场景 | `error_info` | 出处 |
|---|---|---|
| 输入不存在 | `路径不存在: <绝对路径>` | :37 |
| 输入扩展名不支持 | `输入暂只支持 .doc/.wps/.xls/.et/.ppt/.dps，收到: <扩展名>` | :41 |
| 目标扩展名不对 | `目标暂只支持 .docx/.xlsx/.pptx，收到: <扩展名>` | :45 |
| 跨族 | `跨族转换不支持：<输入扩展名> → <输出扩展名>（只出 <合法输出扩展名>）` | :49 |
| 套件跑完却没有产出文件 | `引擎未产出文件: <系统错误原文>` | :68 |

注意跨族的文案两条路不一样：内置是「（doc 族只出 .docx）」，外部是「（只出 .docx）」。

#### 外部引擎：`--try-libreoffice`

| 场景 | `error_info` | 出处 |
|---|---|---|
| 探测不到 LibreOffice | `未检测到 LibreOffice（<安装提示>）——也可设置 ZPACK_OFFICE_LIBREOFFICE_PATH 显式指定` | office/engines.go:226 |
| soffice 退出码非零 | `LibreOffice 转换失败: <进程错误>（<soffice 的输出>）` | :252 |
| soffice 正常退出，但预期位置上没有产物、改不了名 | `LibreOffice 产物归位失败: <系统错误原文>` | :257 |

安装提示按平台（`installHint`，office/engines.go:263）：macOS 是 `brew install --cask libreoffice`，Windows 是 `Microsoft Office / WPS Office / LibreOffice 官网`，Linux 是 `apt install libreoffice 或包管理器等价命令`。这条提示只提了环境变量，没提 `--libreoffice-path` 旗标。

#### 外部引擎：`--try-msoffice` / `--try-wps` 在 Windows（COM）

两族的错误文案是同一套模板，`<套件名>` 是 `Microsoft Word` / `Microsoft Excel` / `Microsoft PowerPoint` / `WPS 文字` / `WPS 表格` / `WPS 演示`：

| 场景 | `error_info` |
|---|---|
| 没装（创建应用对象失败） | `未检测到 <套件名>（COM 创建 <ProgID> 失败）——安装后重试` |
| Word 族：接口查询失败 | `<套件名> 接口查询失败: <错误原文>` |
| 隐藏窗口 / 关警告失败 | `设置隐藏失败: <错误原文>` / `设置静默失败: <错误原文>` |
| 取文档集合失败 | `取 Documents 失败` / `取 Workbooks 失败` / `取 Presentations 失败` + `: <错误原文>` |
| 打开输入失败 | `<套件名> 打开文档失败` / `打开工作簿失败` / `打开演示失败` + `: <错误原文>` |
| 另存失败 | `<套件名> 另存失败: <错误原文>`（Word 族多一句「（文件可能含不支持的特性）」） |
| 关闭文档失败 | `关闭文档失败` / `关闭工作簿失败` / `关闭演示失败` + `: <错误原文>` |
| 退出应用失败 | `退出 <套件名> 失败: <错误原文>` |

出错路径上没有调用「关闭 / 退出」：创建应用对象之后的任何一步失败，函数都直接返回错误，只释放 COM 引用。被隐藏启动的应用进程会不会就这样留在后台，没有验证过【待确认】。

#### 外部引擎：`--try-msoffice` / `--try-wps` 在 macOS、Linux

| 平台与引擎 | 场景 | `error_info` |
|---|---|---|
| macOS + msoffice | 探测不到 Word | `未检测到 Microsoft Word——安装后重试，或设置 ZPACK_OFFICE_WORD_PATH 显式指定` |
| macOS + msoffice | AppleScript 执行失败（包括拿 Word 去开 Excel / PowerPoint 文件） | `Word 自动化失败: <进程错误>（<osascript 的输出>）——文件可能含不支持的特性` |
| macOS + wps | 装了 WPS | `检测到 WPS（macOS）但该平台暂无自动化接口——请在 WPS 中另存，或用 --try-msoffice/--try-libreoffice` |
| macOS + wps | 没装 WPS | `未检测到 WPS（macOS）` |
| Linux + msoffice | 总是 | `Linux 无 Microsoft Office——用 --try-libreoffice` |
| Linux + wps | 装了 WPS | `检测到 WPS（Linux）但该平台暂无自动化接口——请在 WPS 中另存，或用 --try-libreoffice` |
| Linux + wps | 没装 WPS | `未检测到 WPS（Linux）` |

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 内置引擎（office_oxide） | 完整版可用。产线 `build_cli/windows/office_static.sh` | 完整版可用。产线 `build_cli/mac/office_static.sh` | 没有产线脚本（`build_cli/linux/build.sh` 自己标注「待首跑验证」），拿不到静态库 |
| `--try-msoffice` | COM：Word / Excel / PowerPoint 三族 | AppleScript：**只有 Word**。Excel / PowerPoint 文件没有实现，会拿 Word 去开而失败 | 明确报错 |
| `--try-wps` | COM：文字 / 表格 / 演示三族 | 探测后报「无自动化接口」 | 探测后报「无自动化接口」 |
| `--try-libreoffice` | `soffice.exe`。探测：环境变量 → PATH → 注册表 → Program Files 等目录 → 盘符扫描 | `soffice`。探测：环境变量 → PATH → Spotlight → 固定路径 | `soffice`。探测：环境变量 → PATH → flatpak / snap → 发行版路径 |
| 外部引擎用到的系统设施 | `go-ole`（纯 syscall，无 cgo） | `osascript`、`mdfind`（系统自带） | 无 |
| office_oxide 的链接旗标 | `office/linkflags_windows.go`：没有 `-ldl`，多一组 Windows 系统库 | `office/linkflags_other.go`：`-lm -ldl -lpthread` | 同 macOS |

**实测状态**（AGENTS.md:288、:290 的记录）：

| 引擎 | Windows | macOS | Linux |
|---|---|---|---|
| 内置 office_oxide | 产线已接进完整版；转换行为本身的实测记录没有找到【待确认】 | 同左【待确认】 | 不适用 |
| `--try-msoffice` | Word、Excel 已实测通过（2026-11-08）；**PowerPoint 未实测** | **未实测**（开发机没有 Word） | 不适用 |
| `--try-wps` | 文字、表格已实测通过（同上）；**演示未实测** | 不支持 | 不支持 |
| `--try-libreoffice` | 已实测通过（隔离 profile 修复之后） | 没有实测记录【待确认】 | 没有实测记录【待确认】 |

**纯 Go 构建与完整版的差别：**

| | 纯 Go 构建（`CGO_ENABLED=0`） | 完整版（cgo 构建） |
|---|---|---|
| 不带 `--try-*` | 报 `office 转换需完整版（CGO 构建 + office 产线）`，exit 1 | 内置 office_oxide 转换 |
| 带 `--try-*` | **可用**——外部引擎层是纯 Go | 可用，行为相同 |
| 参数校验（用法、互斥、`--libreoffice-path` 缺值） | 可用 | 可用 |

对外交付的二进制恒为完整版（AGENTS.md「产物定案」），所以用户正常不会撞到「需完整版」；它只会出现在开发期的纯 Go 循环里。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 两层引擎，各管一头

- **内置引擎**是 office_oxide v0.1.16：Rust 写的文档转换核心，以 C ABI 编成静态库链进 zpack（MIT / Apache-2.0，AGENTS.md:233）。静态链入对应的是 AGENTS.md 铁律 2「产物单文件自包含（裸机可跑）」：转换不依赖本机装了什么。
- **外部引擎层**是纯 Go（office/engines.go:1），三个平台的套件都是起进程或走系统调用。它必须在纯 Go 构建下也能用——那是纯构建下唯一的出路（cli/office/office.go:80 的注释「外部引擎层纯 Go——双构建皆可用」）。**所以这一层不能引入任何 cgo 依赖。**
- **为什么借本机软件**：原厂转换的保真度更高（cli/tools/skill.md:167）。内置引擎转出来不满意时，用户可以换引擎再转一次。
- **为什么手搓 cgo 薄壳，而不用 office_oxide 的官方 Go 包**：那个包靠环境变量传链接参数，不满足「裸 `go build` / `go test` 不依赖产线环境变量」。薄壳用 `${SRCDIR}/libs` 的方式引用静态库和头文件（office/convert_cgo.go:11、office/linkflags_*.go），和 ffmpeg 直调层是同一个模式（AGENTS.md:233）。
- office_oxide 的 C 接口远不止用到的四个函数（`office/libs/office_oxide.h`）：除了打开 / 另存，还有取纯文本、转 Markdown / HTML、编辑、直接写 xlsx / pptx 等。以后要扩功能，先看这个头文件，不用急着换库。

### 6.2 族匹配是强制的

- office_oxide 在跨族请求下不会报错，而是**尽力转出一份错误的产物**（office/convert_cgo.go:50 的注释）。所以 zpack 在调用它之前先校验族，宁可拒绝（AGENTS.md:71）。
- 族表在 `office/formats.go`，两条路径（内置、外部）各校验一次。两条路的报错文案不完全一样（见 4.3）。
- GUI 那边做了同样的预判：目标格式不让用户选，按输入扩展名自动定（`officeTargetOf`，ui/gui/main.go:3552）。改族表时要一起改它。

### 6.3 `.wps` / `.et` / `.dps` 有两种东西

- 一种是**现代 WPS Office 存出来的**：和 MS 旧格式同一种二进制结构（OLE2 复合文档），只是扩展名不同。这是 zpack 支持的。
- 另一种是 **WPS 私有二进制**（老版本 WPS、Microsoft Works 的 `.wps`）。不支持。
- 内置引擎的处理（office/convert_cgo.go:79-108）：先把文件整个读进来，核对开头 8 个字节是不是 OLE2 的魔数；是，就按对应的 MS 格式（doc / xls / ppt）从**字节**打开——这样 office_oxide 就不会因为扩展名是 `.wps` 而不认（这一步的用意是从注释推出来的：WPS 走「字节级」、MS 扩展名走「路径级」）。`source_format` 报 `doc(wps)` 这种「族名(别名)」，是为了让调用方知道它被当成了什么格式、原本叫什么。
- 报错分两级，帮用户判断是哪一种情况：文件头不对 → 「非现代 WPS 文件」；文件头对但解析不了 → 「可能是 WPS 私有格式」。
- 外部引擎不做魔数校验——路径原样交给套件，套件自己认。

### 6.4 探测层不写死路径（用户定案）

- 原则（office/engines.go:82-88 的注释）：不同用户的安装位置不同，不能写死路径。探测分四段：① 环境变量显式覆盖 → ② PATH → ③ 平台动态解析 → ④ 常见路径兜底。返回的是「一条命令行」而不是一个路径，因为 flatpak / snap 是包装命令。
- **任意安装位寻址**（2026-11-09 定案，AGENTS.md:71）：Windows 上的 MS Office / WPS 根本不需要探测路径——COM 的 `CreateObject` 经注册表自己找到安装位，「装哪个盘都行」，创建失败就等于没装。Windows 上的 LibreOffice 优先查注册表里安装器写的 `InstallPath`（自定义安装位如 `D:\Apps` 也会写；用户级安装在 HKCU、32 位在 WOW6432Node——office/loprobe_windows.go），猜默认目录只作兜底，再加一轮盘符扫描，照顾解压即用的绿色版（26 次 Stat，微秒级）。
- **显式指路的双通道**：`--libreoffice-path` > 环境变量 `ZPACK_OFFICE_LIBREOFFICE_PATH` > 探测链。实现很直接——旗标的值在进程内写进那个环境变量（cli/office/office.go:59-66），探测层只认环境变量。
- **三个容易踩的点**：
  - 目录形态只认 Windows 的布局（去找 `soffice.exe`）。在 macOS / Linux 上给一个目录，等于没给。
  - 给的路径不存在时**静默忽略**，继续往下探测——用户以为指路生效了，实际用的是探测到的另一个安装。
  - Linux 上 flatpak / snap 包装器排在发行版路径**之前**：PATH 里没有 `soffice`、但机器上装了 `flatpak` 命令时，会选 flatpak 包装器，而不是 `/usr/lib/libreoffice/program/soffice`。
- 另外两个环境变量的实际作用很小：`ZPACK_OFFICE_WORD_PATH` 只在 macOS 的探测里被读，而且读的只是「有没有给」——值本身不被使用（AppleScript 按包 ID 寻址），Windows 不读它（COM 自解析），Linux 直接报错；`ZPACK_OFFICE_WPS_PATH` 只影响 macOS / Linux 上「检测到 WPS 但无接口」和「未检测到 WPS」两种文案选哪一个。

### 6.5 COM 必须钉在同一个线程上（2026-11-08 Windows 首实测，bug #4）

- COM 的初始化是**按操作系统线程**算的。Go 的协程会被调度器挪到别的线程上执行，挪过去之后调用 COM 就报「尚未调用 CoInitialize」（AGENTS.md:283）。
- 修法是 `withCOMSession`（office/engine_windows.go:21-29）：`LockOSThread` 把协程钉在当前线程 → `CoInitializeEx`（STA）→ 执行转换 → 配对的 `CoUninitialize` → 解除钉定。返回 `S_FALSE`（本线程早已初始化）也算成功。
- 三族 COM 调用（Word、Excel、PowerPoint；MS 与 WPS 共用）全都包在这个骨架里。**以后加任何 COM 调用，都必须包进去。**
- 另存用的 FileFormat 常量别凭印象改：Word `SaveAs2` 用 16，Excel `SaveAs` 用 51，PowerPoint `SaveAs` 用 24（AGENTS.md:71）。

### 6.6 LibreOffice：隔离的配置目录，和文件 URI 的坑

- **为什么要隔离**：zpack 起的是无界面的 soffice。它如果和用户正开着的 LibreOffice 共用同一份配置目录，两边会冲突。所以每次运行都在临时目录里建一份空的配置，用完删掉（office/engines.go:222-232）。
- **坑**（2026-11-08 Windows 首实测，bug #5；AGENTS.md:284）：配置目录要以 `-env:UserInstallation=file://…` 的形式传给 soffice。手拼这个 URI 时，Windows 路径的反斜杠会造出畸形地址，结果是配置初始化失败——libpng Write Error，甚至弹出「bootstrap.ini 已损坏」的对话框。
- **修法**：用 `url.URL{Scheme: "file", Path: …}` 构造，并把卷符路径补上前导 `/`（office/engines.go:239-243）。路径里的空格等字符也顺带被正确转义。
- 临时目录建不出来时不中止：退化成不带隔离参数，用用户默认的配置继续转（office/engines.go:230-245）。
- **产物的名字是 soffice 定的**：它只能指定输出**目录**，产出固定叫 `<输出目录>/<输入基名>.<目标扩展名>`。zpack 再把它改名成用户要的输出路径（office/engines.go:254-258）。`--convert-to` 的目标格式跟着输出扩展名走（:246）。
- 没有超时。soffice 如果卡在某个对话框上，zpack 会一直等。

### 6.7 一个改了格式名写死的 bug（bug #8）

外部引擎层最早把 `target_format` 写死成 `"docx"`。全格式矩阵加进来之后，`.xls → .xlsx` 的结果里格式名就错了。2026-11-08 在 Windows 上实测 Excel 族时顺手修掉，改成按输出扩展名推导（office/engines.go:75-77；AGENTS.md:288）。

### 6.8 链接与产线

- **两套链接旗标**：`-ldl` 是 Unix 的概念，MinGW 没有这个库，所以 Windows 的旗标单独放在 `office/linkflags_windows.go`（AGENTS.md 陷阱 #20）。
- **Rust 的静态库不带链接指令**，要使用它的 Go 一侧自己补：`-lntdll -luserenv -lws2_32 -lbcrypt -lcrypt32` 等；rustc 1.100 的标准库还引用了 `WaitOnAddress` 一族，得再加 `-lsynchronization`（AGENTS.md 陷阱 #36；office/linkflags_windows.go:3-7）。升 Rust 或 office_oxide 的版本后链接报 undefined reference，先想到这一条。
- **产线**（office_oxide v0.1.16）：`build_cli/mac/office_static.sh` 是浅克隆 → `cargo build --release --lib` → 清理 → 把 `.a` 和头文件拷进 `office/libs/`。Windows 版 `build_cli/windows/office_static.sh` 把克隆和构建都放在临时目录里、退出即清，克隆带三次退避重试（AGENTS.md 陷阱 #36）。
- **克隆之后必须删掉上游的 `examples/`、`cmd/` 和散落的 `*.go`**：它们引用上游自己的模块路径，留在仓库里会被宿主的 `go build ./...` 扫到，污染包图——踩过（AGENTS.md:233）。
- 产线是幂等的：`office/libs/` 里有库就跳过。要升级 office_oxide 的版本，得先手动删掉它。

### 6.9 怎么验证产物

- 不要用「xlsx 里有没有 sharedStrings 表」来判断产物好坏：office_oxide 写出的 xlsx 用的是内联字符串，本来就没有这张表。可靠的交叉验证是让 soffice 把产物再读一遍（转成 csv、或检查 pptx 里的 XML）（AGENTS.md:71）。
- WPS 第一次被自动化拉起来时是冷启动，可能要 180 秒以上，不是卡死；第二次起就是秒级（AGENTS.md:288）。

### 6.10 GUI 耦合

- GUI 的「Office 转换」页（`pageOffice`，ui/gui/main.go:3478）经 `Service.StartConvert` 起 `zpack office convert <输入> <输出>`，引擎下拉四项：自动 / MS Office / WPS / LibreOffice，后三项分别追加 `--try-msoffice` / `--try-wps` / `--try-libreoffice`（ui/gui/main.go:3537-3544）。GUI 不传 `--libreoffice-path`。
- GUI 读的是 `data.output`、`data.size_bytes`，失败时读 `error_info`（ui/gui/service.go:677-716）。输入文件在拖入时按文件内容判格式（`rejectIfForeign(path, "office")`，ui/gui/sniff.go:82）。
- 「自动」就是内置引擎。产物定案保证装机产物是完整版，所以用户不会撞到「需完整版」。

### 6.11 这个功能没有自动化验收

`verify/verify.sh` 里没有任何 office 用例，`office/` 和 `cli/office/` 下也没有测试文件。原因是推测【待确认】：二进制的 `.doc` / `.xls` / `.ppt` 夹具没法像 PDF 那样用脚本现造，外部引擎又依赖本机装了什么。这与「每个命令都必须在 verify.sh 有用例」的铁律相悖，见 9。

### 6.12 改动时容易破坏的东西

| 别动的东西 | 谁在依赖 |
|---|---|
| 两份构建实现必须同名同形：`Available` / `Convert` / `ConvertResult` / `ErrNeedsFullBuild`（office/convert_cgo.go 与 office/convert_nocgo.go） | 少任何一项，另一种构建就编不过 |
| 外部引擎层保持纯 Go | 纯 Go 构建下 `--try-*` 是唯一出路 |
| `engine` 的三个取值、`source_format` 的「族名(别名)」写法 | 调用方按它们显示；没有验收守着 |
| 族表 `OFFICE_FAMILY` 与 GUI 的 `officeTargetOf` | 两份手写、必须一致 |
| COM 的 ProgID 与 FileFormat 常量 | 改错就是转出错误格式或直接失败 |
| soffice 的产出命名规则（`<输入基名>.<目标扩展名>`） | 「改名归位」那一步靠它 |

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面 | `cli/office/office.go` | 子命令分派；参数解析、引擎选择、`--libreoffice-path`、结果输出 |
| | `cli/office/doc.go` | 包文档 |
| 引擎：内置 | `office/convert_cgo.go` | `//go:build cgo`。cgo 薄壳、`Convert`、`openDocument`、`ErrNeedsFullBuild` |
| | `office/convert_nocgo.go` | `//go:build !cgo`。纯构建桩，`Available()` 恒 false |
| 引擎：格式矩阵 | `office/formats.go` | `LEGACY_OFFICE_INPUTS`（扩展名 → 格式名）、`OFFICE_FAMILY`（族匹配）；两种构建共用 |
| 引擎：外部 | `office/engines.go` | `ConvertWithEngine`、探测链、LibreOffice 调用、`installHint`、`formatLabel` |
| | `office/engine_windows.go` | `//go:build windows`。MS Office 与 WPS 的 COM 调用、`withCOMSession` |
| | `office/engine_msoffice_darwin.go` | `//go:build darwin`。Word 的 AppleScript；macOS 上 WPS 的报错 |
| | `office/engine_other.go` | `//go:build !windows && !darwin`。Linux 上两个引擎的报错 |
| | `office/loprobe_windows.go`、`office/loprobe_other.go` | LibreOffice 注册表探测；非 Windows 平台的占位 |
| 链接旗标 | `office/linkflags_windows.go`、`office/linkflags_other.go` | `#cgo LDFLAGS` |
| 静态库 | `office/libs/liboffice_oxide.a`、`office/libs/office_oxide.h` | 产线产物，不入库（`.gitignore`:19） |
| 产线 | `build_cli/mac/office_static.sh`、`build_cli/windows/office_static.sh` | office_oxide v0.1.16 → 静态库 |
| 第三方 | `github.com/go-ole/go-ole` v1.3.0（go.mod:11）、`golang.org/x/sys` v0.49.0（go.mod:23） | COM 调用、注册表读取 |
| 装配 | `cli/root.go` | 挂到根命令（:71） |
| GUI 耦合 | `ui/gui/main.go`（:3478 `pageOffice`、:3552 `officeTargetOf`）、`ui/gui/sniff.go`（:82） | GUI 的 Office 转换页 |
| 测试 | 无 | `office/` 与 `cli/office/` 下都没有测试文件 |
| 验收脚本 | 无 | `verify/verify.sh` 里没有 office 用例（见 8） |

## 8. 验收用例

**自动化用例：没有。** `verify/verify.sh` 里搜不到任何 office 相关的断言（`grep -i 'office\|wps\|libreoffice\|soffice\|docx'` 零命中）。specs/README.md 的目录表里，office 的验收一栏登记的是 `verify/verify.sh`，这与事实不符（见 9）。

也就是说，这个功能的任何回归——内置引擎转不出来、外部引擎找不到套件、`source_format` 报错——目前都不会被门禁发现。

> **【等用户提供夹具】2026-11-24 用户定案**：office 的测试夹具由用户自己造；夹具到位之前**不补** office 的自动化验收用例，agent 不要自行造夹具、也不要先补下面「建议的最小集合」。这是对「验收全绿才算实现完」铁律的一项书面豁免，豁免期到夹具到位为止。

### 手动验证

每一步都检查三件事：退出码、stdout 的 JSON 字段、产物能否被别的软件打开。

```bash
# 1. 参数校验（不需要任何文件，纯 Go 构建也能跑）
zpack office; echo $?                                                   # 用法，1
zpack office convert a.doc b.docx --try-wps --try-wps; echo $?          # 互斥报错，1
zpack office convert a.doc b.docx --libreoffice-path; echo $?           # 缺值报错，1
zpack office convert Z:\no_such\a.doc Z:\no_such\a.docx --libreoffice-path D:\Apps\LibreOffice --try-libreoffice; echo $?   # 路径不存在，1（不会去启动 soffice）

# 2. 族匹配（不需要真实文档：找一个存在的任意文件当输入即可触发前置校验）
zpack office convert 某文件.doc 某文件.xlsx --try-libreoffice; echo $?  # 跨族拒绝，1

# 3. 内置引擎（需要完整版二进制；zpack-dev 这类纯构建会报「需完整版」）
zpack office convert 旧文档.doc 新文档.docx                             # source_format: doc
zpack office convert 报表.et 报表.xlsx                                  # source_format: xls(et)

# 4. 外部引擎（本机装了哪个就试哪个）
zpack office convert 旧文档.doc 新文档.docx --try-libreoffice           # engine: libreoffice
zpack office convert 旧文档.doc 新文档.docx --try-msoffice
zpack office convert 旧文档.doc 新文档.docx --try-wps                   # WPS 首次可能要等两三分钟

# 5. 产物交叉验证（以 xlsx 为例）
soffice --headless --convert-to csv --outdir /tmp/check 新文件.xlsx     # 能读出数据才算数
```

建议补进 verify.sh 的最小集合（至少是不依赖本机软件的那部分）：用法与三种参数错误、跨族拒绝、不支持的扩展名、纯构建下的「需完整版」。

## 9. 未结项与已知限制

**已知不对、暂时没改的：**

- **没有任何自动化验收和单元测试**（见 8）。这与铁律「验收全绿才算实现完」相悖——2026-11-24 用户定案：夹具由用户自己造，到位之前书面豁免（见 8 的标注）。
- **跨族拒绝的文案两条路不一样**：内置是「（doc 族只出 .docx）」，外部是「（只出 .docx）」（office/convert_cgo.go:52、office/engines.go:49）。
- **「未检测到 LibreOffice」的提示只提了环境变量**，没提 `--libreoffice-path` 旗标（office/engines.go:226）。
- **`--libreoffice-path` 的两个坑**：目录形态在 macOS / Linux 上无效；路径不存在时静默忽略（office/engines.go:93-106）。
- **macOS 上 `--try-msoffice` 只有 Word**：`.xls` / `.ppt` 输入没有分派，会拿 Word 去开而失败（office/engine_msoffice_darwin.go:19）。
- **Linux 上 WPS 的探测有两套**：报错那一步只查两个固定路径（office/engine_other.go:18），不看 PATH 和环境变量；`detectWpsApp`（office/engines.go:194）查得更全。WPS 在 PATH 里但不在那两个路径上时，报的是「未检测到 WPS（Linux）」。
- **COM 的出错路径不调用「关闭 / 退出」**（office/engine_windows.go:38-85 等三个函数）：被隐藏启动的应用进程会不会留在后台，没有验证【待确认】。
- **Excel 和 PowerPoint 两条 COM 路径忽略了接口查询的错误**（`app, _ := appObj.QueryInterface(...)`，office/engine_windows.go:125、:172）；Word 那条检查了。
- **`installHint` 的 wps / msoffice 分支是死代码**——只有 LibreOffice 路径调用它（office/engines.go:226、:263）。
- **内置引擎另存失败时会删掉输出路径上原有的文件**（office/convert_cgo.go:66）。
- **一处可能崩溃的写法**：内置引擎在另存成功后 `outputInfo, _ := os.Stat(output)` 紧接着取 `outputInfo.Size()`（office/convert_cgo.go:69、:75）。另存返回成功而文件不在时会是空指针崩溃——退出码不是 0 / 1，stdout 也没有 JSON，破坏输出契约。外部引擎那一路有检查（office/engines.go:66-69）。

**没做的：**

- 新 → 旧（`.docx` → `.doc`）、输出 PDF、批量、合并。
- WPS 私有二进制格式。
- 带密码的文档。
- Linux 的 office_oxide 产线。
- GUI 传 `--libreoffice-path` 的入口。
- macOS 上 Excel / PowerPoint 的自动化。

**未实测的路径**（AGENTS.md:290）：

- Windows：`PowerPoint.Application` / `KWPP.Application`（即 `.ppt` / `.dps` 的外部引擎转换）。
- macOS：`--try-msoffice` 的 AppleScript 路径（开发机没有 Word）。

**代码与文档不一致的地方：**

| 文档 | 文档怎么说 | 代码实际 |
|---|---|---|
| AGENTS.md:71 | 成功输出示例里有 `"engine":"..."`，没说哪种引擎才有 | 只有外部引擎的输出有 `engine`；内置引擎没有这个字段 |
| AGENTS.md:71 命令列 | `office convert <输入> <输出> [--try-msoffice\|--try-wps\|--try-libreoffice]` | 还有 `--libreoffice-path <路径>`（同行的说明里提到了，命令列漏了） |
| AGENTS.md:71 | 「探测层四段式（env 覆盖→PATH→…）」，三个环境变量并列 | env 覆盖只对 LibreOffice 那条探测真正起作用；另两个见 6.4 |
| AGENTS.md:71 | 输出示例没有 `input` / `output` | 两种输出都有，且排在最前 |
| cli/office/office.go:1、:21 | 「doc/wps → docx」 | 全格式矩阵；`zpack office --help` 的第一行描述因此是过时的 |
| office/convert_cgo.go:4 | 「v1 范围：doc/wps → docx」 | `Convert` 早已是全矩阵（同文件 :42） |
| office/engine_windows.go:88 | 「⚠️ 三路均未实测」 | Word、Excel 两路已于 2026-11-08 实测（AGENTS.md:288）；只有 PowerPoint 未实测 |
| build_cli/mac/office_static.sh:2 | 「doc/wps→docx 引擎」 | 引擎是全矩阵 |
| cli/tools/skill.md:177 | 输出示例没有 `input` / `output` | 都有 |
| specs/README.md:18 | office 的验收是 `verify/verify.sh` | verify.sh 里没有 office 用例 |
| AGENTS.md 铁律 1 | 每个命令 / 行为必须在 `verify/verify.sh` 有用例 | office 一个用例都没有 |

**【待确认】：**

- 内置引擎转换行为的实测记录：AGENTS.md 里能找到的都是外部引擎（三引擎 Word、Excel 族）的实测，office_oxide 自己转 doc / xls / ppt 的通过记录没有找到。
- `--try-libreoffice` 在 macOS / Linux 上、内置引擎对 `.ppt` / `.dps` 的转换，都没有实测记录。
- 宏、嵌入对象、修订记录在转换后是否保留；带密码的旧格式文档各引擎怎么表现；输出目录不存在时的报错文案。
- 用户正开着 WPS / PowerPoint 时走 COM 转换，会不会影响用户正在用的窗口（这两个是单实例应用）。
- 没有自动化验收的原因（6.11 里的推测）。

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 标注：office 夹具由用户自己造，到位之前不补自动化验收（书面豁免，见 8） | —（仅文档） |
