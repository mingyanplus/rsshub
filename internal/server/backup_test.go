package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"rss-ai/internal/config"
	"rss-ai/internal/database"
)

// newTestBackupEnv 初始化全局状态并建临时数据库，返回清理函数
func newTestBackupEnv(t *testing.T) func() {
	t.Helper()
	oldCfg, oldDB := appConfig, appDB
	appConfig = &config.Config{}
	appConfig.Database.Path = filepath.Join(t.TempDir(), "test.db")
	db, err := database.New(appConfig.Database.Path)
	if err != nil {
		t.Fatalf("create test db failed: %v", err)
	}
	appDB = db
	return func() {
		db.Close()
		appConfig, appDB = oldCfg, oldDB
	}
}

// 自动备份：未启用不产生文件；无备份文件时执行；距最近备份不足间隔时不重复执行
func TestRunAutoBackupOnce(t *testing.T) {
	cleanup := newTestBackupEnv(t)
	defer cleanup()
	appConfig.DataBackup.Interval = time.Hour
	appConfig.DataBackup.MaxFiles = 5

	// 未启用：不产生备份
	RunAutoBackup()
	if backups := listDBBackups(); len(backups) != 0 {
		t.Fatalf("disabled auto backup created %d files", len(backups))
	}

	// 启用且无历史备份：立即备份
	appConfig.DataBackup.AutoEnable = true
	RunAutoBackup()
	if backups := listDBBackups(); len(backups) != 1 {
		t.Fatalf("auto backup should create 1 file, got %d", len(backups))
	}

	// 间隔未到：不重复备份
	RunAutoBackup()
	if backups := listDBBackups(); len(backups) != 1 {
		t.Fatalf("auto backup should skip within interval, got %d files", len(backups))
	}

	// 最近的备份文件时间戳早于间隔：再次备份（时间戳写在文件名里，改名即回拨）
	old := time.Now().Add(-2 * time.Hour).Format(backupTimeLayout)
	newest := listDBBackups()[0].Filename
	oldPath := filepath.Join(backupDir(), "ai-reader-backup-"+old+".db")
	if err := os.Rename(filepath.Join(backupDir(), newest), oldPath); err != nil {
		t.Fatalf("rename backup failed: %v", err)
	}
	RunAutoBackup()
	if backups := listDBBackups(); len(backups) != 2 {
		t.Fatalf("auto backup should run when newest is stale, got %d files", len(backups))
	}
}

// 超出保留上限的备份被清理，最旧的先删
func TestPruneBackups(t *testing.T) {
	cleanup := newTestBackupEnv(t)
	defer cleanup()
	appConfig.DataBackup.MaxFiles = 2

	// 手工造 4 个备份（文件名时间戳递增）
	for i, ts := range []string{"20260101-000001", "20260102-000002", "20260103-000003", "20260104-000004"} {
		p := filepath.Join(backupDir(), "ai-reader-backup-"+ts+".db")
		if err := os.MkdirAll(backupDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneBackups(2)
	backups := listDBBackups()
	if len(backups) != 2 {
		t.Fatalf("prune should keep 2, got %d", len(backups))
	}
	if backups[0].Filename != "ai-reader-backup-20260104-000004.db" ||
		backups[1].Filename != "ai-reader-backup-20260103-000003.db" {
		t.Errorf("prune should drop oldest, got %+v", backups)
	}
}
