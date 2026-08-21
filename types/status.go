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

// BuildInfo is the running binary's linker-stamped identity (version, commit, date).
// Distinct from BuildStatus, which reports cache/build activity.
type BuildInfo struct {
	Version string `json:"version" yaml:"version"`
	Commit  string `json:"commit" yaml:"commit"`
	Date    string `json:"date" yaml:"date"`
}

// Fingerprint is the full human identity, matching what `magus --version` prints.
func (b BuildInfo) Fingerprint() string {
	return "magus " + b.Version + " (" + b.Commit + ") built " + b.Date
}

// StatusReport is the canonical JSON/YAML shape returned by `magus status -o json`.
// The daemon serves the same data to the console over the typed StatusService (its live
// fields are projected onto magus.status.v1alpha1.Status) so both consumers share one definition.
// Fields are exported so pkg types can be read from internal packages without importing cmd/magus.
type StatusReport struct {
	Telemetry TelemetryStatus `json:"telemetry" yaml:"telemetry"`
	Cache     CacheStatus     `json:"cache" yaml:"cache"`
	Build     BuildStatus     `json:"build" yaml:"build"`
	// BuildInfo is the reporting binary's identity (version/commit/date), so a StatusService
	// client knows which magus it is talking to. Distinct from Build above.
	BuildInfo BuildInfo     `json:"build_info" yaml:"build_info"`
	Pool      *StatusOutput `json:"pool,omitempty" yaml:"pool,omitempty"`
	PoolError string        `json:"pool_error,omitempty" yaml:"pool_error,omitempty"` // reason Pool is absent
	// Pools is every live proc server found on this machine, one entry each, and is set
	// only when there is more than one: Pool above is the first of them, so a single
	// server would just be repeated here. Enumerating them is the point - a multi-server
	// box must still report the capacity and in-use numbers, never an error demanding
	// --socket.
	Pools []StatusOutput `json:"pools,omitempty" yaml:"pools,omitempty"`
	// Runs are the invocations the daemon is executing right now (adopted
	// dispatches), each with its per-target execution state. Empty when nothing is
	// running or when reported by a process that is not the daemon.
	Runs []StatusRun `json:"runs,omitempty" yaml:"runs,omitempty"`
	// Services are the long-running shared services the daemon is hosting right now,
	// kept warm across invocations. Empty when none are held or when reported by a
	// process that is not the daemon.
	Services []StatusService `json:"services,omitempty" yaml:"services,omitempty"`
	// ObservingSince is when this daemon began observing (its start). The telemetry and
	// cache counters above are cumulative from this instant and are NOT persisted across
	// restarts, so a dashboard can be transparent that the numbers are "since <this>", not
	// all-time. Zero (omitted) when reported by a non-daemon `magus status`.
	ObservingSince time.Time `json:"observing_since,omitempty" yaml:"observing_since,omitempty"`
	// Config surfaces the daemon's RESOLVED configuration (read-only) so a dashboard can show what
	// the daemon is set to do - the default charms it applies, the concurrency cap - without a
	// round-trip to the terminal. Additive JSON, not on the proto event wire.
	Config StatusConfig `json:"config,omitempty" yaml:"config,omitempty"`
	// SymbolIndexes reports each symbol-capable project's SCIP index freshness (up to
	// date / out of date / not indexed), so `magus status` and the dashboard show at a
	// glance whether code symbols reflect current source. Empty when the workspace is
	// unavailable or no project is symbol-capable.
	SymbolIndexes []SymbolIndexStatus `json:"symbol_indexes,omitempty" yaml:"symbol_indexes,omitempty"`
	// Locks are the per-project workspace locks held right now, with the process
	// holding each. A held lock is NORMAL - every mutating run takes one - so this is
	// reported as state, never as a fault: it must not fail a readiness or liveness
	// probe, because a run blocked on a peer is waiting correctly and restarting it
	// would only send it to the back of the queue.
	//
	// It is here because the failure it makes visible is otherwise invisible. A lock is
	// held for as long as its process lives, so a process nobody remembers starting
	// holds one indefinitely, and every other run simply waits. Surfacing who holds
	// what turns that from a hang into a fact.
	Locks []StatusLock `json:"locks,omitempty" yaml:"locks,omitempty"`
	// MCPEndpoint reports the health of the MCP HTTP endpoint agent hosts (an editor,
	// IDEs, Desktop) actually connect to: its address and whether it is really serving.
	// It is checked independently of the Pool fields above, which report the proc socket
	// the daemon dispatches jobs on. The two listeners share a process in normal
	// operation but can diverge (the MCP server failing to bind while the proc daemon is
	// fine), so a "daemon is up" reading does not by itself prove the tools are reachable.
	// Nil when reported by a process that does not probe it (e.g. the daemon's own report).
	MCPEndpoint *MCPEndpointStatus `json:"mcp_endpoint,omitempty" yaml:"mcp_endpoint,omitempty"`
}

