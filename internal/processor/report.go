package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
	"rss-ai/internal/notify"
	"sort"
	"strings"
	"sync"
	"time"
)

// 报告配置
const (
	MaxFeaturedTopics   = 5  // 重点主题数量
	MaxBriefArticles    = 10 // 简讯数量
	MinArticlesRequired = 5  // 最少文章数量
	LLMRequestInterval  = 2 * time.Second // LLM 请求间隔
)

// ReportGeneratorConfig 报告生成器配置
type ReportGeneratorConfig struct {
	MaxConcurrentLLM int
	LLMTimeout       time.Duration
	EnableParallel   bool
	RequestInterval  time.Duration // 请求间隔，避免限流
}

// DefaultReportGeneratorConfig 默认配置
func DefaultReportGeneratorConfig() ReportGeneratorConfig {
	return ReportGeneratorConfig{
		MaxConcurrentLLM: 1, // 串行模式
		LLMTimeout:       90 * time.Second,
		EnableParallel:   false, // 关闭并发，避免限流
		RequestInterval:  LLMRequestInterval,
	}
}

// ReportGenerator 报告生成器
type ReportGenerator struct {
	db        *database.DB
	analyzer  *ai.Analyzer
	notifyMgr *notify.Manager
	channels  string
	baseURL   string
	// 新字段
	clustering    *ClusteringEngine
	weightCalc    *WeightCalculator
	topicMatcher  *TopicMatcher
	promptBuilder *PromptBuilder
	config        ReportGeneratorConfig
}

// NewReportGenerator 创建报告生成器
func NewReportGenerator(db *database.DB, analyzer *ai.Analyzer, notifyMgr *notify.Manager, channels string) *ReportGenerator {
	return &ReportGenerator{
		db:            db,
		analyzer:      analyzer,
		notifyMgr:     notifyMgr,
		channels:      channels,
		baseURL:       "http://127.0.0.1:8080",
		clustering:    NewClusteringEngine(analyzer),
		weightCalc:    NewWeightCalculator(),
		topicMatcher:  NewTopicMatcher(),
		promptBuilder: NewPromptBuilder(),
		config:        DefaultReportGeneratorConfig(),
	}
}

// SetBaseURL 设置基础URL
func (g *ReportGenerator) SetBaseURL(url string) {
	g.baseURL = url
}

// Generate 生成报告
func (g *ReportGenerator) Generate(reportType string) (*models.Report, error) {
	log.Printf("开始生成 %s 报告...", reportType)

	// 优先走话题聚合路径（李自然式：基于持久化话题 + AI 选题 + 反重复）
	// 报告时段内没有话题时返回 nil，回退到下方文章聚类路径
	if report, err := g.generateFromTopics(reportType); report != nil || err != nil {
		return report, err
	}

	// Phase 1: Get articles
	articles, err := g.getArticlesForPeriod(reportType)
	if err != nil {
		return nil, fmt.Errorf("获取文章失败: %w", err)
	}

	if len(articles) < MinArticlesRequired {
		log.Printf("文章数量不足（%d篇），跳过报告生成", len(articles))
		return nil, nil
	}

	log.Printf("获取到 %d 篇文章", len(articles))

	// Phase 2: Smart clustering
	clusters := g.clustering.ClusterArticles(articles)
	log.Printf("聚类完成，共 %d 个主题块", len(clusters))

	// Phase 3: Weight calculation and sorting
	for _, c := range clusters {
		c.Weight = g.weightCalc.CalculateClusterWeight(c, clusters)
	}
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Weight.FinalScore > clusters[j].Weight.FinalScore
	})

	// Phase 4: Select top clusters
	maxFeatured := MaxFeaturedTopics
	maxBrief := MaxBriefArticles
	if len(clusters) < maxFeatured {
		maxFeatured = len(clusters)
	}
	if len(clusters)-maxFeatured < maxBrief {
		maxBrief = len(clusters) - maxFeatured
	}
	if maxBrief < 0 {
		maxBrief = 0
	}

	featuredClusters := clusters[:maxFeatured]
	briefClusters := clusters[maxFeatured : maxFeatured+maxBrief]

	log.Printf("精选完成：%d 个重点主题，%d 条简讯", len(featuredClusters), len(briefClusters))

	// Phase 5: Parallel LLM generation
	featuredContents, briefContents := g.generateAllContents(featuredClusters, briefClusters)

	// Phase 6: Build report content
	briefContent, fullContent := g.buildReportContent(featuredClusters, briefClusters, featuredContents, briefContents, articles, reportType)

	// Phase 7: Save report
	report, err := g.saveReport(briefContent, fullContent, articles, reportType)
	if err != nil {
		return nil, fmt.Errorf("保存报告失败: %w", err)
	}

	// Async push
	go g.pushReport(report)

	return report, nil
}

