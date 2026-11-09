#!/usr/bin/env bash
# Windows 编码器全家 + FFmpeg n9.0 静态产线（对齐 mac/ffmpeg_static.sh 配料；2026-11-09 定案）：
#   libjxl v0.11.1(SKCMS) / libaom v3.11 / x264 / x265(Bitbucket) / lame 3.100 / opus v1.5.2 /
#   libvpx v1.15 / FFmpeg n9.0 + fftools 直调库 → prefix + internal/ffmpegcli/libs
# 与 mac 的关键差异：
#   ① 零解压残留——源码/构建树全在 mktemp 临时目录，trap 退出即清（KEEP_WORK=1 保留现场）；
#     prefix 幂等缓存（产物在则整块跳过，重跑零下载零解压）；
#   ② Windows 专属补丁内联：ffmpeg 的 prepare_app_arguments 直调坑（cmdutils.c）、
#     objcopy 符号改名（#undef main/randinit twice 双坑）、pc 闭包静态化。
# 许可证同 mac：含 GPL 库（x264/x265）→ 产物承担 GPL 义务。
# 依赖：MSYS2/UCRT64 工具链（见 build.sh 头注）；网络走 https_proxy 环境变量（本机代理）。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
PREFIX="$HERE/prefix"
ZPACK_ROOT="$(cd "$HERE/../.." && pwd)"
JOBS="$(nproc)"
mkdir -p "$PREFIX"
[ "${FORCE:-0}" = "1" ] && rm -rf "$PREFIX"/* # 配料/版本变更时强制全量重建（对齐 mac FORCE 语义）

# 零解压残留：源码树/构建树全在系统临时目录，退出即清（KEEP_WORK=1 调试保留）
WORK="$(mktemp -d -t zpack-deps-XXXXXX)"
cleanup_work() { [ "${KEEP_WORK:-0}" = "1" ] || rm -rf "$WORK"; }
trap cleanup_work EXIT
cd "$WORK"

# 代理透传提示（本机直连龟速；Actions/GitHub 网络无需）
if [ -z "${https_proxy:-}" ]; then
  echo "ℹ 未设 https_proxy——若下载慢可 export https_proxy=http://127.0.0.1:7897"
fi

fetch() { # 参数：目标目录 / git-url branch | tarball-url
  local dest="$1" src="$2" branch="${3:-}"
  if [ "$src" = "${src%%.tar.*}" ]; then
    git clone --depth 1 ${branch:+--branch "$branch"} --recursive "$src" "$dest"
  else
    curl -sL --retry 3 -o src.tar.xz "$src" && mkdir -p "$dest" && tar -xf src.tar.xz -C "$dest" --strip-components=1
  fi
}

echo "═══ 1/8 libjxl v0.11.1（SKCMS 后端）═══"
if [ ! -f "$PREFIX/lib/libjxl.a" ]; then
  fetch libjxl https://github.com/libjxl/libjxl.git v0.11.1
  cmake -S libjxl -B libjxl-build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib \
    -DJPEGXL_ENABLE_BENCHMARK=OFF -DJPEGXL_ENABLE_EXAMPLES=OFF -DJPEGXL_ENABLE_MANPAGES=OFF \
    -DJPEGXL_ENABLE_JNI=OFF -DJPEGXL_ENABLE_SJPEG=OFF -DJPEGXL_ENABLE_OPENEXR=OFF \
    -DJPEGXL_ENABLE_SKCMS=ON -DJPEGXL_ENABLE_PLUGINS=OFF -DJPEGXL_ENABLE_TCMALLOC=OFF \
    -DJPEGXL_ENABLE_DEVTOOLS=OFF -DJPEGXL_ENABLE_FUZZERS=OFF -DJPEGXL_ENABLE_TOOLS=OFF \
    -DBUILD_TESTING=OFF
  cmake --build libjxl-build -j "$JOBS" && cmake --install libjxl-build
  # libjxl 0.11.1 pc 静态闭包缺陷（mac 陷阱 #2 同源 + Windows 陷阱 #29 链接顺序）：
  # 去掉 Requires、Libs 全闭包按依赖序重写——GNU ld 严格按序解析，Requires 展开顺序
  # 会让 brotlidec 的符号解析不到 brotlicommon（mac ld64 两遍扫描无此问题）
  sed -i 's/^Requires: .*$//' "$PREFIX/lib/pkgconfig/libjxl.pc"
  sed -i 's/^Libs: .*$//' "$PREFIX/lib/pkgconfig/libjxl.pc" && \
  echo "Libs: -L\${libdir} -ljxl -ljxl_cms -lhwy -lbrotlienc -lbrotlidec -lbrotlicommon -lstdc++ -lm" >> "$PREFIX/lib/pkgconfig/libjxl.pc"
  sed -i 's/^Libs: \(.*\)$/Libs: \1 -ljxl -lhwy -lbrotlienc -lbrotlidec -lbrotlicommon -lm -lstdc++/' "$PREFIX/lib/pkgconfig/libjxl_threads.pc"
  # 陷阱 #30（Windows 专属）：头文件默认 dllimport 声明 → 链接器找 __imp_ 符号 → 静态库
  # 必挂；静态链须 -DJXL_STATIC_DEFINE（mac 无 dllimport 机制故未踩）——注入全部 jxl pc 的 Cflags
  for jxlpc in libjxl.pc libjxl_cms.pc libjxl_threads.pc; do
    sed -i 's/^Cflags: \(.*\)$/Cflags: \1 -DJXL_STATIC_DEFINE/' "$PREFIX/lib/pkgconfig/$jxlpc"
  done
  echo "✓ libjxl"
else echo "✓ libjxl（缓存跳过）"; fi

echo "═══ 2/8 libaom v3.11.0 ═══"
if [ ! -f "$PREFIX/lib/libaom.a" ]; then
  fetch aom https://aomedia.googlesource.com/aom v3.11.0
  cmake -S aom -B aom-build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib \
    -DENABLE_TESTS=0 -DENABLE_EXAMPLES=0 -DENABLE_DOCS=0 -DENABLE_TOOLS=0 \
    -DENABLE_TEST_DATA=0 -DCONFIG_AV1_ENCODER=1
  cmake --build aom-build -j "$JOBS" && cmake --install aom-build
  echo "✓ libaom"
else echo "✓ libaom（缓存跳过）"; fi

echo "═══ 3/8 libx264（GPL）═══"
if [ ! -f "$PREFIX/lib/libx264.a" ]; then
  fetch x264 https://code.videolan.org/videolan/x264.git
  (cd x264 && ./configure --prefix="$PREFIX" --disable-shared --enable-static --disable-cli --enable-pic \
    --host=x86_64-w64-mingw32 && make -j "$JOBS" && make install)
  echo "✓ x264"
else echo "✓ x264（缓存跳过）"; fi

echo "═══ 4/8 libx265（GPL；Bitbucket 正源 master——GitHub 镜像停更 2020、code.videolan.org 要认证）═══"
if [ ! -f "$PREFIX/lib/libx265.a" ]; then
  # 陷阱 #27 三连（Windows 专属）：
  # a) Git Bash 的 git 走 schannel 对 bitbucket 握手失败——切 openssl 后端；
  # b) Version.cmake 用 git describe 推版本，MinGW 有 windres 才走 RC 版本块——depth-1 无
  #    tag 推出空串 → cmake list(GET) 崩（mac 无 RC 编译器故未踩）；-D 传值会被 git 推导覆盖。
  #    治本：rm -rf .git + 手写 x265Version.txt（Version.cmake 的 tarball 路径，读显式版本）；
  # c) 稳定 tag 3.6 的 cmake_policy(SET CMP0054 OLD) 被 MSYS2 cmake 4.x 拒（policy 已删除，
  #    CMAKE_POLICY_VERSION_MINIMUM 逃生阀管不到 SET）——故用 master（已适配 cmake 4）。
  git -c http.sslBackend=openssl clone --depth 1 \
    https://bitbucket.org/multicoreware/x265_git.git x265
  rm -rf x265/.git
  printf 'releasetag: 4.1\nreleasetagdistance: 0\nrepositorychangeset: zpackbuild\n' \
    > x265/x265Version.txt
  cmake -S x265/source -B x265-build -DCMAKE_BUILD_TYPE=Release -DENABLE_SHARED=OFF \
    -DENABLE_CLI=OFF -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib
  cmake --build x265-build -j "$JOBS" && cmake --install x265-build
  echo "✓ x265"
else echo "✓ x265（缓存跳过）"; fi
# x265 pc 闭包（陷阱 #31）：新版 x265 cmake 已自装 pc，但 C++ 运行时在 Libs.private——
# pkg-config --libs 不带出，ffmpeg 的 cc 级探测必挂（mac 手写 pc 已过时；mac clang 驱动
# 自动链 C++ 故未踩）。无条件确保 Libs 行含 -lstdc++ -lpthread（幂等）
if [ -f "$PREFIX/lib/libx265.a" ] && ! grep -q -- "-lstdc++" <(grep '^Libs:' "$PREFIX/lib/pkgconfig/x265.pc" 2>/dev/null); then
  sed -i 's/^Libs: \(.*\)$/Libs: \1 -lstdc++ -lpthread/' "$PREFIX/lib/pkgconfig/x265.pc"
fi

echo "═══ 5/8 lame 3.100 ═══"
if [ ! -f "$PREFIX/lib/libmp3lame.a" ]; then
  fetch lame "https://sourceforge.net/projects/lame/files/lame/3.100/lame-3.100.tar.gz/download"
  # 陷阱 #28：frontend 老代码撞 gcc 14+ 隐式声明默认错误化——只要 libmp3lame 库，
  # --disable-frontend 跳过（同 x264 --disable-cli 哲学）
  (cd lame && ./configure --prefix="$PREFIX" --disable-shared --enable-static --disable-dependency-tracking \
    --disable-frontend --host=x86_64-w64-mingw32 && make -j "$JOBS" && make install)
  echo "✓ lame"
else echo "✓ lame（缓存跳过）"; fi

echo "═══ 6/8 opus v1.5.2 ═══"
if [ ! -f "$PREFIX/lib/libopus.a" ]; then
  fetch opus https://github.com/xiph/opus.git v1.5.2
  cmake -S opus -B opus-build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
    -DCMAKE_INSTALL_PREFIX="$PREFIX" -DCMAKE_INSTALL_LIBDIR=lib \
    -DOPUS_BUILD_SHARED_LIBRARY=OFF -DBUILD_TESTING=OFF
  cmake --build opus-build -j "$JOBS" && cmake --install opus-build
  echo "✓ opus"
else echo "✓ opus（缓存跳过）"; fi

echo "═══ 7/8 libvpx v1.15.0 ═══"
if [ ! -f "$PREFIX/lib/libvpx.a" ]; then
  fetch libvpx https://github.com/webmproject/libvpx.git v1.15.0
  (cd libvpx && ./configure --prefix="$PREFIX" --disable-shared --enable-static \
    --disable-unit-tests --disable-examples --disable-tools --disable-docs --disable-install-docs \
    --enable-vp9-highbitdepth --enable-pic --target=x86_64-win64-gcc && make -j "$JOBS" && make install)
  echo "✓ libvpx"
else echo "✓ libvpx（缓存跳过）"; fi

echo "═══ 8/8 FFmpeg n9.0（库 + fftools 直调库）═══"
FFMPEG_TARBALL="https://ffmpeg.org/releases/ffmpeg-9.0.tar.xz"
if [ ! -f "$PREFIX/lib/libavcodec.a" ] || [ ! -f "$PREFIX/lib/libzpack_ffmpeg_cli.a" ]; then
  fetch ffmpeg "$FFMPEG_TARBALL"
  # ⚠️ 进程内直调补丁（版本无关；Windows 陷阱 #17）：cmdutils.c 的 prepare_app_arguments
  # 在 _WIN32 下用 GetCommandLineW() 重取宿主命令行——直调拿到的是 zpack.exe 的参数。
  sed -i 's|prepare_app_arguments(&argc, &argv);|/* zpack: 直调禁用 Win32 argv 重取 */|g' \
    ffmpeg/fftools/cmdutils.c
  cd ffmpeg
  export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig" # mac 陷阱 #1：configure 探测全靠它，必须前置
  ./configure --prefix="$PREFIX" --target-os=mingw32 --arch=x86_64 \
    --disable-shared --enable-static --disable-debug --disable-doc --disable-ffplay \
    --disable-xlib --disable-libxcb \
    --enable-libjxl --enable-libaom \
    --enable-gpl --enable-libx264 --enable-libx265 --enable-libmp3lame --enable-libopus --enable-libvpx \
    --enable-iconv --extra-libs=-liconv \
    --extra-cflags="-I$PREFIX/include -O2" --extra-ldflags="-L$PREFIX/lib"
  make -j "$JOBS" && make install
  # GNU ld 按序解析（Windows 陷阱 #18）：libavcodec 引用 libiconv 须在其后——注入 pc
  sed -i 's/^Libs: \(.*\)$/Libs: \1 -liconv/' "$PREFIX/lib/pkgconfig/libavcodec.pc"
  # 陷阱 #33：ffmpeg 9 的 pc 把依赖闭包散装直写 Libs（8.x 走 Requires.private），cgo 的
  # pkg-config 展开还会重排——jxl 全家顺序失控（JxlGetDefaultCms 必挂）；jxl 符号统一由
  # merged 兜底库紧跟 -lav* 注入（散装残留无害——多余静态库不报错）
  for avpc in libavcodec.pc libavformat.pc libavfilter.pc; do
    if ! grep -q -- "-ljxl_merged" "$PREFIX/lib/pkgconfig/$avpc" 2>/dev/null; then
      sed -i "s|^Libs: \(.*-lav[a-z]*\)|Libs: \1 -L$PREFIX/lib -ljxl_merged|" "$PREFIX/lib/pkgconfig/$avpc"
    fi
  done
  # fftools 直调库（陷阱 #15/#16：-Dmain 被 _WIN32 的 #undef 取消；C main 入链=randinit
  # twice 段错误）——治本：正常编译后 objcopy 目标文件层符号改名（主构建树内完成，同配置）
  gcc -O2 -I. -Ifftools -Wno-error=missing-prototypes -c fftools/ffmpeg.c -o ffmpeg_cli_main.o
  objcopy --redefine-sym main=zpack_ffmpeg_cli_main ffmpeg_cli_main.o
  ar rcs libzpack_ffmpeg_cli.a ffmpeg_cli_main.o \
    $(find fftools -name '*.o' | grep -Ev '/(ffmpeg|ffprobe|ffplay)\.o$')
  cp libzpack_ffmpeg_cli.a "$PREFIX/lib/"
  mkdir -p "$ZPACK_ROOT/internal/ffmpegcli/libs"
  cp libzpack_ffmpeg_cli.a "$ZPACK_ROOT/internal/ffmpegcli/libs/"
  echo "✓ ffmpeg 库族 + 直调库"
