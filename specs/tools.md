# 零依赖消费层（base64 / json / skill）

> 三个让调用方只靠 zpack 一个二进制就能闭环的小命令：`base64` 编解码、`json` 读取 zpack 自己的结果（替代 python3 / jq）、`skill` 输出 / 安装 Agent 技能文档。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

## 1. 功能定义

三个命令互相独立，共同点是**只用标准库、没有引擎包**——命令面就是实现的全部（`cli/tools/`，没有 `core/` 那样的引擎层）。它们存在的理由是同一个：让脚本、Agent、GUI 在消费 zpack 的结果时不必再依赖 python3 或 jq，**单二进制闭环**（AGENTS.md:70，2026-11-08 用户定案）。

| 命令 | 做什么 | 典型场景 |
|---|---|---|
| `base64` | 把文本、文件或标准输入编成 Base64，或把 Base64 解回原始字节；结果打到 stdout 或写入文件 | 内嵌小数据、传输安全编码、在管道里搬运二进制；GUI「Base64」页的引擎 |
| `json get` | 从标准输入读一份 JSON，按「点分段 + 数组下标」的路径取出**一个**值，裸打印到 stdout | 脚本里取 `data.archive`、`data.file_count` 这类字段 |
| `json ok` | 从标准输入读 zpack 的信封 JSON，按 `is_success` 决定退出码（true → 0，false → 1），**stdout 不输出任何内容** | 管道里一行 `&&` / `\|\|` 判断成败 |
| `skill` | 把内嵌在二进制里的 SKILL.md 全文打到 stdout；`--install` 把它安装成 Agent 技能 | 让消费端 Agent「安装即学会」zpack 的用法 |

**不做什么：**

- `base64`：只认标准字母表（`A-Z a-z 0-9 + /`，`=` 补位）。不支持 URL-safe 字母表、不做 MIME 的 76 字符折行、不做 base32 / hex；解码是严格的（必须带补位，`\r` `\n` 以外的空白都算非法）。
- `json`：不是通用 JSON 处理器。只读 stdin、只取一个值；没有过滤、改写、流式处理；键名里含 `.` 的字段无法寻址；数字一律按 float64 解析（见 9）。
- `skill`：不联网、不比较已安装版本的差异（`--install` 直接覆盖）；文档是手工维护的摘要，不是从各命令的 `--help` 自动生成的。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack base64 encode [文本] [--file 路径] [--output 路径]` | Base64 编码（cli/tools/base64.go:93） |
| `zpack base64 decode [Base64] [--file 路径] [--output 路径]` | Base64 解码（cli/tools/base64.go:107） |
| `zpack json get <字段路径>` | stdin 读 JSON，取出路径指向的值并裸打印（cli/tools/json.go:74） |
| `zpack json ok` | stdin 读信封 JSON，`is_success` → exit 0 / 1（cli/tools/json.go:125） |
| `zpack skill [--install [目录]]` | 输出 SKILL.md 全文；带 `--install` 时安装（cli/tools/skill.go:37） |

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--file <路径>`（base64） | 输入取自文件（二进制安全，整个文件读进内存） | `--file` 后面没有值 → `--file 缺少值`；文件读不了 → `无法读取文件: <系统错误>`；没有大小上限 | 不给：先看位置参数，再退到标准输入 |
| `--output <路径>`（base64） | 结果写入文件，stdout 改为输出信封 JSON | 目录必须已存在（不自动创建）；文件已存在则覆盖；`--output` 排在最后、后面没有值时**当作没给**，回落到 stdout（cli/tools/base64.go:86） | 不给：结果打到 stdout |
| `--install [目录]`（skill） | 安装模式；带目录 = 只装到该目录，不带 = 自动探测（见 4.1） | 目录值以 `-` 开头的不会被取作目录，按不带目录处理；目录建不了、文件写不了 → 报错 | 不给：只输出全文，不写任何文件 |
| `--help` / `-h` | 打印帮助，exit 0 | — | — |

**子命令名的位置约束**：子命令（`encode` / `decode` / `get` / `ok`）必须是**第一个不以 `-` 开头的参数**（`cmdutil.FirstPositional`，internal/cmdutil/flags.go:46）。所以带值的旗标不能放在子命令前面：`zpack base64 --file a.bin encode` 会把 `a.bin` 当成子命令名，报用法错误。

**位置参数的语义（base64）**：位置参数是**要编码 / 解码的文本本身**，不是文件路径。`zpack base64 encode 路径` 编码的是「路径」这两个字的 UTF-8 字节；要处理文件必须用 `--file`。以 `-` 开头的文本不能走位置参数（会被当成旗标跳过，转而去读 stdin，cli/tools/base64.go:61）——改用 `--file` 或 stdin。

**字段路径（json get）**：子命令后的第一个参数就是路径，取值时不跳过旗标——`zpack json get --quiet data.archive` 会把 `--quiet` 当成路径。路径语法见 4.1。

**`--quiet`**：这三个命令没有引擎过程日志，代码不读 `--quiet`，传了没有任何效果；装配层的「开始执行」日志也不受它影响，照常输出（cli/root.go:49）。

**帮助文本**：三个命令只有 `Short`、没有 `Long`，帮助里不列 `--file` / `--output` / `--install`（它们是手写解析的，cobra 不认识），也没有示例。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

## 3. 输出契约

这些子命令形态的成功输出里有四种是**纯文本、不带信封**（`base64 encode`、`base64 decode`、`json get`、`skill`；其中 `json get` 没有列在 conventions.md 3.3 的表里，见 9），`json ok` 什么都不输出，其余走标准信封。

