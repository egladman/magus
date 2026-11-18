package evidence

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// RefusesTheGate reports why this row must not be briefed, or nil to render it.
//
// The ONE verdict this command makes, and it is here rather than in job.Terms because
// the brief renders context and never a verdict. The gate runs once, in the orchestrator's
// tree, after every unit lands; seven workers ran the whole pipeline concurrently on one
// machine (2026-09-09) because every hand-typed brief ended with it. The guard already
// denies the gate to a worker that binds a narrower row, and this closes the half the
// guard cannot reach: a row whose validation IS the gate, which the guard reads as a lease
// that legitimately owns it.
//
// A COMPOSITE reaching the gate is refused too. Naming the pipeline indirectly buys the
// same seven concurrent runs, and it is the likelier mistake once this refuses the obvious
// spelling.
func RefusesTheGate(ctx context.Context, root string, row jobtypes.Job, env Env) error {
	// The holder of a row forked with --checkout gates in that checkout, as the guard lets it.
	if row.State.Live() && row.Worktree.Standing() {
		return nil
	}
	fix := fmt.Sprintf(" The gate runs ONCE, in the forking session's tree, after every job lands."+
		" Give this job the narrowest target covering its paths (`%s` decomposes what the gate chains) with `%s`, then ask for the terms again.",
		hint.DescribeTarget.With(types.TargetCI+" <project>"), hint.JobFork)

	if source, owns := guard.LeaseGateSource(row); owns {
		return fmt.Errorf("magus describe job: job %s is assigned %s, which names the `%s` gate.%s", row.ID, source, types.TargetCI, fix)
	}
	if chain := validationReachesGate(ctx, root, row.Validation, env); len(chain) > 0 {
		return fmt.Errorf("magus describe job: job %s is assigned %q, and that target reaches the `%s` gate through %s.%s",
			row.ID, row.Validation, types.TargetCI, strings.Join(chain, " -> "), fix)
	}
	return nil
}

// validationReachesGate walks the ctx.needs graph from each target a validation field
// names and returns the first chain that arrives at the gate, or nil.
//
// WITHIN one project's graph. A cross-project edge is a ref rather than a bare node name,
// so the walk stops at one instead of guessing which project it meant; that under-reports
// and never over-reports, which is the direction a refusal has to fail in. A workspace
// that cannot be inspected also reports nothing: this rule must not be the reason a brief
// is unavailable.
func validationReachesGate(ctx context.Context, root, validation string, env Env) []string {
	if strings.TrimSpace(validation) == "" {
		return nil
	}
	// The boundary loads the full workspace next; an inspection first would load it twice.
	ws, err := env.LoadMagus(ctx, root)
	if err != nil {
		return nil
	}
	graph, err := ws.TargetGraph(ctx)
	if err != nil {
		return nil
	}
	for _, word := range strings.Fields(validation) {
		t, err := types.ParseTarget(word)
		if err != nil {
			continue
		}
		for _, p := range graph.Projects {
			deps := map[string][]string{}
			for _, n := range p.Nodes {
				deps[n.Name] = n.Dependencies
			}
			if chain := chainToGate(t.Name, deps); len(chain) > 0 {
				return chain
			}
		}
	}
	return nil
}

// chainToGate is the path from start to the gate through a project's target dependencies,
// or nil when there is none. Depth-first with a visited set, so a cyclic graph terminates
// rather than being trusted to be acyclic: `magus describe graph` reports cycles instead
// of rejecting them, so one can reach here.
func chainToGate(start string, deps map[string][]string) []string {
	// A start nobody declares is not a chain of length one: every bare word in the
	// validation field reaches here, project paths and flags included.
	if _, ok := deps[start]; !ok {
		return nil
	}
	return gateWalk(start, deps, map[string]bool{})
}

func gateWalk(name string, deps map[string][]string, seen map[string]bool) []string {
	if seen[name] {
		return nil
	}
	seen[name] = true
	if name == types.TargetCI {
		return []string{name}
	}
	for _, next := range deps[name] {
		if chain := gateWalk(next, deps, seen); len(chain) > 0 {
			return append([]string{name}, chain...)
		}
	}
	return nil
}

func gatesOnCI(row jobtypes.Job) bool {
	return slices.ContainsFunc(row.EffectivePostconditions(), func(g jobtypes.Postcondition) bool {
		if g.Kind != jobtypes.PostconditionKindCheck {
			return false
		}
		c, err := job.PostconditionCheck(g)
		return err == nil && c.Target == types.TargetCI
	})
}

// latestGreenGate is the newest green ci gate on root's branch and the tier of the change
// since it. It is zero when there is none, a merge lies between it and HEAD, or the
// change cannot be assessed, the same conditions the redundancy check declines on.
func latestGreenGate(ctx context.Context, root string, env Env) job.GreenGate {
	m, err := env.LoadMagus(ctx, root)
	if err != nil {
		return job.GreenGate{}
	}
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil || res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return job.GreenGate{}
	}
	meta, err := res.VCS.Metadata(ctx, m.Root())
	if err != nil || meta.Ref == "" {
		return job.GreenGate{}
	}
	dir, err := sessions.Dir(m.Root())
	if err != nil {
		return job.GreenGate{}
	}
	fold, err := session.ReadAll(ctx, dir)
	if err != nil {
		return job.GreenGate{}
	}
	rec, ok := sessions.LatestGate(fold, meta.Ref, types.TargetCI)
	if !ok || rec.Outcome != session.OutcomePass || rec.Commit == "" {
		return job.GreenGate{}
	}
	if meta.ID != rec.Commit {
		history, err := res.VCS.History(ctx, m.Root(), types.HistoryQuery{Limit: env.GateMergeScanLimit})
		if err != nil || !internalci.MergeFreeRange(history, rec.Commit) {
			return job.GreenGate{}
		}
	}
	rep, err := m.AssessChange(ctx, types.TargetCI, magus.AssessOptions{Base: rec.Commit})
	if err != nil {
		return job.GreenGate{}
	}
	return job.GreenGate{Commit: rec.Commit, Projects: rec.Projects, Tier: rep.Tier}
}

// GradesSymbols reports whether grading id reads the symbol graph: a symbol gate of its
// own, or one inherited from an ancestor.
func GradesSymbols(rows []jobtypes.Job, id string) bool {
	i := slices.IndexFunc(rows, func(r jobtypes.Job) bool { return r.ID == id })
	if i < 0 {
		return false
	}
	for _, r := range append([]jobtypes.Job{rows[i]}, jobtypes.Ancestors(rows, id)...) {
		if slices.ContainsFunc(r.Postconditions, func(g jobtypes.Postcondition) bool { return g.Resolve().Kind == jobtypes.PostconditionKindSymbol }) {
			return true
		}
	}
	return false
}
