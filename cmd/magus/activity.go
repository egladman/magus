package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessionjournal"
)

// activityDefaultLimit bounds the default listing to a session or two of scrollback.
// The store is grow-only, so an unbounded default would get slower forever.
const activityDefaultLimit = 20

// activityCmd shows what recent magus sessions did, folded across every worktree of
// this repository.
//
// The name matches the console's Activity surface on purpose, and the two do NOT
// read the same store: the console's Activity is internal/trail (actions taken
// against the daemon), while this reads internal/sessionjournal (facts a session
// produced). They are meant to converge. Until they do, one name over two stores
// will read as a bug to anyone who compares them.
func activityCmd(_ context.Context, root string, args []string) error {
	var limit int
	rest, err := cmdParse("activity", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", activityDefaultLimit, "Show at most this many sessions (0 for all)")
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus activity: takes no arguments (got %q); use --limit to bound the listing", rest[0])
	}

	dir, err := sessionjournal.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessionjournal.Read(dir)
	if err != nil {
		return err
	}
	sessions := sessionjournal.Summarize(fold)
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		return renderActivityText(sessions, fold, dir)
	case outputName:
		for _, s := range sessions {
			fmt.Println(s.Session)
		}
		return nil
	}
	return emitFormatted(opts, map[string]any{
		"sessions": sessions,
		"skipped":  fold.Skipped,
		"store":    dir,
	})
}

func renderActivityText(sessions []sessionjournal.Summary, fold sessionjournal.Fold, dir string) error {
	if len(sessions) == 0 {
		// An empty store is the normal state before any run, so this says how facts
		// get there rather than reporting a fault.
		fmt.Fprintf(os.Stdout, "no sessions recorded yet in %s\n", dir)
		fmt.Fprintln(os.Stdout, "magus records one as soon as a `magus run` finishes a target in any worktree of this repository")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tLAST\tHOST\tFACTS\tTARGETS")
	for _, s := range sessions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			s.Session,
			time.UnixMilli(s.LastMs).Format("2006-01-02 15:04:05"),
			orDash(s.Host),
			s.Facts,
			orDash(summarizeTargets(s.Targets)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if fold.Skipped > 0 {
		// Surfaced rather than swallowed: a skipped line is a fact that happened and
		// cannot be shown, which is exactly the thing a silent reader would misreport
		// as "nothing happened".
		fmt.Fprintf(os.Stdout, "\n%d unreadable line(s) skipped (a session killed mid-write leaves a partial line; the rest of its journal still reads)\n", fold.Skipped)
	}
	return nil
}

// summarizeTargets renders a session's targets as "<target> <project> (<outcome>)",
// collapsing a repeated target so a session that ran one target over forty projects
// does not render forty times.
func summarizeTargets(targets []sessionjournal.TargetResult) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range targets {
		label := t.Target
		if t.Project != "" {
			label += " " + t.Project
		}
		label += " (" + t.Outcome
		if t.Replayed {
			label += ", cached"
		}
		label += ")"
		if seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	return strings.Join(out, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ---- producer ----

// sessionFactHandler turns the execution journal's result events into durable session
// facts. It is a slog.Handler so it can ride the capture fan-out magus already builds
// per invocation, rather than threading a second observer down the run path.
//
// It writes NOTHING for a run that produces no result, and it never returns an error
// to the fan-out: capture is best-effort by contract, and a journal that can fail a
// build would be worse than no journal at all.
type sessionFactHandler struct {
	dir   string
	start sessionjournal.SessionStart

	mu     sync.Mutex
	writer *sessionjournal.Writer
	broken bool
}

// beginSessionJournal builds the capture handler that records this invocation's
// results as session facts. It returns nil when the store cannot be resolved, which
// the fan-out treats as "no handler" - a machine with no usable state directory
// still runs builds.
func beginSessionJournal(root string, command []string, magusVersion string) slog.Handler {
	dir, err := sessionjournal.Dir(root)
	if err != nil {
		return nil
	}
	return &sessionFactHandler{
		dir: dir,
		start: sessionjournal.SessionStart{
			Workspace: root,
			Command:   strings.Join(command, " "),
			Version:   magusVersion,
		},
	}
}

func (h *sessionFactHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *sessionFactHandler) Handle(_ context.Context, r slog.Record) error {
	e, ok := journal.EventFromRecord(r)
	if !ok || e.Kind != journal.KindResult || e.Target == "" {
		return nil
	}
	// The invocation id IS the session id. Reusing it rather than minting a second
	// identifier is what keeps a session fact joinable to the execution journal that
	// produced it; an event without one is unattributable, so it is dropped rather
	// than filed under an invented session.
	if e.Inv == "" {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.broken {
		return nil
	}
	if h.writer == nil {
		w, err := sessionjournal.Open(h.dir, e.Inv, h.start)
		if err != nil {
			h.broken = true
			return nil
		}
		h.writer = w
	}
	if err := h.writer.Append(sessionjournal.KindTargetResult, sessionjournal.TargetResult{
		Target:   e.Target,
		Project:  e.Project,
		Outcome:  sessionOutcome(e.Status),
		DurMs:    e.DurMs,
		Replayed: e.Status == journal.StatusCached,
		Ref:      e.Ref,
	}); err != nil {
		// One failed append means the store is unwritable (a full disk, a read-only
		// state dir); retrying per target would repeat the same failure once per
		// result and slow the run down to prove it.
		h.broken = true
	}
	return nil
}

func (h *sessionFactHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *sessionFactHandler) WithGroup(string) slog.Handler      { return h }

// sessionOutcome maps the execution journal's three statuses onto the journal's
// pass/fail axis. A cache hit is a PASS that was replayed, not a third outcome:
// "did this target end up green" and "did work run" are different questions, and
// TargetResult.Replayed answers the second.
//
// Note the known imprecision on the way in: a cache hit whose ref is missing from
// the output store is reported as StatusPass, so Replayed under-reports rather than
// over-reports cache hits.
func sessionOutcome(status string) string {
	if status == journal.StatusFail {
		return sessionjournal.OutcomeFail
	}
	return sessionjournal.OutcomePass
}
