package scheduler

import (
	"testing"
	"time"
)

func TestParseScheduleTime(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		hasError bool
	}{
		{"08:00", 8*time.Hour + 0*time.Minute, false},
		{"20:30", 20*time.Hour + 30*time.Minute, false},
		{"00:00", 0, false},
		{"23:59", 23*time.Hour + 59*time.Minute, false},
		{"invalid", 0, true},
		{"25:00", 0, true},
		{"12:60", 0, true},
	}

	for _, tt := range tests {
		result, err := ParseScheduleTime(tt.input)
		if tt.hasError {
			if err == nil {
				t.Errorf("ParseScheduleTime(%s) should return error", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseScheduleTime(%s) error = %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("ParseScheduleTime(%s) = %v, want %v", tt.input, result, tt.expected)
			}
		}
	}
}

func TestNextRunTime(t *testing.T) {
	now := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		scheduleTime string
		expected     time.Time
	}{
		{"08:00", time.Date(2024, 1, 2, 8, 0, 0, 0, time.UTC)},
		{"12:00", time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)},
		{"20:00", time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		schedule, _ := ParseScheduleTime(tt.scheduleTime)
		result := NextRunTime(now, schedule)
		if !result.Equal(tt.expected) {
			t.Errorf("NextRunTime(%v, %v) = %v, want %v", now, schedule, result, tt.expected)
		}
	}
}

func TestIsTimeToRun(t *testing.T) {
	now := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	schedule, _ := ParseScheduleTime("08:00")
	if !IsTimeToRun(now, schedule, time.Minute*5) {
		t.Error("Should run at exact schedule time")
	}

	now = time.Date(2024, 1, 1, 8, 2, 0, 0, time.UTC)
	if !IsTimeToRun(now, schedule, time.Minute*5) {
		t.Error("Should run within tolerance")
	}

	now = time.Date(2024, 1, 1, 8, 10, 0, 0, time.UTC)
	if IsTimeToRun(now, schedule, time.Minute*5) {
		t.Error("Should not run outside tolerance")
	}
}

func TestTaskType(t *testing.T) {
	task := &Task{
		Name:     "Morning Report",
		Type:     TaskTypeMorningReport,
		Schedule: "08:00",
		Enabled:  true,
	}

	if task.Type != TaskTypeMorningReport {
		t.Errorf("Task.Type = %v, want %v", task.Type, TaskTypeMorningReport)
	}
	if task.Name != "Morning Report" {
		t.Errorf("Task.Name = %v, want Morning Report", task.Name)
	}
}

func TestTaskIsEnabled(t *testing.T) {
	task := &Task{Enabled: true}
	if !task.IsEnabled() {
		t.Error("Task should be enabled")
	}

	task.Enabled = false
	if task.IsEnabled() {
		t.Error("Task should be disabled")
	}
}

func TestCronExpression(t *testing.T) {
	tests := []struct {
		expr      string
		hasError  bool
	}{
		{"0 8 * * *", false},    // 每天 8:00
		{"0 20 * * *", false},   // 每天 20:00
		{"*/5 * * * *", false},  // 每 5 分钟
		{"0 */2 * * *", false},  // 每 2 小时
		{"invalid", true},
		{"* * * *", true},       // 缺少字段
	}

	for _, tt := range tests {
		err := ValidateCronExpression(tt.expr)
		if tt.hasError {
			if err == nil {
				t.Errorf("ValidateCronExpression(%s) should return error", tt.expr)
			}
		} else {
			if err != nil {
				t.Errorf("ValidateCronExpression(%s) error = %v", tt.expr, err)
			}
		}
	}
}

func TestCalculateInterval(t *testing.T) {
	tests := []struct {
		expr     string
		expected time.Duration
	}{
		{"*/5 * * * *", 5 * time.Minute},
		{"*/30 * * * *", 30 * time.Minute},
		{"0 */2 * * *", 2 * time.Hour},
	}

	for _, tt := range tests {
		result := EstimateInterval(tt.expr)
		if result != tt.expected {
			t.Errorf("EstimateInterval(%s) = %v, want %v", tt.expr, result, tt.expected)
		}
	}
}

func TestTaskStatus(t *testing.T) {
	status := &TaskStatus{
		TaskName:    "Test Task",
		LastRun:     time.Now(),
		NextRun:     time.Now().Add(time.Hour),
		RunCount:    10,
		SuccessCount: 9,
		FailureCount: 1,
		IsRunning:    false,
	}

	if status.TaskName != "Test Task" {
		t.Errorf("TaskName = %v", status.TaskName)
	}
	if status.RunCount != 10 {
		t.Errorf("RunCount = %v", status.RunCount)
	}
	if status.SuccessRate() != 90.0 {
		t.Errorf("SuccessRate = %v, want 90.0", status.SuccessRate())
	}
}
