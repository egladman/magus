package magus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/parsecache"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// PlanOptions configures a [Magus.Plan] call.
type PlanOptions struct {
	// MaxShards caps the number of CI shards. -1 = unlimited; 0 uses the
	// value from magus.yaml (CI.MaxShards).
	MaxShards int
	// RunnerPoolBudget limits cross-shard concurrency. 0 = unlimited.
	RunnerPoolBudget int
	// HistoryPath overrides the configured history_path when non-empty.
	HistoryPath string
	// BaseRef overrides the VCS base used to compute the affected set.
	// It is mutually exclusive with ChangedPaths.
	BaseRef string
	// ChangedPaths computes the affected set from these repo-relative paths
	// instead of a VCS diff. A non-nil empty slice deliberately means no paths.
	ChangedPaths []string
	// Charms and Args are what each shard runs the target under. The planner
	// evaluates the target's body under them, so the units are the steps the
	// shard takes.
	Charms []string
	Args   []string
}

// Plan computes a provider-neutral CI shard plan for the affected project
// set using target as the CI target (typically "ci"). Adaptive sharding is applied
// when runtime history is available at the resolved HistoryPath.
func (m *Magus) Plan(ctx context.Context, target string, opts PlanOptions) (types.ShardPlan, error) {
	scope, err := m.planScope(ctx, target, opts)
	if err != nil {
		return types.ShardPlan{}, err
	}
	units, err := m.planUnits(ctx, scope.targets, target, opts.Charms, opts.Args)
	if err != nil {
		return types.ShardPlan{}, fmt.Errorf("affected plan: %w", err)
	}
	machine, err := configuredMachine(m.cfg.CI.Machine)
	if err != nil && !errors.Is(err, errNoMachine) {
		return types.ShardPlan{}, fmt.Errorf("affected plan: %w", err)
	}
	var fallbacks []types.Fallback
	if machine == nil {
		fallbacks = append(fallbacks, types.Fallback{
			What: "No memory check",
			Why:  "magus.yaml sets no `ci.machine`",
			Effect: "No shard is checked against a runner's memory. A shard that runs out shows as cancelled, with no logs. " +
				"Set `ci.machine` to check.",
		})
	}
	hist, err := m.planHistory(ctx, opts)
	if err != nil {
		return types.ShardPlan{}, err
	}
	reach, err := m.planReach(units, scope)
	if err != nil {
		return types.ShardPlan{}, err
	}
	f := planModel(hist, reach, machine, scope.targets)
	maxShards, cold := m.planMaxShards(opts, &f, reach.kept)
	fallbacks = append(fallbacks, cold...)
	plan, err := ci.Build(reach.kept, scope.source, ci.WithMaxShards(maxShards), ci.WithModel(&f))
	if err != nil {
		return types.ShardPlan{}, fmt.Errorf("affected plan: %w", err)
	}
	fallbacks = append(fallbacks, plan.Fallbacks...)
	maxParallel := len(plan.Shards)
	if opts.RunnerPoolBudget > 0 && opts.RunnerPoolBudget < maxParallel {
		maxParallel = opts.RunnerPoolBudget
	}
	// Stage one replays what stage zero computed, so it may repeat it.
	stageOne := f
	var stageZero []types.Shard
	var zeroOverBudget []string
	if len(plan.StageZero) > 0 {
		stageOne = f.Replaying(shardUnits(plan.StageZero))
		if stageZero, zeroOverBudget, err = planShards(&f, plan.StageZero); err != nil {
			return types.ShardPlan{}, err
		}
	}
	shards, oneOverBudget, err := planShards(&stageOne, plan.Shards)
	if err != nil {
		return types.ShardPlan{}, err
	}
	placeDuties(shards)
	var unpeaked []types.Fallback
	if unmeasured := f.UnmeasuredPeaks(reach.kept); f.MemoryBudgetBytes > 0 && len(unmeasured) > 0 {
		unpeaked = []types.Fallback{{
			What:   "Peak memory counted as zero",
			Why:    "the run history holds no peak memory for " + joinUnits(unmeasured),
			Effect: "The memory check can pass a shard that runs out. A main run records peaks for the next plan.",
		}}
	}
	return types.ShardPlan{
		StageZero:   stageZero,
		Shards:      shards,
		Source:      scope.source,
		MaxParallel: maxParallel,
		Charms:      opts.Charms,
		Args:        opts.Args,
		Machine:     machine,
		Fallbacks:   append(fallbacks, unpeaked...),
		Sufficient:  stageOne.SufficientShards(shardUnits(plan.Shards), len(plan.Shards)),
		OverBudget:  slices.Concat(zeroOverBudget, oneOverBudget),
		Unmeasured:  f.Unmeasured(reach.kept),
		Unreached:   reach.unreached,
		Affected:    scope.affected,
		UnboundedBy: scope.unboundedBy,
	}, nil
}

