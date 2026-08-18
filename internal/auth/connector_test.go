package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// ConnectorSuite isolates the state dir at a temp dir before each test so the
// connector store never touches the real user directory.
type ConnectorSuite struct {
	suite.Suite
	stateDir string
}

func (s *ConnectorSuite) SetupTest() {
	s.stateDir = s.T().TempDir()
	s.T().Setenv("XDG_STATE_HOME", s.stateDir)
}

func TestConnectorSuite(t *testing.T) {
	suite.Run(t, new(ConnectorSuite))
}

func (s *ConnectorSuite) store() *ConnectorStore {
	st, err := LoadConnectorStore()
	s.Require().NoError(err)
	return st
}

func (s *ConnectorSuite) TestCreateListVerify() {
	t := s.T()
	st := s.store()

	secret, c, err := st.Create("claude", time.Now().Add(DefaultConnectorTTL), ScopeMCP)
	require.NoError(t, err)
	assert.True(t, validTokenFormat(secret), "minted token not well-formed: %q", secret)
	assert.Equal(t, "claude", c.Name)
	assert.Len(t, c.Fingerprint, 8)
	assert.NotContains(t, c.SHA256, secret, "stored entry leaked the secret")

	// The entry never carries the plaintext.
	list := st.List()
	require.Len(t, list, 1)
	assert.Equal(t, c, list[0])

	assert.True(t, st.VerifyScope(secret, ScopeMCP), "Verify rejected the freshly minted token")

	// A validly-formatted but never-stored token must not verify.
	other, err := mintToken()
	require.NoError(t, err)
	require.True(t, validTokenFormat(other))
	assert.False(t, st.VerifyScope(other, ScopeMCP), "Verify accepted a non-stored token")
}

func (s *ConnectorSuite) TestCreateRejectsDuplicateName() {
	t := s.T()
	st := s.store()

	_, _, err := st.Create("dup", time.Time{}, ScopeMCP)
	require.NoError(t, err)
	_, _, err = st.Create("dup", time.Time{}, ScopeMCP)
	assert.ErrorIs(t, err, ErrConnectorExists)
}

func (s *ConnectorSuite) TestCreateRequiresName() {
	_, _, err := s.store().Create("   ", time.Time{}, ScopeMCP)
	assert.Error(s.T(), err, "Create accepted a blank name")
}

func (s *ConnectorSuite) TestPersistenceAcrossLoads() {
	t := s.T()

	secret, _, err := s.store().Create("ide", time.Now().Add(time.Hour), ScopeMCP)
	require.NoError(t, err)

	// A fresh load sees the persisted entry and verifies the same secret.
	reloaded := s.store()
	require.Len(t, reloaded.List(), 1)
	assert.True(t, reloaded.VerifyScope(secret, ScopeMCP))

	// One file per token, named after the token, so revoking is an rm.
	path := filepath.Join(s.stateDir, "magus", "connectors.d", "ide.json")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "token file perms")

	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm(), "store dir perms")
}

func (s *ConnectorSuite) TestLoadRejectsInsecurePerms() {
	t := s.T()
	_, _, err := s.store().Create("x", time.Time{}, ScopeMCP)
	require.NoError(t, err)
	path := filepath.Join(s.stateDir, "magus", "connectors.d", "x.json")
	require.NoError(t, os.Chmod(path, 0o644))

	_, err = LoadConnectorStore()
	assert.Error(t, err, "LoadConnectorStore accepted a world-readable store")
	assert.ErrorIs(t, err, types.InsecureTokenPermissions, "the error carries MGS9002")
}

func (s *ConnectorSuite) TestRevoke() {
	t := s.T()
	st := s.store()

	secret, c, err := st.Create("gone", time.Time{}, ScopeMCP)
	require.NoError(t, err)

	// Revoke by name; the token stops verifying.
	removed, err := st.Revoke("gone")
	require.NoError(t, err)
	assert.Equal(t, c, removed)
	assert.False(t, st.VerifyScope(secret, ScopeMCP), "revoked token still verifies")
	assert.Empty(t, st.List())

	_, err = st.Revoke("gone")
	assert.ErrorIs(t, err, ErrConnectorNotFound)
}

func (s *ConnectorSuite) TestRevokeByFingerprintAndPrefix() {
	t := s.T()
	st := s.store()

	_, c, err := st.Create("byfp", time.Time{}, ScopeMCP)
	require.NoError(t, err)

	// Exact fingerprint.
	_, err = st.Revoke(c.Fingerprint)
	require.NoError(t, err)

	// Prefix match.
	_, c2, err := st.Create("byprefix", time.Time{}, ScopeMCP)
	require.NoError(t, err)
	_, err = st.Revoke(c2.Fingerprint[:4])
	require.NoError(t, err)
	assert.Empty(t, st.List())
}

