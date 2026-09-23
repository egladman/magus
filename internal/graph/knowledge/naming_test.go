package knowledge

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

const goModule = "gomod example.com/m "

// goNS is the namespace key scip-go writes for a package directory ("" is the module root).
func goNS(dir string) string {
	if dir == "" {
		return goModule + "`example.com/m`/"
	}
	return goModule + "`example.com/m/" + dir + "`/"
}

// goSym is one Go declaration as the SCIP reader distills it. file is workspace-relative.
func goSym(dir, desc, label, kind, sig, file string) types.KnowledgeSymbol {
	return types.KnowledgeSymbol{
		Key: goNS(dir) + desc, Label: label, Language: "go", SymbolKind: kind,
		Namespace: goNS(dir), Signature: sig, Source: file + ":1", Defs: []string{file},
	}
}

func goFunc(dir, name, sig string) types.KnowledgeSymbol {
	file := dir + "/x.go"
	if dir == "" {
		file = "x.go"
	}
	return goSym(dir, name+"().", name, "Function", sig, file)
}

// ctxReader is a function that reads a value from a context, the family EntryPointFrom missed.
func ctxReader(dir, name string) types.KnowledgeSymbol {
	return goFunc(dir, name, "func "+name+"(ctx context.Context) *"+name+"Value")
}

// namingGraph assembles symbols the way a workspace build does, plus the non-symbol nodes the
// collision check reads.
func namingGraph(t *testing.T, syms []types.KnowledgeSymbol, extra ...types.KnowledgeNode) *Graph {
	t.Helper()
	g := mergeAll([]Shard{assembleSymbols(".", syms, []types.TargetGraphProject{{Path: "."}})})
	g.AddNode(types.KnowledgeNode{ID: "project:.", Kind: types.KindProject, Label: "m", Source: "."})
	for _, n := range extra {
		g.AddNode(n)
	}
	return g
}

func symID(s types.KnowledgeSymbol) string { return symbolID(s.Key) }

func subjectIDs(syms ...types.KnowledgeSymbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = symID(s)
	}
	return out
}

func introduced(syms ...types.KnowledgeSymbol) map[string]bool {
	out := map[string]bool{}
	for _, s := range syms {
		out[symID(s)] = true
	}
	return out
}

func advice(name, message string, details ...string) types.Check {
	return types.Check{Name: name, Status: types.CheckAdvice, Message: message, Details: details, Evidence: types.EvidenceInferred}
}

// fromContextFamily is eight context readers named <X>FromContext across packages, and the one
// that is not (RecorderFrom), as the tree had them when EntryPointFrom was proposed.
func fromContextFamily() []types.KnowledgeSymbol {
	var out []types.KnowledgeSymbol
	for i, name := range []string{"Cwd", "Lease", "Root", "Base", "Charms", "Progress", "Runtime", "Writer"} {
		out = append(out, ctxReader(fmt.Sprintf("internal/p%d", i), name+"FromContext"))
	}
	return append(out, ctxReader("internal/obs", "RecorderFrom"))
}

func entryPointFrom() types.KnowledgeSymbol {
	return goFunc("internal/trail", "EntryPointFrom", "func EntryPointFrom(ctx context.Context) EntryPoint")
}

func TestConformanceFlagsEntryPointFrom(t *testing.T) {
	subj := entryPointFrom()
	g := namingGraph(t, append(fromContextFamily(), subj))

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(subj), Introduced: introduced(subj)})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckNamingAffix,
		"`EntryPointFrom`: 8 of 9 functions shaped `func(ctx) value` that say From or Context are named `<X>FromContext`",
		"named <X>FromContext: BaseFromContext, CharmsFromContext, CwdFromContext",
		"not: RecorderFrom",
	)}}, got)
}

func TestConformanceSilentOnConformingAndUnrelatedNames(t *testing.T) {
	conforming := ctxReader("internal/trail", "EntryPointFromContext")
	unrelated := goFunc("internal/vm", "NewVM", "func NewVM(ctx context.Context) *VM")
	// A predicate saying both words of the family is a different shape, not a drifted name.
	predicate := goFunc("internal/job", "IsFromContext", "func IsFromContext(ctx context.Context) bool")
	g := namingGraph(t, append(fromContextFamily(), conforming, unrelated, predicate))

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(conforming, unrelated, predicate)})

	assert.Empty(t, got)
}

