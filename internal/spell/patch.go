package spell

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/egladman/magus/types"
)

// ApplyPatch applies an RFC 6902 JSON Patch to argv, treating argv as a JSON
// array of strings. Ops run in order; each sees the result of the previous, per
// the spec's sequential semantics. The input is never mutated — a fresh slice is
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
		return nil, fmt.Errorf("unknown op")
	}
}

// argvIndex resolves a single-token JSON Pointer into an array index. "/-" yields
// length (the append position) when allowDash is set — its only legal use, per
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
