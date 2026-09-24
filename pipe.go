package magus

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
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/file/record"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/internal/sys/pipepeer"
	"github.com/egladman/magus/types"
)

// ProcessStdio hands a run the standard streams of the process it runs in. Only a caller
// running the invocation in its own process should pass it: a server serving many
// invocations has no stdin that belongs to any one of them.
//
// With it, a run whose stdin is a pipe written by another magus process (proven from the
// kernel, never inferred) takes no project lock while that upstream still holds, or has
// yet to settle, a project this run needs. The pipe is drained meanwhile, so the upstream
// never blocks on it, and Stdin reads the same bytes afterwards. A run whose stdout is a
// pipe publishes the projects it holds, which is what lets the stage after it proceed at
// once when they do not overlap.
type ProcessStdio struct {
	Stdin  *os.File
	Stdout *os.File
	// TakesLocks reports whether a magus invoked with argv (argv[0] first) may take
	// project locks. An upstream it answers false for never delays this run. nil counts
	// every upstream magus as one that may.
	TakesLocks func(argv []string) bool
}

// WithProcessStdio gives the run its process's standard streams. See ProcessStdio.
func WithProcessStdio(s ProcessStdio) RunOption { return func(o *run) { o.stdio = &s } }

func withStdio(s *ProcessStdio) lockerOption { return func(l *projectLocker) { l.stdio = s } }

const (
	pipeDirName    = ".pipe"
	holdsSuffix    = ".holds"
	pipeWaitSuffix = ".wait"
	// pipeWalkDepth bounds the walk back through upstream magus stages; a pipeline
	// deeper than this is not one anybody types.
	pipeWalkDepth = 16
	// pipeWaitQuietFor is how long a wait on an upstream that has not yet settled its
	// lock set goes unannounced.
	pipeWaitQuietFor = time.Second
)

// holdsRecord is a run's complete lock set, published once every lock is taken. The
// stage downstream of it reads it to learn which projects it will not get.
//
// It is paired with a flock the publisher holds for as long as the locks, so a record
// left by a killed publisher reads as absent rather than as a live claim.
type holdsRecord struct {
	PID      int    `record:"pid"`
	Inv      string `record:"invocation,omitempty"`
	Projects string `record:"projects"` // newline-separated, "." for the root
}

// pipeWaitRecord names a run waiting on its upstream, for `magus status`.
type pipeWaitRecord struct {
	PID             int       `record:"pid"`
	Command         string    `record:"command"`
	Dir             string    `record:"dir"`
	UpstreamPID     int       `record:"upstream_pid"`
	UpstreamCommand string    `record:"upstream_command,omitempty"`
	Started         time.Time `record:"started"`
}

// upstreamStage is one magus process upstream of this run in a shell pipe.
type upstreamStage struct {
	pid int
	// writes is the pipe this stage writes, which the stage after it reads.
	writes     pipepeer.Pipe
	argv       []string
	takesLocks bool
}

func (u upstreamStage) command() string { return strings.Join(u.argv, " ") }

var holdsSeq atomic.Int64

func (l *projectLocker) pipeDir() string { return filepath.Join(l.dir, pipeDirName) }

// publishHolds records paths as this invocation's complete lock set, when its stdout is
// a pipe another stage may be reading, and returns the retraction. Best-effort: a stage
// downstream that cannot read the record waits for this one to finish instead.
func (l *projectLocker) publishHolds(ctx context.Context, paths []string) func() {
	if l.stdio == nil || !isPipe(l.stdio.Stdout) {
		return func() {}
	}
	dir := l.pipeDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return func() {}
	}
	base := filepath.Join(dir, fmt.Sprintf("%d-%d%s", os.Getpid(), holdsSeq.Add(1), holdsSuffix))
	fl := flock.New(base + ".lock")
	if got, err := fl.TryLock(); err != nil || !got {
		return func() {}
	}
	rec := holdsRecord{PID: os.Getpid(), Inv: journal.InvocationIDFromContext(ctx), Projects: strings.Join(canonicalProjects(paths), "\n")}
	if record.Write(base, rec) != nil {
		_ = fl.Unlock()
		_ = os.Remove(base + ".lock")
		return func() {}
	}
	// The record goes before the flock, so no reader ever sees a live publisher's record
	// with its flock free.
	return func() {
		_ = record.Remove(base)
		_ = fl.Unlock()
		_ = os.Remove(base + ".lock")
	}
}

// settledProjects returns the projects pid has published as its complete lock set, and
// false when it has published none that is live: it has not finished taking its locks,
// has released them, or never takes any. A dead publisher's record is swept.
func (l *projectLocker) settledProjects(procID int) ([]string, bool) {
	matches, _ := filepath.Glob(filepath.Join(l.pipeDir(), strconv.Itoa(procID)+"-*"+holdsSuffix))
	var out []string
	settled := false
	for _, path := range matches {
		var rec holdsRecord
		if record.Read(path, &rec) != nil || rec.PID != procID {
			continue
		}
		if !lockIsHeld(path + ".lock") {
			_ = record.Remove(path)
			_ = os.Remove(path + ".lock")
			continue
		}
		settled = true
		out = append(out, strings.Split(rec.Projects, "\n")...)
	}
	return out, settled
}

