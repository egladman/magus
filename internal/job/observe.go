package job

import (
	"context"
	"os"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// SymbolReader answers what the knowledge graph knows about a symbol. Supplied by the
// caller rather than reached for here, because internal/job grades evidence and does not
// own a graph; the CLI, the MCP tool and the Buzz binding each already hold one.
type SymbolReader func(ctx context.Context, name string) (SymbolFact, bool)

// CheckpointObserver answers what a job changed, what is in the tree, and what the graph
// says about the symbols its gates named.
//
// Three sources, one for each kind of gate that is not a recorded run: the VCS for the
// diff since the job's checkpoint, the filesystem for what is there now, and symbols
// through the reader the caller supplied. A nil reader leaves the symbol half UNKNOWN
// rather than empty, so a symbol gate refuses instead of passing on a question nobody
// asked.
//
// Every failure resolves the same way: not known. A row with no checkpoint, version
// control disabled, a revision the backend cannot find, all leave the diff unread, and a
// gate that needed it then refuses and says so. That direction is deliberate and is the
// opposite of the guard's fail-open rule: a guard that cannot ask must not refuse a
// person's own command, while a gate that cannot verify must not certify. Refusing costs
// a re-run; certifying on an unread diff is the attestation the gate replaced.
func CheckpointObserver(root string, symbols SymbolReader) Observer {
	return func(ctx context.Context, row types.Job) (Observed, error) {
		seen := Observed{}
		seen.Changed, seen.ChangedFrom, seen.ChangedKnown = changedSince(ctx, root, row.Checkpoint)
		seen.Present, seen.PresentKnown = presentIn(root, row)
		seen.Symbols, seen.SymbolsKnown = readSymbols(ctx, row, symbols)
		return seen, nil
	}
}

// changedSince asks the VCS what differs from the revision half of a checkpoint token.
//
// The token is `<revision>` or `<revision>+<patch digest>`; only the revision half is
// something a VCS can diff against, so the digest is cut. That digest still matters to a
// reader deciding whether the worker saw the same tree, and it is on the row for exactly
// that; it is not a revision, and asking a backend to resolve one would fail.
func changedSince(ctx context.Context, root, checkpoint string) ([]string, string, bool) {
	revision, _, _ := strings.Cut(checkpoint, "+")
	if revision = strings.TrimSpace(revision); revision == "" {
		return nil, "", false
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return nil, revision, false
	}
	changed, err := res.VCS.ChangedFiles(ctx, root, revision)
	if err != nil {
		return nil, revision, false
	}
	return changed, revision, true
}

// presentIn reports which of the paths the row's gates NAME are in the tree.
//
// Only the named ones, never a walk: a gate asks about the paths it declared, and walking
// the tree to answer would read a repository to answer a question about six files.
//
// doublestar, which is the matcher covers(), the guard and the cache all use, and NOT
// filepath.Glob. Under filepath.Glob `**` is one segment, so `db/migrations/**` found
// nothing two levels down and an `absent` gate over it PASSED while the files were there:
// a false certification, which is the one direction this design may never fail in.
func presentIn(root string, row types.Job) ([]string, bool) {
	if root == "" {
		return nil, false
	}
	tree := os.DirFS(root)
	var out []string
	for _, gate := range row.EffectiveCompletionGates() {
		if gate.Kind != types.GateKindPaths {
			continue
		}
		for _, declared := range gate.Paths {
			if declared = strings.TrimSpace(declared); declared == "" {
				continue
			}
			matches, err := doublestar.Glob(tree, declared)
			if err != nil {
				return nil, false
			}
			out = append(out, matches...)
		}
	}
	return out, true
}

// readSymbols resolves every symbol the row's gates named. One miss from the reader leaves
// the whole observation unknown: a graph that answered for three names and failed on the
// fourth cannot be told apart from one that found the fourth absent, and those mean
// opposite things to a gate.
func readSymbols(ctx context.Context, row types.Job, read SymbolReader) (map[string]SymbolFact, bool) {
	if read == nil {
		return nil, false
	}
	out := map[string]SymbolFact{}
	for _, gate := range row.EffectiveCompletionGates() {
		if gate.Kind != types.GateKindSymbol {
			continue
		}
		for _, name := range gate.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			fact, ok := read(ctx, name)
			if !ok {
				return nil, false
			}
			out[name] = fact
		}
	}
	return out, true
}

// SymbolGraph is the slice of the knowledge graph a symbol gate needs.
//
// Declared HERE, as the narrowest interface that answers the question, rather than
// importing the graph package: internal/job grades evidence and has no business knowing
// how a graph is built, and every caller already holds something that satisfies this.
type SymbolGraph interface {
	Refs(ref string) (types.KnowledgeRefsOutput, bool)
}

// GraphSymbols reads symbol facts off a loaded graph. A nil graph reads as UNREADABLE
// rather than as a graph with nothing in it, which is the distinction a symbol gate turns
// on: absent and unknown mean opposite things to a verdict.
func GraphSymbols(g SymbolGraph) SymbolReader {
	return func(_ context.Context, name string) (SymbolFact, bool) {
		if g == nil {
			return SymbolFact{}, false
		}
		out, ok := g.Refs(name)
		if !ok {
			// The graph was readable and holds no such symbol. A real answer, so the
			// reader SUCCEEDS with Defined false; reporting failure here would make a
			// removal that landed indistinguishable from a graph nobody could open.
			return SymbolFact{}, true
		}
		files := make([]string, 0, len(out.Defs))
		for _, d := range out.Defs {
			files = append(files, d.File)
		}
		return SymbolFact{Files: files, Refs: out.RefCount}, true
	}
}
