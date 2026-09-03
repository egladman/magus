package memory

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	memoryv1 "github.com/egladman/magus/proto/gen/go/magus/memory/v1alpha1"
)

// fakeWorkspace reports a fixed root; the handler only reads Root().
type fakeWorkspace struct{ root string }

func (f fakeWorkspace) Root() string { return f.root }

func req[T any](msg *T) *connect.Request[T] { return connect.NewRequest(msg) }

// newTestService isolates the store: XDG_STATE_HOME points at a temp dir and the
// workspace root is a temp dir (no .git, so it keys on itself), so the RPC operates on a
// real store without a live workspace.
func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return &Service{ws: fakeWorkspace{root: t.TempDir()}}
}

func pointer(name, target string) *memoryv1.Memory {
	return &memoryv1.Memory{
		Name: name, Type: memoryv1.MemoryType_MEMORY_TYPE_POINTER,
		Refs: []*memoryv1.MemoryRef{{Kind: memoryv1.MemoryRefKind_MEMORY_REF_KIND_NODE, Target: target}},
	}
}

// TestUpdateIsUpsertAndListRoundTrips proves UpdateMemory with allow_missing creates, the
// record survives a list round trip with server-set timestamps, and enum mapping holds.
func TestUpdateIsUpsertAndListRoundTrips(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	up, err := s.UpdateMemory(ctx, req(&memoryv1.UpdateMemoryRequest{Memory: pointer("cache-op-surface", "project:magus"), AllowMissing: true}))
	require.NoError(t, err)
	assert.NotNil(t, up.Msg.GetCreateTime(), "the store stamps create_time")

	list, err := s.ListMemories(ctx, req(&memoryv1.ListMemoriesRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetMemories(), 1)
	got := list.Msg.GetMemories()[0]
	assert.Equal(t, "cache-op-surface", got.GetName())
	assert.Equal(t, memoryv1.MemoryType_MEMORY_TYPE_POINTER, got.GetType())
	require.Len(t, got.GetRefs(), 1)
	assert.Equal(t, memoryv1.MemoryRefKind_MEMORY_REF_KIND_NODE, got.GetRefs()[0].GetKind())
	assert.Equal(t, "project:magus", got.GetRefs()[0].GetTarget())
}

// TestUpdateMissingWithoutAllowMissingIsNotFound proves the update-only path (allow_missing
// false) rejects an absent record instead of silently creating it.
func TestUpdateMissingWithoutAllowMissingIsNotFound(t *testing.T) {
	s := newTestService(t)
	_, err := s.UpdateMemory(context.Background(), req(&memoryv1.UpdateMemoryRequest{Memory: pointer("ghost", "project:magus")}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestUpdateHonorsAPartialMask proves a named field is the only one written, and that a
// field the mask omits keeps what is stored.
func TestUpdateHonorsAPartialMask(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	full := pointer("cache-op-surface", "project:magus")
	full.Status = "active"
	_, err := s.UpdateMemory(ctx, req(&memoryv1.UpdateMemoryRequest{Memory: full, AllowMissing: true}))
	require.NoError(t, err)

	patch := &memoryv1.Memory{Name: "cache-op-surface", Status: "done"}
	got, err := s.UpdateMemory(ctx, req(&memoryv1.UpdateMemoryRequest{
		Memory:     patch,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, "done", got.Msg.GetStatus())
	require.Len(t, got.Msg.GetRefs(), 1, "a field outside the mask keeps what is stored")
	assert.Equal(t, "project:magus", got.Msg.GetRefs()[0].GetTarget())
}

// TestUpdateWithNoMaskIsAFullReplace pins the console's stated decision: its caller is a
// person editing a form that submits every box, so an emptied one clears the field.
func TestUpdateWithNoMaskIsAFullReplace(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	full := pointer("cache-op-surface", "project:magus")
	full.Status = "active"
	_, err := s.UpdateMemory(ctx, req(&memoryv1.UpdateMemoryRequest{Memory: full, AllowMissing: true}))
	require.NoError(t, err)

	cleared := pointer("cache-op-surface", "project:magus")
	got, err := s.UpdateMemory(ctx, req(&memoryv1.UpdateMemoryRequest{Memory: cleared, AllowMissing: true}))
	require.NoError(t, err)
	assert.Empty(t, got.Msg.GetStatus(), "the form submitted an empty status, so the status is cleared")
}

// TestUpdateRejectsInvalidRecord proves the store's schema validation surfaces as
// InvalidArgument: a pointer with no ref is refused at the door.
func TestUpdateRejectsInvalidRecord(t *testing.T) {
	s := newTestService(t)
	bad := &memoryv1.Memory{Name: "no-refs", Type: memoryv1.MemoryType_MEMORY_TYPE_POINTER}
	_, err := s.UpdateMemory(context.Background(), req(&memoryv1.UpdateMemoryRequest{Memory: bad, AllowMissing: true}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestDelete covers idempotent and strict delete semantics.
func TestDelete(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	_, err := s.UpdateMemory(ctx, req(&memoryv1.UpdateMemoryRequest{Memory: pointer("real", "project:magus"), AllowMissing: true}))
	require.NoError(t, err)

	_, err = s.DeleteMemory(ctx, req(&memoryv1.DeleteMemoryRequest{Name: "real"}))
	require.NoError(t, err)

	// Idempotent delete of an absent record succeeds; strict delete of an absent one is NotFound.
	_, err = s.DeleteMemory(ctx, req(&memoryv1.DeleteMemoryRequest{Name: "real", AllowMissing: true}))
	require.NoError(t, err)
	_, err = s.DeleteMemory(ctx, req(&memoryv1.DeleteMemoryRequest{Name: "real", AllowMissing: false}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestCursorWriteIsRetired keeps old console clients from silently overwriting another
// session's handoff while preserving a read path for an existing legacy cursor.
func TestCursorWriteIsRetired(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	got, err := s.GetCursor(ctx, req(&memoryv1.GetCursorRequest{}))
	require.NoError(t, err)
	assert.Empty(t, got.Msg.GetContent(), "an unwritten cursor reads empty")

	_, err = s.UpdateCursor(ctx, req(&memoryv1.UpdateCursorRequest{Content: "resuming the RPC handler"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	got, err = s.GetCursor(ctx, req(&memoryv1.GetCursorRequest{}))
	require.NoError(t, err)
	assert.Empty(t, got.Msg.GetContent())
}
