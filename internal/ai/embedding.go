package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"rss-ai/internal/config"
	"strings"
	"rss-ai/internal/proxyutil"
)

// EmbeddingClient Embedding 客户端
type EmbeddingClient struct {
	baseURL    string
	apiKey     string
	model      string
	timeout    time.Duration
	userAgent  string
	httpClient *http.Client
}

// NewEmbeddingClient 创建 Embedding 客户端
func NewEmbeddingClient(cfg *config.EmbeddingConfig) *EmbeddingClient {
	return &EmbeddingClient{
		baseURL:   cfg.BaseURL,
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		timeout:   cfg.Timeout,
		userAgent: cfg.UserAgent,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// UpdateConfig 更新客户端配置（支持热重载）
func (c *EmbeddingClient) UpdateConfig(cfg *config.EmbeddingConfig) {
	c.baseURL = cfg.BaseURL
	c.apiKey = cfg.APIKey
	c.model = cfg.Model
	c.timeout = cfg.Timeout
	c.userAgent = cfg.UserAgent
	c.httpClient.Timeout = cfg.Timeout
}

// SetProxy 设置 Embedding 接口代理；proxyURL 为空时清除
func (c *EmbeddingClient) SetProxy(proxyURL string) {
	proxyutil.Apply(c.httpClient, proxyURL)
}

// Model 返回当前使用的模型名称
func (c *EmbeddingClient) Model() string {
	return c.model
}

// EmbeddingRequest Embedding 请求
type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// EmbeddingResponse Embedding 响应
type EmbeddingResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error *FlexAPIError `json:"error,omitempty"`
}

// FlexAPIError 兼容不同 API 的 error 字段格式：
// OpenAI 为对象 {"error":{"message":...}}，LM Studio 等本地服务为字符串 {"error":"..."}
type FlexAPIError struct {
	Message string
}

func (e *FlexAPIError) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	if strings.HasPrefix(s, "\"") { // 字符串形式
		return json.Unmarshal(b, &e.Message)
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &obj); err == nil {
		e.Message = obj.Message
		return nil
	}
	e.Message = s // 其他形式原样保留
	return nil
}

// GetEmbedding 获取文本向量
func (c *EmbeddingClient) GetEmbedding(ctx context.Context, text string) ([]float32, error) {
	reqBody := EmbeddingRequest{
		Model: c.model,
		Input: text,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var embResp EmbeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, snippet)
	}

	if embResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", embResp.Error.Message)
	}

	if len(embResp.Data) == 0 {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("no embedding in response (body: %s)", snippet)
	}

	return embResp.Data[0].Embedding, nil
}

// SerializeEmbedding 将向量序列化为字节
func SerializeEmbedding(embedding []float32) ([]byte, error) {
	return json.Marshal(embedding)
}

// DeserializeEmbedding 将字节反序列化为向量
func DeserializeEmbedding(data []byte) ([]float32, error) {
	var embedding []float32
	if err := json.Unmarshal(data, &embedding); err != nil {
		return nil, err
	}
	return embedding, nil
}
