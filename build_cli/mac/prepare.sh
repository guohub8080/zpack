#!/usr/bin/env bash
# mac libav 依赖解析（POC 阶段：直接用 brew ffmpeg 8.x 的共享库 + pkg-config）
# 后续静态化（可选，若要求 zpack 真单文件）：
#   git clone --branch n8.1.2 https://github.com/FFmpeg/FFmpeg.git /tmp/ffmpeg-src
#   cd /tmp/ffmpeg-src && ./configure --disable-programs --disable-doc --enable-static \
#     --disable-shared --prefix="$(pwd)/../libav-mac" \
#     --enable-gpl --enable-libx264 --enable-videotoolbox \
#     $(pkg-config --exists x264 && echo --extra-cflags="-I$(brew --prefix x264)/include" --extra-ldflags="-L$(brew --prefix x264)/lib")
#   make -j$(sysctl -n hw.ncpu) && make install
#   然后 export PKG_CONFIG_PATH="$(pwd)/../libav-mac/lib/pkgconfig"
set -euo pipefail

if ! command -v pkgconf >/dev/null; then
  brew install pkgconf
fi
if ! pkg-config --exists libavcodec 2>/dev/null; then
  brew install ffmpeg
fi
echo "libavcodec $(pkg-config --modversion libavcodec) 就绪（brew 共享库模式）"
