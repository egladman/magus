package guard

import (
	"bufio"
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The read-navigation rule answers a whole read of a mapped file with its map, and is
// silent wherever the map would be a guess. The catalog's Why carries the measurements
// and the silent cases.

// wholeReadLines is the p90 length of a bounded read.
const wholeReadLines = 120

// mapCap bounds the inline map; the file node lists the rest.
const mapCap = 60

// readCall is one command printing a file to the reader: the whole of it, or the lines
// first..last. tail is the last N lines, placed once the length is known.
type readCall struct {
	path        string
	first, last int
	tail        int
}

func (rc readCall) whole(lines int) bool {
	first, last := rc.span(lines)
	return first <= 1 && last >= lines
}

// span is the 1-based inclusive range the call prints in a file of the given length.
func (rc readCall) span(lines int) (first, last int) {
	if rc.tail > 0 {
		return max(1, lines-rc.tail+1), lines
	}
	if rc.first == 0 {
		return 1, lines
	}
	if rc.last == 0 || rc.last > lines {
		return rc.first, lines
	}
	return rc.first, rc.last
}

var wholeReaders = map[string]bool{"cat": true, "bat": true, "less": true, "more": true, "nl": true}

// sedRangeRe is a quiet sed's line-address print: `10,40p`, `10p`, `10,$p`, `10,+5p`.
var sedRangeRe = regexp.MustCompile(`^(\d+)(?:,(\$|\+?\d+))?p$`)

// parseReadCalls reads one invocation as the files it prints. A whole reader prints every
// operand, so each is its own read; head, tail and sed are judged only on one file, since
// their counts and addresses read differently across several. Byte counts, a follow, an
// in-place flag, a regex address and stdin are shapes this rule does not judge.
func parseReadCalls(c hint.Invocation) []readCall {
	name := path.Base(c.Name)
	var rc readCall
	var files []string
	switch {
	case wholeReaders[name]:
		if name == "bat" && hasFlag(c.Args, 'r', "line-range") {
			return nil
		}
		files = operands(c.Args, "")
		if slices.Contains(files, "-") {
			return nil
		}
		out := make([]readCall, len(files))
		for i, f := range files {
			out[i] = readCall{path: f}
		}
		return out
	case name == "head" || name == "tail":
		n, fromLine, rest, ok := lineCountFlag(c.Args)
		if !ok {
			return nil
		}
		files = rest
		switch {
		case name == "head" && !fromLine:
			rc.first, rc.last = 1, n
		case name == "tail" && fromLine:
			rc.first = n
		case name == "tail":
			rc.tail = n
		default:
			return nil
		}
	case name == "sed":
		first, last, rest, ok := sedRange(c.Args)
		if !ok {
			return nil
		}
		rc.first, rc.last, files = first, last, rest
	default:
		return nil
	}
	if len(files) != 1 || files[0] == "-" {
		return nil
	}
	rc.path = files[0]
	return []readCall{rc}
}

// lineCountFlag reads head's and tail's count: `-n 30`, `-n30`, `-30`, `--lines=30`, and
// tail's `-n +30`, which fromLine reports. Ten when none is given, as the tools default.
func lineCountFlag(args []string) (n int, fromLine bool, files []string, ok bool) {
	n = 10
	value := func(v string) bool {
		if strings.HasPrefix(v, "+") {
			fromLine = true
			v = v[1:]
		}
		i, err := strconv.Atoi(v)
		if err != nil || i <= 0 {
			return false
		}
		n = i
		return true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			files = append(files, args[i+1:]...)
			return n, fromLine, files, true
		case strings.HasPrefix(a, "--lines="):
			ok = value(strings.TrimPrefix(a, "--lines="))
		case a == "--lines" || a == "-n":
			if i+1 >= len(args) {
				return 0, false, nil, false
			}
			i++
			ok = value(args[i])
		case strings.HasPrefix(a, "-n"):
			ok = value(a[2:])
		case strings.HasPrefix(a, "-") && len(a) > 1 && a[1] >= '0' && a[1] <= '9':
			ok = value(a[1:])
		case a == "-c" || strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "--bytes"),
			a == "-f" || a == "-F" || strings.HasPrefix(a, "--follow"):
			return 0, false, nil, false
		case strings.HasPrefix(a, "-") && a != "-":
			continue
		default:
			files = append(files, a)
			continue
		}
		if !ok {
			return 0, false, nil, false
		}
	}
	return n, fromLine, files, true
}

