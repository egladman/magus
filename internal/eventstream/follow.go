package eventstream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/egladman/magus/internal/journal"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// runLogExt is the suffix of one invocation's union event log.
const runLogExt = ".jsonl"

// Follower turns the workspace's run-log directory into a live event stream.
//
// The directory IS the bus, and that is the design rather than a fallback. Every
// magus process in a workspace already appends its invocation to
// <cacheDir>/runs/<inv>.jsonl, so a follower reading that directory sees runs
// started from any terminal, any editor, and the daemon alike - with no daemon
// required, no socket to discover, and no token to provision. A subscriber that
// wants lower latency than a poll can take the daemon socket instead; it learns
// the same events.
//
// Two properties bound what a follower sees, and both are deliberate:
//
//   - Output lines lag. journal.FileHandler flushes every kind EXCEPT output, so
//     lifecycle and result events land immediately while output arrives when a
//     bufio page fills or the run ends. A subscriber asking for output is opting
//     into chunked delivery.
//   - A partially written line is never emitted. An output-triggered page flush
//     can split a line, so reads stop at the last newline and resume there.
//
// A Follower is not safe for concurrent use; one goroutine drives it.
type Follower struct {
	dir       string
	workspace string
	offsets   map[string]int64
}

// NewFollower returns a Follower over runsDir, stamping workspace on every event
// it produces. It touches no files until Replay or Follow is called, so a
// workspace that has never run anything is not an error.
func NewFollower(runsDir, workspace string) *Follower {
	return &Follower{dir: runsDir, workspace: workspace, offsets: map[string]int64{}}
}

// Replay emits the events already on disk from the newest limit invocations,
// oldest event first, and leaves the Follower positioned at the end of each file
// so a subsequent Follow does not repeat them.
//
// A limit of 0 or less replays every retained invocation. Ordering is by file
// modification time rather than by the timestamps inside, because an invocation
// still running has no final timestamp to sort on.
func (f *Follower) Replay(limit int, emit func(types.StreamEvent) error) error {
	logs, err := f.logs()
	if err != nil {
		return err
	}
	if limit > 0 && len(logs) > limit {
		// Position past the logs the window excludes. Follow re-lists the whole
		// directory, so a log left at offset zero is drained in full on the first
		// tick and the replay window means nothing.
		f.skip(logs[:len(logs)-limit])
		logs = logs[len(logs)-limit:]
	}
	for _, name := range logs {
		if err := f.drain(name, emit); err != nil {
			return err
		}
	}
	return nil
}

// Skip positions the Follower at the end of every log currently on disk without
// emitting anything, so Follow reports only what happens next.
//
// This is what `--follow` without a replay window needs: attaching to a workspace
// with months of retained runs must not deliver months of history first.
func (f *Follower) Skip() error {
	logs, err := f.logs()
	if err != nil {
		return err
	}
	f.skip(logs)
	return nil
}

// skip positions the Follower past the current end of each named log. A log it
// cannot stat is left unpositioned, so it replays whole rather than being lost.
func (f *Follower) skip(names []string) {
	for _, name := range names {
		info, err := os.Stat(filepath.Join(f.dir, name))
		if err != nil {
			continue
		}
		f.offsets[name] = info.Size()
	}
}

// Follow polls the run-log directory every interval and emits what appears,
// blocking until ctx is cancelled. It returns nil on cancellation: a follower
// stopping because it was asked to is not a failure.
//
// An emit error stops the follow and is returned - that is the subscriber's pipe
// closing, and continuing to read a directory nobody is listening to is waste. A
// read error on one log is skipped rather than fatal, because a log being written
// concurrently is the normal case.
func (f *Follower) Follow(ctx context.Context, interval time.Duration, emit func(types.StreamEvent) error) error {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			logs, err := f.logs()
			if err != nil {
				continue
			}
			for _, name := range logs {
				if err := f.drain(name, emit); err != nil {
					return err
				}
			}
		}
	}
}

// logs lists the run logs oldest-modified first. A missing directory is an empty
// list, not an error: a workspace that has never been run is a normal state, and
// a follower attached to one should wait rather than exit.
func (f *Follower) logs() ([]string, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	type stamped struct {
		name string
		mod  time.Time
	}
	var found []stamped
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != runLogExt {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, stamped{e.Name(), info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.Before(found[j].mod) })
	names := make([]string, len(found))
	for i, s := range found {
		names[i] = s.name
	}
	return names, nil
}

// drain reads whatever is new in one log and emits it, advancing the stored
// offset to the last COMPLETE line. Stopping short of a partial line is what
// makes a concurrent writer safe to read.
func (f *Follower) drain(name string, emit func(types.StreamEvent) error) error {
	path := filepath.Join(f.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	from := f.offsets[name]
	if info.Size() <= from {
		// A shrinking file means the id was reused after a cache clean; restart it
		// rather than seeking past the end and reading nothing forever.
		if info.Size() < from {
			f.offsets[name] = 0
		}
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(from, io.SeekStart); err != nil {
		return nil
	}
	chunk := make([]byte, info.Size()-from)
	n, err := io.ReadFull(file, chunk)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil
	}
	chunk = chunk[:n]
	end := bytes.LastIndexByte(chunk, '\n')
	if end < 0 {
		return nil
	}
	f.offsets[name] = from + int64(end) + 1
	for _, line := range bytes.Split(chunk[:end], []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		// A torn line is a normal transient, not corruption: the log is read while
		// it is written. Skip it and pick the rest up on the next poll.
		var je journal.Event
		if err := json.Unmarshal(line, &je); err != nil {
			continue
		}
		se, ok := FromJournal(f.workspace, je)
		if !ok {
			continue
		}
		if err := emit(se); err != nil {
			return err
		}
	}
	return nil
}
