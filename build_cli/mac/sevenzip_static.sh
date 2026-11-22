#!/usr/bin/env bash
# 7z 官方 C++ 静态产线：7-Zip 25.01（ip7z/7zip）Alone2 bundle 铸进程内直调库。
# 产出 libzpack_7zcli.a → internal/sevenzipcli/libs/（包内自含——#cgo -L${SRCDIR}/libs
# 引用，裸 go test/build 不依赖产线环境变量）。
# 关键手法（2026-11-22 首铸实录，全部踩过的坑）：
#   1. DISABLE_RAR 只被 Format7zF bundle 的 Arc_gcc.mak 认——Alone2+cmpl_mac_arm64.mak
#      无视它（Rar*.o 照链，unRAR 许可限制代码入产物）。剥 RAR = 归档阶段手动排除 Rar*.o。
#   2. main 改名 extern "C" zpack_7z_main（MainAr.cpp:103）——裸改名得 C++ 修饰名，必须 extern "C"。
#   3. USE_ASM=1 撞 Asm/arm64/7zAsm.S 的 `and` 宏 × -Wc++-keyword（clang 把 .S 当 C++ 编）；
#      var_mac_arm64.mak 又设 CFLAGS_WARN=-Weverything（C/LzmaEnc.c implicit void-ptr-cast 报错）。
#      两伤相权取其轻：USE_ASM=（关 ASM，arm64 的 CRC/SHA/AES 本就是 C 优化路径，无损）+
#      CFLAGS_WARN="-Wno-everything" 覆盖。
#   4. main 改名后可执行文件链接必败（crt 找不到 main）——预期，只要 .o。
#   5. 【最重要】7z 的格式/编码器注册靠静态 ctor：普通按需拉取的 .a 永远拉不到注册对象
#      （引擎注册表全空、命令全 E_NOTIMPL）。先 ld -r 把全部 .o 合并为单一可重定位对象，
#      再 ar 成单成员归档——拉 zpack_7z_main 连带全部对象，等价 whole-archive
#      （cgo LDFLAGS 不接受带路径的 -Wl,-force_load，此路绕开）。
# 幂等：库存在即跳过；FORCE=1 强制重建。
# 依赖：Xcode clang + ar + ld（mac 出厂自带）；源码走代理 clone（ffmpeg 产线同款 https_proxy 惯例）。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)/../.."
SRC="$(dirname "$0")/src/7zip"
BUNDLE="$SRC/CPP/7zip/Bundles/Alone2"
BUILD="$BUNDLE/b/m_arm64"
LIB="libzpack_7zcli.a"
DEST="$ROOT/internal/sevenzipcli/libs/$LIB"
SEVENZIP_TAG="25.01"

if [ -f "$DEST" ] && [ "${FORCE:-0}" != "1" ]; then
  echo "7z 直调库已就绪: $(du -h "$DEST" | cut -f1)（FORCE=1 重建）"
  exit 0
fi

if [ ! -d "$SRC" ] || [ ! -f "$SRC/CPP/7zip/Bundles/Alone2/makefile" ]; then
  rm -rf "$SRC"
  mkdir -p "$(dirname "$SRC")"
  git clone --depth 1 --branch "$SEVENZIP_TAG" https://github.com/ip7z/7zip.git "$SRC"
fi

# main 改名（幂等：已改过则 grep 不中，跳过）
MAINAR="$SRC/CPP/7zip/UI/Console/MainAr.cpp"
if ! grep -q "extern \"C\" int Z7_CDECL zpack_7z_main" "$MAINAR"; then
  sed -i '' 's/^int Z7_CDECL main(int numArgs, char \*args\[\])/extern "C" int Z7_CDECL zpack_7z_main(int numArgs, char *args[])/' "$MAINAR"
fi
grep -q "extern \"C\" int Z7_CDECL zpack_7z_main" "$MAINAR" || { echo "✗ main 改名补丁失败"; exit 1; }

(cd "$BUNDLE" && make -j"$(sysctl -n hw.ncpu)" -f ../../cmpl_mac_arm64.mak USE_ASM= CFLAGS_WARN="-Wno-everything" 2>&1 | grep -v "^clang" | tail -2) || true
# 可执行文件链接失败是预期（crt 找不到改名后的 main）——只要对象
[ -d "$BUILD" ] && [ "$(ls "$BUILD"/*.o 2>/dev/null | wc -l)" -gt 200 ] || { echo "✗ 对象未产出"; exit 1; }

# 剥 RAR（许可）+ 单成员归档（注册 ctor 必须整包链入）
cd "$BUILD"
NON_RAR_OBJS=$(ls *.o | grep -v '^Rar')
ld -r -o zpack_7z_combined.o $NON_RAR_OBJS
rm -f "$LIB"
ar rcs "$LIB" zpack_7z_combined.o
nm "$LIB" | grep -q "T _zpack_7z_main" || { echo "✗ zpack_7z_main 符号缺失"; exit 1; }
if ar t "$LIB" 2>/dev/null | head -1 | grep -qi "^Rar"; then echo "✗ RAR 对象混入"; exit 1; fi

mkdir -p "$ROOT/internal/sevenzipcli/libs"
cp "$LIB" "$DEST"
echo "7z 直调库就绪: $(du -h "$DEST" | cut -f1)（$(ls *.o | grep -vc '^Rar') 对象合并，RAR 已剥除）"
