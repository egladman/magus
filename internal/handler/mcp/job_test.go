package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	jobshandler "github.com/egladman/magus/internal/handler/jobs"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmpJobStore keeps a test's rows under temp directories: a Location without StateBase
// would write the developer's own per-repository store.
//
// The actor is pinned UNBOUND, or a developer running the suite from a checkout bound to
// a job grades every fork and clear here as that worker and is refused.
func tmpJobStore(t *testing.T, root string) *job.Store {
	t.Helper()
	return job.NewStore(job.Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: root, Actor: &job.Actor{}})
}

func TestJobTool(t *testing.T) {
	t.Parallel()

	tool := &jobTool{store: tmpJobStore(t, t.TempDir())}
	invoke := func(t *testing.T, params map[string]any) spells.InvokeResponse {
		t.Helper()
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
		require.NoError(t, err)
		return resp
	}
	report := func(t *testing.T, resp spells.InvokeResponse) types.JobList {
		t.Helper()
		got, ok := resp.Data.(types.JobList)
		require.True(t, ok)
		return got
	}
	jobs := func(t *testing.T, resp spells.InvokeResponse) []types.Job {
		t.Helper()
		return report(t, resp).Jobs
	}

	t.Run("an unwritten store lists empty", func(t *testing.T) {
		assert.Empty(t, jobs(t, invoke(t, nil)), "op defaults to list")
	})

	t.Run("fork records the declared row", func(t *testing.T) {
		resp := invoke(t, map[string]any{
			"op": "fork", "id": "job-a", "goal": "ship the store; TestStoreRoundTrip passes",
			"checkpoint": "60dc9151", "write_paths": "internal/job types/job.go",
			"deny_paths": "MAGUS.md", "model": "standard", "validation": "magus run test",
			"state": "running",
		})
		got, ok := resp.Data.(types.Job)
		require.True(t, ok, "Data is the record itself, so the console and the tool cannot disagree")
		assert.Equal(t, "job-a", got.ID)
		assert.Equal(t, []string{"internal/job", "types/job.go"}, got.WritePaths)
		assert.Equal(t, []string{"MAGUS.md"}, got.DenyPaths)
		assert.Equal(t, types.StateRunning, got.State)
		assert.NotZero(t, got.Created)
	})

	t.Run("fork upserts by id", func(t *testing.T) {
		invoke(t, map[string]any{"op": "fork", "id": "job-b", "state": "declared", "depends_on": "job-a"})
		invoke(t, map[string]any{"op": "fork", "id": "job-a", "state": "pass"})

		got := jobs(t, invoke(t, map[string]any{"op": "list"}))
		require.Len(t, got, 2, "the second fork on job-a replaced its row rather than adding one")
		assert.Equal(t, "job-a", got[0].ID)
		assert.Equal(t, types.StatePass, got[0].State)
		assert.Equal(t, []string{"job-a"}, got[1].DependsOn)
	})

	t.Run("a read-only job carries no paths", func(t *testing.T) {
		resp := invoke(t, map[string]any{"op": "fork", "id": "scout", "read_only": true, "state": "no_return"})
		got := resp.Data.(types.Job)
		assert.True(t, got.ReadOnly)
		assert.Empty(t, got.WritePaths)
		assert.Equal(t, types.StateNoReturn, got.State, "no_return is its own terminal state, not fail")
	})

	t.Run("clear reports how many rows it dropped", func(t *testing.T) {
		resp := invoke(t, map[string]any{"op": "clear"})
		data := resp.Data.(map[string]any)
		assert.Equal(t, 3, data["cleared"], "a destructive op says what it destroyed")
		assert.Empty(t, jobs(t, invoke(t, map[string]any{"op": "list"})))
	})

	t.Run("a lifecycle fork touches only the fields it names", func(t *testing.T) {
		invoke(t, map[string]any{
			"op": "fork", "id": "job-life", "goal": "the declared goal",
			"checkpoint": "abc123", "write_paths": "internal/job", "model": "opus",
		})
		resp := invoke(t, map[string]any{"op": "fork", "id": "job-life", "state": "pass"})
		got, ok := resp.Data.(types.Job)
		require.True(t, ok)
		assert.Equal(t, types.StatePass, got.State)
		assert.Equal(t, "the declared goal", got.Goal, "state advance must not erase the row")
		assert.Equal(t, "abc123", got.Checkpoint)
		assert.Equal(t, []string{"internal/job"}, got.WritePaths)
		assert.Equal(t, "opus", got.Model)
	})

	t.Run("a json array of paths records paths, not nothing", func(t *testing.T) {
		resp := invoke(t, map[string]any{
			"op": "fork", "id": "job-arr", "write_paths": []any{"a/b", "c d"},
		})
		got, ok := resp.Data.(types.Job)
		require.True(t, ok)
		assert.Equal(t, []string{"a/b", "c d"}, got.WritePaths, "array elements are paths verbatim; only the string form splits on spaces")
	})

	t.Run("an unrecognized state is rejected, not stored", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{
			"op": "fork", "id": "job-bad", "state": "passed",
		}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no_return")
	})

	t.Run("fork with no id is rejected", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"op": "fork", "goal": "nameless"}})
		require.ErrorIs(t, err, job.ErrNoID)
	})

	t.Run("an unknown op is rejected, not silently listed", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"op": "delete"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list, fork, exec, clear")
	})

	// exec is the worker's door: it reports the base it actually landed on and is
	// told how that compares with the checkpoint the job was handed. The comparison
	// itself is covered in internal/job; what this asserts is that the tool answers
	// with BOTH halves: the row for a reader, and Text for the worker, which is the only
	// op here that sets it.
	t.Run("exec records the reported base and returns the verdict", func(t *testing.T) {
		invoke(t, map[string]any{"op": "fork", "id": "job-reg", "checkpoint": "aaaa1111", "state": "declared"})

		resp := invoke(t, map[string]any{"op": "exec", "id": "job-reg", "reported_base": "bbbb2222"})
		got, ok := resp.Data.(types.Job)
		require.True(t, ok)
		assert.Equal(t, types.BaseDiverged, got.BaseVerdict)
		assert.Equal(t, "bbbb2222", got.ReportedBase)
		assert.NotZero(t, got.Registered)
		assert.Contains(t, resp.Text, "aaaa1111", "the reading names the checkpoint the job was handed")
		assert.Contains(t, resp.Text, "bbbb2222", "and the base the worker reported")
		assert.Contains(t, resp.Text, "Respawn from", "and what to do about it")

		var found bool
		for _, u := range jobs(t, invoke(t, map[string]any{"op": "list"})) {
			if u.ID == "job-reg" {
				found = true
				assert.Equal(t, types.BaseDiverged, u.BaseVerdict, "the verdict is stored, not only returned")
			}
		}
		assert.True(t, found, "nothing was refused: the diverged row is in the store like any other")
	})

	t.Run("exec on an unknown id names where the declared ids are", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{
			"op": "exec", "id": "never-declared", "reported_base": "aaaa1111",
		}})
		require.ErrorIs(t, err, job.ErrUnknownJob)
		assert.Contains(t, err.Error(), "magus_job list")
	})

	// Every reader answers a wrong-typed field the same way. Dropping one while state and
	// read_only error would tell a client sending goal=3 that its fork succeeded and hand
	// back a row without the field it thought it wrote.
	for name, params := range map[string]map[string]any{
		"a non-string goal":        {"op": "fork", "id": "job-typed", "goal": 3},
		"a non-string model":       {"op": "fork", "id": "job-typed", "model": true},
		"a non-list write_paths":   {"op": "fork", "id": "job-typed", "write_paths": 7},
		"a list with a non-string": {"op": "fork", "id": "job-typed", "depends_on": []any{"a", 2}},
		"a non-string state":       {"op": "fork", "id": "job-typed", "state": 1},
		"a non-boolean read_only":  {"op": "fork", "id": "job-typed", "read_only": "yes"},
		"a non-string deny_paths":  {"op": "fork", "id": "job-typed", "deny_paths": map[string]any{}},
	} {
		t.Run(name+" is rejected, not dropped", func(t *testing.T) {
			_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
			require.Error(t, err)

			got := jobs(t, invoke(t, map[string]any{"op": "list"}))
			for _, u := range got {
				assert.NotEqual(t, "job-typed", u.ID, "a rejected fork writes no row")
			}
		})
	}

	t.Run("every mistyped param is reported at once", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{
			"op": "fork", "id": "job-typed", "goal": 3, "read_only": "yes",
		}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "goal")
		assert.Contains(t, err.Error(), "read_only", "a client that mistyped two params learns both in one round trip")
	})
}

