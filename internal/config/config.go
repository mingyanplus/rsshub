package config

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

//go:embed embedded/config.example.yaml
var embeddedFS embed.FS

// ConfigWatcher 配置监听器
type ConfigWatcher struct {
	viper    *viper.Viper
	path     string
	config   *Config
	mu       sync.RWMutex
	onChange func(*Config)
	stopCh   chan struct{}
}

// currentConfig 当前配置（全局）
var currentConfig *Config
var configWatcher *ConfigWatcher

// EventMatcherConfig 事件匹配器配置
type EventMatcherConfig struct {
	MatchThreshold      float64 `mapstructure:"match_threshold"`       // 综合匹配阈值 (0-1)
	VectorMinSimilarity float64 `mapstructure:"vector_min_similarity"` // 向量相似度最低阈值 (0-1)
	KeywordWeight       float64 `mapstructure:"keyword_weight"`        // 关键词匹配权重 (0-1)
	VectorWeight        float64 `mapstructure:"vector_weight"`         // 向量匹配权重 (0-1)
}

// Config 应用配置结构
type Config struct {
	Server       ServerConfig       `mapstructure:"server"`
	Database     DatabaseConfig     `mapstructure:"database"`
	AI           AIConfig           `mapstructure:"ai"`
	Feeds        FeedsConfig        `mapstructure:"feeds"`
	Scheduler    SchedulerConfig    `mapstructure:"scheduler"`
	Cleanup      CleanupConfig      `mapstructure:"cleanup"`
	Push         PushConfig         `mapstructure:"push"`
	EventMatcher EventMatcherConfig `mapstructure:"event_matcher"`
	Proxy        ProxyConfig        `mapstructure:"proxy"`
	Prompts      PromptsConfig      `mapstructure:"prompts"`
}

// PromptsConfig 提示词覆盖（留空使用程序内置默认，设置页可编辑）
type PromptsConfig struct {
	AnalyzeSystem   string `mapstructure:"analyze_system"`   // 文章分析 system 提示词
	TranslateSystem string `mapstructure:"translate_system"` // 翻译 system 提示词
}

// ProxyConfig 代理配置：获取内容（RSS 抓取/原文获取）与 LLM 接口可分别启用
type ProxyConfig struct {
	URL           string `mapstructure:"url"`            // 支持 http:// 与 socks5://
	EnableContent bool   `mapstructure:"enable_content"` // 内容抓取走代理
	EnableLLM     bool   `mapstructure:"enable_llm"`     // LLM/Embedding 接口走代理
}

// ServerConfig HTTP 服务器配置
type ServerConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	RefreshInterval int    `mapstructure:"refresh_interval"` // RSS 刷新间隔（分钟），默认30
	Timezone        string `mapstructure:"timezone"`         // 显示时区，如 "Asia/Shanghai" 或 "Local"
	Password        string `mapstructure:"password"`         // Web 登录密码，留空则不启用登录
	LogLevel        string `mapstructure:"log_level"`        // 日志级别: DEBUG, INFO, WARN, ERROR
	LogMaxLineLen   int    `mapstructure:"log_max_line_len"` // 单行最大长度（0不限制）
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Path string `mapstructure:"path"`
}

// AIConfig AI 服务配置
type AIConfig struct {
	LLM           LLMConfig      `mapstructure:"llm"`
	Embedding     EmbeddingConfig `mapstructure:"embedding"`
	RateLimit     RateLimitConfig `mapstructure:"rate_limit"`
}

// LLMConfig LLM 配置
type LLMConfig struct {
	Provider      string             `mapstructure:"provider"`
	BaseURL       string             `mapstructure:"base_url"`
	APIKey        string             `mapstructure:"api_key"`
	Model         string             `mapstructure:"model"`
	Fallback      LLMFallbackConfig  `mapstructure:"fallback"` // 备用 LLM：主模型失败时自动切换，留空项沿用主配置
	Timeout       time.Duration      `mapstructure:"timeout"`
	MaxRetries    int                `mapstructure:"max_retries"`
	RetryInterval time.Duration      `mapstructure:"retry_interval"` // 重试基础间隔（指数退避：×1、×2、×4...封顶60s）
	UserAgent     string             `mapstructure:"user_agent"` // 自定义 User-Agent
}

