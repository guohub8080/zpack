//go:build cgo && !windows

// office_oxide 链接旗标（mac/linux）：-ldl 为 unix 动态加载库。
package office

/*
#cgo LDFLAGS: -L${SRCDIR}/libs -loffice_oxide -lm -ldl -lpthread
*/
import "C"
