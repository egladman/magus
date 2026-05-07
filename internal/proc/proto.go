// Package proc implements the magus "process adoption" mechanism: child
// magus processes detect MAGUS_DAEMON_SOCKET and forward work over a
// Unix-domain socket RPC, sharing the parent's cache, logger, and concurrency budget.
package proc

import (
	"errors"
	"time"
)

// ProtocolV2 identifies the JSONL message shape; distinct from the binary Version.
// Servers reject an unknown non-empty protocol with ErrProtocolSkew.
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
)

// RunRequest is the JSONL payload sent from a child magus to its parent.
type RunRequest struct {
	Args     []string `json:"args"`
	Version  string   `json:"version,omitempty"`
	Cwd      string   `json:"cwd"`
	Protocol string   `json:"protocol"`
	Root     string   `json:"root,omitempty"` // empty → daemon walks up from Cwd
}

// RunReply is the response from the parent to the child.
type RunReply struct {
	ExitCode int    `json:"exit_code"`
	Err      string `json:"err,omitempty"` // human-readable; non-empty when ExitCode != 0
}

// StatusRequest is the payload for the status JSONL message.
// Magic must equal StatusMagic; unrecognised requests get an empty reply.
type StatusRequest struct {
	Magic    string `json:"magic"`
	Protocol string `json:"protocol"`
}

// Workspace describes one workspace currently loaded by the daemon.
type Workspace struct {
	Root       string    `json:"root"`
	LoadedAt   time.Time `json:"loaded_at"`
	LastAccess time.Time `json:"last_access"`
}

// StatusReply carries a point-in-time view of the parent's pool.
type StatusReply struct {
	ParentPID     int         `json:"parent_pid"`
	DaemonVersion string      `json:"daemon_version,omitempty"`
	Mode          string      `json:"mode,omitempty"` // "daemon" (multi-workspace) | "proc" (per-process)
	Capacity      int         `json:"capacity"`
	InUse         int         `json:"in_use"`
	Waiting       int         `json:"waiting"`
	Calls         []Call      `json:"calls,omitempty"`
	Workspaces    []Workspace `json:"workspaces,omitempty"` // nil for per-process proc servers
}

// Call describes a single adopted call currently executing.
type Call struct {
	Args      []string  `json:"args"`
	Workspace string    `json:"workspace,omitempty"`  // empty for pre-workspace-aware servers
	StartedAt time.Time `json:"started_at,omitempty"` // zero for pre-timing-aware servers
	SubOp     string    `json:"sub_op,omitempty"`     // short label of what the call is doing now
}

// StatusMagic is the expected value of StatusRequest.Magic.
const StatusMagic = "magus-pool-v1"

// ShutdownRequest is the payload for the shutdown JSONL message.
// Magic must equal ShutdownMagic; unrecognised requests are ignored.
type ShutdownRequest struct {
	Magic    string `json:"magic"`
	Protocol string `json:"protocol"`
}

// ShutdownReply is the response to a shutdown request.
type ShutdownReply struct{}

// ShutdownMagic is the expected value of ShutdownRequest.Magic.
const ShutdownMagic = "magus-shutdown-v1"

// ErrorReply is returned by the server for transport-level failures.
type ErrorReply struct {
	Message string `json:"message"`
}

var (
	// ErrAlreadyAdopted is returned by New when MAGUS_DAEMON_SOCKET is already set.
	ErrAlreadyAdopted = errors.New("proc: already running under a parent magus")

	// ErrCycleDetected is set in RunReply.Err when the same (target, project) pair is already in-flight.
	ErrCycleDetected = errors.New("proc: cycle detected in nested magus invocation")

	// ErrVersionSkew is returned when the child's build version differs from the parent's.
	ErrVersionSkew = errors.New("proc: version mismatch between parent and child magus")

	// ErrProtocolSkew is returned when a client sends an unrecognised non-empty Protocol value.
	ErrProtocolSkew = errors.New("proc: protocol version mismatch")

	// ErrNotAdoptable signals that the daemon cannot service this subcommand; client falls back locally.
	ErrNotAdoptable = errors.New("proc: subcommand not adoptable")
)
