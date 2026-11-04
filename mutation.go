package magus

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// mutantTimeoutFactor times a scope's unmutated run is how long one mutant
// may run. A mutant still running then is killed: it hung the tests.
const mutantTimeoutFactor = 10

// failingBaseline is the gap of a function whose scoped tests fail unmutated.
// Every mutant would read as killed, so none is judged.
const failingBaseline = "its scoped tests fail without a mutant"

// mutantRule names the rule a mutant marker finding reports under.
const mutantRule = "touched-functions-kill-their-mutants"

// mutantsDefinition names what an outcome means, so a reader of the answer
// need not know the engine.
const mutantsDefinition = "a mutant swaps one token of a touched function on a line its scoped tests cover; " +
	"killed when those tests fail or outrun ten times their unmutated run, survived when they pass"

// Mutants judges the mutants of the functions touched since base: what HEAD
// added since it forked from base, and the working tree's edits on top. Each
// runs in a scratch copy against the tests its spell scopes to its file,
// never replayed from the action cache. base "" is refused: mutating a whole
// workspace is not offered.
func (m *Magus) Mutants(ctx context.Context, base string) (types.Mutants, error) {
	if base == "" {
		return types.Mutants{}, errors.New("mutants: name a base, since mutating every function of a workspace is not offered")
	}
	_, fns, err := m.touchedSince(ctx, base)
	if err != nil {
		return types.Mutants{}, err
	}
	subjects, gaps := m.mutationSubjects(fns)
	out, err := m.mutateSubjects(ctx, subjects)
	if err != nil {
		return types.Mutants{}, err
	}
	out.Definition, out.Base, out.Gaps = mutantsDefinition, base, append(gaps, out.Gaps...)
	return out, nil
}

func (m *Magus) mutateSubjects(ctx context.Context, subjects []mutationSubject) (types.Mutants, error) {
	if len(subjects) == 0 {
		return types.Mutants{}, nil
	}
	e, err := m.newMutationEngine(ctx, subjectLinks(subjects))
	if err != nil {
		return types.Mutants{}, err
	}
	return e.mutate(ctx, subjects)
}

// mutationSubject is one touched function and how to test it. A mutant is
// judged by spell::test over scope. The baseline is spell::coverage over the
// same scope, so a covered line is one the mutant's tests reach.
type mutationSubject struct {
	fn       touchedFunction
	syntax   spells.Syntax
	project  string
	spell    string
	test     string
	coverage spells.Coverage
	scope    []string
	links    []string
}

// target names op through its spell, so no magusfile target of the same
// name stands in for it.
func (s mutationSubject) target(op string) string { return fmt.Sprintf("%s::%s", s.spell, op) }

// mutationSubjects resolves each function's spell, test op and scope. One
// that cannot be mutated is a gap, never a pass.
func (m *Magus) mutationSubjects(fns []touchedFunction) ([]mutationSubject, []types.FunctionGap) {
	claimant, owner := m.spellClaiming(), m.projectOwner()
	var subjects []mutationSubject
	var gaps []types.FunctionGap
	for _, fn := range fns {
		s, reason := newMutationSubject(m.ws.Root, fn, owner(fn.Path), claimant(fn.Path))
		if reason != "" {
			gaps = append(gaps, functionGap(fn, 0, reason))
			continue
		}
		subjects = append(subjects, s)
	}
	return subjects, gaps
}

func newMutationSubject(root string, fn touchedFunction, project string, sp *spells.Spell) (mutationSubject, string) {
	if sp == nil {
		return mutationSubject{}, fmt.Sprintf("no spell bound to %s claims %s files", project, path.Ext(fn.Path))
	}
	s, test, reason := spellSubject(sp)
	if reason != "" {
		return mutationSubject{}, reason
	}
	s.fn, s.project, s.links = fn, project, projectLinks(project, sp.IgnoreDirs())
	s.scope, reason = subjectScope(root, project, fn.Path, test)
	return s, reason
}

