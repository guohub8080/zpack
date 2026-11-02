#!/usr/bin/env bash
# zpack 验收门禁：只测 bin/zpack 的预期行为（不与旧版对拍、不依赖旧版存在）。
#
# 原则：确定性输出与预期值全字节比对；随机性用例比对"形状"（长度/字符集/行数）；
#       退出码一并比对。全绿才算一个命令实现完成。
# 命令形态（2026-11-02 扁平化）：zpack compress / zpack extract（无格式子命令）——
# 写格式由 --output 扩展名决定（.zip 默认/.tar/.tar.gz/.tgz），读格式靠魔数识别。
#
# 用法：bash verify/verify.sh
set -u

NEW_CLI="$(cd "$(dirname "$0")/.." && pwd)/bin/zpack"

PASS_COUNT=0
FAIL_COUNT=0

report_pass() { PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
  FAIL_COUNT=$((FAIL_COUNT + 1))
  echo "✗ $1"
  echo "    期望: [$2]"
  echo "    实际: [$3]"
}

assert_equal() { # 用例名 期望值 实际值
  if [ "$2" = "$3" ]; then report_pass; else report_fail "$1" "$2" "$3"; fi
}

# ---------- zip：行为用例（compress 并行打包 / extract 并行解压 + GBK 修复 + zip-slip 防护） ----------
mz_src="/tmp/zpack-verify-zip-$$"
mz_out="/tmp/zpack-verify-zip-$$-out.zip"
mz_dest="/tmp/zpack-verify-zip-dest-$$"
ex_dest="/tmp/zpack-verify-zip-extract-$$"
mkdir -p "$mz_src/sub dir" "$mz_src/node_modules"
printf 'a content' > "$mz_src/a.txt"
printf 'b content' > "$mz_src/sub dir/b file.txt"
printf 'lib' > "$mz_src/node_modules/lib.js"
printf 'junk' > "$mz_src/.DS_Store"
printf 'junk' > "$mz_src/._a.txt"
printf '中文内容' > "$mz_src/中 文 名.txt"
printf '\xff\xd8\xff\xe0JFIF-fake' > "$mz_src/pic.jpg"
new_out=$("$NEW_CLI" compress "$mz_src" --output "$mz_out" 2>/dev/null); new_exit=$?
assert_equal "zip compress 目录 exit" 0 "$new_exit"
assert_equal "zip compress file_count（默认排垃圾）" 5 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "zip compress 输出 archive 字段" "$mz_out" "$(echo "$new_out" | grep -o '"archive":"[^"]*"' | sed 's/"archive":"//;s/"$//')"
if unzip -l "$mz_out" | grep -q 'DS_Store'; then report_fail "zip compress 默认排除 .DS_Store" "已排除" "出现在包内"; else report_pass; fi
if unzip -l "$mz_out" | grep -q '\._a'; then report_fail "zip compress 排除 ._ 资源叉" "已排除" "出现在包内"; else report_pass; fi
new_out=$("$NEW_CLI" compress "$mz_src" --output "$mz_out" --exclude node_modules 2>/dev/null)
assert_equal "zip compress --exclude node_modules file_count" 4 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
# 解压往返（系统 unzip 独立交叉验证）：中文/空格文件名与内容逐字节无损
mkdir -p "$mz_dest" && unzip -q -o "$mz_out" -d "$mz_dest"
if [ -f "$mz_dest/$(basename "$mz_src")/中 文 名.txt" ]; then report_pass; else report_fail "zip compress 中文名入包" "存在" "缺失"; fi
cmp -s "$mz_src/a.txt" "$mz_dest/$(basename "$mz_src")/a.txt"
assert_equal "zip compress 往返内容一致" 0 "$?"
# 强制 UTF-8：每个条目必须置 EFS 标志位 0x800
"$NEW_CLI" compress "$mz_src" --output "$mz_out" >/dev/null 2>&1
utf8_flag_count=$(python3 -c "
import zipfile, sys
infos = zipfile.ZipFile('$mz_out').infolist()
print(sum(1 for i in infos if i.flag_bits & 0x800))" 2>/dev/null)
assert_equal "zip compress 全部条目置 UTF-8 标志位" 5 "$utf8_flag_count"
# High Speed Archiving：魔数合法的 jpg 条目应 Store 直通（compress_type 0）
store_method=$(python3 -c "
import zipfile
infos = zipfile.ZipFile('$mz_out').infolist()
print([i for i in infos if i.filename.endswith('pic.jpg')][0].compress_type)" 2>/dev/null)
assert_equal "zip compress jpg 魔数 Store 直通" 0 "$store_method"
# 并行压缩：--jobs 2 产出同样合法
new_out=$("$NEW_CLI" compress "$mz_src" --output "$mz_out" --jobs 2 2>/dev/null); new_exit=$?
assert_equal "zip compress --jobs 2 exit" 0 "$new_exit"
assert_equal "zip compress --jobs 2 file_count" 5 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
# 多文件输入：按文件名平铺进包根
new_out=$("$NEW_CLI" compress "$mz_src/a.txt" "$mz_src/node_modules/lib.js" --output "$mz_out" 2>/dev/null)
assert_equal "zip compress 多文件 file_count" 2 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
if unzip -l "$mz_out" | grep -q 'a.txt'; then report_pass; else report_fail "zip compress 多文件按文件名入包根" "a.txt 在根" "缺失"; fi
# 未知扩展名输出默认走 zip（--output out.foo 仍是合法 zip）
foo_out="/tmp/zpack-verify-foo-$$"
new_out=$("$NEW_CLI" compress "$mz_src/a.txt" --output "$foo_out" 2>/dev/null); new_exit=$?
assert_equal "compress 未知扩展名默认 zip exit" 0 "$new_exit"
foo_is_zip=$(python3 -c "
import zipfile
print(len(zipfile.ZipFile('$foo_out').infolist()))" 2>/dev/null)
assert_equal "compress 未知扩展名产出合法 zip" 1 "$foo_is_zip"
rm -f "$foo_out"
# extract：自解压（含并行与中文/空格往返）——上一用例把 $mz_out 换成了多文件包，先重建完整包
"$NEW_CLI" compress "$mz_src" --output "$mz_out" >/dev/null 2>&1
new_out=$("$NEW_CLI" extract "$mz_out" --output "$ex_dest" 2>/dev/null); new_exit=$?
assert_equal "zip extract exit" 0 "$new_exit"
assert_equal "zip extract file_count" 5 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
if [ -f "$ex_dest/$(basename "$mz_src")/中 文 名.txt" ]; then report_pass; else report_fail "zip extract 中文+空格名解出" "存在" "缺失"; fi
cmp -s "$mz_src/a.txt" "$ex_dest/$(basename "$mz_src")/a.txt"
assert_equal "zip extract 往返内容一致" 0 "$?"
new_out=$("$NEW_CLI" extract "$mz_out" --output "$ex_dest-2" --jobs 3 2>/dev/null); new_exit=$?
assert_equal "zip extract --jobs 3 exit" 0 "$new_exit"
# 智能解压：散装归档（多根）缺省解压包一层以压缩包名命名的根目录，防散落
multi_zip="/tmp/zpack-verify-smart-multi-$$.zip"
"$NEW_CLI" compress "$mz_src/a.txt" "$mz_src/pic.jpg" --output "$multi_zip" >/dev/null 2>&1
smart_dir="/tmp/zpack-verify-smart-dest-$$"
mkdir -p "$smart_dir"
(cd "$smart_dir" && "$NEW_CLI" extract "$multi_zip" >/dev/null 2>&1); new_exit=$?
assert_equal "zip extract 散装归档智能包裹 exit" 0 "$new_exit"
if [ -f "$smart_dir/$(basename "$multi_zip" .zip)/a.txt" ]; then report_pass; else report_fail "zip extract 智能根目录包裹" "$smart_dir/$(basename "$multi_zip" .zip)/a.txt" "缺失"; fi
# 智能解压：单根归档缺省解压直接落条目自带根目录（当前目录只出现一个新目录）
single_zip="/tmp/zpack-verify-smart-single-$$.zip"
"$NEW_CLI" compress "$mz_src" --output "$single_zip" >/dev/null 2>&1
single_dir="/tmp/zpack-verify-smart-single-dest-$$"
mkdir -p "$single_dir"
(cd "$single_dir" && "$NEW_CLI" extract "$single_zip" >/dev/null 2>&1); new_exit=$?
assert_equal "zip extract 单根直落 exit" 0 "$new_exit"
if [ -f "$single_dir/$(basename "$mz_src")/中 文 名.txt" ]; then report_pass; else report_fail "zip extract 单根直落中文路径" "存在" "缺失"; fi
# 覆盖语义：默认跳过已存在（保护已改动文件），--force 才覆盖
force_dir="/tmp/zpack-verify-force-$$"
"$NEW_CLI" extract "$single_zip" --output "$force_dir" >/dev/null 2>&1
printf 'changed' > "$force_dir/$(basename "$mz_src")/a.txt"
new_out=$("$NEW_CLI" extract "$single_zip" --output "$force_dir" 2>/dev/null)
assert_equal "zip extract 默认跳过已存在 skipped" 5 "$(echo "$new_out" | grep -o '"skipped":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "zip extract 默认不覆盖改动文件" "changed" "$(cat "$force_dir/$(basename "$mz_src")/a.txt")"
new_out=$("$NEW_CLI" extract "$single_zip" --output "$force_dir" --force 2>/dev/null); new_exit=$?
assert_equal "zip extract --force exit" 0 "$new_exit"
assert_equal "zip extract --force skipped" 0 "$(echo "$new_out" | grep -o '"skipped":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "zip extract --force 恢复内容" "a content" "$(cat "$force_dir/$(basename "$mz_src")/a.txt")"
# 大单文件 128KB 分块并行（>16MB 触发）：缝合点结构 + 字节级往返 + 产出确定性
chunk_src="/tmp/zpack-verify-chunk-$$"
chunk_zip="/tmp/zpack-verify-chunk-$$-out.zip"
chunk_zip2="/tmp/zpack-verify-chunk-$$-out2.zip"
chunk_dest="/tmp/zpack-verify-chunk-dest-$$"
mkdir -p "$chunk_src"
perl -e 'print "zpack 分块并行压缩验证行 0123456789 abcdefghijk\n" x 350000' > "$chunk_src/huge-text-17mb.txt"
new_out=$("$NEW_CLI" compress "$chunk_src" --output "$chunk_zip" 2>/dev/null); new_exit=$?
assert_equal "zip compress 大文件分块 exit" 0 "$new_exit"
seam_total=$(python3 -c "
import zipfile, struct
zf = zipfile.ZipFile('$chunk_zip')
info = [i for i in zf.infolist() if i.filename.endswith('huge-text-17mb.txt')][0]
with open('$chunk_zip','rb') as f:
    f.seek(info.header_offset)
    lh = f.read(30)
    nlen = struct.unpack('<H', lh[26:28])[0]; elen = struct.unpack('<H', lh[28:30])[0]
    f.seek(info.header_offset + 30 + nlen + elen)
    raw = f.read(info.compress_size)
print(raw.count(b'\x00\x00\xff\xff'))" 2>/dev/null)
if [ "$seam_total" -ge 100 ] 2>/dev/null; then report_pass; else report_fail "zip compress 128KB 分块缝合点" "≥100（17MB/128KB）" "$seam_total"; fi
"$NEW_CLI" extract "$chunk_zip" --output "$chunk_dest" >/dev/null 2>&1
cmp -s "$chunk_src/huge-text-17mb.txt" "$chunk_dest/$(basename "$chunk_src")/huge-text-17mb.txt"
assert_equal "zip 大文件分块解压往返一致" 0 "$?"
"$NEW_CLI" compress "$chunk_src" --output "$chunk_zip2" >/dev/null 2>&1
cmp -s "$chunk_zip" "$chunk_zip2"
assert_equal "zip 大文件分块产出确定" 0 "$?"
# GBK 归档夹具：手工构造非 EFS、名字为 GBK 字节的条目（python zipfile 对非 ASCII 名会强置 EFS，只能手写结构）
gbk_zip="/tmp/zpack-verify-gbk-$$.zip"
gbk_out="/tmp/zpack-verify-gbk-dest-$$"
python3 -c "
import struct, zlib
name = '中文 文件.txt'.encode('gbk')
data = b'gbk roundtrip payload'
crc = zlib.crc32(data) & 0xffffffff
lh = struct.pack('<IHHHHHIIIHH', 0x04034b50, 20, 0, 0, 0, 0, crc, len(data), len(data), len(name), 0)
cd = struct.pack('<IHHHHHHIIIHHHHHII', 0x02014b50, 20, 20, 0, 0, 0, 0, crc, len(data), len(data), len(name), 0, 0, 0, 0, 0, 0)
eocd = struct.pack('<IHHHHIIH', 0x06054b50, 0, 0, 1, 1, len(cd)+len(name), len(lh)+len(name)+len(data), 0)
open('$gbk_zip','wb').write(lh+name+data+cd+name+eocd)" 2>/dev/null
new_out=$("$NEW_CLI" extract "$gbk_zip" --output "$gbk_out" 2>/dev/null); new_exit=$?
assert_equal "zip extract GBK 归档 exit" 0 "$new_exit"
if [ -f "$gbk_out/中文 文件.txt" ]; then report_pass; else report_fail "zip extract GBK 文件名修复" "中文 文件.txt 解出" "缺失"; fi
if [ "$(cat "$gbk_out/中文 文件.txt" 2>/dev/null)" = "gbk roundtrip payload" ]; then report_pass; else report_fail "zip extract GBK 内容一致" "一致" "不符"; fi
# zip-slip：恶意 ../ 条目必须拒绝且不外泄文件
slip_zip="/tmp/zpack-verify-slip-$$.zip"
slip_dest="/tmp/zpack-verify-slip-dest-$$"
slip_evil="/tmp/evil-slip-$$.txt"
python3 -c "
import zipfile
zf = zipfile.ZipFile('$slip_zip', 'w')
zf.writestr('../evil-slip-$$.txt', 'pwned')
zf.close()" 2>/dev/null
mkdir -p "$slip_dest"
new_out=$("$NEW_CLI" extract "$slip_zip" --output "$slip_dest" 2>/dev/null); new_exit=$?
assert_equal "zip extract zip-slip 拒绝 exit" 1 "$new_exit"
if [ -f "$slip_evil" ]; then report_fail "zip extract zip-slip 无外泄" "目标目录外无文件" "恶意文件被写出"; else report_pass; fi
# 错误分支
new_out=$("$NEW_CLI" compress "/tmp/zpack-not-exist-$RANDOM" --output "$mz_out" 2>/dev/null); new_exit=$?
assert_equal "zip compress 路径不存在 exit" 1 "$new_exit"
new_out=$("$NEW_CLI" compress "$mz_src" --output 2>/dev/null); new_exit=$?
assert_equal "compress --output 缺值 报错JSON" '{"is_success":false,"error_info":"--output 缺少值"}' "$new_out"
new_out=$("$NEW_CLI" compress 2>/dev/null); new_exit=$?
assert_equal "compress 无输入 exit" 1 "$new_exit"
new_out=$("$NEW_CLI" compress "$mz_src" --output "$mz_out" --level 11 2>/dev/null); new_exit=$?
assert_equal "compress --level 越界 报错JSON" '{"is_success":false,"error_info":"--level 需要 0-9 的整数，收到: 11"}' "$new_out"
new_out=$("$NEW_CLI" compress "$mz_src" --output "$mz_out" --jobs 0 2>/dev/null); new_exit=$?
assert_equal "compress --jobs 越界 报错JSON" '{"is_success":false,"error_info":"--jobs 需要 1-64 的整数，收到: 0"}' "$new_out"
new_out=$("$NEW_CLI" extract "$mz_out" --output 2>/dev/null); new_exit=$?
assert_equal "extract --output 缺值 报错JSON" '{"is_success":false,"error_info":"--output 缺少值"}' "$new_out"
new_out=$("$NEW_CLI" extract 2>/dev/null); new_exit=$?
assert_equal "extract 无归档 报错JSON" '{"is_success":false,"error_info":"请指定归档：extract <归档.zip|.tar|.tar.gz> [--output 目录] [--jobs N] [--force]"}' "$new_out"
printf 'not an archive at all' > "$mz_out.notarc"
new_out=$("$NEW_CLI" extract "$mz_out.notarc" --output "$ex_dest-3" 2>/dev/null); new_exit=$?
assert_equal "extract 无法识别的归档 exit" 1 "$new_exit"
new_out=$("$NEW_CLI" extract "/tmp/zpack-not-exist-$RANDOM" 2>/dev/null); new_exit=$?
assert_equal "extract 路径不存在 exit" 1 "$new_exit"
new_out=$("$NEW_CLI" frobnicate 2>/dev/null); new_exit=$?
assert_equal "未知命令 exit" 1 "$new_exit"
# 写面并行压缩三件套（字典衔接/0x40001 阈值/HSA 运行时降档）行为验证
align_src="/tmp/zpack-verify-align-$$"
mkdir -p "$align_src"
python3 -c "
import random
random.seed(7)
tail = bytes(random.randrange(256) for _ in range(32000))  # 周期<32K 窗口（恰=32768 时 deflate 不发窗口边界距离）
open('$align_src/linkage.bin','wb').write(bytes(random.randrange(256) for _ in range(98304)) + tail*48)" 2>/dev/null
align_zip="/tmp/zpack-verify-align-$$.zip"
new_out=$("$NEW_CLI" compress "$align_src" --output "$align_zip" 2>/dev/null); new_exit=$?
assert_equal "对齐-字典衔接 compress exit" 0 "$new_exit"
align_size=$(stat -f%z "$align_zip" 2>/dev/null)
if [ "$align_size" -lt 350000 ] 2>/dev/null; then report_pass; else report_fail "对齐-字典衔接比值" "<350000" "$align_size"; fi
"$NEW_CLI" extract "$align_zip" --output "/tmp/zpack-verify-align-dest-$$" >/dev/null 2>&1
cmp -s "$align_src/linkage.bin" "/tmp/zpack-verify-align-dest-$$/$(basename "$align_src")/linkage.bin"
assert_equal "对齐-字典衔接往返一致" 0 "$?"
# HSA 运行时降档确定性：32MB 不可压语料两次产出字节一致
hsa_src="/tmp/zpack-verify-hsa-$$"
mkdir -p "$hsa_src"
head -c 33554432 /dev/urandom > "$hsa_src/rand.bin"
"$NEW_CLI" compress "$hsa_src" --output "/tmp/zpack-verify-hsa-a-$$.zip" >/dev/null 2>&1
"$NEW_CLI" compress "$hsa_src" --output "/tmp/zpack-verify-hsa-b-$$.zip" >/dev/null 2>&1
cmp -s "/tmp/zpack-verify-hsa-a-$$.zip" "/tmp/zpack-verify-hsa-b-$$.zip"
assert_equal "对齐-HSA 降档产出确定" 0 "$?"
"$NEW_CLI" extract "/tmp/zpack-verify-hsa-a-$$.zip" --output "/tmp/zpack-verify-hsa-dest-$$" >/dev/null 2>&1
cmp -s "$hsa_src/rand.bin" "/tmp/zpack-verify-hsa-dest-$$/$(basename "$hsa_src")/rand.bin"
assert_equal "对齐-HSA 降档往返一致" 0 "$?"
rm -rf "$align_src" "$align_zip" "/tmp/zpack-verify-align-dest-$$" "$hsa_src" "/tmp/zpack-verify-hsa-a-$$.zip" "/tmp/zpack-verify-hsa-b-$$.zip" "/tmp/zpack-verify-hsa-dest-$$"
# --level 0 全 Store 仍可被标准工具读取
new_out=$("$NEW_CLI" compress "$mz_src" --output "$mz_out" --level 0 2>/dev/null); new_exit=$?
assert_equal "zip compress --level 0 exit" 0 "$new_exit"
level0_all_store=$(python3 -c "
import zipfile
infos = zipfile.ZipFile('$mz_out').infolist()
print(all(i.compress_type == 0 for i in infos))" 2>/dev/null)
assert_equal "zip compress --level 0 全 Store" "True" "$level0_all_store"
rm -rf "$mz_src" "$mz_dest" "$mz_out" "$mz_out.notarc" "$ex_dest" "$ex_dest-2" "$ex_dest-3" "$gbk_zip" "$gbk_out" "$slip_zip" "$slip_dest" "$slip_evil" "$multi_zip" "$smart_dir" "$single_zip" "$single_dir" "$force_dir" "$chunk_src" "$chunk_zip" "$chunk_zip2" "$chunk_dest"

# ---------- tar / tar.gz：行为用例（PAX 写入 + 魔数读取 + 符号链接 + 安全防护） ----------
tar_src="/tmp/zpack-verify-tar-$$"
tar_out="/tmp/zpack-verify-tar-$$-out.tar"
targz_out="/tmp/zpack-verify-tar-$$-out.tar.gz"
tar_dest="/tmp/zpack-verify-tar-dest-$$"
mkdir -p "$tar_src/sub dir"
printf 'a content' > "$tar_src/a.txt"
printf 'b content' > "$tar_src/sub dir/b file.txt"
printf '中文内容' > "$tar_src/中 文 名.txt"
printf 'junk' > "$tar_src/.DS_Store"
ln -s a.txt "$tar_src/a-link.txt"
# tar 写入：默认排垃圾 + 符号链接保留 + 系统 tar 交叉验证
new_out=$("$NEW_CLI" compress "$tar_src" --output "$tar_out" 2>/dev/null); new_exit=$?
assert_equal "tar compress 目录 exit" 0 "$new_exit"
assert_equal "tar compress file_count（默认排垃圾+链接计条目）" 4 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "tar compress 输出 archive 字段" "$tar_out" "$(echo "$new_out" | grep -o '"archive":"[^"]*"' | sed 's/"archive":"//;s/"$//')"
if tar -tf "$tar_out" 2>/dev/null | grep -q 'DS_Store'; then report_fail "tar compress 默认排除 .DS_Store" "已排除" "出现在包内"; else report_pass; fi
if tar -tf "$tar_out" 2>/dev/null | grep -q '中 文 名.txt'; then report_pass; else report_fail "tar compress 中文名入包（系统 tar 可读）" "存在" "缺失"; fi
# tar.gz 与 .tgz 写入：gzip 魔数 + 往返
new_out=$("$NEW_CLI" compress "$tar_src" --output "$targz_out" 2>/dev/null); new_exit=$?
assert_equal "tar.gz compress exit" 0 "$new_exit"
assert_equal "tar.gz 产物 gzip 魔数" "1f8b" "$(od -An -tx1 -N2 "$targz_out" | tr -d ' \n')"
tgz_out="/tmp/zpack-verify-tar-$$-out.tgz"
new_out=$("$NEW_CLI" compress "$tar_src" --output "$tgz_out" 2>/dev/null); new_exit=$?
assert_equal ".tgz compress exit" 0 "$new_exit"
assert_equal ".tgz 产物 gzip 魔数" "1f8b" "$(od -An -tx1 -N2 "$tgz_out" | tr -d ' \n')"
# tar 解压往返：内容 + 中文名 + 符号链接（readlink 与间接内容都还原）
new_out=$("$NEW_CLI" extract "$tar_out" --output "$tar_dest" 2>/dev/null); new_exit=$?
assert_equal "tar extract exit" 0 "$new_exit"
assert_equal "tar extract file_count" 4 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
cmp -s "$tar_src/a.txt" "$tar_dest/$(basename "$tar_src")/a.txt"
assert_equal "tar extract 往返内容一致" 0 "$?"
if [ -f "$tar_dest/$(basename "$tar_src")/中 文 名.txt" ]; then report_pass; else report_fail "tar extract 中文+空格名解出" "存在" "缺失"; fi
assert_equal "tar extract 符号链接还原" "a.txt" "$(readlink "$tar_dest/$(basename "$tar_src")/a-link.txt" 2>/dev/null)"
assert_equal "tar extract 符号链接间接内容" "a content" "$(cat "$tar_dest/$(basename "$tar_src")/a-link.txt" 2>/dev/null)"
# tar.gz 往返（--jobs 被接受不报错）
tar_dest_gz="/tmp/zpack-verify-tar-dest-gz-$$"
new_out=$("$NEW_CLI" extract "$targz_out" --output "$tar_dest_gz" --jobs 4 2>/dev/null); new_exit=$?
assert_equal "tar.gz extract exit" 0 "$new_exit"
cmp -s "$tar_src/a.txt" "$tar_dest_gz/$(basename "$tar_src")/a.txt"
assert_equal "tar.gz 往返内容一致" 0 "$?"
# 魔数识别：改掉扩展名仍按内容识别（tar 与 tar.gz 双向）
mv "$tar_out" "$tar_out.bin"
new_out=$("$NEW_CLI" extract "$tar_out.bin" --output "$tar_dest-bin" 2>/dev/null); new_exit=$?
assert_equal "tar 改名 .bin 魔数识别 exit" 0 "$new_exit"
mv "$tgz_out" "$tgz_out.bin"
new_out=$("$NEW_CLI" extract "$tgz_out.bin" --output "$tar_dest-tgzbin" 2>/dev/null); new_exit=$?
assert_equal "tar.gz 改名 .bin 魔数识别 exit" 0 "$new_exit"
# PAX 长名（>100 字节）往返
long_name=$(python3 -c "print('长名字段'*40 + '.txt')" 2>/dev/null)
printf 'pax long name payload' > "$tar_src/$long_name"
long_tar="/tmp/zpack-verify-long-$$.tar"
"$NEW_CLI" compress "$tar_src" --output "$long_tar" >/dev/null 2>&1
"$NEW_CLI" extract "$long_tar" --output "$tar_dest-long" >/dev/null 2>&1
if [ -f "$tar_dest-long/$(basename "$tar_src")/$long_name" ]; then report_pass; else report_fail "tar PAX 长名往返" "存在" "缺失"; fi
rm -f "$tar_src/$long_name" "$long_tar"; rm -rf "$tar_dest-long"
# gzip --level 档位：0（存档式）比 9 明显大（可压语料）
level_src="/tmp/zpack-verify-level-$$"
mkdir -p "$level_src"
perl -e 'print "zpack gzip level 验证数据 0123456789\n" x 20000' > "$level_src/level.txt"
"$NEW_CLI" compress "$level_src" --output "/tmp/zpack-verify-l0-$$.tar.gz" --level 0 >/dev/null 2>&1
"$NEW_CLI" compress "$level_src" --output "/tmp/zpack-verify-l9-$$.tar.gz" --level 9 >/dev/null 2>&1
l0_size=$(stat -f%z "/tmp/zpack-verify-l0-$$.tar.gz" 2>/dev/null)
l9_size=$(stat -f%z "/tmp/zpack-verify-l9-$$.tar.gz" 2>/dev/null)
if [ "$l0_size" -gt "$l9_size" ] 2>/dev/null; then report_pass; else report_fail "tar.gz --level 0 比 --level 9 大" "$l0_size > $l9_size" "$l0_size vs $l9_size"; fi
rm -f "/tmp/zpack-verify-l0-$$.tar.gz" "/tmp/zpack-verify-l9-$$.tar.gz"; rm -rf "$level_src"
# 确定性：同输入两次 tar.gz 产出字节一致
tar_a="/tmp/zpack-verify-det-a-$$.tar.gz"; tar_b="/tmp/zpack-verify-det-b-$$.tar.gz"
"$NEW_CLI" compress "$tar_src" --output "$tar_a" >/dev/null 2>&1
"$NEW_CLI" compress "$tar_src" --output "$tar_b" >/dev/null 2>&1
cmp -s "$tar_a" "$tar_b"
assert_equal "tar.gz 同输入产出确定" 0 "$?"
rm -f "$tar_a" "$tar_b"
# 智能解压：散装 tar 包一层（复合扩展 .tar.gz 剥双后缀），单根 tar 直落
scat_tar="/tmp/zpack-verify-scat-$$.tar"
"$NEW_CLI" compress "$tar_src/a.txt" "$tar_src/中 文 名.txt" --output "$scat_tar" >/dev/null 2>&1
scat_dir="/tmp/zpack-verify-scat-dest-$$"; mkdir -p "$scat_dir"
(cd "$scat_dir" && "$NEW_CLI" extract "$scat_tar" >/dev/null 2>&1); new_exit=$?
assert_equal "tar 散装智能包裹 exit" 0 "$new_exit"
if [ -f "$scat_dir/$(basename "$scat_tar" .tar)/a.txt" ]; then report_pass; else report_fail "tar 散装智能根目录包裹" "包层根/a.txt" "缺失"; fi
root_tar="/tmp/zpack-verify-root-$$.tar.gz"
"$NEW_CLI" compress "$tar_src" --output "$root_tar" >/dev/null 2>&1
root_dir="/tmp/zpack-verify-root-dest-$$"; mkdir -p "$root_dir"
(cd "$root_dir" && "$NEW_CLI" extract "$root_tar" >/dev/null 2>&1); new_exit=$?
assert_equal "tar.gz 单根直落 exit" 0 "$new_exit"
if [ -f "$root_dir/$(basename "$tar_src")/a.txt" ]; then report_pass; else report_fail "tar.gz 单根直落" "存在" "缺失"; fi
# tar.bz2（批次 B：压缩壳变体经内层 ustar 佐证识别；tar.xz/tar.zst 由 core 单测覆盖——系统无常备 CLI）
tbz2_tar="/tmp/zpack-verify-tbz2-$$.tar.bz2"
tar -cjf "$tbz2_tar" -C "$(dirname "$tar_src")" "$(basename "$tar_src")" 2>/dev/null
tbz2_dest="/tmp/zpack-verify-tbz2-dest-$$"
new_out=$("$NEW_CLI" extract "$tbz2_tar" --output "$tbz2_dest" 2>/dev/null); new_exit=$?
assert_equal "tar.bz2 extract exit" 0 "$new_exit"
cmp -s "$tar_src/a.txt" "$tbz2_dest/$(basename "$tar_src")/a.txt"
assert_equal "tar.bz2 往返内容一致" 0 "$?"

# 跳过/覆盖语义与 zip 一致
new_out=$("$NEW_CLI" extract "$scat_tar" --output "$scat_dir/$(basename "$scat_tar" .tar)" 2>/dev/null)
assert_equal "tar extract 默认跳过已存在 skipped" 2 "$(echo "$new_out" | grep -o '"skipped":[0-9]*' | grep -o '[0-9]*$')"
new_out=$("$NEW_CLI" extract "$scat_tar" --output "$scat_dir/$(basename "$scat_tar" .tar)" --force 2>/dev/null); new_exit=$?
assert_equal "tar extract --force exit" 0 "$new_exit"
assert_equal "tar extract --force skipped" 0 "$(echo "$new_out" | grep -o '"skipped":[0-9]*' | grep -o '[0-9]*$')"
# tar-slip：恶意 ../ 条目拒绝
slip_tar="/tmp/zpack-verify-tarslip-$$.tar"
slip_tar_dest="/tmp/zpack-verify-tarslip-dest-$$"
python3 -c "
import tarfile, io
ti = tarfile.TarInfo('../evil-tarslip-$$.txt'); ti.size = 5
with tarfile.open('$slip_tar', 'w') as tf: tf.addfile(ti, io.BytesIO(b'pwned'))" 2>/dev/null
new_out=$("$NEW_CLI" extract "$slip_tar" --output "$slip_tar_dest" 2>/dev/null); new_exit=$?
assert_equal "tar extract tar-slip 拒绝 exit" 1 "$new_exit"
if [ -f "/tmp/evil-tarslip-$$.txt" ]; then report_fail "tar-slip 无外泄" "无文件" "恶意文件被写出"; else report_pass; fi
# 符号链接逃逸拒绝（解析后逃出归档根）
esc_tar="/tmp/zpack-verify-taresc-$$.tar"
python3 -c "
import tarfile
ti = tarfile.TarInfo('bin/tool'); ti.type = tarfile.SYMTYPE; ti.linkname = '../../evil-esc'
with tarfile.open('$esc_tar', 'w') as tf: tf.addfile(ti)" 2>/dev/null
new_out=$("$NEW_CLI" extract "$esc_tar" --output "/tmp/zpack-verify-taresc-dest-$$" 2>/dev/null); new_exit=$?
assert_equal "tar extract 符号链接逃逸拒绝 exit" 1 "$new_exit"
# 设备/管道条目拒绝
dev_tar="/tmp/zpack-verify-tardev-$$.tar"
python3 -c "
import tarfile
ti = tarfile.TarInfo('dev/node'); ti.type = tarfile.FIFOTYPE
with tarfile.open('$dev_tar', 'w') as tf: tf.addfile(ti)" 2>/dev/null
new_out=$("$NEW_CLI" extract "$dev_tar" --output "/tmp/zpack-verify-tardev-dest-$$" 2>/dev/null); new_exit=$?
assert_equal "tar extract 设备/管道条目拒绝 exit" 1 "$new_exit"
# 硬链接落地：复制归档内成员内容
hl_tar="/tmp/zpack-verify-tarhl-$$.tar"
hl_dest="/tmp/zpack-verify-tarhl-dest-$$"
python3 -c "
import tarfile, io
a = tarfile.TarInfo('data/a.txt'); a.size = 3
b = tarfile.TarInfo('data/b.txt'); b.type = tarfile.LNKTYPE; b.linkname = 'data/a.txt'
with tarfile.open('$hl_tar', 'w') as tf:
    tf.addfile(a, io.BytesIO(b'abc')); tf.addfile(b)" 2>/dev/null
new_out=$("$NEW_CLI" extract "$hl_tar" --output "$hl_dest" 2>/dev/null); new_exit=$?
assert_equal "tar extract 硬链接 exit" 0 "$new_exit"
assert_equal "tar extract 硬链接内容复制" "abc" "$(cat "$hl_dest/data/b.txt" 2>/dev/null)"
# 7z / rar 真读取（夹具入库 core/testdata；批次 C）
FIXTURE_DIR="$(cd "$(dirname "$0")/.." && pwd)/core/testdata"
sevenz_dest="/tmp/zpack-verify-7z-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/7z/basic.7z" --output "$sevenz_dest" 2>/dev/null); new_exit=$?
assert_equal "7z extract exit" 0 "$new_exit"
assert_equal "7z file_count" 2 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "7z 中文路径内容" "7z 夹具载荷：中文内容与空格路径" "$(cat "$sevenz_dest/fixture-root/资料 目录/载荷 文件.txt" 2>/dev/null)"
rar4_dest="/tmp/zpack-verify-rar4-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/rar/single-rar4.rar" --output "$rar4_dest" 2>/dev/null); new_exit=$?
assert_equal "rar4 单卷 extract exit" 0 "$new_exit"
if echo "$new_out" | grep -q '"is_success":true'; then report_pass; else report_fail "rar4 单卷成功JSON" "is_success" "$new_out"; fi
rarmulti_dest="/tmp/zpack-verify-rarmulti-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/rar/test.part01.rar" --output "$rarmulti_dest" 2>/dev/null); new_exit=$?
assert_equal "rar 多卷续卷 extract exit" 0 "$new_exit"
if echo "$new_out" | grep -q '"is_success":true'; then report_pass; else report_fail "rar 多卷成功JSON" "is_success" "$new_out"; fi
# 伪头明确报错（铁律：禁止静默失败）
sevenz_magic="/tmp/zpack-verify-7z-$$"
printf '7z\xbc\xaf\x27\x1c rest' > "$sevenz_magic"
new_out=$("$NEW_CLI" extract "$sevenz_magic" 2>/dev/null); new_exit=$?
assert_equal "extract 伪 7z 头 exit" 1 "$new_exit"
if echo "$new_out" | grep -q '无法读取 7z 归档'; then report_pass; else report_fail "extract 伪 7z 头报错JSON" "无法读取 7z 归档" "$new_out"; fi
rar_magic="/tmp/zpack-verify-rar-$$"
printf 'Rar!\x1a\x07\x00\x00\x00\x00' > "$rar_magic"
new_out=$("$NEW_CLI" extract "$rar_magic" 2>/dev/null); new_exit=$?
assert_equal "extract 伪 rar 头 exit" 1 "$new_exit"
if echo "$new_out" | grep -q '无法读取 rar 归档'; then report_pass; else report_fail "extract 伪 rar 头报错JSON" "无法读取 rar 归档" "$new_out"; fi
# 单流格式与 .001 分卷（gzip/bzip2 单文件；分卷拼 zip；非归档分卷还原）
plain_gz="/tmp/zpack-verify-gzsingle-$$.gz"
printf 'gzip single file payload 中文' | gzip > "$plain_gz"
gz_out="/tmp/zpack-verify-gzsingle-dest-$$"
new_out=$("$NEW_CLI" extract "$plain_gz" --output "$gz_out" 2>/dev/null); new_exit=$?
assert_equal "gzip 单流 extract exit" 0 "$new_exit"
assert_equal "gzip 单流解出内容" "gzip single file payload 中文" "$(cat "$gz_out/$(basename "$plain_gz" .gz)" 2>/dev/null)"
new_out=$("$NEW_CLI" extract "$plain_gz" --output "$gz_out" 2>/dev/null)
assert_equal "gzip 单流重复解压默认跳过" 1 "$(echo "$new_out" | grep -o '"skipped":[0-9]*' | grep -o '[0-9]*$')"
plain_bz2="/tmp/zpack-verify-bz2-$$.bz2"
printf 'bzip2 single stream 中文载荷' | bzip2 > "$plain_bz2"
bz2_out="/tmp/zpack-verify-bz2-dest-$$"
new_out=$("$NEW_CLI" extract "$plain_bz2" --output "$bz2_out" 2>/dev/null); new_exit=$?
assert_equal "bzip2 单流 extract exit" 0 "$new_exit"
assert_equal "bzip2 单流解出内容" "bzip2 single stream 中文载荷" "$(cat "$bz2_out/$(basename "$plain_bz2" .bz2)" 2>/dev/null)"
# .001 分卷拼 zip：三段切分，从首卷解出（zstd 单流由 core 单测覆盖——系统无 zstd CLI）
sv_src="/tmp/zpack-verify-svsrc-$$"
sv_zip="/tmp/zpack-verify-split-$$.zip"
mkdir -p "$sv_src"
printf 'split payload 中文' > "$sv_src/inner.txt"
"$NEW_CLI" compress "$sv_src" --output "$sv_zip" >/dev/null 2>&1
sv_dir="/tmp/zpack-verify-svdir-$$"
mkdir -p "$sv_dir"
python3 -c "
data = open('$sv_zip','rb').read()
size = max(1, (len(data) + 2) // 3)
for idx, start in enumerate(range(0, len(data), size), 1):
    open(f'$sv_dir/payload.zip.{idx:03d}','wb').write(data[start:start+size])" 2>/dev/null
sv_dest="/tmp/zpack-verify-svdest-$$"
new_out=$("$NEW_CLI" extract "$sv_dir/payload.zip.001" --output "$sv_dest" 2>/dev/null); new_exit=$?
assert_equal ".001 分卷拼 zip extract exit" 0 "$new_exit"
assert_equal ".001 分卷内容还原" "split payload 中文" "$(cat "$sv_dest/$(basename "$sv_src")/inner.txt" 2>/dev/null)"
raw_dir="/tmp/zpack-verify-rawdir-$$"
raw_out="/tmp/zpack-verify-rawdest-$$"
mkdir -p "$raw_dir"
printf 'raw binary payload part' > "$raw_dir/video.mp4.001"
printf '%s' '-second-part' > "$raw_dir/video.mp4.002"
new_out=$("$NEW_CLI" extract "$raw_dir/video.mp4.001" --output "$raw_out" 2>/dev/null); new_exit=$?
assert_equal "非归档分卷还原 exit" 0 "$new_exit"
assert_equal "非归档分卷拼接内容" "raw binary payload part-second-part" "$(cat "$raw_out/video.mp4" 2>/dev/null)"
# 遗留编码格式（批次 D-1）：.Z（compress LZW）/ uuencode / xxencode
z_out="/tmp/zpack-verify-z-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/z/sample.txt.Z" --output "$z_out" 2>/dev/null); new_exit=$?
assert_equal ".Z 单流 extract exit" 0 "$new_exit"
assert_equal ".Z file_count" 1 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
if [ "$(head -c 9 "$z_out/sample.txt" 2>/dev/null)" = "1E0A;0044" ]; then report_pass; else report_fail ".Z 内容头校验" "1E0A;0044" "$(head -c 9 "$z_out/sample.txt" 2>/dev/null)"; fi
uue_out="/tmp/zpack-verify-uue-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/uue/basic.uue" --output "$uue_out" 2>/dev/null); new_exit=$?
assert_equal "uuencode extract exit" 0 "$new_exit"
if [ -f "$uue_out/data.bin" ]; then report_pass; else report_fail "uuencode 内嵌文件名解出" "data.bin 存在" "缺失"; fi
xxe_out="/tmp/zpack-verify-xxe-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/uue/basic.xxe" --output "$xxe_out" 2>/dev/null); new_exit=$?
assert_equal "xxencode extract exit" 0 "$new_exit"
if [ -f "$xxe_out/xx-payload.bin" ]; then report_pass; else report_fail "xxencode 内嵌文件名解出" "xx-payload.bin 存在" "缺失"; fi
# 容器格式（批次 D-2）：ar / asar / iso
ar_out="/tmp/zpack-verify-ar-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/ar/basic.ar" --output "$ar_out" 2>/dev/null); new_exit=$?
assert_equal "ar extract exit" 0 "$new_exit"
assert_equal "ar file_count（含长名表条目）" 3 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "ar 中文内容" "ar 夹具：中文与空格" "$(cat "$ar_out/载荷 文件.txt" 2>/dev/null)"
asar_out="/tmp/zpack-verify-asar-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/asar/basic.asar" --output "$asar_out" 2>/dev/null); new_exit=$?
assert_equal "asar extract exit" 0 "$new_exit"
assert_equal "asar file_count" 2 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "asar 嵌套内容" "second file contents" "$(cat "$asar_out/app/assets/logo.txt" 2>/dev/null)"
iso_out="/tmp/zpack-verify-iso-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/iso/basic.iso" --output "$iso_out" 2>/dev/null); new_exit=$?
assert_equal "iso extract exit" 0 "$new_exit"
assert_equal "iso b.txt 内容" "second" "$(cat "$iso_out/b.txt" 2>/dev/null)"
# 批次 E-1：lzip + wim
lz_out="/tmp/zpack-verify-lz-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/lzip/sample.txt.lz" --output "$lz_out" 2>/dev/null); new_exit=$?
assert_equal "lzip extract exit" 0 "$new_exit"
assert_equal "lzip file_count" 1 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
if [ "$(head -c 5 "$lz_out/sample.txt" 2>/dev/null)" = "lzip " ] && [ "$(stat -f%z "$lz_out/sample.txt" 2>/dev/null)" = "11800" ]; then report_pass; else report_fail "lzip 内容与尺寸" "lzip 前缀+11800B" "$(head -c 5 "$lz_out/sample.txt" 2>/dev/null) $(stat -f%z "$lz_out/sample.txt" 2>/dev/null)B"; fi
wim_out="/tmp/zpack-verify-wim-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/wim/basic.wim" --output "$wim_out" 2>/dev/null); new_exit=$?
assert_equal "wim extract exit" 0 "$new_exit"
assert_equal "wim file_count" 3 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "wim 中文内容" "wim 夹具载荷：中文与空格路径" "$(cat "$wim_out/资料 目录/载荷 文件.txt" 2>/dev/null)"
assert_equal "wim LZX 大文件" "32768" "$(stat -f%z "$wim_out/big-ish.bin" 2>/dev/null)"
# 批次 E-2：cab（存储/MSZIP/LZX+E8 三夹具，7z 交叉口径）
cab_out="/tmp/zpack-verify-cab-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/cab/stored.cab" --output "$cab_out" 2>/dev/null); new_exit=$?
assert_equal "cab 存储柜 extract exit" 0 "$new_exit"
assert_equal "cab 存储柜 file_count" 3 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
assert_equal "cab 存储柜 file1 长度" "60" "$(stat -f%z "$cab_out/dir1/file1" 2>/dev/null)"
cab2_out="/tmp/zpack-verify-cab2-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/cab/mszip.cab" --output "$cab2_out" 2>/dev/null); new_exit=$?
assert_equal "cab MSZIP extract exit" 0 "$new_exit"
assert_equal "cab MSZIP zero 长度" "33000" "$(stat -f%z "$cab2_out/zero" 2>/dev/null)"
cab3_out="/tmp/zpack-verify-cab3-dest-$$"
new_out=$("$NEW_CLI" extract "$FIXTURE_DIR/cab/lzx.cab" --output "$cab3_out" 2>/dev/null); new_exit=$?
assert_equal "cab LZX+E8 extract exit" 0 "$new_exit"
assert_equal "cab LZX+E8 e8.bin 长度" "30" "$(stat -f%z "$cab3_out/e8.bin" 2>/dev/null)"
# 分卷创建（--volume-size，格式无关字节切分）
vol_src="/tmp/zpack-verify-vol-src-$$"
vol_dir="/tmp/zpack-verify-vol-dest-$$"
mkdir -p "$vol_src"
head -c 200000 /dev/urandom > "$vol_src/data.bin"
printf '分卷测试中文' > "$vol_src/note.txt"

