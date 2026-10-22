package main

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sourcePackages resolves imports between the test's own packages first and the
// standard library second, so a snippet can import another snippet.
type sourcePackages struct {
	built map[string]*types.Package
	std   types.Importer
}

func (s sourcePackages) Import(path string) (*types.Package, error) {
	if p, ok := s.built[path]; ok {
		return p, nil
	}
	return s.std.Import(path)
}

var goRootOnce sync.Once

// ensureGoRoot gives the source importer a GOROOT when a -trimpath build left
// runtime.GOROOT empty and the environment sets none.
func ensureGoRoot(t *testing.T) {
	t.Helper()
	goRootOnce.Do(func() {
		if build.Default.GOROOT != "" {
			return
		}
		out, err := exec.Command("go", "env", "GOROOT").Output()
		require.NoError(t, err)
		build.Default.GOROOT = strings.TrimSpace(string(out))
	})
}

func newSourcePackages(t *testing.T) sourcePackages {
	t.Helper()
	ensureGoRoot(t)
	return sourcePackages{
		built: map[string]*types.Package{},
		std:   importer.ForCompiler(token.NewFileSet(), "source", nil),
	}
}

func (s sourcePackages) check(t *testing.T, path, src string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	pkg, err := (&types.Config{Importer: s}).Check(path, fset, []*ast.File{file}, nil)
	require.NoError(t, err)
	s.built[path] = pkg
	return pkg
}

func renderSource(t *testing.T, src string) []string {
	t.Helper()
	return renderGoAPI([]*types.Package{newSourcePackages(t).check(t, "example.com/m", src)})
}

func TestRenderGoAPIOrdersSymbolsByNameAndMembersUnderTheirType(t *testing.T) {
	got := renderSource(t, `package m

type Zeta struct{ B, A int }
func (Zeta) Y() {}
func (Zeta) X() {}
type Alpha struct{}
func Beta() {}
const Gamma = 1
var Delta string
`)
	assert.Equal(t, []string{
		"example.com/m.Alpha type struct",
		"example.com/m.Beta func()",
		"example.com/m.Delta var string",
		"example.com/m.Gamma const untyped int = 1",
		"example.com/m.Zeta type struct",
		"example.com/m.Zeta.A field int",
		"example.com/m.Zeta.B field int",
		"example.com/m.Zeta.X method func()",
		"example.com/m.Zeta.Y method func()",
	}, got)
}

func TestRenderGoAPIEmitsLinesInPlainByteOrder(t *testing.T) {
	got := renderSource(t, `package m

type Foo struct{ Bar int }
type Foo2 struct{}
type FooBar struct{}
type Store interface{ Get() }
`)
	assert.Equal(t, []string{
		"example.com/m.Foo type struct",
		"example.com/m.Foo.Bar field int",
		"example.com/m.Foo2 type struct",
		"example.com/m.FooBar type struct",
		"example.com/m.Store type interface",
		"example.com/m.Store.Get imethod func()",
	}, got)
	assert.True(t, slices.IsSorted(got), "the lockfiles-are-sorted lint reads the file in byte order")
}

func TestRenderGoAPIOrdersPackagesByImportPath(t *testing.T) {
	std := newSourcePackages(t)
	b := std.check(t, "example.com/m/sub", "package sub\nfunc B() {}\n")
	a := std.check(t, "example.com/m", "package m\nfunc A() {}\n")

	forward := renderGoAPI([]*types.Package{a, b})
	backward := renderGoAPI([]*types.Package{b, a})

	assert.Equal(t, []string{"example.com/m.A func()", "example.com/m/sub.B func()"}, forward)
	assert.Equal(t, forward, backward, "the order the packages are given in must not reach the lock")
}

func TestRenderGoAPILeavesOutUnexportedDeclarations(t *testing.T) {
	got := renderSource(t, `package m

type Public struct {
	Shown  int
	hidden int
}
func (Public) Visible() {}
func (Public) invisible() {}
type private struct{ X int }
func helper() {}
var secret = 1
`)
	assert.Equal(t, []string{
		"example.com/m.Public type struct",
		"example.com/m.Public.Shown field int",
		"example.com/m.Public.Visible method func()",
	}, got)
}

func TestRenderGoAPIMethodSetsSeparateValueFromPointerReceivers(t *testing.T) {
	got := renderSource(t, `package m

type Base struct{}
func (Base) FromBase() {}
func (*Base) PointerFromBase() {}

type Counter struct{ Base }
func (c Counter) Get() int { return 0 }
func (c *Counter) Add(delta int) error { return nil }
`)
	assert.Equal(t, []string{
		"example.com/m.Base type struct",
		"example.com/m.Base.FromBase method func()",
		"example.com/m.Base.PointerFromBase method(ptr) func()",
		"example.com/m.Counter type struct",
		"example.com/m.Counter.Add method(ptr) func(delta int) error",
		"example.com/m.Counter.Base embedded Base",
		"example.com/m.Counter.FromBase method func()",
		"example.com/m.Counter.Get method func() int",
		"example.com/m.Counter.PointerFromBase method(ptr) func()",
	}, got, "promoted methods are part of the method set a caller sees")
}

