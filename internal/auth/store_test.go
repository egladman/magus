package auth

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// isolatedStore isolates the environment, so XDG_STATE_HOME is a fresh temp dir, and
// opens the store there.
func isolatedStore(t *testing.T) *Store {
	t.Helper()
	testkit.Isolate(t)
	return reopen(t)
}

// reopen is a second handle on the store XDG_STATE_HOME names, as another caller would hold.
func reopen(t *testing.T) *Store {
	t.Helper()
	dir, err := StoreDir()
	require.NoError(t, err)
	store, err := LoadStore(dir)
	require.NoError(t, err)
	return store
}

func list(t *testing.T, s *Store) []Token {
	t.Helper()
	toks, err := s.List()
	require.NoError(t, err)
	return toks
}

func names(toks []Token) []string {
	out := []string{}
	for _, tok := range toks {
		out = append(out, tok.Name)
	}
	return out
}

// validGrants mirrors the lattice: every grant Validate accepts.
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
// minter's, grants something, and holds no tokens=write (the operator's alone), and an accepted
// record never holds more than its minter. Checked on a random sample and then on every pair,
// because the pair walk is what makes a wrong rule fail every run.
func TestMintNeverGrantsMoreThanTheMinterHolds(t *testing.T) {
	store := isolatedStore(t)
	grants := validGrants()
	var n atomic.Int64
	check := func(minter, req types.Grant) error {
		name := fmt.Sprintf("t-%d", n.Add(1))
		_, rec, err := store.Mint(minter, MintRequest{Name: name, Grant: req, TTL: time.Hour})
		// The oracle is written out here, not borrowed from Grant.Within, so a wrong Within
		// fails this test rather than agreeing with itself.
		within := func(a, b types.Grant) bool {
			return a.Tokens <= b.Tokens && a.MCP <= b.MCP && a.Console <= b.Console
		}
		wantOK := within(req, minter) && req != (types.Grant{}) && req.Tokens == types.LevelNone
		switch {
		case wantOK && err != nil:
			return fmt.Errorf("%s minting %s: refused: %w", minter, req, err)
		case !wantOK && err == nil:
			return fmt.Errorf("%s minted %s", minter, req)
		case err == nil && !within(rec.Grant, minter):
			return fmt.Errorf("%s's record holds %s", minter, rec.Grant)
		case req.Tokens == types.LevelNone && !within(req, minter) && !errors.Is(err, ErrExceedsGrant):
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
	cases := []struct {
		minter, req types.Grant
		want        error
	}{
		{types.GrantViewer, types.GrantConsole, ErrExceedsGrant},
		{types.GrantViewer, types.GrantConnector, ErrExceedsGrant},
		{types.GrantConnector, types.GrantViewer, ErrExceedsGrant},
		{types.GrantConnector, types.GrantConsole, ErrExceedsGrant},
		{types.GrantConsole, types.GrantConnector, ErrExceedsGrant},
		{types.Grant{}, types.GrantViewer, ErrExceedsGrant},
		// tokens=write is the operator's alone: not even the operator mints a stored copy.
		{types.GrantConsole, types.GrantOperator, ErrInvalidTokenRequest},
		{types.GrantOperator, types.GrantOperator, ErrInvalidTokenRequest},
		{types.GrantOperator, types.Grant{Tokens: types.LevelWrite}, ErrInvalidTokenRequest},
	}
	for i, c := range cases {
		_, _, err := store.Mint(c.minter, MintRequest{Name: fmt.Sprintf("e%d", i), Grant: c.req, TTL: time.Hour})
		assert.ErrorIs(t, err, c.want, "%s minting %s", c.minter, c.req)
	}
	assert.Empty(t, list(t, store), "a refused mint writes nothing")
}

// A lifetime is mandatory and bounded, and a request outside the bound is an error: no code
// path shortens it to fit.
func TestMintRefusesALifetimeOutsideTheBound(t *testing.T) {
	store := isolatedStore(t)
	for name, ttl := range map[string]time.Duration{
		"never":     0,
		"past":      -time.Minute,
		"367 days":  367 * 24 * time.Hour,
		"a century": 100 * 366 * 24 * time.Hour,
	} {
		_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantConsole, TTL: ttl})
		assert.ErrorIs(t, err, ErrTokenLifetime, name)
		assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, name)
		_, _, err = store.MintCode(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantConsole, TTL: ttl})
		assert.ErrorIs(t, err, ErrTokenLifetime, "code "+name)
	}
	now := time.Now()
	_, rec, err := store.Mint(types.GrantOperator, MintRequest{Name: "max", Grant: types.GrantConsole, TTL: MaxTokenTTL})
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(MaxTokenTTL), rec.Expires, time.Second, "the lifetime asked for is the lifetime stored")
}

