// Package proc implements the magus "process adoption" mechanism: child
// magus processes detect [SocketEnv] and forward work over HTTP on a
// Unix-domain socket, sharing the parent's cache, logger, and concurrency budget.
package proc

import (
	"time"

	"github.com/egladman/magus/types"
)

// SocketEnv names the variable a magus process exports to its children: the unix://
// address of the proc server they forward to, which is either this process's own
// per-process pool or the `magus server` it adopted. Set by magus, never by a person.
const SocketEnv = "MAGUS_PROC_SOCKET"

// TokenEnv carries the secret the server at [SocketEnv] requires on every /proc/ request.
// A magus exports it beside SocketEnv, and like SocketEnv only a nested magus inherits
// it: an ordinary op subprocess is never handed either (see run.ProcForwardVars).
const TokenEnv = "MAGUS_PROC_TOKEN" //nolint:gosec // G101: an env var NAME, not a credential

// tokenHeader is the request header a client presents the token in.
const tokenHeader = "Magus-Proc-Token" //nolint:gosec // G101: a header NAME, not a credential

// The paths the proc server answers on its socket, one per operation. The version is in the
// path: a client and a server that disagree on it meet a 404, never a misread body.
const (
	pathRun      = "/proc/v1/run"
	pathJobs     = "/proc/v1/jobs"
	pathStatus   = "/proc/v1/status"
	pathShutdown = "/proc/v1/shutdown"
	pathReload   = "/proc/v1/reload"
)

var (
	needConsoleRead  = types.Need{Surface: types.SurfaceConsole, Level: types.LevelRead}
	needConsoleWrite = types.Need{Surface: types.SurfaceConsole, Level: types.LevelWrite}
)

// routeNeeds is the Need of every proc route. Reading the pool is a console read; anything
// that runs magus, drops workspaces or stops the process is a console write, the level the
// console's own JobService RunJob takes.
var routeNeeds = map[string]types.Need{
	pathRun:      needConsoleWrite,
	pathJobs:     needConsoleWrite,
	pathStatus:   needConsoleRead,
	pathShutdown: needConsoleWrite,
	pathReload:   needConsoleWrite,
}

// jobRequest submits a background job: the server runs `magus <Args>` asynchronously and
// replies immediately, unlike runRequest which blocks until the run completes. Used by
// the VCS refresh hook to kick a rebuild/reindex without delaying a checkout.
type jobRequest struct {
	Args    []string `json:"args"`
	Version string   `json:"version,omitempty"`
	Cwd     string   `json:"cwd"`
	Root    string   `json:"root,omitempty"` // empty → server walks up from Cwd
}

// jobReply acknowledges a submitted job. Inv is the invocation id (a Dashboard deep-link
// into the job's live log), empty when an identical job already in flight absorbed this one.
// The job's own success/failure is observed via the Dashboard, not this reply.
type jobReply struct {
	Inv string `json:"inv,omitempty"`
}

// runRequest is the payload a child magus sends its parent.
type runRequest struct {
	Args    []string `json:"args"`
	Version string   `json:"version,omitempty"`
	Cwd     string   `json:"cwd"`
	Root    string   `json:"root,omitempty"` // empty → server walks up from Cwd
	// Ancestors is the client's invocation ancestry, oldest first. The server adopts it
	// so a run it executes on this client's behalf can recognize a project lock held by
	// one of the client's OWN ancestors, which, under the server, is a lock this very
	// process holds.
	Ancestors []string `json:"ancestors,omitempty"`
	// Lease is the lease the CLIENT was launched under, carried because the
	// server executes the run in its own process and so reads its own environment, not
	// the client's; without this an adopted run records no lease at all.
	//
	// It is the client's own claim about itself, exactly what the BAGGAGE channel's
	// magus.lease member is (the client's trace context does not cross this socket, so
	// an adopted run records the lease and no ancestry). The server therefore re-validates
	// it with types.ValidJobID and drops a value that fails, matching what
	// trail.LeaseFromEnv does with a malformed environment value: a lease id is
	// exempt from the trail's redaction, so an unchecked one is a way to carry a
	// credential onto an event line.
	Lease string `json:"lease,omitempty"`
	// Sandbox is true when the client runs sandboxed, so the server may take the run only
	// if it will sandbox it too. See [WithSandboxFloor].
	Sandbox bool `json:"sandbox,omitempty"`
}

// runReply is the response from the parent to the child.
type runReply struct {
	ExitCode int    `json:"exit_code"`
	Err      string `json:"err,omitempty"` // human-readable; non-empty when ExitCode != 0
}

// Workspace describes one workspace the server holds: loading, loaded, or failed.
type Workspace struct {
	Root string `json:"root"`
	// State is empty from an older server, which reported only loaded workspaces.
	// compat: see types.StatusWorkspace.Loaded
	State types.WorkspaceState `json:"state,omitempty"`
	// Error is set only in WorkspaceFailed.
	Error      *types.WorkspaceFailure `json:"error,omitempty"`
	LoadedAt   time.Time               `json:"loaded_at"`
	LastAccess time.Time               `json:"last_access"`
	// Live cache activity for this workspace's long-lived cache. Zero for pre-cache-aware
	// servers or an Inspect workspace with no cache.
	CacheHit   int   `json:"cache_hit,omitempty"`
	CacheMiss  int   `json:"cache_miss,omitempty"`
	CacheError int   `json:"cache_error,omitempty"`
	CacheBytes int64 `json:"cache_bytes,omitempty"`
	// Work the hits replayed instead of ran, summed from each entry's recorded duration.
	CacheSavedMs int64 `json:"cache_saved_ms,omitempty"`
	// SecretProvider is the selected provider spell's name; empty = built-in env provider.
	SecretProvider string `json:"secret_provider,omitempty"`
}

// Loaded reports whether w is serving: active, or from a server that reports no state.
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
		// Floored: a server whose capacity was clamped under load can report more running
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

// configReloadReply reports how many workspaces a reload dropped and how many it left
// alone because a run was in flight. Busy is not an error: those keep the config they
// started with, which is what a run in progress should do.
//
// A reload carries no "apply this config" payload, and deliberately so: the server does
// not hold a config to patch, it holds OPEN WORKSPACES that each captured one when they
// loaded. Dropping them is the reload; the config is then read from disk the ordinary way,
// through exactly the path a cold start uses.
type configReloadReply struct {
	Dropped int `json:"dropped"`
	Busy    int `json:"busy"`
}

// errorReply is the body of a proc route's refusal: the sentinel's message, which the
// client matches back to the typed error (decodeWireError).
type errorReply struct {
	Message string `json:"message"`
}
