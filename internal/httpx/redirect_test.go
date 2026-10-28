package httpx

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	return req
}

func TestGuardRedirectRefusesHTTPSToHTTP(t *testing.T) {
	t.Parallel()
	secure := mustRequest(t, "https://a.example/x")
	plain := mustRequest(t, "http://a.example/x")

	require.ErrorContains(t, GuardRedirect(plain, []*http.Request{secure}), "refused a redirect from https to http://a.example/x")
	require.NoError(t, GuardRedirect(secure, []*http.Request{plain}))
	require.NoError(t, GuardRedirect(plain, []*http.Request{plain}))
}

func TestGuardRedirectStripsCredentialHeadersAcrossHosts(t *testing.T) {
	t.Parallel()
	first := mustRequest(t, "https://a.example/x")
	cross := mustRequest(t, "https://b.example/y")
	cross.Header.Set("Authorization", "Bearer t")
	cross.Header.Set("X-Api-Key", "k")
	cross.Header.Set("Accept", "application/json")

	require.NoError(t, GuardRedirect(cross, []*http.Request{first}))
	assert.Equal(t, http.Header{"Accept": {"application/json"}}, cross.Header)

	same := mustRequest(t, "https://a.example/z")
	same.Header.Set("Authorization", "Bearer t")
	require.NoError(t, GuardRedirect(same, []*http.Request{first}))
	assert.Equal(t, http.Header{"Authorization": {"Bearer t"}}, same.Header)
}

func TestGuardRedirectStopsAfterMaxRedirects(t *testing.T) {
	t.Parallel()
	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i] = mustRequest(t, "https://a.example/x")
	}
	require.ErrorContains(t, GuardRedirect(mustRequest(t, "https://a.example/y"), via), "stopped after 10 redirects")
	require.NoError(t, GuardRedirect(mustRequest(t, "https://a.example/y"), via[:maxRedirects-1]))
}

func TestIsCredentialHeader(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Auth-Token", "Proxy-Authorization", "X-Secret"} {
		assert.True(t, isCredentialHeader(name), name)
	}
	for _, name := range []string{"Accept", "Content-Type", "User-Agent"} {
		assert.False(t, isCredentialHeader(name), name)
	}
}
