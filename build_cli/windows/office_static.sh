#!/usr/bin/env bash
# office_oxide 静态产线（Windows 版，照 mac/office_static.sh 移植；2026-11-09 临时目录化）：
# MSYS2 UCRT64 rust（x86_64-pc-windows-gnu，与 MinGW gcc 同 ABI）编 Rust 核心为 C ABI 静态库。
# 零解压残留：clone/构建树在 mktemp 临时目录，trap 退出即清（KEEP_WORK=1 保留现场）。
# 许可：MIT/Apache-2.0。产物 office/libs/（.gitignore）——#cgo -L${SRCDIR}/libs 包内自含。
# 依赖：pacman -S mingw-w64-ucrt-x86_64-rust；网络走 https_proxy 环境变量。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"

command -v cargo >/dev/null || { echo "✗ 需要 Rust：pacman -S mingw-w64-ucrt-x86_64-rust"; exit 1; }

if [ -f "$ROOT/office/libs/liboffice_oxide.a" ]; then
  echo "✓ office_oxide（缓存跳过）"
  exit 0
fi

WORK="$(mktemp -d -t zpack-office-XXXXXX)"
cleanup_work() { [ "${KEEP_WORK:-0}" = "1" ] || rm -rf "$WORK"; }
trap cleanup_work EXIT
cd "$WORK"

git clone --depth 1 --branch v0.1.16 https://github.com/yfedoseev/office_oxide.git office_oxide
(cd office_oxide && cargo build --release --lib)

# 清除会污染宿主模块包图的 Go 源（mac 产线同源陷阱）后拷产物（仅 .a + 头文件落地）
mkdir -p "$ROOT/office/libs"
cp office_oxide/target/release/liboffice_oxide.a "$ROOT/office/libs/"
cp office_oxide/include/office_oxide_c/office_oxide.h "$ROOT/office/libs/"
echo "✓ office_oxide 静态库就绪: $(du -h "$ROOT/office/libs/liboffice_oxide.a" | cut -f1)"