### 3.1 base64

| 调用形态 | stdout | 退出码 |
|---|---|---|
| `base64 encode`，无 `--output` | Base64 字符串 + 一个换行（`stdout.TextPrint`，cli/tools/base64.go:104） | 0 |
| `base64 encode --output 路径` | 信封 JSON，见下 | 0 |
| `base64 decode`，无 `--output` | 解码出的原始字节，**不补换行**（cli/tools/base64.go:122） | 0 |
| `base64 decode --output 路径` | 信封 JSON，见下 | 0 |

带 `--output` 时完整 stdout（data 载荷类型 `cliutil.FileOutputJSON`，internal/cliutil/cliutil.go:20）：

```json
{"is_success":true,"data":{"output":"out.b64","size_bytes":344}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `output` | string | 输出文件路径，**原样回显命令行里的写法**（给的是相对路径就是相对路径，不做绝对化） |
| `size_bytes` | int | 写入文件的字节数。encode = Base64 文本的长度（不含任何换行）；decode = 解码后原始字节数 |

### 3.2 json get

stdout = 取到的值的文本形态 + 一个换行（`fmt.Println`，cli/tools/json.go:101），退出码 0。

值的文本形态（`jsonValueText`，cli/tools/json.go:105）：

| 值的类型 | 输出 |
|---|---|
| 字符串 | 字符串内容本身（不含引号，转义已还原） |
| 值为整数的数字（`3`、`12.0`、`1e3`） | 整数写法：`3`、`12`、`1000` |
| 其它数字 | Go 的最短表示（`FormatFloat` 的 `g` 格式）：`0.5`、`1e+21`、`1e-06` |
| 布尔 | `true` / `false` |
| `null` | 空字符串——stdout 只有一个换行，与空字符串 `""` 无法区分 |
| 对象 / 数组 | 紧凑 JSON（无空格）。对象键按字典序重排、不保留输入顺序；HTML 字符转义（`<` → `\u003c`、`&` → `\u0026`） |

复合值的例子：路径取到对象 `{"name":"a.txt"}` 时，stdout 是 `{"name":"a.txt"}`。

### 3.3 json ok

| stdin 的内容 | 退出码 | stdout |
|---|---|---|
| 顶层对象，`is_success` 为 `true` | 0 | 空 |
| 顶层对象，`is_success` 为 `false`（包括判定类的失败信封） | 1 | 空 |
| 读不了、或不是信封（见 4.3） | 1 | `{"is_success":false,"error_info":"..."}` |

### 3.4 skill

| 调用形态 | stdout | 退出码 |
|---|---|---|
| `skill` | SKILL.md 全文 + 一个换行（`stdout.TextPrint`，cli/tools/skill.go:51） | 0 |
| `skill --install [目录]` | 信封 JSON，见下 | 0 |

```json
{"is_success":true,"data":{"skill":"zpack","installed":["/home/me/.claude/skills/zpack/SKILL.md"],"size_bytes":18375}}
```

（`size_bytes` 的数值只是示例，随内嵌文档的长度变化。）

| 字段 | 类型 | 含义 |
|---|---|---|
| `skill` | string | 技能目录名，恒为 `zpack`（`SKILL_DIR_NAME`，cli/tools/skill.go:25） |
| `installed` | string[] | 已写入的 SKILL.md **绝对路径**，每个命中的技能根目录一个。探测模式下的顺序 = 候选目录的顺序（见 4.1）；显式目录时只有一项 |
| `size_bytes` | int | 内嵌文档的字节数（`len(skillDocument)`），比实际写入的文件**少 1 字节**（见 9） |

### 3.5 stderr

这三个命令自己不输出任何过程日志。stderr 上只有装配层的统一开始日志 `[YYYY-MM-DD HH:MM:SS] zpack <命令名> 开始执行`。命令名取的是 cobra 顶层子命令的名字：`zpack base64 encode` 记的是 `zpack base64 开始执行`，`zpack json get` 记的是 `zpack json 开始执行`（`encode` / `get` 这些下级子命令是命令体内部手写分派的，cobra 不知道它们）。

## 4. 行为细则

### 4.1 正常路径

#### base64 encode

1. 确定输入（`base64ResolveInput`，cli/tools/base64.go:50）。优先级从高到低：
   - `--file 路径`：整个文件读进内存。
   - 位置参数：命令行里不以 `-` 开头的参数，**最后一个**生效（cli/tools/base64.go:53-64）。要编码的字节 = 这个参数的 UTF-8 编码。
   - 标准输入：一直读到 EOF（`io.ReadAll(os.Stdin)`，cli/tools/base64.go:76）。
2. 用标准字母表编码成**单行**字符串，不折行（cli/tools/base64.go:95）。
3. 有 `--output`：写文件（权限 0644，已存在则截断覆盖），stdout 输出信封 JSON。没有：字符串加一个换行打到 stdout。

```bash
zpack base64 encode "你好"                      # → 5L2g5aW9
zpack base64 encode --file 图片.bin              # → 单行 Base64
printf 'abc' | zpack base64 encode              # → YWJj（没有位置参数，读 stdin）
```

#### base64 decode

1. 同上取输入。
2. 严格解码（cli/tools/base64.go:109）：必须用标准字母表、必须带 `=` 补位；输入里的 `\r` `\n` 被忽略（所以带换行的 `.b64` 文件能直接解），其它任何空白都是非法。
3. 有 `--output`：原始字节写文件，stdout 输出信封 JSON。没有：原始字节直接写 stdout，**不补换行**。

```bash
zpack base64 decode "5L2g5aW9"                 # → 你好   （后面没有换行）
zpack base64 decode --file 数据.b64 --output 数据.bin
```

#### json get

1. stdin 全量读入，去掉首尾空白（`readStdinJSON`，cli/tools/json.go:47）。读取有两条路：先试 `os.ReadFile("/dev/stdin")`，失败再改用 `os.Stdin` 循环读（Windows 上没有 `/dev/stdin`，走的就是第二条，见 5）。
2. 解析成通用结构：对象 → map，数组 → 切片，数字 → float64。
3. 路径按 `.` 切成若干段，从顶层逐段下钻（cli/tools/json.go:83-100）：

   | 当前所在的值 | 这一段的处理 |
   |---|---|
   | 对象 | 段当键名查找，没有这个键 → 报错 |
   | 数组 | 段必须是非负十进制整数且小于数组长度，当数组下标；否则报错 |
   | 字符串 / 数字 / 布尔 / null | 无法再往下走 → 路径越界 |

4. 取到的值按 3.2 的形态打印。

```bash
result=$(zpack compress 目录/ --output out.zip 2>/dev/null)
zpack json get data.archive <<< "$result"        # /abs/path/out.zip
zpack json get data.file_count <<< "$result"     # 3
zpack json get is_success <<< "$result"          # true
```

对一份 `{"data":{"entries":[{"name":"a.txt"},{"name":"b.txt"}]}}`：

| 路径 | 输出 |
|---|---|
| `data.entries.1.name` | `b.txt` |
| `data.entries.0` | `{"name":"a.txt"}` |
| `data.entries` | `[{"name":"a.txt"},{"name":"b.txt"}]` |
| `data` | 整个 data 对象的紧凑 JSON（键按字典序） |

#### json ok

1. 同上读入并解析。
2. 顶层必须是对象，并且有布尔类型的 `is_success` 字段。
3. `is_success` 为 true → exit 0，false → exit 1。**只看这一个字段**，不读 `error_info`，也不读 `data`。

```bash
zpack compress 目录/ --output out.zip 2>/dev/null | zpack json ok && echo 成功 || echo 失败
```

#### skill

- **不带 `--install`**：全文加一个换行打到 stdout，exit 0。不碰文件系统，多余的参数被忽略。
- **`--install <目录>`（显式目录）**：`<目录>/zpack/` 不存在就创建（连带多级，`os.MkdirAll`，权限 0755），写入 `<目录>/zpack/SKILL.md`（内容 = 全文 + 一个换行，权限 0644），stdout 输出信封 JSON。
- **`--install` 不带目录（探测模式）**（`probeSkillRoots`，cli/tools/skill.go:85）：依次检查下面四个候选根目录，**已存在且是目录的才安装**，装法同上：

  | 顺序 | 候选根目录 | 级别 |
  |---|---|---|
  | 1 | `~/.claude/skills` | 家目录级 |
  | 2 | `~/.agents/skills` | 家目录级 |
  | 3 | `./.claude/skills` | 项目级 |
  | 4 | `./.agents/skills` | 项目级 |

  家目录取不到（`os.UserHomeDir` 失败）时跳过前两个。一个都没有 → 报错（见 4.3）。

```bash
zpack skill                                  # 全文打到 stdout
zpack skill --install                        # 装进所有已存在的候选目录
zpack skill --install ~/.claude/skills       # 只装这一处
```

### 4.2 边界与退化输入

#### base64

| 输入情况 | 行为 |
|---|---|
| 空输入（空文件 / 空 stdin） | encode 输出一个换行（空串的 Base64 是空串，`TextPrint` 再补换行）；decode 得到 0 字节，stdout 无输出，exit 0 |
| 位置参数是空字符串 `""` | 等于没给位置参数（判的是 `positional != ""`，cli/tools/base64.go:72），转而去读 stdin |
| 多个位置参数 | 最后一个生效：`zpack base64 encode a b` 输出 `Yg==` |
| 位置参数 + `--output` 同时给 | **已知缺陷**：`--output` 的值也是个不以 `-` 开头的参数，会被当成位置参数并覆盖前面的。`zpack base64 encode hello --output out.txt` 实际编码的是 `out.txt`。`--file` + `--output` 不受影响（`--file` 优先） |
| 文本以 `-` 开头 | 被当成旗标跳过，转而去读 stdin。`zpack base64 encode -- -abc` 编码的不是 `-abc` |
| stdin 里有 `\r\n` | 原样编码，不做换行转换 |
| 解码输入含空格 / 制表符 / URL-safe 字符（`-` `_`）/ 缺补位 | 一律 `无效的 Base64 输入` |
| 大文件 | 整个文件读进内存，编码出的字符串是另一份（约为文件的 4/3），写文件时还要再拷一份（cli/tools/base64.go:98）；输出是不折行的超长单行。没有流式处理 |

「位置参数 + `--output`」这一条的核对办法：用 decode 形态、让 `--output` 指向一个不存在的目录——`zpack base64 decode "aGVsbG8=" --output <不存在的目录>\x.txt` 报的是 `无效的 Base64 输入: illegal base64 data at input byte 1`，而不是 `无法写入文件`，说明被解码的是 `--output` 的值（解码在写文件之前，所以这个探针不落任何文件）。

#### json get

| 输入情况 | 行为 |
|---|---|
| 路径是空字符串 | 切出一段空串，到对象里找键 `""` → `字段不存在: （段 ""）` |
| 对象上遇到数字段 | 数字当键名：`data.0` 是在对象里找键 `"0"`，不是下标 |
| 数组上遇到非数字、负数、越界的段 | `数组下标非法` |
| 已经下钻到字符串 / 数字 / 布尔 / null，后面还有段 | `路径越界`（null 再往下走也是这个错） |
| 顶层是数组 | 可以直接用下标：`echo '[1,2]' \| zpack json get 1` 输出 `2` |
| 键名里含 `.` | 无法寻址（没有转义语法，`a.b` 总是被切成两段） |
| 路径后面还有别的参数 | 被忽略，只有第一个参数是路径 |
| 重复的键 | 后一个覆盖前一个（标准库解析行为） |
| 整数超过 2 的 53 次方 | 失真：`9007199254740993` 输出 `9007199254740992`、`12345678901234567890` 输出 `1.2345678901234567e+19`（数字是 float64） |
| 很大的整数 | 超出 int64 范围的整值（如 `1e19`）走指数写法 `1e+19`；同一个值在复合输出里却是 `10000000000000000000`，两处写法不一致 |
| 字符串值里有换行 | 原样输出，和「输出本身的结尾换行」混在一起，调用方分不清 |
| stdin 前后有空白 | 容忍（去首尾空白） |
| stdin 里有两份 JSON（比如两行信封拼在一起） | `非法 JSON`——只接受单个顶层值 |

#### json ok

| 输入情况 | 行为 |
|---|---|
| `is_success:false` 且带 `data`（判定类失败） | exit 1，stdout 空。要拿 `data` 里的值（如 `hash --expect` 不匹配时的实算哈希）另用 `json get` |
| `is_success` 缺失 / 不是布尔（字符串 `"true"`、数字）/ `null` | `缺少 is_success 字段（非 zpack 信封 JSON）` |
| 顶层是数组 / 字符串 / 数字 | `非信封 JSON（顶层应为对象）` |
| stdin 空，或只有空白 | `stdin 无输入` |
| 多余的参数 | 被忽略 |

#### skill

| 输入情况 | 行为 |
|---|---|
| `--install` 出现多次 | 以最后一次**带目录**的为准；后面再跟一个不带目录的 `--install` 不会把它清掉 |
| `--install` 后面紧跟的是旗标（`--install --quiet`） | 不取目录，按探测模式处理 |
| 目标处已有 SKILL.md | 直接覆盖（等于把已安装的技能升级到当前二进制内嵌的版本） |
| `--install=目录` 的连等写法 | 不被识别（只认独立的 `--install` 参数），结果只是把全文打到 stdout，**不安装** |
| 不带 `--install` 时的多余位置参数 | 被忽略，照常输出全文 |
| 文档结尾 | `skill.md` 文件本身以一个空行结尾，所以 `zpack skill` 的 stdout 末尾是连续三个换行 |

### 4.3 错误处理

所有失败都是 `{"is_success":false,"error_info":"..."}` + exit 1。带「错误原文」的条目，拼进去的是 Go 标准库的英文错误信息，没有本地化。

#### base64

| 场景 | `error_info` | 出处 |
|---|---|---|
| 没给子命令，或子命令不是 `encode` / `decode` | `用法: base64 encode <文本\|--file 路径> 或 base64 decode <Base64\|--file 路径> [--output 路径]` | cli/tools/base64.go:45 |
| `--file` 后面没有值 | `--file 缺少值` | :57 |
| `--file` 指向的文件读不了（不存在、是目录、没权限） | `无法读取文件: <错误原文>` | :68 |
| 读 stdin 出错 | `无法读取标准输入: <错误原文>` | :78 |
| 解码输入不是合法 Base64 | `无效的 Base64 输入: <错误原文>`（如 `illegal base64 data at input byte 0`） | :111 |
| `--output` 写不了（目录不存在、没权限） | `无法写入文件: <错误原文>` | :99（encode）、:117（decode） |

解码失败发生在写文件之前，不会留下输出文件。

#### json

| 场景 | `error_info` | 出处 |
|---|---|---|
| `json` 后面没有子命令，或不是 `get` / `ok` | 三行用法，在 JSON 里是转义成 `\n` 的一行：`用法: json get <字段路径>（stdin 读 JSON，值裸打印）\n或: json ok（stdin 读信封 JSON，is_success → exit 0/1）\n字段路径：点分段 + 数组下标，如 data.entries.0.name` | cli/tools/json.go:42 |
| `json get` 没给路径 | `用法: json get <字段路径>（如 data.archive / data.entries.0.name / is_success）` | :76 |
| stdin 空，或只有空白 | `stdin 无输入` | :64 |
| stdin 不是合法 JSON | `非法 JSON: <错误原文>`（如 `invalid character 'g' looking for beginning of value`） | :68 |
| 对象里没有这个键 | `字段不存在: <完整路径>（段 "<出错的段>"）` | :88 |
| 数组这一段不是合法下标 | `数组下标非法: <这一段>（路径 <完整路径>）` | :94 |
| 当前值不是对象 / 数组，无法继续下钻 | `路径越界: <完整路径>（段 "<出错的段>" 处非对象/数组）` | :98 |
| `json ok`：顶层不是对象 | `非信封 JSON（顶层应为对象）` | :132 |
| `json ok`：没有 `is_success`，或它不是布尔 | `缺少 is_success 字段（非 zpack 信封 JSON）` | :136 |

`json ok` 遇到 `is_success:false` **不算错误**：exit 1，stdout 空，没有 `error_info`。

#### skill

| 场景 | `error_info` | 出处 |
|---|---|---|
| 探测模式下四个候选目录都不存在 | `未发现 agent 技能目录；用 zpack skill --install <路径> 指定（如 ~/.claude/skills 或 ~/.agents/skills）` | cli/tools/skill.go:61 |
| 技能目录建不了 | `无法创建技能目录: <错误原文>` | :68 |
| SKILL.md 写不了 | `无法写入技能文件: <错误原文>` | :73 |

探测模式命中多个目录时是逐个安装的；中途某一处失败就输出错误并退出，**前面已经写好的不回滚**。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| json 读 stdin | 没有 `/dev/stdin`，`os.ReadFile("/dev/stdin")` 失败，走 `os.Stdin` 循环读的兜底（cli/tools/json.go:49-61） | `os.ReadFile("/dev/stdin")` 直接读 | 同 macOS |
| base64 decode 写 stdout | 原始字节直写，不做换行转换，不补换行 | 同 | 同 |
| 文件权限位（0644 / 0755） | 只影响「只读」属性 | 生效 | 生效 |
| 路径形态 | `--output` 回显照输入原样（反斜杠或正斜杠）；`installed` 是 `filepath.Abs` 得到的反斜杠绝对路径，JSON 里转义成 `\\` | 正斜杠 | 正斜杠 |
| 家目录 | `os.UserHomeDir`（`%USERPROFILE%`） | `$HOME` | `$HOME` |

- **Git Bash 的路径改写**：在 Git Bash 里调用 zpack.exe 时，`/tmp/x` 这种形态的参数会被 MSYS 自动改写成 Windows 路径再交给程序（`MSYS_NO_PATHCONV=1` 可关闭）。这是 shell 的行为，不是 zpack 的，但会影响 `--file` / `--output` 实际指向哪里，也会让 `--output` 回显的路径与敲进去的不一样。
- **纯 Go 构建与完整版在这三个命令上完全相同**：只用标准库，没有 cgo、没有构建标签。office / media 那种「需完整版」的分流在这里不存在。
- 本次核对是在 Windows 上做的；macOS / Linux 上 json 的第一条读取路径（`/dev/stdin`）没有实测【待确认】。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 这三个命令为什么存在

- **单二进制闭环**（2026-11-08 用户定案，AGENTS.md:70）：zpack 的结果是一行 JSON，脚本和 Agent 想取字段原本得靠 `python3 -c "import json…"` 或 jq——在没装它们的机器上，一个「单文件、裸机可跑」的工具反而消费不了自己的输出。`json ok` / `json get` 补上了这最后一环（cli/tools/json.go:1）。
- **base64 是「甜点功能」**（cli/tools/base64.go:1，verify/verify.sh:585）：小而常用的编码转换，与 `crypto` 同属一类附加能力。它的引入日期在代码和 AGENTS.md 里都没有留痕【待确认】。
- **skill 是给消费端 Agent 的自描述**（2026-11-04，AGENTS.md:145-147）：Agent 装上这份技能就知道怎么调 zpack。文档经 `go:embed` 内嵌在二进制里——单文件分发，文档与二进制版本永不漂移；一份文档既是给人看的 `zpack skill` 输出，也是装进技能目录的文件。

### 6.2 手写参数解析的两面

- 三个命令都关掉了 cobra 的旗标解析、手写循环。这样做是为了同时保住两种语义：未知旗标静默忽略、数值参数严格校验（internal/cmdutil/flags.go:3-6，全局约定见 conventions.md）。
- **代价一：旗标的值和位置参数在命令行上长得一样**，都是不以 `-` 开头的参数。别的命令在解析循环里会显式跳过已知旗标的值；`base64` 的输入解析没有跳过 `--output` 的值，于是它被当成位置参数（4.2 的已知缺陷）。
- **代价二：子命令必须是第一个非旗标参数**（第 2 节）。
- **代价三：帮助文本不列旗标**——cobra 不认识这些手写旗标，而命令又没写 `Long`。

### 6.3 有两个成功输出绕过了 `internal/stdout`

conventions.md 的约束是「最终输出只允许经输出层的 print 函数」。本功能里有两处例外，都是历史写法：

- **`base64 decode`**：`fmt.Fprintf(os.Stdout, "%s", decoded)` 之后自己 `os.Exit(0)`（cli/tools/base64.go:122-123）。不能改用 `stdout.TextPrint`——它用的是 `fmt.Println`，会补一个换行（internal/stdout/log.go:70），二进制内容多出一个字节就坏了。`crypto decrypt` 是同一种写法（cli/crypto/crypto.go:438-439）。
- **`json get`**：直接 `fmt.Println(...)`，然后正常返回，不调 `os.Exit`（cli/tools/json.go:101）。输出和用 `TextPrint` 没有区别，只是没有经过输出层。

改动时不要「顺手规范化」`base64 decode`。另外这两处不走输出层，意味着「print 即 exit」这条约束在这里是手工遵守的（decode 写完立刻退出；get 后面没有任何清理）。

### 6.4 GUI 耦合：Base64 页靠 stdin 驱动

- GUI 的 Base64 页不直接调函数，而是起子进程 `zpack base64 encode|decode`，把输入框的内容**喂给 stdin**（`Service.RunToolStdin([]string{"base64", verb}, input, …)`，ui/gui/main.go:2593、ui/gui/service.go:942）。
- 为什么不走 argv（ui/gui/service.go:938-941 注释，2026-11-23 审计）：引擎把首个不以 `-` 开头的参数当文本，明文以 `-` 开头（比如 Markdown 列表）时会被当成旗标跳过、回落去读空的 stdin——当时的表现是「加密页悄悄加密了空内容、Base64 页点了没反应」。argv 还有平台长度上限（Windows 命令行 32767 字符）。
- GUI 依赖 base64 的三件事（`runToolOutput`，ui/gui/service.go:947-971）：
  1. 不带位置参数时把 stdin 一直读到 EOF；
  2. 成功时 stdout 是纯文本（GUI 把末尾的换行去掉后显示，ui/gui/service.go:969）；
  3. 失败时 stdout 是 error 信封、退出码 1（GUI 解析出 `error_info` 显示在失败行，ui/gui/service.go:958-965）；进程失败而 stdout 不是信封时显示「命令执行失败」（:966）。

  所以 **stdin 模式、纯文本输出、失败时的 error 信封，都是 GUI 的依赖面**，改任何一个都是破坏性变更。同一个辅助函数也被 crypto 加密 / 解密页使用。

### 6.5 json ok 只用退出码说话

- `json ok` 处在管道末端，设计目标就是让 `&&` / `||` 直接可用（cli/tools/json.go:124 注释），所以成功和「信封说失败」两种情况 stdout 都是空的。
- 由此带来一个调用方要知道的细节：**exit 1 有两种含义**——信封里 `is_success:false`（stdout 空），或者输入根本不是信封（stdout 上有 error 信封）。要区分就看 stdout。
- 判定类命令的失败信封带 `data`（如 `hash --expect` 不匹配时的实算值），`json ok` 对它给 exit 1；想拿 `data` 里的值，再用 `json get`。

### 6.6 json 的数字是 float64

- 标准库把 JSON 数字解析到通用类型时用的是 float64。`jsonValueText` 对整数值单独走整数格式化（cli/tools/json.go:110-112），否则 `1234567` 会被打成 `1.234567e+06`。
- 这层处理救不了精度：超过 2 的 53 次方的整数会失真，超出 int64 的整值又回到指数写法（见 4.2）。zpack 自己输出里的 `size_bytes` 远够用，但不要拿 `json get` 去读别的来源的大整数。

### 6.7 skill 的几条设计

- **安装文件与 stdout 逐字节一致**：`zpack skill` 输出的是全文加一个换行（`TextPrint`），安装时写的是全文加同一个换行（cli/tools/skill.go:71-72，注释写明这是 POSIX 文本文件的标准结尾）。验收用 `cmp` 守着这一条（verify/verify.sh:875-876）。改任何一边都要改另一边。
- **不代用户决定装哪家**（cli/tools/skill.go:83-84）：探测模式只往**已经存在**的技能目录里装，不凭空创建 `~/.claude` 或项目里的 `.claude`。想装到新位置就显式给目录。
- **渐进披露**（AGENTS.md:147）：frontmatter 里的 `description` 常驻 Agent 上下文，正文按需加载。所以 description 要写全（它现在是一长串能力清单），正文分节供 Agent 按需读。
- **skill.md 是手工维护的摘要**，从 AGENTS.md 提炼（AGENTS.md:146-147），不是从各命令的 `--help` 生成的。任何命令的旗标、输出、默认值变了，都要同步改 `cli/tools/skill.md`；它是 `go:embed` 进二进制的，改完必须重新编译才生效。
- **skill.md 的结构**（改它的时候按这个找位置）：

  | 行 | 内容 |
  |---|---|
  | 1-4 | frontmatter：`name: zpack` 和一长串 `description`（Agent 常驻上下文里的那一行） |
  | 8-17 | §1 输出协议：stdout / 退出码 / stderr / 确定性 / 通用旗标 / Ctrl+C 六行表 |
  | 19-24 | §2 通用值格式：页码规格、时间 / 帧区间、体积、尺寸 |
  | 26-220 | §3 命令：3.1 compress、3.2 extract、3.3 convert、3.4 list/test/info、3.5 hash、3.6 media、3.7 audio、3.8 image、3.9 pdf、3.10 office、3.11 ffmpeg、3.12 base64 与加密、3.13 json、3.14 杂项（bench、skill） |
  | 222-243 | §4 调用范式：零依赖消费的几个管道写法 |
  | 245-252 | 刻录光盘（没有编号，挂在 §4 下面） |

  新增一个命令家族时，在 §3 里加一小节，并考虑给 §1、§2 补协议层面的变化。

### 6.8 改动时容易破坏的东西

| 别动的东西 | 谁在依赖 |
|---|---|
| base64 位置参数 = 文本本身（不是文件路径） | verify/verify.sh:586 逐字节比对 `5L2g5aW9` |
| base64 encode 的 stdout 补一个换行、decode 不补 | GUI（ui/gui/service.go:969 去尾换行）；decode 的二进制安全 |
| base64 的 stdin 模式、失败时的 error 信封 | GUI Base64 页、crypto 页 |
| `json get` 的几种报错文案 | 验收只比对了其中两种的退出码，文案没有比对；目前没有别的消费者，改了也不会被验收发现 |
| `json get` 复合值输出是紧凑 JSON | verify/verify.sh:1089 逐字节比对 `{"name":"a.txt"}` |
| `json ok` 成功与信封失败时 stdout 为空 | 管道里的 `&&` / `\|\|` 写法 |
| skill 安装文件 = stdout | verify/verify.sh:875-876 的 `cmp` |
| skill.md 的 frontmatter 在文件最前、正文里有 `is_success` 和 `volume-size` 两个关键词 | 验收只检查这三样（verify/verify.sh:869-870）——文档内容过时了不会被验收发现 |

**json 命令既是被测对象，也是验收脚本自己的工具**：verify.sh 里约 20 行用 `zpack json get` / `json ok` 读结果（json 段、burn 段、7z 段）。json 命令一旦行为变了，这些用例跟着失败，容易误判成被测功能坏了。

### 6.9 验收脚本自身还没有「零依赖」

verify.sh 里有 23 行调用 python3（造 PDF 夹具、解析 zip 目录、读 crypto 的输出等），和约 20 行 `zpack json` 混用——这是历史层积：`json` 命令 2026-11-08 才有，更早的用例没有回头改。「单二进制闭环」目前只对产品成立，验收脚本仍要求机器上有 python3。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面 | `cli/tools/base64.go` | `base64 encode` / `decode`：输入源解析、输出目标解析 |
| | `cli/tools/json.go` | `json get` / `ok`：stdin 读取、路径下钻、值的文本形态 |
| | `cli/tools/skill.go` | `skill`：全文输出、`--install`、技能目录探测；`go:embed skill.md` |
| | `cli/tools/skill.md` | 内嵌的技能文档本体（手工维护） |
| | `cli/tools/doc.go` | 包文档 |
| 装配 | `cli/root.go` | 三个命令挂到根命令（:78-80）；统一开始日志（:49-53） |
| 共用小件 | `internal/cliutil/cliutil.go` | `RestAfterPositional`（去掉子命令名后的参数）、`FileOutputJSON`（带 `--output` 的载荷，crypto 也用） |
| | `internal/cliutil/help.go` | `HelpAwareRun`（`--help` / `-h` 守卫） |
| | `internal/cmdutil/flags.go` | `FirstPositional`（取子命令名） |
| 输出契约层 | `internal/stdout/log.go` | `TextPrint` / `JSONPrint` / `ErrorPrint` |
| 引擎 | 无 | 纯标准库，命令面就是全部实现 |
| 测试 | 无 | `cli/tools` 下没有 `_test.go`；行为只由验收脚本守 |
| GUI 耦合 | `ui/gui/main.go`（:2593，Base64 页）、`ui/gui/service.go`（:942 `RunToolStdin`） | GUI 经 stdin 驱动 base64 |
| | `ui/gui/service_test.go`（:289-298 `TestRunToolStdinHyphen`） | 以 `base64 encode` 为夹具，验证以 `-` 开头的文本走 stdin 送达 |
| 验收脚本 | `verify/verify.sh` | :585-595（base64）、:866-877（skill）、:1079-1094（json） |

## 8. 验收用例

都在 `verify/verify.sh` 里，纯 Go 构建即可跑（`bash verify/verify.sh`，只测 `bin/zpack-cli`）。

### base64（:585-595，注释「base64 编解码（甜点功能）」）

挂在 tar 段的末尾，没有自己的 `# ----------` 分节标题。

