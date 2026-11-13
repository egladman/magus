package magus

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"mvdan.cc/sh/v3/syntax"
)

// JudgeCommand judges command, a shell line about to run in dir, as the guard hook judges
// an agent's command: the compiled rules, the workspace's magus\guard rules beside their
// approved copies, and what session did before. The lease ctx carries (see
// types.WithJobLease) is the one the call acts under. The judgment is recorded on the
// workspace's trail as a hook's is.
//
// A deny is a judgment, returned with its Reason, Rule and Next and a nil error; a rule
// that would ask the person denies, since no host is here to put the question.
//
// A working tree whose rules do not load is judged by its approved
// sources, as the hook judges it. The error is a
// *types.EngineNotLinkedError: no Buzz engine loads either side.
func (m *Magus) JudgeCommand(ctx context.Context, session, dir, command string) (types.GuardJudgment, error) {
	return m.judge(ctx, guard.Request{Input: command, Dir: dir, Session: session})
}

type authorityKey struct{}

// ContextWithAuthority has a judgment under ctx with no job row read the
// approved rules at revision (ADR 0045). CI passes its target branch's,
// so a pull request cannot approve its own loosening. A leased call reads
// its row's authority instead.
func ContextWithAuthority(ctx context.Context, revision string) context.Context {
	return context.WithValue(ctx, authorityKey{}, revision)
}

// JudgeWrite is JudgeCommand for a file an edit is about to write: path, absolute or
// relative to dir.
func (m *Magus) JudgeWrite(ctx context.Context, session, dir, path string) (types.GuardJudgment, error) {
	return m.judge(ctx, guard.Request{Input: path, IsPath: true, Dir: dir, Session: session})
}

// Judge judges step against the rows WithStepRows declared. The built-in
// rules judge the command and paths its row points at, then each StepRule
// judges it, and the strictest decision wins. Judge never changes, holds
// or takes the step. An undeclared kind is a deny. The error is a step
// that cannot be judged, or a failed StepRule.
func (m *Magus) Judge(ctx context.Context, step agent.Step) (types.GuardJudgment, error) {
	req := guard.Request{Dir: step.Dir, Session: step.Session, Agent: step.Agent, Lease: types.JobLeaseFromContext(ctx)}
	req.Authority, _ = ctx.Value(authorityKey{}).(string)
	authority := guard.ResolveAuthority(ctx, guard.Dependencies{CacheDir: m.guardCacheDir, JobReader: m.jobReader}, req)
	deps, err := m.guardDependenciesAt(ctx, authority)
	if err != nil {
		return types.GuardJudgment{}, err
	}
	return guard.JudgeStep(ctx, deps, req, step)
}

func (m *Magus) judge(ctx context.Context, req guard.Request) (types.GuardJudgment, error) {
	req.Lease = types.JobLeaseFromContext(ctx)
	req.Authority, _ = ctx.Value(authorityKey{}).(string)
	authority := guard.ResolveAuthority(ctx, guard.Dependencies{CacheDir: m.guardCacheDir, JobReader: m.jobReader}, req)
	deps, err := m.guardDependenciesAt(ctx, authority)
	if err != nil {
		return types.GuardJudgment{}, err
	}
	return guard.Judge(ctx, deps, req), nil
}

// guardCacheDir is the cache dir of the workspace at root, for the guard.
func (m *Magus) guardCacheDir(root string) (string, error) {
	if m.ownRoot(root) {
		return m.CacheDir(), nil
	}
	return ResolveCacheDir(root, m.processOptions()...)
}

// guardDependencies is what the guard judges a call with no lease by.
func (m *Magus) guardDependencies(ctx context.Context) (guard.Dependencies, error) {
	return m.guardDependenciesAt(ctx, guard.CallAuthority{})
}

// guardDependenciesAt is what the guard judges a call with authority by: the
// policy of its row's checkout, else this workspace's. The error is a
// program with no Buzz engine, where neither side of that policy loads.
func (m *Magus) guardDependenciesAt(ctx context.Context, authority guard.CallAuthority) (guard.Dependencies, error) {
	root := m.Root()
	if authority.Checkout != "" {
		leased, err := workspace.FindRoot(authority.Checkout)
		if err != nil {
			return guard.Dependencies{}, fmt.Errorf("lease %s: its checkout %s holds no workspace to judge it by: %w", authority.Lease, authority.Checkout, err)
		}
		root = leased
	}
	deps, _ := guard.NewDependencies(ctx, guard.Wiring{
		Root:          root,
		Authority:     authority.Revision,
		Config:        m.cfg,
		BinaryVersion: m.version,
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if m.ownRoot(root) {
				return m, nil
			}
			return Inspect(ctx, root, m.processOptions()...)
		},
		CacheDir: m.guardCacheDir,
	})
	if notLinked := (*types.EngineNotLinkedError)(nil); errors.As(deps.LoadFailure, &notLinked) {
		return guard.Dependencies{}, deps.LoadFailure
	}
	deps.Workspace = m.Root()
	deps.ModelTiers = func() *agent.ModelTierSet { return m.ModelTiers(ctx) }
	deps.TierRule = m.spawnRule
	deps.JobReader = m.jobReader
	deps.Steps = m.steps
	return deps, nil
}

// ownRoot reports whether root names this workspace, "" being the caller's own.
func (m *Magus) ownRoot(root string) bool {
	return root == "" || filepath.Clean(root) == filepath.Clean(m.Root())
}

// judgeRun judges each command line a run stands for, in order, and stops at the first
// deny. A ctx the CLI marked is not judged: its host's hook judged the invocation already.
func (m *Magus) judgeRun(ctx context.Context, o run, lines []string) error {
	if guard.HostJudged(ctx) {
		return nil
	}
	for _, line := range lines {
		// A dry run executes nothing, so its judgment records nothing either.
		j, err := m.judge(ctx, guard.Request{Input: line, Dir: m.Root(), Session: o.session, DryRun: o.DryRun})
		if err != nil {
			return err
		}
		if j.Decision == string(types.GuardDeny) {
			return &types.GuardDeniedError{Judgment: j}
		}
	}
	return nil
}

// runLines is the `magus run` line each target is judged as.
func runLines(targets []types.Target, extraArgs []string) ([]string, error) {
	lines := make([]string, len(targets))
	for i, t := range targets {
		args := []string{t.Name, t.Path}
		if len(extraArgs) > 0 {
			args = append(append(args, "--"), extraArgs...)
		}
		words, err := shellWords(args)
		if err != nil {
			return nil, err
		}
		lines[i] = hint.Run.WithAs(hint.DefaultBinaryName, words...)
	}
	return lines, nil
}

// affectedLine is the `magus affected` line a run over the affected set is judged as.
func affectedLine(target, baseRef string) ([]string, error) {
	args := []string{target}
	if baseRef != "" {
		args = append(args, "--base", baseRef)
	}
	words, err := shellWords(args)
	if err != nil {
		return nil, err
	}
	return []string{hint.Affected.WithAs(hint.DefaultBinaryName, words...)}, nil
}

// shellWords quotes each argument that needs it, so the line judged splits back into the
// arguments the run takes. An argument bash cannot spell (one holding a NUL byte) is an
// error: no command line carries it, so there is no line to judge.
func shellWords(args []string) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		quoted, err := syntax.Quote(a, syntax.LangBash)
		if err != nil {
			return nil, fmt.Errorf("run argument %q cannot be judged as a command line: %w", a, err)
		}
		out[i] = quoted
	}
	return out, nil
}
