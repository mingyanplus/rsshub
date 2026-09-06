package processor

import (
	"fmt"
	"math"
	"time"

	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
)

// 排序参数（取自推荐方案 §5 / 附录速查）
const (
	scoreInterestWeight    = 0.55 // 正簇匹配
	scoreSourceAuthority   = 0.08 // 手动源权重（authority 归一化）
	scoreSourceBehavior    = 0.08 // feed 正负反馈统计（tanh 压缩）
	scoreSourceReadRate    = 0.02 // feed 阅读率
	scoreFreshnessWeight   = 0.18 // 半衰期 36 小时
	scoreFreshnessHalflife = 36.0
	scoreFreshNewArticle   = 0.035 // 无向量新文章（72h 内）保底分
	scoreFreshNewHours     = 72.0

	scoreStateUnread   = 0.06
	scoreStateFavorite = 0.04
	scoreStateRead     = -0.08
	scorePenaltyNegMax = -0.45 // 负簇相似惩罚上限
	scorePenaltyNotInt = -4.0  // 不感兴趣重罚

	// 降级链（画像/向量不可用时的基础排序）
	fallbackFreshnessWeight = 0.35
	fallbackSourceWeight    = 0.28
	fallbackStateFavorite   = 0.5
	fallbackStateUnread     = 0.22
	fallbackStateRead       = -0.06
)

// 推荐通道
const (
	ChannelPrecise   = "precise"   // 精准：正簇匹配
	ChannelFreshness = "freshness" // 新鲜度
)

// ScoredArticle 带分数分项的推荐结果
type ScoredArticle struct {
	Article   *models.Article
	Score     float64
	Interest  float64 `json:"interest"`
	Source    float64 `json:"source"`
	Freshness float64 `json:"freshness"`
	State     float64 `json:"state"`
	Penalty   float64 `json:"penalty"`
	Reason    string  `json:"reason"`
	Channel   string  `json:"channel"`
}

// Recommender 推荐排序引擎（P3：精准 + 新鲜度双通道）
type Recommender struct {
	db      *database.DB
	profile *InterestProfile
}

// NewRecommender 创建推荐引擎
func NewRecommender(db *database.DB, profile *InterestProfile) *Recommender {
	return &Recommender{db: db, profile: profile}
}

// Recommend 生成推荐列表（精选模式默认配比）：五路召回 → 多因子打分 → MMR 重排
func (r *Recommender) Recommend(limit int) ([]*ScoredArticle, error) {
	return r.RecommendWithMix(limit, defaultChannelMix)
}

// RecommendWithMix 按指定通道配比推荐（P5 双模式：curated / discover 查不同配比表）
func (r *Recommender) RecommendWithMix(limit int, mix map[string]float64) ([]*ScoredArticle, error) {
	if limit <= 0 {
		limit = 50
	}
	candidates, err := r.db.ListRecommendationCandidates(limit*recallCandidateFactor, exposureCooldownDays)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// 五路召回（含曝光冷却过滤，候选池查询已剔除冷却文章）
	recalled := r.recall(candidates, limit, mix)
	if len(recalled) == 0 {
		return nil, nil
	}

	feedStats, err := r.db.ListFeedBehaviorStats()
	if err != nil {
		feedStats = map[int64]*database.FeedBehaviorStats{}
	}
	authority := feedAuthorityMap(r.db)

	// 画像可用性：存在正簇才启用兴趣分（否则整体走降级链）
	posClusters, posCentroids := r.profile.PositiveCentroids()
	_, negCentroids := r.profile.NegativeCentroids()
	profileReady := len(posClusters) > 0

	now := time.Now()
	var scored []*ScoredArticle
	for _, cand := range recalled {
		a := cand.Article
		var vec []float32
		if cand.HasVec {
			vec = articleVector(a)
		}

		var s *ScoredArticle
		if profileReady && len(vec) > 0 {
			posMatch, posLabel := bestPositiveMatch(vec, posClusters, posCentroids)
			negSim := bestSimilarity(vec, negCentroids)
			if negSim < clusterNegPenaltyThreshold {
				negSim = 0
			}
			s = r.scoreFull(a, vec, posMatch, posLabel, negSim, authority, feedStats, now)
		} else {
			s = scoreFallback(a, authority, feedStats, now)
		}
		// 通道标记以召回结果为准（召回阶段已做配额分配）
		s.Channel = cand.Channel
		scored = append(scored, s)
	}

	// MMR + 主题配额 + 探索保底
	return rerankAndFinalize(scored, limit), nil
}

// scoreFull 完整多因子打分（§5 公式）
func (r *Recommender) scoreFull(a *models.Article, vec []float32, posMatch float64, posLabel string, negSim float64,
	authority map[int64]int, feedStats map[int64]*database.FeedBehaviorStats, now time.Time) *ScoredArticle {

	s := &ScoredArticle{Article: a}

	// 兴趣：正簇匹配 × 0.55（matchScores 已按阈值截断为 0）
	s.Interest = posMatch * scoreInterestWeight

	// 来源：authority 归一化 + 正负反馈 tanh + 阅读率
	s.Source = sourceScore(a, authority, feedStats)

	// 新鲜度：exp(-age/36) × 0.18；无向量新文章给保底分
	ageHours := articleAgeHours(a, now)
	s.Freshness = math.Exp(-ageHours/scoreFreshnessHalflife) * scoreFreshnessWeight
	if len(vec) == 0 && ageHours <= scoreFreshNewHours {
		s.Freshness = math.Max(s.Freshness, scoreFreshNewArticle)
	}

	// 状态
	s.State = stateScore(a.IsRead, a.IsFavorite)

	// 惩罚：负簇相似（最高 -0.45）+ 不感兴趣重罚
	s.Penalty = penaltyScore(negSim, a.NotInterested)

	s.Score = clamp01(s.Interest + s.Source + s.Freshness + s.State + s.Penalty)
	s.Reason = buildReason(posMatch, posLabel, a.IsFavorite, ageHours)
	return s
}

