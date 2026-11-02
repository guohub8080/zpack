#!/usr/bin/env bash
# zpack 媒体功能验收门禁：只测 bin/zpack（CGO 完整版）。
# 前置：brew ffmpeg（夹具生成与产物校验）+ bin/zpack 为 CGO 构建（build_cli/mac/build.sh 产物）。
# 与 verify.sh 分离：媒体门禁需要 libav 环境，纯 Go 主门禁不受影响。
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
assert_equal() {
  if [ "$2" = "$3" ]; then report_pass; else report_fail "$1" "$2" "$3"; fi
}

# 环境检查：bin/zpack 必须含媒体引擎
probe_out=$("$NEW_CLI" media convert /dev/null /tmp/x.mp4 2>&1)
if echo "$probe_out" | grep -q "完整版"; then
  echo "⛔ bin/zpack 为纯 Go 构建（无媒体引擎）——请用 build_cli/mac/build.sh 产物运行本门禁"
  exit 1
fi
if ! command -v ffmpeg >/dev/null; then
  echo "⛔ 需要 ffmpeg 生成夹具与校验产物"
  exit 1
fi

WORK="/tmp/zpack-verify-media-$$"
mkdir -p "$WORK"
ffmpeg -y -v error -f lavfi -i "testsrc2=size=1280x720:rate=30:duration=6" -f lavfi -i "sine=frequency=440:duration=6" -c:v libx264 -preset fast -b:v 4M -c:a aac -b:a 128k "$WORK/src.mp4"
ffmpeg -y -v error -f lavfi -i "sine=frequency=440:duration=5" -c:a aac -b:a 256k "$WORK/audio-src.m4a"
SRC_SIZE=$(stat -f%z "$WORK/src.mp4")

# ── convert：remux（mp4→mkv 零重编）──
out=$("$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/remux.mkv" 2>/dev/null); exit_code=$?
assert_equal "remux exit" 0 "$exit_code"
if echo "$out" | grep -q '"mode":"remux"' && echo "$out" | grep -q '"action":"copy"'; then report_pass; else report_fail "remux 模式字段" "mode=remux+copy" "$out"; fi
mkv_video=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of default=noprint_wrappers=1:nokey=1 "$WORK/remux.mkv")
assert_equal "remux 视频流保留 h264" "h264" "$mkv_video"
# remux 体积应与源接近（±5%）
mkv_size=$(stat -f%z "$WORK/remux.mkv")
if [ "$mkv_size" -gt $((SRC_SIZE * 95 / 100)) ]; then report_pass; else report_fail "remux 体积近源" ">$((SRC_SIZE * 95 / 100))" "$mkv_size"; fi

# ── convert：音频容器抽取（mp4→mp3，编码路径）──
out=$("$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/out.mp3" 2>/dev/null); exit_code=$?
assert_equal "mp3 抽取 exit" 0 "$exit_code"
mp3_codec=$(ffprobe -v error -show_entries stream=codec_name -of default=noprint_wrappers=1:nokey=1 "$WORK/out.mp3")
assert_equal "mp3 编码正确" "mp3" "$mp3_codec"
ffmpeg -v error -i "$WORK/out.mp3" -f null - 2>&1 && report_pass || report_fail "mp3 全解码" "无损" "失败"

# ── convert：webm 全编（vp9+opus）──
out=$("$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/out.webm" 2>/dev/null); exit_code=$?
assert_equal "webm 全编 exit" 0 "$exit_code"
if echo "$out" | grep -q '"mode":"full"' && echo "$out" | grep -q 'vp9'; then report_pass; else report_fail "webm 模式字段" "full+vp9" "$out"; fi

# ── compress：目标 40%（目标码率公式）──
out=$("$NEW_CLI" media compress "$WORK/src.mp4" --target-size 40% --output "$WORK/compressed.mp4" 2>/dev/null); exit_code=$?
assert_equal "compress exit" 0 "$exit_code"
comp_size=$(stat -f%z "$WORK/compressed.mp4")
if [ "$comp_size" -gt $((SRC_SIZE * 20 / 100)) ] && [ "$comp_size" -lt $((SRC_SIZE * 60 / 100)) ]; then report_pass; else report_fail "compress 40% 落区间(20-60%)" "区间内" "$comp_size/$SRC_SIZE"; fi
ffmpeg -v error -i "$WORK/compressed.mp4" -f null - 2>&1 && report_pass || report_fail "compress 产物全解码" "无损" "失败"

# ── compress：低于下限被拒 ──
out=$("$NEW_CLI" media compress "$WORK/src.mp4" --target-size 1KB --output "$WORK/too-small.mp4" 2>/dev/null); exit_code=$?
assert_equal "低于下限 exit" 1 "$exit_code"
if echo "$out" | grep -q '下限'; then report_pass; else report_fail "下限报错文案" "含下限" "$out"; fi

# ── compress：纯音频 ──
out=$("$NEW_CLI" media compress "$WORK/audio-src.m4a" --target-size 32KB --output "$WORK/audio.mp3" 2>/dev/null); exit_code=$?
assert_equal "纯音频压缩 exit" 0 "$exit_code"
audio_size=$(stat -f%z "$WORK/audio.mp3")
if [ "$audio_size" -gt 16000 ] && [ "$audio_size" -lt 48000 ]; then report_pass; else report_fail "纯音频 32KB 落区间" "16-48KB" "$audio_size"; fi

# ── 预设 ──
out=$("$NEW_CLI" media preset 2>/dev/null); exit_code=$?
assert_equal "preset 列表 exit" 0 "$exit_code"
if echo "$out" | grep -q '"youtube"' && echo "$out" | grep -q '"discord"'; then report_pass; else report_fail "preset 列表内容" "含 youtube/discord" "$out"; fi
out=$("$NEW_CLI" media preset youtube 2>/dev/null)
if echo "$out" | grep -q '"width":1920'; then report_pass; else report_fail "preset 详情字段" "width=1920" "$out"; fi
"$NEW_CLI" media preset nosuch >/dev/null 2>&1; assert_equal "preset 未知 exit" 1 "$?"

