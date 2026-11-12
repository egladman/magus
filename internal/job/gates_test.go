package job

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// gatesWorkspace is a ci chain of a linter, a test, a build that composes a compile step,
// a generator and a linter in another project, the shape a go-test check never finishes.
func gatesWorkspace() []*types.Project {
	return []*types.Project{
		{
			Path:    ".",
			Sources: types.MustParseGlobs("**/*.go"),
			TargetChains: map[string][]types.ChainStep{
				"ci": {
					{Target: "lint"}, {Target: "go-test"}, {Target: "build"}, {Target: "gen"},
					{Project: "docs", Target: "lint"},
				},
				"build": {{Target: "compile"}},
			},
			TargetOutputs: map[string][]types.OutputRef{"gen": {{Glob: "out/gen.go"}}},
		},
		{
			Path:    "docs",
			Sources: types.MustParseGlobs("**/*.md"),
		},
	}
}

func gateTargets(gates []UnrunGate) []string {
	var out []string
	for _, g := range gates {
		out = append(out, fmt.Sprintf("%s:%s", g.Project, g.Target))
	}
	return out
}

func TestUnrunGatesListsCiTargetsNothingTheJobRuns(t *testing.T) {
	t.Parallel()

	check := func(target string) *types.LeaseCheck { return &types.LeaseCheck{Target: target, Project: "."} }
	goal := func(target string) types.Goal {
		return types.Goal{ID: "g-" + target, Check: types.LeaseCheck{Target: target, Project: "."}}.Resolve()
	}
	row := func(mutate func(*types.Job)) types.Job {
		r := types.Job{ID: "j", WritePaths: []string{"internal/job/gates.go"}, Check: check("go-test")}
		mutate(&r)
		return r
	}

	for _, tc := range []struct {
		name        string
		row         types.Job
		regenerated []RegeneratedOutput
		want        []string
	}{
		{
			name: "a go-test check leaves the linter and the build; compile rides with the build",
			row:  row(func(*types.Job) {}),
			want: []string{".:lint", ".:build", ".:gen"},
		},
		{
			name: "a goal that runs a target covers it",
			row:  row(func(r *types.Job) { r.Goals = []types.Goal{goal("lint")} }),
			want: []string{".:build", ".:gen"},
		},
		{
			name: "a check's chain members count as run",
			row:  row(func(r *types.Job) { r.Check = check("build") }),
			want: []string{".:lint", ".:go-test", ".:gen"},
		},
		{
			name: "a script check covers nothing",
			row:  row(func(r *types.Job) { r.Check = &types.LeaseCheck{Script: "hack/check.buzz"} }),
			want: []string{".:lint", ".:go-test", ".:build", ".:gen"},
		},
		{
			name:        "what the root regenerates is left to that list",
			row:         row(func(*types.Job) {}),
			regenerated: []RegeneratedOutput{{Output: "out/gen.go", Target: ".:gen", Source: "**/*.go"}},
			want:        []string{".:lint", ".:build"},
		},
		{
			name: "a project whose sources the write paths miss is not listed",
			row:  row(func(r *types.Job) { r.WritePaths = []string{"docs/page.md"} }),
			want: []string{"docs:lint"},
		},
		{
			name: "a read-only job writes nothing",
			row:  row(func(r *types.Job) { r.ReadOnly = true }),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := UnrunGates(t.TempDir(), tc.row, gatesWorkspace(), tc.regenerated)
			assert.Equal(t, tc.want, gateTargets(got))
			for _, g := range got {
				require.NoError(t, g.Goal.Validate(), "the goal to add must be declarable as printed")
			}
		})
	}
}

// owningProjectGateSuite pins MGS3045 for a write inside a project of its own.
type owningProjectGateSuite struct {
	suite.Suite
}

func TestOwningProjectGate(t *testing.T) { suite.Run(t, new(owningProjectGateSuite)) }

func (s *owningProjectGateSuite) workspace() []*types.Project {
	return []*types.Project{
		{
			Path:             ".",
			Sources:          types.MustParseGlobs("**/*.go"),
			MagusfileTargets: []string{"ci", "test"},
			TargetChains:     map[string][]types.ChainStep{"ci": {{Target: "test"}}},
		},
		{Path: "libs/buzz", Sources: types.MustParseGlobs("**/*.go"), MagusfileTargets: []string{"test"}},
		{Path: "libs/bare", Sources: types.MustParseGlobs("**/*.go")},
	}
}

func (s *owningProjectGateSuite) gates(check *types.LeaseCheck, write ...string) []string {
	row := types.Job{ID: "j", WritePaths: write, Check: check}
	return gateTargets(UnrunGates(s.T().TempDir(), row, s.workspace(), nil))
}

func (s *owningProjectGateSuite) TestGateNamesTheOwningProject() {
	s.Equal([]string{"libs/buzz:test"}, s.gates(nil, "libs/buzz/lexer.go"))
}

func (s *owningProjectGateSuite) TestAnAncestorsCheckDoesNotSatisfyIt() {
	got := s.gates(&types.LeaseCheck{Target: "test", Project: "."}, "libs/buzz/lexer.go")

	s.Equal([]string{"libs/buzz:test"}, got)
}