| 用例名 | 断言 |
|---|---|
| `base64 encode` | `zpack base64 encode "你好"` 输出 `5L2g5aW9` |
| `base64 decode 往返` | `zpack base64 decode "5L2g5aW9"` 输出 `你好` |
| `base64 二进制文件往返` | 256 字节随机数据：`encode --file` 的 stdout 存成 `.b64`，再 `decode --file … --output …`，`cmp` 原文件与还原文件一致 |
| `base64 非法输入 exit` | `zpack base64 decode "!!!"` 退出码 1 |

### skill（:866-877，在「agent 自描述：子命令 --help 守卫 + skill 文档分发」一节里）

| 用例名 | 断言 |
|---|---|
| `skill 输出 exit` | `zpack skill` 退出码 0 |
| `skill frontmatter` | 输出前三行里有 `name: zpack` |
| `skill 内容` | 输出里同时有 `is_success` 与 `volume-size`（契约 + 旗标各取一个关键词） |
| `skill 安装 exit` | `zpack skill --install <临时目录>` 退出码 0 |
| `skill 落位` | `<临时目录>/zpack/SKILL.md` 存在 |
| `skill 安装内容一致` | `zpack skill` 的 stdout 与安装文件 `cmp` 一致 |

### json（:1079-1094，「json 命令族」一节）

