package hint

import (
	"os"
	"path/filepath"
	"slices"
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
func init() { ResolveBinaryNameFrom(os.Args[0]) }

// ResolveBinaryNameFrom derives the spelling from how this process was invoked. It
// assigns nothing when it cannot: exported for the tests that pin the rule, and
// ordinary callers get it from init above.
//
// A binary whose base name is not magus keeps the default. That is what holds every
// existing rendering steady under `go test` (the test binary is magus.test) and in the
// codegen tools that render these same commands into docs, where the reader's own
// invocation is not this process's at all.
func ResolveBinaryNameFrom(argv0 string) {
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
		// A hook template resolves the workspace's binary absolutely and runs with the
		// workspace as its working directory, so that one is the `./magus` a reader
		// would type there; an absolute path from anywhere else is an installed magus
		// on PATH, and spelling it in full costs more context than it buys.
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

// OnPath respells next as the PATH invocation, for a surface whose reader is not this
// process.
//
// The daemon resolves its own argv0 once at startup, so a server started as
// `./magus server start` would otherwise render `./magus ...` into every MCP reply,
// which resolves against the CLIENT's working directory rather than the daemon's.
func OnPath(next []Next) []Next {
	if BinaryName() == defaultBinaryName {
		return next
	}
	out := make([]Next, 0, len(next))
	for _, n := range next {
		n.Run = defaultBinaryName + strings.TrimPrefix(n.Run, BinaryName())
		if len(n.Argv) > 0 {
			n.Argv = slices.Concat([]string{defaultBinaryName}, n.Argv[1:])
		}
		out = append(out, n)
	}
	return out
}
