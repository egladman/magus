package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// Act names what a principal asks to do, such as "refund" or "publish". A scope
// declares it, in lowercase words of letters and digits joined by single hyphens.
type Act string

// Request is one question put to a [RuleSet]: may Principal do Act to Target, with
// Detail? All three are the caller's own attribute types.
type Request[P, T, D any] struct {
	Principal P
	Act       Act
	Target    T
	Detail    D
}

// When is a rule's condition. It reports whether the rule applies to the request; an
// error makes [RuleSet.Judge] deny.
type When[P, T, D any] func(context.Context, Request[P, T, D]) (bool, error)

// Rule permits or forbids one act when its condition holds. Build one with [Permit] or
// [Forbid]; [New] and [RuleSet.Extend] validate it.
type Rule[P, T, D any] struct {
	name        string
	act         Act
	description string
	decision    verdict.Decision
	permits     bool
	when        When[P, T, D]
	scope       string
}

// Permit is a rule that allows act when when holds. description is the condition in one
// sentence, quoted when the act is denied for want of a permit.
func Permit[P, T, D any](name string, act Act, description string, when When[P, T, D]) Rule[P, T, D] {
	return Rule[P, T, D]{name: name, act: act, description: description, permits: true, when: when}
}

// Forbid is a rule that answers decision, deny or ask, for act when when holds.
// description is the reason the verdict carries.
func Forbid[P, T, D any](name string, act Act, decision verdict.Decision, description string, when When[P, T, D]) Rule[P, T, D] {
	return Rule[P, T, D]{name: name, act: act, description: description, decision: decision, when: when}
}

// Name is the rule's name, which its verdicts report under.
func (r Rule[P, T, D]) Name() string { return r.name }

// Act is the act the rule judges.
func (r Rule[P, T, D]) Act() Act { return r.act }

// Description is the rule's one-line condition as written.
func (r Rule[P, T, D]) Description() string { return r.description }

// Decision is a forbid's answer, deny or ask, and unset on a permit.
func (r Rule[P, T, D]) Decision() verdict.Decision { return r.decision }

// Permits reports whether the rule is a permit rather than a forbid.
func (r Rule[P, T, D]) Permits() bool { return r.permits }

// Scope names the scope that declared the rule, or "" before a rule set holds it.
func (r Rule[P, T, D]) Scope() string { return r.scope }

// validate checks what a rule says about itself, apart from the rule set it joins.
func (r Rule[P, T, D]) validate(scope string) error {
	if err := verdict.ValidateRuleName(r.name); err != nil {
		return &DeclarationError{Scope: scope, Rule: r.name, Problem: "the rule name is refused", Err: err}
	}
	problem := ""
	switch {
	case strings.TrimSpace(r.description) == "":
		problem = "the description is empty"
	case r.when == nil:
		problem = "the condition is nil"
	case !r.permits && r.decision != verdict.Deny && r.decision != verdict.Ask:
		problem = fmt.Sprintf("a forbid answers deny or ask, not %s", r.decision)
	}
	if problem != "" {
		return &DeclarationError{Scope: scope, Rule: r.name, Problem: problem}
	}
	return nil
}

// isKebab reports whether name is lowercase words of letters and digits joined by
// single hyphens. verdict.ValidateRuleName also refuses the guard's seam names, which
// an act such as "write" may take.
func isKebab(name string) bool {
	return name != "" &&
		strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") == "" &&
		!strings.HasPrefix(name, "-") && !strings.HasSuffix(name, "-") &&
		!strings.Contains(name, "--")
}
