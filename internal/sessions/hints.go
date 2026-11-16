package sessions

// `magus session hints`: how often a breadcrumb magus printed was taken up. A query
// over loaded sessions rather than a script over somebody's home directory, because
// the store records what a result carried.
//
// It reports and never acts. The floor below is named in the output as a note, not
// applied: which hints go is a decision, and magus informs.

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"text/tabwriter"

	"github.com/egladman/magus/libs/safetyscissors/session"
)

// NextLookahead is how many calls later a breadcrumb still counts as taken up. The
// measurement this metric has to be comparable with used five.
const NextLookahead = 5

// HintFloor is the uptake a breadcrumb has to clear to be worth its context. Measured
// over 21 days of this repo's sessions: the hints that correct the command in front of
// the reader convert at 34-43%, the standing-fact ones at under 5%, and there is
// nothing in between to split the difference with.
const HintFloor = 0.15

// HintUptakeRow is one hint id's record. Rejected and Reflex describe the same
// servings Followed does not, so the three do not sum to Served: a call can reject a
// breadcrumb and re-run the failing target in the same window.
type HintUptakeRow struct {
	ID       string  `json:"id"`
	Served   int     `json:"served"`
	Followed int     `json:"followed"`
	Rejected int     `json:"rejected"`
	Reflex   int     `json:"reflex"`
	Rate     float64 `json:"rate"`
}

// HintUptakeReport is the JSON shape of `session hints`.
type HintUptakeReport struct {
	Definition string          `json:"definition"`
	Floor      float64         `json:"floor"`
	Lookahead  int             `json:"lookahead"`
	Hints      []HintUptakeRow `json:"hints"`
}

// HintUptakeDefinition says what the report counts, so the output explains itself.
const HintUptakeDefinition = "hints reports, per hint id, how often a breadcrumb magus served was followed within the next few calls of the same session, against the calls that ran some other magus verb instead or re-ran the same command."

// HintUptake counts each id's servings and what the calls after them did.
func HintUptake(fold session.Fold) []HintUptakeRow {
	rows := map[string]*HintUptakeRow{}
	row := func(id string) *HintUptakeRow {
		if r, ok := rows[id]; ok {
			return r
		}
		r := &HintUptakeRow{ID: id}
		rows[id] = r
		return r
	}

	for _, calls := range shellCallsBySession(fold) {
		for i, call := range calls {
			window := calls[i+1 : min(i+1+NextLookahead, len(calls))]
			for _, id := range call.NextServed {
				r := row(id)
				r.Served++
				switch {
				case slices.ContainsFunc(window, func(e session.AgentEvent) bool {
					return slices.Contains(e.NextFollowed, id)
				}):
					r.Followed++
				case slices.ContainsFunc(window, func(e session.AgentEvent) bool {
					return e.Program == "magus"
				}):
					r.Rejected++
				}
				// Counted beside the two above rather than instead of them: re-running the
				// command that printed the breadcrumb is the reflex the whole mechanism was
				// measured against, and it happens whether or not the hint was taken.
				if call.Digest != "" && slices.ContainsFunc(window, func(e session.AgentEvent) bool {
					return e.Digest == call.Digest
				}) {
					r.Reflex++
				}
			}
		}
	}

	out := make([]HintUptakeRow, 0, len(rows))
	for _, r := range rows {
		if r.Served > 0 {
			r.Rate = float64(r.Followed) / float64(r.Served)
		}
		out = append(out, *r)
	}
	// The id breaks the last tie, because the rows come out of a map and the sort is not
	// stable: two hints that convert alike and were served alike ordered by whatever the
	// runtime handed back, so the table reordered itself between two runs over one store.
	slices.SortFunc(out, func(a, b HintUptakeRow) int {
		if a.Rate != b.Rate {
			return cmp.Compare(b.Rate, a.Rate)
		}
		if a.Served != b.Served {
			return cmp.Compare(b.Served, a.Served)
		}
		return cmp.Compare(a.ID, b.ID)
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
func shellCallsBySession(fold session.Fold) [][]session.AgentEvent {
	bySession := map[string][]session.AgentEvent{}
	for hostSession, e := range session.EachAgentEvent(fold) {
		// The lookahead counts CALLS, so a file read between two of them must not
		// spend one.
		if e.Kind == session.EventShellCommand {
			bySession[hostSession] = append(bySession[hostSession], e)
		}
	}
	out := make([][]session.AgentEvent, 0, len(bySession))
	for _, hostSession := range slices.Sorted(maps.Keys(bySession)) {
		calls := bySession[hostSession]
		slices.SortStableFunc(calls, func(a, b session.AgentEvent) int { return cmp.Compare(a.AtMs, b.AtMs) })
		out = append(out, calls)
	}
	return out
}

// RenderHintUptake prints the uptake table.
func RenderHintUptake(w io.Writer, report HintUptakeReport) error {
	if len(report.Hints) == 0 {
		fmt.Fprintln(w, "no hint was served in any loaded session.")
		fmt.Fprintln(w, "load a host transcript with `magus session load` first; uptake is counted from what a call served.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "HINT\tSERVED\tFOLLOWED\tREJECTED\tREFLEX\tRATE")
	for _, r := range report.Hints {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%.1f%%\n", r.ID, r.Served, r.Followed, r.Rejected, r.Reflex, r.Rate*100)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nfollowed = the command ran within the next %d calls of the same session.\n", report.Lookahead)
	fmt.Fprintf(w, "a hint under %.0f%% is spending context on advice nobody takes.\n", report.Floor*100)
	return nil
}
