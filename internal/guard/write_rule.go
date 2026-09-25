package guard

import (
	"context"
	"path/filepath"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// workspaceWriteRule is the Verdict.Rule a magus\guard.write answer carries.
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
// built-in rules let through, strengthen only, the way gradeWorkspaceCommand asks about a
// command.
func gradeWorkspaceWrite(ctx context.Context, deps Dependencies, verdict Verdict, path string, fields writeFields, lease string, who hookAttribution, at location) (Verdict, workspaceRuleRecord) {
	if deps.WriteRule == nil && deps.ApprovedWriteRule == nil && deps.LoadFailure == nil {
		return verdict, workspaceRuleRecord{}
	}
	if verdict.Decision != "pass" && verdict.Decision != "advise" {
		return verdict, workspaceRuleRecord{}
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	role, row := actingRole(ctx, at, lease)
	req := types.WriteRequest{
		Host:      who.Host,
		Session:   who.Session,
		Parent:    spawnedAs(facts, who.Agent),
		Role:      role,
		Lease:     row,
		Path:      path,
		Workspace: writeWorkspace(path, at),
		Content:   fields.Content,
		OldText:   fields.OldText,
		NewText:   fields.NewText,
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
	asked := askWorkspaceRules(ctx, seamWrite, deps.LoadFailure, resolve, bind(deps.WriteRule))
	decided := ""
	if verdict.Decision != "pass" {
		decided = decidedByBuiltin
	}
	verdict, decided = applyWorkspaceAnswer(verdict, decided, asked, workspaceWriteRule)
	if asked.by == "" {
		decided = ""
	}
	note := ruleFailureNote(hint.NewGate(at.cacheDir, who.callerKey()), seamWrite, asked.failures, asked.answered)
	return applyRuleFailureNote(verdict, note, advisoryWriteRuleFailed), workspaceRuleRecord{decidedBy: decided, failures: asked.failures}
}

// writeWorkspace is the root of the workspace holding path, which is the checkout the write
// lands in whichever directory the hook runs from. A relative path is the hook location's.
func writeWorkspace(path string, at location) string {
	if !filepath.IsAbs(path) {
		return at.workspace
	}
	root, err := magus.FindRoot(filepath.Dir(path))
	if err != nil {
		// A file in a directory not created yet: the hook's own checkout is the best fact.
		return at.workspace
	}
	return root
}