# tar 分卷（300KB → 64KB/卷 = 5 卷）
new_out=$("$NEW_CLI" compress "$vol_src" --output "/tmp/zpack-verify-vol-$$.tar" --volume-size 64KB 2>/dev/null); new_exit=$?
assert_equal "tar 分卷创建 exit" 0 "$new_exit"
vol_count=$(ls /tmp/zpack-verify-vol-$$\.tar.* 2>/dev/null | wc -l | tr -d ' ')
if [ "$vol_count" -ge 4 ]; then report_pass; else report_fail "tar 分卷数" "≥4（200KB/64KB）" "$vol_count"; fi

# cat 还原合法 tar
cat /tmp/zpack-verify-vol-$$\.tar.* > /tmp/zpack-verify-vol-$$-merged.tar
if tar -tf /tmp/zpack-verify-vol-$$-merged.tar >/dev/null 2>&1; then report_pass; else report_fail "cat 合并还原合法 tar" "合法" "损坏"; fi

# .001 自动拼接解压往返
mkdir -p "$vol_dir"
new_out=$("$NEW_CLI" extract "/tmp/zpack-verify-vol-$$.tar.001" --output "$vol_dir" 2>/dev/null); new_exit=$?
assert_equal "tar 分卷解压 exit" 0 "$new_exit"
cmp -s "$vol_src/data.bin" "$vol_dir/$(basename "$vol_src")/data.bin"
assert_equal "tar 分卷随机数据往返" 0 "$?"

