package processor

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"

	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
)

// 话题聚合默认参数
// 注：text-embedding-ada-002 等模型的余弦相似度区间偏窄（无关文本常在 0.7 上下），
// 因此合入采用「向量相似度 + 关键词/实体门控」双条件
const (
	DefaultTopicMinSimilarity = 0.86
	DefaultTopicActiveWindow  = 72 * time.Hour
	DefaultSummaryRefresh     = 6 * time.Hour
	topicArchiveWindow        = 7 * 24 * time.Hour

	// largeTopicThreshold / largeTopicMinSimilarity：大话题（成员多）更可能是宽泛的主题簇
	// 而非单一事件，合入门槛自动提高，防止"垃圾桶话题"继续吸附边缘内容
	largeTopicThreshold     = 20
	largeTopicMinSimilarity = 0.90

	// HotTopicsWindow 热榜时间窗口（话题页侧栏使用）
	HotTopicsWindow = 24 * time.Hour

	// topicCacheTTL 活跃话题缓存有效期（兜底外部改动）
	topicCacheTTL = 5 * time.Minute

	// summaryBackoff LLM 摘要失败后的全局退避时长（API 限流/配额耗尽时避免每次合入都阻塞等待）
	summaryBackoff = 5 * time.Minute
)

// TopicAggregator 话题聚合器：将 AI 分析完成的文章归入自动话题（Readhub 式话题流）
type TopicAggregator struct {
	db                  *database.DB
	analyzer            *ai.Analyzer
	minSimilarity       float64        // 合入既有话题的最小余弦相似度
	activeWindow        time.Duration  // 活跃话题窗口：超过该时长未更新的话题不再参与合入
	summaryRefresh      time.Duration  // 话题摘要 LLM 重写的最小间隔（控成本）
	archiveWindow       time.Duration  // 超过该时长无更新则归档
	mu                  sync.Mutex     // 串行化聚合，避免并发分析创建重复话题
	lastArchiveRun      time.Time      // 上次归档清理时间（内部节流）
	cache               []*cachedTopic // 活跃话题缓存（mu 保护下维护，合入/新建时同步更新）
	cacheAt             time.Time
	summaryBackoffUntil time.Time // LLM 摘要失败后的退避截止时间（全局）
}

// cachedTopic 缓存条目：预解析的关键词集合与全部成员向量（锚点式匹配用）
type cachedTopic struct {
	topic    *models.Topic
	keywords map[string]bool
	vectors  [][]float32 // 话题内全部成员文章的向量
}

// NewTopicAggregator 创建话题聚合器（参数取包内默认值，需要调整时改常量）
func NewTopicAggregator(db *database.DB, analyzer *ai.Analyzer) *TopicAggregator {
	return &TopicAggregator{
		db:             db,
		analyzer:       analyzer,
		minSimilarity:  DefaultTopicMinSimilarity,
		activeWindow:   DefaultTopicActiveWindow,
		summaryRefresh: DefaultSummaryRefresh,
		archiveWindow:  topicArchiveWindow,
	}
}

