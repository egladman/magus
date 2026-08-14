package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
// It lives in a package rather than at a composition root because it now has two callers -
// `magus notes verify` and the console's NotesService - and the previous arrangement, one
// copy per caller, is exactly how the anchor-to-id mapping silently diverged.
type NoteResolver struct {
	root string
	live map[string]bool
	// node carries the Source ("<path>:<line>") and def_end_line a symbol digest needs; the
	// graph is the only thing that knows where a symbol currently lives, which is the point -
	// the note stores no location at all.
	node map[string]types.KnowledgeNode
	// scope is the store whose notes this resolver is checking. It changes the answer for
	// exactly one anchor kind - note-to-note, which names a note in the SAME store - so a
	// resolver shared across both stores looks a private note's anchor up in the team's
	// namespace and calls it dangling.
	scope string
}

var _ notes.Resolver = NoteResolver{}

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

// Digest fingerprints the anchored source as it is right now.
//
// No failure path invents a change: a caller reads both the empty digest and the error as
// "no opinion" for drift purposes, because a gate that cries wolf out of its own blind spot
// gets ignored and an ignored gate is worse than none. What the error adds is WHICH blind
// spot, since the four causes below are differently actionable and used to be one silence.
func (r NoteResolver) Digest(_ context.Context, a notes.Anchor) (string, error) {
	switch a.Kind {
	case notes.AnchorSymbol:
		n, ok := r.node[r.NodeID(a)]
		if !ok {
			return "", fmt.Errorf("symbol %q is not in the graph; the symbol index may not be built", a.Target)
		}
		path, start, ok := splitSourceLine(n.Source)
		if !ok {
			return "", fmt.Errorf("symbol %q has no source location", a.Target)
		}
		end, err := strconv.Atoi(n.Attrs[AttrDefEndLine])
		if err != nil || end < start {
			// No enclosing range from this indexer: the symbol's extent is unknown, and a
			// guessed one would fingerprint the wrong lines.
			return "", fmt.Errorf("symbol %q has no enclosing range; this indexer did not report one", a.Target)
		}
		src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(path)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		return notes.DigestRange(string(src), start, end), nil
	case notes.AnchorFile:
		src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(a.Target)))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", a.Target, err)
		}
		return notes.Digest(strings.Split(string(src), "\n")), nil
	default:
		// project/target/note have no content of their own to fingerprint - their existence
		// IS the whole signal. Nothing to say, and nothing wrong.
		return "", nil
	}
}

// splitSourceLine parses a node's "<path>:<line>" Source.
func splitSourceLine(src string) (string, int, bool) {
	i := strings.LastIndexByte(src, ':')
	if i < 0 {
		return "", 0, false
	}
	line, err := strconv.Atoi(src[i+1:])
	if err != nil || line < 1 {
		return "", 0, false
	}
	return src[:i], line, true
}
