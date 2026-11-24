package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollectCompressTasksRejectsDuplicates 多输入在包里同名时报错（2026-11-24 用户拍板：不自动改名）。
// 不重名的多输入照常收集；重名的报错里点名包内名与来源，并给出两条出路
func TestCollectCompressTasksRejectsDuplicates(t *testing.T) {
	root := t.TempDir()
	write := func(relativePath, content string) string {
		full := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	first := write("甲/x.txt", "one")
	second := write("乙/x.txt", "two")
	third := write("丙/x.txt", "fourth")
	other := write("乙/y.txt", "three")
	fourth := write("丁/q.txt", "q1")
	fifth := write("戊/q.txt", "q2")

	// 不重名：两个不同目录里的文件平铺进包根，各归各
	tasks, collectErr := collectCompressTasks([]string{first, other}, nil, nil)
	if collectErr != nil {
		t.Fatalf("不重名的多输入不应报错: %v", collectErr)
	}
	if len(tasks) != 2 || tasks[0].entryName != "x.txt" || tasks[1].entryName != "y.txt" {
		t.Fatalf("条目收集不符: %+v", tasks)
	}

	hint := "；请把它们分别放进不同的文件夹再压缩，或只压缩它们共同的上层目录"
	cases := []struct {
		name   string
		inputs []string
		want   string // 期望的整句 error_info
	}{
		{"两个不同目录里的同名文件", []string{first, second},
			"多个输入在压缩包里会同名：x.txt（" + first + " 与 " + second + "）" + hint},
		{"同一个路径给了两次", []string{first, first},
			"多个输入在压缩包里会同名：x.txt（" + first + " 给了 2 次）" + hint},
		{"多处重名：报总数，按「第二次撞上」的先后点名（x.txt 在 second 处先撞、q.txt 到 fifth 才撞）", []string{fourth, first, second, fifth, third},
			"多个输入在压缩包里会同名（2 处）：x.txt（" + first + " 与 " + second + " 与 " + third + "）、q.txt（" + fourth + " 与 " + fifth + "）" + hint},
	}
	for _, testCase := range cases {
		_, collectErr := collectCompressTasks(testCase.inputs, nil, nil)
		if collectErr == nil {
			t.Errorf("%s: 应报错", testCase.name)
			continue
		}
		if collectErr.Error() != testCase.want {
			t.Errorf("%s:\n got: %s\nwant: %s", testCase.name, collectErr.Error(), testCase.want)
		}
	}

	// 同名的目录：两边都是 资料/a.txt，在包里同样撞名
	left := write("己/资料/a.txt", "a")
	right := write("庚/资料/a.txt", "b")
	_, collectErr = collectCompressTasks([]string{filepath.Dir(left), filepath.Dir(right)}, nil, nil)
	if collectErr == nil || !strings.Contains(collectErr.Error(), "资料/a.txt（") {
		t.Errorf("同名目录应报错并点出包内名: %v", collectErr)
	}
}

// TestReportDuplicateEntriesReportsUpToLimit 报错点到上限为止：上限内的全部点名，超出的归成「等」；不重名返回 nil
func TestReportDuplicateEntriesReportsUpToLimit(t *testing.T) {
	var tasks []compressTask
	for index := 0; index < 5; index++ {
		letter := string(rune('a' + index))
		tasks = append(tasks,
			compressTask{entryName: "same" + letter + ".txt", sourcePath: "/甲/same" + letter + ".txt"},
			compressTask{entryName: "same" + letter + ".txt", sourcePath: "/乙/same" + letter + ".txt"},
		)
	}
	duplicateErr := reportDuplicateEntries(tasks)
	if duplicateErr == nil {
		t.Fatal("应报错")
	}
	message := duplicateErr.Error()
	for _, expected := range []string{"（5 处）", "samea.txt（", "sameb.txt（", "samec.txt（", "等；请把它们"} {
		if !strings.Contains(message, expected) {
			t.Errorf("报错 %q 里缺 %q", message, expected)
		}
	}
	for _, unexpected := range []string{"samed.txt", "samee.txt"} {
		if strings.Contains(message, unexpected) {
			t.Errorf("报错 %q 不应点名 %s（超过上限 %d）", message, unexpected, MAX_DUPLICATE_ENTRIES_REPORTED)
		}
	}
	// 恰好到上限：不出现「等」
	atLimit := reportDuplicateEntries(tasks[:6])
	if atLimit == nil || strings.Contains(atLimit.Error(), "等；") || !strings.Contains(atLimit.Error(), fmt.Sprintf("（%d 处）", MAX_DUPLICATE_ENTRIES_REPORTED)) {
		t.Errorf("恰好到上限时应全部点名、不带「等」，得到: %v", atLimit)
	}
	distinct := []compressTask{{entryName: "a.txt", sourcePath: "/a.txt"}, {entryName: "dir/a.txt", sourcePath: "/dir/a.txt"}}
	if noDuplicate := reportDuplicateEntries(distinct); noDuplicate != nil {
		t.Errorf("不重名不应报错，得到: %v", noDuplicate)
	}
	if nilDuplicate := reportDuplicateEntries(nil); nilDuplicate != nil {
		t.Errorf("没有条目不应报错，得到: %v", nilDuplicate)
	}
}
