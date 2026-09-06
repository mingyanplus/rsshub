package database

import (
	"os"
	"testing"

	"rss-ai/internal/models"
)

func TestInitializeDatabase(t *testing.T) {
	// 创建临时数据库
	tmpfile, err := os.CreateTemp("", "rss_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	db, err := New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer db.Close()

	// 验证表是否创建
	tables := []string{"feeds", "articles", "categories", "tags", "article_tags", "follow_rules", "reports", "notifications", "read_logs", "exposures", "interest_clusters"}
	for _, table := range tables {
		var exists int
		err := db.db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists)
		if err != nil {
			t.Errorf("Failed to check table %s: %v", table, err)
		}
		if exists != 1 {
			t.Errorf("Table %s should exist", table)
		}
	}
}

func TestFeedCRUD(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "rss_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	db, err := New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create
	feed := &models.Feed{
		Title:         "Test Feed",
		URL:           "https://example.com/feed.xml",
		Description:   "A test feed",
		FetchInterval: 30,
		IsActive:      true,
	}
	id, err := db.CreateFeed(feed)
	if err != nil {
		t.Fatalf("CreateFeed() error = %v", err)
	}
	if id != 1 {
		t.Errorf("CreateFeed() id = %v, want 1", id)
	}

	// Read
	got, err := db.GetFeedByID(id)
	if err != nil {
		t.Fatalf("GetFeedByID() error = %v", err)
	}
	if got.Title != "Test Feed" {
		t.Errorf("GetFeedByID() Title = %v, want Test Feed", got.Title)
	}

	// Update
	feed.ID = id
	feed.Title = "Updated Feed"
	err = db.UpdateFeed(feed)
	if err != nil {
		t.Fatalf("UpdateFeed() error = %v", err)
	}

	got, _ = db.GetFeedByID(id)
	if got.Title != "Updated Feed" {
		t.Errorf("UpdateFeed() Title = %v, want Updated Feed", got.Title)
	}

	// Delete
	err = db.DeleteFeed(id)
	if err != nil {
		t.Fatalf("DeleteFeed() error = %v", err)
	}

	_, err = db.GetFeedByID(id)
	if err == nil {
		t.Error("GetFeedByID() should return error for deleted feed")
	}
}

func TestArticleCRUD(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "rss_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	db, err := New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 先创建 Feed
	feed := &models.Feed{
		Title:         "Test Feed",
		URL:           "https://example.com/feed.xml",
		FetchInterval: 30,
		IsActive:      true,
	}
	feedID, _ := db.CreateFeed(feed)

	// Create Article
	article := &models.Article{
		FeedID:  feedID,
		Title:   "Test Article",
		Link:    "https://example.com/article",
		Content: "Full content",
		Summary: "Article summary",
	}
	id, err := db.CreateArticle(article)
	if err != nil {
		t.Fatalf("CreateArticle() error = %v", err)
	}

	// Read
	got, err := db.GetArticleByID(id)
	if err != nil {
		t.Fatalf("GetArticleByID() error = %v", err)
	}
	if got.Title != "Test Article" {
		t.Errorf("GetArticleByID() Title = %v, want Test Article", got.Title)
	}

	// List by Feed
	articles, err := db.ListArticlesByFeed(feedID, 10, 0)
	if err != nil {
		t.Fatalf("ListArticlesByFeed() error = %v", err)
	}
	if len(articles) != 1 {
		t.Errorf("ListArticlesByFeed() len = %v, want 1", len(articles))
	}
}

func TestCategoryCRUD(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "rss_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	db, err := New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create
	category := &models.Category{
		Name:        "Technology",
		Description: "Tech news",
		Color:       "#3498db",
	}
	id, err := db.CreateCategory(category)
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}

	// Read
	got, err := db.GetCategoryByID(id)
	if err != nil {
		t.Fatalf("GetCategoryByID() error = %v", err)
	}
	if got.Name != "Technology" {
		t.Errorf("GetCategoryByID() Name = %v, want Technology", got.Name)
	}

	// List all
	categories, err := db.ListCategories()
	if err != nil {
		t.Fatalf("ListCategories() error = %v", err)
	}
	if len(categories) != 1 {
		t.Errorf("ListCategories() len = %v, want 1", len(categories))
	}
}

