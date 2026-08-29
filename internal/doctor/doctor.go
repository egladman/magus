// Package doctor validates a magus workspace and reports health checks.
package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/schema"
	"github.com/egladman/magus/types"
)

// DaemonInfo carries live daemon state for the daemon-related doctor checks.
// A nil daemon field means no daemon was found or queried.
type DaemonInfo struct {
	// Reachable is true when the daemon was successfully dialed.
	Reachable bool
	// SockAddr is the resolved socket address (for display in check details).
	SockAddr string
	// ParentPID is the daemon's OS process ID.
	ParentPID int
	// DaemonVersion is the version string reported by the daemon.
	DaemonVersion string
	// Capacity / Running / Queued mirror the pool snapshot.
	Capacity int
	Running  int
	Queued   int
	// Workspaces lists workspace roots currently loaded by the daemon.
	Workspaces []LoadedWorkspace
	// SockDir is the directory scanned for socket files.
	SockDir string
	// MCPAddr is the host:port the MCP server listens on, for bridge reachability checks.
	MCPAddr string
	// BridgeEnabled is true when the bridge is not explicitly disabled in config.
	BridgeEnabled bool
	// MCPEnabled is true when the MCP server is not explicitly disabled in config. The
	// bridge is mounted on that server, so a false here means no bridge is served no
	// matter what the daemon is doing.
	MCPEnabled bool
	// Persistent is true when the process answering on the socket is a `magus server
	// start` daemon rather than the per-process proc server any command may spin up.
	// Only the persistent one starts the MCP HTTP server, so this - and not Reachable -
	// is what says a bridge is expected.
	Persistent bool
}

// LoadedWorkspace describes one workspace slot in the daemon.
type LoadedWorkspace struct {
	Root       string
	LoadedAt   time.Time
	LastAccess time.Time
}

type options struct {
	cfg          config.Config
	daemonInfo   *DaemonInfo
	probe        bool
	skills       *agent.Catalog
	explanations *Explanations
}

// Option configures a [Run] call.
type Option func(*options)

// WithConfig sets the resolved workspace config.
func WithConfig(c config.Config) Option { return func(o *options) { o.cfg = c } }

// WithDaemonInfo passes live daemon state for the daemon-related checks.
// Pass a nil-pointer-equivalent (empty DaemonInfo with Reachable=false) when
// the daemon is not running; this is not an error.
func WithDaemonInfo(d DaemonInfo) Option { return func(o *options) { o.daemonInfo = &d } }

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

// WithProbe RUNS each declared readiness probe rather than only listing it.
//
// Off by default, and that default is the design: doctor answers questions about the
// workspace, so forking `docker info` to render a report would make a read-only command
// depend on a daemon being up - the exact coupling readiness exists to make legible.
// Opting in is for the case that wants it, checking an environment before a long run
// instead of finding out eight minutes in.
func WithProbe() Option { return func(o *options) { o.probe = true } }

// KnownEnvVars is the precomputed set of every MAGUS_* env var derived
// from the magus config struct via schema. Used to surface typos in
// checkEnvVars. No bespoke entries are accepted here — any MAGUS_* var
// that isn't in schema.Fields should be migrated onto the config struct
// so it shows up in the generated set automatically.
var KnownEnvVars = func() map[string]struct{} {
	m := make(map[string]struct{}, len(schema.Fields))
	for _, f := range schema.Fields {
		m[f.EnvVar] = struct{}{}
	}
	return m
}()

type runner struct {
	opts options
	root string
	ws   types.WorkspaceReader
	// ctx bounds the checks that touch the world: a git probe, a socket dial, an HTTP
	// GET. Each used to invent its own context.Background(), so a `magus_doctor` an
	// agent cancelled kept dialing and walking regardless.
	ctx context.Context
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
// base ref through a git subprocess, dials the daemon socket, GETs the console
// endpoint, and walks the whole tree twice. Those took context.Background(), so the MCP
// handler had to discard the caller's context and an agent could not cancel a run that
// blocks for seconds on a hung socket.
func Run(ctx context.Context, root string, ws types.WorkspaceReader, wsErr error, optFns ...Option) types.DoctorReport {
	var o options
	for _, fn := range optFns {
		fn(&o)
	}
	r := &runner{opts: o, root: root, ws: ws, ctx: ctx}
	return r.run(wsErr)
}

// runCtx is the run's context, defaulting to Background when unset. Run always supplies
// one; the fallback is for a runner built directly, which is how every check in this
// package is unit tested. Without it a zero-value runner panics inside exec.CommandContext
// rather than failing the check it was asked about.
func (r *runner) runCtx() context.Context {
	if r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

func (r *runner) run(wsErr error) types.DoctorReport {
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
		c := def.run(r, projects)
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
		// Those checks return DoctorOK because there was nothing to report, and
		// tallying that as a pass is how "44 ok" came to include checks that never
		// looked at anything.
		if c.Evidence == types.EvidenceUnknown {
			out.Summary.Unknown++
			continue
		}
		switch c.Status {
		case types.DoctorOK:
			out.Summary.OK++
		case types.DoctorFail:
			out.Summary.Fail++
		case types.DoctorAdvice:
			out.Summary.Advice++
		}
	}
	return out
}

// checkWorkspace reports whether the magusfile loaded, and is the hinge every check
// below it in the registry hangs on: a failure here skips them rather than running them
// against a workspace that is not there.
func (r *runner) checkWorkspace(projects []*types.Project) types.DoctorCheck {
	if r.wsErr != nil {
		return types.DoctorCheck{Name: "workspace", Status: types.DoctorFail, Message: r.wsErr.Error()}
	}
	return types.DoctorCheck{
		Name:    "workspace",
		Status:  types.DoctorOK,
		Message: fmt.Sprintf("%d projects discovered", len(projects)),
	}
}
