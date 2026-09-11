package main

import (
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Resolving a shell line into the commands it would actually run. No policy lives
// here, which is the point of the seam: every wrong verdict this guard has produced
// was a tokenizing mistake rather than a rule mistake, so the tokenizing is worth
// reading, testing, and changing on its own. The rules that consume this are in
// guard_shell.go and guard_git.go.

// A pass-through wrapper runs ANOTHER command, with the environment or timeout
// adjusted first. It is never itself the finding: the guard peels it off and
// judges the payload, so an unapproved launcher cannot tunnel a raw tool past
// the guard.
//
// A launcher's declared task subcommand is deliberately absent: it runs a task,
// not a smuggled command, and peeling it would misattribute the task's contents.
var guardWrappers = map[string]bool{
	"env": true, "nohup": true, "command": true, "exec": true,
	"time": true, "timeout": true, "nice": true, "stdbuf": true,
	"xargs": true, "setsid": true, "sudo": true, "doas": true,
	"mise": true, "rtx": true,
	"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true,
	"eval": true,
}

// guardCommand is one command the line would actually run: the program, and its
// arguments with quoting already resolved.
type guardCommand struct {
	Name string
	Args []string
}

// parseGuardCommands resolves a shell line into every command it would run.
//
// A PARSER rather than a pattern because every wrong verdict this guard has
// produced was a tokenizing mistake, not a policy one: a regex cannot tell a
// pipe from a pipe inside quotes, an assignment prefix from an argument, or see
// into a shell's own -c argument. An AST answers all three structurally.
//
// The bool is false when the line does not parse, and the caller skips the
// raw-tool rules rather than guessing: less of a bypass than it looks, since
// shell that does not parse does not run either.
func parseGuardCommands(command string) ([]guardCommand, bool) {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	var out []guardCommand
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
func peelWrappers(words []string) []guardCommand {
	for len(words) > 0 {
		name := path.Base(words[0])
		switch {
		case name == "find":
			// find is not a wrapper (it still runs find) but -exec/-execdir
			// launches a command find never reparses through a shell, so the
			// payload has to be judged on its own. Keep find itself too.
			return append([]guardCommand{{Name: name, Args: words[1:]}}, findExecCommands(words[1:])...)

		case !guardWrappers[name]:
			return []guardCommand{{Name: name, Args: words[1:]}}

		case name == "env":
			// env -S / --split-string takes its argument AS the command line and,
			// unlike sh -c, never reparses it, so it is peeled like -c: the
			// remainder is parsed and the full ruleset runs over what it contains.
			if script, ok := envSplitString(words[1:]); ok {
				inner, _ := parseGuardCommands(script)
				return inner
			}
			rest := skipWrapperArgs(name, words[1:])
			if len(rest) == 0 {
				return []guardCommand{{Name: name, Args: words[1:]}}
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
			return []guardCommand{{Name: name, Args: words[1:]}}

		case guardShells[name]:
			script, ok := shellDashC(words[1:])
			if !ok {
				return []guardCommand{{Name: name, Args: words[1:]}}
			}
			inner, _ := parseGuardCommands(script)
			return inner

		case name == "eval":
			// eval concatenates its arguments and runs the result as a script.
			// Worth following for the same reason as `sh -c`: when the words are
			// literal it is an exact synonym for running them directly. When they
			// come from a variable, literalWord already rendered them empty and
			// the reparse finds nothing, which is the honest answer.
			inner, _ := parseGuardCommands(strings.Join(words[1:], " "))
			return inner

		default:
			// timeout, nice, stdbuf, xargs, nohup, command, exec, time:
			// step over the wrapper's own flags and operands to reach the
			// program it runs.
			rest := skipWrapperArgs(name, words[1:])
			if len(rest) == 0 {
				return []guardCommand{{Name: name, Args: words[1:]}}
			}
			words = rest
		}
	}
	return nil
}

// guardShells are the wrappers whose -c argument is a script. Derived from guardWrappers
// so the two cannot disagree about what a shell is.
var guardShells = func() map[string]bool {
	out := map[string]bool{}
	for _, name := range []string{"sh", "bash", "zsh", "ksh", "dash"} {
		if !guardWrappers[name] {
			panic("guardShells: " + name + " is not a wrapper")
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
		case guardShells[name]:
			return shellDashC(words[1:])
		case name == "eval":
			return strings.Join(words[1:], " "), true
		case name == "env":
			if script, ok := envSplitString(words[1:]); ok {
				return script, true
			}
			fallthrough
		case guardWrappers[name]:
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

// guardWrapperValueFlags are wrapper flags that consume the NEXT word, so the
// scan does not mistake that word for the wrapped program. `env -u GOROOT go
// test` is the case that matters here. env's -S/--split-string is deliberately
// absent: its value is a command line, not an operand, so it is peeled as a
// script (see the env case in peelWrappers) rather than skipped over.
var guardWrapperValueFlags = map[string]bool{
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
			if guardWrapperValueFlags[w] && len(words) > 1 {
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
func findExecCommands(args []string) []guardCommand {
	var out []guardCommand
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
func lastOfPipeline(s *syntax.Stmt) []guardCommand {
	if bc, ok := s.Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.Pipe {
		return lastOfPipeline(bc.Y)
	}
	return stmtCommands(s)
}

func firstOfPipeline(s *syntax.Stmt) []guardCommand {
	if bc, ok := s.Cmd.(*syntax.BinaryCmd); ok && bc.Op == syntax.Pipe {
		return firstOfPipeline(bc.X)
	}
	return stmtCommands(s)
}

func stmtCommands(s *syntax.Stmt) []guardCommand {
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
func sedInPlaceFires(cmds []guardCommand) bool {
	return slices.ContainsFunc(cmds, func(c guardCommand) bool {
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
// This one walks the AST itself rather than reading guardCommand, because the script is
// commonly a HEREDOC and a heredoc body is a redirect rather than an argument. It is
// deliberately the only rule that reads heredoc text: for this rule the heredoc IS the
// program, while for every other rule it is data, and folding it into the shared command
// words would make a heredoc that merely quotes `sed -i` a refused edit.
func scriptedRewriteFires(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return guardScriptedRewriteRe.MatchString(command)
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
func codeSearchFires(cmds []guardCommand) bool {
	return slices.ContainsFunc(cmds, func(c guardCommand) bool {
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
func fileFindFires(cmds []guardCommand) bool {
	return slices.ContainsFunc(cmds, func(c guardCommand) bool {
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
func ciWatchFires(cmds []guardCommand) bool {
	return slices.ContainsFunc(cmds, func(c guardCommand) bool {
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
func docSearchFires(cmds []guardCommand) bool {
	return slices.ContainsFunc(cmds, func(c guardCommand) bool {
		if !docReaders[path.Base(c.Name)] {
			return false
		}
		// grep's -e/-f take a value, so a pattern file is not read as the target.
		return slices.ContainsFunc(operands(c.Args, "ef"), func(a string) bool {
			return strings.HasSuffix(a, ".md")
		})
	})
}
