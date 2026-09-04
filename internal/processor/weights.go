package processor

import (
	"math"
	"math/rand"
	"rss-ai/internal/ai"
	"rss-ai/internal/models"
)

// WeightCalculator 权重计算器
type WeightCalculator struct{}

// NewWeightCalculator 创建权重计算器
func NewWeightCalculator() *WeightCalculator {
	return &WeightCalculator{}
}

// CalculateClusterWeight 计算聚类权重
func (c *WeightCalculator) CalculateClusterWeight(cluster *models.ArticleCluster, allClusters []*models.ArticleCluster) models.ClusterWeight {
	var w models.ClusterWeight

	// 1. 权威性 (0-1)
	w.Authority = c.calcAuthority(cluster.Articles)

	// 2. 信息密度 (0-1)
	w.Density = c.calcDensity(cluster.Articles)

	// 3. 独特性 (0-1)
	w.Uniqueness = c.calcUniqueness(cluster, allClusters)

	// 4. 中心度 (0-1)
	w.Centrality = c.calcCentrality(cluster)

	// 综合权重
	w.FinalScore = 0.3*w.Authority + 0.25*w.Density + 0.25*w.Uniqueness + 0.2*w.Centrality

	return w
}

// calcAuthority 计算权威性
func (c *WeightCalculator) calcAuthority(articles []*models.Article) float64 {
	if len(articles) == 0 {
		return 0
	}

	// 基于文章数量（对数增长）
	countScore := math.Log(float64(len(articles)+1)) / math.Log(10)

	// 基于平均重要性评分
	var avgImportance float64
	for _, a := range articles {
		avgImportance += float64(a.ImportanceScore)
	}
	avgImportance /= float64(len(articles))
	importanceScore := avgImportance / 10.0

	return math.Min(1.0, countScore*0.4+importanceScore*0.6)
}

// calcDensity 计算信息密度
func (c *WeightCalculator) calcDensity(articles []*models.Article) float64 {
	if len(articles) == 0 {
		return 0
	}

	var density float64
	for _, a := range articles {
		// 基于摘要完整度
		summaryScore := 0.0
		if len(a.Summary) > 100 {
			summaryScore = 0.5
		}
		if len(a.Summary) > 200 {
			summaryScore = 1.0
		}

		// 基于内容长度
		contentScore := math.Min(1.0, float64(len(a.Content))/1000)

		density += (summaryScore*0.6 + contentScore*0.4)
	}

	return density / float64(len(articles))
}

// calcUniqueness 计算独特性
func (c *WeightCalculator) calcUniqueness(cluster *models.ArticleCluster, allClusters []*models.ArticleCluster) float64 {
	if len(allClusters) <= 1 {
		return 1.0
	}

	var totalSim float64
	var count int

	for _, other := range allClusters {
		if other.ID == cluster.ID {
			continue
		}

		// 计算关键词重叠度
		sim := c.calcKeywordOverlap(cluster.Keywords, other.Keywords)
		totalSim += sim
		count++
	}

	if count == 0 {
		return 1.0
	}

	avgSim := totalSim / float64(count)
	return 1.0 - avgSim
}

// calcCentrality 计算中心度
func (c *WeightCalculator) calcCentrality(cluster *models.ArticleCluster) float64 {
	// 性能保护：限制最大比较数量，避免 O(n²) 过度
	if len(cluster.Articles) > 100 {
		sampleSize := 100
		articles := make([]*models.Article, sampleSize)
		perm := rand.Perm(len(cluster.Articles))
		for i := 0; i < sampleSize; i++ {
			articles[i] = cluster.Articles[perm[i]]
		}
		cluster = &models.ArticleCluster{Articles: articles}
	}

	if len(cluster.Articles) < 2 {
		return 0.5
	}

	var totalSim float64
	var count int

	for i := 0; i < len(cluster.Articles); i++ {
		for j := i + 1; j < len(cluster.Articles); j++ {
			embI, errI := ai.DeserializeEmbedding(cluster.Articles[i].Embedding)
			embJ, errJ := ai.DeserializeEmbedding(cluster.Articles[j].Embedding)

			if errI != nil || errJ != nil || len(embI) == 0 || len(embJ) == 0 {
				continue
			}

			sim := ai.CalculateCosineSimilarity(embI, embJ)
			totalSim += sim
			count++
		}
	}

	if count == 0 {
		return 0.5
	}

	return totalSim / float64(count)
}

// calcKeywordOverlap 计算关键词重叠度
func (c *WeightCalculator) calcKeywordOverlap(kw1, kw2 []string) float64 {
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
