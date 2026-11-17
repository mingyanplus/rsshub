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

// sqliteDSN 构建连接串：busy_timeout 与 foreign_keys 均进 DSN（连接池中每个连接生效，
// 使 ON DELETE CASCADE 可靠；Exec PRAGMA 只对单个连接生效，不够）
func sqliteDSN(path string) string {
	return "file:" + strings.TrimPrefix(filepath.ToSlash(path), "file:") + "?_busy_timeout=10000&_foreign_keys=on"
}