func contextSetters(prefix string, n int, dir string) []types.KnowledgeSymbol {
	var out []types.KnowledgeSymbol
	for i := range n {
		name := prefix + greek[i]
		out = append(out, goFunc(dir, name, "func "+name+"(ctx context.Context, v string) context.Context"))
	}
	return out
}

// Nine ContextWithX setters beside ten WithX(ctx, ...) setters is a workspace that has not
// decided, so either spelling of a new setter is silent. The control shows the ContextWith
// family alone WOULD flag WithEntryPoint: the silence is the split, not a blind spot.
func TestConformanceSilentOnSplitIdiom(t *testing.T) {
	long := goFunc("internal/trail", "ContextWithEntryPoint", "func ContextWithEntryPoint(ctx context.Context, v string) context.Context")
	short := goFunc("internal/trail", "WithEntryPoint", "func WithEntryPoint(ctx context.Context, v string) context.Context")
	setters := contextSetters("ContextWithThing", 9, "internal/a")

	split := namingGraph(t, append(append(setters, contextSetters("WithItem", 10, "internal/b")...), long, short))
	assert.Empty(t, split.Conformance(ConformanceChange{Subjects: subjectIDs(long, short)}))

	alone := namingGraph(t, append(setters, short))
	got := alone.Conformance(ConformanceChange{Subjects: subjectIDs(short)})
	require.Len(t, got[symID(short)], 1)
	assert.Equal(t, types.CheckNamingAffix, got[symID(short)][0].Name)
}

func TestConformanceIgnoresGeneratedAndTestCode(t *testing.T) {
	syms := fromContextFamily()[:4]
	// Four more carriers in a test file, a protobuf file, a testdata directory and a declared
	// output: none of them can lift the family to the five it needs.
	syms = append(syms,
		goSym("internal/p9", "HelperFromContext().", "HelperFromContext", "Function",
			"func HelperFromContext(ctx context.Context) *T", "internal/p9/x_test.go"),
		goSym("internal/pb", "PbFromContext().", "PbFromContext", "Function",
			"func PbFromContext(ctx context.Context) *T", "internal/pb/x.pb.go"),
		goSym("internal/testdata", "FixtureFromContext().", "FixtureFromContext", "Function",
			"func FixtureFromContext(ctx context.Context) *T", "internal/testdata/x.go"),
		goSym("internal/p7", "OutFromContext().", "OutFromContext", "Function",
			"func OutFromContext(ctx context.Context) *T", "internal/p7/out.go"),
		// Hand-written, whatever the name says: a _gen suffix and a gen directory are both
		// ordinary names until a target declares them output.
		goSym("internal/p6", "ImageFromContext().", "ImageFromContext", "Function",
			"func ImageFromContext(ctx context.Context) *T", "internal/p6/image_gen.go"),
	)
	subj := entryPointFrom()
	g := namingGraph(t, append(syms, subj))
	generated := map[string]bool{"internal/p7/out.go": true}

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(subj), Generated: generated})
	require.Len(t, got[symID(subj)], 1, "image_gen.go is the fifth carrier")
	assert.Contains(t, got[symID(subj)][0].Message, "5 of 5 functions")

	generated["internal/p6/image_gen.go"] = true
	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: subjectIDs(subj), Generated: generated}))
}

func TestConformanceSilentOnATestFileSubject(t *testing.T) {
	subj := goSym("internal/trail", "LeaseFrom().", "LeaseFrom", "Function",
		"func LeaseFrom(ctx context.Context) *T", "internal/trail/x_test.go")
	g := namingGraph(t, append(fromContextFamily(), subj))

	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: subjectIDs(subj)}), "eight carriers, and a test helper is not held to them")
}

// A change cannot manufacture the convention it is measured against: its own new names count
// neither as carriers nor as outliers.
func TestConformanceExcludesTheChangesOwnNames(t *testing.T) {
	family := fromContextFamily()
	subj := entryPointFrom()
	g := namingGraph(t, append(family, subj))

	assert.Empty(t, g.Conformance(ConformanceChange{
		Subjects:   subjectIDs(subj),
		Introduced: introduced(append(family[:4:4], subj)...),
	}), "four of eight carriers arrive with the subject, leaving four")

	var fresh []types.KnowledgeSymbol
	for i := range 6 {
		fresh = append(fresh, ctxReader(fmt.Sprintf("internal/new%d", i), "Fresh"+greek[i]+"FromContext"))
	}
	lone := entryPointFrom()
	g = namingGraph(t, append(fresh, lone))
	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: subjectIDs(lone), Introduced: introduced(append(fresh, lone)...)}),
		"six carriers, every one of them new")
}

