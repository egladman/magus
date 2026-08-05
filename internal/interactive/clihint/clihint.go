// Package clihint is the single source of truth for magus command paths that
// appear inside user-facing OUTPUT - hints, error messages, and examples that
// point the reader at another command to run.
//
// Hardcoding these strings let them drift from the real command surface: a
// failing target once printed "magus query <ref>" long after the command had
// become "magus query output <ref>". Every emitter now renders from a Command
// value here, so a subcommand rename is a single edit, and cmd/magus's drift
// test asserts every referenced head token still resolves to a real subcommand.
//
// It is nested under internal/interactive - the hints home (interactive.Emit and
// the "did you mean" suggester) - since these command references exist to be shown
// in hints; keeping it here rather than a top-level package co-locates the two.
// It does NOT import its parent interactive, which pulls in project/codec and would
// reintroduce a cycle through the low-level cache handler (which already depends on
// this package). It does import types for RefMatchCommand below - types is itself a
// low-dependency leaf (spells, libs/diagnostics only) that does not reach back to
// this package or the cache handler, so that stays cycle-free too.
package clihint

import (
	"strings"

	"github.com/egladman/magus/types"
)

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
// in All so the drift test walks it.
var (
	Run              = cmd("run")
	QueryOutput      = cmd("query", "output")
	GraphOpen        = cmd("graph", "open")
	GraphExport      = cmd("graph", "export")
	GraphStats       = cmd("graph", "stats")
	GraphBuild       = cmd("graph", "build")
	ServerStart      = cmd("server", "start")
	ServerStop       = cmd("server", "stop")
	ServerJob        = cmd("server", "job")
	Status           = cmd("status")
	Watch            = cmd("watch")
	Affected         = cmd("affected")
	DescribeTargets  = cmd("describe", "targets")
	DescribeProject  = cmd("describe", "project")
	Ls               = cmd("ls")
	LsTargets        = cmd("ls", "targets")
	Where            = cmd("where")
	MCPTokenGenerate = cmd("config", "mcp", "token", "generate")
)

// All is every canonical command referenced in output, for the drift test to
// walk. Keep new Command values registered here.
var All = []Command{
	Run, QueryOutput, GraphOpen, GraphExport, GraphStats, GraphBuild,
	ServerStart, ServerStop, ServerJob, Status, Watch, Affected,
	DescribeTargets, DescribeProject, Ls, LsTargets, Where, MCPTokenGenerate,
}

// RefMatchCommand renders a types.RefMatch as the "magus run" invocation that would
// key it: the target name (with a :charm1,charm2 suffix when the match required
// explicit charms), the project (omitted for the workspace root "."), and
// --no-default-charms when the match required the bare CI variant while
// defaultCharms - the workspace's own configured default_charms - is non-empty, since
// the workspace's defaults would otherwise apply and mint a different key.
//
// Shared by cmd/magus/query.go's ref-lookup suggestion and internal/handler/mcp's
// magus_output not-found fallback, so the CLI and the MCP surface render the exact
// same reproduce command instead of two copies that can drift.
func RefMatchCommand(mt types.RefMatch, defaultCharms []string) string {
	target := mt.Target
	if len(mt.Charms) > 0 {
		target += ":" + strings.Join(mt.Charms, ",")
	}
	args := []string{target}
	if mt.Project != "." {
		args = append(args, mt.Project)
	}
	if len(mt.Charms) == 0 && len(defaultCharms) > 0 {
		args = append(args, "--no-default-charms")
	}
	return Run.With(args...)
}
