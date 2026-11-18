package recorder

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/types/feedback"
)

// FeedbackWindow selects one session's observations: the events inside [Since, Until]
// whose Session matches. An empty Session selects the session with the newest agent
// observation in the window, which at the end of a session is the one asking.
type FeedbackWindow struct {
	Session string
	Since   time.Time
	Until   time.Time
}

// FeedbackBases is every trail directory a session in this repository may have recorded
// into: each checkout's cache dir whose events file changed at or after since.
//
// A hook records into the cache dir of the checkout it ran from, which for a session that
// spawns workers into worktrees is the session's own checkout, not the worker's. Reading
// only the caller's checkout therefore misses most sessions. The mtime filter keeps the
// cost to the trails that could hold an event in the window; a repository with hundreds of
// worktrees stats each events file once.
//
// cacheRel is the cache dir relative to a checkout root (".magus" by default); an absolute
// one names a cache every checkout shares, and is the only base.
func FeedbackBases(checkouts []string, cacheRel string, since time.Time) []string {
	if filepath.IsAbs(cacheRel) {
		return []string{cacheRel}
	}
	var out []string
	for _, root := range checkouts {
		base := filepath.Join(root, cacheRel)
		fi, err := os.Stat(eventsPath(base))
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		if !slices.Contains(out, base) {
			out = append(out, base)
		}
	}
	slices.Sort(out)
	return out
}

// ReadFeedback folds the trails at bases into one session's guard record. A trail that
// cannot be read contributes nothing; an event whose blobs are gone is skipped, since the
// trail rotates blobs out with the events that cite them. A request blob that is there and
// does not decode is the error.
//
// Checkouts on the result names the bases that held at least one event of the session.
func ReadFeedback(ctx context.Context, bases []string, w FeedbackWindow) (feedback.Report, error) {
	since, until := w.Since.UnixMilli(), w.Until.UnixMilli()
	out := feedback.Report{Start: since, End: until, Session: w.Session}
	type sourced struct {
		base string
		e    journal.Call
	}
	var events []sourced
	var newest int64
	for _, base := range bases {
		read, err := eventsInWindow(ctx, base, since, until)
		if err != nil {
			return out, err
		}
		for _, e := range read {
			if e.Kind != journal.CallKindAgentCommand && e.Kind != journal.CallKindAgentSpawn {
				continue
			}
			events = append(events, sourced{base, e})
			if w.Session == "" && e.Session != "" && e.Time.UnixMilli() >= newest {
				newest, out.Session = e.Time.UnixMilli(), e.Session
			}
		}
	}
	if out.Session == "" {
		return out, nil
	}
	slices.SortStableFunc(events, func(a, b sourced) int { return a.e.Time.Compare(b.e.Time) })

	for _, s := range events {
		e := s.e
		if e.Session != out.Session {
			continue
		}
		if out.Host == "" {
			out.Host = e.Host
		}
		if !slices.Contains(out.Checkouts, filepath.Dir(s.base)) {
			out.Checkouts = append(out.Checkouts, filepath.Dir(s.base))
		}
		switch e.Kind {
		case journal.CallKindAgentCommand:
			obs, ok, err := feedbackObservation(s.base, e)
			if err != nil {
				return out, err
			}
			if ok {
				out.Observations = append(out.Observations, obs)
			}
		case journal.CallKindAgentSpawn:
			if e.Name == NameAgentContinue {
				continue
			}
			sp := feedback.Spawn{At: e.Time.UnixMilli(), Agent: e.Agent, Child: e.Name, Lease: e.Lease}
			if raw, err := e.Request.Read(callsDir(s.base)); err == nil {
				var req AgentSpawnRequest
				if codec.Unmarshal(raw, &req) == nil {
					sp.Model = req.DeclaredModel
				}
			}
			out.Spawns = append(out.Spawns, sp)
		}
	}
	slices.Sort(out.Checkouts)
	return out, nil
}

