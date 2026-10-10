package vcs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

var (
	_ types.DiffStater = gitVCS{}
	_ types.DiffStater = hgVCS{}
	_ types.DiffStater = saplingVCS{}
	_ types.DiffStater = jjVCS{}
)

// DiffStat implements types.DiffStater with `git diff --numstat` from the merge base to
// HEAD. Unlike ChangedFiles it never fetches: a shallow clone missing the merge base is
// an error here, since a caller counting lines for a budget cannot wait on the network.
//
// --no-textconv because a textconv driver would count the converted text of a file git
// itself calls binary.
func (v gitVCS) DiffStat(ctx context.Context, dir, base string) ([]types.FileStat, error) {
	if base == "" {
		base = v.Base()
	}
	if err := checkRev(base); err != nil {
		return nil, err
	}
	mergeBase, err := gitOutput(ctx, dir, gitOpts{}, "merge-base", "--end-of-options", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git merge-base %s HEAD: %w", base, err)
	}
	out, err := gitOutput(ctx, dir, gitOpts{}, "diff", "--numstat", "-z", "--no-renames", "--no-textconv",
		"--ignore-submodules=none", mergeBase, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat %s HEAD: %w", mergeBase, err)
	}
	return parseNumstat(out)
}

// parseNumstat reads `git diff --numstat -z --no-renames`: one "<added>\t<deleted>\t<path>"
// record per file, NUL terminated, with "-" for both counts of a binary file.
func parseNumstat(out string) ([]types.FileStat, error) {
	stats := []types.FileStat{}
	for _, rec := range splitNUL(out) {
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("git diff --numstat: unreadable record %q", rec)
		}
		st := types.FileStat{Path: parts[2]}
		if parts[0] == "-" && parts[1] == "-" {
			st.Binary = true
		} else {
			var aerr, derr error
			st.Added, aerr = strconv.Atoi(parts[0])
			st.Deleted, derr = strconv.Atoi(parts[1])
			if err := errors.Join(aerr, derr); err != nil {
				return nil, fmt.Errorf("git diff --numstat: unreadable counts in %q: %w", rec, err)
			}
		}
		stats = append(stats, st)
	}
	return sortedStats(stats), nil
}

// DiffStat implements types.DiffStater from `hg diff` between ancestor(base,.) and `.`.
func (v hgVCS) DiffStat(ctx context.Context, dir, base string) ([]types.FileStat, error) {
	if base == "" {
		base = v.Base()
	}
	return hgFamilyDiffStat(ctx, "hg", dir, base)
}

// DiffStat implements types.DiffStater the way hg's does; Sapling inherits the revset and
// the diff command.
func (v saplingVCS) DiffStat(ctx context.Context, dir, base string) ([]types.FileStat, error) {
	if base == "" {
		base = v.Base()
	}
	return hgFamilyDiffStat(ctx, "sl", dir, base)
}

// hgFamilyDiffStat counts a plain `diff` rather than a git-format one. In git format both
// backends print a recorded rename as a header with no hunks, whose line counts are then
// unknowable from the diff; plain format spells it as the delete and the add that
// DiffStater promises. diff.git=0 overrides a user config that turns git format on, and
// Sapling's default.
//
// `.` is the working copy's parent and both revisions are named, so the working copy is
// never read. Paths are root-relative whatever dir is.
func hgFamilyDiffStat(ctx context.Context, prog, dir, base string) ([]types.FileStat, error) {
	if err := checkRequiredRevsetRef(base); err != nil {
		return nil, err
	}
	out, err := vcsOutputRaw(ctx, dir, prog, "diff", "--config", "diff.git=0",
		"-r", "ancestor("+base+",.)", "-r", ".")
	if err != nil {
		return nil, fmt.Errorf("%s diff ancestor(%s,.)-.: %w", prog, base, err)
	}
	entries, err := parseUnifiedStats(out)
	if err != nil {
		return nil, fmt.Errorf("%s diff: %w", prog, err)
	}
	stats := make([]types.FileStat, 0, len(entries))
	for _, e := range entries {
		if e.from != "" {
			return nil, fmt.Errorf("%s diff: %s is reported as moved from %s, which a plain diff never does", prog, e.Path, e.from)
		}
		stats = append(stats, e.FileStat)
	}
	return sortedStats(stats), nil
}

