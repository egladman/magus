package access

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// Bundle is one contributor's declarations: the acts it introduces and its rules.
// Name, and each act, is lowercase words of letters and digits joined by single
// hyphens. A bundle may permit only the acts it declares and may forbid any act declared
// by it or an earlier bundle.
type Bundle[P, T, D any] struct {
	Name  string
	Acts  []Act
	Rules []Rule[P, T, D]
}

// Table is the acts and rules of every bundle joined so far. It is immutable and safe
// for concurrent use when its conditions are. It knows no roles and no closed set of
// acts: those are attributes of the caller's types and the bundles it joins.
type Table[P, T, D any] struct {
	bundles []string
	acts    map[Act]string
	owners  map[string]string
	rules   []Rule[P, T, D]
}

// New builds a table from its first bundle, refusing a bundle [Table.Extend] would
// refuse.
func New[P, T, D any](first Bundle[P, T, D]) (*Table[P, T, D], error) {
	return (&Table[P, T, D]{}).Extend(first)
}

// Extend returns a copy of t with b joined, leaving t unchanged. It is add-only and
// stops at the first refusal. Each refusal is a typed error in this package naming the
// bundles involved.
func (t *Table[P, T, D]) Extend(b Bundle[P, T, D]) (*Table[P, T, D], error) {
	if t == nil {
		t = &Table[P, T, D]{}
	}
	if !isKebab(b.Name) {
		return nil, &DeclarationError{Bundle: b.Name, Problem: "the bundle name is not lowercase letters and digits joined by single hyphens"}
	}
	if slices.Contains(t.bundles, b.Name) {
		return nil, &DuplicateBundleError{Bundle: b.Name}
	}
	acts := maps.Clone(t.acts)
	if acts == nil {
		acts = map[Act]string{}
	}
	for _, act := range b.Acts {
		if !isKebab(string(act)) {
			return nil, &DeclarationError{Bundle: b.Name, Act: act, Problem: "the act name is not lowercase letters and digits joined by single hyphens"}
		}
		if by, ok := acts[act]; ok {
			return nil, &RedeclaredActError{Act: act, Bundle: b.Name, DeclaredBy: by}
		}
		acts[act] = b.Name
	}
	owners := maps.Clone(t.owners)
	if owners == nil {
		owners = map[string]string{}
	}
	rules := slices.Clone(t.rules)
	for _, r := range b.Rules {
		if err := r.validate(b.Name); err != nil {
			return nil, err
		}
		if by, ok := owners[r.name]; ok {
			return nil, &DuplicateRuleError{Rule: r.name, Bundle: b.Name, DeclaredBy: by}
		}
		by, ok := acts[r.act]
		if !ok {
			return nil, &UndeclaredActError{Act: r.act, Rule: r.name, Bundle: b.Name}
		}
		if r.permits && by != b.Name {
			return nil, &ForeignPermitError{Rule: r.name, Act: r.act, Bundle: b.Name, DeclaredBy: by}
		}
		r.bundle = b.Name
		owners[r.name] = b.Name
		rules = append(rules, r)
	}
	return &Table[P, T, D]{
		bundles: append(slices.Clone(t.bundles), b.Name),
		acts:    acts,
		owners:  owners,
		rules:   rules,
	}, nil
}

// Rules lists every rule in declaration order, bundle by bundle, as a fresh slice.
func (t *Table[P, T, D]) Rules() []Rule[P, T, D] {
	if t == nil {
		return nil
	}
	return slices.Clone(t.rules)
}

