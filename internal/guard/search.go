package guard

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The symbol-search rule: a text search whose every alternative the graph can answer
// exactly. An indexed symbol is the one search shape with an EXACT replacement, which is
// what makes denying it free: refs returns the same sites, column-precise and checked
// against the tree, plus the generated and cross-language ones a pattern cannot reach.
// Anything the index cannot vouch for stays an advisory, because a deny that routes
// nowhere takes a capability away. Raw text is the standing case there: a string literal
// or a comment body is not a symbol, so no index holds it.
//
// Measured 2026-09-24 over 14,773 search patterns: 45% were alternations and 13% were
// `func X` or `type X` definition lookups, and the single-identifier form this rule
// started with fired 0 times. Measured 2026-09-26 over 89,116 search calls: 8,500 looked
// for a symbol and most named one Go file (`grep -n Foo file.go`), a shape the rule then
// left alone as a read.

// searchRoute is the graph command that answers one alternative of a search.
type searchRoute struct {
	name string // the symbol or the diagnostic node, as the rule's Arg records it
	run  string
}

// searchVerdict judges the searches on a line against the index, reporting false when no
// search there is one it has anything to say about.
func searchVerdict(deps Dependencies, cmds []hint.Invocation) (ShellVerdict, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return ShellVerdict{}, false
	}
	return searchVerdictAt(deps, dir, cmds)
}

func searchVerdictAt(deps Dependencies, dir string, cmds []hint.Invocation) (ShellVerdict, bool) {
	var inScope []hint.Invocation
	for _, c := range cmds {
		c = asSearch(c)
		if !hint.IsSearchTool(c.Name) {
			continue
		}
		if v, ok := fileSymbolVerdict(deps, dir, c); ok {
			return v, true
		}
		if hint.Classify(c) != hint.ClassSearchSource {
			continue
		}
		// refs answers for THIS workspace, so a search of another tree has no replacement
		// here and nothing to be advised about.
		if allOutside(deps.scope, invocationPaths(c)) {
			continue
		}
		inScope = append(inScope, c)
		if routes, ok := provableRoutes(deps, c); ok {
			return ShellVerdict{Deny: denySymbolSearch(routes), Rule: denyRule{Name: denyRuleSymbolSearch, Arg: routeNames(routes)}}, true
		}
	}
	ident := precedentIdent(inScope)
	if ident == "" {
		return ShellVerdict{}, false
	}
	// The index could not vouch for ident, which is the ONLY reason this is advice rather
	// than a refusal. The long form already says so (searchColdIndexRouting), but the brief
	// is what a reader sees on every call, and "refs finds every use" reads as a preference
	// they may decline. Naming the staleness and the one command that clears it turns a
	// silent degradation into something actionable.
	//
	// It matters most on a branch that is ADDING symbols: the index lags exactly there, so
	// the rule is quietest on the code most likely to need it.
	if _, definitive := deps.symbolDefined(ident); !definitive {
		return ShellVerdict{
			Context: fmt.Sprintf(precedentSearchAdvice, ident, ident),
			Kind:    advisoryPrecedent,
			Brief: "magus workspace: the symbol index cannot vouch for " + ident + " yet, so this is advice and not a refusal. `" +
				hint.GraphBuild.String() + "` refreshes it, then `" + hint.Refs.With(ident, "--occurrences") + "` answers exactly.",
		}, true
	}
	return ShellVerdict{
		Context: fmt.Sprintf(precedentSearchAdvice, ident, ident),
		Kind:    advisoryPrecedent,
		Brief:   "magus workspace: `" + hint.Refs.With(ident, "--occurrences") + "` finds every use.",
	}, true
}

