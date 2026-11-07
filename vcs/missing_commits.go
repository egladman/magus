package vcs

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

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
		id := replayIdentity(c)
		copies[id] = append(copies[id], c)
	}
	lines := newCommitLines(r, dir)
	missing := 0
	for _, c := range commits {
		if len(c.Parents) > 1 {
			continue
		}
		held, err := heldByCopy(ctx, lines, c, copies[replayIdentity(c)])
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

func newCommitLines(r workReader, dir string) *commitLines {
	return &commitLines{r: r, dir: dir, read: map[string]map[string]bool{}}
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

// replayIdentity reads a fixup as the commit it folds into.
func replayIdentity(c types.Commit) string {
	return fmt.Sprintf("%s\x00%s", c.Author.Email, strings.TrimPrefix(c.Subject, "fixup! "))
}
