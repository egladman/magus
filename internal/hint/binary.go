package hint

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// How a rendered command spells the binary.
//
// A hint is copy-pasted, so it has to name the binary the reader is already running.
// Measured 2026-09-11: three hints copied out of a worktree's verdicts ran against a
// v0.3.0 build in ~/.local/bin, because every hint said `magus` while the session had
// been using `./magus` throughout. The stale binary answered, and nothing in either
// output said the two were different programs.

// defaultBinaryName is what a hint spells when nothing better is known: the PATH
// invocation, which is right for an installed magus and is the spelling every
// existing test and doc page carries.
const defaultBinaryName = "magus"

// invokedAs holds the resolved spelling. Atomic because a hint renders from whatever
// goroutine is printing, and the tests that pin this rule write it.
var invokedAs atomic.Value // string

// init resolves the spelling from this process's own argv, and it lives here rather
// than in the CLI's main for an ordering reason: the guard's denial and advisory texts
// are package-level vars in cmd/magus, so they render during that package's variable
// initialization, which runs before any function main could call. A setter called from
// main would come too late for exactly the texts this rule exists for.
func init() { SetBinaryName(os.Args[0]) }

// SetBinaryName teaches the renderer how this process was invoked. Exported for the
// tests that pin the rule; ordinary callers get it from init above.
//
// A binary whose base name is not magus keeps the default. That is what holds every
// existing rendering steady under `go test` (the test binary is magus.test) and in the
// codegen tools that render these same commands into docs, where the reader's own
// invocation is not this process's at all.
func SetBinaryName(argv0 string) {
	argv0 = strings.TrimSpace(argv0)
	base := filepath.Base(argv0)
	if base != defaultBinaryName {
		return
	}
	switch {
	case !strings.ContainsRune(argv0, filepath.Separator):
		// Resolved through PATH, so PATH is what a reader would type.
		invokedAs.Store(defaultBinaryName)
	case filepath.IsAbs(argv0):
		// A hook template resolves the workspace's binary absolutely, and that is the
		// case this whole rule exists for: the hook runs with the workspace as its
		// working directory, so the binary it resolved is the `./magus` a reader would
		// type there. An absolute path from ANYWHERE ELSE falls back to the bare name
		// rather than rendering itself, because a full path in every hint costs more
		// context than it buys and a magus installed outside the tree is on PATH.
		if wd, err := os.Getwd(); err == nil && wd == filepath.Dir(argv0) {
			invokedAs.Store("." + string(filepath.Separator) + base)
			return
		}
		invokedAs.Store(defaultBinaryName)
	default:
		// Relative and spelled with a separator: exactly as typed, which is the
		// spelling the reader already used.
		invokedAs.Store(argv0)
	}
}

// BinaryName is the spelling rendered commands lead with.
func BinaryName() string {
	if s, ok := invokedAs.Load().(string); ok && s != "" {
		return s
	}
	return defaultBinaryName
}
