package main

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
)

// promptCacheForCheckout resolves the prompt-cache clock from the newest guard
// observation in THIS checkout's trail, which is where a hook wired here wrote.
//
// The checkout rather than the repository, unlike everything else the session listing
// reads: a session's tool calls land in the cache dir of the tree they ran against,
// and the question this answers is about the agent sitting in front of you.
//
// An empty clock for a checkout whose trail holds nothing: no cache dir, no hook
// wired, or a tree nobody has run an agent against. Nothing is printed then, because
// the alternative is a clock counting up from an activity magus never saw.
func promptCacheForCheckout(root string, now time.Time) sessions.PromptCacheClock {
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return sessions.PromptCacheClock{}
	}
	act, ok := trail.LastAgentActivity(cacheDir)
	if !ok {
		return sessions.PromptCacheClock{}
	}
	return sessions.PromptCacheAt(act.Session, act.At, now)
}

// renderPromptCache prints how long since the last tool call ran past the guard here,
// and when each published window closes for it.
//
// Every provider, never one: which provider a session is talking to and which window
// it bought are both invisible from here, so picking a row for the reader would be a
// guess stated as a fact.
func renderPromptCache(w io.Writer, clock sessions.PromptCacheClock, now time.Time) {
	if len(clock.Providers) == 0 {
		return
	}
	fmt.Fprintf(w, "\nPrompt cache: last tool call here %s ago", clock.SinceText())
	if clock.Session != "" {
		fmt.Fprintf(w, ", session %s", clock.Session)
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, p := range clock.Providers {
		for _, win := range p.Windows {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", p.Provider, win.Window, win.Describe(now))
		}
	}
	if err := tw.Flush(); err != nil {
		return
	}
	for _, p := range clock.Providers {
		if len(p.Windows) == 0 {
			fmt.Fprintf(w, "  %s: %s\n", p.Provider, p.Note)
		}
	}
	fmt.Fprintln(w, "  resuming past a closed window re-pays the prompt; which window a host bought is not visible here")
}
