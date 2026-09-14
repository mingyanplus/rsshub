package processor

import (
	"testing"
)

// newAggForCategory 构造仅用于分类匹配的聚合器（不触碰 db/analyzer）
func newAggForCategory(rules []CategoryRule) *TopicAggregator {
	return NewTopicAggregator(nil, nil, rules)
}

// 内置默认规则：教育类细粒度分类归入独立「教育」频道（原先并入生活）
func TestNormalizeCategoryDefaultEducation(t *testing.T) {
	a := newAggForCategory(nil)
	cases := map[string]string{
		"教育政策":   CategoryEdu,
		"高考招生":   CategoryEdu,
		"考研调剂":   CategoryEdu,
		"留学申请":   CategoryEdu,
		"学校动态":   CategoryEdu,
		"AI 教育产品": CategoryAI, // AI 规则在前，AI+教育交叉归 AI
		"生活感悟":   CategoryLife,
		"产品发布":   CategoryTech,
		"时政评论":   CategorySociety,
		"科技行业分析": CategoryTech,
		"":       CategoryOther,
		"无法归类":   CategoryOther,
	}
	for raw, want := range cases {
		if got := a.normalizeCategory(raw); got != want {
			t.Errorf("normalizeCategory(%q) = %q, want %q", raw, got, want)
		}
	}
}

// 自定义规则：config.yaml 配置的分类集完全替换默认，顺序决定优先级
func TestNormalizeCategoryCustomRules(t *testing.T) {
	a := newAggForCategory([]CategoryRule{
		{Name: "考试资讯", Keywords: []string{"高考", "中考"}},
		{Name: "技术", Keywords: []string{"技术", "开源"}},
	})
	cases := map[string]string{
		"高考分数线":   "考试资讯",
		"开源技术动态":   "技术",
		"财经新闻":     CategoryOther, // 默认规则不再生效，未命中自定义规则
		"":         CategoryOther,
	}
	for raw, want := range cases {
		if got := a.normalizeCategory(raw); got != want {
			t.Errorf("normalizeCategory(%q) = %q, want %q", raw, got, want)
		}
	}
}
