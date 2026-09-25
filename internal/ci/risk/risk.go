// Package risk classifies a change by how much of its gate magus can prove it does
// not need, and builds the reduced gate: `magus affected <target> --risk`.
//
// Every tier below full rests on a proof, never a guess. A file whose effect magus
// cannot bound lands in full, and the change's tier is its highest file's. The
// statistical pruning on top is the one exception to proof, and it is labeled as
// such in every report that uses it: it drops a gate step only on a long clean
// record, and it relies on main's post-merge CI, which always runs the full gate.
package risk

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Inputs is everything Assess reads, as values and functions, so the tier table
// is testable against fixture trees without opening a workspace.
type Inputs struct {
	// Root is the absolute workspace root; Changed paths are relative to it.
	Root string
	// Target is the invoked target, the one a full gate runs.
	Target string
	// Base is the revision base-side content is read at. Empty means there is no
	// base to read, so nothing can be proven comment- or format-only.
	Base string
	// Label is the base as the report names it.
	Label    string
	Changed  []string
	Files    []types.FileEntry
	Affected []types.ImpactProject
	Projects []*types.Project
	Prose    []ci.ProseScope
	Syntax   map[string]spells.CommentSyntax
	At       func(ctx context.Context, rev, path string) (string, error)
	// GoList returns `go list -e -test -json` output for the module at dir; GoList
	// in this package is the real one.
	GoList func(ctx context.Context, dir string) ([]byte, error)
	// History is the run history statistical pruning reads; nil prunes nothing.
	History *forecast.History
	MinRuns int
	Window  time.Duration
	Now     time.Time
}

// GoTestPackagesOp is the go spell op a scoped gate narrows Go tests with: `go
// test` over exactly the packages forwarded after `--`, with no ./... default.
const GoTestPackagesOp = "go::go-test-packages"

var tierRank = map[types.RiskTier]int{
	types.RiskTrivial: 0, types.RiskMechanical: 1, types.RiskScoped: 2, types.RiskFull: 3,
}

type verdict struct {
	tier     types.RiskTier
	why      string
	module   *goModule
	pkg      string
	testOnly bool
}

type assessment struct {
	in       Inputs
	entries  map[string]types.FileEntry
	outputs  map[string]bool
	projects map[string]*types.Project
	genInput []genGlob
	modules  map[string]*goModule
	modErr   map[string]string
}

// genGlob is one input a generate target declares explicitly with ctx.readsFiles.
type genGlob struct {
	glob, project, target string
}

// Assess classifies every changed path, takes the highest tier, and builds the
// gate that tier needs, pruned by History where the record allows. An error means
// the inputs could not be read at all; an unreadable file only raises its tier.
func Assess(ctx context.Context, in Inputs) (types.RiskReport, error) {
	a := &assessment{
		in:       in,
		entries:  map[string]types.FileEntry{},
		outputs:  map[string]bool{},
		projects: map[string]*types.Project{},
		modules:  map[string]*goModule{},
		modErr:   map[string]string{},
	}
	for _, p := range in.Projects {
		a.projects[p.Path] = p
	}
	for _, e := range in.Files {
		a.entries[e.Path] = e
		if e.Role == "output" || e.Role == "maintained" {
			a.outputs[e.Path] = true
		}
	}
	a.genInput = generateInputs(in.Projects)

	rep := types.RiskReport{Base: in.Label, Target: in.Target, Tier: types.RiskTrivial}
	for _, p := range in.Affected {
		rep.Affected = append(rep.Affected, p.Path)
	}
	scoped := map[*goModule]*scopedModule{}
	for _, p := range in.Changed {
		v := a.classify(ctx, p)
		ev := types.RiskEvidence{Path: p, Project: a.entries[p].Project, Tier: v.tier, Why: v.why}
		if v.tier == types.RiskScoped {
			sm := scoped[v.module]
			if sm == nil {
				sm = &scopedModule{mod: v.module, changed: map[string]bool{}, testOnly: map[string]bool{}}
				scoped[v.module] = sm
			}
			one := map[string]bool{v.pkg: true}
			if v.testOnly {
				sm.testOnly[v.pkg] = true
				ev.Packages = v.module.closure(nil, one)
			} else {
				sm.changed[v.pkg] = true
				ev.Packages = v.module.closure(one, nil)
			}
		}
		rep.Evidence = append(rep.Evidence, ev)
		if tierRank[v.tier] > tierRank[rep.Tier] {
			rep.Tier = v.tier
		}
	}
	if rep.Tier == types.RiskScoped {
		for _, sm := range scoped {
			proj := a.projectAtDir(sm.mod.dir)
			if proj == nil {
				rep.Tier = types.RiskFull
				rep.Evidence = append(rep.Evidence, types.RiskEvidence{Tier: types.RiskFull,
					Why: "Go module " + a.rel(sm.mod.dir) + " is no project's root, so no project can run its narrowed tests"})
				continue
			}
			sm.project = proj
		}
	}
	a.gate(&rep, scoped)
	return rep, nil
}

