package job

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
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
func testRoot(t *testing.T) *testkit.Workspace {
	t.Helper()
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"pkg/a.go":           "package pkg\n",
		"pkg/b.go":           "package pkg\n",
		"pkg/a_test.go":      gradedTests,
		"other/o.go":         "package other\n",
		"docs/page.md":       "page\n",
		"nested/x/deep.go":   "package x\n",
		"nested/x/d_test.go": deepTests,
	})
	return w
}

func goTestCheck(args ...string) *types.LeaseCheck {
	return &types.LeaseCheck{Target: "go-test", Project: ".", Args: args}
}

// refuseNarrowedIn is narrowedTestProblems with a store opened at root, which is all it
// reads, each problem rendered as its message and fix on one line.
func refuseNarrowedIn(root string, row types.Job) error {
	var errs []error
	for _, p := range narrowedTestProblems(&Store{storeConfig: storeConfig{root: root}}, row) {
		if p.Code != types.JobCheckUnrunnable {
			return fmt.Errorf("code %s, want %s", string(p.Code), string(types.JobCheckUnrunnable))
		}
		errs = append(errs, errors.New(p.Message+": "+p.Fix))
	}
	return errors.Join(errs...)
}

func TestRefuseNarrowedTestsListsWhatTheCheckLeavesOut(t *testing.T) {
	t.Parallel()

	root := testRoot(t).Root()
	row := types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha")}

	err := refuseNarrowedIn(root, row)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\n", "an error is one line")
	assert.Contains(t, err.Error(), "pkg (TestBeta, TestGamma_sub)")
	for _, notATest := range []string{"TestAlpha,", "Testlower", "TestWrongType", "TestMain", "helper", "TestMethod"} {
		assert.NotContains(t, err.Error(), notATest)
	}
	assert.NotContains(t, err.Error(), "TestDeep", "a package none of the write paths is in is not the job's")

	const covering = "Alpha|^(TestBeta|TestGamma_sub)$"
	assert.Contains(t, err.Error(), "`"+covering+"`", "the refusal prints a pattern that covers them")
	row.Check = goTestCheck("-run", covering)
	require.NoError(t, refuseNarrowedIn(root, row), "and that pattern is accepted")
}

func TestRefuseNarrowedTestsReadsPatternsAsGoTestDoes(t *testing.T) {
	t.Parallel()

	root := testRoot(t).Root()
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
		{"an element that does not compile keeps its place", goTestCheck("-run", "*bad/Sub"), ""},
		{"a subtest alternative first leaves the test running", goTestCheck("-skip", "Beta/case|Beta"), ""},
		{"a whole-test alternative first skips it", goTestCheck("-skip", "Beta|Beta/case"), "TestBeta"},
		{"another tool's -run is not go test's", &types.LeaseCheck{Target: "cargo-test", Project: ".", Args: []string{"-run", "Alpha"}}, ""},
		{"the spell-qualified go test", &types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Alpha"}}, "TestBeta"},
		{"the test target forwards -run to go test", &types.LeaseCheck{Target: "test", Project: ".", Args: []string{"-run", "Alpha"}}, "TestBeta"},
		{"the test target under a charm", &types.LeaseCheck{Target: "test:extended", Project: ".", Args: []string{"-skip", "Beta"}}, "TestBeta"},
		{"the test target covering every test", &types.LeaseCheck{Target: "test", Project: ".", Args: []string{"-run", "Alpha|Beta|Gamma"}}, ""},
		{"another spell's go-test op", &types.LeaseCheck{Target: "bazel::go-test", Project: ".", Args: []string{"-run", "Alpha"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := refuseNarrowedIn(root, types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: tc.check})
			if tc.refused == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.refused)
		})
	}
}

