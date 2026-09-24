package main

import (
	"context"
	"os"

	"github.com/egladman/magus"
)

// processStdioOption hands a run this process's standard streams, so it can defer to a
// magus upstream of it in a shell pipe (see magus.ProcessStdio). An adopted run gets
// none: it executes inside the daemon, whose stdin belongs to nobody's pipe.
func processStdioOption(ctx context.Context) []magus.RunOption {
	if _, adopted := magusFromContext(ctx); adopted {
		return nil
	}
	return []magus.RunOption{magus.WithProcessStdio(magus.ProcessStdio{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		TakesLocks: takesProjectLocks,
	})}
}

// takesProjectLocks reports whether a magus invoked with argv may take project locks,
// read with the same dispatch this binary applies to its own arguments. A verb it
// answers false for never holds back the stage downstream of it, so a read-only
// producer (status --watch, query, ls) streams to a magus consumer at once. A locking
// verb missing here degrades to the ordinary refusal, never to a hang.
func takesProjectLocks(argv []string) bool {
	if len(argv) == 0 {
		return true
	}
	sub, subArgs := peekSub(argv[1:])
	switch sub {
	case "affected":
		// --plan runs nothing, except the --preflight pass it gates the plan on: the
		// shape `affected ci --plan --preflight generate | magus run --stdin` exists for.
		if hasModeFlag(subArgs, "preflight") {
			return true
		}
		return resolveProfile(sub, subArgs).spawnsWork
	case "run":
		return resolveProfile(sub, subArgs).spawnsWork
	case "clean", "x", "graph", "refs":
		return true
	}
	return false
}
