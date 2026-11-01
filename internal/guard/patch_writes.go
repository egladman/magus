package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// maxPatchBytes bounds what the guard reads of one patch inside a hook.
const maxPatchBytes = 8 << 20

// patchCall is one `git apply` or `patch` as its words say.
type patchCall struct {
	dirs     []string
	strip    int
	prefix   string
	files    []string
	stdin    bool
	only     string
	readOnly bool
	opaque   bool
	// repoRelative is git apply: paths start at the repository root,
	// even from a subdirectory.
	repoRelative bool
}

// patchFlags scans args as a parse-options tool does, bundled shorts included.
// It returns the operands.
func patchFlags(args []string, shortValued string, longValued map[string]bool, set func(name, value string), flag func(name string)) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(out, args[i+1:]...)
		case strings.HasPrefix(a, "--"):
			name, value, joined := strings.Cut(a, "=")
			flag(name)
			if !longValued[name] {
				continue
			}
			if !joined && i+1 < len(args) {
				i++
				value = args[i]
			}
			set(name, value)
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				short := a[j : j+1]
				if !strings.Contains(shortValued, short) {
					flag("-" + short)
					continue
				}
				value := a[j+1:]
				if value == "" && i+1 < len(args) {
					i++
					value = args[i]
				}
				set("-"+short, value)
				break
			}
		default:
			out = append(out, a)
		}
	}
	return out
}

var (
	gitApplyLongValued = map[string]bool{
		"--directory": true, "--exclude": true, "--include": true, "--whitespace": true, "--build-fake-ancestor": true,
	}
	patchLongValued = map[string]bool{
		"--strip": true, "--directory": true, "--input": true, "--output": true, "--prefix": true, "--basename-prefix": true,
		"--suffix": true, "--reject-file": true, "--ifdef": true, "--fuzz": true, "--quoting-style": true, "--get": true,
		"--version-control": true, "--reject-format": true, "--read-only": true, "--debug": true,
	}
)

// parsePatchCall reads c as a patch application, false for any other command.
func parsePatchCall(c hint.Invocation) (patchCall, bool) {
	switch path.Base(c.Name) {
	case "git":
		g := parseGit(c.Args)
		if g.sub != "apply" {
			return patchCall{}, false
		}
		return parseGitApply(g), true
	case "patch":
		return parsePatchTool(c.Args), true
	}
	return patchCall{}, false
}

func parseGitApply(g gitCommand) patchCall {
	call := patchCall{dirs: g.dirs, strip: 1, opaque: g.opaque, repoRelative: true}
	reads, writes := false, false
	operands := patchFlags(g.rest, "pC", gitApplyLongValued, func(name, value string) {
		switch name {
		case "-p":
			call.strip = patchStrip(value, call.strip)
		case "--directory":
			call.prefix = value
		}
	}, func(name string) {
		switch name {
		case "--check", "--stat", "--numstat", "--summary":
			reads = true
		case "--apply", "--index", "--cached", "--3way", "-3":
			writes = true
		}
	})
	call.readOnly = reads && !writes
	call.stdin = len(operands) == 0 || slices.Contains(operands, "-")
	call.files = slices.DeleteFunc(operands, func(f string) bool { return f == "-" })
	return call
}

// parsePatchTool reads patch(1). With no -p it keeps each path's base name.
// `patch orig p` writes orig, whatever the patch names.
func parsePatchTool(args []string) patchCall {
	call := patchCall{strip: -1}
	input, output := "", ""
	operands := patchFlags(args, "pdioBDFgrVxYz", patchLongValued, func(name, value string) {
		switch name {
		case "-p", "--strip":
			call.strip = patchStrip(value, call.strip)
		case "-d", "--directory":
			call.dirs = append(call.dirs, value)
		case "-i", "--input":
			input = value
		case "-o", "--output":
			output = value
		}
	}, func(name string) {
		if name == "--dry-run" || name == "--check" || name == "-C" {
			call.readOnly = true
		}
	})
	if len(operands) > 0 {
		call.only = operands[0]
	}
	if output != "" {
		call.only = output
	}
	switch {
	case input != "":
		call.files = []string{input}
	case len(operands) > 1:
		call.files = operands[1:2]
	default:
		call.stdin = true
	}
	return call
}

func patchStrip(value string, otherwise int) int {
	if n, err := strconv.Atoi(value); err == nil && n >= 0 {
		return n
	}
	return otherwise
}

// patchWrites names the files the line's patch applications write. unread
// holds each patch it could not read: a pipe from another command, a word
// the shell expands, a file not on disk yet.
func patchWrites(command string, depth int, d Dialect, start, home string) (writes []placedWord, unread []string) {
	eachLineCall(command, depth, d, start, home, func(at lineCall) {
		call, ok := parsePatchCall(at.call)
		if !ok || call.readOnly {
			return
		}
		w, u := call.writes(at.command, at.stmt, at.source, at.dir, home)
		writes = append(writes, w...)
		unread = append(unread, u...)
	})
	return writes, unread
}

// lineCall is one command of a line, where it runs and what pipes into it.
type lineCall struct {
	command      string
	call         hint.Invocation
	stmt, source *syntax.Stmt
	dir          string
}

