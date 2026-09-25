package magus

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/cache"
	interp "github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/types"
)

// ExitCodePreflightFailed is the process status of a run stopped by its --preflight
// pass. Distinct from 1 so a CI script can tell the cheap check from the fan-out.
const ExitCodePreflightFailed = 3

// WithPreflight names targets to run first, as a separate pass across every selected
// project, before the invoked target starts anywhere.
//
// Each name must be a target the invoked target already reaches through ctx.needs in at
// least one selected project; the run is refused with MGS3021 before anything executes
// otherwise. A project whose invoked target does not reach the name runs no preflight
// for it. If any preflight step fails, admission stops (a spent failure budget of one,
// which cancels in-flight preflight steps), nothing of the invoked target starts, and
// the run returns a *PreflightError. When every step passes the main run proceeds and
// treats those targets as done: a ctx.needs of one returns without running it again.
func WithPreflight(names ...string) RunOption {
	return func(o *run) { o.Preflight = append(o.Preflight, names...) }
}

// RunPreflight runs only the preflight pass WithPreflight names for targets, and none
// of targets themselves: the gate a caller wants before work it hands elsewhere, such as
// a CI shard plan. Validation, keying and failure are exactly Run's. Charms must be
// the ones the invoked targets would run under (see CharmsForCI for ci). Without
// WithPreflight it runs nothing and returns nil.
func (m *Magus) RunPreflight(ctx context.Context, targets []types.Target, opts ...RunOption) error {
	o := applyRunOpts(opts)
	if len(o.Preflight) == 0 {
		return nil
	}
	pre, err := m.planPreflight(targets, o.Preflight)
	if err != nil {
		return err
	}
	o.preflight = pre
	return m.redactError(m.executeStages(attributeRun(ctx), nil, TargetLabel(targets, ""), o))
}

// PreflightError is a --preflight pass that failed. Its first line names the target,
// the failing projects and the command that fixes or reproduces them; the lines after
// it carry each failure's own first line.
type PreflightError struct {
	*types.DiagnosticError
	// Target is the preflight target that failed.
	Target string
	// Projects are the failing projects' paths, sorted.
	Projects []string
	// Fix is the command a reader runs next: the rw form for drift, the plain target
	// otherwise, or both joined when the failures are mixed.
	Fix string
}

// ExitCode is the status the CLI and the server exit with.
func (e *PreflightError) ExitCode() int { return ExitCodePreflightFailed }

// Unwrap exposes the diagnostic, so errors.Is(err, types.PreflightFailed) holds. The
// target failures stay out of the chain: they were reported as they happened.
func (e *PreflightError) Unwrap() error { return e.DiagnosticError }

type preflightFailure struct {
	project string
	target  string
	err     error
}

// newPreflightError builds the error for a failed pass. The first failing target wins
// the summary line when several preflight targets failed.
func newPreflightError(failures []preflightFailure) *PreflightError {
	slices.SortFunc(failures, func(a, b preflightFailure) int {
		if c := strings.Compare(a.target, b.target); c != 0 {
			return c
		}
		return strings.Compare(a.project, b.project)
	})
	target := failures[0].target
	var projects, drifted, broken []string
	var details []string
	for _, f := range failures {
		details = append(details, fmt.Sprintf("  %s:%s: %s", f.project, f.target, firstLine(f.err.Error())))
		if f.target != target {
			continue
		}
		projects = append(projects, f.project)
		var drift *types.OutputDriftError
		if errors.As(f.err, &drift) {
			drifted = append(drifted, f.project)
		} else {
			broken = append(broken, f.project)
		}
	}
	var fixes []string
	if len(drifted) > 0 {
		fixes = append(fixes, "fix with `magus run "+target+":"+types.CharmReadWrite+" "+strings.Join(drifted, " ")+"`")
	}
	if len(broken) > 0 {
		fixes = append(fixes, "reproduce with `magus run "+target+" "+strings.Join(broken, " ")+"`")
	}
	fix := strings.Join(fixes, "; ")
	head := fmt.Sprintf("preflight %s failed in %s; %s", target, strings.Join(projects, ", "), fix)
	return &PreflightError{
		DiagnosticError: types.DiagnosticErrorf(types.PreflightFailed, "%s\n%s", head, strings.Join(details, "\n")),
		Target:          target,
		Projects:        projects,
		Fix:             fix,
	}
}

// preflightRefusal is an MGS3021 refusal. It exits 2, the misuse status: nothing was
// attempted.
type preflightRefusal struct {
	*types.DiagnosticError
}

func (e preflightRefusal) ExitCode() int { return 2 }

func (e preflightRefusal) Unwrap() error { return e.DiagnosticError }

