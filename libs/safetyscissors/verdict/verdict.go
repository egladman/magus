// Package verdict is what a rule answers about one call and what the judge answers
// about the call as a whole: a Verdict from each rule, merged by Stricter, and the
// Judgment a caller reads.
package verdict

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/internal/enum"
)

// Decision is what a rule answers. The zero value allows, so an empty Verdict{} is the
// pass a rule returns when it has nothing to say.
type Decision string

const (
	// Allow adds nothing to the built-in verdict.
	Allow Decision = "allow"
	// Advise lets the call through with context for the caller.
	Advise Decision = "advise"
	// Deny blocks the call.
	Deny Decision = "deny"
)

var decisions = enum.Set[Decision]{Allow, Advise, Deny}

// Values lists the decisions a rule may return, excluding the zero value.
func (d Decision) Values() []string { return decisions.Strings() }

// Valid reports whether d is a declared decision or unset.
func (d Decision) Valid() bool { return decisions.Valid(d) }

// String renders d for an error message: the value, or "unset" when empty.
func (d Decision) String() string { return enum.String(d) }

// MarshalText writes the name as given.
func (d Decision) MarshalText() ([]byte, error) { return []byte(d), nil }

// UnmarshalText sets d from a name, refusing one outside Values.
func (d *Decision) UnmarshalText(text []byte) error {
	v := Decision(text)
	if !v.Valid() {
		return fmt.Errorf("unknown guard decision %q (want one of %v)", text, v.Values())
	}
	*d = v
	return nil
}

// rank orders decisions by strictness, so merging two verdicts keeps the stricter. An
// undeclared decision ranks as deny: a verdict nobody can read must not pass as allow.
func (d Decision) rank() int {
	switch d {
	case "", Allow:
		return 0
	case Advise:
		return 1
	}
	return 2
}

// Verdict is what a rule returns. The zero value allows.
type Verdict struct {
	Decision Decision
	// Reason is shown to the caller: the refusal for a deny, the context for an advise.
	Reason string
	// Rule is the name the verdict reports under, so a trail, a repeated deny and a
	// report can tell one rule from another. "" reports under the seam that asked. See
	// ValidateRuleName.
	Rule string
}

// reservedRuleNames are the seams an unnamed verdict reports under; a rule named after
// one would read as an unnamed answer.
var reservedRuleNames = []string{"command", "spawn", "write", "stop", "step"}

// ruleName is a rule's name as written: lowercase words of letters and digits joined by
// single hyphens. It is a label, never normalized, so "rule2" stands as written.
var ruleName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateRuleName refuses a Verdict rule name that is not lowercase words joined by
// hyphens, or that names a seam. The refusal suggests the kebab-case spelling.
func ValidateRuleName(name string) error {
	if name == "" {
		return errors.New("guard rule name is empty")
	}
	if !ruleName.MatchString(name) {
		return fmt.Errorf("guard rule name %q is not lowercase letters and digits joined by single hyphens, as in %q", name, kebabCase(name))
	}
	if slices.Contains(reservedRuleNames, name) {
		return fmt.Errorf("guard rule name %q is reserved: an unnamed %s rule reports under it", name, name)
	}
	return nil
}

// Stricter merges two verdicts on one call, keeping the stricter decision and its rule:
// deny over advise over allow. When both carry the same decision their reasons are both
// kept, once each, so two rules that agree on a deny still explain themselves; the merge
// reports under a's rule, or b's when a names none, since one verdict reports under one
// rule.
func Stricter(a, b Verdict) Verdict {
	switch {
	case a.Decision.rank() > b.Decision.rank():
		return a
	case b.Decision.rank() > a.Decision.rank():
		return b
	}
	var reasons []string
	for _, r := range []string{a.Reason, b.Reason} {
		if r = strings.TrimSpace(r); r != "" && !slices.Contains(reasons, r) {
			reasons = append(reasons, r)
		}
	}
	rule := a.Rule
	if rule == "" {
		rule = b.Rule
	}
	return Verdict{Decision: a.Decision, Reason: strings.Join(reasons, "\n\n"), Rule: rule}
}

// kebabSplitWord and kebabSplitNumberLetter copy magus's types.Normalize, which this
// module may not import, so a refused name suggests the spelling magus would.
var (
	kebabSplitWord         = regexp.MustCompile(`([a-z])([A-Z0-9])|([a-zA-Z])([0-9])|([0-9])([a-zA-Z])|([A-Z])([A-Z])([a-z])`)
	kebabSplitNumberLetter = regexp.MustCompile(`([0-9])([a-zA-Z])`)
)

// kebabCase lowercases s and inserts '-' at camelCase and letter/digit boundaries,
// collapsing every non-alphanumeric run to a single '-'.
func kebabCase(s string) string {
	s = kebabSplitWord.ReplaceAllString(s, `$1$3$5$7 $2$4$6$8$9`)
	s = kebabSplitNumberLetter.ReplaceAllString(s, "$1 $2")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.ToLower(strings.Join(strings.Fields(b.String()), "-"))
}