func TestConformanceNamesAnEntityOfTheSameName(t *testing.T) {
	format := goSym("", "Format().", "Format", "Function", "func Format(ctx context.Context) error", "magus.go")
	internal := goSym("internal/fmt", "Format().", "Format", "Function", "func Format(ctx context.Context) error", "internal/fmt/x.go")
	target := types.KnowledgeNode{ID: "target:.:format", Kind: types.KindTarget, Label: "format", Source: "."}
	// An op of that name in two spells is one name, not two hits.
	opA := types.KnowledgeNode{ID: "op:go:format", Kind: types.KindOp, Label: "format"}
	opB := types.KnowledgeNode{ID: "op:node:format", Kind: types.KindOp, Label: "format"}
	g := namingGraph(t, []types.KnowledgeSymbol{format, internal}, target, opA, opB)

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(format, internal)})

	assert.Equal(t, map[string][]types.Check{symID(format): {advice(types.CheckNameCollision,
		"`Format` is also the name of op `format`, target `.:format` in this workspace",
		"op `format`", "target `.:format`",
	)}}, got, "only a symbol at a project's top level travels far enough to report")
}

func TestConformanceNamesAnEntityOfTheSameNameInTypeScript(t *testing.T) {
	sym := func(file, name string) types.KnowledgeSymbol {
		ns := "npm web `" + file + "`/"
		return types.KnowledgeSymbol{
			Key: ns + name + "().", Label: name, Language: "typescript", Namespace: ns,
			Signature: "function " + name + "(): void", Source: "web/" + file + ":1", Defs: []string{"web/" + file},
		}
	}
	top := sym("lint.ts", "lint")
	nested := sym("src/lint.ts", "lint")
	g := namingGraph(t, []types.KnowledgeSymbol{top, nested},
		types.KnowledgeNode{ID: "project:web", Kind: types.KindProject, Label: "web", Source: "web"},
		types.KnowledgeNode{ID: "target:web:lint", Kind: types.KindTarget, Label: "lint", Source: "web"})

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(top, nested)})

	assert.Equal(t, map[string][]types.Check{symID(top): {advice(types.CheckNameCollision,
		"`lint` is also the name of target `web:lint` in this workspace", "target `web:lint`",
	)}}, got)
}

// A word already carrying other senses is a judgment about meaning, which the index cannot
// prove, so it says nothing.
func TestConformanceSilentOnAWordCarryingOtherSenses(t *testing.T) {
	transport := goSym("types", "Transport#", "Transport", "Type", "type Transport string", "types/actor.go")
	retry := goSym("internal/httpx", "retryTransport#", "retryTransport", "Struct",
		"type retryTransport struct{ next http.RoundTripper }", "internal/httpx/retry.go")
	capture := goSym("internal/proc", "captureTransport#", "captureTransport", "Struct",
		"type captureTransport struct{ next http.RoundTripper }", "internal/proc/capture.go")
	g := namingGraph(t, []types.KnowledgeSymbol{transport, retry, capture})

	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: subjectIDs(transport)}))
}

// capability builds a types interface with n method specifications.
func capability(name string, n int) []types.KnowledgeSymbol {
	out := []types.KnowledgeSymbol{goSym("types", name+"#", name, "Interface", "type "+name+" interface { ... }", "types/vcs.go")}
	for i := range n {
		m := fmt.Sprintf("Op%d", i)
		out = append(out, goSym("types", name+"#"+m+".", m, "MethodSpecification",
			"func ("+name+")."+m+"(ctx context.Context) error", "types/vcs.go"))
	}
	return out
}

func capabilities() []types.KnowledgeSymbol {
	var syms []types.KnowledgeSymbol
	for i, n := range []int{1, 1, 1, 2, 3, 5} {
		syms = append(syms, capability("Cap"+greek[i]+"Reporter", n)...)
	}
	return syms
}

func TestConformanceReportsAnOversizedInterface(t *testing.T) {
	stager := capability("Stager", 13)
	modest := capability("Merger", 4)
	g := namingGraph(t, append(append(capabilities(), stager...), modest...))

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(stager[0], modest[0])})

	assert.Equal(t, map[string][]types.Check{symID(stager[0]): {advice(types.CheckInterfaceSize,
		"`Stager` has 13 methods; 6 of 7 interfaces declared beside it have 4 methods or fewer",
		"4 methods or fewer: CapAlphaReporter, CapBetaReporter, CapDeltaReporter",
		"larger: CapZetaReporter",
	)}}, got, "one method past the bound is not a finding")
}

