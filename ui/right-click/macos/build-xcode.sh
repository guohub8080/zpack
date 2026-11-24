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
INSTALLED="/Applications/zpack Finder Menu.app"
INSTALLED_APPEX="$INSTALLED/Contents/PlugIns/zpackSync.appex"
HOST_ID="devplaceholder.UGFOSNW3.zpack"
APPEX_ID="devplaceholder.UGFOSNW3.zpackFinderMenu.zpackSync"

# pluginkit 里该扩展 ID 名下当选的 appex 路径（同一 ID 的条目互斥，只有一条）。
elected_path() { pluginkit -mAvv -i "$APPEX_ID" 2>/dev/null | sed -n 's/^[[:space:]]*Path = //p' | head -1 || true; }

# LaunchServices 里登记在这两个 bundle id 名下的全部包路径（宿主 .app 与 appex 各一条记录）。
# pkd 每次 Finder 重启都与它重新同步——这里登记着谁，右键菜单就用谁。
ls_records() {
  "$LSREG" -dump 2>/dev/null | awk -v host="$HOST_ID" -v appex="$APPEX_ID" '
    /^path:/ { path = $0; sub(/^path:[[:space:]]*/, "", path); sub(/ \(0x[0-9a-f]+\)$/, "", path) }
    /^identifier:/ { id = $0; sub(/^identifier:[[:space:]]*/, "", id); sub(/[[:space:]]+$/, "", id)
      if (id == host || id == appex) print path }'
}

[ -z "$IDENTITY" ] && { echo "✗ 无 Apple Development 证书（Xcode → Settings → Accounts）"; exit 1; }
ENGINE="$ROOT/bin/zpack-cli"
# 2026-11-24 引擎改名 zpack-cli；改名之前铸的旧名产物也认
[ -x "${ENGINE}" ] || ENGINE="$ROOT/bin/zpack"
[ -x "${ENGINE}" ] || { echo "→ 先构建 Go 引擎..."; cd "$ROOT" && bash build_cli/mac/build.sh >/dev/null 2>&1; }

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
cp "$ROOT/bin/zpack-cli" "$APP/Contents/Resources/zpack"
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
xattr -cr "$APP"  # 清资源叉（bin/zpack-cli 的 Finder 自定义图标会让 codesign 拒签）

echo "── 签名（先内后外；appex 必带 --options runtime——裸签抹掉 Hardened Runtime=macOS 26 拒载）──"
codesign --force --sign "$IDENTITY" --options runtime --preserve-metadata=entitlements \
  "$APP/Contents/PlugIns/zpackSync.appex"
codesign --force --sign "$IDENTITY" --options runtime "$APP"
# 封印必须完好：包内任何文件事后被改（如原地替换引擎）封印即失效，系统随即弃用真身
codesign --verify --deep --strict "$APP"

echo "── 装机 ──"
# 同一扩展 ID 名下只留唯一真身。旧副本（Xcode 图形界面的 DerivedData 产物、仓库内构建产物……）
# 没有菜单图标，被系统选中就是「右键消失 / 图标没了」。要拔两处：
# ① LaunchServices——pkd 每次 Finder 重启都与它重新同步，只拔 pluginkit 会被它捞回来；
#    appex 记录随宿主一并注销。
# ② pluginkit——同一 ID 的条目互斥。
stale_hosts=""
while IFS= read -r stale; do
  case "$stale" in ""|"$INSTALLED"|"$INSTALLED_APPEX") continue ;; esac
  stale="${stale%/Contents/PlugIns/*.appex}"
  case "$stale_hosts" in *"|$stale|"*) continue ;; esac   # 宿主与 appex 两条记录折叠成同一宿主
  stale_hosts="$stale_hosts|$stale|"
  echo "   注销旧宿主：$stale"
  "$LSREG" -u "$stale" >/dev/null 2>&1 || true
done < <(ls_records)
while IFS= read -r stale; do
  [ -n "$stale" ] || continue
  pluginkit -r "$stale" >/dev/null 2>&1 || true
done < <(pluginkit -mAvv -i "$APPEX_ID" 2>/dev/null | sed -n 's/^[[:space:]]*Path = //p')
pkill -f zpackSync 2>/dev/null || true
pkill -f "/Applications/zpack Finder Menu.app" 2>/dev/null || true
sleep 1
rm -rf "$INSTALLED"
cp -R "$APP" "$INSTALLED"
# 不裸 open 宿主——宿主 5 秒无任务文件即退（裸启动常驻会吞下一单）；
# 扩展注册走 pluginkit 直注（与宿主进程无关）
# 真身 appex 要在 LaunchServices 有自己的记录，pkd 重同步时才认它（对 appex 路径 lsregister
# 会报一句 failed to scan -10811 并返回非零，但登记已生效——下面按结果断言）
"$LSREG" -f "$INSTALLED" >/dev/null 2>&1 || true
"$LSREG" -f "$INSTALLED_APPEX" >/dev/null 2>&1 || true
pluginkit -a "$INSTALLED_APPEX"
killall Finder 2>/dev/null || true

# 验收一：LaunchServices 里这两个 bundle id 名下只剩真身（pkd 重同步的唯一来源）
while IFS= read -r record; do
  case "$record" in ""|"$INSTALLED"|"$INSTALLED_APPEX") continue ;; esac
  echo "✗ LaunchServices 里还有旧宿主：$record（pkd 重同步会把它捞回来）"
  exit 1
done < <(ls_records)
if ! ls_records | grep -qxF "$INSTALLED_APPEX"; then
  echo "✗ 真身 appex 没登记进 LaunchServices"; exit 1
fi
# 验收二：Finder 重启后当选路径稳定在真身（连查两次，防延迟回退）
for attempt in 1 2; do
  sleep 3
  if [ "$(elected_path)" != "$INSTALLED_APPEX" ]; then
    echo "✗ 扩展注册没落在真身，当选的是：$(elected_path)"
    exit 1
  fi
done
# 验收三：真身封印完好（否则系统弃用它、退回别处的旧副本）
codesign --verify --deep --strict "$INSTALLED"
# 验收四：扩展已启用
if pluginkit -mAD -p com.apple.FinderSync 2>&1 | grep -q "^\+.*zpackSync"; then
  echo "✓ 完成 — Finder 右键即可见「zpack ▸」（菜单点按=宿主跑引擎+原生弹窗）"
else
  echo "⚠ 需手动启用：系统设置 → 登录项与扩展 → Finder → 勾选 zpack"
fi
