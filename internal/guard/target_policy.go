package guard

import (
	"context"
	"path"
	"sync"

	"github.com/egladman/magus/types"
)

// phaseTargetPolicies is the trail's name for the read of the knowledge store's record of
// the workspace's target policies.
const phaseTargetPolicies = "target_policy_record"

// The notes declaredPolicies gives when the record could not answer, so a verdict says how
// it read the declarations instead of falling back unseen.
const (
	policyOpenedNote = "The knowledge store holds no current record of this workspace's target policies, " +
		"so the guard opened the workspace to read them; `magus graph build` records them again."
	policyUnreadNote = "The knowledge store holds no current record of this workspace's target policies, " +
		"and the workspace did not open, so no target policy was read; `magus graph build` records them."
)

// declaredPolicies is what the workspace at root declares: the knowledge store's record
// while it is provably current, else the index of the workspace opened through workspace.
// note is "" when the record answered and says which fallback ran otherwise; ok is false
// when neither could answer.
func declaredPolicies(ctx context.Context, deps Dependencies, root string, workspace func() (types.WorkspaceRepository, error)) (ix types.TargetPolicyIndex, note string, ok bool) {
	if ix, ok := deps.targetPolicies(ctx, root); ok {
		return ix, "", true
	}
	if workspace == nil {
		return types.TargetPolicyIndex{}, policyUnreadNote, false
	}
	ws, err := workspace()
	if err != nil {
		return types.TargetPolicyIndex{}, policyUnreadNote, false
	}
	return types.IndexTargetPolicies(ws), policyOpenedNote, true
}

// declaredIndex is declaredPolicies' answer, kept on a location so a judgment reads it once.
type declaredIndex struct {
	ix   types.TargetPolicyIndex
	note string
}

// policiesAt reads what the workspace at root declares the first time a rule asks.
func policiesAt(ctx context.Context, deps Dependencies, root string) func() declaredIndex {
	return sync.OnceValue(func() declaredIndex {
		if root == "" {
			return declaredIndex{}
		}
		ix, note, _ := declaredPolicies(ctx, deps, root, lazyWorkspace(ctx, deps, root))
		return declaredIndex{ix: ix, note: note}
	})
}

// declared is what at's workspace declares, empty for a location no workspace holds.
func (at location) declared() declaredIndex {
	if at.policies == nil {
		return declaredIndex{}
	}
	return at.policies()
}

// runProjects are the projects r runs in: the ones it names, else the one its directory
// selects, else the root.
func runProjects(r targetRun) []string {
	if len(r.projects) > 0 {
		out := make([]string, len(r.projects))
		for i, p := range r.projects {
			out[i] = path.Clean(p)
		}
		return out
	}
	if r.cwdProject != "" {
		return []string{r.cwdProject}
	}
	return []string{"."}
}

// runPolicy is the policy r's target declares in the first project r runs in that declares
// one, and false when none does.
func runPolicy(ix types.TargetPolicyIndex, r targetRun) (types.TargetPolicy, bool) {
	name := targetName(r.target)
	for _, project := range runProjects(r) {
		if project == "/" {
			// Every project: the first that declares a policy for the target answers.
			for _, t := range ix.Targets {
				if t.Target == name {
					return t, true
				}
			}
			continue
		}
		if pol, ok := ix.Lookup(types.TargetRef{Project: project, Target: name}); ok {
			return pol, true
		}
	}
	return types.TargetPolicy{}, false
}

// runRelinks reports a run whose target declares the binary it replaces.
func runRelinks(ix types.TargetPolicyIndex, r targetRun) bool {
	pol, ok := runPolicy(ix, r)
	return ok && r.verb == "run" && pol.Relinks != ""
}

// runIntegrates reports a run whose target an integrator runs.
func runIntegrates(ix types.TargetPolicyIndex, r targetRun) bool {
	pol, ok := runPolicy(ix, r)
	return ok && pol.Integrates != ""
}
