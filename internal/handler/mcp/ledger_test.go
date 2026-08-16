package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/handler/status"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLedgerTool(t *testing.T) {
	t.Parallel()

	tool := &ledgerTool{store: ledger.NewStore(ledger.Location{CacheDir: t.TempDir(), Root: t.TempDir()})}
	invoke := func(t *testing.T, params map[string]any) spells.InvokeResponse {
		t.Helper()
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
		require.NoError(t, err)
		return resp
	}
	report := func(t *testing.T, resp spells.InvokeResponse) types.DelegationReport {
		t.Helper()
		got, ok := resp.Data.(types.DelegationReport)
		require.True(t, ok)
		return got
	}
	units := func(t *testing.T, resp spells.InvokeResponse) []types.DelegationUnit {
		t.Helper()
		return report(t, resp).Units
	}

	t.Run("an unwritten ledger lists empty", func(t *testing.T) {
		assert.Empty(t, units(t, invoke(t, nil)), "op defaults to list")
	})

	t.Run("put records the declared row", func(t *testing.T) {
		resp := invoke(t, map[string]any{
			"op": "put", "id": "unit-a", "goal": "ship the store; TestStoreRoundTrip passes",
			"checkpoint": "60dc9151", "owned_paths": "internal/ledger types/delegation.go",
			"forbidden_paths": "MAGUS.md", "tier": "standard", "validation": "magus run test",
			"state": "running",
		})
		got, ok := resp.Data.(types.DelegationUnit)
		require.True(t, ok, "Data is the record itself, so the console and the tool cannot disagree")
		assert.Equal(t, "unit-a", got.ID)
		assert.Equal(t, []string{"internal/ledger", "types/delegation.go"}, got.OwnedPaths)
		assert.Equal(t, []string{"MAGUS.md"}, got.ForbiddenPaths)
		assert.Equal(t, types.StateRunning, got.State)
		assert.NotZero(t, got.Created)
	})

	t.Run("put upserts by id", func(t *testing.T) {
		invoke(t, map[string]any{"op": "put", "id": "unit-b", "state": "declared", "depends_on": "unit-a"})
		invoke(t, map[string]any{"op": "put", "id": "unit-a", "state": "pass"})

		got := units(t, invoke(t, map[string]any{"op": "list"}))
		require.Len(t, got, 2, "the second put on unit-a replaced its row rather than adding one")
		assert.Equal(t, "unit-a", got[0].ID)
		assert.Equal(t, types.StatePass, got[0].State)
		assert.Equal(t, []string{"unit-a"}, got[1].DependsOn)
	})

	t.Run("a read-only unit carries no paths", func(t *testing.T) {
		resp := invoke(t, map[string]any{"op": "put", "id": "scout", "read_only": true, "state": "no_return"})
		got := resp.Data.(types.DelegationUnit)
		assert.True(t, got.ReadOnly)
		assert.Empty(t, got.OwnedPaths)
		assert.Equal(t, types.StateNoReturn, got.State, "no_return is its own terminal state, not fail")
	})

	t.Run("clear reports how many rows it dropped", func(t *testing.T) {
		resp := invoke(t, map[string]any{"op": "clear"})
		data := resp.Data.(map[string]any)
		assert.Equal(t, 3, data["cleared"], "a destructive op says what it destroyed")
		assert.Empty(t, units(t, invoke(t, map[string]any{"op": "list"})))
	})

	t.Run("a lifecycle put touches only the fields it names", func(t *testing.T) {
		invoke(t, map[string]any{
			"op": "put", "id": "unit-life", "goal": "the declared goal",
			"checkpoint": "abc123", "owned_paths": "internal/ledger", "tier": "opus",
		})
		resp := invoke(t, map[string]any{"op": "put", "id": "unit-life", "state": "pass"})
		got, ok := resp.Data.(types.DelegationUnit)
		require.True(t, ok)
		assert.Equal(t, types.StatePass, got.State)
		assert.Equal(t, "the declared goal", got.Goal, "state advance must not erase the row")
		assert.Equal(t, "abc123", got.Checkpoint)
		assert.Equal(t, []string{"internal/ledger"}, got.OwnedPaths)
		assert.Equal(t, "opus", got.Tier)
	})

	t.Run("a json array of paths records paths, not nothing", func(t *testing.T) {
		resp := invoke(t, map[string]any{
			"op": "put", "id": "unit-arr", "owned_paths": []any{"a/b", "c d"},
		})
		got, ok := resp.Data.(types.DelegationUnit)
		require.True(t, ok)
		assert.Equal(t, []string{"a/b", "c d"}, got.OwnedPaths, "array elements are paths verbatim; only the string form splits on spaces")
	})

	t.Run("an unrecognized state is rejected, not stored", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{
			"op": "put", "id": "unit-bad", "state": "passed",
		}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no_return")
	})

	t.Run("put with no id is rejected", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"op": "put", "goal": "nameless"}})
		require.ErrorIs(t, err, ledger.ErrNoID)
	})

	t.Run("an unknown op is rejected, not silently listed", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"op": "delete"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list, put, clear")
	})

	// Every reader answers a wrong-typed field the same way. Dropping one while state and
	// read_only error would tell a client sending goal=3 that its put succeeded and hand
	// back a row without the field it thought it wrote.
	for name, params := range map[string]map[string]any{
		"a non-string goal":            {"op": "put", "id": "unit-typed", "goal": 3},
		"a non-string tier":            {"op": "put", "id": "unit-typed", "tier": true},
		"a non-list owned_paths":       {"op": "put", "id": "unit-typed", "owned_paths": 7},
		"a list with a non-string":     {"op": "put", "id": "unit-typed", "depends_on": []any{"a", 2}},
		"a non-string state":           {"op": "put", "id": "unit-typed", "state": 1},
		"a non-boolean read_only":      {"op": "put", "id": "unit-typed", "read_only": "yes"},
		"a non-string forbidden_paths": {"op": "put", "id": "unit-typed", "forbidden_paths": map[string]any{}},
	} {
		t.Run(name+" is rejected, not dropped", func(t *testing.T) {
			_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
			require.Error(t, err)

			got := units(t, invoke(t, map[string]any{"op": "list"}))
			for _, u := range got {
				assert.NotEqual(t, "unit-typed", u.ID, "a rejected put writes no row")
			}
		})
	}

	t.Run("every mistyped param is reported at once", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{
			"op": "put", "id": "unit-typed", "goal": 3, "read_only": "yes",
		}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "goal")
		assert.Contains(t, err.Error(), "read_only", "a client that mistyped two params learns both in one round trip")
	})
}

