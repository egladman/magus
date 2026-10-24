package guard

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// pythonNameRe matches python, python3 and a versioned python3.x.
var pythonNameRe = regexp.MustCompile(`^python(?:3(?:\.\d+)?)?$`)

// interpreterScriptRe is interpreterScripting's answer for a line the shell parser rejects:
// an interpreter at a command position given an inline program, stdin, or a .py file.
var interpreterScriptRe = regexp.MustCompile(`(?m)(?:^|[;&|(]|\$\()\s*(?:\S+=\S*\s+)*(?:\S*/)?` +
	`(?:(python(?:3(?:\.\d+)?)?|node|perl|ruby)(?:\s+-[A-Za-z]*[ceEi]\b|\s+--(?:eval|print)\b|\s+-(?:\s|$)|\s*<<|\s*$)` +
	`|(python)(?:3(?:\.\d+)?)?\s+(?:-\S+\s+)*\S+\.py\b)`)

// interpreterScripting names the interpreter the first command on the line scripts in, as
// interpreterScripts reads one.
func interpreterScripting(cmds []hint.Invocation, parsed bool, command string) (string, bool) {
	if !parsed {
		m := interpreterScriptRe.FindStringSubmatch(command)
		if m == nil {
			return "", false
		}
		return m[1] + m[2], true
	}
	for _, c := range cmds {
		if name, ok := interpreterScripts(c); ok {
			return name, true
		}
	}
	return "", false
}

// interpreterScripts reports c running a program in python, node, perl or ruby that it
// carries itself or reads from stdin, or a python file. A program file in node, perl or
// ruby passes here; its content is judged where the line's scripts are read.
func interpreterScripts(c hint.Invocation) (string, bool) {
	name := path.Base(c.Name)
	switch {
	case strings.HasSuffix(name, ".py"):
		return name, true
	case pythonNameRe.MatchString(name):
		return name, pythonScripts(c.Args)
	case name == "node":
		return name, nodeScripts(c.Args)
	case name == "perl":
		return name, perlScripts(c.Args)
	case name == "ruby":
		return name, rubyScripts(c.Args)
	}
	return "", false
}

// pythonScripts reports -c, a `-` operand, no program at all, or a .py program. -m runs a
// module and -V/--help print, so each ends the walk allowed.
func pythonScripts(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-":
			return true
		case a == "--":
			return i+1 >= len(args) || strings.HasSuffix(args[i+1], ".py")
		case a == "--version" || a == "--help":
			return false
		case a == "--check-hash-based-pycs":
			i++
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			for j := 1; j < len(a); j++ {
				switch a[j] {
				case 'c':
					return true
				case 'm', 'V', 'h':
					return false
				case 'W', 'X':
					// The rest of the cluster is the option's value, else the next word is.
					if j == len(a)-1 {
						i++
					}
					j = len(a)
				}
			}
		default:
			return strings.HasSuffix(a, ".py")
		}
	}
	return true
}

// nodeValueFlags are node options whose value is the next word.
var nodeValueFlags = map[string]bool{
	"-r": true, "--require": true, "--import": true, "--loader": true, "--experimental-loader": true,
	"-C": true, "--conditions": true, "--input-type": true, "--env-file": true,
}

// nodeScripts reports -e, -p, --eval, --print, a `-` operand, or no program. --test and
// --run name files and package scripts, so they pass with a file.
func nodeScripts(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, _, valued := strings.Cut(a, "=")
		switch {
		case a == "-":
			return true
		case a == "--":
			return i+1 >= len(args)
		case name == "-e" || name == "-p" || name == "--eval" || name == "--print" || a == "-pe" || a == "-ep":
			return true
		case a == "-v" || a == "--version" || a == "-h" || a == "--help" || a == "--test" || a == "--run":
			return false
		case nodeValueFlags[name] && !valued:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return false
		}
	}
	return true
}

// perlScripts reports a flag cluster carrying e, E or i, a `-` operand, or no program. A
// cluster stops at an option whose value is the rest of it, so `-Iinclude` is no -i.
func perlScripts(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-":
			return true
		case a == "--":
			return i+1 >= len(args)
		case a == "-v" || a == "-V" || a == "-h" || a == "--version" || a == "--help":
			return false
		case strings.HasPrefix(a, "-"):
			if clusterCarries(a[1:], "eEi", "IMmxdDCV") {
				return true
			}
		default:
			return false
		}
	}
	return true
}

// rubyScripts reports -e or -i in any cluster, a `-` operand, or no program.
func rubyScripts(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-":
			return true
		case a == "--":
			return i+1 >= len(args)
		case a == "-v" || a == "-h" || a == "--version" || a == "--help":
			return false
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			if clusterCarries(a[1:], "ei", "IrCExFKWT0") {
				return true
			}
			// A valued option alone in its cluster takes the next word.
			if len(a) == 2 && strings.ContainsRune("IrCEx", rune(a[1])) {
				i++
			}
		default:
			return false
		}
	}
	return true
}

// clusterCarries reports a flag in cluster (its letters, the dash dropped) that is one of
// flags, reading up to the first option in valued, whose value is the rest of the cluster.
func clusterCarries(cluster, flags, valued string) bool {
	for _, c := range cluster {
		if strings.ContainsRune(flags, c) {
			return true
		}
		if strings.ContainsRune(valued, c) {
			return false
		}
	}
	return false
}

