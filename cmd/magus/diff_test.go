package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/diff"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// TestDiffTUIRefusalMatrix pins every way --tui can be asked for something it cannot do.
//
// Each of these has a plausible reading that would "work" and produce a lie: a viewport over
// a patch file coordinating a session about a working tree nobody is editing, two loops
// fighting for one terminal, or -o json answered with a picture. A refusal that names the
// conflicting flag is the whole point, so the message is asserted, not just the failure.
func TestDiffTUIRefusalMatrix(t *testing.T) {
	workingTree := diffInput{kind: inputWorkingTree, label: "the working tree"}
	stdin := diffInput{kind: inputStdin, label: "a patch on stdin"}
	patchFile := diffInput{kind: inputFile, path: "x.patch", label: "the patch in x.patch"}
	// The viewer needs BOTH descriptors, which is why the gate carries them separately: it
	// reads stdin and paints stdout.
	atTerminal := diffTUITerm{Reads: true, Paints: true}

	tests := []struct {
		name       string
		flags      gen.DiffFlags
		src        diffInput
		format     Format
		term       diffTUITerm
		wantMsg    string
		wantStderr string
	}{
		{
			name:   "without --tui nothing here is refused",
			flags:  gen.DiffFlags{Watch: true},
			src:    stdin,
			format: outputJSON,
		},
		{
			name:   "--tui at a terminal over the working tree runs",
			flags:  gen.DiffFlags{Tui: true},
			src:    workingTree,
			format: outputText,
			term:   atTerminal,
		},
		{
			name:   "--generated composes: it only sets the initial fold",
			flags:  gen.DiffFlags{Tui: true, Generated: true},
			src:    workingTree,
			format: outputText,
			term:   atTerminal,
		},
		{
			name:    "a patch file has no working tree to coordinate over",
			flags:   gen.DiffFlags{Tui: true},
			src:     patchFile,
			format:  outputText,
			term:    atTerminal,
			wantMsg: "--tui reads the working tree, so it cannot be combined with the patch in x.patch",
		},
		{
			name:    "stdin, same reason and named the same way",
			flags:   gen.DiffFlags{Tui: true},
			src:     stdin,
			format:  outputText,
			term:    atTerminal,
			wantMsg: "--tui reads the working tree, so it cannot be combined with a patch on stdin",
		},
		{
			name:    "--watch and --tui both own the terminal",
			flags:   gen.DiffFlags{Tui: true, Watch: true},
			src:     workingTree,
			format:  outputText,
			term:    atTerminal,
			wantMsg: "--tui and --watch both drive the terminal",
		},
		{
			name:    "a machine-readable format cannot be answered with a viewport",
			flags:   gen.DiffFlags{Tui: true},
			src:     workingTree,
			format:  outputJSON,
			term:    atTerminal,
			wantMsg: "cannot be combined with -o json",
		},
		{
			name:       "no terminal names the command that works here",
			flags:      gen.DiffFlags{Tui: true},
			src:        workingTree,
			format:     outputText,
			wantStderr: "magus: diff --tui requires an interactive terminal; use `magus diff` instead\n",
		},
		{
			// `magus diff --tui > file`. Every flag is fine and stdin is still a keyboard, so
			// the stdout probe is the only thing that can refuse this.
			name:       "a redirected stdout is refused here, not deep inside the viewer",
			flags:      gen.DiffFlags{Tui: true},
			src:        workingTree,
			format:     outputText,
			term:       diffTUITerm{Reads: true},
			wantStderr: "magus: diff --tui requires an interactive terminal; use `magus diff` instead\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := tc.flags
			var err error
			stderr := captureStderr(t, func() {
				err = diffTUIRefusal(&flags, tc.src, tc.format, tc.term)
			})
			assert.Equal(t, tc.wantStderr, stderr)
			if tc.wantMsg == "" && tc.wantStderr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			// Both refusal shapes exit 2: nothing was attempted, so this is a misuse rather
			// than a failure of the work.
			assert.Equal(t, exitUsage, exitCodeOf(err))
			if tc.wantMsg != "" {
				assert.Contains(t, err.Error(), tc.wantMsg)
			}
		})
	}
}

