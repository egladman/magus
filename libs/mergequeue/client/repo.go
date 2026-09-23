// Package client implements the merge queue's host interfaces with magus: [Repo] is the
// [mergequeue.VCS], through magus's vcs package and whichever backend magus detects, and
// [Workspace] is the build tool's facts, through magus's Go SDK.
package client

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/libs/mergequeue"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Config names the checkout a [Repo] works in.
type Config struct {
	Root   string // the checkout whose store backs every candidate
	Remote string // name of the configured remote changes and the base are fetched from
}

// Repo is the queue's version control over one checkout. It only translates: every
// choice of merge base, conflict and review is the queue's. Safe for concurrent use.
type Repo struct {
	cfg Config
	drv types.VCSDriver
}

var _ mergequeue.VCS = (*Repo)(nil)

// NewRepo detects cfg.Root's version control system the way magus does, and fails when
// that backend cannot serve the queue or cfg.Remote names no configured remote.
func NewRepo(ctx context.Context, cfg Config) (*Repo, error) {
	if cfg.Root == "" || cfg.Remote == "" {
		return nil, errors.New("a Repo needs a Root and a Remote")
	}
	res, err := vcs.Resolve(ctx, cfg.Root, "", types.VCSOptions{})
	if err != nil {
		return nil, err
	}
	if res.VCS == nil {
		return nil, fmt.Errorf("%s: version control is disabled", cfg.Root)
	}
	r := &Repo{cfg: cfg, drv: res.VCS}
	// Cheap reads, so a backend that declines what the queue needs, a Root that is no
	// checkout, or a remote nobody configured fails here, not mid-run.
	if _, err := r.drv.Checkouts(ctx, cfg.Root); err != nil {
		return nil, fmt.Errorf("%s: %w", cfg.Root, err)
	}
	if _, err := r.drv.RemoteURL(ctx, cfg.Root, cfg.Remote); err != nil {
		if errors.Is(err, types.ErrVCSUnsupported) {
			return nil, fmt.Errorf("%s: no remote named %q is configured", cfg.Root, cfg.Remote)
		}
		return nil, fmt.Errorf("%s: %w", cfg.Root, err)
	}
	return r, nil
}

// RemoteURL is the remote's URL, which names the repository to a provider.
func (r *Repo) RemoteURL(ctx context.Context) (string, error) {
	return r.drv.RemoteURL(ctx, r.cfg.Root, r.cfg.Remote)
}

// RemoveCheckouts removes every checkout this Repo's backend made under dir, such as a
// scratch directory a run leaves behind, and no other.
func (r *Repo) RemoveCheckouts(ctx context.Context, dir string) error {
	dirs, err := r.drv.Checkouts(ctx, r.cfg.Root)
	if err != nil {
		return err
	}
	under, err := filepath.EvalSymlinks(dir)
	if err != nil {
		under = dir
	}
	var errs []error
	for _, d := range dirs {
		real, err := filepath.EvalSymlinks(d)
		if err != nil {
			real = d
		}
		if rel, err := filepath.Rel(under, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		errs = append(errs, r.drv.RemoveCheckout(ctx, r.cfg.Root, d))
	}
	return errors.Join(errs...)
}

func (r *Repo) FetchRef(ctx context.Context, ref string) (string, error) {
	return r.drv.FetchRef(ctx, r.cfg.Root, r.cfg.Remote, ref)
}

func (r *Repo) FetchCommit(ctx context.Context, id string) error {
	return r.drv.FetchCommit(ctx, r.cfg.Root, r.cfg.Remote, id)
}

func (r *Repo) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	return r.drv.IsAncestor(ctx, r.cfg.Root, ancestor, descendant)
}

func (r *Repo) RangeFiles(ctx context.Context, base, head string) ([]string, error) {
	return r.drv.RangeFiles(ctx, r.cfg.Root, base, head, nil)
}

func (r *Repo) RangeCommits(ctx context.Context, base, head string, paths []string) ([]mergequeue.Commit, error) {
	cs, err := r.drv.RangeCommits(ctx, r.cfg.Root, base, head, paths)
	if err != nil {
		return nil, err
	}
	out := make([]mergequeue.Commit, len(cs))
	for i, c := range cs {
		out[i] = commit(c)
	}
	return out, nil
}

func commit(c types.Commit) mergequeue.Commit {
	return mergequeue.Commit{ID: c.ID, Parents: c.Parents, Author: mergequeue.Person(c.Author), Subject: c.Subject}
}

