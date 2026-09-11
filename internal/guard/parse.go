package guard

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// Resolving a shell line into the commands it would actually run. No policy lives
// here, which is the point of the seam: every wrong verdict this guard has produced
// was a tokenizing mistake rather than a rule mistake, so the tokenizing is worth
// reading, testing, and changing on its own. The rules that consume this are in
// internal/guard/shell.go and internal/guard/vcs.go.

// A pass-through wrapper runs ANOTHER command, with the environment or timeout
// adjusted first. It is never itself the finding: the guard peels it off and
// judges the payload, so an unapproved launcher cannot tunnel a raw tool past
// the guard.
//
// A launcher's declared task subcommand is deliberately absent: it runs a task,
// not a smuggled command, and peeling it would misattribute the task's contents.
var wrappers = map[string]bool{
	"env": true, "nohup": true, "command": true, "exec": true,
	"time": true, "timeout": true, "nice": true, "stdbuf": true,
	"xargs": true, "setsid": true, "sudo": true, "doas": true,
	"mise": true, "rtx": true,
	"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true,
	"eval": true,
}

// ParseCommands resolves a shell line into every command it would run.
//
// A PARSER rather than a pattern because every wrong verdict this guard has
// produced was a tokenizing mistake, not a policy one: a regex cannot tell a
// pipe from a pipe inside quotes, an assignment prefix from an argument, or see
// into a shell's own -c argument. An AST answers all three structurally.
//
// The bool is false when the line does not parse, and the caller skips the
// raw-tool rules rather than guessing: less of a bypass than it looks, since
// shell that does not parse does not run either.
func ParseCommands(command string) ([]hint.Invocation, bool) {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	var out []hint.Invocation
	syntax.Walk(f, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok {
			// Assigns are deliberately not consulted: the parser has already
			// separated the VAR=value prefix from the command, which is the
			// entire reason for parsing.
			out = append(out, peelWrappers(literalWords(call.Args))...)
		}
		return true
	})
	return out, true
}

// literalWords renders each word down to the text the shell would pass, as far
// as that is knowable without running anything. A word whose value comes from a
// parameter or a command substitution renders empty: its VALUE is unknown, but a
// command substitution's own commands are separate CallExpr nodes that Walk
// reaches independently, so nothing is lost by declining to guess here.
func literalWords(words []*syntax.Word) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, literalWord(w.Parts))
	}
	return out
}

func literalWord(parts []syntax.WordPart) string {
	var b strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			b.WriteString(literalWord(p.Parts))
		}
	}
	return b.String()
}

// peelWrappers reduces a wrapper invocation to the command it wraps, repeatedly,
// so a stack of them reduces to the program that will actually run. A -c payload
// is parsed as its own script, so it contributes the commands it contains rather
// than one opaque string.
func peelWrappers(words []string) []hint.Invocation {
	for len(words) > 0 {
		name := path.Base(words[0])
		switch {
		case name == "find":
			// find is not a wrapper (it still runs find) but -exec/-execdir
			// launches a command find never reparses through a shell, so the
			// payload has to be judged on its own. Keep find itself too.
			return append([]hint.Invocation{{Name: name, Args: words[1:]}}, findExecCommands(words[1:])...)

		case !wrappers[name]:
			return []hint.Invocation{{Name: name, Args: words[1:]}}

		case name == "env":
			// env -S / --split-string takes its argument AS the command line and,
			// unlike sh -c, never reparses it, so it is peeled like -c: the
			// remainder is parsed and the full ruleset runs over what it contains.
			if script, ok := envSplitString(words[1:]); ok {
				inner, _ := ParseCommands(script)
				return inner
			}
			rest := skipWrapperArgs(name, words[1:])
			if len(rest) == 0 {
				return []hint.Invocation{{Name: name, Args: words[1:]}}
			}
			words = rest

		case name == "mise" || name == "rtx":
			// A launcher may carry tool selectors before `--`; its short form is
			// accepted too. Both then pass the command after `--` through unchanged.
			if len(words) > 1 && (words[1] == "exec" || words[1] == "x") {
				if i := slices.Index(words, "--"); i >= 0 {
					words = words[i+1:]
					continue
				}
			}
			return []hint.Invocation{{Name: name, Args: words[1:]}}

		case shells[name]:
			script, ok := shellDashC(words[1:])
			if !ok {
				return []hint.Invocation{{Name: name, Args: words[1:]}}
			}
			inner, _ := ParseCommands(script)
			return inner

		case name == "eval":
			// eval concatenates its arguments and runs the result as a script.
			// Worth following for the same reason as `sh -c`: when the words are
			// literal it is an exact synonym for running them directly. When they
			// come from a variable, literalWord already rendered them empty and
			// the reparse finds nothing, which is the honest answer.
			inner, _ := ParseCommands(strings.Join(words[1:], " "))
			return inner

		default:
			// timeout, nice, stdbuf, xargs, nohup, command, exec, time:
			// step over the wrapper's own flags and operands to reach the
			// program it runs.
			rest := skipWrapperArgs(name, words[1:])
			if len(rest) == 0 {
				return []hint.Invocation{{Name: name, Args: words[1:]}}
			}
			words = rest
		}
	}
	return nil
}

