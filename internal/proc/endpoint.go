package proc

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// Endpoint is a parsed transport address. Only "unix" is supported; "tcp" is reserved.
type Endpoint struct {
	Scheme string // "unix" or "tcp"
	Addr   string // filesystem path for unix, "host:port" for tcp
}

// ParseEndpoint parses a unix:// URL or bare path into an Endpoint.
func ParseEndpoint(s string) (Endpoint, error) {
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

// Listen opens a listener on the endpoint address.
func (e Endpoint) Listen() (net.Listener, error) {
	if e.Scheme != "unix" {
		return nil, fmt.Errorf("endpoint: listen: unsupported scheme %q", e.Scheme)
	}
	var lc net.ListenConfig
	return lc.Listen(context.Background(), e.Scheme, e.Addr)
}

// Dial connects to the endpoint address.
func (e Endpoint) Dial(ctx context.Context) (net.Conn, error) {
	if e.Scheme != "unix" {
		return nil, fmt.Errorf("endpoint: dial: unsupported scheme %q", e.Scheme)
	}
	return (&net.Dialer{}).DialContext(ctx, e.Scheme, e.Addr)
}
