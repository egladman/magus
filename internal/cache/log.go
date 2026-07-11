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

	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/interactive"
	"golang.org/x/term"
)

// levelTrace mirrors config.LevelTrace (slog.LevelDebug-4); duplicated here
// because config imports cache, so cache cannot import config back.
const levelTrace slog.Level = slog.LevelDebug - 4

// newLogger returns a *slog.Logger for the given format ("text", "json", or "pretty") and level.
//
// Human formats (pretty, plain) render to stderr so stdout stays clean for machine
// output; json/text keep their slog handlers. Pretty uses the shared prettyHandler,
// which is also installed as the process-wide default logger (see cmd/magus) so that
// general diagnostics render in the same compact style as cache events instead of raw
// "time=... level=..." lines interleaving with the pretty output.
func newLogger(format string, level slog.Level) *slog.Logger {
	switch strings.ToLower(format) {
	case "text":
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	default:
		return slog.New(NewPrettyHandler(os.Stderr, level))
	}
}

// prettyHandler renders both cache events (known cache.* messages) and general
// diagnostics in a compact, scannable style: coloured ASCII status glyphs on a TTY,
// bracketed prefixes on plain streams. It carries no timestamps or level=/key=
// boilerplate; that noise is what makes raw slog output hard to read interactively.
type prettyHandler struct {
	mu    sync.Mutex
	w     io.Writer // write destination (os.Stderr in production; bytes.Buffer in tests)
	fd    *os.File  // nil means non-TTY; used only for IsTerminal checks
	level slog.Level
}

// NewPrettyHandler builds the unified pretty handler. If w is an *os.File its
// terminal-ness drives colour; any other writer (e.g. a bytes.Buffer in tests, or a
// pipe) renders plain. TTY detection runs per-Handle so late redirects are noticed.
func NewPrettyHandler(w io.Writer, level slog.Level) slog.Handler {
	h := &prettyHandler{w: w, level: level}
	if f, ok := w.(*os.File); ok {
		h.fd = f
	}
	return h
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
	project := recordStr(r, "project") // real path; used for the runnable repro command
	label := recordStr(r, "label")     // display name; never "" or "." (see types.ProjectLabel)
	if label == "" {
		label = project
	}
	dur := recordDur(r, "duration")
	ref := recordStr(r, "ref") // target-output reference id (see internal/cache/output_store.go)

	switch r.Message {
	case "cache.hit":
		// Cached: passed without running. Dimmed green so a cache hit reads as
		// low-signal next to work that actually ran. Cache state lives in the parens,
		// mirroring the cross-tool convention (e.g. Bazel's "(cached) PASSED").
		_, _ = fmt.Fprintf(h.w, "%s %s (cached, %s)\n", h.glyph(tty, "pass", colDimGreen), label, fmtDur(dur))
		h.printRepro(tty, project, recordStr(r, "target"))
		h.printRef(tty, ref, false)
	case "cache.miss":
		_, _ = fmt.Fprintf(h.w, "%s %s (ran, %s)\n", h.glyph(tty, "pass", colGreen), label, fmtDur(dur))
		h.printRepro(tty, project, recordStr(r, "target"))
		h.printRef(tty, ref, false)
	case "cache.error":
		errStr := recordStr(r, "error")
		_, _ = fmt.Fprintf(h.w, "%s %s (ran, %s): %s\n", h.glyph(tty, "fail", colRed), label, fmtDur(dur), errStr)
		h.printRepro(tty, project, recordStr(r, "target"))
		h.printRef(tty, ref, true)
	case "cache.warn":
		_, _ = fmt.Fprintf(h.w, "%s %s\n", h.glyph(tty, "warn", colYellow), recordStr(r, "msg"))
	case "cache.summary":
		cached := recordInt(r, "hits")
		ran := recordInt(r, "misses")
		failed := recordInt(r, "errors")
		elapsed := recordDur(r, "elapsed")
		if tty {
			_, _ = fmt.Fprintf(h.w, "\nSummary: %d cached, %d ran, %d failed (%s)\n",
				cached, ran, failed, fmtDur(elapsed))
		} else {
			_, _ = fmt.Fprintf(h.w, "[summary] %d cached, %d ran, %d failed (%s)\n",
				cached, ran, failed, fmtDur(elapsed))
		}
	case "cache.dry.banner":
		if tty {
			_, _ = fmt.Fprintf(h.w, "\x1b[2mdry run - commands shown, not executed\x1b[0m\n")
		} else {
			_, _ = fmt.Fprintf(h.w, "dry run - commands shown, not executed\n")
		}
	case "cache.dry":
		// Neutral glyph: a dry run has no pass/fail outcome (nothing executes).
		_, _ = fmt.Fprintf(h.w, "%s %s %s\n", h.glyph(tty, "dry", colDim), label, recordStr(r, "target"))
	case "cache.scope":
		label := recordStr(r, "label")
		source := recordStr(r, "source")
		if source != "" {
			_, _ = fmt.Fprintf(h.w, "projects: %s (%s)\n", label, source)
		} else {
			_, _ = fmt.Fprintf(h.w, "projects: %s\n", label)
		}
	case "cache.charms":
		if charms := recordStr(r, "charms"); charms != "" {
			_, _ = fmt.Fprintf(h.w, "charms: %s\n", charms)
		} else {
			_, _ = fmt.Fprintf(h.w, "charms: (none)\n")
		}
	case "run.exec":
		// Every subprocess magus spawns (os.exec, fork spells) logs through this event
		// in run.Exec. Rendered as a shell-style echo, indented under the owning
		// project/stage. At debug level it surfaces with -v during a real run; in a dry
		// run run.Exec logs it at info so the planned commands always show.
		cmd := recordStr(r, "cmd")
		if args := recordStrs(r, "args"); len(args) > 0 {
			cmd += " " + strings.Join(args, " ")
		}
		if tty {
			_, _ = fmt.Fprintf(h.w, "  \x1b[2m$ %s\x1b[0m\n", cmd)
		} else {
			_, _ = fmt.Fprintf(h.w, "  $ %s\n", cmd)
		}
	case "cache.stage":
		// One indented line per magus.needs sub-target as it completes, so a collapsed
		// project still shows what ran. Project-qualified because stages from concurrently
		// running projects interleave. A stage always ran, so it is pass/fail only.
		target := recordStr(r, "target")
		name, color := "pass", colGreen
		if recordStr(r, "error") != "" {
			name, color = "fail", colRed
		}
		_, _ = fmt.Fprintf(h.w, "  %s %s %s (%s)\n", h.glyph(tty, name, color), label, target, fmtDur(dur))
	default:
		h.handleGeneric(tty, r)
	}
	return nil
}

