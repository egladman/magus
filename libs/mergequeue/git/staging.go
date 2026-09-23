package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus/libs/mergequeue"
)

// StagingRepo is the [mergequeue.StagingRepo] over a checkout. Safe for concurrent use.
type StagingRepo struct {
	repo
	scratch    string
	regenerate mergequeue.RegenerateFunc

	worktrees sync.Mutex // git serializes worktree bookkeeping badly under contention
	seq       atomic.Int64
}

var _ mergequeue.StagingRepo = (*StagingRepo)(nil)

// NewStagingRepo stages in worktrees under scratch, which must lie outside cfg.Root: a
// stage inside the checkout would be discovered as a second copy of it. An empty
// scratch makes a repo that plans but cannot stage. regenerate runs when a change
// touches or conflicts in a derived file; nil leaves derived files as merged, which is
// only right when nothing derives them.
func NewStagingRepo(cfg Config, scratch string, regenerate mergequeue.RegenerateFunc) (*StagingRepo, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	return &StagingRepo{repo: repo{cfg}, scratch: scratch, regenerate: regenerate}, nil
}

// stagingIdent authors the staging commits with a fixed identity and date, so the same
// changes stacked on the same base yield the same commit in every job that stages them.
// They are never pushed; a landing commit is authored by the change's head author.
var stagingIdent = []string{
	"GIT_AUTHOR_NAME=merge queue", "GIT_AUTHOR_EMAIL=queue@mergequeue.invalid",
	"GIT_COMMITTER_NAME=merge queue", "GIT_COMMITTER_EMAIL=queue@mergequeue.invalid",
	"GIT_AUTHOR_DATE=@946684800 +0000", "GIT_COMMITTER_DATE=@946684800 +0000",
}

// Changed lists the paths head changes since its merge base with onto.
func (s *StagingRepo) Changed(ctx context.Context, onto, head string) ([]string, error) {
	out, err := s.run(ctx, s.Root, nil, nil, "diff", "--name-only", "-z", onto+"..."+head, "--")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// CheckMerge merges c onto baseCommit without a checkout.
func (s *StagingRepo) CheckMerge(ctx context.Context, baseCommit string, c mergequeue.Change) error {
	_, conflicts, err := s.mergeTree(ctx, nil, baseCommit, c.Head)
	if err != nil {
		return err
	}
	src, err := s.sources(ctx, baseCommit, conflicts)
	if err != nil {
		return err
	}
	if len(src) > 0 {
		return &mergequeue.ConflictError{Conflict: mergequeue.Conflict{Change: c, Paths: src, With: s.touching(ctx, baseCommit, c.Head, src)}}
	}
	return nil
}

// touching names commits on side, since it forked from other, that changed paths.
func (s *StagingRepo) touching(ctx context.Context, side, other string, paths []string) []string {
	mb, err := s.run(ctx, s.Root, nil, nil, "merge-base", side, other)
	if err != nil {
		return nil
	}
	out, err := s.run(ctx, s.Root, nil, nil, append([]string{"log", "--max-count=10", "--no-merges", "--format=%h %s", mb + ".." + side, "--"}, paths...)...)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// Stage checks out onto in a worktree of its own, merges c, regenerates, and commits.
func (s *StagingRepo) Stage(ctx context.Context, baseCommit, onto string, c mergequeue.Change) (mergequeue.Stage, error) {
	if err := c.Check(); err != nil {
		return mergequeue.Stage{}, err
	}
	if s.scratch == "" {
		return mergequeue.Stage{}, errors.New("git: this StagingRepo was built without a scratch directory, so it cannot stage")
	}
	dir := filepath.Join(s.scratch, fmt.Sprintf("stage-%d-%s", s.seq.Add(1), c.ID))
	s.worktrees.Lock()
	_, err := s.run(ctx, s.Root, nil, nil, "worktree", "add", "--quiet", "--detach", dir, onto)
	s.worktrees.Unlock()
	if err != nil {
		return mergequeue.Stage{}, err
	}
	st := mergequeue.Stage{Dir: dir}
	commit, err := s.build(ctx, dir, baseCommit, onto, c)
	if err != nil {
		_ = s.Discard(context.WithoutCancel(ctx), st)
		return mergequeue.Stage{}, err
	}
	st.Commit = commit
	return st, nil
}

func (s *StagingRepo) build(ctx context.Context, dir, baseCommit, onto string, c mergequeue.Change) (string, error) {
	settled, err := s.merge(ctx, dir, baseCommit, onto, c)
	if err != nil {
		return "", err
	}
	touched, err := s.Changed(ctx, onto, c.Head)
	if err != nil {
		return "", err
	}
	all := slices.Compact(slices.Sorted(slices.Values(slices.Concat(touched, settled))))
	derived, err := s.derived(ctx, baseCommit, all)
	if err != nil {
		return "", err
	}
	var regen []string
	for _, p := range all {
		if derived[p] {
			regen = append(regen, p)
		}
	}
	if len(regen) > 0 && s.regenerate != nil {
		if err := s.regen(ctx, dir, baseCommit, onto, c, regen); err != nil {
			return "", err
		}
	}
	return s.run(ctx, dir, nil, nil, "rev-parse", "HEAD")
}

// merge merges c into the stage. Conflicts in derived files take c's side, since
// regeneration rewrites them next; a conflict in any other file is a
// *mergequeue.ConflictError.
func (s *StagingRepo) merge(ctx context.Context, dir, baseCommit, onto string, c mergequeue.Change) ([]string, error) {
	if _, err := s.run(ctx, dir, stagingIdent, nil, "merge", "--quiet", "--no-ff", "--no-edit", "-m", "merge queue: stage #"+c.ID, c.Head); err == nil {
		return nil, nil
	}
	out, err := s.run(ctx, dir, nil, nil, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	conflicted := splitNUL(out)
	if len(conflicted) == 0 {
		return nil, fmt.Errorf("merging #%s onto %s failed without a conflict", c.ID, onto)
	}
	src, err := s.sources(ctx, baseCommit, conflicted)
	if err != nil {
		return nil, err
	}
	if len(src) > 0 {
		_, _ = s.run(ctx, dir, nil, nil, "merge", "--abort")
		return nil, &mergequeue.ConflictError{Conflict: mergequeue.Conflict{Change: c, Paths: src, With: s.touching(ctx, onto, c.Head, src)}}
	}
	for _, p := range conflicted {
		if _, err := s.run(ctx, dir, nil, nil, "checkout", "--theirs", "--", p); err != nil {
			// c deleted it: the deletion stands until regeneration says otherwise.
			if _, err := s.run(ctx, dir, nil, nil, "rm", "--quiet", "--cached", "--", p); err != nil {
				return nil, err
			}
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(p)))
			continue
		}
		if _, err := s.run(ctx, dir, nil, nil, "add", "--", p); err != nil {
			return nil, err
		}
	}
	if _, err := s.run(ctx, dir, stagingIdent, nil, "commit", "--quiet", "--no-edit"); err != nil {
		return nil, err
	}
	return conflicted, nil
}

// regen runs the hook, then commits what it rewrote. A rewrite of anything baseCommit
// does not mark derived is refused: the queue adds only regenerated files to what was
// approved.
func (s *StagingRepo) regen(ctx context.Context, dir, baseCommit, onto string, c mergequeue.Change, paths []string) error {
	if err := s.regenerate(ctx, dir, onto, c, paths); err != nil {
		return err
	}
	out, err := s.run(ctx, dir, nil, nil, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	var written []string
	entries := splitNUL(out)
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the rename's source follows as its own field
		}
		written = append(written, e[3:])
	}
	if len(written) == 0 {
		return nil
	}
	stray, err := s.sources(ctx, baseCommit, written)
	if err != nil {
		return err
	}
	if len(stray) > 0 {
		return &mergequeue.RefusedError{Reason: "regeneration wrote files that are not derived: " + strings.Join(stray, ", ")}
	}
	if _, err := s.run(ctx, dir, nil, nil, append([]string{"add", "-A", "--"}, written...)...); err != nil {
		return err
	}
	_, err = s.run(ctx, dir, stagingIdent, nil, "commit", "--quiet", "-m", "merge queue: regenerate derived files")
	return err
}

