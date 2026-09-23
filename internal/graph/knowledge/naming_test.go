package knowledge

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
// binding lookup reads.
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

// fromContextFamily is eight context readers named <X>FromContext across packages, and the one
// that is not (RecorderFrom), as the tree had them when EntryPointFrom was proposed.
func fromContextFamily() []types.KnowledgeSymbol {
	var out []types.KnowledgeSymbol
	for i, name := range []string{"Cwd", "Lease", "Root", "Base", "Charms", "Progress", "Runtime", "Writer"} {
		out = append(out, ctxReader(fmt.Sprintf("internal/p%d", i), name+"FromContext"))
	}
	return append(out, ctxReader("internal/obs", "RecorderFrom"))
}

func TestNamingFlagsEntryPointFrom(t *testing.T) {
	subj := goFunc("internal/trail", "EntryPointFrom", "func EntryPointFrom(ctx context.Context) EntryPoint")
	g := namingGraph(t, append(fromContextFamily(), subj))

	got := g.Naming(NamingChange{Subjects: []string{symID(subj)}})

	require.Len(t, got[symID(subj)], 1)
	assert.Equal(t, types.DiffNaming{
		Check:    types.DiffNamingAffix,
		Summary:  "`EntryPointFrom`: 8 of 9 functions of its shape that say From or Context are named `<X>FromContext`",
		Score:    0.67,
		Headline: true,
		Pattern:  "<X>FromContext",
		Shape:    "func(ctx) value",
		Members:  8,
		Cohort:   9,
		Examples: []string{"BaseFromContext", "CharmsFromContext", "CwdFromContext"},
		Outliers: []string{"RecorderFrom"},
	}, got[symID(subj)][0])
}

func TestNamingSilentOnConformingAndUnrelatedNames(t *testing.T) {
	conforming := ctxReader("internal/trail", "EntryPointFromContext")
	unrelated := goFunc("internal/vm", "NewVM", "func NewVM(ctx context.Context) *VM")
	predicate := goFunc("internal/job", "IsJob", "func IsJob(ctx context.Context) bool")
	g := namingGraph(t, append(fromContextFamily(), conforming, unrelated, predicate))

	got := g.Naming(NamingChange{Subjects: []string{symID(conforming), symID(unrelated), symID(predicate)}})

	assert.Empty(t, got, "a conforming name, a name sharing no word, and a different shape all stay silent")
}

// Nine ContextWithX setters beside ten WithX(ctx, ...) setters is a workspace that has not
// decided, so either spelling of a new setter is silent.
func TestNamingSilentOnSplitIdiom(t *testing.T) {
	var syms []types.KnowledgeSymbol
	for i := range 9 {
		name := fmt.Sprintf("ContextWithThing%c", 'A'+i)
		syms = append(syms, goFunc("internal/a", name, "func "+name+"(ctx context.Context, v string) context.Context"))
	}
	for i := range 10 {
		name := fmt.Sprintf("WithItem%c", 'A'+i)
		syms = append(syms, goFunc("internal/b", name, "func "+name+"(ctx context.Context, v string) context.Context"))
	}
	long := goFunc("internal/trail", "ContextWithEntryPoint", "func ContextWithEntryPoint(ctx context.Context, v string) context.Context")
	short := goFunc("internal/trail", "WithEntryPoint", "func WithEntryPoint(ctx context.Context, v string) context.Context")
	g := namingGraph(t, append(syms, long, short))

	assert.Empty(t, g.Naming(NamingChange{Subjects: []string{symID(long), symID(short)}}))
}

