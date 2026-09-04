package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"regexp"
	"strings"
	"time"
)

// EmailSender 邮件发送器
type EmailSender struct {
	config *EmailConfig
}

func NewEmailSender(config *EmailConfig) *EmailSender {
	return &EmailSender{config: config}
}

func (s *EmailSender) Channel() Channel {
	return ChannelEmail
}

func (s *EmailSender) Send(msg *Message) *Result {
	if !s.config.IsValid() {
		return &Result{Success: false, Error: "email config is invalid"}
	}

	subject := msg.Title
	body := msg.Content
	from := s.config.From
	to := s.config.To

	email := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to, subject, body)

	auth := smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.SMTPHost)
	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)
	err := smtp.SendMail(addr, auth, from, []string{to}, []byte(email))
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	return &Result{Success: true}
}

// GotifySender Gotify 推送器
type GotifySender struct {
	config *GotifyConfig
	client *http.Client
}

func NewGotifySender(config *GotifyConfig) *GotifySender {
	return &GotifySender{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *GotifySender) Channel() Channel {
	return ChannelGotify
}

func (s *GotifySender) Send(msg *Message) *Result {
	if !s.config.IsValid() {
		return &Result{Success: false, Error: "gotify config is invalid"}
	}

	payload := map[string]interface{}{
		"title":    msg.Title,
		"message":  msg.Content,
		"priority": s.config.Priority,
		"extras": map[string]interface{}{
			"client::display": map[string]interface{}{
				"contentType": "text/markdown",
			},
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	url := fmt.Sprintf("%s/message?token=%s", s.config.URL, s.config.AppToken)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &Result{Success: false, Error: fmt.Sprintf("gotify returned status %d", resp.StatusCode)}
	}

	return &Result{Success: true}
}

// QQBotSender QQ Bot 推送器
type QQBotSender struct {
	config *QQBotConfig
	client *http.Client
}

func NewQQBotSender(config *QQBotConfig) *QQBotSender {
	return &QQBotSender{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *QQBotSender) Channel() Channel {
	return ChannelQQBot
}

// getAccessToken 获取 QQ Bot access_token（客户端凭证模式）
func (s *QQBotSender) getAccessToken() (string, error) {
	payload := map[string]string{
		"appId":        s.config.AppID,
		"clientSecret": s.config.AppSecret,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", "https://bots.qq.com/app/getAppAccessToken", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"` // 改为 string 类型
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access_token received")
	}

	return result.AccessToken, nil
}

// stripMarkdownLinks 移除 markdown 链接，只保留文本部分（QQ Bot 不允许发送链接）
func stripMarkdownLinks(content string) string {
	// 移除 markdown 链接 [文本](URL) -> 文本
	linkRegex := regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	content = linkRegex.ReplaceAllString(content, "$1")

	// 移除纯 URL (http:// 或 https://)
	urlRegex := regexp.MustCompile(`https?://[^\s\)\]\>]+`)
	content = urlRegex.ReplaceAllString(content, "[链接]")

	return content
}

func (s *QQBotSender) Send(msg *Message) *Result {
	if !s.config.IsValid() {
		return &Result{Success: false, Error: "qqbot config is invalid"}
	}

	token, err := s.getAccessToken()
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("failed to get access_token: %s", err.Error())}
	}

	// 保留 Markdown 格式，只移除链接（QQ Bot 不允许发送链接）
	content := stripMarkdownLinks(msg.Title + "\n\n" + msg.Content)

	payload := map[string]interface{}{
		"msg_type": 2, // Markdown 消息
		"markdown": map[string]string{
			"content": content,
		},
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	url := fmt.Sprintf("https://api.sgroup.qq.com/v2/users/%s/messages", s.config.UserID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "QQBot "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errMsg bytes.Buffer
		errMsg.ReadFrom(resp.Body)
		return &Result{Success: false, Error: fmt.Sprintf("qqbot returned status %d: %s", resp.StatusCode, errMsg.String())}
	}

	return &Result{Success: true}
}

// WebhookSender Webhook 发送器
type WebhookSender struct {
	config *WebhookConfig
	client *http.Client
}

func NewWebhookSender(config *WebhookConfig) *WebhookSender {
	return &WebhookSender{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *WebhookSender) Channel() Channel {
	return ChannelWebhook
}

func (s *WebhookSender) Send(msg *Message) *Result {
	if !s.config.IsValid() {
		return &Result{Success: false, Error: "webhook config is invalid"}
	}

	payload := map[string]interface{}{
		"title":   msg.Title,
		"content": msg.Content,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}

	req, err := http.NewRequest("POST", s.config.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	for key, value := range s.config.Headers {
		req.Header.Set(key, value)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &Result{Success: false, Error: fmt.Sprintf("webhook returned status %d", resp.StatusCode)}
	}

	return &Result{Success: true}
}

// Manager 通知管理器
type Manager struct {
	notifiers map[Channel]Notifier
}

func NewManager() *Manager {
	return &Manager{
		notifiers: make(map[Channel]Notifier),
	}
}

func (m *Manager) Register(channel Channel, notifier Notifier) {
	m.notifiers[channel] = notifier
}

func (m *Manager) Send(channel Channel, msg *Message) *Result {
	notifier, ok := m.notifiers[channel]
	if !ok {
		return &Result{Success: false, Error: fmt.Sprintf("channel %s not registered", channel)}
	}
	return notifier.Send(msg)
}

func (m *Manager) SendAll(channels []Channel, msg *Message) map[Channel]*Result {
	results := make(map[Channel]*Result)
	for _, channel := range channels {
		results[channel] = m.Send(channel, msg)
	}
	return results
}

func ParseChannelsFromStr(channelsStr string) []Channel {
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