// sedRange reads a quiet sed printing one line range. last is 0 for `$`.
func sedRange(args []string) (first, last int, files []string, ok bool) {
	quiet := false
	var scripts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			files = append(files, args[i+1:]...)
			i = len(args)
		case a == "-e" || a == "--expression":
			if i+1 >= len(args) {
				return 0, 0, nil, false
			}
			i++
			scripts = append(scripts, args[i])
		case strings.HasPrefix(a, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(a, "--expression="))
		case a == "--quiet" || a == "--silent":
			quiet = true
		case strings.HasPrefix(a, "--"):
			return 0, 0, nil, false
		case strings.HasPrefix(a, "-") && a != "-":
			if strings.ContainsAny(a[1:], "if") {
				return 0, 0, nil, false
			}
			if strings.Contains(a, "n") {
				quiet = true
			}
			if strings.HasSuffix(a, "e") {
				if i+1 >= len(args) {
					return 0, 0, nil, false
				}
				i++
				scripts = append(scripts, args[i])
			}
		case len(scripts) == 0:
			scripts = append(scripts, a)
		default:
			files = append(files, a)
		}
	}
	if !quiet || len(scripts) != 1 {
		return 0, 0, nil, false
	}
	m := sedRangeRe.FindStringSubmatch(strings.TrimSpace(scripts[0]))
	if m == nil {
		return 0, 0, nil, false
	}
	first, _ = strconv.Atoi(m[1])
	switch {
	case m[2] == "":
		last = first
	case m[2] == "$":
		last = 0
	case strings.HasPrefix(m[2], "+"):
		n, _ := strconv.Atoi(m[2][1:])
		last = first + n
	default:
		last, _ = strconv.Atoi(m[2])
	}
	if first == 0 || (last != 0 && last < first) {
		return 0, 0, nil, false
	}
	return first, last, files, true
}

// readCalls lists the reads on a line whose output reaches the reader.
func readCalls(command string, d Dialect) []readCall {
	f, err := parseFile(command, d)
	if err != nil {
		return nil
	}
	var out []readCall
	var stack []syntax.Node
	syntax.Walk(f, func(n syntax.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		call, ok := n.(*syntax.CallExpr)
		if !ok || stdoutConsumed(stack) {
			return true
		}
		for _, c := range peelWrappers(literalWords(call.Args), d) {
			out = append(out, parseReadCalls(c)...)
		}
		return true
	})
	return out
}

// stdoutConsumed reports whether something on the line takes the innermost command's
// output before the reader would: the left side of a pipe, a redirect, a substitution.
func stdoutConsumed(stack []syntax.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *syntax.Stmt:
			if slices.ContainsFunc(n.Redirs, redirectsStdout) {
				return true
			}
		case *syntax.BinaryCmd:
			if (n.Op == syntax.Pipe || n.Op == syntax.PipeAll) && stack[i+1] == syntax.Node(n.X) {
				return true
			}
		case *syntax.CmdSubst, *syntax.ProcSubst:
			return true
		}
	}
	return false
}

func redirectsStdout(r *syntax.Redirect) bool {
	if r.N != nil && r.N.Value != "1" {
		return false
	}
	switch r.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.DplOut, syntax.RdrClob:
		return true
	}
	return false
}

