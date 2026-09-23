// Package restate holds one-line comments that repeat the next line, and the
// near misses that say one thing the line does not.
package restate

import "os"

type config struct {
	// want +2 `restate: comment only restates`

	// timeout
	timeout int

	// Retries past this many surface the last error to the caller.
	retries int
}

func counter() int {
	count := 0

	// want +2 `restate: comment only restates`

	// increment the count
	count++

	// want +2 `restate: comment only restates`

	// Open the config.
	f, err := os.Open("config.yaml")
	if err != nil {
		return 0
	}

	// want +2 `restate: comment only restates`

	// close f
	defer f.Close()

	// A second close is harmless; the deferred one covers the error path.
	count += 2

	// Fresh count per call keeps runs independent.
	count = 0

	// return
	return count
}

func trailing() int {
	x := 1 // x
	// x

	return x
}

func labels() []string {
	return []string{
		// reads
		"read one",
		"read two",
	}
}

// A declaration doc is docstub's to judge, even over a spec naming several
// values, where docstub has no one name to compare.

// lo and hi
var lo, hi = 0, 1

func long() int {
	// count count count count count count
	count := 0

	return count
}
