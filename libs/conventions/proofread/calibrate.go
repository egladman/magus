package proofread

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/tools/txtar"
)

// Label is what a labeled case says a rule does with its text. Hit and Pass
// are the verdicts the rule must reach. FalsePositive and FalseNegative are
// known misses, kept so precision and recall count them: the rule fires on a
// false positive and stays silent on a false negative, and a replay that sees
// otherwise reports the case, since the rule changed.
type Label string

const (
	LabelHit           Label = "hit"
	LabelPass          Label = "pass"
	LabelFalsePositive Label = "fp"
	LabelFalseNegative Label = "fn"
)

// fires reports whether a case with this label expects its rule to report.
func (l Label) fires() bool { return l == LabelHit || l == LabelFalsePositive }

// Case is one labeled text and the rule judging it.
type Case struct {
	// File and Line place the case's text in its case file, Line 1-based.
	File string
	Line int
	Rule Rule
	Kind Kind
	// Label is the verdict the case records.
	Label Label
	Text  string
	// Symbol names the declaration a doc-comment case documents; its Doc is
	// Text.
	Symbol Symbol
	// ThreadLength is the count of replies the author already posted, for a
	// review-reply case.
	ThreadLength int
}

//go:embed testdata/cases/*.txtar
var labeledCases embed.FS

// LabeledCases returns the case files this package carries, one per rule.
func LabeledCases() fs.FS {
	sub, err := fs.Sub(labeledCases, "testdata/cases")
	if err != nil {
		panic(err)
	}

	return sub
}

// caseLead is the title and opening paragraph a one-line change-description
// case is judged under, so the line reads as a bullet of a description with
// its goal stated.
const caseLead = "fix(cache): keep the key stable\n" +
	"A cached replay missed whenever two workers raced on the key, so the cache key now " +
	"sorts its inputs before hashing.\n\n"

// LoadCases reads every .txtar file at the root of fsys. A file is named for
// the rule its cases judge, <rule>.txtar, and its comment is free text. Each
// section header reads
//
//	<label>/<kind> [whole] [name=NAME] [owner=OWNER] [callable] [thread=N]
//
// where label is hit, pass, fp or fn and kind is one the rule judges. Each
// non-blank line of a section is one case; whole makes the section one case,
// for a text whose lines belong together. A one-line change-description case
// is judged as a bullet under a fixed title and opening paragraph; a whole one
// as written. name, owner and callable describe a doc-comment case's symbol,
// and thread sets a review-reply case's thread length.
//
// A file named for no rule, a header it cannot read, or a kind the rule does
// not judge is an error naming the file and line.
func LoadCases(fsys fs.FS) ([]Case, error) {
	names, err := fs.Glob(fsys, "*.txtar")
	if err != nil {
		return nil, err
	}

	var out []Case

	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}

		cases, err := parseCases(name, data)
		if err != nil {
			return nil, err
		}

		out = append(out, cases...)
	}

	return out, nil
}

func parseCases(name string, data []byte) ([]Case, error) {
	rule := Rule(strings.TrimSuffix(path.Base(name), ".txtar"))

	c, ok := checkFor(rule)
	if !ok {
		return nil, fmt.Errorf("%s: no rule is named %q", name, rule)
	}

	archive := txtar.Parse(data)
	line := 1 + strings.Count(string(archive.Comment), "\n")

	var out []Case

	for _, f := range archive.Files {
		header := line
		line++

		proto, whole, err := parseHeader(f.Name, c)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, header, err)
		}

		proto.File, proto.Rule = name, rule
		body := string(f.Data)

		switch {
		case whole:
			proto.Line, proto.Text = line, strings.TrimSuffix(body, "\n")
			out = append(out, proto)
		default:
			for i, text := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
				if strings.TrimSpace(text) == "" {
					continue
				}

				one := proto
				one.Line, one.Text = line+i, text

				if one.Kind == KindChangeDescription {
					one.Text = caseLead + text
				}

				out = append(out, one)
			}
		}

		line += strings.Count(body, "\n")
	}

	return out, nil
}

