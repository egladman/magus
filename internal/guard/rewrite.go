package guard

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// An interpreter rewriting a file the tree already carries.
//
// `sed -i` has been refused for a while, and that denial sends the reader to their editor
// tool. The same act through an interpreter was refused only when the script both
// SUBSTITUTED and wrote, so the whole-file rewrite a merge conflict invites walked through,
// and a script arriving as a HEREDOC was never read at all.
//
// It keys on the WRITE rather than on the interpreter's flags, because the flags are the
// part with a new spelling every time: -c, -e, -pi, a bare argument, a heredoc. Two things
// stay allowed on purpose, since a rule that refused them is one people turn off: a script
// writing to a scratch or temp path, and a script creating a file that is not there yet.

// scriptWriteCalls name a write in the interpreters' own vocabularies.
var scriptWriteCalls = []string{
	".write(", ".writelines(", ".write_text(", ".write_bytes(",
	"writeFileSync", "writeFile(", "File.write", "IO.write", "fputs",
}

// scriptMutateRe finds the calls that change a file without writing its content: deleting,
// moving, copying over, linking, or handing the job to a shell. None of them names its
// destination in a way scriptWriteTargets reads, and perl's `open(F, '>', p)` is a write
// openWriteRe does not see.
var scriptMutateRe = regexp.MustCompile(
	`\bos\.(?:remove|unlink|rmdir|removedirs|rename|renames|replace|makedirs|mkdir|symlink|link|chmod|chown|truncate|system|popen|exec\w*|spawn\w*)\b` +
		`|\b(?:shutil|subprocess|child_process|FileUtils)\b` +
		`|\b(?:rm|unlink|rename|copyFile|cp|mkdir|rmdir|symlink|symlink_to|truncate|chmod|touch|appendFile|createWriteStream)(?:Sync)?\s*\(` +
		`|\bFile\.(?:delete|unlink|rename|symlink|chmod|truncate)\b|\bDir\.(?:mkdir|rmdir|delete|unlink)\b` +
		`|\b(?:system|exec)\s*\(` +
		`|\bopen\s*\([^)]*['"]\s*\+?[>|]`)

// shellOutRe is perl's and ruby's paren-free spellings of the same acts, and their
// backtick shell-out. JavaScript's backtick is a template string, so it is not read here.
var shellOutRe = regexp.MustCompile("`|\\b(?:unlink|rename|rmdir|mkdir|system|exec)\\b")

// interpreterWriteTargets is what an inline interpreter program could be aimed at: nothing
// for a program that only reads, the literal destinations of its writes when every one of
// them can be read, and ok false when some write or mutation names its destination in a
// way this cannot follow. The last is the caller's cue to keep every path the program
// carries, since reading one as data would let the write through unjudged.
func interpreterWriteTargets(name, script string, args []string) (out []string, ok bool) {
	if scriptMutateRe.MatchString(script) || ((name == "perl" || name == "ruby") && shellOutRe.MatchString(script)) {
		return nil, false
	}
	targets, unknown := scriptWriteTargets(name, script, args)
	if unknown {
		return nil, false
	}
	if len(targets) == 0 && scriptWrites(name, script, args) {
		return nil, false
	}
	for _, literals := range targets {
		if len(literals) == 0 {
			return nil, false
		}
		out = append(out, literals...)
	}
	return out, true
}

// openWriteRe matches an open() whose mode asks to write, so the read that merely names a
// file is left alone.
var openWriteRe = regexp.MustCompile(`open\s*\([^)]*['"][rbt+]*[wa][rbt+]*['"]`)

// awkRedirectRe matches awk's own redirect operators, `>` and `>>`, in the one place awk's
// grammar accepts a write target: right after a `print` or `printf` statement. A bare `>`
// or `>=` used as a numeric or string comparison (`NR>=1`, `$1 > 5`) never follows one of
// those two keywords, so requiring it is what tells a range selection from a write.
var awkRedirectRe = regexp.MustCompile(`\b(?:print|printf)\b[^;{}\n]*>`)

