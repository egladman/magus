// Package doctor validates a magus workspace and reports health checks.
package doctor

import (
	"fmt"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/schema"
	"github.com/egladman/magus/types"
)

// CheckStatus is the outcome of a single doctor check.
type CheckStatus string

// The CheckStatus constants enumerate the possible doctor-check outcomes.
//
// StatusSkip exists because a check whose precondition is unmet verified nothing,
// and reporting that as ok made "0 fail" mean two different things: everything was
// examined and was healthy, or half of it was never looked at. A skip is not a
// failure - the workspace is not broken for having no daemon running - so it does
// not affect the exit code; it only stops a vacuous check from being counted as
// evidence.
const (
	StatusOK   CheckStatus = "ok"
	StatusFail CheckStatus = "fail"
	StatusSkip CheckStatus = "skip"
)

// Check is one doctor check result.
type Check struct {
	Name    string      `json:"name" yaml:"name"`
	Status  CheckStatus `json:"status" yaml:"status"`
	Message string      `json:"message,omitempty" yaml:"message,omitempty"`
	Details []string    `json:"details,omitempty" yaml:"details,omitempty"`
}

// Summary counts check outcomes.
type Summary struct {
	OK   int `json:"ok" yaml:"ok"`
	Fail int `json:"fail" yaml:"fail"`
	Skip int `json:"skip" yaml:"skip"`
}

// Report is the full doctor output.
type Report struct {
	Workspace string  `json:"workspace" yaml:"workspace"`
	Checks    []Check `json:"checks" yaml:"checks"`
	Summary   Summary `json:"summary" yaml:"summary"`
}

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
}

// Option configures a [Run] call.
type Option func(*options)

// WithConfig sets the resolved workspace config.
func WithConfig(c config.Config) Option { return func(o *options) { o.cfg = c } }

// WithDaemonInfo passes live daemon state for the daemon-related checks.
// Pass a nil-pointer-equivalent (empty DaemonInfo with Reachable=false) when
// the daemon is not running; this is not an error.
func WithDaemonInfo(d DaemonInfo) Option { return func(o *options) { o.daemonInfo = &d } }

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
}

// Run executes all doctor checks. ws may be nil when workspace loading failed;
// wsErr carries the reason and is recorded as a failed "workspace" check.
// Doctor only inspects in-memory workspace state, so it takes the narrow
// WorkspaceReader role rather than the full repository.
func Run(root string, ws types.WorkspaceReader, wsErr error, optFns ...Option) Report {
	var o options
	for _, fn := range optFns {
		fn(&o)
	}
	r := &runner{opts: o, root: root, ws: ws}
	return r.run(wsErr)
}

func (r *runner) run(wsErr error) Report {
	var out Report
	out.Checks = append(
		out.Checks,
		r.checkJSONCodec(), r.checkStaleSockets(), r.checkMCPTokens(),
	)

	if wsErr != nil {
		out.Checks = append(out.Checks, Check{
			Name:    "workspace",
			Status:  StatusFail,
			Message: wsErr.Error(),
		})
		return summarize(out)
	}

	projects := r.ws.All()
	out.Workspace = r.ws.Root()
	out.Checks = append(out.Checks, Check{
		Name:    "workspace",
		Status:  StatusOK,
		Message: fmt.Sprintf("%d projects discovered", len(projects)),
	})

	out.Checks = append(
		out.Checks,
		r.checkConfigFile(),
		r.checkCacheWritable(),
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
		r.checkStaleWorktrees(),
		r.checkEnvVars(),
		r.checkTargetNameConventions(projects),
		r.checkBespokePhaseFragmentTargets(projects),
		r.checkSpellProbeCoverage(projects),
		r.checkUnreachedFootprintDecls(projects),
		r.checkRedundantFootprintGlobs(projects),
		r.checkDeadOutputGlobs(projects),
		r.checkOutputOwnedByTwoTargets(projects),
		r.checkSelfStalingOutputs(projects),
		r.checkCharmTargetCollision(projects),
		r.checkHasCharmTypos(projects),
		r.checkStaleShadowAcks(),
		r.checkVCSBaseRef(),
		r.checkWorkspaceRegistration(),
		r.checkBridgeReachability(),
	)

	return summarize(out)
}

// summarize counts every appended check into out.Summary. Both of run's exits go
// through it because the early return for a workspace that failed to load used to
// count only its own failure: the three pre-workspace checks were rendered but
// never summed, so a genuinely failing one (stale sockets) vanished from the
// totals and anything reading report.summary rather than report.checks got a
// count that did not match what it was shown.
func summarize(out Report) Report {
	out.Summary = Summary{}
	for _, c := range out.Checks {
		switch c.Status {
		case StatusOK:
			out.Summary.OK++
		case StatusFail:
			out.Summary.Fail++
		case StatusSkip:
			out.Summary.Skip++
		}
	}
	return out
}
