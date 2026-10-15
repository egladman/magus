package pipe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file/record"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/internal/sys/pipepeer"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// Stdio is the standard streams of the process a run executes in. Only a caller running
// the invocation in its own process should hand it to a run (see WithStdio): a server
// serving many invocations has no stdin that belongs to any one of them.
//
// With it, a run whose stdin is a pipe written by another magus process (proven from the
// kernel, never inferred) takes no project lock while that upstream still holds, or has
// yet to settle, a project this run needs. The pipe is drained meanwhile, so the upstream
// never blocks on it, and Stdin reads the same bytes afterwards. A run whose stdout is a
// pipe publishes the projects it holds, which is what lets the stage after it proceed at
// once when they do not overlap.
//
// A run also starts nothing once a proven upstream has exited non-zero (MGS3030). See
// ProveUpstream and SettlePipeline for the rest of that contract.
type Stdio struct {
	Stdin  *os.File
	Stdout *os.File
	// TakesLocks reports whether a magus invoked with argv (argv[0] first) may take
	// project locks. An upstream it answers false for never delays this run. nil counts
	// every upstream magus as one that may.
	TakesLocks func(argv []string) bool
	// WritesRecords reports whether a magus invoked with argv (argv[0] first) writes
	// records to a magus reading its stdout. When the stage writing Stdin directly is one,
	// ProveUpstream reads Stdin as its records (see Records). nil reads none.
	WritesRecords func(argv []string) bool

	// pipeline is shared by every copy of this value, so the stages one run proves are
	// the ones the process settles at its end. nil until ProveUpstream.
	pipeline *pipeline
}

type stdioKey struct{}

// WithStdio returns ctx carrying s for the runs started under it. The CLI sets it for a
// run it executes in its own process; a run on a ctx without it neither waits on a pipe
// upstream nor publishes its lock set downstream.
func WithStdio(ctx context.Context, s Stdio) context.Context {
	return context.WithValue(ctx, stdioKey{}, s)
}

// StdioFrom returns the Stdio WithStdio put on ctx, and false when there is none.
func StdioFrom(ctx context.Context) (Stdio, bool) {
	s, ok := ctx.Value(stdioKey{}).(Stdio)
	return s, ok
}

// ProveUpstream starts proving, in the background, which magus stages sit upstream of
// this process in a shell pipe. Call it once, at process start: a stage that fails fast
// has exited by the time a run reaches its locks, and with it goes the kernel's only
// evidence that it was ever upstream. Every run given s afterwards weighs those stages
// too, and SettlePipeline reports them.
//
// When a record stage writes Stdin directly, it then starts reading its records (see
// ReadRecords), after the walk: reading them puts a relay in Stdin's place, which no
// walk can follow back. When another magus stage writes Stdin directly, it keeps a
// second descriptor on the pipe instead. Either is what the writer waits to see before
// it exits (see AwaitReaderProof).
func (s *Stdio) ProveUpstream(ctx context.Context) {
	p := &pipeline{ready: make(chan struct{})}
	s.pipeline = p
	if s.Stdin == nil {
		close(p.ready)
		return
	}
	takesLocks, writesRecords := s.TakesLocks, s.WritesRecords
	stdin := s.Stdin
	go func() {
		defer close(p.ready)
		in, err := pipepeer.ReadEnd(os.Getpid(), int(stdin.Fd()))
		if err != nil {
			return
		}
		recordsFrom := 0
		if writesRecords != nil {
			recordsFrom, _ = RecordUpstream(ctx, stdin, writesRecords)
		}
		// A cycle is refused by the walk a run repeats at its locks.
		ups, _ := walkUpstream(ctx, in, takesLocks)
		p.add(ups)
		if recordsFrom == 0 {
			if slices.ContainsFunc(ups, func(u Stage) bool { return u.Depth == 0 }) {
				p.holdProof(stdin)
			}
			return
		}
		r, err := ReadRecords(stdin)
		if err != nil {
			slog.WarnContext(ctx, fmt.Sprintf("pid %d upstream writes records this process cannot read", recordsFrom),
				attr.Notice(""), attr.Component("magus"), attr.Error(err))
			p.holdProof(stdin)
			return
		}
		p.mu.Lock()
		p.records, p.recordsFrom = r, recordsFrom
		p.mu.Unlock()
	}()
}

// Records returns the record stream of the record stage writing Stdin directly, and its
// pid, once ProveUpstream has proven it; nil when none writes Stdin.
func (s *Stdio) Records() (*report.Reader, int) {
	if s == nil || s.pipeline == nil {
		return nil, 0
	}
	<-s.pipeline.ready
	s.pipeline.mu.Lock()
	defer s.pipeline.mu.Unlock()
	return s.pipeline.records, s.pipeline.recordsFrom
}