// spellSubject is what sp declares to mutate a function: its syntax, its
// coverage, and the test op that coverage names.
func spellSubject(sp *spells.Spell) (mutationSubject, spells.Command, string) {
	syn, reason := mutationSyntax(sp)
	if reason != "" {
		return mutationSubject{}, spells.Command{}, reason
	}
	cov, ok := sp.Op(spells.CoverageOp)
	if !ok || cov.Coverage == nil {
		return mutationSubject{}, spells.Command{}, fmt.Sprintf("spell %s declares no coverage (mgs_getCoverage)", sp.Name())
	}
	test, ok := sp.Op(cov.Coverage.Test)
	if !ok {
		return mutationSubject{}, spells.Command{}, fmt.Sprintf("spell %s's coverage names test op %s, which it does not declare", sp.Name(), cov.Coverage.Test)
	}
	s := mutationSubject{syntax: syn, spell: sp.Name(), test: cov.Coverage.Test, coverage: *cov.Coverage}
	return s, test.Command, ""
}

func mutationSyntax(sp *spells.Spell) (spells.Syntax, string) {
	syn := sp.Syntax()
	if syn == nil || syn.Mutations == nil {
		return spells.Syntax{}, fmt.Sprintf("spell %s declares no mutations", sp.Name())
	}
	return *syn, ""
}

// subjectScope is test's TestScope for name, as args relative to project.
// An entry naming no file is dropped. A declared scope left with none is a
// reason: running the whole project instead would hide it.
func subjectScope(root, project, name string, test spells.Command) ([]string, string) {
	dir := filepath.Join(root, filepath.FromSlash(project))
	scope := slices.DeleteFunc(test.ExpandTestScope(projectRelative(project, name)), func(entry string) bool {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(entry)))
		return err != nil
	})
	if len(test.TestScope) > 0 && len(scope) == 0 {
		return nil, fmt.Sprintf("no test_scope entry names a file for %s", name)
	}
	return scope, ""
}

func projectRelative(project, name string) string {
	if project == "." {
		return name
	}
	return strings.TrimPrefix(name, project+"/")
}

func projectLinks(project string, dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, path.Join(project, d))
	}
	return out
}