// fileMap is one file's declarations or headings, in file order, each with the lines it
// spans, parsed from the file itself so it holds whether or not the index is current.
type fileMap struct {
	rel     string
	lines   int
	entries []mapEntry
	// indexed is true when the symbol index vouches for every entry, so refs prints one
	// checked against it; otherwise an entry is read by its lines.
	indexed bool
	// list prints the whole map from the graph, "" when the graph cannot vouch for it.
	list string
	noun string
}

type mapEntry struct {
	name        string
	first, last int
	// symbol is what refs takes, "" for a method or an entry refs cannot print: refs
	// resolves bare names only, so a method's command would print every same-named method.
	symbol string
}

// read is the command that prints one entry: refs where the index vouches for it, its
// line range otherwise.
func (m fileMap) read(e mapEntry) string {
	if m.indexed && e.symbol != "" {
		return hint.Refs.With(e.symbol, "--definition", "--source")
	}
	return sedRangeCommand(m.rel, e.first, e.last)
}

// sedRangeCommand prints lines first..last of rel, the one read every file kind has.
func sedRangeCommand(rel string, first, last int) string {
	return "sed -n " + strconv.Itoa(first) + "," + strconv.Itoa(last) + "p " + shellWord(rel)
}

// shellWord quotes s for a shell only when it needs it, so a plain path reads as typed.
func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"$`\\*?[]{}()&;|<>!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// covering is the one entry whose span holds first..last.
func (m fileMap) covering(first, last int) (mapEntry, bool) {
	for _, e := range m.entries {
		if e.first <= first && last <= e.last {
			return e, true
		}
	}
	return mapEntry{}, false
}

// closeSpans ends each entry where the next begins.
func closeSpans(entries []mapEntry, lines int) []mapEntry {
	for i := range entries {
		if i+1 < len(entries) {
			entries[i].last = entries[i+1].first - 1
		} else {
			entries[i].last = lines
		}
	}
	return entries
}

// goFileMap maps a Go file's top-level declarations from a live parse. It is indexed, and
// lists through the graph, only when the index vouches for every name; a stale index
// still gets the map, read by line ranges.
func goFileMap(deps Dependencies, abs, rel string) (fileMap, bool) {
	entries, lines, ok := goDecls(abs)
	if !ok || len(entries) == 0 {
		return fileMap{}, false
	}
	indexed := !slices.ContainsFunc(entries, func(e mapEntry) bool {
		defined, definitive := deps.symbolDefined(e.name[strings.LastIndexByte(e.name, '.')+1:])
		return !defined || !definitive
	})
	m := fileMap{rel: rel, lines: lines, entries: entries, indexed: indexed, noun: "declaration"}
	if indexed {
		m.list = hint.Explain.With(types.KindFile + ":" + rel)
	}
	return m, true
}

// goDecls parses a Go file's top-level declarations, each span running from its doc
// comment to its closing brace or paren, each member of a grouped var, const or type its
// own entry, and a method named `Type.Method`. lines is the file's length.
func goDecls(abs string) (entries []mapEntry, lines int, ok bool) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, 0, false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, 0, false
	}
	add := func(name, symbol string, doc *ast.CommentGroup, from, to token.Pos) {
		if name[strings.LastIndexByte(name, '.')+1:] == "_" {
			return
		}
		if doc != nil {
			from = doc.Pos()
		}
		entries = append(entries, mapEntry{name: name, symbol: symbol, first: fset.Position(from).Line, last: fset.Position(to).Line})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				add(d.Name.Name, d.Name.Name, d.Doc, d.Pos(), d.End())
			} else {
				add(receiverType(d.Recv)+"."+d.Name.Name, "", d.Doc, d.Pos(), d.End())
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			for _, spec := range d.Specs {
				doc, from, to := d.Doc, d.Pos(), d.End()
				if d.Lparen.IsValid() {
					doc, from, to = nil, spec.Pos(), spec.End()
				}
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if d.Lparen.IsValid() {
						doc = s.Doc
					}
					add(s.Name.Name, s.Name.Name, doc, from, to)
				case *ast.ValueSpec:
					if d.Lparen.IsValid() {
						doc = s.Doc
					}
					for _, n := range s.Names {
						add(n.Name, n.Name, doc, from, to)
					}
				}
			}
		}
	}
	return entries, lineCount(src), true
}

