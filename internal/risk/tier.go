package risk

import (
	"cmp"
	"context"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Inputs is everything Assess reads, as values and functions, so the tier table is
// testable without a workspace, a VCS or a toolchain.
type Inputs struct {
	// Delta is the change's per-path classes, from Classifier.Classify.
	Delta Delta
	// Claims are the describe-file entries of the delta's paths.
	Claims []types.FileEntry
	// Affected are the paths of the projects the change reaches, which a gate runs over.
	Affected []string
	// UnboundedBy, when set, says why the change can move the affected set itself (a
	// declaration, a dependency manifest, a toolchain pin); every path then gates full.
	UnboundedBy string
	// Unbounded maps a path the affected set cannot place (no project claims it) to
	// why. That path gates full on its own; the rest of the change is still sized.
	Unbounded map[string]string
	Projects  []*types.Project
	// Target is the invoked target, the one a full gate runs.
	Target string
	// Provers are keyed by the file extension whose equivalence they prove (".go"); every
	// prover is asked to place every path a placement could lower.
	Provers map[string]Prover
	// Root is the absolute workspace root.
	Root string
	// Base names the revision the change is measured from.
	Base string
	// Read returns a path's content at Base and in the change. An error means one side
	// is unreadable, so no equivalence is proven.
	Read func(ctx context.Context, path string) (old, cur string, err error)
}

var tierRank = map[types.RiskTier]int{
	types.RiskTrivial: 0, types.RiskMechanical: 1, types.RiskScoped: 2, types.RiskFull: 3,
}

// Higher reports whether a outranks b.
func Higher(a, b types.RiskTier) bool { return tierRank[a] > tierRank[b] }

// reader is a (project, target) pair that declares a changed path as its input.
type reader struct{ project, target string }

// verdict is one path's tier before the gate is built.
type verdict struct {
	Classified
	tier types.RiskTier
	why  string
	// place is set while the path waits on a placement to settle its tier.
	place bool
	hit   *PackageHit
	// placed is the placement the hit came from, for its Closure and ops.
	placed *Placement
	// readers are the targets in the invoked target's chain that declare the path.
	readers []reader
	// mechanical is set for a path the drift check and lint must see whatever its tier:
	// a comment edit, which lint and generators read.
	mechanical bool
}

type assessment struct {
	in       Inputs
	entries  map[string]types.FileEntry
	projects map[string]*types.Project
	// chain are the (project, target) pairs the invoked target's chain reaches.
	chain map[[2]string]bool
}

// Assess tiers every path of the delta, takes the highest, and builds the gate that
// tier needs. A path lands on the lowest tier a class or a prover proves:
//
//	generated     trivial; mechanical if a source its generator reads changed too
//	prose         trivial; scoped if a target in the invoked target's chain declares
//	              it (the gate runs those readers) or a prover places it (go:embed)
//	comment-only  mechanical: lint and some generators read comments; scoped when a
//	              target in the chain declares it, which runs that reader too
//	code          mechanical if a prover proves it equivalent; scoped if a prover
//	              places it; else full
//
// A path no project claims is full on its own, and every path is full while
// UnboundedBy is set. The change's tier is its highest path's, and an empty delta is
// trivial.
func Assess(ctx context.Context, in Inputs) types.RiskReport {
	a := &assessment{
		in:       in,
		entries:  make(map[string]types.FileEntry, len(in.Claims)),
		projects: make(map[string]*types.Project, len(in.Projects)),
	}
	for _, e := range in.Claims {
		a.entries[e.Path] = e
	}
	for _, p := range in.Projects {
		a.projects[p.Path] = p
	}
	a.chain = a.chainOf(in.Target)

	verdicts := make([]verdict, len(in.Delta.Paths))
	for i, c := range in.Delta.Paths {
		verdicts[i] = a.classify(ctx, c)
	}
	a.place(ctx, verdicts)

	rep := types.RiskReport{
		Base: in.Base, Target: in.Target, Tier: types.RiskTrivial,
		Affected: slices.Clone(in.Affected), Evidence: []types.RiskEvidence{},
	}
	if rep.Affected == nil {
		rep.Affected = []string{}
	}
	for _, v := range verdicts {
		ev := types.RiskEvidence{Path: v.Path, Project: a.entries[v.Path].Project, Class: v.Class.String(), Tier: v.tier, Why: v.why}
		if v.hit != nil && v.placed.Closure != nil {
			if v.hit.TestOnly {
				ev.Packages = v.placed.Closure(nil, []string{v.hit.Package})
			} else {
				ev.Packages = v.placed.Closure([]string{v.hit.Package}, nil)
			}
		}
		rep.Evidence = append(rep.Evidence, ev)
		if Higher(v.tier, rep.Tier) {
			rep.Tier = v.tier
		}
	}
	a.gate(&rep, verdicts)
	return rep
}

func (a *assessment) classify(ctx context.Context, c Classified) verdict {
	v := verdict{Classified: c}
	if a.in.UnboundedBy != "" {
		v.tier, v.why = types.RiskFull, a.in.UnboundedBy
		return v
	}
	if why, ok := a.in.Unbounded[c.Path]; ok {
		v.tier, v.why = types.RiskFull, why
		return v
	}
	switch c.Class {
	case ClassGenerated:
		v.tier, v.why = a.generated(c)
	case ClassProse:
		v.place = true
		if v.readers = a.chainReaders(c.Path); len(v.readers) > 0 {
			v.tier, v.why = types.RiskScoped, "read by "+readerList(v.readers)+", which "+a.in.Target+" reaches"
			return v
		}
		v.tier, v.why = types.RiskTrivial, c.Why+"; nothing in "+a.in.Target+"'s chain reads it"
	case ClassCommentOnly:
		v.mechanical = true
		if v.readers = a.chainReaders(c.Path); len(v.readers) > 0 {
			v.tier, v.why = types.RiskScoped, c.Why+"; read by "+readerList(v.readers)+", which "+a.in.Target+" reaches"
			return v
		}
		v.tier, v.why = types.RiskMechanical, c.Why+"; lint and generators read comments"
	default:
		if p, ok := a.in.Provers[strings.ToLower(path.Ext(c.Path))]; ok && a.in.Read != nil {
			if old, cur, err := a.in.Read(ctx, c.Path); err == nil {
				if eq, why := p.Equivalent(c.Path, old, cur); eq {
					v.tier, v.why = types.RiskMechanical, why
					return v
				}
			}
		}
		v.tier, v.why, v.place = types.RiskFull, "no prover places it in a package, so magus cannot bound what reads it", true
	}
	return v
}

// generated trusts a generated file as committed unless something its generator reads
// changed in the same delta; then the drift check has to re-derive it.
func (a *assessment) generated(c Classified) (types.RiskTier, string) {
	entry := a.entries[c.Path]
	for _, claim := range entry.Claims {
		if claim.Role != "output" {
			continue
		}
		if f, ok := a.generatorTouched(claim); ok {
			return types.RiskMechanical, "generated by " + claimRef(claim) + ", and " + f + ", which it reads, changed too: the drift check re-derives it"
		}
		return types.RiskTrivial, "generated by " + claimRef(claim) + ", and nothing it reads changed"
	}
	return types.RiskTrivial, c.Why + ", and nothing generating it changed"
}

// generatorTouched finds a changed non-output path the generating claim reads: its
// target's declared inputs when it declares any, else its project's sources.
func (a *assessment) generatorTouched(out types.FileClaim) (string, bool) {
	explicit := false
	if p := a.projects[out.Project]; p != nil && out.Target != "" {
		explicit = len(p.TargetInputs[types.Normalize(out.Target)]) > 0
	}
	for _, c := range a.in.Delta.Paths {
		if c.Class == ClassGenerated {
			continue
		}
		for _, claim := range a.entries[c.Path].Claims {
			if claim.Role != "source" || claim.Project != out.Project {
				continue
			}
			if out.Target == "" || claim.Target == out.Target || (!explicit && claim.Target == "") {
				return c.Path, true
			}
		}
	}
	return "", false
}

// chainReaders returns the targets the invoked target's chain reaches that declare the
// path, as an input or as a file they rewrite in place, in claim order.
func (a *assessment) chainReaders(p string) []reader {
	var out []reader
	for _, claim := range a.entries[p].Claims {
		if claim.Target == "" || (claim.Role != "source" && claim.Role != "update") {
			continue
		}
		r := reader{claim.Project, types.Normalize(claim.Target)}
		if a.chain[[2]string{r.project, r.target}] && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func readerList(rs []reader) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.project + ":" + r.target
	}
	return strings.Join(names, ", ")
}

// chainOf walks target's ctx.needs chain from every project that has it, across
// projects, into the (project, target) pairs it reaches.
func (a *assessment) chainOf(target string) map[[2]string]bool {
	seen := map[[2]string]bool{}
	var walk func(project, target string)
	walk = func(project, target string) {
		key := [2]string{project, types.Normalize(target)}
		if seen[key] {
			return
		}
		seen[key] = true
		p := a.projects[project]
		if p == nil {
			return
		}
		for _, s := range p.TargetChains[key[1]] {
			walk(cmp.Or(s.Project, project), s.Target)
		}
	}
	for _, p := range a.in.Projects {
		if a.hasTarget(p.Path, target) {
			walk(p.Path, target)
		}
	}
	return seen
}

// place asks every prover to place the paths waiting on one, and settles their tiers.
// A placement can only raise a prose path's tier: embedded prose is scoped, and prose a
// prover knows but cannot bound is full.
func (a *assessment) place(ctx context.Context, verdicts []verdict) {
	var paths []string
	for _, v := range verdicts {
		if v.place {
			paths = append(paths, v.Path)
		}
	}
	if len(paths) == 0 {
		return
	}
	var placements []*Placement
	failed := ""
	for _, ext := range slices.Sorted(maps.Keys(a.in.Provers)) {
		pl, err := a.in.Provers[ext].Place(ctx, a.in.Root, paths)
		if err != nil {
			failed = "the " + ext + " prover failed, so what compiles or embeds it is unknown: " + err.Error()
			break
		}
		placements = append(placements, &pl)
	}
	for i := range verdicts {
		v := &verdicts[i]
		if !v.place {
			continue
		}
		v.place = false
		if failed != "" {
			v.tier, v.why = types.RiskFull, failed
			continue
		}
		for _, pl := range placements {
			if why, ok := pl.Unplaced[v.Path]; ok {
				v.tier, v.why, v.hit = types.RiskFull, why, nil
				break
			}
			if hit, ok := pl.Packages[v.Path]; ok && v.hit == nil {
				why := hit.Why
				if len(v.readers) > 0 {
					why += "; " + v.why
				}
				v.tier, v.why, v.hit, v.placed = types.RiskScoped, why, &hit, pl
			}
		}
	}
}

func (a *assessment) hasTarget(project, target string) bool {
	p := a.projects[project]
	if p == nil {
		return false
	}
	target = types.Normalize(target)
	if slices.Contains(p.MagusfileTargets, target) {
		return true
	}
	return slices.ContainsFunc(p.ResolvedSpells, func(s *spells.Spell) bool { return slices.Contains(s.Targets(), target) })
}

// step is one gate command before it is rendered.
type step struct {
	target   string
	project  string
	op       string
	packages []string
}

// scopedModule is one module whose packages a scoped change placed.
type scopedModule struct {
	placed   *Placement
	module   string
	project  *types.Project
	changed  []string
	testOnly []string
}

func (a *assessment) gate(rep *types.RiskReport, verdicts []verdict) {
	var steps []step
	switch rep.Tier {
	case types.RiskTrivial:
		rep.Gate = []types.RiskGateStep{}
		return
	case types.RiskMechanical:
		for _, p := range a.in.Affected {
			steps = append(steps, a.mechanicalSteps(p)...)
		}
	case types.RiskScoped:
		steps = a.scopedGate(rep, verdicts)
	default:
		for _, p := range a.in.Affected {
			if a.hasTarget(p, a.in.Target) {
				steps = append(steps, step{target: a.in.Target, project: p})
			}
		}
	}
	rep.Gate = render(steps)
}

// scopedGate builds a scoped change's gate. Go the prover placed narrows the project
// rooted at its module and runs every other affected project whole, as before any
// reader. Without Go, each affected project gets the mechanical steps when a path
// needs them. Either way every target that declares a changed prose or comment-only
// path runs, since it is the one whose result that path can move.
func (a *assessment) scopedGate(rep *types.RiskReport, verdicts []verdict) []step {
	var readers []reader
	mechanical := false
	for _, v := range verdicts {
		for _, r := range v.readers {
			if !slices.Contains(readers, r) {
				readers = append(readers, r)
			}
		}
		mechanical = mechanical || v.mechanical || v.tier == types.RiskMechanical
	}
	byProject := a.scopedModules(rep, verdicts)
	if rep.Tier == types.RiskFull {
		return a.fullSteps()
	}

	var steps []step
	whole := map[string]bool{}
	if len(byProject) > 0 {
		for _, p := range a.in.Affected {
			if sm := byProject[p]; sm != nil {
				narrowed, skipped, ok := a.scopedSteps(sm, readers)
				if ok {
					steps = append(steps, narrowed...)
					for _, t := range skipped {
						rep.Evidence = append(rep.Evidence, types.RiskEvidence{Project: p, Target: t, Tier: types.RiskScoped,
							Why: "skipped: the change touches none of the files it declares, so its cache key would not move"})
					}
					continue
				}
				rep.Evidence = append(rep.Evidence, types.RiskEvidence{Project: p, Target: a.in.Target, Tier: types.RiskScoped,
					Why: "no test target in " + a.in.Target + "'s chain runs " + sm.placed.Narrows + ", so its tests cannot be narrowed and it runs whole"})
			}
			if a.hasTarget(p, a.in.Target) {
				steps = append(steps, step{target: a.in.Target, project: p})
				whole[p] = true
			}
		}
	} else if mechanical {
		for _, p := range a.in.Affected {
			steps = append(steps, a.mechanicalSteps(p)...)
		}
	}
	for _, r := range readers {
		covered := whole[r.project] || slices.ContainsFunc(steps, func(s step) bool {
			return s.project == r.project && s.op == "" && slices.Contains(chainClosure(a.projects[s.project], s.target), r.target)
		})
		if !covered {
			steps = append(steps, step{target: r.target, project: r.project})
		}
	}
	return steps
}

func (a *assessment) fullSteps() []step {
	var steps []step
	for _, p := range a.in.Affected {
		if a.hasTarget(p, a.in.Target) {
			steps = append(steps, step{target: a.in.Target, project: p})
		}
	}
	return steps
}

// scopedModules groups the scoped paths by module and finds the project rooted at each.
// A module no project is rooted at leaves nobody to run its narrowed tests, so the
// change is full.
func (a *assessment) scopedModules(rep *types.RiskReport, verdicts []verdict) map[string]*scopedModule {
	byModule := map[string]*scopedModule{}
	var order []string
	for _, v := range verdicts {
		if v.tier != types.RiskScoped || v.hit == nil {
			continue
		}
		sm := byModule[v.hit.Module]
		if sm == nil {
			sm = &scopedModule{placed: v.placed, module: v.hit.Module, project: a.projects[v.hit.Module]}
			byModule[v.hit.Module] = sm
			order = append(order, v.hit.Module)
		}
		if v.hit.TestOnly {
			sm.testOnly = appendNew(sm.testOnly, v.hit.Package)
		} else {
			sm.changed = appendNew(sm.changed, v.hit.Package)
		}
	}
	byProject := map[string]*scopedModule{}
	for _, mod := range order {
		sm := byModule[mod]
		if sm.project == nil {
			rep.Tier = types.RiskFull
			rep.Evidence = append(rep.Evidence, types.RiskEvidence{Tier: types.RiskFull,
				Why: "module " + mod + " is no project's root, so no project can run its narrowed tests"})
			continue
		}
		byProject[sm.project.Path] = sm
	}
	return byProject
}

// mechanicalSteps are the drift check and lint for one project, generate left out where
// lint's own chain already runs it. A project with neither runs the invoked target.
func (a *assessment) mechanicalSteps(project string) []step {
	var out []step
	hasLint := a.hasTarget(project, "lint")
	if a.hasTarget(project, "generate") && (!hasLint || !slices.Contains(chainClosure(a.projects[project], "lint"), "generate")) {
		out = append(out, step{target: "generate", project: project})
	}
	if hasLint {
		out = append(out, step{target: "lint", project: project})
	}
	if len(out) == 0 && a.hasTarget(project, a.in.Target) {
		out = append(out, step{target: a.in.Target, project: project})
	}
	return out
}

// scopedSteps expands the invoked target's chain for the project whose module changed.
// The test target, when its body runs the placement's Narrows op, runs with that op's
// packages narrowed to the closure, unless a changed prose path is its input, which
// needs it whole; every other member runs as declared. A member whose declared inputs
// the change does not touch is skipped, since its cache key would not move. narrowed is
// false when there was no test target to narrow.
func (a *assessment) scopedSteps(sm *scopedModule, readers []reader) (out []step, skipped []string, narrowed bool) {
	p := sm.project
	target := types.Normalize(a.in.Target)
	members := p.TargetChains[target]
	if target == "test" {
		members = []types.ChainStep{{Target: target}}
	}
	covered := map[string]bool{}
	add := func(t string) {
		out = append(out, step{target: t, project: p.Path})
		for _, c := range chainClosure(p, t) {
			covered[c] = true
		}
	}
	local := func(s types.ChainStep) bool { return s.Project == "" || s.Project == p.Path }
	for _, m := range members {
		t := types.Normalize(m.Target)
		switch {
		case !local(m):
			out = append(out, step{target: t, project: m.Project})
			continue
		case covered[t]:
			continue
		case slices.Contains(readers, reader{p.Path, t}):
			add(t)
			narrowed = narrowed || t == "test"
			continue
		case !a.reaches(p, t):
			skipped = append(skipped, t)
			continue
		case t != "test" || !runsOp(p, t, sm.placed.Narrows):
			add(t)
			continue
		}
		narrowed = true
		out = append(out, step{target: t, project: p.Path, op: sm.placed.Narrows, packages: sm.placed.Closure(sm.changed, sm.testOnly)})
		for _, c := range chainClosure(p, t) {
			covered[c] = true
		}
	}
	return out, skipped, narrowed
}

// runsOp reports whether target's body invokes op, spelled `spell::op`.
func runsOp(p *types.Project, target, op string) bool {
	spell, name, ok := strings.Cut(op, "::")
	if !ok {
		return false
	}
	return slices.ContainsFunc(p.TargetSpellOps[target], func(u types.TargetSpellUse) bool {
		return u.Spell == spell && slices.Contains(u.Ops, name)
	})
}

// reaches reports whether a changed path is among target's declared inputs. A target
// that declares none keys on the project's sources, so it always runs.
func (a *assessment) reaches(p *types.Project, target string) bool {
	if len(p.TargetInputs[target]) == 0 {
		return true
	}
	for _, c := range a.in.Delta.Paths {
		for _, claim := range a.entries[c.Path].Claims {
			if claim.Role == "source" && claim.Project == p.Path && types.Normalize(claim.Target) == target {
				return true
			}
		}
	}
	return false
}

// chainClosure is target plus every same-project target its chain reaches, normalized
// and sorted. A project without the target yields nothing.
func chainClosure(p *types.Project, target string) []string {
	target = types.Normalize(target)
	if p == nil {
		return nil
	}
	if _, ok := p.TargetChains[target]; !ok && !slices.Contains(p.MagusfileTargets, target) {
		return nil
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(t string) {
		if seen[t] {
			return
		}
		seen[t] = true
		for _, s := range p.TargetChains[t] {
			if s.Project == "" || s.Project == p.Path {
				walk(types.Normalize(s.Target))
			}
		}
	}
	walk(target)
	return slices.Sorted(maps.Keys(seen))
}

// render merges steps that run the same target into one command over their projects,
// in first-seen order, and spells each as the argv a person runs. A narrowed step stays
// its own command, since its narrowing applies to its one project.
func render(steps []step) []types.RiskGateStep {
	out := []types.RiskGateStep{}
	index := map[string]int{}
	for _, s := range steps {
		if s.op != "" {
			out = append(out, types.RiskGateStep{Target: s.target, Projects: []string{s.project}, Op: s.op, Packages: s.packages})
			continue
		}
		if i, ok := index[s.target]; ok {
			if !slices.Contains(out[i].Projects, s.project) {
				out[i].Projects = append(out[i].Projects, s.project)
			}
			continue
		}
		index[s.target] = len(out)
		out = append(out, types.RiskGateStep{Target: s.target, Projects: []string{s.project}})
	}
	for i := range out {
		argv := append([]string{"magus", "run", out[i].Target}, out[i].Projects...)
		out[i].Argv = append(argv, "--no-default-charms")
	}
	return out
}

func claimRef(c types.FileClaim) string {
	if c.Target == "" {
		return c.Project
	}
	return c.Project + ":" + c.Target
}

func appendNew(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
