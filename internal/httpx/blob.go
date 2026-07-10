package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
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
// one-shot handoff, not a standing service. It inherits the server's loopback bind and wraps
// its route in the same stack as every other loopback endpoint: [RequireLoopbackPeer]
// (defense in depth over the bind), [CORS] (locked to the single site origin), and
// [BearerGuard] with a per-run random token. `graph open --serve` uses it.
type BlobServer struct {
	srv    *Server
	path   string
	token  string
	served chan struct{}
	cancel context.CancelFunc
	done   chan struct{} // closed once the background Serve has fully shut down
}

// StartBlob starts a one-shot loopback server on an ephemeral 127.0.0.1 port that serves raw
// (as contentType) at path, CORS-locked to origin and gated by a per-run bearer token, in the
// background. The caller hands the browser [BlobServer.SourceURL] (which carries the token as
// a `?token=` query param), then calls [BlobServer.WaitServed]. path must begin with "/".
func StartBlob(origin, path, contentType string, raw []byte) (*BlobServer, error) {
	s, err := NewServer(netip.AddrPort{})
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("httpx: mint blob token: %w", err)
	}
	b := &BlobServer{srv: s, path: path, token: hex.EncodeToString(tokenBytes), served: make(chan struct{}), done: make(chan struct{})}

	var once sync.Once
	route := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
		once.Do(func() { close(b.served) }) // the page has the blob; begin teardown
	})
	tokenFn := func() (string, error) { return b.token, nil }
	s.Handle(path, RequireLoopbackPeer(CORS(origin)(BearerGuardWithQueryToken(SingleTokenVerifier(tokenFn), route))))

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	go func() { defer close(b.done); _ = s.Serve(ctx) }()
	return b, nil
}

// Addr is the loopback "127.0.0.1:PORT" the server bound.
func (b *BlobServer) Addr() string { return b.srv.Addr().String() }

// Token is the per-run bearer token the page must present to fetch the blob.
func (b *BlobServer) Token() string { return b.token }

// SourceURL is the loopback URL the page fetches the blob from, with the per-run token in a
// `?token=` query param (http://127.0.0.1:PORT/path?token=...). The query param carrier lets
// a plain fetch() authenticate without a preflight-triggering Authorization header, and it
// survives being tucked into the explorer's `#src=` fragment.
func (b *BlobServer) SourceURL() string {
	return "http://" + b.Addr() + b.path + "?token=" + url.QueryEscape(b.token)
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
