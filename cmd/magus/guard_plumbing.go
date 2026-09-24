package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// The workspace facts and the output plumbing `magus shell` needs, kept out of shell.go
// so that file reads as the command and this one as what it stands on.

// guardDependencies hands the guard the five workspace facts its rules cannot resolve for
// themselves: the memoized inspect, this process's loaded config, the index-staleness
// advice, and the spell catalog, all of which live in the CLI rather than in the rules.
func guardDependencies() guard.Dependencies {
	rules, loadErr := loadGuardRules(context.Background())
	shellRules, shellDialect := workspaceShellRules(rules)
	deps := guard.Dependencies{
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if root == "" {
				return inspectWorkspace(ctx, "")
			}
			return magus.Inspect(ctx, root,
				magus.WithLoadedConfig(globalCfg), magus.WithVersion(version))
		},
		CacheDir: func(root string) (string, error) {
			if root != "" {
				return magus.ResolveCacheDir(root)
			}
			found, err := magus.FindRoot("")
			if err != nil {
				return "", err
			}
			return magus.ResolveCacheDir(found, magus.WithLoadedConfig(globalCfg))
		},
		NotesShared:      globalCfg.Knowledge.Notes.Shared,
		ShellRules:       shellRules,
		ShellDialect:     shellDialect,
		GraphStaleAdvice: staleGraphAdvice,
		Spells:           project.DefaultSpellRegistry().All,
		SymbolDefined:    symbolDefinedForGuard,
		HeadCommit:       headCommitForGuard,
		CheckoutBase:     checkoutBaseForGuard,
		GitState:         gitStateForGuard,
	}
	if rules != nil {
		deps.SpawnRule = rules.SpawnRule()
		deps.ApprovedSpawnRule = rules.ApprovedSpawnRule
		deps.CommandRule = rules.CommandRule()
		deps.ApprovedCommandRule = rules.ApprovedCommandRule
		deps.WriteRule = rules.WriteRule()
		deps.ApprovedWriteRule = rules.ApprovedWriteRule
		deps.Policy = func() guard.PolicyState { return guardPolicyState(rules) }
		return deps
	}
	root, err := magus.FindRoot("")
	if err != nil {
		return deps
	}
	// The approved sources load when the working tree does not, and they are what an agent
	// breaking the working tree must not be able to turn off.
	deps.LoadFailure = loadErr
	deps.ApprovedSpawnRule = func(ctx context.Context) (workspace.SpawnRule, error) {
		return magus.ApprovedSpawnRuleAt(ctx, root)
	}
	deps.ApprovedCommandRule = func(ctx context.Context) (workspace.CommandRule, error) {
		return magus.ApprovedCommandRuleAt(ctx, root)
	}
	deps.ApprovedWriteRule = func(ctx context.Context) (workspace.WriteRule, error) {
		return magus.ApprovedWriteRuleAt(ctx, root)
	}
	return deps
}

// loadGuardRules loads the guard rules of the workspace this process runs in from its
// root magusfile alone, nil with the reason when that file does not load, and nil with no
// error outside any workspace. Unloadable is not fatal for the reason
// loadWorkspaceShellRules gives: a magusfile typo must not take down every hook.
//
// Not the full workspace load: that opens every project and costs an order of magnitude
// more, on every agent tool call, for facts no guard rule reads. The rules that do need
// the workspace open it through Dependencies.Inspect, and only when they apply.
func loadGuardRules(ctx context.Context) (*magus.GuardRules, error) {
	root, err := magus.FindRoot("")
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // outside a workspace there are no rules and nothing failed
	}
	return magus.LoadGuardRules(ctx, root, types.VCSOptions{
		Enabled: globalCfg.VCS.Enabled, Name: globalCfg.VCS.Name, BaseRef: globalCfg.VCS.BaseRef,
	})
}

// guardPolicyState describes the workspace's guard rules for the trail's lineage. The
// approved ids are left to a callback, since reading them runs a process per file and the
// lineage asks only when the policy moved.
func guardPolicyState(rules *magus.GuardRules) guard.PolicyState {
	policy := rules.Policy()
	return guard.PolicyState{
		Digest:      policy.Digest,
		ShellRules:  policy.ShellRules,
		SpawnRule:   policy.SpawnRule,
		CommandRule: policy.CommandRule,
		WriteRule:   policy.WriteRule,
		Sources: func(ctx context.Context) []guard.PolicySource {
			paths := make([]string, len(policy.Sources))
			for i, s := range policy.Sources {
				paths[i] = s.Path
			}
			approved := rules.ApprovedContentIDs(ctx, paths)
			if approved == nil {
				return nil
			}
			out := make([]guard.PolicySource, len(policy.Sources))
			for i, s := range policy.Sources {
				out[i] = guard.PolicySource{Path: s.Path, Worktree: s.ContentID, Approved: approved[s.Path]}
			}
			return out
		},
	}
}

