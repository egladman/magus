package main

import (
	"testing"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestMatchesTokenAcceptsAnIDPrefix(t *testing.T) {
	toks := []auth.Token{{Name: "laptop", ID: "deadbeef"}}

	assert.True(t, matchesToken(toks, "laptop"))
	assert.True(t, matchesToken(toks, "deadbeef"))
	assert.True(t, matchesToken(toks, "dead"))
	assert.True(t, matchesToken(toks, "  laptop  "))

	assert.False(t, matchesToken(toks, "beef"), "an id matches by prefix, not anywhere")
	assert.False(t, matchesToken(toks, ""))
	assert.False(t, matchesToken(nil, "laptop"))
}

// The two commands own disjoint pools, split by whether the grant reaches /mcp.
func TestConsoleAndConnectorPoolsAreDisjoint(t *testing.T) {
	for _, g := range []types.Grant{types.GrantConnector, types.GrantConsole, types.GrantViewer} {
		tok := auth.Token{Grant: g}
		assert.NotEqual(t, isConnector(tok), isConsole(tok), g.String())
	}
	assert.True(t, isConnector(auth.Token{Grant: types.GrantConnector}))
	assert.True(t, isConsole(auth.Token{Grant: types.GrantViewer}))
}
