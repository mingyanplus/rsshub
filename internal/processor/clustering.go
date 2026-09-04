package processor

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"rss-ai/internal/ai"
	"rss-ai/internal/models"
	"strings"
)

// ClusteringEngine 聚类引擎
type ClusteringEngine struct {
	analyzer      *ai.Analyzer
	llmClassifier *LLMClassifier
}

// NewClusteringEngine 创建聚类引擎
func NewClusteringEngine(analyzer *ai.Analyzer) *ClusteringEngine {
	return &ClusteringEngine{
		analyzer:      analyzer,
		llmClassifier: NewLLMClassifier(analyzer),
	}
}

// ClusterArticles 对文章进行智能聚类（使用新的两阶段 LLM+向量聚类）
func (e *ClusteringEngine) ClusterArticles(articles []*models.Article) []*models.ArticleCluster {
	if len(articles) == 0 {
		return nil
	}

	// 使用新的两阶段聚类
	return e.ClusterArticlesWithLLM(context.Background(), articles)
}

// ClusterArticlesWithLLM 两阶段聚类：LLM 语义分组 + 向量精细聚类
func (e *ClusteringEngine) ClusterArticlesWithLLM(ctx context.Context, articles []*models.Article) []*models.ArticleCluster {
	if len(articles) == 0 {
		return nil
	}

	log.Printf("开始两阶段聚类，共 %d 篇文章", len(articles))

	// Phase 1: LLM 语义分组
	classifyResult, err := e.llmClassifier.ClassifyArticles(ctx, articles)
	if err != nil {
		log.Printf("LLM 分组失败: %v，使用降级方案", err)
		return e.fallbackCluster(articles)
	}

	log.Printf("LLM 分组完成，共 %d 个语义组", len(classifyResult.Groups))

	// Phase 2: 对每个语义组进行向量精细聚类
	var clusters []*models.ArticleCluster
	for _, group := range classifyResult.Groups {
		// 获取该分组的文章
		groupArticles := e.getArticlesByIDs(articles, group.ArticleIDs)
		if len(groupArticles) == 0 {
			continue
		}

		// 对组内文章进行向量精细聚类
		subClusters := e.refineClusterByEmbedding(groupArticles, 0.8)

		// 为每个子聚类构建 ArticleCluster
		for _, subArticles := range subClusters {
			cluster := &models.ArticleCluster{
				Name:               e.generateClusterNameFromGroup(subArticles, group),
				Domain:             group.Domain,
				Articles:           subArticles,
				Keywords:           extractCommonKeywords(subArticles),
				RepresentativeTags: group.RepresentativeTags,
				TopicType:          group.Domain,
			}
			// 如果有代表性标签，也加入关键词
			if len(group.RepresentativeTags) > 0 {
				cluster.Keywords = mergeKeywords(cluster.Keywords, group.RepresentativeTags)
			}
			clusters = append(clusters, cluster)
		}
	}

	log.Printf("两阶段聚类完成，共 %d 个内容块", len(clusters))
	return clusters
}

// refineClusterByEmbedding 向量精细聚类（优化版：预反序列化所有向量）
func (e *ClusteringEngine) refineClusterByEmbedding(articles []*models.Article, threshold float64) [][]*models.Article {
	if len(articles) <= 1 {
		if len(articles) == 1 {
			return [][]*models.Article{articles}
		}
		return nil
	}

	// 预处理：一次性反序列化所有向量，避免 O(n²) 重复反序列化
	type articleWithVec struct {
		article *models.Article
		vec     []float32
	}
	var validArticles []articleWithVec
	for _, a := range articles {
		vec, err := ai.DeserializeEmbedding(a.Embedding)
		if err != nil || len(vec) == 0 {
			log.Printf("警告: 文章 %d embedding 解析失败: %v", a.ID, err)
			continue
		}
		validArticles = append(validArticles, articleWithVec{article: a, vec: vec})
	}

	if len(validArticles) == 0 {
		return nil
	}

	var clusters [][]*models.Article
	used := make(map[int64]bool)

	for i, av := range validArticles {
		if used[av.article.ID] {
			continue
		}

		cluster := []*models.Article{av.article}
		used[av.article.ID] = true

		// 找相似文章（只计算上三角矩阵，避免重复计算）
		for j := i + 1; j < len(validArticles); j++ {
			bv := validArticles[j]
			if used[bv.article.ID] {
				continue
			}

			sim := ai.CalculateCosineSimilarity(av.vec, bv.vec)
			if sim >= threshold {
				cluster = append(cluster, bv.article)
				used[bv.article.ID] = true
			}
		}

		clusters = append(clusters, cluster)
	}

	return clusters
}

