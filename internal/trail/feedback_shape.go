package trail

import (
	"path"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// CommandShape is a shell line with what it names normalized away, so that calls which
// differ only in their paths, patterns and literals read alike: `grep -rn foo src` and
// `grep -rn bar lib` are both `grep -rn <arg>`. It keeps the programs, their flags, the
// operators between them, and the redirections; "" when the line does not parse, which
// is also a line the shell would refuse to run.
//
// It parses with the guard's parser library and is the one place feedback reads a
// command's structure, so it can move onto the guard's own wrapper-peeling parser
// without its callers changing. Until then a wrapper (timeout, env, sh -c) stays the
// program and its payload is an operand.
func CommandShape(command string) string {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return ""
	}
	return stmtsShape(f.Stmts)
}

func stmtsShape(stmts []*syntax.Stmt) string {
	parts := make([]string, 0, len(stmts))
	for _, s := range stmts {
		if shape := stmtShape(s); shape != "" {
			parts = append(parts, shape)
		}
	}
	return strings.Join(parts, " ; ")
}

func stmtShape(s *syntax.Stmt) string {
	words := []string{}
	if s.Negated {
		words = append(words, "!")
	}
	if cmd := commandShape(s.Cmd); cmd != "" {
		words = append(words, cmd)
	}
	for _, r := range s.Redirs {
		words = append(words, redirectShape(r))
	}
	if s.Background {
		words = append(words, "&")
	}
	return strings.Join(words, " ")
}

func commandShape(cmd syntax.Command) string {
	switch c := cmd.(type) {
	case nil:
		return ""
	case *syntax.CallExpr:
		return callShape(c.Args)
	case *syntax.BinaryCmd:
		return stmtShape(c.X) + " " + c.Op.String() + " " + stmtShape(c.Y)
	case *syntax.Subshell:
		return "( " + stmtsShape(c.Stmts) + " )"
	case *syntax.Block:
		return "{ " + stmtsShape(c.Stmts) + " }"
	case *syntax.IfClause:
		return "if " + stmtsShape(c.Cond) + " ; then " + stmtsShape(c.Then) + " ; fi"
	case *syntax.WhileClause:
		kw := "while"
		if c.Until {
			kw = "until"
		}
		return kw + " " + stmtsShape(c.Cond) + " ; do " + stmtsShape(c.Do) + " ; done"
	case *syntax.ForClause:
		return "for <loop> ; do " + stmtsShape(c.Do) + " ; done"
	case *syntax.TimeClause:
		return "time " + stmtShape(c.Stmt)
	case *syntax.DeclClause:
		return c.Variant.Value + " <assign>"
	case *syntax.CaseClause:
		return "case <arg> ; esac"
	case *syntax.FuncDecl:
		return "function"
	case *syntax.TestClause:
		return "[[ <test> ]]"
	case *syntax.ArithmCmd:
		return "(( <arith> ))"
	default:
		return "<compound>"
	}
}

// subcommandDepth is how many leading bare words name a subcommand rather than an
// operand, for the programs where the first word after the program picks what it does.
// Every other program's operands are normalized from the first one, which is what lets
// `grep foo src` and `grep bar lib` share a shape.
var subcommandDepth = map[string]int{
	"magus": 2, "gh": 2,
	"git": 1, "hg": 1, "sl": 1, "jj": 1, "go": 1, "npm": 1, "pnpm": 1, "yarn": 1,
	"cargo": 1, "docker": 1, "podman": 1, "kubectl": 1, "mise": 1, "brew": 1, "uv": 1,
	"buf": 1, "gcloud": 1, "aws": 1, "terraform": 1,
}

var (
	subcommandWord = regexp.MustCompile(`^[a-z][a-z0-9-]*(:[a-z0-9-]+)?$`)
	numberWord     = regexp.MustCompile(`^[0-9][0-9,.:+-]*[a-z]?$`)
	digits         = regexp.MustCompile(`[0-9]+`)
)

func callShape(args []*syntax.Word) string {
	if len(args) == 0 {
		// A bare assignment: its value is where a credential would sit, so it is never
		// rendered.
		return "<assign>"
	}
	program := "<cmd>"
	if lit, ok := literal(args[0]); ok && lit != "" {
		program = path.Base(lit)
	}
	out := []string{program}
	depth := subcommandDepth[program]
	for i, w := range args[1:] {
		lit, ok := literal(w)
		if ok && i < depth && subcommandWord.MatchString(lit) {
			out = append(out, lit)
			continue
		}
		depth = 0
		token := operandShape(lit, ok)
		if strings.HasPrefix(token, "<") && out[len(out)-1] == token {
			continue
		}
		out = append(out, token)
	}
	return strings.Join(out, " ")
}

// operandShape is the placeholder an operand reads as, or the flag it is.
func operandShape(lit string, isLiteral bool) string {
	switch {
	case !isLiteral:
		return "<arg>"
	case lit == "-" || lit == "--":
		return lit
	case strings.HasPrefix(lit, "--"):
		if name, _, found := strings.Cut(lit, "="); found {
			return name + "=<arg>"
		}
		return lit
	case strings.HasPrefix(lit, "-"):
		// Letters are kept in order: a single-dash word may be one long flag (go's
		// -run) rather than a bundle of short ones, and only the program knows which.
		return digits.ReplaceAllString(lit, "<n>")
	case numberWord.MatchString(lit):
		return "<n>"
	case strings.ContainsAny(lit, "/~") || strings.HasPrefix(lit, ".") || path.Ext(lit) != "":
		return "<path>"
	default:
		return "<arg>"
	}
}

func redirectShape(r *syntax.Redirect) string {
	op := r.Op.String()
	if r.N != nil {
		op = r.N.Value + op
	}
	if r.Hdoc != nil {
		return op + " <heredoc>"
	}
	lit, ok := literal(r.Word)
	switch {
	case ok && (lit == "/dev/null" || numberWord.MatchString(lit)):
		return op + lit
	case ok:
		return op + " <path>"
	default:
		return op + " <arg>"
	}
}

// literal is the text the shell passes for w, and false when part of it is known only at
// run time: a variable, a substitution, an arithmetic expansion.
func literal(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}
