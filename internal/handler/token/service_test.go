package token

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/share"
	tokenv1 "github.com/egladman/magus/proto/gen/go/magus/token/v1"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1/tokenv1connect"
)

// fakeShare is a stand-in for *share.Manager: it reports a fixed active share (or
// none) and records whether CloseIf fired for its fingerprint, so a revoke test can
// assert the share's teardown ran without opening a real LAN listener. closeIfArg
// captures the fingerprint CloseIf was called with so a test can assert the handler
// passed the identity it matched, not the raw request identifier.
type fakeShare struct {
	info        share.TokenInfo
	active      bool
	closed      bool
	closeIfArg  string
	closeIfFail bool // when true, CloseIf reports a lost race (superseded) and closes nothing
}

func (f *fakeShare) Active() (share.TokenInfo, bool) { return f.info, f.active }

func (f *fakeShare) CloseIf(fingerprint string) bool {
	f.closeIfArg = fingerprint
	if f.closeIfFail || !f.active || f.info.Fingerprint != fingerprint {
		return false
	}
	f.closed = true
	f.active = false
	return true
}

// newIsolatedService points the connector store at a temp state dir (via
// XDG_STATE_HOME, the same knob the store's own path resolution reads) so a test
// never touches the real user directory, and wires in view for the share side.
func newIsolatedService(t *testing.T, view shareView) *Service {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return newService(view)
}

func req[T any](msg *T) *connect.Request[T] { return connect.NewRequest(msg) }

// TestListNeverContainsSecret seeds two connector tokens in the store and lists them
// alongside an active share token, then asserts no field of any TokenInfo carries a
// raw secret - only the prefix-only fingerprints - and that the secrets the store
// minted do not appear anywhere in the list response.
func TestListNeverContainsSecret(t *testing.T) {
	sh := &fakeShare{
		active: true,
		info: share.TokenInfo{
			Fingerprint: "abcd1234",
			Scope:       auth.ShareScopeRead,
			Created:     time.Now(),
			Expires:     time.Now().Add(15 * time.Minute),
		},
	}
	s := newIsolatedService(t, sh)

	// Minting is CLI-only, so the handler cannot create connectors; seed the shared
	// store directly (the same store the handler reads) to stand up the list fixture.
	store, err := auth.LoadConnectorStore()
	require.NoError(t, err)
	secret1, _, err := store.Create("alpha", time.Now().Add(time.Hour))
	require.NoError(t, err)
	secret2, _, err := store.Create("beta", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(secret1, "mgs_"))
	require.True(t, strings.HasPrefix(secret2, "mgs_"))

	list, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	got := list.Msg.GetTokens()
	require.Len(t, got, 3, "two connectors plus the active share token")

	for _, info := range got {
		assert.NotContains(t, info.GetName(), "mgs_", "name must not carry a secret")
		assert.NotContains(t, info.GetIdentifier(), "mgs_", "identifier is a fingerprint, not a secret")
		assert.NotEqual(t, secret1, info.GetIdentifier())
		assert.NotEqual(t, secret2, info.GetIdentifier())
		// The fingerprint is a strict prefix of the SHA-256 hex, far shorter than the
		// ~50-char mgs_ secret; a full secret leaking here would blow past this bound.
		assert.LessOrEqual(t, len(info.GetIdentifier()), 8)
	}

	// One token carries the share scope; it is the read share, not a connector.
	var scopes []tokenv1.TokenScope
	for _, info := range got {
		scopes = append(scopes, info.GetScope())
	}
	assert.Contains(t, scopes, tokenv1.TokenScope_TOKEN_SCOPE_SHARE_READ)
	assert.Contains(t, scopes, tokenv1.TokenScope_TOKEN_SCOPE_CONNECTOR)
}

