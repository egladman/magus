// Package client implements the merge queue's host interfaces with magus: [Repo] is the
// version control, through magus's vcs package and whichever backend magus detects, and
// [Workspace] is the build tool's facts, through magus's Go SDK.
package client

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/egladman/magus/libs/mergequeue"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// DefaultAttribute marks derived files when [Config.Attribute] is empty: the attribute
// GitHub already reads.
const DefaultAttribute = "linguist-generated"

// stageAuthor authors every stage commit. Stages are never pushed; an update commit is
// authored by the change's head author.
var stageAuthor = types.Person{Name: "merge queue", Email: "queue@mergequeue.invalid"}

// Config names the checkout a [Repo] works in.
type Config struct {
	Root   string // the checkout whose store backs every stage
	Remote string // remote name or URL changes and the base are fetched from
	// Attribute is the path attribute marking derived files, read at the plan's base
	// commit; empty means DefaultAttribute.
	Attribute string
	// Scratch is where stages are built. It must lie outside Root, where a stage would be
	// discovered as a second copy of the checkout. Empty makes a Repo that cannot stage.
	Scratch string
}

// Repo is the queue's version control over one checkout: a [mergequeue.StagingRepo]
// for planning and validation and a [mergequeue.MergingRepo] for applying. Which rights
// a step has is the interface it is handed, so the Applier's Repo is never asked to run
// a hook. Safe for concurrent use.
type Repo struct {
	cfg     Config
	fetcher types.RevisionFetcher
	stager  types.Stager
	seq     atomic.Int64
}

var (
	_ mergequeue.StagingRepo = (*Repo)(nil)
	_ mergequeue.MergingRepo = (*Repo)(nil)
	_ mergequeue.ExportFunc  = (*Repo)(nil).ExportStage
)

// NewRepo detects cfg.Root's version control system the way magus does and fails when
// that backend cannot stage.
func NewRepo(ctx context.Context, cfg Config) (*Repo, error) {
	if cfg.Root == "" || cfg.Remote == "" {
		return nil, errors.New("a Repo needs a Root and a Remote")
	}
	if cfg.Attribute == "" {
		cfg.Attribute = DefaultAttribute
	}
	res, err := vcs.Resolve(ctx, cfg.Root, "", types.VCSOptions{})
	if err != nil {
		return nil, err
	}
	if res.VCS == nil {
		return nil, fmt.Errorf("%s: version control is disabled", cfg.Root)
	}
	fetcher, ok := res.VCS.(types.RevisionFetcher)
	if !ok {
		return nil, &types.UnsupportedError{Backend: res.Name, Capability: "RevisionFetcher"}
	}
	stager, ok := res.VCS.(types.Stager)
	if !ok {
		return nil, &types.UnsupportedError{Backend: res.Name, Capability: "Stager"}
	}
	// A cheap read, so a backend that declares the capabilities but cannot perform them,
	// or a Root that is no checkout, fails here and not mid-run.
	if _, _, err := fetcher.LookupRemote(ctx, cfg.Root, cfg.Remote); err != nil {
		return nil, fmt.Errorf("%s: %w", cfg.Root, err)
	}
	return &Repo{cfg: cfg, fetcher: fetcher, stager: stager}, nil
}

// RemoteURL names the Repo's remote for a provider: a configured remote's URL, else
// Remote as given, since a URL or a path is its own name.
func (r *Repo) RemoteURL(ctx context.Context) (string, error) {
	url, ok, err := r.fetcher.LookupRemote(ctx, r.cfg.Root, r.cfg.Remote)
	if err != nil || !ok {
		return r.cfg.Remote, err
	}
	return url, nil
}

func (r *Repo) FetchTip(ctx context.Context, branch string) (string, error) {
	if err := mergequeue.CheckBranch(branch); err != nil {
		return "", err
	}
	return r.fetcher.FetchBranch(ctx, r.cfg.Root, r.cfg.Remote, branch)
}

func (r *Repo) FetchHead(ctx context.Context, c mergequeue.Change) error {
	if err := c.Check(); err != nil {
		return err
	}
	return r.fetcher.FetchRevision(ctx, r.cfg.Root, r.cfg.Remote, c.Head, c.Ref)
}

func (r *Repo) ReviewTarget(ctx context.Context, tip, head string) (string, error) {
	return r.stager.ReviewTarget(ctx, r.cfg.Root, r.cfg.Attribute, tip, head)
}

func (r *Repo) Changed(ctx context.Context, onto, head string) ([]string, error) {
	return r.stager.ChangedSince(ctx, r.cfg.Root, onto, head)
}

func (r *Repo) CheckMerge(ctx context.Context, baseCommit string, c mergequeue.Change) error {
	return asConflict(r.stager.CheckMerge(ctx, r.cfg.Root, r.cfg.Attribute, baseCommit, c.Head), c)
}

