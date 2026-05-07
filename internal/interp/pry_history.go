package interp

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/file"
)

// DefaultCap is the maximum number of lines kept in memory and on disk.
const DefaultCap = 1000

// History is an append-only ring of REPL input lines, persisted to disk.
type History struct {
	mu    sync.Mutex
	path  string
	cap   int
	lines []string
}

// DefaultPath returns $XDG_STATE_HOME/magus/pry_history, falling back to ~/.local/state.
func DefaultPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "magus", "pry_history")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "magus", "pry_history")
	}
	// Last resort: cwd-relative.
	return ".magus_pry_history"
}

// Open loads up to maxCap lines from path (0 = DefaultCap); a missing file is not an error.
func Open(path string, maxCap int) (*History, error) {
	if maxCap <= 0 {
		maxCap = DefaultCap
	}
	h := &History{path: path, cap: maxCap}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return h, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		h.lines = append(h.lines, line)
	}
	if len(h.lines) > h.cap {
		h.lines = h.lines[len(h.lines)-h.cap:]
	}
	return h, scanner.Err()
}

// Append records line (skips empty and consecutive duplicates) and writes through to disk.
func (h *History) Append(line string) {
	line = strings.TrimRight(line, "\n")
	if line == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.lines); n > 0 && h.lines[n-1] == line {
		return
	}
	h.lines = append(h.lines, line)
	if len(h.lines) > h.cap {
		h.lines = h.lines[len(h.lines)-h.cap:]
		h.rewriteLocked()
		return
	}
	h.appendLocked(line)
}

// Lines returns a copy of the in-memory ring (oldest first).
func (h *History) Lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.lines))
	copy(out, h.lines)
	return out
}

// Recall returns the n-th most-recent line (1-based; n=1 is the latest).
// Returns "" if n is out of range.
func (h *History) Recall(n int) string {
	if n <= 0 {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n > len(h.lines) {
		return ""
	}
	return h.lines[len(h.lines)-n]
}

// appendLocked appends one line. Caller holds h.mu.
func (h *History) appendLocked(line string) {
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// rewriteLocked truncates and rewrites the history file. Caller holds h.mu.
func (h *History) rewriteLocked() {
	var buf bytes.Buffer
	for _, l := range h.lines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	_ = file.WriteFileAtomic(h.path, buf.Bytes(), 0o600)
}
