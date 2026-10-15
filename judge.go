package magus

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"mvdan.cc/sh/v3/syntax"
)

// JudgeCommand judges command, a shell line about to run in dir, as the guard hook judges
// an agent's command: the compiled rules, the workspace's magus\guard rules beside their
// approved copies, and what session did before. The lease ctx carries (see
// types.WithJobLease) is the one the call acts under. The judgment is recorded on the
// workspace's trail as a hook's is.
//
// A deny is a judgment, returned with its Reason, Rule and Next and a nil error; a rule
// that would ask the person denies, since no host is here to put the question. The error
// is for a judgment that could not be made: the workspace's guard rules did not load,
// which is a *types.EngineNotLinkedError when this program links no Buzz engine.
func (m *Magus) JudgeCommand(ctx context.Context, session, dir, command string) (types.GuardJudgment, error) {
	return m.judge(ctx, guard.Request{Input: command, Dir: dir, Session: session})
}

// JudgeWrite is JudgeCommand for a file an edit is about to write: path, absolute or
// relative to dir.
func (m *Magus) JudgeWrite(ctx context.Context, session, dir, path string) (types.GuardJudgment, error) {
	return m.judge(ctx, guard.Request{Input: path, IsPath: true, Dir: dir, Session: session})
}

func (m *Magus) judge(ctx context.Context, req guard.Request) (types.GuardJudgment, error) {
	req.Lease = types.JobLeaseFromContext(ctx)
	deps, _ := guard.NewDependencies(ctx, guard.Wiring{
		Root:          m.Root(),
		Config:        m.cfg,
		BinaryVersion: m.version,
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if m.ownRoot(root) {
				return m, nil
			}
			return Inspect(ctx, root, m.processOptions()...)
		},
		CacheDir: func(root string) (string, error) {
			if m.ownRoot(root) {
				return m.CacheDir(), nil
			}
			return ResolveCacheDir(root, m.processOptions()...)
		},
	})
	if deps.LoadFailure != nil {
		return types.GuardJudgment{}, deps.LoadFailure
	}
	deps.ModelTiers = func() *types.ModelTierSet { return m.ModelTiers(ctx) }
	return guard.Judge(ctx, deps, req), nil
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
