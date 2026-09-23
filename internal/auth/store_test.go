package auth

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func isolatedStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := LoadStore()
	require.NoError(t, err)
	return store
}

// validGrants mirrors the lattice: every grant Valid accepts.
func validGrants() []types.Grant {
	var out []types.Grant
	for _, tok := range []types.Level{types.LevelNone, types.LevelWrite} {
		for _, mcp := range []types.Level{types.LevelNone, types.LevelWrite} {
			for _, con := range []types.Level{types.LevelNone, types.LevelRead, types.LevelWrite} {
				out = append(out, types.Grant{Tokens: tok, MCP: mcp, Console: con})
			}
		}
	}
	return out
}

// The rule the whole design rests on: Mint succeeds iff the requested grant is Within the
// minter's (and grants something), and an accepted record never holds more than its minter.
// Checked on a random sample and then on every pair, because the pair walk is what makes a
// wrong rule fail every run.
func TestMintNeverGrantsMoreThanTheMinterHolds(t *testing.T) {
	store := isolatedStore(t)
	grants := validGrants()
	var n atomic.Int64
	expires := time.Now().Add(time.Hour)
	check := func(minter, req types.Grant) error {
		name := fmt.Sprintf("t-%d", n.Add(1))
		_, rec, err := store.Mint(minter, MintRequest{Name: name, Grant: req, Expires: expires})
		// The oracle is written out here, not borrowed from Grant.Within, so a wrong Within
		// fails this test rather than agreeing with itself.
		within := func(a, b types.Grant) bool {
			return a.Tokens <= b.Tokens && a.MCP <= b.MCP && a.Console <= b.Console
		}
		wantOK := within(req, minter) && req != (types.Grant{})
		switch {
		case wantOK && err != nil:
			return fmt.Errorf("%s minting %s: refused: %w", minter, req, err)
		case !wantOK && err == nil:
			return fmt.Errorf("%s minted %s", minter, req)
		case err == nil && !within(rec.Grant, minter):
			return fmt.Errorf("%s's record holds %s", minter, rec.Grant)
		case !within(req, minter) && !errors.Is(err, ErrExceedsGrant):
			return fmt.Errorf("%s minting %s: wrong error %w", minter, req, err)
		}
		return nil
	}
	require.NoError(t, quick.Check(func(a, b uint8) bool {
		return check(grants[int(a)%len(grants)], grants[int(b)%len(grants)]) == nil
	}, &quick.Config{MaxCount: 500, Rand: rand.New(rand.NewSource(1))}))
	for _, minter := range grants {
		for _, req := range grants {
			require.NoError(t, check(minter, req))
		}
	}
}

// Named consequences of the rule, so a regression reads as the escalation it is.
func TestMintRefusesEachEscalation(t *testing.T) {
	store := isolatedStore(t)
	exp := time.Now().Add(time.Hour)
	cases := []struct {
		minter, req types.Grant
	}{
		{types.GrantViewer, types.GrantConsole},
		{types.GrantViewer, types.GrantConnector},
		{types.GrantConnector, types.GrantViewer},
		{types.GrantConnector, types.GrantConsole},
		{types.GrantConsole, types.GrantConnector},
		{types.GrantConsole, types.GrantOperator},
		{types.GrantConsole, types.Grant{Tokens: types.LevelWrite}},
		{types.Grant{}, types.GrantViewer},
	}
	for i, c := range cases {
		_, _, err := store.Mint(c.minter, MintRequest{Name: fmt.Sprintf("e%d", i), Grant: c.req, Expires: exp})
		assert.ErrorIs(t, err, ErrExceedsGrant, "%s minting %s", c.minter, c.req)
	}
	assert.Empty(t, store.List(), "a refused mint writes nothing")
}

// Expiry is mandatory and bounded, and a request outside the bound is an error: no code path
// shortens it to fit.
func TestMintRefusesALifetimeOutsideTheBound(t *testing.T) {
	store := isolatedStore(t)
	now := time.Now()
	for name, exp := range map[string]time.Time{
		"never":     {},
		"past":      now.Add(-time.Minute),
		"now":       now,
		"367 days":  now.Add(367 * 24 * time.Hour),
		"a century": now.Add(100 * 366 * 24 * time.Hour),
	} {
		_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantConsole, Expires: exp})
		assert.ErrorIs(t, err, ErrTokenLifetime, name)
		assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, name)
	}
	_, rec, err := store.Mint(types.GrantOperator, MintRequest{Name: "max", Grant: types.GrantConsole, Expires: now.Add(MaxTokenTTL - time.Minute)})
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(MaxTokenTTL-time.Minute), rec.Expires, time.Second, "the expiry asked for is the expiry stored")
}