// shells are the wrappers whose -c argument is a script. Derived from wrappers
// so the two cannot disagree about what a shell is.
var shells = func() map[string]bool {
	out := map[string]bool{}
	for _, name := range []string{"sh", "bash", "zsh", "ksh", "dash"} {
		if !wrappers[name] {
			panic("shells: " + name + " is not a wrapper")
		}
		out[name] = true
	}
	return out
}()

// writesToFile reports a redirect operator that sends a command's output at a file, so a
// rule about what a line WRITES reads all of them rather than the four everyone remembers.
// `>|`, `>&` and `<>` are writes too, and each is one character from a spelling that is
// caught. Pinned exhaustively by TestWritesToFileClassifiesEveryRedirectOperator.
func writesToFile(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll,
		syntax.RdrClob, syntax.AppClob, syntax.RdrAllClob, syntax.AppAllClob,
		syntax.RdrInOut, syntax.DplOut:
		return true
	}
	return false
}

// shellPayload returns the script a line hands to another shell to parse: a `-c` argument,
// eval's joined words, or env's -S string, with any wrappers in front of it peeled.
//
// peelWrappers follows these to reach the COMMANDS inside. A rule that reads the parse
// tree itself, as the cache-dir rule does for a redirect, needs the text so it can parse
// the payload on its own; without it `sh -c 'echo x > .magus/lease'` shows a bare echo.
func shellPayload(words []string) (string, bool) {
	for len(words) > 0 {
		name := path.Base(words[0])
		switch {
		case shells[name]:
			return shellDashC(words[1:])
		case name == "eval":
			return strings.Join(words[1:], " "), true
		case name == "env":
			if script, ok := envSplitString(words[1:]); ok {
				return script, true
			}
			fallthrough
		case wrappers[name]:
			rest := skipWrapperArgs(name, words[1:])
			if len(rest) == 0 {
				return "", false
			}
			words = rest
		default:
			return "", false
		}
	}
	return "", false
}

// Which files a shell line would WRITE. Two rules ask it, the cache dir's and the lease
// lane's, and a second extraction would drift from the first the moment either learned a
// spelling, so the walk lives here with the rest of the tokenizing and each rule brings
// only its own boundary.

// writeReaders are the commands that only READ what they are pointed at. Everything else
// is treated as a writer.
//
// A reader list rather than a writer list, which is the direction that fails safe: the
// eight-verb writer list this replaced let `dd of=`, `ln -sf`, `install`, `rmdir`,
// `chown`, `find -delete` and every inline interpreter through unseen. magus is a reader
// by construction: every magus run writes, and the guard grades the agent's tool calls
// rather than magus's own processes.
var writeReaders = map[string]bool{
	"cat": true, "bat": true, "head": true, "tail": true, "less": true, "more": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"jq": true, "wc": true, "ls": true, "stat": true, "file": true, "diff": true,
	"cut": true, "basename": true, "dirname": true, "realpath": true, "readlink": true,
	"du": true, "tree": true, "cd": true, "git": true,
	"sort": true, "awk": true, "find": true, "sed": true,
	"magus": true,
}

