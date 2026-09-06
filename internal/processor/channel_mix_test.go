package processor

import (
	"testing"
	"time"
)

// TestChannelMixFor 双模式配比
func TestChannelMixFor(t *testing.T) {
	curated := ChannelMixFor("curated")
	if curated[ChannelPrecise] != 0.50 {
		t.Errorf("精选模式 precise 应为 0.50, got %v", curated[ChannelPrecise])
	}
	discover := ChannelMixFor("discover")
	if discover[ChannelAdjacent] != 0.35 {
		t.Errorf("发现模式 adjacent 应为 0.35, got %v", discover[ChannelAdjacent])
	}
	// 未知模式回落精选
	if ChannelMixFor("bogus")[ChannelPrecise] != 0.50 {
		t.Error("未知模式应回落精选配比")
	}
	// 配比各自归一
	for name, mix := range map[string]map[string]float64{"curated": curated, "discover": discover} {
		sum := 0.0
		for _, v := range mix {
			sum += v
		}
		if sum < 0.999 || sum > 1.001 {
			t.Errorf("%s 配比总和应为 1, got %v", name, sum)
		}
	}
}

// TestAdjustChannelWeight 通道权重自适应：点击增强、夹紧、归一化
func TestAdjustChannelWeight(t *testing.T) {
	db, profile := newRecTestDB(t)
	r := NewRecommender(db.DB, profile)

	// 点击 precise ×1.1（归一化到均值 1 后，precise 应比其他通道高 10%）
	r.AdjustChannelWeight(ChannelPrecise, 1.1)
	weights, err := db.GetChannelWeights()
	if err != nil {
		t.Fatal(err)
	}
	others := 0.0
	for ch, w := range weights {
		if ch != ChannelPrecise {
			others = w
			break
		}
	}
	ratio := weights[ChannelPrecise] / others
	if ratio < 1.09 || ratio > 1.11 {
		t.Errorf("precise 相对权重应 ≈1.1, got %v (precise=%v other=%v)", ratio, weights[ChannelPrecise], others)
	}

	// 连续增强后不超上限
	for i := 0; i < 50; i++ {
		r.AdjustChannelWeight(ChannelRandom, 1.1)
	}
	weights, _ = db.GetChannelWeights()
	if weights[ChannelRandom] > channelWeightMax+0.001 {
		t.Errorf("random 权重不应超过上限 %v, got %v", channelWeightMax, weights[ChannelRandom])
	}
	// 权重均值保持 1（归一化）
	total := 0.0
	for _, w := range weights {
		total += w
	}
	mean := total / float64(len(weights))
	if mean < 0.999 || mean > 1.001 {
		t.Errorf("归一化后权重均值应为 1, got %v", mean)
	}
}

// TestEffectiveMix 生效配比：存储权重改变配比且归一化
func TestEffectiveMix(t *testing.T) {
	db, profile := newRecTestDB(t)
	r := NewRecommender(db.DB, profile)

	base := r.effectiveMix("curated")
	if base[ChannelPrecise] != 0.50 {
		t.Errorf("无存储权重时应用默认配比, got %v", base[ChannelPrecise])
	}

	// adjacent ×3 后，discover 模式 adjacent 配比应明显高于基准
	db.SaveChannelWeights(map[string]float64{ChannelAdjacent: channelWeightMax})
	mix := r.effectiveMix("discover")
	if mix[ChannelAdjacent] <= discoverChannelMix[ChannelAdjacent] {
		t.Errorf("adjacent 权重上调后配比应放大, got %v (base %v)", mix[ChannelAdjacent], discoverChannelMix[ChannelAdjacent])
	}
	sum := 0.0
	for _, v := range mix {
		sum += v
	}
	if sum < 0.999 || sum > 1.001 {
		t.Errorf("生效配比总和应为 1, got %v", sum)
	}
}