// TestRevokeShareTokenClosesListener revokes by the share token's fingerprint and
// asserts the share manager's teardown (Close) fired - closing the LAN listener -
// and that the response describes the share token.
func TestRevokeShareTokenClosesListener(t *testing.T) {
	sh := &fakeShare{
		active: true,
		info: share.TokenInfo{
			Fingerprint: "feedface",
			Scope:       auth.ShareScopeRead,
			Created:     time.Now(),
			Expires:     time.Now().Add(15 * time.Minute),
		},
	}
	s := newIsolatedService(t, sh)

	resp, err := s.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Identifier: "feedface"}))
	require.NoError(t, err)
	assert.True(t, sh.closed, "revoking the share token must close its listener via CloseIf")
	assert.Equal(t, "feedface", sh.closeIfArg, "the handler must close by the matched fingerprint, not the raw identifier")
	assert.Equal(t, tokenv1.TokenScope_TOKEN_SCOPE_SHARE_READ, resp.Msg.GetToken().GetScope())
	assert.Equal(t, "feedface", resp.Msg.GetToken().GetIdentifier())
}

// TestRevokeShareLostRaceIsNotFound proves the TOCTOU guard: when the share matched by
// Active is superseded before CloseIf runs (CloseIf reports false), the revoke must return
// NotFound rather than fall through to the connector store with a share fingerprint and
// rather than claim success.
func TestRevokeShareLostRaceIsNotFound(t *testing.T) {
	sh := &fakeShare{
		active:      true,
		closeIfFail: true, // simulate a supersede between Active and CloseIf
		info: share.TokenInfo{
			Fingerprint: "feedface",
			Scope:       auth.ShareScopeRead,
			Created:     time.Now(),
			Expires:     time.Now().Add(15 * time.Minute),
		},
	}
	s := newIsolatedService(t, sh)

	_, err := s.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Identifier: "feedface"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.False(t, sh.closed, "a lost race must tear down nothing")
}

// TestRevokeSharePrefixResolvesToConnector proves shareMatches is exact-only: an
// identifier that is a strict PREFIX of the share fingerprint (and also prefixes a real
// connector fingerprint) must NOT be intercepted as the share. It falls through to the
// connector store, which owns prefix resolution. The share stays live.
func TestRevokeSharePrefixResolvesToConnector(t *testing.T) {
	// Mint a connector first so its fingerprint is concrete, then point the fake share at a
	// fingerprint sharing a leading character with it. A one-character identifier that
	// prefixes both must resolve to the connector, never the share.
	s := newIsolatedService(t, nil)
	store, err := auth.LoadConnectorStore()
	require.NoError(t, err)
	_, conn, err := store.Create("c1", time.Now().Add(time.Hour))
	require.NoError(t, err)

	prefix := conn.Fingerprint[:1]
	sh := &fakeShare{
		active: true,
		info: share.TokenInfo{
			// A fingerprint that also starts with prefix, so a prefix-match WOULD have hit
			// the share under the old behavior.
			Fingerprint: prefix + "0000000",
			Scope:       auth.ShareScopeRead,
			Created:     time.Now(),
			Expires:     time.Now().Add(15 * time.Minute),
		},
	}
	s.share = sh

	resp, err := s.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Identifier: prefix}))
	require.NoError(t, err)
	assert.False(t, sh.closed, "a prefix must never silently revoke the share")
	assert.Equal(t, tokenv1.TokenScope_TOKEN_SCOPE_CONNECTOR, resp.Msg.GetToken().GetScope(),
		"a prefix that also names the share must resolve to the connector store")
	assert.Equal(t, conn.Fingerprint, resp.Msg.GetToken().GetIdentifier())
}

