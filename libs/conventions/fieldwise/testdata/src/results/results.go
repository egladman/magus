// Package results declares the values its tests assert on.
package results

import "io"

type Meta struct {
	Kind string
	Rev  int
}

type Result struct {
	Name  string
	Count int
	Total int
	Tags  []string
	Meta  Meta
	Err   error
	Items []Result
}

func Make() Result { return Result{} }

func MakeAll() []Result { return nil }

// Counter has pointer-receiver methods that change it between assertions.
type Counter struct {
	Name  string
	Count int
}

func (c *Counter) Bump() { c.Count++ }

func (c Counter) Label() string { return c.Name }

// Handle carries a func, which no deep comparison matches.
type Handle struct {
	Name  string
	Close func()
}

// Conn carries a live reader.
type Conn struct {
	Name string
	R    io.Reader
}

// Inner has an unexported field: its own package's tests can build it, an
// external test package cannot.
type Inner struct {
	Name  string
	Other int
	count int
}

func MakeInner() Inner { return Inner{count: 1} }

func Fill(r *Result) {}

func Read(r Result) {}

type Box[T any] struct {
	Value T
	Size  int
}
