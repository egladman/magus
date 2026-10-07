// Package opaque declares a struct a test in another package cannot build.
package opaque

type Token struct {
	ID     string
	Rev    int
	secret string
}

func New() Token { return Token{secret: "s"} }
