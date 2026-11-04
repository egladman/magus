package magus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
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
	if opts.ChangedPaths != nil && opts.BaseRef != "" {
		return types.ShardPlan{}, fmt.Errorf("affected plan: changed paths and base ref are mutually exclusive")
	}

	var (
		targets     []types.Target
		source      string
		unboundedBy string
	)
	if opts.ChangedPaths != nil {
		result, err := m.AffectedFromPaths(ctx, opts.ChangedPaths)
		if err != nil {
			return types.ShardPlan{}, fmt.Errorf("affected plan: compute paths: %w", err)
		}
		targets = make([]types.Target, len(result.Affected))
		for i, path := range result.Affected {
			targets[i] = types.Target{Path: path, Name: target, Files: result.FilesBySeed[path]}
		}
		source = "stdin paths"
		result.Changed = opts.ChangedPaths
		unboundedBy = m.unboundedBy(result)
	} else {
		var (
			fellBack bool
			res      *types.AffectedResult
			err      error
		)
		targets, source, fellBack, res, err = m.ExpandAffectedSet(ctx, target, opts.BaseRef)
		if err != nil {
			return types.ShardPlan{}, err
		}
		switch {
		case fellBack:
			unboundedBy = fmt.Sprintf("the VCS could not diff, so the plan holds every project (%s)", source)
		case res != nil:
			unboundedBy = m.unboundedBy(res)
		}
	}
	affected := make([]string, len(targets))
	for i, t := range targets {
		affected[i] = t.Path
	}
	slices.Sort(affected)

	units, unplanned, err := m.planUnits(ctx, targets, target, opts.Charms, opts.Args)
	if err != nil {
		return types.ShardPlan{}, fmt.Errorf("affected plan: %w", err)
	}
	machine, err := configuredMachine(m.cfg.CI.Machine)
	if err != nil && !errors.Is(err, errNoMachine) {
		return types.ShardPlan{}, fmt.Errorf("affected plan: %w", err)
	}
	var fallbacks []string
	if machine == nil {
		fallbacks = append(fallbacks, "no ci.machine is configured, so the plan checks no shard against a memory budget")
	}

	histPath := opts.HistoryPath
	if histPath == "" {
		histPath = m.cfg.HistoryPath
	}
	var hist forecast.History
	if err := hist.Load(ctx, histPath); err != nil {
		return types.ShardPlan{}, fmt.Errorf("affected plan: load history: %w", err)
	}

	f := forecast.Forecaster{History: hist, Needs: m.unitNeeds}
	if machine != nil {
		f.MemoryBudgetBytes = forecast.BudgetBytes(machine.MemoryMB)
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

	maxShards := opts.MaxShards
	if maxShards == 0 {
		maxShards = m.cfg.CI.MaxShards
	}
	if f.Cold(units) && (maxShards < 0 || maxShards > forecast.ColdShards) {
		maxShards = forecast.ColdShards
		fallbacks = append(fallbacks, fmt.Sprintf(
			"the history holds no duration for any unit, so the plan opens at most %d shards", forecast.ColdShards))
	}

	plan, err := ci.Build(units, source, ci.WithMaxShards(maxShards), ci.WithForecaster(f))
	if err != nil {
		return types.ShardPlan{}, fmt.Errorf("affected plan: %w", err)
	}

	maxParallel := len(plan.Shards)
	if opts.RunnerPoolBudget > 0 && opts.RunnerPoolBudget < maxParallel {
		maxParallel = opts.RunnerPoolBudget
	}

	shards := make([]types.Shard, len(plan.Shards))
	for i, s := range plan.Shards {
		shards[i] = types.Shard{
			ID: s.ID, Label: types.UnitsDigest(s.Units),
			ProjectPaths: types.UnitProjects(s.Units), Units: s.Units,
		}
	}
	placeDuties(shards)
	// Both are asked of the concrete forecaster rather than carried out of ci.Build: the
	// Forecaster interface returns an assignment and nothing about how it was reached, so
	// a plan built from real history and one built from fallbacks are the same value.
	assignments := make([][]types.Unit, len(plan.Shards))
	for i, s := range plan.Shards {
		assignments[i] = s.Units
	}
	var overBudget []string
	for _, i := range f.OverBudget(assignments) {
		overBudget = append(overBudget, plan.Shards[i].ID)
	}
	return types.ShardPlan{
		Shards:      shards,
		Source:      source,
		MaxParallel: maxParallel,
		Charms:      opts.Charms,
		Args:        opts.Args,
		Machine:     machine,
		Fallbacks:   fallbacks,
		Unplanned:   unplanned,
		Sufficient:  f.SufficientShards(units, len(plan.Shards)),
		OverBudget:  overBudget,
		Unmeasured:  f.Unmeasured(units),
		Affected:    affected,
		UnboundedBy: unboundedBy,
	}, nil
}