func parseHeader(header string, c check) (Case, bool, error) {
	fields := strings.Fields(header)
	if len(fields) == 0 {
		return Case{}, false, errors.New("a section needs a <label>/<kind> header")
	}

	label, kind, ok := strings.Cut(fields[0], "/")
	if !ok {
		return Case{}, false, fmt.Errorf("section %q needs a <label>/<kind> header", header)
	}

	out := Case{Label: Label(label), Kind: Kind(kind)}

	switch out.Label {
	case LabelHit, LabelPass, LabelFalsePositive, LabelFalseNegative:
	default:
		return Case{}, false, fmt.Errorf("label %q is none of hit, pass, fp or fn", label)
	}

	if !slices.Contains(c.on, out.Kind) {
		return Case{}, false, fmt.Errorf("rule %s does not judge kind %q", c.rule, kind)
	}

	whole := false

	for _, attr := range fields[1:] {
		key, value, _ := strings.Cut(attr, "=")

		switch key {
		case "whole":
			whole = true
		case "name":
			out.Symbol.Name = value
		case "owner":
			out.Symbol.Owner = value
		case "callable":
			out.Symbol.Callable = true
		case "thread":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return Case{}, false, fmt.Errorf("thread=%q is not a count", value)
			}

			out.ThreadLength = n
		default:
			return Case{}, false, fmt.Errorf("unknown attribute %q", attr)
		}
	}

	return out, whole, nil
}

func checkFor(rule Rule) (check, bool) {
	for _, c := range checks {
		if c.rule == rule {
			return c, true
		}
	}

	return check{}, false
}

// Fires reports whether the case's rule reports its text, judged with every
// rule at its default decision and the case's rule turned on where it is house
// style. The other rules run because a rule leaves a span to another that
// reports it.
func (c Case) Fires() bool {
	opts := []Option{WithThreadLength(c.ThreadLength)}

	if ch, ok := checkFor(c.Rule); ok && ch.defaultDecision(c.Kind) == DecisionOff {
		opts = append(opts, WithDecisions(map[Rule]Decision{c.Rule: DecisionDeny}))
	}

	var found []Finding

	if c.Kind == KindDocComment {
		s := c.Symbol
		s.Doc = c.Text
		found = Judge(s, opts...)
	} else {
		found = JudgeText(c.Text, c.Kind, opts...)
	}

	return slices.ContainsFunc(found, func(f Finding) bool { return f.Rule == c.Rule })
}

// Agrees reports whether the rule still does what the case's label records.
func (c Case) Agrees() bool { return c.Fires() == c.Label.fires() }

// Score is one rule's tally over its labeled cases. Precision is null when no
// case is labeled as firing, and Recall when no case is labeled as text the
// rule should report.
type Score struct {
	Rule      Rule     `json:"rule"`
	Cases     int      `json:"cases"`
	TP        int      `json:"tp"`
	FP        int      `json:"fp"`
	FN        int      `json:"fn"`
	TN        int      `json:"tn"`
	Precision *float64 `json:"precision"`
	Recall    *float64 `json:"recall"`
	// Disagree counts the cases whose rule no longer does what their label
	// records; their verdicts are tallied as observed.
	Disagree int `json:"disagree"`
}

// Calibrate runs every case and tallies the verdicts it observes per rule, in
// [Rules] order. A case labeled hit or fn should be reported; one the rule
// reports is a true positive, one it does not a false negative.
func Calibrate(cases []Case) []Score {
	byRule := map[Rule]*Score{}

	for _, c := range cases {
		s, ok := byRule[c.Rule]
		if !ok {
			s = &Score{Rule: c.Rule}
			byRule[c.Rule] = s
		}

		fired := c.Fires()
		shouldFire := c.Label == LabelHit || c.Label == LabelFalseNegative

		s.Cases++

		switch {
		case fired && shouldFire:
			s.TP++
		case fired:
			s.FP++
		case shouldFire:
			s.FN++
		default:
			s.TN++
		}

		if fired != c.Label.fires() {
			s.Disagree++
		}
	}

	var out []Score

	for _, r := range Rules() {
		s, ok := byRule[r]
		if !ok {
			continue
		}

		s.Precision = ratio(s.TP, s.TP+s.FP)
		s.Recall = ratio(s.TP, s.TP+s.FN)
		out = append(out, *s)
	}

	return out
}