// planScope is the affected set a plan covers and how it was found.
type planScope struct {
	targets     []types.Target
	source      string
	unboundedBy string
	changed     []string
	affected    []string // the targets' paths, sorted
}

// planScope reads the affected set from opts' changed paths, or else from a
// VCS diff against opts' base.
func (m *Magus) planScope(ctx context.Context, target string, opts PlanOptions) (planScope, error) {
	if opts.ChangedPaths != nil && opts.BaseRef != "" {
		return planScope{}, fmt.Errorf("affected plan: changed paths and base ref are mutually exclusive")
	}
	var s planScope
	if opts.ChangedPaths != nil {
		result, err := m.AffectedFromPaths(ctx, opts.ChangedPaths)
		if err != nil {
			return planScope{}, fmt.Errorf("affected plan: compute paths: %w", err)
		}
		s.targets = make([]types.Target, len(result.Affected))
		for i, path := range result.Affected {
			s.targets[i] = types.Target{Path: path, Name: target, Files: result.FilesBySeed[path]}
		}
		s.source = "stdin paths"
		result.Changed = opts.ChangedPaths
		s.changed = opts.ChangedPaths
		s.unboundedBy = m.unboundedBy(result)
	} else {
		targets, source, fellBack, res, err := m.ExpandAffectedSet(ctx, target, opts.BaseRef)
		if err != nil {
			return planScope{}, err
		}
		s.targets, s.source = targets, source
		switch {
		case fellBack:
			s.unboundedBy = fmt.Sprintf("the VCS could not diff, so the plan holds every project (%s)", source)
		case res != nil:
			s.changed = res.Changed
			s.unboundedBy = m.unboundedBy(res)
		}
	}
	s.affected = make([]string, len(s.targets))
	for i, t := range s.targets {
		s.affected[i] = t.Path
	}
	slices.Sort(s.affected)
	return s, nil
}

// planHistory loads the runtime history opts names, or the configured one.
func (m *Magus) planHistory(ctx context.Context, opts PlanOptions) (forecast.History, error) {
	histPath := opts.HistoryPath
	if histPath == "" {
		histPath = m.cfg.HistoryPath
	}
	var hist forecast.History
	if err := hist.Load(ctx, histPath); err != nil {
		return forecast.History{}, fmt.Errorf("affected plan: load history: %w", err)
	}
	return hist, nil
}

// planReach is each unit's prerequisites and whether the change makes it
// compute: kept units do, and unreached ones replay.
type planReach struct {
	needs           map[types.Unit][]types.Unit
	computes        func(types.Unit) bool
	kept, unreached []types.Unit
}

func (m *Magus) planReach(units []types.Unit, scope planScope) (planReach, error) {
	// optimization: each closure is walked once; reach and packing read the
	// map, where each asked WalkChain again for every unit and prerequisite.
	// A prerequisite's closure is there too, for stage zero to pack it.
	r := planReach{needs: make(map[types.Unit][]types.Unit, len(units))}
	closureOf := func(u types.Unit) ([]types.Unit, error) {
		if c, ok := r.needs[u]; ok {
			return c, nil
		}
		c, err := m.unitNeeds(u)
		if err == nil {
			r.needs[u] = c
		}
		return c, err
	}
	for _, u := range units {
		closure, err := closureOf(u)
		if err != nil {
			return planReach{}, fmt.Errorf("affected plan: %w", err)
		}
		for _, n := range closure {
			if _, err := closureOf(n); err != nil {
				return planReach{}, fmt.Errorf("affected plan: %w", err)
			}
		}
	}
	r.computes = func(types.Unit) bool { return true }
	if scope.unboundedBy == "" && scope.changed != nil {
		r.computes = m.computedBy(scope.changed, scope.affected, closureOf)
	}
	for _, u := range units {
		if r.computes(u) {
			r.kept = append(r.kept, u)
		} else {
			r.unreached = append(r.unreached, u)
		}
	}
	slices.SortFunc(r.unreached, func(a, b types.Unit) int { return cmp.Compare(a.String(), b.String()) })
	return r, nil
}