// onlyReads reports a command that only reads what it was pointed at. Four of the readers
// carry one spelling that turns them into writers, and a list that ignored those would be
// the writer allowlist again with the sides swapped.
func onlyReads(name string, args []string) bool {
	if !writeReaders[name] {
		return false
	}
	switch name {
	case "sed":
		return !hasFlag(args, 'i', "in-place")
	case "sort":
		return !hasFlag(args, 'o', "output-file")
	case "awk":
		// awk's own language redirects, so the write is inside the program text rather
		// than on the shell line the parser walked.
		return !slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, ">") })
	case "find":
		return !slices.ContainsFunc(args, func(a string) bool {
			return a == "-delete" || a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir"
		})
	}
	return true
}

// writeScanDepth bounds how far a nested script is followed. A payload that parses to
// another payload shrinks on every hop, so the bound is for a line built not to.
const writeScanDepth = 4

// writeTargetCandidates names every word the line's writes could be aimed at, in the order
// the walk reaches them: each writing redirect's target, and the operands of every command
// that is not a known reader.
//
// CANDIDATES rather than targets, because only a rule's own boundary can say which word is
// a path it cares about: `chmod 600 f` writes f and not 600, and nothing structural
// separates them. A rule that refused on any candidate at all would refuse `echo hi`.
//
// Any `sh -c` or `eval` payload is scanned on its own afterwards: peelWrappers hands back
// the commands inside one, but a redirect there belongs to the inner parse tree and is
// invisible to a walk of the outer one.
func writeTargetCandidates(command string, depth int) []string {
	if depth > writeScanDepth {
		return nil
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var out, nested []string
	syntax.Walk(f, func(n syntax.Node) bool {
		stmt, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		for _, r := range stmt.Redirs {
			if writesToFile(r.Op) {
				out = append(out, literalWord(r.Word.Parts))
			}
		}
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
			if script, ok := shellPayload(literalWords(call.Args)); ok {
				nested = append(nested, script)
			}
		}
		for _, c := range stmtCommands(stmt) {
			out = append(out, commandWriteCandidates(c, heredocText(stmt))...)
		}
		return true
	})
	for _, script := range nested {
		out = append(out, writeTargetCandidates(script, depth+1)...)
	}
	return out
}

// commandWriteCandidates names the words c could be pointed at when it writes.
//
// Every word is examined, not only the operands: a flag's value names a path too
// (`dd of=x`), and an interpreter's script is one argument carrying the path inside it.
//
// heredoc is the body redirected into the statement, and it counts only for an
// INTERPRETER, where the heredoc IS the program. For every other command a heredoc is
// data, and folding it in would read `cat > f <<EOF` prose as a list of write targets.
func commandWriteCandidates(c hint.Invocation, heredoc string) []string {
	name := path.Base(c.Name)
	if onlyReads(name, c.Args) {
		return nil
	}
	words := c.Args
	if name == "cp" || name == "install" {
		// Their operands are not uniform: only the destination is written, so copying a
		// file OUT of a guarded tree is a read.
		ops := operands(c.Args, "m")
		if len(ops) < 2 {
			return nil
		}
		words = ops[len(ops)-1:]
	}
	// An interpreter's whole program arrives as one argument, awk's included, so a path
	// sits inside prose there: the program is offered whole for a boundary that can match
	// inside it, and its quoted strings singly for one that has to resolve a path.
	if scriptedRewriteInterpreters[name] || name == "awk" {
		var out []string
		for _, w := range append(slices.Clone(words), heredoc) {
			if w == "" {
				continue
			}
			out = append(out, w)
			out = append(out, quotedLiterals(w)...)
		}
		return out
	}
	var out []string
	for _, w := range words {
		// Everywhere else a word carrying whitespace is prose rather than a path, which is
		// what keeps `echo "rm -rf .magus"` a quoted mention rather than a write.
		if strings.ContainsAny(w, " \t\n") {
			continue
		}
		out = append(out, w)
		if _, value, ok := strings.Cut(w, "="); ok && value != "" {
			out = append(out, value)
		}
	}
	return out
}

// quotedLiteralRe matches a single- or double-quoted string. The outer quoting is already
// gone by the time a program word reaches it, so what is left is the interpreter's own.
var quotedLiteralRe = regexp.MustCompile(`'([^']*)'|"([^"]*)"`)