// generateClusterNameFromGroup 生成聚类名称（结合 LLM 分组信息）
func (e *ClusteringEngine) generateClusterNameFromGroup(articles []*models.Article, group ArticleGroup) string {
	if len(articles) == 0 {
		return "未知主题"
	}
	if len(articles) == 1 {
		return articles[0].Title
	}

	// 优先使用 LLM 分组的名称
	if group.Name != "" && group.Name != "其他资讯" {
		return group.Name
	}

	// 降级：使用共同关键词
	keywords := extractCommonKeywords(articles)
	if len(keywords) > 0 {
		maxLen := min(3, len(keywords))
		return strings.Join(keywords[:maxLen], "、")
	}

	return articles[0].Title
}

// getArticlesByIDs 根据 ID 列表获取文章
func (e *ClusteringEngine) getArticlesByIDs(articles []*models.Article, ids []int64) []*models.Article {
	idSet := make(map[int64]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	var result []*models.Article
	for _, a := range articles {
		if idSet[a.ID] {
			result = append(result, a)
		}
	}
	return result
}

// fallbackCluster 降级聚类方案（当 LLM 失败时使用原来的方法）
func (e *ClusteringEngine) fallbackCluster(articles []*models.Article) []*models.ArticleCluster {
	// 使用原来的 tag/关键词分组 + 向量聚类
	groupKeyMap := e.groupByTagAndKeyword(articles)

	globalUsed := make(map[int64]bool)
	var clusters []*models.ArticleCluster

	for _, group := range groupKeyMap {
		if len(group) == 0 {
			continue
		}

		// 过滤已被使用的文章
		var available []*models.Article
		for _, a := range group {
			if !globalUsed[a.ID] {
				available = append(available, a)
			}
		}

		if len(available) == 0 {
			continue
		}

		if len(available) == 1 {
			a := available[0]
			globalUsed[a.ID] = true
			clusters = append(clusters, &models.ArticleCluster{
				Name:     a.Title,
				Articles: []*models.Article{a},
				Keywords: parseKeywords(a.Keywords),
			})
			continue
		}

		// 用向量相似度细分
		subClusters := e.clusterByEmbeddingWithUsed(available, 0.75, globalUsed)
		for _, sc := range subClusters {
			cluster := &models.ArticleCluster{
				Name:     e.generateClusterName(sc),
				Articles: sc,
				Keywords: extractCommonKeywords(sc),
			}
			clusters = append(clusters, cluster)
		}
	}

	// 合并相似聚类
	return e.mergeSimilarClusters(clusters, 0.85)
}

// groupByTagAndKeyword 按 Tag 和关键词分组（降级方案使用）
func (e *ClusteringEngine) groupByTagAndKeyword(articles []*models.Article) map[string][]*models.Article {
	groupKeyMap := make(map[string][]*models.Article)

	for _, a := range articles {
		keys := make(map[string]bool)

		// 收集所有 Tag
		tags := parseTags(a.TagsCache)
		for _, tag := range tags {
			keys[tag] = true
		}

		// 收集所有关键词
		keywords := parseKeywords(a.Keywords)
		for _, kw := range keywords {
			keys[kw] = true
		}

		// 统一加入分组
		for key := range keys {
			groupKeyMap[key] = append(groupKeyMap[key], a)
		}
	}

	return groupKeyMap
}

// clusterByEmbeddingWithUsed 用向量相似度聚类（优化版：预反序列化）
func (e *ClusteringEngine) clusterByEmbeddingWithUsed(articles []*models.Article, threshold float64, globalUsed map[int64]bool) [][]*models.Article {
	var clusters [][]*models.Article
	localUsed := make(map[int64]bool)

	// 预处理：一次性反序列化所有向量
	type articleWithVec struct {
		article *models.Article
		vec     []float32
	}
	var withEmbedding []articleWithVec
	var withoutEmbedding []*models.Article

	for _, a := range articles {
		// 检查全局 used
		if globalUsed != nil && globalUsed[a.ID] {
			continue
		}
		if len(a.Embedding) > 0 {
			vec, err := ai.DeserializeEmbedding(a.Embedding)
			if err != nil || len(vec) == 0 {
				withoutEmbedding = append(withoutEmbedding, a)
				continue
			}
			withEmbedding = append(withEmbedding, articleWithVec{article: a, vec: vec})
		} else {
			withoutEmbedding = append(withoutEmbedding, a)
		}
	}

	// 对有 Embedding 的文章进行聚类
	for i, av := range withEmbedding {
		if localUsed[av.article.ID] {
			continue
		}

		cluster := []*models.Article{av.article}
		localUsed[av.article.ID] = true
		if globalUsed != nil {
			globalUsed[av.article.ID] = true
		}

		// 找相似文章（只计算上三角矩阵）
		for j := i + 1; j < len(withEmbedding); j++ {
			bv := withEmbedding[j]
			if localUsed[bv.article.ID] {
				continue
			}

			sim := ai.CalculateCosineSimilarity(av.vec, bv.vec)
			if sim >= threshold {
				cluster = append(cluster, bv.article)
				localUsed[bv.article.ID] = true
				if globalUsed != nil {
					globalUsed[bv.article.ID] = true
				}
			}
		}

		clusters = append(clusters, cluster)
	}

	// 没有 Embedding 的文章各自成单独聚类
	for _, a := range withoutEmbedding {
		if !localUsed[a.ID] {
			clusters = append(clusters, []*models.Article{a})
			localUsed[a.ID] = true
			if globalUsed != nil {
				globalUsed[a.ID] = true
			}
		}
	}

	return clusters
}

// mergeSimilarClusters 合并相似聚类
func (e *ClusteringEngine) mergeSimilarClusters(clusters []*models.ArticleCluster, threshold float64) []*models.ArticleCluster {
	if len(clusters) < 2 {
		return clusters
	}

	used := make(map[int]bool)
	var result []*models.ArticleCluster

	for i, c1 := range clusters {
		if used[i] {
			continue
		}
		used[i] = true

		merged := c1
		for j, c2 := range clusters {
			if used[j] {
				continue
			}

			// 检查关键词相似度
			sim := e.calcKeywordOverlap(merged.Keywords, c2.Keywords)
			if sim >= threshold {
				// 合并聚类
				merged.Articles = append(merged.Articles, c2.Articles...)
				merged.Keywords = extractCommonKeywords(merged.Articles)
				used[j] = true
			}
		}
		result = append(result, merged)
	}

	return result
}

// generateClusterName 生成聚类名称
func (e *ClusteringEngine) generateClusterName(articles []*models.Article) string {
	if len(articles) == 0 {
		return "未知主题"
	}
	if len(articles) == 1 {
		return articles[0].Title
	}

	// 使用共同关键词
	keywords := extractCommonKeywords(articles)
	if len(keywords) > 0 {
		maxLen := min(3, len(keywords))
		return strings.Join(keywords[:maxLen], "、")
	}
	return articles[0].Title
}

// calcKeywordOverlap 计算关键词重叠度
func (e *ClusteringEngine) calcKeywordOverlap(kw1, kw2 []string) float64 {
	if len(kw1) == 0 || len(kw2) == 0 {
		return 0
	}

	set1 := make(map[string]bool)
	for _, k := range kw1 {
		set1[k] = true
	}

	overlap := 0
	for _, k := range kw2 {
		if set1[k] {
			overlap++
		}
	}

	return float64(overlap) / math.Sqrt(float64(len(kw1)*len(kw2)))
}

// parseTags 解析 Tag 缓存
func parseTags(tagsCache string) []string {
	tagsCache = strings.TrimSpace(tagsCache)
	if tagsCache == "" {
		return nil
	}

	// 尝试 JSON 解析
	if strings.HasPrefix(tagsCache, "[") {
		var tags []string
		if err := json.Unmarshal([]byte(tagsCache), &tags); err == nil {
			return tags
		}
	}

	// 逗号分隔解析
	parts := strings.Split(tagsCache, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}

// parseKeywords 解析关键词
func parseKeywords(keywords string) []string {
	return parseTags(keywords) // 格式相同
}

// extractCommonKeywords 提取聚类共同关键词
func extractCommonKeywords(articles []*models.Article) []string {
	if len(articles) == 0 {
		return nil
	}

	// 统计关键词频率
	freq := make(map[string]int)
	for _, a := range articles {
		keys := make(map[string]bool)
		for _, k := range parseKeywords(a.Keywords) {
			keys[k] = true
		}
		for k := range keys {
			freq[k]++
		}
	}

	// 返回出现频率 >= 50% 的关键词
	threshold := max(1, len(articles)/2)
	var result []string
	for k, count := range freq {
		if count >= threshold {
			result = append(result, k)
		}
	}
	return result
}

// mergeKeywords 合并关键词（去重）
func mergeKeywords(kw1, kw2 []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, k := range kw1 {
		if !seen[k] {
			seen[k] = true
			result = append(result, k)
		}
	}
	for _, k := range kw2 {
		if !seen[k] {
			seen[k] = true
			result = append(result, k)
		}
	}
	return result
}
