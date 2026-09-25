package risk

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// fixtureProject mirrors this repository's root: ci needs lint, build and test;
// test runs the go spell's go-test op and needs format and two narrower suites;
// generate reaches x-generate, which declares tools/gen.yaml as its input.
func fixtureProject(root string) *types.Project {
	return &types.Project{
		Path: ".",
		Dir:  root,
		MagusfileTargets: []string{"ci", "lint", "build", "test", "format", "generate", "x-generate",
			"buzz-test", "cgo-test"},
		TargetChains: map[string][]types.ChainStep{
			"ci":       {{Target: "lint"}, {Target: "build"}, {Target: "test"}},
			"lint":     {{Target: "format"}},
			"build":    {{Target: "format"}},
			"format":   {{Target: "generate"}},
			"generate": {{Target: "x-generate"}},
			"test":     {{Target: "format"}, {Target: "buzz-test"}, {Target: "cgo-test"}},
		},
		// cgo-test runs go-test too, over packages of its own choosing, so it runs as
		// declared rather than being narrowed.
		TargetSpellOps: map[string][]types.TargetSpellUse{
			"test":     {{Spell: "go", Ops: []string{"go-test"}}},
			"cgo-test": {{Spell: "go", Ops: []string{"go-test"}}},
		},
		TargetInputs: map[string][]types.InputRef{
			"x-generate": {{Glob: "tools/gen.yaml"}},
			"buzz-test":  {{Glob: "**/*.buzz"}},
			"cgo-test":   {{Glob: "a/*.go"}},
		},
	}
}

// entry classifies a fixture path the way ClassifyFiles would for fixtureProject.
func entry(p *types.Project, path string) types.FileEntry {
	if path == "e/zz_generated.go" {
		return types.FileEntry{Path: path, Project: ".", Role: "output", Exists: true,
			Claims: []types.FileClaim{{Project: ".", Target: "x-generate", Role: "output", Glob: path}}}
	}
	e := types.FileEntry{Path: path, Project: ".", Role: "source", Exists: true,
		Claims: []types.FileClaim{{Project: ".", Role: "source", Glob: "**/*"}}}
	for t, refs := range p.TargetInputs {
		for _, r := range refs {
			if ok, _ := filepath.Match(r.Glob, path); ok || (r.Glob == "**/*.buzz" && strings.HasSuffix(path, ".buzz")) {
				e.Claims = append(e.Claims, types.FileClaim{Project: ".", Target: t, Role: "source", Glob: r.Glob})
			}
		}
	}
	return e
}

func fixtureInputs(t *testing.T, edits map[string]string, changed []string) Inputs {
	t.Helper()
	root := writeTree(t, with(fixtureModule, edits))
	p := fixtureProject(root)
	var files []types.FileEntry
	for _, c := range changed {
		files = append(files, entry(p, c))
	}
	return Inputs{
		Root:     root,
		Target:   "ci",
		Base:     "base",
		Label:    "base",
		Changed:  changed,
		Files:    files,
		Affected: []types.ImpactProject{{Path: "."}},
		Projects: []*types.Project{p},
		Prose:    ci.ProseScopes(nil),
		Syntax:   map[string]spells.CommentSyntax{".go": {Directives: spellDirectives}},
		At: func(_ context.Context, _, path string) (string, error) {
			if body, ok := fixtureModule[path]; ok {
				return body, nil
			}
			return "", errors.New("absent at base")
		},
		GoList: GoList,
	}
}

func tiers(rep types.RiskReport) map[string]types.RiskTier {
	out := map[string]types.RiskTier{}
	for _, e := range rep.Evidence {
		if e.Path != "" {
			out[e.Path] = e.Tier
		}
	}
	return out
}

func argvs(rep types.RiskReport) []string {
	out := []string{}
	for _, g := range rep.Gate {
		out = append(out, strings.Join(g.Argv, " "))
	}
	return out
}

const (
	lintStep  = "magus run lint . --no-default-charms"
	buildStep = "magus run build . --no-default-charms"
	ciStep    = "magus run ci . --no-default-charms"
)

