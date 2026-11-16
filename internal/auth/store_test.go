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
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
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
func validGrants() []job.Access {
	var out []job.Access
	for _, tok := range []job.Level{job.LevelNone, job.LevelWrite} {
		for _, mcp := range []job.Level{job.LevelNone, job.LevelWrite} {
			for _, con := range []job.Level{job.LevelNone, job.LevelRead, job.LevelWrite} {
				out = append(out, job.Access{Tokens: tok, MCP: mcp, Console: con})
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
	check := func(minter, req job.Access) error {
		name := fmt.Sprintf("t-%d", n.Add(1))
		_, rec, err := store.Mint(minter, MintRequest{Name: name, Grant: req, TTL: time.Hour})
		// The oracle is written out here, not borrowed from Grant.Within, so a wrong Within
		// fails this test rather than agreeing with itself.
		within := func(a, b job.Access) bool {
			return a.Tokens <= b.Tokens && a.MCP <= b.MCP && a.Console <= b.Console
		}
		wantOK := within(req, minter) && req != (job.Access{}) && req.Tokens == job.LevelNone
		switch {
		case wantOK && err != nil:
			return fmt.Errorf("%s minting %s: refused: %w", minter, req, err)
		case !wantOK && err == nil:
			return fmt.Errorf("%s minted %s", minter, req)
		case err == nil && !within(rec.Grant, minter):
			return fmt.Errorf("%s's record holds %s", minter, rec.Grant)
		case req.Tokens == job.LevelNone && !within(req, minter) && !errors.Is(err, ErrExceedsGrant):
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
		minter, req job.Access
		want        error
	}{
		{job.AccessViewer, job.AccessConsole, ErrExceedsGrant},
		{job.AccessViewer, job.AccessConnector, ErrExceedsGrant},
		{job.AccessConnector, job.AccessViewer, ErrExceedsGrant},
		{job.AccessConnector, job.AccessConsole, ErrExceedsGrant},
		{job.AccessConsole, job.AccessConnector, ErrExceedsGrant},
		{job.Access{}, job.AccessViewer, ErrExceedsGrant},
		// tokens=write is the operator's alone: not even the operator mints a stored copy.
		{job.AccessConsole, job.AccessOperator, ErrInvalidTokenRequest},
		{job.AccessOperator, job.AccessOperator, ErrInvalidTokenRequest},
		{job.AccessOperator, job.Access{Tokens: job.LevelWrite}, ErrInvalidTokenRequest},
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
		_, _, err := store.Mint(job.AccessOperator, MintRequest{Name: "x", Grant: job.AccessConsole, TTL: ttl})
		assert.ErrorIs(t, err, ErrTokenLifetime, name)
		assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, name)
		_, _, err = store.MintCode(job.AccessOperator, MintRequest{Name: "x", Grant: job.AccessConsole, TTL: ttl})
		assert.ErrorIs(t, err, ErrTokenLifetime, "code "+name)
	}
	now := time.Now()
	_, rec, err := store.Mint(job.AccessOperator, MintRequest{Name: "max", Grant: job.AccessConsole, TTL: MaxTokenTTL})
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(MaxTokenTTL), rec.Expires, time.Second, "the lifetime asked for is the lifetime stored")
}

// A name is a label a person revokes by, so it may not look like an id: revoking by one could
// then mean the other.
func TestMintRefusesANameThatLooksLikeAnID(t *testing.T) {
	store := isolatedStore(t)
	for _, name := range []string{"3fa9c1d2", "deadbeef", "../x", "a/b", ".hidden"} {
		_, _, err := store.Mint(job.AccessOperator, MintRequest{Name: name, Grant: job.AccessViewer, TTL: time.Hour})
		assert.ErrorIs(t, err, ErrInvalidTokenRequest, name)
		assert.ErrorIs(t, err, types.TokenRequestInvalid, name)
	}
	_, _, err := store.Mint(job.AccessOperator, MintRequest{Name: "3fa9c1d", Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err, "seven hex digits are a name")
}

// With no name, Mint takes the first free "<kind>-N" itself, so every door names alike.
func TestMintNamesAnUnnamedToken(t *testing.T) {
	store := isolatedStore(t)
	for _, want := range []string{"console-1", "console-2"} {
		_, rec, err := store.Mint(job.AccessOperator, MintRequest{Grant: job.AccessViewer, TTL: time.Hour})
		require.NoError(t, err)
		assert.Equal(t, want, rec.Name)
	}
	_, rec, err := store.Mint(job.AccessOperator, MintRequest{Grant: job.AccessConnector, TTL: time.Hour})
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
	require.NoError(t, codec.Unmarshal(b, &rec))
	edit(&rec)
	b, err = codec.Marshal(rec)
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
		_, _, err := store.Mint(job.AccessOperator, MintRequest{Name: name, Grant: job.AccessViewer, TTL: time.Hour})
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
	_, rec, err := reopen(t).Mint(job.AccessOperator, MintRequest{Name: "live", Grant: job.AccessConsole, TTL: time.Hour})
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
	_, old, err := store.Mint(job.AccessOperator, MintRequest{Name: "laptop", Grant: job.AccessConsole, TTL: time.Hour})
	require.NoError(t, err)
	_, err = store.Revoke(job.AccessOperator, "laptop")
	require.NoError(t, err)
	secret, live, err := store.Mint(job.AccessOperator, MintRequest{Name: "laptop", Grant: job.AccessConsole, TTL: time.Hour})
	require.NoError(t, err)

	assert.ErrorIs(t, store.removeExact(old), errReplaced)
	got, ok := reopen(t).Lookup(secret.Reveal())
	require.True(t, ok, "the live token that reused the name survives")
	assert.Equal(t, live.ID, got.ID)
}

func TestMintRefusesAnInvalidOrEmptyGrant(t *testing.T) {
	store := isolatedStore(t)
	for _, g := range []job.Access{{}, {MCP: job.LevelRead}, {Tokens: job.LevelRead}, {Console: 7}} {
		_, _, err := store.Mint(job.AccessOperator, MintRequest{Name: "x", Grant: g, TTL: time.Hour})
		assert.ErrorIs(t, err, ErrInvalidTokenRequest, "%+v", g)
	}
}

// A disk that refuses the write is not the caller's mistake: the error wraps no request
// sentinel, so the server answers it Internal rather than InvalidArgument.
func TestMintDiskFailureIsNotARequestError(t *testing.T) {
	testkit.Isolate(t)
	dir, err := StoreDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o700))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
	store := &Store{dir: dir}
	_, _, err = store.Mint(job.AccessOperator, MintRequest{Name: "x", Grant: job.AccessViewer, TTL: time.Hour})
	require.Error(t, err)
	for _, sentinel := range []error{ErrInvalidTokenRequest, ErrExceedsGrant, ErrTokenLifetime, ErrTokenExists, ErrTokenNotFound} {
		assert.NotErrorIs(t, err, sentinel)
	}
}

func TestMintedTokenLooksUpAsItsCredential(t *testing.T) {
	store := isolatedStore(t)
	secret, rec, err := store.Mint(job.AccessOperator, MintRequest{Name: "laptop", Grant: job.AccessConnector, TTL: time.Hour})
	require.NoError(t, err)
	kind, ok := credentialKind(secret.Reveal())
	require.True(t, ok)
	assert.Equal(t, job.KindStored, kind)
	assert.Len(t, secret.Reveal(), tokenLen)

	got, ok := reopen(t).Lookup(secret.Reveal())
	require.True(t, ok)
	assert.Equal(t, rec, got)
	assert.Equal(t, job.Credential{Kind: job.KindStored, ID: rec.ID, Name: "laptop", Grant: job.AccessConnector}, got.Credential())

	info, err := os.Stat(filepath.Join(store.dir, "laptop.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLookupRefusesAnExpiredToken(t *testing.T) {
	store := isolatedStore(t)
	secret, _, err := store.Mint(job.AccessOperator, MintRequest{Name: "brief", Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)
	expireOnDisk(t, store.dir, "brief")
	_, ok := store.Lookup(secret.Reveal())
	assert.False(t, ok)
}

// A name is a label: revoking a token and minting another under its name yields a different
// secret and a different id, and the old secret stays dead.
func TestReusedNameIsANewIdentity(t *testing.T) {
	store := isolatedStore(t)
	first, a, err := store.Mint(job.AccessOperator, MintRequest{Name: "laptop", Grant: job.AccessConsole, TTL: time.Hour})
	require.NoError(t, err)
	_, _, err = store.Mint(job.AccessOperator, MintRequest{Name: "laptop", Grant: job.AccessConsole, TTL: time.Hour})
	require.ErrorIs(t, err, ErrTokenExists)
	require.ErrorIs(t, err, types.TokenNameExists)

	removed, err := store.Revoke(job.AccessOperator, "laptop")
	require.NoError(t, err)
	assert.Equal(t, a.ID, removed.ID)
	second, b, err := store.Mint(job.AccessOperator, MintRequest{Name: "laptop", Grant: job.AccessConsole, TTL: time.Hour})
	require.NoError(t, err)

	assert.NotEqual(t, a.ID, b.ID)
	assert.NotEqual(t, first, second)
	_, ok := store.Lookup(first.Reveal())
	assert.False(t, ok, "the revoked secret must not verify as the new token")
	got, ok := store.Lookup(second.Reveal())
	require.True(t, ok)
	assert.Equal(t, b.ID, got.ID)
}

// Revoke takes an exact id or an exact name, never a prefix and never one for the other.
func TestRevokeIsExactIDOrExactName(t *testing.T) {
	store := isolatedStore(t)
	_, a, err := store.Mint(job.AccessOperator, MintRequest{Name: "a", Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)
	_, b, err := store.Mint(job.AccessOperator, MintRequest{Name: "b", Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)

	for _, q := range []string{b.ID[:3], b.ID[:7], "", "  ", "B", b.ID + "0"} {
		_, err := store.Revoke(job.AccessOperator, q)
		assert.ErrorIs(t, err, ErrTokenNotFound, "%q", q)
		assert.ErrorIs(t, err, types.TokenNotFound, "%q", q)
	}
	got, err := store.Revoke(job.AccessOperator, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)
	got, err = store.Revoke(job.AccessOperator, "b")
	require.NoError(t, err)
	assert.Equal(t, b.ID, got.ID)
	assert.Empty(t, list(t, store))
}

// A revoke is held to the revoker's grant as a mint is: a viewer cannot revoke the console
// token a person uses, nor a connector an agent uses.
func TestRevokeRefusesATokenOutsideTheRevokersGrant(t *testing.T) {
	store := isolatedStore(t)
	_, console, err := store.Mint(job.AccessOperator, MintRequest{Name: "console", Grant: job.AccessConsole, TTL: time.Hour})
	require.NoError(t, err)
	_, conn, err := store.Mint(job.AccessOperator, MintRequest{Name: "agent", Grant: job.AccessConnector, TTL: time.Hour})
	require.NoError(t, err)
	for _, c := range []struct {
		revoker job.Access
		q       string
	}{
		{job.AccessViewer, console.ID},
		{job.AccessViewer, "console"},
		{job.AccessConsole, "agent"},
		{job.AccessConnector, "console"},
		{job.Access{}, conn.ID},
	} {
		_, err := store.Revoke(c.revoker, c.q)
		assert.ErrorIs(t, err, ErrExceedsGrant, "%s revoking %s", c.revoker, c.q)
		assert.ErrorIs(t, err, types.GrantInsufficient)
	}
	assert.Len(t, list(t, store), 2, "a refused revoke deletes nothing")
	_, err = store.Revoke(job.AccessConsole, "console")
	require.NoError(t, err)
}

// plant writes a record straight into the store under file, holding the hash of a real
// secret, and returns the secret: what a process writing tokens.d by hand could do.
func plant(t *testing.T, dir, file string, edit func(*tokenRecord)) string {
	t.Helper()
	secret, err := mintSecret(job.KindStored)
	require.NoError(t, err)
	sum := digest(secret)
	now := time.Now().UTC()
	rec := tokenRecord{Version: storeVersion, Token: Token{
		ID: sum[:8], Name: file, Kind: job.KindStored, SHA256: sum,
		Grant: job.AccessViewer, Created: now.Add(-time.Minute), Expires: now.Add(time.Hour),
	}}
	edit(&rec)
	b, err := codec.Marshal(rec)
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
		"tokens=write":           func(r *tokenRecord) { r.Grant = job.AccessOperator },
		"tokens=write alone":     func(r *tokenRecord) { r.Grant = job.Access{Tokens: job.LevelWrite} },
		"year 9999 expiry":       func(r *tokenRecord) { r.Expires = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) },
		"no expiry":              func(r *tokenRecord) { r.Expires = time.Time{} },
		"expires before created": func(r *tokenRecord) { r.Expires = r.Created.Add(-time.Second) },
		"created in the future":  func(r *tokenRecord) { r.Created = time.Now().Add(time.Hour); r.Expires = r.Created.Add(time.Hour) },
		"no creation time":       func(r *tokenRecord) { r.Created = time.Time{} },
		"name is not the file":   func(r *tokenRecord) { r.Name = "other" },
		"name ../x":              func(r *tokenRecord) { r.Name = "../x" },
		"grants nothing":         func(r *tokenRecord) { r.Grant = job.Access{} },
		"mcp=read":               func(r *tokenRecord) { r.Grant = job.Access{MCP: job.LevelRead} },
		"id not its hash":        func(r *tokenRecord) { r.ID = "bbbbbbbb" },
		"hash malformed":         func(r *tokenRecord) { r.SHA256 = strings.Repeat("z", 64) },
		"operator kind":          func(r *tokenRecord) { r.Kind = job.KindOperator },
		"share kind":             func(r *tokenRecord) { r.Kind = job.KindShare },
		"code past a minute":     func(r *tokenRecord) { r.Kind, r.TokenTTL = job.KindExchange, time.Hour },
		"code for a year+": func(r *tokenRecord) {
			r.Kind, r.TokenTTL, r.Expires = job.KindExchange, 400*24*time.Hour, r.Created.Add(30*time.Second)
		},
		"too new": func(r *tokenRecord) { r.Version = "magus.token/v1alpha2" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			store := isolatedStore(t)
			_, good, err := store.Mint(job.AccessOperator, MintRequest{Name: "good", Grant: job.AccessConsole, TTL: time.Hour})
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
	_, _, err := store.Mint(job.AccessOperator, MintRequest{Name: "x", Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)
	_, _, err = store.Mint(job.AccessOperator, MintRequest{Name: "y", Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)
	require.NoError(t, os.Chmod(filepath.Join(store.dir, "x.json"), 0o644))
	fresh := reopen(t)
	assert.Equal(t, []string{"y"}, names(list(t, fresh)))
	skipped, err := fresh.Skipped()
	require.NoError(t, err)
	require.Len(t, skipped, 1)
	assert.ErrorIs(t, skipped[0], types.InsecureTokenPermissions)
}

// A console link's code is not a token: it lives seconds, is refused as a
// bearer everywhere, and is traded once for the stored token it stands for.
func TestAnExchangeCodeRedeemsOnceForItsToken(t *testing.T) {
	store := isolatedStore(t)
	code, rec, err := store.MintCode(job.AccessOperator, MintRequest{Grant: job.AccessConsole, TTL: 12 * time.Hour})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(code.Reveal(), prefixExchange))
	assert.Equal(t, job.KindExchange, rec.Kind)
	assert.WithinDuration(t, time.Now().Add(ExchangeCodeTTL), rec.Expires, time.Second)
	_, ok := store.Lookup(code.Reveal())
	assert.False(t, ok, "a code is no bearer")
	_, ok = Verify(code.Reveal())
	assert.False(t, ok)

	secret, tok, err := store.Redeem(code.Reveal())
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(12*time.Hour), tok.Expires, time.Second)
	sum := digest(secret.Reveal())
	assert.Equal(t, Token{
		ID:      sum[:8],
		Name:    rec.Name,
		Kind:    job.KindStored,
		SHA256:  sum,
		Grant:   job.AccessConsole,
		Created: tok.Created, // the clock at redemption
		Expires: tok.Expires, // within a second of 12 hours, above
	}, tok)
	got, ok := store.Lookup(secret.Reveal())
	require.True(t, ok)
	assert.Equal(t, tok.ID, got.ID)

	_, _, err = store.Redeem(code.Reveal())
	assert.ErrorIs(t, err, ErrTokenNotFound, "a code is spent on first use")
	assert.Equal(t, []string{tok.Name}, names(list(t, store)), "the code's record is gone")
}

type ExchangeCodeLifetimeSuite struct{ suite.Suite }

func TestExchangeCodeLifetimeSuite(t *testing.T) {
	suite.Run(t, new(ExchangeCodeLifetimeSuite))
}

func (s *ExchangeCodeLifetimeSuite) TestAMintedCodeLivesTenSeconds() {
	store := isolatedStore(s.T())
	_, rec, err := store.MintCode(job.AccessOperator, MintRequest{Grant: job.AccessViewer, TTL: time.Hour})
	s.Require().NoError(err)
	s.WithinDuration(time.Now().Add(10*time.Second), rec.Expires, time.Second)
}

func TestAnExchangeCodeDiesAfterItsLifetime(t *testing.T) {
	store := isolatedStore(t)
	code, rec, err := store.MintCode(job.AccessOperator, MintRequest{Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)
	editRecord(t, filepath.Join(store.dir, rec.Name+".json"), func(r *tokenRecord) {
		r.Created = time.Now().Add(-2 * time.Minute).UTC()
		r.Expires = r.Created.Add(ExchangeCodeTTL)
	})
	_, _, err = store.Redeem(code.Reveal())
	assert.ErrorIs(t, err, ErrTokenNotFound)
	for _, bad := range []string{"", "mgx_", "not a code", prefixStored + strings.Repeat("0", 49)} {
		_, _, err = store.Redeem(bad)
		assert.ErrorIs(t, err, ErrTokenNotFound, "%q", bad)
	}
}

// Of two callers racing one code, exactly one gets a token.
func TestAnExchangeCodeRedeemsOnceUnderARace(t *testing.T) {
	store := isolatedStore(t)
	code, _, err := store.MintCode(job.AccessOperator, MintRequest{Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)
	var wins atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		handle := reopen(t)
		wg.Go(func() {
			if _, _, err := handle.Redeem(code.Reveal()); err == nil {
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
	_, _, err := store.MintCode(job.AccessViewer, MintRequest{Grant: job.AccessConsole, TTL: time.Hour})
	assert.ErrorIs(t, err, ErrExceedsGrant)
	_, _, err = store.MintCode(job.AccessOperator, MintRequest{Grant: job.AccessOperator, TTL: time.Hour})
	assert.ErrorIs(t, err, ErrInvalidTokenRequest)
}

// Every handle on one directory reads one cache: a verify does not re-read every file, and a
// change on disk is still seen by the next read.
func TestStoreHandlesShareOneCacheThatSeesChanges(t *testing.T) {
	store := isolatedStore(t)
	secret, rec, err := store.Mint(job.AccessOperator, MintRequest{Name: "x", Grant: job.AccessViewer, TTL: time.Hour})
	require.NoError(t, err)
	other := reopen(t)
	assert.Same(t, store.state(), other.state())
	_, ok := other.Lookup(secret.Reveal())
	require.True(t, ok)

	sig := store.state().sig
	_, ok = other.Lookup(secret.Reveal())
	require.True(t, ok)
	assert.Equal(t, sig, store.state().sig, "an unchanged directory is not re-read")

	require.NoError(t, os.Remove(filepath.Join(store.dir, rec.Name+".json")))
	_, ok = other.Lookup(secret.Reveal())
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
			testkit.Isolate(t)
			state := os.Getenv("XDG_STATE_HOME")
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

type PreFoldTokenSuite struct {
	suite.Suite
	store  *Store
	secret string
}

func TestPreFoldTokenSuite(t *testing.T) {
	suite.Run(t, new(PreFoldTokenSuite))
}

func (s *PreFoldTokenSuite) SetupTest() {
	s.store = isolatedStore(s.T())
	s.secret = plant(s.T(), s.store.dir, "older", func(*tokenRecord) {})
	path := filepath.Join(s.store.dir, "older.json")
	b, err := os.ReadFile(path)
	s.Require().NoError(err)
	b = []byte(strings.Replace(string(b), `"version":"`+string(storeVersion)+`"`, `"version":2`, 1))
	s.Require().Contains(string(b), `"version":2`)
	s.Require().NoError(os.WriteFile(path, b, 0o600))
}

func (s *PreFoldTokenSuite) TestAVersionTwoTokenVerifies() {
	fresh := reopen(s.T())
	skipped, err := fresh.Skipped()
	s.Require().NoError(err)
	s.Empty(skipped)
	cred, ok := fresh.Lookup(s.secret)
	s.Require().True(ok)
	s.Equal("older", cred.Name)
}

func (s *PreFoldTokenSuite) TestAVersionTwoTokenRevokes() {
	_, err := reopen(s.T()).Revoke(job.AccessOperator, "older")
	s.Require().NoError(err)
	s.NoFileExists(filepath.Join(s.store.dir, "older.json"))
}

// With a retired store present, the operator token still verifies: it never opens the store.
func TestOperatorVerifiesBesideARetiredStore(t *testing.T) {
	testkit.Isolate(t)
	state := os.Getenv("XDG_STATE_HOME")
	old := filepath.Join(state, "magus", "connectors.d", "obsidian.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte(`{"version":1}`), 0o600))
	op, err := GenerateOperator()
	require.NoError(t, err)
	_, err = SaveNewOperator(op)
	require.NoError(t, err)

	cred, ok := Verify(op.Reveal())
	require.True(t, ok)
	assert.Equal(t, job.KindOperator, cred.Kind)
	dir, err := StoreDir()
	require.NoError(t, err)
	_, err = LoadStore(dir)
	assert.ErrorIs(t, err, types.TokenStoreTooOld)
}

// The record on disk and every List entry carry the hash and the id, never the secret.
func TestStoreNeverHoldsTheSecret(t *testing.T) {
	store := isolatedStore(t)
	secret, _, err := store.Mint(job.AccessOperator, MintRequest{Name: "x", Grant: job.AccessConsole, TTL: time.Hour})
	require.NoError(t, err)
	listed, err := codec.Marshal(list(t, store))
	require.NoError(t, err)
	assert.NotContains(t, string(listed), secret)
	raw, err := os.ReadFile(filepath.Join(store.dir, "x.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), secret)
	assert.NotContains(t, string(raw), strings.TrimPrefix(secret.Reveal(), prefixStored))
}