// interpreterScript is the program text an interpreter runs: its arguments, plus the
// heredoc body when the program is read from stdin (`awk -f /dev/stdin <<EOF`).
//
// One assembly, used by every caller. Two call sites building this differently is exactly
// how a redirect spelled only inside a heredoc reached no boundary check at all: the
// rewrite rule included the body and the write-candidate scan did not.
func interpreterScript(args []string, heredoc string) string {
	return strings.Join(args, "\n") + "\n" + heredoc
}

// scriptWrites reports a program that would write a file, in the spelling name uses.
func scriptWrites(name, script string, args []string) bool {
	switch name {
	case "perl", "ruby":
		if hasFlag(args, 'i', "in-place") {
			return true
		}
	case "awk":
		// awk redirects in its own language, so the write is inside the program text.
		if awkRedirectRe.MatchString(script) {
			return true
		}
	}
	if openWriteRe.MatchString(script) {
		return true
	}
	return slices.ContainsFunc(scriptWriteCalls, func(c string) bool { return strings.Contains(script, c) })
}

// denyInterpreterRewrite is the reason an inline interpreter would rewrite a file already
// in this workspace, or "" when it would not.
//
// It walks the AST itself rather than reading writeTargetCandidates, because it needs the
// SCRIPT as one text to ask whether it writes at all, and the heredoc body is a redirect
// rather than an argument.
func denyInterpreterRewrite(location location, command string, d Dialect) string {
	if location.workspace == "" {
		return ""
	}
	f, err := parseFile(command, d)
	if err != nil {
		return ""
	}
	hit := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		if hit != "" {
			return false
		}
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := st.Cmd.(*syntax.CallExpr)
		if !ok {
			return true
		}
		for _, c := range peelWrappers(literalWords(call.Args), d) {
			name := path.Base(c.Name)
			if name == "magus" {
				if script, ok := inlineBuzz(c.Args, heredocText(st)); ok {
					hit = buzzRewrittenFile(location.workspace, script)
				}
				if hit != "" {
					return false
				}
				continue
			}
			if !scriptedRewriteInterpreters[name] && name != "awk" {
				continue
			}
			script := interpreterScript(c.Args, heredocText(st))
			if !scriptWrites(name, script, c.Args) {
				continue
			}
			if rel := rewrittenWorkspaceFile(location, name, script, c.Args); rel != "" {
				hit = rel
				return false
			}
		}
		return true
	})
	if hit == "" || rewriteStaysOutside(scopeAt(location), command, d) {
		return ""
	}
	return interpreterRewriteDenial(hit) + referenceScriptNote(location.workspace, denyRuleInterpreterRewrite)
}

// rewrittenWorkspaceFile names the first file already in the tree that the script would
// write, or "".
//
// Existence is the discriminator: rewriting is what this refuses, and a path that is not
// there yet is a script producing output. A scratch or temp destination is skipped for the
// same reason, whether or not something sits at it. Only a write's DESTINATION is a
// candidate: a tracked path the program merely carries, in a list it prints or a report
// it writes to scratch, is data.
func rewrittenWorkspaceFile(location location, name, script string, args []string) string {
	targets, unknown := scriptWriteTargets(name, script, args)
	var candidates []string
	for _, literals := range targets {
		switch {
		case len(literals) == 0:
			unknown = true
		case !slices.ContainsFunc(literals, throwawayDirRe.MatchString):
			candidates = append(candidates, literals...)
		}
	}
	// A destination spelled from no literal at all is most often argv, so the operands
	// are what it writes.
	if unknown {
		candidates = append(candidates, args...)
	}
	return existingWorkspaceFile(location.workspace, candidates)
}

// buzzRewrittenFile names the first file already in the workspace that a Buzz program
// replaces through fs at a literal destination, or "". A destination built at run time
// from argv or a computed path is not read: this judges what the program text proves.
func buzzRewrittenFile(workspace, script string) string {
	return existingWorkspaceFile(workspace, buzzWriteDestinations(script))
}

