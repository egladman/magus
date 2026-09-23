package git

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/mergequeue"
)

// LandingRepo is the [mergequeue.LandingRepo] over a checkout. It runs plumbing only.
type LandingRepo struct{ repo }

var _ mergequeue.LandingRepo = (*LandingRepo)(nil)

// NewLandingRepo lands from cfg's checkout.
func NewLandingRepo(cfg Config) (*LandingRepo, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	return &LandingRepo{repo{cfg}}, nil
}

// ImportBundle loads a StagingRepo.Export's objects without creating any ref.
func (l *LandingRepo) ImportBundle(ctx context.Context, file string) error {
	if _, err := l.run(ctx, l.Root, nil, nil, "bundle", "verify", "--quiet", file); err != nil {
		return err
	}
	_, err := l.run(ctx, l.Root, nil, nil, "bundle", "unbundle", file)
	return err
}

// Predict merges stage's changes since baseCommit onto tip.
//
// A path that what landed since onto changed, and that the stage changed too or that
// the merge conflicts in, is a combination nobody validated: a derived file a disjoint
// partition regenerated while this stage regenerated it against the old base, say.
// Those are refused, and the change is restaged on the next run. Every other conflicted
// path is one tip still carries as onto had it, so the stage, built onto exactly that,
// holds its right content.
func (l *LandingRepo) Predict(ctx context.Context, baseCommit, tip, onto, stage string) (string, error) {
	tree, conflicts, err := l.mergeTree(ctx, []string{"--merge-base=" + baseCommit}, tip, stage)
	if err != nil {
		return "", err
	}
	landed, err := l.changedBetween(ctx, onto, tip)
	if err != nil {
		return "", err
	}
	if len(landed) > 0 {
		staged, err := l.changedBetween(ctx, onto, stage)
		if err != nil {
			return "", err
		}
		touched := make(map[string]bool, len(staged)+len(conflicts))
		for _, p := range slices.Concat(staged, conflicts) {
			touched[p] = true
		}
		var stale []string
		for _, p := range landed {
			if touched[p] {
				stale = append(stale, p)
			}
		}
		if len(stale) > 0 {
			return "", &mergequeue.ConflictError{Conflict: mergequeue.Conflict{Paths: stale}}
		}
	}
	if len(conflicts) == 0 {
		return tree, nil
	}
	return l.overlay(ctx, tree, stage, conflicts)
}

// overlay returns tree with paths replaced by their content in from (or removed where
// from has none), through a scratch index: plumbing only, no checkout.
func (l *LandingRepo) overlay(ctx context.Context, tree, from string, paths []string) (string, error) {
	index, err := os.CreateTemp("", "mergequeue-index-")
	if err != nil {
		return "", err
	}
	_ = index.Close()
	_ = os.Remove(index.Name()) // git creates it; an empty file is not an index
	defer os.Remove(index.Name())
	env := []string{"GIT_INDEX_FILE=" + index.Name()}
	if _, err := l.run(ctx, l.Root, env, nil, "read-tree", tree); err != nil {
		return "", err
	}
	for _, p := range paths {
		entry, err := l.run(ctx, l.Root, nil, nil, "ls-tree", from, "--", p)
		if err != nil {
			return "", err
		}
		if entry == "" {
			if _, err := l.run(ctx, l.Root, env, nil, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		// "<mode> blob <oid>\t<path>"
		meta, _, _ := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return "", errors.New("git ls-tree " + from + " -- " + p + ": unexpected " + entry)
		}
		if _, err := l.run(ctx, l.Root, env, nil, "update-index", "--add", "--cacheinfo", fields[0]+","+fields[2]+","+p); err != nil {
			return "", err
		}
	}
	return l.run(ctx, l.Root, env, nil, "write-tree")
}

// PushLanding picks the commit to merge. The plain merge is computed from the natural
// merge base, as the host's merge button computes it, while Predict applies the stage's
// delta since baseCommit: one asks what the host will do, the other what was validated.
func (l *LandingRepo) PushLanding(ctx context.Context, baseCommit, tip string, c mergequeue.Change, tree string) (string, error) {
	plain, conflicts, err := l.mergeTree(ctx, nil, tip, c.Head)
	if err != nil {
		return "", err
	}
	if len(conflicts) == 0 && plain == tree {
		return c.Head, nil
	}
	diff, err := l.changedBetween(ctx, plain, tree)
	if err != nil {
		return "", err
	}
	src, err := l.sources(ctx, baseCommit, diff)
	if err != nil {
		return "", err
	}
	if len(src) > 0 {
		return "", &mergequeue.RefusedError{Reason: "the validated tree differs from its merge outside derived files (" +
			strings.Join(src, ", ") + "), which the queue never lands."}
	}
	if c.Branch == "" {
		return "", &mergequeue.RefusedError{Reason: "its derived files need regenerating on top of `" + c.Base +
			"`, and the queue cannot push to its branch. Merge `" + c.Base + "` in, regenerate, push, and queue it again."}
	}
	ident, err := l.run(ctx, l.Root, nil, nil, "log", "-1", "--format=%an%x00%ae", c.Head, "--")
	if err != nil {
		return "", err
	}
	name, email, _ := strings.Cut(ident, "\x00")
	landing, err := l.run(ctx, l.Root, []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email}, nil,
		"commit-tree", tree, "-p", c.Head, "-p", tip, "-m", "merge "+c.Base+" and regenerate derived files")
	if err != nil {
		return "", err
	}
	// The lease holds the branch at the validated head: a push since validation, or a
	// branch deleted after its pull request closed, is rejected rather than overwritten
	// or recreated.
	branch := "refs/heads/" + c.Branch
	if _, err := l.run(ctx, l.Root, nil, nil, "push", "--quiet", "--force-with-lease="+branch+":"+c.Head, l.Remote, landing+":"+branch); err != nil {
		var ge *gitError
		if errors.As(err, &ge) && (strings.Contains(ge.stderr, "stale info") || strings.Contains(ge.stderr, "rejected")) {
			return "", &mergequeue.WaitError{Reason: "its branch moved or was deleted since validation"}
		}
		return "", err
	}
	return landing, nil
}
