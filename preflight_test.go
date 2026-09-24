package magus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preflightFixture opens a workspace of the given projects, each bound to one spell
// whose invoker counts executions per "project:target" and fails a target the fail
// predicate names. Chains are set by hand: this package's tests do not link the Buzz
// interpreter.
type preflightFixture struct {
	m     *Magus
	mu    sync.Mutex
	calls map[string]int
}

func (f *preflightFixture) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func newPreflightFixture(t *testing.T, spellName string, projects []string, targets []string, fail func(project, target string) bool) *preflightFixture {
	t.Helper()
	f := &preflightFixture{calls: map[string]int{}}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))
	for _, p := range projects {
		require.NoError(t, os.MkdirAll(filepath.Join(root, p), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, p, "magusfile.buzz"), []byte(""), 0o644))
	}
	spell := spells.NewSpell(spellName,
		spells.WithTargets(targets...),
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			proj := filepath.Base(req.Dir)
			f.mu.Lock()
			f.calls[proj+":"+req.Target]++
			f.mu.Unlock()
			if fail != nil && fail(proj, req.Target) {
				return nil, errors.New(req.Target + ": broken in " + proj)
			}
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := NewWorkspaceRegistry()
	for _, p := range projects {
		reg.RegisterProject(p, WithSpell(spellName))
	}
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	f.m = m
	return f
}

func TestPreflightFailureStopsEveryProjectBeforeTheInvokedTargetStarts(t *testing.T) {
	f := newPreflightFixture(t, "zzz-preflight-stop-spell", []string{"a", "b"}, []string{"ci", "gate"},
		func(project, target string) bool { return project == "b" && target == "gate" })
	for _, p := range []string{"a", "b"} {
		f.m.Get(p).TargetChains = map[string][]types.ChainStep{"ci": {{Target: "gate"}}}
	}

	err := f.m.Run(t.Context(), []types.Target{{Path: "a", Name: "ci"}, {Path: "b", Name: "ci"}}, WithPreflight("gate"))

	require.ErrorIs(t, err, types.PreflightFailed)
	var pf *PreflightError
	require.ErrorAs(t, err, &pf)
	assert.Equal(t, "gate", pf.Target)
	assert.Equal(t, []string{"b"}, pf.Projects)
	assert.Equal(t, ExitCodePreflightFailed, pf.ExitCode())
	assert.Equal(t, 1, f.count("b:gate"))
	assert.Zero(t, f.count("a:ci"), "nothing of the invoked target may start after a preflight failure")
	assert.Zero(t, f.count("b:ci"), "nothing of the invoked target may start after a preflight failure")
}

func TestPreflightOutsideTheClosureIsRefusedBeforeAnythingRuns(t *testing.T) {
	f := newPreflightFixture(t, "zzz-preflight-closure-spell", []string{"a"}, []string{"ci", "lint", "gate", "unrelated"}, nil)
	f.m.Get("a").TargetChains = map[string][]types.ChainStep{
		"ci":   {{Target: "lint"}},
		"lint": {{Target: "gate"}},
	}
	targets := []types.Target{{Path: "a", Name: "ci"}}

	err := f.m.Run(t.Context(), targets, WithPreflight("unrelated"))
	require.ErrorIs(t, err, types.PreflightOutsideClosure)
	head := firstLine(err.Error())
	assert.Contains(t, head, "--preflight unrelated")
	assert.Contains(t, head, "what ci runs")
	assert.Contains(t, err.Error(), "a:ci chain: lint", "the refusal prints the chain describe target prints")
	var stated interface{ ExitCode() int }
	require.ErrorAs(t, err, &stated)
	assert.Equal(t, 2, stated.ExitCode(), "a refusal is misuse: nothing was attempted")

	err = f.m.Run(t.Context(), targets, WithPreflight("ci"))
	require.ErrorIs(t, err, types.PreflightOutsideClosure, "the invoked target is not its own preflight")

	assert.Zero(t, f.count("a:ci")+f.count("a:unrelated"), "a refusal runs nothing")

	require.NoError(t, f.m.Run(t.Context(), targets, WithPreflight("gate")),
		"a target reached transitively, through lint, is in the closure")
	assert.Equal(t, 1, f.count("a:gate"))
	assert.Equal(t, 1, f.count("a:ci"))
}

