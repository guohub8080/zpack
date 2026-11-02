#!/usr/bin/env bash
# mac 完整版产线：静态 libav（自建 prefix）+ ffmpeg CLI 尾注嵌入。
# 产物 bin/zpack：单文件自包含（媒体功能 + zpack ffmpeg 透传），裸机可跑。
# 首次运行自动触发 ffmpeg_static.sh（源码级全静态，约 15-25 分钟）；prefix 幂等复用。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)" && cd "$ROOT/../.."
PREFIX="$ROOT/prefix"

[ -f "$PREFIX/bin/ffmpeg" ] || bash "$ROOT/ffmpeg_static.sh"
[ -f "$ROOT/office/libs/liboffice_oxide.a" ] || bash "$ROOT/office_static.sh"

echo "── 静态链接构建 ──"
export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"
go clean -cache  # cgo 的 pkg-config 结果会被构建缓存按环境变量之外键缓存——切静态前缀后必须清
CGO_ENABLED=1 go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)" -o bin/zpack ./cmd/zpack

echo "── 校验零动态依赖（brew libav 必须归零）──"
dynamic_count=$(otool -L bin/zpack | grep -c "/opt/homebrew" || true)
if [ "$dynamic_count" != "0" ]; then
  echo "✗ 仍链接 brew 动态库 $(otool -L bin/zpack | grep '/opt/homebrew' | head -5)"
  exit 1
fi
echo "✓ 零 brew 动态依赖"

echo "── 产物 ──"
du -h bin/zpack | awk '{print "  bin/zpack: "$1}'
otool -L bin/zpack | tail -n +2 | awk '{print "  动态依赖:", $1}'
echo "✓ 完成（单文件自包含：媒体引擎 + ffmpeg CLI 进程内直调，零释放）"
