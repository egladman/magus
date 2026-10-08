// Package builtin holds the decision each compiled guard rule takes when the workspace
// declares nothing for it, and resolves what a workspace does declare against that set.
//
// It is a leaf (stdlib and types only) so the guard, the Buzz binding and the CLI can all
// read one table without importing the guard engine.
package builtin

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

// Decision is what a built-in rule does when it matches: say nothing, explain, or refuse.
type Decision string

// The decisions, least to most refusing.
const (
	Off    Decision = "off"
	Advise Decision = "advise"
	Deny   Decision = "deny"
)

// Setting is one rule's resolved configuration. Lines is read-navigation's size of a whole
// read worth flagging; 0 means unset, and the binary ships no number for it.
type Setting struct {
	Decision Decision
	Lines    int
}

const readNavigation = "read-navigation"

// defaults must name exactly the rules internal/guard catalogs. A rule that refuses
// something the caller can recover from by running a different command advises, so
// a workspace that wants it refused says so; a rule guarding something no later
// command undoes, or a boundary a lease or person drew, denies.
var defaults = map[string]Decision{
	// Recoverable: the served command answers what the refused one asked.
	"brief-command":        Advise,
	"busy-wait":            Advise,
	"buzz-unbriefed":       Advise,
	"chained-run":          Advise,
	"exit-status-echo":     Advise,
	"filter-without-input": Advise,
	"grep-reader":          Advise,
	"interpreter-rewrite":  Advise,
	"magus-timeout":        Advise,
	"output-pipe":          Advise,
	"output-redirect":      Advise,
	"process-poll":         Advise,
	"raw-tool":             Advise,
	readNavigation:         Advise,
	"scripted-rewrite":     Advise,
	"search-translation":   Advise,
	"sed-in-place":         Advise,
	"sibling-checkout":     Advise,
	"spawn-unbriefed":      Advise,
	"stage-all":            Advise,
	"symbol-search":        Advise,
	"throwaway-copy":       Advise,
	"unknown-env":          Advise,

	// Irreversible, or a boundary someone else drew.
	"agent-sign-off":        Deny,
	"backtick-substitution": Deny,
	"cache-dir-write":       Deny,
	"claimed-declaration":   Deny,
	"credential-verb":       Deny,
	"focus-read":            Deny,
	"hook-wiring-write":     Deny,
	"inline-alias":          Deny,
	"lease-gate":            Deny,
	"lease-harness":         Deny,
	"lease-rebind":          Deny,
	"lease-undeclared":      Deny,
	"lease-vcs":             Deny,
	"lease-write":           Deny,
	"merge-side-checkout":   Deny,
	"notes-author":          Deny,
	"push-ungated":          Deny,
	"shared-stash":          Deny,
	"token-state":           Deny,
	"vcs-off-switch":        Deny,
	"whole-tree":            Deny,
	"worker-check-only":     Deny,
	"worktree-remove":       Deny,

	// Advisories never refused anything, so they keep advising; a workspace may still
	// raise one to deny or turn it off. lease-state, lease-invalid and lease-terminal are
	// advisories, not the lease-* denies above.
	"capture-filter":     Advise,
	"checkpoint-state":   Advise,
	"dependency-install": Advise,
	"dependency-update":  Advise,
	"echo-on-success":    Advise,
	"focus":              Advise,
	"gate-repeat":        Advise,
	"generated-write":    Advise,
	"graph-pipe":         Advise,
	"graph-stale":        Advise,
	"hook-wiring":        Advise,
	"installed-skill":    Advise,
	"instruction-write":  Advise,
	"lease-invalid":      Advise,
	"lease-state":        Advise,
	"lease-terminal":     Advise,
	"leased-path":        Advise,
	"new-file":           Advise,
	"new-source-dir":     Advise,
	"precedent-search":   Advise,
	"push-gate":          Advise,
	"read-symbol":        Advise,
	"regen-source":       Advise,
	"revert-classify":    Advise,
	"scope-drift":        Advise,
	"shared-checkout":    Advise,
	"skill-source":       Advise,
	"source-read":        Advise,
	"split-run":          Advise,
	"stage-classify":     Advise,
	"stdin-closed":       Advise,
	"timed-magus":        Advise,
	"unleased-write":     Advise,
}

// Defaults returns every compiled rule's name mapped to the decision it takes when the
// workspace declares nothing for it. The map is the caller's to modify.
func Defaults() map[string]Decision {
	return maps.Clone(defaults)
}

// Resolve returns a setting for every compiled rule: the declared one where the workspace
// declared it, the default otherwise. Every misdeclaration is reported at once, each as an
// MGS1045 error: a name no rule has (naming the nearest one), a decision other than off,
// advise or deny, Lines on a rule that takes no such parameter, or Lines below 1.
func Resolve(declared map[string]Setting) (map[string]Setting, error) {
	out := make(map[string]Setting, len(defaults))
	for name, d := range defaults {
		out[name] = Setting{Decision: d}
	}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		s := declared[name]
		if err := validate(name, s); err != nil {
			errs = append(errs, err)
			continue
		}
		out[name] = s
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func validate(name string, s Setting) error {
	if _, ok := defaults[name]; !ok {
		if near := nearest(name); near != "" {
			return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				"unknown built-in rule %q; did you mean %q?", name, near)
		}
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"unknown built-in rule %q; `magus describe rules` lists every name", name)
	}
	if rank(s.Decision) < 0 {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q: decision %q is not off, advise or deny", name, s.Decision)
	}
	if s.Lines != 0 && name != readNavigation {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q takes no lines parameter; only %s does", name, readNavigation)
	}
	if s.Lines < 0 {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q: lines is %d; give 1 or more, or leave it out", name, s.Lines)
	}
	return nil
}

// Stricter returns whichever of a and b refuses more, ranking off < advise < deny. At the
// same decision the smaller Lines wins, and an unset Lines beats any set one: unset
// judges every whole read rather than only the long ones.
func Stricter(a, b Setting) Setting {
	ra, rb := rank(a.Decision), rank(b.Decision)
	switch {
	case ra > rb:
		return a
	case rb > ra:
		return b
	case a.Lines != 0 && (b.Lines == 0 || b.Lines < a.Lines):
		return b
	default:
		return a
	}
}

// rank orders decisions by how much they refuse; an unknown one ranks below off.
func rank(d Decision) int {
	return slices.Index([]Decision{Off, Advise, Deny}, d)
}

// nearest is the rule name within a typo's reach of typed, or "". The tolerance matches
// internal/hint.Nearest, which this leaf cannot import.
func nearest(typed string) string {
	limit := 2
	if utf8.RuneCountInString(typed) >= 8 {
		limit = 3
	}
	folded := strings.ToLower(typed)
	best, bestDist := "", limit+1
	for _, name := range slices.Sorted(maps.Keys(defaults)) {
		if d := distance(folded, name); d < bestDist {
			best, bestDist = name, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b, counted in runes.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
