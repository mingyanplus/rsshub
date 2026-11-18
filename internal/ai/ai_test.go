package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// LLM 偶发在字符串值内输出未转义引号（真实线上样例），修复后应能解析
func TestParseAnalyzeResponseUnescapedQuotes(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			name: "中文值内嵌引号",
			json: `{
  "is_ad": false,
  "summary": "小米将发布旗下首款"中折叠"手机小米18 Fold。",
  "keywords": ["小米18 Fold"],
  "importance_score": 7
}`,
		},
		{
			name: "英文短语引号+值尾引号",
			json: `{
  "is_ad": false,
  "summary": "特斯拉宣布启动活动"Cybercab Japan Tour"，覆盖东京等四城。",
  "keywords": ["特斯拉"],
  "importance_score": 6
}`,
		},
		{
			name: "已转义引号不受影响",
			json: `{
  "is_ad": false,
  "summary": "正常\"转义\"内容",
  "keywords": []
}`,
		},
	}
	for _, tc := range cases {
		result, err := ParseAnalyzeResponse(tc.json)
		if err != nil {
			t.Errorf("%s: ParseAnalyzeResponse() error = %v", tc.name, err)
			continue
		}
		if result.IsAd {
			t.Errorf("%s: IsAd should be false", tc.name)
		}
	}
}