// Discard removes a stage's worktree.
func (s *StagingRepo) Discard(ctx context.Context, st mergequeue.Stage) error {
	if st.Dir == "" {
		return nil
	}
	s.worktrees.Lock()
	defer s.worktrees.Unlock()
	_, err := s.run(ctx, s.Root, nil, nil, "worktree", "remove", "--force", st.Dir)
	return err
}

// SquashMessage is GitHub's default squash body for head's own commits: one
// "* subject" paragraph each, oldest first, merges left out.
func (s *StagingRepo) SquashMessage(ctx context.Context, baseCommit, head string) (string, error) {
	out, err := s.run(ctx, s.Root, nil, nil, "log", "--reverse", "--no-merges", "--format=%s", baseCommit+".."+head, "--")
	if err != nil {
		return "", err
	}
	var parts []string
	for line := range strings.SplitSeq(out, "\n") {
		if line != "" {
			parts = append(parts, "* "+line)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// exportRefs is where Export parks a stage while packing it; git bundles only named refs.
const exportRefs = "refs/mergequeue/export/"

// Export writes stage and every commit beneath it that baseCommit lacks to file as a git
// bundle, so landing imports the validated commits instead of rebuilding anything. Its
// signature is a verdicts.ExportFunc.
func (s *StagingRepo) Export(ctx context.Context, file, baseCommit, stage string) error {
	ref := fmt.Sprintf("%s%d", exportRefs, s.seq.Add(1))
	if _, err := s.run(ctx, s.Root, nil, nil, "update-ref", ref, stage); err != nil {
		return err
	}
	defer func() { _, _ = s.run(context.WithoutCancel(ctx), s.Root, nil, nil, "update-ref", "-d", ref) }()
	_, err := s.run(ctx, s.Root, nil, nil, "bundle", "create", "--quiet", file, ref, "^"+baseCommit)
	return err
}

// Prune drops the bookkeeping of stage worktrees whose directories are gone.
func (s *StagingRepo) Prune(ctx context.Context) error {
	_, err := s.run(ctx, s.Root, nil, nil, "worktree", "prune")
	return err
}
