// Package endpoint is the parsed-transport-address value type, split out of internal/proc
// as a leaf with no server dependencies. Keeping it separate lets pure consumers (notably
// internal/config's endpoint validator) depend on endpoint parsing without importing the
// server package, whose signal handling (syscall.SIGHUP) does not compile for the Buzz
// playground's js/wasm build.
//
// Every unix socket magus binds or dials goes through Endpoint.Listen and
// Endpoint.Dial, the one place a path longer than sun_path is handled.
package endpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// Endpoint is a parsed transport address. Only "unix" is supported; "tcp" is reserved.
type Endpoint struct {
	Scheme string // "unix" or "tcp"
	Addr   string // filesystem path for unix, "host:port" for tcp
}

// Parse parses a unix:// URL or bare path into an Endpoint.
func Parse(s string) (Endpoint, error) {
	switch {
	case strings.HasPrefix(s, "unix://"):
		path := strings.TrimPrefix(s, "unix://")
		if path == "" {
			return Endpoint{}, fmt.Errorf("endpoint: unix:// requires a non-empty path")
		}
		return Endpoint{Scheme: "unix", Addr: path}, nil

	case strings.HasPrefix(s, "tcp://"):
		return Endpoint{}, fmt.Errorf("endpoint: tcp:// is reserved for future use")

	case strings.Contains(s, "://"):
		scheme, _, _ := strings.Cut(s, "://")
		return Endpoint{}, fmt.Errorf("endpoint: unsupported scheme %q", scheme)

	case s != "":
		return Endpoint{Scheme: "unix", Addr: s}, nil // bare path: back-compat

	default:
		return Endpoint{}, fmt.Errorf("endpoint: empty address")
	}
}

// String returns the canonical unix:// URL form.
func (e Endpoint) String() string {
	return e.Scheme + "://" + e.Addr
}

// Network returns the network name expected by net.Listen / net.Dial.
func (e Endpoint) Network() string { return e.Scheme }

// Listen opens a listener on the endpoint address. A path longer than the platform's
// sun_path is bound all the same on linux (see unixName) and refused on darwin; either
// way the listener reports, and on Close removes, the socket at Addr.
func (e Endpoint) Listen() (net.Listener, error) {
	if e.Scheme != "unix" {
		return nil, fmt.Errorf("endpoint: listen: unsupported scheme %q", e.Scheme)
	}
	name, done, err := unixName(e.Addr)
	if err != nil {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), e.Scheme, name)
	done()
	if err != nil || name == e.Addr {
		return ln, realAddr(err, e.Addr)
	}
	ul, ok := ln.(*net.UnixListener)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("endpoint: listen %s: got a %T, not a unix listener", e.Addr, ln)
	}
	// The listener would unlink the name it bound, which named a descriptor now closed.
	ul.SetUnlinkOnClose(false)
	return &longListener{UnixListener: ul, addr: &net.UnixAddr{Name: e.Addr, Net: e.Scheme}}, nil
}

// Dial connects to the endpoint address, reaching a path longer than sun_path as Listen
// binds one.
func (e Endpoint) Dial(ctx context.Context) (net.Conn, error) {
	if e.Scheme != "unix" {
		return nil, fmt.Errorf("endpoint: dial: unsupported scheme %q", e.Scheme)
	}
	name, done, err := unixName(e.Addr)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, e.Scheme, name)
	done()
	return conn, realAddr(err, e.Addr)
}

// realAddr names path, not the name it was bound or dialed by, in err.
func realAddr(err error, path string) error {
	var op *net.OpError
	if errors.As(err, &op) {
		op.Addr = &net.UnixAddr{Name: path, Net: "unix"}
	}
	return err
}

// longListener is a listener bound through unixName's short name, reporting and
// removing the socket at its real path.
type longListener struct {
	*net.UnixListener
	addr *net.UnixAddr
}

func (l *longListener) Addr() net.Addr { return l.addr }

func (l *longListener) Close() error {
	err := l.UnixListener.Close()
	_ = os.Remove(l.addr.Name)
	return err
}
