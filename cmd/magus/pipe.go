package main

import (
	"context"
	"os"
	"syscall"

	"github.com/egladman/magus"
)

type processStdioKey struct{}

// proveStdio starts proving the magus stages upstream of this process, for the verbs
// that run targets, and carries the result on ctx. It runs at process start because a
// stage that fails fast is gone from the kernel before the run reaches its locks.
func proveStdio(ctx context.Context, args []string) context.Context {
	argv := append([]string{os.Args[0]}, args...)
	if sub, _ := peekSub(args); (sub != "run" && sub != "affected") || !takesProjectLocks(argv) {
		return ctx
	}
	s := &magus.ProcessStdio{Stdin: os.Stdin, Stdout: os.Stdout, TakesLocks: takesProjectLocks}
	s.ProveUpstream(ctx)
	return context.WithValue(ctx, processStdioKey{}, s)
}

// processStdioOption hands a run this process's standard streams, so it can defer to a
// magus upstream of it in a shell pipe (see magus.ProcessStdio). An adopted run gets
// none: it executes inside the server, whose stdin belongs to nobody's pipe.
func processStdioOption(ctx context.Context) []magus.RunOption {
	if _, adopted := magusFromContext(ctx); adopted {
		return nil
	}
	if s, ok := ctx.Value(processStdioKey{}).(*magus.ProcessStdio); ok {
		return []magus.RunOption{magus.WithProcessStdio(*s)}
	}
	return []magus.RunOption{magus.WithProcessStdio(magus.ProcessStdio{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		TakesLocks: takesProjectLocks,
	})}
}

// settlePipeline is the pipefail every magus pipeline gets: a stage that succeeded
// reports the first proven upstream magus stage that failed (MGS3030).
func settlePipeline(ctx context.Context, args []string) error {
	s, ok := ctx.Value(processStdioKey{}).(*magus.ProcessStdio)
	if !ok {
		return nil
	}
	root, err := magus.FindRoot(extractRootFlag(args))
	if err != nil {
		return nil //nolint:nilerr // no workspace, so no stage left a record to read
	}
	return s.SettlePipeline(ctx, root, globalCfg)
}

// recordPipeExit leaves code for the magus stage reading this process's stdout. See
// magus.RecordPipeExit.
func recordPipeExit(args []string, code int, interrupted func() (syscall.Signal, bool)) {
	root, err := magus.FindRoot(extractRootFlag(args))
	if err != nil {
		return
	}
	signal := ""
	if sig, ok := interrupted(); ok {
		signal = sig.String()
	}
	magus.RecordPipeExit(root, globalCfg, code, signal)
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
		// shape `affected ci --plan --preflight generate | magus run ci-shard` exists for.
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
