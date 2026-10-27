package hint

// This file is the single source of truth for magus command paths that appear
// inside user-facing OUTPUT: hints, error messages, and examples that point
// the reader at another command to run.
//
// Hardcoding these strings let them drift from the real commands: a
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
	"fmt"
	"slices"
	"strings"
)

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

func command(tokens ...string) Command { return Command{tokens: tokens} }

// String renders the bare invocation, e.g. "magus query output". The binary is
// spelled as this process was invoked (see BinaryName), so a hint copied out of a
// worktree runs the binary that printed it.
func (c Command) String() string { return c.StringAs(BinaryName()) }

// With renders the invocation followed by trailing args, e.g.
// QueryOutput.With(ref, "--open") => "magus query output <ref> --open".
func (c Command) With(args ...string) string { return c.WithAs(BinaryName(), args...) }

// StringAs renders the bare invocation spelled with bin instead of this process's
// own invocation. For a renderer whose reader is not this process (a doc committed
// to the repo, generated once and read by whoever checks it out), the live
// BinaryName would make the file's content depend on how it happened to be built.
func (c Command) StringAs(bin string) string {
	return fmt.Sprintf("%s %s", bin, strings.Join(c.tokens, " "))
}

// WithAs is StringAs followed by trailing args.
func (c Command) WithAs(bin string, args ...string) string {
	if len(args) == 0 {
		return c.StringAs(bin)
	}
	return fmt.Sprintf("%s %s", c.StringAs(bin), strings.Join(args, " "))
}

