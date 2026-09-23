package vcs

import (
	"context"

	"github.com/egladman/magus/types"
)

// The capabilities hg, sl and jj decline, in one place so the matrix reads at a glance;
// TestParityCapabilityMatrix pins it. Each stub refuses before looking at its arguments,
// so asking costs nothing.

func unsupported(v types.VCSDriver, capability string) error {
	return &types.UnsupportedError{VCS: v.Name(), Capability: capability}
}

// jj: MergeDriverInstaller takes the workspace's declared output GLOBS, and jj has nowhere
// to put them: its merge-tools config selects one tool for the whole repository, with no
// per-path key (checked against `jj config list --include-defaults`), so registering magus
// would route every conflicted file through it. jj's path is the bulk one instead:
// ConflictResolver settles a jj workspace with no driver at all.

func (v jjVCS) InstallMergeDriver(context.Context, string, []string) error {
	return unsupported(v, "MergeDriverInstaller")
}

func (v jjVCS) CheckMergeDriver(context.Context, string) (bool, error) {
	return false, unsupported(v, "MergeDriverInstaller")
}

func (v jjVCS) EnsureMergeDriver(context.Context, string, []string) (bool, error) {
	return false, unsupported(v, "MergeDriverInstaller")
}

// jj has no native hooks, and `jj git push` passes --no-verify deliberately, so not even
// git's hooks under a colocated workspace would fire. With no merge driver, nothing is
// owed that a regeneration hook would settle.

func (v jjVCS) InstallRefreshHook(context.Context, string, string) ([]string, error) {
	return nil, unsupported(v, "RefreshHookInstaller")
}

func (v jjVCS) InstallDriftHook(context.Context, string, string) ([]string, error) {
	return nil, unsupported(v, "DriftHookInstaller")
}

func (v jjVCS) InstallRegenHook(context.Context, string, string) ([]string, error) {
	return nil, unsupported(v, "RegenHookInstaller")
}

// CommitPushed: hg and Sapling answer from a phase recorded on the commit; jj records no
// such fact, and asking whether a bookmark's `@<remote>` ref reaches the commit answers
// only for a git-backed repository.
func (v jjVCS) CommitPushed(context.Context, string, string) (bool, bool, error) {
	return false, false, unsupported(v, "PushStatusReporter")
}

// IgnoredFiles: jj exposes no query over its ignore RULES; see jjVCS.IgnoredPaths.
func (v jjVCS) IgnoredFiles(context.Context, string, []string) ([]string, error) {
	return nil, unsupported(v, "IgnoredFileReporter")
}

// BranchChanges excludes "the branch this checkout is on", and jj's working-copy commit is
// usually anonymous, so every bookmark would report as another line of work, the reader's
// own included.
func (v jjVCS) BranchChanges(context.Context, string, string, int) ([]types.BranchChange, error) {
	return nil, unsupported(v, "BranchChangeReporter")
}

// Not yet implemented for hg and sl, though both could be: their hooks and their revsets
// cover the ground.

func (v hgVCS) InstallRegenHook(context.Context, string, string) ([]string, error) {
	return nil, unsupported(v, "RegenHookInstaller")
}

func (v saplingVCS) InstallRegenHook(context.Context, string, string) ([]string, error) {
	return nil, unsupported(v, "RegenHookInstaller")
}

func (v hgVCS) BranchChanges(context.Context, string, string, int) ([]types.BranchChange, error) {
	return nil, unsupported(v, "BranchChangeReporter")
}

func (v saplingVCS) BranchChanges(context.Context, string, string, int) ([]types.BranchChange, error) {
	return nil, unsupported(v, "BranchChangeReporter")
}

// The capabilities a merge queue runs on are git's alone so far: hg and Sapling have
// bundles, shares and commit, and jj has workspaces, but none is implemented.

func (v hgVCS) TreeID(context.Context, string, string) (string, error) {
	return "", unsupported(v, "TreeReporter")
}

func (v hgVCS) DiffTrees(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v, "TreeReporter")
}

func (v hgVCS) MergeTrees(context.Context, string, types.TreeMerge) (types.MergeResult, error) {
	return types.MergeResult{}, unsupported(v, "TreeMerger")
}

func (v hgVCS) CommitTree(context.Context, string, types.TreeCommit) (string, error) {
	return "", unsupported(v, "TreeMerger")
}

func (v hgVCS) GeneratedPaths(context.Context, string, string, []string) (map[string]bool, error) {
	return nil, unsupported(v, "GeneratedPathReporter")
}

func (v hgVCS) CreateCheckout(context.Context, string, string, string) error {
	return unsupported(v, "CheckoutCreator")
}

func (v hgVCS) RemoveCheckout(context.Context, string, string) error {
	return unsupported(v, "CheckoutCreator")
}

func (v hgVCS) Checkouts(context.Context, string) ([]string, error) {
	return nil, unsupported(v, "CheckoutCreator")
}

func (v hgVCS) Commit(context.Context, string, types.CommitOptions) (string, error) {
	return "", unsupported(v, "Committer")
}

