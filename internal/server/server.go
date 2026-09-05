package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	readability "github.com/go-shiori/go-readability"
	"github.com/russross/blackfriday/v2"
	"rss-ai/internal/ai"
	"rss-ai/internal/config"
	"rss-ai/internal/crawler"
	"rss-ai/internal/database"
	"rss-ai/internal/models"
	"rss-ai/internal/notify"
	"rss-ai/internal/processor"
	"rss-ai/internal/proxyutil"
)

// ArticleCluster 聚类后的文章（包含相似文章）
type ArticleCluster struct {
	*models.Article
	SimilarArticles []SimilarArticle `json:"similar_articles,omitempty"`
}

// SimilarArticle 相似文章摘要
type SimilarArticle struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Link  string `json:"link"`
}

var appConfig *config.Config
var appDB *database.DB
var appAnalyzer *ai.Analyzer
var appNotifyMgr *notify.Manager
var appEventMatcher *processor.EventMatcher
var appHotTopicDetector *processor.HotTopicDetector
var appTopicAggregator *processor.TopicAggregator

// SetConfig 设置应用配置
func SetConfig(cfg *config.Config) {
	appConfig = cfg

	// 热重载 AI 客户端配置
	if appAnalyzer != nil {
		appAnalyzer.UpdateConfig(&cfg.AI.LLM, &cfg.AI.Embedding, &cfg.AI.RateLimit)
		log.Printf("AI 客户端配置已热重载")
	}

	applyProxyConfig()
}

// applyProxyConfig 按配置把代理应用到内容抓取与 LLM 接口（支持热更新）
func applyProxyConfig() {
	if appConfig == nil {
		return
	}
	contentProxy := ""
	if appConfig.Proxy.EnableContent {
		contentProxy = appConfig.Proxy.URL
	}
	crawler.SetProxy(contentProxy)

	if appAnalyzer != nil {
		appAnalyzer.SetProxy(appConfig.Proxy.URL, appConfig.Proxy.EnableLLM)
	}
}

// SetDB 设置数据库实例
func SetDB(db *database.DB) {
	appDB = db
}

// SetAnalyzer 设置 AI 分析器
func SetAnalyzer(analyzer *ai.Analyzer) {
	appAnalyzer = analyzer
	applyProxyConfig()
}

// SetNotifyMgr 设置通知管理器
func SetNotifyMgr(mgr *notify.Manager) {
	appNotifyMgr = mgr
}

// SetEventMatcher 设置事件匹配器
func SetEventMatcher(matcher *processor.EventMatcher) {
	appEventMatcher = matcher
}

// SetHotTopicDetector 设置热点检测器
func SetHotTopicDetector(detector *processor.HotTopicDetector) {
	appHotTopicDetector = detector
}

// SetTopicAggregator 设置话题聚合器
func SetTopicAggregator(aggregator *processor.TopicAggregator) {
	appTopicAggregator = aggregator
}

// formatTimeInTimezone 将时间转换为配置的时区并格式化
func formatTimeInTimezone(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	// 获取配置的时区
	loc := time.Local // 默认使用本地时区
	if appConfig != nil && appConfig.Server.Timezone != "" {
		if tz, err := time.LoadLocation(appConfig.Server.Timezone); err == nil {
			loc = tz
		}
	}

	// 转换时区并格式化
	return t.In(loc).Format("2006-01-02 15:04")
}

// formatTimePtrInTimezone 处理可能为 nil 的时间指针
func formatTimePtrInTimezone(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return formatTimeInTimezone(*t)
}

// PageData 页面数据
type PageData struct {
	Title          string
	PageTitle      string
	Active         string
	Categories     []CategoryData
	Tags           []TagData
	Feeds          []FeedData
	Articles       []ArticleData
	Rules          []RuleData
	Reports        []ReportData
	Stats          StatsData
	// 首页专用
	RecentArticles  []ArticleData
	HotTopicCards   []*models.Topic
	FollowAlerts    []AlertData
	// 话题流（Readhub 式聚合）
	TopicList       []*models.Topic
	TopicDetail     *models.Topic
	TopicArticles   []*models.Article
	RelatedTopics   []*models.Topic
	HotTopics24h    []*models.Topic
	TopicCategories []string
	// 分页
	CurrentPage     int
	TotalPages      int
	TotalCount      int
	PageSize        int
	HasPrev         bool
	HasNext         bool
	PrevPage        int
	NextPage        int
	// 搜索和筛选
	SearchQuery     string
	SelectedFeed    string
	SelectedCategory string
	HideAds         bool
	SelectedRule    int64
	RuleName        string
	// 报告配置
	MorningEnabled   bool
	MorningTime      string
	MorningChannels  string
	EveningEnabled   bool
	EveningTime      string
	EveningChannels  string
	DailyEnabled     bool
	DailyTime        string
	DailyChannels    string
	// 设置
	Settings         SettingsData
}

type AlertData struct {
	ArticleTitle string
	ArticleLink  string
	RuleName     string
	Similarity   string
}

type SettingsData struct {
	LLMBaseURL           string
	LLMAPIKey            string
	LLMModel             string
	EmbeddingBaseURL     string
	EmbeddingAPIKey      string
	EmbeddingModel       string
	SMTPHost             string
	SMTPPort             int
	SMTPUsername         string
	SMTPPassword         string
	GotifyURL            string
	GotifyToken          string
	WebhookURL           string
	QQBotAppID           string
	QQBotAppSecret       string
	QQBotUserID          string
	MorningReportTime    string
	EveningReportTime    string
	DailyReportTime      string
	RefreshInterval      int
	AutoSummary          bool
	AdDetection          bool
	AutoTagging          bool
	Embedding            bool
	MorningReportEnabled bool
	EveningReportEnabled bool
	ProxyURL             string
	ProxyEnableContent   bool
	ProxyEnableLLM       bool
	ServerPassword       string
}

type CategoryData struct {
	ID           int64
	Name         string
	Description  string
	Icon         string
	ContentType  string
	FeedCount    int
	ArticleCount int
}

type TagData struct {
	ID           int64
	Name         string
	Color        string
	ArticleCount int
}

type FeedData struct {
	ID            int64
	Title         string
	URL           string
	Description   string
	CategoryID    *int64
	CategoryName  string
	CategoryIcon  string
	ArticleCount  int
	LastFetched   string
	FetchInterval int
	IsActive      bool
	SourceType    string
}

type ArticleData struct {
	ID              int64
	Title           string
	Link            string
	Summary         string // RSS 原始摘要
	AISummary       string // AI 生成的摘要
	FeedTitle       string
	FeedName        string
	Category        string
	CategoryName    string
	Keywords        string
	AdReason        string
	FetchedAt       string
	PublishedAt     string
	IsAd            bool
	IsRead          bool
	IsStarred       bool
	MatchedRuleName string
	Similarity      float64
}

type RuleData struct {
	ID                  int64
	Name                string
	Description         string
	Keywords            string
	SimilarityThreshold float64
	EnablePush          bool
	IsActive            bool
	MatchCount          int
	PushChannels        string
}

type ReportData struct {
	ID           int64
	Name         string
	Type         string
	Summary      string
	ArticleCount int
	TopicCount   int
	Channels     string
	CreatedAt    string
	SentAt       string
}

type StatsData struct {
	TodayArticles   int
	TodayDiff       int
	ActiveFeeds     int
	TotalFeeds      int
	PendingArticles int
	AdArticles      int
	DatabaseSize    string
	TotalArticles   int
	EmbeddingCount  int
	LastBackup      string
	AdArticlesCount int
}

// Router HTTP 路由器
type Router struct {
	mux *chi.Mux
}

// NewRouter 创建路由器
func NewRouter() *Router {
	r := chi.NewRouter()

	// 中间件
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(AuthMiddleware) // 登录校验（server.password 留空则不生效）

	// 登录/登出
	r.Get("/login", LoginPage)
	r.Post("/login", LoginSubmit)
	r.Get("/logout", Logout)

	// 健康检查
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})

	// 前端页面
	r.Get("/", IndexPage)
	r.Get("/feeds", FeedsPage)
	r.Get("/articles", ArticlesPage)
	r.Get("/categories", CategoriesPage)
	r.Get("/tags", TagsPage)
	r.Get("/rules", RulesPage)
	r.Get("/followed", FollowedPage)
	r.Get("/reports", ReportsPage)
	r.Get("/settings", SettingsPage)
	r.Get("/data", DataPage)
	r.Get("/events", EventsPage)
	r.Get("/topics", TopicsPage)
	r.Get("/topics/{id}", TopicDetailPage)

	// API 路由
	r.Route("/api", func(r chi.Router) {
		// 话题
		r.Post("/topics/rebuild", RebuildTopics)
		r.Post("/topics/{id}/to-event", ConvertTopicToEvent)

		// Feed 管理
		r.Get("/feeds", ListFeeds)
		r.Post("/feeds", CreateFeed)
		r.Get("/feeds/{id}", GetFeed)
		r.Put("/feeds/{id}", UpdateFeed)
		r.Delete("/feeds/{id}", DeleteFeed)
		r.Post("/feeds/{id}/refresh", RefreshFeed)
		r.Post("/feeds/refresh-all", RefreshAllFeeds)
		r.Post("/feeds/test-source", TestSource)
		r.Post("/feeds/parse-curl", ParseCurl)

		// 文章列表
		r.Get("/articles", ListArticles)
		r.Get("/articles/html", ListArticlesHTML)
		r.Post("/articles/{id}/fetch-original", FetchOriginalContent)
		r.Post("/articles/{id}/read", MarkArticleRead)
		r.Get("/articles/{id}", GetArticle)
		r.Post("/articles/process-pending", ProcessPendingArticles)
		r.Post("/articles/retry-failed", RetryFailedArticles)
		r.Post("/articles/{id}/retry", RetryArticleProcess)
		r.Post("/articles/regenerate-summary-embeddings", RegenerateSummaryEmbeddings)
		r.Post("/articles/reanalyze-for-summary", ReanalyzeArticlesForSummary)

		// 分类管理
		r.Get("/categories", ListCategories)
		r.Post("/categories", CreateCategory)
		r.Get("/categories/{id}", GetCategory)
		r.Put("/categories/{id}", UpdateCategory)
		r.Delete("/categories/{id}", DeleteCategory)

		// 标签管理
		r.Get("/tags", ListTags)
		r.Get("/tags/{id}", GetTag)
		r.Put("/tags/{id}", UpdateTag)
		r.Delete("/tags/{id}", DeleteTag)

		// 关注规则
		r.Get("/rules", ListRules)
		r.Post("/rules", CreateRule)
		r.Get("/rules/{id}", GetRule)
		r.Put("/rules/{id}", UpdateRule)
		r.Delete("/rules/{id}", DeleteRule)
		r.Post("/rules/{id}/toggle", ToggleRule)
		r.Post("/rules/check-push", CheckFollowRulesPush)
		r.Delete("/rules/{id}/push-records", ClearRulePushRecords)

		// 关注规则匹配的文章
		r.Get("/followed", ListFollowedArticles)

		// 报告
		r.Get("/reports", ListReports)
		r.Post("/reports/generate", GenerateReport)
		r.Get("/reports/latest", GetLatestReport)
		r.Get("/reports/{id}", GetReport)
		r.Get("/reports/{id}/full", GetFullReport)
		r.Post("/reports/resend", ResendReport)
		r.Post("/reports/config", UpdateReportConfig)

		// 数据管理
		r.Post("/data/export", ExportData)
		r.Post("/data/import", ImportData)
		r.Post("/data/cleanup", CleanupData)
		r.Post("/data/reset", ResetData)
		r.Post("/data/vacuum", VacuumDB)

		// 搜索
		r.Get("/search", SearchArticles)

		// 设置
		r.Post("/settings", SaveSettings)
		r.Post("/settings/test", TestSettingsConnection)

		// 配置重载
		r.Post("/config/reload", ReloadConfig)
		r.Get("/config", GetConfig)

		// 主题模板管理
		r.Get("/topic-prompts", ListTopicPrompts)
		r.Post("/topic-prompts", CreateTopicPrompt)
		r.Get("/topic-prompts/{id}", GetTopicPrompt)
		r.Put("/topic-prompts/{id}", UpdateTopicPrompt)
		r.Delete("/topic-prompts/{id}", DeleteTopicPrompt)

		// 事件追踪管理
		r.Get("/events", ListEventTracks)
		r.Post("/events", CreateEventTrack)
		r.Get("/events/pending", ListPendingEvents)
		r.Get("/events/{id}", GetEventTrack)
		r.Put("/events/{id}", UpdateEventTrack)
		r.Delete("/events/{id}", DeleteEventTrack)
		r.Post("/events/{id}/activate", ActivateEventTrack)
		r.Post("/events/{id}/pause", PauseEventTrack)
		r.Post("/events/{id}/complete", CompleteEventTrack)
		r.Post("/events/{id}/match", MatchSingleEventArticles)
		r.Get("/events/{id}/articles", GetEventArticles)
		r.Get("/events/{id}/stats", GetEventStats)
		r.Post("/events/{id}/optimize", OptimizeEventDescription)
		r.Post("/events/{id}/check-quality", CheckEventMatchQuality)

		// 热点检测和生命周期管理
		r.Post("/events/detect-hot-topics", DetectHotTopics)
		r.Post("/events/auto-pause-inactive", AutoPauseInactiveEvents)
		r.Post("/events/match-articles", MatchArticlesToEvents)
	})

	// 静态文件服务
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	return &Router{mux: r}
}

// ServeHTTP 实现 http.Handler 接口
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Routes 返回所有路由信息
func (r *Router) Routes() []RouteInfo {
	return []RouteInfo{
		{Method: "GET", Path: "/health"},
		{Method: "GET", Path: "/ready"},
		{Method: "GET", Path: "/api/feeds"},
		{Method: "POST", Path: "/api/feeds"},
		{Method: "GET", Path: "/api/articles"},
	}
}

