package spells

import (
	"github.com/egladman/magus/internal/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpellOpKind covers OpKind's empty-default resolution and the IsService
// discriminator across the three op shapes (default command, explicit command,
// service).
func TestOpKind(t *testing.T) {
	tests := []struct {
		name     string
		op       Op
		wantKind string
		wantSvc  bool
	}{
		{"empty defaults to command", Op{}, OpKindCommand, false},
		{"explicit command", Op{Kind: OpKindCommand}, OpKindCommand, false},
		{"service", Op{Kind: OpKindService}, OpKindService, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantKind, tt.op.OpKind())
			assert.Equal(t, tt.wantSvc, tt.op.IsService())
		})
	}
}

// TestCommandSecretsJSONRoundTrip pins the descriptor cache-correctness contract: a
// Command's Secrets field carries its own json tag ("secrets") through Marshal and
// Unmarshal untouched, so BuiltinsHash (internal/spellruntime.BuiltinsHash, hashed
// from json.Marshal(Builtins())) and any other descriptor serialization move when a
// declared ref changes - a spell editing which secret an op needs invalidates the
// cache keys that depend on the spell's identity, the same as editing its Bin/Args
// does.
func TestCommandSecretsJSONRoundTrip(t *testing.T) {
	c := Command{
		Bin:     "npm",
		Args:    []string{"publish"},
		Secrets: map[string]string{"NPM_TOKEN": "NPM_TOKEN"},
	}
	b, err := json.Marshal(c)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"secrets":{"NPM_TOKEN":"NPM_TOKEN"}`)

	var got Command
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, c, got)
}

// TestCommandSecretsOmittedWhenEmpty pins the other half of the cache-correctness
// contract: an op declaring no secrets must not move the hash at all, which requires
// the field to actually omit (omitempty), not merely round-trip as an empty map.
func TestCommandSecretsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(Command{Bin: "go", Args: []string{"build"}})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "secrets", "an op with no declared secrets must not carry the key at all")
}
