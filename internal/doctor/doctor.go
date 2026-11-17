// Package doctor validates a magus workspace and reports health checks.
package doctor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

// ServerInfo carries live server state for the server-related doctor checks.
// A nil ServerInfo means no server was found or queried.
type ServerInfo struct {
	// Reachable is true when the server was successfully dialed.
	Reachable bool
	// SockAddr is the resolved socket address (for display in check details).
	SockAddr string
	// ParentPID is the server's OS process ID.
	ParentPID int
	// ServerVersion is the version string reported by the server, and ClientVersion the
	// version of the binary asking. They are compared rather than displayed: a server of
	// another vintage answers every call with a decoder that does not know this build's
	// fields, and writes back what it read.
	ServerVersion string
	ClientVersion string
	// Capacity / Running / Queued mirror the pool snapshot.
	Capacity int
	Running  int
	Queued   int
	// Workspaces lists workspace roots currently loaded by the server.
	Workspaces []loadedWorkspace
	// SockDir is the directory scanned for socket files.
	SockDir string
	// ServerSocket and BrokerSocket are the file names `magus server` and the broker
	// listen on inside SockDir, so the sockets check reports each by its role. Either
	// may be empty, and that socket is then scanned like any other.
	ServerSocket string
	BrokerSocket string
	// MCPAddr is the host:port the MCP server listens on, for bridge reachability checks.
	MCPAddr string
	// BridgeEnabled is true when the bridge is not explicitly disabled in config.
	BridgeEnabled bool
	// MCPEnabled is true when the MCP server is not explicitly disabled in config. The
	// bridge is mounted on that server, so a false here means no bridge is served no
	// matter what the server is doing.
	MCPEnabled bool
	// Persistent is true when the process answering on the socket is `magus server`
	// rather than the per-process proc server any command may spin up.
	// Only the server starts the MCP HTTP server, so this (and not Reachable)
	// is what says a bridge is expected.
	Persistent bool
}

// loadedWorkspace describes one workspace slot in the server.
type loadedWorkspace struct {
	Root       string
	LoadedAt   time.Time
	LastAccess time.Time
}

type options struct {
	cfg          config.Config
	serverInfo   *ServerInfo
	probe        bool
	skills       *agent.Catalog
	explanations *Explanations
	graphNodes   func(context.Context) ([]types.KnowledgeNode, error)
}

// Option configures a [Run] call.
type Option func(*options)

// WithConfig sets the resolved workspace config.
func WithConfig(c config.Config) Option { return func(o *options) { o.cfg = c } }

// WithServerInfo passes live server state for the server-related checks.
// Pass an empty ServerInfo (Reachable false) when the server is not running;
// that is not an error.
func WithServerInfo(s ServerInfo) Option { return func(o *options) { o.serverInfo = &s } }

// WithSkillCatalog supplies the agent skill catalog so the installed copies can be graded
// against this binary. The sources are embedded in the CLI, so a caller without them (the
// SDK, this package's tests) passes nothing and the check reports itself skipped.
func WithSkillCatalog(c *agent.Catalog) Option { return func(o *options) { o.skills = c } }

// WithExplanations supplies what the workspace's notes store covers, so the hotspot check
// can ask whether the hardest-worked code is explained anywhere.
//
// A caller that cannot resolve the store passes nothing, and the check reports itself
// unknown rather than reporting the code unexplained. Those are different claims: the
// notes store is resolved by the CLI, so the SDK and this package's tests have no way to
// tell an empty store from an unreadable one.
func WithExplanations(e Explanations) Option { return func(o *options) { o.explanations = &e } }

// WithGraphNodes supplies the knowledge graph's nodes for the graph-bounds check. Building
// the graph takes the full Inspector role, which Run's WorkspaceReader does not carry, so
// the caller that holds one builds it. A caller that passes nothing gets the check
// reported as skipped. load runs only when that check does.
func WithGraphNodes(load func(context.Context) ([]types.KnowledgeNode, error)) Option {
	return func(o *options) { o.graphNodes = load }
}

// WithProbe RUNS each declared readiness probe rather than only listing it.
//
// Off by default, and that default is the design: doctor answers questions about the
// workspace, so forking `docker info` to render a report would make a read-only command
// depend on a daemon being up, the exact coupling readiness exists to make legible.
// Opting in is for the case that wants it, checking an environment before a long run
// instead of finding out eight minutes in.
func WithProbe() Option { return func(o *options) { o.probe = true } }

