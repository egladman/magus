package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestGuard(t *testing.T) {
	t.Parallel()

	const token = "s3cret-token-value"

	// serve drives one request with the given Authorization header (empty =
	// none) through the guard and returns the recorder for inspection.
	serve := func(authHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rr := httptest.NewRecorder()
		load := func() (string, error) { return token, nil }
		Guard(load, okHandler).ServeHTTP(rr, req)
		return rr
	}

	// authorized asserts the header grants access.
	authorized := func(t *testing.T, authHeader string) {
		t.Helper()
		assert.Equal(t, http.StatusOK, serve(authHeader).Code)
	}

	// rejected asserts the header is denied and a challenge is issued.
	rejected := func(t *testing.T, authHeader string) {
		t.Helper()
		rr := serve(authHeader)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.NotEmpty(t, rr.Header().Get("WWW-Authenticate"), "missing WWW-Authenticate challenge")
	}

	t.Run("valid bearer", func(t *testing.T) { authorized(t, "Bearer "+token) })
	t.Run("valid bearer lowercase scheme", func(t *testing.T) { authorized(t, "bearer "+token) })
	t.Run("valid bearer mixed-case scheme", func(t *testing.T) { authorized(t, "BeArEr "+token) })
	t.Run("no header", func(t *testing.T) { rejected(t, "") })
	t.Run("wrong token", func(t *testing.T) { rejected(t, "Bearer not-the-token") })
	t.Run("token as prefix of real one", func(t *testing.T) { rejected(t, "Bearer s3cret") })
	t.Run("real token plus suffix", func(t *testing.T) { rejected(t, "Bearer "+token+"x") })
	t.Run("missing scheme", func(t *testing.T) { rejected(t, token) })
	t.Run("wrong scheme", func(t *testing.T) { rejected(t, "Basic "+token) })
	t.Run("empty bearer", func(t *testing.T) { rejected(t, "Bearer ") })
	t.Run("bearer whitespace only", func(t *testing.T) { rejected(t, "Bearer    ") })
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	// ok asserts header parses to the expected token.
	ok := func(t *testing.T, header, want string) {
		t.Helper()
		got, ok := bearerToken(header)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}

	// fails asserts header is not a valid bearer credential.
	fails := func(t *testing.T, header string) {
		t.Helper()
		got, ok := bearerToken(header)
		assert.False(t, ok)
		assert.Empty(t, got)
	}

	t.Run("Bearer abc", func(t *testing.T) { ok(t, "Bearer abc", "abc") })
	t.Run("bearer abc", func(t *testing.T) { ok(t, "bearer abc", "abc") })
	t.Run("Bearer padded", func(t *testing.T) { ok(t, "Bearer   abc  ", "abc") })
	t.Run("empty value", func(t *testing.T) { fails(t, "Bearer ") })
	t.Run("scheme only", func(t *testing.T) { fails(t, "Bearer") })
	t.Run("empty header", func(t *testing.T) { fails(t, "") })
	t.Run("wrong scheme", func(t *testing.T) { fails(t, "Basic abc") })
	t.Run("no scheme", func(t *testing.T) { fails(t, "abc") })
}