type scopedModule struct {
	mod      *goModule
	project  *types.Project
	changed  map[string]bool
	testOnly map[string]bool
}

func (a *assessment) classify(ctx context.Context, p string) verdict {
	entry := a.entries[p]
	if a.outputs[p] {
		return a.classifyOutput(entry)
	}
	if strings.HasSuffix(p, ".buzz") {
		return verdict{tier: types.RiskFull, why: "Buzz source: magusfiles, spells and tools define the targets themselves, and magus has no reverse-dependency closure for Buzz"}
	}
	ext := strings.ToLower(path.Ext(p))
	if glob, origin, ok := ci.MatchProse(a.in.Prose, p); ok {
		if g, ok := a.generatorInput(p); ok {
			return g
		}
		if v, ok := a.embedded(ctx, p); ok {
			return v
		}
		return verdict{tier: types.RiskTrivial, why: "prose: matches \"" + glob + "\" (" + origin + ")"}
	}
	if ext == ".go" {
		if a.goEquivalent(ctx, p) {
			return verdict{tier: types.RiskMechanical, why: "comment or format only: its comment-free syntax tree and directives equal the base's"}
		}
		if g, ok := a.generatorInput(p); ok {
			return g
		}
		return a.goPackage(ctx, p, entry)
	}
	if syn, ok := a.in.Syntax[ext]; ok && a.commentOnly(ctx, p, syn) {
		return verdict{tier: types.RiskMechanical, why: "comment only: stripping the comments its spell declares leaves the base's bytes"}
	}
	if g, ok := a.generatorInput(p); ok {
		return g
	}
	if v, ok := a.embedded(ctx, p); ok {
		return v
	}
	return verdict{tier: types.RiskFull, why: "magus cannot bound what reads this file"}
}

// classifyOutput trusts a generated file as committed unless something its
// generator reads changed in the same diff; then the drift check has to re-derive it.
func (a *assessment) classifyOutput(entry types.FileEntry) verdict {
	if entry.Role == "maintained" {
		return verdict{tier: types.RiskTrivial, why: "magus maintains it outside any target, and nothing generates it"}
	}
	for _, c := range entry.Claims {
		if c.Role != "output" {
			continue
		}
		gen := c.Project
		if c.Target != "" {
			gen += ":" + c.Target
		}
		if f, ok := a.generatorTouched(c); ok {
			return verdict{tier: types.RiskMechanical, why: "generated by " + gen + ", and " + f + ", which it reads, changed too: the drift check re-derives it"}
		}
		return verdict{tier: types.RiskTrivial, why: "generated by " + gen + ", and nothing it reads changed"}
	}
	return verdict{tier: types.RiskTrivial, why: "a declared output, and nothing generating it changed"}
}