func refusePreflight(format string, args ...any) error {
	return preflightRefusal{types.DiagnosticErrorf(types.PreflightOutsideClosure, format, args...)}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// planPreflight resolves each preflight name to the projects whose invoked target
// reaches it through ctx.needs, the closure `magus describe target` prints the first hop
// of. A name no selected project reaches is refused with MGS3021.
func (m *Magus) planPreflight(targets []types.Target, names []string) ([]stage, error) {
	var stages []stage
	for _, raw := range names {
		name := types.Normalize(raw)
		var projects []*types.Project
		var outside []string
		for _, t := range targets {
			p := m.Get(t.Path)
			if p == nil {
				continue
			}
			if name == t.Name {
				return nil, refusePreflight(
					"--preflight %s names the target being run; a preflight is a target %s composes through ctx.needs",
					name, t.Name)
			}
			reached := false
			_ = types.WalkChain(p, t.Name, m.Get, func(v types.ChainVisit) error {
				if v.Depth == 0 || v.Target != name {
					return nil
				}
				reached = true
				if !slices.ContainsFunc(projects, func(q *types.Project) bool { return q.Path == v.Project.Path }) {
					projects = append(projects, v.Project)
				}
				return types.ErrSkipChain
			})
			if !reached {
				outside = append(outside, fmt.Sprintf("  %s:%s chain: %s", p.Path, t.Name, chainOrNone(p.TargetChains[t.Name])))
			}
		}
		if len(projects) == 0 {
			invoked := ""
			if len(targets) > 0 {
				invoked = targets[0].Name
			}
			const shown = 5
			if len(outside) > shown {
				outside = append(outside[:shown], fmt.Sprintf("  ... and %d more", len(outside)-shown))
			}
			return nil, refusePreflight(
				"--preflight %s is not in what %s runs in any selected project, so running it first would add work rather than reorder it (see `magus describe target %s`)\n%s",
				name, invoked, invoked, strings.Join(outside, "\n"))
		}
		stages = append(stages, stage{target: name, handler: m.targetHandler(name), projects: projects})
	}
	return stages, nil
}

func chainOrNone(chain []types.ChainStep) string {
	if len(chain) == 0 {
		return "(composes nothing)"
	}
	return types.Chain(chain).String()
}

// preflightDone is the set of (project, target) pairs a passed preflight covered.
type preflightDone map[string]bool

func preflightKey(project, target string) string { return project + "\x00" + target }

// targets lists the done target names in project, the names a memo is seeded with.
func (d preflightDone) targets(project string) []string {
	var out []string
	for key := range d {
		if p, t, _ := strings.Cut(key, "\x00"); p == project {
			out = append(out, t)
		}
	}
	return out
}

type preflightDoneKey struct{}

func withPreflightDone(ctx context.Context, d preflightDone) context.Context {
	return context.WithValue(ctx, preflightDoneKey{}, d)
}

func preflightDoneFrom(ctx context.Context) preflightDone {
	d, _ := ctx.Value(preflightDoneKey{}).(preflightDone)
	return d
}

// runPreflight runs the preflight stages as one batch and returns the pairs that passed.
// runStep is the main batch's step function, so a preflight step is keyed, admitted and
// reported exactly as the same target named on the command line would be.
func (m *Magus) runPreflight(ctx context.Context, stages []stage, newStep func(*types.Project, string) cache.Step, opts run, runStep func(map[string]TargetHandler, map[string]*types.Project) func(context.Context, cache.Step) error, cacheOpts []cache.RunOption) (preflightDone, error) {
	var steps []cache.Step
	handlerOf := make(map[string]TargetHandler, len(stages))
	byPath := map[string]*types.Project{}
	for _, st := range stages {
		handlerOf[st.target] = st.handler
		for _, p := range st.projects {
			// No ExtraArgs and no Spell: both belong to the target the user named, the
			// boundary a ctx.needs dependency draws too.
			step := newStep(p, st.target)
			if raceForcesNoCache(opts) {
				step.NoCache = true
			}
			if opts.NoCache {
				step.SkipReplay = true
			}
			steps = append(steps, step)
			byPath[p.Path] = p
		}
	}
	var mu sync.Mutex
	var failures []preflightFailure
	exec := runStep(handlerOf, byPath)
	// A budget of one: the first failure stops admission and cancels in-flight peers,
	// the existing semantics of a spent max_failures budget.
	_, runErr := m.cache.RunAll(ctx, steps, func(ctx context.Context, s cache.Step) error {
		err := exec(ctx, s)
		if err != nil && ctx.Err() == nil {
			mu.Lock()
			failures = append(failures, preflightFailure{project: s.ProjectPath, target: s.Target, err: err})
			mu.Unlock()
		}
		return err
	}, append(slices.Clone(cacheOpts), cache.WithMaxFailures(1))...)
	if len(failures) > 0 {
		return nil, newPreflightError(failures)
	}
	if runErr != nil {
		return nil, runErr
	}
	done := preflightDone{}
	for _, s := range steps {
		done[preflightKey(s.ProjectPath, s.Target)] = true
	}
	if cd := interp.CrossDispatchFromContext(ctx); cd != nil {
		for _, s := range steps {
			if p := byPath[s.ProjectPath]; p != nil {
				cd.MarkDone(p.Dir, s.Target)
			}
		}
	}
	return done, nil
}
