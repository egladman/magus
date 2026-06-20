package project

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
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
		for _, f := range workspaceRelative(prefix, normalizeFiles(c.Files)) {
			sc.Files = append(sc.Files, f)
			if p, ok := idx.projectForFile(f); ok {
				projSet[p] = struct{}{}
			}
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
func FileHotspots(scan []ScannedCommit, complexity func(rel string) int) []types.FileHotspot {
	counters := aggCounters(scan, true)
	out := make([]types.FileHotspot, 0, len(counters))
	for f, c := range counters {
		cx := complexity(f)
		out = append(out, types.FileHotspot{
			Path: f, Commits: c.commits, Complexity: cx, Score: c.commits * cx,
			Authors: len(c.authors), LastCommit: c.last,
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
func Ownership(scan []ScannedCommit, staleBefore time.Time) []types.Ownership {
	counters := aggCounters(scan, false)
	out := make([]types.Ownership, 0, len(counters))
	for p, c := range counters {
		primary, n := c.primary()
		share := 0
		if c.commits > 0 {
			share = n * 100 / c.commits
		}
		out = append(out, types.Ownership{
			Path: p, Commits: c.commits, Authors: len(c.authors),
			Primary: primary, PrimaryShare: share,
			BusFactor1: len(c.authors) == 1,
			Stale:      !staleBefore.IsZero() && c.last.Before(staleBefore),
			LastCommit: c.last,
		})
	}
	slices.SortFunc(out, func(a, b types.Ownership) int {
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
func Trend(scan []ScannedCommit) []types.Trend {
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
	out := make([]types.Trend, 0, len(m))
	for p, h := range m {
		out = append(out, types.Trend{Path: p, Recent: h.recent, Earlier: h.earlier, Delta: h.recent - h.earlier})
	}
	slices.SortFunc(out, func(a, b types.Trend) int {
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
			} else if r == '\t' {
				indent += 4
			} else {
				break
			}
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
