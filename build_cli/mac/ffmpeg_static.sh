#!/usr/bin/env bash
# mac 静态 ffmpeg 产线：libjxl(BSD) + libaom(BSD) + FFmpeg 8.1.2 全静态。
# 产物 prefix/：静态库(.a)+头文件+pkgconfig+ffmpeg CLI 二进制（供嵌入）。
# 许可证注意（2026-11-08 用户定案功能全优先）：含 GPL 库（x264/x265）→ 产物承担 GPL 义务；
# 对外分发需提供对应源码/重链对象。内部使用无碍；如需 LGPL 版可去掉 x264/x265（VideoToolbox 硬编仍可 h264/hevc）。
# 复用：prefix 已有产物时跳过重编（幂等）；FORCE=1 强制全量重建。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
SRC="$ROOT/src"
PREFIX="$ROOT/prefix"
JOBS="$(sysctl -n hw.ncpu)"
mkdir -p "$SRC" "$PREFIX"
[ "${FORCE:-0}" = "1" ] && rm -rf "$PREFIX" && mkdir -p "$PREFIX"

# ── libjxl v0.11.1（BSD）——JXL 图片编解码 ──
if [ ! -f "$PREFIX/lib/libjxl.a" ]; then
  cd "$SRC"
  if [ ! -d libjxl ]; then
    git clone --depth 1 --branch v0.11.1 --recursive https://github.com/libjxl/libjxl.git
  fi
  cmake -S libjxl -B libjxl-build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib \
    -DJPEGXL_ENABLE_BENCHMARK=OFF -DJPEGXL_ENABLE_EXAMPLES=OFF -DJPEGXL_ENABLE_MANPAGES=OFF \
    -DJPEGXL_ENABLE_JNI=OFF -DJPEGXL_ENABLE_SJPEG=OFF -DJPEGXL_ENABLE_OPENEXR=OFF \
    -DJPEGXL_ENABLE_SKCMS=ON -DJPEGXL_ENABLE_PLUGINS=OFF -DJPEGXL_ENABLE_TCMALLOC=OFF \
    -DJPEGXL_ENABLE_DEVTOOLS=OFF -DJPEGXL_ENABLE_FUZZERS=OFF -DJPEGXL_ENABLE_TOOLS=OFF \
    -DBUILD_TESTING=OFF
  cmake --build libjxl-build -j "$JOBS"
  cmake --install libjxl-build
  # libjxl 0.11.1 pc 缺陷：静态闭包不全（brotlidec 依赖 brotlicommon；threads 依赖 jxl 全家）——ffmpeg configure 探测必挂
  sed -i '' 's/^Libs: \(.*\)$/Libs: \1 -lbrotlicommon/' "$PREFIX/lib/pkgconfig/libjxl.pc"
  sed -i '' 's/^Libs: \(.*\)$/Libs: \1 -ljxl -lhwy -lbrotlienc -lbrotlidec -lbrotlicommon -lm -lc++/' "$PREFIX/lib/pkgconfig/libjxl_threads.pc"
  echo "✓ libjxl → $PREFIX/lib/libjxl.a"
fi

# ── libaom v3.11.0（BSD）——AV1 编解码 ──
if [ ! -f "$PREFIX/lib/libaom.a" ]; then
  cd "$SRC"
  if [ ! -d aom ]; then
    git clone --depth 1 --branch v3.11.0 https://aomedia.googlesource.com/aom
  fi
  cmake -S aom -B aom-build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib \
    -DENABLE_TESTS=0 -DENABLE_EXAMPLES=0 -DENABLE_DOCS=0 -DENABLE_TOOLS=0 \
    -DENABLE_TEST_DATA=0 -DCONFIG_AV1_ENCODER=1
  cmake --build aom-build -j "$JOBS"
  cmake --install aom-build
  echo "✓ libaom → $PREFIX/lib/libaom.a"
fi

# ── 编码器全家（用户定案：功能全优先；GPL 库入列=产物承担 GPL 义务）──
build_autotools() { # 参数：名字/克隆URL或tarball/版本/configure额外参数...
  local name="$1" src="$2" extra="${3:-}"
  if [ -f "$PREFIX/lib/lib${name}.a" ]; then return 0; fi
  cd "$SRC"
  [ -d "$name" ] || { case "$src" in
    *.tar.gz) curl -sL "$src" | tar xz && mv "${name}-${name#*-}"* "$name" 2>/dev/null ;;
    *) git clone --depth 1 "$src" "$name" ;;
  esac; }
  cd "$name"
  ./configure --prefix="$PREFIX" --disable-shared --enable-static $extra
  make -j "$JOBS" && make install
  echo "✓ $name → $PREFIX"
}

# libx264（GPL——h264 软编事实标准）
if [ ! -f "$PREFIX/lib/libx264.a" ]; then
  cd "$SRC"
  [ -d x264 ] || git clone --depth 1 https://code.videolan.org/videolan/x264.git
  cd x264 && ./configure --prefix="$PREFIX" --disable-shared --enable-static --disable-cli --enable-pic
  make -j "$JOBS" && make install
  echo "✓ x264 → $PREFIX"
fi

# libx265（GPL——hevc 软编）
if [ ! -f "$PREFIX/lib/libx265.a" ]; then
  cd "$SRC"
  [ -d x265 ] || git clone --depth 1 https://code.videolan.org/videolan/x265.git
  cmake -S x265/source -B x265-build -DCMAKE_BUILD_TYPE=Release -DENABLE_SHARED=OFF     -DENABLE_CLI=OFF -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib
  cmake --build x265-build -j "$JOBS" && cmake --install x265-build
  echo "✓ x265 → $PREFIX"
