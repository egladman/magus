package guard

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The symbol-search rule classifies each alternative of a tree search by what it looks for
// and denies when any one is a name the graph answers, serving the graph command for each
// such name and a rebuild first when the index is behind. Text runs. Only a workspace with no
// symbol index at all is advised instead, since a deny there would route nowhere. The
// catalog's Why carries the measurements.

// searchRoute is the graph command that answers one alternative of a search.
type searchRoute struct {
	name string // the symbol or the diagnostic node, as the rule's Arg records it
	run  string
	// indexed reports a route the graph found or answers, rather than a parse of a file the
	// search names.
	indexed bool
}

// graphMovedBudget bounds reading the checkout, which starts version control processes.
// Past it the checkout counts as unmoved.
const graphMovedBudget = 150 * time.Millisecond

// staleGraph is why the graph describes another tree than the one on disk.
type staleGraph struct {
	reason string
	// underway reports a history rewrite still in progress, which a rebuild now would
	// describe before it moves the tree again.
	underway bool
}

// graphMoved reports why the graph a deny would route to no longer describes this
// checkout: a merge, rebase, cherry-pick or revert is underway, or the guard index was
// built at another revision. A deny over a stale graph would push the reader from a
// correct grep to a wrong answer. With no revision to compare, the index's stamps decide.
func graphMoved(deps Dependencies) staleGraph {
	if deps.scope.root == "" {
		return staleGraph{}
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return staleGraph{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), graphMovedBudget)
	defer cancel()
	if op := knowledge.ReadGuardOperation(ctx, root); op != "" {
		return staleGraph{reason: knowledge.GuardCheckout{}.StaleAt(knowledge.GuardCheckout{Operation: op}), underway: true}
	}
	// The index header is a file read; the revision starts processes, so it waits on one.
	cacheDir, err := deps.cacheDir(root)
	if err != nil {
		return staleGraph{}
	}
	built, err := knowledge.ReadGuardIndexCheckout(cacheDir, root)
	if err != nil {
		return staleGraph{}
	}
	if built.Operation != "" {
		return staleGraph{reason: built.StaleAt(knowledge.GuardCheckout{})}
	}
	rev := deps.revision(ctx, root, "")
	if rev == "" {
		return staleGraph{}
	}
	return staleGraph{reason: built.StaleAt(knowledge.GuardCheckout{Revision: rev})}
}

// verdict is graph-stale's advice in place of the deny: the search runs, and the rebuild
// is named with when it is worth running.
func (s staleGraph) verdict() ShellVerdict {
	rebuild := "`" + hint.GraphBuild.String() + "` refreshes it"
	if s.underway {
		rebuild += " once that is finished"
	}
	return ShellVerdict{
		Context: "magus workspace: the graph is stale, " + s.reason + ", so no graph answer replaces this search and it runs as typed. " +
			rebuild + ", and the graph answers exactly again.",
		Kind:  advisoryGraphStale,
		Brief: "magus workspace: the graph is stale, " + s.reason + ". This search runs; " + rebuild + ".",
	}
}

// routesIndexed reports whether any route rests on the graph.
func routesIndexed(routes []searchRoute) bool {
	return slices.ContainsFunc(routes, func(r searchRoute) bool { return r.indexed })
}

// searchVerdict judges the searches on a line against the index, reporting false when no
// search there is one it has anything to say about.
func searchVerdict(deps Dependencies, cmds []hint.Invocation) (ShellVerdict, bool) {
	dir, ok := deps.workingDir()
	if !ok {
		return ShellVerdict{}, false
	}
	return searchVerdictAt(deps, dir, cmds)
}

func searchVerdictAt(deps Dependencies, dir string, cmds []hint.Invocation) (ShellVerdict, bool) {
	noGraph := ""
	for i, typed := range cmds {
		c := asSearch(typed)
		if !hint.IsSearchTool(c.Name) {
			continue
		}
		switch searchReachOf(deps, dir, typed, c) {
		case reachNone:
			continue
		case reachFile:
			if v, ok := fileSymbolVerdict(deps, dir, c); ok {
				return v, true
			}
			continue
		}
		js := judgeSearch(deps, c)
		if len(js.routes) == 0 {
			if js.noGraph != "" && noGraph == "" {
				noGraph = js.noGraph
			}
			continue
		}
		return symbolSearchVerdict(deps, dir, c, js, pipedInto(cmds[i+1:])), true
	}
	if noGraph != "" {
		return noGraphVerdict(noGraph), true
	}
	return ShellVerdict{}, false
}

// symbolSearchVerdict refuses a search the judgment routed, serving the rebuild first when
// the index is behind or describes another revision. A rebase still underway advises
// instead: a rebuild then would describe a tree about to move again.
func symbolSearchVerdict(deps Dependencies, dir string, c hint.Invocation, js searchJudgment, piped bool) ShellVerdict {
	moved := graphMoved(deps)
	switch {
	case moved.underway:
		return moved.verdict()
	case js.stale || moved.reason != "":
		js.stale = true
		return staleSymbolVerdict(deps, c, js, moved, piped)
	}
	return treeSymbolVerdict(deps, dir, c, js, piped)
}

// searchReach is how far a search reaches, which decides whether the graph replaces it.
type searchReach int

const (
	reachNone searchReach = iota // stdin, prose, logs, another tree, a revision
	reachFile                    // one named file: a read, which runs
	reachTree                    // a directory, a glob, or several files: a search
)

// searchReachOf places one search. typed is the command as written, c the same command read
// as a search, which differs for `git grep`.
func searchReachOf(deps Dependencies, dir string, typed, c hint.Invocation) searchReach {
	paths := invocationPaths(c)
	switch hint.Classify(c) {
	case hint.ClassSearchSource:
	case hint.ClassRead:
		if len(paths) == 0 {
			return reachNone
		}
	default:
		return reachNone
	}
	if len(paths) == 1 && isNamedFile(dir, paths[0]) {
		return reachFile
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	// refs answers for THIS workspace, so a search of another tree has no replacement here.
	if allOutside(deps.scope, paths) || searchesRevision(typed, dir) || !searchesCode(c, deps.scope.root, dir, paths, indexedExtensions(deps)) {
		return reachNone
	}
	return reachTree
}

// isNamedFile reports an operand naming one file rather than a directory or a glob. A
// missing path with a file extension is still one named file: grep reports it missing, and
// nothing was searched.
func isNamedFile(dir, p string) bool {
	if p == "" || strings.ContainsAny(p, "*?[") || strings.HasSuffix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, unresolved) {
		return true
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dir, abs)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return path.Ext(p) != ""
	}
	return !info.IsDir()
}