// A name is a label a person revokes by, so it may not look like an id: revoking by one could
// then mean the other.
func TestMintRefusesANameThatLooksLikeAnID(t *testing.T) {
	store := isolatedStore(t)
	for _, name := range []string{"3fa9c1d2", "deadbeef", "../x", "a/b", ".hidden"} {
		_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: name, Grant: types.GrantViewer, TTL: time.Hour})
		assert.ErrorIs(t, err, ErrInvalidTokenRequest, name)
		assert.ErrorIs(t, err, types.TokenRequestInvalid, name)
	}
	_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "3fa9c1d", Grant: types.GrantViewer, TTL: time.Hour})
	assert.NoError(t, err, "seven hex digits are a name")
}

// With no name, Mint takes the first free "<kind>-N" itself, so every door names alike.
func TestMintNamesAnUnnamedToken(t *testing.T) {
	store := isolatedStore(t)
	for _, want := range []string{"console-1", "console-2"} {
		_, rec, err := store.Mint(types.GrantOperator, MintRequest{Grant: types.GrantViewer, TTL: time.Hour})
		require.NoError(t, err)
		assert.Equal(t, want, rec.Name)
	}
	_, rec, err := store.Mint(types.GrantOperator, MintRequest{Grant: types.GrantConnector, TTL: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, "connector-1", rec.Name)
}

// expireOnDisk rewinds name's stored creation and expiry into the past, as time passing would.
func expireOnDisk(t *testing.T, dir, name string) {
	t.Helper()
	editRecord(t, filepath.Join(dir, name+".json"), func(rec *tokenRecord) {
		rec.Created = time.Now().Add(-2 * time.Hour).UTC()
		rec.Expires = time.Now().Add(-time.Minute).UTC()
	})
}

func editRecord(t *testing.T, path string, edit func(*tokenRecord)) {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var rec tokenRecord
	require.NoError(t, json.Unmarshal(b, &rec))
	edit(&rec)
	b, err = json.Marshal(rec)
	require.NoError(t, err)
	// A later mtime, so the store's directory signature sees the rewrite.
	require.NoError(t, os.WriteFile(path, b, 0o600))
	later := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(path, later, later))
}

// Expired tokens do not pile up: the next List or Mint deletes their files, and leaves every
// live token alone.
func TestExpiredTokensAreRemovedByListAndMint(t *testing.T) {
	store := isolatedStore(t)
	for _, name := range []string{"old", "older", "live"} {
		_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: name, Grant: types.GrantViewer, TTL: time.Hour})
		require.NoError(t, err)
	}
	expireOnDisk(t, store.dir, "old")
	expireOnDisk(t, store.dir, "older")

	assert.Equal(t, []string{"live"}, names(list(t, reopen(t))))
	for _, name := range []string{"old", "older"} {
		assert.NoFileExists(t, filepath.Join(store.dir, name+".json"))
	}
	assert.FileExists(t, filepath.Join(store.dir, "live.json"))

	// Mint removes them too, and an expired token's name is free again.
	expireOnDisk(t, store.dir, "live")
	_, rec, err := reopen(t).Mint(types.GrantOperator, MintRequest{Name: "live", Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, []Token{rec}, list(t, reopen(t)))
	leftovers, err := filepath.Glob(filepath.Join(store.dir, ".remove-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

// A removal never deletes a live token that reused the name since the record was read: the
// file is checked before it is removed, and put back.
func TestRemoveExactNeverDeletesALiveTokenThatReusedTheName(t *testing.T) {
	store := isolatedStore(t)
	_, old, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)
	_, err = store.Revoke(types.GrantOperator, "laptop")
	require.NoError(t, err)
	secret, live, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)

	assert.ErrorIs(t, store.removeExact(old), errReplaced)
	got, ok := reopen(t).Lookup(secret)
	require.True(t, ok, "the live token that reused the name survives")
	assert.Equal(t, live.ID, got.ID)
}

func TestMintRefusesAnInvalidOrEmptyGrant(t *testing.T) {
	store := isolatedStore(t)
	for _, g := range []types.Grant{{}, {MCP: types.LevelRead}, {Tokens: types.LevelRead}, {Console: 7}} {
		_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: g, TTL: time.Hour})
		assert.ErrorIs(t, err, ErrInvalidTokenRequest, "%+v", g)
	}
}