func TestRefuseNarrowedTestsFindsPackagesFromWritePaths(t *testing.T) {
	t.Parallel()

	root := testRoot(t).Root()
	narrow := goTestCheck("-run", "NoSuchTest")
	for _, tc := range []struct {
		name    string
		paths   []string
		refused string
	}{
		{"a file", []string{"pkg/b.go"}, "pkg (TestAlpha"},
		{"a glob of files", []string{"pkg/*.go"}, "pkg (TestAlpha"},
		{"a claim of a declaration", []string{"pkg/b.go#Helper"}, "pkg (TestAlpha"},
		{"a glob across directories", []string{"nested/**/deep.go"}, "nested/x (TestDeep"},
		{"two packages", []string{"pkg/a.go", "nested/x/deep.go"}, "nested/x (TestDeep"},
		{"a package without tests", []string{"other/o.go"}, ""},
		{"a file not written yet", []string{"pkg/new.go"}, "pkg (TestAlpha"},
		{"a directory that does not exist", []string{"nowhere/new.go"}, ""},
		{"a non-Go file", []string{"docs/page.md"}, ""},
		{"a test file", []string{"pkg/a_test.go"}, "pkg (TestAlpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := refuseNarrowedIn(root, types.Job{ID: "w", WritePaths: tc.paths, Check: narrow})
			if tc.refused == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.refused)
		})
	}

	t.Run("no root", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, refuseNarrowedIn("", types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: narrow}))
		assert.Empty(t, narrowedTestProblems(nil, types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: narrow}), "nor a nil store")
	})
}

// A write path that is a pattern reads packages the way go list does: fixtures under
// testdata, vendored code, hidden and underscore directories and nested modules are not the
// job's packages.
func TestRefuseNarrowedTestsLeavesOutWhatGoListLeavesOut(t *testing.T) {
	t.Parallel()

	w := testRoot(t)
	root := w.Root()
	for rel, name := range map[string]string{
		"pkg/testdata/fixture/f_test.go": "TestFixture",
		"vendor/v/v_test.go":             "TestVendored",
		".claude/worktrees/w/w_test.go":  "TestWorktree",
		"_scratch/s_test.go":             "TestScratch",
		"lib/l_test.go":                  "TestNestedModule",
	} {
		w.Write(rel, "package x\n\nimport \"testing\"\n\nfunc "+name+"(t *testing.T) {}\n")
	}
	w.Write("lib/go.mod", "module lib\n")

	err := refuseNarrowedIn(root, types.Job{ID: "w", WritePaths: []string{"**/*.go"}, Check: goTestCheck("-run", "NoSuchTest")})
	require.Error(t, err)
	for _, tested := range []string{"pkg (TestAlpha", "nested/x (TestDeep"} {
		assert.Contains(t, err.Error(), tested)
	}
	for _, left := range []string{"TestFixture", "TestVendored", "TestWorktree", "TestScratch", "TestNestedModule"} {
		assert.NotContains(t, err.Error(), left)
	}
}

