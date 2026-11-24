package core

import (
	"fmt"
	"strings"
)

// MAX_DUPLICATE_ENTRIES_REPORTED 同名条目的报错里最多点名的条目数——报错要能在任务卡 / 终端里一行读完，
// 再多只说「等」
const MAX_DUPLICATE_ENTRIES_REPORTED = 3

// reportDuplicateEntries 多个输入在包里落成同名条目时报错（没有重名返回 nil）。
//
// 2026-11-24 用户拍板：不自动改名，有问题就报错。同名条目解压时要么互相覆盖（多数工具的行为）、要么被
// 拒绝（zpack 自己的解压就拒绝，见 zip 管线的「归档含重复条目名」）——所以不能让这样的归档生成出来。
// 怎么来的：文件输入按文件名平铺进包根，目录输入以目录名为根前缀——两个不同目录里的 a.txt、两个同名的
// 目录，都会在包里撞成同一个名字。zip / tar / 7z 三条写入线共用 collectCompressTasks，所以在这里拦一次就够
func reportDuplicateEntries(tasks []compressTask) error {
	counts := map[string]int{}
	var duplicated []string // 包内名，按首次重名的先后——报错文案要确定
	for _, task := range tasks {
		counts[task.entryName]++
		if counts[task.entryName] == 2 {
			duplicated = append(duplicated, task.entryName)
		}
	}
	if len(duplicated) == 0 {
		return nil
	}
	shown := duplicated
	more := ""
	if len(shown) > MAX_DUPLICATE_ENTRIES_REPORTED {
		shown = shown[:MAX_DUPLICATE_ENTRIES_REPORTED]
		more = "等"
	}
	descriptions := make([]string, 0, len(shown))
	for _, entryName := range shown {
		descriptions = append(descriptions, entryName+"（"+strings.Join(describeEntrySources(tasks, entryName), " 与 ")+"）")
	}
	total := ""
	if len(duplicated) > 1 {
		total = fmt.Sprintf("（%d 处）", len(duplicated))
	}
	return fmt.Errorf("多个输入在压缩包里会同名%s：%s%s；请把它们分别放进不同的文件夹再压缩，或只压缩它们共同的上层目录",
		total, strings.Join(descriptions, "、"), more)
}

// describeEntrySources 同名条目的各个来源（按出现顺序）：同一个路径给了多次就只写一次并注明次数
func describeEntrySources(tasks []compressTask, entryName string) []string {
	counts := map[string]int{}
	var sources []string
	for _, task := range tasks {
		if task.entryName != entryName {
			continue
		}
		if counts[task.sourcePath] == 0 {
			sources = append(sources, task.sourcePath)
		}
		counts[task.sourcePath]++
	}
	descriptions := make([]string, 0, len(sources))
	for _, source := range sources {
		if times := counts[source]; times > 1 {
			descriptions = append(descriptions, fmt.Sprintf("%s 给了 %d 次", source, times))
		} else {
			descriptions = append(descriptions, source)
		}
	}
	return descriptions
}
