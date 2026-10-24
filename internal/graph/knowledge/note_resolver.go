package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/types"
)

// NoteResolver answers anchor resolution from a knowledge graph's node set.
//
// The dependency runs ONE WAY on purpose: this package learns about notes, and
// internal/notes still knows nothing about the graph. That is what keeps the store
// unit-testable with no graph to build, and it is why the resolver lives here rather than
// beside the store it serves.
//
// It lives in a package rather than at a composition root because it now has two callers
// (`magus notes verify` and the console's NotesService), and the previous arrangement, one
// copy per caller, is exactly how the anchor-to-id mapping silently diverged.
type NoteResolver struct {
	root string
	live map[string]bool
	// node carries the signature a symbol anchor is graded by. The note stores no location,
	// so a symbol that moved within or between files grades the same.
	node map[string]types.KnowledgeNode
	// scope is the store whose notes this resolver is checking. It changes the answer for
	// exactly one anchor kind (note-to-note, which names a note in the SAME store), so a
	// resolver shared across both stores looks a private note's anchor up in the team's
	// namespace and calls it dangling.
	scope string
}

var _ notes.GapResolver = NoteResolver{}

// NewNoteResolver indexes g once. Bind it to a store with ForScope before checking that
// store's notes; the zero scope resolves a note-to-note anchor in the shared namespace,
// which is right for the shared store and wrong for the private one.
func NewNoteResolver(root string, g *Graph) NoteResolver {
	live := map[string]bool{}
	nodes := map[string]types.KnowledgeNode{}
	for _, n := range g.Nodes() {
		live[n.ID] = true
		if n.Kind == types.KindSymbol {
			nodes[n.ID] = n
		}
	}
	return NoteResolver{root: root, live: live, node: nodes, scope: ScopeShared}
}

// ForScope returns a copy bound to one store. The maps are shared by reference and read
// only, so this is cheap enough to do per store rather than indexing the graph twice.
func (r NoteResolver) ForScope(scope string) NoteResolver {
	r.scope = scope
	return r
}

// NodeID renders an anchor as the node ID the graph mints for it, in this resolver's scope.
// Exported because a caller that resolved an anchor usually wants to LINK to the node it
// named, and re-deriving the id at the call site is how the two spellings drifted before.
func (r NoteResolver) NodeID(a notes.Anchor) string {
	return AnchorNodeID(string(a.Kind), a.Target, r.scope)
}

func (r NoteResolver) Resolves(_ context.Context, a notes.Anchor) bool {
	id := r.NodeID(a)
	return id != "" && r.live[id]
}

// Digest fingerprints a file anchor's content as it is right now. Every other kind has
// nothing to hash: a symbol is graded by Signature, and a project, target or note's
// existence IS the whole signal.
func (r NoteResolver) Digest(_ context.Context, a notes.Anchor) (string, error) {
	if a.Kind != notes.AnchorFile {
		return "", nil
	}
	src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(a.Target)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", a.Target, err)
	}
	return notes.Digest(strings.Split(string(src), "\n")), nil
}

// Declaration finds the symbol named name that file defines and returns it as a symbol
// anchor, so a `file#Decl` reference is stored by identity and graded by signature like any
// other symbol. No match and several matches are both errors naming what was looked for:
// picking one of several would be the guess anchors exist to refuse.
func (r NoteResolver) Declaration(_ context.Context, file, name string) (notes.Anchor, error) {
	var ids []string
	for id, n := range r.node {
		path, _, _ := strings.Cut(n.Source, ":")
		if n.Label == name && path == file {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return notes.Anchor{}, fmt.Errorf("%s declares no symbol named %q in the graph, `magus explain file:%s` lists what it defines", file, name, file)
	case 1:
		return notes.Anchor{Kind: notes.AnchorSymbol, Target: strings.TrimPrefix(ids[0], types.KindSymbol+":")}, nil
	default:
		slices.Sort(ids)
		return notes.Anchor{}, fmt.Errorf("%s declares %d symbols named %q, anchor one by id: %s", file, len(ids), name, strings.Join(ids, ", "))
	}
}

// Signature reads a symbol anchor's declaration as its indexer rendered it into the graph.
// A symbol missing from the graph, or one its indexer rendered no signature for, is an error
// naming which, so the anchor reads unverifiable and never clean.
func (r NoteResolver) Signature(_ context.Context, a notes.Anchor) (string, error) {
	if a.Kind != notes.AnchorSymbol {
		return "", fmt.Errorf("%s anchor %q has no signature: only a symbol anchor is graded by one", a.Kind, a.Target)
	}
	n, ok := r.node[r.NodeID(a)]
	if !ok {
		return "", fmt.Errorf("symbol %q is not in the graph, the symbol index may not be built", a.Target)
	}
	sig := n.Attrs[AttrSignature]
	if sig == "" {
		indexer := "its"
		if lang := n.Attrs[attrLanguage]; lang != "" {
			indexer = "the " + lang
		}
		return "", fmt.Errorf("symbol %q has no signature, %s indexer rendered none", a.Target, indexer)
	}
	return sig, nil
}