输入是一份固定的信封 `{"is_success":true,"data":{"archive":"/a/b.zip","file_count":3,"entries":[{"name":"a.txt"},{"name":"b.txt"}]}}`。

| 用例名 | 断言 |
|---|---|
| `json ok 成功信封 exit` | 0 |
| `json ok 失败信封 exit` | `{"is_success":false,"error_info":"x"}` → 1 |
| `json get 字符串字段 exit` / `json get archive 值` | `data.archive` → 退出码 0，值 `/a/b.zip` |
| `json get 数字字段` | `data.file_count` → `3` |
| `json get 布尔字段` | `is_success` → `true` |
| `json get 数组下标` | `data.entries.1.name` → `b.txt` |
| `json get 复合值紧凑 JSON` | `data.entries.0` → `{"name":"a.txt"}`（逐字节） |
| `json get 字段不存在 exit` | `data.nonexistent` → 1 |
| `json get 下标越界 exit` | `data.entries.9.name` → 1 |
| `json 非法输入 exit` | `garbage` 喂给 `json ok` → 1 |
| `json get 缺路径 exit` | 不给路径 → 1 |
| `json --help exit` | 0 |

此外 burn 段（:1099、:1100、:1115、:1119）和 7z 段（:1135、:1136、:1152）把 `zpack json get` 当作读结果的工具来用。

