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
const (
	StatusOK   CheckStatus = "ok"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
)

// FixStatus is the outcome of an auto-remediation attempt.
type FixStatus string

// The FixStatus constants enumerate the possible auto-remediation outcomes.
const (
	FixFixed   FixStatus = "fixed"
	FixSkipped FixStatus = "skipped"
	FixFailed  FixStatus = "failed"
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
	Warn int `json:"warn" yaml:"warn"`
	Fail int `json:"fail" yaml:"fail"`
}

// FixResult records the result of an auto-remediation attempt.
type FixResult struct {
	Check  string    `json:"check" yaml:"check"`
	Status FixStatus `json:"status" yaml:"status"`
	Detail string    `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// Report is the full doctor output.
type Report struct {
	Workspace string      `json:"workspace" yaml:"workspace"`
	Checks    []Check     `json:"checks" yaml:"checks"`
	Summary   Summary     `json:"summary" yaml:"summary"`
	Fixes     []FixResult `json:"fixes,omitempty" yaml:"fixes,omitempty"`
}

// MCPStatus carries MCP availability information for the doctor check.
type MCPStatus struct {
	Compiled bool   // binary was built with -tags mcp
	Enabled  bool   // mcp.enabled is true (or unset → defaults true)
	Address  string // configured or default listen address
	DaemonUp bool   // a running daemon was detected
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
	// Capacity / InUse / Waiting mirror the pool snapshot.
	Capacity int
	InUse    int
	Waiting  int
	// Workspaces lists workspace roots currently loaded by the daemon.
	Workspaces []LoadedWorkspace
	// SockDir is the directory scanned for socket files.
	SockDir string
}

// LoadedWorkspace describes one workspace slot in the daemon.
type LoadedWorkspace struct {
	Root       string
	LoadedAt   time.Time
	LastAccess time.Time
}

type options struct {
	cfg        config.Config
	version    string
	commit     string
	fix        bool
	mcpStatus  *MCPStatus
	daemonInfo *DaemonInfo
}

// Option configures a [Run] call.
type Option func(*options)

// WithConfig sets the resolved workspace config.
func WithConfig(c config.Config) Option { return func(o *options) { o.cfg = c } }

// WithVersion sets the ldflags-injected version string (e.g. "v0.4.2").
func WithVersion(v string) Option { return func(o *options) { o.version = v } }

// WithCommit sets the ldflags-injected commit hash (e.g. "abc1234" or "abc1234-dirty").
func WithCommit(c string) Option { return func(o *options) { o.commit = c } }

// WithFix enables auto-remediation of fixable checks.
func WithFix(v bool) Option { return func(o *options) { o.fix = v } }


// WithMCPStatus passes MCP availability info to the doctor check.
func WithMCPStatus(s MCPStatus) Option { return func(o *options) { o.mcpStatus = &s } }

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
		r.checkBinarySigned(), r.checkBinaryTree(), r.checkJSONCodec(), r.checkMCPServer(),
		r.checkDaemonReachability(), r.checkStaleSockets(),
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
		r.checkReflinkSupport(),
		r.checkLanguageCoverage(projects),
		r.checkCITarget(projects),
		r.checkMagusfileSyntax(projects),
		r.checkSpellDocs(project.DefaultSpellRegistry().All()),
		r.checkExplicitVCS(),
		r.checkWatchBackend(),
		r.checkGraphCycles(),
		r.checkSymlinks(),
		r.checkEnvVars(),
		r.checkMagefileCoexistence(projects),
		r.checkLegacyMageForms(projects),
		r.checkMixedMageforms(projects),
		r.checkVariadicMageTargets(projects),
		r.checkTargetNameConventions(projects),
		r.checkCharmTargetCollision(projects),
		r.checkShellCompletion(),
		r.checkVCSBaseRef(),
		r.checkMergeDriver(),
		r.checkConcurrencyBudget(),
		r.checkWorkspaceRegistration(),
	)

	// Dependency health checks (warn-only). Build the graph once; skip if broken
	// (checkGraphCycles already reported the cycle).
	if healthG, err := r.ws.Graph(); err == nil {
		out.Checks = append(
			out.Checks,
			r.checkNearCycles(healthG),
			r.checkFanOut(projects),
			r.checkBlastRadius(healthG, projects),
			r.checkDependencyTangle(healthG),
		)
	}

	// Apply auto-fixes before tallying so re-run results are reflected.
	if r.opts.fix {
		out.Fixes = r.applyFixes(projects)
		// Re-scan projects from disk so the re-check reflects any renames/rewrites.
		postFixProjects := r.ws.All()
		// Re-run fixable checks so the summary reflects the post-fix state.
		for i, c := range out.Checks {
			switch c.Name {
			case "legacy mage forms":
				out.Checks[i] = r.checkLegacyMageForms(postFixProjects)
			case "consistent mage forms":
				out.Checks[i] = r.checkMixedMageforms(postFixProjects)
			}
		}
	}

	for _, c := range out.Checks {
		switch c.Status {
		case StatusOK:
			out.Summary.OK++
		case StatusWarn:
			out.Summary.Warn++
		case StatusFail:
			out.Summary.Fail++
		}
	}
	return out
}
