package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/hint"
)

// MCP calls: how a call to one of magus's own tools becomes a line the command
// rules read, and how those rules read it back. Writer and reader in one file, because a
// rendering and a parse that disagree is a rule judging something nobody sent.

// writePathsParam is the one list a bound caller may shrink.
const writePathsParam = "write_paths"

// mcpElidedParams render as a presence marker instead of their value. No rule reads this
// one, and criteria are free prose: the rendered line is recorded in the activity trail, so
// copying it there would put a caller's sentences into an audit record shaped like a
// command. Presence is all the rebind rule needs, since naming it at all is a rewrite.
var mcpElidedParams = map[string]bool{"criteria": true}

// mcpElidedValue stands in for an elided value. A word rather than an empty string: an
// empty value is how the merge spells an explicit clear, and the two must not render alike.
const mcpElidedValue = "..."

// unreadValue stands in for a value the script computes. It matches no job id, parent or
// declaration, so every rule reading it takes the refusing branch.
const unreadValue = "%unread%"

// Ops of a rendered job line, named for the magus\job member the script called.
const (
	jobOpPut      = "put"
	jobOpRegister = "register"
	jobOpClear    = "clear"
	// jobOpUnread is a script the guard could not read at all, which may call anything.
	jobOpUnread = "unread"
)

// mcpCLIEquivalent is the CLI command a magus MCP tool is the other door to.
type mcpCLIEquivalent struct {
	command hint.Command
	// operands are the tool parameters that render as positional arguments, in order.
	operands []string
}

// mcpCLIEquivalents route each magus tool to the command line that does the same thing, so
// the rules already written for the CLI judge the tool call rather than a second copy of
// them being written for MCP. The client tool is absent: what it does is its script, which
// clientCall reads.
var mcpCLIEquivalents = map[hint.ToolName]mcpCLIEquivalent{
	hint.ToolDiff:   {command: hint.Diff},
	hint.ToolStatus: {command: hint.Status},
	hint.ToolConfig: {command: hint.ConfigView},
	hint.ToolBuzz:   {command: hint.Buzz, operands: []string{"path"}},
}

// mcpReadOnlyTools say, per magus tool, whether one call only reads. It decides what a
// binary that cannot load the tree still lets through: the line a call renders as names
// the CLI verb it stands for, and a tool is not always that verb (the buzz tool is a pure
// transform where `magus buzz` runs a script). A tool absent here is not judged read-only,
// and TestEveryMCPToolIsClassifiedForAStaleBinary fails until a new one is added.
var mcpReadOnlyTools = map[hint.ToolName]func(input map[string]any) bool{
	hint.ToolStatus:  func(map[string]any) bool { return true },
	hint.ToolConfig:  func(map[string]any) bool { return true },
	hint.ToolConsole: func(map[string]any) bool { return true },
	hint.ToolBuzz:    func(map[string]any) bool { return true },
	// The ops that write into the person's review session are not reads; the handler
	// defaults an op that is absent or not text to state.
	hint.ToolDiff: func(input map[string]any) bool {
		op, ok := input["op"].(string)
		return !ok || strings.TrimSpace(op) == "state"
	},
	// A script can do anything the client host module can.
	hint.ToolClient: func(map[string]any) bool { return false },
}

// mcpReadsOnly reports a call to the magus tool name that only reads.
func mcpReadsOnly(name string, input map[string]any) bool {
	reads, ok := mcpReadOnlyTools[hint.ToolName(name)]
	return ok && reads(input)
}

// buildCall is the command line a magus tool call is judged as. The trail
// records this same string, so a later audit reads what was graded.
func buildCall(name string, input map[string]any, cwd string) string {
	if name == hint.ToolClient.String() {
		return clientCall(input, cwd)
	}
	cli, ok := mcpCLIEquivalents[hint.ToolName(name)]
	if !ok {
		return name
	}
	out := []string{cli.command.String()}
	for _, key := range cli.operands {
		if value := valueString(input[key]); value != "" {
			out = append(out, quoteValue(value))
		}
	}
	return strings.Join(out, " ")
}

// clientCall renders what a client script does that a rule judges, one line per
// call: a gate run as `magus run ci`, a nested `magus job ...` as that command, and
// each magus\job write as `client op=<member> id=<id> <key>=<value>...`. A script
// with none of these renders as the tool's bare name.
//
// The script is parsed, not matched: a comment naming --plan, a `"CI"` or a
// variable holding the argv read as what they are. What the parse cannot settle
// reads as the refusing answer: a computed argv as the gate, a computed id or value
// as unreadValue, and a script that does not parse, or a path that cannot be read,
// as both the gate and an unread job call. A relative path resolves against cwd,
// the process's own when empty.
func clientCall(input map[string]any, cwd string) string {
	name := hint.ToolClient.String()
	script := valueString(input["script"])
	if script == "" {
		path := valueString(input["path"])
		if path == "" {
			return name
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return unreadClientCall()
		}
		script = string(data)
	}
	lines, ok := clientScriptLines(script)
	if !ok {
		return unreadClientCall()
	}
	if len(lines) == 0 {
		return name
	}
	return strings.Join(lines, "\n")
}

