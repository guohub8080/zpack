#!/usr/bin/env bash
# Windows 完整版产线总入口（对齐 mac/build.sh 形态；2026-11-09 定案，zig cc 交叉路线退役）：
#   bash build_cli/windows/build.sh        # 全流程：编码器全家 → FFmpeg n9.0 → office_oxide → cgo 组装 → DLL 校验
#   bash build_cli/windows/build.sh deps   # 仅依赖产线（prefix 幂等缓存，重跑秒过）
#   bash build_cli/windows/build.sh build  # 仅 cgo 组装
# 前置（一次性）：MSYS2/UCRT64 工具链——
#   winget install MSYS2.MSYS2 后（C:\msys64）：
#   /c/msys64/usr/bin/bash -lc "pacman -Syu"  # 首次升级会自杀终端，属预期，再跑下行
#   /c/msys64/usr/bin/bash -lc "pacman -S --needed git make perl diffutils mingw-w64-ucrt-x86_64-{toolchain,cmake,nasm,pkgconf,libiconv,rust}"
# 零解压残留：所有源码树在 mktemp 临时目录、退出即清（KEEP_WORK=1 保留调试现场）；
# 仓库常驻仅脚本与 prefix 缓存（.gitignore）。
# 网络：直连慢时 export https_proxy=...（本机代理 127.0.0.1:7897；GitHub Actions 无需）。
# 产物：bin/zpack.exe 完整版（ffmpeg 直调/media/audio/HDR/office 内置全开，仅系统出厂 DLL）。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
PREFIX="$HERE/prefix"
MODE="${1:-all}"

export PATH="/ucrt64/bin:/c/Program Files/Go/bin:$PATH"

# MSYS2 login shell 不继承 Windows 用户环境（陷阱 #22）：go 找不到 GOCACHE/GOENV。三项有缺时按
# 「系统盘:\Users\<当前用户>」推导；推导不出（用户目录被挪过位置）就明确报错让调用方 export——
# 此前这里写死另一台机器的用户目录（C:\Users\pop），换机即指向不存在的路径（2026-11-24 换机实测）
if [ -z "${LOCALAPPDATA:-}" ] || [ -z "${USERPROFILE:-}" ] || [ -z "${APPDATA:-}" ]; then
	profileName="$(whoami)"
	if [ ! -d "/c/Users/$profileName/AppData/Local" ]; then
		echo "✗ 无法定位用户目录：LOCALAPPDATA / USERPROFILE / APPDATA 未设置，且 /c/Users/$profileName 下没有 AppData/Local——请显式 export 后重跑" >&2
		exit 1
	fi
	profileDir="$(cygpath -w "/c/Users/$profileName")"
	export USERPROFILE="${USERPROFILE:-$profileDir}"
	export LOCALAPPDATA="${LOCALAPPDATA:-$profileDir\\AppData\\Local}"
	export APPDATA="${APPDATA:-$profileDir\\AppData\\Roaming}"
fi
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

cd "$ROOT"

case "$MODE" in
deps|all)
  if [ -f "$PREFIX/lib/libavcodec.a" ] && [ -f "$PREFIX/lib/libzpack_ffmpeg_cli.a" ]; then
    echo "✓ deps prefix 已就绪（跳过）"
  else
    bash "$HERE/deps_static.sh"
  fi
  if [ -f office/libs/liboffice_oxide.a ]; then
    echo "✓ office_oxide 已就绪（跳过）"
  else
    bash "$HERE/office_static.sh"
  fi
  ;;
esac

case "$MODE" in
build|all)
  export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"
  export CGO_ENABLED=1
  mkdir -p internal/ffmpegcli/libs
  # cgo 的 pkg-config 结果进构建缓存且不随 PKG_CONFIG_PATH 失效（mac 陷阱 #7）——切前缀后必须清
  go clean -cache
  # -extldflags 追加在链接行末尾：静态 libstdc++.a（libavfilter）自身引用 pthread_*，
  # 解铃 -lpthread 须在其后——各包 #cgo 段顺序无法保证，尾部统一钉静态（陷阱 #19）
  go build -ldflags "-s -w -X main.version=$(git rev-parse --short HEAD)-full -extldflags '-Wl,-Bstatic -lpthread -Wl,-Bdynamic'" \
    -o bin/zpack.exe ./cmd/zpack

  echo "── DLL 依赖校验（铁律 2：仅系统出厂 DLL）──"
  BAD_DLL=$(objdump -p bin/zpack.exe | grep "DLL Name" | grep -viE "kernel32|ntdll|user32|advapi32|ws2_32|secur32|shlwapi|shell32|ole32|oleaut32|uuid|psapi|gdi32|vfw32|avicap32|userenv|bcrypt|crypt32|mfuuid|strmiids|ncrypt|api-ms-win" || true)
  if [ -n "$BAD_DLL" ]; then
    echo "✗ 非出厂 DLL 入链：$BAD_DLL"
    exit 1
  fi
  echo "✓ DLL 全部系统出厂"
  echo "✓ $ROOT/bin/zpack.exe（完整版 $(du -h bin/zpack.exe | cut -f1)）"
  ;;
deps) ;; # 仅依赖产线到此为止——此前落入 *) 分支恒 exit 1，云端产线从未跑到 cgo 阶段（2026-11-24 实测机首跑查明）
*) echo "用法: $0 [deps|build|all]" >&2; exit 1 ;;
esac