// TestDiffTUIFilesJoinKeepsTheAnnotationOrder guards the one thing this join must not do:
// re-derive an order. The patch arrives in whatever order the VCS emitted and the
// annotations arrive in reading order, so following the patch would quietly undo
// SortForReading and put a lockfile back at the top.
func TestDiffTUIFilesJoinKeepsTheAnnotationOrder(t *testing.T) {
	reach := 12
	rev := types.Diff{Files: []types.DiffFile{
		{Path: "core.go", Role: types.DiffRoleSource, Surface: types.DiffSurfaceInternal, Reach: &reach, Project: "root"},
		{Path: "gen/out.json", Role: types.DiffRoleOutput, Surface: types.DiffSurfaceUnknown},
	}}
	patch := "diff --git a/gen/out.json b/gen/out.json\n" +
		"@@ -1 +1 @@\n" +
		"-{}\n" +
		"+{\"a\":1}\n" +
		"diff --git a/core.go b/core.go\n" +
		"@@ -3 +3 @@\n" +
		"+func F() {}\n" +
		"@@ -9 +9 @@\n" +
		"+func G() {}\n"

	files := diffTUIFiles(rev, diff.ParseHunks(patch))
	require.Len(t, files, 2)
	assert.Equal(t, "core.go", files[0].Path)
	assert.False(t, files[0].Generated)
	assert.Equal(t, []string{"12 files reference its widest changed symbol", "in root"}, files[0].Facts)
	require.Len(t, files[0].Hunks, 2)
	assert.Equal(t, "@@ -3 +3 @@", files[0].Hunks[0].Header)
	assert.NotEmpty(t, files[0].Hunks[0].Digest, "the viewed set is keyed by this")
	// The patch coordinate a comment is anchored by, carried rather than re-derived: dropping
	// it here would hand the viewer a second hunk numbered zero.
	assert.Equal(t, 1, files[0].Hunks[1].Index)

	assert.Equal(t, "gen/out.json", files[1].Path)
	assert.True(t, files[1].Generated)
	require.Len(t, files[1].Hunks, 1)
}

func TestDiffCountsLineIsWhatTheReaderIsLeftWith(t *testing.T) {
	rev := types.Diff{
		Files: []types.DiffFile{
			{Path: "a.go", Role: types.DiffRoleSource},
			{Path: "b.go", Role: types.DiffRoleSource},
			{Path: "gen/out.json", Role: types.DiffRoleOutput},
		},
		SeedProjects:     []string{"root"},
		AffectedProjects: []types.ImpactProject{{Path: "root"}, {Path: "docs"}},
	}
	assert.Equal(t, "2 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild",
		diffCountsLine(rev, false))
	// Under --generated the same files are printed right below this line, so calling them
	// folded contradicts the page it introduces.
	assert.Equal(t, "2 files to read, 1 generated shown; 1 projects edited, 2 projects rebuild",
		diffCountsLine(rev, true))
	assert.Equal(t, "0 files to read", diffCountsLine(types.Diff{}, false))
	assert.Equal(t, "0 files to read", diffCountsLine(types.Diff{}, true),
		"with nothing generated there is no clause either way")
}

// stubDiffSession is the daemon's /api/v1/diff/session route, holding every request until the
// test lets go.
//
// The holding is what makes the queue observable at all: a daemon that answers empties the
// queue as fast as a keyboard can fill it, and every decision in diffBridge is about the case
// where one has stopped.
type stubDiffSession struct {
	srv     *httptest.Server
	started chan struct{} // closed once the first request has reached the handler
	release chan struct{} // closed to let every held request return

	startOnce   sync.Once
	releaseOnce sync.Once

	mu  sync.Mutex
	got []diffSessionOp
}

func newStubDiffSession(t *testing.T) *stubDiffSession {
	t.Helper()
	s := &stubDiffSession{started: make(chan struct{}), release: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var op diffSessionOp
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
func (s *stubDiffSession) unblock() { s.releaseOnce.Do(func() { close(s.release) }) }

// bridge is a diffBridge pointed at the stub, skipping the attach round trip the queue has no
// part in.
func (s *stubDiffSession) bridge() *diffBridge {
	b := &diffBridge{addr: strings.TrimPrefix(s.srv.URL, "http://"), token: "stub"}
	b.start()
	return b
}

func (s *stubDiffSession) ops() []diffSessionOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]diffSessionOp(nil), s.got...)
}

func cursorHunks(ops []diffSessionOp) []int {
	var out []int
	for _, op := range ops {
		if op.Op == "cursor" {
			out = append(out, op.Hunk)
		}
	}
	return out
}

