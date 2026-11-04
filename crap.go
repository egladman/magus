package magus

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/project/impact"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// crapDefinition names the formula, so a reader of the answer need not know it.
const crapDefinition = "complexity^2 * (1 - coverage)^3 + complexity, " +
	"coverage being the covered share of the coverable units in the function's lines"

// crapScorer scores touched functions. Each field is a lookup the workspace
// answers, so a test can answer it instead.
type crapScorer struct {
	// read is a workspace-relative file's text.
	read func(path string) (string, bool)
	// claimant is the spell bound to path's project that claims its
	// extension, or nil.
	claimant func(path string) *spells.Spell
	owner    func(path string) string
	// indexed is the complexity an AST-reading indexer counted for a symbol,
	// and whether it counted.
	indexed func(symbol string) (int, bool)
	// report is the project's kept coverage report, or why there is none.
	report func(project string, cov spells.Coverage) (keptReport, string)
}

// keptReport is a coverage op's report as the last run left it.
type keptReport struct {
	project string
	files   []coverage.File
}

// scoreAll scores each function, or reports it as a gap. A gap is never a
// pass and never a score of 0.
func (s crapScorer) scoreAll(fns []touchedFunction) types.Crap {
	out := types.Crap{Definition: crapDefinition}
	for _, fn := range fns {
		s.score(&out, fn)
	}
	return out
}

func (s crapScorer) score(out *types.Crap, fn touchedFunction) {
	c, reason := s.complexity(fn)
	if reason != "" {
		out.Gaps = append(out.Gaps, functionGap(fn, 0, reason))
		return
	}
	covered, coverable, reason := s.coverage(fn)
	if reason != "" {
		out.Gaps = append(out.Gaps, functionGap(fn, c, reason))
		return
	}
	out.Functions = append(out.Functions, crapFunction(fn, c, covered, coverable))
}

