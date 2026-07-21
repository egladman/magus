package knowledge

import (
	"path"
	"slices"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/types"
)

// IOShardName holds the produces/consumes edges tying a target to the file and doc
// nodes its declared magus.outputs / magus.inputs match. Deterministic (a static read
// of the magusfile), so it is remote-shareable like the other extracted shards.
const IOShardName = "@io"

// maxIOFanout caps how many nodes a single output/input glob may link. A declaration
// broad enough to exceed it (a stray `**/*.go` in outputs) would turn the target into a
// god node and mislead more than it informs, so the whole glob is dropped rather than
// fanned out. Explicit magus.outputs/inputs are specific in practice, so this only ever
// guards against a pathological declaration.
const maxIOFanout = 40

// assembleIO turns each target's declared magus.outputs / magus.inputs globs into
// `produces` / `consumes` edges to the EXISTING file and doc nodes they match. The globs
// are project-relative literals (as written in the body), so each is resolved against its
// project's path before matching the workspace-relative node paths. Only existing nodes
// are linked: an output glob with no matching node (a path magus does not model, or a
// not-yet-produced file) contributes no edge, never a phantom. This is the seam that
// self-labels a generated file - `docs/spells/go.md` gains an incoming `produces` edge
// from content_generate, so the doc layer needs no separate "is it generated" test.
func assembleIO(projects []types.TargetGraphProject, pathToNode map[string]string) Shard {
	s := Shard{Name: IOShardName}
	paths := make([]string, 0, len(pathToNode))
	for p := range pathToNode {
		paths = append(paths, p)
	}
	slices.Sort(paths) // deterministic edge order

	// linkPat links a target to every existing node an already-workspace-relative
	// pattern matches. provenance attributes the edge to the consuming/producing
	// project. Shared by outputs (project-relative globs joined to the project path
	// first) and inputs (each already workspace-relative via its owning project).
	linkPat := func(targetNode, provenance, relation string, pats []string) {
		for _, pat := range pats {
			var matched []string
			for _, p := range paths {
				if ok, _ := doublestar.Match(pat, p); ok {
					matched = append(matched, p)
				}
			}
			// No match, or too broad to be informative: contribute nothing rather than
			// a phantom edge or a god-node fan-out.
			if len(matched) == 0 || len(matched) > maxIOFanout {
				continue
			}
			for _, p := range matched {
				s.Edges = append(s.Edges, extractedEdge(targetNode, pathToNode[p], relation, provenance))
			}
		}
	}
	for _, p := range projects {
		for _, n := range p.Nodes {
			tID := targetID(p.Path, n.Name)
			// Outputs: project-relative globs joined to the project path (workspace-relative).
			outPats := make([]string, len(n.Outputs))
			for i, g := range n.Outputs {
				outPats[i] = path.Join(p.Path, g)
			}
			linkPat(tID, p.Path, types.RelationProduces, outPats)
			// Every input carries its OWNING project's workspace-relative path (resolved
			// in DescribeGraph): a same-project input's owner is this project, a
			// cross-project input's is the other one. path.Join(Project, Glob) yields the
			// workspace-relative file path uniformly, matched against the file node in the
			// owning project directly - never re-anchored to the consumer's path.
			if len(n.Inputs) > 0 {
				pats := make([]string, len(n.Inputs))
				for i, ref := range n.Inputs {
					pats[i] = path.Join(ref.Project, ref.Glob)
				}
				linkPat(tID, p.Path, types.RelationConsumes, pats)
			}
		}
	}
	return s
}
