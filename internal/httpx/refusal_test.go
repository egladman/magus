package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

var rejectAll = func(string) bool { return false }

// wireStatus is the AIP-193 HTTP/1.1+JSON body, decoded the way a client would.
type wireStatus struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type   string `json:"@type"`
			Reason string `json:"reason"`
			Domain string `json:"domain"`
			Links  []struct {
				Description string `json:"description"`
				URL         string `json:"url"`
			} `json:"links"`
		} `json:"details"`
	} `json:"error"`
}

func decodeStatus(t *testing.T, body []byte) wireStatus {
	t.Helper()
	var s wireStatus
	require.NoError(t, json.Unmarshal(body, &s), "body %s", body)
	return s
}

func (s wireStatus) reason() string {
	for _, d := range s.Error.Details {
		if d.Type == "type.googleapis.com/google.rpc.ErrorInfo" {
			return d.Reason
		}
	}
	return ""
}

func TestRefuseWritesAIPStatusJSON(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rr := httptest.NewRecorder()
	BearerGuard(FormatJSON, rejectAll, okHandler).ServeHTTP(rr, req)
	t.Logf("%s", rr.Body.Bytes())

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Equal(t, `Bearer realm="magus", error="invalid_token"`, rr.Header().Get("WWW-Authenticate"))
	got := decodeStatus(t, rr.Body.Bytes())
	assert.Equal(t, http.StatusUnauthorized, got.Error.Code)
	assert.Equal(t, "UNAUTHENTICATED", got.Error.Status)
	assert.Equal(t, types.FormatDiagnostic(types.BearerRejected, bearerRejected.Message), got.Error.Message)
	require.Len(t, got.Error.Details, 2)
	info, help := got.Error.Details[0], got.Error.Details[1]
	assert.Equal(t, "type.googleapis.com/google.rpc.ErrorInfo", info.Type)
	assert.Equal(t, "MGS9001", info.Reason)
	assert.Equal(t, "github.com/egladman/magus", info.Domain)
	assert.Equal(t, "type.googleapis.com/google.rpc.Help", help.Type)
	require.Len(t, help.Links, 1)
	assert.Equal(t, "bearer token rejected", help.Links[0].Description)
	assert.Equal(t, types.CodeURL(types.BearerRejected), help.Links[0].URL)
}

func TestRefuseOmitsTheBearerChallengeOn403(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Host = "evil.example"
	rr := httptest.NewRecorder()
	GuardRebind(FormatJSON, AllowedHosts(netip.MustParseAddrPort("127.0.0.1:7391")), okHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Empty(t, rr.Header().Get("WWW-Authenticate"))
	got := decodeStatus(t, rr.Body.Bytes())
	assert.Equal(t, "PERMISSION_DENIED", got.Error.Status)
	assert.Equal(t, "MGS9007", got.reason())
}

// The Connect cases decode through connect-go's own client, so they prove what a Connect
// client parses rather than a shape this test assumes.
func TestRefuseSpeaksConnectOnConnectMounts(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(BearerGuard(FormatConnect, rejectAll, okHandler))
	t.Cleanup(srv.Close)
	url := srv.URL + "/magus.probe.v1alpha1.ProbeService/Call"

	assertTyped := func(t *testing.T, err error) {
		t.Helper()
		var cerr *connect.Error
		require.ErrorAs(t, err, &cerr)
		assert.Equal(t, connect.CodeUnauthenticated, cerr.Code())
		assert.Equal(t, types.FormatDiagnostic(types.BearerMissing, bearerMissing.Message), cerr.Message())
		var infos []*errdetails.ErrorInfo
		var helps []*errdetails.Help
		for _, d := range cerr.Details() {
			v, verr := d.Value()
			require.NoError(t, verr)
			switch m := v.(type) {
			case *errdetails.ErrorInfo:
				infos = append(infos, m)
			case *errdetails.Help:
				helps = append(helps, m)
			}
		}
		require.Len(t, infos, 1)
		assert.Equal(t, "MGS9011", infos[0].GetReason())
		assert.Equal(t, "github.com/egladman/magus", infos[0].GetDomain())
		require.Len(t, helps, 1)
		require.Len(t, helps[0].GetLinks(), 1)
		assert.Equal(t, types.CodeURL(types.BearerMissing), helps[0].GetLinks()[0].GetUrl())
	}

	t.Run("unary json", func(t *testing.T) {
		t.Parallel()
		client := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), url, connect.WithProtoJSON())
		_, err := client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{}))
		assertTyped(t, err)
		var cerr *connect.Error
		require.ErrorAs(t, err, &cerr)
		assert.Equal(t, `Bearer realm="magus"`, cerr.Meta().Get("WWW-Authenticate"))
	})
	t.Run("server stream", func(t *testing.T) {
		t.Parallel()
		client := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), url)
		stream, err := client.CallServerStream(context.Background(), connect.NewRequest(&emptypb.Empty{}))
		require.NoError(t, err)
		assert.False(t, stream.Receive())
		assertTyped(t, stream.Err())
		require.NoError(t, stream.Close())
	})
}

// A plain-HTTP mount must not speak Connect even to a request connect-go would classify as
// Connect unary (any application/* POST, any GET).
func TestJSONMountIgnoresConnectLookingRequests(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	BearerGuard(FormatJSON, rejectAll, okHandler).ServeHTTP(rr, req)

	assert.Equal(t, "UNAUTHENTICATED", decodeStatus(t, rr.Body.Bytes()).Error.Status)
}

func TestHTTPStatusMatchesConnect(t *testing.T) {
	t.Parallel()
	for c := connect.CodeCanceled; c <= connect.CodeUnauthenticated; c++ {
		rr := httptest.NewRecorder()
		_ = connect.NewErrorWriter().Write(rr, httptest.NewRequest(http.MethodPost, "/", nil), connect.NewError(c, errors.New("probe")))
		assert.Equal(t, rr.Code, httpStatus(c), c.String())
	}
}