// feedbackObservation reads one agent-command event's blobs. It reports false for an event
// that recorded no request blob or whose blob is gone. A request blob that
// cannot be read or decoded is its error, a [*RetiredDecisionError] for one
// holding a retired guard decision.
func feedbackObservation(base string, e journal.Call) (feedback.Observation, bool, error) {
	var req AgentCommandRequest
	var resp AgentCommandResponse
	var retired *RetiredDecisionError
	if e.Request.Ref == "" {
		return feedback.Observation{}, false, nil
	}
	raw, err := e.Request.Read(callsDir(base))
	if errors.Is(err, fs.ErrNotExist) {
		return feedback.Observation{}, false, nil
	}
	if err != nil {
		return feedback.Observation{}, false, err
	}
	if err := DecodeAgentBlob(base, e.Request.Ref, raw, &req); err != nil {
		return feedback.Observation{}, false, err
	}
	// A read carries no response blob: no rule judges it.
	if e.Response.Ref != "" {
		if raw, err := e.Response.Read(callsDir(base)); err == nil {
			if err := DecodeAgentBlob(base, e.Response.Ref, raw, &resp); errors.As(err, &retired) {
				return feedback.Observation{}, false, err
			}
		}
	}
	obs := feedback.Observation{
		At: e.Time.UnixMilli(), Agent: e.Agent, Lease: e.Lease, Tool: req.Tool,
		Command: req.Command, Path: req.Path,
		Decision: string(resp.Decision), Rule: resp.Rule, PreauthorizedBy: resp.PreauthorizedBy,
		Nexts: servedNexts(fmt.Sprintf("%s\n%s", resp.Reason, resp.Context)),
	}
	if req.Command != "" {
		obs.Shape = CommandShape(req.Command)
		obs.Shapes = CommandShapes(req.Command)
	}
	return obs, true, nil
}

// eventsInWindow decodes the events of the trail at base whose Ts falls in [since, until],
// oldest first. A missing trail is no events.
func eventsInWindow(ctx context.Context, base string, since, until int64) ([]journal.Call, error) {
	f, err := os.Open(eventsPath(base))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []journal.Call
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e journal.Call
		if e.UnmarshalJSON(line) != nil || e.Time.UnixMilli() < since || e.Time.UnixMilli() > until {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// servedNexts reads the commands a verdict's text served under its `next:` block: each
// line indented two spaces after a line reading "next:", until the first line that is
// neither one of those nor a deeper-indented explanation.
//
// The block is the guard's rendering of Verdict.Next, the one place the trail keeps it.
func servedNexts(text string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(line) == "next:":
			in = true
		case !in:
		case strings.HasPrefix(line, "    "):
			// an explanation under the command above it
		case strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "":
			out = append(out, strings.TrimSpace(line))
		default:
			in = false
		}
	}
	return out
}

// CommandShape is a shell line with what it names normalized away. Calls
// that differ only in paths and literals read alike: `grep -rn foo src` is
// `grep -rn <arg>`. It keeps programs, flags, operators and redirections,
// and is "" for a line that does not parse. A wrapper stays the program.
func CommandShape(command string) string {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return ""
	}
	return stmtsShape(f.Stmts)
}

// CommandShapes is the shape of each program a shell line runs, in order,
// each with its own redirections. `cd x && grep -rn foo src` is `cd <path>`
// and `grep -rn <arg>`. Programs inside loops and substitutions count. Nil
// for a line that does not parse. Unguarded calls cluster by these.
func CommandShapes(command string) []string {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok {
			if call, ok := s.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 {
				out = append(out, stmtShape(s))
			}
		}
		return true
	})
	return out
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
		return fmt.Sprintf("%s %s %s", stmtShape(c.X), c.Op.String(), stmtShape(c.Y))
	case *syntax.Subshell:
		return fmt.Sprintf("( %s )", stmtsShape(c.Stmts))
	case *syntax.Block:
		return fmt.Sprintf("{ %s }", stmtsShape(c.Stmts))
	case *syntax.IfClause:
		return fmt.Sprintf("if %s ; then %s ; fi", stmtsShape(c.Cond), stmtsShape(c.Then))
	case *syntax.WhileClause:
		kw := "while"
		if c.Until {
			kw = "until"
		}
		return fmt.Sprintf("%s %s ; do %s ; done", kw, stmtsShape(c.Cond), stmtsShape(c.Do))
	case *syntax.ForClause:
		return fmt.Sprintf("for <loop> ; do %s ; done", stmtsShape(c.Do))
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

// subcommandDepth is how many leading bare words name a subcommand, for
// programs whose first word picks what they do. Every other program's
// operands normalize from the first, so `grep foo src` and `grep bar lib`
// share a shape.
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
		// An assignment's value is where a credential sits: never shown.
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
		// Letters stay in order: one dash may be a long flag (go's -run), not a
		// bundle of short ones, and only the program knows which.
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

// literal is the text the shell passes for w. It is false when part of it
// is known only at run time: a variable, a substitution, an expansion.
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
