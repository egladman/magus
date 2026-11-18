package job

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type AttestSuite struct {
	gradingSuite
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func TestAttestSuite(t *testing.T) { suite.Run(t, new(AttestSuite)) }

func (s *AttestSuite) SetupTest() {
	s.gradingSuite.SetupTest()
	var err error
	s.pub, s.priv, err = ed25519.GenerateKey(nil)
	s.Require().NoError(err)
	s.store = s.open(WithVerifier(s.verify))
}

func (s *AttestSuite) verify(payload, sig []byte) (string, error) {
	if !ed25519.Verify(s.pub, payload, sig) {
		return "", errors.New("ed25519: bad signature")
	}
	return "ci", nil
}

func (s *AttestSuite) statement(exit int, started time.Time) []byte {
	b, err := codec.Marshal(Statement{Job: "j1", Postcondition: "tests", Check: testCheck(), Tree: s.tree, Exit: exit,
		Started: started, Ended: started.Add(time.Second)})
	s.Require().NoError(err)
	return b
}

func (s *AttestSuite) TestAnAttestedPassVerifies() {
	s.declare(checkPostconditionAccepting("tests", BasisWitnessed, BasisAttested))
	payload := s.statement(0, time.Now().Add(time.Second))

	ref, err := s.store.Attest(s.T().Context(), payload, ed25519.Sign(s.priv, payload))

	s.Require().NoError(err)
	g := s.grade(ref)
	s.True(g.Verified, g.Violations)
	s.Equal(BasisAttested, g.Basis)
}

func (s *AttestSuite) TestAnAttestedFailureFails() {
	s.declare(checkPostconditionAccepting("tests", BasisAttested))
	payload := s.statement(2, time.Now().Add(time.Second))

	ref, err := s.store.Attest(s.T().Context(), payload, ed25519.Sign(s.priv, payload))

	s.Require().NoError(err)
	g := s.grade(ref)
	s.False(g.Passed)
	s.Contains(g.Violations, "the check exited 2")
}

func (s *AttestSuite) TestAnEditedStatementIsRefused() {
	s.declare(checkPostconditionAccepting("tests", BasisAttested))
	now := time.Now()
	sig := ed25519.Sign(s.priv, s.statement(1, now))

	_, err := s.store.Attest(s.T().Context(), s.statement(0, now), sig)

	_, ok := errors.AsType[*SignatureError](err)
	s.True(ok, err)
}

func (s *AttestSuite) TestAVerifierNamingNoSignerIsRefused() {
	s.declare(checkPostconditionAccepting("tests", BasisAttested))
	st := s.open(WithVerifier(func([]byte, []byte) (string, error) { return "", nil }))

	_, err := st.Attest(s.T().Context(), s.statement(0, time.Now()), nil)

	s.ErrorContains(err, "named no signer")
}

func (s *AttestSuite) TestAStoreWithNoVerifierRefuses() {
	_, err := s.open().Attest(s.T().Context(), s.statement(0, time.Now()), nil)

	refused, ok := errors.AsType[*GradingUnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithVerifier", refused.Option)
}

func (s *AttestSuite) TestAPayloadThatIsNotAStatementIsRefused() {
	payload := []byte(`{"job":"j1","postcondition":"tests","by":"me"}`)

	_, err := s.store.Attest(s.T().Context(), payload, ed25519.Sign(s.priv, payload))

	_, ok := errors.AsType[*StatementError](err)
	s.True(ok, err)
}

func (s *AttestSuite) TestARunAttestedBeforeTheJobWasDeclaredIsAViolation() {
	s.declare(checkPostconditionAccepting("tests", BasisAttested))
	payload := s.statement(0, time.Now().Add(-time.Hour))

	ref, err := s.store.Attest(s.T().Context(), payload, ed25519.Sign(s.priv, payload))

	s.Require().NoError(err)
	s.Contains(s.grade(ref).Violations, "the run started before the job was declared")
}
