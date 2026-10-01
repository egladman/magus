package sessions

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	json "github.com/egladman/magus/internal/json"
)

// DefaultRetention is how long an invocation file outlives its newest fact. [Open]
// prunes with it; a caller that wants a different window calls [Prune] directly.
//
// There is no config key and no environment variable behind this on purpose: the
// store is shared by every worktree of a repository, so a per-checkout override
// would let one worktree delete history the others still expect to read. Wiring a
// workspace-level setting is a decision to take once, not a knob to add ahead of it.
const DefaultRetention = 30 * 24 * time.Hour

// DefaultMaxFiles caps how many invocation files a store keeps, newest fact first.
// Retention alone did not bound it: every magus command that records a fact writes a
// file, so an active repository wrote about eight hundred a day and held seventeen
// thousand inside the window. The cap is a count for the reason internal/trail's
// maxEvents is: what every reader of this store pays grows with the files it lists.
const DefaultMaxFiles = 10000

// agentFileFloor is how many of the newest files holding loaded agent events survive the
// cap however many invocation files are newer. The same trade internal/trail's
// perKindFloor makes: invocation files arrive by the hundred a day and would push out
// every loaded host session, which are few, large, and the only input the graph's
// @session overlay has.
const agentFileFloor = 500

// pruneInterval is how often [Open] prunes a store, tracked by the modification time
// of pruneStamp inside it. The stamp carries no fileExt, so no reader sees an invocation.
const (
	pruneInterval = time.Hour
	pruneStamp    = ".pruned"
)

// claimStalePruneStamp reports on and reacts to staleness under a lock: called only
// after Open's own cheap Stat already suggested stamp looks stale, it takes an
// exclusive, non-blocking file lock, re-checks under it, and if still stale, prunes dir
// and refreshes stamp. That plain Stat-then-write pair on stamp is what let every
// concurrent Open in the same window see the same stale stamp and each independently
// decide to prune; the lock is what makes the decision atomic instead of the staleness
// READ that led to it. A caller that loses the race returns immediately, having never
// blocked: the common "nothing to do" path never touches the lock at all, and a caller
// that does contend skips pruning this round rather than waiting to prune again a
// moment later, which "at most once per interval" already tolerates.
func claimStalePruneStamp(dir, stamp, keep string) {
	_ = file.WithLock(context.Background(), stamp+".lock", 0, func() error {
		if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < pruneInterval {
			return nil // another caller already pruned and refreshed the stamp while this one waited
		}
		prune(dir, DefaultRetention, DefaultMaxFiles, keep)
		now := time.Now()
		if os.Chtimes(stamp, now, now) != nil {
			_ = os.WriteFile(stamp, nil, 0o644)
		}
		return nil
	})
}

// Prune deletes whole invocation files whose newest fact is older than retain, and then
// the oldest of what remains past [DefaultMaxFiles], sparing the newest agentFileFloor
// files that hold loaded agent events.
//
// A schema-1 file ages like any other: its lines still carry a timestamp, so its age is
// known even though this build reads none of its records. A file with no dated line at
// all is never deleted, by either rule, because nothing says how old it is.
//
// Deleting is all it does. Nothing here rolls a file over, renames one, or truncates
// one: an invocation file is append-only or absent, and a truncation would produce a
// third state (a file whose beginning is missing) that no reader in this package is
// written to expect. A retain of zero or less disables pruning entirely.
//
// It is best-effort and reports nothing: every failure path leaves the store exactly
// as it found it. Callers on the write path must not be able to lose a fact because
// housekeeping went wrong.
//
// # The attention exemption
//
// An attention request is raised in one invocation's file and disposed of in another's
// (see [Attention]), so the records of one request routinely straddle two files that
// age out at different times. Deleting one of the pair changes what the queue says:
//
//   - Delete the file holding the dispose while the open survives, and
//     [AttentionQueue] RESURRECTS a request a person already answered.
//   - Delete the file holding a still-open request, and the queue silently loses a
//     wait nobody has answered: an expiry, which this package does not have.
//
// Two rules close both, and both are decidable from the store alone, because pruning
// reads exactly the fold a reader would:
//
//  1. Every file naming a request that is currently OPEN is exempt, at any age. A
//     request is closed by a person or not at all.
//  2. A request's records are deleted as a UNIT: if any file naming a request is not up
//     for deletion, every file naming that request is exempt. So a disposed request
//     either disappears whole or stays whole, and neither half can outlive the other.
//
// Rule 2 keeps a file alive while a younger sibling shares one of its requests, which
// delays that file rather than pinning it: the sibling ages out too, and then both go.
// Rule 1 does pin, for as long as the request stays open, which is the intended trade.
func Prune(dir string, retain time.Duration) { prune(dir, retain, DefaultMaxFiles, "") }

