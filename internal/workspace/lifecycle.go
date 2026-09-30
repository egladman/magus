package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// A LIFECYCLE PROVIDER is a spell that answers when each release cycle of a tool reaches
// its end of life (spells.ListLifecyclesContract). A magusfile wires ONE with
// magus\lifecycle.provider(<spell handle>). Its answer fills columns of `magus describe
// tools` and one doctor line, and never gates a build.
//
// The runner lives behind a hook for the reason the workspace provider's does: only the
// bindings layer can run a Buzz spell.
//
// `magus describe tools` asks on every call, as it re-probes versions on every call, and
// stores the answer. Doctor, and a describe that cannot reach the provider, read the stored
// answer and never fetch.

// ErrLifecycleOffline and ErrLifecycleUnreached are how a runner says it did not answer,
// as opposed to answering badly. Neither is a failure of the caller: each reads as a state
// on the report. Any other runner error is a malformed answer, and is returned.
var (
	ErrLifecycleOffline   = errors.New("MAGUS_OFFLINE is set, so the lifecycle provider was not asked")
	ErrLifecycleUnreached = errors.New("the lifecycle provider did not answer")
)

// LifecycleRunner invokes spellName's list_lifecycles contract for keys and returns the
// decoded records.
type LifecycleRunner func(ctx context.Context, spellName, root string, keys []string) ([]spells.Lifecycle, error)

var lifecycleRunner LifecycleRunner

// RegisterLifecycleRunner installs the runner [AskLifecycles] delegates to. Meant to be
// called once, from the bindings package's init; a second call panics rather than silently
// shadowing the first.
func RegisterLifecycleRunner(fn LifecycleRunner) {
	if lifecycleRunner != nil {
		panic("workspace: lifecycle runner already registered")
	}
	lifecycleRunner = fn
}

// lifecycleCacheVersion is the entry format, folded into the fingerprint for the reason
// providerCacheVersion is: a retagged field must miss rather than decode silently zeroed.
const lifecycleCacheVersion = 1

type lifecycleCacheEntry struct {
	Fingerprint string             `json:"fingerprint"`
	FetchedAt   time.Time          `json:"fetched_at"`
	Lifecycles  []spells.Lifecycle `json:"lifecycles"`
	Installed   []InstalledTool    `json:"installed,omitempty"`
}

// InstalledTool is one probed version the answer was asked alongside. It is stored with
// the answer so doctor can place installed versions in their cycles without forking a
// probe. It is an observation dated by the entry's fetch, not part of the fingerprint.
type InstalledTool struct {
	Project   string `json:"project"`
	Bin       string `json:"bin"`
	Lifecycle string `json:"lifecycle"`
	Version   string `json:"version"`
}

// LifecycleAnswer is a provider's answer and where it came from.
type LifecycleAnswer struct {
	Status     types.LifecycleStatus
	Lifecycles []spells.Lifecycle
	// Installed is what the stored answer was asked alongside; empty on a live answer.
	Installed []InstalledTool
}

// LifecycleKeys returns the distinct Tool.Lifecycle values the projects' resolved spells
// declare, sorted. Every caller asks for the workspace's whole set, whatever it goes on to
// show, so a filtered `describe tools` stores the entry doctor reads.
func LifecycleKeys(projects []*types.Project) []string {
	var keys []string
	for _, p := range projects {
		for _, sp := range p.ResolvedSpells {
			for _, bin := range sp.ToolNames() {
				if t, _ := sp.Tool(bin); t.Lifecycle != "" && !slices.Contains(keys, t.Lifecycle) {
					keys = append(keys, t.Lifecycle)
				}
			}
		}
	}
	slices.Sort(keys)
	return keys
}

// AskLifecycles asks spellName for keys and stores the answer under cache, with the
// installed versions the caller probed. An empty spellName reads as unwired and asks
// nothing.
//
// It errors only on a malformed answer. Offline and unreached are states: the stored
// answer is replayed when its fingerprint still matches, and otherwise the report carries
// no data and says why.
func AskLifecycles(ctx context.Context, cache ProviderCache, root, spellName string, keys []string, installed []InstalledTool) (LifecycleAnswer, error) {
	if spellName == "" {
		return LifecycleAnswer{Status: types.LifecycleStatus{State: types.LifecycleUnwired}}, nil
	}
	if lifecycleRunner == nil {
		return LifecycleAnswer{}, fmt.Errorf("lifecycle provider %q: no runner is registered in this binary", spellName)
	}
	if len(keys) == 0 {
		// Nothing to ask: no spell here names a lifecycle product.
		return LifecycleAnswer{Status: types.LifecycleStatus{Provider: spellName, State: types.LifecycleLive}}, nil
	}
	fingerprint := lifecycleFingerprint(ctx, root, spellName, keys)
	got, err := lifecycleRunner(ctx, spellName, root, keys)
	switch {
	case errors.Is(err, ErrLifecycleOffline):
		return replayLifecycles(cache.Dir, spellName, fingerprint, types.LifecycleOffline, err.Error()), nil
	case errors.Is(err, ErrLifecycleUnreached):
		return replayLifecycles(cache.Dir, spellName, fingerprint, types.LifecycleUnreached, err.Error()), nil
	case err != nil:
		return LifecycleAnswer{}, err
	}

	fetched := time.Now().UTC()
	if cache.Dir != "" && !cache.Immutable && fingerprint != "" {
		writeProviderCache(ctx, lifecycleCachePath(cache.Dir, spellName),
			lifecycleCacheEntry{Fingerprint: fingerprint, FetchedAt: fetched, Lifecycles: got, Installed: installed})
	}
	return lifecycleAnswer(spellName, types.LifecycleLive, "", fetched, got), nil
}

