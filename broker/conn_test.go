package broker

import (
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// lateWriter returns from a write only once reads have hit the peer's close, so the
// caller waits with its reply and the connection's death both already delivered.
type lateWriter struct {
	net.Conn
	once sync.Once
	eof  chan struct{}
}

func (c *lateWriter) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		c.once.Do(func() { close(c.eof) })
	}
	return n, err
}

func (c *lateWriter) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	<-c.eof
	return n, err
}

// A peer that answers and then hangs up, the way the broker answers a shutdown, must
// still deliver its answer.
func TestCallKeepsAReplyThatArrivedBeforeTheClose(t *testing.T) {
	for range 200 {
		client, peer := net.Pipe()
		go func() {
			f, err := newFrameReader(peer).read()
			if err == nil {
				_ = (&frameWriter{w: peer}).write(typeShutdownReply, f.ID, nil)
			}
			_ = peer.Close()
		}()
		cn := newConn(&lateWriter{Conn: client, eof: make(chan struct{})}, nil)
		f, err := cn.call(t.Context(), 1, typeShutdown, nil, nil)
		require.NoError(t, err)
		require.Equal(t, typeShutdownReply, f.Type)
	}
}
