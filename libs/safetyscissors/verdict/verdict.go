// Package verdict is what a rule answers about one call and what the judge answers
// about the call as a whole: a Verdict from each rule, merged by Stricter, and the
// Judgment a caller reads.
package verdict

import (
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

func decisions() enum.Set[Decision] { return enum.Set[Decision]{Allow, Advise, Deny} }

// Values lists the decisions a rule may return, excluding the zero value.
func (d Decision) Values() []string { return decisions().Strings() }

// Valid reports whether d is a declared decision or unset.
func (d Decision) Valid() bool { return decisions().Valid(d) }

// String renders d for an error message: the value, or "unset" when empty.
func (d Decision) String() string { return enum.String(d) }

// MarshalText writes the name as given.
func (d Decision) MarshalText() ([]byte, error) { return []byte(d), nil }

// UnmarshalText sets d from a name, refusing one outside Values with an
// [*UnknownDecisionError].
func (d *Decision) UnmarshalText(text []byte) error {
	v := Decision(text)
	if !v.Valid() {
		return &UnknownDecisionError{Decision: string(text)}
	}
	*d = v
	return nil
}

// UnknownDecisionError is a decision name outside [Decision.Values].
type UnknownDecisionError struct {
	Decision string
}

func (e *UnknownDecisionError) Error() string {
	return fmt.Sprintf("verdict: unknown guard decision %q (want one of %v)", e.Decision, Decision("").Values())
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
func reservedRuleNames() []string { return []string{"command", "spawn", "write", "stop", "step"} }

// isRuleName reports whether name is a rule's name as written: lowercase words of
// letters and digits joined by single hyphens. It is a label, never normalized, so
// "rule2" stands as written.
func isRuleName(name string) bool {
	for word := range strings.SplitSeq(name, "-") {
		if word == "" {
			return false
		}
		for i := 0; i < len(word); i++ {
			if c := word[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

// RuleNameError is a rule name [ValidateRuleName] refuses. Suggestion is the kebab-case
// spelling when the name is not one, and Reserved is set when it names a seam.
type RuleNameError struct {
	Name       string
	Suggestion string
	Reserved   bool
}

func (e *RuleNameError) Error() string {
	switch {
	case e.Name == "":
		return "verdict: guard rule name is empty"
	case e.Reserved:
		return fmt.Sprintf("verdict: guard rule name %q is reserved: an unnamed %s rule reports under it", e.Name, e.Name)
	}
	return fmt.Sprintf("verdict: guard rule name %q is not lowercase letters and digits joined by single hyphens, as in %q", e.Name, e.Suggestion)
}

// ValidateRuleName refuses a Verdict rule name that is not lowercase words joined by
// hyphens, or that names a seam, with a [*RuleNameError]. The refusal suggests the
// kebab-case spelling.
func ValidateRuleName(name string) error {
	switch {
	case name == "":
		return &RuleNameError{}
	case !isRuleName(name):
		return &RuleNameError{Name: name, Suggestion: kebabCase(name)}
	case slices.Contains(reservedRuleNames(), name):
		return &RuleNameError{Name: name, Reserved: true}
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

// kebabCase lowercases s and inserts '-' at camelCase and letter/digit boundaries,
// collapsing every non-alphanumeric run to a single '-'. Its two patterns copy magus's
// types.Normalize, which this module may not import, so a refused name suggests the
// spelling magus would. They compile per call: only a refusal reaches here.
func kebabCase(s string) string {
	splitWord := regexp.MustCompile(`([a-z])([A-Z0-9])|([a-zA-Z])([0-9])|([0-9])([a-zA-Z])|([A-Z])([A-Z])([a-z])`)
	splitNumberLetter := regexp.MustCompile(`([0-9])([a-zA-Z])`)
	s = splitWord.ReplaceAllString(s, `$1$3$5$7 $2$4$6$8$9`)
	s = splitNumberLetter.ReplaceAllString(s, "$1 $2")
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
