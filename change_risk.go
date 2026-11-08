package magus

import (
	"github.com/egladman/magus/types/diff"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/project/impact"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// riskDefinition names the formula, so a reader of the answer need not know it.
const riskDefinition = "complexity^2 * (1 - coverage)^3 + complexity, " +
	"coverage being the covered share of the coverable units in the function's lines"

// riskScorer scores touched functions. Each field is a lookup the workspace
// answers, so a test can answer it instead.
type riskScorer struct {
	// read is a workspace-relative file's text.
	read func(path string) (string, bool)
	// claimant is the spell bound to path's project that claims its
	// extension, or nil.
	claimant func(path string) *spells.Spell
	owner    func(path string) string
	// indexed is the complexity an AST-reading indexer counted for a symbol,
	// and whether it counted.
	indexed func(symbol string) (int, bool)
	// report is the coverage report that measures fn, or why there is none.
	report func(fn touchedFunction, cov spells.Coverage) (keptReport, string)
}

// keptReport is a project's coverage profile as its last test run left it.
type keptReport struct {
	project string
	files   []coverage.File
	// writer is the command that wrote the profile, "" when unknown.
	writer string
}

// source names the profile for a reason: its command, or its project.
func (r keptReport) source() string {
	if r.writer != "" {
		return fmt.Sprintf("the profile `%s` writes", r.writer)
	}
	return "the coverage profile of " + r.project
}

// scoreAll scores each function, or reports it as a gap. A gap is never a
// pass and never a score of 0.
func (s riskScorer) scoreAll(fns []touchedFunction) types.ChangeRisk {
	out := types.ChangeRisk{Definition: riskDefinition}
	for _, fn := range fns {
		s.score(&out, fn)
	}
	return out
}

func (s riskScorer) score(out *types.ChangeRisk, fn touchedFunction) {
	f, gap, ok := s.scoreOne(fn)
	if !ok {
		out.Gaps = append(out.Gaps, gap)
		return
	}
	out.Functions = append(out.Functions, f)
}

// scoreOne is fn's score, or false and the gap that says why it has none.
func (s riskScorer) scoreOne(fn touchedFunction) (types.ChangeRiskFunction, types.FunctionGap, bool) {
	c, reason := s.complexity(fn)
	if reason != "" {
		return types.ChangeRiskFunction{}, functionGap(fn, 0, reason), false
	}
	covered, coverable, reason := s.coverage(fn)
	if reason != "" {
		return types.ChangeRiskFunction{}, functionGap(fn, c, reason), false
	}
	return riskFunction(fn, c, covered, coverable), types.FunctionGap{}, true
}

func (s riskScorer) complexity(fn touchedFunction) (int, string) {
	if fn.EndLine == 0 {
		return 0, fmt.Sprintf("the %s indexer records no body extent", fn.Language)
	}
	sp := s.claimant(fn.Path)
	if sp == nil {
		return 0, fmt.Sprintf("no spell bound to %s claims %s files", s.owner(fn.Path), path.Ext(fn.Path))
	}
	if syn := sp.Syntax(); syn != nil && syn.Decisions != nil {
		return s.countTokens(fn, sp.Name(), *syn)
	}
	return s.indexerDecisions(fn, sp.Name())
}

func (s riskScorer) countTokens(fn touchedFunction, spellName string, syn spells.Syntax) (int, string) {
	src, ok := s.read(fn.Path)
	body, inRange := bodyLines(src, fn.Line, fn.EndLine)
	if !ok || !inRange {
		return 0, fmt.Sprintf("%s lines %d-%d could not be read", fn.Path, fn.Line, fn.EndLine)
	}
	n, err := syn.Complexity(body)
	if err != nil {
		return 0, fmt.Sprintf("spell %s: %s", spellName, err)
	}
	return n, ""
}

// indexerDecisions stands in for a spell whose syntax declares no decisions,
// such as bash, whose indexer counts them from the AST.
func (s riskScorer) indexerDecisions(fn touchedFunction, spellName string) (int, string) {
	n, counted := s.indexed(fn.Symbol)
	if !counted {
		return 0, fmt.Sprintf("spell %s declares no decisions and its indexer counted none", spellName)
	}
	return n, ""
}

func (s riskScorer) coverage(fn touchedFunction) (covered, coverable int, reason string) {
	sp := s.claimant(fn.Path)
	if sp == nil {
		return 0, 0, fmt.Sprintf("no spell bound to %s claims %s files", s.owner(fn.Path), path.Ext(fn.Path))
	}
	cov := sp.Coverage()
	if cov == nil {
		return 0, 0, fmt.Sprintf("spell %s declares no coverage (mgs_getCoverage)", sp.Name())
	}
	report, reason := s.report(fn, *cov)
	if reason != "" {
		return 0, 0, reason
	}
	return report.span(fn)
}

// span tallies fn's lines.
func (r keptReport) span(fn touchedFunction) (covered, coverable int, reason string) {
	i := slices.IndexFunc(r.files, func(f coverage.File) bool { return f.Path == fn.Path })
	if i < 0 {
		return 0, 0, fmt.Sprintf("%s instruments no line of %s: no test it runs loads that file, so %s is unmeasured",
			r.source(), fn.Path, fn.Path)
	}
	covered, coverable = r.files[i].Span(fn.Line, fn.EndLine)
	if coverable == 0 {
		return 0, 0, fmt.Sprintf("%s instruments no line in %s lines %d-%d", r.source(), fn.Path, fn.Line, fn.EndLine)
	}
	return covered, coverable, ""
}

// bodyLines is lines start..end of src, 1-based and inclusive.
func bodyLines(src string, start, end int) (string, bool) {
	lines := strings.Split(src, "\n")
	if start < 1 || end > len(lines) || start > end {
		return "", false
	}
	return strings.Join(lines[start-1:end], "\n"), true
}

func riskFunction(fn touchedFunction, complexity, covered, coverable int) types.ChangeRiskFunction {
	f := types.ChangeRiskFunction{
		Symbol: fn.Symbol, Label: fn.Label, Path: fn.Path, Language: fn.Language, Digest: fn.Digest,
		Line: fn.Line, EndLine: fn.EndLine, Complexity: complexity, Covered: covered, Coverable: coverable,
	}
	f.Score = types.ChangeRiskScore(complexity, f.Coverage())
	return f
}

func functionGap(fn touchedFunction, complexity int, reason string) types.FunctionGap {
	return types.FunctionGap{
		Symbol: fn.Symbol, Label: fn.Label, Path: fn.Path, Language: fn.Language,
		Line: fn.Line, Complexity: complexity, Reason: reason,
	}
}

// ChangeRisk scores the functions touched since base: what HEAD added since
// it forked from base, and the working tree's edits on top. Each is measured
// again as the merge base held it, and the changed files' covered lines are
// compared. base "" scores every function in the workspace, against no base.
// Coverage is the profile each owning spell declares. At head the target
// writing it is freshened through the cache. At base it is the profile main's
// CI cached for that target at the merge base. None is an error naming the
// base and the command; magus never runs the base itself.
func (m *Magus) ChangeRisk(ctx context.Context, base string) (types.ChangeRisk, error) {
	start := time.Now()
	set, err := m.touchedSince(ctx, base)
	if err != nil {
		return types.ChangeRisk{}, stopped(ctx, start, "finding the touched functions", err)
	}
	meter, err := m.newRiskMeter(ctx, set, base)
	if err != nil {
		return types.ChangeRisk{}, stopped(ctx, start, "resolving the base", err)
	}
	defer meter.done()
	out, err := meter.measure(ctx, set.fns)
	if err != nil {
		return types.ChangeRisk{}, err
	}
	out.Base = base
	return out, nil
}

// stopped names what change risk was doing when ctx ended, and how long it
// ran. A stop never reads as a bare "context canceled". While ctx is live,
// err passes through.
func stopped(ctx context.Context, start time.Time, doing string, err error) error {
	if err == nil || ctx.Err() == nil {
		return err
	}
	return fmt.Errorf("change risk: stopped %s after %s: %w", doing, time.Since(start).Round(time.Second), context.Cause(ctx))
}

// coverageSource is where one project's coverage profile comes from: the
// spell that declares it and the project target that writes it.
type coverageSource struct {
	project  string
	spell    string
	target   string
	coverage spells.Coverage
}

func (c coverageSource) key() string { return fmt.Sprintf("%s\x00%s", c.project, c.coverage.Profile) }

func (c coverageSource) command() string { return fmt.Sprintf("magus run %s %s", c.target, c.project) }

// profileRead is what one source's profile held, or why there is none.
type profileRead struct {
	files  []coverage.File
	reason string
}

// riskMeter measures change risk. Each func field is a lookup or a run the
// workspace answers, so a test can answer it instead.
type riskMeter struct {
	// scorer answers complexity at head; measure sets its report.
	scorer riskScorer
	// source is where a file's coverage comes from, or why it has none.
	source func(path string) (coverageSource, string)
	// covers reports whether a changed file's spell declares coverage at all.
	covers func(path string) bool
	// head freshens a source's target and reads its profile in the tree.
	head func(ctx context.Context, c coverageSource) (profileRead, error)
	// atBase reads a source's profile at commit.
	atBase func(ctx context.Context, c coverageSource) (profileRead, error)
	// readAt is a workspace file's text at commit.
	readAt func(path string) (string, bool)
	// done removes what atBase left behind.
	done func()
	// commit is the merge base the change is judged against, "" for none.
	commit  string
	since   sinceBase
	changed []string
}

func (m *Magus) newRiskMeter(ctx context.Context, set touchedSet, base string) (riskMeter, error) {
	claimant, owner := m.spellClaiming(), m.projectOwner()
	r := riskMeter{
		scorer: m.riskScorer(set.defs),
		source: func(p string) (coverageSource, string) { return m.coverageSource(owner(p), p, claimant(p)) },
		covers: func(p string) bool {
			sp := claimant(p)
			return sp != nil && sp.Coverage() != nil
		},
		head:  m.headProfile,
		done:  func() {},
		since: set.since, changed: set.changed,
	}
	if base == "" {
		return r, nil
	}
	v, commit, err := m.conformanceBaseCommit(ctx, base)
	if err != nil {
		return riskMeter{}, fmt.Errorf("change risk: %w", err)
	}
	profiles, err := m.newBaseProfiles(ctx, commit)
	if err != nil {
		return riskMeter{}, fmt.Errorf("change risk: %w", err)
	}
	r.commit, r.atBase, r.done = commit, profiles.read, profiles.remove
	r.readAt = func(p string) (string, bool) {
		text, err := v.ReadFileAt(ctx, m.ws.Root, commit, p)
		return text, err == nil
	}
	return r, nil
}

// coverageSource is where name, a file of project that sp claims, gets its
// coverage: the profile sp declares and the target declaring it an output.
func (m *Magus) coverageSource(project, name string, sp *spells.Spell) (coverageSource, string) {
	if sp == nil {
		return coverageSource{}, fmt.Sprintf("no spell bound to %s claims %s files", project, path.Ext(name))
	}
	cov := sp.Coverage()
	if cov == nil {
		return coverageSource{}, fmt.Sprintf("spell %s declares no coverage (mgs_getCoverage)", sp.Name())
	}
	p := m.Get(project)
	if p == nil {
		return coverageSource{}, fmt.Sprintf("project %s does not resolve, so no target writes its coverage", project)
	}
	target, reason := profileTarget(p, cov.Profile)
	if reason != "" {
		return coverageSource{}, reason
	}
	return coverageSource{project: project, spell: sp.Name(), target: target, coverage: *cov}, ""
}

// profileTarget is the target of p declaring profile among its outputs. None,
// or several, is a reason: magus never guesses which run wrote it.
func profileTarget(p *types.Project, profile string) (string, string) {
	rooted := path.Join(p.Path, profile)
	var found []string
	for _, name := range slices.Sorted(maps.Keys(p.TargetOutputs)) {
		if slices.ContainsFunc(targetOutputGlobs(p, name), func(g types.Glob) bool { return g.Match(rooted) }) {
			found = append(found, name)
		}
	}
	switch len(found) {
	case 1:
		return found[0], ""
	case 0:
		return "", fmt.Sprintf("no target of %s declares its coverage profile %s as an output", p.Path, profile)
	}
	return "", fmt.Sprintf("targets %s of %s each declare its coverage profile %s, so which run wrote it is unknown",
		strings.Join(found, ", "), p.Path, profile)
}

// headProfile freshens c's target and reads the profile it left. A target
// that fails or cannot run is a reason; a cancelled one is an error.
func (m *Magus) headProfile(ctx context.Context, c coverageSource) (profileRead, error) {
	fresh := m.freshProfiles(ctx)
	if !fresh.vouches(c) {
		if err := m.freshenProfile(ctx, c); err != nil {
			if ctx.Err() != nil {
				return profileRead{}, fmt.Errorf("change risk: freshening `%s`: %w", c.command(), context.Cause(ctx))
			}
			return profileRead{reason: fmt.Sprintf("`%s` left no current profile: %s", c.command(), err)}, nil
		}
		if err := fresh.record(c); err != nil {
			return profileRead{}, fmt.Errorf("change risk: %w", err)
		}
	}
	kept, reason := m.readCoverageReport(c.project, c.coverage)
	return profileRead{files: kept.files, reason: reason}, nil
}

// freshenProfile leaves c's profile as a current run of c's target wrote it.
// Inside a target body it runs through that run's scheduler, as
// buildSymbolIndex does, since a nested run is refused MGS3007.
func (m *Magus) freshenProfile(ctx context.Context, c coverageSource) error {
	p := m.Get(c.project)
	cc := m.freshnessCache(ctx)
	if p == nil || cc == nil {
		return fmt.Errorf("the cache would not open, so nothing vouches for %s", c.coverage.Profile)
	}
	step, err := m.targetStep(project.WithExtraArgs(ctx, nil), p, c.target, nil, nil)
	if err != nil {
		return err
	}
	probe := step
	probe.VerifyOutputs = true
	if current, err := cc.IsCached(ctx, probe); err == nil && current {
		return nil
	}
	if ran, err := interp.CrossDispatchFromContext(ctx).DispatchStep(ctx, p, step); ran {
		return err
	}
	return m.runTargets(ctx, []types.Target{{Path: p.Path, Name: c.target}}, run{})
}

// noBase is the base reason of every function when no base was named.
const noBase = "no base was named"

// measure scores fns at head and, with a commit, at base, then compares the
// changed files' lines.
func (r riskMeter) measure(ctx context.Context, fns []touchedFunction) (types.ChangeRisk, error) {
	plan := r.plan(fns)
	head, base, err := r.readSides(ctx, plan.sources)
	if err != nil {
		return types.ChangeRisk{}, err
	}
	scorer := r.scorer
	scorer.report = plan.lookup(head)
	out := scorer.scoreAll(fns)
	if r.commit == "" {
		for i := range out.Functions {
			out.Functions[i].BaseReason = noBase
		}
		return out, nil
	}
	based := r.scorer
	based.read = func(p string) (string, bool) { return r.readAt(r.since.basePath(p)) }
	based.indexed = noIndex
	based.report = r.since.atHeadPath(plan.lookup(base))
	r.judgeBase(out.Functions, fns, based)
	out.Lines = r.lines(plan, head, base)
	return out, nil
}

// readSides reads each source's profile at head and, with a commit, at
// base, both sides at once.
func (r riskMeter) readSides(ctx context.Context, sources []coverageSource) (head, base map[string]profileRead, err error) {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		head, err = readEach(gctx, sources, r.head)
		return err
	})
	if r.commit != "" {
		g.Go(func() error {
			var err error
			base, err = readEach(gctx, sources, r.atBase)
			return err
		})
	}
	return head, base, g.Wait()
}

