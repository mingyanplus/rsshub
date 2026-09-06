package processor

import (
	"fmt"

	"math"
	"math/rand"
	"rss-ai/internal/ai"
	"sort"
	"time"

	"rss-ai/internal/database"
	"rss-ai/internal/models"
)

// 召回/重排参数（取自推荐方案 §4 / §6 / 附录速查）
const (
	recallCandidateFactor   = 3    // 候选池 = limit × factor
	recallOversample        = 2    // 每通道超采样倍数（主题配额砍掉后有替补）
	recallFreshWindowDays   = 3    // 新鲜度通道入库窗口（天）
	recallCoverageMinShare  = 0.02 // 阅读占比低于此值的类别视为盲区
	recallCoveragePerCat    = 2    // 盲区类别每类抽取篇数
	recallAdjacentTopFeeds  = 5    // 取权重前 N 的高频阅读簇找邻接
	recallAdjacentNeighbors = 2    // 每个高频簇的相邻簇数

	mmrLambda            = 0.6 // MMR 相关性权重
	quotaPerTopic        = 3   // 同一主题类别最多篇数
	explorationMin       = 2   // 探索通道保底篇数
	exposureCooldownDays = 7   // 曝光未点击冷却天数
)

// isExplorationChannel 是否为探索类通道（保底统计口径）
func isExplorationChannel(ch string) bool {
	return ch == ChannelAdjacent || ch == ChannelCoverage || ch == ChannelRandom
}

// recallCandidate 带预计算向量与匹配度的候选（召回排序与打分共用）
type recallCandidate struct {
	cand   *database.RecommendationCandidate
	vec    []float32
	posSim float64
	negSim float64
}

// recall 五路召回：对预计算候选池按配比分通道抽取并标记 Channel（方案 §4）
// posClusters/posCentroids 由调用方加载一次传入（邻接簇通道用）
func (r *Recommender) recall(pool []recallCandidate, posClusters []models.InterestCluster, posCentroids [][]float32, limit int, mix map[string]float64) []recallCandidate {
	if len(pool) == 0 || limit <= 0 {
		return nil
	}
	profileReady := len(posClusters) > 0

	used := make(map[int64]bool)
	var result []recallCandidate
	pick := func(e recallCandidate, ch string) {
		if used[e.cand.Article.ID] {
			return
		}
		used[e.cand.Article.ID] = true
		e.cand.Channel = ch
		result = append(result, e)
	}
	quota := func(ch string) int { return int(math.Round(float64(limit) * mix[ch] * recallOversample)) }

	// 1. 精准通道：正簇匹配最高的文章
	if profileReady {
		byPos := append([]recallCandidate{}, pool...)
		sort.SliceStable(byPos, func(i, j int) bool { return byPos[i].posSim > byPos[j].posSim })
		n := quota(ChannelPrecise)
		for _, e := range byPos {
			if n <= 0 {
				break
			}
			if e.posSim >= clusterPosMatchThreshold {
				pick(e, ChannelPrecise)
				n--
			}
		}
	}

	// 2. 邻接簇通道：高频簇的相邻簇中抽高质量（排除精准命中与负惩罚区）
	adjacentCentroids := adjacentClusterCentroids(posClusters, posCentroids, recallAdjacentTopFeeds, recallAdjacentNeighbors)
	if len(adjacentCentroids) > 0 {
		type scored struct {
			e   recallCandidate
			sim float64
		}
		var byAdj []scored
		for _, e := range pool {
			if len(e.vec) == 0 || used[e.cand.Article.ID] {
				continue
			}
			// 邻接而非同主题：排除已达精准匹配的文章；排除负惩罚区
			if e.posSim < clusterPosMatchThreshold && e.negSim < clusterNegPenaltyThreshold {
				if sim := bestSimilarity(e.vec, adjacentCentroids); sim > 0 {
					byAdj = append(byAdj, scored{e, sim})
				}
			}
		}
		sort.SliceStable(byAdj, func(i, j int) bool { return byAdj[i].sim > byAdj[j].sim })
		n := quota(ChannelAdjacent)
		for _, s := range byAdj {
			if n <= 0 {
				break
			}
			pick(s.e, ChannelAdjacent)
			n--
		}
	}

	// 3. 主题覆盖通道：阅读占比 <2% 的类别（含从未读过的——以候选池出现的类别为准），每类抽 1~2 篇
	dist, readTotal := readTopicDistribution(r.db)
	if blind := blindCategories(pool, dist, readTotal); len(blind) > 0 {
		n := quota(ChannelCoverage)
		for cat := range blind {
			if n <= 0 {
				break
			}
			perCat := recallCoveragePerCat
			for _, e := range pool {
				if perCat <= 0 || n <= 0 {
					break
				}
				if !used[e.cand.Article.ID] && e.cand.Article.TopicCategory == cat && e.negSim < clusterNegPenaltyThreshold {
					pick(e, ChannelCoverage)
					perCat--
					n--
				}
			}
		}
	}

	// 4. 新鲜度通道：近 3 天入库，按簇匹配度排序
	now := time.Now()
	var fresh []recallCandidate
	for _, e := range pool {
		if !used[e.cand.Article.ID] && articleAgeHours(e.cand.Article, now) <= recallFreshWindowDays*24 {
			fresh = append(fresh, e)
		}
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].posSim > fresh[j].posSim })
	n := quota(ChannelFreshness)
	for _, e := range fresh {
		if n <= 0 {
			break
		}
		pick(e, ChannelFreshness)
		n--
	}

	// 5. 随机探索通道：纯随机兜底"意外"
	var rest []recallCandidate
	for _, e := range pool {
		if !used[e.cand.Article.ID] && e.negSim < clusterNegPenaltyThreshold {
			rest = append(rest, e)
		}
	}
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	n = quota(ChannelRandom)
	for _, e := range rest {
		if n <= 0 {
			break
		}
		pick(e, ChannelRandom)
		n--
	}

	return result
}

