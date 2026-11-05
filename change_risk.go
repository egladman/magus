package magus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
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

// keptReport is a coverage op's report as the last run left it.
type keptReport struct {
	project string
	files   []coverage.File
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
	op, ok := sp.Op(spells.CoverageOp)
	if !ok || op.Coverage == nil {
		return 0, 0, fmt.Sprintf("spell %s declares no coverage (mgs_getCoverage)", sp.Name())
	}
	report, reason := s.report(fn, *op.Coverage)
	if reason != "" {
		return 0, 0, reason
	}
	return report.span(fn)
}

// span tallies fn's lines.
func (r keptReport) span(fn touchedFunction) (covered, coverable int, reason string) {
	i := slices.IndexFunc(r.files, func(f coverage.File) bool { return f.Path == fn.Path })
	if i < 0 {
		return 0, 0, fmt.Sprintf("the coverage report for %s instruments no line of %s", r.project, fn.Path)
	}
	covered, coverable = r.files[i].Span(fn.Line, fn.EndLine)
	if coverable == 0 {
		return 0, 0, fmt.Sprintf("the coverage report for %s instruments no line in %s lines %d-%d", r.project, fn.Path, fn.Line, fn.EndLine)
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
// Coverage runs in scratch copies of the tree, scoped to the touched files'
// tests, so a caller inside a run holding the workspace's projects never waits
// on itself. A named base bounds the measure by measureBudget.
func (m *Magus) ChangeRisk(ctx context.Context, base string) (types.ChangeRisk, error) {
	if base != "" {
		var cancel context.CancelFunc
		ctx, cancel = withBudget(ctx, "change risk")
		defer cancel()
	}
	start := time.Now()
	set, err := m.touchedSince(ctx, base)
	if err != nil {
		return types.ChangeRisk{}, stopped(ctx, "change risk", start, "finding the touched functions", err)
	}
	meter, err := m.newRiskMeter(ctx, set, base)
	if err != nil {
		return types.ChangeRisk{}, stopped(ctx, "change risk", start, "resolving the base", err)
	}
	meter.start = start
	out, err := meter.measure(ctx, set.fns)
	if err != nil {
		return types.ChangeRisk{}, err
	}
	out.Base = base
	return out, nil
}

// measureBudget bounds one Change.risk or Change.mutants. lint-rules runs
// both in one target, and the pair must end inside its kill.
const measureBudget = 7 * time.Minute

// withBudget is ctx bounded by measureBudget, its cause naming the budget.
func withBudget(ctx context.Context, measure string) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, measureBudget, fmt.Errorf("%s ran past its %s budget", measure, measureBudget))
}

// stopped names what a measure was doing when ctx ended, and how long it ran.
// A stop never reads as a bare "context canceled". While ctx is live, err
// passes through.
func stopped(ctx context.Context, measure string, start time.Time, doing string, err error) error {
	if err == nil || ctx.Err() == nil {
		return err
	}
	return fmt.Errorf("%s: stopped %s after %s: %w", measure, doing, time.Since(start).Round(time.Second), context.Cause(ctx))
}

// coverageScope is one run of a spell's coverage op over a test scope. A nil
// scope runs the whole project.
type coverageScope struct {
	project  string
	spell    string
	coverage spells.Coverage
	scope    []string
}

func (c coverageScope) key() string {
	return strings.Join(slices.Concat([]string{c.project, c.spell}, c.scope), "\x00")
}

// runKey names the one coverage run of c's project and spell.
func (c coverageScope) runKey() string { return c.project + "\x00" + c.spell }

// target names op through its spell, so no magusfile target of the same
// name stands in for it.
func (c coverageScope) target(op string) string { return fmt.Sprintf("%s::%s", c.spell, op) }

func (c coverageScope) command() string {
	return strings.Join(slices.Concat([]string{"magus run", c.target(spells.CoverageOp), c.project, "--"}, c.scope), " ")
}

// scopeOf is the coverage run that measures name, a file of project that sp
// claims. whole runs the whole project instead of name's test scope.
func scopeOf(root, project, name string, sp *spells.Spell, whole bool) (coverageScope, string) {
	if sp == nil {
		return coverageScope{}, fmt.Sprintf("no spell bound to %s claims %s files", project, path.Ext(name))
	}
	cov, ok := sp.Op(spells.CoverageOp)
	if !ok || cov.Coverage == nil {
		return coverageScope{}, fmt.Sprintf("spell %s declares no coverage (mgs_getCoverage)", sp.Name())
	}
	test, ok := sp.Op(cov.Coverage.Test)
	if !ok {
		return coverageScope{}, fmt.Sprintf("spell %s's coverage names test op %s, which it does not declare", sp.Name(), cov.Coverage.Test)
	}
	c := coverageScope{project: project, spell: sp.Name(), coverage: *cov.Coverage}
	if whole {
		return c, ""
	}
	scope, reason := subjectScope(root, project, name, test.Command)
	c.scope = scope
	return c, reason
}