// ReadsRecords reports whether this process reads Stdin as records. That reader drains
// the pipe already, so a run neither spools nor drains it.
func (s *Stdio) ReadsRecords() bool {
	r, _ := s.Records()
	return r != nil
}

// Upstream returns the magus stages upstream of this process now, walked back from
// Stdin, together with every stage ProveUpstream proved at start. It returns none when
// Stdin is nil, and only the proven ones when Stdin is no pipe or the kernel cannot
// prove its peers. It refuses with MGS3023 when Stdin is written, through a magus stage,
// by this process itself.
func (s *Stdio) Upstream(ctx context.Context) ([]Stage, error) {
	if s == nil || s.Stdin == nil {
		return nil, nil
	}
	in, err := pipepeer.ReadEnd(os.Getpid(), int(s.Stdin.Fd()))
	if err != nil {
		return s.pipeline.merge(nil), nil //nolint:nilerr // not a pipe, or no proof on this platform: nothing to wait on
	}
	ups, err := walkUpstream(ctx, in, s.TakesLocks)
	if err != nil {
		return nil, err
	}
	return s.pipeline.merge(ups), nil
}

// KeepSpool hands sp, the spool now relaying Stdin, to the process's pipeline, so
// settling it drains the upstream through sp rather than through Stdin.
func (s *Stdio) KeepSpool(sp *Spool) {
	if s == nil {
		return
	}
	s.pipeline.keepSpool(sp)
}

// SettlePipeline ends a stage that succeeded. It waits for every magus stage proven
// upstream of this process to end, and returns MGS3030 when one exited non-zero, with
// that stage's status (1 for one stopped by a signal) as its ExitCode. It returns nil
// when nothing was proven, which is always the case where the kernel cannot prove it.
//
// It stops reading stdin. While a stage that may take locks still runs, it drains and
// discards the pipe so that stage finishes as it would have; then it closes stdin, as
// exiting would, so a read-only producer ends the way it does under a plain shell. A
// stage that ends without leaving its status is reported on stderr as unknown, as a
// record under -o jsonl, and never counted as a failure. ctx bounds the wait.
func (s *Stdio) SettlePipeline(ctx context.Context, root string, cfg config.Config) error {
	if s.pipeline == nil {
		return nil
	}
	stdin := s.Stdin
	if s.ReadsRecords() {
		// The record reader drains it; closing it under that reader is what exiting does.
		stdin = nil
	}
	return s.pipeline.settle(ctx, stdin, Dir(config.CacheDir(root, cfg), root), newNotices(cfg))
}

// AwaitUpstream holds back a stage whose output another stage acts on, such as a shard
// plan, until every magus stage proven upstream of this process has ended. It returns
// MGS3030 when one exited non-zero, and the caller then writes nothing: a pipeline that
// failed upstream hands nothing on. It waits and drains stdin as SettlePipeline does, so
// call it only once this process has read all it needs from stdin. It returns nil when
// nothing was proven upstream.
func (s *Stdio) AwaitUpstream(ctx context.Context, root string, cfg config.Config) error {
	if s.pipeline == nil {
		return nil
	}
	stdin := s.Stdin
	if s.ReadsRecords() {
		stdin = nil
	}
	red, ok, err := s.pipeline.wait(ctx, stdin, Dir(config.CacheDir(root, cfg), root), newNotices(cfg))
	if err != nil {
		return err
	}
	if !ok {
		// Every stage ended green, so SettlePipeline has nothing left to report.
		s.pipeline.forget()
		return nil
	}
	return &upstreamError{
		DiagnosticError: types.DiagnosticErrorf(types.PipeUpstreamFailed,
			"pid %d (%s), upstream of this run in a pipe, %s, so this run printed nothing."+
				" A pipeline stops at its first failed stage: fix it, then run the pipeline again.",
			red.stage.PID, red.stage.Command(), red.rec.ending()),
		status: red.rec.exitCode(),
	}
}

// RecordExit leaves this process's exit status where the magus stage reading its stdout
// finds it: beside the pipe records of the workspace at root. signal names the signal
// that stopped the process, empty when none did. It does nothing unless stdout is a pipe
// and the workspace's cache dir exists, and is best-effort: a reader that finds no
// record reports the status as unknown. Call it last, just before exiting.
func RecordExit(root string, cfg config.Config, status int, signal string) {
	if !IsPipe(os.Stdout) {
		return
	}
	cacheDir := config.CacheDir(root, cfg)
	// Never recreate a cache dir: `magus clean` just removed it, or nothing has run here.
	if _, err := os.Stat(cacheDir); err != nil {
		return
	}
	writeExitRecord(Dir(cacheDir, root), exitRecord{
		PID: os.Getpid(), Command: strings.Join(os.Args, " "), Status: status, Signal: signal, Ended: time.Now(),
	})
}

