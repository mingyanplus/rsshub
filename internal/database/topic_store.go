package database

import (
	"database/sql"
	"strings"
	"time"

	"rss-ai/internal/models"
)

// topicSelectCols 话题完整列清单（含 embedding，列序与 scanTopic 一致）
const topicSelectCols = `id, title, ai_summary, entity_key, keywords, category, heat_score, article_count, source_count, embedding, status, first_article_at, last_updated_at, summary_updated_at, created_at`

// topicDisplayCols 展示用列清单：embedding 以 NULL 占位保持列序（页面/报告渲染不需要向量，
// 每行向量 BLOB 约 20KB，避免列表查询白白传输）
const topicDisplayCols = `id, title, ai_summary, entity_key, keywords, category, heat_score, article_count, source_count, NULL AS embedding, status, first_article_at, last_updated_at, summary_updated_at, created_at`

// topicScanner 兼容 *sql.Rows 与 *sql.Row 的扫描接口
type topicScanner interface {
	Scan(dest ...interface{}) error
}

// scanTopic 将一行扫描为 Topic（列序与 topicSelectCols 一致）
func scanTopic(rows topicScanner) (*models.Topic, error) {
	t := &models.Topic{}
	var aiSummary, entityKey, keywords, category, status sql.NullString
	var firstAt, lastAt, summaryUpdatedAt sql.NullTime
	err := rows.Scan(&t.ID, &t.Title, &aiSummary, &entityKey, &keywords, &category, &t.HeatScore,
		&t.ArticleCount, &t.SourceCount, &t.Embedding, &status, &firstAt, &lastAt, &summaryUpdatedAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.AISummary = aiSummary.String
	t.EntityKey = entityKey.String
	t.Keywords = keywords.String
	t.Category = category.String
	t.Status = status.String
	if firstAt.Valid {
		t.FirstArticleAt = firstAt.Time
	}
	if lastAt.Valid {
		t.LastUpdatedAt = lastAt.Time
	}
	if summaryUpdatedAt.Valid {
		sat := summaryUpdatedAt.Time
		t.SummaryUpdatedAt = &sat
	}
	return t, nil
}

// queryTopics 执行查询并扫描为话题列表（收敛各查询的 rows 样板）
func (d *DB) queryTopics(query string, args ...interface{}) ([]*models.Topic, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []*models.Topic
	for rows.Next() {
		t, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		topics = append(topics, t)
	}
	return topics, rows.Err()
}

// CreateTopic 创建新话题，返回话题 ID（时间戳统一用 Go 侧绑定，保证格式一致）
func (d *DB) CreateTopic(topic *models.Topic) (int64, error) {
	res, err := d.db.Exec(`
		INSERT INTO topics (title, ai_summary, entity_key, keywords, category, heat_score, article_count, source_count, embedding, status, first_article_at, last_updated_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)
	`, topic.Title, topic.AISummary, topic.EntityKey, topic.Keywords, topic.Category, topic.HeatScore,
		topic.ArticleCount, topic.SourceCount, topic.Embedding, topic.FirstArticleAt, topic.LastUpdatedAt, time.Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetActiveTopicsForAggregation 获取活跃窗口内的话题（用于新文章合入匹配，含代表向量）
func (d *DB) GetActiveTopicsForAggregation(window time.Duration) ([]*models.Topic, error) {
	return d.queryTopics(`
		SELECT `+topicSelectCols+`
		FROM topics
		WHERE status = 'active' AND last_updated_at > ?
		ORDER BY heat_score DESC
	`, time.Now().Add(-window))
}

// GetActiveTopicMemberVectors 批量获取活跃话题的成员文章向量（优先总结向量），
// 用于锚点式匹配：新文章与话题内任意单篇的相似度，而非与质心比较
func (d *DB) GetActiveTopicMemberVectors(window time.Duration) (map[int64][][]byte, error) {
	rows, err := d.db.Query(`
		SELECT ta.topic_id, COALESCE(a.summary_embedding, a.embedding)
		FROM topic_articles ta
		JOIN articles a ON a.id = ta.article_id
		JOIN topics t ON t.id = ta.topic_id
		WHERE t.status = 'active' AND t.last_updated_at > ?
	`, time.Now().Add(-window))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64][][]byte)
	for rows.Next() {
		var topicID int64
		var vec []byte
		if err := rows.Scan(&topicID, &vec); err != nil {
			return nil, err
		}
		if len(vec) > 0 {
			result[topicID] = append(result[topicID], vec)
		}
	}
	return result, rows.Err()
}

// AddArticleToTopic 将文章加入话题（已存在则忽略），返回是否新插入
func (d *DB) AddArticleToTopic(topicID, articleID int64, matchScore float64) (bool, error) {
	res, err := d.db.Exec(`
		INSERT OR IGNORE INTO topic_articles (topic_id, article_id, match_score) VALUES (?, ?, ?)
	`, topicID, articleID, matchScore)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetTopicArticleStats 统计话题的文章数、独立源数、平均重要性、最高来源权威度
func (d *DB) GetTopicArticleStats(topicID int64) (articleCount, sourceCount int, avgImportance, maxAuthority float64, err error) {
	err = d.db.QueryRow(`
		SELECT COUNT(*), COUNT(DISTINCT a.feed_id), COALESCE(AVG(a.importance_score), 5), COALESCE(MAX(CAST(f.authority AS REAL)), 3)
		FROM topic_articles ta
		JOIN articles a ON a.id = ta.article_id
		JOIN feeds f ON f.id = a.feed_id
		WHERE ta.topic_id = ?
	`, topicID).Scan(&articleCount, &sourceCount, &avgImportance, &maxAuthority)
	return
}

// UpdateTopicStats 更新话题统计、关键词、代表向量并刷新活跃时间
func (d *DB) UpdateTopicStats(topicID int64, articleCount, sourceCount int, heatScore float64, keywords string, embedding []byte) error {
	_, err := d.db.Exec(`
		UPDATE topics SET article_count = ?, source_count = ?, heat_score = ?, keywords = ?, embedding = ?, last_updated_at = ? WHERE id = ?
	`, articleCount, sourceCount, heatScore, keywords, embedding, time.Now(), topicID)
	return err
}

// UpdateTopicSummary 更新话题 AI 摘要（LLM 综合改写后）
func (d *DB) UpdateTopicSummary(topicID int64, summary string) error {
	_, err := d.db.Exec(`
		UPDATE topics SET ai_summary = ?, summary_updated_at = ? WHERE id = ?
	`, summary, time.Now(), topicID)
	return err
}

// GetTopicByID 根据 ID 获取话题
func (d *DB) GetTopicByID(id int64) (*models.Topic, error) {
	row := d.db.QueryRow(`SELECT `+topicDisplayCols+` FROM topics WHERE id = ?`, id)
	t, err := scanTopic(row)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetTopicByIDWithEmbedding 根据 ID 获取话题（含代表向量，用于导出为追踪事件等）
func (d *DB) GetTopicByIDWithEmbedding(id int64) (*models.Topic, error) {
	topics, err := d.queryTopics(`SELECT `+topicSelectCols+` FROM topics WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(topics) == 0 {
		return nil, sql.ErrNoRows
	}
	return topics[0], nil
}

// ListTopics 分页列出话题（最新更新在前），category 为空时不过滤
func (d *DB) ListTopics(category string, limit, offset int) ([]*models.Topic, error) {
	query := `SELECT ` + topicDisplayCols + ` FROM topics WHERE status != 'archived'`
	var args []interface{}
	if category != "" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY last_updated_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	return d.queryTopics(query, args...)
}

// GetHotTopics 获取热榜：since 之后有更新的话题按热度排序
func (d *DB) GetHotTopics(since time.Time, limit int) ([]*models.Topic, error) {
	return d.queryTopics(`
		SELECT `+topicDisplayCols+`
		FROM topics
		WHERE status != 'archived' AND last_updated_at > ?
		ORDER BY heat_score DESC, last_updated_at DESC
		LIMIT ?
	`, since, limit)
}

// GetTopicArticles 获取话题下的文章（新加入的在前），带订阅源名称
func (d *DB) GetTopicArticles(topicID int64, limit, offset int) ([]*models.Article, error) {
	rows, err := d.db.Query(`
		SELECT a.id, a.feed_id, a.title, a.link, a.ai_summary, a.summary, a.published_at, a.fetched_at, f.title
		FROM topic_articles ta
		JOIN articles a ON a.id = ta.article_id
		JOIN feeds f ON f.id = a.feed_id
		WHERE ta.topic_id = ?
		ORDER BY ta.created_at DESC, a.id DESC
		LIMIT ? OFFSET ?
	`, topicID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTopicArticleRows(rows)
}

// GetTopicArticlesForTopics 批量获取多个话题的最新文章（每话题最多 perTopic 篇），
// 消除列表页/报告路径的 N+1 查询，返回 topicID → 文章列表（新加入的在前）
func (d *DB) GetTopicArticlesForTopics(topicIDs []int64, perTopic int) (map[int64][]*models.Article, error) {
	result := make(map[int64][]*models.Article, len(topicIDs))
	if len(topicIDs) == 0 || perTopic <= 0 {
		return result, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(topicIDs)), ",")
	args := make([]interface{}, 0, len(topicIDs)+1)
	for _, id := range topicIDs {
		args = append(args, id)
	}
	args = append(args, perTopic)

	rows, err := d.db.Query(`
		SELECT topic_id, id, feed_id, title, link, ai_summary, summary, published_at, fetched_at, feed_title FROM (
			SELECT ta.topic_id AS topic_id, a.id, a.feed_id, a.title, a.link, a.ai_summary, a.summary, a.published_at, a.fetched_at, f.title AS feed_title,
			       ROW_NUMBER() OVER (PARTITION BY ta.topic_id ORDER BY ta.created_at DESC, a.id DESC) AS rn
			FROM topic_articles ta
			JOIN articles a ON a.id = ta.article_id
			JOIN feeds f ON f.id = a.feed_id
			WHERE ta.topic_id IN (`+placeholders+`)
		) WHERE rn <= ?
		ORDER BY topic_id, rn
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var topicID int64
		a := &models.Article{}
		var aiSummary, summary sql.NullString
		var publishedAt sql.NullTime
		if err := rows.Scan(&topicID, &a.ID, &a.FeedID, &a.Title, &a.Link, &aiSummary, &summary, &publishedAt, &a.FetchedAt, &a.FeedTitle); err != nil {
			return nil, err
		}
		a.AISummary = aiSummary.String
		a.Summary = summary.String
		if publishedAt.Valid {
			a.PublishedAt = &publishedAt.Time
		}
		result[topicID] = append(result[topicID], a)
	}
	return result, rows.Err()
}

// scanTopicArticleRows 扫描话题文章行（GetTopicArticles 的列序）
func scanTopicArticleRows(rows *sql.Rows) ([]*models.Article, error) {
	var articles []*models.Article
	for rows.Next() {
		a := &models.Article{}
		var aiSummary, summary sql.NullString
		var publishedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.FeedID, &a.Title, &a.Link, &aiSummary, &summary, &publishedAt, &a.FetchedAt, &a.FeedTitle); err != nil {
			return nil, err
		}
		a.AISummary = aiSummary.String
		a.Summary = summary.String
		if publishedAt.Valid {
			a.PublishedAt = &publishedAt.Time
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetRelatedTopicsByEntity 获取同主实体的历史话题（时间线），排除指定话题
func (d *DB) GetRelatedTopicsByEntity(entityKey string, excludeID int64, limit int) ([]*models.Topic, error) {
	return d.queryTopics(`
		SELECT `+topicDisplayCols+`
		FROM topics
		WHERE entity_key = ? AND entity_key != '' AND id != ?
		ORDER BY first_article_at DESC
		LIMIT ?
	`, entityKey, excludeID, limit)
}

// ListTopicsUpdatedBetween 获取时间段内有更新的话题（按热度排序，用于报告生成）
func (d *DB) ListTopicsUpdatedBetween(startTime, endTime time.Time, limit int) ([]*models.Topic, error) {
	return d.queryTopics(`
		SELECT `+topicDisplayCols+`
		FROM topics
		WHERE status != 'archived' AND last_updated_at > ? AND last_updated_at <= ?
		ORDER BY heat_score DESC, last_updated_at DESC
		LIMIT ?
	`, startTime, endTime, limit)
}

// GetRecentlyReportedTopicTitles 获取近期报告已覆盖的话题标题（反重复选题用）
// 只统计 startTime~endTime 之间生成的报告：endTime 通常传今天 0 点，
// 避免同一天内多次重新生成报告时把彼此的话题全部排除
func (d *DB) GetRecentlyReportedTopicTitles(startTime, endTime time.Time) ([]string, error) {
	rows, err := d.db.Query(`
		SELECT DISTINCT t.title
		FROM topics t
		JOIN topic_articles ta ON ta.topic_id = t.id
		JOIN report_articles ra ON ra.article_id = ta.article_id
		JOIN reports r ON r.id = ra.report_id
		WHERE r.created_at > ? AND r.created_at < ?
	`, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		titles = append(titles, title)
	}
	return titles, rows.Err()
}

// ClearTopics 清空话题数据（重建前调用）
func (d *DB) ClearTopics() error {
	if _, err := d.db.Exec(`DELETE FROM topic_articles`); err != nil {
		return err
	}
	_, err := d.db.Exec(`DELETE FROM topics`)
	return err
}

// ListRecentAnalyzedArticles 列出近期已分析完成的文章 ID（用于话题重建，旧在前）
func (d *DB) ListRecentAnalyzedArticles(since time.Time) ([]int64, error) {
	rows, err := d.db.Query(`
		SELECT id FROM articles
		WHERE fetched_at > ?
		  AND is_ad = 0
		  AND keywords NOT IN (?, ?)
		  AND importance_score > 1
		  AND ((summary_embedding IS NOT NULL AND length(summary_embedding) > 0)
		    OR (embedding IS NOT NULL AND length(embedding) > 0))
		ORDER BY fetched_at ASC
	`, since, models.KeywordPlaceholderMarked, models.KeywordPlaceholderFiltered)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetTopicCategories 获取话题的全部频道分类（去重）
func (d *DB) GetTopicCategories() ([]string, error) {
	rows, err := d.db.Query(`SELECT DISTINCT category FROM topics WHERE status != 'archived' AND category != '' ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

// ArchiveStaleTopics 归档长期无更新的话题，返回归档数量
func (d *DB) ArchiveStaleTopics(olderThan time.Duration) (int64, error) {
	res, err := d.db.Exec(`
		UPDATE topics SET status = 'archived'
		WHERE status = 'active' AND last_updated_at < ?
	`, time.Now().Add(-olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetArticleForAggregation 获取文章聚合所需字段（含向量、重要性与所属分类的内容类型）
func (d *DB) GetArticleForAggregation(id int64) (*models.Article, error) {
	a := &models.Article{}
	var summary, aiSummary, keywords, entities, contentType sql.NullString
	var publishedAt sql.NullTime
	err := d.db.QueryRow(`
		SELECT a.id, a.feed_id, a.title, a.summary, a.ai_summary, a.keywords, a.entities, a.is_ad, a.importance_score, a.topic_category, a.embedding, a.summary_embedding, a.published_at, COALESCE(c.content_type, 'news')
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		LEFT JOIN categories c ON c.id = f.category_id
		WHERE a.id = ?
	`, id).Scan(&a.ID, &a.FeedID, &a.Title, &summary, &aiSummary, &keywords, &entities, &a.IsAd, &a.ImportanceScore,
		&a.TopicCategory, &a.Embedding, &a.SummaryEmbedding, &publishedAt, &contentType)
	if err != nil {
		return nil, err
	}
	a.Summary = summary.String
	a.AISummary = aiSummary.String
	a.Keywords = keywords.String
	a.Entities = entities.String
	a.FeedContentType = contentType.String
	if publishedAt.Valid {
		a.PublishedAt = &publishedAt.Time
	}
	return a, nil
}
