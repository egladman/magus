package job

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// A changed file belongs to exactly one job, and nothing the worker does decides it: the
// write paths are disjoint by construction, so the path alone names the holder.
func TestAttributeWriteNamesTheOneWritePathThatCoversIt(t *testing.T) {
	rows := []types.Job{
		{ID: "pwa/job-watch", State: types.StateRunning, WritePaths: []string{"internal/trail", "cmd/magus/job.go"}},
		{ID: "pwa/jobs-ui", State: types.StateRunning, WritePaths: []string{"console/src/console/plan"}},
	}

	got, ok := AttributeWrite(rows, "internal/trail/trail.go")
	assert.True(t, ok)
	assert.Equal(t, "pwa/job-watch", got.Job)
	assert.Equal(t, "internal/trail", got.WritePath, "the reader wants the DECLARATION that covered it, not just the job")
	assert.Empty(t, got.Ambiguous)

	got, ok = AttributeWrite(rows, "console/src/console/plan/jobs.ts")
	assert.True(t, ok)
	assert.Equal(t, "pwa/jobs-ui", got.Job)
}

// A path nobody leased is nobody's. It is reported as unattributed rather than dropped: a
// write outside every declared boundary is the interesting one, and silently binning it is how the
// console came to show a quiet tree while somebody edited it.
func TestAttributeWriteLeavesAnUnleasedPathUnattributed(t *testing.T) {
	rows := []types.Job{{ID: "pwa/job-watch", State: types.StateRunning, WritePaths: []string{"internal/trail"}}}

	got, ok := AttributeWrite(rows, "README.md")
	assert.False(t, ok)
	assert.Empty(t, got.Job)
	assert.Empty(t, got.Ambiguous)
}

// A job that has ENDED holds nothing, so its write paths stop answering for a path. Otherwise a
// finished job keeps collecting the next holder's writes for as long as its row is kept.
func TestAttributeWriteIgnoresAJobThatIsNoLongerLive(t *testing.T) {
	rows := []types.Job{
		{ID: "old", State: types.StatePass, WritePaths: []string{"internal/trail"}},
		{ID: "new", State: types.StateDeclared, WritePaths: []string{"internal/trail"}},
	}

	got, ok := AttributeWrite(rows, "internal/trail/trail.go")
	assert.True(t, ok)
	assert.Equal(t, "new", got.Job, "only a live job answers")
}

// A path a job's write paths cover but its own deny paths exclude is NOT that job's. The row
// says so itself, and reading the write paths alone would attribute a shared build input to
// whichever job happened to name the directory above it.
func TestAttributeWriteHonoursTheJobsOwnDenyPaths(t *testing.T) {
	rows := []types.Job{{
		ID: "pwa/job-watch", State: types.StateRunning,
		WritePaths: []string{"internal"},
		DenyPaths:  []string{"internal/agent"},
	}}

	_, ok := AttributeWrite(rows, "internal/agent/catalog.go")
	assert.False(t, ok, "a denied path is outside the boundary, whatever the write paths say")

	got, ok := AttributeWrite(rows, "internal/trail/trail.go")
	assert.True(t, ok)
	assert.Equal(t, "pwa/job-watch", got.Job)
}

// TWO LIVE JOBS OVER ONE PATH is the case the whole feed rests on not happening: fork
// refuses a shared checkout and records write_proof, and `magus ls jobs` prints the
// overlaps. When it happens anyway the answer is NEITHER, with both names, because a feed
// that picks one would tell a person a file moved under a worker that never touched it,
// and there is nothing in the path to break the tie with.
func TestAttributeWriteRefusesToPickBetweenTwoLiveJobs(t *testing.T) {
	rows := []types.Job{
		{ID: "a", State: types.StateRunning, WritePaths: []string{"internal/trail"}},
		{ID: "b", State: types.StateDeclared, WritePaths: []string{"internal"}},
	}

	got, ok := AttributeWrite(rows, "internal/trail/trail.go")
	assert.False(t, ok, "an ambiguous path is attributed to nobody")
	assert.Empty(t, got.Job)
	assert.Equal(t, []string{"a", "b"}, got.Ambiguous, "both claimants are named, so a reader can go and fix the plan")
}

// A contested path reaches the reader watching either claimant. It is attributed to nobody,
// which is the honest answer to "whose write is this"; it is still the business of both, and
// a filter on job alone dropped it silently.
//
// Live in this repository while it was being written: pwa/job-watch and pwa/turns-capture
// both declared internal/trail, and `magus job watch pwa/job-watch` showed nothing at all
// while that directory was edited.
func TestFeedFilterMatchesAContestedPathForEveryClaimant(t *testing.T) {
	events := FileEvents([]types.Job{
		{ID: "pwa/job-watch", State: types.StateRunning, WritePaths: []string{"internal/trail"}},
		{ID: "pwa/turns-capture", State: types.StateDeclared, WritePaths: []string{"internal/trail"}},
	}, 5, []string{"internal/trail/trail.go"})

	require.Len(t, events, 1)
	got := events[0]
	assert.Empty(t, got.Job, "nothing can say which of them wrote it")
	assert.Equal(t, []string{"pwa/job-watch", "pwa/turns-capture"}, got.Contested)
	assert.Equal(t, "contested: pwa/job-watch and pwa/turns-capture both declare this path", got.Note)

	assert.True(t, FeedFilter{Jobs: []string{"pwa/job-watch"}}.Match(got))
	assert.True(t, FeedFilter{Jobs: []string{"pwa/turns-capture"}}.Match(got))
	assert.False(t, FeedFilter{Jobs: []string{"pwa/elsewhere"}}.Match(got),
		"a job that never declared the path is not told about it")
}
