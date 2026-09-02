package ward

import (
	"fmt"
	"strings"
)

// Override is one switch that turns off a mechanism magus runs to catch mistakes.
//
// The fields are prose because a refusal has to teach the caller the form that
// satisfies it. Every site builds its message from the same four, so a caller who has
// met one refusal has met them all.
type Override struct {
	// Name is the switch as its caller writes it: "magus vcs add --untracked",
	// "cache.remote.insecure".
	Name string
	// Silences completes "<Name> ...", saying what stops being checked.
	Silences string
	// Spelling is the whole accepted form, copyable as written. A script this refusal
	// breaks needs the replacement in full.
	Spelling string
	// Records completes "The reason ...", naming where the prose is kept. A reason
	// nobody keeps is a form field.
	Records string
}

// RequireReason refuses an override switched on with no prose behind it. It returns
// nil when prose was given, and when the override was never asked for.
//
// It carries into the CLI and magus.yaml the rule skip_cache, drift and
// spells.allow_shadow already carry into the magusfile: switching off a check that
// protects everyone downstream is a claim about this workspace, and prose is what
// separates a considered exemption from a workaround nobody came back to. The rule
// binds where the decision outlives the run. A per-run judgment such as --no-cache is
// left alone, because prose demanded daily becomes a form field.
func RequireReason(o Override, on bool, reason string) error {
	if !on || strings.TrimSpace(reason) != "" {
		return nil
	}
	return fmt.Errorf("%s %s. Say why: %s. The reason %s",
		o.Name, o.Silences, o.Spelling, o.Records)
}
