package guard

import (
	"cmp"
	"context"
	"path/filepath"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// workspaceCommandRule is the Verdict.Rule a magus\guard.command answer carries, in the
// namespace workspace shell rules already use so a reader can tell it from a built-in.
const workspaceCommandRule = workspaceShellPrefix + "command"

// advisoryCommandRuleFailed names the notice that a workspace command rule judged nothing.
// Each distinct set of failures is told in full once per session, since the rule stays
// broken on every command until someone edits it.
const advisoryCommandRuleFailed hint.MarkerKind = "workspace-command-failed"

// workspaceRuleRecord is what the trail keeps about how a workspace command or write rule
// judged.
type workspaceRuleRecord struct {
	// decidedBy is the side whose answer stands, "" when the rule changed nothing.
	decidedBy string
	failures  []trail.RuleFailure
}

// commandRuleInput is the one shell command the workspace rule is asked about, with who
// sent it.
type commandRuleInput struct {
	command     string
	description string
	dialect     Dialect
	// preauth is the served `next` that already cleared this command, "" for any other.
	preauth string
	lease   string
}

// gradeWorkspaceCommand asks the workspace's magus\guard.command rule about a command the
// built-in rules let through, and merges its answer the way the spawn rule's is merged:
// strengthen only.
//
// A command magus itself served stays served: the rule's advice stands down on it as every
// advisory does, while its deny still holds, like the other workspace-wide denies.
func gradeWorkspaceCommand(ctx context.Context, deps Dependencies, verdict Verdict, in commandRuleInput, who hookAttribution, at location) (Verdict, workspaceRuleRecord) {
	if deps.CommandRule == nil && deps.ApprovedCommandRule == nil && deps.LoadFailure == nil {
		return verdict, workspaceRuleRecord{}
	}
	if verdict.Decision != "pass" && verdict.Decision != "advise" {
		return verdict, workspaceRuleRecord{}
	}
	facts := hint.NewGate(at.cacheDir, who.sessionKey())
	req := commandRequest(ctx, in, who, at, facts)
	if deps.CheckoutState != nil {
		if dir, ok := gitPushDir(req.Commands, cmp.Or(at.dir, at.workspace)); ok {
			req.Checkout = deps.CheckoutState(ctx, dir)
		}
	}
	bind := func(rule workspace.CommandRule) ruleCall {
		if rule == nil {
			return nil
		}
		return func(ctx context.Context) (types.GuardVerdict, error) { return rule(ctx, req, facts) }
	}
	var resolve func(context.Context) (ruleCall, error)
	if deps.ApprovedCommandRule != nil {
		resolve = func(ctx context.Context) (ruleCall, error) {
			rule, err := deps.ApprovedCommandRule(ctx)
			return bind(rule), err
		}
	}
	asked := askWorkspaceRules(ctx, seamCommand, deps.LoadFailure, resolve, bind(deps.CommandRule))
	if in.preauth != "" && asked.answer.Decision != types.GuardDeny {
		return verdict, workspaceRuleRecord{failures: asked.failures}
	}
	decided := ""
	if verdict.Decision != "pass" {
		decided = decidedByBuiltin
	}
	verdict, decided = applyWorkspaceAnswer(verdict, decided, asked, workspaceCommandRule)
	if asked.by == "" {
		decided = ""
	}
	note := ruleFailureNote(hint.NewGate(at.cacheDir, who.callerKey()), seamCommand, asked.failures, asked.answered)
	return applyRuleFailureNote(verdict, note, advisoryCommandRuleFailed), workspaceRuleRecord{decidedBy: decided, failures: asked.failures}
}

// commandRequest normalizes one shell command for the workspace rule. Every field is read
// from the envelope, parsed from the line, or taken from what magus recorded; none is
// inferred from the host.
func commandRequest(ctx context.Context, in commandRuleInput, who hookAttribution, at location, facts hint.Gate) types.CommandRequest {
	role, lease := actingRole(ctx, at, in.lease)
	return types.CommandRequest{
		Host:        who.Host,
		Session:     who.Session,
		Command:     in.command,
		Description: in.description,
		Commands:    commandInvocations(in.command, in.dialect),
		Parent:      spawnedAs(facts, who.Agent),
		Role:        role,
		Lease:       lease,
	}
}

// gitPushDir is the directory the first `git push` on a line runs in, the one command whose
// rule needs the checkout's state: dir, moved by each `-C` git is given, as git
// applies them. False when the line pushes nothing.
func gitPushDir(cmds []types.CommandInvocation, dir string) (string, bool) {
	for _, c := range cmds {
		if c.Program != "git" {
			continue
		}
		if ops := operands(c.Args, "C"); len(ops) == 0 || ops[0] != "push" {
			continue
		}
		for i := 0; i+1 < len(c.Args) && c.Args[i] != "push"; i++ {
			if c.Args[i] == "-C" {
				dir = filepath.Join(dir, c.Args[i+1])
				if filepath.IsAbs(c.Args[i+1]) {
					dir = c.Args[i+1]
				}
			}
		}
		return dir, true
	}
	return "", false
}

// actingRole is where a caller acting under lease stands, and the lease's job row. A bound
// id the job store does not carry comes back with only its ID set, so the rule can tell a
// dangling binding from no binding.
func actingRole(ctx context.Context, at location, lease string) (types.AgentRole, *types.Job) {
	if lease == "" {
		return types.AgentRoleRoot, nil
	}
	row := &types.Job{ID: lease}
	if rows, err := leaseRows(ctx, at); err == nil {
		for _, r := range rows {
			if r.ID == lease {
				// The rows are the pinned snapshot's own; the rule gets a copy it may keep.
				clone := r.Clone()
				row = &clone
				break
			}
		}
	}
	return types.AgentRoleWorker, row
}

// commandInvocations resolves a shell line into the programs it runs, the way
// ParseCommandsDialect does, and marks each one a while or until loop repeats. Nil when
// the line does not parse.
//
// A loop inside a wrapper's script (`sh -c 'while ...'`) is not marked: the wrapper's
// payload is parsed on its own, outside the loop walk.
func commandInvocations(command string, d Dialect) []types.CommandInvocation {
	f, err := parseFile(command, d)
	if err != nil {
		return nil
	}
	var out []types.CommandInvocation
	var visit func(root syntax.Node, repeats bool)
	visit = func(root syntax.Node, repeats bool) {
		syntax.Walk(root, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.WhileClause:
				// WhileClause is also the until loop.
				if !repeats && syntax.Node(n) != root {
					visit(n, true)
					return false
				}
			case *syntax.CallExpr:
				for _, inv := range peelWrappers(literalWords(n.Args), d) {
					out = append(out, types.CommandInvocation{Program: inv.Name, Args: inv.Args, Repeats: repeats})
				}
			}
			return true
		})
	}
	visit(f, false)
	return out
}