// A disk that refuses the write is not the caller's mistake: the error wraps no request
// sentinel, so the daemon answers it Internal rather than InvalidArgument.
func TestMintDiskFailureIsNotARequestError(t *testing.T) {
	testkit.Isolate(t)
	dir, err := StoreDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o700))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
	store := &Store{dir: dir}
	_, _, err = store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantViewer, TTL: time.Hour})
	require.Error(t, err)
	for _, sentinel := range []error{ErrInvalidTokenRequest, ErrExceedsGrant, ErrTokenLifetime, ErrTokenExists, ErrTokenNotFound} {
		assert.NotErrorIs(t, err, sentinel)
	}
}

func TestMintedTokenLooksUpAsItsCredential(t *testing.T) {
	store := isolatedStore(t)
	secret, rec, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConnector, TTL: time.Hour})
	require.NoError(t, err)
	class, ok := classOf(secret)
	require.True(t, ok)
	assert.Equal(t, types.ClassStored, class)
	assert.Len(t, secret, tokenLen)

	got, ok := reopen(t).Lookup(secret)
	require.True(t, ok)
	assert.Equal(t, rec, got)
	assert.Equal(t, types.Credential{Class: types.ClassStored, ID: rec.ID, Name: "laptop", Grant: types.GrantConnector}, got.Credential())

	info, err := os.Stat(filepath.Join(store.dir, "laptop.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLookupRefusesAnExpiredToken(t *testing.T) {
	store := isolatedStore(t)
	secret, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "brief", Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)
	expireOnDisk(t, store.dir, "brief")
	_, ok := store.Lookup(secret)
	assert.False(t, ok)
}

// A name is a label: revoking a token and minting another under its name yields a different
// secret and a different id, and the old secret stays dead.
func TestReusedNameIsANewIdentity(t *testing.T) {
	store := isolatedStore(t)
	first, a, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)
	_, _, err = store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, TTL: time.Hour})
	require.ErrorIs(t, err, ErrTokenExists)
	require.ErrorIs(t, err, types.TokenNameExists)

	removed, err := store.Revoke(types.GrantOperator, "laptop")
	require.NoError(t, err)
	assert.Equal(t, a.ID, removed.ID)
	second, b, err := store.Mint(types.GrantOperator, MintRequest{Name: "laptop", Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)

	assert.NotEqual(t, a.ID, b.ID)
	assert.NotEqual(t, first, second)
	_, ok := store.Lookup(first)
	assert.False(t, ok, "the revoked secret must not verify as the new token")
	got, ok := store.Lookup(second)
	require.True(t, ok)
	assert.Equal(t, b.ID, got.ID)
}

