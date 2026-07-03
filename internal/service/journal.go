package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egladman/magus/types"
)

// sweepStopTimeout bounds each stop command replayed during a startup sweep so one
// wedged stop cannot stall the whole reap.
const sweepStopTimeout = 15 * time.Second

// Journal persists which services a daemon has started so a NEW daemon can reap
// orphans left by a previous one that died without a graceful shutdown (SIGKILL,
// power loss - the case ordinary teardown misses). Each hosted service is one file
// recording its stop command; [Journal.Sweep], run at daemon startup, replays those
// stop commands to shut down any survivors.
//
// It is deliberately stop-command-based rather than PID-based: a `docker run` client
// PID is not the container, and a raw PID may have been reused by an unrelated
// process by the time a new daemon starts, so killing it is unsafe. A service's own
// stop command (e.g. `docker stop <name>`) is tool-aware and idempotent. Services
// with no stop command cannot be reaped safely after a restart and are only counted,
// not force-killed. This is a daemon-only concern; the in-process (per-run) Registry
// has no Journal.
type Journal struct {
	dir string
}

type journalEntry struct {
	Key  string        `json:"key"`
	Stop types.Command `json:"stop,omitempty"`
}

// NewJournal returns a Journal writing under dir (created if absent).
func NewJournal(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Journal{dir: dir}, nil
}

func (j *Journal) path(key string) string { return filepath.Join(j.dir, key+".json") }

// record notes that the service for key is running, with the command that stops it.
// A nil Journal (the in-process Registry) is a no-op.
func (j *Journal) record(key string, stop types.Command) {
	if j == nil {
		return
	}
	data, err := json.Marshal(journalEntry{Key: key, Stop: stop})
	if err != nil {
		return
	}
	_ = os.WriteFile(j.path(key), data, 0o600)
}

// forget drops the record for key once the service has been stopped cleanly.
func (j *Journal) forget(key string) {
	if j == nil {
		return
	}
	_ = os.Remove(j.path(key))
}

// SweepResult reports what a startup sweep did.
type SweepResult struct {
	Reaped     int // services shut down by replaying their stop command
	Unreapable int // records with no stop command (left running; cannot reap safely)
}

// Sweep reaps orphaned services recorded by a previous daemon: it runs each recorded
// stop command, then clears every record (they all belong to the dead daemon). Call
// once at daemon startup, before hosting anything. A nil Journal returns a zero
// result.
func (j *Journal) Sweep(ctx context.Context) SweepResult {
	var res SweepResult
	if j == nil {
		return res
	}
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return res
	}
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		path := filepath.Join(j.dir, de.Name())
		e, ok := readEntry(path)
		if !ok {
			_ = os.Remove(path) // unreadable/corrupt record; drop it
			continue
		}
		if e.Stop.Bin == "" {
			res.Unreapable++
			_ = os.Remove(path)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, sweepStopTimeout)
		_ = exec.CommandContext(cctx, e.Stop.Bin, e.Stop.Args...).Run()
		cancel()
		_ = os.Remove(path)
		res.Reaped++
	}
	return res
}

func readEntry(path string) (journalEntry, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return journalEntry{}, false
	}
	var e journalEntry
	if json.Unmarshal(data, &e) != nil {
		return journalEntry{}, false
	}
	return e, true
}