// Judge answers r, never with a yes. An undeclared act or a failing condition errors
// and comes with a deny. A holding deny-forbid denies. An act no permit allows is
// denied, quoting each of its permits; an ask-forbid cannot soften that. Otherwise the
// answer is the stricter holding ask-forbid, or the zero verdict, which defers.
func (t *Table[P, T, D]) Judge(ctx context.Context, r Request[P, T, D]) (verdict.Verdict, error) {
	if t == nil {
		t = &Table[P, T, D]{}
	}
	if _, ok := t.acts[r.Act]; !ok {
		err := &UndeclaredActError{Act: r.Act}
		return verdict.Verdict{Decision: verdict.Deny, Reason: err.Error()}, err
	}
	var forbidden verdict.Verdict
	for _, rule := range t.rules {
		if rule.permits || rule.act != r.Act {
			continue
		}
		holds, err := t.holds(ctx, rule, r)
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
	permitted, err := t.permitted(ctx, r)
	if err != nil {
		return permitted, err
	}
	return verdict.Stricter(permitted, forbidden), nil
}

// permitted is the zero verdict when a permit for r's act holds, and otherwise a deny
// listing every permit for it.
func (t *Table[P, T, D]) permitted(ctx context.Context, r Request[P, T, D]) (verdict.Verdict, error) {
	var lines []string
	for _, rule := range t.rules {
		if !rule.permits || rule.act != r.Act {
			continue
		}
		holds, err := t.holds(ctx, rule, r)
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

func (t *Table[P, T, D]) holds(ctx context.Context, rule Rule[P, T, D], r Request[P, T, D]) (bool, error) {
	holds, err := rule.when(ctx, r)
	if err != nil {
		return false, &ConditionError{Rule: rule.name, Bundle: rule.bundle, Err: err}
	}
	return holds, nil
}

func refused[P, T, D any](rule Rule[P, T, D], err error) verdict.Verdict {
	return verdict.Verdict{Decision: verdict.Deny, Reason: err.Error(), Rule: rule.name}
}

// UndeclaredActError is an act no bundle declared, met by [Table.Judge] or by a rule
// joining the table. Rule and Bundle name that rule, and are empty from Judge.
type UndeclaredActError struct {
	Act    Act
	Rule   string
	Bundle string
}

func (e *UndeclaredActError) Error() string {
	if e.Rule == "" {
		return fmt.Sprintf("access: no bundle declares the act %q", e.Act)
	}
	return fmt.Sprintf("access: bundle %q: rule %q judges the act %q, which no bundle declares", e.Bundle, e.Rule, e.Act)
}

// RedeclaredActError is an act Bundle declares that DeclaredBy already declared.
// DeclaredBy equals Bundle when one bundle lists an act twice.
type RedeclaredActError struct {
	Act        Act
	Bundle     string
	DeclaredBy string
}

func (e *RedeclaredActError) Error() string {
	return fmt.Sprintf("access: bundle %q declares the act %q, which bundle %q already declared", e.Bundle, e.Act, e.DeclaredBy)
}

// DuplicateRuleError is a rule name Bundle reuses from DeclaredBy.
type DuplicateRuleError struct {
	Rule       string
	Bundle     string
	DeclaredBy string
}

func (e *DuplicateRuleError) Error() string {
	return fmt.Sprintf("access: bundle %q names a rule %q, which bundle %q already named", e.Bundle, e.Rule, e.DeclaredBy)
}

// DuplicateBundleError is a bundle name the table already holds.
type DuplicateBundleError struct {
	Bundle string
}

func (e *DuplicateBundleError) Error() string {
	return fmt.Sprintf("access: the table already holds a bundle %q", e.Bundle)
}

// ForeignPermitError is a permit Bundle adds for an act DeclaredBy declared. Only an
// act's own bundle may permit it; any bundle may forbid it.
type ForeignPermitError struct {
	Rule       string
	Act        Act
	Bundle     string
	DeclaredBy string
}

func (e *ForeignPermitError) Error() string {
	return fmt.Sprintf("access: bundle %q: rule %q permits the act %q, which bundle %q declared",e.Bundle, e.Rule, e.Act, e.DeclaredBy)
}

// DeclarationError is a bundle, act or rule refused on its own terms. That is a bad
// name, an empty description, a nil condition, or a forbid answering neither deny nor
// ask. Err is the [*verdict.RuleNameError] for a refused rule name, and nil otherwise.
type DeclarationError struct {
	Bundle  string
	Act     Act
	Rule    string
	Problem string
	Err     error
}

func (e *DeclarationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "access: bundle %q", e.Bundle)
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
	Rule   string
	Bundle string
	Err    error
}

func (e *ConditionError) Error() string {
	return fmt.Sprintf("access: bundle %q: rule %q could not be judged: %v", e.Bundle, e.Rule, e.Err)
}

func (e *ConditionError) Unwrap() error { return e.Err }
