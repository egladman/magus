package ci

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecideGateMatrix pins every cell of the decision matrix by hand. The
// expectations are written out rather than derived, so a change to DecideGate's
// logic fails here instead of being restated by the test.
func TestDecideGateMatrix(t *testing.T) {
	cases := []struct {
		name string
		in   GateFacts
		want GateDecision
	}{
		{"nothing on record", GateFacts{}, GateRun},
		{"not redundant, saturated", GateFacts{Saturated: true}, GateRun},
		{"not redundant, forced", GateFacts{Forced: true}, GateRun},
		{"not redundant, nested", GateFacts{Nested: true}, GateRun},
		{"not redundant, saturated and forced", GateFacts{Saturated: true, Forced: true}, GateRun},
		{"not redundant, saturated and nested", GateFacts{Saturated: true, Nested: true}, GateRun},
		{"not redundant, forced and nested", GateFacts{Forced: true, Nested: true}, GateRun},
		{"not redundant, all set", GateFacts{Saturated: true, Forced: true, Nested: true}, GateRun},

		{"redundant, idle", GateFacts{Redundant: true}, GateAdvise},
		{"redundant, saturated", GateFacts{Redundant: true, Saturated: true}, GateRefuse},
		{"redundant, forced", GateFacts{Redundant: true, Forced: true}, GateRun},
		{"redundant, nested but idle", GateFacts{Redundant: true, Nested: true}, GateAdvise},
		{"redundant, saturated but forced", GateFacts{Redundant: true, Saturated: true, Forced: true}, GateRun},
		{"redundant, saturated but nested", GateFacts{Redundant: true, Saturated: true, Nested: true}, GateAdvise},
		{"redundant, forced and nested", GateFacts{Redundant: true, Forced: true, Nested: true}, GateRun},
		{"redundant, all set", GateFacts{Redundant: true, Saturated: true, Forced: true, Nested: true}, GateRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, DecideGate(tc.in))
		})
	}
}

func TestPoolSaturated(t *testing.T) {
	cases := []struct {
		name string
		snap *types.MachineSnapshot
		want bool
	}{
		{"nil snapshot fails open", nil, false},
		{"idle", &types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 2}, false},
		{"waiters queued", &types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 2, Waiters: []types.MachineClaimant{{PID: 1}}}, true},
		{"all slots held", &types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 8}, true},
		{"all memory held", &types.MachineSnapshot{BudgetMB: 1000, HeldMB: 1000}, true},
		{"unlimited axes never saturate", &types.MachineSnapshot{HeldSlots: 100, HeldMB: 100000}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, PoolSaturated(tc.snap))
		})
	}
}

func TestGateFingerprint(t *testing.T) {
	a := []GateStep{{Project: ".", Target: "ci", Key: "k1"}, {Project: "docs", Target: "ci", Key: "k2"}}
	b := []GateStep{{Project: "docs", Target: "ci", Key: "k2"}, {Project: ".", Target: "ci", Key: "k1"}}
	assert.Equal(t, GateFingerprint(a), GateFingerprint(b), "order-insensitive")
	assert.NotEqual(t, GateFingerprint(a), GateFingerprint([]GateStep{{Project: ".", Target: "ci", Key: "k1"}}), "selection is part of the identity")
	assert.NotEqual(t, GateFingerprint(a), GateFingerprint([]GateStep{{Project: ".", Target: "ci", Key: "OTHER"}, {Project: "docs", Target: "ci", Key: "k2"}}), "a changed key changes the fingerprint")
	assert.Empty(t, GateFingerprint(nil), "an empty selection has no identity")
}

