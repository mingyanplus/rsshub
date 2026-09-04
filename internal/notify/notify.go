package notify

import (
	"strings"
)

// Channel 通知渠道类型
type Channel string

const (
	ChannelEmail   Channel = "email"
	ChannelGotify  Channel = "gotify"
	ChannelWebhook Channel = "webhook"
	ChannelQQBot  Channel = "qqbot"
)

// Message 通知消息
type Message struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Result 发送结果
type Result struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// EmailConfig 邮件配置
type EmailConfig struct {
	SMTPHost  string `json:"smtp_host"`
	SMTPPort  int    `json:"smtp_port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// IsValid 验证配置
func (c *EmailConfig) IsValid() bool {
	return c.SMTPHost != "" && c.SMTPPort > 0 && c.Username != "" && c.Password != "" && c.From != "" && c.To != ""
}

// GotifyConfig Gotify 配置
type GotifyConfig struct {
	URL      string `json:"url"`
	AppToken string `json:"app_token"`
	Priority int    `json:"priority"`
}

// IsValid 验证配置
func (c *GotifyConfig) IsValid() bool {
	return c.URL != "" && c.AppToken != ""
}

// WebhookConfig Webhook 配置
type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// IsValid 验证配置
func (c *WebhookConfig) IsValid() bool {
	return c.URL != ""
}

// QQBotConfig QQ Bot 配置
type QQBotConfig struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	UserID    string `json:"user_id"`
}

// IsValid 验证配置
func (c *QQBotConfig) IsValid() bool {
	return c.AppID != "" && c.AppSecret != "" && c.UserID != ""
}

// Notifier 通知器接口
type Notifier interface {
	Send(msg *Message) *Result
	Channel() Channel
}

// FormatMessage 格式化消息为字符串
func FormatMessage(msg *Message) string {
	if msg == nil {
		return ""
	}
	return msg.Title + "\n\n" + msg.Content
}

// ParseChannels 解析逗号分隔的渠道字符串为渠道列表
func ParseChannels(channelsStr string) []Channel {
	if channelsStr == "" {
		return nil
	}

	parts := strings.Split(channelsStr, ",")
	channels := make([]Channel, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			channels = append(channels, Channel(trimmed))
		}
	}
	return channels
}

