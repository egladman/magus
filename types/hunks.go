package types

import (
	"bytes"
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
}

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

// DiffDriverFor returns the driver p's base name routes to, and false when none does.
func DiffDriverFor(p string) (DiffDriver, bool) {
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
// nearest line at or above it d's funcname patterns name, "" above the first one. A driver
// with no patterns places nothing.
func (d DiffDriver) Declarations(lines []string) []string {
	out := make([]string, len(lines))
	if d.Funcname == "" {
		return out
	}
	current := ""
	for i, l := range lines {
		if name := d.funcname(l); name != "" {
			current = name
		}
		out[i] = current
	}
	return out
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
	emit := func(side RegionSide, from, to int, decls []string) {
		for line := from; line < to; {
			end, decl, name := to-1, "", ""
			if decls != nil {
				decl, name, end = decls[line], driver.Name, line
				for end+1 < to && decls[end+1] == decl {
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
		emit(RegionOld, io, p[0], oldDecls)
		emit(RegionNew, in, p[1], newDecls)
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

// LineMatches returns the index pairs of the lines a and b share on a shortest edit
// script, in order, and false when the script would exceed maxHunkEdits.
func LineMatches(a, b []string) ([][2]int, bool) { return lineMatches(a, b) }

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
