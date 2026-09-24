package token

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/handler/trailrpc"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/share"
	"github.com/egladman/magus/internal/trail"
	tokenv1 "github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1/tokenv1alpha1connect"
	"github.com/egladman/magus/types"
)

// fakeShare stands in for *share.Manager: a fixed active share (or none), recording whether
// CloseIf fired and with what id.
type fakeShare struct {
	info        share.TokenInfo
	active      bool
	closed      bool
	closeIfArg  string
	closeIfFail bool // CloseIf reports a lost race and closes nothing
}

func (f *fakeShare) Active() (share.TokenInfo, bool) { return f.info, f.active }

func (f *fakeShare) CloseIf(id string) bool {
	f.closeIfArg = id
	if f.closeIfFail || !f.active || f.info.ID != id {
		return false
	}
	f.closed, f.active = true, false
	return true
}

func liveShare(id string) *fakeShare {
	return &fakeShare{active: true, info: share.TokenInfo{ID: id, Created: time.Now(), Expires: time.Now().Add(15 * time.Minute)}}
}

func newIsolatedService(t *testing.T, view shareView) *Service {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return &Service{share: view}
}

func req[T any](msg *T) *connect.Request[T] { return connect.NewRequest(msg) }

// as is a context carrying the credential the bearer guard would have verified.
func as(grant types.Grant) context.Context {
	return trail.ContextWithCredential(context.Background(), types.Credential{Class: types.ClassStored, ID: "0badf00d", Grant: grant})
}

// operator is the context the server's guard gives the operator token.
func operator() context.Context { return as(types.GrantOperator) }

func store(t *testing.T) *auth.Store {
	t.Helper()
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	s, err := auth.LoadStore(dir)
	require.NoError(t, err)
	return s
}

func stored(t *testing.T) []auth.Token {
	t.Helper()
	toks, err := store(t).List()
	require.NoError(t, err)
	return toks
}

func mint(t *testing.T, name string, grant types.Grant) (string, auth.Token) {
	t.Helper()
	secret, rec, err := store(t).Mint(types.GrantOperator, auth.MintRequest{Name: name, Grant: grant, TTL: time.Hour})
	require.NoError(t, err)
	return secret, rec
}

func wire(levels ...tokenv1.Level) *tokenv1.Grant {
	return &tokenv1.Grant{Tokens: levels[0], Mcp: levels[1], Console: levels[2]}
}

var (
	none  = tokenv1.Level_LEVEL_UNSPECIFIED
	read  = tokenv1.Level_LEVEL_READ
	write = tokenv1.Level_LEVEL_WRITE
)

// A listing carries each token's class and grant, the share link's included, so the console
// shows one grant model and nothing derived from it.
func TestListCarriesClassAndGrant(t *testing.T) {
	s := newIsolatedService(t, liveShare("abcd1234"))
	mint(t, "agent", types.GrantConnector)
	mint(t, "laptop", types.GrantConsole)
	mint(t, "tv", types.GrantViewer)

	list, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	type row struct {
		class tokenv1.CredentialClass
		grant string
	}
	got := map[string]row{}
	for _, info := range list.Msg.GetTokens() {
		g, err := fromWireGrant(info.GetGrant())
		require.NoError(t, err)
		got[info.GetName()] = row{info.GetClass(), g.String()}
	}
	assert.Equal(t, map[string]row{
		"agent":          {tokenv1.CredentialClass_CREDENTIAL_CLASS_STORED, "mcp=write"},
		"laptop":         {tokenv1.CredentialClass_CREDENTIAL_CLASS_STORED, "console=write"},
		"tv":             {tokenv1.CredentialClass_CREDENTIAL_CLASS_STORED, "console=read"},
		"share to phone": {tokenv1.CredentialClass_CREDENTIAL_CLASS_SHARE, "console=read"},
	}, got)
}