// DiffStat implements types.DiffStater from `jj diff --git` between the merge base and @.
// @ is jj's checked-out revision and holds the working copy's files, so unlike the other
// backends its uncommitted edits count.
//
// jj has no switch that turns off rename detection, so a rename arrives as a header with
// no hunks. The delete and the add DiffStater promises are counted from the files at
// each end instead.
func (v jjVCS) DiffStat(ctx context.Context, dir, base string) ([]types.FileStat, error) {
	if base == "" {
		base = v.Base()
	}
	if err := checkRef(base); err != nil {
		return nil, err
	}
	root, _, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
	// base resolves alone, so a default like trunk() is not interpolated into the revset
	// below, where checkRevsetRef would refuse its parentheses.
	baseID, err := jjSingleCommit(ctx, root, base)
	if err != nil {
		return nil, err
	}
	forkID, err := jjSingleCommit(ctx, root, "heads(::"+baseID+" & ::@)")
	if err != nil {
		return nil, fmt.Errorf("merge base of %s and @: %w", base, err)
	}
	out, err := vcsOutputRaw(ctx, root, "jj", "diff", "--git", "--from", forkID, "--to", "@")
	if err != nil {
		return nil, fmt.Errorf("jj diff --from %s --to @: %w", forkID, err)
	}
	entries, err := parseUnifiedStats(out)
	if err != nil {
		return nil, fmt.Errorf("jj diff: %w", err)
	}
	stats := make([]types.FileStat, 0, len(entries))
	for _, e := range entries {
		if e.from == "" {
			stats = append(stats, e.FileStat)
			continue
		}
		if !e.copied {
			lines, binary, err := jjFileLines(ctx, root, forkID, e.from)
			if err != nil {
				return nil, err
			}
			stats = append(stats, types.FileStat{Path: e.from, Deleted: lines, Binary: binary})
		}
		lines, binary, err := jjFileLines(ctx, root, "@", e.Path)
		if err != nil {
			return nil, err
		}
		stats = append(stats, types.FileStat{Path: e.Path, Added: lines, Binary: binary})
	}
	return sortedStats(stats), nil
}

// jjSingleCommit resolves revset to the one commit id it names. Several is an error:
// a criss-cross merge has no one base, and a count taken from an arbitrary one of them
// would be a number nobody can reproduce.
func jjSingleCommit(ctx context.Context, root, revset string) (string, error) {
	out, err := vcsOutput(ctx, root, "jj", "log", "-r", revset, "--no-graph", "-T", `commit_id ++ "\n"`)
	if err != nil {
		return "", fmt.Errorf("jj log -r %s: %w", revset, err)
	}
	ids := strings.Fields(out)
	if len(ids) != 1 {
		return "", fmt.Errorf("jj: %s names %d revisions, want exactly one", revset, len(ids))
	}
	return ids[0], nil
}

// jjFileLines counts the lines of the file at path as of rev. A file holding a NUL byte is
// binary, as git decides, and has no line count.
func jjFileLines(ctx context.Context, root, rev, path string) (lines int, binary bool, err error) {
	cmd := vcsExec(ctx, "jj", "file", "show", "-r", rev, "root-file:"+strconv.Quote(path))
	cmd.Dir = root
	body, err := revFileOutput(cmd, "jj file show "+path+" at "+rev)
	if err != nil {
		return 0, false, err
	}
	if strings.IndexByte(body, 0) >= 0 {
		return 0, true, nil
	}
	lines = strings.Count(body, "\n")
	if body != "" && !strings.HasSuffix(body, "\n") {
		lines++
	}
	return lines, false, nil
}

func sortedStats(stats []types.FileStat) []types.FileStat {
	slices.SortFunc(stats, func(a, b types.FileStat) int { return strings.Compare(a.Path, b.Path) })
	return stats
}

// diffEntry is one file of a parsed unified diff.
type diffEntry struct {
	types.FileStat
	// from is the path a rename or copy header named as its source. A diff that records
	// one prints no hunks for an unedited file, so the counts here are not the file's.
	from string
	// copied marks from as surviving: the new path is then an add, not a move.
	copied bool
	// minus is the old side's name from a --- line, for a path the diff header gives
	// ambiguously.
	minus string
}

