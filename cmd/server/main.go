package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"rss-ai/internal/ai"
	"rss-ai/internal/config"
	"rss-ai/internal/database"
	"rss-ai/internal/logger"
	"rss-ai/internal/notify"
	"rss-ai/internal/processor"
	"rss-ai/internal/server"
)

var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
)

func main() {
	// 解析命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	logLevel := flag.String("log-level", "", "日志级别 (DEBUG/INFO/WARN/ERROR)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("RSS AI Reader v%s (built: %s, commit: %s)\n", version, buildTime, gitCommit)
		os.Exit(0)
	}

	// 加载配置（支持热加载）
	cfg, err := config.LoadWithWatch(*configPath, func(c *config.Config) {
		logger.Info("配置已更新，重新加载")
		// 配置更新回调
		server.SetConfig(c)
		// 更新日志级别
		if c.Server.LogLevel != "" {
			logger.SetLevelFromString(c.Server.LogLevel)
		}
	})
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	// 初始化日志
	initLogger(cfg, *logLevel)

	logger.Info("Starting RSS AI Reader v%s", version)
	logger.Info("Server will listen on %s:%d", cfg.Server.Host, cfg.Server.Port)

	// 初始化数据库
	db, err := database.New(cfg.Database.Path)
	if err != nil {
		logger.Fatal("Failed to initialize database: %v", err)
	}
	defer db.Close()

	logger.Info("Database initialized: %s", cfg.Database.Path)

	// 初始化默认主题模板
	if err := db.InitDefaultTopicPrompts(); err != nil {
		logger.Warn("failed to init default topic prompts: %v", err)
	}

	// 初始化模板
	templateDir := "web/templates"
	if _, err := os.Stat(templateDir); os.IsNotExist(err) {
		templateDir = "./web/templates"
	}
	if err := server.InitTemplates(templateDir); err != nil {
		logger.Warn("Failed to load templates: %v (using fallback HTML)", err)
	}

	// 设置配置到服务器模块
	server.SetConfig(cfg)
	server.SetConfigPath(*configPath)
	server.SetDB(db)

	// 初始化 AI 分析器
	var analyzer *ai.Analyzer
	if cfg.AI.LLM.APIKey != "" || cfg.AI.Embedding.APIKey != "" {
		analyzer = ai.NewAnalyzer(&cfg.AI.LLM, &cfg.AI.Embedding, &cfg.AI.RateLimit, db.SQL())
		server.SetAnalyzer(analyzer)
		logger.Info("AI analyzer initialized")
	} else {
		logger.Info("AI analyzer disabled: missing API keys")
	}

	// 初始化通知管理器（只注册 enabled=true 的渠道）
	notifyMgr := notify.NewManager()
	var channelsList []string

	if cfg.Push.Gotify.Enabled && cfg.Push.Gotify.URL != "" && cfg.Push.Gotify.AppToken != "" {
		gotifySender := notify.NewGotifySender(&notify.GotifyConfig{
			URL:      cfg.Push.Gotify.URL,
			AppToken: cfg.Push.Gotify.AppToken,
			Priority: cfg.Push.Gotify.Priority,
		})
		notifyMgr.Register(notify.ChannelGotify, gotifySender)
		channelsList = append(channelsList, "gotify")
		logger.Info("Gotify notifier configured: %s", cfg.Push.Gotify.URL)
	}

	if cfg.Push.Email.Enabled && cfg.Push.Email.SMTPHost != "" && cfg.Push.Email.Username != "" {
		emailSender := notify.NewEmailSender(&notify.EmailConfig{
			SMTPHost: cfg.Push.Email.SMTPHost,
			SMTPPort: cfg.Push.Email.SMTPPort,
			Username: cfg.Push.Email.Username,
			Password: cfg.Push.Email.Password,
			From:     cfg.Push.Email.From,
			To:       cfg.Push.Email.To,
		})
		notifyMgr.Register(notify.ChannelEmail, emailSender)
		channelsList = append(channelsList, "email")
		logger.Info("Email notifier configured: %s", cfg.Push.Email.SMTPHost)
	}

	if cfg.Push.QQBot.Enabled && cfg.Push.QQBot.AppID != "" && cfg.Push.QQBot.AppSecret != "" {
		qqbotSender := notify.NewQQBotSender(&notify.QQBotConfig{
			AppID:     cfg.Push.QQBot.AppID,
			AppSecret: cfg.Push.QQBot.AppSecret,
			UserID:    cfg.Push.QQBot.UserID,
		})
		notifyMgr.Register(notify.ChannelQQBot, qqbotSender)
		channelsList = append(channelsList, "qqbot")
		logger.Info("QQBot notifier configured: app_id=%s", cfg.Push.QQBot.AppID)
	}

	if cfg.Push.Webhook.Enabled && cfg.Push.Webhook.URL != "" {
		webhookSender := notify.NewWebhookSender(&notify.WebhookConfig{
			URL:     cfg.Push.Webhook.URL,
			Headers: cfg.Push.Webhook.Headers,
		})
		notifyMgr.Register(notify.ChannelWebhook, webhookSender)
		channelsList = append(channelsList, "webhook")
		logger.Info("Webhook notifier configured: %s", cfg.Push.Webhook.URL)
	}

	channels := strings.Join(channelsList, ",")
	reportGen := processor.NewReportGenerator(db, analyzer, notifyMgr, channels)
	server.SetReportGenerator(reportGen)
	server.SetNotifyMgr(notifyMgr)
	logger.Info("Report generator initialized, push channels: [%s]", channels)

	// 初始化事件匹配器
	if analyzer != nil {
		eventMatcher := processor.NewEventMatcher(db, analyzer,
			cfg.EventMatcher.MatchThreshold,
			cfg.EventMatcher.VectorMinSimilarity,
			cfg.EventMatcher.KeywordWeight,
			cfg.EventMatcher.VectorWeight)
		server.SetEventMatcher(eventMatcher)
		logger.Info("Event matcher initialized (threshold: %.2f, min_similarity: %.2f, keyword_weight: %.2f, vector_weight: %.2f)",
			cfg.EventMatcher.MatchThreshold, cfg.EventMatcher.VectorMinSimilarity, cfg.EventMatcher.KeywordWeight, cfg.EventMatcher.VectorWeight)

		// 初始化热点检测器
		hotTopicDetector := processor.NewHotTopicDetector(db, analyzer)
		server.SetHotTopicDetector(hotTopicDetector)
		logger.Info("Hot topic detector initialized")

		// 初始化话题聚合器（Readhub 式话题流，参数取包内默认常量）
		topicAggregator := processor.NewTopicAggregator(db, analyzer)
		server.SetTopicAggregator(topicAggregator)
		logger.Info("Topic aggregator initialized")
	}

	// 启动定时任务调度器
	stopScheduler := startScheduler(db, cfg, reportGen, analyzer)
	defer stopScheduler()

	// 创建 HTTP 服务
	router := server.NewRouter()

	// 配置 HTTP 服务器
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 启动 HTTP 服务（非阻塞）
	go func() {
		logger.Info("HTTP server starting on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("HTTP server error: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// 优雅关闭
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Server shutdown error: %v", err)
	}

	logger.Info("Server stopped")
}

