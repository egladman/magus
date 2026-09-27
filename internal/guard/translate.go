package guard

import (
	"bufio"
	"cmp"
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

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The search-translation rule compiles a pattern in the tool's dialect and runs it against
// the graph's ids at judge time, so a deny names a query that was checked. Short of that
// proof it stays silent. The catalog's Why lists the provable shapes and measurements.

// translatableTools are the search tools whose flags and dialects this rule models.
var translatableTools = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true}

// searchCall is one search invocation reduced to what decides its answer, and the print
// flags that decide how its rows look.
type searchCall struct {
	tool      string
	mode      regexMode
	perl      bool // `\d` is a digit class rather than grep's literal `d`
	patterns  []string
	paths     []string
	recursive bool
	word      bool
	include   []string // file globs from --include, -g or -t md
	exclude   bool

	lineNumbers  bool
	onlyMatching bool
	heading      bool // rg's grouped layout, which no row model reproduces
	filenames    int  // 1 forced by -H, -1 suppressed by -h, 0 the tool's default
}

// translateFlags are the flags that change how a search prints, never which lines it
// selects, so a translation of the pattern stays exact under them. Every other flag (-i,
// -v, -c, -l, -x, -A/-B/-C, -m, -f) asks a different question and keeps the rule silent.
var translateFlags = map[string]struct{ short, long string }{
	"grep": {short: "rRnHhsEFGPwoI", long: "recursive dereference-recursive line-number with-filename no-filename no-messages extended-regexp fixed-strings basic-regexp perl-regexp word-regexp only-matching color colour"},
	"rg":   {short: "nNHIsSwoFP", long: "line-number no-line-number with-filename no-filename no-heading heading case-sensitive smart-case word-regexp only-matching fixed-strings pcre2 color colour"},
}

// parseSearchCall reads c as a translatable search, or reports false when a flag it
// carries changes the question.
func parseSearchCall(c hint.Invocation) (searchCall, bool) {
	c = asSearch(c)
	tool := path.Base(c.Name)
	if !translatableTools[tool] {
		return searchCall{}, false
	}
	family := tool
	if family != "rg" {
		family = "grep"
	}
	allowed := translateFlags[family]
	longs := strings.Fields(allowed.long)
	sc := searchCall{tool: tool, recursive: tool == "rg"}
	var operands []string
	takeValue := func(flag string, v string) bool {
		switch flag {
		case "e", "regexp":
			sc.patterns = append(sc.patterns, v)
		case "include", "g", "glob":
			sc.include = append(sc.include, v)
		case "t", "type":
			if family != "rg" || v != "md" {
				return false
			}
			sc.include = append(sc.include, "*.md")
		case "exclude":
			sc.exclude = true
		default:
			return false
		}
		return true
	}
	valueShorts := "e"
	valueLongs := []string{"regexp", "include", "exclude"}
	if family == "rg" {
		valueShorts, valueLongs = "egt", []string{"regexp", "glob", "type"}
	}
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		switch {
		case a == "--":
			operands = append(operands, c.Args[i+1:]...)
			i = len(c.Args)
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a[2:], "=")
			switch {
			case slices.Contains(valueLongs, name):
				if !hasVal {
					if i+1 >= len(c.Args) {
						return searchCall{}, false
					}
					i++
					val = c.Args[i]
				}
				if !takeValue(name, val) {
					return searchCall{}, false
				}
			case slices.Contains(longs, name):
				sc.flag(name)
			default:
				return searchCall{}, false
			}
		case len(a) > 1 && a[0] == '-':
			cluster := a[1:]
			for j := 0; j < len(cluster); j++ {
				f := cluster[j]
				if strings.IndexByte(valueShorts, f) >= 0 {
					val := cluster[j+1:]
					if val == "" {
						if i+1 >= len(c.Args) {
							return searchCall{}, false
						}
						i++
						val = c.Args[i]
					}
					if !takeValue(string(f), val) {
						return searchCall{}, false
					}
					break
				}
				if strings.IndexByte(allowed.short, f) < 0 {
					return searchCall{}, false
				}
				sc.flag(string(f))
			}
		default:
			operands = append(operands, a)
		}
	}
	if len(sc.patterns) == 0 {
		if len(operands) == 0 {
			return searchCall{}, false
		}
		sc.patterns, operands = operands[:1], operands[1:]
	}
	sc.paths = operands
	sc.mode = searchMode(c)
	sc.perl = family == "rg" || hasFlag(c.Args, 'P', "perl-regexp")
	return sc, true
}

func (sc *searchCall) flag(name string) {
	switch name {
	case "r", "R", "recursive", "dereference-recursive":
		sc.recursive = true
	case "w", "word-regexp":
		sc.word = true
	case "n", "line-number":
		sc.lineNumbers = true
	case "N", "no-line-number":
		sc.lineNumbers = false
	case "o", "only-matching":
		sc.onlyMatching = true
	case "H", "with-filename":
		sc.filenames = 1
	case "h", "no-filename":
		sc.filenames = -1
	case "heading":
		sc.heading = true
	}
}

// hit is one line a search selects, by workspace-relative file.
type hit struct {
	rel  string
	line int
	text string
}

// pathSpeller renders a workspace-relative file the way the tool prints it: the operand as
// written plus the remainder under it, or, for a search with no operand, the path relative
// to the working directory.
type pathSpeller struct {
	dirRel string
	ops    []spelledOperand
}

type spelledOperand struct {
	written, rel string
	isDir        bool
}

// speller resolves each operand of sc, reporting false for one outside the workspace, a
// glob, or a path that does not exist.
func (sc searchCall) speller(root, dir string) (pathSpeller, bool) {
	_, dirRel, ok := workspacePath(root, dir, ".")
	if !ok {
		return pathSpeller{}, false
	}
	ps := pathSpeller{dirRel: dirRel}
	for _, p := range sc.paths {
		abs, rel, ok := workspacePath(root, dir, p)
		if !ok {
			return pathSpeller{}, false
		}
		info, err := os.Stat(abs)
		if err != nil {
			return pathSpeller{}, false
		}
		ps.ops = append(ps.ops, spelledOperand{written: p, rel: rel, isDir: info.IsDir()})
	}
	return ps, true
}

// spell reports how the tool prints rel, and false when rel is under none of the operands.
func (ps pathSpeller) spell(rel string) (string, bool) {
	if len(ps.ops) == 0 {
		if ps.dirRel == "." {
			return rel, true
		}
		rest, ok := strings.CutPrefix(rel, ps.dirRel+"/")
		return rest, ok
	}
	for _, op := range ps.ops {
		if rel == op.rel {
			return op.written, true
		}
		var rest string
		var ok bool
		if op.rel == "." {
			rest, ok = rel, true
		} else {
			rest, ok = strings.CutPrefix(rel, op.rel+"/")
		}
		if ok && op.isDir {
			return strings.TrimSuffix(op.written, "/") + "/" + rest, true
		}
	}
	return "", false
}

// searchesDir reports an operand set the tool walks rather than reads as named files.
func (ps pathSpeller) searchesDir() bool {
	return len(ps.ops) == 0 || slices.ContainsFunc(ps.ops, func(op spelledOperand) bool { return op.isDir })
}

// rows renders hits as the tool prints them. false is a layout this does not model.
func (sc searchCall) rows(ps pathSpeller, hits []hit) ([]string, bool) {
	if sc.heading {
		return nil, false
	}
	showFile := sc.filenames > 0
	if sc.filenames == 0 {
		showFile = len(sc.paths) > 1 || (sc.tool == "rg" && ps.searchesDir()) || (sc.tool != "rg" && sc.recursive)
	}
	var line *regexp.Regexp
	if sc.onlyMatching {
		var ok bool
		if line, ok = sc.lineRegexp(); !ok {
			return nil, false
		}
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		prefix := ""
		if showFile {
			spelled, ok := ps.spell(h.rel)
			if !ok {
				return nil, false
			}
			prefix = spelled + ":"
		}
		if sc.lineNumbers {
			prefix += strconv.Itoa(h.line) + ":"
		}
		if !sc.onlyMatching {
			out = append(out, prefix+h.text)
			continue
		}
		for _, m := range line.FindAllString(h.text, -1) {
			out = append(out, prefix+m)
		}
	}
	return out, true
}

