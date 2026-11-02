#!/usr/bin/env bash
# Windows x86_64 libav 静态交叉编译（zig cc 路线——⏳ 待产线首跑验证）
# 前置：brew install zig（或任意 zig ≥0.13）
# 原理：zig cc 当 C 交叉编译器编 FFmpeg 静态库，mingw 目标全静态（不挂 msvcrt）
set -euo pipefail

TARGET="x86_64-w64-mingw32"
OUT="$(cd "$(dirname "$0")" && pwd)/libav-win64"
SRC="/tmp/ffmpeg-src-win"

[ -d "$SRC" ] || git clone --depth 1 --branch n8.1.2 https://github.com/FFmpeg/FFmpeg.git "$SRC"
cd "$SRC"

export CC="zig cc -target $TARGET-gnu"
export CXX="zig c++ -target $TARGET-gnu"
export AR="zig ar"
export RANLIB="zig ranlib"

# 注意：libx264 交叉编译需另行准备（同为 zig cc 静态编）；VideoToolbox 不可用，
# Windows 硬编走 Media Foundation（ffmpeg 的 h264_mf 编码器）或纯软编
./configure \
  --cross-prefix="" --arch=x86_64 --target-os=mingw32 \
  --disable-programs --disable-doc --disable-shared --enable-static \
  --enable-gpl --enable-libx264 \
  --prefix="$OUT"
make -j"$(sysctl -n hw.ncpu)" && make install
echo "✓ $OUT（pkg-config: $OUT/lib/pkgconfig）"
echo "下一步：同目录 build.sh"
