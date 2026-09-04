package config

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// 创建临时配置文件
	content := `
server:
  host: "127.0.0.1"
  port: 8080
database:
  path: "./data/rss.db"
ai:
  llm:
    base_url: "https://api.example.com/v1"
    api_key: "test-key"
    model: "test-model"
    timeout: "60s"
  embedding:
    base_url: "https://api.example.com/v1"
    api_key: "test-key"
    model: "text-embedding-3-small"
    timeout: "30s"
feeds:
  default_interval: "30m"
  max_concurrent: 5
  timeout: "30s"
scheduler:
  morning_report_time: "08:00"
  evening_report_time: "20:00"
cleanup:
  ad_retention_days: 7
  notification_retention_days: 14
  auto_cleanup_time: "03:00"
`
	tmpfile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	cfg, err := Load(tmpfile.Name())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// 验证 Server 配置
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %v, want 127.0.0.1", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %v, want 8080", cfg.Server.Port)
	}

	// 验证 Database 配置
	if cfg.Database.Path != "./data/rss.db" {
		t.Errorf("Database.Path = %v, want ./data/rss.db", cfg.Database.Path)
	}

	// 验证 AI LLM 配置
	if cfg.AI.LLM.BaseURL != "https://api.example.com/v1" {
		t.Errorf("AI.LLM.BaseURL = %v, want https://api.example.com/v1", cfg.AI.LLM.BaseURL)
	}
	if cfg.AI.LLM.APIKey != "test-key" {
		t.Errorf("AI.LLM.APIKey = %v, want test-key", cfg.AI.LLM.APIKey)
	}
	if cfg.AI.LLM.Model != "test-model" {
		t.Errorf("AI.LLM.Model = %v, want test-model", cfg.AI.LLM.Model)
	}

	// 验证 AI Embedding 配置
	if cfg.AI.Embedding.Model != "text-embedding-3-small" {
		t.Errorf("AI.Embedding.Model = %v, want text-embedding-3-small", cfg.AI.Embedding.Model)
	}

	// 验证 Feeds 配置
	if cfg.Feeds.MaxConcurrent != 5 {
		t.Errorf("Feeds.MaxConcurrent = %v, want 5", cfg.Feeds.MaxConcurrent)
	}

	// 验证 Scheduler 配置
	if cfg.Scheduler.MorningReportTime != "08:00" {
		t.Errorf("Scheduler.MorningReportTime = %v, want 08:00", cfg.Scheduler.MorningReportTime)
	}

	// 验证 Cleanup 配置
	if cfg.Cleanup.AdRetentionDays != 7 {
		t.Errorf("Cleanup.AdRetentionDays = %v, want 7", cfg.Cleanup.AdRetentionDays)
	}
}

func TestLoadConfigWithDefaults(t *testing.T) {
	// 创建最小配置文件
	content := `
ai:
  llm:
    api_key: "test-key"
`
	tmpfile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	cfg, err := Load(tmpfile.Name())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// 验证默认值
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host default = %v, want 127.0.0.1", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port default = %v, want 8080", cfg.Server.Port)
	}
	if cfg.Database.Path != "./data/rss.db" {
		t.Errorf("Database.Path default = %v, want ./data/rss.db", cfg.Database.Path)
	}
	if cfg.Feeds.MaxConcurrent != 5 {
		t.Errorf("Feeds.MaxConcurrent default = %v, want 5", cfg.Feeds.MaxConcurrent)
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("Load() should return error for nonexistent file")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	// 创建无效的 YAML 文件
	content := `
server:
  host: [invalid
`
	tmpfile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	_, err = Load(tmpfile.Name())
	if err == nil {
		t.Error("Load() should return error for invalid YAML")
	}
}
