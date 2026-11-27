package vcs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// contentReader is what [DifferingPaths] reads, which every backend answers.
type contentReader interface {
	types.TreeReporter
	types.RevisionFileReader
}

// DifferingPaths is those of paths that differ between revisions a and b of
// dir's repository, cleaned and in order. A path neither holds reads the same,
// and a path one side holds differs. A backend declining the tree diff reads
// each path, which cannot see a mode change. An error means no answer: the
// tree diff or a file read failed, or ctx ended.
func DifferingPaths(ctx context.Context, r contentReader, dir, a, b string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	differ, err := r.DiffTrees(ctx, dir, a, b)
	var declined *types.VCSUnsupportedError
	if err != nil && !errors.As(err, &declined) {
		return nil, err
	}
	readEach := err != nil
	var out []string
	for _, changed := range paths {
		p := path.Clean(filepath.ToSlash(strings.TrimSpace(changed)))
		if !readEach {
			if slices.Contains(differ, p) {
				out = append(out, p)
			}
			continue
		}
		ours, oursErr := r.ReadFileAt(ctx, dir, a, p)
		theirs, theirsErr := r.ReadFileAt(ctx, dir, b, p)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if oursErr != nil && !errors.Is(oursErr, ErrObjectMissing) {
			return nil, fmt.Errorf("read %s at %s: %w", p, a, oursErr)
		}
		if theirsErr != nil && !errors.Is(theirsErr, ErrObjectMissing) {
			return nil, fmt.Errorf("read %s at %s: %w", p, b, theirsErr)
		}
		if (oursErr != nil) != (theirsErr != nil) || ours != theirs {
			out = append(out, p)
		}
	}
	return out, nil
}

// revisionReader is what [DifferingPathsByRevision] reads.
type revisionReader interface {
	contentReader
	FindCommit(ctx context.Context, dir, rev string) (types.Commit, error)
}

// manyTreesDiffer is a backend that compares one revision against many in a
// fixed number of processes.
type manyTreesDiffer interface {
	// diffTreesAgainst is DiffTrees between head and each of revisions the
	// repository holds. One it lacks has no entry; a head it lacks is an error.
	diffTreesAgainst(ctx context.Context, root, head string, revisions []string) (map[string][]string, error)
}

// DifferingPathsByRevision is, for each revision paths names, those of its
// paths that differ between it and head, as [DifferingPaths] answers each
// pair. A revision the repository lacks has no entry; a head it lacks is an
// error, as is ctx ending. git answers every revision in two processes;
// another backend is asked a revision at a time.
func DifferingPathsByRevision(ctx context.Context, r revisionReader, dir, head string, paths map[string][]string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(paths) == 0 {
		return out, nil
	}
	revisions := slices.Sorted(maps.Keys(paths))
	if many, ok := r.(manyTreesDiffer); ok {
		differ, err := many.diffTreesAgainst(ctx, dir, head, revisions)
		if err != nil {
			return nil, err
		}
		for rev, changed := range differ {
			out[rev] = pathsAmong(changed, paths[rev])
		}
		return out, nil
	}
	if _, err := r.FindCommit(ctx, dir, head); err != nil {
		return nil, fmt.Errorf("find %s: %w", head, err)
	}
	for _, rev := range revisions {
		if _, err := r.FindCommit(ctx, dir, rev); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		differ, err := DifferingPaths(ctx, r, dir, rev, head, paths[rev])
		if err != nil {
			return nil, err
		}
		out[rev] = differ
	}
	return out, nil
}

// pathsAmong is those of paths, cleaned and in order, that changed names.
func pathsAmong(changed, paths []string) []string {
	differ := make(map[string]bool, len(changed))
	for _, p := range changed {
		differ[p] = true
	}
	var out []string
	for _, p := range paths {
		if p = path.Clean(filepath.ToSlash(strings.TrimSpace(p))); differ[p] {
			out = append(out, p)
		}
	}
	return out
}

// MergedBranches is every local branch rev reaches, in one read. Such a branch
// lacks nothing rev has. Only git answers; another backend declines with
// *types.VCSUnsupportedError.
func MergedBranches(ctx context.Context, d types.VCSIdentity, dir, rev string) (map[string]bool, error) {
	if d.Name() != (gitVCS{}).Name() {
		return nil, &types.VCSUnsupportedError{VCS: d.Name(), Capability: types.CapBranchHeadReporter}
	}
	if rev == "" {
		return nil, fmt.Errorf("vcs: MergedBranches needs a revision")
	}
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	listed, err := gitOutput(ctx, dir, gitOpts{}, "for-each-ref", "--merged="+rev, "--format=%(refname:lstrip=2)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref --merged=%s: %w", rev, err)
	}
	merged := map[string]bool{}
	for _, branch := range splitLines([]byte(listed)) {
		if branch != "" {
			merged[branch] = true
		}
	}
	return merged, nil
}

// workReader is what missingCommits reads, which every backend answers.
type workReader interface {
	types.RangeReporter
	contentReader
}

