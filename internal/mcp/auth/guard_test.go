package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestGuard(t *testing.T) {
	t.Parallel()

	const token = "s3cret-token-value"

	cases := []struct {
		name       string
		authHeader string
		want       int
	}{
		{"valid bearer", "Bearer " + token, http.StatusOK},
		{"valid bearer lowercase scheme", "bearer " + token, http.StatusOK},
		{"valid bearer mixed-case scheme", "BeArEr " + token, http.StatusOK},
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer not-the-token", http.StatusUnauthorized},
		{"token as prefix of real one", "Bearer s3cret", http.StatusUnauthorized},
		{"real token plus suffix", "Bearer " + token + "x", http.StatusUnauthorized},
		{"missing scheme", token, http.StatusUnauthorized},
		{"wrong scheme", "Basic " + token, http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"bearer whitespace only", "Bearer    ", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()
			load := func() (string, error) { return token, nil }
			Guard(load, okHandler).ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Errorf("auth=%q: got %d, want %d", tc.authHeader, rr.Code, tc.want)
			}
			if tc.want == http.StatusUnauthorized {
				if got := rr.Header().Get("WWW-Authenticate"); got == "" {
					t.Errorf("auth=%q: missing WWW-Authenticate challenge", tc.authHeader)
				}
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		header string
		want   string
		wantOK bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"Bearer   abc  ", "abc", true},
		{"Bearer ", "", false},
		{"Bearer", "", false},
		{"", "", false},
		{"Basic abc", "", false},
		{"abc", "", false},
	}
	for _, tc := range cases {
		got, ok := bearerToken(tc.header)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("bearerToken(%q) = (%q, %v), want (%q, %v)", tc.header, got, ok, tc.want, tc.wantOK)
		}
	}
}
