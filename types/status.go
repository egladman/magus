package types

import "time"

// StatusBase holds the static portions of a StatusReport: telemetry, cache, and
// build-flag fields. It is populated by cmd/magus (which has access to the
// build-tag constants selfUpdateCompiled and mcpIsCompiled) and injected into
// webbridge.Options so the bridge can assemble a full StatusReport without
// importing cmd/magus.
type StatusBase struct {
	Telemetry TelemetryStatus
	Cache     CacheStatus
	Build     BuildStatus
}

// StatusReport is the canonical JSON/YAML shape returned by `magus status -o json`.
// It is also served verbatim by GET /api/v1/status on the browser bridge so both
// consumers share one definition. Fields are exported so pkg types can be read from
// internal packages without importing cmd/magus.
type StatusReport struct {
	Telemetry TelemetryStatus `json:"telemetry" yaml:"telemetry"`
	Cache     CacheStatus     `json:"cache" yaml:"cache"`
	Build     BuildStatus     `json:"build" yaml:"build"`
	Pool      *StatusOutput   `json:"pool,omitempty" yaml:"pool,omitempty"`
	PoolError string          `json:"pool_error,omitempty" yaml:"pool_error,omitempty"` // reason Pool is absent
}

// BuildStatus reports optional features compiled into the magus binary via build tags.
// Populated by the caller so the bridge (internal/webbridge) does not need to import
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
	Capacity      int               `json:"capacity" yaml:"capacity"`
	InUse         int               `json:"in_use" yaml:"in_use"`
	Waiting       int               `json:"waiting" yaml:"waiting"`
	Calls         []StatusCall      `json:"calls,omitempty" yaml:"calls,omitempty"`
	Workspaces    []StatusWorkspace `json:"workspaces,omitempty" yaml:"workspaces,omitempty"`
}

// StatusCall describes one in-flight call in the pool.
type StatusCall struct {
	Args      []string  `json:"args" yaml:"args"`
	Workspace string    `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	SubOp     string    `json:"sub_op,omitempty" yaml:"sub_op,omitempty"`
}

// StatusWorkspace describes one workspace currently loaded by the daemon.
type StatusWorkspace struct {
	Root       string    `json:"root" yaml:"root"`
	LoadedAt   time.Time `json:"loaded_at" yaml:"loaded_at"`
	LastAccess time.Time `json:"last_access" yaml:"last_access"`
}
