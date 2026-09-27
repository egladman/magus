package mcp

import (
	"context"
	"os"
	"testing"

	"github.com/egladman/magus/internal/memory"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMemoryRefs(t *testing.T) {
	// One ref per line, split on the FIRST colon so a target with its own colons or
	// commas (a query expression, a namespaced node ID) survives intact.
	refs, err := memory.ParseRefs("query: kind:op depends cache\nnode: file:internal/hash/hasher.go\n\noutput: out1a2b3c")
	require.NoError(t, err)
	assert.Equal(t, []memory.Ref{
		{Kind: "query", Target: "kind:op depends cache"},
		{Kind: "node", Target: "file:internal/hash/hasher.go"},
		{Kind: "output", Target: "out1a2b3c"},
	}, refs)

	empty, err := memory.ParseRefs("   \n\n")
	require.NoError(t, err)
	assert.Empty(t, empty, "blank lines yield no refs")

	_, err = memory.ParseRefs("this line has no colon")
	assert.Error(t, err, "a ref without a kind: prefix is rejected")
}

// TestMemoryPutMatchesTheCLIContract pins MCP parity with `magus memory put`. An agent
// sends a few params and sees nothing of what the name already holds, so the fields it
// omits have to survive.
func TestMemoryPutMatchesTheCLIContract(t *testing.T) {
	testkit.Isolate(t)
	tool := &memoryTool{opts: Options{Magus: fixtureMagus(t)}}
	ctx := context.Background()

	put := func(params map[string]any) (memoryRecordView, error) {
		params["op"] = "put"
		resp, err := tool.Invoke(ctx, spells.InvokeRequest{Params: params})
		if err != nil {
			return memoryRecordView{}, err
		}
		view, ok := resp.Data.(memoryRecordView)
		require.True(t, ok, "put returns the stored record")
		return view, nil
	}

	created, err := put(map[string]any{
		"name": "cache-key-drift", "type": "elimination", "status": "open",
		"refs": "output: out1a2b3c", "body": "Not the cache key.", "excerpt": "0 differing lines",
	})
	require.NoError(t, err)
	assert.Equal(t, "elimination", created.Type)

	amended, err := put(map[string]any{"name": "cache-key-drift", "status": "done"})
	require.NoError(t, err)
	assert.Equal(t, "done", amended.Status)
	assert.Equal(t, "Not the cache key.", amended.Body, "an omitted param keeps what is stored")
	assert.Equal(t, "0 differing lines", amended.Excerpt)
	require.Len(t, amended.Refs, 1)
	assert.Equal(t, created.Created, amended.Created)

	_, err = put(map[string]any{"name": "ghost", "status": "done", "allow_missing": false})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no entry named")

	_, err = put(map[string]any{"name": "cache-key-drift", "type": "pointer"})
	require.Error(t, err)
	assert.ErrorIs(t, err, memory.ErrInvalid, "a record cannot be updated into another type")
}

// TestMemoryDeleteArchivesAndRefusesAnAbsentName pins the agent door: a delete hands back
// the record it archived, a put of that record restores it, and a typo is an error rather
// than a report of a delete that never happened.
func TestMemoryDeleteArchivesAndRefusesAnAbsentName(t *testing.T) {
	testkit.Isolate(t)
	tool := &memoryTool{opts: Options{Magus: fixtureMagus(t)}}
	ctx := context.Background()
	invoke := func(params map[string]any) (any, error) {
		resp, err := tool.Invoke(ctx, spells.InvokeRequest{Params: params})
		return resp.Data, err
	}

	_, err := invoke(map[string]any{"op": "put", "name": "next-release", "type": "plan", "refs": "command: magus affected ci", "body": "Gate after docs."})
	require.NoError(t, err)

	_, err = invoke(map[string]any{"op": "delete", "name": "next-releas"})
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Contains(t, err.Error(), `did you mean "next-release"?`)

	data, err := invoke(map[string]any{"op": "delete", "name": "next-release"})
	require.NoError(t, err)
	out, ok := data.(map[string]any)
	require.True(t, ok)
	view, ok := out["record"].(memoryRecordView)
	require.True(t, ok)
	assert.Equal(t, "next-release", out["deleted"])
	assert.FileExists(t, out["archived"].(string))
	assert.Equal(t, "Gate after docs.", view.Body)

	_, err = invoke(map[string]any{"op": "get", "name": "next-release"})
	require.Error(t, err)
	_, err = invoke(map[string]any{"op": "put", "name": view.Name, "type": view.Type, "refs": "command: magus affected ci", "body": view.Body})
	require.NoError(t, err)
	_, err = invoke(map[string]any{"op": "get", "name": "next-release"})
	require.NoError(t, err, "a put of the archived record restores it")
}

func TestSplitCommaList(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, splitCommaList(" a, b ,c"))
	assert.Empty(t, splitCommaList("  ,  , "))
	assert.Empty(t, splitCommaList(""))
}
