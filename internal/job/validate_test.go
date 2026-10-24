package job

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// validateWorkspace is regeneratedWorkspace with the targets its root project exports and a
// ci chain that lints and tests, so every workspace check has something to read.
func validateWorkspace(t *testing.T) (*Store, []*types.Project) {
	t.Helper()
	root, projects := regeneratedWorkspace(t)
	top := projects[0]
	top.MagusfileTargets = []string{"ci", "lint", "test", "man-generate", "config-generate", "types-generate"}
	top.TargetInputs["lint"] = []types.InputRef{{Glob: "**/*.go"}}
	top.TargetChains = map[string][]types.ChainStep{"ci": {{Target: "lint"}, {Target: "test"}}}
	projects[1].MagusfileTargets = []string{"content-generate", "site-generate"}
	return NewStore(tempLocation(t, root)).InWorkspace(Workspace{Projects: projects}), projects
}

func codes(ps types.JobProblems) []types.DiagnosticCode {
	out := make([]types.DiagnosticCode, len(ps))
	for i, p := range ps {
		out[i] = p.Code
	}
	return out
}

func problemAt(t *testing.T, ps types.JobProblems, code types.DiagnosticCode) types.JobProblem {
	t.Helper()
	i := slices.IndexFunc(ps, func(p types.JobProblem) bool { return p.Code == code })
	require.GreaterOrEqual(t, i, 0, "no %s in %v", code, codes(ps))
	return ps[i]
}

func TestValidateMergeCollectsEveryProblem(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	v := validateMerge(t.Context(), s, nil, "w", func(u *types.Job) {
		u.WritePaths = []string{"cli", "cli/registry.go"}
		u.Check = &types.LeaseCheck{Target: "nosuch", Project: "."}
		u.Goals = []types.Goal{{ID: "elsewhere", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "test", Project: "nowhere"}}}
	}, config.Jobs{}, nil)

	got := codes(v.Problems)
	for _, want := range []types.DiagnosticCode{types.WritePathIsDirectory, types.JobCheckUnrunnable, types.OutputOutsideWritePaths, types.GateNotRun} {
		assert.Contains(t, got, want, "every problem at once")
	}
	assert.Equal(t, "write_paths[0]", problemAt(t, v.Problems, types.WritePathIsDirectory).Field)

	var target, project types.JobProblem
	for _, p := range v.Problems {
		switch p.Field {
		case "check.target":
			target = p
		case "goals[0].check.project":
			project = p
		}
	}
	assert.Contains(t, target.Message, `names target "nosuch", which project "." does not run`)
	assert.Contains(t, target.Fix, "config-generate, lint", "an unknown target names the project's targets")
	assert.Contains(t, project.Message, `names project "nowhere"`)
	assert.Equal(t, "name one of its projects: ., docs", project.Fix)

	regen := problemAt(t, v.Problems, types.OutputOutsideWritePaths)
	assert.NotEmpty(t, regen.Subject, "a permissible problem names what a deviation names")
	assert.Contains(t, regen.Fix, `"reason":"the root regenerates it"`)
	gate := problemAt(t, v.Problems, types.GateNotRun)
	assert.Equal(t, "lint .", gate.Subject)
	assert.Contains(t, gate.Fix, `"check":{"target":"lint","project":"."}`, "the fix is the goal that runs it")
	assert.Empty(t, v.Unchecked)
	assert.Equal(t, "w", v.Job.ID, "the result carries the row as it would be stored")
}

