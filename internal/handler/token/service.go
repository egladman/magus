// Package token is the console-facing TokenService handler: the typed management
// surface for the daemon's auth tokens. It is VIEW-AND-REVOKE ONLY - it lists and
// revokes tokens but can NEVER mint one. Minting stays a CLI-only operation
// (`magus config mcp connector`), so a compromised browser session cannot forge a
// durable credential; the XSS-to-durable-credential escalation is closed by
// construction. It is a SECOND door onto the exact stores the CLI and the share flow
// already use - the on-disk connector store (internal/auth) and the daemon's
// in-memory share manager (internal/share) - never a second store of its own. Two
// tokens are deliberately beyond its reach: the OPERATOR token (the built-in cli
// credential, auto-seeded on first daemon start) and any renew/extend operation (a
// token is reminted via the CLI, never extended). The operator boundary is by
// CONSTRUCTION, not convention: the cli token lives in a store this handler never
// opens (auth.Load, distinct from the connector store), so ListTokens cannot
// enumerate it and RevokeToken keyed on its fingerprint falls through to the
// connector store and returns NotFound, leaving the cli token file untouched - the
// management UI can never lock the operator out of the daemon it authenticates
// against. TestOperatorTokenInvisibleAndImmutable proves it. The daemon mounts it on
// the loopback listener behind a CLI-TOKEN-ONLY
// bearer guard (auth.VerifyCLIBearer): token management is operator-tier, so a
// connector token - a mere MCP-client credential - is rejected at the guard and can
// never revoke credentials. It is NEVER mounted on the LAN share listener and never
// served unauthenticated.
package token

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/share"
	tokenv1 "github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1/tokenv1alpha1connect"
)

// shareView is the narrow slice of *share.Manager the handler needs: read the active
// share token's metadata and tear it (with its listener) down by identity. Satisfied
// structurally by *share.Manager; a test fake stands in for it. CloseIf, not Close, is
// what the handler holds so revoke stays an atomic check-and-close (see RevokeToken).
type shareView interface {
	Active() (share.TokenInfo, bool)
	CloseIf(fingerprint string) bool
}

// Service implements tokenv1alpha1connect.TokenServiceHandler over the shared connector
// store and the daemon's share manager. loadStore is injectable so the list/revoke
// mapping is unit-testable without a live daemon; it defaults to the real store
// loader.
type Service struct {
	share     shareView
	loadStore func() (*auth.ConnectorStore, error)
}

// NewService builds a TokenService handler that manages connector tokens through the
// shared on-disk store and the share token through mgr. It takes the CONCRETE
// *share.Manager (not the shareView interface) on purpose: a typed-nil manager passed
// straight into an interface field would be non-nil at the interface level - the
// classic typed-nil trap - and every `s.share != nil` guard would then pass and
// nil-deref. Converting only a non-nil manager keeps "no share feature" a true nil, so
// a nil mgr simply means no share token is ever listed or revoked.
func NewService(mgr *share.Manager) *Service {
	var view shareView
	if mgr != nil {
		view = mgr
	}
	return newService(view)
}

// newService injects the shareView directly. It backs NewService and lets tests supply
// a fake share manager without opening a real LAN listener.
func newService(view shareView) *Service {
	return &Service{
		share:     view,
		loadStore: auth.LoadConnectorStore,
	}
}

var _ tokenv1alpha1connect.TokenServiceHandler = (*Service)(nil)

// ListTokens returns every connector token plus the active share token, each as a
// secret-free TokenInfo. The cli token is deliberately absent: it is neither read
// from nor exposed here, so this surface cannot reveal or target it. last_used is
// left unset - see the package note; there is no cheap seam to record it.
func (s *Service) ListTokens(_ context.Context, _ *connect.Request[tokenv1.ListTokensRequest]) (*connect.Response[tokenv1.ListTokensResponse], error) {
	store, err := s.loadStore()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	conns := store.List()
	out := make([]*tokenv1.TokenInfo, 0, len(conns)+1)
	for _, c := range conns {
		out = append(out, connectorInfo(c))
	}
	if s.share != nil {
		if info, ok := s.share.Active(); ok {
			out = append(out, shareInfo(info))
		}
	}
	return connect.NewResponse(&tokenv1.ListTokensResponse{Tokens: out}), nil
}

// CreateToken mints a console or viewer token and returns its secret once.
//
// There is no caller-class check here on purpose. The service is mounted behind
// BearerGuard(VerifyCLIBearer) (see internal/daemon), so only the operator tier can
// reach this method at all, and that tier already dominates both scopes it may mint -
// there is no escalation to check for. What IS checked is the requested scope, because
// "operator may mint anything" is not the same claim as "anything may be minted from a
// browser": OPERATOR is refused because it lives in a file this service never opens, and
// CONNECTOR because an /mcp bearer must not be mintable from the console surface.
func (s *Service) CreateToken(_ context.Context, req *connect.Request[tokenv1.CreateTokenRequest]) (*connect.Response[tokenv1.CreateTokenResponse], error) {
	scope, ok := mintableScope(req.Msg.GetScope())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("token: scope must be TOKEN_SCOPE_CONSOLE or TOKEN_SCOPE_CONSOLE_READ; the operator and connector classes are not mintable here"))
	}

	store, err := auth.LoadConnectorStore()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("token: %w", err))
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		name = defaultConsoleTokenName(store)
	}
	var expires time.Time
	if req.Msg.ExpireTime != nil {
		expires = req.Msg.GetExpireTime().AsTime()
	}

	secret, c, err := store.Create(name, expires, scope)
	if err != nil {
		if errors.Is(err, auth.ErrConnectorExists) {
			return nil, connect.NewError(connect.CodeAlreadyExists,
				fmt.Errorf("token: a token named %q already exists", name))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("token: %w", err))
	}
	return connect.NewResponse(&tokenv1.CreateTokenResponse{Token: connectorInfo(c), Secret: secret}), nil
}

