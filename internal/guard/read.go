package guard

import (
	"bufio"
	"context"
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

// A whole read of a file the graph has already mapped.
//
// Measured 2026-09-26 over 66,548 shell reads of workspace files in 4,161 transcripts:
// 8,394 dumped a whole Go, Buzz or Markdown file, and nine in ten bounded reads took at
// most 120 lines. A whole read of a longer file is more than a bounded read ever asks
// for, so the deny carries what a bounded read needs: every declaration or heading with
// its lines, and the command that prints one. A bounded read that sits inside one
// indexed symbol is advised with that symbol's command.
//
// Silent whenever the map would be a guess: a short file, a kind no index covers (Buzz
// has no symbol index), a generated output, a path outside the workspace, a stale index,
// a heading count the graph disagrees with, and a read whose output feeds a pipe, a
// redirect or a substitution rather than the reader.

const (
	denyRuleReadNavigation denyRuleName = "read-navigation"
	advisoryReadSymbol     denyRuleName = "read-symbol"
)

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

// parseReadCall reads one invocation as a read of one file. Byte counts, a follow, an
// in-place flag, a regex address and stdin are shapes this rule does not judge.
func parseReadCall(c hint.Invocation) (readCall, bool) {
	name := path.Base(c.Name)
	var rc readCall
	var files []string
	switch {
	case wholeReaders[name]:
		if name == "bat" && hasFlag(c.Args, 'r', "line-range") {
			return readCall{}, false
		}
		files = operands(c.Args, "")
	case name == "head" || name == "tail":
		n, fromLine, rest, ok := lineCountFlag(c.Args)
		if !ok {
			return readCall{}, false
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
			return readCall{}, false
		}
	case name == "sed":
		first, last, rest, ok := sedRange(c.Args)
		if !ok {
			return readCall{}, false
		}
		rc.first, rc.last, files = first, last, rest
	default:
		return readCall{}, false
	}
	if len(files) != 1 || files[0] == "-" {
		return readCall{}, false
	}
	rc.path = files[0]
	return rc, true
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
			if rc, ok := parseReadCall(c); ok {
				out = append(out, rc)
			}
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
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.DplOut, syntax.ClbOut:
		return true
	}
	return false
}

// fileMap is what the graph holds for one file: its declarations or headings, in file
// order, each with the lines it spans.
type fileMap struct {
	rel     string
	lines   int
	entries []mapEntry
	// one prints a single entry's body, or "" when magus has no verb for that yet.
	one func(name string) string
	// list prints the whole map.
	list string
	noun string
}

type mapEntry struct {
	name        string
	first, last int
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

// goFileMap maps a Go file's top-level declarations, each span opening at its doc
// comment. Every name must be one the index vouches for, or the map is no map.
func goFileMap(deps Dependencies, abs, rel string) (fileMap, bool) {
	f, err := os.Open(abs)
	if err != nil {
		return fileMap{}, false
	}
	defer f.Close()
	var entries []mapEntry
	lines, commentStart := 0, 0
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for s.Scan() {
		lines++
		text := s.Text()
		if strings.HasPrefix(text, "//") {
			if commentStart == 0 {
				commentStart = lines
			}
			continue
		}
		if m := goDeclRe.FindStringSubmatch(text); m != nil {
			if defined, definitive := deps.symbolDefined(m[1]); !defined || !definitive {
				return fileMap{}, false
			}
			first := lines
			if commentStart != 0 {
				first = commentStart
			}
			entries = append(entries, mapEntry{name: m[1], first: first})
		}
		commentStart = 0
	}
	if s.Err() != nil || len(entries) == 0 {
		return fileMap{}, false
	}
	return fileMap{
		rel:     rel,
		lines:   lines,
		entries: closeSpans(entries, lines),
		one:     func(name string) string { return hint.Refs.With(name, "--definition", "--source") },
		list:    hint.Explain.With(types.KindFile + ":" + rel),
		noun:    "declaration",
	}, true
}

var headingRe = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)

