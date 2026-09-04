package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"rss-ai/internal/ai"
	"rss-ai/internal/models"
)

// 深度话题数量与快讯数量（李自然式：每天只挑 3 个最值得讲的故事，宁可漏掉也不凑数）
const (
	TopicsMaxFeatured = 3
	TopicsMaxBriefs   = 10
)

// generateFromTopics 基于持久化话题生成报告（李自然式：AI 选题 + 跨领域 + 反重复）
// 返回 (nil, nil) 表示报告时段内没有话题，调用方回退到文章聚类路径
func (g *ReportGenerator) generateFromTopics(reportType string) (*models.Report, error) {
	startTime, endTime := reportPeriod(reportType)

	topics, err := g.db.ListTopicsUpdatedBetween(startTime, endTime, 50)
	if err != nil {
		return nil, fmt.Errorf("获取话题失败: %w", err)
	}
	if len(topics) == 0 {
		return nil, nil
	}
	log.Printf("话题报告路径：时段内有 %d 个话题", len(topics))

	// 批量加载各话题文章（深度故事素材 + 报告文章关联）
	topicIDs := make([]int64, len(topics))
	for i, t := range topics {
		topicIDs[i] = t.ID
	}
	previews, err := g.db.GetTopicArticlesForTopics(topicIDs, 8)
	if err != nil {
		return nil, fmt.Errorf("获取话题文章失败: %w", err)
	}
	articleIDs := make([]int64, 0, len(topics)*2)
	seen := make(map[int64]bool)
	for _, t := range topics {
		t.LatestArticles = previews[t.ID]
		for _, a := range t.LatestArticles {
			if !seen[a.ID] {
				seen[a.ID] = true
				articleIDs = append(articleIDs, a.ID)
			}
		}
	}

	// 反重复：近 3 天（截至今晨 0 点）报告已覆盖的话题标题——同一天内重新生成报告不会互相排除
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	recentTitles, _ := g.db.GetRecentlyReportedTopicTitles(now.Add(-72*time.Hour), todayStart)

	// AI 选题（LLM 失败时回退为热度排序 + 分类去重）
	selected := g.selectTopics(topics, recentTitles)

	// 深度故事生成
	stories := make(map[int64]string)
	for _, t := range selected {
		stories[t.ID] = g.generateTopicStory(t)
	}

	// 快讯：未入选话题按热度排序
	var briefs []*models.Topic
	for _, t := range topics {
		if _, ok := stories[t.ID]; !ok {
			briefs = append(briefs, t)
		}
	}
	sort.Slice(briefs, func(i, j int) bool { return briefs[i].HeatScore > briefs[j].HeatScore })
	if len(briefs) > TopicsMaxBriefs {
		briefs = briefs[:TopicsMaxBriefs]
	}

	briefContent, fullContent := buildTopicsReportContent(selected, briefs, stories, reportType, len(articleIDs))

	// 保存报告并关联话题下的文章（关联记录支撑反重复查询）
	articles := make([]*models.Article, len(articleIDs))
	for i, id := range articleIDs {
		articles[i] = &models.Article{ID: id}
	}
	report, err := g.saveReport(briefContent, fullContent, articles, reportType)
	if err != nil {
		return nil, fmt.Errorf("保存报告失败: %w", err)
	}

	go g.pushReport(report)
	return report, nil
}