// AggregateArticle 将一篇文章聚合进话题（在 AI 分析与向量化完成后调用）
func (a *TopicAggregator) AggregateArticle(articleID int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.archiveStaleTopicsThrottled()

	article, err := a.db.GetArticleForAggregation(articleID)
	if err != nil {
		return fmt.Errorf("failed to get article %d: %w", articleID, err)
	}

	// 广告/垃圾/无实质内容的文章不参与话题聚合
	if article.IsAd || article.ImportanceScore <= 1 || models.IsPlaceholderKeywords(article.Keywords) {
		return nil
	}

	// 博客/独立作品不参与话题聚合（分类级标记；只有新闻/时事才做多源事件聚合，
	// 博客各有观点，合并会互相埋没，由「文章列表」承接展示）
	if article.FeedContentType == "blog" {
		return nil
	}

	// 话题向量：优先使用总结向量（更精确），没有则用全文向量
	vecBytes := article.SummaryEmbedding
	if len(vecBytes) == 0 {
		vecBytes = article.Embedding
	}
	if len(vecBytes) == 0 {
		return nil
	}
	articleVec, err := ai.DeserializeEmbedding(vecBytes)
	if err != nil || len(articleVec) == 0 {
		return fmt.Errorf("failed to deserialize article vector: %w", err)
	}

	// 与活跃话题锚点式匹配：新文章与话题内任意单篇的最高相似度过阈值才合入
	// （不用质心平均——平均后的向量会漂移成"主题质心"，把同主题不同事件全吸进来）
	actives, err := a.activeTopics()
	if err != nil {
		return fmt.Errorf("failed to get active topics: %w", err)
	}
	entitySet := keywordSet(article.Entities)
	articleKeywords := keywordSet(article.Keywords)
	var best *cachedTopic
	bestSim := 0.0
	for _, c := range actives {
		if !passesGate(entitySet, articleKeywords, c.keywords) {
			continue
		}
		threshold := a.minSimilarity
		if c.topic.ArticleCount > largeTopicThreshold {
			threshold = largeTopicMinSimilarity
		}
		sim := 0.0
		for _, mv := range c.vectors {
			if s := safeCosineSimilarity(articleVec, mv); s > sim {
				sim = s
			}
		}
		if sim >= threshold && sim > bestSim {
			bestSim, best = sim, c
		}
	}

	if best != nil && bestSim >= a.minSimilarity {
		return a.mergeIntoTopic(best, article, bestSim, articleVec)
	}
	return a.createTopic(article, articleVec, vecBytes)
}

// activeTopics 获取活跃话题缓存（过期或首次访问时从数据库加载并预解析成员向量）
func (a *TopicAggregator) activeTopics() ([]*cachedTopic, error) {
	if a.cache == nil || time.Since(a.cacheAt) > topicCacheTTL {
		topics, err := a.db.GetActiveTopicsForAggregation(a.activeWindow)
		if err != nil {
			return nil, err
		}
		memberVecs, err := a.db.GetActiveTopicMemberVectors(a.activeWindow)
		if err != nil {
			return nil, err
		}
		cached := make([]*cachedTopic, 0, len(topics))
		for _, t := range topics {
			c := &cachedTopic{topic: t, keywords: keywordSet(t.Keywords)}
			for _, vb := range memberVecs[t.ID] {
				if vec, err := ai.DeserializeEmbedding(vb); err == nil && len(vec) > 0 {
					c.vectors = append(c.vectors, vec)
				}
			}
			cached = append(cached, c)
		}
		a.cache = cached
		a.cacheAt = time.Now()
	}
	return a.cache, nil
}

// Invalidate 清空活跃话题缓存（外部清空/修改话题数据后调用）
func (a *TopicAggregator) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cache = nil
}

