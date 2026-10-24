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
	if fresh(store, h) {
		return nil
	}
	return store.Update(ctx, func(entries []Heartbeat) []Heartbeat {
		for _, e := range entries {
			if e.key() == h.key() && h.At.Sub(e.At) < heartbeatInterval {
				return nil
			}
		}
		return []Heartbeat{h}
	})
}

// fresh reports whether the store already holds a stamp for h's pair inside the interval.
// An unreadable store reads as not fresh, so the locked path reports its error.
func fresh(store file.JSONL[Heartbeat], h Heartbeat) bool {
	entries, err := store.Load()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(e Heartbeat) bool {
		return e.key() == h.key() && h.At.Sub(e.At) < heartbeatInterval
	})
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