// planModel is the forecast the packer weighs shards with: durations from
// hist, prerequisites from reach, and machine's memory budget and cores.
func planModel(hist forecast.History, reach planReach, machine *types.Machine, targets []types.Target) forecast.Model {
	f := forecast.Model{
		History:    hist,
		Needs:      func(u types.Unit) []types.Unit { return reach.needs[u] },
		Repeatable: func(u types.Unit) bool { return !reach.computes(u) },
	}
	if machine != nil {
		f.MemoryBudgetBytes = forecast.BudgetBytes(machine.MemoryMB)
		f.Slots = machine.Cores
	}
	tags := make(map[string][]string, len(targets))
	for _, t := range targets {
		if len(t.Files) > 0 {
			tags[t.Path] = forecast.Tags(t.Path, t.Files)
		}
	}
	if len(tags) > 0 {
		f.TagsByProject = tags
	}
	return f
}

// planMaxShards is the shard cap from opts or magus.yaml. A history with no
// duration for any unit caps it at forecast.ColdShards, and says so.
func (m *Magus) planMaxShards(opts PlanOptions, f *forecast.Model, units []types.Unit) (int, []types.Fallback) {
	maxShards := opts.MaxShards
	if maxShards == 0 {
		maxShards = m.cfg.CI.MaxShards
	}
	if f.Cold(units) && (maxShards < 0 || maxShards > forecast.ColdShards) {
		return forecast.ColdShards, []types.Fallback{{
			What:   fmt.Sprintf("At most %d shards", forecast.ColdShards),
			Why:    "the run history holds no duration for any target",
			Effect: "Shards are packed by count, not time, so one can run far longer than the rest. A main run records durations for the next plan.",
		}}
	}
	return maxShards, nil
}

// planShards turns one stage's planned shards into plan shards. It refuses a
// unit two shards would compute, and names the shards over f's memory budget.
func planShards(f *forecast.Model, planned []ci.Shard) ([]types.Shard, []string, error) {
	shards := make([]types.Shard, len(planned))
	assignments := make([][]types.Unit, len(planned))
	for i, s := range planned {
		shards[i] = types.Shard{
			ID: s.ID, Digest: types.UnitsDigest(s.Units),
			ProjectPaths: types.UnitProjects(s.Units), Units: s.Units,
		}
		assignments[i] = s.Units
	}
	if repeated := f.Repeats(assignments); len(repeated) > 0 {
		return nil, nil, fmt.Errorf("affected plan: %s would compute on more than one shard", joinUnits(repeated))
	}
	var overBudget []string
	for _, i := range f.OverBudget(assignments) {
		overBudget = append(overBudget, planned[i].ID)
	}
	return shards, overBudget, nil
}

// shardUnits is every unit planned holds, in shard order.
func shardUnits(planned []ci.Shard) []types.Unit {
	var units []types.Unit
	for _, s := range planned {
		units = append(units, s.Units...)
	}
	return units
}

// planUnits is each step anchor asks for through ctx.needs, under charms and
// args. A project whose anchor asks for nothing is the unit (path, anchor).
// A direct call is an error naming each call, since no unit would run it.
func (m *Magus) planUnits(ctx context.Context, targets []types.Target, anchor string, charms, args []string) ([]types.Unit, error) {
	var units []types.Unit
	var unplanned []string
	seen := map[types.Unit]bool{}
	add := func(u types.Unit) {
		if !seen[u] {
			seen[u] = true
			units = append(units, u)
		}
	}
	for _, t := range targets {
		if m.Get(t.Path) == nil {
			return nil, fmt.Errorf("project %q is affected but not in the workspace", t.Path)
		}
	}
	evals := m.traceAnchors(ctx, targets, anchor, charms, args)
	for i, t := range targets {
		p := m.Get(t.Path)
		steps, found, err := evals[i].steps, evals[i].found, evals[i].err
		if err != nil {
			return nil, err
		}
		if !found || len(steps) == 0 {
			add(types.Unit{Project: p.Path, Target: anchor})
			continue
		}
		for _, u := range steps {
			add(u)
		}
		calls, err := directCalls(p, anchor)
		if err != nil {
			return nil, err
		}
		for _, call := range calls {
			if !seen[types.Unit{Project: p.Path, Target: call}] {
				unplanned = append(unplanned, types.DirectCall{Project: p.Path, Anchor: anchor, Target: call}.String())
			}
		}
	}
	if len(unplanned) > 0 {
		return nil, fmt.Errorf("the anchor calls targets directly, so no shard would run them: %s", strings.Join(unplanned, "; "))
	}
	return units, nil
}

