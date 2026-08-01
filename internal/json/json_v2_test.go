//go:build goexperiment.jsonv2

package json

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurationMarshalsAsString(t *testing.T) {
	b, err := Marshal(struct {
		TTL time.Duration `json:"ttl"`
	}{6 * time.Hour})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ttl":"6h0m0s"}`, string(b))
}

func TestMarshalSortsMapKeys(t *testing.T) {
	b, err := Marshal(map[string]int{"b": 1, "a": 2})
	require.NoError(t, err)
	assert.Equal(t, `{"a":2,"b":1}`, string(b))
}

func TestMarshalKeepsNilCollectionsNull(t *testing.T) {
	b, err := Marshal(struct {
		Items []string       `json:"items"`
		Attrs map[string]int `json:"attrs"`
	}{})
	require.NoError(t, err)
	assert.Equal(t, `{"items":null,"attrs":null}`, string(b))
}
