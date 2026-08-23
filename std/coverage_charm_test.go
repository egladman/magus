package std

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covPredicate adapts a Go func to the VM predicate a charm _func constructor
// calls. It returns `any` rather than bool on purpose: callPredicate's truthiness
// follows the source language, where every value but nil and false is true.
type covPredicate func(string) any

func (f covPredicate) Call(_ context.Context, args ...any) ([]any, error) {
	s, _ := args[0].(string)
	return []any{f(s)}, nil
}

// covRaisingPredicate is a predicate that fails instead of answering.
type covRaisingPredicate struct{ err error }

func (c covRaisingPredicate) Call(context.Context, ...any) ([]any, error) { return nil, c.err }

// covSilentPredicate returns no values at all, the shape a VM function with no
// return statement produces.
type covSilentPredicate struct{}

func (covSilentPredicate) Call(context.Context, ...any) ([]any, error) { return nil, nil }

// covIsCheck matches the one argv element the predicate-form tests anchor on.
var covIsCheck = covPredicate(func(s string) any { return s == "check" })

// TestCharmFuncConstructors is the predicate half of the constructor set: the
// anchor is found by calling back into the VM rather than by string equality, and
// the resulting patch must be identical to the equality form's.
func TestCharmFuncConstructors(t *testing.T) {
	ctx := context.Background()
	argv := []string{"run", "ruff", "check", "."}

	assertCharm := func(t *testing.T, want spells.Charm, got spells.Charm, err error) {
		t.Helper()
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	t.Run("after_func", func(t *testing.T) {
		got, err := CharmAfterFunc(ctx, argv, covIsCheck, []string{"--fix"})
		assertCharm(t, wantCharm(op("add", "/3", "--fix")), got, err)
	})
	t.Run("before_func", func(t *testing.T) {
		got, err := CharmBeforeFunc(ctx, argv, covIsCheck, []string{"--fix", "-v"})
		assertCharm(t, wantCharm(op("add", "/2", "--fix"), op("add", "/3", "-v")), got, err)
	})
	t.Run("set_func", func(t *testing.T) {
		got, err := CharmSetFunc(ctx, argv, covIsCheck, "format")
		assertCharm(t, wantCharm(op("replace", "/2", "format")), got, err)
	})
	t.Run("drop_func", func(t *testing.T) {
		got, err := CharmDropFunc(ctx, argv, covIsCheck)
		assertCharm(t, wantCharm(spells.PatchOp{Op: "remove", Path: "/2"}), got, err)
	})
	t.Run("move_func", func(t *testing.T) {
		got, err := CharmMoveFunc(ctx, argv, covIsCheck, "/0")
		assertCharm(t, wantCharm(spells.PatchOp{Op: "move", From: "/2", Path: "/0"}), got, err)
	})
	t.Run("copy_func", func(t *testing.T) {
		got, err := CharmCopyFunc(ctx, argv, covIsCheck, "/-")
		assertCharm(t, wantCharm(spells.PatchOp{Op: "copy", From: "/2", Path: "/-"}), got, err)
	})
	t.Run("test_func guards the matched element", func(t *testing.T) {
		got, err := CharmTestFunc(ctx, argv, covIsCheck)
		assertCharm(t, wantCharm(spells.PatchOp{Op: "test", Path: "/2", Value: "check"}), got, err)
	})
	t.Run("path_func", func(t *testing.T) {
		got, err := CharmPathFunc(ctx, argv, covIsCheck)
		require.NoError(t, err)
		assert.Equal(t, "/2", got)
	})
}

// TestCharmFuncNoMatch: a predicate that matches nothing is a spell bug, surfaced
// at author time rather than mis-targeting index 0.
func TestCharmFuncNoMatch(t *testing.T) {
	ctx := context.Background()
	argv := []string{"a", "b"}
	never := covPredicate(func(string) any { return false })

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"after_func", func() error { _, err := CharmAfterFunc(ctx, argv, never, []string{"x"}); return err }},
		{"before_func", func() error { _, err := CharmBeforeFunc(ctx, argv, never, []string{"x"}); return err }},
		{"set_func", func() error { _, err := CharmSetFunc(ctx, argv, never, "x"); return err }},
		{"drop_func", func() error { _, err := CharmDropFunc(ctx, argv, never); return err }},
		{"path_func", func() error { _, err := CharmPathFunc(ctx, argv, never); return err }},
		{"move_func", func() error { _, err := CharmMoveFunc(ctx, argv, never, "/0"); return err }},
		{"copy_func", func() error { _, err := CharmCopyFunc(ctx, argv, never, "/0"); return err }},
		{"test_func", func() error { _, err := CharmTestFunc(ctx, argv, never); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "no argv element matched the predicate")
		})
	}
}

// TestCharmFuncPropagatesAPredicateFailure: a raise inside the VM predicate is the
// caller's error, not a silent "no match".
func TestCharmFuncPropagatesAPredicateFailure(t *testing.T) {
	boom := errors.New("predicate exploded")
	fn := covRaisingPredicate{err: boom}

	_, err := CharmPathFunc(context.Background(), []string{"a", "b"}, fn)
	assert.ErrorIs(t, err, boom)

	_, err = CharmAfterFunc(context.Background(), []string{"a", "b"}, fn, []string{"x"})
	assert.ErrorIs(t, err, boom)
}

// TestCharmFuncTruthiness pins the truthiness rule the predicate form inherits
// from the source language: only nil, false, and a return of nothing are false.
func TestCharmFuncTruthiness(t *testing.T) {
	ctx := context.Background()
	argv := []string{"a", "b"}

	for _, tc := range []struct {
		name    string
		ret     any
		wantPtr string
	}{
		{"a string is truthy", "yes", "/0"},
		{"zero is truthy", 0, "/0"},
		{"true is truthy", true, "/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CharmPathFunc(ctx, argv, covPredicate(func(string) any { return tc.ret }))
			require.NoError(t, err)
			assert.Equal(t, tc.wantPtr, got)
		})
	}

	for _, tc := range []struct {
		name string
		fn   Callback
	}{
		{"nil is falsy", covPredicate(func(string) any { return nil })},
		{"false is falsy", covPredicate(func(string) any { return false })},
		{"returning nothing is falsy", covSilentPredicate{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CharmPathFunc(ctx, argv, tc.fn)
			assert.Error(t, err, "a falsy predicate matches nothing")
		})
	}
}

// TestCharmDestinationMustBeAPointer: move and copy validate the destination at
// author time so the hint arrives with the magusfile, not at patch-decode time.
func TestCharmDestinationMustBeAPointer(t *testing.T) {
	ctx := context.Background()
	argv := []string{"run", "ruff", "check", "."}

	for _, to := range []string{"", "check", "0"} {
		_, err := CharmMoveFunc(ctx, argv, covIsCheck, to)
		require.Errorf(t, err, "move_func to %q", to)
		assert.Contains(t, err.Error(), "must be a JSON Pointer")

		_, err = CharmCopyFunc(ctx, argv, covIsCheck, to)
		require.Errorf(t, err, "copy_func to %q", to)

		_, err = CharmCopy(ctx, argv, "check", to)
		require.Errorf(t, err, "copy to %q", to)
	}
}
