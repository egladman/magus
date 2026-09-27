package endpoint

import (
	"context"
	"fmt"
	"net"
)

// ListenUnix binds a *net.UnixListener at path, reaching one longer than the
// platform's sun_path the way Endpoint.Listen does. Use it in place of
// net.Listen("unix", path) when the caller needs the concrete *net.UnixListener
// (to duplicate its descriptor, or to drive SetUnlinkOnClose itself): Endpoint.Listen
// hides that behind net.Listener once it wraps a long path.
func ListenUnix(path string) (*net.UnixListener, error) {
	name, done, err := unixName(path)
	if err != nil {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
	done()
	if err != nil {
		return nil, realAddr(err, path)
	}
	if name != path {
		// name's directory descriptor is already closed by done; the default
		// unlink-on-close would resolve it against whatever now holds that
		// descriptor number, not this socket.
		ln.SetUnlinkOnClose(false)
	}
	return ln, nil
}

// ListenUnixgram binds a *net.UnixConn in datagram mode at path, reaching one longer
// than sun_path the same way ListenUnix does.
func ListenUnixgram(path string) (*net.UnixConn, error) {
	name, done, err := unixName(path)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
	done()
	if err != nil {
		return nil, realAddr(err, path)
	}
	return conn, nil
}

// DialUnix connects to a *net.UnixConn at path, reaching one longer than sun_path the
// way Endpoint.Dial does.
func DialUnix(ctx context.Context, path string) (*net.UnixConn, error) {
	name, done, err := unixName(path)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", name)
	done()
	if err != nil {
		return nil, realAddr(err, path)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("endpoint: dial %s: got a %T, not a unix connection", path, conn)
	}
	return uc, nil
}
