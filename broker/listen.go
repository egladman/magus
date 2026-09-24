package broker

import (
	"context"
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