func (s *owningProjectGateSuite) TestTheOwningProjectsCheckSatisfiesIt() {
	s.Empty(s.gates(&types.LeaseCheck{Target: "test", Project: "libs/buzz"}, "libs/buzz/lexer.go"))
}

func (s *owningProjectGateSuite) TestARootFileStaysWithTheRoot() {
	s.Equal([]string{".:test"}, s.gates(nil, "internal/a.go"))
}

func (s *owningProjectGateSuite) TestFilesInBothGateBoth() {
	got := s.gates(&types.LeaseCheck{Target: "test", Project: "libs/buzz"}, "libs/buzz/lexer.go", "internal/a.go")

	s.Equal([]string{".:test"}, got)
}

func (s *owningProjectGateSuite) TestAProjectWithoutTheTargetLeavesItToTheAncestor() {
	s.Equal([]string{".:test"}, s.gates(nil, "libs/bare/a.go"))
}

func (s *owningProjectGateSuite) TestASiblingSharingAPrefixIsNotTheOwner() {
	s.Equal([]string{".:test"}, s.gates(nil, "libs/buzzard/a.go"))
}

// reachingWorkspace has a root lint naming libs/ in its inputs, as this
// repo's does. Its root test reaches a nested module by language default.
func (s *owningProjectGateSuite) reachingWorkspace() []*types.Project {
	return []*types.Project{
		{
			Path:             ".",
			Sources:          types.MustParseGlobs("**/*.go"),
			MagusfileTargets: []string{"ci", "lint", "test"},
			TargetChains:     map[string][]types.ChainStep{"ci": {{Target: "lint"}, {Target: "test"}}},
			TargetInputs:     map[string][]types.InputRef{"lint": {{Glob: "**/*.go"}, {Glob: "libs/**/*.go"}}},
		},
		{Path: "libs/buzz", Sources: types.MustParseGlobs("**/*.go"), MagusfileTargets: []string{"lint", "test"}},
		{
			Path:             "libs/digest",
			Sources:          types.MustParseGlobs("**/*.go"),
			MagusfileTargets: []string{"lint"},
			TargetInputs:     map[string][]types.InputRef{"lint": {{Glob: "gen/table.digest"}}},
		},
	}
}

func (s *owningProjectGateSuite) reaching(write ...string) []UnrunGate {
	row := types.Job{ID: "j", WritePaths: write, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}
	return UnrunGates(s.T().TempDir(), row, s.reachingWorkspace(), nil)
}

func (s *owningProjectGateSuite) TestARootTargetSpellingOutThePathKeepsItsGate() {
	s.Equal([]string{"libs/buzz:lint", ".:lint", "libs/buzz:test"}, gateTargets(s.reaching("libs/buzz/lexer.go")))
}

func (s *owningProjectGateSuite) TestAnOwnersTargetNotReadingTheFileLeavesItToTheAncestor() {
	s.Equal([]string{".:lint", ".:test"}, gateTargets(s.reaching("libs/digest/a.go")))
}

func (s *owningProjectGateSuite) TestEachGateNamesItsOwnSource() {
	gates := s.reaching("libs/buzz/lexer.go")

	s.Require().Len(gates, 3)
	s.Equal("libs/buzz/**/*.go", gates[0].Source)
	s.Equal("libs/**/*.go", gates[1].Source)
}

// unresolvedStore validates against a libs/spelled whose spells are unresolved.
func (s *owningProjectGateSuite) unresolvedStore() *Store {
	projects := s.workspace()
	projects = append(projects, &types.Project{Path: "libs/spelled", Sources: types.MustParseGlobs("**/*.go"), Spells: []string{"go"}})
	root := testkit.NewRepository(s.T(), map[string]string{"a.go": "package a\n"}).Root()
	return NewStore(tempLocation(s.T(), root)).InWorkspace(Workspace{Projects: projects})
}

func (s *owningProjectGateSuite) validated(store *Store, check *types.LeaseCheck, write string) types.JobValidation {
	v, err := validateDryFork(s.T().Context(), store, "j", func(u *types.Job) {
		u.WritePaths, u.Check, u.Agent = []string{write}, check, agent.Instructed("c")
	})
	s.Require().NoError(err)
	return v
}

func (s *owningProjectGateSuite) TestAnOwnerWithUnresolvedSpellsIsNamedUnchecked() {
	v := s.validated(s.unresolvedStore(), &types.LeaseCheck{Target: "test", Project: "."}, "libs/spelled/a.go")

	s.Contains(v.Unchecked, "whether project \"libs/spelled\" runs `test`: it binds spells it carries unresolved, so `test .` gates its files")
}

func (s *owningProjectGateSuite) TestAnOwnerWithResolvedTargetsIsNotNamed() {
	projects := s.workspace()
	resolved := goCheckWorkspace("libs/buzz").Projects[0]
	resolved.Sources = types.MustParseGlobs("**/*.go")
	projects[1] = resolved
	root := testkit.NewRepository(s.T(), map[string]string{"a.go": "package a\n"}).Root()
	store := NewStore(tempLocation(s.T(), root)).InWorkspace(Workspace{Projects: projects})

	v := s.validated(store, &types.LeaseCheck{Target: "test", Project: "libs/buzz"}, "libs/buzz/a.go")

	s.Empty(v.Unchecked)
}