// fileSymbolVerdict denies a search of named Go files for names the index vouches for,
// with the lines the search would have printed, so the reader keeps the line numbers a
// bounded read needs and learns the query that has them for every file. Silent for a
// tree, a pipe, a file the index does not cover, or a flag that changes the question.
func fileSymbolVerdict(deps Dependencies, dir string, c hint.Invocation) (ShellVerdict, bool) {
	sc, ok := parseSearchCall(c)
	if !ok || sc.readsStdin() || len(sc.paths) == 0 || deps.scope.root == "" {
		return ShellVerdict{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return ShellVerdict{}, false
	}
	var files []string
	for _, p := range sc.paths {
		abs, rel, ok := workspacePath(root, dir, p)
		if !ok || path.Ext(rel) != ".go" {
			return ShellVerdict{}, false
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			return ShellVerdict{}, false
		}
		files = append(files, rel)
	}
	routes, ok := provableRoutes(deps, c)
	if !ok {
		return ShellVerdict{}, false
	}
	line, ok := sc.lineRegexp()
	if !ok {
		return ShellVerdict{}, false
	}
	var hits []string
	for _, rel := range files {
		lines, ok := selectedLines(filepath.Join(root, rel), line)
		if !ok {
			return ShellVerdict{}, false
		}
		for _, l := range lines {
			hits = append(hits, rel+":"+l)
		}
	}
	return ShellVerdict{
		Deny: denySymbolSearch(routes) + "\n" + answerBlock("What this search selects", hits),
		Rule: denyRule{Name: denyRuleSymbolSearch, Arg: routeNames(routes)},
	}, true
}

// selectedLines is what `grep -n` prints for file: `<line>:<text>` for each line re selects.
func selectedLines(file string, re *regexp.Regexp) ([]string, bool) {
	f, err := os.Open(file)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; s.Scan(); n++ {
		if re.MatchString(s.Text()) {
			out = append(out, strconv.Itoa(n)+":"+s.Text())
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

// definitionLookupRe matches `func X`, `func (r *T) X` and `type X`, receiver escaped or
// not. The keyword is what separates a lookup from prose, so X needs only an identifier's
// shape rather than precedentIdentRe's.
var definitionLookupRe = regexp.MustCompile(`^(?:func|type) (?:\\?\([^()]*\\?\) ?)?([A-Za-z_][A-Za-z0-9_]*)$`)

// diagnosticCodeRe matches a diagnostic code. A BZZ code is recognized so it is never
// mistaken for a symbol, and never provable: the graph carries no node for one.
var diagnosticCodeRe = regexp.MustCompile(`^(?:MGS|BZZ)[0-9]{4}$`)

// registeredDiagnostics are the codes the graph builds a diagnostic node for.
var registeredDiagnostics = sync.OnceValue(func() map[string]bool {
	out := map[string]bool{}
	for _, code := range types.AllDiagnosticCodes() {
		out[string(code)] = true
	}
	return out
})

func provableRoute(deps Dependencies, alt string) (searchRoute, bool) {
	if diagnosticCodeRe.MatchString(alt) {
		if !registeredDiagnostics()[alt] {
			return searchRoute{}, false
		}
		node := string(types.KindDiagnostic) + ":" + alt
		return searchRoute{name: node, run: hint.Explain.With(node)}, true
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
		return searchRoute{name: ident, run: hint.Refs.With(ident, "--definition", "--source")}, true
	}
	return searchRoute{name: ident, run: hint.Refs.With(ident, "--occurrences")}, true
}

func routeNames(routes []searchRoute) string {
	names := make([]string, len(routes))
	for i, r := range routes {
		names[i] = r.name
	}
	return strings.Join(names, ",")
}

// denySymbolSearch leads with the commands, one per name, since that is the whole
// correction.
func denySymbolSearch(routes []searchRoute) string {
	runs := make([]string, len(routes))
	for i, r := range routes {
		runs[i] = "`" + r.run + "`"
	}
	verb := "answers"
	if len(routes) > 1 {
		verb = "answer"
	}
	return strings.Join(runs, ", ") + " " + verb + " this exactly, checked against the tree rather than matched against it.\n" +
		"Every name searched for is indexed here, so the graph knows every definition, reference and document, including the generated and cross-language ones a pattern misses. Search raw TEXT (a string literal, a comment, a config value) with grep as before: no index holds that, so nothing replaces it."
}
