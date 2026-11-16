package difftui

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// heldDiffSession is the server's /api/v1/diff/session route, holding every request until the
// test lets go.
//
// The holding is what makes the queue observable at all: a server that answers empties the
// queue as fast as a keyboard can fill it, and every decision in Bridge is about the case
// where one has stopped.
type heldDiffSession struct {
	srv     *httptest.Server
	started chan struct{} // closed once the first request has reached the handler
	release chan struct{} // closed to let every held request return

	startOnce   sync.Once
	releaseOnce sync.Once

	mu  sync.Mutex
	got []sessionOp
}

func newHeldDiffSession(t *testing.T) *heldDiffSession {
	t.Helper()
	s := &heldDiffSession{started: make(chan struct{}), release: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op sessionOp
		if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.got = append(s.got, op)
		s.mu.Unlock()
		s.startOnce.Do(func() { close(s.started) })
		<-s.release
	}))
	// Released before the server is shut down whatever the test did: Close waits for the
	// handlers, so a test that never unblocks would hang here rather than fail.
	t.Cleanup(func() {
		s.unblock()
		s.srv.Close()
	})
	return s
}

// unblock lets every held request return. Safe to call twice, because the cleanup calls it
// again after a test already has.
func (s *heldDiffSession) unblock() { s.releaseOnce.Do(func() { close(s.release) }) }

// bridge is a Bridge pointed at the held session, skipping the attach round trip the queue has no
// part in.
func (s *heldDiffSession) bridge() *Bridge {
	b := &Bridge{addr: strings.TrimPrefix(s.srv.URL, "http://"), token: types.NewSecret("held")}
	b.start()
	return b
}

func (s *heldDiffSession) ops() []sessionOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sessionOp(nil), s.got...)
}

func cursorHunks(ops []sessionOp) []int {
	var out []int
	for _, op := range ops {
		if op.Op == "cursor" {
			out = append(out, op.Hunk)
		}
	}
	return out
}

func viewedDigests(ops []sessionOp) []string {
	var out []string
	for _, op := range ops {
		if op.Op == "viewed" {
			out = append(out, op.Digest)
		}
	}
	return out
}

// TestDiffBridgeQueueKeepsTheNewestCursor pins which end of a full queue is dropped.
//
// A backlog means the server stopped keeping up, and the only cursor still worth sending is the
// last one: every entry behind it names somewhere the reader has already walked past. A plain
// non-blocking send drops the arriving op, which keeps exactly the stale ones and throws away
// the only true one.
func TestDiffBridgeQueueKeepsTheNewestCursor(t *testing.T) {
	held := newHeldDiffSession(t)
	b := held.bridge()

	// The first op is taken by the sender and held in the handler, which is what makes the rest
	// deterministic: nothing is receiving from the queue while they are pushed.
	b.SetCursor(diff.Cursor{Path: "a.go", Hunk: 0})
	<-held.started

	const pushed = 20
	for i := 1; i <= pushed; i++ {
		b.SetCursor(diff.Cursor{Path: "a.go", Hunk: i})
	}
	held.unblock()
	b.Close()

	want := []int{0}
	for i := pushed - bridgeQueue + 1; i <= pushed; i++ {
		want = append(want, i)
	}
	assert.Equal(t, want, cursorHunks(held.ops()),
		"the one in flight, then the newest bridgeQueue: the stale head is what gets evicted")
}

// TestDiffBridgeNeverDropsAReadMark pins the asymmetry the two queues exist for.
//
// A cursor is replaceable by the next one. A read mark is replaceable by nothing: the bridge is
// the only writer of it on this path, so a dropped `viewed` is a hunk the reader read that no
// client will ever be told about. Sharing one evicting queue made a fast reader's marks
// collateral damage of their own scrolling.
func TestDiffBridgeNeverDropsAReadMark(t *testing.T) {
	held := newHeldDiffSession(t)
	b := held.bridge()

	b.SetCursor(diff.Cursor{Path: "a.go", Hunk: 0})
	<-held.started

	// Far more cursor moves than the cursor queue holds, with marks interleaved: the overflow
	// that evicts one must not be able to reach the other.
	var want []string
	for i := 0; i < 40; i++ {
		b.SetCursor(diff.Cursor{Path: "a.go", Hunk: i + 1})
		if i%8 == 0 {
			d := fmt.Sprintf("digest-%d", i)
			want = append(want, d)
			b.SetViewed(d, true)
		}
	}
	held.unblock()
	b.Close()

	assert.Equal(t, want, viewedDigests(held.ops()),
		"every mark, in order, however far behind the sender fell")
}

// TestDiffBridgeCloseDrainsAndTheSenderExits pins the quit path: a mark made on the last
// keypress leaves, and the goroutine that carried it is gone by the time close returns.
func TestDiffBridgeCloseDrainsAndTheSenderExits(t *testing.T) {
	held := newHeldDiffSession(t)
	b := held.bridge()

	b.SetViewed("d0", true)
	<-held.started
	b.SetViewed("d1", true)
	b.SetViewed("d2", false)
	held.unblock()

	b.Close()
	select {
	case <-b.done:
	default:
		t.Fatal("the sender goroutine is still running after close returned")
	}
	assert.Equal(t, []string{"d0", "d1", "d2"}, viewedDigests(held.ops()),
		"a mark made on the last keypress is not lost to the process exiting")
}

