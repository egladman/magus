package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLedgerTool(t *testing.T) {
	t.Parallel()

	tool := &ledgerTool{store: ledger.NewStore(t.TempDir())}
	invoke := func(t *testing.T, params map[string]any) spells.InvokeResponse {
		t.Helper()
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: params})
		require.NoError(t, err)
		return resp
	}
	units := func(t *testing.T, resp spells.InvokeResponse) []types.DelegationUnit {
		t.Helper()
		data, ok := resp.Data.(map[string]any)
		require.True(t, ok)
		got, ok := data["units"].([]types.DelegationUnit)
		require.True(t, ok)
		return got
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
}