// Argv renders the invocation as an argument vector, args unquoted: the form a caller
// execs rather than pastes. String is the shell spelling of the same command, so an
// argument needing quotes differs between the two. argv[0] is [BinaryName], so it
// follows how this process was invoked.
func (c Command) Argv(args ...string) []string {
	argv := make([]string, 0, 1+len(c.tokens)+len(args))
	argv = append(argv, BinaryName())
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

// MatchedBy reports whether words, the bare subcommand words of a magus argv, begin with
// this command's path. Words past the path are operands and do not stop a match.
func (c Command) MatchedBy(words []string) bool {
	return len(words) >= len(c.tokens) && slices.Equal(words[:len(c.tokens)], c.tokens)
}

// Canonical commands referenced from user-facing output. Register every new one
// in AllCommands so the drift test walks it.
var (
	Run                 = command("run")
	Query               = command("query")
	QueryOutput         = command("query", "output")
	QueryInvocation     = command("query", "invocation")
	GraphExport         = command("graph", "export")
	GraphStats          = command("graph", "stats")
	GraphBuild          = command("graph", "build")
	GraphDiff           = command("graph", "diff")
	ServerStart         = command("server", "start")
	ServerStop          = command("server", "stop")
	ServerStatus        = command("server", "status")
	ServerReload        = command("server", "reload")
	BrokerStatus        = command("broker", "status")
	BrokerStop          = command("broker", "stop")
	BrokerUnits         = command("broker", "units")
	Status              = command("status")
	Watch               = command("watch")
	Affected            = command("affected")
	Describe            = command("describe")
	DescribeTargets     = command("describe", "targets")
	DescribeTarget      = command("describe", "target")
	DescribeProject     = command("describe", "project")
	DescribeFile        = command("describe", "file")
	DescribeGraph       = command("describe", "graph")
	DescribeMCPTools    = command("describe", "mcp-tools")
	DescribeRule        = command("describe", "rule")
	DescribeRules       = command("describe", "rules")
	DescribeHarness     = command("describe", "harness")
	Explain             = command("explain")
	Path                = command("path")
	Diff                = command("diff")
	Init                = command("init")
	Clean               = command("clean")
	Doctor              = command("doctor")
	Where               = command("where")
	Buzz                = command("buzz")
	X                   = command("x")
	Ls                  = command("ls")
	LsTargets           = command("ls", "targets")
	Refs                = command("refs")
	Shell               = command("shell")
	LsJobs              = command("ls", "jobs")
	DescribeJob         = command("describe", "job")
	JobFork             = command("job", "fork")
	JobExec             = command("job", "exec")
	JobExit             = command("job", "exit")
	JobApply            = command("job", "apply")
	JobWait             = command("job", "wait")
	JobWatch            = command("job", "watch")
	JobRun              = command("job", "run")
	JobRm               = command("job", "rm")
	NotesLs             = command("notes", "ls")
	NotesGet            = command("notes", "get")
	NotesEdit           = command("notes", "edit")
	Session             = command("session")
	SessionLoad         = command("session", "load")
	SessionShow         = command("session", "show")
	SessionAttention    = command("session", "attention")
	SessionDispose      = command("session", "dispose")
	SessionNotify       = command("session", "notify")
	SessionCheckpoint   = command("session", "checkpoint")
	VCSLsFiles          = command("vcs", "ls-files")
	VCSResolve          = command("vcs", "resolve")
	VCSCheckpoint       = command("vcs", "checkpoint")
	AgentInstall        = command("agent", "install")
	AgentStarter        = command("agent", "starter")
	AgentHarnessInstall = command("agent", "harness", "install")
	AgentHarnessVerify  = command("agent", "harness", "verify")
	ConfigView          = command("config", "view")
	ConfigSet           = command("config", "set")
	ConfigToken         = command("config", "token")
	ConfigTokenPrint    = command("config", "token", "print")
	MCPTokenGenerate    = command("config", "token", "generate")

	ConfigConsoleToken       = command("config", "console", "token")
	ConfigConsoleTokenCreate = command("config", "console", "token", "create")
	ConfigConsoleTokenRevoke = command("config", "console", "token", "revoke")
	ConfigMCPConnectorCreate = command("config", "mcp", "connector", "create")
	ConfigMCPConnectorLs     = command("config", "mcp", "connector", "ls")
	ConfigMCPConnectorRevoke = command("config", "mcp", "connector", "revoke")

	SpellPull = command("spell", "pull")
	GraphDeps = command("graph", "deps")

	SelfUpdate = command("self", "update")
	Version    = command("version")
)

// Lookup resolves a space-separated verb path ("agent improve") to the canonical
// Command, reporting false for a path nobody declared.
//
// For GENERATED output whose prose names a command: a skill body, a docs page,
// a starter block. Retyping the path there is how it goes stale, because nothing
// connects the sentence to the CLI that moved. Resolving it means a rename either
// updates the prose or fails the lookup, and the caller decides which.
//
// The reader of such output is usually another process on another machine, so
// render the result with StringAs(DefaultBinaryName) rather than String(): this
// process may have been invoked as ./magus, and that path means nothing there.
func Lookup(path string) (Command, bool) {
	for _, c := range AllCommands {
		if strings.Join(c.tokens, " ") == path {
			return c, true
		}
	}
	return Command{}, false
}

// AllCommands is every canonical command referenced in output, for the drift
// test to walk. Keep new Command values registered here:
// TestAllDeclaredAreRegistered reads this file and fails if a declaration is
// missing, which is how ServerReload sat outside the guard while serverCommand
// routed on it.
var AllCommands = []Command{
	Run, Query, QueryOutput, QueryInvocation, GraphExport, GraphStats, GraphBuild,
	GraphDiff, ServerStart, ServerStop, ServerStatus, ServerReload, BrokerStatus, BrokerStop, BrokerUnits, Status, Watch, Affected,
	Describe, DescribeTargets, DescribeTarget, DescribeProject, DescribeFile, DescribeGraph,
	DescribeMCPTools, DescribeJob, DescribeRule, DescribeRules, DescribeHarness, Explain, Path, Diff, Init, Clean, Doctor, Where, Buzz, X, Ls, LsTargets, LsJobs, Refs, Shell,
	JobFork, JobApply, JobExec, JobExit, JobWait, JobWatch, JobRun, JobRm, NotesLs, NotesGet, NotesEdit,
	Session, SessionLoad, SessionShow, SessionAttention, SessionCheckpoint, SessionDispose, SessionNotify,
	VCSLsFiles, VCSResolve, VCSCheckpoint, AgentInstall, AgentStarter,
	AgentHarnessInstall, AgentHarnessVerify,
	ConfigView, ConfigSet, ConfigToken, ConfigTokenPrint, MCPTokenGenerate,
	ConfigConsoleToken, ConfigConsoleTokenCreate, ConfigConsoleTokenRevoke,
	ConfigMCPConnectorCreate, ConfigMCPConnectorLs, ConfigMCPConnectorRevoke,
	SpellPull, GraphDeps, SelfUpdate, Version,
}