type runner struct {
	opts options
	root string
	ws   types.WorkspaceReader
	// wsErr is the workspace load error, held on the runner so checkWorkspace can be a
	// registry entry like every other check rather than a special case in the loop.
	wsErr error
}

// Run executes all doctor checks. ws may be nil when workspace loading failed;
// wsErr carries the reason and is recorded as a failed "workspace" check.
// Doctor only inspects in-memory workspace state, so it takes the narrow
// WorkspaceReader role rather than the full repository.
//
// ctx bounds the checks that do I/O. Doctor is not pure inspection: it resolves a VCS
// base ref through a git subprocess, dials the server socket, GETs the console
// endpoint, and walks the whole tree twice. Those took context.Background(), so the MCP
// handler had to discard the caller's context and an agent could not cancel a run that
// blocks for seconds on a hung socket.
func Run(ctx context.Context, root string, ws types.WorkspaceReader, wsErr error, optFns ...Option) types.DoctorReport {
	var o options
	for _, fn := range optFns {
		fn(&o)
	}
	r := &runner{opts: o, root: root, ws: ws}
	return r.run(ctx, wsErr)
}

func (r *runner) run(ctx context.Context, wsErr error) types.DoctorReport {
	r.wsErr = wsErr

	var projects []*types.Project
	var out types.DoctorReport
	if wsErr == nil {
		projects = r.ws.All()
		out.Workspace = r.ws.Root()
	}

	for _, def := range allChecks {
		if def.NeedsWorkspace && wsErr != nil {
			continue
		}
		c := def.run(ctx, r, projects)
		// The registry is authoritative for the name, so a check body cannot report
		// itself under one identifier while the listing advertises another. Evidence is
		// only a DEFAULT here: a check that could not look, or looked harder than usual,
		// has already said so and that answer wins.
		c.Name = def.Name
		if c.Evidence == "" {
			c.Evidence = def.Evidence
		}
		out.Checks = append(out.Checks, c)
	}

	for _, c := range out.Checks {
		// A check that did not run is counted as unknown whatever status it carries.
		// Such a check returns check.OK, so tallying it as a pass would count
		// checks that never looked.
		if c.Evidence == check.EvidenceUnknown {
			out.Summary.Unknown++
			continue
		}
		switch c.Status {
		case check.OK:
			out.Summary.OK++
		case check.Fail:
			out.Summary.Fail++
		case check.Advice:
			out.Summary.Advice++
		}
	}
	return out
}

// checkWorkspace reports whether the magusfile loaded, and is the hinge every check
// below it in the registry hangs on: a failure here skips them rather than running them
// against a workspace that is not there.
func (r *runner) checkWorkspace(projects []*types.Project) check.Check {
	if r.wsErr != nil {
		return check.Check{Name: "workspace", Status: check.Fail, Message: r.wsErr.Error()}
	}
	return check.Check{
		Name:    "workspace",
		Status:  check.OK,
		Message: fmt.Sprintf("%d projects discovered", len(projects)),
	}
}

// RenderChecks writes `magus doctor --list`: what magus would check, without
// checking it.
//
// The name is printed alone on its line so the identifier is what a reader copies, and
// the subject sits underneath rather than beside it: a name column wide enough for
// "stale-spell-shadow-acknowledgments" pushes every subject past a terminal's width.
func RenderChecks(w io.Writer, checks []CheckInfo) {
	fmt.Fprintf(w, "%d checks, in the order `magus doctor` reports them:\n\n", len(checks))
	for _, c := range checks {
		fmt.Fprint(w, "  "+c.Name)
		var notes []string
		if c.Code != "" {
			notes = append(notes, c.Code)
		}
		if !c.NeedsWorkspace {
			notes = append(notes, "reported even when the workspace fails to load")
		}
		if len(notes) > 0 {
			fmt.Fprintf(w, "  (%s)", strings.Join(notes, "; "))
		}
		fmt.Fprintf(w, "\n      %s [%s]\n", c.Doc, c.Evidence)
	}
}

// CheckNames is the name of each check, the `magus doctor --list -o name` form.
func CheckNames(checks []CheckInfo) []string {
	names := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c.Name)
	}
	return names
}

