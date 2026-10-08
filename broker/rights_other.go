//go:build !linux && !darwin

package broker

import (
	"io"
	"net"
	"os"
)

// writeWithFiles reports that this platform cannot pass files over the broker's socket.
func writeWithFiles(io.Writer, []byte, []*os.File) error { return errNoFilePassing }

// newConnReader reads c as is: no file arrives beside a frame here.
func newConnReader(c net.Conn) (io.Reader, func() []*os.File) {
	return c, func() []*os.File { return nil }
}