// TestDiffBridgeCloseGivesUpOnAWedgedServer is the other half of the same bargain: quitting is
// bounded whether or not anything answers, and the sender still exits.
//
// Nothing releases the handler here, so the post is on the wire for the whole test. Cancelling
// it is the only thing that ends the goroutine; abandoning it left one talking to the server
// about a session the reader had walked away from, with nobody to receive the answer.
func TestDiffBridgeCloseGivesUpOnAWedgedServer(t *testing.T) {
	held := newHeldDiffSession(t)
	b := held.bridge()

	b.SetViewed("d0", true)
	<-held.started
	b.SetViewed("d1", true)

	start := time.Now()
	b.Close()
	assert.Less(t, time.Since(start), bridgeClose+2*bridgeWrite,
		"a wedged server must not hold the shell")

	select {
	case <-b.done:
	case <-time.After(2 * bridgeWrite):
		t.Fatal("the sender goroutine outlived close")
	}
}

// TestDiffBridgeSendAfterCloseIsSafe pins why stop is closed instead of the queues: closing a
// channel the key loop can still send on turns a late keypress into a panic.
func TestDiffBridgeSendAfterCloseIsSafe(t *testing.T) {
	held := newHeldDiffSession(t)
	held.unblock() // an answering server; this is about the queue, not the wire
	b := held.bridge()

	b.SetViewed("d0", true)
	b.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.SetCursor(diff.Cursor{Path: "a.go", Hunk: 1})
		b.SetViewed("d1", true)
	}()
	select {
	case <-done:
	case <-time.After(2 * bridgeWrite):
		t.Fatal("a send after close blocked")
	}
}

func TestDialDiffBridgeSaysWhyItDidNotJoin(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	token, err := auth.GenerateOperator()
	require.NoError(t, err)
	_, err = auth.SaveOperator(token)
	require.NoError(t, err)

	cases := []struct {
		name    string
		handler http.HandlerFunc
		warning string
	}{
		{"refused status", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusForbidden) }, "refused the session"},
		{"undecodable session", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }, "did not decode"},
		{"declined as-of", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"as_of":"other"}`)) }, "different patch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			t.Cleanup(srv.Close)
			addr := strings.TrimPrefix(srv.URL, "http://")
			var got *Bridge
			logged := captureWarnings(t, func() { got = DialBridge(context.Background(), storeTokens{}, addr, nil, "digest") })

			assert.Nil(t, got)
			assert.Contains(t, logged, c.warning)
		})
	}

	t.Run("no server stays silent", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		addr := strings.TrimPrefix(srv.URL, "http://")
		srv.Close()
		var got *Bridge
		logged := captureWarnings(t, func() { got = DialBridge(context.Background(), storeTokens{}, addr, nil, "digest") })

		assert.Nil(t, got)
		assert.Empty(t, logged)
	})
}

func TestDialDiffBridgeWarnsOnATokenThatCannotBeMinted(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	require.NoError(t, os.MkdirAll(filepath.Join(state, "magus"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(state, "magus", "tokens.d"), []byte("a file\n"), 0o600))
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	var got *Bridge
	logged := captureWarnings(t, func() { got = DialBridge(context.Background(), storeTokens{}, addr, nil, "digest") })

	assert.Nil(t, got)
	assert.Contains(t, logged, "could not be minted")
}

func TestDialDiffBridgeCarriesAConsoleTokenAndRevokesIt(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var presented string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, _ = w.Write([]byte(`{"as_of":"digest"}`))
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")

	b := DialBridge(context.Background(), storeTokens{}, addr, nil, "digest")
	require.NotNil(t, b)
	cred, ok := auth.Verify(presented)
	require.True(t, ok, "the token is live while the bridge is open")
	cred.ID, cred.Name = "", ""
	assert.Equal(t, job.Credential{Kind: job.KindStored, Grant: job.AccessConsole}, cred)

	b.Close()
	_, ok = auth.Verify(presented)
	assert.False(t, ok, "close revokes it")
}

// storeTokens is the token store the way the shell opens it: the operator mints and revokes
// against the state directory, which the tests point at a temp dir.
type storeTokens struct{}

func (storeTokens) open() (*auth.Store, error) {
	dir, err := auth.StoreDir()
	if err != nil {
		return nil, err
	}
	return auth.LoadStore(dir)
}

func (s storeTokens) Mint(req auth.MintRequest) (string, auth.Token, error) {
	store, err := s.open()
	if err != nil {
		return "", auth.Token{}, err
	}
	secret, rec, err := store.Mint(job.AccessOperator, req)
	if err != nil {
		return "", auth.Token{}, err
	}
	return secret.Reveal(), rec, nil
}

func (s storeTokens) Revoke(id string) error {
	store, err := s.open()
	if err != nil {
		return err
	}
	_, err = store.Revoke(job.AccessOperator, id)
	return err
}

// captureWarnings installs a slog handler for the duration of fn and returns what it logged.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}
