package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rss-ai/internal/config"
)

func TestAnalyzeArticle(t *testing.T) {
	// 这个测试需要 mock，暂时跳过
	t.Skip("需要 mock LLM API")
}

func TestAnalyzePromptSplit(t *testing.T) {
	title := "Test Article Title"
	content := "This is the full content of the test article. It contains information about Go programming."

	user := BuildAnalyzeUserPrompt(title, content)
	if !contains(user, title) || !contains(user, content) {
		t.Error("user message should contain article title and content")
	}

	// system 指令与文章数据分离：system 不含文章内容，user 不含任务指令
	if contains(DefaultAnalyzeSystemPrompt, content) {
		t.Error("system prompt should not contain article content")
	}
	if !contains(DefaultAnalyzeSystemPrompt, "JSON") || !contains(DefaultAnalyzeSystemPrompt, "is_ad") {
		t.Error("system prompt should contain JSON schema and ad rules")
	}
	// 翻译 user 消息应就是待翻译原文本身
	if BuildTranslateUserPrompt("hello") != "hello" {
		t.Error("translate user message should be raw content")
	}
}

func TestChatWithSystemMessages(t *testing.T) {
	var lastMessages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		lastMessages = req.Messages
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	c := NewLLMClient(&config.LLMConfig{
		BaseURL: srv.URL, APIKey: "k", Model: "m", Timeout: 5 * time.Second, MaxRetries: 1,
	})
	if _, err := c.ChatWithSystem(context.Background(), "你是助手", "你好"); err != nil {
		t.Fatal(err)
	}
	if len(lastMessages) != 2 || lastMessages[0].Role != "system" || lastMessages[0].Content != "你是助手" ||
		lastMessages[1].Role != "user" || lastMessages[1].Content != "你好" {
		t.Errorf("messages = %+v, want [system, user]", lastMessages)
	}

	// 无 system 时只有 user 一条
	if _, err := c.Chat(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if len(lastMessages) != 1 || lastMessages[0].Role != "user" {
		t.Errorf("messages = %+v, want single user", lastMessages)
	}
}

func TestTruncateRunes(t *testing.T) {
	// 纯英文按字节
	if got := TruncateRunes("abcdef", 3); got != "abc" {
		t.Errorf("TruncateRunes(abcdef,3) = %q", got)
	}
	// 中文 3 字节/字：预算 7 字节应切在 6（2 个完整汉字），不产生半个字符
	if got := TruncateRunes("你好世界", 7); got != "你好" {
		t.Errorf("TruncateRunes(你好世界,7) = %q, want 你好", got)
	}
	// 未超预算原样返回
	if got := TruncateRunes("你好", 100); got != "你好" {
		t.Errorf("TruncateRunes short = %q", got)
	}
}

func TestParseAnalyzeResponse(t *testing.T) {
	jsonResponse := `{
		"is_ad": false,
		"ad_reason": null,
		"content_cleaned": "Cleaned content here",
		"summary": "This is a summary of the article.",
		"keywords": ["go", "programming", "testing"],
		"tags": ["golang", "tdd", "backend"]
	}`

	result, err := ParseAnalyzeResponse(jsonResponse)
	if err != nil {
		t.Fatalf("ParseAnalyzeResponse() error = %v", err)
	}

	if result.IsAd {
		t.Error("IsAd should be false")
	}
	if result.Summary != "This is a summary of the article." {
		t.Errorf("Summary = %v", result.Summary)
	}
	if len(result.Keywords) != 3 {
		t.Errorf("Keywords length = %v, want 3", len(result.Keywords))
	}
	if len(result.Tags) != 3 {
		t.Errorf("Tags length = %v, want 3", len(result.Tags))
	}
}

func TestParseAnalyzeResponseAd(t *testing.T) {
	jsonResponse := `{
		"is_ad": true,
		"ad_reason": "Contains promotional content for a product",
		"content_cleaned": "",
		"summary": "",
		"keywords": [],
		"tags": []
	}`

	result, err := ParseAnalyzeResponse(jsonResponse)
	if err != nil {
		t.Fatalf("ParseAnalyzeResponse() error = %v", err)
	}

	if !result.IsAd {
		t.Error("IsAd should be true")
	}
	if result.AdReason != "Contains promotional content for a product" {
		t.Errorf("AdReason = %v", result.AdReason)
	}
}

func TestParseInvalidJSONResponse(t *testing.T) {
	invalidJSON := `{invalid json`

	_, err := ParseAnalyzeResponse(invalidJSON)
	if err == nil {
		t.Error("ParseAnalyzeResponse() should return error for invalid JSON")
	}
}

func TestCalculateCosineSimilarity(t *testing.T) {
	// 相同向量，相似度应为 1.0
	vec1 := []float32{1.0, 0.0, 0.0}
	vec2 := []float32{1.0, 0.0, 0.0}

	similarity := CalculateCosineSimilarity(vec1, vec2)
	if similarity != 1.0 {
		t.Errorf("Similarity = %v, want 1.0", similarity)
	}

	// 正交向量，相似度应为 0.0
	vec3 := []float32{1.0, 0.0, 0.0}
	vec4 := []float32{0.0, 1.0, 0.0}

	similarity = CalculateCosineSimilarity(vec3, vec4)
	if similarity != 0.0 {
		t.Errorf("Similarity = %v, want 0.0", similarity)
	}

	// 相反向量，相似度应为 -1.0
	vec5 := []float32{1.0, 0.0}
	vec6 := []float32{-1.0, 0.0}

	similarity = CalculateCosineSimilarity(vec5, vec6)
	if similarity != -1.0 {
		t.Errorf("Similarity = %v, want -1.0", similarity)
	}
}

func TestCalculateCosineSimilarityDifferentLengths(t *testing.T) {
	vec1 := []float32{1.0, 0.0, 0.0}
	vec2 := []float32{1.0, 0.0}

	defer func() {
		if r := recover(); r == nil {
			t.Error("CalculateCosineSimilarity should panic for different length vectors")
		}
	}()

	CalculateCosineSimilarity(vec1, vec2)
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestLLMFallbackModel(t *testing.T) {
	// makeServer: 指定 model 返回 500（模拟主模型故障），其余模型正常
	makeServer := func(failModel string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Model == failModel {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error": {"message": "model overloaded"}}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "ok:" + req.Model}},
				},
			})
		}))
	}

	t.Run("同端点只换备用模型", func(t *testing.T) {
		srv := makeServer("main-model")
		defer srv.Close()
		c := NewLLMClient(&config.LLMConfig{
			BaseURL: srv.URL, APIKey: "k", Model: "main-model",
			Fallback: config.LLMFallbackConfig{Model: "backup-model"}, // base_url/key 留空沿用主配置
			Timeout: 5 * time.Second, MaxRetries: 2,
		})
		got, err := c.Chat(context.Background(), "hi")
		if err != nil {
			t.Fatalf("Chat() error = %v, want nil", err)
		}
		if got != "ok:backup-model" {
			t.Errorf("Chat() = %q, want %q", got, "ok:backup-model")
		}
	})

	t.Run("备用为完全独立端点", func(t *testing.T) {
		mainSrv := makeServer("main-model")
		defer mainSrv.Close()
		backupSrv := makeServer("") // 备用端点不故障
		defer backupSrv.Close()
		c := NewLLMClient(&config.LLMConfig{
			BaseURL: mainSrv.URL, APIKey: "k", Model: "main-model",
			Fallback: config.LLMFallbackConfig{BaseURL: backupSrv.URL, APIKey: "k2", Model: "backup-model"},
			Timeout: 5 * time.Second, MaxRetries: 2,
		})
		got, err := c.Chat(context.Background(), "hi")
		if err != nil {
			t.Fatalf("Chat() error = %v, want nil", err)
		}
		if got != "ok:backup-model" {
			t.Errorf("Chat() = %q, want %q", got, "ok:backup-model")
		}
	})

	t.Run("未配置备用时直接失败", func(t *testing.T) {
		srv := makeServer("main-model")
		defer srv.Close()
		c := NewLLMClient(&config.LLMConfig{
			BaseURL: srv.URL, APIKey: "k", Model: "main-model",
			Timeout: 5 * time.Second, MaxRetries: 2,
		})
		if _, err := c.Chat(context.Background(), "hi"); err == nil {
			t.Fatal("Chat() should fail without fallback")
		}
	})
}