// initLogger 初始化日志系统
func initLogger(cfg *config.Config, cmdLogLevel string) {
	// 优先使用命令行参数，其次使用配置文件
	level := cmdLogLevel
	if level == "" {
		level = cfg.Server.LogLevel
	}
	if level != "" {
		logger.SetLevelFromString(level)
	}

	// 设置单行最大长度
	if cfg.Server.LogMaxLineLen > 0 {
		logger.SetMaxLineLen(cfg.Server.LogMaxLineLen)
	} else {
		logger.SetMaxLineLen(500) // 默认 500 字符
	}

	logger.Debug("Logger initialized: level=%s, max_line_len=%d", level, cfg.Server.LogMaxLineLen)
}

// startScheduler 启动定时任务调度器
func startScheduler(db *database.DB, cfg *config.Config, reportGen *processor.ReportGenerator, analyzer *ai.Analyzer) (stopFunc func()) {
	stopChan := make(chan struct{})

	go func() {
		// RSS 刷新间隔（默认30分钟）
		refreshInterval := 30 * time.Minute
		if cfg.Server.RefreshInterval > 0 {
			refreshInterval = time.Duration(cfg.Server.RefreshInterval) * time.Minute
		}

		// AI 处理间隔（默认10分钟）
		aiProcessInterval := 10 * time.Minute

		// 热点检测间隔（默认6小时）
		hotTopicInterval := 6 * time.Hour

		// 生命周期检查间隔（默认每天一次）
		lifecycleCheckInterval := 24 * time.Hour

		// 初始延迟（启动后30秒开始第一次任务）
		initialDelay := 30 * time.Second

		logger.Info("Scheduler started: RSS refresh every %v, AI process every %v", refreshInterval, aiProcessInterval)
		if cfg.Scheduler.MorningReportTime != "" {
			logger.Info("Morning report scheduled at: %s", cfg.Scheduler.MorningReportTime)
		}
		if cfg.Scheduler.EveningReportTime != "" {
			logger.Info("Evening report scheduled at: %s", cfg.Scheduler.EveningReportTime)
		}

		refreshTicker := time.NewTicker(refreshInterval)
		aiTicker := time.NewTicker(aiProcessInterval)
		reportCheckTicker := time.NewTicker(1 * time.Minute) // 每分钟检查一次早晚报
		hotTopicTicker := time.NewTicker(hotTopicInterval)   // 热点检测
		lifecycleTicker := time.NewTicker(lifecycleCheckInterval) // 生命周期检查

		// 记录上次生成报告的日期，防止重复生成
		var lastMorningReportDate, lastEveningReportDate, lastDailyReportDate string

		// 启动后延迟执行一次初始任务
		go func() {
			time.Sleep(initialDelay)

			// 初始刷新所有订阅
			logger.Debug("Initial feed refresh...")
			if err := refreshAllFeeds(db); err != nil {
				logger.Error("Initial feed refresh error: %v", err)
			}

			// 初始处理待分析文章
			logger.Debug("Initial AI processing...")
			processPendingArticles(db)
		}()

		for {
			select {
			case <-stopChan:
				refreshTicker.Stop()
				aiTicker.Stop()
				reportCheckTicker.Stop()
				hotTopicTicker.Stop()
				lifecycleTicker.Stop()
				logger.Info("Scheduler stopped")
				return

			case <-refreshTicker.C:
				logger.Debug("Scheduled feed refresh...")
				if err := refreshAllFeeds(db); err != nil {
					logger.Error("Scheduled feed refresh error: %v", err)
				}

			case <-aiTicker.C:
				logger.Debug("Scheduled AI processing...")
				processPendingArticles(db)

			case <-reportCheckTicker.C:
				// 检查是否需要生成早晚报
				if reportGen != nil {
					now := time.Now()
					currentTime := now.Format("15:04")
					today := now.Format("2006-01-02")

					// 检查早报时间
					if cfg.Scheduler.MorningAutoPush && cfg.Scheduler.MorningReportTime == currentTime {
						lastMorningKey := fmt.Sprintf("morning_%s", today)
						if lastMorningReportDate != lastMorningKey {
							lastMorningReportDate = lastMorningKey
							logger.Info("Generating morning report...")
							go func() {
								if _, err := reportGen.Generate("morning"); err != nil {
									logger.Error("Failed to generate morning report: %v", err)
								}
							}()
						}
					}

					// 检查晚报时间
					if cfg.Scheduler.EveningAutoPush && cfg.Scheduler.EveningReportTime == currentTime {
						lastEveningKey := fmt.Sprintf("evening_%s", today)
						if lastEveningReportDate != lastEveningKey {
							lastEveningReportDate = lastEveningKey
							logger.Info("Generating evening report...")
							go func() {
								if _, err := reportGen.Generate("evening"); err != nil {
									logger.Error("Failed to generate evening report: %v", err)
								}
							}()
						}
					}

					// 检查日报时间
					if cfg.Scheduler.DailyAutoPush && cfg.Scheduler.DailyReportTime == currentTime {
						lastDailyKey := fmt.Sprintf("daily_%s", today)
						if lastDailyReportDate != lastDailyKey {
							lastDailyReportDate = lastDailyKey
							logger.Info("Generating daily report...")
							go func() {
								if _, err := reportGen.Generate("daily"); err != nil {
									logger.Error("Failed to generate daily report: %v", err)
								}
							}()
						}
					}
				}

			case <-hotTopicTicker.C:
				// 热点检测
				if analyzer != nil {
					logger.Debug("Running hot topic detection...")
					go detectHotTopics(db, analyzer)
				}

			case <-lifecycleTicker.C:
				// 生命周期管理：自动暂停长时间无更新的事件
				logger.Debug("Running event lifecycle check...")
				go autoPauseInactiveEvents(db, 7) // 7天无更新自动暂停
			}
		}
	}()

	return func() {
		close(stopChan)
	}
}

