package hint

// This file is the single source of truth for magus command paths that appear
// inside user-facing OUTPUT: hints, error messages, and examples that point
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
// a path nothing renders buys nothing though (it drifts just as quietly, with no
// output depending on it), so declare one when an emitter starts using it.

import (
	"path/filepath"
	"strings"
	"sync/atomic"
)

// invokedName is the binary as THIS process was invoked, which is what a copyable
// hint has to spell. A bare "magus" reaches whatever the reader's PATH holds, and on
// a machine carrying an older release that is a binary which cannot answer for this
// workspace.
//
// "magus" until main says otherwise, so a library caller and every test render the
// canonical form.
var invokedName atomic.Pointer[string]

// SetInvokedName records how this process was invoked, from os.Args[0]. A relative
// spelling is kept whole, since `./magus` names a checkout's own binary and a base
// name would not; anything else renders as its base name.
//
// A binary called something OTHER than magus is ignored, and the canonical name
// stands. A renamed copy is usually a harness's own artifact rather than anything a
// reader could type: the docs' example generator builds one as `magus-bin`, and a
// hint spelling that is a hint nobody can run.
//
// Call it once before any hint renders: every Command reads it.
func SetInvokedName(argv0 string) {
	if name, ok := invokedSpelling(argv0); ok {
		invokedName.Store(&name)
	}
}

// invokedSpelling is SetInvokedName's decision, split out so it can be graded
// without moving the package's global.
func invokedSpelling(argv0 string) (string, bool) {
	name := strings.TrimSpace(argv0)
	if name == "" || filepath.Base(name) != "magus" {
		return "", false
	}
	if strings.HasPrefix(name, ".") {
		return name, true
	}
	return filepath.Base(name), true
}

// binary is the name Command renders with.
func binary() string {
	if p := invokedName.Load(); p != nil {
		return *p
	}
	return "magus"
}

// Command is a canonical magus command path (the tokens after "magus"). Values
// are declared once below; call sites render them with String or With.
//
// Where a parent command routes to a subcommand by positionally matching a token
// (for example `query` matching "output" to reach `query output`), the
// dispatcher should compare against Leaf rather than a bare string literal, so
// the accepted form and the printed hint share one source of truth, the exact
// drift that shipped the wrong ref hint.
type Command struct {
	tokens []string
}

func cmd(tokens ...string) Command { return Command{tokens: tokens} }

// String renders the bare invocation, e.g. "magus query output", spelling the binary
// as SetInvokedName reported it.
func (c Command) String() string { return binary() + " " + strings.Join(c.tokens, " ") }

// With renders the invocation followed by trailing args, e.g.
// QueryOutput.With(ref, "--open") => "magus query output <ref> --open".
func (c Command) With(args ...string) string {
	if len(args) == 0 {
		return c.String()
	}
	return c.String() + " " + strings.Join(args, " ")
}

// Argv renders the invocation as an argument vector, args unquoted: the form a caller
// execs rather than pastes. String is the shell spelling of the same command, so an
// argument needing quotes differs between the two.
func (c Command) Argv(args ...string) []string {
	argv := make([]string, 0, 1+len(c.tokens)+len(args))
	argv = append(argv, binary())
	argv = append(argv, c.tokens...)
	return append(argv, args...)
}

// Head is the top-level subcommand token (e.g. "query" for "query output"), the
// one cmd/magus's dispatchSub switches on. The drift test asserts it is real.
func (c Command) Head() string { return c.tokens[0] }

// Leaf is the last token of the path (e.g. "output" for "query output"), the
// positional a parent command matches to route here. Compare against this in a
// dispatcher instead of a bare literal to keep it tied to the hint.
func (c Command) Leaf() string { return c.tokens[len(c.tokens)-1] }