func readEach(ctx context.Context, sources []coverageSource, read func(context.Context, coverageSource) (profileRead, error)) (map[string]profileRead, error) {
	out := make(map[string]profileRead, len(sources))
	for _, c := range sources {
		got, err := read(ctx, c)
		if err != nil {
			return nil, err
		}
		out[c.key()] = got
	}
	return out, nil
}

type reportLookup func(touchedFunction, spells.Coverage) (keptReport, string)

// atHeadPath answers a base report under head paths. A renamed file is
// entered there by its old path, and a span looks it up by its new one.
func (s sinceBase) atHeadPath(lookup reportLookup) reportLookup {
	return func(fn touchedFunction, cov spells.Coverage) (keptReport, string) {
		kept, reason := lookup(fn, cov)
		old := s.basePath(fn.Path)
		if reason != "" || old == fn.Path {
			return kept, reason
		}
		kept.files = slices.Clone(kept.files)
		for i := range kept.files {
			if kept.files[i].Path == old {
				kept.files[i].Path = fn.Path
			}
		}
		return kept, ""
	}
}

// noIndex stands in for the base's indexer, which magus never ran.
func noIndex(string) (int, bool) { return 0, false }

// judgeBase measures each scored function again over its span at base.
func (r riskMeter) judgeBase(scored []types.ChangeRiskFunction, fns []touchedFunction, based riskScorer) {
	byID := map[string]touchedFunction{}
	for _, fn := range fns {
		byID[fn.Symbol] = fn
	}
	for i := range scored {
		f := &scored[i]
		fn := byID[f.Symbol]
		lo, hi, ok := r.since.baseSpan(fn)
		if !ok {
			f.BaseReason = newSinceBase
			continue
		}
		fn.Line, fn.EndLine = lo, hi
		b, gap, ok := based.scoreOne(fn)
		if !ok {
			f.BaseReason = "its base is unmeasured: " + gap.Reason
			continue
		}
		f.BaseScore, f.BaseComplexity, f.BaseCovered, f.BaseCoverable = b.Score, b.Complexity, b.Covered, b.Coverable
	}
}