else echo "✓ ffmpeg 库族 + 直调库（缓存跳过）"; fi

# 陷阱 #32：GNU ld 按序解析 + pkg-config 多 pc 展开顺序不可控 → jxl 全家跨库互引
#（libjxl 引用 libjxl_cms 的 JxlGetDefaultCms）在 cgo 终链必挂——物理合并为单一 .a 消解
#（mac ld64 两遍扫描无此问题）。合并幂等：产物存在即跳过。
if [ -f "$PREFIX/lib/libjxl.a" ] && [ ! -f "$PREFIX/lib/libjxl_merged.a" ]; then
  MERGE_DIR="$(mktemp -d -t zpack-jxl-merge-XXXX)"
  for jxllib in libjxl libjxl_cms libhwy libbrotlienc libbrotlidec libbrotlicommon; do
    (cd "$MERGE_DIR" && ar x "$PREFIX/lib/$jxllib.a")
  done
  # cmake 系对象后缀是 .obj（非 .o）——两种都收（nullglob：空 glob 不传字面量给 ar）
  (cd "$MERGE_DIR" && shopt -s nullglob && ar rcs "$PREFIX/lib/libjxl_merged.a" *.obj *.o)
  rm -rf "$MERGE_DIR"
  sed -i 's/^Libs: .*$//' "$PREFIX/lib/pkgconfig/libjxl.pc" && \
    echo "Libs: -L\${libdir} -ljxl_merged -lstdc++ -lm" >> "$PREFIX/lib/pkgconfig/libjxl.pc"
  sed -i 's/-ljxl /-ljxl_merged /g; s/-ljxl$/-ljxl_merged/' "$PREFIX/lib/pkgconfig/libjxl_threads.pc"
fi

# MSYS2 运行库静态钉正（Windows 陷阱 #19）：pc 里 -lXXX 一律替换为 .a 绝对路径——
# ld 见 .a 即静态链，MSYS2 的 DLL 一律不得进 import 表（铁律 2）。
# sed 拆朴素表达式（陷阱 #21：BRE 的 \(..\|..\) 组合在 MSYS2 失配）
for libname in z stdc++ bz2 lzma iconv pthread; do
  STATIC_LIB="$(gcc -print-file-name=lib${libname}.a)"
  [ -f "$STATIC_LIB" ] || continue
  for pcfile in "$PREFIX/lib/pkgconfig/"*.pc; do
    sed -i "s| -l${libname} | $STATIC_LIB |g; s| -l${libname}$| $STATIC_LIB|g" "$pcfile"
  done
done

echo "═══ 编码器产线就绪 ═══"
ls "$PREFIX/lib/"lib{x264,x265,mp3lame,opus,vpx,jxl,aom,avcodec}.a 2>/dev/null | sed 's/^/  /'
