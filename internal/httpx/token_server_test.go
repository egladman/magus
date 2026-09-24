package httpx

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every route of a per-run page server holds its one page's token and nothing else: no token
// and any other token, a daemon operator token included, are refused, and the page's own is
// admitted by header or by the query an EventSource sends.
func TestTokenServerRoutesAdmitOnlyThePageToken(t *testing.T) {
	t.Parallel()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ts, err := StartTokenServer("http://127.0.0.1", map[string]http.Handler{"/data": ok, "/events/": ok})
	require.NoError(t, err)
	t.Cleanup(ts.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	call := func(path, header string) int {
		req, err := http.NewRequest(http.MethodGet, "http://"+ts.Addr()+path, nil)
		require.NoError(t, err)
		if header != "" {
			req.Header.Set("Authorization", "Bearer "+header)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for _, path := range []string{"/data", "/events/x"} {
		assert.Equal(t, http.StatusUnauthorized, call(path, ""), path)
		assert.Equal(t, http.StatusUnauthorized, call(path, "mgo_"+ts.Token()), path)
		assert.Equal(t, http.StatusUnauthorized, call(path, ts.Token()+"x"), path)
		assert.Equal(t, http.StatusOK, call(path, ts.Token()), path)
		assert.Equal(t, http.StatusOK, call(path+"?token="+ts.Token(), ""), path)
	}
}
