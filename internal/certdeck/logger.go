package certdeck

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxLogLines = 2000

// DeckLogger writes timestamped lines to disk and broadcasts to WebSocket subscribers.
type DeckLogger struct {
	mu          sync.Mutex
	path        string
	lines       []string
	subscribers map[chan string]struct{}
}

func NewDeckLogger() *DeckLogger {
	p := filepath.Join(DataDir(), "unificert.log")
	return &DeckLogger{
		path:        p,
		lines:       nil,
		subscribers: make(map[chan string]struct{}),
	}
}

func (l *DeckLogger) Subscribe(buf int) chan string {
	ch := make(chan string, buf)
	l.mu.Lock()
	l.subscribers[ch] = struct{}{}
	// replay recent
	for _, line := range l.tailLocked(200) {
		select {
		case ch <- line:
		default:
		}
	}
	l.mu.Unlock()
	return ch
}

func (l *DeckLogger) Unsubscribe(ch chan string) {
	l.mu.Lock()
	delete(l.subscribers, ch)
	l.mu.Unlock()
}

func (l *DeckLogger) Info(format string, args ...any) {
	l.log("INFO", format, args...)
}

func (l *DeckLogger) Warn(format string, args ...any) {
	l.log("WARN", format, args...)
}

func (l *DeckLogger) log(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] %s %s", time.Now().Format("15:04:05"), level, msg)
	l.append(line)
}

func (l *DeckLogger) append(line string) {
	l.mu.Lock()
	l.lines = append(l.lines, line)
	if len(l.lines) > maxLogLines {
		l.lines = l.lines[len(l.lines)-maxLogLines:]
	}
	path := l.path
	subs := make([]chan string, 0, len(l.subscribers))
	for ch := range l.subscribers {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		_, _ = fmt.Fprintln(f, line)
		_ = f.Close()
	}

	for _, ch := range subs {
		select {
		case ch <- line:
		default:
		}
	}
}

func (l *DeckLogger) tailLocked(n int) []string {
	if n <= 0 || len(l.lines) == 0 {
		return nil
	}
	if n >= len(l.lines) {
		out := make([]string, len(l.lines))
		copy(out, l.lines)
		return out
	}
	return append([]string(nil), l.lines[len(l.lines)-n:]...)
}

// Tail returns up to n most recent lines from memory (and falls back to file if empty).
func (l *DeckLogger) Tail(n int) []string {
	l.mu.Lock()
	mem := l.tailLocked(n)
	path := l.path
	l.mu.Unlock()
	if len(mem) > 0 {
		return mem
	}
	return tailFile(path, n)
}

func tailFile(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
	}
	return lines
}

func formatLogLinesForWS(lines []string) string {
	return strings.Join(lines, "\n")
}
