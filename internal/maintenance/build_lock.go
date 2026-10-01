package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sys/pid"
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

// AcquireGraphBuild takes the build lock of the knowledge store in dir for me, waiting as
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
func AcquireGraphBuild(ctx context.Context, dir string, me GraphBuildHolder, w io.Writer, every time.Duration) (_ *GraphBuildLock, waitedOn *GraphBuildHolder, err error) {
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
			h = GraphBuildHolder{} // a dead build's record, which the live holder has yet to replace
		}
		waitedOn = &h
		fmt.Fprintf(w, "magus graph build: waiting for %s, which is building this workspace's graph; interrupting stops the wait, not that build\n", h)
		if got, err = fl.TryLockContext(ctx, every); err != nil || !got {
			if ctx.Err() != nil {
				return nil, nil, fmt.Errorf("maintenance: stopped waiting for %s: %w", h, ctx.Err())
			}
			return nil, nil, fmt.Errorf("maintenance: graph build lock %s: %w", fl.Path(), err)
		}
	}
	if prev, ok, err := readGraphBuildHolder(holderPath); err != nil {
		return nil, nil, errors.Join(err, fl.Unlock())
	} else if ok {
		fmt.Fprintf(w, "magus graph build: took over the build lock from %s, which ended without releasing it\n", prev)
	}
	data, err := json.Marshal(me)
	if err == nil {
		err = file.ReplaceFile(holderPath, data, 0o600)
	}
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("maintenance: record graph build holder: %w", err), fl.Unlock())
	}
	return &GraphBuildLock{fl: fl, holderPath: holderPath}, waitedOn, nil
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
	if err := json.Unmarshal(data, &h); err != nil {
		return GraphBuildHolder{}, false, fmt.Errorf("maintenance: %s: %w", path, err)
	}
	return h, true, nil
}
