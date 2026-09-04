package ai

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBuildCacheKey(t *testing.T) {
	key1 := buildCacheKey("analyze", "gpt-4", "hello world")
	key2 := buildCacheKey("analyze", "gpt-4", "hello world")
	key3 := buildCacheKey("analyze", "gpt-3.5", "hello world")
	key4 := buildCacheKey("translate", "gpt-4", "hello world")

	if key1 != key2 {
		t.Error("same inputs should produce same key")
	}
	if key1 == key3 {
		t.Error("different models should produce different keys")
	}
	if key1 == key4 {
		t.Error("different task types should produce different keys")
	}
}

func TestLLMCache_CachedCall(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()

	ctx := context.Background()
	callCount := 0
	llmFn := func() (string, error) {
		callCount++
		return "test result", nil
	}

	// 第一次：未命中缓存
	result, err := cache.CachedCall(ctx, "test-key-1", llmFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "test result" {
		t.Errorf("expected 'test result', got '%s'", result)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// 第二次：命中缓存
	result, err = cache.CachedCall(ctx, "test-key-1", llmFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "test result" {
		t.Errorf("expected 'test result', got '%s'", result)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (cache hit), got %d", callCount)
	}

	// 不同 key：未命中
	_, _ = cache.CachedCall(ctx, "test-key-2", llmFn)
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestLLMCache_CleanExpired(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()

	ctx := context.Background()

	// 插入一个立即过期的条目
	cache.defaultTTL = -1 * time.Second
	_, _ = cache.CachedCall(ctx, "expired-key", func() (string, error) {
		return "old result", nil
	})

	count, err := cache.CleanExpired()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 expired entry cleaned, got %d", count)
	}
}

func TestLLMCache_Invalidate(t *testing.T) {
	cache, cleanup := newTestCache(t)
	defer cleanup()

	ctx := context.Background()
	_, _ = cache.CachedCall(ctx, "invalidate-key", func() (string, error) {
		return "result", nil
	})

	err := cache.Invalidate("invalidate-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	callCount := 0
	_, _ = cache.CachedCall(ctx, "invalidate-key", func() (string, error) {
		callCount++
		return "new result", nil
	})
	if callCount != 1 {
		t.Errorf("expected llmFn to be called after invalidation, got %d calls", callCount)
	}
}

func newTestCache(t *testing.T) (*LLMCache, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS llm_cache (
		key_hash   TEXT PRIMARY KEY,
		result     TEXT NOT NULL,
		model      TEXT NOT NULL,
		task_type  TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatalf("failed to create llm_cache table: %v", err)
	}

	cache := &LLMCache{
		db:         db,
		defaultTTL: 7 * 24 * time.Hour,
	}
	return cache, func() { db.Close() }
}