// gate is Gate's flock. Referenced for the life of the process so no finalizer closes
// it, which would lift the gate early.
var gate *flock.Flock

// Gate holds the magus stage reading this process's stdout from starting anything
// until this process exits: it takes no lock, and it refuses with MGS3030 if this process
// exits non-zero. It does nothing unless stdout is a pipe. Call it at process start: a
// stage that has already passed its locks does not look again.
//
// There is no release. The reader must see this process's exit record before it starts,
// and that is written after the command returns.
func Gate(root string, cfg config.Config) {
	if gate != nil || !IsPipe(os.Stdout) {
		return
	}
	dir := Dir(config.CacheDir(root, cfg), root)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	fl := flock.New(GatePath(dir, os.Getpid()))
	if got, err := fl.TryLock(); err == nil && got {
		gate = fl
	}
}

// GatePath is where Gate leaves the flock of process procID in the pipe dir dir.
func GatePath(dir string, procID int) string {
	return filepath.Join(dir, strconv.Itoa(procID)+gateSuffix)
}

// ReadByMagus reports whether out is a pipe that the kernel proves another process
// running this same executable reads: the next magus stage of a shell pipe, which takes
// this process's records rather than its prose. A reader that is one of this process's
// ancestors (a parent magus capturing a target's output) does not count, and neither
// does anything on a platform where the kernel cannot prove it.
//
// It blocks while a reader is a shell child that has yet to exec its command, up to
// execWait, since the shell forks every stage of a pipeline at once and a stage may
// run other commands before it execs.
func ReadByMagus(ctx context.Context, out *os.File) bool {
	_, ok := ReaderArgv(ctx, out)
	return ok
}

// ReaderArgv is ReadByMagus that also returns the reader's argv, argv[0] first: what the
// next stage of the pipe was asked to do. argv is nil when a magus reads out but its
// argv cannot be read.
func ReaderArgv(ctx context.Context, out *os.File) (argv []string, ok bool) {
	if out == nil {
		return nil, false
	}
	self := os.Getpid()
	p, err := pipepeer.WriteEnd(self, int(out.Fd()))
	if err != nil {
		return nil, false
	}
	ancestors := ancestorPIDs(ctx, self)
	ok = pollPeers(ctx, p.Readers, p.ReaderExec, func(pid int) bool { return pid == self || ancestors[pid] }, func(_ int, readerArgv []string) bool {
		argv = readerArgv
		return true
	})
	return argv, ok
}

// AwaitReaderProof holds this process, a stage writing out, until every magus reading out
// as its stdin whose argv proves answers true for has proven this stage upstream of it,
// or has exited, or limit has passed. A reader proves its upstream from the kernel, which
// it cannot do once this stage has exited, and it may get to that late on a loaded
// machine. It has proven it once it holds the pipe at a descriptor besides stdin, or no
// longer at stdin (see ProveUpstream). limit only frees a writer whose reader failed its
// proof. It returns at once where the kernel cannot prove a pipe's peers.
func AwaitReaderProof(out *os.File, proves func(argv []string) bool, limit time.Duration) {
	self := os.Getpid()
	p, err := pipepeer.WriteEnd(self, int(out.Fd()))
	if err != nil {
		return
	}
	readers, err := p.Readers()
	if err != nil {
		return
	}
	unproven := func(pid int) bool {
		fds := p.ReadFDs(pid)
		return len(fds) == 1 && fds[0] == 0
	}
	pending := slices.DeleteFunc(readers, func(pid int) bool {
		if pid == self || !unproven(pid) {
			return true
		}
		argv, state := p.ReaderExec(pid)
		return state != pipepeer.ExecSame || argv == nil || !proves(argv)
	})
	deadline := time.Now().Add(limit)
	for len(pending) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		pending = slices.DeleteFunc(pending, func(pid int) bool { return !unproven(pid) })
	}
}

// RecordUpstream returns the pid of a magus stage writing in directly, proven from the
// kernel as ReadByMagus proves a reader, whose argv writes answers true for: a stage
// whose stdout carries records. A shell child still between fork and exec is waited
// for, up to execWait. ok is false when no such stage writes in.
func RecordUpstream(ctx context.Context, in *os.File, writes func(argv []string) bool) (pid int, ok bool) {
	if in == nil {
		return 0, false
	}
	self := os.Getpid()
	p, err := pipepeer.ReadEnd(self, int(in.Fd()))
	if err != nil {
		return 0, false
	}
	ancestors := ancestorPIDs(ctx, self)
	found := 0
	ok = pollPeers(ctx, p.Writers, p.WriterExec, func(w int) bool { return w == self || ancestors[w] }, func(w int, argv []string) bool {
		if argv == nil || !writes(argv) {
			return false
		}
		found = w
		return true
	})
	return found, ok
}

