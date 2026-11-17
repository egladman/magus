package sandbox

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// Adoption is a server's view of a forwarded run's sandbox: what the client runs under,
// what this process's workspace runs under, and the error that declines a run the
// workspace cannot take, which internal/proc owns and this package cannot import.
type Adoption struct {
	Floor     types.SandboxMode
	Workspace func() (types.SandboxMode, error)
	Declined  error
}

// Check holds an adopted run to the sandbox mode its client runs under, or a
// stronger one.
//
// The forwarded flags bind into this process's globalCfg, but the run is sandboxed by
// the workspace this process already opened, so a client's --sandbox reached nothing in
// either direction. A client asking for less than its floor or than this workspace's
// mode is refused (MGS2010). A client whose floor this workspace does not meet is
// declined (a.Declined), so it runs the work itself under its own mode; executing
// it here would run a confined client's work under less.
func (a Adoption) Check(subArgs []string) error {
	flag, set := Flag(subArgs)
	if set && flag.WeakerThan(a.Floor) {
		return errorWeakened("a forwarded run", flag, a.Floor)
	}
	want := a.Floor
	if set && want.WeakerThan(flag) {
		want = flag
	}
	if !want.Enabled() && !set {
		return nil
	}
	have, err := a.Workspace()
	if err != nil {
		return err
	}
	if set && flag.WeakerThan(have) {
		return errorWeakened("a forwarded run", flag, have)
	}
	if have.WeakerThan(want) {
		return fmt.Errorf("%w: the client runs under sandbox mode %s and this workspace's server runs it under %s",
			a.Declined, want, have)
	}
	return nil
}

// Flag reads --sandbox=<mode> (or --sandbox <mode>) from a subcommand's args, the
// way the flag package would, the last one winning. It stops at "--", past which args
// belong to a tool. A value that names no mode is not a setting: the subcommand's own
// parse rejects it.
func Flag(args []string) (mode types.SandboxMode, set bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "sandbox" {
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				continue
			}
			i++
			value = args[i]
		}
		var m types.SandboxMode
		if err := m.UnmarshalText([]byte(value)); err == nil && value != "" {
			mode, set = m, true
		}
	}
	return mode, set
}

// RefuseInheritedWeakened refuses a nested magus that would run under a weaker
// sandbox mode than the run that started it. The parent hands every child its mode
// (spawn.SandboxEnvVar, the setting's own variable), and only a flag can lower it, so this
// is the flag's check. nested is whether a parent started this process.
func RefuseInheritedWeakened(mode types.SandboxMode, nested bool, inherited types.SandboxMode) error {
	if !nested {
		return nil
	}
	// An unknown value never reaches here: loading the config already refused it.
	if !inherited.Valid() || !mode.WeakerThan(inherited) {
		return nil
	}
	return errorWeakened("this magus, started inside a sandboxed run,", mode, inherited)
}

// errorWeakened is the MGS2010 refusal of who asking for mode under floor.
func errorWeakened(who string, mode, floor types.SandboxMode) error {
	return types.DiagnosticErrorf(types.SandboxWeakened,
		"%s asked for sandbox mode %s, weaker than the %s it runs under; a nested or forwarded run may only strengthen the sandbox", who, mode, floor)
}
