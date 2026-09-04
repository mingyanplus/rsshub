package database

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"rss-ai/internal/models"
)

// CreateFeed 创建订阅源
func (d *DB) CreateFeed(feed *models.Feed) (int64, error) {
	sourceType := feed.SourceType
	if sourceType == "" {
		sourceType = "rss"
	}
	sourceConfig := feed.SourceConfig
	if sourceConfig == "" {
		sourceConfig = "{}"
	}
	result, err := d.db.Exec(`
		INSERT INTO feeds (title, url, description, category_id, fetch_interval, is_active, source_type, source_config, content_filter, authority, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, feed.Title, feed.URL, feed.Description, feed.CategoryID, feed.FetchInterval, feed.IsActive, sourceType, sourceConfig, feed.ContentFilter, feed.NormalizeAuthority(), time.Now())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ListFeeds 列出所有订阅源
func (d *DB) ListFeeds() ([]*models.Feed, error) {
	rows, err := d.db.Query(`
		SELECT id, title, url, description, category_id, last_fetched_at, fetch_interval, is_active, source_type, source_config, content_filter, authority, created_at
		FROM feeds ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []*models.Feed
	for rows.Next() {
		feed := &models.Feed{}
		err := rows.Scan(&feed.ID, &feed.Title, &feed.URL, &feed.Description,
			&feed.CategoryID, &feed.LastFetchedAt, &feed.FetchInterval, &feed.IsActive, &feed.SourceType, &feed.SourceConfig, &feed.ContentFilter, &feed.Authority, &feed.CreatedAt)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, nil
}

// GetFeedByID 根据 ID 获取订阅源
func (d *DB) GetFeedByID(id int64) (*models.Feed, error) {
	feed := &models.Feed{}
	err := d.db.QueryRow(`
		SELECT id, title, url, description, category_id, last_fetched_at, fetch_interval, is_active, source_type, source_config, content_filter, authority, created_at
		FROM feeds WHERE id = ?
	`, id).Scan(&feed.ID, &feed.Title, &feed.URL, &feed.Description, &feed.CategoryID,
		&feed.LastFetchedAt, &feed.FetchInterval, &feed.IsActive, &feed.SourceType, &feed.SourceConfig, &feed.ContentFilter, &feed.Authority, &feed.CreatedAt)
	if err != nil {
		return nil, err
	}
	return feed, nil
}

// GetFeedByURL 根据 URL 获取订阅源
func (d *DB) GetFeedByURL(url string) (*models.Feed, error) {
	feed := &models.Feed{}
	err := d.db.QueryRow(`
		SELECT id, title, url, description, category_id, last_fetched_at, fetch_interval, is_active, source_type, source_config, created_at
		FROM feeds WHERE url = ?
	`, url).Scan(&feed.ID, &feed.Title, &feed.URL, &feed.Description, &feed.CategoryID,
		&feed.LastFetchedAt, &feed.FetchInterval, &feed.IsActive, &feed.SourceType, &feed.SourceConfig, &feed.CreatedAt)
	if err != nil {
		return nil, err
	}
	return feed, nil
}

// UpdateFeed 更新订阅源
func (d *DB) UpdateFeed(feed *models.Feed) error {
	_, err := d.db.Exec(`
		UPDATE feeds SET title = ?, url = ?, description = ?, category_id = ?, fetch_interval = ?, is_active = ?, source_type = ?, source_config = ?, content_filter = ?, authority = ?
		WHERE id = ?
	`, feed.Title, feed.URL, feed.Description, feed.CategoryID, feed.FetchInterval, feed.IsActive, feed.SourceType, feed.SourceConfig, feed.ContentFilter, feed.NormalizeAuthority(), feed.ID)
	return err
}

// DeleteFeed 删除订阅源
func (d *DB) DeleteFeed(id int64) error {
	_, err := d.db.Exec(`DELETE FROM feeds WHERE id = ?`, id)
	return err
}

// UpdateFeedLastFetched 更新订阅源的最后抓取时间
func (d *DB) UpdateFeedLastFetched(id int64) error {
	_, err := d.db.Exec(`UPDATE feeds SET last_fetched_at = ? WHERE id = ?`, time.Now(), id)
	return err
}

// CreateArticle 创建文章
func (d *DB) CreateArticle(article *models.Article) (int64, error) {
	// 清理HTML用于列表显示（存入ai_summary）
	cleanSummary := stripHTML(article.Summary)
	cleanContent := stripHTML(article.Content)

	// 如果没有发布时间，使用当前时间
	publishedAt := time.Now()
	if article.PublishedAt != nil {
		publishedAt = *article.PublishedAt
	}

	result, err := d.db.Exec(`
		INSERT INTO articles (feed_id, category_id, title, link, content, summary, ai_summary, content_cleaned, published_at, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, article.FeedID, article.CategoryID, article.Title, article.Link, article.Content, article.Summary, cleanSummary, cleanContent, publishedAt, time.Now())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetArticleByID 根据 ID 获取文章
func (d *DB) GetArticleByID(id int64) (*models.Article, error) {
	article := &models.Article{}
	var contentCleaned, summary, aiSummary, keywords, tagsCache, adReason, entities sql.NullString
	err := d.db.QueryRow(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, ai_summary, keywords, tags_cache, entities, is_ad, ad_reason, published_at, fetched_at
		FROM articles WHERE id = ?
	`, id).Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
		&article.Content, &contentCleaned, &summary, &aiSummary, &keywords,
		&tagsCache, &entities, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt)
	if err != nil {
		return nil, err
	}
	article.ContentCleaned = contentCleaned.String
	article.Summary = summary.String
	article.AISummary = aiSummary.String
	article.Keywords = keywords.String
	article.TagsCache = tagsCache.String
	article.Entities = entities.String
	article.AdReason = adReason.String
	return article, nil
}

// ListArticlesByFeed 根据订阅源 ID 列出文章
func (d *DB) ListArticlesByFeed(feedID int64, limit, offset int) ([]*models.Article, error) {
	rows, err := d.db.Query(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, ai_summary, keywords, tags_cache, is_ad, ad_reason, published_at, fetched_at
		FROM articles WHERE feed_id = ? ORDER BY published_at DESC LIMIT ? OFFSET ?
	`, feedID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, aiSummary, keywords, tagsCache, adReason sql.NullString
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &aiSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt)
		if err != nil {
			return nil, err
		}
		article.ContentCleaned = contentCleaned.String
		article.Summary = summary.String
		article.AISummary = aiSummary.String
		article.Keywords = keywords.String
		article.TagsCache = tagsCache.String
		article.AdReason = adReason.String
		articles = append(articles, article)
	}
	return articles, nil
}

// CreateCategory 创建分类
func (d *DB) CreateCategory(category *models.Category) (int64, error) {
	if category.ContentType != "blog" {
		category.ContentType = "news"
	}
	result, err := d.db.Exec(`
		INSERT INTO categories (name, description, color, content_type, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, category.Name, category.Description, category.Color, category.ContentType, time.Now())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetCategoryByID 根据 ID 获取分类
func (d *DB) GetCategoryByID(id int64) (*models.Category, error) {
	category := &models.Category{}
	err := d.db.QueryRow(`
		SELECT id, name, description, color, content_type, created_at
		FROM categories WHERE id = ?
	`, id).Scan(&category.ID, &category.Name, &category.Description, &category.Color, &category.ContentType, &category.CreatedAt)
	if err != nil {
		return nil, err
	}
	return category, nil
}

// GetCategoryByName 根据名称获取分类
func (d *DB) GetCategoryByName(name string) (*models.Category, error) {
	category := &models.Category{}
	err := d.db.QueryRow(`
		SELECT id, name, description, color, content_type, created_at
		FROM categories WHERE name = ?
	`, name).Scan(&category.ID, &category.Name, &category.Description, &category.Color, &category.ContentType, &category.CreatedAt)
	if err != nil {
		return nil, err
	}
	return category, nil
}

// ListCategories 列出所有分类
func (d *DB) ListCategories() ([]*models.Category, error) {
	rows, err := d.db.Query(`SELECT id, name, description, color, content_type, created_at FROM categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []*models.Category
	for rows.Next() {
		category := &models.Category{}
		err := rows.Scan(&category.ID, &category.Name, &category.Description, &category.Color, &category.ContentType, &category.CreatedAt)
		if err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	return categories, nil
}

// CreateTag 创建标签
func (d *DB) CreateTag(tag *models.Tag) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO tags (name, color, usage_count, created_at)
		VALUES (?, ?, ?, ?)
	`, tag.Name, tag.Color, tag.UsageCount, time.Now())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetTagByID 根据 ID 获取标签
func (d *DB) GetTagByID(id int64) (*models.Tag, error) {
	tag := &models.Tag{}
	var color sql.NullString
	err := d.db.QueryRow(`
		SELECT id, name, color, usage_count, created_at
		FROM tags WHERE id = ?
	`, id).Scan(&tag.ID, &tag.Name, &color, &tag.UsageCount, &tag.CreatedAt)
	if err != nil {
		return nil, err
	}
	if color.Valid {
		tag.Color = color.String
	} else {
		tag.Color = "#3b82f6"
	}
	return tag, nil
}

// GetTagByName 根据名称获取标签
func (d *DB) GetTagByName(name string) (*models.Tag, error) {
	tag := &models.Tag{}
	var color sql.NullString
	err := d.db.QueryRow(`
		SELECT id, name, color, usage_count, created_at
		FROM tags WHERE name = ?
	`, name).Scan(&tag.ID, &tag.Name, &color, &tag.UsageCount, &tag.CreatedAt)
	if err != nil {
		return nil, err
	}
	if color.Valid {
		tag.Color = color.String
	} else {
		tag.Color = "#3b82f6"
	}
	return tag, nil
}

// IncrementTagUsage 增加标签使用次数
func (d *DB) IncrementTagUsage(id int64) error {
	_, err := d.db.Exec(`UPDATE tags SET usage_count = usage_count + 1 WHERE id = ?`, id)
	return err
}

// AddArticleTag 添加文章-标签关联
func (d *DB) AddArticleTag(articleID, tagID int64) error {
	_, err := d.db.Exec(`INSERT OR IGNORE INTO article_tags (article_id, tag_id) VALUES (?, ?)`, articleID, tagID)
	return err
}

// GetArticleTags 获取文章的标签
func (d *DB) GetArticleTags(articleID int64) ([]*models.Tag, error) {
	rows, err := d.db.Query(`
		SELECT t.id, t.name, t.color, t.usage_count, t.created_at
		FROM tags t
		INNER JOIN article_tags at ON t.id = at.tag_id
		WHERE at.article_id = ?
	`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*models.Tag
	for rows.Next() {
		tag := &models.Tag{}
		var color sql.NullString
		err := rows.Scan(&tag.ID, &tag.Name, &color, &tag.UsageCount, &tag.CreatedAt)
		if err != nil {
			return nil, err
		}
		if color.Valid {
			tag.Color = color.String
		} else {
			tag.Color = "#3b82f6"
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// ArticleExists 检查文章是否已存在
func (d *DB) ArticleExists(link string) (bool, error) {
	var exists bool
	err := d.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM articles WHERE link = ?)`, link).Scan(&exists)
	return exists, err
}

// GetUnprocessedArticles 获取未处理的文章（无关键词表示未被 LLM 处理过）
func (d *DB) GetUnprocessedArticles(limit int) ([]*models.Article, error) {
	rows, err := d.db.Query(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, keywords, tags_cache, is_ad, ad_reason, published_at, fetched_at
		FROM articles
		WHERE (keywords IS NULL OR keywords = '')
		  AND (process_attempts IS NULL OR process_attempts < 3)
		ORDER BY fetched_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, keywords, tagsCache, adReason sql.NullString
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt)
		if err != nil {
			return nil, err
		}
		if contentCleaned.Valid {
			article.ContentCleaned = contentCleaned.String
		}
		if summary.Valid {
			article.Summary = summary.String
		}
		if keywords.Valid {
			article.Keywords = keywords.String
		}
		if tagsCache.Valid {
			article.TagsCache = tagsCache.String
		}
		if adReason.Valid {
			article.AdReason = adReason.String
		}
		articles = append(articles, article)
	}
	return articles, nil
}

// ArticleQueryType 文章查询类型
type ArticleQueryType int

const (
	QueryMissingSummaryEmbedding ArticleQueryType = iota // 已处理但缺少 summary_embedding
	QueryMissingOneLineSummary                            // 需要重新 AI 分析
	QueryIncomplete                                        // 处理不完整（缺少 entities/one_line_summary/summary_embedding）
)

// GetArticlesByQueryType 通用文章查询（消除重复代码）
// 使用 "keywords IS NOT NULL AND keywords != '' AND keywords != '[]'" 作为"已处理"的判断标准
func (d *DB) GetArticlesByQueryType(queryType ArticleQueryType, limit int) ([]*models.Article, error) {
	var whereClause string
	switch queryType {
	case QueryMissingSummaryEmbedding:
		whereClause = `(keywords IS NOT NULL AND keywords != '' AND keywords != '[]')
		  AND (one_line_summary IS NOT NULL AND one_line_summary != '')
		  AND (summary_embedding IS NULL OR summary_embedding = '')`
	case QueryMissingOneLineSummary:
		whereClause = `(keywords IS NOT NULL AND keywords != '' AND keywords != '[]')
		  AND (one_line_summary IS NULL OR one_line_summary = '')`
	case QueryIncomplete:
		// 已处理但缺少 entities、one_line_summary 或 summary_embedding 的文章
		// 排除广告文章
		whereClause = `(is_ad = 0 OR is_ad IS NULL)
		  AND (keywords IS NOT NULL AND keywords != '' AND keywords != '[]')
		  AND (
		    (entities IS NULL OR entities = '')
		    OR (one_line_summary IS NULL OR one_line_summary = '')
		    OR (summary_embedding IS NULL OR summary_embedding = '')
		    OR (translated_content IS NULL OR translated_content = '')
		  )`
	default:
		return nil, fmt.Errorf("unknown query type: %d", queryType)
	}

	query := fmt.Sprintf(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, ai_summary, one_line_summary, keywords, tags_cache, is_ad, ad_reason, published_at, fetched_at
		FROM articles
		WHERE %s
		ORDER BY fetched_at DESC
		LIMIT ?
	`, whereClause)

	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return d.scanArticlesWithOneLineSummary(rows)
}

// scanArticlesWithOneLineSummary 扫描包含 one_line_summary 的文章行
func (d *DB) scanArticlesWithOneLineSummary(rows *sql.Rows) ([]*models.Article, error) {
	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, aiSummary, oneLineSummary, keywords, tagsCache, adReason sql.NullString
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &aiSummary, &oneLineSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt)
		if err != nil {
			return nil, err
		}
		if contentCleaned.Valid {
			article.ContentCleaned = contentCleaned.String
		}
		if summary.Valid {
			article.Summary = summary.String
		}
		if aiSummary.Valid {
			article.AISummary = aiSummary.String
		}
		if oneLineSummary.Valid {
			article.OneLineSummary = oneLineSummary.String
		}
		if keywords.Valid {
			article.Keywords = keywords.String
		}
		if tagsCache.Valid {
			article.TagsCache = tagsCache.String
		}
		if adReason.Valid {
			article.AdReason = adReason.String
		}
		articles = append(articles, article)
	}
	return articles, nil
}

// GetArticlesMissingSummaryEmbedding 获取已处理但缺少 summary_embedding 的文章
func (d *DB) GetArticlesMissingSummaryEmbedding(limit int) ([]*models.Article, error) {
	return d.GetArticlesByQueryType(QueryMissingSummaryEmbedding, limit)
}

// GetArticlesMissingOneLineSummary 获取已处理但缺少 one_line_summary 的文章（需要重新AI分析）
func (d *DB) GetArticlesMissingOneLineSummary(limit int) ([]*models.Article, error) {
	return d.GetArticlesByQueryType(QueryMissingOneLineSummary, limit)
}

// GetArticlesIncomplete 获取处理不完整的非广告文章（缺少 entities/one_line_summary/summary_embedding）
func (d *DB) GetArticlesIncomplete(limit int) ([]*models.Article, error) {
	return d.GetArticlesByQueryType(QueryIncomplete, limit)
}

// UpdateArticleAI 更新文章的 AI 分析结果（不修改分类，分类由 RSS feed 决定）
func (d *DB) UpdateArticleAI(params *models.AIUpdateParams) error {
	_, err := d.db.Exec(`
		UPDATE articles
		SET ai_summary = ?, one_line_summary = ?, keywords = ?, tags_cache = ?, is_ad = ?, ad_reason = ?, importance_score = ?, topic_category = ?, entities = ?, translated_content = ?
		WHERE id = ?
	`, params.AISummary, params.OneLineSummary, params.Keywords, params.TagsCache, params.IsAd, params.AdReason, params.ImportanceScore, params.TopicCategory, params.Entities, params.TranslatedContent, params.ID)
	return err
}

// UpdateArticleEmbedding 更新文章的向量
func (d *DB) UpdateArticleEmbedding(id int64, embedding []byte) error {
	_, err := d.db.Exec(`UPDATE articles SET embedding = ? WHERE id = ?`, embedding, id)
	return err
}

// UpdateArticleContent 更新文章正文内容
func (d *DB) UpdateArticleContent(id int64, content string) error {
	_, err := d.db.Exec(`UPDATE articles SET content = ? WHERE id = ?`, content, id)
	return err
}

// UpdateArticleSummaryEmbedding 更新文章的总结向量
func (d *DB) UpdateArticleSummaryEmbedding(id int64, summaryEmbedding []byte) error {
	_, err := d.db.Exec(`UPDATE articles SET summary_embedding = ? WHERE id = ?`, summaryEmbedding, id)
	return err
}

// MaxProcessAttempts 最大处理尝试次数
const MaxProcessAttempts = 3

// IncrementProcessAttempts 递增文章处理失败计数，超过最大次数则标记为已处理
func (d *DB) IncrementProcessAttempts(id int64, errMsg string) {
	_, err := d.db.Exec(`
		UPDATE articles
		SET process_attempts = COALESCE(process_attempts, 0) + 1,
		    process_error = ?
		WHERE id = ?
	`, errMsg, id)
	if err != nil {
		return
	}
	// 检查是否达到最大次数，标记为已处理
	d.db.Exec(`
		UPDATE articles SET keywords = '处理失败'
		WHERE id = ? AND process_attempts >= ? AND (keywords IS NULL OR keywords = '')
	`, id, MaxProcessAttempts)
}

// ResetArticleProcess 重置文章处理状态（手动重试入口）：
// 清零失败计数与错误、清空 keywords 使其重新进入待处理队列
func (d *DB) ResetArticleProcess(id int64) error {
	_, err := d.db.Exec(`
		UPDATE articles
		SET process_attempts = 0,
		    process_error = NULL,
		    keywords = ''
		WHERE id = ?
	`, id)
	return err
}

// ResetFailedArticles 批量重置所有「处理失败」文章（重新进入处理队列），返回重置数量
func (d *DB) ResetFailedArticles() (int64, error) {
	result, err := d.db.Exec(`
		UPDATE articles
		SET process_attempts = 0,
		    process_error = NULL,
		    keywords = ''
		WHERE keywords = '处理失败'
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ListArticles 列出所有文章（分页）
func (d *DB) ListArticles(limit, offset int) ([]*models.Article, error) {
	rows, err := d.db.Query(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, ai_summary, keywords, tags_cache, is_ad, ad_reason, published_at, fetched_at
		FROM articles ORDER BY published_at DESC LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, aiSummary, keywords, tagsCache, adReason sql.NullString
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &aiSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt)
		if err != nil {
			return nil, err
		}
		article.ContentCleaned = contentCleaned.String
		article.Summary = summary.String
		article.AISummary = aiSummary.String
		article.Keywords = keywords.String
		article.TagsCache = tagsCache.String
		article.AdReason = adReason.String
		articles = append(articles, article)
	}
	return articles, nil
}

// CountArticles 统计文章总数
func (d *DB) CountArticles() (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&count)
	return count, err
}

// ArticleQueryParams 文章查询参数
type ArticleQueryParams struct {
	FeedID     *int64
	CategoryID *int64
	TagID      *int64
	Keyword    string
	HideAds    bool
	IsRead     *bool // nil=全部, true=已读, false=未读
	Search     string
	Limit      int
	Offset     int
}

// QueryArticles 条件查询文章
func (d *DB) QueryArticles(params ArticleQueryParams) ([]*models.Article, error) {
	// 构建查询
	query := `SELECT DISTINCT a.id, a.feed_id, a.category_id, a.title, a.link, a.content, a.content_cleaned, a.summary, a.ai_summary, a.keywords, a.tags_cache, a.is_ad, a.ad_reason, a.published_at, a.fetched_at, a.is_read
		FROM articles a`
	args := []interface{}{}

	// 如果按标签筛选，需要 JOIN article_tags 表
	if params.TagID != nil {
		query += " JOIN article_tags at ON a.id = at.article_id"
	}

	query += " WHERE 1=1"

	if params.FeedID != nil {
		query += " AND a.feed_id = ?"
		args = append(args, *params.FeedID)
	}
	if params.CategoryID != nil {
		query += " AND a.category_id = ?"
		args = append(args, *params.CategoryID)
	}
	if params.TagID != nil {
		query += " AND at.tag_id = ?"
		args = append(args, *params.TagID)
	}
	if params.HideAds {
		query += " AND a.is_ad = ?"
		args = append(args, false)
	}
	if params.IsRead != nil {
		query += " AND a.is_read = ?"
		args = append(args, *params.IsRead)
	}
	if params.Keyword != "" {
		query += " AND a.keywords LIKE ?"
		args = append(args, "%"+params.Keyword+"%")
	}
	if params.Search != "" {
		query += " AND (a.title LIKE ? OR a.content LIKE ? OR a.summary LIKE ?)"
		searchPattern := "%" + params.Search + "%"
		args = append(args, searchPattern, searchPattern, searchPattern)
	}

	query += " ORDER BY a.published_at DESC LIMIT ? OFFSET ?"
	args = append(args, params.Limit, params.Offset)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, aiSummary, keywords, tagsCache, adReason sql.NullString
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &aiSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt, &article.IsRead)
		if err != nil {
			return nil, err
		}
		article.ContentCleaned = contentCleaned.String
		article.Summary = summary.String
		article.AISummary = aiSummary.String
		article.Keywords = keywords.String
		article.TagsCache = tagsCache.String
		article.AdReason = adReason.String
		articles = append(articles, article)
	}
	return articles, nil
}

// MarkArticleRead 标记文章为已读
func (d *DB) MarkArticleRead(id int64) error {
	_, err := d.db.Exec(`UPDATE articles SET is_read = TRUE WHERE id = ?`, id)
	return err
}

// ListTags 列出所有标签（排除系统内部标签）
func (d *DB) ListTags() ([]*models.Tag, error) {
	rows, err := d.db.Query(`SELECT id, name, color, usage_count, created_at FROM tags WHERE name != '已标记' ORDER BY usage_count DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*models.Tag
	for rows.Next() {
		tag := &models.Tag{}
		var color sql.NullString
		err := rows.Scan(&tag.ID, &tag.Name, &color, &tag.UsageCount, &tag.CreatedAt)
		if err != nil {
			return nil, err
		}
		if color.Valid {
			tag.Color = color.String
		} else {
			tag.Color = "#3b82f6"
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// UpdateTag 更新标签
func (d *DB) UpdateTag(tag *models.Tag) error {
	_, err := d.db.Exec(`
		UPDATE tags SET name = ?, color = ? WHERE id = ?
	`, tag.Name, tag.Color, tag.ID)
	return err
}

// UpdateCategory 更新分类
func (d *DB) UpdateCategory(category *models.Category) error {
	if category.ContentType != "blog" {
		category.ContentType = "news"
	}
	_, err := d.db.Exec(`
		UPDATE categories SET name = ?, description = ?, color = ?, content_type = ? WHERE id = ?
	`, category.Name, category.Description, category.Color, category.ContentType, category.ID)
	return err
}

// DeleteCategory 删除分类
func (d *DB) DeleteCategory(id int64) error {
	_, err := d.db.Exec(`DELETE FROM categories WHERE id = ?`, id)
	return err
}

// GetCategoryStats 获取分类的订阅数和文章数
func (d *DB) GetCategoryStats(categoryID int64) (feedCount, articleCount int, err error) {
	// 统计该分类下的订阅数
	err = d.db.QueryRow(`SELECT COUNT(*) FROM feeds WHERE category_id = ?`, categoryID).Scan(&feedCount)
	if err != nil {
		return 0, 0, err
	}

	// 统计该分类下的文章数
	err = d.db.QueryRow(`SELECT COUNT(*) FROM articles WHERE category_id = ?`, categoryID).Scan(&articleCount)
	if err != nil {
		return feedCount, 0, err
	}

	return feedCount, articleCount, nil
}

// DeleteTag 删除标签
func (d *DB) DeleteTag(id int64) error {
	// 先删除关联记录
	d.db.Exec(`DELETE FROM article_tags WHERE tag_id = ?`, id)
	// 再删除标签
	_, err := d.db.Exec(`DELETE FROM tags WHERE id = ?`, id)
	return err
}

// CreateFollowRule 创建关注规则
func (d *DB) CreateFollowRule(rule *models.FollowRule) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO follow_rules (name, description, keywords, similarity_threshold, is_active, enable_push, push_channels, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, rule.Name, rule.Description, rule.Keywords, rule.SimilarityThreshold, rule.IsActive, rule.EnablePush, rule.PushChannels, time.Now())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetFollowRuleByID 根据 ID 获取关注规则
func (d *DB) GetFollowRuleByID(id int64) (*models.FollowRule, error) {
	rule := &models.FollowRule{}
	err := d.db.QueryRow(`
		SELECT id, name, description, keywords, similarity_threshold, is_active, enable_push, push_channels, created_at
		FROM follow_rules WHERE id = ?
	`, id).Scan(&rule.ID, &rule.Name, &rule.Description, &rule.Keywords, &rule.SimilarityThreshold,
		&rule.IsActive, &rule.EnablePush, &rule.PushChannels, &rule.CreatedAt)
	if err != nil {
		return nil, err
	}
	return rule, nil
}

// GetFollowRuleByName 根据名称获取关注规则
func (d *DB) GetFollowRuleByName(name string) (*models.FollowRule, error) {
	rule := &models.FollowRule{}
	err := d.db.QueryRow(`
		SELECT id, name, description, keywords, similarity_threshold, is_active, enable_push, push_channels, created_at
		FROM follow_rules WHERE name = ?
	`, name).Scan(&rule.ID, &rule.Name, &rule.Description, &rule.Keywords, &rule.SimilarityThreshold,
		&rule.IsActive, &rule.EnablePush, &rule.PushChannels, &rule.CreatedAt)
	if err != nil {
		return nil, err
	}
	return rule, nil
}

// ListFollowRules 列出所有关注规则
func (d *DB) ListFollowRules() ([]*models.FollowRule, error) {
	rows, err := d.db.Query(`
		SELECT id, name, description, keywords, similarity_threshold, is_active, enable_push, push_channels, created_at
		FROM follow_rules ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*models.FollowRule
	for rows.Next() {
		rule := &models.FollowRule{}
		err := rows.Scan(&rule.ID, &rule.Name, &rule.Description, &rule.Keywords, &rule.SimilarityThreshold,
			&rule.IsActive, &rule.EnablePush, &rule.PushChannels, &rule.CreatedAt)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// UpdateFollowRule 更新关注规则
func (d *DB) UpdateFollowRule(rule *models.FollowRule) error {
	_, err := d.db.Exec(`
		UPDATE follow_rules SET name = ?, description = ?, keywords = ?, similarity_threshold = ?, is_active = ?, enable_push = ?, push_channels = ?
		WHERE id = ?
	`, rule.Name, rule.Description, rule.Keywords, rule.SimilarityThreshold, rule.IsActive, rule.EnablePush, rule.PushChannels, rule.ID)
	return err
}

// DeleteFollowRule 删除关注规则
func (d *DB) DeleteFollowRule(id int64) error {
	_, err := d.db.Exec(`DELETE FROM follow_rules WHERE id = ?`, id)
	return err
}

// CreateReport 创建报告
func (d *DB) CreateReport(report *models.Report) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO reports (name, type, schedule_time, channels, is_active)
		VALUES (?, ?, ?, ?, ?)
	`, report.Name, report.Type, report.ScheduleTime, report.Channels, report.IsActive)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetReportByID 根据 ID 获取报告
func (d *DB) GetReportByID(id int64) (*models.Report, error) {
	report := &models.Report{}
	err := d.db.QueryRow(`
		SELECT id, name, type, schedule_time, channels, is_active
		FROM reports WHERE id = ?
	`, id).Scan(&report.ID, &report.Name, &report.Type, &report.ScheduleTime, &report.Channels, &report.IsActive)
	if err != nil {
		return nil, err
	}
	return report, nil
}

// ListReports 列出所有报告
func (d *DB) ListReports() ([]*models.Report, error) {
	rows, err := d.db.Query(`
		SELECT id, name, type, summary, article_count, is_active, created_at, sent_at
		FROM reports ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []*models.Report
	for rows.Next() {
		report := &models.Report{}
		var summary sql.NullString
		var createdAt sql.NullTime
		var sentAt sql.NullTime
		err := rows.Scan(&report.ID, &report.Name, &report.Type, &summary, &report.ArticleCount, &report.IsActive, &createdAt, &sentAt)
		if err != nil {
			return nil, err
		}
		if summary.Valid {
			report.Summary = summary.String
		}
		if createdAt.Valid {
			report.CreatedAt = createdAt.Time
		}
		if sentAt.Valid {
			report.SentAt = &sentAt.Time
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// UpdateReport 更新报告
func (d *DB) UpdateReport(report *models.Report) error {
	_, err := d.db.Exec(`
		UPDATE reports SET name = ?, type = ?, schedule_time = ?, channels = ?, is_active = ? WHERE id = ?
	`, report.Name, report.Type, report.ScheduleTime, report.Channels, report.IsActive, report.ID)
	return err
}

// DeleteReport 删除报告
func (d *DB) DeleteReport(id int64) error {
	_, err := d.db.Exec(`DELETE FROM reports WHERE id = ?`, id)
	return err
}

// GetStats 获取统计数据
func (d *DB) GetStats() (map[string]int, error) {
	stats := make(map[string]int)

	var val int

	// 今日文章数
	d.db.QueryRow(`SELECT COUNT(*) FROM articles WHERE fetched_at >= date('now')`).Scan(&val)
	stats["today_articles"] = val

	// 活跃订阅数
	d.db.QueryRow(`SELECT COUNT(*) FROM feeds WHERE is_active = 1`).Scan(&val)
	stats["active_feeds"] = val

	// 总订阅数
	d.db.QueryRow(`SELECT COUNT(*) FROM feeds`).Scan(&val)
	stats["total_feeds"] = val

	// 待处理文章数
	d.db.QueryRow(`SELECT COUNT(*) FROM articles WHERE keywords IS NULL OR keywords = '[]' OR keywords = ''`).Scan(&val)
	stats["pending_articles"] = val

	// 广告文章数
	d.db.QueryRow(`SELECT COUNT(*) FROM articles WHERE is_ad = 1`).Scan(&val)
	stats["ad_articles"] = val

	// 总文章数
	d.db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&val)
	stats["total_articles"] = val

	return stats, nil
}

// HotTag 热门标签
type HotTag struct {
	ID          int64
	Name        string
	Color       string
	TodayCount  int
	TotalCount  int
}

// GetTodayHotTags 获取最近热门标签（按出现次数排序）
func (d *DB) GetTodayHotTags(limit int) ([]*HotTag, error) {
	// 查询最近 7 天文章的 keywords 字段
	rows, err := d.db.Query(`
		SELECT keywords
		FROM articles
		WHERE fetched_at >= datetime('now', '-7 days')
		  AND keywords IS NOT NULL
		  AND keywords != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 统计关键词频率（排除系统内部标记）
	freq := make(map[string]int)
	for rows.Next() {
		var keywords string
		if err := rows.Scan(&keywords); err != nil {
			continue
		}
		// 解析逗号分隔的关键词
		for _, kw := range strings.Split(keywords, ",") {
			kw = strings.TrimSpace(kw)
			// 跳过空值和系统内部标记
			if kw != "" && kw != "已标记" {
				freq[kw]++
			}
		}
	}

	// 转换为切片并排序
	type kv struct {
		Key   string
		Value int
	}
	var sorted []kv
	for k, v := range freq {
		sorted = append(sorted, kv{k, v})
	}
	// 按频率降序排序
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Value > sorted[i].Value {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// 返回前 limit 个
	var tags []*HotTag
	for i := 0; i < len(sorted) && i < limit; i++ {
		tags = append(tags, &HotTag{
			Name:       sorted[i].Key,
			TodayCount: sorted[i].Value,
			TotalCount: sorted[i].Value,
			Color:      "#3b82f6",
		})
	}

	return tags, nil
}

// ListTagsPaginated 分页获取标签（排除系统内部标签）
func (d *DB) ListTagsPaginated(limit, offset int, sortBy string) ([]*models.Tag, error) {
	var orderBy string
	switch sortBy {
	case "name":
		orderBy = "name ASC"
	case "recent":
		orderBy = "created_at DESC"
	default:
		orderBy = "usage_count DESC"
	}

	query := `SELECT id, name, color, usage_count, created_at FROM tags WHERE name != '已标记' ORDER BY ` + orderBy + ` LIMIT ? OFFSET ?`
	rows, err := d.db.Query(query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []*models.Tag
	for rows.Next() {
		tag := &models.Tag{}
		var color sql.NullString
		err := rows.Scan(&tag.ID, &tag.Name, &color, &tag.UsageCount, &tag.CreatedAt)
		if err != nil {
			return nil, err
		}
		if color.Valid {
			tag.Color = color.String
		} else {
			tag.Color = "#3b82f6"
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// CountTags 统计标签总数（排除系统内部标签）
func (d *DB) CountTags() (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE name != '已标记'`).Scan(&count)
	return count, err
}

// SearchTags 搜索标签（排除系统内部标签）
func (d *DB) SearchTags(query string, limit, offset int) ([]*models.Tag, int, error) {
	// 搜索标签
	searchPattern := "%" + query + "%"
	rows, err := d.db.Query(`
		SELECT id, name, color, usage_count, created_at
		FROM tags
		WHERE name LIKE ? AND name != '已标记'
		ORDER BY usage_count DESC
		LIMIT ? OFFSET ?
	`, searchPattern, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var tags []*models.Tag
	for rows.Next() {
		tag := &models.Tag{}
		var color sql.NullString
		err := rows.Scan(&tag.ID, &tag.Name, &color, &tag.UsageCount, &tag.CreatedAt)
		if err != nil {
			return nil, 0, err
		}
		if color.Valid {
			tag.Color = color.String
		} else {
			tag.Color = "#3b82f6"
		}
		tags = append(tags, tag)
	}

	// 获取搜索结果总数（排除系统内部标签）
	var total int
	err = d.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE name LIKE ? AND name != '已标记'`, searchPattern).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	return tags, total, nil
}

// GetArticlesForReport 获取指定时间范围内的文章（用于生成报告）
func (d *DB) GetArticlesForReport(since time.Time) ([]*models.Article, error) {
	rows, err := d.db.Query(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, ai_summary, keywords, tags_cache, is_ad, ad_reason, published_at, fetched_at, importance_score, topic_category, embedding
		FROM articles
		WHERE fetched_at >= ? AND is_ad = 0 AND embedding IS NOT NULL AND embedding != ''
		ORDER BY importance_score DESC, published_at DESC
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, aiSummary, keywords, tagsCache, adReason sql.NullString
		var topicCategory sql.NullString
		var embedding []byte
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &aiSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt,
			&article.ImportanceScore, &topicCategory, &embedding)
		if err != nil {
			return nil, err
		}
		article.ContentCleaned = contentCleaned.String
		article.Summary = summary.String
		article.AISummary = aiSummary.String
		article.Keywords = keywords.String
		article.TagsCache = tagsCache.String
		article.AdReason = adReason.String
		article.Embedding = embedding
		if topicCategory.Valid {
			article.TopicCategory = topicCategory.String
		}
		articles = append(articles, article)
	}
	return articles, nil
}

// GetArticlesForReportBetween 获取指定时间范围内的文章（用于生成报告，带结束时间）
func (d *DB) GetArticlesForReportBetween(startTime, endTime time.Time) ([]*models.Article, error) {
	rows, err := d.db.Query(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, ai_summary, keywords, tags_cache, is_ad, ad_reason, published_at, fetched_at, importance_score, topic_category, embedding
		FROM articles
		WHERE fetched_at >= ? AND fetched_at < ? AND is_ad = 0 AND embedding IS NOT NULL AND embedding != ''
		ORDER BY importance_score DESC, published_at DESC
	`, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, aiSummary, keywords, tagsCache, adReason sql.NullString
		var topicCategory sql.NullString
		var embedding []byte
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &aiSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt,
			&article.ImportanceScore, &topicCategory, &embedding)
		if err != nil {
			return nil, err
		}
		article.ContentCleaned = contentCleaned.String
		article.Summary = summary.String
		article.AISummary = aiSummary.String
		article.Keywords = keywords.String
		article.TagsCache = tagsCache.String
		article.AdReason = adReason.String
		article.Embedding = embedding
		if topicCategory.Valid {
			article.TopicCategory = topicCategory.String
		}
		articles = append(articles, article)
	}
	return articles, nil
}

// SaveReport 保存报告
func (d *DB) SaveReport(report *models.Report) (int64, error) {
	// 设置默认值
	scheduleTime := report.ScheduleTime
	log.Printf("DEBUG: ScheduleTime='%s', len=%d", scheduleTime, len(scheduleTime))
	if scheduleTime == "" {
		scheduleTime = time.Now().Format("15:04")
		log.Printf("DEBUG: Set default scheduleTime='%s'", scheduleTime)
	}
	channels := report.Channels
	if channels == "" {
		channels = "gotify"
	}
	isActive := report.IsActive
	// 默认激活
	if !isActive {
		isActive = true
	}

	log.Printf("DEBUG: Inserting report: name=%s, type=%s, scheduleTime=%s, channels=%s, isActive=%v",
		report.Name, report.Type, scheduleTime, channels, isActive)

	result, err := d.db.Exec(`
		INSERT INTO reports (name, type, schedule_time, channels, is_active, content, summary, article_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, report.Name, report.Type, scheduleTime, channels, isActive, report.Content, report.Summary, report.ArticleCount, time.Now())
	if err != nil {
		log.Printf("SaveReport error: %v", err)
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// SaveReportArticles 保存报告关联的文章
func (d *DB) SaveReportArticles(reportID int64, articleIDs []int64) error {
	for _, articleID := range articleIDs {
		_, err := d.db.Exec(`
			INSERT OR IGNORE INTO report_articles (report_id, article_id)
			VALUES (?, ?)
		`, reportID, articleID)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetReportWithArticles 获取报告详情（包含关联文章）
func (d *DB) GetReportWithArticles(id int64) (*models.Report, []*models.Article, error) {
	report := &models.Report{}
	var content, summary sql.NullString
	var createdAt sql.NullTime
	var sentAt sql.NullTime

	err := d.db.QueryRow(`
		SELECT id, name, type, content, summary, article_count, is_active, created_at, sent_at
		FROM reports WHERE id = ?
	`, id).Scan(&report.ID, &report.Name, &report.Type, &content, &summary,
		&report.ArticleCount, &report.IsActive, &createdAt, &sentAt)
	if err != nil {
		return nil, nil, err
	}

	if content.Valid {
		report.Content = content.String
	}
	if summary.Valid {
		report.Summary = summary.String
	}
	if createdAt.Valid {
		report.CreatedAt = createdAt.Time
	}
	if sentAt.Valid {
		report.SentAt = &sentAt.Time
	}

	// 获取关联文章
	rows, err := d.db.Query(`
		SELECT a.id, a.feed_id, a.category_id, a.title, a.link, a.content, a.content_cleaned, a.summary, a.ai_summary, a.keywords, a.tags_cache, a.is_ad, a.ad_reason, a.published_at, a.fetched_at
		FROM articles a
		INNER JOIN report_articles ra ON a.id = ra.article_id
		WHERE ra.report_id = ?
		ORDER BY a.published_at DESC
	`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, summary, aiSummary, keywords, tagsCache, adReason sql.NullString
		err := rows.Scan(&article.ID, &article.FeedID, &article.CategoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &summary, &aiSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &article.PublishedAt, &article.FetchedAt)
		if err != nil {
			return nil, nil, err
		}
		article.ContentCleaned = contentCleaned.String
		article.Summary = summary.String
		article.AISummary = aiSummary.String
		article.Keywords = keywords.String
		article.TagsCache = tagsCache.String
		article.AdReason = adReason.String
		articles = append(articles, article)
	}

	return report, articles, nil
}

// ListReportsWithFilter 获取报告列表（带过滤）
func (d *DB) ListReportsWithFilter(reportType string, limit, offset int) ([]*models.Report, error) {
	var query string
	var args []interface{}

	if reportType != "" && reportType != "all" {
		query = `
			SELECT id, name, type, content, summary, article_count, is_active, created_at, sent_at
			FROM reports WHERE type = ?
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{reportType, limit, offset}
	} else {
		query = `
			SELECT id, name, type, content, summary, article_count, is_active, created_at, sent_at
			FROM reports
			ORDER BY created_at DESC
			LIMIT ? OFFSET ?
		`
		args = []interface{}{limit, offset}
	}

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reports []*models.Report
	for rows.Next() {
		report := &models.Report{}
		var content, summary sql.NullString
		var createdAt sql.NullTime
		var sentAt sql.NullTime
		err := rows.Scan(&report.ID, &report.Name, &report.Type, &content, &summary,
			&report.ArticleCount, &report.IsActive, &createdAt, &sentAt)
		if err != nil {
			return nil, err
		}
		if content.Valid {
			report.Content = content.String
		}
		if summary.Valid {
			report.Summary = summary.String
		}
		if createdAt.Valid {
			report.CreatedAt = createdAt.Time
		}
		if sentAt.Valid {
			report.SentAt = &sentAt.Time
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// UpdateReportSent 更新报告发送状态
func (d *DB) UpdateReportSent(id int64) error {
	_, err := d.db.Exec(`UPDATE reports SET sent_at = ? WHERE id = ?`, time.Now(), id)
	return err
}

// CreateNotification 创建通知记录
func (d *DB) CreateNotification(reportID int64, channel, content, status string) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO notifications (report_id, channel, content, sent_at, status)
		VALUES (?, ?, ?, ?, ?)
	`, reportID, channel, content, time.Now(), status)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetNotificationsByReportID 获取报告的通知记录
func (d *DB) GetNotificationsByReportID(reportID int64) ([]*models.Notification, error) {
	rows, err := d.db.Query(`
		SELECT id, report_id, channel, content, sent_at, status
		FROM notifications WHERE report_id = ?
		ORDER BY sent_at DESC
	`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notifications []*models.Notification
	for rows.Next() {
		n := &models.Notification{}
		err := rows.Scan(&n.ID, &n.ReportID, &n.Channel, &n.Content, &n.SentAt, &n.Status)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, n)
	}
	return notifications, nil
}

// ========== TopicPrompt CRUD 方法 ==========

// GetTopicPromptByID 根据ID获取主题模板
func (d *DB) GetTopicPromptByID(id int64) (*models.TopicPrompt, error) {
	p := &models.TopicPrompt{}
	var keywords, tags, briefTemplate sql.NullString
	err := d.db.QueryRow(`
		SELECT id, name, keywords, tags, persona, prompt_template, brief_template, usage_count, is_active, created_at, updated_at
		FROM topic_prompts WHERE id = ?
	`, id).Scan(&p.ID, &p.Name, &keywords, &tags, &p.Persona,
		&p.PromptTemplate, &briefTemplate, &p.UsageCount, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.Keywords = keywords.String
	p.Tags = tags.String
	p.BriefTemplate = briefTemplate.String
	return p, nil
}

// CreateTopicPrompt 创建主题模板
func (d *DB) CreateTopicPrompt(prompt *models.TopicPrompt) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO topic_prompts (name, keywords, tags, persona, prompt_template, brief_template, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, prompt.Name, prompt.Keywords, prompt.Tags, prompt.Persona, prompt.PromptTemplate, prompt.BriefTemplate, prompt.IsActive, time.Now(), time.Now())
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// UpdateTopicPrompt 更新主题模板
func (d *DB) UpdateTopicPrompt(prompt *models.TopicPrompt) error {
	_, err := d.db.Exec(`
		UPDATE topic_prompts SET name = ?, keywords = ?, tags = ?, persona = ?, prompt_template = ?, brief_template = ?, is_active = ?, updated_at = ?
		WHERE id = ?
	`, prompt.Name, prompt.Keywords, prompt.Tags, prompt.Persona, prompt.PromptTemplate, prompt.BriefTemplate, prompt.IsActive, time.Now(), prompt.ID)
	return err
}

// DeleteTopicPrompt 删除主题模板
func (d *DB) DeleteTopicPrompt(id int64) error {
	_, err := d.db.Exec(`DELETE FROM topic_prompts WHERE id = ?`, id)
	return err
}

// ListTopicPrompts 列出所有主题模板
func (d *DB) ListTopicPrompts() ([]*models.TopicPrompt, error) {
	rows, err := d.db.Query(`
		SELECT id, name, keywords, tags, persona, prompt_template, brief_template, usage_count, is_active, created_at, updated_at
		FROM topic_prompts ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prompts []*models.TopicPrompt
	for rows.Next() {
		p := &models.TopicPrompt{}
		var keywords, tags, briefTemplate sql.NullString
		err := rows.Scan(&p.ID, &p.Name, &keywords, &tags, &p.Persona,
			&p.PromptTemplate, &briefTemplate, &p.UsageCount, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		p.Keywords = keywords.String
		p.Tags = tags.String
		p.BriefTemplate = briefTemplate.String
		prompts = append(prompts, p)
	}
	return prompts, nil
}

// InitDefaultTopicPrompts 初始化默认主题模板
func (d *DB) InitDefaultTopicPrompts() error {
	// 检查是否已有模板
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM topic_prompts`).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // 已有模板，跳过
	}

	defaultPrompts := []struct {
		name, keywords, tags, persona, promptTemplate, briefTemplate string
	}{
		{
			name:           "科技资讯",
			keywords:       "AI,人工智能,科技,互联网,软件,硬件,芯片,编程,开发者",
			tags:           "人工智能,科技,互联网",
			persona:        "资深科技编辑",
			promptTemplate: TechFeaturedPrompt,
			briefTemplate:  TechBriefPrompt,
		},
		{
			name:           "时事政文",
			keywords:       "政治,政策,外交,国际,政府,会议,法规",
			tags:           "时事,政治,政策",
			persona:        "时事观察专家",
			promptTemplate: DefaultFeaturedPrompt,
			briefTemplate:  DefaultBriefPrompt,
		},
		{
			name:           "财经金融",
			keywords:       "财经,股市,金融,投资,经济,基金,银行",
			tags:           "财经,金融,投资",
			persona:        "财经分析师",
			promptTemplate: DefaultFeaturedPrompt,
			briefTemplate:  DefaultBriefPrompt,
		},
		{
			name:           "产品发布",
			keywords:       "发布,上线,新品,推出,发布,公告,更新",
			tags:           "产品,发布",
			persona:        "产品评测专家",
			promptTemplate: DefaultFeaturedPrompt,
			briefTemplate:  DefaultBriefPrompt,
		},
		{
			name:           "通用资讯",
			keywords:       "",
			tags:           "",
			persona:        "资深新闻编辑",
			promptTemplate: DefaultFeaturedPrompt,
			briefTemplate:  DefaultBriefPrompt,
		},
	}

	for _, p := range defaultPrompts {
		_, err := d.db.Exec(`
			INSERT INTO topic_prompts (name, keywords, tags, persona, prompt_template, brief_template, is_active, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, TRUE, ?, ?)
		`, p.name, p.keywords, p.tags, p.persona, p.promptTemplate, p.briefTemplate, time.Now(), time.Now())
		if err != nil {
			return err
		}
	}
	return nil
}

// HasFollowRuleNotification 检查文章是否已推送过某规则
func (d *DB) HasFollowRuleNotification(ruleID, articleID int64) bool {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM follow_rule_notifications WHERE rule_id = ? AND article_id = ?`, ruleID, articleID).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

// CreateFollowRuleNotification 记录关注规则推送
func (d *DB) CreateFollowRuleNotification(ruleID, articleID int64) error {
	_, err := d.db.Exec(`INSERT OR IGNORE INTO follow_rule_notifications (rule_id, article_id) VALUES (?, ?)`, ruleID, articleID)
	return err
}

// ClearFollowRulePushRecords 清除规则的推送记录
func (d *DB) ClearFollowRulePushRecords(ruleID int64) error {
	_, err := d.db.Exec(`DELETE FROM follow_rule_notifications WHERE rule_id = ?`, ruleID)
	return err
}

// GetActiveFollowRulesWithPush 获取所有启用推送的规则
func (d *DB) GetActiveFollowRulesWithPush() ([]*models.FollowRule, error) {
	rows, err := d.db.Query(`SELECT id, name, description, keywords, push_channels FROM follow_rules WHERE enable_push = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*models.FollowRule
	for rows.Next() {
		rule := &models.FollowRule{EnablePush: true}
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Description, &rule.Keywords, &rule.PushChannels); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// 默认提示词模板常量
const (
	DefaultFeaturedPrompt = `你是资深新闻编辑。

## 素材
{materials}

## 任务
基于以上素材，撰写一篇 500 字以内的深度报道。

## 要求
1. 客观报道事实，引用关键数据和观点
2. 分析事件影响
3. 提炼 3 个关键知识点
4. 末尾加入 1-2 句洞察预测

## 输出格式
{报道内容}

**知识点：**
- 要点1
- 要点2
- 要点3

**来源:** [原文1](url) | [原文2](url)`

	DefaultBriefPrompt = `你是资深新闻编辑。

## 素材
{materials}

## 任务
基于以上素材，撰写一条 100 字以内的精简报道。

## 要求
1. 概括核心事实
2. 末尾加一句简短洞察（用 → 开头)

## 输出格式
{100字报道} → {简短洞察}

**来源:** [原文](url)`

	TechFeaturedPrompt = `你是资深科技编辑，关注技术突破和行业影响。

## 素材
{materials}

## 任务
基于以上素材，撰写一篇 500 字以内的科技深度报道。

## 要求
1. 客观报道技术细节，引用关键数据
2. 分析技术突破点和行业影响
3. 提炼 3 个关键知识点（技术参数、应用场景等）
4. 末尾加入 1-2 句技术趋势洞察

## 输出格式
{报道内容}

**知识点：**
- 要点1
- 要点2
- 要点3

**来源:** [原文1](url) | [原文2](url)`

	TechBriefPrompt = `你是资深科技编辑。

## 素材
{materials}

## 任务
撰写 100 字以内的科技简讯。

## 要求
1. 概括技术核心事实
2. 末尾加一句技术洞察（用 → 开头)

## 输出格式
{100字简讯} → {技术洞察}

**来源:** [原文](url)`
)

// stripHTML 移除HTML标签，只保留纯文本
func stripHTML(html string) string {
	if html == "" {
		return ""
	}

	// 使用 goquery 解析 HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		// 如果解析失败，使用正则表达式简单清理
		return stripHTMLSimple(html)
	}

	// 获取纯文本内容
	text := doc.Text()

	// 清理多余的空白字符
	text = cleanWhitespace(text)

	return text
}

// stripHTMLSimple 使用正则表达式简单清理HTML（备用方案）
func stripHTMLSimple(html string) string {
	// 移除 script 和 style 标签及其内容
	re := regexp.MustCompile(`(?i)<script[^>]*>.*?</script>`)
	html = re.ReplaceAllString(html, "")
	re = regexp.MustCompile(`(?i)<style[^>]*>.*?</style>`)
	html = re.ReplaceAllString(html, "")

	// 将 <br>、</p>、</div> 等替换为换行
	re = regexp.MustCompile(`(?i)<br\s*/?>`)
	html = re.ReplaceAllString(html, "\n")
	re = regexp.MustCompile(`(?i)</(p|div|li|tr)>`)
	html = re.ReplaceAllString(html, "\n")

	// 移除所有HTML标签
	re = regexp.MustCompile(`<[^>]+>`)
	html = re.ReplaceAllString(html, "")

	// 解码常见的HTML实体
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")

	// 清理多余的空白字符
	return cleanWhitespace(html)
}

// cleanWhitespace 清理多余的空白字符
func cleanWhitespace(text string) string {
	// 替换多个连续空白为单个空格
	re := regexp.MustCompile(`[ \t]+`)
	text = re.ReplaceAllString(text, " ")

	// 替换多个连续换行为最多两个换行
	re = regexp.MustCompile(`\n{3,}`)
	text = re.ReplaceAllString(text, "\n\n")

	// 去除每行首尾空白
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	text = strings.Join(lines, "\n")

	return strings.TrimSpace(text)
}

// ========== 事件追踪相关操作 ==========

// CreateEventTrack 创建事件追踪
func (d *DB) CreateEventTrack(event *models.EventTrack) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO event_tracks (name, keywords, negative_keywords, description, roles, embedding, status, is_auto, match_count, last_match_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.Name, event.Keywords, event.NegativeKeywords, event.Description, event.Roles, event.Embedding, event.Status, event.IsAuto, event.MatchCount, event.LastMatchAt)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetEventTrack 获取单个事件
func (d *DB) GetEventTrack(id int64) (*models.EventTrack, error) {
	event := &models.EventTrack{}
	var lastMatchAt sql.NullTime
	var negativeKeywords sql.NullString
	err := d.db.QueryRow(`
		SELECT id, name, keywords, COALESCE(negative_keywords, ''), description, roles, embedding, status, is_auto, match_count, last_match_at, created_at, updated_at
		FROM event_tracks WHERE id = ?
	`, id).Scan(&event.ID, &event.Name, &event.Keywords, &negativeKeywords, &event.Description, &event.Roles, &event.Embedding,
		&event.Status, &event.IsAuto, &event.MatchCount, &lastMatchAt, &event.CreatedAt, &event.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if lastMatchAt.Valid {
		event.LastMatchAt = &lastMatchAt.Time
	}
	if negativeKeywords.Valid {
		event.NegativeKeywords = negativeKeywords.String
	}
	return event, nil
}

// ListEventTracks 列出所有事件
func (d *DB) ListEventTracks(status string) ([]*models.EventTrack, error) {
	query := `SELECT id, name, keywords, COALESCE(negative_keywords, ''), description, roles, embedding, status, is_auto, match_count, last_match_at, created_at, updated_at FROM event_tracks`
	var rows *sql.Rows
	var err error

	if status != "" {
		query += " WHERE status = ? ORDER BY updated_at DESC"
		rows, err = d.db.Query(query, status)
	} else {
		query += " ORDER BY updated_at DESC"
		rows, err = d.db.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.EventTrack
	for rows.Next() {
		event := &models.EventTrack{}
		var lastMatchAt sql.NullTime
		var negativeKeywords sql.NullString
		err := rows.Scan(&event.ID, &event.Name, &event.Keywords, &negativeKeywords, &event.Description, &event.Roles, &event.Embedding,
			&event.Status, &event.IsAuto, &event.MatchCount, &lastMatchAt, &event.CreatedAt, &event.UpdatedAt)
		if err != nil {
			continue
		}
		if lastMatchAt.Valid {
			event.LastMatchAt = &lastMatchAt.Time
		}
		if negativeKeywords.Valid {
			event.NegativeKeywords = negativeKeywords.String
		}
		events = append(events, event)
	}
	return events, nil
}

// GetActiveEventTracks 获取活跃事件（用于匹配）
func (d *DB) GetActiveEventTracks() ([]*models.EventTrack, error) {
	rows, err := d.db.Query(`
		SELECT id, name, keywords, COALESCE(negative_keywords, ''), description, roles, embedding, status, is_auto, match_count, last_match_at, created_at, updated_at
		FROM event_tracks WHERE status = 'active'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.EventTrack
	for rows.Next() {
		event := &models.EventTrack{}
		var lastMatchAt sql.NullTime
		var negativeKeywords sql.NullString
		err := rows.Scan(&event.ID, &event.Name, &event.Keywords, &negativeKeywords, &event.Description, &event.Roles, &event.Embedding,
			&event.Status, &event.IsAuto, &event.MatchCount, &lastMatchAt, &event.CreatedAt, &event.UpdatedAt)
		if err != nil {
			continue
		}
		if lastMatchAt.Valid {
			event.LastMatchAt = &lastMatchAt.Time
		}
		if negativeKeywords.Valid {
			event.NegativeKeywords = negativeKeywords.String
		}
		events = append(events, event)
	}
	return events, nil
}

// UpdateEventTrack 更新事件
func (d *DB) UpdateEventTrack(event *models.EventTrack) error {
	// 添加重试机制，避免数据库锁定
	var err error
	for i := 0; i < 3; i++ {
		_, err = d.db.Exec(`
			UPDATE event_tracks SET name = ?, keywords = ?, negative_keywords = ?, description = ?, roles = ?, embedding = ?, status = ?, is_auto = ?, match_count = ?, last_match_at = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, event.Name, event.Keywords, event.NegativeKeywords, event.Description, event.Roles, event.Embedding, event.Status, event.IsAuto, event.MatchCount, event.LastMatchAt, event.ID)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "database is locked") {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}
	return err
}

// UpdateEventTrackStatus 更新事件状态
func (d *DB) UpdateEventTrackStatus(id int64, status string) error {
	_, err := d.db.Exec(`UPDATE event_tracks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}

// UpdateEventTrackEmbedding 更新事件向量
func (d *DB) UpdateEventTrackEmbedding(id int64, embedding []byte) error {
	_, err := d.db.Exec(`UPDATE event_tracks SET embedding = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, embedding, id)
	return err
}

// DeleteEventTrack 删除事件
func (d *DB) DeleteEventTrack(id int64) error {
	_, err := d.db.Exec(`DELETE FROM event_tracks WHERE id = ?`, id)
	return err
}

// IncrementEventMatchCount 增加事件匹配计数
// GetEventTrackByName 按名称查找事件（话题导出为追踪事件时防重复）
func (d *DB) GetEventTrackByName(name string) (*models.EventTrack, error) {
	event := &models.EventTrack{}
	var keywords, negativeKeywords, description, roles sql.NullString
	var lastMatchAt sql.NullTime
	err := d.db.QueryRow(`
		SELECT id, name, keywords, negative_keywords, description, roles, status, last_match_at FROM event_tracks WHERE name = ?
	`, name).Scan(&event.ID, &event.Name, &keywords, &negativeKeywords, &description, &roles, &event.Status, &lastMatchAt)
	if err != nil {
		return nil, err
	}
	event.Keywords = keywords.String
	event.NegativeKeywords = negativeKeywords.String
	event.Description = description.String
	event.Roles = roles.String
	if lastMatchAt.Valid {
		event.LastMatchAt = &lastMatchAt.Time
	}
	return event, nil
}

func (d *DB) IncrementEventMatchCount(id int64) error {
	_, err := d.db.Exec(`
		UPDATE event_tracks SET match_count = match_count + 1, last_match_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?
	`, id)
	return err
}

// CreateEventArticle 创建事件-文章关联
func (d *DB) CreateEventArticle(ea *models.EventArticle) (int64, error) {
	result, err := d.db.Exec(`
		INSERT OR IGNORE INTO event_articles (event_id, article_id, role, importance, match_reason, match_score)
		VALUES (?, ?, ?, ?, ?, ?)
	`, ea.EventID, ea.ArticleID, ea.Role, ea.Importance, ea.MatchReason, ea.MatchScore)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetEventArticles 获取事件关联的文章（包含总结向量用于聚类）
func (d *DB) GetEventArticles(eventID int64, role string, limit, offset int) ([]*models.Article, error) {
	var query string
	var rows *sql.Rows
	var err error

	baseQuery := `
		SELECT a.id, a.feed_id, a.category_id, a.title, a.link, a.content, a.content_cleaned, a.summary, a.ai_summary, a.one_line_summary, a.keywords, a.tags_cache, a.is_ad, a.ad_reason, a.published_at, a.fetched_at, a.importance_score, a.topic_category, a.summary_embedding, ea.role, ea.match_score, f.title as feed_title
		FROM articles a
		INNER JOIN event_articles ea ON a.id = ea.article_id
		LEFT JOIN feeds f ON a.feed_id = f.id
		WHERE ea.event_id = ?
	`
	if role != "" {
		query = baseQuery + " AND ea.role = ? ORDER BY a.published_at DESC LIMIT ? OFFSET ?"
		rows, err = d.db.Query(query, eventID, role, limit, offset)
	} else {
		query = baseQuery + " ORDER BY a.published_at DESC LIMIT ? OFFSET ?"
		rows, err = d.db.Query(query, eventID, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var contentCleaned, aiSummary, oneLineSummary, keywords, tagsCache, adReason, feedTitle sql.NullString
		var categoryID sql.NullInt64
		var publishedAt sql.NullTime
		var summaryEmbedding []byte
		var eventRole string
		var matchScore float64
		err := rows.Scan(&article.ID, &article.FeedID, &categoryID, &article.Title, &article.Link,
			&article.Content, &contentCleaned, &article.Summary, &aiSummary, &oneLineSummary, &keywords,
			&tagsCache, &article.IsAd, &adReason, &publishedAt, &article.FetchedAt,
			&article.ImportanceScore, &article.TopicCategory, &summaryEmbedding, &eventRole, &matchScore, &feedTitle)
		if err != nil {
			continue
		}
		if contentCleaned.Valid {
			article.ContentCleaned = contentCleaned.String
		}
		if aiSummary.Valid {
			article.AISummary = aiSummary.String
		}
		if oneLineSummary.Valid {
			article.OneLineSummary = oneLineSummary.String
		}
		if keywords.Valid {
			article.Keywords = keywords.String
		}
		if tagsCache.Valid {
			article.TagsCache = tagsCache.String
		}
		if adReason.Valid {
			article.AdReason = adReason.String
		}
		if categoryID.Valid {
			article.CategoryID = &categoryID.Int64
		}
		if publishedAt.Valid {
			article.PublishedAt = &publishedAt.Time
		}
		if feedTitle.Valid {
			article.FeedTitle = feedTitle.String
		}
		article.SummaryEmbedding = summaryEmbedding
		article.MatchScore = matchScore
		articles = append(articles, article)
	}
	return articles, nil
}

// GetEventRoleStats 获取事件角色统计
func (d *DB) GetEventRoleStats(eventID int64) ([]*models.EventRoleStats, error) {
	rows, err := d.db.Query(`
		SELECT ea.role, COUNT(*) as count, (SELECT a.title FROM event_articles ea2 JOIN articles a ON a.id = ea2.article_id WHERE ea2.event_id = ea.event_id AND ea2.role = ea.role ORDER BY ea2.created_at DESC LIMIT 1) as latest
		FROM event_articles ea
		WHERE ea.event_id = ? AND ea.role IS NOT NULL AND ea.role != ''
		GROUP BY ea.role
		ORDER BY count DESC
	`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []*models.EventRoleStats
	for rows.Next() {
		s := &models.EventRoleStats{}
		if err := rows.Scan(&s.Role, &s.Count, &s.Latest); err != nil {
			continue
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// CountEventArticles 统计事件文章数
func (d *DB) CountEventArticles(eventID int64, role string) (int, error) {
	var count int
	var err error
	if role != "" {
		err = d.db.QueryRow(`SELECT COUNT(*) FROM event_articles WHERE event_id = ? AND role = ?`, eventID, role).Scan(&count)
	} else {
		err = d.db.QueryRow(`SELECT COUNT(*) FROM event_articles WHERE event_id = ?`, eventID).Scan(&count)
	}
	return count, err
}

// CheckArticleInEvent 检查文章是否已关联到事件
func (d *DB) CheckArticleInEvent(eventID, articleID int64) (bool, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM event_articles WHERE event_id = ? AND article_id = ?`, eventID, articleID).Scan(&count)
	return count > 0, err
}

// GetRecentArticlesWithEmbedding 获取近期带有向量的文章（用于热点检测）
func (d *DB) GetRecentArticlesWithEmbedding(window time.Duration) ([]*models.Article, error) {
	cutoff := time.Now().Add(-window)
	rows, err := d.db.Query(`
		SELECT id, feed_id, category_id, title, link, content, content_cleaned, summary, ai_summary, keywords, tags_cache, is_ad, ad_reason, published_at, fetched_at, embedding, summary_embedding, importance_score, topic_category, topic_weight
		FROM articles
		WHERE fetched_at >= ? AND (embedding IS NOT NULL AND embedding != '' OR summary_embedding IS NOT NULL AND summary_embedding != '')
		ORDER BY fetched_at DESC
		LIMIT 200
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		article := &models.Article{}
		var categoryID sql.NullInt64
		var content, contentCleaned, summary, aiSummary, keywords, tagsCache, adReason sql.NullString
		var publishedAt sql.NullTime
		var embedding, summaryEmbedding []byte

		err := rows.Scan(&article.ID, &article.FeedID, &categoryID, &article.Title, &article.Link,
			&content, &contentCleaned, &summary, &aiSummary, &keywords, &tagsCache,
			&article.IsAd, &adReason, &publishedAt, &article.FetchedAt, &embedding, &summaryEmbedding,
			&article.ImportanceScore, &article.TopicCategory, &article.TopicWeight)
		if err != nil {
			continue
		}

		article.Content = content.String
		article.ContentCleaned = contentCleaned.String
		article.Summary = summary.String
		article.AISummary = aiSummary.String
		article.Keywords = keywords.String
		article.TagsCache = tagsCache.String
		article.AdReason = adReason.String
		article.Embedding = embedding
		article.SummaryEmbedding = summaryEmbedding
		if categoryID.Valid {
			article.CategoryID = &categoryID.Int64
		}
		if publishedAt.Valid {
			article.PublishedAt = &publishedAt.Time
		}
		articles = append(articles, article)
	}
	return articles, nil
}

// GetActiveAndPendingEventTracks 获取活跃和待处理的事件（用于热点去重）
func (d *DB) GetActiveAndPendingEventTracks() ([]*models.EventTrack, error) {
	rows, err := d.db.Query(`
		SELECT id, name, keywords, description, roles, embedding, status, is_auto, match_count, last_match_at, created_at, updated_at
		FROM event_tracks WHERE status IN ('active', 'pending')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.EventTrack
	for rows.Next() {
		event := &models.EventTrack{}
		var lastMatchAt sql.NullTime
		err := rows.Scan(&event.ID, &event.Name, &event.Keywords, &event.Description, &event.Roles, &event.Embedding,
			&event.Status, &event.IsAuto, &event.MatchCount, &lastMatchAt, &event.CreatedAt, &event.UpdatedAt)
		if err != nil {
			continue
		}
		if lastMatchAt.Valid {
			event.LastMatchAt = &lastMatchAt.Time
		}
		events = append(events, event)
	}
	return events, nil
}

// DeleteEventArticles 删除某个事件的所有文章关联
func (d *DB) DeleteEventArticles(eventID int64) error {
	_, err := d.db.Exec(`DELETE FROM event_articles WHERE event_id = ?`, eventID)
	return err
}

// ResetEventMatchCount 重置事件的匹配计数为0
func (d *DB) ResetEventMatchCount(eventID int64) error {
	_, err := d.db.Exec(`UPDATE event_tracks SET match_count = 0, last_match_at = NULL WHERE id = ?`, eventID)
	return err
}

// GetInactiveEventTracks 获取长时间无更新的活跃事件（用于生命周期管理）
func (d *DB) GetInactiveEventTracks(inactiveDays int) ([]*models.EventTrack, error) {
	rows, err := d.db.Query(`
		SELECT id, name, keywords, description, roles, embedding, status, is_auto, match_count, last_match_at, created_at, updated_at
		FROM event_tracks
		WHERE status = 'active'
		  AND (last_match_at IS NULL OR last_match_at < datetime('now', '-' || ? || ' days'))
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.EventTrack
	for rows.Next() {
		event := &models.EventTrack{}
		var lastMatchAt sql.NullTime
		err := rows.Scan(&event.ID, &event.Name, &event.Keywords, &event.Description, &event.Roles, &event.Embedding,
			&event.Status, &event.IsAuto, &event.MatchCount, &lastMatchAt, &event.CreatedAt, &event.UpdatedAt)
		if err != nil {
			continue
		}
		if lastMatchAt.Valid {
			event.LastMatchAt = &lastMatchAt.Time
		}
		events = append(events, event)
	}
	return events, nil
}