// expireOnDisk rewinds name's stored expiry into the past, as time passing would.
func expireOnDisk(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var rec tokenRecord
	require.NoError(t, json.Unmarshal(b, &rec))
	rec.Expires = time.Now().Add(-time.Minute).UTC()
	b, err = json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// Expired tokens do not pile up: the next List or Mint deletes their files, and leaves every
// live token alone.
func TestExpiredTokensAreRemovedByListAndMint(t *testing.T) {
	store := isolatedStore(t)
	exp := time.Now().Add(time.Hour)
	for _, name := range []string{"old", "older", "live"} {
		_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: name, Grant: types.GrantViewer, Expires: exp})
		require.NoError(t, err)
	}
	expireOnDisk(t, store.dir, "old")
	expireOnDisk(t, store.dir, "older")

	byList, err := LoadStore()
	require.NoError(t, err)
	names := func(toks []Token) []string {
		var out []string
		for _, tok := range toks {
			out = append(out, tok.Name)
		}
		return out
	}
	assert.Equal(t, []string{"live"}, names(byList.List()))
	for _, name := range []string{"old", "older"} {
		assert.NoFileExists(t, filepath.Join(store.dir, name+".json"))
	}
	assert.FileExists(t, filepath.Join(store.dir, "live.json"))

	// Mint removes them too, and an expired token's name is free again.
	expireOnDisk(t, store.dir, "live")
	byMint, err := LoadStore()
	require.NoError(t, err)
	_, rec, err := byMint.Mint(types.GrantOperator, MintRequest{Name: "live", Grant: types.GrantConsole, Expires: exp})
	require.NoError(t, err)
	fresh, err := LoadStore()
	require.NoError(t, err)
	assert.Equal(t, []Token{rec}, fresh.List())
	leftovers, err := filepath.Glob(filepath.Join(store.dir, ".prune-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

// A snapshot that still holds an expired token must not delete a live token minted under the
// same name since: the file is checked before it is removed, and put back.
func TestPruneNeverDeletesALiveTokenThatReusedTheName(t *testing.T) {
	store := isolatedStore(t)
	exp := time.Now().Add(time.Hour)
	_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, Expires: exp})
	require.NoError(t, err)
	expireOnDisk(t, store.dir, "laptop")
	stale, err := LoadStore() // holds the expired "laptop"
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(store.dir, "laptop.json")))
	secret, live, err := isolatedStoreAt(t, store.dir).Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, Expires: exp})
	require.NoError(t, err)

	assert.Empty(t, stale.List(), "the stale expired entry leaves the snapshot")
	fresh, err := LoadStore()
	require.NoError(t, err)
	got, ok := fresh.Lookup(secret)
	require.True(t, ok, "the live token that reused the name survives the prune")
	assert.Equal(t, live.ID, got.ID)
}

// isolatedStoreAt is a second handle on the same directory, as another process would hold.
func isolatedStoreAt(t *testing.T, dir string) *Store {
	t.Helper()
	tokens, err := readTokenDir(dir)
	require.NoError(t, err)
	return &Store{dir: dir, tokens: tokens}
}

func TestMintRefusesAnInvalidOrEmptyGrant(t *testing.T) {
	store := isolatedStore(t)
	exp := time.Now().Add(time.Hour)
	for _, g := range []types.Grant{{}, {MCP: types.LevelRead}, {Tokens: types.LevelRead}, {Console: 7}} {
		_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: g, Expires: exp})
		assert.Error(t, err, "%+v", g)
	}
}

func TestMintedTokenLooksUpAsItsCredential(t *testing.T) {
	store := isolatedStore(t)
	secret, rec, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConnector, Expires: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	class, ok := Class(secret)
	require.True(t, ok)
	assert.Equal(t, types.ClassToken, class)
	assert.Len(t, secret, TokenLen)

	reloaded, err := LoadStore()
	require.NoError(t, err)
	got, ok := reloaded.Lookup(secret)
	require.True(t, ok)
	assert.Equal(t, rec, got)
	assert.Equal(t, types.Credential{Class: types.ClassToken, ID: rec.ID, Name: "laptop", Grant: types.GrantConnector}, got.Credential())

	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "magus", "tokens.d", "laptop.json")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLookupRefusesAnExpiredToken(t *testing.T) {
	store := isolatedStore(t)
	secret, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "brief", Grant: types.GrantViewer, Expires: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	store.mu.Lock()
	store.tokens[0].Expires = time.Now().Add(-time.Second)
	store.mu.Unlock()
	_, ok := store.Lookup(secret)
	assert.False(t, ok)
}

// A name is a label: revoking a token and minting another under its name yields a different
// secret and a different id, and the old secret stays dead.
func TestReusedNameIsANewIdentity(t *testing.T) {
	store := isolatedStore(t)
	exp := time.Now().Add(time.Hour)
	first, a, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, Expires: exp})
	require.NoError(t, err)
	_, _, err = store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, Expires: exp})
	require.ErrorIs(t, err, ErrTokenExists)

	removed, err := store.Revoke("laptop")
	require.NoError(t, err)
	assert.Equal(t, a.ID, removed.ID)
	second, b, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, Expires: exp})
	require.NoError(t, err)

	assert.NotEqual(t, a.ID, b.ID)
	assert.NotEqual(t, first, second)
	_, ok := store.Lookup(first)
	assert.False(t, ok, "the revoked secret must not verify as the new token")
	got, ok := store.Lookup(second)
	require.True(t, ok)
	assert.Equal(t, b.ID, got.ID)
}

