package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