// awaitUpstream holds this run back, before it takes any lock, while a magus upstream of
// it in a shell pipe holds or may yet take a project in paths. See ProcessStdio.
//
// It returns the relay now feeding stdin, or nil when it did not wait, and every proven
// upstream stage. It never waits on an unrelated process: every upstream is proven from
// the kernel to write this run's stdin, directly or through other magus stages, and to
// run this same executable.
func (l *projectLocker) awaitUpstream(ctx context.Context, paths []string) (*stdinSpool, []upstreamStage, error) {
	if l.stdio == nil || l.stdio.Stdin == nil {
		return nil, nil, nil
	}
	in, err := pipepeer.ReadEnd(os.Getpid(), int(l.stdio.Stdin.Fd()))
	if err != nil {
		return nil, nil, nil //nolint:nilerr // not a pipe, or no proof on this platform: nothing to wait on
	}
	ups, err := l.upstream(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	blocking := l.blocking(ups, paths)
	if len(blocking) == 0 {
		return nil, ups, nil
	}
	sp, err := l.waitOut(ctx, ups, blocking, paths)
	return sp, ups, err
}

// waitOut drains stdin while any upstream stage still blocks paths, starting from
// blocking, then relays it.
func (l *projectLocker) waitOut(ctx context.Context, ups []upstreamStage, blocking []upstreamBlock, paths []string) (*stdinSpool, error) {
	sp, err := startSpool(l.stdio.Stdin, l.pipeDir())
	if err != nil {
		return nil, fmt.Errorf("workspace lock: drain stdin while waiting on pid %d: %w", blocking[0].stage.pid, err)
	}
	retract := func() {}
	defer func() { retract() }()
	announced := false
	start := time.Now()

	t := time.NewTicker(lockPollEvery)
	defer t.Stop()
	for len(blocking) > 0 {
		// An upstream that has yet to settle usually does so within a workspace load, and
		// a line for every streaming pipe would be noise. A real conflict, or a wait that
		// drags, is said at once.
		if !announced && (blocking[0].conflict || time.Since(start) >= pipeWaitQuietFor) {
			l.emitPipeWait(ctx, blocking[0].stage)
			retract = l.writePipeWait(ctx, blocking[0].stage)
			announced = true
		}
		select {
		case <-ctx.Done():
			sp.abandon()
			return nil, fmt.Errorf("workspace lock: gave up waiting on pid %d upstream of this run: %w", blocking[0].stage.pid, ctx.Err())
		case <-t.C:
		}
		if err := sp.failed(); err != nil {
			sp.abandon()
			return nil, fmt.Errorf("workspace lock: drain stdin while waiting on pid %d: %w", blocking[0].stage.pid, err)
		}
		blocking = l.blocking(ups, paths)
	}
	if err := sp.handOff(l.stdio.Stdin); err != nil {
		sp.abandon()
		return nil, fmt.Errorf("workspace lock: restore stdin: %w", err)
	}
	return sp, nil
}

// upstream walks back from the pipe this run reads, through every process writing it,
// to the processes writing theirs, and returns the magus stages among them. A stage that
// is not this executable (cat, tee, jq) is walked through but never weighed: only its
// pipes are read, never its arguments. The walk skips this run's own ancestors, which
// write its stdin by design (magus.run's stdin option) and wait for it, so they are
// never waited on; that also keeps it out of the shell and harness that launched it.
func (l *projectLocker) upstream(ctx context.Context, in pipepeer.Pipe) ([]upstreamStage, error) {
	self := os.Getpid()
	ancestors := ancestorPIDs(ctx, self)
	seen := map[int]bool{self: true}
	var out []upstreamStage
	frontier := []pipepeer.Pipe{in}
	for depth := 0; depth < pipeWalkDepth && len(frontier) > 0; depth++ {
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
						"this run's standard input is written, through magus pid %s, by this run itself; each stage would wait on the one before it, so it is refused instead."+
							" Break the loop so one stage's input does not depend on its own output.", joinPIDs(out))
				}
				if seen[w] || ancestors[w] {
					continue
				}
				seen[w] = true
				if pipepeer.SameExecutable(w) {
					argv, aerr := pipepeer.Args(w)
					takes := aerr != nil || l.stdio.TakesLocks == nil || l.stdio.TakesLocks(argv)
					out = append(out, upstreamStage{pid: w, writes: p, argv: argv, takesLocks: takes})
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

// upstreamBlock is an upstream stage this run still waits for. conflict is set when the
// stage holds a project this run needs, rather than having yet to settle its set.
type upstreamBlock struct {
	stage    upstreamStage
	conflict bool
}

// blocking returns the upstream stages this run must still wait for, conflicts first:
// those that may take locks, still write their pipe, and have either not settled a lock
// set yet or settled one that overlaps paths. A stage whose settled set is disjoint
// streams alongside.
func (l *projectLocker) blocking(ups []upstreamStage, paths []string) []upstreamBlock {
	want := canonicalProjects(paths)
	var out []upstreamBlock
	for _, u := range ups {
		if !u.takesLocks || !u.writes.WrittenBy(u.pid) {
			continue
		}
		held, settled := l.settledProjects(u.pid)
		conflict := slices.ContainsFunc(held, func(p string) bool { return slices.Contains(want, p) })
		if settled && !conflict {
			continue
		}
		out = append(out, upstreamBlock{stage: u, conflict: conflict})
	}
	slices.SortStableFunc(out, func(a, b upstreamBlock) int {
		switch {
		case a.conflict == b.conflict:
			return 0
		case a.conflict:
			return -1
		default:
			return 1
		}
	})
	return out
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

func joinPIDs(ups []upstreamStage) string {
	ids := make([]string, len(ups))
	for i, u := range ups {
		ids[i] = strconv.Itoa(u.pid)
	}
	return strings.Join(ids, ", ")
}

// canonicalProjects spells the root one way, so two runs compare project sets exactly.
func canonicalProjects(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			p = "."
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func isPipe(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeNamedPipe != 0
}

// emitPipeWait states the decision, once: a run that sits silently before taking a lock
// looks hung.
func (l *projectLocker) emitPipeWait(ctx context.Context, up upstreamStage) {
	if l.rw != nil {
		_ = report.Record(l.rw, report.LockPipeWait{UpstreamPID: up.pid, Command: up.command()})
		return
	}
	fmt.Fprintf(l.out, "magus: waiting for pid %d (%s), upstream of this run in a pipe, to finish with the projects this run needs before taking their locks.\n",
		up.pid, up.command())
	slog.InfoContext(ctx, "lock.pipe_wait", slog.Int("upstream_pid", up.pid), slog.String("upstream_command", up.command()))
}

// writePipeWait publishes the wait for `magus status` and returns its retraction.
func (l *projectLocker) writePipeWait(ctx context.Context, up upstreamStage) func() {
	dir := l.pipeDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return func() {}
	}
	self := l.selfRecord(ctx, time.Now())
	path := filepath.Join(dir, strconv.Itoa(self.PID)+pipeWaitSuffix)
	rec := pipeWaitRecord{
		PID: self.PID, Command: self.Command, Dir: self.Dir,
		UpstreamPID: up.pid, UpstreamCommand: up.command(), Started: self.Started,
	}
	if record.Write(path, rec) != nil {
		return func() {}
	}
	return func() { _ = record.Remove(path) }
}

// PipeWaits reports every run in this workspace waiting, before taking its locks, on the
// magus upstream of it in a pipe. A record whose waiter has exited is swept.
func (m *Magus) PipeWaits() []types.StatusPipeWait {
	return pipeWaits(resolveCacheDir(m.ws.Root, m.cfg), m.ws.Root)
}

func pipeWaits(cacheDir, workspaceRoot string) []types.StatusPipeWait {
	dir := filepath.Join(cacheDir, locksDirName, workspaceLockKey(workspaceRoot), pipeDirName)
	matches, _ := filepath.Glob(filepath.Join(dir, "*"+pipeWaitSuffix))
	var out []types.StatusPipeWait
	for _, path := range matches {
		var rec pipeWaitRecord
		if record.Read(path, &rec) != nil || rec.PID == 0 {
			continue
		}
		if !pid.Alive(rec.PID) {
			_ = record.Remove(path)
			continue
		}
		out = append(out, types.StatusPipeWait{
			PID: rec.PID, Command: rec.Command, Dir: rec.Dir,
			UpstreamPID: rec.UpstreamPID, UpstreamCommand: rec.UpstreamCommand, WaitTime: rec.Started,
		})
	}
	slices.SortFunc(out, func(a, b types.StatusPipeWait) int { return a.PID - b.PID })
	return out
}

// stdinSpool drains a waiting run's stdin into an unlinked file, so the upstream writing
// it never blocks on a full pipe, and then replays it: the spooled bytes first, then
// whatever arrives after, through a pipe put in stdin's place.
type stdinSpool struct {
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

func startSpool(stdin *os.File, dir string) (*stdinSpool, error) {
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
	s := &stdinSpool{src: src, restore: restore, file: file, drained: make(chan struct{}), relayed: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	go s.drain()
	return s, nil
}

func (s *stdinSpool) drain() {
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

func (s *stdinSpool) failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// handOff puts a pipe in stdin's place and starts replaying into it.
func (s *stdinSpool) handOff(stdin *os.File) error {
	w, err := installRelay(stdin)
	if err != nil {
		return err
	}
	go s.relay(w)
	return nil
}

func (s *stdinSpool) relay(w *os.File) {
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

// abandon stops the drain without replaying, and puts stdin back as it was.
func (s *stdinSpool) abandon() {
	_ = s.src.Close()
	<-s.drained
	_ = s.restore()
	_ = s.file.Close()
}

// wait blocks until stdin has been drained to EOF and every byte replayed. Tests use it;
// a real run's relay lives as long as its stdin does.
func (s *stdinSpool) wait() {
	<-s.drained
	<-s.relayed
	_ = s.file.Close()
}