# zip 分卷 + 解压往返
new_out=$("$NEW_CLI" compress "$vol_src" --output "/tmp/zpack-verify-vol-$$.zip" --volume-size 100KB 2>/dev/null); new_exit=$?
assert_equal "zip 分卷创建 exit" 0 "$new_exit"
vol_zip_dir="/tmp/zpack-verify-vol-zip-dest-$$"
mkdir -p "$vol_zip_dir"
new_out=$("$NEW_CLI" extract "/tmp/zpack-verify-vol-$$.zip.001" --output "$vol_zip_dir" 2>/dev/null); new_exit=$?
assert_equal "zip 分卷解压 exit" 0 "$new_exit"
cmp -s "$vol_src/data.bin" "$vol_zip_dir/$(basename "$vol_src")/data.bin"
assert_equal "zip 分卷往返" 0 "$?"

# 卷太小报错（下限 64KB=65536）
new_out=$("$NEW_CLI" compress "$vol_src" --output "/tmp/zpack-verify-vol-small-$$.zip" --volume-size 100 2>/dev/null); new_exit=$?
assert_equal "分卷太小 exit" 1 "$new_exit"
if echo "$new_out" | grep -q '65536'; then report_pass; else report_fail "分卷太小报错含 65536" "≥65536" "$new_out"; fi