func subjectLinks(subjects []mutationSubject) []string {
	var out []string
	for _, s := range subjects {
		out = append(out, s.links...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// plannedMutant is a mutant with its site re-based onto the whole file.
type plannedMutant struct {
	types.Mutant
	site spells.MutationSite
	key  string
}

// subjectPlan is one subject's mutants, judged in place. gap is set when the
// subject could not be judged.
type subjectPlan struct {
	subject mutationSubject
	src     string
	mutants []plannedMutant
	gap     string
	fresh   []types.MutantRun
}

// testRun is what one scoped run did. A cached run replayed an answer and
// ran no test.
type testRun struct{ passed, cached bool }

// replayed refuses a cached run: its time is no timeout, and its verdict
// may not be this tree's.
func (r testRun) replayed(target string) error {
	if r.cached {
		return fmt.Errorf("%s replayed from the cache, so it ran no test", target)
	}
	return nil
}

// mutationEngine runs mutants of touched functions in scratch copies, one per
// seat the broker granted the calling step. Each func field is a lookup or
// a run the workspace answers, so a test can answer it instead.
type mutationEngine struct {
	root  string
	seats int
	// run runs target over s's scope in dir, a scratch copy.
	run func(ctx context.Context, dir, target string, s mutationSubject) (testRun, error)
	// covered is the lines of s's file that the baseline in dir hit.
	covered func(dir string, s mutationSubject) (map[int]bool, error)
	// copy makes a scratch copy of the working tree, edits included, and
	// returns its root. The caller removes it.
	copy    func(ctx context.Context) (string, error)
	testKey func(context.Context, mutationSubject) (string, error)
	store   mutantStore
}

// newMutationEngine runs the workspace's own magus in each scratch copy, so a
// mutant meets the command path, charms, env and sandbox a target does.
func (m *Magus) newMutationEngine(ctx context.Context, links []string) (mutationEngine, error) {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil || res.VCS == nil {
		return mutationEngine{}, types.DiagnosticErrorf(types.VCSCapabilityMissing,
			"version control is disabled, so magus cannot copy the tree to mutate")
	}
	store, err := config.StoreDir(m.ws.Root, m.cfg)
	if err != nil {
		return mutationEngine{}, fmt.Errorf("mutants: %w", err)
	}
	bin, err := os.Executable()
	if err != nil {
		return mutationEngine{}, fmt.Errorf("mutants: %w", err)
	}
	copier := treeCopier{root: m.ws.Root, trees: filepath.Join(store, mutantTreesDir), vcs: res.VCS, links: links}
	return mutationEngine{
		root: m.ws.Root, seats: max(1, cache.SlotsHeld(ctx)),
		run: magusRunner{bin: bin}.run, covered: copyCoverage, copy: copier.copy,
		store: newMutantStore(store), testKey: m.scopedTestKey,
	}, nil
}

// scopedTestKey is the cache key `magus run <test> <project> -- <scope>`
// has at the unmutated tree.
func (m *Magus) scopedTestKey(ctx context.Context, s mutationSubject) (string, error) {
	key, _, err := m.ComputeTargetKey(project.WithExtraArgs(ctx, s.scope), s.project, s.target(s.test), nil)
	if err != nil {
		return "", fmt.Errorf("mutants: key %s %s: %w", s.target(s.test), s.project, err)
	}
	return key, nil
}

// mutate judges every mutant of subjects. A kept run replays; the rest run in
// scratch copies and are kept. Markers in each subject's span then except one
// survivor each, and a marker with no reason or no survivor is Unused.
func (e mutationEngine) mutate(ctx context.Context, subjects []mutationSubject) (types.Mutants, error) {
	plans, err := e.plan(ctx, subjects)
	if err != nil {
		return types.Mutants{}, err
	}
	if err := e.replay(plans); err != nil {
		return types.Mutants{}, err
	}
	if err := e.runPending(ctx, plans); err != nil {
		return types.Mutants{}, err
	}
	if err := e.store.record(ctx, freshRuns(plans)); err != nil {
		return types.Mutants{}, err
	}
	return answerMutants(plans), nil
}

func (e mutationEngine) plan(ctx context.Context, subjects []mutationSubject) ([]subjectPlan, error) {
	plans := make([]subjectPlan, 0, len(subjects))
	for _, s := range subjects {
		p, err := e.planSubject(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("mutants: %s: %w", s.fn.Label, err)
		}
		plans = append(plans, p)
	}
	return plans, nil
}

func (e mutationEngine) planSubject(ctx context.Context, s mutationSubject) (subjectPlan, error) {
	src, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(s.fn.Path)))
	if err != nil {
		return subjectPlan{}, err
	}
	p := subjectPlan{subject: s, src: string(src)}
	if s.fn.EndLine == 0 {
		p.gap = "its indexer emitted no extent, so its body cannot be cut"
		return p, nil
	}
	testKey, err := e.testKey(ctx, s)
	if err != nil {
		return subjectPlan{}, err
	}
	p.mutants, err = planMutants(s, p.src, testKey)
	return p, err
}

// planMutants lists every mutant of s. The baseline later drops those on
// lines it did not hit. A mutant's ordinal counts every site of its operator
// in the body, so a change in coverage never renames a mutant.
func planMutants(s mutationSubject, src, testKey string) ([]plannedMutant, error) {
	start, stop, err := bodySpan(src, s.fn.Line, s.fn.EndLine)
	if err != nil {
		return nil, err
	}
	sites, err := s.syntax.MutationSites(src[start:stop])
	if err != nil {
		return nil, err
	}
	ordinals := map[string]int{}
	out := make([]plannedMutant, 0, len(sites))
	for _, site := range sites {
		ordinal := ordinals[site.Operator]
		ordinals[site.Operator]++
		out = append(out, s.planSite(site, start, ordinal, testKey))
	}
	return out, nil
}

func (s mutationSubject) planSite(site spells.MutationSite, start, ordinal int, testKey string) plannedMutant {
	site.Offset += start
	site.Line += s.fn.Line - 1
	id := mutantID(s.fn.Digest, site.Operator, ordinal)
	return plannedMutant{
		Mutant: types.Mutant{
			ID: id, Symbol: s.fn.Symbol, Path: s.fn.Path, Operator: site.Operator,
			Find: site.Find, Replace: site.Replace, Line: site.Line, Col: site.Col,
		},
		site: site, key: mutantKey(id, testKey),
	}
}

// mutantID names a mutant by its function's text, so moving the function
// keeps its mutants' IDs and editing it changes them.
func mutantID(digest, operator string, ordinal int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d", digest, operator, ordinal))
	return hex.EncodeToString(sum[:])[:16]
}

// bodySpan is the byte range of src's lines line through end, 1-based and
// inclusive.
func bodySpan(src string, line, end int) (int, int, error) {
	start := lineOffset(src, line)
	if line < 1 || end < line || start < 0 {
		return 0, 0, fmt.Errorf("lines %d-%d are not in the file", line, end)
	}
	stop := lineOffset(src, end+1)
	if stop < 0 {
		stop = len(src)
	}
	return start, stop, nil
}

// lineOffset is the byte offset line starts at, or -1 past the last line.
func lineOffset(src string, line int) int {
	off := 0
	for range line - 1 {
		i := strings.IndexByte(src[off:], '\n')
		if i < 0 {
			return -1
		}
		off += i + 1
	}
	return off
}

// replay judges each mutant a kept run answers.
func (e mutationEngine) replay(plans []subjectPlan) error {
	kept, err := e.store.runs()
	if err != nil {
		return err
	}
	for i := range plans {
		for j := range plans[i].mutants {
			mu := &plans[i].mutants[j]
			if r, ok := kept[mu.key]; ok {
				mu.Outcome, mu.Seconds = r.Outcome, r.Seconds
			}
		}
	}
	return nil
}

// pending reports whether p has a mutant no kept run answered.
func (p subjectPlan) pending() bool {
	return p.gap == "" && slices.ContainsFunc(p.mutants, func(mu plannedMutant) bool { return mu.Outcome == "" })
}

// runPending runs every unanswered mutant, one scratch copy per seat. A seat
// takes a whole subject, so each copy runs a scope's baseline once.
func (e mutationEngine) runPending(ctx context.Context, plans []subjectPlan) error {
	var queue []int
	for i := range plans {
		if plans[i].pending() {
			queue = append(queue, i)
		}
	}
	work := make(chan int, len(queue))
	for _, i := range queue {
		work <- i
	}
	close(work)
	g, gctx := errgroup.WithContext(ctx)
	for range min(e.seats, len(queue)) {
		g.Go(func() error { return e.seat(gctx, work, plans) })
	}
	return g.Wait()
}

// seat runs the subjects it takes from work in one scratch copy.
func (e mutationEngine) seat(ctx context.Context, work <-chan int, plans []subjectPlan) error {
	dir, err := e.copy(ctx)
	if dir != "" {
		defer os.RemoveAll(dir)
	}
	if err != nil {
		return fmt.Errorf("mutants: scratch copy: %w", err)
	}
	for i := range work {
		if err := e.runSubject(ctx, dir, &plans[i]); err != nil {
			return err
		}
	}
	return nil
}

// runSubject runs p's baseline, then each unanswered mutant it left under
// mutantTimeoutFactor times the baseline's run.
func (e mutationEngine) runSubject(ctx context.Context, dir string, p *subjectPlan) error {
	took, err := e.baseline(ctx, dir, p)
	if err != nil || p.gap != "" {
		return err
	}
	timeout := took * mutantTimeoutFactor
	for j := range p.mutants {
		if err := e.runMutant(ctx, dir, p, j, timeout); err != nil {
			return err
		}
	}
	return nil
}

// baseline runs p's coverage op over its scope unmutated, then keeps only
// the mutants on lines it hit. A red run is p's gap. A cancelled or replayed
// run is an error: it measured no test.
func (e mutationEngine) baseline(ctx context.Context, dir string, p *subjectPlan) (time.Duration, error) {
	target := p.subject.target(spells.CoverageOp)
	start := time.Now()
	res, err := e.run(ctx, dir, target, p.subject)
	if err == nil {
		err = cmp.Or(ctx.Err(), res.replayed(target))
	}
	if err != nil {
		return 0, fmt.Errorf("mutants: %s baseline: %w", p.subject.fn.Label, err)
	}
	took := time.Since(start)
	if !res.passed {
		p.gap = failingBaseline
		return took, nil
	}
	e.keepCovered(dir, p)
	return took, nil
}

// keepCovered drops p's mutants on lines the baseline did not hit: no test
// can kill a mutant it never runs. A baseline that left no report is p's gap.
func (e mutationEngine) keepCovered(dir string, p *subjectPlan) {
	covered, err := e.covered(dir, p.subject)
	if err != nil {
		p.gap = err.Error()
		return
	}
	p.mutants = slices.DeleteFunc(p.mutants, func(mu plannedMutant) bool { return !covered[mu.Line] })
}

// runMutant writes mutant j into dir's copy of its file, runs the scoped
// tests and restores the file's bytes.
func (e mutationEngine) runMutant(ctx context.Context, dir string, p *subjectPlan, j int, timeout time.Duration) error {
	mu := &p.mutants[j]
	if mu.Outcome != "" {
		return nil
	}
	mutated, err := mu.site.Apply(p.src)
	if err != nil {
		return fmt.Errorf("mutants: %s %s: %w", mu.Path, mu.ID, err)
	}
	target := filepath.Join(dir, filepath.FromSlash(mu.Path))
	if err := swapFile(target, mutated, p.src, func() error { return e.judge(ctx, dir, p.subject, timeout, mu) }); err != nil {
		return err
	}
	p.fresh = append(p.fresh, types.MutantRun{Key: mu.key, ID: mu.ID, Outcome: mu.Outcome, Seconds: mu.Seconds, Checkout: e.root})
	return nil
}

// swapFile holds mutated at path while fn runs, then puts original back.
func swapFile(path, mutated, original string, fn func() error) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("mutants: %w", err)
	}
	if err := os.WriteFile(path, []byte(mutated), info.Mode().Perm()); err != nil {
		return fmt.Errorf("mutants: %w", err)
	}
	runErr := fn()
	if err := os.WriteFile(path, []byte(original), info.Mode().Perm()); err != nil {
		return errors.Join(runErr, fmt.Errorf("mutants: restore %s: %w", path, err))
	}
	return runErr
}

