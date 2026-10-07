// Package geo declares a struct with only exported fields, which a test in
// another package can build whole.
package geo

type Point struct{ X, Y int }

func Origin() *Point { return &Point{} }
