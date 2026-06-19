package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"github.com/egladman/magus/internal/config"
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
	for _, a := range args {
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

// applyDisplay configures the process-global slog logger and writes the resolved level back to globalCfg.
func applyDisplay() {
	// --silent implies --quiet's suppression; the extra behavior rides on Log.Silent.
	quiet := global.quiet || global.silent
	lvl := effectiveLevel(global.verbose, quiet)
	addSource := !quiet && global.verbose >= 3

	globalCfg.Log.Level = levelName(lvl)
	if global.silent {
		s := true
		globalCfg.Log.Silent = &s
	}

	opts := &slog.HandlerOptions{Level: lvl, AddSource: addSource}
	var h slog.Handler
	if globalCfg.Log.Format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(dirHandler{h}))
}
