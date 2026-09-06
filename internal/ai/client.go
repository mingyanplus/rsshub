package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"rss-ai/internal/config"
	"rss-ai/internal/proxyutil"
)

// 默认冷却时间：遇到 503 后暂停 5 分钟
const defaultCooldownDuration = 5 * time.Minute

// maxRetryBackoff 重试退避等待上限
const maxRetryBackoff = 60 * time.Second

// LLMClient LLM 客户端
type LLMClient struct {
	maxRetries      int
	baseURL         string
	apiKey          string
	model           string
	fallback        *LLMClient // 备用 LLM（独立端点、独立冷却），主模型失败时自动切换
	timeout         time.Duration
	retryBaseDelay  time.Duration // 重试基础间隔（指数退避）
	userAgent       string
	httpClient      *http.Client

	mu            sync.Mutex
	cooldownUntil time.Time // API 不可用时的冷却截止时间
}

// newFallbackClient 由备用配置构造备用客户端（留空项沿用主配置；不再嵌套二级备用）
func newFallbackClient(main *config.LLMConfig, timeout time.Duration, httpClient *http.Client) *LLMClient {
	fb := main.Fallback
	baseURL, apiKey := fb.BaseURL, fb.APIKey
	if baseURL == "" {
		baseURL = main.BaseURL
	}
	if apiKey == "" {
		apiKey = main.APIKey
	}
	ua := main.UserAgent
	return &LLMClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      fb.Model,
		timeout:    timeout,
		maxRetries: main.MaxRetries,
		userAgent:  ua,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// NewLLMClient 创建 LLM 客户端
func NewLLMClient(cfg *config.LLMConfig) *LLMClient {
	c := &LLMClient{
		baseURL:        cfg.BaseURL,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		timeout:        cfg.Timeout,
		maxRetries:     cfg.MaxRetries,
		retryBaseDelay: cfg.RetryInterval,
		userAgent:      cfg.UserAgent,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
	if cfg.Fallback.Model != "" {
		c.fallback = newFallbackClient(cfg, cfg.Timeout, nil)
	}
	return c
}

// UpdateConfig 更新客户端配置（支持热重载）
func (c *LLMClient) UpdateConfig(cfg *config.LLMConfig) {
	c.baseURL = cfg.BaseURL
	c.apiKey = cfg.APIKey
	c.model = cfg.Model
	c.timeout = cfg.Timeout
	c.maxRetries = cfg.MaxRetries
	c.retryBaseDelay = cfg.RetryInterval
	c.userAgent = cfg.UserAgent
	c.httpClient.Timeout = cfg.Timeout
	if cfg.Fallback.Model != "" {
		c.fallback = newFallbackClient(cfg, cfg.Timeout, nil)
	} else {
		c.fallback = nil
	}
}

// retryDelay 计算第 attempt 次失败后的重试等待（指数退避：base×1、base×2、base×4...，封顶 60s）
func (c *LLMClient) retryDelay(attempt int) time.Duration {
	base := c.retryBaseDelay
	if base <= 0 {
		base = 3 * time.Second
	}
	delay := base << (attempt - 1) // 位移翻倍
	if delay > maxRetryBackoff || delay <= 0 {
		delay = maxRetryBackoff
	}
	return delay
}

// SetProxy 设置 LLM 接口代理；proxyURL 为空时清除（同步备用客户端）
func (c *LLMClient) SetProxy(proxyURL string) {
	proxyutil.Apply(c.httpClient, proxyURL)
	if c.fallback != nil {
		proxyutil.Apply(c.fallback.httpClient, proxyURL)
	}
}

// Model 返回当前使用的模型名称
func (c *LLMClient) Model() string {
	return c.model
}

// ChatRequest 聊天请求
type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
	EnableThinking bool            `json:"enable_thinking,omitempty"`  // 启用深度思考（部分模型支持）
	ThinkingBudget int             `json:"thinking_budget,omitempty"`  // 思考token预算
}

// ResponseFormat 响应格式
type ResponseFormat struct {
	Type       string                 `json:"type,omitempty"`        // "json_object" 或 "text"
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"` // 可选的 JSON Schema
}

// ChatMessage 聊天消息
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse 聊天响应
type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// Chat 发送聊天请求（普通文本模式）
func (c *LLMClient) Chat(ctx context.Context, prompt string) (string, error) {
	return c.chatCore(ctx, "", prompt, nil, 0)
}

// ChatJSON 发送聊天请求（JSON 输出模式）
func (c *LLMClient) ChatJSON(ctx context.Context, prompt string) (string, error) {
	format := &ResponseFormat{Type: "json_object"}
	return c.chatCore(ctx, "", prompt, format, 0)
}

// ChatWithSystem 发送聊天请求（system 指令与 user 内容分离，system 前缀稳定利于 LLM 服务商的 context caching）
func (c *LLMClient) ChatWithSystem(ctx context.Context, system, prompt string) (string, error) {
	return c.chatCore(ctx, system, prompt, nil, 0)
}

// ChatJSONWithSystem 发送聊天请求（JSON 输出模式 + system 指令分离）
func (c *LLMClient) ChatJSONWithSystem(ctx context.Context, system, prompt string) (string, error) {
	format := &ResponseFormat{Type: "json_object"}
	return c.chatCore(ctx, system, prompt, format, 0)
}

// ChatWithDeepThinking 发送聊天请求（深度思考模式）
func (c *LLMClient) ChatWithDeepThinking(ctx context.Context, prompt string, thinkingBudget int) (string, error) {
	return c.chatCore(ctx, "", prompt, nil, thinkingBudget)
}

// ChatJSONWithDeepThinking 发送聊天请求（JSON + 深度思考模式）
func (c *LLMClient) ChatJSONWithDeepThinking(ctx context.Context, prompt string, thinkingBudget int) (string, error) {
	format := &ResponseFormat{Type: "json_object"}
	return c.chatCore(ctx, "", prompt, format, thinkingBudget)
}

// chatCore 发送聊天请求（统一入口：支持 system、响应格式、重试与备用 LLM 自动降级）
func (c *LLMClient) chatCore(ctx context.Context, system, prompt string, format *ResponseFormat, thinkingBudget int) (string, error) {
	result, err := c.chatWithModel(ctx, system, prompt, format, thinkingBudget, c.model)
	if err == nil || c.fallback == nil {
		return result, err
	}
	// 主模型失败（重试耗尽/冷却/内容过滤等），自动切换备用 LLM（独立端点、独立重试与冷却）再试
	log.Printf("LLM 主模型 %s 请求失败: %v，切换备用模型 %s 重试", c.model, err, c.fallback.model)
	result2, err2 := c.fallback.chatWithModel(ctx, system, prompt, format, thinkingBudget, c.fallback.model)
	if err2 == nil {
		return result2, nil
	}
	return "", fmt.Errorf("LLM 主模型 %s 与备用模型 %s 均失败: 主模型错误: %v; 备用模型错误: %v",
		c.model, c.fallback.model, err, err2)
}

// chatWithModel 以指定模型发送请求（支持响应格式、重试和深度思考）
func (c *LLMClient) chatWithModel(ctx context.Context, system, prompt string, format *ResponseFormat, thinkingBudget int, model string) (string, error) {
	// 检查是否在冷却期
	c.mu.Lock()
	if time.Now().Before(c.cooldownUntil) {
		remaining := time.Until(c.cooldownUntil).Truncate(time.Second)
		c.mu.Unlock()
		return "", fmt.Errorf("LLM API 冷却中，剩余 %v，跳过请求", remaining)
	}
	c.mu.Unlock()

	maxRetries := c.maxRetries
	if maxRetries <= 0 {
		maxRetries = 3 // 默认重试3次
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		result, err := c.doChatRequestWithThinking(ctx, system, prompt, format, thinkingBudget, model)
		if err == nil {
			return result, nil
		}
		lastErr = err
		log.Printf("LLM 请求失败 (model=%s, 尝试 %d/%d): %v", model, attempt, maxRetries, err)

		// 503 表示 API key 池耗尽，不再重试，直接进入冷却
		if isUnavailableError(err) {
			c.mu.Lock()
			c.cooldownUntil = time.Now().Add(defaultCooldownDuration)
			c.mu.Unlock()
			log.Printf("LLM API 不可用 (503)，进入冷却，%v 后恢复", defaultCooldownDuration)
			return "", fmt.Errorf("LLM API 不可用，已进入冷却 (%v): %w", defaultCooldownDuration, lastErr)
		}

		// 内容安全过滤：特定文章问题，重试无意义
		if IsContentBlockedError(err) {
			log.Printf("LLM 内容安全过滤，跳过重试")
			return "", fmt.Errorf("LLM 内容安全过滤: %w", lastErr)
		}

		// 如果不是最后一次尝试，指数退避后重试（如 3s、6s、12s，封顶 60s）
		if attempt < maxRetries {
			time.Sleep(c.retryDelay(attempt))
		}
	}
	return "", fmt.Errorf("LLM 请求失败，已重试 %d 次: %w", maxRetries, lastErr)
}

// doChatRequest 执行单次聊天请求
func (c *LLMClient) doChatRequest(ctx context.Context, system, prompt string, format *ResponseFormat) (string, error) {
	return c.doChatRequestWithThinking(ctx, system, prompt, format, 0, c.model)
}

// doChatRequestWithThinking 执行单次聊天请求（非流式模式）
func (c *LLMClient) doChatRequestWithThinking(ctx context.Context, system, prompt string, format *ResponseFormat, thinkingBudget int, model string) (string, error) {
	messages := make([]ChatMessage, 0, 2)
	if system != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: system})
	}
	messages = append(messages, ChatMessage{Role: "user", Content: prompt})
	reqBody := ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}
	if format != nil {
		reqBody.ResponseFormat = format
	}
	if thinkingBudget > 0 {
		reqBody.EnableThinking = true
		reqBody.ThinkingBudget = thinkingBudget
		log.Printf("LLM 深度思考模式已启用 (budget: %d tokens)", thinkingBudget)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("API returned status %d: %s", resp.StatusCode, string(respBody))
		// 部分 API 中转不支持 response_format（json_object），自动降级为纯文本模式重试
		// （prompt 已要求 JSON 输出，调用方配合 ExtractJSONFromResponse 解析）
		if format != nil && strings.Contains(string(respBody), "response_format") {
			log.Printf("LLM API 拒绝 response_format，降级为纯文本模式重试")
			return c.doChatRequestWithThinking(ctx, system, prompt, nil, thinkingBudget, model)
		}
		return "", fmt.Errorf("%s", errMsg)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("empty choices in response")
	}

	result := chatResp.Choices[0].Message.Content
	if strings.TrimSpace(result) == "" {
		return "", fmt.Errorf("empty response from API")
	}

	log.Printf("DEBUG: LLM 响应长度: %d chars, tokens: prompt=%d completion=%d",
		len(result), chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens)
	return result, nil
}

// isUnavailableError 判断是否为 API 不可用错误（如 503、key 耗尽），需要全局冷却
func isUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "status 503") ||
		strings.Contains(msg, "NO_KEYS_AVAILABLE") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "status 429")
}

// IsContentBlockedError 判断是否为内容安全过滤错误（特定文章问题，不重试，不需全局冷却）
func IsContentBlockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "contentFilter") ||
		strings.Contains(msg, "sensitive content") ||
		strings.Contains(msg, "content_policy") ||
		strings.Contains(msg, "unsafe") ||
		strings.Contains(msg, "违规") ||
		strings.Contains(msg, "敏感") ||
		strings.Contains(msg, "内容审核")
}