// sourcePlan is the profiles a measure reads: one per project and profile.
type sourcePlan struct {
	byPath  map[string]coverageSource
	reason  map[string]string
	sources []coverageSource
}

func (r riskMeter) plan(fns []touchedFunction) sourcePlan {
	p := sourcePlan{byPath: map[string]coverageSource{}, reason: map[string]string{}}
	seen := map[string]bool{}
	add := func(name string) {
		if _, done := p.byPath[name]; done || p.reason[name] != "" {
			return
		}
		c, reason := r.source(name)
		if reason != "" {
			p.reason[name] = reason
			return
		}
		p.byPath[name] = c
		if !seen[c.key()] {
			seen[c.key()] = true
			p.sources = append(p.sources, c)
		}
	}
	for _, fn := range fns {
		add(fn.Path)
	}
	for _, name := range r.changed {
		if r.covers(name) {
			add(name)
		}
	}
	return p
}

// lookup answers a scorer's report from reads.
func (p sourcePlan) lookup(reads map[string]profileRead) reportLookup {
	return func(fn touchedFunction, _ spells.Coverage) (keptReport, string) {
		if reason := p.reason[fn.Path]; reason != "" {
			return keptReport{}, reason
		}
		c := p.byPath[fn.Path]
		got := reads[c.key()]
		if got.reason != "" {
			return keptReport{}, got.reason
		}
		return keptReport{project: c.project, files: got.files, writer: c.command()}, ""
	}
}