// ANSI colour codes used by the status glyphs. Cache state is conveyed by colour as
// well as by the parenthetical: a cached pass is dim, a fresh run is bright green.
const (
	colDimGreen = "2;32" // cached (passed without running) — low signal
	colGreen    = "32"   // ran and passed
	colRed      = "31"   // failed
	colYellow   = "33"   // warning
	colDim      = "2"    // info/debug
)

// glyph renders a bracketed status glyph like "[pass]" or "[fail]", ASCII only (no
// Unicode symbols or emoji), coloured only on a TTY. pass/fail are the per-target
// outcome words; cache state (cached vs ran) is shown separately in the line's
// parenthetical, the orthogonal split every major build tool uses (e.g. Bazel's
// "(cached) PASSED"). Named to match the doctor command's statusGlyph.
func (h *prettyHandler) glyph(tty bool, label, color string) string {
	s := "[" + label + "]"
	if tty {
		return "\x1b[" + color + "m" + s + "\x1b[0m"
	}
	return s
}

// handleGeneric renders any non-cache slog record (the 76-odd general diagnostics
// across the codebase) in the same compact style: a level glyph, the message, and any
// attrs trailing dimmed. No timestamp or level= boilerplate. The "dir" attr that the
// process-wide handler stamps on every context-aware record is suppressed above debug
// level, since it is a correlation aid, not something a reader needs on each line.
func (h *prettyHandler) handleGeneric(tty bool, r slog.Record) {
	label, color := "debug", colDim
	switch {
	case r.Level >= slog.LevelError:
		label, color = "error", colRed
	case r.Level >= slog.LevelWarn:
		label, color = "warn", colYellow
	case r.Level >= slog.LevelInfo:
		label, color = "info", colDim
	}
	attrs := formatAttrs(r)
	if tty && attrs != "" {
		attrs = "\x1b[2m" + attrs + "\x1b[0m"
	}
	_, _ = fmt.Fprintf(h.w, "%s %s%s\n", h.glyph(tty, label, color), r.Message, attrs)
}

// formatAttrs renders a record's attrs as " key=value" pairs, skipping the noisy
// "dir" correlation attr unless the record is at debug level or below.
func formatAttrs(r slog.Record) string {
	var b strings.Builder
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "dir" && r.Level > slog.LevelDebug {
			return true
		}
		_, _ = fmt.Fprintf(&b, " %s=%s", a.Key, a.Value.String())
		return true
	})
	return b.String()
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
	repro := clihint.Run.With(target, project)
	if tty {
		_, _ = fmt.Fprintf(h.w, "  \x1b[2m%s\x1b[0m\n", repro)
	} else {
		_, _ = fmt.Fprintf(h.w, "  %s\n", repro)
	}
}

// printRef prints a target's output reference id on its OWN bare, unindented line so
// a triple-click selects exactly the ref - no "ref:" label (the "ref" prefix is
// self-evident). Dimmed on a TTY to read as low-signal chrome; the escapes are
// non-printing, so the copied text is still just the ref. On failure it adds the
// retrieval + open hints - the primary nudge toward a failing target's full output.
func (h *prettyHandler) printRef(tty bool, ref string, failed bool) {
	if ref == "" {
		return
	}
	if tty {
		_, _ = fmt.Fprintf(h.w, "\x1b[2m%s\x1b[0m\n", ref)
	} else {
		_, _ = fmt.Fprintf(h.w, "%s\n", ref)
	}
	if !failed {
		return
	}
	full := clihint.QueryOutput.With(ref)
	open := clihint.QueryOutput.With(ref, "--open")
	if tty {
		_, _ = fmt.Fprintf(h.w, "  \x1b[2mfull output: %s\x1b[0m\n", full)
		_, _ = fmt.Fprintf(h.w, "  \x1b[2mopen in browser: %s\x1b[0m\n", open)
	} else {
		_, _ = fmt.Fprintf(h.w, "  full output: %s\n", full)
		_, _ = fmt.Fprintf(h.w, "  open in browser: %s\n", open)
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

// recordStrs extracts a []string attr (e.g. a command's args) from a record.
func recordStrs(r slog.Record, key string) []string {
	var out []string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if s, ok := a.Value.Any().([]string); ok {
				out = s
			}
			return false
		}
		return true
	})
	return out
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
