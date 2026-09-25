package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

type (
	processStdioKey struct{}
	pipeStageKey    struct{}
)

// proveStdio starts proving the magus stages on either side of this process in a shell
// pipe, and carries the results on ctx. It runs at process start because a stage that
// fails fast is gone from the kernel before the run reaches its locks.
//
// The verbs that run targets prove every stage upstream, for their locks and for the
// pipeline's exit status. A stage that trades records (see pipeRecordStage) proves the
// stage it writes to and the one writing to it.
func proveStdio(ctx context.Context, args []string) context.Context {
	ctx = context.WithValue(ctx, pipeStageKey{}, startPipeStage(ctx, args))
	argv := append([]string{os.Args[0]}, args...)
	sub, _ := peekSub(args)
	locks := (sub == "run" || sub == "affected") && takesProjectLocks(argv)
	if !locks && !tradesRecords(args) {
		return ctx
	}
	s := &magus.ProcessStdio{Stdin: os.Stdin, Stdout: os.Stdout, TakesLocks: takesProjectLocks}
	// Only run and buzz read records: affected takes its input from the VCS or, with
	// --stdin, as paths.
	if (sub == "run" || sub == "buzz") && tradesRecords(args) {
		s.WritesRecords = writesPipeRecords
	}
	s.ProveUpstream(ctx)
	return context.WithValue(ctx, processStdioKey{}, s)
}

// writesPipeRecords is pipeRecordStage over a full argv, as the kernel reports one.
func writesPipeRecords(argv []string) bool { return len(argv) > 0 && pipeRecordStage(argv[1:]) }

// pipeStage is what this process proved about the magus stage reading its stdout. The
// proof starts at process start and is awaited only where it is needed.
type pipeStage struct {
	done chan struct{}
	// records is set when stdout carries records: this process is a record stage and a
	// magus reads its stdout. proved is when the reader was proven.
	records bool
	proved  time.Time
}

// startPipeStage starts proving who reads stdout. A process that is no record stage
// writes prose whoever reads it.
func startPipeStage(ctx context.Context, args []string) *pipeStage {
	s := &pipeStage{done: make(chan struct{})}
	if !pipeRecordStage(args) || !isPipeFile(os.Stdout) {
		close(s.done)
		return s
	}
	go func() {
		defer close(s.done)
		if magus.ReadByMagus(ctx, os.Stdout) {
			s.records, s.proved = true, time.Now()
		}
	}()
	return s
}

func pipeStageOf(ctx context.Context) *pipeStage {
	s, _ := ctx.Value(pipeStageKey{}).(*pipeStage)
	return s
}

// writesRecords reports whether this process writes its records to stdout, and its
// prose to stderr.
func (s *pipeStage) writesRecords() bool {
	if s == nil {
		return false
	}
	<-s.done
	return s.records
}

// pipeRecordsIn returns the record stream of the record stage writing stdin, and its
// pid; nil when none does.
func pipeRecordsIn(ctx context.Context) (*report.Reader, int) {
	s, _ := ctx.Value(processStdioKey{}).(*magus.ProcessStdio)
	return s.Records()
}

// pipeReaderGrace is how long a record stage outlives the proof of its reader. The
// reader proves its upstream from the kernel, which it cannot do once the upstream has
// exited, and it does so within milliseconds of starting.
const pipeReaderGrace = 250 * time.Millisecond

// linger holds a record stage back from exiting until its reader has had the time to
// prove it. A stage that ran longer than that pays nothing.
func (s *pipeStage) linger() {
	if !s.writesRecords() {
		return
	}
	time.Sleep(time.Until(s.proved.Add(pipeReaderGrace)))
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
	own, _ := splitOnDashDash(subArgs)
	switch sub {
	case "run":
		if isUsageOnlyInvocation(subArgs) || hasDetachFlag(subArgs) || hasModeFlag(own, "graph") {
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
		if isUsageOnlyInvocation(subArgs) || hasDetachFlag(subArgs) || isForensicAffected(subArgs) || hasModeFlag(own, "bisect") {
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
	own, _, _ := splitScriptArgs(args)
	if len(own) > 0 && own[0] == "lsp" {
		return false
	}
	for _, mode := range []string{gen.FlagBuzzT, "test", gen.FlagBuzzCheck} {
		if hasModeFlag(own, mode) {
			return false
		}
	}
	if hasModeFlag(own, gen.FlagBuzzE) {
		return true
	}
	file, _, ok := splitTargetFromArgs(own, func(fs *flag.FlagSet) { gen.BindBuzz(fs) })
	return ok && file != "-"
}

// outputFlagGiven reports whether args set -o in any spelling, before a "--".
func outputFlagGiven(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, name := range []string{"o", "output"} {
			if isFlagNamed(a, name) || flagValueOf(a, name) != "" {
				return true
			}
		}
	}
	return false
}

func isPipeFile(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeNamedPipe != 0
}

// inheritedProjects is the selection a run naming no projects takes from the record
// stage writing its stdin: every project that stage's run.scope records named, in
// order. ok is false when no record stage writes stdin. It waits for that stage to
// close the stream, since its last record may name another project.
func inheritedProjects(ctx context.Context) (projects []string, upstream int, ok bool, err error) {
	in, pid := pipeRecordsIn(ctx)
	if in == nil {
		return nil, 0, false, nil
	}
	lines, err := in.All(ctx)
	if err != nil {
		return nil, pid, true, fmt.Errorf("read the records of pid %d upstream of this run: %w", pid, err)
	}
	projects, err = scopeProjects(lines)
	if err != nil {
		return nil, pid, true, fmt.Errorf("read the records of pid %d upstream of this run: %w", pid, err)
	}
	return projects, pid, true, nil
}

// scopePaths is the run.scope projects field for a selection: each project once, the
// root as ".".
func scopePaths(targets []types.Target) []string {
	var out []string
	for _, t := range targets {
		p := t.Path
		if p == "" {
			p = "."
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// scopeProjects collects the projects lines' run.scope records name, first mention
// first.
func scopeProjects(lines []report.Line) ([]string, error) {
	var out []string
	for _, l := range lines {
		if l.Type != report.TypeRunScope {
			continue
		}
		var scope report.RunScope
		if err := l.Decode(&scope); err != nil {
			return nil, fmt.Errorf("%s record: %w", l.Type, err)
		}
		for _, p := range scope.Projects {
			if p == "" {
				p = "."
			}
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out, nil
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
