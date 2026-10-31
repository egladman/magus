// The standard library exempts this name; here it pairs with example.go or not at all.
package pairing // want `example_test.go has no source file of the same name; move its tests into the _test.go of the file they exercise, or move the code it owns into example.go`

func ExampleResolve() { _ = Resolve("a") }
