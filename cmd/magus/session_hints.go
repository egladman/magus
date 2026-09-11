package main

// `magus session hints`: how often a breadcrumb magus printed was taken up. A query
// over loaded sessions rather than a script over somebody's home directory, because
// the store records what a result carried.
//
// It reports and never acts. The floor below is named in the output as a note, not
// applied: which hints go is a decision, and magus informs.

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/egladman/magus/internal/sessions"
)

// hintFloor is the uptake a breadcrumb has to clear to be worth its context. Measured
// over 21 days of this repo's sessions: the hints that correct the command in front of
// the reader convert at 34-43%, the standing-fact ones at under 5%, and there is
// nothing in between to split the difference with.
const hintFloor = 0.15

// hintUptakeRow is one hint id's record. Rejected and Reflex describe the same
// servings Followed does not, so the three do not sum to Served: a call can reject a
// breadcrumb and re-run the failing target in the same window.
type hintUptakeRow struct {
	ID       string  `json:"id"`
	Served   int     `json:"served"`
	Followed int     `json:"followed"`
	Rejected int     `json:"rejected"`
	Reflex   int     `json:"reflex"`
	Rate     float64 `json:"rate"`
}

type hintUptakeReport struct {
	Definition string          `json:"definition"`
	Floor      float64         `json:"floor"`
	Lookahead  int             `json:"lookahead"`
	Hints      []hintUptakeRow `json:"hints"`
}

const hintUptakeDefinition = "hints reports, per hint id, how often a breadcrumb magus served was followed within the next few calls of the same session, against the calls that ran some other magus verb instead or re-ran the same command."

func sessionHintsUsage(fs *flag.FlagSet) func() {
	return func() {
		fmt.Fprintln(os.Stderr, "Usage: magus session hints")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Per hint id: how many times it was served, how often the command was run")
		fmt.Fprintf(os.Stderr, "within the next %d calls, how often another magus verb was run instead, and\n", nextLookahead)
		fmt.Fprintln(os.Stderr, "how often the same command was repeated.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "It reads the sessions this repository has loaded, so a store with no")
		fmt.Fprintln(os.Stderr, "transcripts in it reports nothing. Load one with `magus session load`.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
		fs.PrintDefaults()
	}
}

// sessionHints implements `magus session hints`.
func sessionHints(root string, args []string) error {
	rest, err := cmdParse("session hints", args, func(fs *flag.FlagSet) {
		fs.Usage = sessionHintsUsage(fs)
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus session hints: takes no arguments (got %q)", rest[0])
	}

	root = resolveRootOrEmpty(root)
	if root == "" {
		return errors.New("magus session hints: no workspace here: the session store is keyed by repository, so run from inside one or pass --root <path>")
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return err
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return err
	}

	report := hintUptakeReport{
		Definition: hintUptakeDefinition,
		Floor:      hintFloor,
		Lookahead:  nextLookahead,
		Hints:      hintUptake(fold),
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputText {
		renderHintUptake(os.Stdout, report)
		return nil
	}
	return emitFormatted(opts, report)
}

// hintUptake counts each id's servings and what the calls after them did.
func hintUptake(fold sessions.Fold) []hintUptakeRow {
	rows := map[string]*hintUptakeRow{}
	row := func(id string) *hintUptakeRow {
		if r, ok := rows[id]; ok {
			return r
		}
		r := &hintUptakeRow{ID: id}
		rows[id] = r
		return r
	}

	for _, calls := range shellCallsBySession(fold) {
		for i, call := range calls {
			window := calls[i+1:min(i+1+nextLookahead, len(calls))]
			for _, id := range call.NextServed {
				r := row(id)
				r.Served++
				switch {
				case slices.ContainsFunc(window, func(e sessions.AgentEvent) bool {
					return slices.Contains(e.NextFollowed, id)
				}):
					r.Followed++
				case slices.ContainsFunc(window, func(e sessions.AgentEvent) bool {
					return e.Program == "magus"
				}):
					r.Rejected++
				}
				// Counted beside the two above rather than instead of them: re-running the
				// command that printed the breadcrumb is the reflex the whole mechanism was
				// measured against, and it happens whether or not the hint was taken.
				if call.Digest != "" && slices.ContainsFunc(window, func(e sessions.AgentEvent) bool {
					return e.Digest == call.Digest
				}) {
					r.Reflex++
				}
			}
		}
	}

	out := make([]hintUptakeRow, 0, len(rows))
	for _, r := range rows {
		if r.Served > 0 {
			r.Rate = float64(r.Followed) / float64(r.Served)
		}
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b hintUptakeRow) int {
		if a.Rate != b.Rate {
			return cmp.Compare(b.Rate, a.Rate)
		}
		return cmp.Compare(b.Served, a.Served)
	})
	return out
}

// shellCallsBySession buckets the fold's shell commands by session, oldest first
// within each, sessions in a stable order so two runs over one store report the same
// table.
//
// ONE pass over the records. Listing the sessions and then asking for each one's
// events walks the whole fold once per session, and a store with 200 sessions over
// 100k records decodes 20M payloads to answer one table.
func shellCallsBySession(fold sessions.Fold) [][]sessions.AgentEvent {
	bySession := map[string][]sessions.AgentEvent{}
	for session, e := range sessions.EachAgentEvent(fold) {
		// The lookahead counts CALLS, so a file read between two of them must not
		// spend one.
		if e.Kind == sessions.EventShellCommand {
			bySession[session] = append(bySession[session], e)
		}
	}
	out := make([][]sessions.AgentEvent, 0, len(bySession))
	for _, session := range slices.Sorted(maps.Keys(bySession)) {
		calls := bySession[session]
		slices.SortStableFunc(calls, func(a, b sessions.AgentEvent) int { return cmp.Compare(a.AtMs, b.AtMs) })
		out = append(out, calls)
	}
	return out
}

func renderHintUptake(w io.Writer, report hintUptakeReport) {
	if len(report.Hints) == 0 {
		fmt.Fprintln(w, "no hint was served in any loaded session.")
		fmt.Fprintln(w, "load a host transcript with `magus session load` first; uptake is counted from what a call served.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HINT\tSERVED\tFOLLOWED\tREJECTED\tREFLEX\tRATE")
	for _, r := range report.Hints {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%.1f%%\n", r.ID, r.Served, r.Followed, r.Rejected, r.Reflex, r.Rate*100)
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\nfollowed = the command ran within the next %d calls of the same session.\n", report.Lookahead)
	fmt.Fprintf(w, "a hint under %.0f%% is spending context on advice nobody takes.\n", report.Floor*100)
}
