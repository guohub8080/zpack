# 加密与校验（crypto / hash）

> 用 Go 标准库提供 RSA 密钥对生成、公钥加密 / 私钥解密、随机密码与 UUID 生成，以及文件哈希的计算与校验（含与 shasum 互通的清单）。最后核对日期：2026-11-24（对应提交 `40fb9e7`）。

这份 spec 是该功能的**行为与设计的权威说明**。代码与 spec 不一致时：spec 是有意修改的，改代码；spec 过时了，先修 spec。
每条规则都以代码为准核对过；拿不准的标了「【待确认】」。

本功能全部是 Go 标准库实现，没有引擎包，命令面就是全部代码（cli/crypto/doc.go:3）。压缩包自己的加密（`compress --password`、`extract --password`，`core/` 里那套 zip 加密原语）不归本功能，见 [archive.md](archive.md)。

## 1. 功能定义

- **做什么**
  - `crypto genkey`：生成一对 RSA 密钥。公钥是 PEM（`PUBLIC KEY`，PKIX 格式），私钥是 PEM（`RSA PRIVATE KEY`，PKCS#1 格式）。可以输出到 stdout，也可以落成两个文件。
  - `crypto encrypt` / `crypto decrypt`：公钥加密、私钥解密。填充用 RSA-OAEP（SHA-256）；数据超过单次 RSA 的上限时自动改用「RSA 保护一把 AES-256-GCM 会话密钥」的混合加密，所以任意大小的数据都能加密。明文可以是一段文本、一个文件或 stdin；结果可以是 Base64 文本或文件。
  - `crypto password`：生成随机密码——四个字符类（小写、大写、数字、符号）缺省全开，可加自定义字符集。
  - `crypto uuid`：生成 UUID v4。
  - `hash`：对文件计算 `md5` / `sha1` / `sha224` / `sha256` / `sha384` / `sha512` / `crc32`。四种形态：计算、对单个期望值校验、生成校验清单、批量校验清单。
- **不做什么**（明确的边界）
  - 没有口令加密：不做口令派生，也没有独立的对称加密命令（AES 只在混合加密内部使用）。
  - 没有签名、验签、证书；只支持 RSA，不支持 EC / Ed25519。
  - 不支持小于 1024 位的密钥（参数校验放行到 512，但 Go 标准库会拒绝，见 6.5）。
  - `crypto encrypt` 不加密目录；`hash` 不对目录计算，也不递归——要算一个目录就把它里面的文件逐个列出来。
  - 不是密码管理器：生成的密码只出现在 stdout，不保存。
  - 不做压缩包加密（见本节开头的边界说明）。
  - `md5` / `sha1` 只是照常提供，用于校验和与兼容旧清单——它们不适合做安全用途。
- **使用场景**
  - 下载之后对官方哈希（`hash 文件 --expect <hex>`，退出码直接给脚本用）。
  - 给一批文件出一份清单，日后用 `hash --check` 或 `shasum -c` 核对有没有被改动。
  - 生成一对密钥，用公钥加密一小段配置或密钥，把 Base64 密文发出去，对方用私钥解开。
  - 临时要一个强密码或一个 UUID。
  - GUI 的「加密 / 解密 / 生成密钥 / 文件哈希 / 随机生成」几页都经这些命令驱动（见 6.11）。

## 2. 命令与旗标

| 命令 | 作用 |
|---|---|
| `zpack crypto genkey [--bits N] [--output <前缀>]` | 生成 RSA 密钥对 |
| `zpack crypto encrypt (--key <文本> \| --key-file <路径>) [<明文> \| --file <路径>] [--output <路径>]` | 公钥加密 |
| `zpack crypto decrypt (--key <文本> \| --key-file <路径>) [<密文> \| --file <路径>] [--output <路径>]` | 私钥解密 |
| `zpack crypto password [--length N] [--count N] [--lower …] [--upper …] [--digit …] [--symbol …] [--charset <字符集>]` | 生成随机密码 |
| `zpack crypto uuid [--count N] [--upper] [--no-dash]` | 生成 UUID v4 |
| `zpack hash <文件...> [--algorithm 算法]` | 计算哈希（缺省形态） |
| `zpack hash <文件> --algorithm 算法 --expect <十六进制>` | 对单个期望值校验 |
| `zpack hash <文件...> --algorithm 算法 --output <清单>` | 生成校验清单 |
| `zpack hash --check <清单>` | 批量校验清单（算法自动判定，`--algorithm` 不参与） |

### 2.1 `crypto genkey`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--bits N` | 密钥位数 | 必须是 1024–4096 之间、256 的倍数的整数，否则报 `--bits 需要 1024-4096 的 256 倍数整数，收到: <值>`（cli/crypto/crypto.go:296-302）。**实际只有 ≥ 1024 的能成功**，512 与 768 会在生成阶段被标准库拒绝（6.5） | `2048` |
| `--output <前缀>` | 把密钥写成文件：`<前缀>_private.pem`（私钥）与 `<前缀>_public.pem`（公钥） | 任意路径前缀，原样拼接后缀。任一目标文件已存在就拒绝，不覆盖 | 不给 = 密钥内容输出到 stdout |

### 2.2 `crypto encrypt` / `crypto decrypt`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--key <文本>` | 密钥内容：PEM 文本，或「Base64 编码的 PEM」 | 下一个参数原样当作密钥（即使以 `-` 开头——PEM 本来就以 `-----BEGIN` 开头）。与 `--key-file` 同时给时，先出现的生效 | 必须给 `--key` 或 `--key-file` 之一 |
| `--key-file <路径>` | 从文件读密钥，格式同上 | 文件必须可读 | 同上 |
| `--file <路径>` | 明文 / 密文取自文件（加密时读原始字节；解密时读 Base64 文本） | 文件必须可读 | 不给则看位置参数，再看 stdin |
| `--output <路径>` | 结果写文件（加密写 Base64 文本，解密写明文字节） | 任意路径；**已存在直接覆盖**，没有存在性检查 | 不给 = 输出到 stdout |

数据的来源按优先级：`--file` > 第一个不以 `-` 开头的位置参数 > stdin 的全部字节（`cryptoResolveData`，cli/crypto/crypto.go:594-626）。找位置参数时会跳过 `--key` / `--key-file` / `--output` / `--bits` / `--file` 及其值。

### 2.3 `crypto password`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--length N` | 密码长度（按字符数，不是字节） | 必须是 4–128 的整数，否则报 `--length 需要 4-128 整数，收到: <值>`（cli/crypto/crypto.go:164-171） | `16` |
| `--count N` | 生成几个，每行一个 | 必须是 1–100 的整数，否则报 `--count 需要 1-100 整数，收到: <值>`（cli/crypto/crypto.go:173-179） | `1` |
| `--lower` `--upper` `--digit` `--symbol` | 四个字符类的开关 | 布尔旗标：`--upper false`、`--upper=false`、单独的 `--upper`（= true）三种写法都收。值不分大小写，`false` / `0` / `no` 为关，`true` / `1` / `yes` 为开，其余报 `<旗标> 需要 true/false，收到: <值>`（cli/crypto/crypto.go:86-111） | 全开 |
| `--charset <字符集>` | 自定义字符集，当作第五个字符类 | `--charset 值` 或 `--charset=值`。必须非空且是合法 UTF-8，否则报 `--charset 需要非空的字符集（合法 UTF-8），收到: <带引号的值>`（cli/crypto/crypto.go:122-140、187-191） | 不给 |

四个内置字符类的字符（cli/crypto/crypto.go:180-185）：

| 字符类 | 字符 |
|---|---|
| `--lower` | `abcdefghijklmnopqrstuvwxyz` |
| `--upper` | `ABCDEFGHIJKLMNOPQRSTUVWXYZ` |
| `--digit` | `0123456789` |
| `--symbol` | `!@#$%^&*()-_=+[]{}<>?,.`（23 个） |

四类全关又没有 `--charset` 时报 `至少启用一个字符类（--lower/--upper/--digit/--symbol 不能全部为 false），或用 --charset 指定自定义字符集`（cli/crypto/crypto.go:193-196）。

### 2.4 `crypto uuid`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--count N` | 生成几个，每行一个 | 1–100 的整数，报错文案同 `crypto password` | `1` |
| `--upper` | 输出大写 | 布尔旗标（写法与取值同 2.3） | 关（小写） |
| `--no-dash` | 去掉连字符，输出 32 个十六进制字符 | 布尔旗标 | 关（带连字符的 8-4-4-4-12） |

### 2.5 `hash`

| 旗标 | 含义 | 取值与校验 | 缺省 |
|---|---|---|---|
| `--algorithm 算法` | 哈希算法 | `md5` / `sha1` / `sha224` / `sha256` / `sha384` / `sha512` / `crc32`。算法名先转小写、再去掉连字符再比对（`SHA-256`、`sha-256` 都等于 `sha256`）；认不出报 `未知算法: <归一后的名字>（支持 md5/sha1/sha224/sha256/sha384/sha512/crc32）`（cli/crypto/hash.go:82-88、114-117） | `sha256` |
| `--expect <十六进制>` | 对单个文件校验 | 先转小写、去首尾空白。长度必须等于所选算法的十六进制位数，否则报 `--expect 期望 <位数> 位十六进制（<算法>），收到 <实际位数> 位`；再必须是合法十六进制，否则报 `--expect 不是合法十六进制: <原值>`（cli/crypto/hash.go:195-202）。大写的官方哈希可以直接用 | — |
| `--output <清单>` | 把结果写成 shasum 格式的清单 | 至少要给一个文件；不能与 `--expect` 同用 | — |
| `--check <清单>` | 批量校验一份清单 | 不能与 `--expect` / `--output` 同用 | — |

