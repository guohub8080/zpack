//go:build windows

// 按方法名调用引擎的底层小件：COM 会话、对象创建、按值传参的 IDispatch::Invoke、
// 类型化属性读写、错误收敛。全部建立在 go-ole 之上，不出现任何虚函数表偏移。
package imapi2

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// WithSession COM 会话：钉住 OS 线程 + 初始化 COM。COM 初始化是「每 OS 线程」的——goroutine 被调度器
// 迁到未初始化线程会报错（同 office 引擎 2026-11-08 的修复）；fn 内完成全部引擎调用
func WithSession(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil &&
		!strings.Contains(err.Error(), "S_FALSE") && !strings.Contains(err.Error(), "0x1") {
		return fmt.Errorf("初始化 COM 失败: %w", err)
	}
	defer ole.CoUninitialize()
	return fn()
}

// dispparamsMirror COM DISPPARAMS 的公开布局（oaidl.h）——go-ole 的同名字段未导出，无法直接构造
type dispparamsMirror struct {
	rgvarg            uintptr
	rgdispidNamedArgs uintptr
	cArgs             uint32
	cNamedArgs        uint32
}

// createObject 按 CLSID 创建引擎对象，取其 IDispatch 接口（调用方 Release）
func createObject(clsid string) (*ole.IDispatch, error) {
	unknown, err := ole.CreateInstance(ole.NewGUID(clsid), ole.IID_IDispatch)
	if err != nil {
		return nil, fmt.Errorf("创建刻录引擎对象失败（CLSID %s）: %w", clsid, err)
	}
	return (*ole.IDispatch)(unsafe.Pointer(unknown)), nil
}

// bstrArg 构造字符串参数；返回的释放函数须在调用结束后执行
func bstrArg(text string) (ole.VARIANT, func()) {
	value := ole.SysAllocString(text)
	return ole.NewVariant(ole.VT_BSTR, int64(uintptr(unsafe.Pointer(value)))), func() { _ = ole.SysFreeString(value) }
}

// unknownArg 构造接口指针参数（如 IStream）
func unknownArg(unknown *ole.IUnknown) ole.VARIANT {
	return ole.NewVariant(ole.VT_UNKNOWN, int64(uintptr(unsafe.Pointer(unknown))))
}

// invokeRaw 经 IDispatch::Invoke 按名调用，参数按值传入。go-ole 的调用层对 *VARIANT 只按引用传、
// 且不支持接口指针类型，而引擎的数组参数（MultisessionInterfaces）与流参数（Write / AddFile）要求按值。
// 成功时返回的 VARIANT 由调用方 Clear
func invokeRaw(object *ole.IDispatch, name string, dispatch int, operation string, args ...ole.VARIANT) (*ole.VARIANT, error) {
	displayID, err := object.GetSingleIDOfName(name)
	if err != nil {
		return nil, wrapCall(operation, err)
	}
	// rgvarg 自右向左排列
	ordered := make([]ole.VARIANT, len(args))
	for index, arg := range args {
		ordered[len(args)-1-index] = arg
	}
	namedArg := int32(ole.DISPID_PROPERTYPUT)
	params := dispparamsMirror{cArgs: uint32(len(ordered))}
	if len(ordered) > 0 {
		params.rgvarg = uintptr(unsafe.Pointer(&ordered[0]))
	}
	if dispatch&ole.DISPATCH_PROPERTYPUT != 0 {
		params.rgdispidNamedArgs = uintptr(unsafe.Pointer(&namedArg))
		params.cNamedArgs = 1
	}
	var result ole.VARIANT
	ole.VariantInit(&result)
	var excepInfo ole.EXCEPINFO
	ret, _, _ := syscall.Syscall9(object.VTable().Invoke, 9,
		uintptr(unsafe.Pointer(object)), uintptr(displayID), uintptr(unsafe.Pointer(ole.IID_NULL)),
		uintptr(ole.GetUserDefaultLCID()), uintptr(dispatch),
		uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&result)),
		uintptr(unsafe.Pointer(&excepInfo)), 0)
	if ret != 0 {
		failure := &CallError{Operation: operation, HResult: uint32(ret), SCode: uint32(ret)}
		if excepInfo.SCODE() != 0 {
			failure.SCode = excepInfo.SCODE()
		}
		failure.Message = excepInfoMessage(&excepInfo)
		excepInfo.Clear()
		return nil, failure
	}
	return &result, nil
}

// excepInfoMessage 取 EXCEPINFO 的说明文字；引擎只给了错误码、没有说明时返回空串
func excepInfoMessage(excepInfo *ole.EXCEPINFO) string {
	description := strings.TrimSpace(excepInfo.Error())
	code := excepInfo.SCODE()
	if excepInfo.WCode() != 0 {
		code = uint32(excepInfo.WCode())
	}
	if strings.HasSuffix(description, fmt.Sprintf(": %#x", code)) {
		return ""
	}
	return description
}

