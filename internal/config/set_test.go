package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSetArg(t *testing.T) {
	key, value, err := ParseSetArg("key=cache.mode,value=write")
	require.NoError(t, err)
	assert.Equal(t, "cache.mode", key)
	assert.Equal(t, "write", value)

	// The value keeps its own '=' and its escaped commas.
	key, value, err = ParseSetArg(`key=spells,value=go\,rust`)
	require.NoError(t, err)
	assert.Equal(t, "spells", key)
	assert.Equal(t, "go,rust", value)

	// A key with no value is legal: it is how a key is set to empty.
	key, value, err = ParseSetArg("key=cache.dir")
	require.NoError(t, err)
	assert.Equal(t, "cache.dir", key)
	assert.Equal(t, "", value)

	_, _, err = ParseSetArg("cache.mode")
	assert.ErrorContains(t, err, "not in key=value form")

	_, _, err = ParseSetArg("k=cache.mode")
	assert.ErrorContains(t, err, `unknown field "k"`)

	_, _, err = ParseSetArg("value=write")
	assert.ErrorContains(t, err, "key is required")
}

func TestStringOrDefAndIntOrDef(t *testing.T) {
	assert.Equal(t, "(default)", StringOrDef("", "(default)"))
	assert.Equal(t, "set", StringOrDef("set", "(default)"))

	assert.Equal(t, "(default)", IntOrDef(0, "(default)"))
	assert.Equal(t, "8", IntOrDef(8, "(default)"))
}
