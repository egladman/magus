package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExpiry(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	t.Run("default is 90 days", func(t *testing.T) {
		got, err := parseExpiry(now, "")
		require.NoError(t, err)
		assert.Equal(t, now.Add(auth.DefaultTokenTTL), got)
	})
	t.Run("N days", func(t *testing.T) {
		got, err := parseExpiry(now, "7d")
		require.NoError(t, err)
		assert.Equal(t, now.Add(7*24*time.Hour), got)
	})
	t.Run("the maximum is honored exactly", func(t *testing.T) {
		got, err := parseExpiry(now, "366d")
		require.NoError(t, err)
		assert.Equal(t, now.Add(366*24*time.Hour), got)
	})
	t.Run("go duration", func(t *testing.T) {
		got, err := parseExpiry(now, "48h")
		require.NoError(t, err)
		assert.Equal(t, now.Add(48*time.Hour), got)
	})
	// A token must expire, and a lifetime past the maximum is refused, never shortened.
	for _, bad := range []string{"never", "Never", "367d", "8785h", "0d", "-3d", "0h", "40000d"} {
		t.Run("out of bound "+bad, func(t *testing.T) {
			_, err := parseExpiry(now, bad)
			assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, "parseExpiry accepted %q", bad)
		})
	}
	for _, bad := range []string{"5x", "abc", "99999999999999999999d"} {
		t.Run("unparseable "+bad, func(t *testing.T) {
			_, err := parseExpiry(now, bad)
			assert.Error(t, err, "parseExpiry accepted %q", bad)
		})
	}
}

func TestMintTokenNamesAndRefusesAReusedName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	exp := time.Now().Add(time.Hour)
	_, rec, err := mintToken("", types.GrantConnector, exp)
	require.NoError(t, err)
	assert.Equal(t, "connector-1", rec.Name)
	_, rec, err = mintToken("", types.GrantConnector, exp)
	require.NoError(t, err)
	assert.Equal(t, "connector-2", rec.Name)
	_, rec, err = mintToken("", types.GrantViewer, exp)
	require.NoError(t, err)
	assert.Equal(t, "console-1", rec.Name)

	_, _, err = mintToken("connector-1", types.GrantConsole, exp)
	assert.ErrorIs(t, err, types.TokenNameExists)
}

// A console link token that has expired is gone after the next mint: every `open` line mints
// one, and they must not pile up in tokens.d.
func TestExpiredConsoleLinkTokenDisappearsOnTheNextMint(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	first, err := mintConsoleLinkToken()
	require.NoError(t, err)
	store, err := auth.LoadStore()
	require.NoError(t, err)
	require.Len(t, store.List(), 1)
	old := store.List()[0]

	// Time passes: rewrite the record's expiry into the past.
	path := filepath.Join(state, "magus", "tokens.d", old.Name+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	stale := strings.Replace(string(raw), old.Expires.Format(time.RFC3339Nano), time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), 1)
	require.NotEqual(t, string(raw), stale, "the fixture must actually move the expiry")
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o600))

	_, err = mintConsoleLinkToken()
	require.NoError(t, err)
	store, err = auth.LoadStore()
	require.NoError(t, err)
	toks := store.List()
	require.Len(t, toks, 1, "the expired link token is removed, the new one kept")
	assert.NotEqual(t, old.ID, toks[0].ID)
	_, ok := auth.Verify(first)
	assert.False(t, ok)
	files, err := filepath.Glob(filepath.Join(state, "magus", "tokens.d", "*"))
	require.NoError(t, err)
	assert.Len(t, files, 1, "exactly one file left on disk")
}

// The console link token is console=write, expires in consoleLinkTTL, and is never the
// operator token.
func TestConsoleLinkTokenIsAShortLivedConsoleToken(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	secret, err := mintConsoleLinkToken()
	require.NoError(t, err)
	cred, ok := auth.Verify(secret)
	require.True(t, ok)
	assert.Equal(t, types.ClassToken, cred.Class)
	assert.Equal(t, types.GrantConsole, cred.Grant)
	store, err := auth.LoadStore()
	require.NoError(t, err)
	require.Len(t, store.List(), 1)
	assert.WithinDuration(t, time.Now().Add(consoleLinkTTL), store.List()[0].Expires, time.Minute)
}
