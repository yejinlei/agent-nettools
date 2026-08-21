package web

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxLogSize    = 256 * 1024 // 256KB rotate threshold
	defaultLogPath = "agent-netx.log"
)

// DefaultLogPath returns the shared log file path (~/.agent-netx/agent-netx.log
// by default). Falls back to ./agent-netx.log if home is unknown.
func DefaultLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return defaultLogPath
	}
	return filepath.Join(home, ".agent-netx", defaultLogPath)
}

// rotatingFile is an io.Writer that appends to path and rotates when the file
// grows beyond maxLogSize (rename old -> path+".1", start fresh).
type rotatingFile struct {
	mu     sync.Mutex
	path   string
	f      *os.File
	w      *bufio.Writer
}

// OpenRotatingFile returns an io.Writer that appends to path and rotates when
// the file grows beyond maxLogSize (rename old -> path+".1", start fresh).
func OpenRotatingFile(path string) (io.Writer, error) {
	return openRotatingFile(path)
}

func openRotatingFile(path string) (*rotatingFile, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	r := &rotatingFile{path: path, f: f, w: bufio.NewWriterSize(f, 4096)}
	return r, nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fi, err := r.f.Stat()
	if err == nil && fi.Size() >= maxLogSize {
		r.w.Flush()
		r.f.Close()
		os.Rename(r.path, r.path+".1")
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return 0, err
		}
		r.f = f
		r.w = bufio.NewWriterSize(f, 4096)
	}
	return r.w.Write(p)
}

// ReadLogFile returns the last n lines of path, filtered by level.
// Levels are matched case-insensitively. Empty level = include all.
func ReadLogFile(path string, n int, level string) ([]LogEntry, error) {
	if path == "" {
		path = DefaultLogPath()
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	if level == "" {
		level = "info"
	}
	levelLower := strings.ToLower(level)

	var out []LogEntry
	for _, ln := range lines {
		entry, ok := parseLogLine(ln)
		if !ok {
			continue
		}
		if levelLower != "all" && strings.ToLower(entry.Level) != levelLower {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// parseLogLine parses a line written by writeLogLine.
func parseLogLine(line string) (LogEntry, bool) {
	if len(line) < 23 {
		return LogEntry{}, false
	}
	ts := line[:23]
	rest := line[23:]
	// Find " | " separator
	idx := strings.Index(rest, " | ")
	if idx < 0 {
		return LogEntry{}, false
	}
	lvl := strings.TrimSpace(rest[:idx])
	msg := rest[idx+3:]
	return LogEntry{Time: ts, Level: lvl, Message: msg}, true
}

func writeLogLine(w io.Writer, e LogEntry) {
	fmt.Fprintf(w, "%s | %s | %s\n", e.Time, e.Level, e.Message)
}