// missingCommits is MissingCommits for every backend. It merges nothing, so
// no merge driver runs. Only commits since the two diverged can be copies of
// head's, so it reads no further back.
func missingCommits(ctx context.Context, r workReader, dir, branch, head string) (int, error) {
	commits, err := r.RangeCommits(ctx, dir, branch, head, nil)
	if err != nil {
		return 0, err
	}
	if len(commits) == 0 || contentOnBranch(ctx, r, dir, branch, head) {
		return 0, nil
	}
	onBranch, err := r.RangeCommits(ctx, dir, head, branch, nil)
	if err != nil {
		return 0, err
	}
	copies := make(map[string][]types.Commit, len(onBranch))
	for _, c := range onBranch {
		id := fmt.Sprintf("%s\x00%s", c.Author.Email, strings.TrimPrefix(c.Subject, "fixup! "))
		copies[id] = append(copies[id], c)
	}
	lines := &commitLines{r: r, dir: dir, read: map[string]map[string]bool{}}
	missing := 0
	for _, c := range commits {
		if len(c.Parents) > 1 {
			continue
		}
		held, err := heldByCopy(ctx, lines, c, copies[fmt.Sprintf("%s\x00%s", c.Author.Email, strings.TrimPrefix(c.Subject, "fixup! "))])
		if err != nil {
			return 0, err
		}
		if !held {
			missing++
		}
	}
	if missing > 0 {
		held, err := heldByNetPatch(ctx, r, lines, dir, branch, head)
		if err != nil {
			return 0, err
		}
		if held {
			return 0, nil
		}
	}
	return missing, nil
}

// heldByNetPatch reports one commit on branch carrying every line head
// changed since the split, as a squash does. Part of them is not held.
func heldByNetPatch(ctx context.Context, r workReader, lines *commitLines, dir, branch, head string) (bool, error) {
	diff, err := r.RangeDiff(ctx, dir, branch, head, nil)
	if err != nil {
		return false, err
	}
	net := diffLines(diff, head)
	if len(net) == 0 {
		return false, nil
	}
	paths, err := r.RangeFiles(ctx, dir, branch, head, nil)
	if err != nil {
		return false, err
	}
	candidates, err := r.RangeCommits(ctx, dir, head, branch, paths)
	if err != nil {
		return false, err
	}
	for _, c := range candidates {
		if len(c.Parents) != 1 {
			continue
		}
		theirs, err := lines.of(ctx, c)
		if err != nil {
			return false, err
		}
		if carriesAll(theirs, net) {
			return true, nil
		}
	}
	return false, nil
}

// heldByCopy reports a candidate carrying every line c changed. Author and
// subject alone repeat across unrelated commits. Only a subject match pays for
// the diffs. A rewritten copy reads as not held, the side that loses no work.
func heldByCopy(ctx context.Context, lines *commitLines, c types.Commit, candidates []types.Commit) (bool, error) {
	if len(candidates) == 0 || len(c.Parents) == 0 {
		return false, nil
	}
	ours, err := lines.of(ctx, c)
	if err != nil {
		return false, err
	}
	for _, candidate := range candidates {
		if len(candidate.Parents) == 0 {
			continue
		}
		theirs, err := lines.of(ctx, candidate)
		if err != nil {
			return false, err
		}
		if carriesAll(theirs, ours) {
			return true, nil
		}
	}
	return false, nil
}

// commitLines keeps each commit's changed lines for one missingCommits call,
// since commits sharing a subject compare against each other.
type commitLines struct {
	r    workReader
	dir  string
	read map[string]map[string]bool
}

func (l *commitLines) of(ctx context.Context, c types.Commit) (map[string]bool, error) {
	if lines, ok := l.read[c.ID]; ok {
		return lines, nil
	}
	lines, err := changedLines(ctx, l.r, l.dir, c)
	if err != nil {
		return nil, err
	}
	l.read[c.ID] = lines
	return lines, nil
}

func carriesAll(theirs, ours map[string]bool) bool {
	for line := range ours {
		if !theirs[line] {
			return false
		}
	}
	return true
}

// changedLines is what c changed against its first parent, without the hunk
// positions, context and similarity a replay shifts. A binary's patch says
// nothing but its blob ids. One with none carries c's id, so none matches it.
func changedLines(ctx context.Context, r workReader, dir string, c types.Commit) (map[string]bool, error) {
	diff, err := r.RangeDiff(ctx, dir, c.Parents[0], c.ID, nil)
	if err != nil {
		return nil, err
	}
	return diffLines(diff, c.ID), nil
}

// diffLines is changedLines over a patch already read. id stands in for a
// binary's missing blob ids.
func diffLines(diff, id string) map[string]bool {
	lines := map[string]bool{}
	blobs := ""
	for line := range strings.Lines(diff) {
		switch {
		case strings.HasPrefix(line, "diff "):
			blobs = ""
			lines[line] = true
		case strings.HasPrefix(line, "index "):
			blobs = line
		case strings.HasPrefix(line, "Binary"), strings.HasPrefix(line, "GIT binary patch"):
			lines[cmp.Or(blobs, id)+line] = true
		case line == "\n", strings.HasPrefix(line, " "), strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "\\"),
			strings.HasPrefix(line, "similarity index"), strings.HasPrefix(line, "dissimilarity index"):
		default:
			lines[line] = true
		}
	}
	return lines
}

// contentOnBranch reports whether every file head changed since it diverged
// reads the same at branch. A squash of several branches holds no commit of
// any of them. See [DifferingPaths] for what reads the same.
func contentOnBranch(ctx context.Context, r workReader, dir, branch, head string) bool {
	paths, err := r.RangeFiles(ctx, dir, branch, head, nil)
	if err != nil {
		return false
	}
	differ, err := DifferingPaths(ctx, r, dir, branch, head, paths)
	return err == nil && len(differ) == 0
}
