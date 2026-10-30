// Package events 是统一事件模型：进程内日志、SSE 推送、CLI 打印、日志落盘都用它。
package events

import (
	"sync"
	"time"
)

// ProgressBytes 是下载/加载进度的结构化细节。
type ProgressBytes struct {
	Done       uint64 `json:"done"`
	Total      uint64 `json:"total"`
	FilesDone  uint32 `json:"files_done"`
	FilesTotal uint32 `json:"files_total"`
	// File 是当前正在下的文件名（worker 挂在 tqdm 子类上的真文件名）。
	File      *string `json:"file,omitempty"`
	FileDone  uint64  `json:"file_done"`
	FileTotal uint64  `json:"file_total"`
}

// 事件类型。
const (
	TypeLog      = "log"
	TypeProgress = "progress"
	TypeStatus   = "status"
	TypeEnv      = "env"
)

// Event 用一个结构表达四类事件（不相关的字段留空 / nil）。
//
// JSON 形状与前端 `web/src/types.ts` 里的 Event 一一对应：`type` 是判别字段。
type Event struct {
	Type string `json:"type"`
	Ts   int64  `json:"ts"`

	// log
	Source  string  `json:"source,omitempty"`
	Level   string  `json:"level,omitempty"`
	Message *string `json:"message,omitempty"`

	// progress
	Stage   string         `json:"stage,omitempty"`
	Percent *float64       `json:"percent,omitempty"`
	Bytes   *ProgressBytes `json:"bytes,omitempty"`
	// Job 是进度/日志归属的识别任务（没有就不带，前端按 model 归并）。
	Job string `json:"job,omitempty"`

	// status
	Model string `json:"model,omitempty"`
	State string `json:"state,omitempty"`

	// job 事件（type=job）挂的任务快照；Data 是 any 是为了不让 events 依赖 core。
	Data any `json:"data,omitempty"`
}

func nowMS() int64 { return time.Now().UnixMilli() }

func strPtr(value string) *string { return &value }

// Sink 收每一条事件的落地副本（日志文件）；Emit 在锁外同步调用它。
type Sink interface {
	Write(Event)
}

// EventBus 是环形缓冲 + 广播。
type EventBus struct {
	mu       sync.Mutex
	ring     []Event
	capacity int
	subs     map[chan Event]struct{}
	sink     Sink
	closed   bool
}

// New 建一个最多保留 capacity 条历史的广播总线。
func New(capacity int) *EventBus {
	if capacity <= 0 {
		capacity = 500
	}
	return &EventBus{ring: make([]Event, 0, capacity), capacity: capacity, subs: map[chan Event]struct{}{}}
}

// Default 是标准容量（500 条）。
func Default() *EventBus { return New(500) }

// SetSink 让每条事件再多一份落地副本（`logs/yyyy-mm-dd.log`）。传 nil 关闭落地。
func (b *EventBus) SetSink(sink Sink) {
	b.mu.Lock()
	b.sink = sink
	b.mu.Unlock()
}

// Subscribe 订阅新事件，返回取消函数（必须调用，否则泄漏）。
func (b *EventBus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
	return ch, cancel
}

// Close 关掉所有订阅（进程退出 / 测试收尾）。
func (b *EventBus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// Recent 返回最近 n 条历史。
func (b *EventBus) Recent(n int) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.ring) {
		n = len(b.ring)
	}
	out := make([]Event, n)
	copy(out, b.ring[len(b.ring)-n:])
	return out
}

// Emit 记录并广播一条事件。
func (b *EventBus) Emit(ev Event) {
	b.mu.Lock()
	// 下载进度每秒好几条：环形缓冲只保留每个 (source, job, stage) 的最新一条，
	// 否则真日志会被进度挤掉。订阅者不受影响，仍能收到每一条。
	if ev.Type == TypeProgress {
		for idx := len(b.ring) - 1; idx >= 0; idx-- {
			if b.ring[idx].Type == TypeProgress && b.ring[idx].Source == ev.Source &&
				b.ring[idx].Stage == ev.Stage && b.ring[idx].Job == ev.Job {
				b.ring = append(b.ring[:idx], b.ring[idx+1:]...)
				break
			}
		}
	}
	if len(b.ring) == b.capacity {
		b.ring = b.ring[1:]
	}
	b.ring = append(b.ring, ev)
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// 订阅者跟不上就丢这一条，不能阻塞生产者
		}
	}
	sink := b.sink
	b.mu.Unlock()

	// 落盘在锁外做：写文件比广播慢，不能让所有生产者排队等磁盘
	if sink != nil {
		sink.Write(ev)
	}
}

// Log 记一条日志。
func (b *EventBus) Log(source, level, message string) {
	b.Emit(Event{Type: TypeLog, Ts: nowMS(), Source: source, Level: level, Message: strPtr(message)})
}

// Info 是 info 级日志。
func (b *EventBus) Info(source, message string) { b.Log(source, "info", message) }

// Warn 是 warn 级日志。
func (b *EventBus) Warn(source, message string) { b.Log(source, "warn", message) }

// Error 是 error 级日志。
func (b *EventBus) Error(source, message string) { b.Log(source, "error", message) }

// Progress 记一条不带字节数的进度。
func (b *EventBus) Progress(source, stage string, percent *float64, message string) {
	b.ProgressBytes(source, stage, percent, message, nil)
}

// ProgressBytes 记一条带字节数的进度。
func (b *EventBus) ProgressBytes(source, stage string, percent *float64, message string, bytes *ProgressBytes) {
	b.Emit(Event{
		Type:    TypeProgress,
		Ts:      nowMS(),
		Source:  source,
		Stage:   stage,
		Percent: percent,
		Message: strPtr(message),
		Bytes:   bytes,
	})
}

// Status 记一次模型状态变化（idle | preparing | loading | ready | running | error）。
func (b *EventBus) Status(model, state string, message *string) {
	b.Emit(Event{Type: TypeStatus, Ts: nowMS(), Model: model, State: state, Message: message})
}

// Env 记一次 python 环境状态变化（missing | installing | waiting | ready | error）。
func (b *EventBus) Env(state, message string) {
	b.Emit(Event{Type: TypeEnv, Ts: nowMS(), State: state, Message: strPtr(message)})
}
