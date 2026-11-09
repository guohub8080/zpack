//go:build cgo && windows

// ffmpeg 直调库链接旗标（Windows/MinGW）：zlib 钉静态——MinGW ld 默认优先 libz.dll.a，
// 会把 MSYS2 的 libz-1.dll 带进 import 表违反「仅出厂 DLL」铁律（2026-11-09 产线首跑发现）。
package ffmpegcli

/*
#cgo LDFLAGS: -L${SRCDIR}/libs -lzpack_ffmpeg_cli -Wl,-Bstatic -lz -Wl,-Bdynamic -lm -Wl,-Bstatic -lpthread -liconv -Wl,-Bdynamic
*/
import "C"
