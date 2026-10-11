package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/types"
)

// AnchorHit is one note anchor that names something in a changeset, shaped for a report rather
// than for the store.
type AnchorHit struct {
	Note  string `json:"note"            yaml:"note"`
	Title string `json:"title,omitempty" yaml:"title,omitempty"`
	// Pos is the anchor's index in its note, and Matched the changed thing that pulled the
	// note in: a symbol node id or a file path. Both are carried rather than dropped because
	// a note with several anchors is otherwise reported without saying WHICH of them fired.
	Pos     int              `json:"pos"             yaml:"pos"`
	Kind    notes.AnchorKind `json:"kind"            yaml:"kind"`
	Target  string           `json:"target"          yaml:"target"`
	Matched string           `json:"matched"         yaml:"matched"`
	// Match is notes.MatchStrength: how the hit was found. It restates Kind for every hit this
	// caller can currently produce, and stops doing so once one supplies a symbol anchor's file
	// - the weak neighbor match must never render with the exact match's authority.
	Match string `json:"match" yaml:"match"`
	// Drift is the notes.IssueCode this anchor resolved to. Empty is graded CLEAN;
	// notes.StatusUngraded is unmeasured, which grading needs the knowledge graph for and
	// cannot report when the graph will not load. The two are distinct so a renderer cannot
	// show an anchor nobody checked as fresh.
	Drift string `json:"drift,omitempty" yaml:"drift,omitempty"`
}

// Line renders the hit as one clause: which note anchors what, and the drift verdict when the
// anchor has one.
//
// An unmeasured anchor is marked too, and deliberately not with its wire code: rendered as
// "ungraded-anchor" it scans as a fourth kind of drift verdict, when what it says is that no
// verdict was reached. Bare would be worse: that reads as clean, which is the one thing nobody
// checked it for.
func (h AnchorHit) Line() string {
	line := fmt.Sprintf("note %s anchors %s:%s", h.Note, h.Kind, h.Target)
	if h.Drift == "" {
		return line
	}
	marker := h.Drift
	if h.Drift == string(notes.StatusUngraded) {
		marker = "ungraded"
	}
	return line + " [" + marker + "]"
}

// NoteDirs are the notes locations a workspace declares, as written in its config. An empty
// field is a store the workspace does not declare.
type NoteDirs struct {
	Shared, Private string
}

// NoteStore is one declared notes store: where it lives and what putting a note there means.
type NoteStore struct {
	Dir   string
	Scope notes.Scope
}

// NoteStores resolves the notes stores declared in dirs, shared first. A store the workspace does
// not declare is skipped, and a misdeclared one is an error.
//
// Declaring neither is the default rather than a fault, so it yields no stores and no error.
func NoteStores(workspaceRoot string, dirs NoteDirs) ([]NoteStore, error) {
	var out []NoteStore
	for _, s := range []struct {
		scope    notes.Scope
		declared string
	}{{notes.ScopeShared, dirs.Shared}, {notes.ScopePrivate, dirs.Private}} {
		dir, err := notes.Dir(workspaceRoot, s.scope, s.declared)
		if errors.Is(err, notes.ErrDisabled) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, NoteStore{Dir: dir, Scope: s.scope})
	}
	return out, nil
}

// ChangesetAnchors joins the declared notes stores against everything rev changed.
//
// A misdeclared store is returned as an error rather than as no anchors: the section would
// otherwise read as a clean tree that was never checked. loadGraph supplies the graph the
// anchors are graded against and is called only when a store is declared; it must load the
// symbol shards, because without them every symbol anchor would report as dangling. A graph
// that will not load costs the drift column, and a workspace declaring no store costs the
// section; the report around either still prints.
func ChangesetAnchors(ctx context.Context, workspaceRoot string, dirs NoteDirs, loadGraph func(context.Context) (*knowledge.Graph, error), rev types.Diff) ([]AnchorHit, error) {
	stores, err := NoteStores(workspaceRoot, dirs)
	if err != nil {
		return nil, fmt.Errorf("note anchors: %w", err)
	}
	if len(stores) == 0 {
		return nil, nil
	}
	g, err := loadGraph(ctx)
	if err != nil {
		g = nil
	}
	return joinAnchors(ctx, workspaceRoot, stores, g, ChangedPaths(rev), changedSymbolIDs(rev)), nil
}

// joinAnchors joins every note anchor in stores against the changed files and symbols.
//
// g is the knowledge graph the anchors are graded against; nil costs the drift column and
// nothing else, since notes.ResolveAnchors grades every anchor ungraded without a resolver, so
// the answer to WHAT is anchored survives a graph that would not load. A store that cannot be
// read contributes nothing.
func joinAnchors(ctx context.Context, workspaceRoot string, stores []NoteStore, g *knowledge.Graph, files, symbols []string) []AnchorHit {
	var resolver *knowledge.NoteResolver
	if g != nil {
		r := knowledge.NewNoteResolver(workspaceRoot, g)
		resolver = &r
	}

	var resolved []notes.ResolvedAnchor
	for _, st := range stores {
		var scoped notes.Resolver
		if resolver != nil {
			scoped = resolver.ForScope(string(st.Scope))
		}
		ra, err := notes.ResolveAnchors(ctx, st.Dir, scoped)
		if err != nil {
			continue
		}
		resolved = append(resolved, stampAnchorNodeIDs(ra, string(st.Scope))...)
	}

	hits := notes.AnchorHits(resolved, files, symbols)
	out := make([]AnchorHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, AnchorHit{
			Note: h.Note, Title: h.Title, Pos: h.Pos, Kind: h.Kind, Target: h.Target,
			Matched: h.Matched, Match: string(h.Match), Drift: string(h.Status),
		})
	}
	return out
}

// stampAnchorNodeIDs mints each anchor's graph node id in place and returns the same slice.
//
// This is the only layer allowed to know both vocabularies: internal/notes must not learn the
// graph (see its Resolver doc), and the graph mints ids from one place so two hand-kept copies
// cannot diverge (knowledge.AnchorNodeID). Without it a symbol anchor is compared bare against
// a node id and never matches, which is the join's headline case.
//
// scope is the ANCHORING store's, because a note-to-note anchor names a note in the same store.
func stampAnchorNodeIDs(res []notes.ResolvedAnchor, scope string) []notes.ResolvedAnchor {
	for i := range res {
		res[i].NodeID = knowledge.AnchorNodeID(string(res[i].Anchor.Kind), res[i].Anchor.Target, scope)
	}
	return res
}

// ChangedPaths is the changed file set, generated files included: a note may anchor one.
func ChangedPaths(rev types.Diff) []string {
	out := make([]string, 0, len(rev.Files))
	for _, f := range rev.Files {
		out = append(out, f.Path)
	}
	return out
}

// changedSymbolIDs is every changed symbol's index id, which is what a symbol anchor names.
func changedSymbolIDs(rev types.Diff) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range rev.Files {
		for _, s := range f.Symbols {
			if s.ID != "" && !seen[s.ID] {
				seen[s.ID] = true
				out = append(out, s.ID)
			}
		}
	}
	return out
}
