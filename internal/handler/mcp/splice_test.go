package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/spells"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpliceToolAppliesExistingBlockDocument(t *testing.T) {
	m := fixtureMagus(t)
	path := filepath.Join(m.Root(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))
	tool := &spliceTool{root: m.Root(), workspace: m}
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
	tool := &spliceTool{root: m.Root(), workspace: m}
	handler := authorize(declaredParams(descriptor(t, hint.ToolSplice), adapt(tool)))
	ctx := recorder.ContextWithCredential(bgCtx(), auth.CredentialStdio())
	doc := "## file.txt\n```old\nbefore\n```\n```new\nafter\n```\n"

	preview, err := handler(ctx, callRequest(tool.Name(), map[string]any{"document": doc}))
	require.NoError(t, err)
	require.False(t, preview.IsError, preview.Content)
	assert.NotEmpty(t, preview.StructuredContent)
	assert.Equal(t, "before\n", readSpliceFile(t, path))

	result, err := handler(ctx, callRequest(tool.Name(), map[string]any{"document": doc, "apply": true}))
	require.NoError(t, err)
	require.False(t, result.IsError, result.Content)
	assert.Equal(t, "after\n", readSpliceFile(t, path))
}

func TestSpliceToolDeniesUnstampedHTTPApply(t *testing.T) {
	m := fixtureMagus(t)
	path := filepath.Join(m.Root(), "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("before\n"), 0o644))
	tool := &spliceTool{root: m.Root(), workspace: m}
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
	tool := &spliceTool{root: m.Root(), workspace: m}
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
	tool := &spliceTool{root: m.Root(), workspace: m}
	desc := descriptor(t, hint.ToolSplice)
	handler := authorize(declaredParams(desc, adapt(tool)))
	ctx := recorder.ContextWithCredential(bgCtx(), auth.CredentialStdio())
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
