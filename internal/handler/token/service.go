// Package token is the console-facing TokenService handler: it lists, mints and revokes stored
// tokens, and lists and revokes the active share link. It is a second door onto the stores the
// CLI and the share flow already use (auth.Store, share.Manager), never a store of its own.
//
// Two rules keep it from being a way up. A mint and a revoke are checked against the caller's
// own grant, read from the credential the bearer guard verified (auth.Store refuses anything
// wider), so the server's tokens=write mount is defense in depth rather than the rule. And it
// mints console grants only: a browser has no business minting an /mcp token, and the operator
// token lives in a file this handler never opens, so it can be neither listed nor revoked here
// and the management UI cannot lock the operator out.
package token

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/share"
	"github.com/egladman/magus/internal/trail"
	tokenv1 "github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1/tokenv1alpha1connect"
	"github.com/egladman/magus/types"
)

// shareView is the slice of *share.Manager the handler needs. CloseIf, not Close, so a revoke
// is an atomic check-and-close (see RevokeToken).
type shareView interface {
	Active() (share.TokenInfo, bool)
	CloseIf(id string) bool
}

// Service implements tokenv1alpha1connect.TokenServiceHandler over the token store and the
// server's share manager.
type Service struct {
	share shareView
}

// NewService builds a TokenService handler over the token store and mgr. It takes the concrete
// *share.Manager so a nil one stays a true nil rather than a non-nil interface holding nil; a
// nil mgr means no share is ever listed or revoked.
func NewService(mgr *share.Manager) *Service {
	var view shareView
	if mgr != nil {
		view = mgr
	}
	return &Service{share: view}
}

var _ tokenv1alpha1connect.TokenServiceHandler = (*Service)(nil)

func openStore() (*auth.Store, error) {
	dir, err := auth.StoreDir()
	if err != nil {
		return nil, err
	}
	return auth.LoadStore(dir)
}

// ListTokens returns every stored token plus the active share link, each secret-free. The
// operator token is never read here, so it never appears.
func (s *Service) ListTokens(_ context.Context, _ *connect.Request[tokenv1.ListTokensRequest]) (*connect.Response[tokenv1.ListTokensResponse], error) {
	store, err := openStore()
	if err != nil {
		return nil, connectError(err)
	}
	stored, err := store.List()
	if err != nil {
		return nil, connectError(err)
	}
	out := make([]*tokenv1.TokenInfo, 0, len(stored)+1)
	for _, t := range stored {
		out = append(out, storedInfo(t))
	}
	if s.share != nil {
		if info, ok := s.share.Active(); ok {
			out = append(out, shareInfo(info))
		}
	}
	return connect.NewResponse(&tokenv1.ListTokensResponse{Tokens: out}), nil
}

// CreateToken mints a stored token holding the requested console grant, within the caller's
// grant, and returns its secret once. expire_time is required and must fall within
// auth.MaxTokenTTL; it is refused, never shortened.
func (s *Service) CreateToken(ctx context.Context, req *connect.Request[tokenv1.CreateTokenRequest]) (*connect.Response[tokenv1.CreateTokenResponse], error) {
	grant, err := fromWireGrant(req.Msg.GetGrant())
	if err != nil {
		return nil, connectError(err)
	}
	if grant.MCP != types.LevelNone || grant.Tokens != types.LevelNone {
		return nil, connectError(types.WrapDiagnostic(types.TokenRequestInvalid, auth.ErrInvalidTokenRequest,
			"token: the console mints console grants only; mint an /mcp token with `magus config mcp connector create`"))
	}
	var ttl time.Duration
	if exp := req.Msg.ExpireTime; exp != nil {
		ttl = time.Until(exp.AsTime())
	}
	store, err := openStore()
	if err != nil {
		return nil, connectError(err)
	}
	minter := trail.CredentialFromContext(ctx).Grant
	secret, rec, err := store.Mint(minter, auth.MintRequest{Name: req.Msg.GetName(), Grant: grant, TTL: ttl})
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&tokenv1.CreateTokenResponse{Token: storedInfo(rec), Secret: secret}), nil
}

// RevokeToken removes the token id names, by exact id or exact name, when it is within the
// caller's grant. The active share link is checked first, and CloseIf revokes it only if that
// exact link is still live, so a revoke that raced a new share reports NotFound rather than
// tearing the new one down. The operator token is never consulted, so it cannot be revoked
// here.
func (s *Service) RevokeToken(ctx context.Context, req *connect.Request[tokenv1.RevokeTokenRequest]) (*connect.Response[tokenv1.TokenInfo], error) {
	id := req.Msg.GetName()
	caller := trail.CredentialFromContext(ctx).Grant
	if s.share != nil {
		if info, ok := s.share.Active(); ok && shareMatches(info, id) {
			if !types.GrantViewer.Within(caller) {
				return nil, connectError(types.WrapDiagnostic(types.GrantInsufficient, auth.ErrExceedsGrant, "token: revoking the share link needs %s", types.GrantViewer))
			}
			if s.share.CloseIf(info.ID) {
				return connect.NewResponse(shareInfo(info)), nil
			}
			return nil, connectError(types.WrapDiagnostic(types.TokenNotFound, auth.ErrTokenNotFound, "token: no token has the name or id %q", id))
		}
	}
	store, err := openStore()
	if err != nil {
		return nil, connectError(err)
	}
	removed, err := store.Revoke(caller, id)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(storedInfo(removed)), nil
}