# 分卷模板（介质标称容量 SI 精确字节）：小输入 → 单卷即可容纳 → 不分卷不产 .001（保持原名）
new_out=$("$NEW_CLI" compress "$vol_src" --output "/tmp/zpack-verify-vol-tpl-$$.zip" --volume-size vcd 2>/dev/null); new_exit=$?
assert_equal "模板 vcd 创建 exit" 0 "$new_exit"
if [ -f "/tmp/zpack-verify-vol-tpl-$$.zip" ] && [ ! -f "/tmp/zpack-verify-vol-tpl-$$.zip.001" ]; then report_pass; else report_fail "模板 vcd 单卷不分卷" "仅原名义档" "存在 .001 或义档缺失"; fi
vol_tpl_size=$(stat -f%z "/tmp/zpack-verify-vol-tpl-$$.zip" 2>/dev/null)
if [ "$vol_tpl_size" -gt 0 ] && [ "$vol_tpl_size" -lt 700000000 ]; then report_pass; else report_fail "模板 vcd 义档尺寸" "0<尺寸<700000000" "$vol_tpl_size"; fi
# 单卷义档直接解压往返（原名即普通 zip）
vol_tpl_dir="/tmp/zpack-verify-vol-tpl-dest-$$"
mkdir -p "$vol_tpl_dir"
new_out=$("$NEW_CLI" extract "/tmp/zpack-verify-vol-tpl-$$.zip" --output "$vol_tpl_dir" 2>/dev/null); new_exit=$?
assert_equal "模板单卷解压 exit" 0 "$new_exit"
cmp -s "$vol_src/data.bin" "$vol_tpl_dir/$(basename "$vol_src")/data.bin"
assert_equal "模板单卷往返" 0 "$?"