// lineCount counts a file's lines, a last line without its newline included.
func lineCount(src []byte) int {
	lines := bytes.Count(src, []byte("\n"))
	if len(src) > 0 && src[len(src)-1] != '\n' {
		lines++
	}
	return lines
}

// receiverType is a method receiver's type name, pointer and type arguments dropped.
func receiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

var headingRe = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)

// markdownFileMap maps a Markdown file's headings, fenced ones skipped. It lists through
// the graph only when the graph carries exactly one section node per heading found.
func markdownFileMap(deps Dependencies, abs, rel string) (fileMap, bool) {
	f, err := os.Open(abs)
	if err != nil {
		return fileMap{}, false
	}
	defer f.Close()
	var entries []mapEntry
	lines, fence := 0, ""
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for s.Scan() {
		lines++
		text := s.Text()
		trimmed := strings.TrimLeft(text, " ")
		if marker := fenceMarker(trimmed); marker != "" && len(text)-len(trimmed) < 4 {
			switch {
			case fence == "":
				fence = marker
			case strings.HasPrefix(trimmed, fence) && strings.TrimSpace(strings.TrimLeft(trimmed, fence[:1])) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if m := headingRe.FindStringSubmatch(text); len(m) > 2 && m[2] != "" {
			entries = append(entries, mapEntry{name: m[1] + " " + m[2], first: lines})
		}
	}
	if s.Err() != nil || len(entries) == 0 {
		return fileMap{}, false
	}
	m := fileMap{rel: rel, lines: lines, entries: closeSpans(entries, lines), noun: "heading"}
	prefix := types.KindDocSection + ":" + rel + "#"
	if ids, definitive := deps.graphIDs(context.Background(), types.KindDocSection); definitive {
		held := 0
		for _, id := range ids {
			if strings.HasPrefix(id, prefix) {
				held++
			}
		}
		if held == len(entries) {
			m.list = hint.Query.With("kind="+types.KindDocSection, "'id=~^"+regexp.QuoteMeta(prefix)+"'", "-o", "name")
		}
	}
	return m, true
}

// wholeReadFiles are the files written to be read whole: an agent's instructions and a
// skill, whose every section applies at once.
var wholeReadFiles = baseNames("SKILL.md", "AGENTS.md", "./CLAUDE.md")

// baseNames keys a set by each path's last element, so a host's file is written as the
// path it is rather than as a bare host name.
func baseNames(paths ...string) map[string]bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[path.Base(p)] = true
	}
	return set
}

// buildFileMap maps rel by its kind, or reports false for a kind with no parser here. TypeScript
// has none, so it stays silent.
func buildFileMap(deps Dependencies, abs, rel string) (fileMap, bool) {
	switch path.Ext(rel) {
	case ".go":
		return goFileMap(deps, abs, rel)
	case ".md":
		return markdownFileMap(deps, abs, rel)
	case ".buzz":
		return buzzFileMap(abs, rel)
	}
	return fileMap{}, false
}

// generatedOutput reports a path the workspace declares as a target's output. A caller
// that cannot classify leaves the map to speak for itself.
func generatedOutput(deps Dependencies, rel string) bool {
	ctx := context.Background()
	ws, err := deps.inspect(ctx, "")
	if err != nil || ws == nil {
		return false
	}
	files, err := ws.ClassifyFiles(ctx, []string{rel})
	return err == nil && len(files) == 1 && files[0].Role == types.DiffRoleOutput
}

