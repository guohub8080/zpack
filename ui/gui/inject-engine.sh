#!/usr/bin/env bash
# 把引擎交给 GUI 打包器随包分发（Windows / Linux）：拷进 ui/gui/resources/<平台>-<架构>/zpack-cli[.exe]。
# `go tool mygo build` 会把该目录里的文件放到 GUI 可执行文件旁边、一并打进安装包（mygo v0.10.0 实测，
# 2026-11-24 Windows：resources/windows-amd64/zpack-cli.exe 出现在 build/windows-amd64/ 里）；
# GUI 启动后按「与 GUI 同目录」找到它（ui/gui/service.go 的 engineCandidates）。
#
# 2026-11-24 用户定案引擎改名 zpack-cli：GUI 自己叫 zpack，引擎叫 zpack-cli——同目录候选不再等于 GUI 自己，
# 这条注入通道才走得通。macOS 不走这里：引擎是在 mygo 签名之后才注入的，见 install.sh。
#
# 用法：bash ui/gui/inject-engine.sh                 # 当前平台，目录名按 mygo 的约定（windows-amd64 / linux-amd64…）
#       bash ui/gui/inject-engine.sh linux-arm64     # 跨平台打包时显式给 mygo 的平台目录名
#       bash ui/gui/inject-engine.sh --remove        # 撤掉注入（不带包了）
# 引擎取仓库 bin/zpack-cli[.exe]（产线产物），改名之前铸的旧名 bin/zpack[.exe] 也认；都缺就提示先跑产线。
# 引擎不入库：.gitignore 挡 ui/gui/resources/*/zpack-cli*
set -euo pipefail
cd "$(dirname "$0")"

ENGINE_NAME="zpack-cli"
case "${1:-}" in
  --remove)
    rm -f resources/*/zpack-cli resources/*/zpack-cli.exe
    echo "✓ 已撤掉 resources/*/zpack-cli*"
    exit 0
    ;;
  "" )
    case "$(uname -s)" in
      Darwin)
        echo "macOS 不用这个脚本：引擎在 mygo 签名之后才注入，走 bash ui/gui/install.sh" >&2
        exit 1
        ;;
      Linux) platform="linux" ;;
      *) platform="windows" ;;
    esac
    case "$(uname -m)" in
      arm64|aarch64) platform="${platform}-arm64" ;;
      *) platform="${platform}-amd64" ;;
    esac
    ;;
  * )
    platform="$1"
    ;;
esac

suffix=""
case "$platform" in
  windows*) suffix=".exe" ;;
esac

engine="../../bin/${ENGINE_NAME}${suffix}"
# 2026-11-24 引擎改名；改名之前铸的旧名产物也认
if [ ! -f "${engine}" ] && [ -f "../../bin/zpack${suffix}" ]; then
  engine="../../bin/zpack${suffix}"
fi
if [ ! -f "${engine}" ]; then
  echo "✗ 缺引擎 bin/${ENGINE_NAME}${suffix}——先跑产线铸出完整版（build_cli/<平台>/build.sh），开发版的 zpack-dev 不能装机" >&2
  exit 1
fi

target="resources/${platform}/${ENGINE_NAME}${suffix}"
mkdir -p "resources/${platform}"
cp "${engine}" "${target}"
echo "✓ 已注入 ${target}（$(du -h "${target}" | cut -f1)）——接着 go tool mygo build，引擎会随包分发到 GUI 可执行文件旁边"
