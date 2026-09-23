// Package enum checks closed sets of named string values. Go has no enum type, so a
// defined string accepts any string; a Set is the case list the boundary checks
// against. Each package declares its sets by hand beside the types they close.
package enum

import "slices"

// Set is the declared cases of one named string type, excluding the zero value, which
// means unset and is always valid.
type Set[T ~string] []T

// Valid reports whether v is one of s's cases or unset.
func (s Set[T]) Valid(v T) bool { return v == "" || slices.Contains(s, v) }

// Strings lists s for an error message, as a fresh slice the caller may keep.
func (s Set[T]) Strings() []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = string(v)
	}
	return out
}

// String renders v for an error message: the value, or "unset" when empty.
func String[T ~string](v T) string {
	if v == "" {
		return "unset"
	}
	return string(v)
}
