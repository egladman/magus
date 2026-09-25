package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// ErrPeerCredentialsUnsupported is reading a unix socket peer's uid on a platform where magus
// does not implement it, so [PeerGuard] could admit no one.
var ErrPeerCredentialsUnsupported = fmt.Errorf("httpx: reading a unix socket peer's uid is not implemented on %s", runtime.GOOS)

// PeerCredentialsSupported reports whether this platform can read a unix socket peer's uid,
// which [PeerGuard] requires.
func PeerCredentialsSupported() bool { return peerCredentialsSupported }

// peer is what the kernel reported about a connection's other end.
type peer struct {
	uid int
	err error
}

type peerKey struct{}

// PeerConnContext is an [http.Server.ConnContext] for a unix socket listener: it asks the
// kernel for each accepted connection's peer uid, once, for [PeerGuard] to read. A connection
// that is not a unix socket records an error, which PeerGuard refuses.
func PeerConnContext(ctx context.Context, c net.Conn) context.Context {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return context.WithValue(ctx, peerKey{}, peer{err: fmt.Errorf("httpx: %T is not a unix connection", c)})
	}
	uid, err := unixPeerUID(uc)
	return context.WithValue(ctx, peerKey{}, peer{uid: uid, err: err})
}

func unixPeerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid := -1
	var perr error
	if err := raw.Control(func(fd uintptr) { uid, perr = peerUID(int(fd)) }); err != nil {
		return -1, err
	}
	return uid, perr
}

// PeerGuard admits a request whose connection's peer runs as uid, as recorded by
// [PeerConnContext], and hands it to next as cred on the rpc entry point, as [BearerGuard]
// would. It reads no bearer token: the kernel's report of the peer is the whole
// authentication, so it decides no route's Need; put a [GrantGuard] behind it for that. A
// peer of another uid, or one the kernel would not name, is refused 403 MGS9022.
//
// No DNS-rebind check applies: a browser cannot open a unix socket.
func PeerGuard(format rpcerr.Format, uid int, cred types.Credential, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := r.Context().Value(peerKey{}).(peer)
		switch {
		case !ok || p.err != nil:
			format.Write(w, r, peerRefused(fmt.Sprintf("this socket admits only processes running as uid %d, and the kernel did not report this connection's peer", uid)))
			return
		case p.uid != uid:
			format.Write(w, r, peerRefused(fmt.Sprintf("this socket admits only processes running as uid %d; this connection's peer runs as uid %d", uid, p.uid)))
			return
		}
		ctx := trail.ContextWithEntryPoint(trail.ContextWithCredential(r.Context(), cred), types.EntryPointRPC)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func peerRefused(msg string) rpcerr.Error {
	return rpcerr.Error{Code: connect.CodePermissionDenied, Reason: types.SocketPeerNotOwner, Message: msg}
}

// GrantGuard holds each request to the Need of its path against the credential already on its
// context, the one a [PeerGuard] in front of it stamped. needs maps a path, or a Connect
// procedure path, to its Need; a path it does not name is held to the strictest Need in it, as
// [ProcedureGuard] does. A credential below the Need is refused 403 MGS9015, and a request
// that reaches it with no credential holds nothing. It refuses to build with an empty table or
// an invalid Need.
func GrantGuard(format rpcerr.Format, needs map[string]types.Need, next http.Handler) (http.Handler, error) {
	needOf, err := needTable(needs)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		need := needOf(r)
		if held := trail.CredentialFromContext(r.Context()).Grant; !held.Allows(need) {
			format.Write(w, r, grantBelow(need, held))
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

// needTable validates needs and returns the lookup [ProcedureGuard] and [GrantGuard] share: a
// path's own Need, else the strictest in the table.
func needTable(needs map[string]types.Need) (func(*http.Request) types.Need, error) {
	if len(needs) == 0 {
		return nil, errors.New("httpx: a procedure guard needs at least one procedure")
	}
	var strictest types.Need
	for proc, n := range needs {
		if err := n.Validate(); err != nil {
			return nil, fmt.Errorf("httpx: %s: %w", proc, err)
		}
		if strictest == (types.Need{}) || stricter(n, strictest) {
			strictest = n
		}
	}
	return func(r *http.Request) types.Need {
		if n, ok := needs[r.URL.Path]; ok {
			return n
		}
		return strictest
	}, nil
}