// ReadRecords starts reading stdin, which a record stage writes (see RecordUpstream), as
// records. A line that is not a record is what the upstream's targets wrote to their
// stdout; it keeps reaching whatever in this process reads stdin, through a pipe put in
// stdin's place, so a target downstream reads its upstream's bytes as it always has.
// Those bytes are held in memory until read, so the upstream never blocks on them.
func ReadRecords(stdin *os.File) (*report.Reader, error) {
	src, _, err := dupForDrain(stdin)
	if err != nil {
		return nil, fmt.Errorf("read the records on stdin: %w", err)
	}
	w, err := installRelay(stdin)
	if err != nil {
		_ = src.Close()
		return nil, fmt.Errorf("relay stdin: %w", err)
	}
	relay := newByteRelay(w)
	r := report.NewReader(src, relay)
	go func() {
		<-r.Done()
		relay.close()
	}()
	return r, nil
}

// byteRelay passes bytes on to w without ever blocking the writer: what w has not taken
// yet waits in memory.
type byteRelay struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	eof  bool
}

func newByteRelay(w io.WriteCloser) *byteRelay {
	b := &byteRelay{}
	b.cond = sync.NewCond(&b.mu)
	go b.pump(w)
	return b
}

func (b *byteRelay) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.eof {
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	b.cond.Broadcast()
	return len(p), nil
}