// eachLineCall visits every command of the line and of the shells it nests.
func eachLineCall(command string, depth int, d Dialect, start, home string, visit func(lineCall)) {
	if depth > writeScanDepth {
		return
	}
	f, err := parseFile(command, d)
	if err != nil {
		return
	}
	dirs := stmtDirs(f, command, start, home)
	piped := pipeSources(f)
	var scripts []nestedScript
	syntax.Walk(f, func(n syntax.Node) bool {
		stmt, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
			if script, ok := shellPayload(literalWords(call.Args)); ok {
				scripts = append(scripts, nestedScript{script, dirs[stmt]})
				return true
			}
		}
		for _, c := range shellCommands(command, stmt, d, placeableWord) {
			visit(lineCall{command: command, call: c, stmt: stmt, source: piped[stmt], dir: dirs[stmt]})
		}
		return true
	})
	for _, s := range scripts {
		eachLineCall(s.text, depth+1, d, s.dir, home, visit)
	}
}

// cherryPickLongValued are the long cherry-pick flags that take a value.
var cherryPickLongValued = map[string]bool{
	"--mainline": true, "--strategy": true, "--strategy-option": true, "--cleanup": true,
}

// pickWrites names the files the line's `git cherry-pick` calls write. unread
// holds each pick it could not list: a commit the line makes first, a word the
// shell expands. A `--continue` names no revision and writes nothing new.
func pickWrites(ctx context.Context, command string, d Dialect, start, home string) (writes []placedWord, unread []string) {
	eachLineCall(command, 0, d, start, home, func(at lineCall) {
		if path.Base(at.call.Name) != "git" {
			return
		}
		g := parseGit(at.call.Args)
		if g.sub != "cherry-pick" {
			return
		}
		fromStdin := false
		revs := patchFlags(g.rest, "mX", cherryPickLongValued, func(string, string) {}, func(name string) {
			fromStdin = fromStdin || name == "--stdin"
		})
		dir := at.dir
		for _, step := range g.dirs {
			next, ok := placePath(step, dir, home)
			if !ok {
				unread = append(unread, "a directory the line does not determine")
				return
			}
			dir = next
		}
		switch {
		case g.opaque:
			unread = append(unread, "a repository named by --git-dir or --work-tree")
			return
		case fromStdin:
			unread = append(unread, "standard input")
		}
		if len(revs) == 0 {
			return
		}
		names, err := pickedPaths(ctx, dir, revs)
		if err != nil {
			unread = append(unread, strings.Join(revs, " "))
			return
		}
		base := repositoryBase(dir)
		for _, name := range names {
			writes = append(writes, placedWord{word: name, dir: base})
		}
	})
	return writes, unread
}

// pickedPaths lists every file the commits revs select write, as cherry-pick
// selects them: a range walks, a lone commit does not. Each commit counts, so a
// file a range writes and reverts is still named, and renames name both sides.
func pickedPaths(ctx context.Context, dir string, revs []string) ([]string, error) {
	args := make([]string, 0, 10+len(revs))
	args = append(args, "-C", dir, "log", "--format=", "--name-only", "-z", "--no-renames", "--diff-merges=separate", "--no-walk=unsorted")
	args = append(args, revs...)
	args = append(args, "--")
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w", strings.Join(revs, " "), err)
	}
	var names []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// pipeSources maps each statement a pipe feeds to the statement feeding it.
func pipeSources(f *syntax.File) map[*syntax.Stmt]*syntax.Stmt {
	isPipe := func(op syntax.BinCmdOperator) bool { return op == syntax.Pipe || op == syntax.PipeAll }
	out := map[*syntax.Stmt]*syntax.Stmt{}
	syntax.Walk(f, func(n syntax.Node) bool {
		if b, ok := n.(*syntax.BinaryCmd); ok && isPipe(b.Op) {
			target := b.Y
			for {
				inner, ok := target.Cmd.(*syntax.BinaryCmd)
				if !ok || !isPipe(inner.Op) {
					break
				}
				target = inner.X
			}
			out[target] = b.X
		}
		return true
	})
	return out
}

// writes resolves c against the patches it reads.
func (c patchCall) writes(command string, stmt, source *syntax.Stmt, dir, home string) ([]placedWord, []string) {
	for _, step := range c.dirs {
		next, ok := placePath(step, dir, home)
		if !ok {
			return nil, []string{"a directory the line does not determine"}
		}
		dir = next
	}
	if c.opaque {
		return nil, []string{"a repository named by --git-dir or --work-tree"}
	}
	var bodies, unread []string
	for _, name := range c.files {
		body, ok := readPatchFile(name, dir, home)
		if !ok {
			unread = append(unread, name)
			continue
		}
		bodies = append(bodies, body)
	}
	if c.stdin {
		got, ok := stdinPatches(command, stmt, source, dir, home)
		if !ok {
			unread = append(unread, "standard input")
		}
		bodies = append(bodies, got...)
	}
	if c.only != "" {
		return []placedWord{{word: c.only, dir: dir}}, unread
	}
	base := dir
	if c.repoRelative {
		base = repositoryBase(dir)
	}
	var out []placedWord
	for _, body := range bodies {
		for _, p := range patchedPaths(body, c.strip) {
			out = append(out, placedWord{word: path.Join(c.prefix, p), dir: base})
		}
	}
	return out, unread
}

