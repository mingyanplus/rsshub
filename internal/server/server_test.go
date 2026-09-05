package server

import (
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