func TestValidateMergePermitsOnlyWhatTheRecordNames(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(deviations ...types.Deviation) func(*types.Job) {
		return func(u *types.Job) {
			u.WritePaths, u.Check, u.Deviations = []string{"cli/registry.go"}, forkCheck(), deviations
		}
	}
	v := validateMerge(t.Context(), s, nil, "w", declare(), config.Jobs{}, nil)
	require.NotEmpty(t, v.Problems)
	var deviations []types.Deviation
	for _, p := range v.Problems {
		require.True(t, types.Permissible(p.Code), "only permissible problems are left: %v", p)
		deviations = append(deviations, types.Deviation{Code: p.Code, Subject: p.Subject, Reason: "the root regenerates and runs it"})
	}

	permitted := validateMerge(t.Context(), s, nil, "w", declare(deviations...), config.Jobs{}, nil)
	assert.Empty(t, permitted.Problems)
	assert.Equal(t, v.Problems, permitted.Permitted)

	stored, err := ForkMerge(t.Context(), s, "w", declare(deviations...), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.NotZero(t, stored.Validated, "a validated row is stamped")
	assert.Equal(t, deviations, stored.Deviations)

	wrong := validateMerge(t.Context(), s, nil, "w2", func(u *types.Job) {
		u.WritePaths, u.Check = []string{"cli"}, forkCheck()
		u.Deviations = []types.Deviation{{Code: types.WritePathIsDirectory, Subject: "cli", Reason: "it is mine"}, {Code: types.GateNotRun, Subject: "lint ."}}
	}, config.Jobs{}, nil)
	fields := map[string]types.DiagnosticCode{}
	for _, p := range wrong.Problems {
		fields[p.Field] = p.Code
	}
	assert.Equal(t, types.JobRecordInvalid, fields["deviations[0].code"], "a deviation on a code that is not permissible is itself a problem")
	assert.Equal(t, types.JobRecordInvalid, fields["deviations[1].reason"], "and so is one with no reason")
	assert.Equal(t, types.WritePathIsDirectory, fields["write_paths[0]"], "and it permits nothing")
}

func TestValidateMergeNamesWhatItCouldNotCheck(t *testing.T) {
	t.Parallel()

	root, _ := regeneratedWorkspace(t)
	merge := func(u *types.Job) { u.WritePaths, u.Check = []string{"cli/registry.go"}, forkCheck() }

	v := validateMerge(t.Context(), NewStore(tempLocation(t, root)), nil, "w", merge, config.Jobs{}, nil)
	assert.Empty(t, v.Problems)
	assert.Len(t, v.Unchecked, 4, "the target, outputs, gates and breadth checks each say they did not run")
	for _, why := range v.Unchecked {
		assert.Contains(t, why, "without a workspace")
	}

	unavailable := NewStore(tempLocation(t, root)).InWorkspace(Workspace{Unavailable: "magusfile.buzz: line 3: unexpected }"})
	v = validateMerge(t.Context(), unavailable, nil, "w", merge, config.Jobs{}, nil)
	require.Len(t, v.Unchecked, 4)
	assert.Contains(t, v.Unchecked[0], "the workspace did not load: magusfile.buzz: line 3")

	v = validateMerge(t.Context(), unavailable, nil, "scout", func(u *types.Job) { u.ReadOnly = true }, config.Jobs{}, nil)
	assert.Empty(t, v.Unchecked, "a read-only row with no check skips nothing it would have needed")
}

func TestValidateForkWritesNothing(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	v, err := ValidateFork(t.Context(), s, "w", func(u *types.Job) {
		u.WritePaths, u.Check, u.Checkpoint = []string{"cli"}, forkCheck(), "abc+zz"
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	got := codes(v.Problems)
	assert.Contains(t, got, types.JobCheckpointUnplaceable, "the checkpoint is judged in the same pass")
	assert.Contains(t, got, types.WritePathIsDirectory)
	assert.Equal(t, "checkpoint", problemAt(t, v.Problems, types.JobCheckpointUnplaceable).Field)

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a dry run writes nothing")

	_, err = ForkMerge(t.Context(), s, "w", func(u *types.Job) {
		u.WritePaths, u.Check, u.Checkpoint = []string{"cli"}, forkCheck(), "abc+zz"
	}, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.JobCheckpointUnplaceable)
	require.ErrorIs(t, err, types.WritePathIsDirectory, "a fork reports both, not the first")
}

func TestValidateMergeJudgesAnUpdateByWhatItAdds(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	seed(t, s, types.Job{ID: "w", State: types.StateRunning, WritePaths: []string{"cli/**", "docs/**"}, Check: forkCheck()})
	rows, err := s.List()
	require.NoError(t, err)

	narrowed := validateMerge(t.Context(), s, rows, "w", func(u *types.Job) { u.WritePaths = []string{"cli/registry.go"} }, config.Jobs{}, nil)
	assert.Empty(t, narrowed.Problems, "narrowing a glob to a file it covers adds nothing")

	widened := validateMerge(t.Context(), s, rows, "w", func(u *types.Job) { u.WritePaths = []string{"cli/**", "docs/**", "cmd"} }, config.Jobs{}, nil)
	assert.Equal(t, "write_paths[2]", problemAt(t, widened.Problems, types.WritePathIsDirectory).Field,
		"a problem found on what the update adds names the field in the whole row")
	assert.NotContains(t, codes(widened.Problems), types.JobRecordInvalid, "a shape the row already had is not the update's")
}

func TestApplyCollectsEveryRecordsProblems(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	_, err := Apply(t.Context(), s, []types.Declaration{
		{ID: "a", WritePaths: []string{"cli"}, Check: forkCheck()},
		{ID: "b", WritePaths: []string{"cli/registry.go"}, State: types.StateRunning, Check: forkCheck()},
	}, config.Jobs{}, nil, nil, false)
	require.ErrorIs(t, err, types.WritePathIsDirectory)
	require.ErrorIs(t, err, types.JobRecordInvalid)
	assert.Contains(t, err.Error(), `a: "cli", inside project "." is a directory`, "each message names its record")
	assert.Contains(t, err.Error(), `b: the record for b carries state "running"`)

	rows, lerr := s.List()
	require.NoError(t, lerr)
	assert.Empty(t, rows, "nothing is written while any record has a problem")
}

func TestValidateMergePermitsEveryRegeneratedOutputByGlob(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(deviations ...types.Deviation) func(*types.Job) {
		return func(u *types.Job) {
			u.WritePaths, u.Check, u.Deviations = []string{"cli/registry.go"}, forkCheck(), deviations
		}
	}
	bare := validateMerge(t.Context(), s, nil, "w", declare(), config.Jobs{}, nil)
	require.Greater(t, len(bare.Problems), 2, "several outputs and a gate stand unpermitted")

	outputs := validateMerge(t.Context(), s, nil, "w", declare(types.Deviation{Code: types.OutputOutsideWritePaths, Subject: "**", Reason: "the integrator regenerates it"}), config.Jobs{}, nil)
	assert.Equal(t, []types.DiagnosticCode{types.GateNotRun}, slices.Compact(codes(outputs.Problems)), "a glob permits its own code alone")

	both := validateMerge(t.Context(), s, nil, "w", declare(
		types.Deviation{Code: types.OutputOutsideWritePaths, Subject: "**", Reason: "the integrator regenerates it"},
		types.Deviation{Code: types.GateNotRun, Subject: "**", Reason: "the integrator runs it"},
	), config.Jobs{}, nil)
	assert.Equal(t, []types.DiagnosticCode{types.GateNotRun}, codes(both.Problems), "lint is the one gate a glob cannot permit")
	assert.Len(t, both.Permitted, len(bare.Problems)-1)

	narrow := validateMerge(t.Context(), s, nil, "w", declare(types.Deviation{Code: types.OutputOutsideWritePaths, Subject: "docs/**", Reason: "the integrator regenerates it"}), config.Jobs{}, nil)
	assert.Contains(t, codes(narrow.Problems), types.OutputOutsideWritePaths, "a glob permits only the subjects it matches")
	assert.Less(t, len(narrow.Problems), len(bare.Problems))
}

func TestIntegratorDeviationsAcceptEveryOutputButADeniedOne(t *testing.T) {
	t.Parallel()

	root, projects := regeneratedWorkspace(t)
	row := types.Job{WritePaths: []string{"cli/registry.go"}, DenyPaths: []string{"cli/testdata/api.lock"}}

	got := IntegratorDeviations(root, row, projects, ignoresCoverage)

	subjects := make([]string, len(got))
	for i, w := range got {
		assert.Equal(t, types.OutputOutsideWritePaths, w.Code)
		assert.Equal(t, integratorRegenerates, w.Reason)
		subjects[i] = w.Subject
	}
	assert.Contains(t, subjects, "docs/manpage/*.md")
	assert.Contains(t, subjects, "cmd/gen/*.go")
	assert.NotContains(t, subjects, "cli/testdata/api.lock", "a denied output stays refused")

	row.Deviations = got
	assert.Empty(t, IntegratorDeviations(root, row, projects, ignoresCoverage), "an output already permitted needs no second deviation")
}

func TestExtendFootprintAddsSiblingTests(t *testing.T) {
	t.Parallel()

	root, projects := regeneratedWorkspace(t)
	row := types.Job{WritePaths: []string{"cli/registry.go"}, DenyPaths: []string{"cli/testdata/api.lock"}}
	referrers := func(file, _ string) map[string]int {
		if file != "cli/registry.go" {
			return nil
		}
		return map[string]int{"cli/registry_test.go": 3, "cmd/main_test.go": 1, "cmd/main.go": 2}
	}

	paths, notes := ExtendFootprint(root, row, projects, referrers)

	assert.Equal(t, []string{"cli/registry_test.go"}, paths, "a test beside a written file is reached; outputs and other directories are not")
	assert.Len(t, notes, len(paths), "each added path says why")

	none, _ := ExtendFootprint(root, types.Job{ReadOnly: true, WritePaths: row.WritePaths}, projects, referrers)
	assert.Empty(t, none)
}

func TestValidateMergePermitsLintOnlyByItsExactSubject(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(deviations ...types.Deviation) func(*types.Job) {
		return func(u *types.Job) {
			u.WritePaths, u.Check, u.Deviations = []string{"cli/registry.go"}, forkCheck(), deviations
		}
	}
	glob := types.Deviation{Code: types.GateNotRun, Subject: "**", Reason: "the integrator runs it"}
	got := validateMerge(t.Context(), s, nil, "w", declare(glob), config.Jobs{}, nil)
	lint := problemAt(t, got.Problems, types.GateNotRun)
	assert.Equal(t, "lint .", lint.Subject, "a glob leaves the lint gate standing")

	exact := types.Deviation{Code: types.GateNotRun, Subject: "lint .", Reason: "the root lints this one"}
	got = validateMerge(t.Context(), s, nil, "w", declare(glob, exact), config.Jobs{}, nil)
	assert.NotContains(t, codes(got.Problems), types.GateNotRun)
	assert.Contains(t, codes(got.Permitted), types.GateNotRun)
}

func TestResolveMergeAddsALintGoalForGoSourceAndSaysSo(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(paths ...string) func(*types.Job) {
		return func(u *types.Job) { u.WritePaths, u.Check = paths, forkCheck() }
	}
	resolve := func(merge func(*types.Job)) (types.Job, string) {
		var notes bytes.Buffer
		s.notices = &notes
		merged, err := resolveMerge(t.Context(), s, nil, "w", merge, nil)
		require.NoError(t, err)
		var row types.Job
		merged(&row)
		return row, notes.String()
	}

	row, note := resolve(declare("cli/registry.go"))
	i := slices.IndexFunc(row.Goals, func(g types.Goal) bool { return g.ID == "lint" })
	require.GreaterOrEqual(t, i, 0, "goals: %v", row.Goals)
	assert.Equal(t, types.LeaseCheck{Target: "lint", Project: ".", NoDefaultCharms: true}, row.Goals[i].Check)
	assert.Contains(t, note, `added the goal "lint"`)

	row, note = resolve(func(u *types.Job) {
		declare("cli/registry.go")(u)
		u.Goals = []types.Goal{types.Goal{ID: "lint", Check: types.LeaseCheck{Target: "lint", Project: "."}}.Resolve()}
	})
	assert.Len(t, row.Goals, 1, "a goal that runs lint is kept alone")
	assert.Empty(t, note)

	row, note = resolve(declare("docs/notes.md"))
	assert.Empty(t, row.Goals, "no Go source, no lint goal")
	assert.Empty(t, note)
}

func TestAmbiguousSymbolProblemsRefuseAnUnresolvedGoalOnAnExistingSymbol(t *testing.T) {
	t.Parallel()

	read := func(_ context.Context, name, _ string) (SymbolFact, bool) {
		if name == "job.Declare" {
			return SymbolFact{DefinedIn: []string{"internal/job/plan.go"}}, true
		}
		return SymbolFact{}, true
	}
	goals := []types.Goal{
		{ID: "gone", Kind: types.GoalKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"job.Declare", "job.Typo"}},
		{ID: "new", Kind: types.GoalKindSymbol, Expect: types.ExpectPresent, Symbols: []string{"job.Typo"}},
		{ID: "bare", Kind: types.GoalKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"Typo"}},
	}

	ps := ambiguousSymbolProblems(t.Context(), goals, read)

	require.Len(t, ps, 1, "a present goal names what the job will write, and a bare name may be a graph without an index")
	assert.Equal(t, "goals[0].symbols[1]", ps[0].Field)
	assert.Contains(t, ps[0].Message, `"job.Typo"`)
}

func TestDirectoryWritePathStandsDownForAnAllOutputDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, rel := range []string{
		"docs/gen/a.md", "docs/gen/sub/b.md",
		"docs/mixed/c.md", "docs/mixed/hand.txt",
		"docs/src/page.buzz",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(rel), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs", "empty"), 0o755))
	projects := []*types.Project{{
		Path: "docs",
		TargetOutputs: map[string][]types.OutputRef{
			"site-generate": {{Glob: "gen/**"}, {Glob: "mixed/*.md"}, {Glob: "empty/*.md"}},
		},
	}}
	store := NewStore(tempLocation(t, root)).InWorkspace(Workspace{Projects: projects})
	problems := func(paths ...string) types.JobProblems {
		return directoryWritePathProblems(store, types.Job{WritePaths: paths})
	}

	assert.Empty(t, problems("docs/gen"), "every file under it is a declared output")
	assert.Empty(t, problems("docs/gen/**"), "a wildcard subtree reads as the directory")

	ps := problems("docs/mixed")
	require.Len(t, ps, 1)
	assert.Equal(t, types.WritePathIsDirectory, ps[0].Code)
	assert.Contains(t, ps[0].Message, "docs/mixed/hand.txt is not a declared output", "the message names the file that keeps the refusal")

	assert.Len(t, problems("docs/src"), 1, "a source file keeps the refusal")
	assert.Len(t, problems("docs/empty"), 1, "a directory with no file shows nothing generated")
	assert.Len(t, problems("**"), 1, "a bare ** stays refused")
	assert.Len(t, problems("."), 1)

	bare := NewStore(tempLocation(t, root))
	assert.Len(t, directoryWritePathProblems(bare, types.Job{WritePaths: []string{"docs/gen"}}), 1, "with no workspace nothing is known to be generated")

	row := types.Job{WritePaths: []string{"docs/gen"}}
	assert.True(t, outputInside(row, types.Glob{Pattern: "docs/gen/*.md"}), "the directory claim holds an output glob under it (MGS3044)")
	assert.False(t, outputInside(row, types.Glob{Pattern: "docs/mixed/*.md"}))
}
