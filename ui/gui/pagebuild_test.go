package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestToolPagesBuild 四个工具页在零值 app 上能完整建帧不崩
// （NewTester 头内建帧——布局路径全覆盖，点击回调不触发）。
func TestToolPagesBuild(t *testing.T) {
	pages := map[string]func(a *app, c *ui.Context){
		"encrypt":    (*app).pageEncrypt,
		"decrypt":    (*app).pageDecrypt,
		"keys":       (*app).pageKeys,
		"base64":     (*app).pageBase64,
		"media":      (*app).pageMedia,
		"pdfmerge":   (*app).pagePdfMerge,
		"pdfextract": (*app).pagePdfExtract,
		"image":      (*app).pageImage,
		"imagecmp":   (*app).pageImageCmp,
		"burn":       (*app).pageBurn,
	}
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			testApp := &app{keyBitsIdx: 1, b64In: "样本输入",
				b64Results: []b64Result{{from: "样本", to: "5p2x5Lqs", enc: true}},
				mediaPath:  "/tmp/样本.mp4", mediaFmt: ".mp4",
				imagePath: "/tmp/样本.png", imageFmt: ".png",
				imageCmpInputs: []string{"/tmp/样本.jpg", "/tmp/另一张.jpg"}, imageCmpMode: "1MB", imageCmpPriority: "size-first",
				pdfMergeInputs: []string{"/tmp/甲.pdf", "/tmp/乙.pdf"},
				pdfExtractPath: "/tmp/报告.pdf", pdfExtractPages: "1-3,8-",
				burnInputs: []string{"/tmp/备份.iso"}, burnModeStr: "多区段可追加",
				burnDevices: []BurnDevice{{Index: 0, Path: "/dev/sr0", Vendor: "HL-DT-ST", Product: "DVDRAM GH24NSC0", Status: "blank"}},
				burnDevStr:  "1 · HL-DT-ST DVDRAM GH24NSC0（空白）"}
			ui.NewTester(func(c *ui.Context) {
				page(testApp, c)
			}, 720, 804)
		})
	}
}
