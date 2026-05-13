package playground

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/spell"
)

const sampleMagusfile = `
import "magus";
import "magus/spell/go";

magus.project.register(".", {
    "spells": [go],
    "outputs": ["bin/**"],
    "targets": {"regen-pgo": {"cachable": false}},
});

export fun format(_args: [str]) > void { go["go-fmt"](); }
export fun lint(_args: [str]) > void { magus.depends_on(["format"]); go["go-vet"](); }
export fun build(_args: [str]) > void { magus.depends_on(["format"]); go["go-build"](); }
export fun ci(_args: [str]) > void { magus.depends_on(["lint", "build"]); }
`

func TestLoadMagusfile_graph(t *testing.T) {
	g := LoadMagusfile(context.Background(), sampleMagusfile)
	if !g.OK {
		t.Fatalf("load failed: %+v", g.Diag)
	}
	if len(g.Projects) != 1 || g.Projects[0].Path != "." {
		t.Fatalf("projects = %+v", g.Projects)
	}
	if len(g.Projects[0].NoCache) != 1 || g.Projects[0].NoCache[0] != "regen-pgo" {
		t.Fatalf("noCache = %+v", g.Projects[0].NoCache)
	}
	if len(g.Projects[0].Spells) != 1 || g.Projects[0].Spells[0] != "go" {
		t.Fatalf("spells = %+v", g.Projects[0].Spells)
	}

	gotTargets := map[string]bool{}
	for _, tg := range g.Targets {
		gotTargets[tg.Key] = true
	}
	for _, want := range []string{"format", "lint", "build", "ci"} {
		if !gotTargets[want] {
			t.Errorf("missing target %q (got %v)", want, gotTargets)
		}
	}

	if !hasEdge(g.Edges, "ci", "lint") || !hasEdge(g.Edges, "ci", "build") ||
		!hasEdge(g.Edges, "lint", "format") || !hasEdge(g.Edges, "build", "format") {
		t.Fatalf("edges = %+v", g.Edges)
	}
}

func TestDryRun_orderAndTrace(t *testing.T) {
	r := DryRun(context.Background(), sampleMagusfile, "ci")
	if !r.OK {
		t.Fatalf("dry-run failed: %+v", r.Diag)
	}
	// format must precede lint and build; everything precedes ci.
	pos := map[string]int{}
	for i, k := range r.Order {
		pos[k] = i
	}
	if !(pos["format"] < pos["lint"] && pos["format"] < pos["build"] &&
		pos["lint"] < pos["ci"] && pos["build"] < pos["ci"]) {
		t.Fatalf("bad order: %v", r.Order)
	}
	// The trace must include the recorded spell ops from the dependencies.
	ops := map[string]bool{}
	for _, op := range r.Trace {
		ops[op.Name] = true
	}
	for _, want := range []string{"go-fmt", "go-vet", "go-build"} {
		if !ops[want] {
			t.Errorf("trace missing op %q (got %v)", want, ops)
		}
	}
}

func TestDryRun_unknownTarget(t *testing.T) {
	r := DryRun(context.Background(), sampleMagusfile, "nope")
	if r.OK || r.Diag == nil {
		t.Fatal("expected an unknown-target diag")
	}
}

// TestManifestMatchesBuiltins gates the hand-written spell manifest against the
// real built-in registry: every spell and op the playground claims must exist.
// (Host-only — the spell package's embedded bytecode never enters the wasm
// build.)
func TestManifestMatchesBuiltins(t *testing.T) {
	builtins := spell.Builtins()
	for name, ops := range builtinSpellOps {
		spec, ok := builtins[name]
		if !ok {
			t.Errorf("manifest spell %q is not a built-in", name)
			continue
		}
		for _, op := range ops {
			if _, ok := spec.Targets[op]; !ok {
				t.Errorf("manifest op %q.%q is not a real target", name, op)
			}
		}
	}
}

func hasEdge(edges []Edge, from, to string) bool {
	for _, e := range edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}