// TestJobToolListAnswersOverlapsAndReleases covers what a list is FOR beyond the
// rows: two jobs claiming one tree, and the version of a path a finished editor left
// behind. Both are answers an orchestrator would otherwise have to derive by hand from
// a table it wrote itself.
func TestJobToolListAnswersOverlapsAndReleases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "shared.go"), []byte("package shared\n"), 0o644))

	tool := &jobTool{store: tmpJobStore(t, root)}
	invoke := func(params map[string]any) spells.InvokeResponse {
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
		require.NoError(t, err)
		return resp
	}
	invoke(map[string]any{"op": "fork", "id": "u1", "write_paths": "shared.go docs", "state": "running"})
	invoke(map[string]any{"op": "fork", "id": "u2", "write_paths": "shared.go", "state": "declared"})

	got, ok := invoke(map[string]any{"op": "list"}).Data.(types.JobList)
	require.True(t, ok)
	require.Len(t, got.Overlaps, 1)
	assert.Equal(t, "u1", got.Overlaps[0].JobA)
	assert.Equal(t, "u2", got.Overlaps[0].JobB)
	assert.Equal(t, []string{"shared.go"}, got.Overlaps[0].PathsA)
	assert.Equal(t, []string{"shared.go"}, got.Overlaps[0].PathsB)

	// u1 finishes editing the contested file and announces it by shrinking the row. The
	// digest is what tells u2 which version it is starting from.
	invoke(map[string]any{"op": "fork", "id": "u1", "write_paths": "docs"})
	got, ok = invoke(map[string]any{"op": "list"}).Data.(types.JobList)
	require.True(t, ok)
	assert.Empty(t, got.Overlaps, "the released path is no longer claimed twice")
	require.Len(t, got.Jobs[0].Releases, 1)
	assert.Equal(t, "shared.go", got.Jobs[0].Releases[0].Path)
	assert.Contains(t, got.Jobs[0].Releases[0].Digest, "sha256:")
	assert.NotZero(t, got.Jobs[0].Updated, "every fork re-stamps the row a reader watches for staleness")
}

