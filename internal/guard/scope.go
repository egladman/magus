package guard

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// workspaceScope is where the judged call runs: the workspace root and the home directory
// a `~` resolves against. Judge fills it; a zero scope knows no root and recognizes only a
// temp or scratchpad path as outside, which is what the pure rule tests exercise.
type workspaceScope struct {
	root string
	home string
}

// scopeAt is the scope of a call graded at loc. An unreadable home leaves `~` unresolved,
// which keeps every home-relative path in scope.
func scopeAt(loc location) workspaceScope {
	home, _ := os.UserHomeDir()
	return workspaceScope{root: loc.workspace, home: home}
}

// unresolved prefixes a word whose value the line does not determine, such as a variable
// nobody assigned on it. Such a word is never provably outside anything.
const unresolved = "\x00"

// outside reports a path that provably lands outside the workspace. A relative path
// resolves from the call's directory, which is inside, so only an absolute or home-relative
// path can be outside.
func (s workspaceScope) outside(p string) bool {
	if p == "" || strings.HasPrefix(p, unresolved) {
		return false
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if s.home == "" {
			return false
		}
		p = s.home + p[1:]
	}
	if !filepath.IsAbs(p) {
		return false
	}
	if s.root == "" {
		return throwawayDirRe.MatchString(p)
	}
	// macOS reaches /tmp and /var through /private, and the two sides may arrive under
	// different spellings of the same directory.
	norm := func(x string) string { return strings.TrimPrefix(filepath.Clean(x), "/private") }
	rel, err := filepath.Rel(norm(s.root), norm(p))
	return err == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// lineOutside reports a line whose every path operand provably lands outside the
// workspace. A line naming no path at all works on the call's directory, so it is not.
func (s workspaceScope) lineOutside(command string, d Dialect) bool {
	paths, ok := operandPaths(command, d, s.home)
	if !ok || len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !s.outside(p) {
			return false
		}
	}
	return true
}

// operandPaths names every word on the line that a command could be reading or writing:
// redirect targets, and each command's operands minus its flags, their values, and a
// search's patterns. Over-collecting is the safe direction: an extra relative word counts
// as inside and keeps the line in scope.
func operandPaths(command string, d Dialect, home string) ([]string, bool) {
	f, err := parseFile(command, d)
	if err != nil {
		return nil, false
	}
	vars := map[string]string{}
	for _, m := range assignmentRe.FindAllStringSubmatch(command, -1) {
		vars[m[1]] = m[3]
	}
	if home != "" {
		vars["HOME"] = home
	}
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		stmt, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		for _, r := range stmt.Redirs {
			switch r.Op {
			case syntax.DplOut, syntax.DplIn, syntax.WordHdoc, syntax.Hdoc, syntax.DashHdoc:
				continue
			}
			if r.Word != nil {
				out = append(out, scopeWord(command, r.Word, vars))
			}
		}
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok {
			return true
		}
		words := make([]string, len(call.Args))
		for i, w := range call.Args {
			words[i] = scopeWord(command, w, vars)
		}
		for _, c := range peelWrappers(words, d) {
			out = append(out, invocationPaths(c)...)
		}
		return true
	})
	return out, true
}

// scopeWord renders a word to the path it names. A word built from a variable is resolved
// from assignments made earlier on the line; one that cannot be is marked unresolved.
func scopeWord(command string, w *syntax.Word, vars map[string]string) string {
	if literalOnly(w.Parts) {
		return literalWord(w.Parts)
	}
	expanded := expandGuardVars(rawWord(command, w), vars)
	if strings.ContainsAny(expanded, "$`") {
		return unresolved + expanded
	}
	return expanded
}

func literalOnly(parts []syntax.WordPart) bool {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			if !literalOnly(p.Parts) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// scopeValueFlags are the short flags that consume the next word, per command, so a flag's
// value is not read as a path.
var scopeValueFlags = map[string]string{
	"grep": "efmABCDd", "egrep": "efmABCDd", "fgrep": "efmABCDd",
	"rg": "efgmtTABCEM", "ag": "gGmABC",
	"head": "nc", "tail": "nc",
}

// invocationPaths is the operands of one command that name a file or directory.
func invocationPaths(c hint.Invocation) []string {
	name := path.Base(c.Name)
	switch {
	case name == "find":
		// find's paths lead; everything from the first expression on is a predicate.
		var out []string
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-") || a == "(" || a == "!" {
				break
			}
			out = append(out, a)
		}
		return out
	case name == "sed":
		files, _ := hint.SedFiles(c.Args)
		return files
	}
	ops := operands(c.Args, scopeValueFlags[name])
	// A search's first operand is its pattern unless -e or -f supplied one.
	if hint.IsSearchTool(name) && len(ops) > 0 && !hasFlag(c.Args, 'e', "regexp") && !hasFlag(c.Args, 'f', "file") {
		ops = ops[1:]
	}
	return ops
}
