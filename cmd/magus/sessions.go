package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
)

// sessionsDefaultLimit bounds the default listing to a session or two of scrollback.
// The store is grow-only, so an unbounded default would get slower forever.
const sessionsDefaultLimit = 20

// sessionCmd is the whole session family behind one noun. The grouping is the point:
// every subverb reads or writes the one store keyed by repository identity, none needs
// the magusfile to load, and the machine ingest (hook, notify) lives beside the human
// reads instead of burning top-level names no person types.
//
// The listing is NOT the console's Activity surface: Activity reads internal/trail
// (actions against the daemon), this reads internal/sessions (facts a session
// produced). The stores are meant to converge; until they do, each is named after
// what it holds.
func sessionCmd(ctx context.Context, root string, args []string) error {
	verb, rest := "ls", args
	// A leading flag belongs to the default verb, so `magus session -o json` and
	// `magus session --since 2h` read as listings rather than unknown subcommands.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, rest = args[0], args[1:]
	}
	switch verb {
	case "help":
		sessionUsage()
		return nil
	case "ls":
		return sessionList(root, rest)
	case "attention":
		return attentionList(root, rest)
	case "dispose":
		return attentionDispose(root, rest)
	case "hook":
		return hookCmd(ctx, os.Stdin, os.Stdout, rest)
	case "notify":
		return notifyCmd(ctx, root, os.Stdin, os.Stdout, rest)
	default:
		return usagef("magus session: unknown subcommand %q (want ls, attention, dispose, hook, or notify); the bare command lists recent sessions, bounded by --limit and --since", verb)
	}
}

func sessionUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus session [ls] [--limit <n>] [--since <when>]")
	fmt.Fprintln(os.Stderr, "       magus session attention [flags]")
	fmt.Fprintln(os.Stderr, "       magus session dispose <id> [-reason <text>]")
	fmt.Fprintln(os.Stderr, "       magus session hook [flags]      # machine: guard verdicts, wired by agent hosts")
	fmt.Fprintln(os.Stderr, "       magus session notify [flags]    # machine: event ingest, wired by agent hosts")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One store, two sides. Humans read it: `session` lists what recent sessions")
	fmt.Fprintln(os.Stderr, "did across every worktree of this repository, `session attention` lists the")
	fmt.Fprintln(os.Stderr, "blocks agents raised, and `session dispose` closes one - nothing closes a")
	fmt.Fprintln(os.Stderr, "request automatically. Agent hosts write it: their hooks pipe events through")
	fmt.Fprintln(os.Stderr, "`session hook` and `session notify`.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run `magus session <subcommand> -h` for each subverb's flags.")
}

// sessionList shows what recent magus sessions did, folded across every worktree of
// this repository.
func sessionList(root string, args []string) error {
	var limit int
	var since string
	rest, err := cmdParse("session", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", sessionsDefaultLimit, "Show at most this many sessions (0 for all)")
		fs.StringVar(&since, "since", "", "Show only sessions active since this point: a duration back from now (2h, 45m, 168h) or an RFC3339 timestamp")
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus session: takes no arguments (got %q); use --limit to bound the listing and --since to bound its age", rest[0])
	}
	if limit < 0 {
		return usagef("magus session: --limit must be zero or more (got %d); 0 lists every session", limit)
	}
	cutoff, err := parseSince(since)
	if err != nil {
		return err
	}

	root = resolveRootOrEmpty(root)
	if root == "" {
		return fmt.Errorf("magus session: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
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
		fmt.Fprintln(os.Stdout, "magus records one as soon as a `"+hint.Run.String()+"` finishes a target in any worktree of this repository")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	bySpan := make(map[string]string, len(summaries))
	for _, s := range summaries {
		if s.SpanID != "" {
			bySpan[s.SpanID] = s.Session
		}
	}
	fmt.Fprintln(tw, "SESSION\tLAST\tHOST\tLEASE\tSPAWNER\tPARENT\tFACTS\tTARGETS")
	for _, s := range summaries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			s.Session,
			time.UnixMilli(s.LastMs).Format("2006-01-02 15:04:05"),
			orDash(s.Host),
			orDash(s.Lease),
			orDash(s.Spawner),
			orDash(sessionParent(bySpan, s.ParentSpanID)),
			s.Facts,
			orDash(summarizeTargets(s.Targets)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if open := len(sessions.AttentionQueue(fold)); open > 0 {
		// A reader of the history is often looking for what needs them; one line
		// points at the other view of the same store.
		fmt.Fprintf(os.Stdout, "\n%d attention request(s) open; `%s` lists them\n", open, hint.SessionAttention)
	}

	if fold.Skipped > 0 {
		// Surfaced rather than swallowed: a skipped line is a fact that happened and
		// cannot be shown, which is exactly the thing a silent reader would misreport
		// as "nothing happened".
		fmt.Fprintf(os.Stdout, "\n%d unreadable line(s) skipped (a session killed mid-write leaves a partial line; the rest of its records still read)\n", fold.Skipped)
	}
	return nil
}

// sessionParent names the session a span id belongs to, falling back to the raw id when this
// store holds no record of it. The id is the honest answer rather than a blank: the parent may
// have run in a repository this store does not cover, or before the retention window, and
// "unknown to magus" is a different fact from "spawned by nobody".
//
// Resolved for the TEXT view only. -o json carries the ids, because a consumer joining sessions
// wants the identity rather than this reader's guess at a name for it.
func sessionParent(bySpan map[string]string, parentSpanID string) string {
	if session, ok := bySpan[parentSpanID]; ok {
		return session
	}
	return parentSpanID
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
	return time.Time{}, usagef("magus session: --since %q is neither a duration nor an RFC3339 timestamp; write a duration back from now (2h, 45m, 168h) or an instant (2006-01-02T15:04:05Z)", raw)
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