# 预设转换：4K 源 → youtube 档（强制重编 + 降档 1080p + 8Mbps）
ffmpeg -y -v error -f lavfi -i "testsrc2=size=3840x2160:rate=30:duration=3" -c:v libx264 -preset ultrafast -b:v 20M -an "$WORK/src4k.mp4"
out=$("$NEW_CLI" media convert "$WORK/src4k.mp4" "$WORK/yt.mp4" --preset youtube 2>/dev/null); exit_code=$?
assert_equal "preset youtube 转换 exit" 0 "$exit_code"
yt_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/yt.mp4")
assert_equal "youtube 预设降档 1080p" "1920,1080" "$yt_dims"

# 预设压缩：discord 25MB 上限
out=$("$NEW_CLI" media compress "$WORK/src4k.mp4" --preset discord --output "$WORK/dc.mp4" 2>/dev/null); exit_code=$?
assert_equal "preset discord 压缩 exit" 0 "$exit_code"
dc_size=$(stat -f%z "$WORK/dc.mp4")
if [ "$dc_size" -le 25000000 ]; then report_pass; else report_fail "discord ≤25MB" "≤25000000" "$dc_size"; fi
# 已达标 → 直拷贝 + already_fit
ffmpeg -y -v error -f lavfi -i "testsrc2=size=640x360:rate=24:duration=2" -c:v libx264 -preset ultrafast -b:v 500k "$WORK/tiny.mp4"
out=$("$NEW_CLI" media compress "$WORK/tiny.mp4" --preset discord --output "$WORK/fit.mp4" 2>/dev/null)
if echo "$out" | grep -q '"already_fit":true' && echo "$out" | grep -q '"mode":"remux"'; then report_pass; else report_fail "已达标直拷贝" "already_fit+remux" "$out"; fi

# ── media info：体检 + 建议 ──
out=$("$NEW_CLI" media info "$WORK/src4k.mp4" 2>/dev/null); exit_code=$?
assert_equal "info exit" 0 "$exit_code"
if echo "$out" | grep -q '"codec":"h264"' && echo "$out" | grep -q '"fps":30'; then report_pass; else report_fail "info 事实字段" "codec+fps" "$out"; fi
if echo "$out" | grep -q '"kind":"resolution"' && echo "$out" | grep -q '"kind":"codec-generation"'; then report_pass; else report_fail "info 4K 建议两条" "resolution+codec-generation" "$out"; fi
out=$("$NEW_CLI" media info "$WORK/audio-src.m4a" 2>/dev/null)
if echo "$out" | grep -q '"audio"'; then report_pass; else report_fail "info 纯音频" "audio 字段" "$out"; fi
"$NEW_CLI" media info 不存在.mp4 >/dev/null 2>&1; assert_equal "info 不存在 exit" 1 "$?"
"$NEW_CLI" media info >/dev/null 2>&1; assert_equal "info 缺参数 exit" 1 "$?"

# ── 正交指令：--screen / --codec（suggestions[].flag 的落地闭环）──
out=$("$NEW_CLI" media convert "$WORK/src4k.mp4" "$WORK/screen.mp4" --screen 1080p 2>/dev/null); exit_code=$?
assert_equal "screen 1080p exit" 0 "$exit_code"
sc_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/screen.mp4")
assert_equal "screen 降档 1080p" "1920,1080" "$sc_dims"
out=$("$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/hevc.mp4" --codec hevc 2>/dev/null); exit_code=$?
assert_equal "codec hevc exit" 0 "$exit_code"
hevc_codec=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of default=noprint_wrappers=1:nokey=1 "$WORK/hevc.mp4")
assert_equal "codec 换码 hevc" "hevc" "$hevc_codec"
# 组合：4K → 1080p + hevc
"$NEW_CLI" media compress "$WORK/src4k.mp4" --target-size 8MB --screen 1080p --codec hevc --output "$WORK/combo.mp4" >/dev/null 2>&1
assert_equal "screen+codec 组合 exit" 0 "$?"
combo=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,codec_name -of csv=p=0 "$WORK/combo.mp4" 2>/dev/null)
if echo "$combo" | grep -q "1920" && echo "$combo" | grep -q "hevc"; then report_pass; else report_fail "组合 1080p+hevc" "1920+hevc" "$combo"; fi
# 退化
"$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/y.mp4" --screen 1080i >/dev/null 2>&1; assert_equal "screen 非法 exit" 1 "$?"
"$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/y.mp4" --codec mpeg2 >/dev/null 2>&1; assert_equal "codec 非法 exit" 1 "$?"
out=$("$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/y.webm" --codec h264 2>/dev/null)
if echo "$out" | grep -q '不兼容'; then report_pass; else report_fail "容器不兼容文案" "含不兼容" "$out"; fi
"$NEW_CLI" media convert "$WORK/src.mp4" "$WORK/y.mp4" --screen 720p --preset youtube >/dev/null 2>&1; assert_equal "screen+preset 互斥 exit" 1 "$?"

