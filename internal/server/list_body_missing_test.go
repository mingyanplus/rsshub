package server

import "testing"

// TestListBodyMissing 列表型源（html/json）正文缺失判定：空正文或标题复读时
// 需要在入库阶段主动抓取原文，真实正文则不动
func TestListBodyMissing(t *testing.T) {
	cases := []struct {
		name, content, title string
		want                 bool
	}{
		{"空正文", "", "标题", true},
		{"纯空白", "  \n\t ", "标题", true},
		{"仅标签无文本", "<p></p><img src='a.png'>", "标题", true},
		{"正文与标题相同", "标题", "标题", true},
		{"HTML 包裹的标题复读", "<p> 标题 </p>", "标题", true},
		{"正文与标题空白差异（含全角空格）", " 标　题\n", "标题", true},
		{"真实正文", "<p>这是一段足够长的正文内容。</p>", "标题", false},
		{"正文包含标题但有补充", "标题：补充说明", "标题", false},
	}
	for _, c := range cases {
		if got := listBodyMissing(c.content, c.title); got != c.want {
			t.Errorf("%s: listBodyMissing(%q, %q) = %v, want %v", c.name, c.content, c.title, got, c.want)
		}
	}
}
