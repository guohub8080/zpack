//go:build cgo && windows

// office_oxide 链接旗标（Windows/MinGW）：无 libdl（dlfcn 语义内置于系统，MSYS2 不提供
// 该存根库——mac 产线原样带来的 -ldl 在 MinGW 链接必挂，2026-11-08 Windows 实测拆分）。
package office

/*
#cgo LDFLAGS: -L${SRCDIR}/libs -loffice_oxide -lntdll -luserenv -lws2_32 -lbcrypt -lcrypt32 -lgdi32 -luser32 -lm -Wl,-Bstatic -lpthread -Wl,-Bdynamic
*/
import "C"