func TestRenderGoAPIRendersGenericTypeParameters(t *testing.T) {
	got := renderSource(t, `package m

type Number interface{ ~int | ~float64 }

type Pair[K comparable, V any] struct {
	Key K
	Val V
}

func (p Pair[K, V]) Swap() (V, K) { return p.Val, p.Key }

func Sum[T Number](xs ...T) T { var z T; return z }

type Set[T comparable] map[T]struct{}
`)
	assert.Equal(t, []string{
		"example.com/m.Number type interface",
		"example.com/m.Number.embeds ~int | ~float64",
		"example.com/m.Pair type[K comparable, V any] struct",
		"example.com/m.Pair.Key field K",
		"example.com/m.Pair.Swap method func() (V, K)",
		"example.com/m.Pair.Val field V",
		"example.com/m.Set type[T comparable] map[T]struct{}",
		"example.com/m.Sum func[T Number](xs ...T) T",
	}, got)
}

func TestRenderGoAPIRendersEmbeddedFieldsAndTags(t *testing.T) {
	got := renderSource(t, `package m

import "io"

type Inner struct{}
type Outer struct {
	Inner
	*io.PipeReader
	io.Writer
	Named int `+"`json:\"named,omitempty\"`"+`
	embeddedHidden
}
type embeddedHidden struct{}
`)
	assert.Contains(t, got, "example.com/m.Outer.Inner embedded Inner")
	assert.Contains(t, got, "example.com/m.Outer.PipeReader embedded *io.PipeReader")
	assert.Contains(t, got, "example.com/m.Outer.Writer embedded io.Writer")
	assert.Contains(t, got, "example.com/m.Outer.Named field int \"json:\\\"named,omitempty\\\"\"")
	assert.NotContains(t, got, "example.com/m.Outer.embeddedHidden embedded embeddedHidden")
}

func TestRenderGoAPIRendersInterfaceMethodsAndEmbeds(t *testing.T) {
	got := renderSource(t, `package m

import "io"

type Store interface {
	io.Closer
	Get(key string) ([]byte, error)
	put(key string)
}
`)
	assert.Equal(t, []string{
		"example.com/m.Store type interface",
		"example.com/m.Store.Get imethod func(key string) ([]byte, error)",
		"example.com/m.Store.embeds io.Closer",
	}, got)
}

func TestRenderGoAPIRendersConstantsAndVariablesWithTheirTypes(t *testing.T) {
	got := renderSource(t, `package m

import "time"

type Level int

const (
	Low Level = iota
	High
)
const Name = "magus"
const Timeout time.Duration = 3
var Default = []string{"a"}
var Err error
`)
	assert.Equal(t, []string{
		"example.com/m.Default var []string",
		"example.com/m.Err var error",
		"example.com/m.High const Level = 1",
		"example.com/m.Level type int",
		"example.com/m.Low const Level = 0",
		"example.com/m.Name const untyped string = \"magus\"",
		"example.com/m.Timeout const time.Duration = 3",
	}, got)
}

func TestRenderGoAPIExpandsAnAliasOfATypeOutsideThePublicSet(t *testing.T) {
	pk := newSourcePackages(t)
	pk.check(t, "example.com/m/internal/inner", `package inner

type Engine struct{ Name string }
func (*Engine) Start() error { return nil }
func (Engine) Label() string { return "" }
`)
	root := pk.check(t, "example.com/m", `package m

import "example.com/m/internal/inner"

type Engine = inner.Engine
`)
	got := renderGoAPI([]*types.Package{root})

	assert.Equal(t, []string{
		"example.com/m.Engine type = example.com/m/internal/inner.Engine",
		"example.com/m.Engine.Label method func() string",
		"example.com/m.Engine.Name field string",
		"example.com/m.Engine.Start method(ptr) func() error",
	}, got, "the lock must pin what a caller can do with the alias, not only its name")
}

func TestRenderGoAPIDoesNotExpandAnAliasOfAPublicType(t *testing.T) {
	pk := newSourcePackages(t)
	base := pk.check(t, "example.com/m/base", "package base\ntype T struct{ X int }\n")
	root := pk.check(t, "example.com/m", "package m\nimport \"example.com/m/base\"\ntype T = base.T\n")

	got := renderGoAPI([]*types.Package{root, base})

	assert.Equal(t, []string{
		"example.com/m.T type = example.com/m/base.T",
		"example.com/m/base.T type struct",
		"example.com/m/base.T.X field int",
	}, got, "the field is pinned once, under its own package")
}

func TestRenderGoAPIQualifiesForeignTypesWithTheirFullPath(t *testing.T) {
	pk := newSourcePackages(t)
	pk.check(t, "example.com/m/other", "package other\ntype Kind int\n")
	got := renderGoAPI([]*types.Package{pk.check(t, "example.com/m", `package m

import "example.com/m/other"

func Classify(k other.Kind) other.Kind { return k }
`)})
	assert.Equal(t, []string{
		"example.com/m.Classify func(k example.com/m/other.Kind) example.com/m/other.Kind",
	}, got)
}

