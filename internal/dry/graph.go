// Package dry is the in-process, non-executing magus evaluator: it runs Buzz
// source and magusfiles with every host effect (subprocess, filesystem, network)
// replaced by an in-memory tracer, then reports the project graph and the dry-run
// op trace instead of running anything. It backs three surfaces: the WebAssembly
// playground (internal/playground, via cmd/buzz-playground), the `magus buzz` CLI
// subcommand (cmd/magus/buzz.go, via WASMCompatibleMagusModules), and the docs
// generator (cmd/magus-docs, for runnability detection). It carries no build tags
// and none of the heavyweight host stack (proc/cache/http), so it both compiles to
// js/wasm and is unit-testable on the host against the real interpreter.
//
// It is a thin adapter over the real engine, not a parallel implementation: the
// genuinely shared, pure logic is imported, not re-derived - command decoding
// (spell.DecodeCommandValue), charm application (spell.ApplyCharms), ward checks
// (ward.Check), and the magus.* module surface (parity-guarded against
// bindings.MagusModuleKeys). Only the legitimately different pieces live here: the
// tracing host stubs (host effects cannot run in a browser), the lenient spell probe
// (it keeps warded/undecodable ops so `ls`/`graph` still list them, where the
// engine's strict resolve aborts), and the traced-edge topo order.
//
// It lives at internal/dry because it can share a package with neither counterpart:
// the high-level runner is the host-only public API, and the subprocess primitive
// (internal/proc) forks processes - both pull in os/exec, syscall, and platform
// build tags. Compiling to js/wasm means it MUST NOT import internal/proc,
// internal/cache, or the root magus engine; one such import would break that build.
// It stays a leaf over the pure, shared logic (internal/spell, internal/ward, types).
package dry

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
	Sources          []string `json:"sources"`
	Spells           []string `json:"spells"`
	Exclusive        bool     `json:"exclusive"`
	NoCache          []string `json:"noCache"`          // target names opted out of caching (skip_cache)
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

// Op is one traced host operation a target body would have performed, or (for a
// SPELL buffer) one resolved op and its wards.
type Op struct {
	Target string `json:"target"` // target/op whose body emitted it
	Kind   string `json:"kind"`   // "spell" | "exec" | "log" | "run" | "service" | "command" | "ward"
	Name   string `json:"name"`   // op name ("go-build"), argv[0], log level, or MGS code (ward)
	Detail string `json:"detail"` // argv / message / diagnostic / extra context
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
	// invocation), so magus.has_charm(name) reports true under a charm - e.g.
	// `run release:cd` takes the cd branch. Empty for graph/ls.
	charms []string

	// targetKeys is every discovered target's canonical key, set before probing so
	// a magus.needs(glob/regex) can expand its pattern against the real target set
	// (a literal edge needs no set; a pattern does).
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

// depsOf returns the direct dependencies of target (edges from target -> dep).
func (r *Tracer) depsOf(target string) []string {
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
