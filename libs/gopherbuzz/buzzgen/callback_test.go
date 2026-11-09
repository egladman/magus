package buzzgen

import (
	"context"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"testing"

	"github.com/stretchr/testify/suite"
)

type CallbackSuite struct {
	suite.Suite
	cbs *Callbacks
}

func TestCallback(t *testing.T) { suite.Run(t, new(CallbackSuite)) }

const callbackFixture = `package fixture

import "context"

func Walk(root string, cb func(ctx context.Context, path string, isDir bool) (bool, error)) {}

func Unnamed(cb func(context.Context, int) error) {}

func First(cb func(ctx context.Context, a string) error) {}

func Second(cb func(ctx context.Context, b string) error) {}

var Extern = (func(ctx context.Context, rule func(ctx context.Context, n int64) (any, error)) error)(nil)
`

func (s *CallbackSuite) SetupSuite() {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", callbackFixture, 0)
	s.Require().NoError(err)
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("example.com/fixture", fset, []*ast.File{file}, info)
	s.Require().NoError(err)
	s.cbs = collectCallbacks("example.com/fixture", []*ast.File{file}, info)
}

func (s *CallbackSuite) TestParamsAreTheNamesAfterTheContext() {
	got, err := s.cbs.Params(reflect.TypeFor[func(context.Context, string, bool) (bool, error)]())
	s.Require().NoError(err)
	s.Equal([]string{"path", "isDir"}, got)
}

func (s *CallbackSuite) TestParamsReadAConversion() {
	got, err := s.cbs.Params(reflect.TypeFor[func(context.Context, int64) (any, error)]())
	s.Require().NoError(err)
	s.Equal([]string{"n"}, got)
}

func (s *CallbackSuite) TestAnUnnamedParameterIsRefused() {
	_, err := s.cbs.Params(reflect.TypeFor[func(context.Context, int) error]())
	s.ErrorContains(err, "leaves parameter 1 unnamed")
}

func (s *CallbackSuite) TestTwoSpellingsOfOneShapeAreRefused() {
	_, err := s.cbs.Params(reflect.TypeFor[func(context.Context, string) error]())
	s.ErrorContains(err, "with parameters (a) and (b); name them alike")
}

func (s *CallbackSuite) TestAnUndeclaredShapeIsRefused() {
	_, err := s.cbs.Params(reflect.TypeFor[func(context.Context, float64) error]())
	s.ErrorContains(err, "no parameter of example.com/fixture declares callback")
}

func (s *CallbackSuite) TestFuncTypeLabelsTheParameters() {
	got, err := FuncType(reflect.TypeFor[func(context.Context, string, bool) (bool, error)](), []string{"path", "isDir"}, DefaultOptions())
	s.Require().NoError(err)
	s.Equal("fun (path: str, isDir: bool) > bool !> any", got)
}

func (s *CallbackSuite) TestFuncTypeOfAnErrorOnlyCallbackIsVoid() {
	got, err := FuncType(reflect.TypeFor[func(context.Context) error](), nil, DefaultOptions())
	s.Require().NoError(err)
	s.Equal("fun () > void !> any", got)
}

func (s *CallbackSuite) TestFuncTypeOfAnInterfaceResultIsAny() {
	got, err := FuncType(reflect.TypeFor[func(context.Context, []string) (any, error)](), []string{"paths"}, DefaultOptions())
	s.Require().NoError(err)
	s.Equal("fun (paths: [str]) > any !> any", got)
}
