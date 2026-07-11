package types

import "time"

// StatusBase holds the static portions of a StatusReport: telemetry, cache, and
// build-flag fields. It is populated by cmd/magus (which has access to the
// selfUpdateCompiled build-tag constant) and injected into
// console.NewService so the bridge can assemble a full StatusReport without
// importing cmd/magus.
type StatusBase struct {
	Telemetry TelemetryStatus
	Cache     CacheStatus
	Build     BuildStatus
}

// StatusReport is the canonical JSON/YAML shape returned by `magus status -o json`.
// It is also served verbatim by GET /api/v1/status on the console so both
// consumers share one definition. Fields are exported so pkg types can be read from
// internal packages without importing cmd/magus.
type StatusReport struct {
	Telemetry TelemetryStatus `json:"telemetry" yaml:"telemetry"`
	Cache     CacheStatus     `json:"cache" yaml:"cache"`
	Build     BuildStatus     `json:"build" yaml:"build"`
	Pool      *StatusOutput   `json:"pool,omitempty" yaml:"pool,omitempty"`
	PoolError string          `json:"pool_error,omitempty" yaml:"pool_error,omitempty"` // reason Pool is absent
	// Runs are the invocations the daemon is executing right now (adopted
	// dispatches), each with its per-target execution state. Empty when nothing is
	// running or when reported by a process that is not the daemon.
	Runs []StatusRun `json:"runs,omitempty" yaml:"runs,omitempty"`
}

// TargetRunState is where a target sits in its lifecycle within a run. Values match the
// magus.status.v1.TargetRun.State enum names (lowercased) so the JSON and the wire agree.
type TargetRunState string

const (
	TargetRunQueued  TargetRunState = "queued"
	TargetRunRunning TargetRunState = "running"
	TargetRunPassed  TargetRunState = "passed"
	TargetRunFailed  TargetRunState = "failed"
	TargetRunCached  TargetRunState = "cached"
)

// StatusRun is one in-flight invocation the daemon has adopted, keyed by its invocation id,
// carrying the per-target execution state a dashboard renders as a live run.
type StatusRun struct {
	Inv       string            `json:"inv" yaml:"inv"`
	Trigger   string            `json:"trigger,omitempty" yaml:"trigger,omitempty"`
	StartedAt time.Time         `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	Targets   []StatusTargetRun `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// StatusTargetRun is the execution state of one target within a StatusRun.
type StatusTargetRun struct {
	Project    string         `json:"project,omitempty" yaml:"project,omitempty"`
	Target     string         `json:"target,omitempty" yaml:"target,omitempty"`
	State      TargetRunState `json:"state" yaml:"state"`
	StartedAt  time.Time      `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	EndedAt    time.Time      `json:"ended_at,omitempty" yaml:"ended_at,omitempty"`
	OutputRef  string         `json:"output_ref,omitempty" yaml:"output_ref,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
}

// BuildStatus reports optional features compiled into the magus binary via build tags.
// Populated by the caller so the bridge (internal/service/console) does not need to import
// the build-tag constants from cmd/magus.
type BuildStatus struct {
	SelfUpdate bool `json:"selfupdate" yaml:"selfupdate"`
	MCP        bool `json:"mcp" yaml:"mcp"`
}

// TelemetryStatus reports the current telemetry configuration.
type TelemetryStatus struct {
	Enabled     bool    `json:"enabled" yaml:"enabled"`
	Endpoint    string  `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Protocol    string  `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	Insecure    bool    `json:"insecure,omitempty" yaml:"insecure,omitempty"`
	ServiceName string  `json:"service_name,omitempty" yaml:"service_name,omitempty"`
	SampleRatio float64 `json:"sample_ratio,omitempty" yaml:"sample_ratio,omitempty"`
	Note        string  `json:"note,omitempty" yaml:"note,omitempty"`
}

// CacheStatus reports the current cache configuration.
type CacheStatus struct {
	Immutable bool   `json:"immutable" yaml:"immutable"`
	Dir       string `json:"dir,omitempty" yaml:"dir,omitempty"`
	SizeMB    int    `json:"size_mb,omitempty" yaml:"size_mb,omitempty"`
}

// StatusOutput is the public shape of the live concurrency pool reported by `magus status`.
type StatusOutput struct {
	ParentPID     int               `json:"parent_pid" yaml:"parent_pid"`
	DaemonVersion string            `json:"daemon_version,omitempty" yaml:"daemon_version,omitempty"`
	Mode          string            `json:"mode,omitempty" yaml:"mode,omitempty"` // "daemon", "proc", or ""
	Capacity       int                   `json:"capacity" yaml:"capacity"`
	Running        int                   `json:"running" yaml:"running"`
	Queued         int                   `json:"queued" yaml:"queued"`
	RunningTargets []StatusRunningTarget `json:"running_targets,omitempty" yaml:"running_targets,omitempty"`
	Workspaces     []StatusWorkspace     `json:"workspaces,omitempty" yaml:"workspaces,omitempty"`
	Affected       []string              `json:"affected,omitempty" yaml:"affected,omitempty"`
}

// StatusRunningTarget describes one running target in the pool.
type StatusRunningTarget struct {
	Args      []string  `json:"args" yaml:"args"`
	Workspace string    `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	Step      string    `json:"step,omitempty" yaml:"step,omitempty"`
	Inv       string    `json:"inv,omitempty" yaml:"inv,omitempty"` // invocation id; deep-links to this running target's live log
}

// StatusWorkspace describes one workspace currently loaded by the daemon.
type StatusWorkspace struct {
	Root       string    `json:"root" yaml:"root"`
	LoadedAt   time.Time `json:"loaded_at" yaml:"loaded_at"`
	LastAccess time.Time `json:"last_access" yaml:"last_access"`
	// Live cache activity for this workspace (daemon mode; zero otherwise).
	CacheHit   int   `json:"cache_hit,omitempty" yaml:"cache_hit,omitempty"`
	CacheMiss  int   `json:"cache_miss,omitempty" yaml:"cache_miss,omitempty"`
	CacheError int   `json:"cache_error,omitempty" yaml:"cache_error,omitempty"`
	CacheBytes int64 `json:"cache_bytes,omitempty" yaml:"cache_bytes,omitempty"`
}
