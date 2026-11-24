#!/usr/bin/env bash
# zpack Finder 右键菜单——Quick Action 轨（免签名，立即生效）。
# 产出 ~/Library/Services/ 三服务：zpack 压缩 / zpack 解压 / zpack 测试
# （Finder 右键 → 快速操作；wflow=真样例模板经 plistlib 改参——杜绝手写 XML 转义）。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
HERE="$ROOT/ui/right-click/macos"
SERVICES="$HOME/Library/Services"
# 2026-11-24 引擎改名 zpack-cli；改名之前装的宿主里还是旧名 zpack，两个都认
HOST_RESOURCES="/Applications/zpack Finder Menu.app/Contents/Resources"
ZPACK_EMBEDDED="$HOST_RESOURCES/zpack-cli"
[ -x "${ZPACK_EMBEDDED}" ] || ZPACK_EMBEDDED="$HOST_RESOURCES/zpack"
ZPACK_BIN="${ZPACK_BIN:-$([ -x "$ZPACK_EMBEDDED" ] && echo "$ZPACK_EMBEDDED" || echo "$ROOT/bin/zpack-cli")}"
[ -x "$ZPACK_BIN" ] || { echo "✗ 缺 zpack 二进制"; exit 1; }

# 服务动作脚本（Run Shell Script 以 argv 收路径；heredoc 仅做 bash 层转义——\$ 运行期展开）
make_runner() { # 参数：命令名
  cat <<RUNNER
ZPACK="\$( [ -x "$ZPACK_EMBEDDED" ] && echo "$ZPACK_EMBEDDED" || echo "$ZPACK_BIN" )"
for item in "\$@"; do
  dir="\$(dirname "\$item")"
  case "$1" in
    压缩)  cd "\$dir" && out="\$("\$ZPACK" compress "\$item" --output "\$(basename "\$item").zip" 2>/dev/null)" ;;
    解压)  cd "\$dir" && out="\$("\$ZPACK" extract "\$item" 2>/dev/null)" ;;
    测试)  cd "\$dir" && out="\$("\$ZPACK" test "\$item" 2>/dev/null)" ;;
  esac
  if echo "\$out" | grep -q '"is_success":true'; then
    note="\$(echo "\$out" | grep -o '"archive":"[^"]*"\|"output":"[^"]*"' | head -1 | cut -d'"' -f4)"
    [ -z "\$note" ] && note="\$item"
    osascript -e "display notification \"\$note\" with title \"zpack $1 成功\""
  else
    reason="\$(echo "\$out" | grep -o '"error_info":"[^"]*"' | head -1 | cut -d'"' -f4)"
    [ -z "\$reason" ] && reason="\$item"
    osascript -e "display notification \"\$reason\" with title \"zpack $1 失败\""
  fi
done
RUNNER
}

# 服务 Info.plist（Finder 文件右键可见）
make_service_plist() {
  cat <<SPLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>NSServices</key>
	<array>
		<dict>
			<key>NSMenuItem</key><dict><key>default</key><string>zpack $1</string></dict>
			<key>NSMessage</key><string>runWorkflowAsService</string>
			<key>NSRequiredContext</key><dict><key>NSTextContent</key><string>FilePath</string></dict>
			<key>NSSendFileTypes</key><array><string>public.item</string></array>
			<key>NSServiceDescription</key><string>zpack $1（压缩解压工具箱）</string>
		</dict>
	</array>
</dict>
</plist>
SPLIST
}

mkdir -p "$SERVICES"
for verb in 压缩 解压 测试; do
  NAME="zpack $verb"
  WF="$SERVICES/$NAME.workflow"
  rm -rf "$WF"
  mkdir -p "$WF/Contents"
  make_service_plist "$verb" > "$WF/Contents/Info.plist"
  # 模板克隆：plistlib 改参（inputMethod=1 即 argv 传参；COMMAND_STRING=脚本本体）
  ZPACK_RUNNER="$(make_runner "$verb")" python3 - "$HERE/src/document.wflow.template" "$WF/Contents/document.wflow" <<'PYEOF'
import os, plistlib, sys
template_path, out_path = sys.argv[1], sys.argv[2]
with open(template_path, "rb") as f:
    wf = plistlib.load(f)
params = wf["actions"][0]["action"]["ActionParameters"]
params["COMMAND_STRING"] = os.environ["ZPACK_RUNNER"]
params["inputMethod"] = 1          # 0=stdin, 1=作为 argv
params["shell"] = "/bin/bash"
params["CheckedForUserDefaultShell"] = True
with open(out_path, "wb") as f:
    plistlib.dump(wf, f)
PYEOF
  plutil -lint "$WF/Contents/Info.plist" "$WF/Contents/document.wflow" >/dev/null
  echo "✓ $NAME"
done

killall pbs 2>/dev/null || true   # Services 菜单守护刷新
echo "✓ 装机完成：Finder 右键 → 快速操作/服务 → zpack 压缩/解压/测试"