# ── 旋转烤入（display-matrix 侧数据）+ 字幕保轨 ──
ffmpeg -y -v error -display_rotation 90 -i "$WORK/src.mp4" -c copy "$WORK/rot90.mp4"
out=$("$NEW_CLI" media info "$WORK/rot90.mp4" 2>/dev/null)
if echo "$out" | grep -q '"rotation":'; then report_pass; else report_fail "info 读到侧数据旋转" "rotation 字段" "$out"; fi
# remux：矩阵零损保留（v2：旋转不再否决直拷）
"$NEW_CLI" media convert "$WORK/rot90.mp4" "$WORK/rot-mkv.mkv" >/dev/null 2>&1
rot_mode=$("$NEW_CLI" media convert "$WORK/rot90.mp4" "$WORK/rot-mkv2.mkv" 2>/dev/null | grep -o '"mode":"[a-z-]*"')
assert_equal "旋转 remux 模式" '"mode":"remux"' "$rot_mode"
rot_kept=$(ffprobe -v error -select_streams v:0 -show_entries side_data=rotation -of default=noprint_wrappers=1 "$WORK/rot-mkv2.mkv" 2>/dev/null | head -1)
assert_equal "remux 保 display-matrix" "rotation=90" "$rot_kept"
# 重编：transpose 烤入（宽高对调 + 侧数据消失）
"$NEW_CLI" media convert "$WORK/rot90.mp4" "$WORK/rot-baked.mp4" --codec hevc >/dev/null 2>&1
rot_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/rot-baked.mp4")
assert_equal "烤入后宽高对调" "720,1280" "$rot_dims"
rot_sd=$(ffprobe -v error -select_streams v:0 -show_entries side_data=rotation -of default=noprint_wrappers=1 "$WORK/rot-baked.mp4" 2>/dev/null | wc -l | tr -d ' ')
assert_equal "烤入后侧数据消失" "0" "$rot_sd"
ffmpeg -v error -i "$WORK/rot-baked.mp4" -f null - 2>&1 && report_pass || report_fail "烤入产物全解码" "无损" "失败"
# 字幕：mkv(srt) remux 保轨；→mp4 丢弃+告警
printf '1\n00:00:00,000 --> 00:00:05,000\n字幕测试\n' > "$WORK/sub.srt"
ffmpeg -y -v error -i "$WORK/src.mp4" -i "$WORK/sub.srt" -map 0 -map 1 -c copy -c:s srt "$WORK/subbed.mkv"
"$NEW_CLI" media convert "$WORK/subbed.mkv" "$WORK/subbed-out.mkv" >/dev/null 2>&1
sub_kept=$(ffprobe -v error -select_streams s -show_entries stream=codec_name -of default=noprint_wrappers=1:nokey=1 "$WORK/subbed-out.mkv" 2>/dev/null)
assert_equal "mkv remux 字幕保轨" "subrip" "$sub_kept"
sub_warn=$("$NEW_CLI" media convert "$WORK/subbed.mkv" "$WORK/subbed.mp4" 2>&1 | grep -c "跳过字幕流")
assert_equal "mp4 丢弃字幕告警" "1" "$sub_warn"
sub_gone=$(ffprobe -v error -select_streams s "$WORK/subbed.mp4" 2>&1 | wc -l | tr -d ' ')
assert_equal "mp4 无字幕流" "0" "$sub_gone"

# ── 几何变换命令（rotate-left/right/180 + mirror）──
out=$("$NEW_CLI" media rotate-right "$WORK/src.mp4" "$WORK/rr.mp4" 2>/dev/null); exit_code=$?
assert_equal "rotate-right 零损 exit" 0 "$exit_code"
if echo "$out" | grep -q '"mode":"transform-matrix"'; then report_pass; else report_fail "rotate-right 模式" "transform-matrix" "$out"; fi
rr_rot=$(ffprobe -v error -select_streams v:0 -show_entries side_data=rotation -of default=noprint_wrappers=1 "$WORK/rr.mp4" 2>/dev/null | head -1)
if [ -n "$rr_rot" ]; then report_pass; else report_fail "矩阵已写入" "rotation 有值" "$rr_rot"; fi
# --bake：宽高交换
"$NEW_CLI" media rotate-right "$WORK/src.mp4" "$WORK/rrb.mp4" --bake >/dev/null 2>&1
rrb_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/rrb.mp4")
assert_equal "rotate-right 烤入宽高交换" "720,1280" "$rrb_dims"
# rotate-180 / mirror：尺寸不变
"$NEW_CLI" media rotate-180 "$WORK/src.mp4" "$WORK/r180.mp4" --bake >/dev/null 2>&1
r180_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/r180.mp4")
assert_equal "rotate-180 尺寸不变" "1280,720" "$r180_dims"
"$NEW_CLI" media mirror "$WORK/src.mp4" "$WORK/mir.mp4" >/dev/null 2>&1
mir_mode=$("$NEW_CLI" media mirror "$WORK/src.mp4" "$WORK/mir2.mp4" 2>/dev/null | grep -o '"mode":"[a-z-]*"')
assert_equal "mirror 恒烤入" '"mode":"transform-bake"' "$mir_mode"
# 两路径一致性（PSNR ≥ 20dB = 同向）
ffmpeg -y -v error -i "$WORK/rr.mp4" -c:v libx264 -preset fast -an "$WORK/rr-ref.mp4" 2>/dev/null
psnr=$(ffmpeg -v info -i "$WORK/rrb.mp4" -i "$WORK/rr-ref.mp4" -lavfi psnr -f null - 2>&1 | grep -o "average:[0-9.]*" | head -1 | cut -d: -f2 | cut -d. -f1)
if [ -n "$psnr" ] && [ "$psnr" -ge 20 ]; then report_pass; else report_fail "矩阵≡烤入 PSNR" "≥20" "$psnr"; fi
# 退化：音频容器 / 无视频流 / 缺参
"$NEW_CLI" media mirror "$WORK/src.mp4" "$WORK/y.mp3" >/dev/null 2>&1; assert_equal "变换→音频容器 exit" 1 "$?"
"$NEW_CLI" media rotate-left "$WORK/audio-src.m4a" "$WORK/y.mp4" >/dev/null 2>&1; assert_equal "纯音频变换 exit" 1 "$?"
"$NEW_CLI" media mirror "$WORK/src.mp4" >/dev/null 2>&1; assert_equal "变换缺参数 exit" 1 "$?"

# ── 烤入语义补全：净角度 / convert --bake / info 恒显旋转 ──
# rotate-right --bake 于 -90° 源 = 净 0°（转正，尺寸不变）
"$NEW_CLI" media rotate-right "$WORK/rot90.mp4" "$WORK/net0.mp4" --bake >/dev/null 2>&1
net0_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/net0.mp4")
assert_equal "净角度 0° 转正尺寸不变" "1280,720" "$net0_dims"
# bake 命令：带矩阵源物理化（原 convert --bake 已退休）
"$NEW_CLI" media bake "$WORK/rot90.mp4" "$WORK/cb.mp4" >/dev/null 2>&1
cb_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/cb.mp4")
assert_equal "bake 烤实宽高对调" "720,1280" "$cb_dims"
cb_sd=$(ffprobe -v error -select_streams v:0 -show_entries side_data=rotation -of default=noprint_wrappers=1 "$WORK/cb.mp4" 2>/dev/null | wc -l | tr -d ' ')
assert_equal "烤实后矩阵消失" "0" "$cb_sd"
# info 恒显 rotation + 显示尺寸
info_out=$("$NEW_CLI" media info "$WORK/src.mp4" 2>/dev/null)
if echo "$info_out" | grep -q '"rotation":0'; then report_pass; else report_fail "info 无旋转源恒显 0" "rotation:0" "$info_out"; fi
info_rot=$("$NEW_CLI" media info "$WORK/rot90.mp4" 2>/dev/null)
if echo "$info_rot" | grep -q '"display_width":720' && echo "$info_rot" | grep -q '"display_height":1280'; then report_pass; else report_fail "info 显示尺寸对调" "720x1280" "$info_rot"; fi