// indexedExtensions are the file extensions of every language a spell in the catalog
// declares a symbol indexer for: the files refs can answer for. Buzz is source no indexer
// reads, so a search of Buzz files is text to refs, which would answer with a symbol of
// another language that shares the name. An unwired catalog indexes nothing.
func indexedExtensions(deps Dependencies) map[string]bool {
	out := map[string]bool{}
	for _, s := range deps.spells() {
		if s == nil || s.SymbolIndexer() == nil {
			continue
		}
		for _, ext := range s.LanguageExtensions() {
			out["."+strings.TrimPrefix(strings.ToLower(ext), ".")] = true
		}
	}
	return out
}

// nonCodeDirs are directories whose contents no symbol index describes.
var nonCodeDirs = map[string]bool{"node_modules": true}

// sourceProbeCap bounds the entries read to decide whether a directory holds source.
const sourceProbeCap = 400

// searchesCode reports a search whose files include source a symbol index covers. A file
// filter decides when the search carries one; otherwise any operand that is source, a
// directory, or a glob over source does. A log, a capture, Markdown or JSON is text.
func searchesCode(c hint.Invocation, root, dir string, paths []string, exts map[string]bool) bool {
	if filters, ok := fileFilters(c); ok {
		return slices.ContainsFunc(filters, func(g string) bool { return exts[strings.ToLower(path.Ext(g))] })
	}
	return slices.ContainsFunc(paths, func(p string) bool { return codeOperand(root, dir, p, exts) })
}

func codeOperand(root, dir, p string, exts map[string]bool) bool {
	if p == "" || strings.HasPrefix(p, unresolved) {
		return false
	}
	// Judged below the workspace root, which may itself sit under a dot-directory (a
	// worktree under .claude/worktrees).
	segs := filepath.ToSlash(p)
	if filepath.IsAbs(p) && root != "" {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			segs = filepath.ToSlash(rel)
		}
	}
	for _, seg := range strings.Split(segs, "/") {
		// .git, .github, .claude, .magus: version control, CI config and host state.
		if nonCodeDirs[seg] || (strings.HasPrefix(seg, ".") && seg != "." && seg != "..") {
			return false
		}
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dir, abs)
	}
	info, err := os.Stat(abs)
	if err == nil && info.IsDir() {
		return holdsSource(abs, exts)
	}
	if ext := path.Ext(p); ext != "" && !strings.ContainsAny(ext, "/*?[") {
		return exts[strings.ToLower(ext)]
	}
	// A glob without an extension, or a missing directory: grep finds nothing there either
	// way, and the graph's answer for a symbol is the same.
	return strings.ContainsAny(p, "*?[") || err != nil
}

// holdsSource reports whether a directory holds a source file, reading at most
// sourceProbeCap entries; past the cap it is taken to, since a large tree almost always does.
// A directory of Markdown skills or JSON fixtures holds none, and a search there is text.
func holdsSource(abs string, exts map[string]bool) bool {
	seen := 0
	found := false
	err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipDir
		}
		if seen++; seen > sourceProbeCap {
			found = true
			return fs.SkipAll
		}
		if d.IsDir() {
			if p != abs && (strings.HasPrefix(d.Name(), ".") || nonCodeDirs[d.Name()]) {
				return fs.SkipDir
			}
			return nil
		}
		if exts[strings.ToLower(path.Ext(d.Name()))] {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found || err != nil
}

// rgTypeGlobs are the rg file types this rule recognizes, as the glob each one selects.
var rgTypeGlobs = map[string]string{
	"go": "*.go", "ts": "*.ts", "js": "*.js", "py": "*.py", "rust": "*.rs", "md": "*.md",
	"markdown": "*.md", "json": "*.json", "yaml": "*.yaml", "toml": "*.toml", "sh": "*.sh",
}

// fileFilters is the file globs a search narrows itself to (grep --include, rg -g and -t),
// reporting false when it carries none. An rg type the table does not know reads as a glob
// with no extension, which is not source.
func fileFilters(c hint.Invocation) ([]string, bool) {
	name := path.Base(c.Name)
	var out []string
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if a == "--" {
			break
		}
		flag, val, hasVal := strings.Cut(a, "=")
		var kind string
		switch {
		case flag == "--include":
			kind = "glob"
		case name == "rg" && (flag == "-g" || flag == "--glob" || flag == "--iglob"):
			kind = "glob"
		case name == "rg" && (flag == "-t" || flag == "--type"):
			kind = "type"
		case name == "rg" && strings.HasPrefix(a, "-t") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			kind, val, hasVal = "type", a[2:], true
		case name == "rg" && strings.HasPrefix(a, "-g") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			kind, val, hasVal = "glob", a[2:], true
		default:
			continue
		}
		if !hasVal {
			if i+1 >= len(c.Args) {
				break
			}
			i++
			val = c.Args[i]
		}
		if kind == "type" {
			val = rgTypeGlobs[val]
		}
		// A negated glob excludes; it never names what the search reads.
		if !strings.HasPrefix(val, "!") {
			out = append(out, val)
		}
	}
	return out, len(out) > 0
}

