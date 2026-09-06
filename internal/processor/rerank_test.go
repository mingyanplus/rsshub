package processor

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"rss-ai/internal/ai"
	"rss-ai/internal/models"
)

// seedCandidateTopic 造一篇带 topic_category 的候选文章
func seedCandidateTopic(t *testing.T, db *testDB, title string, vec []float32, ageHours int, topic string) int64 {
	t.Helper()
	emb, _ := ai.SerializeEmbedding(vec)
	pub := time.Now().Add(-time.Duration(ageHours) * time.Hour)
	link := fmt.Sprintf("https://t.com/%s-%d", title, rand.Int63())
	res, err := db.Exec(`INSERT INTO articles (feed_id, title, link, content, keywords, embedding, published_at, fetched_at, topic_category)
		VALUES (?, ?, ?, 'c', '', ?, ?, ?, ?)`, db.feedID, title, link, emb, pub, pub, topic)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// fakeArticle 构造只含配额所需字段的文章
func fakeArticle(id int64, topic string) *models.Article {
	return &models.Article{ID: id, TopicCategory: topic, FeedID: 1}
}

// randomUnitVec 随机单位向量（测试用）
func randomUnitVec(n int) []float32 {
	v := make([]float32, n)
	var norm float64
	for i := range v {
		v[i] = rand.Float32() - 0.5
		norm += float64(v[i] * v[i])
	}
	if norm == 0 {
		v[0] = 1
		return v
	}
	s := float32(1 / math.Sqrt(norm))
	for i := range v {
		v[i] *= s
	}
	return v
}

// TestRerankTopicQuota 主题配额：同 topic_category 最多 quotaPerTopic 篇
func TestRerankTopicQuota(t *testing.T) {
	var scored []*ScoredArticle
	for i := 0; i < 6; i++ {
		scored = append(scored, &ScoredArticle{Article: fakeArticle(int64(i+1), "go"), Score: 0.9, Channel: ChannelPrecise})
	}
	scored = append(scored,
		&ScoredArticle{Article: fakeArticle(7, "family"), Score: 0.5, Channel: ChannelPrecise},
		&ScoredArticle{Article: fakeArticle(8, "food"), Score: 0.4, Channel: ChannelPrecise},
		&ScoredArticle{Article: fakeArticle(9, "travel"), Score: 0.4, Channel: ChannelPrecise},
		&ScoredArticle{Article: fakeArticle(10, "music"), Score: 0.4, Channel: ChannelPrecise},
		&ScoredArticle{Article: fakeArticle(11, "health"), Score: 0.4, Channel: ChannelPrecise},
	)

	result := rerankAndFinalize(scored, 8)
	count := make(map[string]int)
	for _, s := range result {
		count[topicKey(s.Article)]++
	}
	if count["cat:go"] > quotaPerTopic {
		t.Errorf("同主题最多 %d 篇, got %d", quotaPerTopic, count["cat:go"])
	}
	if len(result) < 8 {
		t.Errorf("有足够异主题候选应取满 limit, got %d", len(result))
	}
}

// TestRerankExplorationGuarantee 探索保底：至少 explorationMin 篇来自探索通道
func TestRerankExplorationGuarantee(t *testing.T) {
	var scored []*ScoredArticle
	for i := 0; i < 6; i++ {
		scored = append(scored, &ScoredArticle{Article: fakeArticle(int64(i+1), fmt.Sprintf("topic%d", i)), Score: 0.9, Channel: ChannelPrecise})
	}
	for i := 6; i < 9; i++ {
		scored = append(scored, &ScoredArticle{Article: fakeArticle(int64(i+1), fmt.Sprintf("topic%d", i)), Score: 0.1, Channel: ChannelRandom})
	}

	result := rerankAndFinalize(scored, 6)
	explore := 0
	for _, s := range result {
		if isExplorationChannel(s.Channel) {
			explore++
		}
	}
	if explore < explorationMin {
		t.Errorf("探索保底应 ≥ %d 篇, got %d", explorationMin, explore)
	}
}

// TestRecallChannelMix 五通道召回整体行为：precise 有产出、探索保底、总量不超限
func TestRecallChannelMix(t *testing.T) {
	db, profile := newRecTestDB(t)

	// 画像：一个正簇 + 一个负簇
	goID := seedProfileArticle(t, db.DB, []float32{1, 0, 0, 0}, "Go")
	aGo, _ := db.GetArticleProfileData(goID)
	profile.RecordFeedback(aGo, PolarityPositive, 3)
	negID := seedProfileArticle(t, db.DB, []float32{-1, 0, 0.1, 0}, "广告")
	aNeg, _ := db.GetArticleProfileData(negID)
	profile.RecordFeedback(aNeg, PolarityNegative, 2)

	// 候选：Go 方向 10 篇 + 随机方向 30 篇（混合 topic 类别）
	for i := 0; i < 10; i++ {
		seedCandidateTopic(t, db, fmt.Sprintf("Go %d", i), []float32{0.98, 0.15, 0, 0}, 1, "go")
	}
	for i := 0; i < 30; i++ {
		seedCandidateTopic(t, db, fmt.Sprintf("杂项 %d", i), randomUnitVec(4), 1, fmt.Sprintf("cat%d", i%5))
	}

	r := NewRecommender(db.DB, profile)
	scored, err := r.Recommend(20)
	if err != nil {
		t.Fatalf("Recommend() error = %v", err)
	}
	if len(scored) > 20 {
		t.Errorf("总数超限: %d", len(scored))
	}

	chCount := make(map[string]int)
	for _, s := range scored {
		chCount[s.Channel]++
	}
	if chCount[ChannelPrecise] == 0 {
		t.Error("精准通道应有产出")
	}
	explore := chCount[ChannelAdjacent] + chCount[ChannelCoverage] + chCount[ChannelRandom]
	if len(scored) >= explorationMin && explore < explorationMin {
		t.Errorf("探索通道保底不足: %d (分布 %v)", explore, chCount)
	}
	// 同主题配额仍然生效
	count := make(map[string]int)
	for _, s := range scored {
		count[topicKey(s.Article)]++
		if count[topicKey(s.Article)] > quotaPerTopic {
			t.Fatalf("同主题超过配额: %s × %d", topicKey(s.Article), count[topicKey(s.Article)])
		}
	}
}

// TestExposureCooldown 曝光冷却：7 天前曝光未点击的文章不再出现
func TestExposureCooldown(t *testing.T) {
	db, profile := newRecTestDB(t)

	freshID := seedCandidateTopic(t, db, "新文章", []float32{1, 0, 0, 0}, 1, "go")
	cooledID := seedCandidateTopic(t, db, "冷文章", []float32{0.9, 0.4, 0, 0}, 1, "go")
	// 冷文章 8 天前曝光未点击
	oldExposed := time.Now().Add(-8 * 24 * time.Hour).Unix()
	if _, err := db.Exec(`INSERT INTO exposures (article_id, position, channel, clicked, exposed_at) VALUES (?, 0, 'precise', 0, ?)`, cooledID, oldExposed); err != nil {
		t.Fatal(err)
	}

	r := NewRecommender(db.DB, profile)
	scored, err := r.Recommend(10)
	if err != nil {
		t.Fatalf("Recommend() error = %v", err)
	}
	foundFresh := false
	for _, s := range scored {
		if s.Article.ID == cooledID {
			t.Error("曝光 7 天未点击的文章应被冷却剔除")
		}
		if s.Article.ID == freshID {
			foundFresh = true
		}
	}
	if !foundFresh {
		t.Error("未冷却文章应正常出现")
	}
}