// generatorTouched finds a changed non-output file the generating claim reads: its
// target's ctx.readsFiles when it declares any, else the project-wide sources.
func (a *assessment) generatorTouched(out types.FileClaim) (string, bool) {
	explicit := false
	if proj := a.projects[out.Project]; proj != nil && out.Target != "" {
		explicit = len(proj.TargetInputs[types.Normalize(out.Target)]) > 0
	}
	for _, f := range a.in.Changed {
		if a.outputs[f] {
			continue
		}
		for _, c := range a.entries[f].Claims {
			if c.Role != "source" || c.Project != out.Project {
				continue
			}
			if out.Target == "" || c.Target == out.Target || (!explicit && c.Target == "") {
				return f, true
			}
		}
	}
	return "", false
}

// generatorInput reports a file that a target in some project's generate chain
// reads by explicit declaration: generator code or data, which always gates full.
func (a *assessment) generatorInput(p string) (verdict, bool) {
	for _, g := range a.genInput {
		if ok, _ := doublestar.Match(g.glob, p); ok {
			return verdict{tier: types.RiskFull, why: "generator input: " + g.project + ":" + g.target + " declares \"" + g.glob + "\""}, true
		}
	}
	return verdict{}, false
}

// generateInputs collects the explicit inputs of every target the canonical
// generate target reaches, in every project.
func generateInputs(projects []*types.Project) []genGlob {
	var out []genGlob
	for _, p := range projects {
		for _, t := range chainClosure(p, "generate") {
			for _, ref := range p.TargetInputs[t] {
				owner := cmp.Or(ref.Project, p.Path)
				out = append(out, genGlob{glob: joinGlob(owner, ref.Glob), project: p.Path, target: t})
			}
		}
	}
	return out
}

// chainClosure is target plus every same-project target its ctx.needs chain
// reaches, normalized. A project without the target yields nothing.
func chainClosure(p *types.Project, target string) []string {
	target = types.Normalize(target)
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
	return slices.Sorted(func(yield func(string) bool) {
		for t := range seen {
			if !yield(t) {
				return
			}
		}
	})
}

func joinGlob(project, glob string) string {
	if project == "" || project == "." {
		return glob
	}
	return project + "/" + glob
}

func (a *assessment) readBoth(ctx context.Context, p string) (old, cur string, ok bool) {
	if a.in.Base == "" || a.in.At == nil {
		return "", "", false
	}
	o, err := a.in.At(ctx, a.in.Base, p)
	if err != nil {
		return "", "", false
	}
	b, err := os.ReadFile(filepath.Join(a.in.Root, filepath.FromSlash(p)))
	if err != nil {
		return "", "", false
	}
	return o, string(b), true
}

func (a *assessment) goEquivalent(ctx context.Context, p string) bool {
	old, cur, ok := a.readBoth(ctx, p)
	if !ok {
		return false
	}
	return goEquivalent(old, cur, a.in.Syntax[".go"].Directives)
}

func (a *assessment) commentOnly(ctx context.Context, p string, syn spells.CommentSyntax) bool {
	old, cur, ok := a.readBoth(ctx, p)
	return ok && ci.CommentOnlyDeclared(old, cur, syn)
}

// module loads the Go module containing the workspace-relative path p, if any.
func (a *assessment) module(ctx context.Context, p string) (*goModule, string) {
	dir := filepath.Dir(filepath.Join(a.in.Root, filepath.FromSlash(p)))
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		if dir == a.in.Root || !strings.HasPrefix(dir, a.in.Root) {
			return nil, ""
		}
		dir = filepath.Dir(dir)
	}
	if m, ok := a.modules[dir]; ok {
		return m, a.modErr[dir]
	}
	if a.in.GoList == nil {
		a.modules[dir], a.modErr[dir] = nil, "no go list runner"
		return nil, a.modErr[dir]
	}
	out, err := a.in.GoList(ctx, dir)
	var pkgs []goPackage
	if err == nil {
		pkgs, err = decodeGoList(out)
	}
	if err != nil {
		a.modules[dir], a.modErr[dir] = nil, err.Error()
		return nil, a.modErr[dir]
	}
	a.modules[dir] = newGoModule(dir, pkgs)
	return a.modules[dir], ""
}

