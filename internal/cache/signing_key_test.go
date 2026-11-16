package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveKeyInfo(t *testing.T) {
	km, err := GenerateSigningKey()
	require.NoError(t, err)
	noEnv := func(string) string { return "" }

	byPub, err := ResolveKeyInfo([]string{km.PubB64}, noEnv)
	require.NoError(t, err)
	assert.Equal(t, km.KeyID, byPub.KeyID)

	bySeed, err := ResolveKeyInfo(nil, func(name string) string {
		assert.Equal(t, SigningKeyEnv, name)
		return km.SeedB64
	})
	require.NoError(t, err)
	assert.Equal(t, byPub, bySeed, "the seed and its public half name one key")

	_, err = ResolveKeyInfo(nil, noEnv)
	assert.ErrorIs(t, err, ErrNoKeyGiven)

	_, err = ResolveKeyInfo([]string{""}, func(string) string { return km.SeedB64 })
	assert.Error(t, err, "an explicit empty argument is a bad key, not a request for the environment")
}

func TestNewSigningKeyRecordCarriesTheTemplateFields(t *testing.T) {
	rec := NewSigningKeyRecord(KeyMaterial{SeedB64: "s", PubB64: "p", KeyID: "k"})
	assert.Equal(t, SigningKeyRecord{KeyID: "k", Seed: "s", PubKey: "p"}, rec)
}
