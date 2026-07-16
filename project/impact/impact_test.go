package impact

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// fakeWorkspace implements just the types.WorkspaceRepository surface the impact
// engine touches (Affected, AffectedFromPaths, Get, DescribeTargets). Embedding the
// interface leaves every other method nil - the engine never calls them.
type fakeWorkspace struct {
	types.WorkspaceRepository
	affected *types.AffectedResult
	projects map[string]*types.Project
	targets  types.TargetsOutput
}

func (f *fakeWorkspace) Affected(context.Context, string) (*types.AffectedResult, error) {
	return f.affected, nil
}

func (f *fakeWorkspace) AffectedFromPaths(context.Context, []string) (*types.AffectedResult, error) {
	return f.affected, nil
}

func (f *fakeWorkspace) Get(path string) *types.Project { return f.projects[path] }

func (f *fakeWorkspace) DescribeTargets() types.TargetsOutput { return f.targets }

func TestCompute(t *testing.T) {
	goSpell := types.NewSpell("go", types.WithTargets("go-build", "go-test", "go-vet"))
	tsSpell := types.NewSpell("ts", types.WithTargets("biome-check"))

	tests := []struct {
		name     string
		affected *types.AffectedResult
		projects map[string]*types.Project
		targets  types.TargetsOutput
		want     *Result
	}{
		{
			name: "no changed files",
			affected: &types.AffectedResult{
				Base: "origin/main",
			},
			want: &Result{
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
				"api": {Path: "api", Spells: []string{"go"}, ResolvedSpells: []*types.Spell{goSpell}},
				"web": {Path: "web", Spells: []string{"ts"}, ResolvedSpells: []*types.Spell{tsSpell}},
			},
			targets: types.TargetsOutput{Targets: []types.TargetEntry{
				{Name: "ci", Kind: "canonical"},
				{Name: "test", Kind: "custom", Projects: []string{"api"}},
				{Name: "build", Kind: "custom", Projects: []string{"api", "web"}},
				{Name: "go-test", Kind: "spell"}, // spell entries carry no Projects; ignored here
			}},
			want: &Result{
				Base:             "origin/main",
				ChangedFileCount: 3,
				// Changed is sorted and includes files outside any project.
				ChangedFiles:     []string{"api/main.go", "api/util.go", "docs/README.md"},
				SeedProjects:     []string{"api"},
				TestProjectCount: 1,
				AffectedProjects: []AffectedProject{
					{
						Path:        "api",
						Seed:        true,
						Files:       []string{"api/main.go", "api/util.go"},
						Spells:      []string{"go"},
						Targets:     []string{"build", "go-build", "go-test", "go-vet", "test"},
						TestTargets: []string{"go-test", "test"},
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
	require.Equal(t, &Result{
		Base:             "paths",
		ChangedFileCount: 1,
		ChangedFiles:     []string{"api/main.go"},
		SeedProjects:     []string{"api"},
		AffectedProjects: []AffectedProject{{Path: "api", Seed: true, Files: []string{"api/main.go"}}},
	}, got)
}

func TestIsTestTarget(t *testing.T) {
	for name, want := range map[string]bool{
		"test":             true,
		"go-test":          true,
		"integration-test": true,
		"build":            false,
		"go-vet":           false,
		"biome-check":      false,
	} {
		require.Equal(t, want, isTestTarget(name), name)
	}
}
