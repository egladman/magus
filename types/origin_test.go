package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	json "github.com/egladman/magus/internal/json"
)

// The label names what each channel said and nothing it did not: an origin with no host and
// no credential reads as its OS user, never as "a person" or "an agent".
func TestOriginLabelNamesEachChannelItHas(t *testing.T) {
	t.Parallel()
	cases := []struct {
		origin Origin
		want   string
	}{
		{Origin{User: "eli", EntryPoint: EntryPointCLI}, "eli"},
		{Origin{User: "eli", EntryPoint: EntryPointHook, Host: "claude-code", Session: "s1"}, "eli via claude-code"},
		{Origin{User: "eli", EntryPoint: EntryPointHook, Host: "claude-code", Session: "s1", Agent: "a1b2"}, "eli via claude-code via agent a1b2"},
		{Origin{User: "eli", EntryPoint: EntryPointRPC, Credential: Credential{Class: ClassStored, ID: "3fa9c1d2", Name: "console-1"}}, "eli via token console-1 (3fa9c1d2)"},
		{Origin{User: "eli", EntryPoint: EntryPointMCP, Host: "claude-code", Credential: Credential{Class: ClassOperator, ID: "0badf00d"}}, "eli via claude-code via the operator token"},
		{Origin{User: "eli", EntryPoint: EntryPointRPC, Credential: Credential{Class: ClassShare, ID: "9b2e04aa"}}, "eli via share link 9b2e04aa"},
		{Origin{User: "eli", EntryPoint: EntryPointDaemon}, "daemon"},
		{Origin{Host: "codex"}, "codex"},
		{Origin{}, "unattributed"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.origin.Label(), "%+v", c.origin)
	}
}

// A filter names one channel's value, and matches only that value exactly: the rendered
// label is prose, and matching it would tie what a filter selects to its wording.
func TestOriginNamesMatchesEachFieldExactly(t *testing.T) {
	t.Parallel()
	o := Origin{User: "eli", EntryPoint: EntryPointMCP, Host: "claude-code", Agent: "a1b2",
		Credential: Credential{Class: ClassStored, ID: "3fa9c1d2", Name: "laptop", Grant: GrantConnector}}
	for _, name := range []string{"eli", "claude-code", "a1b2", "stored", "3fa9c1d2", "laptop", "mcp"} {
		assert.True(t, o.Names(name), name)
	}
	for _, name := range []string{"", "el", "eli via claude-code", "claude", "s1", "3fa9", "mcp=write", "token laptop (3fa9c1d2)"} {
		assert.False(t, o.Names(name), name)
	}
}

// A review draft written before the review route's author was renamed still restores as a
// draft its reader can publish; reading it as an unknown author would discard it.
func TestDiffAuthorReadsTheReviewRouteUnderItsOldName(t *testing.T) {
	t.Parallel()
	var c DiffComment
	require.NoError(t, json.Unmarshal([]byte(`{"id":"c1","path":"a.go","hunk":0,"author":"human","body":"x","resolved":false}`), &c))
	assert.Equal(t, DiffAuthorUnattributed, c.Author)
	require.NoError(t, json.Unmarshal([]byte(`{"id":"c2","path":"a.go","hunk":0,"author":"agent","body":"x","resolved":false}`), &c))
	assert.Equal(t, DiffAuthorAgent, c.Author)
}
