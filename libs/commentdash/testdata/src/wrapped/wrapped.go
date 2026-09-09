// Package wrapped exercises the opt-in half: an aside whose second clause sits
// on the next line, which no line-oriented search can see.
package wrapped

// Carry states the shape the default rule leaves alone.
//
// want +1 `carrying an em-dash aside onto the next line`
// The lock is taken before the check -
// which is what makes the fast path safe.
func Carry() {}

// Once proves a line holding both shapes reports once, on the inline one.
//
// want +1 `em-dash aside`
// The inline aside wins - and this line also ends in -
// so the count stays one.
func Once() {}

// Compound keeps a trailing hyphen with no space before it, which is a broken
// word rather than an aside:
//
//   - an item whose text ends in a compound word-
func Compound() {}

// Preformatted keeps a code line ending in a hyphen, which is an argument:
//
//	magus run build . -
func Preformatted() {}
