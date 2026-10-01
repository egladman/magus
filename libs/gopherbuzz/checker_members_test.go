package buzz

import (
	"context"
	"testing"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkStrict type-checks src as an upstream-strict session would.
func checkStrict(t *testing.T, src string) []typeError {
	t.Helper()
	prog, err := Parse(src)
	require.NoError(t, err)
	errs, _ := checkWithGlobals(prog, nil, nil, nil, nil, nil, nil, false)
	return errs
}

// The cases below each pin a program upstream Buzz (294d8f9) refuses at compile
// time. Each was accepted here until the checker learned the rule.

func TestCheck_NamespaceDotRejected(t *testing.T) {
	errs := checkStrict(t, "import \"std\";\n\nfun main() > void {\n    std.print(\"hi\");\n}\n")
	require.Len(t, errs, 1, fmtErrors(errs))
	assert.Equal(t, 4, errs[0].Line)
	assert.Equal(t, "`std` is not defined; std is a module, so its members are reached with a backslash: write std\\print", errs[0].Msg)

	assert.Empty(t, checkStrict(t, "import \"std\";\n\nfun main() > void {\n    std\\print(\"hi\");\n}\n"))
	assert.Empty(t, checkStrict(t, "import \"buzz:std\" as s;\n\nfun main() > void {\n    s\\print(\"hi\");\n}\n"))
}

// Embedded mode refuses the dot form as upstream does: its message, at the module
// name (probe p1 in TestCheck_PositionsMatchUpstream is the upstream run).
func TestCheck_NamespaceDotRejectedEmbedded(t *testing.T) {
	errs := checkSrc("import \"fs\";\n\nfun main() > void {\n    fs.writeFile(\"x\", content: \"y\");\n}\n")
	require.Len(t, errs, 1, fmtErrors(errs))
	assert.Equal(t, [2]int{4, 5}, [2]int{errs[0].Line, errs[0].Col})
	assert.Equal(t, "`fs` is not defined; fs is a module, so its members are reached with a backslash: write fs\\writeFile", errs[0].Msg)
}

func TestCheck_NamespaceDotAcceptedWhereMagusReadsIt(t *testing.T) {
	// A magus spell import binds an object whose ops are read with a dot.
	assert.Empty(t, checkStrict(t, "import \"magus/spell/markdown\";\n\nfun main() > void {\n    markdown.markdownlint(1);\n}\n"))
	// A local shadowing the module name is a value, not the module.
	assert.Empty(t, checkStrict(t, "import \"std\";\n\nfun f() > int {\n    final std = \"x\";\n    return std.len();\n}\n"))
}

func TestCheck_NamespaceDotThroughSession(t *testing.T) {
	sess := NewSession(context.Background())
	t.Cleanup(func() { sess.Close() })
	std := vmpackage.NewMap()
	std.MapSet("print", vmpackage.DirectValue("print", func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
		return vmpackage.Null, nil
	}))
	sess.SetNativeModule("std", std)
	diags := sess.Diagnostics("import \"std\";\n\nfun main() > void {\n    std.print(\"hi\");\n}\n")
	require.Len(t, diags, 1)
	assert.Equal(t, 4, diags[0].Line)
	assert.Contains(t, diags[0].Msg, `write std\print`)
}

// An object's members take a dot even behind a namespace. Upstream at the pin, on
// the same program with plib.buzz beside it:
//
//	o4.buzz:4:21: [E74] Syntax error: `list` does not exists in that namespace or namespace does not exists
//
// and the dot form, `plib\job.list()`, compiles and runs clean.
func TestCheck_ObjectMemberBackslashRejected(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "plib.buzz", "namespace plib;\n\nexport object job {\n    static fun list() > int {\n        return 1;\n    }\n}\n")
	sess := NewSession(context.Background())
	t.Cleanup(func() { sess.Close() })
	sess.SetIncludeDirs([]string{dir})

	diags := sess.Diagnostics("import \"plib\";\n\nfun count() > int {\n    return plib\\job\\list();\n}\n")
	require.Len(t, diags, 1)
	assert.Equal(t, [2]int{4, 21}, [2]int{diags[0].Line, diags[0].Col})
	assert.Equal(t, "`list` does not exists in that namespace or namespace does not exists; job is an object, so its members are reached with a dot: write plib\\job.list", diags[0].Msg)

	assert.Empty(t, sess.Diagnostics("import \"plib\";\n\nfun count() > int {\n    return plib\\job.list();\n}\n"))
}