// lines compares each changed file's covered lines at head with those at
// base. A line removed since base is never compared.
func (r riskMeter) lines(plan sourcePlan, head, base map[string]profileRead) []types.Finding {
	var out []types.Finding
	for _, name := range r.changed {
		c, ok := plan.byPath[name]
		if !ok {
			out = append(out, uncomparedLines(name, plan.reason[name])...)
			continue
		}
		out = append(out, r.fileLines(name, head[c.key()], base[c.key()])...)
	}
	return out
}

// uncomparedLines is the finding for a changed file with no coverage source,
// or none when the plan never looked at the file.
func uncomparedLines(name, reason string) []types.Finding {
	if reason == "" {
		return nil
	}
	return []types.Finding{{Path: name, Line: 1,
		Message: fmt.Sprintf("the coverage of %s was not compared: %s", name, reason),
		Fix:     "Declare the coverage profile its spell names as an output of the target that writes it."}}
}

func (r riskMeter) fileLines(name string, head, base profileRead) []types.Finding {
	for _, run := range []struct{ side, reason string }{{"", head.reason}, {"at base ", base.reason}} {
		if run.reason != "" {
			return []types.Finding{{Path: name, Line: 1,
				Message: fmt.Sprintf("the coverage of %s %swas not measured: %s", name, run.side, run.reason),
				Fix:     "Make its project's tests pass, so its covered lines can be compared."}}
		}
	}
	now := fileNamed(head.files, name).Lines()
	var lost, bare []int
	for line, ran := range fileNamed(base.files, r.since.basePath(name)).Lines() {
		at, kept := r.since.headLine(name, line)
		if ran && kept && uncovered(now, at) {
			lost = append(lost, at)
		}
	}
	for _, line := range r.since.lines[name] {
		if uncovered(now, line) {
			bare = append(bare, line)
		}
	}
	out := lineFindings(name, lost, "was covered at base and no test of its package covers it now",
		"Restore the test that covered it, or delete the code no test needs.")
	return append(out, lineFindings(name, bare, "was added and no test of its package covers it",
		"Cover the added lines with a test.")...)
}

