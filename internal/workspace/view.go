package workspace

import (
	"fmt"

	"github.com/egladman/magus/types"
)

// view is ViewAs's answer, which the root magus package installs once, in its init.
var view func(types.WorkspaceRepository) types.WorkspaceRepository

// SetView installs ViewAs's answer. The root magus package calls it in its init and nothing
// else may: a second call panics, since two answers would make ViewAs depend on import
// order.
func SetView(f func(types.WorkspaceRepository) types.WorkspaceRepository) {
	if view != nil {
		panic("workspace: SetView called twice")
	}
	view = f
}

// ViewAs returns ws as the root magus package shows it to magus's own packages, as T, the
// interface a caller needs the internal methods through: an opened workspace with the
// methods whose signatures name internal types (KnowledgeGraph, JobLimits, SpellImports,
// ServiceSession, GetArtifact, ListArtifacts, Telemetry, LastRecordedRun), which the public
// *magus.Magus does not carry. A ws the root package did not open is returned unchanged.
// It panics when no root package is linked, which no binary that opened a workspace can
// be, and when the answer is not a T, which only a build that drifted from the root
// package's methods can be.
func ViewAs[T any](ws types.WorkspaceRepository) T {
	if view == nil {
		panic("workspace: ViewAs called with no root magus package linked")
	}
	shown := view(ws)
	v, ok := shown.(T)
	if !ok {
		panic(fmt.Sprintf("workspace: the root magus package's view is %T, which lacks the methods of %T", shown, (*T)(nil)))
	}
	return v
}