func TestNamingIgnoresGeneratedAndTestCode(t *testing.T) {
	var syms []types.KnowledgeSymbol
	for i, name := range []string{"Cwd", "Lease", "Root", "Base"} {
		syms = append(syms, ctxReader(fmt.Sprintf("internal/p%d", i), name+"FromContext"))
	}
	// Four more in a test file, a gen/ directory, a _gen.go file, and a declared output: none
	// of them can lift the family to the five it needs.
	helper := goSym("internal/p9", "TestHelperFromContext().", "TestHelperFromContext", "Function",
		"func TestHelperFromContext(ctx context.Context) *T", "internal/p9/x_test.go")
	genDir := goSym("internal/gen", "GenFromContext().", "GenFromContext", "Function",
		"func GenFromContext(ctx context.Context) *T", "internal/gen/x.go")
	genFile := goSym("internal/p8", "StubFromContext().", "StubFromContext", "Function",
		"func StubFromContext(ctx context.Context) *T", "internal/p8/stub_gen.go")
	declared := goSym("internal/p7", "OutFromContext().", "OutFromContext", "Function",
		"func OutFromContext(ctx context.Context) *T", "internal/p7/out.go")
	subj := goFunc("internal/trail", "EntryPointFrom", "func EntryPointFrom(ctx context.Context) EntryPoint")
	testSubj := goSym("internal/trail", "LeaseFrom().", "LeaseFrom", "Function",
		"func LeaseFrom(ctx context.Context) *T", "internal/trail/x_test.go")
	g := namingGraph(t, append(syms, helper, genDir, genFile, declared, subj, testSubj))

	got := g.Naming(NamingChange{
		Subjects:  []string{symID(subj), symID(testSubj)},
		Generated: func(path string) bool { return path == "internal/p7/out.go" },
	})

	assert.Empty(t, got)
}

func TestNamingSilentOnFamilyTheChangeIntroduces(t *testing.T) {
	family := fromContextFamily()
	subj := goFunc("internal/trail", "EntryPointFrom", "func EntryPointFrom(ctx context.Context) EntryPoint")
	g := namingGraph(t, append(family, subj))
	introduced := map[string]bool{symID(subj): true}
	for _, s := range family[:4] {
		introduced[symID(s)] = true
	}

	got := g.Naming(NamingChange{
		Subjects:   []string{symID(subj)},
		Introduced: func(id string) bool { return introduced[id] },
	})

	assert.Empty(t, got, "four of eight members arrive with the subject: a convention being born")
}

func TestNamingAsksAboutAnEntityOfTheSameName(t *testing.T) {
	format := goSym("", "Format().", "Format", "Function", "func Format(ctx context.Context) error", "magus.go")
	internal := goSym("internal/fmt", "Format().", "Format", "Function", "func Format(ctx context.Context) error", "internal/fmt/x.go")
	target := types.KnowledgeNode{ID: "target:.:format", Kind: types.KindTarget, Label: "format", Source: "."}
	g := namingGraph(t, []types.KnowledgeSymbol{format, internal}, target)

	got := g.Naming(NamingChange{Subjects: []string{symID(format), symID(internal)}})

	assert.Equal(t, map[string][]types.DiffNaming{symID(format): {{
		Check:    types.DiffNamingBinding,
		Summary:  "`Format` is already the name of target `.:format` in this workspace; say whether this mirrors it or means something else",
		Score:    1,
		Headline: true,
		Pattern:  "Format",
		Members:  1,
		Cohort:   1,
		Examples: []string{"target `.:format`"},
	}}}, got, "only a root or types symbol travels far enough to ask about")
}

