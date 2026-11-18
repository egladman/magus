package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// withSecrets gives the trail writers the resolver they refuse to run without.
func withSecrets(ctx context.Context) context.Context {
	return secrets.WithResolver(ctx, secrets.NewResolver())
}

// loggedCalls is the newest calls in the log under base.
func loggedCalls(t *testing.T, base string) []journal.Call {
	t.Helper()
	var out []journal.Call
	for c, err := range recorder.ReadCalls(t.Context(), base, 10) {
		require.NoError(t, err)
		out = append(out, c)
	}
	return out
}

// A console link's code is traded once for its token, and every other presentation of it is
// refused as a wrong bearer would be: used, unknown, malformed, or sent by GET.
func TestExchangeTradesACodeOnceForItsToken(t *testing.T) {
	testkit.Isolate(t)
	trailDir := t.TempDir()
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	store, err := auth.LoadStore(dir)
	require.NoError(t, err)
	minted, _, err := store.MintCode(auth.AccessOperator(), auth.MintRequest{Grant: auth.AccessConsole(), TTL: 12 * time.Hour})
	require.NoError(t, err)
	code := minted.Reveal()
	h := newExchangeHandler(trailDir, slog.New(slog.DiscardHandler))

	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/token/exchange", strings.NewReader(body))
		h.ServeHTTP(rr, req.WithContext(withSecrets(req.Context())))
		return rr
	}
	rr := post(fmt.Sprintf(`{"code":"%s"}`, code))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	var got struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	require.NoError(t, codec.Unmarshal(rr.Body.Bytes(), &got))
	cred, ok := auth.Verify(got.Token)
	require.True(t, ok)
	assert.Equal(t, auth.AccessConsole(), cred.Grant)
	expires, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(12*time.Hour), expires, time.Minute)

	for name, body := range map[string]string{
		"used":      fmt.Sprintf(`{"code":"%s"}`, code),
		"unknown":   `{"code":"mgx_nope"}`,
		"a token":   fmt.Sprintf(`{"code":"%s"}`, got.Token),
		"malformed": `code=` + code,
		"empty":     `{}`,
	} {
		rr := post(body)
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusBadRequest}, rr.Code, name)
		assert.NotContains(t, rr.Body.String(), "mgs_", name)
	}
	assert.Equal(t, http.StatusUnauthorized, post(fmt.Sprintf(`{"code":"%s"}`, code)).Code, "a spent code is a wrong bearer")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/token/exchange?code="+code, nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)

	// The redeem is audited, naming the code it spent and the token it minted, never either
	// secret.
	events := loggedCalls(t, trailDir)
	require.Len(t, events, 1)
	assert.Equal(t, "link.redeem", events[0].Name)
	blob, err := recorder.ReadCallPayload(trailDir, events[0].Request)
	require.NoError(t, err)
	for _, s := range []string{code, got.Token} {
		assert.NotContains(t, string(blob), s)
		assert.NotContains(t, events[0].Preview, s)
	}
	var rec recorder.MintRecord
	require.NoError(t, codec.Unmarshal(blob, &rec))
	assert.Equal(t, recorder.MintRecord{
		Minted:  auth.Credential{Kind: auth.KindStored, ID: cred.ID, Name: cred.Name, Grant: cred.Grant},
		Expires: rec.Expires, // stamped at mint time
		Minter:  auth.Credential{Kind: auth.KindExchange, ID: auth.TokenID(code), Name: cred.Name, Grant: cred.Grant},
	}, rec)
}
