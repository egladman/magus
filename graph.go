package magus

import (
	"slices"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/wire"
	"github.com/egladman/magus/types"
)

// ComposeOption configures a ComposeGraph call.
type ComposeOption = wire.ComposeOption

// WithGraphInput enables blast-radius enrichment.
func WithGraphInput(g *types.Graph) ComposeOption {
	return func(c *wire.Compose) { c.Graph = g }
}

// WithUpstream switches graph direction to upstream (dependents instead of dependencies).
func WithUpstream() ComposeOption {
	return func(c *wire.Compose) { c.Upstream = true }
}

// WithComposeSpell limits the graph to projects that use the named spell.
func WithComposeSpell(name string) ComposeOption {
	return func(c *wire.Compose) { c.SpellFilter = name }
}

// WithComposeRoots restricts the graph to the listed project paths.
func WithComposeRoots(paths ...string) ComposeOption {
	return func(c *wire.Compose) { c.RootFilter = append(c.RootFilter, paths...) }
}

// WithGraphHistory enables per-node DurationMs prediction in ComposeGraph using
// adaptive CI history for the given target (typically "ci" or "test").
func WithGraphHistory(h *forecast.History, target string) ComposeOption {
	return func(c *wire.Compose) { c.History = h; c.Target = target }
}

// ComposeGraph assembles the structured graph view. Edges to unknown projects are dropped.
func ComposeGraph(ws types.WorkspaceRepository, opts ...ComposeOption) types.GraphOutput {
	cfg := &wire.Compose{}
	for _, o := range opts {
		o(cfg)
	}

	upstream := cfg.Upstream
	spell := cfg.SpellFilter
	rootFilter := cfg.RootFilter

	out := types.GraphOutput{Direction: "downstream"}
	if upstream {
		out.Direction = "upstream"
	}
	if spell != "" {
		out.SpellName = spell
	}

	rootSet := map[string]struct{}{}
	for _, r := range rootFilter {
		rootSet[r] = struct{}{}
	}

	all := ws.All()
	downstream := make(map[string][]string, len(all))
	for _, p := range all {
		var kids []string
		for _, dep := range p.DependsOn {
			if ws.Get(dep) != nil {
				kids = append(kids, dep)
			}
		}
		slices.Sort(kids)
		downstream[p.Path] = kids
	}

	var blastRadius map[string]int
	if cfg.Graph != nil {
		blastRadius = cfg.Graph.BlastRadius()
	}

	for _, p := range all {
		if spell != "" && !slices.Contains(p.Spells, spell) && p.Spell != spell {
			continue
		}
		if len(rootSet) > 0 {
			if _, ok := rootSet[p.Path]; !ok {
				continue
			}
		}

		var kids []string
		if upstream {
			for _, q := range all {
				for _, dep := range downstream[q.Path] {
					if dep == p.Path {
						if spell == "" || q.Spell == spell || slices.Contains(q.Spells, spell) {
							kids = append(kids, q.Path)
						}
						break
					}
				}
			}
		} else {
			for _, dep := range downstream[p.Path] {
				if dep == p.Path {
					continue
				}
				if spell != "" {
					if dp := ws.Get(dep); dp == nil || (dp.Spell != spell && !slices.Contains(dp.Spells, spell)) {
						continue
					}
				}
				kids = append(kids, dep)
			}
		}
		slices.Sort(kids)
		if kids == nil {
			kids = []string{}
		}
		node := types.Node{
			Path:      p.Path,
			SpellName: p.Spell,
			Children:  kids,
			Dir:       p.Dir,
			Exclusive: p.Exclusive,
		}
		if blastRadius != nil {
			node.BlastRadius = blastRadius[p.Path]
		}
		if cfg.History != nil && cfg.Target != "" {
			d := cfg.History.PredictDuration(p.Path, cfg.Target, nil)
			if d > 0 {
				node.DurationMs = d.Milliseconds()
			}
		}
		out.Nodes = append(out.Nodes, node)
	}
	if len(rootFilter) > 0 {
		out.Roots = append(out.Roots, rootFilter...)
	}
	return out
}
