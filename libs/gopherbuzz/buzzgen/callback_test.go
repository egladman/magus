package buzzgen

import (
	"context"
	"os"
	"path/filepath"
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

// The fixture loads through LoadCallbacks, whose go/packages asks the go
// command for the standard library; go/importer asks runtime.GOROOT,
// which a -trimpath test binary leaves empty without GOROOT set.
func (s *CallbackSuite) SetupSuite() {
	dir := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.22\n"), 0o600))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(callbackFixture), 0o600))
	cbs, err := LoadCallbacks(dir, "example.com/fixture")
	s.Require().NoError(err)
	s.cbs = cbs
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