// scoreFallback 降级链：embedding/画像不可用时退回基础排序（新鲜度 0.35 + 来源 0.28 + 状态分）
func scoreFallback(a *models.Article, authority map[int64]int, feedStats map[int64]*database.FeedBehaviorStats, now time.Time) *ScoredArticle {
	s := &ScoredArticle{Article: a}

	ageHours := articleAgeHours(a, now)
	s.Freshness = math.Exp(-ageHours/scoreFreshnessHalflife) * fallbackFreshnessWeight
	s.Source = sourceScore(a, authority, feedStats) * (fallbackSourceWeight / (scoreSourceAuthority + scoreSourceBehavior + scoreSourceReadRate))
	switch {
	case a.IsFavorite:
		s.State = fallbackStateFavorite
	case a.IsRead:
		s.State = fallbackStateRead
	default:
		s.State = fallbackStateUnread
	}
	s.Penalty = penaltyScore(0, a.NotInterested)

	s.Score = clamp01(s.Freshness + s.Source + s.State + s.Penalty)
	s.Reason = buildReason(0, "", a.IsFavorite, ageHours)
	return s
}

// sourceScore 来源分：authority×0.08 + tanh(正负统计)×0.08 + 阅读率×0.02
func sourceScore(a *models.Article, authority map[int64]int, feedStats map[int64]*database.FeedBehaviorStats) float64 {
	auth := 3
	if v, ok := authority[a.FeedID]; ok && v >= 1 && v <= 5 {
		auth = v
	}
	score := float64(auth-1) / 4 * scoreSourceAuthority

	if s, ok := feedStats[a.FeedID]; ok && s != nil {
		total := s.Positive + s.Negative
		if total > 0 {
			score += math.Tanh(float64(s.Positive-s.Negative) / float64(total+1)) * scoreSourceBehavior
		}
		score += clamp01(s.ReadRate) * scoreSourceReadRate
	}
	return score
}

// stateScore 状态分
func stateScore(isRead, isFavorite bool) float64 {
	score := 0.0
	if isFavorite {
		score += scoreStateFavorite
	}
	if isRead {
		score += scoreStateRead
	} else {
		score += scoreStateUnread
	}
	return score
}

// penaltyScore 惩罚分：负簇相似 + 不感兴趣
func penaltyScore(negSim float64, notInterested bool) float64 {
	score := 0.0
	if negSim > 0 {
		score += scorePenaltyNegMax * negSim
	}
	if notInterested {
		score += scorePenaltyNotInt
	}
	return score
}

// buildReason 生成人话推荐理由（分数分项 → 文案）
func buildReason(posMatch float64, posLabel string, isFavorite bool, ageHours float64) string {
	ageText := ageText(ageHours)
	switch {
	case posMatch >= clusterPosMatchThreshold && posLabel != "":
		return fmt.Sprintf("与你近期阅读的『%s』主题相近 · %s发布", posLabel, ageText)
	case posMatch >= clusterPosMatchThreshold:
		return fmt.Sprintf("与你近期阅读的兴趣相近 · %s发布", ageText)
	case isFavorite:
		return fmt.Sprintf("你收藏的内容 · %s发布", ageText)
	default:
		return fmt.Sprintf("%s发布的新内容", ageText)
	}
}

// ageText 时长文案
func ageText(hours float64) string {
	switch {
	case hours < 1:
		return "刚刚"
	case hours < 24:
		return fmt.Sprintf("%.0f 小时前", hours)
	case hours < 24*30:
		return fmt.Sprintf("%.0f 天前", hours/24)
	default:
		return "较早"
	}
}

// articleAgeHours 文章年龄（小时），优先发布时间其次抓取时间
func articleAgeHours(a *models.Article, now time.Time) float64 {
	t := a.FetchedAt
	if a.PublishedAt != nil && !a.PublishedAt.IsZero() {
		t = *a.PublishedAt
	}
	if t.IsZero() {
		return 0
	}
	return math.Max(0, now.Sub(t).Hours())
}

// bestPositiveMatch 与正簇质心集合算最大匹配（低于匹配阈值取 0）
func bestPositiveMatch(vec []float32, clusters []models.InterestCluster, centroids [][]float32) (posMatch float64, posLabel string) {
	for i := range centroids {
		sim := cosineSim(vec, centroids[i])
		if sim > posMatch {
			posMatch = sim
			if i < len(clusters) {
				posLabel = clusters[i].Label
			}
		}
	}
	if posMatch < clusterPosMatchThreshold {
		posMatch, posLabel = 0, ""
	}
	return posMatch, posLabel
}

// cosineSim float32 向量余弦相似度（float64 返回）
func cosineSim(a, b []float32) float64 {
	return float64(ai.CalculateCosineSimilarity(a, b))
}

// bestSimilarity 与一组质心的最大相似度
func bestSimilarity(vec []float32, centroids [][]float32) float64 {
	best := 0.0
	for _, c := range centroids {
		if sim := cosineSim(vec, c); sim > best {
			best = sim
		}
	}
	return best
}

// clamp01 夹到 [0,1]
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// feedAuthorityMap feed_id → authority
func feedAuthorityMap(db *database.DB) map[int64]int {
	feeds, err := db.ListFeeds()
	if err != nil {
		return map[int64]int{}
	}
	m := make(map[int64]int, len(feeds))
	for _, f := range feeds {
		m[f.ID] = f.Authority
	}
	return m
}