// planUnits is each step anchor asks for through ctx.needs, under charms and
// args. A project whose anchor asks for nothing is the unit (path, anchor).
// unplanned names targets an anchor calls directly that no unit runs.
func (m *Magus) planUnits(ctx context.Context, targets []types.Target, anchor string, charms, args []string) (units []types.Unit, unplanned []types.DirectCall, err error) {
	seen := map[types.Unit]bool{}
	add := func(u types.Unit) {
		if !seen[u] {
			seen[u] = true
			units = append(units, u)
		}
	}
	for _, t := range targets {
		p := m.Get(t.Path)
		if p == nil {
			continue
		}
		steps, err := m.anchorSteps(ctx, p, anchor, charms, args)
		if err != nil {
			return nil, nil, err
		}
		if len(steps) == 0 {
			add(types.Unit{Project: p.Path, Target: anchor})
			continue
		}
		for _, u := range steps {
			add(types.Unit{Project: cmp.Or(u.Project, p.Path), Target: u.Target})
		}
		for _, call := range directCalls(p, anchor) {
			if !seen[types.Unit{Project: p.Path, Target: call}] {
				unplanned = append(unplanned, types.DirectCall{Project: p.Path, Anchor: anchor, Target: call})
			}
		}
	}
	return units, unplanned, nil
}

// anchorSteps evaluates anchor's body in p as a dry run does, under charms and
// args, and returns the steps it asks for through ctx.needs. The recorder
// answers ctx.needs and the trace stubs every effectful host call.
func (m *Magus) anchorSteps(ctx context.Context, p *types.Project, anchor string, charms, args []string) ([]types.Unit, error) {
	if !slices.Contains(p.MagusfileTargets, types.Normalize(anchor)) {
		return nil, nil
	}
	rec := types.NewStepLog()
	ctx = types.WithStepLog(ctx, rec)
	ctx = types.WithTrace(types.WithCharms(ctx, charms))
	ctx = project.WithExtraArgs(installWorkspaceRegistry(ctx, m.wsReg), args)
	ctx = types.WithWorkspace(ctx, m.internals())
	ctx = buzz.WithTargetRuns(ctx, buzz.NewTargetRuns())
	if err := m.runTarget(ctx, p, anchor); err != nil {
		return nil, fmt.Errorf("evaluate %s in %s: %w", anchor, p.Path, err)
	}
	return rec.Units(), nil
}

// directCalls are the exported targets anchor's body in p calls as functions,
// read from the magusfile. ctx.needs is the only call the recorder sees.
func directCalls(p *types.Project, anchor string) []string {
	src, err := os.ReadFile(filepath.Join(p.Dir, "magusfile.buzz"))
	if err != nil {
		return nil
	}
	prog, err := buzz.ParseEmbedded(string(src))
	if err != nil || prog == nil {
		return nil
	}
	funcs := map[string]*ast.FunDecl{}
	for _, stmt := range prog.Stmts {
		if fn, ok := stmt.(*ast.FunDecl); ok {
			funcs[fn.Name] = fn
		}
	}
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
			if u.Project == "." {
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
func (m *Magus) unitNeeds(u types.Unit) []types.Unit {
	var out []types.Unit
	// The callback returns no error, so the walk returns none.
	_ = types.WalkChain(m.Get(u.Project), u.Target, m.Get, func(v types.ChainVisit) error {
		out = append(out, types.Unit{Project: v.Project.Path, Target: v.Target})
		return nil
	})
	return out
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