// RouteInfo 路由信息
type RouteInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// ListFeeds 列出所有订阅源
func ListFeeds(w http.ResponseWriter, r *http.Request) {
	if appDB != nil {
		feeds, err := appDB.ListFeeds()
		if err != nil {
			http.Error(w, "Failed to list feeds: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(feeds)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode([]interface{}{})
}

// CreateFeed 创建订阅源
func CreateFeed(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		http.Error(w, "Request body is required", http.StatusBadRequest)
		return
	}

	// 检查 Content-Type 来决定如何解析
	contentType := r.Header.Get("Content-Type")
	var req CreateFeedRequest

	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		// 表单数据
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}
		req.URL = r.FormValue("url")
		req.Title = r.FormValue("title")
		req.Description = r.FormValue("description")
		if catID := r.FormValue("category_id"); catID != "" {
			if id, err := strconv.ParseInt(catID, 10, 64); err == nil {
				req.CategoryID = &id
			}
		}
		if interval := r.FormValue("fetch_interval"); interval != "" {
			if i, err := strconv.Atoi(interval); err == nil {
				req.FetchInterval = i
			}
		}
		req.IsActive = r.FormValue("is_active") == "on" || r.FormValue("is_active") == "true"
	}

	// 验证必填字段
	if req.URL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}

	// 设置默认值
	if req.FetchInterval <= 0 {
		req.FetchInterval = 30
	}

	// 保存到数据库
	if appDB != nil {
		feed := &models.Feed{
			Title:         req.Title,
			URL:           req.URL,
			Description:   req.Description,
			CategoryID:    req.CategoryID,
			FetchInterval: req.FetchInterval,
			IsActive:      req.IsActive,
			SourceType:    req.SourceType,
			SourceConfig:  req.SourceConfig,
		ContentFilter: req.ContentFilter,
		}
		id, err := appDB.CreateFeed(feed)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				http.Error(w, "Feed URL already exists", http.StatusConflict)
				return
			}
			http.Error(w, "Failed to create feed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		feed.ID = id

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Feed created successfully",
			"data":    feed,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

// GetFeed 获取单个订阅源
func GetFeed(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	id, err := strconv.ParseInt(feedID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid feed ID", http.StatusBadRequest)
		return
	}

	feed, err := appDB.GetFeedByID(id)
	if err != nil {
		http.Error(w, "Feed not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(feed)
}

// UpdateFeed 更新订阅源
func UpdateFeed(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	id, err := strconv.ParseInt(feedID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid feed ID", http.StatusBadRequest)
		return
	}

	var req CreateFeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	feed := &models.Feed{
		ID:           id,
		Title:        req.Title,
		URL:          req.URL,
		Description:  req.Description,
		CategoryID:   req.CategoryID,
		FetchInterval: req.FetchInterval,
		IsActive:     req.IsActive,
		SourceType:    req.SourceType,
		SourceConfig:  req.SourceConfig,
		ContentFilter: req.ContentFilter,
	}

	if err := appDB.UpdateFeed(feed); err != nil {
		http.Error(w, "Failed to update feed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      id,
		"updated": true,
	})
}

// DeleteFeed 删除订阅源
func DeleteFeed(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	id, err := strconv.ParseInt(feedID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid feed ID", http.StatusBadRequest)
		return
	}

	if err := appDB.DeleteFeed(id); err != nil {
		http.Error(w, "Failed to delete feed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RefreshFeed 刷新订阅源
func RefreshFeed(w http.ResponseWriter, r *http.Request) {
	feedID := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 获取 feed 信息
	var feedIDInt int64
	if _, err := fmt.Sscanf(feedID, "%d", &feedIDInt); err != nil {
		http.Error(w, "Invalid feed ID", http.StatusBadRequest)
		return
	}

	feed, err := appDB.GetFeedByID(feedIDInt)
	if err != nil {
		http.Error(w, "Feed not found", http.StatusNotFound)
		return
	}

	// 异步抓取
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()

		source, err := crawler.NewSource(feed.URL, feed.SourceType, feed.SourceConfig)
		if err != nil {
			fmt.Printf("Failed to create source for feed %s: %v\n", feed.URL, err)
			return
		}

		parsedFeed, err := source.FetchAndParse(ctx)
		if err != nil {
			fmt.Printf("Failed to fetch feed %s: %v\n", feed.URL, err)
			return
		}

		fmt.Printf("Fetched feed %s: %d items\n", feed.Title, len(parsedFeed.Items))

		// 保存文章到数据库
		newCount := 0
		var newArticles []struct {
			id          int64
			title       string
			content     string
			description string
		}

		for _, item := range parsedFeed.Items {
			// 检查文章是否已存在
			exists, _ := appDB.ArticleExists(item.Link)
			if exists {
				fmt.Printf("Article already exists: %s\n", item.Link)
				continue
			}

			fmt.Printf("Creating article: %s (link: %s)\n", item.Title, item.Link)

			// 使用 Content，如果为空则回退到 Description
			content := item.Content
			if content == "" {
				content = item.Description
			// Apply per-feed content filter rules
			content = crawler.ApplyContentFilter(content, feed.ContentFilter)
			}

			// 解析发布时间 - 优先使用 gofeed 已解析的时间
			var publishedAt *time.Time
			if item.PublishedParsed != nil {
				publishedAt = item.PublishedParsed
			} else if item.Published != "" {
				if t, err := time.Parse(time.RFC1123, item.Published); err == nil {
					publishedAt = &t
				} else if t, err := time.Parse(time.RFC1123Z, item.Published); err == nil {
					publishedAt = &t
				} else if t, err := time.Parse(time.RFC3339, item.Published); err == nil {
					publishedAt = &t
				} else if t, err := time.Parse("2006-01-02 15:04:05", item.Published); err == nil {
					publishedAt = &t
				}
			}

			article := &models.Article{
				FeedID:      feed.ID,
				CategoryID:  feed.CategoryID, // 使用订阅源的默认分类
				Title:       item.Title,
				Link:        item.Link,
				Content:     content,
				Summary:     item.Description,
				PublishedAt: publishedAt,
			}

			articleID, err := appDB.CreateArticle(article)
			if err != nil {
				fmt.Printf("Failed to create article %s: %v\n", item.Link, err)
				continue
			}
			newCount++

			// 收集新文章用于后续 AI 分析
			newArticles = append(newArticles, struct {
				id          int64
				title       string
				content     string
				description string
			}{articleID, item.Title, content, item.Description})
		}

		fmt.Printf("Feed %s refreshed, %d new articles\n", feed.Title, newCount)

		// 更新最后抓取时间
		appDB.UpdateFeedLastFetched(feed.ID)

		// 串行处理 AI 分析（避免 API 限流）
		if appAnalyzer != nil && len(newArticles) > 0 {
			// 从配置获取文章分析间隔
			articleInterval := 2 * time.Second
			if appConfig != nil {
				articleInterval = appConfig.AI.RateLimit.ArticleInterval
			}

			for i, art := range newArticles {
				// 每篇文章之间添加延迟
				if i > 0 {
					time.Sleep(articleInterval)
				}
				analyzeArticleAsync(art.id, art.title, art.content, art.description)
			}
		}
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         feedID,
		"refreshing": true,
		"message":    "Feed refresh started",
	})
}

// stripHTMLSimple 简单清理 HTML 标签
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
	html = strings.TrimSpace(html)
	re = regexp.MustCompile(`\s+`)
	html = re.ReplaceAllString(html, " ")

	return html
}

// isObviousSpam 检测是否为明显的垃圾/广告内容
func isObviousSpam(title, content string) bool {
	// 1. 来自 Post Bot 转发的内容
	if strings.Contains(content, "Forwarded From <b>Post Bot</b>") {
		return true
	}

	// 2. 标题包含 🔁（转发）且主要是表情符号
	if strings.Contains(title, "🔁") {
		// 检查标题是否主要是表情符号
		emojiCount := 0
		for _, r := range title {
			if r >= 0x1F300 && r <= 0x1FADBF {
				emojiCount++
			}
		}
		if emojiCount > len(title)/2 {
			return true
		}
	}

	// 3. 标题以 📢 开头且内容主要是频道推广
	if strings.Contains(title, "📢") && strings.Contains(content, "t.me/") {
		// 检查内容是否主要是频道推广（包含多个 t.me/ 链接）
		linkCount := strings.Count(content, "t.me/")
		if linkCount >= 2 {
			return true
		}
	}

	// 4. 标题包含明显的广告关键词
	adKeywords := []string{"球速", "体育投注", "博彩", "Y3国际", "Y3娱乐"}
	for _, kw := range adKeywords {
		if strings.Contains(title, kw) || strings.Contains(content, kw) {
			return true
		}
	}

	return false
}

// analyzeArticleAsync 异步分析文章
func analyzeArticleAsync(articleID int64, title, content, description string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 使用内容或描述
	text := content
	if text == "" {
		text = description
	}
	if text == "" {
		return
	}

	// 限制内容长度
	if len(text) > 4000 {
		text = text[:4000]
	}

	// 预过滤：检测明显的垃圾/广告内容
	if isObviousSpam(title, text) {
		fmt.Printf("Article %d detected as obvious spam, skipping AI analysis\n", articleID)
		// 清理 summary 中的 HTML 标签，用于列表显示
		cleanSummary := stripHTMLSimple(text)
		if len(cleanSummary) > 200 {
			cleanSummary = cleanSummary[:200] + "..."
		}
		appDB.UpdateArticleAI(&models.AIUpdateParams{
			ID:              articleID,
			AISummary:       cleanSummary,
			Keywords:        "已标记",
			IsAd:            true,
			AdReason:        "自动检测：垃圾/广告内容（来自机器人转发或内容空洞）",
			ImportanceScore: 1,
			TopicCategory:   "垃圾信息",
		})
		return
	}

	// AI 分析
	result, err := appAnalyzer.AnalyzeArticle(ctx, title, text)
	if err != nil {
		// 内容安全过滤：直接标记为广告+已处理
		if ai.IsContentBlockedError(err) {
			fmt.Printf("Article %d blocked by content filter, skipping\n", articleID)
			appDB.UpdateArticleAI(&models.AIUpdateParams{
				ID:              articleID,
				AISummary:       "文章内容触发安全过滤，跳过AI分析",
				Keywords:        "内容过滤",
				IsAd:            false,
				AdReason:        "内容安全过滤：" + err.Error(),
				ImportanceScore: 1,
				TopicCategory:   "内容过滤",
			})
			return
		}
		fmt.Printf("Failed to analyze article %d: %v\n", articleID, err)
		appDB.IncrementProcessAttempts(articleID, err.Error())
		return
	}

	// 更新文章 AI 分析结果（不修改分类，分类由 RSS feed 决定）
	keywords := strings.Join(result.Keywords, ",")
	tagsCache := strings.Join(result.Tags, ",")
	entities := strings.Join(result.Entities, ",")

	// 处理纯图片/无实质内容的文章：LLM 返回空 keywords 时设置标记，避免无限循环
	if keywords == "" {
		keywords = "已标记"
		if result.Summary == "" {
			result.Summary = "文章内容为图片或无实质文本，无法提取摘要"
		}
		if result.OneLineSummary == "" {
			result.OneLineSummary = "仅含图片，缺少可分析的内容"
		}
		if result.TopicCategory == "" {
			result.TopicCategory = "纯图片"
		}
		fmt.Printf("Article %d has no text content (image-only), marked as processed\n", articleID)
	}

	if err := appDB.UpdateArticleAI(&models.AIUpdateParams{
		ID:              articleID,
		AISummary:       result.Summary,
		OneLineSummary:  result.OneLineSummary,
		Keywords:        keywords,
		TagsCache:       tagsCache,
		IsAd:            result.IsAd,
		AdReason:        result.AdReason,
		ImportanceScore: result.ImportanceScore,
		TopicCategory:   result.TopicCategory,
		Entities:        entities,
		TranslatedContent: result.TranslatedContent,
	}); err != nil {
		fmt.Printf("Failed to update article AI data %d: %v\n", articleID, err)
	}

	// 生成总结向量（用于更精确的事件匹配）
	var summaryEmbBytes []byte
	if result.OneLineSummary != "" {
		var err2 error
		summaryEmbBytes, err2 = appAnalyzer.GetEmbedding(ctx, result.OneLineSummary)
		if err2 == nil {
			appDB.UpdateArticleSummaryEmbedding(articleID, summaryEmbBytes)
		}
	}

	// 处理标签：为每个关键词创建标签并关联文章
	for _, keyword := range result.Keywords {
		if keyword == "" {
			continue
		}
		// 查找或创建标签
		var tagID int64
		existingTag, err := appDB.GetTagByName(keyword)
		if err == nil && existingTag != nil {
			tagID = existingTag.ID
			appDB.IncrementTagUsage(tagID)
		} else {
			// 创建新标签
			newTag := &models.Tag{
				Name:       keyword,
				Color:      getRandomTagColor(),
				UsageCount: 1,
			}
			tagID, err = appDB.CreateTag(newTag)
			if err != nil {
				fmt.Printf("Failed to create tag %s: %v\n", keyword, err)
				continue
			}
		}
		// 建立文章-标签关联
		if err := appDB.AddArticleTag(articleID, tagID); err != nil {
			fmt.Printf("Failed to add article tag relation: %v\n", err)
		}
	}

	// 获取全文向量嵌入（用于聚类和报告生成）
	embeddingText := title + " " + result.Summary
	embeddingBytes, err := appAnalyzer.GetEmbedding(ctx, embeddingText)
	if err != nil {
		fmt.Printf("Failed to get embedding for article %d: %v\n", articleID, err)
		return
	}

	if err := appDB.UpdateArticleEmbedding(articleID, embeddingBytes); err != nil {
		fmt.Printf("Failed to update article embedding %d: %v\n", articleID, err)
	}

	// 事件匹配：只调用一次，优先使用总结向量（更精确），否则使用全文向量
	if appEventMatcher != nil {
		go func() {
			// 优先使用总结向量，如果没有则使用全文向量
			embBytes := summaryEmbBytes
			summBytes := summaryEmbBytes
			if len(embBytes) == 0 {
				embBytes = embeddingBytes
				summBytes = nil
			}
			if err := appEventMatcher.MatchArticleToEvents(articleID, title, text, embBytes, summBytes); err != nil {
				fmt.Printf("Failed to match article %d to events: %v\n", articleID, err)
			}
		}()
	}

	// 话题聚合：将文章归入自动话题（Readhub 式话题流）
	if appTopicAggregator != nil {
		go func() {
			if err := appTopicAggregator.AggregateArticle(articleID); err != nil {
				fmt.Printf("Failed to aggregate article %d into topics: %v\n", articleID, err)
			}
		}()
	}

	fmt.Printf("Article %d analyzed: isAd=%v, keywords=%s\n", articleID, result.IsAd, keywords)

	// 检查关注规则并发送推送
	checkAndNotifyFollowRules(articleID, title, result.Summary, result.Keywords, "")
}

// CheckAllArticlesFollowRules 检查所有文章的关注规则推送（用于初始化或手动触发）
func CheckAllArticlesFollowRules() {
	if appDB == nil || appNotifyMgr == nil {
		log.Printf("CheckAllArticlesFollowRules: appDB or appNotifyMgr is nil")
		return
	}

	// 获取所有启用推送的规则
	rules, err := appDB.GetActiveFollowRulesWithPush()
	if err != nil {
		log.Printf("Failed to get follow rules: %v", err)
		return
	}

	log.Printf("CheckAllArticlesFollowRules: found %d rules with push enabled", len(rules))
	if len(rules) == 0 {
		return
	}

	// 获取最近 24 小时的文章
	since := time.Now().Add(-24 * time.Hour)
	articles, err := appDB.GetArticlesForReport(since)
	if err != nil {
		log.Printf("Failed to get articles: %v", err)
		return
	}

	log.Printf("CheckAllArticlesFollowRules: checking %d articles from last 24h", len(articles))

	for _, article := range articles {
		if article.IsAd {
			continue
		}

		// 解析文章关键词
		var keywords []string
		if article.Keywords != "" {
			keywords = strings.Split(article.Keywords, ",")
		}

		// 获取文章摘要
		summary := article.Summary
		if summary == "" {
			summary = article.Content
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}
		}

		// 检查每个规则
		for _, rule := range rules {
			// 检查是否已推送过
			if appDB.HasFollowRuleNotification(rule.ID, article.ID) {
				continue
			}

			// 检查是否匹配
			ruleKeywords := strings.Split(rule.Keywords, ",")
			matched := false
			for _, rk := range ruleKeywords {
				rk = strings.ToLower(strings.TrimSpace(rk))
				if rk == "" {
					continue
				}
				// 检查关键词
				for _, ak := range keywords {
					if strings.ToLower(strings.TrimSpace(ak)) == rk {
						matched = true
						break
					}
				}
				if matched {
					break
				}
				// 检查标题
				if strings.Contains(strings.ToLower(article.Title), rk) {
					matched = true
					break
				}
			}

			if !matched {
				continue
			}

			// 发送推送
			channels := notify.ParseChannelsFromStr(rule.PushChannels)
			if len(channels) == 0 {
				continue
			}

			pushContent := fmt.Sprintf("%s\n\n[查看原文](%s)", summary, article.Link)

			msg := &notify.Message{
				Title:   fmt.Sprintf("【%s】%s", rule.Name, article.Title),
				Content: pushContent,
			}

			for _, channel := range channels {
				result := appNotifyMgr.Send(channel, msg)
				if !result.Success {
					log.Printf("Failed to push to %s for rule %s: %s", channel, rule.Name, result.Error)
				} else {
					log.Printf("Pushed article %d to %s for rule %s", article.ID, channel, rule.Name)
				}
			}

			// 记录已推送
			if err := appDB.CreateFollowRuleNotification(rule.ID, article.ID); err != nil {
				log.Printf("Failed to record notification: %v", err)
			}
		}
	}

	log.Printf("CheckAllArticlesFollowRules: completed")
}

// CheckFollowRulesPush 手动触发关注规则推送检查
func CheckFollowRulesPush(w http.ResponseWriter, r *http.Request) {
	go CheckAllArticlesFollowRules()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "开始检查关注规则推送",
	})
}

// checkAndNotifyFollowRules 检查关注规则并发送推送通知
func checkAndNotifyFollowRules(articleID int64, title, summary string, keywords []string, articleLink string) {
	if appDB == nil || appNotifyMgr == nil {
		return
	}

	// 获取所有启用推送的规则
	rules, err := appDB.GetActiveFollowRulesWithPush()
	if err != nil {
		log.Printf("Failed to get follow rules: %v", err)
		return
	}

	if len(rules) == 0 {
		return
	}

	// 将关键词转为 map 便于快速查找
	keywordMap := make(map[string]bool)
	for _, kw := range keywords {
		keywordMap[strings.ToLower(strings.TrimSpace(kw))] = true
	}

	// 检查每个规则
	for _, rule := range rules {
		// 检查是否已推送过
		if appDB.HasFollowRuleNotification(rule.ID, articleID) {
			continue
		}

		// 检查是否匹配（规则关键词与文章关键词有交集）
		ruleKeywords := strings.Split(rule.Keywords, ",")
		matched := false
		for _, rk := range ruleKeywords {
			rk = strings.ToLower(strings.TrimSpace(rk))
			if rk == "" {
				continue
			}
			// 检查关键词是否匹配
			if keywordMap[rk] {
				matched = true
				break
			}
			// 也检查标题中是否包含规则关键词
			if strings.Contains(strings.ToLower(title), rk) {
				matched = true
				break
			}
		}

		if !matched {
			continue
		}

		// 发送推送通知
		channels := notify.ParseChannelsFromStr(rule.PushChannels)
		if len(channels) == 0 {
			continue
		}

		pushContent := fmt.Sprintf("%s\n\n[查看原文](%s)", summary, articleLink)

		msg := &notify.Message{
			Title:   fmt.Sprintf("【%s】%s", rule.Name, title),
			Content: pushContent,
		}

		for _, channel := range channels {
			result := appNotifyMgr.Send(channel, msg)
			if !result.Success {
				log.Printf("Failed to push to %s for rule %s: %s", channel, rule.Name, result.Error)
			} else {
				log.Printf("Pushed article %d to %s for rule %s", articleID, channel, rule.Name)
			}
		}

		// 记录已推送
		if err := appDB.CreateFollowRuleNotification(rule.ID, articleID); err != nil {
			log.Printf("Failed to record notification: %v", err)
		}
	}
}

// RetryArticleProcess 手动重新处理一篇文章（重置失败计数后重新触发 AI 分析）
func RetryArticleProcess(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}
	if appAnalyzer == nil {
		http.Error(w, "AI analyzer not initialized", http.StatusServiceUnavailable)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid article id", http.StatusBadRequest)
		return
	}

	article, err := appDB.GetArticleByID(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get article: %v", err), http.StatusNotFound)
		return
	}

	// 重置失败计数与"处理失败"标记，重新进入处理队列
	if err := appDB.ResetArticleProcess(id); err != nil {
		http.Error(w, fmt.Sprintf("Failed to reset article: %v", err), http.StatusInternalServerError)
		return
	}

	// 异步重新分析
	text := article.Content
	if text == "" {
		text = article.Summary
	}
	if len(text) > 4000 {
		text = text[:4000]
	}
	go analyzeArticleAsync(id, article.Title, text, "")

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "已重新提交处理",
	})
}

// RetryFailedArticles 批量重新处理所有「处理失败」的文章
func RetryFailedArticles(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}
	if appAnalyzer == nil {
		http.Error(w, "AI analyzer not initialized", http.StatusServiceUnavailable)
		return
	}

	count, err := appDB.ResetFailedArticles()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to reset articles: %v", err), http.StatusInternalServerError)
		return
	}

	// 立即异步处理一轮重置后的文章（后续仍有 10 分钟定时任务兜底）
	go func() {
		articles, err := appDB.GetUnprocessedArticles(200)
		if err != nil {
			return
		}
		ProcessPendingArticlesInternal(articles)
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("已重置 %d 篇失败文章，正在后台重新分析", count),
		"count":   count,
	})
}

// ProcessPendingArticles 批量处理未处理的文章
func ProcessPendingArticles(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if appAnalyzer == nil {
		http.Error(w, "AI analyzer not initialized", http.StatusServiceUnavailable)
		return
	}

	// 获取未处理的文章
	articles, err := appDB.GetUnprocessedArticles(50)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get unprocessed articles: %v", err), http.StatusInternalServerError)
		return
	}

	if len(articles) == 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "没有待处理的文章",
			"count":   0,
		})
		return
	}

	// 异步处理文章
	go func() {
		// 从配置获取文章分析间隔
		articleInterval := 2 * time.Second
		if appConfig != nil {
			articleInterval = appConfig.AI.RateLimit.ArticleInterval
		}

		for i, article := range articles {
			// 每篇文章之间添加延迟
			if i > 0 {
				time.Sleep(articleInterval)
			}

			// 使用内容或摘要
			text := article.Content
			if text == "" {
				text = article.Summary
			}
			if text == "" {
				continue
			}

			// 限制内容长度
			if len(text) > 4000 {
				text = text[:4000]
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

			// AI 分析
			result, err := appAnalyzer.AnalyzeArticle(ctx, article.Title, text)
			if err != nil {
				fmt.Printf("Failed to analyze article %d: %v\n", article.ID, err)
				appDB.IncrementProcessAttempts(article.ID, err.Error())
				cancel()
				continue
			}

			// 更新文章 AI 分析结果（不修改分类，分类由 RSS feed 决定）
			keywords := strings.Join(result.Keywords, ",")
			tagsCache := strings.Join(result.Tags, ",")
			entities := strings.Join(result.Entities, ",")

			if err := appDB.UpdateArticleAI(&models.AIUpdateParams{
				ID:              article.ID,
				AISummary:       result.Summary,
				OneLineSummary:  result.OneLineSummary,
				Keywords:        keywords,
				TagsCache:       tagsCache,
				IsAd:            result.IsAd,
				AdReason:        result.AdReason,
				ImportanceScore: result.ImportanceScore,
				TopicCategory:   result.TopicCategory,
				Entities:        entities,
				TranslatedContent: result.TranslatedContent,
			}); err != nil {
				fmt.Printf("Failed to update article AI data %d: %v\n", article.ID, err)
			}

			// 生成总结向量
			if result.OneLineSummary != "" {
				summaryEmb, err := appAnalyzer.GetEmbedding(ctx, result.OneLineSummary)
				if err == nil {
					appDB.UpdateArticleSummaryEmbedding(article.ID, summaryEmb)
				}
			}

			// 处理标签：为每个关键词创建标签并关联文章
			for _, keyword := range result.Keywords {
				if keyword == "" {
					continue
				}
				var tagID int64
				existingTag, err := appDB.GetTagByName(keyword)
				if err == nil && existingTag != nil {
					tagID = existingTag.ID
					appDB.IncrementTagUsage(tagID)
				} else {
					newTag := &models.Tag{
						Name:       keyword,
						Color:      getRandomTagColor(),
						UsageCount: 1,
					}
					tagID, err = appDB.CreateTag(newTag)
					if err != nil {
						continue
					}
				}
				appDB.AddArticleTag(article.ID, tagID)
			}

			// 获取向量嵌入
			embeddingText := article.Title + " " + result.Summary
			embeddingBytes, err := appAnalyzer.GetEmbedding(ctx, embeddingText)
			if err != nil {
				fmt.Printf("Failed to get embedding for article %d: %v\n", article.ID, err)
				cancel()
				continue
			}

			if err := appDB.UpdateArticleEmbedding(article.ID, embeddingBytes); err != nil {
				fmt.Printf("Failed to update article embedding %d: %v\n", article.ID, err)
			}

			cancel()
			fmt.Printf("Processed article %d (%d/%d)\n", article.ID, i+1, len(articles))
		}

		fmt.Printf("Batch processing completed. Processed %d articles.\n", len(articles))
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("开始处理 %d 篇待处理文章", len(articles)),
		"count":   len(articles),
	})
}

// RegenerateSummaryEmbeddings 重新生成已有文章的总结向量
func RegenerateSummaryEmbeddings(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if appAnalyzer == nil {
		http.Error(w, "AI analyzer not initialized", http.StatusServiceUnavailable)
		return
	}

	// 获取需要重新生成向量的文章
	articles, err := appDB.GetArticlesMissingSummaryEmbedding(100)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get articles: %v", err), http.StatusInternalServerError)
		return
	}

	if len(articles) == 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "没有需要重新生成向量的文章",
			"count":   0,
		})
		return
	}

	// 异步处理
	go func() {
		embeddingInterval := 500 * time.Millisecond
		if appConfig != nil {
			embeddingInterval = appConfig.AI.RateLimit.EmbeddingInterval
		}

		successCount := 0
		for i, article := range articles {
			if i > 0 {
				time.Sleep(embeddingInterval)
			}

			// 使用 one_line_summary 生成向量
			if article.OneLineSummary == "" {
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			summaryEmb, err := appAnalyzer.GetEmbedding(ctx, article.OneLineSummary)
			if err != nil {
				fmt.Printf("Failed to get summary embedding for article %d: %v\n", article.ID, err)
				cancel()
				continue
			}
			cancel()

			if err := appDB.UpdateArticleSummaryEmbedding(article.ID, summaryEmb); err != nil {
				fmt.Printf("Failed to update summary embedding for article %d: %v\n", article.ID, err)
				continue
			}

			successCount++
		}

		fmt.Printf("Regenerated summary embeddings for %d/%d articles\n", successCount, len(articles))

		// 重新匹配事件
		if appEventMatcher != nil {
			updatedArticles, _ := appDB.GetArticlesMissingSummaryEmbedding(0) // 获取刚刚更新的文章
			for _, article := range updatedArticles {
				if len(article.SummaryEmbedding) > 0 {
					appEventMatcher.MatchArticleToEvents(article.ID, article.Title, article.Content, article.Embedding, article.SummaryEmbedding)
				}
			}
		}
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("开始为 %d 篇文章重新生成总结向量", len(articles)),
		"count":   len(articles),
	})
}