# ── 竖拍 × preset 降档回归（旋转审计补丁：applyPresetToParams 曾漏设 rotation）──
ffmpeg -y -v error -display_rotation 90 -i "$WORK/src4k.mp4" -c copy "$WORK/rot4k.mp4"
"$NEW_CLI" media convert "$WORK/rot4k.mp4" "$WORK/rotyt.mp4" --preset youtube >/dev/null 2>&1
rotyt_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$WORK/rotyt.mp4")
assert_equal "竖拍4K+preset 烤实竖屏" "608,1080" "$rotyt_dims"
rotyt_sd=$(ffprobe -v error -select_streams v:0 -show_entries side_data=rotation -of default=noprint_wrappers=1 "$WORK/rotyt.mp4" 2>/dev/null | wc -l | tr -d ' ')
assert_equal "竖拍preset 矩阵已烤实" "0" "$rotyt_sd"

# ── 可烤元数据 #2 SAR：老 DVD 变形宽银幕（720x576 SAR 16:15 → 显示 768x576）──
ffmpeg -y -v error -f lavfi -i "testsrc2=size=720x576:rate=25:duration=3" -vf setsar=16/15 -c:v libx264 -preset ultrafast -an "$WORK/ana.mp4"
ana_info=$("$NEW_CLI" media info "$WORK/ana.mp4" 2>/dev/null)
if echo "$ana_info" | grep -q '"sar_num":16' && echo "$ana_info" | grep -q '"display_width":768'; then report_pass; else report_fail "SAR 探测与显示尺寸" "16:15→768" "$ana_info"; fi
if echo "$ana_info" | grep -q '"kind":"aspect-ratio"'; then report_pass; else report_fail "SAR 建议" "aspect-ratio" "$ana_info"; fi
"$NEW_CLI" media convert "$WORK/ana.mp4" "$WORK/ana-out.mp4" --screen 360p >/dev/null 2>&1
ana_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height,sample_aspect_ratio -of default=noprint_wrappers=1:nokey=1 "$WORK/ana-out.mp4" 2>/dev/null | tr "\n" " " | sed "s/ *$//")
assert_equal "SAR 缩放归一(480x360 1:1)" "480 360 1:1" "$ana_dims"

# ── bake 一级命令：显示元数据物理化 ──
# 双烤夹具（旋转 -90 + SAR 16:15 → 显示 576x768）
ffmpeg -y -v error -f lavfi -i "testsrc2=size=720x576:rate=25:duration=3" -vf "setsar=16/15" -c:v libx264 -preset ultrafast -an "$WORK/dual.mp4"
ffmpeg -y -v error -display_rotation 90 -i "$WORK/dual.mp4" -c copy "$WORK/dual90.mp4"
info_out=$("$NEW_CLI" media info "$WORK/dual90.mp4" 2>/dev/null)
if echo "$info_out" | grep -q '"bakeable":\["rotation","sar"\]'; then report_pass; else report_fail "info bakeable 双项" '["rotation","sar"]' "$info_out"; fi
if echo "$info_out" | grep -q '"kind":"bakeable"'; then report_pass; else report_fail "info bake 建议" "bakeable kind" "$info_out"; fi
out=$("$NEW_CLI" media bake "$WORK/dual90.mp4" "$WORK/baked.mp4" 2>/dev/null); exit_code=$?
assert_equal "bake 双烤 exit" 0 "$exit_code"
if echo "$out" | grep -q '"baked":\["rotation","sar"\]'; then report_pass; else report_fail "baked 字段" '["rotation","sar"]' "$out"; fi
baked_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height,sample_aspect_ratio -of default=noprint_wrappers=1:nokey=1 "$WORK/baked.mp4" 2>/dev/null | tr '\n' ' ' | sed 's/ *$//')
assert_equal "双烤 576x768 1:1" "576 768 1:1" "$baked_dims"
baked_rot=$(ffprobe -v error -select_streams v:0 -show_entries side_data=rotation "$WORK/baked.mp4" 2>/dev/null | grep -c "rotation=")
assert_equal "烤后无矩阵" "0" "$baked_rot"
ffmpeg -v error -i "$WORK/baked.mp4" -f null - 2>&1 && report_pass || report_fail "烤后全解码" "无损" "失败"
# 无可烤幂等：baked=[] 而非缺省
out=$("$NEW_CLI" media bake "$WORK/src.mp4" "$WORK/idem.mp4" 2>/dev/null)
if echo "$out" | grep -q '"baked":\[\]'; then report_pass; else report_fail "无可烤 baked=[]" "空数组" "$out"; fi
# bake 退化
"$NEW_CLI" media bake "$WORK/audio-src.m4a" "$WORK/y.mp4" >/dev/null 2>&1; assert_equal "纯音频 bake exit" 1 "$?"
"$NEW_CLI" media bake "$WORK/src.mp4" >/dev/null 2>&1; assert_equal "bake 缺参数 exit" 1 "$?"

