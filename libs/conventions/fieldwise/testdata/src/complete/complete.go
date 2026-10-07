// Package complete declares small structs its tests assert on every field of.
package complete

type Span struct{ Start, End int }

type Entry struct {
	Key   string
	Value string
	Rev   int
}

type Wrapped struct {
	Span
	Label string
}
