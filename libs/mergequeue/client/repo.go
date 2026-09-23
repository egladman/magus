// Package client implements the merge queue's host interfaces with magus: [Repo] is the
// [mergequeue.VCS], through magus's vcs package and whichever backend magus detects, and
// [Workspace] is the build tool's facts, through magus's Go SDK.
package client

import (
	"context"
	"errors"
	"fmt"
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
//
// TODO(merge-queue): a capability the backend lacks answers ErrVCSUnsupported until the
// capability redesign lands in vcs; git's then answer every method.
type Repo struct {
	cfg     Config
	name    string
	drv     types.VCSDriver
	fetcher types.RevisionFetcher
	stager  types.Stager
	merges  types.MergeStarter
	settle  types.ConflictResolver
}

var _ mergequeue.VCS = (*Repo)(nil)

// NewRepo detects cfg.Root's version control system the way magus does, and fails when
// that backend lacks a capability the queue needs or cfg.Remote names no configured
// remote.
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
	r := &Repo{cfg: cfg, name: res.Name, drv: res.VCS}
	for _, need := range []struct {
		name string
		ok   bool
	}{
		{"RevisionFetcher", assign(res.VCS, &r.fetcher)},
		{"Stager", assign(res.VCS, &r.stager)},
		{"MergeStarter", assign(res.VCS, &r.merges)},
		{"ConflictResolver", assign(res.VCS, &r.settle)},
	} {
		if !need.ok {
			return nil, &types.UnsupportedError{Backend: res.Name, Capability: need.name}
		}
	}
	// A cheap read, so a backend that declares the capabilities but cannot perform them,
	// a Root that is no checkout, or a remote nobody configured fails here, not mid-run.
	_, configured, err := r.fetcher.LookupRemote(ctx, cfg.Root, cfg.Remote)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cfg.Root, err)
	}
	if !configured {
		return nil, fmt.Errorf("%s: no remote named %q is configured", cfg.Root, cfg.Remote)
	}
	return r, nil
}

func assign[T any](v types.VCSDriver, dst *T) bool {
	c, ok := v.(T)
	*dst = c
	return ok
}

func (r *Repo) unsupported(capability string) error {
	return &types.UnsupportedError{Backend: r.name, Capability: capability}
}

// RemoteURL is the remote's URL, which names the repository to a provider.
func (r *Repo) RemoteURL(ctx context.Context) (string, error) {
	url, _, err := r.fetcher.LookupRemote(ctx, r.cfg.Root, r.cfg.Remote)
	return url, err
}

// RemoveCheckouts drops the registration of every checkout under dir, such as a scratch
// directory removed whole.
func (r *Repo) RemoveCheckouts(ctx context.Context, _ string) error {
	return r.stager.PruneStages(ctx, r.cfg.Root)
}

func (r *Repo) FetchRef(ctx context.Context, ref string) (string, error) {
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return "", r.unsupported("RevisionFetcher.FetchRef")
	}
	return r.fetcher.FetchBranch(ctx, r.cfg.Root, r.cfg.Remote, branch)
}

func (r *Repo) FetchCommit(ctx context.Context, id string) error {
	return r.fetcher.FetchRevision(ctx, r.cfg.Root, r.cfg.Remote, id, "")
}

func (r *Repo) IsAncestor(context.Context, string, string) (bool, error) {
	return false, r.unsupported("AncestryReporter")
}

func (r *Repo) RangeFiles(ctx context.Context, base, head string) ([]string, error) {
	return r.stager.ChangedSince(ctx, r.cfg.Root, base, head)
}

func (r *Repo) RangeCommits(context.Context, string, string, []string) ([]mergequeue.Commit, error) {
	return nil, r.unsupported("RangeReporter.RangeCommits")
}

func (r *Repo) FindCommit(ctx context.Context, rev string) (mergequeue.Commit, error) {
	c, err := r.drv.FindCommit(ctx, r.cfg.Root, rev)
	if err != nil {
		return mergequeue.Commit{}, err
	}
	return mergequeue.Commit{ID: c.ID, Parents: c.Parents, Author: mergequeue.Person{Name: c.Author.Name, Email: c.Author.Email}, Subject: c.Subject}, nil
}

func (r *Repo) TreeID(ctx context.Context, rev string) (string, error) {
	return r.stager.TreeOf(ctx, r.cfg.Root, rev)
}

func (r *Repo) DiffTrees(context.Context, string, string) ([]string, error) {
	return nil, r.unsupported("TreeReporter.DiffTrees")
}

func (r *Repo) MergeTrees(context.Context, mergequeue.TreeMerge) (mergequeue.TreeMergeResult, error) {
	return mergequeue.TreeMergeResult{}, r.unsupported("TreeMerger")
}

func (r *Repo) GeneratedPaths(context.Context, string, []string) (map[string]bool, error) {
	return nil, r.unsupported("GeneratedPathReporter")
}

func (r *Repo) CreateCheckout(context.Context, string, string) error {
	return r.unsupported("CheckoutProvisioner")
}

func (r *Repo) RemoveCheckout(ctx context.Context, dir string) error {
	return r.stager.RemoveStage(ctx, r.cfg.Root, dir)
}

func (r *Repo) StartMerge(ctx context.Context, dir, rev string) error {
	return r.merges.StartMerge(ctx, dir, rev)
}

func (r *Repo) AbortMerge(ctx context.Context, dir string) error {
	return r.merges.AbortMerge(ctx, dir)
}

func (r *Repo) Conflicts(ctx context.Context, dir string) ([]mergequeue.ConflictedPath, error) {
	cs, err := r.settle.Conflicts(ctx, dir)
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
	return r.settle.KeepIncoming(ctx, dir, paths)
}

func (r *Repo) MarkResolved(ctx context.Context, dir string, paths []string) error {
	return r.settle.MarkResolved(ctx, dir, paths)
}

func (r *Repo) RemoveConflicts(ctx context.Context, dir string, paths []string) error {
	return r.settle.RemoveConflicts(ctx, dir, paths)
}

func (r *Repo) DirtyFiles(ctx context.Context, dir string) ([]string, error) {
	return r.drv.DirtyFiles(ctx, dir, nil)
}

func (r *Repo) Commit(context.Context, string, mergequeue.CheckoutCommit) (string, error) {
	return "", r.unsupported("CommitWriter.Commit")
}

func (r *Repo) CommitTree(context.Context, mergequeue.TreeCommit) (string, error) {
	return "", r.unsupported("CommitWriter.CommitTree")
}

func (r *Repo) Push(context.Context, mergequeue.PushLease) error {
	return r.unsupported("Pusher")
}

func (r *Repo) Bundle(ctx context.Context, file string, b mergequeue.BundleRange) error {
	return r.stager.ExportStage(ctx, r.cfg.Root, file, b.Base, b.Head)
}

func (r *Repo) Unbundle(ctx context.Context, file string) error {
	return r.stager.ImportStage(ctx, r.cfg.Root, file)
}
