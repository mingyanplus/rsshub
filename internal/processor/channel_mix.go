package processor

import (
	"log"
	"sort"
	"time"
)

// 通道权重自适应参数（方案 §7.2 简化 MAB）
const (
	channelClickFactor = 1.1  // 点击/读完：权重 ×1.1
	channelSkipFactor  = 0.95 // 曝光未点击：权重 ×0.95
	channelWeightMin   = 0.2  // 单通道权重下限（防跌零）
	channelWeightMax   = 3.0  // 单通道权重上限（防独大）
	channelSkipWindow  = 7 * 24 * 3600 // 跳过惩罚统计窗口（秒）
)

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
	weights, err := r.db.GetChannelWeights()
	if err != nil {
		log.Printf("通道权重: 读取失败: %v", err)
		return
	}
	// 确保五个通道都有记录
	for ch := range defaultChannelMix {
		if _, ok := weights[ch]; !ok {
			weights[ch] = 1
		}
	}
	w := weights[channel] * factor
	if w < channelWeightMin {
		w = channelWeightMin
	} else if w > channelWeightMax {
		w = channelWeightMax
	}
	weights[channel] = w

	if err := r.db.SaveChannelWeights(normalizeWeights(weights)); err != nil {
		log.Printf("通道权重: 写回失败: %v", err)
	}
}

// ApplyChannelSkipPenalty 跳过惩罚：对 [7天前, 6天前) 窗口内曝光至今未点击的通道 ×0.95
// （每天只惩罚一天的量，避免旧文章被重复计罚；每日衰减任务调用）
func (r *Recommender) ApplyChannelSkipPenalty() error {
	now := time.Now().Unix()
	channels, err := r.db.ListUnclickedExposureChannels(now-channelSkipWindow, now-channelSkipWindow+24*3600)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		r.AdjustChannelWeight(ch, channelSkipFactor)
	}
	if len(channels) > 0 {
		log.Printf("通道权重: 跳过惩罚应用于 %v", channels)
	}
	return nil
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

// sortedChannels 通道名有序列表（日志/调试用）
func sortedChannels(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for ch := range m {
		out = append(out, ch)
	}
	sort.Strings(out)
	return out
}
