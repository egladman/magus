package main

import (
	"context"

	"github.com/egladman/magus/internal/notes"
)

// anchorHit is one note anchor that names something in the changeset, shaped for the
// impact report rather than for the store.
type anchorHit struct {
	Note   string           `json:"note"            yaml:"note"`
	Title  string           `json:"title,omitempty" yaml:"title,omitempty"`
	Kind   notes.AnchorKind `json:"kind"            yaml:"kind"`
	Target string           `json:"target"          yaml:"target"`
	// Drift is the notes.IssueCode this anchor resolved to. Empty is graded CLEAN;
	// notes.StatusUngraded is unmeasured, which grading needs the knowledge graph for and
	// cannot report when the graph will not load. The two are distinct so a renderer cannot
	// show an anchor nobody checked as fresh.
	Drift string `json:"drift,omitempty" yaml:"drift,omitempty"`
}

// impactAnchors joins every note anchor against the changeset. A knowledge graph that will
// not load costs the drift column and nothing else: notes.ResolveAnchors takes a nil resolver
// and grades every anchor ungraded, so the section still answers WHAT is anchored.
func impactAnchors(ctx context.Context, root string, files, symbols []string) []anchorHit {
	stores, err := notesStores(root, "")
	if err != nil {
		return nil
	}
	res, resErr := notesResolver(ctx, root)

	var resolved []notes.ResolvedAnchor
	for _, st := range stores {
		var scoped notes.Resolver
		if resErr == nil {
			scoped = res.ForScope(string(st.scope))
		}
		ra, raErr := notes.ResolveAnchors(ctx, st.dir, scoped)
		if raErr != nil {
			continue
		}
		resolved = append(resolved, ra...)
	}

	hits := notes.AnchorHits(resolved, files, symbols)
	out := make([]anchorHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, anchorHit{
			Note: h.Note, Title: h.Title, Kind: h.Kind, Target: h.Target, Drift: string(h.Status),
		})
	}
	return out
}
