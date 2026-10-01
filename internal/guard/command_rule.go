package guard

import (
	"cmp"
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"

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
// built-in rules did not deny, and merges its answer the way the spawn rule's is merged:
// strengthen only.
//
// A built-in ask is still graded: the person approves the one thing the ask names, so a
// deny elsewhere on the line must replace it rather than ride through on that approval.
//
// A command magus itself served stays served: the rule's advice stands down on it as every
// advisory does, while its deny still holds, like the other workspace-wide denies.
func gradeWorkspaceCommand(ctx context.Context, deps Dependencies, verdict Verdict, in commandRuleInput, who hookAttribution, at location) (Verdict, workspaceRuleRecord) {
	if deps.CommandRule == nil && deps.ApprovedCommandRule == nil && deps.LoadFailure == nil {
		return verdict, workspaceRuleRecord{}
	}
	if verdict.Decision == "deny" {
		return verdict, workspaceRuleRecord{}
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	req := commandRequest(ctx, in, who, at, facts)
	if deps.CheckoutState != nil {
		if site, ok := locateCheckout(in.command, in.dialect, cmp.Or(at.dir, at.workspace)); ok {
			req.Checkout = deps.CheckoutState(ctx, site.dir)
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
	// Parsed again only when nothing loaded, so the pass path pays nothing. The unloaded
	// deny names the failures itself.
	denied := asked.unloaded && denyUnloaded(&asked, seamCommand, gatedVerb(in.command, in.dialect), at)
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
	if denied || asked.timedOut {
		return verdict, workspaceRuleRecord{decidedBy: decided, failures: asked.failures}
	}
	note := ruleFailureNote(hint.NewGate(at.cacheDir, who.callerKey()), seamCommand, asked.failures, asked.answered)
	return applyRuleFailureNote(verdict, note, advisoryCommandRuleFailed), workspaceRuleRecord{decidedBy: decided, failures: asked.failures}
}

// gatedVerb names, in backticks, the first call on the line that must wait while no side
// of the policy loads: a push, a pull request merge, or a magus verb writing state other
// checkouts or magus versions read. "" for anything else, the rebuild included, and for a
// line that does not parse, which the built-ins already judge.
func gatedVerb(command string, d Dialect) string {
	cmds, ok := ParseCommandsDialect(command, d)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		switch {
		case isPush(c):
			return "`" + vcsMutation(c) + "`"
		case filepath.Base(c.Name) == "gh" && len(c.Args) > 1 && c.Args[0] == "pr" && c.Args[1] == "merge":
			return "`gh pr merge`"
		case isMagusInvocation(c):
			if verb := magusStateWrite(c.Args); verb != "" {
				return "`magus " + verb + "`"
			}
		}
	}
	return ""
}

// magusStateWrite is the state-writing verb a magus argv runs, "" for any other. The set
// is hack/policy/stale.buzz's stateWrite: a dry run, a streamed install and `init spell`
// write nothing shared.
func magusStateWrite(args []string) string {
	if magusFlag(args, "dry-run") {
		return ""
	}
	ops := magusSubcommandWords(args)
	if len(ops) == 0 {
		return ""
	}
	sub := ""
	if len(ops) > 1 {
		sub = ops[1]
	}
	switch {
	case ops[0] == "init" && sub != "spell":
		return "init"
	case ops[0] == "server" && sub == "start":
		return "server start"
	case ops[0] == "config" && sub == "set":
		return "config set"
	case ops[0] == "agent" && sub == "install" && !magusFlag(args, "tar"):
		return "agent install"
	case ops[0] == "agent" && sub == "harness" && len(ops) > 2 && (ops[2] == "install" || ops[2] == "apply" || ops[2] == "remove"):
		return "agent harness " + ops[2]
	}
	return ""
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
		Commands:    commandInvocations(in.command, in.dialect, at.dir),
		Agent:       who.Agent,
		Parent:      spawnedAs(facts, who.Agent),
		Role:        role,
		Lease:       lease,
		Dir:         at.dir,
		Workspace:   at.workspace,
	}
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
// ParseCommandsDialect does, and marks each one a polling loop repeats (pollingLoop). Nil
// when the line does not parse.
//
// A loop inside a wrapper's script (`sh -c 'while ...'`) is not marked: the wrapper's
// payload is parsed on its own, outside the loop walk.
func commandInvocations(command string, d Dialect, dir string) []types.CommandInvocation {
	f, err := parseFile(command, d)
	if err != nil {
		return nil
	}
	var out []types.CommandInvocation
	moved := false
	var visit func(root syntax.Node, repeats bool)
	visit = func(root syntax.Node, repeats bool) {
		syntax.Walk(root, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.WhileClause, *syntax.ForClause:
				if _, polls := pollingLoop(n, d); polls && !repeats && n != root {
					visit(n, true)
					return false
				}
			case *syntax.CallExpr:
				words := literalWords(n.Args)
				for _, inv := range peelWrappers(words, d) {
					out = append(out, types.CommandInvocation{
						Program: inv.Name,
						Args:    inv.Args,
						Repeats: repeats,
						Path:    programPath(n.Args, words, inv, dir, moved),
						VCS:     vcsInvocation(inv),
					})
					if inv.Name == "cd" || inv.Name == "pushd" || inv.Name == "popd" {
						moved = true
					}
				}
			}
			return true
		})
	}
	visit(f, false)
	return out
}

// vcsInvocation is inv split at its subcommand by the parser every built-in VCS rule reads,
// nil when inv is not git, hg, sl or jj.
func vcsInvocation(inv hint.Invocation) *types.VCSInvocation {
	if _, other := vcsGlobals[inv.Name]; !other && inv.Name != "git" {
		return nil
	}
	sub, rest := vcsSubcommand(inv)
	return &types.VCSInvocation{Tool: inv.Name, Subcommand: sub, Args: slices.Clone(rest)}
}

// programPath is the file inv runs when the line names it by a path, resolved against dir,
// or "" when that file is not known before the line runs. The program word is found by
// position: inv's arguments are the tail of words, so the word before them names it. A
// program reparsed out of a `-c` payload has no such word here and stays unknown.
func programPath(parts []*syntax.Word, words []string, inv hint.Invocation, dir string, moved bool) string {
	i := len(words) - len(inv.Args) - 1
	if i < 0 || path.Base(words[i]) != inv.Name || !slices.Equal(words[i+1:], inv.Args) {
		return ""
	}
	word := words[i]
	if !strings.Contains(word, "/") || strings.HasPrefix(word, "~") || !fixedPath(parts[i].Parts) {
		return ""
	}
	if filepath.IsAbs(word) {
		return filepath.Clean(word)
	}
	if dir == "" || moved {
		return ""
	}
	return filepath.Join(dir, word)
}

// fixedPath reports whether a word's value is fixed before the line runs: no variable,
// substitution or glob inside it. Stricter than literalOnly, which lets a glob through.
func fixedPath(parts []syntax.WordPart) bool {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, "*?[") {
				return false
			}
		case *syntax.SglQuoted:
		case *syntax.DblQuoted:
			if !fixedPath(p.Parts) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