// ReadinessReport is the JSON body of GET /readyz: Ready mirrors the pass/fail gate a
// kubelet's status-code check already enforces (200 iff Ready), and Components adds
// component-level detail an orchestrator ignores but a browser client (the console PWA)
// can render as per-subsystem daemon health. Adding this body does not change the gate -
// it is purely additive alongside the existing 200/503 status code.
type ReadinessReport struct {
	Ready      bool                 `json:"ready"`
	Components []ReadinessComponent `json:"components"`
}

// ReadinessComponent is one subsystem's readiness within a ReadinessReport. Status is one
// of: ok, degraded, down, idle, disabled. Detail is a short plain-ASCII human-readable line
// that stays generic and quantitative (e.g. "2 of 3 up to date") because /readyz is served
// unguarded: counts inform without identifying, but workspace roots, project or service
// names, filesystem paths, PIDs, and raw error text must never appear here - that
// identifying detail lives behind the bearer-guarded StatusService. Never the sole
// signal - a client should key off Status.
type ReadinessComponent struct {
	Name   ReadinessName   `json:"name"`
	Status ReadinessStatus `json:"status"`
	Detail string          `json:"detail"`
}

// ReadinessName identifies which subsystem a readiness component reports on, and
// ReadinessStatus is its verdict. Both were bare strings whose vocabulary lived in a
// trailing comment - which a compiler cannot check and a reader has to trust. A probe
// endpoint is read by a kubelet, so a value drifting from what the reader expects is
// the kind of break nothing surfaces until a rollout stalls.
type ReadinessName string

const (
	ReadinessWorkspaces     ReadinessName = "workspaces"
	ReadinessSymbolIndex    ReadinessName = "symbol_index"
	ReadinessServices       ReadinessName = "services"
	ReadinessKnowledgeGraph ReadinessName = "knowledge_graph"
)

// ReadinessStatus is one component's verdict. Idle and disabled are deliberately
// distinct from ok: a subsystem nobody asked for is not the same as one that ran.
type ReadinessStatus string

const (
	ReadinessOK       ReadinessStatus = "ok"
	ReadinessDegraded ReadinessStatus = "degraded"
	ReadinessDown     ReadinessStatus = "down"
	ReadinessIdle     ReadinessStatus = "idle"
	ReadinessDisabled ReadinessStatus = "disabled"
)

// MCPEndpointStatus is the runtime health of the MCP HTTP endpoint agent hosts connect
// to. State is one of: serving (listening and a workspace is loaded), not-ready
// (listening but no workspace yet), unreachable (nothing is listening), or disabled
// (mcp.enabled=false).
type MCPEndpointStatus struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	Address   string `json:"address,omitempty" yaml:"address,omitempty"`
	URL       string `json:"url,omitempty" yaml:"url,omitempty"`
	Reachable bool   `json:"reachable" yaml:"reachable"`
	State     string `json:"state" yaml:"state"`
	Note      string `json:"note,omitempty" yaml:"note,omitempty"`
}

// StatusConfig is the read-only slice of the daemon's resolved config surfaced on the status wire.
type StatusConfig struct {
	// DefaultCharms are the execution charms applied to every run (e.g. rw, cd, gha).
	DefaultCharms []string `json:"default_charms,omitempty" yaml:"default_charms,omitempty"`
	// Concurrency is the CONFIGURED cap on concurrent builds; 0 means nothing was
	// configured, not that no build may run.
	Concurrency int `json:"concurrency,omitempty" yaml:"concurrency,omitempty"`
	// ConcurrencyEffective is the width a run actually gets - Concurrency resolved through
	// the default and the machine clamp (internal/cache.ResolveConcurrency). It is the
	// number to budget against; Concurrency alone cannot be, because its common value is
	// the one that means "ask someone else".
	ConcurrencyEffective int `json:"concurrency_effective" yaml:"concurrency_effective"`
	// Sandbox reports whether subprocess/spell sandboxing is enabled.
	Sandbox bool `json:"sandbox" yaml:"sandbox"`
}

// ServiceState is where a supervised service sits in its lifecycle. Deliberately NOT
// TargetRunState: the two share only "running" and "failed", and describe different
// things - a service is idle when nothing needs it, a target run is cached when its
// result was replayed. One union type would make half the values invalid for each
// user, which is the opposite of what naming them buys.
type ServiceState string

const (
	ServiceStarting ServiceState = "starting"
	ServiceRunning  ServiceState = "running"
	ServiceIdle     ServiceState = "idle"
	ServiceFailed   ServiceState = "failed"
)