### 没有被验收覆盖的

- base64：`--output` 的信封 JSON 字段、stdin 模式、`--file 缺少值`、多个位置参数、位置参数与 `--output` 同用。
- json：`json ok` 的两种「非信封」报错与 `stdin 无输入`；`json get` 的报错只比对了两种（字段不存在、下标越界）的退出码，文案都没有比对。
- skill：探测模式（`--install` 不带目录）、「未发现 agent 技能目录」报错、信封 JSON 里 `installed` / `size_bytes` 的取值。

### 手动验证

```bash
# base64：三种输入源；decode 的 stdout 不补换行
zpack base64 encode "你好"                        # 5L2g5aW9
zpack base64 decode "5L2g5aW9" | od -c            # 「你好」的 UTF-8 字节，后面没有 \n
printf 'abc' | zpack base64 encode                # YWJj（读 stdin）

# json：各类型取一次，再试报错
echo '{"is_success":true,"data":{"n":3,"b":true,"z":null,"a":[10,20]}}' | zpack json get data.a.1   # 20
echo '{"is_success":true,"data":{"n":3}}' | zpack json get data.z                               # 空行，退出码 0
echo '{"is_success":true,"data":{"n":3}}' | zpack json get data.x; echo $?                      # error_info，1
echo '{"is_success":false}' | zpack json ok; echo $?                                            # 无输出，1
echo 'garbage' | zpack json ok; echo $?                                                        # error_info，1

# skill：stdout 与安装文件逐字节一致（装到临时目录；不要在没有参数的情况下 --install，那会写进真实的技能目录）
zpack skill > /tmp/skill-stdout.txt
zpack skill --install /tmp/skill-root
cmp /tmp/skill-stdout.txt /tmp/skill-root/zpack/SKILL.md && echo 一致
```

