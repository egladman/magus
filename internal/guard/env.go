package guard

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
)

// A retired or misspelled MAGUS_* variable is misconfiguration: the setting the caller
// meant never takes effect. The day MAGUS_NO_WAIT was removed, agents prefixed 462
// commands with it, copied from briefs written the week before.
//
// config.EnvVarProblem is the one decision, shared with magus's own startup refusal, so
// the guard denies exactly what the binary would refuse. A name it cannot prove wrong
// (one a newer magus or the repository's own tooling reads) passes here as it does there.

// misconfiguredMagusEnv returns the first MAGUS_* name the line hands a command's
// environment that config.EnvVarProblem proves wrong, and its message: a `NAME=value`
// prefix, an `env NAME=value` or `env -u NAME` operand, or an `export`. A bare
// `NAME=value` statement is not one: it sets a shell variable no child process sees.
func misconfiguredMagusEnv(command string, d Dialect) (name, msg string, ok bool) {
	return misconfiguredMagusEnvAt(command, d, 0)
}

func misconfiguredMagusEnvAt(command string, d Dialect, depth int) (name, msg string, ok bool) {
	if depth > writeScanDepth {
		return "", "", false
	}
	f, err := parseFile(command, d)
	if err != nil {
		return "", "", false
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		if ok {
			return false
		}
		var names []string
		switch n := n.(type) {
		case *syntax.CallExpr:
			if len(n.Args) > 0 {
				for _, a := range n.Assigns {
					if a.Name != nil {
						names = append(names, a.Name.Value)
					}
				}
			}
			words := literalWords(n.Args)
			names = append(names, envOperandNames(words)...)
			if script, isShell := shellPayload(words); isShell {
				if inner, innerMsg, found := misconfiguredMagusEnvAt(script, d, depth+1); found {
					name, msg, ok = inner, innerMsg, true
					return false
				}
			}
		case *syntax.DeclClause:
			if n.Variant != nil && n.Variant.Value == "export" {
				for _, a := range n.Args {
					if a.Name != nil {
						names = append(names, a.Name.Value)
					}
				}
			}
		}
		for _, candidate := range names {
			if problem, bad := config.EnvVarProblem(candidate); bad {
				name, msg, ok = candidate, problem, true
				return false
			}
		}
		return true
	})
	return name, msg, ok
}

// envOperandNames reads the variable names an `env` invocation sets or unsets, stepping
// over the wrappers that may stand in front of it.
func envOperandNames(words []string) []string {
	for len(words) > 0 {
		name := path.Base(words[0])
		if name == "env" {
			break
		}
		if !wrappers[name] || shells[name] || name == "eval" || name == "mise" || name == "rtx" {
			return nil
		}
		words = skipWrapperArgs(name, words[1:])
	}
	if len(words) == 0 {
		return nil
	}
	var names []string
	args := words[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-u" || a == "--unset":
			if i+1 < len(args) {
				names = append(names, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--unset="):
			names = append(names, strings.TrimPrefix(a, "--unset="))
		case strings.HasPrefix(a, "-u"):
			names = append(names, strings.TrimPrefix(a, "-u"))
		case strings.HasPrefix(a, "-"):
		case strings.Contains(a, "=") && !strings.HasPrefix(a, "/"):
			name, _, _ := strings.Cut(a, "=")
			names = append(names, name)
		default:
			return names
		}
	}
	return names
}

// denyMisconfiguredMagusEnv leads with the fix: the variable does nothing, so the
// correction is to drop or rename it.
func denyMisconfiguredMagusEnv(msg string) string {
	return "[MGS1046] " + msg + "\n`" + hint.ConfigView.With("-h") + "` lists every MAGUS_* variable beside its flag."
}
