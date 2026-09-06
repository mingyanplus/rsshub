package models

import (
	"time"
)

// 关键词占位标记（表示文章已处理但无有效关键词，不参与话题聚合/标签统计等）
const (
	KeywordPlaceholderMarked   = "已标记"
	KeywordPlaceholderFiltered = "内容过滤"
)

// IsPlaceholderKeywords 判断关键词字符串是否为处理占位标记
func IsPlaceholderKeywords(s string) bool {
	return s == KeywordPlaceholderMarked || s == KeywordPlaceholderFiltered
}

// Feed RSS 订阅源
type Feed struct {
	ID            int64      `json:"id" db:"id"`
	Title         string     `json:"title" db:"title"`
	URL           string     `json:"url" db:"url"`
	Description   string     `json:"description" db:"description"`
	CategoryID    *int64     `json:"category_id" db:"category_id"` // 默认分类
	LastFetchedAt *time.Time `json:"last_fetched_at" db:"last_fetched_at"`
	FetchInterval int        `json:"fetch_interval" db:"fetch_interval"` // 分钟
	IsActive      bool       `json:"is_active" db:"is_active"`
	SourceType    string     `json:"source_type" db:"source_type"`     // rss | html | json
	SourceConfig  string     `json:"source_config" db:"source_config"` // JSON 配置
	ContentFilter string     `json:"content_filter" db:"content_filter"` // 内容过滤规则：每行一条（正则或纯文本）
	Authority     int        `json:"authority" db:"authority"`         // 来源权威度 1-5（官方博客/一线媒体=5，聚合类=2）
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
}

// Article 文章
type Article struct {
	ID              int64      `json:"id" db:"id"`
	FeedID          int64      `json:"feed_id" db:"feed_id"`
	CategoryID      *int64     `json:"category_id" db:"category_id"`
	Title           string     `json:"title" db:"title"`
	Link            string     `json:"link" db:"link"`
	Content         string     `json:"content" db:"content"`
	ContentCleaned  string     `json:"content_cleaned" db:"content_cleaned"`
	Summary         string     `json:"summary" db:"summary"`            // RSS 原始摘要
	AISummary       string     `json:"ai_summary" db:"ai_summary"`      // AI 生成的摘要
	OneLineSummary  string     `json:"one_line_summary" db:"one_line_summary"` // 一句话总结（用于向量匹配）
	Keywords        string     `json:"keywords" db:"keywords"`
	TagsCache       string     `json:"tags_cache" db:"tags_cache"`
	Entities           string     `json:"entities" db:"entities"` // AI 提取的实体（公司、人物、国家等）
	TranslatedContent  string     `json:"translated_content" db:"translated_content"` // 非中文内容的中文翻译
	IsAd               bool       `json:"is_ad" db:"is_ad"`
	IsRead             bool       `json:"is_read" db:"is_read"`
	IsFavorite         bool       `json:"is_favorite" db:"is_favorite"`                 // 用户收藏（推荐正反馈）
	NotInterested      bool       `json:"not_interested" db:"not_interested"`           // 不感兴趣（推荐惩罚状态）
	AdReason        string     `json:"ad_reason" db:"ad_reason"`
	PublishedAt     *time.Time `json:"published_at" db:"published_at"`
	FetchedAt       time.Time  `json:"fetched_at" db:"fetched_at"`
	Embedding       []byte     `json:"embedding" db:"embedding"`
	SummaryEmbedding []byte    `json:"summary_embedding" db:"summary_embedding"` // 总结向量（用于更精确的匹配）
	// 新增字段 - 用于AI增强报告生成
	ImportanceScore int     `json:"importance_score" db:"importance_score"`
	TopicCategory   string  `json:"topic_category" db:"topic_category"`
	TopicWeight     float64 `json:"topic_weight" db:"topic_weight"` // 主题综合权重
	// 用于事件匹配的临时字段
	MatchScore float64 `json:"match_score" db:"-"` // 匹配得分 (来自 event_articles 表)
	FeedTitle   string  `json:"feed_title" db:"-"`  // 订阅源标题（临时字段）
	// 内容类型（聚合查询时经订阅源所属分类 JOIN 获得，临时字段）
	FeedContentType string `json:"feed_content_type" db:"-"` // news | blog
}

