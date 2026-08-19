package web

import (
	"fmt"
	"sync"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type LogRing struct {
	mu        sync.RWMutex
	entries   []LogEntry
	maxSize   int
	nextID    int
	listeners []chan LogEntry
}

func NewLogRing(maxSize int) *LogRing {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &LogRing{
		entries:   make([]LogEntry, 0, maxSize),
		maxSize:   maxSize,
		listeners: make([]chan LogEntry, 0),
	}
}

func (r *LogRing) Write(level LogLevel, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	entry := LogEntry{
		Time:    time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Level:   level.String(),
		Message: msg,
	}
	r.mu.Lock()
	if len(r.entries) >= r.maxSize {
		r.entries = r.entries[1:]
	}
	r.entries = append(r.entries, entry)
	listeners := make([]chan LogEntry, len(r.listeners))
	copy(listeners, r.listeners)
	r.mu.Unlock()

	for _, ch := range listeners {
		select {
		case ch <- entry:
		default:
		}
	}
}

func (r *LogRing) Subscribe() (chan LogEntry, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan LogEntry, 64)
	r.listeners = append(r.listeners, ch)
	idx := len(r.listeners) - 1
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.listeners = append(r.listeners[:idx], r.listeners[idx+1:]...)
	}
}

func (r *LogRing) Recent(n int) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n <= 0 || n > len(r.entries) {
		n = len(r.entries)
	}
	result := make([]LogEntry, n)
	copy(result, r.entries[len(r.entries)-n:])
	return result
}