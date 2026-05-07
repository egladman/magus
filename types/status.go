package types

import "time"

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
