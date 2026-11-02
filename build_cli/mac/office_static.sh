#!/usr/bin/env bash
# office_oxide 静态产线：Rust 核心编译为 C ABI 静态库（doc/wps→docx 引擎）。
# 许可：MIT/Apache-2.0（无 GPL 污染）。产物入 prefix 与 office/libs/（包内自含——
# #cgo -L${SRCDIR}/libs 引用，裸 go test/build 不依赖产线环境变量）。
# 依赖：cargo（brew install rust）；幂等：库存在即跳过。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)/../.."
PREFIX="$(cd "$(dirname "$0")" && pwd)/prefix"
SRC="$(dirname "$0")/src"
LIB="liboffice_oxide.a"
JOBS="$(sysctl -n hw.ncpu)"

command -v cargo >/dev/null || { echo "✗ 需要 Rust 工具链：brew install rust"; exit 1; }

if [ ! -f "$SRC/office_oxide" ] || [ ! -d "$SRC/office_oxide/.git" ]; then
  rm -rf "$SRC/office_oxide"
  mkdir -p "$SRC"
  git clone --depth 1 --branch v0.1.16 https://github.com/yfedoseev/office_oxide.git "$SRC/office_oxide"
fi

if [ ! -f "$SRC/office_oxide/target/release/$LIB" ]; then
  (cd "$SRC/office_oxide" && cargo build --release --lib)
fi

# 清除会污染宿主模块包图的 Go 源（examples/cmd 引用其内部模块路径——go build ./... 会扫到）
rm -rf "$SRC/office_oxide/examples" "$SRC/office_oxide/cmd"
find "$SRC/office_oxide" -name "*.go" -not -path "*/go/*" -delete 2>/dev/null || true

mkdir -p "$PREFIX/lib" "$ROOT/office/libs"
cp "$SRC/office_oxide/target/release/$LIB" "$PREFIX/lib/"
cp "$SRC/office_oxide/target/release/$LIB" "$ROOT/office/libs/"
cp "$SRC/office_oxide/include/office_oxide_c/office_oxide.h" "$ROOT/office/libs/"
LIB_SIZE=$(du -h "$ROOT/office/libs/$LIB" | cut -f1)
echo "office_oxide 静态库就绪: $LIB_SIZE"
