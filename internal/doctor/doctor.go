// Package doctor validates a magus workspace and reports health checks.
package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/schema"
	"github.com/egladman/magus/types"
)

// DaemonInfo carries live daemon state for the daemon-related doctor checks.
// A nil daemon field means no daemon was found or queried.
type DaemonInfo struct {
	// Reachable is true when the daemon was successfully dialled.
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
}

// LoadedWorkspace describes one workspace slot in the daemon.
type LoadedWorkspace struct {
	Root       string
	LoadedAt   time.Time
	LastAccess time.Time
}

type options struct {
	cfg        config.Config
	daemonInfo *DaemonInfo
	probe      bool
}

// Option configures a [Run] call.
type Option func(*options)

// WithConfig sets the resolved workspace config.
func WithConfig(c config.Config) Option { return func(o *options) { o.cfg = c } }

// WithDaemonInfo passes live daemon state for the daemon-related checks.
// Pass a nil-pointer-equivalent (empty DaemonInfo with Reachable=false) when
// the daemon is not running; this is not an error.
func WithDaemonInfo(d DaemonInfo) Option { return func(o *options) { o.daemonInfo = &d } }

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
	// agent cancelled kept dialling and walking regardless.
	ctx context.Context
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
	var out types.DoctorReport
	out.Checks = append(
		out.Checks,
		r.checkJSONCodec(), r.checkStaleSockets(), r.checkMCPTokens(),
	)

	if wsErr != nil {
		out.Checks = append(out.Checks, types.DoctorCheck{
			Name:    "workspace",
			Status:  types.DoctorFail,
			Message: wsErr.Error(),
		})
		out.Summary.Fail++
		return out
	}

	projects := r.ws.All()
	out.Workspace = r.ws.Root()
	out.Checks = append(out.Checks, types.DoctorCheck{
		Name:    "workspace",
		Status:  types.DoctorOK,
		Message: fmt.Sprintf("%d projects discovered", len(projects)),
	})

	out.Checks = append(
		out.Checks,
		r.checkConfigFile(),
		r.checkCacheWritable(),
		r.checkConcurrencySizing(),
		r.checkCacheYield(projects),
		r.checkLanguageCoverage(projects),
		r.checkCITarget(projects),
		r.checkNearDuplicateServices(projects),
		r.checkStaleServiceSuppressions(projects),
		r.checkMagusfileSyntax(projects),
		r.checkSpellDocs(project.DefaultSpellRegistry().All()),
		r.checkSpellContract(),
		r.checkGraphCycles(),
		r.checkGuardBinary(),
		r.checkSymlinks(),
		r.checkGraphBounds(),
		r.checkGeneratedDrift(),
		r.checkStaleWorktrees(),
		r.checkEnvVars(),
		r.checkTargetNameConventions(projects),
		r.checkBespokePhaseFragmentTargets(projects),
		r.checkUnreachedFootprintDecls(projects),
		r.checkRedundantFootprintGlobs(projects),
		r.checkDeadOutputGlobs(projects),
		r.checkOutputOwnedByTwoTargets(projects),
		r.checkSelfStalingOutputs(projects),
		r.checkCharmTargetCollision(projects),
		r.checkHasCharmTypos(projects),
		r.checkReadinessProbes(projects),
		r.checkStaleShadowAcks(),
		r.checkVCSBaseRef(),
		r.checkWorkspaceRegistration(),
		r.checkBridgeReachability(),
	)

	for _, c := range out.Checks {
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