// Canonical commands referenced from user-facing output. Register every new one
// in AllCommands so the drift test walks it.
var (
	Run               = cmd("run")
	Query             = cmd("query")
	QueryOutput       = cmd("query", "output")
	QueryInvocation   = cmd("query", "invocation")
	GraphExport       = cmd("graph", "export")
	GraphStats        = cmd("graph", "stats")
	GraphBuild        = cmd("graph", "build")
	GraphDiff         = cmd("graph", "diff")
	ServerStart       = cmd("server", "start")
	ServerStop        = cmd("server", "stop")
	ServerJob         = cmd("server", "job")
	ServerReload      = cmd("server", "reload")
	Status            = cmd("status")
	Watch             = cmd("watch")
	Affected          = cmd("affected")
	Describe          = cmd("describe")
	DescribeTargets   = cmd("describe", "targets")
	DescribeTarget    = cmd("describe", "target")
	DescribeProject   = cmd("describe", "project")
	DescribeFile      = cmd("describe", "file")
	DescribeGraph     = cmd("describe", "graph")
	DescribeMCPTools  = cmd("describe", "mcp-tools")
	Explain           = cmd("explain")
	Path              = cmd("path")
	Diff              = cmd("diff")
	Init              = cmd("init")
	Doctor            = cmd("doctor")
	Where             = cmd("where")
	X                 = cmd("x")
	Ls                = cmd("ls")
	LsTargets         = cmd("ls", "targets")
	Refs              = cmd("refs")
	MemoryLs          = cmd("memory", "ls")
	MemoryPut         = cmd("memory", "put")
	MemoryVerify      = cmd("memory", "verify")
	Ledger            = cmd("ledger")
	LedgerBrief       = cmd("ledger", "brief")
	LedgerAccept      = cmd("ledger", "accept")
	NotesLs           = cmd("notes", "ls")
	NotesGet          = cmd("notes", "get")
	NotesEdit         = cmd("notes", "edit")
	Session           = cmd("session")
	SessionLoad       = cmd("session", "load")
	SessionShow       = cmd("session", "show")
	SessionLease      = cmd("session", "lease")
	SessionAttention  = cmd("session", "attention")
	SessionDispose    = cmd("session", "dispose")
	SessionNotify     = cmd("session", "notify")
	SessionCheckpoint = cmd("session", "checkpoint")
	VCSAdd            = cmd("vcs", "add")
	VCSResolve        = cmd("vcs", "resolve")
	VCSCheckpoint     = cmd("vcs", "checkpoint")
	AgentInstall      = cmd("agent", "install")
	AgentSample       = cmd("agent", "sample")
	ConfigView        = cmd("config", "view")
	ConfigToken       = cmd("config", "token")
	ConfigTokenPrint  = cmd("config", "token", "print")
	MCPTokenGenerate  = cmd("config", "token", "generate")

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
// test to walk. Keep new Command values registered here:
// TestAllDeclaredAreRegistered reads this file and fails if a declaration is
// missing, which is how ServerReload sat outside the guard while serverCmd
// routed on it.
var AllCommands = []Command{
	Run, Query, QueryOutput, QueryInvocation, GraphExport, GraphStats, GraphBuild,
	GraphDiff, ServerStart, ServerStop, ServerJob, ServerReload, Status, Watch, Affected,
	Describe, DescribeTargets, DescribeTarget, DescribeProject, DescribeFile, DescribeGraph,
	DescribeMCPTools, Explain, Path, Diff, Init, Doctor, Where, X, Ls, LsTargets, Refs,
	MemoryLs, MemoryPut, MemoryVerify, Ledger, LedgerBrief, LedgerAccept, NotesLs, NotesGet, NotesEdit,
	Session, SessionLoad, SessionShow, SessionLease, SessionAttention, SessionCheckpoint, SessionDispose, SessionNotify,
	VCSAdd, VCSResolve, VCSCheckpoint, AgentInstall, AgentSample,
	ConfigView, ConfigToken, ConfigTokenPrint, MCPTokenGenerate,
	ConfigConsoleToken, ConfigConsoleTokenCreate, ConfigConsoleTokenRevoke,
	ConfigMCPConnectorCreate, ConfigMCPConnectorLs, ConfigMCPConnectorRevoke,
	SelfUpdate, SelfRefresh,
}
