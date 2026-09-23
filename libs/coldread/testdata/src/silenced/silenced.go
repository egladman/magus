// Package silenced trips every check; the test disables them all.
package silenced

// Client is a Client.
type Client struct{}

func body() {
	count := 0

	// increment count
	count++

	// Step 1: this used to run twice - once per caller.
	count++

	// x := count
	_ = count
}