// refreshAllFeeds 刷新所有订阅源
func refreshAllFeeds(db *database.DB) error {
	feeds, err := db.ListFeeds()
	if err != nil {
		return fmt.Errorf("failed to list feeds: %w", err)
	}

	for _, feed := range feeds {
		if !feed.IsActive {
			continue
		}

		// 调用服务器的刷新逻辑
		if err := server.RefreshFeedInternal(feed.ID); err != nil {
			logger.Error("Failed to refresh feed %s: %v", feed.Title, err)
		}
	}

	return nil
}

// processPendingArticles 处理待分析的文章（包括未处理和不完整的）
func processPendingArticles(db *database.DB) {
	// 1. 先处理未处理的文章
	articles, err := db.GetUnprocessedArticles(20)
	if err != nil {
		logger.Error("Failed to get unprocessed articles: %v", err)
	} else if len(articles) > 0 {
		logger.Info("Processing %d pending articles...", len(articles))
		server.ProcessPendingArticlesInternal(articles)
	}

	// 2. 然后处理不完整的文章（缺少 entities/one_line_summary/summary_embedding）
	incompleteArticles, err := db.GetArticlesIncomplete(20)
	if err != nil {
		logger.Error("Failed to get incomplete articles: %v", err)
		return
	}

	if len(incompleteArticles) == 0 {
		return
	}

	logger.Info("Processing %d incomplete articles (missing entities/one_line_summary/summary_embedding)...", len(incompleteArticles))
	server.ProcessIncompleteArticlesInternal(incompleteArticles)
}

