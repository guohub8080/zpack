#!/usr/bin/env bash
# zpack 图片功能验收门禁：只测 bin/zpack（纯 Go 即可——image 零 cgo）。
set -u
NEW_CLI="$(cd "$(dirname "$0")/.." && pwd)/bin/zpack"
PASS_COUNT=0; FAIL_COUNT=0
report_pass() { PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() { FAIL_COUNT=$((FAIL_COUNT + 1)); echo "✗ $1"; echo "    期望: [$2]"; echo "    实际: [$3]"; }
assert_equal() { if [ "$2" = "$3" ]; then report_pass; else report_fail "$1" "$2" "$3"; fi; }

IMG_WORK="/tmp/zpack-verify-image-$$"
mkdir -p "$IMG_WORK"
ffmpeg -y -v error -f lavfi -i "testsrc2=size=640x480:duration=1" -frames:v 1 "$IMG_WORK/src.png" 2>/dev/null
ffmpeg -y -v error -i "$IMG_WORK/src.png" -frames:v 1 "$IMG_WORK/src.bmp" 2>/dev/null
ffmpeg -y -v error -i "$IMG_WORK/src.png" -frames:v 1 "$IMG_WORK/src.tiff" 2>/dev/null

# ── convert ──
for fmt in jpg png webp bmp tiff; do
  out=$("$NEW_CLI" image convert "$IMG_WORK/src.png" "$IMG_WORK/out.$fmt" 2>/dev/null); ec=$?
  assert_equal "convert png→$fmt exit" 0 "$ec"
  if [ -s "$IMG_WORK/out.$fmt" ]; then report_pass; else report_fail "convert png→$fmt 产出" "非空" "空"; fi
done
# bmp→png（921KB→33KB 压缩显著）
out=$("$NEW_CLI" image convert "$IMG_WORK/src.bmp" "$IMG_WORK/bmp-out.png" 2>/dev/null)
bmp_size=$(stat -f%z "$IMG_WORK/bmp-out.png")
if [ "$bmp_size" -lt 100000 ]; then report_pass; else report_fail "bmp→png 压缩" "<100KB" "$bmp_size"; fi
# webp 输出确实更小
webp_size=$(stat -f%z "$IMG_WORK/out.webp")
png_size=$(stat -f%z "$IMG_WORK/out.png")
if [ "$webp_size" -lt "$png_size" ]; then report_pass; else report_fail "webp<png" "更小" "$webp_size vs $png_size"; fi

# ── compress ──
out=$("$NEW_CLI" image compress "$IMG_WORK/out.jpg" --output "$IMG_WORK/compressed.jpg" --quality 50 2>/dev/null); ec=$?
assert_equal "compress jpg exit" 0 "$ec"
comp_size=$(stat -f%z "$IMG_WORK/compressed.jpg")
orig_size=$(stat -f%z "$IMG_WORK/out.jpg")
if [ "$comp_size" -lt "$orig_size" ]; then report_pass; else report_fail "compress 压缩" "<$orig_size" "$comp_size"; fi

# ── info ──
out=$("$NEW_CLI" image info "$IMG_WORK/src.png" 2>/dev/null); ec=$?
assert_equal "info exit" 0 "$ec"
for field in '"format":"png"' '"width":640' '"height":480'; do
  if echo "$out" | grep -q "$field"; then report_pass; else report_fail "info $field" "含" "$out"; fi
done

# ── resize ──
ffmpeg -y -v error -f lavfi -i "testsrc2=size=1920x1080:duration=1" -frames:v 1 "$IMG_WORK/big.png" 2>/dev/null
out=$("$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/rs-w.jpg" --width 640 2>/dev/null); ec=$?
assert_equal "resize --width exit" 0 "$ec"
rs_dims=$("$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/rs-w2.jpg" --width 640 2>/dev/null | grep -o '"width":[0-9]*' | head -1 | grep -o '[0-9]*')
assert_equal "resize --width 640" "640" "$rs_dims"
out=$("$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/rs-50.jpg" --percent 50 2>/dev/null)
if echo "$out" | grep -q '"width":960'; then report_pass; else report_fail "resize --percent 50" "960" "$out"; fi
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/rs-max.jpg" --max 800x600 >/dev/null 2>&1
rs_max=$("$NEW_CLI" image info "$IMG_WORK/rs-max.jpg" 2>/dev/null | grep -o '"width":[0-9]*' | head -1 | grep -o '[0-9]*')
assert_equal "resize --max 800x600" "800" "$rs_max"
# 缩放+格式转换一步
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/rs.webp" --percent 50 >/dev/null 2>&1
if [ -s "$IMG_WORK/rs.webp" ]; then report_pass; else report_fail "resize png→webp" "非空" "空"; fi
# 不升尺寸
"$NEW_CLI" image resize "$IMG_WORK/src.png" "$IMG_WORK/rs-up.png" --max 1920x1080 >/dev/null 2>&1
rs_up=$("$NEW_CLI" image info "$IMG_WORK/rs-up.png" 2>/dev/null | grep -o '"width":[0-9]*' | head -1 | grep -o '[0-9]*')
assert_equal "resize 不升尺寸" "640" "$rs_up"
# 退化
"$NEW_CLI" image resize "$IMG_WORK/src.png" "$IMG_WORK/x.jpg" >/dev/null 2>&1; assert_equal "resize 无方式 exit" 1 "$?"

# ── 物理尺寸缩放（cm/inch + DPI）──
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/phys-55.jpg" --cm-w 5.5 --ppi 300 >/dev/null 2>&1
phys_w=$("$NEW_CLI" image info "$IMG_WORK/phys-55.jpg" 2>/dev/null | grep -o '"width":[0-9]*' | head -1 | grep -o '[0-9]*')
assert_equal "--cm-w 5.5 @300PPI = 650px" "650" "$phys_w"
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/passport.jpg" --cm 3.5x4.5 --ppi 300 >/dev/null 2>&1
pp_w=$("$NEW_CLI" image info "$IMG_WORK/passport.jpg" 2>/dev/null | grep -o '"width":[0-9]*' | head -1 | grep -o '[0-9]*')
pp_h=$("$NEW_CLI" image info "$IMG_WORK/passport.jpg" 2>/dev/null | grep -o '"height":[0-9]*' | head -1 | grep -o '[0-9]*')
assert_equal "护照宽 3.5cm=413px" "413" "$pp_w"
assert_equal "护照高 4.5cm=531px" "531" "$pp_h"
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/hires.jpg" --cm-w 3.5 --ppi 600 >/dev/null 2>&1
hr_w=$("$NEW_CLI" image info "$IMG_WORK/hires.jpg" 2>/dev/null | grep -o '"width":[0-9]*' | head -1 | grep -o '[0-9]*')
assert_equal "--cm-w 3.5 @600PPI = 827px" "827" "$hr_w"
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/inch.jpg" --inch-w 2 --dpi 72 >/dev/null 2>&1
in_w=$("$NEW_CLI" image info "$IMG_WORK/inch.jpg" 2>/dev/null | grep -o '"width":[0-9]*' | head -1 | grep -o '[0-9]*')
assert_equal "--inch-w 2 @72DPI = 144px" "144" "$in_w"
# 只定高：另一侧按比例（big.png 1920x1080 → 高 177px 时宽 315px）
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/phys-h.jpg" --mm-h 45 --dpi 100 >/dev/null 2>&1
phys_h=$(ffprobe -v error -show_entries stream=height -of default=noprint_wrappers=1:nokey=1 "$IMG_WORK/phys-h.jpg" 2>/dev/null | head -1)
assert_equal "--mm-h 45 @100DPI 高=177px" "177" "$phys_h"
# mm 双数值
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/phys-mm.jpg" --mm 35x45 --dpi 100 >/dev/null 2>&1
mm_dim=$(ffprobe -v error -show_entries stream=width,height -of csv=p=0 "$IMG_WORK/phys-mm.jpg" 2>/dev/null | head -1)
assert_equal "--mm 35x45 @100DPI = 138x177" "138,177" "$mm_dim"
# -w/-h 组合 = 双尺寸
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/phys-both.jpg" --mm-w 55 --mm-h 45 --dpi 100 >/dev/null 2>&1
both_dim=$(ffprobe -v error -show_entries stream=width,height -of csv=p=0 "$IMG_WORK/phys-both.jpg" 2>/dev/null | head -1)
assert_equal "--mm-w 55 --mm-h 45 组合 = 217x177" "217,177" "$both_dim"
# --ppi 正名与 --dpi 别名等价
"$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/ppi-alias.jpg" --mm-w 55 --ppi 100 >/dev/null 2>&1
alias_w=$(ffprobe -v error -show_entries stream=width -of default=noprint_wrappers=1:nokey=1 "$IMG_WORK/ppi-alias.jpg" 2>/dev/null | head -1)
assert_equal "--mm-w 55 @100PPI = 217px" "217" "$alias_w"
# 单值拒绝（显式引导）
out=$("$NEW_CLI" image resize "$IMG_WORK/big.png" "$IMG_WORK/phys-x.jpg" --mm 55 --dpi 100 2>/dev/null); ec=$?
assert_equal "--mm 单值 exit" 1 "$ec"
if echo "$out" | grep -q '\-mm-w'; then report_pass; else report_fail "--mm 单值引导文案" "指向 --mm-w" "$out"; fi

# ── 几何变换（与 media 家族同名同义；ffmpeg 像素级交叉验证）──
img_xval() { # 参数：zpack子命令 / ffmpeg 等效滤镜
  "$NEW_CLI" image "$1" "$IMG_WORK/src.png" "$IMG_WORK/tf-$1.png" >/dev/null 2>&1
  local mine ref
  mine=$(ffmpeg -v error -i "$IMG_WORK/tf-$1.png" -f rawvideo -pix_fmt rgba - 2>/dev/null | md5 -q)
  ref=$(ffmpeg -v error -i "$IMG_WORK/src.png" -vf "$2" -f rawvideo -pix_fmt rgba - 2>/dev/null | md5 -q)
  assert_equal "变换 $1 像素级=ffmpeg $2" "$ref" "$mine"
}
img_xval rotate-left "transpose=2"
img_xval rotate-right "transpose=1"
img_xval rotate-180 "transpose=1,transpose=1"
img_xval mirror "hflip"
img_xval flip "vflip"
# ±90° 宽高对调断言
tf_dim=$(ffprobe -v error -show_entries stream=width,height -of csv=p=0 "$IMG_WORK/tf-rotate-left.png" 2>/dev/null | head -1)
assert_equal "rotate-left 宽高对调（640x480→480x640）" "480,640" "$tf_dim"
# jpg 输出 + 质量
"$NEW_CLI" image mirror "$IMG_WORK/src.png" "$IMG_WORK/tf-m.jpg" --quality 90 >/dev/null 2>&1
if [ -s "$IMG_WORK/tf-m.jpg" ]; then report_pass; else report_fail "mirror jpg 输出" "非空" "空"; fi
# 退化
"$NEW_CLI" image rotate-left /nonexistent.png "$IMG_WORK/x.png" >/dev/null 2>&1; assert_equal "变换 输入不存在 exit" 1 "$?"
"$NEW_CLI" image flip "$IMG_WORK/src.png" >/dev/null 2>&1; assert_equal "变换 缺参数 exit" 1 "$?"

# ── merge 拼接（长图；基准对齐轴，其余等比缩放）──
out=$("$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" "$IMG_WORK/merge-v.png" 2>/dev/null); ec=$?
assert_equal "merge 纵向 exit" 0 "$ec"
if echo "$out" | grep -q '"direction":"vertical"'; then report_pass; else report_fail "merge 方向字段" "vertical" "$out"; fi
# 尺寸：基准=首图 1920 宽 → src 800x600 缩到 1920x1440；总高 1080+1440=2520
mv_dim=$("$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" "$IMG_WORK/merge-v2.png" 2>/dev/null | grep -o '"height":[0-9]*' | grep -o '[0-9]*')
assert_equal "merge 纵向总高（1080+1440）" "2520" "$mv_dim"
mv_w=$("$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" "$IMG_WORK/merge-v3.png" 2>/dev/null | grep -o '"width":[0-9]*' | grep -o '[0-9]*')
assert_equal "merge 纵向宽=基准宽" "1920" "$mv_w"
# --reference 换基准：基准=src 640 宽 → big 缩到 640x360；总高 360+480=840
mr_dim=$("$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" --reference "$IMG_WORK/src.png" "$IMG_WORK/merge-r.png" 2>/dev/null | grep -o '"height":[0-9]*' | grep -o '[0-9]*')
assert_equal "merge --reference 总高（360+480）" "840" "$mr_dim"
# 横向：基准高 1080 → src 800x600 缩到 1440x1080；总宽 1920+1440=3360
mh_dim=$("$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" "$IMG_WORK/merge-h.png" --direction horizontal 2>/dev/null | grep -o '"width":[0-9]*' | grep -o '[0-9]*')
assert_equal "merge 横向总宽（1920+1440）" "3360" "$mh_dim"
# jpg 输出
"$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" "$IMG_WORK/merge.jpg" >/dev/null 2>&1
if [ -s "$IMG_WORK/merge.jpg" ]; then report_pass; else report_fail "merge jpg 输出" "非空" "空"; fi
# merge 退化
"$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/x.png" >/dev/null 2>&1; assert_equal "merge 单图 exit" 1 "$?"
"$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" "$IMG_WORK/y.png" --reference /nonexistent.png >/dev/null 2>&1; assert_equal "merge 基准不在列表 exit" 1 "$?"
"$NEW_CLI" image merge "$IMG_WORK/big.png" "$IMG_WORK/src.png" "$IMG_WORK/y.png" --direction diagonal >/dev/null 2>&1; assert_equal "merge 非法方向 exit" 1 "$?"

# ── EXIF Orientation（图片界的可烤旋转；读取即定向）──
python3 -I - <<PYEOF2
import struct
w, h = 400, 300
quadrants = [(255,0,0),(0,255,0),(0,0,255),(255,255,0)]
rows = []
for y in range(h):
    row = bytearray()
    for x in range(w):
        q = (0 if y < h//2 else 2) + (0 if x < w//2 else 1)
        row += bytes(quadrants[q])
    rows.append(bytes(row))
open('$IMG_WORK/quad.raw','wb').write(b''.join(rows))
tiff = b'MM\x00*' + struct.pack('>I',8) + struct.pack('>H',1)
tiff += struct.pack('>HHI',0x0112,3,1) + struct.pack('>HH',6,0) + struct.pack('>I',0)
app1 = b'Exif\x00\x00' + tiff
open('$IMG_WORK/app1.bin','wb').write(b'\xff\xe1' + struct.pack('>H',len(app1)+2) + app1)
PYEOF2
ffmpeg -y -v error -f rawvideo -pix_fmt rgb24 -s 400x300 -i "$IMG_WORK/quad.raw" -q:v 1 "$IMG_WORK/quad-plain.jpg" 2>/dev/null
cat "$IMG_WORK/quad-plain.jpg" | head -c 2 > "$IMG_WORK/quad-o6.jpg"
cat "$IMG_WORK/app1.bin" >> "$IMG_WORK/quad-o6.jpg"
tail -c +3 "$IMG_WORK/quad-plain.jpg" >> "$IMG_WORK/quad-o6.jpg"
# info：orientation 6 + 观看态尺寸对调
out=$("$NEW_CLI" image info "$IMG_WORK/quad-o6.jpg" 2>/dev/null)
if echo "$out" | grep -q '"orientation":6'; then report_pass; else report_fail "info orientation 字段" "6" "$out"; fi
if echo "$out" | grep -q '"display_width":300,"display_height":400'; then report_pass; else report_fail "info 观看态尺寸" "300x400" "$out"; fi
# info：无标签图不出现 orientation 字段（omitzero）
out_plain=$("$NEW_CLI" image info "$IMG_WORK/quad-plain.jpg" 2>/dev/null)
if echo "$out_plain" | grep -qv '"orientation"'; then report_pass; else report_fail "info 无标签零字段" "不出现" "$out_plain"; fi
# 处理路径读取即定向：convert 产出=观看态尺寸
"$NEW_CLI" image convert "$IMG_WORK/quad-o6.jpg" "$IMG_WORK/quad-view.png" >/dev/null 2>&1
view_dim=$(ffprobe -v error -show_entries stream=width,height -of csv=p=0 "$IMG_WORK/quad-view.png" 2>/dev/null | head -1)
assert_equal "convert 定向产出（观看态 300x400）" "300,400" "$view_dim"
# 方向验证：四象限与 ffmpeg autorotate 解码一致（容差 ±4——JPEG 解码器间舍入）
qx() { ffmpeg -v error -i "$1" -vf "crop=1:1:$2:$3" -f rawvideo -pix_fmt rgb24 - 2>/dev/null | od -An -tu1 | tr -s ' ' | sed 's/^ //'; }
quad_ok=1
for probe in "100 75" "200 75" "100 325" "200 325"; do
  set -- $probe
  mine=$(qx "$IMG_WORK/quad-view.png" "$1" "$2")
  ref=$(qx "$IMG_WORK/quad-o6.jpg" "$1" "$2")
  for chan in 1 2 3; do
    mv=$(echo "$mine" | cut -d' ' -f$chan); rv=$(echo "$ref" | cut -d' ' -f$chan)
    diff=$((mv - rv)); [ "$diff" -lt 0 ] && diff=$((-diff))
    [ "$diff" -gt 4 ] && quad_ok=0
  done
done
if [ "$quad_ok" = "1" ]; then report_pass; else report_fail "定向方向四象限=ffmpeg autorotate" "逐通道差≤4" "有通道超差"; fi

# ── compress --target-size（质量二分到 ≤ 目标的最高质量；仅 jpg——webp 纯 Go 无损）──
ffmpeg -y -v error -f lavfi -i "mandelbrot=size=800x600:rate=1" -frames:v 1 -q:v 1 "$IMG_WORK/tgt-src.jpg" 2>/dev/null
tgt_src_size=$(stat -f%z "$IMG_WORK/tgt-src.jpg")
tgt_bytes=$((tgt_src_size * 30 / 100))
out=$("$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size "$tgt_bytes" --output "$IMG_WORK/tgt-out.jpg" 2>/dev/null); ec=$?
assert_equal "target-size 压缩 exit" 0 "$ec"
tgt_out_size=$(stat -f%z "$IMG_WORK/tgt-out.jpg")
if [ "$tgt_out_size" -le "$tgt_bytes" ]; then report_pass; else report_fail "target-size 体积达标" "≤$tgt_bytes" "$tgt_out_size"; fi
if echo "$out" | grep -q '"quality":'; then report_pass; else report_fail "target-size 质量字段" "quality=N" "$out"; fi
# 缺省 = size 优先（质量钉 90）：strategy/尺寸缩放断言
if echo "$out" | grep -q '"strategy":"size-first"'; then report_pass; else report_fail "target-size 缺省 size 优先" "size-first" "$out"; fi
if echo "$out" | grep -q '"quality":90'; then report_pass; else report_fail "size 优先质量钉 90" "90" "$out"; fi
src_w=$(ffprobe -v error -show_entries stream=width -of default=noprint_wrappers=1:nokey=1 "$IMG_WORK/tgt-src.jpg" 2>/dev/null | head -1)
out_w=$(ffprobe -v error -show_entries stream=width -of default=noprint_wrappers=1:nokey=1 "$IMG_WORK/tgt-out.jpg" 2>/dev/null | head -1)
if [ "$out_w" -lt "$src_w" ]; then report_pass; else report_fail "size 优先尺寸已缩" "<$src_w" "$out_w"; fi
# --priority quality：保像素优先（全尺寸或缩更少）
out2=$("$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size "$tgt_bytes" --priority quality --output "$IMG_WORK/tgt-out-q.jpg" 2>/dev/null)
if echo "$out2" | grep -q '"strategy":"quality-only"\|"strategy":"scale-then-quality"'; then report_pass; else report_fail "quality 优先策略字段" "quality-only|scale-then-quality" "$out2"; fi
q_out_w=$(ffprobe -v error -show_entries stream=width -of default=noprint_wrappers=1:nokey=1 "$IMG_WORK/tgt-out-q.jpg" 2>/dev/null | head -1)
if [ "$q_out_w" -ge "$out_w" ]; then report_pass; else report_fail "quality 优先像素 ≥ size 优先" ">=$out_w" "$q_out_w"; fi
# 已达标直拷
out=$("$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size 100MB --output "$IMG_WORK/tgt-fit.jpg" 2>/dev/null)
if echo "$out" | grep -q '"already_fit":true'; then report_pass; else report_fail "target-size 已达标直拷" "already_fit" "$out"; fi
cmp -s "$IMG_WORK/tgt-src.jpg" "$IMG_WORK/tgt-fit.jpg"; assert_equal "已达标直拷字节一致" 0 "$?"
# 极小目标（1KB）：size 优先兜底链——5% 缩放 + 降质仍可装下 → last-resort 成功 + 降级标记
out_lr=$("$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size 1KB --output "$IMG_WORK/tgt-lr.jpg" 2>/dev/null); ec=$?
if [ "$ec" = "0" ] && echo "$out_lr" | grep -q '"quality_degraded":true'; then report_pass; else report_fail "极小目标 last-resort" "降级成功" "$out_lr"; fi
# 物理不可达（100B——1 像素 JPEG 都 ~300B）明确报错引导
"$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size 100B --output "$IMG_WORK/x.jpg" >/dev/null 2>&1; assert_equal "target-size 不可达 exit" 1 "$?"
# 无质量维度格式引导（png）
"$NEW_CLI" image compress "$IMG_WORK/src.png" --target-size 10KB --output "$IMG_WORK/x.png" >/dev/null 2>&1; assert_equal "target-size png 引导 exit" 1 "$?"
# 与 --quality 互斥
"$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size 50KB --quality 90 --output "$IMG_WORK/x.jpg" >/dev/null 2>&1; assert_equal "target-size 互斥 exit" 1 "$?"
# 非法体积 / 非法优先级
"$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size abc --output "$IMG_WORK/x.jpg" >/dev/null 2>&1; assert_equal "target-size 非法值 exit" 1 "$?"
"$NEW_CLI" image compress "$IMG_WORK/tgt-src.jpg" --target-size 50KB --priority both --output "$IMG_WORK/x.jpg" >/dev/null 2>&1; assert_equal "非法 priority exit" 1 "$?"

# ── 退化 ──
"$NEW_CLI" image convert "$IMG_WORK/src.png" "$IMG_WORK/out.xyz" >/dev/null 2>&1; assert_equal "不支持格式 exit" 1 "$?"
"$NEW_CLI" image convert /nonexistent.png "$IMG_WORK/x.png" >/dev/null 2>&1; assert_equal "不存在 exit" 1 "$?"
"$NEW_CLI" image info /nonexistent.png >/dev/null 2>&1; assert_equal "info 不存在 exit" 1 "$?"
"$NEW_CLI" image compress "$IMG_WORK/src.png" --quality 0 >/dev/null 2>&1; assert_equal "quality 0 exit" 1 "$?"
"$NEW_CLI" image --help >/dev/null 2>&1; assert_equal "--help exit" 0 "$?"

rm -rf "$IMG_WORK"
echo; echo "图片门禁：通过 $PASS_COUNT / 失败 $FAIL_COUNT"
[ "$FAIL_COUNT" -eq 0 ]
