#!/usr/bin/env bash
# Linux x86_64 产线（musl 静态——⏳ 待验证）
# 前置：先跑同目录 prepare.sh（zig cc 交叉编 libav，目标 x86_64-linux-musl）
# 构建：CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-musl" go build
set -euo pipefail
echo "⏳ 产线待首跑验证——按上方注释补全"
