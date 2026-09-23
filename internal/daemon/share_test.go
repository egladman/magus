package daemon

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// The share endpoint answers its own failures in the AIP-193 shape its guards already use,
// so the console reads one error shape from the route rather than a bare string.
func TestShareHandlerRefusesInTheAIPShape(t *testing.T) {
	t.Parallel()
	h := (&Daemon{}).newShareHandler(nil, "", nil, slog.New(slog.DiscardHandler))
	cases := []struct {
		name, method string
		status       int
		reason       string
		allow        string
	}{
		{"a GET", http.MethodGet, http.StatusMethodNotAllowed, "MGS9012", http.MethodPost},
		{"no built console", http.MethodPost, http.StatusBadRequest, "MGS9013", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(tc.method, "/api/v1/share", nil))

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