func (s crapScorer) complexity(fn touchedFunction) (int, string) {
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

func (s crapScorer) countTokens(fn touchedFunction, spellName string, syn spells.Syntax) (int, string) {
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
func (s crapScorer) indexerDecisions(fn touchedFunction, spellName string) (int, string) {
	n, counted := s.indexed(fn.Symbol)
	if !counted {
		return 0, fmt.Sprintf("spell %s declares no decisions and its indexer counted none", spellName)
	}
	return n, ""
}

func (s crapScorer) coverage(fn touchedFunction) (covered, coverable int, reason string) {
	sp := s.claimant(fn.Path)
	op, ok := sp.Op(spells.CoverageOp)
	if !ok || op.Coverage == nil {
		return 0, 0, fmt.Sprintf("spell %s declares no coverage (mgs_getCoverage)", sp.Name())
	}
	report, reason := s.report(s.owner(fn.Path), *op.Coverage)
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

func crapFunction(fn touchedFunction, complexity, covered, coverable int) types.CrapFunction {
	f := types.CrapFunction{
		Symbol: fn.Symbol, Label: fn.Label, Path: fn.Path, Language: fn.Language, Digest: fn.Digest,
		Line: fn.Line, EndLine: fn.EndLine, Complexity: complexity, Covered: covered, Coverable: coverable,
	}
	f.Score = types.CrapScore(complexity, f.Coverage())
	return f
}

func functionGap(fn touchedFunction, complexity int, reason string) types.FunctionGap {
	return types.FunctionGap{
		Symbol: fn.Symbol, Label: fn.Label, Path: fn.Path, Language: fn.Language,
		Line: fn.Line, Complexity: complexity, Reason: reason,
	}
}

// Crap scores the functions touched since base: what HEAD added since it
// forked from base, and the working tree's edits on top. base "" scores every
// function in the workspace.
func (m *Magus) Crap(ctx context.Context, base string) (types.Crap, error) {
	defs, fns, err := m.touchedSince(ctx, base)
	if err != nil {
		return types.Crap{}, err
	}
	scorer := m.crapScorer(defs)
	if err := m.runCoverage(ctx, scorer, fns); err != nil {
		return types.Crap{}, err
	}
	out := scorer.scoreAll(fns)
	out.Base = base
	return out, nil
}

// runCoverage runs each owning project's coverage op through the cache.
// A hit replays the report.
func (m *Magus) runCoverage(ctx context.Context, s crapScorer, fns []touchedFunction) error {
	projects := coverageProjects(s, fns)
	targets := make([]types.Target, 0, len(projects))
	for _, p := range projects {
		targets = append(targets, types.Target{Path: p, Name: spells.CoverageOp})
	}
	if len(targets) == 0 {
		return nil
	}
	if err := m.Run(ctx, targets); err != nil {
		return fmt.Errorf("crap: %w", err)
	}
	return nil
}

// coverageProjects is the sorted projects whose spell has a coverage op.
func coverageProjects(s crapScorer, fns []touchedFunction) []string {
	var out []string
	for _, fn := range fns {
		sp := s.claimant(fn.Path)
		if sp == nil {
			continue
		}
		if _, ok := sp.Op(spells.CoverageOp); ok {
			out = append(out, s.owner(fn.Path))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// crapGraph is the head graph with every symbol. Its indexes are brought
// current first, so each span describes the tree being scored.
func (m *Magus) crapGraph(ctx context.Context) (*knowledge.Graph, error) {
	projects := make([]string, 0, len(m.ws.All()))
	for _, p := range m.ws.All() {
		projects = append(projects, p.Path)
	}
	if err := m.freshenSymbolIndexes(ctx, projects); err != nil {
		return nil, fmt.Errorf("crap: %w", err)
	}
	return m.knowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
}

// touchedSince is the head graph's definitions and the functions touched
// since base: the one entry point Crap and Mutants share.
func (m *Magus) touchedSince(ctx context.Context, base string) (map[string]symbolDef, []touchedFunction, error) {
	graph, err := m.crapGraph(ctx)
	if err != nil {
		return nil, nil, err
	}
	defs := definedSymbols(graph.Nodes())
	fns, err := m.crapSubjects(ctx, defs, base)
	return defs, fns, err
}

func (m *Magus) crapSubjects(ctx context.Context, defs map[string]symbolDef, base string) ([]touchedFunction, error) {
	if base == "" {
		return m.touchedIn(ctx, defs, everySymbol(defs))
	}
	since, err := m.linesSince(ctx, base)
	if err != nil {
		return nil, err
	}
	return m.touchedIn(ctx, defs, spansTouched(defs, since, m.readWorkspaceFile))
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
	entries, err := m.ClassifyFiles(ctx, slices.Compact(paths))
	if err != nil {
		return nil, fmt.Errorf("crap: %w", err)
	}
	byPath := make(map[string]*types.DiffFile, len(entries))
	for _, e := range entries {
		byPath[e.Path] = &types.DiffFile{Path: e.Path, Project: e.Project, Role: e.Role}
	}
	return touchedFunctions(defs, byPath, conformanceInput{changes: changes}), nil
}

// sinceBase is what the tree changed since a base: each file's working-tree
// lines, and the text of every line the changes removed.
type sinceBase struct {
	lines   map[string][]int
	removed string
}

func (m *Magus) linesSince(ctx context.Context, base string) (sinceBase, error) {
	committed, err := m.RangeDiff(ctx, base, "HEAD", nil)
	if err != nil {
		return sinceBase{}, fmt.Errorf("crap: what HEAD changed since %s: %w", base, err)
	}
	working, err := m.WorkingDiff(ctx, nil)
	if err != nil {
		return sinceBase{}, fmt.Errorf("crap: the working tree's changes: %w", err)
	}
	removed := readPatchFacts(committed).removedText + readPatchFacts(working).removedText
	return sinceBase{lines: changedLines(committed, working), removed: removed}, nil
}

// changedLines is the working patch's changed lines, plus the committed
// patch's carried through the working patch's hunks. A committed line the
// working tree rewrote is already among the working patch's lines.
func changedLines(committed, working string) map[string][]int {
	out := newSideLines(working)
	hunks := map[string][]changeset.Hunk{}
	for _, f := range changeset.Parse(working) {
		hunks[f.Path] = f.Hunks
	}
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
// rewrote it.
func forwardLine(line int, hunks []changeset.Hunk) (int, bool) {
	shift := 0
	for _, h := range hunks {
		if line < firstShifted(h) {
			break
		}
		if line < h.OldStart+h.OldCount {
			return 0, false
		}
		shift += h.NewCount - h.OldCount
	}
	return line + shift, true
}

// firstShifted is the first old line a hunk moves. One with no old lines
// inserts after OldStart.
func firstShifted(h changeset.Hunk) int {
	if h.OldCount == 0 {
		return h.OldStart + 1
	}
	return h.OldStart
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

// crapScorer answers the scorer's lookups from the workspace.
func (m *Magus) crapScorer(defs map[string]symbolDef) crapScorer {
	return crapScorer{
		read:     m.readWorkspaceFile,
		claimant: m.spellClaiming(),
		owner:    m.projectOwner(),
		indexed:  indexedComplexity(defs),
		report:   oncePerProject(m.readCoverageReport),
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

// attachCrap adds a crap check to each symbol the review says changed. It
// is advice: the threshold that refuses is repo policy, not magus's.
//
// changes is what the review classified, or nil to read it off the files'
// symbols.
func (m *Magus) attachCrap(out *types.Diff, byPath map[string]*types.DiffFile, graph *knowledge.Graph, changes map[string]string) {
	if changes == nil {
		changes = symbolChanges(out.Files)
	}
	if len(changes) == 0 {
		return
	}
	defs := definedSymbols(graph.Nodes())
	fns := touchedFunctions(defs, byPath, conformanceInput{changes: changes})
	attachCrapChecks(out.Files, m.crapScorer(defs).scoreAll(fns))
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

func attachCrapChecks(files []types.DiffFile, crap types.Crap) {
	checks := crapChecks(crap)
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

// crapCheck names the check on a DiffSymbol.
const crapCheck = "crap"

func crapChecks(crap types.Crap) map[string]types.Check {
	out := map[string]types.Check{}
	for _, f := range crap.Functions {
		out[f.Symbol] = types.Check{
			Name: crapCheck, Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
			Message: fmt.Sprintf("CRAP %.1f: complexity %d, %d of %d coverable units covered",
				f.Score, f.Complexity, f.Covered, f.Coverable),
		}
	}
	for _, g := range crap.Gaps {
		out[g.Symbol] = types.Check{
			Name: crapCheck, Status: types.CheckAdvice, Evidence: types.EvidenceUnknown,
			Message: "CRAP unmeasured: " + g.Reason,
		}
	}
	return out
}
