package knowledge

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// seqGraph builds Go symbol records whose calls carry the reference lines an index records.
type seqGraph struct {
	recs  []*types.KnowledgeSymbol
	byKey map[string]*types.KnowledgeSymbol
	pkgs  map[string][]string
}

func newSeqGraph() *seqGraph {
	return &seqGraph{byKey: map[string]*types.KnowledgeSymbol{}, pkgs: map[string][]string{}}
}

// def declares desc ("Run()." or "T#M().") in package pkg at file lines start through end and
// returns its key. An empty sig renders one taking nothing.
func (s *seqGraph) def(pkg, desc, file string, start, end int, sig string) string {
	label := strings.TrimSuffix(desc, "().")
	if i := strings.LastIndexByte(label, '#'); i >= 0 {
		label = label[i+1:]
	}
	if sig == "" {
		sig = "func " + label + "()"
	}
	r := &types.KnowledgeSymbol{
		Key: conformanceNS(pkg) + desc, Label: label, Language: "go", Namespace: conformanceNS(pkg), Signature: sig,
		Source: fmt.Sprintf("%s:%d", file, start), DefEndLine: end, Defs: []string{file},
	}
	s.recs = append(s.recs, r)
	s.byKey[r.Key] = r
	if !slices.Contains(s.pkgs[pkg], file) {
		s.pkgs[pkg] = append(s.pkgs[pkg], file)
	}
	return r.Key
}

// steps declares n ten-line functions S1..Sn in internal/lib, too long to be wrappers.
func (s *seqGraph) steps(n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, s.def("/internal/lib", fmt.Sprintf("S%d().", i), "internal/lib/lib.go", 10*i, 10*i+9, ""))
	}
	return out
}

// call records caller calling callees in order, one call per line after its first.
func (s *seqGraph) call(caller string, callees ...string) {
	c := s.byKey[caller]
	file, start, _ := splitPathLine(c.Source)
	for i, key := range callees {
		c.Calls = append(c.Calls, types.KnowledgeSymbolCall{Key: key, Count: 1})
		callee := s.byKey[key]
		line := start + 1 + i
		at := -1
		for j, r := range callee.Refs {
			if r.Path == file {
				at = j
			}
		}
		if at < 0 {
			callee.Refs = append(callee.Refs, types.KnowledgeSymbolRef{Path: file})
			at = len(callee.Refs) - 1
		}
		callee.Refs[at].Count++
		callee.Refs[at].Lines = append(callee.Refs[at].Lines, line)
	}
}

func (s *seqGraph) graph() *Graph {
	var syms []types.KnowledgeSymbol
	for pkg, files := range s.pkgs {
		syms = append(syms, conformancePkg(pkg, files))
	}
	for _, r := range s.recs {
		syms = append(syms, *r)
	}
	return mergeAll([]Shard{assembleSymbols(".", syms, nil)})
}

func callSeqOptions() types.ConformanceOptions {
	return types.ConformanceOptions{
		Limits: map[types.ConformanceKey]types.ConformanceLimit{
			{Metric: types.ConformanceCallSequence, Population: types.ConformancePopSource}: {Percentile: 99, Anchor: 7},
			{Metric: types.ConformanceCallSequence, Population: types.ConformancePopMain}:   {Percentile: 99, Anchor: 7},
		},
		Gates: testGates(),
	}
}

// pipelinePair is A in internal/a and B in internal/b running the same n steps in order, B
// with a step of its own between them, and both with the wrapper W interleaved. C makes W a
// wrapper: three callables call its one-line body.
func pipelinePair(n int, bFile string) (s *seqGraph, a, b string) {
	s = newSeqGraph()
	steps := s.steps(n)
	w := s.def("/internal/lib", "W().", "internal/lib/wrap.go", 1, 1, "")
	own := s.def("/internal/lib", "Own().", "internal/lib/own.go", 1, 9, "")
	a = s.def("/internal/a", "A().", "internal/a/a.go", 1, 40, "")
	b = s.def("/internal/b", "B().", bFile, 1, 40, "")
	c := s.def("/internal/c", "C().", "internal/c/c.go", 1, 5, "")
	s.call(a, append([]string{w}, steps...)...)
	s.call(b, append(append([]string{steps[0], own}, steps[1:]...), w)...)
	s.call(c, w, steps[0])
	return s, a, b
}

func callSeqAdvice(t *testing.T, got map[string][]types.Check, id string) []string {
	t.Helper()
	var out []string
	for _, c := range got[id] {
		require.Equal(t, types.CheckCallSequence, c.Name)
		out = append(out, c.Message)
	}
	return out
}

