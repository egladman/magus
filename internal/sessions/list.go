package sessions

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/session"
)

// RenderInvocations prints the listing `magus session` opens with.
//
// filtered says a --since cutoff was applied, which is what separates an empty store
// from a store whose invocations are all older than the window. Reporting the second as
// the first sends a person looking for a broken producer.
//
// INVOCATION is magus's own id for the process; SESSION is the host's conversation it
// ran in, a dash when no host delivered one. clock is the prompt-cache clock of the
// checkout, empty when its trail holds nothing.
func RenderInvocations(ctx context.Context, root string, w io.Writer, summaries []session.Summary, fold session.Fold, dir string, filtered bool, clock PromptCacheClock) error {
	renderCheckpoints(ctx, root, w, LatestCheckpoints(fold))
	if len(summaries) == 0 {
		if filtered {
			fmt.Fprintf(w, "no invocations in that window; %s holds %d invocation file(s)\n", dir, fold.Invocations)
			fmt.Fprintln(w, "widen --since, or drop it to list every invocation")
			return nil
		}
		// An empty store is the normal state before any run, so this says how facts
		// get there rather than reporting a fault.
		fmt.Fprintf(w, "no invocations recorded yet in %s\n", dir)
		fmt.Fprintf(w, "magus records one as soon as a `%s` finishes a target in any checkout of this repository\n", hint.Run.String())
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	bySpan := make(map[string]string, len(summaries))
	for _, s := range summaries {
		if s.SpanID != "" {
			bySpan[s.SpanID] = s.Invocation
		}
	}
	fmt.Fprintln(tw, "INVOCATION\tLAST\tSESSION\tUSER\tHOST\tLEASE\tSPAWNER\tPARENT\tFACTS\tEVENTS\tTARGETS")
	for _, s := range summaries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			s.Invocation,
			time.UnixMilli(s.LastMs).Format("2006-01-02 15:04:05"),
			OrDash(s.Session),
			OrDash(s.User),
			OrDash(s.Host),
			OrDash(s.Lease),
			OrDash(s.Spawner),
			OrDash(invocationParent(bySpan, s.ParentSpanID)),
			s.Facts,
			// A dash rather than a zero: no loaded events is the ordinary state, and a
			// column of zeros reads as a producer that broke.
			OrDash(strconv.Itoa(s.Events)),
			OrDash(summarizeTargets(s.Targets)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	renderPromptCache(w, clock, time.Now())

	if open := len(AttentionQueue(fold)); open > 0 {
		// A reader of the history is often looking for what needs them; one line
		// points at the other view of the same store.
		fmt.Fprintf(w, "\n%d attention request(s) open; `%s` lists them\n", open, hint.SessionAttention)
	}

	if fold.Skipped > 0 {
		// Surfaced rather than swallowed: a skipped line is a fact that happened and
		// cannot be shown, which is exactly the thing a silent reader would misreport
		// as "nothing happened".
		fmt.Fprintf(w, "\n%d unreadable line(s) skipped (an invocation killed mid-write leaves a partial line; the rest of its records still read)\n", fold.Skipped)
	}
	if fold.Legacy > 0 {
		fmt.Fprintf(w, "\n%d line(s) not shown: written before magus called a per-process id an invocation, in a shape this build does not read; their files are kept\n", fold.Legacy)
	}
	return nil
}

// invocationParent names the invocation a span id belongs to, falling back to the raw id when
// this store holds no record of it. The id is the honest answer rather than a blank: the parent may
// have run in a repository this store does not cover, or before the retention window, and
// "unknown to magus" is a different fact from "spawned by nobody".
//
// Resolved for the TEXT view only. -o json carries the ids, because a consumer joining
// invocations wants the identity rather than this reader's guess at a name for it.
func invocationParent(bySpan map[string]string, parentSpanID string) string {
	if invocation, ok := bySpan[parentSpanID]; ok {
		return invocation
	}
	return parentSpanID
}

// summarizeTargets renders a session's targets as "<target> <project> (<outcome>)",
// collapsing a repeated target so a session that ran one target over forty projects
// does not render forty times.
func summarizeTargets(targets []session.TargetResult) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range targets {
		label := t.Target
		if t.Project != "" {
			label += " " + t.Project
		}
		label += " (" + t.Outcome
		if t.DiagnosticCode != "" {
			label += ", " + t.DiagnosticCode
		}
		if t.Replayed {
			label += ", cached"
		}
		if t.Permitted {
			label += ", permitted"
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

// ParseSince turns a --since value into the earliest last-fact time worth listing. An
// empty value returns the zero time, which admits everything.
//
// Two spellings, because the question has two shapes: "what has happened lately" is a
// duration back from now, and "what happened since the incident" is an instant. A bare
// duration is accepted as written rather than requiring a leading "-": the flag already
// says "since", and a caller who wrote --since -2h would be asking for the future.
func ParseSince(raw string) (time.Time, error) {
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
	return time.Time{}, exit.Usagef("magus session: --since %q is neither a duration nor an RFC3339 timestamp; write a duration back from now (2h, 45m, 168h) or an instant (2006-01-02T15:04:05Z)", raw)
}

// InvocationsInCheckout keeps the invocations that ran in the checkout at root. The store
// is keyed by repository, so every checkout writes to it.
func InvocationsInCheckout(summaries []session.Summary, root string) []session.Summary {
	want := resolvedPath(root)
	resolved := map[string]string{}
	out := make([]session.Summary, 0, len(summaries))
	for _, s := range summaries {
		// compat(until: no stored invocation lacks a Workspace): one recorded before the
		// field existed stays, because dropping it would hide history rather than scope it.
		if s.Workspace == "" {
			out = append(out, s)
			continue
		}
		got, ok := resolved[s.Workspace]
		if !ok {
			got = resolvedPath(s.Workspace)
			resolved[s.Workspace] = got
		}
		if got == want {
			out = append(out, s)
		}
	}
	return out
}

// resolvedPath is p with symlinks resolved, so a checkout reached through /tmp and through
// /private/tmp compares equal. A path that no longer exists keeps its cleaned spelling.
func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// InvocationsSince drops the invocations whose last fact predates cutoff.
//
// It filters on LastMs rather than StartedMs so a long-lived invocation that is still
// working stays listed. The alternative hides exactly the one a person asking "what is
// happening lately" most wants: one that began before the window and has not stopped.
func InvocationsSince(summaries []session.Summary, cutoff time.Time) []session.Summary {
	if cutoff.IsZero() {
		return summaries
	}
	ms := cutoff.UnixMilli()
	out := make([]session.Summary, 0, len(summaries))
	for _, s := range summaries {
		if s.LastMs >= ms {
			out = append(out, s)
		}
	}
	return out
}
