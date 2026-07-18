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

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/share"
	tokenv1 "github.com/egladman/magus/proto/gen/go/magus/token/v1"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1/tokenv1connect"
)

// shareView is the narrow slice of *share.Manager the handler needs: read the active
// share token's metadata and tear it (with its listener) down by identity. Satisfied
// structurally by *share.Manager; a test fake stands in for it. CloseIf, not Close, is
// what the handler holds so revoke stays an atomic check-and-close (see RevokeToken).
type shareView interface {
	Active() (share.TokenInfo, bool)
	CloseIf(fingerprint string) bool
}

// Service implements tokenv1connect.TokenServiceHandler over the shared connector
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

var _ tokenv1connect.TokenServiceHandler = (*Service)(nil)

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

// RevokeToken removes the token matching identifier. It checks the active share
// token first: when identifier names it, CloseIf revokes the token AND tears the LAN
// listener down (the share's own teardown, not a reimplementation), but ONLY if that
// exact share is still live - if a supersede won the race between Active and CloseIf,
// the revoke reports NotFound rather than tearing down whatever share replaced it.
// Otherwise it falls to the connector store. The cli token is never consulted, so it
// cannot be revoked here even if its fingerprint is supplied.
func (s *Service) RevokeToken(_ context.Context, req *connect.Request[tokenv1.RevokeTokenRequest]) (*connect.Response[tokenv1.RevokeTokenResponse], error) {
	id := strings.TrimSpace(req.Msg.GetIdentifier())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token: identifier is required"))
	}

	if s.share != nil {
		if info, ok := s.share.Active(); ok && shareMatches(info, id) {
			if s.share.CloseIf(info.Fingerprint) {
				return connect.NewResponse(&tokenv1.RevokeTokenResponse{Token: shareInfo(info)}), nil
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
	return connect.NewResponse(&tokenv1.RevokeTokenResponse{Token: connectorInfo(removed)}), nil
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
		Scope:      tokenv1.TokenScope_TOKEN_SCOPE_CONNECTOR,
	}
	if !c.Expires.IsZero() {
		info.Expires = timestamppb.New(c.Expires)
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
		Expires:    timestamppb.New(i.Expires),
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