四种形态的优先级是 `--check` > `--expect` > `--output` > 计算（cli/crypto/hash.go:119-143）。互斥与数量规则：

| 情形 | `error_info` |
|---|---|
| `--check` 与 `--expect` 或 `--output` 同用 | `--check 不能与 --expect/--output 同时使用` |
| `--expect` 与 `--output` 同用 | `--expect 不能与 --output 同时使用` |
| `--expect` 但文件不是恰好一个 | `--expect 校验需要恰好一个文件` |
| `--output` 但一个文件都没给 | `--output 生成清单需要至少一个文件` |
| 计算形态但一个文件都没给 | `用法: hash <文件...> [--algorithm 算法] [--expect 值] [--output 清单] [--check 清单]` |

`hash` 的四个旗标都必须带值，缺值时报 `<旗标> 缺少值`；算法是否合法的检查排在所有形态规则之前（所以 `hash 文件 --algorithm md6 --expect x` 先报「未知算法」）。旗标可以放在文件前面或后面；不以 `-` 开头的参数都是文件路径，按给出的顺序处理。

### 2.6 解析规则（本功能特有）

- **子命令是 `crypto` 后第一个不以 `-` 开头的参数**（`cmdutil.FirstPositional`，internal/cmdutil/flags.go:46-53）。`zpack crypto --bits 2048 genkey` 会把 `2048` 当成子命令名而报用法错误——子命令要放在最前面。`zpack crypto` 不带子命令、或子命令不是五种之一，都报：
  `用法: crypto genkey [--bits 2048] / crypto encrypt|decrypt --key-file <pem> / crypto password [--length 16] [--count 1] [--charset <字符集>] / crypto uuid [--count 1]`（cli/crypto/crypto.go:72-74）。
- **`--help` / `-h` 出现在任意位置都打印帮助并 exit 0**。帮助只有命令组层面的一份（Short 描述加 Usage 行），没有分子命令的详细说明——`zpack crypto password --help` 打印的也是 `crypto` 的帮助（internal/cliutil/help.go:15-23）。
- **值旗标只认「旗标 + 下一个参数」这一种写法**（`cmdutil.FlagValue`，internal/cmdutil/flags.go:25-32）。`--bits=4096` 这种等号写法不被识别，会被当成未知旗标静默忽略，然后取缺省值——实测 `crypto genkey --bits=4096` 生成的是 2048 位。只有两处例外支持等号写法：布尔旗标（`--upper=false`）与 `--charset=值`。
- **旗标的值可以是任何字符串，包括以 `-` 开头的**：取值时不看下一个参数的前缀。所以 `--bits -1` 会得到正常的报错（`…，收到: -1`），`--key "-----BEGIN PUBLIC KEY-----…"` 也能原样收到。反过来，`--bits --output x` 会把 `--output` 当成 `--bits` 的值。
- **同一个旗标给多次，第一次的生效。未知旗标静默忽略。**
- **旗标在末尾没有值时**：`--length` / `--count` / `--bits`、genkey 的 `--output`、encrypt / decrypt 的 `--output` 都当作没给，取缺省；`--key` / `--key-file` / `--file` 与 `hash` 的四个旗标报「缺少值」。
- **数值是严格的**：整个字符串必须是十进制整数（可带正负号）。`5abc`、`3x`、`1.5`、空串都算非法（`ParseIntStrict`，internal/cmdutil/flags.go:37-43）。

（全局规则见 [conventions.md](conventions.md)：退出码、stdout JSON、stderr 日志。这里只写本功能特有的部分。）

- **判定类失败**：`hash --expect` 不匹配、`hash --check` 有不匹配或缺失时，是「操作完成、结论为否」——JSON 带 `is_success:false` 与完整的判定字段，退出码 1（`JSONFailPrint`，internal/stdout/log.go:86-92）。脚本看退出码，管道与 GUI 只看字段。
- **纯文本输出**：`crypto password`、`crypto uuid`、`crypto encrypt`（无 `--output`）走 `TextPrint`，末尾带一个换行。`crypto decrypt`（无 `--output`）是例外：为了二进制安全，明文字节原样写 stdout，不追加换行，也不经过 `stdout` 包的 print 函数（cli/crypto/crypto.go:438-439，见 6.9）。
- **输出文件一律直接覆盖**，只有 `crypto genkey --output` 有「已存在就拒绝」的守卫（6.4）。
- **stderr**：本功能除了装配层的统一开始日志（`zpack crypto 开始执行` / `zpack hash 开始执行`）之外不输出任何过程日志（cli/crypto 下没有 `LoggerInfo` 调用）。私钥、密码、明文都不会进日志——开始日志只记命令名（cli/root.go:49-53）。

## 3. 输出契约

信封遵循 conventions.md；下面写的都是 `data` 载荷与 `error_info` 的取值。JSON 载荷全部是 struct，键序 = 声明序（cli/crypto/crypto.go:637-651，cli/crypto/hash.go:309-349）。

### 3.1 `crypto genkey`（exit 0）

**不带 `--output`**——密钥内容直接在 JSON 里：

