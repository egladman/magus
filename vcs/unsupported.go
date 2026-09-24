package vcs

import (
	"context"
	"time"

	"github.com/egladman/magus/types"
)

// backendName names the backend a declines answers for, so a zero-value driver still
// refuses in its own name.
type backendName interface{ name() string }

type (
	hgName      struct{}
	saplingName struct{}
	jjName      struct{}
)

func (hgName) name() string      { return "hg" }
func (saplingName) name() string { return "sl" }
func (jjName) name() string      { return "jj" }

// declines answers every capability with a *types.VCSUnsupportedError naming backend N.
// hg, sl and jj embed it and override what they implement; git implements everything and
// does not embed it. TestParityCapabilityMatrix pins what each still declines, so an
// embed that hides a missing implementation fails there.
//
// Why each backend declines what it does:
//
//   - jj, MergeDriverInstaller: its merge-tools config selects one tool for the whole
//     repository, with no per-path key (checked against `jj config list
//     --include-defaults`), so registering magus would route every conflicted file
//     through it. ConflictResolver settles a jj workspace with no driver at all.
//   - jj, the hook installers: jj has no native hooks, and `jj git push` passes
//     --no-verify, so not even git's hooks under a colocated workspace would fire.
//   - jj, PushStatusReporter: hg and Sapling answer from a phase recorded on the commit;
//     jj records no such fact.
//   - jj, IgnoredFileReporter: no query over its ignore RULES; see jjVCS.IgnoredPaths.
//   - jj, BranchChangeReporter: its working-copy commit is usually anonymous, so every
//     bookmark, the reader's own included, would report as another line of work.
//   - jj, Bisector: jj has no bisect command.
//   - hg and sl, RegenHookInstaller and BranchChangeReporter: not built yet, though their
//     hooks and revsets could answer.
//   - hg, sl and jj, the capabilities that combine revisions without a checkout: git's
//     alone so far, though hg and Sapling have bundles, shares and commit, and jj has
//     workspaces.
type declines[N backendName] struct{}

func decline[N backendName](c types.VCSCapability) error {
	var n N
	return &types.VCSUnsupportedError{VCS: n.name(), Capability: c}
}

func (declines[N]) Bisect(context.Context, string, types.BisectOptions) (types.Culprit, error) {
	return types.Culprit{}, decline[N](types.CapBisector)
}

func (declines[N]) InstallMergeDriver(context.Context, string, []string) error {
	return decline[N](types.CapMergeDriverInstaller)
}

func (declines[N]) CheckMergeDriver(context.Context, string) (bool, error) {
	return false, decline[N](types.CapMergeDriverInstaller)
}

func (declines[N]) EnsureMergeDriver(context.Context, string, []string) (bool, error) {
	return false, decline[N](types.CapMergeDriverInstaller)
}

func (declines[N]) MergeDriverCommand(context.Context, string) (string, error) {
	return "", decline[N](types.CapMergeDriverInstaller)
}

func (declines[N]) InstallRefreshHook(context.Context, string, string) ([]string, error) {
	return nil, decline[N](types.CapRefreshHookInstaller)
}

func (declines[N]) InstallDriftHook(context.Context, string, string) ([]string, error) {
	return nil, decline[N](types.CapDriftHookInstaller)
}

func (declines[N]) InstallRegenHook(context.Context, string, string) ([]string, error) {
	return nil, decline[N](types.CapRegenHookInstaller)
}

func (declines[N]) RemoteURL(context.Context, string, string) (string, error) {
	return "", decline[N](types.CapRemoteReporter)
}

func (declines[N]) ConfiguredRemote(string) (string, error) {
	return "", decline[N](types.CapRemoteConfigReporter)
}

func (declines[N]) DefaultRef(context.Context, string) (string, error) {
	return "", decline[N](types.CapDefaultRefReporter)
}

func (declines[N]) CheckoutState(context.Context, string) (types.CheckoutState, error) {
	return types.CheckoutState{}, decline[N](types.CapCheckoutStateReporter)
}

func (declines[N]) CommitPushed(context.Context, string, string) (bool, bool, error) {
	return false, false, decline[N](types.CapPushStatusReporter)
}

func (declines[N]) RevTime(context.Context, string, string) (time.Time, bool, error) {
	return time.Time{}, false, decline[N](types.CapRevTimeReporter)
}

func (declines[N]) TrackedFiles(context.Context, string, []string) ([]string, error) {
	return nil, decline[N](types.CapTrackedFileReporter)
}