// parseUnifiedStats counts the lines each file of a git-style or Mercurial plain unified
// diff adds and deletes. Hunks are consumed by the lengths their @@ line declares, so a
// content line that reads like a header ("--- x" for a deleted "-- x") is never mistaken
// for one.
func parseUnifiedStats(diff string) ([]diffEntry, error) {
	var entries []diffEntry
	lines := strings.Split(diff, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "diff --git "):
			entries = append(entries, diffEntry{FileStat: types.FileStat{Path: equalNamePath(strings.TrimPrefix(line, "diff --git "))}})
		case strings.HasPrefix(line, "diff -r "):
			// diff -r <rev> -r <rev> <path>
			f := strings.SplitN(line, " ", 6)
			if len(f) != 6 {
				return nil, fmt.Errorf("unreadable file header %q", line)
			}
			entries = append(entries, diffEntry{FileStat: types.FileStat{Path: f[5]}})
		case len(entries) == 0:
		case strings.HasPrefix(line, "@@ "):
			var err error
			if i, err = countHunk(lines, i, &entries[len(entries)-1]); err != nil {
				return nil, err
			}
		default:
			entries[len(entries)-1].readHeader(line)
		}
	}
	for _, e := range entries {
		if e.Path == "" {
			return nil, errors.New("a file header names no path")
		}
	}
	return entries, nil
}

// equalNamePath returns P from "a/P b/P", the one shape of a git file header that splits
// unambiguously when P holds spaces. A rename, or a quoted name, returns "" and takes its
// path from the rename and +++ lines.
func equalNamePath(names string) string {
	n := len(names)
	if n < 5 || n%2 == 0 {
		return ""
	}
	p := names[2 : 2+(n-5)/2]
	if names != "a/"+p+" b/"+p {
		return ""
	}
	return p
}

func (e *diffEntry) readHeader(line string) {
	switch {
	case strings.HasPrefix(line, "rename from "):
		e.from = unquoteName(strings.TrimPrefix(line, "rename from "))
	case strings.HasPrefix(line, "rename to "):
		e.Path = unquoteName(strings.TrimPrefix(line, "rename to "))
	case strings.HasPrefix(line, "copy from "):
		e.from, e.copied = unquoteName(strings.TrimPrefix(line, "copy from ")), true
	case strings.HasPrefix(line, "copy to "):
		e.Path = unquoteName(strings.TrimPrefix(line, "copy to "))
	case strings.HasPrefix(line, "--- "):
		e.minus = sideName(strings.TrimPrefix(line, "--- "), "a/")
	case strings.HasPrefix(line, "+++ "):
		if name := sideName(strings.TrimPrefix(line, "+++ "), "b/"); name != "" {
			e.Path = name
		} else if e.minus != "" {
			e.Path = e.minus
		}
	case line == "GIT binary patch", strings.HasPrefix(line, "Binary file"):
		e.Binary = true
	}
}

// sideName is the path a --- or +++ line names, without the a/ or b/ prefix and the
// tab-separated date Mercurial appends; "" for /dev/null.
func sideName(s, prefix string) string {
	if name, _, ok := strings.Cut(s, "\t"); ok && !strings.HasPrefix(s, `"`) {
		s = name
	}
	s = unquoteName(s)
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}

// unquoteName undoes the C-style quoting a diff gives a path with unusual bytes.
func unquoteName(s string) string {
	if q, err := strconv.Unquote(s); err == nil && strings.HasPrefix(s, `"`) {
		return q
	}
	return s
}

// countHunk adds the hunk whose @@ line is lines[i] to e and returns the index of its
// last line.
func countHunk(lines []string, i int, e *diffEntry) (int, error) {
	oldLeft, newLeft, ok := hunkLengths(lines[i])
	if !ok {
		return i, fmt.Errorf("unreadable hunk header %q", lines[i])
	}
	for oldLeft > 0 || newLeft > 0 {
		i++
		if i >= len(lines) {
			return i, fmt.Errorf("diff ends inside a hunk of %s", e.Path)
		}
		switch {
		case strings.HasPrefix(lines[i], "+"):
			e.Added++
			newLeft--
		case strings.HasPrefix(lines[i], "-"):
			e.Deleted++
			oldLeft--
		case strings.HasPrefix(lines[i], `\`):
		default:
			oldLeft--
			newLeft--
		}
	}
	return i, nil
}

// hunkLengths reads the two line counts of "@@ -a[,b] +c[,d] @@", each 1 when omitted.
func hunkLengths(header string) (oldLen, newLen int, ok bool) {
	f := strings.Fields(header)
	if len(f) < 3 || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, false
	}
	oldLen, ok = rangeLength(f[1][1:])
	if !ok {
		return 0, 0, false
	}
	newLen, ok = rangeLength(f[2][1:])
	return oldLen, newLen, ok
}

func rangeLength(r string) (int, bool) {
	_, length, found := strings.Cut(r, ",")
	if !found {
		return 1, true
	}
	n, err := strconv.Atoi(length)
	return n, err == nil && n >= 0
}
