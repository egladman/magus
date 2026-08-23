package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
)

// DefaultRetention is how long a session file outlives its newest fact. [Open]
// prunes with it; a caller that wants a different window calls [Prune] directly.
//
// There is no config key and no environment variable behind this on purpose: the
// store is shared by every worktree of a repository, so a per-checkout override
// would let one worktree delete history the others still expect to read. Wiring a
// workspace-level setting is a decision to take once, not a knob to add ahead of it.
const DefaultRetention = 30 * 24 * time.Hour

// Prune deletes whole session files whose newest fact is older than retain.
//
// Deleting is all it does. Nothing here rolls a file over, renames one, or truncates
// one: a session file is append-only or absent, and a truncation would produce a
// third state - a file whose beginning is missing - that no reader in this package is
// written to expect. A retain of zero or less disables pruning entirely.
//
// It is best-effort and reports nothing: every failure path leaves the store exactly
// as it found it. Callers on the write path must not be able to lose a fact because
// housekeeping went wrong.
//
// # The attention exemption
//
// An attention request is raised in one session's file and disposed of in another's
// (see [Attention]), so the records of one request routinely straddle two files that
// age out at different times. Deleting one of the pair changes what the queue says:
//
//   - Delete the file holding the dispose while the open survives, and
//     [AttentionQueue] RESURRECTS a request a person already answered.
//   - Delete the file holding a still-open request, and the queue silently loses a
//     wait nobody has answered - an expiry, which this package does not have.
//
// Two rules close both, and both are decidable from the store alone, because pruning
// reads exactly the fold a reader would:
//
//  1. Every file naming a request that is currently OPEN is exempt, at any age. A
//     request is closed by a person or not at all.
//  2. A request's records are deleted as a UNIT: if any file naming a request is too
//     young to delete, every file naming that request is exempt. So a disposed request
//     either disappears whole or stays whole, and neither half can outlive the other.
//
// Rule 2 keeps a file alive while a younger sibling shares one of its requests, which
// delays that file rather than pinning it: the sibling ages out too, and then both go.
// Rule 1 does pin, for as long as the request stays open, which is the intended trade.
func Prune(dir string, retain time.Duration) { prune(dir, retain, "") }

// prune is [Prune] with the caller's own session file held back. Open passes its
// session file so a writer can never delete the file it is about to append to,
// whatever the clock or a reused session id says.
func prune(dir string, retain time.Duration, keep string) {
	if retain <= 0 {
		return
	}
	cutoff := time.Now().Add(-retain)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Modification time is a cheap pre-filter, never the retention test: a file it
	// calls young is left alone unread, and one it calls stale still has to prove it by
	// its records below. The filter can therefore only ever spare a file that could
	// have gone - a copy or a restore rewrites a modification time without touching a
	// record - and never delete one that should have stayed. That is what makes the
	// common case, a store with nothing old enough to delete, cost a ReadDir and no
	// file reads at all.
	var names []string
	stale := make(map[string]time.Time)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		names = append(names, e.Name())
		if e.Name() == keep {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		stale[e.Name()] = info.ModTime()
	}
	if len(stale) == 0 {
		return
	}

	cutoffMs := cutoff.UnixMilli()
	var all []Record
	requestFiles := make(map[string]map[string]bool)
	candidates := make(map[string]bool)
	for _, name := range names {
		records, _, vanished := readFile(filepath.Join(dir, name))
		if vanished {
			continue
		}
		all = append(all, records...)
		var last int64
		for _, rec := range records {
			if rec.Ts > last {
				last = rec.Ts
			}
			if id := attentionID(rec); id != "" {
				if requestFiles[id] == nil {
					requestFiles[id] = make(map[string]bool)
				}
				requestFiles[id][name] = true
			}
		}
		// last stays zero for a file with no decodable record, which reads as older
		// than any cutoff and deletes it. That is the right answer: it is the file of
		// a session that recorded nothing anybody can still use.
		if _, ok := stale[name]; ok && last < cutoffMs {
			candidates[name] = true
		}
	}
	if len(candidates) == 0 {
		return
	}

	sortRecords(all)
	openIDs := make(map[string]bool)
	for _, req := range AttentionQueue(Fold{Records: all}) {
		openIDs[req.ID] = true
	}

	// Exempting one file can expose another - two requests can share a file - so this
	// runs to a fixed point rather than in one pass. It only ever removes candidates,
	// so it converges in at most one round per file.
	for changed := true; changed; {
		changed = false
		for id, files := range requestFiles {
			if !openIDs[id] && prunableTogether(candidates, files) {
				continue
			}
			for name := range files {
				if candidates[name] {
					delete(candidates, name)
					changed = true
				}
			}
		}
	}

	for name := range candidates {
		path := filepath.Join(dir, name)
		// A session idle past the window can still wake up and append. Re-stat so a
		// file that grew after the fold was read survives: the decision to delete it
		// was made about contents it no longer has.
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().Equal(stale[name]) {
			continue
		}
		_ = os.Remove(path)
	}
}

// prunableTogether reports whether every file naming one request is up for pruning,
// which is the only shape in which any of them may go.
func prunableTogether(candidates map[string]bool, files map[string]bool) bool {
	for name := range files {
		if !candidates[name] {
			return false
		}
	}
	return true
}

// attentionID returns the request a record names, or "" for a record that names none.
//
// The payload is decoded through a narrow struct shared by both kinds rather than
// through [AttentionOpen] and [AttentionDispose]: pruning needs the id and nothing
// else, and a decode that ignores the rest cannot start disagreeing with [Attention]
// about a field neither of them reads. A record whose id fails to decode is one
// [Attention] also skips, so it holds no file back.
func attentionID(rec Record) string {
	if rec.Kind != KindAttentionOpen && rec.Kind != KindAttentionDispose {
		return ""
	}
	var named struct {
		Request string `json:"request"`
	}
	if json.Unmarshal(rec.Payload, &named) != nil {
		return ""
	}
	return named.Request
}
