package impact

import (
	"context"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// fakeWorkspace implements just the types.WorkspaceRepository surface the impact
// engine touches (Affected, AffectedFromPaths, Get, ListTargets). Embedding the
// interface leaves every other method nil - the engine never calls them.
type fakeWorkspace struct {
	types.WorkspaceRepository
	affected *types.AffectedResult
	projects map[string]*types.Project
	targets  []types.TargetEntry
}

func (f *fakeWorkspace) Affected(context.Context, string) (*types.AffectedResult, error) {
	return f.affected, nil
}

func (f *fakeWorkspace) AffectedFromPaths(context.Context, []string) (*types.AffectedResult, error) {
	return f.affected, nil
}

func (f *fakeWorkspace) Get(path string) *types.Project { return f.projects[path] }

func (f *fakeWorkspace) ListTargets(context.Context) ([]types.TargetEntry, error) {
	return f.targets, nil
}

func TestCompute(t *testing.T) {
	goSpell := spells.NewSpell("go", spells.WithTargets("go-build", "go-test", "go-vet"))
	tsSpell := spells.NewSpell("ts", spells.WithTargets("biome-check"))

	tests := []struct {
		name     string
		affected *types.AffectedResult
		projects map[string]*types.Project
		targets  []types.TargetEntry
		want     *types.ImpactResult
	}{
		{
			name: "no changed files",
			affected: &types.AffectedResult{
				Base: "origin/main",
			},
			want: &types.ImpactResult{
				Base:             "origin/main",
				ChangedFileCount: 0,
			},
		},
		{
			name: "seed plus transitive dependent, spell and custom targets",
			affected: &types.AffectedResult{
				Base:        "origin/main",
				Changed:     []string{"api/main.go", "api/util.go", "docs/README.md"},
				Seed:        []string{"api"},
				FilesBySeed: map[string][]string{"api": {"api/util.go", "api/main.go"}},
				Affected:    []string{"api", "web"},
			},
			projects: map[string]*types.Project{
				"api": {Path: "api", Spells: []string{"go"}, ResolvedSpells: []*spells.Spell{goSpell}},
				"web": {Path: "web", Spells: []string{"ts"}, ResolvedSpells: []*spells.Spell{tsSpell}},
			},
			targets: []types.TargetEntry{
				{Name: "ci", Kind: "canonical"},
				{Name: "test", Kind: "custom", Projects: []string{"api"}},
				{Name: "build", Kind: "custom", Projects: []string{"api", "web"}},
				{Name: "go-test", Kind: "spell"}, // spell entries carry no Projects; ignored here
			},
			want: &types.ImpactResult{
				Base:             "origin/main",
				ChangedFileCount: 3,
				// Changed is sorted and includes files outside any project.
				ChangedFiles: []string{"api/main.go", "api/util.go", "docs/README.md"},
				SeedProjects: []string{"api"},
				AffectedProjects: []types.ImpactProject{
					{
						Path:    "api",
						Seed:    true,
						Files:   []string{"api/main.go", "api/util.go"},
						Spells:  []string{"go"},
						Targets: []string{"build", "go-build", "go-test", "go-vet", "test"},
					},
					{
						Path:    "web",
						Seed:    false,
						Spells:  []string{"ts"},
						Targets: []string{"biome-check", "build"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := &fakeWorkspace{affected: tt.affected, projects: tt.projects, targets: tt.targets}
			got, err := Compute(context.Background(), ws, "origin/main")
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestComputeFromPaths(t *testing.T) {
	ws := &fakeWorkspace{
		affected: &types.AffectedResult{
			Base:        "paths",
			Changed:     []string{"api/main.go"},
			Seed:        []string{"api"},
			FilesBySeed: map[string][]string{"api": {"api/main.go"}},
			Affected:    []string{"api"},
		},
		projects: map[string]*types.Project{"api": {Path: "api"}},
	}
	got, err := ComputeFromPaths(context.Background(), ws, []string{"api/main.go"})
	require.NoError(t, err)
	require.Equal(t, &types.ImpactResult{
		Base:             "paths",
		ChangedFileCount: 1,
		ChangedFiles:     []string{"api/main.go"},
		SeedProjects:     []string{"api"},
		AffectedProjects: []types.ImpactProject{{Path: "api", Seed: true, Files: []string{"api/main.go"}}},
	}, got)
}

// fakeSymbolStore is a canned SymbolStore for exercising Enrich without a real graph.
type fakeSymbolStore struct {
	hasSymbols bool
	facts      map[string]FileFacts
}

func (f *fakeSymbolStore) HasSymbols() bool { return f.hasSymbols }

func (f *fakeSymbolStore) FileFacts(relPath string) FileFacts { return f.facts[relPath] }

func TestEnrich(t *testing.T) {
	tests := []struct {
		name  string
		res   *types.ImpactResult
		store SymbolStore
		want  *types.ImpactResult
	}{
		{
			name:  "no symbol index: single note, no overlay fields",
			res:   &types.ImpactResult{Base: "origin/main", ChangedFiles: []string{"a.go"}},
			store: &fakeSymbolStore{hasSymbols: false},
			want: &types.ImpactResult{
				Base:         "origin/main",
				ChangedFiles: []string{"a.go"},
				Notes: []string{
					"no symbol index loaded: changed-symbol callers and coverage overlays are unavailable (build it with `magus graph build`)",
				},
			},
		},
		{
			name: "callers and coverage: symbols sorted by descending refs",
			res:  &types.ImpactResult{Base: "origin/main", ChangedFiles: []string{"a.go", "b.go"}},
			store: &fakeSymbolStore{
				hasSymbols: true,
				facts: map[string]FileFacts{
					"a.go": {
						Coverage: &Coverage{Ratio: 0.5, Covered: 5, Total: 10},
						Symbols: []SymbolFacts{
							{ID: "symbol:a.Foo", Label: "Foo", RefCount: 3, FileCount: 2},
							{ID: "symbol:a.Bar", Label: "Bar", RefCount: 10, FileCount: 4, Coverage: &Coverage{Ratio: 1, Covered: 4, Total: 4}},
						},
					},
					"b.go": {}, // a changed file with no indexed symbol contributes nothing
				},
			},
			want: &types.ImpactResult{
				Base:         "origin/main",
				ChangedFiles: []string{"a.go", "b.go"},
				ChangedSymbols: []types.ImpactSymbol{
					{File: "a.go", Symbol: "symbol:a.Bar", Label: "Bar", RefCount: 10, FileCount: 4, Coverage: types.ImpactCoverage{Ratio: 1, Covered: 4, Total: 4}},
					{File: "a.go", Symbol: "symbol:a.Foo", Label: "Foo", RefCount: 3, FileCount: 2},
				},
				ChangedFileCoverage: []types.ImpactFileCoverage{
					{File: "a.go", Coverage: types.ImpactCoverage{Ratio: 0.5, Covered: 5, Total: 10}},
				},
			},
		},
		{
			name: "symbols but no coverage: coverage note only",
			res:  &types.ImpactResult{Base: "origin/main", ChangedFiles: []string{"a.go"}},
			store: &fakeSymbolStore{
				hasSymbols: true,
				facts: map[string]FileFacts{
					"a.go": {Symbols: []SymbolFacts{{ID: "symbol:a.Foo", Label: "Foo", RefCount: 1, FileCount: 1}}},
				},
			},
			want: &types.ImpactResult{
				Base:         "origin/main",
				ChangedFiles: []string{"a.go"},
				ChangedSymbols: []types.ImpactSymbol{
					{File: "a.go", Symbol: "symbol:a.Foo", Label: "Foo", RefCount: 1, FileCount: 1},
				},
				Notes: []string{"no coverage data on changed files (run `magus run coverage` to populate it)"},
			},
		},
		{
			name:  "index loaded but no changed file defines a symbol: two notes",
			res:   &types.ImpactResult{Base: "origin/main", ChangedFiles: []string{"README.md"}},
			store: &fakeSymbolStore{hasSymbols: true, facts: map[string]FileFacts{}},
			want: &types.ImpactResult{
				Base:         "origin/main",
				ChangedFiles: []string{"README.md"},
				Notes: []string{
					"symbol index loaded, but no changed file defines an indexed symbol (callers overlay empty)",
					"no coverage data on changed files (run `magus run coverage` to populate it)",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Enrich(tt.res, tt.store)
			require.Equal(t, tt.want, tt.res)
		})
	}
}

func TestEnrichNilInputs(t *testing.T) {
	// A nil store (graph failed to load) or nil result is a no-op, never a panic.
	res := &types.ImpactResult{ChangedFiles: []string{"a.go"}}
	Enrich(res, nil)
	require.Equal(t, &types.ImpactResult{ChangedFiles: []string{"a.go"}}, res)
	Enrich(nil, &fakeSymbolStore{hasSymbols: true})
}
