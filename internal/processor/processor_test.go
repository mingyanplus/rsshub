package processor

import (
	"testing"
)

func TestProcessStats(t *testing.T) {
	stats := &ProcessStats{
		TotalArticles:   10,
		NewArticles:     8,
		SkippedArticles: 2,
		AdArticles:      1,
	}

	if stats.TotalArticles != 10 {
		t.Errorf("TotalArticles = %d, want 10", stats.TotalArticles)
	}
	if stats.NewArticles != 8 {
		t.Errorf("NewArticles = %d, want 8", stats.NewArticles)
	}
	if stats.SkippedArticles != 2 {
		t.Errorf("SkippedArticles = %d, want 2", stats.SkippedArticles)
	}
	if stats.AdArticles != 1 {
		t.Errorf("AdArticles = %d, want 1", stats.AdArticles)
	}
}

func TestShouldProcessArticle(t *testing.T) {
	// 测试新文章应该处理
	if !ShouldProcessArticle(false) {
		t.Error("New article should be processed")
	}

	// 测试已存在的文章不应处理
	if ShouldProcessArticle(true) {
		t.Error("Existing article should not be processed")
	}
}

func TestIsAdArticle(t *testing.T) {
	// 测试广告文章
	adResult := &AIResult{IsAd: true, AdReason: "Promotional content"}
	if !IsAdArticle(adResult) {
		t.Error("Should detect ad article")
	}

	// 测试非广告文章
	normalResult := &AIResult{IsAd: false}
	if IsAdArticle(normalResult) {
		t.Error("Should not detect normal article as ad")
	}

	// 测试 nil 结果
	if IsAdArticle(nil) {
		t.Error("nil result should not be ad")
	}
}

func TestProcessArticleResult(t *testing.T) {
	// 测试正常结果
	result := &AIResult{
		IsAd:     false,
		Summary:  "Test summary",
		Keywords: []string{"go", "test"},
	}
	isAd, adReason := ProcessArticleResult(result)
	if isAd {
		t.Error("Should not be ad")
	}
	if adReason != "" {
		t.Errorf("AdReason should be empty, got %s", adReason)
	}

	// 测试广告结果
	adResult := &AIResult{
		IsAd:     true,
		AdReason: "Promotional content",
	}
	isAd, adReason = ProcessArticleResult(adResult)
	if !isAd {
		t.Error("Should be ad")
	}
	if adReason != "Promotional content" {
		t.Errorf("AdReason = %s, want 'Promotional content'", adReason)
	}

	// 测试 nil 结果
	isAd, adReason = ProcessArticleResult(nil)
	if isAd {
		t.Error("nil result should not be ad")
	}
}

func TestCalculateStats(t *testing.T) {
	stats := CalculateStats(100, 80, 20, 5)

	if stats.TotalArticles != 100 {
		t.Errorf("TotalArticles = %d, want 100", stats.TotalArticles)
	}
	if stats.NewArticles != 80 {
		t.Errorf("NewArticles = %d, want 80", stats.NewArticles)
	}
	if stats.SkippedArticles != 20 {
		t.Errorf("SkippedArticles = %d, want 20", stats.SkippedArticles)
	}
	if stats.AdArticles != 5 {
		t.Errorf("AdArticles = %d, want 5", stats.AdArticles)
	}
}

func TestAIResultFields(t *testing.T) {
	result := &AIResult{
		IsAd:           false,
		AdReason:       "",
		ContentCleaned: "Cleaned content",
		Summary:        "Article summary",
		Keywords:       []string{"go", "testing", "tdd"},
		Category:       "Technology",
		Tags:           []string{"golang", "backend"},
	}

	if result.ContentCleaned != "Cleaned content" {
		t.Errorf("ContentCleaned = %v", result.ContentCleaned)
	}
	if len(result.Keywords) != 3 {
		t.Errorf("Keywords length = %d, want 3", len(result.Keywords))
	}
	if result.Category != "Technology" {
		t.Errorf("Category = %v", result.Category)
	}
	if len(result.Tags) != 2 {
		t.Errorf("Tags length = %d, want 2", len(result.Tags))
	}
}