// mergeIntoTopic 将文章合入既有话题（同步更新缓存）
func (a *TopicAggregator) mergeIntoTopic(c *cachedTopic, article *models.Article, similarity float64, articleVec []float32) error {
	topic := c.topic
	inserted, err := a.db.AddArticleToTopic(topic.ID, article.ID, similarity)
	if err != nil {
		return fmt.Errorf("failed to add article %d to topic %d: %w", article.ID, topic.ID, err)
	}
	if !inserted {
		return nil // 文章已在该话题中
	}

	// 重算统计与热度（多源交叉验证是最强重要性信号，来源权威度为第二信号）
	articleCount, sourceCount, avgImportance, maxAuthority, err := a.db.GetTopicArticleStats(topic.ID)
	if err != nil {
		return fmt.Errorf("failed to get topic stats %d: %w", topic.ID, err)
	}

	// 合并关键词集合（门控依据，控制上限）
	mergedKeywords := mergeKeywordSets(topic.Keywords, article.Keywords, article.Entities)

	if err := a.db.UpdateTopicStats(topic.ID, articleCount, sourceCount,
		computeTopicHeat(articleCount, sourceCount, avgImportance, maxAuthority), mergedKeywords, topic.Embedding); err != nil {
		return fmt.Errorf("failed to update topic stats %d: %w", topic.ID, err)
	}

	// 同步缓存：追加新成员向量（锚点式），更新话题状态；topic.Embedding 保持为首篇锚点向量
	topic.ArticleCount = articleCount
	topic.SourceCount = sourceCount
	topic.Keywords = mergedKeywords
	c.keywords = keywordSet(mergedKeywords)
	c.vectors = append(c.vectors, articleVec)

	log.Printf("TopicAggregator: article %d merged into topic %d (%s), sim=%.3f, articles=%d, sources=%d",
		article.ID, topic.ID, topic.Title, similarity, articleCount, sourceCount)

	// 多源后用 LLM 综合改写话题摘要（限流：距上次重写超过 summaryRefresh；
	// API 失败后全局退避，避免配额耗尽/限流时每次合入都阻塞等待）
	if articleCount >= 2 && time.Now().After(a.summaryBackoffUntil) &&
		(topic.SummaryUpdatedAt == nil || time.Since(*topic.SummaryUpdatedAt) > a.summaryRefresh) {
		a.refreshTopicSummary(topic)
	}
	return nil
}

// 频道大类（话题分类收敛目标，避免 AI 生成的细粒度分类碎片化）
const (
	CategoryTech    = "科技"
	CategoryAI      = "AI"
	CategoryFinance = "财经"
	CategoryWorld   = "国际"
	CategorySociety = "社会"
	CategoryLife    = "生活"
	CategoryHealth  = "健康"
	CategoryOther   = "其他"
)

// normalizeCategory 将 AI 生成的细粒度主题分类（如"社会观察""生活感悟""产品发布"）收敛为频道大类
func normalizeCategory(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return CategoryOther
	case strings.Contains(s, "AI") || strings.Contains(s, "人工智能") || strings.Contains(s, "大模型"):
		return CategoryAI
	case strings.ContainsAny(s, "财经金融投资股市经济商业"):
		return CategoryFinance
	case strings.ContainsAny(s, "国际军事外交地缘"):
		return CategoryWorld
	case strings.ContainsAny(s, "健康医疗养生医学"):
		return CategoryHealth
	case strings.ContainsAny(s, "生活个人情感文化娱乐体育职场教育读书知识"):
		return CategoryLife
	case strings.ContainsAny(s, "社会时政评论观点法律政策"):
		return CategorySociety
	case strings.ContainsAny(s, "科技技术软件硬件互联网数码开源产品公司行业编程工程科学"):
		return CategoryTech
	default:
		return CategoryOther
	}
}

// createTopic 为文章创建新话题（并加入缓存）
func (a *TopicAggregator) createTopic(article *models.Article, articleVec []float32, vecBytes []byte) error {
	now := time.Now()
	firstAt := now
	if article.PublishedAt != nil {
		firstAt = *article.PublishedAt
	}
	// 摘要优先级：AI 摘要（精炼、已剔除噪音）→ RSS 原文摘要 → 标题
	summary := stripHTMLTags(firstNonEmpty(article.AISummary, article.Summary))
	if summary == "" {
		summary = stripHTMLTags(article.Title)
	}
	topic := &models.Topic{
		Title:          truncateRunes(trimLeadingSymbols(article.Title), 200),
		AISummary:      summary,
		EntityKey:      firstNonEmpty(article.Entities, article.Keywords),
		Keywords:       mergeKeywordSets("", article.Keywords, article.Entities),
		Category:       normalizeCategory(article.TopicCategory),
		HeatScore:      computeTopicHeat(1, 1, float64(article.ImportanceScore), 3),
		ArticleCount:   1,
		SourceCount:    1,
		Embedding:      vecBytes,
		FirstArticleAt: firstAt,
		LastUpdatedAt:  now,
	}
	topicID, err := a.db.CreateTopic(topic)
	if err != nil {
		return fmt.Errorf("failed to create topic: %w", err)
	}
	topic.ID = topicID
	if _, err := a.db.AddArticleToTopic(topicID, article.ID, 1.0); err != nil {
		return fmt.Errorf("failed to add article %d to new topic %d: %w", article.ID, topicID, err)
	}
	a.cache = append(a.cache, &cachedTopic{topic: topic, keywords: keywordSet(topic.Keywords), vectors: [][]float32{articleVec}})
	log.Printf("TopicAggregator: created topic %d (%s) from article %d", topicID, topic.Title, article.ID)
	return nil
}

