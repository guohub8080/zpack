package burn

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestBurnImageOnUsedDiscWarning 镜像输入遇到有数据的盘才警告（表驱动）：空白盘不警告，
// 目录 / 多输入会导入上一区段、原有文件保持可见，也不警告；读不到介质状态（mediaUsed=false）不警告
func TestBurnImageOnUsedDiscWarning(t *testing.T) {
	cases := []struct {
		name      string
		inputs    []string
		mediaUsed bool
		want      string // 期望的警告；空串=不警告
	}{
		{"镜像写进有数据的盘：警告后允许", []string{"a.ISO"}, true, BURN_IMAGE_ON_USED_DISC_WARNING},
		{"镜像写进空白盘", []string{"a.iso"}, false, ""},
		{"目录写进有数据的盘（会导入上一区段）", []string{"目录"}, true, ""},
		{"多输入不警告（里面有镜像的组合在入口就被拒）", []string{"a.iso", "b"}, true, ""},
	}
	for _, testCase := range cases {
		if got := burnImageOnUsedDiscWarning(testCase.inputs, testCase.mediaUsed); got != testCase.want {
			t.Errorf("%s: 警告 %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}

// TestBurnStatusRefusal 按介质状态的写入前拒绝（表驱动）：只有没放盘、已封盘两种；--blank 不改变结论
func TestBurnStatusRefusal(t *testing.T) {
	cases := []struct {
		status string
		want   string // 期望的 error；空串=不拒绝
	}{
		{"no-media", "刻录机里没有光盘——请放入光盘后重试"},
		{"full", "光盘已封盘，无法继续写入"},
		{"blank", ""}, {"appendable", ""}, {"", ""}, {"is offline", ""},
	}
	for _, testCase := range cases {
		refusal := burnStatusRefusal(testCase.status)
		switch {
		case testCase.want == "" && refusal != nil:
			t.Errorf("状态 %q：不应拒绝，得到: %v", testCase.status, refusal)
		case testCase.want != "" && refusal == nil:
			t.Errorf("状态 %q：应拒绝（%s）", testCase.status, testCase.want)
		case refusal != nil && refusal.Error() != testCase.want:
			t.Errorf("状态 %q：%q ≠ %q", testCase.status, refusal.Error(), testCase.want)
		}
	}
}

// TestBurnSessionLogSize xorriso 会话日志 → 本次写入的字节数（表驱动）：每个区段一行
// `timestamp start_lba size volume-id`，取最后一行的 size × 2048；读不出给 0
func TestBurnSessionLogSize(t *testing.T) {
	cases := []struct {
		name    string
		logText string
		want    int64
	}{
		{"空日志", "", 0},
		{"一行", "2026.11.24.120000 0 12345 BACKUP\n", 12345 * 2048},
		{"卷标带空格不影响前三项", "2026.11.24.120000 295496 777 My Backup Disc\n", 777 * 2048},
		{"两个区段取最后一个", "2026.11.24.120000 0 100 A\n2026.11.24.130000 300 42 A\n", 42 * 2048},
		{"末尾空行", "2026.11.24.120000 0 5 A\n\n", 5 * 2048},
		{"size 不是数字", "2026.11.24.120000 0 abc A\n", 0},
		{"行不完整", "garbage\n", 0},
	}
	for _, testCase := range cases {
		if got := burnSessionLogSize(testCase.logText); got != testCase.want {
			t.Errorf("%s: %d ≠ %d", testCase.name, got, testCase.want)
		}
	}
}

// TestBurnLinuxTreeArguments Linux 目录 / 多输入的 xorriso 命令行（表驱动）：顺序即执行顺序，
// 卷标规则（显式 > 空白盘给缺省名 > 可追加的盘不给 > 判不出时给缺省名），封盘 / 弹出 / 速度各归其位
func TestBurnLinuxTreeArguments(t *testing.T) {
	join := func(elements ...string) string { return filepath.Join(elements...) }
	directory := join("data", "备份")
	named := burnFlags{volid: "BACKUP"}
	explicit := burnFlags{volid: "MINE", volidGiven: true}
	commandLine := func(arguments []string) string { return strings.Join(arguments, " ") }

	cases := []struct {
		name        string
		mediaStatus string
		inputs      []string
		flags       burnFlags
		want        string
	}{
		{"空白盘上的单个目录：内容进镜像根，带缺省卷标", "blank", []string{directory}, named,
			"-no_rc -session_log SESSION -dev /dev/sr0 -volid BACKUP -joliet on -map " + mustAbs(directory) +
				" / -find / -exec mkisofs_r -- -close as_needed -commit_eject all"},
		{"可追加的盘不给卷标：沿用盘上名称", "appendable", []string{directory}, named,
			"-no_rc -session_log SESSION -dev /dev/sr0 -joliet on -map " + mustAbs(directory) +
				" / -find / -exec mkisofs_r -- -close as_needed -commit_eject all"},
		{"可追加的盘显式给了卷标：以显式为准", "appendable", []string{directory}, explicit,
			"-no_rc -session_log SESSION -dev /dev/sr0 -volid MINE -joliet on -map " + mustAbs(directory) +
				" / -find / -exec mkisofs_r -- -close as_needed -commit_eject all"},
		{"判不出介质状态：按旧行为给缺省名", "", []string{directory}, named,
			"-no_rc -session_log SESSION -dev /dev/sr0 -volid BACKUP -joliet on -map " + mustAbs(directory) +
				" / -find / -exec mkisofs_r -- -close as_needed -commit_eject all"},
		{"先擦再刻：擦完是空白盘，给缺省卷标；擦除紧跟 -dev", "appendable", []string{directory}, burnFlags{volid: "BACKUP", blank: true},
			"-no_rc -session_log SESSION -dev /dev/sr0 -blank as_needed -volid BACKUP -joliet on -map " + mustAbs(directory) +
				" / -find / -exec mkisofs_r -- -close as_needed -commit_eject all"},
		{"多输入：每项按唯一名进镜像根", "blank", []string{join("x", "a.txt"), join("y", "a.txt"), join("x", "资料")}, named,
			"-no_rc -session_log SESSION -dev /dev/sr0 -volid BACKUP -joliet on" +
				" -map " + mustAbs(join("x", "a.txt")) + " /a.txt" +
				" -map " + mustAbs(join("y", "a.txt")) + " /a-2.txt" +
				" -map " + mustAbs(join("x", "资料")) + " /资料" +
				" -find / -exec mkisofs_r -- -close as_needed -commit_eject all"},
		{"封盘 + 限速 + 不弹出", "blank", []string{directory}, burnFlags{volid: "BACKUP", close: true, speed: 8, noEject: true},
			"-no_rc -session_log SESSION -dev /dev/sr0 -volid BACKUP -joliet on -map " + mustAbs(directory) +
				" / -find / -exec mkisofs_r -- -speed 8 -close on -commit"},
	}
	for _, testCase := range cases {
		got := burnLinuxTreeArguments("/dev/sr0", testCase.mediaStatus, testCase.inputs, testCase.flags, "SESSION")
		if commandLine(got) != testCase.want {
			t.Errorf("%s:\n got: %s\nwant: %s", testCase.name, commandLine(got), testCase.want)
		}
	}

	// 以 -- 开头的路径不会被当成 xorriso 的列表分隔符：它是 -map 的固定参数，原样跟在 -map 后面
	arguments := burnLinuxTreeArguments("/dev/sr0", "blank", []string{"--not-an-option"}, named, "SESSION")
	if mapAt := slices.Index(arguments, "-map"); mapAt < 0 || !reflect.DeepEqual(arguments[mapAt:mapAt+3], []string{"-map", mustAbs("--not-an-option"), "/"}) {
		t.Errorf("以 -- 开头的路径应原样作为 -map 的参数: %v", arguments)
	}
}

// TestBurnFailureDetail 外部工具的 stderr → error_info 里的一句话（表驱动）：取最后一条带严重级别的
// FAILURE / SORRY；没有就取最后一个非空行；过长截断；全空返回空串
func TestBurnFailureDetail(t *testing.T) {
	cases := []struct {
		name       string
		stderrText string
		want       string
	}{
		{"全空", "", ""},
		{"只有空白行", "\n  \n", ""},
		{"取最后一条 FAILURE", "xorriso : UPDATE : Writing: 10%\nxorriso : FAILURE : -dev '/dev/sr0' : cannot open\nxorriso : UPDATE : done\n", "xorriso : FAILURE : -dev '/dev/sr0' : cannot open"},
		{"SORRY 也算", "xorriso : SORRY : Medium is not appendable\n", "xorriso : SORRY : Medium is not appendable"},
		{"没有带级别的不算", "xorriso : NOTE : hello\nxorriso : UPDATE : bye\n", "xorriso : UPDATE : bye"},
		{"不认得格式的工具取最后一行", "hdiutil: burn: …\nhdiutil: burn failed - Disk not writable\n", "hdiutil: burn failed - Disk not writable"},
		{"过长截断", strings.Repeat("x", 300), strings.Repeat("x", 240) + "…"},
	}
	for _, testCase := range cases {
		if got := burnFailureDetail(testCase.stderrText); got != testCase.want {
			t.Errorf("%s: %q ≠ %q", testCase.name, got, testCase.want)
		}
	}
}
