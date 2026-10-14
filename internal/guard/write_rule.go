package guard

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// workspaceWriteRule is the GuardJudgment.Rule an unnamed magus\guard.write answer carries.
const workspaceWriteRule = workspaceShellPrefix + "write"

// advisoryWriteRuleFailed names the notice that a workspace write rule judged nothing.
const advisoryWriteRuleFailed hint.MarkerKind = "workspace-write-failed"

// writeFields is the text a host said a file write carries.
type writeFields struct {
	Content string
	OldText string
	NewText string
	// ReplaceAll replaces every occurrence of OldText rather than requiring exactly one.
	ReplaceAll bool
	// Edits is a sequence of replacements applied in order, for a host that sends several
	// in one write; OldText and NewText are then empty.
	Edits []textEdit
}

// textEdit is one replacement a host said a write makes.
type textEdit struct {
	OldText    string
	NewText    string
	ReplaceAll bool
}

// gradeWorkspaceWrite asks the workspace's magus\guard.write rule about a write the
// built-in rules did not deny, strengthen only, the way gradeWorkspaceCommand asks about a
// command.
func gradeWorkspaceWrite(ctx context.Context, deps Dependencies, verdict types.GuardJudgment, path string, fields writeFields, lease string, who hookAttribution, at location) (types.GuardJudgment, workspaceRuleRecord) {
	verdict = gradeStall(deps, gradeMainWrite(deps, verdict, path, lease, who, at), lease, at)
	if deps.WriteRule == nil && deps.ApprovedWriteRule == nil && deps.LoadFailure == nil {
		return verdict, workspaceRuleRecord{}
	}
	if verdict.Decision == decisionDeny {
		return verdict, workspaceRuleRecord{}
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	role, row := actingRole(ctx, at, lease)
	thread, from, window := callerCost(at, who, lease, facts)
	req := types.WriteRequest{
		Host:          who.Host,
		Session:       who.Session,
		Agent:         who.Agent,
		Parent:        spawnedAs(facts, who.Agent),
		Role:          role,
		Lease:         row,
		Thread:        thread,
		ThreadFrom:    from,
		Orchestration: callerOrchestration(at, who, thread, from, lease),
		Window:        window,
		Path:          path,
		Workspace:     writeWorkspace(path, at),
		Content:       fields.Content,
		OldText:       fields.OldText,
		NewText:       fields.NewText,
	}
	bind := func(rule workspace.WriteRule) ruleCall {
		if rule == nil {
			return nil
		}
		return func(ctx context.Context) (types.GuardVerdict, error) { return rule(ctx, req, facts) }
	}
	var resolve func(context.Context) (ruleCall, error)
	if deps.ApprovedWriteRule != nil {
		resolve = func(ctx context.Context) (ruleCall, error) {
			rule, err := deps.ApprovedWriteRule(ctx)
			return bind(rule), err
		}
	}
	asked := askWorkspaceRules(ctx, seamWrite, deps, resolve, bind(deps.WriteRule))
	// The guard loads a policy source itself, so refusing its write when the judgment ran out
	// of time would leave a slow or looping committed rule with no way to be fixed. Only while
	// the approved policy stands: its side answered, or no policy change is pending. An agent
	// can slow the approved resolve (a flood of untracked files does it), and with a change
	// of its own pending that must not open the policy to an unjudged edit.
	spared := asked.lapse != nil && isGuardSource(deps, at, path) &&
		(asked.approvedAnswered() || asked.approvedRuleless || !policyPending(ctx, deps, at))
	denied := !spared && asked.denyShort(func() judgedCall {
		return judgedCall{seam: seamWrite, changes: true, what: "this file write", lease: lease}
	}, at)
	decided := ""
	if verdict.Decision != decisionPass {
		decided = decidedByBuiltin
	}
	verdict, decided = applyWorkspaceAnswer(verdict, decided, asked, workspaceWriteRule)
	if asked.by == "" {
		decided = ""
	}
	if denied {
		return verdict, workspaceRuleRecord{decidedBy: decided, failures: asked.failures}
	}
	note := ruleFailureNote(ruleFailureReport{
		gate: hint.NewGate(at.cacheDir, who.callerKey()), seam: seamWrite, failures: asked.failures, answered: asked.answered, toldInFull: asked.lapse != nil,
		judging: func() string { return judgingBinaryNote(deps, at, asked.loadFailure, lease) },
		lead:    loadFailureLead(deps.LoadFailure, at.workspace),
	})
	if spared {
		note = lapseSparedNote(*asked.lapse) + "\n\n" + note
	}
	return applyRuleFailureNote(verdict, note, advisoryWriteRuleFailed), workspaceRuleRecord{decidedBy: decided, failures: asked.failures}
}

// pendingCheckBound bounds the one status policyPending runs. A lapsed load has spent the
// hook's budget already, so it runs past it, inside the margin the host still waits.
const pendingCheckBound = time.Second

// policyPending reports whether a policy source may differ from its approved content, as
// version control reports it changed or untracked: a file the policy load read, the root
// magusfile sources, or the config that shapes a load. The files read are this call's
// load's, else the last recorded one's; a file the load reads can change only through one it
// already read, so that set bounds the answer. With no record of it, any changed Buzz
// source or untracked directory in the workspace counts.
//
// True whenever it cannot tell. No version control is no approval authority: the working
// tree is the whole policy, and nothing in it is pending.
func policyPending(ctx context.Context, deps Dependencies, at location) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pendingCheckBound)
	defer cancel()
	res, err := vcs.Resolve(ctx, at.workspace, "", deps.VCS)
	switch {
	case err != nil:
		return true
	case res.VCS == nil:
		return false
	}
	var files []string
	if deps.Policy != nil {
		files = deps.Policy().Files
	}
	if files == nil && at.cacheDir != "" {
		if m, ok := readPolicyMarker(filepath.Join(at.cacheDir, policyMarkerFile)); ok {
			files = m.Files
		}
	}
	var scope []string
	if files != nil {
		scope = append([]string{"magusfile.buzz", "magusfiles"}, policyShapingFiles...)
		for _, f := range files {
			if rel, err := filepath.Rel(at.workspace, resolveFrom(at.workspace, f)); err == nil && filepath.IsLocal(rel) {
				scope = append(scope, filepath.ToSlash(rel))
			}
		}
	}
	dirty, err := res.VCS.DirtyFiles(ctx, at.workspace, scope)
	if err != nil {
		// A workspace in no repository has no approved state, as approvalAuthority reads it.
		_, rootErr := res.VCS.Root(ctx, at.workspace)
		return rootErr == nil || ctx.Err() != nil
	}
	return slices.ContainsFunc(dirty, func(p string) bool {
		return scope != nil || strings.HasSuffix(p, "/") || strings.HasSuffix(p, ".buzz") || slices.Contains(policyShapingFiles, filepath.Base(p))
	})
}

// policyShapingFiles are the files besides Buzz sources whose edit changes what a policy load
// registers: the workspace config and the remote spell lock.
var policyShapingFiles = []string{config.Filename, remotespell.LockFile}

// lapseSparedNote is the advice for a write to a file the guard loads when its judgment ran
// out of time while the approved policy stood, which every other write is denied for.
func lapseSparedNote(l ruleLapse) string {
	return "magus ran out of time judging this write (" + l.what() + ") and lets it through, because the guard loads this file itself: " +
		"refusing it would leave a slow or looping rule with no way to be fixed. Check the rule answers quickly before relying on it."
}

// writeWorkspace is the root of the workspace holding path, which is the checkout the write
// lands in whichever directory the hook runs from. A relative path is the hook location's.
func writeWorkspace(path string, at location) string {
	if !filepath.IsAbs(path) {
		return at.workspace
	}
	root, err := workspace.FindRoot(filepath.Dir(path))
	if err != nil {
		// A file in a directory not created yet: the hook's own checkout is the best fact.
		return at.workspace
	}
	return root
}
