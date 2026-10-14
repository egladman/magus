package types

import (
	"bytes"
	"go/scanner"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// DiffDriver is a diff driver magus routes files to: the name git knows it by, the files
// it covers, and the funcname patterns that name the declaration enclosing a line.
type DiffDriver struct {
	// Name is the driver's name in `.gitattributes` (`diff=golang`).
	Name string
	// Globs are the gitattributes patterns it covers, each matched against the file's base
	// name, as a slashless gitattributes pattern is.
	Globs []string
	// Funcname is git's xfuncname value: POSIX extended regexes, one per line, tried in
	// order until one matches; a line starting with ! rejects what it matches. Group 1,
	// when a pattern has one, is the declaration's name.
	Funcname string
	// Builtin says git ships the pattern for Name, so magus registers nothing for it.
	Builtin bool
}

// DiffDrivers are the diff drivers magus routes files to, in the order the managed
// `.gitattributes` section lists them. The builtin patterns are git's own (userdiff.c),
// copied so a declaration named without git is the one git names.
var DiffDrivers = []DiffDriver{
	{Name: "golang", Globs: []string{"*.go"}, Funcname: golangFuncname},
	{Name: "python", Globs: []string{"*.py"}, Builtin: true, Funcname: "^[ \t]*((class|(async[ \t]+)?def)[ \t].*)$"},
	{Name: "rust", Globs: []string{"*.rs"}, Builtin: true, Funcname: "^[\t ]*((pub(\\([^\\)]+\\))?[\t ]+)?((async|const|unsafe|extern([\t ]+\"[^\"]+\"))[\t ]+)?(struct|enum|union|mod|trait|fn|impl|macro_rules!)[< \t]+[^;]*)$"},
	{Name: "markdown", Globs: []string{"*.md"}, Builtin: true, Funcname: "^ {0,3}#{1,6}[ \t].*"},
	{Name: "typescript", Globs: []string{"*.ts", "*.tsx"}, Funcname: typescriptFuncname},
	{Name: "buzz", Globs: []string{"*.buzz"}, Funcname: buzzFuncname},
	{Name: "yaml", Globs: []string{"*.yaml", "*.yml"}, Funcname: yamlFuncname},
}

// yamlFuncname names a top-level YAML key by the key alone (`jobs`); a line under it is
// named `<key>.<sub>` afterwards, by [DiffDriver.Conform] and Declarations.
const yamlFuncname = `^"?([A-Za-z_][A-Za-z0-9_.-]*)"?[[:blank:]]*:([[:blank:]]|$)`

// golangFuncname is git's built-in golang pattern (a func, and a type ... struct or
// interface, at any indentation) plus a top-level var, const or type declaration, single or
// a `(` block. The built-in leaves those to the declaration above them, so a change to a
// table such as `var allDiagnosticCodes` read as a change to the function before it. A
// registration for a built-in driver replaces the built-in, which is why the first two
// lines repeat it.
const golangFuncname = `^[[:blank:]]*(func[[:blank:]]*.*(\{[[:blank:]]*)?)` + "\n" +
	`^[[:blank:]]*(type[[:blank:]].*(struct|interface)[[:blank:]]*(\{[[:blank:]]*)?)` + "\n" +
	`^((var|const|type)[[:blank:]].*)`

// buzzFuncname names Buzz's top-level declarations: fun (export, extern or both), object,
// protocol, enum (enum<str> too) and test blocks. A method is indented, so a change inside
// one is named by its object. [[:blank:]] stands in for [ \t], which a bracket expression
// reads as a backslash and a t.
const buzzFuncname = `^((export[[:blank:]]+)?(extern[[:blank:]]+)?(fun|object|protocol|enum(<[^>]*>)?)[[:blank:]]+[A-Za-z_].*|test[[:blank:]]+".*)$`

// typescriptFuncname names TypeScript declarations. The control-flow rejection comes first
// because `  if (x) {` otherwise reads as a method. Top-level forms anchor at column 0 so a
// local arrow function does not rename the function around it. A method must be indented
// and close its parameter list on the line, which keeps a wrapped call from reading as one.
var typescriptFuncname = strings.Join([]string{
	`!^[[:blank:]]*(if|else|for|while|do|switch|case|catch|return|throw|with|new|await|yield|typeof|delete)([[:blank:](]|$)`,
	`^((export[[:blank:]]+)?(default[[:blank:]]+)?(declare[[:blank:]]+)?(abstract[[:blank:]]+)?(async[[:blank:]]+)?(function[[:blank:]*]|class[[:blank:]]|interface[[:blank:]]|type[[:blank:]]+[A-Za-z_$][A-Za-z0-9_$]*[[:blank:]]*(<.*>)?[[:blank:]]*=|enum[[:blank:]]|namespace[[:blank:]]).*)$`,
	`^((export[[:blank:]]+)?(const|let|var)[[:blank:]]+[A-Za-z_$][A-Za-z0-9_$]*[[:blank:]]*(:[^=]*)?=[[:blank:]]*(async[[:blank:]]+)?(<[^>]*>[[:blank:]]*)?(\(|function[[:blank:]*(]|[A-Za-z_$][A-Za-z0-9_$]*[[:blank:]]*=>).*)$`,
	`^[[:blank:]]+(((public|private|protected|static|readonly|override|async|get|set)[[:blank:]]+)*\*?[A-Za-z_$#][A-Za-z0-9_$]*[[:blank:]]*(<[^>]*>)?[[:blank:]]*\([^;]*\)[[:blank:]]*(:.*)?\{[[:blank:]]*)$`,
}, "\n")

// MatchDiffDriver returns the driver p's base name routes to, and false when none does.
func MatchDiffDriver(p string) (DiffDriver, bool) {
	base := path.Base(p)
	for _, d := range DiffDrivers {
		for _, g := range d.Globs {
			if ok, _ := path.Match(g, base); ok {
				return d, true
			}
		}
	}
	return DiffDriver{}, false
}

// funcnameRule is one compiled line of a Funcname.
type funcnameRule struct {
	re     *regexp.Regexp
	negate bool
}

var compiledFuncnames sync.Map // Funcname -> []funcnameRule

func (d DiffDriver) rules() []funcnameRule {
	if cached, ok := compiledFuncnames.Load(d.Funcname); ok {
		if rules, ok := cached.([]funcnameRule); ok {
			return rules
		}
	}
	var rules []funcnameRule
	for line := range strings.SplitSeq(d.Funcname, "\n") {
		negate := strings.HasPrefix(line, "!")
		// POSIX semantics, leftmost-longest, as git's regexec matches. A pattern Go
		// cannot compile names nothing, the way git skips a line it cannot read.
		re, err := regexp.CompilePOSIX(strings.TrimPrefix(line, "!"))
		if err != nil {
			continue
		}
		rules = append(rules, funcnameRule{re: re, negate: negate})
	}
	compiledFuncnames.Store(d.Funcname, rules)
	return rules
}

// funcnameLimit is git's cap on a hunk header's declaration, in bytes.
const funcnameLimit = 80

// funcname is the declaration line names, as git's hunk header prints it, or "" when
// line is not a declaration.
func (d DiffDriver) funcname(line string) string {
	line = strings.TrimSuffix(line, "\n")
	for _, r := range d.rules() {
		m := r.re.FindStringSubmatchIndex(line)
		if m == nil {
			continue
		}
		if r.negate {
			return ""
		}
		start, end := m[0], m[1]
		if len(m) > 3 && m[2] >= 0 {
			start, end = m[2], m[3]
		}
		name := strings.TrimRight(line[start:end], " \t\r\n\v\f")
		if len(name) > funcnameLimit {
			name = name[:funcnameLimit]
		}
		return strings.TrimSpace(name)
	}
	return ""
}

// Declarations returns the declaration enclosing each line of lines, indexed by line-1: the
// nearest line at or above it d's funcname patterns name, "" above the first one. A line
// that begins inside a string or comment of the driver's language (see OpaqueLines) names
// nothing. Lines inside a magusfile's project call, and under a YAML key, are named by their
// key path (see placeKeys), and the doc comment directly above a declaration is named by it
// (see handLeadersDown). A driver with no patterns places nothing.
func (d DiffDriver) Declarations(lines []string) []string {
	out := make([]string, len(lines))
	if d.Funcname == "" {
		return out
	}
	opaque := d.OpaqueLines(lines)
	current := ""
	for i, l := range lines {
		if name := d.funcname(l); name != "" && !opaque[i] {
			current = name
		}
		out[i] = current
	}
	d.refine(lines, out)
	return out
}

// OpaqueLines reports, indexed by line-1, which lines begin inside a string literal or a
// comment that spans lines, where a funcname pattern matches text rather than a declaration:
// Buzz embedded in a Go raw string has `var i = 0;` at column 0. Only golang is lexed, with
// go/scanner; every other driver reports no line opaque. Source go/scanner cannot read is
// lexed as far as it goes.
func (d DiffDriver) OpaqueLines(lines []string) []bool {
	opaque := make([]bool, len(lines))
	if d.Name != "golang" {
		return opaque
	}
	src := []byte(strings.Join(lines, ""))
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING && tok != token.COMMENT {
			continue
		}
		// The scanner drops a raw string's carriage returns from lit but keeps every newline.
		first := file.Line(pos)
		for line := first + 1; line <= first+strings.Count(lit, "\n") && line <= len(opaque); line++ {
			opaque[line-1] = true
		}
	}
	return opaque
}

