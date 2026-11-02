#!/usr/bin/env bash
# Windows x86_64 产线：CGO 交叉构建（zig cc 路线——⏳ 待首跑验证）
# 前置：先跑同目录 prepare.sh 产出 libav-win64 静态库
set -euo pipefail

LIBAV="$(cd "$(dirname "$0")" && pwd)/libav-win64"
ZIG_TARGET="x86_64-windows-gnu"

cd "$(dirname "$0")/../poc"
export PKG_CONFIG_PATH="$LIBAV/lib/pkgconfig"
export CGO_ENABLED=1
export GOOS=windows GOARCH=amd64
export CC="zig cc -target $ZIG_TARGET"
export CXX="zig c++ -target $ZIG_TARGET"

go build -ldflags "-s -w" -o transcode.exe .

# 铁律 2 验收：import 表不新增 DLL（zig 全静态预期仅 kernel32 系）
if command -v objdump >/dev/null; then
  objdump -p transcode.exe | grep "DLL Name" || echo "（无 DLL 依赖——全静态）"
fi
echo "✓ $(pwd)/transcode.exe"