// LLMFallbackConfig 备用 LLM 配置（独立端点，留空项沿用主配置）
type LLMFallbackConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// EmbeddingConfig Embedding 配置
type EmbeddingConfig struct {
	Provider   string        `mapstructure:"provider"`
	BaseURL    string        `mapstructure:"base_url"`
	APIKey     string        `mapstructure:"api_key"`
	Model      string        `mapstructure:"model"`
	Timeout    time.Duration `mapstructure:"timeout"`
	MaxRetries int           `mapstructure:"max_retries"`
	UserAgent  string        `mapstructure:"user_agent"` // 自定义 User-Agent
}

// RateLimitConfig 速率限制配置
type RateLimitConfig struct {
	LLMInterval         time.Duration `mapstructure:"llm_interval"`          // LLM API 调用间隔
	EmbeddingInterval   time.Duration `mapstructure:"embedding_interval"`    // Embedding API 调用间隔
	ArticleInterval     time.Duration `mapstructure:"article_interval"`      // 文章分析间隔
}

// FeedsConfig RSS 订阅配置
type FeedsConfig struct {
	DefaultInterval      time.Duration `mapstructure:"default_interval"`
	MaxConcurrent        int           `mapstructure:"max_concurrent"`
	Timeout              time.Duration `mapstructure:"timeout"`
	FetchUserAgent       string        `mapstructure:"fetch_user_agent"`              // 获取原文时的 User-Agent
	ProtectFetchOriginal bool          `mapstructure:"protect_fetch_original"`       // 获取原文保护：抓取结果比现有内容短很多（疑似失败页）时不覆盖
}

// SchedulerConfig 定时任务配置
type SchedulerConfig struct {
	MorningReportTime  string `mapstructure:"morning_report_time"`
	EveningReportTime  string `mapstructure:"evening_report_time"`
	DailyReportTime    string `mapstructure:"daily_report_time"`    // 日报推送时间
	MorningAutoPush    bool   `mapstructure:"morning_auto_push"`    // 早报是否自动推送
	EveningAutoPush    bool   `mapstructure:"evening_auto_push"`    // 晚报是否自动推送
	DailyAutoPush      bool   `mapstructure:"daily_auto_push"`      // 日报是否自动推送
}

// CleanupConfig 清理配置
type CleanupConfig struct {
	AdRetentionDays           int    `mapstructure:"ad_retention_days"`
	NotificationRetentionDays int    `mapstructure:"notification_retention_days"`
	AutoCleanupTime           string `mapstructure:"auto_cleanup_time"`
}

// PushConfig 推送配置
type PushConfig struct {
	Email   EmailPushConfig   `mapstructure:"email"`
	Gotify  GotifyPushConfig  `mapstructure:"gotify"`
	QQBot   QQBotPushConfig   `mapstructure:"qqbot"`
	Webhook WebhookPushConfig `mapstructure:"webhook"`
}