# ── 截图 ──
out=$("$NEW_CLI" media screenshot "$WORK/src.mp4" 2>/dev/null); ec=$?
assert_equal "screenshot 缺省中间帧 exit" 0 "$ec"
if echo "$out" | grep -q '"timestamp_seconds":'; then report_pass; else report_fail "screenshot 字段" "timestamp" "$out"; fi
# 单帧 --time 3
"$NEW_CLI" media screenshot "$WORK/src.mp4" --time 3 --output "$WORK/shot3.jpg" >/dev/null 2>&1
assert_equal "screenshot --time exit" 0 "$?"
if [ -s "$WORK/shot3.jpg" ]; then report_pass; else report_fail "screenshot --time 产出" "非空" "空"; fi
# 多帧 --count 4
rm -rf "$WORK/shots"
"$NEW_CLI" media screenshot "$WORK/src.mp4" --count 4 --output-dir "$WORK/shots" >/dev/null 2>&1
shot_count=$(ls "$WORK/shots"/*.jpg 2>/dev/null | wc -l | tr -d ' ')
assert_equal "screenshot --count 4 产出 4 帧" "4" "$shot_count"
# --every 1.5s
rm -rf "$WORK/shots-every"
"$NEW_CLI" media screenshot "$WORK/src.mp4" --every 1.5s --output-dir "$WORK/shots-every" >/dev/null 2>&1
every_count=$(ls "$WORK/shots-every"/*.jpg 2>/dev/null | wc -l | tr -d ' ')
if [ "$every_count" -ge 2 ]; then report_pass; else report_fail "screenshot --every 产出" "≥2" "$every_count"; fi
# PNG 格式
"$NEW_CLI" media screenshot "$WORK/src.mp4" --time 1 --output "$WORK/shot.png" --format png >/dev/null 2>&1
assert_equal "screenshot PNG exit" 0 "$?"
if [ -s "$WORK/shot.png" ]; then report_pass; else report_fail "screenshot PNG 产出" "非空" "空"; fi
# 退化
"$NEW_CLI" media screenshot /nonexistent.mp4 >/dev/null 2>&1; assert_equal "screenshot 不存在 exit" 1 "$?"
"$NEW_CLI" media screenshot "$WORK/src.mp4" --time 999 --output "$WORK/x.jpg" >/dev/null 2>&1; assert_equal "screenshot 超时长 exit" 1 "$?"

# ── HDR 图片解码（EXR/Radiance 走 astiav；AVIF 回归 HEIC 通道真解码）──
# EXR SDR 夹具：ffmpeg 直转（sRGB 值域浮点——直通路径）
ffmpeg -y -v error -f lavfi -i "testsrc2=size=160x120:duration=1" -frames:v 1 -vf "format=gbrpf32le" "$WORK/hdr-sdr.exr" 2>/dev/null
out=$("$NEW_CLI" image info "$WORK/hdr-sdr.exr" 2>/dev/null); ec=$?
assert_equal "EXR info exit" 0 "$ec"
if echo "$out" | grep -q '"format":"exr"'; then report_pass; else report_fail "EXR info 格式字段" "exr" "$out"; fi
if echo "$out" | grep -q '"width":160'; then report_pass; else report_fail "EXR info 尺寸" "160" "$out"; fi
# EXR 解码：与 ffmpeg rgb24 参照逐像素（±2 浮点舍入）
"$NEW_CLI" image convert "$WORK/hdr-sdr.exr" "$WORK/hdr-sdr.png" >/dev/null 2>&1
python3 -I - "$WORK/hdr-sdr.exr" "$WORK/hdr-sdr.png" <<'PYX'
import subprocess, sys
def raw(p, fmt):
    return subprocess.run(['ffmpeg','-v','error','-i',p,'-pix_fmt',fmt,'-f','rawvideo','-'], capture_output=True).stdout
ref = raw(sys.argv[1], 'rgb24'); mine = raw(sys.argv[2], 'rgb24')
sys.exit(0 if len(ref)==len(mine) and max(abs(a-b) for a,b in zip(mine,ref))<=2 else 1)
PYX
assert_equal "EXR 解码 vs ffmpeg（±2）" 0 "$?"
# EXR 真 HDR 夹具：python 直造无压缩 FLOAT（R=4.0 G=2.0 B=1.0）
python3 -I - "$WORK" <<'PYX'
import struct, sys
def attr(n,t,d): return n.encode()+b'\0'+t.encode()+b'\0'+struct.pack('<I',len(d))+d
def cle(n): return n.encode()+b'\0'+struct.pack('<i',2)+b'\x00'+b'\0\0\0'+struct.pack('<ii',1,1)
W,H=4,2
hdr=b'\x76\x2f\x31\x01'+struct.pack('<I',2)
hdr+=attr('channels','chlist',cle('B')+cle('G')+cle('R')+b'\0')
hdr+=attr('compression','compression',struct.pack('<B',0))
hdr+=attr('dataWindow','box2i',struct.pack('<4i',0,0,W-1,H-1))
hdr+=attr('displayWindow','box2i',struct.pack('<4i',0,0,W-1,H-1))
hdr+=attr('lineOrder','lineOrder',struct.pack('<B',0))
hdr+=attr('pixelAspectRatio','float',struct.pack('<f',1.0))
hdr+=attr('screenWindowCenter','v2f',struct.pack('<2f',0,0))
hdr+=attr('screenWindowWidth','float',struct.pack('<f',1.0))
hdr+=b'\0'
chunks=[struct.pack('<II',y,W*3*4)+b''.join(struct.pack('<f',v)*W for v in (1.0,2.0,4.0)) for y in range(H)]
off=b''; body=b''
for c in chunks:
    off+=struct.pack('<Q',len(hdr)+8*H+len(body)); body+=c
open(sys.argv[1]+'/hdr-true.exr','wb').write(hdr+off+body)
PYX
"$NEW_CLI" image convert "$WORK/hdr-true.exr" "$WORK/hdr-true.png" >/dev/null 2>&1
python3 -I - "$WORK/hdr-true.png" <<'PYX'
import subprocess, sys
px = subprocess.run(['ffmpeg','-v','error','-i',sys.argv[1],'-f','rawvideo','-pix_fmt','rgb24','-'], capture_output=True).stdout
r,g,b = px[0],px[1],px[2]
# W=4 Reinhard: R=4→1.0 G=2→0.75 B=1→0.53125
sys.exit(0 if abs(r-255)<=1 and abs(g-191)<=1 and abs(b-135)<=1 else 1)
PYX
assert_equal "EXR 真 HDR tonemap（Reinhard 手算值）" 0 "$?"
# AVIF 夹具 + HEIC 通道真解码回归（execFFmpeg 空壳修复）
ffmpeg -y -v error -f lavfi -i "testsrc2=size=80x60:duration=1" -frames:v 1 -c:v libsvtav1 "$WORK/avif.avif" 2>/dev/null
"$NEW_CLI" image convert "$WORK/avif.avif" "$WORK/avif-out.png" >/dev/null 2>&1
python3 -I - "$WORK/avif.avif" "$WORK/avif-out.png" <<'PYX'
import subprocess, sys
def raw(p, fmt):
    return subprocess.run(['ffmpeg','-v','error','-i',p,'-pix_fmt',fmt,'-f','rawvideo','-'], capture_output=True).stdout
a = raw(sys.argv[1],'rgba64le'); b = raw(sys.argv[2],'rgba')
low = bytes(a[i*8+k] for i in range(len(a)//8) for k in (1,3,5))
mine = bytes(b[i*4+k] for i in range(len(b)//4) for k in (0,1,2))
sys.exit(0 if low==mine else 1)
PYX
assert_equal "AVIF 解码 vs ffmpeg rgba64le（逐位）" 0 "$?"

# ── zpack ffmpeg 透传 + JXL 解码链（捆绑 CLI 自产夹具=自举验证）──
out=$("$NEW_CLI" ffmpeg -version 2>/dev/null); ec=$?
assert_equal "ffmpeg 透传 -version exit" 0 "$ec"
if echo "$out" | grep -q "ffmpeg version"; then report_pass; else report_fail "ffmpeg -version 输出" "ffmpeg version 头" "$out"; fi
if echo "$out" | grep -q "libjxl"; then report_pass; else report_fail "静态构建含 libjxl" "libjxl 配置行" "$out"; fi
# 透传真实转码
"$NEW_CLI" ffmpeg -y -v error -f lavfi -i "sine=frequency=440:duration=2" "$WORK/ff-sine.mp3" >/dev/null 2>&1
if [ -s "$WORK/ff-sine.mp3" ]; then report_pass; else report_fail "ffmpeg 透传转码产出" "非空" "空"; fi
# 退出码原样传播（ffmpeg 非法参数=1）
"$NEW_CLI" ffmpeg -definitely-invalid-flag >/dev/null 2>&1; ec=$?
if [ "$ec" -ne 0 ]; then report_pass; else report_fail "ffmpeg 退出码传播" "非0" "$ec"; fi
# 进程内直调：零落盘（无缓存目录/无临时文件产生）
"$NEW_CLI" ffmpeg -version >/dev/null 2>&1; assert_equal "ffmpeg 进程内直调 exit" 0 "$?"
if [ ! -d "$HOME/Library/Caches/zpack" ] && [ ! -d "$HOME/.cache/zpack" ]; then report_pass; else report_fail "ffmpeg 零释放" "无缓存目录" "存在"; fi
# 管道输出不丢（cgo exit 不 flush 的坑已修——setvbuf 行缓冲）
if "$NEW_CLI" ffmpeg -version 2>/dev/null | grep -q "ffmpeg version"; then report_pass; else report_fail "ffmpeg 管道输出" "version 可见" "空"; fi
# JXL：捆绑 ffmpeg 编码夹具 → image 通道解码（整链路自举）
"$NEW_CLI" ffmpeg -y -v error -i "$WORK/src.mp4" -frames:v 1 -c:v libjxl "$WORK/jxl-test.jxl" >/dev/null 2>&1
if [ -s "$WORK/jxl-test.jxl" ]; then report_pass; else report_fail "JXL 夹具（捆绑 ffmpeg 编码）" "非空" "空"; fi
if [ -s "$WORK/jxl-test.jxl" ]; then
  "$NEW_CLI" image convert "$WORK/jxl-test.jxl" "$WORK/jxl-out.png" >/dev/null 2>&1
  jxl_dim=$(ffprobe -v error -show_entries stream=width,height -of csv=p=0 "$WORK/jxl-out.png" 2>/dev/null | head -1)
  src_dim=$(ffprobe -v error -show_entries stream=width,height -of csv=p=0 "$WORK/src.mp4" 2>/dev/null | head -1)
  assert_equal "JXL 解码尺寸=源" "$src_dim" "$jxl_dim"
fi

# ── office 转换（doc/wps → docx；office_oxide Rust 静态核心；textutil 双向交叉验证）──
cat > "$WORK/office-src.html" <<'HTML'
<html><body><h1>zpack office gate</h1><p>Alpha Bravo 中文混排 123。</p></body></html>
HTML
textutil -convert doc -output "$WORK/legacy.doc" "$WORK/office-src.html" 2>/dev/null
[ -s "$WORK/legacy.doc" ] && report_pass || report_fail "textutil 造 doc 夹具" "非空" "空"
out=$("$NEW_CLI" office convert "$WORK/legacy.doc" "$WORK/modern.docx" 2>/dev/null); ec=$?
assert_equal "office convert exit" 0 "$ec"
if echo "$out" | grep -q '"source_format":"doc","target_format":"docx"'; then report_pass; else report_fail "office convert 字段" "doc→docx" "$out"; fi
# docx = zip 容器 + word/document.xml 存在
if unzip -l "$WORK/modern.docx" 2>/dev/null | grep -q "word/document.xml"; then report_pass; else report_fail "docx 结构" "word/document.xml" "缺失"; fi
# textutil 读回交叉验证（内容保真）
if textutil -convert txt -stdout "$WORK/modern.docx" 2>/dev/null | grep -q "Alpha Bravo"; then report_pass; else report_fail "转换内容保真（textutil 读回）" "含源文本" "内容缺失"; fi
# .wps（现代 WPS = doc 同构；同字节改名 → 魔数识别路径）
cp "$WORK/legacy.doc" "$WORK/renamed.wps"
out=$("$NEW_CLI" office convert "$WORK/renamed.wps" "$WORK/wps-out.docx" 2>/dev/null); ec=$?
assert_equal "wps 转换 exit" 0 "$ec"
if echo "$out" | grep -q '"source_format":"doc(wps)"'; then report_pass; else report_fail "wps 魔数识别" "doc(wps)" "$out"; fi
if textutil -convert txt -stdout "$WORK/wps-out.docx" 2>/dev/null | grep -q "Alpha Bravo"; then report_pass; else report_fail "wps 转换内容保真" "含源文本" "缺失"; fi
# 退化五路
printf 'garbage' > "$WORK/bad.doc"
"$NEW_CLI" office convert "$WORK/bad.doc" "$WORK/x.docx" >/dev/null 2>&1; assert_equal "损坏 doc exit" 1 "$?"
"$NEW_CLI" office convert "$WORK/legacy.doc" "$WORK/x.pdf" >/dev/null 2>&1; assert_equal "非法目标格式 exit" 1 "$?"
"$NEW_CLI" office convert "$WORK/office-src.html" "$WORK/x.docx" >/dev/null 2>&1; assert_equal "非法输入格式 exit" 1 "$?"
printf '\x00\x01\x02wps' > "$WORK/old.wps"
"$NEW_CLI" office convert "$WORK/old.wps" "$WORK/x.docx" >/dev/null 2>&1; assert_equal "非 OLE2 wps（WPS2000 引导） exit" 1 "$?"
"$NEW_CLI" office convert /nonexistent.doc "$WORK/x.docx" >/dev/null 2>&1; assert_equal "输入不存在 exit" 1 "$?"
"$NEW_CLI" office --help >/dev/null 2>&1; assert_equal "office --help exit" 0 "$?"

# ── office --try-* 外部引擎（纯 Go 层；条件式——按机器安装状态断言不同路径）──
if [ -d "/Applications/LibreOffice.app" ] || command -v soffice >/dev/null 2>&1; then
  out=$("$NEW_CLI" office convert "$WORK/legacy.doc" "$WORK/lo.docx" --try-libreoffice 2>/dev/null); ec=$?
  assert_equal "try-libreoffice 转换 exit" 0 "$ec"
  if echo "$out" | grep -q '"engine":"libreoffice"'; then report_pass; else report_fail "engine 字段" "libreoffice" "$out"; fi
  if textutil -convert txt -stdout "$WORK/lo.docx" 2>/dev/null | grep -q "Alpha Bravo"; then report_pass; else report_fail "LO 转换内容保真" "含源文本" "缺失"; fi
else
  # 未装 LibreOffice：确定性报错（不是崩溃）
  out=$("$NEW_CLI" office convert "$WORK/legacy.doc" "$WORK/lo.docx" --try-libreoffice 2>/dev/null); ec=$?
  assert_equal "未装 LibreOffice 明确报错 exit" 1 "$ec"
  if echo "$out" | grep -q "未检测到 LibreOffice"; then report_pass; else report_fail "未装报错文案" "未检测到" "$out"; fi
fi
# 旗标互斥
"$NEW_CLI" office convert "$WORK/legacy.doc" "$WORK/x.docx" --try-wps --try-libreoffice >/dev/null 2>&1; assert_equal "try 旗标互斥 exit" 1 "$?"
# 外部引擎在纯构建也可用（engines 层零 cgo）——由本门禁跑在完整版上即可覆盖逻辑

# ── office 全格式矩阵（xls→xlsx / ppt→pptx / WPS 别名 et/dps）──
printf 'name,score\nalpha,90\nbravo,85\n' > "$WORK/sheet.csv"
if command -v soffice >/dev/null 2>&1 || [ -x "/Applications/LibreOffice.app/Contents/MacOS/soffice" ]; then
  LO_BIN="/Applications/LibreOffice.app/Contents/MacOS/soffice"
  command -v soffice >/dev/null 2>&1 && LO_BIN="soffice"
  "$LO_BIN" --headless --convert-to xls --outdir "$WORK" "$WORK/sheet.csv" >/dev/null 2>&1
  cat > "$WORK/slides.fodp" <<'FODP'
<?xml version="1.0" encoding="UTF-8"?>
<office:document xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" office:version="1.2" office:mimetype="application/vnd.oasis.opendocument.presentation">
 <office:body><office:presentation>
  <draw:page draw:name="p1"><draw:frame><draw:text-box><text:p>SlideGate Alpha</text:p></draw:text-box></draw:frame></draw:page>
 </office:presentation></office:body>
</office:document>
FODP
  "$LO_BIN" --headless --convert-to ppt --outdir "$WORK" "$WORK/slides.fodp" >/dev/null 2>&1
  # xls → xlsx（内置 office_oxide）
  out=$("$NEW_CLI" office convert "$WORK/sheet.xls" "$WORK/sheet.xlsx" 2>/dev/null); ec=$?
  assert_equal "xls→xlsx exit" 0 "$ec"
  if echo "$out" | grep -q '"source_format":"xls","target_format":"xlsx"'; then report_pass; else report_fail "xls 字段" "xls→xlsx" "$out"; fi
  "$LO_BIN" --headless --convert-to csv --outdir "$WORK/rt" "$WORK/sheet.xlsx" >/dev/null 2>&1
  if grep -q "alpha" "$WORK/rt/sheet.csv" 2>/dev/null; then report_pass; else report_fail "xlsx 内容（LO csv 回读）" "alpha" "缺失"; fi
  # ppt → pptx（内置）
  out=$("$NEW_CLI" office convert "$WORK/slides.ppt" "$WORK/slides.pptx" 2>/dev/null); ec=$?
  assert_equal "ppt→pptx exit" 0 "$ec"
  if unzip -p "$WORK/slides.pptx" ppt/slides/slide1.xml 2>/dev/null | grep -q "SlideGate Alpha"; then report_pass; else report_fail "pptx 内容" "SlideGate" "缺失"; fi
  # LO 引擎的 xls 转换（产物名动态扩展）
  out=$("$NEW_CLI" office convert "$WORK/sheet.xls" "$WORK/sheet-lo.xlsx" --try-libreoffice 2>/dev/null); ec=$?
  assert_equal "LO xls 引擎 exit" 0 "$ec"
  "$LO_BIN" --headless --convert-to csv --outdir "$WORK/rt2" "$WORK/sheet-lo.xlsx" >/dev/null 2>&1
  if grep -q "alpha" "$WORK/rt2/sheet-lo.csv" 2>/dev/null; then report_pass; else report_fail "LO xls 内容（csv 回读）" "alpha" "缺失"; fi
  # WPS 别名 .et/.dps（同字节改名 → 魔数路径）
  cp "$WORK/sheet.xls" "$WORK/renamed.et"
  cp "$WORK/slides.ppt" "$WORK/renamed.dps"
  out=$("$NEW_CLI" office convert "$WORK/renamed.et" "$WORK/et.xlsx" 2>/dev/null)
  if echo "$out" | grep -q '"source_format":"xls(et)"'; then report_pass; else report_fail "et 别名" "xls(et)" "$out"; fi
  out=$("$NEW_CLI" office convert "$WORK/renamed.dps" "$WORK/dps.pptx" 2>/dev/null)
  if echo "$out" | grep -q '"source_format":"ppt(dps)"'; then report_pass; else report_fail "dps 别名" "ppt(dps)" "$out"; fi
  # 退化：目标与源同族不符（doc→xlsx）
  "$NEW_CLI" office convert "$WORK/legacy.doc" "$WORK/x.xlsx" >/dev/null 2>&1; assert_equal "跨族转换 exit" 1 "$?"
fi

# ── audio 命令族（音频与视频分家：码率/声道/采样率质量旗标）──
# audio compress 缺省 50%：256k 语料 → 重编码（非直拷）+ 体积真降
out=$("$NEW_CLI" audio compress "$WORK/audio-src.m4a" --output "$WORK/ac-half.m4a" 2>/dev/null); exit_code=$?
assert_equal "audio compress 缺省 exit" 0 "$exit_code"
if echo "$out" | grep -q '"action":"encode"'; then report_pass; else report_fail "audio compress 重编码" "encode" "$out"; fi
ac_half_size=$(stat -f%z "$WORK/ac-half.m4a")
ac_src_size=$(stat -f%z "$WORK/audio-src.m4a")
if [ "$ac_half_size" -lt "$((ac_src_size * 7 / 10))" ]; then report_pass; else report_fail "audio compress 体积降 <70%" "明显小于源" "$ac_half_size/$ac_src_size"; fi
ac_half_rate=$(ffprobe -v error -show_entries stream=bit_rate -of default=noprint_wrappers=1:nokey=1 "$WORK/ac-half.m4a" 2>/dev/null | head -1)
if [ "$ac_half_rate" -lt 200000 ]; then report_pass; else report_fail "audio compress 码率降 <200k" "≤200k 量级" "$ac_half_rate"; fi
# --bitrate 精确码率（旗标来源值，确定性断言）
out=$("$NEW_CLI" audio compress "$WORK/audio-src.m4a" --bitrate 64k --output "$WORK/ac-64.m4a" 2>/dev/null)
if echo "$out" | grep -q '"bitrate":64000'; then report_pass; else report_fail "audio compress --bitrate 64k" "64000" "$out"; fi
# --mono 单声道
"$NEW_CLI" audio compress "$WORK/audio-src.m4a" --bitrate 64k --mono --output "$WORK/ac-mono.m4a" >/dev/null 2>&1
ac_channels=$(ffprobe -v error -show_entries stream=channels -of default=noprint_wrappers=1:nokey=1 "$WORK/ac-mono.m4a" 2>/dev/null | head -1)
assert_equal "audio compress --mono 声道" "1" "$ac_channels"
# 目标≥原始 → 明确拒绝
out=$("$NEW_CLI" audio compress "$WORK/audio-src.m4a" --bitrate 320k --output "$WORK/ac-x.m4a" 2>/dev/null); exit_code=$?
assert_equal "audio compress 目标≥原始 exit" 1 "$exit_code"
if echo "$out" | grep -q '"is_success":false'; then report_pass; else report_fail "audio compress 拒绝 JSON" "is_success:false" "$out"; fi
# audio convert 质量旗标全开：码率+采样率+声道同时生效
out=$("$NEW_CLI" audio convert "$WORK/audio-src.m4a" "$WORK/ac-conv.mp3" --bitrate 128k --sample-rate 22050 --mono 2>/dev/null); exit_code=$?
assert_equal "audio convert 旗标 exit" 0 "$exit_code"
if echo "$out" | grep -q '"bitrate":128000'; then report_pass; else report_fail "audio convert --bitrate 字段" "128000" "$out"; fi
ac_rate=$(ffprobe -v error -show_entries stream=sample_rate -of default=noprint_wrappers=1:nokey=1 "$WORK/ac-conv.mp3" 2>/dev/null | head -1)
assert_equal "audio convert --sample-rate 落地" "22050" "$ac_rate"
ac_ch=$(ffprobe -v error -show_entries stream=channels -of default=noprint_wrappers=1:nokey=1 "$WORK/ac-conv.mp3" 2>/dev/null | head -1)
assert_equal "audio convert --mono 落地" "1" "$ac_ch"
# audio info + 退化
out=$("$NEW_CLI" audio info "$WORK/audio-src.m4a" 2>/dev/null); exit_code=$?
assert_equal "audio info exit" 0 "$exit_code"
if echo "$out" | grep -q '"audio"'; then report_pass; else report_fail "audio info 字段" "audio" "$out"; fi
"$NEW_CLI" audio compress /nonexistent.m4a >/dev/null 2>&1; assert_equal "audio compress 不存在 exit" 1 "$?"
"$NEW_CLI" audio compress "$WORK/audio-src.m4a" --bitrate abc --output "$WORK/y.m4a" >/dev/null 2>&1; assert_equal "audio compress 非法码率 exit" 1 "$?"
"$NEW_CLI" audio --help >/dev/null 2>&1; assert_equal "audio --help exit" 0 "$?"

# ── 契约与退化 ──
"$NEW_CLI" media 2>/dev/null >/dev/null; assert_equal "media 无子命令 exit" 1 "$?"
"$NEW_CLI" media convert 不存在.mp4 out.mkv 2>/dev/null >/dev/null; assert_equal "输入不存在 exit" 1 "$?"
"$NEW_CLI" media convert "$WORK/src.mp4" 2>/dev/null >/dev/null; assert_equal "convert 缺参数 exit" 1 "$?"
out=$("$NEW_CLI" media compress "$WORK/src.mp4" --target-size 999zz --output "$WORK/x.mp4" 2>/dev/null); exit_code=$?
assert_equal "非法 target-size exit" 1 "$exit_code"
# --help 守卫
"$NEW_CLI" media --help >/dev/null 2>&1; assert_equal "media --help exit" 0 "$?"

rm -rf "$WORK"
# ---------- 汇总 ----------
echo
echo "媒体门禁：通过 $PASS_COUNT / 失败 $FAIL_COUNT"
[ "$FAIL_COUNT" -eq 0 ]
