package ai

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"time"
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// LLMCache LLM 结果缓存
type LLMCache struct {
	db         *sql.DB
	defaultTTL time.Duration
}

// NewLLMCache 创建缓存实例
func NewLLMCache(db *sql.DB, ttl time.Duration) *LLMCache {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour // 默认 7 天
	}
	return &LLMCache{db: db, defaultTTL: ttl}
}

// buildCacheKey 生成缓存 key
func buildCacheKey(taskType, model, content string) string {
	h := md5.Sum([]byte(model + ":" + taskType + ":" + content))
	return hex.EncodeToString(h[:])
}

// CachedCall 通用缓存包装
func (c *LLMCache) CachedCall(ctx context.Context, cacheKey string, llmFn func() (string, error)) (string, error) {
	// 1. 查询缓存
	var result string
	err := c.db.QueryRowContext(ctx,
		"SELECT result FROM llm_cache WHERE key_hash = ? AND expires_at > ?",
		cacheKey, time.Now().Unix()).Scan(&result)
	if err == nil {
		n := minInt(len(cacheKey), 12)
		log.Printf("LLM cache hit: key=%s", cacheKey[:n])
		return result, nil
	}
	if err != sql.ErrNoRows {
		log.Printf("LLM cache query error: %v", err)
	}

	// 2. 缓存未命中，调用 LLM
	result, err = llmFn()
	if err != nil {
		return "", err
	}

	// 3. 写入缓存
	now := time.Now().Unix()
	expiresAt := time.Now().Add(c.defaultTTL).Unix()
	_, err = c.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO llm_cache (key_hash, result, model, task_type, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		cacheKey, result, "", "", now, expiresAt)
	if err != nil {
		log.Printf("LLM cache write error: %v", err)
	}

	return result, nil
}

// CleanExpired 清理过期缓存
func (c *LLMCache) CleanExpired() (int64, error) {
	result, err := c.db.Exec("DELETE FROM llm_cache WHERE expires_at < ?", time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("failed to clean expired cache: %w", err)
	}
	return result.RowsAffected()
}

// Invalidate 删除指定缓存
func (c *LLMCache) Invalidate(cacheKey string) error {
	_, err := c.db.Exec("DELETE FROM llm_cache WHERE key_hash = ?", cacheKey)
	return err
}