// EmailPushConfig 邮件推送配置
type EmailPushConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	SMTPHost string `mapstructure:"smtp_host"`
	SMTPPort int    `mapstructure:"smtp_port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	From     string `mapstructure:"from"`
	To       string `mapstructure:"to"`
}

// GotifyPushConfig Gotify 推送配置
type GotifyPushConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	URL      string `mapstructure:"url"`
	AppToken string `mapstructure:"app_token"`
	Priority int    `mapstructure:"priority"`
}

// QQBotPushConfig QQ Bot 推送配置
type QQBotPushConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	AppID          string `mapstructure:"app_id"`
	AppSecret      string `mapstructure:"app_secret"`
	UserID         string `mapstructure:"user_id"`
	EnableMarkdown bool   `mapstructure:"enable_markdown"` // 是否启用 Markdown 格式（包含链接、标题等）
}

// WebhookPushConfig Webhook 推送配置
type WebhookPushConfig struct {
	Enabled bool              `mapstructure:"enabled"`
	URL     string            `mapstructure:"url"`
	Headers map[string]string `mapstructure:"headers"`
}

// 默认配置值
var defaults = &Config{
	Server: ServerConfig{
		Host:     "127.0.0.1",
		Port:     8080,
		Timezone: "Asia/Shanghai",
	},
	Database: DatabaseConfig{
		Path: "./data/rss.db",
	},
	AI: AIConfig{
		LLM: LLMConfig{
			Provider:      "openai",
			Timeout:       60 * time.Second,
			RetryInterval: 3 * time.Second,
		},
		Embedding: EmbeddingConfig{
			Provider: "openai",
			Timeout:  30 * time.Second,
		},
		RateLimit: RateLimitConfig{
			LLMInterval:       3 * time.Second,
			EmbeddingInterval: 1 * time.Second,
			ArticleInterval:   2 * time.Second,
		},
	},
	Feeds: FeedsConfig{
		DefaultInterval: 30 * time.Minute,
		MaxConcurrent:   5,
		Timeout:         30 * time.Second,
	},
	Scheduler: SchedulerConfig{
		MorningReportTime:  "08:00",
		EveningReportTime:  "20:00",
		DailyReportTime:    "22:00",
		MorningAutoPush:    true,
		EveningAutoPush:    true,
		DailyAutoPush:      true,
	},
	Cleanup: CleanupConfig{
		AdRetentionDays:           7,
		NotificationRetentionDays: 14,
        AutoCleanupTime:           "03:00",
	 },
    EventMatcher: EventMatcherConfig{
        MatchThreshold:      0.65,
        VectorMinSimilarity: 0.45,
    },
}

// ensureConfigExists 配置文件不存在时自动创建默认配置（首次初始化）
func ensureConfigExists(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	data, err := embeddedFS.ReadFile("embedded/config.example.yaml")
	if err != nil {
		log.Printf("读取内置示例配置失败: %v", err)
		return
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("创建配置目录失败: %v", err)
			return
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("自动创建默认配置失败: %v", err)
		return
	}
	log.Printf("未发现配置文件，已自动创建默认配置: %s（请编辑后重启生效）", path)
}

// Load 从文件加载配置
func Load(path string) (*Config, error) {
	v := viper.New()

	// 设置默认值
	setDefaults(v, defaults)

	// 首次启动时自动创建默认配置文件
	ensureConfigExists(path)

	// 配置文件设置
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	// 解析到结构体
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// 保存为当前配置
	currentConfig = &cfg

	return &cfg, nil
}

// LoadWithWatch 加载配置并启动热加载监听
func LoadWithWatch(path string, onChange func(*Config)) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}

	configWatcher = &ConfigWatcher{
		viper:    viper.New(),
		path:     path,
		config:   cfg,
		onChange: onChange,
		stopCh:   make(chan struct{}),
	}

	// 设置默认值
	setDefaults(configWatcher.viper, defaults)
	configWatcher.viper.SetConfigFile(path)
	configWatcher.viper.SetConfigType("yaml")

	// 读取配置文件
	if err := configWatcher.viper.ReadInConfig(); err != nil {
		return nil, err
	}

	// 启动文件监听
	go configWatcher.watch()

	return cfg, nil
}

// watch 监听配置文件变化
func (w *ConfigWatcher) watch() {
	w.viper.WatchConfig()
	w.viper.OnConfigChange(func(e fsnotify.Event) {
		log.Printf("检测到配置文件变化: %s", e.Name)

		// 重新解析配置
		var newCfg Config
		if err := w.viper.Unmarshal(&newCfg); err != nil {
			log.Printf("解析新配置失败: %v", err)
			return
		}

		w.mu.Lock()
		w.config = &newCfg
		currentConfig = &newCfg
		w.mu.Unlock()

		log.Printf("配置已重新加载")

		// 触发回调
		if w.onChange != nil {
			w.onChange(&newCfg)
		}
	})
}

// StopWatch 停止配置监听
func StopWatch() {
	if configWatcher != nil {
		close(configWatcher.stopCh)
	}
}

// Reload 手动重新加载配置
func Reload() (*Config, error) {
	if configWatcher == nil {
		return nil, nil
	}

	// 重新读取配置文件
	if err := configWatcher.viper.ReadInConfig(); err != nil {
		return nil, err
	}

	// 解析新配置
	var newCfg Config
	if err := configWatcher.viper.Unmarshal(&newCfg); err != nil {
		return nil, err
	}

	configWatcher.mu.Lock()
	configWatcher.config = &newCfg
	currentConfig = &newCfg
	configWatcher.mu.Unlock()

	log.Printf("配置已手动重新加载")
	return &newCfg, nil
}

// GetCurrentConfig 获取当前配置
func GetCurrentConfig() *Config {
	if configWatcher != nil {
		configWatcher.mu.RLock()
		defer configWatcher.mu.RUnlock()
		return configWatcher.config
	}
	return currentConfig
}

// SaveReportConfig 保存报告配置到文件
func SaveReportConfig(reportType string, enabled bool) error {
	if configWatcher == nil {
		return nil
	}

	configWatcher.mu.Lock()
	defer configWatcher.mu.Unlock()

	// 更新内存中的配置
	switch reportType {
	case "morning":
		configWatcher.config.Scheduler.MorningAutoPush = enabled
	case "evening":
		configWatcher.config.Scheduler.EveningAutoPush = enabled
	case "daily":
		configWatcher.config.Scheduler.DailyAutoPush = enabled
	}
	currentConfig = configWatcher.config

	// 直接写入配置文件
	var key string
	switch reportType {
	case "morning":
		key = "scheduler.morning_auto_push"
	case "evening":
		key = "scheduler.evening_auto_push"
	case "daily":
		key = "scheduler.daily_auto_push"
	}

	configWatcher.viper.Set(key, enabled)

	// 使用 WriteConfigAs 强制写入
	if err := configWatcher.viper.WriteConfigAs(configWatcher.path); err != nil {
		log.Printf("保存配置失败: %v", err)
		return err
	}

	log.Printf("报告配置已保存: %s = %v", reportType, enabled)
	return nil
}

// setDefaults 设置默认值
func setDefaults(v *viper.Viper, cfg *Config) {
	v.SetDefault("server.host", cfg.Server.Host)
	v.SetDefault("server.port", cfg.Server.Port)
	v.SetDefault("server.timezone", cfg.Server.Timezone)
	v.SetDefault("database.path", cfg.Database.Path)
	v.SetDefault("ai.llm.provider", cfg.AI.LLM.Provider)
	v.SetDefault("ai.llm.timeout", cfg.AI.LLM.Timeout)
	v.SetDefault("ai.embedding.provider", cfg.AI.Embedding.Provider)
	v.SetDefault("ai.embedding.timeout", cfg.AI.Embedding.Timeout)
	v.SetDefault("ai.rate_limit.llm_interval", cfg.AI.RateLimit.LLMInterval)
	v.SetDefault("ai.rate_limit.embedding_interval", cfg.AI.RateLimit.EmbeddingInterval)
	v.SetDefault("ai.rate_limit.article_interval", cfg.AI.RateLimit.ArticleInterval)
	v.SetDefault("feeds.default_interval", cfg.Feeds.DefaultInterval)
	v.SetDefault("feeds.max_concurrent", cfg.Feeds.MaxConcurrent)
	v.SetDefault("feeds.timeout", cfg.Feeds.Timeout)
	v.SetDefault("feeds.protect_fetch_original", true) // 获取原文保护默认开启
	v.SetDefault("scheduler.morning_report_time", cfg.Scheduler.MorningReportTime)
	v.SetDefault("scheduler.evening_report_time", cfg.Scheduler.EveningReportTime)
	v.SetDefault("scheduler.daily_report_time", cfg.Scheduler.DailyReportTime)
	v.SetDefault("scheduler.morning_auto_push", cfg.Scheduler.MorningAutoPush)
	v.SetDefault("scheduler.evening_auto_push", cfg.Scheduler.EveningAutoPush)
	v.SetDefault("scheduler.daily_auto_push", cfg.Scheduler.DailyAutoPush)
	v.SetDefault("cleanup.ad_retention_days", cfg.Cleanup.AdRetentionDays)
	v.SetDefault("cleanup.notification_retention_days", cfg.Cleanup.NotificationRetentionDays)
	v.SetDefault("cleanup.auto_cleanup_time", cfg.Cleanup.AutoCleanupTime)
	v.SetDefault("event_matcher.match_threshold", cfg.EventMatcher.MatchThreshold)
	v.SetDefault("event_matcher.vector_min_similarity", cfg.EventMatcher.VectorMinSimilarity)
}
