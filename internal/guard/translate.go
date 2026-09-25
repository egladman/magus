package guard

import (
	"bufio"
	"context"
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

// The search-translation rule: a text search whose pattern a graph query answers with the
// same entities. The pattern is compiled in the dialect the tool would use and run against
// the graph's own ids at judge time, so the deny names a query that was checked, not one
// that merely looks equivalent. Anything short of that proof stays silent: a deny that
// routes nowhere takes a capability away.
//
// Three shapes are provable: a pattern that can only match diagnostic codes, a pattern
// that selects every Markdown heading of the files searched, and a pattern whose every hit
// in a magusfile is a target declaration.

// translatableTools are the search tools whose flags and dialects this rule models.
var translatableTools = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true}

// searchCall is one search invocation reduced to what decides its answer.
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
	}
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

// translation is one provable answer: the magus command and why it is the same answer.
type translation struct {
	args []string // after the binary name
	why  string
	// routes is set for a search of literal diagnostic codes, which keeps the
	// symbol-search rule's per-code answer.
	routes []searchRoute
}

// translateVerdict denies the first search on the line that a graph query provably
// answers, and reports false when none is.
func translateVerdict(deps Dependencies, cmds []hint.Invocation) (ShellVerdict, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return ShellVerdict{}, false
	}
	return translateSearches(deps, dir, cmds)
}

func translateSearches(deps Dependencies, dir string, cmds []hint.Invocation) (ShellVerdict, bool) {
	for _, c := range cmds {
		sc, ok := parseSearchCall(c)
		if !ok || sc.readsStdin() || allOutside(deps.scope, sc.paths) || searchesRevision(c, dir) {
			continue
		}
		tr, ok := translateDiagnostics(dir, sc)
		if !ok {
			tr, ok = translateHeadings(deps, dir, sc)
		}
		if !ok {
			tr, ok = translateTargets(deps, dir, sc)
		}
		if !ok {
			continue
		}
		if tr.routes != nil {
			return ShellVerdict{Deny: denySymbolSearch(tr.routes), Rule: denyRule{Name: denyRuleSymbolSearch, Arg: routeNames(tr.routes)}}, true
		}
		return ShellVerdict{Deny: denySearchTranslation(tr), Rule: denyRule{Name: denyRuleSearchTranslation, Arg: strings.Join(tr.args, " ")}}, true
	}
	return ShellVerdict{}, false
}

func denySearchTranslation(tr translation) string {
	return "`" + hint.BinaryName() + " " + strings.Join(tr.args, " ") + "` answers this search exactly.\n" +
		tr.why + "\n" +
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
	return translation{
		args: []string{"query", "kind=" + types.KindDiagnostic, "'id=~" + idRe + "'", "-o", "name"},
		why: "The pattern can match nothing but a diagnostic code, and it matches " + countNoun(len(matched), "registered code") + " (" + sample(matched) + "). " +
			"The graph holds a node for every registered code, and `" + hint.Explain.With("diagnostic:<code>") + "` gives each one's page and the docs that cite it.",
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
	i := slices.Index(c.Args, "grep")
	if i < 0 {
		return false
	}
	args := c.Args[i+1:]
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
	for _, rel := range files {
		n, ok := headingLines(filepath.Join(root, rel), line)
		if !ok {
			return translation{}, false
		}
		if n > 0 {
			want[rel] = n
		}
	}
	if len(want) == 0 {
		return translation{}, false
	}
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
	for _, id := range ids {
		if answer.MatchString(id) {
			file, _, _ := strings.Cut(strings.TrimPrefix(id, "docsection:"), "#")
			got[file]++
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

// headingLines counts the lines of file that re selects, reporting false when one of
// them sits inside a fenced code block.
func headingLines(file string, re *regexp.Regexp) (int, bool) {
	f, err := os.Open(file)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	n := 0
	fence := ""
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for s.Scan() {
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
				return 0, false
			}
			n++
		}
	}
	return n, s.Err() == nil
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
		for _, text := range strings.Split(string(data), "\n") {
			if !line.MatchString(text) {
				continue
			}
			m := targetDeclRe.FindStringSubmatch(text)
			if m == nil {
				return translation{}, false
			}
			id := types.KindTarget + ":" + project + ":" + strings.ReplaceAll(m[1], "_", "-")
			if !slices.Contains(targets, id) {
				targets = append(targets, id)
			}
		}
	}
	if len(targets) == 0 {
		return translation{}, false
	}
	ids, definitive := deps.graphIDs(context.Background(), types.KindTarget)
	if !definitive {
		return translation{}, false
	}
	for _, id := range targets {
		if !slices.Contains(ids, id) {
			return translation{}, false
		}
	}
	why := "Every line the pattern selects declares a target, and the graph holds each one: " + strings.Join(targets, ", ") + "."
	if len(targets) == 1 {
		return translation{args: []string{"explain", targets[0]}, why: why}, true
	}
	quoted := make([]string, len(targets))
	for i, id := range targets {
		quoted[i] = regexp.QuoteMeta(id)
	}
	return translation{
		args: []string{"query", "kind=" + types.KindTarget, "'id=~^(?:" + strings.Join(quoted, "|") + ")$'", "-o", "name"},
		why:  why,
	}, true
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
