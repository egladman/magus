// Package steps holds step narration inside function bodies, and the numbered
// shapes that are lists rather than a sequence.
package steps

func named() {
	// Step 1: load the manifest. // want `steps: comment narrates a numbered step`
	a := 1

	// step 2 resolve the graph // want `steps: comment narrates a numbered step`
	_ = a
}

func sequenced() {
	// 1. read the lock file // want `steps: comment narrates a numbered step`
	a := 1

	// 2) write the new one // want `steps: comment narrates a numbered step`
	_ = a
}

func single() {
	// 1. is the only rule this function needs to hold.
	_ = 0
}

func table() []string {
	return []string{
		// 1. Cold. Real work, so the band is drawn against a busy run.
		"cold",

		// 2. Warm. The same command, served from the cache.
		"warm",
	}
}

func list() {
	// Two reasons the order matters:
	// 1. the lock is taken first
	// 2. the write sees the lock
	_ = 0
}

// Resolve documents its algorithm in steps, which is contract, not narration:
//
//  1. load the manifest
//  2. resolve the graph
//
// Step 3 is the caller's.
func Resolve() {}