// Conform rewrites decls, a placement git's own funcname matching made of lines, so it
// reads as Declarations does: a declaration that begins on a line OpaqueLines reports gives
// way to the one enclosing it, and the keys of a magusfile's project call or a YAML file
// are named by their path (see placeKeys), and a doc comment goes to the declaration below
// it (see handLeadersDown). decls is indexed by line-1 like lines.
func (d DiffDriver) Conform(lines, decls []string) {
	d.dropOpaque(lines, decls)
	d.refine(lines, decls)
}

// refine is what a placement follows from the declarations alone, so Declarations and
// Conform end alike.
func (d DiffDriver) refine(lines, decls []string) {
	d.placeKeys(lines, decls)
	handLeadersDown(decls, lines, declarationLeaders[d.Name])
}

// declarationLeaders are the line prefixes, per diff driver, of what is written directly
// above a declaration and belongs to it: a doc comment, a decorator, an attribute.
var declarationLeaders = map[string][]string{
	"golang":     {"//", "/*", "*"},
	"buzz":       {"//"},
	"typescript": {"//", "/*", "*", "@"},
	"python":     {"#", "@"},
	"rust":       {"//", "/*", "*", "#["},
	"markdown":   {"<!--"},
}

// handLeadersDown gives the run of leader lines directly above each declaration to that
// declaration. git names a line by searching up from it, so without this a doc comment
// belongs to the declaration above, and a claim on a function does not cover an edit to
// its own documentation. The run stops at a blank line or any other line, so a comment
// separated from the declaration by a blank line stays where git put it.
func handLeadersDown(decls, lines, leaders []string) {
	if len(leaders) == 0 {
		return
	}
	leads := func(line string) bool {
		line = strings.TrimLeft(line, " \t")
		return slices.ContainsFunc(leaders, func(p string) bool { return strings.HasPrefix(line, p) })
	}
	for i := 1; i < len(decls); i++ {
		above := decls[i-1]
		if decls[i] == above {
			continue
		}
		for j := i - 1; j >= 0 && decls[j] == above && leads(lines[j]); j-- {
			decls[j] = decls[i]
		}
	}
}