func TestCallSeqCheckReportsACopiedPipeline(t *testing.T) {
	s, a, b := pipelinePair(8, "internal/b/b.go")
	g := s.graph()

	got := g.ConformanceChecks(ConformanceChange{
		Subjects: []string{symbolID(b)}, Introduced: map[string]bool{symbolID(b): true}, Conformance: callSeqOptions(),
	})

	assert.Equal(t, map[string][]types.Check{symbolID(b): {advice(types.CheckCallSequence,
		"`B` calls 8 workspace functions in the order `A` does, past the configured anchor of 7 for call-sequence source",
		"in order: S1, S2, S3, S4, S5, S6, S7, S8",
		"partner: internal/a/a.go#A at internal/a/a.go:1",
		"call-sequence source: 7, the configured anchor",
		"share the sequence in one function, or record a known call-sequence entry with its reason",
	)}}, got, "W is a wrapper, so it is no step; Own is B's alone")
	assert.NotContains(t, got, symbolID(a))
}

func TestCallSeqCheckRunLength(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps int
		fires bool
	}{
		{name: "a run of 7 sits at the anchor", steps: 7},
		{name: "a run of 8 is past it", steps: 8, fires: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, b := pipelinePair(tc.steps, "internal/b/b.go")
			got := s.graph().ConformanceChecks(ConformanceChange{Subjects: []string{symbolID(b)}, Conformance: callSeqOptions()})
			assert.Equal(t, tc.fires, len(callSeqAdvice(t, got, symbolID(b))) == 1)
		})
	}
}

// Seven steps and a shared wrapper is a run of 8 only if the wrapper counted.
func TestCallSeqCheckNeverCountsAWrapper(t *testing.T) {
	s := newSeqGraph()
	steps := s.steps(7)
	w := s.def("/internal/lib", "W().", "internal/lib/wrap.go", 1, 3, "")
	a := s.def("/internal/a", "A().", "internal/a/a.go", 1, 40, "")
	b := s.def("/internal/b", "B().", "internal/b/b.go", 1, 40, "")
	c := s.def("/internal/c", "C().", "internal/c/c.go", 1, 5, "")
	s.call(a, append(steps, w)...)
	s.call(b, append(steps, w)...)
	s.call(c, w)

	assert.Empty(t, s.graph().ConformanceChecks(ConformanceChange{Subjects: []string{symbolID(b)}, Conformance: callSeqOptions()}))
}

func TestCallSeqCheckExcludesBuildersAndVerbs(t *testing.T) {
	t.Run("binding builders", func(t *testing.T) {
		s := newSeqGraph()
		var methods []string
		for i := 1; i <= 8; i++ {
			methods = append(methods, s.def("/internal/vm", fmt.Sprintf("Value#M%d().", i), "internal/vm/value.go", 10*i, 10*i+9, ""))
		}
		p := s.def("/internal/bind", "buildP().", "internal/bind/p.go", 1, 40, "")
		q := s.def("/internal/bind", "buildQ().", "internal/bind/q.go", 1, 40, "")
		s.call(p, methods...)
		s.call(q, methods...)

		assert.Empty(t, s.graph().ConformanceChecks(ConformanceChange{Subjects: []string{symbolID(q)}, Conformance: callSeqOptions()}),
			"every shared callee is a method of Value")
	})

	for _, tc := range []struct {
		name, params string
		fires        bool
	}{
		{name: "command verbs", params: "ctx context.Context, args []string"},
		{name: "main functions taking no argument list", params: "ctx context.Context", fires: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSeqGraph()
			steps := s.steps(8)
			s.def("/cmd/tool", "main().", "cmd/tool/main.go", 1, 3, "")
			p := s.def("/cmd/tool", "build().", "cmd/tool/build.go", 1, 40, "func build("+tc.params+") error")
			q := s.def("/cmd/tool", "lint().", "cmd/tool/lint.go", 1, 40, "func lint("+tc.params+") error")
			s.call(p, steps...)
			s.call(q, steps...)

			got := s.graph().ConformanceChecks(ConformanceChange{Subjects: []string{symbolID(q)}, Conformance: callSeqOptions()})
			assert.Equal(t, tc.fires, len(callSeqAdvice(t, got, symbolID(q))) == 1,
				"two verbs of a main package share its argument prologue")
		})
	}
}

func TestCallSeqCheckSkipsAKnownItem(t *testing.T) {
	s, _, b := pipelinePair(8, "internal/b/b.go")
	o := callSeqOptions()
	o.Ratchet.Known = []types.ConformanceKnown{{Metric: types.ConformanceCallSequence, Items: []string{"internal/b/b.go#B"}, Reason: "fold pending"}}

	assert.Empty(t, s.graph().ConformanceChecks(ConformanceChange{Subjects: []string{symbolID(b)}, Conformance: o}))
}

