package serverhttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/share"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// The share endpoint answers its own failures in the AIP-193 shape its guards already use,
// so the console reads one error shape from the route rather than a bare string. A body that
// does not parse has its own code, apart from a lifetime out of range.
func TestShareHandlerRefusesInTheAIPShape(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	cases := []struct {
		name, method, consoleDir, body string
		status                         int
		reason                         string
		allow                          string
	}{
		{"a GET", http.MethodGet, "", "", http.StatusMethodNotAllowed, "MGS9012", http.MethodPost},
		{"no built console", http.MethodPost, "", "", http.StatusBadRequest, "MGS9013", ""},
		{"a malformed body", http.MethodPost, "built", "not json", http.StatusBadRequest, "MGS9020", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := (&Server{}).newShareHandler(nil, tc.consoleDir, nil, "", log)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(tc.method, "/api/v1/share", strings.NewReader(tc.body)))

			assert.Equal(t, tc.status, rr.Code)
			assert.Equal(t, tc.allow, rr.Header().Get("Allow"))
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			var body struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Details []struct {
						Type   string `json:"@type"`
						Reason string `json:"reason"`
					} `json:"details"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body), rr.Body.String())
			assert.Equal(t, tc.status, body.Error.Code)
			assert.Contains(t, body.Error.Message, tc.reason)
			require.NotEmpty(t, body.Error.Details)
			assert.Equal(t, "type.googleapis.com/google.rpc.ErrorInfo", body.Error.Details[0].Type)
			assert.Equal(t, tc.reason, body.Error.Details[0].Reason)
		})
	}
}

// A share mint is audited like every other: the trail names the link's class, id and grant
// and the credential that opened it, and never the secret the link carries.
func TestShareMintIsAudited(t *testing.T) {
	trailDir, consoleDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(consoleDir, "index.html"), []byte("<html></html>"), 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := share.NewManager(ctx, slog.New(slog.DiscardHandler), share.WithListenAddr(netip.MustParseAddr("127.0.0.1")))
	defer mgr.Close()
	routes := map[string]share.Route{"/api/v1/events": {Handler: http.NotFoundHandler(), Format: rpcerr.FormatJSON, Needs: apiNeedsFor("/api/v1/events")}}
	h := (&Server{}).newShareHandler(mgr, consoleDir, routes, trailDir, slog.New(slog.DiscardHandler))

	minter := types.Credential{Class: types.ClassStored, ID: "3fa9c1d2", Name: "laptop", Grant: types.GrantConsole}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/share", nil)
	req = req.WithContext(trail.ContextWithCredential(req.Context(), minter))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp shareResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	_, secret, ok := strings.Cut(resp.URL, "#token=")
	require.True(t, ok)

	events, err := trail.ReadRecent(trailDir, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	e := events[0]
	assert.Equal(t, "share.mint", e.Action)
	assert.Equal(t, trail.KindTokenLifecycle, e.Kind)
	assert.Contains(t, e.Preview, "share link ")
	assert.Contains(t, e.Preview, "console=read")
	assert.NotContains(t, e.Preview, secret)
	blob, err := trail.ReadBlob(trailDir, e.RequestRef)
	require.NoError(t, err)
	assert.NotContains(t, string(blob), secret)
	var rec trail.MintRecord
	require.NoError(t, json.Unmarshal(blob, &rec))
	assert.Equal(t, types.ClassShare, rec.Minted.Class)
	assert.Equal(t, types.GrantViewer, rec.Minted.Grant)
	assert.Len(t, rec.Minted.ID, 8)
	assert.Equal(t, minter, rec.Minter)
}