// selectTopics AI 选题：从候选话题中选出最多 3 个深度话题
// 选题原则（李自然方法论）：跨领域覆盖、多源交叉优先、避开近期已报道话题、宁缺毋滥
func (g *ReportGenerator) selectTopics(topics []*models.Topic, recentTitles []string) []*models.Topic {
	if g.analyzer != nil {
		if selected := g.selectTopicsByLLM(topics, recentTitles); len(selected) > 0 {
			return selected
		}
	}

	// 回退：热度排序 + 分类去重（尽量跨领域）
	sort.Slice(topics, func(i, j int) bool { return topics[i].HeatScore > topics[j].HeatScore })

	recent := make(map[string]bool, len(recentTitles))
	for _, t := range recentTitles {
		recent[t] = true
	}

	chosen := make(map[int64]bool)
	usedCategory := make(map[string]bool)
	var selected []*models.Topic
	for _, t := range topics {
		if len(selected) >= TopicsMaxFeatured {
			break
		}
		if chosen[t.ID] || recent[t.Title] || usedCategory[t.Category] {
			continue
		}
		chosen[t.ID] = true
		usedCategory[t.Category] = true
		selected = append(selected, t)
	}
	// 分类去重后不足 3 个时，允许同分类补齐
	for _, t := range topics {
		if len(selected) >= TopicsMaxFeatured {
			break
		}
		if chosen[t.ID] || recent[t.Title] {
			continue
		}
		chosen[t.ID] = true
		selected = append(selected, t)
	}
	return selected
}

