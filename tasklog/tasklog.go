package tasklog

import (
	"fmt"
	"sync"
	"time"
)

// TaskLog is a simple in-memory ring buffer for task progress messages.
// Long-running operations (recalc, scrape, validate) write here.
// The admin panel polls GET /api/tasklog to display progress.
var store = &taskLogStore{
	entries: make([]Entry, 0, 200),
}

type Entry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
	Type    string `json:"type"` // "info", "success", "error"
}

type taskLogStore struct {
	mu      sync.RWMutex
	entries []Entry
}

// Add appends a log entry (keeps last 200).
func Add(msg string, logType string) {
	store.mu.Lock()
	defer store.mu.Unlock()

	entry := Entry{
		Time:    time.Now().Format("15:04:05"),
		Message: msg,
		Type:    logType,
	}
	store.entries = append(store.entries, entry)
	if len(store.entries) > 200 {
		store.entries = store.entries[len(store.entries)-200:]
	}
}

func Info(msg string)                        { Add(msg, "info") }
func Success(msg string)                     { Add(msg, "success") }
func Error(msg string)                       { Add(msg, "error") }
func Infof(format string, args ...any)       { Add(fmt.Sprintf(format, args...), "info") }
func Successf(format string, args ...any)    { Add(fmt.Sprintf(format, args...), "success") }
func Errorf(format string, args ...any)      { Add(fmt.Sprintf(format, args...), "error") }

// GetSince returns entries after the given index. Pass 0 for all.
func GetSince(since int) ([]Entry, int) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	if since >= len(store.entries) {
		return nil, len(store.entries)
	}
	if since < 0 {
		since = 0
	}
	result := make([]Entry, len(store.entries)-since)
	copy(result, store.entries[since:])
	return result, len(store.entries)
}

// Clear empties the log.
func Clear() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.entries = store.entries[:0]
}