func TestRevokeResolvesNameThenIDThenPrefix(t *testing.T) {
	store := isolatedStore(t)
	exp := time.Now().Add(time.Hour)
	_, a, err := store.Mint(types.GrantOperator, MintRequest{Name: "a", Grant: types.GrantViewer, Expires: exp})
	require.NoError(t, err)
	_, b, err := store.Mint(types.GrantOperator, MintRequest{Name: "b", Grant: types.GrantViewer, Expires: exp})
	require.NoError(t, err)

	got, err := store.Revoke(a.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)
	got, err = store.Revoke(b.ID[:3])
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)
	_, err = store.Revoke("a")
	assert.ErrorIs(t, err, ErrTokenNotFound)
	_, err = store.Revoke("")
	assert.ErrorIs(t, err, ErrTokenNotFound)
}

// Confinement lives inside the resolution: a pool's revoke never deletes another pool's token,
// even one it names exactly.
func TestRevokeMatchingNeverLeavesItsPool(t *testing.T) {
	store := isolatedStore(t)
	exp := time.Now().Add(time.Hour)
	_, conn, err := store.Mint(types.GrantOperator, MintRequest{Name: "agent", Grant: types.GrantConnector, Expires: exp})
	require.NoError(t, err)
	consoleOnly := func(t Token) bool { return t.Grant.MCP == types.LevelNone }
	for _, q := range []string{"agent", conn.ID, conn.ID[:4]} {
		_, err := store.RevokeMatching(q, consoleOnly)
		assert.ErrorIs(t, err, ErrTokenNotFound, q)
	}
	assert.Len(t, store.List(), 1)
}

// A version-1 record, or any file left in the retired connectors.d or connectors.json, is
// refused with MGS9017 naming every file and the commands that replace it.
func TestLoadStoreRefusesTokensWrittenBeforeGrants(t *testing.T) {
	cases := map[string]string{
		"connectors.d/obsidian.json": `{"version":1,"name":"obsidian","sha256":"x","fingerprint":"x","scope":"mcp"}`,
		"connectors.json":            `{"version":1,"tokens":[]}`,
		"tokens.d/legacy.json":       `{"version":1,"name":"legacy","sha256":"x"}`,
	}
	for rel, body := range cases {
		t.Run(rel, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			path := filepath.Join(state, "magus", filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

			_, err := LoadStore()
			require.ErrorIs(t, err, types.TokenStoreTooOld)
			assert.Contains(t, err.Error(), path)
			assert.Contains(t, err.Error(), "config mcp connector create")
		})
	}
}

// With a retired store present, the operator token still verifies: it never opens the store.
func TestOperatorVerifiesBesideARetiredStore(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	old := filepath.Join(state, "magus", "connectors.d", "obsidian.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte(`{"version":1}`), 0o600))
	op, err := Generate()
	require.NoError(t, err)
	_, err = SaveNew(op)
	require.NoError(t, err)

	cred, ok := Verify(op)
	require.True(t, ok)
	assert.Equal(t, types.ClassOperator, cred.Class)
	_, err = LoadStore()
	assert.ErrorIs(t, err, types.TokenStoreTooOld)
}

func TestLoadStoreRefusesAMalformedRecord(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `{`,
		"too new":       `{"version":3}`,
		"no grant":      `{"version":2,"id":"aaaaaaaa","name":"x","sha256":"` + strings.Repeat("a", 64) + `","expires":"2999-01-01T00:00:00Z"}`,
		"mcp=read":      `{"version":2,"id":"aaaaaaaa","name":"x","sha256":"` + strings.Repeat("a", 64) + `","grant":{"mcp":"read"},"expires":"2999-01-01T00:00:00Z"}`,
		"no expiry":     `{"version":2,"id":"aaaaaaaa","name":"x","sha256":"` + strings.Repeat("a", 64) + `","grant":{"mcp":"write"}}`,
		"id not hashed": `{"version":2,"id":"bbbbbbbb","name":"x","sha256":"` + strings.Repeat("a", 64) + `","grant":{"mcp":"write"},"expires":"2999-01-01T00:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			path := filepath.Join(state, "magus", "tokens.d", "x.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := LoadStore()
			assert.Error(t, err)
		})
	}
}

func TestLoadStoreRefusesAWorldReadableRecord(t *testing.T) {
	store := isolatedStore(t)
	_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantViewer, Expires: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	require.NoError(t, os.Chmod(filepath.Join(store.dir, "x.json"), 0o644))
	_, err = LoadStore()
	assert.ErrorIs(t, err, types.InsecureTokenPermissions)
}

// The record on disk and every List entry carry the hash and the id, never the secret.
func TestStoreNeverHoldsTheSecret(t *testing.T) {
	store := isolatedStore(t)
	secret, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantConsole, Expires: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	listed, err := json.Marshal(store.List())
	require.NoError(t, err)
	assert.NotContains(t, string(listed), secret)
	raw, err := os.ReadFile(filepath.Join(store.dir, "x.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secret)
	assert.NotContains(t, string(raw), strings.TrimPrefix(secret, prefixToken))
}
