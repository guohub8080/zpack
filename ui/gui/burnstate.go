package main

import (
	"path/filepath"
	"strings"
)

// 刻录页的介质状态规则（纯函数，规则可单测）。status 是 burn devices 输出里的 status：
// Windows 引擎报 blank / appendable / full / no-media / damaged / erase-required / write-protected /
// unsupported / unrecognized（光驱认不出这张盘）；macOS 只报 no-media（有盘时为空）；Linux 是 xorriso
// 状态短语归一后的值，认不出的原样透传。所以只有明确认识的状态才下结论，其余一律不拦、不起名提示
// 也不改——交给引擎裁决，避免误拦 mac/Linux

// burnBlockReason 选中刻录机的介质挡住刻录的原因（可刻返回空串）。erase=勾了「先擦空再烧」：
// 可擦写盘的异常状态擦除即恢复，不再拦截；认不出的盘擦除也无从谈起，同样拦
func burnBlockReason(status string, erase bool) string {
	switch status {
	case "full":
		return "光盘已封盘"
	case "unrecognized":
		return "光驱认不出这张光盘"
	case "no-media":
		return "未放入光盘"
	case "damaged":
		if erase {
			return ""
		}
		return "光盘状态异常"
	case "erase-required":
		if erase {
			return ""
		}
		return "光盘需先擦除"
	case "write-protected":
		return "光盘被写保护"
	case "unsupported":
		return "不支持这张光盘"
	}
	return ""
}

// burnMediaSuffix 设备下拉里介质状态的括注（认不出的状态不加括注）
func burnMediaSuffix(status string) string {
	switch status {
	case "blank":
		return "（空白）"
	case "appendable":
		return "（可追加）"
	case "full":
		return "（已满）"
	case "no-media":
		return "（无介质）"
	case "damaged":
		return "（状态异常）"
	case "erase-required":
		return "（需先擦除）"
	case "write-protected":
		return "（写保护）"
	case "unsupported":
		return "（不支持）"
	case "unrecognized":
		return "（认不出）"
	}
	return ""
}

// BURN_IMAGE_EXTENSIONS 刻录内容里认作镜像的扩展名——与引擎的白名单一致（只认扩展名，不分大小写）。
// 单独一个镜像时引擎原样直烧，不与盘上原有文件合并
var BURN_IMAGE_EXTENSIONS = map[string]bool{".iso": true, ".img": true, ".bin": true}

// BURN_IMAGE_ON_USED_DISC_CAUTION 刻录内容是一个镜像、选中的盘已有内容时的提醒。镜像原样写进新区段，
// 不导入上一区段——写入后通常只看得到镜像里的文件，原有文件没被擦掉但一般工具读不到。
// 2026-11-24 用户拍板：警告后照常允许，不拦截
const BURN_IMAGE_ON_USED_DISC_CAUTION = "这张盘已有内容。镜像是原样写进新区段的，不会与原有文件合并——写入后通常只看得到镜像里的文件，原有文件虽没被擦掉，但一般工具读不到。要追加到原有文件之后，请改用文件夹或文件；要保留原有文件，请换一张空白盘"

// burnImageCaution 镜像 + 已有内容的盘时的提醒（空串=不用提醒）。只对明确认识的「有数据且可写」状态
// （appendable）下结论——macOS 有盘时状态为空，判不出有没有数据，不提醒；勾了「先擦空再烧」的盘擦完
// 是空白盘，也不提醒
func burnImageCaution(status string, inputs []string, erase bool) string {
	if erase || status != "appendable" || len(inputs) != 1 {
		return ""
	}
	if !BURN_IMAGE_EXTENSIONS[strings.ToLower(filepath.Ext(inputs[0]))] {
		return ""
	}
	return BURN_IMAGE_ON_USED_DISC_CAUTION
}
