package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/types"
)

// holdMachineRefresh is a workspace root, and another checkout's refresh
// holding this machine's refresh lock for the test.
func holdMachineRefresh(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	dir, err := machineRefreshDir()
	require.NoError(t, err)
	lock, _, err := maintenance.AcquireGraphBuild(t.Context(), dir,
		maintenance.GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the symbol refresh of /other"}, io.Discard, time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Release() })
	return root
}

func TestStaleReadFindsTheMachinesRefreshAndStartsNothing(t *testing.T) {
	root := holdMachineRefresh(t)
	ans := types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"."}}

	cause := refreshInBackground(t.Context(), root, ans, "magus refs Foo --refresh")

	require.NotNil(t, cause)
	assert.Contains(t, cause.Why, "this read started no refresh")
	assert.Contains(t, cause.Why, fmt.Sprintf("the symbol refresh of /other (pid %d", os.Getpid()))
	assert.Contains(t, cause.Fix, "magus refs Foo --refresh")
}

func TestDetachedRefreshExitsWhenTheMachineLockIsHeld(t *testing.T) {
	root := holdMachineRefresh(t)
	t.Setenv(detachedRefreshEnv, "1")

	assert.True(t, refreshIndexesNow(t.Context(), root, "magus refs"), "it must not go on to answer a read")
	assert.Empty(t, os.Getenv(detachedRefreshEnv), "a run it starts is not the detached process")
}

func TestForegroundRefreshWaitsOnTheMachineLockThenStillAnswers(t *testing.T) {
	root := holdMachineRefresh(t)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	assert.False(t, refreshIndexesNow(ctx, root, "magus refs"), "a reader is waiting on the answer")
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "it waited for the lock until ctx ended")
}

// TestStaleIndexNotice: the banner names what is stale and the one command that fixes it,
// and says nothing at all when nothing is.
func TestStaleIndexNotice(t *testing.T) {
	answer := func(stale ...types.KnowledgeStaleIndex) types.KnowledgeAnswer {
		return knowledge.Answer("Foo", true, knowledge.Coverage{Seeded: true, Probed: true, Stale: stale})
	}
	got := staleIndexNotice(answer(types.KnowledgeStaleIndex{Project: "libs/api"}, types.KnowledgeStaleIndex{Project: "."}))
	assert.Contains(t, got, "stale index")
	assert.Contains(t, got, "libs/api", "the reader has to know WHICH answers are short")
	assert.Contains(t, got, "magus graph build", "and the command that fixes it")
	assert.Contains(t, got, "projects changed", "two projects read as plural")

	assert.Contains(t, staleIndexNotice(answer(types.KnowledgeStaleIndex{Project: "."})), "a project changed")
	assert.Empty(t, staleIndexNotice(answer()),
		"a current index draws silence; a banner on every lookup is one nobody reads")

	both := staleIndexNotice(answer(
		types.KnowledgeStaleIndex{Project: ".", Language: "buzz", Op: "scip-buzz"},
		types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip"}))
	assert.Contains(t, both, "a project changed", "two indexes of one project are one project")
	assert.Contains(t, both, "missing sites in . (buzz index), . (go index).", "and the line says which of its indexes is behind")
}

// The banner names the first changed files, counts the rest, and says what it cannot see;
// an index whose files nobody listed adds no line at all.
func TestStaleIndexNoticeNamesTheChangedFiles(t *testing.T) {
	answer := func(stale ...types.KnowledgeStaleIndex) types.KnowledgeAnswer {
		return knowledge.Answer("Foo", true, knowledge.Coverage{Seeded: true, Probed: true, Stale: stale})
	}
	got := staleIndexNotice(answer(
		types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip",
			Changed: []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "magusfile.buzz"}, ChangedCount: 9},
		types.KnowledgeStaleIndex{Project: ".", Language: "buzz", Op: "scip-buzz",
			Changed: []string{"magusfile.buzz"}, ChangedCount: 1}))
	assert.Contains(t, got, "  changed since: a.go, b.go, c.go, d.go, e.go and 4 more; deletions are not listed\n",
		"five named, the shared magusfile counted once, and the two files past the list's cap counted")

	assert.NotContains(t, staleIndexNotice(answer(types.KnowledgeStaleIndex{Project: "."})), "changed since:",
		"a recorded verdict names no files, and the line does not pretend it does")
}