// grepReaderVerdict denies a search that reads a declaration's body through a context
// flag, `grep -A40 'func X' f.go`, and serves the command that prints that declaration
// whole: refs where the index vouches for the name, the declaration's own lines from a
// live parse where it does not. It fires even on one named file, the case symbol-search
// lets run: with -A the search is the read, and the read has a better command. Silent when
// any alternative is not a definition lookup, the search ignores case, or no declaration
// of the name can be found.
func grepReaderVerdict(deps Dependencies, cmds []hint.Invocation) (ShellVerdict, bool) {
	dir, ok := deps.workingDir()
	if !ok || deps.scope.root == "" {
		return ShellVerdict{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return ShellVerdict{}, false
	}
	for i, c := range cmds {
		c = asSearch(c)
		if !hint.IsSearchTool(c.Name) || !readsContext(c) || hasFlag(c.Args, 'i', "ignore-case") {
			continue
		}
		idents, ok := definitionLookups(c)
		if !ok {
			continue
		}
		routes, ok := declarationRoutes(deps, root, dir, c, idents)
		if !ok {
			continue
		}
		if routesIndexed(routes) {
			if stale := graphMoved(deps); stale.reason != "" {
				return stale.verdict(), true
			}
		}
		v := ShellVerdict{Deny: denyGrepReader(routes) + pipeNote(pipedInto(cmds[i+1:])), Rule: denyRule{Name: denyRuleGrepReader, Arg: strings.Join(idents, ",")}}
		next := make([]hint.Next, 0, len(routes))
		for _, r := range routes {
			if argv := servedArgv(r.run); len(argv) > 0 {
				next = append(next, hint.NextForDenyRemedy(string(denyRuleGrepReader), argv, "prints the whole declaration, where a context count guesses at its length."))
			}
		}
		if len(next) == len(routes) {
			v = v.withRemedy(printClause(routes)+" the declaration whole.", next...)
		}
		return v, true
	}
	return ShellVerdict{}, false
}

// contextValueShorts are the short flags that take a value, per search family, so a
// cluster's value is never read as more flags.
var contextValueShorts = map[string]string{"grep": "efmDd", "rg": "efgmtTEM", "ag": "gGm"}

// readsContext reports a context flag: -A, -B or -C with a count, grep's bare -NUM, or the
// long spellings.
func readsContext(c hint.Invocation) bool {
	family := path.Base(c.Name)
	if family != "rg" && family != "ag" {
		family = "grep"
	}
	values := contextValueShorts[family]
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		switch {
		case a == "--":
			return false
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a[2:], "=")
			if name == "after-context" || name == "before-context" || name == "context" {
				return true
			}
		case len(a) > 1 && a[0] == '-':
			cluster := a[1:]
			if family == "grep" && strings.Trim(cluster, "0123456789") == "" {
				return true
			}
			for j := 0; j < len(cluster); j++ {
				if strings.IndexByte("ABC", cluster[j]) >= 0 {
					return true
				}
				if strings.IndexByte(values, cluster[j]) >= 0 {
					if j == len(cluster)-1 {
						i++
					}
					break
				}
			}
		}
	}
	return false
}

// typeKindRe is a type lookup that names its kind, `type X struct`, which reads its body
// the way `type X` does.
var typeKindRe = regexp.MustCompile(`^type ([A-Za-z_][A-Za-z0-9_]*) (?:struct|interface)\b`)

// definitionLookups is the name every alternative of c's patterns looks up a definition
// of (`func X`, `func (r *T) X`, `type X`), reporting false when any alternative is
// something else.
func definitionLookups(c hint.Invocation) ([]string, bool) {
	patterns := hint.Patterns(c)
	if len(patterns) == 0 {
		return nil, false
	}
	var idents []string
	for _, p := range patterns {
		for _, alt := range splitAlternation(p, searchMode(c)) {
			alt = normalizeAlternative(alt)
			if m := typeKindRe.FindStringSubmatch(alt); m != nil {
				alt = "type " + m[1]
			}
			m := definitionLookupRe.FindStringSubmatch(alt)
			if len(m) < 2 || !hint.IsIdentifier(m[1]) {
				return nil, false
			}
			if !slices.Contains(idents, m[1]) {
				idents = append(idents, m[1])
			}
		}
	}
	return idents, true
}

// declarationRoutes answers each name with the command that prints its declaration, or
// reports false when one of them has none.
func declarationRoutes(deps Dependencies, root, dir string, c hint.Invocation, idents []string) ([]searchRoute, bool) {
	var routes []searchRoute
	for _, ident := range idents {
		if defined, definitive := deps.symbolDefined(ident); defined && definitive {
			routes = append(routes, searchRoute{name: ident, run: hint.Refs.With(ident, "--definition", "--source"), indexed: true})
			continue
		}
		found := liveDeclarations(deps, root, dir, c, ident)
		if len(found) == 0 {
			return nil, false
		}
		routes = append(routes, found...)
	}
	return routes, len(routes) > 0
}

// liveDeclarations finds ident's declarations by parsing the Go files the search reads:
// its named files and globs, and under a directory the files the index last saw name it,
// since walking a tree is past the hook's budget.
func liveDeclarations(deps Dependencies, root, dir string, c hint.Invocation, ident string) []searchRoute {
	operands := invocationPaths(c)
	if len(operands) == 0 {
		operands = []string{"."}
	}
	var files []string
	fromIndex := map[string]bool{}
	for _, p := range operands {
		matches := []string{p}
		if strings.ContainsAny(p, "*?[") {
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			matches, _ = filepath.Glob(p)
		}
		for _, m := range matches {
			abs, rel, ok := workspacePath(root, dir, m)
			if !ok {
				continue
			}
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				sites, _ := deps.symbolSites(ident)
				for _, s := range sites {
					if underAny([]string{rel}, s.File) && path.Ext(s.File) == ".go" {
						files = append(files, s.File)
						fromIndex[s.File] = true
					}
				}
				continue
			}
			if path.Ext(rel) == ".go" {
				files = append(files, rel)
			}
		}
	}
	slices.Sort(files)
	var routes []searchRoute
	for _, rel := range slices.Compact(files) {
		entries, _, ok := goDecls(filepath.Join(root, filepath.FromSlash(rel)))
		if !ok {
			continue
		}
		for _, e := range entries {
			if e.name[strings.LastIndexByte(e.name, '.')+1:] == ident {
				routes = append(routes, searchRoute{name: ident, run: sedRangeCommand(rel, e.first, e.last), indexed: fromIndex[rel]})
			}
		}
	}
	return routes
}

// printClause is the commands, one per declaration, and the verb that agrees with them.
func printClause(routes []searchRoute) string {
	runs := make([]string, len(routes))
	for i, r := range routes {
		runs[i] = "`" + r.run + "`"
	}
	if len(routes) == 1 {
		return runs[0] + " prints"
	}
	return strings.Join(runs, ", ") + " print"
}

// denyGrepReader leads with the commands, since they are the whole correction.
func denyGrepReader(routes []searchRoute) string {
	return printClause(routes) + " the declaration whole, numbered, where a context count only guesses at its length.\n" +
		"With -A, -B or -C this search is a read, so the single-file allowance a plain search gets does not apply. " +
		"A search for where a name is USED, without a context flag, runs as before."
}