func ratio(n, d int) *float64 {
	if d == 0 {
		return nil
	}

	r := float64(n) / float64(d)

	return &r
}

// Rate is how often one rule reports over the texts of a group.
type Rate struct {
	Rule     Rule     `json:"rule"`
	Decision Decision `json:"decision"`
	Findings int      `json:"findings"`
	// PerThousand is findings per 1000 texts.
	PerThousand float64 `json:"per_thousand"`
	// Texts counts the texts with at least one finding, and Share is that
	// count over the group's texts.
	Texts int     `json:"texts"`
	Share float64 `json:"share"`
	// DeniedTexts counts the texts with at least one finding the rule denies,
	// which differs from Texts where the rule marks some of its findings
	// advise itself.
	DeniedTexts int `json:"denied_texts"`
}

// GroupRates is every rule's rate over one group of texts.
type GroupRates struct {
	Group string `json:"group"`
	Texts int    `json:"texts"`
	Rules []Rate `json:"rules"`
}

// Tally counts findings per rule and group over texts of one kind, judged
// with the rules' default decisions unless opts say otherwise. It is safe
// for concurrent use.
type Tally struct {
	kind  Kind
	opts  []Option
	rules []Rate

	mu     sync.Mutex
	groups map[string]*groupTally
}

type groupTally struct {
	texts    int
	findings map[Rule]int
	hit      map[Rule]int
	denied   map[Rule]int
}

// NewTally returns a Tally over the rules opts leave on for kind.
func NewTally(kind Kind, opts ...Option) *Tally {
	o := collect(opts)
	t := &Tally{kind: kind, opts: opts, groups: map[string]*groupTally{}}

	for _, c := range checks {
		if d := o.decide(c, kind); d != DecisionOff && c.judge != nil {
			t.rules = append(t.rules, Rate{Rule: c.rule, Decision: d})
		}
	}

	return t
}

// Add judges text and counts its findings under group.
func (t *Tally) Add(group, text string) {
	var found []Finding
	if t.kind == KindDocComment {
		found = Judge(Symbol{Doc: text}, t.opts...)
	} else {
		found = JudgeText(text, t.kind, t.opts...)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	g, ok := t.groups[group]
	if !ok {
		g = &groupTally{findings: map[Rule]int{}, hit: map[Rule]int{}, denied: map[Rule]int{}}
		t.groups[group] = g
	}

	g.texts++

	seen, denied := map[Rule]bool{}, map[Rule]bool{}

	for _, f := range found {
		g.findings[f.Rule]++

		if !seen[f.Rule] {
			seen[f.Rule] = true
			g.hit[f.Rule]++
		}

		if f.Decision == DecisionDeny && !denied[f.Rule] {
			denied[f.Rule] = true
			g.denied[f.Rule]++
		}
	}
}

// Rates returns each group's rates, groups sorted by name and rules in
// [Rules] order, every rule that runs on the kind listed.
func (t *Tally) Rates() []GroupRates {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []GroupRates

	for _, name := range slices.Sorted(maps.Keys(t.groups)) {
		g := t.groups[name]
		gr := GroupRates{Group: name, Texts: g.texts, Rules: []Rate{}}

		for _, r := range t.rules {
			r.Findings, r.Texts, r.DeniedTexts = g.findings[r.Rule], g.hit[r.Rule], g.denied[r.Rule]
			r.PerThousand = 1000 * float64(r.Findings) / float64(g.texts)
			r.Share = float64(r.Texts) / float64(g.texts)
			gr.Rules = append(gr.Rules, r)
		}

		out = append(out, gr)
	}

	return out
}
