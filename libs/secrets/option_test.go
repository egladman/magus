package secrets

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type OptionSuite struct{ suite.Suite }

func TestOption(t *testing.T) { suite.Run(t, new(OptionSuite)) }

func (s *OptionSuite) TestNoOptionsMeansTheDefaultTimeouts() {
	s.Equal(DefaultTimeouts(), newTestResolver(s.T()).Timeouts())
}

// A partly specified configuration must not zero the other budget: a zero timeout would
// make every provider read fail instantly.
func (s *OptionSuite) TestWithTimeoutsKeepsDefaultsForZeroFields() {
	only := newTestResolver(s.T(), WithTimeouts(Timeouts{Interactive: 5 * time.Second}))
	s.Equal(5*time.Second, only.Timeouts().Interactive)
	s.Equal(DefaultTimeouts().Unattended, only.Timeouts().Unattended)

	both := newTestResolver(s.T(), WithTimeouts(Timeouts{Interactive: time.Second, Unattended: 2 * time.Second}))
	s.Equal(Timeouts{Interactive: time.Second, Unattended: 2 * time.Second}, both.Timeouts())
}

func (s *OptionSuite) TestConfigTimeoutsKeepDefaultsForZeroFields() {
	r, err := NewResolver(Config{Timeouts: Timeouts{Unattended: 3 * time.Second}})
	s.Require().NoError(err)
	s.Equal(Timeouts{Interactive: DefaultTimeouts().Interactive, Unattended: 3 * time.Second}, r.Timeouts())
}

// requireOptionError asserts opt makes NewResolver refuse, naming option.
func (s *OptionSuite) requireOptionError(option string, opt Option) {
	r, err := NewResolver(Config{}, opt)
	s.Nil(r)
	var oe *OptionError
	s.Require().ErrorAs(err, &oe)
	s.Equal(option, oe.Option)
	s.Contains(err.Error(), "secrets: option "+option+": ")
}

func (s *OptionSuite) TestWithOpenerNilIsAnOptionError() {
	s.requireOptionError("WithOpener", WithOpener(nil))
}

func (s *OptionSuite) TestWithShortSecretNilIsAnOptionError() {
	s.requireOptionError("WithShortSecret", WithShortSecret(nil))
}

func (s *OptionSuite) TestWithLoggerNilIsAnOptionError() {
	s.requireOptionError("WithLogger", WithLogger(nil))
}

func (s *OptionSuite) TestWithTimeoutsNegativeIsAnOptionError() {
	s.requireOptionError("WithTimeouts", WithTimeouts(Timeouts{Interactive: -time.Second}))
}

func (s *OptionSuite) TestEveryRecordedOptionErrorIsReported() {
	_, err := NewResolver(Config{}, WithOpener(nil), WithLogger(nil))
	s.Require().Error(err)
	s.Contains(err.Error(), "WithOpener")
	s.Contains(err.Error(), "WithLogger")
}

func (s *OptionSuite) TestNegativeConfigTimeoutIsAnOptionError() {
	_, err := NewResolver(Config{Timeouts: Timeouts{Unattended: -time.Second}})
	var oe *OptionError
	s.Require().ErrorAs(err, &oe)
	s.Equal("Config.Timeouts", oe.Option)
}

func (s *OptionSuite) TestWithOpenerBuildsTheSelectedProvider() {
	var opened string
	r := newTestResolver(s.T(), WithOpener(func(_ context.Context, name string) (Provider, error) {
		opened = name
		return providerFunc(func(context.Context, string) (Secret, error) { return New("from-opener"), nil }), nil
	})).WithProvider("vault")
	v, err := r.Read(s.T().Context(), "REF")
	s.Require().NoError(err)
	s.Equal("from-opener", v.Reveal())
	s.Equal("vault", opened)
}

// Declining to mask a short value is correct; declining in SILENCE would leave the caller
// treating the value as protected with no way to find out it is not.
func (s *OptionSuite) TestWithShortSecretIsToldOfAValueTooShortToMask() {
	var told []string
	r := newTestResolver(s.T(), WithShortSecret(func(_ context.Context, ref string) { told = append(told, ref) }))
	s.T().Setenv("SECRETS_TEST_TINY", "ab")
	_, err := r.Read(s.T().Context(), "SECRETS_TEST_TINY")
	s.Require().NoError(err, "the read succeeds; only the protection is declined")
	_, err = r.Read(s.T().Context(), "SECRETS_TEST_TINY")
	s.Require().NoError(err)
	s.Equal([]string{"SECRETS_TEST_TINY"}, told, "told once per resolved reference, not per read")
}

func (s *OptionSuite) TestWithShortSecretIsNotToldOfAMaskableValue() {
	told := 0
	r := newTestResolver(s.T(), WithShortSecret(func(context.Context, string) { told++ }))
	s.T().Setenv("SECRETS_TEST_LONG_ENOUGH", "long-enough-token")
	_, err := r.Read(s.T().Context(), "SECRETS_TEST_LONG_ENOUGH")
	s.Require().NoError(err)
	s.Zero(told)
}

func (s *OptionSuite) TestWithLoggerReceivesTheResolversRecords() {
	var sink bytes.Buffer
	r := newTestResolver(s.T(), WithLogger(slog.New(slog.NewTextHandler(&sink, nil))))
	r.errorLog(s.T().Context(), "secret.endpoint.server").Print("accept failed")
	s.Contains(sink.String(), "secret.endpoint.server")
	s.Contains(sink.String(), "accept failed")
}

func (s *OptionSuite) TestNoLoggerDiscards() {
	r := newTestResolver(s.T())
	s.NotPanics(func() { r.errorLog(s.T().Context(), "m").Print("dropped") })
}
