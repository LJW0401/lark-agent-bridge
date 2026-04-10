package config

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// Logger 提供带时间戳的日志，同时输出到终端和文件
type Logger struct {
	logger *log.Logger
	file   *os.File
}

// NewLogger 创建日志实例，输出到 stdout + 日志文件
func NewLogger(logPath string) (*Logger, error) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}

	w := io.MultiWriter(os.Stdout, f)
	l := log.New(w, "", 0)

	return &Logger{logger: l, file: f}, nil
}

// Log 输出带时间戳的日志
func (l *Logger) Log(format string, args ...any) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	l.logger.Printf("[%s] %s", ts, msg)
}

// Close 关闭日志文件
func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}