// judge sets mu's outcome: survived when the scoped tests pass, killed when
// they fail or outrun timeout. A run that reached no test is an error.
func (e mutationEngine) judge(ctx context.Context, dir string, s mutationSubject, timeout time.Duration, mu *plannedMutant) error {
	run, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	target := s.target(s.test)
	start := time.Now()
	res, err := e.run(run, dir, target, s)
	mu.Seconds = time.Since(start).Seconds()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if run.Err() != nil {
		mu.Outcome = types.MutantKilled
		return nil
	}
	if err == nil {
		err = res.replayed(target)
	}
	if err != nil {
		return fmt.Errorf("mutants: %s %s: %w", mu.Path, mu.ID, err)
	}
	mu.Outcome = testOutcome(res.passed)
	return nil
}

func testOutcome(passed bool) types.MutantOutcome {
	if passed {
		return types.MutantSurvived
	}
	return types.MutantKilled
}

func freshRuns(plans []subjectPlan) []types.MutantRun {
	var out []types.MutantRun
	for _, p := range plans {
		out = append(out, p.fresh...)
	}
	return out
}

// answerMutants is plans as a types.Mutants, each subject's markers applied.
func answerMutants(plans []subjectPlan) types.Mutants {
	var out types.Mutants
	for i := range plans {
		p := &plans[i]
		if p.gap != "" {
			out.Gaps = append(out.Gaps, subjectGap(p.subject, p.gap))
			continue
		}
		out.Unused = append(out.Unused, exceptSurvivors(p)...)
		for _, mu := range p.mutants {
			out.Mutants = append(out.Mutants, mu.Mutant)
		}
	}
	slices.SortStableFunc(out.Mutants, func(a, b types.Mutant) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
	})
	return out
}