// scopeRun is what one coverage scope's run left: its report's files, or why
// there are none.
type scopeRun struct {
	files  []coverage.File
	reason string
}

// riskMeter measures change risk in scratch copies. Each func field is a
// lookup or a run the workspace answers, so a test can answer it instead.
type riskMeter struct {
	// scorer answers complexity at head; measure sets its report.
	scorer riskScorer
	// scopeOf is the coverage run that measures a file, or why there is none.
	scopeOf func(path string) (coverageScope, string)
	// covers reports whether a changed file's spell declares coverage at all.
	covers func(path string) bool
	// copy makes a scratch copy of rev, or of the working tree when rev is "".
	copy   func(ctx context.Context, rev string, links []string) (string, error)
	run    scopedRun
	report func(dir string, c coverageScope) ([]coverage.File, error)
	// commit is the merge base the change is judged against, "" for none.
	commit  string
	since   sinceBase
	changed []string
	// links are the dirs each copy links rather than copies.
	links []string
	// start is when the measure began, for the elapsed time a stop names.
	start time.Time
}

func (m *Magus) newRiskMeter(ctx context.Context, set touchedSet, base string) (riskMeter, error) {
	sc, err := m.newScratch(ctx)
	if err != nil {
		return riskMeter{}, fmt.Errorf("change risk: %w", err)
	}
	claimant, owner := m.spellClaiming(), m.projectOwner()
	r := riskMeter{
		scorer: m.riskScorer(set.defs),
		scopeOf: func(p string) (coverageScope, string) {
			return scopeOf(m.ws.Root, owner(p), p, claimant(p), base == "")
		},
		covers: func(p string) bool {
			sp := claimant(p)
			if sp == nil {
				return false
			}
			_, ok := sp.Op(spells.CoverageOp)
			return ok
		},
		copy: sc.copy, run: magusRunner{bin: sc.bin}.run, report: scratchReport,
		since: set.since, changed: set.changed, links: m.workspaceLinks(),
	}
	if base != "" {
		if _, r.commit, err = m.conformanceBaseCommit(ctx, base); err != nil {
			return riskMeter{}, fmt.Errorf("change risk: %w", err)
		}
	}
	return r, nil
}

// noBase is the base reason of every function when no base was named.
const noBase = "no base was named"

// measure scores fns at head and, with a commit, at base, then compares the
// changed files' lines.
func (r riskMeter) measure(ctx context.Context, fns []touchedFunction) (types.ChangeRisk, error) {
	plan := r.plan(fns)
	head, base, err := r.runSides(ctx, plan.runs)
	defer removeScratch(head.dir)
	defer removeScratch(base.dir)
	if err != nil {
		return types.ChangeRisk{}, err
	}
	scorer := r.scorer
	scorer.report = plan.lookup(head.runs)
	out := scorer.scoreAll(fns)
	if r.commit == "" {
		for i := range out.Functions {
			out.Functions[i].BaseReason = noBase
		}
		return out, nil
	}
	based := r.scorer
	based.read, based.indexed = r.readAtBase(base.dir), noIndex
	based.report = r.since.atHeadPath(plan.lookup(base.runs))
	r.judgeBase(out.Functions, fns, based)
	out.Lines = r.lines(plan, head.runs, base.runs)
	return out, nil
}

// side is one tree's coverage runs: the copy they ran in and what each left.
type side struct {
	dir  string
	runs map[string]scopeRun
}

// runSides runs runs at head and, with a commit, at base, each side in its own
// copy and both at once. The caller removes both copies, even on an error.
func (r riskMeter) runSides(ctx context.Context, runs []coverageScope) (head, base side, err error) {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		head.dir, head.runs, err = r.runAll(gctx, "", runs)
		return err
	})
	if r.commit != "" {
		g.Go(func() error {
			var err error
			base.dir, base.runs, err = r.runAll(gctx, r.commit, runs)
			return err
		})
	}
	return head, base, g.Wait()
}