// StatusService is one long-running shared service the daemon is hosting, surfaced on
// the status wire so a dashboard can show what is running and how many targets depend
// on it. It mirrors service.ServiceStatus (the registry's introspection view).
type StatusService struct {
	ID         string       `json:"id" yaml:"id"`
	Label      string       `json:"label,omitempty" yaml:"label,omitempty"`
	Command    string       `json:"command,omitempty" yaml:"command,omitempty"`
	Ports      []string     `json:"ports,omitempty" yaml:"ports,omitempty"`
	State      ServiceState `json:"state,omitempty" yaml:"state,omitempty"`
	Dependents int          `json:"dependents,omitempty" yaml:"dependents,omitempty"`
	StartedAt  time.Time    `json:"started_at,omitempty" yaml:"started_at,omitempty"`
}

// StatusLock is one held per-project workspace lock and the process holding it.
//
// Held is the normal state, not an alarm. What makes it worth reporting is the
// holder: an OS file lock carries no identity of its own, so without this a blocked
// run can only say "another magus process" and a lock held by something abandoned is
// indistinguishable from one held by a peer that is about to finish.
type StatusLock struct {
	// Project is the workspace-relative path whose lock is held ("." for the root).
	Project string `json:"project" yaml:"project"`
	// PID, Command and Dir identify the holder. Dir is the one that usually settles
	// it: a holder running in a directory that no longer exists is abandoned.
	PID     int    `json:"pid,omitempty" yaml:"pid,omitempty"`
	Command string `json:"command,omitempty" yaml:"command,omitempty"`
	Dir     string `json:"dir,omitempty" yaml:"dir,omitempty"`
	// StaleAfterSeconds is the threshold at which this holder should be read as
	// possibly abandoned rather than busy. Carried on the wire so every renderer
	// shares one judgment instead of each picking its own.
	StaleAfterSeconds int `json:"stale_after_seconds,omitempty" yaml:"stale_after_seconds,omitempty"`
	// Waiters are the processes blocked on this lock right now. Empty is the common
	// case; a non-empty list is the other half of the picture, because a holder alone
	// says who is working and a waiter list says who is stalled because of it.
	Waiters []StatusLockWaiter `json:"waiters,omitempty" yaml:"waiters,omitempty"`
	// AcquireTime is when the holder took it. Age is the signal a human reads: seconds
	// is a peer, days is something nobody knows is running. Named per AIP-142, which
	// asks for a _time suffix on a timestamp rather than the _at spelling.
	AcquireTime time.Time `json:"acquire_time,omitempty" yaml:"acquire_time,omitempty"`
}

// StatusLockWaiter is one process blocked on a lock, with how long it has been
// blocked. A waiter is transient by nature, so this is only ever a snapshot.
type StatusLockWaiter struct {
	PID      int       `json:"pid,omitempty" yaml:"pid,omitempty"`
	Command  string    `json:"command,omitempty" yaml:"command,omitempty"`
	Dir      string    `json:"dir,omitempty" yaml:"dir,omitempty"`
	WaitTime time.Time `json:"wait_time,omitempty" yaml:"wait_time,omitempty"`
}

// TargetRunState is where a target sits in its lifecycle within a run. Values match the
// magus.status.v1alpha1.TargetRun.State enum names (lowercased) so the JSON and the wire agree.
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
	ParentPID     int    `json:"parent_pid" yaml:"parent_pid"`
	DaemonVersion string `json:"daemon_version,omitempty" yaml:"daemon_version,omitempty"`
	Mode          string `json:"mode,omitempty" yaml:"mode,omitempty"` // "daemon", "proc", or ""
	// Socket is the proc-server address this snapshot was read from, so a reader running
	// more than one server can tell the entries apart and narrow with --socket.
	Socket   string `json:"socket,omitempty" yaml:"socket,omitempty"`
	Capacity int    `json:"capacity" yaml:"capacity"`
	Running  int    `json:"running" yaml:"running"`
	// Available is the free slots: Capacity minus Running, floored at zero. Carried as a
	// number because the question it answers - how much work can I hand this box right
	// now - should not require the reader to subtract.
	Available      int                   `json:"available" yaml:"available"`
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
	// Work the hits replayed instead of ran, summed from each entry's recorded run duration.
	CacheSavedMs int64 `json:"cache_saved_ms,omitempty" yaml:"cache_saved_ms,omitempty"`
	// SecretProvider is the provider spell the magusfile selected; empty means the
	// built-in environment provider. The NAME only - never a reference, never a value.
	SecretProvider string `json:"secret_provider,omitempty" yaml:"secret_provider,omitempty"`
}