// ReanalyzeArticlesForSummary 重新分析处理不完整的文章（缺少 entities/one_line_summary/summary_embedding）
func ReanalyzeArticlesForSummary(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if appAnalyzer == nil {
		http.Error(w, "AI analyzer not initialized", http.StatusServiceUnavailable)
		return
	}

	// 获取处理不完整的文章（缺少 entities/one_line_summary/summary_embedding，排除广告）
	articles, err := appDB.GetArticlesIncomplete(50)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get articles: %v", err), http.StatusInternalServerError)
		return
	}

	if len(articles) == 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "没有需要重新分析的文章",
			"count":   0,
		})
		return
	}

	// 异步处理
	go func() {
		articleInterval := 2 * time.Second
		if appConfig != nil {
			articleInterval = appConfig.AI.RateLimit.ArticleInterval
		}

		successCount := 0
		for i, article := range articles {
			if i > 0 {
				time.Sleep(articleInterval)
			}

			// 使用内容或摘要
			text := article.Content
			if text == "" {
				text = article.Summary
			}
			if text == "" {
				continue
			}

			// 限制内容长度
			if len(text) > 4000 {
				text = text[:4000]
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

			// 检查文章缺少哪些字段
			needsReanalyze := article.OneLineSummary == "" || article.Entities == ""
			needsEmbedding := len(article.SummaryEmbedding) == 0 && article.OneLineSummary != ""

			// 如果只需要生成向量
			if needsEmbedding && !needsReanalyze && article.OneLineSummary != "" {
				summaryEmb, err := appAnalyzer.GetEmbedding(ctx, article.OneLineSummary)
				if err == nil {
					appDB.UpdateArticleSummaryEmbedding(article.ID, summaryEmb)
					fmt.Printf("Generated summary embedding for article %d (%d/%d)\n", article.ID, i+1, len(articles))
				}
				cancel()
				successCount++
				continue
			}

			// 需要重新 AI 分析
			result, err := appAnalyzer.AnalyzeArticle(ctx, article.Title, text)
			if err != nil {
				fmt.Printf("Failed to reanalyze article %d: %v\n", article.ID, err)
				cancel()
				continue
			}

			// 更新文章 AI 分析结果
			keywords := strings.Join(result.Keywords, ",")
			tagsCache := strings.Join(result.Tags, ",")
			entities := strings.Join(result.Entities, ",")

			if err := appDB.UpdateArticleAI(&models.AIUpdateParams{
				ID:              article.ID,
				AISummary:       result.Summary,
				OneLineSummary:  result.OneLineSummary,
				Keywords:        keywords,
				TagsCache:       tagsCache,
				IsAd:            result.IsAd,
				AdReason:        result.AdReason,
				ImportanceScore: result.ImportanceScore,
				TopicCategory:   result.TopicCategory,
				Entities:        entities,
				TranslatedContent: result.TranslatedContent,
			}); err != nil {
				fmt.Printf("Failed to update article AI data %d: %v\n", article.ID, err)
				cancel()
				continue
			}

			// 生成总结向量
			if result.OneLineSummary != "" {
				summaryEmb, err := appAnalyzer.GetEmbedding(ctx, result.OneLineSummary)
				if err == nil {
					appDB.UpdateArticleSummaryEmbedding(article.ID, summaryEmb)
				}
			}

			cancel()
			successCount++
			fmt.Printf("Reanalyzed article %d (%d/%d)\n", article.ID, i+1, len(articles))
		}

		fmt.Printf("Reanalysis completed. Processed %d/%d articles\n", successCount, len(articles))
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("开始重新分析 %d 篇文章", len(articles)),
		"count":   len(articles),
	})
}

// ListArticles 列出文章 (JSON API)
func ListArticles(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 解析分页参数
	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
			if limit > 100 {
				limit = 100
			}
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// 解析筛选参数
	feedIDStr := r.URL.Query().Get("feed_id")
	categoryIDStr := r.URL.Query().Get("category_id")
	hideAds := r.URL.Query().Get("hide_ads") == "true" || r.URL.Query().Get("hide_ads") == "on"
	searchQuery := r.URL.Query().Get("q")
	keyword := r.URL.Query().Get("tag")
	isReadStr := r.URL.Query().Get("is_read")

	// 加载 feeds 用于显示标题
	feeds, _ := appDB.ListFeeds()
	feedMap := make(map[int64]string)
	for _, f := range feeds {
		feedMap[f.ID] = f.Title
	}

	// 构建查询参数
	params := database.ArticleQueryParams{
		HideAds: hideAds,
		Search:  searchQuery,
		Keyword: keyword,
		Limit:   limit,
		Offset:  offset,
	}

	if feedIDStr != "" {
		fid, err := strconv.ParseInt(feedIDStr, 10, 64)
		if err == nil {
			params.FeedID = &fid
		}
	}
	if categoryIDStr != "" {
		cid, err := strconv.ParseInt(categoryIDStr, 10, 64)
		if err == nil {
			params.CategoryID = &cid
		}
	}
	if isReadStr != "" {
		if isReadStr == "true" {
			v := true
			params.IsRead = &v
		} else if isReadStr == "false" {
			v := false
			params.IsRead = &v
		}
	}

	articles, err := appDB.QueryArticles(params)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list articles: %v", err), http.StatusInternalServerError)
		return
	}

	// 构建返回数据，添加 feed_title
	type ArticleResponse struct {
		ID            int64   `json:"id"`
		FeedID        int64   `json:"feed_id"`
		FeedTitle     string  `json:"feed_title"`
		CategoryID    *int64  `json:"category_id"`
		Title         string  `json:"title"`
		Link          string  `json:"link"`
		Summary       string  `json:"summary"`
		AISummary     string  `json:"ai_summary"`
		Keywords      string  `json:"keywords"`
		IsAd          bool    `json:"is_ad"`
		IsRead        bool    `json:"is_read"`
		FetchedAt     string  `json:"fetched_at"`
		PublishedAt   string  `json:"published_at"`
	}

	var response []ArticleResponse
	if len(articles) == 0 {
		response = []ArticleResponse{} // 返回空数组而不是 null
	}
	for _, a := range articles {
		fetchedAt := formatTimeInTimezone(a.FetchedAt)
		// 优先使用发布时间，没有则使用抓取时间
		publishedAt := formatTimePtrInTimezone(a.PublishedAt)
		if publishedAt == "" {
			publishedAt = fetchedAt
		}
		response = append(response, ArticleResponse{
			ID:          a.ID,
			FeedID:      a.FeedID,
			FeedTitle:   feedMap[a.FeedID],
			CategoryID:  a.CategoryID,
			Title:       a.Title,
			Link:        a.Link,
			Summary:     a.Summary,
			AISummary:   a.AISummary,
			Keywords:    a.Keywords,
			IsAd:        a.IsAd,
			IsRead:      a.IsRead,
			FetchedAt:   fetchedAt,
			PublishedAt: publishedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// ListArticlesHTML 返回文章列表 HTML 片段（用于 HTMX 筛选）
func ListArticlesHTML(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 解析筛选参数
	feedIDStr := r.URL.Query().Get("feed_id")
	categoryIDStr := r.URL.Query().Get("category_id")
	hideAds := r.URL.Query().Get("hide_ads") == "true" || r.URL.Query().Get("hide_ads") == "on"
	searchQuery := r.URL.Query().Get("q")

	// 加载 feeds 用于显示标题
	feeds, _ := appDB.ListFeeds()
	feedMap := make(map[int64]string)
	for _, f := range feeds {
		feedMap[f.ID] = f.Title
	}

	// 构建查询参数
	params := database.ArticleQueryParams{
		HideAds: hideAds,
		Search: searchQuery,
		Limit:  100,
		Offset: 0,
	}

	if feedIDStr != "" {
		fid, err := strconv.ParseInt(feedIDStr, 10, 64)
		if err == nil {
			params.FeedID = &fid
		}
	}
	if categoryIDStr != "" {
		cid, err := strconv.ParseInt(categoryIDStr, 10, 64)
		if err == nil {
			params.CategoryID = &cid
		}
	}

	// 使用条件查询
	articles, err := appDB.QueryArticles(params)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list articles: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	hasArticles := false
	for _, a := range articles {
		hasArticles = true
		fetchedAt := formatTimeInTimezone(a.FetchedAt)
		feedTitle := feedMap[a.FeedID]

		// 优先显示 AI 摘要，如果没有则显示原始摘要
		displaySummary := a.AISummary
		if displaySummary == "" {
			displaySummary = a.Summary
		}

		fmt.Fprintf(w, `
<article class="bg-white rounded-lg shadow-sm hover:shadow-md transition cursor-pointer border border-transparent hover:border-blue-200"
         data-article-id="%d"
         onclick="selectArticle(%d, this)">
    <div class="p-4">
        <div class="flex items-center gap-2 mb-1">
            <span class="text-xs bg-blue-100 text-blue-700 px-2 py-0.5 rounded">%s</span>
            %s
        </div>
        <h2 class="text-sm font-semibold text-gray-900 mb-1 line-clamp-2">%s</h2>
        <p class="text-gray-500 text-xs line-clamp-2 mb-2">%s</p>
        <div class="flex items-center gap-3 text-xs text-gray-400">
            <span>📅 %s</span>
        </div>
    </div>
</article>
`, a.ID, a.ID, feedTitle,
			func() string {
				if a.IsAd {
					return `<span class="text-xs bg-red-100 text-red-700 px-2 py-0.5 rounded">广告</span>`
				}
				return ""
			}(),
			a.Title, displaySummary, fetchedAt)
	}

	if !hasArticles {
		fmt.Fprint(w, `
<div class="bg-white rounded-xl shadow-sm p-12 text-center text-gray-500">
    <div class="text-4xl mb-4">📭</div>
    <p>暂无文章</p>
</div>
`)
	}
}

// GetArticle 获取单篇文章
func GetArticle(w http.ResponseWriter, r *http.Request) {
	articleID := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	id, err := strconv.ParseInt(articleID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid article ID", http.StatusBadRequest)
		return
	}

	article, err := appDB.GetArticleByID(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Article not found: %v", err), http.StatusNotFound)
		return
	}

	// 获取 Feed 标题
	feedTitle := ""
	if feed, err := appDB.GetFeedByID(article.FeedID); err == nil {
		feedTitle = feed.Title
	}

	// 返回 JSON 格式
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	publishedAt := ""
	if article.PublishedAt != nil && !article.PublishedAt.IsZero() {
		publishedAt = formatTimePtrInTimezone(article.PublishedAt)
	}

	response := map[string]interface{}{
		"id":              article.ID,
		"title":           article.Title,
		"link":            article.Link,
		"content":         article.Content,
		"content_cleaned": article.ContentCleaned,
		"summary":         article.Summary,
		"ai_summary":      article.AISummary,
		"keywords":        article.Keywords,
		"feed_title":      feedTitle,
		"published_at":    publishedAt,
		"is_ad":           article.IsAd,
		"ad_reason":         article.AdReason,
		"translated_content": article.TranslatedContent,
	}

	json.NewEncoder(w).Encode(response)
}

// MarkArticleRead 标记文章为已读
func MarkArticleRead(w http.ResponseWriter, r *http.Request) {
	articleID := chi.URLParam(r, "id")
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}
	id, err := strconv.ParseInt(articleID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid article ID", http.StatusBadRequest)
		return
	}
	if err := appDB.MarkArticleRead(id); err != nil {
		http.Error(w, fmt.Sprintf("Failed to mark article as read: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// FetchOriginalContent 从文章原始链接获取完整内容
func FetchOriginalContent(w http.ResponseWriter, r *http.Request) {
	articleID := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	id, err := strconv.ParseInt(articleID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid article ID", http.StatusBadRequest)
		return
	}

	article, err := appDB.GetArticleByID(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Article not found: %v", err), http.StatusNotFound)
		return
	}

	if article.Link == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "文章没有原始链接",
		})
		return
	}

	// 使用 readability 获取原文（经 crawler.HTTPClient，跟随代理配置）
	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	if appConfig != nil && appConfig.Feeds.FetchUserAgent != "" {
		userAgent = appConfig.Feeds.FetchUserAgent
	}
	fetchReq, err := http.NewRequest("GET", article.Link, nil)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("创建请求失败: %v", err),
		})
		return
	}
	fetchReq.Header.Set("User-Agent", userAgent)
	fetchResp, err := crawler.HTTPClient.Do(fetchReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("获取原文失败: %v", err),
		})
		return
	}
	defer fetchResp.Body.Close()
	art, err := readability.FromReader(fetchResp.Body, fetchReq.URL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("获取原文失败: %v", err),
		})
		return
	}

	// 保存到 content 字段
	if err := appDB.UpdateArticleContent(id, art.Content); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("保存失败: %v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"content": art.Content,
		"title":   art.Title,
	})
}

// ListCategories 列出分类
func ListCategories(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	categories, err := appDB.ListCategories()
	if err != nil {
		http.Error(w, "Failed to list categories", http.StatusInternalServerError)
		return
	}

	// 转换为前端期望的格式，将 Color 映射为 Icon
	result := make([]map[string]interface{}, len(categories))
	for i, cat := range categories {
		result[i] = map[string]interface{}{
			"id":          cat.ID,
			"name":        cat.Name,
			"description": cat.Description,
			"icon":        cat.Color, // 前端使用 icon
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

// CreateCategory 创建分类
func CreateCategory(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	var req CreateCategoryRequest

	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		// 表单数据
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}
		req.Name = r.FormValue("name")
		req.Description = r.FormValue("description")
		req.Color = r.FormValue("icon")
	}

	// 验证必填字段
	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	// 处理 icon/color 字段
	color := req.Icon
	if color == "" {
		color = req.Color
	}

	// 保存到数据库
	if appDB != nil {
		category := &models.Category{
			Name:        req.Name,
			Description: req.Description,
			Color:       color,
			ContentType: req.ContentType,
		}
		id, err := appDB.CreateCategory(category)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				http.Error(w, "Category name already exists", http.StatusConflict)
				return
			}
			http.Error(w, "Failed to create category: "+err.Error(), http.StatusInternalServerError)
			return
		}
		category.ID = id

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Category created successfully",
			"data":    category,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

// ListTags 列出标签（支持分页和搜索）
func ListTags(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 解析分页参数
	page := 1
	pageSize := 20
	sortBy := "count" // count, name, recent
	search := ""

	if p := r.URL.Query().Get("page"); p != "" {
		if val, err := strconv.Atoi(p); err == nil && val > 0 {
			page = val
		}
	}
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if val, err := strconv.Atoi(ps); err == nil && val > 0 && val <= 100 {
			pageSize = val
		}
	}
	if s := r.URL.Query().Get("sort"); s != "" {
		switch s {
		case "name", "recent", "count":
			sortBy = s
		}
	}
	search = r.URL.Query().Get("search")

	offset := (page - 1) * pageSize

	var tags []*models.Tag
	var total int
	var err error

	if search != "" {
		// 搜索模式
		tags, total, err = appDB.SearchTags(search, pageSize, offset)
	} else {
		// 正常分页
		tags, err = appDB.ListTagsPaginated(pageSize, offset, sortBy)
		if err == nil {
			total, err = appDB.CountTags()
		}
	}

	if err != nil {
		http.Error(w, "Failed to list tags", http.StatusInternalServerError)
		return
	}

	// 返回分页响应
	response := map[string]interface{}{
		"tags":       tags,
		"page":       page,
		"page_size":  pageSize,
		"total":      total,
		"total_page": (total + pageSize - 1) / pageSize,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(response)
}

// ListFollowRules 列出关注规则
func ListFollowRules(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode([]interface{}{})
}

// CreateFollowRule 创建关注规则
func CreateFollowRule(w http.ResponseWriter, r *http.Request) {
	var req CreateFollowRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

// reportGenerator 全局报告生成器
var reportGenerator *processor.ReportGenerator

// SetReportGenerator 设置报告生成器
func SetReportGenerator(rg *processor.ReportGenerator) {
	reportGenerator = rg
}

// ListReports 列出报告
func ListReports(w http.ResponseWriter, r *http.Request) {
	reportType := r.URL.Query().Get("type")
	limit := 20
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil {
			limit = val
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil {
			offset = val
		}
	}

	reports, err := appDB.ListReportsWithFilter(reportType, limit, offset)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(reports)
}

// GetLatestReport 获取最新报告（完整版页面）
func GetLatestReport(w http.ResponseWriter, r *http.Request) {
	// 获取最新报告
	reports, err := appDB.ListReportsWithFilter("", 1, 0)
	if err != nil || len(reports) == 0 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>暂无报告</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
        .empty { text-align: center; color: #6b7280; }
    </style>
</head>
<body>
    <div class="empty">
        <h1>📭 暂无报告</h1>
        <p>请先生成报告</p>
    </div>
</body>
</html>`)
		return
	}

	report := reports[0]

	// 返回简报版 HTML 页面
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// 渲染 Markdown 内容为 HTML（使用简报 Summary）
	displayContent := report.Summary
	if displayContent == "" {
		displayContent = report.Content
	}
	// 使用更完整的扩展集，确保列表等元素正确渲染
	extensions := blackfriday.CommonExtensions | blackfriday.NoEmptyLineBeforeBlock
	htmlContent := string(blackfriday.Run([]byte(displayContent), blackfriday.WithExtensions(extensions)))

	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            background: #f5f5f5;
        }
        .container { max-width: 800px; margin: 0 auto; padding: 20px; }
        .report {
            background: white;
            border-radius: 12px;
            padding: 30px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
        }
        h1 { font-size: 1.8rem; margin-bottom: 1rem; color: #1f2937; }
        h2 { font-size: 1.4rem; margin: 1.5rem 0 0.75rem; color: #374151; border-bottom: 2px solid #e5e7eb; padding-bottom: 0.5rem; }
        h3 { font-size: 1.2rem; margin: 1rem 0 0.5rem; color: #4b5563; }
        h4 { font-size: 1rem; margin: 0.75rem 0 0.25rem; color: #6b7280; }
        p { margin: 0.75rem 0; color: #4b5563; }
        a { color: #2563eb; text-decoration: none; }
        a:hover { text-decoration: underline; }
        strong { font-weight: 600; color: #1f2937; }
        hr { margin: 1.5rem 0; border: none; border-top: 1px solid #e5e7eb; }
        blockquote {
            border-left: 4px solid #d1d5db;
            padding-left: 1rem;
            margin: 1rem 0;
            color: #6b7280;
            font-style: italic;
        }
        ul, ol { padding-left: 1.5rem; margin: 0.5rem 0; list-style-position: outside; }
        ul { list-style-type: disc; }
        ol { list-style-type: decimal; }
        li { margin: 0.25rem 0; display: list-item; }
        .footer {
            text-align: center;
            margin-top: 2rem;
            padding-top: 1rem;
            border-top: 1px solid #e5e7eb;
            color: #9ca3af;
            font-size: 0.875rem;
        }
        .timestamp {
            display: inline-block;
            background: #dbeafe;
            color: #1d4ed8;
            padding: 0.125rem 0.5rem;
            border-radius: 4px;
            font-size: 0.875rem;
            font-weight: 500;
            margin-right: 0.5rem;
        }
        .featured { margin-bottom: 1.5rem; }
    </style>
</head>
<body>
    <div class="container">
        <div class="report">
            %s
            <div class="footer">
                *本报告由 RSS AI 自动生成*
            </div>
        </div>
    </div>
</body>
</html>`, report.Name, htmlContent)
}

// GetReport 获取报告详情
func GetReport(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid report id"})
		return
	}

	report, articles, err := appDB.GetReportWithArticles(id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "report not found"})
		return
	}

	// 检查是否是 HTMX 请求，返回 HTML
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		// 渲染 Markdown 内容为 HTML（使用简报 Summary 而非完整版 Content）
		displayContent := report.Summary
		if displayContent == "" {
			displayContent = report.Content
		}
		// 使用更完整的扩展集，确保列表等元素正确渲染
		extensions := blackfriday.CommonExtensions | blackfriday.NoEmptyLineBeforeBlock
		htmlContent := string(blackfriday.Run([]byte(displayContent), blackfriday.WithExtensions(extensions)))

		// 渲染报告内容，使用内联样式确保 Markdown 正确渲染
		fmt.Fprintf(w, `<style>
			.report-content h1 { font-size: 1.5rem; font-weight: bold; margin: 1rem 0 0.5rem; color: #1f2937; }
			.report-content h2 { font-size: 1.25rem; font-weight: bold; margin: 1rem 0 0.5rem; color: #374151; }
			.report-content h3 { font-size: 1.1rem; font-weight: 600; margin: 0.75rem 0 0.25rem; color: #4b5563; }
			.report-content p { margin: 0.5rem 0; color: #4b5563; line-height: 1.6; }
			.report-content a { color: #2563eb; text-decoration: underline; }
			.report-content a:hover { color: #1d4ed8; }
			.report-content strong { font-weight: 600; color: #1f2937; }
			.report-content hr { margin: 1.5rem 0; border-color: #e5e7eb; }
			.report-content ul, .report-content ol { padding-left: 1.5rem; margin: 0.5rem 0; list-style-position: outside; }
			.report-content ul { list-style-type: disc; }
			.report-content ol { list-style-type: decimal; }
			.report-content li { margin: 0.25rem 0; display: list-item; }
		</style>
		<div class="space-y-4">
			<div class="flex items-center justify-between">
				<h2 class="text-xl font-bold text-gray-800">%s</h2>
				<span class="text-sm text-gray-500">%s</span>
			</div>
			<div class="bg-gray-50 rounded-lg p-4 max-h-96 overflow-y-auto">
				<div class="report-content">
					%s
				</div>
			</div>`, report.Name, formatTimeInTimezone(report.CreatedAt), htmlContent)

		// 渲染文章列表
		if len(articles) > 0 {
			fmt.Fprintf(w, `<div class="mt-6">
				<h3 class="text-lg font-semibold text-gray-800 mb-3">包含文章 (%d)</h3>
				<div class="space-y-2">`, len(articles))
			for _, article := range articles {
				fmt.Fprintf(w, `<div class="p-3 bg-white border border-gray-200 rounded-lg hover:bg-gray-50">
					<a href="%s" target="_blank" class="text-blue-600 hover:underline font-medium">%s</a>
					<p class="text-sm text-gray-500 mt-1 line-clamp-2">%s</p>
				</div>`, article.Link, article.Title, article.Summary)
			}
			fmt.Fprintf(w, `</div></div>`)
		}

		fmt.Fprintf(w, `</div>`)
		return
	}

	// 非 HTMX 请求返回 JSON
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"report":   report,
		"articles": articles,
	})
}

