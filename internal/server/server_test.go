package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rss-ai/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	router := NewRouter()
	server := httptest.NewServer(router)

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestReadyEndpoint(t *testing.T) {
	router := NewRouter()
	server := httptest.NewServer(router)

	resp, err := http.Get(server.URL + "/ready")
	if err != nil {
		t.Fatalf("GET /ready error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestListFeedsEndpoint(t *testing.T) {
	router := NewRouter()
	server := httptest.NewServer(router)

	resp, err := http.Get(server.URL + "/api/feeds")
	if err != nil {
		t.Fatalf("GET /api/feeds error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestCreateFeedEndpoint(t *testing.T) {
	router := NewRouter()
	server := httptest.NewServer(router)

	resp, err := http.Post(server.URL+"/api/feeds", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/feeds error: %v", err)
	}
	defer resp.Body.Close()

	// 应该返回 400 因为没有 body
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestNotFoundEndpoint(t *testing.T) {
	router := NewRouter()
	server := httptest.NewServer(router)

	resp, err := http.Get(server.URL + "/nonexistent")
	if err != nil {
		t.Fatalf("GET /nonexistent error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestRouterGrouping(t *testing.T) {
	router := NewRouter()

	// 验证路由器已创建
	if router == nil {
		t.Error("NewRouter should not return nil")
	}
}

func TestSaveConfigToFileServerPassword(t *testing.T) {
	oldCfg, oldPath := appConfig, configFilePath
	defer func() { appConfig, configFilePath = oldCfg, oldPath }()

	base := "server:\n  host: \"127.0.0.1\"\n  port: 8080\n\ndatabase:\n  path: \"./data/rss.db\"\n\npush:\n  email:\n    password: \"smtp-pass\"\n"

	t.Run("server段无password行则插入", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
			t.Fatal(err)
		}
		appConfig = &config.Config{}
		appConfig.Server.Password = "new-pass"
		appConfig.Push.Email.Password = "smtp-pass" // 与文件现值一致，避免既有 email 段更新逻辑干扰断言
		configFilePath = path
		if err := saveConfigToFile(); err != nil {
			t.Fatal(err)
		}
		out, _ := os.ReadFile(path)
		s := string(out)
		if !strings.Contains(s, "server:\n  password: \"new-pass\"\n") {
			t.Errorf("password 未正确插入 server 段:\n%s", s)
		}
		if !strings.Contains(s, "password: \"smtp-pass\"") {
			t.Errorf("email password 被误改:\n%s", s)
		}
	})

	t.Run("server段已有password行则行内更新", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		content := "server:\n  host: \"0.0.0.0\"\n  password: \"old\"\ndatabase:\n  path: \"/data/rss.db\"\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		appConfig = &config.Config{}
		appConfig.Server.Password = ""
		configFilePath = path
		if err := saveConfigToFile(); err != nil {
			t.Fatal(err)
		}
		out, _ := os.ReadFile(path)
		s := string(out)
		if !strings.Contains(s, "password: \"\"") || strings.Contains(s, "\"old\"") {
			t.Errorf("password 未正确更新为空:\n%s", s)
		}
		if strings.Count(s, "password:") != 1 {
			t.Errorf("password 行出现多次:\n%s", s)
		}
	})
}

func TestSaveConfigToFileLLMFallback(t *testing.T) {
	oldCfg, oldPath := appConfig, configFilePath
	defer func() { appConfig, configFilePath = oldCfg, oldPath }()

	t.Run("llm段无fallback子段则插入完整块", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		content := "ai:\n    llm:\n        model: \"main\"\n        timeout: 60s\n    embedding:\n        model: \"emb\"\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		appConfig = &config.Config{}
		appConfig.AI.LLM.Model = "main"
		appConfig.AI.LLM.Fallback = config.LLMFallbackConfig{BaseURL: "https://b.com/v1", APIKey: "bk", Model: "backup"}
		appConfig.AI.Embedding.Model = "emb" // 与文件现值一致，避免既有 embedding 段更新逻辑干扰断言
		configFilePath = path
		if err := saveConfigToFile(); err != nil {
			t.Fatal(err)
		}
		out, _ := os.ReadFile(path)
		s := string(out)
		want := "        model: \"main\"\n" +
			"        fallback:\n" +
			"          base_url: \"https://b.com/v1\"\n" +
			"          api_key: \"bk\"\n" +
			"          model: \"backup\"\n"
		if !strings.Contains(s, want) {
			t.Errorf("fallback 子段未按缩进插入:\n%s", s)
		}
		if !strings.Contains(s, "        timeout: 60s") {
			t.Errorf("timeout 行受影响:\n%s", s)
		}
	})

	t.Run("llm段已有fallback子段则行内更新", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		content := "ai:\n  llm:\n    model: \"main\"\n    base_url: \"https://a.com/v1\"\n    fallback:\n      base_url: \"https://old.com/v1\"\n      api_key: \"oldk\"\n      model: \"old\"\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		appConfig = &config.Config{}
		appConfig.AI.LLM.Model = "main"
		appConfig.AI.LLM.BaseURL = "https://a.com/v1"
		appConfig.AI.LLM.Fallback = config.LLMFallbackConfig{BaseURL: "https://new.com/v1", APIKey: "newk", Model: "new"}
		configFilePath = path
		if err := saveConfigToFile(); err != nil {
			t.Fatal(err)
		}
		out, _ := os.ReadFile(path)
		s := string(out)
		if !strings.Contains(s, "base_url: \"https://new.com/v1\"") || strings.Contains(s, "old.com") || strings.Contains(s, "\"old\"") {
			t.Errorf("fallback 子段未正确更新:\n%s", s)
		}
		if strings.Count(s, "fallback:") != 1 || strings.Count(s, "base_url:") != 2 {
			t.Errorf("行数异常:\n%s", s)
		}
	})
}

func TestFetchModels(t *testing.T) {
	// 模拟 OpenAI 兼容 /models 接口
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]string{{"id": "model-a"}, {"id": "model-b"}},
		})
	}))
	defer srv.Close()

	router := NewRouter()
	ts := httptest.NewServer(router)
	defer ts.Close()

	// 正常拉取
	resp, err := http.Post(ts.URL+"/api/settings/models", "application/json",
		strings.NewReader(`{"base_url":"`+srv.URL+`/v1","api_key":"k"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Success bool     `json:"success"`
		Models  []string `json:"models"`
		Message string   `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if !result.Success || len(result.Models) != 2 || result.Models[0] != "model-a" {
		t.Errorf("FetchModels = %+v, want 2 models", result)
	}

	// 缺参数
	resp2, _ := http.Post(ts.URL+"/api/settings/models", "application/json",
		strings.NewReader(`{"base_url":""}`))
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp2.StatusCode)
	}
}