// buzzWriteDestinations are the literals each fs write in a Buzz program is spelled from,
// less any write already aimed at a scratch or temp path.
func buzzWriteDestinations(script string) []string {
	var candidates []string
	for _, loc := range buzzWriterRe.FindAllStringIndex(script, -1) {
		callArgs, ok := callArguments(script, loc[1]-1)
		if !ok {
			continue
		}
		parts := splitArguments(callArgs)
		if len(parts) == 0 {
			continue
		}
		if literals := destinationLiterals(script, parts[0]); !slices.ContainsFunc(literals, throwawayDirRe.MatchString) {
			candidates = append(candidates, literals...)
		}
	}
	return candidates
}

// inlineBuzz is the program a `magus buzz` line carries itself: an -e snippet, or the
// heredoc it reads from stdin when it names no file. A named file is judged from its
// content instead, like any other script a line runs.
func inlineBuzz(args []string, heredoc string) (string, bool) {
	if len(args) == 0 || args[0] != "buzz" {
		return "", false
	}
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-e" && i+1 < len(args):
			return args[i+1], true
		case a == "-C":
			i++
		case a == "-" || a == "--":
			return heredoc, heredoc != ""
		case !strings.HasPrefix(a, "-"):
			return "", false
		}
	}
	return heredoc, heredoc != ""
}

// buzzWriterRe finds the fs members that replace a file's content. fs\appendFile only adds
// to the end, which cannot mangle a line already there.
var buzzWriterRe = regexp.MustCompile(`\bfs\\(?:writeFileAtomic|writeFile|writeLines)\s*\(`)

// existingWorkspaceFile names the first candidate that is a file already in the workspace,
// or "". A relative candidate resolves from the workspace root.
func existingWorkspaceFile(workspace string, candidates []string) string {
	if workspace == "" {
		return ""
	}
	for _, candidate := range candidates {
		if candidate == "" || throwawayDirRe.MatchString(candidate) {
			continue
		}
		rel, inside := workspaceRelative(workspace, candidate)
		if !inside || rel == "." {
			continue
		}
		abs := candidate
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(workspace, candidate)
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return rel
		}
	}
	return ""
}

// pathWriterRe finds the calls whose first argument is the file they write: an open (whose
// mode is checked separately) and node's and ruby's one-shot writers.
var pathWriterRe = regexp.MustCompile(`\b(?:f?open|writeFileSync|writeFile|appendFileSync|appendFile|createWriteStream|File\.write|IO\.write)\s*\(`)

// receiverWriterRe finds pathlib's writers, which write the path their receiver names.
var receiverWriterRe = regexp.MustCompile(`\.(?:write_text|write_bytes)\s*\(`)

// handleWriteRe finds a write through a handle, whose file is named where it was opened.
var handleWriteRe = regexp.MustCompile(`\.(?:write|writelines)\s*\(|\bfputs\s*\(`)

// awkTargetRe captures what follows awk's redirect after a print: the file it writes.
var awkTargetRe = regexp.MustCompile(`\b(?:print|printf)\b[^;{}\n]*?>>?\s*([^;}\n]+)`)

// writeModeRe is an open mode that writes: w, a, x, or an update `+`.
var writeModeRe = regexp.MustCompile(`^(?:mode\s*=\s*)?['"][rbtU]*[wax+][rbtwax+]*['"]$`)

// stdStreamRe is a receiver that is the program's own output rather than a file.
var stdStreamRe = regexp.MustCompile(`^(?:sys\.|process\.|\$)?(?:stdout|stderr|STDOUT|STDERR)$`)

var identRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// moduleOpeners are receivers whose `.open(path, mode)` is the module function, not a
// method on a path.
var moduleOpeners = map[string]bool{"io": true, "os": true, "codecs": true, "gzip": true, "builtins": true}