// UnhealthyNames is the name of each check that did not pass, the `magus doctor -o name` form.
func UnhealthyNames(out types.DoctorReport) []string {
	var names []string
	for _, c := range out.Checks {
		if c.Status != check.OK {
			names = append(names, c.Name)
		}
	}
	return names
}

// RenderReport writes the text form of a doctor report to w.
func RenderReport(w io.Writer, out types.DoctorReport) {
	// Doctor's report stays on stdout (it is the command's primary output, meant to be
	// piped/grepped) but shares the cache's colored [pass]/[fail] status glyphs so the
	// whole tool reads consistently. Color only when stdout is a TTY and NO_COLOR is unset.
	color := termkit.WantsColor(os.Stdout, termkit.SystemProbe)

	if out.Workspace != "" {
		fmt.Fprintf(w, "workspace: %s\n\n", out.Workspace)
	}
	for _, c := range out.Checks {
		fmt.Fprintf(w, "%s %s", checkGlyph(c, color), c.Name)
		if c.Message != "" {
			fmt.Fprintf(w, ": %s", c.Message)
		}
		// Only the evidence that qualifies the verdict is printed. Stamping "measured"
		// on the forty checks that measured something is noise, and noise is what
		// people learn to skip; "inferred" is the one that says read this twice.
		if c.Evidence == check.EvidenceInferred {
			fmt.Fprintf(w, " [%s]", c.Evidence)
		}
		fmt.Fprintln(w)
		for _, d := range c.Details {
			fmt.Fprintf(w, "    %s\n", d)
		}
	}
	fmt.Fprintf(w, "\nsummary: %d ok, %d fail", out.Summary.OK, out.Summary.Fail)
	if out.Summary.Advice > 0 {
		// Named only when there is some. A count of things that did not fail reads as
		// a scold when it is always there.
		fmt.Fprintf(w, ", %d advice", out.Summary.Advice)
	}
	if out.Summary.Unknown > 0 {
		fmt.Fprintf(w, ", %d unknown", out.Summary.Unknown)
	}
	fmt.Fprintln(w)
}

// checkGlyph renders a finding's level, except that a check which did not run gets its
// own glyph rather than the pass it nominally returned.
//
// A skipped check carries check.OK, which is true and misleading.
// Nothing was looked at. Rendering it as [pass] is how a green report came
// to mean "either fine or unexamined, and you cannot tell which from here".
func checkGlyph(c check.Check, color bool) string {
	if c.Evidence == check.EvidenceUnknown {
		if color {
			return "\x1b[36m[unknown]\x1b[0m"
		}
		return "[unknown]"
	}
	return statusGlyph(c.Status, color)
}

// statusGlyph renders a doctor check's status with the shared [pass]/[fail] glyphs,
// colored (green/red) when color is true. Mirrors the cache handler's glyphs so a
// failed check and a failed build look identical across the tool.
func statusGlyph(status check.Status, color bool) string {
	label, code := "[?]", "0"
	switch status {
	case check.OK:
		label, code = "[pass]", "32" // green
	case check.Fail:
		label, code = "[fail]", "31" // red
	case check.Advice:
		// Yellow, not red: it did not fail, and coloring it like a failure would
		// undo the whole point of the level.
		label, code = "[advice]", "33"
	}
	if color {
		return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, label)
	}
	return label
}

// serverSocketIn is the file name the server listens on inside sockDir: the configured
// server.address when it names a socket there, server.sock otherwise. A configured
// address elsewhere leaves the default, which is the one a second server would take.
func serverSocketIn(sockDir, serverAddr string) string {
	if path := strings.TrimPrefix(serverAddr, "unix://"); filepath.Dir(path) == sockDir {
		return filepath.Base(path)
	}
	return proc.ServerSocketName()
}

// ServerSource is what BuildServerInfo cannot read from the process: the invoking
// binary's version and the resolved config the server checks report on.
type ServerSource struct {
	ClientVersion string
	// Addr is where the server listens, as resolved from config.
	Addr string
	// MCPAddr is the configured MCP address, host:port.
	MCPAddr string
	// BridgeEnabled and MCPEnabled are true unless explicitly set to false.
	BridgeEnabled bool
	MCPEnabled    bool
	// ServerEnabled is server.enabled: false means this invocation is self-contained.
	ServerEnabled bool
}

