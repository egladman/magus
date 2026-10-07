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

// Counter has a pointer-receiver method that changes it.
type Counter struct {
	Name  string
	Count int
}

func (c *Counter) Bump() { c.Count++ }

func Fill(s *Span) {}

// SpanPtr and SpanRef reach a Span through an alias and a defined pointer type.
type (
	SpanPtr = *Span
	SpanRef *Span
)

func NewSpan() SpanPtr { return &Span{} }

func NewRef() SpanRef { return &Span{} }

func Grow(p SpanPtr) {}

func Stretch(p SpanRef) {}

type List struct {
	Names []string
	Size  int
}

type Index map[string]Span

func Put(idx Index) {}