func (declines[N]) IgnoredFiles(context.Context, string, []string) ([]string, error) {
	return nil, decline[N](types.CapIgnoredFileReporter)
}

func (declines[N]) ChangesByCommit(context.Context, string, int, string) ([]types.CommitChange, error) {
	return nil, decline[N](types.CapChurnReporter)
}

func (declines[N]) BranchChanges(context.Context, string, string, int) ([]types.BranchChange, error) {
	return nil, decline[N](types.CapBranchChangeReporter)
}

func (declines[N]) RangeDiff(context.Context, string, string, string, []string) (string, error) {
	return "", decline[N](types.CapRangeReporter)
}

func (declines[N]) RangeFiles(context.Context, string, string, string, []string) ([]string, error) {
	return nil, decline[N](types.CapRangeReporter)
}

func (declines[N]) RangeCommits(context.Context, string, string, string, []string) ([]types.Commit, error) {
	return nil, decline[N](types.CapRangeReporter)
}

func (declines[N]) Regions(context.Context, string, string, []types.FileChange) ([]types.RegionChange, error) {
	return nil, decline[N](types.CapRegionReporter)
}

func (declines[N]) IsAncestor(context.Context, string, string, string) (bool, error) {
	return false, decline[N](types.CapAncestryReporter)
}

func (declines[N]) Conflicts(context.Context, string) ([]types.Conflict, error) {
	return nil, decline[N](types.CapConflictResolver)
}

func (declines[N]) KeepIncoming(context.Context, string, []string) error {
	return decline[N](types.CapConflictResolver)
}

func (declines[N]) MarkResolved(context.Context, string, []string) error {
	return decline[N](types.CapConflictResolver)
}

func (declines[N]) RemoveConflicts(context.Context, string, []string) error {
	return decline[N](types.CapConflictResolver)
}

func (declines[N]) IgnoredPaths(context.Context, string, []string) (map[string]bool, error) {
	return nil, decline[N](types.CapConflictResolver)
}

func (declines[N]) ReadFileAt(context.Context, string, string, string) (string, error) {
	return "", decline[N](types.CapRevisionFileReader)
}

func (declines[N]) ExportRevision(context.Context, string, string, string) error {
	return decline[N](types.CapRevisionExporter)
}

func (declines[N]) StartMerge(context.Context, string, string, types.Person) error {
	return decline[N](types.CapMergeStarter)
}

func (declines[N]) AbortMerge(context.Context, string) error {
	return decline[N](types.CapMergeStarter)
}

func (declines[N]) Commit(context.Context, string, types.CheckoutCommit) (string, error) {
	return "", decline[N](types.CapCommitWriter)
}

func (declines[N]) CommitTree(context.Context, string, types.TreeCommit) (string, error) {
	return "", decline[N](types.CapCommitWriter)
}

func (declines[N]) TreeID(context.Context, string, string) (string, error) {
	return "", decline[N](types.CapTreeReporter)
}

func (declines[N]) DiffTrees(context.Context, string, string, string) ([]string, error) {
	return nil, decline[N](types.CapTreeReporter)
}

func (declines[N]) MergeTrees(context.Context, string, types.TreeMerge) (types.TreeMergeResult, error) {
	return types.TreeMergeResult{}, decline[N](types.CapTreeMerger)
}

func (declines[N]) GeneratedPaths(context.Context, string, string, []string) (map[string]bool, error) {
	return nil, decline[N](types.CapGeneratedPathReporter)
}

func (declines[N]) CreateCheckout(context.Context, string, string, string) error {
	return decline[N](types.CapCheckoutProvisioner)
}

func (declines[N]) RemoveCheckout(context.Context, string, string) error {
	return decline[N](types.CapCheckoutProvisioner)
}

func (declines[N]) Checkouts(context.Context, string) ([]string, error) {
	return nil, decline[N](types.CapCheckoutProvisioner)
}

func (declines[N]) OtherCheckouts(string) ([]string, error) {
	return nil, decline[N](types.CapCheckoutLister)
}

func (declines[N]) FetchRef(context.Context, string, string, string) (string, error) {
	return "", decline[N](types.CapRevisionFetcher)
}

func (declines[N]) FetchCommit(context.Context, string, string, string) error {
	return decline[N](types.CapRevisionFetcher)
}

func (declines[N]) Push(context.Context, string, types.PushLease) error {
	return decline[N](types.CapPusher)
}

func (declines[N]) Bundle(context.Context, string, string, types.BundleRange) error {
	return decline[N](types.CapBundler)
}

func (declines[N]) Unbundle(context.Context, string, string) error {
	return decline[N](types.CapBundler)
}