// BuildServerInfo queries the running server (if any) and returns a
// ServerInfo for the doctor checks. If no server is reachable, returns an
// empty ServerInfo so checks render a sensible "no server" message.
func BuildServerInfo(ctx context.Context, src ServerSource) ServerInfo {
	sockDir := proc.SockDir()
	di := ServerInfo{
		SockDir: sockDir, ClientVersion: src.ClientVersion,
		ServerSocket: serverSocketIn(sockDir, src.Addr), BrokerSocket: broker.SocketName,
	}

	// Bridge fields come from resolved config. BridgeEnabled is true unless
	// explicitly set to false (mirrors how MCP.Enabled works).
	di.MCPAddr = src.MCPAddr
	di.BridgeEnabled = src.BridgeEnabled
	di.MCPEnabled = src.MCPEnabled

	// server.enabled=false means this invocation is self-contained, so there is nothing
	// to ask. Without this check the probe still dials whatever server the host happens
	// to be running: doctor then reports on a process the caller opted out of, and the
	// testscript suite (which sets MAGUS_SERVER_ENABLED=false precisely to stay
	// hermetic) reaches the real socket and fails wherever a server is up.
	if !src.ServerEnabled {
		return di
	}

	di.SockAddr = src.Addr

	reply, err := proc.QueryStatus(ctx, src.Addr)
	if err != nil {
		return di
	}
	di.Reachable = true
	// Only the server reports itself, and only it starts the MCP HTTP server, so only
	// it makes a bridge something to expect.
	di.Persistent = reply.Server != nil
	di.ParentPID = reply.ParentPID
	di.ServerVersion = reply.Version
	di.Capacity = reply.Capacity
	di.Running = reply.Running
	di.Queued = reply.Queued
	for _, w := range reply.Workspaces {
		if !w.Loaded() {
			continue
		}
		di.Workspaces = append(di.Workspaces, loadedWorkspace{
			Root:       w.Root,
			LoadedAt:   w.LoadedAt,
			LastAccess: w.LastAccess,
		})
	}
	return di
}

// ApplyFixes runs the remedy each non-OK finding names.
//
// It dispatches EXISTING magus subcommands, never a private repair routine, and that is
// the safety property rather than an implementation detail: --fix can only do things you
// could have typed yourself and can inspect afterwards. A finding whose remedy needs
// judgment (narrow this over-wide glob, or accept that the key is deliberately volatile?)
// declares no Fix and stays a report, which is why this can be blunt about running what
// it is given.
//
// Nothing here writes config directly either. A config-shaped remedy is spelled `config
// set ...`, so the one thing in magus that edits config stays the one thing that edits
// config.
//
// Failures are reported and do not stop the rest: the findings are independent, and a
// remedy that cannot run is a thing to say, not a reason to leave the others unapplied.
func ApplyFixes(ctx context.Context, w io.Writer, out types.DoctorReport, dryRun bool, dispatch func(ctx context.Context, sub string, args []string) error) error {
	var ran, failed int
	for _, c := range out.Checks {
		if c.Status == check.OK || len(c.Fix) == 0 {
			continue
		}
		cmdline := "magus " + strings.Join(c.Fix, " ")
		if dryRun {
			fmt.Fprintf(w, "would fix %s: %s\n", c.Name, cmdline)
			continue
		}
		fmt.Fprintf(w, "fixing %s: %s\n", c.Name, cmdline)
		if err := dispatch(ctx, c.Fix[0], c.Fix[1:]); err != nil {
			slog.ErrorContext(ctx, c.Name, attr.Notice(""), attr.Component("magus doctor --fix"), attr.Error(err))
			failed++
			continue
		}
		ran++
	}
	switch {
	case dryRun:
		return nil
	case ran == 0 && failed == 0:
		fmt.Fprintln(w, "nothing to fix: no finding named a remedy magus can run")
	case failed > 0:
		return fmt.Errorf("magus doctor --fix: %d of %d remedy/remedies failed", failed, ran+failed)
	default:
		fmt.Fprintf(w, "\napplied %d remedy/remedies; re-run `%s` to confirm\n", ran, hint.Doctor)
	}
	return nil
}