// fileSymbolVerdict advises refs for a search of named Go files for names the index
// vouches for. It lets the search run: a deny could only hand back the lines grep prints.
// Silent for a tree, a pipe, a file the index does not cover, or a flag that changes the
// question.
func fileSymbolVerdict(deps Dependencies, dir string, c hint.Invocation) (ShellVerdict, bool) {
	sc, ok := parseSearchCall(c)
	if !ok || sc.readsStdin() || len(sc.paths) == 0 || deps.scope.root == "" {
		return ShellVerdict{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return ShellVerdict{}, false
	}
	for _, p := range sc.paths {
		abs, rel, ok := workspacePath(root, dir, p)
		if !ok || path.Ext(rel) != ".go" {
			return ShellVerdict{}, false
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return ShellVerdict{}, false
		}
	}
	routes, ok := provableRoutes(deps, c)
	if !ok {
		return ShellVerdict{}, false
	}
	if _, ok := sc.lineRegexp(); !ok {
		return ShellVerdict{}, false
	}
	if stale := graphMoved(deps); stale.reason != "" {
		return stale.verdict(), true
	}
	return ShellVerdict{
		Context: routeClause(routes) + " this for every file, checked against the tree, including the generated and cross-language sites a pattern misses. " +
			"The search runs as typed.",
		Kind:  advisoryPrecedent,
		Brief: "magus workspace: " + routeClause(routes) + " this for every file.",
	}, true
}

// treeSymbolVerdict denies a search of the tree for names the index vouches for, and
// carries the index's own answer when it has one: every site under the searched paths.
// A pipe after the search is named as not reproduced; the answer is never filtered.
func treeSymbolVerdict(deps Dependencies, dir string, c hint.Invocation, js searchJudgment, piped bool) ShellVerdict {
	routes := js.routes
	textNote, textNext := textRemedy(c, js)
	v := ShellVerdict{Deny: denySymbolSearch(routes) + "\n" + classifiedLine(js) + textNote, Rule: denyRule{Name: denyRuleSymbolSearch, Arg: routeNames(routes)}}
	if answer, ok := treeSymbolAnswer(deps, dir, c, routes); ok {
		v.Deny += "\n" + answerBlock("Its answer", answer) + pipeNote(piped)
		// The answer is already in the deny, and a lead would drop it.
		return v
	}
	v.Deny += pipeNote(piped)
	next := routeNexts(routes)
	if len(next) > 0 {
		next = append(next, textNext...)
	}
	return v.withRemedy(routeClause(routes)+" this exactly, checked against the tree rather than matched against it. "+classifiedLine(js)+textNote, next...)
}

// routeNexts serves each route as a remedy, or none when one of them cannot be.
func routeNexts(routes []searchRoute) []hint.Next {
	next := make([]hint.Next, 0, len(routes))
	for _, r := range routes {
		argv := servedArgv(r.run)
		if len(argv) == 0 {
			return nil
		}
		next = append(next, hint.NextForDenyRemedy(string(denyRuleSymbolSearch), argv,
			"the index holds every definition and use, generated and cross-language ones included."))
	}
	return next
}

func isDiagnosticRoute(r searchRoute) bool {
	return strings.HasPrefix(r.name, types.KindDiagnostic+":")
}

// treeSymbolAnswer is the sites the index holds for every name searched, under the paths
// searched: one line per file with its count and lines, as `magus refs` prints them.
func treeSymbolAnswer(deps Dependencies, dir string, c hint.Invocation, routes []searchRoute) ([]string, bool) {
	sc, ok := parseSearchCall(c)
	if !ok || deps.scope.root == "" {
		return nil, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return nil, false
	}
	searched, ok := sc.searchedRels(root, dir)
	if !ok {
		return nil, false
	}
	type site struct {
		rel   string
		count int
		lines []int
	}
	var sites []site
	for _, r := range routes {
		if isDiagnosticRoute(r) {
			return nil, false
		}
		found, definitive := deps.symbolSites(r.name)
		if !definitive {
			return nil, false
		}
		for _, f := range found {
			if !underAny(searched, f.File) || !sc.includes(f.File) {
				continue
			}
			sites = append(sites, site{rel: f.File, count: f.Count, lines: f.Lines})
		}
	}
	slices.SortFunc(sites, func(a, b site) int { return strings.Compare(a.rel, b.rel) })
	merged := sites[:0]
	for _, s := range sites {
		if n := len(merged); n > 0 && merged[n-1].rel == s.rel {
			merged[n-1].count += s.count
			merged[n-1].lines = append(merged[n-1].lines, s.lines...)
			continue
		}
		merged = append(merged, s)
	}
	answer := make([]string, 0, len(merged))
	for _, s := range merged {
		slices.Sort(s.lines)
		answer = append(answer, s.rel+"  ("+strconv.Itoa(s.count)+")  lines "+joinInts(s.lines))
	}
	return answer, true
}

// searchedRels are the workspace-relative paths sc searches, the call's directory when it
// names none, reporting false for a path outside the workspace, a glob, or a missing one.
func (sc searchCall) searchedRels(root, dir string) ([]string, bool) {
	paths := sc.paths
	if len(paths) == 0 {
		paths = []string{"."}
	}
	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		_, rel, ok := workspacePath(root, dir, p)
		if !ok {
			return nil, false
		}
		rels = append(rels, rel)
	}
	return rels, true
}

// underAny reports whether rel is one of dirs or lies inside one of them.
func underAny(dirs []string, rel string) bool {
	return slices.ContainsFunc(dirs, func(d string) bool {
		return d == "." || rel == d || strings.HasPrefix(rel, d+"/")
	})
}

