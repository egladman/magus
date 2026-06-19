package cache

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/interactive"
	"golang.org/x/term"
)

// levelTrace mirrors config.LevelTrace (slog.LevelDebug-4); duplicated here
// because config imports cache, so cache cannot import config back.
const levelTrace slog.Level = slog.LevelDebug - 4

// newLogger returns a *slog.Logger for the given format ("text", "json", or "pretty") and level.
func newLogger(format string, level slog.Level) *slog.Logger {
	switch strings.ToLower(format) {
	case "text":
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	default:
		return slog.New(newPrettyHandler(os.Stdout, level))
	}
}

// prettyHandler renders cache events: coloured glyphs on TTY, [cache]-prefixed on plain streams.
type prettyHandler struct {
	mu    sync.Mutex
	w     io.Writer // write destination (os.Stdout in production; bytes.Buffer in tests)
	fd    *os.File  // nil means non-TTY; used only for IsTerminal checks
	level slog.Level
}

// newPrettyHandler creates a prettyHandler. TTY detection runs per-Handle so late redirects are noticed.
func newPrettyHandler(w *os.File, level slog.Level) *prettyHandler {
	return &prettyHandler{w: w, fd: w, level: level}
}

func (h *prettyHandler) isTTY() bool {
	if h.fd == nil {
		return false
	}
	return term.IsTerminal(int(h.fd.Fd())) && os.Getenv("NO_COLOR") == ""
}

func (h *prettyHandler) Enabled(_ context.Context, lvl slog.Level) bool { return lvl >= h.level }
func (h *prettyHandler) WithAttrs(_ []slog.Attr) slog.Handler           { return h }
func (h *prettyHandler) WithGroup(_ string) slog.Handler                { return h }

func (h *prettyHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx.Err() != nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	tty := h.isTTY()
	project := recordStr(r, "project")
	dur := recordDur(r, "duration")

	switch r.Message {
	case "cache.hit":
		if tty {
			_, _ = fmt.Fprintf(h.w, "\x1b[32m✓\x1b[0m %s (hit, %s)\n", project, fmtDur(dur))
		} else {
			_, _ = fmt.Fprintf(h.w, "[cache] hit  %s (%s)\n", project, fmtDur(dur))
		}
		h.printRepro(tty, project, recordStr(r, "target"))
	case "cache.miss":
		if tty {
			_, _ = fmt.Fprintf(h.w, "\x1b[33m↻\x1b[0m %s (miss, %s)\n", project, fmtDur(dur))
		} else {
			_, _ = fmt.Fprintf(h.w, "[cache] miss %s (%s)\n", project, fmtDur(dur))
		}
		h.printRepro(tty, project, recordStr(r, "target"))
	case "cache.error":
		errStr := recordStr(r, "error")
		if tty {
			_, _ = fmt.Fprintf(h.w, "\x1b[31m✗\x1b[0m %s (error, %s): %s\n", project, fmtDur(dur), errStr)
		} else {
			_, _ = fmt.Fprintf(h.w, "[cache] error %s (%s): %s\n", project, fmtDur(dur), errStr)
		}
		h.printRepro(tty, project, recordStr(r, "target"))
	case "cache.warn":
		_, _ = fmt.Fprintf(h.w, "[cache] warn  %s\n", recordStr(r, "msg"))
	case "cache.summary":
		hits := recordInt(r, "hits")
		misses := recordInt(r, "misses")
		errors := recordInt(r, "errors")
		elapsed := recordDur(r, "elapsed")
		if tty {
			_, _ = fmt.Fprintf(h.w, "\nSummary: %d hit, %d miss, %d error (%s)\n",
				hits, misses, errors, fmtDur(elapsed))
		} else {
			_, _ = fmt.Fprintf(h.w, "[summary] %d hit, %d miss, %d error (%s)\n",
				hits, misses, errors, fmtDur(elapsed))
		}
	case "cache.scope":
		label := recordStr(r, "label")
		source := recordStr(r, "source")
		if source != "" {
			_, _ = fmt.Fprintf(h.w, "[scope] %s (%s)\n", label, source)
		} else {
			_, _ = fmt.Fprintf(h.w, "[scope] %s\n", label)
		}
	}
	return nil
}

// printRepro prints the standalone `magus run <target> <project>` for the project
// just reported, so a reader can copy/paste it to run that one project on its own
// — handy after a fan-out (`magus run`/`magus affected`) to isolate a single
// project. Indented and dimmed on a TTY; gated on the hints toggle so it can be
// silenced. A blank target (the non-result events) prints nothing.
func (h *prettyHandler) printRepro(tty bool, project, target string) {
	if !interactive.Enabled() || project == "" || target == "" {
		return
	}
	if tty {
		_, _ = fmt.Fprintf(h.w, "  \x1b[2mmagus run %s %s\x1b[0m\n", target, project)
	} else {
		_, _ = fmt.Fprintf(h.w, "  magus run %s %s\n", target, project)
	}
}

func recordStr(r slog.Record, key string) string {
	var v string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.String()
			return false
		}
		return true
	})
	return v
}

func recordDur(r slog.Record, key string) time.Duration {
	var d time.Duration
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if a.Value.Kind() == slog.KindInt64 {
				d = time.Duration(a.Value.Int64())
			}
			return false
		}
		return true
	})
	return d
}

func fmtDur(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fµs", float64(d.Nanoseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Second*10:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

func recordInt(r slog.Record, key string) int {
	var i int
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if a.Value.Kind() == slog.KindInt64 {
				i = int(a.Value.Int64())
			}
			return false
		}
		return true
	})
	return i
}
