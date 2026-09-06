package processor

import (
	"os"
	"testing"
	"time"

	"rss-ai/internal/ai"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
)

func newProfileTestDB(t *testing.T) (*database.DB, *InterestProfile) {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "profile_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()
	t.Cleanup(func() { os.Remove(tmpfile.Name()) })

	db, err := database.New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, NewInterestProfile(db)
}

func seedProfileArticle(t *testing.T, db *database.DB, vec []float32, keywords string) int64 {
	t.Helper()
	feed := &models.Feed{Title: "T", URL: "https://t.com/f" + time.Now().Format("150405.000000000"), IsActive: true}
	feedID, err := db.CreateFeed(feed)
	if err != nil {
		t.Fatal(err)
	}
	emb, _ := ai.SerializeEmbedding(vec)
	res, err := db.Exec(`INSERT INTO articles (feed_id, title, link, content, keywords, embedding, fetched_at)
		VALUES (?, 'a', ?, 'c', ?, ?, CURRENT_TIMESTAMP)`, feedID, feed.URL+"/a"+time.Now().Format("150405.000000000"), keywords, emb)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestInterestProfileClusterAndMerge(t *testing.T) {
	db, profile := newProfileTestDB(t)

	// 两条高相似向量 → 应并入同一簇
	v1 := []float32{1, 0, 0, 0}
	v2 := []float32{0.99, 0.05, 0, 0}
	id1 := seedProfileArticle(t, db, v1, "Go,并发")
	id2 := seedProfileArticle(t, db, v2, "Go,goroutine")

	a1, err := db.GetArticleProfileData(id1)
	if err != nil {
		t.Fatal(err)
	}
	profile.RecordFeedback(a1, PolarityPositive, 1)

	clusters, err := db.ListInterestClusters(PolarityPositive)
	if err != nil || len(clusters) != 1 {
		t.Fatalf("首次反馈后应有 1 个正簇, got %d (err=%v)", len(clusters), err)
	}
	first := clusters[0]
	if first.Label == "" {
		t.Error("簇标签不应为空")
	}

	a2, err := db.GetArticleProfileData(id2)
	if err != nil {
		t.Fatal(err)
	}
	profile.RecordFeedback(a2, PolarityPositive, 1)

	clusters, _ = db.ListInterestClusters(PolarityPositive)
	if len(clusters) != 1 {
		t.Fatalf("相似向量应并入同一簇, got %d", len(clusters))
	}
	if clusters[0].ID != first.ID || clusters[0].SampleCount != 2 || clusters[0].Weight != 2 {
		t.Errorf("簇未正确合并: id=%d sample=%d weight=%v", clusters[0].ID, clusters[0].SampleCount, clusters[0].Weight)
	}

	// 半相关向量（cos≈0.7，落在 [创建阈值, 合并阈值) 区间）→ 新建簇（方案 §3.1 第 3 步）
	v3 := []float32{0.7, 0.714, 0, 0}
	id3 := seedProfileArticle(t, db, v3, "育儿,教育")
	a3, _ := db.GetArticleProfileData(id3)
	profile.RecordFeedback(a3, PolarityPositive, 1)
	clusters, _ = db.ListInterestClusters(PolarityPositive)
	if len(clusters) != 2 {
		t.Fatalf("半相关向量应新建簇, got %d", len(clusters))
	}

	// 完全不相关向量（sim < 创建阈值）→ 视为噪声忽略
	id5 := seedProfileArticle(t, db, []float32{0, 0, 1, 0}, "乱码")
	a5, _ := db.GetArticleProfileData(id5)
	profile.RecordFeedback(a5, PolarityPositive, 1)
	clusters, _ = db.ListInterestClusters(PolarityPositive)
	if len(clusters) != 2 {
		t.Errorf("低于创建阈值的反馈应被忽略, got %d", len(clusters))
	}

	// 负反馈（秒退）
	id4 := seedProfileArticle(t, db, []float32{-1, 0, 0.1, 0}, "广告,促销")
	a4, _ := db.GetArticleProfileData(id4)
	profile.RecordFeedback(a4, PolarityNegative, 1)
	negClusters, _ := db.ListInterestClusters(PolarityNegative)
	if len(negClusters) != 1 {
		t.Fatalf("负反馈应建负簇, got %d", len(negClusters))
	}

	// 负簇相似度：秒退方向的文章应与负簇匹配达惩罚阈值以上
	_, negCentroids := profile.NegativeCentroids()
	negSim := bestSimilarity([]float32{-0.98, 0, 0.15, 0}, negCentroids)
	if negSim < clusterNegPenaltyThreshold {
		t.Errorf("负簇相似度应 ≥ 惩罚阈值, got %v", negSim)
	}
}

func TestInterestProfileDailyDecay(t *testing.T) {
	db, profile := newProfileTestDB(t)

	id := seedProfileArticle(t, db, []float32{1, 0, 0, 0}, "Go")
	a, _ := db.GetArticleProfileData(id)
	profile.RecordFeedback(a, PolarityPositive, 1)

	clusters, _ := db.ListInterestClusters(PolarityPositive)
	if len(clusters) != 1 {
		t.Fatalf("应有 1 个簇, got %d", len(clusters))
	}
	c := clusters[0]

	// 模拟 31 天前的单样本簇：超期应被删除
	old := c.LastActiveAt - int64(31*24*3600)
	db.Exec(`UPDATE interest_clusters SET last_active_at = ? WHERE id = ?`, old, c.ID)

	if err := profile.ApplyDailyDecay(); err != nil {
		t.Fatalf("ApplyDailyDecay() error = %v", err)
	}
	clusters, _ = db.ListInterestClusters(PolarityPositive)
	if len(clusters) != 0 {
		t.Errorf("超期单样本簇应被删除, got %d", len(clusters))
	}
}

func TestInterestProfileSeedFromSubscriptions(t *testing.T) {
	db, profile := newProfileTestDB(t)

	// 无行为日志时，两个 feed 各一篇文章 → 冷启动建 2 个先验簇
	seedProfileArticle(t, db, []float32{1, 0, 0, 0}, "Go,编程")
	seedProfileArticle(t, db, []float32{0, 1, 0, 0}, "育儿")

	if err := profile.SeedFromSubscriptions(); err != nil {
		t.Fatalf("SeedFromSubscriptions() error = %v", err)
	}
	clusters, _ := db.ListInterestClusters(PolarityPositive)
	if len(clusters) != 2 {
		t.Fatalf("冷启动应建 2 个先验簇, got %d", len(clusters))
	}
	for _, c := range clusters {
		if c.Weight != seedInitWeight {
			t.Errorf("先验簇权重应为 %v, got %v", seedInitWeight, c.Weight)
		}
	}
}

func TestMergeClusterLabelNoDuplication(t *testing.T) {
	// 旧标签（顿号分隔）与新关键词应正确去重合并，而非重复堆叠
	got := mergeClusterLabel("Go、并发", []string{"Go", "goroutine"}, 3)
	if got != "Go、goroutine、并发" {
		t.Errorf("mergeClusterLabel = %q, want %q", got, "Go、goroutine、并发")
	}
	// 逗号格式兼容
	if got := mergeClusterLabel("Go,并发", []string{"Go"}, 3); got != "Go、并发" {
		t.Errorf("mergeClusterLabel(逗号) = %q", got)
	}
	// 空旧标签
	if got := mergeClusterLabel("", []string{"a", "b"}, 3); got != "a、b" {
		t.Errorf("mergeClusterLabel(空) = %q", got)
	}
}
