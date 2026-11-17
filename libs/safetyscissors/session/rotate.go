package session

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/codec"
)

// DefaultRetention is how long an invocation file outlives its newest fact. [Open]
// prunes with it; a caller that wants a different window calls [Prune] directly.
const DefaultRetention = 30 * 24 * time.Hour

// defaultMaxFiles caps how many invocation files a store keeps, newest fact first.
// Retention alone did not bound it: every command that records a fact writes a file, so
// an active repository wrote about eight hundred a day and held seventeen thousand inside
// the window. The cap is a count because what every reader of a store pays grows with
// the files it lists.
const defaultMaxFiles = 10000

// agentFileFloor is how many of the newest files holding loaded agent events survive the
// cap however many invocation files are newer. Invocation files arrive by the hundred a
// day and would push out every loaded host session, which are few, large, and often the
// only record of what a host did.
const agentFileFloor = 500

// pruneInterval is how often [Open] prunes a store, tracked by the modification time of
// pruneStamp inside it. The stamp carries no FileExt, so no reader sees an invocation.
const (
	pruneInterval = time.Hour
	pruneStamp    = ".pruned"
)

// Tie binds records that pruning may delete only together, wherever they were written.
//
// Key names the group a record belongs to, or "" for none. Held names the groups pruning
// keeps at any age; it is handed the fold of every file pruning read, in fold order,
// which is the fold a reader would see. Either may be nil: a nil Key ties nothing, and a
// nil Held holds nothing back.
type Tie struct {
	Key  func(Record) string
	Held func(Fold) []string
}

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
//
// Every error is dropped, a failed prune included: Open cannot fail over housekeeping,
// and the next interval prunes again.
func claimStalePruneStamp(ctx context.Context, dir, stamp, keep string, o options) {
	_ = codec.WithLock(ctx, stamp+".lock", 0, func() error {
		if fi, err := os.Stat(stamp); err == nil && o.now().Sub(fi.ModTime()) < pruneInterval {
			return nil // another caller already pruned and refreshed the stamp while this one waited
		}
		_ = prune(ctx, dir, DefaultRetention, defaultMaxFiles, keep, o)
		now := o.now()
		if os.Chtimes(stamp, now, now) != nil {
			_ = os.WriteFile(stamp, nil, 0o644)
		}
		return nil
	})
}

// Prune deletes whole invocation files whose newest fact is older than retain, and then
// the oldest of what remains past defaultMaxFiles (10000), sparing the newest agentFileFloor
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
// It returns a [*StoreError] for a store it could not list and for each file it chose
// but could not delete, joined; a store that does not exist has nothing to prune. A
// failure deletes nothing it did not mean to: every file it cannot read or that changed
// since it was read is kept. Callers on the write path must not be able to lose a fact
// because housekeeping went wrong, which is why [Open] drops the error.
//
// # Ties
//
// A group of records the caller pairs, such as a request raised in one invocation's file
// and answered in another's, routinely straddles two files that age out at different
// times. Deleting one of the pair changes what a reader of the pair sees: delete the
// answer while the request survives and the request comes back; delete a request nobody
// answered and the wait is lost.
//
// The options' [Tie] closes both, with two rules decidable from the store alone, because
// pruning reads exactly the fold a reader would:
//
//  1. Every file naming a group the Tie holds is exempt, at any age.
//  2. A group's records are deleted as a UNIT: if any file naming a group is not up for
//     deletion, every file naming that group is exempt. So a group either disappears
//     whole or stays whole, and neither half can outlive the other.
//
// Rule 2 keeps a file alive while a younger sibling shares one of its groups, which
// delays that file rather than pinning it: the sibling ages out too, and then both go.
// Rule 1 does pin, for as long as the Tie holds the group, which is the intended trade.
//
// It stops early and returns ctx.Err() as is once ctx is done.
func Prune(ctx context.Context, dir string, retain time.Duration, opts ...Option) error {
	return prune(ctx, dir, retain, defaultMaxFiles, "", newOptions(opts))
}

// prune is [Prune] with the caller's own invocation file held back. Open passes its
// invocation file so a writer can never delete the file it is about to append to,
// whatever the clock or a reused invocation id says. maxFiles of zero or less leaves the
// count uncapped.
func prune(ctx context.Context, dir string, retain time.Duration, maxFiles int, keep string, o options) error {
	if retain <= 0 {
		return nil
	}
	cutoff := o.now().Add(-retain)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return &StoreError{Op: "list store to prune", Path: dir, Err: err}
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), FileExt) {
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
		return nil
	}

	cutoffMs := cutoff.UnixMilli()
	var all []Record
	groupFiles := make(map[string]map[string]bool)
	candidates := make(map[string]bool)
	newest := make(map[string]int64)
	holdsAgentEvents := make(map[string]bool)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
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
			if id := o.tie.key(rec); id != "" {
				if groupFiles[id] == nil {
					groupFiles[id] = make(map[string]bool)
				}
				groupFiles[id][name] = true
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
		return nil
	}

	sortRecords(all)
	held := make(map[string]bool)
	for _, id := range o.tie.held(Fold{Records: all}) {
		held[id] = true
	}

	// Exempting one file can expose another (two groups can share a file), so this runs
	// to a fixed point rather than in one pass. It only ever removes candidates, so it
	// converges in at most one round per file.
	for changed := true; changed; {
		changed = false
		for id, files := range groupFiles {
			if !held[id] && prunableTogether(candidates, files) {
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

	var errs []error
	for name := range candidates {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		path := filepath.Join(dir, name)
		// An invocation idle past the window can still wake up and append. Re-stat so a
		// file that grew after the fold was read survives: the decision to delete it
		// was made about contents it no longer has.
		listed, ok := modTimes[name]
		info, err := os.Stat(path)
		if !ok || err != nil || !info.ModTime().Equal(listed) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, &StoreError{Op: "prune", Path: path, Err: err})
		}
	}
	return errors.Join(errs...)
}

func (t Tie) key(rec Record) string {
	if t.Key == nil {
		return ""
	}
	return t.Key(rec)
}

func (t Tie) held(fold Fold) []string {
	if t.Held == nil {
		return nil
	}
	return t.Held(fold)
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
				Ts int64 `json:"ts"`
			}
			if olderVersion(bytes.TrimSpace(line)) && codec.Unmarshal(bytes.TrimSpace(line), &dated) == nil {
				newest = max(newest, dated.Ts)
			}
		}
		if err != nil {
			return newest
		}
	}
}

// prunableTogether reports whether every file naming one group is up for pruning, which
// is the only shape in which any of them may go.
func prunableTogether(candidates map[string]bool, files map[string]bool) bool {
	for name := range files {
		if !candidates[name] {
			return false
		}
	}
	return true
}