// reportPeriod 根据报告类型返回时间范围
// 早报 8:00 推送 → 获取昨天 20:00 ~ 今天 8:00 的文章
// 晚报 20:00 推送 → 获取今天 8:00 ~ 今天 20:00 的文章
// 日报 22:00 推送 → 获取今天 0:00 ~ 24:00 的文章
// reportTypeName 根据报告类型返回带 emoji 与日期的报告名（如 "🌅 2026-09-04 早报"）
func reportTypeName(reportType string) string {
	dateStr := time.Now().Format("2006-01-02")
	switch reportType {
	case "morning":
		return fmt.Sprintf("🌅 %s 早报", dateStr)
	case "daily":
		return fmt.Sprintf("📅 %s 日报", dateStr)
	default:
		return fmt.Sprintf("🌙 %s 晚报", dateStr)
	}
}

// reportPeriod 根据报告类型返回时间范围
// 早报 8:00 推送 → 获取昨天 20:00 ~ 今天 8:00 的文章
// 晚报 20:00 推送 → 获取今天 8:00 ~ 今天 20:00 的文章
// 日报 22:00 推送 → 获取今天 0:00 ~ 24:00 的文章
func reportPeriod(reportType string) (time.Time, time.Time) {
	now := time.Now()
	switch reportType {
	case "morning":
		return time.Date(now.Year(), now.Month(), now.Day(), 20, 0, 0, 0, now.Location()).Add(-24 * time.Hour),
			time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, now.Location())
	case "evening":
		return time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, now.Location()),
			time.Date(now.Year(), now.Month(), now.Day(), 20, 0, 0, 0, now.Location())
	case "daily":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()),
			time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(24 * time.Hour)
	default:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), now
	}
}

// getArticlesForPeriod 根据报告类型获取时间范围内的文章
func (g *ReportGenerator) getArticlesForPeriod(reportType string) ([]*models.Article, error) {
	startTime, endTime := reportPeriod(reportType)

	articles, err := g.db.GetArticlesForReportBetween(startTime, endTime)
	if err != nil {
		return nil, err
	}

	// 过滤广告
	var filtered []*models.Article
	for _, a := range articles {
		if !a.IsAd {
			filtered = append(filtered, a)
		}
	}

	return filtered, nil
}

// generateAllContents 并发生成所有内容
func (g *ReportGenerator) generateAllContents(featuredClusters, briefClusters []*models.ArticleCluster) ([]string, []string) {
	featuredContents := make([]string, len(featuredClusters))
	briefContents := make([]string, len(briefClusters))

	if !g.config.EnableParallel || g.analyzer == nil {
		// Serial generation with interval to avoid rate limit
		for i, c := range featuredClusters {
			if i > 0 && g.config.RequestInterval > 0 {
				time.Sleep(g.config.RequestInterval)
			}
			prompt := g.topicMatcher.GetPromptForCluster(c)
			featuredContents[i] = g.generateTopicContent(c, prompt, true)
		}
		for i, c := range briefClusters {
			// 简讯和重点报道之间也需要间隔
			if (len(featuredClusters) > 0 || i > 0) && g.config.RequestInterval > 0 {
				time.Sleep(g.config.RequestInterval)
			}
			prompt := g.topicMatcher.GetPromptForCluster(c)
			briefContents[i] = g.generateTopicContent(c, prompt, false)
		}
		return featuredContents, briefContents
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, g.config.MaxConcurrentLLM)

	// Generate featured reports
	for i, c := range featuredClusters {
		wg.Add(1)
		go func(idx int, cluster *models.ArticleCluster) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			prompt := g.topicMatcher.GetPromptForCluster(cluster)
			content := g.generateTopicContent(cluster, prompt, true)
			featuredContents[idx] = content
		}(i, c)
	}

	// Generate briefs
	for i, c := range briefClusters {
		wg.Add(1)
		go func(idx int, cluster *models.ArticleCluster) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			prompt := g.topicMatcher.GetPromptForCluster(cluster)
			content := g.generateTopicContent(cluster, prompt, false)
			briefContents[idx] = content
		}(i, c)
	}

	wg.Wait()
	return featuredContents, briefContents
}

