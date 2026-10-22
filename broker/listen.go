package broker

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/internal/proc/sockdir"
)

// SocketName is the broker socket's file name in the per-user runtime directory.
const SocketName = "broker.sock"

// ErrRunning is Listen finding a live broker already bound to the address. It is the
// expected answer when two runs race to start one: the loser exits quietly.
var ErrRunning = errors.New("broker: a broker is already serving this socket")

// DefaultAddr is where this user's broker listens: broker.sock in the private (0700)
// runtime directory magus keeps its sockets in.
func DefaultAddr() string {
	return "unix://" + filepath.Join(sockdir.Dir(), SocketName)
}

// Listen binds the broker socket at addr, a unix:// URL or a bare path. The bind is the
// only lock a broker takes: when a live broker holds addr it returns ErrRunning, and a
// socket file left behind by a dead one is removed and bound again.
func Listen(ctx context.Context, addr string) (net.Listener, error) {
	ep, err := endpoint.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("broker: %w", err)
	}
	ln, err := ep.Listen()
	if err == nil {
		return ln, nil
	}
	if !addrInUse(err) {
		return nil, fmt.Errorf("broker: listen %s: %w", ep, err)
	}
	if Live(ctx, addr) {
		return nil, ErrRunning
	}
	_ = os.Remove(ep.Addr)
	ln, err = ep.Listen()
	if addrInUse(err) {
		return nil, ErrRunning
	}
	if err != nil {
		return nil, fmt.Errorf("broker: listen %s: %w", ep, err)
	}
	return ln, nil
}

// addrInUse matches the platform's "address already in use", which Windows spells as a
// WSA error that syscall.EADDRINUSE does not match.
func addrInUse(err error) bool {
	return err != nil && (errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use"))
}

func tokenPath(sock string) string { return sock + ".token" }

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("broker: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// writeToken replaces any file a crashed broker left; O_EXCL after the remove refuses to
// follow anything planted at the path in between. Serve calls it, not Listen, so a
// socket a supervisor bound carries a token too.
func writeToken(sock, token string) error {
	path := tokenPath(sock)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("broker: replace token %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("broker: write token: %w", err)
	}
	_, werr := f.WriteString(token)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("broker: write token %s: %w", path, werr)
	}
	return nil
}

func readToken(sock string) string {
	b, err := os.ReadFile(tokenPath(sock))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func tokenMatches(want, got string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// Live reports whether something accepts connections on addr, within 100ms.
func Live(ctx context.Context, addr string) bool {
	ep, err := endpoint.Parse(addr)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	conn, err := ep.Dial(ctx)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
