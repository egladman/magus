package impact

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	difftypes "github.com/egladman/magus/types/diff"
)

// Workspace is the slice of a loaded workspace Collect reads, narrow so the root package
// satisfies it without this package importing the root.
type Workspace interface {
	Root() string
	CacheDir() string
	VCSOptions() types.VCSOptions
	Ownership(ctx context.Context, opts types.HistoryMetricsOptions) (types.Ownership, error)
}

// Sources are the lenses whose readers live above this package: each is handed in so a lens
// that cannot run degrades to its empty form here, in one place.
type Sources struct {
	// OwnershipCommits is the history window the ownership lens walks.
	OwnershipCommits int
	// HistoryPath is the recorded run history the cost lens reads; empty skips the lens.
	HistoryPath string
	// Advisors runs the local advisors against base.
	Advisors func(ctx context.Context, base string, rev difftypes.Diff) ([]review.AdviceSection, []string, error)
	// Anchors joins the declared notes stores against the changeset.
	Anchors func(ctx context.Context, rev difftypes.Diff) ([]review.AnchorHit, error)
	// LoadGraph reads the knowledge graph the symbol lens needs.
	LoadGraph func(ctx context.Context) (*knowledge.Graph, error)
	// UnusedSymbols is the symbols the change left defined and unreferenced, and false when
	// the symbol index does not cover the changed files.
	UnusedSymbols func(ctx context.Context, g *knowledge.Graph, driver types.RevisionReader, head string, changed []string) ([]string, bool)
	// RequiredIn answers whether a path sits in a declared review_required glob; it returns nil when none is declared.
	RequiredIn func(ctx context.Context) func(string) bool
	// BulkReasons is every distinct reason a bulk ack recorded against the changeset.
	BulkReasons func(cacheDir string, rev difftypes.Diff) []string
}

// Collect joins the lenses the report needs onto an already-annotated changeset.
//
// Every lens is best-effort and every failure degrades to that lens's empty form. A impact
// that refuses to print because the symbol index is cold or no server is running is a
// impact nobody runs, and this command reports context rather than passing judgement.
func Collect(ctx context.Context, ws Workspace, src Sources, rev difftypes.Diff, patch string, working bool) (Report, error) {
	p := Report{Reach: computeReach(rev)}

	// The same bounded git-log walk annotateDiff pays for the churn lenses, so the two
	// sections of one report cannot describe two different windows of history.
	if own, err := ws.Ownership(ctx, types.HistoryMetricsOptions{Commits: src.OwnershipCommits}); err == nil {
		p.Ownership = listOwners(own, rev.AffectedProjects)
	}

	if path := src.HistoryPath; path != "" {
		var h forecast.History
		if err := h.Load(ctx, path); err == nil {
			p.Cost = estimateCost(&h, rev.AffectedProjects)
		}
	}

	// The configured VCS base ref, never rev.Base: the working diff's base is a STATE
	// label ("working"), and the advisors compare git revisions (`origin/<base>`), so the
	// label would send them fetching a branch that does not exist. Their section
	// therefore describes committed state against the base branch, the most a rev-based
	// advisor can measure.
	base := ws.VCSOptions().BaseRef
	if base == "" {
		base = "main"
	}
	sections, failed, err := src.Advisors(ctx, base, rev)
	p.Advisors, p.AdvisorNotes = sections, failed
	if err != nil {
		p.AdvisorNotes = append(p.AdvisorNotes, fmt.Sprintf("advisors did not run: %v", err))
	}
	p.AdvisorBase = resolveAdvisorBase(ctx, ws.Root(), ws.VCSOptions(), base)

	anchors, aerr := src.Anchors(ctx, rev)
	if aerr != nil {
		// Not a lens that degrades: a misdeclared notes store is a fault in the workspace's own
		// config, and an anchors section that went missing would read as a clean tree.
		return Report{}, aerr
	}
	p.Anchors = anchors
	p.LeftBehind = collectLeftBehind(ctx, ws, src, rev, patch, working)
	// The trail and window trail.AttachTouches walks for the per-file touches, read here for the
	// questions the authors asked rather than the files they opened.
	p.Evidence, p.EvidenceGap = trail.Consulted(ws.Root(), ws.CacheDir(), review.ChangedPaths(rev), trail.DefaultReplayEvents)
	p.Review = collectReview(rev, src.RequiredIn(ctx), src.BulkReasons(ws.CacheDir(), rev))
	return p, nil
}