// GetFullReport 获取完整报告页面
func GetFullReport(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid report id"})
		return
	}

	report, _, err := appDB.GetReportWithArticles(id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "report not found"})
		return
	}

	// 使用更完整的扩展集
	extensions := blackfriday.CommonExtensions | blackfriday.NoEmptyLineBeforeBlock
	htmlContent := string(blackfriday.Run([]byte(report.Content), blackfriday.WithExtensions(extensions)))

	// 返回完整报告的 HTML 页面
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            line-height: 1.6;
            color: #333;
            background: #f5f5f5;
            padding: 20px;
        }
        .container { max-width: 900px; margin: 0 auto; }
        .report {
            background: white;
            border-radius: 12px;
            padding: 30px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
        }
        h1 { font-size: 1.8rem; margin-bottom: 1rem; color: #1f2937; }
        h2 { font-size: 1.4rem; margin: 1.5rem 0 0.75rem; color: #374151; border-bottom: 2px solid #e5e7eb; padding-bottom: 0.5rem; }
        h3 { font-size: 1.2rem; margin: 1rem 0 0.5rem; color: #4b5563; }
        h4 { font-size: 1rem; margin: 0.75rem 0 0.25rem; color: #6b7280; }
        p { margin: 0.75rem 0; color: #4b5563; }
        a { color: #2563eb; text-decoration: none; }
        a:hover { color: #1d4ed8; }
        ul { list-style-type: disc; padding-left: 1.5rem; margin: 0.5rem 0; list-style-position: outside; }
        ol { list-style-type: decimal; padding-left: 1.5rem; margin: 0.5rem 0; list-style-position: outside; }
        li { margin: 0.25rem 0; color: #4b5563; display: list-item; }
        strong { font-weight: 600; color: #1f2937; }
        blockquote { border-left: 3px solid #d1d5db; padding-left: 1rem; margin: 0.5rem 0; color: #6b7280; }
        hr { border-color: #e5e7eb; margin: 1rem 0; }
    </style>
</head>
<body>
    <div class="container">
        <div class="report">
            %s
        </div>
    </div>
</body>
</html>`, report.Name, htmlContent)
}

// GenerateReport 生成报告
func GenerateReport(w http.ResponseWriter, r *http.Request) {
	if reportGenerator == nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "report generator not initialized"})
		return
	}

	reportType := r.FormValue("type")
	if reportType == "" {
		reportType = r.URL.Query().Get("type")
	}
	if reportType == "" {
		reportType = "morning"
	}

	// 立即返回响应，让用户知道任务已提交
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "报告生成任务已提交，正在后台处理",
		"type":    reportType,
	})

	// 在后台 goroutine 中执行生成任务
	go func() {
		log.Printf("开始后台生成 %s 报告...", reportType)
		report, err := reportGenerator.Generate(reportType)
		if err != nil {
			log.Printf("后台生成 %s 报告失败: %v", reportType, err)
			return
		}
		if report != nil {
			log.Printf("后台生成 %s 报告成功: ID=%d, 名称=%s", reportType, report.ID, report.Name)
		}
	}()
}



// SearchArticles 搜索文章
func SearchArticles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"query": query})
}

// SaveSettings 保存设置
func SaveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`<div class="p-4 bg-red-100 text-red-700 rounded-lg">❌ 无效的表单数据</div>`))
		return
	}

	// 更新内存中的配置
	if appConfig != nil {
		// AI 配置
		appConfig.AI.LLM.BaseURL = r.FormValue("llm_base_url")
		appConfig.AI.LLM.Model = r.FormValue("llm_model")
		appConfig.AI.LLM.APIKey = r.FormValue("llm_api_key")
		appConfig.AI.Embedding.BaseURL = r.FormValue("embedding_base_url")
		appConfig.AI.Embedding.Model = r.FormValue("embedding_model")
		appConfig.AI.Embedding.APIKey = r.FormValue("embedding_api_key")

		// 推送配置
		appConfig.Push.Gotify.URL = r.FormValue("gotify_url")
		appConfig.Push.Gotify.AppToken = r.FormValue("gotify_token")
		appConfig.Push.Email.SMTPHost = r.FormValue("smtp_host")
		if port := r.FormValue("smtp_port"); port != "" {
			appConfig.Push.Email.SMTPPort = parseInt(port, 587)
		}
		appConfig.Push.Email.Username = r.FormValue("smtp_username")
		appConfig.Push.Email.Password = r.FormValue("smtp_password")
		appConfig.Push.Webhook.URL = r.FormValue("webhook_url")
		// QQ Bot 配置
		appConfig.Push.QQBot.AppID = r.FormValue("qqbot_app_id")
		appConfig.Push.QQBot.AppSecret = r.FormValue("qqbot_app_secret")
		appConfig.Push.QQBot.UserID = r.FormValue("qqbot_user_id")

		// 代理配置
		appConfig.Proxy.URL = strings.TrimSpace(r.FormValue("proxy_url"))
		appConfig.Proxy.EnableContent = r.FormValue("proxy_enable_content") == "on"
		appConfig.Proxy.EnableLLM = r.FormValue("proxy_enable_llm") == "on"
		applyProxyConfig()

		// 定时任务
		appConfig.Scheduler.MorningReportTime = r.FormValue("morning_report_time")
		appConfig.Scheduler.EveningReportTime = r.FormValue("evening_report_time")
		appConfig.Scheduler.DailyReportTime = r.FormValue("daily_report_time")

		// 登录密码（留空则不启用登录校验；修改后旧会话立即失效）
		appConfig.Server.Password = r.FormValue("server_password")

		// 保存到配置文件
		if err := saveConfigToFile(); err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<div class="p-4 bg-red-100 text-red-700 rounded-lg">❌ 保存配置文件失败: ` + err.Error() + `</div>`))
			return
		}
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<div class="p-4 bg-green-100 text-green-700 rounded-lg">✅ 设置已保存成功！</div>`))
}

// TestSettingsConnection 测试 LLM / Embedding / 推送通道连接
// 使用表单当前值构造临时客户端，无需先保存设置
func TestSettingsConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		// AI 配置
		LLMBaseURL       string `json:"llm_base_url"`
		LLMModel         string `json:"llm_model"`
		LLMAPIKey        string `json:"llm_api_key"`
		EmbeddingBaseURL string `json:"embedding_base_url"`
		EmbeddingModel   string `json:"embedding_model"`
		EmbeddingAPIKey  string `json:"embedding_api_key"`
		// Gotify
		GotifyURL    string `json:"gotify_url"`
		GotifyToken  string `json:"gotify_token"`
		// Email
		SMTPHost     string `json:"smtp_host"`
		SMTPPort     string `json:"smtp_port"`
		SMTPUsername string `json:"smtp_username"`
		SMTPPassword string `json:"smtp_password"`
		// QQ Bot
		QQBotAppID     string `json:"qqbot_app_id"`
		QQBotAppSecret string `json:"qqbot_app_secret"`
		QQBotUserID    string `json:"qqbot_user_id"`
		// Webhook
		WebhookURL string `json:"webhook_url"`
		// Proxy
		ProxyURL string `json:"proxy_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeTestJSON(w, http.StatusBadRequest, false, "无效的请求数据: "+err.Error(), 0)
		return
	}

	start := time.Now()

	respond := func(ok bool, msg string) {
		writeTestJSON(w, http.StatusOK, ok, msg, time.Since(start).Milliseconds())
	}

	switch req.Type {
	case "llm":
		if req.LLMBaseURL == "" || req.LLMModel == "" || req.LLMAPIKey == "" {
			respond(false, "请先填写 LLM API 地址、模型和 API Key")
			return
		}
		client := ai.NewLLMClient(&config.LLMConfig{
			BaseURL: strings.TrimRight(req.LLMBaseURL, "/"),
			APIKey:  req.LLMAPIKey,
			Model:   req.LLMModel,
			Timeout: 30 * time.Second,
		})
		ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		reply, err := client.Chat(ctx, "连接测试。请只回复两个字：成功")
		if err != nil {
			respond(false, "LLM 调用失败: "+err.Error())
			return
		}
		if len(reply) > 100 {
			reply = reply[:100] + "…"
		}
		respond(true, fmt.Sprintf("模型 %s 连接成功，回复：%s", req.LLMModel, reply))

	case "embedding":
		if req.EmbeddingBaseURL == "" || req.EmbeddingModel == "" || req.EmbeddingAPIKey == "" {
			respond(false, "请先填写 Embedding API 地址、模型和 API Key")
			return
		}
		client := ai.NewEmbeddingClient(&config.EmbeddingConfig{
			BaseURL: strings.TrimRight(req.EmbeddingBaseURL, "/"),
			APIKey:  req.EmbeddingAPIKey,
			Model:   req.EmbeddingModel,
			Timeout: 20 * time.Second,
		})
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		vec, err := client.GetEmbedding(ctx, "连接测试")
		if err != nil {
			respond(false, "Embedding 调用失败: "+err.Error())
			return
		}
		respond(true, fmt.Sprintf("模型 %s 连接成功，返回 %d 维向量", req.EmbeddingModel, len(vec)))

	case "gotify":
		if req.GotifyURL == "" || req.GotifyToken == "" {
			respond(false, "请先填写 Gotify URL 和应用 Token")
			return
		}
		result := notify.NewGotifySender(&notify.GotifyConfig{URL: req.GotifyURL, AppToken: req.GotifyToken}).
			Send(&notify.Message{Title: "RSS AI Reader 连接测试", Content: "✅ 收到这条消息说明 Gotify 推送配置正确"})
		respondPushResult(result, "Gotify", respond)

	case "email":
		if req.SMTPHost == "" || req.SMTPUsername == "" || req.SMTPPassword == "" {
			respond(false, "请先填写 SMTP 服务器、发件人邮箱和密码")
			return
		}
		// From / To 不在设置表单中，从已加载配置取
		emailFrom, emailTo := "", ""
		if appConfig != nil {
			emailFrom = appConfig.Push.Email.From
			emailTo = appConfig.Push.Email.To
		}
		if emailFrom == "" || emailTo == "" {
			respond(false, "缺少发件人（from）或收件人（to），请在 config.yaml 的 email 段配置后再测试")
			return
		}
		port := parseInt(req.SMTPPort, 587)
		result := notify.NewEmailSender(&notify.EmailConfig{
			SMTPHost: req.SMTPHost,
			SMTPPort: port,
			Username: req.SMTPUsername,
			Password: req.SMTPPassword,
			From:     emailFrom,
			To:       emailTo,
		}).Send(&notify.Message{Title: "RSS AI Reader 连接测试", Content: "✅ 收到这封邮件说明 SMTP 配置正确"})
		respondPushResult(result, "邮件（发送到 "+emailTo+"）", respond)

	case "qqbot":
		if req.QQBotAppID == "" || req.QQBotAppSecret == "" || req.QQBotUserID == "" {
			respond(false, "请先填写 QQ Bot 的 App ID、App Secret 和接收用户 ID")
			return
		}
		result := notify.NewQQBotSender(&notify.QQBotConfig{
			AppID:     req.QQBotAppID,
			AppSecret: req.QQBotAppSecret,
			UserID:    req.QQBotUserID,
		}).Send(&notify.Message{Title: "RSS AI Reader 连接测试", Content: "✅ 收到这条消息说明 QQ Bot 配置正确"})
		respondPushResult(result, "QQ Bot", respond)

	case "webhook":
		if req.WebhookURL == "" {
			respond(false, "请先填写 Webhook URL")
			return
		}
		result := notify.NewWebhookSender(&notify.WebhookConfig{URL: req.WebhookURL}).
			Send(&notify.Message{Title: "RSS AI Reader 连接测试", Content: "✅ 收到这条请求说明 Webhook 配置正确"})
		respondPushResult(result, "Webhook", respond)

	case "proxy":
		if req.ProxyURL == "" {
			respond(false, "请先填写代理地址（支持 http:// 或 socks5://）")
			return
		}
		client := proxyutil.NewClient(req.ProxyURL, 15*time.Second)
		probeResp, err := client.Get("https://www.gstatic.com/generate_204")
		if err != nil {
			respond(false, "代理连接失败: "+err.Error())
			return
		}
		probeResp.Body.Close()
		if probeResp.StatusCode != 204 && probeResp.StatusCode != 200 {
			respond(false, fmt.Sprintf("代理可用但探测返回异常状态 %d", probeResp.StatusCode))
			return
		}
		respond(true, fmt.Sprintf("代理 %s 可用（探测返回 %d）", req.ProxyURL, probeResp.StatusCode))

	default:
		writeTestJSON(w, http.StatusBadRequest, false, "未知的测试类型: "+req.Type, 0)
	}
}

// respondPushResult 将推送发送结果转为测试响应
func respondPushResult(result *notify.Result, channel string, respond func(bool, string)) {
	if result != nil && result.Success {
		respond(true, channel+" 测试消息发送成功")
		return
	}
	errMsg := "未知错误"
	if result != nil && result.Error != "" {
		errMsg = result.Error
	}
	respond(false, channel+" 测试失败: "+errMsg)
}

// writeTestJSON 输出测试结果
func writeTestJSON(w http.ResponseWriter, status int, ok bool, msg string, latencyMs int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    ok,
		"message":    msg,
		"latency_ms": latencyMs,
	})
}

// parseInt 辅助函数
func parseInt(s string, defaultVal int) int {
	var val int
	if _, err := fmt.Sscanf(s, "%d", &val); err != nil {
		return defaultVal
	}
	return val
}

// parseFloat 辅助函数
func parseFloat(s string) (float64, error) {
	var val float64
	_, err := fmt.Sscanf(s, "%f", &val)
	return val, err
}

// tagColors 预定义的标签颜色
var tagColors = []string{
	"#3b82f6", // blue
	"#10b981", // emerald
	"#f59e0b", // amber
	"#ef4444", // red
	"#8b5cf6", // violet
	"#ec4899", // pink
	"#6366f1", // indigo
	"#14b8a6", // teal
}

// getRandomTagColor 获取随机标签颜色
func getRandomTagColor() string {
	return tagColors[time.Now().UnixNano()%int64(len(tagColors))]
}

// configFilePath 配置文件路径
var configFilePath string

// SetConfigPath 设置配置文件路径
func SetConfigPath(path string) {
	configFilePath = path
}

// saveConfigToFile 保存配置到文件
func saveConfigToFile() error {
	if configFilePath == "" || appConfig == nil {
		return fmt.Errorf("config not initialized")
	}

	// 读取原始配置文件
	originalContent, err := os.ReadFile(configFilePath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// 将原始内容按行分割
	lines := strings.Split(string(originalContent), "\n")
	var result strings.Builder

	// 预检 server 段内是否已有 password 行（没有则在 server: 行后插入）
	serverHasPassword := false
	{
		inServer := false
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "server:") {
				inServer = true
				continue
			}
			if inServer {
				if t != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
					inServer = false // 下一个顶级键，server 段结束
				} else if strings.HasPrefix(t, "password:") {
					serverHasPassword = true
					break
				}
			}
		}
	}
	inServerSection := false

	// 当前所在的配置区域
	inLLMSection := false
	inEmbeddingSection := false
	inEmailSection := false
	inGotifySection := false
	inWebhookSection := false
	inQQBotSection := false
	proxySectionSeen := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// 顶级键开启新段时结束 server 段（避免误更新其他段的 password，如 email）
		if inServerSection && trimmedLine != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(trimmedLine, "#") {
			inServerSection = false
		}

		// 检测区域
		if strings.HasPrefix(trimmedLine, "server:") {
			inServerSection = true
			// server 段缺 password 行时紧跟 server: 行插入（缩进与示例配置一致）
			if !serverHasPassword {
				result.WriteString(line + "\n")
				result.WriteString(fmt.Sprintf("  password: %q\n", appConfig.Server.Password))
				continue
			}
		} else if strings.HasPrefix(trimmedLine, "llm:") {
			inLLMSection = true
			inEmbeddingSection = false
		} else if strings.HasPrefix(trimmedLine, "embedding:") {
			inEmbeddingSection = true
			inLLMSection = false
		} else if strings.HasPrefix(trimmedLine, "email:") {
			inEmailSection = true
			inGotifySection = false
			inWebhookSection = false
		} else if strings.HasPrefix(trimmedLine, "gotify:") {
			inGotifySection = true
			inEmailSection = false
			inWebhookSection = false
		} else if strings.HasPrefix(trimmedLine, "webhook:") {
			inWebhookSection = true
			inEmailSection = false
			inGotifySection = false
			inQQBotSection = false
		} else if strings.HasPrefix(trimmedLine, "qqbot:") {
			inQQBotSection = true
			inEmailSection = false
			inGotifySection = false
			inWebhookSection = false
		} else if strings.HasPrefix(trimmedLine, "proxy:") {
			proxySectionSeen = true
			inQQBotSection = false
			inEmailSection = false
			inGotifySection = false
			inWebhookSection = false
		} else if strings.HasPrefix(trimmedLine, "push:") {
			inEmailSection = false
			inGotifySection = false
			inWebhookSection = false
			inQQBotSection = false
		}

		// 更新 server 段登录密码
		if inServerSection && strings.Contains(line, "password:") {
			line = updateYAMLValue(line, appConfig.Server.Password)
		}

		// 更新 LLM 配置
		if inLLMSection && !inEmbeddingSection {
			if strings.Contains(line, "base_url:") {
				line = updateYAMLValue(line, appConfig.AI.LLM.BaseURL)
			} else if strings.Contains(line, "api_key:") {
				line = updateYAMLValue(line, appConfig.AI.LLM.APIKey)
			} else if strings.Contains(line, "model:") {
				line = updateYAMLValue(line, appConfig.AI.LLM.Model)
			}
		}

		// 更新 Embedding 配置
		if inEmbeddingSection {
			if strings.Contains(line, "base_url:") {
				line = updateYAMLValue(line, appConfig.AI.Embedding.BaseURL)
			} else if strings.Contains(line, "api_key:") {
				line = updateYAMLValue(line, appConfig.AI.Embedding.APIKey)
			} else if strings.Contains(line, "model:") {
				line = updateYAMLValue(line, appConfig.AI.Embedding.Model)
			}
		}

		// 更新推送配置
		if inEmailSection {
			if strings.Contains(line, "smtp_host:") {
				line = updateYAMLValue(line, appConfig.Push.Email.SMTPHost)
			} else if strings.Contains(line, "smtp_port:") {
				line = updateYAMLValue(line, fmt.Sprintf("%d", appConfig.Push.Email.SMTPPort))
			} else if strings.Contains(line, "username:") {
				line = updateYAMLValue(line, appConfig.Push.Email.Username)
			} else if strings.Contains(line, "password:") {
				line = updateYAMLValue(line, appConfig.Push.Email.Password)
			}
		}

		if inGotifySection {
			if strings.Contains(line, "url:") {
				line = updateYAMLValue(line, appConfig.Push.Gotify.URL)
			} else if strings.Contains(line, "app_token:") {
				line = updateYAMLValue(line, appConfig.Push.Gotify.AppToken)
			}
		}

		if inWebhookSection {
			if strings.Contains(line, "url:") {
				line = updateYAMLValue(line, appConfig.Push.Webhook.URL)
			}
		}

		if inQQBotSection {
			if strings.Contains(line, "app_id:") {
				line = updateYAMLValue(line, appConfig.Push.QQBot.AppID)
			} else if strings.Contains(line, "app_secret:") {
				line = updateYAMLValue(line, appConfig.Push.QQBot.AppSecret)
			} else if strings.Contains(line, "user_id:") {
				line = updateYAMLValue(line, appConfig.Push.QQBot.UserID)
			}
		}

		// 更新代理配置（proxy: 段内）
		if proxySectionSeen && !strings.HasPrefix(trimmedLine, "proxy:") &&
			(strings.Contains(line, "url:") || strings.Contains(line, "enable_content:") || strings.Contains(line, "enable_llm:")) {
			if strings.Contains(line, "enable_content:") {
				line = updateYAMLValue(line, fmt.Sprintf("%t", appConfig.Proxy.EnableContent))
			} else if strings.Contains(line, "enable_llm:") {
				line = updateYAMLValue(line, fmt.Sprintf("%t", appConfig.Proxy.EnableLLM))
			} else {
				line = updateYAMLValue(line, appConfig.Proxy.URL)
			}
		}

		// 更新定时任务配置
		if strings.Contains(line, "morning_report_time:") {
			line = updateYAMLValue(line, appConfig.Scheduler.MorningReportTime)
		}
		if strings.Contains(line, "evening_report_time:") {
			line = updateYAMLValue(line, appConfig.Scheduler.EveningReportTime)
		}
		if strings.Contains(line, "daily_report_time:") {
			line = updateYAMLValue(line, appConfig.Scheduler.DailyReportTime)
		}

		result.WriteString(line + "\n")
	}

	// 代理段：文件中没有时追加到尾部（已有则在上面循环中行内更新）
	if !strings.Contains(string(originalContent), "proxy:") && !proxySectionSeen {
		result.WriteString(fmt.Sprintf("\n# 代理设置（内容抓取与 LLM 接口可分别启用）\nproxy:\n    url: \"%s\"\n    enable_content: %t\n    enable_llm: %t\n",
			appConfig.Proxy.URL, appConfig.Proxy.EnableContent, appConfig.Proxy.EnableLLM))
	}

	return os.WriteFile(configFilePath, []byte(result.String()), 0644)
}

// updateYAMLValue 更新 YAML 行中的值
func updateYAMLValue(line string, value string) string {
	// 找到冒号位置
	colonIdx := strings.Index(line, ":")
	if colonIdx == -1 {
		return line
	}

	// 保留原始缩进和键
	prefix := line[:colonIdx+1]
	rest := line[colonIdx+1:]

	// 检查是否有注释
	commentIdx := strings.Index(rest, "#")
	var comment string
	if commentIdx != -1 {
		comment = rest[commentIdx:]
		rest = rest[:commentIdx]
	} else {
		comment = ""
	}

	// 构建新行
	return prefix + " " + fmt.Sprintf("%q", value) + comment
}

// 请求结构体
type CreateFeedRequest struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Description   string `json:"description"`
	CategoryID    *int64 `json:"category_id"`
	FetchInterval int    `json:"fetch_interval"`
	IsActive      bool   `json:"is_active"`
	SourceType    string `json:"source_type"`
	SourceConfig  string `json:"source_config"`
	ContentFilter string `json:"content_filter"`
}

type CreateCategoryRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Icon        string `json:"icon"` // 前端使用 icon
	ContentType string `json:"content_type"`
}

type CreateFollowRuleRequest struct {
	Name                string  `json:"name"`
	Description         string  `json:"description"`
	Keywords            string  `json:"keywords"`
	SimilarityThreshold float64 `json:"similarity_threshold"`
	IsActive            bool    `json:"is_active"`
	EnablePush          bool    `json:"enable_push"`
	PushChannels        string  `json:"push_channels"`
}

// 页面处理函数

// IndexPage 首页
func IndexPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "仪表盘",
		PageTitle: "仪表盘",
		Active:    "dashboard",
		Stats: StatsData{
			TodayArticles:   0,
			TodayDiff:       0,
			ActiveFeeds:     0,
			TotalFeeds:      0,
			PendingArticles: 0,
			AdArticles:      0,
		},
	}

	// 从数据库加载统计数据
	if appDB != nil {
		stats, err := appDB.GetStats()
		if err == nil {
			data.Stats.TodayArticles = stats["today_articles"]
			data.Stats.ActiveFeeds = stats["active_feeds"]
			data.Stats.TotalFeeds = stats["total_feeds"]
			data.Stats.PendingArticles = stats["pending_articles"]
			data.Stats.AdArticles = stats["ad_articles"]
			data.Stats.TotalArticles = stats["total_articles"]
		}

		// 加载最新文章
		feeds, _ := appDB.ListFeeds()
		feedMap := make(map[int64]string)
		for _, f := range feeds {
			feedMap[f.ID] = f.Title
		}

		articles, err := appDB.ListArticles(10, 0)
		if err == nil {
			for _, a := range articles {
				fetchedAt := formatTimeInTimezone(a.FetchedAt)
				publishedAt := formatTimePtrInTimezone(a.PublishedAt)
				data.RecentArticles = append(data.RecentArticles, ArticleData{
					ID:          a.ID,
					Title:       a.Title,
					Summary:     a.Summary,
					AISummary:   a.AISummary,
					FeedTitle:   feedMap[a.FeedID],
					FetchedAt:   fetchedAt,
					PublishedAt: publishedAt,
					IsAd:        a.IsAd,
					Keywords:    a.Keywords,
				})
			}
		}

		// 加载话题流的高分话题（24 小时热榜，与话题流侧栏同源）
		if hot, err := appDB.GetHotTopics(time.Now().Add(-processor.HotTopicsWindow), 10); err == nil {
			data.HotTopicCards = hot
		}
	}

	renderTemplate(w, "index", data)
}

// TopicsPage 话题流页面（Readhub 式：跨多源聚合的话题卡片 + 热榜侧栏）
func TopicsPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "话题流",
		PageTitle: "话题流",
		Active:    "topics",
	}

	if appDB != nil {
		page := 1
		if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
			page = p
		}
		const pageSize = 20
		category := r.URL.Query().Get("category")

		topics, err := appDB.ListTopics(category, pageSize, (page-1)*pageSize)
		if err == nil {
			// 批量加载每个话题的最新几条相关新闻（单次查询，避免 N+1）
			topicIDs := make([]int64, len(topics))
			for i, t := range topics {
				topicIDs[i] = t.ID
			}
			if previews, err := appDB.GetTopicArticlesForTopics(topicIDs, 3); err == nil {
				for _, t := range topics {
					t.LatestArticles = previews[t.ID]
				}
			}
			data.TopicList = topics
			data.HasNext = len(topics) == pageSize
		}
		data.CurrentPage = page
		data.HasPrev = page > 1
		data.PrevPage = page - 1
		data.NextPage = page + 1
		data.SelectedCategory = category

		if hot, err := appDB.GetHotTopics(time.Now().Add(-processor.HotTopicsWindow), 10); err == nil {
			data.HotTopics24h = hot
		}
		if cats, err := appDB.GetTopicCategories(); err == nil {
			data.TopicCategories = cats
		}
	}

	renderTemplate(w, "topics", data)
}

// TopicDetailPage 话题详情页（完整相关新闻 + 同实体历史话题时间线）
func TopicDetailPage(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "数据库未初始化", http.StatusInternalServerError)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "无效的话题 ID", http.StatusBadRequest)
		return
	}

	topic, err := appDB.GetTopicByID(id)
	if err != nil {
		http.Error(w, "话题不存在", http.StatusNotFound)
		return
	}

	data := PageData{
		Title:      "话题详情",
		PageTitle:  "话题详情",
		Active:     "topics",
		TopicDetail: topic,
	}

	if articles, err := appDB.GetTopicArticles(id, 100, 0); err == nil {
		data.TopicArticles = articles
	}
	if related, err := appDB.GetRelatedTopicsByEntity(topic.EntityKey, topic.ID, 30); err == nil {
		data.RelatedTopics = related
	}

	renderTemplate(w, "topic_detail", data)
}

// RebuildTopics 从存量已分析文章重建话题（清空现有话题后按时间序重新聚合，异步执行）
func RebuildTopics(w http.ResponseWriter, r *http.Request) {
	if appDB == nil || appTopicAggregator == nil {
		http.Error(w, "服务未初始化", http.StatusServiceUnavailable)
		return
	}

	// 重建窗口（天），默认 7，范围 1-365
	days := 7
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v >= 1 && v <= 365 {
		days = v
	}
	ids, err := appDB.ListRecentAnalyzedArticles(time.Now().AddDate(0, 0, -days))
	if err != nil {
		http.Error(w, "获取文章失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := appDB.ClearTopics(); err != nil {
		http.Error(w, "清空话题失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 话题数据已清空，同步失效聚合器缓存
	appTopicAggregator.Invalidate()

	go func() {
		for _, id := range ids {
			if err := appTopicAggregator.AggregateArticle(id); err != nil {
				fmt.Printf("RebuildTopics: failed to aggregate article %d: %v\n", id, err)
			}
		}
		fmt.Printf("RebuildTopics: done, %d articles processed\n", len(ids))
	}()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "started", "articles": len(ids)})
}

// ConvertTopicToEvent 将话题导出为事件追踪（复用话题的关键词/摘要/锚点向量，
// 话题下已有文章直接导入，后续新文章由事件匹配器持续追踪）
func ConvertTopicToEvent(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "数据库未初始化", http.StatusInternalServerError)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "无效的话题 ID", http.StatusBadRequest)
		return
	}
	topic, err := appDB.GetTopicByIDWithEmbedding(id)
	if err != nil {
		http.Error(w, "话题不存在", http.StatusNotFound)
		return
	}

	// 防重复：同名事件已存在则直接复用
	if existing, err := appDB.GetEventTrackByName(topic.Title); err == nil && existing != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"event_id": existing.ID, "exists": true})
		return
	}

	now := time.Now()
	event := &models.EventTrack{
		Name:        topic.Title,
		Keywords:    topic.Keywords, // 实体优先排序过的话题关键词集合
		Description: topic.AISummary,
		Embedding:   topic.Embedding, // 话题锚点向量，直接用于后续事件匹配
		Status:      "active",
		MatchCount:  topic.ArticleCount,
		LastMatchAt: &now,
	}
	eventID, err := appDB.CreateEventTrack(event)
	if err != nil {
		http.Error(w, "创建事件失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 导入话题下已有文章
	imported := 0
	if articles, err := appDB.GetTopicArticles(topic.ID, 200, 0); err == nil {
		for _, a := range articles {
			if _, err := appDB.CreateEventArticle(&models.EventArticle{
				EventID:     eventID,
				ArticleID:   a.ID,
				MatchReason: "topic-import",
				MatchScore:  1.0,
				Importance:  5,
			}); err == nil {
				imported++
			}
		}
	}
	fmt.Printf("ConvertTopicToEvent: topic %d -> event %d, imported %d articles\n", topic.ID, eventID, imported)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"event_id": eventID, "articles": imported})
}

// FeedsPage 订阅管理页
func FeedsPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "订阅管理",
		PageTitle: "订阅管理",
		Active:    "feeds",
		Feeds:     []FeedData{},
		Categories: []CategoryData{},
	}

	// 从数据库加载分类
	if appDB != nil {
		categories, _ := appDB.ListCategories()
		catMap := make(map[int64]*models.Category)
		for _, c := range categories {
			catMap[c.ID] = c
			data.Categories = append(data.Categories, CategoryData{
				ID:          c.ID,
				Name:        c.Name,
				Description: c.Description,
				Icon:        c.Color,
			})
		}

		// 加载 feeds
		feeds, err := appDB.ListFeeds()
		if err == nil {
			for _, f := range feeds {
				lastFetched := "未刷新"
				if f.LastFetchedAt != nil {
					lastFetched = formatTimePtrInTimezone(f.LastFetchedAt)
				}

				var catName, catIcon string
				if f.CategoryID != nil {
					if cat, ok := catMap[*f.CategoryID]; ok {
						catName = cat.Name
						catIcon = cat.Color
					}
				}

				data.Feeds = append(data.Feeds, FeedData{
					ID:            f.ID,
					Title:         f.Title,
					URL:           f.URL,
					Description:   f.Description,
					CategoryID:    f.CategoryID,
					CategoryName:  catName,
					CategoryIcon:  catIcon,
					ArticleCount:  0, // TODO: 统计文章数
					LastFetched:   lastFetched,
					FetchInterval: f.FetchInterval,
					IsActive:      f.IsActive,
					SourceType:    f.SourceType,
				})
			}
		}
	}

	renderTemplate(w, "feeds", data)
}

// ArticlesPage 文章列表页
func ArticlesPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:      "文章列表",
		PageTitle:  "文章列表",
		Active:     "articles",
		Articles:   []ArticleData{},
		Feeds:      []FeedData{},
		Categories: []CategoryData{},
	}

	// 从数据库加载文章
	if appDB != nil {
		// 加载所有 feeds
		feeds, _ := appDB.ListFeeds()
		feedMap := make(map[int64]string)
		for _, f := range feeds {
			feedMap[f.ID] = f.Title
			data.Feeds = append(data.Feeds, FeedData{
				ID:       f.ID,
				Title:    f.Title,
				URL:      f.URL,
				IsActive: f.IsActive,
			})
		}

		// 加载所有分类
		categories, _ := appDB.ListCategories()
		for _, c := range categories {
			data.Categories = append(data.Categories, CategoryData{
				ID:          c.ID,
				Name:        c.Name,
				Description: c.Description,
				Icon:        c.Color,
			})
		}

		articles, err := appDB.ListArticles(50, 0)
		if err == nil {
			for _, a := range articles {
				fetchedAt := formatTimeInTimezone(a.FetchedAt)
				feedTitle := feedMap[a.FeedID]
				data.Articles = append(data.Articles, ArticleData{
					ID:           a.ID,
					Title:        a.Title,
					Summary:      a.Summary,
					AISummary:    a.AISummary,
					FeedTitle:    feedTitle,
					Category:     "",
					CategoryName: "",
					Keywords:     a.Keywords,
					AdReason:     a.AdReason,
					FetchedAt:    fetchedAt,
					IsAd:         a.IsAd,
					IsRead:       false,
					IsStarred:    false,
				})
			}
		}
	}

	renderTemplate(w, "articles", data)
}

// SettingsPage 设置页
func SettingsPage(w http.ResponseWriter, r *http.Request) {
	// 默认设置
	settings := SettingsData{
		AutoSummary:       true,
		AdDetection:       true,
		AutoTagging:       true,
		Embedding:         true,
		MorningReportTime: "08:00",
		EveningReportTime: "20:00",
		RefreshInterval:   30,
	}

	// 从配置加载
	if appConfig != nil {
		settings.LLMBaseURL = appConfig.AI.LLM.BaseURL
		settings.LLMModel = appConfig.AI.LLM.Model
		settings.LLMAPIKey = appConfig.AI.LLM.APIKey
		settings.EmbeddingBaseURL = appConfig.AI.Embedding.BaseURL
		settings.EmbeddingModel = appConfig.AI.Embedding.Model
		settings.EmbeddingAPIKey = appConfig.AI.Embedding.APIKey
		settings.MorningReportTime = appConfig.Scheduler.MorningReportTime
		settings.EveningReportTime = appConfig.Scheduler.EveningReportTime
		settings.DailyReportTime = appConfig.Scheduler.DailyReportTime
		// 推送配置
		settings.GotifyURL = appConfig.Push.Gotify.URL
		settings.GotifyToken = appConfig.Push.Gotify.AppToken
		settings.SMTPHost = appConfig.Push.Email.SMTPHost
		settings.SMTPPort = appConfig.Push.Email.SMTPPort
		settings.SMTPUsername = appConfig.Push.Email.Username
		settings.SMTPPassword = appConfig.Push.Email.Password
		settings.WebhookURL = appConfig.Push.Webhook.URL
		// QQ Bot 配置
		settings.QQBotAppID = appConfig.Push.QQBot.AppID
		settings.QQBotAppSecret = appConfig.Push.QQBot.AppSecret
		settings.QQBotUserID = appConfig.Push.QQBot.UserID
		// 代理配置
		settings.ProxyURL = appConfig.Proxy.URL
		settings.ProxyEnableContent = appConfig.Proxy.EnableContent
		settings.ProxyEnableLLM = appConfig.Proxy.EnableLLM
		// 登录密码
		settings.ServerPassword = appConfig.Server.Password
	}

	data := PageData{
		Title:     "设置",
		PageTitle: "设置",
		Active:    "settings",
		Settings:  settings,
	}
	renderTemplate(w, "settings", data)
}

// CategoriesPage 分类管理页
func CategoriesPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "分类管理",
		PageTitle: "分类管理",
		Active:    "categories",
		Categories: []CategoryData{},
	}

	// 从数据库加载分类
	if appDB != nil {
		categories, err := appDB.ListCategories()
		if err == nil {
			for _, c := range categories {
				// 获取该分类的统计数据
				feedCount, articleCount, _ := appDB.GetCategoryStats(c.ID)
				data.Categories = append(data.Categories, CategoryData{
					ID:           c.ID,
					Name:         c.Name,
					Description:  c.Description,
					Icon:         c.Color, // 用 Color 作为 Icon
					ContentType:  c.ContentType,
					FeedCount:    feedCount,
					ArticleCount: articleCount,
				})
			}
		}
	}

	renderTemplate(w, "categories", data)
}

// TagsPage 标签管理页
func TagsPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "标签管理",
		PageTitle: "标签管理",
		Active:    "tags",
		Tags:      []TagData{},
	}

	// 从数据库加载标签
	if appDB != nil {
		tags, err := appDB.ListTags()
		if err == nil {
			for _, t := range tags {
				color := t.Color
				if color == "" {
					color = "#3b82f6"
				}
				data.Tags = append(data.Tags, TagData{
					ID:           t.ID,
					Name:         t.Name,
					Color:        color,
					ArticleCount: t.UsageCount,
				})
			}
		}
	}

	renderTemplate(w, "tags", data)
}

// RulesPage 关注规则页
func RulesPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "关注规则",
		PageTitle: "关注规则",
		Active:    "rules",
		Rules:     []RuleData{},
	}

	// 从数据库加载规则
	if appDB != nil {
		rules, err := appDB.ListFollowRules()
		if err == nil {
			for _, rl := range rules {
				data.Rules = append(data.Rules, RuleData{
					ID:                  rl.ID,
					Name:                rl.Name,
					Description:         rl.Description,
					Keywords:            rl.Keywords,
					SimilarityThreshold: rl.SimilarityThreshold,
					EnablePush:          rl.EnablePush,
					IsActive:            rl.IsActive,
					PushChannels:        rl.PushChannels,
				})
			}
		}
	}

	renderTemplate(w, "rules", data)
}

// FollowedPage 关注的文章页
func FollowedPage(w http.ResponseWriter, r *http.Request) {
	pageSize := 20
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if val, err := strconv.Atoi(p); err == nil && val > 0 {
			page = val
		}
	}
	ruleFilter := r.URL.Query().Get("rule")

	data := PageData{
		Title:       "关注的文章",
		PageTitle:   "关注的文章",
		Active:      "followed",
		Articles:    []ArticleData{},
		Rules:       []RuleData{},
		CurrentPage: page,
		PageSize:    pageSize,
	}

	// 设置选中的规则
	if ruleFilter != "" {
		if ruleID, err := strconv.ParseInt(ruleFilter, 10, 64); err == nil {
			data.SelectedRule = ruleID
		}
	}

	if appDB == nil {
		renderTemplate(w, "followed", data)
		return
	}

	// 加载规则列表（用于筛选下拉框）
	rules, err := appDB.ListFollowRules()
	if err == nil {
		for _, rl := range rules {
			data.Rules = append(data.Rules, RuleData{
				ID:                  rl.ID,
				Name:                rl.Name,
				Description:         rl.Description,
				Keywords:            rl.Keywords,
				SimilarityThreshold: rl.SimilarityThreshold,
				EnablePush:          rl.EnablePush,
				IsActive:            rl.IsActive,
				PushChannels:        rl.PushChannels,
			})
		}
	}

	// 获取用于匹配的规则
	matchRules := rules

	// 如果指定了规则筛选，只使用该规则
	if ruleFilter != "" {
		ruleID, err := strconv.ParseInt(ruleFilter, 10, 64)
		if err == nil {
			for _, rl := range rules {
				if rl.ID == ruleID {
					matchRules = []*models.FollowRule{rl}
					// 设置规则名称用于页面标题
					data.RuleName = rl.Name
					break
				}
			}
		}
	} else if len(rules) > 0 {
		data.RuleName = "全部关注"
	}

	// 获取所有文章并匹配
	articles, err := appDB.ListArticles(1000, 0) // 获取最近的文章
	if err != nil {
		renderTemplate(w, "followed", data)
		return
	}

	// 匹配文章
	matchedArticles := make([]ArticleData, 0)
	for _, article := range articles {
		for _, rule := range matchRules {
			if matchKeywords(article.Keywords, article.Title, rule.Keywords) {
				// 获取 Feed 名称
				feedName := ""
				if feed, err := appDB.GetFeedByID(article.FeedID); err == nil {
					feedName = feed.Title
				}

				matchedArticles = append(matchedArticles, ArticleData{
					ID:              article.ID,
					Title:           article.Title,
					Link:            article.Link,
					Summary:         article.Summary,
					FeedName:        feedName,
					Keywords:        article.Keywords,
					MatchedRuleName: rule.Name,
					FetchedAt:       article.FetchedAt.Format("2006-01-02 15:04"),
				})
				break // 一篇文章只匹配一个规则
			}
		}
	}

	// 分页
	totalCount := len(matchedArticles)
	data.TotalCount = totalCount
	data.TotalPages = (totalCount + pageSize - 1) / pageSize
	if data.TotalPages < 1 {
		data.TotalPages = 1
	}

	// 计算分页范围
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > totalCount {
		start = totalCount
	}
	if end > totalCount {
		end = totalCount
	}

	data.Articles = matchedArticles[start:end]
	data.HasPrev = page > 1
	data.HasNext = page < data.TotalPages
	data.PrevPage = page - 1
	data.NextPage = page + 1

	renderTemplate(w, "followed", data)
}