```json
{"is_success":true,"data":{"bits":2048,"public_key":"-----BEGIN PUBLIC KEY-----\nMIIBIjANBg…\n-----END PUBLIC KEY-----\n","private_key":"-----BEGIN RSA PRIVATE KEY-----\nMIIEogIBAA…\n-----END RSA PRIVATE KEY-----\n","public_b64":"LS0tLS1CRUdJTi…","private_b64":"LS0tLS1CRUdJTi…"}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `bits` | int | 密钥位数（就是 `--bits` 的值） |
| `public_key` | string | 公钥 PEM 全文（`-----BEGIN PUBLIC KEY-----`，PKIX），末尾带换行 |
| `private_key` | string | 私钥 PEM 全文（`-----BEGIN RSA PRIVATE KEY-----`，PKCS#1），末尾带换行。**未加密的私钥明文** |
| `public_b64` | string | 公钥 PEM 全文的 Base64（单行）——可以原样喂给 `crypto encrypt --key` |
| `private_b64` | string | 私钥 PEM 全文的 Base64（单行）——可以原样喂给 `crypto decrypt --key` |

**带 `--output <前缀>`**——密钥落盘，JSON 里是两个路径：

```json
{"is_success":true,"data":{"bits":2048,"public_key":"E:\\keys\\kp_public.pem","private_key":"E:\\keys\\kp_private.pem"}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `bits` | int | 密钥位数 |
| `public_key` | string | 公钥文件路径：`<前缀>_public.pem` |
| `private_key` | string | 私钥文件路径：`<前缀>_private.pem` |

两个路径是「前缀 + 后缀」原样拼接，**不转绝对路径**（cli/crypto/crypto.go:319-321）——前缀给的是相对路径，JSON 里就是相对路径。

### 3.2 `crypto encrypt` / `crypto decrypt` 带 `--output`（exit 0）

```json
{"is_success":true,"data":{"output":"<路径>","size_bytes":N}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `output` | string | `--output` 给的路径，原样（不转绝对路径） |
| `size_bytes` | int | 写入文件的字节数。加密 = Base64 文本的长度（不含换行，文件里没有尾换行）；解密 = 明文的长度 |

### 3.3 `hash` 计算（exit 0）

```json
{"is_success":true,"data":{"algorithm":"sha256","files":[{"path":"E:\\data\\report.pdf","size_bytes":123456,"hash":"d4e7d483…"}]}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `algorithm` | string | 归一后的算法名（`sha256`） |
| `files[]` | array | 与输入文件一一对应、顺序相同 |
| `files[].path` | string | 文件的绝对路径 |
| `files[].size_bytes` | int | 文件大小（字节数） |
| `files[].hash` | string | 小写十六进制哈希，位数由算法决定：crc32 8 / md5 32 / sha1 40 / sha224 56 / sha256 64 / sha384 96 / sha512 128 |

### 3.4 `hash --expect`

匹配（exit 0）：

```json
{"is_success":true,"data":{"is_match":true,"algorithm":"sha256","path":"E:\\data\\report.pdf","size_bytes":123456,"hash":"d4e7d483…","expected":"d4e7d483…"}}
```

不匹配（**exit 1**，判定类失败）：

```json
{"is_success":false,"data":{"is_match":false,"algorithm":"sha256","path":"E:\\data\\report.pdf","size_bytes":123456,"hash":"d4e7d483…","expected":"00000000…"}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `is_match` | bool | 实算值与期望值是否相等 |
| `algorithm` | string | 算法名 |
| `path` | string | 文件的绝对路径 |
| `size_bytes` | int | 文件大小 |
| `hash` | string | 实算的哈希（不匹配时也在，便于对照） |
| `expected` | string | 期望值——**归一之后**的（已转小写、去首尾空白），不是用户敲的原样 |

### 3.5 `hash --output`（exit 0）

```json
{"is_success":true,"data":{"output":"E:\\data\\sums.txt","algorithm":"sha256","file_count":2}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `output` | string | 清单的绝对路径 |
| `algorithm` | string | 算法名 |
| `file_count` | int | 写进清单的文件数 |

清单文件本身是纯文本，每行 `<哈希><两个空格><文件路径>`，换行是 LF。路径是**命令行里原样给出的路径**（相对就写相对），不是绝对路径（cli/crypto/hash.go:219-236）。

### 3.6 `hash --check`

全部匹配（exit 0）：

```json
{"is_success":true,"data":{"checked":2,"matched":2,"mismatched":[],"missing":[]}}
```

有不匹配或缺失（**exit 1**，判定类失败）：

```json
{"is_success":false,"data":{"checked":3,"matched":1,"mismatched":[{"path":"E:\\data\\one.txt","algorithm":"sha256","expected":"2cf24dba…","actual":"9f86d081…"}],"missing":["E:\\data\\two.txt"]}}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `checked` | int | 清单里参与校验的条目数（空行与 `#` 注释行不算；缺失的文件也算） |
| `matched` | int | 匹配的条目数 |
| `mismatched[]` | array | 不匹配的条目；没有时是 `[]` |
| `mismatched[].path` | string | 文件的路径（相对路径已按清单所在目录展开） |
| `mismatched[].algorithm` | string | 按哈希长度判定的算法名 |
| `mismatched[].expected` | string | 清单里的期望值（已转小写） |
| `mismatched[].actual` | string | 实算值 |
| `missing[]` | array of string | 读不到的文件路径；没有时是 `[]` |

`missing` 收的是「文件读不到」的全部三种情况——路径不存在、路径是目录、读取失败——不区分（cli/crypto/hash.go:277-282）。

### 3.7 纯文本输出（无信封）

| 命令 | stdout |
|---|---|
| `crypto encrypt`（无 `--output`） | 密文的 Base64（标准字母表、带填充）+ 一个换行 |
| `crypto decrypt`（无 `--output`） | 明文的原始字节，不追加换行 |
| `crypto password` | 每行一个密码 + 末尾一个换行（`--count N` 共 N 行） |
| `crypto uuid` | 每行一个 UUID + 末尾一个换行 |

### 3.8 失败（exit 1，`{"is_success":false,"error_info":"..."}`）

**`crypto` 通用**

| `error_info` | 触发条件 | 出处 |
|---|---|---|
| `用法: crypto genkey [--bits 2048] / … / crypto uuid [--count 1]`（全文见 2.6） | 没给子命令，或子命令不认识 | cli/crypto/crypto.go:73 |

**`crypto genkey`**

| `error_info` | 触发条件 | 出处 |
|---|---|---|
| `--bits 需要 512-4096 的 256 倍数整数，收到: <值>` | 不是整数，或不在范围内，或不是 256 的倍数 | cli/crypto/crypto.go:300 |
| `密钥生成失败: <标准库错误>` | `rsa.GenerateKey` 失败——目前只有 512 / 768 位：`密钥生成失败: crypto/rsa: 512-bit keys are insecure (see https://go.dev/pkg/crypto/rsa#hdr-Minimum_key_size)` | cli/crypto/crypto.go:303-306 |
| `密钥文件已存在: <路径>` | `--output` 的两个目标文件里任一个已存在（先查私钥路径） | cli/crypto/crypto.go:322-326 |
| `无法写入私钥文件: <系统错误>` / `无法写入公钥文件: <系统错误>` | 写文件失败（目录不存在等） | cli/crypto/crypto.go:327-332 |

**`crypto encrypt` / `crypto decrypt`**（按检查的先后）

| `error_info` | 触发条件 | 出处 |
|---|---|---|
| `需要公钥：--key <PEM文本> 或 --key-file <路径>` / `需要私钥：…` | 两个密钥旗标都没给 | cli/crypto/crypto.go:352-355、413-416 |
| `--key 缺少值` / `--key-file 缺少值` | 旗标在末尾、后面没有值 | cli/crypto/crypto.go:574-582 |
| `无法读取密钥文件: <系统错误>` | `--key-file` 读不了 | cli/crypto/crypto.go:583-586 |
| `无法解析公钥: <原因>` / `无法解析私钥: <原因>` | 密钥内容不是可识别的 RSA 密钥。`<原因>` 取值见 4.1.2 | cli/crypto/crypto.go:356-359、417-420 |
| `--file 缺少值` | `--file` 在末尾 | cli/crypto/crypto.go:597-599 |
| `无法读取文件: <系统错误>` | `--file` 读不了 | cli/crypto/crypto.go:600-603 |
| `加密失败: <标准库错误>` | RSA 加密失败。用小于 1024 位的公钥时是 `加密失败: crypto/rsa: 512-bit keys are insecure (…)` | cli/crypto/crypto.go:362-365 |
| `无效的 Base64 密文: <错误>`（仅解密） | 输入不是合法的 Base64 | cli/crypto/crypto.go:422-425 |
| `解密失败（密钥不匹配或数据损坏）: <错误>`（仅解密） | 解密失败。`<错误>` 常见取值：`crypto/rsa: decryption error`（密钥不对或密文坏了）、`混合加密数据不完整`、`混合加密数据过短`、`AES 数据过短`、`cipher: message authentication failed`；私钥小于 1024 位时是 `crypto/rsa: …-bit keys are insecure (…)`（标准库对加解密同样有下限，见 6.5） | cli/crypto/crypto.go:426-429、452-478 |
| `无法写入文件: <系统错误>` | `--output` 写不了 | cli/crypto/crypto.go:369-371、432-434 |

密钥检查在数据之前：密钥不对时，即使数据也有问题，报的也是密钥的错。

**`crypto password`**

| `error_info` | 触发条件 | 出处 |
|---|---|---|
| `--length 需要 4-128 整数，收到: <值>` | `--length` 非法或越界 | cli/crypto/crypto.go:168-169 |
| `--count 需要 1-100 整数，收到: <值>` | `--count` 非法或越界 | cli/crypto/crypto.go:177 |
| `<旗标> 需要 true/false，收到: <值>` | 布尔旗标的值不认识 | cli/crypto/crypto.go:109 |
| `--charset 需要非空的字符集（合法 UTF-8），收到: <带引号的值>` | `--charset` 为空串或不是合法 UTF-8 | cli/crypto/crypto.go:189 |
| `至少启用一个字符类（--lower/--upper/--digit/--symbol 不能全部为 false），或用 --charset 指定自定义字符集` | 四类全关且没有 `--charset` | cli/crypto/crypto.go:195 |
| `随机源失败: <错误>` | 系统随机源不可用（正常不会发生） | cli/crypto/crypto.go:201 |

**`crypto uuid`**

| `error_info` | 触发条件 | 出处 |
|---|---|---|
| `--count 需要 1-100 整数，收到: <值>` | `--count` 非法或越界 | cli/crypto/crypto.go:258 |
| `<旗标> 需要 true/false，收到: <值>` | `--upper` / `--no-dash` 的值不认识 | cli/crypto/crypto.go:109 |
| `随机源失败: <错误>` | 系统随机源不可用 | cli/crypto/crypto.go:267 |

**`hash`**

| `error_info` | 触发条件 | 出处 |
|---|---|---|
| `--algorithm 缺少值` / `--expect 缺少值` / `--output 缺少值` / `--check 缺少值` | 旗标在末尾 | cli/crypto/hash.go:83-104 |
| `未知算法: <归一后的名字>（支持 md5/sha1/sha224/sha256/sha384/sha512/crc32）` | 算法名认不出 | cli/crypto/hash.go:116 |
| 互斥与数量规则的五条文案 | 见 2.5 的表 | cli/crypto/hash.go:122、127、130、135、140 |
| `路径不存在：<绝对路径>` | 文件不存在（注意冒号是全角的「：」） | cli/crypto/hash.go:182 |
| `不支持对目录计算哈希：<绝对路径>` | 路径是目录 | cli/crypto/hash.go:185 |
| `读取文件失败: <系统错误>` | 打开或读取出错 | cli/crypto/hash.go:189 |
| `--expect 期望 <位数> 位十六进制（<算法>），收到 <实际位数> 位` | 期望值长度不对 | cli/crypto/hash.go:198 |
| `--expect 不是合法十六进制: <原值>` | 期望值含非十六进制字符 | cli/crypto/hash.go:201 |
| `无法写入清单: <系统错误>` | `--output` 写不了 | cli/crypto/hash.go:232 |
| `无法读取清单: <系统错误>` | `--check` 的清单读不了 | cli/crypto/hash.go:243 |
| `清单第 N 行无法解析: <该行原文>` | 清单某行没有空格 | cli/crypto/hash.go:260 |
| `清单第 N 行哈希长度 <长度> 无法判定算法` | 清单某行的哈希长度不属于七种算法之一 | cli/crypto/hash.go:271 |

多文件计算时任一文件出错就整体失败，不会输出部分结果。

### 3.9 stderr

只有统一开始日志一行：`[YYYY-MM-DD HH:MM:SS] zpack crypto 开始执行` 或 `… zpack hash 开始执行`。没有进度、没有警告。

## 4. 行为细则

### 4.1 正常路径

#### 4.1.1 `crypto genkey`

1. 解析 `--bits`（缺省 2048）并校验范围。
2. 生成密钥（`rsa.GenerateKey`）。本机实测（含进程启动）：1024 位约 0.35 秒，1280 位约 0.35 秒，4096 位约 1.2 秒。
3. 编码成 PEM：公钥 `-----BEGIN PUBLIC KEY-----`（PKIX），私钥 `-----BEGIN RSA PRIVATE KEY-----`（PKCS#1）。
4. 没有 `--output` → 输出 3.1 的第一种 JSON。
5. 有 `--output <前缀>` → 检查 `<前缀>_private.pem` 与 `<前缀>_public.pem` 是否已存在（存在就拒绝，两个文件都不动）→ 写私钥（权限 `0600`）→ 写公钥（权限 `0644`）→ 输出 3.1 的第二种 JSON（cli/crypto/crypto.go:294-341）。

注意「已存在」的检查发生在密钥**生成之后**——即使注定要拒绝，也会先花一次生成密钥的时间。

#### 4.1.2 密钥的识别（encrypt / decrypt 共用）

| 输入形态 | 怎么处理 |
|---|---|
| PEM 文本（内容里含 `-----BEGIN`） | 取**第一个** PEM 块（块类型不作检查），解出 DER |
| Base64 编码的 PEM | 先去掉换行、空格、制表符（兼容 `macOS base64` 命令的多行输出），Base64 解码后当作 PEM，再取第一个块 |
| 公钥 DER | 先试 PKIX（`PUBLIC KEY`），不行再试 PKCS#1（`RSA PUBLIC KEY`） |
| 私钥 DER | 先试 PKCS#1（`RSA PRIVATE KEY`），不行再试 PKCS#8（`PRIVATE KEY`） |

出处：cli/crypto/crypto.go:482-566。识别失败时 `<原因>` 的取值：

| `无法解析公钥: <原因>` / `无法解析私钥: <原因>` | 情形 |
|---|---|
| `无效的 PEM 格式` | 文本里有 `-----BEGIN` 但不是合法 PEM 块 |
| `无法识别的公钥格式（需要 PEM 或 Base64 编码 PEM）` / `无法识别的私钥格式` | 既不含 `-----BEGIN`，又不能当 Base64 解开（例如 OpenSSH 格式的 `ssh-rsa AAAA…`） |
| `Base64 解码后不是有效的 PEM` | Base64 解开了，但解出来的不是 PEM |
| `不是 RSA 公钥` / `不是 RSA 私钥` | DER 是合法的密钥，但不是 RSA（PKIX / PKCS#8 里装了别的算法） |
| `无法解析公钥 DER 数据` / `无法解析私钥 DER 数据` | DER 两种格式都解不了——包括把公钥当私钥用（或反过来）、带口令的加密私钥（`ENCRYPTED PRIVATE KEY`） |

实测可用：OpenSSL 生成的 PKIX 公钥与 PKCS#8 私钥（OpenSSL 3 的默认形态）都能直接用；`crypto genkey` 输出里的 `public_b64` / `private_b64` 也可以原样传给 `--key`。

#### 4.1.3 `crypto encrypt`

1. 取密钥（2.2）并解析成 RSA 公钥。
2. 取明文：`--file` > 位置参数 > stdin 的全部字节。stdin 没有重定向也没有管道时会一直等输入。
3. 按明文长度选方式（`rsaEncrypt`，cli/crypto/crypto.go:378-408）：

| 明文长度 | 方式 | 输出 |
|---|---|---|
| ≤ 模长字节数 − 66 | 直接 RSA-OAEP（SHA-256，label 为空，MGF1 也是 SHA-256） | 密文长度 = 模长字节数 |
| 更长 | 混合加密：RSA-OAEP 保护一把 32 字节的 AES-256 会话密钥，数据用 AES-256-GCM 加密 | 见下面的布局 |

   上限随密钥位数变：1024 位 → 62 字节，2048 位 → 190 字节，4096 位 → 446 字节。实测 2048 位密钥：190 字节走直接模式，191 字节起走混合。

4. 结果做 Base64（标准字母表、带填充）。
5. 有 `--output` → 把 Base64 文本写进文件（不追加换行）并输出 3.2 的 JSON；否则输出 3.7 的纯文本。

混合加密的二进制布局（整体再做 Base64）：

| 偏移 | 长度 | 内容 |
|---|---|---|
| 0 | 6 | 魔数，ASCII 的 `ZPACK:` |
| 6 | 2 | 被加密的会话密钥的长度，大端 16 位（等于模长字节数；2048 位是 256） |
| 8 | 上一段的长度 | RSA-OAEP 加密后的会话密钥 |
| 之后 | 12 | AES-GCM 的随机 nonce |
| 之后 | 16 + 明文长度 | AES-256-GCM 的密文，末尾 16 字节是认证标签；没有附加认证数据 |

每次加密都带随机数（OAEP 的填充、GCM 的 nonce），所以**同一份明文每次的密文都不同**——这一点与压缩包的「同输入产出字节确定」不一样。

#### 4.1.4 `crypto decrypt`

1. 取密钥并解析成 RSA 私钥。
2. 取密文：`--file` > 位置参数 > stdin。内容先去掉首尾空白（粘贴时带的换行没关系），再做 Base64 解码。
3. 解码后的字节以 `ZPACK:` 开头且长度大于 6 → 按混合格式解；否则按直接 RSA-OAEP 解（`rsaDecrypt`，cli/crypto/crypto.go:442-478）。
4. 有 `--output` → 明文字节写进文件并输出 3.2 的 JSON；否则明文字节原样写 stdout（cli/crypto/crypto.go:412-440）。

「密钥不匹配或数据损坏」是同一条文案：RSA-OAEP 解不开、GCM 认证不过、混合格式不完整，都归到这里。实测：密文被截短时报 `…: 混合加密数据不完整`。

#### 4.1.5 `crypto password`

生成一个密码的步骤（`cryptoGeneratePassword`，cli/crypto/crypto.go:208-236）：

1. 算出候选池：把启用的字符类的字符并在一起，去重，按首次出现的顺序（内置四类按 小写、大写、数字、符号 的顺序，自定义字符集排最后）。
2. **保底**：每个启用的字符类先各随机取 1 个字符。
3. 不足 `--length` 的部分，从候选池里随机取。
4. Fisher–Yates 洗牌。
5. 取前 `--length` 个字符。

- 随机源是 `crypto/rand`；取字符按 rune 取，所以自定义字符集可以含中文等非 ASCII 字符，长度也按字符数算。
- `--charset` 是第五个字符类：同样保底 1 个字符（2026-11-23 为 GUI 随机生成页的「自定义」分组而加）。
- `--count N` 重复 N 次，逐行输出。

#### 4.1.6 `crypto uuid`

每个 UUID 取 16 个随机字节，把第 6 字节高 4 位设成 `0100`（版本 4）、第 8 字节高 2 位设成 `10`（RFC 4122 变体），格式化成小写 `8-4-4-4-12`（`cryptoGenerateUUID`，cli/crypto/crypto.go:280-290）。`--no-dash` 去掉连字符；`--upper` 转大写；两者可同用。

#### 4.1.7 `hash` 的四种形态

**计算**：对每个文件转绝对路径 → 检查存在且不是目录 → 以 1MB 为块流式读取（内存有界）→ 输出 3.3 的 JSON。多文件逐条计算，顺序与命令行一致。

**`--expect`**：先校验期望值（长度、十六进制），再算文件哈希，比对。匹配 exit 0，不匹配 exit 1，JSON 结构相同（3.4）。

**`--output`**：对每个文件计算后写一行 `<哈希>  <命令行里给出的路径>`。这是 shasum 的文本格式，`shasum -a N -c` / `md5sum -c` 可以直接验（crc32 没有对应的 shasum 档位）。清单用 LF 换行。

**`--check`**（`hashCheckManifest`，cli/crypto/hash.go:238-307），对清单逐行处理：

1. 去首尾空白（所以 CRLF 换行的清单也能读）；空行、以 `#` 开头的注释行跳过。
2. 第一个空格之前是期望哈希（转小写），之后是文件路径。shasum 的两种分隔都认：`<哈希>  <路径>`（文本模式）与 `<哈希> *<路径>`（二进制模式）。
3. 算法按期望哈希的**长度**判定：8 = crc32、32 = md5、40 = sha1、56 = sha224、64 = sha256、96 = sha384、128 = sha512。一份清单里可以混用算法；`--algorithm` 在这个形态里不起作用。期望值本身不检查是不是十六进制，只看长度。
4. 路径是绝对路径就原样用；相对路径以**清单所在的目录**为基准。
5. 文件读不到（不存在 / 是目录 / 读取出错）→ 记进 `missing`；哈希不等 → 记进 `mismatched`；相等 → `matched` 加一。
6. 全部匹配 → exit 0；否则 exit 1。清单里一条可校验的行都没有（空清单）也算成功：`checked:0, matched:0`。

### 4.2 边界与退化输入

| 情形 | 行为 |
|---|---|
| `--bits 512` / `--bits 768` | 通过参数校验，但生成阶段被标准库拒绝：`密钥生成失败: crypto/rsa: 512-bit keys are insecure (…)` |
| `--bits 1024` / `1280` / `2560` 等 | 可以生成（实测 1024、1280、4096 均成功）——GUI 只提供 1024 / 2048 / 4096 三档，CLI 的范围更宽 |
| `--bits=4096`（等号写法） | 不被识别，静默取缺省 2048 |
| `crypto genkey --output ""` | 前缀为空 → 在当前目录生成 `_private.pem` 与 `_public.pem` |
| `crypto genkey --output` 的目标已存在 | 拒绝，两个文件都不动（哪怕只存在其中一个） |
| `crypto encrypt` / `decrypt` 的 `--output` 目标已存在 | 直接覆盖 |
| 明文以 `-` 开头，又把它当位置参数传 | 被当成未知旗标忽略，转而读 stdin；stdin 为空时就**加密了空内容而不报错**（见 6.10）。这类内容请用 `--file` 或 stdin |
| 明文为空（空文件 / 空 stdin） | 可以加密（2048 位密钥下得到 256 字节的直接 RSA 密文），解密得到空 |
| 明文恰好是直接模式的上限（2048 位 → 190 字节） | 走直接模式；多 1 字节走混合 |
| 密文前后有空白 / 换行 | 解密前会去掉，可以直接粘贴 |
| 用公钥去解密、用私钥去加密 | 报 `无法解析私钥: 无法解析私钥 DER 数据` / `无法解析公钥: 无法解析公钥 DER 数据` |
| 外部生成的小于 1024 位的公钥 | 加密时报 `加密失败: crypto/rsa: …-bit keys are insecure (…)` |
| `--key` 与 `--key-file` 同给 | 先出现的生效 |
| `crypto password --length 4`（四类全开） | 恰好每个字符类出一个字符 |
| 启用的字符类数 **大于** `--length` | 保底不完全：先生成「每类一个」，再截到 `--length`，被截掉的是随机的某一类。实测 `--length 4 --charset 中文`（五类）的结果里未必有中文 |
| `--charset` 的值以 `-` 开头 | 照收（`--charset "-_+"`）——`--charset` 后面的下一个参数一律当字符集 |
| `--length 3` / `--length 129` / `--count 0` / `--count 101` | 报错，exit 1 |
| `--length 5abc` / `--count 3x` / `--bits 1024abc` | 报错，exit 1（2026-11-24 起严格校验，见 6.8） |
| `crypto uuid --upper false` | 显式关掉，输出小写 |
| `hash` 的文件路径以 `-` 开头 | 没法当位置参数传（会被当成旗标忽略） |
| `hash` 多个文件里有一个读不了 | 整体失败，不输出部分结果 |
| `hash --expect` 的期望值是大写 / 带首尾空白 | 归一（小写、去空白）后比对，可以直接贴官方给的大写哈希 |
| `hash --expect` 长度对但不是十六进制 | 报 `--expect 不是合法十六进制: <原值>` |
| `hash --check` 的清单是空文件 / 只有注释 | 成功：`{"checked":0,"matched":0,"mismatched":[],"missing":[]}` |
| `hash --check` 的清单路径是相对路径 | 以当前目录为基准找清单；清单**里面**的相对路径以清单所在目录为基准 |
| 清单里的相对路径：zpack 与 shasum 的基准不同 | zpack 以清单所在目录为基准；`shasum -c` 以**当前目录**为基准。想两边通用，就在清单所在目录里生成相对路径的清单（或一律用绝对路径） |

### 4.3 错误处理

- 所有失败的 `error_info` 见 3.8。参数错误在动手之前就报出来，不会留下半成品。
- **判定类失败**（`hash --expect` 不匹配、`hash --check` 有不符）不是「出错」：操作完成了，结论为否。JSON 里带完整的判定字段，退出码 1。调用方的约定是脚本用退出码、管道与 GUI 只信 JSON 字段（conventions.md 第 3.2 节）。
- **`crypto decrypt` 的失败文案是合并的**：「密钥不匹配或数据损坏」。具体原因跟在冒号后面（3.8），但文案本身不区分是密钥不对还是密文坏了。
- **写文件失败的残留**：
  - `crypto genkey --output`：私钥写成功、公钥写失败时，私钥文件会留在原地（没有回滚，cli/crypto/crypto.go:327-332）。
  - `crypto encrypt` / `decrypt --output`、`hash --output`：一次 `WriteFile` 写完，失败时不会有半截文件【待确认】——`os.WriteFile` 在磁盘满等情况下可能留下部分内容，代码没有先写临时文件再改名。
- 系统错误（文件不存在之类）的文案是 Go 原始错误的原文，随系统语言而变（中文 Windows 上是英文 `The system cannot find the path specified.`）。

## 5. 平台差异

| 能力 | Windows | macOS | Linux |
|---|---|---|---|
| 实现 | 全部是 Go 标准库，三个平台行为一致，没有平台分支 | 同左 | 同左 |
| 私钥文件权限 `0600` | 不体现：Windows 没有 Unix 权限位，Go 只在「没有写位」时才置只读属性，`0600` 有写位，所以什么都不发生 | 生效：只有本人可读写 | 生效 |
| 系统错误的文案 | 随系统语言。本机（中文 Windows）实测是英文，如 `The system cannot find the path specified.` | 随系统语言 | 随系统语言 |
| 清单文件的换行 | 写 LF；读取时逐行去首尾空白，CRLF 的清单也能读 | 同左 | 同左 |
| 官方哈希的大小写 | `certutil -hashfile`、PowerShell `Get-FileHash` 输出大写——`--expect` 会先转小写，可以直接用 | 工具多为小写 | 同左 |
| Git Bash 下传给 exe 的路径 | 以 `/` 开头的参数会被 MSYS 自动转成 Windows 路径（`/tmp/x` → `C:/Users/…/Temp/x`）。而 python3、perl 把 `/tmp/…` 当成另一个目录（`C:\tmp\…`）——两套坐标错位。AGENTS.md:288 记录：verify.sh 在 Git Bash 上剩余的 6 个具名失败都是这个原因造成的，其中包含 crypto 链路里由 python3 落盘密钥文件的那几例；同一批操作在 Windows 坐标下手动验证全部正常 | — | — |
| stdin 传二进制 | 管道里可以传任意字节（`--file` 不用时） | 同左 | 同左 |
| 可用的最小密钥 | 取决于**构建所用的 Go 版本**，与运行平台无关（见 6.5） | 同左 | 同左 |

**纯 Go 构建与完整版（cgo 构建）在本功能上没有差别。** 命令面就是全部代码，只 import 标准库与仓库内的 `internal/cliutil`、`internal/cmdutil`、`internal/stdout`（cli/crypto/crypto.go:18-40，cli/crypto/hash.go:10-28），没有任何构建标签。

## 6. 设计与陷阱

**改动这个功能之前必须知道的事。**

### 6.1 RSA 方案的取舍（cli/crypto/crypto.go:1-15）

这个功能是照着「在 JS 里不得不选 jsencrypt / jsrsasign」的痛点反着设计的（verify.sh:596 的注释称它为「甜点功能」）：

| 取舍 | zpack 的做法 | 对照 |
|---|---|---|
| 填充 | OAEP-SHA256 | jsencrypt 默认是 PKCS#1 v1.5，较旧 |
| 大数据 | 自动混合加密：RSA 只保护一把 AES-256-GCM 会话密钥，数据交给 AES | jsencrypt 只能加密 ≤ 密钥字节数 − 42 的数据，超长直接失败 |
| 密钥格式 | PEM 输出；输入自动识别 PEM 与 Base64 编码的 PEM | — |
| 缺省位数 | 2048（安全与速度的平衡点） | — |

### 6.2 混合加密的容器是自家的格式

- 区分两种方式靠魔数：解码后的字节以 `ZPACK:` 开头就是混合格式，否则是直接 RSA（cli/crypto/crypto.go:45、443-450）。
- 这个容器**没有版本号、没有算法标识**——AES-256-GCM、OAEP-SHA256、nonce 长度都是写死的。它不是 JWE 之类的标准格式，混合密文只有 zpack 自己能解开。
- 直接模式是标准的 RSA-OAEP（SHA-256），理论上可以与 OpenSSL、WebCrypto 之类互通【待确认】——本次核对没能实测（zpack 用 OpenSSL 生成的密钥加解密是通的，但「外部工具加密、zpack 解密」的方向没验成）。
- AGENTS.md 的「不做私有格式（互操作即护城河）」说的是归档格式；加密这边只有直接模式是对外互通的，混合模式是自家容器。这是否符合预期【待确认】。
- 理论上有一处歧义：直接 RSA 的密文恰好以 `ZPACK:` 开头（概率 2⁻⁴⁸）会被当成混合格式去解，结果是解密失败。可以忽略。
- **改动含义**：改布局、魔数或算法，旧密文就解不开了，而且没有版本字段可以做平滑迁移。真要改，就把版本放进魔数（例如 `ZPACK2:`）并保留旧分支。

### 6.3 密钥格式（cli/crypto/crypto.go:343-347、482-566）

- 输出：公钥用 PKIX（「最通用」），私钥用 PKCS#1。
- 输入：公钥先试 PKIX 再试 PKCS#1；私钥先试 PKCS#1 再试 PKCS#8。所以 OpenSSL 3 默认生成的 PKCS#8 私钥（`-----BEGIN PRIVATE KEY-----`）可以直接用。
- 「Base64 编码的 PEM」这种输入形态是为了让密钥能作为单行字符串传递——`genkey` 输出里的 `public_b64` / `private_b64` 就是它，GUI 与 Agent 不用处理多行 PEM。
- 不支持的：裸 DER 文件（必须是 PEM 或 Base64 的 PEM）、OpenSSH 格式的公钥、带口令的加密私钥（`ENCRYPTED PRIVATE KEY`）。加密私钥会落到「无法解析私钥 DER 数据」，文案里没有提示「不支持带口令的私钥」。

### 6.4 `genkey --output` 的覆盖守卫

- **规则**（2026-11-22 GUI 议题定案）：两个目标文件里任一个已存在就拒绝，不覆盖（cli/crypto/crypto.go:316-326）。理由写在代码注释里：覆盖旧密钥会让旧公钥加密的数据**永久无法解开**，静默覆盖就是数据丢失——让用户换名字或先删除旧文件。
- 密钥不同于普通产物：丢了可以重新生成，但被覆盖的那把回不来，用它加密过的数据也就回不来。所以只有 `genkey` 有这个守卫；`encrypt` / `decrypt` / `hash` 的 `--output` 都是直接覆盖。
- GUI 侧还有一道前置守卫：调引擎之前先对两个目标文件各做一次 `os.Stat`，任一存在直接红行拒绝，不再调引擎——注释里称之为「m05014 血案补丁」，为的是即使引擎是没有守卫的旧版也不会静默覆盖（ui/gui/main.go:2515-2523）。
- 验收守着两件事：被拒绝时旧文件原封不动（verify.sh:606-613）。
- **改动含义**：不要为了「方便」把它改成覆盖，或者加一个不经确认的 `--force`。

### 6.5 密钥位数：三处口径不一致

| 出处 | 说法 |
|---|---|
| 参数校验（cli/crypto/crypto.go:299） | 512–4096，256 的倍数 |
| 文件头注释（cli/crypto/crypto.go:8） | 「默认 2048，支持 1024 / 2048 / 4096」 |
| GUI（ui/gui/DESIGN.md:171-173） | 只给 1024 / 2048 / 4096 三档，理由是「Go 标准库拒生成 < 1024 位密钥，故 GUI 三档」 |
| Go 标准库（`crypto/rsa` 的 Minimum key size） | 小于 1024 位：生成报错，加密、解密、签名、验签也全部报错（`GODEBUG=rsa1024min=0` 可以关掉这个限制） |

- 实测：`--bits 512` 与 `--bits 768` 通过了参数校验，然后死在生成阶段——`密钥生成失败: crypto/rsa: 512-bit keys are insecure (see https://go.dev/pkg/crypto/rsa#hdr-Minimum_key_size)`。外部生成的 512 位公钥在 `crypto encrypt` 时同样被拒。所以 `--bits 需要 512-4096 的 256 倍数整数` 这条报错文案给出的下限是达不到的。
- 1024、1280、4096 都能生成——CLI 实际允许的档位比注释与 GUI 都宽。
- 这个下限来自构建所用的 Go 版本，不是本功能自己定的；用没有这个限制的旧版 Go 构建，512 / 768 才可能通过。是否把校验下限提到 1024、文案同步改掉【待确认】（见第 9 节）。

### 6.6 密码生成（cli/crypto/crypto.go:160-249）

- **保底 + 填充 + 洗牌**：每个启用的字符类先各取一个字符，再从并集里随机补足长度，最后整体洗牌。洗牌是必须的——保底字符是按字符类的顺序追加的，不洗牌的话密码的前几位永远是「小写、大写、数字、符号」的固定排列。
- **按 rune 取字符**：自定义字符集可以含非 ASCII 字符，长度按字符数算（`--length 8 --charset 中文密码` 得到 8 个汉字）。这是 2026-11-23 为 GUI 随机生成页的「自定义」分组加的（提交 329e4fb），当时把四个开关换成了原生多选加自定义符号分组。
- **随机源是 `crypto/rand`**，整数取样用 `rand.Int`（本身就是无偏的）。`cryptoRandRune` 里留着一个只跑一轮的 `for` 循环，注释写的是「拒绝采样防模偏」——实际的无偏性来自 `rand.Int`，循环是个空架子（cli/crypto/crypto.go:238-249）。
- **保底的边界**：启用的字符类数大于 `--length` 时保底不成立（4.2）。内置四类加自定义共五类，所以 `--length 4` 配 `--charset` 时总有一类要被截掉。
- **候选池去重**：自定义字符集与内置类重叠的字符在池里只留一份，但保底取字是按各自的字符集取的，所以重叠的类各自仍然保底。

### 6.7 `hash` 的设计（cli/crypto/hash.go:1-8，AGENTS.md:140-143）

- **纯标准库、零新依赖**；1MB 流式读取，任意大的文件内存有界。
- **crc32 是 IEEE 多项式**——与 zlib、zip 里用的是同一个，便于和压缩包的 CRC 对照。
- **算法名归一**：转小写、去连字符，`SHA-256` 与 `sha256` 等价，用户不必记写法。
- **四形态**（2026-11-04）：计算、`--expect` 校验、`--output` 清单、`--check` 批量校验。
- **`--expect` 不匹配是「判定类失败」**：exit 1，但 JSON 带完整字段（`is_match` / `hash` / `expected`）。设计意图写在注释里——「脚本用退出码、GUI 用字段」。这是「exit 0 ⇔ `is_success:true`」的例外：判定类命令（本功能的 `hash --expect` / `hash --check`，以及压缩包的 `test`）退出码跟随结论。
- **清单用 shasum 的文本格式**（`<哈希><两个空格><路径>`），为的是与 `shasum -a N -c` / `md5sum -c` 双向互通；验收里两个方向各有一例（verify.sh:790-796）。
- **`--check` 按哈希长度判算法**：七种算法的十六进制位数（8 / 32 / 40 / 56 / 64 / 96 / 128）互不相同，所以一份清单可以混用算法，不需要指定。`hashAlgorithmByHexLength` 是遍历 map 找第一个长度匹配的（cli/crypto/hash.go:51-60）——**以后若新增算法与现有算法位数相同（例如 sha3-256 也是 64 位），判定就会变得不确定**，到时候得换一种判法（比如看清单文件名约定，或要求显式 `--algorithm`）。
- **相对路径以清单所在目录为基准**：清单放在哪就能在哪验，不必切到别的目录。这与 `shasum -c` 的基准（当前目录）不同（4.2）。

### 6.8 数值参数严格校验的补漏（提交 6016f9b，2026-11-24）

- **契约**：数值参数必须严格校验，非法 → error JSON + exit 1（conventions.md 第 2 节）。
- **漏洞**：`--length` / `--count` / `--bits`（以及不属于本功能的 `bench --size`）此前用的是 `fmt.Sscanf("%d")`——**只看前缀**。`zpack crypto password --length 5abc` 被当成 5，`crypto uuid --count 3x` 被当成 3，`genkey --bits 1024abc` 被当成 1024，都不报错。容易误导的是，那个本地函数就叫 `parseIntStrict`，注释写的是「本地版本（避免 cmdutil 的 import cycle）」——而真正严格的 `ParseIntStrict` 早就在 `internal/cmdutil` 里了。
- **修法**：统一改用 `cmdutil.ParseIntStrict`（内部就是 `strconv.Atoi`：整串必须是十进制整数，可带正负号），删掉本地的假「strict」。补了单测 `TestParseIntStrict`（`"5abc"`、`"3x"`、`"1.5"`、`""` 都必须判非法，internal/cmdutil/flags_test.go:5-30）与门禁（verify.sh:1164-1174：crypto 三例——`--length 5abc`、`--count 3x`、`--bits 1024abc`——加 bench 一例）。
- **教训**：「严格」要靠反例测试守着，不能靠函数名。新增数值旗标一律走 `ParseIntStrict`。
- **两处提醒**：
  - conventions.md 第 9 节仍把 crypto 的这条列为「漏网点」，已经过时（6016f9b 已修）。
  - 开发期的 `bin/zpack-dev.exe` 如果是在这个提交之前构建的，表现仍是旧行为。本次核对时它正是如此（14:26 构建，早于 17:11 的提交），所以严格校验这一条只能以代码与门禁为据。

### 6.9 输出通道的例外：`decrypt` 裸写 stdout

`stdout` 包的约束是「禁止任何包裸写 stdout」（internal/stdout/log.go:3-4）。cli 里只有两处违反，都是「不带 `--output` 时输出二进制内容」：`crypto decrypt`（cli/crypto/crypto.go:438-439）与 `base64 decode`（cli/tools/base64.go:122-123）。前者是：

```go
fmt.Fprintf(os.Stdout, "%s", plaintext)
os.Exit(0)
```

- 原因：`TextPrint` 用的是 `Println`，会追加一个换行——对文本无所谓，对二进制明文就是破坏。所以这里绕开了输出层。
- 它手工满足了「print 即 exit」（紧跟 `os.Exit(0)`），stdout 上不会出现第二个输出。
- **改动含义**：如果以后给 `stdout` 层加一个不加换行的二进制输出函数，应该把这两处一起收回去。

### 6.10 以 `-` 开头的文本会被静默丢弃

`crypto encrypt` 的数据来源里，「第一个不以 `-` 开头的位置参数」这一条有坑：明文以 `-` 开头（例如 Markdown 列表 `- 项`）时，它会被当成未知旗标忽略，然后转去读 stdin；stdin 是空的就加密了空内容，**不报错**（`cryptoResolveData`，cli/crypto/crypto.go:594-626）。

- 2026-11-23 的 GUI 审计发现了这个问题：加密页悄悄加密了空内容、Base64 页点了没反应。修法在 GUI 侧——文本一律经 stdin 送引擎，不再走 argv；顺带避开了命令行长度上限（Windows 命令行 32767 字符）（ui/gui/service.go:938-941，提交 329e4fb）。
- **CLI 侧没有修**：没有 `--` 分隔符，也没有「位置参数被当成旗标」的警告。Agent 调用时的约定是——文本内容走 stdin 或 `--file`，不要当位置参数传。

### 6.11 与 GUI 的耦合点

GUI 不直连代码，全部经这两条命令的 stdout 驱动（细节见 gui spec）。会反过来约束本功能的耦合点：

- **`Service.RunTool`**（文件态）读信封里的 `data.output`、`data.size_bytes`、`data.private_key`、`data.public_key`（ui/gui/service.go:733-772）：`private_key` 非空就当作 genkey 的结果，两把钥匙都展示给用户。所以 `genkey --output` 输出里的两个字段虽然叫 `public_key` / `private_key`，值是**路径**，GUI 按路径展示。
- **`Service.RunToolStdin`**（文本态）把文本喂给引擎的 stdin，读 stdout 的裸文本，末尾换行会去掉（ui/gui/service.go:938-971）。失败信封（`is_success:false`）也在 stdout，GUI 直接展示 `error_info`。它依赖 3.7 的纯文本契约：`encrypt` 输出 Base64、`decrypt` 输出明文。
- **`Service.HashExec`** 读 `data.files[]`（`path` / `size_bytes` / `hash`）与 `data.is_match`（ui/gui/service.go:783-826）。`--expect` 不匹配时引擎出的是 `is_success:false` + `data`，GUI 照样解析——**判定类失败必须带回 data**，否则哈希页在「不匹配」时什么都显示不出来。
- **GUI 拼出来的参数**：
  - 加密 / 解密：`crypto encrypt|decrypt --key-file <钥匙> [--file <输入> --output <输出>]`，文本态不带数据参数（ui/gui/main.go:2246、2259、2358、2371）。
  - 生成密钥：`crypto genkey --bits <1024|2048|4096> --output <目录/名字>`（ui/gui/main.go:2524）。
  - 文件哈希：`hash --algorithm <算法> <文件...> [--expect <小写的期望值>]`——只有恰好一个文件且填了期望值时才带 `--expect`（ui/gui/main.go:3946-3959）。
  - 密码：`crypto password --length <N> [--<类> false]… [--charset <字符集>]`——没勾的内置类显式关掉，自定义分组合成一个 `--charset`（ui/gui/main.go:4173-4184）。GUI 用的是「旗标 + 值」两个 token 的写法。
  - UUID：`crypto uuid [--no-dash] [--upper]`，用的是裸旗标写法（ui/gui/main.go:4187-4198）。
- **两份必须同步的算法表**：引擎的 `HASH_ALGORITHMS`（cli/crypto/hash.go:41-49）与 GUI 的 `HASH_HEX_LENGTHS`（ui/gui/validate.go:44-47）。GUI 用后者做期望值的前端校验（位数不对就地红字并禁用主按钮，ui/gui/validate.go:251-268），依据的规则是「GUI 判错的引擎一定也判错」。新增算法要两边一起加。

### 6.12 安全上的注意事项

- **私钥经 `--key` 走的是命令行参数**：会留在 shell 历史与进程列表里。私钥优先用 `--key-file`。
- **`genkey` 不带 `--output` 时，未加密的私钥明文出现在 stdout**：调用方不要把这次调用的 stdout 写进日志。
- **统一开始日志只记命令名、不记参数**（cli/root.go:49-53）——这条全局规则在这里尤其重要，因为参数里可能就是密钥或密码。
- **解密失败的文案是合并的**（「密钥不匹配或数据损坏」），不区分到底是哪一种。
- **`md5` / `sha1` 照常提供**，只适合校验和，不适合安全用途。
- GUI 的复制钮写入系统剪贴板，密文与密码可能进入剪贴板历史（ui/gui/DESIGN.md:196）。

### 6.13 改动时容易破坏的东西

- **JSON 字段名**：`output` / `size_bytes` / `private_key` / `public_key` / `files` / `is_match` / `hash` / `expected` / `mismatched` / `missing`，GUI 都按名字读。
- **`genkey` 的文件命名** `<前缀>_private.pem` / `<前缀>_public.pem`：GUI 的前置守卫按同样的名字做 `Stat`。
- **判定类失败的结构**：`hash --expect` 不匹配、`hash --check` 有不符时必须仍然输出 `data`。
- **混合加密的魔数与布局**（6.2）。
- **清单格式**：`<哈希><两个空格><路径>`，两个空格不能改成一个（`--check` 那边一个空格也认，但 shasum 只认标准的两种）。
- **布尔旗标的三种写法**（`--x` / `--x 值` / `--x=值`）：GUI 两种都在用。
- **密码的保底规则**：GUI 的自定义符号分组依赖「每类保底 1 个」。
- **`TextPrint` 的尾换行**：GUI 靠 `TrimRight` 去掉它；`decrypt` 不加换行，GUI 不需要处理。
- **`hash` 的算法表与 GUI 的位数字**是两份：加算法要两边一起。
- **数值旗标一律 `ParseIntStrict`**，不要再引入前缀解析。

## 7. 代码位置

| 部分 | 路径 | 说明 |
|---|---|---|
| 命令面（crypto） | `cli/crypto/crypto.go` | genkey / encrypt / decrypt / password / uuid、密钥解析、混合加密、输出契约 struct |
| 命令面（hash） | `cli/crypto/hash.go` | hash 四形态、算法表、清单的生成与校验、输出契约 struct |
| 命令面（包文档） | `cli/crypto/doc.go` | 包注释 |
| 装配 | `cli/root.go:67-68` | `crypto.NewCryptoCmd()` 与 `crypto.NewHashCmd()` 挂到根命令 |
| 引擎 | 没有。全部是标准库：`crypto/rsa`、`crypto/aes`、`crypto/cipher`、`crypto/rand`、`crypto/x509`、`encoding/pem`；哈希用 `crypto/md5`、`crypto/sha1`、`crypto/sha256`、`crypto/sha512`、`hash/crc32` | 命令面就是全部实现 |
| 旗标小件 | `internal/cmdutil/flags.go` | `FlagValue` / `ParseIntStrict` / `FirstPositional` |
| 命令面公共底座 | `internal/cliutil/cliutil.go`、`internal/cliutil/help.go` | `RestAfterPositional`、通用载荷 `FileOutputJSON`、help 守卫 |
| 输出契约层 | `internal/stdout/log.go` | 信封、`JSONFailPrint`（判定类失败）、`TextPrint` |
| 单测 | `cli/crypto/crypto_test.go`、`internal/cmdutil/flags_test.go` | 候选池、自定义字符集生成、`--charset` 解析、`ParseIntStrict` |
| GUI 调用点 | `ui/gui/service.go:733-826、938-971`、`ui/gui/main.go:2246-2530、3946-3959、4173-4198`、`ui/gui/validate.go:44-47、251-268` | `RunTool` / `RunToolStdin` / `HashExec`、各页拼参数、算法位数字 |
| 验收脚本 | `verify/verify.sh:596-675`（crypto）、`743-815`（hash）、`863-864`（hash --help）、`1164-1174`（数值严格校验） | 见第 8 节 |
| Agent 消费文档 | `cli/tools/skill.md:78-87`（hash）、`cli/tools/skill.md:190-202`（crypto） | `zpack skill` 输出里的两节 |

## 8. 验收用例

### 8.1 `verify/verify.sh`（纯 Go 产物即可跑）

**crypto 段**（596-675 行，共 35 个断言）：

| 分组 | 用例名 |
|---|---|
| genkey | crypto genkey 密钥产出（用 python3 把 stdout JSON 里的两把钥匙落成文件） |
| genkey 覆盖守卫 | crypto genkey 已存在拒绝 exit / 已存在拒绝 error_info / 已存在旧私钥未被覆盖 / 已存在旧公钥未被覆盖 |
| 加解密 | crypto 短文本加密解密（位置参数传明文、Base64 密文当参数解密）/ 混合加密 5KB 往返（`--file` 输入，解密结果 `--output` 落盘后 `cmp`）/ 错误密钥 exit |
| password 基础 | crypto password exit / 长度 / count 行数 |
| password 字符类开关 | `--symbol=false` 排除符号 / `--upper false` 排除大写 |
| password 退化 | 全关 exit / 全关 error_info / 长度越界 exit（`--length 3`） |
| password `--charset` | `--charset` exit / 长度 / 只含自定义字符 / 非 ASCII exit / 非 ASCII 8 个字符且都在集内 / 与内置类同开 exit / 保底含自定义符号且不含其它符号 / 空串 exit / 空串 error_info / 各类全关仅 `--charset` exit / 仅 `--charset` 只含自定义字符 |
| uuid | exit / v4 格式（正则） / count 行数 / `--upper` 大写 / `--no-dash` exit / `--no-dash` 长度 32 / `--no-dash` 无连字符 / `--no-dash --upper` |

**数值严格校验段**（1164-1174 行，与本功能相关的 4 个断言）：crypto password `--length 5abc` exit / error_info、crypto uuid `--count 3x` exit、crypto genkey `--bits 1024abc` exit。

**hash 段**（743-815 行，共 23 个断言）：

| 分组 | 用例名 |
|---|---|
| 计算 | hash exit / sha256 向量（`abc` 的 FIPS 180 标准值）/ md5 向量（RFC 1321 标准值）/ 算法别名（`SHA-256`）/ crc32 向量（`123456789` → `cbf43926`）/ 多文件条数 |
| 退化 | 未知算法 exit / 不存在 exit / 目录 exit / expect 长度错 exit / 无参数 exit |
| `--expect` | 匹配 exit / 匹配字段 / 不匹配 exit / 不匹配字段（`is_match:false` 且带实算值） |
| `--output` 与互操作 | 清单 shasum 互操作（zpack 出的清单用 `shasum -a 256 -c` 验）/ `--check` 外部清单 exit（`shasum` 出的清单 zpack 也能验） |
| `--check` | check exit / check matched / 篡改 exit / 篡改记录 / 缺失 exit / 缺失记录 |

另有 `hash --help exit`（864 行）。

**在 Windows 的 Git Bash 上**：crypto 段里由 python3 落盘密钥文件的那条链路会因坐标系错位而失败（python3 写到 `C:\tmp\…`，exe 却去 MSYS 的临时目录读），这是验收脚本的问题而不是产品问题——同一批操作在 Windows 坐标下手动验证全部正常（AGENTS.md:288）。

**验收没有覆盖的行为**：

- `crypto encrypt --output`；`crypto decrypt --output` 的 JSON（用例里把输出丢弃了）；`genkey --output` 的成功路径与它输出的 JSON；`public_b64` / `private_b64`。
- `--key`（密钥内容直接传）与「Base64 编码的 PEM」这两种密钥输入形态——用例只用 `--key-file` 读 PEM 文件。
- `--length` 的上界、`--count` 的上下界、布尔旗标的非法值、`--lower false` / `--digit false`。
- `sha1` / `sha224` / `sha384` / `sha512` 的向量（只验了 sha256、md5、crc32）。
- `hash --output` 输出的 JSON；`--check` 的相对路径基准、`#` 注释行、CRLF 清单、二进制分隔符、混合算法、空清单、无法解析的行；`mismatched` 记录里的字段；`--expect` 非十六进制；各旗标的「缺少值」；`--check` 与 `--expect` 的互斥。
- 小于 1024 位的密钥（genkey 与外部公钥两边）。

### 8.2 单元测试

本功能**没有针对加解密逻辑与 hash 逻辑的单测**，这两块完全靠验收脚本。现有单测只覆盖纯函数：

| 测试 | 守的是什么 |
|---|---|
| `TestCryptoCharsetPool`（cli/crypto/crypto_test.go） | 候选池只收启用的类、去重、按首次出现顺序、非 ASCII 按字符去重 |
| `TestCryptoGeneratePasswordCharset` | 自定义字符集：长度按字符数、只含池内字符、启用的类各保底 1 个 |
| `TestCryptoCharsetFlagValue` | `--charset` 的两种写法；值以连字符开头照收；空串也算给了 |
| `TestParseIntStrict`（internal/cmdutil/flags_test.go） | 严格整数解析的反例：`"5abc"`、`"3x"`、`"1.5"`、`""`、`"abc"` 都判非法 |

GUI 侧有随机生成页字符集的测试（`ui/gui/pwcharset_test.go`，五个自定义符号分组恰好铺满 32 个 ASCII 符号）与 service 的测试，见 gui spec。

### 8.3 手动验证

全部是只读、秒级的命令：

```bash
zpack crypto --help; echo $?                          # 0
zpack crypto password --length 24                     # 一行 24 字符的密码
zpack crypto password --length 4 --count 3            # 三行，每行四类字符各一个
zpack crypto uuid --count 2 --upper --no-dash         # 两行 32 位大写十六进制
zpack crypto uuid --count 101                         # --count 需要 1-100 整数，收到: 101
zpack crypto genkey --bits 4096                       # JSON；私钥明文在 stdout，别写进日志
zpack crypto genkey --bits=4096                       # 等号写法不认：输出里 bits 仍是 2048

# 文本加密解密（密钥走管道，不落文件；内容走 stdin）
keys=$(zpack crypto genkey 2>/dev/null)
pub=$(printf '%s' "$keys" | zpack json get data.public_key 2>/dev/null)
priv=$(printf '%s' "$keys" | zpack json get data.private_key 2>/dev/null)
printf '你好' | zpack crypto encrypt --key "$pub" 2>/dev/null | zpack crypto decrypt --key "$priv" 2>/dev/null; echo

# hash
zpack hash 某文件                                      # 计算形态的 JSON
zpack hash 某文件 --algorithm SHA-256 --expect 0000000000000000000000000000000000000000000000000000000000000000; echo $?   # 1 + is_match:false
zpack hash 某文件 --algorithm md6                      # 未知算法
```

带 `--output` 的形态（`crypto encrypt|decrypt --output`、`crypto genkey --output`、`hash --output`）会写文件，而且除了 `genkey` 都是直接覆盖——要验证就在一个临时目录里做，不要对着真实数据。清单与 `hash --check` 同理。

## 9. 未结项与已知限制

### 没做的

- 没有口令加密、口令派生、独立的对称加密命令；没有签名 / 验签；不支持 RSA 以外的算法。
- 不支持带口令的私钥（`ENCRYPTED PRIVATE KEY`）、裸 DER 密钥文件、OpenSSH 格式的公钥。
- CLI 没有 `--` 分隔符：以 `-` 开头的内容不能当位置参数传（6.10）。
- 混合加密的容器没有版本号与算法标识（6.2）。
- `hash` 不递归目录，也不对目录本身计算。
- `genkey --output` 写两个文件没有回滚（4.3）。
- 直接 RSA 模式与外部工具（OpenSSL、WebCrypto）的互通没有实测过【待确认】。
- **验收缺口**：见 8.1 末尾的清单；加解密与 hash 逻辑没有任何单测。

### 已知不对但暂时没改的

- **`--bits` 的下限是达不到的**：参数校验放行 512 与 768，标准库却拒绝小于 1024 位的密钥（生成与加解密都拒绝），报错文案 `--bits 需要 512-4096 的 256 倍数整数` 会误导用户（6.5）。把下限提到 1024 并同步文案是一个待定的决定【待确认】。 **2026-11-24 已修复（提交 118dde3）**：下限提到 1024。
- **等号写法被静默忽略**：`--bits=4096`、`--length=24`、`--count=3` 都不生效，取缺省值，没有任何提示（2.6）。只有布尔旗标与 `--charset` 认等号。
- **以 `-` 开头的位置参数被静默丢弃**：`crypto encrypt --key K "- 列表项"` 会去读 stdin，stdin 为空就加密空内容并成功返回（6.10）。
- **`genkey --output` 先生成密钥再查「文件已存在」**：注定要拒绝的调用也要先花一次生成密钥的时间（4.1.1）。
- **`genkey --output` 输出的路径不转绝对路径**：前缀是相对路径时 JSON 里就是相对路径；`hash --output` 输出的清单路径却是转了的（3.1、3.5）。
- **`cryptoRandRune` 的空循环与注释不符**：注释说「拒绝采样防模偏」，实际直接返回 `rand.Int` 的结果，循环体只跑一轮（cli/crypto/crypto.go:238-249）。
- **保底在字符类数大于长度时失效**（4.2）。
- **带口令的私钥报错没有针对性**：只报「无法解析私钥 DER 数据」，不提示「不支持带口令的私钥」。
- **`rsaDecryptHybrid` 忽略了 `aes.NewCipher` / `cipher.NewGCM` 的错误**（cli/crypto/crypto.go:466-467、394-395）：会话密钥长度不对时会 panic 而不是报错。目前只有本工具自己产出的 32 字节密钥能走到这里，实际不可达，但防御性不足。
- **`hash --check` 把「文件不存在」「是目录」「读取失败」都算作 `missing`**，不区分原因（3.6）。
- **空清单算校验成功**（`checked:0`，exit 0）——对「清单写错了路径导致一行都没解析」这种情况没有防护，不过那种情况多半会先报「无法解析」或「无法判定算法」。
- **清单里相对路径的基准与 shasum 不同**（4.2），文档里没有提醒过用户。
- **`zpack crypto --help` 看不到 `password` 与 `uuid`**：帮助只有一行 Usage（`zpack crypto <genkey|encrypt|decrypt> [参数]`）和一句 Short 描述。

### 代码与文档不一致的地方（以代码为准）

| # | 文档怎么写 | 代码实际 | 出处 |
|---|---|---|---|
| 1 | 密钥位数「支持 1024 / 2048 / 4096」 | 校验范围是 512–4096、256 的倍数；1024、1280、…、4096 都能生成，512 与 768 被标准库拒绝 | cli/crypto/crypto.go:8 对比 299 |（2026-11-24 已对齐：下限 1024）
| 2 | 用法是 `crypto <genkey\|encrypt\|decrypt> [参数]`，Short 只提「RSA 密钥生成 / 公钥加密 / 私钥解密」 | 还有 `password` 与 `uuid` 两个子命令；用法错误文案与包文档都列了五个 | cli/crypto/crypto.go:10-15、51-52 对比 61-73、cli/crypto/doc.go:1 |
| 3 | `crypto genkey [--output <前缀>]`「输出 .pub/.pem + b64 形态」 | 文件名是 `<前缀>_private.pem` 与 `<前缀>_public.pem`；b64 形态只在**不带** `--output` 时输出 | cli/tools/skill.md:195 对比 cli/crypto/crypto.go:319-340 |
| 4 | `crypto encrypt\|decrypt <输入> [--output <路径>]` | 必须给 `--key` 或 `--key-file`，文档没写 | cli/tools/skill.md:196 对比 cli/crypto/crypto.go:352-355 |
| 5 | GUI 的密钥位数三档，「Go 标准库拒生成 < 1024 位密钥，故 GUI 三档」 | CLI 的校验与报错文案却从 512 起 | ui/gui/DESIGN.md:171-173 对比 cli/crypto/crypto.go:299-300 |（2026-11-24 已对齐）
| 6 | crypto 的数值参数「用的是 `fmt.Sscanf`，只看前缀…应替换」 | 6016f9b 已改为 `ParseIntStrict`，门禁已有用例 | specs/conventions.md:136 对比 cli/crypto/crypto.go:166、175、257、298 |
| 7 | genkey 覆盖守卫标注的提交号 `77b058b` | 这个提交号在本仓库的历史里找不到（可能是开发机上的编号） | ui/gui/DESIGN.md:178 |

AGENTS.md 输出表里 crypto 与 hash 的 JSON 示例都省略了 `data` 这一层（AGENTS.md:47-62）。这是该表的统一写法（表头注明「读时自动包一层 data」），不算不一致，但照着示例去解析会取不到字段。

### 【待确认】汇总

| # | 事项 | 为什么拿不准 |
|---|---|---|
| 1 | 直接 RSA 模式（OAEP-SHA256）与外部工具的互通 | 算法参数是标准的，但「外部加密、zpack 解密」的方向没能实测——OpenSSL 那一侧要把密钥放进文件，而本次核对约定不落文件 |
| 2 | 混合加密用自家容器是否符合预期 | 没有文档说明过这是有意的取舍还是权宜之计；AGENTS.md 的「不做私有格式」没有提到加密 |
| 3 | `--bits` 的下限要不要提到 1024 | 改校验是行为变更，需要定案 |
| 4 | `os.WriteFile` 写输出文件失败时会不会留下部分内容 | 代码没有「先写临时文件再改名」；具体行为取决于失败时机，没有实测 |

## 变更记录

| 日期 | 改了什么 | 同步的代码（提交） |
|---|---|---|
| 2026-11-24 | 初版：从代码与 AGENTS.md 整理 | 40fb9e7 |
| 2026-11-24 | 修复：`genkey --bits` 下限从 512 提到 1024（标准库本来就拒绝更短的密钥） | 118dde3 |
| 2026-11-24 | 修复：`--length` / `--count` / `--bits` 改为严格整数校验（此前 `--length 5abc` 被当成 5） | 6016f9b |
