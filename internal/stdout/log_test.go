package stdout

// encodeJSONToBuffer 单测：信封协议形状 + v2 struct 键序确定性 + 原子性前提（失败零字节）
import (
	"strings"
	"testing"
)

type testPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestEncodeJSONToBuffer(t *testing.T) {
	testCases := []struct {
		name    string
		value   any
		want    string
		wantErr bool
	}{
		{"信封成功三键序", envelope{IsSuccess: true, Data: testPayload{Name: "压缩", Count: 3}},
			"{\"is_success\":true,\"data\":{\"name\":\"压缩\",\"count\":3}}\n", false},
		{"信封失败对称", envelope{IsSuccess: false, ErrorInfo: "无法读取"},
			"{\"is_success\":false,\"error_info\":\"无法读取\"}\n", false},
		{"HTML 字符不转义", envelope{IsSuccess: true, Data: testPayload{Name: "<a>&"}},
			"{\"is_success\":true,\"data\":{\"name\":\"<a>&\",\"count\":0}}\n", false},
		{"中文原样 UTF-8", envelope{IsSuccess: true, Data: testPayload{Name: "中 文"}},
			"{\"is_success\":true,\"data\":{\"name\":\"中 文\",\"count\":0}}\n", false},
		{"不可序列化值报错", map[string]any{"bad": make(chan int)}, "", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			buffer, encodeErr := encodeJSONToBuffer(testCase.value)
			if testCase.wantErr {
				if encodeErr == nil {
					t.Fatalf("期望编码失败，实际成功: %s", buffer.String())
				}
				// 原子性前提：失败时缓冲必须零字节（jsonEncode 据此保证 stdout 不出半截）
				if buffer.Len() != 0 {
					t.Errorf("失败路径缓冲非空（%d 字节），破坏原子输出前提", buffer.Len())
				}
				return
			}
			if encodeErr != nil {
				t.Fatalf("编码失败: %v", encodeErr)
			}
			if buffer.String() != testCase.want {
				t.Errorf("编码结果不符:\n got: %q\nwant: %q", buffer.String(), testCase.want)
			}
		})
	}
}

func TestEnvelopeKeyOrderDeterministic(t *testing.T) {
	// 同一 struct 反复编码必须逐字节一致（v1 时代靠字母序；v2 时代靠声明序——生产禁 map 的原因）
	first, err := encodeJSONToBuffer(envelope{IsSuccess: true, Data: testPayload{Name: "x", Count: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for idx := 0; idx < 20; idx++ {
		again, err := encodeJSONToBuffer(envelope{IsSuccess: true, Data: testPayload{Name: "x", Count: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if again.String() != first.String() {
			t.Fatalf("第 %d 次编码不一致——确定性破坏: %q vs %q", idx, again.String(), first.String())
		}
	}
	if !strings.HasSuffix(first.String(), "\n") {
		t.Error("契约尾部换行缺失")
	}
}