// scriptWriteTargets lists each file the program writes, as the literals its destination is
// spelled from (a variable read through its assignment). unknown reports a write whose
// destination this cannot read at all: an in-place flag, or a handle opened somewhere it
// does not look.
func scriptWriteTargets(name, script string, args []string) (targets [][]string, unknown bool) {
	if (name == "perl" || name == "ruby") && hasFlag(args, 'i', "in-place") {
		unknown = true
	}
	if name == "awk" {
		for _, m := range awkTargetRe.FindAllStringSubmatch(script, -1) {
			targets = append(targets, destinationLiterals(script, m[1]))
		}
	}
	for _, loc := range pathWriterRe.FindAllStringIndex(script, -1) {
		callArgs, ok := callArguments(script, loc[1]-1)
		if !ok {
			unknown = true
			continue
		}
		parts := splitArguments(callArgs)
		method := loc[0] > 0 && script[loc[0]-1] == '.'
		recv := ""
		if method {
			recv = receiverBefore(script, loc[0]-1)
		}
		if strings.Contains(script[loc[0]:loc[1]], "open") {
			modes := parts
			if !method || moduleOpeners[recv] {
				modes = parts[min(1, len(parts)):]
			}
			if !slices.ContainsFunc(modes, writeModeRe.MatchString) {
				continue
			}
			if method && !moduleOpeners[recv] {
				targets = append(targets, destinationLiterals(script, recv))
				continue
			}
		}
		if len(parts) > 0 {
			targets = append(targets, destinationLiterals(script, parts[0]))
		}
	}
	for _, loc := range receiverWriterRe.FindAllStringIndex(script, -1) {
		targets = append(targets, destinationLiterals(script, receiverBefore(script, loc[0])))
	}
	if len(targets) == 0 {
		for _, loc := range handleWriteRe.FindAllStringIndex(script, -1) {
			if script[loc[0]] == '.' && stdStreamRe.MatchString(receiverBefore(script, loc[0])) {
				continue
			}
			unknown = true
		}
	}
	return targets, unknown
}

// destinationLiterals are the quoted strings a destination expression is built from, each
// identifier in it read through its assignments, one level deep. A declaring keyword and a
// type annotation are allowed before the `=`, which is how Buzz (`final p: str = ...`) and
// JavaScript (`const p = ...`) bind one.
func destinationLiterals(script, expr string) []string {
	out := quotedLiterals(expr)
	for _, ident := range identRe.FindAllString(expr, -1) {
		assign := regexp.MustCompile(`(?m)^\s*(?:(?:final|var|const|let)\s+)?` + regexp.QuoteMeta(ident) + `(?:\s*:[^=\n]*)?\s*=\s*([^=\n].*)$`)
		for _, m := range assign.FindAllStringSubmatch(script, -1) {
			out = append(out, quotedLiterals(m[1])...)
		}
	}
	return out
}

// callArguments is the text between the paren at open and its match.
func callArguments(s string, open int) (string, bool) {
	depth, quote := 0, byte(0)
	for i := open; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
			if depth == 0 {
				return s[open+1 : i], true
			}
		}
	}
	return "", false
}