func (r *Repo) FindCommit(ctx context.Context, rev string) (mergequeue.Commit, error) {
	c, err := r.drv.FindCommit(ctx, r.cfg.Root, rev)
	if err != nil {
		return mergequeue.Commit{}, err
	}
	return commit(c), nil
}

func (r *Repo) TreeID(ctx context.Context, rev string) (string, error) {
	return r.drv.TreeID(ctx, r.cfg.Root, rev)
}

func (r *Repo) DiffTrees(ctx context.Context, a, b string) ([]string, error) {
	return r.drv.DiffTrees(ctx, r.cfg.Root, a, b)
}

func (r *Repo) MergeTrees(ctx context.Context, m mergequeue.TreeMerge) (mergequeue.TreeMergeResult, error) {
	res, err := r.drv.MergeTrees(ctx, r.cfg.Root, types.TreeMerge(m))
	if err != nil {
		return mergequeue.TreeMergeResult{}, err
	}
	out := mergequeue.TreeMergeResult{Tree: res.Tree}
	for _, c := range res.Conflicts {
		out.Conflicts = append(out.Conflicts, c.Path)
	}
	return out, nil
}

func (r *Repo) GeneratedPaths(ctx context.Context, rev string, paths []string) (map[string]bool, error) {
	return r.drv.GeneratedPaths(ctx, r.cfg.Root, rev, paths)
}

func (r *Repo) CreateCheckout(ctx context.Context, dir, rev string) error {
	return r.drv.CreateCheckout(ctx, r.cfg.Root, dir, rev)
}

func (r *Repo) RemoveCheckout(ctx context.Context, dir string) error {
	return r.drv.RemoveCheckout(ctx, r.cfg.Root, dir)
}

func (r *Repo) StartMerge(ctx context.Context, dir, rev string) error {
	return r.drv.StartMerge(ctx, dir, rev)
}

func (r *Repo) AbortMerge(ctx context.Context, dir string) error {
	return r.drv.AbortMerge(ctx, dir)
}

func (r *Repo) Conflicts(ctx context.Context, dir string) ([]mergequeue.ConflictedPath, error) {
	cs, err := r.drv.Conflicts(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]mergequeue.ConflictedPath, len(cs))
	for i, c := range cs {
		out[i] = mergequeue.ConflictedPath{Path: c.Path, Deleted: c.Kind != types.ConflictKindContent}
	}
	return out, nil
}

func (r *Repo) KeepIncoming(ctx context.Context, dir string, paths []string) error {
	return r.drv.KeepIncoming(ctx, dir, paths)
}

func (r *Repo) MarkResolved(ctx context.Context, dir string, paths []string) error {
	return r.drv.MarkResolved(ctx, dir, paths)
}

func (r *Repo) RemoveConflicts(ctx context.Context, dir string, paths []string) error {
	return r.drv.RemoveConflicts(ctx, dir, paths)
}

func (r *Repo) DirtyFiles(ctx context.Context, dir string) ([]string, error) {
	return r.drv.DirtyFiles(ctx, dir, nil)
}

func meta(m mergequeue.CommitMeta) types.CommitMeta {
	return types.CommitMeta{Message: m.Message, Author: types.Person(m.Author), Committer: types.Person(m.Committer), Date: m.Date}
}

func (r *Repo) Commit(ctx context.Context, dir string, c mergequeue.CheckoutCommit) (string, error) {
	return r.drv.Commit(ctx, dir, types.CheckoutCommit{CommitMeta: meta(c.CommitMeta), Paths: c.Paths})
}

func (r *Repo) CommitTree(ctx context.Context, c mergequeue.TreeCommit) (string, error) {
	return r.drv.CommitTree(ctx, r.cfg.Root, types.TreeCommit{CommitMeta: meta(c.CommitMeta), Tree: c.Tree, Parents: c.Parents})
}

func (r *Repo) Push(ctx context.Context, p mergequeue.PushLease) error {
	err := r.drv.Push(ctx, r.cfg.Root, types.PushLease{Remote: r.cfg.Remote, Ref: p.Ref, To: p.To, Expected: p.Expected})
	if errors.Is(err, types.ErrStaleLease) {
		return fmt.Errorf("%w: %w", mergequeue.ErrStaleLease, err)
	}
	return err
}

func (r *Repo) Bundle(ctx context.Context, file string, b mergequeue.BundleRange) error {
	return r.drv.Bundle(ctx, r.cfg.Root, file, types.BundleRange(b))
}

func (r *Repo) Unbundle(ctx context.Context, file string) error {
	return r.drv.Unbundle(ctx, r.cfg.Root, file)
}