// An interface that already existed grows through the methods the change adds to it, and the
// finding lands on the interface.
func TestConformanceReportsAnInterfaceTheChangeGrows(t *testing.T) {
	stager := capability("Stager", 13)
	g := namingGraph(t, append(capabilities(), stager...))

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(stager[12], stager[13]), Introduced: introduced(stager[12:]...)})

	require.Len(t, got[symID(stager[0])], 1)
	assert.Equal(t, types.CheckInterfaceSize, got[symID(stager[0])][0].Name)
}

// The checks read no language: a TypeScript tree, whose signatures no reader here parses, forms
// families from the descriptor grammar and the name alone.
func TestConformanceFindsDriftInAnIndexWithNoSignatures(t *testing.T) {
	var syms []types.KnowledgeSymbol
	for _, name := range []string{"chordFromEvent", "keyFromEvent", "pointFromEvent", "rangeFromEvent", "targetFromEvent", "modsFromEvent"} {
		syms = append(syms, tsSym(name+".ts", name, "function "+name+"(e: KeyboardEvent): string"))
	}
	syms = append(syms, tsSym("parse.ts", "parseEvent", "function parseEvent(e: KeyboardEvent): string"))
	subj := tsSym("offset.ts", "offsetFrom", "function offsetFrom(e: KeyboardEvent): string")
	inTest := tsSym("offset.test.ts", "cursorFrom", "function cursorFrom(e: KeyboardEvent): string")
	g := namingGraph(t, append(syms, subj, inTest))

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(subj, inTest)})

	assert.Equal(t, map[string][]types.Check{symID(subj): {advice(types.CheckNamingAffix,
		"`offsetFrom`: 6 of 7 functions shaped `function` that say From or Event are named `<X>FromEvent`",
		"named <X>FromEvent: chordFromEvent, keyFromEvent, modsFromEvent",
		"not: parseEvent",
	)}}, got)
}

func tsSym(file, name, sig string) types.KnowledgeSymbol {
	ns := "npm web src/`" + file + "`/"
	return types.KnowledgeSymbol{
		Key: ns + name + "().", Label: name, Language: "typescript", Namespace: ns,
		Signature: sig, Source: "web/src/" + file + ":1", Defs: []string{"web/src/" + file},
	}
}

func TestConformanceCapsFindingsPerChange(t *testing.T) {
	syms := fromContextFamily()
	var subjects []types.KnowledgeSymbol
	for i := range 7 {
		name := "Thing" + greek[i] + "From"
		s := goFunc(fmt.Sprintf("internal/s%d", i), name, "func "+name+"(ctx context.Context) T")
		syms = append(syms, s)
		subjects = append(subjects, s)
	}
	g := namingGraph(t, syms)

	got := g.Conformance(ConformanceChange{Subjects: subjectIDs(subjects...), Introduced: introduced(subjects...)})

	var kept []string
	for _, s := range subjects {
		for range got[symID(s)] {
			kept = append(kept, s.Label)
		}
	}
	assert.ElementsMatch(t, []string{"ThingAlphaFrom", "ThingBetaFrom", "ThingDeltaFrom", "ThingEpsilonFrom", "ThingEtaFrom"}, kept,
		"seven tie, and the message breaks the tie")
}

// Two families tie on score for one subject; the order, and which findings survive the cap,
// must not depend on map iteration.
func TestConformanceIsDeterministicOnTies(t *testing.T) {
	var syms []types.KnowledgeSymbol
	for i := range 6 {
		syms = append(syms,
			ctxReader(fmt.Sprintf("internal/a%d", i), greek[i]+"FromContext"),
			ctxReader(fmt.Sprintf("internal/b%d", i), "ReadOnly"+greek[i]))
	}
	subj := ctxReader("internal/c", "ReadFrom")
	g := namingGraph(t, append(syms, subj))
	change := ConformanceChange{Subjects: subjectIDs(subj)}

	first := g.Conformance(change)
	require.Len(t, first[symID(subj)], 2, "both families score 0.75")
	assert.Contains(t, first[symID(subj)][0].Message, "`<X>FromContext`", "the tie breaks on the message")
	for range 20 {
		assert.Equal(t, first, g.Conformance(change))
	}
}

