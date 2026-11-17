//go:build !linux || !cgo

package database

import (
	"path/filepath"
	"strings"
)

import _ "modernc.org/sqlite"

func sqliteDriver() string {
	return "sqlite"
}

// sqliteDSN 构建连接串：busy_timeout 与 foreign_keys 均进 DSN，
// 对连接池中每个新连接生效（db.Exec 的 PRAGMA 只作用于当时拿到的那一个连接，
// 会导致 ON DELETE CASCADE 时灵时不灵）
func sqliteDSN(path string) string {
	return "file:" + strings.TrimPrefix(filepath.ToSlash(path), "file:") +
		"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)"
}
