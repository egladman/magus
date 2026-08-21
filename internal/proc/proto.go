// Package proc implements the magus "process adoption" mechanism: child
// magus processes detect MAGUS_DAEMON_SOCKET and forward work over a
// Unix-domain socket RPC, sharing the parent's cache, logger, and concurrency budget.
package proc

import (
	"context"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// ProtocolV2 identifies the JSONL message shape; distinct from the binary Version.
// Servers reject an unknown non-empty protocol with ErrProtocolMismatch.
const ProtocolV2 = "v2"

// Wire-type strings embedded in every JSONL frame's "type" field.
const (
	typeRun           = "run"
	typeRunReply      = "run.reply"
	typeStatus        = "status"
	typeStatusReply   = "status.reply"
	typeShutdown      = "shutdown"
	typeShutdownReply = "shutdown.reply"
	typeError         = "error"

	typeServiceAcquire      = "service.acquire"
	typeServiceAcquireReply = "service.acquire.reply"
	typeServiceRelease      = "service.release"
	typeServiceReleaseReply = "service.release.reply"
	typeServiceStopAll      = "service.stopall"
	typeServiceStopAllReply = "service.stopall.reply"

	typeConfigReload      = "config.reload"
	typeConfigReloadReply = "config.reload.reply"

	typeJob      = "job"
	typeJobReply = "job.reply"
)

// JobMagic guards JobRequest: a fire-and-forget submission that the daemon runs in the
// background is a privileged operation (it executes arbitrary magus args), so a request
// without the magic is ignored, matching the StatusRequest/ShutdownRequest pattern.
const JobMagic = "magus-job-v1"

// JobRequest submits a background job: the daemon runs `magus <Args>` asynchronously and
// replies immediately, unlike RunRequest which blocks until the run completes. Used by
// the VCS refresh hook to kick a rebuild/reindex without delaying a checkout.
type JobRequest struct {
	Magic    string   `json:"magic"`
	Args     []string `json:"args"`
	Version  string   `json:"version,omitempty"`
	Cwd      string   `json:"cwd"`
	Protocol string   `json:"protocol"`
	Root     string   `json:"root,omitempty"` // empty → daemon walks up from Cwd
}

// JobReply acknowledges a submitted job. Inv is the invocation id (a Dashboard deep-link
// into the job's live log); Err is non-empty only when the job could not be accepted
// (the job's own success/failure is observed via the Dashboard, not this reply).
type JobReply struct {
	Inv string `json:"inv,omitempty"`
	Err string `json:"err,omitempty"`
}

// RunRequest is the JSONL payload sent from a child magus to its parent.
type RunRequest struct {
	Args     []string `json:"args"`
	Version  string   `json:"version,omitempty"`
	Cwd      string   `json:"cwd"`
	Protocol string   `json:"protocol"`
	Root     string   `json:"root,omitempty"` // empty → daemon walks up from Cwd
	// Ancestors is the client's invocation ancestry, oldest first. The daemon adopts it
	// so a run it executes on this client's behalf can recognize a project lock held by
	// one of the client's OWN ancestors - which, under the daemon, is a lock this very
	// process holds. Empty from a client that predates the field: re-entry detection is
	// then unavailable and the acquire falls back to waiting.
	Ancestors []string `json:"ancestors,omitempty"`
}

// RunReply is the response from the parent to the child.
type RunReply struct {
	ExitCode int    `json:"exit_code"`
	Err      string `json:"err,omitempty"` // human-readable; non-empty when ExitCode != 0
}

// StatusRequest is the payload for the status JSONL message.
// Magic must equal StatusMagic; unrecognized requests get an empty reply.
type StatusRequest struct {
	Magic    string `json:"magic"`
	Protocol string `json:"protocol"`
}

// Workspace describes one workspace currently loaded by the daemon.
type Workspace struct {
	Root       string    `json:"root"`
	LoadedAt   time.Time `json:"loaded_at"`
	LastAccess time.Time `json:"last_access"`
	// Live cache activity for this workspace's long-lived cache. Zero for pre-cache-aware
	// daemons or an Inspect workspace with no cache.
	CacheHit   int   `json:"cache_hit,omitempty"`
	CacheMiss  int   `json:"cache_miss,omitempty"`
	CacheError int   `json:"cache_error,omitempty"`
	CacheBytes int64 `json:"cache_bytes,omitempty"`
	// Work the hits replayed instead of ran, summed from each entry's recorded duration.
	CacheSavedMs int64 `json:"cache_saved_ms,omitempty"`
	// SecretProvider is the selected provider spell's name; empty = built-in env provider.
	SecretProvider string `json:"secret_provider,omitempty"`
}

// StatusReply carries a point-in-time view of the parent's pool.
type StatusReply struct {
	ParentPID     int         `json:"parent_pid"`
	DaemonVersion string      `json:"daemon_version,omitempty"`
	Mode          string      `json:"mode,omitempty"` // "daemon" (multi-workspace) | "proc" (per-process)
	Capacity      int         `json:"capacity"`
	Running       int         `json:"running"`
	Queued        int         `json:"queued"`
	Calls         []Call      `json:"calls,omitempty"`
	Workspaces    []Workspace `json:"workspaces,omitempty"` // nil for per-process proc servers
	// Services are the long-running shared services the daemon is hosting right now.
	// Nil for a per-process proc server (no cross-invocation service host).
	Services []types.StatusService `json:"services,omitempty"`
}

// Call describes a single adopted call currently executing.
type Call struct {
	Args      []string  `json:"args"`
	Workspace string    `json:"workspace,omitempty"`  // empty for pre-workspace-aware servers
	StartedAt time.Time `json:"started_at,omitempty"` // zero for pre-timing-aware servers
	SubOp     string    `json:"sub_op,omitempty"`     // short label of what the call is doing now
	Inv       string    `json:"inv,omitempty"`        // the invocation id this call runs under; deep-links to its live log
}

// StatusMagic is the expected value of StatusRequest.Magic.
const StatusMagic = "magus-pool-v1"

// ShutdownRequest is the payload for the shutdown JSONL message.
// Magic must equal ShutdownMagic; unrecognized requests are ignored.
type ShutdownRequest struct {
	Magic    string `json:"magic"`
	Protocol string `json:"protocol"`
}

// ShutdownReply is the response to a shutdown request.
type ShutdownReply struct{}

// ShutdownMagic is the expected value of ShutdownRequest.Magic.
const ShutdownMagic = "magus-shutdown-v1"

// ServiceAcquireRequest asks the daemon to start (or reuse) a shared service and
// keep it warm past this invocation. Key is the service fingerprint; Service is the
// resolved process description (command, readiness, stop, idle).
type ServiceAcquireRequest struct {
	Protocol string         `json:"protocol"`
	Key      string         `json:"key"`
	Service  spells.Service `json:"service"`
}

// ServiceAcquireReply reports whether the service came up. Err is non-empty when it
// could not be started or did not become ready.
type ServiceAcquireReply struct {
	Err string `json:"err,omitempty"`
}

// ServiceReleaseRequest drops this invocation's hold on a shared service. The daemon
// keeps it warm (idle timeout) and reaps it later, so a later run reuses it.
type ServiceReleaseRequest struct {
	Protocol string `json:"protocol"`
	Key      string `json:"key"`
}

// ServiceReleaseReply is the response to a release.
type ServiceReleaseReply struct{}

// ServiceStopAllRequest asks the daemon to stop every service it is hosting while
// staying up, for `magus server stop --services`. It clears warm services (stale
// data, held ports) without killing the daemon.
type ServiceStopAllRequest struct {
	Protocol string `json:"protocol"`
}

// ServiceStopAllReply reports how many services were stopped.
type ServiceStopAllReply struct {
	Count int `json:"count"`
}

// ConfigReloadRequest asks the daemon to drop the workspaces it is holding open, so the
// next command against each one reopens it and re-reads magus.yaml. It is the config
// counterpart of ServiceStopAllRequest: a partial reset that leaves the daemon up.
//
// There is no "apply this config" payload, and deliberately so - the daemon does not hold
// a config to patch, it holds OPEN WORKSPACES that each captured one when they loaded.
// Dropping them is the reload; the config is then read from disk the ordinary way,
// through exactly the path a cold start uses. Nothing here can disagree with that path
// because nothing here duplicates it.
type ConfigReloadRequest struct {
	Protocol string `json:"protocol"`
}

// ConfigReloadReply reports how many workspaces were dropped and how many were left
// alone because a run was in flight. Busy is not an error: those keep the config they
// started with, which is what a run in progress should do.
type ConfigReloadReply struct {
	Dropped int `json:"dropped"`
	Busy    int `json:"busy"`
}

// ServiceHost hosts long-running shared services on behalf of adopted magus
// invocations, keeping them warm across separate runs. The daemon supplies one via
// [Options]; a per-process proc server leaves it nil (no cross-invocation hosting).
// Acquire/Release mirror the ref-counted lifecycle of cache.Limiter and
// service.Registry, the shared vocabulary for held resources.
type ServiceHost interface {
	// Acquire starts (or reuses) the service identified by key, returning once it is
	// ready, and increments its dependent count.
	Acquire(ctx context.Context, key string, svc spells.Service) error
	// Release drops one dependent of key; the host keeps it warm and reaps it later.
	Release(key string)
	// StopAll stops every hosted service and returns how many were stopped, leaving
	// the daemon running.
	StopAll() int
}

// ErrorReply is returned by the server for transport-level failures.
type ErrorReply struct {
	Message string `json:"message"`
}
