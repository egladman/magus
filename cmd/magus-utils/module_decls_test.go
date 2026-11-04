package main

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hostmodules"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
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

type ModuleDeclsSuite struct {
	suite.Suite
}

func TestModuleDeclsSuite(t *testing.T) {
	suite.Run(t, new(ModuleDeclsSuite))
}

// image\MediaType names values for a str field, so no signature or field names it, and
// the module still declares it.
func (s *ModuleDeclsSuite) TestCollectEnumsDeclaresAnEnumScopedToTheModule() {
	mirrors, err := collectMirrors(std.Image)
	s.Require().NoError(err)
	enums, err := collectEnums(std.Image, mirrors)
	s.Require().NoError(err)
	names := make([]string, 0, len(enums))
	for _, e := range enums {
		names = append(names, e.Name)
	}
	s.Contains(names, "MediaType")

	enums, err = collectEnums(std.Module{Name: "probe"}, nil)
	s.Require().NoError(err)
	s.Empty(enums, "another module declares no enum scoped to image")
}

func (s *ModuleDeclsSuite) TestAForwardingMethodPassesThisAndLabelsItsArguments() {
	o := std.Object{Name: "Change"}
	got, err := forwardingMethod(std.Module{Name: "magus"}, o, std.Method{
		Name: "tier", Args: []std.Arg{{Name: "target", Type: std.TypeString}, {Name: "depth", Type: std.TypeInt}},
		Returns: []std.Ret{{Type: std.TypeString}}, Raises: true,
	})
	s.Require().NoError(err)
	s.Equal("\n    fun tier(target: str, depth: int) > str !> any {\n        return magus__Change__tier(this, target: target, depth: depth);\n    }\n", got)
}

func (s *ModuleDeclsSuite) TestAVoidForwardingMethodReturnsNothing() {
	got, err := forwardingMethod(std.Module{Name: "magus"}, std.Object{Name: "Change"}, std.Method{Name: "bust"})
	s.Require().NoError(err)
	s.Equal("\n    fun bust() > void {\n        magus__Change__bust(this);\n    }\n", got)
}

func (s *ModuleDeclsSuite) TestAVariadicObjectMethodIsRefused() {
	_, err := forwardingMethod(std.Module{Name: "magus"}, std.Object{Name: "Change"}, std.Method{
		Name: "join", Args: []std.Arg{{Name: "parts", Type: std.TypeString, Variadic: true}},
	})
	s.ErrorContains(err, "variadic")
}

func (s *ModuleDeclsSuite) TestTheObjectExternIsDeclaredPrivate() {
	src, err := renderRecordDecls(std.Image)
	s.Require().NoError(err)
	s.Contains(src, "\nextern fun image__Digest__string(recv: Digest) > str;\n")
	s.Contains(src, "return image__Digest__string(this);")
}

func (s *ModuleDeclsSuite) recordModule(name string) std.Module {
	return std.Module{Name: name, Objects: []std.Object{{Name: "Digest", Methods: []std.Method{{
		Name: "string", Returns: []std.Ret{{Type: std.TypeString}}, Impl: std.ImageDigestString,
	}}}}}
}

func (s *ModuleDeclsSuite) load(sess *buzz.Session, mod std.Module) {
	src, err := renderRecordDecls(mod)
	s.Require().NoError(err)
	native := vm.NewMap()
	answer := mod.Name
	for _, ext := range mod.ObjectExterns() {
		native.MapSet(ext.Name, vm.DirectValue(ext.Name, func(context.Context, []vm.Value) (vm.Value, error) {
			return vm.StringValue(answer), nil
		}))
	}
	sess.SetNativeModule(mod.Name, native)
	sess.SetModuleDecls(mod.Name, src)
}

// Two modules may declare one method on one record name, each reaching its
// own Go.
func (s *ModuleDeclsSuite) TestTwoModulesShareAnObjectMethodName() {
	ctx := s.T().Context()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	s.T().Cleanup(func() { _ = sess.Close() })
	s.load(sess, s.recordModule("alpha"))
	s.load(sess, s.recordModule("beta"))
	s.Require().NoError(sess.Exec(ctx, `
import "alpha";
import "beta";
final a = alpha\Digest{}.string();
final b = beta\Digest{}.string();
`))
	s.Equal("alpha", sess.Globals()["a"].AsString())
	s.Equal("beta", sess.Globals()["b"].AsString())
}

// The image decls and the generated trampoline run together, as a magusfile
// loads them: Digest.string() reaches Go through the private extern.
func (s *ModuleDeclsSuite) TestDigestStringCallsGo() {
	ctx := s.T().Context()
	src, err := renderModuleDecls(std.Image)
	s.Require().NoError(err)
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	s.T().Cleanup(func() { _ = sess.Close() })
	sess.SetNativeModule("image", bindgen.RegisterImage(ctx, sess))
	sess.SetModuleDecls("image", src)
	s.Require().NoError(sess.Exec(ctx, `
import "image";
final set = image\Digest{ algorithm = "sha256", encoded = "ab" }.string();
final unset = image\Digest{}.string();
`))
	s.Equal("sha256:ab", sess.Globals()["set"].AsString())
	s.Empty(sess.Globals()["unset"].AsString())
}
