package result

import (
	"context"
	"fmt"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const answerProgram = `import "result";
export fun answer() > str {
%s
}
`

type ModuleSuite struct {
	suite.Suite
}

func TestModuleSuite(t *testing.T) {
	suite.Run(t, new(ModuleSuite))
}

// answer returns what body returns, run as a function returning str.
func (s *ModuleSuite) answer(body string) string {
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	s.T().Cleanup(func() { _ = sess.Close() })
	require.NoError(s.T(), sess.Provide(buzz.ModuleEnv{Ctx: ctx}, buzzstd.Modules...))
	sess.SetModuleDecls(ModulePath, Source)
	require.NoError(s.T(), sess.Exec(ctx, fmt.Sprintf(answerProgram, body)))
	got, err := sess.CallValue(ctx, sess.Exports()["answer"], nil)
	require.NoError(s.T(), err)
	return got.AsString()
}

func (s *ModuleSuite) TestOkHoldsTheValueAndNoError() {
	got := s.answer(`final r = result\ok::<int>(4);
    return "{r.value} {r.error == null}";`)
	assert.Equal(s.T(), "4 true", got)
}

func (s *ModuleSuite) TestFailCarriesTheMessageAndNoCode() {
	got := s.answer(`final r = result\fail::<int>("bad");
    return "{r.value == null}|{r.error!.message}|{r.error!.code}";`)
	assert.Equal(s.T(), "true|bad|", got)
}

func (s *ModuleSuite) TestPropagatingAnErrorChangesTheValueType() {
	got := s.answer(`final inner = result\fail::<int>("bad");
    final outer = result\Result::<str>{ error = inner.error };
    return outer.error!.message;`)
	assert.Equal(s.T(), "bad", got)
}

func (s *ModuleSuite) TestCaughtNarrowsAThrownString() {
	got := s.answer(`return result\caught::<int>("plain").error!.message;`)
	assert.Equal(s.T(), "plain", got)
}

func (s *ModuleSuite) TestCaughtPrefixesWhatItWasDoing() {
	got := s.answer(`return result\caught::<int>("plain", doing: "reading").error!.message;`)
	assert.Equal(s.T(), "reading: plain", got)
}

func (s *ModuleSuite) TestCaughtKeepsAThrownDiagnosticsFields() {
	got := s.answer(`final d = result\Diagnostic{ code = "X1", message = "m", url = "u", fix = "f" };
    final e = result\caught::<int>(d, doing: "doing").error!;
    return "{e.code}|{e.message}|{e.url}|{e.fix}";`)
	assert.Equal(s.T(), "X1|doing: m|u|f", got)
}

func (s *ModuleSuite) TestCaughtReadsAHostErrorsMapInsteadOfPrintingIt() {
	got := s.answer(`final host: {str: any} = {"code": "MGS1", "message": "no such file", "fix": "create it"};
    final e = result\caught::<int>(host).error!;
    return "{e.code}|{e.message}|{e.url}|{e.fix}";`)
	assert.Equal(s.T(), "MGS1|no such file||create it", got)
}

func (s *ModuleSuite) TestCaughtRendersAnyOtherValue() {
	got := s.answer(`return result\caught::<int>(3).error!.message;`)
	assert.Equal(s.T(), "3", got)
}
