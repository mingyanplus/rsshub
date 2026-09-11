package ai

import "testing"

func TestExtractJSONFromResponse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "纯 JSON 对象",
			in:   `{"is_ad":false,"summary":"测试"}`,
			want: `{"is_ad":false,"summary":"测试"}`,
		},
		{
			name: "markdown 代码块包裹",
			in:   "说明文字\n```json\n{\"a\":1}\n```\n结尾",
			want: `{"a":1}`,
		},
		{
			name: "LLM 把同一 JSON 输出两遍",
			in:   `{"is_ad":false,"summary":"测试"},{"is_ad":false,"summary":"测试"}`,
			want: `{"is_ad":false,"summary":"测试"}`,
		},
		{
			name: "对象后跟随带逗号的说明文字",
			in:   `{"a":1},以上就是分析结果`,
			want: `{"a":1}`,
		},
		{
			name: "字符串值内含大括号不干扰截取",
			in:   `{"summary":"函数体 { return 1 } 结束"},"extra":"x"}`,
			want: `{"summary":"函数体 { return 1 } 结束"}`,
		},
		{
			name: "字符串值内的转义引号",
			in:   `{"summary":"他说\"你好\"","b":2}`,
			want: `{"summary":"他说\"你好\"","b":2}`,
		},
		{
			name: "无 JSON 时原样返回（去空白）",
			in:   "  普通文本  ",
			want: "普通文本",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractJSONFromResponse(c.in); got != c.want {
				t.Errorf("ExtractJSONFromResponse() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestUnmarshalLenientDuplicatedObjects(t *testing.T) {
	// LLM 双份输出经提取后应可正常解析（端到端：提取 + 解析）
	raw := `{"is_ad":false,"summary":"陀思妥耶夫斯基","keywords":["文学"]},{"is_ad":false,"summary":"重复"}`
	var result AnalyzeResult
	if err := UnmarshalLenient([]byte(ExtractJSONFromResponse(raw)), &result); err != nil {
		t.Fatalf("UnmarshalLenient failed: %v", err)
	}
	if result.Summary != "陀思妥耶夫斯基" {
		t.Errorf("Summary = %q, want 陀思妥耶夫斯基", result.Summary)
	}
}
