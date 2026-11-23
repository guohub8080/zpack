#!/usr/bin/env bash
# zpack GUI 打包装机一条龙：mygo build → 注入引擎 → 安装 /Applications/zpack.app。
#
# App 正名即 zpack（用户定案 2026-11-21）。FinderSync 宿主让位改名为
# /Applications/zpack Finder Menu.app（扩展注册随 pluginkit -a 重登记），
# /Applications/zpack.app 归 GUI 本体。引擎随包注入 Contents/Resources/zpack
# ——服务层 bundleResource 候选命中，安装包自包含。
#
# 陷阱备忘：本机 bash 对「$VAR 后紧跟多字节字符（——／（）等）」会把多字节
# 吸进变量名（报 unbound 且名字带乱码）——所有与多字节相邻的引用一律 ${BRACE}。
set -euo pipefail
cd "$(dirname "$0")"

go tool mygo build

BUNDLE=build/darwin-arm64/zpack.app
REPO_ENGINE=../../bin/zpack
if [ ! -x "${REPO_ENGINE}" ]; then
	echo "缺引擎 ${REPO_ENGINE}，先构建: go build -o bin/zpack ./cmd/zpack" >&2
	exit 1
fi
cp "${REPO_ENGINE}" "${BUNDLE}/Contents/Resources/zpack"
# 引擎是 mygo 签名之后才注入的，还带着 Finder 自定义图标的资源叉——注入后必须重签，
# 否则装出来的包封印失效（codesign --verify 报 detritus / sealed resource invalid）
xattr -cr "${BUNDLE}/Contents/Resources/zpack"
codesign --force --sign - "${BUNDLE}"
codesign --verify --strict "${BUNDLE}"

TARGET="/Applications/zpack.app"
rm -rf "${TARGET}"
ditto "${BUNDLE}" "${TARGET}"
codesign --verify --strict "${TARGET}"
echo "OK 已安装 ${TARGET} (bundleID top.guohub.zpack-gui)"
