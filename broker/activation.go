package broker

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/proc/endpoint"
)

// The variables of systemd's socket-activation protocol (sd_listen_fds(3)). The first
// passed descriptor is always 3.
const (
	envListenPID     = "LISTEN_PID"
	envListenFDs     = "LISTEN_FDS"
	envListenFDNames = "LISTEN_FDNAMES"
	listenFDsStart   = 3
)

// Activated returns the listening socket a supervisor handed this process through
// systemd's socket-activation protocol, or nil when it was handed none. A supervisor
// handing over a socket is telling the broker where to serve, so what it hands over is
// checked strictly: a malformed variable, more than one descriptor, a descriptor that is
// not a listening unix stream socket, or a socket bound anywhere but addr (where every
// run dials) is an error, never a reason to bind a socket itself.
//
// LISTEN_PID naming another process means the variables were inherited and are not for
// this one; they are ignored. Otherwise Activated clears them, so a service the broker
// starts does not inherit them.
//
// launchd hands sockets over only through launch_activate_socket, a C call magus does
// not make. Under launchd the broker runs from a KeepAlive agent and binds its own
// socket instead (see `magus broker units launchd`).
func Activated(addr string) (net.Listener, error) {
	n, err := listenFDs(os.LookupEnv, os.Getpid())
	if err != nil || n == 0 {
		return nil, err
	}
	for _, k := range []string{envListenPID, envListenFDs, envListenFDNames} {
		_ = os.Unsetenv(k)
	}
	ln, err := adoptListener(listenFDsStart)
	if err != nil {
		return nil, err
	}
	if err := servesAddr(ln, addr); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// listenFDs is how many descriptors the protocol handed to the process pid: 0 when none
// were, 1 when the one socket the broker serves was, and an error for anything else.
func listenFDs(lookup func(string) (string, bool), pid int) (int, error) {
	pidVal, hasPID := lookup(envListenPID)
	fdsVal, hasFDs := lookup(envListenFDs)
	if !hasPID && !hasFDs {
		return 0, nil
	}
	if hasPID != hasFDs {
		return 0, fmt.Errorf("broker: socket activation: %s and %s come together; only one is set", envListenPID, envListenFDs)
	}
	target, err := strconv.Atoi(pidVal)
	if err != nil || target <= 0 {
		return 0, fmt.Errorf("broker: socket activation: %s=%q is not a process id", envListenPID, pidVal)
	}
	if target != pid {
		return 0, nil
	}
	n, err := strconv.Atoi(fdsVal)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("broker: socket activation: %s=%q is not a positive count", envListenFDs, fdsVal)
	}
	if n != 1 {
		return 0, fmt.Errorf("broker: socket activation: the supervisor passed %d sockets; the broker serves exactly one", n)
	}
	if names, ok := lookup(envListenFDNames); ok && strings.Contains(names, ":") {
		return 0, fmt.Errorf("broker: socket activation: %s=%q names more than the one socket passed", envListenFDNames, names)
	}
	return n, nil
}

// servesAddr refuses a socket bound anywhere but addr: runs dial addr, so a broker on
// another path holds capacity nobody asks for.
func servesAddr(ln net.Listener, addr string) error {
	ep, err := endpoint.Parse(addr)
	if err != nil {
		return fmt.Errorf("broker: %w", err)
	}
	got := ln.Addr().String()
	if samePath(got, ep.Addr) {
		return nil
	}
	return fmt.Errorf("broker: socket activation: the supervisor's socket is %s, but runs dial %s; point the unit's ListenStream there", got, ep.Addr)
}

// samePath compares socket paths through a symlinked directory (macOS's /var is
// /private/var), which a socket file itself cannot be resolved through.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	da, errA := filepath.EvalSymlinks(filepath.Dir(a))
	db, errB := filepath.EvalSymlinks(filepath.Dir(b))
	return errA == nil && errB == nil && da == db && filepath.Base(a) == filepath.Base(b)
}
