// Package commentedcode holds commented-out Go, and the prose and examples that
// only look like it.
package commentedcode

import "fmt"

func body() error {
	// want +2 `commentedcode: comment holds commented-out Go`

	// x := compute()
	x := 1

	// want +2 `commentedcode: comment holds commented-out Go`

	// if err := check(x); err != nil {
	// 	return err
	// }
	_ = x

	// want +2 `commentedcode: comment holds commented-out Go`

	// fmt.Println(x)
	fmt.Print()

	// Retry (bounded) until the lock frees.
	_ = x

	_ = x // x != 0 on every path here
	_ = x // CUP: row;col

	// Holds when a == b for every key.
	_ = x

	// See fmt.Println for the format.
	return nil
}

// want +2 `commentedcode: comment holds commented-out Go`

// func old() { return }

// Render writes the page. A caller wires it like this:
//
//	r := Render(w)
//	if err := r.Flush(); err != nil {
//		return err
//	}
func Render() {}

func ExampleRender() {
	fmt.Println("a := b")
	// Output: a := b
}