var greek = []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta", "Iota", "Kappa"}

func TestNameWords(t *testing.T) {
	for name, want := range map[string][]string{
		"EntryPointFrom":   {"entry", "point", "from"},
		"HTTPServer":       {"http", "server"},
		"parseJSONBody":    {"parse", "json", "body"},
		"read_file":        {"read", "file"},
		"kebab-case-name":  {"kebab", "case", "name"},
		"sha256Sum":        {"sha256", "sum"},
		"VCSDriver":        {"vcs", "driver"},
		"InvocationIDFrom": {"invocation", "id", "from"},
		"IDs":              {"ids"},
		"SessionIDs":       {"session", "ids"},
		"URLs":             {"urls"},
		"URLsFor":          {"urls", "for"},
		"IPv6":             {"ipv6"},
		"IPv6Addr":         {"ipv6", "addr"},
		"OAuth":            {"oauth"},
		"OAuthToken":       {"oauth", "token"},
		"IOReader":         {"io", "reader"},
		"HTTPSServer":      {"https", "server"},
		"straßeName":       {"straße", "name"},
		"ÜberName":         {"über", "name"},
		"":                 {},
	} {
		assert.Equal(t, want, foldedWords(nameWords(name)), name)
	}
}

func TestIndexIdentifier(t *testing.T) {
	for _, tc := range []struct {
		text, name string
		want       int
	}{
		{"func EntryPoint() {}", "EntryPoint", 5},
		{"x := EntryPointFrom(ctx)", "EntryPoint", -1},
		{"myEntryPoint := 1", "EntryPoint", -1},
		{"$EntryPoint", "EntryPoint", -1},
		{"EntryPoint", "EntryPoint", 0},
		{"a EntryPointFrom b EntryPoint.", "EntryPoint", 19},
		{"éEntryPoint", "EntryPoint", -1},
		{"anything", "", -1},
	} {
		assert.Equal(t, tc.want, IndexIdentifier(tc.text, tc.name), "%q in %q", tc.name, tc.text)
	}
}

func TestRenamedFrom(t *testing.T) {
	for _, tc := range []struct {
		added, name string
		removed     []string
		want        string
	}{
		{"func EntryPointFromContext(ctx context.Context) EntryPoint {", "EntryPointFromContext",
			[]string{"func EntryPointFrom(ctx context.Context) EntryPoint {"}, "EntryPointFrom"},
		{"export function applyPlan(p: Plan): void {", "applyPlan",
			[]string{"// a comment", "export function landPlan(p: Plan): void {"}, "landPlan"},
		{"func Apply(p Plan) error {", "Apply",
			[]string{"func Land(p Plan, force bool) error {"}, ""},
		{"func Apply(p Plan) error {", "Apply",
			[]string{"func Land(p Plan) error {", "func Merge(p Plan) error {"}, ""},
		{"func Apply(p Plan) error {", "Apply",
			[]string{"func (s) Land(p Plan) error {"}, ""},
		{"func Apply(p Plan) error {", "Apply", nil, ""},
	} {
		assert.Equal(t, tc.want, RenamedFrom(tc.added, tc.name, tc.removed), tc.added)
	}
}

