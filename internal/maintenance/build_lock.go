package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

const (
	graphBuildLockFile   = "graph-build.lock"
	graphBuildHolderFile = "graph-build.holder.json"
)

// GraphBuildHolder names a graph build that holds, or held, a knowledge store's build
// lock.
type GraphBuildHolder struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	// By names the build for a reader: "`magus graph build`" for a manual one, "the
	// server's sync-graph job" for one the server runs inside its own process.
	By string `json:"by"`
	// Store is the knowledge store the work writes, empty when none. A build
	// queued behind this holder loads from it only when the store is its own.
	Store string `json:"store,omitempty"`
}

// machineGraphBuildDir holds this machine's one build lock, taken by every
// graph build, symbol refresh and background symbol index. Take it before a
// store's lock, so two builds never each hold one and wait on the other.
func machineGraphBuildDir() (string, error) {
	state, err := xdg.StateDir()
	if err != nil {
		return "", fmt.Errorf("maintenance: machine graph build lock: %w", err)
	}
	return filepath.Join(state, "magus", "graph-build"), nil
}

// acquireMachineGraphBuild is [acquireGraphBuild] on the machine's build lock.
func acquireMachineGraphBuild(ctx context.Context, me GraphBuildHolder, w io.Writer, every time.Duration) (*GraphBuildLock, *GraphBuildHolder, error) {
	dir, err := machineGraphBuildDir()
	if err != nil {
		return nil, nil, err
	}
	return acquireGraphBuild(ctx, dir, me, w, every)
}

// TryMachineGraphBuild is [tryGraphBuild] on the machine's build lock.
func TryMachineGraphBuild(me GraphBuildHolder, w io.Writer) (*GraphBuildLock, *GraphBuildHolder, error) {
	dir, err := machineGraphBuildDir()
	if err != nil {
		return nil, nil, err
	}
	return tryGraphBuild(dir, me, w)
}

// RunningMachineGraphBuild is [RunningGraphBuild] on the machine's build lock.
// The error says the lock's place could not be resolved.
func RunningMachineGraphBuild() (GraphBuildHolder, bool, error) {
	dir, err := machineGraphBuildDir()
	if err != nil {
		return GraphBuildHolder{}, false, err
	}
	holder, running := RunningGraphBuild(dir)
	return holder, running, nil
}

// ReindexSymbols runs reindex for `graph build` and returns how many symbol indexes
// it refreshed. A failing or missing indexer is a hint, not an error: the domain graph
// rebuilds regardless. The error is non-nil only for the [reindexRefusals] part, worded
// for the rebuild that goes on over the stale index.
func ReindexSymbols(ctx context.Context, reindex func(context.Context) (int, error)) (int, error) {
	n, err := reindex(ctx)
	slog.InfoContext(ctx, fmt.Sprintf("reindexed %d symbol index(es)", n), attr.Notice(""))
	if err == nil {
		return n, nil
	}
	interactive.Hint(ctx, "some projects were not reindexed", attr.Error(err))
	if refused := reindexRefusals(err); refused != nil {
		return n, fmt.Errorf("magus graph build: the graph was rebuilt over an index this run was refused the lock or the build budget to refresh: %w", refused)
	}
	return n, nil
}

// reindexRefusals is the part of a reindex failure that fails `graph build`:
// each project refused for the outer run's lock (MGS3007), a full build budget
// (MGS3009) or memory pressure (MGS3053). The index it wanted stayed stale,
// and an exit 0 under magus\cmd's quiet hid that. A missing indexer stays a
// warning. Nil when nothing was refused.
func reindexRefusals(err error) error {
	errs := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	}
	var refused []error
	for _, e := range errs {
		if errors.Is(e, types.ProjectLockHeldByAncestor) || errors.Is(e, types.MachineBudgetExhausted) || errors.Is(e, types.MemoryPressureHigh) {
			refused = append(refused, e)
		}
	}
	return errors.Join(refused...)
}

func (h GraphBuildHolder) String() string {
	if h.PID == 0 {
		return "another graph build (it has not recorded itself yet)"
	}
	return fmt.Sprintf("%s (pid %d, started %s)", h.By, h.PID, stamp(h.Started))
}

// GraphBuildLock is a held knowledge store build lock. Release it exactly once.
type GraphBuildLock struct {
	fl         *flock.Flock
	holderPath string
}

// acquireGraphBuild takes the build lock of the knowledge store in dir for me, waiting as
// long as another build holds it. Every graph build takes it, manual or server, so two
// builds never write one store and one set of SCIP shards at once.
//
// Before it waits it writes to w whose build it is waiting on, and it returns that
// holder, so the caller can load what the holder stored instead of repeating its work.
// waitedOn is nil when the lock was free. A holder record left by a build that ended
// without releasing (killed, or its process died) is taken over with a notice to w; the
// lock itself is an OS file lock, which the kernel drops when its holder exits, so only
// the record can be stale. Two acquisitions in one process exclude each other, which is
// what serializes a manual build against the server's in-process job when both run in one
// process, and lets a test stand in for two processes.
//
// It returns ctx's error when ctx ends first; the other build carries on.
func acquireGraphBuild(ctx context.Context, dir string, me GraphBuildHolder, w io.Writer, every time.Duration) (_ *GraphBuildLock, waitedOn *GraphBuildHolder, err error) {
	lock, h, err := tryGraphBuild(dir, me, w)
	if err != nil || h == nil {
		return lock, nil, err
	}
	fmt.Fprintf(w, "magus graph build: waiting for %s; interrupting stops the wait, not that work\n", h)
	fl := flock.New(filepath.Join(dir, graphBuildLockFile))
	if got, err := fl.TryLockContext(ctx, every); err != nil || !got {
		if ctx.Err() != nil {
			return nil, nil, fmt.Errorf("maintenance: stopped waiting for %s: %w", h, ctx.Err())
		}
		return nil, nil, fmt.Errorf("maintenance: graph build lock %s: %w", fl.Path(), err)
	}
	lock, err = recordGraphBuild(fl, filepath.Join(dir, graphBuildHolderFile), me, w)
	return lock, h, err
}

