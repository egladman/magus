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
	"github.com/egladman/magus/types"
)

// Every class mints a 53-character token with a base62 body and a valid checksum, so Class
// accepts it offline, and flipping any one character makes Class refuse it.
func TestEveryClassHasOneFormat(t *testing.T) {
	t.Parallel()
	for class, prefix := range classPrefixes {
		secret, err := mintSecret(class)
		require.NoError(t, err)
		assert.Len(t, secret, TokenLen, class)
		assert.True(t, strings.HasPrefix(secret, prefix), class)
		assert.True(t, isBase62(strings.TrimPrefix(secret, prefix)), class)
		got, ok := Class(secret)
		require.True(t, ok, class)
		assert.Equal(t, class, got)
		for i := len(prefix); i < len(secret); i++ {
			flipped := []byte(secret)
			if flipped[i] == 'A' {
				flipped[i] = 'B'
			} else {
				flipped[i] = 'A'
			}
			_, ok := Class(string(flipped))
			assert.False(t, ok, "%s with byte %d flipped", class, i)
		}
	}
	for _, s := range []string{"", "mgo_", "mgx_" + strings.Repeat("0", 49), "dGhpcyBpcyAzMiBieXRlcyBvZiBiYXNlNjR1cmwgZGF0YQ"} {
		_, ok := Class(s)
		assert.False(t, ok, s)
	}
	_, err := mintSecret("unknown")
	assert.Error(t, err)
}

// plantRecord writes a stored-token record whose hash is that of secret, whatever its class:
// what an attacker able to write tokens.d, or a bug, could produce.
func plantRecord(t *testing.T, name, secret string, grant types.Grant) {
	t.Helper()
	sum := digest(secret)
	rec := tokenRecord{Version: storeVersion, Token: Token{
		ID: sum[:8], Name: name, SHA256: sum, Grant: grant,
		Created: time.Now().UTC(), Expires: time.Now().Add(time.Hour).UTC(),
	}}
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	dir, err := StateDir()
	require.NoError(t, err)
	path := filepath.Join(dir, "tokens.d", name+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// Verify routes by class and consults only that class's store: a stored record can never
// match the operator file, the operator secret can never match the store, and a share secret
// never verifies on loopback, even when a record carries its hash.
func TestVerifyRoutesByClass(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	op, err := EnsureOperator(t.Context(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	opCred, ok := Verify(op)
	require.True(t, ok)
	assert.Equal(t, OperatorCredential(op), opCred)
	assert.Equal(t, types.GrantOperator, opCred.Grant)

	// A record carrying the operator secret's hash, as a viewer.
	plantRecord(t, "shadow", op, types.GrantViewer)
	opCred, ok = Verify(op)
	require.True(t, ok)
	assert.Equal(t, types.ClassOperator, opCred.Class, "an mgo_ string is never matched against the store")

	// An mgs_ secret placed in the operator file cannot be read as the operator at all.
	stored, err := mintSecret(types.ClassToken)
	require.NoError(t, err)
	plantRecord(t, "real", stored, types.GrantViewer)
	cred, ok := Verify(stored)
	require.True(t, ok)
	assert.Equal(t, types.ClassToken, cred.Class)
	assert.Equal(t, types.GrantViewer, cred.Grant, "an mgs_ string is never matched against the operator file")

	// A share secret with a planted record still fails on loopback.
	share, _, err := MintShare(types.GrantOperator, time.Hour)
	require.NoError(t, err)
	plantRecord(t, "planted-share", share, types.GrantOperator)
	_, ok = Verify(share)
	assert.False(t, ok, "mgl_ never verifies on loopback")

	// Wrong, malformed, and empty.
	for _, s := range []string{"", "not-a-token", op + "x", strings.Replace(op, "mgo_", "mgs_", 1)} {
		_, ok := Verify(s)
		assert.False(t, ok, s)
	}
}

// The share verifier accepts its own mgl_ secret and nothing else: an operator or stored token
// whose hash it somehow held still fails, because the class is wrong.
func TestShareVerifierNeverAcceptsAnotherClass(t *testing.T) {
	t.Parallel()
	for _, class := range []types.CredentialClass{types.ClassOperator, types.ClassToken} {
		secret, err := mintSecret(class)
		require.NoError(t, err)
		tok := ShareToken{SHA256: digest(secret), Expires: time.Now().Add(time.Hour)}
		assert.False(t, tok.Verify(secret, time.Now()), class)
	}
}