// tracedAnchor is the steps one project's anchor asks for, as anchorSteps
// returns them.
type tracedAnchor struct {
	steps []types.Unit
	found bool
	err   error
}

// traceAnchors evaluates each target's anchor, one result per target in
// order.
func (m *Magus) traceAnchors(ctx context.Context, targets []types.Target, anchor string, charms, args []string) []tracedAnchor {
	// optimization: each project's anchor evaluates on its own goroutine,
	// as the evaluations share nothing. Results merge in target order, so
	// the units and the first error stay deterministic.
	evals := make([]tracedAnchor, len(targets))
	var g errgroup.Group
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, t := range targets {
		g.Go(func() error {
			e := &evals[i]
			e.steps, e.found, e.err = m.anchorSteps(ctx, m.Get(t.Path), anchor, charms, args)
			return nil
		})
	}
	_ = g.Wait()
	return evals
}

// anchorSteps evaluates anchor's body in p as a dry run does, under charms and
// args, and returns the steps it asks for through ctx.needs. The recorder
// answers ctx.needs and the trace stubs every effectful host call. found is
// false when p's magusfile does not declare anchor.
func (m *Magus) anchorSteps(ctx context.Context, p *types.Project, anchor string, charms, args []string) (steps []types.Unit, found bool, err error) {
	if !slices.Contains(p.MagusfileTargets, types.Normalize(anchor)) {
		return nil, false, nil
	}
	rec := types.NewStepLog()
	ctx = types.WithStepLog(ctx, rec)
	ctx = types.WithTrace(types.WithCharms(ctx, charms))
	ctx = project.WithExtraArgs(installWorkspaceRegistry(ctx, m.wsReg), args)
	ctx = types.WithWorkspace(ctx, m.internals())
	ctx = buzz.WithTargetRuns(ctx, buzz.NewTargetRuns())
	if err := m.runTarget(ctx, p, anchor); err != nil {
		return nil, true, fmt.Errorf("evaluate %s in %s: %w", anchor, p.Path, err)
	}
	if effects := rec.Effects(); len(effects) > 0 {
		return nil, true, fmt.Errorf(
			"%s in %s does more than ctx.needs (%s), and a shard runs only the steps it asks for: move that work into a target %s needs",
			anchor, p.Path, strings.Join(effects, ", "), anchor)
	}
	declared := map[types.Unit]bool{}
	for _, s := range p.TargetChains[types.Normalize(anchor)] {
		declared[types.Unit{Project: cmp.Or(s.Project, p.Path), Target: types.Normalize(s.Target)}] = true
	}
	var undeclared []string
	for _, u := range rec.Units() {
		u = types.Unit{Project: cmp.Or(u.Project, p.Path), Target: types.Normalize(u.Target)}
		if !declared[u] {
			undeclared = append(undeclared, u.String())
		}
		steps = append(steps, u)
	}
	if len(undeclared) > 0 {
		return nil, true, fmt.Errorf(
			"%s in %s asks for %s, which its declared ctx.needs chain does not name, so no shard could walk its prerequisites",
			anchor, p.Path, strings.Join(undeclared, ", "))
	}
	return steps, true, nil
}

// directCalls are the exported targets anchor's body in p calls as functions,
// read from the magusfile. ctx.needs is the only call the recorder sees.
func directCalls(p *types.Project, anchor string) ([]string, error) {
	funcs, err := projectFuncDecls(p)
	if err != nil {
		return nil, err
	}
	return exportedCallees(funcs, anchor), nil
}

