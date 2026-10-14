package guard

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// workspaceScope is where the judged call runs: the workspace root, the directory the call
// runs in, and the home directory a `~` resolves against. Judge fills it; a zero scope knows
// no root and recognizes only a temp or scratchpad path as outside, which is what the pure
// rule tests exercise.
type workspaceScope struct {
	root string
	dir  string
	home string
}

// scopeAt is the scope of a call graded at loc. An unreadable home leaves `~` unresolved,
// which keeps every home-relative path in scope.
func scopeAt(loc location) workspaceScope {
	return workspaceScope{root: loc.workspace, dir: loc.dir, home: userHome()}
}

// expandHome resolves a leading `~` against home, as the shell does before the command sees
// the word, so `~/x` is never taken for a path under the call's directory. With home
// unknown, p comes back unexpanded, and each caller says what an unplaceable path means.
func expandHome(p, home string) string {
	if home == "" || (p != "~" && !strings.HasPrefix(p, "~/")) {
		return p
	}
	return home + p[1:]
}

// userHome is the home directory a `~` in a judged path resolves against, "" when unknown.
func userHome() string {
	home, _ := os.UserHomeDir()
	return home
}

// unresolved prefixes a word whose value the line does not determine, such as a variable
// nobody assigned on it. Such a word is never provably outside anything.
const unresolved = "\x00"

// placePath is where the shell opens p, a word as placeableWord renders it, from the directory
// cwd: a leading `~` is the home's, an absolute path is itself, and a relative one is cwd's.
// It is false when the line does not determine the place: a word built from an expansion,
// a `~user`, a `~` with the home unknown, or a relative path with cwd unknown.
func placePath(p, cwd, home string) (string, bool) {
	switch {
	case p == "" || strings.HasPrefix(p, unresolved):
		return "", false
	case p == "~" || strings.HasPrefix(p, "~/"):
		if home == "" {
			return "", false
		}
		return filepath.Clean(home + p[1:]), true
	case strings.HasPrefix(p, "~"):
		return "", false
	case filepath.IsAbs(p):
		return filepath.Clean(p), true
	case cwd == "":
		return "", false
	}
	return filepath.Join(cwd, p), true
}

// placeableWord renders w for placePath: the text the shell passes, with an unquoted leading `~`
// kept for placePath to expand, a leading $HOME or ${HOME} spelled `~`, a quoted leading `~`
// spelled `./~` because the shell leaves it a name, and any other expansion marked
// unresolved rather than dropped.
func placeableWord(command string, w *syntax.Word) string {
	if w == nil || len(w.Parts) == 0 {
		return ""
	}
	parts, home := w.Parts, false
	switch first := parts[0].(type) {
	case *syntax.ParamExp:
		if home = homeParam(command, first); home {
			parts = parts[1:]
		}
	case *syntax.DblQuoted:
		if len(first.Parts) > 0 {
			if p, ok := first.Parts[0].(*syntax.ParamExp); ok && homeParam(command, p) {
				home = true
				parts = append([]syntax.WordPart{&syntax.DblQuoted{Parts: first.Parts[1:]}}, parts[1:]...)
			}
		}
	}
	text, literal := plainWord(&syntax.Word{Parts: parts})
	switch {
	case !literal:
		return unresolved + command[w.Pos().Offset():w.End().Offset()]
	case home:
		return "~" + text
	case strings.HasPrefix(text, "~"):
		if lit, ok := parts[0].(*syntax.Lit); !ok || !strings.HasPrefix(lit.Value, "~") {
			return "./" + text
		}
	}
	return text
}

// homeParam reports a parameter expansion that is exactly $HOME or ${HOME}.
func homeParam(command string, p *syntax.ParamExp) bool {
	src := command[p.Pos().Offset():p.End().Offset()]
	return src == "$HOME" || src == "${HOME}"
}

