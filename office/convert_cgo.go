//go:build cgo

// Office 文档转换（office_oxide Rust 核心，C ABI 静态链入）。
// v1 范围：doc/wps → docx（.wps = 现代 WPS 的 doc 同构文件——魔数识别 OLE2）。
package office

/*
#include <stdlib.h>
#include <stdint.h>
#include "office_oxide.h"
#cgo CFLAGS: -I${SRCDIR}/libs
#cgo LDFLAGS: -L${SRCDIR}/libs -loffice_oxide -lm -ldl -lpthread
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// ErrNeedsFullBuild 纯构建桩语义（cgo 构建恒可用）
var ErrNeedsFullBuild = errors.New("office 转换需完整版（CGO 构建 + office 产线）")

// ConvertResult 转换结果（信封 data 载荷）
type ConvertResult struct {
	Input        string `json:"input"`
	Output       string `json:"output"`
	SourceFormat string `json:"source_format"` // 实际识别（doc/wps→doc）
	TargetFormat string `json:"target_format"`
	SizeBytes    int64  `json:"size_bytes"`
}

// OLE2_MAGIC OLE2 复合文档魔数（doc/xls/ppt/wps 共用的容器头）
var OLE2_MAGIC = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// Available 完整版可用
func Available() bool { return true }

// Convert 旧格式 → OOXML（doc/wps→docx、xls/et→xlsx、ppt/dps→pptx）
func Convert(input, output string) (*ConvertResult, error) {
	inputExt := strings.ToLower(filepath.Ext(input))
	outputExt := strings.ToLower(filepath.Ext(output))
	outputFormat, outputOK := map[string]string{".docx": "docx", ".xlsx": "xlsx", ".pptx": "pptx"}[outputExt]
	if !outputOK {
		return nil, fmt.Errorf("目标暂只支持 .docx/.xlsx/.pptx，收到: %s", outputExt)
	}
	// 族匹配：doc 族只出 docx（跨族转换拒绝——office_oxide 会尽力转出错误产物）
	if familyOutput, hasFamily := OFFICE_FAMILY[inputExt]; hasFamily && familyOutput != outputExt {
		return nil, fmt.Errorf("跨族转换不支持：%s → %s（%s 族只出 %s）", inputExt, outputExt, strings.TrimPrefix(LEGACY_OFFICE_INPUTS[inputExt], LEGACY_OFFICE_INPUTS[inputExt][:0]), familyOutput)
	}
	if _, statErr := os.Stat(input); statErr != nil {
		return nil, fmt.Errorf("路径不存在: %s", input)
	}

	handle, sourceFormat, openErr := openDocument(input, inputExt)
	if openErr != nil {
		return nil, openErr
	}
	defer C.office_document_free(handle)

	status := C.office_document_save_as(handle, C.CString(output), nil)
	if status != 0 {
		os.Remove(output) // 清理半成品
		return nil, fmt.Errorf("转换失败（office_oxide 错误码 %d）——文件可能损坏或含不支持的特性", int32(status))
	}
	outputInfo, _ := os.Stat(output)
	return &ConvertResult{
		Input:        absPath(input),
		Output:       absPath(output),
		SourceFormat: sourceFormat,
		TargetFormat: outputFormat,
		SizeBytes:    outputInfo.Size(),
	}, nil
}

// openDocument 打开文档句柄：WPS 系扩展名（.wps/.et/.dps）走字节级（现代 WPS=MS 旧格式同构，
// 魔数 OLE2 校验后按对应格式解析）；MS 原生扩展名走路径级。返回实际识别的源格式名。
func openDocument(input, inputExt string) (*C.OfficeDocumentHandle, string, error) {
	coreFormat, known := LEGACY_OFFICE_INPUTS[inputExt]
	if !known {
		return nil, "", fmt.Errorf("输入暂只支持 .doc/.wps/.xls/.et/.ppt/.dps，收到: %s", inputExt)
	}
	isWpsAlias := inputExt == ".wps" || inputExt == ".et" || inputExt == ".dps"
	if isWpsAlias {
		data, readErr := os.ReadFile(input)
		if readErr != nil {
			return nil, "", fmt.Errorf("读取失败: %w", readErr)
		}
		if len(data) < 8 || string(data[:8]) != string(OLE2_MAGIC) {
			return nil, "", errors.New("非现代 WPS 文件（WPS 私有二进制不支持——请用 WPS Office 另存为对应 MS 格式）")
		}
		handle := openFromBytes(data, coreFormat)
		if handle == nil {
			return nil, "", fmt.Errorf("WPS 文件按 %s 解析失败——可能是 WPS 私有格式（用 WPS Office 另存）", coreFormat)
		}
		return handle, coreFormat + "(" + inputExt[1:] + ")", nil
	}
	pathC := C.CString(input)
	defer C.free(unsafe.Pointer(pathC))
	handle := C.office_document_open(pathC, nil)
	if handle == nil {
		return nil, "", fmt.Errorf("无法读取 %s——文件损坏或非 %s 二进制格式", inputExt, coreFormat)
	}
	return handle, coreFormat, nil
}

// openFromBytes 字节级打开（format: doc/docx/xls/...）
func openFromBytes(data []byte, format string) *C.OfficeDocumentHandle {
	formatC := C.CString(format)
	defer C.free(unsafe.Pointer(formatC))
	return C.office_document_open_from_bytes(
		(*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), formatC, nil)
}

func absPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}
