package main

import (
	"context"
	"flag"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/pipe"
	"github.com/egladman/magus/types"
)

// provesUpstream reports whether a magus invoked with argv (argv[0] first) proves the
// magus stages upstream of it at start, as proveStdio does.
func provesUpstream(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	args := argv[1:]
	sub, subArgs := peekSub(args)
	locks := (sub == "run" || sub == "affected") && takesProjectLocks(argv)
	return locks || tradesRecords(args) || handlesPlan(sub, subArgs)
}

// readsPipeRecords reports whether a magus invoked with args reads the records a record
// stage writing its stdin writes. Only run and buzz do: affected takes its input from
// the VCS or, with --stdin, as paths.
func readsPipeRecords(args []string) bool {
	sub, _ := peekSub(args)
	return (sub == "run" || sub == "buzz") && tradesRecords(args)
}

// pipeRecordStage reports whether a magus invoked with args (argv without argv[0])
// trades records with the magus stages beside it: when a magus reads its stdout it
// writes the -o jsonl records there and its prose on stderr, and it reads the records a
// record stage upstream writes. Both ends of a pipe ask it, the writer of its own args
// and the reader of the argv the kernel reports for its upstream, so they agree on what
// the pipe carries.
//
// It covers the stages that run targets or a script. An explicit -o keeps the format
// asked for, and a verb whose stdout is its product (a plan, a list, a graph) keeps it.
func pipeRecordStage(args []string) bool {
	return !outputFlagGiven(args) && tradesRecords(args)
}

// tradesRecords is pipeRecordStage without the -o check, which decides only what a
// stage writes: a stage with -o still reads the records written to it.
func tradesRecords(args []string) bool {
	sub, subArgs := peekSub(args)
	own, _ := commands.SplitOnDashDash(subArgs)
	switch sub {
	case "run":
		// A run reading a saved plan takes stdin as its input, like a buzz script read from
		// stdin, not as records.
		if commands.IsUsageOnlyInvocation(subArgs) || commands.HasDetachFlag(subArgs) || hasModeFlag(own, "graph") || runReadsPlan(subArgs) {
			return false
		}
		raw, _, ok := splitTargetFromArgs(own, func(fs *flag.FlagSet) { bindRunFlags(fs, nil) })
		if !ok {
			return false
		}
		_, name := parseTarget(raw)
		t, err := types.ParseTarget(name)
		return err == nil && canonicalTarget(t.Name) != "ls"
	case "affected":
		if commands.IsUsageOnlyInvocation(subArgs) || commands.HasDetachFlag(subArgs) || isForensicAffected(subArgs) || hasModeFlag(own, "bisect") {
			return false
		}
		target, _, ok := splitTargetFromArgs(own, nil)
		return ok && target != "ls"
	case "buzz":
		return buzzScriptStage(subArgs)
	}
	return false
}

// buzzScriptStage reports whether `magus buzz` runs a script named by a file or -e.
// A script read from stdin is not a stage: stdin is its source.
func buzzScriptStage(args []string) bool {
	own, _, _ := interp.SplitScriptArgs(args)
	if _, rest, ok := cutBuzzWorkDir(own); ok {
		own = rest
	}
	if len(own) > 0 && own[0] == "lsp" {
		return false
	}
	for _, mode := range []string{flaggen.FlagBuzzT, "test", flaggen.FlagBuzzCheck} {
		if hasModeFlag(own, mode) {
			return false
		}
	}
	if hasModeFlag(own, flaggen.FlagBuzzE) {
		return true
	}
	file, _, ok := splitTargetFromArgs(own, func(fs *flag.FlagSet) { flaggen.BindBuzz(fs) })
	return ok && file != "-"
}

// outputFlagGiven reports whether args set -o in any spelling, before a "--".
func outputFlagGiven(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, name := range []string{"o", "output"} {
			if commands.IsFlagNamed(a, name) || commands.ParseFlagValue(a, name) != "" {
				return true
			}
		}
	}
	return false
}

// processStdioContext hands the runs on ctx this process's standard streams unless the
// run is adopted. See pipe.ProcessStdioContext.
func processStdioContext(ctx context.Context) context.Context {
	_, adopted := magusFromContext(ctx)
	return pipe.ProcessStdioContext(ctx, adopted, takesProjectLocks)
}

// handlesPlan reports whether a magus invoked with sub and subArgs prints a shard plan
// (affected --plan) or reads one (run --stdin).
func handlesPlan(sub string, subArgs []string) bool {
	switch sub {
	case "affected":
		return hasModeFlag(subArgs, "plan")
	case "run":
		return runReadsPlan(subArgs)
	}
	return false
}

// awaitPipeline holds a stage that prints or reads a shard plan until every magus stage
// proven upstream of it has ended. See pipe.AwaitProcess.
func awaitPipeline(ctx context.Context, root string) error {
	_, adopted := magusFromContext(ctx)
	return pipe.AwaitProcess(ctx, root, adopted, globalCfg)
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
	case "affected", "run":
		return resolveProfile(sub, subArgs).SpawnsWork
	case "clean", "x", "graph", "refs":
		return true
	}
	return false
}