// A test plain `go test` never builds is not one the check leaves out: a file behind a build
// tag, one for another platform, and one go ignores by its name.
func TestRefuseNarrowedTestsCountsOnlyTestsGoTestBuilds(t *testing.T) {
	t.Parallel()

	w := testRoot(t)
	other := "windows"
	if runtime.GOOS == other {
		other = "darwin"
	}
	w.WriteTree(map[string]string{
		"pkg/tagged_test.go":           "//go:build integration\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) {}\n",
		"pkg/os_" + other + "_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestOtherPlatform(t *testing.T) {}\n",
		"pkg/_ignored_test.go":         "package pkg\n\nimport \"testing\"\n\nfunc TestIgnored(t *testing.T) {}\n",
		"pkg/.hidden_test.go":          "package pkg\n\nimport \"testing\"\n\nfunc TestHidden(t *testing.T) {}\n",
	})

	require.NoError(t, refuseNarrowedIn(w.Root(), types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha|Beta|Gamma")}))
}

// The check's own -tags decide which files it builds, so a tagged test it builds and leaves
// out is refused.
func TestRefuseNarrowedTestsBuildsWithTheChecksTags(t *testing.T) {
	t.Parallel()

	w := testRoot(t)
	w.WriteTree(map[string]string{
		"pkg/tagged_test.go": "//go:build integration\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) {}\n",
	})
	for _, args := range [][]string{
		{"-run", "Alpha|Beta|Gamma", "-tags", "integration"},
		{"-tags=other,integration", "-run", "Alpha|Beta|Gamma"},
	} {
		err := refuseNarrowedIn(w.Root(), types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck(args...)})
		require.Error(t, err, args)
		assert.Contains(t, err.Error(), "pkg (TestTagged)", args)
	}
}

// A file whose build constraint does not read is one go test would report, never one it
// silently leaves out, so its tests count.
func TestRefuseNarrowedTestsCountsAFileWhoseConstraintDoesNotRead(t *testing.T) {
	t.Parallel()

	w := testRoot(t)
	w.WriteTree(map[string]string{
		"pkg/broken_test.go": "//go:build (\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestUnread(t *testing.T) {}\n",
	})

	err := refuseNarrowedIn(w.Root(), types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha|Beta|Gamma")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkg (TestUnread)")
}

// A check goal narrowed with -run is held to the same rule as the job's own check.
func TestRefuseNarrowedTestsGradesEveryCheckGoal(t *testing.T) {
	t.Parallel()

	root := testRoot(t).Root()
	row := types.Job{
		ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck(),
		Goals: []types.Goal{{ID: "unit", Kind: types.GoalKindCheck, Check: *goTestCheck("-run", "Alpha")}},
	}

	err := refuseNarrowedIn(root, row)

	require.ErrorContains(t, err, `w's goal "unit" `+"`magus run go-test . -- -run Alpha`")
	assert.Contains(t, err.Error(), "pkg (TestBeta, TestGamma_sub)")
}

// A package with hundreds of tests would print them all, in one line. The refusal names the
// first few per package, counts the rest, and prints a covering pattern only when a person
// could paste it.
func TestRefuseNarrowedTestsCapsWhatItLists(t *testing.T) {
	t.Parallel()

	w := testRoot(t)
	root := w.Root()
	var many strings.Builder
	many.WriteString("package big\n\nimport \"testing\"\n\n")
	for i := range 200 {
		fmt.Fprintf(&many, "func TestCase%03d(t *testing.T) {}\n", i)
	}
	w.Write("big/b.go", "package big\n")
	w.Write("big/b_test.go", many.String())

	err := refuseNarrowedIn(root, types.Job{ID: "w", WritePaths: []string{"big/b.go"}, Check: goTestCheck("-run", "TestCase000")})
	require.Error(t, err)
	msg := err.Error()
	assert.NotContains(t, msg, "\n", "an error is one line")
	assert.Contains(t, msg, "big (TestCase001, TestCase002, TestCase003, TestCase004, TestCase005 and 194 more)")
	assert.NotContains(t, msg, "TestCase006", "only the first few are named")
	assert.Contains(t, msg, "drop -run", "a pattern for 199 tests is not pasteable")
	assert.NotContains(t, msg, "widen -run to")
	assert.Less(t, len(msg), 600)

	few := refuseNarrowedIn(root, types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha")})
	require.Error(t, few)
	assert.Contains(t, few.Error(), "pkg (TestBeta, TestGamma_sub)", "a short list is named whole")
	assert.NotContains(t, few.Error(), "more)")
	assert.Contains(t, few.Error(), "widen -run to `Alpha|^(TestBeta|TestGamma_sub)$`", "and a short pattern is printed")

	skipped := refuseNarrowedIn(root, types.Job{ID: "w", WritePaths: []string{"big/b.go"}, Check: goTestCheck("-skip", "TestCase")})
	require.Error(t, skipped)
	assert.NotContains(t, skipped.Error(), "\n")
	assert.Contains(t, skipped.Error(), "and 195 more)")
	assert.Contains(t, skipped.Error(), "drop -skip")
}

func TestForkAndApplyRefuseACheckThatLeavesOutATest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	narrow := goTestCheck("-run", "Alpha")
	paths := []string{"pkg/a.go"}

	s := tempStore(t, testRoot(t).Root())
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

	_, err = ForkMerge(ctx, s, "w", func(u *types.Job) {
		u.Goals = append(u.Goals, types.Goal{ID: "unit", Kind: types.GoalKindCheck, Check: *narrow}.Resolve())
	}, config.Jobs{}, nil)
	require.ErrorContains(t, err, `goal "unit"`, "adding a narrowed goal to a row that exists")

	_, err = ForkMerge(ctx, s, "w", func(u *types.Job) { u.WritePaths = []string{"pkg/a.go", "pkg/b.go"} }, config.Jobs{}, nil)
	require.NoError(t, err, "an update that leaves the check alone is held to nothing new")
}

func TestForkLetsARowThatAlreadyNarrowsTakeAnUnrelatedUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := tempStore(t, testRoot(t).Root())
	seed(t, s, types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha"), State: types.StateDeclared})

	_, err := ForkMerge(ctx, s, "w", func(u *types.Job) { u.Model = "opus" }, config.Jobs{}, nil)
	require.NoError(t, err)
}

// An update that keeps the check grades the packages of the paths it adds, not the ones the
// row already held, which its own check was accepted against.
func TestForkGradesOnlyTheAddedWritePathsOfAnUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	w := testRoot(t)
	w.Write("other/o_test.go", "package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
	s := tempStore(t, w.Root())
	seed(t, s, types.Job{ID: "w", WritePaths: []string{"pkg/a.go"}, Check: goTestCheck("-run", "Alpha"), State: types.StateDeclared})

	_, err := ForkMerge(ctx, s, "w", func(u *types.Job) { u.WritePaths = append(u.WritePaths, "other/o.go") }, config.Jobs{}, nil)
	require.ErrorContains(t, err, "other (TestOther)", "the added package is graded")
	assert.NotContains(t, err.Error(), "pkg (", "and the package the row already held is not")

	_, err = ForkMerge(ctx, s, "w", func(u *types.Job) { u.WritePaths = append(u.WritePaths, "docs/page.md") }, config.Jobs{}, nil)
	require.NoError(t, err, "a path that names no Go package adds nothing to grade")
}

// A check scoped to the packages of the write paths leaves the rest of the suite to the
// integration's full go-test, so it may narrow those packages' tests too. Scoped to a
// package outside them, or leaving one of them out, it is still refused.
func TestNarrowedTestsAcceptAWritePackageScopedCheck(t *testing.T) {
	t.Parallel()

	root := testRoot(t).Root()
	store := &Store{storeConfig: storeConfig{root: root}}
	refused := func(writePaths []string, args ...string) []string {
		var out []string
		for _, p := range unscopedNarrowedProblems(store, types.Job{ID: "w", WritePaths: writePaths, Check: goTestCheck(args...)}) {
			out = append(out, fmt.Sprintf("%s %s", p.Code, p.Field))
		}
		return out
	}

	assert.Empty(t, refused([]string{"pkg/a.go"}, "./pkg", "-run", "Alpha"), "the package plus -run")
	assert.Empty(t, refused([]string{"pkg/a.go"}, "./pkg/...", "-run", "Alpha"), "the package as a pattern")
	assert.Empty(t, refused([]string{"pkg/a.go"}, "./pkg"), "the package alone")
	assert.Equal(t, []string{"MGS3040 check"}, refused([]string{"pkg/a.go"}, "./other", "-run", "Alpha"), "a package outside the write paths")
	assert.Equal(t, []string{"MGS3040 check"}, refused([]string{"pkg/a.go", "nested/x/deep.go"}, "./pkg", "-run", "Alpha"),
		"a write path's package the check leaves out")
	assert.Equal(t, []string{"MGS3040 check"}, refused([]string{"pkg/a.go"}, "-run", "Alpha"), "no package at all")
}

func TestGoTestPackagesReadsRelativePatterns(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"./pkg", "./a/...", "."}, goTestPackages([]string{"-run", "X", "./pkg", "-count=1", "-v", "./a/...", "."}))
	assert.Empty(t, goTestPackages([]string{"-run", "./looks/like/one", "-tags", "x"}), "a flag's value is not a package")
}
