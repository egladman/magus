package interp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit"
)

type ChoicesSuite struct {
	suite.Suite
	prevBindings BuzzHostBindingsFn
}

func TestChoicesSuite(t *testing.T) {
	suite.Run(t, new(ChoicesSuite))
}

// SetupTest installs a host module whose functions record through the ctx the bindings
// were built with, as magus\cache.remote and magus\secret.provider do.
func (s *ChoicesSuite) SetupTest() {
	s.prevBindings = buzzHostBindingsFn
	buzzHostBindingsFn = func(bindCtx context.Context, sess *buzz.Session, _ map[string]vm.Callable, _ map[string]vm.Value, _ bool) error {
		host := vm.NewMap()
		host.MapSet("backend", vm.DirectValue("host.backend", func(_ context.Context, args []vm.Value) (vm.Value, error) {
			return vm.Null, RecordRemoteBackend(bindCtx, args[0].AsString())
		}))
		host.MapSet("provider", vm.DirectValue("host.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
			return vm.Null, RecordSecretProvider(bindCtx, args[0].AsString())
		}))
		sess.SetNativeModule("host", host)
		sess.SetNativeModule("magus", vm.NewMap())
		return spell.DeclareMagusTypes(sess, func(string) bool { return false })
	}
}

func (s *ChoicesSuite) TearDownTest() {
	buzzHostBindingsFn = s.prevBindings
}

func (s *ChoicesSuite) parse(magusfile string) (Parsed, error) {
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(magusfile)
	src, err := Find(w.Root())
	s.Require().NoError(err)
	return Parse(s.T().Context(), src)
}

func (s *ChoicesSuite) TestParseReturnsTheChoices() {
	parsed, err := s.parse("import \"magus\";\nimport \"host\";\n" +
		"host\\backend(\"aws-s3\");\nhost\\provider(\"onepassword\");\nhost\\provider(\"onepassword\");\n" +
		"export fun build(ctx: magus\\Context, args: [str]) > void {}\n")
	s.Require().NoError(err, "naming the same spell twice passes")
	s.Equal(Choices{SecretProvider: "onepassword", RemoteBackend: "aws-s3"}, parsed.Choices)
	s.Require().Len(parsed.Targets, 1)
	s.Equal("build", parsed.Targets[0].Key)
}

func (s *ChoicesSuite) TestParseRefusesASecondBackend() {
	_, err := s.parse("import \"magus\";\nimport \"host\";\n" +
		"host\\backend(\"aws-s3\");\nhost\\backend(\"gcs-cache\");\n" +
		"export fun build(ctx: magus\\Context, args: [str]) > void {}\n")
	s.Require().Error(err)
	s.Contains(err.Error(), `magus\cache.remote: this magusfile chose "aws-s3" and then "gcs-cache"`)
}

func (s *ChoicesSuite) TestRecordConflictIsTyped() {
	ctx, rec := WithChoiceRecord(s.T().Context())
	s.Require().NoError(RecordSecretProvider(ctx, "onepassword"))
	err := RecordSecretProvider(ctx, "system-keychain")
	var conflict *ChoiceConflictError
	s.Require().ErrorAs(err, &conflict)
	s.Equal(ChoiceConflictError{Declaration: declSecretProvider, First: "onepassword", Second: "system-keychain"}, *conflict)
	s.Equal("onepassword", rec.Choices().SecretProvider, "the first choice stands")
}

func (s *ChoicesSuite) TestRecordWithoutAnEvaluationIsRefused() {
	var outside *outsideMagusfileError
	s.Require().ErrorAs(RecordRemoteBackend(s.T().Context(), "aws-s3"), &outside)
	s.Equal(declRemoteBackend, outside.Declaration)
	s.Require().ErrorAs(RecordSecretProvider(s.T().Context(), "onepassword"), &outside)
	s.Equal(declSecretProvider, outside.Declaration)
	s.Contains(outside.Error(), "is a magusfile declaration")
}

func (s *ChoicesSuite) TestMergePassesIdenticalChoices() {
	var m ChoiceMerge
	s.Require().NoError(m.Add("api", Choices{RemoteBackend: "aws-s3"}))
	s.Require().NoError(m.Add("web", Choices{RemoteBackend: "aws-s3", SecretProvider: "onepassword"}))
	s.Require().NoError(m.Add("docs", Choices{}))
	s.Equal(Choices{SecretProvider: "onepassword", RemoteBackend: "aws-s3"}, m.Choices())
}

func (s *ChoicesSuite) TestMergeRefusesDifferentChoices() {
	var m ChoiceMerge
	s.Require().NoError(m.Add("api", Choices{RemoteBackend: "aws-s3"}))
	err := m.Add("web", Choices{RemoteBackend: "gcs-cache", SecretProvider: "onepassword"})
	var conflict *ChoiceConflictError
	s.Require().ErrorAs(err, &conflict)
	s.Equal(ChoiceConflictError{
		Declaration: declRemoteBackend, First: "aws-s3", Second: "gcs-cache",
		FirstProject: "api", SecondProject: "web",
	}, *conflict)
	s.Contains(err.Error(), `project "api" chose "aws-s3" and project "web" chose "gcs-cache"`)
	s.Equal(Choices{RemoteBackend: "aws-s3"}, m.Choices(), "a refused set is not folded")
}
