package main

import (
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/log/audience"
	"github.com/egladman/magus/internal/trail"
)

// audienceForced is set for a command only an agent host runs: the MCP server (its
// dispatch profile) and the guard when installed glue calls it (shell.go).
var audienceForced atomic.Bool

// resolveAudience decides who reads this invocation's display. configured is log.audience
// after magus.yaml, MAGUS_LOG_AUDIENCE and --log-audience have merged, and wins when set.
// Then an agent host (forced) or a lease in BAGGAGE means an agent; a terminal on stderr,
// or a CI runner whose log a person reads, means a human. Anything else is a program
// reading a pipe, and an agent is the reader that pays for every line it is sent.
func resolveAudience(configured string, env func(string) string, stderrTerminal, forced bool) audience.Audience {
	switch a := audience.Audience(configured); {
	case a == audience.Human || a == audience.Agent:
		return a
	case forced, trail.LeaseFromBaggage(env(trail.EnvBaggage)) != "":
		return audience.Agent
	case stderrTerminal, env("CI") != "":
		return audience.Human
	default:
		return audience.Agent
	}
}

// invocationAudience is resolveAudience over this process's config and environment.
func invocationAudience() audience.Audience {
	return resolveAudience(globalCfg.Log.Audience, os.Getenv,
		tty.IsTerminalWriter(os.Stderr, tty.SystemProbe), audienceForced.Load())
}

// forceAgentAudience marks this invocation as run by an agent host, for a command that
// learns it from its own flags after applyDisplay has run. It filters the installed
// display rather than rebuilding it, which would hand back and retake the terminal; a
// configured log.audience still wins, and -o jsonl stays whole as applyDisplay leaves it.
func forceAgentAudience() {
	audienceForced.Store(true)
	if global.output == string(FormatJSONL) || invocationAudience() != audience.Agent {
		return
	}
	slog.SetDefault(slog.New(audience.Wrap(slog.Default().Handler(), audience.Agent, global.verbose >= 1)))
}