// includes reports whether a file's name passes the search's --include globs.
func (sc searchCall) includes(rel string) bool {
	if len(sc.include) == 0 {
		return true
	}
	return slices.ContainsFunc(sc.include, func(g string) bool {
		matched, err := path.Match(g, path.Base(rel))
		return err == nil && matched
	})
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// selectedLines is each line of file that re selects.
func selectedLines(file string, re *regexp.Regexp) ([]hit, bool) {
	f, err := os.Open(file)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var out []hit
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; s.Scan(); n++ {
		if re.MatchString(s.Text()) {
			out = append(out, hit{line: n, text: s.Text()})
		}
	}
	return out, s.Err() == nil
}

// answerCap bounds an inline answer, so a wide pattern's deny stays readable.
const answerCap = 20

// answerBlock renders the answer a denied search would have got, bounded, so the deny
// costs the reader nothing. An empty answer is said outright: a hit grep would not have
// printed either.
func answerBlock(title string, items []string) string {
	if len(items) == 0 {
		return title + ": nothing."
	}
	var b strings.Builder
	b.WriteString(title + " (" + countNoun(len(items), "result") + "):\n")
	for i, it := range items {
		if i == answerCap {
			b.WriteString("  ... " + strconv.Itoa(len(items)-answerCap) + " more\n")
			break
		}
		b.WriteString("  " + it + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// pipeConsumers are the programs that read a search's output on a pipe.
var pipeConsumers = map[string]bool{
	"head": true, "tail": true, "wc": true, "sort": true, "uniq": true, "cut": true, "tr": true, "sed": true,
	"awk": true, "gawk": true, "grep": true, "egrep": true, "fgrep": true, "rg": true, "cat": true,
	"xargs": true, "tee": true, "less": true, "more": true, "jq": true, "column": true, "nl": true, "tac": true,
}

// pipedInto reports whether the command after a search reads its output. The command list
// is flat, so one after `;` reads the same; the deny then over-reports a pipe, never an
// answer.
func pipedInto(rest []hint.Invocation) bool {
	return len(rest) > 0 && pipeConsumers[path.Base(rest[0].Name)]
}

// pipeNote says a deny's answer is the graph command's alone. Reproducing a filter's
// output would substitute a model of sort, sed or awk for the tool, and a model diverges.
func pipeNote(piped bool) string {
	if !piped {
		return ""
	}
	return "\nThe pipe after the search is not reproduced: run it over the command's output."
}

// asSearch reads `git grep` as the recursive grep it is, so its patterns and paths are
// judged by the same parser. Every other command is returned unchanged, and so is a `git
// grep` relocated by -C, --git-dir or --work-tree, which searches a tree this workspace's
// graph does not describe, or one carrying -c, which can change the pattern dialect.
func asSearch(c hint.Invocation) hint.Invocation {
	if path.Base(c.Name) != "git" {
		return c
	}
	g := parseGit(c.Args)
	if g.sub != "grep" || len(g.dirs) > 0 || g.opaque || g.configured {
		return c
	}
	return hint.Invocation{Name: "grep", Args: append([]string{"-r"}, g.rest...)}
}

// provableRoutes answers every alternative of c's patterns with a graph command, or
// reports false when any one of them has none. A case-insensitive search is never
// provable: the graph matches case exactly, so it would answer a narrower question.
func provableRoutes(deps Dependencies, c hint.Invocation) ([]searchRoute, bool) {
	if hasFlag(c.Args, 'i', "ignore-case") {
		return nil, false
	}
	patterns := hint.Patterns(c)
	if len(patterns) == 0 {
		return nil, false
	}
	mode := searchMode(c)
	var routes []searchRoute
	for _, p := range patterns {
		for _, alt := range splitAlternation(p, mode) {
			r, ok := provableRoute(deps, normalizeAlternative(alt))
			if !ok {
				return nil, false
			}
			if !slices.Contains(routes, r) {
				routes = append(routes, r)
			}
		}
	}
	return routes, len(routes) > 0
}

// regexMode is how a search tool reads its pattern, which decides what separates two
// alternatives.
type regexMode int

const (
	regexBasic    regexMode = iota // `\|` separates
	regexExtended                  // `|` separates
	regexFixed                     // nothing does
)

func searchMode(c hint.Invocation) regexMode {
	name := path.Base(c.Name)
	switch {
	case name == "fgrep" || hasFlag(c.Args, 'F', "fixed-strings"),
		name == "ag" && hasFlag(c.Args, 'Q', "literal"):
		return regexFixed
	case name == "egrep" || name == "rg" || name == "ag":
		return regexExtended
	// rg's -E names an encoding, so only grep's spelling reaches here.
	case hasFlag(c.Args, 'E', "extended-regexp") || hasFlag(c.Args, 'P', "perl-regexp"):
		return regexExtended
	}
	return regexBasic
}

func splitAlternation(p string, mode regexMode) []string {
	switch mode {
	case regexBasic:
		return strings.Split(p, `\|`)
	case regexExtended:
		var out []string
		start := 0
		for i := 0; i < len(p); i++ {
			switch p[i] {
			case '\\':
				i++
			case '|':
				out = append(out, p[start:i])
				start = i + 1
			}
		}
		return append(out, p[start:])
	}
	return []string{p}
}

// whitespaceClassRe matches the regex spellings of "some whitespace", so `func\s+Foo`
// reads as the definition lookup it is.
var whitespaceClassRe = regexp.MustCompile(`(?:\\s|\[\[:space:\]\]| )[+*]?`)

// normalizeAlternative strips the anchors and word boundaries that narrow a match without
// changing which name it looks for, and a trailing call paren.
func normalizeAlternative(alt string) string {
	alt = strings.TrimSpace(whitespaceClassRe.ReplaceAllString(alt, " "))
	for {
		before := alt
		for _, prefix := range []string{"^", `\b`, `\<`} {
			alt = strings.TrimPrefix(alt, prefix)
		}
		for _, suffix := range []string{"$", `\b`, `\>`, `\(`, "(", " "} {
			alt = strings.TrimSuffix(alt, suffix)
		}
		if alt == before {
			return strings.TrimSpace(alt)
		}
	}
}

// altKind is what one alternative of a search pattern looks for.
type altKind int

const (
	altText       altKind = iota
	altName               // a name: a call, a member, a word search, or an identifier's shape
	altDefinition         // a declaration lookup: func X, type X, class X, def X
	altDiagnostic
)

// searchAlt is one alternative of a search's patterns, classified by its syntax alone. Whether
// a name is a symbol is the index's call, made in judgeSearch.
type searchAlt struct {
	raw   string
	ident string
	kind  altKind
	// strong reports a name whose own shape says identifier (CamelCase, snake_case, or a
	// declaration keyword before it), which a stale index may vouch for before it has
	// indexed it. A plain word is as likely prose, so only a current index may call it one.
	strong bool
	// explicit reports syntax that can only be naming a symbol, a declaration keyword or a
	// call of a CamelCase name: the one shape a stale index may route before indexing it,
	// since it is the name a branch is adding. A bare CamelCase word is as often a string
	// constant's value or a hook event name.
	explicit bool
	how      string // the syntax that decided it, said in the deny
}

// declLookupRe is a declaration lookup in the languages a symbol index covers: the keyword
// is what separates a lookup from prose.
// Buzz's `fun`, `object` and `protocol` are absent: no symbol index reads Buzz, so refs
// would answer with a Go or TypeScript symbol that merely shares the name.
var declLookupRe = regexp.MustCompile(`^(?:export |pub |async )?(?:func|type|class|def|function|fn|interface|struct|enum|trait|const|var|let) (?:\\?\([^()]*\\?\) ?)?([A-Za-z_][A-Za-z0-9_]*)(?: (?:struct|interface)\b.*)?$`)

// fileExts are the trailing segments that make `a.b` a file name rather than a member.
var fileExts = map[string]bool{
	"go": true, "buzz": true, "md": true, "yaml": true, "yml": true, "json": true, "jsonl": true, "ts": true,
	"tsx": true, "js": true, "toml": true, "txt": true, "sh": true, "py": true, "css": true, "html": true,
	"lock": true, "mod": true, "sum": true, "proto": true, "log": true, "png": true, "svg": true,
}

// searchAlts splits c's patterns into alternatives and classifies each. A pattern whose
// alternation sits inside a group is one regular expression, so it stays whole: splitting
// it would read `kg\.\(AddNode` as a name.
func searchAlts(c hint.Invocation) []searchAlt {
	mode := searchMode(c)
	word := hasFlag(c.Args, 'w', "word-regexp")
	var out []searchAlt
	for _, p := range hint.Patterns(c) {
		parts := splitAlternation(p, mode)
		if len(parts) > 1 && slices.ContainsFunc(parts, func(a string) bool { return !groupsBalanced(a, mode) }) {
			parts = []string{p}
		}
		for _, a := range parts {
			out = append(out, classifyAlternative(a, mode, word))
		}
	}
	return out
}

// groupsBalanced reports whether alt opens as many regex groups as it closes, ignoring a
// trailing call paren, which is a literal the name is followed by.
func groupsBalanced(alt string, mode regexMode) bool {
	open, closeTok := "(", ")"
	if mode == regexBasic {
		open, closeTok = `\(`, `\)`
	}
	if mode == regexFixed {
		return true
	}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(alt, `\(`), "(")
	depth := 0
	for i := 0; i < len(trimmed); i++ {
		switch {
		case mode != regexBasic && trimmed[i] == '\\':
			i++
		case strings.HasPrefix(trimmed[i:], open):
			depth++
			i += len(open) - 1
		case strings.HasPrefix(trimmed[i:], closeTok):
			depth--
			i += len(closeTok) - 1
		}
	}
	return depth == 0
}

// classifyAlternative reads one alternative as a declaration lookup, a diagnostic code, a
// name, or text, and says which syntax decided it.
func classifyAlternative(raw string, mode regexMode, word bool) searchAlt {
	a := searchAlt{raw: raw, kind: altText}
	norm := normalizeAlternative(raw)
	if mode == regexFixed {
		norm = strings.TrimSpace(raw)
	}
	if diagnosticCodeRe.MatchString(norm) {
		a.kind, a.ident, a.how = altDiagnostic, norm, "a diagnostic code"
		return a
	}
	if m := declLookupRe.FindStringSubmatch(norm); len(m) > 1 && hint.IsIdentifier(m[1]) {
		a.ident = m[1]
		// `func main`, `function field`: a lowercase word names so many declarations that
		// refs, which resolves one, would answer a narrower question.
		if lowerWordRe.MatchString(a.ident) {
			a.how = "a declaration lookup of a lowercase word many declarations share"
			return a
		}
		a.kind, a.strong, a.explicit, a.how = altDefinition, true, true, "a declaration lookup"
		return a
	}
	core, how := strings.TrimSpace(raw), ""
	if word {
		how = "a word search (-w)"
	}
	if mode != regexFixed {
		core = strings.TrimPrefix(strings.TrimSpace(whitespaceClassRe.ReplaceAllString(core, " ")), "^")
		for _, b := range []string{`\b`, `\<`} {
			if strings.HasPrefix(core, b) {
				core, how = strings.TrimPrefix(core, b), "a word search"
			}
		}
		for _, b := range []string{`\b`, `\>`, "$"} {
			if strings.HasSuffix(core, b) {
				core = strings.TrimSuffix(core, b)
				if b != "$" {
					how = "a word search"
				}
			}
		}
	}
	call := false
	for _, paren := range []string{`\(`, "("} {
		if strings.HasSuffix(core, paren) {
			core, how, call = strings.TrimSuffix(core, paren), "a call", true
			break
		}
	}
	for _, assign := range []string{" :=", " =", ":=", "="} {
		if strings.HasSuffix(core, assign) {
			core, how = strings.TrimSpace(strings.TrimSuffix(core, assign)), "an assignment"
			break
		}
	}
	qualifier, member, qualified := memberName(core)
	if qualified {
		core = member
		if !call {
			how = "a member access"
		}
	}
	if !hint.IsIdentifier(core) {
		a.how = "a regular expression or text, not a name"
		return a
	}
	a.ident = core
	// CamelCase only: in this tree's languages a snake_case word is a config key, a JSON tag
	// or a Buzz function, none of which a symbol index holds.
	a.strong = len(core) >= precedentIdentMin && camelIdentRe.MatchString(core)
	exported := core[0] >= 'A' && core[0] <= 'Z'
	switch {
	case qualified && stdlibQualifiers[qualifier]:
		a.how = "a member of the standard library (" + qualifier + "), which this workspace's index does not define"
		return a
	case a.strong:
		a.explicit = call
		if how == "" {
			how = "an identifier's shape"
		}
	case how == "":
		a.how = "a plain word, as likely prose as a name"
		return a
	case call || !exported:
		// `Mkdir(` is os.Mkdir as often as a local function, and `ctx.glob(` a host method:
		// a short or lowercase name is a symbol only when its shape says so.
		a.how = how + " of a name too common to pin to one symbol"
		return a
	}
	a.how = how
	a.kind = altName
	return a
}

// lowerWordRe is a single lowercase word, the declaration names refs cannot pin to one.
var lowerWordRe = regexp.MustCompile(`^[a-z]+$`)

// camelIdentRe is a CamelCase identifier: a lowercase letter or digit followed by a capital.
var camelIdentRe = regexp.MustCompile(`^[A-Za-z0-9]*[a-z0-9][A-Z][A-Za-z0-9]*$`)

// stdlibQualifiers are the standard-library packages and globals a member access through
// them selects: the name belongs to the language, and refs would resolve a workspace symbol
// that merely shares it.
var stdlibQualifiers = map[string]bool{
	"os": true, "io": true, "fmt": true, "strings": true, "strconv": true, "bytes": true, "filepath": true,
	"path": true, "time": true, "json": true, "http": true, "url": true, "context": true, "errors": true,
	"sort": true, "sync": true, "atomic": true, "regexp": true, "exec": true, "slices": true, "maps": true,
	"bufio": true, "log": true, "slog": true, "math": true, "rand": true, "reflect": true, "runtime": true,
	"signal": true, "syscall": true, "testing": true, "unicode": true, "utf8": true, "hex": true,
	"base64": true, "net": true, "ioutil": true, "fs": true, "template": true, "flag": true,
	"ast": true, "token": true, "parser": true, "printer": true, "scanner": true, "cmp": true, "iter": true,
	"binary": true, "big": true, "bits": true, "sha256": true, "crypto": true, "tls": true, "gzip": true,
	"zip": true, "tar": true, "csv": true, "xml": true, "html": true, "mime": true, "embed": true,
	"unsafe": true, "httptest": true, "fstest": true,
	"Object": true, "Math": true, "JSON": true, "console": true, "process": true, "Promise": true,
}

// memberName splits a qualified name, `opts.Jobs`, `\.Member` or `s.lim.Acquire`, into the
// segment before the member and the member, reporting false for anything else, a file name
// such as `queue.yaml` among them.
func memberName(core string) (qualifier, member string, ok bool) {
	s := strings.ReplaceAll(core, `\.`, ".")
	if !strings.Contains(s, ".") {
		return "", "", false
	}
	segs := strings.Split(strings.TrimPrefix(s, "."), ".")
	last := segs[len(segs)-1]
	if fileExts[strings.ToLower(last)] {
		return "", "", false
	}
	for _, seg := range segs {
		if seg == "" || !segmentRe.MatchString(seg) {
			return "", "", false
		}
	}
	if len(segs) > 1 {
		qualifier = segs[len(segs)-2]
	}
	return qualifier, last, true
}

var segmentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// searchJudgment is what the index made of one search's alternatives.
type searchJudgment struct {
	routes []searchRoute
	// stale reports a route the index could not vouch for as current, so the deny serves a
	// rebuild before the graph command.
	stale bool
	texts []searchAlt // the alternatives that are text, served on their own
	// classified is one clause per alternative naming what it was taken for and why, so a
	// false positive is disputable from the deny alone.
	classified []string
	// noGraph names a name that would have routed to the graph, when the workspace has no
	// symbol index at all to route it to.
	noGraph string
	folded  bool
}

// judgeSearch decides, alternative by alternative, which ones the graph answers. A current
// index decides outright. A stale one still vouches for the names it holds, and for a
// declaration lookup or a call it has not indexed yet, since that is the name a branch is
// adding; anything else it does not hold stays text. With no index at all nothing routes.
func judgeSearch(deps Dependencies, c hint.Invocation) searchJudgment {
	js := searchJudgment{folded: hasFlag(c.Args, 'i', "ignore-case")}
	var built *bool
	indexBuilt := func() bool {
		if built == nil {
			b := symbolIndexBuilt(deps)
			built = &b
		}
		return *built
	}
	text := func(a searchAlt, why string) {
		js.texts = append(js.texts, a)
		js.classified = append(js.classified, "`"+a.raw+"` is text ("+why+")")
	}
	route := func(a searchAlt, r searchRoute, why string) {
		if !slices.Contains(js.routes, r) {
			js.routes = append(js.routes, r)
		}
		js.classified = append(js.classified, "`"+a.raw+"` is a name ("+why+")")
	}
	for _, a := range searchAlts(c) {
		switch a.kind {
		case altText:
			text(a, a.how)
			continue
		case altDiagnostic:
			if codes, ok := graphDiagnostics(deps); ok && slices.Contains(codes, a.ident) && !js.folded {
				node := string(types.KindDiagnostic) + ":" + a.ident
				route(a, searchRoute{name: node, run: hint.Explain.With(node), indexed: true}, "a diagnostic code with a graph node")
			} else {
				text(a, "a code the graph holds no node for")
			}
			continue
		}
		if js.folded && !a.strong {
			text(a, a.how+", widened by -i to text in any case")
			continue
		}
		r := searchRoute{name: a.ident, run: hint.Refs.With(a.ident, "--occurrences"), indexed: true}
		if a.kind == altDefinition {
			// A declaration lookup wants the body next; --source prints it in place of the
			// grep-then-sed pair.
			r.run = hint.Refs.With(a.ident, "--definition", "--source")
		}
		defined, definitive := deps.symbolDefined(a.ident)
		switch {
		case definitive && defined:
			route(a, r, a.how+", defined in the index")
		case definitive:
			text(a, a.how+", but the index holds no symbol by that name")
		case defined:
			js.stale = true
			route(a, r, a.how+", defined in an index behind the tree")
		case a.explicit && indexBuilt():
			js.stale = true
			route(a, r, a.how+", not indexed yet")
		case !indexBuilt() && js.noGraph == "":
			js.noGraph = a.ident
			text(a, a.how+", with no symbol index to ask")
		default:
			text(a, a.how+", not in the index")
		}
	}
	return js
}

// symbolIndexBuilt reports whether `magus graph build` has written a symbol index for this
// workspace at all, current or not.
func symbolIndexBuilt(deps Dependencies) bool {
	if deps.scope.root == "" {
		return false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return false
	}
	cacheDir, err := deps.cacheDir(root)
	if err != nil {
		return false
	}
	_, err = knowledge.ReadGuardIndexCheckout(cacheDir, root)
	return err == nil
}

// symbolIndexCause is why the symbol index cannot vouch for every site, as a clause
// completing "the symbol index ...". It is the one place that cause is worded.
func symbolIndexCause(moved staleGraph) string {
	if moved.reason != "" {
		return "describes another tree (" + moved.reason + ")"
	}
	return "is older than the sources it covers"
}

// indexCauseNote is what deps observed about why the index fell behind and what keeps it
// current, on a line of its own, or "" when it observed nothing within its budget.
func indexCauseNote(deps Dependencies) string {
	if deps.IndexCause == nil {
		return ""
	}
	if cause := deps.IndexCause(); cause != "" {
		return "\n" + cause
	}
	return ""
}

// classifiedLine is the deny's account of each alternative.
func classifiedLine(js searchJudgment) string {
	return "Classified: " + strings.Join(js.classified, "; ") + "."
}

// textRemedy serves each text alternative as the literal search magus runs, when it is a
// literal; a regular expression is named, not translated.
func textRemedy(c hint.Invocation, js searchJudgment) (string, []hint.Next) {
	if len(js.texts) == 0 {
		return "", nil
	}
	sc, _ := parseSearchCall(c)
	paths := invocationPaths(c)
	if sc.tool != "" {
		paths = sc.paths
	}
	var names []string
	var next []hint.Next
	for _, t := range js.texts {
		names = append(names, "`"+t.raw+"`")
		lit := t.raw
		if searchMode(c) != regexFixed {
			lit = strings.ReplaceAll(lit, `\.`, ".")
		}
		if js.folded || strings.ContainsAny(lit, `\^$*+?()[]{}|`) || strings.TrimSpace(lit) == "" {
			continue
		}
		argv := append([]string{hint.BinaryName(), "refs", "--text", lit}, paths...)
		next = append(next, hint.NextForDenyRemedy(string(denyRuleSymbolSearch), argv, "a literal search over the same paths, for the text the graph does not hold."))
	}
	return "\nThe text alternatives (" + strings.Join(names, ", ") + ") are not symbols: search them on their own.", next
}

// staleSymbolVerdict denies a search for names a stale index vouches for, serving the
// rebuild and then the graph command for each name.
func staleSymbolVerdict(deps Dependencies, c hint.Invocation, js searchJudgment, moved staleGraph, piped bool) ShellVerdict {
	build := hint.GraphBuild.With("--silent")
	textNote, textNext := textRemedy(c, js)
	v := ShellVerdict{
		Deny: "`" + build + "`, then " + routeClause(js.routes) + " this exactly.\n" +
			classifiedLine(js) + "\n" +
			"The symbol index " + symbolIndexCause(moved) + ", so it is rebuilt first; refs then checks every site against the tree, the generated and cross-language ones a pattern misses included." +
			indexCauseNote(deps) + textNote + pipeNote(piped),
		Rule: denyRule{Name: denyRuleSymbolSearch, Arg: routeNames(js.routes)},
	}
	argv := servedArgv(build)
	if len(argv) == 0 {
		return v
	}
	next := []hint.Next{hint.NextForDenyRemedy(string(denyRuleSymbolSearch), argv, "brings the symbol index up to the tree, so the next command answers exactly.")}
	routes := routeNexts(js.routes)
	if len(routes) == 0 {
		return v
	}
	next = append(append(next, routes...), textNext...)
	// The lead replaces the deny when the remedy is served, so it carries the classification:
	// it is what makes a false positive disputable.
	return v.withRemedy("Rebuild the index, then "+routeClause(js.routes)+" this exactly. "+classifiedLine(js)+textNote, next...)
}

// noGraphVerdict advises a search for a name in a workspace with no symbol index to route
// it to: the one case the rule fails open, since a deny there would route nowhere.
func noGraphVerdict(ident string) ShellVerdict {
	return ShellVerdict{
		Context: fmt.Sprintf(precedentSearchAdvice, ident, ident),
		Kind:    advisoryPrecedent,
		Brief: "magus workspace: no symbol index exists yet, so this search runs. `" + hint.GraphBuild.With("--silent") +
			"` builds one, then `" + hint.Refs.With(ident, "--occurrences") + "` answers exactly, and this search is refused.",
	}
}

// definitionLookupRe matches `func X`, `func (r *T) X` and `type X`, receiver escaped or
// not. The keyword is what separates a lookup from prose, so X needs only an identifier's
// shape rather than precedentIdentRe's.
var definitionLookupRe = regexp.MustCompile(`^(?:func|type) (?:\\?\([^()]*\\?\) ?)?([A-Za-z_][A-Za-z0-9_]*)$`)

// diagnosticCodeRe matches a diagnostic code. A BZZ code is recognized so it is never
// mistaken for a symbol, and never provable: the graph carries no node for one.
var diagnosticCodeRe = regexp.MustCompile(`^(?:MGS|BZZ)[0-9]{4}$`)

// graphDiagnostics are the codes of the graph's diagnostic nodes, definitive on graphIDs'
// terms. They come from the graph and not this binary's catalog: the two differ once the
// checkout moves past the build the graph describes, as it does mid-rebase.
func graphDiagnostics(deps Dependencies) ([]string, bool) {
	ids, definitive := deps.graphIDs(context.Background(), types.KindDiagnostic)
	if !definitive {
		return nil, false
	}
	codes := make([]string, 0, len(ids))
	for _, id := range ids {
		if code, ok := strings.CutPrefix(id, types.KindDiagnostic+":"); ok {
			codes = append(codes, code)
		}
	}
	return codes, true
}

func provableRoute(deps Dependencies, alt string) (searchRoute, bool) {
	if diagnosticCodeRe.MatchString(alt) {
		if codes, ok := graphDiagnostics(deps); !ok || !slices.Contains(codes, alt) {
			return searchRoute{}, false
		}
		node := string(types.KindDiagnostic) + ":" + alt
		return searchRoute{name: node, run: hint.Explain.With(node), indexed: true}, true
	}
	var ident string
	definition := false
	switch m := definitionLookupRe.FindStringSubmatch(alt); {
	case len(m) > 1 && hint.IsIdentifier(m[1]):
		ident, definition = m[1], true
	case len(alt) >= precedentIdentMin && precedentIdentRe.MatchString(alt):
		ident = alt
	default:
		return searchRoute{}, false
	}
	if defined, definitive := deps.symbolDefined(ident); !defined || !definitive {
		return searchRoute{}, false
	}
	if definition {
		// A `func X` lookup wants the body next; --source prints it in place of the
		// grep-then-sed pair.
		return searchRoute{name: ident, run: hint.Refs.With(ident, "--definition", "--source"), indexed: true}, true
	}
	return searchRoute{name: ident, run: hint.Refs.With(ident, "--occurrences"), indexed: true}, true
}

func routeNames(routes []searchRoute) string {
	names := make([]string, len(routes))
	for i, r := range routes {
		names[i] = r.name
	}
	return strings.Join(names, ",")
}

// routeClause is the commands, one per name, and the verb that agrees with them.
func routeClause(routes []searchRoute) string {
	runs := make([]string, len(routes))
	for i, r := range routes {
		runs[i] = "`" + r.run + "`"
	}
	verb := "answers"
	if len(routes) > 1 {
		verb = "answer"
	}
	return strings.Join(runs, ", ") + " " + verb
}

// denySymbolSearch leads with the commands, one per name, since that is the whole
// correction.
func denySymbolSearch(routes []searchRoute) string {
	return routeClause(routes) + " this exactly, checked against the tree rather than matched against it.\n" +
		"Every name searched for is indexed here, so the graph knows every definition, reference and document, including the generated and cross-language ones a pattern misses. Search raw TEXT (a string literal, a comment, a config value) with grep as before: no index holds that, so nothing replaces it."
}