// TestApplyChannelSkipPenalty 跳过惩罚：7 天前曝光未点击的通道 ×0.95
func TestApplyChannelSkipPenalty(t *testing.T) {
	db, profile := newRecTestDB(t)
	r := NewRecommender(db.DB, profile)

	id := seedCandidateTopic(t, db, "惩罚对象", []float32{1, 0, 0, 0}, 1, "go")
	// 7.5 天前曝光未点击（落在 [7天前, 6天前) 窗口外——应为 [since, until)=[7d, 6d) 才对？
	// 实现取 [now-7d, now-7d+1d) = [7天前, 6天前)，此处取 6.5 天前命中窗口）
	db.Exec(`INSERT INTO exposures (article_id, position, channel, clicked, exposed_at) VALUES (?, 0, ?, 0, ?)`,
		id, ChannelAdjacent, time.Now().Add(-156*time.Hour).Unix())

	if err := r.ApplyChannelSkipPenalty(); err != nil {
		t.Fatalf("ApplyChannelSkipPenalty() error = %v", err)
	}
	weights, _ := db.GetChannelWeights()
	if _, ok := weights[ChannelAdjacent]; !ok {
		t.Fatal("adjacent 应有权重记录")
	}
	// adjacent 初始 1 ×0.95 = 0.95（其他通道补齐到均值 1 后可能有微调，取 0.9~1.0 区间）
	if weights[ChannelAdjacent] > 1.0 || weights[ChannelAdjacent] < 0.9 {
		t.Errorf("adjacent 应被惩罚至 ≈0.95, got %v", weights[ChannelAdjacent])
	}
}

// TestComputeMetrics 指标计算
func TestComputeMetrics(t *testing.T) {
	db, profile := newRecTestDB(t)
	r := NewRecommender(db.DB, profile)

	// 两个主题：tech 读 3 篇，life 读 1 篇 → 覆盖率 1.0，熵 > 0
	for i := 0; i < 3; i++ {
		id := seedCandidateTopic(t, db, "tech 已读", []float32{1, 0, 0, 0}, 5, "tech")
		db.MarkArticleRead(id)
	}
	lifeID := seedCandidateTopic(t, db, "life 已读", []float32{0, 1, 0, 0}, 5, "life")
	db.MarkArticleRead(lifeID)

	// 曝光：precise 10 曝 3 点，random 5 曝 2 点 → explore_ctr = 0.4/0.3
	for i := 0; i < 10; i++ {
		id := seedCandidateTopic(t, db, "tech 候选", []float32{0.99, 0.1, 0, 0}, 1, "tech")
		db.Exec(`INSERT INTO exposures (article_id, position, channel, clicked, exposed_at) VALUES (?, ?, 'precise', ?, ?)`,
			id, i, boolInt(i < 3), time.Now().Add(-time.Duration(i)*time.Hour).Unix())
	}
	for i := 0; i < 5; i++ {
		id := seedCandidateTopic(t, db, "随机候选", randomUnitVec(4), 1, "life")
		db.Exec(`INSERT INTO exposures (article_id, position, channel, clicked, exposed_at) VALUES (?, ?, 'random', ?, ?)`,
			id, i, boolInt(i < 2), time.Now().Add(-time.Duration(i)*time.Hour).Unix())
	}

	m, err := r.ComputeMetrics()
	if err != nil {
		t.Fatalf("ComputeMetrics() error = %v", err)
	}
	if m.TopicCoverage != 1.0 {
		t.Errorf("主题覆盖率应为 1.0, got %v", m.TopicCoverage)
	}
	if m.TopicEntropy <= 0 {
		t.Errorf("主题熵应为正, got %v", m.TopicEntropy)
	}
	wantCTR := (2.0 / 5.0) / (3.0 / 10.0)
	if m.ExploreCTR < wantCTR-0.01 || m.ExploreCTR > wantCTR+0.01 {
		t.Errorf("探索点击率应 ≈ %.2f, got %v", wantCTR, m.ExploreCTR)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
