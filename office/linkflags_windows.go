//go:build cgo && windows

// office_oxide 链接旗标（Windows/MinGW）：无 libdl（dlfcn 语义内置于系统，MSYS2 不提供
// 该存根库——mac 产线原样带来的 -ldl 在 MinGW 链接必挂，2026-11-08 Windows 实测拆分）。
// -lsynchronization：rustc 1.100 的 std 引用 WaitOnAddress / WakeByAddressSingle / WakeByAddressAll，
// MinGW 运行库里只有它提供（导入自系统自带的 api-ms-win-core-synch-l1-2-0.dll）；rustc 自己
// 链可执行文件时会自动加这个库，但 staticlib 不带链接指令，须由消费者补上（2026-11-24 实测机首跑）
package office

/*
#cgo LDFLAGS: -L${SRCDIR}/libs -loffice_oxide -lntdll -lsynchronization -luserenv -lws2_32 -lbcrypt -lcrypt32 -lgdi32 -luser32 -lm -Wl,-Bstatic -lpthread -Wl,-Bdynamic
*/
import "C"
