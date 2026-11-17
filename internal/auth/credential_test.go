package auth

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

type CredentialSuite struct{ suite.Suite }

func TestCredential(t *testing.T) { suite.Run(t, new(CredentialSuite)) }

// Generate makes quick draw only valid grants, so the laws below are about the lattice magus
// uses rather than about levels Valid refuses.
func (Access) Generate(r *rand.Rand, _ int) reflect.Value {
	all := validGrants()
	return reflect.ValueOf(all[r.Intn(len(all))])
}

// Generate draws any need over the three scopes and the three levels.
func (Need) Generate(r *rand.Rand, _ int) reflect.Value {
	return reflect.ValueOf(Need{Scope: scopes()[r.Intn(3)], Level: Level(r.Intn(3))})
}

func implies(p, q bool) bool { return !p || q }

func (s *CredentialSuite) law(f any) {
	s.T().Helper()
	s.Require().NoError(quick.Check(f, &quick.Config{MaxCount: 2000}))
}

func (s *CredentialSuite) TestWithinIsReflexive() { s.law(func(a Access) bool { return a.Within(a) }) }

func (s *CredentialSuite) TestWithinIsAntisymmetric() {
	s.law(func(a, b Access) bool { return implies(a.Within(b) && b.Within(a), a == b) })
}

func (s *CredentialSuite) TestWithinIsTransitive() {
	s.law(func(a, b, c Access) bool { return implies(a.Within(b) && b.Within(c), a.Within(c)) })
}

// Anything a narrower grant reaches, a wider one reaches too.
func (s *CredentialSuite) TestAllowsIsMonotone() {
	s.law(func(a, b Access, n Need) bool { return implies(a.Within(b) && a.Allows(n), b.Allows(n)) })
}

func (s *CredentialSuite) TestOperatorIsTheTopAndNothingTheBottom() {
	s.law(func(a Access) bool { return a.Within(AccessOperator()) && Access{}.Within(a) })
}

func (s *CredentialSuite) TestAGrantRoundTripsThroughJSON() {
	s.law(func(a Access) bool {
		b, err := codec.Marshal(a)
		if err != nil {
			return false
		}
		var back Access
		return codec.Unmarshal(b, &back) == nil && back == a
	})
}

// The laws above are checked on a sample; the lattice is small enough to check whole, and the
// exhaustive walk is what makes a wrong Within fail every time rather than usually.
func (s *CredentialSuite) TestWithinIsPointwiseOnEveryPair() {
	for _, a := range validGrants() {
		for _, b := range validGrants() {
			want := a.Tokens <= b.Tokens && a.MCP <= b.MCP && a.Console <= b.Console
			s.Equal(want, a.Within(b), "%s within %s", a, b)
		}
	}
}

func (s *CredentialSuite) TestValidateAcceptsEveryValidGrant() {
	for _, g := range validGrants() {
		s.NoError(g.Validate(), g.String())
	}
}

func (s *CredentialSuite) TestValidateRefusesLevelsAScopeHasNoMeaningFor() {
	s.Error(Access{Tokens: LevelRead}.Validate())
	s.Error(Access{MCP: LevelRead}.Validate())
	s.Error(Access{Console: 3}.Validate())
	s.Error(Access{Tokens: 9}.Validate())
}

func (s *CredentialSuite) TestANeedEachScopeMeansIsValid() {
	s.NoError(Need{Scope: ScopeTokens, Level: LevelWrite}.Validate())
	s.NoError(Need{Scope: ScopeMCP, Level: LevelWrite}.Validate())
	s.NoError(Need{Scope: ScopeConsole, Level: LevelRead}.Validate())
	s.NoError(Need{Scope: ScopeConsole, Level: LevelWrite}.Validate())
}

// A guard built on a zero or invalid need would admit every credential, so Validate refuses
// each: none is not a need, and a scope or level magus does not know is not one either.
func (s *CredentialSuite) TestNeedValidateRefusesZeroAndInvalid() {
	s.Error(Need{}.Validate())
	s.Error(Need{Scope: ScopeConsole}.Validate())
	s.Error(Need{Scope: "files", Level: LevelWrite}.Validate())
	s.Error(Need{Scope: ScopeMCP, Level: LevelRead}.Validate())
	s.Error(Need{Scope: ScopeConsole, Level: 7}.Validate())
}

// The presets are hand-pinned: a change to any of them is a change to what a minted token can
// do, and must show in a diff. A share link holds AccessViewer, so it and a viewer see the same
// routes.
func (s *CredentialSuite) TestGrantPresets() {
	s.Equal("tokens=write,mcp=write,console=write", AccessOperator().String())
	s.Equal("mcp=write", AccessConnector().String())
	s.Equal("console=write", AccessConsole().String())
	s.Equal("console=read", AccessViewer().String())
	s.Equal("mcp=write,console=write", accessSocketPeer().String())
	s.Empty(Access{}.String())
}

// A stored grant reads as names, not numbers, and a record naming an unknown level fails to
// decode rather than reading as none.
func (s *CredentialSuite) TestGrantJSONUsesLevelNames() {
	b, err := codec.Marshal(AccessConnector())
	s.Require().NoError(err)
	s.JSONEq(`{"mcp":"write"}`, string(b))
	var g Access
	s.Require().NoError(codec.Unmarshal([]byte(`{"console":"read"}`), &g))
	s.Equal(AccessViewer(), g)
	s.Error(codec.Unmarshal([]byte(`{"console":"admin"}`), &g))
}

func (s *CredentialSuite) TestALevelPastWriteRendersAsItsNumber() {
	s.Equal("level(7)", Level(7).String())
	_, err := Level(7).MarshalText()
	s.Error(err)
}

func (s *CredentialSuite) TestPhrase() {
	s.Empty(Credential{}.Phrase())
	s.Equal("the operator token", Credential{Kind: KindOperator, ID: "0badf00d"}.Phrase())
	s.Equal("token laptop (3fa9c1d2)", Credential{Kind: KindStored, ID: "3fa9c1d2", Name: "laptop"}.Phrase())
	s.Equal("share link 9b2e04aa", Credential{Kind: KindShare, ID: "9b2e04aa"}.Phrase())
	s.Equal("link code 51c0de00", Credential{Kind: KindExchange, ID: "51c0de00", Name: "console-1"}.Phrase())
	s.Equal("stdio", CredentialStdio().Phrase())
	s.Equal("the socket's owner", CredentialSocketPeer().Phrase())
}

func (s *CredentialSuite) TestARecordedCredentialKeepsNoGrant() {
	s.Equal(job.Credential{Kind: KindStored, ID: "3fa9c1d2", Name: "laptop"},
		Credential{Kind: KindStored, ID: "3fa9c1d2", Name: "laptop", Grant: AccessConnector()}.Recorded())
	s.Equal(job.Credential{Kind: KindStdio}, CredentialStdio().Recorded())
}
