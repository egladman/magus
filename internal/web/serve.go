package web

import (
	"context"
	"net/http"
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

// BlobServer hands a single blob to a hosted page over the shared loopback [server], then
// STOPS - a one-shot handoff, not a standing service. It is the static sibling of
// [LiveServer]; both hold a server and inherit its loopback + CORS discipline. `graph open
// --serve` uses it.
type BlobServer struct {
	srv    *server
	path   string
	served chan struct{}
}

// StartBlob starts a one-shot loopback server (per cfg) that serves raw (as contentType) at
// path, CORS-locked to cfg.Origin, in the background. The caller hands the browser the URL,
// then calls [BlobServer.WaitServed]. path must begin with "/".
func StartBlob(cfg Config, path, contentType string, raw []byte) (*BlobServer, error) {
	s, err := newServer(cfg)
	if err != nil {
		return nil, err
	}
	b := &BlobServer{srv: s, path: path, served: make(chan struct{})}

	var once sync.Once
	s.handle(path, "GET, OPTIONS", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
		once.Do(func() { close(b.served) }) // the page has the blob; begin teardown
	})
	s.start()
	return b, nil
}

// Addr is the loopback "127.0.0.1:PORT" the server bound.
func (b *BlobServer) Addr() string { return b.srv.addr() }

// SourceURL is the loopback URL the page fetches the blob from (http://127.0.0.1:PORT/path).
func (b *BlobServer) SourceURL() string {
	return "http://" + b.srv.addr() + b.path
}

// WaitServed blocks until the page fetches the blob (then a short grace for a reload), or
// the max wait elapses, or ctx is canceled - and shuts the server down before returning the
// outcome.
func (b *BlobServer) WaitServed(ctx context.Context) ServeOutcome {
	defer b.srv.stop()
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
