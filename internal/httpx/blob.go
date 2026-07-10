package httpx

import (
	"context"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// blobMaxWait bounds how long a one-shot server waits for the browser to fetch the blob
// before giving up (e.g. no browser opened). blobGrace is a short window kept open AFTER
// the first fetch so a quick reload still succeeds.
const (
	blobMaxWait = 2 * time.Minute
	blobGrace   = 1500 * time.Millisecond
)

// ServeOutcome reports how a one-shot [BlobServer] ended, so the caller can print an
// accurate message.
type ServeOutcome int

const (
	ServeCompleted ServeOutcome = iota // the page fetched the blob; server stopped after a grace window
	ServeTimedOut                      // nobody fetched within blobMaxWait (browser never opened?)
	ServeCanceled                      // ctx was canceled (Ctrl-C)
)

// BlobServer hands a single blob to a hosted page over a loopback [Server], then STOPS - a
// one-shot handoff, not a standing service. It inherits the server's loopback bind, wraps
// its route in [RequireLoopbackPeer] (defense in depth over the bind) and [CORS] (locked to
// the single site origin). `graph open --serve` uses it.
type BlobServer struct {
	srv    *Server
	path   string
	served chan struct{}
	cancel context.CancelFunc
	done   chan struct{} // closed once the background Serve has fully shut down
}

// StartBlob starts a one-shot loopback server on an ephemeral 127.0.0.1 port that serves raw
// (as contentType) at path, CORS-locked to origin, in the background. The caller hands the
// browser the URL, then calls [BlobServer.WaitServed]. path must begin with "/".
func StartBlob(origin, path, contentType string, raw []byte) (*BlobServer, error) {
	s, err := NewServer(netip.AddrPort{})
	if err != nil {
		return nil, err
	}
	b := &BlobServer{srv: s, path: path, served: make(chan struct{}), done: make(chan struct{})}

	var once sync.Once
	route := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
		once.Do(func() { close(b.served) }) // the page has the blob; begin teardown
	})
	s.Handle(path, RequireLoopbackPeer(CORS(origin)(route)))

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	go func() { defer close(b.done); _ = s.Serve(ctx) }()
	return b, nil
}

// Addr is the loopback "127.0.0.1:PORT" the server bound.
func (b *BlobServer) Addr() string { return b.srv.Addr().String() }

// SourceURL is the loopback URL the page fetches the blob from (http://127.0.0.1:PORT/path).
func (b *BlobServer) SourceURL() string {
	return "http://" + b.Addr() + b.path
}

// WaitServed blocks until the page fetches the blob (then a short grace for a reload), or
// the max wait elapses, or ctx is canceled - and shuts the server down before returning the
// outcome.
func (b *BlobServer) WaitServed(ctx context.Context) ServeOutcome {
	defer b.stop()
	select {
	case <-b.served:
		select {
		case <-time.After(blobGrace):
		case <-ctx.Done():
		}
		return ServeCompleted
	case <-time.After(blobMaxWait):
		return ServeTimedOut
	case <-ctx.Done():
		return ServeCanceled
	}
}

// stop cancels the background Serve (triggering a graceful shutdown) and waits for it to
// finish, so the port is released before WaitServed returns.
func (b *BlobServer) stop() {
	b.cancel()
	<-b.done
}
