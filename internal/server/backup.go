package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"rss-ai/internal/config"

	"github.com/go-chi/chi/v5"
)

// backupDir 备份目录：数据库文件同目录下的 backups/
func backupDir() string {
	if appConfig == nil {
		return "backups"
	}
	return filepath.Join(filepath.Dir(appConfig.Database.Path), "backups")
}

// backupNameRe 合法备份文件名（ai-reader-backup-YYYYMMDD-HHMMSS.db），
// 下载/删除按此白名单校验，杜绝路径穿越
var backupNameRe = regexp.MustCompile(`^ai-reader-backup-(\d{8}-\d{6})\.db$`)

// backupTimeLayout 备份文件名内嵌的时间戳格式
const backupTimeLayout = "20060102-150405"

// backupInfo 备份文件信息（CreatedAt 以文件名时间戳为准——文件复制/恢复不改名，比 ModTime 可靠）
type backupInfo struct {
	Filename  string    `json:"filename"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"-"`
	CreatedAtText string `json:"created_at"`
	SizeText  string    `json:"size_h"`
}

// backupTimeFromName 从备份文件名解析时间戳；格式不符返回零值
func backupTimeFromName(name string) time.Time {
	m := backupNameRe.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}
	}
	t, err := time.ParseInLocation(backupTimeLayout, m[1], time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// createDBBackup 用 VACUUM INTO 生成一致性快照（顺带压缩碎片），
// 之后按 max_files 上限清理最旧的备份。返回文件名与字节数
func createDBBackup() (string, int64, error) {
	if appDB == nil || appConfig == nil {
		return "", 0, fmt.Errorf("database not initialized")
	}
	dir := backupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create backup dir failed: %w", err)
	}
	name := "ai-reader-backup-" + time.Now().Format(backupTimeLayout) + ".db"
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return "", 0, fmt.Errorf("backup file already exists: %s", name)
	}
	if _, err := appDB.SQL().Exec("VACUUM INTO ?", path); err != nil {
		return "", 0, fmt.Errorf("vacuum into failed: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat backup failed: %w", err)
	}
	pruneBackups(maxBackupFiles())
	return name, info.Size(), nil
}

// backupInterval 自动备份间隔（非法配置回退默认）
func backupInterval() time.Duration {
	if appConfig != nil && appConfig.DataBackup.Interval > 0 {
		return appConfig.DataBackup.Interval
	}
	return config.DefaultBackupInterval
}

// maxBackupFiles 备份保留上限（非法配置回退默认）
func maxBackupFiles() int {
	if appConfig != nil && appConfig.DataBackup.MaxFiles > 0 {
		return appConfig.DataBackup.MaxFiles
	}
	return config.DefaultMaxBackupFiles
}

// listDBBackups 备份文件信息列表，按时间倒序（最新在前）
func listDBBackups() []backupInfo {
	entries, err := os.ReadDir(backupDir())
	if err != nil {
		return nil
	}
	backups := make([]backupInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !backupNameRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ts := backupTimeFromName(e.Name())
		if ts.IsZero() {
			ts = info.ModTime() // 名字解析失败的兜底
		}
		backups = append(backups, backupInfo{
			Filename:      e.Name(),
			Size:          info.Size(),
			CreatedAt:     ts,
			CreatedAtText: ts.Format("2006-01-02 15:04:05"),
			SizeText:      formatBytes(info.Size()),
		})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].CreatedAt.After(backups[j].CreatedAt) })
	return backups
}

// pruneBackups 超出 keep 上限时删除最旧的备份
func pruneBackups(keep int) {
	if keep <= 0 {
		return
	}
	backups := listDBBackups() // 倒序：最旧在后
	if len(backups) <= keep {
		return
	}
	for _, b := range backups[keep:] {
		if err := os.Remove(filepath.Join(backupDir(), b.Filename)); err != nil {
			fmt.Printf("DataBackup: prune old backup %s failed: %v\n", b.Filename, err)
		} else {
			fmt.Printf("DataBackup: pruned old backup %s (limit %d)\n", b.Filename, keep)
		}
	}
}

// RunAutoBackup 定时自动备份的单次检查（供调度器周期调用）：
// 距最近一次备份超过配置间隔才执行，避免重启风暴
func RunAutoBackup() {
	if appConfig == nil || !appConfig.DataBackup.AutoEnable || appDB == nil {
		return
	}
	backups := listDBBackups()
	if len(backups) > 0 && time.Since(backups[0].CreatedAt) < backupInterval() {
		return
	}
	name, size, err := createDBBackup()
	if err != nil {
		fmt.Printf("DataBackup: auto backup failed: %v\n", err)
		return
	}
	fmt.Printf("DataBackup: auto backup created %s (%d bytes)\n", name, size)
}

// BackupDB 手动创建数据库备份。VACUUM INTO 是全库拷贝，大库耗时可达数秒到数十秒，
// 异步执行立即返回 202，前端稍后经备份列表看到新文件
func BackupDB(w http.ResponseWriter, r *http.Request) {
	go func() {
		name, size, err := createDBBackup()
		if err != nil {
			fmt.Printf("DataBackup: manual backup failed: %v\n", err)
			return
		}
		fmt.Printf("DataBackup: manual backup created %s (%d bytes)\n", name, size)
	}()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "备份已在后台执行，稍后刷新列表查看",
	})
}

// ListBackups 备份文件列表（size_h 为服务端预格式化的人类可读大小）
func ListBackups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	autoEnabled := appConfig != nil && appConfig.DataBackup.AutoEnable
	json.NewEncoder(w).Encode(map[string]interface{}{
		"backups":     listDBBackups(),
		"auto_enable": autoEnabled,
		"interval_h":  int(backupInterval().Hours()),
		"max_files":   maxBackupFiles(),
	})
}

// DownloadBackup 下载备份文件（文件名白名单校验，防路径穿越）
func DownloadBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !backupNameRe.MatchString(name) {
		http.Error(w, "invalid backup filename", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, filepath.Join(backupDir(), name))
}

// DeleteBackup 删除指定备份文件
func DeleteBackup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !backupNameRe.MatchString(name) {
		http.Error(w, "invalid backup filename", http.StatusBadRequest)
		return
	}
	if err := os.Remove(filepath.Join(backupDir(), name)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// UpdateBackupConfig 保存自动备份配置（内存 + config.yaml）
func UpdateBackupConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AutoEnable bool `json:"auto_enable"`
		IntervalH  int  `json:"interval_h"`
		MaxFiles   int  `json:"max_files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.IntervalH <= 0 || req.IntervalH > 24*30 {
		http.Error(w, "interval_h must be 1..720", http.StatusBadRequest)
		return
	}
	if req.MaxFiles <= 0 || req.MaxFiles > 100 {
		http.Error(w, "max_files must be 1..100", http.StatusBadRequest)
		return
	}
	if appConfig == nil {
		http.Error(w, "config not initialized", http.StatusInternalServerError)
		return
	}
	appConfig.DataBackup.AutoEnable = req.AutoEnable
	appConfig.DataBackup.Interval = time.Duration(req.IntervalH) * time.Hour
	appConfig.DataBackup.MaxFiles = req.MaxFiles
	if err := saveConfigToFile(); err != nil {
		http.Error(w, "save config failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
