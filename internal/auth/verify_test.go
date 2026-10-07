package auth

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// Every kind mints a 53-character token with a base62 body and a valid checksum, so credentialKind
// accepts it offline, and flipping any one character makes credentialKind refuse it.
func TestEveryKindHasOneFormat(t *testing.T) {
	t.Parallel()
	for kind, prefix := range kindPrefixes {
		secret, err := mintSecret(kind)
		require.NoError(t, err)
		assert.Len(t, secret, tokenLen, kind)
		assert.True(t, strings.HasPrefix(secret, prefix), kind)
		assert.True(t, isBase62(strings.TrimPrefix(secret, prefix)), kind)
		got, ok := credentialKind(secret)
		require.True(t, ok, kind)
		assert.Equal(t, kind, got)
		for i := len(prefix); i < len(secret); i++ {
			flipped := []byte(secret)
			if flipped[i] == 'A' {
				flipped[i] = 'B'
			} else {
				flipped[i] = 'A'
			}
			_, ok := credentialKind(string(flipped))
			assert.False(t, ok, "%s with byte %d flipped", kind, i)
		}
	}
	for _, s := range []string{"", "mgo_", "mgz_" + strings.Repeat("0", 49), "dGhpcyBpcyAzMiBieXRlcyBvZiBiYXNlNjR1cmwgZGF0YQ"} {
		_, ok := credentialKind(s)
		assert.False(t, ok, s)
	}
	_, err := mintSecret("unknown")
	assert.Error(t, err)
}

// plantRecord writes a stored-token record whose hash is that of secret, whatever its kind:
// what an attacker able to write tokens.d, or a bug, could produce.
func plantRecord(t *testing.T, name, secret string, grant types.Grant) {
	t.Helper()
	sum := digest(secret)
	rec := tokenRecord{Version: storeVersion, Token: Token{
		ID: sum[:8], Name: name, Kind: types.KindStored, SHA256: sum, Grant: grant,
		Created: time.Now().Add(-time.Minute).UTC(), Expires: time.Now().Add(time.Hour).UTC(),
	}}
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	dir, err := StateDir()
	require.NoError(t, err)
	path := filepath.Join(dir, "tokens.d", name+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// Verify routes by kind and consults only that kind's store: a stored record can never
// match the operator file, the operator secret can never match the store, and a share secret
// never verifies on loopback, even when a record carries its hash.
func TestVerifyRoutesByKind(t *testing.T) {
	testkit.Isolate(t)
	op, err := EnsureOperator(t.Context(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	opCred, ok := Verify(op)
	require.True(t, ok)
	assert.Equal(t, operatorCredential(op), opCred)
	assert.Equal(t, types.GrantOperator, opCred.Grant)

	// A record carrying the operator secret's hash, as a viewer.
	plantRecord(t, "shadow", op, types.GrantViewer)
	opCred, ok = Verify(op)
	require.True(t, ok)
	assert.Equal(t, types.KindOperator, opCred.Kind, "an mgo_ string is never matched against the store")

	// An mgs_ secret placed in the operator file cannot be read as the operator at all.
	stored, err := mintSecret(types.KindStored)
	require.NoError(t, err)
	plantRecord(t, "real", stored, types.GrantViewer)
	cred, ok := Verify(stored)
	require.True(t, ok)
	assert.Equal(t, types.KindStored, cred.Kind)
	assert.Equal(t, types.GrantViewer, cred.Grant, "an mgs_ string is never matched against the operator file")

	// A share secret with a planted record still fails on loopback.
	share, _, err := MintShare(types.GrantOperator, time.Hour)
	require.NoError(t, err)
	plantRecord(t, "planted-share", share, types.GrantConsole)
	_, ok = Verify(share)
	assert.False(t, ok, "mgl_ never verifies on loopback")

	// Nor does a console link's code, even with a stored record carrying its hash.
	code, err := mintSecret(types.KindExchange)
	require.NoError(t, err)
	plantRecord(t, "planted-code", code, types.GrantConsole)
	_, ok = Verify(code)
	assert.False(t, ok, "mgx_ is never a bearer")

	// Wrong, malformed, and empty.
	for _, s := range []string{"", "not-a-token", op + "x", strings.Replace(op, "mgo_", "mgs_", 1)} {
		_, ok := Verify(s)
		assert.False(t, ok, s)
	}
}

// A flood of well-formed wrong tokens is answered from memory: 1,000 of them load the stores
// at most once, a valid token among them still verifies, and a mint or a revoke is seen by
// the very next call.
func TestVerifyAnswersBadTokensFromMemory(t *testing.T) {
	testkit.Isolate(t)
	prev := revalidateEvery
	revalidateEvery = time.Hour
	t.Cleanup(func() { revalidateEvery = prev })
	op, err := EnsureOperator(t.Context(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	dir, err := StoreDir()
	require.NoError(t, err)
	store, err := LoadStore(dir)
	require.NoError(t, err)
	good, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "good", Grant: types.GrantConnector, TTL: time.Hour})
	require.NoError(t, err)
	_, ok := Verify(good)
	require.True(t, ok)

	loads := func() int {
		viewMu.Lock()
		defer viewMu.Unlock()
		return viewLoads
	}
	before := loads()
	for i := range 1000 {
		kind := types.KindStored
		if i%2 == 1 {
			kind = types.KindOperator
		}
		bad, err := mintSecret(kind)
		require.NoError(t, err)
		_, ok := Verify(bad)
		require.False(t, ok)
		if i == 500 {
			_, ok := Verify(good)
			require.True(t, ok, "a valid token verifies mid-flood")
			_, ok = Verify(op)
			require.True(t, ok, "and so does the operator")
		}
	}
	assert.LessOrEqual(t, loads()-before, 1, "the flood loads the stores at most once")

	fresh, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "fresh", Grant: types.GrantConnector, TTL: time.Hour})
	require.NoError(t, err)
	_, ok = Verify(fresh)
	assert.True(t, ok, "a mint is seen at once")
	_, err = store.Revoke(types.GrantOperator, "good")
	require.NoError(t, err)
	_, ok = Verify(good)
	assert.False(t, ok, "a revoke is seen at once")
	require.NoError(t, RevokeOperator())
	_, ok = Verify(op)
	assert.False(t, ok, "so is a revoked operator token")
}

// The share verifier accepts its own mgl_ secret and nothing else: an operator or stored token
// whose hash it somehow held still fails, because the kind is wrong.
func TestShareVerifierNeverAcceptsAnotherKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []types.CredentialKind{types.KindOperator, types.KindStored, types.KindExchange} {
		secret, err := mintSecret(kind)
		require.NoError(t, err)
		tok := ShareToken{SHA256: digest(secret), Expires: time.Now().Add(time.Hour)}
		assert.False(t, tok.Verify(secret, time.Now()), kind)
	}
}
