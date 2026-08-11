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

	spells := make([]types.Spell, 20)
	for s := range spells {
		spells[s] = types.Spell{
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
		Spells:      spells,
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

// syntheticSymbols builds a symbol-ingest fixture. It is deliberately separate from
// syntheticInputs: that fixture is shared by every other benchmark here and by the store
// and scale tests, so growing it would silently invalidate benchstat comparisons against
// every baseline already recorded against it.
func syntheticSymbols(nSymbols, callsPerSymbol int) []types.KnowledgeSymbol {
	out := make([]types.KnowledgeSymbol, nSymbols)
	for i := range out {
		file := fmt.Sprintf("pkg/p%05d/f.go", i%64)
		calls := make([]types.KnowledgeSymbolCall, callsPerSymbol)
		for c := range calls {
			calls[c] = types.KnowledgeSymbolCall{Key: fmt.Sprintf("gomod example.com/x Sym%05d().", (i+c+1)%nSymbols), Count: c + 1}
		}
		out[i] = types.KnowledgeSymbol{
			Key:      fmt.Sprintf("gomod example.com/x Sym%05d().", i),
			Label:    fmt.Sprintf("Sym%05d", i),
			Language: "go",
			Source:   fmt.Sprintf("%s:%d", file, i),
			Defs:     []string{file},
			Refs:     []types.KnowledgeSymbolRef{{Path: file, Count: 3, Lines: []int{1, 2, 3}}},
			Calls:    calls,
		}
	}
	return out
}

// BenchmarkAssembleSymbols isolates what call edges add to shard assembly: calls=0 is the
// pre-change cost, calls=16 an order of magnitude past what real indexes produce (the
// repo's own index averages under one call edge per symbol).
func BenchmarkAssembleSymbols(b *testing.B) {
	projects := []types.TargetGraphProject{{Path: "pkg/p00000"}}
	for _, calls := range []int{0, 16} {
		syms := syntheticSymbols(4000, calls)
		b.Run(fmt.Sprintf("calls=%d", calls), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = assembleSymbols("pkg/p00000", syms, projects)
			}
		})
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
