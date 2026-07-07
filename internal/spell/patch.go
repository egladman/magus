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

// ExplainCharms is ApplyCharms with its work shown: it returns one step per
// active declared charm, in the same sorted-name order ApplyCharms uses, each
// carrying the argv after that charm's patch applies on top of the prior step.
// The returned steps do NOT include the base; the caller pairs them with the
// unmodified argv. A charm named in activeNames but not declared in charms (or
// declaring no ops) contributes nothing and no step. An op that does not apply is
// an error, exactly as in ApplyCharms, naming the charm that failed.
func ExplainCharms(argv []string, charms map[string]types.Charm, activeNames []string) ([]types.CharmTraceStep, error) {
	on := make(map[string]bool, len(activeNames))
	for _, name := range activeNames {
		on[name] = true
	}
	names := make([]string, 0, len(charms))
	for name := range charms {
		if on[name] && len(charms[name].Ops) > 0 {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	var steps []types.CharmTraceStep
	cur := slices.Clone(argv)
	for _, name := range names {
		next, err := ApplyPatch(cur, charms[name].Ops)
		if err != nil {
			return nil, fmt.Errorf("charm %q: %w", name, err)
		}
		cur = next
		steps = append(steps, types.CharmTraceStep{Charm: name, Command: slices.Clone(cur)})
	}
	return steps, nil
}

// CharmConflict reports an active charm whose edit is overwritten by another active
// charm on the same command. Name changes the command on its own, yet the fully
// charm-applied command is identical whether or not Name is active, because
// OverriddenBy edits the same position and, applied later in sorted-name order, wins.
// The surviving value is decided by alphabetical charm name rather than any declared
// precedence, so a conflict is almost always an authoring mistake, not intent.
type CharmConflict struct {
	Name         string // the charm whose edit is lost
	OverriddenBy string // the active charm that overwrites it, or "" if none is singly responsible
}

// Conflicts returns the active charms whose effect is clobbered by another active
// charm on argv. A charm conflicts when it changes the command on its own but the
// command with the full active set equals the command with that charm removed -
// proof its edit left no trace. Disjoint edits (two appended flags both survive)
// never conflict; only a destructive overlap on the same position does. A charm that
// is a no-op on its own is not a conflict (that is the Before==After case describe
// surfaces separately). activeNames need not be sorted or deduped.
func Conflicts(argv []string, charms map[string]types.Charm, activeNames []string) ([]CharmConflict, error) {
	seen := map[string]bool{}
	var active []string
	for _, name := range activeNames {
		if _, ok := charms[name]; ok && !seen[name] {
			seen[name] = true
			active = append(active, name)
		}
	}
	if len(active) < 2 {
		return nil, nil // one charm cannot be overridden by another
	}

	canonical, err := ApplyCharms(argv, charms, active)
	if err != nil {
		return nil, err
	}

	var out []CharmConflict
	for _, name := range active {
		alone, err := ApplyCharms(argv, charms, []string{name})
		if err != nil {
			return nil, err
		}
		if slices.Equal(alone, argv) {
			continue // no-op in isolation; not a conflict
		}
		rest := make([]string, 0, len(active)-1)
		for _, n := range active {
			if n != name {
				rest = append(rest, n)
			}
		}
		without, err := ApplyCharms(argv, charms, rest)
		if err != nil {
			return nil, err
		}
		if slices.Equal(without, canonical) {
			out = append(out, CharmConflict{Name: name, OverriddenBy: overrider(argv, charms, name, active)})
		}
	}
	return out, nil
}

// overrider returns the active charm that overwrites name's edit: the pair {name,
// other} applied together yields the same command as other alone (name is lost),
// which name alone would have changed. Returns "" when no single charm accounts for
// the override. Scanned in sorted-name order for a stable answer.
func overrider(argv []string, charms map[string]types.Charm, name string, active []string) string {
	others := make([]string, 0, len(active)-1)
	for _, n := range active {
		if n != name {
			others = append(others, n)
		}
	}
	slices.Sort(others)
	for _, other := range others {
		pair, err := ApplyCharms(argv, charms, []string{name, other})
		if err != nil {
			continue
		}
		solo, err := ApplyCharms(argv, charms, []string{other})
		if err != nil {
			continue
		}
		if slices.Equal(pair, solo) {
			return other
		}
	}
	return ""
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