// tryGraphBuild is acquireGraphBuild without the wait: when another holds
// the lock it returns that holder and no lock.
func tryGraphBuild(dir string, me GraphBuildHolder, w io.Writer) (_ *GraphBuildLock, holder *GraphBuildHolder, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("maintenance: graph build lock: %w", err)
	}
	fl := flock.New(filepath.Join(dir, graphBuildLockFile))
	got, err := fl.TryLock()
	if err != nil {
		return nil, nil, fmt.Errorf("maintenance: graph build lock %s: %w", fl.Path(), err)
	}
	holderPath := filepath.Join(dir, graphBuildHolderFile)
	if !got {
		h, _, err := readGraphBuildHolder(holderPath)
		if err != nil {
			return nil, nil, err
		}
		if !pid.Alive(h.PID) {
			h = GraphBuildHolder{} // a dead build's record, not yet replaced
		}
		return nil, &h, nil
	}
	lock, err := recordGraphBuild(fl, holderPath, me, w)
	return lock, nil, err
}

// recordGraphBuild writes me as the holder of the lock fl holds.
func recordGraphBuild(fl *flock.Flock, holderPath string, me GraphBuildHolder, w io.Writer) (*GraphBuildLock, error) {
	if prev, ok, err := readGraphBuildHolder(holderPath); err != nil {
		return nil, errors.Join(err, fl.Unlock())
	} else if ok {
		fmt.Fprintf(w, "magus graph build: took over the build lock from %s, which ended without releasing it\n", prev)
	}
	data, err := codec.Marshal(me)
	if err == nil {
		err = file.ReplaceFile(holderPath, data, 0o600)
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("maintenance: record graph build holder: %w", err), fl.Unlock())
	}
	return &GraphBuildLock{fl: fl, holderPath: holderPath}, nil
}

// Release clears the holder record and drops the lock, in that order, so a waiter that
// wakes never reads the finished build as its predecessor.
func (l *GraphBuildLock) Release() error {
	var errs []error
	if err := os.Remove(l.holderPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("maintenance: clear graph build holder: %w", err))
	}
	if err := l.fl.Unlock(); err != nil {
		errs = append(errs, fmt.Errorf("maintenance: release graph build lock: %w", err))
	}
	return errors.Join(errs...)
}

// RunningGraphBuild returns the build holding the lock of the knowledge store in dir, ok
// false when none is. It reads the holder record and checks that its process is alive
// without touching the lock, so a diagnosis never makes a starting build wait.
func RunningGraphBuild(dir string) (GraphBuildHolder, bool) {
	h, ok, err := readGraphBuildHolder(filepath.Join(dir, graphBuildHolderFile))
	if err != nil || !ok || !pid.Alive(h.PID) {
		return GraphBuildHolder{}, false
	}
	return h, true
}

// readGraphBuildHolder reads a holder record, ok false when there is none. An empty
// record is a holder that took the lock and has not written itself yet.
func readGraphBuildHolder(path string) (h GraphBuildHolder, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return GraphBuildHolder{}, false, nil
	}
	if err != nil {
		return GraphBuildHolder{}, false, fmt.Errorf("maintenance: read graph build holder: %w", err)
	}
	if len(data) == 0 {
		return GraphBuildHolder{}, true, nil
	}
	if err := codec.Unmarshal(data, &h); err != nil {
		return GraphBuildHolder{}, false, fmt.Errorf("maintenance: %s: %w", path, err)
	}
	return h, true, nil
}

const (
	// GraphBuildPoll is how often a waiting graph build retries.
	GraphBuildPoll = 250 * time.Millisecond
	// GraphBuildMemoryMB is a build's own process while it assembles the graph. Measured
	// 2026-10-31 here: 1.83 and 2.13 GiB resident, 2.28 GiB in that day's memory panic.
	GraphBuildMemoryMB = 2400
)

// AcquireGraphBuilds takes the machine's build lock, then the store's at dir, waiting
// on each and saying so to notices. waitedOn is the build whose work this one can load.
func AcquireGraphBuilds(ctx context.Context, wsRoot, dir string, notices io.Writer) (release func() error, waitedOn *GraphBuildHolder, err error) {
	me := GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "`magus graph build` in " + wsRoot, Store: dir}
	if proc.IsJob(ctx) {
		me.By = "the server's sync-graph job in " + wsRoot
	}
	machine, behind, err := acquireMachineGraphBuild(ctx, me, notices, GraphBuildPoll)
	if err != nil {
		return nil, nil, fmt.Errorf("magus graph build: %w", err)
	}
	store, waitedOn, err := acquireGraphBuild(ctx, dir, me, notices, GraphBuildPoll)
	if err != nil {
		return nil, nil, errors.Join(err, machine.Release())
	}
	// The build ahead released this store first, so its lock read free.
	if waitedOn == nil && behind != nil && behind.Store == dir {
		waitedOn = behind
	}
	return func() error { return errors.Join(store.Release(), machine.Release()) }, waitedOn, nil
}
