package logger

import (
	"log"
	"os"
	"strings"
)

// LogLevel 定义了日志的级别
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var currentLevel = INFO // 默认日志级别

// Init 从环境变量 LOG_LEVEL 初始化日志级别。
// 应在应用程序启动时调用。
func Init() {
	levelStr := strings.ToUpper(os.Getenv("LOG_LEVEL"))
	switch levelStr {
	case "DEBUG":
		currentLevel = DEBUG
	case "INFO":
		currentLevel = INFO
	case "WARN":
		currentLevel = WARN
	case "ERROR":
		currentLevel = ERROR
	default:
		currentLevel = INFO // 默认或无效值
	}
	log.Printf("Logger initialized with level: %s", levelStr)
}

func logf(level LogLevel, format string, v ...any) {
	if level >= currentLevel {
		log.Printf(format, v...)
	}
}

// Debugf 记录 DEBUG 级别的日志
func Debugf(format string, v ...any) {
	logf(DEBUG, "[DEBUG] "+format, v...)
}

// Infof 记录 INFO 级别的日志
func Infof(format string, v ...any) {
	logf(INFO, "[INFO] "+format, v...)
}

// Warnf 记录 WARN 级别的日志
func Warnf(format string, v ...any) {
	logf(WARN, "[WARN] "+format, v...)
}

// Errorf 记录 ERROR 级别的日志
func Errorf(format string, v ...any) {
	logf(ERROR, "[ERROR] "+format, v...)
}

// Fatalf 记录 ERROR 级别的日志，然后退出程序
func Fatalf(format string, v ...any) {
	logf(ERROR, "[FATAL] "+format, v...)
	os.Exit(1)
}