// TestNilShareManagerConstructor proves NewService given a typed-nil *share.Manager
// treats the share feature as OFF: List works (no share token) and no nil-deref occurs.
// This is the typed-nil trap the concrete-typed constructor closes.
func TestNilShareManagerConstructor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := NewService((*share.Manager)(nil))
	assert.Nil(t, s.share, "a nil *share.Manager must become a true-nil share view")

	list, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	assert.Empty(t, list.Msg.GetTokens(), "no connectors and no share token")

	_, err = s.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Identifier: "share to phone"}))
	require.Error(t, err, "with no share manager, the share label matches nothing")
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// TestOperatorTokenInvisibleAndImmutable proves the OPERATOR-class boundary (the
// built-in cli token) by construction: with a real cli token on disk, ListTokens
// never enumerates it and a deliberate RevokeToken keyed on its fingerprint fails
// (NotFound) and leaves the token file byte-for-byte intact. The boundary is not a
// convention this handler chooses to honor - the cli token lives in a store this
// service never opens (auth.Load, not the connector store), so there is no code path
// by which the browser-facing surface could enumerate or delete it.
func TestOperatorTokenInvisibleAndImmutable(t *testing.T) {
	s := newIsolatedService(t, nil)

	// Seed a connector too, so the list is non-empty: the operator token must be absent
	// even when the service does return other tokens (it is not merely "empty list").
	store, err := auth.LoadConnectorStore()
	require.NoError(t, err)
	_, conn, err := store.Create("mcp-client", time.Now().Add(time.Hour))
	require.NoError(t, err)

	cliTok, err := auth.Generate()
	require.NoError(t, err)
	_, err = auth.Save(cliTok)
	require.NoError(t, err)
	cliFingerprint := auth.Fingerprint(cliTok)

	list, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetTokens(), 1, "only the connector is listed; the operator token is not")
	for _, info := range list.Msg.GetTokens() {
		assert.NotEqual(t, cliFingerprint, info.GetIdentifier(), "operator token must never appear in the list")
		assert.NotEqual(t, tokenv1.TokenScope_TOKEN_SCOPE_OPERATOR, info.GetScope(), "no listed token may carry the operator class")
		assert.Equal(t, conn.Fingerprint, info.GetIdentifier())
	}

	// A deliberate attempt to revoke the operator token by its fingerprint must fail,
	// not silently pass: it falls through to the connector store, which does not hold it.
	_, err = s.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Identifier: cliFingerprint}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// The cli token file is still there, unchanged: the surface never touched it.
	loaded, err := auth.Load()
	require.NoError(t, err)
	assert.Equal(t, cliTok, loaded)
}

// TestListResponseCarriesNoSecretBytes is the hostile invariant behind "List returns
// no secrets": it stands up a full ListTokensResponse holding a REAL connector token
// (so the raw secret and the full hash are concrete, not synthesized) plus the active
// share token, serializes the response every way a browser could observe it (proto
// wire bytes AND protojson), and FAILS if any serialization contains the raw secret,
// the mgs_ secret prefix, or the full-length hash. The list must carry only the short
// revoke-handle fingerprint - never the secret or the full hash.
func TestListResponseCarriesNoSecretBytes(t *testing.T) {
	// A share whose 8-char fingerprint is the prefix of a known 64-char hash, so the
	// test can assert the REST of that hash never rides the list.
	shareFullHash := "deadbeef" + strings.Repeat("0", 56)
	sh := &fakeShare{
		active: true,
		info: share.TokenInfo{
			Fingerprint: shareFullHash[:8],
			Scope:       auth.ShareScopeRead,
			Created:     time.Now(),
			Expires:     time.Now().Add(15 * time.Minute),
		},
	}
	s := newIsolatedService(t, sh)

	store, err := auth.LoadConnectorStore()
	require.NoError(t, err)
	secret, conn, err := store.Create("client", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(secret, "mgs_"))
	require.Len(t, conn.SHA256, 64, "the full hash we hunt for is the 64-char hex digest")

	resp, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetTokens(), 2, "the connector and the active share")

	protoBytes, err := proto.Marshal(resp.Msg)
	require.NoError(t, err)
	jsonBytes, err := protojson.Marshal(resp.Msg)
	require.NoError(t, err)

	// Scan every serialization: the connector's real secret and full hash, the mgs_
	// prefix, and the share's full hash must appear in NONE of them.
	for name, blob := range map[string]string{"proto": string(protoBytes), "json": string(jsonBytes)} {
		assert.NotContainsf(t, blob, secret, "%s serialization leaked the raw secret", name)
		assert.NotContainsf(t, blob, "mgs_", "%s serialization leaked the mgs_ secret prefix", name)
		assert.NotContainsf(t, blob, conn.SHA256, "%s serialization leaked the connector full hash", name)
		assert.NotContainsf(t, blob, shareFullHash, "%s serialization leaked the share full hash", name)
	}

	// The revoke handle really is only the short fingerprint (8 hex), not the full hash.
	for _, info := range resp.Msg.GetTokens() {
		assert.LessOrEqual(t, len(info.GetIdentifier()), 8, "identifier is a short revoke handle, not a full hash")
	}
}