// quotedLiterals are the quoted strings inside an interpreter's program text, so the path
// in `open('x/y.go','w')` is offered as a path and not only as the program holding it.
func quotedLiterals(program string) []string {
	var out []string
	for _, m := range quotedLiteralRe.FindAllStringSubmatch(program, -1) {
		for _, group := range m[1:] {
			if group != "" {
				out = append(out, group)
			}
		}
	}
	return out
}

// wrapperValueFlags are wrapper flags that consume the NEXT word, so the
// scan does not mistake that word for the wrapped program. `env -u GOROOT go
// test` is the case that matters here. env's -S/--split-string is deliberately
// absent: its value is a command line, not an operand, so it is peeled as a
// script (see the env case in peelWrappers) rather than skipped over.
var wrapperValueFlags = map[string]bool{
	"-u": true, "-C": true, "-n": true, "-I": true,
	"-L": true, "-P": true, "-d": true, "-s": true, "-k": true,
	"--signal": true, "--kill-after": true,
}

// skipWrapperArgs consumes a wrapper's own flags and operands, returning the
// words from the wrapped program onward.
func skipWrapperArgs(wrapper string, words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case strings.HasPrefix(w, "-"):
			if wrapperValueFlags[w] && len(words) > 1 {
				words = words[2:]
				continue
			}
			words = words[1:]
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "/"):
			// `env VAR=value cmd`: an assignment operand, not the program.
			words = words[1:]
		case wrapper == "timeout":
			// timeout's first non-flag operand is the DURATION, not the program.
			words = words[1:]
			wrapper = ""
		default:
			return words
		}
	}
	return nil
}

// envSplitString returns the command line carried by env's -S/--split-string,
// in each of its spellings: bundled (`-S'cmd'`), `=`-joined
// (`--split-string='cmd'`), and separated (`-S 'cmd'`, `--split-string 'cmd'`).
// Other env flags and VAR=value assignments may precede it; the first non-flag,
// non-assignment word is the program, at which point there is no -S to find.
func envSplitString(words []string) (string, bool) {
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "-S" || w == "--split-string":
			if i+1 < len(words) {
				return words[i+1], true
			}
			return "", false
		case strings.HasPrefix(w, "-S"):
			return w[len("-S"):], true
		case strings.HasPrefix(w, "--split-string="):
			return w[len("--split-string="):], true
		case strings.HasPrefix(w, "-"):
			continue
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "/"):
			continue
		default:
			return "", false
		}
	}
	return "", false
}

// findExecCommands resolves the command(s) find would launch through
// -exec/-execdir (and their interactive -ok/-okdir forms). Each clause runs the
// argv from after the flag up to the terminating `;` or `+`; find execs it
// directly with no shell, so the argv is peeled as its own command rather than
// reparsed as a script, which still unwraps an inner `sh -c` payload.
func findExecCommands(args []string) []hint.Invocation {
	var out []hint.Invocation
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-exec", "-execdir", "-ok", "-okdir":
			var argv []string
			j := i + 1
			for ; j < len(args); j++ {
				if args[j] == ";" || args[j] == "+" {
					break
				}
				argv = append(argv, args[j])
			}
			if len(argv) > 0 {
				out = append(out, peelWrappers(argv)...)
			}
			i = j
		}
	}
	return out
}

// shellDashC returns the script argument of a `-c` invocation. -c consumes the next word as
// the script and, taking an argument, is always the last flag of a short bundle (`sh -ec ...`);
// a long option (`--rcfile`, `--norc`) is never -c even when it contains the letter. The scan
// does not stop at the first non-flag word: an option-argument such as the path after
// `--rcfile` would otherwise end it before the real -c and let `bash --rcfile x -c '<payload>'`
// through unjudged.
func shellDashC(args []string) (string, bool) {
	for i, a := range args {
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' && a[len(a)-1] == 'c' && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// lastOfPipeline and firstOfPipeline resolve the commands immediately either
// side of one pipe, descending through a longer pipeline to reach them.
func lastOfPipeline(s *syntax.Stmt) []hint.Invocation {
	if bc, ok := s.Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.Pipe {
		return lastOfPipeline(bc.Y)
	}
	return stmtCommands(s)
}

func firstOfPipeline(s *syntax.Stmt) []hint.Invocation {
	if bc, ok := s.Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.Pipe {
		return firstOfPipeline(bc.X)
	}
	return stmtCommands(s)
}

func stmtCommands(s *syntax.Stmt) []hint.Invocation {
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok {
		return nil
	}
	return peelWrappers(literalWords(call.Args))
}

// busyWaitFires reports whether the line contains a loop whose body only sleeps.
//
// Structural, not textual, for the reason at the top of this file. The first version of
// this rule was a pattern and proved the point twice within a minute: it denied `echo
// 'until grep -q x f; do sleep 1; done'`, a quoted string that runs no loop, and then
// denied a heredoc that merely quoted the rule's own test cases. An AST knows a
// WhileClause from a word that looks like one.
func busyWaitFires(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if found {
			return false
		}
		if loop, ok := n.(*syntax.WhileClause); ok && bodyOnlySleeps(loop.Do) {
			found = true
			return false
		}
		return true
	})
	return found
}