// wrapCall 把 go-ole 的调用错误收敛为 CallError（带引擎真实错误码与本地化说明）
func wrapCall(operation string, err error) error {
	if err == nil {
		return nil
	}
	failure := &CallError{Operation: operation}
	var oleErr *ole.OleError
	if errors.As(err, &oleErr) {
		failure.HResult = uint32(oleErr.Code())
		failure.SCode = failure.HResult
		var excepInfo ole.EXCEPINFO
		if subErr := oleErr.SubError(); subErr != nil && errors.As(subErr, &excepInfo) {
			if excepInfo.SCODE() != 0 {
				failure.SCode = excepInfo.SCODE()
			}
			failure.Message = excepInfoMessage(&excepInfo)
		}
	} else {
		failure.Message = err.Error()
	}
	return failure
}

// getString 读字符串属性
func getString(object *ole.IDispatch, name, label string) (string, error) {
	value, err := oleutil.GetProperty(object, name)
	if err != nil {
		return "", wrapCall("读取"+label, err)
	}
	defer func() { _ = value.Clear() }()
	return strings.TrimSpace(value.ToString()), nil
}

// getInt 读整数属性
func getInt(object *ole.IDispatch, name, label string) (int32, error) {
	value, err := oleutil.GetProperty(object, name)
	if err != nil {
		return 0, wrapCall("读取"+label, err)
	}
	defer func() { _ = value.Clear() }()
	number, ok := value.Value().(int32)
	if !ok {
		return 0, fmt.Errorf("读取%s失败: 引擎返回了非整数", label)
	}
	return number, nil
}

// getBool 读布尔属性
func getBool(object *ole.IDispatch, name, label string) (bool, error) {
	value, err := oleutil.GetProperty(object, name)
	if err != nil {
		return false, wrapCall("读取"+label, err)
	}
	defer func() { _ = value.Clear() }()
	flag, ok := value.Value().(bool)
	if !ok {
		return false, fmt.Errorf("读取%s失败: 引擎返回了非布尔值", label)
	}
	return flag, nil
}

// readArray 读数组属性为通用值切片（元素为 int32 / string 等）
func readArray(object *ole.IDispatch, name, label string) ([]interface{}, error) {
	value, err := oleutil.GetProperty(object, name)
	if err != nil {
		return nil, wrapCall("读取"+label, err)
	}
	defer func() { _ = value.Clear() }()
	if value.VT&ole.VT_ARRAY == 0 {
		return []interface{}{}, nil
	}
	conversion := value.ToArray()
	if conversion == nil {
		return []interface{}{}, nil
	}
	return conversion.ToValueArray(), nil
}

// takeDispatch 从属性值里取出接口指针（AddRef 后交给调用方 Release）并释放属性值
func takeDispatch(value *ole.VARIANT, err error, operation string) (*ole.IDispatch, error) {
	if err != nil {
		return nil, wrapCall(operation, err)
	}
	defer func() { _ = value.Clear() }()
	dispatch := value.ToIDispatch()
	if dispatch == nil {
		return nil, fmt.Errorf("%s失败: 引擎未返回对象", operation)
	}
	dispatch.AddRef()
	return dispatch, nil
}

// takeUnknown 同 takeDispatch，取 IUnknown 形态的接口指针（如 IStream）
func takeUnknown(value *ole.VARIANT, err error, operation string) (*ole.IUnknown, error) {
	if err != nil {
		return nil, wrapCall(operation, err)
	}
	defer func() { _ = value.Clear() }()
	var unknown *ole.IUnknown
	switch value.VT {
	case ole.VT_UNKNOWN:
		unknown = value.ToIUnknown()
	case ole.VT_DISPATCH:
		unknown = (*ole.IUnknown)(unsafe.Pointer(value.ToIDispatch()))
	}
	if unknown == nil {
		return nil, fmt.Errorf("%s失败: 引擎未返回对象", operation)
	}
	unknown.AddRef()
	return unknown, nil
}

// putValue 写属性
func putValue(object *ole.IDispatch, name, label string, value interface{}) error {
	result, err := oleutil.PutProperty(object, name, value)
	if err != nil {
		return wrapCall("设置"+label, err)
	}
	_ = result.Clear()
	return nil
}

// callVoid 调用无返回值的方法
func callVoid(object *ole.IDispatch, name, label string, params ...interface{}) error {
	result, err := oleutil.CallMethod(object, name, params...)
	if err != nil {
		return wrapCall(label, err)
	}
	_ = result.Clear()
	return nil
}

// callBool 调用返回布尔值的方法
func callBool(object *ole.IDispatch, name, label string, params ...interface{}) (bool, error) {
	value, err := oleutil.CallMethod(object, name, params...)
	if err != nil {
		return false, wrapCall(label, err)
	}
	defer func() { _ = value.Clear() }()
	flag, ok := value.Value().(bool)
	if !ok {
		return false, fmt.Errorf("%s失败: 引擎返回了非布尔值", label)
	}
	return flag, nil
}