// readsStdin reports a search with no operand that reads the pipe rather than the tree.
func (sc searchCall) readsStdin() bool { return len(sc.paths) == 0 && !sc.recursive }

// alternatives are the pattern's alternatives, each already in Go's regexp syntax.
func (sc searchCall) alternatives() ([]string, bool) {
	var out []string
	for _, p := range sc.patterns {
		for _, alt := range splitAlternation(p, sc.mode) {
			re, ok := toGoRegexp(alt, sc.mode, sc.perl)
			if !ok || re == "" {
				return nil, false
			}
			out = append(out, re)
		}
	}
	return out, len(out) > 0
}

// lineRegexp is what the tool matches each line against.
func (sc searchCall) lineRegexp() (*regexp.Regexp, bool) {
	alts, ok := sc.alternatives()
	if !ok {
		return nil, false
	}
	expr := "(?:" + strings.Join(alts, ")|(?:") + ")"
	if sc.word {
		expr = `\b(?:` + expr + `)\b`
	}
	re, err := regexp.Compile(expr)
	return re, err == nil
}

// toGoRegexp rewrites one alternative from the tool's dialect into Go's. It reports false
// for a construct whose meaning differs between the two and that it does not model, a
// backreference among them.
func toGoRegexp(p string, mode regexMode, perl bool) (string, bool) {
	if mode == regexFixed {
		return regexp.QuoteMeta(p), true
	}
	basic := mode == regexBasic
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '[':
			end, ok := bracketEnd(p, i)
			if !ok {
				return "", false
			}
			body := p[i:end]
			if !perl {
				// A POSIX bracket takes a backslash literally; Go reads it as an escape.
				body = strings.ReplaceAll(body, `\`, `\\`)
			}
			b.WriteString(body)
			i = end - 1
		case c == '\\':
			if i+1 >= len(p) {
				return "", false
			}
			i++
			e := p[i]
			switch {
			case basic && strings.IndexByte("|(){}+?", e) >= 0:
				b.WriteByte(e)
			case e == '<' || e == '>':
				b.WriteString(`\b`)
			case strings.IndexByte("bBwWsS", e) >= 0:
				b.WriteByte('\\')
				b.WriteByte(e)
			case e == 'd' || e == 'D':
				if !perl {
					return "", false
				}
				b.WriteByte('\\')
				b.WriteByte(e)
			case isASCIILetter(rune(e)) || (e >= '0' && e <= '9'):
				return "", false
			default:
				b.WriteString(regexp.QuoteMeta(string(e)))
			}
		case basic && strings.IndexByte("|(){}+?", c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		case basic && c == '*' && basicStart(b.String()):
			b.WriteString(`\*`)
		case basic && c == '^' && !basicStart(b.String()):
			b.WriteString(`\^`)
		case basic && c == '$' && i != len(p)-1 && !strings.HasPrefix(p[i+1:], `\)`):
			b.WriteString(`\$`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// basicStart reports a position where BRE reads `^` as an anchor and `*` as a literal.
func basicStart(out string) bool {
	return out == "" || strings.HasSuffix(out, "(") || strings.HasSuffix(out, "^")
}

// bracketEnd returns the index just past the bracket expression opening at p[i]. A `]`
// first in the set is a member, and `[:digit:]` nests.
func bracketEnd(p string, i int) (int, bool) {
	j := i + 1
	if j < len(p) && p[j] == '^' {
		j++
	}
	if j < len(p) && p[j] == ']' {
		j++
	}
	for ; j < len(p); j++ {
		switch {
		case p[j] == '[' && j+1 < len(p) && p[j+1] == ':':
			end := strings.Index(p[j+2:], ":]")
			if end < 0 {
				return 0, false
			}
			j += end + 3
		case p[j] == ']':
			return j + 1, true
		}
	}
	return 0, false
}

// translation is one provable answer: the magus command, why it is the same answer, and
// the answer itself.
type translation struct {
	args   []string // after the binary name
	why    string
	answer []string // what the command prints, as far as the proof computed it
	// rows is what the search prints, exact, or nil when the proof did not read the lines.
	// A pipe's filters run over it so the deny answers the pipeline's question.
	rows []string
	// routes is set for a search of literal diagnostic codes, which keeps the
	// symbol-search rule's per-code answer.
	routes []searchRoute
	piped  pipeResult
}

// translateVerdict denies the first search on the line that a graph query provably
// answers, and reports false when none is.
func translateVerdict(deps Dependencies, cmds []hint.Invocation) (ShellVerdict, bool) {
	dir, ok := deps.workingDir()
	if !ok {
		return ShellVerdict{}, false
	}
	return translateSearches(deps, dir, cmds)
}

func translateSearches(deps Dependencies, dir string, cmds []hint.Invocation) (ShellVerdict, bool) {
	for i, c := range cmds {
		var tr translation
		var ok bool
		if path.Base(c.Name) == "find" {
			tr, ok = translateFind(deps, dir, c)
		} else {
			tr, ok = translateSearch(deps, dir, c)
		}
		if !ok {
			continue
		}
		stages, ok := filterStages(cmds[i+1:])
		if !ok {
			continue
		}
		if tr.routes != nil {
			if !entityStages(stages) {
				continue
			}
			return ShellVerdict{Deny: denySymbolSearch(tr.routes), Rule: denyRule{Name: denyRuleSymbolSearch, Arg: routeNames(tr.routes)}}, true
		}
		if tr.piped, ok = runStages(stages, tr.rows, tr.answer); !ok {
			continue
		}
		return ShellVerdict{Deny: denySearchTranslation(tr), Rule: denyRule{Name: denyRuleSearchTranslation, Arg: strings.Join(tr.args, " ")}}, true
	}
	return ShellVerdict{}, false
}

func translateSearch(deps Dependencies, dir string, c hint.Invocation) (translation, bool) {
	sc, ok := parseSearchCall(c)
	if !ok || sc.readsStdin() || allOutside(deps.scope, sc.paths) || searchesRevision(c, dir) {
		return translation{}, false
	}
	tr, ok := translateDiagnostics(dir, sc)
	if !ok {
		tr, ok = translateHeadings(deps, dir, sc)
	}
	if !ok {
		tr, ok = translateTargets(deps, dir, sc)
	}
	if !ok {
		tr, ok = translateDeclarations(deps, dir, c, sc)
	}
	return tr, ok
}

func denySearchTranslation(tr translation) string {
	return "`" + hint.BinaryName() + " " + strings.Join(tr.args, " ") + "` answers this search exactly.\n" +
		tr.why + "\n" +
		tr.piped.block(tr.answer) + "\n" +
		"Search raw TEXT (a string literal, a comment, a config value) with grep as before: no graph node holds that, so nothing replaces it."
}

// diagnosticDigitAtom is one digit position of a code-shaped alternative, optionally
// repeated.
var diagnosticDigitAtom = regexp.MustCompile(`^(?:([0-9])|(\\d|\.|\[\[:digit:\]\])|(\[[0-9-]+\]))(?:\{([1-4])\})?`)

// codeAlternative is a code-shaped alternative: the query regex that selects the same
// codes, and whether it names exactly one code.
type codeAlternative struct {
	query   string
	literal string
}

// parseCodeAlternative reads an alternative that can only match an MGS code: the literal
// prefix, then one to four digit positions. A line anchor makes it a question about line
// layout rather than about codes, so it is not one; a BZZ code has no graph node.
func parseCodeAlternative(alt string) (codeAlternative, bool) {
	for {
		trimmed := strings.TrimSuffix(strings.TrimPrefix(alt, `\b`), `\b`)
		if trimmed == alt {
			break
		}
		alt = trimmed
	}
	rest, ok := strings.CutPrefix(alt, "MGS")
	if !ok || rest == "" {
		return codeAlternative{}, false
	}
	var q strings.Builder
	q.WriteString("MGS")
	positions, literal := 0, true
	for rest != "" {
		m := diagnosticDigitAtom.FindStringSubmatch(rest)
		if m == nil {
			return codeAlternative{}, false
		}
		n := 1
		if m[4] != "" {
			n, _ = strconv.Atoi(m[4])
		}
		atom := m[1] + m[3]
		if m[2] != "" {
			atom = `\d`
		}
		if m[1] == "" {
			literal = false
		}
		for range n {
			q.WriteString(atom)
		}
		positions += n
		rest = rest[len(m[0]):]
	}
	if positions > 4 {
		return codeAlternative{}, false
	}
	switch pad := 4 - positions; {
	case pad == 1:
		q.WriteString(`\d`)
	case pad > 1:
		q.WriteString(`\d{` + strconv.Itoa(pad) + `}`)
	}
	out := codeAlternative{query: collapseDigitRuns(q.String())}
	if literal && positions == 4 {
		out.literal = alt
	}
	return out, true
}

var digitRunRe = regexp.MustCompile(`(?:\\d){2,}`)

func collapseDigitRuns(s string) string {
	return digitRunRe.ReplaceAllStringFunc(s, func(run string) string {
		return `\d{` + strconv.Itoa(len(run)/2) + `}`
	})
}

// translateDiagnostics answers a pattern that can only match diagnostic codes with the
// query selecting the registered codes it matches. The registry is what the graph builds
// its diagnostic nodes from, so it is the graph's id set without loading the graph.
func translateDiagnostics(dir string, sc searchCall) (translation, bool) {
	for _, p := range sc.paths {
		if !codeBearingPath(dir, p) {
			return translation{}, false
		}
	}
	alts, ok := sc.alternatives()
	if !ok {
		return translation{}, false
	}
	var parsed []codeAlternative
	for _, alt := range alts {
		ca, ok := parseCodeAlternative(alt)
		if !ok {
			return translation{}, false
		}
		parsed = append(parsed, ca)
	}
	line, ok := sc.lineRegexp()
	if !ok {
		return translation{}, false
	}
	registered := types.AllDiagnosticCodes()
	var matched []string
	for _, code := range registered {
		if line.MatchString(string(code)) {
			matched = append(matched, string(code))
		}
	}
	// Every alternative must name something the graph holds, or the search is also
	// looking for text it does not: a retired code in a changelog, say.
	for _, ca := range parsed {
		re := regexp.MustCompile(`^` + ca.query + `$`)
		if !slices.ContainsFunc(matched, re.MatchString) {
			return translation{}, false
		}
	}
	if routes, ok := literalCodeRoutes(parsed); ok {
		return translation{routes: routes}, true
	}
	queries := make([]string, len(parsed))
	for i, ca := range parsed {
		queries[i] = ca.query
	}
	queries = slices.Compact(queries)
	expr := strings.Join(queries, "|")
	if len(queries) > 1 {
		expr = "(?:" + expr + ")"
	}
	idRe := `^diagnostic:` + expr + `$`
	answer := regexp.MustCompile(idRe)
	var selected []string
	for _, code := range registered {
		if answer.MatchString(string(types.KindDiagnostic) + ":" + string(code)) {
			selected = append(selected, string(code))
		}
	}
	if !slices.Equal(selected, matched) {
		return translation{}, false
	}
	ids := make([]string, len(selected))
	for i, code := range selected {
		ids[i] = types.KindDiagnostic + ":" + code
	}
	return translation{
		args: []string{"query", "kind=" + types.KindDiagnostic, "'id=~" + idRe + "'", "-o", "name"},
		why: "The pattern can match nothing but a diagnostic code, and it matches " + countNoun(len(matched), "registered code") + " (" + sample(matched) + "). " +
			"The graph holds a node for every registered code, and `" + hint.Explain.With("diagnostic:<code>") + "` gives each one's page and the docs that cite it.",
		answer: ids,
	}, true
}

// codeBearingPath reports an operand whose mentions of a code are the workspace's own
// sources and docs, which is what the graph is built from. A log or a captured output
// holds what one run emitted, a question no graph node answers.
func codeBearingPath(dir, p string) bool {
	switch ext := path.Ext(p); {
	case strings.HasPrefix(p, ":"):
		// A git pathspec with magic, `:!gen`, only narrows the tree.
		return true
	case ext == ".md" || sourceExt[ext]:
		return true
	case p == "" || strings.HasPrefix(p, unresolved):
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// searchesRevision reports a `git grep` naming a revision, which searches a tree other
// than the one the graph was built from. An operand before `--` is a revision when a `--`
// follows, and otherwise when no such path exists, which is how git itself tells them apart.
func searchesRevision(c hint.Invocation, dir string) bool {
	if path.Base(c.Name) != "git" {
		return false
	}
	g := parseGit(c.Args)
	if g.sub != "grep" {
		return false
	}
	args := g.rest
	sep := slices.Index(args, "--")
	before := args
	if sep >= 0 {
		before = args[:sep]
	}
	var ops []string
	patternGiven := false
	for j := 0; j < len(before); j++ {
		a := before[j]
		switch {
		case a == "-e":
			patternGiven = true
			j++
		case strings.HasPrefix(a, "-"):
			if strings.HasPrefix(a, "-e") {
				patternGiven = true
			} else if len(a) == 2 && strings.IndexByte("ABCmf", a[1]) >= 0 {
				j++
			}
		default:
			ops = append(ops, a)
		}
	}
	if !patternGiven && len(ops) > 0 {
		ops = ops[1:]
	}
	for _, op := range ops {
		if sep >= 0 {
			return true
		}
		if !filepath.IsAbs(op) {
			op = filepath.Join(dir, op)
		}
		if _, err := os.Stat(op); err != nil {
			return true
		}
	}
	return false
}

func literalCodeRoutes(parsed []codeAlternative) ([]searchRoute, bool) {
	var routes []searchRoute
	for _, ca := range parsed {
		if ca.literal == "" {
			return nil, false
		}
		node := types.KindDiagnostic + ":" + ca.literal
		r := searchRoute{name: node, run: hint.Explain.With(node)}
		if !slices.Contains(routes, r) {
			routes = append(routes, r)
		}
	}
	return routes, true
}

// headingProbes are lines every level-agnostic heading pattern selects, and nonHeadings
// lines it must not: a pattern that tells heading levels apart asks something the query
// cannot, since a section's level is not part of its id.
var (
	headingProbes = []string{"# Alpha", "## zz 9", "### Alpha", "#### zz 9", "##### Alpha", "###### zz 9"}
	nonHeadings   = []string{"Alpha", "text # Alpha", "", "  zz 9"}
)

// maxHeadingFiles bounds the files a directory search is proved over, since the proof
// reads each one.
const maxHeadingFiles = 2000

// translateHeadings answers a search for every Markdown heading of some files with the
// query selecting their section nodes. The proof reads the files: the lines the pattern
// selects must be, file by file, as many as the sections the graph holds, and none may
// sit inside a code fence, where a `# comment` is text rather than a heading.
func translateHeadings(deps Dependencies, dir string, sc searchCall) (translation, bool) {
	if sc.word || sc.exclude || deps.scope.root == "" || len(sc.paths) == 0 {
		return translation{}, false
	}
	if len(sc.include) > 0 && !slices.Equal(sc.include, []string{"*.md"}) {
		return translation{}, false
	}
	line, ok := sc.lineRegexp()
	if !ok {
		return translation{}, false
	}
	for _, p := range headingProbes {
		if !line.MatchString(p) {
			return translation{}, false
		}
	}
	for _, p := range nonHeadings {
		if line.MatchString(p) {
			return translation{}, false
		}
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	var files []string // workspace-relative
	var prefixes []string
	for _, p := range sc.paths {
		abs, rel, ok := workspacePath(root, dir, p)
		if !ok {
			return translation{}, false
		}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			return translation{}, false
		case !info.IsDir():
			if path.Ext(rel) != ".md" {
				return translation{}, false
			}
			files = append(files, rel)
			prefixes = append(prefixes, regexp.QuoteMeta(rel))
		case sc.tool == "rg" || !sc.recursive || len(sc.include) == 0:
			// rg walks by ignore files, and grep without --include reads every file, so
			// neither is a set of Markdown files this proof can enumerate.
			return translation{}, false
		default:
			walked, ok := markdownUnder(root, rel)
			if !ok {
				return translation{}, false
			}
			files = append(files, walked...)
			if rel == "." {
				prefixes = append(prefixes, `[^#]*\.md`)
			} else {
				prefixes = append(prefixes, regexp.QuoteMeta(rel)+`/[^#]*\.md`)
			}
		}
	}
	want := map[string]int{}
	var hits []hit
	for _, rel := range files {
		selected, ok := headingLines(rel, filepath.Join(root, rel), line)
		if !ok {
			return translation{}, false
		}
		if len(selected) > 0 {
			want[rel] = len(selected)
			hits = append(hits, selected...)
		}
	}
	if len(want) == 0 {
		return translation{}, false
	}
	ps, ok := sc.speller(root, dir)
	if !ok {
		return translation{}, false
	}
	rows, _ := sc.rows(ps, hits)
	expr := strings.Join(slices.Compact(prefixes), "|")
	if len(prefixes) > 1 {
		expr = "(?:" + expr + ")"
	}
	idRe := `^docsection:` + expr + `#`
	answer, err := regexp.Compile(idRe)
	if err != nil {
		return translation{}, false
	}
	ids, definitive := deps.graphIDs(context.Background(), types.KindDocSection)
	if !definitive {
		return translation{}, false
	}
	got := map[string]int{}
	var sections []string
	for _, id := range ids {
		if answer.MatchString(id) {
			file, _, _ := strings.Cut(strings.TrimPrefix(id, "docsection:"), "#")
			got[file]++
			sections = append(sections, id)
		}
	}
	if !mapsEqual(got, want) {
		return translation{}, false
	}
	total := 0
	for _, n := range want {
		total += n
	}
	return translation{
		args: []string{"query", "kind=" + types.KindDocSection, "'id=~" + idRe + "'", "-o", "name"},
		why: "The pattern selects every Markdown heading and nothing else here: " + countNoun(total, "heading line") + " across " + countNoun(len(want), "file") +
			", one per section node the graph holds for them. `" + hint.Explain.With("docsection:<file>#<anchor>") + "` gives a section's text and links.",
		answer: sections,
		rows:   rows,
	}, true
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// headingLines is the lines of file that re selects, reporting false when one of them
// sits inside a fenced code block.
func headingLines(rel, file string, re *regexp.Regexp) ([]hit, bool) {
	f, err := os.Open(file)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var hits []hit
	fence := ""
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; s.Scan(); n++ {
		text := s.Text()
		trimmed := strings.TrimLeft(text, " ")
		if marker := fenceMarker(trimmed); marker != "" && len(text)-len(trimmed) < 4 {
			switch {
			case fence == "":
				fence = marker
			case strings.HasPrefix(trimmed, fence) && strings.TrimSpace(strings.TrimLeft(trimmed, fence[:1])) == "":
				fence = ""
			}
		}
		if re.MatchString(text) {
			if fence != "" {
				return nil, false
			}
			hits = append(hits, hit{rel: rel, line: n, text: text})
		}
	}
	return hits, s.Err() == nil
}

// fenceMarker is the run of backticks or tildes opening a CommonMark code fence, or "".
func fenceMarker(line string) string {
	for _, ch := range []string{"`", "~"} {
		n := len(line) - len(strings.TrimLeft(line, ch))
		if n >= 3 {
			return line[:n]
		}
	}
	return ""
}

// markdownUnder lists the .md files grep -r --include='*.md' would read under rel.
func markdownUnder(root, rel string) ([]string, bool) {
	var out []string
	err := filepath.WalkDir(filepath.Join(root, rel), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".md") {
			r, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(r))
			if len(out) > maxHeadingFiles {
				return fs.SkipAll
			}
		}
		return nil
	})
	return out, err == nil && len(out) <= maxHeadingFiles
}

