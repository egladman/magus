package magus

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file"
	procrun "github.com/egladman/magus/internal/proc/run"
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

// symbolIndexStamp is what the stamp file must hold for the index op writes for p to read
// fresh without a probe. It combines op's step key before run keying (sources by content
// through the mtime fast path, config, spell definitions and this binary, no tool versions
// and so no subprocess) with the identity of each completion stamp the step declares, the
// index among them. "" when the key cannot be computed, which matches no stamp file.
func (m *Magus) symbolIndexStamp(ctx context.Context, c *cache.Cache, p *types.Project, op string, memo *cache.SourceMemo) string {
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
	for _, s := range step.Stamps {
		writeFileIdentity(&b, cache.StampPath(step.WorkspaceRoot, s))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
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
// probe finds fresh is stamped for the next read. Safe for concurrent use.
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
	for i, idx := range capable {
		out[i] = types.SymbolIndexStatus{Project: idx.projectRef(), Op: idx.op, Language: idx.language, Freshness: types.SymbolIndexNotBuilt}
		index := symbols.IndexPath(cacheDir, idx.project.Dir, idx.op)
		if _, err := os.Stat(index); err != nil {
			// The install hint is the fix for an index that was never built because its
			// indexer is missing; an index whose indexer a run would find needs only a build.
			if _, err := procrun.LookPath(ctx, idx.bin); err != nil {
				out[i].Detail = symbols.MissingIndexerHint(idx.language, idx.bin)
			}
			continue
		}
		out[i].Freshness = types.SymbolIndexStale
		if c == nil {
			continue
		}
		stamp := m.symbolIndexStamp(ctx, c, idx.project, idx.op, memo)
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
			// A cache hit means the indexer op would not re-run, so the index is current.
			fresh, err := c.IsCached(ctx, m.symbolIndexStep(w.idx.project, w.idx.op, toolVersions[path], observations[path]))
			if err == nil && fresh {
				out[w.at].Freshness = types.SymbolIndexFresh
				writeSymbolStamp(w.index, w.stamp)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b types.SymbolIndexStatus) int { return cmp.Compare(a.Project.Path, b.Project.Path) })
	return out
}