func uncovered(lines map[int]bool, line int) bool {
	ran, coverable := lines[line]
	return coverable && !ran
}

// lineFindings is one finding per run of consecutive lines.
func lineFindings(name string, lines []int, what, fix string) []types.Finding {
	slices.Sort(lines)
	lines = slices.Compact(lines)
	var out []types.Finding
	for i := 0; i < len(lines); {
		j := i
		for j+1 < len(lines) && lines[j+1] == lines[j]+1 {
			j++
		}
		span := strconv.Itoa(lines[i])
		if j > i {
			span = fmt.Sprintf("%d-%d", lines[i], lines[j])
		}
		out = append(out, types.Finding{Path: name, Line: lines[i], Message: fmt.Sprintf("%s:%s %s", name, span, what), Fix: fix})
		i = j + 1
	}
	return out
}

// riskGraph is the head graph with every symbol. Its indexes are brought
// current first, so each span describes the tree being scored.
func (m *Magus) riskGraph(ctx context.Context) (*knowledge.Graph, error) {
	projects := make([]string, 0, len(m.ws.All()))
	for _, p := range m.ws.All() {
		projects = append(projects, p.Path)
	}
	if err := m.freshenSymbolIndexes(ctx, projects, nil); err != nil {
		return nil, fmt.Errorf("change risk: %w", err)
	}
	return m.knowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
}

// touchedSet is what a change touched since base. since and changed, the
// hand-written files it changed, are empty when no base was named.
type touchedSet struct {
	defs    map[string]symbolDef
	fns     []touchedFunction
	since   sinceBase
	changed []string
}

// touchedSince is the functions a change touched since base, as ChangeRisk
// scores them.
func (m *Magus) touchedSince(ctx context.Context, base string) (touchedSet, error) {
	graph, err := m.riskGraph(ctx)
	if err != nil {
		return touchedSet{}, err
	}
	set := touchedSet{defs: definedSymbols(graph.Nodes())}
	if base == "" {
		set.fns, err = m.touchedIn(ctx, set.defs, everySymbol(set.defs))
		return set, err
	}
	if set.since, err = m.linesSince(ctx, base); err != nil {
		return touchedSet{}, err
	}
	spans, err := spansTouched(set.defs, set.since, m.readWorkspaceFile)
	if err != nil {
		return touchedSet{}, err
	}
	if set.fns, err = m.touchedIn(ctx, set.defs, spans); err != nil {
		return touchedSet{}, err
	}
	set.changed, err = m.handWritten(ctx, slices.Sorted(maps.Keys(set.since.lines)))
	return set, err
}

// handWritten is paths less the generated ones, in order.
func (m *Magus) handWritten(ctx context.Context, paths []string) ([]string, error) {
	byPath, err := m.classified(ctx, paths)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(paths), func(p string) bool {
		f, ok := byPath[p]
		return !ok || f.Generated()
	}), nil
}

func (m *Magus) classified(ctx context.Context, paths []string) (map[string]*diff.File, error) {
	entries, err := m.ClassifyFiles(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("change risk: %w", err)
	}
	byPath := make(map[string]*diff.File, len(entries))
	for _, e := range entries {
		byPath[e.Path] = &diff.File{Path: e.Path, Project: e.Project, Role: e.Role}
	}
	return byPath, nil
}

func everySymbol(defs map[string]symbolDef) map[string]string {
	out := make(map[string]string, len(defs))
	for id := range defs {
		out[id] = diff.ChangeBody
	}
	return out
}