func TestGoShapes(t *testing.T) {
	for _, tc := range []struct {
		kind, sig string
		path      []descriptor
		want      declShape
	}{
		{
			kind: "Function", sig: "func LeaseFromContext(ctx context.Context) (string, bool)",
			path: []descriptor{{"LeaseFromContext", '('}},
			want: declShape{Kind: declFunction, Visible: true, Key: "func(ctx) value, bool", Meaning: "func",
				Params: []namingParam{{"ctx", "context.Context"}}},
		},
		{
			kind: "Function", sig: "func Lookup(key string) (value string, mapped bool)",
			path: []descriptor{{"Lookup", '('}},
			want: declShape{Kind: declFunction, Visible: true, Key: "func(string) value, bool", Meaning: "func",
				Params: []namingParam{{"key", "string"}}},
		},
		{
			kind: "Function", sig: "func Drain(chan<- Event, <-chan int, chan int)",
			path: []descriptor{{"Drain", '('}},
			want: declShape{Kind: declFunction, Visible: true, Key: "func(chan<- Event, <-chan int, chan int) none", Meaning: "func",
				Params: []namingParam{{"", "chan<- Event"}, {"", "<-chan int"}, {"", "chan int"}}},
		},
		{
			kind: "Function", sig: "func Pipe(in <-chan int, out chan<- int, m map[string]int) (ch chan int, err error)",
			path: []descriptor{{"Pipe", '('}},
			want: declShape{Kind: declFunction, Visible: true, Key: "func(<-chan int, chan<- int, map[string]int) value, error", Meaning: "func",
				Params: []namingParam{{"in", "<-chan int"}, {"out", "chan<- int"}, {"m", "map[string]int"}}},
		},
		{
			kind: "Method", sig: "func (Set[T]).Valid(v T) bool",
			path: []descriptor{{"Set", '#'}, {"Valid", '('}},
			want: declShape{Kind: declMethod, Visible: true, Key: "func(T) bool", Meaning: "func",
				Params: []namingParam{{"v", "T"}}},
		},
		{
			kind: "Function", sig: "func predictMerge(root, base, tip string, stage *types.Stage) error",
			path: []descriptor{{"predictMerge", '('}},
			want: declShape{Kind: declFunction, Key: "func(string, string, string, *Stage) error", Meaning: "func",
				Params: []namingParam{{"root", "string"}, {"base", "string"}, {"tip", "string"}, {"stage", "*types.Stage"}}},
		},
		{
			kind: "Function", sig: "func WithExitCapture(ctx context.Context) (context.Context, func() (int, bool))",
			path: []descriptor{{"WithExitCapture", '('}},
			want: declShape{Kind: declFunction, Visible: true, Key: "func(ctx) ctx, func", Meaning: "func",
				Params: []namingParam{{"ctx", "context.Context"}}},
		},
		{
			kind: "Interface", sig: "type Stager interface {     BuildStage() error }",
			path: []descriptor{{"Stager", '#'}},
			want: declShape{Kind: declInterface, Visible: true, Key: "interface", Meaning: "interface"},
		},
		{
			kind: "Type", sig: "type Transport string", path: []descriptor{{"Transport", '#'}},
			want: declShape{Kind: declType, Visible: true, Key: "type string", Meaning: "string"},
		},
		{
			kind: "TypeAlias", sig: "type DiagnosticCode = diagnostics.Code", path: []descriptor{{"DiagnosticCode", '#'}},
			want: declShape{Kind: declType, Visible: true, Key: "alias", Meaning: "alias"},
		},
		{
			kind: "Constant", sig: `const Added untyped string = "added"`, path: []descriptor{{"Added", '.'}},
			want: declShape{Kind: declValue, Visible: true, Key: "const string", Meaning: "string"},
		},
		{
			kind: "Variable", sig: "var ErrGone error", path: []descriptor{{"ErrGone", '.'}},
			want: declShape{Kind: declValue, Visible: true, Key: "var error", Meaning: "error"},
		},
		{
			kind: "Field", sig: "struct field Transport string", path: []descriptor{{"hookAttribution", '#'}, {"Transport", '.'}},
			want: declShape{Kind: declField, Key: "field string", Meaning: "string"},
		},
		{
			kind: "Function", sig: "not a declaration", path: []descriptor{{"Odd", '('}},
			want: declShape{Kind: declFunction, Visible: true},
		},
	} {
		got := goShapes{}.read(symbolFacts{Kind: tc.kind, Signature: tc.sig, Path: tc.path, Neutral: neutralKind(tc.path)})
		assert.Equal(t, tc.want, got, tc.sig)
	}
}

func TestParseDescriptors(t *testing.T) {
	got, ok := parseDescriptors("symbol:gomod example.com/m `example.com/m/types`/Stager#Build().")
	require.True(t, ok)
	assert.Equal(t, []descriptor{{"example.com/m/types", '/'}, {"Stager", '#'}, {"Build", '('}}, got)

	got, ok = parseDescriptors("symbol:npm web src/`views.ts`/opens().(nodeCount)")
	require.True(t, ok)
	assert.Equal(t, []descriptor{{"src", '/'}, {"views.ts", '/'}, {"opens", '('}, {"nodeCount", 'p'}}, got)
	assert.Empty(t, neutralKind(afterNamespace(got)), "a parameter is never compared")

	for _, bad := range []string{"symbol:", "symbol:x y `unterminated", "symbol:x y name(", "symbol:x y name", "symbol:x y (open"} {
		_, ok := parseDescriptors(bad)
		assert.False(t, ok, bad)
	}
}

