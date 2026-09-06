package processor

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
)

// 兴趣簇参数（取自推荐方案附录速查，后续可做成 config 可调项）
const (
	clusterPosMergeThreshold   = 0.82 // 正簇合并阈值
	clusterPosCreateThreshold  = 0.62 // 正簇创建阈值
	clusterPosMatchThreshold   = 0.78 // 正簇匹配阈值（排序用）
	clusterNegMergeThreshold   = 0.84
	clusterNegCreateThreshold  = 0.65
	clusterNegPenaltyThreshold = 0.62 // 负簇惩罚阈值（排序用）
	clusterPosLimit            = 48   // 正簇上限
	clusterNegLimit            = 32   // 负簇上限
	clusterCompressThreshold   = 0.92 // 达上限时压缩合并的相似度门槛
	clusterWeightCap           = 100.0

	decayPosDaily        = 0.985 // 正簇日衰减
	decayNegDaily        = 0.99  // 负簇日衰减（负兴趣遗忘更慢）
	decayInactiveDays    = 21    // 不活跃加速衰减阈值（天）
	decayInactiveFactor  = 0.96
	decayDeleteWeight    = 0.5 // 低于此权重删除
	decaySingleSamplePos = 30  // 单样本正簇保留天数
	decaySingleSampleNeg = 60

	seedMinReadLogs = 20      // 行为日志超过此数不再做订阅先验冷启动
	seedVectorCount = 20      // 每个 feed 取近期文章数
	seedInitWeight  = 5.0     // 初始先验簇权重
	seedLabelLimit  = 3       // 初始标签取关键词数
)

// Polarity 簇极性
const (
	PolarityPositive = "positive"
	PolarityNegative = "negative"
)

// InterestProfile 兴趣簇画像：正/负兴趣在线增量建簇、每日衰减、订阅先验冷启动
type InterestProfile struct {
	db *database.DB
	mu sync.Mutex // 串行化建簇操作，避免并发反馈写坏质心
}

// NewInterestProfile 创建画像服务
func NewInterestProfile(db *database.DB) *InterestProfile {
	return &InterestProfile{db: db}
}

