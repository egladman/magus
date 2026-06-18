package std

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	ispell "github.com/egladman/magus/internal/spell"
)

//go:generate go run ../cmd/magus-bindings-gen -module charm -lang buzz -out ../hostbuzz/gen/charm.go

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
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmAppend,
		},
		{
			Name:    "prepend",
			Doc:     "Insert vals at the front of the argv, in order.",
			Args:    []Arg{{Name: "vals", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmPrepend,
		},
		{
			Name:    "after",
			Doc:     "Insert vals immediately after the first argv element equal to anchor.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "vals", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmAfter,
		},
		{
			Name:    "before",
			Doc:     "Insert vals immediately before the first argv element equal to anchor.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "vals", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmBefore,
		},
		{
			Name:    "set",
			Doc:     "Replace the first argv element equal to anchor with val.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "val", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmSet,
		},
		{
			// "drop", not "remove": the Buzz surface exposes this as a map member
			// (charm.drop), and "remove" would be shadowed by the built-in map
			// .remove() method, so a charm.remove call could never reach this.
			Name:    "drop",
			Doc:     "Drop (remove) the first argv element equal to anchor.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmDrop,
		},
		{
			Name:    "after_func",
			Doc:     "Insert vals after the first argv element for which fn(s) is truthy.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "vals", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmAfterFunc,
		},
		{
			Name:    "before_func",
			Doc:     "Insert vals before the first argv element for which fn(s) is truthy.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "vals", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmBeforeFunc,
		},
		{
			Name:    "set_func",
			Doc:     "Replace the first argv element for which fn(s) is truthy with val.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "val", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmSetFunc,
		},
		{
			Name:    "drop_func",
			Doc:     "Drop (remove) the first argv element for which fn(s) is truthy.",
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmDropFunc,
		},
		{
			Name:    "path",
			Doc:     `Return the JSON Pointer ("/N") of the first argv element equal to anchor — the index, auto-calculated, for hand-built move/copy/test ops.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}}, Impl: CharmPath,
		},
		{
			Name:    "path_func",
			Doc:     `Return the JSON Pointer ("/N") of the first argv element for which fn(s) is truthy.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}},
			Returns: []Ret{{Type: TypeString}}, Impl: CharmPathFunc,
		},
		{
			Name:    "move",
			Doc:     `Move the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "to", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmMove,
		},
		{
			Name:    "move_func",
			Doc:     `Move the first argv element for which fn(s) is truthy to the JSON Pointer to.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "to", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmMoveFunc,
		},
		{
			Name:    "copy",
			Doc:     `Copy the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}, {Name: "to", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmCopy,
		},
		{
			Name:    "copy_func",
			Doc:     `Copy the first argv element for which fn(s) is truthy to the JSON Pointer to.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}, {Name: "to", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmCopyFunc,
		},
		{
			Name:    "test",
			Doc:     `Guard: assert the first argv element equal to anchor is still at its position when the patch applies (else the run errors).`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "anchor", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmTest,
		},
		{
			Name:    "test_func",
			Doc:     `Guard: assert the first argv element for which fn(s) is truthy is still at its position when the patch applies.`,
			Args:    []Arg{{Name: "argv", Type: TypeStringSlice}, {Name: "fn", Type: TypeFunc}},
			Returns: []Ret{{Type: TypeAnyMap}}, Impl: CharmTestFunc,
		},
	},
}

// charmResult wraps the ops as the { ops = [...] } record Decode reads. The list
// is []any of map[string]any so the AnyMap marshallers (which recurse over those
// two types) carry it across the VM boundary unchanged.
func charmResult(ops ...map[string]any) map[string]any {
	arr := make([]any, len(ops))
	for i := range ops {
		arr[i] = ops[i]
	}
	return map[string]any{"ops": arr}
}

// ptr renders a JSON Pointer to argv index i.
func ptr(i int) string { return "/" + strconv.Itoa(i) }

// addOps builds a run of `add` ops that insert vals starting at index start, so
// the values land in order (each subsequent insert sits one past the previous).
func addOps(start int, vals []string) map[string]any {
	ops := make([]map[string]any, len(vals))
	for k, v := range vals {
		ops[k] = map[string]any{"op": ispell.OpAdd, "path": ptr(start + k), "value": v}
	}
	return charmResult(ops...)
}