## 9. 未结项与已知限制

**已知不对、暂时没改的：**

- **base64：位置参数与 `--output` 同用时，`--output` 的值被当成输入**（cli/tools/base64.go:53-64，见 4.2）。想绕开就用 `--file` 或 stdin。
- **base64：帮助文本不列 `--file` / `--output`，也没有示例**（没有 `Long`）。
- **json：数字是 float64**——超过 2 的 53 次方的整数失真；超出 int64 的整值在标量输出里是指数写法、在复合输出里是普通写法，两处不一致（cli/tools/json.go:110-113、:119）。
- **json get：键名含 `.` 无法寻址；`null` 与空字符串输出相同；字符串里的换行、首尾空白原样输出，与输出结尾的换行分不清。**
- **json ok：exit 1 有两种含义**（信封失败 / 不是信封），要区分得看 stdout。
- **json get：路径不跳过旗标**——旗标写在子命令前面时会被当成路径（`zpack json --quiet get data` 报 `字段不存在: --quiet`）。
- **skill：`size_bytes` 比安装文件少 1 字节**——报的是内嵌文档的长度，文件多一个结尾换行（cli/tools/skill.go:72、:79）。
- **skill.md（内嵌文档）已经和代码 / AGENTS.md 有漂移**，它是手工摘要、没有验收守着内容是否最新：
  - cli/tools/skill.md:95 的 `media convert … [--bake]`：convert 并不解析 `--bake`（只有旋转族 cli/media/media.go:729 和 `bake` 子命令 :51 认它），AGENTS.md:155 记为 2026-11-07 已退休。
  - cli/tools/skill.md:17「Ctrl+C：一次 = 清理半成品后退出；两次 = 立即退出」：只有压缩包引擎响应第一次 Ctrl+C（conventions.md 第 2 节）。
  - cli/tools/skill.md:16 把 `--quiet` 列为通用旗标：并不是所有命令都读它（conventions.md 第 9 节）。
  - cli/tools/skill.md:194 的 base64 用法只写了 `<输入> [--output <路径>]`，没提 `--file` 和 stdin。
  - cli/tools/skill.md:161 把 pdf resize 的纸型列成「`a0`-`a10`/B 系/`letter`/`legal`/`ledger`」，其中 `letter`、`legal`、`ledger` 现在都报错（见 [pdf.md](pdf.md) 第 9 节）。
  - cli/tools/skill.md:170、:175 的 office 用法没有 `--libreoffice-path` 旗标，只提了同名的环境变量（见 [office.md](office.md)）。
