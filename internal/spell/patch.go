package spell

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/egladman/magus/types"
)

// ApplyPatch applies an RFC 6902 JSON Patch to argv, treating argv as a JSON
// array of strings. Ops run in order; each sees the result of the previous, per
// the spec's sequential semantics. The input is never mutated - a fresh slice is
// returned. An out-of-range index, a failed `test`, or a malformed pointer is an
// error naming the offending op.
//
// Only the flat-array slice of RFC 6902 is implemented, because the document is
// always argv: paths are single-token pointers ("/N" or, for add, "/-"), and
// values are strings. The op vocabulary is complete (add/remove/replace/move/
// copy/test); the test suite checks it against the RFC's own examples.
func ApplyPatch(argv []string, ops []types.PatchOp) ([]string, error) {
	out := slices.Clone(argv)
	for i, op := range ops {
		next, err := applyOp(out, op)
		if err != nil {
			return nil, fmt.Errorf("magus/spell: patch op %d (%s %s): %w", i, op.Op, op.Path, err)
		}
		out = next
	}
	return out, nil
}

// ApplyCharms reshapes argv by the active charms declared in charms: every charm named
// in activeNames contributes its ops, concatenated in sorted charm-name order and
// applied as one sequential RFC 6902 patch, so the result is deterministic and immune
// to activation order or duplicate names. A name not declared in charms (or absent from
// activeNames) contributes nothing. The result is always a fresh slice. This is the one
// place the "which charms, in what order, over which argv" rule lives; both the engine's
// command binding and the dry run route through it.
func ApplyCharms(argv []string, charms map[string]types.Charm, activeNames []string) ([]string, error) {
	if len(charms) == 0 || len(activeNames) == 0 {
		return slices.Clone(argv), nil
	}
	on := make(map[string]bool, len(activeNames))
	for _, name := range activeNames {
		on[name] = true
	}
	names := make([]string, 0, len(charms))
	for name := range charms {
		if on[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	var ops []types.PatchOp
	for _, name := range names {
		ops = append(ops, charms[name].Ops...)
	}
	if len(ops) == 0 {
		return slices.Clone(argv), nil
	}
	return ApplyPatch(argv, ops)
}

// applyOp applies a single op to argv (already a private copy ApplyPatch owns).
func applyOp(argv []string, op types.PatchOp) ([]string, error) {
	switch op.Op {
	case OpAdd:
		i, err := argvIndex(op.Path, len(argv), true)
		if err != nil {
			return nil, err
		}
		return slices.Insert(argv, i, op.Value), nil
	case OpRemove:
		i, err := argvIndex(op.Path, len(argv), false)
		if err != nil {
			return nil, err
		}
		return slices.Delete(argv, i, i+1), nil
	case OpReplace:
		i, err := argvIndex(op.Path, len(argv), false)
		if err != nil {
			return nil, err
		}
		argv[i] = op.Value
		return argv, nil
	case OpTest:
		i, err := argvIndex(op.Path, len(argv), false)
		if err != nil {
			return nil, err
		}
		if argv[i] != op.Value {
			return nil, fmt.Errorf("test failed: %q != %q", argv[i], op.Value)
		}
		return argv, nil
	case OpMove:
		from, err := argvIndex(op.From, len(argv), false)
		if err != nil {
			return nil, err
		}
		v := argv[from]
		argv = slices.Delete(argv, from, from+1)
		i, err := argvIndex(op.Path, len(argv), true)
		if err != nil {
			return nil, err
		}
		return slices.Insert(argv, i, v), nil
	case OpCopy:
		from, err := argvIndex(op.From, len(argv), false)
		if err != nil {
			return nil, err
		}
		v := argv[from]
		i, err := argvIndex(op.Path, len(argv), true)
		if err != nil {
			return nil, err
		}
		return slices.Insert(argv, i, v), nil
	default:
		return nil, fmt.Errorf("unknown JSON Patch op %q", op.Op)
	}
}

// argvIndex resolves a single-token JSON Pointer into an array index. "/-" yields
// length (the append position) when allowDash is set - its only legal use, per
// RFC 6901, is the add target. "/N" yields N, bounds-checked: add/move/copy
// targets accept [0,length] (length appends); remove/replace/test/from accept
// [0,length). Leading zeros and non-numeric tokens are rejected.
func argvIndex(path string, length int, allowDash bool) (int, error) {
	if len(path) < 2 || path[0] != '/' {
		return 0, fmt.Errorf("invalid pointer %q", path)
	}
	tok := path[1:]
	if tok == "-" {
		if !allowDash {
			return 0, fmt.Errorf("pointer %q (end-of-array) is only valid for add/move/copy targets", path)
		}
		return length, nil
	}
	if len(tok) > 1 && tok[0] == '0' {
		return 0, fmt.Errorf("invalid array index %q (leading zero)", tok)
	}
	n, err := strconv.Atoi(tok)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid array index %q", tok)
	}
	hi := length
	if !allowDash {
		hi = length - 1
	}
	if n > hi {
		return 0, fmt.Errorf("index %d out of range for argv length %d", n, length)
	}
	return n, nil
}