// matchKeywords 检查规则关键词是否与文章关键词/标题匹配
func matchKeywords(articleKeywords, articleTitle, ruleKeywords string) bool {
	if ruleKeywords == "" {
		return false
	}

	// 构建文章的关键词集合
	articleWords := make(map[string]bool)

	// 添加文章关键词
	if articleKeywords != "" {
		for _, kw := range strings.Split(articleKeywords, ",") {
			kw = strings.TrimSpace(strings.ToLower(kw))
			if kw != "" {
				articleWords[kw] = true
			}
		}
	}

	// 检查规则关键词是否匹配
	for _, ruleKw := range strings.Split(ruleKeywords, ",") {
		ruleKw = strings.TrimSpace(strings.ToLower(ruleKw))
		if ruleKw == "" {
			continue
		}

		// 直接匹配文章关键词
		if articleWords[ruleKw] {
			return true
		}

		// 在标题中搜索
		if strings.Contains(strings.ToLower(articleTitle), ruleKw) {
			return true
		}
	}

	return false
}

// ListFollowedArticles 获取关注规则匹配的文章（API）
func ListFollowedArticles(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 解析分页参数
	offset := 0
	limit := 20
	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}

	ruleFilter := r.URL.Query().Get("rule")

	// 获取所有活跃的规则
	rules, err := appDB.ListFollowRules()
	if err != nil || len(rules) == 0 {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	// 筛选规则
	var matchRules []*models.FollowRule
	if ruleFilter != "" {
		ruleID, err := strconv.ParseInt(ruleFilter, 10, 64)
		if err == nil {
			for _, rule := range rules {
				if rule.ID == ruleID {
					matchRules = append(matchRules, rule)
				}
			}
		}
	} else {
		matchRules = rules
	}

	if len(matchRules) == 0 {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	// 获取所有文章并匹配
	articles, err := appDB.ListArticles(2000, 0) // 获取足够多的文章
	if err != nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	// 匹配文章
	var matchedArticles []map[string]interface{}
	for _, article := range articles {
		for _, rule := range matchRules {
			if matchKeywords(article.Keywords, article.Title, rule.Keywords) {
				// 获取 Feed 名称
				feedName := ""
				if feed, err := appDB.GetFeedByID(article.FeedID); err == nil {
					feedName = feed.Title
				}

				// 优先使用发布时间，没有则使用抓取时间
				publishedAt := formatTimePtrInTimezone(article.PublishedAt)
				if publishedAt == "" {
					publishedAt = formatTimeInTimezone(article.FetchedAt)
				}

				// 单独查询 is_read（ListArticles 不含此字段）
				isRead := false
				var readVal bool
				_ = appDB.SQL().QueryRow("SELECT COALESCE(is_read, 0) FROM articles WHERE id = ?", article.ID).Scan(&readVal)
				isRead = readVal

				matchedArticles = append(matchedArticles, map[string]interface{}{
					"id":                article.ID,
					"title":             article.Title,
					"link":              article.Link,
					"summary":           article.Summary,
					"ai_summary":        article.AISummary,
					"feed_name":         feedName,
					"keywords":          article.Keywords,
					"matched_rule_name": rule.Name,
					"published_at":      publishedAt,
					"is_read":           isRead,
					"translated_content": article.TranslatedContent,
				})
				break // 一篇文章只匹配一个规则
			}
		}
	}

	// 分页
	start := offset
	end := offset + limit
	if start > len(matchedArticles) {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	if end > len(matchedArticles) {
		end = len(matchedArticles)
	}

	json.NewEncoder(w).Encode(matchedArticles[start:end])
}

// ReportsPage 早晚报页
func ReportsPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "早晚报",
		PageTitle: "早晚报",
		Active:    "reports",
		Reports:   []ReportData{},
	}

	// 从配置中读取报告设置
	cfg := config.GetCurrentConfig()
	if cfg != nil {
		data.MorningEnabled = cfg.Scheduler.MorningAutoPush
		data.EveningEnabled = cfg.Scheduler.EveningAutoPush
		data.DailyEnabled = cfg.Scheduler.DailyAutoPush
		data.MorningTime = cfg.Scheduler.MorningReportTime
		data.EveningTime = cfg.Scheduler.EveningReportTime
		data.DailyTime = cfg.Scheduler.DailyReportTime
	} else {
		// 默认值
		data.MorningEnabled = true
		data.EveningEnabled = true
		data.DailyEnabled = true
		data.MorningTime = "08:00"
		data.EveningTime = "20:00"
		data.DailyTime = "22:00"
	}

	// 推送渠道从数据库中获取（暂时使用默认值）
	data.MorningChannels = "gotify"
	data.EveningChannels = "gotify"
	data.DailyChannels = "gotify"

	// 从数据库加载报告
	if appDB != nil {
		reports, err := appDB.ListReports()
		if err == nil {
			for _, rp := range reports {
				createdAt := formatTimeInTimezone(rp.CreatedAt)
				sentAt := ""
				if rp.SentAt != nil {
					sentAt = formatTimePtrInTimezone(rp.SentAt)
				}
				// 估算话题数：每5篇文章约1个话题
				topicCount := rp.ArticleCount / 5
				if topicCount < 1 && rp.ArticleCount > 0 {
					topicCount = 1
				}
				data.Reports = append(data.Reports, ReportData{
					ID:           rp.ID,
					Name:         rp.Name,
					Type:         rp.Type,
					Summary:      rp.Summary,
					ArticleCount: rp.ArticleCount,
					TopicCount:   topicCount,
					Channels:     rp.Channels,
					CreatedAt:    createdAt,
					SentAt:       sentAt,
				})
			}
		}
	}

	renderTemplate(w, "reports", data)
}

// DataPage 数据管理页
func DataPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "数据管理",
		PageTitle: "数据管理",
		Active:    "data",
		Stats: StatsData{
			DatabaseSize:   "0 KB",
			TotalArticles:  0,
			EmbeddingCount: 0,
			LastBackup:     "从未备份",
			AdArticles:     0,
		},
	}

	// 从数据库加载统计数据
	if appDB != nil {
		stats, err := appDB.GetStats()
		if err == nil {
			data.Stats.TotalArticles = stats["total_articles"]
			data.Stats.AdArticles = stats["ad_articles"]
		}
	}

	renderTemplate(w, "data", data)
}

// EventsPage 事件追踪页
func EventsPage(w http.ResponseWriter, r *http.Request) {
	data := PageData{
		Title:     "事件追踪",
		PageTitle: "事件追踪",
		Active:    "events",
	}
	renderTemplate(w, "events", data)
}

// PendingEventsPage 待关注事件页
func PendingEventsPage(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	events, err := appDB.ListEventTracks("pending")
	if err != nil {
		http.Error(w, "Failed to get pending events: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(events)
}

// EventDetailPage 事件详情页
func EventDetailPage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	event, err := appDB.GetEventTrack(id)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	// 获取角色统计
	roleStats, _ := appDB.GetEventRoleStats(id)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"event":      event,
		"role_stats": roleStats,
	})
}

// EventTimelinePage 事件时间线页
func EventTimelinePage(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	role := r.URL.Query().Get("role")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	articles, err := appDB.GetEventArticles(id, role, limit, 0)
	if err != nil {
		http.Error(w, "Failed to get event articles: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(articles)
}

// 以下是新增的 API 处理函数

// GetCategory 获取单个分类
func GetCategory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	categoryID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		http.Error(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	category, err := appDB.GetCategoryByID(categoryID)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          category.ID,
		"name":        category.Name,
		"description": category.Description,
		"icon":        category.Color, // 前端使用 icon，数据库存储为 color
		"content_type": category.ContentType,
	})
}

// UpdateCategory 更新分类
func UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	categoryID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		http.Error(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	var req CreateCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	// 处理 icon/color 字段
	color := req.Icon
	if color == "" {
		color = req.Color
	}

	// 更新数据库（Color 字段存储 icon）
	category := &models.Category{
		ID:          categoryID,
		Name:        req.Name,
		Description: req.Description,
		Color:       color,
		ContentType: req.ContentType,
	}

	if err := appDB.UpdateCategory(category); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Category name already exists", http.StatusConflict)
			return
		}
		http.Error(w, "Failed to update category: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          categoryID,
		"name":        req.Name,
		"description": req.Description,
		"icon":        color, // 返回处理后的 color 值
	})
}

// DeleteCategory 删除分类
func DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	categoryID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		http.Error(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	if err := appDB.DeleteCategory(categoryID); err != nil {
		http.Error(w, "Failed to delete category: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetTag 获取单个标签
func GetTag(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid tag ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	tag, err := appDB.GetTagByID(id)
	if err != nil {
		http.Error(w, "Tag not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(tag)
}

// UpdateTag 更新标签
func UpdateTag(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid tag ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	tag := &models.Tag{
		ID:    id,
		Name:  req.Name,
		Color: req.Color,
	}

	if err := appDB.UpdateTag(tag); err != nil {
		http.Error(w, "Failed to update tag", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(tag)
}

// DeleteTag 删除标签
func DeleteTag(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid tag ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if err := appDB.DeleteTag(id); err != nil {
		http.Error(w, "Failed to delete tag", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListRules 列出规则
func ListRules(w http.ResponseWriter, r *http.Request) {
	if appDB != nil {
		rules, err := appDB.ListFollowRules()
		if err != nil {
			http.Error(w, "Failed to list rules: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(rules)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode([]interface{}{})
}

// CreateRule 创建规则
func CreateRule(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	var req CreateFollowRuleRequest

	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		// 表单数据
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}
		req.Name = r.FormValue("name")
		req.Description = r.FormValue("description")
		req.Keywords = r.FormValue("keywords")
		req.EnablePush = r.FormValue("enable_push") == "on"
		if threshold := r.FormValue("similarity_threshold"); threshold != "" {
			if val, err := parseFloat(threshold); err == nil {
				req.SimilarityThreshold = val
			}
		}
	}

	// 验证必填字段
	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	// 保存到数据库
	if appDB != nil {
		rule := &models.FollowRule{
			Name:              req.Name,
			Description:       req.Description,
			Keywords:          req.Keywords,
			SimilarityThreshold: req.SimilarityThreshold,
			IsActive:          true,
			EnablePush:        req.EnablePush,
		}
		id, err := appDB.CreateFollowRule(rule)
		if err != nil {
			http.Error(w, "Failed to create rule: "+err.Error(), http.StatusInternalServerError)
			return
		}
		rule.ID = id

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Rule created successfully",
			"data":    rule,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(req)
}

// GetRule 获取单个规则
func GetRule(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	rule, err := appDB.GetFollowRuleByID(id)
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(rule)
}

// UpdateRule 更新规则
func UpdateRule(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	contentType := r.Header.Get("Content-Type")
	var req CreateFollowRuleRequest

	if strings.Contains(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		// 表单数据
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}
		req.Name = r.FormValue("name")
		req.Description = r.FormValue("description")
		req.Keywords = r.FormValue("keywords")
		req.EnablePush = r.FormValue("enable_push") == "on"
		req.IsActive = r.FormValue("is_active") == "on"
		if threshold := r.FormValue("similarity_threshold"); threshold != "" {
			if val, err := parseFloat(threshold); err == nil {
				req.SimilarityThreshold = val / 100.0 // 转换为 0-1 范围
			}
		}
		// 处理推送渠道（多选）
		channels := r.Form["channels"]
		req.PushChannels = strings.Join(channels, ",")
	}

	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 获取现有规则
	rule, err := appDB.GetFollowRuleByID(id)
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	// 更新字段
	rule.Name = req.Name
	rule.Description = req.Description
	rule.Keywords = req.Keywords
	rule.SimilarityThreshold = req.SimilarityThreshold
	rule.EnablePush = req.EnablePush
	rule.PushChannels = req.PushChannels
	rule.IsActive = req.IsActive

	if err := appDB.UpdateFollowRule(rule); err != nil {
		http.Error(w, "Failed to update rule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rule updated successfully",
		"data":    rule,
	})
}

// DeleteRule 删除规则
func DeleteRule(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if err := appDB.DeleteFollowRule(id); err != nil {
		http.Error(w, "Failed to delete rule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ToggleRule 切换规则状态
func ToggleRule(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 获取现有规则
	rule, err := appDB.GetFollowRuleByID(id)
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}

	// 切换状态
	rule.IsActive = !rule.IsActive

	if err := appDB.UpdateFollowRule(rule); err != nil {
		http.Error(w, "Failed to toggle rule: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"id":        id,
		"is_active": rule.IsActive,
	})
}

// ClearRulePushRecords 清除规则的推送记录
func ClearRulePushRecords(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid rule ID", http.StatusBadRequest)
		return
	}

	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if err := appDB.ClearFollowRulePushRecords(id); err != nil {
		http.Error(w, "Failed to clear push records: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"id":      id,
	})
}

// ResendReport 重发报告
func ResendReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReportID int64 `json:"report_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.ReportID == 0 {
		http.Error(w, "report_id is required", http.StatusBadRequest)
		return
	}

	// 获取报告（包含内容）
	report, _, err := appDB.GetReportWithArticles(req.ReportID)
	if err != nil {
		http.Error(w, "report not found: "+err.Error(), http.StatusNotFound)
		return
	}

	if reportGenerator == nil {
		http.Error(w, "report generator not initialized", http.StatusInternalServerError)
		return
	}

	// 在后台 goroutine 中执行推送
	go reportGenerator.PushReport(report)

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "resending"})
}

// UpdateReportConfig 更新报告配置
func UpdateReportConfig(w http.ResponseWriter, r *http.Request) {
	// HTMX 发送的是表单数据，使用 FormValue 解析
	reportType := r.FormValue("type")
	action := r.FormValue("action")

	if reportType == "" {
		http.Error(w, "Missing type parameter", http.StatusBadRequest)
		return
	}

	// 先获取当前状态
	cfg := config.GetCurrentConfig()
	var currentEnabled bool
	switch reportType {
	case "morning":
		currentEnabled = cfg.Scheduler.MorningAutoPush
	case "evening":
		currentEnabled = cfg.Scheduler.EveningAutoPush
	case "daily":
		currentEnabled = cfg.Scheduler.DailyAutoPush
	default:
		http.Error(w, "Invalid report type", http.StatusBadRequest)
		return
	}

	// 切换状态
	newEnabled := !currentEnabled
	if action == "toggle" {
		newEnabled = !currentEnabled
	}

	log.Printf("更新报告配置: type=%s, action=%s, currentEnabled=%v, newEnabled=%v", reportType, action, currentEnabled, newEnabled)

	// 保存到配置文件
	if err := config.SaveReportConfig(reportType, newEnabled); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 检查是否是 HTMX 请求，返回 HTML 片段
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if newEnabled {
			fmt.Fprintf(w, `<span class="font-medium text-green-600">已开启</span>`)
		} else {
			fmt.Fprintf(w, `<span class="font-medium text-gray-400">已关闭</span>`)
		}
		return
	}

	// 非 HTMX 请求返回 JSON
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"type":      reportType,
		"auto_push": newEnabled,
	})
}

// ExportData 导出数据
func ExportData(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	format := r.FormValue("format")
	if format == "" {
		format = "json"
	}

	switch format {
	case "json":
		exportJSON(w, r)
	case "csv":
		exportCSV(w, r)
	case "opml":
		exportOPML(w, r)
	default:
		exportJSON(w, r)
	}
}

// exportJSON 导出为 JSON 格式
func exportJSON(w http.ResponseWriter, r *http.Request) {
	// 获取所有订阅源
	feeds, err := appDB.ListFeeds()
	if err != nil {
		http.Error(w, "Failed to get feeds: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 获取所有分类
	categories, err := appDB.ListCategories()
	if err != nil {
		http.Error(w, "Failed to get categories: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 获取所有关注规则
	rules, err := appDB.ListFollowRules()
	if err != nil {
		http.Error(w, "Failed to get follow rules: "+err.Error(), http.StatusInternalServerError)
		return
	}

	exportData := map[string]interface{}{
		"version":    "1.0",
		"exportedAt": time.Now().Format(time.RFC3339),
		"feeds":      feeds,
		"categories": categories,
		"rules":      rules,
	}

	filename := fmt.Sprintf("rss-backup-%s.json", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", filename, filename))
	json.NewEncoder(w).Encode(exportData)
}

// exportCSV 导出为 CSV 格式（仅订阅源）
func exportCSV(w http.ResponseWriter, r *http.Request) {
	feeds, err := appDB.ListFeeds()
	if err != nil {
		http.Error(w, "Failed to get feeds: "+err.Error(), http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("feeds-%s.csv", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", filename, filename))

	// 写入 BOM 以支持 Excel 正确识别 UTF-8
	w.Write([]byte("\xEF\xBB\xBF"))

	// 写入表头
	fmt.Fprintln(w, "标题,URL,描述,分类ID,抓取间隔,是否启用")

	// 写入数据
	for _, feed := range feeds {
		title := strings.ReplaceAll(feed.Title, ",", "，")
		desc := strings.ReplaceAll(feed.Description, ",", "，")
		fmt.Fprintf(w, "%s,%s,%s,%d,%d,%t\n", title, feed.URL, desc, feed.CategoryID, feed.FetchInterval, feed.IsActive)
	}
}

// exportOPML 导出为 OPML 格式
func exportOPML(w http.ResponseWriter, r *http.Request) {
	feeds, err := appDB.ListFeeds()
	if err != nil {
		http.Error(w, "Failed to get feeds: "+err.Error(), http.StatusInternalServerError)
		return
	}

	categories, err := appDB.ListCategories()
	if err != nil {
		http.Error(w, "Failed to get categories: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 按分类分组订阅源
	catFeeds := make(map[int64][]*models.Feed)
	for _, feed := range feeds {
		catID := int64(0)
		if feed.CategoryID != nil {
			catID = *feed.CategoryID
		}
		catFeeds[catID] = append(catFeeds[catID], feed)
	}

	filename := fmt.Sprintf("feeds-%s.opml", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", filename, filename))

	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintln(w, `<opml version="2.0">`)
	fmt.Fprintln(w, `  <head>`)
	fmt.Fprintf(w, `    <title>RSS 订阅导出</title>`)
	fmt.Fprintf(w, `    <dateCreated>%s</dateCreated>`+"\n", time.Now().Format(time.RFC3339))
	fmt.Fprintln(w, `  </head>`)
	fmt.Fprintln(w, `  <body>`)

	// 无分类的订阅源
	if uncategorized, ok := catFeeds[0]; ok && len(uncategorized) > 0 {
		for _, feed := range uncategorized {
			fmt.Fprintf(w, `    <outline type="rss" text="%s" title="%s" xmlUrl="%s" description="%s"/>`+"\n",
				escapeXML(feed.Title), escapeXML(feed.Title), feed.URL, escapeXML(feed.Description))
		}
	}

	// 有分类的订阅源
	for _, cat := range categories {
		catID := cat.ID
		if feedsInCat, ok := catFeeds[catID]; ok && len(feedsInCat) > 0 {
			fmt.Fprintf(w, `    <outline text="%s" title="%s">`+"\n", escapeXML(cat.Name), escapeXML(cat.Name))
			for _, feed := range feedsInCat {
				fmt.Fprintf(w, `      <outline type="rss" text="%s" title="%s" xmlUrl="%s" description="%s"/>`+"\n",
					escapeXML(feed.Title), escapeXML(feed.Title), feed.URL, escapeXML(feed.Description))
			}
			fmt.Fprintln(w, `    </outline>`)
		}
	}

	fmt.Fprintln(w, `  </body>`)
	fmt.Fprintln(w, `</opml>`)
}

// escapeXML 转义 XML 特殊字符
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, `'`, "&apos;")
	return s
}

// ImportData 导入数据
func ImportData(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Database not initialized"})
		return
	}

	// 解析 multipart form (最大 32MB)
	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Failed to parse form: " + err.Error()})
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "请选择要导入的文件"})
		return
	}
	defer file.Close()

	// 根据文件扩展名判断导入类型
	ext := strings.ToLower(handler.Filename)
	if strings.HasSuffix(ext, ".json") {
		importJSON(w, r, file)
	} else if strings.HasSuffix(ext, ".opml") {
		importOPML(w, r, file)
	} else if strings.HasSuffix(ext, ".csv") {
		importCSV(w, r, file)
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "不支持的文件格式，请使用 JSON、CSV 或 OPML 文件"})
	}
}

// importJSON 导入 JSON 格式数据
func importJSON(w http.ResponseWriter, r *http.Request, file io.Reader) {
	var data struct {
		Version    string            `json:"version"`
		Feeds      []*models.Feed    `json:"feeds"`
		Categories []*models.Category `json:"categories"`
		Rules      []*models.FollowRule `json:"rules"`
	}

	if err := json.NewDecoder(file).Decode(&data); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "JSON 解析失败: " + err.Error()})
		return
	}

	stats := struct {
		Feeds      int `json:"feeds"`
		Categories int `json:"categories"`
		Rules      int `json:"rules"`
	}{}

	// 导入分类
	for _, cat := range data.Categories {
		// 检查是否已存在
		existing, _ := appDB.GetCategoryByName(cat.Name)
		if existing == nil {
			_, err := appDB.CreateCategory(cat)
			if err == nil {
				stats.Categories++
			}
		}
	}

	// 导入订阅源
	for _, feed := range data.Feeds {
		// 检查是否已存在（通过 URL）
		existing, _ := appDB.GetFeedByURL(feed.URL)
		if existing == nil {
			_, err := appDB.CreateFeed(feed)
			if err == nil {
				stats.Feeds++
			}
		}
	}

	// 导入关注规则
	for _, rule := range data.Rules {
		// 检查是否已存在（通过名称）
		existing, _ := appDB.GetFollowRuleByName(rule.Name)
		if existing == nil {
			_, err := appDB.CreateFollowRule(rule)
			if err == nil {
				stats.Rules++
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("导入完成：新增 %d 个分类、%d 个订阅源、%d 个关注规则", stats.Categories, stats.Feeds, stats.Rules),
		"stats":   stats,
	})
}

// importOPML 导入 OPML 格式数据
func importOPML(w http.ResponseWriter, r *http.Request, file io.Reader) {
	content, err := io.ReadAll(file)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "读取文件失败: " + err.Error()})
		return
	}

	// 简单解析 OPML（提取所有 outline 元素）
	imported := 0
	lines := strings.Split(string(content), "\n")
	var currentCategory string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// 检查是否是分类 outline
		if strings.Contains(line, `<outline`) && !strings.Contains(line, `xmlUrl=`) {
			// 提取分类名称
			if match := regexp.MustCompile(`text="([^"]+)"`).FindStringSubmatch(line); len(match) > 1 {
				currentCategory = match[1]
				// 创建分类
				cat := &models.Category{Name: currentCategory, Color: "#3498db"}
				appDB.CreateCategory(cat)
			}
		}

		// 检查是否是订阅源 outline
		if strings.Contains(line, `xmlUrl=`) {
			// 提取标题和 URL
			titleMatch := regexp.MustCompile(`title="([^"]+)"`).FindStringSubmatch(line)
			urlMatch := regexp.MustCompile(`xmlUrl="([^"]+)"`).FindStringSubmatch(line)

			if len(urlMatch) > 1 {
				title := ""
				if len(titleMatch) > 1 {
					title = titleMatch[1]
				} else {
					title = "未命名订阅"
				}

				feed := &models.Feed{
					Title: title,
					URL:   urlMatch[1],
				}

				// 如果有分类，查找分类 ID
				if currentCategory != "" {
					cat, _ := appDB.GetCategoryByName(currentCategory)
					if cat != nil {
						feed.CategoryID = &cat.ID
					}
				}

				// 检查是否已存在
				existing, _ := appDB.GetFeedByURL(feed.URL)
				if existing == nil {
					_, err := appDB.CreateFeed(feed)
					if err == nil {
						imported++
					}
				}
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("导入完成：新增 %d 个订阅源", imported),
	})
}

