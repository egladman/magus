package project

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// ScannedCommit is one commit reduced to workspace-relative, project-attributed form
// — the shared input every insight lens aggregates from.
type ScannedCommit struct {
	Author   string
	Date     time.Time
	Files    []string // workspace-relative paths that fell inside the workspace
	Projects []string // distinct projects the commit touched, sorted
	// Renames carries {before, after} for every path this commit moved, already
	// workspace-relative. It is the lineage edge: FileHotspots walks these to fold a
	// file's churn onto the name it ends the window under, instead of ranking each
	// name it has been through as a separate, quieter file. A pair is dropped unless
	// BOTH sides fall inside the workspace, since a half-resolved chain would
	// silently reattribute churn to a path the caller cannot open.
	Renames [][2]string
	// Deleted is the subset of Files this commit removed. Kept separate rather than
	// inferred from the filesystem: a path missing from disk today says nothing about
	// WHEN it went, and a file deleted and later restored must not read as deleted.
	Deleted []string
}

// Scan reads recent history (scoped to dir) and attributes each commit's files to
// projects. since bounds the window by commit date. It returns a wrapped
// ErrVCSUnsupported when VCS is disabled or the backend can't report per-commit files.
func Scan(ctx context.Context, w *types.Workspace, dir string, commits int, since string) ([]ScannedCommit, error) {
	sinceRef, err := parseSince(since)
	if err != nil {
		return nil, err
	}
	res, err := vcs.Resolve(ctx, w.Root, "", w.VCSOptions)
	if err != nil {
		return nil, err
	}
	if res.Source == types.VCSSourceDisabled {
		return nil, fmt.Errorf("%w: vcs disabled", types.ErrVCSUnsupported)
	}
	reporter, ok := res.VCS.(types.ChurnReporter)
	if !ok {
		return nil, fmt.Errorf("%w: %s cannot report per-commit files", types.ErrVCSUnsupported, res.Name)
	}
	changes, err := reporter.ChangesByCommit(ctx, dir, commits, sinceRef)
	if err != nil {
		return nil, err
	}
	// git reports paths relative to the VCS root regardless of where the log ran, so
	// the prefix is still measured from the workspace root, not dir.
	prefix := vcsRootPrefix(w.Root, res.VCS.Claims())
	idx := newProjectIndex(w)

	out := make([]ScannedCommit, 0, len(changes))
	for _, c := range changes {
		sc := ScannedCommit{Author: c.Author, Date: c.Date}
		projSet := map[string]struct{}{}
		paths := make([]string, 0, len(c.Files))
		for _, ch := range c.Files {
			paths = append(paths, ch.Path)
		}
		for _, f := range workspaceRelative(prefix, normalizeFiles(paths)) {
			sc.Files = append(sc.Files, f)
			if p, ok := idx.projectForFile(f); ok {
				projSet[p] = struct{}{}
			}
		}
		// Both sides through the same prefix strip the paths above take, so a rename
		// whose old name sat outside the scanned subtree is dropped rather than
		// pointing lineage at a path no lens can resolve.
		for _, ch := range c.Files {
			if ch.Status == types.ChangeDeleted {
				if p, ok := scanRelative(prefix, ch.Path); ok {
					sc.Deleted = append(sc.Deleted, p)
				}
				continue
			}
			if ch.PrevPath == "" {
				continue
			}
			prev, okPrev := scanRelative(prefix, ch.PrevPath)
			cur, okCur := scanRelative(prefix, ch.Path)
			if !okPrev || !okCur {
				continue
			}
			sc.Renames = append(sc.Renames, [2]string{prev, cur})
		}
		sc.Projects = make([]string, 0, len(projSet))
		for p := range projSet {
			sc.Projects = append(sc.Projects, p)
		}
		slices.Sort(sc.Projects)
		out = append(out, sc)
	}
	return out, nil
}

// counter accumulates one entity's (project or file) churn during aggregation.
type counter struct {
	commits int
	authors map[string]int // author -> commit count, for primary-author resolution
	last    time.Time
}

func (c *counter) add(author string, date time.Time) {
	c.commits++
	if c.authors == nil {
		c.authors = map[string]int{}
	}
	c.authors[author]++
	if date.After(c.last) {
		c.last = date
	}
}

// primary returns the author with the most commits (ties broken by name for
// determinism) and that count.
func (c *counter) primary() (string, int) {
	names := make([]string, 0, len(c.authors))
	for a := range c.authors {
		names = append(names, a)
	}
	slices.Sort(names)
	best, bestN := "", 0
	for _, a := range names {
		if c.authors[a] > bestN {
			best, bestN = a, c.authors[a]
		}
	}
	return best, bestN
}