// splitArguments splits a call's argument text at its top-level commas.
func splitArguments(args string) []string {
	var out []string
	depth, quote, start := 0, byte(0), 0
	for i := 0; i < len(args); i++ {
		c := args[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(args[start:i]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(args[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

// receiverBefore is the expression ending just before the dot at dot: `Path('x')` in
// `Path('x').write_text(`, `sys.stdout` in `sys.stdout.write(`.
func receiverBefore(s string, dot int) string {
	i := dot
	for i > 0 {
		c := s[i-1]
		switch {
		case c == ')' || c == ']':
			depth := 0
			j := i - 1
			for ; j >= 0; j-- {
				switch s[j] {
				case ')', ']':
					depth++
				case '(', '[':
					depth--
				}
				if depth == 0 {
					break
				}
			}
			if j < 0 {
				return s[i:dot]
			}
			i = j
		case c == '_' || c == '.' || c == '$' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z':
			i--
		default:
			return s[i:dot]
		}
	}
	return s[:dot]
}

// interpreterRewriteDenial names the editor tool, in the shape the raw-tool denials use to
// name a magus target: a refusal that does not hand back the verb it wanted is one the
// reader routes around.
func interpreterRewriteDenial(rel string) string {
	return fmt.Sprintf("Use your editor tool on %s: it reads the file first and reports what it changed.\n"+
		"Whole-tree mechanical edit? `"+hint.Refs.With("<symbol>", "--occurrences")+"` gives column-precise sites.\n"+
		"Scratch paths and scripts that CREATE a file are untouched.", rel)
}

// rewriteStaysOutside reports a line whose every in-place rewrite provably lands outside the
// workspace, which is what lets the rewrite rules keep their promise that a scratch file is
// untouched. A relative path resolves from the directory the line last cd'd into, the way a
// scratch edit is usually typed; before any cd it resolves where the call runs, inside. A
// rewrite whose files come from a traversal, a pipe or a computed path proves nothing, and
// neither does a line that names no file.
func rewriteStaysOutside(scope workspaceScope, command string, d Dialect) bool {
	f, err := parseFile(command, d)
	if err != nil {
		return false
	}
	vars := map[string]string{}
	for _, m := range assignmentRe.FindAllStringSubmatch(command, -1) {
		vars[m[1]] = m[3]
	}
	if scope.home != "" {
		vars["HOME"] = scope.home
	}
	dir, blind := "", false
	var paths []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if blind {
			return false
		}
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := st.Cmd.(*syntax.CallExpr)
		if !ok {
			return true
		}
		words := make([]string, len(call.Args))
		for i, w := range call.Args {
			words[i] = scopeWord(command, w, vars)
		}
		for _, c := range peelWrappers(words, d) {
			if isCdInvocation(c) {
				dir = cdTarget(dir, c.Args)
				continue
			}
			rewritten, named := inPlaceRewrites(c, heredocText(st))
			if !named {
				blind = true
				return false
			}
			for _, p := range rewritten {
				paths = append(paths, underDir(dir, p))
			}
		}
		return true
	})
	return !blind && allOutside(scope, paths)
}

// inPlaceRewrites are the files c rewrites, and false when it rewrites files it does not
// name. A command that rewrites nothing names nothing and is true.
func inPlaceRewrites(c hint.Invocation, heredoc string) ([]string, bool) {
	name := path.Base(c.Name)
	if driven, ok := hint.DrivenCommand(c); ok && rewritesInPlace(driven) {
		return nil, false
	}
	switch {
	case name == "sed" && hasFlag(c.Args, 'i', "in-place"):
		files := sedEditedFiles(c.Args)
		return files, len(files) > 0
	case name == "awk":
		return nil, !awkRedirectRe.MatchString(interpreterScript(c.Args, heredoc))
	case name == "magus":
		script, ok := inlineBuzz(c.Args, heredoc)
		if !ok {
			return nil, true
		}
		return buzzWriteDestinations(script), true
	case scriptedRewriteInterpreters[name]:
		script := interpreterScript(c.Args, heredoc)
		if !scriptWrites(name, script, c.Args) {
			return nil, true
		}
		// The program's own literals, plus the operands it would read as argv.
		files := scriptPaths(script)
		for _, op := range operands(c.Args, "ceEm") {
			if op != "-" {
				files = append(files, op)
			}
		}
		return files, len(files) > 0
	}
	return nil, true
}

// sedEditedFiles are the files an in-place sed edits under either reading of a bare -i:
// GNU's, where the next word is the script, and BSD's, where it is the backup suffix. An
// empty suffix is never a file and the script is never one, so dropping both leaves the
// files under each.
func sedEditedFiles(args []string) []string {
	ops := operands(args, "ef")
	if len(ops) > 0 && ops[0] == "" {
		ops = ops[1:]
	}
	if !hasFlag(args, 'e', "expression") && !hasFlag(args, 'f', "file") && len(ops) > 0 {
		ops = ops[1:]
	}
	return ops
}

// cdTarget is the directory a cd from dir lands in. A cd to home or to the previous
// directory is not read from the line, so it is unresolved.
func cdTarget(dir string, args []string) string {
	ops := operands(args, "")
	if len(ops) == 0 || ops[0] == "-" {
		return unresolved
	}
	return underDir(dir, ops[0])
}

// underDir resolves a relative p against dir, the directory the line cd'd into; with none,
// p stays relative and resolves where the call runs.
func underDir(dir, p string) string {
	if dir == "" || p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "~") || strings.HasPrefix(p, unresolved) {
		return p
	}
	return filepath.Join(dir, p)
}

// rankInterpreterRewrite ranks this reason against the verdict the other command rules
// reached. It fills a silence and outranks an advisory, but never replaces a deny: `sed -i`
// and the substitute-then-write rule both refuse the same act in their own words.
func rankInterpreterRewrite(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" || v.Deny != "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyRuleInterpreterRewrite}}
}