// embedded reports a non-Go file some package compiles in with go:embed.
func (a *assessment) embedded(ctx context.Context, p string) (verdict, bool) {
	m, _ := a.module(ctx, p)
	if m == nil {
		return verdict{}, false
	}
	hit, ok := m.files[filepath.Join(a.in.Root, filepath.FromSlash(p))]
	if !ok || m.poison != "" {
		return verdict{}, false
	}
	if gen, ok := m.generators[hit.pkg]; ok {
		return verdict{tier: types.RiskFull, why: "embedded by " + hit.pkg + ", which only go:generate program " + gen + " reaches"}, true
	}
	return verdict{tier: types.RiskScoped, why: "embedded by " + hit.pkg + " (go:embed)", module: m, pkg: hit.pkg, testOnly: hit.role == goTestOnly}, true
}

func (a *assessment) goPackage(ctx context.Context, p string, entry types.FileEntry) verdict {
	m, errMsg := a.module(ctx, p)
	if m == nil {
		if errMsg != "" {
			return verdict{tier: types.RiskFull, why: "go list failed, so reverse dependencies are unknown: " + errMsg}
		}
		return verdict{tier: types.RiskFull, why: "in no Go module"}
	}
	if m.poison != "" {
		return verdict{tier: types.RiskFull, why: "go list reported a load error, so reverse dependencies cannot be trusted: " + m.poison}
	}
	abs := filepath.Join(a.in.Root, filepath.FromSlash(p))
	hit, ok := m.files[abs]
	if !ok && !entry.Exists {
		for path, pkg := range m.primary {
			if pkg.Dir == filepath.Dir(abs) {
				hit, ok = goFileHit{pkg: path, role: goCompiled}, true
				break
			}
		}
		if !ok {
			return verdict{tier: types.RiskFull, why: "deleted with its whole package, so no package is left to test its former importers against"}
		}
	}
	switch {
	case !ok:
		return verdict{tier: types.RiskFull, why: "compiled into no package of module " + a.rel(m.dir) + " (testdata, or a directory go does not build)"}
	case hit.role == goIgnored:
		return verdict{tier: types.RiskFull, why: "excluded by build constraints on this platform, so its tests cannot run here"}
	}
	if gen, ok := m.generators[hit.pkg]; ok {
		return verdict{tier: types.RiskFull, why: "generator code: " + hit.pkg + " is reachable only from go:generate program " + gen}
	}
	why := "Go package " + hit.pkg + ": its tests and every package importing it"
	if hit.role == goTestOnly {
		why = "test file of Go package " + hit.pkg + ": only that package's tests compile it"
	}
	return verdict{tier: types.RiskScoped, why: why, module: m, pkg: hit.pkg, testOnly: hit.role == goTestOnly}
}

