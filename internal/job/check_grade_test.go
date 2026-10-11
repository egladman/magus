package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

const gradedTests = `package pkg

import "testing"

func TestAlpha(t *testing.T) {}
func TestBeta(t *testing.T) {}
func TestGamma_sub(t *testing.T) {}
func Testlower(t *testing.T) {}
func TestWrongType(t *testing.B) {}
func TestMain(m *testing.M) {}
func helper(t *testing.T) {}
func (s suite) TestMethod(t *testing.T) {}

type suite struct{}
`

const deepTests = "package x\n\nimport \"testing\"\n\nfunc TestDeep(t *testing.T) {}\n"

// testRoot writes pkg/ with sources and the tests above, other/ with sources only, and
// nested/x/ with a test of its own.
func testRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range [][2]string{
		{"pkg/a.go", "package pkg\n"},
		{"pkg/b.go", "package pkg\n"},
		{"pkg/a_test.go", gradedTests},
		{"other/o.go", "package other\n"},
		{"docs/page.md", "page\n"},
		{"nested/x/deep.go", "package x\n"},
		{"nested/x/d_test.go", deepTests},
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, f[0])), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, f[0]), []byte(f[1]), 0o644))
	}
	return root
}

func goTestCheck(args ...string) *types.LeaseCheck {
	return &types.LeaseCheck{Target: "go-test", Project: ".", Args: args}
}

func TestRefuseSkippedTestsListsWhatTheCheckSkips(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, testRoot(t))
	row := types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha")}

	err := RefuseSkippedTests(s, "w", row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkg: TestBeta, TestGamma_sub")
	for _, notATest := range []string{"TestAlpha,", "Testlower", "TestWrongType", "TestMain", "helper", "TestMethod"} {
		assert.NotContains(t, err.Error(), notATest)
	}
	assert.NotContains(t, err.Error(), "TestDeep", "a package none of the write paths is in is not the job's")

	const covering = "Alpha|^(TestBeta|TestGamma_sub)$"
	assert.Contains(t, err.Error(), "`"+covering+"`", "the refusal prints a pattern that covers them")
	row.Check = goTestCheck("-run", covering)
	require.NoError(t, RefuseSkippedTests(s, "w", row), "and that pattern is accepted")
}

func TestRefuseSkippedTestsReadsPatternsAsGoTestDoes(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, testRoot(t))
	for _, tc := range []struct {
		name    string
		check   *types.LeaseCheck
		refused string
	}{
		{"no pattern", goTestCheck(), ""},
		{"another flag", goTestCheck("-count=1", "-v"), ""},
		{"every test by a word", goTestCheck("-run", "Alpha|Beta|Gamma"), ""},
		{"an equals sign", goTestCheck("-run=Alpha"), "TestBeta"},
		{"the test. spelling", goTestCheck("-test.run", "Alpha"), "TestBeta"},
		{"two dashes", goTestCheck("--run", "Alpha"), "TestBeta"},
		{"the last -run wins", goTestCheck("-run", "Alpha", "-run", "Alpha|Beta|Gamma"), ""},
		{"shell quotes are removed", goTestCheck("-run", "'Alpha|Beta|Gamma'"), ""},
		{"an anchored list", goTestCheck("-run", "^(TestAlpha|TestBeta|TestGamma_sub)$"), ""},
		{"a subtest element selects", goTestCheck("-run", "Alpha/case"), "TestBeta"},
		{"the first element is read", goTestCheck("-run", "^Test(Alpha|Beta)$/case|Gamma"), ""},
		{"an escaped slash is no split", goTestCheck("-run", `Alpha\/x|Beta|Gamma`), "TestAlpha"},
		{"a slash in brackets is no split", goTestCheck("-run", `Alpha[/]x|Beta|Gamma`), "TestAlpha"},
		{"-skip a test", goTestCheck("-skip", "Beta"), "TestBeta"},
		{"-skip a subtest only", goTestCheck("-skip", "Beta/case"), ""},
		{"-skip an unrelated name", goTestCheck("-skip", "Delta"), ""},
		{"-run covers, -skip drops", goTestCheck("-run", "Test", "-skip", "Gamma"), "TestGamma_sub"},
		{"a script check", &types.LeaseCheck{Script: "probe.buzz", Args: []string{"-run", "Alpha"}}, ""},
		{"an invalid pattern", goTestCheck("-run", "("), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := RefuseSkippedTests(s, "w", types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: tc.check})
			if tc.refused == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.refused)
		})
	}
}

