package pipe

import (
	"context"
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

type (
	processStdioKey struct{}
	stdoutStageKey  struct{}
)

// Rules are the questions about a command line that only the CLI can answer: which
// verbs run targets, which carry an explicit -o. The pipe protocol asks them of this
// process's args and of the argv the kernel reports for its neighbours, so both ends
// of a pipe agree on what it carries.
type Rules struct {
	// RecordStage reports whether a magus invoked with args (argv without argv[0])
	// trades records with the magus stages beside it, and writes them to stdout.
	RecordStage func(args []string) bool
	// ReadsRecords reports whether a magus invoked with args reads the records a record
	// stage writing its stdin writes.
	ReadsRecords func(args []string) bool
	// ProvesUpstream reports whether a magus invoked with argv (argv[0] first) proves
	// the magus stages upstream of it at start.
	ProvesUpstream func(argv []string) bool
	// TakesLocks reports whether a magus invoked with argv (argv[0] first) may take
	// project locks.
	TakesLocks func(argv []string) bool
}

// ProveStdio starts proving the magus stages on either side of this process in a shell
// pipe, and carries the results on ctx. It runs at process start because a stage that
// fails fast is gone from the kernel before the run reaches its locks.
//
// The verbs that run targets prove every stage upstream, for their locks and for the
// pipeline's exit status, and so does a stage that prints or reads a shard plan (see
// AwaitProcess). A stage that trades records proves the stage it writes to and the one
// writing to it.
func ProveStdio(ctx context.Context, args []string, r Rules) context.Context {
	ctx = context.WithValue(ctx, stdoutStageKey{}, startStdoutStage(ctx, r.RecordStage(args), r.ProvesUpstream))
	if !r.ProvesUpstream(append([]string{os.Args[0]}, args...)) {
		return ctx
	}
	s := &Stdio{Stdin: os.Stdin, Stdout: os.Stdout, TakesLocks: r.TakesLocks}
	if r.ReadsRecords(args) {
		s.WritesRecords = func(argv []string) bool { return len(argv) > 0 && r.RecordStage(argv[1:]) }
	}
	s.ProveUpstream(ctx)
	return context.WithValue(ctx, processStdioKey{}, s)
}

// StdoutStage is what this process proved about the magus stage reading its stdout. The
// proof starts at process start and is awaited only where it is needed.
type StdoutStage struct {
	done chan struct{}
	// records is set when stdout carries records: this process is a record stage and a
	// magus reads its stdout.
	records bool
	proves  func(argv []string) bool
}

// startStdoutStage starts proving who reads stdout. A process that is no record stage
// writes prose whoever reads it.
func startStdoutStage(ctx context.Context, recordStage bool, proves func(argv []string) bool) *StdoutStage {
	s := &StdoutStage{done: make(chan struct{}), proves: proves}
	if !recordStage || !IsPipe(os.Stdout) {
		close(s.done)
		return s
	}
	go func() {
		defer close(s.done)
		if ReadByMagus(ctx, os.Stdout) {
			s.records = true
		}
	}()
	return s
}

// StageFromContext returns the stdout proof ProveStdio put on ctx, or nil.
func StageFromContext(ctx context.Context) *StdoutStage {
	s, _ := ctx.Value(stdoutStageKey{}).(*StdoutStage)
	return s
}

// WritesRecords reports whether this process writes its records to stdout, and its
// prose to stderr.
func (s *StdoutStage) WritesRecords() bool {
	if s == nil {
		return false
	}
	<-s.done
	return s.records
}

// readerProofWait bounds how long a record stage waits for the magus reading its
// stdout to prove it. A reader proves its upstream as soon as it starts, however long
// that takes on a loaded machine; the bound only frees a writer whose reader failed its
// proof and still holds the pipe, waiting for the end of a stream it never reads.
const readerProofWait = 10 * time.Second

// Linger holds a record stage back from exiting until every magus reading its stdout
// has proven it, which none can do once this stage has exited: a reader slowed by load
// past any fixed grace would otherwise find no upstream at all.
func (s *StdoutStage) Linger() {
	if !s.WritesRecords() {
		return
	}
	AwaitReaderProof(os.Stdout, s.proves, readerProofWait)
}

// RecordsIn returns the record stream of the record stage writing stdin, and its pid;
// nil when none does.
func RecordsIn(ctx context.Context) (*report.Reader, int) {
	s, _ := ctx.Value(processStdioKey{}).(*Stdio)
	return s.Records()
}

// InheritedProjects is the selection a run naming no projects takes from the record
// stage writing its stdin: every project that stage's run.scope records named, in
// order. ok is false when no record stage writes stdin. It waits for that stage to
// close the stream, since its last record may name another project.
func InheritedProjects(ctx context.Context) (projects []string, upstream int, ok bool, err error) {
	in, pid := RecordsIn(ctx)
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

// ScopePaths is the run.scope projects field for a selection: each project once, the
// root as ".".
func ScopePaths(targets []types.Target) []string {
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

// ProcessStdioContext hands the runs on ctx this process's standard streams, so they can
// defer to a magus upstream of them in a shell pipe (see Stdio). An adopted run gets
// none: it executes inside the server, whose stdin belongs to nobody's pipe.
func ProcessStdioContext(ctx context.Context, adopted bool, takesLocks func(argv []string) bool) context.Context {
	if adopted {
		return ctx
	}
	if s, ok := ctx.Value(processStdioKey{}).(*Stdio); ok {
		return WithStdio(ctx, *s)
	}
	return WithStdio(ctx, Stdio{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		TakesLocks: takesLocks,
	})
}

// SettleProcess is the pipefail every magus pipeline gets: a stage that succeeded
// reports the first proven upstream magus stage that failed (MGS3030). dir is where the
// workspace search starts.
func SettleProcess(ctx context.Context, dir string, cfg config.Config) error {
	s, ok := ctx.Value(processStdioKey{}).(*Stdio)
	if !ok {
		return nil
	}
	root, err := workspace.FindRoot(dir)
	if err != nil {
		return nil //nolint:nilerr // no workspace, so no stage left a record to read
	}
	return s.SettlePipeline(ctx, root, cfg)
}

// AwaitProcess holds a stage that prints or reads a shard plan until every magus stage
// proven upstream of it has ended, and refuses with MGS3030 when one failed. A plan
// stage takes no project lock, so nothing else would hold it back, and a plan printed
// behind a red stage fans shards out from a tree that stage rejected. Call it once stdin
// holds nothing more the stage reads. An adopted run waits on nothing.
func AwaitProcess(ctx context.Context, dir string, adopted bool, cfg config.Config) error {
	s, ok := ctx.Value(processStdioKey{}).(*Stdio)
	if !ok {
		return nil
	}
	if adopted {
		return nil
	}
	ws, err := workspace.FindRoot(dir)
	if err != nil {
		return nil //nolint:nilerr // no workspace, so no stage left a record to read
	}
	return s.AwaitUpstream(ctx, ws, cfg)
}

// RecordProcessExit leaves code for the magus stage reading this process's stdout. See
// RecordExit. dir is where the workspace search starts.
func RecordProcessExit(dir string, cfg config.Config, code int, interrupted func() (syscall.Signal, bool)) {
	root, err := workspace.FindRoot(dir)
	if err != nil {
		return
	}
	signal := ""
	if sig, ok := interrupted(); ok {
		signal = sig.String()
	}
	RecordExit(root, cfg, code, signal)
}
