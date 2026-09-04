package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Level 日志级别
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
}

var levelColors = map[Level]string{
	DEBUG: "\033[36m", // 青色
	INFO:  "\033[32m", // 绿色
	WARN:  "\033[33m", // 黄色
	ERROR: "\033[31m", // 红色
}

const resetColor = "\033[0m"

// Logger 日志记录器
type Logger struct {
	mu          sync.Mutex
	level       Level
	output      io.Writer
	showCaller  bool
	colorize    bool
	maxLineLen  int // 单行最大长度（0 表示不限制）
	prefix      string
}

var defaultLogger *Logger

func init() {
	defaultLogger = NewLogger(INFO, os.Stdout)
}

// NewLogger 创建日志记录器
func NewLogger(level Level, output io.Writer) *Logger {
	return &Logger{
		level:      level,
		output:     output,
		colorize:   true,
		maxLineLen: 500, // 默认限制 500 字符
	}
}

// SetLevel 设置日志级别
func SetLevel(level Level) {
	defaultLogger.SetLevel(level)
}

// SetLevelFromString 从字符串设置日志级别
func SetLevelFromString(levelStr string) {
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		SetLevel(DEBUG)
	case "INFO":
		SetLevel(INFO)
	case "WARN":
		SetLevel(WARN)
	case "ERROR":
		SetLevel(ERROR)
	default:
		SetLevel(INFO)
	}
}

// SetColorize 设置是否着色
func SetColorize(enabled bool) {
	defaultLogger.colorize = enabled
}

// SetMaxLineLen 设置单行最大长度
func SetMaxLineLen(len int) {
	defaultLogger.maxLineLen = len
}

// SetPrefix 设置前缀
func SetPrefix(prefix string) {
	defaultLogger.prefix = prefix
}

// SetLevel 设置日志级别
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// log 记录日志
func (l *Logger) log(level Level, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().Format("2006-01-02 15:04:05")
	levelName := levelNames[level]

	message := fmt.Sprintf(format, args...)

	// 截断过长的消息
	if l.maxLineLen > 0 && len(message) > l.maxLineLen {
		message = message[:l.maxLineLen] + "..."
	}

	// 构建日志行
	var logLine string
	prefix := l.prefix
	if prefix != "" {
		prefix = "[" + prefix + "] "
	}

	if l.colorize {
		color := levelColors[level]
		logLine = fmt.Sprintf("%s %s%s%-5s%s %s%s\n",
			now, prefix, color, levelName, resetColor, message, resetColor)
	} else {
		logLine = fmt.Sprintf("%s %s%-5s %s\n", now, prefix, levelName, message)
	}

	l.output.Write([]byte(logLine))
}

// Debug 调试日志
func Debug(format string, args ...interface{}) {
	defaultLogger.log(DEBUG, format, args...)
}

// Info 信息日志
func Info(format string, args ...interface{}) {
	defaultLogger.log(INFO, format, args...)
}

// Warn 警告日志
func Warn(format string, args ...interface{}) {
	defaultLogger.log(WARN, format, args...)
}

// Error 错误日志
func Error(format string, args ...interface{}) {
	defaultLogger.log(ERROR, format, args...)
}

// Fatal 致命错误日志（退出程序）
func Fatal(format string, args ...interface{}) {
	defaultLogger.log(ERROR, format, args...)
	os.Exit(1)
}

// WithPrefix 创建带前缀的子日志
func WithPrefix(prefix string) *PrefixedLogger {
	return &PrefixedLogger{
		prefix: prefix,
	}
}

// PrefixedLogger 带前缀的日志记录器
type PrefixedLogger struct {
	prefix string
}

func (p *PrefixedLogger) Debug(format string, args ...interface{}) {
	Debug("[%s] "+format, append([]interface{}{p.prefix}, args...)...)
}

func (p *PrefixedLogger) Info(format string, args ...interface{}) {
	Info("[%s] "+format, append([]interface{}{p.prefix}, args...)...)
}

func (p *PrefixedLogger) Warn(format string, args ...interface{}) {
	Warn("[%s] "+format, append([]interface{}{p.prefix}, args...)...)
}

func (p *PrefixedLogger) Error(format string, args ...interface{}) {
	Error("[%s] "+format, append([]interface{}{p.prefix}, args...)...)
}

// 兼容标准库 log
var stdLog = log.New(os.Stdout, "", log.LstdFlags)

// Printf 兼容标准库 log.Printf
func Printf(format string, args ...interface{}) {
	Info(format, args...)
}

// Println 兼容标准库 log.Println
func Println(args ...interface{}) {
	Info("%s", fmt.Sprint(args...))
}

// Fatalf 兼容标准库 log.Fatalf
func Fatalf(format string, args ...interface{}) {
	Fatal(format, args...)
}
