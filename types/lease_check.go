package types

// LeaseCheck is the one check a lease runs, in the parts the output store records a run
// by. The parts are the target, the project it runs in, and whatever
// is forwarded past `--`. The target carries a `spell::` filter when the job
// named one.
//
// A RECORD rather than a command line. A line has to be parsed before it can be compared
// with a stored run, and a parser that reads positions binds `run -o json test .` to a
// target named json. Rendering one as a command is the runner's business.
//
// The job module keeps a check as opaque JSON; this is the shape magus writes there.
type LeaseCheck struct {
	// Target is the target the check runs, without the runner's run command in front of it.
	// Empty when Script names the check instead.
	Target string `json:"target"             yaml:"target" schema:"target"`
	// Script is a Buzz script the check runs, recorded, in place of a
	// target, for a fact no target checks: a passing recorded run of it is the evidence. A
	// check names a target or a script, never both.
	Script string `json:"script,omitempty"   yaml:"script,omitempty"`
	// Project is the project the target runs in, "." when the job names none.
	Project string `json:"project,omitempty"  yaml:"project,omitempty"`
	// Args are forwarded to the tool after `--`, never to the runner.
	Args []string `json:"args,omitempty"     yaml:"args,omitempty"`
	// NoDefaultCharms runs the check with --no-default-charms, so only a run made without
	// the workspace's default_charms satisfies it: the charmless `generate` that compares
	// against HEAD, in a workspace whose default is rw.
	NoDefaultCharms bool `json:"no_default_charms,omitzero" yaml:"no_default_charms,omitempty"`
	// Test runs Script's tests instead of its main: passing tests and
	// a passing main are different facts, so each is satisfied only by its own run. A
	// script check alone carries it.
	Test bool `json:"test,omitzero" yaml:"test,omitempty"`
}