// generateTopicContent 生成单个主题内容
func (g *ReportGenerator) generateTopicContent(cluster *models.ArticleCluster, prompt *models.TopicPrompt, isFeatured bool) string {
	if g.analyzer == nil {
		return g.fallbackContent(cluster, isFeatured)
	}

	ctx, cancel := context.WithTimeout(context.Background(), g.config.LLMTimeout)
	defer cancel()

	var fullPrompt string
	if isFeatured {
		fullPrompt = g.promptBuilder.BuildFeaturedPrompt(cluster, prompt)
	} else {
		fullPrompt = g.promptBuilder.BuildBriefPrompt(cluster, prompt)
	}

	// 使用 JSON 模式调用 LLM
	response, err := g.analyzer.ChatJSON(ctx, fullPrompt)
	if err != nil {
		log.Printf("主题 %s LLM 生成失败: %v，使用降级方案", cluster.Name, err)
		return g.fallbackContent(cluster, isFeatured)
	}

	// 解析 JSON 响应并格式化
	return g.formatJSONResponse(response, cluster, isFeatured)
}

// formatJSONResponse 解析并格式化 JSON 响应
func (g *ReportGenerator) formatJSONResponse(response string, cluster *models.ArticleCluster, isFeatured bool) string {
	// 清理可能的 markdown 代码块标记
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	log.Printf("DEBUG: 主题 %s LLM 原始响应 (前500字符): %s", cluster.Name, truncate(response, 500))

	if isFeatured {
		var report FeaturedReportJSON
		if err := json.Unmarshal([]byte(response), &report); err != nil {
			log.Printf("解析重点报道 JSON 失败: %v，使用降级方案", err)
			return g.fallbackContent(cluster, true)
		}

		log.Printf("DEBUG: 解析成功 - Content长度:%d, KeyPoints数:%d, Insight长度:%d",
			len(report.Content), len(report.KeyPoints), len(report.Insight))

		return FormatFeaturedReport(&report, cluster.Articles)
	}

	// 简讯
	var report BriefReportJSON
	if err := json.Unmarshal([]byte(response), &report); err != nil {
		log.Printf("解析简讯 JSON 失败: %v，使用降级方案", err)
		return g.fallbackContent(cluster, false)
	}
	return FormatBriefReport(&report, cluster.Articles)
}

// getArticleSummary 获取文章摘要，优先使用 AI 摘要，否则清理原始摘要中的 HTML
func getArticleSummary(a *models.Article) string {
	if a.AISummary != "" {
		return a.AISummary
	}
	// 清理 HTML 标签（使用 prompts.go 中定义的 htmlTagRegex）
	return strings.TrimSpace(htmlTagRegex.ReplaceAllString(a.Summary, ""))
}

// fallbackContent 降级内容生成
func (g *ReportGenerator) fallbackContent(cluster *models.ArticleCluster, isFeatured bool) string {
	var sb strings.Builder

	if len(cluster.Articles) == 0 {
		return ""
	}

	if isFeatured {
		// 重点报道：只使用第一篇文章的内容，避免重复
		a := cluster.Articles[0]
		summary := getArticleSummary(a)
		sb.WriteString(fmt.Sprintf("**%s**\n\n%s", a.Title, summary))

		// 列出所有来源链接（使用序号格式，悬停显示标题）
		sb.WriteString("\n\n**来源:** ")
		for i, a := range cluster.Articles {
			if i > 0 {
				sb.WriteString(", ")
			}
			// 使用序号作为显示文本，完整标题放在 title 属性中
			// Markdown 链接格式: [显示文本](URL "title")
			sb.WriteString(fmt.Sprintf("[%d](%s \"%s\")", i+1, a.Link, strings.ReplaceAll(a.Title, "\"", "'")))
		}
	} else {
		// 简讯：使用第一篇文章（不截断，完整显示）
		a := cluster.Articles[0]
		summary := getArticleSummary(a)
		// 使用序号格式，悬停显示标题
		sb.WriteString(fmt.Sprintf("%s [%d](%s \"%s\")", summary, 1, a.Link, strings.ReplaceAll(a.Title, "\"", "'")))
	}

	return sb.String()
}