// touchedIn is the functions changes names, less tests and generated files.
func (m *Magus) touchedIn(ctx context.Context, defs map[string]symbolDef, changes map[string]string) ([]touchedFunction, error) {
	paths := make([]string, 0, len(changes))
	for id := range changes {
		paths = append(paths, defs[id].path)
	}
	slices.Sort(paths)
	byPath, err := m.classified(ctx, slices.Compact(paths))
	if err != nil {
		return nil, err
	}
	return touchedFunctions(defs, byPath, conformanceInput{changes: changes}), nil
}

// sinceBase is what the tree changed since a base: each file's working-tree
// lines, and the text of every line the changes removed. committed holds the
// hunks from base to HEAD, working those from HEAD to the working tree.
type sinceBase struct {
	lines     map[string][]int
	removed   string
	committed map[string][]changeset.Hunk
	working   map[string][]changeset.Hunk
	// workingOld and committedOld map a renamed file's new path to its old one
	// in the working patch and the committed patch.
	workingOld   map[string]string
	committedOld map[string]string
}

// headPath is where path was at HEAD, before the working tree renamed it.
func (s sinceBase) headPath(path string) string { return cmp.Or(s.workingOld[path], path) }

// basePath is where the base held the file path is now.
func (s sinceBase) basePath(path string) string {
	at := s.headPath(path)
	return cmp.Or(s.committedOld[at], at)
}

// baseSpan is fn's lines as the base held them, false when it is new since.
func (s sinceBase) baseSpan(fn touchedFunction) (int, int, bool) {
	if fn.Change == diff.ChangeAdded || fn.EndLine == 0 {
		return 0, 0, false
	}
	lo, hi, ok := oldSpan(fn.Line, fn.EndLine, s.working[fn.Path])
	if !ok {
		return 0, 0, false
	}
	return oldSpan(lo, hi, s.committed[s.headPath(fn.Path)])
}

// headLine maps a base line of path to the working tree, false when the
// change removed or rewrote it.
func (s sinceBase) headLine(path string, line int) (int, bool) {
	at, ok := forwardLine(line, s.committed[s.headPath(path)])
	if !ok {
		return 0, false
	}
	return forwardLine(at, s.working[path])
}

func fileHunks(patch string) map[string][]changeset.Hunk {
	out := map[string][]changeset.Hunk{}
	for _, f := range changeset.Parse(patch) {
		out[f.Path] = f.Hunks
	}
	return out
}

// renamedPaths maps the new path of each renamed file in patch to its old one.
func renamedPaths(patch string) map[string]string {
	out := map[string]string{}
	for _, f := range changeset.Parse(patch) {
		if f.Status == changeset.StatusRenamed && f.OldPath != f.Path {
			out[f.Path] = f.OldPath
		}
	}
	return out
}

func (m *Magus) linesSince(ctx context.Context, base string) (sinceBase, error) {
	committed, err := m.RangeDiff(ctx, base, "HEAD", nil)
	if err != nil {
		return sinceBase{}, fmt.Errorf("change risk: what HEAD changed since %s: %w", base, err)
	}
	working, err := m.WorkingDiff(ctx, nil)
	if err != nil {
		return sinceBase{}, fmt.Errorf("change risk: the working tree's changes: %w", err)
	}
	removed := readPatchFacts(committed).removedText + readPatchFacts(working).removedText
	return sinceBase{
		lines: changedLines(committed, working), removed: removed,
		committed: fileHunks(committed), working: fileHunks(working),
		committedOld: renamedPaths(committed), workingOld: renamedPaths(working),
	}, nil
}

// changedLines is the working patch's changed lines, plus the committed
// patch's carried through the working patch's hunks. A committed line the
// working tree rewrote is already among the working patch's lines.
func changedLines(committed, working string) map[string][]int {
	out := newSideLines(working)
	hunks := fileHunks(working)
	for p, lines := range newSideLines(committed) {
		out[p] = append(out[p], forwardLines(lines, hunks[p])...)
	}
	for p := range out {
		slices.Sort(out[p])
		out[p] = slices.Compact(out[p])
	}
	return out
}

func newSideLines(patch string) map[string][]int {
	out := map[string][]int{}
	for p, hunks := range impact.ChangedLinesByHunk(patch) {
		for _, h := range hunks {
			out[p] = append(out[p], h.Lines...)
		}
	}
	return out
}

func forwardLines(lines []int, hunks []changeset.Hunk) []int {
	var out []int
	for _, line := range lines {
		if moved, ok := forwardLine(line, hunks); ok {
			out = append(out, moved)
		}
	}
	return out
}

// forwardLine maps an old-side line to the new side, false when a hunk
// rewrote it. A hunk's context row keeps its line.
func forwardLine(line int, hunks []changeset.Hunk) (int, bool) {
	shift := 0
	for _, h := range hunks {
		if line < firstShifted(h.OldStart, h.OldCount) {
			break
		}
		if line < h.OldStart+h.OldCount {
			return contextPair(h.Rows, line, false)
		}
		shift += h.NewCount - h.OldCount
	}
	return line + shift, true
}

