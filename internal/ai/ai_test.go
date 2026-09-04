package ai

import (
	"testing"
)

func TestAnalyzeArticle(t *testing.T) {
	// 这个测试需要 mock，暂时跳过
	t.Skip("需要 mock LLM API")
}

func TestBuildAnalyzePrompt(t *testing.T) {
	title := "Test Article Title"
	content := "This is the full content of the test article. It contains information about Go programming."

	prompt := BuildAnalyzePrompt(title, content)

	if !contains(prompt, title) {
		t.Error("Prompt should contain article title")
	}
	if !contains(prompt, content) {
		t.Error("Prompt should contain article content")
	}
	if !contains(prompt, "JSON") {
		t.Error("Prompt should request JSON format")
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
