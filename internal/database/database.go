package database

import (
	"database/sql"
	"fmt"
)

// DB 数据库封装
type DB struct {
	db *sql.DB
}

// New 创建数据库连接并初始化表结构
func New(path string) (*DB, error) {
	db, err := sql.Open(sqliteDriver(), sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// 启用 WAL 日志模式（持久设置）：后台聚合与 API 并发读写时减少锁冲突
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	// 启用 SQLite 外键约束（默认关闭），使 ON DELETE CASCADE 生效
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	d := &DB{db: db}

	// 执行迁移
	if err := d.migrate(); err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return d, nil
}

// SQL 返回底层 *sql.DB（用于 LLM 缓存）
func (d *DB) SQL() *sql.DB {
	return d.db
}

// Close 关闭数据库连接
func (d *DB) Close() error {
	return d.db.Close()
}

// Exec 执行 SQL 语句
func (d *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return d.db.Exec(query, args...)
}

// migrate 执行数据库迁移
func (d *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS feeds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    url TEXT NOT NULL UNIQUE,
    description TEXT,
    category_id INTEGER REFERENCES categories(id),
    last_fetched_at DATETIME,
    fetch_interval INTEGER DEFAULT 30,
    is_active BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    color TEXT DEFAULT '#3498db',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    usage_count INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS articles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    feed_id INTEGER NOT NULL,
    category_id INTEGER REFERENCES categories(id),
    title TEXT NOT NULL,
    link TEXT NOT NULL UNIQUE,
    content TEXT,
    content_cleaned TEXT,
    summary TEXT,
    ai_summary TEXT,
    keywords TEXT,
    tags_cache TEXT,
    is_ad BOOLEAN DEFAULT FALSE,
    ad_reason TEXT,
    published_at DATETIME,
    fetched_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    embedding BLOB,
    importance_score INTEGER DEFAULT 5,
    topic_category TEXT DEFAULT '',
    FOREIGN KEY (feed_id) REFERENCES feeds(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS article_tags (
    article_id INTEGER NOT NULL,
    tag_id INTEGER NOT NULL,
    PRIMARY KEY (article_id, tag_id),
    FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS follow_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    keywords TEXT,
    similarity_threshold REAL DEFAULT 0.75,
    is_active BOOLEAN DEFAULT TRUE,
    enable_push BOOLEAN DEFAULT TRUE,
    push_channels TEXT,
    embedding BLOB,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS reports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    content TEXT,
    summary TEXT,
    article_count INTEGER DEFAULT 0,
    schedule_time TEXT,
    channels TEXT,
    is_active BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    sent_at DATETIME
);

CREATE TABLE IF NOT EXISTS notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    report_id INTEGER,
    channel TEXT NOT NULL,
    content TEXT,
    sent_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    status TEXT DEFAULT 'pending'
);

CREATE TABLE IF NOT EXISTS report_articles (
    report_id INTEGER NOT NULL,
    article_id INTEGER NOT NULL,
    PRIMARY KEY (report_id, article_id),
    FOREIGN KEY (report_id) REFERENCES reports(id) ON DELETE CASCADE,
    FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
);
`
	_, err := d.db.Exec(schema)
	if err != nil {
		return err
	}

	// 添加 feeds 表的 category_id 字段（如果不存在）
	d.db.Exec(`ALTER TABLE feeds ADD COLUMN category_id INTEGER REFERENCES categories(id)`)

	// 添加 articles 表的 ai_summary 字段（如果不存在）
	d.db.Exec(`ALTER TABLE articles ADD COLUMN ai_summary TEXT`)

	// 迁移：将现有的 AI 分析结果从 summary 复制到 ai_summary
	// 只迁移那些有关键词的文章（说明已经被 AI 分析过）
	d.db.Exec(`UPDATE articles SET ai_summary = summary WHERE (ai_summary IS NULL OR ai_summary = '') AND keywords IS NOT NULL AND keywords != ''`)

	// 修复文章分类：将文章的 category_id 设置为其所属 feed 的 category_id
	// 这是因为之前 AI 分析会覆盖分类，现在改为分类由 feed 决定
	d.db.Exec(`UPDATE articles SET category_id = (SELECT f.category_id FROM feeds f WHERE f.id = articles.feed_id) WHERE EXISTS (SELECT 1 FROM feeds f WHERE f.id = articles.feed_id AND f.category_id IS NOT NULL)`)

	// 添加 tags 表的 color 字段（如果不存在）
	d.db.Exec(`ALTER TABLE tags ADD COLUMN color TEXT DEFAULT '#3b82f6'`)

	// 添加 reports 表缺失的字段
	d.db.Exec(`ALTER TABLE reports ADD COLUMN content TEXT`)
	d.db.Exec(`ALTER TABLE reports ADD COLUMN summary TEXT`)
	d.db.Exec(`ALTER TABLE reports ADD COLUMN article_count INTEGER DEFAULT 0`)
	d.db.Exec(`ALTER TABLE reports ADD COLUMN created_at DATETIME DEFAULT CURRENT_TIMESTAMP`)
	d.db.Exec(`ALTER TABLE reports ADD COLUMN sent_at DATETIME`)

	// 添加 articles 表的重要性评分和主题分类字段
	d.db.Exec(`ALTER TABLE articles ADD COLUMN importance_score INTEGER DEFAULT 5`)
	d.db.Exec(`ALTER TABLE articles ADD COLUMN topic_category TEXT DEFAULT ''`)

	// 添加 articles 表的实体字段（用于事件追踪匹配）
	d.db.Exec(`ALTER TABLE articles ADD COLUMN entities TEXT DEFAULT ''`)

	// 创建主题提示词模板表
	d.db.Exec(`CREATE TABLE IF NOT EXISTS topic_prompts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    keywords TEXT,
    tags TEXT,
    persona TEXT NOT NULL,
    prompt_template TEXT NOT NULL,
    brief_template TEXT,
    usage_count INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`)

	// 创建索引
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topic_prompts_is_active ON topic_prompts(is_active)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topic_prompts_keywords ON topic_prompts(keywords)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topic_prompts_tags ON topic_prompts(tags)`)

	// 添加 articles.topic_weight 字段
	d.db.Exec(`ALTER TABLE articles ADD COLUMN topic_weight REAL DEFAULT 0`)

	// 创建关注规则推送记录表
	d.db.Exec(`CREATE TABLE IF NOT EXISTS follow_rule_notifications (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		rule_id INTEGER NOT NULL,
		article_id INTEGER NOT NULL,
		pushed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(rule_id, article_id),
		FOREIGN KEY (rule_id) REFERENCES follow_rules(id) ON DELETE CASCADE,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_follow_rule_notifications_rule ON follow_rule_notifications(rule_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_follow_rule_notifications_article ON follow_rule_notifications(article_id)`)

	// 创建事件追踪表
	d.db.Exec(`CREATE TABLE IF NOT EXISTS event_tracks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		keywords TEXT,
		description TEXT,
		roles TEXT,
		embedding BLOB,
		status TEXT DEFAULT 'pending',
		is_auto BOOLEAN DEFAULT FALSE,
		match_count INTEGER DEFAULT 0,
		last_match_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_tracks_status ON event_tracks(status)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_tracks_is_auto ON event_tracks(is_auto)`)

	// 创建事件-文章关联表
	d.db.Exec(`CREATE TABLE IF NOT EXISTS event_articles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id INTEGER NOT NULL,
		article_id INTEGER NOT NULL,
		role TEXT,
		importance INTEGER DEFAULT 5,
		match_reason TEXT,
		match_score REAL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(event_id, article_id),
		FOREIGN KEY (event_id) REFERENCES event_tracks(id) ON DELETE CASCADE,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_articles_event ON event_articles(event_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_articles_article ON event_articles(article_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_articles_role ON event_articles(role)`)

	// 添加 articles 表的一句话总结和总结向量字段
	d.db.Exec(`ALTER TABLE articles ADD COLUMN one_line_summary TEXT DEFAULT ''`)
	d.db.Exec(`ALTER TABLE articles ADD COLUMN summary_embedding BLOB`)
	d.db.Exec(`ALTER TABLE articles ADD COLUMN translated_content TEXT DEFAULT ''`)

	// 添加文章已读标记字段
	d.db.Exec(`ALTER TABLE articles ADD COLUMN is_read BOOLEAN DEFAULT FALSE`)

	// 添加 event_tracks 表的负面关键词字段
	d.db.Exec(`ALTER TABLE event_tracks ADD COLUMN negative_keywords TEXT DEFAULT ''`)

	// ========== 性能优化索引 ==========
	// articles 表索引（高频查询优化）
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_feed_id ON articles(feed_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_category_id ON articles(category_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_is_ad ON articles(is_ad)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_fetched_at ON articles(fetched_at)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_published_at ON articles(published_at)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_keywords ON articles(keywords)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_importance_score ON articles(importance_score)`)
	// 复合索引：用于获取待分析文章
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_keywords_fetched ON articles(keywords, fetched_at)`)
	// 复合索引：用于报告生成查询
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_report ON articles(fetched_at, is_ad, importance_score)`)

	// feeds 表索引
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_feeds_category_id ON feeds(category_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_feeds_is_active ON feeds(is_active)`)

	// tags 表索引
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_tags_usage_count ON tags(usage_count)`)

	// follow_rules 表索引
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_follow_rules_is_active ON follow_rules(is_active)`)

	// reports 表索引
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_reports_is_active ON reports(is_active)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_reports_type ON reports(type)`)

	// notifications 表索引
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status)`)

	// 创建 LLM 缓存表
	d.db.Exec(`CREATE TABLE IF NOT EXISTS llm_cache (
		key_hash   TEXT PRIMARY KEY,
		result     TEXT NOT NULL,
		model      TEXT NOT NULL,
		task_type  TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_llm_cache_expires ON llm_cache(expires_at)`)

	// feeds 表新增 source_type 和 source_config 字段
	d.db.Exec(`ALTER TABLE feeds ADD COLUMN source_type TEXT DEFAULT 'rss'`)
	d.db.Exec(`ALTER TABLE feeds ADD COLUMN source_config TEXT DEFAULT '{}'`)

	// 添加文章处理失败计数字段
	d.db.Exec(`ALTER TABLE articles ADD COLUMN process_attempts INTEGER DEFAULT 0`)
	d.db.Exec(`ALTER TABLE articles ADD COLUMN process_error TEXT DEFAULT ''`)

	// 添加 feeds 表的来源权威度字段（1-5，官方博客/一线媒体权重高）
	d.db.Exec(`ALTER TABLE feeds ADD COLUMN authority INTEGER DEFAULT 3`)
	d.db.Exec(`ALTER TABLE feeds ADD COLUMN content_filter TEXT DEFAULT ''`)

	// 添加 categories 表的内容类型字段（news 参与话题聚合；blog 为独立作品不聚合，订阅源按所属分类继承）
	d.db.Exec(`ALTER TABLE categories ADD COLUMN content_type TEXT DEFAULT 'news'`)

	// 创建自动话题聚合表（Readhub 式话题流）
	d.db.Exec(`CREATE TABLE IF NOT EXISTS topics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		ai_summary TEXT DEFAULT '',
		entity_key TEXT DEFAULT '',
		keywords TEXT DEFAULT '',
		category TEXT DEFAULT '',
		heat_score REAL DEFAULT 0,
		article_count INTEGER DEFAULT 0,
		source_count INTEGER DEFAULT 0,
		embedding BLOB,
		status TEXT DEFAULT 'active',
		first_article_at DATETIME,
		last_updated_at DATETIME,
		summary_updated_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topics_status ON topics(status)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topics_entity_key ON topics(entity_key)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topics_last_updated ON topics(last_updated_at)`)

	// 话题-文章关联表
	d.db.Exec(`CREATE TABLE IF NOT EXISTS topic_articles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic_id INTEGER NOT NULL,
		article_id INTEGER NOT NULL,
		match_score REAL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(topic_id, article_id),
		FOREIGN KEY (topic_id) REFERENCES topics(id) ON DELETE CASCADE,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topic_articles_topic ON topic_articles(topic_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_topic_articles_article ON topic_articles(article_id)`)

	// ========== 推荐系统 P1：行为数据层 ==========
	// 阅读行为日志（读完/秒退/收藏/不感兴趣等原始信号，unix 时间戳）
	d.db.Exec(`CREATE TABLE IF NOT EXISTS read_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		article_id INTEGER NOT NULL,
		action TEXT NOT NULL,
		progress REAL DEFAULT 0,
		dwell_ms INTEGER DEFAULT 0,
		created_at INTEGER NOT NULL,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_read_logs_article ON read_logs(article_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_read_logs_time ON read_logs(created_at)`)

	// 曝光记录（推荐列表展示即曝光，用于点击率与冷却判定）
	d.db.Exec(`CREATE TABLE IF NOT EXISTS exposures (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		article_id INTEGER NOT NULL,
		position INTEGER DEFAULT 0,
		channel TEXT DEFAULT '',
		clicked INTEGER DEFAULT 0,
		exposed_at INTEGER NOT NULL,
		FOREIGN KEY (article_id) REFERENCES articles(id) ON DELETE CASCADE
	)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_exposures_article ON exposures(article_id)`)
	d.db.Exec(`CREATE INDEX IF NOT EXISTS idx_exposures_time ON exposures(exposed_at)`)

	// 兴趣簇（P2 使用，先建表）
	d.db.Exec(`CREATE TABLE IF NOT EXISTS interest_clusters (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		polarity TEXT NOT NULL,
		centroid BLOB NOT NULL,
		weight REAL NOT NULL DEFAULT 1,
		sample_count INTEGER NOT NULL DEFAULT 1,
		last_active_at INTEGER NOT NULL,
		label TEXT DEFAULT '',
		created_at INTEGER NOT NULL
	)`)

	// 文章收藏与不感兴趣标记（推荐状态分/惩罚项的持久状态）
	d.db.Exec(`ALTER TABLE articles ADD COLUMN is_favorite BOOLEAN DEFAULT FALSE`)
	d.db.Exec(`ALTER TABLE articles ADD COLUMN not_interested BOOLEAN DEFAULT FALSE`)

	// 通道权重（P5 通道权重自适应：点击 ×1.1 / 跳过 ×0.95，定期归一化）
	d.db.Exec(`CREATE TABLE IF NOT EXISTS channel_weights (
		channel TEXT PRIMARY KEY,
		weight REAL NOT NULL DEFAULT 1
	)`)

	return nil
}
