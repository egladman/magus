package main

import (
	"log/slog"
	"sync/atomic"

	"github.com/egladman/magus/internal/log/quiet"
)

// quietForced is set for a command only a host's glue runs, never a person: the MCP
// server (its dispatch profile) and the guard when installed glue calls it (shell.go).
var quietForced atomic.Bool

// quietDisplay reports whether this invocation's display is quiet: -q, -s, log.silent,
// or a command only a host's glue runs.
func quietDisplay() bool {
	return global.quiet || global.silent || globalCfg.Log.IsSilent() || quietForced.Load()
}

// forceQuietDisplay quiets the display for a command that learns from its own flags,
// after applyDisplay has run, that a host's glue called it. It filters the installed
// display rather than rebuilding it, which would hand back and retake the terminal;
// -o jsonl stays whole as applyDisplay leaves it.
func forceQuietDisplay() {
	wasQuiet := quietDisplay()
	quietForced.Store(true)
	if wasQuiet || global.output == string(FormatJSONL) {
		return
	}
	slog.SetDefault(slog.New(quiet.Wrap(slog.Default().Handler(), global.verbose >= 1)))
}