// PlaceHunk returns the declaration of each of lines[from:to], indexed from the first, as
// decls names them except that the blank lines the range starts with go to the declaration
// of the first line after them, when the range has one. A hunk that adds a blank line, a
// doc comment and a declaration between two others starts with the blank line, which a
// placement searching up from it charges to the declaration above, though nothing in that
// declaration changed. A blank line anywhere else in the range keeps its name: it sits
// between lines the change touches, and a function added or deleted with its trailing blank
// line does not touch the function after it. decls is indexed by line-1 like lines.
func PlaceHunk(lines, decls []string, from, to int) []string {
	out := make([]string, to-from)
	for i := from; i < to && i < len(decls); i++ {
		out[i-from] = decls[i]
	}
	lead := from
	for lead < to && lead < len(lines) && strings.TrimSpace(lines[lead]) == "" {
		lead++
	}
	if lead < to {
		for i := from; i < lead; i++ {
			out[i-from] = out[lead-from]
		}
	}
	return out
}

// dropOpaque replaces each declaration in decls that begins on a line OpaqueLines reports
// with the declaration enclosing it. A declaration begins where its text differs from the
// line above's.
func (d DiffDriver) dropOpaque(lines, decls []string) {
	opaque := d.OpaqueLines(lines)
	enclosing, dropping, above := "", false, ""
	for i, decl := range decls {
		if i == 0 || decl != above {
			dropping = decl != "" && i < len(opaque) && opaque[i]
			if !dropping {
				enclosing = decl
			}
		}
		above = decl
		if dropping {
			decls[i] = enclosing
		}
	}
}

