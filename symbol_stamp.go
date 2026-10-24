package magus

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/types"
)

// Whether a symbol index is current is a cache question: would the scip step replay for
// today's sources? Asked the authoritative way it forks every declared tool for its
// version, 26 subprocesses on this repository, which was most of the wall time of a
// `magus query` that could match a symbol.
//
// The stamp lets a read skip that. When the probe finds an index fresh, a stamp file records
// what a stat-only look at the same inputs sees; a later read that sees the same is fresh
// without forking, and any difference sends it back to the probe. The stamp can only skip
// a probe. It never grants freshness the probe did not grant first.
//
// The indexer's own version is in the stamp: an upgraded indexer writes a different index,
// and its observation probe is served from the probe cache by the binary's identity, so it
// forks nothing once seen. What the stamp cannot see is a toolchain upgrade with unchanged
// sources, which the scip key leaves out on purpose.

// symbolStampFormat changes whenever the stamp's composition does, so a stamp file written
// under an older meaning never matches.
const symbolStampFormat = "symbol-stamp/3"

// symbolStampPath is the stamp file beside one project's SCIP index.
func symbolStampPath(indexPath string) string { return indexPath + ".fresh" }

// symbolIndexStamp is what the stamp file must hold for index, the one op writes for p, to
// read fresh without a probe: op's step key before run keying (no tool versions, so no
// subprocess) and the identity of the index and of each stamp the step declares. "" when
// the key cannot be computed, which matches no stamp file.
func (m *Magus) symbolIndexStamp(ctx context.Context, c *cache.Cache, p *types.Project, op, index string, memo *cache.SourceMemo) string {
	step := m.buildStep(p, op)
	key, _, err := c.StepKeyMemo(ctx, &step, memo)
	if err != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", symbolStampFormat, key)
	// The op's own driven set, so binding a second indexing spell leaves this stamp as it was.
	driven := map[string]map[string]bool{p.Path: targetDrivenBins(p, op)}
	observed := m.probeObservations(ctx, []*types.Project{p}, driven)[p.Path]
	for _, name := range slices.Sorted(maps.Keys(observed)) {
		fmt.Fprintf(&b, "observed %s=%s\n", name, observed[name])
	}
	writeFileIdentity(&b, index)
	for _, s := range step.Stamps {
		writeFileIdentity(&b, cache.StampPath(step.WorkspaceRoot, s))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// indexReplayed reports whether step, an index op's, would replay AND the index on disk is
// the one that entry recorded. A hit alone would vouch for an index another key's run left
// behind, such as the edit's after the sources were reverted.
func indexReplayed(ctx context.Context, c *cache.Cache, step cache.Step) (bool, error) {
	step.VerifyOutputs = true
	return c.IsCached(ctx, step)
}

func symbolStampMatches(indexPath, want string) bool {
	if want == "" {
		return false
	}
	got, err := os.ReadFile(symbolStampPath(indexPath))
	return err == nil && string(got) == want
}

// writeSymbolStamp records want once the probe has found the index fresh. Best-effort: a
// stamp file that cannot be written costs the next read a probe and nothing else. Written
// atomically, so a concurrent reader never matches half a stamp.
func writeSymbolStamp(indexPath, want string) {
	if want == "" {
		return
	}
	_ = file.WriteFileAtomic(symbolStampPath(indexPath), []byte(want), 0o644)
}

// changedListCap bounds Changed on a status, so an edit across thousands of files cannot
// bloat it; ChangedCount still counts them all.
const changedListCap = 200

// changedSinceIndex is the workspace-relative sources the step for op keys on in p whose
// mtime is past index's, sorted. A deleted source leaves nothing to stat, so it is never
// listed. memo is the sweep's, so the expansion symbolIndexStamp paid is not paid again.
func (m *Magus) changedSinceIndex(ctx context.Context, p *types.Project, op, index string, memo *cache.SourceMemo) ([]string, error) {
	built, err := os.Stat(index)
	if err != nil {
		return nil, err
	}
	c := m.freshnessCache(ctx)
	if c == nil {
		return nil, errors.New("no cache is open to expand the indexer's sources")
	}
	step := m.buildStep(p, op)
	_, lines, err := c.StepKeyMemo(ctx, &step, memo)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, line := range lines {
		rel, ok := keyedSource(line)
		if !ok {
			continue
		}
		abs := filepath.Join(step.WorkspaceRoot, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil || !info.ModTime().After(built.ModTime()) {
			continue
		}
		if r, err := filepath.Rel(m.Root(), abs); err == nil {
			rel = filepath.ToSlash(r)
		}
		changed = append(changed, rel)
	}
	slices.Sort(changed)
	return slices.Compact(changed), nil
}

// keyedSource is the path of a cache key's "src:<path>:<hash>:<x>" line, read from the
// right since a path may hold a colon. The key lines are the one record of the step's
// expanded sources that honors every glob's exclusions.
func keyedSource(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "src:")
	if !ok {
		return "", false
	}
	for range 2 {
		i := strings.LastIndexByte(rest, ':')
		if i < 0 {
			return "", false
		}
		rest = rest[:i]
	}
	return rest, rest != ""
}

// SymbolFreshnessEnv names the environment variables an index freshness verdict depends
// on, sorted: the ones the scip steps key on, and PATH, which decides the binary a version
// probe runs. Two processes that agree on every value reach the same verdict about the
// same tree; ones that do not may each be right about their own environment.
func (m *Magus) SymbolFreshnessEnv() []string {
	capable := m.workspaceIndexes()
	names := make([]string, 0, 1+len(capable))
	names = append(names, "PATH")
	for _, idx := range capable {
		names = append(names, m.buildStep(idx.project, idx.op).EnvAllow...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// SymbolIndexStatusByStamp reports the freshness of each symbol index the workspace's
// projects declare, one entry per (project, indexer op), sorted by project and then
// binding order, checked now: unlike SymbolIndexStatus it never answers
// from the server's watcher memo, so a graph read can state freshness as of the moment it
// reads. An index whose stamp file matches is fresh with no probe; the rest pay the
// tool-version and observation probes once for the sweep, as a run does, and each one the
// probe finds fresh is stamped for the next read. A stale entry carries the sources whose
// mtime is past its index's in Changed; a deleted source is never among them. Safe for
// concurrent use.
func (m *Magus) SymbolIndexStatusByStamp(ctx context.Context) []types.SymbolIndexStatus {
	capable := m.workspaceIndexes()
	if len(capable) == 0 {
		return nil
	}
	cacheDir := resolveCacheDir(m.Root(), m.cfg)
	c := m.freshnessCache(ctx)
	memo := cache.NewSourceMemo()
	out := make([]types.SymbolIndexStatus, len(capable))
	type pending struct {
		at    int
		idx   projectIndex
		index string
		stamp string
	}
	var probe []pending
	indexes := make([]string, len(capable))
	for i, idx := range capable {
		out[i] = types.SymbolIndexStatus{Project: idx.projectRef(), Op: idx.op, Language: idx.language, Freshness: types.SymbolIndexNotBuilt}
		index := symbols.IndexPath(cacheDir, idx.project.Path, idx.op)
		indexes[i] = index
		if _, err := os.Stat(index); err != nil {
			// The install hint is the fix for an index that was never built because its
			// indexer is missing; an index whose indexer a run would find needs only a build.
			if _, err := spawn.LookPath(ctx, idx.bin); err != nil {
				out[i].Detail = symbols.MissingIndexerHint(idx.language, idx.bin)
			}
			continue
		}
		out[i].Freshness = types.SymbolIndexStale
		if c == nil {
			continue
		}
		stamp := m.symbolIndexStamp(ctx, c, idx.project, idx.op, index, memo)
		if symbolStampMatches(index, stamp) {
			out[i].Freshness = types.SymbolIndexFresh
			continue
		}
		probe = append(probe, pending{at: i, idx: idx, index: index, stamp: stamp})
	}
	if len(probe) > 0 {
		var projects []*types.Project
		idxs := make([]projectIndex, len(probe))
		for i, w := range probe {
			idxs[i] = w.idx
			if !slices.Contains(projects, w.idx.project) {
				projects = append(projects, w.idx.project)
			}
		}
		toolVersions, unprobeable := m.toolVersionsEach(ctx, projects)
		observations := m.probeObservations(ctx, projects, indexesDriven(idxs))
		for _, w := range probe {
			path := w.idx.project.Path
			if err := unprobeable[path]; err != nil {
				out[w.at].Freshness = types.SymbolIndexUnvouched
				out[w.at].Detail = err.Error()
				continue
			}
			fresh, err := indexReplayed(ctx, c, m.symbolIndexStep(w.idx.project, w.idx.op, toolVersions[path], observations[path]))
			if err == nil && fresh {
				out[w.at].Freshness = types.SymbolIndexFresh
				writeSymbolStamp(w.index, w.stamp)
			}
		}
	}
	for i, idx := range capable {
		if out[i].Freshness != types.SymbolIndexStale {
			continue
		}
		changed, err := m.changedSinceIndex(ctx, idx.project, idx.op, indexes[i], memo)
		if err != nil {
			out[i].Detail = "cannot list the sources changed since it was built: " + err.Error()
			continue
		}
		out[i].Changed, out[i].ChangedCount = changed[:min(len(changed), changedListCap)], len(changed)
	}
	slices.SortStableFunc(out, func(a, b types.SymbolIndexStatus) int { return cmp.Compare(a.Project.Path, b.Project.Path) })
	return out
}
