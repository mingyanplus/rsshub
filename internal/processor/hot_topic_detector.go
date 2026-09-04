package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
	"strings"
	"time"
)

// HotTopicDetector 热点检测器
type HotTopicDetector struct {
	db               *database.DB
	analyzer         *ai.Analyzer
	clusteringEngine *ClusteringEngine
	minArticles      int       // 最小文章数阈值
	minClusterSize   int       // 最小聚类大小
	timeWindow       time.Duration // 时间窗口
}

// NewHotTopicDetector 创建热点检测器
func NewHotTopicDetector(db *database.DB, analyzer *ai.Analyzer) *HotTopicDetector {
	return &HotTopicDetector{
		db:               db,
		analyzer:         analyzer,
		clusteringEngine: NewClusteringEngine(analyzer),
		minArticles:      5,      // 至少5篇相关文章
		minClusterSize:   3,      // 聚类至少3篇文章
		timeWindow:       24 * time.Hour, // 24小时内的文章
	}
}

// HotTopicCandidate 热点候选
type HotTopicCandidate struct {
	Name        string   `json:"name"`
	Keywords    []string `json:"keywords"`
	ArticleIDs  []int64  `json:"article_ids"`
	ArticleCount int     `json:"article_count"`
	Description string   `json:"description"`
	Trend       string   `json:"trend"` // rising, stable, declining
	Score       float64  `json:"score"` // 热度分数
}

// DetectHotTopics 检测热点事件
func (d *HotTopicDetector) DetectHotTopics(ctx context.Context) ([]*HotTopicCandidate, error) {
	// 1. 获取近期文章
	articles, err := d.db.GetRecentArticlesWithEmbedding(d.timeWindow)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent articles: %w", err)
	}

	if len(articles) < d.minArticles {
		log.Printf("文章数量不足 (%d < %d)，跳过热点检测", len(articles), d.minArticles)
		return nil, nil
	}

	log.Printf("开始热点检测，共 %d 篇文章", len(articles))

	// 2. 使用聚类引擎对文章进行聚类
	clusters := d.clusteringEngine.ClusterArticles(articles)
	if len(clusters) == 0 {
		return nil, nil
	}

	// 3. 筛选候选热点
	var candidates []*HotTopicCandidate
	for _, cluster := range clusters {
		if len(cluster.Articles) < d.minClusterSize {
			continue
		}

		// 计算热度分数
		score := d.calculateHotScore(cluster)

		// 只保留分数超过阈值的热点
		if score < 0.5 {
			continue
		}

		// 提取文章ID
		var articleIDs []int64
		for _, a := range cluster.Articles {
			articleIDs = append(articleIDs, a.ID)
		}

		candidate := &HotTopicCandidate{
			Name:         cluster.Name,
			Keywords:     cluster.Keywords,
			ArticleIDs:   articleIDs,
			ArticleCount: len(cluster.Articles),
			Description:  d.generateDescription(cluster),
			Score:        score,
		}

		candidates = append(candidates, candidate)
	}

	// 4. 使用LLM进一步筛选和命名热点
	candidates = d.refineCandidatesWithLLM(ctx, candidates)

	// 5. 检查是否已存在相似事件
	candidates = d.filterExistingEvents(candidates)

	log.Printf("检测到 %d 个热点候选", len(candidates))
	return candidates, nil
}

// calculateHotScore 计算热度分数
func (d *HotTopicDetector) calculateHotScore(cluster *models.ArticleCluster) float64 {
	// 因素1: 文章数量（归一化到0-1）
	articleScore := float64(len(cluster.Articles)) / 20.0
	if articleScore > 1 {
		articleScore = 1
	}

	// 因素2: 权威性（来自ClusterWeight）
	authorityScore := cluster.Weight.Authority

	// 因素3: 信息密度
	densityScore := cluster.Weight.Density

	// 因素4: 关键词数量（更多关键词可能代表更复杂的事件）
	keywordScore := float64(len(cluster.Keywords)) / 5.0
	if keywordScore > 1 {
		keywordScore = 1
	}

	// 综合得分
	score := articleScore*0.3 + authorityScore*0.3 + densityScore*0.2 + keywordScore*0.2

	return score
}

// generateDescription 生成事件描述
func (d *HotTopicDetector) generateDescription(cluster *models.ArticleCluster) string {
	if len(cluster.Articles) == 0 {
		return ""
	}

	// 使用第一篇文章的摘要作为基础
	desc := cluster.Articles[0].AISummary
	if desc == "" {
		desc = cluster.Articles[0].Summary
	}

	// 截断到200字符
	if len(desc) > 200 {
		desc = desc[:200] + "..."
	}

	return desc
}