// bodyOnlySleeps reports whether a loop body does nothing but sleep, which is what
// separates polling from work. A loop that builds, tests or prints each pass is doing
// something however long it runs; one that only sleeps is waiting, and the host already
// waits for free.
func bodyOnlySleeps(body []*syntax.Stmt) bool {
	sleeps := 0
	for _, s := range body {
		for _, c := range stmtCommands(s) {
			if path.Base(c.Name) != "sleep" {
				return false
			}
			sleeps++
		}
	}
	return sleeps > 0
}

// The rules below answer "which program runs, with what arguments", which is what the
// parser is for. They were patterns, and each one denied or advised on a line that merely
// MENTIONED the program: `echo "searching for the string sed -i in a file"` was refused as
// an in-place edit, and the same misfire caught a `grep` looking for where the rule was
// tested. Every predicate here takes the parsed commands, so a quoted string, a comment
// and a heredoc are words rather than commands.

// hasFlag reports whether args carry a long flag, or a short flag packed into a cluster
// (`-rn` carries `r`). Only the letter matters, not where it sits.
func hasFlag(args []string, short rune, long string) bool {
	for _, a := range args {
		if a == "--" {
			return false // everything after is an operand
		}
		if long != "" && (a == "--"+long || strings.HasPrefix(a, "--"+long+"=")) {
			return true
		}
		if len(a) > 1 && a[0] == '-' && !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], short) {
			return true
		}
	}
	return false
}

// operands are the arguments that are not flags nor a flag's own value, so a rule can ask
// what a command was pointed AT rather than how it was spelled.
//
// takesValue names the short flags that consume the next word. Without it `fd -t d` reads
// as a search for a file named "d", which is what the pattern this replaced could not tell
// apart and why it deliberately said nothing about that shape. Knowing it is the point of
// parsing.
func operands(args []string, takesValue string) []string {
	var out []string
	rest, skip := false, false
	for _, a := range args {
		switch {
		case skip:
			skip = false
		case rest:
			out = append(out, a)
		case a == "--":
			rest = true
		case strings.HasPrefix(a, "--"):
			// A long flag carries its value with `=`, or takes none we care about.
		case strings.HasPrefix(a, "-") && a != "-":
			last := a[len(a)-1:]
			skip = strings.Contains(takesValue, last)
		default:
			out = append(out, a)
		}
	}
	return out
}

// sedInPlaceFires reports an in-place sed. The flag may be packed (`-ni`) or long.
func sedInPlaceFires(cmds []hint.Invocation) bool {
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool {
		return path.Base(c.Name) == "sed" && hasFlag(c.Args, 'i', "in-place")
	})
}

// scriptedRewriteInterpreters are the inline interpreters a refused rewrite reaches for
// once `sed -i` is denied.
var scriptedRewriteInterpreters = map[string]bool{
	"python": true, "python3": true, "perl": true, "ruby": true, "node": true,
}