func TestAssessTiers(t *testing.T) {
	aCode := "package a\n\n// A is the leaf.\nfunc A() int { return 2 }\n"
	tests := []struct {
		name    string
		edits   map[string]string
		changed []string
		noBase  bool
		tier    types.RiskTier
		files   map[string]types.RiskTier
		gate    []string
	}{
		{
			name:    "comment-only Go edit",
			edits:   map[string]string{"a/a.go": "package a\n\n// A is the leaf of the fixture graph.\nfunc A() int { return 1 }\n"},
			changed: []string{"a/a.go"},
			tier:    types.RiskMechanical,
			files:   map[string]types.RiskTier{"a/a.go": types.RiskMechanical},
			gate:    []string{lintStep},
		},
		{
			name:    "gofmt-only Go edit",
			edits:   map[string]string{"b/b.go": "package b\nimport \"example.com/fx/a\"\nfunc B() int {\n\treturn a.A()\n}\n"},
			changed: []string{"b/b.go"},
			tier:    types.RiskMechanical,
			files:   map[string]types.RiskTier{"b/b.go": types.RiskMechanical},
			gate:    []string{lintStep},
		},
		{
			name:    "generated output alone",
			edits:   map[string]string{"e/zz_generated.go": "package e\n\nfunc Generated() { E() }\n"},
			changed: []string{"e/zz_generated.go"},
			tier:    types.RiskTrivial,
			files:   map[string]types.RiskTier{"e/zz_generated.go": types.RiskTrivial},
			gate:    []string{},
		},
		{
			name:    "changelog fragment",
			edits:   map[string]string{"changes/unreleased/risk.md": "Added risk tiers.\n"},
			changed: []string{"changes/unreleased/risk.md"},
			tier:    types.RiskTrivial,
			files:   map[string]types.RiskTier{"changes/unreleased/risk.md": types.RiskTrivial},
			gate:    []string{},
		},
		{
			name:    "single package with a known importer set",
			edits:   map[string]string{"a/a.go": aCode},
			changed: []string{"a/a.go"},
			tier:    types.RiskScoped,
			files:   map[string]types.RiskTier{"a/a.go": types.RiskScoped},
			gate: []string{lintStep, buildStep,
				"magus run go::go-test-packages . --no-default-charms -- example.com/fx/a example.com/fx/app example.com/fx/b example.com/fx/c example.com/fx/e",
				"magus run cgo-test . --no-default-charms"},
		},
		{
			name:    "test file reaches only its own package",
			edits:   map[string]string{"e/e_test.go": "package e\n\nimport \"testing\"\n\nfunc TestE(t *testing.T) {}\n"},
			changed: []string{"e/e_test.go"},
			tier:    types.RiskScoped,
			files:   map[string]types.RiskTier{"e/e_test.go": types.RiskScoped},
			gate:    []string{lintStep, buildStep, "magus run go::go-test-packages . --no-default-charms -- example.com/fx/e"},
		},
		{
			name:    "embedded markdown is code",
			edits:   map[string]string{"skills/doc.md": "# Skill\n\nMore.\n"},
			changed: []string{"skills/doc.md"},
			tier:    types.RiskScoped,
			files:   map[string]types.RiskTier{"skills/doc.md": types.RiskScoped},
			gate:    []string{lintStep, buildStep, "magus run go::go-test-packages . --no-default-charms -- example.com/fx/skills"},
		},
		{
			name:    "magusfile edit",
			edits:   map[string]string{"magusfile.buzz": "// ci.\n" + fixtureModule["magusfile.buzz"]},
			changed: []string{"magusfile.buzz"},
			tier:    types.RiskFull,
			files:   map[string]types.RiskTier{"magusfile.buzz": types.RiskFull},
			gate:    []string{ciStep},
		},
		{
			name:    "generator-only package",
			edits:   map[string]string{"genlib/genlib.go": "package genlib\n\nfunc Run() { println() }\n"},
			changed: []string{"genlib/genlib.go"},
			tier:    types.RiskFull,
			files:   map[string]types.RiskTier{"genlib/genlib.go": types.RiskFull},
			gate:    []string{ciStep},
		},
		{
			name:    "declared generator input, and the output it moved",
			edits:   map[string]string{"tools/gen.yaml": "a: 2\n", "e/zz_generated.go": "package e\n\nfunc Generated() { E() }\n"},
			changed: []string{"e/zz_generated.go", "tools/gen.yaml"},
			tier:    types.RiskFull,
			files:   map[string]types.RiskTier{"e/zz_generated.go": types.RiskMechanical, "tools/gen.yaml": types.RiskFull},
			gate:    []string{ciStep},
		},
		{
			name:    "files go does not build here",
			edits:   map[string]string{"w/w_windows.go": "package w\n\nfunc W() {}\n", "a/testdata/x.go": "package fixture\n\nvar X = 1\n"},
			changed: []string{"a/testdata/x.go", "w/w_windows.go"},
			tier:    types.RiskFull,
			files:   map[string]types.RiskTier{"a/testdata/x.go": types.RiskFull, "w/w_windows.go": types.RiskFull},
			gate:    []string{ciStep},
		},
		{
			name:    "no base to read proves nothing comment-only",
			edits:   map[string]string{"a/a.go": "package a\n\n// A is the leaf of the fixture graph.\nfunc A() int { return 1 }\n"},
			changed: []string{"a/a.go"},
			noBase:  true,
			tier:    types.RiskScoped,
			files:   map[string]types.RiskTier{"a/a.go": types.RiskScoped},
			gate: []string{lintStep, buildStep,
				"magus run go::go-test-packages . --no-default-charms -- example.com/fx/a example.com/fx/app example.com/fx/b example.com/fx/c example.com/fx/e",
				"magus run cgo-test . --no-default-charms"},
		},
		{
			name: "mixed change takes the highest tier",
			edits: map[string]string{
				"README.md": "# fx, documented\n",
				"a/a.go":    "package a\n\n// A is the leaf of the fixture graph.\nfunc A() int { return 1 }\n",
				"c/c.go":    "package c\n\nimport \"example.com/fx/b\"\n\nfunc C() int { return b.B() + 1 }\n",
			},
			changed: []string{"README.md", "a/a.go", "c/c.go"},
			tier:    types.RiskScoped,
			files:   map[string]types.RiskTier{"README.md": types.RiskTrivial, "a/a.go": types.RiskMechanical, "c/c.go": types.RiskScoped},
			gate: []string{lintStep, buildStep,
				"magus run go::go-test-packages . --no-default-charms -- example.com/fx/app example.com/fx/c",
				"magus run cgo-test . --no-default-charms"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := fixtureInputs(t, tt.edits, tt.changed)
			if tt.noBase {
				in.Base = ""
			}
			rep, err := Assess(context.Background(), in)
			require.NoError(t, err)
			assert.Equal(t, tt.tier, rep.Tier)
			assert.Equal(t, tt.files, tiers(rep))
			assert.Equal(t, tt.gate, argvs(rep))
		})
	}
}