// backLine is forwardLine from the new side to the old.
func backLine(line int, hunks []changeset.Hunk) (int, bool) {
	shift := 0
	for _, h := range hunks {
		if line < firstShifted(h.NewStart, h.NewCount) {
			break
		}
		if line < h.NewStart+h.NewCount {
			return contextPair(h.Rows, line, true)
		}
		shift += h.NewCount - h.OldCount
	}
	return line - shift, true
}

// firstShifted is the first line of a side a hunk moves. One with no lines
// on that side inserts after start.
func firstShifted(start, count int) int {
	if count == 0 {
		return start + 1
	}
	return start
}

// contextPair is the other side's line of the context row at line, read
// from the new side when fromNew.
func contextPair(rows []changeset.Row, line int, fromNew bool) (int, bool) {
	for _, r := range rows {
		if r.Kind != changeset.KindContext || r.OldLine == nil || r.NewLine == nil {
			continue
		}
		if fromNew && *r.NewLine == line {
			return *r.OldLine, true
		}
		if !fromNew && *r.OldLine == line {
			return *r.NewLine, true
		}
	}
	return 0, false
}

// oldSpan is the old side of new lines start..end, false when none of them
// is old. A removed run inside the span belongs to it, and so does one a
// rewritten first line replaced.
func oldSpan(start, end int, hunks []changeset.Hunk) (int, int, bool) {
	var old []int
	for line := start; line <= end; line++ {
		if at, ok := backLine(line, hunks); ok {
			old = append(old, at)
		}
	}
	for _, h := range hunks {
		old = append(old, removedInSpan(h.Rows, start, end)...)
	}
	if len(old) == 0 {
		return 0, 0, false
	}
	return slices.Min(old), slices.Max(old), true
}

// removedInSpan is the old lines of the removed runs that precede a row of
// start..end. A run before an unchanged first row belongs to a neighbor.
func removedInSpan(rows []changeset.Row, start, end int) []int {
	var out, pending []int
	for _, r := range rows {
		switch {
		case r.Kind == changeset.KindDel && r.OldLine != nil:
			pending = append(pending, *r.OldLine)
		case r.NewLine != nil:
			n := *r.NewLine
			if n >= start && n <= end && (n > start || r.Kind == changeset.KindAdd) {
				out = append(out, pending...)
			}
			pending = pending[:0]
		}
	}
	return out
}

// spansTouched marks each defined symbol whose lines a change touched. A
// symbol that only moved is not touched: its text is added whole and a
// removed line run spells it verbatim. An end line that does not parse is an
// error: read as one line, it would leave the body's edits unscored.
func spansTouched(defs map[string]symbolDef, since sinceBase, read func(string) (string, bool)) (map[string]string, error) {
	out := map[string]string{}
	for id, def := range defs {
		start := sourceLine(def.node.Source)
		end := start
		if raw, ok := def.node.Attrs[attrDefEndLine]; ok {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("change risk: the end line %q of %s is not a number: %w", raw, def.node.Source, err)
			}
			end = max(start, n)
		}
		if slices.ContainsFunc(since.lines[def.path], inSpan(start, end)) && !since.moved(def.path, start, end, read) {
			out[id] = diff.ChangeBody
		}
	}
	return out, nil
}

// moved reports whether lines start..end of path are all changed lines and
// their text is among the removed ones.
func (s sinceBase) moved(path string, start, end int, read func(string) (string, bool)) bool {
	changed := s.lines[path]
	for line := start; line <= end; line++ {
		if !slices.Contains(changed, line) {
			return false
		}
	}
	src, ok := read(path)
	body, inRange := bodyLines(src, start, end)
	return ok && inRange && strings.Contains(fmt.Sprintf("\n%s", s.removed), fmt.Sprintf("\n%s\n", body))
}

func inSpan(start, end int) func(int) bool {
	return func(line int) bool { return line >= start && line <= end }
}

// riskScorer answers the scorer's lookups from the workspace.
func (m *Magus) riskScorer(defs map[string]symbolDef) riskScorer {
	owner, kept := m.projectOwner(), oncePerProject(m.readCoverageReport)
	return riskScorer{
		read:     m.readWorkspaceFile,
		claimant: m.spellClaiming(),
		owner:    owner,
		indexed:  indexedComplexity(defs),
		report: func(fn touchedFunction, cov spells.Coverage) (keptReport, string) {
			return kept(owner(fn.Path), cov)
		},
	}
}

func (m *Magus) readWorkspaceFile(p string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(m.ws.Root, filepath.FromSlash(p)))
	return string(b), err == nil
}

// spellClaiming finds, among the spells bound to a path's project, the one
// whose language claims its extension.
func (m *Magus) spellClaiming() func(p string) *spells.Spell {
	owner := m.projectOwner()
	bound := map[string][]*spells.Spell{}
	for _, p := range m.ws.All() {
		bound[p.Path] = p.ResolvedSpells
	}
	return func(p string) *spells.Spell { return claimingSpell(bound[owner(p)], p) }
}