// stmtDirs is the directory each statement of f runs in by the line's cds: start until the
// first cd, then where each cd lands, and "" once one lands where the line does not say.
func stmtDirs(f *syntax.File, command, start, home string) map[*syntax.Stmt]string {
	dirs := map[*syntax.Stmt]string{}
	cwd := start
	syntax.Walk(f, func(n syntax.Node) bool {
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		dirs[st] = cwd
		if call, ok := st.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			if name, _ := plainWord(call.Args[0]); name == "cd" {
				cwd = cdLands(command, call.Args[1:], cwd, home)
			}
		}
		return true
	})
	return dirs
}

// cdLands is the directory `cd args` moves to from cwd, "" when the line does not say:
// `cd -` returns to a directory the line never named.
func cdLands(command string, args []*syntax.Word, cwd, home string) string {
	for len(args) > 0 {
		flag, _ := plainWord(args[0])
		if flag == "--" {
			args = args[1:]
			break
		}
		if len(flag) < 2 || flag[0] != '-' {
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return home
	}
	if t, _ := plainWord(args[0]); t == "-" {
		return ""
	}
	dir, ok := placePath(placeableWord(command, args[0]), cwd, home)
	if !ok {
		return ""
	}
	return dir
}

// outside reports a path that provably lands outside the workspace: one placePath places
// outside the root, or in a checkout nested under it.
//
// A copy of the placers' answer with the opposite polarity, kept because an unplaceable
// path counts as INSIDE here, the safe direction for scoping advice away.
func (s workspaceScope) outside(p string) bool {
	abs, ok := placePath(p, s.dir, s.home)
	if !ok {
		return false
	}
	if s.root == "" {
		return throwawayDirRe.MatchString(abs)
	}
	// macOS reaches /tmp and /var through /private, and the two sides may arrive under
	// different spellings of the same directory.
	norm := func(x string) string { return strings.TrimPrefix(filepath.Clean(x), "/private") }
	rel, err := filepath.Rel(norm(s.root), norm(abs))
	return err == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || nestedCheckout(s.root, abs))
}

// lineOutside reports a line whose every path operand provably lands outside the
// workspace. A line naming no path at all works on the call's directory, so it is not.
func (s workspaceScope) lineOutside(command string, d Dialect) bool {
	paths, ok := operandPaths(command, d, s.dir, s.home)
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
// search's patterns, each placed from the directory its statement runs in, starting at
// start. A word no placer can place comes back unresolved. Over-collecting is the safe
// direction: an extra unplaced word counts as inside and keeps the line in scope.
func operandPaths(command string, d Dialect, start, home string) ([]string, bool) {
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
	dirs := stmtDirs(f, command, start, home)
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		stmt, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		place := func(word string) string {
			// A bare relative word such as git's `push` is collected too, and placed after a
			// cd it would carry the line out of scope; only a word shaped like a path, or
			// naming a file there, is placed.
			p, ok := placePath(word, dirs[stmt], home)
			if ok && !filepath.IsAbs(word) && !strings.HasPrefix(word, "~") && !strings.ContainsAny(word, "/.") {
				if _, err := os.Stat(p); err != nil {
					ok = false
				}
			}
			if ok {
				return p
			}
			if strings.HasPrefix(word, unresolved) {
				return word
			}
			return unresolved + word
		}
		for _, r := range stmt.Redirs {
			switch r.Op {
			case syntax.DplOut, syntax.DplIn, syntax.WordHdoc, syntax.Hdoc, syntax.DashHdoc:
				continue
			}
			if r.Word != nil {
				out = append(out, place(scopeWord(command, r.Word, vars)))
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
			if isCdInvocation(c) {
				continue
			}
			for _, p := range invocationPaths(c) {
				out = append(out, place(p))
			}
		}
		return true
	})
	return out, true
}

// scopeWord renders a word to the path it names. A word built from a variable is resolved
// from assignments made earlier on the line; one that cannot be is marked unresolved.
func scopeWord(command string, w *syntax.Word, vars map[string]string) string {
	if literalOnly(w.Parts) {
		return placeableWord(command, w)
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
	switch name {
	case "find":
		// find's paths lead; everything from the first expression on is a predicate.
		var out []string
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-") || a == "(" || a == "!" {
				break
			}
			out = append(out, a)
		}
		return out
	case "sed":
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
