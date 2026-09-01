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
// raw-tool rules rather than guessing - less of a bypass than it looks, since
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
			// find is not a wrapper - it still runs find - but -exec/-execdir
			// launches a command find never reparses through a shell, so the
			// payload has to be judged on its own. Keep find itself too.
			return append([]guardCommand{{Name: name, Args: words[1:]}}, findExecCommands(words[1:])...)

		case !guardWrappers[name]:
			return []guardCommand{{Name: name, Args: words[1:]}}

		case name == "env":
			// env -S / --split-string takes its argument AS the command line and,
			// unlike sh -c, never reparses it - so it is peeled like -c: the
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

var guardShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true}

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
// reparsed as a script - which still unwraps an inner `sh -c` payload.
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