func TestRunPreflightRunsThePassAndNotTheInvokedTarget(t *testing.T) {
	f := newPreflightFixture(t, "zzz-preflight-only-spell", []string{"a"}, []string{"ci", "gate"}, nil)
	f.m.Get("a").TargetChains = map[string][]types.ChainStep{"ci": {{Target: "gate"}}}
	targets := []types.Target{{Path: "a", Name: "ci"}}

	require.NoError(t, f.m.RunPreflight(t.Context(), targets, WithPreflight("gate")))
	assert.Equal(t, 1, f.count("a:gate"))
	assert.Zero(t, f.count("a:ci"), "the invoked target is the caller's to run elsewhere")

	require.NoError(t, f.m.RunPreflight(t.Context(), targets), "no preflight named runs nothing")
	assert.Equal(t, 1, f.count("a:gate"))

	err := f.m.RunPreflight(t.Context(), targets, WithPreflight("ci"))
	require.ErrorIs(t, err, types.PreflightOutsideClosure)
}

func TestPlanPreflightSkipsAProjectWhoseTargetDoesNotReachIt(t *testing.T) {
	f := newPreflightFixture(t, "zzz-preflight-plan-spell", []string{"a", "b"}, []string{"ci", "gate"}, nil)
	f.m.Get("a").TargetChains = map[string][]types.ChainStep{"ci": {{Target: "gate"}}}

	stages, err := f.m.planPreflight([]types.Target{{Path: "a", Name: "ci"}, {Path: "b", Name: "ci"}}, []string{"gate"})
	require.NoError(t, err)
	require.Len(t, stages, 1)
	require.Len(t, stages[0].projects, 1, "b's ci never reaches gate, so running it there would add work")
	assert.Equal(t, "a", stages[0].projects[0].Path)
}

// A composed skip_cache gate the preflight pass ran is not run a second time ahead of
// the replayed composer.
func TestPreflightCountsAsTheComposedGateRun(t *testing.T) {
	f := newPreflightFixture(t, "zzz-preflight-gate-spell", []string{"a"}, []string{"composer", "gate"}, nil)
	p := f.m.Get("a")
	p.TargetPolicies = map[string]types.Target{"gate": {SkipCache: true}}
	p.TargetChains = map[string][]types.ChainStep{"composer": {{Target: "gate"}}}
	p.TargetOutputs = map[string][]types.OutputRef{"gate": {{Glob: "GATE.md"}}}
	targets := []types.Target{{Path: "a", Name: "composer"}}
	ctx := t.Context()

	require.NoError(t, f.m.Run(ctx, targets))
	require.Equal(t, 1, f.count("a:composer"))
	key, _, err := f.m.ComputeTargetKey(ctx, "a", "composer", nil)
	require.NoError(t, err)

	require.NoError(t, f.m.Run(ctx, targets, WithPreflight("gate")))
	assert.Equal(t, 1, f.count("a:composer"), "the composer replays the entry a run without the flag wrote")
	assert.Equal(t, 1, f.count("a:gate"), "the gate ran once, in the preflight pass")

	after, _, err := f.m.ComputeTargetKey(ctx, "a", "composer", nil)
	require.NoError(t, err)
	assert.Equal(t, key, after, "the flag does not move the invoked target's key")
}

func TestPreflightErrorFirstLineNamesTargetProjectsAndFix(t *testing.T) {
	err := newPreflightError([]preflightFailure{
		{project: "proto", target: "generate", err: errors.New("generate: exit 1\nstderr follows")},
		{project: "docs", target: "generate", err: &types.OutputDriftError{
			Project: "docs", Target: "generate", Message: "docs: generate left declared output stale; re-run\nMAGUS.md",
		}},
	})
	lines := strings.Split(err.Error(), "\n")
	assert.Equal(t,
		"[MGS3020] preflight generate failed in docs, proto; fix with `magus run generate:rw docs`; reproduce with `magus run generate proto`",
		lines[0])
	assert.Equal(t, "  docs:generate: docs: generate left declared output stale; re-run", lines[1])
	assert.Equal(t, "  proto:generate: generate: exit 1", lines[2])
	assert.Equal(t, []string{"docs", "proto"}, err.Projects)
}
