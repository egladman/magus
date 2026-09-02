package ward

import (
	"fmt"
	"strings"
)

// Override is one switch that turns off a mechanism magus runs to catch mistakes,
// described in the terms its refusal needs.
//
// Prose rather than identifiers, because the caller a refusal reaches has to learn
// the exact form that satisfies it. Assembling every site's message from the same
// four parts is what makes learning it once enough.
type Override struct {
	// Name is the switch as its caller writes it: "magus vcs add --untracked",
	// "cache.remote.insecure".
	Name string
	// Silences completes "<Name> ...", saying what stops being checked.
	Silences string
	// Spelling is the whole accepted form, copyable as written. A scripted
	// invocation this refusal just broke needs the replacement exactly, not
	// described.
	Spelling string
	// Records completes "The reason ...", naming where the prose is kept. A reason
	// demanded and then dropped is a form field.
	Records string
}

// RequireReason refuses an override switched on with no prose behind it, and returns
// nil for one that carries prose or for an override nobody asked for.
//
// The rule is the one skip_cache, drift and spells.allow_shadow already carry into
// the magusfile and magus.yaml: switching off a check that protects everyone
// downstream is a claim about this workspace, not a preference, and prose is the only
// thing separating a considered exemption from a workaround nobody came back to. It
// binds where the decision outlives the run. A per-run judgment such as --no-cache is
// deliberately not one of these, because prose demanded daily becomes a form field.
func RequireReason(o Override, on bool, reason string) error {
	if !on || strings.TrimSpace(reason) != "" {
		return nil
	}
	return fmt.Errorf("%s %s, so it needs a reason; write it as: %s. The reason %s",
		o.Name, o.Silences, o.Spelling, o.Records)
}