// headCommitForGuard answers this checkout's revision for the push gate, or "" when it
// cannot be read.
//
// Resolved through the VCS layer rather than by shelling out to git, because magus drives
// four backends and the hook runs in whichever the workspace uses. Every failure answers
// "": no VCS, an unreadable one, or a repository with no commit yet all mean the push gate
// has nothing to match a recorded run against, and it stands down rather than refusing on
// an absence it cannot account for.
func headCommitForGuard(ctx context.Context) string {
	root, err := magus.FindRoot("")
	if err != nil {
		return ""
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return ""
	}
	// Metadata rather than FindCommit: it is the cheap call the brief already uses for
	// exactly this field, and it answers the abbreviation the run log's version string
	// carries.
	meta, err := res.VCS.Metadata(ctx, root)
	if err != nil {
		return ""
	}
	return meta.Short
}

// checkoutBaseForGuard answers the checkout at root as `magus vcs checkpoint -o name`
// prints it, or "" when it cannot be read. Resolved by root rather than through
// inspectWorkspace, which is memoized for one root and the hook may name another.
func checkoutBaseForGuard(ctx context.Context, root string) string {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return ""
	}
	cp, err := vcs.Checkpoint(ctx, root, res, false)
	if err != nil {
		return ""
	}
	return checkpointToken(cp)
}

