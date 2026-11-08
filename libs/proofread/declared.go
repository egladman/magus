package proofread

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// KindDecl is one kind a decisions table's "kinds" object declares, as the
// table writes it. It is a fact, never an algorithm: it picks a reader, the
// rules that judge the kind and their decisions, and the spellings it
// replaces. New judging behavior is a Go rule.
type KindDecl struct {
	// Extends is the built-in kind the declared one starts from: its reader,
	// its rules at their defaults, and its facts.
	Extends Kind `json:"extends"`
	// Reader replaces the base's reader whole when set.
	Reader *Reader `json:"reader"`
	// Rules sets each named rule's default on the kind, adding a rule the base
	// does not judge.
	Rules map[string]string `json:"rules"`
	// Weights sets a rule's grade weight, from 0 to 1, in place of its
	// measured one.
	Weights map[string]float64 `json:"weights"`
	// Minimum is the grade, 0 to 100, a text of the kind must reach.
	Minimum *int `json:"minimum"`
	// Terms maps each spelling the kind replaces to its replacement. Setting
	// any turns [RuleTerms] on at deny unless Rules names it.
	Terms map[string]string `json:"terms"`
}

// DeclareKinds resolves a decisions table's declared kinds into specs, sorted
// by name, for [WithKinds]. Each error names the field at fault. A built-in
// name, an unknown base, rule, format or decision is an error. So is a rule
// the kind's format cannot feed, or a weight or minimum out of range.
func DeclareKinds(decls map[string]KindDecl) ([]KindSpec, error) {
	out := make([]KindSpec, 0, len(decls))

	for _, name := range slices.Sorted(maps.Keys(decls)) {
		s, err := declareKind(name, decls[name])
		if fe := (fieldError{}); errors.As(err, &fe) {
			fe.field = "kinds." + name + "." + fe.field

			return nil, fe
		} else if err != nil {
			return nil, fmt.Errorf("kinds.%s: %w", name, err)
		}

		out = append(out, s)
	}

	return out, nil
}

// fieldError is an error at field of a declared kind, which [DeclareKinds]
// prefixes with the kind's own path.
type fieldError struct {
	field string
	err   error
}

func (e fieldError) Error() string { return e.field + ": " + e.err.Error() }
func (e fieldError) Unwrap() error { return e.err }

func at(field, format string, args ...any) error {
	return fieldError{field, fmt.Errorf(format, args...)}
}

func declareKind(name string, d KindDecl) (KindSpec, error) {
	if name == "" || strings.ContainsAny(name, " \t\n/") {
		return KindSpec{}, at("name", "a kind's name is one word, not %q", name)
	}

	if _, ok := ParseKind(name); ok {
		return KindSpec{}, at("name", "%q is a built-in kind: give the declared one its own name", name)
	}

	s, err := extend(d.Extends)
	if err != nil {
		return KindSpec{}, err
	}

	s.Name, s.declared = Kind(name), true

	switch {
	case d.Reader != nil:
		s.Reader = *d.Reader
	case d.Extends == "":
		return KindSpec{}, at("reader", "set a reader, or extend a kind for its reader")
	}

	for _, step := range []func() error{
		func() error { return checkReader(s.Reader) },
		func() error { return s.setRules(d.Rules) },
		func() error { return s.setTerms(d.Terms, d.Rules) },
		s.checkFormat,
		func() error { return s.setWeights(d.Weights) },
		func() error { return s.setMinimum(d.Minimum) },
	} {
		if err := step(); err != nil {
			return KindSpec{}, err
		}
	}

	return s, nil
}

// extend returns the spec a declared kind starts from: base's, or an empty
// one for no base. The base's rules come over at the defaults they run at,
// since a declared kind's decisions are not [unmeasured]'s to turn off.
func extend(base Kind) (KindSpec, error) {
	if base == "" {
		return KindSpec{Rules: map[Rule]Decision{}}, nil
	}

	b, ok := builtins[base]
	if !ok {
		return KindSpec{}, at("extends", "unknown kind %q: want one of %s", base, kindList(kinds))
	}

	s := b.clone()
	s.Extends = b.Name

	for _, c := range checks {
		if b.judges(c.rule) {
			s.Rules[c.rule] = c.defaultDecision(b)
		}
	}

	return s, nil
}

