package processor

// AIResult AI 分析结果
type AIResult struct {
	IsAd           bool     `json:"is_ad"`
	AdReason       string   `json:"ad_reason"`
	ContentCleaned string   `json:"content_cleaned"`
	Summary        string   `json:"summary"`
	Keywords       []string `json:"keywords"`
	Category       string   `json:"category"`
	Tags           []string `json:"tags"`
}

// ProcessStats 处理统计
type ProcessStats struct {
	TotalArticles   int
	NewArticles     int
	SkippedArticles int
	AdArticles      int
}

// ShouldProcessArticle 判断是否应该处理文章
func ShouldProcessArticle(articleExists bool) bool {
	return !articleExists
}

// FilterAdArticles 过滤广告文章
func FilterAdArticles[T interface{ GetIsAd() bool }](articles []T) []T {
	var result []T
	for _, article := range articles {
		if !article.GetIsAd() {
			result = append(result, article)
		}
	}
	return result
}

// IsAdArticle 判断是否为广告文章
func IsAdArticle(aiResult *AIResult) bool {
	return aiResult != nil && aiResult.IsAd
}

// ProcessArticleResult 处理单篇文章的结果
func ProcessArticleResult(aiResult *AIResult) (isAd bool, adReason string) {
	if aiResult == nil {
		return false, ""
	}
	return aiResult.IsAd, aiResult.AdReason
}

// CalculateStats 计算处理统计
func CalculateStats(total, newCount, skipped, adCount int) *ProcessStats {
	return &ProcessStats{
		TotalArticles:   total,
		NewArticles:     newCount,
		SkippedArticles: skipped,
		AdArticles:      adCount,
	}
}
