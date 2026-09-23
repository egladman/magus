// Package token is the console-facing TokenService handler: it lists, mints and revokes stored
// tokens, and lists and revokes the active share link. It is a second door onto the stores the
// CLI and the share flow already use (auth.Store, share.Manager), never a store of its own.
//
// Two rules keep it from being a way up. A mint is checked against the caller's own grant, read
// from the credential the bearer guard verified (auth.Store.Mint refuses anything wider), so the
// daemon's tokens=write mount is defense in depth rather than the rule. And it mints only the
// two console presets: a browser has no business minting an /mcp bearer, and the operator token
// lives in a file this handler never opens, so it can be neither listed nor revoked here and the
// management UI cannot lock the operator out.
package token

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
// daemon's share manager. loadStore is injectable so the mapping is testable without a daemon.
type Service struct {
	share     shareView
	loadStore func() (*auth.Store, error)
}

// NewService builds a TokenService handler over the token store and mgr. It takes the concrete
// *share.Manager so a nil one stays a true nil rather than a non-nil interface holding nil; a
// nil mgr means no share is ever listed or revoked.
func NewService(mgr *share.Manager) *Service {
	var view shareView
	if mgr != nil {
		view = mgr
	}
	return newService(view)
}

func newService(view shareView) *Service {
	return &Service{share: view, loadStore: auth.LoadStore}
}

var _ tokenv1alpha1connect.TokenServiceHandler = (*Service)(nil)

// ListTokens returns every stored token plus the active share link, each secret-free. The
// operator token is never read here, so it never appears.
func (s *Service) ListTokens(_ context.Context, _ *connect.Request[tokenv1.ListTokensRequest]) (*connect.Response[tokenv1.ListTokensResponse], error) {
	store, err := s.loadStore()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	stored := store.List()
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

// CreateToken mints a console or viewer token within the caller's grant and returns its
// secret once. expire_time is required and must fall within auth.MaxTokenTTL; it is refused,
// never shortened. A caller whose grant does not cover the request gets PermissionDenied.
func (s *Service) CreateToken(ctx context.Context, req *connect.Request[tokenv1.CreateTokenRequest]) (*connect.Response[tokenv1.CreateTokenResponse], error) {
	grant, ok := mintableGrant(req.Msg.GetScope())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("token: scope must be TOKEN_SCOPE_CONSOLE or TOKEN_SCOPE_CONSOLE_READ; the operator and connector classes are not minted here"))
	}
	if req.Msg.ExpireTime == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token: expire_time is required; a token must expire, at most 366 days out"))
	}
	store, err := s.loadStore()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("token: %w", err))
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		name = defaultConsoleTokenName(store)
	}
	minter := trail.CredentialFromContext(ctx).Grant
	secret, rec, err := store.Mint(minter, auth.MintRequest{Name: name, Grant: grant, Expires: req.Msg.GetExpireTime().AsTime()})
	switch {
	case errors.Is(err, auth.ErrExceedsGrant):
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, auth.ErrTokenLifetime):
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, auth.ErrTokenExists):
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("token: a token named %q already exists", name))
	case err != nil:
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&tokenv1.CreateTokenResponse{Token: storedInfo(rec), Secret: secret}), nil
}

// defaultConsoleTokenName picks an unused "console-N", so a caller that names nothing cannot
// collide with a name it never chose.
func defaultConsoleTokenName(store *auth.Store) string {
	taken := map[string]bool{}
	for _, t := range store.List() {
		taken[t.Name] = true
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("console-%d", i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// RevokeToken removes the token identifier names. The active share link is checked first,
// and CloseIf revokes it only if that exact link is still live, so a revoke that raced a new
// share reports NotFound rather than tearing the new one down. Otherwise it falls to the store.
// The operator token is never consulted, so it cannot be revoked here.
func (s *Service) RevokeToken(_ context.Context, req *connect.Request[tokenv1.RevokeTokenRequest]) (*connect.Response[tokenv1.TokenInfo], error) {
	id := strings.TrimSpace(req.Msg.GetName())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token: identifier is required"))
	}
	if s.share != nil {
		if info, ok := s.share.Active(); ok && shareMatches(info, id) {
			if s.share.CloseIf(info.ID) {
				return connect.NewResponse(shareInfo(info)), nil
			}
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("token: no token matches %q", id))
		}
	}
	store, err := s.loadStore()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	removed, err := store.Revoke(id)
	if err != nil {
		if errors.Is(err, auth.ErrTokenNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("token: no token matches %q", id))
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(storedInfo(removed)), nil
}

// mintableGrant is the which-door policy: the grants a browser may ask for by scope.
func mintableGrant(s tokenv1.TokenScope) (types.Grant, bool) {
	switch s {
	case tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE:
		return types.GrantConsole, true
	case tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE_READ:
		return types.GrantViewer, true
	}
	return types.Grant{}, false
}

// wireScope labels a grant with its preset. A grant matching no preset is UNSPECIFIED; the
// grant field says what it is.
func wireScope(g types.Grant) tokenv1.TokenScope {
	switch g {
	case types.GrantConnector:
		return tokenv1.TokenScope_TOKEN_SCOPE_CONNECTOR
	case types.GrantConsole:
		return tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE
	case types.GrantViewer:
		return tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE_READ
	}
	return tokenv1.TokenScope_TOKEN_SCOPE_UNSPECIFIED
}

// shareTokenLabel names the share link in a listing, and doubles as a revoke alias.
const shareTokenLabel = "share to phone"

// storedInfo is a stored token's secret-free wire shape.
func storedInfo(t auth.Token) *tokenv1.TokenInfo {
	return &tokenv1.TokenInfo{
		Name:       t.Name,
		Identifier: t.ID,
		Scope:      wireScope(t.Grant),
		ExpireTime: timestamppb.New(t.Expires),
		Grant:      t.Grant.String(),
	}
}

func shareInfo(i share.TokenInfo) *tokenv1.TokenInfo {
	return &tokenv1.TokenInfo{
		Name:       shareTokenLabel,
		Identifier: i.ID,
		Scope:      tokenv1.TokenScope_TOKEN_SCOPE_SHARE_READ,
		ExpireTime: timestamppb.New(i.Expires),
		Grant:      types.GrantShare.String(),
	}
}

// shareMatches reports whether identifier names the share link: its label or its exact id.
// No prefix match, so a prefix shared with a stored token's id resolves to the stored token.
func shareMatches(i share.TokenInfo, identifier string) bool {
	return identifier == shareTokenLabel || identifier == i.ID
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
	preview = "token " + info.GetIdentifier()
	if info.GetName() != "" {
		preview += " (" + info.GetName() + ")"
	}
	if info.GetGrant() != "" {
		preview += " " + info.GetGrant()
	}
	if info.ExpireTime != nil {
		preview += " until " + info.GetExpireTime().AsTime().UTC().Format("2006-01-02")
	}
	return blob, preview
}