// placeKeys names lines a funcname pattern leaves to the declaration above them by the key
// path they sit under, so a claim can hold one key of a shared config file: a magusfile's
// project call for buzz (placeProjectKeys) and a second-level key for yaml (placeYAMLKeys).
// Any other driver's placement is left as it is.
func (d DiffDriver) placeKeys(lines, decls []string) {
	switch d.Name {
	case "buzz":
		placeProjectKeys(lines, decls)
	case "yaml":
		placeYAMLKeys(lines, decls)
	}
}

// projectCallOpen matches the top-level call that declares a magusfile's project, under
// whatever name the magus module was imported as.
var projectCallOpen = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\\project\(\{`)

// quotedKey matches a quoted map key at the start of a trimmed line, capturing the key.
var quotedKey = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"[ \t]*:`)

// placeProjectKeys names the lines of a magusfile's top-level project call, which buzz's
// funcname pattern leaves to the declaration above it: `project` for the call's own lines,
// `project.<key>` for a top-level key, and `project.<key>.<entry>` for an entry of a key
// whose value is a map. A claim can then hold one target's settings
// (`magusfile.buzz#project.targets.test`) where the call is otherwise one declaration of the
// whole file. Lines after the call that were given the declaration above it belong to the
// call too, up to the next declaration; every other line keeps its name.
func placeProjectKeys(lines, decls []string) {
	start := slices.IndexFunc(lines, projectCallOpen.MatchString)
	if start < 0 {
		return
	}
	above := decls[start]
	decls[start] = "project"
	// base is the depth of the lines directly inside the call's map: `"targets": {`.
	base := bracketDepth(lines[start], 0)
	if base <= 0 {
		return
	}
	depth := base
	var key, entry string
	keyIsMap := false
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		at := depth
		switch {
		case at == base && strings.HasPrefix(trimmed, "}"):
			key, entry = "", ""
		case at == base:
			if m := quotedKey.FindStringSubmatch(trimmed); m != nil {
				key, entry = m[1], ""
				keyIsMap = strings.HasPrefix(strings.TrimLeft(trimmed[len(m[0]):], " \t"), "{")
			}
		case at == base+1 && key != "" && (strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, "]")):
			entry = ""
		case at == base+1 && key != "" && keyIsMap:
			if m := quotedKey.FindStringSubmatch(trimmed); m != nil {
				entry = m[1]
			}
		}
		decls[i] = keyPath("project", key, entry)
		depth = bracketDepth(lines[i], depth)
		if depth <= 0 {
			for j := i + 1; j < len(decls) && decls[j] == above; j++ {
				decls[j] = "project"
			}
			return
		}
	}
}

// keyPath joins the non-empty parts of a key path with dots.
func keyPath(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), ".")
}

// bracketDepth is depth after line, counting (, [ and { as opening and their pairs as
// closing, outside strings and line comments. A string does not span lines here; one that
// does is rare enough in a project table that miscounting it costs only its own lines.
func bracketDepth(line string, depth int) int {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return depth
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		}
	}
	return depth
}

// yamlKey matches a mapping key at the start of a trimmed YAML line, capturing the key.
var yamlKey = regexp.MustCompile(`^"?([A-Za-z_][A-Za-z0-9_.-]*)"?[ \t]*:([ \t]|$)`)

// placeYAMLKeys names each line under a top-level YAML key, which yamlFuncname names
// (`jobs`), by the second-level key it sits under: `jobs.stale_after`. The second level is
// the indentation of the first indented line under the key; a deeper line, a list item, a
// comment and a blank line keep the key above them. A top-level key whose value is a block
// scalar (`key: |`) has no second level.
func placeYAMLKeys(lines, decls []string) {
	var top, sub string
	subIndent, block := -1, false
	for i, l := range lines {
		content := strings.TrimRight(l, "\r\n")
		trimmed := strings.TrimLeft(content, " ")
		indent := len(content) - len(trimmed)
		if indent == 0 && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			top, sub, subIndent, block = "", "", -1, false
			if m := yamlKey.FindStringSubmatch(trimmed); m != nil && decls[i] != "" {
				top = decls[i]
				value := strings.TrimSpace(trimmed[len(m[0]):])
				block = strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">")
			}
			continue
		}
		if top == "" || block {
			continue
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if subIndent < 0 {
				subIndent = indent
			}
			if indent == subIndent {
				if m := yamlKey.FindStringSubmatch(trimmed); m != nil {
					sub = m[1]
				}
			}
		}
		decls[i] = keyPath(top, sub)
	}
}

