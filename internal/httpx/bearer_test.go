package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

func TestBearerGuard(t *testing.T) {
	t.Parallel()

	const token = "s3cret-token-value"

	// serve drives one request (optional Authorization header and raw query
	// string) through the header-only guard and returns the recorder.
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
		built(BearerGuard(rpcerr.FormatJSON, SingleTokenVerifier(load, types.GrantViewer), anyNeed, okHandler)).ServeHTTP(rr, req)
		return rr
	}

	authorized := func(t *testing.T, authHeader, rawQuery string) {
		t.Helper()
		assert.Equal(t, http.StatusOK, serve(authHeader, rawQuery).Code)
	}

	// missing and refused are the two 401s: no bearer credential at all, or one that
	// verify turned down. Only the second earns error="invalid_token".
	missing := func(t *testing.T, authHeader, rawQuery string) {
		t.Helper()
		rr := serve(authHeader, rawQuery)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, `Bearer realm="magus"`, rr.Header().Get("WWW-Authenticate"))
		assert.Equal(t, "MGS9011", decodeStatus(t, rr.Body.Bytes()).reason())
	}
	refused := func(t *testing.T, authHeader, rawQuery string) {
		t.Helper()
		rr := serve(authHeader, rawQuery)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Equal(t, `Bearer realm="magus", error="invalid_token"`, rr.Header().Get("WWW-Authenticate"))
		assert.Equal(t, "MGS9001", decodeStatus(t, rr.Body.Bytes()).reason())
	}

	// Header path.
	t.Run("valid bearer", func(t *testing.T) { authorized(t, "Bearer "+token, "") })
	t.Run("valid bearer lowercase scheme", func(t *testing.T) { authorized(t, "bearer "+token, "") })
	t.Run("valid bearer mixed-case scheme", func(t *testing.T) { authorized(t, "BeArEr "+token, "") })
	// A query token is NOT a credential carrier for the header-only guard: a valid
	// token in the URL must be rejected (RFC 6750 section 2.3: keep secrets out of URLs).
	t.Run("valid query token rejected (header-only)", func(t *testing.T) { missing(t, "", "token="+token) })
	t.Run("header still wins with a bogus query token", func(t *testing.T) { authorized(t, "Bearer "+token, "token=wrong") })
	// Rejections.
	t.Run("no header no query", func(t *testing.T) { missing(t, "", "") })
	t.Run("wrong token header", func(t *testing.T) { refused(t, "Bearer not-the-token", "") })
	t.Run("token as prefix of real one", func(t *testing.T) { refused(t, "Bearer s3cret", "") })
	t.Run("real token plus suffix", func(t *testing.T) { refused(t, "Bearer "+token+"x", "") })
	t.Run("missing scheme", func(t *testing.T) { missing(t, token, "") })
	t.Run("wrong scheme", func(t *testing.T) { missing(t, "Basic "+token, "") })
	t.Run("empty bearer", func(t *testing.T) { missing(t, "Bearer ", "") })
}

// TestBearerGuardWithQueryToken covers the browser-EventSource variant that also
// accepts the token from a `?token=` query param (header still preferred).
func TestBearerGuardWithQueryToken(t *testing.T) {
	t.Parallel()

	const token = "s3cret-token-value"

	serve := func(authHeader, rawQuery string) *httptest.ResponseRecorder {
		target := "/events"
		if rawQuery != "" {
			target += "?" + rawQuery
		}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rr := httptest.NewRecorder()
		load := func() (string, error) { return token, nil }
		built(BearerGuardWithQueryToken(rpcerr.FormatJSON, SingleTokenVerifier(load, types.GrantViewer), anyNeed, okHandler)).ServeHTTP(rr, req)
		return rr
	}
	code := func(authHeader, rawQuery string) int { return serve(authHeader, rawQuery).Code }

	t.Run("valid query token", func(t *testing.T) { assert.Equal(t, http.StatusOK, code("", "token="+token)) })
	t.Run("valid header token", func(t *testing.T) { assert.Equal(t, http.StatusOK, code("Bearer "+token, "")) })
	t.Run("header wins over query", func(t *testing.T) { assert.Equal(t, http.StatusOK, code("Bearer "+token, "token=wrong")) })
	t.Run("query fallback when header absent", func(t *testing.T) { assert.Equal(t, http.StatusOK, code("", "foo=bar&token="+token)) })
	t.Run("wrong query token", func(t *testing.T) { assert.Equal(t, http.StatusUnauthorized, code("", "token=not-the-token")) })
	t.Run("empty query token", func(t *testing.T) { assert.Equal(t, http.StatusUnauthorized, code("", "token=")) })
	t.Run("no header no query", func(t *testing.T) { assert.Equal(t, http.StatusUnauthorized, code("", "")) })
}