// Serialized every way a browser could observe it, a List response carries no secret, no
// secret prefix and no full hash: only the 8-hex id.
func TestListResponseCarriesNoSecretBytes(t *testing.T) {
	shareFullHash := "deadbeef" + strings.Repeat("0", 56)
	s := newIsolatedService(t, liveShare(shareFullHash[:8]))
	secret, rec := mint(t, "client", types.GrantConnector)
	require.Len(t, rec.SHA256, 64)

	resp, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetTokens(), 2)
	protoBytes, err := proto.Marshal(resp.Msg)
	require.NoError(t, err)
	jsonBytes, err := protojson.Marshal(resp.Msg)
	require.NoError(t, err)
	for name, blob := range map[string]string{"proto": string(protoBytes), "json": string(jsonBytes)} {
		assert.NotContainsf(t, blob, secret, "%s leaked the secret", name)
		for _, prefix := range []string{"mgo_", "mgs_", "mgl_", "mgx_"} {
			assert.NotContainsf(t, blob, prefix, "%s leaked a secret prefix", name)
		}
		assert.NotContainsf(t, blob, rec.SHA256, "%s leaked the full hash", name)
		assert.NotContainsf(t, blob, shareFullHash, "%s leaked the share's full hash", name)
	}
	for _, info := range resp.Msg.GetTokens() {
		assert.Len(t, info.GetId(), 8)
	}
}

func TestRevokeShareTokenClosesListener(t *testing.T) {
	sh := liveShare("feedface")
	s := newIsolatedService(t, sh)
	resp, err := s.RevokeToken(as(types.GrantConsole), req(&tokenv1.RevokeTokenRequest{Name: "feedface"}))
	require.NoError(t, err)
	assert.True(t, sh.closed)
	assert.Equal(t, "feedface", sh.closeIfArg)
	assert.Equal(t, tokenv1.CredentialClass_CREDENTIAL_CLASS_SHARE, resp.Msg.GetClass())
}