// connectError maps an auth refusal onto its Connect code by the sentinel it wraps, keeping
// the coded message. Anything else, a disk failure included, is Internal.
func connectError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, auth.ErrExceedsGrant):
		code = connect.CodePermissionDenied
	case errors.Is(err, auth.ErrTokenLifetime), errors.Is(err, auth.ErrInvalidTokenRequest):
		code = connect.CodeInvalidArgument
	case errors.Is(err, auth.ErrTokenExists):
		code = connect.CodeAlreadyExists
	case errors.Is(err, auth.ErrTokenNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, types.TokenStoreTooOld), errors.Is(err, types.InsecureTokenPermissions), errors.Is(err, types.TokenStoreTooNew):
		code = connect.CodeFailedPrecondition
	}
	return connect.NewError(code, err)
}

// shareTokenLabel names the share link in a listing, and doubles as a revoke alias.
const shareTokenLabel = "share to phone"

// storedInfo is a stored record's secret-free wire shape.
func storedInfo(t auth.Token) *tokenv1.TokenInfo {
	return &tokenv1.TokenInfo{
		Name:       t.Name,
		Id:         t.ID,
		Class:      wireClass(t.Class),
		Grant:      wireGrant(t.Grant),
		ExpireTime: timestamppb.New(t.Expires),
	}
}

func shareInfo(i share.TokenInfo) *tokenv1.TokenInfo {
	return &tokenv1.TokenInfo{
		Name:       shareTokenLabel,
		Id:         i.ID,
		Class:      tokenv1.CredentialClass_CREDENTIAL_CLASS_SHARE,
		Grant:      wireGrant(types.GrantViewer),
		ExpireTime: timestamppb.New(i.Expires),
	}
}

// shareMatches reports whether identifier names the share link: its label or its exact id.
func shareMatches(i share.TokenInfo, identifier string) bool {
	return identifier == shareTokenLabel || identifier == i.ID
}

var wireLevels = map[types.Level]tokenv1.Level{
	types.LevelNone:  tokenv1.Level_LEVEL_UNSPECIFIED,
	types.LevelRead:  tokenv1.Level_LEVEL_READ,
	types.LevelWrite: tokenv1.Level_LEVEL_WRITE,
}

func wireGrant(g types.Grant) *tokenv1.Grant {
	return &tokenv1.Grant{Tokens: wireLevels[g.Tokens], Mcp: wireLevels[g.MCP], Console: wireLevels[g.Console]}
}

// fromWireGrant reads a wire grant, refusing a level this magus does not know.
func fromWireGrant(w *tokenv1.Grant) (types.Grant, error) {
	level := func(l tokenv1.Level) (types.Level, error) {
		for local, wire := range wireLevels {
			if wire == l {
				return local, nil
			}
		}
		return types.LevelNone, types.WrapDiagnostic(types.TokenRequestInvalid, auth.ErrInvalidTokenRequest, "token: unknown level %d", l)
	}
	var g types.Grant
	var err error
	if g.Tokens, err = level(w.GetTokens()); err != nil {
		return g, err
	}
	if g.MCP, err = level(w.GetMcp()); err != nil {
		return g, err
	}
	g.Console, err = level(w.GetConsole())
	return g, err
}

func wireClass(c types.CredentialClass) tokenv1.CredentialClass {
	switch c {
	case types.ClassOperator:
		return tokenv1.CredentialClass_CREDENTIAL_CLASS_OPERATOR
	case types.ClassStored:
		return tokenv1.CredentialClass_CREDENTIAL_CLASS_STORED
	case types.ClassShare:
		return tokenv1.CredentialClass_CREDENTIAL_CLASS_SHARE
	case types.ClassExchange:
		return tokenv1.CredentialClass_CREDENTIAL_CLASS_EXCHANGE
	}
	return tokenv1.CredentialClass_CREDENTIAL_CLASS_UNSPECIFIED
}

// AuditSubject renders what the trail records about a mint or revoke: the TokenInfo of the
// token acted on as JSON, which has no secret field, and a one-line preview such as "token
// 3fa9c1d2 (laptop) console=write until 2026-12-22". It returns nil for a response that names
// no token (ListTokens), so nothing is recorded for it.
func AuditSubject(resp connect.AnyResponse) (blob []byte, preview string) {
	var info *tokenv1.TokenInfo
	switch msg := resp.Any().(type) {
	case *tokenv1.CreateTokenResponse:
		info = msg.GetToken()
	case *tokenv1.TokenInfo:
		info = msg
	}
	if info == nil {
		return nil, ""
	}
	blob, err := protojson.Marshal(info)
	if err != nil {
		return nil, ""
	}
	preview = "token " + info.GetId()
	if info.GetName() != "" {
		preview += " (" + info.GetName() + ")"
	}
	if g, err := fromWireGrant(info.GetGrant()); err == nil && g != (types.Grant{}) {
		preview += " " + g.String()
	}
	if info.ExpireTime != nil {
		preview += " until " + info.GetExpireTime().AsTime().UTC().Format("2006-01-02")
	}
	return blob, preview
}