func TestCallSeqCheckIgnoresTestAndGeneratedPartners(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		generated  map[string]bool
	}{
		{name: "test", file: "internal/b/b_test.go"},
		{name: "gen directory", file: "internal/b/gen/b.go"},
		{name: "declared output", file: "internal/b/b_string.go", generated: map[string]bool{"internal/b/b_string.go": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a, _ := pipelinePair(8, tc.file)
			o := callSeqOptions()
			o.Generated = tc.generated
			got := s.graph().ConformanceChecks(ConformanceChange{Subjects: []string{symbolID(a)}, Generated: tc.generated, Conformance: o})
			assert.Empty(t, got, "B in %s is never a partner", tc.file)
		})
	}
}

// The change's own new declarations never form the norm: A copied into a new B is reported on
// B, never on A for its new partner.
func TestCallSeqCheckNeverMeasuresAgainstAnIntroducedPartner(t *testing.T) {
	s, a, b := pipelinePair(8, "internal/b/b.go")
	g := s.graph()

	got := g.ConformanceChecks(ConformanceChange{
		Subjects: []string{symbolID(a)}, Introduced: map[string]bool{symbolID(b): true}, Conformance: callSeqOptions(),
	})

	assert.Empty(t, got)
}

func TestCallSeqCheckScoresAPartnerTheChangeAlsoTouches(t *testing.T) {
	s, a, b := pipelinePair(8, "internal/b/b.go")
	g := s.graph()
	for _, tc := range []struct {
		name     string
		subjects []string
		want     float64
	}{
		{name: "partner untouched", subjects: []string{symbolID(b)}, want: callSeqScore},
		{name: "partner re-signed", subjects: []string{symbolID(a), symbolID(b)}, want: callSeqPartnerScore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newNamingIndex(g, ConformanceChange{Subjects: tc.subjects, Conformance: callSeqOptions()})
			var scores []float64
			for _, f := range (callSeqCheck{}).run(x) {
				if f.subject == symbolID(b) {
					scores = append(scores, f.score)
				}
			}
			assert.Equal(t, []float64{tc.want}, scores)
		})
	}
}

func TestCallSeqCheckIsSilentWithoutASymbolIndexOrAValue(t *testing.T) {
	assert.Empty(t, NewGraph().ConformanceChecks(ConformanceChange{Subjects: []string{"symbol:x"}, Conformance: callSeqOptions()}))

	s, _, b := pipelinePair(8, "internal/b/b.go")
	assert.Empty(t, s.graph().ConformanceChecks(ConformanceChange{Subjects: []string{symbolID(b)}, Conformance: testConformance()}),
		"no call-sequence value is configured or committed")
}

func TestCallSeqCheckIsDeterministic(t *testing.T) {
	s, _, b := pipelinePair(8, "internal/b/b.go")
	change := ConformanceChange{Subjects: []string{symbolID(b)}, Conformance: callSeqOptions()}
	first := s.graph().ConformanceChecks(change)
	require.NotEmpty(t, first)
	for range 10 {
		assert.Equal(t, first, s.graph().ConformanceChecks(change))
	}
}

func TestLongestSharedRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		a, b     []string
		wrappers map[string]bool
		want     int
	}{
		{name: "equal", a: []string{"x", "y", "z"}, b: []string{"x", "y", "z"}, want: 3},
		{name: "interleaved", a: []string{"x", "p", "y", "z"}, b: []string{"x", "y", "q", "z"}, want: 3},
		{name: "reordered", a: []string{"x", "y", "z"}, b: []string{"z", "y", "x"}, want: 1},
		{name: "wrapper dropped", a: []string{"x", "w", "y"}, b: []string{"x", "w", "y"}, wrappers: map[string]bool{"w": true}, want: 2},
		{name: "disjoint", a: []string{"x"}, b: []string{"y"}, want: 0},
		{name: "empty", b: []string{"y"}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, longestSharedRun(tc.a, tc.b, tc.wrappers))
			assert.Len(t, sharedRun(tc.a, tc.b, tc.wrappers), tc.want)
		})
	}
}

// Order is each callee's first reference line inside the caller's span. A callee whose
// recorded lines all sit outside it sorts by its first recorded line.
func TestCallSequencesOrderBySourceLine(t *testing.T) {
	s := newSeqGraph()
	steps := s.steps(3)
	a := s.def("/internal/a", "A().", "internal/a/a.go", 10, 20, "")
	s.call(a, steps[2], steps[0], steps[1])
	g := s.graph()

	assert.Equal(t, []string{symbolID(steps[2]), symbolID(steps[0]), symbolID(steps[1])}, callSequences(g)[symbolID(a)])

	// S1's only recorded line in a.go sits past A's body, so it sorts last.
	s.byKey[steps[0]].Refs[0].Lines = []int{40}
	assert.Equal(t, []string{symbolID(steps[2]), symbolID(steps[1]), symbolID(steps[0])}, callSequences(s.graph())[symbolID(a)])
}