// defaultConsoleTokenName picks an unused "console-N" label so a caller that supplies no
// name cannot collide with an existing token and get AlreadyExists for a name it never
// chose. The CLI derives its default the same way.
func defaultConsoleTokenName(store *auth.ConnectorStore) string {
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

// RevokeToken removes the token matching identifier. It checks the active share
// token first: when identifier names it, CloseIf revokes the token AND tears the LAN
// listener down (the share's own teardown, not a reimplementation), but ONLY if that
// exact share is still live - if a supersede won the race between Active and CloseIf,
// the revoke reports NotFound rather than tearing down whatever share replaced it.
// Otherwise it falls to the connector store. The cli token is never consulted, so it
// cannot be revoked here even if its fingerprint is supplied.
func (s *Service) RevokeToken(_ context.Context, req *connect.Request[tokenv1.RevokeTokenRequest]) (*connect.Response[tokenv1.TokenInfo], error) {
	id := strings.TrimSpace(req.Msg.GetName())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token: identifier is required"))
	}

	if s.share != nil {
		if info, ok := s.share.Active(); ok && shareMatches(info, id) {
			if s.share.CloseIf(info.Fingerprint) {
				return connect.NewResponse(shareInfo(info)), nil
			}
			// The share we matched was superseded between Active and CloseIf, so there
			// is nothing of that identity left to revoke; do not fall through to the
			// connector store with a share fingerprint.
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("token: no token matches %q", id))
		}
	}

	store, err := s.loadStore()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	removed, err := store.Revoke(id)
	if err != nil {
		if errors.Is(err, auth.ErrConnectorNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("token: no token matches %q", id))
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(connectorInfo(removed)), nil
}

// wireScope maps a stored token's surface to its wire class. One store holds all three
// client classes, so reading the record's own scope is what keeps a console token from
// being listed as a connector - which is what a hardcoded class did, and it mislabelled
// every console and viewer token the moment the tiers split.
func wireScope(s auth.ClientScope) tokenv1.TokenScope {
	switch s {
	case auth.ScopeConsole:
		return tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE
	case auth.ScopeConsoleRead:
		return tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE_READ
	default:
		return tokenv1.TokenScope_TOKEN_SCOPE_CONNECTOR
	}
}

// mintableScope maps a requested wire class to the stored scope it may be minted as, and
// reports false for every class this service refuses to mint. Only the two console tiers
// are mintable: OPERATOR lives in a file this service never opens, and CONNECTOR would be
// an /mcp bearer minted from a browser.
func mintableScope(s tokenv1.TokenScope) (auth.ClientScope, bool) {
	switch s {
	case tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE:
		return auth.ScopeConsole, true
	case tokenv1.TokenScope_TOKEN_SCOPE_CONSOLE_READ:
		return auth.ScopeConsoleRead, true
	}
	return "", false
}

// shareTokenLabel is the display name for the anonymous share token, which carries
// no user-assigned name. It doubles as a revoke alias (revoke "share to phone").
const shareTokenLabel = "share to phone"

// connectorInfo maps a stored connector record to its secret-free, minimized wire
// shape: the revoke handle (fingerprint), the class, the user-chosen name, and the
// expiry only - never the secret, the full hash, or the creation time (see TokenInfo's
// minimization note). A zero Expires (never expires) leaves the expires timestamp unset.
func connectorInfo(c auth.ConnectorToken) *tokenv1.TokenInfo {
	info := &tokenv1.TokenInfo{
		Name:       c.Name,
		Identifier: c.Fingerprint,
		Scope:      wireScope(c.EffectiveScope()),
	}
	if !c.Expires.IsZero() {
		info.ExpireTime = timestamppb.New(c.Expires)
	}
	return info
}

// shareInfo maps the active share's metadata to its secret-free, minimized wire shape:
// the same handle/class/name/expiry-only projection as connectorInfo, with no creation
// time or full hash.
func shareInfo(i share.TokenInfo) *tokenv1.TokenInfo {
	return &tokenv1.TokenInfo{
		Name:       shareTokenLabel,
		Identifier: i.Fingerprint,
		Scope:      tokenv1.TokenScope_TOKEN_SCOPE_SHARE_READ,
		ExpireTime: timestamppb.New(i.Expires),
	}
}

// shareMatches reports whether identifier names the active share token: either its
// label or its EXACT full fingerprint. Unlike the connector store's name/fingerprint/
// prefix resolution, the share deliberately does NOT prefix-match: a prefix that also
// prefixes a connector fingerprint must resolve to the connector (the store's job),
// never get intercepted here by the share. Exact-only keeps that disambiguation
// unambiguous - List hands out the full 8-char fingerprint, so an exact match is
// always available to a client that wants the share.
func shareMatches(i share.TokenInfo, identifier string) bool {
	return identifier == shareTokenLabel || identifier == i.Fingerprint
}
