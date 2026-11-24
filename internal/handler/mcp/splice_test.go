package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/libs/codec"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpliceToolAppliesExistingBlockDocument(t *testing.T) {
	m := fixtureMagus(t)
	path := filepath.Join(m.Root(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))
	tool := &spliceTool{workspace: m}
	assert.Equal(t, hint.ToolSplice.String(), tool.Name())
	doc := "## file.txt\n```old\nbefore\n```\n```new\nafter\n```\n"

	preview, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"document": doc}})
	require.NoError(t, err)
	assert.Equal(t, "before\n", readSpliceFile(t, path), "a plan does not write")
	files := preview.Data.(spliceResponse).Plan.Files
	require.Len(t, files, 1)
	assert.Equal(t, "file.txt", files[0].Path)

	result, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"document": doc, "apply": true}})
	require.NoError(t, err)
	assert.Equal(t, "after\n", readSpliceFile(t, path))
	require.NotNil(t, result.Data.(spliceResponse).Applied)
	assert.True(t, result.Data.(spliceResponse).Applied.IsOk())
}

func TestSplicePublicProtocolPlansAndApplies(t *testing.T) {
	m := fixtureMagus(t)
	path := filepath.Join(m.Root(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))
	tool := &spliceTool{workspace: m}
	handler := authorize(declaredParams(descriptor(t, hint.ToolSplice), adapt(tool)))
	ctx := recorder.ContextWithCredential(bgContext(), auth.CredentialStdio())
	doc := "## file.txt\n```old\nbefore\n```\n```new\nafter\n```\n"

	preview, err := handler(ctx, callRequest(tool.Name(), map[string]any{"document": doc}))
	require.NoError(t, err)
	require.False(t, preview.IsError, preview.Content)
	assert.NotEmpty(t, preview.StructuredContent)
	assert.Equal(t, "before\n", readSpliceFile(t, path))

	judged := std.WithEditJudge(ctx, std.EditJudge{
		Covers: func(context.Context, string) bool { return true },
		Grade: func(context.Context, string, []byte, []byte) error {
			return fmt.Errorf("caller refused file.txt")
		},
	})
	refused, err := handler(judged, callRequest(tool.Name(), map[string]any{"document": doc, "apply": true}))
	require.NoError(t, err)
	require.False(t, refused.IsError, refused.Content)
	data, err := codec.Marshal(refused.StructuredContent)
	require.NoError(t, err)
	var response spliceResponse
	require.NoError(t, codec.Unmarshal(data, &response))
	require.Len(t, response.Plan.Refused, 1)
	assert.Contains(t, response.Plan.Refused[0].Reason, "caller refused file.txt")
	assert.Equal(t, "before\n", readSpliceFile(t, path))

	result, err := handler(ctx, callRequest(tool.Name(), map[string]any{"document": doc, "apply": true}))
	require.NoError(t, err)
	require.False(t, result.IsError, result.Content)
	assert.Equal(t, "after\n", readSpliceFile(t, path))
}

func TestSplicePublicProtocolPreservesCallerPolicy(t *testing.T) {
	for _, mode := range []string{"off", "best-effort"} {
		t.Run(mode, func(t *testing.T) { testSpliceCallerPolicy(t, mode) })
	}
}