// buildReportContent 构建报告内容
func (g *ReportGenerator) buildReportContent(featuredClusters, briefClusters []*models.ArticleCluster, featuredContents, briefContents []string, allArticles []*models.Article, reportType string) (string, string) {
	reportName := reportTypeName(reportType)

	// Brief version
	var briefBuilder strings.Builder
	briefBuilder.WriteString(fmt.Sprintf("# %s\n\n", reportName))
	briefBuilder.WriteString(fmt.Sprintf("> 从 %d 篇文章中智能精选，AI 自动生成\n\n---\n\n", len(allArticles)))

	// Featured topics
	briefBuilder.WriteString("## 📌 重点关注\n\n")
	for i, content := range featuredContents {
		if i < len(featuredClusters) {
			briefBuilder.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, featuredClusters[i].Name))
			briefBuilder.WriteString(content)
			briefBuilder.WriteString("\n\n---\n\n")
		}
	}

	// Briefs
	briefBuilder.WriteString("## 📰 快讯\n\n")
	for i, content := range briefContents {
		briefBuilder.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, content))
	}

	briefBuilder.WriteString("\n---\n")

	// Full version
	var fullBuilder strings.Builder
	fullBuilder.WriteString(fmt.Sprintf("# %s - 完整版\n\n", reportName))
	fullBuilder.WriteString(fmt.Sprintf("> 共 %d 篇文章，按时间线展示\n\n---\n\n", len(allArticles)))

	// Sort by time
	sort.Slice(allArticles, func(i, j int) bool {
		return allArticles[i].FetchedAt.After(allArticles[j].FetchedAt)
	})

	currentDate := ""
	for _, article := range allArticles {
		dateStr := article.FetchedAt.Format("2006-01-02")
		if dateStr != currentDate {
			currentDate = dateStr
			fullBuilder.WriteString(fmt.Sprintf("### %s\n\n", dateStr))
		}

		timeStr := article.FetchedAt.Format("15:04")

		// Check if featured
		isFeatured := false
		for _, c := range featuredClusters {
			for _, a := range c.Articles {
				if a.ID == article.ID {
					isFeatured = true
					break
				}
			}
		}

		if isFeatured {
			fullBuilder.WriteString(fmt.Sprintf("#### %s 🔥 %s\n\n", timeStr, article.Title))
			if article.Summary != "" {
				fullBuilder.WriteString(fmt.Sprintf("%s\n\n", stripHTMLTags(article.Summary)))
			}
			fullBuilder.WriteString(fmt.Sprintf("[查看原文](%s)\n\n---\n\n", article.Link))
		} else {
			fullBuilder.WriteString(fmt.Sprintf("- **%s** [%s](%s)\n", timeStr, article.Title, article.Link))
		}
	}

	return briefBuilder.String(), fullBuilder.String()
}

// saveReport 保存报告
func (g *ReportGenerator) saveReport(briefContent, fullContent string, articles []*models.Article, reportType string) (*models.Report, error) {
	now := time.Now()
	reportName := reportTypeName(reportType)

	report := &models.Report{
		Name:         reportName,
		Type:         reportType,
		Content:      fullContent,
		Summary:      briefContent,
		ArticleCount: len(articles),
		CreatedAt:    now,
	}

	reportID, err := g.db.SaveReport(report)
	if err != nil {
		return nil, err
	}
	report.ID = reportID

	// 保存报告-文章关联
	articleIDs := make([]int64, len(articles))
	for i, a := range articles {
		articleIDs[i] = a.ID
	}
	g.db.SaveReportArticles(reportID, articleIDs)

	return report, nil
}

// pushReport 推送报告
func (g *ReportGenerator) pushReport(report *models.Report) {
	g.PushReport(report)
}

// PushReport 公共推送报告方法（供外部调用，如重新推送）
func (g *ReportGenerator) PushReport(report *models.Report) {
	if g.notifyMgr == nil || g.channels == "" {
		return
	}

	channels := notify.ParseChannelsFromStr(g.channels)
	if len(channels) == 0 {
		return
	}

	pushContent := report.Summary
	if pushContent == "" {
		pushContent = report.Content
	}

	for _, channel := range channels {
		msg := &notify.Message{
			Title:   report.Name,
			Content: pushContent,
		}
		result := g.notifyMgr.Send(channel, msg)
		if !result.Success {
			log.Printf("推送到 %s 失败: %s", channel, result.Error)
		} else {
			log.Printf("推送到 %s 成功", channel)
		}

		status := "sent"
		if !result.Success {
			status = "failed"
		}
		g.db.CreateNotification(report.ID, string(channel), pushContent, status)
	}

	g.db.UpdateReportSent(report.ID)
}

// truncate 截断字符串到指定长度
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
