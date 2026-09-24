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
	for in, want := range map[string]time.Duration{
		"":     auth.DefaultTokenTTL,
		"7d":   7 * 24 * time.Hour,
		"366d": 366 * 24 * time.Hour,
		"48h":  48 * time.Hour,
	} {
		got, err := parseExpiry(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	// A token must expire, and a lifetime past the maximum is refused, never shortened.
	for _, bad := range []string{"never", "Never", "367d", "8785h", "0d", "-3d", "0h", "40000d"} {
		_, err := parseExpiry(bad)
		assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, "parseExpiry accepted %q", bad)
	}
	for _, bad := range []string{"5x", "abc", "99999999999999999999d"} {
		_, err := parseExpiry(bad)
		assert.Error(t, err, "parseExpiry accepted %q", bad)
	}
}

func TestMintTokenNamesAndRefusesAReusedName(t *testing.T) {
	outsideAnyWorkspace(t)
	mint := func(name string, g types.Grant) (auth.Token, error) {
		_, rec, err := mintToken(auth.MintRequest{Name: name, Grant: g, TTL: time.Hour}, false)
		return rec, err
	}
	for _, c := range []struct {
		grant types.Grant
		want  string
	}{{types.GrantConnector, "connector-1"}, {types.GrantConnector, "connector-2"}, {types.GrantViewer, "console-1"}} {
		rec, err := mint("", c.grant)
		require.NoError(t, err)
		assert.Equal(t, c.want, rec.Name)
	}
	_, err := mint("connector-1", types.GrantConsole)
	assert.ErrorIs(t, err, types.TokenNameExists)
}

// A console link's code that has expired is gone after the next mint: every `open` line mints
// one, and they must not pile up in tokens.d.
func TestExpiredConsoleLinkCodeDisappearsOnTheNextMint(t *testing.T) {
	outsideAnyWorkspace(t)
	first, err := mintConsoleLinkCode()
	require.NoError(t, err)
	store, err := openTokenStore()
	require.NoError(t, err)
	toks, err := store.List()
	require.NoError(t, err)
	require.Len(t, toks, 1)
	old := toks[0]

	// Time passes: rewrite the record into the past.
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	path := filepath.Join(dir, old.Name+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	stale := strings.Replace(string(raw), old.Expires.Format(time.RFC3339Nano), time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), 1)
	stale = strings.Replace(stale, old.Created.Format(time.RFC3339Nano), time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), 1)
	require.NotEqual(t, string(raw), stale, "the fixture must actually move the expiry")
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o600))
	later := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(path, later, later))

	_, err = mintConsoleLinkCode()
	require.NoError(t, err)
	toks, err = store.List()
	require.NoError(t, err)
	require.Len(t, toks, 1, "the expired code is removed, the new one kept")
	assert.NotEqual(t, old.ID, toks[0].ID)
	_, _, err = store.Redeem(first)
	assert.ErrorIs(t, err, auth.ErrTokenNotFound)
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	require.NoError(t, err)
	assert.Len(t, files, 1, "exactly one file left on disk")
}