// Revoke takes an exact id or an exact name, never a prefix and never one for the other.
func TestRevokeIsExactIDOrExactName(t *testing.T) {
	store := isolatedStore(t)
	_, a, err := store.Mint(types.GrantOperator, MintRequest{Name: "a", Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)
	_, b, err := store.Mint(types.GrantOperator, MintRequest{Name: "b", Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)

	for _, q := range []string{b.ID[:3], b.ID[:7], "", "  ", "B", b.ID + "0"} {
		_, err := store.Revoke(types.GrantOperator, q)
		assert.ErrorIs(t, err, ErrTokenNotFound, "%q", q)
		assert.ErrorIs(t, err, types.TokenNotFound, "%q", q)
	}
	got, err := store.Revoke(types.GrantOperator, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)
	got, err = store.Revoke(types.GrantOperator, "b")
	require.NoError(t, err)
	assert.Equal(t, b.ID, got.ID)
	assert.Empty(t, list(t, store))
}

// A revoke is held to the revoker's grant as a mint is: a viewer cannot revoke the console
// token a person uses, nor a connector an agent uses.
func TestRevokeRefusesATokenOutsideTheRevokersGrant(t *testing.T) {
	store := isolatedStore(t)
	_, console, err := store.Mint(types.GrantOperator, MintRequest{Name: "console", Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)
	_, conn, err := store.Mint(types.GrantOperator, MintRequest{Name: "agent", Grant: types.GrantConnector, TTL: time.Hour})
	require.NoError(t, err)
	for _, c := range []struct {
		revoker types.Grant
		q       string
	}{
		{types.GrantViewer, console.ID},
		{types.GrantViewer, "console"},
		{types.GrantConsole, "agent"},
		{types.GrantConnector, "console"},
		{types.Grant{}, conn.ID},
	} {
		_, err := store.Revoke(c.revoker, c.q)
		assert.ErrorIs(t, err, ErrExceedsGrant, "%s revoking %s", c.revoker, c.q)
		assert.ErrorIs(t, err, types.GrantInsufficient)
	}
	assert.Len(t, list(t, store), 2, "a refused revoke deletes nothing")
	_, err = store.Revoke(types.GrantConsole, "console")
	assert.NoError(t, err)
}

// plant writes a record straight into the store under file, holding the hash of a real
// secret, and returns the secret: what a process writing tokens.d by hand could do.
func plant(t *testing.T, dir, file string, edit func(*tokenRecord)) string {
	t.Helper()
	secret, err := mintSecret(types.ClassStored)
	require.NoError(t, err)
	sum := digest(secret)
	now := time.Now().UTC()
	rec := tokenRecord{Version: storeVersion, Token: Token{
		ID: sum[:8], Name: file, Class: types.ClassStored, SHA256: sum,
		Grant: types.GrantViewer, Created: now.Add(-time.Minute), Expires: now.Add(time.Hour),
	}}
	edit(&rec)
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, file+".json"), b, 0o600))
	return secret
}

// A record planted in tokens.d is held to every rule a mint is, at load: one that no mint could
// have written is skipped with a coded error naming its file, never verifies, and never
// disables the records beside it.
func TestAPlantedRecordCannotExceedWhatMintWrites(t *testing.T) {
	cases := map[string]func(*tokenRecord){
		"tokens=write":           func(r *tokenRecord) { r.Grant = types.GrantOperator },
		"tokens=write alone":     func(r *tokenRecord) { r.Grant = types.Grant{Tokens: types.LevelWrite} },
		"year 9999 expiry":       func(r *tokenRecord) { r.Expires = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) },
		"no expiry":              func(r *tokenRecord) { r.Expires = time.Time{} },
		"expires before created": func(r *tokenRecord) { r.Expires = r.Created.Add(-time.Second) },
		"created in the future":  func(r *tokenRecord) { r.Created = time.Now().Add(time.Hour); r.Expires = r.Created.Add(time.Hour) },
		"no creation time":       func(r *tokenRecord) { r.Created = time.Time{} },
		"name is not the file":   func(r *tokenRecord) { r.Name = "other" },
		"name ../x":              func(r *tokenRecord) { r.Name = "../x" },
		"grants nothing":         func(r *tokenRecord) { r.Grant = types.Grant{} },
		"mcp=read":               func(r *tokenRecord) { r.Grant = types.Grant{MCP: types.LevelRead} },
		"id not its hash":        func(r *tokenRecord) { r.ID = "bbbbbbbb" },
		"hash malformed":         func(r *tokenRecord) { r.SHA256 = strings.Repeat("z", 64) },
		"operator class":         func(r *tokenRecord) { r.Class = types.ClassOperator },
		"share class":            func(r *tokenRecord) { r.Class = types.ClassShare },
		"code past a minute":     func(r *tokenRecord) { r.Class, r.TokenTTL = types.ClassExchange, time.Hour },
		"code for a year+": func(r *tokenRecord) {
			r.Class, r.TokenTTL, r.Expires = types.ClassExchange, 400*24*time.Hour, r.Created.Add(30*time.Second)
		},
		"too new": func(r *tokenRecord) { r.Version = storeVersion + 1 },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			store := isolatedStore(t)
			_, good, err := store.Mint(types.GrantOperator, MintRequest{Name: "good", Grant: types.GrantConsole, TTL: time.Hour})
			require.NoError(t, err)
			secret := plant(t, store.dir, "planted", edit)

			fresh := reopen(t)
			assert.Equal(t, []string{"good"}, names(list(t, fresh)), "the planted record is skipped, the good one kept")
			_, ok := fresh.Lookup(secret)
			assert.False(t, ok, "a planted secret never verifies")
			_, ok = Verify(secret)
			assert.False(t, ok, "nor through Verify")
			_, ok = fresh.Lookup(good.SHA256)
			assert.False(t, ok, "a hash is not a token")

			skipped, err := fresh.Skipped()
			require.NoError(t, err)
			require.Len(t, skipped, 1)
			assert.Contains(t, skipped[0].Error(), filepath.Join(store.dir, "planted.json"))
			var coded types.DiagnosticCode
			for _, c := range []types.DiagnosticCode{types.TokenRecordInvalid, types.TokenStoreTooNew} {
				if errors.Is(skipped[0], c) {
					coded = c
				}
			}
			assert.NotEmpty(t, coded, "the skip is coded: %v", skipped[0])
		})
	}
}

