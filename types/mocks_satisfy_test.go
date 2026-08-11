// Nothing else forces these mocks to actually satisfy the interfaces they were
// generated from. mockery's testify template emits a plain struct with methods and
// no `var _ Iface = (*Mock)(nil)` line, so a mock that has gone stale - an interface
// gained a method, nobody re-ran `magus run generate` - still COMPILES. It would fail
// only at a use site, and since these are published for downstream consumers rather
// than used by our own tests, the first build to break could well be someone else's.
//
// The assertions live here rather than beside the mocks because a gen/ dir is
// generated-only; a hand-written file inside one would be erased by the next run.

package types_test

import (
	"github.com/egladman/magus/types"
	mocks "github.com/egladman/magus/types/gen/mocks"
)

var (
	_ types.Observer             = (*mocks.MockObserver)(nil)
	_ types.VCSDriver            = (*mocks.MockVCSDriver)(nil)
	_ types.MergeDriverInstaller = (*mocks.MockMergeDriverInstaller)(nil)
	_ types.RefreshHookInstaller = (*mocks.MockRefreshHookInstaller)(nil)
	_ types.RemoteReporter       = (*mocks.MockRemoteReporter)(nil)
	_ types.DefaultRefReporter   = (*mocks.MockDefaultRefReporter)(nil)
	_ types.TrackedFileReporter  = (*mocks.MockTrackedFileReporter)(nil)
	_ types.IgnoredFileReporter  = (*mocks.MockIgnoredFileReporter)(nil)
	_ types.ChurnReporter        = (*mocks.MockChurnReporter)(nil)
	_ types.ConflictResolver     = (*mocks.MockConflictResolver)(nil)
	_ types.RevisionExporter     = (*mocks.MockRevisionExporter)(nil)
	_ types.MergeStarter         = (*mocks.MockMergeStarter)(nil)
	_ types.DepGraphRepository   = (*mocks.MockDepGraphRepository)(nil)
	_ types.WorkspaceReader      = (*mocks.MockWorkspaceReader)(nil)
	_ types.TargetExpander       = (*mocks.MockTargetExpander)(nil)
	_ types.AffectedComputer     = (*mocks.MockAffectedComputer)(nil)
	_ types.Inspector            = (*mocks.MockInspector)(nil)
	_ types.WorkspaceRepository  = (*mocks.MockWorkspaceRepository)(nil)
)