// adjacentClusterCentroids 高频阅读簇的相邻簇质心集合（按簇权重取高频，找最近邻簇，去重）
func adjacentClusterCentroids(clusters []models.InterestCluster, centroids [][]float32, topFeeds, neighbors int) [][]float32 {
	if len(clusters) < 2 {
		return nil
	}
	if topFeeds > len(clusters) {
		topFeeds = len(clusters)
	}
	seen := make(map[int]bool)
	var out [][]float32
	for i := 0; i < topFeeds; i++ {
		type pair struct {
			idx int
			sim float64
		}
		sims := make([]pair, 0, len(clusters)-1)
		for j := range clusters {
			if i != j {
				sims = append(sims, pair{j, ai.CalculateCosineSimilarity(centroids[i], centroids[j])})
			}
		}
		sort.SliceStable(sims, func(a, b int) bool { return sims[a].sim > sims[b].sim })
		for k := 0; k < neighbors && k < len(sims); k++ {
			idx := sims[k].idx
			if !seen[idx] {
				seen[idx] = true
				out = append(out, centroids[idx])
			}
		}
	}
	return out
}

// readTopicDistribution 已读主题分布（复用 ListTopicCategoryStats，与指标口径一致）
func readTopicDistribution(db *database.DB) (map[string]int, int) {
	stats, err := db.ListTopicCategoryStats()
	if err != nil {
		return nil, 0
	}
	dist := make(map[string]int, len(stats))
	total := 0
	for _, s := range stats {
		dist[s.Category] = s.Read
		total += s.Read
	}
	return dist, total
}

// blindCategories 盲区类别：候选池中出现的类别，其阅读占比 <2%（从未读过的类别占比为 0，天然是盲区）
func blindCategories(pool []recallCandidate, dist map[string]int, readTotal int) map[string]bool {
	blind := make(map[string]bool)
	for _, e := range pool {
		cat := e.cand.Article.TopicCategory
		if cat == "" {
			continue
		}
		if blind[cat] {
			continue
		}
		share := 0.0
		if readTotal > 0 {
			share = float64(dist[cat]) / float64(readTotal)
		}
		if share < recallCoverageMinShare {
			blind[cat] = true
		}
	}
	return blind
}

// rerankAndFinalize MMR 多样性打散 + 主题配额 + 探索保底（方案 §6，纯内存计算）
// 相似度用预归一化向量的点积（等价余弦，避免每对重算范数）；topicKey 预计算一次
func rerankAndFinalize(scored []*ScoredArticle, limit int) []*ScoredArticle {
	// 预归一化向量副本与主题键（MMR 内层高频访问）
	normed := make([][]float32, len(scored))
	keys := make([]string, len(scored))
	for i, s := range scored {
		keys[i] = topicKey(s.Article)
		if len(s.vec) > 0 {
			normed[i] = normalizeF32(s.vec)
		}
	}
	dot := func(a, b []float32) float64 {
		if len(a) == 0 || len(b) == 0 {
			return 0
		}
		sum := float64(0)
		for i := range a {
			sum += float64(a[i]) * float64(b[i])
		}
		return sum
	}

	quota := make(map[string]int)
	selected := make([]*ScoredArticle, 0, limit)
	selectedVecs := make([][]float32, 0, limit)
	chosen := make([]bool, len(scored))

	for len(selected) < limit {
		bestIdx, bestVal := -1, math.Inf(-1)
		for i, s := range scored {
			if chosen[i] || quota[keys[i]] >= quotaPerTopic {
				continue
			}
			// MMR：λ×相关性 - (1-λ)×与已选文章的最大相似度
			maxSim := 0.0
			for _, sv := range selectedVecs {
				if sim := dot(normed[i], sv); sim > maxSim {
					maxSim = sim
				}
			}
			if val := mmrLambda*s.Score - (1-mmrLambda)*maxSim; val > bestVal {
				bestVal, bestIdx = val, i
			}
		}
		if bestIdx < 0 {
			break // 全部被配额卡住或没有候选
		}
		chosen[bestIdx] = true
		quota[keys[bestIdx]]++
		selected = append(selected, scored[bestIdx])
		selectedVecs = append(selectedVecs, normed[bestIdx])
	}

	// 探索保底：至少 explorationMin 篇来自探索通道，不足则用未入选的探索候选替换末位非探索文章
	explore := 0
	for _, s := range selected {
		if isExplorationChannel(s.Channel) {
			explore++
		}
	}
	if explore < explorationMin {
		var backups []*ScoredArticle
		for i, s := range scored {
			if !chosen[i] && isExplorationChannel(s.Channel) {
				backups = append(backups, s)
			}
		}
		for i := len(selected) - 1; i >= 0 && explore < explorationMin && len(backups) > 0; i-- {
			if !isExplorationChannel(selected[i].Channel) {
				selected[i] = backups[0]
				backups = backups[1:]
				explore++
			}
		}
	}
	return selected
}

// topicKey 主题配额键：topic_category 为空时退化为 feed_id
func topicKey(a *models.Article) string {
	if a.TopicCategory != "" {
		return "cat:" + a.TopicCategory
	}
	return fmt.Sprintf("feed:%d", a.FeedID)
}

// normalizeF32 L2 归一化副本（零向量返回 nil）
func normalizeF32(v []float32) []float32 {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return nil
	}
	s := float32(1 / math.Sqrt(norm))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * s
	}
	return out
}
