package std

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

type ErrorsModuleSuite struct{ suite.Suite }

func TestErrorsModuleSuite(t *testing.T) { suite.Run(t, new(ErrorsModuleSuite)) }

func (s *ErrorsModuleSuite) TestItRegistersAsAnUpstreamModule() {
	i := slices.IndexFunc(Modules, func(m buzz.Module) bool { return m.Name == "errors" })
	s.Require().GreaterOrEqual(i, 0)
	s.Equal([]string{buzz.LabelUpstream}, Modules[i].Labels)
	s.True(strings.HasPrefix(errorsSource, "namespace errors;\n"))
}

func (s *ErrorsModuleSuite) TestAnUnknownMemberIsRefused() {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	Register(sess)

	err := sess.Exec(context.Background(), `import "errors"; final e = errors\NoSuchError.Nope;`)

	s.Error(err)
}