func (a *assessment) rel(dir string) string {
	r, err := filepath.Rel(a.in.Root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(r)
}

func (a *assessment) projectAtDir(dir string) *types.Project {
	for _, p := range a.in.Projects {
		if filepath.Clean(p.Dir) == filepath.Clean(dir) {
			return p
		}
	}
	return nil
}

// step is one gate command before it is rendered, with the history key its
// statistics are read under.
type step struct {
	target   string
	project  string
	packages []string
	riskKey  string
}

func (a *assessment) gate(rep *types.RiskReport, scoped map[*goModule]*scopedModule) {
	var steps []step
	switch rep.Tier {
	case types.RiskTrivial:
		rep.Gate = []types.RiskGateStep{}
		return
	case types.RiskMechanical:
		for _, ap := range a.in.Affected {
			steps = append(steps, a.mechanicalSteps(ap)...)
		}
	case types.RiskScoped:
		byProject := map[string]*scopedModule{}
		for _, sm := range scoped {
			if sm.project != nil {
				byProject[sm.project.Path] = sm
			}
		}
		for _, ap := range a.in.Affected {
			if sm := byProject[ap.Path]; sm != nil {
				narrowed, skipped, ok := a.scopedSteps(sm)
				if ok {
					steps = append(steps, narrowed...)
					for _, t := range skipped {
						rep.Evidence = append(rep.Evidence, types.RiskEvidence{Project: ap.Path, Target: t, Tier: types.RiskScoped,
							Why: "skipped: the change touches none of the files its ctx.readsFiles declares, so its cache key would not move"})
					}
					continue
				}
				rep.Evidence = append(rep.Evidence, types.RiskEvidence{Project: ap.Path, Target: a.in.Target, Tier: types.RiskScoped,
					Why: "no test target in " + a.in.Target + "'s chain runs the go spell's go-test op, so its tests cannot be narrowed and it runs whole"})
			}
			if a.hasTarget(ap.Path, a.in.Target) {
				steps = append(steps, step{target: a.in.Target, project: ap.Path, riskKey: a.in.Target})
			}
		}
	default:
		for _, ap := range a.in.Affected {
			if a.hasTarget(ap.Path, a.in.Target) {
				steps = append(steps, step{target: a.in.Target, project: ap.Path, riskKey: a.in.Target})
			}
		}
	}
	if rep.Tier == types.RiskScoped || rep.Tier == types.RiskFull {
		steps = a.prune(rep, steps)
	}
	rep.Gate = render(steps)
}

// mechanicalSteps are the drift check and lint for one project, generate left out
// where lint's own chain already runs it.
func (a *assessment) mechanicalSteps(ap types.ImpactProject) []step {
	var out []step
	hasLint := a.hasTarget(ap.Path, "lint")
	if a.hasTarget(ap.Path, "generate") && (!hasLint || !slices.Contains(chainClosure(a.projects[ap.Path], "lint"), "generate")) {
		out = append(out, step{target: "generate", project: ap.Path})
	}
	if hasLint {
		out = append(out, step{target: "lint", project: ap.Path})
	}
	return out
}

// hasTarget reports whether the project's magusfile exports target or one of its
// spells serves it.
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

// scopedSteps expands the invoked target's chain for the project whose Go module
// changed. The canonical test target, when its body runs the go spell's go-test op,
// is replaced by that op narrowed to the package closure, followed by its own chain;
// every other member runs as declared. A member whose ctx.readsFiles the change
// does not touch is skipped, because its cache key would not move either. narrowed
// is false when there was no test target to narrow.
func (a *assessment) scopedSteps(sm *scopedModule) (out []step, skipped []string, narrowed bool) {
	p := sm.project
	target := types.Normalize(a.in.Target)
	members := p.TargetChains[target]
	if target == "test" {
		members = []types.ChainStep{{Target: target}}
	}
	covered := map[string]bool{}
	add := func(t string) {
		out = append(out, step{target: t, project: p.Path, riskKey: t})
		for _, c := range chainClosure(p, t) {
			covered[c] = true
		}
	}
	local := func(s types.ChainStep) bool { return s.Project == "" || s.Project == p.Path }
	for _, m := range members {
		t := types.Normalize(m.Target)
		switch {
		case !local(m):
			out = append(out, step{target: t, project: m.Project, riskKey: t})
			continue
		case covered[t]:
			continue
		case !a.reaches(p, t):
			skipped = append(skipped, t)
			continue
		case t != "test" || !runsGoTest(p, t):
			add(t)
			continue
		}
		narrowed = true
		covered[t] = true
		out = append(out, step{target: GoTestPackagesOp, project: p.Path, packages: sm.mod.closure(sm.changed, sm.testOnly), riskKey: t})
		for _, s := range p.TargetChains[t] {
			st := types.Normalize(s.Target)
			switch {
			case !local(s) || covered[st]:
			case !a.reaches(p, st):
				skipped = append(skipped, st)
			default:
				add(st)
			}
		}
	}
	return out, skipped, narrowed
}

func runsGoTest(p *types.Project, target string) bool {
	return slices.ContainsFunc(p.TargetSpellOps[target], func(u types.TargetSpellUse) bool {
		return slices.Contains(u.Ops, "go-test")
	})
}

// reaches reports whether a changed file is among target's declared inputs. A
// target with no ctx.readsFiles keys on the project's sources, so it always runs.
func (a *assessment) reaches(p *types.Project, target string) bool {
	if len(p.TargetInputs[target]) == 0 {
		return true
	}
	for _, f := range a.in.Changed {
		for _, c := range a.entries[f].Claims {
			if c.Role == "source" && c.Project == p.Path && types.Normalize(c.Target) == target {
				return true
			}
		}
	}
	return false
}

// prune drops every (project, target) whose recorded runs clear the statistical
// bar, and records each pair's numbers either way.
func (a *assessment) prune(rep *types.RiskReport, steps []step) []step {
	if a.in.History == nil {
		return steps
	}
	window := formatWindow(a.in.Window)
	seen := map[[2]string]bool{}
	var kept []step
	for _, s := range steps {
		key := s.riskKey
		if key == "" {
			key = s.target
		}
		runs, fails := a.record(s.project, key)
		tr := types.TargetRisk{Project: s.project, Target: key, Runs: runs, Failures: fails, Window: window}
		if runs > 0 {
			tr.Rate = float64(fails) / float64(runs)
		}
		tr.Pruned = a.in.MinRuns > 0 && runs >= a.in.MinRuns && fails == 0
		if k := [2]string{s.project, key}; !seen[k] {
			seen[k] = true
			rep.Risk = append(rep.Risk, tr)
		}
		if !tr.Pruned {
			kept = append(kept, s)
			continue
		}
		rep.Evidence = append(rep.Evidence, types.RiskEvidence{Project: s.project, Target: key, Tier: rep.Tier,
			Why: fmt.Sprintf("statistically pruned: %d recorded affected runs in the last %s, none failed (minimum %d); main's post-merge CI still runs it",
				runs, window, a.in.MinRuns)})
	}
	return kept
}

// record counts one (project, target)'s affected outcomes inside the window. A
// magusfile target is read under its own key alone, because the spells serving
// the same name record a no-op pass beside it on every run; a zero-duration
// outcome is such a no-op wherever it appears and verifies nothing.
func (a *assessment) record(project, target string) (runs, fails int) {
	var outcomes []forecast.Outcome
	if targets, ok := a.in.History.Projects[project]; ok {
		if s, ok := targets["magusfile/"+target]; ok {
			outcomes = s.RecentOutcomes
		} else if s, ok := a.in.History.FoldTargetHistories(project, target); ok {
			outcomes = s.RecentOutcomes
		}
	}
	since := a.in.Now.Add(-a.in.Window)
	for _, o := range outcomes {
		if !o.AffectedByDiff || o.DurationMs == 0 || o.At.Before(since) {
			continue
		}
		runs++
		if o.Result != forecast.OutcomePass {
			fails++
		}
	}
	return runs, fails
}

func formatWindow(d time.Duration) string {
	if d%(24*time.Hour) == 0 && d > 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// render merges steps that run the same target into one command over their
// projects, in first-seen order, and spells each as the argv a person runs.
func render(steps []step) []types.RiskGateStep {
	out := []types.RiskGateStep{}
	index := map[string]int{}
	for _, s := range steps {
		if len(s.packages) > 0 {
			out = append(out, types.RiskGateStep{Target: s.target, Projects: []string{s.project}, Packages: s.packages})
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
		argv = append(argv, "--no-default-charms")
		if len(out[i].Packages) > 0 {
			argv = append(append(argv, "--"), out[i].Packages...)
		}
		out[i].Argv = argv
	}
	return out
}
