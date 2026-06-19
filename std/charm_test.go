package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// op builds the expected op map a constructor emits.
func op(fields ...string) map[string]any {
	m := map[string]any{"op": fields[0], "path": fields[1]}
	if len(fields) > 2 {
		m["value"] = fields[2]
	}
	return m
}

func wantCharm(ops ...map[string]any) map[string]any {
	arr := make([]any, len(ops))
	for i := range ops {
		arr[i] = ops[i]
	}
	return map[string]any{"ops": arr}
}

func TestCharmConstructors(t *testing.T) {
	ctx := context.Background()
	argv := []string{"run", "ruff", "check", "."}

	assertCharm := func(want map[string]any, got map[string]any, err error) {
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	t.Run("append", func(t *testing.T) {
		got, err := CharmAppend(ctx, []string{"-v", "-x"})
		assertCharm(wantCharm(op("add", "/-", "-v"), op("add", "/-", "-x")), got, err)
	})
	t.Run("prepend", func(t *testing.T) {
		got, err := CharmPrepend(ctx, []string{"a", "b"})
		assertCharm(wantCharm(op("add", "/0", "a"), op("add", "/1", "b")), got, err)
	})
	t.Run("after", func(t *testing.T) {
		got, err := CharmAfter(ctx, argv, "check", []string{"--fix"})
		assertCharm(wantCharm(op("add", "/3", "--fix")), got, err)
	})
	t.Run("before", func(t *testing.T) {
		got, err := CharmBefore(ctx, argv, "check", []string{"--fix"})
		assertCharm(wantCharm(op("add", "/2", "--fix")), got, err)
	})
	t.Run("set", func(t *testing.T) {
		got, err := CharmSet(ctx, argv, "check", "format")
		assertCharm(wantCharm(op("replace", "/2", "format")), got, err)
	})
	t.Run("drop", func(t *testing.T) {
		got, err := CharmDrop(ctx, argv, "check")
		assertCharm(wantCharm(map[string]any{"op": "remove", "path": "/2"}), got, err)
	})
	t.Run("move to front", func(t *testing.T) {
		got, err := CharmMove(ctx, argv, "check", "/0")
		assertCharm(wantCharm(map[string]any{"op": "move", "from": "/2", "path": "/0"}), got, err)
	})
	t.Run("move to end", func(t *testing.T) {
		got, err := CharmMove(ctx, argv, "run", "/-")
		assertCharm(wantCharm(map[string]any{"op": "move", "from": "/0", "path": "/-"}), got, err)
	})
	t.Run("copy to end", func(t *testing.T) {
		got, err := CharmCopy(ctx, argv, "check", "/-")
		assertCharm(wantCharm(map[string]any{"op": "copy", "from": "/2", "path": "/-"}), got, err)
	})
	t.Run("test guard", func(t *testing.T) {
		got, err := CharmTest(ctx, argv, "check")
		assertCharm(wantCharm(map[string]any{"op": "test", "path": "/2", "value": "check"}), got, err)
	})
}

// TestCharmAnchorNotFound: a missing anchor is a spell bug surfaced at author time.
func TestCharmAnchorNotFound(t *testing.T) {
	ctx := context.Background()
	_, err := CharmAfter(ctx, []string{"a", "b"}, "missing", []string{"x"})
	assert.Error(t, err, "after with missing anchor: want error")
	_, err = CharmDrop(ctx, []string{"a", "b"}, "missing")
	assert.Error(t, err, "drop with missing anchor: want error")
	_, err = CharmPath(ctx, []string{"a", "b"}, "missing")
	assert.Error(t, err, "path with missing anchor: want error")
	_, err = CharmMove(ctx, []string{"a", "b"}, "missing", "/0")
	assert.Error(t, err, "move with missing anchor: want error")
	// A destination that isn't a JSON Pointer is rejected at author time.
	_, err = CharmMove(ctx, []string{"a", "b"}, "a", "b")
	assert.Error(t, err, "move with non-pointer destination: want error")
}

// TestCharmPath: the path helper resolves an anchor to its 0-based JSON Pointer.
func TestCharmPath(t *testing.T) {
	ctx := context.Background()
	argv := []string{"run", "ruff", "check", "."}
	got, err := CharmPath(ctx, argv, "check")
	require.NoError(t, err)
	assert.Equal(t, "/2", got)
	got, err = CharmPath(ctx, argv, "run")
	require.NoError(t, err)
	assert.Equal(t, "/0", got)
}
