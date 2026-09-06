package database

import (
	"database/sql"
	"strings"

	"rss-ai/internal/models"
)

// ListInterestClusters 列出指定极性的兴趣簇（weight 降序）
func (d *DB) ListInterestClusters(polarity string) ([]*models.InterestCluster, error) {
	rows, err := d.db.Query(`
		SELECT id, polarity, centroid, weight, sample_count, last_active_at, label, created_at
		FROM interest_clusters WHERE polarity = ? ORDER BY weight DESC
	`, polarity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clusters []*models.InterestCluster
	for rows.Next() {
		c := &models.InterestCluster{}
		if err := rows.Scan(&c.ID, &c.Polarity, &c.Centroid, &c.Weight, &c.SampleCount, &c.LastActiveAt, &c.Label, &c.CreatedAt); err != nil {
			return nil, err
		}
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

// CreateInterestCluster 新建兴趣簇
func (d *DB) CreateInterestCluster(c *models.InterestCluster) (int64, error) {
	result, err := d.db.Exec(`
		INSERT INTO interest_clusters (polarity, centroid, weight, sample_count, last_active_at, label, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, c.Polarity, c.Centroid, c.Weight, c.SampleCount, c.LastActiveAt, c.Label, c.CreatedAt)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// UpdateInterestCluster 更新兴趣簇质心/权重/样本数/标签/活跃时间
func (d *DB) UpdateInterestCluster(c *models.InterestCluster) error {
	_, err := d.db.Exec(`
		UPDATE interest_clusters SET centroid = ?, weight = ?, sample_count = ?, last_active_at = ?, label = ?
		WHERE id = ?
	`, c.Centroid, c.Weight, c.SampleCount, c.LastActiveAt, c.Label, c.ID)
	return err
}

// DeleteInterestCluster 删除兴趣簇
func (d *DB) DeleteInterestCluster(id int64) error {
	_, err := d.db.Exec(`DELETE FROM interest_clusters WHERE id = ?`, id)
	return err
}

// CountReadLogs 统计阅读行为日志条数（判断冷启动条件用）
func (d *DB) CountReadLogs() (int, error) {
	var count int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM read_logs`).Scan(&count)
	return count, err
}

// GetArticleProfileData 取文章的画像相关字段（向量/关键词），反馈入簇用
func (d *DB) GetArticleProfileData(id int64) (*models.Article, error) {
	a := &models.Article{ID: id}
	var keywords, tagsCache sql.NullString
	err := d.db.QueryRow(`
		SELECT embedding, summary_embedding, keywords, tags_cache FROM articles WHERE id = ?
	`, id).Scan(&a.Embedding, &a.SummaryEmbedding, &keywords, &tagsCache)
	if err != nil {
		return nil, err
	}
	a.Keywords = keywords.String
	a.TagsCache = tagsCache.String
	return a, nil
}

// FeedArticleDigest 订阅源近期文章摘要（冷启动建先验簇用）
type FeedArticleDigest struct {
	FeedID   int64
	Title    string
	Vectors  [][]byte // 近期文章 embedding（原样 BLOB）
	Keywords []string // 近期文章关键词（去重）
}

// ListFeedArticleDigests 每个活跃订阅源取近期带向量文章的 embedding 与关键词
func (d *DB) ListFeedArticleDigests(limit int) ([]*FeedArticleDigest, error) {
	feedRows, err := d.db.Query(`SELECT id, title FROM feeds WHERE is_active = TRUE ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type feedInfo struct {
		id    int64
		title string
	}
	var feeds []feedInfo
	for feedRows.Next() {
		var f feedInfo
		if err := feedRows.Scan(&f.id, &f.title); err != nil {
			feedRows.Close()
			return nil, err
		}
		feeds = append(feeds, f)
	}
	feedRows.Close()

	var digests []*FeedArticleDigest
	for _, f := range feeds {
		rows, err := d.db.Query(`
			SELECT embedding, keywords FROM articles
			WHERE feed_id = ? AND embedding IS NOT NULL AND LENGTH(embedding) > 0 AND is_ad = FALSE
			ORDER BY fetched_at DESC LIMIT ?
		`, f.id, limit)
		if err != nil {
			return nil, err
		}
		digest := &FeedArticleDigest{FeedID: f.id, Title: f.title}
		kwSeen := make(map[string]bool)
		for rows.Next() {
			var emb []byte
			var keywords string
			if err := rows.Scan(&emb, &keywords); err != nil {
				rows.Close()
				return nil, err
			}
			digest.Vectors = append(digest.Vectors, emb)
			for _, kw := range splitKeywords(keywords) {
				if !kwSeen[kw] {
					kwSeen[kw] = true
					digest.Keywords = append(digest.Keywords, kw)
				}
			}
		}
		rows.Close()
		if len(digest.Vectors) > 0 {
			digests = append(digests, digest)
		}
	}
	return digests, nil
}

// splitKeywords 拆分逗号分隔的关键词串
func splitKeywords(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' })
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}
