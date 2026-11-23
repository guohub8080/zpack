#!/usr/bin/env bash
# 刷新两装机位的内嵌引擎并重签——AGENTS.md「动过引擎代码 → 刷新两装机位」的可执行版。
#
# 为什么必须重签：两个装机位都是已签名的 .app，包内文件事后被改（哪怕只是换一个
# Resources/zpack）封印即失效。Finder 宿主带 Hardened Runtime，封印一坏系统就弃用真身、
# 退回别处注册的旧副本——2026-11-23「右键图标又没了」即此因（旧副本里没有 zpack-menu.icns）。
#
# 用法：bash ui/right-click/macos/refresh-engine.sh      # 引擎取 bin/zpack
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
ENGINE="$ROOT/bin/zpack"
GUI="/Applications/zpack.app"
HOST="/Applications/zpack Finder Menu.app"
HOST_APPEX="$HOST/Contents/PlugIns/zpackSync.appex"
PLUGIN_ID="devplaceholder.UGFOSNW3.zpackFinderMenu.zpackSync"
IDENTITY="${IDENTITY:-$(security find-identity -v -p codesigning 2>/dev/null | grep 'Apple Development' | head -1 | sed 's/.*"\(.*\)"/\1/')}"

# 把引擎放进 .app 的 Resources/zpack。引擎自带 Finder 自定义图标（资源叉），codesign
# 见到即拒签——只清拷贝件的，原件 bin/zpack 的图标不动。
embed_engine() {
  cp "$ENGINE" "$1/Contents/Resources/zpack"
  xattr -cr "$1/Contents/Resources/zpack"
}

[ -x "$ENGINE" ] || { echo "✗ 缺引擎 $ENGINE（先跑产线 build_cli/mac/build.sh）"; exit 1; }

if [ -d "$GUI" ]; then
  embed_engine "$GUI"
  codesign --force --sign - "$GUI"   # GUI 本体是 adhoc 签名
  codesign --verify --strict "$GUI"
  echo "✓ 已刷新 ${GUI}"
fi

if [ -d "$HOST" ]; then
  [ -n "$IDENTITY" ] || { echo "✗ 无 Apple Development 证书，无法重签 Finder 宿主"; exit 1; }
  embed_engine "$HOST"
  # 与 build-xcode.sh 同款签名：Apple Development + Hardened Runtime（appex 未动，无需重签）
  codesign --force --sign "$IDENTITY" --options runtime "$HOST"
  codesign --verify --deep --strict "$HOST"
  echo "✓ 已刷新 ${HOST}"
  # 右键扩展当前生效的必须是真身——否则 Finder 用的是别处的旧副本
  elected="$(pluginkit -mAvv -i "$PLUGIN_ID" 2>/dev/null | sed -n 's/^[[:space:]]*Path = //p' | head -1 || true)"
  if [ "$elected" != "$HOST_APPEX" ]; then
    echo "⚠ 右键扩展当前生效的是别处的副本：${elected:-（无）}——跑 build-xcode.sh 重新部署"
  fi
fi