// TestSingleTokenVerifierLoadErrorFailsClosed confirms a token-load error denies
// access even when the client presents a plausible token.
func TestSingleTokenVerifierLoadErrorFailsClosed(t *testing.T) {
	t.Parallel()
	load := func() (string, error) { return "", errors.New("revoked") }
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rr := httptest.NewRecorder()
	built(BearerGuard(rpcerr.FormatJSON, SingleTokenVerifier(load, types.GrantOperator), anyNeed, okHandler)).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// TestBearerGuardVerifierRejectionFailsClosed confirms that a verifier which
// returns false denies access regardless of the presented token.
func TestBearerGuardVerifierRejectionFailsClosed(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rr := httptest.NewRecorder()
	built(BearerGuard(rpcerr.FormatJSON, rejectAll, anyNeed, okHandler)).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// The guard's authorization is the grant against the need, checked exhaustively: every grant
// on the lattice against every need. A credential below the need is a 403 MGS9015 naming the
// need, never a 401 (the token is valid) and never a pass.
func TestBearerGuardAdmitsExactlyTheGrantsThatAllowTheNeed(t *testing.T) {
	t.Parallel()
	scopes := []types.Scope{types.ScopeTokens, types.ScopeMCP, types.ScopeConsole}
	levels := []types.Level{types.LevelNone, types.LevelRead, types.LevelWrite}
	for _, tok := range []types.Level{types.LevelNone, types.LevelWrite} {
		for _, mcp := range []types.Level{types.LevelNone, types.LevelWrite} {
			for _, con := range levels {
				grant := types.Grant{Tokens: tok, MCP: mcp, Console: con}
				verify := func(string) (types.Credential, bool) {
					return types.Credential{Kind: types.KindStored, Grant: grant}, true
				}
				for _, s := range scopes {
					for _, l := range levels[1:] {
						need := types.Need{Scope: s, Level: l}
						if need.Validate() != nil {
							continue // tokens=read and mcp=read are no need; a guard refuses to build on one
						}
						req := httptest.NewRequest(http.MethodPost, "/x", nil)
						req.Header.Set("Authorization", "Bearer anything")
						rr := httptest.NewRecorder()
						built(BearerGuard(rpcerr.FormatJSON, verify, need, okHandler)).ServeHTTP(rr, req)
						want := http.StatusForbidden
						if grant.Level(s) >= l {
							want = http.StatusOK
						}
						if !assert.Equal(t, want, rr.Code, "%s against %s", grant, need) || want == http.StatusOK {
							continue
						}
						st := decodeStatus(t, rr.Body.Bytes())
						assert.Equal(t, "MGS9015", st.reason(), "%s against %s", grant, need)
						assert.Contains(t, st.Error.Message, need.String())
					}
				}
			}
		}
	}
}

// The guard puts the credential it verified and the rpc entry point on the context once, so
// every record made under the request is stamped from there and no handler copies either.
func TestBearerGuardPutsTheVerifiedCredentialOnTheContext(t *testing.T) {
	t.Parallel()
	cred := types.Credential{Kind: types.KindStored, ID: "3fa9c1d2", Name: "console-1", Grant: types.GrantConsole}
	named := func(presented string) (types.Credential, bool) { return cred, presented == "good" }
	var seen types.Origin
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = trail.StampOrigin(r.Context(), types.Origin{})
	})
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.Header.Set("Authorization", "Bearer good")
	built(BearerGuard(rpcerr.FormatJSON, named, anyNeed, next)).ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, cred, seen.Credential)
	assert.Equal(t, types.EntryPointRPC, seen.EntryPoint)
	assert.Zero(t, trail.CredentialFromContext(t.Context()), "no guard, no credential")
}