// conformanceFixture is a workspace near this repository's size: 400 packages of 50 functions,
// 4 interfaces and 16 of their methods each (about 28,000 declarations), calls between
// neighbors, and the 20 new functions one change adds.
func conformanceFixture() (*Graph, []types.KnowledgeSymbol) {
	verbs := []string{"Read", "Write", "Load", "Parse", "Render", "Open", "Close", "Build", "Resolve", "Check"}
	nouns := []string{"Config", "Graph", "Target", "Spell", "Lease", "Plan", "Output", "Digest", "Store", "Session"}
	name := func(p, f int) string {
		return verbs[f%10] + nouns[(f/10+p)%10] + []string{"", "FromContext", "At", "For", "With"}[f/10]
	}
	var syms []types.KnowledgeSymbol
	for p := range 400 {
		dir := fmt.Sprintf("internal/p%03d", p)
		for f := range 50 {
			s := goFunc(dir, name(p, f), "func "+name(p, f)+"(ctx context.Context, root string, out io.Writer) error")
			s.Source, s.DefEndLine = dir+"/x.go:"+fmt.Sprint(10*f+1), 10*f+8
			if f > 0 {
				s.Calls = []types.KnowledgeSymbolCall{{Key: goNS(dir) + name(p, f-1) + "().", Count: 1}}
			}
			syms = append(syms, s)
		}
		for i := range 4 {
			syms = append(syms, capability(fmt.Sprintf("P%d%sReporter", p, nouns[i]), 4)...)
		}
	}
	var added []types.KnowledgeSymbol
	for i := range 20 {
		n := fmt.Sprintf("%s%sFrom", verbs[i%10], nouns[i/2])
		added = append(added, goFunc("internal/new", n, "func "+n+"(out io.Writer, root string) error"))
	}
	g := mergeAll([]Shard{assembleSymbols(".", append(syms, added...), []types.TargetGraphProject{{Path: "."}})})
	g.AddNode(types.KnowledgeNode{ID: "project:.", Kind: types.KindProject, Label: "m", Source: "."})
	return g, added
}

func BenchmarkConformance(b *testing.B) {
	g, added := conformanceFixture()
	change := ConformanceChange{Subjects: subjectIDs(added...), Introduced: introduced(added...), Read: files(nil)}
	b.ReportAllocs()
	for b.Loop() {
		g.Conformance(change)
	}
}