// TestLedgerToolListAnswersOverlapsAndReleases covers what a list is FOR beyond the
// rows: two units claiming one tree, and the version of a path a finished editor left
// behind. Both are answers an orchestrator would otherwise have to derive by hand from
// a table it wrote itself.
func TestLedgerToolListAnswersOverlapsAndReleases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "shared.go"), []byte("package shared\n"), 0o644))

	tool := &ledgerTool{store: ledger.NewStore(ledger.Location{CacheDir: t.TempDir(), Root: root})}
	invoke := func(params map[string]any) spells.InvokeResponse {
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
		require.NoError(t, err)
		return resp
	}
	invoke(map[string]any{"op": "put", "id": "u1", "owned_paths": "shared.go docs", "state": "running"})
	invoke(map[string]any{"op": "put", "id": "u2", "owned_paths": "shared.go", "state": "declared"})

	got, ok := invoke(map[string]any{"op": "list"}).Data.(types.DelegationReport)
	require.True(t, ok)
	require.Len(t, got.Overlaps, 1)
	assert.Equal(t, "u1", got.Overlaps[0].UnitA)
	assert.Equal(t, "u2", got.Overlaps[0].UnitB)
	assert.Equal(t, []string{"shared.go"}, got.Overlaps[0].PathsA)
	assert.Equal(t, []string{"shared.go"}, got.Overlaps[0].PathsB)

	// u1 finishes editing the contested file and announces it by shrinking the row. The
	// digest is what tells u2 which version it is starting from.
	invoke(map[string]any{"op": "put", "id": "u1", "owned_paths": "docs"})
	got, ok = invoke(map[string]any{"op": "list"}).Data.(types.DelegationReport)
	require.True(t, ok)
	assert.Empty(t, got.Overlaps, "the released path is no longer claimed twice")
	require.Len(t, got.Units[0].Releases, 1)
	assert.Equal(t, "shared.go", got.Units[0].Releases[0].Path)
	assert.Contains(t, got.Units[0].Releases[0].Digest, "sha256:")
	assert.NotZero(t, got.Units[0].Updated, "every put re-stamps the row a reader watches for staleness")
}

// TestLedgerToolPutMergesConcurrently is why a put goes through Store.Update. Two
// writers advance different fields of one unit - an orchestrator moving the state, a
// worker recording its checkpoint - and both have to survive. Reading the row with List
// and writing it back with Put releases the store's lock in between, so each writer
// merges onto a row it read before the other wrote, and the second write reverts the
// first one's field.
func TestLedgerToolPutMergesConcurrently(t *testing.T) {
	t.Parallel()

	tool := &ledgerTool{store: ledger.NewStore(ledger.Location{CacheDir: t.TempDir(), Root: t.TempDir()})}
	put := func(params map[string]any) error {
		_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
		return err
	}
	require.NoError(t, put(map[string]any{
		"op": "put", "id": "u1", "goal": "the declared goal", "state": "declared",
	}))

	const rounds = 25
	var wg sync.WaitGroup
	errs := make(chan error, 2*rounds)
	for _, params := range []map[string]any{
		{"op": "put", "id": "u1", "state": "running"},
		{"op": "put", "id": "u1", "checkpoint": "deadbeef"},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				errs <- put(params)
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
	assert.Equal(t, "the declared goal", got[0].Goal, "neither put erased the field it did not name")
}

// TestLedgerDoorsAgreeOnAnEmptyLedger is the parity the constructor exists to guarantee.
// The ledger has two read doors - this tool and the console's GET /api/v1/ledger - and an
// unwritten ledger is the case they used to answer differently, one serving "units":[]
// because the route normalized it by hand and the other serving null.
func TestLedgerDoorsAgreeOnAnEmptyLedger(t *testing.T) {
	t.Parallel()

	store := ledger.NewStore(ledger.Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	tool := &ledgerTool{store: store}
	resp, err := tool.Invoke(t.Context(), spells.InvokeRequest{Params: map[string]any{"op": "list"}})
	require.NoError(t, err)
	fromTool, err := json.Marshal(resp.Data)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	status.NewLedgerHandler(store, nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/ledger", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var route, toolBody any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &route))
	require.NoError(t, json.Unmarshal(fromTool, &toolBody))
	assert.Equal(t, route, toolBody)
	assert.Contains(t, string(fromTool), `"units":[]`)
}
