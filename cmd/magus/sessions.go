package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/sessions"
)

// sessionsDefaultLimit bounds the default listing to a session or two of scrollback.
// The store is grow-only, so an unbounded default would get slower forever.
const sessionsDefaultLimit = 20

// sessionsCmd shows what recent magus sessions did, folded across every worktree of
// this repository.
//
// It is NOT the console's Activity surface, and the two no longer share a name: the
// console's Activity reads internal/trail (actions taken against the daemon) and
// keeps the Activity name, while this reads internal/sessions (facts a session
// produced). The two stores are still meant to converge; until they do, each is named
// after what it holds rather than both after one word.
func sessionsCmd(_ context.Context, root string, args []string) error {
	var limit int
	var since string
	rest, err := cmdParse("sessions", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", sessionsDefaultLimit, "Show at most this many sessions (0 for all)")
		fs.StringVar(&since, "since", "", "Show only sessions active since this point: a duration back from now (2h, 45m, 168h) or an RFC3339 timestamp")
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus sessions: takes no arguments (got %q); use --limit to bound the listing and --since to bound its age", rest[0])
	}
	if limit < 0 {
		return usagef("magus sessions: --limit must be zero or more (got %d); 0 lists every session", limit)
	}
	cutoff, err := parseSince(since)
	if err != nil {
		return err
	}

	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus sessions: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return err
	}
	summaries := sessions.Summarize(fold)
	summaries = sessionsSince(summaries, cutoff)
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		return renderSessionsText(summaries, fold, dir, !cutoff.IsZero())
	case outputName:
		for _, s := range summaries {
			fmt.Println(s.Session)
		}
		return nil
	}
	return emitFormatted(opts, map[string]any{
		"sessions": summaries,
		"skipped":  fold.Skipped,
		"store":    dir,
	})
}

// filtered says a --since cutoff was applied, which is what separates an empty store
// from a store whose sessions are all older than the window. Reporting the second as the
// first sends a person looking for a broken producer.
func renderSessionsText(summaries []sessions.Summary, fold sessions.Fold, dir string, filtered bool) error {
	if len(summaries) == 0 {
		if filtered {
			fmt.Fprintf(os.Stdout, "no sessions in that window; %s holds %d session file(s)\n", dir, fold.Sessions)
			fmt.Fprintln(os.Stdout, "widen --since, or drop it to list every session")
			return nil
		}
		// An empty store is the normal state before any run, so this says how facts
		// get there rather than reporting a fault.
		fmt.Fprintf(os.Stdout, "no sessions recorded yet in %s\n", dir)
		fmt.Fprintln(os.Stdout, "magus records one as soon as a `magus run` finishes a target in any worktree of this repository")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tLAST\tHOST\tDELEGATION\tFACTS\tTARGETS")
	for _, s := range summaries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			s.Session,
			time.UnixMilli(s.LastMs).Format("2006-01-02 15:04:05"),
			orDash(s.Host),
			orDash(s.Delegation),
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
		fmt.Fprintf(os.Stdout, "\n%d unreadable line(s) skipped (a session killed mid-write leaves a partial line; the rest of its records still read)\n", fold.Skipped)
	}
	return nil
}

// summarizeTargets renders a session's targets as "<target> <project> (<outcome>)",
// collapsing a repeated target so a session that ran one target over forty projects
// does not render forty times.
func summarizeTargets(targets []sessions.TargetResult) string {
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

// parseSince turns a --since value into the earliest last-fact time worth listing. An
// empty value returns the zero time, which admits everything.
//
// Two spellings, because the question has two shapes: "what has happened lately" is a
// duration back from now, and "what happened since the incident" is an instant. A bare
// duration is accepted as written rather than requiring a leading "-": the flag already
// says "since", and a caller who wrote --since -2h would be asking for the future.
func parseSince(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			d = -d
		}
		return time.Now().Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, nil
	}
	return time.Time{}, usagef("magus sessions: --since %q is neither a duration nor an RFC3339 timestamp; write a duration back from now (2h, 45m, 168h) or an instant (2006-01-02T15:04:05Z)", raw)
}

// sessionsSince drops the sessions whose last fact predates cutoff.
//
// It filters on LastMs rather than StartedMs so a long-lived session that is still
// working stays listed. The alternative hides exactly the session a person asking "what
// is happening lately" most wants: one that began before the window and has not stopped.
func sessionsSince(summaries []sessions.Summary, cutoff time.Time) []sessions.Summary {
	if cutoff.IsZero() {
		return summaries
	}
	ms := cutoff.UnixMilli()
	out := make([]sessions.Summary, 0, len(summaries))
	for _, s := range summaries {
		if s.LastMs >= ms {
			out = append(out, s)
		}
	}
	return out
}
