package auth

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/httpx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// okHandler is the terminal handler the guard delegates to once a request is
// authenticated.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// TokenSuite isolates the state dir at a temp dir before each test so they
// never touch the real user directory. The token lives under the state dir
// (XDG_STATE_HOME).
type TokenSuite struct {
	suite.Suite
	stateDir string
}

func (s *TokenSuite) SetupTest() {
	s.stateDir = s.T().TempDir()
	s.T().Setenv("XDG_STATE_HOME", s.stateDir)
}

func TestTokenSuite(t *testing.T) {
	suite.Run(t, new(TokenSuite))
}

func (s *TokenSuite) TestRoundTrip() {
	t := s.T()

	_, err := Load()
	require.ErrorIs(t, err, ErrNoToken, "Load on empty")
	ok, err := Exists()
	require.NoError(t, err)
	assert.False(t, ok, "Exists on empty")

	tok, err := Generate()
	require.NoError(t, err)
	require.NotEmpty(t, tok, "Generate returned empty token")

	path, err := Save(tok)
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err, "stat saved token")
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "token perms")

	got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, tok, got)

	ok, err = Exists()
	require.NoError(t, err)
	assert.True(t, ok, "Exists after Save")
}

func (s *TokenSuite) TestLoadRejectsInsecurePerms() {
	t := s.T()

	tok, _ := Generate()
	path, err := Save(tok)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o644))

	_, err = Load()
	assert.Error(t, err, "Load accepted a world-readable token file; want error")
}

func (s *TokenSuite) TestRevoke() {
	t := s.T()

	// Revoke with no token is a no-op.
	require.NoError(t, Revoke(), "Revoke on empty")

	tok, _ := Generate()
	_, err := Save(tok)
	require.NoError(t, err)
	require.NoError(t, Revoke())

	_, err = Load()
	assert.ErrorIs(t, err, ErrNoToken, "Load after Revoke")
}

func (s *TokenSuite) TestPathLocation() {
	t := s.T()

	path, err := Path()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(s.stateDir, "magus", "mcp_token"), path)
}

// TestGuardHotReload proves httpx.BearerGuard(VerifyBearer, ...) picks up a rotated or
// revoked cli token without rebuilding the handler — the daemon's hot-reload
// contract. It lives here (not in httpx) because it exercises the persistent
// token store's Save/Revoke path feeding the shared guard.
func (s *TokenSuite) TestGuardHotReload() {
	t := s.T()

	a, _ := Generate()
	_, err := SaveNew(a)
	require.NoError(t, err, "SaveNew")
	h := httpx.BearerGuard(VerifyBearer, okHandler)

	assert.Equal(t, http.StatusOK, reqStatus(h, "Bearer "+a), "token A")

	// Rotate to B without touching the handler.
	b, _ := Generate()
	_, err = Save(b)
	require.NoError(t, err, "Save")
	assert.Equal(t, http.StatusUnauthorized, reqStatus(h, "Bearer "+a), "old token A after rotate")
	assert.Equal(t, http.StatusOK, reqStatus(h, "Bearer "+b), "new token B")

	// Revoke fails closed.
	require.NoError(t, Revoke())
	assert.Equal(t, http.StatusUnauthorized, reqStatus(h, "Bearer "+b), "after revoke")
}

// TestResolveGeneratesWithoutLoggingSecret pins the security-critical contract:
// Resolve provisions and persists a token on first use but never writes the
// secret to the logger (which commonly lands in journald/nohup.out).
func (s *TokenSuite) TestResolveGeneratesWithoutLoggingSecret() {
	t := s.T()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	tok, err := Resolve(log)
	require.NoError(t, err)
	require.NotEmpty(t, tok, "Resolve returned empty token")

	got, err := Load()
	require.NoError(t, err, "Load after Resolve")
	assert.Equal(t, tok, got, "persisted token")

	assert.NotContains(t, buf.String(), tok, "Resolve leaked the secret into the log")
}

// TestResolveIdempotent confirms a second Resolve returns the already-persisted
// token instead of minting a new one.
func (s *TokenSuite) TestResolveIdempotent() {
	t := s.T()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := Resolve(log)
	require.NoError(t, err)
	b, err := Resolve(log)
	require.NoError(t, err)
	assert.Equal(t, a, b, "Resolve not idempotent")
}

// TestSaveNewRefusesOverwrite confirms the create-only write fails closed (with
// os.ErrExist) rather than clobbering an existing token.
func (s *TokenSuite) TestSaveNewRefusesOverwrite() {
	t := s.T()

	first, _ := Generate()
	_, err := SaveNew(first)
	require.NoError(t, err, "SaveNew first")

	_, err = SaveNew("other")
	assert.ErrorIs(t, err, os.ErrExist, "SaveNew over existing")

	got, _ := Load()
	assert.Equal(t, first, got, "token clobbered")
}

// TestGenerateUnique needs no config isolation: it only checks Generate's
// randomness, so it stays a plain top-level test.
func TestGenerateUnique(t *testing.T) {
	a, err := Generate()
	require.NoError(t, err)
	b, err := Generate()
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "Generate produced identical tokens")
}

// TestFingerprintStable pins Fingerprint's determinism and 8-char width without
// needing config isolation.
func TestFingerprintStable(t *testing.T) {
	const tok = "abc"
	assert.Equal(t, Fingerprint(tok), Fingerprint(tok), "Fingerprint not deterministic")
	assert.NotEqual(t, Fingerprint("abc"), Fingerprint("abd"), "Fingerprint collision on distinct tokens")
	assert.Len(t, Fingerprint(tok), 8)
}

// reqStatus drives one request through h with the given Authorization header
// (empty = none) and returns the status code.
func reqStatus(h http.Handler, authHeader string) int {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}