// A revoke is held to the caller's grant, as a mint is: a caller below the token it names is
// refused and nothing is deleted or closed.
func TestRevokeTokenNeverReachesPastTheCallersGrant(t *testing.T) {
	sh := liveShare("feedface")
	s := newIsolatedService(t, sh)
	_, conn := mint(t, "agent", types.GrantConnector)
	_, console := mint(t, "laptop", types.GrantConsole)
	for _, c := range []struct {
		caller types.Grant
		name   string
	}{
		{types.GrantViewer, console.ID},
		{types.GrantViewer, "laptop"},
		{types.GrantConsole, conn.ID},
		{types.GrantConnector, "laptop"},
		{types.Grant{}, "feedface"},
		{types.GrantConnector, "share to phone"},
	} {
		_, err := s.RevokeToken(as(c.caller), req(&tokenv1.RevokeTokenRequest{Name: c.name}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "%s revoking %s", c.caller, c.name)
	}
	assert.False(t, sh.closed)
	assert.Len(t, stored(t), 2)
}

// A revoke that lost a race with a new share reports NotFound and tears nothing down.
func TestRevokeShareLostRaceIsNotFound(t *testing.T) {
	sh := liveShare("feedface")
	sh.closeIfFail = true
	s := newIsolatedService(t, sh)
	_, err := s.RevokeToken(operator(), req(&tokenv1.RevokeTokenRequest{Name: "feedface"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.False(t, sh.closed)
}

// A revoke names an exact id or an exact name: a prefix matches nothing, share or stored.
func TestRevokeTakesNoPrefix(t *testing.T) {
	s := newIsolatedService(t, nil)
	_, rec := mint(t, "c1", types.GrantConnector)
	sh := liveShare(rec.ID[:1] + "0000000")
	s.share = sh
	for _, q := range []string{rec.ID[:1], rec.ID[:4], "c"} {
		_, err := s.RevokeToken(operator(), req(&tokenv1.RevokeTokenRequest{Name: q}))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), q)
	}
	assert.False(t, sh.closed)
	resp, err := s.RevokeToken(operator(), req(&tokenv1.RevokeTokenRequest{Name: rec.ID}))
	require.NoError(t, err)
	assert.Equal(t, rec.ID, resp.Msg.GetId())
}

func TestNilShareManagerConstructor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := NewService((*share.Manager)(nil))
	assert.Nil(t, s.share)
	list, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	assert.Empty(t, list.Msg.GetTokens())
	_, err = s.RevokeToken(operator(), req(&tokenv1.RevokeTokenRequest{Name: "share to phone"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// The operator token lives in a file this service never opens: it is never listed, a revoke
// keyed on its id is NotFound, and the file is untouched.
func TestOperatorTokenInvisibleAndImmutable(t *testing.T) {
	s := newIsolatedService(t, nil)
	_, rec := mint(t, "mcp-client", types.GrantConnector)
	op, err := auth.GenerateOperator()
	require.NoError(t, err)
	_, err = auth.SaveOperator(op)
	require.NoError(t, err)

	list, err := s.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetTokens(), 1)
	assert.Equal(t, rec.ID, list.Msg.GetTokens()[0].GetId())

	_, err = s.RevokeToken(operator(), req(&tokenv1.RevokeTokenRequest{Name: auth.TokenID(op)}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	loaded, err := auth.LoadOperator()
	require.NoError(t, err)
	assert.Equal(t, op, loaded)
}

// The handler's mint obeys the caller's grant, whatever mount it sits behind: a caller below
// the requested grant gets PermissionDenied and nothing is stored.
func TestCreateTokenNeverExceedsTheCallersGrant(t *testing.T) {
	s := newIsolatedService(t, nil)
	exp := timestamppb.New(time.Now().Add(time.Hour))
	cases := []struct {
		caller types.Grant
		grant  *tokenv1.Grant
	}{
		{types.Grant{}, wire(none, none, read)},
		{types.GrantViewer, wire(none, none, write)},
		{types.GrantConnector, wire(none, none, read)},
		{types.GrantConnector, wire(none, none, write)},
	}
	for _, c := range cases {
		_, err := s.CreateToken(as(c.caller), req(&tokenv1.CreateTokenRequest{Grant: c.grant, ExpireTime: exp}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "%s minting %v", c.caller, c.grant)
	}
	// With no guard in front at all, the caller holds nothing and mints nothing.
	_, err := s.CreateToken(context.Background(), req(&tokenv1.CreateTokenRequest{Grant: wire(none, none, read), ExpireTime: exp}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.Empty(t, stored(t))
}

// Which door: this surface mints console grants only, even for the operator.
func TestCreateTokenRefusesWhatTheConsoleMayNotMint(t *testing.T) {
	s := newIsolatedService(t, nil)
	for name, g := range map[string]*tokenv1.Grant{
		"operator":  wire(write, write, write),
		"tokens":    wire(write, none, none),
		"connector": wire(none, write, none),
		"nothing":   wire(none, none, none),
		"absent":    nil,
		"mcp=read":  wire(none, read, none),
		"unknown":   {Console: tokenv1.Level(9)},
	} {
		_, err := s.CreateToken(operator(), req(&tokenv1.CreateTokenRequest{
			Name: "escalate", Grant: g, ExpireTime: timestamppb.New(time.Now().Add(time.Hour)),
		}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
		assert.ErrorIs(t, err, types.TokenRequestInvalid, name)
	}
	assert.Empty(t, stored(t))
}

// Expiry is required and bounded, and a request beyond the bound is refused: no ceiling is
// substituted for what was asked.
func TestCreateTokenRefusesAnExpiryItCannotHonor(t *testing.T) {
	s := newIsolatedService(t, nil)
	for name, exp := range map[string]*timestamppb.Timestamp{
		"absent":    nil,
		"past":      timestamppb.New(time.Now().Add(-time.Hour)),
		"367 days":  timestamppb.New(time.Now().Add(367 * 24 * time.Hour)),
		"two years": timestamppb.New(time.Now().Add(2 * 366 * 24 * time.Hour)),
	} {
		_, err := s.CreateToken(operator(), req(&tokenv1.CreateTokenRequest{Grant: wire(none, none, write), ExpireTime: exp}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
		assert.ErrorIs(t, err, types.TokenLifetimeOutOfRange, name)
	}
	asked := time.Now().Add(300 * 24 * time.Hour)
	resp, err := s.CreateToken(operator(), req(&tokenv1.CreateTokenRequest{Grant: wire(none, none, write), ExpireTime: timestamppb.New(asked)}))
	require.NoError(t, err)
	assert.WithinDuration(t, asked, resp.Msg.GetToken().GetExpireTime().AsTime(), time.Second)
}

// A disk that refuses the write is the server's fault, not the caller's: Internal, never
// InvalidArgument.
func TestCreateTokenDiskFailureIsInternal(t *testing.T) {
	s := newIsolatedService(t, nil)
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o700))
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
	_, err = s.CreateToken(operator(), req(&tokenv1.CreateTokenRequest{Grant: wire(none, none, read), ExpireTime: timestamppb.New(time.Now().Add(time.Hour))}))
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// A minted console token opens the console and is refused at /mcp and token management; a
// viewer opens the read surface only.
func TestCreateTokenMintsTheGrantItNames(t *testing.T) {
	s := newIsolatedService(t, nil)
	exp := timestamppb.New(time.Now().Add(time.Hour))
	for _, want := range []types.Grant{types.GrantConsole, types.GrantViewer} {
		resp, err := s.CreateToken(operator(), req(&tokenv1.CreateTokenRequest{Grant: wireGrant(want), ExpireTime: exp}))
		require.NoError(t, err)
		got, err := fromWireGrant(resp.Msg.GetToken().GetGrant())
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, tokenv1.CredentialClass_CREDENTIAL_CLASS_STORED, resp.Msg.GetToken().GetClass())
		cred, ok := auth.Verify(resp.Msg.GetSecret())
		require.True(t, ok)
		assert.Equal(t, want, cred.Grant)
	}
}

// Mounted behind the server's own guard (auth.Verify, tokens=write), a connector, console or
// viewer token is refused with 403 on every RPC and the operator is admitted.
func TestTokenServiceGuardAdmitsOnlyTokensWrite(t *testing.T) {
	s := newIsolatedService(t, nil)
	op, err := auth.EnsureOperator(context.Background(), nil)
	require.NoError(t, err)
	_, h := tokenv1alpha1connect.NewTokenServiceHandler(s)
	guarded, err := httpx.BearerGuard(rpcerr.FormatConnect, auth.Verify, types.Need{Surface: types.SurfaceTokens, Level: types.LevelWrite}, h)
	require.NoError(t, err)
	srv := httptest.NewServer(guarded)
	defer srv.Close()

	for _, g := range []types.Grant{types.GrantConnector, types.GrantConsole, types.GrantViewer} {
		secret, _ := mint(t, strings.ReplaceAll(g.String(), "=", "-"), g)
		c := tokenv1alpha1connect.NewTokenServiceClient(http.DefaultClient, srv.URL, connect.WithInterceptors(bearer(secret)))
		_, err := c.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), g.String())
		_, err = c.CreateToken(context.Background(), req(&tokenv1.CreateTokenRequest{Grant: wire(none, none, write), ExpireTime: timestamppb.New(time.Now().Add(time.Hour))}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), g.String())
		_, err = c.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Name: "console-read"}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), g.String())
	}
	assert.Len(t, stored(t), 3, "no refused call minted or revoked anything")

	opClient := tokenv1alpha1connect.NewTokenServiceClient(http.DefaultClient, srv.URL, connect.WithInterceptors(bearer(op)))
	resp, err := opClient.CreateToken(context.Background(), req(&tokenv1.CreateTokenRequest{Grant: wire(none, none, write), ExpireTime: timestamppb.New(time.Now().Add(time.Hour))}))
	require.NoError(t, err)
	got, err := fromWireGrant(resp.Msg.GetToken().GetGrant())
	require.NoError(t, err)
	assert.Equal(t, types.GrantConsole, got)

	anon := tokenv1alpha1connect.NewTokenServiceClient(http.DefaultClient, srv.URL)
	_, err = anon.ListTokens(context.Background(), req(&tokenv1.ListTokensRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// The audit subject names the token acted on by id, name, grant and expiry, and never carries
// the secret, even from a mint response that does.
func TestAuditSubjectNamesTheTokenNeverItsSecret(t *testing.T) {
	info := &tokenv1.TokenInfo{Name: "laptop", Id: "3fa9c1d2", Grant: wireGrant(types.GrantConsole),
		ExpireTime: timestamppb.New(time.Date(2026, 12, 22, 0, 0, 0, 0, time.UTC))}
	const secret = "mgs_not-a-real-secret"
	blob, preview := AuditSubject(connect.NewResponse(&tokenv1.CreateTokenResponse{Token: info, Secret: secret}))
	assert.Equal(t, "token 3fa9c1d2 (laptop) console=write until 2026-12-22", preview)
	assert.NotContains(t, string(blob), secret)
	assert.Contains(t, string(blob), "3fa9c1d2")

	blob, preview = AuditSubject(connect.NewResponse(info))
	assert.NotEmpty(t, blob)
	assert.Contains(t, preview, "3fa9c1d2")

	blob, _ = AuditSubject(connect.NewResponse(&tokenv1.ListTokensResponse{Tokens: []*tokenv1.TokenInfo{info}}))
	assert.Nil(t, blob, "a list names no single subject")
}

// A name is a label and the id is the identity. Mint "laptop", revoke it, mint "laptop" again:
// the trail's two mint records name different ids, a record made under the first token names
// the first id and never the second, and a filter on the label still finds both tokens' work.
func TestReusedNameIsADifferentIdentityInTheTrail(t *testing.T) {
	s := newIsolatedService(t, nil)
	dir := t.TempDir()
	_, h := tokenv1alpha1connect.NewTokenServiceHandler(s,
		connect.WithInterceptors(trailrpc.Interceptor(dir, trail.KindTokenLifecycle, trailrpc.WithSubject(AuditSubject))))
	op := types.Credential{Class: types.ClassOperator, ID: "0badf00d", Grant: types.GrantOperator}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(trail.ContextWithEntryPoint(trail.ContextWithCredential(r.Context(), op), types.EntryPointRPC)))
	}))
	defer srv.Close()
	c := tokenv1alpha1connect.NewTokenServiceClient(http.DefaultClient, srv.URL)
	exp := time.Now().Add(time.Hour)
	create := func() (string, string) {
		resp, err := c.CreateToken(context.Background(), req(&tokenv1.CreateTokenRequest{Name: "laptop",
			Grant: wire(none, none, write), ExpireTime: timestamppb.New(exp)}))
		require.NoError(t, err)
		return resp.Msg.GetSecret(), resp.Msg.GetToken().GetId()
	}
	firstSecret, firstID := create()
	_, err := c.RevokeToken(context.Background(), req(&tokenv1.RevokeTokenRequest{Name: "laptop"}))
	require.NoError(t, err)
	secondSecret, secondID := create()
	require.NotEqual(t, firstID, secondID)

	events, err := trail.ReadRecent(dir, 10)
	require.NoError(t, err)
	var subjects []string
	for _, e := range events {
		blob, err := trail.ReadBlob(dir, e.RequestRef)
		require.NoError(t, err)
		assert.NotContains(t, string(blob), firstSecret)
		assert.NotContains(t, string(blob), secondSecret)
		assert.Equal(t, op, e.Credential, "each record names the credential that acted")
		subjects = append(subjects, e.Action+" "+e.Preview)
	}
	until := " (laptop) console=write until " + exp.UTC().Format("2006-01-02")
	assert.Equal(t, []string{
		"CreateToken token " + secondID + until,
		"RevokeToken token " + firstID + until,
		"CreateToken token " + firstID + until,
	}, subjects, "newest first: each record names the id it acted on")

	second, ok := auth.Verify(secondSecret)
	require.True(t, ok)
	assert.Equal(t, secondID, second.ID)
	_, ok = auth.Verify(firstSecret)
	assert.False(t, ok, "the revoked secret does not come back with the name")
	underFirst := types.Origin{Credential: types.Credential{Class: types.ClassStored, ID: firstID, Name: "laptop"}}
	underSecond := types.Origin{Credential: second}
	assert.True(t, underFirst.Names(firstID))
	assert.False(t, underFirst.Names(secondID), "a record made under the first token never reads as the second")
	assert.True(t, underFirst.Names("laptop"))
	assert.True(t, underSecond.Names("laptop"))
}

func bearer(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, ar connect.AnyRequest) (connect.AnyResponse, error) {
			ar.Header().Set("Authorization", "Bearer "+token)
			return next(ctx, ar)
		}
	}
}