func unreadClientCall() string {
	return "magus run ci\n" + hint.ToolClient.String() + " op=" + jobOpUnread
}

// clientScriptLines walks a client script for the magus calls a rule judges. ok is
// false when the script does not parse.
func clientScriptLines(script string) ([]string, bool) {
	prog, err := buzz.ParseEmbedded(script)
	if err != nil || prog == nil {
		return nil, false
	}
	r := clientReader{bound: map[string]bool{}, only: map[string]bool{}}
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok || imp.Path != "magus" {
			continue
		}
		switch {
		case len(imp.Only) > 0:
			for _, n := range imp.Only {
				r.only[n] = true
			}
		case imp.Alias == "_":
			r.flat = true
		case imp.Alias != "":
			r.bound[imp.Alias] = true
		default:
			r.bound["magus"] = true
		}
	}
	for _, stmt := range prog.Stmts {
		ast.Inspect(stmt, r.visit)
	}
	return r.lines, true
}

// clientReader collects the judged calls of one script. bound are the names the
// magus module is imported as; flat and only are its unprefixed members.
type clientReader struct {
	bound map[string]bool
	only  map[string]bool
	flat  bool
	lines []string
}

// judgedMembers are the magus members whose calls a rule reads. Any of them reached
// other than by a direct call, stored or passed as a value, could be called with
// anything, so that use reads as an unread script.
var judgedMembers = map[string]bool{"run": true, "cmd": true, "job": true}

func (r *clientReader) visit(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.CallExpr:
		if member, ok := r.magusMember(e.Callee); ok {
			r.call(member, e)
			for _, a := range e.Args {
				ast.Inspect(a, r.visit)
			}
			return false
		}
	case *ast.MemberExpr, *ast.IdentExpr:
		member, ok := r.magusMember(e)
		if !ok {
			return true
		}
		if len(member) == 0 || judgedMembers[member[0]] {
			r.lines = append(r.lines, strings.Split(unreadClientCall(), "\n")...)
		}
		return false
	}
	return true
}

// magusMember is the path below the magus module that n names (["job", "put"] for
// magus\job.put), and ok false when n names nothing in it. An empty path is the
// module itself. A namespace object's member takes a dot, so a dot is read only as
// the last link, after a backslash; magus\job\put, which the checker refuses, still
// reads as the same path so a script spelling it is judged rather than missed.
func (r *clientReader) magusMember(n ast.Node) ([]string, bool) {
	var path []string
	for {
		switch e := n.(type) {
		case *ast.MemberExpr:
			if !e.Namespaced && (len(path) > 0 || !r.namesObject(e.Object)) {
				return nil, false
			}
			path = append([]string{e.Name}, path...)
			n = e.Object
			continue
		case *ast.IdentExpr:
			if r.bound[e.Name] {
				return path, true
			}
			if r.flat || r.only[e.Name] {
				return append([]string{e.Name}, path...), true
			}
		}
		return nil, false
	}
}

// namesObject reports whether n reaches a member of the magus module: magus\job, or
// job itself when the module's members are imported unprefixed.
func (r *clientReader) namesObject(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.MemberExpr:
		return e.Namespaced
	case *ast.IdentExpr:
		return r.flat || r.only[e.Name]
	}
	return false
}