// scanRelative applies to ONE path the same normalization the bulk path list gets:
// slash-normalized, trimmed, and stripped of the VCS-root prefix. It exists because
// normalizeFiles sorts and dedupes, which is right for a set of paths and destroys
// the ordering a rename pair depends on. Reports false when the path falls outside
// the scanned subtree, or is empty.
func scanRelative(prefix, path string) (string, bool) {
	p := strings.TrimSpace(filepath.ToSlash(path))
	if p == "" {
		return "", false
	}
	if prefix != "" {
		rel, ok := strings.CutPrefix(p, prefix)
		if !ok {
			return "", false
		}
		p = rel
	}
	return p, p != ""
}

// renameChains resolves every historical path in the scan to the name its file ends
// the window under. The scan is newest-first, so by the time a rename's OLD name is
// seen its NEW name has already been folded: resolving the new side first and
// pointing the old side at that result collapses a chain of any length in one pass,
// with no fixpoint loop and no risk of cycling on a path that was renamed away and
// later reused.
func renameChains(scan []ScannedCommit) map[string]string {
	canon := map[string]string{}
	for _, c := range scan {
		for _, r := range c.Renames {
			prev, cur := r[0], r[1]
			if to, ok := canon[cur]; ok {
				cur = to
			}
			if prev == cur {
				continue
			}
			canon[prev] = cur
		}
	}
	return canon
}

// aggCounters tallies per project (or per file when byFile) across the scan.
func aggCounters(scan []ScannedCommit, byFile bool) map[string]*counter {
	m := map[string]*counter{}
	for _, c := range scan {
		keys := c.Projects
		if byFile {
			keys = c.Files
		}
		for _, k := range keys {
			cc := m[k]
			if cc == nil {
				cc = &counter{}
				m[k] = cc
			}
			cc.add(c.Author, c.Date)
		}
	}
	return m
}

// ProjectStat is one project's churn over the window.
type ProjectStat struct {
	Commits int
	Authors int
	Last    time.Time
}

// ProjectStats counts commits, distinct authors, and the most recent commit per project.
func ProjectStats(scan []ScannedCommit) map[string]ProjectStat {
	out := make(map[string]ProjectStat)
	for p, c := range aggCounters(scan, false) {
		out[p] = ProjectStat{Commits: c.commits, Authors: len(c.authors), Last: c.last}
	}
	return out
}