func (b *byteRelay) close() {
	b.mu.Lock()
	b.eof = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

func (b *byteRelay) pump(w io.WriteCloser) {
	defer w.Close()
	for {
		b.mu.Lock()
		for len(b.buf) == 0 && !b.eof {
			b.cond.Wait()
		}
		chunk, done := b.buf, b.eof && len(b.buf) == 0
		b.buf = nil
		b.mu.Unlock()
		if done {
			return
		}
		if _, err := w.Write(chunk); err != nil {
			// Nobody reads stdin any more; the bytes have nowhere to go.
			b.mu.Lock()
			b.buf, b.eof = nil, true
			b.mu.Unlock()
			return
		}
	}
}

// execWait bounds how long a peer proof waits for a shell child to exec its command.
// The proof waits for the exec itself, however long a loaded machine or a subshell like
// `(sleep 1; exec magus ...)` takes to reach it: a peer judged before it execs is judged
// by the shell it still is. A fork that sits blocked instead, like a bash `while read`
// loop, is let go once seen blocked for blockedFor. The bound only frees a proof
// whose peer keeps working without ever exec'ing, like `(make; cat out)`.
const execWait = 3 * time.Second

// blockedFor is how long a fork must sit blocked, with no child and not one step
// taken, before a peer proof stops waiting for it to exec. A fork on its way to exec is
// runnable even when a loaded machine gives it no CPU, so this only has to outlast a
// fork's brief waits on the kernel before its exec.
const blockedFor = 50 * time.Millisecond

// pollPeers asks peers until one running this executable matches, skipping those skip
// names; exec reports what each has exec'd. It keeps asking only while some peer is a
// fork that has not exec'd yet and is not sitting blocked.
func pollPeers(ctx context.Context, peers func() ([]int, error), exec func(pid int) ([]string, pipepeer.ExecState),
	skip func(pid int) bool, match func(pid int, argv []string) bool,
) bool {
	deadline := time.Now().Add(execWait)
	blocked := map[int]blockedFork{}
	for {
		pids, err := peers()
		if err != nil {
			return false
		}
		pending := false
		for _, pid := range pids {
			if skip(pid) {
				continue
			}
			argv, state := exec(pid)
			switch state {
			case pipepeer.ExecPending:
				pending = pending || !sitsBlocked(blocked, pid)
			case pipepeer.ExecSame:
				if match(pid, argv) {
					return true
				}
			case pipepeer.ExecOther:
			}
		}
		if !pending || time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// blockedFork is the first sample of a fork seen blocked, and when it was taken.
type blockedFork struct {
	first pipepeer.Activity
	since time.Time
}

// sitsBlocked reports whether the fork pid has sat blocked since the first of these
// samples for blockedFor, recording the samples in seen. A fork that cannot be
// sampled never counts as blocked.
func sitsBlocked(seen map[int]blockedFork, pid int) bool {
	now, err := pipepeer.Sample(pid)
	if err != nil {
		return false
	}
	b, ok := seen[pid]
	if !ok || !b.first.StillBlocked(now) {
		seen[pid] = blockedFork{first: now, since: time.Now()}
		return false
	}
	return time.Since(b.since) >= blockedFor
}

const (
	// DirName is the pipe dir inside a workspace's locks dir.
	DirName    = ".pipe"
	gateSuffix = ".gate"
	exitSuffix = ".exit"
	// sweepName's mtime is the last scan, shared across processes that each
	// exit before a timer could fire.
	sweepName = ".sweep"
	// exitRecordKeep is how long an exit record outlives its writer. It must cover the
	// longest run downstream of it, which reads the record when it finishes.
	exitRecordKeep = 24 * time.Hour
	// exitSweepEvery spaces out that scan. A piped magus writes a record as it exits,
	// and a hook's stdout is a pipe, so scanning on every write re-reads the whole
	// directory on the way out of every verdict. Readers already ignore a record older
	// than the proof they were shown; the scan only bounds the directory.
	exitSweepEvery = time.Hour
	// walkDepth bounds the walk back through upstream magus stages; a pipeline
	// deeper than this is not one anybody types.
	walkDepth = 16
	// pollEvery paces a wait on upstream stages to end.
	pollEvery = 100 * time.Millisecond
)

// Dir is the pipe dir of the workspace at root whose cache dir is cacheDir: where its
// runs leave their gates, exit records and the records a downstream stage reads.
func Dir(cacheDir, root string) string {
	return filepath.Join(workspace.LocksDir(cacheDir, root), DirName)
}

// exitRecord is how a pipe stage ended. Unlike a holds record it outlives its writer on
// purpose, so the stage reading the pipe learns the status after the writer has gone.
type exitRecord struct {
	PID     int    `record:"pid"`
	Command string `record:"command,omitempty"`
	Status  int    `record:"status"`
	Signal  string `record:"signal,omitempty"`
	// Ended has the record format's one-second resolution.
	Ended time.Time `record:"ended"`
}

func (r exitRecord) failed() bool { return r.Status != 0 || r.Signal != "" }

// exitCode is the status a stage failing because of r exits with. A signal's 128+N
// would claim this stage was the one interrupted.
func (r exitRecord) exitCode() int {
	if r.Signal != "" || r.Status <= 0 {
		return 1
	}
	return r.Status
}

func (r exitRecord) ending() string {
	if r.Signal != "" {
		return "was stopped by a signal (" + r.Signal + ")"
	}
	return fmt.Sprintf("exited %d", r.Status)
}

// Stage is one magus process upstream of this run in a shell pipe.
type Stage struct {
	PID int
	// Writes is the pipe this stage writes, which the stage after it reads.
	Writes     pipepeer.Pipe
	Argv       []string
	TakesLocks bool
	// Depth is how many pipes back from the run the stage sits, 0 for one writing its
	// stdin directly.
	Depth int
	// Proved is when the stage was seen alive. An exit record older than that was left
	// by an earlier process with the same pid.
	Proved time.Time
}

// Command is the stage's argv as one line, for a message that names it.
func (u Stage) Command() string { return strings.Join(u.Argv, " ") }

// walkUpstream walks back from the pipe this run reads, through every process writing it,
// to the processes writing theirs, and returns the magus stages among them. A stage that
// is not this executable (cat, tee, jq) is walked through but never weighed: only its
// pipes are read, never its arguments. The walk skips this run's own ancestors, which
// write its stdin by design (magus.run's stdin option) and wait for it, so they are
// never waited on; that also keeps it out of the shell and harness that launched it.
func walkUpstream(ctx context.Context, in pipepeer.Pipe, takesLocks func([]string) bool) ([]Stage, error) {
	self := os.Getpid()
	ancestors := ancestorPIDs(ctx, self)
	seen := map[int]bool{self: true}
	var out []Stage
	frontier := []pipepeer.Pipe{in}
	for depth := 0; depth < walkDepth && len(frontier) > 0; depth++ {
		var next []pipepeer.Pipe
		for _, p := range frontier {
			writers, err := p.Writers()
			if err != nil {
				continue
			}
			for _, w := range writers {
				// A loop through nothing but other tools holds nobody's lock, so only a
				// loop through a magus stage would wait on itself.
				if w == self && depth > 0 && len(out) > 0 {
					return nil, types.DiagnosticErrorf(types.PipeCycle,
						"this run's standard input is written, through magus pid %s, by this run itself."+
							" Break the loop so no stage's input depends on its own output.", joinPIDs(out))
				}
				if seen[w] || ancestors[w] {
					continue
				}
				seen[w] = true
				// A fork pending exec is never a stage, and unlike pollPeers the walk does
				// not wait for it: it writes through a copy of its parent's descriptor, so
				// the parent is a writer the walk weighs in its place.
				if argv, state := p.WriterExec(w); state == pipepeer.ExecSame {
					takes := argv == nil || takesLocks == nil || takesLocks(argv)
					out = append(out, Stage{PID: w, Writes: p, Argv: argv, TakesLocks: takes, Depth: depth, Proved: time.Now()})
				}
				if wp, err := pipepeer.ReadEnd(w, 0); err == nil {
					next = append(next, wp)
				}
			}
		}
		frontier = next
	}
	return out, nil
}

// ancestorPIDs is this process's parent chain plus the processes that minted this
// invocation's recorded ancestry.
func ancestorPIDs(ctx context.Context, self int) map[int]bool {
	out := map[int]bool{}
	for p, i := self, 0; p > 1 && i < 64; i++ {
		parent, err := pipepeer.Parent(p)
		if err != nil || parent <= 0 {
			break
		}
		out[parent] = true
		p = parent
	}
	for _, ref := range types.InvocationAncestorsFromContext(ctx) {
		head, _, _ := strings.Cut(ref, ":")
		if n, err := strconv.Atoi(head); err == nil {
			out[n] = true
		}
	}
	return out
}

func joinPIDs(ups []Stage) string {
	ids := make([]string, len(ups))
	for i, u := range ups {
		ids[i] = strconv.Itoa(u.PID)
	}
	return strings.Join(ids, ", ")
}

// IsPipe reports whether f is a pipe; false for nil.
func IsPipe(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeNamedPipe != 0
}

// pipeline is what one process has proven of the magus stages upstream of it. It is safe
// for concurrent use, and its methods are no-ops on nil.
type pipeline struct {
	ready chan struct{} // closed once ProveUpstream's walk is done

	mu     sync.Mutex
	stages []Stage
	spool  *Spool
	// records reads the stdin recordsFrom writes, when that stage writes records.
	records     *report.Reader
	recordsFrom int
	// proof is a second descriptor on stdin's pipe, held from the end of the walk until
	// stdin closes. It is how the stage writing stdin sees the walk is done (see
	// AwaitReaderProof).
	proof *os.File
}

// holdProof keeps a second descriptor on stdin's pipe, so the stage writing it sees this
// process has proven it. A record reader needs none: its relay moves stdin off the pipe.
func (p *pipeline) holdProof(stdin *os.File) {
	dup, restore, err := dupForDrain(stdin)
	if err != nil {
		return
	}
	// The dup is never read; stdin keeps the blocking mode it had.
	_ = restore()
	p.mu.Lock()
	p.proof = dup
	p.mu.Unlock()
}

// releaseProof closes the descriptor holdProof kept, with stdin, so a stage this process
// stops reading sees the pipe close as it would at exit.
func (p *pipeline) releaseProof() {
	p.mu.Lock()
	proof := p.proof
	p.proof = nil
	p.mu.Unlock()
	if proof != nil {
		_ = proof.Close()
	}
}

func (p *pipeline) add(ups []Stage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, u := range ups {
		if !slices.ContainsFunc(p.stages, func(k Stage) bool { return k.PID == u.PID }) {
			p.stages = append(p.stages, u)
		}
	}
}

// merge records ups and returns them together with every stage proven earlier.
func (p *pipeline) merge(ups []Stage) []Stage {
	if p == nil {
		return ups
	}
	<-p.ready
	p.add(ups)
	out := slices.Clone(ups)
	for _, u := range p.all() {
		if !slices.ContainsFunc(out, func(k Stage) bool { return k.PID == u.PID }) {
			out = append(out, u)
		}
	}
	return out
}

func (p *pipeline) forget() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stages = nil
}

func (p *pipeline) all() []Stage {
	<-p.ready
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.stages)
}

func (p *pipeline) keepSpool(sp *Spool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.spool = sp
	p.mu.Unlock()
}

// stageOutcome is an upstream stage whose exit record has been read.
type stageOutcome struct {
	stage Stage
	rec   exitRecord
}

// firstRed picks the failed stage furthest upstream: the one that failed first, since
// every magus stage after it fails with it.
func firstRed(ended []stageOutcome) (stageOutcome, bool) {
	var red stageOutcome
	found := false
	for _, o := range ended {
		if o.rec.failed() && (!found || o.stage.Depth > red.stage.Depth) {
			red, found = o, true
		}
	}
	return red, found
}

// FailedUpstream returns MGS3030 when a stage of ups has already exited non-zero, as the
// exit records in the pipe dir dir show; nil otherwise. A stage still running is
// SettlePipeline's to report.
func FailedUpstream(dir string, ups []Stage) error {
	var ended []stageOutcome
	for _, u := range ups {
		if rec, ok := readExitRecord(dir, u); ok {
			ended = append(ended, stageOutcome{stage: u, rec: rec})
		}
	}
	red, ok := firstRed(ended)
	if !ok {
		return nil
	}
	return &upstreamError{
		DiagnosticError: types.DiagnosticErrorf(types.PipeUpstreamFailed,
			"pid %d (%s), upstream of this run in a pipe, %s, so this run started nothing."+
				" A pipeline stops at its first failed stage: fix it, then run the pipeline again.",
			red.stage.PID, red.stage.Command(), red.rec.ending()),
		status: red.rec.exitCode(),
	}
}

// upstreamError is MGS3030 with the status the stage exits with.
type upstreamError struct {
	*types.DiagnosticError
	status int
}

func (e *upstreamError) ExitCode() int { return e.status }

func (e *upstreamError) Unwrap() error { return e.DiagnosticError }

// notices is where settling a pipeline reports a stage it could not judge: a line of
// prose on out or, under -o jsonl, a record on records, since prose would be free text on
// a stream a caller parses. Like the lock's decision lines they are written straight to
// the stream, so -s/--silent cannot suppress them. The run's record writer has closed by
// the time a stage settles, so a record goes to the notice stream on stderr.
type notices struct {
	out     io.Writer
	records *report.LineEncoder
}

// newNotices reports on stderr, as records when cfg asks for -o jsonl.
func newNotices(cfg config.Config) notices {
	n := notices{out: os.Stderr}
	if strings.EqualFold(cfg.Log.Format, "jsonl") {
		n.records = report.NewLineEncoder(os.Stderr)
	}
	return n
}

// statusUnknown reports that u ended without leaving its exit status.
func (n notices) statusUnknown(u Stage) {
	msg := fmt.Sprintf("magus: pid %d (%s), upstream of this run in a pipe, ended without leaving its exit status, so whether it failed is unknown.",
		u.PID, u.Command())
	if n.records != nil {
		_ = n.records.Encode(report.Notice{Level: slog.LevelWarn, Message: msg, Attrs: map[string]any{
			"upstream_pid": u.PID, "upstream_command": u.Command(),
		}})
		return
	}
	fmt.Fprintln(n.out, msg)
}

// settle is SettlePipeline once the pipe dir is known.
func (p *pipeline) settle(ctx context.Context, stdin *os.File, dir string, n notices) error {
	red, ok, err := p.wait(ctx, stdin, dir, n)
	if err != nil || !ok {
		return err
	}
	return &upstreamError{
		DiagnosticError: types.DiagnosticErrorf(types.PipeUpstreamFailed,
			"pid %d (%s), upstream of this run in a pipe, %s. This run succeeded, but the pipeline failed at that stage.",
			red.stage.PID, red.stage.Command(), red.rec.ending()),
		status: red.rec.exitCode(),
	}
}

// wait blocks until every proven upstream stage has ended, draining stdin meanwhile, and
// returns the failed one furthest upstream (see firstRed); ok is false when none failed.
func (p *pipeline) wait(ctx context.Context, stdin *os.File, dir string, n notices) (stageOutcome, bool, error) {
	pending := p.all()
	if len(pending) == 0 {
		return stageOutcome{}, false, nil
	}
	stop := p.stopReading(stdin)
	defer stop()

	var ended []stageOutcome
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		running := pending[:0]
		for _, u := range pending {
			rec, ok := readExitRecord(dir, u)
			if !ok && pid.Alive(u.PID) && pipepeer.SameExecutable(u.PID) {
				running = append(running, u)
				continue
			}
			if !ok {
				// The record is written before exit, so a stage that has gone may only
				// have finished writing it since the read above.
				rec, ok = readExitRecord(dir, u)
			}
			if !ok {
				n.statusUnknown(u)
				continue
			}
			ended = append(ended, stageOutcome{stage: u, rec: rec})
		}
		pending = running
		if len(pending) == 0 {
			break
		}
		if !slices.ContainsFunc(pending, func(u Stage) bool { return u.TakesLocks }) {
			stop()
		}
		select {
		case <-ctx.Done():
			return stageOutcome{}, false, fmt.Errorf("pipe: gave up waiting on pid %d (%s) upstream of this run: %w", pending[0].PID, pending[0].Command(), ctx.Err())
		case <-t.C:
		}
	}
	red, found := firstRed(ended)
	return red, found, nil
}