// A path the host resolver binds (a remote spell handle) is a value whose members
// are read with a dot, though its path names no prefix the checker knows.
func TestCheck_NamespaceDotAcceptedOnResolverValue(t *testing.T) {
	sess := NewSession(context.Background(), WithEmbedded())
	t.Cleanup(func() { sess.Close() })
	spell := vmpackage.NewMap()
	spell.MapSet("name", vmpackage.StrValue("lint"))
	sess.SetModuleResolver(func(path string) (vmpackage.Value, bool) {
		return spell, path == "ghcr.io/team/spells/lint"
	})
	assert.Empty(t, sess.Diagnostics("import \"ghcr.io/team/spells/lint\";\nfinal n = lint.name;\n"))
}

func TestCheck_UnknownBuiltinMethodRejected(t *testing.T) {
	cases := []struct {
		name, src string
		line      int
		want      string
	}{
		{"list foreign name", "final xs = mut [1, 2];\nxs.push(3);\n", 2, "unknown method push on mut [int]; lists have append"},
		{"str foreign name", "final s = \"abc\";\nfinal u = s.toUpperCase();\n", 2, "unknown method toUpperCase on str; strings have upper"},
		{"str case slip", "final b = \"abc\".startswith(\"a\");\n", 1, "unknown method startswith on str; strings have startsWith"},
		{"map foreign name", "final m = {\"a\": 1};\nfinal h = m.has(\"a\");\n", 2, "unknown method has on {str:int}; maps have hasKey"},
		{"nothing close", "final xs = [1];\nfinal q = xs.zzzzzz();\n", 2, "unknown method zzzzzz on [int]; lists have append, clone,"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkSrc(tc.src)
			require.Len(t, errs, 1, fmtErrors(errs))
			assert.Equal(t, tc.line, errs[0].Line)
			assert.Contains(t, errs[0].Msg, tc.want)
		})
	}
}

func TestCheck_KnownBuiltinMethodsAccepted(t *testing.T) {
	checkOK(t, `
final xs = mut [3, 1, 2];
xs.append(4);
final n = xs.len();
final i = xs.indexOf(2);
final s = "Hello";
final u = s.upper();
final l = s.len();
final m = {"a": 1};
final k = m.keys();
final h = m.hasKey("a");
final z = m.size();
`)
}

func TestCheck_AnonymousObjectFieldIsNotAMapMethod(t *testing.T) {
	checkOK(t, "final rec = .{ name = \"x\", count = 2 };\nfinal n = rec.name;\nfinal c = rec.count;\n")
}

func TestCheck_LenAsFunctionRejected(t *testing.T) {
	errs := checkSrc("final xs = [1, 2];\nfinal n = len(xs);\n")
	require.Len(t, errs, 1, fmtErrors(errs))
	assert.Equal(t, 2, errs[0].Line)
	assert.Equal(t, UndefinedName, errs[0].Code)
	assert.Equal(t, "undefined: len; len is a method in Buzz: write xs.len()", errs[0].Msg)
}

func TestCheck_ReturnNullFromNonOptionalRejected(t *testing.T) {
	errs := checkSrc("fun f() > int {\n    return null;\n}\n")
	require.Len(t, errs, 1, fmtErrors(errs))
	assert.Equal(t, 2, errs[0].Line)
	assert.Equal(t, TypeMismatch, errs[0].Code)
	assert.Equal(t, "return value is null but this function declares a non-optional > int; declare > int? to allow null", errs[0].Msg)

	checkErr(t, "fun f(b: bool) > str {\n    return if (b) \"x\" else null;\n}\n", "return value is null")
	checkErr(t, "final g = fun () > int {\n    return null;\n};\n", "return value is null")
}

func TestCheck_ReturnNullFromOptionalAccepted(t *testing.T) {
	checkOK(t, "fun f() > int? {\n    return null;\n}\n")
	checkOK(t, "fun f() > [str]? {\n    return null;\n}\n")
	// A closure's own `?` decides, not the enclosing function's.
	checkOK(t, "fun f() > int {\n    final _g = fun () > str? {\n        return null;\n    };\n    return 1;\n}\n")
}

// upstream's functional.buzz: map::<str> on a list yields [str], so join is a
// list method on the result, not a str one.
func TestCheck_ListMapTypeArgNamesTheElement(t *testing.T) {
	checkOK(t, "final data = [1, 2];\nfinal mapped = data.map::<str>(fun (_: int, e: int) => \"{e}\");\nfinal s = mapped.join(\", \");\n")
}

func TestForeignMethodNamesResolve(t *testing.T) {
	for kind, names := range foreignMethodNames {
		for from, to := range names {
			assert.Truef(t, vmpackage.HasBuiltinMethod(kind, to), "%q maps to %q, which kind %d does not have", from, to, kind)
			assert.Falsef(t, vmpackage.HasBuiltinMethod(kind, from), "%q is a builtin of kind %d, so it needs no suggestion", from, kind)
		}
	}
}