func TestStaleIndexAdviceNamesTheChangedFiles(t *testing.T) {
	got := renderStaleGraphAdvice("", []types.KnowledgeStaleIndex{{Project: ".", Language: "go", Changed: []string{"run.go"}, ChangedCount: 1}})
	assert.Contains(t, got, "Changed since: run.go; deletions are not listed.")
	assert.NotContains(t, renderStaleGraphAdvice("", []types.KnowledgeStaleIndex{{Project: "."}}), "Changed since")
}

// An answer from a server that predates per-index detail still names its projects.
func TestStaleIndexNoticeFromProjectsAlone(t *testing.T) {
	got := staleIndexNotice(types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"libs/api"}})
	assert.Contains(t, got, "missing sites in libs/api.")
}

// A graph read mid-rebase is told why the graph is stale before anything else, and when
// rebuilding it is worth it.
func TestStaleGraphAdviceLeadsWithWhy(t *testing.T) {
	underway := renderStaleGraphAdvice("a rebase is in progress", nil)
	assert.True(t, strings.HasPrefix(underway,
		"magus workspace: the graph is stale, a rebase is in progress. Run `magus graph build` once any merge or rebase is finished, then ask again."), underway)

	api := []types.KnowledgeStaleIndex{{Project: "libs/api"}}
	both := renderStaleGraphAdvice("the index was built at 0123456789ab and the checkout is at fedcba9", api)
	assert.True(t, strings.HasPrefix(both, "magus workspace: the graph is stale, the index was built at 0123456789ab and the checkout is at fedcba9."), both)
	assert.Contains(t, both, "One project changed since it was last indexed: libs/api.")

	projects := renderStaleGraphAdvice("", api)
	assert.True(t, strings.HasPrefix(projects, "magus workspace: the answer you are about to get is drawn from an index built before the current sources."), projects)
	assert.NotContains(t, projects, "graph build` first", "nobody is sent away to rebuild")

	root := renderStaleGraphAdvice("", []types.KnowledgeStaleIndex{{Project: ".", Language: "buzz"}, {Project: ".", Language: "go"}})
	assert.Contains(t, root, "One project changed since it was last indexed: . (buzz index), . (go index).", root)

	assert.Empty(t, renderStaleGraphAdvice("", nil))
}

// The contradiction this closes: query printed "verdict: absent (magus searched everything
// it could reach)" and "stale index: ..." in one output. Both halves now read the same
// answer, so an index the verdict did not account for cannot be announced under it.
func TestStalenessBannerAgreesWithTheVerdict(t *testing.T) {
	cov := knowledge.Coverage{Seeded: true, Probed: true, Stale: []types.KnowledgeStaleIndex{{Project: "libs/api", Language: "go"}}}

	render := func(ans types.KnowledgeAnswer) string {
		var buf bytes.Buffer
		printVerdict(&buf, ans, "")
		_ = reportIndexStaleness(&buf, ans)
		return buf.String()
	}

	// The miss the index explains: the verdict carries it, so the banner stays out of the
	// way rather than repeating the projects and the refresh a second time.
	miss := render(knowledge.Answer("Foo", false, cov))
	assert.Contains(t, miss, "verdict: unknown, not absent")
	assert.Contains(t, miss, "libs/api")
	assert.NotContains(t, miss, "stale index")

	// A hit: the rows are facts and the index's age is a caveat on them, which is the case
	// the banner exists for.
	hit := render(knowledge.Answer("Foo", true, cov))
	assert.Contains(t, hit, "stale index")
	assert.Contains(t, hit, "libs/api")

	// A kind outside the lazy layer: the symbol index could not have held the answer, so
	// the absence is verified and naming a stale index would point at a layer never in scope.
	outOfScope := render(knowledge.Answer("kind=author nobody", false, cov))
	assert.Contains(t, outOfScope, "absent (magus searched everything")
	assert.NotContains(t, outOfScope, "stale index")
}
