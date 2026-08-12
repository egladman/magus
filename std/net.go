//go:build !wasm

package std

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module net -lang buzz -out ../internal/interp/bindings/gen/net.go

func init() { Register(Net) }

// netDefaultWaitMs bounds wait_for_port when the caller names no timeout. Thirty
// seconds is long enough for a dev server or a database container to come up and
// short enough that a wedged one is reported inside a coffee break rather than
// occupying a CI runner until the job's own timeout fires.
const netDefaultWaitMs = 30_000

// netDialTimeout bounds ONE connection attempt inside the wait loop. Without it a
// host that accepts the SYN and then goes silent - a firewall that drops rather
// than rejects is the usual cause - would hang the attempt for the OS default,
// which on Linux is over two minutes, blowing past the caller's own timeout.
const netDialTimeout = 2 * time.Second

// netPollInterval is the pause between attempts. Short enough that a service
// coming up is noticed promptly, long enough not to spin.
const netPollInterval = 100 * time.Millisecond

// Net is the "net" host module: the two questions a build actually asks about
// TCP, and nothing else.
//
// It is NOT a networking library. There is no listener, no client, no protocol -
// http already covers requests. What was missing is the pair of things every
// target that starts a service needs and had to shell out for:
//
//   - "is it up yet?" - a dev server, a database container, a mock backend. The
//     shell form is a `until nc -z localhost 3000; do sleep 0.1; done` loop, which
//     is unportable (nc's flags differ across BSD, GNU and busybox), silently
//     infinite when the service never starts, and invisible to magus.
//   - "give me a port nobody is using" - so two targets running in parallel, or
//     two workspaces on one machine, do not collide on a hardcoded 3000.
var Net = Module{
	Name: "net",
	Doc:  "TCP readiness and port allocation: wait for a service to accept connections, and find a free port.",
	Methods: []Method{
		{
			Name: "wait_for_port",
			Doc:  "Block until a TCP connection to host:port succeeds, then return true; return false when the timeout elapses first. Use it after starting a dev server or a container instead of sleeping a guessed number of seconds. timeout_ms defaults to 30000. Returns FALSE rather than raising on timeout, so a caller can fall back or report its own message; the run's cancellation is honored, so Ctrl-C does not wait out the timeout.",
			Args: []Arg{
				{Name: "host", Type: TypeString},
				{Name: "port", Type: TypeInt},
				{Name: "timeout_ms", Type: TypeInt, Optional: true},
			},
			Returns: []Ret{{Type: TypeBool}},
			Raises:  true,
			Impl:    NetWaitForPort,
		},
		{
			Name: "is_port_open",
			Doc:  "Report whether a TCP connection to host:port succeeds right now, with no waiting. The single-shot form of wait_for_port - for deciding whether a service is ALREADY running before starting another one.",
			Args: []Arg{
				{Name: "host", Type: TypeString},
				{Name: "port", Type: TypeInt},
			},
			Returns: []Ret{{Type: TypeBool}},
			Raises:  true,
			Impl:    NetIsPortOpen,
		},
		{
			Name:    "free_port",
			Doc:     "Ask the operating system for an unused TCP port and return it. Bind it promptly: the port is released before this returns, so between the call and your server's own bind another process could take it. That race is unavoidable for any \"find a free port\" answer and is why this is for choosing a dev-server port, not for anything that must not collide.",
			Returns: []Ret{{Type: TypeInt}},
			Raises:  true,
			Impl:    NetFreePort,
		},
	},
}

// netAddr renders host:port, defaulting an empty host to localhost.
//
// Localhost rather than all-interfaces: a magusfile asking "is it up" means the
// service it just started on this machine, and an empty host reaching 0.0.0.0
// would be a different, wider question than the caller asked.
func netAddr(host string, port int) string {
	if host == "" {
		host = "localhost"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// NetIsPortOpen reports whether host:port accepts a connection right now.
func NetIsPortOpen(ctx context.Context, host string, port int) (bool, error) {
	if types.Tracing(ctx) {
		return false, nil
	}
	if port <= 0 || port > 65535 {
		return false, fmt.Errorf("net.is_port_open: port %d is out of range (1-65535)", port)
	}
	return netDialOnce(ctx, netAddr(host, port)), nil
}

// netDialOnce makes one bounded connection attempt, reporting only whether it
// succeeded. The error is deliberately dropped: every failure mode here -
// refused, unreachable, timed out - is the same answer to "is it up", and
// surfacing them separately would invite a caller to branch on text.
func netDialOnce(ctx context.Context, addr string) bool {
	d := net.Dialer{Timeout: netDialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// NetWaitForPort blocks until host:port accepts a connection or the timeout
// elapses.
func NetWaitForPort(ctx context.Context, host string, port, timeoutMs int) (bool, error) {
	if types.Tracing(ctx) {
		// Dry run: report ready without waiting. A record pass must not spend the
		// timeout on a service it never started.
		return true, nil
	}
	if port <= 0 || port > 65535 {
		return false, fmt.Errorf("net.wait_for_port: port %d is out of range (1-65535)", port)
	}
	if timeoutMs <= 0 {
		timeoutMs = netDefaultWaitMs
	}
	addr := netAddr(host, port)

	// Bound the whole wait, not each attempt, and derive it from the run's context
	// so an interrupted run stops waiting immediately instead of sitting out the
	// remaining timeout.
	deadline, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	for {
		if netDialOnce(deadline, addr) {
			return true, nil
		}
		select {
		case <-deadline.Done():
			// Timed out, or the run was cancelled. Both mean "not ready", which is
			// the question asked; a cancelled run is ended by its own machinery.
			return false, nil
		case <-time.After(netPollInterval):
		}
	}
}

// NetFreePort returns a TCP port the OS reports as unused.
//
// Port 0 asks the kernel to assign one, which is the only answer that is not a
// guess: scanning upward from a base port races every other process doing the
// same thing, and magus already has one such scan in http.server.
func NetFreePort(ctx context.Context) (int, error) {
	if types.Tracing(ctx) {
		// A plausible high port, created nothing. Matches fs.temp_dir's dry-run shape.
		return 49152, nil
	}
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, fmt.Errorf("net.free_port: %w", err)
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("net.free_port: listener address is not TCP")
	}
	return addr.Port, nil
}
