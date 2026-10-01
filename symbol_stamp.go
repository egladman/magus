package magus

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Whether a symbol index is current is a cache question: would the scip step replay for
// today's sources? Asked the authoritative way it forks every declared tool for its
// version, 26 subprocesses on this repository, which was most of the wall time of a
// `magus query` that could match a symbol.
//
// The stamp lets a read skip that. When the probe finds an index fresh, a sidecar records
// what a stat-only look at the same inputs sees; a later read that sees the same is fresh
// without forking, and any difference sends it back to the probe. The stamp can only skip
// a probe. It never grants freshness the probe did not grant first.
//
// What it cannot see is a toolchain upgrade with unchanged sources: the index keeps
// reading fresh until the next build. The stale notice asks whether the sources moved
// since the index was built, and tool versions are not part of that question.

// symbolStampFormat changes whenever the stamp's composition does, so a sidecar written
// under an older meaning never matches.
const symbolStampFormat = "symbol-stamp/1"

// symbolStampPath is the sidecar beside one project's SCIP index.
func symbolStampPath(indexPath string) string { return indexPath + ".fresh" }

// symbolIndexStamp is what the sidecar must hold for p's index at indexPath to read fresh
// without a probe: the scip step's key before run keying (sources by content through the
// mtime fast path, config, spell definitions and this binary, no tool versions and so no
// subprocess), the index file's identity, and the identity of each completion stamp the
// step declares. "" when the key cannot be computed, which matches no sidecar.
func (m *Magus) symbolIndexStamp(ctx context.Context, c *cache.Cache, p *types.Project, indexPath string, memo *cache.SourceMemo) string {
	step := m.buildStep(p, spells.SymbolIndexOp)
	key, _, err := c.StepKeyMemo(ctx, &step, memo)
	if err != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", symbolStampFormat, key)
	writeFileIdentity(&b, indexPath)
	for _, s := range step.Stamps {
		writeFileIdentity(&b, filepath.Join(step.WorkspaceRoot, s))
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
// sidecar that cannot be written costs the next read a probe and nothing else. The rename
// keeps a concurrent reader from matching half a stamp.
func writeSymbolStamp(indexPath, want string) {
	if want == "" {
		return
	}
	path := symbolStampPath(indexPath)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fresh-*")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(want)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// SymbolFreshnessEnv names the environment variables an index freshness verdict depends
// on, sorted: the ones the scip steps key on, and PATH, which decides the binary a version
// probe runs. Two processes that agree on every value reach the same verdict about the
// same tree; ones that do not may each be right about their own environment.
func (m *Magus) SymbolFreshnessEnv() []string {
	names := []string{"PATH"}
	capable, _ := m.symbolCapableWithLanguage()
	for _, p := range capable {
		names = append(names, m.buildStep(p, spells.SymbolIndexOp).EnvAllow...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// SymbolIndexStatusByStamp reports each symbol-capable project's index freshness, sorted
// by project, checked now: unlike SymbolIndexStatus it never answers from the server's
// watcher memo, so a graph read can state freshness as of the moment it reads. A project
// whose sidecar matches is fresh with no probe; the rest pay the tool-version and
// observation probes once for the sweep, as a run does, and each one the probe finds
// fresh is stamped for the next read. Safe for concurrent use.
func (m *Magus) SymbolIndexStatusByStamp(ctx context.Context) []types.SymbolIndexStatus {
	capable, langs := m.symbolCapableWithLanguage()
	if len(capable) == 0 {
		return nil
	}
	cacheDir := resolveCacheDir(m.Root(), m.cfg)
	c := m.freshnessCache(ctx)
	memo := cache.NewSourceMemo()
	out := make([]types.SymbolIndexStatus, len(capable))
	type pending struct {
		at    int
		p     *types.Project
		index string
		stamp string
	}
	var probe []pending
	for i, p := range capable {
		out[i] = types.SymbolIndexStatus{Project: types.NewProjectRef(p.Path, p.Dir), Language: langs[p.Path], Freshness: types.SymbolIndexNotBuilt}
		index := symbols.IndexPath(cacheDir, p.Dir)
		if _, err := os.Stat(index); err != nil {
			continue
		}
		out[i].Freshness = types.SymbolIndexStale
		if c == nil {
			continue
		}
		stamp := m.symbolIndexStamp(ctx, c, p, index, memo)
		if symbolStampMatches(index, stamp) {
			out[i].Freshness = types.SymbolIndexFresh
			continue
		}
		probe = append(probe, pending{at: i, p: p, index: index, stamp: stamp})
	}
	if len(probe) > 0 {
		projects := make([]*types.Project, len(probe))
		for i, w := range probe {
			projects[i] = w.p
		}
		toolVersions := m.toolVersionsByProject(ctx, projects)
		observations := m.probeObservations(ctx, projects, nil)
		for _, w := range probe {
			// A cache hit means the scip op would not re-run, so the index is current.
			fresh, err := c.IsCached(ctx, m.symbolIndexStep(w.p, toolVersions[w.p.Path], observations[w.p.Path]))
			if err == nil && fresh {
				out[w.at].Freshness = types.SymbolIndexFresh
				writeSymbolStamp(w.index, w.stamp)
			}
		}
	}
	slices.SortFunc(out, func(a, b types.SymbolIndexStatus) int { return cmp.Compare(a.Project.Path, b.Project.Path) })
	return out
}
