package types

// Result is a value or the Diagnostic that stopped it, as it crosses a language
// boundary. The Buzz mirror is `result\Result::<T>` in libs/result; the
// TypeScript one is a union narrowed on `error`. Go code inside the module
// returns (T, error) and builds a Result only where the value leaves Go.
//
// Exactly one side is meaningful: Error nil means Value holds the answer, and Error
// non-nil means Value is T's zero and carries nothing.
type Result[T any] struct {
	Value T           `json:"value"`
	Error *Diagnostic `json:"error,omitempty" buzz:"error"`
}

// Ok is a successful Result holding v.
func Ok[T any](v T) Result[T] { return Result[T]{Value: v} }

// Fail is a failed Result carrying d, with Value left at T's zero.
func Fail[T any](d Diagnostic) Result[T] { return Result[T]{Error: &d} }

// IsOk reports whether r carries a value rather than a Diagnostic.
func (r Result[T]) IsOk() bool { return r.Error == nil }
