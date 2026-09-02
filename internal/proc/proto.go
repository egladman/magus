// Package proc implements the magus "process adoption" mechanism: child
// magus processes detect MAGUS_DAEMON_SOCKET and forward work over a
// Unix-domain socket RPC, sharing the parent's cache, logger, and concurrency budget.
package proc

import (
	"context"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// protocolV2 identifies the JSONL message shape; distinct from the binary Version.
// Servers reject an unknown non-empty protocol with ErrProtocolMismatch.
const protocolV2 = "v2"

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

	typeAdmit             = "admit"
	typeAdmitReply        = "admit.reply"
	typeAdmitRelease      = "admit.release"
	typeAdmitReleaseReply = "admit.release.reply"
)

// admitRequest asks the machine budget to seat one step. It is a POLL, not a blocking
// wait: the budget answers immediately with a grant, a refusal, or a queue position,
// and the client owns the waiting. The client is the process that can print the wait
// and whose death should retire the waiter, and a poll needs no per-connection queue
// state to survive a daemon restart.
type admitRequest struct {
	Protocol string `json:"protocol"`
	// Waiter identifies this step across its polls, so the budget can hold its place.
	Waiter string             `json:"waiter"`
	Claim  cache.MachineClaim `json:"claim"`
}

// admitReply carries the verdict. Err is non-empty only when this server holds no
// budget to arbitrate, which a client treats as no arbiter rather than as a failure.
type admitReply struct {
	Verdict cache.MachineVerdict `json:"verdict"`
	Err     string               `json:"err,omitempty"`
}

// admitReleaseRequest returns a granted claim (ID) or retires a waiter that gave up
// (Waiter). One frame for both because they are the same event from the budget's side:
// this step is no longer asking for room.
type admitReleaseRequest struct {
	Protocol string `json:"protocol"`
	ID       string `json:"id,omitempty"`
	Waiter   string `json:"waiter,omitempty"`
}

// admitReleaseReply is the response to a release. It carries no fields: the client
// only checks the frame's type tag, so a field added here would need a matching decode
// arm added to the client at the same time.
type admitReleaseReply struct{}

// jobMagic guards jobRequest: a fire-and-forget submission that the daemon runs in the
// background is a privileged operation (it executes arbitrary magus args), so a request
// without the magic is ignored, matching the statusRequest/shutdownRequest pattern.
const jobMagic = "magus-job-v1"

// jobRequest submits a background job: the daemon runs `magus <Args>` asynchronously and
// replies immediately, unlike runRequest which blocks until the run completes. Used by
// the VCS refresh hook to kick a rebuild/reindex without delaying a checkout.
type jobRequest struct {
	Magic    string   `json:"magic"`
	Args     []string `json:"args"`
	Version  string   `json:"version,omitempty"`
	Cwd      string   `json:"cwd"`
	Protocol string   `json:"protocol"`
	Root     string   `json:"root,omitempty"` // empty → daemon walks up from Cwd
}

// jobReply acknowledges a submitted job. Inv is the invocation id (a Dashboard deep-link
// into the job's live log); Err is non-empty only when the job could not be accepted
// (the job's own success/failure is observed via the Dashboard, not this reply).
type jobReply struct {
	Inv string `json:"inv,omitempty"`
	Err string `json:"err,omitempty"`
}

// runRequest is the JSONL payload sent from a child magus to its parent.
type runRequest struct {
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
	// Lease is the lease the CLIENT was launched under, carried because the
	// daemon executes the run in its own process and so reads its own environment, not
	// the client's - without this an adopted run records no lease at all.
	//
	// It is the client's own claim about itself, exactly what the BAGGAGE channel's
	// magus.lease member is - the client's trace context does not cross this socket, so
	// an adopted run records the lease and no ancestry - and it
	// arrives over a socket any local process may dial. The server therefore re-validates
	// it with types.ValidLeaseID and drops a value that fails, matching what
	// trail.LeaseFromEnv does with a malformed environment value: a lease id is
	// exempt from the trail's redaction, so an unchecked one is a way to carry a
	// credential onto an event line. Empty from a client that predates the field.
	Lease string `json:"lease,omitempty"`
}

