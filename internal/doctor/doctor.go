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

// The CheckStatus constants enumerate the possible doctor-check outcomes.
//
// StatusFail and StatusAdvice are a deliberate split, and which one a check
// returns is a statement about whose judgement is involved.
//
// StatusFail is for a workspace that is WRONG in a way nobody's taste can rescue:
// a dependency cycle, a magusfile that will not parse, two targets claiming one
// output, a policy naming a target that does not exist. These are facts, they
// break the build or corrupt the cache, and failing on them is not an opinion.
//
// StatusAdvice is for a convention magus RECOMMENDS: how targets are named,
// whether every project binds a language spell, whether a spell target carries a
// doc comment. These are conventions that have worked well, documented so you can
// take them - not requirements, because magus does not get to decide how your
// repository is laid out. `ci` is the one reserved target, and everything past it
// is yours.
//
// The distinction is not cosmetic. When doctor had only ok and fail, a convention
// check had two options: fail (and dictate) or not exist. What actually happened
// is that each one grew its own private escape hatch - no_language for language
// coverage, and briefly allow_bespoke_name for target naming - so the config
// surface grew one key per opinion, and taking magus's advice became mandatory
// unless you wrote a paragraph explaining yourself. Advice that exits zero needs
// no escape hatch at all.
//
// There is deliberately no switch that promotes advice to failure. A knob for
// that would just be the imposition again with an opt-in label on it, and the
// workspace that wants a convention enforced can enforce it - in its own lint
// target, with its own tools, on its own terms. magus reports what it noticed and
// gets out of the way.
const (
	StatusOK     = types.DoctorOK
	StatusFail   = types.DoctorFail
	StatusAdvice = types.DoctorAdvice
)

// The report shape is a DOMAIN type (types.DoctorReport and friends): magus.doctor
// returns it to a magusfile, so a caller iterates checks and branches on status
// instead of grepping console text. Aliased rather than moved outright so every
// check in this package keeps saying Check, Status, Report - the local vocabulary
// is the one its authors read.
type (
	CheckStatus = types.DoctorCheckStatus
	Check       = types.DoctorCheck
	Summary     = types.DoctorSummary
	Report      = types.DoctorReport
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
		out.Summary.Fail++
		return out
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
		case StatusOK:
			out.Summary.OK++
		case StatusFail:
			out.Summary.Fail++
		case StatusAdvice:
			out.Summary.Advice++
		}
	}
	return out
}