// selectTopicsByLLM 用 LLM 完成选题，返回选中的话题（失败或无效时返回 nil）
func (g *ReportGenerator) selectTopicsByLLM(topics []*models.Topic, recentTitles []string) []*models.Topic {
	var sb strings.Builder
	sb.WriteString("你是一名新闻编辑，需要从今日候选话题中选出最多3个值得深入报道的话题。\n\n")
	sb.WriteString("选题原则：\n")
	sb.WriteString("1. 3个话题应覆盖不同领域（分类），避免同质化\n")
	sb.WriteString("2. 优先选择多个独立来源报道的话题（多源交叉验证是重要性信号）\n")
	sb.WriteString("3. 避开「近期已报道话题」列表中的话题，防止重复选题\n")
	sb.WriteString("4. 宁缺毋滥：没有值得深入的话题就不选满3个\n\n")

	if len(recentTitles) > 0 {
		sb.WriteString("近期已报道话题（避免重复）：\n")
		for i, t := range recentTitles {
			if i >= 30 {
				break
			}
			sb.WriteString(fmt.Sprintf("- %s\n", t))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("今日候选话题：\n")
	for _, t := range topics {
		fmt.Fprintf(&sb, "- id=%d｜%s｜分类：%s｜%d个来源｜%d篇报道｜热度%.1f｜%s\n",
			t.ID, t.Title, t.Category, t.SourceCount, t.ArticleCount, t.HeatScore, ellipsize(t.AISummary, 80))
	}
	sb.WriteString("\n请只输出 JSON（不要其他文字）：{\"selected\": [话题id, 话题id, 话题id]}")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := g.analyzer.ChatJSON(ctx, sb.String())
	if err != nil {
		log.Printf("话题选题 LLM 调用失败，回退热度排序: %v", err)
		return nil
	}

	var result struct {
		Selected []int64 `json:"selected"`
	}
	if err := json.Unmarshal([]byte(ai.ExtractJSONFromResponse(resp)), &result); err != nil {
		log.Printf("话题选题结果解析失败，回退热度排序: %v", err)
		return nil
	}

	byID := make(map[int64]*models.Topic, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	var selected []*models.Topic
	for _, id := range result.Selected {
		if t, ok := byID[id]; ok && len(selected) < TopicsMaxFeatured {
			selected = append(selected, t)
		}
	}
	return selected
}

// sourceLinks 生成话题的来源链接行（李自然式：每条新闻都能点回原文验证）
func sourceLinks(arts []*models.Article, totalSources int) string {
	if len(arts) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("**来源:** ")
	n := len(arts)
	if n > 4 {
		n = 4
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(fmt.Sprintf("[%s](%s)", arts[i].FeedTitle, arts[i].Link))
	}
	if totalSources > n {
		sb.WriteString(fmt.Sprintf(" (+%d)", totalSources-n))
	}
	return sb.String()
}

// generateTopicStory 为选中话题生成 150-250 字综合报道（LLM 失败时回退话题摘要）
func (g *ReportGenerator) generateTopicStory(topic *models.Topic) string {
	if g.analyzer == nil || len(topic.LatestArticles) == 0 {
		return topic.AISummary
	}
	prompt := buildTopicDigestPrompt(topic.Title, topic.LatestArticles,
		"你是一名新闻编辑，请基于以下同一话题的多篇报道，撰写一段150-250字的综合报道。\n要求：客观陈述事实，综合各来源信息，不添加推测，不使用夸张措辞。\n\n",
		"只输出报道正文。")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	story, err := g.analyzer.Chat(ctx, prompt)
	if err != nil || strings.TrimSpace(story) == "" {
		log.Printf("话题故事生成失败（topic %d），使用话题摘要: %v", topic.ID, err)
		return topic.AISummary
	}
	return strings.TrimSpace(stripMarkdownHeadings(story))
}

// stripMarkdownHeadings 剥离 LLM 输出开头的 Markdown 标题行与空行
// （prompt 要求"只输出正文"，但模型常自带标题）
func stripMarkdownHeadings(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 {
		t := strings.TrimSpace(lines[0])
		if strings.HasPrefix(t, "#") || t == "" {
			lines = lines[1:]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}

// buildTopicsReportContent 组装话题报告（简版 + 完整版）
// 展示对齐李自然日报：每条新闻附来源链接可点回原文验证、导语标注阅读时长
func buildTopicsReportContent(selected, briefs []*models.Topic, stories map[int64]string, reportType string, articleCount int) (string, string) {
	reportName := reportTypeName(reportType)

	// 简版：导语 + 3 个深度话题（附来源链接）+ 快讯（附首源链接）
	var brief strings.Builder
	brief.WriteString(fmt.Sprintf("# %s\n\n", reportName))
	brief.WriteString(fmt.Sprintf("> 今日 %d 个话题：%d 篇重点报道 · %d 条快讯 · 覆盖 %d 篇报道\n\n---\n\n",
		len(selected)+len(briefs), len(selected), len(briefs), articleCount))

	brief.WriteString("## 📌 今日重点\n\n")
	for i, t := range selected {
		brief.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, t.Title))
		if story, ok := stories[t.ID]; ok && story != "" {
			brief.WriteString(story)
			brief.WriteString("\n\n")
		}
		if links := sourceLinks(t.LatestArticles, t.SourceCount); links != "" {
			brief.WriteString(links + "\n\n")
		}
		brief.WriteString("---\n\n")
	}

	if len(briefs) > 0 {
		brief.WriteString("## 📰 快讯\n\n")
		for i, t := range briefs {
			brief.WriteString(fmt.Sprintf("%d. **%s** —— %s", i+1, t.Title, ellipsize(t.AISummary, 60)))
			if len(t.LatestArticles) > 0 {
				brief.WriteString(fmt.Sprintf(" ｜[%s](%s)", t.LatestArticles[0].FeedTitle, t.LatestArticles[0].Link))
			}
			brief.WriteString("\n")
		}
	}
	brief.WriteString("\n---\n")

	// 完整版：重点话题附全部新闻链接，快讯话题附主要链接
	var full strings.Builder
	full.WriteString(fmt.Sprintf("# %s - 完整版\n\n", reportName))
	full.WriteString(fmt.Sprintf("> 共 %d 个话题，每条均可点击查看原文\n\n---\n\n", len(selected)+len(briefs)))

	full.WriteString("## 📌 今日重点\n\n")
	for i, t := range selected {
		full.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, t.Title))
		if story, ok := stories[t.ID]; ok && story != "" {
			full.WriteString(story)
			full.WriteString("\n\n")
		}
		for _, art := range t.LatestArticles {
			full.WriteString(fmt.Sprintf("- [%s] [%s](%s)\n", art.FeedTitle, art.Title, art.Link))
		}
		full.WriteString("\n---\n\n")
	}

	if len(briefs) > 0 {
		full.WriteString("## 📰 快讯\n\n")
		for _, t := range briefs {
			full.WriteString(fmt.Sprintf("- **%s**", t.Title))
			for j, art := range t.LatestArticles {
				if j >= 2 {
					break
				}
				full.WriteString(fmt.Sprintf("｜[%s](%s)", art.FeedTitle, art.Link))
			}
			full.WriteString("\n")
		}
	}
	full.WriteString("\n---\n")

	return brief.String(), full.String()
}