func subjectGap(s mutationSubject, reason string) types.FunctionGap {
	fn := s.fn
	return types.FunctionGap{Symbol: fn.Symbol, Label: fn.Label, Path: fn.Path, Language: fn.Language, Line: fn.Line, Reason: reason}
}

// exceptSurvivors applies the mutant markers from the line above p's
// function through its last line. It returns each marker that excepted
// nothing.
func exceptSurvivors(p *subjectPlan) []types.Finding {
	var unused []types.Finding
	for _, mk := range knowledge.MutantMarkers(p.subject.fn.Path, []byte(p.src)) {
		if mk.Line < p.subject.fn.Line-1 || mk.Line > p.subject.fn.EndLine {
			continue
		}
		if f, bad := exceptOne(p, mk); bad {
			unused = append(unused, f)
		}
	}
	return unused
}

// exceptOne excepts the first survivor of mk's op on mk's line or the next.
func exceptOne(p *subjectPlan, mk knowledge.MutantMarker) (types.Finding, bool) {
	finding := types.Finding{Path: p.subject.fn.Path, Line: mk.Line, Col: 1, Rule: mutantRule,
		Fix: "name the swap and why its mutant is equivalent: // magus:mutant op=<swap> reason=<why>"}
	if mk.Op == "" || mk.Reason == "" {
		finding.Message = fmt.Sprintf("magus:mutant op=%q carries no op or no reason", mk.Op)
		return finding, true
	}
	i := slices.IndexFunc(p.mutants, func(mu plannedMutant) bool {
		return mu.Outcome == types.MutantSurvived && mu.Operator == mk.Op && (mu.Line == mk.Line || mu.Line == mk.Line+1)
	})
	if i < 0 {
		finding.Message = fmt.Sprintf("magus:mutant op=%s matches no surviving mutant on its line or the next", mk.Op)
		finding.Fix = "delete the marker"
		return finding, true
	}
	p.mutants[i].Outcome, p.mutants[i].Reason = types.MutantExcepted, mk.Reason
	return types.Finding{}, false
}

