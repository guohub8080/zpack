//go:build windows

// Windows 引擎层：MS Office 与 WPS 均走 COM 自动化（go-ole 纯 syscall——无 cgo）。
// Word.Application / KWPS.Application 同构 API（WPS 兼容 Word COM 面）。
// 2026-11-08 Windows 首实测修复：COM apartment 初始化是「每 OS 线程」的，goroutine 会被
// Go 调度器迁到未初始化线程上（报「尚未调用 CoInitialize」）——全程 LockOSThread 钉线程。
package office

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// withCOMSession COM 会话骨架：钉住 OS 线程 + 初始化 COM + 用毕逆序释放（S_FALSE=本线程
// 已初始化也算成功，配对 CoUninitialize 维持引用计数平衡）
func withCOMSession(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initCOM(); err != nil {
		return err
	}
	defer ole.CoUninitialize()
	return fn()
}

// convertViaCOMWordLike COM 自动化（progID: Word.Application / KWPS.Application）
func convertViaCOMWordLike(input, output, progID, suiteName string) error {
	return withCOMSession(func() error {
		return convertWordLikeInner(input, output, progID, suiteName)
	})
}

func convertWordLikeInner(input, output, progID, suiteName string) error {
	appObj, createErr := oleutil.CreateObject(progID)
	if createErr != nil {
		return fmt.Errorf("未检测到 %s（COM 创建 %s 失败）——安装后重试", suiteName, progID)
	}
	defer appObj.Release()

	app, queryErr := appObj.QueryInterface(ole.IID_IDispatch)
	if queryErr != nil {
		return fmt.Errorf("%s 接口查询失败: %w", suiteName, queryErr)
	}
	defer app.Release()

	// 隐藏 + 静默（不弹窗、不抢焦点）
	if _, err := oleutil.PutProperty(app, "Visible", false); err != nil {
		return fmt.Errorf("设置隐藏失败: %w", err)
	}
	if _, err := oleutil.PutProperty(app, "DisplayAlerts", 0); err != nil {
		return fmt.Errorf("设置静默失败: %w", err)
	}

	docsVar, err := oleutil.GetProperty(app, "Documents")
	if err != nil {
		return fmt.Errorf("取 Documents 失败: %w", err)
	}
	docs := docsVar.ToIDispatch()
	defer docs.Release()

	// ReadOnly=true 打开（不动源文件）；ConfirmConversions=false 不弹转换对话框
	docVar, err := oleutil.CallMethod(docs, "Open", input, false, true)
	if err != nil {
		return fmt.Errorf("%s 打开文档失败: %w", suiteName, err)
	}
	doc := docVar.ToIDispatch()
	defer doc.Release()

	// SaveAs2 FileFormat=16（wdFormatDocumentDefault → .docx）
	if _, err := oleutil.CallMethod(doc, "SaveAs2", output, 16); err != nil {
		return fmt.Errorf("%s 另存失败: %w（文件可能含不支持的特性）", suiteName, err)
	}
	if _, err := oleutil.CallMethod(doc, "Close", false); err != nil {
		return fmt.Errorf("关闭文档失败: %w", err)
	}
	if _, err := oleutil.CallMethod(app, "Quit"); err != nil {
		return fmt.Errorf("退出 %s 失败: %w", suiteName, err)
	}
	return nil
}

// convertViaMSOffice Windows：按输入类型分派 COM 应用（CreateObject 经注册表自解析，
// 安装位任意；失败=未安装/未注册）。⚠️ 三路均未实测（用户 Windows 机验证）。
func convertViaMSOffice(input, output string) error {
	switch strings.ToLower(filepath.Ext(input)) {
	case ".xls", ".et":
		return convertViaCOMExcel(input, output, "Excel.Application", "Microsoft Excel")
	case ".ppt", ".dps":
		return convertViaCOMPowerPoint(input, output, "PowerPoint.Application", "Microsoft PowerPoint")
	default:
		return convertViaCOMWordLike(input, output, "Word.Application", "Microsoft Word")
	}
}

// convertViaWPS Windows：WPS COM 分派（KWPS=文字 / KET=表格 / KWPP=演示——兼容 MS 接口面）
func convertViaWPS(input, output string) error {
	switch strings.ToLower(filepath.Ext(input)) {
	case ".xls", ".et":
		return convertViaCOMExcel(input, output, "KET.Application", "WPS 表格")
	case ".ppt", ".dps":
		return convertViaCOMPowerPoint(input, output, "KWPP.Application", "WPS 演示")
	default:
		return convertViaCOMWordLike(input, output, "KWPS.Application", "WPS 文字")
	}
}

