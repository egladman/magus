package std

import (
	"context"
	"reflect"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// op builds the expected op a constructor emits. Typed, like what it compares against:
// the constructors return spells.Charm rather than hand-built maps, so a field the test
// misspells is now a compile error instead of a silently-absent key.
func op(fields ...string) spells.PatchOp {
	o := spells.PatchOp{Op: spells.PatchOpKind(fields[0]), Path: fields[1]}
	if len(fields) > 2 {
		o.Value = fields[2]
	}
	return o
}

func wantCharm(ops ...spells.PatchOp) spells.Charm {
	return spells.Charm{Ops: ops}
}

func TestCharmConstructors(t *testing.T) {
	ctx := context.Background()
	argv := []string{"run", "ruff", "check", "."}

	assertCharm := func(want spells.Charm, got spells.Charm, err error) {
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
		assertCharm(wantCharm(spells.PatchOp{Op: "remove", Path: "/2"}), got, err)
	})
	t.Run("move to front", func(t *testing.T) {
		got, err := CharmMove(ctx, argv, "check", "/0")
		assertCharm(wantCharm(spells.PatchOp{Op: "move", From: "/2", Path: "/0"}), got, err)
	})
	t.Run("move to end", func(t *testing.T) {
		got, err := CharmMove(ctx, argv, "run", "/-")
		assertCharm(wantCharm(spells.PatchOp{Op: "move", From: "/0", Path: "/-"}), got, err)
	})
	t.Run("copy to end", func(t *testing.T) {
		got, err := CharmCopy(ctx, argv, "check", "/-")
		assertCharm(wantCharm(spells.PatchOp{Op: "copy", From: "/2", Path: "/-"}), got, err)
	})
	t.Run("test guard", func(t *testing.T) {
		got, err := CharmTest(ctx, argv, "check")
		assertCharm(wantCharm(spells.PatchOp{Op: "test", Path: "/2", Value: "check"}), got, err)
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

// The charm constructors, the decoder that reads them back, and the generated PatchOp
// mirror must all spell the move/copy source the same way. They did not: the
// constructors emitted "from" (the RFC 6902 / JSON name) while the mirror declared
// fromPtr, because `from` is a reserved word in Buzz and no mirror can use it. Nothing
// noticed, because nothing referenced the Buzz name - so a magusfile annotating a charm
// would have read an empty field forever.
func TestCharmMoveUsesTheBuzzFieldName(t *testing.T) {
	got, err := CharmMove(context.Background(), []string{"a", "b", "c"}, "c", "/0")
	require.NoError(t, err)
	require.Len(t, got.Ops, 1)
	assert.Equal(t, "/2", got.Ops[0].From, "the move source is the anchor's index")

	// The name the Buzz side sees comes from the struct tag, which is the point of
	// returning spells.Charm rather than a hand-built map: the constructors used to emit
	// "from" while the generated mirror declared fromPtr, and nothing reconciled them.
	// A tag cannot drift from the field it is written on.
	f, ok := reflect.TypeFor[spells.PatchOp]().FieldByName("From")
	require.True(t, ok)
	assert.Equal(t, "fromPtr", f.Tag.Get("buzz"),
		"`from` is reserved in Buzz, so the mirror and the encoder must both say fromPtr")
	assert.Equal(t, "from,omitempty", f.Tag.Get("json"),
		"the wire form stays RFC 6902's `from`")
}
