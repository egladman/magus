package types

import (
	"fmt"
	"slices"
)

// SandboxMode is how hard magus confines a run: not at all, as far as the host
// allows, or with the kernel's enforcement or not at all.
//
// UnmarshalText is the only way from a name to a mode, and it refuses an unknown one,
// so yaml, the environment and flags all stop a misspelling where it enters. The zero
// value means unset and behaves as SandboxModeOff.
type SandboxMode string

const (
	// SandboxModeOff attaches no policy: every check passes and children inherit the
	// whole environment. The default.
	SandboxModeOff SandboxMode = "off"
	// SandboxModeBestEffort confines each child with kernel landlock where the host
	// has it, and with magus's own binding checks alone, saying so once (MGS2005),
	// where it does not.
	SandboxModeBestEffort SandboxMode = "best-effort"
	// SandboxModeRequired refuses to run (MGS2012) unless the kernel can confine
	// every child: landlock ABI 3 or newer.
	SandboxModeRequired SandboxMode = "required"
)

// Resolved is m with the zero value replaced by the mode it behaves as.
func (m SandboxMode) Resolved() SandboxMode {
	if m == "" {
		return SandboxModeOff
	}
	return m
}

// Enabled reports whether m attaches a policy at all.
func (m SandboxMode) Enabled() bool { return m.Resolved() != SandboxModeOff }

// WeakerThan reports whether m confines less than o. The modes are ordered off,
// best-effort, required; a nested or forwarded run may only move up that order.
func (m SandboxMode) WeakerThan(o SandboxMode) bool {
	return slices.Index(sandboxModes, m.Resolved()) < slices.Index(sandboxModes, o.Resolved())
}

// String renders m, naming the zero value by the mode it behaves as.
func (m SandboxMode) String() string { return string(m.Resolved()) }

// MarshalText writes the name as configured, so an unset mode stays unset.
func (m SandboxMode) MarshalText() ([]byte, error) { return []byte(m), nil }

// UnmarshalText sets m from a name, refusing one outside Values.
func (m *SandboxMode) UnmarshalText(text []byte) error {
	v := SandboxMode(text)
	if !v.Valid() {
		return fmt.Errorf("unknown sandbox mode %q (want one of %v)", text, v.Values())
	}
	*m = v
	return nil
}