// convertViaCOMExcel Excel 系 COM：Workbooks.Open(ReadOnly) + SaveAs(FileFormat=51 xlOpenXMLWorkbook)
func convertViaCOMExcel(input, output, progID, suiteName string) error {
	return withCOMSession(func() error {
		return convertExcelInner(input, output, progID, suiteName)
	})
}

func convertExcelInner(input, output, progID, suiteName string) error {
	appObj, createErr := oleutil.CreateObject(progID)
	if createErr != nil {
		return fmt.Errorf("未检测到 %s（COM 创建 %s 失败）", suiteName, progID)
	}
	defer appObj.Release()
	app, _ := appObj.QueryInterface(ole.IID_IDispatch)
	defer app.Release()
	if _, err := oleutil.PutProperty(app, "Visible", false); err != nil {
		return fmt.Errorf("设置隐藏失败: %w", err)
	}
	if _, err := oleutil.PutProperty(app, "DisplayAlerts", false); err != nil {
		return fmt.Errorf("设置静默失败: %w", err)
	}
	booksVar, err := oleutil.GetProperty(app, "Workbooks")
	if err != nil {
		return fmt.Errorf("取 Workbooks 失败: %w", err)
	}
	books := booksVar.ToIDispatch()
	defer books.Release()
	// Open(FileName, UpdateLinks=0, ReadOnly=true)
	bookVar, err := oleutil.CallMethod(books, "Open", input, 0, true)
	if err != nil {
		return fmt.Errorf("%s 打开工作簿失败: %w", suiteName, err)
	}
	book := bookVar.ToIDispatch()
	defer book.Release()
	if _, err := oleutil.CallMethod(book, "SaveAs", output, 51); err != nil {
		return fmt.Errorf("%s 另存失败: %w", suiteName, err)
	}
	if _, err := oleutil.CallMethod(book, "Close", false); err != nil {
		return fmt.Errorf("关闭工作簿失败: %w", err)
	}
	if _, err := oleutil.CallMethod(app, "Quit"); err != nil {
		return fmt.Errorf("退出 %s 失败: %w", suiteName, err)
	}
	return nil
}

// convertViaCOMPowerPoint 演示系 COM：Presentations.Open(ReadOnly, 无窗口) +
// SaveAs(FileFormat=24 ppSaveAsOpenXMLPresentation)
func convertViaCOMPowerPoint(input, output, progID, suiteName string) error {
	return withCOMSession(func() error {
		return convertPowerPointInner(input, output, progID, suiteName)
	})
}

func convertPowerPointInner(input, output, progID, suiteName string) error {
	appObj, createErr := oleutil.CreateObject(progID)
	if createErr != nil {
		return fmt.Errorf("未检测到 %s（COM 创建 %s 失败）", suiteName, progID)
	}
	defer appObj.Release()
	app, _ := appObj.QueryInterface(ole.IID_IDispatch)
	defer app.Release()
	if _, err := oleutil.PutProperty(app, "Visible", false); err != nil {
		return fmt.Errorf("设置隐藏失败: %w", err)
	}
	presVar, err := oleutil.GetProperty(app, "Presentations")
	if err != nil {
		return fmt.Errorf("取 Presentations 失败: %w", err)
	}
	presentations := presVar.ToIDispatch()
	defer presentations.Release()
	// Open(FileName, ReadOnly=msoTrue=-1, Untitled=msoFalse=0, WithWindow=msoFalse=0)
	presOpenVar, err := oleutil.CallMethod(presentations, "Open", input, -1, 0, 0)
	if err != nil {
		return fmt.Errorf("%s 打开演示失败: %w", suiteName, err)
	}
	presentation := presOpenVar.ToIDispatch()
	defer presentation.Release()
	if _, err := oleutil.CallMethod(presentation, "SaveAs", output, 24); err != nil {
		return fmt.Errorf("%s 另存失败: %w", suiteName, err)
	}
	if _, err := oleutil.CallMethod(presentation, "Close"); err != nil {
		return fmt.Errorf("关闭演示失败: %w", err)
	}
	if _, err := oleutil.CallMethod(app, "Quit"); err != nil {
		return fmt.Errorf("退出 %s 失败: %w", suiteName, err)
	}
	return nil
}

// initCOM COM 初始化（S_FALSE=已初始化不算错）
func initCOM() error {
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		if !strings.Contains(err.Error(), "S_FALSE") && !strings.Contains(err.Error(), "0x1") {
			return fmt.Errorf("COM 初始化失败: %w", err)
		}
	}
	return nil
}