func (s *KindSpec) setMinimum(m *int) error {
	if m == nil {
		return nil
	}

	if *m < 0 || *m > 100 {
		return at("minimum", "want a grade from 0 to 100, not %d", *m)
	}

	v := *m
	s.Minimum = &v

	return nil
}

// checkReader refuses a format proofread has no reader for, and a Markdown
// property set on a reader that reads no Markdown.
func checkReader(r Reader) error {
	if !slices.Contains(formats, r.Format) {
		return at("reader.format", "unknown format %q: want one of %s", r.Format, formatList(formats))
	}

	if r.Format == FormatMarkdown {
		if r.Subject && !r.Titled {
			return at("reader.subject", "a subject line is a title: set titled too")
		}

		return nil
	}

	set := []struct {
		field string
		on    bool
	}{
		{"titled", r.Titled}, {"headings", r.Headings}, {"sections", r.Sections},
		{"front_matter", r.FrontMatter}, {"quotes", r.Quotes}, {"subject", r.Subject},
	}

	for _, p := range set {
		if p.on {
			return at("reader."+p.field, "the %s format reads no Markdown, so it takes no %s", r.Format, p.field)
		}
	}

	return nil
}

func (s *KindSpec) setRules(in map[string]string) error {
	known := Rules()

	for _, name := range slices.Sorted(maps.Keys(in)) {
		r := Rule(name)
		if !slices.Contains(known, r) {
			return at("rules."+name, "unknown rule: `proofread rules` lists every rule")
		}

		var d Decision
		if err := d.UnmarshalText([]byte(in[name])); err != nil || d == 0 {
			return at("rules."+name, "unknown decision %q: want off, advise or deny", in[name])
		}

		s.Rules[r] = d
	}

	return nil
}

// setTerms adds in to s's terms and turns [RuleTerms] on to report them,
// unless the declaration's own rules name it.
func (s *KindSpec) setTerms(in, declared map[string]string) error {
	if len(in) == 0 {
		return nil
	}

	if s.Terms == nil {
		s.Terms = map[string]string{}
	}

	for _, from := range slices.Sorted(maps.Keys(in)) {
		if strings.TrimSpace(from) == "" {
			return at("terms", "a term to replace is empty")
		}

		s.Terms[from] = in[from]
	}

	switch d, named := declared[string(RuleTerms)]; {
	case !named:
		s.Rules[RuleTerms] = DecisionDeny
	case d == DecisionOff.String():
		return at("terms", "set, but rules.terms is off, so nothing would report them")
	}

	return nil
}

// checkFormat refuses a rule s's format cannot feed, naming where it came
// from.
func (s *KindSpec) checkFormat() error {
	for _, c := range checks {
		if !s.judges(c.rule) || c.canRead(s.Reader.Format) {
			continue
		}

		from := "rules." + string(c.rule)
		if s.Extends != "" && builtins[s.Extends].judges(c.rule) {
			from = "extends"
		}

		return at(from, "rule %s reads %s, not the %s format", c.rule, formatList(c.reads), s.Reader.Format)
	}

	return nil
}

func (s *KindSpec) setWeights(in map[string]float64) error {
	for _, name := range slices.Sorted(maps.Keys(in)) {
		r, w := Rule(name), in[name]

		switch {
		case !slices.Contains(Rules(), r):
			return at("weights."+name, "unknown rule: `proofread rules` lists every rule")
		case !s.judges(r):
			return at("weights."+name, "the kind does not judge %s: name it in rules first", r)
		case w < 0 || w > 1:
			return at("weights."+name, "want a weight from 0 to 1, not %v", w)
		}

		if s.Weights == nil {
			s.Weights = map[Rule]float64{}
		}

		s.Weights[r] = w
	}

	return nil
}

func formatList(fs []Format) string {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = string(f)
	}

	return strings.Join(names, ", ")
}

// WithKinds lets [JudgeText] judge the kinds [DeclareKinds] resolved, by
// name, after the built-ins. A spec built by hand is judged as written, and
// one whose format has no reader panics.
func WithKinds(specs ...KindSpec) Option {
	return func(o *options) { o.kinds = append(o.kinds, specs...) }
}
