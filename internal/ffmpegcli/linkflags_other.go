//go:build cgo && !windows

// ffmpeg 直调库链接旗标（mac/linux）。
package ffmpegcli

/*
#cgo LDFLAGS: -L${SRCDIR}/libs -lzpack_ffmpeg_cli -lz -lm -lpthread -liconv
*/
import "C"
