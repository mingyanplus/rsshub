package database

import (
	"database/sql"

	"rss-ai/internal/models"
)

// RecommendationCandidate 推荐候选文章（未读 + 非广告 + 非不感兴趣，含向量）
type RecommendationCandidate struct {
	Article *models.Article
	HasVec  bool // 是否带有可用的 embedding
}

// FeedBehaviorStats 订阅源行为统计（来源分用）
type FeedBehaviorStats struct {
	Positive int     // 正反馈数（读完 + 收藏）
	Negative int     // 负反馈数（秒退 + 不感兴趣）
	Total    int     // 文章总数
	Read     int     // 已读数
	ReadRate float64 // 阅读率
}

// ListRecommendationCandidates 推荐候选池：未读 + 非广告 + 非不感兴趣，按时间倒序取 limit 篇
func (d *DB) ListRecommendationCandidates(limit int) ([]*RecommendationCandidate, error) {
	rows, err := d.db.Query(`
		SELECT a.id, a.feed_id, a.title, a.link, a.summary, a.ai_summary, a.keywords, a.tags_cache,
		       a.is_ad, a.published_at, a.fetched_at, a.is_read, a.is_favorite, a.not_interested,
		       a.embedding, a.summary_embedding
		FROM articles a
		WHERE a.is_read = FALSE AND a.is_ad = FALSE AND a.not_interested = FALSE
		ORDER BY COALESCE(a.published_at, a.fetched_at) DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*RecommendationCandidate
	for rows.Next() {
		a := &models.Article{}
		var summary, aiSummary, keywords, tagsCache sql.NullString
		if err := rows.Scan(&a.ID, &a.FeedID, &a.Title, &a.Link, &summary, &aiSummary, &keywords, &tagsCache,
			&a.IsAd, &a.PublishedAt, &a.FetchedAt, &a.IsRead, &a.IsFavorite, &a.NotInterested,
			&a.Embedding, &a.SummaryEmbedding); err != nil {
			return nil, err
		}
		a.Summary = summary.String
		a.AISummary = aiSummary.String
		a.Keywords = keywords.String
		a.TagsCache = tagsCache.String
		result = append(result, &RecommendationCandidate{
			Article: a,
			HasVec:  len(a.Embedding) > 0 || len(a.SummaryEmbedding) > 0,
		})
	}
	return result, rows.Err()
}

// ListFeedBehaviorStats 各订阅源的正/负反馈与阅读率统计
func (d *DB) ListFeedBehaviorStats() (map[int64]*FeedBehaviorStats, error) {
	stats := make(map[int64]*FeedBehaviorStats)

	// 文章总量与已读量
	rows, err := d.db.Query(`SELECT feed_id, COUNT(*), SUM(is_read) FROM articles GROUP BY feed_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var feedID int64
		var total, read int
		if err := rows.Scan(&feedID, &total, &read); err != nil {
			rows.Close()
			return nil, err
		}
		s := &FeedBehaviorStats{Total: total, Read: read}
		if total > 0 {
			s.ReadRate = float64(read) / float64(total)
		}
		stats[feedID] = s
	}
	rows.Close()

	// 行为日志正负反馈
	rows, err = d.db.Query(`
		SELECT a.feed_id,
			SUM(CASE WHEN rl.action IN ('read_complete', 'favorite') THEN 1 ELSE 0 END),
			SUM(CASE WHEN rl.action IN ('quick_bounce', 'not_interested') THEN 1 ELSE 0 END)
		FROM read_logs rl JOIN articles a ON a.id = rl.article_id
		GROUP BY a.feed_id
	`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var feedID int64
		var pos, neg int
		if err := rows.Scan(&feedID, &pos, &neg); err != nil {
			rows.Close()
			return nil, err
		}
		s, ok := stats[feedID]
		if !ok {
			s = &FeedBehaviorStats{}
			stats[feedID] = s
		}
		s.Positive, s.Negative = pos, neg
	}
	rows.Close()
	return stats, nil
}