// gitStateForGuard reads the git checkout holding dir for a rule judging a `git push`: the
// command being judged is git's own, so its facts come from git rather than through the
// VCS layer. Two processes, spent only on a push. Nil when dir is in no git checkout.
func gitStateForGuard(ctx context.Context, dir string) *types.GitState {
	if dir == "" {
		return nil
	}
	// symbolic-ref -q exits 1, printing nothing, exactly when HEAD names no branch.
	_, headErr := exec.CommandContext(ctx, "git", "-C", dir, "symbolic-ref", "-q", "HEAD").Output()
	var exit *exec.ExitError
	if headErr != nil && (!errors.As(headErr, &exit) || exit.ExitCode() != 1) {
		return nil
	}
	refs, err := exec.CommandContext(ctx, "git", "-C", dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes").Output()
	if err != nil {
		return nil
	}
	return &types.GitState{Detached: headErr != nil, RemoteBranches: strings.Fields(string(refs))}
}

// symbolDefinedForGuard answers the one question that lets the guard deny a symbol
// search: does refs return the same sites this grep is reaching for.
//
// Definitive only when no project's index is older than its sources. A stale index
// makes refs answer "unknown, not absent", and a deny resting on that would take grep
// away on the strength of a lookup that admits it may be wrong.
//
// Reached only after precedentIdent has already matched, which the transcript mining
// tuned to fire rarely, so the graph load stays off the path of an ordinary tool call.
// The empty rootOverride is load-bearing: inspectWorkspace is memoized per process and
// panics when a second call names a different root, and every other hook dependency
// resolves through the same empty spelling.
func symbolDefinedForGuard(ident string) (defined, definitive bool) {
	ctx := context.Background()
	g, err := loadKnowledgeGraphForRefs(ctx, "", false, ident)
	if err != nil {
		return false, false
	}
	_, ok := g.Refs(ident)
	return ok, staleGraphAdvice(ctx) == ""
}

// loadWorkspaceShellRules returns additive rules the root magusfile declared via
// magus\guard.shell, and the last non-empty dialect among them for outer parse.
// Missing or unloadable is empty so a magusfile typo cannot take down every
// shell hook (built-ins still apply).
func loadWorkspaceShellRules(ctx context.Context) ([]guard.WorkspaceShellRule, guard.Dialect) {
	rules, _ := loadGuardRules(ctx)
	return workspaceShellRules(rules)
}

// workspaceShellRules converts the loaded rules' magus\guard.shell entries for the guard,
// empty when nothing loaded.
func workspaceShellRules(rules *magus.GuardRules) ([]guard.WorkspaceShellRule, guard.Dialect) {
	if rules == nil {
		return nil, ""
	}
	src := rules.ShellRules()
	if len(src) == 0 {
		return nil, ""
	}
	out := make([]guard.WorkspaceShellRule, len(src))
	var dialect guard.Dialect
	for i, r := range src {
		out[i] = guard.WorkspaceShellRule{
			Name:     r.Name,
			Decision: r.Decision,
			Program:  r.Program,
			Args:     r.Args,
			Reason:   r.Reason,
			Dialect:  r.Dialect,
		}
		if r.Dialect != "" {
			dialect = guard.Dialect(r.Dialect)
		}
	}
	return out, dialect
}

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and exits 0 blocks NOTHING: the host sees success
// and runs the command anyway, so the guard looks enforced and is not. 2 rather
// than 1 is what the dominant host reads as "block and show the reason to the
// model". The collision with the usage code is harmless: a guard that could not
// parse its input has not judged the command either.
const guardDenyExitCode = 2

// enforceVerdict turns a deny or an ask into a blocking exit. Applies to every format: an
// `-o json` caller with a zero status would be told the same lie in a different
// shape. An ask blocks too because the exit status cannot carry the person's approval: a
// glue that reads only the status must stop, and one that renders the host's prompt reads
// the decision off stdout.
//
// The reason reaches stderr only when stdout does not already carry it as prose,
// i.e. every format but text. The guard templates read the verdict off stdout and
// one discards stderr outright, so an unconditional copy printed a kilobyte-plus
// reason twice to an audience with a context budget.
func enforceVerdict(opts OutputOptions, verdict guard.Verdict) error {
	return enforceVerdictTo(os.Stderr, opts, verdict)
}

func enforceVerdictTo(errOut io.Writer, opts OutputOptions, verdict guard.Verdict) error {
	if verdict.Decision != "deny" && verdict.Decision != "ask" {
		return nil
	}
	if opts.Format != FormatText {
		fmt.Fprintln(errOut, verdict.Reason)
	}
	return errSilent{exitCode: guardDenyExitCode}
}

// writeGuardVerdict renders a verdict through the standard output arm.
func writeGuardVerdict(out io.Writer, opts OutputOptions, verdict guard.Verdict) error {
	switch opts.Format {
	case FormatText:
		switch verdict.Decision {
		case "deny":
			fmt.Fprintln(out, decisionLabel("deny", verdict.Rule)+" "+verdict.Reason)
		case "ask":
			fmt.Fprintln(out, decisionLabel("ask", verdict.Rule)+" "+verdict.Reason)
		case "advise":
			fmt.Fprintln(out, decisionLabel("advise", verdict.Rule)+" "+verdict.Context)
		default:
			fmt.Fprintln(out, "pass")
		}
		return nil
	case FormatName:
		fmt.Fprintln(out, verdict.Decision)
		return nil
	}
	return writeFormatted(out, opts, verdict)
}

// decisionLabel opens a text verdict with the decision and, where the rule can identify
// itself, its name: `deny [stage-all]`.
//
// The name was reachable only through `-o json` before this, which meant a person could
// read a refusal and still have nothing to look up, grep a trail for, or cite when
// reporting it as a false positive. Every other verdict magus reaches carries a code a
// reader can take somewhere; this was the exception.
//
// Bracketed rather than appended so it survives a line-wrap next to the decision it
// qualifies, and omitted entirely when the rule is anonymous: an empty `[]` would promise
// an identifier that does not exist.
func decisionLabel(decision, rule string) string {
	if rule == "" {
		return decision + ":"
	}
	return decision + " [" + rule + "]:"
}

// guardInputLimit bounds the payload one hook call may carry.
//
// An MCP params object is caller-controlled and can be megabytes, the raw-event wiring
// forwards the WHOLE event rather than one extracted command string, and the template
// holds a second copy of it under the host's hook timeout. Overflow is a read FAILURE
// rather than a truncation, for the reason readGuardInput gives: a truncated payload is
// exactly the "not the command the guard was shown" case.
const guardInputLimit = 1 << 20

// readGuardInput reports the payload, and the read failure separately from an empty one.
// "No input" is a host that sent nothing and "the read broke" is a payload magus was
// supposed to receive, and only the second one means the command about to run is not the
// command the guard was shown.
func readGuardInput(in io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(in, guardInputLimit+1))
	if err != nil {
		return "", err
	}
	if len(b) > guardInputLimit {
		return "", fmt.Errorf("the payload is larger than the %d-byte limit, so it was not read whole", guardInputLimit)
	}
	return strings.TrimSpace(string(b)), nil
}
