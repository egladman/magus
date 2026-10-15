package workspace

import (
	"fmt"

	"github.com/egladman/magus/types"
)

// view is View's answer, which the root magus package installs once, in its init.
var view func(types.WorkspaceRepository) types.WorkspaceRepository

// SetView installs View's answer. The root magus package calls it in its init and nothing
// else may: a second call panics, since two answers would make View depend on import
// order.
func SetView(f func(types.WorkspaceRepository) types.WorkspaceRepository) {
	if view != nil {
		panic("workspace: SetView called twice")
	}
	view = f
}

// View returns ws as the root magus package shows it to magus's own packages: an opened
// workspace with the methods whose signatures name internal types (KnowledgeGraph,
// JobLimits, SpellImports, ServiceSession, GetArtifact, ListArtifacts, Telemetry,
// LastRecordedRun), which the public *magus.Magus does not carry. A ws the root package
// did not open is returned unchanged. It panics when no root package is linked, which no
// binary that opened a workspace can be.
func View(ws types.WorkspaceRepository) types.WorkspaceRepository {
	if view == nil {
		panic("workspace: View called with no root magus package linked")
	}
	return view(ws)
}

// ViewAs is View's answer as T, the interface a caller needs the internal methods through.
// It panics when the answer is not a T, which only a build that drifted from the root
// package's methods can be.
func ViewAs[T any](ws types.WorkspaceRepository) T {
	v, ok := View(ws).(T)
	if !ok {
		panic(fmt.Sprintf("workspace: the root magus package's view is %T, which lacks the methods of %T", View(ws), (*T)(nil)))
	}
	return v
}