func (v hgVCS) FetchBranch(context.Context, string, string, string) (string, error) {
	return "", unsupported(v, "RevisionFetcher")
}

func (v hgVCS) FetchRef(context.Context, string, string, string) (string, error) {
	return "", unsupported(v, "RevisionFetcher")
}

func (v hgVCS) FetchCommit(context.Context, string, string, string) error {
	return unsupported(v, "RevisionFetcher")
}

func (v hgVCS) Push(context.Context, string, string, string, string, string) error {
	return unsupported(v, "Pusher")
}

func (v hgVCS) Bundle(context.Context, string, string, string, string) error {
	return unsupported(v, "Bundler")
}

func (v hgVCS) Unbundle(context.Context, string, string) error {
	return unsupported(v, "Bundler")
}

func (v saplingVCS) TreeID(context.Context, string, string) (string, error) {
	return "", unsupported(v, "TreeReporter")
}

func (v saplingVCS) DiffTrees(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v, "TreeReporter")
}

func (v saplingVCS) MergeTrees(context.Context, string, types.TreeMerge) (types.MergeResult, error) {
	return types.MergeResult{}, unsupported(v, "TreeMerger")
}

func (v saplingVCS) CommitTree(context.Context, string, types.TreeCommit) (string, error) {
	return "", unsupported(v, "TreeMerger")
}

func (v saplingVCS) GeneratedPaths(context.Context, string, string, []string) (map[string]bool, error) {
	return nil, unsupported(v, "GeneratedPathReporter")
}

func (v saplingVCS) CreateCheckout(context.Context, string, string, string) error {
	return unsupported(v, "CheckoutCreator")
}

func (v saplingVCS) RemoveCheckout(context.Context, string, string) error {
	return unsupported(v, "CheckoutCreator")
}

func (v saplingVCS) Checkouts(context.Context, string) ([]string, error) {
	return nil, unsupported(v, "CheckoutCreator")
}

func (v saplingVCS) Commit(context.Context, string, types.CommitOptions) (string, error) {
	return "", unsupported(v, "Committer")
}

func (v saplingVCS) FetchBranch(context.Context, string, string, string) (string, error) {
	return "", unsupported(v, "RevisionFetcher")
}

func (v saplingVCS) FetchRef(context.Context, string, string, string) (string, error) {
	return "", unsupported(v, "RevisionFetcher")
}

func (v saplingVCS) FetchCommit(context.Context, string, string, string) error {
	return unsupported(v, "RevisionFetcher")
}

func (v saplingVCS) Push(context.Context, string, string, string, string, string) error {
	return unsupported(v, "Pusher")
}

func (v saplingVCS) Bundle(context.Context, string, string, string, string) error {
	return unsupported(v, "Bundler")
}

func (v saplingVCS) Unbundle(context.Context, string, string) error {
	return unsupported(v, "Bundler")
}

func (v jjVCS) TreeID(context.Context, string, string) (string, error) {
	return "", unsupported(v, "TreeReporter")
}

func (v jjVCS) DiffTrees(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v, "TreeReporter")
}

func (v jjVCS) MergeTrees(context.Context, string, types.TreeMerge) (types.MergeResult, error) {
	return types.MergeResult{}, unsupported(v, "TreeMerger")
}

func (v jjVCS) CommitTree(context.Context, string, types.TreeCommit) (string, error) {
	return "", unsupported(v, "TreeMerger")
}

func (v jjVCS) GeneratedPaths(context.Context, string, string, []string) (map[string]bool, error) {
	return nil, unsupported(v, "GeneratedPathReporter")
}

func (v jjVCS) CreateCheckout(context.Context, string, string, string) error {
	return unsupported(v, "CheckoutCreator")
}

func (v jjVCS) RemoveCheckout(context.Context, string, string) error {
	return unsupported(v, "CheckoutCreator")
}

func (v jjVCS) Checkouts(context.Context, string) ([]string, error) {
	return nil, unsupported(v, "CheckoutCreator")
}

func (v jjVCS) Commit(context.Context, string, types.CommitOptions) (string, error) {
	return "", unsupported(v, "Committer")
}

func (v jjVCS) FetchBranch(context.Context, string, string, string) (string, error) {
	return "", unsupported(v, "RevisionFetcher")
}

func (v jjVCS) FetchRef(context.Context, string, string, string) (string, error) {
	return "", unsupported(v, "RevisionFetcher")
}

func (v jjVCS) FetchCommit(context.Context, string, string, string) error {
	return unsupported(v, "RevisionFetcher")
}

func (v jjVCS) Push(context.Context, string, string, string, string, string) error {
	return unsupported(v, "Pusher")
}

func (v jjVCS) Bundle(context.Context, string, string, string, string) error {
	return unsupported(v, "Bundler")
}

func (v jjVCS) Unbundle(context.Context, string, string) error {
	return unsupported(v, "Bundler")
}

// jj has no bisect command of its own.
func (v jjVCS) Bisect(context.Context, string, types.BisectOptions) (types.Culprit, error) {
	return types.Culprit{}, unsupported(v, "Bisect")
}