// markdownFileMap maps a Markdown file's headings, fenced ones skipped, and holds only
// when the graph carries exactly one section node per heading found.
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
		if m := headingRe.FindStringSubmatch(text); m != nil && m[2] != "" {
			entries = append(entries, mapEntry{name: m[1] + " " + m[2], first: lines})
		}
	}
	if s.Err() != nil || len(entries) == 0 {
		return fileMap{}, false
	}
	ids, definitive := deps.graphIDs(context.Background(), types.KindDocSection)
	if !definitive {
		return fileMap{}, false
	}
	prefix := types.KindDocSection + ":" + rel + "#"
	held := 0
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			held++
		}
	}
	if held != len(entries) {
		return fileMap{}, false
	}
	return fileMap{
		rel:     rel,
		lines:   lines,
		entries: closeSpans(entries, lines),
		list:    hint.Query.With("kind="+types.KindDocSection, "'id=~^"+regexp.QuoteMeta(prefix)+"'", "-o", "name"),
		noun:    "heading",
	}, true
}

// mapFor maps rel by its kind, or reports false for a kind the graph does not map.
func mapFor(deps Dependencies, abs, rel string) (fileMap, bool) {
	switch path.Ext(rel) {
	case ".go":
		return goFileMap(deps, abs, rel)
	case ".md":
		return markdownFileMap(deps, abs, rel)
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
	return err == nil && len(files) == 1 && files[0].Role == "output"
}

// readVerdict denies the first whole read on the line of a mapped file longer than
// wholeReadLines, advises the first bounded read that sits inside one indexed symbol,
// and reports false when neither is there.
func readVerdict(deps Dependencies, command string, d Dialect) (ShellVerdict, bool) {
	dir, err := os.Getwd()
	if err != nil {
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
		if !ok {
			continue
		}
		m, ok := mapFor(deps, abs, rel)
		if !ok {
			continue
		}
		if rc.whole(m.lines) {
			if m.lines <= wholeReadLines || generatedOutput(deps, rel) {
				continue
			}
			return ShellVerdict{Deny: denyReadNavigation(m), Rule: denyRule{Name: denyRuleReadNavigation, Arg: rel}}, true
		}
		if m.one == nil {
			continue
		}
		first, last := rc.span(m.lines)
		if e, ok := m.covering(first, last); ok {
			return ShellVerdict{Context: adviseReadSymbol(m, e, first, last), Rule: denyRule{Name: advisoryReadSymbol, Arg: e.name}}, true
		}
	}
	return ShellVerdict{}, false
}

func denyReadNavigation(m fileMap) string {
	var b strings.Builder
	b.WriteString("`" + m.list + "` maps this file, and the map is below: " + strconv.Itoa(m.lines) + " lines, " + countNoun(len(m.entries), m.noun) + ", each with its lines.\n")
	if m.one != nil {
		b.WriteString("A whole read spends " + strconv.Itoa(m.lines) + " lines to reach one of them; `" + m.one("<name>") + "` prints that one, numbered and checked against the index.\n")
	} else {
		b.WriteString("A whole read spends " + strconv.Itoa(m.lines) + " lines to reach one section; read that section by its line range.\n")
	}
	b.WriteString("What the file holds (" + countNoun(len(m.entries), m.noun) + "):")
	for i, e := range m.entries {
		if i == mapCap {
			b.WriteString("\n  ... " + strconv.Itoa(len(m.entries)-mapCap) + " more; `" + m.list + "` lists them all")
			break
		}
		b.WriteString("\n  " + strconv.Itoa(e.first) + "-" + strconv.Itoa(e.last) + ": " + e.name)
	}
	return b.String()
}

func adviseReadSymbol(m fileMap, e mapEntry, first, last int) string {
	return "magus workspace: lines " + strconv.Itoa(first) + "-" + strconv.Itoa(last) + " of " + m.rel + " sit inside `" + e.name + "` (" +
		strconv.Itoa(e.first) + "-" + strconv.Itoa(e.last) + "). `" + m.one(e.name) + "` prints that body with its line numbers, checked against the index, and finds it again after the file moves."
}