func TestUnrunGatesNeedsACiChain(t *testing.T) {
	t.Parallel()

	r := types.Job{ID: "j", WritePaths: []string{"a.go"}, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}
	assert.Empty(t, UnrunGates(t.TempDir(), r, []*types.Project{{Path: ".", Sources: types.MustParseGlobs("**/*.go")}}, nil))
	assert.Empty(t, UnrunGates(t.TempDir(), r, nil, nil), "no root project")
}

func TestUnrunGoalNamesTheProject(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "lint", unrunGoal(".", "lint", map[string]bool{}).ID)
	assert.Equal(t, "lint-libs-conventions", unrunGoal("libs/conventions", "lint", map[string]bool{}).ID)
	assert.Equal(t, types.GoalKindCheck, unrunGoal(".", "lint", map[string]bool{}).Kind)
}

// Two projects can fold to one id, and a ci target can share its name with a goal the row
// already holds: each goal to add gets an id of its own.
func TestUnrunGatesGiveEachGoalItsOwnID(t *testing.T) {
	t.Parallel()

	projects := []*types.Project{
		{
			Path:    ".",
			Sources: types.MustParseGlobs("**/*.go"),
			TargetChains: map[string][]types.ChainStep{
				"ci": {{Target: "check"}, {Project: "a/b", Target: "lint"}, {Project: "a-b", Target: "lint"}},
			},
		},
		{Path: "a/b", Sources: types.MustParseGlobs("**/*.go")},
		{Path: "a-b", Sources: types.MustParseGlobs("**/*.go")},
	}
	row := types.Job{ID: "j", WritePaths: []string{"a/b/x.go", "a-b/y.go", "z.go"}, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}

	got := UnrunGates(t.TempDir(), row, projects, nil)

	var ids []string
	for _, g := range got {
		ids = append(ids, g.Goal.ID)
	}
	assert.Equal(t, []string{"check-2", "lint-a-b", "lint-a-b-2"}, ids)
}

// workerGrade is the guard's answer for a worker: the build of magus is never its to run, and
// a test only without default charms.
func workerGrade(c types.LeaseCheck) CheckVerdict {
	switch {
	case c.Target == "build":
		return CheckVerdict{Denied: true, Rule: "worker-builds-magus", Reason: "a worker does not build magus"}
	case c.Target == "go-test" && !c.NoDefaultCharms:
		return CheckVerdict{Denied: true, Rule: "lease-gate", Reason: "test needs --no-default-charms"}
	}
	return CheckVerdict{}
}

func TestUnrunGatesOfferOnlyGoalsAWorkerMayRun(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "j", WritePaths: []string{"internal/job/gates.go"}, Check: &types.LeaseCheck{Target: "compile", Project: "."}}

	got, _ := unrunGates(t.TempDir(), row, gatesWorkspace(), nil, workerGrade)

	byTarget := map[string]UnrunGate{}
	for _, g := range got {
		byTarget[g.Target] = g
	}
	assert.False(t, byTarget["lint"].IntegratorGates)
	assert.True(t, byTarget["go-test"].Goal.Check.NoDefaultCharms, "a test the worker may run only without default charms is offered that way")
	build := byTarget["build"]
	assert.True(t, build.IntegratorGates, "no form of the build may run")
	assert.Zero(t, build.Goal, "a goal no worker may run is not offered")

	lines := unrunLines(got)
	assert.Contains(t, lines, "build . (reads **/*.go): the integrator gates it; no worker may run it")
	assert.NotContains(t, lines[len(lines)-1], `"id":"build"`)

	ungraded, _ := unrunGates(t.TempDir(), row, gatesWorkspace(), nil, nil)
	for _, g := range ungraded {
		assert.False(t, g.IntegratorGates, "with no grader every goal is offered as it is")
	}
}

func TestRenderUnrunGatesCapsTheListAndPrintsTheGoals(t *testing.T) {
	t.Parallel()

	var gates []UnrunGate
	taken := map[string]bool{}
	for i := range unrunGatesLimit + 2 {
		gates = append(gates, UnrunGate{Target: fmt.Sprintf("t%d", i), Project: ".", Source: "**/*.go", Goal: unrunGoal(".", fmt.Sprintf("t%d", i), taken)})
	}
	var buf bytes.Buffer
	renderUnrunGates(&buf, gates)
	out := buf.String()
	assert.Contains(t, out, unrunHeading)
	assert.Contains(t, out, "t0 . (reads **/*.go)")
	assert.NotContains(t, out, fmt.Sprintf("t%d . (reads", unrunGatesLimit))
	assert.Contains(t, out, "and 2 more; -o json lists every one")
	assert.Contains(t, out, `goals to add: [{"id":"t0","kind":"check"`)

	buf.Reset()
	renderUnrunGates(&buf, nil)
	assert.Empty(t, buf.String(), "nothing to say prints no heading")
}