// maxHunkEdits bounds the edit script Hunks computes. The trace a shortest edit script
// keeps grows with its square.
const maxHunkEdits = 2000

// Hunks returns the lines old and new differ in, as RegionChange values for path: each
// changed run on each side, split where the declaration enclosing it changes, the way
// RegionReporter reports a working tree. Declarations come from driver's funcname
// patterns, so they read as the declarations git's hunk headers and a job's claim name;
// the zero DiffDriver places nothing, and every region is then the whole file. The line
// ranges are a shortest edit script, so a hunk's boundary may sit a line away from git's
// histogram diff where both are shortest.
//
// Lines keep their terminators: a CRLF ending or a missing final newline is part of the
// line. It reports false for binary input (a NUL byte) and for sides more than
// maxHunkEdits lines apart, where no line diff is attempted.
func Hunks(path string, old, new []byte, driver DiffDriver) ([]RegionChange, bool) {
	if bytes.IndexByte(old, 0) >= 0 || bytes.IndexByte(new, 0) >= 0 {
		return nil, false
	}
	o, n := SplitLines(old), SplitLines(new)
	pairs, ok := lineMatches(o, n)
	if !ok {
		return nil, false
	}
	var oldDecls, newDecls []string
	if driver.Funcname != "" {
		oldDecls, newDecls = driver.Declarations(o), driver.Declarations(n)
	}
	var regions []RegionChange
	// emit splits the 0-based lines [from, to) of side into one region per declaration.
	emit := func(side RegionSide, from, to int, lines, decls []string) {
		var placed []string
		if decls != nil {
			placed = PlaceHunk(lines, decls, from, to)
		}
		for line := from; line < to; {
			end, decl, name := to-1, "", ""
			if decls != nil {
				decl, name, end = placed[line-from], driver.Name, line
				for end+1 < to && placed[end+1-from] == decl {
					end++
				}
			}
			regions = append(regions, RegionChange{File: FileChange{Path: path}, Side: side, Lines: [2]int{line + 1, end + 1},
				Declaration: decl, Driver: name})
			line = end + 1
		}
	}
	io, in := 0, 0
	for _, p := range append(pairs, [2]int{len(o), len(n)}) {
		emit(RegionOld, io, p[0], o, oldDecls)
		emit(RegionNew, in, p[1], n, newDecls)
		io, in = p[0]+1, p[1]+1
	}
	return regions, true
}

// SplitLines splits b after every '\n', keeping it; a final line without one is kept as
// it is.
func SplitLines(b []byte) []string {
	var lines []string
	s := string(b)
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// lineMatches returns the index pairs of the lines a and b share on a shortest edit
// script, in order, and false when the script would exceed maxHunkEdits.
func lineMatches(a, b []string) ([][2]int, bool) {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	mid, ok := myers(a[pre:len(a)-suf], b[pre:len(b)-suf])
	if !ok {
		return nil, false
	}
	pairs := make([][2]int, 0, pre+len(mid)+suf)
	for i := range pre {
		pairs = append(pairs, [2]int{i, i})
	}
	for _, p := range mid {
		pairs = append(pairs, [2]int{pre + p[0], pre + p[1]})
	}
	for i := range suf {
		pairs = append(pairs, [2]int{len(a) - suf + i, len(b) - suf + i})
	}
	return pairs, true
}

// myers returns the matched index pairs of a shortest edit script from a to b, in order
// (Myers, "An O(ND) Difference Algorithm and Its Variations", 1986).
func myers(a, b []string) ([][2]int, bool) {
	n, m := len(a), len(b)
	limit := n + m
	if limit > maxHunkEdits {
		limit = maxHunkEdits
	}
	off := limit + 1
	v := make([]int, 2*limit+3)
	// trace[d] is v as step d found it, over diagonals -d..d.
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, slices.Clone(v[off-d:off+d+1]))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m), true
			}
		}
	}
	return nil, false
}

func backtrack(trace [][]int, n, m int) [][2]int {
	var pairs [][2]int
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d]
		k := x - y
		prev := k - 1
		if k == -d || (k != d && v[k-1+d] < v[k+1+d]) {
			prev = k + 1
		}
		px := v[prev+d]
		py := px - prev
		for x > px && y > py {
			pairs = append(pairs, [2]int{x - 1, y - 1})
			x, y = x-1, y-1
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		pairs = append(pairs, [2]int{x - 1, y - 1})
		x, y = x-1, y-1
	}
	slices.Reverse(pairs)
	return pairs
}