// RecordFeedback 记录一次反馈并入簇（polarity=positive/negative，mult 为权重倍数如收藏 ×2）
func (p *InterestProfile) RecordFeedback(article *models.Article, polarity string, mult float64) {
	if mult <= 0 {
		mult = 1
	}
	vec := articleVector(article)
	if len(vec) == 0 {
		log.Printf("兴趣画像: 文章 %d 无向量，跳过反馈", article.ID)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	clusters, err := p.db.ListInterestClusters(polarity)
	if err != nil {
		log.Printf("兴趣画像: 读取簇失败: %v", err)
		return
	}

	now := time.Now().Unix()
	var best *models.InterestCluster
	bestSim := -1.0
	for _, c := range clusters {
		centroid, err := ai.DeserializeEmbedding(c.Centroid)
		if err != nil || len(centroid) == 0 {
			continue
		}
		sim := float64(ai.CalculateCosineSimilarity(vec, centroid))
		if sim > bestSim {
			bestSim, best = sim, c
		}
	}

	mergeThreshold := clusterPosMergeThreshold
	createThreshold := clusterPosCreateThreshold
	if polarity == PolarityNegative {
		mergeThreshold = clusterNegMergeThreshold
		createThreshold = clusterNegCreateThreshold
	}

	switch {
	case best != nil && bestSim >= mergeThreshold:
		// 并入最相似簇：质心加权更新
		p.mergeIntoCluster(best, vec, mult, articleKeywords(article), now)
		if err := p.db.UpdateInterestCluster(best); err != nil {
			log.Printf("兴趣画像: 更新簇 %d 失败: %v", best.ID, err)
		}
	case best == nil || bestSim >= createThreshold:
		// 独立新兴趣：新建簇（必要时先压缩腾位）
		if len(clusters) >= clusterLimit(polarity) {
			if !p.compressClusters(clusters) {
				// 无法压缩时并入最相似簇兜底
				if best != nil {
					p.mergeIntoCluster(best, vec, mult, articleKeywords(article), now)
					if err := p.db.UpdateInterestCluster(best); err != nil {
						log.Printf("兴趣画像: 更新簇 %d 失败: %v", best.ID, err)
					}
					return
				}
			}
		}
		c := &models.InterestCluster{
			Polarity:     polarity,
			Weight:       math.Max(1, mult),
			SampleCount:  1,
			LastActiveAt: now,
			Label:        joinKeywords(articleKeywords(article), seedLabelLimit),
			CreatedAt:    now,
		}
		c.Centroid = normalizeVec(vec)
		if _, err := p.db.CreateInterestCluster(c); err != nil {
			log.Printf("兴趣画像: 新建簇失败: %v", err)
		}
	default:
		// 相似度过低且不足创建阈值：视为噪声，忽略
		log.Printf("兴趣画像: 文章 %d 相似度 %.3f 低于创建阈值，忽略", article.ID, bestSim)
	}
}

// MatchScores 计算文章向量与画像的匹配度（排序时用）
// 返回：正簇最大相似（低于匹配阈值取 0）与对应标签、负簇最大相似（低于惩罚阈值取 0）与对应标签
func (p *InterestProfile) MatchScores(vec []float32) (posMatch float64, posLabel string, negSim float64, negLabel string) {
	if len(vec) == 0 {
		return 0, "", 0, ""
	}
	posMatch, posLabel = p.bestMatch(vec, PolarityPositive)
	if posMatch < clusterPosMatchThreshold {
		posMatch = 0
	}
	negSim, negLabel = p.bestMatch(vec, PolarityNegative)
	if negSim < clusterNegPenaltyThreshold {
		negSim = 0
	}
	return
}

// AdjacentClusters 取与给定向量最相近的 n 个正簇质心（邻接簇召回用，排除命中簇自身）
func (p *InterestProfile) AdjacentClusters(vec []float32, n int) ([][]float32, []string) {
	clusters, err := p.db.ListInterestClusters(PolarityPositive)
	if err != nil {
		return nil, nil
	}
	type scored struct {
		vec   []float32
		label string
		sim   float64
	}
	var all []scored
	for _, c := range clusters {
		centroid, err := ai.DeserializeEmbedding(c.Centroid)
		if err != nil || len(centroid) == 0 {
			continue
		}
		sim := float64(ai.CalculateCosineSimilarity(vec, centroid))
		if sim < clusterPosMatchThreshold {
			continue // 邻接簇：低于正簇匹配阈值的才算"相邻"而非"同主题"
		}
		all = append(all, scored{vec: centroid, label: c.Label, sim: sim})
	}
	// 排序取前 n
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].sim > all[i].sim {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	if len(all) > n {
		all = all[:n]
	}
	vecs := make([][]float32, 0, len(all))
	labels := make([]string, 0, len(all))
	for _, s := range all {
		vecs = append(vecs, s.vec)
		labels = append(labels, s.label)
	}
	return vecs, labels
}

// PositiveCentroids 返回全部正簇（质心+标签+权重），排序/召回用
func (p *InterestProfile) PositiveCentroids() ([]models.InterestCluster, [][]float32) {
	return p.centroidsOf(PolarityPositive)
}

// NegativeCentroids 返回全部负簇（质心+标签+权重），排序惩罚用
func (p *InterestProfile) NegativeCentroids() ([]models.InterestCluster, [][]float32) {
	return p.centroidsOf(PolarityNegative)
}

// centroidsOf 加载指定极性全部簇并反序列化质心
func (p *InterestProfile) centroidsOf(polarity string) ([]models.InterestCluster, [][]float32) {
	clusters, err := p.db.ListInterestClusters(polarity)
	if err != nil || len(clusters) == 0 {
		return nil, nil
	}
	result := make([]models.InterestCluster, 0, len(clusters))
	vecs := make([][]float32, 0, len(clusters))
	for _, c := range clusters {
		centroid, err := ai.DeserializeEmbedding(c.Centroid)
		if err != nil || len(centroid) == 0 {
			continue
		}
		result = append(result, *c)
		vecs = append(vecs, centroid)
	}
	return result, vecs
}

// ApplyDailyDecay 每日衰减与淘汰（定时任务调用）
func (p *InterestProfile) ApplyDailyDecay() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, polarity := range []string{PolarityPositive, PolarityNegative} {
		clusters, err := p.db.ListInterestClusters(polarity)
		if err != nil {
			return fmt.Errorf("list %s clusters: %w", polarity, err)
		}
		now := time.Now()
		for _, c := range clusters {
			lastActive := time.Unix(c.LastActiveAt, 0)
			staleDays := now.Sub(lastActive).Hours() / 24

			// 单样本簇超期无新样本：直接删除
			staleLimit := float64(decaySingleSamplePos)
			if polarity == PolarityNegative {
				staleLimit = float64(decaySingleSampleNeg)
			}
			if c.SampleCount <= 1 && staleDays > staleLimit {
				if err := p.db.DeleteInterestCluster(c.ID); err != nil {
					return err
				}
				continue
			}

			factor := decayPosDaily
			if polarity == PolarityNegative {
				factor = decayNegDaily
			}
			if staleDays > decayInactiveDays {
				factor = decayInactiveFactor
			}
			c.Weight *= factor
			if c.Weight < decayDeleteWeight {
				if err := p.db.DeleteInterestCluster(c.ID); err != nil {
					return err
				}
				continue
			}
			if err := p.db.UpdateInterestCluster(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// SeedFromSubscriptions 订阅即先验：行为数据不足时用各 feed 近期文章均值建初始正簇
func (p *InterestProfile) SeedFromSubscriptions() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	logCount, err := p.db.CountReadLogs()
	if err != nil {
		return err
	}
	if logCount >= seedMinReadLogs {
		log.Printf("兴趣画像: 已有 %d 条行为日志，跳过订阅先验冷启动", logCount)
		return nil
	}

	digests, err := p.db.ListFeedArticleDigests(seedVectorCount)
	if err != nil {
		return err
	}

	now := time.Now().Unix()
	created := 0
	for _, digest := range digests {
		var sum []float64
		count := 0
		for _, blob := range digest.Vectors {
			vec, err := ai.DeserializeEmbedding(blob)
			if err != nil || len(vec) == 0 {
				continue
			}
			if sum == nil {
				sum = make([]float64, len(vec))
			}
			for i, v := range vec {
				sum[i] += float64(v)
			}
			count++
		}
		if count == 0 {
			continue
		}
		mean := make([]float32, len(sum))
		for i, s := range sum {
			mean[i] = float32(s / float64(count))
		}
		c := &models.InterestCluster{
			Polarity:     PolarityPositive,
			Weight:       seedInitWeight,
			SampleCount:  count,
			LastActiveAt: now,
			Label:        pickNonEmpty(joinKeywords(digest.Keywords, seedLabelLimit), digest.Title),
			CreatedAt:    now,
		}
		c.Centroid = normalizeVec(mean)
		if _, err := p.db.CreateInterestCluster(c); err != nil {
			return err
		}
		created++
	}
	log.Printf("兴趣画像: 订阅先验冷启动完成，建立 %d 个初始正簇", created)
	return nil
}

// ---- 内部工具 ----

func clusterLimit(polarity string) int {
	if polarity == PolarityNegative {
		return clusterNegLimit
	}
	return clusterPosLimit
}

// mergeIntoCluster 质心加权更新：centroid = normalize(centroid*weight + v*mult)
func (p *InterestProfile) mergeIntoCluster(c *models.InterestCluster, vec []float32, mult float64, keywords []string, now int64) {
	centroid, err := ai.DeserializeEmbedding(c.Centroid)
	if err != nil || len(centroid) == 0 {
		c.Centroid = normalizeVec(vec)
	} else if len(centroid) == len(vec) {
		merged := make([]float64, len(centroid))
		for i := range centroid {
			merged[i] = float64(centroid[i])*c.Weight + float64(vec[i])*mult
		}
		c.Centroid = normalizeVecF64(merged)
	} else {
		// 维度不一致（换过 embedding 模型）：以新向量重建质心
		c.Centroid = normalizeVec(vec)
	}
	c.Weight = math.Min(clusterWeightCap, c.Weight+mult)
	c.SampleCount++
	c.LastActiveAt = now
	c.Label = mergeClusterLabel(c.Label, keywords, seedLabelLimit)
}

// compressClusters 簇满时压缩：合并最相似的两簇（需 > compress 阈值），成功返回 true
func (p *InterestProfile) compressClusters(clusters []*models.InterestCluster) bool {
	var vecs [][]float32
	for _, c := range clusters {
		v, err := ai.DeserializeEmbedding(c.Centroid)
		if err != nil || len(v) == 0 {
			vecs = append(vecs, nil)
			continue
		}
		vecs = append(vecs, v)
	}
	bi, bj, best := -1, -1, clusterCompressThreshold
	for i := 0; i < len(vecs); i++ {
		if vecs[i] == nil {
			continue
		}
		for j := i + 1; j < len(vecs); j++ {
			if vecs[j] == nil {
				continue
			}
			sim := float64(ai.CalculateCosineSimilarity(vecs[i], vecs[j]))
			if sim > best {
				best, bi, bj = sim, i, j
			}
		}
	}
	if bi < 0 {
		return false
	}
	// j 并入 i
	a, b := clusters[bi], clusters[bj]
	total := a.Weight + b.Weight
	merged := make([]float64, len(vecs[bi]))
	for k := range vecs[bi] {
		merged[k] = (float64(vecs[bi][k])*a.Weight + float64(vecs[bj][k])*b.Weight) / total
	}
	a.Centroid = normalizeVecF64(merged)
	a.Weight = math.Min(clusterWeightCap, total)
	a.SampleCount += b.SampleCount
	a.LastActiveAt = maxInt64(a.LastActiveAt, b.LastActiveAt)
	a.Label = pickNonEmpty(a.Label, b.Label)
	if err := p.db.UpdateInterestCluster(a); err != nil {
		log.Printf("兴趣画像: 压缩簇失败: %v", err)
		return false
	}
	if err := p.db.DeleteInterestCluster(b.ID); err != nil {
		log.Printf("兴趣画像: 删除被压缩簇失败: %v", err)
		return false
	}
	return true
}

// bestMatch 找同极性最相似簇
func (p *InterestProfile) bestMatch(vec []float32, polarity string) (float64, string) {
	clusters, err := p.db.ListInterestClusters(polarity)
	if err != nil {
		return 0, ""
	}
	bestSim := -1.0
	bestLabel := ""
	for _, c := range clusters {
		centroid, err := ai.DeserializeEmbedding(c.Centroid)
		if err != nil || len(centroid) == 0 {
			continue
		}
		sim := float64(ai.CalculateCosineSimilarity(vec, centroid))
		if sim > bestSim {
			bestSim, bestLabel = sim, c.Label
		}
	}
	if bestSim < 0 {
		return 0, ""
	}
	return bestSim, bestLabel
}

// articleVector 文章向量（优先全文向量，其次总结向量）
func articleVector(a *models.Article) []float32 {
	if a == nil {
		return nil
	}
	if len(a.Embedding) > 0 {
		if v, err := ai.DeserializeEmbedding(a.Embedding); err == nil && len(v) > 0 {
			return v
		}
	}
	if len(a.SummaryEmbedding) > 0 {
		if v, err := ai.DeserializeEmbedding(a.SummaryEmbedding); err == nil && len(v) > 0 {
			return v
		}
	}
	return nil
}

// articleKeywords 文章关键词列表
func articleKeywords(a *models.Article) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(list []string) {
		for _, k := range list {
			if k != "" && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	add(parseKeywords(a.Keywords))
	add(parseTags(a.TagsCache))
	return out
}

// mergeClusterLabel 标签合并：新文章关键词优先，旧标签词补位，取前 limit 个
func mergeClusterLabel(oldLabel string, keywords []string, limit int) string {
	old := parseKeywords(oldLabel)
	merged := append(append([]string{}, keywords...), old...)
	seen := make(map[string]bool)
	var out []string
	for _, k := range merged {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
			if len(out) >= limit {
				break
			}
		}
	}
	return joinKeywords(out, limit)
}

// joinKeywords 取前 limit 个关键词用顿号连接
func joinKeywords(kws []string, limit int) string {
	if len(kws) > limit {
		kws = kws[:limit]
	}
	out := ""
	for i, k := range kws {
		if i > 0 {
			out += "、"
		}
		out += k
	}
	return out
}

// pickNonEmpty 返回第一个非空字符串
func pickNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// normalizeVec L2 归一化
func normalizeVec(v []float32) []byte {
	f64 := make([]float64, len(v))
	for i, x := range v {
		f64[i] = float64(x)
	}
	b, err := ai.SerializeEmbedding(normalized32(f64))
	if err != nil {
		return nil
	}
	return b
}

// normalizeVecF64 L2 归一化（float64 输入）
func normalizeVecF64(v []float64) []byte {
	b, err := ai.SerializeEmbedding(normalized32(v))
	if err != nil {
		return nil
	}
	return b
}

// normalized32 计算归一化后的 float32 向量
func normalized32(v []float64) []float32 {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	if norm == 0 {
		out := make([]float32, len(v))
		return out
	}
	norm = math.Sqrt(norm)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x / norm)
	}
	return out
}
