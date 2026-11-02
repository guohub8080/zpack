#!/usr/bin/env bash
# Linux x86_64 产线骨架（musl 静态——⏳ 待验证；前置同 windows 模式：zig cc 交叉编 libav 后 CGO 构建）
# 与 windows/prepare.sh 同构，目标改 x86_64-linux-musl，GOOS=linux GOARCH=amd64
