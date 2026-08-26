package bindings

import "fmt"

// The decoders a spell contract reads a RECORD answer with.
//
// A spell returns a dynamically typed map, so something has to turn `any` into a Go field, and
// the posture that turn takes is a decision rather than a detail. It is made once here so the
// contracts that decode records - workspace and review - make it the same way, rather than each
// growing its own coercion helpers.
//
// The other two contracts do NOT belong here, and moving them in would be a regression rather
// than consistency. The secret contract returns ONE value, refuses an empty or wrong-shaped
// answer with its own message, and carries a compat path for providers that predate its typed
// return. The CI contract's quote_prefixes is a bare list of cosmetic output prefixes, where
// dropping a malformed one is the deliberate behavior. Field-by-field decoding answers neither
// question.
//
// The posture is: a field of the wrong type NAMES ITSELF. Not a panic, because a spell is user
// code and a bug in it must not take magus down; and emphatically not a zero value, because a
// silent zero is a wrong answer wearing a right answer's clothes. A mistyped project path
// becomes a silently wrong cache key. A mistyped review id becomes "no pull request for this
// branch", which sends the reader to look at their branch when the fault is in their spell.
//
// Absent and null read as the ZERO VALUE, not as an error. A Buzz object carries every declared
// field, so "declared nothing" and "did not declare" are the same statement and must decode to
// the same record - otherwise two spellings of one answer produce two different results.
//
// `where` is the caller's description of what is being decoded, e.g. `spell "github-review":
// open_review`. It is prepended to every message, because "field \"number\" is string, want
// int" without it names a field in a file the reader has not opened.

// strField reads an optional string field.
func strField(m map[string]any, key, where string) (string, error) {
	v, present := m[key]
	if !present || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: field %q is %T, want str", where, key, v)
	}
	return s, nil
}

// intField reads an optional integer field.
//
// float64 is accepted alongside int and is not a leniency: a Buzz integer that has crossed a
// JSON boundary arrives as one, so refusing it would reject the ordinary case. A float with a
// fractional part is refused, because a spell that answered 4.5 for a pull-request number meant
// something this cannot guess.
func intField(m map[string]any, key, where string) (int, error) {
	v, present := m[key]
	if !present || v == nil {
		return 0, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("%s: field %q is %v, want a whole number", where, key, n)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf("%s: field %q is %T, want int", where, key, v)
}

// strListField reads an optional [str] field, rejecting a non-string element rather
// than dropping it: a dropped dependency or source glob is a silently wrong cache
// key, which is the failure this whole decoder exists to prevent.
func strListField(m map[string]any, key, where string) ([]string, error) {
	v, present := m[key]
	if !present || v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: field %q is %T, want [str]", where, key, v)
	}
	if len(items) == 0 {
		// nil, not an empty slice: a Buzz object always carries the field, so "declared
		// nothing" and "did not declare" must decode to the same record - otherwise two
		// spellings of the same answer produce different cache entries.
		return nil, nil
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("%s: field %q[%d] is %T, want str", where, key, i, it)
		}
		out = append(out, s)
	}
	return out, nil
}
