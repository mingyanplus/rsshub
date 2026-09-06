package processor

import (
	"testing"
	"time"

	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
)

// testDB 带预建 feed 的测试库
type testDB struct {
	*database.DB
	feedID int64
}

func newRecTestDB(t *testing.T) (*testDB, *InterestProfile) {
	t.Helper()
	db, profile := newProfileTestDB(t)
	feed := &models.Feed{Title: "F", URL: "https://f.com/" + time.Now().Format("150405.000000000"), IsActive: true}
	feedID, err := db.CreateFeed(feed)
	if err != nil {
		t.Fatal(err)
	}
	return &testDB{db, feedID}, profile
}

func seedCandidate(t *testing.T, db *testDB, title, link string, vec []float32, ageHours float64) int64 {
	t.Helper()
	emb, _ := ai.SerializeEmbedding(vec)
	published := time.Now().Add(-time.Duration(ageHours) * time.Hour)
	res, err := db.Exec(`INSERT INTO articles (feed_id, title, link, content, keywords, embedding, published_at, fetched_at)
		VALUES (?, ?, ?, 'c', '', ?, ?, ?)`, db.feedID, title, link, emb, published, published)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestRecommenderScoring(t *testing.T) {
	db, profile := newRecTestDB(t)

	// 建立正簇：Go 方向（模拟读完两篇）
	goVec := []float32{1, 0, 0, 0}
	id1 := seedProfileArticle(t, db.DB, goVec, "Go,并发")
	a1, _ := db.GetArticleProfileData(id1)
	profile.RecordFeedback(a1, PolarityPositive, 3)
	// 负簇：广告方向
	negID := seedProfileArticle(t, db.DB, []float32{-1, 0, 0.05, 0}, "广告")
	aNeg, _ := db.GetArticleProfileData(negID)
	profile.RecordFeedback(aNeg, PolarityNegative, 2)

	r := NewRecommender(db.DB, profile)
	scored, err := r.Recommend(20)
	if err != nil {
		t.Fatalf("Recommend() error = %v", err)
	}

	// 候选：兴趣匹配的新文章 vs 同样新但无关方向的文章 vs 负簇方向文章
	interestID := seedCandidate(t, db, "Go 1.24 新特性", "https://go.dev/new", []float32{0.95, 0.3, 0, 0}, 2)
	unrelatedID := seedCandidate(t, db, "园艺日记", "https://garden.com/a", []float32{0, 0.2, 0.98, 0}, 2)
	adishID := seedCandidate(t, db, "限时促销", "https://ad.com/a", []float32{-0.97, 0.1, 0, 0}, 1)

	scored, err = r.Recommend(20)
	if err != nil {
		t.Fatalf("Recommend() error = %v", err)
	}

	byID := make(map[int64]*ScoredArticle)
	for _, s := range scored {
		byID[s.Article.ID] = s
	}

	in, ok := byID[interestID]
	if !ok {
		t.Fatal("兴趣匹配文章应进入推荐列表")
	}
	if in.Interest == 0 || in.Channel != ChannelPrecise {
		t.Errorf("兴趣文章应有兴趣分且通道为 precise: interest=%.3f channel=%s", in.Interest, in.Channel)
	}
	if in.Reason == "" {
		t.Error("兴趣文章应有推荐理由")
	}

	un, ok := byID[unrelatedID]
	if !ok {
		t.Fatal("无关文章应进入推荐列表（新鲜度通道）")
	}
	if un.Interest != 0 || un.Channel != ChannelFreshness {
		t.Errorf("无关文章应无兴趣分且通道为 freshness: interest=%.3f channel=%s", un.Interest, un.Channel)
	}
	if in.Score <= un.Score {
		t.Errorf("兴趣文章分数 (%.3f) 应高于无关文章 (%.3f)", in.Score, un.Score)
	}

	ad, ok := byID[adishID]
	if !ok {
		t.Fatal("负簇方向文章仍会出现（惩罚压低分数），应进入列表")
	}
	if ad.Penalty >= -0.2 {
		t.Errorf("负簇相似文章应有明显惩罚, got %.3f", ad.Penalty)
	}
	if ad.Score >= in.Score {
		t.Errorf("负簇文章分数 (%.3f) 不应高于兴趣文章 (%.3f)", ad.Score, in.Score)
	}
}

func TestRecommenderFallback(t *testing.T) {
	db, profile := newRecTestDB(t)

	// 无任何簇（画像空）→ 走降级链：新文章按新鲜度排
	newID := seedCandidate(t, db, "新鲜事", "https://n.com/a", nil, 1)
	oldID := seedCandidate(t, db, "旧闻", "https://n.com/b", nil, 200)

	r := NewRecommender(db.DB, profile)
	scored, err := r.Recommend(10)
	if err != nil {
		t.Fatalf("Recommend() error = %v", err)
	}
	if len(scored) != 2 {
		t.Fatalf("降级链应返回全部候选, got %d", len(scored))
	}
	// 新文章分数更高（排序在前）
	if scored[0].Article.ID != newID || scored[1].Article.ID != oldID {
		t.Errorf("降级链应按新鲜度排序: got [%d, %d], want [%d, %d]",
			scored[0].Article.ID, scored[1].Article.ID, newID, oldID)
	}
	if scored[0].Channel != ChannelFreshness {
		t.Errorf("降级链通道应为 freshness, got %s", scored[0].Channel)
	}
}

func TestScoreComponents(t *testing.T) {
	// 状态分
	if v := stateScore(false, false); v != scoreStateUnread {
		t.Errorf("stateScore(未读) = %v, want %v", v, scoreStateUnread)
	}
	if v := stateScore(true, true); v != scoreStateRead+scoreStateFavorite {
		t.Errorf("stateScore(已读+收藏) = %v", v)
	}
	// 惩罚分：不感兴趣重罚（应达到 -4 量级）
	if v := penaltyScore(0, true); v > -3.9 {
		t.Errorf("不感兴趣惩罚应接近 -4, got %v", v)
	}
	// 理由文案
	if s := buildReason(0, "", false); s == "" {
		t.Error("理由不应为空")
	}
	_ = models.Article{}
}