func TestTagCRUD(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "rss_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	db, err := New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create
	tag := &models.Tag{
		Name: "golang",
	}
	id, err := db.CreateTag(tag)
	if err != nil {
		t.Fatalf("CreateTag() error = %v", err)
	}

	// Read
	got, err := db.GetTagByID(id)
	if err != nil {
		t.Fatalf("GetTagByID() error = %v", err)
	}
	if got.Name != "golang" {
		t.Errorf("GetTagByID() Name = %v, want golang", got.Name)
	}

	// Increment usage
	err = db.IncrementTagUsage(id)
	if err != nil {
		t.Fatalf("IncrementTagUsage() error = %v", err)
	}

	got, _ = db.GetTagByID(id)
	if got.UsageCount != 1 {
		t.Errorf("IncrementTagUsage() UsageCount = %v, want 1", got.UsageCount)
	}
}

func TestArticleTagRelation(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "rss_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	db, err := New(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 创建 Feed 和 Article
	feed := &models.Feed{Title: "Test", URL: "https://test.com", IsActive: true}
	feedID, _ := db.CreateFeed(feed)

	article := &models.Article{FeedID: feedID, Title: "Test", Link: "https://test.com/a"}
	articleID, _ := db.CreateArticle(article)

	// 创建 Tag
	tag := &models.Tag{Name: "tech"}
	tagID, _ := db.CreateTag(tag)

	// 添加关联
	err = db.AddArticleTag(articleID, tagID)
	if err != nil {
		t.Fatalf("AddArticleTag() error = %v", err)
	}

	// 验证关联
	tags, err := db.GetArticleTags(articleID)
	if err != nil {
		t.Fatalf("GetArticleTags() error = %v", err)
	}
	if len(tags) != 1 {
		t.Errorf("GetArticleTags() len = %v, want 1", len(tags))
	}
	if tags[0].Name != "tech" {
		t.Errorf("GetArticleTags()[0].Name = %v, want tech", tags[0].Name)
	}
}

func TestBehaviorLogging(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "rss_test*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Close()

	db, err := New(tmpfile.Name())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer db.Close()

	// 建 Feed 和文章作为外键目标
	feed := &models.Feed{Title: "Test", URL: "https://test.com", IsActive: true}
	feedID, _ := db.CreateFeed(feed)
	res, err := db.Exec(`INSERT INTO articles (feed_id, title, link, content, fetched_at) VALUES (?, 't', 'https://example.com/a', 'c', CURRENT_TIMESTAMP)`, feedID)
	if err != nil {
		t.Fatal(err)
	}
	articleID, _ := res.LastInsertId()

	// 阅读行为日志
	if err := db.InsertReadLog(articleID, "read_complete", 0.95, 120000); err != nil {
		t.Fatalf("InsertReadLog() error = %v", err)
	}
	var action string
	var progress float64
	if err := db.db.QueryRow(`SELECT action, progress FROM read_logs WHERE article_id = ?`, articleID).Scan(&action, &progress); err != nil {
		t.Fatalf("查询 read_logs 失败: %v", err)
	}
	if action != "read_complete" || progress != 0.95 {
		t.Errorf("read_logs 记录 = (%v, %v), want (read_complete, 0.95)", action, progress)
	}

	// 曝光批量写入 + 点击标记
	if err := db.InsertExposures([]ExposureRecord{{ArticleID: articleID, Position: 3, Channel: "freshness"}}); err != nil {
		t.Fatalf("InsertExposures() error = %v", err)
	}
	if err := db.MarkExposureClicked(articleID); err != nil {
		t.Fatalf("MarkExposureClicked() error = %v", err)
	}
	var clicked int
	if err := db.db.QueryRow(`SELECT clicked FROM exposures WHERE article_id = ?`, articleID).Scan(&clicked); err != nil {
		t.Fatalf("查询 exposures 失败: %v", err)
	}
	if clicked != 1 {
		t.Errorf("exposures.clicked = %v, want 1", clicked)
	}

	// 收藏/不感兴趣状态
	if err := db.SetArticleFavorite(articleID, true); err != nil {
		t.Fatalf("SetArticleFavorite() error = %v", err)
	}
	if err := db.SetArticleNotInterested(articleID); err != nil {
		t.Fatalf("SetArticleNotInterested() error = %v", err)
	}
	article, err := db.GetArticleByID(articleID)
	if err != nil {
		t.Fatalf("GetArticleByID() error = %v", err)
	}
	if !article.IsFavorite || !article.NotInterested {
		t.Errorf("IsFavorite/NotInterested = (%v, %v), want (true, true)", article.IsFavorite, article.NotInterested)
	}
}