// CachedLifecycles returns the stored answer for keys, or false when there is none or the
// one stored no longer answers this question. It never runs the provider: doctor reads
// through it.
func CachedLifecycles(ctx context.Context, cacheDir, root, spellName string, keys []string) (LifecycleAnswer, bool) {
	if spellName == "" || cacheDir == "" {
		return LifecycleAnswer{}, false
	}
	entry, ok := readLifecycleCache(cacheDir, spellName)
	if !ok || entry.Fingerprint != lifecycleFingerprint(ctx, root, spellName, keys) {
		return LifecycleAnswer{}, false
	}
	a := lifecycleAnswer(spellName, types.LifecycleCached, "", entry.FetchedAt, entry.Lifecycles)
	a.Installed = entry.Installed
	return a, true
}

func replayLifecycles(cacheDir, spellName, fingerprint, state, detail string) LifecycleAnswer {
	if entry, ok := readLifecycleCache(cacheDir, spellName); ok && fingerprint != "" && entry.Fingerprint == fingerprint {
		return lifecycleAnswer(spellName, state, detail, entry.FetchedAt, entry.Lifecycles)
	}
	return LifecycleAnswer{Status: types.LifecycleStatus{Provider: spellName, State: state, Detail: detail}}
}

func lifecycleAnswer(spellName, state, detail string, fetched time.Time, got []spells.Lifecycle) LifecycleAnswer {
	st := types.LifecycleStatus{Provider: spellName, State: state, Detail: detail, FetchedAt: fetched.UTC().Format(time.RFC3339)}
	var oldest time.Time
	for _, l := range got {
		if !slices.Contains(st.Sources, l.Source) {
			st.Sources = append(st.Sources, l.Source)
		}
		// The runner rejected any as_of that does not parse, so a failure here is a
		// hand-edited cache file, and skipping it keeps AsOf honest.
		if at, err := time.Parse(time.RFC3339, l.AsOf); err == nil && (oldest.IsZero() || at.Before(oldest)) {
			oldest = at
		}
	}
	slices.Sort(st.Sources)
	if !oldest.IsZero() {
		st.AsOf = oldest.UTC().Format(time.RFC3339)
	}
	return LifecycleAnswer{Status: st, Lifecycles: got}
}

// lifecycleOwnSourceGlobs are the workspace spell sources folded into the fingerprint: a
// workspace-local provider's body decides its answer as surely as the keys do. Walked from
// spells/ only, since the whole tree is far more than a report should pay for.
var lifecycleOwnSourceGlobs = []string{"**/*.buzz"}

// lifecycleFingerprint digests everything that decides spellName's answer for keys through
// the digest providerFingerprint uses, with the keys in place of declared globs. It is not
// keyed on a clock: an answer is stale when the question changes, and a reader sees its
// age in fetched_at. Empty when a cancelled walk left the digest meaningless.
func lifecycleFingerprint(ctx context.Context, root, spellName string, keys []string) string {
	lines := matchedFileIdentities(ctx, filepath.Join(root, "spells"), lifecycleOwnSourceGlobs)
	if ctx.Err() != nil {
		return ""
	}
	asked := make([]string, 0, len(keys))
	for _, k := range slices.Sorted(slices.Values(keys)) {
		asked = append(asked, "key\x00"+k)
	}
	return providerDigest(lifecycleCacheVersion, root, spellName, asked, lines)
}

// lifecycleCachePath sits beside the workspace providers' entries, prefixed so a spell
// wired as both cannot overwrite one answer with the other.
func lifecycleCachePath(cacheDir, spellName string) string {
	return providerCachePath(cacheDir, "lifecycle-"+spellName)
}

func readLifecycleCache(cacheDir, spellName string) (lifecycleCacheEntry, bool) {
	var entry lifecycleCacheEntry
	ok := cacheDir != "" && readProviderCache(lifecycleCachePath(cacheDir, spellName), &entry)
	return entry, ok
}
