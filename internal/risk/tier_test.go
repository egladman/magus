package risk

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProver answers from fixed tables, so the tier rules are exercised without a
// toolchain.
type fakeProver struct {
	equivalent bool
	hits       map[string]PackageHit
	unplaced   map[string]string
	// closure maps a changed package to what can observe it.
	closure map[string][]string
	err     error
}

func (f fakeProver) Equivalent(string, string, string) (bool, string) {
	return f.equivalent, "same syntax tree"
}

func (f fakeProver) Place(context.Context, string, []string) (Placement, error) {
	if f.err != nil {
		return Placement{}, f.err
	}
	return Placement{
		Packages: f.hits,
		Unplaced: f.unplaced,
		Closure: func(changed, testOnly []string) []string {
			out := slices.Clone(testOnly)
			for _, c := range changed {
				out = append(out, c)
				out = append(out, f.closure[c]...)
			}
			slices.Sort(out)
			return slices.Compact(out)
		},
		Narrows: "go::go-test",
	}, nil
}

// fixtureProject mirrors this repository's root: ci needs lint, build and test; lint and
// build need format, which needs generate, which needs x-generate; test runs the go
// spell's go-test op and needs format and two narrower suites.
func fixtureProject() *types.Project {
	return &types.Project{
		Path: ".",
		MagusfileTargets: []string{"ci", "lint", "build", "test", "format", "generate", "x-generate",
			"buzz-test", "cgo-test", "release"},
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
			"x-generate": {{Glob: "tools/gen.yaml"}, {Glob: "docs/**/*.md"}},
			"buzz-test":  {{Glob: "**/*.buzz"}},
			"cgo-test":   {{Glob: "a/*.go"}},
			"release":    {{Glob: "notes/*.md"}},
		},
	}
}

// claims are the describe-file entries fixtureProject gives each fixture path.
var claims = map[string][]types.FileClaim{
	"e/zz_generated.go": {{Project: ".", Target: "x-generate", Role: "output", Glob: "e/zz_generated.go"}},
	"tools/gen.yaml":    {{Project: ".", Role: "source", Glob: "**/*"}, {Project: ".", Target: "x-generate", Role: "source", Glob: "tools/gen.yaml"}},
	"docs/guide.md":     {{Project: ".", Role: "source", Glob: "**/*"}, {Project: ".", Target: "x-generate", Role: "source", Glob: "docs/**/*.md"}},
	"notes/v1.md":       {{Project: ".", Role: "source", Glob: "**/*"}, {Project: ".", Target: "release", Role: "source", Glob: "notes/*.md"}},
	"a/a.go":            {{Project: ".", Role: "source", Glob: "**/*"}, {Project: ".", Target: "cgo-test", Role: "source", Glob: "a/*.go"}},
}

func entries(paths ...string) []types.FileEntry {
	out := make([]types.FileEntry, len(paths))
	for i, p := range paths {
		cs := claims[p]
		if cs == nil {
			cs = []types.FileClaim{{Project: ".", Role: "source", Glob: "**/*"}}
		}
		role := "source"
		if cs[0].Role == "output" {
			role = "output"
		}
		out[i] = types.FileEntry{Path: p, Project: ".", Role: role, Claims: cs}
	}
	return out
}

