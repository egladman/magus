package std

// Direct access to the loaded workspace's analytics, for the magus module's typed report.
//
// magus\insight used to answer by FORKING a nested magus and decoding its JSON.
// That was not laziness: root magus imports internal/interp, which imports this package,
// so std importing root is an import cycle and the API was genuinely out of reach. The
// fork bought the answer at the cost of a process, a second workspace load, and a
// serialization round trip - and it made a CLI subcommand load-bearing for a surface that
// has nothing to do with the CLI.
//
// A STRUCTURAL interface sidesteps the cycle. The workspace is ALREADY on the context
// (types.WorkspaceFromContext, what magus\projects and magus\affected read); it is carried as
// the narrow types.WorkspaceRepository, but the concrete value is the real *magus.Magus,
// which has the lenses. So the capability is named here in terms of types.* and recovered
// by assertion, with neither package naming the other. internal/handler/mcp reaches the
// same API the same way and for the same reason.

import (
	"context"

	"github.com/egladman/magus/types"
)

// Analyzer is the workspace's codebase-analytics surface: the VCS-history lenses plus the
// two that read the knowledge graph. Deliberately the narrow set the report needs rather
// than the whole of *Magus - a wide interface here would re-couple std to the shape of the
// package it cannot import.
type Analyzer interface {
	Hotspots(ctx context.Context, opts types.InsightOptions) (types.HotspotOutput, error)
	Affinity(ctx context.Context, opts types.InsightOptions) (types.AffinityOutput, error)
	Ownership(ctx context.Context, opts types.InsightOptions) (types.OwnershipOutput, error)
	Trend(ctx context.Context, opts types.InsightOptions) (types.TrendOutput, error)
	Volatility(ctx context.Context) (types.VolatilityReport, error)
	Unreferenced(ctx context.Context) (types.UnreferencedOutput, error)
}

// AnalyzerFromContext recovers the analytics surface from the workspace on ctx.
//
// Two distinct absences, and the caller wants to tell them apart: no workspace at all (a
// `magus buzz` script outside one), or a workspace whose implementation does not analyze -
// a test double, or a provider-supplied workspace that models projects without git
// history. Both report false here; the caller's message names the first, which is the one
// a reader can act on.
func AnalyzerFromContext(ctx context.Context) (Analyzer, bool) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, false
	}
	a, ok := ws.(Analyzer)
	return a, ok
}
