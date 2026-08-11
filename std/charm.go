package std

import (
	"context"
	"fmt"
	"github.com/egladman/magus/spells"
	"slices"
	"strconv"
)

//go:generate go run ../cmd/magus-utils bindings -module charm -lang buzz -out ../internal/interp/bindings/gen/charm.go

func init() { Register(Charm) }

// Charm is magus.extra.charm: the constructor set for the values a spell target
// lists under `charms`. Each constructor returns an RFC 6902 JSON Patch (the
// { ops = [...] } record Decode reads) over the target's base argv, treated as a
// JSON array of strings. Anchors are resolved to numeric pointers here, at spell
// author/load time, so the stored patch is pure positional RFC 6902. The patches
// of the active charms are concatenated and applied at run time (fork.go).
var Charm = Module{
	Name: "charm",
	Doc:  "Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md).",
	Methods: []Method{
		{
			Name:    "append",
			Doc:     "Append vals to the end of the argv.",
			Args:    []Arg{{Name: "vals", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmAppend,
		},
		{
			Name:    "prepend",
			Doc:     "Insert vals at the front of the argv, in order.",
			Args:    []Arg{{Name: "vals", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmPrepend,
		},
		{
			Name:    "after",
			Doc:     "Insert vals immediately after the first argv element equal to anchor.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "vals", Type: TypeStringSlice}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmAfter,
		},
		{
			Name:    "before",
			Doc:     "Insert vals immediately before the first argv element equal to anchor.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "vals", Type: TypeStringSlice}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmBefore,
		},
		{
			Name:    "set",
			Doc:     "Replace the first argv element equal to anchor with val.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "val", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmSet,
		},
		{
			// "drop", not "remove": the Buzz surface exposes this as a map member
			// (charm.drop), and "remove" would be shadowed by the built-in map
			// .remove() method, so a charm.remove call could never reach this.
			Name:    "drop",
			Doc:     "Drop (remove) the first argv element equal to anchor.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmDrop,
		},
		{
			Name:    "after_func",
			Doc:     "Insert vals after the first argv element for which fn(s) is truthy.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "vals", Type: TypeStringSlice}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmAfterFunc,
		},
		{
			Name:    "before_func",
			Doc:     "Insert vals before the first argv element for which fn(s) is truthy.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "vals", Type: TypeStringSlice}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmBeforeFunc,
		},
		{
			Name:    "set_func",
			Doc:     "Replace the first argv element for which fn(s) is truthy with val.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "val", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmSetFunc,
		},
		{
			Name:    "drop_func",
			Doc:     "Drop (remove) the first argv element for which fn(s) is truthy.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmDropFunc,
		},
		{
			Name:    "path",
			Doc:     `Return the JSON Pointer ("/N") of the first argv element equal to anchor - the index, auto-calculated, for hand-built move/copy/test ops.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeString}}, Impl: CharmPath,
		},
		{
			Name:    "path_func",
			Doc:     `Return the JSON Pointer ("/N") of the first argv element for which fn(s) is truthy.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}},
			Raises:  true,
			Returns: []Ret{{Type: TypeString}}, Impl: CharmPathFunc,
		},
		{
			Name:    "move",
			Doc:     `Move the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "to", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmMove,
		},
		{
			Name:    "move_func",
			Doc:     `Move the first argv element for which fn(s) is truthy to the JSON Pointer to.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "to", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmMoveFunc,
		},
		{
			Name:    "copy",
			Doc:     `Copy the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "to", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmCopy,
		},
		{
			Name:    "copy_func",
			Doc:     `Copy the first argv element for which fn(s) is truthy to the JSON Pointer to.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "to", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmCopyFunc,
		},
		{
			Name:    "test",
			Doc:     `Guard: assert the first argv element equal to anchor is still at its position when the patch applies (else the run errors).`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmTest,
		},
		{
			Name:    "test_func",
			Doc:     `Guard: assert the first argv element for which fn(s) is truthy is still at its position when the patch applies.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}},
			Raises:  true,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Charm"}}, Impl: CharmTestFunc,
		},
	},
}

// charmResult wraps the ops as the spells.Charm every charm builder returns.
//
// It used to hand back map[string]any of map[string]any, hand-built, while spells.Charm
// and spells.PatchOp already described exactly that shape - the same duplication
// vcs.metadata was making over the typed accessors beside it. Returning the struct lets
// the module declare Object "Charm", so the checker knows the shape and the codegen's
// return-contract check verifies the declaration against the Impl. That check is not
// hypothetical here: hand-built maps are how the constructors came to emit "from" where
// the mirror said "fromPtr", and a struct field cannot drift from its own buzz tag.
func charmResult(ops ...spells.PatchOp) spells.Charm {
	return spells.Charm{Ops: ops}
}

// ptr renders a JSON Pointer to argv index i.
func ptr(i int) string { return "/" + strconv.Itoa(i) }

// addOps builds a run of `add` ops that insert vals starting at index start, so
// the values land in order (each subsequent insert sits one past the previous).
func addOps(start int, vals []string) spells.Charm {
	ops := make([]spells.PatchOp, len(vals))
	for k, v := range vals {
		ops[k] = spells.PatchOp{Op: spells.OpAdd, Path: ptr(start + k), Value: v}
	}
	return charmResult(ops...)
}

// anchorIndex returns the position of the first argv element equal to anchor, or
// an error - a not-found anchor is a spell bug, surfaced now (author/load time)
// rather than silently mis-targeting an index.
func anchorIndex(argv []string, anchor string) (int, error) {
	if i := slices.Index(argv, anchor); i >= 0 {
		return i, nil
	}
	return 0, fmt.Errorf("charm: anchor %q not found in argv %v", anchor, argv)
}

// anchorIndexFunc returns the position of the first argv element for which fn is
// truthy, or an error when none match (same fail-fast rationale as anchorIndex).
func anchorIndexFunc(ctx context.Context, argv []string, fn Callback) (int, error) {
	var cbErr error
	i := slices.IndexFunc(argv, func(s string) bool {
		if cbErr != nil {
			return false
		}
		ok, err := callPredicate(ctx, fn, s)
		if err != nil {
			cbErr = err
		}
		return ok
	})
	if cbErr != nil {
		return 0, cbErr
	}
	if i < 0 {
		return 0, fmt.Errorf("charm: no argv element matched the predicate (argv %v)", argv)
	}
	return i, nil
}

// CharmAppend implements charm.append.
func CharmAppend(_ context.Context, vals []string) (spells.Charm, error) {
	ops := make([]spells.PatchOp, len(vals))
	for i, v := range vals {
		ops[i] = spells.PatchOp{Op: spells.OpAdd, Path: "/-", Value: v}
	}
	return charmResult(ops...), nil
}

// CharmPrepend implements charm.prepend.
func CharmPrepend(_ context.Context, vals []string) (spells.Charm, error) {
	return addOps(0, vals), nil
}

// CharmAfter implements charm.after.
func CharmAfter(_ context.Context, argv []string, anchor string, vals []string) (spells.Charm, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return spells.Charm{}, err
	}
	return addOps(i+1, vals), nil
}

// CharmBefore implements charm.before.
func CharmBefore(_ context.Context, argv []string, anchor string, vals []string) (spells.Charm, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return spells.Charm{}, err
	}
	return addOps(i, vals), nil
}

// CharmSet implements charm.set.
func CharmSet(_ context.Context, argv []string, anchor, val string) (spells.Charm, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpReplace, Path: ptr(i), Value: val}), nil
}

// CharmDrop implements charm.drop.
func CharmDrop(_ context.Context, argv []string, anchor string) (spells.Charm, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpRemove, Path: ptr(i)}), nil
}

// CharmAfterFunc implements charm.after_func.
func CharmAfterFunc(ctx context.Context, argv []string, fn Callback, vals []string) (spells.Charm, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return spells.Charm{}, err
	}
	return addOps(i+1, vals), nil
}

// CharmBeforeFunc implements charm.before_func.
func CharmBeforeFunc(ctx context.Context, argv []string, fn Callback, vals []string) (spells.Charm, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return spells.Charm{}, err
	}
	return addOps(i, vals), nil
}

// CharmSetFunc implements charm.set_func.
func CharmSetFunc(ctx context.Context, argv []string, fn Callback, val string) (spells.Charm, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpReplace, Path: ptr(i), Value: val}), nil
}

// CharmDropFunc implements charm.drop_func.
func CharmDropFunc(ctx context.Context, argv []string, fn Callback) (spells.Charm, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpRemove, Path: ptr(i)}), nil
}

// CharmPath implements charm.path: the JSON Pointer of the anchor element.
func CharmPath(_ context.Context, argv []string, anchor string) (string, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return "", err
	}
	return ptr(i), nil
}

// CharmPathFunc implements charm.path_func.
func CharmPathFunc(ctx context.Context, argv []string, fn Callback) (string, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return "", err
	}
	return ptr(i), nil
}

// destPointer validates a move/copy destination is a JSON Pointer, failing fast
// at author time with a hint rather than deferring to decode-time validation.
func destPointer(to string) error {
	if to == "" || to[0] != '/' {
		return fmt.Errorf("charm: destination %q must be a JSON Pointer (%q, %q, or charm.path(argv, x))", to, "/-", "/0")
	}
	return nil
}

// CharmMove implements charm.move.
func CharmMove(_ context.Context, argv []string, anchor, to string) (spells.Charm, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return spells.Charm{}, err
	}
	if err := destPointer(to); err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpMove, From: ptr(i), Path: to}), nil
}

// CharmMoveFunc implements charm.move_func.
func CharmMoveFunc(ctx context.Context, argv []string, fn Callback, to string) (spells.Charm, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return spells.Charm{}, err
	}
	if err := destPointer(to); err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpMove, From: ptr(i), Path: to}), nil
}

// CharmCopy implements charm.copy.
func CharmCopy(_ context.Context, argv []string, anchor, to string) (spells.Charm, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return spells.Charm{}, err
	}
	if err := destPointer(to); err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpCopy, From: ptr(i), Path: to}), nil
}

// CharmCopyFunc implements charm.copy_func.
func CharmCopyFunc(ctx context.Context, argv []string, fn Callback, to string) (spells.Charm, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return spells.Charm{}, err
	}
	if err := destPointer(to); err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpCopy, From: ptr(i), Path: to}), nil
}

// CharmTest implements charm.test: a guard asserting the anchor is still present
// at its index when the patch applies.
func CharmTest(_ context.Context, argv []string, anchor string) (spells.Charm, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpTest, Path: ptr(i), Value: anchor}), nil
}

// CharmTestFunc implements charm.test_func.
func CharmTestFunc(ctx context.Context, argv []string, fn Callback) (spells.Charm, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return spells.Charm{}, err
	}
	return charmResult(spells.PatchOp{Op: spells.OpTest, Path: ptr(i), Value: argv[i]}), nil
}

// callPredicate invokes a VM predicate on s and reports its truthiness.
// Truthiness follows the source language: any value other than nil/false is true.
func callPredicate(ctx context.Context, fn Callback, s string) (bool, error) {
	res, err := fn.Call(ctx, s)
	if err != nil {
		return false, err
	}
	if len(res) == 0 {
		return false, nil
	}
	switch v := res[0].(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	default:
		return true, nil
	}
}