// importCSV 导入 CSV 格式数据
func importCSV(w http.ResponseWriter, r *http.Request, file io.Reader) {
	content, err := io.ReadAll(file)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "读取文件失败: " + err.Error()})
		return
	}

	// 移除 BOM
	content = bytes.TrimPrefix(content, []byte("\xEF\xBB\xBF"))

	lines := strings.Split(string(content), "\n")
	imported := 0

	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // 跳过表头和空行
		}

		// 简单 CSV 解析（假设没有逗号在字段内）
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			title := strings.TrimSpace(parts[0])
			url := strings.TrimSpace(parts[1])

			if url != "" && strings.HasPrefix(url, "http") {
				feed := &models.Feed{
					Title: title,
					URL:   url,
				}

				// 检查是否已存在
				existing, _ := appDB.GetFeedByURL(feed.URL)
				if existing == nil {
					_, err := appDB.CreateFeed(feed)
					if err == nil {
						imported++
					}
				}
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("导入完成：新增 %d 个订阅源", imported),
	})
}

// CleanupData 清理数据
func CleanupData(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	cleanupType := r.FormValue("type")

	var result map[string]interface{}

	switch cleanupType {
	case "all_articles":
		// 清空所有文章和标签
		if _, err := appDB.Exec("DELETE FROM article_tags"); err != nil {
			http.Error(w, "Failed to delete article tags: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := appDB.Exec("DELETE FROM articles"); err != nil {
			http.Error(w, "Failed to delete articles: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := appDB.Exec("DELETE FROM tags"); err != nil {
			http.Error(w, "Failed to delete tags: "+err.Error(), http.StatusInternalServerError)
			return
		}
		result = map[string]interface{}{
			"success": true,
			"message": "已清空所有文章和标签",
		}

	case "old_articles":
		days := r.FormValue("days")
		if days == "" {
			days = "30"
		}
		res, err := appDB.Exec(fmt.Sprintf("DELETE FROM articles WHERE fetched_at < datetime('now', '-%s days')", days))
		if err != nil {
			http.Error(w, "Failed to delete old articles: "+err.Error(), http.StatusInternalServerError)
			return
		}
		affected, _ := res.RowsAffected()
		result = map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("已删除 %d 天前的 %d 篇文章", parseInt(days, 30), affected),
		}

	case "ad_articles":
		res, err := appDB.Exec("DELETE FROM articles WHERE is_ad = 1")
		if err != nil {
			http.Error(w, "Failed to delete ad articles: "+err.Error(), http.StatusInternalServerError)
			return
		}
		affected, _ := res.RowsAffected()
		result = map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("已删除 %d 篇广告文章", affected),
		}

	case "embeddings":
		_, err := appDB.Exec("UPDATE articles SET embedding = NULL")
		if err != nil {
			http.Error(w, "Failed to clear embeddings: "+err.Error(), http.StatusInternalServerError)
			return
		}
		result = map[string]interface{}{
			"success": true,
			"message": "已清理所有向量缓存",
		}

	case "failed_notifications":
		res, err := appDB.Exec("DELETE FROM notifications WHERE status = 'failed'")
		if err != nil {
			http.Error(w, "Failed to delete failed notifications: "+err.Error(), http.StatusInternalServerError)
			return
		}
		affected, _ := res.RowsAffected()
		result = map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("已删除 %d 条失败通知", affected),
		}

	default:
		result = map[string]interface{}{
			"success": true,
			"message": "清理完成",
		}
	}

	// 返回 HTML 格式供 HTMX 渲染
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if success, ok := result["success"].(bool); ok && success {
		fmt.Fprintf(w, `<span class="text-green-600">✓ %s</span>`, result["message"])
	} else {
		fmt.Fprintf(w, `<span class="text-red-600">✗ %s</span>`, result["message"])
	}
}

// ResetData 重置数据
func ResetData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if appDB == nil {
		fmt.Fprintf(w, `<span class="text-red-600">✗ 数据库未初始化</span>`)
		return
	}

	// 删除所有数据表的内容（按依赖顺序）
	tables := []string{
		"event_articles",
		"event_tracks",
		"follow_rule_notifications",
		"notifications",
		"report_articles",
		"reports",
		"article_tags",
		"articles",
		"tags",
		"follow_rules",
		"topic_prompts",
		"feeds",
		"categories",
	}

	for _, table := range tables {
		if _, err := appDB.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			fmt.Fprintf(w, `<span class="text-red-600">✗ 重置表 %s 失败: %v</span>`, table, err)
			return
		}
	}

	// 重置自增 ID
	for _, table := range tables {
		appDB.Exec(fmt.Sprintf("DELETE FROM sqlite_sequence WHERE name='%s'", table))
	}

	// 执行 VACUUM 释放磁盘空间
	if _, err := appDB.Exec("VACUUM"); err != nil {
		fmt.Fprintf(w, `<span class="text-yellow-600">✓ 数据库已重置，但压缩失败: %v</span>`, err)
		return
	}

	fmt.Fprintf(w, `<span class="text-green-600">✓ 数据库已重置并压缩</span>`)
}

// VacuumDB 压缩数据库
func VacuumDB(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if appDB == nil {
		fmt.Fprintf(w, `<span class="text-red-600">✗ 数据库未初始化</span>`)
		return
	}

	// VACUUM 需要独占访问，尝试设置繁忙超时
	appDB.Exec("PRAGMA busy_timeout = 30000")

	if _, err := appDB.Exec("VACUUM"); err != nil {
		// 如果仍然失败，提供手动压缩方法
		fmt.Fprintf(w, `<span class="text-yellow-600">⚠ 压缩失败: %v<br><small>请在服务器停止后，使用 sqlite3 命令行工具手动执行: <code>sqlite3 data/rss.db "VACUUM"</code></small></span>`, err)
		return
	}

	fmt.Fprintf(w, `<span class="text-green-600">✓ 数据库已压缩，磁盘空间已释放</span>`)
}

// RefreshAllFeeds 刷新所有订阅
func RefreshAllFeeds(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "refreshing"})
}

// RefreshFeedInternal 内部刷新单个订阅源（供调度器调用）
func RefreshFeedInternal(feedID int64) error {
	if appDB == nil {
		return fmt.Errorf("database not initialized")
	}

	feed, err := appDB.GetFeedByID(feedID)
	if err != nil {
		return fmt.Errorf("feed not found: %w", err)
	}

	// 异步抓取
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()

		source, err := crawler.NewSource(feed.URL, feed.SourceType, feed.SourceConfig)
		if err != nil {
			fmt.Printf("Failed to create source for feed %s: %v\n", feed.URL, err)
			return
		}

		parsedFeed, err := source.FetchAndParse(ctx)
		if err != nil {
			fmt.Printf("Failed to fetch feed %s: %v\n", feed.URL, err)
			return
		}

		fmt.Printf("Fetched feed %s: %d items\n", feed.Title, len(parsedFeed.Items))

		// 保存文章到数据库
		newCount := 0
		var newArticles []struct {
			id          int64
			title       string
			content     string
			description string
		}

		for _, item := range parsedFeed.Items {
			exists, _ := appDB.ArticleExists(item.Link)
			if exists {
				fmt.Printf("Article already exists: %s\n", item.Link)
				continue
			}

			fmt.Printf("Creating article: %s (link: %s)\n", item.Title, item.Link)

			// 使用 Content，如果为空则回退到 Description
			content := item.Content
			if content == "" {
				content = item.Description
			// Apply per-feed content filter rules
			content = crawler.ApplyContentFilter(content, feed.ContentFilter)
			}

			// 解析发布时间 - 优先使用 gofeed 已解析的时间
			var publishedAt *time.Time
			if item.PublishedParsed != nil {
				publishedAt = item.PublishedParsed
			} else if item.Published != "" {
				if t, err := time.Parse(time.RFC1123, item.Published); err == nil {
					publishedAt = &t
				} else if t, err := time.Parse(time.RFC1123Z, item.Published); err == nil {
					publishedAt = &t
				} else if t, err := time.Parse(time.RFC3339, item.Published); err == nil {
					publishedAt = &t
				} else if t, err := time.Parse("2006-01-02 15:04:05", item.Published); err == nil {
					publishedAt = &t
				}
			}

			article := &models.Article{
				FeedID:      feed.ID,
				CategoryID:  feed.CategoryID,
				Title:       item.Title,
				Link:        item.Link,
				Content:     content,
				Summary:     item.Description,
				PublishedAt: publishedAt,
			}

			articleID, err := appDB.CreateArticle(article)
			if err != nil {
				fmt.Printf("Failed to create article %s: %v\n", item.Link, err)
				continue
			}
			newCount++

			newArticles = append(newArticles, struct {
				id          int64
				title       string
				content     string
				description string
			}{articleID, item.Title, content, item.Description})
		}

		fmt.Printf("Feed %s refreshed, %d new articles\n", feed.Title, newCount)

		// 更新最后抓取时间
		appDB.UpdateFeedLastFetched(feed.ID)

		// 串行处理 AI 分析
		if appAnalyzer != nil && len(newArticles) > 0 {
			articleInterval := 2 * time.Second
			if appConfig != nil {
				articleInterval = appConfig.AI.RateLimit.ArticleInterval
			}

			for i, art := range newArticles {
				if i > 0 {
					time.Sleep(articleInterval)
				}
				analyzeArticleAsync(art.id, art.title, art.content, art.description)
			}
		}
	}()

	return nil
}

// ProcessPendingArticlesInternal 内部处理待分析文章（供调度器调用）
func ProcessPendingArticlesInternal(articles []*models.Article) {
	if appAnalyzer == nil || len(articles) == 0 {
		return
	}

	articleInterval := 2 * time.Second
	if appConfig != nil {
		articleInterval = appConfig.AI.RateLimit.ArticleInterval
	}

	for i, article := range articles {
		if i > 0 {
			time.Sleep(articleInterval)
		}

		text := article.Content
		if text == "" {
			text = article.Summary
		}
		if text == "" {
			continue
		}

		if len(text) > 4000 {
			text = text[:4000]
		}

		analyzeArticleAsync(article.ID, article.Title, text, "")
	}

	fmt.Printf("Processed %d pending articles\n", len(articles))
}

// ProcessIncompleteArticlesInternal 内部处理不完整的文章（供调度器调用）
func ProcessIncompleteArticlesInternal(articles []*models.Article) {
	if appAnalyzer == nil || len(articles) == 0 {
		return
	}

	articleInterval := 2 * time.Second
	if appConfig != nil {
		articleInterval = appConfig.AI.RateLimit.ArticleInterval
	}

	for i, article := range articles {
		if i > 0 {
			time.Sleep(articleInterval)
		}

		text := article.Content
		if text == "" {
			text = article.Summary
		}
		if text == "" {
			continue
		}

		if len(text) > 4000 {
			text = text[:4000]
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		// 检查文章缺少哪些字段
		needsReanalyze := article.OneLineSummary == "" || article.Entities == ""
		needsEmbedding := len(article.SummaryEmbedding) == 0 && article.OneLineSummary != ""

		// 如果只需要生成向量（已有 one_line_summary）
		if needsEmbedding && !needsReanalyze && article.OneLineSummary != "" {
			summaryEmb, err := appAnalyzer.GetEmbedding(ctx, article.OneLineSummary)
			if err == nil {
				appDB.UpdateArticleSummaryEmbedding(article.ID, summaryEmb)
				fmt.Printf("Generated summary embedding for incomplete article %d (%d/%d)\n", article.ID, i+1, len(articles))
			}
			cancel()
			continue
		}

		// 需要重新 AI 分析
		result, err := appAnalyzer.AnalyzeArticle(ctx, article.Title, text)
		if err != nil {
			fmt.Printf("Failed to reanalyze incomplete article %d: %v\n", article.ID, err)
			appDB.IncrementProcessAttempts(article.ID, err.Error())
			cancel()
			continue
		}

		// 更新文章 AI 分析结果
		keywords := strings.Join(result.Keywords, ",")
		tagsCache := strings.Join(result.Tags, ",")
		entities := strings.Join(result.Entities, ",")

		if err := appDB.UpdateArticleAI(&models.AIUpdateParams{
			ID:              article.ID,
			AISummary:       result.Summary,
			OneLineSummary:  result.OneLineSummary,
			Keywords:        keywords,
			TagsCache:       tagsCache,
			IsAd:            result.IsAd,
			AdReason:        result.AdReason,
			ImportanceScore: result.ImportanceScore,
			TopicCategory:   result.TopicCategory,
			Entities:        entities,
			TranslatedContent: result.TranslatedContent,
		}); err != nil {
			fmt.Printf("Failed to update incomplete article AI data %d: %v\n", article.ID, err)
			cancel()
			continue
		}

		// 生成总结向量
		if result.OneLineSummary != "" {
			summaryEmb, err := appAnalyzer.GetEmbedding(ctx, result.OneLineSummary)
			if err == nil {
				appDB.UpdateArticleSummaryEmbedding(article.ID, summaryEmb)
			}
		}

		cancel()
		fmt.Printf("Reanalyzed incomplete article %d (%d/%d)\n", article.ID, i+1, len(articles))
	}

	fmt.Printf("Processed %d incomplete articles\n", len(articles))
}

// TestSource 测试源配置（返回前 5 条预览）
func TestSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceType   string `json:"source_type"`
		SourceConfig string `json:"source_config"`
		URL          string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	source, err := crawler.NewSource(req.URL, req.SourceType, req.SourceConfig)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	feed, err := source.FetchAndParse(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	preview := feed.Items
	if len(preview) > 5 {
		preview = preview[:5]
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"title":      feed.Title,
		"item_count": len(feed.Items),
		"preview":    preview,
	})
}

// ParseCurl 解析 curl 命令
func ParseCurl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := crawler.ParseCurlCommand(req.Command)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// writeJSON 辅助函数：写入 JSON 响应
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ========== 主题模板管理 API ==========

// ListTopicPrompts 获取主题模板列表
func ListTopicPrompts(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	prompts, err := appDB.ListTopicPrompts()
	if err != nil {
		http.Error(w, "Failed to list topic prompts: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"prompts": prompts,
	})
}

