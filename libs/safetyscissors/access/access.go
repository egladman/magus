package access

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// Scope is one contributor's declarations: the acts it introduces and its rules.
// Name, and each act, is lowercase words of letters and digits joined by single
// hyphens. A scope may permit only the acts it declares and may forbid any act declared
// by it or an earlier scope.
type Scope[P, T, D any] struct {
	Name  string
	Acts  []Act
	Rules []Rule[P, T, D]
}

// RuleSet is the acts and rules of every scope joined so far. It is immutable and safe
// for concurrent use when its conditions are. It knows no roles and no closed set of
// acts: those are attributes of the caller's types and the scopes it joins.
type RuleSet[P, T, D any] struct {
	scopes []string
	acts   map[Act]string
	owners map[string]string
	rules  []Rule[P, T, D]
}

// New builds a rule set from its first scope, refusing a scope [RuleSet.Extend] would
// refuse.
func New[P, T, D any](first Scope[P, T, D]) (*RuleSet[P, T, D], error) {
	return (&RuleSet[P, T, D]{}).Extend(first)
}

// Extend returns a copy of s with scope joined, leaving s unchanged. It is add-only and
// stops at the first refusal. Each refusal is a typed error in this package naming the
// scopes involved.
func (s *RuleSet[P, T, D]) Extend(scope Scope[P, T, D]) (*RuleSet[P, T, D], error) {
	if s == nil {
		s = &RuleSet[P, T, D]{}
	}
	if !isKebab(scope.Name) {
		return nil, &DeclarationError{Scope: scope.Name, Problem: "the scope name is not lowercase letters and digits joined by single hyphens"}
	}
	if slices.Contains(s.scopes, scope.Name) {
		return nil, &DuplicateScopeError{Scope: scope.Name}
	}
	acts := maps.Clone(s.acts)
	if acts == nil {
		acts = map[Act]string{}
	}
	for _, act := range scope.Acts {
		if !isKebab(string(act)) {
			return nil, &DeclarationError{Scope: scope.Name, Act: act, Problem: "the act name is not lowercase letters and digits joined by single hyphens"}
		}
		if by, ok := acts[act]; ok {
			return nil, &RedeclaredActError{Act: act, Scope: scope.Name, DeclaredBy: by}
		}
		acts[act] = scope.Name
	}
	owners := maps.Clone(s.owners)
	if owners == nil {
		owners = map[string]string{}
	}
	rules := slices.Clone(s.rules)
	for _, r := range scope.Rules {
		if err := r.validate(scope.Name); err != nil {
			return nil, err
		}
		if by, ok := owners[r.name]; ok {
			return nil, &DuplicateRuleError{Rule: r.name, Scope: scope.Name, DeclaredBy: by}
		}
		by, ok := acts[r.act]
		if !ok {
			return nil, &UndeclaredActError{Act: r.act, Rule: r.name, Scope: scope.Name}
		}
		if r.permits && by != scope.Name {
			return nil, &ForeignPermitError{Rule: r.name, Act: r.act, Scope: scope.Name, DeclaredBy: by}
		}
		r.scope = scope.Name
		owners[r.name] = scope.Name
		rules = append(rules, r)
	}
	return &RuleSet[P, T, D]{
		scopes: append(slices.Clone(s.scopes), scope.Name),
		acts:   acts,
		owners: owners,
		rules:  rules,
	}, nil
}

// Rules lists every rule in declaration order, scope by scope, as a fresh slice.
func (s *RuleSet[P, T, D]) Rules() []Rule[P, T, D] {
	if s == nil {
		return nil
	}
	return slices.Clone(s.rules)
}

// Judge answers r, never with a yes. An undeclared act or a failing condition errors
// and comes with a deny. A holding deny-forbid denies. An act no permit allows is
// denied, quoting each of its permits; an ask-forbid cannot soften that. Otherwise the
// answer is the stricter holding ask-forbid, or the zero verdict, which defers.
func (s *RuleSet[P, T, D]) Judge(ctx context.Context, r Request[P, T, D]) (verdict.Verdict, error) {
	if s == nil {
		s = &RuleSet[P, T, D]{}
	}
	if _, ok := s.acts[r.Act]; !ok {
		err := &UndeclaredActError{Act: r.Act}
		return verdict.Verdict{Decision: verdict.Deny, Reason: err.Error()}, err
	}
	var forbidden verdict.Verdict
	for _, rule := range s.rules {
		if rule.permits || rule.act != r.Act {
			continue
		}
		holds, err := s.holds(ctx, rule, r)
		if err != nil {
			return refused(rule, err), err
		}
		if !holds {
			continue
		}
		v := verdict.Verdict{Decision: rule.decision, Reason: rule.description, Rule: rule.name}
		if rule.decision == verdict.Deny {
			return v, nil
		}
		forbidden = verdict.Stricter(forbidden, v)
	}
	permitted, err := s.permitted(ctx, r)
	if err != nil {
		return permitted, err
	}
	return verdict.Stricter(permitted, forbidden), nil
}

