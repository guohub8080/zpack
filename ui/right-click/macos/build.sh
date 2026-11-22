#!/usr/bin/env bash
# zpack Finder 右键菜单——构建+装机（macOS）。
# 产物：/Applications/zpack Finder Menu.app（宿主 + FinderSync 扩展 + 内嵌 zpack 二进制 + 图标）
# 装机后需在 系统设置→登录项与扩展→Finder 扩展 勾选「zpack Finder Menu」（脚本会打开该面板）。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
SRC="$ROOT/ui/right-click/macos/src"
BUILD="$ROOT/ui/right-click/macos/build"
APP_NAME="zpack Finder Menu.app"
APP="$BUILD/$APP_NAME"
APPEX="$APP/Contents/PlugIns/zpackSync.appex"
SDK="$(xcrun --show-sdk-path)"
ZPACK_BIN="${ZPACK_BIN:-$ROOT/bin/zpack}"

[ -x "$ZPACK_BIN" ] || { echo "✗ 缺 zpack 二进制：$ZPACK_BIN（先跑产线或 ZPACK_BIN= 指定）"; exit 1; }

rm -rf "$BUILD" && mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$APPEX/Contents/MacOS"

echo "── 编译 ──"
clang -fobjc-arc -isysroot "$SDK" \
    -framework Foundation -framework AppKit \
    "$SRC/host_main.m" -o "$APP/Contents/MacOS/zpackFinderMenu"
# appex 需 main（NSApplicationMain 起进程后由扩展运行时实例化 NSExtensionPrincipalClass）
clang -fobjc-arc -isysroot "$SDK" \
    -framework Foundation -framework AppKit -framework FinderSync \
    "$SRC/ZpackSync.m" "$SRC/host_main.m" -o "$APPEX/Contents/MacOS/zpackSync"

cp "$SRC/host-Info.plist" "$APP/Contents/Info.plist"
cp "$SRC/appex-Info.plist" "$APPEX/Contents/Info.plist"
cp "$ZPACK_BIN" "$APP/Contents/Resources/zpack" && chmod +x "$APP/Contents/Resources/zpack"
cp "$ROOT/assets/zpack.icns" "$APP/Contents/Resources/zpack.icns"

echo "── 签名（ad-hoc）──"
xattr -cr "$APP"   # 资源叉/Finder 信息会让 codesign 拒签（bin/zpack 带自定义图标资源叉）
codesign --force --sign - "$APPEX/Contents/MacOS/zpackSync" >/dev/null 2>&1 || true
codesign --force --sign - "$APPEX" >/dev/null
codesign --force --sign - "$APP" >/dev/null

echo "── 装机 ──"
rm -rf "/Applications/$APP_NAME"
cp -R "$APP" "/Applications/$APP_NAME"
open "/Applications/$APP_NAME"   # 启动一次完成扩展注册
sleep 1
pluginkit -m -p com.apple.FinderSync | grep -i zpack || echo "（pluginkit 未列——先勾选扩展后重查）"

echo "── 打开扩展设置面板 ──"
open "x-apple.systempreferences:com.apple.ExtensionsPreferences"
echo "✓ 完成：在「登录项与扩展 → Finder 扩展」勾选 zpack Finder Menu，然后到任意 Finder 右键验证「zpack ▸」一级菜单"
