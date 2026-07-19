package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memoryv1 "github.com/egladman/magus/proto/gen/go/magus/memory/v1"
)

// fakeWorkspace reports a fixed root; the handler only reads Root().
type fakeWorkspace struct{ root string }

func (f fakeWorkspace) Root() string { return f.root }

func req[T any](msg *T) *connect.Request[T] { return connect.NewRequest(msg) }

// newTestService points the memory directory at a temp dir (bypassing the per-repo
// resolution) so a test operates on real files without a live workspace.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	return &Service{ws: fakeWorkspace{root: dir}, dir: func(string) (string, error) { return dir, nil }}, dir
}

// TestListReportsMetadataOnlyAndOmitsContent seeds one file and lists: every known file
// appears in order, the seeded one reports exists+size, absent ones report exists=false,
// and List never carries content (it is metadata only).
func TestListReportsMetadataOnlyAndOmitsContent(t *testing.T) {
	s, dir := newTestService(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status.md"), []byte("# hi\n"), 0o644))

	resp, err := s.ListMemory(context.Background(), req(&memoryv1.ListMemoryRequest{}))
	require.NoError(t, err)
	docs := resp.Msg.GetDocs()
	require.Len(t, docs, 3)
	assert.Equal(t, dir, resp.Msg.GetDir())

	assert.Equal(t, memoryv1.MemoryFile_MEMORY_FILE_STATUS, docs[0].GetFile())
	assert.Equal(t, "status.md", docs[0].GetName())
	assert.True(t, docs[0].GetExists())
	assert.Equal(t, int64(5), docs[0].GetSizeBytes())
	assert.Empty(t, docs[0].GetContent(), "List must not carry content")

	assert.Equal(t, memoryv1.MemoryFile_MEMORY_FILE_PROGRESS, docs[1].GetFile())
	assert.False(t, docs[1].GetExists())
	assert.Equal(t, memoryv1.MemoryFile_MEMORY_FILE_DECISIONS, docs[2].GetFile())
	assert.False(t, docs[2].GetExists())
}

// TestPutGetDeleteRoundTrip exercises the write/read/delete lifecycle on the real files.
func TestPutGetDeleteRoundTrip(t *testing.T) {
	s, dir := newTestService(t)

	// A never-written file reads back empty, not an error.
	got, err := s.GetMemory(context.Background(), req(&memoryv1.GetMemoryRequest{File: memoryv1.MemoryFile_MEMORY_FILE_DECISIONS}))
	require.NoError(t, err)
	assert.False(t, got.Msg.GetDoc().GetExists())
	assert.Empty(t, got.Msg.GetDoc().GetContent())

	// Put writes the file to disk and echoes the stored doc.
	put, err := s.PutMemory(context.Background(), req(&memoryv1.PutMemoryRequest{
		File: memoryv1.MemoryFile_MEMORY_FILE_DECISIONS, Content: "## 2026-01-02\nkeep sha256\n",
	}))
	require.NoError(t, err)
	assert.True(t, put.Msg.GetDoc().GetExists())
	onDisk, err := os.ReadFile(filepath.Join(dir, "decisions.md"))
	require.NoError(t, err)
	assert.Equal(t, "## 2026-01-02\nkeep sha256\n", string(onDisk))

	// Get returns the written content.
	got, err = s.GetMemory(context.Background(), req(&memoryv1.GetMemoryRequest{File: memoryv1.MemoryFile_MEMORY_FILE_DECISIONS}))
	require.NoError(t, err)
	assert.Equal(t, "## 2026-01-02\nkeep sha256\n", got.Msg.GetDoc().GetContent())

	// Delete removes the file and reports it absent; a second delete is a no-op success.
	del, err := s.DeleteMemory(context.Background(), req(&memoryv1.DeleteMemoryRequest{File: memoryv1.MemoryFile_MEMORY_FILE_DECISIONS}))
	require.NoError(t, err)
	assert.False(t, del.Msg.GetDoc().GetExists())
	assert.NoFileExists(t, filepath.Join(dir, "decisions.md"))
	_, err = s.DeleteMemory(context.Background(), req(&memoryv1.DeleteMemoryRequest{File: memoryv1.MemoryFile_MEMORY_FILE_DECISIONS}))
	require.NoError(t, err)
}

// TestUnspecifiedFileRejected proves an UNSPECIFIED (or unknown) file never reaches the
// filesystem: it is an InvalidArgument on every mutating and reading RPC.
func TestUnspecifiedFileRejected(t *testing.T) {
	s, _ := newTestService(t)
	_, err := s.GetMemory(context.Background(), req(&memoryv1.GetMemoryRequest{File: memoryv1.MemoryFile_MEMORY_FILE_UNSPECIFIED}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = s.PutMemory(context.Background(), req(&memoryv1.PutMemoryRequest{File: memoryv1.MemoryFile_MEMORY_FILE_UNSPECIFIED, Content: "x"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
