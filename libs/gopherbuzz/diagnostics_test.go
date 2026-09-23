package buzz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSession_Diagnostics_Clean(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	got := s.Diagnostics(`fun add(a: int, b: int) > int { return a + b; }`)
	assert.Empty(t, got, "a well-formed program should report no diagnostics")
}

func TestSession_Diagnostics_MultipleTypeErrors(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	// Two independent undefined references: the checker accumulates, so both must
	// come back (the point of Diagnostics over Exec, which stops at the first).
	got := s.Diagnostics("var a: int = missingOne;\nvar b: int = missingTwo;")
	require.Len(t, got, 2, "both undefined references should be reported")

	assert.Equal(t, 1, got[0].Line, "first diagnostic on line 1")
	assert.Contains(t, got[0].Msg, "missingOne")
	assert.NotContains(t, got[0].Msg, "buzz: line", "position prefix should be stripped from Msg")

	assert.Equal(t, 2, got[1].Line, "second diagnostic on line 2")
	assert.Contains(t, got[1].Msg, "missingTwo")
}

func TestDiagnosticOf(t *testing.T) {
	exec := func(code string) error {
		t.Helper()
		err := NewSession(context.Background(), WithEmbedded()).Exec(context.Background(), code)
		require.Error(t, err)
		return fmt.Errorf("magusfile: exec magusfile.buzz: %w", err)
	}

	got, ok := DiagnosticOf(exec("fun f() > void {\n  final x: int = \"s\";\n}"))
	require.True(t, ok)
	assert.Equal(t, Diagnostic{Line: 2, Col: 3, Code: "BZZ1005", Msg: `cannot assign str to int variable "x"`}, got)

	got, ok = DiagnosticOf(exec("var x: int = ;"))
	require.True(t, ok, "a parse error is located from its rendered position")
	assert.Equal(t, 1, got.Line)
	assert.Positive(t, got.Col)
	assert.Empty(t, got.Code)

	_, ok = DiagnosticOf(errors.New("magus.yaml: unknown key"))
	assert.False(t, ok)
	_, ok = DiagnosticOf(nil)
	assert.False(t, ok)
}

func TestSession_Diagnostics_ParseError(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	// A parse failure yields exactly one diagnostic (checking cannot proceed) with
	// a recovered position and the "buzz: line L:C:" prefix stripped.
	got := s.Diagnostics("var x: int = ;")
	require.Len(t, got, 1, "a parse error reports a single diagnostic")
	assert.Equal(t, 1, got[0].Line)
	assert.Positive(t, got[0].Col, "column should be recovered from the parser message")
	assert.NotEmpty(t, got[0].Msg)
	assert.False(t, strings.HasPrefix(got[0].Msg, "buzz: line"), "prefix stripped: %q", got[0].Msg)
}

// TestSession_Diagnostics_UnusedImport reproduces the gap this fix closes: upstream
// Buzz warns on an unused import, gopherbuzz previously said nothing at all.
func TestSession_Diagnostics_UnusedImport(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	got := s.Diagnostics(`import "unused/mod";`)

	require.Len(t, got, 1, "an import never referenced should produce exactly one diagnostic")
	assert.Equal(t, SeverityWarning, got[0].Severity, "unused import must be a WARNING, not an error")
	assert.Equal(t, diagnostics.Code("BZZ3001"), got[0].Code)
	assert.Contains(t, got[0].Msg, "unused/mod")
}

// TestSession_Diagnostics_ImportUsedViaDotAccess verifies dot access on the imported
// module (`mod.field`) counts as use, same as backslash access: gopherbuzz accepts
// both, unlike upstream, which only has the backslash form.
func TestSession_Diagnostics_ImportUsedViaDotAccess(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	mod := vm.NewMap()
	mod.MapSet("answer", vm.IntValue(42))
	s.SetNativeModule("example/demo", mod)

	got := s.Diagnostics("import \"example/demo\";\nvar x = demo.answer;")
	assert.Empty(t, got, "dot access on the imported module must count as use")
}

