package cache

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedReader delivers first IN FULL (io.Copy's internal buffer is far smaller
// than first, so this takes several Read calls), then blocks - closing waiting
// so a test can observe the pause - until proceed is closed, then delivers
// second in full, then io.EOF. It gives a test deterministic control over
// exactly how much of a PutArtifact push has landed on disk before a second,
// competing push runs - no wall-clock race needed.
type gatedReader struct {
	first, second []byte
	proceed       chan struct{}
	waiting       chan struct{}
	pos           int
	state         int // 0 = delivering first, 1 = waiting, 2 = delivering second, 3 = EOF
}

func (r *gatedReader) Read(p []byte) (int, error) {
	switch r.state {
	case 0:
		n := copy(p, r.first[r.pos:])
		r.pos += n
		if r.pos >= len(r.first) {
			r.state, r.pos = 1, 0
		}
		return n, nil
	case 1:
		close(r.waiting)
		<-r.proceed
		r.state = 2
		return r.Read(p)
	case 2:
		n := copy(p, r.second[r.pos:])
		r.pos += n
		if r.pos >= len(r.second) {
			r.state = 3
		}
		return n, nil
	default:
		return 0, io.EOF
	}
}

// TestFSRemoteBackendPutArtifactConcurrentPushesDontTear verifies two pushes to
// the SAME (project, hash) that genuinely overlap in time never tear the stored
// artifact. Before the fix, PutArtifact staged through a FIXED temp path
// (artifactPath + ".tmp"): a second push's os.Create truncates the SAME inode
// the first push's file descriptor is still writing through (O_TRUNC on an
// existing path reuses the inode, it does not create a new one), so the first
// push's later writes land on a file that is already midway through being
// rewritten by the second push, and whichever push renames last can carry the
// other's leftover bytes into the shared store.
//
// gatedReader pins the interleaving deterministically instead of racing wall-clock
// timing: push A writes half its payload, then blocks before writing the rest;
// the test lets push B (a distinct, complete payload) run to completion in that
// window; then push A is unblocked to finish. The store must end up holding
// exactly one push's complete, untorn payload.
func TestFSRemoteBackendPutArtifactConcurrentPushesDontTear(t *testing.T) {
	r, err := NewFSRemoteBackend(t.TempDir())
	require.NoError(t, err)

	const half = 8 << 20 // 8 MiB per half: large enough that a shared-inode overwrite is not a no-op
	payloadA := bytes.Repeat([]byte{0xAA}, 2*half)
	payloadB := bytes.Repeat([]byte{0xBB}, 3*half) // deliberately a different size than A

	readerA := &gatedReader{
		first:   payloadA[:half],
		second:  payloadA[half:],
		proceed: make(chan struct{}),
		waiting: make(chan struct{}),
	}

	doneA := make(chan error, 1)
	go func() {
		doneA <- r.PutArtifact(context.Background(), "proj", "deadbeef", readerA)
	}()

	<-readerA.waiting // push A has written its first half and is paused mid-push

	require.NoError(t, r.PutArtifact(context.Background(), "proj", "deadbeef", bytes.NewReader(payloadB)),
		"push B, fully overlapping push A's pause, must complete cleanly")

	close(readerA.proceed) // let push A finish writing and attempt its rename
	<-doneA                // its own error (if any - e.g. a stale temp name) is not the point; the stored bytes are

	got, err := os.ReadFile(r.artifactPath("proj", "deadbeef"))
	require.NoError(t, err, "an artifact must exist after both pushes")

	isA := bytes.Equal(got, payloadA)
	isB := bytes.Equal(got, payloadB)
	assert.True(t, isA || isB,
		"the stored artifact must be exactly one push's complete payload (got %d bytes), never a mix of both", len(got))
}