// FileHotspots ranks files by churn × complexity (the canonical hotspot score).
// complexity maps a workspace-relative path to its complexity proxy.
//
// Churn is attributed along LINEAGE, not by path string: every name a file went by
// in the window folds onto the name it ends under, so a file renamed three times
// ranks once with its whole history rather than four times with a quarter each. That
// is what makes the ranking answer "what keeps getting rewritten" - the thing, not
// the path - and it is why a file's move count is worth reporting beside its edits.
//
// A file whose last event was a delete is left OUT. The ranking exists to point at
// what to fix first, and a deleted file is not a refactoring target; including it
// would seat a long tail of unfixable zero-complexity rows above real ones.
func FileHotspots(scan []ScannedCommit, complexity func(rel string) int) []types.FileHotspot {
	canon := renameChains(scan)
	resolve := func(p string) string {
		if to, ok := canon[p]; ok {
			return to
		}
		return p
	}
	counters := map[string]*counter{}
	names := map[string]map[string]struct{}{}
	// The scan is newest-first, so the FIRST event seen for a name is its most recent
	// one. Recording only that verdict is what lets a file deleted and later restored
	// read as live: the restore is seen first and settles the question.
	gone := map[string]bool{}
	seen := map[string]bool{}
	for _, c := range scan {
		for _, f := range c.Deleted {
			if k := resolve(f); !seen[k] {
				seen[k], gone[k] = true, true
			}
		}
		for _, f := range c.Files {
			k := resolve(f)
			if !seen[k] {
				seen[k] = true
			}
			cc := counters[k]
			if cc == nil {
				cc = &counter{}
				counters[k] = cc
				names[k] = map[string]struct{}{}
			}
			cc.add(c.Author, c.Date)
			names[k][f] = struct{}{}
		}
	}
	out := make([]types.FileHotspot, 0, len(counters))
	for f, c := range counters {
		if gone[f] {
			continue
		}
		cx := complexity(f)
		out = append(out, types.FileHotspot{
			Path: f, Commits: c.commits, Complexity: cx, Score: c.commits * cx,
			Authors: len(c.authors), LastCommit: c.last, Moves: len(names[f]) - 1,
		})
	}
	slices.SortFunc(out, func(a, b types.FileHotspot) int {
		if d := cmp.Compare(b.Score, a.Score); d != 0 {
			return d
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return out
}

// Affinity returns every pair of projects that changed together, hottest pair first.
// The Hidden flag is left for the caller to set from the dependency graph.
func Affinity(scan []ScannedCommit) []types.CoChange {
	couple := map[[2]string]int{}
	for _, c := range scan {
		for i := range c.Projects {
			for j := i + 1; j < len(c.Projects); j++ {
				couple[[2]string{c.Projects[i], c.Projects[j]}]++
			}
		}
	}
	out := make([]types.CoChange, 0, len(couple))
	for pair, n := range couple {
		out = append(out, types.CoChange{A: pair[0], B: pair[1], Count: n})
	}
	slices.SortFunc(out, func(a, b types.CoChange) int {
		if d := cmp.Compare(b.Count, a.Count); d != 0 {
			return d
		}
		if d := cmp.Compare(a.A, b.A); d != 0 {
			return d
		}
		return cmp.Compare(a.B, b.B)
	})
	return out
}

// Ownership reports per-project author concentration, most-concentrated first.
// staleBefore flags projects whose most recent commit predates it (abandonment risk);
// pass the zero time to disable the flag.
func Ownership(scan []ScannedCommit, staleBefore time.Time) []types.OwnershipEntry {
	counters := aggCounters(scan, false)
	out := make([]types.OwnershipEntry, 0, len(counters))
	for p, c := range counters {
		primary, n := c.primary()
		share := 0
		if c.commits > 0 {
			share = n * 100 / c.commits
		}
		out = append(out, types.OwnershipEntry{
			Path: p, Commits: c.commits, Authors: len(c.authors),
			Primary: primary, PrimaryShare: share,
			BusFactor1: len(c.authors) == 1,
			Stale:      !staleBefore.IsZero() && c.last.Before(staleBefore),
			LastCommit: c.last,
		})
	}
	slices.SortFunc(out, func(a, b types.OwnershipEntry) int {
		if d := cmp.Compare(b.PrimaryShare, a.PrimaryShare); d != 0 {
			return d
		}
		if d := cmp.Compare(b.Commits, a.Commits); d != 0 {
			return d
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return out
}

// Trend splits the window at its midpoint and ranks projects by the change in
// activity between the two halves (rising first).
func Trend(scan []ScannedCommit) []types.TrendEntry {
	mid := midpoint(scan)
	type halves struct{ recent, earlier int }
	m := map[string]*halves{}
	for _, c := range scan {
		for _, p := range c.Projects {
			h := m[p]
			if h == nil {
				h = &halves{}
				m[p] = h
			}
			if c.Date.Before(mid) {
				h.earlier++
			} else {
				h.recent++
			}
		}
	}
	out := make([]types.TrendEntry, 0, len(m))
	for p, h := range m {
		out = append(out, types.TrendEntry{Path: p, Recent: h.recent, Earlier: h.earlier, Delta: h.recent - h.earlier})
	}
	slices.SortFunc(out, func(a, b types.TrendEntry) int {
		if d := cmp.Compare(b.Delta, a.Delta); d != 0 {
			return d
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return out
}

// Midpoint returns the time halfway between the oldest and newest commit in the scan,
// or the zero time when the scan carries no dated commits.
func Midpoint(scan []ScannedCommit) time.Time { return midpoint(scan) }

func midpoint(scan []ScannedCommit) time.Time {
	var lo, hi time.Time
	for _, c := range scan {
		if c.Date.IsZero() {
			continue
		}
		if lo.IsZero() || c.Date.Before(lo) {
			lo = c.Date
		}
		if c.Date.After(hi) {
			hi = c.Date
		}
	}
	if lo.IsZero() {
		return time.Time{}
	}
	return lo.Add(hi.Sub(lo) / 2)
}

// Complexity returns a whitespace-complexity proxy for the file at path: one point
// per non-blank line plus one per indentation level, so size and nesting both count.
// Unreadable files (e.g. deleted within the window) score 0.
func Complexity(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	total := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimLeft(line, " \t") == "" {
			continue
		}
		indent := 0
		for _, r := range line {
			if r == ' ' {
				indent++
				continue
			}
			if r == '\t' {
				indent += 4
				continue
			}
			break
		}
		total += 1 + indent/4
	}
	return total
}

// parseSince converts a friendly window ("90d", "12w", "6mo", "1y") into an RFC3339
// lower bound for the VCS scan. Empty input means no bound. A month is approximated
// as 30 days and a year as 365.
func parseSince(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || i == len(s) {
		return "", fmt.Errorf("insight: invalid --since %q (use e.g. 90d, 12w, 6mo, 1y)", s)
	}
	day := 24 * time.Hour
	var unit time.Duration
	switch s[i:] {
	case "d":
		unit = day
	case "w":
		unit = 7 * day
	case "mo":
		unit = 30 * day
	case "y":
		unit = 365 * day
	default:
		return "", fmt.Errorf("insight: invalid --since unit in %q (use d, w, mo, or y)", s)
	}
	return time.Now().Add(-time.Duration(n) * unit).Format(time.RFC3339), nil
}