- **没有单元测试**：`cli/tools` 下没有 `_test.go`，json 的路径下钻和值的格式化这类纯逻辑只能靠验收脚本的十几个用例。
- **验收脚本仍依赖 python3**（verify.sh 23 行），见 6.9。

**代码与文档不一致的地方：**

| 文档 | 文档怎么说 | 代码实际 |
|---|---|---|
| AGENTS.md:146 | 「探测 `~/.claude/skills`/`~/.agents/skills`（家目录级）与 `./.claude/skills`/`./.agents/skills`（项目级，仅当已存在）全部安装」——「仅当已存在」只挂在项目级后面 | 四个候选**一律**要求已存在才安装（cli/tools/skill.go:98-102；注释 :83「家目录级（不存在则跳过——不代用户决定装哪家）」）。AGENTS.md:84 的「全部命中处」与代码一致 |
| AGENTS.md:43-46、cli/tools/skill.md:194 | base64 的输入只写了位置参数与 `--output` | 还有 `--file` 与 stdin 两种输入源，且 `--output` 的值会被当成位置参数（上面的缺陷） |
| AGENTS.md:84 | `skill --install` 的 `size_bytes` 未说明含义 | 是内嵌文档长度，不是安装文件大小 |
| conventions.md 3.3 | 纯文本输出的命令表里没有 `json get` | `json get` 的 stdout 也是裸值、不走信封（cli/tools/json.go:101），应在那张表里 |

**没做的：**

- base64：URL-safe 字母表、流式处理、折行输出。
- json：数组切片 / 过滤、键名转义、大整数保真。
- skill：安装时提示覆盖、版本比对。

**【待确认】：**

- base64 命令的引入日期与来由，代码和 AGENTS.md 里都没有留痕。
- macOS / Linux 上 json 读 stdin 的第一条路径（`/dev/stdin`）没有实测，本次核对环境是 Windows。

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
