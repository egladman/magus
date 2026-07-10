package main

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExpiry(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	t.Run("default is 90 days", func(t *testing.T) {
		got, err := parseExpiry(now, "")
		require.NoError(t, err)
		assert.Equal(t, now.Add(auth.DefaultConnectorTTL), got)
	})
	t.Run("never yields zero time", func(t *testing.T) {
		got, err := parseExpiry(now, "Never")
		require.NoError(t, err)
		assert.True(t, got.IsZero())
	})
	t.Run("N days", func(t *testing.T) {
		got, err := parseExpiry(now, "7d")
		require.NoError(t, err)
		assert.Equal(t, now.Add(7*24*time.Hour), got)
	})
	t.Run("go duration", func(t *testing.T) {
		got, err := parseExpiry(now, "48h")
		require.NoError(t, err)
		assert.Equal(t, now.Add(48*time.Hour), got)
	})
	for _, bad := range []string{"5x", "abc", "0d", "-3d", "0h", "40000d", "99999999999999999999d"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			_, err := parseExpiry(now, bad)
			assert.Error(t, err, "parseExpiry accepted %q", bad)
		})
	}
}

func TestDefaultConnectorName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := auth.LoadConnectorStore()
	require.NoError(t, err)

	assert.Equal(t, "connector-1", defaultConnectorName(store))

	_, _, err = store.Create("connector-1", time.Time{})
	require.NoError(t, err)
	// After connector-1 is taken, the next default skips to connector-2.
	assert.Equal(t, "connector-2", defaultConnectorName(store))
}
