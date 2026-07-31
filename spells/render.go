package spells

// Render outputs: what a spell's command looks like once charms apply, and the
// static shape of a service op. They describe a spell's work, so they live with it.

// ServiceView is the static, pre-run description of a service op, shown by `magus
// describe target` when the target is a service. Every field is known without
// starting the service; live registry state (ref-count, probe status) needs the
// daemon and is not part of this static view.
type ServiceView struct {
	Readiness   []string `json:"readiness,omitempty"   yaml:"readiness,omitempty"`   // probe command polled until it exits 0, if any
	Stop        []string `json:"stop,omitempty"        yaml:"stop,omitempty"`        // graceful-shutdown command, if any
	Idle        string   `json:"idle,omitempty"        yaml:"idle,omitempty"`        // idle-timeout override (a duration), else the daemon default
	Distinct    string   `json:"distinct,omitempty"    yaml:"distinct,omitempty"`    // dedup opt-out reason; empty means the instance is shared
	Fingerprint string   `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"` // content hash that keys shared-instance dedup
}

// CharmConflict reports an active charm (Name) whose edit is overwritten by another
// active charm (OverriddenBy) on the same command, so Name has no effect there. The
// winner is decided by sorted charm name, not declared precedence.
type CharmConflict struct {
	Name         string `json:"name"                    yaml:"name"`
	OverriddenBy string `json:"overridden_by,omitempty" yaml:"overridden_by,omitempty"`
}

// CharmTraceStep is one line of a charm-application trace: the command (cmd as
// element 0) after the named charm's patch applies on top of the prior step. The
// base step (before any charm) has an empty Charm.
type CharmTraceStep struct {
	Charm   string   `json:"charm,omitempty"   yaml:"charm,omitempty"`
	Command []string `json:"command"           yaml:"command"`
}