// A guard built on a zero or invalid need would hold its route to nothing, so every
// constructor refuses one rather than admitting every credential.
func TestGuardsRefuseToBuildOnAZeroOrInvalidNeed(t *testing.T) {
	t.Parallel()
	for _, n := range []types.Need{{}, {Scope: types.ScopeConsole}, {Scope: "files", Level: types.LevelWrite}, {Scope: types.ScopeMCP, Level: types.LevelRead}} {
		_, err := BearerGuard(rpcerr.FormatJSON, rejectAll, n, okHandler)
		assert.Error(t, err, "BearerGuard %+v", n)
		_, err = BearerGuardWithQueryToken(rpcerr.FormatJSON, rejectAll, n, okHandler)
		assert.Error(t, err, "BearerGuardWithQueryToken %+v", n)
		_, err = ProcedureGuard(rpcerr.FormatConnect, rejectAll, map[string]types.Need{"/s/A": anyNeed, "/s/B": n}, okHandler)
		assert.Error(t, err, "ProcedureGuard %+v", n)
	}
	_, err := ProcedureGuard(rpcerr.FormatConnect, rejectAll, nil, okHandler)
	assert.Error(t, err, "an empty table guards nothing")
}

// Each procedure is held to its own need, and a path the table does not name to the
// strictest one in it, so an unlisted procedure is never the weakest door.
func TestProcedureGuardHoldsEachProcedureToItsNeed(t *testing.T) {
	t.Parallel()
	read := types.Need{Scope: types.ScopeConsole, Level: types.LevelRead}
	write := types.Need{Scope: types.ScopeConsole, Level: types.LevelWrite}
	h := built(ProcedureGuard(rpcerr.FormatConnect, func(string) (types.Credential, bool) {
		return types.Credential{Kind: types.KindStored, Grant: types.GrantViewer}, true
	}, map[string]types.Need{"/s.S/List": read, "/s.S/Run": write}, okHandler))
	for path, want := range map[string]int{"/s.S/List": http.StatusOK, "/s.S/Run": http.StatusForbidden, "/s.S/Unlisted": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer viewer")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		assert.Equal(t, want, rr.Code, path)
	}
}