func testSpliceCallerPolicy(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), fmt.Appendf(nil, "sandbox:\n  mode: %s\n", mode), 0o644))
	m, err := magus.Open(t.Context(), root, magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	path := filepath.Join(m.Root(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: m.CacheDir(), Root: m.Root()})
	const lease = "fleet/client"
	_, err = store.Update(t.Context(), lease, func(row *jobtypes.Job) {
		*row = jobtypes.Job{ID: lease, Parent: "fleet/root", State: jobtypes.StateRunning, Paths: jobtypes.Paths{Write: []string{"file.txt"}}}
	})
	require.NoError(t, err)
	tool := &spliceTool{workspace: m, jobs: store}
	handler := authorize(declaredParams(descriptor(t, hint.ToolSplice), adapt(tool)))
	doc := "## file.txt\n```old\nbefore\n```\n```new\nafter\n```\n"
	for _, read := range []bool{false, true} {
		policy := &sandbox.Policy{Workspace: m.Root(), FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: path, Read: read}}}}
		ctx := withCallerLease(sandbox.WithPolicy(recorder.ContextWithCredential(t.Context(), auth.CredentialSocketPeer()), policy), lease)
		for _, apply := range []bool{false, true} {
			reply, err := handler(ctx, callRequest(tool.Name(), map[string]any{"document": doc, "apply": apply}))
			require.NoError(t, err)
			require.False(t, reply.IsError, reply.Content)
			data, err := codec.Marshal(reply.StructuredContent)
			require.NoError(t, err)
			var response spliceResponse
			require.NoError(t, codec.Unmarshal(data, &response))
			require.NotEmpty(t, response.Plan.Refused)
			access := "read"
			if read {
				access = "write"
			}
			assert.Contains(t, response.Plan.Refused[0].Reason, fmt.Sprintf("fs %s denied", access))
			assert.Empty(t, response.Plan.Files)
			if apply {
				require.NotNil(t, response.Applied)
				assert.False(t, response.Applied.IsOk())
			}
			assert.Equal(t, "before\n", readSpliceFile(t, path))
		}
	}
}

func TestSpliceToolHonorsCanceledContext(t *testing.T) {
	m := fixtureMagus(t)
	tool := &spliceTool{workspace: m}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := tool.Invoke(ctx, spells.InvokeRequest{Params: map[string]any{"document": "", "apply": true}})
	require.ErrorIs(t, err, context.Canceled)
}

func TestSpliceToolReportsMalformedDocumentOnce(t *testing.T) {
	m := fixtureMagus(t)
	tool := &spliceTool{workspace: m}
	for _, apply := range []bool{false, true} {
		result, err := tool.Invoke(t.Context(), spells.InvokeRequest{Params: map[string]any{"document": "", "apply": apply}})
		require.NoError(t, err)
		response := result.Data.(spliceResponse)
		require.Len(t, response.Plan.Refused, 1)
		assert.Equal(t, "MCP splice", response.Plan.Refused[0].Path)
		if apply {
			require.NotNil(t, response.Applied)
			assert.False(t, response.Applied.IsOk())
		}
	}
}

func TestSpliceToolDeniesUnstampedHTTPApply(t *testing.T) {
	m := fixtureMagus(t)
	path := filepath.Join(m.Root(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))
	tool := &spliceTool{workspace: m}
	doc := "## file.txt\n```old\nbefore\n```\n```new\nafter\n```\n"

	ctx := withCallerLease(context.Background(), "")
	result, err := tool.Invoke(ctx, spells.InvokeRequest{Params: map[string]any{"document": doc, "apply": true}})
	require.NoError(t, err)
	response := result.Data.(spliceResponse)
	require.NotNil(t, response.Applied)
	assert.False(t, response.Applied.IsOk(), "an HTTP caller without a lease cannot write")
	assert.Equal(t, "before\n", readSpliceFile(t, path))
}

func TestSpliceToolDeniesWorkspaceEscape(t *testing.T) {
	m := fixtureMagus(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("before\n"), 0o644))
	tool := &spliceTool{workspace: m}
	doc := fmt.Sprintf("## %s\n```old\nbefore\n```\n```new\nafter\n```\n", outside)

	result, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"document": doc, "apply": true}})
	require.NoError(t, err)
	response := result.Data.(spliceResponse)
	require.NotNil(t, response.Applied)
	assert.False(t, response.Applied.IsOk(), "the policy bounds writes to the workspace")
	assert.Equal(t, "before\n", readSpliceFile(t, outside))
}

func TestSplicePublicProtocolRejectsUnknownParameter(t *testing.T) {
	m := fixtureMagus(t)
	tool := &spliceTool{workspace: m}
	desc := descriptor(t, hint.ToolSplice)
	handler := authorize(declaredParams(desc, adapt(tool)))
	ctx := recorder.ContextWithCredential(bgContext(), auth.CredentialStdio())
	result, err := handler(ctx, callRequest(tool.Name(), map[string]any{"document": "", "surprise": true}))
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcplib.TextContent).Text, "unknown parameter")
}

func readSpliceFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}