// TestUnauthenticatedCallRejected mounts the real Connect handler behind the same
// bearer guard the daemon uses and asserts a call with no Authorization header is
// rejected with 401 before reaching the service, while a valid bearer passes.
func TestUnauthenticatedCallRejected(t *testing.T) {
	s := newIsolatedService(t, nil)
	path, h := tokenv1connect.NewTokenServiceHandler(s)
	// A fixed verifier standing in for auth.VerifyBearer: accept exactly "good".
	guarded := httpx.BearerGuard(func(presented string) bool { return presented == "good" }, h)
	srv := httptest.NewServer(guarded)
	defer srv.Close()

	// No Authorization header: rejected at the guard, never reaching ListTokens.
	unauth := tokenv1connect.NewTokenServiceClient(http.DefaultClient, srv.URL)
	_, err := unauth.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// Valid bearer: the call passes the guard and the service answers.
	authed := tokenv1connect.NewTokenServiceClient(http.DefaultClient, srv.URL,
		connect.WithInterceptors(bearer("good")))
	resp, err := authed.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	assert.NotNil(t, resp.Msg)

	_ = path // the mount path is asserted indirectly via the client round-trip
}

// TestTierHierarchyAtGuard proves the three-tier policy the daemon mounts this
// service behind: the guard is BearerGuard(auth.VerifyCLIBearer, ...) - exactly
// the daemon's wiring - with a REAL cli token and a REAL non-expired connector
// token on disk. The connector token, though valid on every data surface
// (auth.VerifyBearer accepts it), must be rejected on BOTH TokenService RPCs
// (List, Revoke): a client credential must never list or revoke credentials. The
// cli token must pass. (The share token needs no test here: it is only ever
// verified by the LAN listener's per-session closure, and this service is never
// mounted there - asserted structurally by the daemon's shareGuarded map not
// containing it.)
func TestTierHierarchyAtGuard(t *testing.T) {
	s := newIsolatedService(t, nil)

	// Operator tier: the retrievable cli token.
	cliTok, err := auth.Generate()
	require.NoError(t, err)
	_, err = auth.Save(cliTok)
	require.NoError(t, err)

	// Client tier: a real, non-expired connector token minted through the store.
	store, err := auth.LoadConnectorStore()
	require.NoError(t, err)
	connSecret, _, err := store.Create("mcp-client", time.Now().Add(time.Hour))
	require.NoError(t, err)
	// Sanity: the connector token IS a valid data-surface credential...
	require.True(t, auth.VerifyBearer(connSecret))
	// ...but never an operator credential.
	require.False(t, auth.VerifyCLIBearer(connSecret))

	_, h := tokenv1connect.NewTokenServiceHandler(s)
	srv := httptest.NewServer(httpx.BearerGuard(auth.VerifyCLIBearer, h))
	defer srv.Close()

	asConnector := tokenv1connect.NewTokenServiceClient(http.DefaultClient, srv.URL,
		connect.WithInterceptors(bearer(connSecret)))
	asCLI := tokenv1connect.NewTokenServiceClient(http.DefaultClient, srv.URL,
		connect.WithInterceptors(bearer(cliTok)))

	// Connector token: rejected on both RPCs, before the handler runs.
	_, err = asConnector.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "connector must not list tokens")

	_, err = asConnector.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Identifier: "mcp-client"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "connector must not revoke tokens")

	// The rejected revoke really was a no-op: the store still holds exactly the one
	// connector token.
	fresh, err := auth.LoadConnectorStore()
	require.NoError(t, err)
	require.Len(t, fresh.List(), 1)

	// cli token: full access to both RPCs. Revoking the seeded connector empties the
	// store, confirming the operator credential reaches the handler.
	list, err := asCLI.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	assert.Len(t, list.Msg.GetTokens(), 1)

	_, err = asCLI.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Identifier: "mcp-client"}))
	require.NoError(t, err)
}

// bearer is a tiny client interceptor that sets a fixed Authorization header, so
// the guarded round-trip test can present a valid token.
func bearer(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, ar connect.AnyRequest) (connect.AnyResponse, error) {
			ar.Header().Set("Authorization", "Bearer "+token)
			return next(ctx, ar)
		}
	}
}
