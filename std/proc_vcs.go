//go:build !wasm

package std

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// vcsBinaries are the backends vcs\cmd drives.
var vcsBinaries = []string{"git", "hg", "sl", "jj"}

func isVCSBinary(name string) bool {
	return slices.Contains(vcsBinaries, strings.TrimSuffix(filepath.Base(name), ".exe"))
}

// procVCSRefusal is why proc\exec may not start cmd with args in dir, nil when it may.
// Version control runs through vcs\cmd alone, because that is where the lease-vcs gate
// grades it; a VCS binary run by proc\exec, by name, by path, behind a wrapper or inside
// a shell line, would commit or rewrite a job's tree around every lease rule.
func procVCSRefusal(cmd string, args []string, dir string) error {
	for _, c := range argvCommands(append([]string{cmd}, args...)) {
		if isVCSBinary(c.Name) {
			return fmt.Errorf(`proc\exec %s: %s runs only through vcs\cmd, which holds it to the lease rules: call %s instead`,
				cmd, c.Name, vcsCmdCall(c.Args, dir))
		}
	}
	return nil
}

// vcsCmdCall renders the vcs\cmd call that runs args in dir.
func vcsCmdCall(args []string, dir string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	call := `vcs\cmd([` + strings.Join(quoted, ", ") + `]`
	if dir != "" {
		call += `, {"dir": ` + strconv.Quote(dir) + `}`
	}
	return call + ")"
}

// The guard reads an agent's shell line the same way (internal/guard/parse.go), but
// it imports this package, so the peeling is repeated here at the depth proc\exec
// needs: which programs a process would start.

// execWrappers run another command after adjusting its environment, priority or
// lifetime, so the program after their own flags is the one that runs.
var execWrappers = map[string]bool{
	"env": true, "nohup": true, "command": true, "exec": true, "time": true,
	"timeout": true, "gtimeout": true, "nice": true, "stdbuf": true, "xargs": true,
	"setsid": true, "sudo": true, "doas": true,
}

// execShells run their -c argument as a script.
var execShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true}

// wrapperValueFlags consume the next word, which is therefore not the wrapped program
// (`env -u GOROOT git`, `timeout -s KILL 5 git`).
var wrapperValueFlags = map[string]bool{
	"-u": true, "-C": true, "-n": true, "-I": true, "-L": true, "-P": true,
	"-d": true, "-s": true, "-k": true, "--signal": true, "--kill-after": true,
}

// argvCommands is every program argv would start: argv's own, with a wrapper peeled to
// the program it runs, a shell's -c script and eval's words to the commands in them,
// and find's -exec clauses alongside find. Names are base names.
func argvCommands(words []string) []hint.Invocation {
	for len(words) > 0 {
		name := path.Base(words[0])
		switch {
		case name == "find":
			return append([]hint.Invocation{{Name: name, Args: words[1:]}}, findExecCommands(words[1:])...)
		case execShells[name]:
			if script, ok := dashCScript(words[1:]); ok {
				return lineCommands(script)
			}
			return []hint.Invocation{{Name: name, Args: words[1:]}}
		case name == "eval":
			return lineCommands(strings.Join(words[1:], " "))
		case name == "env":
			if script, ok := envSplitString(words[1:]); ok {
				return lineCommands(script)
			}
		case !execWrappers[name]:
			return []hint.Invocation{{Name: name, Args: words[1:]}}
		}
		rest := skipWrapperArgs(name, words[1:])
		if len(rest) == 0 {
			return []hint.Invocation{{Name: name, Args: words[1:]}}
		}
		words = rest
	}
	return nil
}

// lineCommands is every program a shell line would start. A line that does not parse
// falls back to reading every word as a program: over-eager, since `echo git` reads as
// git, but the safe direction for a line nothing could read.
func lineCommands(line string) []hint.Invocation {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(line), "")
	if err != nil {
		words := strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(" \t\n;&|()`$<>'\"", r) })
		out := make([]hint.Invocation, len(words))
		for i, w := range words {
			out[i] = hint.Invocation{Name: path.Base(w), Args: words[i+1:]}
		}
		return out
	}
	var out []hint.Invocation
	syntax.Walk(f, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			words := make([]string, len(call.Args))
			for i, w := range call.Args {
				words[i] = literalWord(w.Parts)
			}
			out = append(out, argvCommands(words)...)
		}
		return true
	})
	return out
}

// literalWord is the text the shell passes for a word, as far as it is known without
// running anything; an expansion renders empty, and a command substitution's own
// commands are CallExprs the walk reaches on its own.
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

// skipWrapperArgs is words from the wrapped program on, past the wrapper's own flags,
// `VAR=value` assignments and, for timeout, its duration.
func skipWrapperArgs(wrapper string, words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case strings.HasPrefix(w, "-"):
			if wrapperValueFlags[w] && len(words) > 1 {
				words = words[1:]
			}
			words = words[1:]
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "/"):
			words = words[1:]
		case wrapper == "timeout" || wrapper == "gtimeout":
			words = words[1:]
			wrapper = ""
		default:
			return words
		}
	}
	return nil
}

// dashCScript is the script a shell's -c runs. -c takes the next word and ends a short
// bundle (`sh -ec ...`); the scan goes past option arguments such as `--rcfile <path>`.
func dashCScript(args []string) (string, bool) {
	for i, a := range args {
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' && a[len(a)-1] == 'c' && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// envSplitString is the command line env -S / --split-string carries, in each spelling.
func envSplitString(words []string) (string, bool) {
	for i, w := range words {
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
		case strings.HasPrefix(w, "-"), strings.Contains(w, "=") && !strings.HasPrefix(w, "/"):
			continue
		default:
			return "", false
		}
	}
	return "", false
}

// findExecCommands is what find's -exec, -execdir, -ok and -okdir clauses start: the
// argv up to the terminating `;` or `+`, with no shell between.
func findExecCommands(args []string) []hint.Invocation {
	var out []hint.Invocation
	for i := 0; i < len(args); i++ {
		if args[i] != "-exec" && args[i] != "-execdir" && args[i] != "-ok" && args[i] != "-okdir" {
			continue
		}
		j := i + 1
		for j < len(args) && args[j] != ";" && args[j] != "+" {
			j++
		}
		out = append(out, argvCommands(args[i+1:j])...)
		i = j
	}
	return out
}