// targetDeclRe is a magusfile target: an exported function, whose name the graph spells
// with `-` for `_`.
var targetDeclRe = regexp.MustCompile(`^export fun ([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

// translateTargets answers a search of magusfiles whose every selected line declares a
// target the graph holds. A single hit that is a call, a comment or a string is text the
// graph does not hold, and keeps the rule silent.
func translateTargets(deps Dependencies, dir string, sc searchCall) (translation, bool) {
	if deps.scope.root == "" || len(sc.paths) == 0 {
		return translation{}, false
	}
	for _, p := range sc.paths {
		if path.Base(p) != "magusfile.buzz" {
			return translation{}, false
		}
	}
	line, ok := sc.lineRegexp()
	if !ok {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	var targets []string
	var hits []hit
	for _, p := range sc.paths {
		abs, rel, ok := workspacePath(root, dir, p)
		if !ok {
			return translation{}, false
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return translation{}, false
		}
		project := path.Dir(rel)
		for n, text := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if !line.MatchString(text) {
				continue
			}
			m := targetDeclRe.FindStringSubmatch(text)
			if m == nil {
				return translation{}, false
			}
			hits = append(hits, hit{rel: rel, line: n + 1, text: text})
			id := types.KindTarget + ":" + project + ":" + strings.ReplaceAll(m[1], "_", "-")
			if !slices.Contains(targets, id) {
				targets = append(targets, id)
			}
		}
	}
	if len(targets) == 0 {
		return translation{}, false
	}
	ps, ok := sc.speller(root, dir)
	if !ok {
		return translation{}, false
	}
	rows, _ := sc.rows(ps, hits)
	ids, definitive := deps.graphIDs(context.Background(), types.KindTarget)
	if !definitive {
		return translation{}, false
	}
	for _, id := range targets {
		if !slices.Contains(ids, id) {
			return translation{}, false
		}
	}
	why := "Every line the pattern selects declares a target, and the graph holds each one."
	if len(targets) == 1 {
		return translation{args: []string{"explain", targets[0]}, why: why, answer: targets, rows: rows}, true
	}
	quoted := make([]string, len(targets))
	for i, id := range targets {
		quoted[i] = regexp.QuoteMeta(id)
	}
	return translation{
		args:   []string{"query", "kind=" + types.KindTarget, "'id=~^(?:" + strings.Join(quoted, "|") + ")$'", "-o", "name"},
		why:    why,
		answer: targets,
		rows:   rows,
	}, true
}

// goDeclRe is a Go top-level declaration and its name, the receiver of a method skipped.
// A `type (` or `var (` block opener names nothing and so is not one.
var goDeclRe = regexp.MustCompile(`^(?:func(?:\s*\([^()]*\))?|type|var|const)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// translateDeclarations answers a search of one Go file whose every selected line
// declares a symbol the index holds: `^func `, `func (s \*Store)`. A lookup of names is
// the symbol-search rule's. One hit that is a call, a comment or a string is text the
// graph does not hold, and keeps the rule silent.
func translateDeclarations(deps Dependencies, dir string, c hint.Invocation, sc searchCall) (translation, bool) {
	if deps.scope.root == "" || len(sc.paths) != 1 || path.Ext(sc.paths[0]) != ".go" {
		return translation{}, false
	}
	if _, provable := provableRoutes(deps, asSearch(c)); provable {
		return translation{}, false
	}
	line, ok := sc.lineRegexp()
	if !ok {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	abs, rel, ok := workspacePath(root, dir, sc.paths[0])
	if !ok {
		return translation{}, false
	}
	selected, ok := selectedLines(rel, abs, line)
	if !ok || len(selected) == 0 {
		return translation{}, false
	}
	names := make([]string, 0, len(selected))
	for _, h := range selected {
		m := goDeclRe.FindStringSubmatch(h.text)
		if m == nil {
			return translation{}, false
		}
		if defined, definitive := deps.symbolDefined(m[1]); !defined || !definitive {
			return translation{}, false
		}
		names = append(names, strconv.Itoa(h.line)+": "+m[1])
	}
	ps, ok := sc.speller(root, dir)
	if !ok {
		return translation{}, false
	}
	rows, _ := sc.rows(ps, selected)
	return translation{
		args: []string{"explain", types.KindFile + ":" + rel},
		why: "Every line the pattern selects declares a symbol, and the index holds each of the " + countNoun(len(names), "name") + ". " +
			"The file node lists everything the file defines, and `" + hint.Refs.With("<name>", "--definition", "--source") + "` prints any one body.",
		answer: names,
		rows:   rows,
	}, true
}

// find prints the files under its paths whose name matches, and the graph holds a node for
// every file an index read. The proof walks the same tree and requires the two sets to be
// equal file for file: a Markdown tree, a directory holding a sibling checkout, or a file
// no index covers all fail it and keep the rule silent.

// findCall is one find invocation reduced to what decides its answer.
type findCall struct {
	paths    []string
	name     string
	files    bool // -type f
	maxDepth int  // 0 for unbounded
}

// maxFindEntries bounds the directory entries a find is proved over.
const maxFindEntries = 20000

// parseFindCall reads c as a name search, or reports false for any other predicate, which
// asks something the file nodes do not say.
func parseFindCall(c hint.Invocation) (findCall, bool) {
	fc := findCall{}
	args := c.Args
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") && args[0] != "(" && args[0] != "!" {
		fc.paths = append(fc.paths, args[0])
		args = args[1:]
	}
	if len(fc.paths) == 0 {
		fc.paths = []string{"."}
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-name":
			if i+1 >= len(args) || fc.name != "" || strings.Contains(args[i+1], "/") {
				return findCall{}, false
			}
			if _, err := path.Match(args[i+1], ""); err != nil {
				return findCall{}, false
			}
			fc.name = args[i+1]
			i++
		case "-type":
			if i+1 >= len(args) || args[i+1] != "f" {
				return findCall{}, false
			}
			fc.files = true
			i++
		case "-maxdepth":
			if i+1 >= len(args) {
				return findCall{}, false
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return findCall{}, false
			}
			fc.maxDepth = n
			i++
		case "-print":
		default:
			return findCall{}, false
		}
	}
	return fc, fc.name != ""
}

// translateFind answers a `find -name` under the workspace with the query selecting the
// same file nodes.
func translateFind(deps Dependencies, dir string, c hint.Invocation) (translation, bool) {
	fc, ok := parseFindCall(c)
	if !ok || deps.scope.root == "" || allOutside(deps.scope, fc.paths) {
		return translation{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return translation{}, false
	}
	var found, rows, prefixes []string
	entries := 0
	for _, p := range fc.paths {
		abs, rel, ok := workspacePath(root, dir, p)
		if !ok {
			return translation{}, false
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			return translation{}, false
		}
		written := strings.TrimSuffix(p, "/")
		var matchedDir bool
		err = filepath.WalkDir(abs, func(q string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entries++; entries > maxFindEntries {
				return fs.SkipAll
			}
			sub, _ := filepath.Rel(abs, q)
			sub = filepath.ToSlash(sub)
			if sub == "." {
				return nil
			}
			depth := strings.Count(sub, "/") + 1
			var descend error
			if d.IsDir() && fc.maxDepth > 0 && depth >= fc.maxDepth {
				descend = fs.SkipDir
			}
			if matched, _ := path.Match(fc.name, d.Name()); !matched {
				return descend
			}
			switch {
			case d.Type().IsRegular():
				r := sub
				if rel != "." {
					r = rel + "/" + sub
				}
				found = append(found, r)
				rows = append(rows, written+"/"+sub)
			case d.IsDir() && fc.files:
			default:
				matchedDir = true
			}
			return descend
		})
		if err != nil || matchedDir || entries > maxFindEntries {
			return translation{}, false
		}
		prefix := ""
		if rel != "." {
			prefix = regexp.QuoteMeta(rel) + "/"
		}
		if fc.maxDepth > 0 {
			prefix += `(?:[^/]+/){0,` + strconv.Itoa(fc.maxDepth-1) + `}`
		} else {
			prefix += `(?:.*/)?`
		}
		prefixes = append(prefixes, prefix)
	}
	expr := strings.Join(slices.Compact(prefixes), "|")
	if len(prefixes) > 1 {
		expr = "(?:" + expr + ")"
	}
	idRe := `^file:` + expr + globRegexp(fc.name) + `$`
	answer, err := regexp.Compile(idRe)
	if err != nil {
		return translation{}, false
	}
	ids, definitive := deps.graphIDs(context.Background(), types.KindFile)
	if !definitive {
		return translation{}, false
	}
	var selected []string
	for _, id := range ids {
		if answer.MatchString(id) {
			selected = append(selected, strings.TrimPrefix(id, types.KindFile+":"))
		}
	}
	slices.Sort(found)
	if !slices.Equal(slices.Compact(found), selected) {
		return translation{}, false
	}
	nodes := make([]string, len(selected))
	for i, f := range selected {
		nodes[i] = types.KindFile + ":" + f
	}
	return translation{
		args: []string{"query", "kind=" + types.KindFile, "'id=~" + idRe + "'", "-o", "name"},
		why: "The graph holds a file node for each of the " + countNoun(len(selected), "file") + " this find prints, checked name for name, and `" +
			hint.Explain.With("file:<path>") + "` lists what any one defines and who depends on it.",
		answer: nodes,
		rows:   rows,
	}, true
}

// globRegexp is find's -name glob over a file name as a Go regexp.
func globRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; c {
		case '*':
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			end := strings.IndexByte(glob[i:], ']')
			if end < 0 {
				b.WriteString(regexp.QuoteMeta(string(c)))
				continue
			}
			b.WriteString(glob[i : i+end+1])
			i += end
		case '\\':
			if i+1 < len(glob) {
				i++
				b.WriteString(regexp.QuoteMeta(string(glob[i])))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// resolvedRoot is the workspace root with symlinks resolved, so it compares with a path
// reached through /private on macOS.
func resolvedRoot(root string) (string, bool) {
	r, err := filepath.EvalSymlinks(root)
	return r, err == nil
}

// workspacePath resolves a search operand against the call's directory, and reports false
// for a path outside the workspace, a glob, or one that does not exist.
func workspacePath(root, dir, p string) (abs, rel string, ok bool) {
	if p == "" || strings.HasPrefix(p, "~") || strings.HasPrefix(p, unresolved) || strings.ContainsAny(p, "*?[") {
		return "", "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	abs, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", "", false
	}
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	return abs, filepath.ToSlash(r), true
}

// A pipe after a search only ever does one of five things to its rows: truncates them,
// counts them, dedupes or orders them, projects a column, or filters them. Each is run
// over the rows the proof computed, so the deny answers the pipeline's question rather
// than the search's. A filter this does not model changes the question in a way no graph
// command reproduces, and keeps the rule silent.
//
// Measured 2026-09-26 over 30,037 piped searches in 1,452 transcripts: 18,646 were
// `grep | head`, 3,512 `grep | grep -v | head`, 1,391 `grep | grep -v`, 986 `grep | grep |
// head`, 462 `grep | sed`, 356 `find | head`, 297 `grep | tail`, 177 `grep | wc -l`, 154
// `grep | sort -u`, 117 `grep | cut | head`, 101 `find | wc -l`, 62 `grep | sort | uniq -c`.

// pipeStage is one filter on a search's pipe.
type pipeStage struct {
	text     string
	apply    func([]string) []string
	entities bool // truncates, orders or dedupes only, so it runs over an entity list too
	scalar   bool // reduces the rows to one number
	toFiles  bool // projects a file:line:text row onto its file column
}

// pipeResult is a search's answer after its pipe: the rows, the filters as written, and
// whether the last one reduced them to a number. A zero value is a search with no pipe.
type pipeResult struct {
	text   string
	rows   []string
	scalar bool
}

func (p pipeResult) block(answer []string) string {
	switch {
	case p.text == "":
		return answerBlock("Its answer", answer)
	case p.scalar:
		return "Its answer, after `" + p.text + "`: " + strings.Join(p.rows, " ")
	}
	return answerBlock("Its answer, after `"+p.text+"`", p.rows)
}

// runStages runs the filters over rows, or over the entity list when the rows are unknown
// and every filter only truncates, orders or dedupes. false is a question the proof holds
// no rows to answer.
func runStages(stages []pipeStage, rows, answer []string) (pipeResult, bool) {
	if len(stages) == 0 {
		return pipeResult{}, true
	}
	if rows == nil {
		if !entityStages(stages) {
			return pipeResult{}, false
		}
		rows = answer
	}
	texts := make([]string, len(stages))
	for i, s := range stages {
		rows = s.apply(rows)
		texts[i] = s.text
	}
	return pipeResult{text: "| " + strings.Join(texts, " | "), rows: rows, scalar: stages[len(stages)-1].scalar}, true
}

func entityStages(stages []pipeStage) bool {
	return !slices.ContainsFunc(stages, func(s pipeStage) bool { return !s.entities })
}

func projectsToFiles(stages []pipeStage) bool {
	return slices.ContainsFunc(stages, func(s pipeStage) bool { return s.toFiles })
}

// pipeConsumers are the programs that read a search's output on a pipe. Any other program
// after the search names its own input, and ends the pipe.
var pipeConsumers = map[string]bool{
	"head": true, "tail": true, "wc": true, "sort": true, "uniq": true, "cut": true, "tr": true, "sed": true,
	"awk": true, "gawk": true, "grep": true, "egrep": true, "fgrep": true, "rg": true, "cat": true,
	"xargs": true, "tee": true, "less": true, "more": true, "jq": true, "yq": true, "column": true, "paste": true,
	"nl": true, "tac": true, "rev": true, "perl": true, "python": true, "python3": true, "ruby": true, "node": true,
	"sh": true, "bash": true, "zsh": true, "read": true, "while": true, "fzf": true, "fold": true, "fmt": true,
	"comm": true, "diff": true, "join": true, "base64": true, "md5": true, "md5sum": true, "shasum": true,
	"sha256sum": true, "xxd": true, "od": true, "hexdump": true, "sponge": true, "pbcopy": true, "expand": true,
	"unexpand": true, "pr": true, "split": true, "tsort": true, "shuf": true, "gron": true,
}

// filterStages reads the filters that follow a search on its pipe, up to the first command
// that names its own input. false is a consumer this does not model. The command list is
// flat, so a filter after `;` reads the same; it would have been refused as unfed first.
func filterStages(cmds []hint.Invocation) ([]pipeStage, bool) {
	var out []pipeStage
	for _, c := range cmds {
		name := path.Base(c.Name)
		if !pipeConsumers[name] {
			break
		}
		st, ok := parseStage(name, c.Args)
		switch {
		case st.apply == nil && ok:
			return out, true // the consumer names its own input
		case !ok:
			return nil, false
		}
		st.text = stageText(name, c.Args)
		out = append(out, st)
	}
	return out, true
}

var plainWordRe = regexp.MustCompile(`^[A-Za-z0-9_./=:,+@%^-]+$`)

func stageText(name string, args []string) string {
	words := []string{name}
	for _, a := range args {
		if plainWordRe.MatchString(a) {
			words = append(words, a)
		} else {
			words = append(words, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
		}
	}
	return strings.Join(words, " ")
}

// stageFlags splits a filter's arguments into its flags, each flag's value where the flag
// takes one (valueShorts and valueLongs), and its operands. Clustered short flags are
// split, and `-N` is returned as a flag named by its digits.
type stageFlags struct {
	flags    []string // "n", "line-number"
	values   map[string]string
	operands []string
	bad      bool
}

func parseStageFlags(args []string, valueShorts string, valueLongs []string) stageFlags {
	sf := stageFlags{values: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			sf.operands = append(sf.operands, args[i+1:]...)
			return sf
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a[2:], "=")
			if slices.Contains(valueLongs, name) {
				if !hasVal {
					if i+1 >= len(args) {
						sf.bad = true
						return sf
					}
					i++
					val = args[i]
				}
				sf.values[name] = val
				sf.flags = append(sf.flags, name)
				continue
			}
			if hasVal {
				sf.bad = true
				return sf
			}
			sf.flags = append(sf.flags, name)
		case len(a) > 1 && a[0] == '-':
			cluster := a[1:]
			if _, err := strconv.Atoi(cluster); err == nil {
				sf.flags = append(sf.flags, cluster)
				continue
			}
			for j := 0; j < len(cluster); j++ {
				f := string(cluster[j])
				sf.flags = append(sf.flags, f)
				if strings.IndexByte(valueShorts, cluster[j]) < 0 {
					continue
				}
				val := cluster[j+1:]
				if val == "" {
					if i+1 >= len(args) {
						sf.bad = true
						return sf
					}
					i++
					val = args[i]
				}
				sf.values[f] = val
				break
			}
		default:
			sf.operands = append(sf.operands, a)
		}
	}
	return sf
}

// only reports whether every flag is one of allowed, so a flag the stage does not model is
// noticed rather than ignored.
func (sf stageFlags) only(allowed ...string) bool {
	return !sf.bad && !slices.ContainsFunc(sf.flags, func(f string) bool { return !slices.Contains(allowed, f) })
}

func (sf stageFlags) has(names ...string) bool {
	return slices.ContainsFunc(sf.flags, func(f string) bool { return slices.Contains(names, f) })
}

// value is the last value given for any of names, or "".
func (sf stageFlags) value(names ...string) string {
	for i := len(sf.flags) - 1; i >= 0; i-- {
		if slices.Contains(names, sf.flags[i]) {
			return sf.values[sf.flags[i]]
		}
	}
	return ""
}

// count is the line count a head or tail was given: -n N, -N, --lines=N, else 10.
func (sf stageFlags) count() (int, bool) {
	n := 10
	for _, f := range sf.flags {
		v := f
		if f == "n" || f == "lines" {
			v = sf.values[f]
		}
		if k, err := strconv.Atoi(v); err == nil && k >= 0 {
			n = k
		} else if strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") {
			return 0, false
		}
	}
	return n, true
}

// parseStage reduces one consumer to its transform. A stage with a nil apply and ok names
// its own input rather than the pipe; ok false is a consumer or a flag this does not model.
func parseStage(name string, args []string) (pipeStage, bool) {
	own := func(sf stageFlags) (pipeStage, bool) { return pipeStage{}, len(sf.operands) > 0 }
	switch name {
	case "head", "tail":
		sf := parseStageFlags(args, "nc", []string{"lines", "bytes"})
		if len(sf.operands) > 0 {
			return own(sf)
		}
		n, ok := sf.count()
		if !ok || sf.has("c", "bytes", "f", "follow", "r", "v", "q", "z") {
			return pipeStage{}, false
		}
		if name == "head" {
			return pipeStage{entities: true, apply: func(rows []string) []string { return rows[:min(n, len(rows))] }}, true
		}
		return pipeStage{entities: true, apply: func(rows []string) []string { return rows[max(0, len(rows)-n):] }}, true
	case "wc":
		sf := parseStageFlags(args, "", nil)
		if len(sf.operands) > 0 {
			return own(sf)
		}
		if !sf.only("l", "lines") || !sf.has("l", "lines") {
			return pipeStage{}, false
		}
		return pipeStage{scalar: true, apply: func(rows []string) []string { return []string{strconv.Itoa(len(rows))} }}, true
	case "sort":
		sf := parseStageFlags(args, "kto", []string{"key", "field-separator", "output", "buffer-size"})
		if len(sf.operands) > 0 {
			return own(sf)
		}
		if !sf.only("u", "unique", "r", "reverse", "n", "numeric-sort", "f", "ignore-case", "b", "ignore-leading-blanks", "s", "stable", "i", "d") {
			return pipeStage{}, false
		}
		cmpRows := sortComparator(sf.has("n", "numeric-sort"), sf.has("f", "ignore-case"), sf.has("r", "reverse"))
		unique := sf.has("u", "unique")
		return pipeStage{entities: true, apply: func(rows []string) []string {
			out := slices.Clone(rows)
			slices.SortStableFunc(out, cmpRows)
			if unique {
				out = slices.CompactFunc(out, func(a, b string) bool { return cmpRows(a, b) == 0 })
			}
			return out
		}}, true
	case "uniq":
		sf := parseStageFlags(args, "fsw", nil)
		if len(sf.operands) > 0 {
			return own(sf)
		}
		if !sf.only("c", "count") {
			return pipeStage{}, false
		}
		counted := sf.has("c", "count")
		return pipeStage{entities: !counted, apply: func(rows []string) []string {
			var out []string
			for i, r := range rows {
				if i > 0 && rows[i-1] == r {
					if counted {
						n, _ := strconv.Atoi(strings.Fields(out[len(out)-1])[0])
						out[len(out)-1] = fmt.Sprintf("%7d %s", n+1, r)
					}
					continue
				}
				if counted {
					r = fmt.Sprintf("%7d %s", 1, r)
				}
				out = append(out, r)
			}
			return out
		}}, true
	case "cut":
		return cutStage(args)
	case "tr":
		return trStage(args)
	case "sed":
		return sedStage(args)
	case "awk", "gawk":
		return awkStage(args)
	case "grep", "egrep", "fgrep", "rg":
		return grepStage(name, args)
	case "cat":
		if len(args) == 0 {
			return pipeStage{entities: true, apply: func(rows []string) []string { return rows }}, true
		}
		sf := parseStageFlags(args, "", nil)
		if len(sf.operands) > 0 {
			return own(sf)
		}
	}
	return pipeStage{}, false
}

func sortComparator(numeric, fold, reverse bool) func(a, b string) int {
	key := func(s string) string {
		if fold {
			return strings.ToLower(s)
		}
		return s
	}
	leading := func(s string) (float64, bool) {
		f := strings.TrimLeft(s, " \t")
		end := 0
		for end < len(f) && (f[end] == '-' || f[end] == '+' || f[end] == '.' || (f[end] >= '0' && f[end] <= '9')) {
			end++
		}
		v, err := strconv.ParseFloat(f[:end], 64)
		return v, err == nil
	}
	return func(a, b string) int {
		c := 0
		if numeric {
			va, oka := leading(a)
			vb, okb := leading(b)
			if !oka {
				va = 0
			}
			if !okb {
				vb = 0
			}
			c = cmp.Compare(va, vb)
		}
		if c == 0 {
			c = strings.Compare(key(a), key(b))
		}
		if reverse {
			return -c
		}
		return c
	}
}

// fieldList reads cut's LIST: `1`, `1,3`, `2-4`, `3-`, `-2`, as a predicate over 1-based
// positions.
func fieldList(list string) (func(int) bool, bool) {
	type span struct{ lo, hi int }
	var spans []span
	for _, part := range strings.Split(list, ",") {
		lo, hi, ranged := strings.Cut(part, "-")
		s := span{1, 1 << 30}
		if lo != "" {
			n, err := strconv.Atoi(lo)
			if err != nil || n < 1 {
				return nil, false
			}
			s.lo = n
			if !ranged {
				s.hi = n
			}
		} else if !ranged {
			return nil, false
		}
		if hi != "" {
			n, err := strconv.Atoi(hi)
			if err != nil || n < s.lo {
				return nil, false
			}
			s.hi = n
		}
		spans = append(spans, s)
	}
	if len(spans) == 0 {
		return nil, false
	}
	return func(i int) bool {
		return slices.ContainsFunc(spans, func(s span) bool { return i >= s.lo && i <= s.hi })
	}, true
}

func cutStage(args []string) (pipeStage, bool) {
	sf := parseStageFlags(args, "dfcb", []string{"delimiter", "fields", "characters", "bytes"})
	if len(sf.operands) > 0 {
		return pipeStage{}, true
	}
	if !sf.only("d", "delimiter", "f", "fields", "c", "characters", "b", "bytes", "s", "only-delimited") {
		return pipeStage{}, false
	}
	if list := sf.value("c", "characters", "b", "bytes"); list != "" {
		keep, ok := fieldList(list)
		if !ok || sf.has("f", "fields") {
			return pipeStage{}, false
		}
		return pipeStage{apply: func(rows []string) []string {
			out := make([]string, len(rows))
			for i, r := range rows {
				var b strings.Builder
				for j, ch := range []rune(r) {
					if keep(j + 1) {
						b.WriteRune(ch)
					}
				}
				out[i] = b.String()
			}
			return out
		}}, true
	}
	list := sf.value("f", "fields")
	keep, ok := fieldList(list)
	if !ok {
		return pipeStage{}, false
	}
	delim := "\t"
	if sf.has("d", "delimiter") {
		delim = sf.value("d", "delimiter")
		if len(delim) != 1 {
			return pipeStage{}, false
		}
	}
	onlyDelimited := sf.has("s", "only-delimited")
	return pipeStage{toFiles: delim == ":" && list == "1", apply: func(rows []string) []string {
		var out []string
		for _, r := range rows {
			if !strings.Contains(r, delim) {
				if !onlyDelimited {
					out = append(out, r)
				}
				continue
			}
			var kept []string
			for j, f := range strings.Split(r, delim) {
				if keep(j + 1) {
					kept = append(kept, f)
				}
			}
			out = append(out, strings.Join(kept, delim))
		}
		return out
	}}, true
}

// trChars reads a tr set of plain characters, with `\n` and `\t` spelled out. A range or a
// class is not one.
func trChars(set string) (string, bool) {
	if strings.ContainsAny(set, "[-") {
		return "", false
	}
	set = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`).Replace(set)
	return set, set != ""
}

func trStage(args []string) (pipeStage, bool) {
	sf := parseStageFlags(args, "", nil)
	if !sf.only("d", "delete") {
		return pipeStage{}, false
	}
	if sf.has("d", "delete") {
		if len(sf.operands) != 1 {
			return pipeStage{}, false
		}
		set, ok := trChars(sf.operands[0])
		if !ok {
			return pipeStage{}, false
		}
		if strings.Contains(set, "\n") {
			return pipeStage{apply: func(rows []string) []string { return []string{strings.ReplaceAll(strings.Join(rows, ""), set, "")} }}, true
		}
		return pipeStage{apply: func(rows []string) []string {
			out := make([]string, len(rows))
			for i, r := range rows {
				out[i] = strings.Map(func(ch rune) rune {
					if strings.ContainsRune(set, ch) {
						return -1
					}
					return ch
				}, r)
			}
			return out
		}}, true
	}
	if len(sf.operands) != 2 {
		return pipeStage{}, false
	}
	from, ok1 := trChars(sf.operands[0])
	to, ok2 := trChars(sf.operands[1])
	if !ok1 || !ok2 || len([]rune(from)) != 1 || len([]rune(to)) != 1 {
		return pipeStage{}, false
	}
	if from == "\n" {
		// Every newline goes, the one ending the last row included.
		return pipeStage{apply: func(rows []string) []string {
			if len(rows) == 0 {
				return rows
			}
			return []string{strings.Join(rows, to) + to}
		}}, true
	}
	return pipeStage{apply: func(rows []string) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = strings.ReplaceAll(r, from, to)
		}
		return out
	}}, true
}

// sedAddressRe is a sed address: a line, a range of lines, `$`, or a pattern.
var sedAddressRe = regexp.MustCompile(`^(?:(\d+|\$)(?:,(\d+|\$))?|/((?:[^/\\]|\\.)*)/)`)

// sedStage models the substitutions and the line selections a sed on a pipe performs:
// `s/RE/REP/[g]`, `-n 'N,Mp'`, `-n '/RE/p'`, `'/RE/d'`, `'Nd'`. One command per script.
func sedStage(args []string) (pipeStage, bool) {
	sf := parseStageFlags(args, "e", []string{"expression"})
	if !sf.only("n", "quiet", "silent", "E", "r", "regexp-extended", "e", "expression") {
		return pipeStage{}, false
	}
	scripts := []string{}
	for i, f := range sf.flags {
		if f == "e" || f == "expression" {
			scripts = append(scripts, sf.values[sf.flags[i]])
		}
	}
	operands := sf.operands
	if len(scripts) == 0 {
		if len(operands) == 0 {
			return pipeStage{}, false
		}
		scripts, operands = operands[:1], operands[1:]
	}
	if len(operands) > 0 {
		return pipeStage{}, true
	}
	quiet := sf.has("n", "quiet", "silent")
	mode := regexBasic
	if sf.has("E", "r", "regexp-extended") {
		mode = regexExtended
	}
	var applies []func([]string) []string
	for _, script := range scripts {
		apply, ok := sedCommand(strings.TrimSpace(script), mode, quiet)
		if !ok {
			return pipeStage{}, false
		}
		applies = append(applies, apply)
	}
	return pipeStage{apply: func(rows []string) []string {
		for _, a := range applies {
			rows = a(rows)
		}
		return rows
	}}, true
}

func sedCommand(script string, mode regexMode, quiet bool) (func([]string) []string, bool) {
	if strings.HasPrefix(script, "s") && len(script) > 1 && !isASCIILetter(rune(script[1])) && script[1] != ' ' {
		if quiet {
			return nil, false
		}
		return sedSubstitute(script, mode)
	}
	m := sedAddressRe.FindStringSubmatch(script)
	if m == nil {
		return nil, false
	}
	cmd := script[len(m[0]):]
	if (cmd != "p" && cmd != "d") || (cmd == "p") != quiet {
		return nil, false
	}
	var selects func(i, n int, row string) bool
	switch {
	case m[3] != "":
		expr, ok := toGoRegexp(m[3], mode, false)
		if !ok {
			return nil, false
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, false
		}
		selects = func(_, _ int, row string) bool { return re.MatchString(row) }
	default:
		lo, hi := m[1], m[2]
		if hi == "" {
			hi = lo
		}
		selects = func(i, n int, _ string) bool {
			at := func(s string) int {
				if s == "$" {
					return n
				}
				v, _ := strconv.Atoi(s)
				return v
			}
			return i >= at(lo) && i <= at(hi)
		}
	}
	keep := cmd == "p"
	return func(rows []string) []string {
		out := []string{}
		for i, r := range rows {
			if selects(i+1, len(rows), r) == keep {
				out = append(out, r)
			}
		}
		return out
	}, true
}

// sedSubstitute reads `s<d>RE<d>REP<d>[g]` with any delimiter.
func sedSubstitute(script string, mode regexMode) (func([]string) []string, bool) {
	delim := script[1]
	var parts []string
	var b strings.Builder
	for i := 2; i < len(script); i++ {
		switch {
		case script[i] == '\\' && i+1 < len(script) && script[i+1] == delim:
			b.WriteByte(delim)
			i++
		case script[i] == '\\' && i+1 < len(script):
			b.WriteByte('\\')
			b.WriteByte(script[i+1])
			i++
		case script[i] == delim:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteByte(script[i])
		}
	}
	if len(parts) != 2 {
		return nil, false
	}
	flags := b.String()
	if flags != "" && flags != "g" {
		return nil, false
	}
	expr, ok := toGoRegexp(parts[0], mode, false)
	if !ok {
		return nil, false
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, false
	}
	rep, ok := sedReplacement(parts[1])
	if !ok {
		return nil, false
	}
	global := flags == "g"
	return func(rows []string) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			if global {
				out[i] = re.ReplaceAllString(r, rep)
				continue
			}
			loc := re.FindStringSubmatchIndex(r)
			if loc == nil {
				out[i] = r
				continue
			}
			out[i] = r[:loc[0]] + string(re.ExpandString(nil, rep, r, loc)) + r[loc[1]:]
		}
		return out
	}, true
}

// sedReplacement rewrites sed's replacement into Go's: `&` and `\N` are the match and its
// groups, `$` is literal.
func sedReplacement(rep string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(rep); i++ {
		switch c := rep[i]; {
		case c == '$':
			b.WriteString("$$")
		case c == '&':
			b.WriteString("${0}")
		case c == '\\' && i+1 < len(rep):
			i++
			switch e := rep[i]; {
			case e >= '0' && e <= '9':
				b.WriteString("${" + string(e) + "}")
			case e == 'n':
				return "", false
			default:
				b.WriteByte(e)
			}
		case c == '\\':
			return "", false
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// awkPrintRe is the one awk program a pipe reaches for: a print of some fields.
var awkPrintRe = regexp.MustCompile(`^\{\s*print(?:\s+\$(\d+|NF)(?:\s*,\s*\$(\d+|NF))*)?\s*\}$`)

func awkStage(args []string) (pipeStage, bool) {
	sf := parseStageFlags(args, "F", []string{"field-separator"})
	if !sf.only("F", "field-separator") {
		return pipeStage{}, false
	}
	if len(sf.operands) == 0 {
		return pipeStage{}, false
	}
	if len(sf.operands) > 1 {
		return pipeStage{}, true
	}
	program := strings.TrimSpace(sf.operands[0])
	if !awkPrintRe.MatchString(program) {
		return pipeStage{}, false
	}
	var fields []string
	for _, m := range regexp.MustCompile(`\$(\d+|NF)`).FindAllStringSubmatch(program, -1) {
		fields = append(fields, m[1])
	}
	sep := ""
	if sf.has("F", "field-separator") {
		sep, _ = trChars(sf.value("F", "field-separator"))
		if len(sep) != 1 || sep == " " {
			return pipeStage{}, false
		}
	}
	split := func(row string) []string {
		if sep == "" {
			return strings.Fields(row)
		}
		return strings.Split(row, sep)
	}
	return pipeStage{toFiles: sep == ":" && len(fields) == 1 && fields[0] == "1", apply: func(rows []string) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			if len(fields) == 0 {
				out[i] = r
				continue
			}
			parts := split(r)
			var picked []string
			for _, f := range fields {
				n := len(parts)
				if f != "NF" {
					n, _ = strconv.Atoi(f)
				}
				switch {
				case n == 0:
					picked = append(picked, r)
				case n <= len(parts):
					picked = append(picked, parts[n-1])
				default:
					picked = append(picked, "")
				}
			}
			out[i] = strings.Join(picked, " ")
		}
		return out
	}}, true
}

// grepStage models a grep on a pipe as a row filter: -v, -i, -w, -x, -E, -F, -P, -e, -c
// and -m. A print flag that reshapes rows (-n, -o) or one that adds context is not modeled.
func grepStage(name string, args []string) (pipeStage, bool) {
	sf := parseStageFlags(args, "em", []string{"regexp", "max-count"})
	if !sf.only("v", "invert-match", "i", "ignore-case", "w", "word-regexp", "x", "line-regexp", "E", "extended-regexp",
		"F", "fixed-strings", "P", "perl-regexp", "G", "basic-regexp", "e", "regexp", "c", "count", "m", "max-count",
		"s", "no-messages", "a", "text", "color", "colour", "N", "no-line-number", "S", "smart-case") {
		return pipeStage{}, false
	}
	var patterns []string
	for i, f := range sf.flags {
		if f == "e" || f == "regexp" {
			patterns = append(patterns, sf.values[sf.flags[i]])
		}
	}
	operands := sf.operands
	if len(patterns) == 0 {
		if len(operands) == 0 {
			return pipeStage{}, false
		}
		patterns, operands = operands[:1], operands[1:]
	}
	if len(operands) > 0 {
		return pipeStage{}, true
	}
	sc := searchCall{tool: name, patterns: patterns, word: sf.has("w", "word-regexp")}
	sc.mode = searchMode(hint.Invocation{Name: name, Args: args})
	sc.perl = name == "rg" || sf.has("P", "perl-regexp")
	if sf.has("S", "smart-case") && strings.ToLower(strings.Join(patterns, "")) != strings.Join(patterns, "") {
		return pipeStage{}, false
	}
	line, ok := sc.lineRegexp()
	if !ok {
		return pipeStage{}, false
	}
	expr := line.String()
	if sf.has("x", "line-regexp") {
		expr = "^(?:" + expr + ")$"
	}
	if sf.has("i", "ignore-case", "S", "smart-case") {
		expr = "(?i)" + expr
	}
	line, err := regexp.Compile(expr)
	if err != nil {
		return pipeStage{}, false
	}
	invert := sf.has("v", "invert-match")
	limit := -1
	if v := sf.value("m", "max-count"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return pipeStage{}, false
		}
		limit = n
	}
	counted := sf.has("c", "count")
	return pipeStage{scalar: counted, apply: func(rows []string) []string {
		out := []string{}
		for _, r := range rows {
			if limit >= 0 && len(out) >= limit {
				break
			}
			if line.MatchString(r) != invert {
				out = append(out, r)
			}
		}
		if counted {
			return []string{strconv.Itoa(len(out))}
		}
		return out
	}}, true
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// sample names the first few of a list, so a wide pattern's deny stays one line.
func sample(xs []string) string {
	const shown = 4
	if len(xs) <= shown {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:shown], ", ") + ", ..."
}