// refreshTopicSummary 用 LLM 综合话题下各篇报道，改写话题摘要
func (a *TopicAggregator) refreshTopicSummary(topic *models.Topic) {
	articles, err := a.db.GetTopicArticles(topic.ID, 6, 0)
	if err != nil || len(articles) < 2 {
		return
	}
	prompt := buildTopicDigestPrompt(topic.Title, articles,
		"你是一名新闻编辑。以下是对同一话题的多篇报道。请综合这些报道生成一段150-250字的话题摘要：客观陈述事实，覆盖各篇的关键信息，不添加推测，不使用夸张措辞。\n\n",
		"只输出摘要正文，不要标题和任何额外说明。")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	newSummary, err := a.analyzer.Chat(ctx, prompt)
	if err != nil {
		// API 故障（限流/配额耗尽）时全局退避，期间不再尝试摘要生成
		a.summaryBackoffUntil = time.Now().Add(summaryBackoff)
		log.Printf("TopicAggregator: failed to refresh summary for topic %d (backoff %v): %v", topic.ID, summaryBackoff, err)
		return
	}
	newSummary = strings.TrimSpace(stripMarkdownHeadings(newSummary))
	if newSummary == "" {
		return
	}
	if err := a.db.UpdateTopicSummary(topic.ID, newSummary); err != nil {
		log.Printf("TopicAggregator: failed to save summary for topic %d: %v", topic.ID, err)
		return
	}
	// 同步内存时间戳，避免缓存内话题反复触发限流窗口判断失效
	now := time.Now()
	topic.SummaryUpdatedAt = &now
	topic.AISummary = newSummary
}

// archiveStaleTopicsThrottled 归档长期无更新的话题（每小时最多执行一次）
func (a *TopicAggregator) archiveStaleTopicsThrottled() {
	if time.Since(a.lastArchiveRun) < time.Hour {
		return
	}
	a.lastArchiveRun = time.Now()
	if n, err := a.db.ArchiveStaleTopics(a.archiveWindow); err != nil {
		log.Printf("TopicAggregator: failed to archive stale topics: %v", err)
	} else if n > 0 {
		log.Printf("TopicAggregator: archived %d stale topics", n)
		a.cache = nil // 归档改变了活跃集合，缓存失效
	}
}

// buildTopicDigestPrompt 构造「同一话题多源报道 → LLM 综合」的 prompt（话题摘要与报告故事共用）
func buildTopicDigestPrompt(topicTitle string, arts []*models.Article, instruction, closing string) string {
	var sb strings.Builder
	sb.WriteString(instruction)
	fmt.Fprintf(&sb, "话题标题：%s\n\n报道列表：\n", topicTitle)
	n := 0
	for _, art := range arts {
		source := art.FeedTitle
		if source == "" {
			source = "来源"
		}
		summary := getArticleSummary(art)
		if summary == "" {
			continue
		}
		n++
		fmt.Fprintf(&sb, "%d. [%s] %s：%s\n\n", n, source, trimLeadingSymbols(art.Title), summary)
	}
	sb.WriteString(closing)
	return sb.String()
}

