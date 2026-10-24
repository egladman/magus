package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// heartbeatInterval is how often a (session, lease) pair is stamped at most. A stamp
// is a lock, a read and an append, which a judgment that runs on every tool call cannot
// afford each time; a minute is far finer than the silence the report measures.
const heartbeatInterval = time.Minute

// SilentLeaseAfter is how long a lease's session must go without a judged tool call before
// `magus status` reports the lease as silent. A session waiting on a long bounded foreground command makes no call, so a short figure would report a slow
// worker as dead. It is a starting figure for the shadow measurement to tune. Background
// commands are counted as alive separately, because a session can sit past this while
// one runs.
const SilentLeaseAfter = 30 * time.Minute

// Heartbeat is one stamp: the session was judged on a call under lease at At. The last
// stamp per (session, lease) is the entry.
type Heartbeat struct {
	Session string
	Lease   string
	// Transcript is the host's transcript of the session, which the silent-row report
	// reads for background commands still running. Empty when the host named none.
	Transcript string
	At         time.Time
}

type heartbeatJSON struct {
	Session    string `json:"session"`
	Lease      string `json:"lease"`
	Transcript string `json:"transcript,omitempty"`
	At         int64  `json:"at"`
}

// MarshalJSON writes At as unix seconds, which is all the throttle and the report resolve.
func (h Heartbeat) MarshalJSON() ([]byte, error) {
	return json.Marshal(heartbeatJSON{Session: h.Session, Lease: h.Lease, Transcript: h.Transcript, At: h.At.Unix()})
}

// UnmarshalJSON reads a stamp written by MarshalJSON.
func (h *Heartbeat) UnmarshalJSON(b []byte) error {
	var raw heartbeatJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*h = Heartbeat{Session: raw.Session, Lease: raw.Lease, Transcript: raw.Transcript, At: time.Unix(raw.At, 0)}
	return nil
}

func (h Heartbeat) key() string { return fmt.Sprintf("%s\x00%s", h.Session, h.Lease) }

// heartbeatStore is the stamps of the workspace at root, one file per workspace under the
// user state dir: a stamp is a fact about a live session, which no cache wipe should
// invent or erase, and a lease id is unique only within its workspace.
func heartbeatStore(root string) (file.JSONL[Heartbeat], error) {
	stateBase, err := xdg.StateDir()
	if err != nil {
		return file.JSONL[Heartbeat]{}, fmt.Errorf("resolve the user state dir for the session heartbeat: %w", err)
	}
	sum := sha256.Sum256([]byte(root))
	return file.JSONL[Heartbeat]{
		Path: filepath.Join(stateBase, "magus", "heartbeat", fmt.Sprintf("%s.jsonl", hex.EncodeToString(sum[:8]))),
		Perm: 0o600,
		Key:  Heartbeat.key,
	}, nil
}

// stampHeartbeat records that session was judged on a call under lease at now, at most
// once per [heartbeatInterval] for the pair. It never fails a judgment: a store error is
// logged and the judgment goes on, since the report this feeds is shadow only.
func stampHeartbeat(ctx context.Context, root string, h Heartbeat) {
	if root == "" || h.Session == "" || h.Lease == "" {
		return
	}
	if err := stampHeartbeatAt(ctx, root, h); err != nil {
		slog.With(attr.Component("magus")).WarnContext(ctx, "session heartbeat not stamped", attr.Error(err))
	}
}

func stampHeartbeatAt(ctx context.Context, root string, h Heartbeat) error {
	store, err := heartbeatStore(root)
	if err != nil {
		return err
	}
	// Read without the lock first: the file holds a line or two per live pair, and nearly
	// every call finds its pair stamped inside the interval and goes no further.
	entries, err := store.Load()
	if err == nil && fresh(entries, h) {
		return nil
	}
	if err := store.Update(ctx, func(entries []Heartbeat) []Heartbeat {
		if fresh(entries, h) {
			return nil
		}
		return []Heartbeat{h}
	}); err != nil {
		return err
	}
	if len(entries) < heartbeatMaxEntries {
		return nil
	}
	return compactHeartbeats(ctx, store, h.At)
}

// fresh reports whether entries already hold a stamp for h's pair inside the interval.
func fresh(entries []Heartbeat, h Heartbeat) bool {
	return slices.ContainsFunc(entries, func(e Heartbeat) bool {
		return e.key() == h.key() && h.At.Sub(e.At) < heartbeatInterval
	})
}

// heartbeatMaxEntries bounds the pairs the store keeps. The JSON Lines store already
// rewrites itself to the last line per pair, but every session and lease adds a pair that
// nothing ends, so past this the oldest go.
const heartbeatMaxEntries = 1024

// heartbeatRetain is how old a stamp a compaction keeps whatever the count: a lease silent
// this long was ended by its deadline or a prune long before.
const heartbeatRetain = 7 * 24 * time.Hour

// compactHeartbeats rewrites the store by rename to its last line per pair, dropping each
// stamp older than [heartbeatRetain] before now and then the oldest past
// [heartbeatMaxEntries].
func compactHeartbeats(ctx context.Context, store file.JSONL[Heartbeat], now time.Time) error {
	return file.WithLock(ctx, fmt.Sprintf("%s.lock", store.Path), file.LockWait, func() error {
		entries, err := store.Load()
		if err != nil {
			return err
		}
		kept := slices.DeleteFunc(entries, func(e Heartbeat) bool { return now.Sub(e.At) > heartbeatRetain })
		slices.SortStableFunc(kept, func(a, b Heartbeat) int { return a.At.Compare(b.At) })
		kept = kept[max(0, len(kept)-heartbeatMaxEntries):]
		lines := make([][]byte, 0, len(kept))
		for _, e := range kept {
			line, err := json.Marshal(e)
			if err != nil {
				return err
			}
			lines = append(lines, line)
		}
		return file.RewriteLines(store.Path, lines, store.Perm)
	})
}