# dvd 模板大小写归一
"$NEW_CLI" compress "$vol_src" --output "/tmp/zpack-verify-vol-tpl2-$$.zip" --volume-size DVD >/dev/null 2>&1
assert_equal "模板 DVD 大小写 exit" 0 "$?"

# 未知模板报错
new_out=$("$NEW_CLI" compress "$vol_src" --output "/tmp/zpack-verify-vol-tpl3-$$.zip" --volume-size naver 2>/dev/null); new_exit=$?
assert_equal "未知模板 exit" 1 "$new_exit"
if echo "$new_out" | grep -q '模板'; then report_pass; else report_fail "未知模板报错文案" "含模板提示" "$new_out"; fi

rm -rf "$vol_tpl_dir" /tmp/zpack-verify-vol-tpl-*$$* /tmp/zpack-verify-vol-tpl2-*$$* /tmp/zpack-verify-vol-tpl3-*$$*

rm -rf "$vol_src" "$vol_dir" "$vol_zip_dir" /tmp/zpack-verify-vol-$$\.tar.* /tmp/zpack-verify-vol-$$-merged.tar /tmp/zpack-verify-vol-$$.zip.*
# .zpackignore 排除规则
zi_src="/tmp/zpack-verify-zi-$$"
mkdir -p "$zi_src/dist" "$zi_src/doc"
echo "code" > "$zi_src/main.go"
echo "build" > "$zi_src/dist/out.js"
echo "doc" > "$zi_src/doc/guide.md"
echo "log" > "$zi_src/debug.log"
echo "important" > "$zi_src/important.log"
cat > "$zi_src/.zpackignore" << 'ZI'
dist/
*.log
!important.log
ZI
new_out=$("$NEW_CLI" compress "$zi_src" --output "/tmp/zpack-verify-zi-$$.zip" 2>/dev/null)
assert_equal "zpackignore file_count" 3 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"
if unzip -l "/tmp/zpack-verify-zi-$$.zip" 2>/dev/null | grep -q 'dist/'; then report_fail "zpackignore 排除 dist/" "已排除" "包内"; else report_pass; fi
if unzip -l "/tmp/zpack-verify-zi-$$.zip" 2>/dev/null | grep -q 'debug\.log'; then report_fail "zpackignore 排除 *.log" "已排除" "包内"; else report_pass; fi
if unzip -l "/tmp/zpack-verify-zi-$$.zip" 2>/dev/null | grep -q 'important\.log'; then report_pass; else report_fail "zpackignore !反选保留" "存在" "缺失"; fi
if unzip -l "/tmp/zpack-verify-zi-$$.zip" 2>/dev/null | grep -q '.zpackignore'; then report_fail "zpackignore 自身排除" "已排除" "包内"; else report_pass; fi
rm -rf "$zi_src" "/tmp/zpack-verify-zi-$$.zip"
rm -rf "$tar_src" "$tar_out.bin" "$targz_out" "$tar_out" "$tgz_out.bin" "$tar_dest" "$tar_dest_gz" "$tar_dest-bin" "$tar_dest-tgzbin" "$scat_tar" "$scat_dir" "$root_tar" "$root_dir" "$slip_tar" "$slip_tar_dest" "/tmp/evil-tarslip-$$.txt" "$esc_tar" "/tmp/zpack-verify-taresc-dest-$$" "$dev_tar" "/tmp/zpack-verify-tardev-dest-$$" "$hl_tar" "$hl_dest" "$sevenz_magic" "$rar_magic" "$sevenz_dest" "$rar4_dest" "$rarmulti_dest" "$z_out" "$uue_out" "$xxe_out" "$ar_out" "$asar_out" "$iso_out" "$lz_out" "$wim_out" "$cab_out" "$cab2_out" "$cab3_out" "$plain_gz" "$tbz2_tar" "$tbz2_dest" "$gz_out" "$plain_bz2" "$bz2_out" "$sv_src" "$sv_zip" "$sv_dir" "$sv_dest" "$raw_dir" "$raw_out"

# base64 编解码（甜点功能）
b64_out=$("$NEW_CLI" base64 encode "你好"); assert_equal "base64 encode" "5L2g5aW9" "$b64_out"
b64_back=$("$NEW_CLI" base64 decode "5L2g5aW9"); assert_equal "base64 decode 往返" "你好" "$b64_back"
b64_file="/tmp/zpack-verify-b64-$$"
head -c 256 /dev/urandom > "$b64_file.bin"
"$NEW_CLI" base64 encode --file "$b64_file.bin" > "$b64_file.b64" 2>/dev/null
"$NEW_CLI" base64 decode --file "$b64_file.b64" --output "$b64_file.out" >/dev/null 2>&1
cmp -s "$b64_file.bin" "$b64_file.out"; assert_equal "base64 二进制文件往返" 0 "$?"
new_out=$("$NEW_CLI" base64 decode "!!!" 2>/dev/null); new_exit=$?
assert_equal "base64 非法输入 exit" 1 "$new_exit"
rm -f "$b64_file"*
# crypto RSA（甜点功能）
cr_dir="/tmp/zpack-verify-crypto-$$"
mkdir -p "$cr_dir"
"$NEW_CLI" crypto genkey --bits 2048 2>/dev/null | python3 -c "
import json, sys
r = json.load(sys.stdin)['data']
open('$cr_dir/pub.pem', 'w').write(r['public_key'])
open('$cr_dir/priv.pem', 'w').write(r['private_key'])
" 2>/dev/null
[ -s "$cr_dir/pub.pem" ] && [ -s "$cr_dir/priv.pem" ]; assert_equal "crypto genkey 密钥产出" 0 "$?"
CR_ENC=$("$NEW_CLI" crypto encrypt --key-file "$cr_dir/pub.pem" "你好secret" 2>/dev/null)
CR_DEC=$("$NEW_CLI" crypto decrypt --key-file "$cr_dir/priv.pem" "$CR_ENC" 2>/dev/null)
assert_equal "crypto 短文本加密解密" "你好secret" "$CR_DEC"
head -c 5000 /dev/urandom > "$cr_dir/big.bin"
"$NEW_CLI" crypto encrypt --key-file "$cr_dir/pub.pem" --file "$cr_dir/big.bin" > "$cr_dir/big.enc" 2>/dev/null
"$NEW_CLI" crypto decrypt --key-file "$cr_dir/priv.pem" --file "$cr_dir/big.enc" --output "$cr_dir/big.out" >/dev/null 2>&1
cmp -s "$cr_dir/big.bin" "$cr_dir/big.out"; assert_equal "crypto 混合加密 5KB 往返" 0 "$?"
new_out=$("$NEW_CLI" crypto encrypt --key-file "$cr_dir/priv.pem" "wrong" 2>/dev/null); new_exit=$?
assert_equal "crypto 错误密钥 exit" 1 "$new_exit"
rm -rf "$cr_dir"
# list / test / info 命令
li_src="/tmp/zpack-verify-li-$$"
mkdir -p "$li_src/sub"
echo "hello" > "$li_src/a.txt"
echo "中文" > "$li_src/sub/b.txt"
printf '\xff\xd8\xff\xe0JFIF' > "$li_src/pic.jpg"
"$NEW_CLI" compress "$li_src" --output "/tmp/zpack-verify-li-$$.zip" >/dev/null 2>&1

