package main

import (
	"log/slog"
	"os"
	"strconv"

	"github.com/egladman/magus/internal/config"
)

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
	lvl := effectiveLevel(global.verbose, global.quiet)
	addSource := !global.quiet && global.verbose >= 3

	globalCfg.Log.Level = levelName(lvl)

	opts := &slog.HandlerOptions{Level: lvl, AddSource: addSource}
	var h slog.Handler
	if globalCfg.Log.Format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}