// detectHotTopics 检测热点事件
func detectHotTopics(db *database.DB, analyzer *ai.Analyzer) {
	detector := processor.NewHotTopicDetector(db, analyzer)

	candidates, err := detector.DetectHotTopics(context.Background())
	if err != nil {
		logger.Error("Failed to detect hot topics: %v", err)
		return
	}

	if len(candidates) == 0 {
		logger.Debug("No hot topics detected")
		return
	}

	logger.Info("Detected %d hot topic candidates", len(candidates))

	// 自动创建待关注事件
	for _, candidate := range candidates {
		event, err := detector.CreateEventFromCandidate(candidate)
		if err != nil {
			logger.Error("Failed to create event from candidate: %v", err)
			continue
		}
		logger.Info("Created pending event: %s (ID: %d)", event.Name, event.ID)
	}
}

// autoPauseInactiveEvents 自动暂停长时间无更新的事件
func autoPauseInactiveEvents(db *database.DB, inactiveDays int) {
	events, err := db.GetInactiveEventTracks(inactiveDays)
	if err != nil {
		logger.Error("Failed to get inactive events: %v", err)
		return
	}

	if len(events) == 0 {
		logger.Debug("No inactive events to pause")
		return
	}

	for _, event := range events {
		if err := db.UpdateEventTrackStatus(event.ID, "paused"); err != nil {
			logger.Error("Failed to pause event %d: %v", event.ID, err)
			continue
		}
		logger.Info("Auto-paused inactive event: %s (ID: %d, no updates for %d days)", event.Name, event.ID, inactiveDays)
	}
}
