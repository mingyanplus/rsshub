package processor

import (
	"log"
	"time"
)

// 通道权重自适应参数（方案 §7.2 简化 MAB）
const (
	ChannelClickFactor = 1.1           // 点击/读完：权重增益（导出供 handler 引用）
	channelSkipFactor  = 0.95          // 曝光未点击：权重 ×0.95
	channelWeightMin   = 0.2           // 单通道权重下限（防跌零）
	channelWeightMax   = 3.0           // 单通道权重上限（防独大）
	channelSkipWindow  = 7 * 24 * 3600 // 跳过惩罚统计窗口（秒）
)

// 推荐通道（五通道，方案 §4）与双模式配比统一在此定义
const (
	ChannelPrecise   = "precise"   // 精准：正簇匹配
	ChannelFreshness = "freshness" // 新鲜度
	ChannelAdjacent  = "adjacent"  // 邻接簇
	ChannelCoverage  = "coverage"  // 主题覆盖（盲区）
	ChannelRandom    = "random"    // 随机探索
)

// defaultChannelMix 精选模式默认配比（方案附录）
var defaultChannelMix = map[string]float64{
	ChannelPrecise:   0.50,
	ChannelFreshness: 0.15,
	ChannelAdjacent:  0.15,
	ChannelCoverage:  0.10,
	ChannelRandom:    0.10,
}

// discoverChannelMix 拓展发现模式配比（方案 §6.5：探索主导）
var discoverChannelMix = map[string]float64{
	ChannelAdjacent:  0.35,
	ChannelCoverage:  0.25,
	ChannelRandom:    0.15,
	ChannelFreshness: 0.15,
	ChannelPrecise:   0.10,
}

// ChannelMixFor 按模式取基础配比（curated 精选 / discover 拓展发现）
func ChannelMixFor(mode string) map[string]float64 {
	if mode == "discover" {
		return discoverChannelMix
	}
	return defaultChannelMix
}

// effectiveMix 当前生效配比：基础配比 × 存储的通道权重，归一化后返回
func (r *Recommender) effectiveMix(mode string) map[string]float64 {
	base := ChannelMixFor(mode)
	weights, err := r.db.GetChannelWeights()
	if err != nil || len(weights) == 0 {
		return base
	}

	mix := make(map[string]float64, len(base))
	total := 0.0
	for ch, ratio := range base {
		w, ok := weights[ch]
		if !ok || w <= 0 {
			w = 1
		}
		mix[ch] = ratio * w
		total += mix[ch]
	}
	if total <= 0 {
		return base
	}
	for ch := range mix {
		mix[ch] /= total
	}
	return mix
}

// RecommendMode 按模式推荐（模式决定基础配比，通道自适应权重叠加其上）
func (r *Recommender) RecommendMode(mode string, limit int) ([]*ScoredArticle, error) {
	return r.RecommendWithMix(limit, r.effectiveMix(mode))
}

// AdjustChannelWeight 通道权重调整（factor > 1 增强 / < 1 收缩），调整后立即归一化写回
func (r *Recommender) AdjustChannelWeight(channel string, factor float64) {
	if channel == "" {
		return
	}
	weights, ok := r.loadChannelWeights()
	if !ok {
		return
	}
	applyFactor(weights, channel, factor)
	if err := r.db.SaveChannelWeights(normalizeWeights(weights)); err != nil {
		log.Printf("通道权重: 写回失败: %v", err)
	}
}

// ApplyChannelSkipPenalty 跳过惩罚：对 [7天前, 6天前) 窗口内曝光至今未点击的通道 ×0.95
// （每天只惩罚一天的量，避免旧文章被重复计罚；每日衰减任务调用。批量读一次改一次写）
func (r *Recommender) ApplyChannelSkipPenalty() error {
	now := time.Now().Unix()
	channels, err := r.db.ListUnclickedExposureChannels(now-channelSkipWindow, now-channelSkipWindow+24*3600)
	if err != nil || len(channels) == 0 {
		return err
	}
	weights, ok := r.loadChannelWeights()
	if !ok {
		return nil
	}
	for _, ch := range channels {
		applyFactor(weights, ch, channelSkipFactor)
	}
	if err := r.db.SaveChannelWeights(normalizeWeights(weights)); err != nil {
		return err
	}
	log.Printf("通道权重: 跳过惩罚应用于 %v", channels)
	return nil
}

// loadChannelWeights 读取权重并补全五通道缺省记录
func (r *Recommender) loadChannelWeights() (map[string]float64, bool) {
	weights, err := r.db.GetChannelWeights()
	if err != nil {
		log.Printf("通道权重: 读取失败: %v", err)
		return nil, false
	}
	for ch := range defaultChannelMix {
		if _, ok := weights[ch]; !ok {
			weights[ch] = 1
		}
	}
	return weights, true
}

// applyFactor 单通道权重乘系数并夹紧到 [min, max]
func applyFactor(weights map[string]float64, channel string, factor float64) {
	w := weights[channel] * factor
	w = min(channelWeightMax, max(channelWeightMin, w))
	weights[channel] = w
}

// normalizeWeights 权重归一化到均值 1（保持相对比例，总量不漂移）
func normalizeWeights(weights map[string]float64) map[string]float64 {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	if total <= 0 {
		return weights
	}
	n := float64(len(weights))
	for ch, w := range weights {
		weights[ch] = w * n / total
	}
	return weights
}