fi
# x265 cmake 不装 pc（模板在源码树未处理）——手写一份带静态闭包（无条件：.a 在则构建块跳过也要补）
if [ -f "$PREFIX/lib/libx265.a" ] && [ ! -s "$PREFIX/lib/pkgconfig/x265.pc" ]; then
  cat > "$PREFIX/lib/pkgconfig/x265.pc" <<PC
Name: x265
Description: H.265/HEVC video encoder
Version: 4.1
Libs: -L$PREFIX/lib -lx265 -lpthread -lm -lc++
Cflags: -I$PREFIX/include
PC
fi

# libmp3lame（mp3 编码——ffmpeg 无原生 mp3 编码器）
if [ ! -f "$PREFIX/lib/libmp3lame.a" ]; then
  cd "$SRC"
  [ -d lame ] || { curl -sL https://sourceforge.net/projects/lame/files/lame/3.100/lame-3.100.tar.gz/download -o lame.tar.gz && mkdir lame && tar xzf lame.tar.gz -C lame --strip-components=1; }
  cd lame && ./configure --prefix="$PREFIX" --disable-shared --enable-static --disable-dependency-tracking
  make -j "$JOBS" && make install
  echo "✓ lame → $PREFIX"
fi

# libopus（opus 编码——webm 音轨）
if [ ! -f "$PREFIX/lib/libopus.a" ]; then
  cd "$SRC"
  [ -d opus ] || git clone --depth 1 --branch v1.5.2 https://github.com/xiph/opus.git
  cmake -S opus -B opus-build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF     -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib -DOPUS_BUILD_SHARED_LIBRARY=OFF -DBUILD_TESTING=OFF
  cmake --build opus-build -j "$JOBS" && cmake --install opus-build
  echo "✓ opus → $PREFIX"
fi

# libvpx（vp8/vp9 编码——webm 视频）
if [ ! -f "$PREFIX/lib/libvpx.a" ]; then
  cd "$SRC"
  [ -d libvpx ] || git clone --depth 1 --branch v1.15.0 https://github.com/webmproject/libvpx.git
  cd libvpx && ./configure --prefix="$PREFIX" --disable-shared --enable-static     --disable-unit-tests --disable-examples --disable-tools --disable-docs --disable-install-docs     --enable-vp9-highbitdepth --enable-pic
  make -j "$JOBS" && make install
  echo "✓ libvpx → $PREFIX"
fi

# ── FFmpeg n9.0 全静态（库 + CLI 程序；2026-11-09 用户定案 8.1.2→9.0，与 Windows 产线对齐）──
cd "$SRC"
if [ ! -d ffmpeg ]; then
  git clone --depth 1 --branch n9.0 https://github.com/FFmpeg/FFmpeg.git
fi
cd ffmpeg
if [ ! -f "$PREFIX/lib/libavcodec.a" ] || [ ffmpeg -nt "$PREFIX/lib/libavcodec.a" ]; then
  export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"
  ./configure --prefix="$PREFIX" --disable-shared --enable-static \
    --disable-debug --disable-doc --disable-ffplay \
    --disable-xlib --disable-libxcb \
    --enable-libjxl --enable-libaom \
    --enable-gpl --enable-libx264 --enable-libx265 --enable-libmp3lame --enable-libopus --enable-libvpx \
    --enable-videotoolbox --enable-pthreads \
    --extra-cflags="-I$PREFIX/include -O2" \
    --extra-ldflags="-L$PREFIX/lib"
  make -j "$JOBS"
  make install
  echo "✓ ffmpeg 静态库族 → $PREFIX/lib/"
fi
# ffmpeg CLI 进程内直调对象（-Dmain 改名 → zpack ffmpeg 命令零释放零子进程）
if [ ! -f "$PREFIX/lib/libzpack_ffmpeg_cli.a" ]; then
  cd "$SRC/ffmpeg"
  CLIFLAGS=$(grep "^CPPFLAGS=" ffbuild/config.mak | sed "s/^CPPFLAGS=//;s|\$(SRC_PATH)|$(pwd)|g")
  eval cc $CLIFLAGS -I. -Dmain=zpack_ffmpeg_cli_main -Wno-error=missing-prototypes \
    -c fftools/ffmpeg.c -o fftools/ffmpeg_zpack.o
  # 全量 fftools 对象（含 8.x 新增 graph/ 子目录）排除 ffprobe/ffplay/原名 ffmpeg.o
  ar rcs "$PREFIX/lib/libzpack_ffmpeg_cli.a" fftools/ffmpeg_zpack.o \
    $(ls fftools/*.o fftools/*/*.o 2>/dev/null | grep -vE '/(ffmpeg|ffprobe|ffplay)\.o$|ffplay_renderer|ffmpeg_zpack')
  echo "✓ ffmpeg CLI 对象 → $PREFIX/lib/libzpack_ffmpeg_cli.a（进程内直调用，~0.5MB）"
  # 拷进包目录——#cgo LDFLAGS -L${SRCDIR}/libs 引用（裸 go test/build 不再依赖产线环境变量）
  mkdir -p "$ROOT/internal/ffmpegcli/libs"
  cp "$PREFIX/lib/libzpack_ffmpeg_cli.a" "$ROOT/internal/ffmpegcli/libs/"
fi
echo "═══ 产线就绪 ═══"
ls -la "$PREFIX/lib/"*.a | awk '{print "  lib:", $NF, "("$5/1024/1024"MB)"}'
ls -la "$PREFIX/bin/ffmpeg" | awk '{print "  cli:", $NF, "("$5/1024/1024"MB)"}'