func TestNamingNotesAWordAlreadyCarryingOtherSenses(t *testing.T) {
	transport := goSym("types", "Transport#", "Transport", "Type", "type Transport string", "types/actor.go")
	field := goSym("internal/guard", "hookAttribution#Transport.", "Transport", "Field",
		"struct field Transport string", "internal/guard/hook.go")
	owner := goSym("internal/guard", "hookAttribution#", "hookAttribution", "Struct",
		"type hookAttribution struct{ Transport string }", "internal/guard/hook.go")
	retry := goSym("internal/httpx", "retryTransport#", "retryTransport", "Struct",
		"type retryTransport struct{ next http.RoundTripper }", "internal/httpx/retry.go")
	capture := goSym("internal/proc", "captureTransport#", "captureTransport", "Struct",
		"type captureTransport struct{ next http.RoundTripper }", "internal/proc/capture.go")
	daemon := types.KnowledgeNode{ID: "docsection:docs/daemon.md#two-transports", Kind: types.KindDocSection, Label: "Two transports"}
	editor := types.KnowledgeNode{ID: "docsection:docs/editor.md#transport", Kind: types.KindDocSection, Label: "Transport: one contract, two transports"}
	g := namingGraph(t, []types.KnowledgeSymbol{transport, field, owner, retry, capture}, daemon, editor)

	got := g.Naming(NamingChange{Subjects: []string{symID(transport)}})

	assert.Equal(t, map[string][]types.DiffNaming{symID(transport): {{
		Check:   types.DiffNamingBinding,
		Summary: "`Transport` reuses a name 5 other things here already carry in 3 senses; say which this is",
		Score:   0.4,
		Pattern: "Transport",
		Members: 2,
		Cohort:  5,
		Examples: []string{
			"field of string: hookAttribution.Transport",
			"heading: \"Transport: one contract, two transports\", \"Two transports\"",
			"struct: captureTransport, retryTransport",
		},
	}}}, got, "ranked evidence below the headline: three senses already, this is a fourth")
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

func TestNamingHeadlinesAnOversizedInterface(t *testing.T) {
	var syms []types.KnowledgeSymbol
	for i, n := range []int{1, 1, 1, 2, 3, 5} {
		syms = append(syms, capability(fmt.Sprintf("Cap%cReporter", 'A'+i), n)...)
	}
	stager := capability("Stager", 13)
	modest := capability("Merger", 4)
	g := namingGraph(t, append(append(syms, stager...), modest...))

	got := g.Naming(NamingChange{Subjects: []string{symID(stager[0]), symID(modest[0])}})

	assert.Equal(t, []types.DiffNaming{{
		Check:    types.DiffNamingSize,
		Summary:  "`Stager` has 13 methods; 6 of 7 interfaces declared beside it have 4 methods or fewer",
		Score:    0.86,
		Headline: true,
		Pattern:  "4 methods or fewer",
		Shape:    "interface",
		Members:  6,
		Cohort:   7,
		Examples: []string{"CapAReporter", "CapBReporter", "CapCReporter"},
		Outliers: []string{"CapFReporter"},
	}}, got[symID(stager[0])])
	assert.Empty(t, got[symID(modest[0])], "one method past the bound is not a finding")
}

// The scorer reads no language: a TypeScript tree, whose indexer reports no kind and no signature
// this lens parses, forms families from the descriptor grammar and the name alone.
func TestNamingFindsDriftInAnIndexWithNoSignatures(t *testing.T) {
	tsSym := func(file, name string) types.KnowledgeSymbol {
		ns := "npm web src/`" + file + "`/"
		return types.KnowledgeSymbol{
			Key: ns + name + "().", Label: name, Language: "typescript", Namespace: ns,
			Signature: "function " + name + "(e: KeyboardEvent): string", Source: "web/src/" + file + ":1",
			Defs: []string{"web/src/" + file},
		}
	}
	var syms []types.KnowledgeSymbol
	for _, name := range []string{"chordFromEvent", "keyFromEvent", "pointFromEvent", "rangeFromEvent", "targetFromEvent", "modsFromEvent"} {
		syms = append(syms, tsSym(name+".ts", name))
	}
	syms = append(syms, tsSym("parse.ts", "parseEvent"))
	subj := tsSym("offset.ts", "offsetFrom")
	inTest := tsSym("offset.test.ts", "cursorFrom")
	g := namingGraph(t, append(syms, subj, inTest))

	got := g.Naming(NamingChange{Subjects: []string{symID(subj), symID(inTest)}})

	assert.Equal(t, map[string][]types.DiffNaming{symID(subj): {{
		Check:    types.DiffNamingAffix,
		Summary:  "`offsetFrom`: 6 of 7 functions of its shape that say From or Event are named `<X>FromEvent`",
		Score:    0.64,
		Headline: true,
		Pattern:  "<X>FromEvent",
		Members:  6,
		Cohort:   7,
		Examples: []string{"chordFromEvent", "keyFromEvent", "modsFromEvent"},
		Outliers: []string{"parseEvent"},
	}}}, got)
}

func TestNamingCapsHeadlinesPerChange(t *testing.T) {
	syms := fromContextFamily()
	var subjects []string
	for i := range 7 {
		s := goFunc(fmt.Sprintf("internal/s%d", i), fmt.Sprintf("Thing%cFrom", 'A'+i),
			fmt.Sprintf("func Thing%cFrom(ctx context.Context) T", 'A'+i))
		syms = append(syms, s)
		subjects = append(subjects, symID(s))
	}
	g := namingGraph(t, syms)

	got := g.Naming(NamingChange{
		Subjects:   subjects,
		Introduced: func(id string) bool { return slices.Contains(subjects, id) },
	})

	headlines := 0
	for _, fs := range got {
		for _, f := range fs {
			if f.Headline {
				headlines++
			}
		}
	}
	assert.Len(t, got, 7, "every finding is still reported")
	assert.Equal(t, namingMaxHeadlines, headlines)
}

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
		"":                 {},
	} {
		assert.Equal(t, want, foldedWords(nameWords(name)), name)
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