func (s *ConnectorSuite) TestExpiredTokenDoesNotVerify() {
	t := s.T()
	st := s.store()

	past, _, err := st.Create("expired", time.Now().Add(-time.Minute), ScopeMCP)
	require.NoError(t, err)
	assert.False(t, st.VerifyScope(past, ScopeMCP), "expired token verified")

	future, _, err := st.Create("live", time.Now().Add(time.Hour), ScopeMCP)
	require.NoError(t, err)
	assert.True(t, st.VerifyScope(future, ScopeMCP), "non-expired token failed to verify")

	never, _, err := st.Create("never", time.Time{}, ScopeMCP)
	require.NoError(t, err)
	assert.True(t, st.VerifyScope(never, ScopeMCP), "never-expiring token failed to verify")
}

func (s *ConnectorSuite) TestVerifyRejectsGarbageOffline() {
	st := s.store()
	for _, bad := range []string{"", "not-a-token", "mgs_short", "ghp_wrongprefix"} {
		assert.False(s.T(), st.VerifyScope(bad, ScopeMCP), "Verify accepted garbage %q", bad)
	}
}

// TestVerifyTwoTier exercises the composite daemon verifier: it accepts the
// retrievable cli token, accepts a non-expired connector token, and rejects an
// expired connector token and outright garbage.
func (s *ConnectorSuite) TestVerifyTwoTier() {
	t := s.T()

	// No credentials at all: everything is rejected.
	assert.False(t, VerifyMCPBearer("anything"))

	// cli token tier.
	cli, err := Generate()
	require.NoError(t, err)
	_, err = SaveNew(cli)
	require.NoError(t, err)
	assert.True(t, VerifyMCPBearer(cli), "cli token not accepted")
	assert.False(t, VerifyMCPBearer(cli+"x"), "near-miss cli token accepted")

	// connector tier.
	live, _, err := s.store().Create("live", time.Now().Add(time.Hour), ScopeMCP)
	require.NoError(t, err)
	assert.True(t, VerifyMCPBearer(live), "live connector token not accepted")

	expired, _, err := s.store().Create("expired", time.Now().Add(-time.Hour), ScopeMCP)
	require.NoError(t, err)
	assert.False(t, VerifyMCPBearer(expired), "expired connector token accepted")

	assert.False(t, VerifyMCPBearer("mgs_not_a_real_token"), "garbage accepted")
}

// The MCP and console surfaces must not share a credential class. A connector token
// is minted for an agent, so accepting it on the console would let a leaked agent
// credential drive the console's mutating routes; the two used to share one verifier
// and did exactly that. The operator token is the deliberate exception - it is the
// bootstrap credential and the CLI's own reads depend on it.
func (s *ConnectorSuite) TestConsoleRejectsConnectorTokens() {
	t := s.T()

	cli, err := Generate()
	require.NoError(t, err)
	_, err = SaveNew(cli)
	require.NoError(t, err)

	live, _, err := s.store().Create("agent", time.Now().Add(time.Hour), ScopeMCP)
	require.NoError(t, err)

	assert.True(t, VerifyMCPBearer(live), "a connector token must still reach /mcp")
	assert.False(t, VerifyConsoleBearer(live), "a connector token must NOT reach the console")

	assert.True(t, VerifyConsoleBearer(cli), "the operator token opens both surfaces by design")
	assert.True(t, VerifyMCPBearer(cli))
}

// TestConcurrentCreateNoLostUpdates proves N independent creates all survive.
// It used to prove the store lock closed a read-modify-write race; there is no
// longer a read-modify-write to race, because each create writes its own file and
// reads nobody else's. The test stays because the PROPERTY is what mattered.
func (s *ConnectorSuite) TestConcurrentCreateNoLostUpdates() {
	t := s.T()
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := LoadConnectorStore()
			if err != nil {
				errs[i] = err
				return
			}
			_, _, errs[i] = st.Create(fmt.Sprintf("c%d", i), time.Time{}, ScopeMCP)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "concurrent create %d", i)
	}
	assert.Len(t, s.store().List(), n, "concurrent creates lost entries")
}

// TestConcurrentCreateNeedsNoLockFile is the other half: the directory layout
// removed the lock, so nothing may be left behind to strand a later mutation the
// way an orphaned lock file once did.
func (s *ConnectorSuite) TestConcurrentCreateNeedsNoLockFile() {
	t := s.T()
	_, _, err := s.store().Create("first", time.Time{}, ScopeMCP)
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(s.stateDir, "magus", "connectors.d"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "first.json", entries[0].Name(), "a create left something besides the token behind")
}

// TestRevokeByRemovingTheFile is the ergonomic the directory exists for: the store
// must agree with the filesystem, so `rm` is a revoke even when magus is not
// available to perform one.
func (s *ConnectorSuite) TestRevokeByRemovingTheFile() {
	t := s.T()
	secret, _, err := s.store().Create("byhand", time.Time{}, ScopeMCP)
	require.NoError(t, err)
	require.True(t, s.store().VerifyScope(secret, ScopeMCP))

	require.NoError(t, os.Remove(filepath.Join(s.stateDir, "magus", "connectors.d", "byhand.json")))
	assert.False(t, s.store().VerifyScope(secret, ScopeMCP), "a removed file must stop verifying")
	assert.Empty(t, s.store().List())
}