// TestConformanceMeasure is the precision gate's harness, skipped unless
// MAGUS_CONFORMANCE_GRAPH names a `magus graph export --symbols -o json` file. It treats every
// symbol the export defines as a subject of one change and prints, per check, how many findings
// clear the bar before the per-change cap, then a seeded sample of 30 to read by hand:
//
//	./magus graph export --symbols -o json --tee /tmp/graph.json -s
//	MAGUS_CONFORMANCE_GRAPH=/tmp/graph.json ./magus run go::go-test . -- -run 'TestConformanceMeasure$' -v -count=1
//
// An export carries no declared outputs, so a file under a gen/ directory stands in for one:
// that is this repository's layout, and the harness is this repository's.
//
// Rename leftovers need a rename, which a single export does not hold, so they are measured
// over history instead (TestConformanceMeasureRenames).
func TestConformanceMeasure(t *testing.T) {
	path := os.Getenv("MAGUS_CONFORMANCE_GRAPH")
	if path == "" {
		t.Skip("MAGUS_CONFORMANCE_GRAPH is not set")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var export types.KnowledgeGraphOutput
	require.NoError(t, json.Unmarshal(raw, &export), path)
	g := NewGraph()
	g.Merge(export.Nodes, export.Links)

	var subjects []string
	generated := map[string]bool{}
	for id, n := range g.nodes {
		if n.Kind != types.KindSymbol || n.Source == "" {
			continue
		}
		subjects = append(subjects, id)
		file, _, _ := strings.Cut(n.Source, ":")
		if slices.Contains(strings.Split(file, "/"), "gen") {
			generated[file] = true
		}
	}
	slices.Sort(subjects)
	x := newNamingIndex(g, ConformanceChange{Subjects: subjects, Generated: generated})
	t.Logf("subjects: %d read of %d defined; callables %d", len(x.subjects), len(subjects), len(x.callables))
	for _, chk := range conformanceChecks {
		var kept []finding
		for _, f := range chk.run(x) {
			if f.score >= conformanceBar {
				kept = append(kept, f)
			}
		}
		t.Logf("== %s: %d findings clear the bar", chk.name(), len(kept))
		r := rand.New(rand.NewPCG(1, 2))
		r.Shuffle(len(kept), func(i, j int) { kept[i], kept[j] = kept[j], kept[i] })
		for i, f := range kept[:min(30, len(kept))] {
			t.Logf("%2d. %.2f %s\n      %s\n      %s", i+1, f.score, g.nodes[f.subject].Source, f.message, strings.Join(f.details, " | "))
		}
	}
}

// TestConformanceMeasureRenames replays the rename-leftover check over history, skipped unless
// MAGUS_CONFORMANCE_HISTORY names a revision range of this repository:
//
//	MAGUS_CONFORMANCE_HISTORY=HEAD~700..HEAD ./magus run go::go-test . --stall-timeout -1s -- \
//	    -run TestConformanceMeasureRenames -v -count=1 -timeout 60m
//
// Each commit stands in for a change with no baseline. A rename is what RenamedFrom finds on an
// added declaration line whose new name the commit declares and whose old name it no longer
// declares in any casing, found by `git grep` in place of an index. The files searched are every
// code file the commit's tree names the new name in, which is what the index's references give.
func TestConformanceMeasureRenames(t *testing.T) {
	span := os.Getenv("MAGUS_CONFORMANCE_HISTORY")
	if span == "" {
		t.Skip("MAGUS_CONFORMANCE_HISTORY is not set")
	}
	git := func(dir string, args ...string) string {
		out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out))
	}
	root := git(".", "rev-parse", "--show-toplevel")
	commits := strings.Fields(git(root, "rev-list", "--no-merges", span))
	declared := func(rev, name string, anyCase bool) bool {
		// POSIX ERE, which is what `git grep -E` speaks: no \s and no \b.
		pattern := `(func|type|const|var|function|class|interface|let)[[:space:]]+(\([^)]*\)[[:space:]]*)?` +
			name + `([^A-Za-z0-9_$]|$)`
		flags := "-lE"
		if anyCase {
			flags = "-liE"
		}
		return git(root, "grep", flags, pattern, rev, "--", "*.go", "*.ts", ":!*/gen/*") != ""
	}
	declaration := regexp.MustCompile(`^\s*(export\s+)?(func|type|const|var|function|class|interface|let)\b`)
	candidates, renames, findings := 0, 0, 0
	for i, sha := range commits {
		if i%100 == 0 {
			t.Logf("commit %d of %d", i, len(commits))
		}
		patch := git(root, "show", "--format=", "--no-color", sha, "--", "*.go", "*.ts", ":!*/gen/*", ":!*_test.go", ":!*.test.ts")
		for _, f := range changeset.Parse(patch) {
			var removed []string
			for _, h := range f.Hunks {
				for _, r := range h.Rows {
					if r.Kind == changeset.KindDel {
						removed = append(removed, r.Text)
					}
				}
			}
			for _, h := range f.Hunks {
				for _, r := range h.Rows {
					if r.Kind != changeset.KindAdd || !declaration.MatchString(r.Text) {
						continue
					}
					for _, id := range identifiers(r.Text) {
						old := RenamedFrom(r.Text, id, removed)
						oldWords, newWords := foldedWords(nameWords(old)), foldedWords(nameWords(id))
						if len(oldWords) < 2 {
							continue
						}
						candidates++
						// A struct field is in the index and not in this grep, so this errs
						// toward reporting.
						if !declared(sha, id, false) || declared(sha, old, true) || !declared(sha+"^", old, false) {
							continue
						}
						renames++
						var sites []string
						for _, file := range strings.Fields(git(root, "grep", "-lw", id, sha, "--", "*.go", "*.ts", ":!*/gen/*")) {
							file = strings.TrimPrefix(file, sha+":")
							for _, line := range leftoverLines(git(root, "show", sha+":"+file), oldWords, newWords) {
								sites = append(sites, fmt.Sprintf("%s:%d", file, line))
							}
						}
						if len(sites) > 0 {
							findings++
							t.Logf("%s %s -> %s: %d site(s): %s", sha[:10], old, id, len(sites), strings.Join(capList(sites, 4), " "))
						}
					}
				}
			}
		}
	}
	t.Logf("%d commits, %d composed names changed in place on a declaration line, %d renames, %d with leftovers",
		len(commits), candidates, renames, findings)
}
