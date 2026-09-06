package processor

import (
	"math"

	"rss-ai/internal/ai"
)

// 防茧房评估指标健康范围（方案 §8）
const (
	metricsTopicCoverageHealthy = 0.60 // 主题覆盖率 > 60%
	metricsTopicEntropyHealthy  = 2.0  // 主题熵 > 2.0
	metricsAvgSimHealthy        = 0.6  // 相邻推荐相似度 < 0.6（过高=同质化）
	metricsExploreCTRHealthy    = 0.3  // 探索点击率 > 0.3
	metricsSimLookback          = 20   // 相邻相似度取最近一次推荐的前 N 篇
)

// RecMetrics 防茧房评估指标（周报输出）
type RecMetrics struct {
	TopicCoverage float64 `json:"topic_coverage"` // 已读类别数 / 有文章的类别数
	TopicEntropy  float64 `json:"topic_entropy"`  // 各类别阅读占比的熵
	AvgRecSim     float64 `json:"avg_rec_sim"`    // 推荐列表内文章两两余弦均值
	ExploreCTR    float64 `json:"explore_ctr"`    // 探索通道点击率 / 精准通道点击率
}

// Healthy 各指标是否落在健康范围（用于前端红绿展示）
func (m *RecMetrics) Healthy() map[string]bool {
	return map[string]bool{
		"topic_coverage": m.TopicCoverage > metricsTopicCoverageHealthy,
		"topic_entropy":  m.TopicEntropy > metricsTopicEntropyHealthy,
		"avg_rec_sim":    m.AvgRecSim < metricsAvgSimHealthy,
		"explore_ctr":    m.ExploreCTR > metricsExploreCTRHealthy,
	}
}

// ComputeMetrics 计算防茧房评估指标（方案 §8）
func (r *Recommender) ComputeMetrics() (*RecMetrics, error) {
	m := &RecMetrics{}

	// 主题覆盖率 + 主题熵
	stats, err := r.db.ListTopicCategoryStats()
	if err != nil {
		return nil, err
	}
	if len(stats) > 0 {
		readCats, totalRead := 0, 0
		for _, s := range stats {
			if s.Read > 0 {
				readCats++
				totalRead += s.Read
			}
		}
		m.TopicCoverage = float64(readCats) / float64(len(stats))

		if totalRead > 0 {
			entropy := 0.0
			for _, s := range stats {
				if s.Read > 0 {
					p := float64(s.Read) / float64(totalRead)
					entropy -= p * math.Log(p)
				}
			}
			m.TopicEntropy = entropy
		}
	}

	// 相邻推荐相似度：当前推荐列表两两余弦均值
	recs, err := r.Recommend(metricsSimLookback)
	if err == nil && len(recs) > 1 {
		var vecs [][]float32
		for _, s := range recs {
			if v := articleVector(s.Article); len(v) > 0 {
				vecs = append(vecs, v)
			}
		}
		if len(vecs) > 1 {
			total, pairs := 0.0, 0
			for i := 0; i < len(vecs); i++ {
				for j := i + 1; j < len(vecs); j++ {
					total += ai.CalculateCosineSimilarity(vecs[i], vecs[j])
					pairs++
				}
			}
			if pairs > 0 {
				m.AvgRecSim = total / float64(pairs)
			}
		}
	}

	// 探索点击率 = 探索通道点击率 / 精准通道点击率
	clickStats, err := r.db.ListChannelClickStats(30)
	if err == nil {
		ctr := func(channels ...string) float64 {
			exposed, clicked := 0, 0
			for _, ch := range channels {
				if s, ok := clickStats[ch]; ok {
					exposed += s.Exposed
					clicked += s.Clicked
				}
			}
			if exposed == 0 {
				return 0
			}
			return float64(clicked) / float64(exposed)
		}
		explore := ctr(ChannelAdjacent, ChannelCoverage, ChannelRandom)
		precise := ctr(ChannelPrecise)
		if precise > 0 {
			m.ExploreCTR = explore / precise
		}
	}

	return m, nil
}
