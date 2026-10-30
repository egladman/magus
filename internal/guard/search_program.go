package guard

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// asSearch reads `git grep` and programSearch's tools as grep. It leaves a git
// grep relocated by -C, --git-dir or --work-tree, which searches another tree.
// It leaves one carrying -c, which can change the pattern dialect.
func asSearch(c hint.Invocation) hint.Invocation {
	if g, ok := programSearch(c); ok {
		return g
	}
	if path.Base(c.Name) != "git" {
		return c
	}
	g := parseGit(c.Args)
	if g.sub != "grep" || len(g.dirs) > 0 || g.opaque || g.configured {
		return c
	}
	return hint.Invocation{Name: "grep", Args: append([]string{"-r"}, g.rest...)}
}

var (
	// awkAddressRe selects lines by one regex address and one plain action.
	// A range, a negation or a BEGIN block is another question.
	awkAddressRe     = regexp.MustCompile(`^\s*/((?:\\.|[^/\\])+)/\s*(?:\{[^{}]*\})?\s*$`)
	sedPrintRe       = regexp.MustCompile(`^\s*/((?:\\.|[^/\\])+)/p\s*$`)
	perlPrintIfRe    = regexp.MustCompile(`^\s*print(?:\s+\$_)?\s+if\s+(?:\$_\s*=~\s*)?m?/((?:\\.|[^/\\])+)/(i?);?\s*$`)
	perlMatchPrintRe = regexp.MustCompile(`^\s*m?/((?:\\.|[^/\\])+)/(i?)\s*(?:and|&&)\s*print;?\s*$`)
	awkVarOperandRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// programSearch reads ack, and an awk, sed or perl program printing the lines
// a regex matches, as grep. Any other program reports false. A search with no
// file operand reads stdin, so placeSearch leaves its grep alone.
func programSearch(c hint.Invocation) (hint.Invocation, bool) {
	switch path.Base(c.Name) {
	case "awk", "gawk", "mawk":
		return awkSearch(c.Args)
	case "sed":
		return sedSearch(c.Args)
	case "perl":
		return perlSearch(c.Args)
	case "ack":
		return hint.Invocation{Name: "grep", Args: append([]string{"-r"}, c.Args...)}, true
	}
	return hint.Invocation{}, false
}

// grepOf is the grep for pattern, written as a delimited regex, in files.
func grepOf(flags []string, pattern string, files []string) hint.Invocation {
	args := append(slices.Clone(flags), "-e", strings.ReplaceAll(pattern, `\/`, "/"))
	return hint.Invocation{Name: "grep", Args: append(args, files...)}
}

func awkSearch(args []string) (hint.Invocation, bool) {
	var operands []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "--file="):
			return hint.Invocation{}, false
		case a == "-F" || a == "-v":
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			operands = append(operands, a)
		}
	}
	if len(operands) < 2 {
		return hint.Invocation{}, false
	}
	m := awkAddressRe.FindStringSubmatch(operands[0])
	if m == nil || slices.ContainsFunc(operands[1:], awkVarOperandRe.MatchString) {
		return hint.Invocation{}, false
	}
	return grepOf([]string{"-E"}, m[1], operands[1:]), true
}

func sedSearch(args []string) (hint.Invocation, bool) {
	var operands, flags []string
	quiet := false
	script := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case a == "-e" || a == "--expression":
			if i+1 >= len(args) || script != "" {
				return hint.Invocation{}, false
			}
			i++
			script = args[i]
		case a == "-n" || a == "--quiet" || a == "--silent":
			quiet = true
		case a == "-E" || a == "-r" || a == "--regexp-extended":
			flags = append(flags, "-E")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return hint.Invocation{}, false
		default:
			operands = append(operands, a)
		}
	}
	if script == "" && len(operands) > 0 {
		script, operands = operands[0], operands[1:]
	}
	m := sedPrintRe.FindStringSubmatch(script)
	if !quiet || m == nil {
		return hint.Invocation{}, false
	}
	return grepOf(flags, m[1], operands), true
}

func perlSearch(args []string) (hint.Invocation, bool) {
	var operands []string
	program, numbered := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" || program != "" {
			operands = append(operands, a)
			continue
		}
		cluster := a[1:]
		for j := 0; j < len(cluster); j++ {
			switch cluster[j] {
			case 'n':
				numbered = true
			case 'l', 'a':
			case 'e':
				if rest := cluster[j+1:]; rest != "" {
					program = rest
				} else if i+1 < len(args) {
					i++
					program = args[i]
				}
				j = len(cluster)
			default:
				return hint.Invocation{}, false
			}
		}
	}
	if !numbered || program == "" {
		return hint.Invocation{}, false
	}
	m := perlPrintIfRe.FindStringSubmatch(program)
	if m == nil {
		m = perlMatchPrintRe.FindStringSubmatch(program)
	}
	if m == nil {
		return hint.Invocation{}, false
	}
	flags := []string{"-P"}
	if m[2] == "i" {
		flags = append(flags, "-i")
	}
	return grepOf(flags, m[1], operands), true
}
