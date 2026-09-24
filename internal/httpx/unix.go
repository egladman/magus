package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

var (
	// ErrSocketInUse is NewUnixServer finding a live listener already bound to its path.
	ErrSocketInUse = errors.New("httpx: another process is serving this socket")
	// ErrPeerCredentialsUnsupported is NewUnixServer on a platform where magus cannot read a
	// connection's peer uid, so [SocketPeerGuard] could admit no one.
	ErrPeerCredentialsUnsupported = fmt.Errorf("httpx: reading a unix socket peer's uid is not implemented on %s", runtime.GOOS)
)

// NewUnixServer binds a unix socket at path, mode 0600, and prepares the mux. Every
// connection's peer uid is read as it is accepted, for [SocketPeerGuard]. A socket file left
// behind by a dead listener is removed and bound again; a live one is ErrSocketInUse. Where
// peer credentials cannot be read it binds nothing and returns ErrPeerCredentialsUnsupported.
//
// The socket's directory is the caller's to secure: the file mode alone leaves a window
// between bind and chmod.
func NewUnixServer(path string) (*Server, error) {
	if !peerCredentialsSupported {
		return nil, ErrPeerCredentialsUnsupported
	}
	ln, err := listenUnix(path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("httpx: restrict %s: %w", path, err)
	}
	mux := http.NewServeMux()
	return &Server{
		ln:  ln,
		mux: mux,
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ConnContext: withPeer},
	}, nil
}

// PeerCredentialsSupported reports whether this platform can read a unix socket peer's uid,
// which NewUnixServer requires.
func PeerCredentialsSupported() bool { return peerCredentialsSupported }

func listenUnix(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err == nil {
		return ln, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) && !strings.Contains(err.Error(), "address already in use") {
		return nil, fmt.Errorf("httpx: listen %s: %w", path, err)
	}
	if conn, derr := net.DialTimeout("unix", path, 100*time.Millisecond); derr == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %s", ErrSocketInUse, path)
	}
	if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return nil, fmt.Errorf("httpx: remove stale socket %s: %w", path, rerr)
	}
	ln, err = net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("httpx: listen %s: %w", path, err)
	}
	return ln, nil
}

// Path is the unix socket the server is bound to, or "" for a TCP server.
func (s *Server) Path() string {
	if a, ok := s.ln.Addr().(*net.UnixAddr); ok {
		return a.Name
	}
	return ""
}

// Where names the bound address for a log line: the socket path, or host:port.
func (s *Server) Where() string {
	if p := s.Path(); p != "" {
		return p
	}
	return s.Addr().String()
}

// peer is what the kernel reported about a connection's other end.
type peer struct {
	uid int
	err error
}

type peerKey struct{}

func withPeer(ctx context.Context, c net.Conn) context.Context {
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

// SocketPeerGuard admits a request on a [NewUnixServer] connection whose peer runs as uid, as
// cred, and only when cred's grant allows need; that is the whole authentication, so no
// bearer token is read. A peer of another uid, or one the kernel would not name, is refused
// 403 MGS9022. An admitted request reaches next with cred and the rpc entry point on its
// context, as [BearerGuard] leaves them. It refuses to build with an invalid need.
//
// No DNS-rebind check applies: a browser cannot open a unix socket.
func SocketPeerGuard(format rpcerr.Format, uid int, cred types.Credential, need types.Need, next http.Handler) (http.Handler, error) {
	if err := need.Validate(); err != nil {
		return nil, fmt.Errorf("httpx: %w", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := r.Context().Value(peerKey{}).(peer)
		switch {
		case !ok || p.err != nil:
			format.Write(w, r, peerRefused(fmt.Sprintf("the MCP socket admits only processes running as uid %d, and the kernel did not report this connection's peer", uid)))
			return
		case p.uid != uid:
			format.Write(w, r, peerRefused(fmt.Sprintf("the MCP socket admits only processes running as uid %d; this connection's peer runs as uid %d", uid, p.uid)))
			return
		case !cred.Grant.Allows(need):
			format.Write(w, r, grantBelow(need, cred.Grant))
			return
		}
		ctx := trail.ContextWithEntryPoint(trail.ContextWithCredential(r.Context(), cred), types.EntryPointRPC)
		next.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

func peerRefused(msg string) rpcerr.Error {
	return rpcerr.Error{Code: connect.CodePermissionDenied, Reason: types.SocketPeerNotOwner, Message: msg}
}
