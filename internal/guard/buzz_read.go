package guard

import (
	"os"
	"strconv"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// buzzFileMap maps a Buzz file's top-level fun, test, object and enum declarations from a
// parse of the file. Nothing indexes Buzz symbols for refs to print, so every entry is read
// by its lines, and a file that does not parse has no map.
func buzzFileMap(abs, rel string) (fileMap, bool) {
	src, err := os.ReadFile(abs)
	if err != nil {
		return fileMap{}, false
	}
	prog, err := buzz.ParseEmbedded(string(src))
	if err != nil {
		return fileMap{}, false
	}
	text := strings.Split(string(src), "\n")
	lines := lineCount(src)
	// Every top-level statement bounds the one before it, named or not: a declaration ends
	// where the next statement, or the comment documenting it, begins.
	var starts []int
	var entries []mapEntry
	var named []int
	for _, stmt := range prog.Stmts {
		line := ast.NodePos(stmt).Line
		if line <= 0 {
			continue
		}
		start := docStart(text, line)
		starts = append(starts, start)
		if name := buzzDeclName(stmt); name != "" {
			entries = append(entries, mapEntry{name: name, first: start})
			named = append(named, len(starts)-1)
		}
	}
	if len(entries) == 0 {
		return fileMap{}, false
	}
	for i, at := range named {
		end := lines
		if at+1 < len(starts) {
			end = starts[at+1] - 1
		}
		entries[i].last = lastContentLine(text, entries[i].first, end)
	}
	return fileMap{rel: rel, lines: lines, entries: entries, noun: "declaration"}, true
}

// buzzDeclName names a declaration a reader looks for by name, "" for any other statement.
func buzzDeclName(stmt ast.Node) string {
	switch d := stmt.(type) {
	case *ast.FunDecl:
		return "fun " + d.Name
	case *ast.TestDecl:
		return "test " + strconv.Quote(d.Name)
	case *ast.ObjectDecl:
		if d.IsProtocol {
			return "protocol " + d.Name
		}
		return "object " + d.Name
	case *ast.EnumDecl:
		return "enum " + d.Name
	}
	return ""
}

// docStart is the first line of the comment block directly above line (1-based), or line
// when none is.
func docStart(text []string, line int) int {
	for line > 1 && strings.HasPrefix(strings.TrimSpace(text[line-2]), "//") {
		line--
	}
	return line
}

// lastContentLine is the last non-blank line in first..last, so a span stops at its own
// closing brace rather than the blank lines after it.
func lastContentLine(text []string, first, last int) int {
	for last > first && last <= len(text) && strings.TrimSpace(text[last-1]) == "" {
		last--
	}
	return last
}