// A world-readable record is skipped like any other bad one, rather than failing the store.
func TestAWorldReadableRecordIsSkippedNotFatal(t *testing.T) {
	store := isolatedStore(t)
	_, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)
	_, _, err = store.Mint(types.GrantOperator, MintRequest{Name: "y", Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)
	require.NoError(t, os.Chmod(filepath.Join(store.dir, "x.json"), 0o644))
	fresh := reopen(t)
	assert.Equal(t, []string{"y"}, names(list(t, fresh)))
	skipped, err := fresh.Skipped()
	require.NoError(t, err)
	require.Len(t, skipped, 1)
	assert.ErrorIs(t, skipped[0], types.InsecureTokenPermissions)
}

// A console link's code is not a token: it lives a minute, is refused as a bearer everywhere,
// and is traded once for the stored token it stands for.
func TestAnExchangeCodeRedeemsOnceForItsToken(t *testing.T) {
	store := isolatedStore(t)
	code, rec, err := store.MintCode(types.GrantOperator, MintRequest{Grant: types.GrantConsole, TTL: 12 * time.Hour})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(code, prefixExchange))
	assert.Equal(t, types.ClassExchange, rec.Class)
	assert.WithinDuration(t, time.Now().Add(ExchangeCodeTTL), rec.Expires, time.Second)
	_, ok := store.Lookup(code)
	assert.False(t, ok, "a code is no bearer")
	_, ok = Verify(code)
	assert.False(t, ok)

	secret, tok, err := store.Redeem(code)
	require.NoError(t, err)
	assert.Equal(t, types.ClassStored, tok.Class)
	assert.Equal(t, types.GrantConsole, tok.Grant)
	assert.Equal(t, rec.Name, tok.Name)
	assert.WithinDuration(t, time.Now().Add(12*time.Hour), tok.Expires, time.Second)
	got, ok := store.Lookup(secret)
	require.True(t, ok)
	assert.Equal(t, tok.ID, got.ID)

	_, _, err = store.Redeem(code)
	assert.ErrorIs(t, err, ErrTokenNotFound, "a code is spent on first use")
	assert.Equal(t, []string{tok.Name}, names(list(t, store)), "the code's record is gone")
}