// GetTopicPrompt 获取单个主题模板
func GetTopicPrompt(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	prompt, err := appDB.GetTopicPromptByID(id)
	if err != nil {
		http.Error(w, "Topic prompt not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(prompt)
}

// CreateTopicPrompt 创建主题模板
func CreateTopicPrompt(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if r.Body == nil {
		http.Error(w, "Request body is required", http.StatusBadRequest)
		return
	}

	var prompt models.TopicPrompt
	if err := json.NewDecoder(r.Body).Decode(&prompt); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 验证必填字段
	if prompt.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}
	if prompt.Persona == "" {
		http.Error(w, "Persona is required", http.StatusBadRequest)
		return
	}
	if prompt.PromptTemplate == "" {
		http.Error(w, "PromptTemplate is required", http.StatusBadRequest)
		return
	}

	// 设置默认值
	prompt.IsActive = true

	id, err := appDB.CreateTopicPrompt(&prompt)
	if err != nil {
		http.Error(w, "Failed to create topic prompt: "+err.Error(), http.StatusInternalServerError)
		return
	}

	prompt.ID = id
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(prompt)
}

// UpdateTopicPrompt 更新主题模板
func UpdateTopicPrompt(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if r.Body == nil {
		http.Error(w, "Request body is required", http.StatusBadRequest)
		return
	}

	var prompt models.TopicPrompt
	if err := json.NewDecoder(r.Body).Decode(&prompt); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	prompt.ID = id
	if err := appDB.UpdateTopicPrompt(&prompt); err != nil {
		http.Error(w, "Failed to update topic prompt: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(prompt)
}

// DeleteTopicPrompt 删除主题模板（软删除）
func DeleteTopicPrompt(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	// 软删除：设置为非激活状态
	prompt, err := appDB.GetTopicPromptByID(id)
	if err != nil {
		http.Error(w, "Topic prompt not found", http.StatusNotFound)
		return
	}
	prompt.IsActive = false
	if err := appDB.UpdateTopicPrompt(prompt); err != nil {
		http.Error(w, "Failed to delete topic prompt: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Topic prompt deleted",
	})
}

// ReloadConfig 重新加载配置文件
func ReloadConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Reload()
	if err != nil {
		http.Error(w, "Failed to reload config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Config reloaded successfully",
	})

	// 记录日志
	log.Printf("配置已通过 API 重新加载: %+v", cfg)
}

// GetConfig 获取当前配置
func GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := config.GetCurrentConfig()
	if cfg == nil {
		http.Error(w, "Config not loaded", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(cfg)
}

// ========== 事件追踪 API ==========

// ListEventTracks 列出所有事件
func ListEventTracks(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	status := r.URL.Query().Get("status")
	events, err := appDB.ListEventTracks(status)
	if err != nil {
		http.Error(w, "Failed to list events: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(events)
}

// ListPendingEvents 列出待关注事件
func ListPendingEvents(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	events, err := appDB.ListEventTracks("pending")
	if err != nil {
		http.Error(w, "Failed to list pending events: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(events)
}

// CreateEventTrackRequest 创建事件请求
type CreateEventTrackRequest struct {
	Name             string `json:"name"`
	Keywords         string `json:"keywords"`
	NegativeKeywords string `json:"negative_keywords"`
	Description      string `json:"description"`
	Roles            string `json:"roles"`
	IsAuto           bool   `json:"is_auto"`
}

// CreateEventTrack 创建事件
func CreateEventTrack(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	var req CreateEventTrackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Event name is required", http.StatusBadRequest)
		return
	}

	event := &models.EventTrack{
		Name:             req.Name,
		Keywords:         req.Keywords,
		NegativeKeywords: req.NegativeKeywords,
		Description:      req.Description,
		Roles:            req.Roles,
		Status:           "active", // 手动创建的事件直接激活
		IsAuto:           req.IsAuto,
		MatchCount:       0,
	}

	// 生成事件向量（优先用描述，描述最能表达追踪意图）
	if appAnalyzer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// 只用描述做向量化，因为描述最能准确表达要追踪的内容
		embeddingText := req.Description
		if embeddingText == "" {
			embeddingText = req.Name + " " + req.Keywords
		}
		if embeddingText != "" {
			embedding, err := appAnalyzer.GetEmbedding(ctx, embeddingText)
			if err == nil {
				event.Embedding = embedding
			}
		}
	}

	id, err := appDB.CreateEventTrack(event)
	if err != nil {
		http.Error(w, "Failed to create event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	event.ID = id
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(event)
}

// GetEventTrack 获取单个事件
func GetEventTrack(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	event, err := appDB.GetEventTrack(id)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(event)
}

// UpdateEventTrackRequest 更新事件请求
type UpdateEventTrackRequest struct {
	Name             string `json:"name"`
	Status           string `json:"status"`
	Keywords         string `json:"keywords"`
	NegativeKeywords string `json:"negative_keywords"`
	Description      string `json:"description"`
	Roles            string `json:"roles"`
}

// UpdateEventTrack 更新事件
func UpdateEventTrack(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	var req UpdateEventTrackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	event, err := appDB.GetEventTrack(id)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	if req.Name != "" {
		event.Name = req.Name
	}
	if req.Status != "" {
		event.Status = req.Status
	}
	if req.Keywords != "" {
		event.Keywords = req.Keywords
	}
	// 负面关键词可以为空字符串（清除），所以始终更新
	event.NegativeKeywords = req.NegativeKeywords
	event.Description = req.Description
	event.Roles = req.Roles

	// 重新生成向量（只用描述，描述最能表达追踪意图）
	if appAnalyzer != nil {
		embeddingText := req.Description
		if embeddingText == "" {
			embeddingText = event.Name + " " + event.Keywords
		}
		if embeddingText != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			embedding, err := appAnalyzer.GetEmbedding(ctx, embeddingText)
			if err == nil {
				event.Embedding = embedding
			}
		}
	}

	if err := appDB.UpdateEventTrack(event); err != nil {
		http.Error(w, "Failed to update event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(event)
}

// DeleteEventTrack 删除事件
func DeleteEventTrack(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if err := appDB.DeleteEventTrack(id); err != nil {
		http.Error(w, "Failed to delete event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Event deleted",
	})
}

// ActivateEventTrack 激活事件（从 pending 变为 active）
func ActivateEventTrack(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if err := appDB.UpdateEventTrackStatus(id, "active"); err != nil {
		http.Error(w, "Failed to activate event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 为事件生成向量（如果还没有），只用描述做向量化
	event, err := appDB.GetEventTrack(id)
	if err == nil && len(event.Embedding) == 0 && appAnalyzer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// 只用描述做向量化，描述最能表达追踪意图
		embeddingText := event.Description
		if embeddingText == "" {
			embeddingText = event.Name + " " + event.Keywords
		}
		if embeddingText != "" {
			embedding, err := appAnalyzer.GetEmbedding(ctx, embeddingText)
			if err == nil {
				appDB.UpdateEventTrackEmbedding(id, embedding)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Event activated",
	})
}

// PauseEventTrack 暂停事件
func PauseEventTrack(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if err := appDB.UpdateEventTrackStatus(id, "paused"); err != nil {
		http.Error(w, "Failed to pause event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Event paused",
	})
}

// CompleteEventTrack 完成事件
func CompleteEventTrack(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	if err := appDB.UpdateEventTrackStatus(id, "completed"); err != nil {
		http.Error(w, "Failed to complete event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Event completed",
	})
}

// clusterSimilarArticles 聚类相似文章
func clusterSimilarArticles(articles []*models.Article, threshold float64) []*ArticleCluster {
	// 预处理：反序列化所有向量
	vectors := make([][]float32, len(articles))
	for i, article := range articles {
		if len(article.SummaryEmbedding) > 0 {
			vec, err := ai.DeserializeEmbedding(article.SummaryEmbedding)
			if err == nil {
				vectors[i] = vec
			}
		}
	}

	// 标记已聚类的文章
	clustered := make(map[int64]bool)
	var result []*ArticleCluster

	for i, article := range articles {
		if clustered[article.ID] {
			continue
		}

		cluster := &ArticleCluster{
			Article:         article,
			SimilarArticles: nil,
		}

		// 查找相似文章
		if vectors[i] != nil {
			for j, other := range articles {
				if i == j || clustered[other.ID] || vectors[j] == nil {
					continue
				}
				similarity := ai.CalculateCosineSimilarity(vectors[i], vectors[j])
				if similarity >= threshold {
					cluster.SimilarArticles = append(cluster.SimilarArticles, SimilarArticle{
						ID:    other.ID,
						Title: other.Title,
						Link:  other.Link,
					})
					clustered[other.ID] = true
				}
			}
		}

		clustered[article.ID] = true
		result = append(result, cluster)
	}

	return result
}

// MatchSingleEventArticles 针对单个事件匹配文章
func MatchSingleEventArticles(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if appEventMatcher == nil {
		http.Error(w, "Event matcher not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	eventID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	// 获取事件
	event, err := appDB.GetEventTrack(eventID)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	// 检查是否需要清除旧数据
	clearOld := r.URL.Query().Get("clear") == "true"
	if clearOld {
		// 清除该事件的旧匹配记录
		if err := appDB.DeleteEventArticles(eventID); err != nil {
			http.Error(w, "Failed to clear old matches: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// 重置匹配计数
		if err := appDB.ResetEventMatchCount(eventID); err != nil {
			http.Error(w, "Failed to reset match count: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 获取所有有向量的文章（优先使用 summary_embedding），扩大到最近7天
	articles, err := appDB.GetRecentArticlesWithEmbedding(168 * time.Hour)
	if err != nil {
		http.Error(w, "Failed to get articles: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 异步处理
	go func() {
		matchedCount := 0
		for _, article := range articles {
			// 优先使用 summary_embedding，其次使用 embedding
			if len(article.SummaryEmbedding) > 0 || len(article.Embedding) > 0 {
				if err := appEventMatcher.MatchArticleToSingleEvent(article.ID, article.Title, article.Content, article.Embedding, article.SummaryEmbedding, event); err == nil {
					matchedCount++
				}
			}
		}
		log.Printf("Single event %d matching completed: %d articles matched", eventID, matchedCount)
	}()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message":  fmt.Sprintf("开始为事件 '%s' 匹配文章", event.Name),
		"event_id": eventID,
		"clear_old": clearOld,
	})
}

// GetEventArticles 获取事件关联的文章
func GetEventArticles(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	role := r.URL.Query().Get("role")
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil {
			offset = parsed
		}
	}

	articles, err := appDB.GetEventArticles(id, role, limit, offset)
	if err != nil {
		http.Error(w, "Failed to get event articles: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 聚类相似文章（相似度阈值0.9）
	clustered := clusterSimilarArticles(articles, 0.9)

	total, _ := appDB.CountEventArticles(id, role)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"articles": clustered,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// GetEventStats 获取事件统计
func GetEventStats(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	event, err := appDB.GetEventTrack(id)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	roleStats, err := appDB.GetEventRoleStats(id)
	if err != nil {
		roleStats = []*models.EventRoleStats{}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"event":      event,
		"role_stats": roleStats,
	})
}

// DetectHotTopics 检测热点事件
func DetectHotTopics(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 创建热点检测器
	detector := processor.NewHotTopicDetector(appDB, appAnalyzer)

	// 执行热点检测
	candidates, err := detector.DetectHotTopics(r.Context())
	if err != nil {
		http.Error(w, "Failed to detect hot topics: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 自动创建待关注事件
	var createdEvents []*models.EventTrack
	for _, candidate := range candidates {
		event, err := detector.CreateEventFromCandidate(candidate)
		if err != nil {
			log.Printf("Failed to create event from candidate: %v", err)
			continue
		}
		createdEvents = append(createdEvents, event)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"candidates":     candidates,
		"created_events": createdEvents,
		"message":        fmt.Sprintf("检测到 %d 个热点，创建了 %d 个待关注事件", len(candidates), len(createdEvents)),
	})
}

// AutoPauseInactiveEvents 自动暂停长时间无更新的活跃事件
func AutoPauseInactiveEvents(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	// 解析参数：无更新天数阈值（默认7天）
	inactiveDays := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			inactiveDays = parsed
		}
	}

	// 获取长时间无更新的事件
	events, err := appDB.GetInactiveEventTracks(inactiveDays)
	if err != nil {
		http.Error(w, "Failed to get inactive events: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 自动暂停这些事件
	var pausedIDs []int64
	for _, event := range events {
		if err := appDB.UpdateEventTrackStatus(event.ID, "paused"); err != nil {
			log.Printf("Failed to pause event %d: %v", event.ID, err)
			continue
		}
		pausedIDs = append(pausedIDs, event.ID)
		log.Printf("自动暂停事件: %s (ID: %d, 超过 %d 天无更新)", event.Name, event.ID, inactiveDays)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"paused_ids": pausedIDs,
		"count":      len(pausedIDs),
		"message":    fmt.Sprintf("已自动暂停 %d 个超过 %d 天无更新的事件", len(pausedIDs), inactiveDays),
	})
}

// MatchArticlesToEvents 批量匹配文章到事件
func MatchArticlesToEvents(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if appEventMatcher == nil {
		http.Error(w, "Event matcher not initialized", http.StatusInternalServerError)
		return
	}

	// 检查是否需要清除旧数据
	clearOld := r.URL.Query().Get("clear") == "true"
	if clearOld {
		// 清除所有旧的匹配数据
		result, err := appDB.Exec("DELETE FROM event_articles")
		if err != nil {
			http.Error(w, "Failed to clear old matches: "+err.Error(), http.StatusInternalServerError)
			return
		}
		deleted, _ := result.RowsAffected()
		log.Printf("清除了 %d 条旧的匹配记录", deleted)

		// 重置事件的匹配计数
		if _, err := appDB.Exec("UPDATE event_tracks SET match_count = 0"); err != nil {
			http.Error(w, "Failed to reset match count: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Println("已重置所有事件的匹配计数")
	}

	// 获取所有活跃事件
	events, err := appDB.GetActiveEventTracks()
	if err != nil {
		http.Error(w, "Failed to get active events: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(events) == 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"matched": 0,
			"message": "没有活跃的事件",
		})
		return
	}

	// 获取所有有关键词或embedding的文章
	articles, err := appDB.GetRecentArticlesWithEmbedding(168 * time.Hour) // 最近7天
	if err != nil {
		http.Error(w, "Failed to get articles: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("开始批量匹配 %d 篇文章到 %d 个事件", len(articles), len(events))

	matchedCount := 0
	for _, article := range articles {
		if err := appEventMatcher.MatchArticleToEvents(article.ID, article.Title, article.Content, article.Embedding, article.SummaryEmbedding); err != nil {
			log.Printf("Failed to match article %d: %v", article.ID, err)
			continue
		}
		matchedCount++
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"matched":  matchedCount,
		"events":   len(events),
		"message":  fmt.Sprintf("已匹配 %d 篇文章到 %d 个事件", matchedCount, len(events)),
	})
}

// OptimizeEventDescription 用 LLM 优化事件描述
func OptimizeEventDescription(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if appAnalyzer == nil {
		http.Error(w, "AI analyzer not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	// 解析请求体中的改进建议（可选）
	var reqBody struct {
		Recommendations []string `json:"recommendations"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&reqBody)
	}

	// 获取事件
	event, err := appDB.GetEventTrack(id)
	if err != nil {
		http.Error(w, "Event not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// 获取事件关联的文章（用于分析）
	articles, err := appDB.GetEventArticles(id, "", 10, 0)
	if err != nil {
		log.Printf("Failed to get event articles: %v", err)
	}

	// 构建已匹配文章摘要（用于分析匹配质量）
	var articleSummaries string
	if len(articles) > 0 {
		var summaries []string
		for i, a := range articles {
			if i >= 5 {
				break
			}
			summaries = append(summaries, fmt.Sprintf("- %s", a.Title))
		}
		articleSummaries = "\n\n已匹配的文章标题（参考）：\n" + strings.Join(summaries, "\n")
	}

	// 构建改进建议部分（如果有）
	var recommendationsText string
	if len(reqBody.Recommendations) > 0 {
		recommendationsText = "\n\n【质量检查改进建议】（请重点参考这些建议进行优化）：\n- " + strings.Join(reqBody.Recommendations, "\n- ")
	}

	// 构建当前描述部分
	currentDesc := event.Description
	if currentDesc == "" {
		currentDesc = "(暂无描述)"
	}

	// 构建 LLM 提示 - 使用专业的优化提示词
	prompt := fmt.Sprintf(`请根据我提供的事件基础信息，完成以下优化任务，所有内容必须客观、精炼、结构统一，适合向量化处理与向量数据库相似度匹配，无主观情绪、无冗余信息。

【优化任务】
1. 优化标题：精准、概括、可检索、适合向量匹配。
2. 优化正向关键词：提炼核心、去重、聚焦、便于聚类，控制在5-10个。
3. 优化负面关键词：识别与该事件主题不相关或应排除的内容类型，控制在3-5个。例如：如果追踪"AI大模型"，负面关键词可以包括"硬件制造、融资新闻、能源电力"等容易误匹配但不相关的内容。
4. 优化事件描述：100–200字，包含事件主题、核心参与方、领域、性质、动态特征，句式稳定，便于语义匹配。
5. 需关注对象：明确需持续追踪的主体、维度、信号。

【当前事件信息】
- 事件名称：%s
- 当前描述：%s
- 正向关键词：%s
- 当前负面关键词：%s
- 相关人/物：%s
%s%s

请严格按以下JSON格式输出，不要多余内容：
{"title":"优化后的标题","keywords":"正向关键词1,正向关键词2,...","negative_keywords":"负面关键词1,负面关键词2,...","description":"100-200字的事件描述","roles":"需关注的对象1,对象2,...","match_suggestions":"匹配改进建议"}`, event.Name, currentDesc, event.Keywords, event.NegativeKeywords, event.Roles, articleSummaries, recommendationsText)

	// 调用 LLM（使用深度思考模式，thinkingBudget=10000）
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	response, err := appAnalyzer.ChatWithDeepThinking(ctx, prompt, 10000)
	if err != nil {
		http.Error(w, "LLM request failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 解析 LLM 返回的 JSON（兼容中文和英文字段名）
	var optimized struct {
		Title               string `json:"title"`
		TitleCN             string `json:"优化后的标题"`
		Description         string `json:"description"`
		DescriptionCN       string `json:"优化后描述"`
		Keywords            string `json:"keywords"`
		KeywordsCN          string `json:"优化后关键词"`
		NegativeKeywords    string `json:"negative_keywords"`
		NegativeKeywordsCN  string `json:"负面关键词"`
		Roles               string `json:"roles"`
		RolesCN             string `json:"需关注的主体"`
		MatchSuggestions    string `json:"match_suggestions"`
		MatchSuggestionsCN  string `json:"匹配改进建议"`
	}

	// 清理响应（去除 markdown 代码块）
	cleanResponse := strings.TrimSpace(response)
	if strings.HasPrefix(cleanResponse, "```") {
		// 去除 markdown 代码块
		lines := strings.Split(cleanResponse, "\n")
		var cleanLines []string
		for i, line := range lines {
			if i == 0 && strings.HasPrefix(line, "```") {
				continue
			}
			if strings.HasPrefix(line, "```") {
				continue
			}
			cleanLines = append(cleanLines, line)
		}
		cleanResponse = strings.Join(cleanLines, "\n")
	}

	if err := json.Unmarshal([]byte(cleanResponse), &optimized); err != nil {
		// 尝试提取 JSON
		start := strings.Index(cleanResponse, "{")
		end := strings.LastIndex(cleanResponse, "}")
		if start != -1 && end != -1 && end > start {
			if err := json.Unmarshal([]byte(cleanResponse[start:end+1]), &optimized); err != nil {
				http.Error(w, "Failed to parse LLM response: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "Failed to parse LLM response: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 兼容中英文字段名
	title := optimized.Title
	if title == "" {
		title = optimized.TitleCN
	}
	description := optimized.Description
	if description == "" {
		description = optimized.DescriptionCN
	}
	keywords := optimized.Keywords
	if keywords == "" {
		keywords = optimized.KeywordsCN
	}
	negativeKeywords := optimized.NegativeKeywords
	if negativeKeywords == "" {
		negativeKeywords = optimized.NegativeKeywordsCN
	}
	roles := optimized.Roles
	if roles == "" {
		roles = optimized.RolesCN
	}
	matchSuggestions := optimized.MatchSuggestions
	if matchSuggestions == "" {
		matchSuggestions = optimized.MatchSuggestionsCN
	}

	// 更新事件
	event.Name = title
	event.Description = description
	event.Keywords = keywords
	event.NegativeKeywords = negativeKeywords
	event.Roles = roles
	event.UpdatedAt = time.Now()

	if err := appDB.UpdateEventTrack(event); err != nil {
		http.Error(w, "Failed to update event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 重新生成事件向量
	if len(articles) > 0 {
		go func() {
			// 使用优化后的描述生成新向量
			embeddingText := event.Name + " " + event.Description + " " + event.Keywords
			embedding, err := appAnalyzer.GetEmbedding(context.Background(), embeddingText)
			if err == nil {
				appDB.UpdateEventTrackEmbedding(event.ID, embedding)
				log.Printf("Updated embedding for event %d after optimization", event.ID)
			}
		}()
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"title":             title,
		"description":       description,
		"keywords":          keywords,
		"negative_keywords": negativeKeywords,
		"roles":             roles,
		"match_suggestions": matchSuggestions,
		"raw_response":      response,
		"message":           "事件描述已优化",
	})
}

// CheckEventMatchQuality 用 LLM 检查事件匹配质量
func CheckEventMatchQuality(w http.ResponseWriter, r *http.Request) {
	if appDB == nil {
		http.Error(w, "Database not initialized", http.StatusInternalServerError)
		return
	}

	if appAnalyzer == nil {
		http.Error(w, "AI analyzer not initialized", http.StatusInternalServerError)
		return
	}

	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid event ID", http.StatusBadRequest)
		return
	}

	// 获取事件
	event, err := appDB.GetEventTrack(id)
	if err != nil {
		http.Error(w, "Event not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// 获取匹配的文章
	articles, err := appDB.GetEventArticles(id, "", 10, 0)
	if err != nil {
		http.Error(w, "Failed to get matched articles: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(articles) == 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"event":        event.Name,
			"total_matches": 0,
			"analysis":     "该事件暂无匹配文章",
			"recommendations": []string{"请添加更多相关关键词", "检查事件描述是否准确"},
		})
		return
	}

	// 构建文章摘要
	var articleSummaries []string
	for i, a := range articles {
		summary := a.Title
		if len(a.Summary) > 200 {
			summary += " - " + a.Summary[:200] + "..."
		} else if a.Summary != "" {
			summary += " - " + a.Summary
		}
		articleSummaries = append(articleSummaries, fmt.Sprintf("%d. %s (得分:%.2f)", i+1, summary, a.MatchScore))
	}

	// 构建 LLM 提示
	prompt := fmt.Sprintf(`请分析以下事件追踪的匹配质量。

事件信息：
- 名称：%s
- 描述：%s
- 关键词：%s

已匹配的文章（共%d篇）：
%s

请分析并返回JSON格式结果：
1. relevance_score: 匹配相关性评分（0-100分）
2. analysis: 分析匹配质量的简要说明（100-300字）
3. good_matches: 匹配得很好的文章编号列表
4. bad_matches: 匹配不当的文章编号列表
5. recommendations: 改进建议列表

只返回JSON，不要其他内容。`,
		event.Name, event.Description, event.Keywords,
		len(articles), strings.Join(articleSummaries, "\n"))

	// 调用 LLM
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := appAnalyzer.Chat(ctx, prompt)
	if err != nil {
		http.Error(w, "LLM request failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 解析 LLM 返回的 JSON
	var analysis struct {
		RelevanceScore  int      `json:"relevance_score"`
		Analysis        string   `json:"analysis"`
		GoodMatches     []int    `json:"good_matches"`
		BadMatches      []int    `json:"bad_matches"`
		Recommendations []string `json:"recommendations"`
	}

	// 清理响应
	cleanResponse := strings.TrimSpace(response)
	if strings.HasPrefix(cleanResponse, "```") {
		lines := strings.Split(cleanResponse, "\n")
		var cleanLines []string
		for i, line := range lines {
			if i == 0 && strings.HasPrefix(line, "```") {
				continue
			}
			if strings.HasPrefix(line, "```") {
				continue
			}
			cleanLines = append(cleanLines, line)
		}
		cleanResponse = strings.Join(cleanLines, "\n")
	}

	if err := json.Unmarshal([]byte(cleanResponse), &analysis); err != nil {
		start := strings.Index(cleanResponse, "{")
		end := strings.LastIndex(cleanResponse, "}")
		if start != -1 && end != -1 && end > start {
			if err := json.Unmarshal([]byte(cleanResponse[start:end+1]), &analysis); err != nil {
				http.Error(w, "Failed to parse LLM response: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, "Failed to parse LLM response: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"event":          event.Name,
		"total_matches":  len(articles),
		"relevance_score": analysis.RelevanceScore,
		"analysis":       analysis.Analysis,
		"good_matches":   analysis.GoodMatches,
		"bad_matches":    analysis.BadMatches,
		"recommendations": analysis.Recommendations,
		"raw_response":   response,
	})
}