func TestMergeFreeRange(t *testing.T) {
	linear := []types.Commit{
		{ID: "c3", Parents: []string{"c2"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.True(t, MergeFreeRange(linear, "c1"), "linear history down to green")
	assert.True(t, MergeFreeRange(linear, "c3"), "green at head is a trivially clean range")
	assert.False(t, MergeFreeRange(linear, "c0"), "green outside the walked window cannot be vouched for")

	// A merge always re-gates, however clean: it combines two verified
	// histories into a tree neither gate saw.
	merged := []types.Commit{
		{ID: "m1", Parents: []string{"c2", "f1"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.False(t, MergeFreeRange(merged, "c1"), "merge between green and head")
	assert.True(t, MergeFreeRange(merged, "m1"), "green AT the merge is behind us, not in the range")
}

// TestGateDeltaLines pins the per-file rendering the refusal prints: every
// path, its class word, and the fact behind it - never a summary.
func TestGateDeltaLines(t *testing.T) {
	d := GateDelta{Paths: []ClassifiedPath{
		{Path: "gen/a.go", Class: ClassGenerated, Why: "a declared output glob claims it"},
		{Path: "docs/x.md", Class: ClassProse, Why: `matches "**/*.md" (built-in default)`},
		{Path: "run.go", Class: ClassCommentOnly, Why: "only comments differ from the green gate's revision"},
		{Path: "internal/y.go", Class: ClassCode, Why: "differs beyond comments from the green gate's revision"},
	}}
	assert.False(t, d.LowRiskOnly())
	assert.Equal(t, []string{
		"gen/a.go: generated (a declared output glob claims it)",
		`docs/x.md: prose (matches "**/*.md" (built-in default))`,
		"run.go: comment-only (only comments differ from the green gate's revision)",
		"internal/y.go: code (differs beyond comments from the green gate's revision)",
	}, d.Lines())

	empty := GateDelta{}
	assert.True(t, empty.LowRiskOnly(), "an empty delta is low risk")
	assert.Empty(t, empty.Lines())
}

const goCode = `package p

// Greet says hi.
func Greet(name string) string {
	return "hi " + name
}
`

const goCommentEdit = `package p

// Greet greets by name; see the caller for why.
func Greet(name string) string {
	// the concatenation is deliberate
	return "hi " + name
}
`

const goCodeEdit = `package p

// Greet says hi.
func Greet(name string) string {
	return "hello " + name
}
`

func TestCommentOnlyGo(t *testing.T) {
	assert.True(t, CommentOnlyGo(goCode, goCode), "identical")
	assert.True(t, CommentOnlyGo(goCode, goCommentEdit), "comments added and reworded")
	assert.True(t, CommentOnlyGo(goCommentEdit, goCode), "comments removed")
	assert.False(t, CommentOnlyGo(goCode, goCodeEdit), "a string literal changed")
	assert.False(t, CommentOnlyGo(goCode, goCommentEdit+"\nfunc Extra() {}\n"), "code added alongside comments")
	assert.False(t, CommentOnlyGo(goCode, `package p; var s = "unterminated`), "a file that does not lex re-gates")
	assert.False(t, CommentOnlyGo(`package p; var s = "unterminated`, `package p; var s = "unterminated`), "even two identically unlexable files re-gate")
	// "//" inside a string is content, not a comment.
	assert.False(t, CommentOnlyGo(`package p; var u = "http://a"`, `package p; var u = "http://b"`))
}

// TestCommentOnlyGoDirectives: a directive comment is code. Editing one can
// change what compiles, embeds, generates, or lints, so it never defers.
func TestCommentOnlyGoDirectives(t *testing.T) {
	build := "//go:build linux\n\npackage p\n"
	assert.False(t, CommentOnlyGo(build, "//go:build darwin\n\npackage p\n"), "a build constraint edit is a code edit")
	assert.False(t, CommentOnlyGo("package p\n", "package p\n\n//go:generate stringer -type=T\n"), "adding a generate directive is a code edit")
	assert.False(t, CommentOnlyGo("//go:embed a.txt\npackage p\n", "package p\n"), "removing an embed directive is a code edit")
	assert.False(t, CommentOnlyGo("package p\nvar x = 1 //nolint\n", "package p\nvar x = 1\n"), "removing a nolint is a code edit")
	assert.True(t, CommentOnlyGo("//go:build linux\n\npackage p\n// old note\n", "//go:build linux\n\npackage p\n// new note\n"),
		"an untouched directive does not stop ordinary comment edits from deferring")
}

const buzzCode = `// greet builds the greeting.
fun greet(name: str) > str {
    return "hi";
}
`

const buzzCommentEdit = `// greet builds the greeting for one caller.
/* block note */
fun greet(name: str) > str {
    // trailing thought
    return "hi";
}
`

const buzzCodeEdit = `// greet builds the greeting.
fun greet(name: str) > str {
    return "hello";
}
`

func TestCommentOnlyBuzz(t *testing.T) {
	assert.True(t, CommentOnlyBuzz(buzzCode, buzzCode), "identical")
	assert.True(t, CommentOnlyBuzz(buzzCode, buzzCommentEdit), "comments added and reworded")
	assert.True(t, CommentOnlyBuzz(buzzCommentEdit, buzzCode), "comments removed")
	assert.False(t, CommentOnlyBuzz(buzzCode, buzzCodeEdit), "a string literal changed")
	assert.False(t, CommentOnlyBuzz(buzzCode, buzzCode+"\nfun extra() > void {}\n"), "code added alongside comments")
	assert.False(t, CommentOnlyBuzz(buzzCode, "fun broken( {"+"\x00"), "a file that does not lex re-gates")
}

// TestProseScopes pins the three configuration states: no declaration anywhere
// (built-in defaults), a declaration (replaces the defaults workspace-wide),
// and a declared-empty list (prose class off).
func TestProseScopes(t *testing.T) {
	undeclared := []*types.Project{{Path: "."}, {Path: "docs"}}
	scopes := ProseScopes(undeclared)
	assert.Equal(t, []ProseScope{{Globs: DefaultProseGlobs, Origin: ProseOriginDefault}}, scopes)

	declared := []*types.Project{
		{Path: "."},
		{Path: "docs", GateLowRisk: []string{"content/**"}, GateLowRiskDeclared: true},
	}
	scopes = ProseScopes(declared)
	assert.Equal(t, []ProseScope{{Dir: "docs", Globs: []string{"content/**"}, Origin: "gate_low_risk of project docs"}}, scopes,
		"a declaration replaces the built-in defaults, so an undeclared .md elsewhere stops classifying prose")

	emptied := []*types.Project{{Path: ".", GateLowRiskDeclared: true}}
	assert.Empty(t, ProseScopes(emptied), "declaring [] turns the prose class off entirely")
}

func classifierWith(scopes []ProseScope) ChangeClassifier {
	old := map[string]string{
		"a.go":       goCode,
		"b.buzz":     buzzCode,
		"broken.go":  goCode,
		"deleted.go": goCode,
		"tool.py":    "# comment\nX = 1\n",
	}
	cur := map[string]string{
		"a.go":      goCommentEdit,
		"b.buzz":    buzzCommentEdit,
		"broken.go": `package p; var s = "unterminated`,
		"new.go":    goCode,
		"tool.py":   "# a longer comment\nX = 1\n",
	}
	return ChangeClassifier{
		Role: func(_ context.Context, paths []string) (map[string]string, error) {
			return map[string]string{
				"gen/index.html": "output",
				".gitattributes": "maintained",
				"a.go":           "source",
				"b.buzz":         "source",
			}, nil
		},
		Prose: scopes,
		At: func(_ context.Context, rev, p string) (string, error) {
			if s, ok := old[p]; ok {
				return s, nil
			}
			return "", errors.New("not at " + rev)
		},
		Working: func(p string) (string, error) {
			if s, ok := cur[p]; ok {
				return s, nil
			}
			return "", errors.New("gone")
		},
	}
}

// TestClassifyChanges walks each class in and out of the table: generated
// paths by role, prose by the default globs, comment-only Go, Buzz and
// declared-syntax Python by content, and the code remainder including every
// failure to read or lex. The Why strings are asserted whole because the
// refusal prints them verbatim - they are how a reader traces each verdict
// back to the declaration or mechanism behind it.
func TestClassifyChanges(t *testing.T) {
	c := classifierWith(ProseScopes([]*types.Project{{Path: "."}}))
	paths := []string{
		"gen/index.html", // output role: generated, even with a non-md extension
		".gitattributes", // maintained: magus's own bookkeeping
		"docs/guide.md",  // markdown prose via the built-in default
		"CHANGELOG.md",   // changelog prose via the built-in default
		"a.go",           // comment-only Go edit
		"b.buzz",         // comment-only Buzz edit
		"broken.go",      // no longer lexes: code
		"deleted.go",     // gone from the working tree: code
		"new.go",         // absent at the green commit: code
		"tool.py",        // comment-only via python's declared syntax
		"magus.yaml",     // no declared comment syntax: code
	}
	got := c.Classify(context.Background(), paths, "green")
	want := []ClassifiedPath{
		{Path: "gen/index.html", Class: ClassGenerated, Why: "a declared output glob claims it"},
		{Path: ".gitattributes", Class: ClassGenerated, Why: "magus maintains it outside any target"},
		{Path: "docs/guide.md", Class: ClassProse, Why: `matches "**/*.md" (built-in default)`},
		{Path: "CHANGELOG.md", Class: ClassProse, Why: `matches "**/*.md" (built-in default)`},
		{Path: "a.go", Class: ClassCommentOnly, Why: "only comments differ from the green gate's revision"},
		{Path: "b.buzz", Class: ClassCommentOnly, Why: "only comments differ from the green gate's revision"},
		{Path: "broken.go", Class: ClassCode, Why: "differs beyond comments from the green gate's revision"},
		{Path: "deleted.go", Class: ClassCode, Why: "gone from the working tree"},
		{Path: "new.go", Class: ClassCode, Why: "absent at the green gate's revision"},
		{Path: "tool.py", Class: ClassCommentOnly, Why: "only comments differ from the green gate's revision"},
		{Path: "magus.yaml", Class: ClassCode, Why: "no comment syntax is declared for this language; classified as code"},
	}
	assert.Equal(t, want, got.Paths)
	assert.False(t, got.LowRiskOnly())

	lowOnly := c.Classify(context.Background(), []string{"a.go", "docs/guide.md"}, "green")
	assert.True(t, lowOnly.LowRiskOnly())
}

// TestClassifyDeclaredScopes: a workspace declaration replaces the defaults,
// scopes to the declaring project, and names itself in the attribution.
func TestClassifyDeclaredScopes(t *testing.T) {
	projects := []*types.Project{
		{Path: "docs", GateLowRisk: []string{"content/**"}, GateLowRiskDeclared: true},
	}
	c := classifierWith(ProseScopes(projects))
	got := c.Classify(context.Background(), []string{"docs/content/a.txt", "docs/guide.md", "CHANGELOG.md"}, "green")
	want := []ClassifiedPath{
		{Path: "docs/content/a.txt", Class: ClassProse, Why: `matches "content/**" (gate_low_risk of project docs)`},
		{Path: "docs/guide.md", Class: ClassCode, Why: "no comment syntax is declared for this language; classified as code"},
		{Path: "CHANGELOG.md", Class: ClassCode, Why: "no comment syntax is declared for this language; classified as code"},
	}
	assert.Equal(t, want, got.Paths)
}

// TestClassifyEmptiedScopes: gate_low_risk [] disables the prose class, so
// even the shipped markdown defaults stop classifying.
func TestClassifyEmptiedScopes(t *testing.T) {
	c := classifierWith(ProseScopes([]*types.Project{{Path: ".", GateLowRiskDeclared: true}}))
	got := c.Classify(context.Background(), []string{"docs/guide.md"}, "green")
	assert.Equal(t, ClassCode, got.Paths[0].Class)
}

// TestClassifyWithoutReaders pins the degraded mode: a VCS backend that cannot
// read a blob at a revision classifies every code-language edit as code rather
// than guessing.
func TestClassifyWithoutReaders(t *testing.T) {
	c := ChangeClassifier{Prose: ProseScopes(nil)}
	got := c.Classify(context.Background(), []string{"a.go", "b.buzz", "docs/x.md"}, "green")
	assert.Equal(t, ClassCode, got.Paths[0].Class)
	assert.Equal(t, ClassCode, got.Paths[1].Class)
	assert.Equal(t, ClassProse, got.Paths[2].Class)
}

func syntaxFor(t *testing.T, ext string) spells.CommentSyntax {
	t.Helper()
	syn, ok := spells.CommentSyntaxForExtension(ext)
	require.True(t, ok, "expected a shipped declaration for %s", ext)
	return syn
}

// TestStripCommentsStateMachine pins the string-awareness: a comment token
// inside a string is content, a quote inside a comment is comment, and a
// whole-line comment takes its line with it while code lines keep their bytes.
func TestStripCommentsStateMachine(t *testing.T) {
	py := syntaxFor(t, ".py")
	assert.Equal(t, `x = "# not a comment"`+"\n", StripComments(`x = "# not a comment"`+"\n", py),
		"a comment token inside a string is content")
	assert.Equal(t, "x = 1\n", StripComments("x = 1  # say \"hi\"\n", py),
		"a quote inside a comment is comment")
	assert.Equal(t, "x = 1\ny = 2\n", StripComments("x = 1\n# whole-line note\ny = 2\n", py),
		"a whole-line comment takes its newline with it")
	assert.Equal(t, "s = '''\n# kept\n'''\n", StripComments("s = '''\n# kept\n'''\n", py),
		"a docstring is a string; its hash lines are content")
}

// TestStripCommentsNested: rust declares nested block comments, so the outer
// closer is the one that ends the span.
func TestStripCommentsNested(t *testing.T) {
	rs := syntaxFor(t, ".rs")
	src := "let a = 1; /* outer /* inner */ still comment */ let b = 2;\n"
	assert.Equal(t, "let a = 1; let b = 2;\n", StripComments(src, rs))
	assert.Equal(t, `let s = r#"// not a comment "quote" "#;`+"\n",
		StripComments(`let s = r#"// not a comment "quote" "#;`+"\n", rs),
		"a raw string keeps comment tokens as content")
}

// TestCommentOnlyDeclared: the comparison the classifier runs for declared
// languages, including the no-whitespace-normalization rule.
func TestCommentOnlyDeclared(t *testing.T) {
	py := syntaxFor(t, ".py")
	assert.True(t, CommentOnlyDeclared("x = 1\n", "x = 1  # note\n", py), "a trailing comment appeared")
	assert.True(t, CommentOnlyDeclared("x = 1\n", "# header\nx = 1\n", py), "a comment line appeared")
	assert.False(t, CommentOnlyDeclared("x = 1\n", "x = 2  # note\n", py), "code changed alongside")
	assert.False(t, CommentOnlyDeclared("def f():\n    x = 1\n", "def f():\n  x = 1\n", py),
		"indentation is semantics; whitespace is never normalized")
	assert.False(t, CommentOnlyDeclared(`s = """doc"""`+"\n", `s = """docs"""`+"\n", py),
		"a docstring is a runtime value, so editing it is a code edit")

	ts := syntaxFor(t, ".ts")
	assert.True(t, CommentOnlyDeclared("const a = 1;\n", "const a = 1; /* note */\n", ts))
	assert.False(t, CommentOnlyDeclared("const u = `//x`;\n", "const u = `//y`;\n", ts),
		"a comment token inside a template literal is content")
}

// TestCommentOnlyDeclaredDirectives: a directive comment is code per the
// declaring language, so touching one never reads comment-only.
func TestCommentOnlyDeclaredDirectives(t *testing.T) {
	py := syntaxFor(t, ".py")
	assert.False(t, CommentOnlyDeclared("x = f()  # type: int\n", "x = f()  # type: str\n", py), "a type comment edit is a code edit")
	assert.False(t, CommentOnlyDeclared("import os  # noqa\n", "import os\n", py), "removing a noqa is a code edit")
	assert.True(t, CommentOnlyDeclared("import os  # noqa\n# old\n", "import os  # noqa\n# new\n", py),
		"an untouched directive does not stop ordinary comment edits from deferring")

	ts := syntaxFor(t, ".ts")
	assert.False(t, CommentOnlyDeclared("// @ts-ignore\nf();\n", "f();\n", ts), "removing a ts directive is a code edit")
}