func (r *Repo) Stage(ctx context.Context, baseCommit, onto string, c mergequeue.Change, regenerate mergequeue.RegenerateFunc) (mergequeue.Stage, error) {
	if err := c.Check(); err != nil {
		return mergequeue.Stage{}, err
	}
	if r.cfg.Scratch == "" {
		return mergequeue.Stage{}, errors.New("this Repo was built without a Scratch directory, so it cannot stage")
	}
	spec := types.StageSpec{
		Dir:     filepath.Join(r.cfg.Scratch, fmt.Sprintf("stage-%d-%s", r.seq.Add(1), c.ID)),
		Derived: r.cfg.Attribute,
		Base:    baseCommit,
		Onto:    onto,
		Rev:     c.Head,
		Message: "merge queue: stage #" + c.ID,
		Author:  stageAuthor,
	}
	if regenerate != nil {
		spec.Regenerate = func(ctx context.Context, dir string, paths []string) error {
			return regenerate(ctx, dir, onto, c, paths)
		}
	}
	commit, err := r.stager.BuildStage(ctx, r.cfg.Root, spec)
	var nd *types.NotDerivedError
	if errors.As(err, &nd) {
		return mergequeue.Stage{}, &mergequeue.RefusedError{Reason: "regeneration wrote files that are not derived: " + strings.Join(nd.Paths, ", ")}
	}
	if err != nil {
		return mergequeue.Stage{}, asConflict(err, c)
	}
	return mergequeue.Stage{Commit: commit, Dir: spec.Dir}, nil
}

func (r *Repo) Discard(ctx context.Context, s mergequeue.Stage) error {
	return r.stager.RemoveStage(ctx, r.cfg.Root, s.Dir)
}

// Prune forgets the stages whose directories are gone, such as a Scratch removed whole.
func (r *Repo) Prune(ctx context.Context) error {
	return r.stager.PruneStages(ctx, r.cfg.Root)
}

// SquashMessage is GitHub's default squash body for head's own commits: one
// "* subject" paragraph each, oldest first, merges left out.
func (r *Repo) SquashMessage(ctx context.Context, baseCommit, head string) (string, error) {
	subjects, err := r.stager.CommitSubjects(ctx, r.cfg.Root, baseCommit, head)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(subjects))
	for i, s := range subjects {
		parts[i] = "* " + s
	}
	return strings.Join(parts, "\n\n"), nil
}

// ExportStage writes stage and every commit beneath it that baseCommit lacks to file, so
// applying imports the validated commits instead of rebuilding anything. It is a
// [mergequeue.ExportFunc].
func (r *Repo) ExportStage(ctx context.Context, file, baseCommit, stage string) error {
	return r.stager.ExportStage(ctx, r.cfg.Root, file, baseCommit, stage)
}

func (r *Repo) ImportStage(ctx context.Context, file string) error {
	return r.stager.ImportStage(ctx, r.cfg.Root, file)
}

func (r *Repo) Predict(ctx context.Context, baseCommit, tip, onto, stage string) (string, error) {
	tree, err := r.stager.PredictMerge(ctx, r.cfg.Root, baseCommit, tip, onto, stage)
	return tree, asConflict(err, mergequeue.Change{})
}

func (r *Repo) UpdateBranch(ctx context.Context, baseCommit, tip string, c mergequeue.Change, tree string) (string, error) {
	commit, err := r.stager.UpdateBranch(ctx, r.cfg.Root, r.cfg.Remote, types.BranchUpdate{
		Derived: r.cfg.Attribute,
		Base:    baseCommit,
		Tip:     tip,
		Head:    c.Head,
		Branch:  c.Branch,
		Tree:    tree,
		Message: "merge " + c.Base + " and regenerate derived files",
	})
	var nd *types.NotDerivedError
	switch {
	case errors.As(err, &nd):
		return "", &mergequeue.RefusedError{Reason: "the validated tree differs from its merge outside derived files (" +
			strings.Join(nd.Paths, ", ") + "), which the queue never merges."}
	case errors.Is(err, types.ErrNoBranch):
		return "", &mergequeue.RefusedError{Reason: "its derived files need regenerating on top of `" + c.Base +
			"`, and the queue cannot push to its branch. Merge `" + c.Base + "` in, regenerate, push, and queue it again."}
	case errors.Is(err, types.ErrBranchMoved):
		return "", &mergequeue.WaitError{Reason: "its branch moved or was deleted since validation"}
	}
	return commit, err
}

func (r *Repo) TreeOf(ctx context.Context, rev string) (string, error) {
	return r.stager.TreeOf(ctx, r.cfg.Root, rev)
}

// asConflict restates a VCS merge conflict as the queue's, naming c.
func asConflict(err error, c mergequeue.Change) error {
	var mc *types.MergeConflictError
	if errors.As(err, &mc) {
		return &mergequeue.ConflictError{Conflict: mergequeue.Conflict{Change: c, Paths: mc.Paths, With: mc.With}}
	}
	return err
}