// With mcp.insecure_bind the listener faces the network, and the operator token stays on the
// machine: a non-loopback TCP peer presenting it is refused as if the token were wrong, while
// a stored token from the same peer is admitted. Judged by RemoteAddr, never a header.
func TestOperatorTokenIsRefusedFromANonLoopbackPeer(t *testing.T) {
	t.Parallel()
	verify := func(presented string) (types.Credential, bool) {
		if presented == "op" {
			return types.Credential{Kind: types.KindOperator, Grant: types.GrantOperator}, true
		}
		return types.Credential{Kind: types.KindStored, Grant: types.GrantConsole}, true
	}
	h := built(BearerGuard(rpcerr.FormatJSON, verify, anyNeed, okHandler))
	serve := func(token, peer string, header map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = peer
		req.Header.Set("Authorization", "Bearer "+token)
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	assert.Equal(t, http.StatusOK, serve("op", "127.0.0.1:5000", nil))
	assert.Equal(t, http.StatusOK, serve("op", "[::1]:5000", nil))
	assert.Equal(t, http.StatusUnauthorized, serve("op", "192.168.1.20:5000", nil))
	assert.Equal(t, http.StatusUnauthorized, serve("op", "192.168.1.20:5000", map[string]string{"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1"}))
	assert.Equal(t, http.StatusOK, serve("stored", "192.168.1.20:5000", nil))
}

// A stream outlives the check that admitted it, so the guard checks again while it runs: a
// token revoked mid-stream cancels the request's context. Not parallel: it shortens the
// package interval.
func TestRevokedTokenCutsAnOpenStream(t *testing.T) {
	old := reverifyEvery
	reverifyEvery = 10 * time.Millisecond
	t.Cleanup(func() { reverifyEvery = old })

	var revoked atomic.Bool
	verify := func(string) (types.Credential, bool) {
		return types.Credential{Kind: types.KindStored, Grant: types.GrantConsole}, !revoked.Load()
	}
	started, ended := make(chan struct{}), make(chan struct{})
	stream := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(ended)
	})
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Authorization", "Bearer t")
	go built(BearerGuard(rpcerr.FormatJSON, verify, anyNeed, stream)).ServeHTTP(httptest.NewRecorder(), req)
	<-started
	select {
	case <-ended:
		t.Fatal("the stream ended while its token still verified")
	case <-time.After(50 * time.Millisecond):
	}
	revoked.Store(true)
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("a revoked token's stream kept running")
	}
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

var rejectAll Verifier = func(string) (types.Credential, bool) { return types.Credential{}, false }

// anyNeed is a need every test credential below meets; the refusals here are about the token.
var anyNeed = types.Need{Scope: types.ScopeConsole, Level: types.LevelRead}

// built unwraps a guard constructor for a test. A build error answers every request 500 with
// the error text, so the status assertion that follows names it.
func built(h http.Handler, err error) http.Handler {
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		})
	}
	return h
}

// wireStatus is the AIP-193 HTTP/1.1+JSON body, decoded the way a client would.
type wireStatus struct {
	Error wireError `json:"error"`
}

type wireError struct {
	Code    int          `json:"code"`
	Message string       `json:"message"`
	Status  string       `json:"status"`
	Details []wireDetail `json:"details"`
}

type wireDetail struct {
	Type   string     `json:"@type"`
	Reason string     `json:"reason"`
	Domain string     `json:"domain"`
	Links  []wireLink `json:"links"`
}

type wireLink struct {
	Description string `json:"description"`
	URL         string `json:"url"`
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

func TestBearerRefusalIsAIPStatusJSON(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rr := httptest.NewRecorder()
	built(BearerGuard(rpcerr.FormatJSON, rejectAll, anyNeed, okHandler)).ServeHTTP(rr, req)
	t.Logf("%s", rr.Body.Bytes())

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Equal(t, `Bearer realm="magus", error="invalid_token"`, rr.Header().Get("WWW-Authenticate"))
	got := decodeStatus(t, rr.Body.Bytes())
	assert.Equal(t, wireError{
		Code:    http.StatusUnauthorized,
		Message: types.FormatDiagnostic(types.BearerRejected, bearerRejected.Message),
		Status:  "UNAUTHENTICATED",
		Details: []wireDetail{
			{Type: "type.googleapis.com/google.rpc.ErrorInfo", Reason: "MGS9001", Domain: "github.com/egladman/magus"},
			{
				Type:  "type.googleapis.com/google.rpc.Help",
				Links: []wireLink{{Description: "bearer token rejected", URL: types.CodeURL(types.BearerRejected)}},
			},
		},
	}, got.Error)
}

// The Connect cases decode through connect-go's own client, so they prove what a Connect
// client parses rather than a shape this test assumes.
func TestBearerRefusalSpeaksConnectOnConnectMounts(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(built(BearerGuard(rpcerr.FormatConnect, rejectAll, anyNeed, okHandler)))
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
	built(BearerGuard(rpcerr.FormatJSON, rejectAll, anyNeed, okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, "UNAUTHENTICATED", decodeStatus(t, rr.Body.Bytes()).Error.Status)
}