// 2026-11-18 线上 5 条解析失败的真实样例（浓缩），四种畸形逐一复现：
// 38034 裸引号+非法转义 \ 、38124 裸引号后跟 ASCII 逗号、39083 值后全角逗号分隔符、39328 裸反斜杠 \sigma
func TestParseAnalyzeResponseMalformedVariants(t *testing.T) {
	cases := []struct {
		name string
		json string
		check func(*AnalyzeResult) error
	}{
		{
			name: "裸引号加非法转义_反斜杠空格",
			json: `{
  "is_ad": false,
  "content_cleaned": "微软已推送重新设计的"运行"窗口，采用 WinUI 3 构建，并支持输入 ~\ 快速进入用户文件夹。",
  "summary": "微软将推出新版运行窗口。",
  "keywords": ["Windows 11", "运行窗口"],
  "tags": ["操作系统"],
  "importance_score": 7
}`,
			check: func(r *AnalyzeResult) error {
				if !strings.Contains(r.ContentCleaned, `设计的"运行"窗口`) {
					return fmt.Errorf("ContentCleaned 缺少内嵌引号原文: %q", r.ContentCleaned)
				}
				if !strings.Contains(r.ContentCleaned, `~\ 快速进入`) {
					return fmt.Errorf("ContentCleaned 缺少字面反斜杠: %q", r.ContentCleaned)
				}
				return nil
			},
		},
		{
			name: "裸引号后跟ASCII逗号",
			json: `{
  "is_ad": false,
  "content_cleaned": "微软起初将原因归结为多个地区基础设施上的 " 负载升高 ", 随后暂停了部署。大家又都拿起了电话, 聊天群也比平时更忙了。",
  "summary": "微软服务故障重创金融业。",
  "keywords": ["微软服务故障"],
  "importance_score": 7
}`,
			check: func(r *AnalyzeResult) error {
				if !strings.Contains(r.ContentCleaned, `" 负载升高 ", 随后暂停了部署`) {
					return fmt.Errorf("ContentCleaned 内嵌引号+逗号原文丢失: %q", r.ContentCleaned)
				}
				return nil
			},
		},
		{
			name: "值结束后全角逗号当分隔符",
			json: `{
  "is_ad": false,
  "content_cleaned": "这届网友爱上了新的解压神器，这就是最近走红的"脆皮捏捏"，外脆内软的反差带来奇妙体验。",
  "summary": "脆皮捏捏成为解压新宠。"，
  "keywords": ["脆皮捏捏", "解压玩具"],
  "importance_score": 6
}`,
			check: func(r *AnalyzeResult) error {
				if r.Summary != "脆皮捏捏成为解压新宠。" {
					return fmt.Errorf("Summary = %q", r.Summary)
				}
				if len(r.Keywords) != 2 {
					return fmt.Errorf("Keywords = %v, want 2 个", r.Keywords)
				}
				return nil
			},
		},
		{
			name: "正文裸反斜杠_非法转义",
			json: `{
  "is_ad": false,
  "content_cleaned": "概率论从最基本的测度论讲起，如\sigma代数、Borel\sigma-代数、Lebesgue测度。",
  "summary": "国内外概率论课程设置存在差异。",
  "keywords": ["概率论", "测度论"],
  "importance_score": 7
}`,
			check: func(r *AnalyzeResult) error {
				if !strings.Contains(r.ContentCleaned, `\sigma代数`) {
					return fmt.Errorf("ContentCleaned 缺少字面 \\sigma: %q", r.ContentCleaned)
				}
				return nil
			},
		},
	}
	for _, tc := range cases {
		result, err := ParseAnalyzeResponse(tc.json)
		if err != nil {
			t.Errorf("%s: ParseAnalyzeResponse() error = %v", tc.name, err)
			continue
		}
		if err := tc.check(result); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
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

// 截断响应（finish_reason=length）必须按错误处理走重试，不能把半截 JSON 当成功返回并写入缓存
func TestChatRejectsTruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]string{"role": "assistant", "content": `{"summary": "半截`},
					"finish_reason": "length",
				},
			},
			"usage": map[string]int{"completion_tokens": 1024},
		})
	}))
	defer srv.Close()

	c := NewLLMClient(&config.LLMConfig{
		BaseURL: srv.URL, APIKey: "k", Model: "m", Timeout: 5 * time.Second, MaxRetries: 1,
	})
	_, err := c.Chat(context.Background(), "hi")
	if err == nil {
		t.Fatal("Chat() should fail on truncated response (finish_reason=length)")
	}
	if !strings.Contains(err.Error(), "截断") {
		t.Errorf("err = %v, want 包含「截断」", err)
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
			Timeout:  5 * time.Second, MaxRetries: 2,
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
			Timeout:  5 * time.Second, MaxRetries: 2,
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

// LLM 偶发在纯文本字段输出 **加粗** 定界符，界面裸露星号——解析后应统一脱除
func TestParseAnalyzeResponseStripsMarkdownEmphasis(t *testing.T) {
	resp := `{
		"summary": "**OpenAI** 发布了新模型 **o5**，性能大幅提升",
		"one_line_summary": "**OpenAI 发布 o5**，性能提升",
		"ad_reason": "推广**付费课程**",
		"topic_category": "**AI 动态**",
		"keywords": ["**OpenAI**", "o5"],
		"tags": ["**模型发布**"],
		"entities": ["**OpenAI**"],
		"importance_score": 8
	}`
	res, err := ParseAnalyzeResponse(resp)
	if err != nil {
		t.Fatalf("ParseAnalyzeResponse() error = %v", err)
	}
	if res.Summary != "OpenAI 发布了新模型 o5，性能大幅提升" {
		t.Errorf("Summary = %q", res.Summary)
	}
	if res.OneLineSummary != "OpenAI 发布 o5，性能提升" {
		t.Errorf("OneLineSummary = %q", res.OneLineSummary)
	}
	if res.AdReason != "推广付费课程" {
		t.Errorf("AdReason = %q", res.AdReason)
	}
	if res.TopicCategory != "AI 动态" {
		t.Errorf("TopicCategory = %q", res.TopicCategory)
	}
	for _, list := range [][]string{res.Keywords, res.Tags, res.Entities} {
		for _, v := range list {
			if strings.Contains(v, "**") {
				t.Errorf("list item %q still contains **", v)
			}
		}
	}
	// 合法文本不受影响：__init__、乘号、单星
	keep := StripMarkdownEmphasis("Python __init__ 与 3*4 和 a*b")
	if keep != "Python __init__ 与 3*4 和 a*b" {
		t.Errorf("StripMarkdownEmphasis() = %q, want unchanged", keep)
	}
}
