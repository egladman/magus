package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/std"
)

// dirHandler stamps each record with a "dir" attribute — the magusfile working
// directory established by a target, pulled from the context.
// Because execution no longer changes the process working directory (targets carry
// their cwd on the context so they can run concurrently), a bare log line can no
// longer be traced to the project that emitted it; this restores that correlation
// for every slog.*Context call. An explicit "dir" attribute on the record wins, so
// callers that already know a more specific directory (e.g. a subprocess cwd) are
// left untouched.
type dirHandler struct{ slog.Handler }

func (h dirHandler) Handle(ctx context.Context, r slog.Record) error {
	dir, ok := std.CwdFromContext(ctx)
	if !ok {
		return h.Handler.Handle(ctx, r)
	}
	hasDir := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "dir" {
			hasDir = true
			return false
		}
		return true
	})
	if !hasDir {
		r = r.Clone()
		r.AddAttrs(slog.String("dir", dir))
	}
	return h.Handler.Handle(ctx, r)
}

func (h dirHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return dirHandler{h.Handler.WithAttrs(as)}
}

func (h dirHandler) WithGroup(name string) slog.Handler {
	return dirHandler{h.Handler.WithGroup(name)}
}

// verbosity is a counted flag value (flag.Value); IsBoolFlag lets -v be used without an argument.
type verbosity int

func (v *verbosity) String() string   { return strconv.Itoa(int(*v)) }
func (v *verbosity) Set(string) error { *v++; return nil }
func (*verbosity) IsBoolFlag() bool   { return true }

func expandVerbosityArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			// Past the separator the tokens belong to a forwarded tool, not to magus;
			// this builds the master argv startup() parses, so copy the rest through
			// unchanged instead of expanding -vvv-lookalikes meant for the tool.
			out = append(out, args[i:]...)
			break
		}
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			allV := true
			for _, c := range a[1:] {
				if c != 'v' {
					allV = false
					break
				}
			}
			if allV {
				for range a[1:] {
					out = append(out, "-v")
				}
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// effectiveLevel maps verbosity/quiet flags to a slog.Level; quiet wins over -v.
//
// -v and -vv share the debug level on purpose: they differ in whether each
// target's own output streams (Log.Stream, set in applyDisplay), not in which
// records are admitted. Splitting them by level instead would mean -v silently
// dropping debug records, which is the opposite of what the flag is for.
func effectiveLevel(v verbosity, quiet bool) slog.Level {
	switch {
	case quiet:
		return slog.LevelError
	case v >= 3:
		return config.LevelTrace
	case v >= 1:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

func levelName(lvl slog.Level) string {
	if lvl == config.LevelTrace {
		return "trace"
	}
	return lvl.String()
}

// displayHandler is the pretty handler currently installed as the process-wide
// default, or nil when the configured format does not use one. applyDisplay runs
// more than once per invocation (once while parsing global flags, again after
// magus.yaml is loaded), and each pretty handler owns a terminal region; holding
// the live one here lets each call release its predecessor.
//
// Guarded by displayMu: applyDisplay runs on the startup path while
// restoreTerminal can run from the signal-driven cleanup path.
var (
	displayMu      sync.Mutex
	displayHandler *cache.PrettyHandler
)

// resetOnce guards the inbound reset. applyDisplay runs more than once per
// invocation, so reaching restoreTerminal each time emitted the reset burst
// three times before a command's first byte of output. One is enough to heal a
// previously crashed run.
var resetOnce sync.Once

// releaseDisplay hands back the current handler's rows without touching global
// terminal state, for the startup path where nothing has reserved any yet.
func releaseDisplay() {
	displayMu.Lock()
	h := displayHandler
	displayHandler = nil
	displayMu.Unlock()

	if h != nil {
		_ = h.Close()
	}
	_ = tty.ReleaseStderr()
}

// restoreTerminal undoes any terminal state this run reserved. runCLI calls it
// on every exit path, including the signal path, so an interrupted run does not
// hand back a shell pinned inside a scroll region.
//
// The unconditional reset is not redundant with closing the handler: the cache
// builds its own pretty handler (see internal/cache.Open), and that one reserves
// the region during a normal run. Margins belong to the terminal rather than to
// whoever set them, so one reset covers every handler without threading
// ownership through the cache.
func restoreTerminal() {
	displayMu.Lock()
	h := displayHandler
	displayHandler = nil
	displayMu.Unlock()

	if h != nil {
		_ = h.Close()
	}
	// Give back every leased row on stderr and stop the notification band's
	// expiry sweeper. This is the ordered teardown; the unconditional reset
	// below is still the backstop for margins nothing here owns.
	_ = tty.ReleaseStderr()
	_ = tty.ResetScrollMargins(os.Stderr, tty.SystemProbe)
	// And mouse reporting, for the same reason and on the same unconditional
	// terms: a process that exits with tracking on leaves the user unable to
	// select text in their own shell.
	_ = tty.ResetMouseTracking(os.Stderr, tty.SystemProbe)
}

// applyDisplay configures the process-global slog logger and writes the resolved level back to globalCfg.
func applyDisplay() {
	// Release the previous handler's region before installing a replacement,
	// so a second call does not strand the first one's scroll margins.
	//
	// releaseDisplay, not restoreTerminal: nothing has reserved a region on this
	// path yet, so the escape burst would be pure output.
	releaseDisplay()
	resetOnce.Do(func() {
		_ = tty.ResetScrollMargins(os.Stderr, tty.SystemProbe)
		_ = tty.ResetMouseTracking(os.Stderr, tty.SystemProbe)
	})

	// --silent implies --quiet's suppression; the extra behavior rides on Log.Silent.
	quiet := global.quiet || global.silent
	lvl := effectiveLevel(global.verbose, quiet)
	addSource := !quiet && global.verbose >= 3

	globalCfg.Log.Level = levelName(lvl)
	if global.silent {
		s := true
		globalCfg.Log.Silent = &s
	}
	// -vv is where target output starts streaming live; -v deliberately keeps the
	// collapsed scoreboard so "a bit more detail" stays readable. -vvv implies -vv.
	// Only set on an explicit -vv so a magus.yaml log.stream is not overwritten by
	// a plain run.
	// quiet dominates: -vv -q asked for silence, and streaming every target's output
	// would be the loudest possible reading of that. Set explicitly in both directions
	// so a magus.yaml log.stream cannot resurrect it under --quiet either.
	if global.verbose >= 2 || quiet {
		stream := global.verbose >= 2 && !quiet
		globalCfg.Log.Stream = &stream
	}

	opts := &slog.HandlerOptions{Level: lvl, AddSource: addSource}
	var h slog.Handler
	switch globalCfg.Log.Format {
	case "json":
		h = secret.NewRedactingHandler(slog.NewJSONHandler(os.Stderr, opts))
	case "text":
		h = secret.NewRedactingHandler(slog.NewTextHandler(os.Stderr, opts))
	default: // pretty, plain
		// Render general diagnostics through the same compact handler as cache
		// events so they share one style and stop interleaving as raw
		// "time=... level=..." lines with the pretty output.
		ph := cache.NewPrettyHandler(os.Stderr, lvl)
		displayMu.Lock()
		displayHandler = ph
		displayMu.Unlock()
		h = ph
	}
	slog.SetDefault(slog.New(dirHandler{h}))
}
