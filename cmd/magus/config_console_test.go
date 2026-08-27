package main

import (
	"testing"

	"github.com/egladman/magus/internal/auth"
	"github.com/stretchr/testify/assert"
)

func TestMatchesScopedAcceptsAFingerprintPrefix(t *testing.T) {
	toks := []auth.ConnectorToken{{Name: "laptop", Fingerprint: "deadbeef"}}

	assert.True(t, matchesScoped(toks, "laptop"))
	assert.True(t, matchesScoped(toks, "deadbeef"))
	assert.True(t, matchesScoped(toks, "dead"))
	assert.True(t, matchesScoped(toks, "  laptop  "))

	assert.False(t, matchesScoped(toks, "beef"), "a fingerprint matches by prefix, not anywhere")
	assert.False(t, matchesScoped(toks, ""))
	assert.False(t, matchesScoped(nil, "laptop"))
}