// magusRunner runs a scoped target through bin, a magus, in a scratch copy.
// It judges from the target's own result record: an exit status cannot tell
// a failed test from a run that never reached one.
type magusRunner struct{ bin string }

func (r magusRunner) run(ctx context.Context, dir, target string, s mutationSubject) (testRun, error) {
	args := slices.Concat([]string{"run", target, "--no-cache", "--no-volatility-retry", "-o", "jsonl", s.project, "--"}, s.scope)
	opts := spawn.ExecOptions{Dir: dir, Env: []string{copyCacheEnv(dir)}, Capture: true, Quiet: true}
	res, err := spawn.Exec(ctx, r.bin, args, opts)
	line := strings.Join(slices.Concat([]string{r.bin}, args), " ")
	if !res.Started {
		return testRun{}, fmt.Errorf("%s did not start: %w", line, err)
	}
	if res.Code == exit.TempFail {
		return testRun{}, fmt.Errorf("%s exited %d: the run found no seat or its project held, so it ran no test", line, res.Code)
	}
	result, ok := recordedResult(res.Stdout, s.project, target)
	if !ok {
		return testRun{}, fmt.Errorf("%s exited %d with no result for %s, so no test ran: %s", line, res.Code, target, lastLine(res.Stderr))
	}
	return testRun{passed: result.Status != targetFailed, cached: result.CacheHit}, nil
}

// targetFailed is the Status a report.TargetResult carries for a step that
// failed.
const targetFailed = "failed"

// recordedResult is the result stdout records for target in project. A
// record names the op without its spell.
func recordedResult(stdout, project, target string) (report.TargetResult, bool) {
	_, op, _ := strings.Cut(target, "::")
	for _, line := range strings.Split(stdout, "\n") {
		r, ok := decodeTargetResult(line)
		if ok && r.Project == project && r.Target == op {
			return r, true
		}
	}
	return report.TargetResult{}, false
}

func decodeTargetResult(line string) (report.TargetResult, bool) {
	rec, err := report.ParseLine([]byte(line))
	if err != nil || rec.Type != report.TypeTargetResult {
		return report.TargetResult{}, false
	}
	var r report.TargetResult
	return r, rec.Decode(&r) == nil
}

func lastLine(text string) string {
	text = strings.TrimSpace(text)
	return text[strings.LastIndexByte(text, '\n')+1:]
}

// copyCacheDir is the cache dir of the magus run in a scratch copy. Pinning
// it keeps each copy's entries and coverage report its own.
func copyCacheDir(dir string) string { return filepath.Join(dir, ".magus") }

func copyCacheEnv(dir string) string {
	return fmt.Sprintf("%s=%s", config.EnvName("MAGUS", "cache", "dir"), copyCacheDir(dir))
}

// copyCoverage is the lines of s's file that the coverage baseline in dir
// hit. The copy is read as a workspace of its own, as Crap reads a report.
func copyCoverage(dir string, s mutationSubject) (map[int]bool, error) {
	reportPath := filepath.Join(coverage.Dir(copyCacheDir(dir), s.project), s.coverage.File)
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, fmt.Errorf("its coverage baseline left no report: %w", err)
	}
	scratch := &Magus{ws: &types.Workspace{Root: dir}}
	files, err := scratch.parseReport(s.project, s.coverage, data)
	if err != nil {
		return nil, fmt.Errorf("its coverage baseline's report: %w", err)
	}
	return hitLines(files, s.fn.Path), nil
}

