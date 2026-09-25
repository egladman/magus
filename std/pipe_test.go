package std

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

const (
	pipeScopeLine  = `{"schema":5,"type":"run.scope","label":"api","projects":["api"]}`
	pipeFailedLine = `{"schema":5,"type":"run.target.result","project":"api","target":"test","status":"failed","cache_hit":false,"error":"exit 1","ref":"out0123456789ab"}`
)

func pipeCtx(t *testing.T, upstream string) (context.Context, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return WithPipe(t.Context(), PipeIO{In: report.NewReader(strings.NewReader(upstream), nil), Upstream: 42, Out: &out}), &out
}

// A script reads the upstream's records in order as typed values, with the fields it
// filters on lifted out and the whole record kept as its body.
func TestPipeReadsRecordsInOrder(t *testing.T) {
	ctx, _ := pipeCtx(t, pipeScopeLine+"\n"+pipeFailedLine+"\n")

	more, err := PipeMore(ctx)
	require.NoError(t, err)
	require.True(t, more)
	first, err := PipeNext(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.PipeRecord{Schema: 5, Type: "run.scope", Projects: []string{"api"}, Body: pipeScopeLine}, first)

	rest, err := PipeAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, []types.PipeRecord{{
		Schema: 5, Type: "run.target.result", Project: "api", Target: "test", Status: "failed",
		Error: "exit 1", Ref: "out0123456789ab", Body: pipeFailedLine,
	}}, rest)

	more, err = PipeMore(ctx)
	require.NoError(t, err)
	assert.False(t, more, "the upstream ended")
	_, err = PipeNext(ctx)
	assert.ErrorContains(t, err, "every record is read")
}

// Reading from a script no record stage feeds is an error, never an empty stream that
// passes for "nothing failed".
func TestPipeWithoutAnUpstreamRaises(t *testing.T) {
	ctx := WithPipe(t.Context(), PipeIO{Out: &bytes.Buffer{}})
	_, err := PipeMore(ctx)
	assert.ErrorContains(t, err, "no magus stage writing records feeds")
	_, err = PipeNext(ctx)
	assert.ErrorContains(t, err, "pipe.next")
	_, err = PipeAll(context.Background())
	assert.ErrorContains(t, err, "pipe.all")
	assert.ErrorContains(t, PipeEmit(context.Background(), map[string]any{"type": "run.scope"}), "not a pipe stage")
}

// value reads a run.target.value record's value, a str or a list, and refuses any other.
func TestPipeValue(t *testing.T) {
	for _, tc := range []struct {
		body string
		want any
	}{
		{`{"schema":5,"type":"run.target.value","project":".","target":"describe","value":"v1.2.3"}`, "v1.2.3"},
		{`{"schema":5,"type":"run.target.value","project":".","target":"listy","value":["alpha","beta"]}`, []any{"alpha", "beta"}},
	} {
		got, err := PipeValue(t.Context(), map[string]any{"type": report.TypeTargetValue, "body": tc.body})
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	_, err := PipeValue(t.Context(), map[string]any{"type": "run.target.result", "body": pipeFailedLine})
	assert.ErrorContains(t, err, "carries no value")
}

// The artifact members need a path to act on and a workspace to find it in, and say
// which is missing rather than acting on nothing.
func TestPipeArtifactMembersRefuseWhatTheyCannotUse(t *testing.T) {
	_, err := PipeOutputs(t.Context(), map[string]any{"type": "run.scope"})
	assert.ErrorContains(t, err, "names no target")
	_, err = PipeOutputs(t.Context(), map[string]any{"project": "api", "target": "build"})
	assert.ErrorContains(t, err, "no workspace is attached")
	_, err = PipeExport(t.Context(), map[string]any{"glob": "dist/*"}, "out")
	assert.ErrorContains(t, err, "names no path")
	_, err = PipeHistory(t.Context(), map[string]any{"path": "dist/a"})
	assert.ErrorContains(t, err, "no workspace is attached")
	assert.ErrorContains(t, PipeDiff(t.Context(), map[string]any{}), "names no path")
}

// exportArtifact replaces what sits at dst, a symlink included, keeps the source's
// mode, survives dst == src, and leaves no temp file behind.
func TestExportArtifact(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dist", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o755))
	require.NoError(t, os.WriteFile(src, []byte("PRECIOUS"), 0o755))

	require.NoError(t, exportArtifact(src, src))
	got, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, "PRECIOUS", string(got), "an artifact exported onto itself survives")

	outside := filepath.Join(dir, "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("DO-NOT-OVERWRITE"), 0o600))
	link := filepath.Join(dir, "sym", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(outside, link))
	require.NoError(t, exportArtifact(src, link))
	kept, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "DO-NOT-OVERWRITE", string(kept), "the symlink's target is untouched")
	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode(), "a regular file with the source's mode replaced the link")

	entries, err := os.ReadDir(filepath.Dir(link))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp file is left")
}

// A record read upstream passes through byte for byte; one a script builds is written as
// magus writes a record, schema and type first, so the next stage reads it as one.
func TestPipeEmit(t *testing.T) {
	ctx, out := pipeCtx(t, "")
	require.NoError(t, PipeEmit(ctx, map[string]any{"type": "run.target.result", "body": pipeFailedLine}))
	require.NoError(t, PipeEmit(ctx, map[string]any{
		"schema": int64(0), "type": "run.scope", "project": "", "projects": []any{"libs/x", "."},
	}))
	assert.Equal(t, pipeFailedLine+"\n"+`{"schema":5,"type":"run.scope","projects":["libs/x","."]}`+"\n", out.String())

	assert.ErrorContains(t, PipeEmit(ctx, map[string]any{"project": "api"}), "needs a type")
	assert.ErrorContains(t, PipeEmit(ctx, map[string]any{"body": "not json"}), "not a record")
}