// TestSession_Diagnostics_ImportUsedViaBackslashAccess is the DotAccess test's sibling
// for the normal namespace-access form.
func TestSession_Diagnostics_ImportUsedViaBackslashAccess(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	mod := vm.NewMap()
	mod.MapSet("answer", vm.IntValue(42))
	s.SetNativeModule("example/demo", mod)

	got := s.Diagnostics(`import "example/demo";
var x = demo\answer;`)
	assert.Empty(t, got, "backslash namespace access must count as use")
}

// TestSession_Diagnostics_AliasedImportUsed verifies usage is tracked under the
// ALIAS, not the module's own path basename.
func TestSession_Diagnostics_AliasedImportUsed(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	mod := vm.NewMap()
	mod.MapSet("answer", vm.IntValue(42))
	s.SetNativeModule("example/demo", mod)

	got := s.Diagnostics(`import "example/demo" as d;
var x = d\answer;`)
	assert.Empty(t, got, "using the alias must count as use")
}

func TestSession_Diagnostics_AliasedImportUnused(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("example/demo", vm.NewMap())

	got := s.Diagnostics(`import "example/demo" as d;`)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Msg, "as d", "the message should name the unused ALIAS")
}

// TestSession_Diagnostics_ImportUsedOnlyInObjectLiteralType is the hard case: a module
// referenced only by constructing its exported type as `ns\Type{...}`. gopherbuzz's
// parser lowers that straight to ast.ObjectLit{TypeName: "Type"}, discarding the "ns"
// identifier, so this only passes if detection runs during parsing (before that
// lowering erases the reference), not by walking the finished AST.
//
// Modeled on how gopherbuzz's own stdlib does this (std/std.go's crypto/io, "registered
// TWICE"): a native module value PLUS a declaration source under the same import path,
// so the checker can resolve the exported object type. That combination resolves as
// ImportNative, which importUsageIsReliable always trusts, unlike a plain file import,
// whose non-aliased form also flat-merges (see NonAliasedFileImportNeverWarnsUnused)
// and can't reach `ns\Type{...}` resolution at all without it.
func TestSession_Diagnostics_ImportUsedOnlyInObjectLiteralType(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("lib", vm.NewMap())
	s.SetModuleDecls("lib", `export object Foo { n: int = 0 }`)

	got := s.Diagnostics(`
import "lib";
final f = lib\Foo{ n = 7 };
`)
	assert.Empty(t, got, "a module referenced only via ns\\Type{...} object-literal construction must not warn as unused")
}

// TestSession_Diagnostics_ImportUsedOnlyInTypeAnnotation is the annotation-position
// sibling of the object-literal case above: `v: lib\Foo` with the type never
// constructed.
func TestSession_Diagnostics_ImportUsedOnlyInTypeAnnotation(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("lib", vm.NewMap())
	s.SetModuleDecls("lib", `export object Foo { n: int = 0 }`)

	got := s.Diagnostics(`
import "lib";
fun readN(v: lib\Foo) > int { return v.n; }
`)
	assert.Empty(t, got, "a module referenced only in a namespaced type annotation must not warn as unused")
}

// TestSession_Diagnostics_NonAliasedFileImportNeverWarnsUnused reproduces the false-
// positive class found calibrating against this repo's real .buzz files: EVERY
// spells/*/spell.buzz does `import "magus/spell";` (no alias) and then uses that
// module's exported object types completely bare (`Command{...}`, `target: Target`),
// never once writing `spell\Command`. resolveImport's own alias-semantics comment
// confirms why: a non-aliased FILE import flat-merges its exports straight into
// scope, same as upstream's `as _`, so the namespace name genuinely can go
// unreferenced on a fully-used import. Detecting that would mean cross-referencing
// the imported file's export list against every identifier in the importer, which
// this package cannot do without a second resolution pass; see
// importUsageIsReliable, which leaves this shape unreported rather than risk it.
func TestSession_Diagnostics_NonAliasedFileImportNeverWarnsUnused(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte(`export object Foo { n: int = 0 }`), 0644))

	s := NewSession(context.Background(), WithEmbedded())
	s.SetIncludeDirs([]string{dir})

	// "lib" (the import's namespace binding) is never written as lib\... or lib....;
	// Foo is used entirely through the flat-merged bare name.
	got := s.Diagnostics(`
import "lib";
final f = Foo{ n = 7 };
`)
	assert.Empty(t, got, "a non-aliased file import may have flat-merged its members into scope; the namespace binding alone cannot prove it unused")
}

