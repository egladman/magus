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

	secret, c, err := st.Create("claude", time.Now().Add(DefaultConnectorTTL))
	require.NoError(t, err)
	assert.True(t, validTokenFormat(secret), "minted token not well-formed: %q", secret)
	assert.Equal(t, "claude", c.Name)
	assert.Len(t, c.Fingerprint, 8)
	assert.NotContains(t, c.SHA256, secret, "stored entry leaked the secret")

	// The entry never carries the plaintext.
	list := st.List()
	require.Len(t, list, 1)
	assert.Equal(t, c, list[0])

	assert.True(t, st.Verify(secret), "Verify rejected the freshly minted token")

	// A validly-formatted but never-stored token must not verify.
	other, err := mintToken()
	require.NoError(t, err)
	require.True(t, validTokenFormat(other))
	assert.False(t, st.Verify(other), "Verify accepted a non-stored token")
}

func (s *ConnectorSuite) TestCreateRejectsDuplicateName() {
	t := s.T()
	st := s.store()

	_, _, err := st.Create("dup", time.Time{})
	require.NoError(t, err)
	_, _, err = st.Create("dup", time.Time{})
	assert.ErrorIs(t, err, ErrConnectorExists)
}

func (s *ConnectorSuite) TestCreateRequiresName() {
	_, _, err := s.store().Create("   ", time.Time{})
	assert.Error(s.T(), err, "Create accepted a blank name")
}

func (s *ConnectorSuite) TestPersistenceAcrossLoads() {
	t := s.T()

	secret, _, err := s.store().Create("ide", time.Now().Add(time.Hour))
	require.NoError(t, err)

	// A fresh load sees the persisted entry and verifies the same secret.
	reloaded := s.store()
	require.Len(t, reloaded.List(), 1)
	assert.True(t, reloaded.Verify(secret))

	path, err := connectorStorePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(s.stateDir, "magus", "connectors.json"), path)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "store perms")
}

func (s *ConnectorSuite) TestLoadRejectsInsecurePerms() {
	t := s.T()
	_, _, err := s.store().Create("x", time.Time{})
	require.NoError(t, err)
	path, _ := connectorStorePath()
	require.NoError(t, os.Chmod(path, 0o644))

	_, err = LoadConnectorStore()
	assert.Error(t, err, "LoadConnectorStore accepted a world-readable store")
	assert.ErrorIs(t, err, types.InsecureTokenPermissions, "the error carries MGS9002")
}

func (s *ConnectorSuite) TestRevoke() {
	t := s.T()
	st := s.store()

	secret, c, err := st.Create("gone", time.Time{})
	require.NoError(t, err)

	// Revoke by name; the token stops verifying.
	removed, err := st.Revoke("gone")
	require.NoError(t, err)
	assert.Equal(t, c, removed)
	assert.False(t, st.Verify(secret), "revoked token still verifies")
	assert.Empty(t, st.List())

	_, err = st.Revoke("gone")
	assert.ErrorIs(t, err, ErrConnectorNotFound)
}

func (s *ConnectorSuite) TestRevokeByFingerprintAndPrefix() {
	t := s.T()
	st := s.store()

	_, c, err := st.Create("byfp", time.Time{})
	require.NoError(t, err)

	// Exact fingerprint.
	_, err = st.Revoke(c.Fingerprint)
	require.NoError(t, err)

	// Prefix match.
	_, c2, err := st.Create("byprefix", time.Time{})
	require.NoError(t, err)
	_, err = st.Revoke(c2.Fingerprint[:4])
	require.NoError(t, err)
	assert.Empty(t, st.List())
}

func (s *ConnectorSuite) TestExpiredTokenDoesNotVerify() {
	t := s.T()
	st := s.store()

	past, _, err := st.Create("expired", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	assert.False(t, st.Verify(past), "expired token verified")

	future, _, err := st.Create("live", time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, st.Verify(future), "non-expired token failed to verify")

	never, _, err := st.Create("never", time.Time{})
	require.NoError(t, err)
	assert.True(t, st.Verify(never), "never-expiring token failed to verify")
}

func (s *ConnectorSuite) TestVerifyRejectsGarbageOffline() {
	st := s.store()
	for _, bad := range []string{"", "not-a-token", "mgs_short", "ghp_wrongprefix"} {
		assert.False(s.T(), st.Verify(bad), "Verify accepted garbage %q", bad)
	}
}

// TestVerifyTwoTier exercises the composite daemon verifier: it accepts the
// retrievable cli token, accepts a non-expired connector token, and rejects an
// expired connector token and outright garbage.
func (s *ConnectorSuite) TestVerifyTwoTier() {
	t := s.T()

	// No credentials at all: everything is rejected.
	assert.False(t, VerifyBearer("anything"))

	// cli token tier.
	cli, err := Generate()
	require.NoError(t, err)
	_, err = SaveNew(cli)
	require.NoError(t, err)
	assert.True(t, VerifyBearer(cli), "cli token not accepted")
	assert.False(t, VerifyBearer(cli+"x"), "near-miss cli token accepted")

	// connector tier.
	live, _, err := s.store().Create("live", time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, VerifyBearer(live), "live connector token not accepted")

	expired, _, err := s.store().Create("expired", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.False(t, VerifyBearer(expired), "expired connector token accepted")

	assert.False(t, VerifyBearer("mgs_not_a_real_token"), "garbage accepted")
}

// TestConcurrentCreateNoLostUpdates proves the store lock closes the
// read-modify-write race: N processes each loading, appending, and saving
// independently must all survive, not clobber each other last-writer-wins.
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
			_, _, errs[i] = st.Create(fmt.Sprintf("c%d", i), time.Time{})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "concurrent create %d", i)
	}
	assert.Len(t, s.store().List(), n, "concurrent creates lost entries")
}

// TestStaleLockIsStolen proves a lock file orphaned by a crashed process (old
// mtime) is stolen rather than bricking every future mutation.
func (s *ConnectorSuite) TestStaleLockIsStolen() {
	t := s.T()
	path, err := connectorStorePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))

	lock := path + ".lock"
	require.NoError(t, os.WriteFile(lock, nil, 0o600))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(lock, old, old))

	_, _, err = s.store().Create("after-crash", time.Time{})
	require.NoError(t, err, "Create did not steal the stale lock")
	assert.Len(t, s.store().List(), 1)
}

// TestRejectsNewerStoreVersion confirms a store written by a hypothetical future
// magus (higher schema version) is refused rather than silently misread.
func (s *ConnectorSuite) TestRejectsNewerStoreVersion() {
	t := s.T()
	path, err := connectorStorePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"version":999,"tokens":[]}`), 0o600))

	_, err = LoadConnectorStore()
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
