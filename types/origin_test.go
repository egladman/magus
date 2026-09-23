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
		{Origin{User: "eli", EntryPoint: EntryPointRPC, Credential: "console-1"}, "eli via credential console-1"},
		{Origin{User: "eli", EntryPoint: EntryPointMCP, Host: "claude-code", Credential: "cli"}, "eli via claude-code via credential cli"},
		{Origin{User: "eli", EntryPoint: EntryPointDaemon}, "daemon"},
		{Origin{Host: "codex"}, "codex"},
		{Origin{}, "unattributed"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.origin.Label(), "%+v", c.origin)
	}
	assert.False(t, Origin{User: "eli"}.Attributed(), "no host session is unattributed, whoever the user is")
	assert.True(t, Origin{Session: "s1"}.Attributed())
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