// stopReading keeps stdin drained, so an upstream still writing never blocks on it, and
// returns the func that closes it for good. That func is idempotent.
func (p *pipeline) stopReading(stdin *os.File) func() {
	p.mu.Lock()
	sp := p.spool
	p.mu.Unlock()
	closeStdin := func() {
		p.releaseProof()
		if stdin != nil {
			_ = stdin.Close()
		}
	}
	if sp != nil {
		// The spool already drains the upstream's pipe; stdin is only its relay.
		return sync.OnceFunc(func() { sp.Abandon(); closeStdin() })
	}
	if stdin == nil {
		return func() {}
	}
	src, restore, err := dupForDrain(stdin)
	if err != nil {
		return sync.OnceFunc(closeStdin)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, src)
	}()
	return sync.OnceFunc(func() {
		_ = src.Close()
		<-done
		_ = restore()
		closeStdin()
	})
}

// readExitRecord returns u's exit record, when u has ended and left one.
func readExitRecord(dir string, u Stage) (exitRecord, bool) {
	var rec exitRecord
	if record.Read(filepath.Join(dir, strconv.Itoa(u.PID)+exitSuffix), &rec) != nil || rec.PID != u.PID {
		return exitRecord{}, false
	}
	if rec.Ended.Before(u.Proved.Truncate(time.Second)) {
		return exitRecord{}, false
	}
	return rec, true
}