func projectFuncDecls(p *types.Project) (map[string]*ast.FunDecl, error) {
	srcs, err := interp.FindAll(p.Dir)
	if err != nil {
		return nil, fmt.Errorf("find %s's magusfile for direct calls: %w", p.Path, err)
	}
	funcs := map[string]*ast.FunDecl{}
	for _, src := range srcs {
		for _, file := range src.Files {
			text, err := os.ReadFile(file)
			if err != nil {
				return nil, fmt.Errorf("read %s's magusfile for direct calls: %w", p.Path, err)
			}
			prog, err := parsecache.Shared().ParseEmbedded(string(text))
			if err != nil {
				return nil, fmt.Errorf("parse %s's magusfile for direct calls: %w", file, err)
			}
			for _, stmt := range prog.Stmts {
				if fn, ok := stmt.(*ast.FunDecl); ok {
					funcs[fn.Name] = fn
				}
			}
		}
	}
	return funcs, nil
}

func exportedCallees(funcs map[string]*ast.FunDecl, anchor string) []string {
	var calls []string
	visited := map[string]bool{}
	var walk func(fn *ast.FunDecl)
	walk = func(fn *ast.FunDecl) {
		if fn == nil || visited[fn.Name] {
			return
		}
		visited[fn.Name] = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Callee.(*ast.IdentExpr)
			if !ok {
				return true
			}
			switch callee := funcs[id.Name]; {
			case callee == nil:
			case callee.IsExported:
				calls = append(calls, types.Normalize(callee.Name))
			default:
				walk(callee)
			}
			return true
		})
	}
	for name, fn := range funcs {
		if fn.IsExported && types.Normalize(name) == types.Normalize(anchor) {
			walk(fn)
		}
	}
	slices.Sort(calls)
	return slices.Compact(calls)
}

// errNoMachine is configuredMachine's answer when ci.machine is unset.
var errNoMachine = errors.New("no ci.machine is configured")

// configuredMachine is the machine ci.machine describes. One with only some of
// its sizes set is an error, not a partial budget.
func configuredMachine(c config.CIMachine) (*types.Machine, error) {
	if c == (config.CIMachine{}) {
		return nil, errNoMachine
	}
	if c.MemoryMB <= 0 || c.Cores <= 0 {
		return nil, fmt.Errorf("ci.machine %q needs both memory_mb and cores above zero (memory_mb=%d cores=%d)",
			c.Name, c.MemoryMB, c.Cores)
	}
	return &types.Machine{Name: c.Name, MemoryMB: c.MemoryMB, Cores: c.Cores}, nil
}

// placeDuties puts each workspace-wide duty on one shard. The cache export
// goes where the root's units are, so its bundle covers the most of the tree.
// Notes verify goes to the lightest shard. Ties go to the lower index.
func placeDuties(shards []types.Shard) {
	if len(shards) == 0 {
		return
	}
	rootUnits := func(s types.Shard) int {
		n := 0
		for _, u := range s.Units {
			if u.Project == types.RootProject {
				n++
			}
		}
		return n
	}
	root, fewest := 0, 0
	mostRoot, leastUnits := rootUnits(shards[0]), len(shards[0].Units)
	for i, s := range shards {
		if n := rootUnits(s); n > mostRoot {
			root, mostRoot = i, n
		}
		if n := len(s.Units); n < leastUnits {
			fewest, leastUnits = i, n
		}
	}
	shards[root].Duties = append(shards[root].Duties, types.DutyCacheExport)
	shards[fewest].Duties = append(shards[fewest].Duties, types.DutyNotesVerify)
}

