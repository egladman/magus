package hint

// This file is the single source of truth for magus command paths that appear
// inside user-facing OUTPUT - hints, error messages, and examples that point
// the reader at another command to run.
//
// Hardcoding these strings let them drift from the real command surface: a
// failing target once printed "magus query <ref>" long after the command had
// become "magus query output <ref>". An emitter that renders from a Command value
// here survives a subcommand rename as a single edit, and cmd/magus's drift test
// asserts every registered head token still resolves to a real subcommand.
//
// Not every emitter does yet: usage blocks and doc tables still carry the path as a
// literal, and those are the ones that can silently go stale. A Command declared for
// a path nothing renders buys nothing though - it drifts just as quietly, with no
// output depending on it - so declare one when an emitter starts using it.

import "strings"

// Command is a canonical magus command path (the tokens after "magus"). Values
// are declared once below; call sites render them with String or With.
//
// Where a parent command routes to a subcommand by positionally matching a token
// (for example `query` matching "output" to reach `query output`), the
// dispatcher should compare against Leaf rather than a bare string literal, so
// the accepted form and the printed hint share one source of truth - the exact
// drift that shipped the wrong ref hint.
type Command struct {
	tokens []string
}

func cmd(tokens ...string) Command { return Command{tokens: tokens} }

// String renders the bare invocation, e.g. "magus query output".
func (c Command) String() string { return "magus " + strings.Join(c.tokens, " ") }

// With renders the invocation followed by trailing args, e.g.
// QueryOutput.With(ref, "--open") => "magus query output <ref> --open".
func (c Command) With(args ...string) string {
	if len(args) == 0 {
		return c.String()
	}
	return c.String() + " " + strings.Join(args, " ")
}

// Head is the top-level subcommand token (e.g. "query" for "query output"), the
// one cmd/magus's dispatchSub switches on. The drift test asserts it is real.
func (c Command) Head() string { return c.tokens[0] }

// Leaf is the last token of the path (e.g. "output" for "query output") - the
// positional a parent command matches to route here. Compare against this in a
// dispatcher instead of a bare literal to keep it tied to the hint.
func (c Command) Leaf() string { return c.tokens[len(c.tokens)-1] }

// Canonical commands referenced from user-facing output. Register every new one
// in AllCommands so the drift test walks it.
var (
	Run              = cmd("run")
	Query            = cmd("query")
	QueryOutput      = cmd("query", "output")
	QueryInvocation  = cmd("query", "invocation")
	GraphExport      = cmd("graph", "export")
	GraphStats       = cmd("graph", "stats")
	GraphBuild       = cmd("graph", "build")
	GraphDiff        = cmd("graph", "diff")
	ServerStart      = cmd("server", "start")
	ServerStop       = cmd("server", "stop")
	ServerJob        = cmd("server", "job")
	ServerReload     = cmd("server", "reload")
	Status           = cmd("status")
	Watch            = cmd("watch")
	Affected         = cmd("affected")
	Describe         = cmd("describe")
	DescribeTargets  = cmd("describe", "targets")
	DescribeTarget   = cmd("describe", "target")
	DescribeProject  = cmd("describe", "project")
	DescribeFile     = cmd("describe", "file")
	DescribeGraph    = cmd("describe", "graph")
	DescribeMCPTools = cmd("describe", "mcp-tools")
	Explain          = cmd("explain")
	Path             = cmd("path")
	Diff             = cmd("diff")
	Init             = cmd("init")
	Doctor           = cmd("doctor")
	Where            = cmd("where")
	X                = cmd("x")
	Ls               = cmd("ls")
	LsTargets        = cmd("ls", "targets")
	Refs             = cmd("refs")
	MemoryLs         = cmd("memory", "ls")
	MemoryPut        = cmd("memory", "put")
	MemoryVerify     = cmd("memory", "verify")
	NotesLs          = cmd("notes", "ls")
	NotesGet         = cmd("notes", "get")
	NotesEdit        = cmd("notes", "edit")
	SessionAttention = cmd("session", "attention")
	SessionDispose   = cmd("session", "dispose")
	SessionNotify    = cmd("session", "notify")
	VCSAdd           = cmd("vcs", "add")
	VCSResolve       = cmd("vcs", "resolve")
	VCSCheckpoint    = cmd("vcs", "checkpoint")
	AgentInstall     = cmd("agent", "install")
	AgentSample      = cmd("agent", "sample")
	ConfigView       = cmd("config", "view")
	ConfigToken      = cmd("config", "token")
	ConfigTokenPrint = cmd("config", "token", "print")
	MCPTokenGenerate = cmd("config", "token", "generate")

	ConfigConsoleToken       = cmd("config", "console", "token")
	ConfigConsoleTokenCreate = cmd("config", "console", "token", "create")
	ConfigConsoleTokenRevoke = cmd("config", "console", "token", "revoke")
	ConfigMCPConnectorCreate = cmd("config", "mcp", "connector", "create")
	ConfigMCPConnectorLs     = cmd("config", "mcp", "connector", "ls")
	ConfigMCPConnectorRevoke = cmd("config", "mcp", "connector", "revoke")

	SelfUpdate  = cmd("self", "update")
	SelfRefresh = cmd("self", "refresh")
)

// AllCommands is every canonical command referenced in output, for the drift
// test to walk. Keep new Command values registered here -
// TestAllDeclaredAreRegistered reads this file and fails if a declaration is
// missing, which is how ServerReload sat outside the guard while serverCmd
// routed on it.
var AllCommands = []Command{
	Run, Query, QueryOutput, QueryInvocation, GraphExport, GraphStats, GraphBuild,
	GraphDiff, ServerStart, ServerStop, ServerJob, ServerReload, Status, Watch, Affected,
	Describe, DescribeTargets, DescribeTarget, DescribeProject, DescribeFile, DescribeGraph,
	DescribeMCPTools, Explain, Path, Diff, Init, Doctor, Where, X, Ls, LsTargets, Refs,
	MemoryLs, MemoryPut, MemoryVerify, NotesLs, NotesGet, NotesEdit,
	SessionAttention, SessionDispose, SessionNotify,
	VCSAdd, VCSResolve, VCSCheckpoint, AgentInstall, AgentSample,
	ConfigView, ConfigToken, ConfigTokenPrint, MCPTokenGenerate,
	ConfigConsoleToken, ConfigConsoleTokenCreate, ConfigConsoleTokenRevoke,
	ConfigMCPConnectorCreate, ConfigMCPConnectorLs, ConfigMCPConnectorRevoke,
	SelfUpdate, SelfRefresh,
}