func claimingSpell(list []*spells.Spell, p string) *spells.Spell {
	ext := strings.ToLower(path.Ext(p))
	for _, sp := range list {
		if slices.Contains(sp.LanguageExtensions(), ext) {
			return sp
		}
	}
	return nil
}

// indexedComplexity reads the complexity an AST-reading indexer left on a
// symbol. The attribute's absence means it did not count.
func indexedComplexity(defs map[string]symbolDef) func(id string) (int, bool) {
	return func(id string) (int, bool) {
		raw, ok := defs[id].node.Attrs[knowledge.AttrComplexity]
		n, err := strconv.Atoi(raw)
		return n, ok && err == nil
	}
}

// oncePerProject reads each project's report once per scoring.
func oncePerProject(read func(string, spells.Coverage) (keptReport, string)) func(string, spells.Coverage) (keptReport, string) {
	type kept struct {
		report keptReport
		reason string
	}
	seen := map[string]kept{}
	return func(project string, cov spells.Coverage) (keptReport, string) {
		key := fmt.Sprintf("%s\x00%s", project, cov.Profile)
		k, ok := seen[key]
		if !ok {
			k.report, k.reason = read(project, cov)
			seen[key] = k
		}
		return k.report, k.reason
	}
}

// readCoverageReport is the profile the project's tests last left in the
// working tree.
func (m *Magus) readCoverageReport(project string, cov spells.Coverage) (keptReport, string) {
	data, err := os.ReadFile(filepath.Join(m.ws.Root, filepath.FromSlash(project), filepath.FromSlash(cov.Profile)))
	if errors.Is(err, fs.ErrNotExist) {
		return keptReport{}, fmt.Sprintf("%s holds no coverage profile at %s: run the target that writes it", project, cov.Profile)
	}
	if err != nil {
		return keptReport{}, fmt.Sprintf("the coverage profile of %s could not be read: %s", project, err)
	}
	files, err := m.parseReport(project, cov, data)
	if err != nil {
		return keptReport{}, fmt.Sprintf("the coverage profile of %s: %s", project, err)
	}
	return keptReport{project: project, files: files}, ""
}

// parseReport reads a report with workspace-relative paths. A Go profile's
// paths are stripped of the project directory's import path first.
func (m *Magus) parseReport(project string, cov spells.Coverage, data []byte) ([]coverage.File, error) {
	importPath := ""
	if cov.Format == spells.CoverageFormatGoProfile {
		_, ip, ok := goImportPath(m.ws.Root, path.Join(project, "go.mod"))
		if !ok {
			return nil, fmt.Errorf("no go.mod at or above %s names the module its profile's paths are in", project)
		}
		importPath = ip
	}
	files, err := coverage.Parse(cov.Format, data, importPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cov.Profile, err)
	}
	return coverage.Rebase(files, m.ws.Root, project), nil
}

// attachChangeRisk adds advice to each symbol the review says changed: the
// threshold that refuses is repo policy. changes is what the review
// classified, or nil to read it off the files' symbols.
func (m *Magus) attachChangeRisk(out *diff.Diff, byPath map[string]*diff.File, graph *knowledge.Graph, changes map[string]string) {
	if changes == nil {
		changes = symbolChanges(out.Files)
	}
	if len(changes) == 0 {
		return
	}
	defs := definedSymbols(graph.Nodes())
	fns := touchedFunctions(defs, byPath, conformanceInput{changes: changes})
	attachChangeRiskChecks(out.Files, m.riskScorer(defs).scoreAll(fns))
}

func symbolChanges(files []diff.File) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, s := range f.Symbols {
			if s.Change != "" {
				out[s.ID] = s.Change
			}
		}
	}
	return out
}

func attachChangeRiskChecks(files []diff.File, risk types.ChangeRisk) {
	checks := riskChecks(risk)
	for _, f := range files {
		for j := range f.Symbols {
			addCheck(&f.Symbols[j], checks)
		}
	}
}

func addCheck(sym *diff.Symbol, checks map[string]types.Check) {
	if c, ok := checks[sym.ID]; ok {
		sym.Checks = append(sym.Checks, c)
	}
}

// riskCheck names the check on a DiffSymbol.
const riskCheck = "change-risk"

func riskChecks(risk types.ChangeRisk) map[string]types.Check {
	out := map[string]types.Check{}
	for _, f := range risk.Functions {
		out[f.Symbol] = types.Check{
			Name: riskCheck, Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
			Message: fmt.Sprintf("change risk %.1f: complexity %d, %d of %d coverable units covered",
				f.Score, f.Complexity, f.Covered, f.Coverable),
		}
	}
	for _, g := range risk.Gaps {
		out[g.Symbol] = types.Check{
			Name: riskCheck, Status: types.CheckAdvice, Evidence: types.EvidenceUnknown,
			Message: "change risk unmeasured: " + g.Reason,
		}
	}
	return out
}

// newSinceBase is the base reason of a function the base does not hold.
const newSinceBase = "it is new since base"