// TestJobToolForkMergesConcurrently is why a fork goes through Store.Update. Two
// writers advance different fields of one job (an orchestrator moving the state, a
// worker recording its checkpoint) and both have to survive. Reading the row with List
// and writing it back with Fork releases the store's lock in between, so each writer
// merges onto a row it read before the other wrote, and the second write reverts the
// first one's field.
func TestJobToolForkMergesConcurrently(t *testing.T) {
	t.Parallel()

	tool := &jobTool{store: tmpJobStore(t, t.TempDir())}
	fork := func(params map[string]any) error {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
		return err
	}
	require.NoError(t, fork(map[string]any{
		"op": "fork", "id": "u1", "goal": "the declared goal", "state": "declared",
	}))

	const rounds = 25
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for _, params := range []map[string]any{
		{"op": "fork", "id": "u1", "state": "running"},
		{"op": "fork", "id": "u1", "checkpoint": "deadbeef"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				errs <- fork(params)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	got, err := tool.store.List()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, types.StateRunning, got[0].State, "the state advance survived the concurrent checkpoint write")
	assert.Equal(t, "deadbeef", got[0].Checkpoint, "and the checkpoint survived the concurrent state advance")
	assert.Equal(t, "the declared goal", got[0].Goal, "neither fork erased the field it did not name")
}

// TestJobDoorsAgreeOnAnEmptyStore is the parity the constructor exists to guarantee.
// The job store has two read doors (this tool and the console's GET /api/v1/jobs), and an
// unwritten store is the case they used to answer differently, one serving "jobs":[]
// because the route normalized it by hand and the other serving null.
func TestJobDoorsAgreeOnAnEmptyStore(t *testing.T) {
	t.Parallel()

	store := tmpJobStore(t, t.TempDir())
	tool := &jobTool{store: store}
	resp, err := tool.Invoke(t.Context(), spells.InvokeRequest{Params: map[string]any{"op": "list"}})
	require.NoError(t, err)
	fromTool, err := json.Marshal(resp.Data)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	jobshandler.NewHandler(store, nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var route, toolBody any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &route))
	require.NoError(t, json.Unmarshal(fromTool, &toolBody))
	assert.Equal(t, route, toolBody)
	assert.Contains(t, string(fromTool), `"jobs":[]`)
}