// scriptedRewriteFires reports an interpreter running a regex substitution and writing the
// result back, or perl/ruby invoked with -i.
//
// Narrow on purpose, as before: an interpreter that merely WRITES is ordinary authoring.
// What is refused is substitute-then-write, the shape that cannot tell a symbol from a word
// that looks like one.
//
// This one walks the AST itself rather than reading hint.Invocation, because the script is
// commonly a HEREDOC and a heredoc body is a redirect rather than an argument. It is
// deliberately the only rule that reads heredoc text: for this rule the heredoc IS the
// program, while for every other rule it is data, and folding it into the shared command
// words would make a heredoc that merely quotes `sed -i` a refused edit.
func scriptedRewriteFires(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return scriptedRewriteRe.MatchString(command)
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if found {
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
		for _, c := range peelWrappers(literalWords(call.Args)) {
			if !scriptedRewriteInterpreters[path.Base(c.Name)] {
				continue
			}
			if hasFlag(c.Args, 'i', "in-place") && path.Base(c.Name) != "node" {
				found = true
				return false
			}
			script := strings.Join(c.Args, "\n") + "\n" + heredocText(st)
			if substitutes(script) && strings.Contains(script, ".write(") {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// heredocText is the body of every heredoc redirected into a statement.
func heredocText(st *syntax.Stmt) string {
	var b strings.Builder
	for _, r := range st.Redirs {
		if r.Hdoc == nil {
			continue
		}
		for _, part := range r.Hdoc.Parts {
			if lit, ok := part.(*syntax.Lit); ok {
				b.WriteString(lit.Value)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// substitutes reports whether a script performs a regex or string substitution.
func substitutes(script string) bool {
	for _, call := range []string{"re.sub", "re.subn", "str.replace", ".replace("} {
		if strings.Contains(script, call) {
			return true
		}
	}
	return false
}

// codeSearchFires reports a repo-wide CONTENT search: a recursive grep, or a bare ripgrep
// or ag, both effectively always repo-wide. A plain `grep pattern file` reads one file and
// is left alone.
func codeSearchFires(cmds []hint.Invocation) bool {
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool {
		switch path.Base(c.Name) {
		case "grep", "egrep", "fgrep":
			return hasFlag(c.Args, 'r', "recursive") || hasFlag(c.Args, 'R', "dereference-recursive")
		case "rg", "ag":
			return true
		}
		return false
	})
}

// fileFindFires reports a repo-wide search for a file by NAME. `find . -type d` and `fd -t
// d` list a tree rather than look a name up, and stay silent; fd is recursive by default,
// so its admitting shapes are the name query itself.
func fileFindFires(cmds []hint.Invocation) bool {
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool {
		switch path.Base(c.Name) {
		case "find":
			return slices.Contains(c.Args, "-name") || slices.Contains(c.Args, "-iname")
		case "fd":
			return hasFlag(c.Args, 'e', "extension") || hasFlag(c.Args, 'g', "glob") ||
				len(operands(c.Args, fdValueFlags)) > 0
		}
		return false
	})
}

// ciWatchFires reports a gh invocation that BLOCKS until a CI run reaches a terminal
// state: `gh run watch`, and the --watch form of `gh run view` and `gh pr checks`.
//
// Reading a result that already exists (`gh run view --log`, a bare `gh pr checks`) is
// untouched. The rule is about the WAITING.
func ciWatchFires(cmds []hint.Invocation) bool {
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool {
		if path.Base(c.Name) != "gh" {
			return false
		}
		ops := operands(c.Args, "")
		if len(ops) >= 2 && ops[0] == "run" && ops[1] == "watch" {
			return true
		}
		if !hasFlag(c.Args, 0, "watch") {
			return false
		}
		return len(ops) >= 2 && (ops[0] == "run" && ops[1] == "view" || ops[0] == "pr" && ops[1] == "checks")
	})
}

// docReaders are the commands that read or search a file's text.
var docReaders = map[string]bool{
	"cat": true, "bat": true, "head": true, "tail": true, "less": true, "more": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
}

// docSearchFires reports a read or search pointed at a markdown file. Markdown headings are
// indexed as doc-section nodes, so the answer is a section query rather than a whole-file
// scan. It asks whether an OPERAND is markdown, so a pattern that merely contains ".md"
// does not count.
func docSearchFires(cmds []hint.Invocation) bool {
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool {
		if !docReaders[path.Base(c.Name)] {
			return false
		}
		// grep's -e/-f take a value, so a pattern file is not read as the target.
		return slices.ContainsFunc(operands(c.Args, "ef"), func(a string) bool {
			return strings.HasSuffix(a, ".md")
		})
	})
}