new_out=$("$NEW_CLI" info "/tmp/zpack-verify-li-$$.zip" 2>/dev/null); new_exit=$?
assert_equal "info exit" 0 "$new_exit"
assert_equal "info format" "zip" "$(echo "$new_out" | grep -o '"format":"[^"]*"' | cut -d'"' -f4)"
assert_equal "info file_count" 3 "$(echo "$new_out" | grep -o '"file_count":[0-9]*' | grep -o '[0-9]*$')"

new_out=$("$NEW_CLI" list "/tmp/zpack-verify-li-$$.zip" 2>/dev/null)
entry_count=$(echo "$new_out" | grep -o '"name"' | wc -l | tr -d ' ')
assert_equal "list 条目数" 3 "$entry_count"
if echo "$new_out" | grep -q 'pic\.jpg'; then report_pass; else report_fail "list 含 jpg" "存在" "缺失"; fi

new_out=$("$NEW_CLI" test "/tmp/zpack-verify-li-$$.zip" 2>/dev/null); new_exit=$?
assert_equal "test exit" 0 "$new_exit"
if echo "$new_out" | grep -q '"all_ok":true'; then report_pass; else report_fail "test all_ok" "true" "$new_out"; fi

# tar.gz
"$NEW_CLI" compress "$li_src" --output "/tmp/zpack-verify-li-$$.tar.gz" >/dev/null 2>&1
new_out=$("$NEW_CLI" test "/tmp/zpack-verify-li-$$.tar.gz" 2>/dev/null)
if echo "$new_out" | grep -q '"all_ok":true'; then report_pass; else report_fail "test tar.gz" "true" "$new_out"; fi

# 加密 zip info
"$NEW_CLI" compress "$li_src" --output "/tmp/zpack-verify-li-enc.zip" --password PW >/dev/null 2>&1
new_out=$("$NEW_CLI" info "/tmp/zpack-verify-li-enc.zip" 2>/dev/null)
if echo "$new_out" | grep -q '"encrypted":true'; then report_pass; else report_fail "info 加密标记" "true" "$new_out"; fi

# rar
new_out=$("$NEW_CLI" info "$FIXTURE_DIR/rar/single-rar4.rar" 2>/dev/null)
assert_equal "info rar format" "rar" "$(echo "$new_out" | grep -o '"format":"[^"]*"' | cut -d'"' -f4)"

rm -rf "$li_src" /tmp/zpack-verify-li-*

# ---------- hash：哈希计算与校验（标准测试向量 + shasum 互操作） ----------
h_src="/tmp/zpack-verify-hash-$$"
mkdir -p "$h_src"
printf 'abc' > "$h_src/vector.txt"
printf 'hello' > "$h_src/one.txt"
printf 'world' > "$h_src/two.txt"

# 计算：sha256 默认算法（FIPS 180 标准向量）
new_out=$("$NEW_CLI" hash "$h_src/vector.txt" 2>/dev/null); new_exit=$?
assert_equal "hash exit" 0 "$new_exit"
assert_equal "hash sha256 向量" "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" "$(echo "$new_out" | grep -o '"hash":"[0-9a-f]*"' | head -1 | cut -d'"' -f4)"

# 计算：md5（RFC 1321 标准向量）
new_out=$("$NEW_CLI" hash "$h_src/vector.txt" --algorithm md5 2>/dev/null)
assert_equal "hash md5 向量" "900150983cd24fb0d6963f7d28e17f72" "$(echo "$new_out" | grep -o '"hash":"[0-9a-f]*"' | head -1 | cut -d'"' -f4)"

# 计算：SHA-256 大小写连字符别名归一
new_out=$("$NEW_CLI" hash "$h_src/vector.txt" --algorithm SHA-256 2>/dev/null)
if echo "$new_out" | grep -q 'ba7816bf'; then report_pass; else report_fail "hash 算法别名" "归一命中" "$new_out"; fi

# 计算：crc32（IEEE 标准向量 "123456789"→cbf43926）
printf '123456789' > "$h_src/crc.txt"
new_out=$("$NEW_CLI" hash "$h_src/crc.txt" --algorithm crc32 2>/dev/null)
assert_equal "hash crc32 向量" "cbf43926" "$(echo "$new_out" | grep -o '"hash":"[0-9a-f]*"' | head -1 | cut -d'"' -f4)"

# 计算：多文件逐条
new_out=$("$NEW_CLI" hash "$h_src/one.txt" "$h_src/two.txt" 2>/dev/null)
assert_equal "hash 多文件条数" 2 "$(echo "$new_out" | grep -o '"hash":' | wc -l | tr -d ' ')"

# 退化：未知算法 / 不存在路径 / 目录 / expect 长度错
"$NEW_CLI" hash "$h_src/one.txt" --algorithm md6 >/dev/null 2>&1; assert_equal "hash 未知算法 exit" 1 "$?"
"$NEW_CLI" hash "$h_src/no-such-file" >/dev/null 2>&1; assert_equal "hash 不存在 exit" 1 "$?"
"$NEW_CLI" hash "$h_src" >/dev/null 2>&1; assert_equal "hash 目录 exit" 1 "$?"
"$NEW_CLI" hash "$h_src/one.txt" --expect deadbeef >/dev/null 2>&1; assert_equal "hash expect 长度错 exit" 1 "$?"
"$NEW_CLI" hash >/dev/null 2>&1; assert_equal "hash 无参数 exit" 1 "$?"

# --expect 匹配（exit 0 + is_match true）
"$NEW_CLI" hash "$h_src/one.txt" --expect 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824 >/dev/null 2>&1
assert_equal "hash expect 匹配 exit" 0 "$?"
new_out=$("$NEW_CLI" hash "$h_src/one.txt" --expect 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824 2>/dev/null)
if echo "$new_out" | grep -q '"is_match":true'; then report_pass; else report_fail "hash expect 匹配字段" "true" "$new_out"; fi

# --expect 不匹配（exit 1 + 结构化 JSON 含 actual）
new_out=$("$NEW_CLI" hash "$h_src/one.txt" --expect 0000000000000000000000000000000000000000000000000000000000000000 2>/dev/null); new_exit=$?
assert_equal "hash 不匹配 exit" 1 "$new_exit"
if echo "$new_out" | grep -q '"is_match":false' && echo "$new_out" | grep -q '"hash":"2cf24dba'; then report_pass; else report_fail "hash 不匹配字段" "is_match:false+actual" "$new_out"; fi

# --output 生成清单 → shasum -c 互操作（外部工具验收 zpack 产出）
"$NEW_CLI" hash "$h_src/one.txt" "$h_src/two.txt" --output "$h_src/sums.txt" >/dev/null 2>&1
if shasum -a 256 -c "$h_src/sums.txt" >/dev/null 2>&1; then report_pass; else report_fail "hash 清单 shasum 互操作" "shasum -c 全 OK" "失败"; fi
# shasum 产出的清单 zpack 也能校验（双向互操作）
shasum -a 256 "$h_src/one.txt" > "$h_src/ext-sums.txt"
"$NEW_CLI" hash --check "$h_src/ext-sums.txt" >/dev/null 2>&1
assert_equal "hash --check 外部清单 exit" 0 "$?"

# --check 全匹配
new_out=$("$NEW_CLI" hash --check "$h_src/sums.txt" 2>/dev/null); new_exit=$?
assert_equal "hash check exit" 0 "$new_exit"
assert_equal "hash check matched" 2 "$(echo "$new_out" | grep -o '"matched":[0-9]*' | grep -o '[0-9]*$')"

# --check 篡改抓获（改一字节 → mismatched 记录 + exit 1）
printf 'tampered' > "$h_src/one.txt"
new_out=$("$NEW_CLI" hash --check "$h_src/sums.txt" 2>/dev/null); new_exit=$?
assert_equal "hash check 篡改 exit" 1 "$new_exit"
if echo "$new_out" | grep -q '"mismatched":\[' && echo "$new_out" | grep -q 'one\.txt'; then report_pass; else report_fail "hash check 篡改记录" "mismatched 含 one.txt" "$new_out"; fi

# --check 缺失文件（missing 记录 + exit 1）
rm "$h_src/two.txt"
new_out=$("$NEW_CLI" hash --check "$h_src/sums.txt" 2>/dev/null); new_exit=$?
assert_equal "hash check 缺失 exit" 1 "$new_exit"
if echo "$new_out" | grep -q '"missing":\['; then report_pass; else report_fail "hash check 缺失记录" "missing 非空" "$new_out"; fi

rm -rf "$h_src"

# ---------- --temp-dir：临时目录落点指定（convert 中转/bench 语料/core 溢写） ----------
td_src="/tmp/zpack-verify-td-$$"
td_tmp="/tmp/zpack-verify-td-tmp-$$"
td_out="/tmp/zpack-verify-td-out-$$"
mkdir -p "$td_src"
printf '临时目录测试中文' > "$td_src/a.txt"
"$NEW_CLI" compress "$td_src" --output "$td_src.zip" >/dev/null 2>&1

# convert --temp-dir：嵌套不存在的目录自动创建、转换成功、事后无残留
new_out=$("$NEW_CLI" convert "$td_src.zip" "$td_src.tar.gz" --temp-dir "$td_tmp/nested/deep" 2>/dev/null); new_exit=$?
assert_equal "convert --temp-dir exit" 0 "$new_exit"
if [ -d "$td_tmp/nested/deep" ]; then report_pass; else report_fail "temp-dir 自动创建" "目录存在" "缺失"; fi
td_leftover=$(ls "$td_tmp/nested/deep" 2>/dev/null | grep -c "zpack-convert")
assert_equal "convert 临时中转清理" 0 "$td_leftover"
# 转换产物真实可用
"$NEW_CLI" extract "$td_src.tar.gz" --output "$td_out" >/dev/null 2>&1
assert_equal "convert --temp-dir 产物可解" 0 "$?"
cmp -s "$td_src/a.txt" "$td_out/$(basename "$td_src" .zip 2>/dev/null || basename "$td_src")/a.txt" 2>/dev/null || cmp -s "$td_src/a.txt" "$td_out/a.txt" 2>/dev/null
assert_equal "convert --temp-dir 往返" 0 "$?"

# compress/extract --temp-dir 旗标生效（溢写落点，行为不变 exit 0）
"$NEW_CLI" compress "$td_src" --output "$td_src-2.zip" --temp-dir "$td_tmp" >/dev/null 2>&1
assert_equal "compress --temp-dir exit" 0 "$?"
"$NEW_CLI" extract "$td_src-2.zip" --output "$td_out-2" --temp-dir "$td_tmp" >/dev/null 2>&1
assert_equal "extract --temp-dir exit" 0 "$?"

# bench --temp-dir（--size 1 最小语料快速跑）
"$NEW_CLI" bench --size 1 --temp-dir "$td_tmp" >/dev/null 2>&1
assert_equal "bench --temp-dir exit" 0 "$?"
td_bench_leftover=$(ls "$td_tmp" 2>/dev/null | grep -c "zpack-bench")
assert_equal "bench 语料清理" 0 "$td_bench_leftover"

# --temp-dir 缺值报错
"$NEW_CLI" convert "$td_src.zip" --temp-dir >/dev/null 2>&1
assert_equal "--temp-dir 缺值 exit" 1 "$?"

rm -rf "$td_src" "$td_src.zip" "$td_src.tar.gz" "$td_src-2.zip" "$td_tmp" "$td_out" "$td_out-2"

# ---------- agent 自描述：子命令 --help 守卫 + skill 文档分发 ----------
# --help 守卫（DisableFlagParsing 曾吞掉 --help 而报参数错误——agent 嗅探断点）
"$NEW_CLI" compress --help >/dev/null 2>&1
assert_equal "compress --help exit" 0 "$?"
new_out=$("$NEW_CLI" compress --help 2>/dev/null)
if echo "$new_out" | grep -q 'zpack compress 目录/'; then report_pass; else report_fail "compress --help 示例" "含示例" "$new_out"; fi
"$NEW_CLI" extract --help >/dev/null 2>&1
assert_equal "extract --help exit" 0 "$?"
"$NEW_CLI" hash --help >/dev/null 2>&1
assert_equal "hash --help exit" 0 "$?"

# skill：内嵌 SKILL.md 输出与安装
new_out=$("$NEW_CLI" skill 2>/dev/null); new_exit=$?
assert_equal "skill 输出 exit" 0 "$new_exit"
if echo "$new_out" | head -3 | grep -q 'name: zpack'; then report_pass; else report_fail "skill frontmatter" "name: zpack" "缺失"; fi
if echo "$new_out" | grep -q 'is_success' && echo "$new_out" | grep -q 'volume-size'; then report_pass; else report_fail "skill 内容" "契约+旗标" "缺失"; fi
sk_root="/tmp/zpack-verify-skill-$$"
new_out=$("$NEW_CLI" skill --install "$sk_root" 2>/dev/null); new_exit=$?
assert_equal "skill 安装 exit" 0 "$new_exit"
if [ -f "$sk_root/zpack/SKILL.md" ]; then report_pass; else report_fail "skill 落位" "$sk_root/zpack/SKILL.md" "缺失"; fi
cmp -s <("$NEW_CLI" skill 2>/dev/null) "$sk_root/zpack/SKILL.md"
assert_equal "skill 安装内容一致" 0 "$?"
rm -rf "$sk_root"
# ── pdf 命令族（纯 Go——pdfcpu；夹具=python 手写最小合法 PDF）──
WORK="/tmp/zpack-verify-pdf-$$"
mkdir -p "$WORK"
trap 'rm -rf "$WORK"' EXIT
make_pdf() { # 参数：路径 页数
python3 -I - "$1" "$2" <<'PYX'
import sys
path, pages = sys.argv[1], int(sys.argv[2])
objs = {1: b"<< /Type /Catalog /Pages 2 0 R >>"}
kids = " ".join(f"{3+2*i} 0 R" for i in range(pages))
objs[2] = f"<< /Type /Pages /Kids [{kids}] /Count {pages} >>".encode()
for i in range(pages):
    n = 3 + 2*i
    content = f"0.0 0.0 {100+i} {140+i} re f".encode()
    objs[n] = f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 400] /Contents {n+1} 0 R >>".encode()
    objs[n+1] = b"<< /Length %d >>\nstream\n" % len(content) + content + b"\nendstream"
out = bytearray(b"%PDF-1.4\n")
offsets = {}
for n in sorted(objs):
    offsets[n] = len(out)
    out += f"{n} 0 obj\n".encode() + objs[n] + b"\nendobj\n"
xref = len(out)
mx = max(objs)
out += f"xref\n0 {mx+1}\n".encode() + b"0000000000 65535 f \n"
for n in range(1, mx+1):
    out += f"{offsets[n]:010d} 00000 n \n".encode()
out += f"trailer\n<< /Size {mx+1} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF".encode()
open(path, "wb").write(out)
PYX
}
make_pdf "$WORK/pdf-a.pdf" 2
make_pdf "$WORK/pdf-b.pdf" 3
make_pdf "$WORK/pdf-c.pdf" 5
# info
out=$("$NEW_CLI" pdf info "$WORK/pdf-c.pdf" 2>/dev/null); exit_code=$?
assert_equal "pdf info exit" 0 "$exit_code"
if echo "$out" | grep -q '"page_count":5'; then report_pass; else report_fail "pdf info 页数" "5" "$out"; fi
if echo "$out" | grep -q '"width_mm":105.83'; then report_pass; else report_fail "pdf info mm 换算" "105.83" "$out"; fi
# merge：2+3 → 5 页
out=$("$NEW_CLI" pdf merge "$WORK/pdf-a.pdf" "$WORK/pdf-b.pdf" "$WORK/pdf-merged.pdf" 2>/dev/null); exit_code=$?
assert_equal "pdf merge exit" 0 "$exit_code"
if echo "$out" | grep -q '"page_count":5'; then report_pass; else report_fail "pdf merge 页数（2+3）" "5" "$out"; fi
if echo "$out" | grep -q '"input_count":2'; then report_pass; else report_fail "pdf merge 输入计数" "2" "$out"; fi
# merge 产物可再读
"$NEW_CLI" pdf info "$WORK/pdf-merged.pdf" >/dev/null 2>&1; assert_equal "pdf merge 产物可读" 0 "$?"
# extract：闭区间/单页/开尾
out=$("$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages 2-3 --output "$WORK/pdf-ex.pdf" 2>/dev/null)
if echo "$out" | grep -q '"pages":\[2,3\]'; then report_pass; else report_fail "pdf extract 区间 [2,3]" "[2,3]" "$out"; fi
ex_count=$("$NEW_CLI" pdf info "$WORK/pdf-ex.pdf" 2>/dev/null | grep -o '"page_count":[0-9]*' | grep -o '[0-9]*')
assert_equal "pdf extract 产物实际页数（区间2页）" "2" "$ex_count"
out=$("$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages 4- --output "$WORK/pdf-ex2.pdf" 2>/dev/null)
if echo "$out" | grep -q '"pages":\[4,5\]'; then report_pass; else report_fail "pdf extract 开尾 [4,5]" "[4,5]" "$out"; fi
out=$("$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages -2 --output "$WORK/pdf-ex3.pdf" 2>/dev/null)
if echo "$out" | grep -q '"pages":\[1,2\]'; then report_pass; else report_fail "pdf extract 前开 [1,2]" "[1,2]" "$out"; fi
out=$("$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages "1,3,5" --output "$WORK/pdf-ex4.pdf" 2>/dev/null)
if echo "$out" | grep -q '"pages":\[1,3,5\]'; then report_pass; else report_fail "pdf extract 散列 [1,3,5]" "[1,3,5]" "$out"; fi
ex4_count=$("$NEW_CLI" pdf info "$WORK/pdf-ex4.pdf" 2>/dev/null | grep -o '"page_count":[0-9]*' | grep -o '[0-9]*')
assert_equal "pdf extract 产物实际页数（散列3页）" "3" "$ex4_count"
# extract 缺省输出名（同目录 <名>-extract.pdf）
rm -f "$WORK/pdf-c-extract.pdf"
"$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages 1 >/dev/null 2>&1
if [ -s "$WORK/pdf-c-extract.pdf" ]; then report_pass; else report_fail "pdf extract 缺省输出名" "pdf-c-extract.pdf" "未产出"; fi
# 宽尾钳制：4-10 → 截至末页 [4,5] + stderr 提示（同视频剪辑语义）
clamp_out=$("$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages 4-10 --output "$WORK/pdf-clamp.pdf" 2>"$WORK/clamp.err"); ec=$?
assert_equal "pdf 宽尾提取 exit" 0 "$ec"
if echo "$clamp_out" | grep -q '"pages":\[4,5\]'; then report_pass; else report_fail "pdf 宽尾钳制 [4,5]" "[4,5]" "$clamp_out"; fi
if grep -q "已截至第 5 页" "$WORK/clamp.err"; then report_pass; else report_fail "pdf 宽尾提示" "已截至第 5 页" "$(cat "$WORK/clamp.err")"; fi
# ── pdf resize 统一页面尺寸（图片型 PDF 各页不一；混合夹具=每页不同 MediaBox）──
make_pdf_mixed() { # 混合尺寸 3 页：300x400 / 500x300(横) / 200x900
python3 -I - "$1" <<'PYX'
import sys
path = sys.argv[1]
dims = [(300,400),(500,300),(200,900)]
objs = {1: b"<< /Type /Catalog /Pages 2 0 R >>"}
kids = " ".join(f"{3+2*i} 0 R" for i in range(len(dims)))
objs[2] = f"<< /Type /Pages /Kids [{kids}] /Count {len(dims)} >>".encode()
for i,(w,h) in enumerate(dims):
    n = 3+2*i
    content = f"0.0 0.0 {w-20} {h-20} re f".encode()
    objs[n] = f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 {w} {h}] /Contents {n+1} 0 R >>".encode()
    objs[n+1] = b"<< /Length %d >>\nstream\n" % len(content) + content + b"\nendstream"
out = bytearray(b"%PDF-1.4\n"); offsets = {}
for n in sorted(objs):
    offsets[n] = len(out); out += f"{n} 0 obj\n".encode() + objs[n] + b"\nendobj\n"
xref = len(out); mx = max(objs)
out += f"xref\n0 {mx+1}\n".encode() + b"0000000000 65535 f \n"
for n in range(1, mx+1): out += f"{offsets[n]:010d} 00000 n \n".encode()
out += f"trailer\n<< /Size {mx+1} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF".encode()
open(path,"wb").write(out)
PYX
}
make_pdf_mixed "$WORK/pdf-mixed.pdf"
# --all-pages 体检：三页尺寸如实
out=$("$NEW_CLI" pdf info "$WORK/pdf-mixed.pdf" --all-pages 2>/dev/null)
if echo "$out" | grep -q '"width_pt":500'; then report_pass; else report_fail "混合尺寸体检" "500 宽页可见" "$out"; fi
if echo "$out" | grep -q '"pages":\['; then report_pass; else report_fail "--all-pages pages 数组" "存在" "$out"; fi
# 统一到 a4：竖页 A4P / 横页 best-fit A4L
"$NEW_CLI" pdf resize "$WORK/pdf-mixed.pdf" --size a4 --output "$WORK/pdf-unified.pdf" >/dev/null 2>&1
out=$("$NEW_CLI" pdf info "$WORK/pdf-unified.pdf" --all-pages 2>/dev/null)
a4p_count=$(echo "$out" | python3 -c "import json,sys; print(sum(1 for p in json.load(sys.stdin)['data']['pages'] if p['width_pt']==595))")
a4l_count=$(echo "$out" | python3 -c "import json,sys; print(sum(1 for p in json.load(sys.stdin)['data']['pages'] if p['width_pt']==842))")
assert_equal "a4 统一竖页数" "2" "$a4p_count"
assert_equal "a4 统一横页数（best-fit）" "1" "$a4l_count"
# a4p 强制全竖
"$NEW_CLI" pdf resize "$WORK/pdf-mixed.pdf" --size a4p --output "$WORK/pdf-portrait.pdf" >/dev/null 2>&1
out=$("$NEW_CLI" pdf info "$WORK/pdf-portrait.pdf" --all-pages 2>/dev/null)
all_p=$(echo "$out" | python3 -c "import json,sys; print(sum(1 for p in json.load(sys.stdin)['data']['pages'] if p['width_pt']==595))")
assert_equal "a4p 强制全竖" "3" "$all_p"
# 自定义 210x297mm
"$NEW_CLI" pdf resize "$WORK/pdf-mixed.pdf" --size 210x297mm --output "$WORK/pdf-mm.pdf" >/dev/null 2>&1
if "$NEW_CLI" pdf info "$WORK/pdf-mm.pdf" 2>/dev/null | grep -q '"width_pt":595.27'; then report_pass; else report_fail "自定义 mm 尺寸" "595.27" "$out"; fi
# 退化
"$NEW_CLI" pdf resize "$WORK/pdf-mixed.pdf" --size a99 --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "resize 未知纸型 exit" 1 "$?"
"$NEW_CLI" pdf resize "$WORK/pdf-mixed.pdf" --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "resize 缺 --size exit" 1 "$?"
"$NEW_CLI" pdf resize "$WORK/pdf-mixed.pdf" --size 0x0 --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "resize 零尺寸 exit" 1 "$?"

# ── pdf resize 文档型拦截（含字体页 → 默认拒绝；--force 放行；图片型不受影响）──
make_pdf_textdoc() { # 2 页：p1 矩形页 p2 声明 Helvetica 的文档页
python3 -I - "$1" <<'PYX'
import sys
objs = {1: b"<< /Type /Catalog /Pages 2 0 R >>",
        2: b"<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>"}
c1 = b"10 10 200 100 re f"
objs[3] = b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 400] /Contents 4 0 R >>"
objs[4] = b"<< /Length %d >>\nstream\n" % len(c1) + c1 + b"\nendstream"
c2 = b"10 10 200 100 re f"
objs[5] = (b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 400] /Contents 6 0 R "
           b"/Resources << /Font << /F1 7 0 R >> >> >>")
objs[6] = b"<< /Length %d >>\nstream\n" % len(c2) + c2 + b"\nendstream"
objs[7] = b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
out = bytearray(b"%PDF-1.4\n"); offsets = {}
for n in sorted(objs):
    offsets[n] = len(out); out += f"{n} 0 obj\n".encode() + objs[n] + b"\nendobj\n"
xref = len(out); mx = max(objs)
out += f"xref\n0 {mx+1}\n".encode() + b"0000000000 65535 f \n"
for n in range(1, mx+1): out += f"{offsets[n]:010d} 00000 n \n".encode()
out += f"trailer\n<< /Size {mx+1} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF".encode()
open(sys.argv[1], "wb").write(out)
PYX
}
make_pdf_textdoc "$WORK/pdf-text.pdf"
# info 体检字段
if "$NEW_CLI" pdf info "$WORK/pdf-text.pdf" 2>/dev/null | grep -q '"text_based":true'; then report_pass; else report_fail "文档型体检字段" "text_based" "缺失"; fi
out=$("$NEW_CLI" pdf info "$WORK/pdf-text.pdf" --all-pages 2>/dev/null)
if echo "$out" | grep -q '"page":2,"width_pt":300,"height_pt":400,"text":true'; then report_pass; else report_fail "逐页 text 标注" "第2页 text" "$out"; fi
# 默认拦截 + 文案（点名具体页码——混合型可定位）
out=$("$NEW_CLI" pdf resize "$WORK/pdf-text.pdf" --size a4 --output "$WORK/x.pdf" 2>/dev/null); ec=$?
assert_equal "文档型 resize 拦截 exit" 1 "$ec"
if echo "$out" | grep -q "第 2 页含文字"; then report_pass; else report_fail "拦截文案点名页码" "第 2 页" "$out"; fi
# 混合型：--pages 圈住图片页（第 1 页）→ 放行（不连坐）
"$NEW_CLI" pdf resize "$WORK/pdf-text.pdf" --pages 1 --size a4 --output "$WORK/p1-only.pdf" >/dev/null 2>&1
assert_equal "混合型圈图片页放行 exit" 0 "$?"
if "$NEW_CLI" pdf info "$WORK/p1-only.pdf" 2>/dev/null | grep -q '"width_pt":595'; then report_pass; else report_fail "圈图片页产物" "595" "错"; fi
# 圈中文字页 → 拦截
"$NEW_CLI" pdf resize "$WORK/pdf-text.pdf" --pages 2 --size a4 --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "圈中文字页拦截 exit" 1 "$?"
# --force 放行
"$NEW_CLI" pdf resize "$WORK/pdf-text.pdf" --size a4 --force --output "$WORK/text-forced.pdf" >/dev/null 2>&1; assert_equal "--force 放行 exit" 0 "$?"
if "$NEW_CLI" pdf info "$WORK/text-forced.pdf" 2>/dev/null | grep -q '"width_pt":595'; then report_pass; else report_fail "force 产物尺寸" "595" "错"; fi
# 图片型（矩形页）不受影响
"$NEW_CLI" pdf resize "$WORK/pdf-mixed.pdf" --size a4 --output "$WORK/m2.pdf" >/dev/null 2>&1; assert_equal "图片型不受拦截 exit" 0 "$?"

# ── pdf 旋转（/Rotate 元数据零损；方向经 info.rotation 断言）──
"$NEW_CLI" pdf rotate-right "$WORK/pdf-c.pdf" --output "$WORK/rot-r.pdf" >/dev/null 2>&1
rot=$("$NEW_CLI" pdf info "$WORK/rot-r.pdf" 2>/dev/null | grep -o '"rotation":[0-9]*' | grep -o '[0-9]*')
assert_equal "pdf rotate-right =90°(顺时针)" "90" "$rot"
"$NEW_CLI" pdf rotate-left "$WORK/pdf-c.pdf" --output "$WORK/rot-l.pdf" >/dev/null 2>&1
rot=$("$NEW_CLI" pdf info "$WORK/rot-l.pdf" 2>/dev/null | grep -o '"rotation":[0-9]*' | grep -o '[0-9]*')
assert_equal "pdf rotate-left =270°(逆时针)" "270" "$rot"
"$NEW_CLI" pdf rotate-180 "$WORK/pdf-c.pdf" --output "$WORK/rot-180.pdf" >/dev/null 2>&1
rot=$("$NEW_CLI" pdf info "$WORK/rot-180.pdf" 2>/dev/null | grep -o '"rotation":[0-9]*' | grep -o '[0-9]*')
assert_equal "pdf rotate-180 =180°" "180" "$rot"
# 尺寸反映旋转（300x400 → 观看态 400x300）
dim=$("$NEW_CLI" pdf info "$WORK/rot-r.pdf" 2>/dev/null | grep -o '"width_pt":[0-9]*' | grep -o '[0-9]*')
assert_equal "pdf 旋转后观看态宽" "400" "$dim"
# 叠加语义：两次 right=180
"$NEW_CLI" pdf rotate-right "$WORK/rot-r.pdf" --output "$WORK/rot-rr.pdf" >/dev/null 2>&1
rot=$("$NEW_CLI" pdf info "$WORK/rot-rr.pdf" 2>/dev/null | grep -o '"rotation":[0-9]*' | grep -o '[0-9]*')
assert_equal "pdf 旋转叠加（right×2=180）" "180" "$rot"
# 选页旋转：只转第 2 页（首页不受影响）
out=$("$NEW_CLI" pdf rotate-left "$WORK/pdf-c.pdf" --pages 2 --output "$WORK/rot-p2.pdf" 2>/dev/null)
if echo "$out" | grep -q '"pages":\[2\]'; then report_pass; else report_fail "pdf 选页旋转 [2]" "[2]" "$out"; fi
if "$NEW_CLI" pdf info "$WORK/rot-p2.pdf" 2>/dev/null | grep -q '"rotation"'; then report_fail "pdf 选页不染首页" "无 rotation 字段" "首页被转"; else report_pass; fi
# 缺省输出名
rm -f "$WORK/pdf-c-rotated.pdf"
"$NEW_CLI" pdf rotate-right "$WORK/pdf-c.pdf" >/dev/null 2>&1
if [ -s "$WORK/pdf-c-rotated.pdf" ]; then report_pass; else report_fail "pdf rotate 缺省输出名" "pdf-c-rotated.pdf" "未产出"; fi
"$NEW_CLI" pdf rotate-right "$WORK/pdf-c.pdf" --pages 9 --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "pdf rotate 页码越界 exit" 1 "$?"
"$NEW_CLI" pdf rotate-sideways "$WORK/pdf-c.pdf" >/dev/null 2>&1; assert_equal "pdf 非法方向子命令 exit" 1 "$?"
# 退化
"$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages 9 --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "pdf extract 越界 exit" 1 "$?"
"$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages 6-9 --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "pdf extract 起始越界 exit" 1 "$?"
"$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --pages abc --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "pdf extract 非法页码 exit" 1 "$?"
"$NEW_CLI" pdf extract "$WORK/pdf-c.pdf" --output "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "pdf extract 缺 --pages exit" 1 "$?"
"$NEW_CLI" pdf merge "$WORK/pdf-a.pdf" "$WORK/missing.pdf" "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "pdf merge 输入缺失 exit" 1 "$?"
"$NEW_CLI" pdf merge "$WORK/pdf-a.pdf" "$WORK/only-one.pdf" >/dev/null 2>&1; assert_equal "pdf merge 单输入 exit" 1 "$?"
"$NEW_CLI" pdf info /nonexistent.pdf >/dev/null 2>&1; assert_equal "pdf info 不存在 exit" 1 "$?"
"$NEW_CLI" pdf --help >/dev/null 2>&1; assert_equal "pdf --help exit" 0 "$?"

# ── json 命令族（零依赖消费 zpack 结果——替代 python3 -c/jq）──
envelope='{"is_success":true,"data":{"archive":"/a/b.zip","file_count":3,"entries":[{"name":"a.txt"},{"name":"b.txt"}]}}'
echo "$envelope" | "$NEW_CLI" json ok; assert_equal "json ok 成功信封 exit" 0 "$?"
echo '{"is_success":false,"error_info":"x"}' | "$NEW_CLI" json ok >/dev/null 2>&1; assert_equal "json ok 失败信封 exit" 1 "$?"
out=$(echo "$envelope" | "$NEW_CLI" json get data.archive 2>/dev/null); ec=$?
assert_equal "json get 字符串字段 exit" 0 "$ec"
assert_equal "json get archive 值" "/a/b.zip" "$out"
assert_equal "json get 数字字段" "3" "$(echo "$envelope" | "$NEW_CLI" json get data.file_count 2>/dev/null)"
assert_equal "json get 布尔字段" "true" "$(echo "$envelope" | "$NEW_CLI" json get is_success 2>/dev/null)"
assert_equal "json get 数组下标" "b.txt" "$(echo "$envelope" | "$NEW_CLI" json get data.entries.1.name 2>/dev/null)"
assert_equal "json get 复合值紧凑 JSON" '{"name":"a.txt"}' "$(echo "$envelope" | "$NEW_CLI" json get data.entries.0 2>/dev/null)"
echo "$envelope" | "$NEW_CLI" json get data.nonexistent >/dev/null 2>&1; assert_equal "json get 字段不存在 exit" 1 "$?"
echo "$envelope" | "$NEW_CLI" json get data.entries.9.name >/dev/null 2>&1; assert_equal "json get 下标越界 exit" 1 "$?"
echo 'garbage' | "$NEW_CLI" json ok >/dev/null 2>&1; assert_equal "json 非法输入 exit" 1 "$?"
"$NEW_CLI" json get >/dev/null 2>&1; assert_equal "json get 缺路径 exit" 1 "$?"
"$NEW_CLI" json --help >/dev/null 2>&1; assert_equal "json --help exit" 0 "$?"

# ---------- 汇总 ----------
echo
echo "验收结果：通过 $PASS_COUNT / 失败 $FAIL_COUNT"
[ "$FAIL_COUNT" -eq 0 ]