func removeScratch(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func readUnder(dir string) func(string) (string, bool) {
	return func(p string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		return string(b), err == nil
	}
}

// readAtBase reads a head path from dir, the base tree, where the base held it.
func (r riskMeter) readAtBase(dir string) func(string) (string, bool) {
	read := readUnder(dir)
	return func(p string) (string, bool) { return read(r.since.basePath(p)) }
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

// scopePlan is the coverage runs a measure makes: one per project and spell.
// Each runs the union of its touched functions' and changed files' scopes.
type scopePlan struct {
	byPath map[string]coverageScope
	reason map[string]string
	runs   []coverageScope
}

func (r riskMeter) plan(fns []touchedFunction) scopePlan {
	p := scopePlan{byPath: map[string]coverageScope{}, reason: map[string]string{}}
	runOf := map[string]string{}
	merged := map[string]*coverageScope{}
	var order []string
	add := func(name string) {
		if _, done := runOf[name]; done || p.reason[name] != "" {
			return
		}
		c, reason := r.scopeOf(name)
		if reason != "" {
			p.reason[name] = reason
			return
		}
		k := c.runKey()
		runOf[name] = k
		if m, ok := merged[k]; ok {
			m.scope = unionScope(m.scope, c.scope)
			return
		}
		merged[k], order = &c, append(order, k)
	}
	for _, fn := range fns {
		add(fn.Path)
	}
	for _, name := range r.changed {
		if r.covers(name) {
			add(name)
		}
	}
	for _, k := range order {
		p.runs = append(p.runs, *merged[k])
	}
	for name, k := range runOf {
		p.byPath[name] = *merged[k]
	}
	return p
}

// unionScope is a, then b's entries a lacks. An empty scope runs the whole
// project, so it absorbs any other.
func unionScope(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := slices.Clone(a)
	for _, entry := range b {
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	return out
}

// lookup answers a scorer's report from runs.
func (p scopePlan) lookup(runs map[string]scopeRun) func(touchedFunction, spells.Coverage) (keptReport, string) {
	return func(fn touchedFunction, _ spells.Coverage) (keptReport, string) {
		if reason := p.reason[fn.Path]; reason != "" {
			return keptReport{}, reason
		}
		c := p.byPath[fn.Path]
		run := runs[c.key()]
		if run.reason != "" {
			return keptReport{}, run.reason
		}
		return keptReport{project: c.project, files: run.files}, ""
	}
}

// runAll runs each scope's coverage in one scratch copy of rev. The caller
// removes the copy, which it returns even on an error.
func (r riskMeter) runAll(ctx context.Context, rev string, runs []coverageScope) (string, map[string]scopeRun, error) {
	if len(runs) == 0 {
		return "", nil, nil
	}
	dir, err := r.copy(ctx, rev, r.links)
	if err != nil {
		err = fmt.Errorf("change risk: scratch copy: %w", err)
		return dir, nil, stopped(ctx, "change risk", r.start, "copying the tree at "+sideName(rev), err)
	}
	out := make(map[string]scopeRun, len(runs))
	for _, c := range runs {
		run, err := r.runScope(ctx, dir, rev, c)
		if err != nil {
			return dir, nil, err
		}
		out[c.key()] = run
	}
	return dir, out, nil
}

// runScope runs c in dir. At a rev, a scope entry the tree lacks is dropped,
// as the change may have added the test file.
func (r riskMeter) runScope(ctx context.Context, dir, rev string, c coverageScope) (scopeRun, error) {
	if rev != "" {
		scope, reason := scopeAtBase(dir, c.project, c.scope)
		if reason != "" {
			return scopeRun{reason: reason}, nil
		}
		c.scope = scope
	}
	return r.runOne(ctx, dir, rev, c)
}

// sideName names the tree rev is, for an error.
func sideName(rev string) string {
	if rev == "" {
		return "head"
	}
	return "base " + rev
}

// runOne runs c in dir, a copy of rev. A red or unreadable run is a reason
// that makes its functions gaps. A cancelled run, or one the scheduler could
// not seat, is an error.
func (r riskMeter) runOne(ctx context.Context, dir, rev string, c coverageScope) (scopeRun, error) {
	res, err := r.run(ctx, dir, c.target(spells.CoverageOp), c)
	if ctx.Err() != nil {
		doing := fmt.Sprintf("running `%s` at %s", c.command(), sideName(rev))
		return scopeRun{}, stopped(ctx, "change risk", r.start, doing, ctx.Err())
	}
	if errors.Is(err, errNoSeat) {
		return scopeRun{}, fmt.Errorf("change risk: %w", err)
	}
	if err != nil {
		return scopeRun{reason: err.Error()}, nil
	}
	if !res.passed {
		return scopeRun{reason: fmt.Sprintf("a test in its scope fails: `%s`", c.command())}, nil
	}
	files, err := r.report(dir, c)
	if err != nil {
		return scopeRun{reason: err.Error()}, nil
	}
	return scopeRun{files: files}, nil
}

// lines compares each changed file's covered lines at head with those at
// base. A line removed since base is never compared.
func (r riskMeter) lines(plan scopePlan, head, base map[string]scopeRun) []types.Finding {
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

// uncomparedLines is the finding for a changed file with no coverage scope to
// run, or none when the plan never looked at the file.
func uncomparedLines(name, reason string) []types.Finding {
	if reason == "" {
		return nil
	}
	return []types.Finding{{Path: name, Line: 1,
		Message: fmt.Sprintf("the coverage of %s was not compared: %s", name, reason),
		Fix:     "Give its spell a test scope that names a test, so its covered lines can be compared."}}
}

func (r riskMeter) fileLines(name string, head, base scopeRun) []types.Finding {
	for _, run := range []struct{ side, reason string }{{"", head.reason}, {"at base ", base.reason}} {
		if run.reason != "" {
			return []types.Finding{{Path: name, Line: 1,
				Message: fmt.Sprintf("the coverage of %s %swas not measured: %s", name, run.side, run.reason),
				Fix:     "Make the tests in its scope pass, so its covered lines can be compared."}}
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
	out := lineFindings(name, lost, "was covered at base and no test in its scope covers it now",
		"Restore the test that covered it, or delete the code no test needs.")
	return append(out, lineFindings(name, bare, "was added and no test in its scope covers it",
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

// touchedSince is the one entry point ChangeRisk and Mutants share.
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
	if set.fns, err = m.touchedIn(ctx, set.defs, spansTouched(set.defs, set.since, m.readWorkspaceFile)); err != nil {
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

func (m *Magus) classified(ctx context.Context, paths []string) (map[string]*types.DiffFile, error) {
	entries, err := m.ClassifyFiles(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("change risk: %w", err)
	}
	byPath := make(map[string]*types.DiffFile, len(entries))
	for _, e := range entries {
		byPath[e.Path] = &types.DiffFile{Path: e.Path, Project: e.Project, Role: e.Role}
	}
	return byPath, nil
}

func everySymbol(defs map[string]symbolDef) map[string]string {
	out := make(map[string]string, len(defs))
	for id := range defs {
		out[id] = types.DiffChangeBody
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
	if fn.Change == types.DiffChangeAdded || fn.EndLine == 0 {
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

// renamedFrom maps the new path of each renamed file in patch to its old one.
func renamedFrom(patch string) map[string]string {
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
		committedOld: renamedFrom(committed), workingOld: renamedFrom(working),
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
// removed line run spells it verbatim.
func spansTouched(defs map[string]symbolDef, since sinceBase, read func(string) (string, bool)) map[string]string {
	out := map[string]string{}
	for id, def := range defs {
		start := sourceLine(def.node.Source)
		end, _ := strconv.Atoi(def.node.Attrs[attrDefEndLine])
		end = max(start, end)
		if slices.ContainsFunc(since.lines[def.path], inSpan(start, end)) && !since.moved(def.path, start, end, read) {
			out[id] = types.DiffChangeBody
		}
	}
	return out
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
		key := fmt.Sprintf("%s\x00%s", project, cov.File)
		k, ok := seen[key]
		if !ok {
			k.report, k.reason = read(project, cov)
			seen[key] = k
		}
		return k.report, k.reason
	}
}

// readCoverageReport is the report the project's coverage op last wrote.
func (m *Magus) readCoverageReport(project string, cov spells.Coverage) (keptReport, string) {
	file := filepath.Join(coverage.Dir(m.CacheDir(), project), cov.File)
	data, err := os.ReadFile(file)
	if err != nil {
		return keptReport{}, fmt.Sprintf("no coverage report is kept for %s: run `./magus run coverage %s`", project, project)
	}
	files, err := m.parseReport(project, cov, data)
	if err != nil {
		return keptReport{}, fmt.Sprintf("the coverage report for %s: %s", project, err)
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
		return nil, fmt.Errorf("%s: %w", cov.File, err)
	}
	return coverage.Rebase(files, m.ws.Root, project), nil
}

// attachChangeRisk adds advice to each symbol the review says changed: the
// threshold that refuses is repo policy. changes is what the review
// classified, or nil to read it off the files' symbols.
func (m *Magus) attachChangeRisk(out *types.Diff, byPath map[string]*types.DiffFile, graph *knowledge.Graph, changes map[string]string) {
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

func symbolChanges(files []types.DiffFile) map[string]string {
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

func attachChangeRiskChecks(files []types.DiffFile, risk types.ChangeRisk) {
	checks := riskChecks(risk)
	for _, f := range files {
		for j := range f.Symbols {
			addCheck(&f.Symbols[j], checks)
		}
	}
}

func addCheck(sym *types.DiffSymbol, checks map[string]types.Check) {
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