// TestCreateRejectsUnsafeName: the name became a path component when the store
// became a directory, so a slash is a traversal rather than a cosmetic problem.
func (s *ConnectorSuite) TestCreateRejectsUnsafeName() {
	t := s.T()
	for _, name := range []string{"../escape", "a/b", ".hidden", "has space", "sub/../x"} {
		_, _, err := s.store().Create(name, time.Time{}, ScopeMCP)
		require.Error(t, err, "Create accepted unsafe name %q", name)
	}
}

// TestMigratesLegacyStore moves a pre-directory connectors.json into the
// directory once, keeping every token verifiable across the change.
func (s *ConnectorSuite) TestMigratesLegacyStore() {
	t := s.T()
	dir := filepath.Join(s.stateDir, "magus")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	legacy := filepath.Join(dir, "connectors.json")
	require.NoError(t, os.WriteFile(legacy, []byte(`{"version":1,"tokens":[
		{"name":"claude-code","sha256":"aa","fingerprint":"aa","created":"2026-07-14T17:35:33Z","expires":"0001-01-01T00:00:00Z"},
		{"name":"has space","sha256":"bb","fingerprint":"bbbbbbbb","created":"2026-07-14T17:35:33Z","expires":"0001-01-01T00:00:00Z"}
	]}`), 0o600))

	list := s.store().List()
	require.Len(t, list, 2, "both legacy tokens must survive")
	assert.Equal(t, "claude-code", list[0].Name)
	assert.Equal(t, "has space", list[1].Name, "the record keeps the real name")

	// A name that is not a legal filename falls back to its fingerprint.
	assert.FileExists(t, filepath.Join(dir, "connectors.d", "claude-code.json"))
	assert.FileExists(t, filepath.Join(dir, "connectors.d", "bbbbbbbb.json"))

	// The old file is retired rather than deleted, and the migration runs once.
	assert.NoFileExists(t, legacy)
	assert.FileExists(t, legacy+".migrated")
	assert.Len(t, s.store().List(), 2, "a second load must not re-migrate or duplicate")
}

// TestRejectsNewerStoreVersion confirms a store written by a hypothetical future
// magus (higher schema version) is refused rather than silently misread.
func (s *ConnectorSuite) TestRejectsNewerStoreVersion() {
	t := s.T()
	dir := filepath.Join(s.stateDir, "magus", "connectors.d")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "future.json"), []byte(`{"version":999,"name":"future"}`), 0o600))

	_, err := LoadConnectorStore()
	assert.Error(t, err, "load accepted a store version newer than supported")
	assert.ErrorIs(t, err, types.ConnectorStoreTooNew, "the error carries MGS9003")
}

// --- format-only tests (no state isolation needed) ---

func TestMintTokenFormat(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 2000; i++ {
		tok, err := mintToken()
		require.NoError(t, err)
		assert.Len(t, tok, len(tokenPrefix)+tokenBodyLen+tokenCheckLen)
		assert.True(t, validTokenFormat(tok), "mintToken produced an invalid token: %q", tok)
		_, dup := seen[tok]
		require.False(t, dup, "mintToken produced a duplicate")
		seen[tok] = struct{}{}
	}
}

func TestValidTokenFormatRejects(t *testing.T) {
	good, err := mintToken()
	require.NoError(t, err)
	require.True(t, validTokenFormat(good))

	// Wrong prefix.
	assert.False(t, validTokenFormat("xxx_"+good[4:]))
	// Truncated / too long.
	assert.False(t, validTokenFormat(good[:len(good)-1]))
	assert.False(t, validTokenFormat(good+"0"))
	// Non-base62 byte in the body ('_' is outside the alphabet).
	assert.False(t, validTokenFormat(good[:10]+"_"+good[11:]))

	// A single-character typo in the body is caught by the checksum.
	typo := []byte(good)
	if typo[6] == '0' {
		typo[6] = '1'
	} else {
		typo[6] = '0'
	}
	assert.False(t, validTokenFormat(string(typo)), "checksum did not catch a body typo")
}

func TestBase62EncodeWidthAndPadding(t *testing.T) {
	// Zero encodes to all-'0' at the requested width.
	assert.Equal(t, "000000", base62Encode([]byte{0, 0, 0, 0}, 6))
	// 61 is the last alphabet char, right-aligned.
	assert.Equal(t, "00000z", base62Encode([]byte{61}, 6))
	// 62 rolls over to "10".
	assert.Equal(t, "0010", base62Encode([]byte{62}, 4))
}

func TestBase62EncodeOverflowPanics(t *testing.T) {
	// 62^1 == 62 values fit in width 1 (0..61); 62 does not.
	assert.Panics(t, func() { base62Encode([]byte{62}, 1) })
}