// newestStamp is the newest stamp under row's lease in the store of the checkout its holder
// took it in and in workspace's, false when neither holds one.
func newestStamp(row types.Job, workspace string) (Heartbeat, bool) {
	var newest Heartbeat
	for _, root := range slices.Compact([]string{row.CheckoutRoot, workspace}) {
		if root == "" {
			continue
		}
		store, err := heartbeatStore(root)
		if err != nil {
			continue
		}
		stamps, err := store.Load()
		if err != nil {
			continue
		}
		for _, s := range stamps {
			if s.Lease == row.ID && s.At.After(newest.At) {
				newest = s
			}
		}
	}
	return newest, newest.Lease != ""
}

// deadOwners is each of owners other than exclude holding rel whose holder [deadHolder]
// judges dead at now.
func deadOwners(at location, owners []types.Job, rel, exclude string, now time.Time) []job.DeadHolder {
	var out []job.DeadHolder
	store := job.NewStore(at.jobs())
	for _, row := range owners {
		if row.ID == exclude {
			continue
		}
		if _, ok, err := declarationCovering(row.HardPaths(), rel); err != nil || !ok {
			continue
		}
		if d, ok := deadHolder(store, at, row, now); ok {
			out = append(out, d)
		}
	}
	return out
}

// deadHolder judges the holder of row dead at now: the session that took it ended, or its
// newest stamp is older than [SilentLeaseAfter] and its transcript shows no background
// command running. A row nobody took, a server's, and one whose transcript cannot be read
// are never dead: magus grants a dead holder's path to someone else, so it does not guess.
func deadHolder(store *job.Store, at location, row types.Job, now time.Time) (job.DeadHolder, bool) {
	if !job.Editing(row) || row.Registered == 0 || row.Holder.OrSession() == types.HolderServer {
		return job.DeadHolder{}, false
	}
	if since, ok := store.SessionEnded(row.ID); ok {
		return job.DeadHolder{Row: row, Since: since, Why: "its holder's session ended"}, true
	}
	s, ok := newestStamp(row, at.workspace)
	if !ok || now.Sub(s.At) <= SilentLeaseAfter || s.Transcript == "" {
		return job.DeadHolder{}, false
	}
	if tasks, err := liveBackgroundTasks(s.Transcript, at.cacheDir, now); err != nil || len(tasks) > 0 {
		return job.DeadHolder{}, false
	}
	return job.DeadHolder{Row: row, Since: s.At.Unix(), Why: fmt.Sprintf("its session %s made no judged call for %s, past %s, and runs no background command",
		s.Session, now.Sub(s.At).Round(time.Second), SilentLeaseAfter)}, true
}

// SilentLease is a leased row whose session made no judged call for longer than the
// threshold, and had no background command running.
type SilentLease struct {
	Lease   string `json:"lease"`
	Session string `json:"session"`
	// LastSeen is the session's newest stamp under the lease.
	LastSeen time.Time `json:"last_seen"`
	// SilentSeconds is how long before the report the session was last seen.
	SilentSeconds int64 `json:"silent_seconds"`
	// BackgroundNote says the transcript could not be read for background commands, so the
	// row is reported without knowing whether one still runs. Empty when it was read.
	BackgroundNote string `json:"background_note,omitempty"`
}

// SilentLeases reports the rows of rows still being edited whose session was last stamped
// more than after before now and whose transcript shows no background command running.
// It reports and ends nothing. A row never stamped is not silent, only unobserved: stamps
// begin with the guard that writes them, and a row older than that has none.
//
// The workspace's stamps are read from the user state dir; cacheDir is the host-scratch
// directory the transcript reader keeps its own state in.
func SilentLeases(root, cacheDir string, rows []types.Job, now time.Time, after time.Duration) ([]SilentLease, error) {
	store, err := heartbeatStore(root)
	if err != nil {
		return nil, err
	}
	stamps, err := store.Load()
	if err != nil {
		return nil, err
	}
	newest := map[string]Heartbeat{}
	for _, s := range stamps {
		if cur, ok := newest[s.Lease]; !ok || s.At.After(cur.At) {
			newest[s.Lease] = s
		}
	}
	var out []SilentLease
	for _, row := range rows {
		s, ok := newest[row.ID]
		if !ok || !job.Editing(row) || row.Holder.OrSession() == types.HolderServer || now.Sub(s.At) <= after {
			continue
		}
		silent := SilentLease{Lease: row.ID, Session: s.Session, LastSeen: s.At, SilentSeconds: int64(now.Sub(s.At).Seconds())}
		if s.Transcript != "" {
			tasks, err := liveBackgroundTasks(s.Transcript, cacheDir, now)
			if err != nil {
				silent.BackgroundNote = fmt.Sprintf("transcript not read for background commands: %v", err)
			} else if len(tasks) > 0 {
				continue
			}
		} else {
			silent.BackgroundNote = "the host named no transcript, so background commands were not counted"
		}
		out = append(out, silent)
	}
	return out, nil
}