func writeExitRecord(dir string, rec exitRecord) {
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	if sweepDue(dir) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*"+exitSuffix))
		for _, path := range matches {
			var old exitRecord
			if record.Read(path, &old) == nil && time.Since(old.Ended) > exitRecordKeep && !pid.Alive(old.PID) {
				_ = record.Remove(path)
			}
		}
	}
	_ = record.Write(filepath.Join(dir, strconv.Itoa(rec.PID)+exitSuffix), rec)
}

// sweepDue reports whether dir should be scanned, and records that decision
// before the caller walks. The record is the file's mtime. A walk whose mtime
// cannot be written would run again on the next exit, which is the full
// directory scan this hour exists to skip, so that walk is not started.
// A missing file is created; that first success is due. Any other stat or
// update failure leaves the previous mtime alone and is not due.
func sweepDue(dir string) bool {
	path := filepath.Join(dir, sweepName)
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return false
		}
		return true
	}
	if time.Since(info.ModTime()) < exitSweepEvery {
		return false
	}
	now := time.Now()
	if err := stampSweep(path, now, now); err != nil {
		return false
	}
	return true
}

// stampSweep records a scan in the sweep file's mtime. A test swaps it to fail
// the record while the directory stays writable.
var stampSweep = os.Chtimes

// Spool drains a waiting run's stdin into an unlinked file, so the upstream writing it
// never blocks on a full pipe, and then replays it: the spooled bytes first, then
// whatever arrives after, through a pipe put in stdin's place.
type Spool struct {
	src     *os.File // a duplicate of stdin, read by drain
	restore func() error
	file    *os.File

	mu   sync.Mutex
	cond *sync.Cond
	size int64
	eof  bool
	err  error

	drained chan struct{}
	relayed chan struct{}
}