// anchorIndex returns the position of the first argv element equal to anchor, or
// an error — a not-found anchor is a spell bug, surfaced now (author/load time)
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
func CharmAppend(_ context.Context, vals []string) (map[string]any, error) {
	ops := make([]map[string]any, len(vals))
	for i, v := range vals {
		ops[i] = map[string]any{"op": ispell.OpAdd, "path": "/-", "value": v}
	}
	return charmResult(ops...), nil
}

// CharmPrepend implements charm.prepend.
func CharmPrepend(_ context.Context, vals []string) (map[string]any, error) {
	return addOps(0, vals), nil
}

// CharmAfter implements charm.after.
func CharmAfter(_ context.Context, argv []string, anchor string, vals []string) (map[string]any, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return nil, err
	}
	return addOps(i+1, vals), nil
}

// CharmBefore implements charm.before.
func CharmBefore(_ context.Context, argv []string, anchor string, vals []string) (map[string]any, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return nil, err
	}
	return addOps(i, vals), nil
}

// CharmSet implements charm.set.
func CharmSet(_ context.Context, argv []string, anchor, val string) (map[string]any, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpReplace, "path": ptr(i), "value": val}), nil
}

// CharmDrop implements charm.drop.
func CharmDrop(_ context.Context, argv []string, anchor string) (map[string]any, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpRemove, "path": ptr(i)}), nil
}

// CharmAfterFunc implements charm.after_func.
func CharmAfterFunc(ctx context.Context, argv []string, fn Callback, vals []string) (map[string]any, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return nil, err
	}
	return addOps(i+1, vals), nil
}

// CharmBeforeFunc implements charm.before_func.
func CharmBeforeFunc(ctx context.Context, argv []string, fn Callback, vals []string) (map[string]any, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return nil, err
	}
	return addOps(i, vals), nil
}

// CharmSetFunc implements charm.set_func.
func CharmSetFunc(ctx context.Context, argv []string, fn Callback, val string) (map[string]any, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpReplace, "path": ptr(i), "value": val}), nil
}

// CharmDropFunc implements charm.drop_func.
func CharmDropFunc(ctx context.Context, argv []string, fn Callback) (map[string]any, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpRemove, "path": ptr(i)}), nil
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
func CharmMove(_ context.Context, argv []string, anchor, to string) (map[string]any, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return nil, err
	}
	if err := destPointer(to); err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpMove, "from": ptr(i), "path": to}), nil
}

// CharmMoveFunc implements charm.move_func.
func CharmMoveFunc(ctx context.Context, argv []string, fn Callback, to string) (map[string]any, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return nil, err
	}
	if err := destPointer(to); err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpMove, "from": ptr(i), "path": to}), nil
}

// CharmCopy implements charm.copy.
func CharmCopy(_ context.Context, argv []string, anchor, to string) (map[string]any, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return nil, err
	}
	if err := destPointer(to); err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpCopy, "from": ptr(i), "path": to}), nil
}

// CharmCopyFunc implements charm.copy_func.
func CharmCopyFunc(ctx context.Context, argv []string, fn Callback, to string) (map[string]any, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return nil, err
	}
	if err := destPointer(to); err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpCopy, "from": ptr(i), "path": to}), nil
}

// CharmTest implements charm.test: a guard asserting the anchor is still present
// at its index when the patch applies.
func CharmTest(_ context.Context, argv []string, anchor string) (map[string]any, error) {
	i, err := anchorIndex(argv, anchor)
	if err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpTest, "path": ptr(i), "value": anchor}), nil
}

// CharmTestFunc implements charm.test_func.
func CharmTestFunc(ctx context.Context, argv []string, fn Callback) (map[string]any, error) {
	i, err := anchorIndexFunc(ctx, argv, fn)
	if err != nil {
		return nil, err
	}
	return charmResult(map[string]any{"op": ispell.OpTest, "path": ptr(i), "value": argv[i]}), nil
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
