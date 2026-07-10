package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBearerGuard(t *testing.T) {
	t.Parallel()

	const token = "s3cret-token-value"

	// serve drives one request (optional Authorization header and raw query
	// string) through the guard and returns the recorder for inspection.
	serve := func(authHeader, rawQuery string) *httptest.ResponseRecorder {
		target := "/mcp"
		if rawQuery != "" {
			target += "?" + rawQuery
		}
		req := httptest.NewRequest(http.MethodPost, target, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rr := httptest.NewRecorder()
		load := func() (string, error) { return token, nil }
		BearerGuard(load, okHandler).ServeHTTP(rr, req)
		return rr
	}

	authorized := func(t *testing.T, authHeader, rawQuery string) {
		t.Helper()
		assert.Equal(t, http.StatusOK, serve(authHeader, rawQuery).Code)
	}

	rejected := func(t *testing.T, authHeader, rawQuery string) {
		t.Helper()
		rr := serve(authHeader, rawQuery)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.NotEmpty(t, rr.Header().Get("WWW-Authenticate"), "missing WWW-Authenticate challenge")
	}

	// Header path.
	t.Run("valid bearer", func(t *testing.T) { authorized(t, "Bearer "+token, "") })
	t.Run("valid bearer lowercase scheme", func(t *testing.T) { authorized(t, "bearer "+token, "") })
	t.Run("valid bearer mixed-case scheme", func(t *testing.T) { authorized(t, "BeArEr "+token, "") })
	// Query-param path (EventSource cannot set headers).
	t.Run("valid query token", func(t *testing.T) { authorized(t, "", "token="+token) })
	t.Run("header wins over query", func(t *testing.T) { authorized(t, "Bearer "+token, "token=wrong") })
	t.Run("query fallback when header absent", func(t *testing.T) { authorized(t, "", "foo=bar&token="+token) })
	// Rejections.
	t.Run("no header no query", func(t *testing.T) { rejected(t, "", "") })
	t.Run("wrong token header", func(t *testing.T) { rejected(t, "Bearer not-the-token", "") })
	t.Run("wrong token query", func(t *testing.T) { rejected(t, "", "token=not-the-token") })
	t.Run("token as prefix of real one", func(t *testing.T) { rejected(t, "Bearer s3cret", "") })
	t.Run("real token plus suffix", func(t *testing.T) { rejected(t, "Bearer "+token+"x", "") })
	t.Run("missing scheme", func(t *testing.T) { rejected(t, token, "") })
	t.Run("wrong scheme", func(t *testing.T) { rejected(t, "Basic "+token, "") })
	t.Run("empty bearer", func(t *testing.T) { rejected(t, "Bearer ", "") })
	t.Run("empty query token", func(t *testing.T) { rejected(t, "", "token=") })
}

// TestBearerGuardLoadErrorFailsClosed confirms a token-load error denies access
// even when the client presents a plausible token.
func TestBearerGuardLoadErrorFailsClosed(t *testing.T) {
	t.Parallel()
	load := func() (string, error) { return "", errors.New("revoked") }
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rr := httptest.NewRecorder()
	BearerGuard(load, okHandler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	ok := func(t *testing.T, header, want string) {
		t.Helper()
		got, ok := bearerToken(header)
		assert.True(t, ok)
		assert.Equal(t, want, got)
	}

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