func TestAnExchangeCodeDiesAfterAMinute(t *testing.T) {
	store := isolatedStore(t)
	code, rec, err := store.MintCode(types.GrantOperator, MintRequest{Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)
	editRecord(t, filepath.Join(store.dir, rec.Name+".json"), func(r *tokenRecord) {
		r.Created = time.Now().Add(-2 * time.Minute).UTC()
		r.Expires = r.Created.Add(ExchangeCodeTTL)
	})
	_, _, err = store.Redeem(code)
	assert.ErrorIs(t, err, ErrTokenNotFound)
	for _, bad := range []string{"", "mgx_", "not a code", prefixStored + strings.Repeat("0", 49)} {
		_, _, err = store.Redeem(bad)
		assert.ErrorIs(t, err, ErrTokenNotFound, "%q", bad)
	}
}

// Of two callers racing one code, exactly one gets a token.
func TestAnExchangeCodeRedeemsOnceUnderARace(t *testing.T) {
	store := isolatedStore(t)
	code, _, err := store.MintCode(types.GrantOperator, MintRequest{Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)
	var wins atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		handle := reopen(t)
		wg.Go(func() {
			if _, _, err := handle.Redeem(code); err == nil {
				wins.Add(1)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, int64(1), wins.Load())
}

// A code is minted under the rules a token is: nothing past the minter, no tokens=write.
func TestMintCodeIsHeldToMintsRules(t *testing.T) {
	store := isolatedStore(t)
	_, _, err := store.MintCode(types.GrantViewer, MintRequest{Grant: types.GrantConsole, TTL: time.Hour})
	assert.ErrorIs(t, err, ErrExceedsGrant)
	_, _, err = store.MintCode(types.GrantOperator, MintRequest{Grant: types.GrantOperator, TTL: time.Hour})
	assert.ErrorIs(t, err, ErrInvalidTokenRequest)
}

// Every handle on one directory reads one cache: a verify does not re-read every file, and a
// change on disk is still seen by the next read.
func TestStoreHandlesShareOneCacheThatSeesChanges(t *testing.T) {
	store := isolatedStore(t)
	secret, rec, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantViewer, TTL: time.Hour})
	require.NoError(t, err)
	other := reopen(t)
	assert.Same(t, store.state(), other.state())
	_, ok := other.Lookup(secret)
	require.True(t, ok)

	sig := store.state().sig
	_, ok = other.Lookup(secret)
	require.True(t, ok)
	assert.Equal(t, sig, store.state().sig, "an unchanged directory is not re-read")

	require.NoError(t, os.Remove(filepath.Join(store.dir, rec.Name+".json")))
	_, ok = other.Lookup(secret)
	assert.False(t, ok, "`rm tokens.d/<name>.json` revokes at once")
}

// A version-1 record, or any file left in the retired connectors.d or connectors.json, is
// refused with MGS9017 naming every file and the commands that replace it.
func TestLoadStoreRefusesTokensWrittenBeforeGrants(t *testing.T) {
	cases := map[string]string{
		"connectors.d/obsidian.json": `{"version":1,"name":"obsidian","sha256":"x","fingerprint":"x","scope":"mcp"}`,
		"connectors.json":            `{"version":1,"tokens":[]}`,
	}
	for rel, body := range cases {
		t.Run(rel, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			path := filepath.Join(state, "magus", filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

			dir, err := StoreDir()
			require.NoError(t, err)
			_, err = LoadStore(dir)
			require.ErrorIs(t, err, types.TokenStoreTooOld)
			assert.Contains(t, err.Error(), path)
			assert.Contains(t, err.Error(), "config mcp connector create")
		})
	}
	// A version-1 record inside tokens.d is one bad file: skipped, coded, not fatal.
	store := isolatedStore(t)
	require.NoError(t, os.MkdirAll(store.dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store.dir, "legacy.json"), []byte(`{"version":1,"name":"legacy","sha256":"x"}`), 0o600))
	skipped, err := reopen(t).Skipped()
	require.NoError(t, err)
	require.Len(t, skipped, 1)
	assert.ErrorIs(t, skipped[0], types.TokenStoreTooOld)
}

// With a retired store present, the operator token still verifies: it never opens the store.
func TestOperatorVerifiesBesideARetiredStore(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	old := filepath.Join(state, "magus", "connectors.d", "obsidian.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte(`{"version":1}`), 0o600))
	op, err := GenerateOperator()
	require.NoError(t, err)
	_, err = SaveNewOperator(op)
	require.NoError(t, err)

	cred, ok := Verify(op)
	require.True(t, ok)
	assert.Equal(t, types.ClassOperator, cred.Class)
	dir, err := StoreDir()
	require.NoError(t, err)
	_, err = LoadStore(dir)
	assert.ErrorIs(t, err, types.TokenStoreTooOld)
}

// The record on disk and every List entry carry the hash and the id, never the secret.
func TestStoreNeverHoldsTheSecret(t *testing.T) {
	store := isolatedStore(t)
	secret, _, err := store.Mint(types.GrantOperator, MintRequest{Name: "x", Grant: types.GrantConsole, TTL: time.Hour})
	require.NoError(t, err)
	listed, err := json.Marshal(list(t, store))
	require.NoError(t, err)
	assert.NotContains(t, string(listed), secret)
	raw, err := os.ReadFile(filepath.Join(store.dir, "x.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secret)
	assert.NotContains(t, string(raw), strings.TrimPrefix(secret, prefixStored))
}