func TestAssessPrunesOnACleanRecordOnly(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	outcomes := func(pass, fail int) []forecast.Outcome {
		var out []forecast.Outcome
		for i := range pass + fail {
			r := forecast.OutcomePass
			if i < fail {
				r = forecast.OutcomeFail
			}
			out = append(out, forecast.Outcome{Result: r, AffectedByDiff: true, DurationMs: 1000, At: now.Add(-time.Duration(i+1) * time.Hour)})
		}
		return out
	}
	stale := forecast.Outcome{Result: forecast.OutcomeFail, AffectedByDiff: true, DurationMs: 1000, At: now.Add(-40 * 24 * time.Hour)}
	noop := forecast.Outcome{Result: forecast.OutcomeFail, AffectedByDiff: true, DurationMs: 0, At: now.Add(-time.Minute)}
	unaffected := forecast.Outcome{Result: forecast.OutcomeFail, AffectedByDiff: false, DurationMs: 1000, At: now.Add(-time.Minute)}

	in := fixtureInputs(t, map[string]string{"magusfile.buzz": "// ci.\n" + fixtureModule["magusfile.buzz"]}, []string{"magusfile.buzz"})
	in.Projects = append(in.Projects,
		&types.Project{Path: "docs", Dir: filepath.Join(in.Root, "docs"), MagusfileTargets: []string{"ci"}},
		&types.Project{Path: "site", Dir: filepath.Join(in.Root, "site"), MagusfileTargets: []string{"ci"}})
	in.Affected = []types.ImpactProject{{Path: "."}, {Path: "docs"}, {Path: "site"}}
	in.MinRuns, in.Window, in.Now = 50, 30*24*time.Hour, now
	in.History = &forecast.History{Projects: map[string]map[string]forecast.Stats{
		".": {
			"magusfile/ci": {RecentOutcomes: append(outcomes(60, 0), stale, noop, unaffected)},
			"go/ci":        {RecentOutcomes: []forecast.Outcome{{Result: forecast.OutcomeFail, AffectedByDiff: true, DurationMs: 5, At: now}}},
		},
		"docs": {"magusfile/ci": {RecentOutcomes: outcomes(59, 1)}},
		"site": {"magusfile/ci": {RecentOutcomes: outcomes(20, 0)}},
	}}

	rep, err := Assess(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, types.RiskFull, rep.Tier)
	assert.Equal(t, []string{"magus run ci docs site --no-default-charms"}, argvs(rep))
	assert.Equal(t, []types.TargetRisk{
		{Project: ".", Target: "ci", Rate: 0, Runs: 60, Failures: 0, Window: "30d", Pruned: true},
		{Project: "docs", Target: "ci", Rate: 1.0 / 60, Runs: 60, Failures: 1, Window: "30d"},
		{Project: "site", Target: "ci", Rate: 0, Runs: 20, Failures: 0, Window: "30d"},
	}, rep.Risk)
	assert.Contains(t, rep.Evidence, types.RiskEvidence{Project: ".", Target: "ci", Tier: types.RiskFull,
		Why: "statistically pruned: 60 recorded affected runs in the last 30d, none failed (minimum 50); main's post-merge CI still runs it"})
}

func TestAssessNeverPrunesMechanical(t *testing.T) {
	in := fixtureInputs(t, map[string]string{"a/a.go": "package a\n\n// A.\nfunc A() int { return 1 }\n"}, []string{"a/a.go"})
	in.MinRuns, in.Window, in.Now = 1, time.Hour, time.Now()
	in.History = &forecast.History{Projects: map[string]map[string]forecast.Stats{
		".": {"magusfile/lint": {RecentOutcomes: []forecast.Outcome{{Result: forecast.OutcomePass, AffectedByDiff: true, DurationMs: 1, At: time.Now()}}}},
	}}
	rep, err := Assess(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, []string{lintStep}, argvs(rep))
	assert.Empty(t, rep.Risk)
}
