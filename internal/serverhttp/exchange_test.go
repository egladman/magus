package serverhttp

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// A console link's code is traded once for its token, and every other presentation of it is
// refused as a wrong bearer would be: used, unknown, malformed, or sent by GET.
func TestExchangeTradesACodeOnceForItsToken(t *testing.T) {
	testkit.Isolate(t)
	trailDir := t.TempDir()
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	store, err := auth.LoadStore(dir)
	require.NoError(t, err)
	code, _, err := store.MintCode(types.GrantOperator, auth.MintRequest{Grant: types.GrantConsole, TTL: 12 * time.Hour})
	require.NoError(t, err)
	h := newExchangeHandler(trailDir, slog.New(slog.DiscardHandler))

	post := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/v1/token/exchange", strings.NewReader(body)))
		return rr
	}
	rr := post(`{"code":"` + code + `"}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	var got exchangeResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	cred, ok := auth.Verify(got.Token)
	require.True(t, ok)
	assert.Equal(t, types.GrantConsole, cred.Grant)
	expires, err := time.Parse(time.RFC3339, got.ExpiresAt)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(12*time.Hour), expires, time.Minute)

	for name, body := range map[string]string{
		"used":      `{"code":"` + code + `"}`,
		"unknown":   `{"code":"mgx_nope"}`,
		"a token":   `{"code":"` + got.Token + `"}`,
		"malformed": `code=` + code,
		"empty":     `{}`,
	} {
		rr := post(body)
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusBadRequest}, rr.Code, name)
		assert.NotContains(t, rr.Body.String(), "mgs_", name)
	}
	assert.Equal(t, http.StatusUnauthorized, post(`{"code":"`+code+`"}`).Code, "a spent code is a wrong bearer")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/token/exchange?code="+code, nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)

	// The redeem is audited, naming the code it spent and the token it minted, never either
	// secret.
	events, err := trail.ReadRecent(trailDir, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "link.redeem", events[0].Action)
	blob, err := trail.ReadBlob(trailDir, events[0].RequestRef)
	require.NoError(t, err)
	for _, s := range []string{code, got.Token} {
		assert.NotContains(t, string(blob), s)
		assert.NotContains(t, events[0].Preview, s)
	}
	var rec trail.MintRecord
	require.NoError(t, json.Unmarshal(blob, &rec))
	assert.Equal(t, types.ClassStored, rec.Minted.Class)
	assert.Equal(t, cred.ID, rec.Minted.ID)
	assert.Equal(t, types.ClassExchange, rec.Minter.Class)
	assert.Equal(t, auth.TokenID(code), rec.Minter.ID)
}
