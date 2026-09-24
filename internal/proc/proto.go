// Package proc implements the magus "process adoption" mechanism: child
// magus processes detect MAGUS_DAEMON_SOCKET and forward work over a
// Unix-domain socket RPC, sharing the parent's cache, logger, and concurrency budget.
package proc

import (
	"time"

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

	typeConfigReload      = "config.reload"
	typeConfigReloadReply = "config.reload.reply"

	typeJob      = "job"
	typeJobReply = "job.reply"
)

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
	// one of the client's OWN ancestors, which, under the daemon, is a lock this very
	// process holds. Empty from a client that predates the field: re-entry detection is
	// then unavailable and the acquire falls back to waiting.
	Ancestors []string `json:"ancestors,omitempty"`
	// Lease is the lease the CLIENT was launched under, carried because the
	// daemon executes the run in its own process and so reads its own environment, not
	// the client's; without this an adopted run records no lease at all.
	//
	// It is the client's own claim about itself, exactly what the BAGGAGE channel's
	// magus.lease member is (the client's trace context does not cross this socket, so
	// an adopted run records the lease and no ancestry) and it
	// arrives over a socket any local process may dial. The server therefore re-validates
	// it with types.ValidJobID and drops a value that fails, matching what
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

// Workspace describes one workspace the daemon holds: loading, loaded, or failed.
type Workspace struct {
	Root string `json:"root"`
	// State is empty from an older daemon, which reported only loaded workspaces.
	// compat: see types.StatusWorkspace.Loaded
	State types.WorkspaceState `json:"state,omitempty"`
	// Error is set only in WorkspaceFailed.
	Error      *types.WorkspaceFailure `json:"error,omitempty"`
	LoadedAt   time.Time               `json:"loaded_at"`
	LastAccess time.Time               `json:"last_access"`
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

// Loaded reports whether w is serving: active, or from a daemon that reports no state.
// compat: see types.StatusWorkspace.Loaded
func (w Workspace) Loaded() bool { return types.StatusWorkspace{State: w.State}.Loaded() }

// StatusReply carries a point-in-time view of the parent's pool.
type StatusReply struct {
	ParentPID  int         `json:"parent_pid"`
	Version    string      `json:"version,omitempty"`
	Capacity   int         `json:"capacity"`
	Running    int         `json:"running"`
	Queued     int         `json:"queued"`
	Calls      []Call      `json:"calls,omitempty"`
	Workspaces []Workspace `json:"workspaces,omitempty"` // nil for per-process proc servers
	// Server is the server's own identity and listeners. Only `magus server` sets it,
	// so its presence is what tells the server from a per-process proc server, which
	// dies with the command that started it.
	Server *types.StatusServer `json:"server,omitempty"`
}

// StatusOutput is r as the status report's pool section, nil for a nil reply. It is the
// one conversion `magus status` and the console both read, so the two cannot disagree.
//
// Affected is left unset: it needs a workspace-scoped VCS diff, and neither reader opens a
// workspace to answer a status query.
func (r *StatusReply) StatusOutput() *types.StatusOutput {
	if r == nil {
		return nil
	}
	out := &types.StatusOutput{
		ParentPID: r.ParentPID,
		Version:   r.Version,
		Capacity:  r.Capacity,
		Running:   r.Running,
		// Floored: a daemon whose capacity was clamped under load can report more running
		// than capacity, and "-2 available" is worse than "0".
		Available: max(0, r.Capacity-r.Running),
		Queued:    r.Queued,
	}
	for _, c := range r.Calls {
		out.RunningTargets = append(out.RunningTargets, types.StatusRunningTarget{
			Args: c.Args, Workspace: c.Workspace, StartedAt: c.StartedAt, Step: c.SubOp, Inv: c.Inv,
		})
	}
	for _, w := range r.Workspaces {
		out.Workspaces = append(out.Workspaces, types.StatusWorkspace{
			Root: w.Root, State: w.State, Error: w.Error, LoadedAt: w.LoadedAt, LastAccess: w.LastAccess,
			CacheHit: w.CacheHit, CacheMiss: w.CacheMiss, CacheError: w.CacheError,
			CacheBytes: w.CacheBytes, CacheSavedMs: w.CacheSavedMs, SecretProvider: w.SecretProvider,
		})
	}
	return out
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

// configReloadRequest asks the server to drop the workspaces it is holding open, so the
// next command against each one reopens it and re-reads magus.yaml: a partial reset
// that leaves the server up.
//
// There is no "apply this config" payload, and deliberately so: the server does not hold
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

// errorReply is returned by the server for transport-level failures.
type errorReply struct {
	Message string `json:"message"`
}
