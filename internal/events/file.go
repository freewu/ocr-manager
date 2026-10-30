package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileSink 把事件追加到 `logs/yyyy-mm-dd.log`（一行一条 JSON，方便 grep / jq）。
//
// 进程可能活好几天，日期换了要跟着换文件：每次写之前看当前日期。
type FileSink struct {
	dir string

	mu       sync.Mutex
	day      string
	file     *os.File
	writeErr error
}

// NewFileSink 建一个落地到 dir 的 sink（目录不存在会被创建）。
func NewFileSink(dir string) (*FileSink, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %s: %w", dir, err)
	}
	return &FileSink{dir: dir}, nil
}

// Write 实现 Sink。
func (s *FileSink) Write(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	day := time.Now().Format("2006-01-02")
	if s.file == nil || s.day != day {
		s.rotate(day)
	}
	if s.file == nil {
		return
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if _, err := s.file.Write(append(raw, '\n')); err != nil {
		s.writeErr = err
	}
}

func (s *FileSink) rotate(day string) {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	file, err := os.OpenFile(filepath.Join(s.dir, day+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		s.writeErr = err
		return
	}
	s.file, s.day, s.writeErr = file, day, nil
}

// Close 关掉当前文件（进程退出时调用）。
func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return s.writeErr
	}
	err := s.file.Close()
	s.file = nil
	return err
}