// StartSpool starts draining stdin into an unlinked file in dir.
func StartSpool(stdin *os.File, dir string) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, "stdin-*.spool")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(file.Name())
	src, restore, err := dupForDrain(stdin)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	s := &Spool{src: src, restore: restore, file: file, drained: make(chan struct{}), relayed: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	go s.drain()
	return s, nil
}

func (s *Spool) drain() {
	defer close(s.drained)
	buf := make([]byte, 64<<10)
	for {
		n, err := s.src.Read(buf)
		if n > 0 {
			s.mu.Lock()
			if _, werr := s.file.WriteAt(buf[:n], s.size); werr != nil {
				s.err, s.eof = werr, true
				s.cond.Broadcast()
				s.mu.Unlock()
				return
			}
			s.size += int64(n)
			s.cond.Broadcast()
			s.mu.Unlock()
		}
		if err != nil {
			s.mu.Lock()
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				s.err = err
			}
			s.eof = true
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
	}
}

// Failed returns the error that stopped the drain, nil while it runs or once it reached
// the end of stdin.
func (s *Spool) Failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// HandOff puts a pipe in stdin's place and starts replaying into it.
func (s *Spool) HandOff(stdin *os.File) error {
	w, err := installRelay(stdin)
	if err != nil {
		return err
	}
	go s.relay(w)
	return nil
}

func (s *Spool) relay(w *os.File) {
	defer close(s.relayed)
	defer w.Close()
	buf := make([]byte, 64<<10)
	var off int64
	for {
		s.mu.Lock()
		for off == s.size && !s.eof {
			s.cond.Wait()
		}
		avail, eof := s.size-off, s.eof
		s.mu.Unlock()
		if avail == 0 && eof {
			return
		}
		n, err := s.file.ReadAt(buf[:min(int64(len(buf)), avail)], off)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				// The reader closed stdin. The drain carries on regardless, so the
				// upstream still never blocks.
				return
			}
			off += int64(n)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return
		}
	}
}

// Abandon stops the drain without replaying, and puts stdin back as it was.
func (s *Spool) Abandon() {
	_ = s.src.Close()
	<-s.drained
	_ = s.restore()
	_ = s.file.Close()
}

// Wait blocks until stdin has been drained to EOF and every byte replayed. Tests use it;
// a real run's relay lives as long as its stdin does.
func (s *Spool) Wait() {
	<-s.drained
	<-s.relayed
	_ = s.file.Close()
}
