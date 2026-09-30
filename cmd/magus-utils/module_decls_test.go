package main

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hostmodules"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/std"
)

// A namespace's Objects are declared like a signature's objects are: with everything
// they reference, ahead of their first use.
func TestCollectMirrorsVisitsNamespaceObjects(t *testing.T) {
	got, err := collectMirrors(std.Module{
		Name:       "probe",
		Namespaces: []std.Namespace{{Name: "guard", Objects: []string{"SpawnRequest"}}},
	})
	require.NoError(t, err)
	require.Contains(t, got, "SpawnRequest")
	require.Contains(t, got, "Job", "SpawnRequest.lease brings Job with it")
	assert.Less(t, slices.Index(got, "Job"), slices.Index(got, "SpawnRequest"), "declared before use")

	_, err = collectMirrors(std.Module{
		Name:       "probe",
		Namespaces: []std.Namespace{{Name: "guard", Objects: []string{"NoSuchObject"}}},
	})
	assert.ErrorContains(t, err, `object "NoSuchObject" is not declared in boundaryTypes`)
}

// Every bundle declares each type it names, so executing it alone cannot stop on one
// another bundle declares. CheckStatus, reached only through Check's field, is the case
// that used to go missing, and TypeFunc used to name a Function nothing declared.
func TestEveryModuleDeclBundleDeclaresTheTypesItNames(t *testing.T) {
	for _, mod := range hostmodules.All() {
		src, err := renderModuleDecls(mod)
		require.NoError(t, err, mod.Name)
		prog, err := buzz.Parse(src)
		require.NoError(t, err, mod.Name)
		assert.Empty(t, undeclaredTypeNames(prog), mod.Name)
	}
}

func TestUndeclaredTypeNames(t *testing.T) {
	prog, err := buzz.Parse(`export enum<str> Kind { a = "a" }
export object Rec {
    kind: Kind = Kind.a,
    status: Status = Status.none,
    items: [Item] = [<Item>],
    static extern fun each(fn: fun (item: Item, at: int) > bool) > void;
}
export extern fun walk(callback: Function) > {str: Rec} !> any;
export final v: magus\Dir = null;
`)
	require.NoError(t, err)
	assert.Equal(t, []string{"Function", "Item", "Status", `magus\Dir`}, undeclaredTypeNames(prog))
}

// Each module's records and enums run on their own, which is what lets a script build
// a magus\ record at run time.
func TestModuleRecordDeclsRunAlone(t *testing.T) {
	for _, mod := range hostmodules.All() {
		src, err := renderRecordDecls(mod)
		require.NoError(t, err, mod.Name)
		sess := buzz.NewSession(t.Context(), buzz.WithEmbedded())
		require.NoError(t, sess.Exec(t.Context(), src), mod.Name)
		_ = sess.Close()
	}

	var magus std.Module
	for _, mod := range hostmodules.All() {
		if mod.Name == "magus" {
			magus = mod
		}
	}
	require.Equal(t, "magus", magus.Name)
	src, err := renderRecordDecls(magus)
	require.NoError(t, err)
	sess := buzz.NewSession(t.Context(), buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	require.NoError(t, sess.Exec(t.Context(), src))
	sess.DeclareModuleTypes("magus", src)
	require.NoError(t, sess.Exec(t.Context(), `
export fun build() > str {
    final none = Check{}.status == CheckStatus.none;
    return "{DirsOptions{ language = "go", depth = 2 }.language} {none}";
}
`))
	got, err := sess.CallValue(t.Context(), sess.Exports()["build"], nil)
	require.NoError(t, err)
	assert.Equal(t, "go true", got.AsString())
}

// A nullable return declares `str?`, so the checker makes a caller handle null. Only a
// string takes it: a nullable of any other tag has no caller and is refused.
func TestBuzzReturnTypeDeclaresANullableString(t *testing.T) {
	got, err := buzzReturnType(std.Method{Returns: []std.Ret{{Type: std.TypeString, Nullable: true}}})
	require.NoError(t, err)
	assert.Equal(t, "str?", got)

	_, err = buzzReturnType(std.Method{Returns: []std.Ret{{Type: std.TypeInt, Nullable: true}}})
	assert.ErrorContains(t, err, "a nullable return is a str?")
}

// A callback declares its function type when the descriptor names one, and `any` when
// it does not; Func on anything but a callback is a descriptor bug.
func TestExternDeclTypesACallback(t *testing.T) {
	walk := std.Method{Name: "walk", Args: []std.Arg{
		{Name: "root", Type: std.TypeString},
		{Name: "callback", Type: std.TypeFunc, Func: "fun (path: str, isDir: bool) > bool !> any"},
	}}
	got, err := externDecl(walk)
	require.NoError(t, err)
	assert.Equal(t, "export extern fun walk(root: str, callback: fun (path: str, isDir: bool) > bool !> any) > void;\n", got)

	walk.Args[1].Func = ""
	got, err = externDecl(walk)
	require.NoError(t, err)
	assert.Contains(t, got, "callback: any)")

	_, err = externDecl(std.Method{Name: "bad", Args: []std.Arg{{Name: "s", Type: std.TypeString, Func: "fun () > void"}}})
	assert.ErrorContains(t, err, "it types only a TypeFunc")
}
