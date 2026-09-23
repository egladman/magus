// Package history holds comments that narrate a change, and the phrases that
// sound like one but describe the code as it stands.
package history

// Load reads the manifest. It used to read the lock file too. // want `history: comment narrates a change \("used to"\)`
func Load() {}

// Parse returns an error for an empty input; previously it returned nil. // want `history: .*"previously"`
func Parse() {}

func body() {
	// This now correctly rejects a nil key. // want `history: .*"now correctly"`
	_ = 0

	// Keeps the old behavior for callers that pass a path. // want `history: .*"the old behavior"`
	_ = 0
}

// Sign takes the key that is used to sign every envelope, which is what a
// verifier checks.
func Sign() {}

// SignBare pins the known false positive: a purpose after a bare noun reads the
// same as a habit, so the key used to sign is reported. // want `history: .*"used to"`
func SignBare() {}

// Walk reads the directory now, before the caller can change it, instead of
// deferring the read. The rejected alternative is what `used to` would say.
func Walk() {}

// Example keeps its command table verbatim, whatever it says:
//
//	tool --previously-set  rerun with the old flag
func Example() {}