func fixtureInputs(provers map[string]Prover, delta ...Classified) Inputs {
	paths := make([]string, len(delta))
	for i, c := range delta {
		paths[i] = c.Path
	}
	return Inputs{
		Delta:    Delta{Paths: delta},
		Claims:   entries(paths...),
		Affected: []string{"."},
		Projects: []*types.Project{fixtureProject()},
		Target:   "ci",
		Provers:  provers,
		Root:     "/ws",
		Base:     "base",
		Read: func(context.Context, string) (string, string, error) {
			return "old", "cur", nil
		},
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

// TestAssessTierRules pins every class against every prover outcome, with and without an
// unbounded affected set.
func TestAssessTierRules(t *testing.T) {
	hit := PackageHit{Package: "fx/a", Module: ".", Why: "Go package fx/a"}
	outcomes := map[string]func(path string) map[string]Prover{
		"no prover":  func(string) map[string]Prover { return nil },
		"equivalent": func(string) map[string]Prover { return map[string]Prover{".go": fakeProver{equivalent: true}} },
		"placed": func(p string) map[string]Prover {
			return map[string]Prover{".go": fakeProver{hits: map[string]PackageHit{p: hit}}}
		},
		"unplaced": func(p string) map[string]Prover {
			return map[string]Prover{".go": fakeProver{unplaced: map[string]string{p: "generator code"}}}
		},
		"prover fails": func(string) map[string]Prover {
			return map[string]Prover{".go": fakeProver{err: errors.New("go list: exit 1")}}
		},
	}
	cases := []struct {
		name  string
		path  string
		class Class
		want  map[string]types.RiskTier
	}{
		{"generated", "e/zz_generated.go", ClassGenerated, map[string]types.RiskTier{"no prover": types.RiskTrivial,
			"equivalent": types.RiskTrivial, "placed": types.RiskTrivial, "unplaced": types.RiskTrivial, "prover fails": types.RiskTrivial}},
		{"prose", "README.md", ClassProse, map[string]types.RiskTier{"no prover": types.RiskTrivial,
			"equivalent": types.RiskTrivial, "placed": types.RiskScoped, "unplaced": types.RiskFull, "prover fails": types.RiskFull}},
		{"prose the chain reads", "docs/guide.md", ClassProse, map[string]types.RiskTier{"no prover": types.RiskScoped,
			"equivalent": types.RiskScoped, "placed": types.RiskScoped, "unplaced": types.RiskFull, "prover fails": types.RiskFull}},
		{"comment-only", "c/c.go", ClassCommentOnly, map[string]types.RiskTier{"no prover": types.RiskMechanical,
			"equivalent": types.RiskMechanical, "placed": types.RiskMechanical, "unplaced": types.RiskMechanical, "prover fails": types.RiskMechanical}},
		{"comment-only the chain reads", "a/a.go", ClassCommentOnly, map[string]types.RiskTier{"no prover": types.RiskScoped,
			"equivalent": types.RiskScoped, "placed": types.RiskScoped, "unplaced": types.RiskScoped, "prover fails": types.RiskScoped}},
		{"code", "a/a.go", ClassCode, map[string]types.RiskTier{"no prover": types.RiskFull,
			"equivalent": types.RiskMechanical, "placed": types.RiskScoped, "unplaced": types.RiskFull, "prover fails": types.RiskFull}},
	}
	const graphMoves = "magusfile.buzz can change the dependency graph or every project's build, which the affected set cannot see"
	for _, tc := range cases {
		for outcome, tier := range tc.want {
			for _, unbounded := range []string{"", "change", "path"} {
				name := tc.name + "/" + outcome
				if unbounded != "" {
					name += "/unbounded by the " + unbounded
					tier = types.RiskFull
				}
				t.Run(name, func(t *testing.T) {
					in := fixtureInputs(outcomes[outcome](tc.path), Classified{Path: tc.path, Class: tc.class, Why: "classified"})
					want := ""
					switch unbounded {
					case "change":
						in.UnboundedBy, want = graphMoves, graphMoves
					case "path":
						want = "no project claims " + tc.path
						in.Unbounded = map[string]string{tc.path: want}
					}
					rep := Assess(context.Background(), in)
					assert.Equal(t, tier, rep.Tier)
					assert.Equal(t, map[string]types.RiskTier{tc.path: tier}, tiers(rep))
					if want != "" {
						assert.Equal(t, want, rep.Evidence[0].Why, "the reason is the unbounded one")
					}
				})
			}
		}
	}
}

// TestAssessProseAGeneratorReads is the docs regression: markdown a target in the gate's
// chain declares as its input is prose by class, and skipping it skipped the generator
// that renders it.
func TestAssessProseAGeneratorReads(t *testing.T) {
	rep := Assess(context.Background(), fixtureInputs(nil,
		Classified{Path: "docs/guide.md", Class: ClassProse, Why: `matches "**/*.md" (built-in default)`},
		Classified{Path: "notes/v1.md", Class: ClassProse, Why: `matches "**/*.md" (built-in default)`},
	))
	assert.Equal(t, types.RiskScoped, rep.Tier)
	assert.Equal(t, []string{
		`docs/guide.md: scoped (prose: read by .:x-generate, which ci reaches)`,
		`notes/v1.md: trivial (prose: matches "**/*.md" (built-in default); nothing in ci's chain reads it)`,
	}, rep.Lines(), "a target outside ci's chain reading the file leaves it trivial")
	assert.Equal(t, []string{"magus run x-generate . --no-default-charms"}, gateLines(rep), "the gate is the reader, not the whole of ci")
}

// TestAssessReaderGates: a read prose path runs every target in the chain that declares
// it, across projects, beside what the rest of the change needs.
func TestAssessReaderGates(t *testing.T) {
	site := &types.Project{Path: "site", MagusfileTargets: []string{"ci", "render", "lint"},
		TargetChains: map[string][]types.ChainStep{"ci": {{Target: "render"}, {Target: "lint"}}}}
	guide := Classified{Path: "docs/guide.md", Class: ClassProse, Why: "prose"}
	in := func(provers map[string]Prover, delta ...Classified) Inputs {
		in := fixtureInputs(provers, delta...)
		in.Projects = append(in.Projects, site)
		in.Affected = []string{".", "site"}
		for i, e := range in.Claims {
			if e.Path == guide.Path {
				in.Claims[i].Claims = append(in.Claims[i].Claims,
					types.FileClaim{Project: "site", Target: "render", Role: "source", Glob: "docs/**/*.md"},
					types.FileClaim{Project: "site", Target: "publish", Role: "source", Glob: "docs/**/*.md"})
			}
		}
		return in
	}

	alone := Assess(context.Background(), in(nil, guide))
	assert.Equal(t, types.RiskScoped, alone.Tier)
	assert.Equal(t, []string{"docs/guide.md: scoped (prose: read by .:x-generate, site:render, which ci reaches)"}, alone.Lines(),
		"site:publish is outside ci's chain")
	assert.Equal(t, []string{"magus run x-generate . --no-default-charms", "magus run render site --no-default-charms"}, gateLines(alone))

	withComment := Assess(context.Background(), in(nil, guide, Classified{Path: "c/c.go", Class: ClassCommentOnly}))
	assert.Equal(t, []string{"magus run lint . site --no-default-charms", "magus run render site --no-default-charms"}, gateLines(withComment),
		"lint already runs x-generate, so only the reader lint does not reach is added")

	placed := fakeProver{hits: map[string]PackageHit{"a/a.go": {Package: "fx/a", Module: ".", Why: "Go package fx/a"}}}
	withGo := Assess(context.Background(), in(map[string]Prover{".go": placed}, guide, Classified{Path: "a/a.go", Class: ClassCode}))
	assert.Equal(t, []string{"magus run lint . --no-default-charms", "magus run build . --no-default-charms",
		"magus run test . --no-default-charms (go::go-test narrowed to fx/a)", "magus run ci site --no-default-charms"}, gateLines(withGo),
		"site runs whole beside the narrowed module, which covers its reader")
}

// TestAssessCommentOnlyAReaderDeclares: a comment edit a chain target declares runs
// that reader beside the drift check and lint, since a conventions test reads comments
// as surely as lint does. One nothing declares stays mechanical.
func TestAssessCommentOnlyAReaderDeclares(t *testing.T) {
	read := Assess(context.Background(), fixtureInputs(nil, Classified{Path: "a/a.go", Class: ClassCommentOnly, Why: "only comments differ"}))
	assert.Equal(t, types.RiskScoped, read.Tier)
	assert.Equal(t, []string{"a/a.go: scoped (comment-only: only comments differ; read by .:cgo-test, which ci reaches)"}, read.Lines())
	assert.Equal(t, []string{"magus run lint . --no-default-charms", "magus run cgo-test . --no-default-charms"}, gateLines(read))

	unread := Assess(context.Background(), fixtureInputs(nil, Classified{Path: "c/c.go", Class: ClassCommentOnly, Why: "only comments differ"}))
	assert.Equal(t, types.RiskMechanical, unread.Tier)
	assert.Equal(t, []string{"magus run lint . --no-default-charms"}, gateLines(unread))
}

// TestAssessProseTheTestTargetReads: a prose path the narrowable test target itself
// declares needs that target whole.
func TestAssessProseTheTestTargetReads(t *testing.T) {
	placed := fakeProver{hits: map[string]PackageHit{"a/a.go": {Package: "fx/a", Module: ".", Why: "Go package fx/a"}}}
	in := fixtureInputs(map[string]Prover{".go": placed},
		Classified{Path: "CHANGELOG.md", Class: ClassProse, Why: "prose"}, Classified{Path: "a/a.go", Class: ClassCode})
	in.Claims[0].Claims = append(in.Claims[0].Claims, types.FileClaim{Project: ".", Target: "test", Role: "source", Glob: "CHANGELOG.md"})
	rep := Assess(context.Background(), in)
	assert.Equal(t, []string{"magus run lint . --no-default-charms", "magus run build . --no-default-charms",
		"magus run test . --no-default-charms"}, gateLines(rep))
}

// TestAssessUnboundedPathAlone: a path no project claims is full on its own, and the
// rest of the change keeps its tiers, unlike a change that can move the graph.
func TestAssessUnboundedPathAlone(t *testing.T) {
	in := fixtureInputs(nil, Classified{Path: "stray.txt", Class: ClassCode}, Classified{Path: "notes/v1.md", Class: ClassProse, Why: "prose"})
	in.Unbounded = map[string]string{"stray.txt": "no project claims stray.txt"}
	rep := Assess(context.Background(), in)
	assert.Equal(t, types.RiskFull, rep.Tier)
	assert.Equal(t, []string{
		"stray.txt: full (code: no project claims stray.txt)",
		"notes/v1.md: trivial (prose: prose; nothing in ci's chain reads it)",
	}, rep.Lines())

	in.Delta.Paths = in.Delta.Paths[1:]
	assert.Equal(t, types.RiskTrivial, Assess(context.Background(), in).Tier, "the claimed path alone is sized by its own tier")
}

// gateLines is Commands with each narrowed step's op and packages beside it.
func gateLines(rep types.RiskReport) []string {
	out := rep.Commands()
	for i, g := range rep.Gate {
		if g.Op != "" {
			out[i] += " (" + g.Op + " narrowed to " + strings.Join(g.Packages, " ") + ")"
		}
	}
	return out
}

// TestAssessEmbeddedProse is the go:embed regression: markdown a package compiles in is
// prose by class, and inheriting a verdict over it skipped the tests that read it.
func TestAssessEmbeddedProse(t *testing.T) {
	skill := "internal/agent/skills/run/SKILL.md"
	prover := fakeProver{hits: map[string]PackageHit{skill: {Package: "fx/skills", Module: ".", Why: "embedded by Go package fx/skills (go:embed)"}},
		closure: map[string][]string{"fx/skills": {"fx/agent"}}}
	rep := Assess(context.Background(), fixtureInputs(map[string]Prover{".go": prover},
		Classified{Path: skill, Class: ClassProse, Why: `matches "**/*.md" (built-in default)`}))
	assert.Equal(t, types.RiskScoped, rep.Tier)
	assert.Equal(t, []types.RiskEvidence{
		{Path: skill, Project: ".", Class: "prose", Tier: types.RiskScoped, Why: "embedded by Go package fx/skills (go:embed)", Packages: []string{"fx/agent", "fx/skills"}},
	}, rep.Evidence)
	assert.Equal(t, []string{
		"magus run lint . --no-default-charms",
		"magus run build . --no-default-charms",
		"magus run test . --no-default-charms (go::go-test narrowed to fx/agent fx/skills)",
	}, gateLines(rep))
	assert.Equal(t, types.RiskGateStep{Target: "test", Projects: []string{"."}, Op: "go::go-test", Packages: []string{"fx/agent", "fx/skills"},
		Argv: []string{"magus", "run", "test", ".", "--no-default-charms"}}, rep.Gate[2], "the step runs the project's own test target")
}

// TestAssessGeneratedOutput: generated output is trusted as committed unless its
// generator's input moved in the same change; then the drift check re-derives it.
func TestAssessGeneratedOutput(t *testing.T) {
	alone := Assess(context.Background(), fixtureInputs(nil,
		Classified{Path: "e/zz_generated.go", Class: ClassGenerated, Why: "a declared output glob claims it"}))
	assert.Equal(t, types.RiskTrivial, alone.Tier)
	assert.Equal(t, []string{"e/zz_generated.go: trivial (generated: generated by .:x-generate, and nothing it reads changed)"}, alone.Lines())
	assert.Equal(t, []types.RiskGateStep{}, alone.Gate, "a trivial change runs nothing")

	unrelated := Assess(context.Background(), fixtureInputs(nil,
		Classified{Path: "e/zz_generated.go", Class: ClassGenerated, Why: "a declared output glob claims it"},
		Classified{Path: "c/c.go", Class: ClassCommentOnly, Why: "only comments differ"}))
	assert.Equal(t, map[string]types.RiskTier{"e/zz_generated.go": types.RiskTrivial, "c/c.go": types.RiskMechanical}, tiers(unrelated),
		"a file its generator does not declare leaves the output trusted")

	withInput := Assess(context.Background(), fixtureInputs(nil,
		Classified{Path: "e/zz_generated.go", Class: ClassGenerated, Why: "a declared output glob claims it"},
		Classified{Path: "tools/gen.yaml", Class: ClassCode, Why: "no comment syntax"}))
	assert.Equal(t, map[string]types.RiskTier{"e/zz_generated.go": types.RiskMechanical, "tools/gen.yaml": types.RiskFull}, tiers(withInput))
	assert.Contains(t, withInput.Lines()[0], "tools/gen.yaml, which it reads, changed too")
}

// TestAssessCodeEquivalenceNeedsBothSides: a path the base cannot supply is never
// proven equivalent, whatever the prover would have said.
func TestAssessCodeEquivalenceNeedsBothSides(t *testing.T) {
	in := fixtureInputs(map[string]Prover{".go": fakeProver{equivalent: true}}, Classified{Path: "a/a.go", Class: ClassCode})
	in.Read = func(context.Context, string) (string, string, error) { return "", "", errors.New("absent at base") }
	assert.Equal(t, types.RiskFull, Assess(context.Background(), in).Tier)
	in.Read = nil
	assert.Equal(t, types.RiskFull, Assess(context.Background(), in).Tier)
}

func TestAssessEmptyDeltaIsTrivial(t *testing.T) {
	rep := Assess(context.Background(), fixtureInputs(nil))
	assert.Equal(t, types.RiskReport{Base: "base", Target: "ci", Tier: types.RiskTrivial, Affected: []string{"."},
		Evidence: []types.RiskEvidence{}, Gate: []types.RiskGateStep{}}, rep)
}

// TestAssessGates pins the gate each tier builds.
func TestAssessGates(t *testing.T) {
	docs := &types.Project{Path: "docs", MagusfileTargets: []string{"ci", "generate"}}
	bare := &types.Project{Path: "site", MagusfileTargets: []string{"ci"}}
	withProjects := func(in Inputs) Inputs {
		in.Projects = append(in.Projects, docs, bare)
		in.Affected = []string{".", "docs", "site"}
		return in
	}
	placed := fakeProver{hits: map[string]PackageHit{"a/a.go": {Package: "fx/a", Module: ".", Why: "Go package fx/a"}},
		closure: map[string][]string{"fx/a": {"fx/b"}}}

	tests := []struct {
		name string
		in   Inputs
		tier types.RiskTier
		gate []string
	}{
		{
			name: "mechanical runs lint, and generate only where lint does not",
			in:   withProjects(fixtureInputs(nil, Classified{Path: "c/c.go", Class: ClassCommentOnly})),
			tier: types.RiskMechanical,
			gate: []string{"magus run lint . --no-default-charms", "magus run generate docs --no-default-charms", "magus run ci site --no-default-charms"},
		},
		{
			name: "scoped narrows the test target and runs the rest of the chain",
			in:   fixtureInputs(map[string]Prover{".go": placed}, Classified{Path: "a/a.go", Class: ClassCode}),
			tier: types.RiskScoped,
			gate: []string{"magus run lint . --no-default-charms", "magus run build . --no-default-charms",
				"magus run test . --no-default-charms (go::go-test narrowed to fx/a fx/b)"},
		},
		{
			name: "scoped runs every other affected project whole",
			in:   withProjects(fixtureInputs(map[string]Prover{".go": placed}, Classified{Path: "a/a.go", Class: ClassCode})),
			tier: types.RiskScoped,
			gate: []string{"magus run lint . --no-default-charms", "magus run build . --no-default-charms",
				"magus run test . --no-default-charms (go::go-test narrowed to fx/a fx/b)", "magus run ci docs site --no-default-charms"},
		},
		{
			name: "a scoped reader alone runs only the reader",
			in:   withProjects(fixtureInputs(nil, Classified{Path: "docs/guide.md", Class: ClassProse})),
			tier: types.RiskScoped,
			gate: []string{"magus run x-generate . --no-default-charms"},
		},
		{
			name: "a module no project is rooted at is full",
			in: fixtureInputs(map[string]Prover{".go": fakeProver{hits: map[string]PackageHit{"libs/x/x.go": {Package: "fx/x", Module: "libs/x"}}}},
				Classified{Path: "libs/x/x.go", Class: ClassCode}),
			tier: types.RiskFull,
			gate: []string{"magus run ci . --no-default-charms"},
		},
		{
			name: "full runs the target over every affected project that has it",
			in:   withProjects(fixtureInputs(nil, Classified{Path: "tools/gen.yaml", Class: ClassCode})),
			tier: types.RiskFull,
			gate: []string{"magus run ci . docs site --no-default-charms"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := Assess(context.Background(), tt.in)
			assert.Equal(t, tt.tier, rep.Tier)
			assert.Equal(t, tt.gate, gateLines(rep))
		})
	}
}

// TestAssessScopedWithoutANarrowableTest: a project whose test target runs no op the
// prover narrows runs the invoked target whole, and says why.
func TestAssessScopedWithoutANarrowableTest(t *testing.T) {
	in := fixtureInputs(map[string]Prover{".go": fakeProver{hits: map[string]PackageHit{"a/a.go": {Package: "fx/a", Module: "."}}}},
		Classified{Path: "a/a.go", Class: ClassCode})
	in.Projects[0].TargetSpellOps = nil
	rep := Assess(context.Background(), in)
	require.Equal(t, types.RiskScoped, rep.Tier)
	assert.Equal(t, []string{"magus run ci . --no-default-charms"}, rep.Commands())
	assert.True(t, slices.ContainsFunc(rep.Lines(), func(l string) bool {
		return strings.HasPrefix(l, ". ci: scoped (no test target in ci's chain runs go::go-test")
	}), rep.Lines())
}

func TestHigher(t *testing.T) {
	order := []types.RiskTier{types.RiskTrivial, types.RiskMechanical, types.RiskScoped, types.RiskFull}
	for i, a := range order {
		for j, b := range order {
			assert.Equal(t, i > j, Higher(a, b), "%s over %s", a, b)
		}
	}
}