func TestRenderGoAPIIsDeterministic(t *testing.T) {
	src := `package m
type A struct{ X, Y int }
func (A) M() {}
type B interface{ M() }
func F() {}
`
	first := renderSource(t, src)
	for range 5 {
		require.Equal(t, first, renderSource(t, src))
	}
}

// leakSource type-checks src as example.com/m beside an internal package that declares
// T, R (whose method takes a T) and Option, and returns the leaks internalLeaks finds.
func leakSource(t *testing.T, src string) []string {
	t.Helper()
	pk := newSourcePackages(t)
	pk.check(t, "example.com/m/internal/x", `package x

type T struct{}
type R struct{}
func (*R) Use(T) {}
type Option func(*T)
`)
	return internalLeaks([]*types.Package{pk.check(t, "example.com/m", src)})
}

func TestInternalLeaksFindsAParameter(t *testing.T) {
	got := leakSource(t, "package m\nimport \"example.com/m/internal/x\"\nfunc F(v x.T) {}\n")
	assert.Equal(t, []string{"example.com/m.F names example.com/m/internal/x.T"}, got)
}

func TestInternalLeaksFindsAResult(t *testing.T) {
	got := leakSource(t, "package m\nimport \"example.com/m/internal/x\"\nfunc G() []*x.T { return nil }\n")
	assert.Equal(t, []string{"example.com/m.G names example.com/m/internal/x.T"}, got)
}

func TestInternalLeaksFindsAStructField(t *testing.T) {
	got := leakSource(t, "package m\nimport \"example.com/m/internal/x\"\ntype S struct{ F map[string]x.T; hidden x.T }\n")
	assert.Equal(t, []string{"example.com/m.S.F names example.com/m/internal/x.T"}, got,
		"an unexported field is no caller's business")
}

func TestInternalLeaksFindsAMethodOfAnAliasedType(t *testing.T) {
	got := leakSource(t, "package m\nimport \"example.com/m/internal/x\"\ntype R = x.R\n")
	assert.Equal(t, []string{"example.com/m.R.Use names example.com/m/internal/x.T"}, got,
		"R itself is re-exported, but the T its method takes is not")
}

func TestInternalLeaksExcusesATypeTheSamePackageAliases(t *testing.T) {
	got := leakSource(t, `package m

import "example.com/m/internal/x"

type Option = x.Option

func With() Option { return nil }
`)
	assert.Empty(t, got, "the alias is how a caller names the type, so naming it is no leak")
}

func TestInternalLeaksExcusesAnAliasOfAnExternalType(t *testing.T) {
	got := leakSource(t, "package m\nimport \"bytes\"\ntype Buffer = bytes.Buffer\nfunc New() *Buffer { return nil }\n")
	assert.Empty(t, got)
}

func TestInternalLeaksFindsADeclaredFuncType(t *testing.T) {
	got := leakSource(t, "package m\nimport \"example.com/m/internal/x\"\ntype Hook func(x.T) error\n")
	assert.Equal(t, []string{"example.com/m.Hook names example.com/m/internal/x.T"}, got)
}

func TestPublicGoPackagesNamesDistinctPackages(t *testing.T) {
	seen := map[string]bool{}
	for _, path := range publicGoPackages {
		assert.False(t, seen[path], "%s is listed twice", path)
		seen[path] = true
	}
	assert.True(t, seen["github.com/egladman/magus"], "the root package is the SDK")
}

func TestRunGoAPIRejectsAnUnknownFlag(t *testing.T) {
	assert.Error(t, runGoAPI([]string{"-nosuchflag"}))
}

func renderRepoLock(t *testing.T) []string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "api-go.lock")
	require.NoError(t, runGoAPI([]string{"-dir", "../..", "-out", out}))
	body, err := os.ReadFile(out)
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
}

// TestGoAPILockUpToDate is the drift gate for the committed Go API lock: a public
// signature cannot change without the diff appearing in the PR. On failure,
// regenerate:
//
//	magus run go-api-generate .
func TestGoAPILockUpToDate(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "..", "internal", "commands", "testdata", "api-go.lock"))
	require.NoError(t, err, "read internal/commands/testdata/api-go.lock")

	assert.Equal(t, strings.Split(strings.TrimSuffix(string(committed), "\n"), "\n"), renderRepoLock(t),
		"internal/commands/testdata/api-go.lock is out of date; run: magus run go-api-generate .")
}

func TestGoAPILockCoversEveryPublicPackage(t *testing.T) {
	lines := renderRepoLock(t)

	require.NotEmpty(t, lines)
	assert.True(t, slices.IsSorted(lines), "the lock must stay in byte order")
	for _, path := range publicGoPackages {
		found := slices.ContainsFunc(lines, func(l string) bool {
			return strings.HasPrefix(l, path+".")
		})
		assert.True(t, found, "no declaration of %s reached the lock", path)
	}
}