// permitted is the zero verdict when a permit for r's act holds, and otherwise a deny
// listing every permit for it.
func (s *RuleSet[P, T, D]) permitted(ctx context.Context, r Request[P, T, D]) (verdict.Verdict, error) {
	var lines []string
	for _, rule := range s.rules {
		if !rule.permits || rule.act != r.Act {
			continue
		}
		holds, err := s.holds(ctx, rule, r)
		if err != nil {
			return refused(rule, err), err
		}
		if holds {
			return verdict.Verdict{}, nil
		}
		lines = append(lines, fmt.Sprintf("  %s: %s", rule.name, rule.description))
	}
	if len(lines) == 0 {
		return verdict.Verdict{Decision: verdict.Deny, Reason: fmt.Sprintf("access: no rule permits %s", r.Act)}, nil
	}
	reason := fmt.Sprintf("access: nothing permits this %s. One of these must hold:\n%s", r.Act, strings.Join(lines, "\n"))
	return verdict.Verdict{Decision: verdict.Deny, Reason: reason}, nil
}

func (s *RuleSet[P, T, D]) holds(ctx context.Context, rule Rule[P, T, D], r Request[P, T, D]) (bool, error) {
	holds, err := rule.when(ctx, r)
	if err != nil {
		return false, &ConditionError{Rule: rule.name, Scope: rule.scope, Err: err}
	}
	return holds, nil
}

func refused[P, T, D any](rule Rule[P, T, D], err error) verdict.Verdict {
	return verdict.Verdict{Decision: verdict.Deny, Reason: err.Error(), Rule: rule.name}
}

// UndeclaredActError is an act no scope declared, met by [RuleSet.Judge] or by a rule
// joining the rule set. Rule and Scope name that rule, and are empty from Judge.
type UndeclaredActError struct {
	Act   Act
	Rule  string
	Scope string
}

func (e *UndeclaredActError) Error() string {
	if e.Rule == "" {
		return fmt.Sprintf("access: no scope declares the act %q", e.Act)
	}
	return fmt.Sprintf("access: scope %q: rule %q judges the act %q, which no scope declares", e.Scope, e.Rule, e.Act)
}

// RedeclaredActError is an act Scope declares that DeclaredBy already declared.
// DeclaredBy equals Scope when one scope lists an act twice.
type RedeclaredActError struct {
	Act        Act
	Scope      string
	DeclaredBy string
}

func (e *RedeclaredActError) Error() string {
	return fmt.Sprintf("access: scope %q declares the act %q, which scope %q already declared", e.Scope, e.Act, e.DeclaredBy)
}

// DuplicateRuleError is a rule name Scope reuses from DeclaredBy.
type DuplicateRuleError struct {
	Rule       string
	Scope      string
	DeclaredBy string
}

func (e *DuplicateRuleError) Error() string {
	return fmt.Sprintf("access: scope %q names a rule %q, which scope %q already named", e.Scope, e.Rule, e.DeclaredBy)
}

// DuplicateScopeError is a scope name the rule set already holds.
type DuplicateScopeError struct {
	Scope string
}

func (e *DuplicateScopeError) Error() string {
	return fmt.Sprintf("access: the rule set already holds a scope %q", e.Scope)
}

// ForeignPermitError is a permit Scope adds for an act DeclaredBy declared. Only an
// act's own scope may permit it; any scope may forbid it.
type ForeignPermitError struct {
	Rule       string
	Act        Act
	Scope      string
	DeclaredBy string
}

func (e *ForeignPermitError) Error() string {
	return fmt.Sprintf("access: scope %q: rule %q permits the act %q, which scope %q declared", e.Scope, e.Rule, e.Act, e.DeclaredBy)
}

// DeclarationError is a scope, act or rule refused on its own terms. That is a bad
// name, an empty description, a nil condition, or a forbid answering neither deny nor
// ask. Err is the [*verdict.RuleNameError] for a refused rule name, and nil otherwise.
type DeclarationError struct {
	Scope   string
	Act     Act
	Rule    string
	Problem string
	Err     error
}

func (e *DeclarationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "access: scope %q", e.Scope)
	if e.Act != "" {
		fmt.Fprintf(&b, ": act %q", e.Act)
	}
	if e.Rule != "" {
		fmt.Fprintf(&b, ": rule %q", e.Rule)
	}
	fmt.Fprintf(&b, ": %s", e.Problem)
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

func (e *DeclarationError) Unwrap() error { return e.Err }

// ConditionError is a condition that failed while judging. Err is the condition's own
// error.
type ConditionError struct {
	Rule  string
	Scope string
	Err   error
}

func (e *ConditionError) Error() string {
	return fmt.Sprintf("access: scope %q: rule %q could not be judged: %v", e.Scope, e.Rule, e.Err)
}

func (e *ConditionError) Unwrap() error { return e.Err }
