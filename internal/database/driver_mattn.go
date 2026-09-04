//go:build linux && cgo

package database

import (
	"path/filepath"
	"strings"
)

import _ "github.com/mattn/go-sqlite3"

func sqliteDriver() string {
	return "sqlite3"
}

// sqliteDSN 构建连接串：设置 busy_timeout（连接池中每个连接生效，避免并发写入时 SQLITE_BUSY）
func sqliteDSN(path string) string {
	return "file:" + strings.TrimPrefix(filepath.ToSlash(path), "file:") + "?_busy_timeout=10000"
}
