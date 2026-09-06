package database

import (
	"time"
)

// GetChannelWeights 读取全部通道权重（无记录的通道不在返回值中）
func (d *DB) GetChannelWeights() (map[string]float64, error) {
	rows, err := d.db.Query(`SELECT channel, weight FROM channel_weights`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]float64)
	for rows.Next() {
		var ch string
		var w float64
		if err := rows.Scan(&ch, &w); err != nil {
			return nil, err
		}
		m[ch] = w
	}
	return m, rows.Err()
}

// SaveChannelWeights 写回通道权重（覆盖）
func (d *DB) SaveChannelWeights(weights map[string]float64) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO channel_weights (channel, weight) VALUES (?, ?)
		ON CONFLICT(channel) DO UPDATE SET weight = excluded.weight`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for ch, w := range weights {
		if _, err := stmt.Exec(ch, w); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ListUnclickedExposureChannels 统计 [since, until) 时间窗内曝光且至今未点击的通道（跳过惩罚用）
func (d *DB) ListUnclickedExposureChannels(since, until int64) ([]string, error) {
	rows, err := d.db.Query(`
		SELECT DISTINCT channel FROM exposures
		WHERE clicked = 0 AND exposed_at >= ? AND exposed_at < ? AND channel != ''
	`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// LatestExposureChannel 文章最近一次曝光的通道（点击归因用）
func (d *DB) LatestExposureChannel(articleID int64) (string, error) {
	var ch string
	err := d.db.QueryRow(`SELECT channel FROM exposures WHERE article_id = ? ORDER BY exposed_at DESC LIMIT 1`, articleID).Scan(&ch)
	return ch, err
}

// TopicCategoryStats 主题类别阅读统计（指标用）
type TopicCategoryStats struct {
	Category string
	Total    int
	Read     int
}

// ListTopicCategoryStats 各主题类别的文章量与已读量
func (d *DB) ListTopicCategoryStats() ([]TopicCategoryStats, error) {
	rows, err := d.db.Query(`
		SELECT topic_category, COUNT(*), SUM(is_read) FROM articles
		WHERE topic_category != '' AND is_ad = FALSE
		GROUP BY topic_category
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []TopicCategoryStats
	for rows.Next() {
		var s TopicCategoryStats
		if err := rows.Scan(&s.Category, &s.Total, &s.Read); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// ChannelClickStats 通道曝光点击统计（探索点击率用）
type ChannelClickStats struct {
	Exposed int
	Clicked int
}

// ListChannelClickStats 各通道的曝光/点击量（近 lookbackDays 天）
func (d *DB) ListChannelClickStats(lookbackDays int) (map[string]*ChannelClickStats, error) {
	since := time.Now().AddDate(0, 0, -lookbackDays).Unix()
	rows, err := d.db.Query(`
		SELECT channel, COUNT(*), SUM(clicked) FROM exposures
		WHERE channel != '' AND exposed_at >= ?
		GROUP BY channel
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]*ChannelClickStats)
	for rows.Next() {
		var ch string
		var s ChannelClickStats
		if err := rows.Scan(&ch, &s.Exposed, &s.Clicked); err != nil {
			return nil, err
		}
		m[ch] = &s
	}
	return m, rows.Err()
}
