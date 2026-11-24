package main

import (
	"os"
	"runtime"
	"strings"
)

// burnVolumeNamePlan 光盘名称行的显示与传参规则（纯函数，规则可单测）。
// 2026-11-24 用户定案：只有空白盘能起名——名称属于最后一个区段，已有内容的盘沿用盘上名称，
// 追加时改写会让盘名漂移。勾了「先擦空再烧」的盘擦完就是空白盘，同样可以起名
type burnVolumeNamePlan struct {
	editable    bool   // 显示输入框
	hint        string // 标签列副标（≤7 个汉字——TestLabelHintsStayOnOneLine 守着恒一行）
	placeholder string // 输入框占位文字
	note        string // 输入框下方的说明（可空）
	lockedText  string // 不可改名时控件位置上的一句说明
	volumeArg   string // 起刻时传给 --volid 的值（空=不传，交给引擎缺省：单个目录=目录名）
}

// BURN_VOLUME_NAME_CHARSET_NOTE 非 Windows 平台的字符集提示——引擎在 mac/Linux 上只保留字母、数字、下划线
const BURN_VOLUME_NAME_CHARSET_NOTE = "macOS/Linux 下只保留字母、数字和下划线"

// planBurnVolumeName 按选中刻录机的介质状态定名称行。status 是 burn devices 输出里的 status；
// name 是输入框当前内容；singleDir=刻录内容恰好是一个文件夹；erase=勾了「先擦空再烧」
func planBurnVolumeName(status, name string, singleDir, erase bool) burnVolumeNamePlan {
	if status != "blank" && !erase {
		plan := burnVolumeNamePlan{hint: "需空白光盘", lockedText: "放入空白光盘后可以设置名称"}
		switch status {
		case "appendable":
			plan.hint, plan.lockedText = "沿用盘上名称", "这张盘已有内容，名称保持不变"
		case "full":
			plan.hint, plan.lockedText = "已封盘", "这张盘已封盘，不能再写入"
		}
		return plan
	}
	plan := burnVolumeNamePlan{editable: true, hint: "仅空白盘可设", placeholder: "留空用默认名", volumeArg: strings.TrimSpace(name)}
	if singleDir {
		plan.placeholder = "留空用文件夹名"
	}
	if runtime.GOOS != "windows" {
		plan.note = BURN_VOLUME_NAME_CHARSET_NOTE
	}
	return plan
}

// burnInputIsDir 刻录内容项是不是文件夹（判断不了按不是处理——输入框占位退回「留空用默认名」）
func burnInputIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
