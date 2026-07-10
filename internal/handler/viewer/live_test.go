package viewer

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveServerStreamsBacklogAndLive connects to a running live server, and confirms it
// replays a pre-subscribe backlog event, streams a live event, CORS-locks to the
// origin, and ends with a `done` event when the broadcaster closes.
// emit sends one event to bc through the real capture path (a logger fanning to it).
func emit(bc *journal.Broadcaster, ev journal.Event) {
	journal.Emit(journal.WithLogger(context.Background(), journal.NewLogger(bc)), ev)
}

func TestLiveServerStreamsBacklogAndLive(t *testing.T) {
	bc := journal.NewBroadcaster()
	emit(bc, journal.Event{Kind: journal.KindOutput, Text: "backlog-line"})

	ls, err := StartLive("https://example.test", bc)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer ls.Stop(ctx)

	req, _ := http.NewRequest(http.MethodGet, "http://"+ls.Addr()+"/events?token="+ls.Token(), nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "https://example.test", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	sc := bufio.NewScanner(resp.Body)
	texts := make(chan string, 8)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: done") {
				texts <- "__done__"
				return
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				raw, decErr := base64.StdEncoding.DecodeString(data)
				if decErr != nil {
					continue
				}
				var ev viewerv1.Event
				if proto.Unmarshal(raw, &ev) == nil {
					texts <- ev.GetText()
				}
			}
		}
	}()

	assert.Equal(t, "backlog-line", recv(t, texts))

	emit(bc, journal.Event{Kind: journal.KindOutput, Text: "live-line"})
	assert.Equal(t, "live-line", recv(t, texts))

	bc.Close()
	assert.Equal(t, "__done__", recv(t, texts))
}

// TestLiveServerRejectsBadToken confirms a wrong/absent token is rejected by the shared
// bearer guard with a 401 challenge (the guard treats missing and wrong tokens alike).
func TestLiveServerRejectsBadToken(t *testing.T) {
	bc := journal.NewBroadcaster()
	ls, err := StartLive("https://example.test", bc)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer ls.Stop(ctx)

	resp, err := http.Get("http://" + ls.Addr() + "/events?token=wrong")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("WWW-Authenticate"))
}

// TestLiveServerViewerURL confirms the viewer link carries the loopback addr and token in the
// fragment.
func TestLiveServerViewerURL(t *testing.T) {
	bc := journal.NewBroadcaster()
	ls, err := StartLive("https://example.test", bc)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer ls.Stop(ctx)

	u := ls.ViewerURL("https://eli.gladman.cc/magus/logs/")
	// Both the loopback host and the bearer token ride the fragment - nothing in the query.
	assert.True(t, strings.HasPrefix(u, "https://eli.gladman.cc/magus/logs/#live="), u)
	before, after, found := strings.Cut(u, "#")
	require.True(t, found, "live url must have a fragment")
	assert.NotContains(t, before, "?", "nothing must ride the query string")
	assert.Contains(t, after, "live=", "host must live in the fragment")
	assert.Contains(t, after, "token="+ls.Token(), "token must live in the fragment")
}

// nonFlusherRecorder is an http.ResponseWriter that is deliberately NOT an http.Flusher, so
// streamEvents takes its "streaming unsupported" branch.
type nonFlusherRecorder struct {
	header http.Header
	code   int
}

func (n *nonFlusherRecorder) Header() http.Header         { return n.header }
func (n *nonFlusherRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (n *nonFlusherRecorder) WriteHeader(code int)        { n.code = code }

// TestStreamEventsRejectsNonFlusher confirms a ResponseWriter that cannot flush is turned away
// with 500 before any subscription happens.
func TestStreamEventsRejectsNonFlusher(t *testing.T) {
	ls := &LiveServer{bc: journal.NewBroadcaster()}
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	w := &nonFlusherRecorder{header: make(http.Header)}
	ls.streamEvents(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.code)
}

// flushErrWriter is a Flusher whose Write always fails, so streamEvents aborts on its first
// backlog write.
type flushErrWriter struct{ header http.Header }

func (f *flushErrWriter) Header() http.Header       { return f.header }
func (f *flushErrWriter) WriteHeader(int)           {}
func (f *flushErrWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (f *flushErrWriter) Flush()                    {}

// TestStreamEventsAbortsOnBacklogWriteError confirms streamEvents returns early (does not hang)
// when writing a backlog event fails - e.g. the browser hung up mid-replay.
func TestStreamEventsAbortsOnBacklogWriteError(t *testing.T) {
	bc := journal.NewBroadcaster()
	emit(bc, journal.Event{Kind: journal.KindOutput, Text: "backlog"})
	ls := &LiveServer{bc: bc}
	req := httptest.NewRequest(http.MethodGet, "/events", nil)

	done := make(chan struct{})
	go func() { defer close(done); ls.streamEvents(&flushErrWriter{header: make(http.Header)}, req) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamEvents did not return after a backlog write error")
	}
}

// errWriter fails every Write so writeEvent surfaces the write error to its caller.
type errWriter struct{ header http.Header }

func (e *errWriter) Header() http.Header       { return e.header }
func (e *errWriter) WriteHeader(int)           {}
func (e *errWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestWriteEventPropagatesWriteError confirms a failed write is returned so the stream loop can
// end; a healthy writer returns nil.
func TestWriteEventPropagatesWriteError(t *testing.T) {
	err := writeEvent(&errWriter{header: make(http.Header)}, journal.Event{Kind: journal.KindOutput, Text: "x"})
	require.Error(t, err)

	var ok okWriter
	require.NoError(t, writeEvent(&ok, journal.Event{Kind: journal.KindOutput, Text: "x"}))
}

// okWriter accepts every write.
type okWriter struct{}

func (okWriter) Header() http.Header         { return http.Header{} }
func (okWriter) WriteHeader(int)             {}
func (okWriter) Write(b []byte) (int, error) { return len(b), nil }

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE line")
		return ""
	}
}
