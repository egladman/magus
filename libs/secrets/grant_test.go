package secrets

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type GrantSuite struct{ suite.Suite }

func TestGrant(t *testing.T) { suite.Run(t, new(GrantSuite)) }

// refused asserts g fails Normalize with a *GrantError whose message contains want.
func (s *GrantSuite) refused(g Grant, want string) {
	s.T().Helper()
	_, err := g.Normalize()
	var ge *GrantError
	s.Require().ErrorAs(err, &ge, "grant %+v refused without a GrantError", g)
	s.Contains(ge.Error(), want)
}

func (s *GrantSuite) TestNormalizeRequiresEveryField() {
	s.refused(Grant{Host: "h", Header: "A"}, "secrets: grant: ref is required")
	s.refused(Grant{Ref: "R", Header: "A"}, `secrets: grant "R": host is required`)
	s.refused(Grant{Ref: "R", Host: "h"}, "header is required")
}

func (s *GrantSuite) TestNormalizeRefusesAWildcardHost() {
	s.refused(Grant{Ref: "R", Host: "*.example.com", Header: "A"}, "wildcard")
}

func (s *GrantSuite) TestNormalizeRefusesAURLForAHost() {
	s.refused(Grant{Ref: "R", Host: "https://x.com/y", Header: "A"}, "bare host[:port]")
}

// U+017F folds to "s" under Unicode case folding, so a non-ASCII host could satisfy a
// grant for a different, ASCII destination.
func (s *GrantSuite) TestNormalizeRefusesANonASCIIHost() {
	s.refused(Grant{Ref: "R", Host: "hookſ.slack.com", Header: "A"}, "not ASCII")
}

func (s *GrantSuite) TestNormalizeRefusesAnInvalidHeaderName() {
	s.refused(Grant{Ref: "R", Host: "h", Header: "X Api Key"}, "not a valid HTTP header name")
}

func (s *GrantSuite) TestNormalizeRefusesANewlineInRefOrPrefix() {
	s.refused(Grant{Ref: "R\n", Host: "h", Header: "A", Prefix: "x\r\n"}, "prefix must not contain a newline")
	s.refused(Grant{Ref: "R\nS", Host: "h", Header: "A"}, "ref must not contain a newline")
}

func (s *GrantSuite) TestNormalizeTrimsWhatItChecks() {
	g, err := Grant{Ref: " R ", Host: " api.example.com", Header: " Authorization "}.Normalize()
	s.Require().NoError(err)
	s.Equal(Grant{Ref: "R", Host: "api.example.com", Header: "Authorization"}, g)
}

// g.Host is what the forwarder dials, so the spellings of one destination fold onto one
// form, while two different destinations stay two.
func (s *GrantSuite) TestNormalizeFoldsOneDestinationsSpellings() {
	g, err := Grant{Ref: "R", Host: "API.Example.com.:443", Header: "Authorization"}.Normalize()
	s.Require().NoError(err)
	s.Equal("api.example.com", g.Host)

	v6, err := Grant{Ref: "R", Host: "[::1]:443", Header: "Authorization"}.Normalize()
	s.Require().NoError(err)
	s.Equal("[::1]", v6.Host, "a first-colon split would mangle IPv6")
}

// The forwarder writes Host into the upstream URL, where an unbracketed IPv6 literal
// does not parse as a host.
func (s *GrantSuite) TestNormalizeKeepsIPv6Brackets() {
	for _, in := range []string{"[::1]", "::1", "[::1]:443", "[::1]."} {
		g, err := Grant{Ref: "R", Host: in, Header: "Authorization"}.Normalize()
		s.Require().NoError(err)
		s.Equal("[::1]", g.Host, "host %q", in)
	}
}

func (s *GrantSuite) TestNormalizeKeepsANonDefaultPort() {
	v6p, err := Grant{Ref: "R", Host: "[::1]:8443", Header: "Authorization"}.Normalize()
	s.Require().NoError(err)
	s.Equal("[::1]:8443", v6p.Host)

	p80, err := Grant{Ref: "R", Host: "api.example.com:80", Header: "Authorization"}.Normalize()
	s.Require().NoError(err)
	s.Equal("api.example.com:80", p80.Host, ":80 is not folded into the bare name")
}
