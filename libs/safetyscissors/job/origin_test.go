package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type OriginSuite struct{ suite.Suite }

func TestOrigin(t *testing.T) { suite.Run(t, new(OriginSuite)) }

// The label names what each channel said and nothing it did not: an origin with no host and
// no credential reads as its OS user, never as "a person" or "an agent".
func (s *OriginSuite) TestALabelNamesEachChannelItHas() {
	s.Equal("eli", Origin{User: "eli", EntryPoint: EntryPointCLI}.Label())
	s.Equal("eli via claude-code", Origin{User: "eli", EntryPoint: EntryPointHook, Host: "claude-code", Session: "s1"}.Label())
	s.Equal("eli via claude-code via agent a1b2",
		Origin{User: "eli", EntryPoint: EntryPointHook, Host: "claude-code", Session: "s1", Agent: "a1b2"}.Label())
	s.Equal("codex", Origin{Host: "codex"}.Label())
}

func (s *OriginSuite) TestALabelNamesTheCredentialByKindNameAndID() {
	s.Equal("eli via stored console-1 (3fa9c1d2)",
		Origin{User: "eli", EntryPoint: EntryPointRPC, Credential: Credential{Kind: "stored", ID: "3fa9c1d2", Name: "console-1"}}.Label())
	s.Equal("eli via claude-code via operator (0badf00d)",
		Origin{User: "eli", EntryPoint: EntryPointMCP, Host: "claude-code", Credential: Credential{Kind: "operator", ID: "0badf00d"}}.Label())
	s.Equal("stdio", Origin{Credential: Credential{Kind: "stdio"}}.Label())
	s.Equal("eli via 9b2e04aa", Origin{User: "eli", Credential: Credential{ID: "9b2e04aa"}}.Label())
}

func (s *OriginSuite) TestTheServerAndNobodyReadAsThemselves() {
	s.Equal("server", Origin{User: "eli", EntryPoint: EntryPointServer, Credential: Credential{Kind: "operator"}}.Label())
	s.Equal("unattributed", Origin{}.Label())
	s.Equal("unattributed", Origin{Session: "s1", UID: "501"}.Label(), "a session id and a uid name nobody")
}

// A filter names one channel's value, and matches only that value exactly: the rendered
// label is prose, and matching it would tie what a filter selects to its wording.
func (s *OriginSuite) TestNamesMatchesEachFieldExactly() {
	o := Origin{User: "eli", EntryPoint: EntryPointMCP, Host: "claude-code", Agent: "a1b2",
		Credential: Credential{Kind: "stored", ID: "3fa9c1d2", Name: "laptop"}}
	for _, name := range []string{"eli", "claude-code", "a1b2", "stored", "3fa9c1d2", "laptop", "mcp"} {
		s.True(o.Names(name), name)
	}
	for _, name := range []string{"", "el", "eli via claude-code", "claude", "s1", "3fa9", "stored laptop (3fa9c1d2)"} {
		s.False(o.Names(name), name)
	}
}
