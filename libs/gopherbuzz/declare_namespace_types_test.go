package buzz

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/suite"
)

const (
	orderDecls = "namespace shop\\order;\n\nexport object Line {\n    qty: int = 3,\n}\n"
	cartDecls  = "import \"shop/order\";\n\nexport object Cart {\n    line: shop\\order\\Line = shop\\order\\Line{},\n}\n"
)

type declareNamespaceTypesSuite struct {
	suite.Suite
	ctx  context.Context
	sess *Session
}

func TestDeclareNamespaceTypes(t *testing.T) { suite.Run(t, new(declareNamespaceTypesSuite)) }

func (s *declareNamespaceTypesSuite) SetupTest() {
	s.ctx = context.Background()
	s.sess = NewSession(s.ctx, WithEmbedded())
	s.T().Cleanup(func() { _ = s.sess.Close() })
	s.sess.SetModuleDecls("shop/order", orderDecls)
}

// definitions runs the declarations in a session of their own, as a host
// does, and returns the runtime definitions of Line and Cart.
func (s *declareNamespaceTypesSuite) definitions() (line, cart vm.Value) {
	host := NewSession(s.ctx, WithEmbedded())
	s.T().Cleanup(func() { _ = host.Close() })
	host.SetModuleDecls("shop/order", orderDecls)
	s.Require().NoError(host.Exec(s.ctx, cartDecls))
	return host.GetGlobal(QualifiedTypeKey(`shop\order`, "Line")), host.GetGlobal("Cart")
}

func (s *declareNamespaceTypesSuite) TestATypeIsNotNameableBare() {
	s.Require().NoError(s.sess.DeclareNamespaceTypes(`shop\order`, orderDecls))

	s.Error(s.sess.Exec(s.ctx, "final line = Line{};"))
}

func (s *declareNamespaceTypesSuite) TestAProgramsOwnTypeOfTheNameStands() {
	s.Require().NoError(s.sess.DeclareNamespaceTypes(`shop\order`, orderDecls))

	s.NoError(s.sess.Exec(s.ctx, "object Line { sku: str = \"a\" }\nfinal sku = Line{}.sku;"))
}

func (s *declareNamespaceTypesSuite) TestNothingRunsOrIsBound() {
	s.Require().NoError(s.sess.DeclareNamespaceTypes(`shop\order`, orderDecls))

	s.True(s.sess.GetGlobal(QualifiedTypeKey(`shop\order`, "Line")).IsNull())
	s.True(s.sess.GetGlobal("shop").IsNull())
	s.True(s.sess.GetGlobal("Line").IsNull())
}

func (s *declareNamespaceTypesSuite) TestADefaultFillsItsFieldsAtTheLiteral() {
	line, cart := s.definitions()
	s.sess.SetNativeModule("example/shop", vm.NewMap())
	s.Require().NoError(s.sess.DeclareNamespaceTypes(`shop\order`, orderDecls))
	s.sess.DeclareModuleTypes("shop", cartDecls)
	s.sess.SetGlobal(QualifiedTypeKey(`shop\order`, "Line"), line)
	s.sess.SetGlobal(QualifiedTypeKey("shop", "Cart"), cart)
	s.Require().NoError(s.sess.Exec(s.ctx, "import \"example/shop\";\nexport fun qty() > int { return shop\\Cart{}.line.qty; }"))

	got, err := s.sess.CallValue(s.ctx, s.sess.Exports()["qty"], nil)

	s.Require().NoError(err)
	s.Equal(int64(3), got.AsInt())
}

func (s *declareNamespaceTypesSuite) TestASourceThatDoesNotParseIsAnError() {
	s.ErrorContains(s.sess.DeclareNamespaceTypes(`shop\order`, "export object {"), "do not parse")
}
