package main

import (
	"context"

	"github.com/egladman/magus/internal/notes"
)

// anchorHit is one note anchor that names something in the changeset, shaped for the
// preflight report rather than for the store.
type anchorHit struct {
	Note   string           `json:"note"            yaml:"note"`
	Title  string           `json:"title,omitempty" yaml:"title,omitempty"`
	Kind   notes.AnchorKind `json:"kind"            yaml:"kind"`
	Target string           `json:"target"          yaml:"target"`
	// Drift is the notes.IssueCode this anchor resolved to, or empty when it was not graded.
	//
	// Empty means UNGRADED, never clean: grading needs the knowledge graph, and when it
	// will not load the join below still answers WHAT is anchored while claiming nothing
	// about freshness nobody measured.
	Drift string `json:"drift,omitempty" yaml:"drift,omitempty"`
}

// preflightAnchors joins every note anchor against the changeset. Graded through
// ResolveAllAnchors when the knowledge graph loads; when it will not, the same join runs
// over ungraded anchors so a broken index costs the drift column, never the section.
func preflightAnchors(ctx context.Context, root string, files, symbols []string) []anchorHit {
	stores, err := notesStores(root, "")
	if err != nil {
		return nil
	}
	res, resErr := notesResolver(ctx, root)

	var resolved []notes.ResolvedAnchor
	for _, st := range stores {
		if resErr == nil {
			ra, raErr := notes.ResolveAllAnchors(ctx, st.dir, res.ForScope(string(st.scope)))
			if raErr == nil {
				resolved = append(resolved, ra...)
				continue
			}
		}
		found, _, ierr := notes.Inspect(st.dir)
		if ierr != nil {
			continue
		}
		for _, n := range found {
			for i, a := range n.Anchors {
				file := ""
				if a.Kind == notes.AnchorFile {
					file = a.Target
				}
				resolved = append(resolved, notes.ResolvedAnchor{
					Note: n.Name, Title: n.Title, Pos: i, Anchor: a, File: file,
				})
			}
		}
	}

	hits := notes.AnchorsTouching(resolved, files, symbols)
	out := make([]anchorHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, anchorHit{
			Note: h.Note, Title: h.Title, Kind: h.Kind, Target: h.Target, Drift: string(h.Status),
		})
	}
	return out
}