func readPatchFile(name, dir, home string) (string, bool) {
	p, ok := placePath(name, dir, home)
	if !ok {
		return "", false
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPatchBytes {
		return "", false
	}
	body, err := os.ReadFile(p)
	return string(body), err == nil
}

// stdinPatches is what stmt reads on standard input when the line says:
// a redirect, a literal here-document, or a cat of files.
func stdinPatches(command string, stmt, source *syntax.Stmt, dir, home string) ([]string, bool) {
	for _, r := range stmt.Redirs {
		switch r.Op {
		case syntax.RdrIn:
			body, ok := readPatchFile(placeableWord(command, r.Word), dir, home)
			return []string{body}, ok
		case syntax.Hdoc, syntax.DashHdoc:
			body, ok := plainWord(r.Hdoc)
			return []string{body}, ok
		case syntax.WordHdoc:
			return nil, false
		}
	}
	if source == nil {
		return nil, false
	}
	call, ok := source.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) < 2 {
		return nil, false
	}
	if name, _ := plainWord(call.Args[0]); path.Base(name) != "cat" {
		return nil, false
	}
	var bodies []string
	for _, w := range call.Args[1:] {
		body, ok := readPatchFile(placeableWord(command, w), dir, home)
		if !ok {
			return nil, false
		}
		bodies = append(bodies, body)
	}
	return bodies, true
}

// repositoryBase is the root of the repository holding dir, spelled through
// dir so a symlinked parent still compares with the checkout it names.
func repositoryBase(dir string) string {
	root, _, ok := gitCheckout(dir)
	if !ok {
		return dir
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." {
		return dir
	}
	base := dir
	for range strings.Split(rel, string(filepath.Separator)) {
		base = filepath.Dir(base)
	}
	return base
}

var unifiedHunkRe = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// patchedPaths are the files a patch's headers name, sorted, with strip leading
// directories removed; a negative strip keeps the base name. Hunk bodies are
// counted past: a removed `-- x` line reads as a `--- x` header.
func patchedPaths(body string, strip int) []string {
	var out []string
	add := func(name string, n int) {
		if p, ok := stripPatchPath(name, n); ok {
			out = append(out, p)
		}
	}
	old, added := 0, 0
	for line := range strings.Lines(body) {
		line = strings.TrimRight(line, "\r\n")
		if old > 0 || added > 0 {
			switch {
			case strings.HasPrefix(line, "-"):
				old--
			case strings.HasPrefix(line, "+"):
				added--
			case strings.HasPrefix(line, `\`):
			default:
				old--
				added--
			}
			continue
		}
		if m := unifiedHunkRe.FindStringSubmatch(line); m != nil {
			old, added = hunkCount(m[1]), hunkCount(m[2])
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			a, b := gitHeaderNames(strings.TrimPrefix(line, "diff --git "))
			add(a, strip)
			add(b, strip)
		case strings.HasPrefix(line, "--- ") && !strings.HasSuffix(line, " ----"):
			add(headerName(line[4:]), strip)
		case strings.HasPrefix(line, "+++ "):
			add(headerName(line[4:]), strip)
		case strings.HasPrefix(line, "*** ") && !strings.HasSuffix(line, " ****"):
			add(headerName(line[4:]), strip)
		case strings.HasPrefix(line, "rename from "), strings.HasPrefix(line, "rename to "), strings.HasPrefix(line, "copy to "):
			// These lines carry no a/ or b/ for -p to remove.
			_, name, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(line, "rename "), "copy "), " ")
			add(headerName(name), 0)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func hunkCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

// headerName is the path of a ---/+++ line: a timestamp follows a tab,
// and git quotes a name with special characters.
func headerName(s string) string {
	name, _, _ := strings.Cut(s, "\t")
	if strings.HasPrefix(name, `"`) {
		if u, err := strconv.Unquote(name); err == nil {
			return u
		}
	}
	return name
}

// gitHeaderNames splits a `diff --git` line's two names. An unquoted pair
// holding a space is ambiguous: the ` b/` split or equal halves settle it.
func gitHeaderNames(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		if rest, err := strconv.QuotedPrefix(s); err == nil {
			return headerName(rest), headerName(strings.TrimSpace(s[len(rest):]))
		}
	}
	if a, b, ok := strings.Cut(s, " b/"); ok {
		return a, "b/" + b
	}
	if mid := len(s) / 2; len(s)%2 == 1 && s[mid] == ' ' && s[:mid] == s[mid+1:] {
		return s[:mid], s[mid+1:]
	}
	return "", ""
}

func stripPatchPath(name string, strip int) (string, bool) {
	if name == "" || name == "/dev/null" {
		return "", false
	}
	if strip < 0 {
		return path.Base(name), true
	}
	parts := strings.Split(name, "/")
	if strip >= len(parts) {
		return "", false
	}
	return path.Join(parts[strip:]...), true
}