// NormalizeAuthority 钳制来源权威度到 1-5 并回写字段（越界取默认值 3）
func (f *Feed) NormalizeAuthority() int {
	if f.Authority < 1 || f.Authority > 5 {
		f.Authority = 3
	}
	return f.Authority
}

// Category 分类
type Category struct {
	ID          int64     `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	Color       string    `json:"color" db:"color"`
	ContentType string    `json:"content_type" db:"content_type"` // news（新闻/时事，参与话题聚合）| blog（博客/独立作品，不聚合）
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// Tag 标签
type Tag struct {
	ID         int64     `json:"id" db:"id"`
	Name       string    `json:"name" db:"name"`
	Color      string    `json:"color" db:"color"`
	UsageCount int       `json:"usage_count" db:"usage_count"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

// ArticleTag 文章-标签关联
type ArticleTag struct {
	ArticleID int64 `json:"article_id" db:"article_id"`
	TagID     int64 `json:"tag_id" db:"tag_id"`
}

// FollowRule 关注规则
type FollowRule struct {
	ID                  int64     `json:"id" db:"id"`
	Name                string    `json:"name" db:"name"`
	Description         string    `json:"description" db:"description"`
	Keywords            string    `json:"keywords" db:"keywords"`
	SimilarityThreshold float64   `json:"similarity_threshold" db:"similarity_threshold"`
	IsActive            bool      `json:"is_active" db:"is_active"`
	EnablePush          bool      `json:"enable_push" db:"enable_push"`
	PushChannels        string    `json:"push_channels" db:"push_channels"`
	Embedding           []byte    `json:"embedding" db:"embedding"`
	CreatedAt           time.Time `json:"created_at" db:"created_at"`
}

// Report 早晚报
type Report struct {
	ID           int64      `json:"id" db:"id"`
	Name         string     `json:"name" db:"name"`
	Type         string     `json:"type" db:"type"`               // morning, evening
	Content      string     `json:"content" db:"content"`         // 报告完整内容
	Summary      string     `json:"summary" db:"summary"`         // 报告摘要
	ScheduleTime string     `json:"schedule_time" db:"schedule_time"`
	Channels     string     `json:"channels" db:"channels"`
	ArticleCount int        `json:"article_count" db:"article_count"`
	IsActive     bool       `json:"is_active" db:"is_active"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	SentAt       *time.Time `json:"sent_at" db:"sent_at"`
}

// Notification 推送记录
type Notification struct {
	ID       int64     `json:"id" db:"id"`
	ReportID *int64    `json:"report_id" db:"report_id"`
	Channel  string    `json:"channel" db:"channel"`
	Content  string    `json:"content" db:"content"`
	SentAt   time.Time `json:"sent_at" db:"sent_at"`
	Status   string    `json:"status" db:"status"` // pending, sent, failed
}

// TopicPrompt 主题提示词模板
type TopicPrompt struct {
	ID             int64     `json:"id" db:"id"`
	Name           string    `json:"name" db:"name"`
	Keywords       string    `json:"keywords" db:"keywords"`
	Tags           string    `json:"tags" db:"tags"`
	Persona        string    `json:"persona" db:"persona"`
	PromptTemplate string    `json:"prompt_template" db:"prompt_template"`
	BriefTemplate  string    `json:"brief_template" db:"brief_template"`
	UsageCount     int       `json:"usage_count" db:"usage_count"`
	IsActive       bool      `json:"is_active" db:"is_active"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// ArticleCluster 文章聚类
type ArticleCluster struct {
	ID               int64         `json:"id"`
	Name             string        `json:"name"`              // LLM 分组名称，如"芯片半导体"
	Domain          string        `json:"domain"`            // 领域，如"科技"、"医疗"
	Articles         []*Article    `json:"articles"`
	Keywords         []string      `json:"keywords"`
	RepresentativeTags []string    `json:"representative_tags"` // LLM 提取的代表标签
	TopicType        string        `json:"topic_type"`
	Weight           ClusterWeight `json:"weight"`
	GeneratedContent string        `json:"generated_content"`
}

// ClusterWeight 聚类权重
type ClusterWeight struct {
	Authority  float64 `json:"authority"`   // 权威性 (0-1)
	Density    float64 `json:"density"`     // 信息密度 (0-1)
	Uniqueness float64 `json:"uniqueness"`  // 独特性 (0-1)
	Centrality float64 `json:"centrality"`  // 中心度 (0-1)
	FinalScore float64 `json:"final_score"` // 综合权重
}

// EventTrack 事件追踪
type EventTrack struct {
	ID               int64      `json:"id" db:"id"`
	Name             string     `json:"name" db:"name"`                         // 事件名称，如"伊朗局势"
	Keywords         string     `json:"keywords" db:"keywords"`                 // 关键词，逗号分隔（精确匹配）
	NegativeKeywords string     `json:"negative_keywords" db:"negative_keywords"` // 负面关键词，匹配到则扣分
	Description      string     `json:"description" db:"description"`           // 事件描述（用于向量匹配）
	Roles            string     `json:"roles" db:"roles"`                       // 预定义角色，逗号分隔
	Embedding        []byte     `json:"embedding" db:"embedding"`               // 事件向量
	Status           string     `json:"status" db:"status"`                     // pending, active, paused, completed
	IsAuto           bool       `json:"is_auto" db:"is_auto"`                   // 是否自动发现
	MatchCount       int        `json:"match_count" db:"match_count"`           // 匹配文章数
	LastMatchAt      *time.Time `json:"last_match_at" db:"last_match_at"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}

// EventArticle 事件-文章关联
type EventArticle struct {
	ID          int64     `json:"id" db:"id"`
	EventID     int64     `json:"event_id" db:"event_id"`
	ArticleID   int64     `json:"article_id" db:"article_id"`
	Role        string    `json:"role" db:"role"`           // 识别的角色（国家/组织/人物）
	Importance  int       `json:"importance" db:"importance"` // 重要性 1-10
	MatchReason string    `json:"match_reason" db:"match_reason"` // 匹配原因（keyword/vector/both）
	MatchScore  float64   `json:"match_score" db:"match_score"` // 匹配得分
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// EventRoleStats 事件角色统计
type EventRoleStats struct {
	Role   string `json:"role"`
	Count  int    `json:"count"`
	Latest string `json:"latest"` // 最新文章标题
}

// Topic 自动聚合的话题（跨多源的事件级实体，Readhub 式话题流）
type Topic struct {
	ID               int64      `json:"id" db:"id"`
	Title            string     `json:"title" db:"title"`
	AISummary        string     `json:"ai_summary" db:"ai_summary"`
	EntityKey        string     `json:"entity_key" db:"entity_key"`   // 主实体（用于历史话题时间线串联）
	Keywords         string     `json:"keywords" db:"keywords"`       // 话题关键词集合（合入门控与展示）
	Category         string     `json:"category" db:"category"`       // 频道分类
	HeatScore        float64    `json:"heat_score" db:"heat_score"`   // 综合热度
	ArticleCount     int        `json:"article_count" db:"article_count"`
	SourceCount      int        `json:"source_count" db:"source_count"` // 独立订阅源数（多源交叉验证）
	Embedding        []byte     `json:"embedding" db:"embedding"`     // 话题代表向量
	Status           string     `json:"status" db:"status"`           // active, archived
	FirstArticleAt   time.Time  `json:"first_article_at" db:"first_article_at"`
	LastUpdatedAt    time.Time  `json:"last_updated_at" db:"last_updated_at"`
	SummaryUpdatedAt *time.Time `json:"summary_updated_at" db:"summary_updated_at"` // 上次 LLM 重写摘要时间（控成本）
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	// 临时字段（页面展示用）
	LatestArticles []*Article `json:"latest_articles,omitempty" db:"-"`
}

// AIUpdateParams AI 分析结果更新参数
type AIUpdateParams struct {
	ID              int64
	AISummary       string
	OneLineSummary  string
	Keywords        string
	TagsCache       string
	IsAd            bool
	AdReason        string
	ImportanceScore int
	TopicCategory     string
	Entities          string
	TranslatedContent string
}