// interpreterScriptVerdict is interpreter-script's refusal; interp is the interpreter the
// line named, "" for a script judged from its file.
func interpreterScriptVerdict(interp string) ShellVerdict {
	what := "this script"
	if interp != "" {
		what = interp
	}
	return ShellVerdict{
		Deny: "script in Buzz, not " + what + ": `" + hint.Buzz.With("-e", "'<buzz>'") + "`; load the magus-buzz-lang skill for its syntax.",
		Why: "The guard reads what a Buzz program does; a python, node, perl or ruby program arrives as one string it cannot follow.\n" +
			"A script worth keeping runs as `" + hint.Buzz.With("<file>") + "`. `--version`, `python -m`, `node file.js`, `perl file.pl`, bash and sh are untouched.",
		Rule: denyRule{Name: denyRuleInterpreterScript},
	}
}

// buzzSpawnVerdict is the first deny a command a `magus buzz -e` body or stdin heredoc on
// the line starts earns. It reads a hooked call, never limits Buzz: proc runs anything, and
// a script run outside the harness is not judged at all.
func buzzSpawnVerdict(deps Dependencies, held *heldAdvice, command string, d Dialect) (ShellVerdict, bool) {
	for _, program := range inlineBuzzPrograms(command, d) {
		if v, ok := buzzProgramSpawnVerdict(deps, held, program); ok {
			return v, true
		}
	}
	return ShellVerdict{}, false
}

// inlineBuzzPrograms are the programs the line's `magus buzz` calls carry. --check runs
// nothing, so its program starts nothing either.
func inlineBuzzPrograms(command string, d Dialect) []string {
	f, err := parseFile(command, d)
	if err != nil {
		return nil
	}
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		st, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := st.Cmd.(*syntax.CallExpr)
		if !ok {
			return true
		}
		for _, c := range peelWrappers(literalWords(call.Args), d) {
			if path.Base(c.Name) != "magus" || slices.Contains(c.Args, "--check") {
				continue
			}
			if program, ok := inlineBuzz(c.Args, heredocText(st)); ok {
				out = append(out, program)
			}
		}
		return true
	})
	return out
}

// buzzProgramSpawnVerdict judges each command program's proc calls start as the line it
// would be typed as, every rule included, and returns the first deny. A demoted deny is
// left in held.
func buzzProgramSpawnVerdict(deps Dependencies, held *heldAdvice, program string) (ShellVerdict, bool) {
	for _, line := range buzzSpawns(program) {
		v := evaluateLine(deps, held, line, DialectBash)
		if v.Deny == "" {
			continue
		}
		return judgedFrom(v, line, "\nJudged as the command the Buzz program starts through proc: a command a script spawns gets the verdict it would get typed."), true
	}
	return ShellVerdict{}, false
}

// procCallRe finds a proc\exec or proc\shell call in a Buzz program.
var procCallRe = regexp.MustCompile(`\bproc\\(exec|shell)\s*\(`)

// buzzLabelRe is a labeled Buzz argument's label.
var buzzLabelRe = regexp.MustCompile(`^([A-Za-z_]\w*)\s*:\s*`)

// buzzSpawns are the shell lines a Buzz program's proc calls start, spelled from literals:
// proc\shell's line, and proc\exec's command and argument list quoted into one. A call
// whose command or argument list is computed at run time proves nothing and is skipped.
func buzzSpawns(program string) []string {
	var out []string
	for _, m := range procCallRe.FindAllStringSubmatchIndex(program, -1) {
		args, ok := callArguments(program, m[1]-1)
		if !ok {
			continue
		}
		parts := splitArguments(args)
		if len(parts) == 0 {
			continue
		}
		if program[m[2]:m[3]] != "exec" {
			if line, ok := buzzString(parts[0]); ok {
				out = append(out, line)
			}
			continue
		}
		argv, ok := procExecArgv(parts)
		if !ok {
			continue
		}
		if line, ok := quoteArgv(argv); ok {
			out = append(out, line)
		}
	}
	return out
}

// procExecArgv is the argv a proc\exec call's arguments spell: its command, then the list
// passed second or labeled args.
func procExecArgv(parts []string) ([]string, bool) {
	cmd, ok := buzzString(parts[0])
	if !ok {
		return nil, false
	}
	argv := []string{cmd}
	for i, p := range parts[1:] {
		label := ""
		if m := buzzLabelRe.FindStringSubmatch(p); m != nil {
			label, p = m[1], p[len(m[0]):]
		}
		if label == "args" || label == "" && i == 0 {
			words, ok := buzzStringList(p)
			if !ok {
				return nil, false
			}
			argv = append(argv, words...)
		}
	}
	return argv, true
}

// buzzStringList is a list literal of string literals, whole.
func buzzStringList(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	inner, ok := callArguments(s, 0)
	if !ok || len(inner)+2 != len(s) {
		return nil, false
	}
	var out []string
	for _, p := range splitArguments(inner) {
		w, ok := buzzString(p)
		if !ok {
			return nil, false
		}
		out = append(out, w)
	}
	return out, true
}

// buzzString is a Buzz string literal's value. A quoted string carrying an interpolation
// is computed, so it is not one; a backtick string is raw.
func buzzString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != s[len(s)-1] {
		return "", false
	}
	quote, body := s[0], s[1:len(s)-1]
	switch quote {
	case '`':
		return body, !strings.Contains(body, "`")
	case '"', '\'':
	default:
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == quote || c == '{':
			return "", false
		case c == '\\' && i+1 < len(body):
			i++
			switch body[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(body[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// quoteArgv spells argv as the shell line that runs it.
func quoteArgv(argv []string) (string, bool) {
	words := make([]string, len(argv))
	for i, w := range argv {
		q, err := syntax.Quote(w, syntax.LangBash)
		if err != nil {
			return "", false
		}
		words[i] = q
	}
	return strings.Join(words, " "), true
}