// TestSession_Diagnostics_FlatImportNeverWarnsUnused pins that a flat import (`as _`)
// is out of scope for BZZ3001: it binds no single namespace name, so attempting the
// check would mean tracking each flattened symbol individually: a broader "unused
// local" question, not "unused import".
func TestSession_Diagnostics_FlatImportNeverWarnsUnused(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	got := s.Diagnostics(`import "unused/mod" as _;`)
	assert.Empty(t, got, "a flat import binds no namespace name to check")
}

// TestSession_Diagnostics_SelectiveImportNeverWarnsUnused is the FlatImport test's
// sibling for `import a, b from "path"`.
func TestSession_Diagnostics_SelectiveImportNeverWarnsUnused(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte(`export fun pub() > int { return 1; }`), 0644))

	s := NewSession(context.Background(), WithEmbedded())
	s.SetIncludeDirs([]string{dir})

	got := s.Diagnostics(`import pub from "lib";`)
	assert.Empty(t, got, "a selective import binds no namespace name to check")
}

// TestSession_Diagnostics_ReplSuppressesUnusedImportWarning matches upstream: a REPL
// evaluates one statement at a time, so an import "unused so far" may simply be used
// by a line not typed yet; upstream Buzz gates its own unused_import warning on the
// same flavor check (Parser.zig: `self.flavor != .Repl`).
func TestSession_Diagnostics_ReplSuppressesUnusedImportWarning(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded(), WithREPL())
	s.SetNativeModule("unused/mod", vm.NewMap())

	got := s.Diagnostics(`import "unused/mod";`)
	assert.Empty(t, got, "a REPL session must not warn on an import that may be used by a later line")
}

// TestAllBZZCodesEnumerated guards allBZZCodes against the const block in diagnostics.go: every declared
// BZZ code must be enumerated, and the counts must match, so a new code cannot silently escape the
// doc-coverage check below.
func TestAllBZZCodesEnumerated(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	src, err := os.ReadFile(filepath.Join(dir, "diagnostics.go"))
	if err != nil {
		t.Fatal(err)
	}
	declared := regexp.MustCompile(`diagnostics\.Code = "(BZZ\d+)"`).FindAllStringSubmatch(string(src), -1)
	if len(declared) == 0 {
		t.Fatal("no BZZ codes found in diagnostics.go")
	}
	enum := map[diagnostics.Code]bool{}
	for _, c := range allBZZCodes {
		if enum[c] {
			t.Errorf("duplicate code %s in allBZZCodes", c)
		}
		enum[c] = true
	}
	for _, m := range declared {
		if !enum[diagnostics.Code(m[1])] {
			t.Errorf("%s is declared but missing from allBZZCodes", m[1])
		}
	}
	if len(allBZZCodes) != len(declared) {
		t.Errorf("allBZZCodes has %d entries, the const block declares %d", len(allBZZCodes), len(declared))
	}
}

// TestEveryBZZCodeHasDocPage keeps a new code from shipping without its lookup page, at exactly the path
// its docs URL resolves to (docs/codes/<code>.md inside gopherbuzz's own tree).
func TestEveryBZZCodeHasDocPage(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for _, c := range allBZZCodes {
		path := filepath.Join(dir, "docs", "codes", string(c)+".md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: no doc page at %s", c, path)
		}
	}
}