// readVerdict denies the first whole read on the line of a mapped file longer than
// wholeReadLines, advises the first bounded read that sits inside one indexed symbol,
// and reports false when neither is there.
func readVerdict(deps Dependencies, command string, d Dialect) (ShellVerdict, bool) {
	dir, ok := deps.workingDir()
	if !ok {
		return ShellVerdict{}, false
	}
	return readVerdictAt(deps, dir, command, d)
}

func readVerdictAt(deps Dependencies, dir, command string, d Dialect) (ShellVerdict, bool) {
	if deps.scope.root == "" {
		return ShellVerdict{}, false
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return ShellVerdict{}, false
	}
	for _, rc := range readCalls(command, d) {
		abs, rel, ok := workspacePath(root, dir, rc.path)
		if !ok || wholeReadFiles[path.Base(rel)] {
			continue
		}
		m, ok := buildFileMap(deps, abs, rel)
		if !ok {
			continue
		}
		if rc.whole(m.lines) {
			if m.lines <= wholeReadLines || generatedOutput(deps, rel) {
				continue
			}
			return ShellVerdict{Deny: denyReadNavigation(m), Rule: denyRule{Name: denyRuleReadNavigation, Arg: rel}}, true
		}
		if !m.indexed {
			continue
		}
		first, last := rc.span(m.lines)
		if e, ok := m.covering(first, last); ok && e.symbol != "" {
			return ShellVerdict{Context: adviseReadSymbol(m, e, first, last), Rule: denyRule{Name: advisoryReadSymbol, Arg: e.name}}, true
		}
	}
	return ShellVerdict{}, false
}

func denyReadNavigation(m fileMap) string {
	var b strings.Builder
	if m.list != "" {
		b.WriteString("`" + m.list + "` maps this file, and the map is below: ")
	} else {
		b.WriteString("The map below is parsed from the file itself: ")
	}
	b.WriteString(strconv.Itoa(m.lines) + " lines, " + countNoun(len(m.entries), m.noun) + ", each with its lines.\n")
	byLines := "`sed -n <first>,<last>p " + shellWord(m.rel) + "`"
	switch {
	case m.indexed && slices.ContainsFunc(m.entries, func(e mapEntry) bool { return e.symbol == "" }):
		b.WriteString("A whole read spends " + strconv.Itoa(m.lines) + " lines to reach one of them; `" + hint.Refs.With("<name>", "--definition", "--source") +
			"` prints that one, numbered and checked against the index, and " + byLines + " prints a method by its lines.\n")
	case m.indexed:
		b.WriteString("A whole read spends " + strconv.Itoa(m.lines) + " lines to reach one of them; `" + hint.Refs.With("<name>", "--definition", "--source") +
			"` prints that one, numbered and checked against the index.\n")
	default:
		b.WriteString("A whole read spends " + strconv.Itoa(m.lines) + " lines to reach one of them; " + byLines + " prints that one by its lines.\n")
	}
	b.WriteString("What the file holds (" + countNoun(len(m.entries), m.noun) + "):")
	for i, e := range m.entries {
		if i == mapCap {
			b.WriteString("\n  ... " + strconv.Itoa(len(m.entries)-mapCap) + " more")
			if m.list != "" {
				b.WriteString("; `" + m.list + "` lists them all")
			}
			break
		}
		b.WriteString("\n  " + strconv.Itoa(e.first) + "-" + strconv.Itoa(e.last) + ": " + e.name)
	}
	return b.String()
}

func adviseReadSymbol(m fileMap, e mapEntry, first, last int) string {
	return "magus workspace: lines " + strconv.Itoa(first) + "-" + strconv.Itoa(last) + " of " + m.rel + " sit inside `" + e.name + "` (" +
		strconv.Itoa(e.first) + "-" + strconv.Itoa(e.last) + "). `" + m.read(e) + "` prints that body with its line numbers, checked against the index, and finds it again after the file moves."
}
