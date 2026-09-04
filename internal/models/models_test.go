package models

import (
	"testing"
	"time"
)

func TestFeedModel(t *testing.T) {
	now := time.Now()
	feed := Feed{
		ID:            1,
		Title:         "Test Feed",
		URL:           "https://example.com/feed.xml",
		Description:   "A test feed",
		LastFetchedAt: &now,
		FetchInterval: 30,
		IsActive:      true,
		CreatedAt:     now,
	}

	if feed.ID != 1 {
		t.Errorf("Feed.ID = %v, want 1", feed.ID)
	}
	if feed.Title != "Test Feed" {
		t.Errorf("Feed.Title = %v, want Test Feed", feed.Title)
	}
	if feed.URL != "https://example.com/feed.xml" {
		t.Errorf("Feed.URL = %v, want https://example.com/feed.xml", feed.URL)
	}
	if feed.FetchInterval != 30 {
		t.Errorf("Feed.FetchInterval = %v, want 30", feed.FetchInterval)
	}
	if !feed.IsActive {
		t.Error("Feed.IsActive should be true")
	}
}

func TestArticleModel(t *testing.T) {
	now := time.Now()
	categoryID := int64(2)
	article := Article{
		ID:             1,
		FeedID:         1,
		CategoryID:     &categoryID,
		Title:          "Test Article",
		Link:           "https://example.com/article",
		Content:        "Full content",
		ContentCleaned: "Cleaned content",
		Summary:        "Article summary",
		Keywords:       "go,testing,tdd",
		TagsCache:      "tech,programming",
		IsAd:           false,
		AdReason:       "",
		PublishedAt:    &now,
		FetchedAt:      now,
		Embedding:      []byte{0x01, 0x02, 0x03},
	}

	if article.FeedID != 1 {
		t.Errorf("Article.FeedID = %v, want 1", article.FeedID)
	}
	if article.Title != "Test Article" {
		t.Errorf("Article.Title = %v, want Test Article", article.Title)
	}
	if article.IsAd {
		t.Error("Article.IsAd should be false")
	}
	if len(article.Embedding) != 3 {
		t.Errorf("Article.Embedding length = %v, want 3", len(article.Embedding))
	}
}

func TestArticleIsAdTrue(t *testing.T) {
	article := Article{
		ID:       1,
		Title:    "Sponsored Post",
		IsAd:     true,
		AdReason: "Contains promotional content",
	}

	if !article.IsAd {
		t.Error("Article.IsAd should be true")
	}
	if article.AdReason != "Contains promotional content" {
		t.Errorf("Article.AdReason = %v", article.AdReason)
	}
}

func TestCategoryModel(t *testing.T) {
	category := Category{
		ID:          1,
		Name:        "Technology",
		Description: "Tech news",
		Color:       "#3498db",
		CreatedAt:   time.Now(),
	}

	if category.Name != "Technology" {
		t.Errorf("Category.Name = %v, want Technology", category.Name)
	}
	if category.Color != "#3498db" {
		t.Errorf("Category.Color = %v, want #3498db", category.Color)
	}
}

func TestTagModel(t *testing.T) {
	tag := Tag{
		ID:         1,
		Name:       "golang",
		UsageCount: 10,
		CreatedAt:  time.Now(),
	}

	if tag.Name != "golang" {
		t.Errorf("Tag.Name = %v, want golang", tag.Name)
	}
	if tag.UsageCount != 10 {
		t.Errorf("Tag.UsageCount = %v, want 10", tag.UsageCount)
	}
}

func TestFollowRuleModel(t *testing.T) {
	rule := FollowRule{
		ID:                  1,
		Name:               "AI News",
		Description:        "Follow AI related news",
		Keywords:           "AI,GPT,LLM,machine learning",
		SimilarityThreshold: 0.75,
		IsActive:           true,
		EnablePush:         true,
		PushChannels:       "web,gotify",
		Embedding:          []byte{0x01, 0x02},
		CreatedAt:          time.Now(),
	}

	if rule.Name != "AI News" {
		t.Errorf("FollowRule.Name = %v, want AI News", rule.Name)
	}
	if rule.SimilarityThreshold != 0.75 {
		t.Errorf("FollowRule.SimilarityThreshold = %v, want 0.75", rule.SimilarityThreshold)
	}
	if !rule.EnablePush {
		t.Error("FollowRule.EnablePush should be true")
	}
}

func TestReportModel(t *testing.T) {
	report := Report{
		ID:           1,
		Name:         "Morning Report",
		Type:         "morning",
		ScheduleTime: "08:00",
		Channels:     "web,email",
		IsActive:     true,
	}

	if report.Name != "Morning Report" {
		t.Errorf("Report.Name = %v, want Morning Report", report.Name)
	}
	if report.Type != "morning" {
		t.Errorf("Report.Type = %v, want morning", report.Type)
	}
	if !report.IsActive {
		t.Error("Report.IsActive should be true")
	}
}

func TestNotificationModel(t *testing.T) {
	reportID := int64(1)
	notification := Notification{
		ID:        1,
		ReportID:  &reportID,
		Channel:   "web",
		Content:   "Test notification",
		SentAt:    time.Now(),
		Status:    "pending",
	}

	if notification.Channel != "web" {
		t.Errorf("Notification.Channel = %v, want web", notification.Channel)
	}
	if notification.Status != "pending" {
		t.Errorf("Notification.Status = %v, want pending", notification.Status)
	}
}

func TestArticleTagModel(t *testing.T) {
	articleTag := ArticleTag{
		ArticleID: 1,
		TagID:     2,
	}

	if articleTag.ArticleID != 1 {
		t.Errorf("ArticleTag.ArticleID = %v, want 1", articleTag.ArticleID)
	}
	if articleTag.TagID != 2 {
		t.Errorf("ArticleTag.TagID = %v, want 2", articleTag.TagID)
	}
}

// Helper function
func ptrInt(i int) *int {
	return &i
}