// computeTopicHeat 计算话题热度：多源交叉验证（独立源数）> 来源权威度 > 文章数 > 平均重要性
func computeTopicHeat(articleCount, sourceCount int, avgImportance, maxAuthority float64) float64 {
	heat := avgImportance*0.3 + math.Min(float64(sourceCount), 6)*1.5 + math.Min(float64(articleCount), 10)*0.5 + maxAuthority*0.6
	return math.Round(heat*100) / 100
}

// safeCosineSimilarity 余弦相似度的防御包装：话题/文章向量可能来自不同批次或模型，
// 长度不一致视为不相关（返回 0）而不是 panic
func safeCosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	return ai.CalculateCosineSimilarity(a, b)
}

// passesGate 合入门控：优先实体交集（实体是事件级信号，泛关键词容易误放行），
// 文章无实体时退化为关键词交集；话题无关键词时不设门控（纯向量匹配）
func passesGate(entitySet, articleKeywords, topicSet map[string]bool) bool {
	if len(topicSet) == 0 {
		return true
	}
	if len(entitySet) > 0 {
		return overlaps(entitySet, topicSet)
	}
	return overlaps(articleKeywords, topicSet)
}

// overlaps 判断两个集合是否有交集
func overlaps(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// normalizeKeyword 规范化单个关键词：去空白、转小写、过滤占位标记（空串表示丢弃）
func normalizeKeyword(k string) string {
	k = strings.TrimSpace(strings.ToLower(k))
	if k == models.KeywordPlaceholderMarked || k == models.KeywordPlaceholderFiltered {
		return ""
	}
	return k
}

// keywordSet 将逗号分隔的关键词转为规范化集合
func keywordSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, k := range strings.Split(s, ",") {
		if k = normalizeKeyword(k); k != "" {
			set[k] = true
		}
	}
	return set
}

// mergeKeywordSets 合并话题与文章的关键词/实体集合（实体优先，旧词保序），限制数量上限
func mergeKeywordSets(topicKeywords, articleKeywords, articleEntities string) string {
	seen := make(map[string]bool)
	var result []string
	addList := func(s string) {
		for _, k := range strings.Split(s, ",") {
			if k = normalizeKeyword(k); k != "" && !seen[k] {
				seen[k] = true
				result = append(result, k)
			}
		}
	}
	addList(articleEntities)
	addList(topicKeywords)
	addList(articleKeywords)
	if len(result) > 12 {
		result = result[:12]
	}
	return strings.Join(result, ",")
}

// firstNonEmpty 取第一个非空实体（实体列表 > 关键词列表，均逗号分隔）
func firstNonEmpty(entities, keywords string) string {
	for _, s := range strings.Split(entities, ",") {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	if models.IsPlaceholderKeywords(keywords) {
		return ""
	}
	for _, s := range strings.Split(keywords, ",") {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// truncateRunes 按字符截断字符串
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// ellipsize 截断到 max 个字符，超出部分以省略号结尾
func ellipsize(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// stripHTMLTags 去除文本中的 HTML 标签并压缩空白（清洗 RSS 摘要里的原始 HTML）
func stripHTMLTags(s string) string {
	s = htmlTagRegex.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// trimLeadingSymbols 去除标题开头的 emoji/箭头/符号前缀与空白（如"↩️ "、"🖼 "）
func trimLeadingSymbols(s string) string {
	s = strings.TrimSpace(s)
	for _, r := range s {
		if isEmojiRune(r) || unicode.IsSpace(r) || unicode.IsSymbol(r) ||
			(r >= 0x2190 && r <= 0x21FF) || r == 0xFE0F || r == 0x200D {
			s = strings.TrimSpace(strings.TrimPrefix(s, string(r)))
			continue
		}
		break
	}
	return strings.TrimSpace(s)
}
