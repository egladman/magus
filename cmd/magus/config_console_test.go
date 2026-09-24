package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// outsideAnyWorkspace moves the test to a directory no magusfile governs, so a CLI mint's
// audit has no workspace trail to land in and writes none into this repository's.
func outsideAnyWorkspace(t *testing.T) {
	t.Helper()
	testkit.Isolate(t)
	t.Chdir(t.TempDir())
}

// A console link carries a one-time code rather than a token: the code verifies nowhere, and
// traded once it yields a console=write token living console.LinkTokenLifetime.
func TestConsoleLinkCarriesAOneTimeCode(t *testing.T) {
	outsideAnyWorkspace(t)
	code, err := mintConsoleLinkCode()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(code, "mgx_"), code)
	_, ok := auth.Verify(code)
	assert.False(t, ok, "a code is no bearer")

	store, err := openTokenStore()
	require.NoError(t, err)
	secret, tok, err := store.Redeem(code)
	require.NoError(t, err)
	cred, ok := auth.Verify(secret)
	require.True(t, ok)
	assert.Equal(t, types.ClassStored, cred.Class)
	assert.Equal(t, types.GrantConsole, cred.Grant)
	assert.WithinDuration(t, time.Now().Add(console.LinkTokenLifetime), tok.Expires, time.Minute)
	_, _, err = store.Redeem(code)
	assert.ErrorIs(t, err, auth.ErrTokenNotFound, "spent on first use")
}

// Every CLI mint writes one trail event naming what was minted, by class, id and grant, and
// by whom, and none carries the secret.
func TestCLIMintsAreAudited(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	t.Chdir(root)

	secret, rec, err := mintToken(auth.MintRequest{Name: "laptop", Grant: types.GrantConsole, TTL: time.Hour}, false)
	require.NoError(t, err)
	code, codeRec, err := mintToken(auth.MintRequest{Grant: types.GrantViewer, TTL: time.Hour}, true)
	require.NoError(t, err)

	base, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	events, err := trail.ReadRecent(base, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	want := map[string]auth.Token{"cli.mint": rec, "link.code": codeRec}
	for _, e := range events {
		minted, ok := want[e.Action]
		require.True(t, ok, e.Action)
		assert.Equal(t, trail.KindTokenLifecycle, e.Kind)
		assert.Contains(t, e.Preview, minted.ID)
		assert.Contains(t, e.Preview, minted.Grant.String())
		blob, err := trail.ReadBlob(base, e.RequestRef)
		require.NoError(t, err)
		for _, s := range []string{secret, code} {
			assert.NotContains(t, string(blob), s)
			assert.NotContains(t, e.Preview, s)
		}
		assert.Contains(t, string(blob), `"class":"`+string(minted.Class)+`"`)
		assert.Contains(t, string(blob), `"class":"operator"`, "the minter is named")
	}
}

// Both token commands list the whole store, so neither hides a token the other minted, and
// revoke by an exact id or an exact name only.
func TestTokenCommandsShareOneStore(t *testing.T) {
	outsideAnyWorkspace(t)
	_, conn, err := mintToken(auth.MintRequest{Grant: types.GrantConnector, TTL: time.Hour}, false)
	require.NoError(t, err)
	_, _, err = mintToken(auth.MintRequest{Name: "laptop", Grant: types.GrantConsole, TTL: time.Hour}, false)
	require.NoError(t, err)

	require.Error(t, tokenRevoke("config console token revoke", []string{conn.ID[:4]}), "no prefix")
	require.NoError(t, tokenRevoke("config console token revoke", []string{conn.ID}), "a console command revokes a connector by id")
	require.NoError(t, tokenRevoke("config mcp connector revoke", []string{"laptop"}), "and the reverse by name")
	store, err := openTokenStore()
	require.NoError(t, err)
	left, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, left)
}