// prune is [Prune] with the caller's own invocation file held back. Open passes its
// invocation file so a writer can never delete the file it is about to append to,
// whatever the clock or a reused invocation id says. maxFiles of zero or less leaves the
// count uncapped.
func prune(dir string, retain time.Duration, maxFiles int, keep string) {
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
	// have gone (a copy or a restore rewrites a modification time without touching a
	// record) and never delete one that should have stayed. That is what makes the
	// common case, a store under its cap with nothing old enough to delete, cost a
	// ReadDir and no file reads at all.
	var names []string
	stale := make(map[string]bool)
	modTimes := make(map[string]time.Time)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		names = append(names, e.Name())
		if e.Name() == keep {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		modTimes[e.Name()] = info.ModTime()
		if !info.ModTime().After(cutoff) {
			stale[e.Name()] = true
		}
	}
	overCap := maxFiles > 0 && len(names) > maxFiles
	if len(stale) == 0 && !overCap {
		return
	}

	cutoffMs := cutoff.UnixMilli()
	var all []Record
	requestFiles := make(map[string]map[string]bool)
	candidates := make(map[string]bool)
	newest := make(map[string]int64)
	holdsAgentEvents := make(map[string]bool)
	for _, name := range names {
		path := filepath.Join(dir, name)
		records, _, legacy, vanished := readFile(path)
		if vanished {
			continue
		}
		all = append(all, records...)
		var last int64
		for _, rec := range records {
			if rec.Ts > last {
				last = rec.Ts
			}
			if rec.Kind == KindAgentEvent {
				holdsAgentEvents[name] = true
			}
			if id := attentionID(rec); id != "" {
				if requestFiles[id] == nil {
					requestFiles[id] = make(map[string]bool)
				}
				requestFiles[id][name] = true
			}
		}
		if len(records) == 0 && legacy > 0 {
			last = newestLegacyTs(path)
		}
		// A file with no dated line is never deleted: nothing says how old it is.
		if last == 0 {
			continue
		}
		newest[name] = last
		if stale[name] && last < cutoffMs {
			candidates[name] = true
		}
	}
	if overCap {
		for _, name := range pastCap(newest, holdsAgentEvents, maxFiles, keep) {
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

	// Exempting one file can expose another (two requests can share a file), so this
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
		// An invocation idle past the window can still wake up and append. Re-stat so a
		// file that grew after the fold was read survives: the decision to delete it
		// was made about contents it no longer has.
		listed, ok := modTimes[name]
		info, err := os.Stat(path)
		if !ok || err != nil || !info.ModTime().Equal(listed) {
			continue
		}
		_ = os.Remove(path)
	}
}

// pastCap names the files the count cap removes: everything past the newest maxFiles by
// newest fact, except keep and the newest agentFileFloor files holding agent events.
// Ties order by name so two prunes of one store agree on which file is past the line.
func pastCap(newest map[string]int64, holdsAgentEvents map[string]bool, maxFiles int, keep string) []string {
	ranked := make([]string, 0, len(newest))
	for name := range newest {
		ranked = append(ranked, name)
	}
	slices.SortFunc(ranked, func(a, b string) int {
		if c := cmp.Compare(newest[b], newest[a]); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	var out []string
	agentFiles := 0
	for i, name := range ranked {
		if holdsAgentEvents[name] {
			agentFiles++
		}
		if i < maxFiles || name == keep || (holdsAgentEvents[name] && agentFiles <= agentFileFloor) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// newestLegacyTs is the newest timestamp on a schema-1 file's lines, or zero when none
// decodes. [readFile] counts those lines without keeping them, so pruning reads the file
// again for the one field it needs to age it.
func newestLegacyTs(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	var newest int64
	br := bufio.NewReader(f)
	for {
		line, over, err := readLine(br, maxLineBytes)
		if !over {
			var dated struct {
				V  int   `json:"v"`
				Ts int64 `json:"ts"`
			}
			if json.Unmarshal(bytes.TrimSpace(line), &dated) == nil && dated.V == schemaVersionSession {
				newest = max(newest, dated.Ts)
			}
		}
		if err != nil {
			return newest
		}
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
