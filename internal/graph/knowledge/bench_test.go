package knowledge

import (
	"context"
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
)

// syntheticInputs builds a large-monorepo fixture: nProjects projects each with
// targetsPerProject targets (an intra-project dependency chain, a spell-op use per
// target, a charm on every fifth), a project dependency chain, plus a registry of
// spells/modules/diagnostics. nProjects=2000, targetsPerProject=8 ~= 16k targets.
func syntheticInputs(nProjects, targetsPerProject int) Inputs {
	projects := make([]types.TargetGraphProject, nProjects)
	for p := range projects {
		path := fmt.Sprintf("pkg/p%05d", p)
		nodes := make([]types.TargetGraphNode, targetsPerProject)
		for tIdx := range nodes {
			n := types.TargetGraphNode{
				Name:   fmt.Sprintf("t%03d", tIdx),
				Doc:    "A synthetic target for benchmarking.",
				Spells: []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}},
			}
			if tIdx > 0 {
				n.Dependencies = []string{fmt.Sprintf("t%03d", tIdx-1)}
			}
			if tIdx%5 == 0 {
				n.Charms = []string{"rw"}
			}
			nodes[tIdx] = n
		}
		pr := types.TargetGraphProject{Path: path, Engine: "buzz", Nodes: nodes}
		if p > 0 {
			pr.DependsOn = []string{fmt.Sprintf("pkg/p%05d", p-1)}
		}
		projects[p] = pr
	}

	spells := make([]types.SpellEntry, 20)
	for s := range spells {
		spells[s] = types.SpellEntry{
			Name:    fmt.Sprintf("spell%02d", s),
			Targets: []string{"build", "test", "lint", "format"},
		}
	}
	spells[0].Name = "go" // matched by every target's spell-op use

	modules := make([]types.ModuleEntry, 15)
	for m := range modules {
		methods := make([]types.ModuleMethodEntry, 10)
		for me := range methods {
			methods[me] = types.ModuleMethodEntry{Name: fmt.Sprintf("m%02d", me), Doc: "method", Buzz: "sig()"}
		}
		modules[m] = types.ModuleEntry{Name: fmt.Sprintf("mod%02d", m), Doc: "module", Methods: methods}
	}

	return Inputs{
		Graph:       types.TargetGraphOutput{Projects: projects},
		Spells:      types.SpellsOutput{Spells: spells},
		Modules:     modules,
		Diagnostics: types.AllDiagnosticCodes(),
	}
}

const (
	benchProjects = 2000
	benchTargets  = 8
)

func BenchmarkAssembleShards(b *testing.B) {
	in := syntheticInputs(benchProjects, benchTargets)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AssembleShards(in)
	}
}

func BenchmarkMergeOutput(b *testing.B) {
	shards := AssembleShards(syntheticInputs(benchProjects, benchTargets))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mergeAll(shards).Output()
	}
}

// BenchmarkBuildNoop is the steady-state cost every query pays: assemble +
// fingerprint every shard + reconcile against an up-to-date store (nothing to
// write except - today - the manifest).
func BenchmarkBuildNoop(b *testing.B) {
	in := syntheticInputs(benchProjects, benchTargets)
	cacheDir := b.TempDir()
	ctx := context.Background()
	if _, err := Build(ctx, cacheDir, BuildOptions{}, in, nil); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Build(ctx, cacheDir, BuildOptions{}, in, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBuildCold pays the full first-build cost (assemble + fingerprint +
// write every shard + manifest) into a fresh store each iteration.
func BenchmarkBuildCold(b *testing.B) {
	in := syntheticInputs(benchProjects, benchTargets)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cacheDir := b.TempDir()
		b.StartTimer()
		if _, err := Build(ctx, cacheDir, BuildOptions{}, in, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolve(b *testing.B) {
	g := mergeAll(AssembleShards(syntheticInputs(benchProjects, benchTargets)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.Resolve("kind:target t003", 50)
	}
}

func BenchmarkQueryNeighborhood(b *testing.B) {
	g := mergeAll(AssembleShards(syntheticInputs(benchProjects, benchTargets)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.Query("t003", 50)
	}
}
