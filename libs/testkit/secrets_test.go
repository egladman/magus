package testkit

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/secrets"
)

type SecretProviderSuite struct{ suite.Suite }

func TestSecretProvider(t *testing.T) { suite.Run(t, new(SecretProviderSuite)) }

func (s *SecretProviderSuite) TestAResolverReadsThroughItOnce() {
	p := NewSecretProvider(map[string]string{"vault/token": "fake-vault-token"})
	r := secrets.NewResolver(secrets.WithOpener(p.Opener()))
	r.SetProviderName("vault")

	for range 2 {
		v, err := r.Read(s.T().Context(), "vault/token")
		s.Require().NoError(err)
		s.Equal("fake-vault-token", v.Reveal())
	}
	s.Equal(1, p.Fetches("vault/token"), "the resolver memoizes, so the provider is fetched once")
	s.Equal("token="+"***", r.RedactString("token=fake-vault-token"))
}

func (s *SecretProviderSuite) TestAnUnknownReferenceIsAnErrorNamingIt() {
	p := NewSecretProvider(nil)
	_, err := p.Fetch(s.T().Context(), "absent")
	s.Require().ErrorContains(err, `"absent"`)
	s.Equal(1, p.Fetches("absent"))
}