func viewedDigests(ops []diffSessionOp) []string {
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
// A backlog means the daemon stopped keeping up, and the only cursor still worth sending is the
// last one - every entry behind it names somewhere the reader has already walked past. A plain
// non-blocking send drops the arriving op, which keeps exactly the stale ones and throws away
// the only true one.
func TestDiffBridgeQueueKeepsTheNewestCursor(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	// The first op is taken by the sender and held in the handler, which is what makes the rest
	// deterministic: nothing is receiving from the queue while they are pushed.
	b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: 0})
	<-stub.started

	const pushed = 20
	for i := 1; i <= pushed; i++ {
		b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: i})
	}
	stub.unblock()
	b.close()

	want := []int{0}
	for i := pushed - diffBridgeQueue + 1; i <= pushed; i++ {
		want = append(want, i)
	}
	assert.Equal(t, want, cursorHunks(stub.ops()),
		"the one in flight, then the newest diffBridgeQueue: the stale head is what gets evicted")
}

// TestDiffBridgeNeverDropsAReadMark pins the asymmetry the two queues exist for.
//
// A cursor is replaceable by the next one. A read mark is replaceable by nothing: the bridge is
// the only writer of it on this path, so a dropped `viewed` is a hunk the reader read that no
// client will ever be told about. Sharing one evicting queue made a fast reader's marks
// collateral damage of their own scrolling.
func TestDiffBridgeNeverDropsAReadMark(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: 0})
	<-stub.started

	// Far more cursor moves than the cursor queue holds, with marks interleaved: the overflow
	// that evicts one must not be able to reach the other.
	var want []string
	for i := 0; i < 40; i++ {
		b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: i + 1})
		if i%8 == 0 {
			d := fmt.Sprintf("digest-%d", i)
			want = append(want, d)
			b.SetViewed(d, true)
		}
	}
	stub.unblock()
	b.close()

	assert.Equal(t, want, viewedDigests(stub.ops()),
		"every mark, in order, however far behind the sender fell")
}

// TestDiffBridgeCloseDrainsAndTheSenderExits pins the quit path: a mark made on the last
// keypress leaves, and the goroutine that carried it is gone by the time close returns.
func TestDiffBridgeCloseDrainsAndTheSenderExits(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	b.SetViewed("d0", true)
	<-stub.started
	b.SetViewed("d1", true)
	b.SetViewed("d2", false)
	stub.unblock()

	b.close()
	select {
	case <-b.done:
	default:
		t.Fatal("the sender goroutine is still running after close returned")
	}
	assert.Equal(t, []string{"d0", "d1", "d2"}, viewedDigests(stub.ops()),
		"a mark made on the last keypress is not lost to the process exiting")
}

// TestDiffBridgeCloseGivesUpOnAWedgedDaemon is the other half of the same bargain: quitting is
// bounded whether or not anything answers, and the sender still exits.
//
// Nothing releases the handler here, so the post is on the wire for the whole test. Cancelling
// it is the only thing that ends the goroutine; abandoning it left one talking to the daemon
// about a session the reader had walked away from, with nobody to receive the answer.
func TestDiffBridgeCloseGivesUpOnAWedgedDaemon(t *testing.T) {
	stub := newStubDiffSession(t)
	b := stub.bridge()

	b.SetViewed("d0", true)
	<-stub.started
	b.SetViewed("d1", true)

	start := time.Now()
	b.close()
	assert.Less(t, time.Since(start), diffBridgeClose+2*diffBridgeWrite,
		"a wedged daemon must not hold the shell")

	select {
	case <-b.done:
	case <-time.After(2 * diffBridgeWrite):
		t.Fatal("the sender goroutine outlived close")
	}
}

// TestDiffBridgeSendAfterCloseIsSafe pins why stop is closed instead of the queues: closing a
// channel the key loop can still send on turns a late keypress into a panic.
func TestDiffBridgeSendAfterCloseIsSafe(t *testing.T) {
	stub := newStubDiffSession(t)
	stub.unblock() // an answering daemon; this is about the queue, not the wire
	b := stub.bridge()

	b.SetViewed("d0", true)
	b.close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.SetCursor(types.DiffCursor{Path: "a.go", Hunk: 1})
		b.SetViewed("d1", true)
	}()
	select {
	case <-done:
	case <-time.After(2 * diffBridgeWrite):
		t.Fatal("a send after close blocked")
	}
}