// call renders one call to a magus member.
func (r *clientReader) call(member []string, c *ast.CallExpr) {
	switch strings.Join(member, `\`) {
	case "run":
		argv, ok := literalStrings(clientArg(c, 0, "args"))
		r.magusLine(append([]string{"run"}, argv...), ok)
	case "cmd":
		sub, subOK := literalString(clientArg(c, 0, "sub"))
		argv, ok := literalStrings(clientArg(c, 1, "args"))
		r.magusLine(append([]string{sub}, argv...), subOK && ok)
	case `job\put`:
		r.jobLine(jobOpPut, c, clientArg(c, 1, "opts"))
	case `job\register`:
		r.jobLine(jobOpRegister, c, nil)
	case `job\clear`:
		r.lines = append(r.lines, hint.ToolClient.String()+" op="+jobOpClear)
	case `job\wait`:
		id, _ := literalString(clientArg(c, 0, "id"))
		r.lines = append(r.lines, hint.JobWait.WithAs("magus", quoteValue(orUnread(id))))
	case "job":
		r.lines = append(r.lines, strings.Split(unreadClientCall(), "\n")...)
	}
}

// magusLine renders a nested magus invocation a rule reads: the gate, and anything
// under `magus job`, which the rebind rule judges. An argv the script computes may be
// the gate, so it renders as one.
func (r *clientReader) magusLine(argv []string, literal bool) {
	switch {
	case !literal:
		r.lines = append(r.lines, "magus run ci")
	case isGateCommand(argv), len(argv) > 0 && argv[0] == hint.JobFork.Head():
		quoted := make([]string, len(argv))
		for i, a := range argv {
			quoted[i] = quoteValue(a)
		}
		r.lines = append(r.lines, "magus "+strings.Join(quoted, " "))
	}
}

// jobLine renders a magus\job write as its op, its id and every key opts names.
// Every key, not a list of the ones a rule reads: a key the rendering drops reaches
// the row with no rule having seen it.
func (r *clientReader) jobLine(op string, c *ast.CallExpr, opts ast.Node) {
	id, _ := literalString(clientArg(c, 0, "id"))
	out := []string{hint.ToolClient.String(), "op=" + op, "id=" + quoteValue(orUnread(id))}
	if opts != nil {
		m, ok := opts.(*ast.MapExpr)
		if !ok {
			out = append(out, "opts="+unreadValue)
		} else {
			fields := make([]string, 0, len(m.Keys))
			for i, k := range m.Keys {
				key, ok := mapKey(k)
				if !ok {
					fields = append(fields, "opts="+unreadValue)
					continue
				}
				if mcpElidedParams[key] {
					fields = append(fields, key+"="+mcpElidedValue)
					continue
				}
				value, ok := literalValue(m.Values[i])
				if !ok {
					value = unreadValue
				}
				fields = append(fields, key+"="+quoteValue(value))
			}
			slices.Sort(fields)
			out = append(out, fields...)
		}
	}
	r.lines = append(r.lines, strings.Join(out, " "))
}

func orUnread(s string) string {
	if s == "" {
		return unreadValue
	}
	return s
}

// clientArg is a call's argument at position i, or the one labeled name.
func clientArg(c *ast.CallExpr, i int, name string) ast.Node {
	for j, label := range c.ArgNames {
		if label == name && j < len(c.Args) {
			return c.Args[j]
		}
	}
	if i < len(c.Args) && (i >= len(c.ArgNames) || c.ArgNames[i] == "") {
		return c.Args[i]
	}
	return nil
}

func mapKey(n ast.Node) (string, bool) {
	if id, ok := n.(*ast.IdentExpr); ok {
		return id.Name, true
	}
	return literalString(n)
}

func literalString(n ast.Node) (string, bool) {
	switch e := n.(type) {
	case *ast.StringLit:
		return e.Val, true
	case *ast.InterpExpr:
		var b strings.Builder
		for _, p := range e.Parts {
			if p.Expr != nil {
				return "", false
			}
			b.WriteString(p.Lit)
		}
		return b.String(), true
	}
	return "", false
}

// literalStrings reads a list literal of string literals. An absent argv is empty.
func literalStrings(n ast.Node) ([]string, bool) {
	if n == nil {
		return nil, true
	}
	l, ok := n.(*ast.ListExpr)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l.Items))
	for _, it := range l.Items {
		s, ok := literalString(it)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// literalValue flattens an opts value the way valueString flattens a tool parameter.
func literalValue(n ast.Node) (string, bool) {
	switch e := n.(type) {
	case *ast.BoolLit:
		return strconv.FormatBool(e.Val), true
	case *ast.IntLit:
		return strconv.FormatInt(e.Val, 10), true
	case *ast.ListExpr:
		items, ok := literalStrings(e)
		return strings.Join(items, ","), ok
	}
	return literalString(n)
}

// valueString flattens one parameter. A list arrives as a comma-separated
// string or an array, and both spell the same line.
func valueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, valueString(item))
		}
		return strings.Join(parts, ",")
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// quoteValue keeps a value with whitespace as one word.
func quoteValue(value string) string {
	if strings.ContainsAny(value, " \t\n\"'\\") {
		return strconv.Quote(value)
	}
	return value
}

// mcpMagusPrefix is how a host spells a call to magus's own MCP server. The prefix is the
// host's, the name after it is magus's.
const mcpMagusPrefix = "mcp__magus__"

// magusToolCall returns the magus MCP tool a host's tool name refers to, or "" when it
// refers to none.
//
// Only the server-qualified name. magus's tool names are plain words (status, diff,
// client), so a bare name is as likely another server's tool as magus's, and a suffix
// match let any other server's tool decode as a magus call, be rendered into magus's
// activity trail, and be judged by magus's rules.
func magusToolCall(toolName string) string {
	name, ok := strings.CutPrefix(toolName, mcpMagusPrefix)
	if !ok {
		return ""
	}
	if slices.ContainsFunc(hint.AllToolNames, func(t hint.ToolName) bool { return t.String() == name }) {
		return name
	}
	return ""
}

// mcpParams reads back the parameters clientCall wrote for a job write:
// `client op=<op> <key>=<value>...`.
func mcpParams(args []string) map[string]string {
	params := make(map[string]string, len(args))
	for _, a := range args {
		if key, value, ok := strings.Cut(a, "="); ok {
			params[key] = value
		}
	}
	return params
}