// unitNeeds is u's transitive needs closure, u included.
func (m *Magus) unitNeeds(u types.Unit) ([]types.Unit, error) {
	var out []types.Unit
	err := types.WalkChain(m.Get(u.Project), u.Target, m.Get, func(v types.ChainVisit) error {
		out = append(out, types.Unit{Project: v.Project.Path, Target: v.Target})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk the needs of %s: %w", u, err)
	}
	return out, nil
}

// computedBy reports whether changed makes a unit compute rather than replay
// from the cache: a target in its closure reads a changed path. A target
// with no declared reads reads its project, so an affected one counts.
func (m *Magus) computedBy(changed, affected []string, closureOf func(types.Unit) ([]types.Unit, error)) func(types.Unit) bool {
	inAffected := map[string]bool{}
	for _, p := range affected {
		inAffected[p] = true
	}
	read := map[types.Unit]bool{}
	reads := func(n types.Unit) bool {
		if v, ok := read[n]; ok {
			return v
		}
		v := true
		if p := m.Get(n.Project); p != nil {
			node := cache.DeclaredNode(p, n.Target, types.TargetRef{Project: p.Path, Target: n.Target}, m.Get)
			if node.DeclaredReads {
				v = slices.ContainsFunc(changed, func(c string) bool { return types.MatchGlobs(node.Reads, c) })
			} else {
				v = inAffected[n.Project]
			}
		}
		read[n] = v
		return v
	}
	memo := map[types.Unit]bool{}
	return func(u types.Unit) bool {
		if v, ok := memo[u]; ok {
			return v
		}
		closure, err := closureOf(u)
		v := err != nil || slices.ContainsFunc(closure, reads)
		memo[u] = v
		return v
	}
}

func joinUnits(us []types.Unit) string {
	names := make([]string, len(us))
	for i, u := range us {
		names[i] = u.String()
	}
	return strings.Join(names, ", ")
}

// unboundedBy says why the closure computed from res is not a proof, or "" when it is.
func (m *Magus) unboundedBy(res *types.AffectedResult) string {
	change, unclaimed := m.unboundedPaths(res)
	if change != "" {
		return change
	}
	for _, p := range res.Changed {
		if why, ok := unclaimed[p]; ok {
			return why
		}
	}
	return ""
}

// unboundedPaths splits unboundedBy by reach. change is why the whole change can move
// the graph (an edge input, an opaque provider); unclaimed names each path no project
// claims, which reaches nothing the closure can name but moves no edge either.
func (m *Magus) unboundedPaths(res *types.AffectedResult) (change string, unclaimed map[string]string) {
	claimed := make(map[string]bool, len(res.Changed))
	for _, files := range res.FilesBySeed {
		for _, f := range files {
			claimed[f] = true
		}
	}
	if len(res.Changed) > 0 {
		if name := m.opaqueProvider(); name != "" {
			return fmt.Sprintf("workspace provider %s declares no inputs, so any file may decide the project graph", name), nil
		}
	}
	edge := m.edgeInputs()
	for _, p := range res.Changed {
		if edge(p) {
			return unboundedBy([]string{p}, claimed, edge), nil
		}
	}
	unclaimed = map[string]string{}
	for _, p := range res.Changed {
		if !claimed[p] {
			unclaimed[p] = unboundedBy([]string{p}, claimed, edge)
		}
	}
	return "", unclaimed
}

// opaqueProvider names a wired workspace provider that declares no input globs, or "".
func (m *Magus) opaqueProvider() string {
	if m.wsReg == nil {
		return ""
	}
	for _, name := range m.wsReg.Providers() {
		if sp, ok := project.DefaultSpellRegistry().Lookup(name); !ok || len(sp.Sources()) == 0 {
			return name
		}
	}
	return ""
}

// edgeInputs matches the files that can move the graph's edges or every project's
// verdict at once without seeding the projects they reach.
func (m *Magus) edgeInputs() func(string) bool {
	names := map[string]bool{config.Filename: true, config.DottedFilename: true, remotespell.LockFile: true}
	for _, p := range m.ws.All() {
		for _, sp := range p.ResolvedSpells {
			for _, mf := range sp.Manifests() {
				names[path.Base(mf.Value)] = true
				for _, lock := range mf.LockCandidates {
					names[lock] = true
				}
			}
		}
	}
	var globs []types.Glob
	if m.wsReg != nil {
		for _, name := range m.wsReg.Providers() {
			if sp, ok := project.DefaultSpellRegistry().Lookup(name); ok {
				// A malformed list matches nothing.
				if sources, _, err := types.SpellGlobs(sp); err == nil {
					globs = append(globs, sources...)
				}
			}
		}
	}
	return func(p string) bool {
		if names[path.Base(p)] || strings.HasSuffix(p, ".buzz") || types.LooksLikeBuildInput(p) {
			return true
		}
		return types.MatchGlobs(globs, p)
	}
}

// unboundedBy is the rule [Magus.unboundedBy] applies. The closure is computed from the
// declarations as they stand, so a change that edits them (a Buzz source, the workspace
// config or lock, a dependency manifest a spell or a workspace provider reads, a
// toolchain pin or rule set every project builds under) can add an edge the closure
// never saw. A path no project claims reaches nothing the closure can name.
func unboundedBy(changed []string, claimed map[string]bool, edgeInput func(string) bool) string {
	for _, p := range changed {
		if edgeInput(p) {
			return p + " can change the dependency graph or every project's build, which the affected set cannot see"
		}
	}
	for _, p := range changed {
		if !claimed[p] {
			return "no project claims " + p
		}
	}
	return ""
}