// refineCandidatesWithLLM 使用LLM精炼候选热点
func (d *HotTopicDetector) refineCandidatesWithLLM(ctx context.Context, candidates []*HotTopicCandidate) []*HotTopicCandidate {
	if d.analyzer == nil || len(candidates) == 0 {
		return candidates
	}

	// 构建提示词
	var sb strings.Builder
	sb.WriteString("分析以下新闻聚类，判断哪些是值得关注的事件追踪主题。\n\n")

	for i, c := range candidates {
		fmt.Fprintf(&sb, "%d. %s (文章数: %d, 热度: %.2f)\n", i+1, c.Name, c.ArticleCount, c.Score)
		fmt.Fprintf(&sb, "   关键词: %s\n", strings.Join(c.Keywords, ", "))
		if c.Description != "" {
			fmt.Fprintf(&sb, "   描述: %s\n", c.Description)
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`
请返回JSON格式：
{
  "topics": [
    {
      "index": 1,
      "name": "更精确的事件名称",
      "is_worth_tracking": true,
      "priority": "high",
      "reason": "为什么值得追踪"
    }
  ]
}

要求：
- is_worth_tracking: 只有真正重要、有持续发展潜力的事件才设为true
- priority: high/medium/low
- 优先选择：重大突发事件、持续发展的行业动态、重要政策变化
- 排除：日常新闻、单次事件、无持续价值的资讯`)

	response, err := d.analyzer.Chat(ctx, sb.String())
	if err != nil {
		log.Printf("LLM精炼热点失败: %v", err)
		return candidates
	}

	// 解析响应
	var result struct {
		Topics []struct {
			Index           int    `json:"index"`
			Name            string `json:"name"`
			IsWorthTracking bool   `json:"is_worth_tracking"`
			Priority        string `json:"priority"`
			Reason          string `json:"reason"`
		} `json:"topics"`
	}

	// 使用公共函数提取 JSON
	response = ai.ExtractJSONFromResponse(response)

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		log.Printf("解析LLM响应失败: %v", err)
		return candidates
	}

	// 更新候选列表
	var refined []*HotTopicCandidate
	for _, t := range result.Topics {
		if t.Index < 1 || t.Index > len(candidates) {
			continue
		}
		c := candidates[t.Index-1]
		if t.IsWorthTracking {
			// 更新名称
			if t.Name != "" {
				c.Name = t.Name
			}
			refined = append(refined, c)
		}
	}

	return refined
}

// filterExistingEvents 过滤已存在的相似事件
func (d *HotTopicDetector) filterExistingEvents(candidates []*HotTopicCandidate) []*HotTopicCandidate {
	// 获取所有活跃和待处理的事件
	events, err := d.db.GetActiveAndPendingEventTracks()
	if err != nil {
		log.Printf("获取已有事件失败: %v", err)
		return candidates
	}

	var filtered []*HotTopicCandidate
	for _, c := range candidates {
		isDuplicate := false

		for _, e := range events {
			// 检查名称相似度
			if d.isSimilarName(c.Name, e.Name) {
				isDuplicate = true
				log.Printf("热点 '%s' 与已有事件 '%s' 相似，跳过", c.Name, e.Name)
				break
			}

			// 检查关键词重叠
			if d.hasKeywordOverlap(c.Keywords, e.Keywords) {
				isDuplicate = true
				log.Printf("热点 '%s' 与已有事件 '%s' 关键词重叠，跳过", c.Name, e.Name)
				break
			}
		}

		if !isDuplicate {
			filtered = append(filtered, c)
		}
	}

	return filtered
}

// isSimilarName 检查名称是否相似
func (d *HotTopicDetector) isSimilarName(name1, name2 string) bool {
	name1 = strings.ToLower(strings.TrimSpace(name1))
	name2 = strings.ToLower(strings.TrimSpace(name2))

	// 完全相同
	if name1 == name2 {
		return true
	}

	// 包含关系
	if strings.Contains(name1, name2) || strings.Contains(name2, name1) {
		return true
	}

	return false
}

// hasKeywordOverlap 检查关键词是否重叠
func (d *HotTopicDetector) hasKeywordOverlap(keywords1 []string, keywords2 string) bool {
	if keywords2 == "" || len(keywords1) == 0 {
		return false
	}

	kw2Set := make(map[string]bool)
	for _, kw := range strings.Split(keywords2, ",") {
		kw2Set[strings.TrimSpace(kw)] = true
	}

	overlap := 0
	for _, kw1 := range keywords1 {
		if kw2Set[kw1] {
			overlap++
		}
	}

	// 超过50%重叠
	return float64(overlap)/float64(len(keywords1)) > 0.5
}

// CreateEventFromCandidate 从候选创建事件（放入待关注列表）
func (d *HotTopicDetector) CreateEventFromCandidate(candidate *HotTopicCandidate) (*models.EventTrack, error) {
	// 生成事件向量（只用描述，描述最能表达追踪意图）
	var embedding []byte
	if d.analyzer != nil {
		embeddingText := candidate.Description
		if embeddingText == "" {
			embeddingText = candidate.Name + " " + strings.Join(candidate.Keywords, " ")
		}
		if embeddingText != "" {
			var err error
			embedding, err = d.analyzer.GetEmbedding(context.Background(), embeddingText)
			if err != nil {
				log.Printf("生成事件向量失败: %v", err)
			}
		}
	}

	event := &models.EventTrack{
		Name:        candidate.Name,
		Keywords:    strings.Join(candidate.Keywords, ","),
		Description: candidate.Description,
		Embedding:   embedding,
		Status:      "pending", // 自动发现的事件放入待关注列表
		IsAuto:      true,
		MatchCount:  0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	id, err := d.db.CreateEventTrack(event)
	if err != nil {
		return nil, err
	}
	event.ID = id

	log.Printf("创建待关注事件: %s (ID: %d)", event.Name, event.ID)
	return event, nil
}
