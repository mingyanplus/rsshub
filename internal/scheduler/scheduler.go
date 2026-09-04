package scheduler

import (
	"fmt"
	"strings"
	"time"
)

// Task types
const (
	TaskTypeFeedRefresh     = "feed_refresh"
	TaskTypeMorningReport   = "morning_report"
	TaskTypeEveningReport   = "evening_report"
	TaskTypeCleanup         = "cleanup"
	TaskTypeFollowRuleCheck = "follow_rule_check"
)

// Task 定时任务
type Task struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Schedule string `json:"schedule"`
	Enabled  bool   `json:"enabled"`
}

// IsEnabled 检查任务是否启用
func (t *Task) IsEnabled() bool {
	return t.Enabled
}

// TaskStatus 任务状态
type TaskStatus struct {
	TaskName     string    `json:"task_name"`
	LastRun      time.Time `json:"last_run"`
	NextRun      time.Time `json:"next_run"`
	RunCount     int       `json:"run_count"`
	SuccessCount int       `json:"success_count"`
	FailureCount int       `json:"failure_count"`
	IsRunning    bool      `json:"is_running"`
}

// SuccessRate 计算成功率
func (s *TaskStatus) SuccessRate() float64 {
	if s.RunCount == 0 {
		return 0
	}
	return float64(s.SuccessCount) / float64(s.RunCount) * 100
}

// ParseScheduleTime 解析时间字符串（HH:MM 格式）
func ParseScheduleTime(timeStr string) (time.Duration, error) {
	parts := strings.Split(timeStr, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid time format: %s", timeStr)
	}

	var hour, minute int
	if _, err := fmt.Sscanf(parts[0], "%d", &hour); err != nil {
		return 0, fmt.Errorf("invalid hour: %s", parts[0])
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minute); err != nil {
		return 0, fmt.Errorf("invalid minute: %s", parts[1])
	}

	if hour < 0 || hour > 23 {
		return 0, fmt.Errorf("hour must be between 0 and 23: %d", hour)
	}
	if minute < 0 || minute > 59 {
		return 0, fmt.Errorf("minute must be between 0 and 59: %d", minute)
	}

	return time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute, nil
}

// NextRunTime 计算下次运行时间
func NextRunTime(now time.Time, schedule time.Duration) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	target := today.Add(schedule)

	if target.After(now) {
		return target
	}
	return target.Add(24 * time.Hour)
}

// IsTimeToRun 检查是否应该运行
func IsTimeToRun(now time.Time, schedule time.Duration, tolerance time.Duration) bool {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	target := today.Add(schedule)

	diff := now.Sub(target)
	if diff < 0 {
		diff = -diff
	}

	return diff <= tolerance
}

// ValidateCronExpression 验证 cron 表达式
func ValidateCronExpression(expr string) error {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return fmt.Errorf("cron expression must have 5 fields: %s", expr)
	}

	// 简单验证每个字段
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("cron field cannot be empty")
		}
	}

	return nil
}

// EstimateInterval 估算 cron 表达式的执行间隔
func EstimateInterval(expr string) time.Duration {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return 0
	}

	// 检查分钟字段
	minuteField := parts[0]
	if strings.HasPrefix(minuteField, "*/") {
		var interval int
		fmt.Sscanf(minuteField, "*/%d", &interval)
		return time.Duration(interval) * time.Minute
	}

	// 检查小时字段
	hourField := parts[1]
	if strings.HasPrefix(hourField, "*/") {
		var interval int
		fmt.Sscanf(hourField, "*/%d", &interval)
		return time.Duration(interval) * time.Hour
	}

	return 0
}
