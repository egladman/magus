package dry

import (
	"bytes"
	"slices"
	"strings"
)

// Project is one magus.project(...) call, flattened to the fields the
// playground surfaces.
type Project struct {
	Path      string   `json:"path"`
	DependsOn []string `json:"dependsOn"`
	Outputs   []string `json:"outputs"`
	Sources   []string `json:"sources"`
	Spells    []string `json:"spells"`
	NoCache   []string `json:"noCache"` // target names opted out of caching (skip_cache)
	Slots     []string `json:"slots"`   // "name=N" for targets that hold N concurrency slots
}

// Target is an exported function discovered as a runnable target.
type Target struct {
	Key  string `json:"key"`  // canonical kebab-case name (regen_pgo -> regen-pgo)
	Name string `json:"name"` // source export name
}

// Edge is a depends_on relationship: From depends on To.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Op is one traced host operation a target body would have performed, or (for a
// SPELL buffer) one resolved op and its wards.
type Op struct {
	Target string `json:"target"` // target/op whose body emitted it
	Kind   string `json:"kind"`   // "spell" | "exec" | "log" | "run" | "service" | "command" | "ward"
	Name   string `json:"name"`   // op name ("go-build"), argv[0], log level, or MGS code (ward)
	Detail string `json:"detail"` // argv / message / diagnostic / extra context
	// Argv is an exec op's argv as passed, unjoined, so a caller can match tokens
	// that hold spaces.
	Argv []string `json:"argv,omitempty"`
}

// Tracer accumulates the side effects of evaluating a magusfile under the
// stub host. It is the single sink the magus/spell stubs write to.
type Tracer struct {
	out         bytes.Buffer
	projects    []Project
	edges       []Edge
	opsByTarget map[string][]Op

	// cur is the target whose body is currently executing, so depends_on edges
	// and host ops attribute to the right node. Empty at top level.
	cur string

	// charms is the evaluation's active charm set (from a `run t:charm`
	// invocation), so magus.has_charm(name) reports true under a charm, e.g.
	// `run release:cd` takes the cd branch. Empty for graph/ls.
	charms []string

	// targetKeys is every discovered target's canonical key, set before probing so
	// a magus.glob can expand its pattern against the real target set
	// (an exact needs edge needs no set; a pattern does).
	targetKeys []string
}

func newTracer() *Tracer {
	return &Tracer{opsByTarget: map[string][]Op{}}
}

func (r *Tracer) addOp(kind, name, detail string) {
	if r.cur == "" {
		return // an op at top level (not inside a target) has nowhere to attribute
	}
	r.opsByTarget[r.cur] = append(r.opsByTarget[r.cur], Op{
		Target: r.cur, Kind: kind, Name: name, Detail: detail,
	})
}

func (r *Tracer) addExec(argv []string) {
	if r.cur == "" {
		return
	}
	r.opsByTarget[r.cur] = append(r.opsByTarget[r.cur], Op{
		Target: r.cur, Kind: "exec", Name: argv[0], Detail: strings.Join(argv[1:], " "), Argv: argv,
	})
}

func (r *Tracer) addEdge(to string) {
	if r.cur == "" || to == "" || to == r.cur {
		return
	}
	for _, e := range r.edges {
		if e.From == r.cur && e.To == to {
			return // dedupe
		}
	}
	r.edges = append(r.edges, Edge{From: r.cur, To: to})
}

// directDeps returns the direct dependencies of target (edges from target -> dep).
func (r *Tracer) directDeps(target string) []string {
	var out []string
	for _, e := range r.edges {
		if e.From == target {
			out = append(out, e.To)
		}
	}
	return out
}

// topoOrder returns the dependency closure of target in run order (deps before
// dependents), de-duplicated and cycle-guarded. target itself is last.
func (r *Tracer) topoOrder(target string) []string {
	var order []string
	seen := map[string]bool{}
	onStack := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if seen[n] || onStack[n] {
			return // already placed, or a cycle: stop without re-adding
		}
		onStack[n] = true
		deps := r.directDeps(n)
		slices.Sort(deps) // deterministic order among siblings
		for _, d := range deps {
			visit(d)
		}
		onStack[n] = false
		seen[n] = true
		order = append(order, n)
	}
	visit(target)
	return order
}
