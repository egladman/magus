// Package asides exercises the default rule: the aside is reported, and every
// shape that carries a spaced hyphen for another reason stays silent.
package asides

//go:generate echo a - b

// Resolve answers the question the caller asked - not the one it meant. // want `em-dash aside`
//
// A second paragraph with no aside stays silent.
func Resolve() {}

// Bullets holds the doc-list shapes gofmt formats, none of which is an aside:
//
//   - the first item
//   - the second item
//
// and the numbered form:
//
//  1. the first step
//  2. the second step
func Bullets() {}

// Loose reports the aside inside a list item while leaving the marker alone:
//
//   - the item body carries an aside - which is reported // want `em-dash aside`
//   - this one does not
func Loose() {}

// Wrapped keeps a list item's continuation line as prose:
//
//   - the item runs past the width of a line and wraps onto
//     a second one, where the aside still counts - like this // want `em-dash aside`
func Wrapped() {}

// Preformatted holds a command table, which godoc renders verbatim:
//
//	magus run build .   - compiles the binary
//	magus run test .    - runs the suite
//
// and prose resumes here.
func Preformatted() {}

// Literal keeps the spaced hyphen inside backticks, where it is an argument:
//
// The supported install is `tar -xf - -C <dir>`, nothing else.
func Literal() {}

// Link carries a URL, which holds no spaces and so cannot spell the aside:
//
// See https://example.com/a-b-c for the background.
func Link() {}

// Arithmetic exempts a hyphen with a digit on each side:
//
// The window is 3 - 5 seconds, and residual = 100 - 30 is subtraction.
func Arithmetic() {}

// Identifiers are NOT exempt, which is the documented cost of the digit rule:
//
// The last index is n - 1, which reads as prose from out here. // want `em-dash aside`
func Identifiers() {}

func Block() {
	/* A block comment is prose too - so this is reported. */ // want `em-dash aside`
	_ = 0
}

// Trailing carries the aside onto the next line, which the default rule leaves
// alone because the fix rewraps a paragraph -
// see the wrapped testdata package for the opt-in half.
func Trailing() {}

func Interior() {
	// A hand-written list inside a function body, which gofmt never reformats:
	// - the first item
	// - the second item

	// An aside here counts the same as one in a doc comment - reported. // want `em-dash aside`
	_ = 0
}