func hitLines(files []coverage.File, name string) map[int]bool {
	hit := map[int]bool{}
	i := slices.IndexFunc(files, func(f coverage.File) bool { return f.Path == name })
	if i < 0 {
		return hit
	}
	for _, b := range files[i].Blocks {
		if b.Hits == 0 {
			continue
		}
		for line := b.StartLine; line <= b.EndLine; line++ {
			hit[line] = true
		}
	}
	return hit
}

// mutantTreesDir holds the scratch copies inside the workspace store. A
// sandboxed gate may write the store but not the user cache.
const mutantTreesDir = "mutant-trees"

// treeCopier exports HEAD into a new dir under trees and lays the working
// tree's uncommitted edits over it. links are workspace-relative dirs, such
// as node_modules, linked rather than copied; a dir the tests write is not
// one.
type treeCopier struct {
	root  string
	trees string
	vcs   types.VCSDriver
	links []string
}

func (c treeCopier) copy(ctx context.Context) (string, error) {
	if err := os.MkdirAll(c.trees, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(c.trees, "")
	if err != nil {
		return "", err
	}
	if err := c.export(ctx, dir); err != nil {
		return dir, err
	}
	if err := c.overlay(ctx, dir); err != nil {
		return dir, err
	}
	return dir, linkDirs(c.root, dir, c.links)
}

func (c treeCopier) export(ctx context.Context, dir string) error {
	head, err := c.vcs.FindCommit(ctx, c.root, "")
	if err != nil {
		return err
	}
	return c.vcs.ExportRevision(ctx, c.root, head.ID, dir)
}

// overlay copies each uncommitted path into dir, removing one the tree
// deleted. DirtyFiles names paths from the repository root, which may sit
// above the workspace.
func (c treeCopier) overlay(ctx context.Context, dir string) error {
	repo, err := c.vcs.Root(ctx, c.root)
	if err != nil {
		return err
	}
	dirty, err := c.vcs.DirtyFiles(ctx, c.root, nil)
	if err != nil {
		return err
	}
	for _, p := range dirty {
		if err := overlayFile(c.root, dir, filepath.Join(repo, filepath.FromSlash(p))); err != nil {
			return err
		}
	}
	return nil
}

// overlayFile mirrors abs, a path in the working tree, into dir. A path
// outside root is not the workspace's, so it is skipped.
func overlayFile(root, dir, abs string) error {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	data, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		return removeIfPresent(filepath.Join(dir, rel))
	}
	if err != nil {
		return err
	}
	return writeCopy(filepath.Join(dir, rel), data, abs)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeCopy(dst string, data []byte, src string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// linkDirs links each of links that root holds into dir. One the copy
// already holds is tracked, so the export wrote the right copy of it.
func linkDirs(root, dir string, links []string) error {
	for _, l := range links {
		src := filepath.Join(root, filepath.FromSlash(l))
		dst := filepath.Join(dir, filepath.FromSlash(l))
		if !exists(src) || exists(dst) {
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// mutantStoreFile is the mutation cache inside the shared workspace store.
const mutantStoreFile = "mutants.jsonl"

// mutantStore keeps what each mutant did under one scoped test run. A hit
// replays an outcome, so deleting the file changes how long an answer takes,
// never the answer.
type mutantStore struct {
	l file.JSONL[types.MutantRun]
}

func newMutantStore(dir string) mutantStore {
	return mutantStore{l: file.JSONL[types.MutantRun]{
		Path: filepath.Join(dir, mutantStoreFile), Perm: 0o600,
		Key: func(r types.MutantRun) string { return r.Key },
	}}
}

// mutantKey keys a mutant's run by its ID and the scoped test op's cache key,
// so a changed test, tool or scope misses.
func mutantKey(id, testKey string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s", id, testKey))
	return hex.EncodeToString(sum[:])
}

func (s mutantStore) runs() (map[string]types.MutantRun, error) {
	all, err := s.l.Load()
	if err != nil {
		return nil, fmt.Errorf("mutants: %w", err)
	}
	out := make(map[string]types.MutantRun, len(all))
	for _, r := range all {
		out[r.Key] = r
	}
	return out, nil
}

// record appends runs, none when there are none.
func (s mutantStore) record(ctx context.Context, runs []types.MutantRun) error {
	if len(runs) == 0 {
		return nil
	}
	err := s.l.Update(ctx, func([]types.MutantRun) []types.MutantRun { return runs })
	if err != nil {
		return fmt.Errorf("mutants: record %d runs: %w", len(runs), err)
	}
	return nil
}
