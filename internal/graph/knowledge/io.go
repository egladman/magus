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

	link := func(targetNode, projectPath, relation string, globs []string) {
		for _, g := range globs {
			pat := path.Join(projectPath, g) // project-relative -> workspace-relative
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
				s.Edges = append(s.Edges, extractedEdge(targetNode, pathToNode[p], relation, projectPath))
			}
		}
	}

	for _, p := range projects {
		for _, n := range p.Nodes {
			tID := targetID(p.Path, n.Name)
			link(tID, p.Path, types.RelationProduces, n.Outputs)
			link(tID, p.Path, types.RelationConsumes, n.Inputs)
		}
	}
	return s
}
