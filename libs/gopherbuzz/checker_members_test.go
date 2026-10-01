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
	assert.Equal(t, `std is a module, so its members are reached with a backslash: write std\print`, errs[0].Msg)

	assert.Empty(t, checkStrict(t, "import \"std\";\n\nfun main() > void {\n    std\\print(\"hi\");\n}\n"))
	assert.Empty(t, checkStrict(t, "import \"buzz:std\" as s;\n\nfun main() > void {\n    s\\print(\"hi\");\n}\n"))
}

func TestCheck_NamespaceDotAcceptedWhereMagusReadsIt(t *testing.T) {
	// A magus spell import binds an object whose ops are read with a dot.
	assert.Empty(t, checkStrict(t, "import \"magus/spell/markdown\";\n\nfun main() > void {\n    markdown.markdownlint(1);\n}\n"))
	// A local shadowing the module name is a value, not the module.
	assert.Empty(t, checkStrict(t, "import \"std\";\n\nfun f() > int {\n    final std = \"x\";\n    return std.len();\n}\n"))
	// The embedded dialect keeps the dot form: gopherbuzz's std conformance files use it.
	checkOK(t, "import \"std\";\nstd.print(\"hi\");\n")
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
