package auth

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// TokenSuite isolates XDG_STATE_HOME per test, so no test touches the real state dir.
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
	_, err := LoadOperator()
	require.ErrorIs(t, err, ErrNoToken, "LoadOperator on empty")

	tok, err := GenerateOperator()
	require.NoError(t, err)
	class, ok := classOf(tok)
	require.True(t, ok, "GenerateOperator must mint a well-formed token")
	assert.Equal(t, types.ClassOperator, class)

	path, err := SaveOperator(tok)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := LoadOperator()
	require.NoError(t, err)
	assert.Equal(t, tok, got)
}

func (s *TokenSuite) TestLoadRejectsInsecurePerms() {
	t := s.T()
	tok, _ := GenerateOperator()
	path, err := SaveOperator(tok)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o644))

	_, err = LoadOperator()
	assert.ErrorIs(t, err, types.InsecureTokenPermissions)
}

// An operator file written before the class prefix, or edited into anything else, is refused
// with MGS9016 naming the command that re-issues it, from LoadOperator and from
// EnsureOperator: the server does not start on it and no command runs on it.
func (s *TokenSuite) TestOldOperatorFileIsRefusedWithTheReissueCommand() {
	t := s.T()
	for _, old := range []string{
		"dGhpcyBpcyAzMiBieXRlcyBvZiBiYXNlNjR1cmwgZGF0YQ", // base64url, the old format
		"",
		prefixStored + "00000000000000000000000000000000000000000000000000", // an mgs_ body in the operator file
	} {
		path, err := OperatorPath()
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(old+"\n"), 0o600))

		_, err = LoadOperator()
		require.ErrorIs(t, err, types.OperatorTokenFormat, "%q", old)
		assert.Contains(t, err.Error(), "config token generate --force")

		_, err = EnsureOperator(t.Context(), slog.New(slog.DiscardHandler))
		require.ErrorIs(t, err, types.OperatorTokenFormat, "the server must not start on %q", old)
	}
	// A valid store token copied into the operator file is refused too: its class is wrong.
	stored, err := mintSecret(types.ClassStored)
	require.NoError(t, err)
	_, err = SaveOperator(stored)
	require.NoError(t, err)
	_, err = LoadOperator()
	assert.ErrorIs(t, err, types.OperatorTokenFormat)
}

func (s *TokenSuite) TestRevokeOperator() {
	t := s.T()
	require.NoError(t, RevokeOperator(), "RevokeOperator on empty")
	tok, _ := GenerateOperator()
	_, err := SaveOperator(tok)
	require.NoError(t, err)
	require.NoError(t, RevokeOperator())
	_, err = LoadOperator()
	assert.ErrorIs(t, err, ErrNoToken)
}

func (s *TokenSuite) TestOperatorPathLocation() {
	t := s.T()
	path, err := OperatorPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(s.stateDir, "magus", "mcp_token"), path)
}

// The guard re-reads the operator file per request: a rotate or revoke takes effect without
// rebuilding the handler.
func (s *TokenSuite) TestGuardHotReload() {
	t := s.T()
	a, _ := GenerateOperator()
	_, err := SaveNewOperator(a)
	require.NoError(t, err)
	need := types.Need{Surface: types.SurfaceMCP, Level: types.LevelWrite}
	h, err := httpx.BearerGuard(rpcerr.FormatJSON, Verify, need, okHandler)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, reqStatus(h, "Bearer "+a))
	b, _ := GenerateOperator()
	_, err = SaveOperator(b)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, reqStatus(h, "Bearer "+a), "old token after rotate")
	assert.Equal(t, http.StatusOK, reqStatus(h, "Bearer "+b))
	require.NoError(t, RevokeOperator())
	assert.Equal(t, http.StatusUnauthorized, reqStatus(h, "Bearer "+b), "after revoke")
}

// EnsureOperator persists a token on first use and never writes the secret to the log.
func (s *TokenSuite) TestEnsureOperatorGeneratesWithoutLoggingSecret() {
	t := s.T()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	tok, err := EnsureOperator(t.Context(), log)
	require.NoError(t, err)
	got, err := LoadOperator()
	require.NoError(t, err)
	assert.Equal(t, tok, got)
	assert.NotContains(t, buf.String(), tok, "the secret leaked into the log")

	again, err := EnsureOperator(t.Context(), log)
	require.NoError(t, err)
	assert.Equal(t, tok, again, "a second call returns the persisted token")
}

func (s *TokenSuite) TestSaveNewOperatorRefusesOverwrite() {
	t := s.T()
	first, _ := GenerateOperator()
	_, err := SaveNewOperator(first)
	require.NoError(t, err)
	_, err = SaveNewOperator("other")
	assert.ErrorIs(t, err, os.ErrExist)
	got, _ := LoadOperator()
	assert.Equal(t, first, got, "token clobbered")
}

func TestTokenIDStable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, TokenID("abc"), TokenID("abc"))
	assert.NotEqual(t, TokenID("abc"), TokenID("abd"))
	assert.Len(t, TokenID("abc"), 8)
}

// reqStatus drives one request from a loopback peer through h with the given Authorization
// header (empty = none).
func reqStatus(h http.Handler, authHeader string) int {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}