// runReply is the response from the parent to the child.
type runReply struct {
	ExitCode int    `json:"exit_code"`
	Err      string `json:"err,omitempty"` // human-readable; non-empty when ExitCode != 0
}

// statusRequest is the payload for the status JSONL message.
// Magic must equal statusMagic; unrecognized requests get an empty reply.
type statusRequest struct {
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
	// Machine is the host-wide admission budget this daemon arbitrates: what every
	// magus on the machine holds and who is queued for it. Nil for a per-process proc
	// server, which arbitrates nothing beyond itself.
	Machine *cache.MachineSnapshot `json:"machine,omitempty"`
}

// Call describes a single adopted call currently executing.
type Call struct {
	Args      []string  `json:"args"`
	Workspace string    `json:"workspace,omitempty"`  // empty for pre-workspace-aware servers
	StartedAt time.Time `json:"started_at,omitempty"` // zero for pre-timing-aware servers
	SubOp     string    `json:"sub_op,omitempty"`     // short label of what the call is doing now
	Inv       string    `json:"inv,omitempty"`        // the invocation id this call runs under; deep-links to its live log
}

// statusMagic is the expected value of statusRequest.Magic.
const statusMagic = "magus-pool-v1"

// shutdownRequest is the payload for the shutdown JSONL message.
// Magic must equal shutdownMagic; unrecognized requests are ignored.
type shutdownRequest struct {
	Magic    string `json:"magic"`
	Protocol string `json:"protocol"`
}

// shutdownReply is the response to a shutdown request. It carries no fields: the client
// only checks the frame's type tag, so a field added here would need a matching decode
// arm added to Shutdown in client.go at the same time.
type shutdownReply struct{}

// shutdownMagic is the expected value of shutdownRequest.Magic.
const shutdownMagic = "magus-shutdown-v1"

// serviceAcquireRequest asks the daemon to start (or reuse) a shared service and
// keep it warm past this invocation. Key is the service fingerprint; Service is the
// resolved process description (command, readiness, stop, idle).
type serviceAcquireRequest struct {
	Protocol string         `json:"protocol"`
	Key      string         `json:"key"`
	Service  spells.Service `json:"service"`
}

// serviceAcquireReply reports whether the service came up. Err is non-empty when it
// could not be started or did not become ready.
type serviceAcquireReply struct {
	Err string `json:"err,omitempty"`
}

// serviceReleaseRequest drops this invocation's hold on a shared service. The daemon
// keeps it warm (idle timeout) and reaps it later, so a later run reuses it.
type serviceReleaseRequest struct {
	Protocol string `json:"protocol"`
	Key      string `json:"key"`
}

// serviceReleaseReply is the response to a release. It carries no fields: the client
// only checks the frame's type tag, so a field added here would need a matching decode
// arm added to ReleaseService in client.go at the same time.
type serviceReleaseReply struct{}

// serviceStopAllRequest asks the daemon to stop every service it is hosting while
// staying up, for `magus server stop --services`. It clears warm services (stale
// data, held ports) without killing the daemon.
type serviceStopAllRequest struct {
	Protocol string `json:"protocol"`
}

// serviceStopAllReply reports how many services were stopped.
type serviceStopAllReply struct {
	Count int `json:"count"`
}

// configReloadRequest asks the daemon to drop the workspaces it is holding open, so the
// next command against each one reopens it and re-reads magus.yaml. It is the config
// counterpart of serviceStopAllRequest: a partial reset that leaves the daemon up.
//
// There is no "apply this config" payload, and deliberately so - the daemon does not hold
// a config to patch, it holds OPEN WORKSPACES that each captured one when they loaded.
// Dropping them is the reload; the config is then read from disk the ordinary way,
// through exactly the path a cold start uses. Nothing here can disagree with that path
// because nothing here duplicates it.
type configReloadRequest struct {
	Protocol string `json:"protocol"`
}

// configReloadReply reports how many workspaces were dropped and how many were left
// alone because a run was in flight. Busy is not an error: those keep the config they
// started with, which is what a run in progress should do.
type configReloadReply struct {
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

// errorReply is returned by the server for transport-level failures.
type errorReply struct {
	Message string `json:"message"`
}
