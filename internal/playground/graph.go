// Package playground evaluates Buzz source and magusfiles in-process for the
// WebAssembly playground, with every host effect (subprocess, filesystem,
// network) replaced by an in-memory recorder. It is deliberately free of build
// tags and of the heavyweight magus host stack (proc/cache/http), so it both
// compiles to js/wasm and is unit-testable on the host against the real
// interpreter.
//
// A browser sandbox cannot fork processes or touch a filesystem, so a magusfile
// is never executed for real here. Instead it is evaluated to the project graph
// it builds (magus.project + exported target functions + depends_on
// edges), and a target is "dry-run" to the ordered trace of host operations it
// would invoke — mirroring `magus ls` / `magus run --dry-run`, never the tools.
package playground

import (
	"bytes"
	"slices"
)

// Project is one magus.project(...) call, flattened to the fields the
// playground surfaces.
type Project struct {
	Path             string   `json:"path"`
	DependsOn        []string `json:"dependsOn"`
	Outputs          []string `json:"outputs"`
	Spells           []string `json:"spells"`
	Exclusive        bool     `json:"exclusive"`
	NoCache          []string `json:"noCache"`          // target names opted out of caching (skipCache)
	ExclusiveTargets []string `json:"exclusiveTargets"` // target names that run alone (exclusive=true)
	Slots            []string `json:"slots"`            // "name=N" for targets that hold N concurrency slots
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

// Op is one recorded host operation a target body would have performed, or (for a
// SPELL buffer) one resolved op and its wards.
type Op struct {
	Target string `json:"target"` // target/op whose body emitted it
	Kind   string `json:"kind"`   // "spell" | "exec" | "log" | "run" | "service" | "command" | "ward"
	Name   string `json:"name"`   // op name ("go-build"), argv[0], log level, or MGS code (ward)
	Detail string `json:"detail"` // argv / message / diagnostic / extra context
}

// Recorder accumulates the side effects of evaluating a magusfile under the
// stub host. It is the single sink the magus/extra/spell stubs write to.
type Recorder struct {
	out         bytes.Buffer
	projects    []Project
	edges       []Edge
	opsByTarget map[string][]Op

	// cur is the target whose body is currently executing, so depends_on edges
	// and host ops attribute to the right node. Empty at top level.
	cur string

	// charms is the active charm set for this evaluation (from a `run t:charm`
	// invocation), so magus.has_charm(name) reports true for a dry-run under a
	// charm — e.g. `run release:cd` takes the cd branch. Empty for graph/ls.
	charms []string

	// targetKeys is every discovered target's canonical key, set before probing so
	// a magus.needs(glob/regex) can expand its pattern against the real target set
	// (a literal edge needs no set; a pattern does).
	targetKeys []string
}

func newRecorder() *Recorder {
	return &Recorder{opsByTarget: map[string][]Op{}}
}

func (r *Recorder) addOp(kind, name, detail string) {
	if r.cur == "" {
		return // an op at top level (not inside a target) has nowhere to attribute
	}
	r.opsByTarget[r.cur] = append(r.opsByTarget[r.cur], Op{
		Target: r.cur, Kind: kind, Name: name, Detail: detail,
	})
}

func (r *Recorder) addEdge(to string) {
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

// depsOf returns the direct dependencies of target (edges from target -> dep).
func (r *Recorder) depsOf(target string) []string {
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
func (r *Recorder) topoOrder(target string) []string {
	var order []string
	seen := map[string]bool{}
	onStack := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if seen[n] || onStack[n] {
			return // already placed, or a cycle — stop without re-adding
		}
		onStack[n] = true
		deps := r.depsOf(n)
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