func TestRefuseSkippedTestsFindsPackagesFromWritePaths(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, testRoot(t))
	narrow := goTestCheck("-run", "NoSuchTest")
	for _, tc := range []struct {
		name    string
		paths   []string
		refused string
	}{
		{"a file", []string{"pkg/b.go"}, "pkg: TestAlpha"},
		{"a glob of files", []string{"pkg/*.go"}, "pkg: TestAlpha"},
		{"a claim of a declaration", []string{"pkg/b.go#Helper"}, "pkg: TestAlpha"},
		{"a glob across directories", []string{"nested/**/deep.go"}, "nested/x: TestDeep"},
		{"two packages", []string{"pkg/a.go", "nested/x/deep.go"}, "nested/x: TestDeep"},
		{"a package without tests", []string{"other/o.go"}, ""},
		{"a file not written yet", []string{"pkg/new.go"}, "pkg: TestAlpha"},
		{"a directory that does not exist", []string{"nowhere/new.go"}, ""},
		{"a non-Go file", []string{"docs/page.md"}, ""},
		{"a test file", []string{"pkg/a_test.go"}, "pkg: TestAlpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := RefuseSkippedTests(s, "w", types.Job{ID: "w", WritePaths: tc.paths, Check: narrow})
			if tc.refused == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.refused)
		})
	}

	t.Run("a store with no root", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, RefuseSkippedTests(tmpStore(t, ""), "w", types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: narrow}))
		require.NoError(t, RefuseSkippedTests(nil, "w", types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: narrow}))
	})
}

func TestForkAndApplyRefuseASkippingCheck(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	narrow := goTestCheck("-run", "Alpha")
	paths := []string{"pkg/a.go"}

	s := tmpStore(t, testRoot(t))
	_, err := ForkMerge(ctx, s, "w", func(u *types.Job) { u.WritePaths, u.Check = paths, narrow }, config.Jobs{}, nil)
	require.ErrorContains(t, err, "TestBeta", "a fork by merge, the tool's put")

	_, err = Apply(ctx, s, []types.Declaration{{ID: "w", WritePaths: paths, Check: narrow}}, config.Jobs{}, nil, nil, true)
	require.ErrorContains(t, err, "TestBeta", "job apply, a dry run included")

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused fork wrote nothing")

	_, err = ForkMerge(ctx, s, "w", func(u *types.Job) { u.WritePaths, u.Check = paths, goTestCheck() }, config.Jobs{}, nil)
	require.NoError(t, err)

	_, err = ForkMerge(ctx, s, "w", func(u *types.Job) { u.Check = narrow }, config.Jobs{}, nil)
	require.ErrorContains(t, err, "TestBeta", "narrowing the check of a row that exists")

	_, err = ForkMerge(ctx, s, "w", func(u *types.Job) { u.WritePaths = []string{"pkg/a.go", "pkg/b.go"} }, config.Jobs{}, nil)
	require.NoError(t, err, "an update that leaves the check alone is held to nothing new")
}

func TestForkLetsARowThatAlreadySkipsTakeAnUnrelatedUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := tmpStore(t, testRoot(t))
	seed(t, s, types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha"), State: types.StateDeclared})

	_, err := ForkMerge(ctx, s, "w", func(u *types.Job) { u.Model = "opus" }, config.Jobs{}, nil)
	require.NoError(t, err)
}

func graderStore(t *testing.T, verdict CheckVerdict, calls *[]types.Job) *Store {
	t.Helper()
	loc := tmpLoc(t, testRoot(t))
	loc.Grader = func(_ context.Context, row types.Job) CheckVerdict {
		*calls = append(*calls, row)
		return verdict
	}
	return NewStore(loc)
}

func TestRefuseDeniedCheckQuotesTheVerdictAndNamesATargetThatCompiles(t *testing.T) {
	t.Parallel()

	var calls []types.Job
	s := graderStore(t, CheckVerdict{Denied: true, Rule: "worker-builds-magus", Reason: "a worker does not build magus;\nask the main session"}, &calls)
	build := &types.LeaseCheck{Target: "go-build", Project: "."}

	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = []string{"pkg/b.go"}, build }, config.Jobs{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "w's check `magus run go-build .` would be denied to its worker by the guard (worker-builds-magus)")
	assert.Contains(t, err.Error(), "a worker does not build magus;\n    ask the main session", "the verdict is quoted")
	assert.Contains(t, err.Error(), "`go::go-vet .`", "the remedy names a target that compiles without linking the binary")
	require.Len(t, calls, 1)
	assert.Equal(t, "w", calls[0].ID, "the grader is handed the row, its id the lease to grade under")
	assert.Equal(t, build, calls[0].Check)

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused fork wrote nothing")

	_, err = Apply(t.Context(), s, []types.Declaration{{ID: "w", WritePaths: []string{"pkg/b.go"}, Check: build}}, config.Jobs{}, nil, nil, true)
	require.ErrorContains(t, err, "would be denied to its worker", "job apply grades too")
}

func TestRefuseDeniedCheckAcceptsWhatTheGuardLets(t *testing.T) {
	t.Parallel()

	var calls []types.Job
	s := graderStore(t, CheckVerdict{}, &calls)
	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = []string{"pkg/b.go"}, forkCheck() }, config.Jobs{}, nil)
	require.NoError(t, err)
	require.Len(t, calls, 1)

	_, err = ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.Model = "opus" }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, calls, 1, "an update that leaves the check alone is not graded again")

	_, err = ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.Check = &types.LeaseCheck{Target: "lint", Project: "."} }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, calls, 2, "a changed check is")

	require.NoError(t, RefuseDeniedCheck(t.Context(), tmpStore(t, t.TempDir()), types.Job{ID: "w", Check: forkCheck()}), "a store with no grader refuses nothing")
	_, err = ForkMerge(t.Context(), s, "scout", func(u *types.Job) { u.ReadOnly = true }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, calls, 2, "a job with no check has nothing to grade")
}
