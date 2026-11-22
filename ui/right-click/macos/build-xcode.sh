#!/usr/bin/env bash
# zpack Finder 右键菜单——统一构建（Go 引擎 + Xcode FinderSync 壳）
# 产物：/Applications/zpack Finder Menu.app（含内嵌 zpack 二进制 + 双图标 + FinderSync 扩展）
# 架构（2026-11-21 全链实测定案，详见 AGENTS.md 右键菜单节）：
#   appex（沙箱+硬化）只管菜单 → 任务文件写自家容器 → LS 拉起宿主（非沙箱）干活+弹窗
# 前置：钥匙串有 Apple Development 证书（免费 Apple ID 即可）
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
XCODE_DIR="$ROOT/ui/right-click/macos/xcode"
BUILD_DIR="$XCODE_DIR/build"
IDENTITY="${IDENTITY:-$(security find-identity -v -p codesigning 2>/dev/null | grep 'Apple Development' | head -1 | sed 's/.*"\(.*\)"/\1/')}"
TEAM="${TEAM:-7LB3A9H742}"
LSREG="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

[ -z "$IDENTITY" ] && { echo "✗ 无 Apple Development 证书（Xcode → Settings → Accounts）"; exit 1; }
[ -x "$ROOT/bin/zpack" ] || { echo "→ 先构建 Go 引擎..."; cd "$ROOT" && bash build_cli/mac/build.sh >/dev/null 2>&1; }

echo "── 构建 FinderSync 扩展（Xcode）──"
cd "$XCODE_DIR"
xcodebuild -project zpackFinderMenu.xcodeproj \
  -scheme zpackFinderMenu \
  -configuration Release \
  -derivedDataPath build \
  DEVELOPMENT_TEAM="$TEAM" \
  build 2>&1 | grep -E "^Build|error:" | head -3

APP="$BUILD_DIR/Build/Products/Release/zpack.app"   # PRODUCT_NAME=zpack
[ -d "$APP" ] || { echo "✗ 构建失败"; exit 1; }

echo "── 嵌入 Go 引擎 + 双图标 ──"
# 双图标各司其职：zpack.icns=app 本体（squircle 底板 80.5% 内缩+透明边距）/ zpack-menu.icns=右键菜单（透明底造型）
# 陷阱备忘：全新 xcodebuild 产物 appex 无 Contents/Resources 目录（历史靠增量残留）——cp 前必须 mkdir -p
mkdir -p "$APP/Contents/PlugIns/zpackSync.appex/Contents/Resources"
cp "$ROOT/bin/zpack" "$APP/Contents/Resources/zpack"
chmod +x "$APP/Contents/Resources/zpack"
cp "$ROOT/assets/zpack.icns" "$APP/Contents/Resources/"
cp "$ROOT/assets/zpack-menu.icns" "$APP/Contents/Resources/"
cp "$ROOT/assets/zpack-menu.icns" "$APP/Contents/PlugIns/zpackSync.appex/Contents/Resources/"
python3 - "$APP" <<'PYEOF'
import plistlib, sys
app = sys.argv[1]
for path in (f"{app}/Contents/Info.plist",
             f"{app}/Contents/PlugIns/zpackSync.appex/Contents/Info.plist"):
    with open(path, "rb") as f:
        pl = plistlib.load(f)
    pl["CFBundleIconFile"] = "zpack"
    pl["CFBundleIconName"] = "zpack"
    with open(path, "wb") as f:
        plistlib.dump(pl, f)
print("✓ Info.plist 图标声明")
PYEOF
xattr -cr "$APP"  # 清资源叉（bin/zpack 的 Finder 自定义图标会让 codesign 拒签）

echo "── 签名（先内后外；appex 必带 --options runtime——裸签抹掉 Hardened Runtime=macOS 26 拒载）──"
codesign --force --sign "$IDENTITY" --options runtime --preserve-metadata=entitlements \
  "$APP/Contents/PlugIns/zpackSync.appex" 2>/dev/null
codesign --force --sign "$IDENTITY" --options runtime "$APP" 2>/dev/null

echo "── 装机 ──"
# 拔干净历史注册再装唯一真身（DerivedData 假注册同 ID 打架=右键静默消失元凶）
pluginkit -r "$APP/Contents/PlugIns/zpackSync.appex" 2>/dev/null || true
pkill -f zpackSync 2>/dev/null || true
pkill -f "/Applications/zpack Finder Menu.app" 2>/dev/null || true
sleep 1
rm -rf "/Applications/zpack Finder Menu.app"
cp -R "$APP" "/Applications/zpack Finder Menu.app"
# 不裸 open 宿主——宿主 5 秒无任务文件即退（裸启动常驻会吞下一单）；
# 扩展注册走 pluginkit 直注（与宿主进程无关）
pluginkit -a "/Applications/zpack Finder Menu.app/Contents/PlugIns/zpackSync.appex" 2>/dev/null || true
"$LSREG" -f "/Applications/zpack Finder Menu.app"
killall Finder 2>/dev/null || true

if pluginkit -mAD -p com.apple.FinderSync 2>&1 | grep -q "^\+.*zpackSync"; then
  echo "✓ 完成 — Finder 右键即可见「zpack ▸」（菜单点按=宿主跑引擎+原生弹窗）"
else
  echo "⚠ 需手动启用：系统设置 → 登录项与扩展 → Finder → 勾选 zpack"
fi
