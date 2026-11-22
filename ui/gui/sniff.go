// 魔数分型与用户反馈（格式守卫 + 异步 toast 队列）。
//
// 哲学（m00961 用户定案）：格式判定只看文件内容不看扩展名。GUI 侧只做
// 「明确认识 → 拒收」的正向防御——分类器认出文件属于其它已知族才拦，
// 认不出的长尾格式（引擎可能支持）一律放行，由引擎内容判格后经任务
// 失败 toast 给出权威结论。GUI 永不抢引擎的格式裁判权。
package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// magicSniff 读文件头判格用字节（覆盖 ustar@257 与 ftyp@4-8 判定点）。
// 打不开/读不出返回 nil——nil 一律视为「不认识」放行（被占的网络盘等）。
func magicSniff(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	head := make([]byte, 300)
	read, err := io.ReadFull(file, head)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil
	}
	return head[:read]
}

func hasMagicPrefix(data []byte, prefix string) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == prefix
}

func magicAt(data []byte, offset int, brand string) bool {
	return offset+len(brand) <= len(data) && string(data[offset:offset+len(brand)]) == brand
}

// classifyMagic 内容分型：返回 "" 表示不认识（放行给引擎裁决）。
func classifyMagic(data []byte) string {
	switch {
	case hasMagicPrefix(data, "%PDF-"):
		return "pdf"
	case len(data) >= 12 && magicAt(data, 4, "ftyp"): // ISO-BMFF：mp4/mov/m4a 与 heic/avif 同容器
		switch string(data[8:12]) {
		case "heic", "heix", "hevc", "heim", "heis", "mif1", "msf1", "avif":
			return "image"
		}
		return "av"
	case hasMagicPrefix(data, "\x1a\x45\xdf\xa3"): // EBML：mkv/webm/mka
		return "av"
	case hasMagicPrefix(data, "ID3"): // mp3 + ID3v2 头
		return "av"
	case len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0: // MPEG 音频帧同步（裸 mp3）
		return "av"
	case hasMagicPrefix(data, "fLaC"), hasMagicPrefix(data, "OggS"):
		return "av"
	case hasMagicPrefix(data, "RIFF"): // wav/avi/webp 共壳
		switch {
		case magicAt(data, 8, "WAVE"), magicAt(data, 8, "AVI "):
			return "av"
		case magicAt(data, 8, "WEBP"):
			return "image"
		}
		return ""
	case hasMagicPrefix(data, "\xff\xd8\xff"): // jpeg
		return "image"
	case hasMagicPrefix(data, "\x89PNG"), hasMagicPrefix(data, "GIF8"):
		return "image"
	case hasMagicPrefix(data, "II*\x00"), hasMagicPrefix(data, "MM\x00*"): // tiff
		return "image"
	case hasMagicPrefix(data, "\x76\x2f\x31\x01"): // OpenEXR
		return "image"
	case hasMagicPrefix(data, "#?RADIANCE"): // Radiance HDR
		return "image"
	case hasMagicPrefix(data, "\xff\x0a"): // jpeg xl 裸流
		return "image"
	case hasMagicPrefix(data, "\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"): // OLE2 复合文档：doc/xls/ppt/wps/et/dps
		return "office"
	case hasMagicPrefix(data, "PK\x03\x04"), hasMagicPrefix(data, "PK\x05\x06"),
		hasMagicPrefix(data, "Rar!"), hasMagicPrefix(data, "7z\xbc\xaf\x27\x1c"),
		hasMagicPrefix(data, "\x1f\x8b"), hasMagicPrefix(data, "BZh"),
		hasMagicPrefix(data, "\xfd7zXZ\x00"), hasMagicPrefix(data, "\x28\xb5\x2f\xfd"),
		hasMagicPrefix(data, "MSCF"):
		return "archive"
	case len(data) >= 262 && string(data[257:262]) == "ustar":
		return "archive"
	case isTexty(data):
		return "text"
	}
	return ""
}

// isTexty 前 64 字节全可打印/空白 → 纯文本（txt/代码/配置——可明确拒收的依据）。
func isTexty(data []byte) bool {
	probe := data
	if len(probe) > 64 {
		probe = probe[:64]
	}
	if len(probe) == 0 {
		return false
	}
	for _, byteValue := range probe {
		if byteValue == '\t' || byteValue == '\n' || byteValue == '\r' {
			continue
		}
		if byteValue < 0x20 || byteValue == 0x7f {
			return false
		}
	}
	return true
}

// queueToast 异步入队一条 toast（帧外安全——goroutine/引擎回调里都能调），
// 下一帧 pane 顶部 drainToasts 倒进 c.Toast（Toast 仅帧构建期可用）。
func (a *app) queueToast(message string) {
	a.toastMu.Lock()
	a.pendingToasts = append(a.pendingToasts, message)
	a.toastMu.Unlock()
	a.win.Update(func() {})
}

// drainToasts 帧首把队列倒进真 toast。
func (a *app) drainToasts(c *ui.Context) {
	a.toastMu.Lock()
	defer a.toastMu.Unlock()
	for _, message := range a.pendingToasts {
		c.Toast(message)
	}
	a.pendingToasts = nil
}

// rejectIfForeign 单文件格式守卫：分类器认出该文件属于其它已知族 →
// toast「格式不支持」并返回 true（调用方拒绝落位）；不认识 → 放行 false。
func (a *app) rejectIfForeign(path string, want string) bool {
	got := classifyMagic(magicSniff(path))
	if got == "" || got == want {
		return false
	}
	a.queueToast("格式不支持：" + filepath.Base(path))
	return true
}

// toastEngineVerdict 引擎判格浮出：识别类失败 → 「格式不支持：<名>」；
// 其它失败（损坏/密码…）→ 原样点名；良性/环境缺失（引擎未找到/预览不可用）不扰。
func (a *app) toastEngineVerdict(name string, verdict string) {
	switch verdict {
	case "", "未找到 zpack 引擎", "预览不可用":
		return
	}
	if strings.Contains(verdict, "无法识别") {
		a.queueToast("格式不支持：" + name)
		return
	}
	a.queueToast(name + "：" + verdict)
}
